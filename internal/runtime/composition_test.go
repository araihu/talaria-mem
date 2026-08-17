package runtime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite"
	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
	"github.com/guilhermecastro/talaria-mem/internal/security"
	"github.com/guilhermecastro/talaria-mem/internal/workspace"
)

func TestCompositionBuildsOneExplicitRuntimeGraph(t *testing.T) {
	environment := testEnvironment(t)
	if _, err := security.CreateRootKey(filepath.Join(environment.ConfigDir, "root.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := security.CreateBearerToken(filepath.Join(environment.ConfigDir, "token")); err != nil {
		t.Fatal(err)
	}
	composition, err := New(context.Background(), Config{Environment: environment, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = composition.Close() })
	if composition.Root == nil || composition.Daemon == nil || composition.HTTP == nil || composition.MCP == nil || composition.Projector == nil {
		t.Fatalf("incomplete graph: root=%v daemon=%v http=%v mcp=%v projector=%v", composition.Root != nil, composition.Daemon != nil, composition.HTTP != nil, composition.MCP != nil, composition.Projector != nil)
	}
	for _, name := range []string{"daemon", "doctor", "setup", "status", "token"} {
		if _, ok := composition.Root.Registry().Lookup(name); !ok {
			t.Fatalf("command %q not registered", name)
		}
	}
	report, err := composition.Doctor.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.FTS == nil || report.FTS.Tokenizer == "" || !report.FTS.RowsMatch || !report.FTS.HashesMatch {
		t.Fatalf("unexpected FTS diagnostic: %+v", report.FTS)
	}
	dryRun, err := composition.Doctor.RepairFTS(context.Background(), lifecycle.FTSRepairRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Receipt == "" {
		t.Fatal("FTS dry-run returned no receipt")
	}
	if _, err := composition.Doctor.RepairFTS(context.Background(), lifecycle.FTSRepairRequest{Receipt: dryRun.Receipt}); err != nil {
		t.Fatal(err)
	}
}

func TestCompositionRequiresExistingRootKeyAndToken(t *testing.T) {
	environment := testEnvironment(t)
	if _, err := New(context.Background(), Config{Environment: environment}); err == nil {
		t.Fatalf("missing installation credentials accepted: %v", err)
	}
}

func TestCompositionSessionStartListsBoundVerifiedMemory(t *testing.T) {
	environment := testEnvironment(t)
	if _, err := security.CreateRootKey(filepath.Join(environment.ConfigDir, "root.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := security.CreateBearerToken(filepath.Join(environment.ConfigDir, "token")); err != nil {
		t.Fatal(err)
	}
	composition, err := New(context.Background(), Config{Environment: environment, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = composition.Close() })

	ctx := context.Background()
	store := sqlite.NewWorkspaceStore(composition.DB)
	workspaceID := "018f1f61-7b5c-7abc-8def-1123456789ab"
	if err := store.CreateWorkspace(ctx, domain.Workspace{ID: workspaceID, Name: "repo"}); err != nil {
		t.Fatal(err)
	}
	workingDirectory := t.TempDir()
	if _, err := workspace.NewBinder(store, nil).Bind(ctx, "path:"+workingDirectory, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := composition.Memory.Create(ctx, application.MutationRequest{
		Actor:          application.ActorCLI,
		Caller:         "composition-test",
		WorkspaceID:    workspaceID,
		IdempotencyKey: "session-start-memory",
		Kind:           domain.MemoryKindState,
		Title:          "session title",
		Content:        "session content",
		Verified:       true,
		Provenance:     domain.Provenance{Actor: "composition-test", Source: "test"},
	}); err != nil {
		t.Fatal(err)
	}
	token, err := security.LoadBearerToken(filepath.Join(environment.ConfigDir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://"+composition.Address+"/control/v1/session-start", strings.NewReader(`{"event_id":"event-1","session_id":"session-1","hook_name":"SessionStart","working_directory":"`+workingDirectory+`"}`))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = composition.Address
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	composition.HTTP.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("session-start status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "session title") {
		t.Fatalf("session-start omitted verified memory: %s", recorder.Body.String())
	}
}

func TestRunSetupDryRunCreatesOnlyManagedDirectoriesOnFirstInstall(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	environment := lifecycle.Environment{
		StateDir:  filepath.Join(root, "state"),
		ConfigDir: filepath.Join(root, "config"),
		BackupDir: filepath.Join(root, "state", "backups"),
	}
	var stdout bytes.Buffer
	if err := RunSetup(context.Background(), []string{"setup", "codex", "--dry-run"}, Config{Environment: environment, Stdout: &stdout, Stderr: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{environment.StateDir, environment.ConfigDir, environment.BackupDir} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("managed directory %s: %v", path, err)
		}
		if info.Mode().Perm() != lifecycle.ManagedDirectoryMode.Perm() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unsafe managed directory %s: mode=%o symlink=%t", path, info.Mode().Perm(), info.Mode()&os.ModeSymlink != 0)
		}
	}
	for _, path := range []string{filepath.Join(environment.ConfigDir, "root.key"), filepath.Join(environment.ConfigDir, "token"), lifecycle.DatabasePath(environment.StateDir)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s: err=%v", path, err)
		}
	}
}

func TestRunSetupDryRunCreatesPrivateRootOnCleanHome(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	environment, err := lifecycle.DefaultEnvironment(home)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := RunSetup(context.Background(), []string{"setup", "codex", "--dry-run"}, Config{Environment: environment, Stdout: &stdout, Stderr: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, ".talaria-mem"), environment.StateDir, environment.ConfigDir, environment.BackupDir} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("managed path %s: %v", path, err)
		}
		if info.Mode().Perm() != lifecycle.ManagedDirectoryMode.Perm() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unsafe managed path %s: mode=%o symlink=%t", path, info.Mode().Perm(), info.Mode()&os.ModeSymlink != 0)
		}
	}
	if _, err := os.Lstat(filepath.Join(environment.ConfigDir, "session-start.sh")); !os.IsNotExist(err) {
		t.Fatalf("dry-run installed hook: err=%v", err)
	}
}

func TestRunSetupApplyInstallsHookAndCredentials(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	environment := lifecycle.Environment{StateDir: filepath.Join(root, "state"), ConfigDir: filepath.Join(root, "config"), BackupDir: filepath.Join(root, "state", "backups")}
	var stdout bytes.Buffer
	if err := RunSetup(context.Background(), []string{"setup", "codex", "--apply"}, Config{Environment: environment, BinaryPath: "/tmp/talaria-mem", Stdout: &stdout, Stderr: &stdout}); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(environment.ConfigDir, "session-start.sh")
	info, err := os.Lstat(hook)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("hook mode=%o symlink=%t", info.Mode().Perm(), info.Mode()&os.ModeSymlink != 0)
	}
	for _, path := range []string{filepath.Join(environment.ConfigDir, "root.key"), filepath.Join(environment.ConfigDir, "token")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("credential %s: %v", path, err)
		}
	}
}

func testEnvironment(t *testing.T) lifecycle.Environment {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	config := filepath.Join(root, "config")
	backup := filepath.Join(state, "backups")
	for _, path := range []string{state, config, backup} {
		if err := os.Mkdir(path, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	return lifecycle.Environment{StateDir: state, ConfigDir: config, BackupDir: backup}
}
