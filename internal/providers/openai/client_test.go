package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

func TestCuratorPostsStrictStructuredRequestAndParsesCandidate(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"{\"candidates\":[{\"kind\":\"procedure\",\"title\":\"Use cache\",\"content\":\"Keep cache warm\"}]}"}}]}`))
	}))
	defer server.Close()
	client, err := NewClient(ClientConfig{BaseURL: server.URL + "/v1", Model: "local-model", Credential: "secret", Scanner: snapshotScanner{}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Curate(context.Background(), curation.CurationRequest{Snapshot: []byte(`{"turns":[{"role":"user","text":"safe"}]}`)})
	if err != nil {
		t.Fatalf("Curate() error = %v", err)
	}
	if result.Provider != "openai_compatible" || result.Model != "local-model" || len(result.Candidates) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if requestBody["response_format"] == nil || requestBody["model"] != "local-model" {
		t.Fatalf("request body = %#v", requestBody)
	}
}

func TestCuratorMapsHTTPFailureClasses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		class  curation.ErrorClass
	}{
		{name: "authentication", status: http.StatusUnauthorized, class: curation.ErrorAuthentication},
		{name: "rate limit", status: http.StatusTooManyRequests, class: curation.ErrorRateLimit},
		{name: "unavailable", status: http.StatusBadGateway, class: curation.ErrorUnavailable},
		{name: "policy", status: http.StatusBadRequest, class: curation.ErrorPolicyRefusal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(`{"error":"provider failure"}`))
			}))
			defer server.Close()
			client, err := NewClient(ClientConfig{BaseURL: server.URL + "/v1", Model: "model", Scanner: snapshotScanner{}, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Curate(context.Background(), curation.CurationRequest{Snapshot: []byte(`{"turns":[]}`)})
			if !IsClass(err, test.class) {
				t.Fatalf("Curate() error = %v, want %s", err, test.class)
			}
		})
	}
}

func TestCuratorRefusesScannerUncertaintyBeforeHTTP(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		called = true
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(ClientConfig{BaseURL: server.URL + "/v1", Model: "model", Scanner: scannerStatus{status: ports.ScanUncertain}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Curate(context.Background(), curation.CurationRequest{Snapshot: []byte(`{"turns":[]}`)})
	if !IsClass(err, curation.ErrorScannerRefusal) || called {
		t.Fatalf("Curate() error=%v called=%v", err, called)
	}
}

func TestNewClientRejectsRemotePlainHTTPAndRedirects(t *testing.T) {
	if _, err := NewClient(ClientConfig{BaseURL: "http://localhost:11434/v1", Model: "model", Scanner: snapshotScanner{}}); err == nil {
		t.Fatal("NewClient() accepted hostname HTTP")
	}
	redirectTargetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { redirectTargetCalled = true }))
	defer target.Close()
	source := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer source.Close()
	client, err := NewClient(ClientConfig{BaseURL: source.URL + "/v1", Model: "model", Scanner: snapshotScanner{}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Curate(context.Background(), curation.CurationRequest{Snapshot: []byte(`{"turns":[]}`)})
	if !IsClass(err, curation.ErrorUnavailable) || redirectTargetCalled {
		t.Fatalf("redirect error=%v targetCalled=%v", err, redirectTargetCalled)
	}
}

func TestCuratorRedactsOutboundSecretAndRescans(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		body = string(data)
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"{\"candidates\":[]}"}}]}`))
	}))
	defer server.Close()
	client, err := NewClient(ClientConfig{BaseURL: server.URL + "/v1", Model: "model", Scanner: snapshotScanner{find: "secret"}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Curate(context.Background(), curation.CurationRequest{Snapshot: []byte(`{"turns":[{"text":"secret"}]}`)})
	if err != nil {
		t.Fatalf("Curate() error = %v", err)
	}
	if strings.Contains(body, "secret") {
		t.Fatalf("request retained secret: %s", body)
	}
}
