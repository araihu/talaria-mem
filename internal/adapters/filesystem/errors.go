package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
)

var (
	ErrUnsafePath   = errors.New("managed path unsafe")
	ErrInvalidMode  = errors.New("managed file mode invalid")
	ErrNotRegular   = errors.New("managed path is not a regular file")
	ErrTargetExists = errors.New("managed target already exists")
	// Wrap fs.ErrNotExist so generic ports can distinguish a safe missing
	// target from an unsafe filesystem failure without importing this adapter.
	ErrTargetMissing       = fmt.Errorf("managed target missing: %w", fs.ErrNotExist)
	ErrFingerprintMismatch = errors.New("managed target fingerprint mismatch")
	ErrDifferentDirectory  = errors.New("managed paths are not in the same directory")
	ErrInvalidPrefix       = errors.New("managed temporary prefix invalid")
)
