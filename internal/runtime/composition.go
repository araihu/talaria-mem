package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/adapters/codex"
	"github.com/guilhermecastro/talaria-mem/internal/adapters/filesystem"
	httpadapter "github.com/guilhermecastro/talaria-mem/internal/adapters/http"
	"github.com/guilhermecastro/talaria-mem/internal/adapters/mcp"
	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite"
	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/cli"
	"github.com/guilhermecastro/talaria-mem/internal/cli/commands"
	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
	"github.com/guilhermecastro/talaria-mem/internal/maintenance"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/projection"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/guilhermecastro/talaria-mem/internal/scanner"
	"github.com/guilhermecastro/talaria-mem/internal/security"
	"github.com/guilhermecastro/talaria-mem/internal/workspace"
)

const DefaultAddress = "127.0.0.1:7437"

type Config struct {
	Environment    lifecycle.Environment
	Address        string
	Stdout         io.Writer
	Stderr         io.Writer
	BinaryPath     string
	CodexHooksPath string
}

// RunHelp builds the same Cobra command tree as the runtime without opening
// directories, credentials, SQLite, listeners, or background workers.
func RunHelp(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	registry := cli.NewRegistry()
	if err := commands.RegisterDB(registry, commands.NewDBCommands(nil, nil)); err != nil {
		return err
	}
	if err := commands.RegisterDaemon(registry, commands.NewDaemonCommands(nil)); err != nil {
		return err
	}
	if err := commands.RegisterDoctor(registry, commands.NewDoctorCommands(nil)); err != nil {
		return err
	}
	if err := commands.RegisterSetup(registry, commands.NewSetupCommands(nil, lifecycle.SetupRequest{})); err != nil {
		return err
	}
	if err := commands.RegisterStatus(registry, commands.NewStatusCommands(nil)); err != nil {
		return err
	}
	if err := commands.RegisterToken(registry, commands.NewTokenCommands(nil)); err != nil {
		return err
	}
	if err := commands.RegisterScanner(registry, &commands.ScannerCommands{}); err != nil {
		return err
	}
	if err := commands.RegisterMCP(registry, commands.NewMCPCommands(nil)); err != nil {
		return err
	}
	return cli.NewRoot(cli.RootConfig{Registry: registry, Stdout: stdout, Stderr: stderr}).Run(ctx, args)
}

type Composition struct {
	Environment lifecycle.Environment
	Address     string
	Root        *cli.Root
	Daemon      *lifecycle.Daemon
	HTTP        *httpadapter.Server
	MCP         *mcp.Server
	DB          *sqlite.DB
	Memory      *application.MemoryService
	Searcher    *retrieval.Searcher
	Projector   *projection.Worker
	Readiness   *lifecycle.Readiness
	Setup       *lifecycle.SetupService
	Doctor      *lifecycle.Doctor
	Status      *lifecycle.StatusService
	Token       *lifecycle.TokenService
	closeOnce   sync.Once
}

// New constructs one explicit runtime graph.  All concrete providers are
// created here, never through package init registration or a second binary.
func New(ctx context.Context, configuration Config) (*Composition, error) {
	environment, err := resolveEnvironment(configuration.Environment)
	if err != nil {
		return nil, err
	}
	if err := environment.EnsureDirectories(); err != nil {
		return nil, err
	}
	address := configuration.Address
	if address == "" {
		address = DefaultAddress
	}
	if configuration.Stdout == nil {
		configuration.Stdout = io.Discard
	}
	if configuration.Stderr == nil {
		configuration.Stderr = io.Discard
	}
	rootKeyPath := filepath.Join(environment.ConfigDir, "root.key")
	tokenPath := filepath.Join(environment.ConfigDir, "token")
	rootKey, err := security.LoadRootKey(rootKeyPath)
	if err != nil {
		return nil, err
	}
	if _, err := security.LoadBearerToken(tokenPath); err != nil {
		return nil, err
	}
	deriver, err := security.NewHKDFDeriver(rootKey)
	if err != nil {
		return nil, err
	}
	clock := wallClock{}
	scan, err := scanner.New(scanner.Config{Generation: scanner.ReviewedRuleGeneration, Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	databasePath := lifecycle.DatabasePath(environment.StateDir)
	policy := filesystem.NewPathPolicy()
	lockPath := filepath.Join(environment.StateDir, ".maintenance.lock")
	projectionDir := filepath.Join(environment.StateDir, "projections")
	if err := filesystem.EnsureManagedDirectory(projectionDir); err != nil {
		return nil, err
	}
	var database *sqlite.DB
	if err := maintenance.WithLock(ctx, lockPath, func(ctx context.Context) error {
		var openErr error
		database, openErr = sqlite.OpenForMaintenance(ctx, databasePath, policy, prepareDatabase)
		return openErr
	}); err != nil {
		return nil, err
	}
	repository := sqlite.NewRepository(database)
	memory := application.NewMemoryService(repository, scan, clock, deriver)
	usage := retrieval.NewUsageLedger(clock, deriver)
	index := sqlite.NewIndex(database)
	searcher := retrieval.NewSearcher(index, usage, memory.Guard, clock)
	projectionStore := sqlite.NewProjectionStore(database, projectionDir)
	projector := projection.NewWorker(projectionStore, projectionStore, filesystem.NewManagedFileStore(policy), memory.Guard, clock)

	activation, err := maintenance.NewFileActivationJournal(filepath.Join(environment.StateDir, "activation.json"), maintenance.DefaultActivationRecord())
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	receiptStore, err := maintenance.NewReceiptStoreFromDeriver(ctx, environment.StateDir, deriver)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	backupKey, err := deriver.DeriveKey(ctx, ports.KeyPurposeBackupManifest, ports.KeyDerivationVersion)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	backup, err := maintenance.NewBackupManager(environment.BackupDir, backupKey, clock)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	backup.SetReceiptStore(receiptStore).SetLockPath(lockPath)
	readiness := lifecycle.NewReadiness(lifecycle.Readiness{
		Storage:    func(context.Context) error { return nil },
		Scanner:    func(context.Context) error { return nil },
		Migration:  func(context.Context) error { return nil },
		Projection: projectionStore.ProjectionReady,
		Inventory: func(ctx context.Context) error {
			inventory, err := backup.Inventory(ctx)
			if err != nil {
				return err
			}
			if !inventory.Complete() {
				return errors.New("backup inventory requires reconciliation")
			}
			return nil
		},
		Activation: activation,
	})
	token, err := security.LoadBearerToken(tokenPath)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	authenticator, err := security.NewAuthenticator(security.AuthConfig{Token: token, Host: address})
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	store := sqlite.NewWorkspaceStore(database)
	resolver := codex.NewBoundWorkspaceResolver(store)
	session := codex.NewService(codex.Config{
		Resolver: resolver,
		Source: codex.CandidateSourceFunc(func(ctx context.Context, workspaceID string) ([]retrieval.Candidate, error) {
			return index.ListSessionStart(ctx, workspaceID)
		}),
		Usage: usage,
		Guard: memory.Guard,
		Clock: clock,
	})
	httpServer, err := httpadapter.NewServer(httpadapter.ServerConfig{Authenticator: authenticator, SessionStart: httpSessionStarter{service: session}, Reader: httpadapter.ServiceReader{Searcher: searcher, Memory: memory}, Guard: memory.Guard, Readiness: readiness})
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	mcpServer, err := mcp.NewServer(mcp.ServerConfig{Authenticator: authenticator, Reader: mcp.ServiceReader{Searcher: searcher, Memory: memory}, Mutator: memory, Guard: memory.Guard})
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpServer.Handler())
	mux.Handle("/mcp/", mcpServer.Handler())
	mux.Handle("/", httpServer.Handler())
	daemon, err := lifecycle.NewDaemon(lifecycle.DaemonConfig{Address: address, Handler: mux, Readiness: readiness})
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	status := lifecycle.NewStatusService(readiness, databasePath)
	status.Freelist = database.FreelistPages
	ftsReceipts, err := lifecycle.NewFileFTSRepairReceiptStore(receiptStore.Dir, backupKey)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	doctor := lifecycle.NewDoctor(lifecycle.DoctorConfig{
		Environment:  environment,
		Readiness:    readiness,
		FTS:          sqliteFTSRepairer{database: database},
		Receipts:     ftsReceipts,
		RootKeyPath:  rootKeyPath,
		TokenPath:    tokenPath,
		DatabasePath: databasePath,
		WALPath:      databasePath + "-wal",
		SHMPath:      databasePath + "-shm",
	})
	setupRequest := lifecycle.SetupRequest{ConfigPath: filepath.Join(environment.ConfigDir, "codex.toml"), HookPath: filepath.Join(environment.ConfigDir, "session-start.sh"), CodexHooksPath: resolveCodexHooksPath(configuration.CodexHooksPath, environment), TokenPath: tokenPath, BinaryPath: configuration.BinaryPath, Endpoint: address}
	if setupRequest.BinaryPath == "" {
		setupRequest.BinaryPath, _ = os.Executable()
	}
	setup := lifecycle.NewSetupService(lifecycle.NewCodexInstaller())
	registry := cli.NewRegistry()
	_ = commands.RegisterDB(registry, commands.NewDBCommands(commands.NewBackupCommands(backup), nil))
	_ = commands.RegisterDaemon(registry, commands.NewDaemonCommands(daemon))
	_ = commands.RegisterDoctor(registry, commands.NewDoctorCommands(doctor))
	_ = commands.RegisterSetup(registry, commands.NewSetupCommands(setup, setupRequest))
	_ = commands.RegisterStatus(registry, commands.NewStatusCommands(status))
	_ = commands.RegisterToken(registry, commands.NewTokenCommands(lifecycle.NewTokenService(tokenPath)))
	_ = commands.RegisterScanner(registry, &commands.ScannerCommands{})
	memoryCore := cli.NewMemoryCore(cli.ServiceClient{Memory: memory, Retrieval: searcher})
	workspaceCommands := cli.NewWorkspaceCommands(cli.StoreClient{Store: store, Resolver: workspace.NewResolver(store, func() time.Time { return clock.Now() }), Binder: workspace.NewBinder(store, func() time.Time { return clock.Now() }), Merge: workspace.NewMergeService(store, func() time.Time { return clock.Now() }), ListFn: store.ListWorkspaces, Clock: func() time.Time { return clock.Now() }})
	root := cli.NewRoot(cli.RootConfig{Registry: registry, Memory: memoryCore, Workspace: workspaceCommands, Projection: cli.NewProjectionCommands(projector), Stdout: configuration.Stdout, Stderr: configuration.Stderr})
	return &Composition{Environment: environment, Address: address, Root: root, Daemon: daemon, HTTP: httpServer, MCP: mcpServer, DB: database, Memory: memory, Searcher: searcher, Projector: projector, Readiness: readiness, Setup: setup, Doctor: doctor, Status: status, Token: lifecycle.NewTokenService(tokenPath)}, nil
}

// RunSetup composes only the first-install command graph. It deliberately
// avoids opening SQLite, creating a root key, or creating a bearer token for a
// dry-run. Apply creates installation credentials before writing the hook or
// registering it with Codex.
func RunSetup(ctx context.Context, args []string, configuration Config) error {
	environment, err := resolveEnvironmentForSetup(configuration.Environment)
	if err != nil {
		return err
	}
	if err := environment.EnsureDirectoriesForSetup(); err != nil {
		return err
	}
	if configuration.Stdout == nil {
		configuration.Stdout = io.Discard
	}
	if configuration.Stderr == nil {
		configuration.Stderr = io.Discard
	}
	binaryPath := configuration.BinaryPath
	if binaryPath == "" {
		binaryPath, _ = os.Executable()
	}
	request := lifecycle.SetupRequest{
		ConfigPath:     filepath.Join(environment.ConfigDir, "codex.toml"),
		HookPath:       filepath.Join(environment.ConfigDir, "session-start.sh"),
		CodexHooksPath: resolveCodexHooksPath(configuration.CodexHooksPath, environment),
		TokenPath:      filepath.Join(environment.ConfigDir, "token"),
		BinaryPath:     binaryPath,
		Endpoint:       configuration.Address,
	}
	if request.Endpoint == "" {
		request.Endpoint = DefaultAddress
	}
	if setupApplyRequested(args) && !setupRemoveRequested(args) {
		if _, err := ensureInstallation(request.TokenPath, filepath.Join(environment.ConfigDir, "root.key")); err != nil {
			return err
		}
	}
	setup := lifecycle.NewSetupService(lifecycle.NewCodexInstaller())
	registry := cli.NewRegistry()
	if err := commands.RegisterSetup(registry, commands.NewSetupCommands(setup, request)); err != nil {
		return err
	}
	root := cli.NewRoot(cli.RootConfig{Registry: registry, Stdout: configuration.Stdout, Stderr: configuration.Stderr})
	return root.Run(ctx, args)
}

func resolveCodexHooksPath(override string, environment lifecycle.Environment) string {
	if override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	defaultEnvironment, err := lifecycle.DefaultEnvironment(home)
	if err != nil || filepath.Clean(defaultEnvironment.ConfigDir) != filepath.Clean(environment.ConfigDir) {
		// Isolated setup fixtures must opt in explicitly. This prevents tests and
		// alternate roots from mutating the operator's real Codex configuration.
		return ""
	}
	return filepath.Join(home, ".codex", "hooks.json")
}

func (composition *Composition) Run(ctx context.Context, args []string) error {
	if composition == nil || composition.Root == nil {
		return errors.New("runtime composition unavailable")
	}
	return composition.Root.Run(ctx, args)
}

func (composition *Composition) Close() error {
	if composition == nil {
		return nil
	}
	var err error
	composition.closeOnce.Do(func() {
		if composition.Daemon != nil {
			err = composition.Daemon.Stop(context.Background())
		}
		if composition.DB != nil {
			err = errors.Join(err, composition.DB.Close())
		}
	})
	return err
}

func resolveEnvironment(environment lifecycle.Environment) (lifecycle.Environment, error) {
	if environment.StateDir != "" || environment.ConfigDir != "" || environment.BackupDir != "" {
		return environment, environment.Validate()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return lifecycle.Environment{}, err
	}
	environment, err = lifecycle.DefaultEnvironment(home)
	if err != nil {
		return lifecycle.Environment{}, err
	}
	return environment, nil
}

func resolveEnvironmentForSetup(environment lifecycle.Environment) (lifecycle.Environment, error) {
	if environment.StateDir != "" || environment.ConfigDir != "" || environment.BackupDir != "" {
		if err := environment.ValidatePaths(); err != nil {
			return lifecycle.Environment{}, err
		}
		return environment, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return lifecycle.Environment{}, err
	}
	return lifecycle.DefaultEnvironment(home)
}

func prepareDatabase(path string, policy ports.ManagedPathPolicy) error {
	if err := policy.ValidateManagedPathParent(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return createErr
		}
		if syncErr := file.Sync(); syncErr != nil {
			_ = file.Close()
			return syncErr
		}
		if closeErr := file.Close(); closeErr != nil {
			return closeErr
		}
	}
	return policy.ValidateManagedPath(path, 0o600)
}

func ensureInstallation(tokenPath, rootKeyPath string) (security.RootKey, error) {
	rootKey, err := security.LoadRootKey(rootKeyPath)
	if errors.Is(err, security.ErrRootKeyMissing) {
		rootKey, err = security.CreateRootKey(rootKeyPath)
	}
	if err != nil {
		return security.RootKey{}, err
	}
	if _, err := security.LoadBearerToken(tokenPath); errors.Is(err, security.ErrTokenMissing) {
		if _, createErr := security.CreateBearerToken(tokenPath); createErr != nil {
			return security.RootKey{}, createErr
		}
	} else if err != nil {
		return security.RootKey{}, err
	}
	return rootKey, nil
}

func setupApplyRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--apply" {
			return true
		}
	}
	return false
}

func setupRemoveRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--remove" {
			return true
		}
	}
	return false
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

var _ ports.Clock = wallClock{}

type httpSessionStarter struct{ service *codex.Service }

func (starter httpSessionStarter) SessionStart(ctx context.Context, request httpadapter.SessionStartRequest) (httpadapter.SessionStartResponse, error) {
	if starter.service == nil {
		return httpadapter.SessionStartResponse{}, errors.New("session start unavailable")
	}
	response, err := starter.service.SessionStart(ctx, codex.Request{SessionID: request.SessionId, HookName: string(request.HookEventName), WorkingDirectory: request.Cwd})
	if err != nil {
		return httpadapter.SessionStartResponse{}, err
	}
	items := make([]httpadapter.MemoryItem, 0, len(response.Items))
	for _, item := range response.Items {
		memoryID, err := uuid.Parse(item.MemoryID)
		if err != nil {
			return httpadapter.SessionStartResponse{}, err
		}
		revisionID, err := uuid.Parse(item.RevisionID)
		if err != nil {
			return httpadapter.SessionStartResponse{}, err
		}
		items = append(items, httpadapter.MemoryItem{MemoryId: memoryID, RevisionId: revisionID, WorkspaceId: item.WorkspaceID, Kind: httpadapter.MemoryItemKind(item.Kind), Trust: httpadapter.MemoryItemTrust(item.Trust), Lifecycle: httpadapter.MemoryItemLifecycle(item.Lifecycle), Title: item.Title, Content: item.Content, Tags: append([]string(nil), item.Tags...)})
	}
	var warning *string
	if response.Warning != "" {
		value := response.Warning
		warning = &value
	}
	return httpadapter.SessionStartResponse{Version: httpadapter.SessionStartResponseVersion(httpadapter.SessionStartVersion), WorkspaceId: response.WorkspaceID, Items: items, Included: response.Included, Omitted: response.Omitted, Warning: warning}, nil
}
