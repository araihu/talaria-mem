package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type fakeProjector struct{ scope string }

func (projector *fakeProjector) Project(_ context.Context, request ports.ProjectionRequest) error {
	projector.scope = request.ScopeID
	return nil
}

func TestCLIProjectionCommand(t *testing.T) {
	projector := &fakeProjector{}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(RootConfig{Projection: NewProjectionCommands(projector), Stdout: stdout, Stderr: stderr})
	if code := root.Execute(context.Background(), []string{"--json", "projection", "rebuild", "--workspace", "w"}); code != ExitSuccess {
		t.Fatalf("projection exit=%d", code)
	}
	if projector.scope != "w" || stdout.Len() == 0 {
		t.Fatalf("scope=%q stdout=%q", projector.scope, stdout.String())
	}
}
