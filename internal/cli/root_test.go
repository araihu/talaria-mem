package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLIContractMachineJSONAndHelp(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(RootConfig{Stdout: stdout, Stderr: stderr})
	if code := root.Execute(context.Background(), []string{"--json", "help"}); code != ExitSuccess {
		t.Fatalf("help exit=%d", code)
	}
	if stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := root.Execute(context.Background(), []string{"does-not-exist"}); code != ExitUsage {
		t.Fatalf("unknown exit=%d", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("human error was not written to stderr")
	}
}

func TestCLIContractCobraRegistrationBoundary(t *testing.T) {
	root := NewRoot(RootConfig{})
	command := root.CobraCommand()
	if command.DisableFlagParsing {
		t.Fatal("root command must let Cobra parse flags")
	}
	for _, name := range []string{"memory", "workspace", "projection"} {
		found := false
		for _, child := range command.Commands() {
			if child.Name() == name {
				if child.DisableFlagParsing {
					t.Fatalf("cobra command %q disables flag parsing", name)
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("cobra command %q not registered", name)
		}
	}
}

func TestCLIContractUsesCobraHelpAndRejectsUnknownFlags(t *testing.T) {
	root := NewRoot(RootConfig{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	command := root.CobraCommand()
	var help bytes.Buffer
	command.SetOut(&help)
	command.SetErr(&help)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), "Usage:") || !strings.Contains(help.String(), "--json") {
		t.Fatalf("Cobra help=%q", help.String())
	}
	if code := root.Execute(context.Background(), []string{"workspace", "list", "--not-a-real-flag"}); code != ExitUsage {
		t.Fatalf("unknown flag exit=%d", code)
	}
	if code := root.Execute(context.Background(), []string{"memory", "list", "--limit", "not-a-number"}); code != ExitUsage {
		t.Fatalf("invalid typed flag exit=%d", code)
	}
}

func TestCLIContractAuthenticationExit(t *testing.T) {
	if got := ExitCodeFor(AuthenticationError{}); got != ExitAuthentication {
		t.Fatalf("authentication exit=%d", got)
	}
}
