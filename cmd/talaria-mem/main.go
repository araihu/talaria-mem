package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

const usage = `talaria-mem stores local, workspace-scoped memory.

Usage:
  talaria-mem --help
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

// Run is the stable, provider-free bootstrap command shell. Runtime composition
// is intentionally added by the lifecycle task.
func Run(ctx context.Context, args []string, stdout, _ io.Writer) error {
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
	return &UsageError{Command: args[0]}
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
