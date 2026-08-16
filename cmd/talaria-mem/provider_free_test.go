package main

import (
	"os/exec"
	"strings"
	"testing"
)

// T14 replaces the provider-free bootstrap shell with one explicit runtime
// graph. The graph must still be rooted in this single binary package.
func TestFinalCompositionUsesRuntimeGraph(t *testing.T) {
	command := exec.Command("go", "list", "-deps", ".")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("binary dependency graph failed: %v\n%s", err, output)
	}
	graph := string(output)
	for _, dependency := range []string{
		"github.com/guilhermecastro/talaria-mem/internal/runtime",
		"github.com/guilhermecastro/talaria-mem/internal/adapters/filesystem",
	} {
		if !strings.Contains(graph, dependency) {
			t.Fatalf("final composition does not reach %s", dependency)
		}
	}
}
