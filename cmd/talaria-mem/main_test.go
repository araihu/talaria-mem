package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRejectsUnavailableBootstrapOutput(t *testing.T) {
	if err := Run(context.Background(), []string{"--help"}, nil, nil); !errors.Is(err, ErrBootstrapOutputUnavailable) {
		t.Fatalf("Run with unavailable output = %v, want %v", err, ErrBootstrapOutputUnavailable)
	}
}

func TestRunHelpAndUnknownCommandDoNotCreateFiles(t *testing.T) {
	workdir := t.TempDir()
	t.Chdir(workdir)
	before, err := os.ReadDir(workdir)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(--help) error = %v", err)
	}
	if stdout.Len() == 0 {
		t.Fatal("Run(--help) wrote no usage text")
	}

	stdout.Reset()
	stderr.Reset()
	err = Run(context.Background(), []string{"definitely-unknown"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run(unknown) error = nil, want usage failure")
	}
	if !IsUsageError(err) {
		t.Fatalf("Run(unknown) error = %v, want usage failure", err)
	}

	after, err := os.ReadDir(workdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("Run created files in %s: before=%d after=%d", filepath.Base(workdir), len(before), len(after))
	}
}

func TestRunSubcommandHelpDoesNotComposeRuntime(t *testing.T) {
	workdir := t.TempDir()
	t.Chdir(workdir)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"setup", "codex", "--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(setup codex --help) error = %v", err)
	}
	if stdout.Len() == 0 || !bytes.Contains(stdout.Bytes(), []byte("setup codex")) {
		t.Fatalf("help output=%q", stdout.String())
	}
	entries, err := os.ReadDir(workdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("subcommand help created files: %v", entries)
	}
}

func TestParseDaemonAddressRemovesOnlyItsAddressOption(t *testing.T) {
	address, args, err := parseDaemonArgs([]string{"daemon", "--foreground", "--address", "127.0.0.1:8743"})
	if err != nil {
		t.Fatal(err)
	}
	if address != "127.0.0.1:8743" || len(args) != 2 || args[0] != "daemon" || args[1] != "--foreground" {
		t.Fatalf("address=%q args=%v", address, args)
	}
}

func TestParseDaemonAddressRejectsMissingValue(t *testing.T) {
	if _, _, err := parseDaemonArgs([]string{"daemon", "--address"}); err == nil {
		t.Fatal("missing address value accepted")
	}
}

func TestParseDaemonAddressUsesCobraFlagSyntax(t *testing.T) {
	address, args, err := parseDaemonArgs([]string{"daemon", "--address=127.0.0.1:8743", "--foreground", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if address != "127.0.0.1:8743" || len(args) != 3 || args[1] != "--foreground" || args[2] != "--json" {
		t.Fatalf("address=%q args=%v", address, args)
	}
	if _, _, err := parseDaemonArgs([]string{"daemon", "--address=127.0.0.1:8743", "--address", "127.0.0.1:8744"}); err == nil {
		t.Fatal("duplicate address accepted")
	}
}
