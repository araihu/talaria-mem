package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type fakeMutator struct {
	last application.MutationRequest
}

type fakeGenerated struct {
	request application.GeneratedMutationRequest
	source  application.GeneratedSource
}

func (generated *fakeGenerated) CreateGenerated(_ context.Context, request application.GeneratedMutationRequest, source application.GeneratedSource) (application.MutationResult, error) {
	generated.request, generated.source = request, source
	return application.MutationResult{MemoryID: "018f1f61-7b5c-7abc-8def-0123456789ab", RevisionID: "018f1f61-7b5c-7abc-8def-1123456789ab", WorkspaceID: request.WorkspaceID, Trust: domain.TrustGenerated, Lifecycle: domain.LifecycleActive}, nil
}

func (mutator *fakeMutator) Create(_ context.Context, request application.MutationRequest) (application.MutationResult, error) {
	mutator.last = request
	return application.MutationResult{MemoryID: "018f1f61-7b5c-7abc-8def-0123456789ab", RevisionID: "018f1f61-7b5c-7abc-8def-1123456789ab", WorkspaceID: request.WorkspaceID, Trust: domain.TrustUnverified, Lifecycle: domain.LifecycleActive}, nil
}
func (mutator *fakeMutator) Update(_ context.Context, request application.MutationRequest) (application.MutationResult, error) {
	mutator.last = request
	return application.MutationResult{MemoryID: request.MemoryID, RevisionID: request.ExpectedRevisionID, Trust: domain.TrustUnverified, Lifecycle: domain.LifecycleActive}, nil
}
func (mutator *fakeMutator) Pin(_ context.Context, request application.MutationRequest) (application.MutationResult, error) {
	mutator.last = request
	return application.MutationResult{MemoryID: request.MemoryID, RevisionID: request.ExpectedRevisionID, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive}, nil
}
func (mutator *fakeMutator) Forget(_ context.Context, request application.MutationRequest) (application.MutationResult, error) {
	mutator.last = request
	return application.MutationResult{MemoryID: request.MemoryID, RevisionID: request.ExpectedRevisionID, Lifecycle: domain.LifecycleForgotten}, nil
}

func mutationServer(t *testing.T, mutator MutationService) (*Server, string) {
	t.Helper()
	token, err := security.GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := security.NewAuthenticator(security.AuthConfig{Token: token, Host: "127.0.0.1:7437"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Authenticator: authenticator, Mutator: mutator})
	if err != nil {
		t.Fatal(err)
	}
	return server, token
}

func TestMCPMutationToolsHaveNoVerifiedOverride(t *testing.T) {
	for _, definition := range MutationToolDefinitions() {
		properties, ok := definition.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("tool %s has invalid schema", definition.Name)
		}
		if _, found := properties["verified"]; found {
			t.Fatalf("tool %s accepts verified override", definition.Name)
		}
	}
}

func TestMCPMutationCreateIsUnverifiedAndRejectsUnknownFields(t *testing.T) {
	mutator := &fakeMutator{}
	server, token := mutationServer(t, mutator)
	request := mcpRequest(token, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_create","arguments":{"workspace_id":"w","kind":"procedure","title":"title","content":"content","verified":true}}}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), "invalid memory create arguments") {
		t.Fatalf("verified override response=%s", recorder.Body.String())
	}
	request = mcpRequest(token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_create","arguments":{"workspace_id":"w","kind":"procedure","title":"title","content":"content"}}}`)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), `"error"`) {
		t.Fatalf("create response=%s", recorder.Body.String())
	}
	if mutator.last.Actor != application.ActorMCP || mutator.last.Verified || mutator.last.WorkspaceID != "w" {
		t.Fatalf("mutation request=%+v", mutator.last)
	}
	var response rpcResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recorder.Body.Bytes()), `"content":"content"`) || strings.Contains(string(recorder.Body.Bytes()), `"title":"title"`) {
		t.Fatal("mutation response leaked memory content")
	}
}

func TestMCPInlineCurationAssignsGeneratedTrustAndRejectsOverrides(t *testing.T) {
	generated := &fakeGenerated{}
	token, err := security.GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := security.NewAuthenticator(security.AuthConfig{Token: token, Host: "127.0.0.1:7437"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Authenticator: authenticator, Generated: generated})
	if err != nil {
		t.Fatal(err)
	}
	request := mcpRequest(token, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_curate_inline","arguments":{"workspace_id":"w","kind":"procedure","title":"title","content":"content","trust":"verified"}}}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), "workspace, kind, title, and content are required") {
		t.Fatalf("trust override response=%s", recorder.Body.String())
	}
	request = mcpRequest(token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_curate_inline","arguments":{"workspace_id":"w","kind":"procedure","title":"title","content":"content"}}}`)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), `"error"`) {
		t.Fatalf("inline response=%s", recorder.Body.String())
	}
	if generated.source != application.GeneratedSourceInline || generated.request.WorkspaceID != "w" || generated.request.Kind != domain.MemoryKindProcedure {
		t.Fatalf("generated request=%+v source=%q", generated.request, generated.source)
	}
	if !strings.Contains(recorder.Body.String(), `"trust":"generated"`) {
		t.Fatalf("generated trust missing: %s", recorder.Body.String())
	}
}
