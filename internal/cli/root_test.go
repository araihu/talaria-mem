package cli

import (
	"bytes"
	"context"
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
	for _, name := range []string{"memory", "workspace", "projection"} {
		found := false
		for _, child := range command.Commands() {
			if child.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("cobra command %q not registered", name)
		}
	}
}

func TestCLIContractAuthenticationExit(t *testing.T) {
	if got := ExitCodeFor(AuthenticationError{}); got != ExitAuthentication {
		t.Fatalf("authentication exit=%d", got)
	}
}
