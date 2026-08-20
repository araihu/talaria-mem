package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/adapters/codex"
	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

const (
	HealthVersion       = "talaria.health.v1"
	ReadinessVersion    = "talaria.ready.v1"
	SessionStartVersion = "talaria.session-start.v1"
	SearchVersion       = "talaria.memory-search.v1"
	maxSessionIDBytes   = 256
	maxWorkingDirBytes  = 512
)

// SessionStarter is implemented by the T11 context-selection service. T10
// owns only the authenticated transport and never duplicates its ranking or
// precedence rules.
type SessionStarter interface {
	SessionStart(context.Context, SessionStartRequest) (SessionStartResponse, error)
}

// CurationHooker handles the three asynchronous Codex hook events. The
// adapter keeps parsing and authentication here while the Codex package owns
// cadence, recall, and encrypted enqueue policy.
type CurationHooker interface {
	Handle(context.Context, codex.HookEvent) (codex.HookResponse, error)
}

// ReadService is the verified, scanner-gated read seam shared by HTTP and
// MCP. The retrieval package's Searcher is the reference implementation.
type ReadService interface {
	Search(context.Context, retrieval.SearchRequest) (retrieval.SearchResult, error)
	Get(context.Context, string, string, string, ports.FieldIdentifier) (retrieval.SearchItem, error)
	Explain(context.Context, string) (application.Explanation, error)
}

// ServiceReader is the normal composition adapter: retrieval owns search and
// scanner-gated content, while application owns metadata explanations.
type ServiceReader struct {
	Searcher *retrieval.Searcher
	Memory   *application.MemoryService
}

func (reader ServiceReader) Search(ctx context.Context, request retrieval.SearchRequest) (retrieval.SearchResult, error) {
	if reader.Searcher == nil {
		return retrieval.SearchResult{}, domain.NewError(domain.CodeUnavailable, "retrieval unavailable", true)
	}
	return reader.Searcher.Search(ctx, request)
}
func (reader ServiceReader) Get(ctx context.Context, memoryID, workspaceID, session string, route ports.FieldIdentifier) (retrieval.SearchItem, error) {
	if reader.Searcher == nil {
		return retrieval.SearchItem{}, domain.NewError(domain.CodeUnavailable, "retrieval unavailable", true)
	}
	return reader.Searcher.Get(ctx, memoryID, workspaceID, session, route)
}
func (reader ServiceReader) Explain(ctx context.Context, memoryID string) (application.Explanation, error) {
	if reader.Memory == nil {
		return application.Explanation{}, domain.NewError(domain.CodeUnavailable, "explanation unavailable", true)
	}
	return reader.Memory.Explain(ctx, memoryID)
}

const routeMCPRead ports.FieldIdentifier = ports.FieldMCPRead

// ReadinessChecker reports the durable readiness state. A nil checker is
// treated as unavailable; readiness must never be optimistic when composition
// has not supplied the lifecycle boundary.
type ReadinessChecker interface {
	Ready(context.Context) (bool, string, error)
}

type ContentGuard interface {
	Check(context.Context, application.OutputRequest) (application.OutputResult, error)
}

type ServerConfig struct {
	Authenticator *security.Authenticator
	SessionStart  SessionStarter
	CurationHooks CurationHooker
	Reader        ReadService
	Guard         ContentGuard
	Readiness     ReadinessChecker
	Now           func() time.Time
}

// Config is a compatibility alias used by composition code.
type Config = ServerConfig

type Server struct {
	config ServerConfig
	public http.Handler
	secure http.Handler
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	server := &Server{config: config}
	secureRoutes := http.NewServeMux()
	secureRoutes.HandleFunc(ReadinessPath, server.handleReadiness)
	secureRoutes.HandleFunc(SessionStartPath, server.handleSessionStart)
	secureRoutes.HandleFunc(UserPromptSubmitPath, server.handleUserPromptSubmit)
	secureRoutes.HandleFunc(PreCompactPath, server.handlePreCompact)
	secureRoutes.HandleFunc(SessionEndPath, server.handleSessionEnd)
	secureRoutes.HandleFunc("/control/v1/memory/search", server.handleSearch)
	secureRoutes.HandleFunc("/control/v1/memory/", server.handleMemory)
	server.secure = Middleware(config.Authenticator, secureRoutes)
	public := http.NewServeMux()
	public.HandleFunc(HealthPath, server.handleHealth)
	public.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == HealthPath {
			server.handleHealth(writer, request)
			return
		}
		server.secure.ServeHTTP(writer, request)
	})
	server.public = public
	if config.Authenticator == nil {
		return nil, security.ErrAuthRequired
	}
	return server, nil
}

func NewHandler(config ServerConfig) (http.Handler, error) {
	server, err := NewServer(config)
	if err != nil {
		return nil, err
	}
	return server.Handler(), nil
}

func (server *Server) Handler() http.Handler {
	if server == nil || server.public == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writeError(writer, security.ErrAuthRequired) })
	}
	return server.public
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	server.Handler().ServeHTTP(writer, request)
}

func (server *Server) handleHealth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeError(writer, &RequestError{Status: http.StatusMethodNotAllowed, Code: domain.CodeValidation, Message: "method not allowed"})
		return
	}
	writeJSON(writer, http.StatusOK, HealthResponse{Status: HealthResponseStatus("ok"), Version: HealthResponseVersion(HealthVersion)})
}

func (server *Server) handleReadiness(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeError(writer, &RequestError{Status: http.StatusMethodNotAllowed, Code: domain.CodeValidation, Message: "method not allowed"})
		return
	}
	if server.config.Readiness == nil {
		reason := "readiness unavailable"
		writeJSON(writer, http.StatusServiceUnavailable, ReadinessResponse{Ready: false, Version: ReadinessResponseVersion(ReadinessVersion), Reason: &reason})
		return
	}
	ready, reason, err := server.config.Readiness.Ready(request.Context())
	if err != nil {
		writeError(writer, err)
		return
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	writeJSON(writer, status, ReadinessResponse{Ready: ready, Version: ReadinessResponseVersion(ReadinessVersion), Reason: reasonPtr})
}

func (server *Server) handleSessionStart(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, &RequestError{Status: http.StatusMethodNotAllowed, Code: domain.CodeValidation, Message: "method not allowed"})
		return
	}
	if server.config.SessionStart == nil {
		writeError(writer, domain.NewError(domain.CodeUnavailable, "session start unavailable", true))
		return
	}
	var input SessionStartRequest
	if err := decodeJSONAllowUnknown(request, &input); err != nil {
		writeError(writer, err)
		return
	}
	if input.SessionId == "" || input.Cwd == "" || !input.HookEventName.Valid() {
		writeError(writer, domain.NewError(domain.CodeValidation, "session, hook, and working directory are required", false))
		return
	}
	if !validSessionStartString(input.SessionId, maxSessionIDBytes) || !validSessionStartString(input.Cwd, maxWorkingDirBytes) {
		writeError(writer, domain.NewError(domain.CodeValidation, "session start field is invalid or oversized", false))
		return
	}
	result, err := server.config.SessionStart.SessionStart(request.Context(), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	if result.Version == "" {
		result.Version = SessionStartResponseVersion(SessionStartVersion)
	}
	for _, item := range result.Items {
		if err := server.guardItem(request.Context(), itemFromDTO(item), result.WorkspaceId); err != nil {
			writeError(writer, err)
			return
		}
	}
	additionalContext, err := sessionStartAdditionalContext(result)
	if err != nil {
		writeError(writer, err)
		return
	}
	result.HookSpecificOutput = SessionStartHookSpecificOutput{
		HookEventName:     SessionStartHookSpecificOutputHookEventNameSessionStart,
		AdditionalContext: additionalContext,
	}
	writeJSON(writer, http.StatusOK, result)
}

func (server *Server) handleUserPromptSubmit(writer http.ResponseWriter, request *http.Request) {
	server.handleCurationHook(writer, request, codex.HookUserPromptSubmit)
}

func (server *Server) handlePreCompact(writer http.ResponseWriter, request *http.Request) {
	server.handleCurationHook(writer, request, codex.HookPreCompact)
}

func (server *Server) handleSessionEnd(writer http.ResponseWriter, request *http.Request) {
	server.handleCurationHook(writer, request, codex.HookSessionEnd)
}

func (server *Server) handleCurationHook(writer http.ResponseWriter, request *http.Request, expected string) {
	if request.Method != http.MethodPost {
		writeError(writer, &RequestError{Status: http.StatusMethodNotAllowed, Code: domain.CodeValidation, Message: "method not allowed"})
		return
	}
	if server.config.CurationHooks == nil {
		writeError(writer, domain.NewError(domain.CodeUnavailable, "curation hook unavailable", true))
		return
	}
	event, err := codex.DecodeHookEvent(request.Body)
	if err != nil {
		writeError(writer, err)
		return
	}
	if event.HookName != expected {
		writeError(writer, domain.NewError(domain.CodeValidation, "hook event does not match endpoint", false))
		return
	}
	response, err := server.config.CurationHooks.Handle(request.Context(), event)
	if err != nil {
		writeError(writer, err)
		return
	}
	if response.Version == "" {
		response.Version = codex.CurationHookResponseVersion
	}
	writeJSON(writer, http.StatusOK, response)
}

// sessionStartAdditionalContext keeps the transport response useful to
// programmatic clients while supplying the official Codex hook field. The
// hook receives this bounded JSON snapshot as text; it never receives the raw
// request or transcript fields.
func sessionStartAdditionalContext(response SessionStartResponse) (string, error) {
	payload := struct {
		Version     SessionStartResponseVersion `json:"version"`
		WorkspaceID string                      `json:"workspace_id"`
		Items       []MemoryItem                `json:"items"`
		Included    int                         `json:"included"`
		Omitted     int                         `json:"omitted"`
		Warning     *string                     `json:"warning,omitempty"`
		NextCursor  *string                     `json:"next_cursor,omitempty"`
	}{
		Version:     response.Version,
		WorkspaceID: response.WorkspaceId,
		Items:       response.Items,
		Included:    response.Included,
		Omitted:     response.Omitted,
		Warning:     response.Warning,
		NextCursor:  response.NextCursor,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", domain.NewError(domain.CodeUnavailable, "hook context unavailable", true)
	}
	return string(encoded), nil
}

func validSessionStartString(value string, maxBytes int) bool {
	return utf8.ValidString(value) && len([]byte(value)) <= maxBytes && !strings.ContainsAny(value, "\x00\r\n")
}

func (server *Server) handleSearch(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, &RequestError{Status: http.StatusMethodNotAllowed, Code: domain.CodeValidation, Message: "method not allowed"})
		return
	}
	if server.config.Reader == nil {
		writeError(writer, domain.NewError(domain.CodeUnavailable, "retrieval unavailable", true))
		return
	}
	var input MemorySearchRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, err)
		return
	}
	if input.Query == "" || input.WorkspaceId == "" || input.SessionId == "" {
		writeError(writer, domain.NewError(domain.CodeValidation, "query, workspace, and session are required", false))
		return
	}
	limit := 0
	if input.Limit != nil {
		limit = *input.Limit
	}
	if limit < 0 || limit > domain.MaxSearchItems {
		writeError(writer, domain.NewError(domain.CodeValidation, "limit is out of range", false))
		return
	}
	result, err := server.config.Reader.Search(request.Context(), retrieval.SearchRequest{Query: input.Query, WorkspaceID: input.WorkspaceId, ConsumerSession: input.SessionId, Limit: limit, Route: "mcp_read"})
	if err != nil {
		writeError(writer, err)
		return
	}
	items := make([]MemoryItem, 0, len(result.Items))
	for _, item := range result.Items {
		if err := server.guardItem(request.Context(), item, input.WorkspaceId); err != nil {
			writeError(writer, err)
			return
		}
		items = append(items, itemDTO(item))
	}
	receipt, err := uuid.Parse(receiptID(server.config.Now().UTC()))
	if err != nil {
		writeError(writer, domain.NewError(domain.CodeUnavailable, "receipt unavailable", true))
		return
	}
	writeJSON(writer, http.StatusOK, MemorySearchResponse{Version: MemorySearchResponseVersion(SearchVersion), Items: items, Included: result.Included, Omitted: result.Omitted, ReceiptId: receipt})
}

func (server *Server) handleMemory(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, &RequestError{Status: http.StatusMethodNotAllowed, Code: domain.CodeValidation, Message: "method not allowed"})
		return
	}
	if server.config.Reader == nil {
		writeError(writer, domain.NewError(domain.CodeUnavailable, "retrieval unavailable", true))
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/control/v1/memory/")
	if path == "" || strings.Contains(path, "/") {
		if strings.HasSuffix(path, "/explain") {
			memoryID := strings.TrimSuffix(path, "/explain")
			if memoryID != "" && !strings.Contains(memoryID, "/") {
				server.handleExplain(writer, request, memoryID)
				return
			}
		}
		writeError(writer, domain.NewError(domain.CodeNotFound, "memory not found", false))
		return
	}
	workspaceID := request.URL.Query().Get("workspace_id")
	if workspaceID == "" {
		writeError(writer, domain.NewError(domain.CodeValidation, "workspace is required", false))
		return
	}
	sessionID := request.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		sessionID = request.Header.Get("X-Talaria-Session")
	}
	item, err := server.config.Reader.Get(request.Context(), path, workspaceID, sessionID, routeMCPRead)
	if err != nil {
		writeError(writer, err)
		return
	}
	if err := server.guardItem(request.Context(), item, workspaceID); err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, itemDTO(item))
}

func (server *Server) handleExplain(writer http.ResponseWriter, request *http.Request, memoryID string) {
	if request.Method != http.MethodGet {
		writeError(writer, &RequestError{Status: http.StatusMethodNotAllowed, Code: domain.CodeValidation, Message: "method not allowed"})
		return
	}
	if server.config.Reader == nil {
		writeError(writer, domain.NewError(domain.CodeUnavailable, "retrieval unavailable", true))
		return
	}
	result, err := server.config.Reader.Explain(request.Context(), memoryID)
	if err != nil {
		writeError(writer, err)
		return
	}
	var resolutionState *ExplanationResolutionState
	if result.ResolutionState != "" {
		value := ExplanationResolutionState(result.ResolutionState)
		resolutionState = &value
	}
	writeJSON(writer, http.StatusOK, Explanation{MemoryId: uuidValue(result.MemoryID), RevisionId: uuidValue(result.RevisionID), Trust: ExplanationTrust(result.Trust), Lifecycle: ExplanationLifecycle(result.Lifecycle), Pinned: result.Pinned, CreatedAt: result.CreatedAt.UTC(), UpdatedAt: result.UpdatedAt.UTC(), ResolutionState: resolutionState})
}

func itemDTO(item retrieval.SearchItem) MemoryItem {
	var resolutionState *MemoryItemResolutionState
	if item.ResolutionState != "" {
		value := MemoryItemResolutionState(item.ResolutionState)
		resolutionState = &value
	}
	score := float32(item.Score.FinalScore)
	return MemoryItem{MemoryId: uuidValue(item.MemoryID), RevisionId: uuidValue(item.RevisionID), WorkspaceId: item.WorkspaceID, Kind: MemoryItemKind(item.Kind), Title: item.Title, Content: item.Content, Tags: append([]string(nil), item.Tags...), Trust: MemoryItemTrust("verified"), Lifecycle: MemoryItemLifecycle("active"), ResolutionState: resolutionState, Score: &score}
}

func itemFromDTO(item MemoryItem) retrieval.SearchItem {
	var resolutionState domain.ResolutionState
	if item.ResolutionState != nil {
		resolutionState = domain.ResolutionState(*item.ResolutionState)
	}
	var score float64
	if item.Score != nil {
		score = float64(*item.Score)
	}
	return retrieval.SearchItem{MemoryID: item.MemoryId.String(), RevisionID: item.RevisionId.String(), WorkspaceID: item.WorkspaceId, Kind: domain.MemoryKind(item.Kind), Title: item.Title, Content: item.Content, Tags: append([]string(nil), item.Tags...), Score: retrieval.Score{FinalScore: score}, ResolutionState: resolutionState}
}

func uuidValue(value string) uuid.UUID {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}

func (server *Server) guardItem(ctx context.Context, item retrieval.SearchItem, workspaceID string) error {
	if server.config.Guard == nil {
		return nil
	}
	fields := []ports.TextField{{Name: ports.FieldMCPRead, Value: item.Title}, {Name: ports.FieldMCPRead, Value: item.Content}}
	for _, tag := range item.Tags {
		fields = append(fields, ports.TextField{Name: ports.FieldMCPRead, Value: tag})
	}
	result, err := server.config.Guard.Check(ctx, application.OutputRequest{Route: ports.FieldMCPRead, WorkspaceID: workspaceID, MemoryID: item.MemoryID, RevisionID: item.RevisionID, Fields: fields})
	if err != nil {
		return err
	}
	if !result.Allowed {
		return domain.NewError(domain.CodeQuarantine, "content quarantined", false)
	}
	return nil
}

func decodeJSON(request *http.Request, destination any) error {
	return decodeJSONWithUnknownPolicy(request, destination, true)
}

func decodeJSONAllowUnknown(request *http.Request, destination any) error {
	return decodeJSONWithUnknownPolicy(request, destination, false)
}

func decodeJSONWithUnknownPolicy(request *http.Request, destination any, rejectUnknown bool) error {
	if request == nil || request.Body == nil {
		return domain.NewError(domain.CodeValidation, "JSON body is required", false)
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, domain.MaxHTTPRequestBodyBytes+1))
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return &RequestError{Status: http.StatusRequestEntityTooLarge, Code: domain.CodeValidation, Message: "request body too large"}
		}
		return domain.NewError(domain.CodeValidation, "request body unavailable", false)
	}
	if int64(len(body)) > domain.MaxHTTPRequestBodyBytes {
		return &RequestError{Status: http.StatusRequestEntityTooLarge, Code: domain.CodeValidation, Message: "request body too large"}
	}
	if len(body) == 0 {
		return domain.NewError(domain.CodeValidation, "JSON body is required", false)
	}
	if !utf8.Valid(body) {
		return domain.NewError(domain.CodeValidation, "invalid JSON UTF-8", false)
	}
	if err := domain.RejectDuplicateJSONKeys(body); err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if rejectUnknown {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return domain.NewError(domain.CodeValidation, "invalid JSON request", false)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return domain.NewError(domain.CodeValidation, "invalid JSON request", false)
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
