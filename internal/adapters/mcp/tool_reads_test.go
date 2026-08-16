package mcp

import (
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
)

func TestMCPToolSchemasRejectUnverifiedOverride(t *testing.T) {
	for _, definition := range ReadToolDefinitions() {
		if strings.Contains(strings.ToLower(definition.Description), "unverified") {
			t.Fatal("unverified content mentioned in read tool")
		}
		if _, found := definition.InputSchema["properties"].(map[string]any)["include_unverified"]; found {
			t.Fatalf("tool %s accepts include_unverified", definition.Name)
		}
	}
}

func TestMCPToolArgumentsRejectTrailingValuesAndInvalidLimit(t *testing.T) {
	var destination struct {
		Query string `json:"query"`
	}
	if err := decodeToolArguments([]byte(`{"query":"one"} {"query":"two"}`), &destination); err == nil {
		t.Fatal("concatenated JSON arguments accepted")
	}
	if err := decodeToolArguments([]byte(`{"query":"one"}`), &destination); err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}

	server := &Server{config: ServerConfig{Reader: testReader{item: retrieval.SearchItem{MemoryID: "m", RevisionID: "r", WorkspaceID: "w"}}}}
	_, rpcErr := server.readTool(t.Context(), "memory_search", []byte(`{"query":"q","workspace_id":"w","limit":21}`), "session")
	if rpcErr == nil || rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "limit") {
		t.Fatalf("invalid limit error = %+v", rpcErr)
	}
	_, rpcErr = server.readTool(t.Context(), "memory_get", []byte(`{"memory_id":"m","workspace_id":"w"}`), "session")
	if rpcErr == nil || rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "memory ID") {
		t.Fatalf("invalid memory ID error = %+v", rpcErr)
	}
}
