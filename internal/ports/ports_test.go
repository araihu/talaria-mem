package ports

import (
	"context"
	"crypto/sha256"
	"io"
	"io/fs"
	"testing"
	"time"
)

type fakeKeyDeriver struct{}

func (fakeKeyDeriver) DeriveKey(_ context.Context, purpose KeyPurpose, version uint32) ([]byte, error) {
	value := sha256.Sum256([]byte(string(purpose) + ":" + string(rune(version))))
	return value[:], nil
}

func TestKeyDeriverPurposeSeparation(t *testing.T) {
	t.Parallel()
	var deriver KeyDeriver = fakeKeyDeriver{}
	ctx := context.Background()
	seen := map[string]bool{}
	for _, purpose := range []KeyPurpose{KeyPurposeSession, KeyPurposeIdempotency, KeyPurposeBackupManifest} {
		key, err := deriver.DeriveKey(ctx, purpose, KeyDerivationVersion)
		if err != nil {
			t.Fatal(err)
		}
		seen[string(key)] = true
	}
	if len(seen) != 3 {
		t.Fatal("key purposes are not domain separated")
	}
}

type fakeManagedFileStore struct{}

func (fakeManagedFileStore) CreateTemp(context.Context, string, string, fs.FileMode) (ManagedTempFile, error) {
	return ManagedTempFile{}, nil
}
func (fakeManagedFileStore) SyncFile(context.Context, string) error { return nil }
func (fakeManagedFileStore) ReplaceNoFollow(context.Context, string, string, *FileFingerprint) error {
	return nil
}
func (fakeManagedFileStore) SyncParent(context.Context, string) error { return nil }
func (fakeManagedFileStore) CleanupStaleTemps(context.Context, string, string, time.Time) error {
	return nil
}
func (fakeManagedFileStore) FingerprintNoFollow(context.Context, string) (FileFingerprint, error) {
	return FileFingerprint{}, nil
}

func TestManagedFileStoreContract(t *testing.T) {
	t.Parallel()
	var store ManagedFileStore = fakeManagedFileStore{}
	if store == nil {
		t.Fatal("nil managed file store")
	}
	if ManagedFileMode != 0o600 {
		t.Fatalf("ManagedFileMode = %o", ManagedFileMode)
	}
	var _ io.WriteCloser = ManagedTempFile{}.Writer
}
