package security

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testAuthenticator(t *testing.T) (*Authenticator, string) {
	t.Helper()
	token, err := GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewAuthenticator(AuthConfig{Token: token, Host: "127.0.0.1:7331"})
	if err != nil {
		t.Fatal(err)
	}
	return authenticator, token
}

func validRequest(token string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/healthz", nil)
	request.RemoteAddr = "127.0.0.1:42317"
	request.Host = "127.0.0.1:7331"
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func TestAuthAcceptsAuthenticatedLiteralLoopback(t *testing.T) {
	authenticator, token := testAuthenticator(t)
	if err := authenticator.ValidateRequest(validRequest(token), false); err != nil {
		t.Fatal(err)
	}
}

func TestAuthRejectsTrustBoundaryMatrix(t *testing.T) {
	authenticator, token := testAuthenticator(t)
	cases := []struct {
		name   string
		mutate func(*http.Request)
		want   error
	}{
		{"remote hostname", func(request *http.Request) { request.RemoteAddr = "localhost:42317" }, ErrAuthLoopback},
		{"remote other address", func(request *http.Request) { request.RemoteAddr = "127.0.0.2:42317" }, ErrAuthLoopback},
		{"host mismatch", func(request *http.Request) { request.Host = "localhost:7331" }, ErrAuthHost},
		{"origin", func(request *http.Request) { request.Header.Set("Origin", "http://evil.invalid") }, ErrAuthOrigin},
		{"origin null", func(request *http.Request) { request.Header.Set("Origin", "null") }, ErrAuthOrigin},
		{"forwarded", func(request *http.Request) { request.Header.Set("X-Forwarded-For", "127.0.0.1") }, ErrAuthForwarded},
		{"query credential", func(request *http.Request) { request.URL.RawQuery = "token=" + token }, ErrAuthCredentialPath},
		{"cookie credential", func(request *http.Request) { request.AddCookie(&http.Cookie{Name: "access_token", Value: token}) }, ErrAuthCredentialPath},
		{"missing auth", func(request *http.Request) { request.Header.Del("Authorization") }, ErrAuthRequired},
		{"stale auth", func(request *http.Request) { request.Header.Set("Authorization", "Bearer "+strings.Repeat("A", 43)) }, ErrAuthRequired},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest(token)
			test.mutate(request)
			if err := authenticator.Authenticate(request); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAuthRejectsMutationMethodAndContentTypeViolations(t *testing.T) {
	authenticator, token := testAuthenticator(t)
	request := validRequest(token)
	request.Method = http.MethodPost
	if err := authenticator.ValidateRequest(request, true); !errors.Is(err, ErrAuthContentType) {
		t.Fatalf("missing content type error = %v", err)
	}
	request.Header.Set("Content-Type", "text/plain")
	if err := authenticator.ValidateRequest(request, true); !errors.Is(err, ErrAuthContentType) {
		t.Fatalf("text content type error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	if err := authenticator.ValidateRequest(request, true); err != nil {
		t.Fatal(err)
	}
	request.Method = http.MethodGet
	if err := authenticator.ValidateRequest(request, true); !errors.Is(err, ErrAuthMethod) {
		t.Fatalf("GET mutation error = %v", err)
	}
}

func TestAuthLoopbackAddressAndEndpoint(t *testing.T) {
	for _, address := range []string{"127.0.0.1:7331", "[::1]:7331"} {
		if err := ValidateListenAddress(address); err != nil {
			t.Fatalf("address %q: %v", address, err)
		}
		if err := ValidateConfiguredHost(address); err != nil {
			t.Fatalf("host %q: %v", address, err)
		}
		if err := ValidateLoopbackEndpoint("http://" + address + "/control/v1"); err != nil {
			t.Fatalf("endpoint %q: %v", address, err)
		}
	}
	for _, address := range []string{"localhost:7331", "0.0.0.0:7331", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:7331:extra"} {
		if err := ValidateListenAddress(address); err == nil {
			t.Fatalf("accepted unsafe listen address %q", address)
		}
	}
	for _, endpoint := range []string{"http://localhost:7331", "https://127.0.0.1:7331", "http://127.0.0.1:7331?token=x", "http://user@127.0.0.1:7331"} {
		if err := ValidateLoopbackEndpoint(endpoint); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", endpoint)
		}
	}
}

func TestAuthMiddlewareUsesReaderWriterBounds(t *testing.T) {
	authenticator, token := testAuthenticator(t)
	if cap(authenticator.reads) != MaxConcurrentReads || cap(authenticator.writer) != 1 {
		t.Fatal("unexpected concurrency bounds")
	}
	called := false
	handler := authenticator.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		writer.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	request := validRequest(token)
	handler.ServeHTTP(recorder, request)
	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("middleware response = %d, called = %v", recorder.Code, called)
	}
}
