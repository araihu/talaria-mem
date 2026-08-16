package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/caarlos0/env/v11"
)

const (
	ManagedDirectoryMode os.FileMode = 0o700
	ManagedFileMode      os.FileMode = 0o600
)

// Environment is the small, non-secret path boundary used by acceptance
// composition.  Runtime code receives one parsed value; it does not call
// os.Getenv while starting a server or opening storage.
//
// The variables are intentionally required when this type is parsed.  The
// normal installation path uses DefaultEnvironment and creates its private
// directories explicitly, while acceptance fixtures inject all three paths.
//
//go:generate sh -c "go tool envdoc -types Environment -output environment.md -format markdown && perl -0pi -e 's/\\n+\\z/\\n/' environment.md"
type Environment struct {
	// Private state directory for the database, root-owned lifecycle state, and receipts.
	StateDir string `env:"TALARIA_STATE_DIR,required,notEmpty"`
	// Private configuration directory for the bearer token, root key, and Codex setup metadata.
	ConfigDir string `env:"TALARIA_CONFIG_DIR,required,notEmpty"`
	// Private directory containing authenticated database backups and sidecars.
	BackupDir string `env:"TALARIA_BACKUP_DIR,required,notEmpty"`
}

// ParseEnvironment parses an explicit environment map.  Keeping the map seam
// makes tests deterministic and prevents a second environment read during
// composition.
func ParseEnvironment(values map[string]string) (Environment, error) {
	configuration, err := env.ParseAsWithOptions[Environment](env.Options{Environment: values})
	if err != nil {
		return Environment{}, fmt.Errorf("parse lifecycle environment: %w", err)
	}
	if err := configuration.Validate(); err != nil {
		return Environment{}, err
	}
	return configuration, nil
}

// ParseEnvironmentOS parses the process environment once at the composition
// boundary.  Callers should pass the returned value down rather than reading
// environment variables in child services.
func ParseEnvironmentOS() (Environment, error) {
	return parseEnvironmentOS(false)
}

// ParseEnvironmentOSOrDefault uses the explicit test/configuration paths when
// any TALARIA_* path is supplied, and otherwise returns the normal per-user
// installation paths.  The process environment is snapshotted exactly once.
func ParseEnvironmentOSOrDefault() (Environment, error) {
	return parseEnvironmentOS(true)
}

// ParseEnvironmentOSOrDefaultForSetup parses the same typed path boundary as
// normal composition but permits the final managed directories to be absent.
// EnsureDirectories performs the existence/owner/mode/no-follow checks before
// setup writes anything.
func ParseEnvironmentOSOrDefaultForSetup() (Environment, error) {
	values := make(map[string]string)
	provided := false
	for _, item := range os.Environ() {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[key] = value
			switch key {
			case "TALARIA_STATE_DIR", "TALARIA_CONFIG_DIR", "TALARIA_BACKUP_DIR":
				provided = true
			}
		}
	}
	if !provided {
		home, err := os.UserHomeDir()
		if err != nil {
			return Environment{}, err
		}
		return DefaultEnvironment(home)
	}
	configuration, err := env.ParseAsWithOptions[Environment](env.Options{Environment: values})
	if err != nil {
		return Environment{}, fmt.Errorf("parse lifecycle environment: %w", err)
	}
	if err := configuration.ValidatePaths(); err != nil {
		return Environment{}, err
	}
	return configuration, nil
}

func parseEnvironmentOS(defaultWhenUnset bool) (Environment, error) {
	values := make(map[string]string)
	provided := false
	for _, item := range os.Environ() {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[key] = value
			switch key {
			case "TALARIA_STATE_DIR", "TALARIA_CONFIG_DIR", "TALARIA_BACKUP_DIR":
				provided = true
			}
		}
	}
	if defaultWhenUnset && !provided {
		home, err := os.UserHomeDir()
		if err != nil {
			return Environment{}, err
		}
		return DefaultEnvironment(home)
	}
	return ParseEnvironment(values)
}

// DefaultEnvironment returns the private paths used by a normal local
// installation.  It does not create anything; callers that are performing an
// explicit first-install action may call EnsureDirectories afterwards.
func DefaultEnvironment(home string) (Environment, error) {
	if strings.TrimSpace(home) == "" || !filepath.IsAbs(home) {
		return Environment{}, errors.New("home directory must be absolute")
	}
	base := filepath.Join(home, ".talaria-mem")
	return Environment{
		StateDir:  filepath.Join(base, "state"),
		ConfigDir: filepath.Join(base, "config"),
		BackupDir: filepath.Join(base, "backups"),
	}, nil
}

// Validate enforces the acceptance path contract: absolute, canonical,
// existing, owner-owned, mode-0700, non-symlink directories whose parents are
// also existing owner-owned non-symlink directories.
func (configuration Environment) Validate() error {
	if err := configuration.ValidatePaths(); err != nil {
		return err
	}
	for name, path := range map[string]string{
		"TALARIA_STATE_DIR":  configuration.StateDir,
		"TALARIA_CONFIG_DIR": configuration.ConfigDir,
		"TALARIA_BACKUP_DIR": configuration.BackupDir,
	} {
		if err := validateManagedDirectory(path); err != nil {
			return fmt.Errorf("%s is unsafe: %w", name, err)
		}
	}
	return nil
}

// ValidatePaths enforces shape without requiring final directories to exist.
// It is the only weaker validation allowed at first-install parse time; the
// caller must immediately follow it with EnsureDirectories.
func (configuration Environment) ValidatePaths() error {
	for name, path := range map[string]string{
		"TALARIA_STATE_DIR":  configuration.StateDir,
		"TALARIA_CONFIG_DIR": configuration.ConfigDir,
		"TALARIA_BACKUP_DIR": configuration.BackupDir,
	} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%s is unsafe: path must be absolute and canonical", name)
		}
	}
	return nil
}

// EnsureDirectories creates only the final managed directories.  Every
// parent must already exist and pass the same ownership/no-follow checks; no
// recursive mkdir can silently claim an arbitrary path tree.
func (configuration Environment) EnsureDirectories() error {
	for _, path := range []string{configuration.StateDir, configuration.ConfigDir, configuration.BackupDir} {
		if err := ensureManagedDirectory(path); err != nil {
			return err
		}
	}
	return configuration.Validate()
}

func validateManagedDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("path must be absolute and canonical")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path must be a non-symlink directory")
	}
	if !currentUserOwns(info) {
		return errors.New("path is not owned by the current user")
	}
	if info.Mode().Perm() != ManagedDirectoryMode.Perm() {
		return errors.New("path must use mode 0700")
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() || !currentUserOwns(parentInfo) {
		return errors.New("path parent is unsafe")
	}
	return nil
}

func ensureManagedDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("path must be absolute and canonical")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !currentUserOwns(info) {
			return errors.New("existing path is unsafe")
		}
		if err := os.Chmod(path, ManagedDirectoryMode.Perm()); err != nil {
			return err
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() || !currentUserOwns(parentInfo) {
		return errors.New("directory parent is unsafe")
	}
	if err := os.Mkdir(path, ManagedDirectoryMode.Perm()); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return os.Chmod(path, ManagedDirectoryMode.Perm())
}

func currentUserOwns(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	uid := value.FieldByName("Uid")
	return uid.IsValid() && uid.CanUint() && uid.Uint() == uint64(os.Getuid())
}
