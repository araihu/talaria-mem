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
