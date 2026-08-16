package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	workspacepkg "github.com/guilhermecastro/talaria-mem/internal/workspace"
)

type fakeWorkspaceClient struct{}

func (fakeWorkspaceClient) List(context.Context) ([]domain.Workspace, error) {
	return []domain.Workspace{{ID: "w", Name: "workspace"}}, nil
}
func (fakeWorkspaceClient) Show(context.Context, string) (domain.Workspace, error) {
	return domain.Workspace{ID: "w", Name: "workspace"}, nil
}
func (fakeWorkspaceClient) Bind(context.Context, string, string) (workspacepkg.Binding, error) {
	return workspacepkg.Binding{Key: "key", WorkspaceID: "w"}, nil
}
func (fakeWorkspaceClient) Resolve(context.Context, workspacepkg.ResolutionInput) (workspacepkg.Resolution, error) {
	return workspacepkg.Resolution{}, nil
}
func (fakeWorkspaceClient) MergeDryRun(context.Context, string, string) (workspacepkg.MergePlan, workspacepkg.MergeReceipt, error) {
	return workspacepkg.MergePlan{}, workspacepkg.MergeReceipt{ID: "merge", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (fakeWorkspaceClient) MergeApply(context.Context, workspacepkg.MergePlan, workspacepkg.MergeReceipt) error {
	return nil
}

func TestCLIWorkspaceCommands(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(RootConfig{Workspace: NewWorkspaceCommands(fakeWorkspaceClient{}), Stdout: stdout, Stderr: stderr})
	if code := root.Execute(context.Background(), []string{"--json", "workspace", "list"}); code != ExitSuccess {
		t.Fatalf("workspace list exit=%d", code)
	}
	if stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
