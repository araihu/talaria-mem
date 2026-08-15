package ports

import (
	"context"
	"io"
	"io/fs"
	"time"
)

const ManagedFileMode fs.FileMode = 0o600

type FileFingerprint struct {
	SHA256Hex string
	Size      int64
	Mode      fs.FileMode
}

type ManagedTempFile struct {
	Path   string
	Writer io.WriteCloser
}

type ManagedFileStore interface {
	CreateTemp(ctx context.Context, directory, prefix string, mode fs.FileMode) (ManagedTempFile, error)
	SyncFile(ctx context.Context, path string) error
	ReplaceNoFollow(ctx context.Context, tempPath, targetPath string, expected *FileFingerprint) error
	SyncParent(ctx context.Context, path string) error
	CleanupStaleTemps(ctx context.Context, directory, prefix string, olderThan time.Time) error
	FingerprintNoFollow(ctx context.Context, path string) (FileFingerprint, error)
}
