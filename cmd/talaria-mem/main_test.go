package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

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

func TestBootstrapGREENConsumesUnknownCommandFixture(t *testing.T) {
	var fixture struct {
		Args         []string `json:"args"`
		ExpectedExit int      `json:"expected_exit"`
		CreatesFiles bool     `json:"creates_files"`
	}
	testutil.ReadJSONFixture(t, &fixture, "bootstrap", "unknown-command.json")
	if len(fixture.Args) != 1 || fixture.ExpectedExit != 2 || fixture.CreatesFiles {
		t.Fatalf("unexpected bootstrap green fixture: %+v", fixture)
	}
	if err := Run(context.Background(), fixture.Args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("Run accepted unknown command from fixture")
	}
}
