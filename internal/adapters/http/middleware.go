package httpadapter

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/security"
)

// Middleware is the single HTTP authentication boundary. Routes must not
// bypass it by calling their handlers directly in the composed server.
func Middleware(authenticator *security.Authenticator, next http.Handler) http.Handler {
	if authenticator == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writeError(writer, security.ErrAuthRequired)
		})
	}
	secured := authenticator.Middleware(next)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		buffer := &responseBuffer{header: make(http.Header)}
		secured.ServeHTTP(buffer, request)
		buffer.flush(writer)
	})
}

// responseBuffer lets the adapter normalize the security package's
// deliberately terse auth rejection into the versioned JSON envelope used by
// the control API. Successful handler responses are copied byte-for-byte.
type responseBuffer struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (buffer *responseBuffer) Header() http.Header { return buffer.header }

func (buffer *responseBuffer) WriteHeader(status int) {
	if buffer.status == 0 {
		buffer.status = status
	}
}

func (buffer *responseBuffer) Write(data []byte) (int, error) {
	if buffer.status == 0 {
		buffer.status = http.StatusOK
	}
	return buffer.body.Write(data)
}

func (buffer *responseBuffer) flush(writer http.ResponseWriter) {
	for key, values := range buffer.header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	status := buffer.status
	if status == 0 {
		status = http.StatusOK
	}
	if isAuthRejection(buffer) {
		writeError(writer, authRejectionError(status))
		return
	}
	writer.WriteHeader(status)
	_, _ = writer.Write(buffer.body.Bytes())
}

func isAuthRejection(buffer *responseBuffer) bool {
	if buffer == nil || !strings.HasPrefix(strings.ToLower(buffer.header.Get("Content-Type")), "text/plain") {
		return false
	}
	return buffer.body.String() == "request rejected\n"
}

func authRejectionError(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return security.ErrAuthRequired
	case http.StatusForbidden:
		return security.ErrAuthLoopback
	case http.StatusMethodNotAllowed:
		return security.ErrAuthMethod
	case http.StatusUnsupportedMediaType:
		return security.ErrAuthContentType
	case http.StatusRequestEntityTooLarge:
		return security.ErrAuthBodyTooLarge
	case http.StatusGatewayTimeout:
		return security.ErrAuthTimeout
	case http.StatusServiceUnavailable:
		return security.ErrAuthUnavailable
	default:
		return security.ErrAuthRequired
	}
}

// LoopbackClientEndpoint validates the literal endpoint used by CLI and MCP
// clients. It is kept here as a forwarding helper so surfaces share one rule.
func LoopbackClientEndpoint(raw string) error { return security.ValidateLoopbackEndpoint(raw) }
