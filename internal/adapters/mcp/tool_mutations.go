package mcp

import (
	"context"
	"encoding/json"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

// MutationService is intentionally narrower than MemoryService so MCP cannot
// reach any CLI-only trust or maintenance operation.
type MutationService interface {
	Create(context.Context, application.MutationRequest) (application.MutationResult, error)
	Update(context.Context, application.MutationRequest) (application.MutationResult, error)
	Pin(context.Context, application.MutationRequest) (application.MutationResult, error)
	Forget(context.Context, application.MutationRequest) (application.MutationResult, error)
}

func MutationToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{Name: "memory_create", Description: "Create an unverified memory in one explicit workspace scope.", InputSchema: objectSchema(map[string]any{
			"workspace_id":     map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"global":           map[string]any{"type": "boolean"},
			"kind":             map[string]any{"type": "string", "enum": []string{"state", "procedure", "failure"}},
			"title":            map[string]any{"type": "string", "minLength": 1, "maxLength": domain.MaxTitleBytes},
			"content":          map[string]any{"type": "string", "minLength": 1, "maxLength": domain.MaxContentBytes},
			"tags":             map[string]any{"type": "array", "maxItems": domain.MaxTags, "items": map[string]any{"type": "string", "maxLength": domain.MaxTagBytes}},
			"resolution_state": map[string]any{"type": "string", "enum": []string{"open", "resolved"}},
			"source":           map[string]any{"type": "string"},
			"source_locator":   map[string]any{"type": "string"},
			"idempotency_key":  map[string]any{"type": "string", "maxLength": 256},
		}, []string{"kind", "title", "content"})},
		{Name: "memory_update", Description: "Update a memory as unverified content using an exact revision.", InputSchema: objectSchema(map[string]any{
			"memory_id":         map[string]any{"type": "string", "format": "uuid"},
			"expected_revision": map[string]any{"type": "string", "format": "uuid"},
			"kind":              map[string]any{"type": "string", "enum": []string{"state", "procedure", "failure"}},
			"title":             map[string]any{"type": "string", "maxLength": domain.MaxTitleBytes},
			"content":           map[string]any{"type": "string", "maxLength": domain.MaxContentBytes},
			"tags":              map[string]any{"type": "array", "maxItems": domain.MaxTags, "items": map[string]any{"type": "string", "maxLength": domain.MaxTagBytes}},
			"resolution_state":  map[string]any{"type": "string", "enum": []string{"open", "resolved"}},
			"workspace_id":      map[string]any{"type": "string", "maxLength": 256},
			"source":            map[string]any{"type": "string"},
			"source_locator":    map[string]any{"type": "string"},
			"idempotency_key":   map[string]any{"type": "string", "maxLength": 256},
		}, []string{"memory_id", "expected_revision"})},
		{Name: "memory_pin", Description: "Pin a verified memory using an exact revision.", InputSchema: objectSchema(map[string]any{
			"memory_id":         map[string]any{"type": "string", "format": "uuid"},
			"expected_revision": map[string]any{"type": "string", "format": "uuid"},
			"idempotency_key":   map[string]any{"type": "string", "maxLength": 256},
		}, []string{"memory_id", "expected_revision"})},
		{Name: "memory_forget", Description: "Forget a memory using an exact revision.", InputSchema: objectSchema(map[string]any{
			"memory_id":         map[string]any{"type": "string", "format": "uuid"},
			"expected_revision": map[string]any{"type": "string", "format": "uuid"},
			"idempotency_key":   map[string]any{"type": "string", "maxLength": 256},
		}, []string{"memory_id", "expected_revision"})},
	}
}

func AllToolDefinitions() []ToolDefinition {
	definitions := ReadToolDefinitions()
	return append(definitions, MutationToolDefinitions()...)
}

func (server *Server) mutationTool(ctx context.Context, name string, raw json.RawMessage) (any, *rpcError) {
	if server.config.Mutator == nil {
		return nil, &rpcError{Code: -32002, Message: "mutation service unavailable"}
	}
	switch name {
	case "memory_create":
		var input struct {
			WorkspaceID     string                 `json:"workspace_id"`
			Global          bool                   `json:"global"`
			Kind            domain.MemoryKind      `json:"kind"`
			Title           string                 `json:"title"`
			Content         string                 `json:"content"`
			Tags            []string               `json:"tags"`
			ResolutionState domain.ResolutionState `json:"resolution_state"`
			Source          string                 `json:"source"`
			SourceLocator   string                 `json:"source_locator"`
			IdempotencyKey  string                 `json:"idempotency_key"`
		}
		if err := decodeToolArguments(raw, &input); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid memory create arguments"}
		}
		if input.Kind == "" || input.Title == "" || input.Content == "" || (!input.Global && input.WorkspaceID == "") {
			return nil, &rpcError{Code: -32602, Message: "kind, title, content, and workspace are required"}
		}
		result, err := server.config.Mutator.Create(ctx, application.MutationRequest{Actor: application.ActorMCP, Caller: "mcp", WorkspaceID: input.WorkspaceID, UserGlobal: input.Global, IdempotencyKey: input.IdempotencyKey, Kind: input.Kind, Title: input.Title, Content: input.Content, Tags: input.Tags, ResolutionState: input.ResolutionState, Provenance: domain.Provenance{Actor: "mcp", Source: input.Source, SourceLocator: input.SourceLocator}})
		if err != nil {
			return nil, rpcDomainError(err)
		}
		return mutationResult(result), nil
	case "memory_update":
		var input struct {
			MemoryID         string                 `json:"memory_id"`
			ExpectedRevision string                 `json:"expected_revision"`
			WorkspaceID      string                 `json:"workspace_id"`
			Kind             domain.MemoryKind      `json:"kind"`
			Title            string                 `json:"title"`
			Content          string                 `json:"content"`
			Tags             []string               `json:"tags"`
			ResolutionState  domain.ResolutionState `json:"resolution_state"`
			Source           string                 `json:"source"`
			SourceLocator    string                 `json:"source_locator"`
			IdempotencyKey   string                 `json:"idempotency_key"`
		}
		if err := decodeToolArguments(raw, &input); err != nil || input.MemoryID == "" || input.ExpectedRevision == "" {
			return nil, &rpcError{Code: -32602, Message: "memory and expected revision are required"}
		}
		result, err := server.config.Mutator.Update(ctx, application.MutationRequest{Actor: application.ActorMCP, Caller: "mcp", MemoryID: input.MemoryID, ExpectedRevisionID: input.ExpectedRevision, WorkspaceID: input.WorkspaceID, IdempotencyKey: input.IdempotencyKey, Kind: input.Kind, Title: input.Title, Content: input.Content, Tags: input.Tags, ResolutionState: input.ResolutionState, Provenance: domain.Provenance{Actor: "mcp", Source: input.Source, SourceLocator: input.SourceLocator}})
		if err != nil {
			return nil, rpcDomainError(err)
		}
		return mutationResult(result), nil
	case "memory_pin", "memory_forget":
		var input struct {
			MemoryID         string `json:"memory_id"`
			ExpectedRevision string `json:"expected_revision"`
			IdempotencyKey   string `json:"idempotency_key"`
		}
		if err := decodeToolArguments(raw, &input); err != nil || input.MemoryID == "" || input.ExpectedRevision == "" {
			return nil, &rpcError{Code: -32602, Message: "memory and expected revision are required"}
		}
		request := application.MutationRequest{Actor: application.ActorMCP, Caller: "mcp", MemoryID: input.MemoryID, ExpectedRevisionID: input.ExpectedRevision, IdempotencyKey: input.IdempotencyKey}
		var result application.MutationResult
		var err error
		if name == "memory_pin" {
			result, err = server.config.Mutator.Pin(ctx, request)
		} else {
			result, err = server.config.Mutator.Forget(ctx, request)
		}
		if err != nil {
			return nil, rpcDomainError(err)
		}
		return mutationResult(result), nil
	default:
		return nil, &rpcError{Code: -32601, Message: "tool not found"}
	}
}

func mutationResult(result application.MutationResult) map[string]any {
	return toolResult(map[string]any{"version": "talaria.memory-mutation.v1", "memory_id": result.MemoryID, "revision_id": result.RevisionID, "revision_number": result.RevisionNumber, "workspace_id": result.WorkspaceID, "trust": string(result.Trust), "lifecycle": string(result.Lifecycle), "resolution_state": string(result.ResolutionState), "replayed": result.Replayed})
}

var _ MutationService = (*application.MemoryService)(nil)
