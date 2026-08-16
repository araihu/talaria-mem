package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"golang.org/x/crypto/hkdf"
)

const DerivedKeySize = 32

var (
	ErrInvalidKeyPurpose     = errors.New("invalid key purpose")
	ErrUnsupportedKeyVersion = errors.New("unsupported key version")
	ErrInvalidRootKey        = errors.New("invalid root key")
)

// HKDFDeriver derives independent fixed-size keys from one installation root.
// The info string includes both an explicit protocol namespace and the fixed
// version, preventing accidental cross-version or cross-purpose reuse.
type HKDFDeriver struct{ root RootKey }

func NewHKDFDeriver(root RootKey) (HKDFDeriver, error) {
	if !root.valid() {
		return HKDFDeriver{}, ErrInvalidRootKey
	}
	return HKDFDeriver{root: root}, nil
}

func NewKeyDeriver(root RootKey) (HKDFDeriver, error) { return NewHKDFDeriver(root) }

func (deriver HKDFDeriver) DeriveKey(ctx context.Context, purpose ports.KeyPurpose, version uint32) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !purpose.Valid() {
		return nil, ErrInvalidKeyPurpose
	}
	if version != ports.KeyDerivationVersion {
		return nil, ErrUnsupportedKeyVersion
	}
	if version != RootKeyVersion {
		return nil, ErrUnsupportedKeyVersion
	}
	info := []byte(fmt.Sprintf("talaria-mem/hkdf-sha256/v%d/%s", version, strings.TrimSpace(string(purpose))))
	reader := hkdf.New(sha256.New, deriver.root.bytes[:], nil, info)
	key := make([]byte, DerivedKeySize)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	return key, nil
}

// Derive is a convenience alias for callers that do not need the port name.
func (deriver HKDFDeriver) Derive(ctx context.Context, purpose ports.KeyPurpose, version uint32) ([]byte, error) {
	return deriver.DeriveKey(ctx, purpose, version)
}

// Digest computes a fixed-size HMAC digest using a purpose-separated subkey.
// It is useful for session and idempotency identifiers; raw identifiers must
// never be persisted in its place.
func (deriver HKDFDeriver) Digest(ctx context.Context, purpose ports.KeyPurpose, version uint32, value []byte) ([]byte, error) {
	key, err := deriver.DeriveKey(ctx, purpose, version)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil), nil
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

var _ ports.KeyDeriver = HKDFDeriver{}
