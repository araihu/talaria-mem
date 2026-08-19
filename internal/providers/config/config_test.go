package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileUsesCodexLunaDefault(t *testing.T) {
	configuration, err := Load(filepath.Join(t.TempDir(), "providers.toml"), map[string]string{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !configuration.Enabled || len(configuration.Chain) != 1 || configuration.Chain[0] != "codex" {
		t.Fatalf("configuration = %#v", configuration)
	}
	provider := configuration.Providers["codex"]
	if provider.Type != TypeCodex || provider.Model != "gpt-5.6-luna" || provider.ReasoningEffort != "high" || provider.Command != "codex" || provider.Timeout.String() != "1m30s" {
		t.Fatalf("codex provider = %#v", provider)
	}
}

func TestLoadParsesOrderedLocalOnlyChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.toml")
	contents := `version = 1
enabled = true
chain = ["ollama"]

[providers.ollama]
type = "openai_compatible"
base_url = "http://127.0.0.1:11434/v1"
model = "qwen3:8b"
timeout = "90s"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load(path, map[string]string{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	provider := configuration.Providers["ollama"]
	if provider.BaseURL != "http://127.0.0.1:11434/v1" || provider.Model != "qwen3:8b" || provider.Timeout.String() != "1m30s" {
		t.Fatalf("ollama provider = %#v", provider)
	}
}

func TestLoadResolvesCredentialFromCapturedEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.toml")
	contents := `version = 1
enabled = true
chain = ["remote"]

[providers.remote]
type = "openai_compatible"
base_url = "https://api.example.test/v1"
model = "model"
credential_env = "REMOTE_TOKEN"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load(path, map[string]string{"REMOTE_TOKEN": "secret-value"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := configuration.Providers["remote"].Credential; got != "secret-value" {
		t.Fatalf("credential = %q, want secret-value", got)
	}
}

func TestLoadRejectsCredentialSourceConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.toml")
	contents := `version = 1
enabled = true
chain = ["remote"]

[providers.remote]
type = "openai_compatible"
base_url = "https://api.example.test/v1"
model = "model"
credential_env = "REMOTE_TOKEN"
credential_file = "/tmp/token"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, map[string]string{"REMOTE_TOKEN": "secret-value"}); err == nil {
		t.Fatal("Load() error = nil")
	}
}

func TestLoadRejectsDuplicateOrMissingChainEntries(t *testing.T) {
	for _, chain := range []string{`["codex", "codex"]`, `[]`} {
		path := filepath.Join(t.TempDir(), "providers.toml")
		contents := "version = 1\nenabled = true\nchain = " + chain + "\n\n[providers.codex]\ntype = \"codex\"\nmodel = \"gpt-5.6-luna\"\ncommand = \"codex\"\n"
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, map[string]string{}); err == nil {
			t.Errorf("Load(%s) error = nil", chain)
		}
	}
}
