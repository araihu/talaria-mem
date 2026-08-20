package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/spf13/cobra"
)

type ExitCode int

const (
	ExitSuccess        ExitCode = 0
	ExitInternal       ExitCode = 1
	ExitUsage          ExitCode = 2
	ExitConflict       ExitCode = 3
	ExitAuthentication ExitCode = 4
	ExitRefusal        ExitCode = 5
	ExitUnavailable    ExitCode = 6
	ExitNotFound       ExitCode = 7
)

type UsageError struct{ Message string }

func (err *UsageError) Error() string {
	if err == nil || err.Message == "" {
		return "usage error"
	}
	return err.Message
}

type Output struct {
	JSON   bool
	Stdout io.Writer
	Stderr io.Writer
}

func (output Output) writeMachine(value any) error {
	if output.Stdout == nil {
		return errors.New("stdout unavailable")
	}
	return json.NewEncoder(output.Stdout).Encode(value)
}

func (output Output) human(format string, values ...any) error {
	if output.Stderr == nil {
		return errors.New("stderr unavailable")
	}
	_, err := fmt.Fprintf(output.Stderr, format+"\n", values...)
	return err
}

func (output Output) Result(value any, human string, values ...any) error {
	if output.JSON {
		return output.writeMachine(value)
	}
	return output.human(human, values...)
}

type RootConfig struct {
	Registry   *Registry
	Memory     *MemoryCore
	Workspace  *WorkspaceCommands
	Projection *ProjectionCommands
	Stdout     io.Writer
	Stderr     io.Writer
}

type Root struct {
	registry   *Registry
	memory     *MemoryCore
	workspace  *WorkspaceCommands
	projection *ProjectionCommands
	stdout     io.Writer
	stderr     io.Writer
}

func NewRoot(config RootConfig) *Root {
	registry := config.Registry
	if registry == nil {
		registry = NewRegistry()
	}
	stdout, stderr := config.Stdout, config.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	return &Root{registry: registry, memory: config.Memory, workspace: config.Workspace, projection: config.Projection, stdout: stdout, stderr: stderr}
}

// NewBootstrapCommand builds the side-effect-free command tree used to answer
// root help and reject unknown top-level commands before runtime composition.
func NewBootstrapCommand(stdout, stderr io.Writer) *cobra.Command {
	registry := NewRegistry()
	for _, specification := range []struct {
		name        string
		description string
	}{
		{name: "daemon", description: "run local daemon"},
		{name: "setup", description: "install local integrations"},
		{name: "status", description: "show daemon status"},
		{name: "doctor", description: "diagnose local state"},
		{name: "token", description: "manage local bearer token"},
		{name: "db", description: "database maintenance"},
		{name: "scanner", description: "scanner rule maintenance"},
		{name: "mcp", description: "MCP client integrations"},
	} {
		_ = registry.Register(Command{
			Name:        specification.name,
			Description: specification.description,
			Run: func(context.Context, []string, Output) error {
				return errCommandUnavailable
			},
		})
	}
	return NewRoot(RootConfig{Registry: registry, Stdout: stdout, Stderr: stderr}).CobraCommand()
}

// CobraCommand is the composition-facing command tree. Cobra owns command
// discovery, help, flag parsing, positional validation, and completion; the
// registered handlers only receive normalized application arguments.
func (root *Root) CobraCommand() *cobra.Command {
	command, _ := root.newCobraCommand()
	return command
}

func (root *Root) newCobraCommand() (*cobra.Command, *bool) {
	jsonOutput := false
	output := func(command *cobra.Command) Output {
		return Output{JSON: jsonOutput, Stdout: command.OutOrStdout(), Stderr: command.ErrOrStderr()}
	}
	command := &cobra.Command{
		Use:              "talaria-mem",
		Short:            "local workspace-scoped memory",
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		Args:             cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return output(command).Result(map[string]any{
				"version":  "talaria.cli.v1",
				"commands": append([]string{"memory", "workspace", "projection"}, root.registry.Names()...),
			}, "talaria-mem: use memory, workspace, or projection")
		},
	}
	command.SetOut(root.stdout)
	command.SetErr(root.stderr)
	command.PersistentFlags().BoolVar(&jsonOutput, "json", false, "write machine-readable JSON to stdout")
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &UsageError{Message: err.Error()}
	})

	command.AddCommand(memoryCobraCommand(root.memory, output))
	command.AddCommand(workspaceCobraCommand(root.workspace, output))
	command.AddCommand(projectionCobraCommand(root.projection, output))
	for _, name := range root.registry.Names() {
		registered, found := root.registry.Lookup(name)
		if !found {
			continue
		}
		child := registeredCobraCommand(registered, CobraContext{Run: registered.Run, Output: output})
		command.AddCommand(child)
	}
	command.SetHelpCommand(&cobra.Command{
		Use:   "help",
		Short: "show the command contract",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return output(command).Result(map[string]any{
				"version":  "talaria.cli.v1",
				"commands": append([]string{"memory", "workspace", "projection"}, root.registry.Names()...),
			}, "talaria-mem: use memory, workspace, or projection")
		},
	})
	return command, &jsonOutput
}

func registeredCobraCommand(registered Command, context CobraContext) *cobra.Command {
	if registered.Build != nil {
		if command := registered.Build(context); command != nil {
			if command.Short == "" {
				command.Short = registered.Description
			}
			return command
		}
	}
	return &cobra.Command{
		Use:   registered.Name,
		Short: registered.Description,
		Args:  cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			return invokeCobra(context, command, args)
		},
	}
}

func (root *Root) Registry() *Registry {
	if root == nil {
		return nil
	}
	return root.registry
}

func (root *Root) Run(ctx context.Context, args []string) error {
	if root == nil {
		return &UsageError{Message: "CLI unavailable"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	command, _ := root.newCobraCommand()
	command.SetArgs(args)
	return normalizeCobraError(command.ExecuteContext(ctx))
}

func (root *Root) Execute(ctx context.Context, args []string) ExitCode {
	if root == nil {
		return ExitInternal
	}
	if err := ctx.Err(); err != nil {
		if outputErr := root.writeError(err, false); outputErr != nil {
			return ExitInternal
		}
		return ExitCodeFor(err)
	}
	command, jsonOutput := root.newCobraCommand()
	command.SetArgs(args)
	err := normalizeCobraError(command.ExecuteContext(ctx))
	if err == nil {
		return ExitSuccess
	}
	if outputErr := root.writeError(err, *jsonOutput); outputErr != nil {
		return ExitInternal
	}
	return ExitCodeFor(err)
}

func normalizeCobraError(err error) error {
	if err == nil {
		return nil
	}
	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return err
	}
	message := err.Error()
	if strings.HasPrefix(message, "unknown command ") || strings.HasPrefix(message, "unknown flag:") || strings.HasPrefix(message, "unknown shorthand flag:") || strings.Contains(message, "flag provided but not defined") || strings.Contains(message, "flag needs an argument") || strings.Contains(message, "invalid argument") || strings.Contains(message, "requires at least") || strings.Contains(message, "accepts ") {
		return &UsageError{Message: message}
	}
	return err
}

func (root *Root) writeError(err error, machine bool) error {
	if root == nil || root.stderr == nil {
		return err
	}
	code := ExitCodeFor(err)
	if machine && root.stdout != nil {
		return json.NewEncoder(root.stdout).Encode(map[string]any{"version": "talaria.cli.error.v1", "code": code, "message": safeCLIError(err)})
	}
	_, writeErr := fmt.Fprintln(root.stderr, safeCLIError(err))
	return writeErr
}

func ExitCodeFor(err error) ExitCode {
	if err == nil {
		return ExitSuccess
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return ExitUsage
	}
	if errors.Is(err, errCommandUnavailable) {
		return ExitUnavailable
	}
	var authentication AuthenticationError
	if errors.As(err, &authentication) {
		return ExitAuthentication
	}
	if typed := errorCode(err); typed != "" {
		switch typed {
		case "not-found":
			return ExitNotFound
		case "revision-conflict", "idempotency-conflict":
			return ExitConflict
		case "secret-refusal", "quarantine":
			return ExitRefusal
		case "unavailable", "timeout", "storage-full", "maintenance-lock":
			return ExitUnavailable
		}
	}
	return ExitInternal
}

func errorCode(err error) string {
	return string(domain.CodeOf(err))
}

func safeCLIError(err error) string {
	if err == nil {
		return ""
	}
	if code := domain.CodeOf(err); code != "" {
		switch code {
		case domain.CodeValidation:
			return "invalid request"
		case domain.CodeNotFound:
			return "not found"
		case domain.CodeRevisionConflict, domain.CodeIdempotencyConflict:
			return "conflict"
		case domain.CodeSecretRefusal:
			return "content refused"
		case domain.CodeQuarantine:
			return "content quarantined"
		case domain.CodeTimeout:
			return "request timed out"
		case domain.CodeUnavailable, domain.CodeStorageFull, domain.CodeMaintenanceLock:
			return "service unavailable"
		}
	}
	message := err.Error()
	if strings.ContainsAny(message, "\r\n") {
		return "command failed"
	}
	return message
}
