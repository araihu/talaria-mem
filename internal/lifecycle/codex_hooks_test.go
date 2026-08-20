package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codexSecretCanary = "DO_NOT_PRINT_CODEX_CREDENTIAL_CANARY"

func TestCodexInstallerRegistersPreservesAndIsIdempotent(t *testing.T) {
	root := secureCodexTestDirectory(t)
	hookPath := filepath.Join(root, "session-start.sh")
	hooksPath := filepath.Join(root, "hooks.json")
	original := []byte(`{"description":"consumer config","credential":"` + codexSecretCanary + `","hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"user-stop"}]}]}}`)
	if err := os.WriteFile(hooksPath, original, ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{HookPath: hookPath, CodexHooksPath: hooksPath, Endpoint: "127.0.0.1:8743"}
	installer := NewCodexInstaller()

	plan, err := installer.Plan(context.Background(), request, "fingerprint", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Action != "register" || plan[0].Path != hooksPath {
		t.Fatalf("unexpected hooks plan: %+v", plan)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(planJSON), codexSecretCanary) {
		t.Fatal("hooks plan exposed credential canary")
	}

	if err := installer.Install(context.Background(), request, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), codexSecretCanary) || !strings.Contains(string(first), "user-stop") {
		t.Fatal("unrelated Codex configuration was not preserved")
	}
	assertCodexManagedGroup(t, first, hookPath)

	if err := installer.Install(context.Background(), request, "fingerprint"); err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("second install changed hooks.json")
	}
	if info, err := os.Stat(hooksPath); err != nil || info.Mode().Perm() != ManagedFileMode.Perm() {
		t.Fatalf("hooks mode is not private: err=%v", err)
	}
	if info, err := os.Stat(hookPath); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("hook mode is not executable/private: err=%v", err)
	}
}

func TestCodexHooksInstallerRegistersAllCurationEvents(t *testing.T) {
	root := secureCodexTestDirectory(t)
	request := SetupRequest{HookPath: filepath.Join(root, "codex-hook.sh"), CodexHooksPath: filepath.Join(root, "hooks.json")}
	installer := NewCodexHooksInstaller()
	if err := installer.Install(context.Background(), request, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(request.CodexHooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreCompact", "SessionEnd"} {
		if len(document.Hooks[event]) != 1 {
			t.Fatalf("event %s groups=%v", event, document.Hooks[event])
		}
	}
	if err := installer.Remove(context.Background(), request, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(request.CodexHooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), request.HookPath) {
		t.Fatal("managed hook command remained after removal")
	}
}

func TestCodexHooksInstallerRejectsChangedManagedEntryWithoutOverwrite(t *testing.T) {
	root := secureCodexTestDirectory(t)
	hookPath := filepath.Join(root, "session-start.sh")
	hooksPath := filepath.Join(root, "hooks.json")
	changed := []byte(`{"hooks":{"SessionStart":[{"matcher":"startup|resume|clear|compact","hooks":[{"type":"command","command":"` + hookPath + `","statusMessage":"` + codexSecretCanary + `"}]}]}}`)
	if err := os.WriteFile(hooksPath, changed, ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{HookPath: hookPath, CodexHooksPath: hooksPath}
	err := NewCodexHooksInstaller().Install(context.Background(), request, "fingerprint")
	if !errors.Is(err, ErrSetupCollision) {
		t.Fatalf("changed managed entry error=%v, want collision", err)
	}
	if strings.Contains(err.Error(), codexSecretCanary) {
		t.Fatal("collision diagnostic exposed hooks content")
	}
	after, readErr := os.ReadFile(hooksPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, changed) {
		t.Fatal("collision changed hooks.json")
	}
}

func TestCodexHooksInstallerRejectsMalformedAndDuplicateJSONWithoutEcho(t *testing.T) {
	root := secureCodexTestDirectory(t)
	hookPath := filepath.Join(root, "session-start.sh")
	hooksPath := filepath.Join(root, "hooks.json")
	request := SetupRequest{HookPath: hookPath, CodexHooksPath: hooksPath}
	for _, value := range []string{
		`{"credential":"` + codexSecretCanary,
		`{"credential":"` + codexSecretCanary + `","credential":"second"}`,
	} {
		if err := os.WriteFile(hooksPath, []byte(value), ManagedFileMode.Perm()); err != nil {
			t.Fatal(err)
		}
		err := NewCodexHooksInstaller().Install(context.Background(), request, "fingerprint")
		if !errors.Is(err, ErrCodexHooksInvalid) {
			t.Fatalf("invalid JSON error=%v, want stable invalid error", err)
		}
		if strings.Contains(err.Error(), codexSecretCanary) {
			t.Fatal("invalid JSON diagnostic exposed credential canary")
		}
	}
}

func TestCodexInstallerRollsBackOfficialRegistryOnHookCollision(t *testing.T) {
	root := secureCodexTestDirectory(t)
	hookPath := filepath.Join(root, "session-start.sh")
	hooksPath := filepath.Join(root, "hooks.json")
	originalHooks := []byte(`{"hooks":{"Stop":[]}}`)
	if err := os.WriteFile(hooksPath, originalHooks, ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	userHook := []byte("#!/bin/sh\n" + codexSecretCanary + "\n")
	if err := os.WriteFile(hookPath, userHook, 0o700); err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{HookPath: hookPath, CodexHooksPath: hooksPath}
	err := NewCodexInstaller().Install(context.Background(), request, "fingerprint")
	if !errors.Is(err, ErrSetupCollision) {
		t.Fatalf("hook collision error=%v, want collision", err)
	}
	if strings.Contains(err.Error(), codexSecretCanary) {
		t.Fatal("hook collision diagnostic exposed user hook content")
	}
	afterHooks, readErr := os.ReadFile(hooksPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(afterHooks, originalHooks) {
		t.Fatal("official Codex registry was not rolled back")
	}
	afterHook, readErr := os.ReadFile(hookPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(afterHook, userHook) {
		t.Fatal("user hook was modified after collision")
	}
}

func TestCodexInstallerRemovalPreservesUnrelatedConfiguration(t *testing.T) {
	root := secureCodexTestDirectory(t)
	hookPath := filepath.Join(root, "session-start.sh")
	hooksPath := filepath.Join(root, "hooks.json")
	original := []byte(`{"description":"keep","credential":"` + codexSecretCanary + `","hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"user-stop"}]}]}}`)
	if err := os.WriteFile(hooksPath, original, ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{HookPath: hookPath, CodexHooksPath: hooksPath, Endpoint: "127.0.0.1:8743"}
	installer := NewCodexInstaller()
	if err := installer.Install(context.Background(), request, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	if err := installer.Remove(context.Background(), request, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), hookPath) || !strings.Contains(string(data), "user-stop") || !strings.Contains(string(data), codexSecretCanary) {
		t.Fatal("removal changed unrelated Codex configuration")
	}
	if _, err := os.Lstat(hookPath); !os.IsNotExist(err) {
		t.Fatalf("managed hook still exists: err=%v", err)
	}
}

func TestSetupDryRunAndApplyDoNotExposeHooksContent(t *testing.T) {
	root := secureCodexTestDirectory(t)
	hookPath := filepath.Join(root, "session-start.sh")
	hooksPath := filepath.Join(root, "hooks.json")
	configPath := filepath.Join(root, "codex.toml")
	tokenPath := filepath.Join(root, "token")
	request := SetupRequest{
		ConfigPath:     configPath,
		HookPath:       hookPath,
		CodexHooksPath: hooksPath,
		TokenPath:      tokenPath,
		BinaryPath:     "/tmp/talaria-mem",
		Endpoint:       "127.0.0.1:7437",
		DryRun:         true,
	}
	service := NewSetupService(NewCodexInstaller())
	result, err := service.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), codexSecretCanary) {
		t.Fatal("setup dry-run exposed a credential canary")
	}
	if len(result.Changes) != 2 {
		t.Fatalf("setup dry-run changes=%d, want metadata plus official registry", len(result.Changes))
	}

	request.DryRun = false
	if _, err := service.Apply(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hookPath); err != nil {
		t.Fatal(err)
	}
	request.Remove = true
	request.Fingerprint, err = Fingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestSetupRejectsNonFingerprintCredentialInput(t *testing.T) {
	root := secureCodexTestDirectory(t)
	request := SetupRequest{
		ConfigPath:  filepath.Join(root, "codex.toml"),
		HookPath:    filepath.Join(root, "session-start.sh"),
		TokenPath:   filepath.Join(root, "token"),
		BinaryPath:  "/tmp/talaria-mem",
		Endpoint:    "127.0.0.1:7437",
		Remove:      true,
		Fingerprint: codexSecretCanary,
	}
	_, err := NewSetupService(NewCodexInstaller()).Plan(context.Background(), request)
	if !errors.Is(err, ErrSetupReceipt) {
		t.Fatalf("invalid fingerprint error=%v, want receipt error", err)
	}
	if strings.Contains(err.Error(), codexSecretCanary) {
		t.Fatal("invalid fingerprint diagnostic exposed credential input")
	}
}

func TestCodexHooksInstallerRejectsSymlink(t *testing.T) {
	root := secureCodexTestDirectory(t)
	linkTarget := filepath.Join(root, "real-hooks.json")
	linkPath := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(linkTarget, []byte(`{}`), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkTarget, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	request := SetupRequest{HookPath: filepath.Join(root, "session-start.sh"), CodexHooksPath: linkPath}
	err := NewCodexHooksInstaller().Install(context.Background(), request, "fingerprint")
	if !errors.Is(err, ErrCodexHooksUnsafe) {
		t.Fatalf("symlink error=%v, want unsafe path", err)
	}
}

func TestCodexHooksInstallerAllowsReadOnlyCodexParent(t *testing.T) {
	root := secureCodexTestDirectory(t)
	codexDirectory := filepath.Join(root, "codex")
	if err := os.Mkdir(codexDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{
		HookPath:       filepath.Join(root, "session-start.sh"),
		CodexHooksPath: filepath.Join(codexDirectory, "hooks.json"),
	}
	if err := NewCodexInstaller().Install(context.Background(), request, "fingerprint"); err != nil {
		t.Fatalf("read-only Codex parent rejected: %v", err)
	}
	info, err := os.Stat(request.CodexHooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != ManagedFileMode.Perm() {
		t.Fatalf("hooks mode=%o", info.Mode().Perm())
	}
}

func assertCodexManagedGroup(t *testing.T, data []byte, command string) {
	t.Helper()
	var document struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type          string `json:"type"`
				Command       string `json:"command"`
				StatusMessage string `json:"statusMessage"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	groups := document.Hooks[codexHookName]
	if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Matcher != codexHooksMatcher || groups[0].Hooks[0].Type != "command" || groups[0].Hooks[0].Command != command || groups[0].Hooks[0].StatusMessage != codexHooksStatusMessage {
		t.Fatal("managed Codex hook group was not registered exactly")
	}
}

func secureCodexTestDirectory(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	return root
}
