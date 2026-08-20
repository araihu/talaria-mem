package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	httpadapter "github.com/guilhermecastro/talaria-mem/internal/adapters/http"
	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/guilhermecastro/talaria-mem/internal/security"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ProtocolVersion = "2025-03-26"
	ServerName      = "talaria-mem"
	ServerVersion   = "0.0.2"
)

// ReadService is intentionally compatible with the retrieval/application
// services. Those services own eligibility, ordering, usage, and the T5
// ContentOutputGuard; MCP is only a transport boundary.
type ReadService interface {
	Search(context.Context, retrieval.SearchRequest) (retrieval.SearchResult, error)
	Get(context.Context, string, string, string, ports.FieldIdentifier) (retrieval.SearchItem, error)
	Explain(context.Context, string) (application.Explanation, error)
}

// ServiceReader is the canonical composition adapter for the shared
// retrieval/application services.
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

// Optional guard injection is useful for transport tests and for alternate
// readers. The canonical retrieval.Searcher already invokes its guard, so a
// composed server should leave Guard nil when it supplies that implementation.
type Guard interface {
	Check(context.Context, application.OutputRequest) (application.OutputResult, error)
}

type ServerConfig struct {
	Authenticator *security.Authenticator
	Reader        ReadService
	Mutator       MutationService
	Generated     GeneratedMutationService
	Guard         Guard
}

type Server struct {
	config  ServerConfig
	sdk     *sdk.Server
	handler http.Handler
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.Authenticator == nil {
		return nil, security.ErrAuthRequired
	}
	server := &Server{config: config}
	server.sdk = sdk.NewServer(&sdk.Implementation{Name: ServerName, Version: ServerVersion}, &sdk.ServerOptions{
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	server.registerSDKTools()
	server.handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		return server.sdk
	}, &sdk.StreamableHTTPOptions{
		JSONResponse:                 true,
		Stateless:                    true,
		MaxRequestBodyBytes:          domain.MaxHTTPRequestBodyBytes,
		PropagateRequestCancellation: true,
	})
	return server, nil
}

func (server *Server) Handler() http.Handler {
	if server == nil || server.config.Authenticator == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writeRPCError(writer, nil, rpcError{Code: -32001, Message: "authentication required"})
		})
	}
	return httpadapter.Middleware(server.config.Authenticator, http.HandlerFunc(server.serveSDK))
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	server.Handler().ServeHTTP(writer, request)
}

func (server *Server) serveRPC(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeRPCError(writer, nil, rpcError{Code: -32600, Message: "POST required"})
		return
	}
	accept := request.Header.Get("Accept")
	if accept != "" && !strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/event-stream") {
		writeRPCError(writer, nil, rpcError{Code: -32600, Message: "unsupported MCP response type"})
		return
	}
	body, err := readRPCBody(request)
	if err != nil {
		writeRPCError(writer, nil, rpcError{Code: -32600, Message: "invalid JSON-RPC request"})
		return
	}
	var call rpcRequest
	if err := json.Unmarshal(body, &call); err != nil || call.JSONRPC != "2.0" || call.Method == "" {
		writeRPCError(writer, call.ID, rpcError{Code: -32600, Message: "invalid JSON-RPC request"})
		return
	}
	if len(call.ID) == 0 && strings.HasPrefix(call.Method, "notifications/") {
		writer.WriteHeader(http.StatusAccepted)
		return
	}
	if call.Method == "initialize" && request.Header.Get("Mcp-Session-Id") == "" {
		if session, err := security.GenerateBearerToken(); err == nil {
			writer.Header().Set("Mcp-Session-Id", session)
		}
	}
	result, rpcErr := server.dispatch(request.Context(), request, call)
	if rpcErr != nil {
		writeRPCError(writer, call.ID, *rpcErr)
		return
	}
	writeRPCResult(writer, call.ID, result)
}

// serveSDK adds Talaria's strict JSON/body/session boundary around the
// official SDK's Streamable HTTP transport. The SDK owns JSON-RPC framing,
// initialization, tool discovery, and request lifecycle; this adapter owns
// duplicate-key rejection, the one MiB limit, and the local session policy.
func (server *Server) serveSDK(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost {
		body, err := readRPCBody(request)
		if err != nil {
			writeRPCError(writer, nil, rpcError{Code: -32600, Message: "invalid JSON-RPC request"})
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		if !strings.Contains(request.Header.Get("Accept"), "text/event-stream") {
			request.Header.Set("Accept", request.Header.Get("Accept")+", text/event-stream")
		}
		method := rpcMethod(body)
		session := request.Header.Get("Mcp-Session-Id")
		if method == "initialize" && session == "" {
			generated, err := security.GenerateBearerToken()
			if err != nil {
				writeRPCError(writer, nil, rpcError{Code: -32002, Message: "session unavailable"})
				return
			}
			request.Header.Set("Mcp-Session-Id", generated)
			writer.Header().Set("Mcp-Session-Id", generated)
		}
		if method == "tools/call" {
			if session == "" || !safeSession(session) {
				writeRPCError(writer, nil, rpcError{Code: -32602, Message: "MCP session is required"})
				return
			}
			if name := rpcToolName(body); name != "" && !isKnownTool(name) {
				writeRPCError(writer, nil, rpcError{Code: -32601, Message: "tool not found"})
				return
			}
		}
	}
	if server.handler == nil {
		writeRPCError(writer, nil, rpcError{Code: -32002, Message: "MCP unavailable"})
		return
	}
	server.handler.ServeHTTP(writer, request)
}

func rpcMethod(body []byte) string {
	var envelope struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	return envelope.Method
}

func rpcToolName(body []byte) string {
	var envelope struct {
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	return envelope.Params.Name
}

func isReadTool(name string) bool {
	switch name {
	case "memory_search", "memory_get", "memory_explain":
		return true
	default:
		return false
	}
}

func isKnownTool(name string) bool {
	if isReadTool(name) {
		return true
	}
	switch name {
	case "memory_create", "memory_update", "memory_pin", "memory_forget", "memory_curate_inline":
		return true
	default:
		return false
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (server *Server) dispatch(ctx context.Context, request *http.Request, call rpcRequest) (any, *rpcError) {
	switch call.Method {
	case "initialize":
		return map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": ServerName, "version": ServerVersion}}, nil
	case "tools/list":
		return map[string]any{"tools": AllToolDefinitions()}, nil
	case "tools/call":
		return server.callTool(ctx, request, call.Params)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func (server *Server) callTool(ctx context.Context, request *http.Request, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &params) != nil || params.Name == "" {
		return nil, &rpcError{Code: -32602, Message: "tool name is required"}
	}
	if params.Name != "memory_search" && params.Name != "memory_get" && params.Name != "memory_explain" && params.Name != "memory_create" && params.Name != "memory_update" && params.Name != "memory_pin" && params.Name != "memory_forget" && params.Name != "memory_curate_inline" {
		return nil, &rpcError{Code: -32601, Message: "tool not found"}
	}
	if (params.Name == "memory_search" || params.Name == "memory_get" || params.Name == "memory_explain") && server.config.Reader == nil {
		return nil, &rpcError{Code: -32002, Message: "retrieval unavailable"}
	}
	if params.Name != "memory_search" && params.Name != "memory_get" && params.Name != "memory_explain" {
		return server.mutationTool(ctx, params.Name, params.Arguments)
	}
	session := request.Header.Get("Mcp-Session-Id")
	if session == "" {
		return nil, &rpcError{Code: -32602, Message: "MCP session is required"}
	}
	if !safeSession(session) {
		return nil, &rpcError{Code: -32602, Message: "MCP session is invalid"}
	}
	return server.readTool(ctx, params.Name, params.Arguments, session)
}

func safeSession(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
}

func readRPCBody(request *http.Request) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, errors.New("missing body")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, domain.MaxHTTPRequestBodyBytes+1))
	if err != nil || int64(len(body)) > domain.MaxHTTPRequestBodyBytes {
		return nil, errors.New("body limit")
	}
	if err := domain.RejectDuplicateJSONKeys(body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeRPCResult(writer http.ResponseWriter, id json.RawMessage, result any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeRPCError(writer http.ResponseWriter, id json.RawMessage, err rpcError) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(rpcResponse{JSONRPC: "2.0", ID: id, Error: &err})
}
