package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
	runtimegraph "github.com/guilhermecastro/talaria-mem/internal/runtime"
	"github.com/spf13/cobra"
)

var ErrBootstrapOutputUnavailable = errors.New("bootstrap output is unavailable")

// UsageError reports invalid bootstrap command-line input.
type UsageError struct {
	Command string
	Message string
}

func (err *UsageError) Error() string {
	if err.Message != "" {
		return err.Message
	}
	return fmt.Sprintf("unknown command %q", err.Command)
}

// IsUsageError reports whether err represents command-line usage failure.
func IsUsageError(err error) bool {
	var usageErr *UsageError
	return errors.As(err, &usageErr) || cli.ExitCodeFor(err) == cli.ExitUsage
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

	if len(args) == 0 {
		return &UsageError{Command: ""}
	}
	if isHelpRequest(args) {
		return runtimegraph.RunHelp(ctx, args, stdout, stderr)
	}
	bootstrap := cli.NewBootstrapCommand(stdout, stderr)
	bootstrap.InitDefaultHelpCmd()
	found, _, err := bootstrap.Find(args)
	if err != nil || found == bootstrap {
		return &UsageError{Command: bootstrapCommandName(args)}
	}
	address, normalizedArgs, err := parseDaemonArgs(args)
	if err != nil {
		return err
	}
	var environment lifecycle.Environment
	if normalizedArgs[0] == "setup" {
		environment, err = lifecycle.ParseEnvironmentOSOrDefaultForSetup()
	} else {
		environment, err = lifecycle.ParseEnvironmentOSOrDefault()
	}
	if err != nil {
		return err
	}
	configuration := runtimegraph.Config{
		Environment: environment,
		Address:     address,
		Stdout:      stdout,
		Stderr:      stderr,
	}
	if normalizedArgs[0] == "setup" {
		return runtimegraph.RunSetup(ctx, normalizedArgs, configuration)
	}
	composition, err := runtimegraph.New(ctx, configuration)
	if err != nil {
		return err
	}
	defer func() { _ = composition.Close() }()
	return composition.Run(ctx, normalizedArgs)
}

func bootstrapCommandName(args []string) string {
	for _, arg := range args {
		if arg == "--json" {
			continue
		}
		if len(arg) > 0 && arg[0] == '-' {
			continue
		}
		return arg
	}
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func isHelpRequest(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	if len(args) == 1 {
		return args[0] == "help"
	}
	return len(args) == 2 && args[0] == "--json" && args[1] == "help"
}

func parseDaemonArgs(args []string) (string, []string, error) {
	if len(args) == 0 || args[0] != "daemon" {
		return "", args, nil
	}
	addressCount := 0
	for _, arg := range args[1:] {
		if arg == "--address" || strings.HasPrefix(arg, "--address=") {
			addressCount++
		}
	}
	if addressCount > 1 {
		return "", nil, &UsageError{Message: "usage: daemon accepts --address only once"}
	}

	parser := &cobra.Command{Use: "daemon", Args: cobra.NoArgs}
	parser.Flags().String("address", "", "loopback listen address")
	parser.Flags().Bool("foreground", false, "run in the foreground")
	parser.Flags().Bool("diagnostic", false, "run with diagnostic output")
	parser.Flags().Bool("json", false, "write machine-readable JSON")
	if err := parser.ParseFlags(args[1:]); err != nil {
		return "", nil, &UsageError{Message: "usage: daemon: " + err.Error()}
	}
	if err := cobra.NoArgs(parser, parser.Flags().Args()); err != nil {
		return "", nil, &UsageError{Message: "usage: daemon: " + err.Error()}
	}
	address, _ := parser.Flags().GetString("address")
	if addressCount > 0 && address == "" {
		return "", nil, &UsageError{Message: "usage: daemon --address requires HOST:PORT"}
	}
	normalized := []string{args[0]}
	if foreground, _ := parser.Flags().GetBool("foreground"); foreground {
		normalized = append(normalized, "--foreground")
	}
	if diagnostic, _ := parser.Flags().GetBool("diagnostic"); diagnostic {
		normalized = append(normalized, "--diagnostic")
	}
	if jsonOutput, _ := parser.Flags().GetBool("json"); jsonOutput {
		normalized = append(normalized, "--json")
	}
	return address, normalized, nil
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
