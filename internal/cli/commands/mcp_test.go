package commands

import (
	"bytes"
	"context"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
)

type mcpProxyFixture struct{ calls int }

func (fixture *mcpProxyFixture) Run(context.Context) error {
	fixture.calls++
	return nil
}

func TestMCPProxyCommandRunsOnlyExplicitProxy(t *testing.T) {
	fixture := &mcpProxyFixture{}
	registry := cli.NewRegistry()
	if err := RegisterMCP(registry, NewMCPCommands(fixture)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootConfig{Registry: registry, Stdout: &stdout, Stderr: &stderr})
	if code := root.Execute(context.Background(), []string{"mcp", "proxy", "--json"}); code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if fixture.calls != 1 || !bytes.Contains(stdout.Bytes(), []byte(`"version":"talaria.mcp-proxy.v1"`)) {
		t.Fatalf("calls=%d stdout=%q", fixture.calls, stdout.String())
	}
	if err := (&MCPCommands{Proxy: fixture}).Run(context.Background(), nil, cli.Output{Stdout: &stdout, Stderr: &stderr}); err == nil {
		t.Fatal("missing subcommand accepted")
	}
}
