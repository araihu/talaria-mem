package ports

import "context"

type KeyPurpose string

const (
	KeyPurposeSession        KeyPurpose = "talaria-mem/session"
	KeyPurposeIdempotency    KeyPurpose = "talaria-mem/idempotency"
	KeyPurposeBackupManifest KeyPurpose = "talaria-mem/backup-manifest"
	KeyDerivationVersion     uint32     = 1
)

func (purpose KeyPurpose) Valid() bool {
	switch purpose {
	case KeyPurposeSession, KeyPurposeIdempotency, KeyPurposeBackupManifest:
		return true
	default:
		return false
	}
}

type KeyDeriver interface {
	DeriveKey(ctx context.Context, purpose KeyPurpose, version uint32) ([]byte, error)
}
