package main

import (
	"os"
	"strings"
	"testing"
)

// T1 RED proves the rejected source lacks the required T9 composition
// registration that lets T3 consume an injected filesystem contract.
func TestBootstrapCompositionRegistersManagedFilesystem(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/bootstrap/cp1-a-4-composition-registration.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fixture), "managed-filesystem-composition") {
		t.Fatal("composition fixture is not bound to the T1 contract")
	}
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `internal/adapters/filesystem`) {
		t.Fatal("bootstrap does not register the managed filesystem composition")
	}
}
