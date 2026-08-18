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
	hook, err := renderSessionStartHook(request.Endpoint)
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
	hook, err := renderSessionStartHook(request.Endpoint)
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
	hook  *CodexHookInstaller
	hooks *CodexHooksInstaller
}

func NewCodexInstaller() *CodexInstaller {
	return &CodexInstaller{hook: NewCodexHookInstaller(), hooks: NewCodexHooksInstaller()}
}

func (installer *CodexInstaller) Plan(ctx context.Context, request SetupRequest, fingerprint string, remove bool) ([]SetupChange, error) {
	if installer == nil || installer.hooks == nil {
		return nil, errors.New("Codex installer unavailable")
	}
	return installer.hooks.Plan(ctx, request, fingerprint, remove)
}

func (installer *CodexInstaller) Install(ctx context.Context, request SetupRequest, fingerprint string) error {
	if installer == nil || installer.hook == nil || installer.hooks == nil {
		return errors.New("Codex installer unavailable")
	}
	if request.CodexHooksPath == "" {
		return installer.hook.Install(ctx, request, fingerprint)
	}
	hooksPlan, err := installer.hooks.prepare(ctx, request, fingerprint, false)
	if err != nil {
		return err
	}
	if err := installer.hooks.apply(ctx, hooksPlan); err != nil {
		return err
	}
	if err := installer.hook.Install(ctx, request, fingerprint); err != nil {
		if restoreErr := installer.hooks.restore(ctx, hooksPlan); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	return nil
}

func (installer *CodexInstaller) Remove(ctx context.Context, request SetupRequest, fingerprint string) error {
	if installer == nil || installer.hook == nil || installer.hooks == nil {
		return errors.New("Codex installer unavailable")
	}
	if request.CodexHooksPath == "" {
		return installer.hook.Remove(ctx, request, fingerprint)
	}
	hooksPlan, err := installer.hooks.prepare(ctx, request, fingerprint, true)
	if err != nil {
		return err
	}
	if err := installer.hooks.apply(ctx, hooksPlan); err != nil {
		return err
	}
	if err := installer.hook.Remove(ctx, request, fingerprint); err != nil {
		if restoreErr := installer.hooks.restore(ctx, hooksPlan); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	return nil
}

func renderSessionStartHook(endpoint string) ([]byte, error) {
	if endpoint == "" {
		endpoint = "127.0.0.1:7437"
	}
	if err := validateLoopbackEndpoint(endpoint); err != nil {
		return nil, err
	}
	const placeholder = "__TALARIA_ENDPOINT__"
	rendered := strings.Replace(string(sessionStartHook), placeholder, "http://"+endpoint, 1)
	if strings.Contains(rendered, placeholder) {
		return nil, errors.New("Codex hook endpoint template is invalid")
	}
	return []byte(rendered), nil
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
