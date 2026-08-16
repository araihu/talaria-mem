package filesystem

import "errors"

var (
	ErrUnsafePath          = errors.New("managed path unsafe")
	ErrInvalidMode         = errors.New("managed file mode invalid")
	ErrNotRegular          = errors.New("managed path is not a regular file")
	ErrTargetExists        = errors.New("managed target already exists")
	ErrTargetMissing       = errors.New("managed target missing")
	ErrFingerprintMismatch = errors.New("managed target fingerprint mismatch")
	ErrDifferentDirectory  = errors.New("managed paths are not in the same directory")
	ErrInvalidPrefix       = errors.New("managed temporary prefix invalid")
)
