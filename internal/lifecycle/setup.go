package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/filesystem"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

var (
	ErrSetupCollision = errors.New("Codex setup fingerprint collision")
	ErrSetupReceipt   = errors.New("Codex setup fingerprint is required")
)

const (
	setupBegin = "# talaria-mem:begin"
	setupEnd   = "# talaria-mem:end"
)

type SetupRequest struct {
	ConfigPath     string
	HookPath       string
	CodexHooksPath string
	TokenPath      string
	BinaryPath     string
	Endpoint       string
	DryRun         bool
	Remove         bool
	Fingerprint    string
}

type SetupChange struct {
	Path        string `json:"path"`
	Action      string `json:"action"`
	Fingerprint string `json:"fingerprint"`
	BackupPath  string `json:"backup_path,omitempty"`
}

type SetupResult struct {
	Version     string        `json:"version"`
	DryRun      bool          `json:"dry_run"`
	Removed     bool          `json:"removed"`
	Fingerprint string        `json:"fingerprint"`
	Changes     []SetupChange `json:"changes"`
}

// ServiceInstaller is deliberately separate from Codex config replacement so
// an integration-artifact failure can roll the config back to its verified
// backup. Implementations must not start a host service during setup.
type ServiceInstaller interface {
	Install(context.Context, SetupRequest, string) error
	Remove(context.Context, SetupRequest, string) error
}

// ServiceInstallerPlanner lets an installer expose safe, content-free
// changes during setup --dry-run without widening ServiceInstaller's
// compatibility contract. Paths and fingerprints are the only receipt data;
// installer configuration bytes never enter SetupResult.
type ServiceInstallerPlanner interface {
	Plan(context.Context, SetupRequest, string, bool) ([]SetupChange, error)
}

type SetupService struct {
	Files     *filesystem.ManagedFileStore
	Installer ServiceInstaller
}

func NewSetupService(installer ServiceInstaller) *SetupService {
	return &SetupService{Files: filesystem.NewManagedFileStore(), Installer: installer}
}

// Fingerprint is deterministic over the installation identity, not over
// arbitrary config bytes.  This lets remove target only the exact installation
// block while preserving unknown user configuration.
func Fingerprint(request SetupRequest) (string, error) {
	if err := validateSetupRequest(request); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		"talaria-mem/setup/v1", request.ConfigPath, request.HookPath,
		request.TokenPath, request.BinaryPath, request.Endpoint,
	}, "\x00")))
	return hex.EncodeToString(digest[:]), nil
}

func (service *SetupService) Plan(ctx context.Context, request SetupRequest) (SetupResult, error) {
	if service == nil {
		return SetupResult{}, errors.New("setup unavailable")
	}
	if err := contextError(ctx); err != nil {
		return SetupResult{}, err
	}
	if err := validateSetupRequest(request); err != nil {
		return SetupResult{}, err
	}
	fingerprint, err := Fingerprint(request)
	if err != nil {
		return SetupResult{}, err
	}
	if request.Fingerprint != "" {
		if !validSetupFingerprint(request.Fingerprint) {
			return SetupResult{}, ErrSetupReceipt
		}
		if request.Fingerprint != fingerprint && !request.Remove {
			return SetupResult{}, ErrSetupCollision
		}
		fingerprint = request.Fingerprint
	}
	current, exists, err := readConfig(request.ConfigPath)
	if err != nil {
		return SetupResult{}, err
	}
	markerFingerprint, markerStart, markerEnd := findSetupMarker(current)
	result := SetupResult{Version: "talaria.setup.v1", DryRun: request.DryRun, Fingerprint: fingerprint, Changes: []SetupChange{}}
	if request.Remove {
		if markerFingerprint != "" && (request.Fingerprint == "" || request.Fingerprint != markerFingerprint) {
			return SetupResult{}, ErrSetupReceipt
		}
		if markerFingerprint != "" {
			result.Changes = append(result.Changes, SetupChange{Path: request.ConfigPath, Action: "remove", Fingerprint: markerFingerprint})
			if !exists {
				result.Changes = nil
			}
		}
	} else {
		if markerFingerprint != "" && markerFingerprint != fingerprint {
			return SetupResult{}, ErrSetupCollision
		}
		marker := setupMarker(request, fingerprint)
		if markerStart >= 0 && markerEnd > markerStart {
			// The same installation is idempotent. A changed request necessarily
			// changes the fingerprint and was rejected above.
			if string(current[markerStart:markerEnd]) != marker {
				result.Changes = append(result.Changes, SetupChange{Path: request.ConfigPath, Action: "install", Fingerprint: fingerprint})
			}
		} else {
			result.Changes = append(result.Changes, SetupChange{Path: request.ConfigPath, Action: "install", Fingerprint: fingerprint})
		}
	}
	if planner, ok := service.Installer.(ServiceInstallerPlanner); ok {
		changes, err := planner.Plan(ctx, request, fingerprint, request.Remove)
		if err != nil {
			return SetupResult{}, err
		}
		result.Changes = append(result.Changes, changes...)
	}
	result.Removed = request.Remove && len(result.Changes) > 0
	return result, nil
}

func (service *SetupService) Apply(ctx context.Context, request SetupRequest) (SetupResult, error) {
	if service == nil {
		return SetupResult{}, errors.New("setup unavailable")
	}
	request.DryRun = false
	plan, err := service.Plan(ctx, request)
	if err != nil {
		return SetupResult{}, err
	}
	if len(plan.Changes) == 0 {
		if service.Installer != nil {
			var installerErr error
			if request.Remove {
				installerErr = service.Installer.Remove(ctx, request, plan.Fingerprint)
			} else {
				installerErr = service.Installer.Install(ctx, request, plan.Fingerprint)
			}
			if installerErr != nil {
				return SetupResult{}, installerErr
			}
		}
		return plan, nil
	}
	configChangeIndex := setupConfigChangeIndex(plan.Changes, request.ConfigPath)
	configChanged := configChangeIndex >= 0
	var current []byte
	var exists bool
	var oldFingerprint *filesystemFingerprint
	var backupPath string
	if configChanged {
		current, exists, err = readConfig(request.ConfigPath)
		if err != nil {
			return SetupResult{}, err
		}
		oldFingerprint, err = configFingerprint(request.ConfigPath, exists)
		if err != nil {
			return SetupResult{}, err
		}
		backupPath = request.ConfigPath + ".talaria-mem-" + plan.Fingerprint[:12] + ".bak"
		if exists {
			if err := writeNewManagedFile(ctx, backupPath, current); err != nil && !errors.Is(err, filesystem.ErrTargetExists) {
				return SetupResult{}, err
			}
		}
	}

	if configChanged {
		updated := current
		if request.Remove {
			_, start, end := findSetupMarker(current)
			if start < 0 || end <= start {
				return SetupResult{}, ErrSetupReceipt
			}
			updated = append([]byte(nil), current[:start]...)
			updated = append(updated, current[end:]...)
		} else {
			marker := []byte(setupMarker(request, plan.Fingerprint))
			if len(updated) > 0 && updated[len(updated)-1] != '\n' {
				updated = append(updated, '\n')
			}
			updated = append(updated, marker...)
		}
		if err := replaceManagedFile(ctx, request.ConfigPath, updated, oldFingerprint, exists); err != nil {
			return SetupResult{}, err
		}
	}
	if service.Installer != nil {
		var installerErr error
		if request.Remove {
			installerErr = service.Installer.Remove(ctx, request, plan.Fingerprint)
		} else {
			installerErr = service.Installer.Install(ctx, request, plan.Fingerprint)
		}
		if installerErr != nil {
			if configChanged {
				_ = rollbackManagedFile(ctx, request.ConfigPath, current, exists)
			}
			return SetupResult{}, installerErr
		}
	}
	if configChanged {
		plan.Changes[configChangeIndex].BackupPath = backupPath
	}
	return plan, nil
}

func setupConfigChangeIndex(changes []SetupChange, path string) int {
	for index, change := range changes {
		if change.Path == path && (change.Action == "install" || change.Action == "remove") {
			return index
		}
	}
	return -1
}

func validSetupFingerprint(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func validateSetupRequest(request SetupRequest) error {
	if request.ConfigPath == "" || !filepath.IsAbs(request.ConfigPath) || filepath.Clean(request.ConfigPath) != request.ConfigPath {
		return errors.New("setup config path must be absolute and canonical")
	}
	if request.HookPath == "" || !filepath.IsAbs(request.HookPath) || filepath.Clean(request.HookPath) != request.HookPath {
		return errors.New("setup hook path must be absolute and canonical")
	}
	if request.TokenPath == "" || !filepath.IsAbs(request.TokenPath) || filepath.Clean(request.TokenPath) != request.TokenPath {
		return errors.New("setup token path must be absolute and canonical")
	}
	if request.BinaryPath == "" || !filepath.IsAbs(request.BinaryPath) || filepath.Clean(request.BinaryPath) != request.BinaryPath {
		return errors.New("setup binary path must be absolute and canonical")
	}
	if err := validateLoopbackEndpoint(request.Endpoint); err != nil {
		return err
	}
	if err := validateManagedParent(request.ConfigPath); err != nil {
		return err
	}
	return validateManagedParent(request.HookPath)
}

func validateLoopbackEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || port == "" || (host != "127.0.0.1" && host != "::1") {
		return errors.New("endpoint must be literal loopback host and port")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return errors.New("endpoint port is invalid")
	}
	return nil
}

func validateManagedParent(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != ManagedDirectoryMode.Perm() || !currentUserOwns(info) {
		return errors.New("setup parent is unsafe")
	}
	return nil
}

func readConfig(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != ManagedFileMode.Perm() || !currentUserOwns(info) {
		return nil, false, errors.New("Codex config path is unsafe")
	}
	value, err := os.ReadFile(path)
	return value, true, err
}

func findSetupMarker(value []byte) (fingerprint string, start, end int) {
	text := string(value)
	begin := strings.Index(text, setupBegin)
	if begin < 0 {
		return "", -1, -1
	}
	close := strings.Index(text[begin:], setupEnd)
	if close < 0 {
		return "", -1, -1
	}
	close += begin + len(setupEnd)
	if close < len(text) && text[close] == '\n' {
		close++
	}
	lineEnd := strings.IndexByte(text[begin:], '\n')
	if lineEnd < 0 {
		return "", -1, -1
	}
	line := text[begin : begin+lineEnd]
	parts := strings.SplitN(line, "fingerprint=", 2)
	if len(parts) != 2 || len(parts[1]) != 64 {
		return "", -1, -1
	}
	return parts[1], begin, close
}

func setupMarker(request SetupRequest, fingerprint string) string {
	return strings.Join([]string{
		setupBegin + " fingerprint=" + fingerprint,
		"[talaria_mem]",
		"session_start_hook = " + strconv.Quote(request.HookPath),
		"endpoint = " + strconv.Quote("http://"+request.Endpoint),
		"token_file = " + strconv.Quote(request.TokenPath),
		"binary = " + strconv.Quote(request.BinaryPath),
		setupEnd,
		"",
	}, "\n")
}

func configFingerprint(path string, exists bool) (*filesystemFingerprint, error) {
	if !exists {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	fingerprint := filesystemFingerprint{SHA256Hex: hex.EncodeToString(digest[:]), Size: int64(len(data)), Mode: info.Mode().Perm()}
	return &fingerprint, nil
}

// filesystemFingerprint is kept local so setup does not expose config bytes
// through an application API.  It is converted at the managed-file boundary.
type filesystemFingerprint struct {
	SHA256Hex string
	Size      int64
	Mode      fs.FileMode
}

func replaceManagedFile(ctx context.Context, path string, value []byte, old *filesystemFingerprint, exists bool) error {
	store := filesystem.NewManagedFileStore()
	temp, err := store.CreateTemp(ctx, filepath.Dir(path), ".talaria-setup-", ManagedFileMode)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temp.Path)
		}
	}()
	if _, err := temp.Writer.Write(value); err != nil {
		_ = temp.Writer.Close()
		return err
	}
	if err := temp.Writer.Close(); err != nil {
		return err
	}
	var expected *ports.FileFingerprint
	if exists && old != nil {
		expected = old.toPorts()
	}
	if err := store.ReplaceNoFollow(ctx, temp.Path, path, expected); err != nil {
		return err
	}
	keep = true
	return nil
}

func (fingerprint *filesystemFingerprint) toPorts() *ports.FileFingerprint {
	if fingerprint == nil {
		return nil
	}
	return &ports.FileFingerprint{SHA256Hex: fingerprint.SHA256Hex, Size: fingerprint.Size, Mode: fingerprint.Mode}
}

func writeNewManagedFile(ctx context.Context, path string, value []byte) error {
	return filesystem.AtomicWriteNoFollow(ctx, path, value)
}

func rollbackManagedFile(ctx context.Context, path string, value []byte, existed bool) error {
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	current, _, err := readConfig(path)
	if err != nil {
		return err
	}
	old, err := configFingerprint(path, true)
	if err != nil {
		return err
	}
	_ = current
	return replaceManagedFile(ctx, path, value, old, true)
}
