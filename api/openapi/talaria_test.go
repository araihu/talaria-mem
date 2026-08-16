package openapi

import (
	"os"
	"strings"
	"testing"
)

func TestContractSourceContainsVersionedControlRoutes(t *testing.T) {
	data, err := os.ReadFile("talaria.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "openapi: 3.1.0") {
		t.Fatal("OpenAPI version missing")
	}
	for _, route := range []string{"/control/v1/session-start:", "/healthz:", "/readyz:", "/control/v1/memory/search:"} {
		if !strings.Contains(text, route) {
			t.Fatalf("route missing: %s", route)
		}
	}
	if strings.Contains(text, "password") || strings.Contains(text, "token:") {
		t.Fatal("credential-like value in contract")
	}
}
