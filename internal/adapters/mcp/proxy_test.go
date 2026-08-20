package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/security"
)

func TestProxyBridgesJSONRPCAndRetainsSession(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.Header.Get("Authorization") == "" || strings.Contains(request.Header.Get("Authorization"), "\n") {
			t.Errorf("authorization missing or malformed")
		}
		if calls == 1 {
			writer.Header().Set("Mcp-Session-Id", "session-1")
		}
		body, _ := io.ReadAll(request.Body)
		var envelope map[string]any
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": envelope["id"], "result": map[string]any{"ok": true}})
	}))
	defer server.Close()
	tokenPath := proxyToken(t)
	var output bytes.Buffer
	proxy, err := NewProxy(ProxyConfig{Endpoint: server.URL, TokenPath: tokenPath, Input: strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n"), Output: &output})
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || strings.Count(output.String(), `"jsonrpc":"2.0"`) != 2 {
		t.Fatalf("calls=%d output=%q", calls, output.String())
	}
}

func TestProxyRejectsInvalidFramesAndDoesNotEchoSecrets(t *testing.T) {
	tokenPath := proxyToken(t)
	canary := "PROXY_BODY_SECRET_CANARY"
	proxy, err := NewProxy(ProxyConfig{Endpoint: "http://127.0.0.1:7437/mcp", TokenPath: tokenPath, Input: strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"x\",\"secret\":\"" + canary + "\"}\n"), Output: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	err = proxy.Run(context.Background())
	if !errors.Is(err, ErrProxyInput) && !errors.Is(err, ErrProxyUnavailable) {
		t.Fatalf("proxy error=%v", err)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("secret echoed in proxy error: %v", err)
	}
}

func TestProxyRequiresOwnerOnlyTokenFileAndLoopbackEndpoint(t *testing.T) {
	if _, err := NewProxy(ProxyConfig{Endpoint: "http://localhost:7437/mcp", TokenPath: "token", Input: strings.NewReader(""), Output: io.Discard}); err == nil {
		t.Fatal("hostname endpoint accepted")
	}
	if _, err := NewProxy(ProxyConfig{Endpoint: "https://127.0.0.1:7437/mcp", TokenPath: "token", Input: strings.NewReader(""), Output: io.Discard}); err == nil {
		t.Fatal("HTTPS endpoint accepted")
	}
}

func proxyToken(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "token")
	if _, err := security.CreateBearerToken(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return path
}
