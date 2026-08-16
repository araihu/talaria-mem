package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
)

type fakeMemoryClient struct {
	key     string
	created application.MutationResult
	item    retrieval.SearchItem
}

func (client *fakeMemoryClient) Create(_ context.Context, request application.MutationRequest) (application.MutationResult, error) {
	client.key = request.IdempotencyKey
	if client.created.MemoryID == "" {
		client.created = application.MutationResult{MemoryID: "m", RevisionID: "r", WorkspaceID: request.WorkspaceID, Trust: domain.TrustUnverified, Lifecycle: domain.LifecycleActive}
	}
	return client.created, nil
}
func (client *fakeMemoryClient) Update(context.Context, application.MutationRequest) (application.MutationResult, error) {
	return client.created, nil
}
func (client *fakeMemoryClient) Confirm(context.Context, application.MutationRequest) (application.MutationResult, error) {
	return client.created, nil
}
func (client *fakeMemoryClient) Pin(context.Context, application.MutationRequest) (application.MutationResult, error) {
	return client.created, nil
}
func (client *fakeMemoryClient) Forget(context.Context, application.MutationRequest) (application.MutationResult, error) {
	return client.created, nil
}
func (client *fakeMemoryClient) Restore(context.Context, application.MutationRequest) (application.MutationResult, error) {
	return client.created, nil
}
func (client *fakeMemoryClient) Review(context.Context, string, string) (application.ReviewResult, error) {
	return application.ReviewResult{MemoryID: "m", RevisionID: "r", WorkspaceID: "w", Title: "title", Content: "content"}, nil
}
func (client *fakeMemoryClient) Explain(context.Context, string) (application.Explanation, error) {
	return application.Explanation{MemoryID: "m", RevisionID: "r", Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
}
func (client *fakeMemoryClient) Search(context.Context, retrieval.SearchRequest) (retrieval.SearchResult, error) {
	return retrieval.SearchResult{Items: []retrieval.SearchItem{client.item}, Included: 1}, nil
}
func (client *fakeMemoryClient) Get(context.Context, string, string, string, ports.FieldIdentifier) (retrieval.SearchItem, error) {
	return client.item, nil
}

func TestCLIContractExitCodesAndMachineOutput(t *testing.T) {
	client := &fakeMemoryClient{}
	core := NewMemoryCore(client)
	core.Key = func() (string, error) { return "generated-key", nil }
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(RootConfig{Memory: core, Stdout: stdout, Stderr: stderr})
	code := root.Execute(context.Background(), []string{"--json", "memory", "add", "--workspace", "w", "--kind", "state", "--title", "title", "--content", "content"})
	if code != ExitSuccess {
		t.Fatalf("add exit=%d stderr=%s", code, stderr.String())
	}
	if client.key != "generated-key" {
		t.Fatalf("idempotency key=%q", client.key)
	}
	if stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestContentOutputGuard(t *testing.T) {
	client := &fakeMemoryClient{item: retrieval.SearchItem{MemoryID: "m", RevisionID: "r", WorkspaceID: "w", Kind: domain.MemoryKindState, Title: "title", Content: "content"}}
	guard := &cliGuard{}
	core := NewMemoryCore(client)
	core.Guard = guard
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := NewRoot(RootConfig{Memory: core, Stdout: stdout, Stderr: stderr})
	if code := root.Execute(context.Background(), []string{"--json", "memory", "get", "m", "--workspace", "w"}); code != ExitSuccess {
		t.Fatalf("get exit=%d stderr=%s", code, stderr.String())
	}
	if guard.calls != 1 {
		t.Fatalf("guard calls=%d", guard.calls)
	}
	if strings.Contains(stderr.String(), "content") {
		t.Fatal("content logged to stderr")
	}
}

type cliGuard struct{ calls int }

func (guard *cliGuard) Check(context.Context, application.OutputRequest) (application.OutputResult, error) {
	guard.calls++
	return application.OutputResult{Allowed: true, Status: ports.ScanClean}, nil
}
