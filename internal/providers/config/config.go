package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	TypeCodex            = "codex"
	TypeOpenAICompatible = "openai_compatible"
	configVersion        = 1
	defaultTimeout       = 90 * time.Second
)

type Provider struct {
	Type            string
	Model           string
	ReasoningEffort string
	Command         string
	BaseURL         string
	CredentialEnv   string
	CredentialFile  string
	Credential      string
	Timeout         time.Duration
}

type Configuration struct {
	Version   int
	Enabled   bool
	Chain     []string
	Providers map[string]Provider
}

type fileConfiguration struct {
	Version   int                     `toml:"version"`
	Enabled   *bool                   `toml:"enabled"`
	Chain     []string                `toml:"chain"`
	Providers map[string]fileProvider `toml:"providers"`
}

type fileProvider struct {
	Type            string `toml:"type"`
	Model           string `toml:"model"`
	ReasoningEffort string `toml:"reasoning_effort"`
	Command         string `toml:"command"`
	BaseURL         string `toml:"base_url"`
	CredentialEnv   string `toml:"credential_env"`
	CredentialFile  string `toml:"credential_file"`
	Timeout         string `toml:"timeout"`
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func Default() Configuration {
	return Configuration{
		Version: configVersion,
		Enabled: true,
		Chain:   []string{"codex"},
		Providers: map[string]Provider{
			"codex": {
				Type:            TypeCodex,
				Model:           "gpt-5.6-luna",
				ReasoningEffort: "high",
				Command:         "codex",
				Timeout:         defaultTimeout,
			},
		},
	}
}

func Load(path string, environment map[string]string) (Configuration, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return Configuration{}, errors.New("provider config path must be absolute and canonical")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Configuration{}, fmt.Errorf("stat provider config: %w", err)
	}
	if err := validateOwnerFile(info); err != nil {
		return Configuration{}, fmt.Errorf("provider config is unsafe: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Configuration{}, fmt.Errorf("read provider config: %w", err)
	}
	var raw fileConfiguration
	decoder := toml.NewDecoder(strings.NewReader(string(data))).DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Configuration{}, fmt.Errorf("decode provider config: %w", err)
	}
	configuration, err := materialize(raw, environment)
	if err != nil {
		return Configuration{}, err
	}
	return configuration, nil
}

func materialize(raw fileConfiguration, environment map[string]string) (Configuration, error) {
	if raw.Version != configVersion {
		return Configuration{}, fmt.Errorf("unsupported provider config version %d", raw.Version)
	}
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}
	configuration := Configuration{Version: raw.Version, Enabled: enabled, Chain: append([]string(nil), raw.Chain...), Providers: make(map[string]Provider, len(raw.Providers))}
	for name, rawProvider := range raw.Providers {
		provider, err := materializeProvider(name, rawProvider, environment)
		if err != nil {
			return Configuration{}, err
		}
		configuration.Providers[name] = provider
	}
	if err := configuration.Validate(); err != nil {
		return Configuration{}, err
	}
	return configuration, nil
}

func materializeProvider(name string, raw fileProvider, environment map[string]string) (Provider, error) {
	provider := Provider{
		Type:            strings.TrimSpace(raw.Type),
		Model:           strings.TrimSpace(raw.Model),
		ReasoningEffort: strings.TrimSpace(raw.ReasoningEffort),
		Command:         strings.TrimSpace(raw.Command),
		BaseURL:         strings.TrimSpace(raw.BaseURL),
		CredentialEnv:   strings.TrimSpace(raw.CredentialEnv),
		CredentialFile:  strings.TrimSpace(raw.CredentialFile),
	}
	provider.Timeout = defaultTimeout
	if raw.Timeout != "" {
		value, err := time.ParseDuration(raw.Timeout)
		if err != nil || value <= 0 {
			return Provider{}, fmt.Errorf("provider %q has invalid timeout", name)
		}
		provider.Timeout = value
	}
	if provider.CredentialEnv != "" && provider.CredentialFile != "" {
		return Provider{}, fmt.Errorf("provider %q configures both credential_env and credential_file", name)
	}
	if provider.CredentialEnv != "" {
		if !environmentName.MatchString(provider.CredentialEnv) {
			return Provider{}, fmt.Errorf("provider %q has invalid credential_env", name)
		}
		provider.Credential = environment[provider.CredentialEnv]
		if provider.Credential == "" {
			return Provider{}, fmt.Errorf("provider %q credential_env is unset", name)
		}
	}
	if provider.CredentialFile != "" {
		if !filepath.IsAbs(provider.CredentialFile) || filepath.Clean(provider.CredentialFile) != provider.CredentialFile {
			return Provider{}, fmt.Errorf("provider %q credential_file must be absolute and canonical", name)
		}
		info, err := os.Lstat(provider.CredentialFile)
		if err != nil {
			return Provider{}, fmt.Errorf("provider %q credential_file unavailable: %w", name, err)
		}
		if err := validateOwnerFile(info); err != nil {
			return Provider{}, fmt.Errorf("provider %q credential_file unsafe: %w", name, err)
		}
		value, err := os.ReadFile(provider.CredentialFile)
		if err != nil {
			return Provider{}, fmt.Errorf("provider %q credential_file unreadable: %w", name, err)
		}
		provider.Credential = strings.TrimSpace(string(value))
		if provider.Credential == "" {
			return Provider{}, fmt.Errorf("provider %q credential_file is empty", name)
		}
	}
	return provider, nil
}

func (configuration Configuration) Validate() error {
	if configuration.Version != configVersion {
		return fmt.Errorf("unsupported provider config version %d", configuration.Version)
	}
	seen := make(map[string]struct{}, len(configuration.Chain))
	for _, name := range configuration.Chain {
		if name == "" {
			return errors.New("provider chain contains empty name")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("provider chain contains duplicate %q", name)
		}
		seen[name] = struct{}{}
		provider, ok := configuration.Providers[name]
		if !ok {
			return fmt.Errorf("provider chain references missing provider %q", name)
		}
		if err := validateProvider(name, provider); err != nil {
			return err
		}
	}
	if configuration.Enabled && len(configuration.Chain) == 0 {
		return errors.New("enabled provider config requires a non-empty chain")
	}
	return nil
}

func validateProvider(name string, provider Provider) error {
	if provider.Model == "" || provider.Timeout <= 0 {
		return fmt.Errorf("provider %q requires model and positive timeout", name)
	}
	switch provider.Type {
	case TypeCodex:
		if provider.Command == "" {
			return fmt.Errorf("provider %q requires command", name)
		}
	case TypeOpenAICompatible:
		if err := ValidateBaseURL(provider.BaseURL); err != nil {
			return fmt.Errorf("provider %q base_url: %w", name, err)
		}
	default:
		return fmt.Errorf("provider %q has unsupported type %q", name, provider.Type)
	}
	return nil
}

func validateOwnerFile(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("must be a regular non-symlink file")
	}
	if info.Mode().Perm() != 0o600 {
		return errors.New("must use mode 0600")
	}
	if !currentUserOwns(info) {
		return errors.New("must be owned by current user")
	}
	return nil
}

func currentUserOwns(info os.FileInfo) bool {
	system := info.Sys()
	if system == nil {
		return true
	}
	value := reflect.ValueOf(system)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return true
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return true
	}
	field := value.FieldByName("Uid")
	if !field.IsValid() || !field.CanUint() {
		return true
	}
	return uint64(os.Getuid()) == field.Uint()
}
