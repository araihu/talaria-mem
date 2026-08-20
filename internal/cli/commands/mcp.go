package commands

import (
	"context"
	"errors"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
	"github.com/spf13/cobra"
)

type MCPProxyOperator interface {
	Run(context.Context) error
}

type MCPCommands struct {
	Proxy MCPProxyOperator
}

func NewMCPCommands(proxy MCPProxyOperator) *MCPCommands {
	return &MCPCommands{Proxy: proxy}
}

func (commands *MCPCommands) Run(ctx context.Context, args []string, output cli.Output) error {
	if commands == nil || commands.Proxy == nil {
		return errors.New("MCP proxy unavailable")
	}
	if len(args) != 1 || args[0] != "proxy" {
		return &cli.UsageError{Message: "usage: mcp proxy"}
	}
	if err := commands.Proxy.Run(ctx); err != nil {
		return err
	}
	return output.Result(map[string]any{"version": "talaria.mcp-proxy.v1", "status": "stopped"}, "MCP proxy stopped")
}

func RegisterMCP(registry *cli.Registry, commands *MCPCommands) error {
	if registry == nil || commands == nil {
		return &cli.UsageError{Message: "MCP command unavailable"}
	}
	return registry.Register(cli.Command{Name: "mcp", Description: "MCP client integrations", Run: commands.Run, Build: mcpCobraCommand})
}

func mcpCobraCommand(context cli.CobraContext) *cobra.Command {
	command := &cobra.Command{Use: "mcp", Short: "MCP client integrations", Args: cobra.NoArgs}
	proxy := &cobra.Command{Use: "proxy", Short: "bridge MCP stdio to the local daemon", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		return invoke(context, command, []string{"proxy"})
	}}
	command.AddCommand(proxy)
	return command
}
