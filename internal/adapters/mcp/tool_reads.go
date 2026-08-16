package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func ReadToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{Name: "memory_search", Description: "Search verified memories in one explicit workspace scope.", InputSchema: objectSchema(map[string]any{
			"query":        map[string]any{"type": "string", "minLength": 1, "maxLength": domain.MaxQueryBytes},
			"workspace_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": domain.MaxSearchItems},
		}, []string{"query", "workspace_id"})},
		{Name: "memory_get", Description: "Get one verified memory after workspace scope and output scanning.", InputSchema: objectSchema(map[string]any{
			"memory_id":    map[string]any{"type": "string", "format": "uuid"},
			"workspace_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
		}, []string{"memory_id", "workspace_id"})},
		{Name: "memory_explain", Description: "Explain safe lifecycle and ranking metadata without memory content.", InputSchema: objectSchema(map[string]any{
			"memory_id": map[string]any{"type": "string", "format": "uuid"},
		}, []string{"memory_id"})},
	}
}

func (server *Server) registerSDKTools() {
	for _, definition := range ReadToolDefinitions() {
		definition := definition
		server.sdk.AddTool(&sdk.Tool{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: definition.InputSchema,
		}, func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			if request == nil || request.Params == nil {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "tool arguments are required"}
			}
			session := ""
			if extra := request.GetExtra(); extra != nil {
				session = extra.Header.Get("Mcp-Session-Id")
			}
			if session == "" || !safeSession(session) {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "MCP session is required"}
			}
			if server.config.Reader == nil {
				return nil, &jsonrpc.Error{Code: -32002, Message: "retrieval unavailable"}
			}
			value, rpcErr := server.readTool(ctx, definition.Name, request.Params.Arguments, session)
			if rpcErr != nil {
				return nil, &jsonrpc.Error{Code: int64(rpcErr.Code), Message: rpcErr.Message}
			}
			return sdkResult(value), nil
		})
	}
}

func sdkResult(value any) *sdk.CallToolResult {
	result, ok := value.(map[string]any)
	if !ok {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "verified memory result"}}}
	}
	text := "verified memory result"
	if content, ok := result["content"].([]map[string]string); ok && len(content) > 0 {
		text = content[0]["text"]
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}, StructuredContent: result["structuredContent"]}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}

func (server *Server) readTool(ctx context.Context, name string, raw json.RawMessage, session string) (any, *rpcError) {
	if server == nil || server.config.Reader == nil {
		return nil, &rpcError{Code: -32002, Message: "retrieval unavailable"}
	}
	switch name {
	case "memory_search":
		var input struct {
			Query       string `json:"query"`
			WorkspaceID string `json:"workspace_id"`
			Limit       int    `json:"limit"`
		}
		if err := decodeToolArguments(raw, &input); err != nil || input.Query == "" || input.WorkspaceID == "" {
			return nil, &rpcError{Code: -32602, Message: "query and workspace are required"}
		}
		if input.Limit < 0 || input.Limit > domain.MaxSearchItems {
			return nil, &rpcError{Code: -32602, Message: "limit is out of range"}
		}
		result, err := server.config.Reader.Search(ctx, retrieval.SearchRequest{Query: input.Query, WorkspaceID: input.WorkspaceID, ConsumerSession: session, Limit: input.Limit, Route: ports.FieldMCPRead})
		if err != nil {
			return nil, rpcDomainError(err)
		}
		items := make([]map[string]any, 0, len(result.Items))
		for _, item := range result.Items {
			if err := server.guardItem(ctx, item, input.WorkspaceID); err != nil {
				return nil, rpcDomainError(err)
			}
			items = append(items, searchItemMap(item))
		}
		return toolResult(map[string]any{"version": "talaria.memory-search.v1", "items": items, "included": result.Included, "omitted": result.Omitted}), nil
	case "memory_get":
		var input struct {
			MemoryID    string `json:"memory_id"`
			WorkspaceID string `json:"workspace_id"`
		}
		if err := decodeToolArguments(raw, &input); err != nil || input.MemoryID == "" || input.WorkspaceID == "" {
			return nil, &rpcError{Code: -32602, Message: "memory and workspace are required"}
		}
		if err := domain.ValidateUUIDv7(input.MemoryID); err != nil {
			return nil, &rpcError{Code: -32602, Message: "memory ID is invalid"}
		}
		item, err := server.config.Reader.Get(ctx, input.MemoryID, input.WorkspaceID, session, ports.FieldMCPRead)
		if err != nil {
			return nil, rpcDomainError(err)
		}
		if err := server.guardItem(ctx, item, input.WorkspaceID); err != nil {
			return nil, rpcDomainError(err)
		}
		return toolResult(searchItemMap(item)), nil
	case "memory_explain":
		var input struct {
			MemoryID string `json:"memory_id"`
		}
		if err := decodeToolArguments(raw, &input); err != nil || input.MemoryID == "" {
			return nil, &rpcError{Code: -32602, Message: "memory is required"}
		}
		if err := domain.ValidateUUIDv7(input.MemoryID); err != nil {
			return nil, &rpcError{Code: -32602, Message: "memory ID is invalid"}
		}
		explanation, err := server.config.Reader.Explain(ctx, input.MemoryID)
		if err != nil {
			return nil, rpcDomainError(err)
		}
		return toolResult(map[string]any{"memory_id": explanation.MemoryID, "revision_id": explanation.RevisionID, "trust": string(explanation.Trust), "lifecycle": string(explanation.Lifecycle), "pinned": explanation.Pinned, "created_at": explanation.CreatedAt.UTC(), "updated_at": explanation.UpdatedAt.UTC(), "resolution_state": string(explanation.ResolutionState)}), nil
	default:
		return nil, &rpcError{Code: -32601, Message: "tool not found"}
	}
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

func decodeToolArguments(raw json.RawMessage, destination any) error {
	if len(raw) == 0 {
		return domain.NewError(domain.CodeValidation, "tool arguments are required", false)
	}
	if err := domain.RejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	// A single JSON value is the entire tool argument document. Without this
	// second decode, a valid object followed by another value would be silently
	// accepted and the transport would not enforce its strict JSON contract.
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return domain.NewError(domain.CodeValidation, "invalid tool arguments", false)
	}
	return nil
}

func toolResult(value any) map[string]any {
	encoded, err := json.Marshal(value)
	text := "verified memory result"
	if err == nil {
		text = string(encoded)
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "structuredContent": value, "isError": false}
}

func searchItemMap(item retrieval.SearchItem) map[string]any {
	return map[string]any{"memory_id": item.MemoryID, "revision_id": item.RevisionID, "workspace_id": item.WorkspaceID, "kind": string(item.Kind), "title": item.Title, "content": item.Content, "tags": append([]string(nil), item.Tags...), "trust": "verified", "lifecycle": "active", "resolution_state": string(item.ResolutionState), "score": item.Score.FinalScore}
}

func rpcDomainError(err error) *rpcError {
	if typed := domain.CodeOf(err); typed != "" {
		code := -32000
		switch typed {
		case domain.CodeValidation:
			code = -32602
		case domain.CodeNotFound:
			code = -32004
		case domain.CodeRevisionConflict, domain.CodeIdempotencyConflict:
			code = -32009
		case domain.CodeSecretRefusal, domain.CodeQuarantine:
			code = -32005
		}
		return &rpcError{Code: code, Message: safeRPCMessage(typed)}
	}
	return &rpcError{Code: -32002, Message: "service unavailable"}
}

func safeRPCMessage(code domain.ErrorCode) string {
	switch code {
	case domain.CodeValidation:
		return "invalid request"
	case domain.CodeNotFound:
		return "not found"
	case domain.CodeRevisionConflict, domain.CodeIdempotencyConflict:
		return "conflict"
	case domain.CodeSecretRefusal, domain.CodeQuarantine:
		return "content refused"
	default:
		return "service unavailable"
	}
}

var _ Guard = (*application.ContentOutputGuard)(nil)
