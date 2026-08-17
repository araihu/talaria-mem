package lifecycle

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/filesystem"
)

//go:embed assets/session-start.sh
var sessionStartHook []byte

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
	info, err := os.Lstat(request.HookPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !currentUserOwns(info) {
			return errors.New("Codex hook path is unsafe")
		}
		value, readErr := os.ReadFile(request.HookPath)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(value, sessionStartHook) {
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
	if err := filesystem.AtomicWriteNoFollow(ctx, request.HookPath, sessionStartHook); err != nil {
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
	if !bytes.Equal(value, sessionStartHook) {
		return ErrSetupCollision
	}
	return os.Remove(request.HookPath)
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
