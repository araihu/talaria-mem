package httpadapter

import (
	"os"
	"strings"
	"testing"
)

func TestContractGeneratedOpenAPIIdentity(t *testing.T) {
	data, err := os.ReadFile("../../../api/openapi/talaria.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, path := range []string{"/healthz:", "/readyz:", "/control/v1/session-start:", "/control/v1/memory/search:", "/control/v1/memory/{memory_id}/explain:"} {
		if !strings.Contains(text, path) {
			t.Fatalf("missing path %s", path)
		}
	}
	if !strings.Contains(text, "additionalProperties: false") || !strings.Contains(text, "talaria.error.v1") {
		t.Fatal("strict error contract missing")
	}
}
