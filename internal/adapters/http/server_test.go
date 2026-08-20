package httpadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/codex"
	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type testStarter struct{ called bool }

func (starter *testStarter) SessionStart(_ context.Context, input SessionStartRequest) (SessionStartResponse, error) {
	starter.called = true
	return SessionStartResponse{Included: 0, Omitted: 0}, nil
}

type testHooker struct{ events []codex.HookEvent }

func (hooker *testHooker) Handle(_ context.Context, event codex.HookEvent) (codex.HookResponse, error) {
	hooker.events = append(hooker.events, event)
	return codex.HookResponse{Version: codex.CurationHookResponseVersion, HookName: event.HookName, Accepted: true, Recall: &codex.RecallResult{WorkspaceID: "workspace", Items: []codex.RecallItem{}, Context: ""}}, nil
}

type testReader struct {
	item retrieval.SearchItem
}

func (reader testReader) Search(context.Context, retrieval.SearchRequest) (retrieval.SearchResult, error) {
	return retrieval.SearchResult{Items: []retrieval.SearchItem{reader.item}, Included: 1}, nil
}
func (reader testReader) Get(context.Context, string, string, string, ports.FieldIdentifier) (retrieval.SearchItem, error) {
	return reader.item, nil
}
func (reader testReader) Explain(context.Context, string) (application.Explanation, error) {
	return application.Explanation{MemoryID: reader.item.MemoryID, RevisionID: reader.item.RevisionID, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
}

type testReadiness struct{ ready bool }

func (readiness testReadiness) Ready(context.Context) (bool, string, error) {
	return readiness.ready, "fixture", nil
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

func newTestServer(t *testing.T, reader ReadService, guard ContentGuard) (*Server, string) {
	t.Helper()
	token, err := security.GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := security.NewAuthenticator(security.AuthConfig{Token: token, Host: "127.0.0.1:7437"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Authenticator: authenticator, SessionStart: &testStarter{}, CurationHooks: &testHooker{}, Reader: reader, Guard: guard, Readiness: testReadiness{ready: true}})
	if err != nil {
		t.Fatal(err)
	}
	return server, token
}

func TestCurationHookEndpointsParseEventsAndPreserveEmptyResponses(t *testing.T) {
	server, token := newTestServer(t, testReader{}, nil)
	for _, test := range []struct {
		path, name string
	}{
		{path: UserPromptSubmitPath, name: codex.HookUserPromptSubmit},
		{path: PreCompactPath, name: codex.HookPreCompact},
		{path: SessionEndPath, name: codex.HookSessionEnd},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"session_id":"session","cwd":"/workspace","hook_event_name":"` + test.name + `","current_prompt":"prompt","transcript_path":"MUST_NOT_APPEAR"}`
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, authenticatedRequest(http.MethodPost, test.path, token, body))
			if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "MUST_NOT_APPEAR") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	request := authenticatedRequest(http.MethodPost, UserPromptSubmitPath, token, `{"session_id":"s","cwd":"/tmp","hook_event_name":"SessionEnd"}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("mismatched hook status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func authenticatedRequest(method, path, token, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	request.Host = "127.0.0.1:7437"
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func TestContractHealthReadinessAndSessionStart(t *testing.T) {
	server, token := newTestServer(t, testReader{}, nil)
	for _, test := range []struct {
		name, method, path, body string
		wantStatus               int
	}{
		{name: "health", method: http.MethodGet, path: HealthPath, wantStatus: http.StatusOK},
		{name: "readiness", method: http.MethodGet, path: ReadinessPath, wantStatus: http.StatusOK},
		{name: "session", method: http.MethodPost, path: SessionStartPath, body: `{"session_id":"s","hook_event_name":"SessionStart","cwd":"/tmp","transcript_path":"/private/transcript","transcript":{"content":"must not be retained"}}`, wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := authenticatedRequest(test.method, test.path, token, test.body)
			if test.path == HealthPath {
				request.Header.Del("Authorization")
			}
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestSessionStartReturnsOfficialCodexHookOutput(t *testing.T) {
	server, token := newTestServer(t, testReader{}, nil)
	body := `{"session_id":"codex-session","transcript_path":"/tmp/codex-rollout.jsonl","cwd":"/workspace/repo","hook_event_name":"SessionStart","model":"gpt-5.6","permission_mode":"default","source":"startup"}`
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, authenticatedRequest(http.MethodPost, SessionStartPath, token, body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(recorder.Body.String(), "codex-rollout.jsonl") {
		t.Fatalf("transcript path leaked into response: %s", recorder.Body.String())
	}
	if response.HookSpecificOutput.HookEventName != "SessionStart" || response.HookSpecificOutput.AdditionalContext == "" {
		t.Fatalf("hookSpecificOutput = %+v; body=%s", response.HookSpecificOutput, recorder.Body.String())
	}
}

func TestContractAuthenticationAndDuplicateJSONKeys(t *testing.T) {
	server, token := newTestServer(t, testReader{}, nil)
	unauthenticated := authenticatedRequest(http.MethodPost, SessionStartPath, token, `{"session_id":"s","hook_event_name":"SessionStart","cwd":"/tmp"}`)
	unauthenticated.Header.Del("Authorization")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, unauthenticated)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", recorder.Code)
	}
	duplicate := authenticatedRequest(http.MethodPost, SessionStartPath, token, `{"session_id":"s","hook_event_name":"SessionStart","cwd":"/tmp","session_id":"other"}`)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, duplicate)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status = %d", recorder.Code)
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope.Version) != errorEnvelopeVersion || envelope.ReceiptId == [16]byte{} {
		t.Fatalf("unsafe envelope = %+v", envelope)
	}
}

func TestAuthenticationFailureDoesNotEchoCredentialTransport(t *testing.T) {
	server, token := newTestServer(t, testReader{}, nil)
	canary := "HTTP_CREDENTIAL_CANARY_MUST_NOT_APPEAR"
	request := authenticatedRequest(http.MethodGet, ReadinessPath+"?token="+canary, token, "")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("credential transport status=%d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), canary) {
		t.Fatal("credential query value leaked into HTTP response")
	}
}

func TestSessionStartRejectsOversizedFields(t *testing.T) {
	server, token := newTestServer(t, testReader{}, nil)
	body := `{"session_id":"` + strings.Repeat("s", 257) + `","hook_event_name":"SessionStart","cwd":"/tmp"}`
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, authenticatedRequest(http.MethodPost, SessionStartPath, token, body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized session status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body = `{"session_id":"s","hook_event_name":"SessionStart","cwd":"` + strings.Repeat("/", 513) + `"}`
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, authenticatedRequest(http.MethodPost, SessionStartPath, token, body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized working directory status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSessionStartRejectsInvalidUTF8(t *testing.T) {
	server, token := newTestServer(t, testReader{}, nil)
	body := []byte(`{"session_id":"s","hook_event_name":"SessionStart","cwd":"/tmp","transcript":"`)
	body = append(body, 0xff)
	body = append(body, []byte(`"}`)...)
	request := authenticatedRequest(http.MethodPost, SessionStartPath, token, string(body))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid UTF-8 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestContentOutputGuardAndRouteQuarantine(t *testing.T) {
	item := retrieval.SearchItem{MemoryID: "m", RevisionID: "r", WorkspaceID: "w", Kind: domain.MemoryKindState, Title: "title", Content: "secret"}
	guard := &testGuard{}
	server, token := newTestServer(t, testReader{item: item}, guard)
	request := authenticatedRequest(http.MethodPost, "/control/v1/memory/search", token, `{"query":"title","workspace_id":"w","session_id":"s"}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || guard.calls != 1 {
		t.Fatalf("search status=%d guard calls=%d body=%s", recorder.Code, guard.calls, recorder.Body.String())
	}
	guard.err = domain.NewError(domain.CodeQuarantine, "content quarantined", false)
	request = authenticatedRequest(http.MethodPost, "/control/v1/memory/search", token, `{"query":"title","workspace_id":"w","session_id":"s"}`)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("quarantine status=%d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatal("quarantined content returned")
	}
}

func TestScannerFailureNoMutation(t *testing.T) {
	guard := &testGuard{err: domain.NewError(domain.CodeUnavailable, "scanner unavailable", true)}
	server, token := newTestServer(t, testReader{item: retrieval.SearchItem{MemoryID: "m", RevisionID: "r", WorkspaceID: "w", Title: "title", Content: "secret"}}, guard)
	request := authenticatedRequest(http.MethodPost, "/control/v1/memory/search", token, `{"query":"title","workspace_id":"w","session_id":"s"}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("scanner failure status=%d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatal("scanner failure returned content")
	}
}

func TestSearchRejectsOutOfRangeLimit(t *testing.T) {
	server, token := newTestServer(t, testReader{}, nil)
	request := authenticatedRequest(http.MethodPost, "/control/v1/memory/search", token, `{"query":"title","workspace_id":"w","session_id":"s","limit":21}`)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Message != "invalid request" {
		t.Fatalf("request-derived limit leaked into error message: %q", envelope.Message)
	}
}
