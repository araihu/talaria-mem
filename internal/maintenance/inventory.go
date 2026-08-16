package maintenance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

const (
	BackupManifestSuffix = ".manifest.json"
	BackupFileSuffix     = ".db"
)

type BackupLifecycle string

const (
	BackupPending          BackupLifecycle = "pending"
	BackupComplete         BackupLifecycle = "complete"
	BackupCleanupCandidate BackupLifecycle = "cleanup_candidate"
	BackupQuarantined      BackupLifecycle = "quarantined"
	BackupDeleted          BackupLifecycle = "deleted"
)

type BackupManifest struct {
	Version                   int             `json:"version"`
	ID                        string          `json:"id"`
	Path                      string          `json:"path"`
	SHA256                    string          `json:"sha256"`
	Mode                      uint32          `json:"mode"`
	CreationCause             string          `json:"creation_cause"`
	SchemaVersion             int64           `json:"schema_version"`
	DatabaseRevisionWatermark int64           `json:"database_revision_watermark"`
	Lifecycle                 BackupLifecycle `json:"lifecycle"`
	OwnerID                   string          `json:"owner_id"`
	KeyVersion                uint32          `json:"key_version"`
	CreatedAt                 string          `json:"created_at"`
	UpdatedAt                 string          `json:"updated_at"`
	MAC                       string          `json:"mac"`
}

func (manifest BackupManifest) unsigned() BackupManifest {
	manifest.MAC = ""
	return manifest
}

func (manifest BackupManifest) validate() error {
	if manifest.Version != 1 || manifest.ID == "" || manifest.Path == "" || manifest.Mode != uint32(ManagedFileMode.Perm()) || manifest.KeyVersion == 0 {
		return ErrReceiptInvalid
	}
	if manifest.Lifecycle != BackupPending && manifest.Lifecycle != BackupComplete && manifest.Lifecycle != BackupCleanupCandidate && manifest.Lifecycle != BackupQuarantined && manifest.Lifecycle != BackupDeleted {
		return ErrReceiptInvalid
	}
	if manifest.SchemaVersion < 0 || manifest.DatabaseRevisionWatermark < 0 {
		return ErrReceiptInvalid
	}
	for _, field := range []struct {
		value string
		max   int
		req   bool
	}{
		{manifest.ID, 128, true}, {manifest.Path, 4096, true}, {manifest.SHA256, 64, false},
		{manifest.CreationCause, 128, true}, {manifest.OwnerID, 128, true}, {manifest.CreatedAt, 64, true}, {manifest.UpdatedAt, 64, true},
	} {
		if err := validateMetadata(field.value, field.max, field.req); err != nil {
			return err
		}
	}
	return nil
}

type InventoryUnknown struct {
	Path        string                `json:"path"`
	Fingerprint ports.FileFingerprint `json:"fingerprint"`
	Reason      string                `json:"reason"`
	Action      string                `json:"action"`
}

type Inventory struct {
	Entries []BackupManifest   `json:"entries"`
	Unknown []InventoryUnknown `json:"unknown"`
	Ready   bool               `json:"ready"`
}

func (inventory Inventory) Complete() bool { return inventory.Ready && len(inventory.Unknown) == 0 }

func (inventory Inventory) BackupIDs() []string {
	ids := make([]string, 0, len(inventory.Entries))
	for _, entry := range inventory.Entries {
		ids = append(ids, entry.ID)
	}
	sort.Strings(ids)
	return ids
}

type InventoryReader struct {
	Dir        string
	Key        []byte
	OwnerID    string
	KeyVersion uint32
}

func NewInventoryReader(dir string, key []byte) (*InventoryReader, error) {
	if len(key) < 16 {
		return nil, ErrReceiptInvalid
	}
	if err := validateManagedDirectory(dir); err != nil {
		return nil, err
	}
	return &InventoryReader{Dir: dir, Key: append([]byte(nil), key...), OwnerID: ownerID(), KeyVersion: 1}, nil
}

func ownerID() string {
	return strconv.Itoa(os.Getuid())
}

func (reader *InventoryReader) Read(ctx context.Context) (Inventory, error) {
	if err := contextErr(ctx); err != nil {
		return Inventory{}, err
	}
	if reader == nil || len(reader.Key) < 16 {
		return Inventory{}, ErrInventoryUnready
	}
	if err := validateManagedDirectory(reader.Dir); err != nil {
		return Inventory{}, err
	}
	entries, err := os.ReadDir(reader.Dir)
	if err != nil {
		return Inventory{}, err
	}
	manifestPaths := make(map[string]struct{})
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), BackupManifestSuffix) {
			manifestPaths[entry.Name()] = struct{}{}
		}
	}
	result := Inventory{Entries: make([]BackupManifest, 0), Unknown: make([]InventoryUnknown, 0), Ready: true}
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return Inventory{}, err
		}
		path := filepath.Join(reader.Dir, entry.Name())
		if strings.HasSuffix(entry.Name(), BackupManifestSuffix) {
			manifest, readErr := reader.readManifest(path)
			if readErr != nil {
				result.Unknown = append(result.Unknown, reader.unknown(path, "unauthenticated or malformed manifest", "quarantine"))
				result.Ready = false
				continue
			}
			backup, backupErr := os.Lstat(manifest.Path)
			if backupErr != nil || backup.Mode()&os.ModeSymlink != 0 || !backup.Mode().IsRegular() || backup.Mode().Perm() != ManagedFileMode || !owns(backup) {
				result.Unknown = append(result.Unknown, reader.unknown(path, "manifest target is missing or unsafe", "quarantine"))
				result.Ready = false
				continue
			}
			if manifest.Lifecycle == BackupComplete {
				actual, hashErr := fingerprint(manifest.Path)
				if hashErr != nil || !strings.EqualFold(manifest.SHA256, actual.SHA256Hex) {
					result.Unknown = append(result.Unknown, reader.unknown(path, "backup fingerprint drift", "quarantine"))
					result.Ready = false
					continue
				}
			} else {
				result.Ready = false
			}
			result.Entries = append(result.Entries, manifest)
			continue
		}
		if strings.HasSuffix(entry.Name(), BackupFileSuffix) || entry.Name() != "" {
			// A backup file is known only when a valid sidecar names it.  The
			// sidecar pass above has already established those paths.
			known := false
			for manifestPath := range manifestPaths {
				if strings.TrimSuffix(manifestPath, BackupManifestSuffix)+BackupFileSuffix == entry.Name() {
					known = true
					break
				}
			}
			if !known {
				result.Unknown = append(result.Unknown, reader.unknown(path, "unmanaged backup entry", "quarantine"))
				result.Ready = false
			}
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].ID < result.Entries[j].ID })
	sort.Slice(result.Unknown, func(i, j int) bool { return result.Unknown[i].Path < result.Unknown[j].Path })
	return result, nil
}

func (reader *InventoryReader) readManifest(path string) (BackupManifest, error) {
	if _, err := validateManagedFile(path); err != nil {
		return BackupManifest{}, err
	}
	var manifest BackupManifest
	if _, err := readManaged(path, &manifest); err != nil {
		return BackupManifest{}, err
	}
	if err := manifest.validate(); err != nil {
		return BackupManifest{}, err
	}
	digest, err := jsonDigest(reader.Key, manifest.unsigned())
	if err != nil || !secureEqualHex(digest, manifest.MAC) {
		return BackupManifest{}, ErrReceiptInvalid
	}
	if manifest.KeyVersion != reader.KeyVersion || manifest.OwnerID != reader.OwnerID || filepath.Dir(manifest.Path) != reader.Dir {
		return BackupManifest{}, ErrReceiptInvalid
	}
	return manifest, nil
}

func (reader *InventoryReader) unknown(path, reason, action string) InventoryUnknown {
	fp, err := fingerprint(path)
	if err != nil {
		if info, statErr := os.Lstat(path); statErr == nil {
			fp = ports.FileFingerprint{Size: info.Size(), Mode: info.Mode().Perm()}
		}
	}
	return InventoryUnknown{Path: path, Fingerprint: fp, Reason: reason, Action: action}
}

func jsonDigest(key []byte, value any) (string, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return hmacHex(key, bytes), nil
}

func sameFingerprint(left, right ports.FileFingerprint) bool {
	return strings.EqualFold(left.SHA256Hex, right.SHA256Hex) && left.Size == right.Size && left.Mode.Perm() == right.Mode.Perm()
}
