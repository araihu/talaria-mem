package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
	runtimegraph "github.com/guilhermecastro/talaria-mem/internal/runtime"
)

const usage = `talaria-mem stores local, workspace-scoped memory.

Usage:
  talaria-mem --help
  talaria-mem daemon [--foreground|--diagnostic]
  talaria-mem setup codex --dry-run|--apply [--remove]
  talaria-mem status
  talaria-mem doctor [--repair=fts --dry-run|--apply <receipt>]
  talaria-mem token rotate
  talaria-mem db backup reconcile --dry-run|--apply <receipt>
  talaria-mem memory ...
  talaria-mem workspace ...
  talaria-mem projection ...
`

var ErrBootstrapOutputUnavailable = errors.New("bootstrap output is unavailable")

// UsageError reports invalid bootstrap command-line input.
type UsageError struct {
	Command string
}

func (err *UsageError) Error() string {
	return fmt.Sprintf("unknown command %q", err.Command)
}

// IsUsageError reports whether err represents command-line usage failure.
func IsUsageError(err error) bool {
	var usageErr *UsageError
	return errors.As(err, &usageErr)
}

// Run is the one-binary composition boundary. Help and usage failures are
// resolved before composition so they never create local state.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if stdout == nil {
		return ErrBootstrapOutputUnavailable
	}

	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		_, err := io.WriteString(stdout, usage)
		return err
	}
	if len(args) == 0 {
		return &UsageError{Command: ""}
	}
	if !knownCommand(args[0]) {
		return &UsageError{Command: args[0]}
	}
	environment, err := lifecycle.ParseEnvironmentOSOrDefault()
	if err != nil {
		return err
	}
	configuration := runtimegraph.Config{
		Environment: environment,
		Stdout:      stdout,
		Stderr:      stderr,
	}
	if args[0] == "setup" {
		return runtimegraph.RunSetup(ctx, args, configuration)
	}
	composition, err := runtimegraph.New(ctx, configuration)
	if err != nil {
		return err
	}
	defer func() { _ = composition.Close() }()
	return composition.Run(ctx, args)
}

func knownCommand(command string) bool {
	switch command {
	case "memory", "workspace", "projection", "db", "daemon", "setup", "status", "doctor", "token", "scanner":
		return true
	default:
		return false
	}
}

func main() {
	err := Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, err)
	if IsUsageError(err) {
		os.Exit(2)
	}
	os.Exit(1)
}
