package security

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

func TestCurationCipherPurposeSeparationAndSessionDigest(t *testing.T) {
	root, err := GenerateRootKey()
	if err != nil {
		t.Fatal(err)
	}
	deriver, err := NewHKDFDeriver(root)
	if err != nil {
		t.Fatal(err)
	}
	keys := make(map[ports.KeyPurpose][]byte)
	for _, purpose := range []ports.KeyPurpose{ports.KeyPurposeCurationSession, ports.KeyPurposeCurationLocator, ports.KeyPurposeCurationSnapshot} {
		key, err := deriver.DeriveKey(context.Background(), purpose, ports.KeyDerivationVersion)
		if err != nil {
			t.Fatal(err)
		}
		keys[purpose] = key
	}
	if bytes.Equal(keys[ports.KeyPurposeCurationSession], keys[ports.KeyPurposeCurationLocator]) || bytes.Equal(keys[ports.KeyPurposeCurationLocator], keys[ports.KeyPurposeCurationSnapshot]) {
		t.Fatal("curation key purposes reused the same derived key")
	}
	cipher, err := NewCurationCipher(deriver)
	if err != nil {
		t.Fatal(err)
	}
	first, err := cipher.SessionDigest(context.Background(), []byte("session-1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.SessionDigest(context.Background(), []byte("session-1"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := cipher.SessionDigest(context.Background(), []byte("session-2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 || !bytes.Equal(first, second) || bytes.Equal(first, other) {
		t.Fatalf("session digest identity mismatch: %x %x %x", first, second, other)
	}
}

func TestCurationCipherLocatorAndSnapshotAuthentication(t *testing.T) {
	root, err := GenerateRootKey()
	if err != nil {
		t.Fatal(err)
	}
	deriver, err := NewHKDFDeriver(root)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewCurationCipher(deriver)
	if err != nil {
		t.Fatal(err)
	}
	metadata := []byte("workspace\x00reason=periodic\x00watermark=10")
	locator := []byte("/private/thread/opaque")
	snapshot := []byte("sanitized snapshot content")
	sealedLocator, err := cipher.SealLocator(context.Background(), "job-1", metadata, locator)
	if err != nil {
		t.Fatal(err)
	}
	sealedLocatorAgain, err := cipher.SealLocator(context.Background(), "job-1", metadata, locator)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sealedLocator, sealedLocatorAgain) {
		t.Fatal("locator sealing reused nonce")
	}
	openedLocator, err := cipher.OpenLocator(context.Background(), "job-1", metadata, sealedLocator)
	if err != nil || !bytes.Equal(openedLocator, locator) {
		t.Fatalf("locator open = %q, err=%v", openedLocator, err)
	}
	if _, err := cipher.OpenLocator(context.Background(), "job-2", metadata, sealedLocator); !errors.Is(err, ErrCurationCipher) {
		t.Fatalf("wrong job accepted locator: %v", err)
	}
	if _, err := cipher.OpenLocator(context.Background(), "job-1", []byte("changed"), sealedLocator); !errors.Is(err, ErrCurationCipher) {
		t.Fatalf("changed metadata accepted locator: %v", err)
	}
	tampered := append([]byte(nil), sealedLocator...)
	tampered[len(tampered)-1] ^= 1
	if _, err := cipher.OpenLocator(context.Background(), "job-1", metadata, tampered); !errors.Is(err, ErrCurationCipher) {
		t.Fatalf("tampered locator accepted: %v", err)
	}
	sealedSnapshot, err := cipher.SealSnapshot(context.Background(), "job-1", metadata, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	openedSnapshot, err := cipher.OpenSnapshot(context.Background(), "job-1", metadata, sealedSnapshot)
	if err != nil || !bytes.Equal(openedSnapshot, snapshot) {
		t.Fatalf("snapshot open = %q, err=%v", openedSnapshot, err)
	}
	if _, err := cipher.OpenSnapshot(context.Background(), "job-1", metadata, sealedLocator); !errors.Is(err, ErrCurationCipher) {
		t.Fatalf("locator ciphertext accepted as snapshot: %v", err)
	}
}

func TestCurationCipherBoundsAndErrorHygiene(t *testing.T) {
	root, err := GenerateRootKey()
	if err != nil {
		t.Fatal(err)
	}
	deriver, err := NewHKDFDeriver(root)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewCurationCipher(deriver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cipher.SealLocator(context.Background(), "job", nil, bytes.Repeat([]byte{'x'}, MaxCurationLocatorPlaintext+1)); !errors.Is(err, ErrCurationPlaintextTooLarge) {
		t.Fatalf("oversized locator error = %v", err)
	}
	if _, err := cipher.SealSnapshot(context.Background(), "job", nil, bytes.Repeat([]byte{'x'}, MaxCurationSnapshotPlaintext+1)); !errors.Is(err, ErrCurationPlaintextTooLarge) {
		t.Fatalf("oversized snapshot error = %v", err)
	}
	sealed, err := cipher.SealSnapshot(context.Background(), "job", nil, []byte("secret canary"))
	if err != nil {
		t.Fatal(err)
	}
	sealed = sealed[:len(sealed)-1]
	_, err = cipher.OpenSnapshot(context.Background(), "job", nil, sealed)
	if !errors.Is(err, ErrCurationCipher) || strings.Contains(err.Error(), "secret canary") {
		t.Fatalf("cipher error leaked plaintext: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cipher.SessionDigest(ctx, []byte("session")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled digest error = %v", err)
	}
}
