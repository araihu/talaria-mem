package security

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBearerTokenCreateLoadCompareAndRotate(t *testing.T) {
	store, err := NewTokenStore(filepath.Join(secureTestDirectory(t), "token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrTokenMissing) {
		t.Fatalf("missing token error = %v", err)
	}
	first, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	if !CompareBearerToken(first, first) || CompareBearerToken(first, first+"x") {
		t.Fatal("token comparison contract failed")
	}
	loaded, err := store.Load()
	if err != nil || loaded != first {
		t.Fatalf("loaded token = %q, error = %v", loaded, err)
	}
	second, err := store.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if second == first || CompareBearerToken(first, second) || !CompareBearerToken(second, second) {
		t.Fatal("token rotation failed")
	}
	if err := store.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(); !errors.Is(err, ErrTokenExists) {
		t.Fatalf("second token creation error = %v", err)
	}
}

func TestBearerAuthorizationStrictSyntax(t *testing.T) {
	token, err := GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Bearer " + token, "bearer " + token} {
		parsed, err := ParseBearerAuthorization(value)
		if err != nil || parsed != token {
			t.Fatalf("parse %q: %q, %v", value[:7], parsed, err)
		}
	}
	for _, value := range []string{"Bearer", "Bearer  " + token, "Bearer " + token + " ", "Basic " + token, "Bearer token"} {
		if _, err := ParseBearerAuthorization(value); !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("accepted invalid authorization %q", value[:min(len(value), 12)])
		}
	}
}

func TestTokenRejectsSymlinkAndMalformedBytes(t *testing.T) {
	directory := secureTestDirectory(t)
	realPath := filepath.Join(directory, "real")
	linkPath := filepath.Join(directory, "token")
	if err := os.WriteFile(realPath, []byte("not-a-token"), ProtectedFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}
	store, _ := NewTokenStore(linkPath)
	if _, err := store.Load(); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("symlink token error = %v", err)
	}
}
