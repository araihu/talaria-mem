package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/security"
)

func TestContractMiddlewareRejectsForwardingAndWrongHost(t *testing.T) {
	token, err := security.GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := security.NewAuthenticator(security.AuthConfig{Token: token, Host: "127.0.0.1:7437"})
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	handler := Middleware(authenticator, next)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7437/readyz", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Host = "127.0.0.1:7437"
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("forwarded status = %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("forwarded content type = %q", recorder.Header().Get("Content-Type"))
	}
	request = httptest.NewRequest(http.MethodGet, "http://localhost:7437/readyz", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Host = "localhost:7437"
	request.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("host status = %d", recorder.Code)
	}
}
