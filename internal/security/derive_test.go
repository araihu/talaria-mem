package security

import (
	"context"
	"errors"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

func TestKeyDerivationPurposeAndVersionSeparation(t *testing.T) {
	rootBytes := make([]byte, RootKeySize)
	rootBytes[0] = 1
	root, err := NewRootKey(rootBytes, RootKeyVersion)
	if err != nil {
		t.Fatal(err)
	}
	deriver, err := NewHKDFDeriver(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	keys := make(map[string]struct{})
	for _, purpose := range []ports.KeyPurpose{ports.KeyPurposeSession, ports.KeyPurposeIdempotency, ports.KeyPurposeBackupManifest} {
		key, err := deriver.DeriveKey(ctx, purpose, ports.KeyDerivationVersion)
		if err != nil {
			t.Fatal(err)
		}
		if len(key) != DerivedKeySize {
			t.Fatalf("key length = %d", len(key))
		}
		keys[string(key)] = struct{}{}
	}
	if len(keys) != 3 {
		t.Fatal("key purposes are not separated")
	}
	first, err := deriver.DeriveKey(ctx, ports.KeyPurposeSession, ports.KeyDerivationVersion)
	if err != nil {
		t.Fatal(err)
	}
	second, err := deriver.DeriveKey(ctx, ports.KeyPurposeSession, ports.KeyDerivationVersion)
	if err != nil || string(first) != string(second) {
		t.Fatal("derivation is not stable")
	}
	if _, err := deriver.DeriveKey(ctx, ports.KeyPurposeSession, ports.KeyDerivationVersion+1); !errors.Is(err, ErrUnsupportedKeyVersion) {
		t.Fatalf("version error = %v", err)
	}
	if _, err := deriver.DeriveKey(ctx, ports.KeyPurpose("invalid"), ports.KeyDerivationVersion); !errors.Is(err, ErrInvalidKeyPurpose) {
		t.Fatalf("purpose error = %v", err)
	}
}

func TestKeyDerivationCancellation(t *testing.T) {
	rootBytes := make([]byte, RootKeySize)
	rootBytes[0] = 1
	root, _ := NewRootKey(rootBytes, RootKeyVersion)
	deriver, _ := NewHKDFDeriver(root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := deriver.DeriveKey(ctx, ports.KeyPurposeSession, ports.KeyDerivationVersion); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}
