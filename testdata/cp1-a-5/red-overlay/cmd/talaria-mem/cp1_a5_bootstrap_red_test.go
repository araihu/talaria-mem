package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// T1 RED proves the bootstrap package remains provider-free. The rejected
// cp1-a-4 source imported T9 from main.go, so the package dependency graph
// exposed a concrete filesystem before the T14 composition root existed.
func TestBootstrapRemainsProviderFree(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/bootstrap/cp1-a-5-provider-free.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fixture), "internal/adapters/filesystem") {
		t.Fatal("provider-free fixture is not bound to the forbidden dependency")
	}
	command := exec.Command("go", "list", "-deps", ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap dependency graph failed: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "internal/adapters/filesystem") {
		t.Fatal("provider-free bootstrap depends on concrete filesystem")
	}
}
