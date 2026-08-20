package lifecycle

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/filesystem"
)

//go:embed assets/codex-hook.sh
var codexHook []byte

//go:embed assets/session-start.sh
var sessionStartHook []byte

//go:embed assets/session-start-v1.sh
var legacySessionStartHook []byte

// CodexHookInstaller provisions only the executable SessionStart artifact.
// Platform daemon lifecycle remains a separate, explicit operation; setup
// never starts a process or invokes a host service manager.
type CodexHookInstaller struct{}

func NewCodexHookInstaller() *CodexHookInstaller { return &CodexHookInstaller{} }

func (installer *CodexHookInstaller) Install(ctx context.Context, request SetupRequest, _ string) error {
	if installer == nil {
		return errors.New("codex hook installer unavailable")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateHookPath(request.HookPath); err != nil {
		return err
	}
	hook, err := renderSessionStartHook(request)
	if err != nil {
		return err
	}
	info, err := os.Lstat(request.HookPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !currentUserOwns(info) {
			return errors.New("Codex hook path is unsafe")
		}
		value, readErr := os.ReadFile(request.HookPath)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(value, legacySessionStartHook) {
			return upgradeManagedHook(request.HookPath, legacySessionStartHook, hook)
		}
		if !bytes.Equal(value, hook) {
			return ErrSetupCollision
		}
		if err := os.Chmod(request.HookPath, 0o700); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := filesystem.AtomicWriteNoFollow(ctx, request.HookPath, hook); err != nil {
		return err
	}
	if err := os.Chmod(request.HookPath, 0o700); err != nil {
		_ = os.Remove(request.HookPath)
		return err
	}
	return nil
}

func (installer *CodexHookInstaller) Remove(ctx context.Context, request SetupRequest, _ string) error {
	if installer == nil {
		return errors.New("codex hook installer unavailable")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateHookPath(request.HookPath); err != nil {
		return err
	}
	hook, err := renderSessionStartHook(request)
	if err != nil {
		return err
	}
	info, err := os.Lstat(request.HookPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !currentUserOwns(info) {
		return errors.New("Codex hook path is unsafe")
	}
	value, err := os.ReadFile(request.HookPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(value, hook) && !bytes.Equal(value, legacySessionStartHook) {
		return ErrSetupCollision
	}
	return os.Remove(request.HookPath)
}

// CodexInstaller is the consumer-facing integration. It composes the private
// executable hook with the official Codex hooks registry. The registry write
// is prepared and committed before the hook; if the hook fails, the registry
// is restored only when it still contains the exact bytes written by Talaria.
type CodexInstaller struct {
	hook      *CodexHookInstaller
	hooks     *CodexHooksInstaller
	mcp       *CodexMCPInstaller
	providers *ProviderConfigInstaller
}

type codexHookSnapshot struct {
	exists bool
	value  []byte
	mode   fs.FileMode
}

func NewCodexInstaller() *CodexInstaller {
	return &CodexInstaller{hook: NewCodexHookInstaller(), hooks: NewCodexHooksInstaller(), mcp: NewCodexMCPInstaller(), providers: NewProviderConfigInstaller()}
}

func (installer *CodexInstaller) Plan(ctx context.Context, request SetupRequest, fingerprint string, remove bool) ([]SetupChange, error) {
	if installer == nil || installer.hooks == nil {
		return nil, errors.New("Codex installer unavailable")
	}
	changes, err := installer.hooks.Plan(ctx, request, fingerprint, remove)
	if err != nil {
		return nil, err
	}
	if installer.mcp != nil {
		mcpChanges, err := installer.mcp.Plan(ctx, request, fingerprint, remove)
		if err != nil {
			return nil, err
		}
		changes = append(changes, mcpChanges...)
	}
	if installer.providers != nil {
		providerChanges, err := installer.providers.Plan(ctx, request, fingerprint, remove)
		if err != nil {
			return nil, err
		}
		changes = append(changes, providerChanges...)
	}
	return changes, nil
}

func (installer *CodexInstaller) Install(ctx context.Context, request SetupRequest, fingerprint string) error {
	if installer == nil || installer.hook == nil || installer.hooks == nil {
		return errors.New("Codex installer unavailable")
	}
	hookBefore, err := captureCodexHook(request.HookPath)
	if err != nil {
		return err
	}
	var hooksPlan codexHooksPlan
	if request.CodexHooksPath == "" {
		// The private hook is applied below after every other artifact has been
		// prepared, so later failures can restore it safely.
	} else {
		hooksPlan, err = installer.hooks.prepare(ctx, request, fingerprint, false)
		if err != nil {
			return err
		}
	}
	var mcpPlan codexMCPPlan
	if installer.mcp != nil {
		mcpPlan, err = installer.mcp.prepare(ctx, request, fingerprint, false)
		if err != nil {
			return err
		}
	}
	var providerPlan providerConfigPlan
	if installer.providers != nil {
		providerPlan, err = installer.providers.prepare(ctx, request, false)
		if err != nil {
			return err
		}
	}
	hooksApplied, hookApplied, mcpApplied, providerApplied := false, false, false, false
	hookAfter, hookErr := renderSessionStartHook(request)
	if hookErr != nil {
		return hookErr
	}
	rollback := func(cause error) error {
		var rollbackErr error
		if providerApplied && installer.providers != nil {
			rollbackErr = errors.Join(rollbackErr, installer.providers.restore(ctx, providerPlan))
		}
		if mcpApplied && installer.mcp != nil {
			rollbackErr = errors.Join(rollbackErr, installer.mcp.restore(ctx, mcpPlan))
		}
		if hookApplied {
			rollbackErr = errors.Join(rollbackErr, hookBefore.restore(ctx, request.HookPath, hookAfter, request.Remove))
		}
		if hooksApplied {
			rollbackErr = errors.Join(rollbackErr, installer.hooks.restore(ctx, hooksPlan))
		}
		return errors.Join(cause, rollbackErr)
	}
	if hooksPlan.changed {
		if err := installer.hooks.apply(ctx, hooksPlan); err != nil {
			return err
		}
		hooksApplied = true
	}
	if err := installer.hook.Install(ctx, request, fingerprint); err != nil {
		return rollback(err)
	}
	hookApplied = true
	if mcpPlan.changed {
		if err := installer.mcp.apply(ctx, mcpPlan); err != nil {
			return rollback(err)
		}
		mcpApplied = true
	}
	if providerPlan.changed {
		if err := installer.providers.apply(ctx, providerPlan); err != nil {
			return rollback(err)
		}
		providerApplied = true
	}
	return nil
}

func (installer *CodexInstaller) Remove(ctx context.Context, request SetupRequest, fingerprint string) error {
	if installer == nil || installer.hook == nil || installer.hooks == nil {
		return errors.New("Codex installer unavailable")
	}
	hookBefore, err := captureCodexHook(request.HookPath)
	if err != nil {
		return err
	}
	var hooksPlan codexHooksPlan
	if request.CodexHooksPath == "" {
	} else {
		hooksPlan, err = installer.hooks.prepare(ctx, request, fingerprint, true)
		if err != nil {
			return err
		}
	}
	var mcpPlan codexMCPPlan
	if installer.mcp != nil {
		mcpPlan, err = installer.mcp.prepare(ctx, request, fingerprint, true)
		if err != nil {
			return err
		}
	}
	var providerPlan providerConfigPlan
	if installer.providers != nil {
		providerPlan, err = installer.providers.prepare(ctx, request, true)
		if err != nil {
			return err
		}
	}
	hooksApplied, hookApplied, mcpApplied, providerApplied := false, false, false, false
	hookAfter, hookErr := renderSessionStartHook(request)
	if hookErr != nil {
		return hookErr
	}
	rollback := func(cause error) error {
		var rollbackErr error
		if providerApplied && installer.providers != nil {
			rollbackErr = errors.Join(rollbackErr, installer.providers.restore(ctx, providerPlan))
		}
		if mcpApplied && installer.mcp != nil {
			rollbackErr = errors.Join(rollbackErr, installer.mcp.restore(ctx, mcpPlan))
		}
		if hookApplied {
			rollbackErr = errors.Join(rollbackErr, hookBefore.restore(ctx, request.HookPath, hookAfter, request.Remove))
		}
		if hooksApplied {
			rollbackErr = errors.Join(rollbackErr, installer.hooks.restore(ctx, hooksPlan))
		}
		return errors.Join(cause, rollbackErr)
	}
	if hooksPlan.changed {
		if err := installer.hooks.apply(ctx, hooksPlan); err != nil {
			return err
		}
		hooksApplied = true
	}
	if err := installer.hook.Remove(ctx, request, fingerprint); err != nil {
		return rollback(err)
	}
	hookApplied = true
	if mcpPlan.changed {
		if err := installer.mcp.apply(ctx, mcpPlan); err != nil {
			return rollback(err)
		}
		mcpApplied = true
	}
	if providerPlan.changed {
		if err := installer.providers.apply(ctx, providerPlan); err != nil {
			return rollback(err)
		}
		providerApplied = true
	}
	return nil
}

func captureCodexHook(path string) (codexHookSnapshot, error) {
	if err := validateHookPath(path); err != nil {
		return codexHookSnapshot{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return codexHookSnapshot{}, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !currentUserOwns(info) {
		return codexHookSnapshot{}, errors.New("Codex hook path is unsafe")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return codexHookSnapshot{}, err
	}
	return codexHookSnapshot{exists: true, value: append([]byte(nil), value...), mode: info.Mode()}, nil
}

func (snapshot codexHookSnapshot) restore(ctx context.Context, path string, expected []byte, removed bool) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	current, err := captureCodexHook(path)
	if err != nil {
		return err
	}
	if removed {
		if current.exists {
			return ErrSetupCollision
		}
	} else if !current.exists || !bytes.Equal(current.value, expected) {
		return ErrSetupCollision
	}
	if !snapshot.exists {
		if current.exists {
			return os.Remove(path)
		}
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, snapshot.mode.Perm())
	if err != nil {
		return err
	}
	if _, err := file.Write(snapshot.value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(snapshot.mode.Perm()); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func renderSessionStartHook(request SetupRequest) ([]byte, error) {
	endpoint := request.Endpoint
	if endpoint == "" {
		endpoint = "127.0.0.1:7437"
	}
	if err := validateLoopbackEndpoint(endpoint); err != nil {
		return nil, err
	}
	configDir := firstNonEmpty(request.TalariaConfigPath, request.ConfigPath)
	if configDir != "" {
		configDir = filepath.Dir(configDir)
	}
	if configDir == "" {
		configDir = filepath.Dir(request.HookPath)
	}
	tokenPath := request.TokenPath
	if tokenPath == "" {
		tokenPath = filepath.Join(configDir, "token")
	}
	replacements := map[string]string{
		"__TALARIA_ENDPOINT__":   "http://" + endpoint,
		"__TALARIA_CONFIG_DIR__": shellSingleQuote(configDir),
		"__TALARIA_TOKEN_FILE__": shellSingleQuote(tokenPath),
	}
	rendered := string(codexHook)
	for placeholder, value := range replacements {
		rendered = strings.ReplaceAll(rendered, placeholder, value)
	}
	if strings.Contains(rendered, "__TALARIA_") {
		return nil, errors.New("Codex hook endpoint template is invalid")
	}
	return []byte(rendered), nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func upgradeManagedHook(path string, legacy, current []byte) error {
	file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !currentUserOwns(info) || info.Mode().Perm() != 0o700 {
		return errors.New("Codex hook path is unsafe")
	}
	value, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	if !bytes.Equal(value, legacy) {
		return ErrSetupCollision
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := file.Write(current); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Chmod(0o700)
}

func validateHookPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("setup hook path must be absolute and canonical")
	}
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != ManagedDirectoryMode.Perm() || !currentUserOwns(info) {
		return errors.New("setup hook parent is unsafe")
	}
	return nil
}
