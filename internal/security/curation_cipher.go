package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

const (
	MaxCurationLocatorPlaintext  = 8 * 1024
	MaxCurationSnapshotPlaintext = 128 * 1024
	maxCurationJobIDBytes        = 256
	maxCurationMetadataBytes     = 4 * 1024
)

var (
	ErrCurationCipher            = errors.New("curation ciphertext invalid")
	ErrCurationPlaintextTooLarge = errors.New("curation plaintext exceeds limit")
	ErrCurationKeyUnavailable    = errors.New("curation key unavailable")
)

// CurationCipher owns the purpose-separated cryptographic boundary for
// retained automatic-curation data. SQLite receives only the output bytes.
type CurationCipher struct {
	deriver ports.KeyDeriver
}

func NewCurationCipher(deriver ports.KeyDeriver) (CurationCipher, error) {
	if deriver == nil {
		return CurationCipher{}, ErrCurationKeyUnavailable
	}
	return CurationCipher{deriver: deriver}, nil
}

func (cc CurationCipher) SessionDigest(ctx context.Context, sessionID []byte) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if len(sessionID) == 0 || len(sessionID) > maxCurationMetadataBytes {
		return nil, ErrCurationCipher
	}
	key, err := cc.derive(ctx, ports.KeyPurposeCurationSession)
	if err != nil {
		return nil, err
	}
	defer clearBytes(key)
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(sessionID)
	return digest.Sum(nil), nil
}

func (cc CurationCipher) SealLocator(ctx context.Context, jobID string, metadata, plaintext []byte) ([]byte, error) {
	return cc.seal(ctx, ports.KeyPurposeCurationLocator, "locator", MaxCurationLocatorPlaintext, jobID, metadata, plaintext)
}

func (cc CurationCipher) OpenLocator(ctx context.Context, jobID string, metadata, ciphertext []byte) ([]byte, error) {
	return cc.open(ctx, ports.KeyPurposeCurationLocator, "locator", MaxCurationLocatorPlaintext, jobID, metadata, ciphertext)
}

func (cc CurationCipher) SealSnapshot(ctx context.Context, jobID string, metadata, plaintext []byte) ([]byte, error) {
	return cc.seal(ctx, ports.KeyPurposeCurationSnapshot, "snapshot", MaxCurationSnapshotPlaintext, jobID, metadata, plaintext)
}

func (cc CurationCipher) OpenSnapshot(ctx context.Context, jobID string, metadata, ciphertext []byte) ([]byte, error) {
	return cc.open(ctx, ports.KeyPurposeCurationSnapshot, "snapshot", MaxCurationSnapshotPlaintext, jobID, metadata, ciphertext)
}

func (cc CurationCipher) seal(ctx context.Context, purpose ports.KeyPurpose, kind string, maxPlaintext int, jobID string, metadata, plaintext []byte) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !validCurationIdentity(jobID, metadata) {
		return nil, ErrCurationCipher
	}
	if len(plaintext) > maxPlaintext {
		return nil, ErrCurationPlaintextTooLarge
	}
	key, err := cc.derive(ctx, purpose)
	if err != nil {
		return nil, err
	}
	defer clearBytes(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrCurationCipher
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrCurationCipher
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, ErrCurationCipher
	}
	aad := curationAAD(kind, jobID, metadata)
	defer clearBytes(aad)
	sealed := make([]byte, 0, len(nonce)+len(plaintext)+gcm.Overhead())
	sealed = append(sealed, nonce...)
	sealed = gcm.Seal(sealed, nonce, plaintext, aad)
	clearBytes(nonce)
	return sealed, nil
}

func (cc CurationCipher) open(ctx context.Context, purpose ports.KeyPurpose, kind string, maxPlaintext int, jobID string, metadata, ciphertext []byte) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if !validCurationIdentity(jobID, metadata) {
		return nil, ErrCurationCipher
	}
	key, err := cc.derive(ctx, purpose)
	if err != nil {
		return nil, err
	}
	defer clearBytes(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrCurationCipher
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrCurationCipher
	}
	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() || len(ciphertext) > gcm.NonceSize()+gcm.Overhead()+maxPlaintext {
		return nil, ErrCurationCipher
	}
	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	aad := curationAAD(kind, jobID, metadata)
	defer clearBytes(aad)
	plaintext, err := gcm.Open(nil, nonce, sealed, aad)
	if err != nil || len(plaintext) > maxPlaintext {
		if plaintext != nil {
			clearBytes(plaintext)
		}
		return nil, ErrCurationCipher
	}
	return plaintext, nil
}

func (cc CurationCipher) derive(ctx context.Context, purpose ports.KeyPurpose) ([]byte, error) {
	if cc.deriver == nil {
		return nil, ErrCurationKeyUnavailable
	}
	key, err := cc.deriver.DeriveKey(ctx, purpose, ports.KeyDerivationVersion)
	if err != nil {
		if contextError(ctx) != nil {
			return nil, contextError(ctx)
		}
		return nil, ErrCurationKeyUnavailable
	}
	if len(key) != DerivedKeySize {
		clearBytes(key)
		return nil, ErrCurationKeyUnavailable
	}
	return key, nil
}

func validCurationIdentity(jobID string, metadata []byte) bool {
	return jobID != "" && len(jobID) <= maxCurationJobIDBytes && len(metadata) <= maxCurationMetadataBytes && !strings.ContainsAny(jobID, "\x00\r\n")
}

func curationAAD(kind, jobID string, metadata []byte) []byte {
	// Length-prefix each component to prevent concatenation ambiguity.
	aad := make([]byte, 0, len(kind)+len(jobID)+len(metadata)+24)
	aad = appendLengthPrefixed(aad, []byte("talaria-mem/curation-aead/v1/"+kind))
	aad = appendLengthPrefixed(aad, []byte(jobID))
	aad = appendLengthPrefixed(aad, metadata)
	return aad
}

func appendLengthPrefixed(destination, value []byte) []byte {
	var length [4]byte
	length[0] = byte(len(value) >> 24)
	length[1] = byte(len(value) >> 16)
	length[2] = byte(len(value) >> 8)
	length[3] = byte(len(value))
	destination = append(destination, length[:]...)
	return append(destination, value...)
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
