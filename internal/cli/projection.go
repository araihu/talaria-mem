package cli

import (
	"context"
	"fmt"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type ProjectionCommands struct{ Projector ports.Projector }

func NewProjectionCommands(projector ports.Projector) *ProjectionCommands {
	return &ProjectionCommands{Projector: projector}
}

func (commands *ProjectionCommands) Run(ctx context.Context, args []string, output Output) error {
	if commands == nil || commands.Projector == nil {
		return errCommandUnavailable
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		return output.Result(map[string]any{"version": "talaria.projection.v1", "commands": []string{"rebuild"}}, "projection commands: rebuild")
	}
	if args[0] != "rebuild" {
		return &UsageError{Message: fmt.Sprintf("unknown projection command %q", args[0])}
	}
	flags, _, err := parseFlags(args[1:])
	if err != nil {
		return err
	}
	scope := flags["workspace"]
	if scope == "" {
		return &UsageError{Message: "projection rebuild requires --workspace"}
	}
	if err := commands.Projector.Project(ctx, ports.ProjectionRequest{ScopeID: scope}); err != nil {
		return err
	}
	return output.Result(map[string]any{"version": "talaria.projection-rebuild.v1", "workspace_id": scope}, "projection rebuilt for %s", scope)
}
