package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var defaultProvidersTOML = []byte(`version = 1
enabled = true
chain = ["codex"]

[providers.codex]
type = "codex"
model = "gpt-5.6-luna"
reasoning_effort = "high"
command = "codex"
timeout = "90s"
`)

type providerConfigPlan struct {
	path         string
	before       []byte
	beforeExists bool
	after        []byte
	changed      bool
	remove       bool
}

type ProviderConfigInstaller struct{}

func NewProviderConfigInstaller() *ProviderConfigInstaller { return &ProviderConfigInstaller{} }

func (installer *ProviderConfigInstaller) Plan(ctx context.Context, request SetupRequest, fingerprint string, remove bool) ([]SetupChange, error) {
	plan, err := installer.prepare(ctx, request, remove)
	if err != nil {
		return nil, err
	}
	if !plan.changed {
		return nil, nil
	}
	action := "install"
	if remove {
		action = "remove"
	}
	return []SetupChange{{Path: plan.path, Action: action, Fingerprint: fingerprint}}, nil
}

func (installer *ProviderConfigInstaller) Install(ctx context.Context, request SetupRequest, _ string) error {
	plan, err := installer.prepare(ctx, request, false)
	if err != nil {
		return err
	}
	return installer.apply(ctx, plan)
}

func (installer *ProviderConfigInstaller) Remove(ctx context.Context, request SetupRequest, _ string) error {
	plan, err := installer.prepare(ctx, request, true)
	if err != nil {
		return err
	}
	return installer.apply(ctx, plan)
}

func (installer *ProviderConfigInstaller) prepare(ctx context.Context, request SetupRequest, remove bool) (providerConfigPlan, error) {
	if installer == nil {
		return providerConfigPlan{}, errors.New("provider config installer unavailable")
	}
	if request.ProviderConfigPath == "" {
		return providerConfigPlan{}, nil
	}
	if err := contextError(ctx); err != nil {
		return providerConfigPlan{}, err
	}
	if err := validateManagedParent(request.ProviderConfigPath); err != nil {
		return providerConfigPlan{}, err
	}
	before, exists, err := readProviderConfigFile(request.ProviderConfigPath)
	if err != nil {
		return providerConfigPlan{}, err
	}
	plan := providerConfigPlan{path: request.ProviderConfigPath, before: append([]byte(nil), before...), beforeExists: exists, after: append([]byte(nil), defaultProvidersTOML...), remove: remove}
	if !exists {
		if remove {
			return plan, nil
		}
		plan.changed = true
		return plan, nil
	}
	if remove && bytes.Equal(before, defaultProvidersTOML) {
		plan.after = nil
		plan.changed = true
	}
	// Existing provider topology is user-owned and is intentionally preserved.
	return plan, nil
}

func (installer *ProviderConfigInstaller) apply(ctx context.Context, plan providerConfigPlan) error {
	if !plan.changed {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	current, exists, err := readProviderConfigFile(plan.path)
	if err != nil || exists != plan.beforeExists || !bytes.Equal(current, plan.before) {
		return ErrSetupCollision
	}
	if plan.remove {
		if exists {
			if err := os.Remove(plan.path); err != nil {
				return errors.New("provider config removal failed")
			}
		}
		return nil
	}
	if !exists {
		file, err := os.OpenFile(plan.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, ManagedFileMode.Perm())
		if err != nil {
			return errors.New("provider config write failed")
		}
		if _, err := file.Write(plan.after); err != nil {
			_ = file.Close()
			_ = os.Remove(plan.path)
			return errors.New("provider config write failed")
		}
		if err := file.Close(); err != nil {
			return errors.New("provider config write failed")
		}
		return nil
	}
	if err := writeOwnerFileNoFollow(plan.path, plan.after, ManagedFileMode.Perm()); err != nil {
		return errors.New("provider config write failed")
	}
	return nil
}

func (installer *ProviderConfigInstaller) restore(ctx context.Context, plan providerConfigPlan) error {
	if installer == nil || !plan.changed {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	current, exists, err := readProviderConfigFile(plan.path)
	if err != nil || !exists || !bytes.Equal(current, plan.after) {
		return ErrSetupCollision
	}
	if !plan.beforeExists {
		return os.Remove(plan.path)
	}
	return writeOwnerFileNoFollow(plan.path, plan.before, ManagedFileMode.Perm())
}

func readProviderConfigFile(path string) ([]byte, bool, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, false, errors.New("provider config path is unsafe")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != ManagedFileMode.Perm() || !currentUserOwns(info) {
		return nil, false, errors.New("provider config path is unsafe")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, false, errors.New("provider config read failed")
	}
	return value, true, nil
}
