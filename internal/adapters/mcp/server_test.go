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
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type testReader struct{ item retrieval.SearchItem }

func (reader testReader) Search(context.Context, retrieval.SearchRequest) (retrieval.SearchResult, error) {
	return retrieval.SearchResult{Items: []retrieval.SearchItem{reader.item}, Included: 1}, nil
}
func (reader testReader) Get(context.Context, string, string, string, ports.FieldIdentifier) (retrieval.SearchItem, error) {
	return reader.item, nil
}
func (reader testReader) Explain(context.Context, string) (application.Explanation, error) {
	return application.Explanation{MemoryID: reader.item.MemoryID, RevisionID: reader.item.RevisionID, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive}, nil
}

type testGuard struct {
	calls int
	err   error
}

func (guard *testGuard) Check(context.Context, application.OutputRequest) (application.OutputResult, error) {
	guard.calls++
	if guard.err != nil {
		return application.OutputResult{}, guard.err
	}
	return application.OutputResult{Allowed: true, Status: ports.ScanClean}, nil
}

func newMCPServer(t *testing.T, guard Guard) (*Server, string) {
	t.Helper()
	token, err := security.GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := security.NewAuthenticator(security.AuthConfig{Token: token, Host: "127.0.0.1:7437"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Authenticator: authenticator, Reader: testReader{item: retrieval.SearchItem{MemoryID: "m", RevisionID: "r", WorkspaceID: "w", Kind: domain.MemoryKindState, Title: "title", Content: "content"}}, Guard: guard})
	if err != nil {
		t.Fatal(err)
	}
	return server, token
}

func mcpRequest(token, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7437/mcp", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:1234"
	request.Host = "127.0.0.1:7437"
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Mcp-Session-Id", "session-1")
	return request
}

func TestMCPContractInitializeListAndScope(t *testing.T) {
	server, token := newMCPServer(t, nil)
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, mcpRequest(token, body))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d", recorder.Code)
		}
		var response rpcResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error != nil {
			t.Fatalf("rpc error=%+v", response.Error)
		}
	}
	request := mcpRequest(token, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"x"}}}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("missing scope status=%d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "workspace") {
		t.Fatalf("missing scope error=%s", recorder.Body.String())
	}
}

func TestMCPStreamableHTTPAssignsSessionForInitialize(t *testing.T) {
	server, token := newMCPServer(t, nil)
	request := mcpRequest(token, `{"jsonrpc":"2.0","id":10,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	request.Header.Del("Mcp-Session-Id")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	session := recorder.Header().Get("Mcp-Session-Id")
	if !safeSession(session) {
		t.Fatalf("invalid assigned session=%q", session)
	}
	request = mcpRequest(token, `{"jsonrpc":"2.0","id":11,"method":"tools/list","params":{}}`)
	request.Header.Set("Mcp-Session-Id", session)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), `"error"`) {
		t.Fatalf("tools/list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCPContentOutputGuardAndMutationExclusion(t *testing.T) {
	guard := &testGuard{}
	server, token := newMCPServer(t, guard)
	request := mcpRequest(token, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"x","workspace_id":"w"}}}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || guard.calls != 1 {
		t.Fatalf("status=%d guard=%d", recorder.Code, guard.calls)
	}
	guard.err = domain.NewError(domain.CodeQuarantine, "content quarantined", false)
	request = mcpRequest(token, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"x","workspace_id":"w"}}}`)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), "content refused") {
		t.Fatalf("quarantine response=%s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "content\" content") {
		t.Fatal("content leaked")
	}
	mutation := mcpRequest(token, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"memory_create","arguments":{}}}`)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, mutation)
	if !strings.Contains(recorder.Body.String(), "mutation service unavailable") {
		t.Fatalf("mutation service availability error missing: %s", recorder.Body.String())
	}
	secretTool := mcpRequest(token, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"sk_live_secret_should_not_echo","arguments":{}}}`)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, secretTool)
	if strings.Contains(recorder.Body.String(), "sk_live_secret_should_not_echo") {
		t.Fatal("unknown tool name leaked")
	}
}
