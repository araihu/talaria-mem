package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

const BearerTokenBytes = 32

var (
	ErrTokenMissing = errors.New("bearer token unavailable")
	ErrTokenInvalid = errors.New("bearer token invalid")
	ErrTokenExists  = errors.New("bearer token already exists")
	ErrTokenUnsafe  = errors.New("bearer token path unsafe")
)

// GenerateBearerToken returns an opaque, URL-safe token. The token is
// independent of the root key and therefore rotation does not invalidate
// derived session, idempotency, or backup-manifest keys.
func GenerateBearerToken() (string, error) {
	raw := make([]byte, BearerTokenBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate bearer token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ValidateBearerTokenSyntax accepts only the representation emitted by
// GenerateBearerToken. It rejects whitespace, control characters, padding,
// and ambiguous alternate encodings.
func ValidateBearerTokenSyntax(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(BearerTokenBytes) {
		return false
	}
	for _, character := range token {
		if unicode.IsSpace(character) || character < 0x21 || character > 0x7e || character == '=' {
			return false
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == BearerTokenBytes
}

// CompareBearerToken performs a fixed-size constant-time comparison even if
// an attacker supplies a token with a different length.
func CompareBearerToken(expected, provided string) bool {
	expectedDigest := sha256Bytes([]byte(expected))
	providedDigest := sha256Bytes([]byte(provided))
	valid := ValidateBearerTokenSyntax(expected) && ValidateBearerTokenSyntax(provided)
	return valid && subtle.ConstantTimeCompare(expectedDigest[:], providedDigest[:]) == 1
}

// ParseBearerAuthorization parses the only accepted credential transport.
// Scheme matching follows HTTP's case-insensitive token rule, while spacing
// and token syntax stay strict to avoid alternate credential channels.
func ParseBearerAuthorization(header string) (string, error) {
	if header == "" || strings.Count(header, " ") != 1 {
		return "", ErrTokenInvalid
	}
	parts := strings.SplitN(header, " ", 2)
	if !strings.EqualFold(parts[0], "Bearer") || !ValidateBearerTokenSyntax(parts[1]) {
		return "", ErrTokenInvalid
	}
	return parts[1], nil
}

type TokenStore struct{ Path string }

func NewTokenStore(path string) (TokenStore, error) {
	if path == "" {
		return TokenStore{}, ErrTokenUnsafe
	}
	return TokenStore{Path: path}, nil
}

func (store TokenStore) Create() (string, error) {
	if err := validateProtectedParent(store.Path); err != nil {
		return "", err
	}
	token, err := GenerateBearerToken()
	if err != nil {
		return "", err
	}
	file, err := openProtected(store.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, ProtectedFileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", ErrTokenExists
		}
		return "", classifyTokenPathError(err)
	}
	created := false
	defer func() {
		if !created {
			_ = file.Close()
			_ = os.Remove(store.Path)
		}
	}()
	if err := validateOpenedProtectedFile(file, -1); err != nil {
		return "", err
	}
	if _, err := file.WriteString(token); err != nil {
		return "", classifyTokenPathError(err)
	}
	if err := file.Sync(); err != nil {
		return "", classifyTokenPathError(err)
	}
	if err := file.Close(); err != nil {
		return "", classifyTokenPathError(err)
	}
	if err := syncProtectedParent(store.Path); err != nil {
		return "", err
	}
	created = true
	return token, nil
}

func (store TokenStore) Load() (string, error) {
	if err := validateProtectedParent(store.Path); err != nil {
		return "", err
	}
	info, err := os.Lstat(store.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrTokenMissing
		}
		return "", classifyTokenPathError(err)
	}
	if err := validateProtectedInfo(info, -1); err != nil {
		return "", ErrTokenInvalid
	}
	if info.Size() != int64(base64.RawURLEncoding.EncodedLen(BearerTokenBytes)) {
		return "", ErrTokenInvalid
	}
	file, err := openProtected(store.Path, os.O_RDONLY, 0)
	if err != nil {
		return "", classifyTokenPathError(err)
	}
	defer file.Close()
	if err := validateOpenedProtectedFile(file, info.Size()); err != nil {
		return "", ErrTokenInvalid
	}
	raw := make([]byte, info.Size())
	if _, err := io.ReadFull(file, raw); err != nil {
		return "", ErrTokenInvalid
	}
	var extra [1]byte
	if count, err := file.Read(extra[:]); err != nil && !errors.Is(err, io.EOF) {
		return "", ErrTokenInvalid
	} else if count != 0 {
		return "", ErrTokenInvalid
	}
	token := string(raw)
	if !ValidateBearerTokenSyntax(token) {
		return "", ErrTokenInvalid
	}
	return token, nil
}

func (store TokenStore) Validate() error {
	_, err := store.Load()
	return err
}

// Rotate changes only the token file. It first validates the existing token,
// then replaces it through a same-directory fsync/rename sequence.
func (store TokenStore) Rotate() (string, error) {
	if _, err := store.Load(); err != nil {
		return "", err
	}
	token, err := GenerateBearerToken()
	if err != nil {
		return "", err
	}
	if err := atomicReplaceProtected(store.Path, []byte(token), ProtectedFileMode); err != nil {
		return "", err
	}
	return token, nil
}

func CreateBearerToken(path string) (string, error) {
	store, err := NewTokenStore(path)
	if err != nil {
		return "", err
	}
	return store.Create()
}

func LoadBearerToken(path string) (string, error) {
	store, err := NewTokenStore(path)
	if err != nil {
		return "", err
	}
	return store.Load()
}

func RotateBearerToken(path string) (string, error) {
	store, err := NewTokenStore(path)
	if err != nil {
		return "", err
	}
	return store.Rotate()
}

func ConstantTimeTokenEqual(expected, provided string) bool {
	return CompareBearerToken(expected, provided)
}

func classifyTokenPathError(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ErrTokenMissing
	case errors.Is(err, os.ErrPermission):
		return ErrTokenUnsafe
	default:
		return fmt.Errorf("protected token operation failed: %w", err)
	}
}

func sha256Bytes(value []byte) [32]byte {
	// Kept as a tiny local helper so token comparison does not expose token
	// bytes through an error, log, or formatting path.
	return sha256.Sum256(value)
}
