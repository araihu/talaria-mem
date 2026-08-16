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

// CobraCommand is the composition-facing command tree. The subcommand
// handlers remain in this package so T12-T14 can register their commands
// explicitly without package initialization side effects.
func (root *Root) CobraCommand() *cobra.Command {
	command := &cobra.Command{
		Use:                "talaria-mem",
		Short:              "local workspace-scoped memory",
		SilenceUsage:       true,
		SilenceErrors:      true,
		DisableFlagParsing: true,
		RunE: func(command *cobra.Command, args []string) error {
			root.stdout = command.OutOrStdout()
			root.stderr = command.ErrOrStderr()
			return root.Run(command.Context(), args)
		},
	}
	command.SetOut(root.stdout)
	command.SetErr(root.stderr)
	for _, name := range []string{"memory", "workspace", "projection"} {
		name := name
		child := &cobra.Command{
			Use:                name,
			DisableFlagParsing: true,
			SilenceUsage:       true,
			SilenceErrors:      true,
			RunE: func(child *cobra.Command, args []string) error {
				root.stdout = child.OutOrStdout()
				root.stderr = child.ErrOrStderr()
				return root.Run(child.Context(), append([]string{name}, args...))
			},
		}
		child.SetOut(root.stdout)
		child.SetErr(root.stderr)
		command.AddCommand(child)
	}
	for _, name := range root.registry.Names() {
		name := name
		child := &cobra.Command{
			Use:                name,
			DisableFlagParsing: true,
			SilenceUsage:       true,
			SilenceErrors:      true,
			RunE: func(child *cobra.Command, args []string) error {
				root.stdout = child.OutOrStdout()
				root.stderr = child.ErrOrStderr()
				return root.Run(child.Context(), append([]string{name}, args...))
			},
		}
		child.SetOut(root.stdout)
		child.SetErr(root.stderr)
		command.AddCommand(child)
	}
	return command
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
	jsonOutput, args := takeJSONFlag(args)
	output := Output{JSON: jsonOutput, Stdout: root.stdout, Stderr: root.stderr}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return output.Result(map[string]any{"version": "talaria.cli.v1", "commands": append([]string{"memory", "workspace", "projection"}, root.registry.Names()...)}, "talaria-mem: use memory, workspace, or projection")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	command, rest := args[0], args[1:]
	switch command {
	case "memory":
		if root.memory == nil {
			return errCommandUnavailable
		}
		return root.memory.Run(ctx, rest, output)
	case "workspace":
		if root.workspace == nil {
			return errCommandUnavailable
		}
		return root.workspace.Run(ctx, rest, output)
	case "projection":
		if root.projection == nil {
			return errCommandUnavailable
		}
		return root.projection.Run(ctx, rest, output)
	default:
		registered, found := root.registry.Lookup(command)
		if !found {
			return &UsageError{Message: fmt.Sprintf("unknown command %q", command)}
		}
		return registered.Run(ctx, rest, output)
	}
}

func (root *Root) Execute(ctx context.Context, args []string) ExitCode {
	err := root.Run(ctx, args)
	if err == nil {
		return ExitSuccess
	}
	if outputErr := root.writeError(err, containsJSONFlag(args)); outputErr != nil {
		return ExitInternal
	}
	return ExitCodeFor(err)
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

func takeJSONFlag(args []string) (bool, []string) {
	filtered := make([]string, 0, len(args))
	jsonOutput := false
	for _, arg := range args {
		if arg == "--json" {
			jsonOutput = true
			continue
		}
		filtered = append(filtered, arg)
	}
	return jsonOutput, filtered
}

func containsJSONFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}
