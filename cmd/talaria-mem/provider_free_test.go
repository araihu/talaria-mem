package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// T1 proves the bootstrap package remains provider-free. Final T14 wiring is
// intentionally deferred; the package dependency graph must not reach T9.
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
