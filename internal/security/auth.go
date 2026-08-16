package security

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const (
	MaxRequestBodyBytes     int64         = domain.MaxHTTPRequestBodyBytes
	MaxConcurrentReads                    = domain.MaxConcurrentReads
	MaxConcurrentWriters                  = domain.MaxConcurrentWriters
	ReadRequestTimeout      time.Duration = domain.FTSDeadline
	MutationTimeout         time.Duration = domain.MutationDeadline
	ReadConcurrency                       = MaxConcurrentReads
	WriteConcurrency                      = MaxConcurrentWriters
	FTSRequestDeadline                    = ReadRequestTimeout
	MutationRequestDeadline               = MutationTimeout
)

var (
	ErrAuthRequired       = errors.New("authentication required")
	ErrAuthHost           = errors.New("request host not allowed")
	ErrAuthOrigin         = errors.New("request origin not allowed")
	ErrAuthForwarded      = errors.New("forwarding headers not allowed")
	ErrAuthCredentialPath = errors.New("credential transport not allowed")
	ErrAuthMethod         = errors.New("request method not allowed")
	ErrAuthContentType    = errors.New("request content type not allowed")
	ErrAuthBodyTooLarge   = errors.New("request body too large")
	ErrAuthUnavailable    = errors.New("request concurrency unavailable")
	ErrAuthTimeout        = errors.New("request deadline exceeded")
	ErrAuthLoopback       = errors.New("literal loopback required")
)

type AuthConfig struct {
	// Token is the per-install bearer token. It must be supplied independently
	// from any root-key-derived value.
	Token string
	// Host is the configured literal authority. AllowedHosts is an optional
	// compatibility alias for a dual-stack listener.
	Host         string
	AllowedHosts []string
	MaxBodyBytes int64
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type Authenticator struct {
	token        string
	hosts        map[string]struct{}
	maxBody      int64
	readTimeout  time.Duration
	writeTimeout time.Duration
	reads        chan struct{}
	writer       chan struct{}
}

func NewAuthenticator(configuration AuthConfig) (*Authenticator, error) {
	if !ValidateBearerTokenSyntax(configuration.Token) {
		return nil, ErrTokenInvalid
	}
	hostValues := append([]string(nil), configuration.AllowedHosts...)
	if configuration.Host != "" {
		hostValues = append(hostValues, configuration.Host)
	}
	if len(hostValues) == 0 {
		return nil, ErrAuthHost
	}
	hosts := make(map[string]struct{}, len(hostValues))
	for _, host := range hostValues {
		if err := ValidateConfiguredHost(host); err != nil {
			return nil, err
		}
		hosts[host] = struct{}{}
	}
	maxBody := configuration.MaxBodyBytes
	if maxBody == 0 {
		maxBody = MaxRequestBodyBytes
	}
	readTimeout := configuration.ReadTimeout
	if readTimeout == 0 {
		readTimeout = ReadRequestTimeout
	}
	writeTimeout := configuration.WriteTimeout
	if writeTimeout == 0 {
		writeTimeout = MutationTimeout
	}
	if maxBody <= 0 || readTimeout <= 0 || writeTimeout <= 0 {
		return nil, ErrAuthMethod
	}
	return &Authenticator{
		token:        configuration.Token,
		hosts:        hosts,
		maxBody:      maxBody,
		readTimeout:  readTimeout,
		writeTimeout: writeTimeout,
		reads:        make(chan struct{}, MaxConcurrentReads),
		writer:       make(chan struct{}, 1),
	}, nil
}

func NewAuth(configuration AuthConfig) (*Authenticator, error) {
	return NewAuthenticator(configuration)
}

// Authenticate validates every request-bound trust property. It never
// returns a request header, query value, cookie, or body in an error.
func (authenticator *Authenticator) Authenticate(request *http.Request) error {
	if authenticator == nil || request == nil {
		return ErrAuthRequired
	}
	if !isLiteralLoopbackRemote(request.RemoteAddr) {
		return ErrAuthLoopback
	}
	if _, allowed := authenticator.hosts[request.Host]; !allowed {
		return ErrAuthHost
	}
	if hasForwardingHeader(request.Header) {
		return ErrAuthForwarded
	}
	if !originAllowed(request.Header) {
		return ErrAuthOrigin
	}
	if hasCredentialInQuery(request.URL) || hasCredentialCookie(request.Cookies()) || len(request.Header.Values("Cookie")) > 0 {
		return ErrAuthCredentialPath
	}
	values := request.Header.Values("Authorization")
	if len(values) != 1 {
		return ErrAuthRequired
	}
	provided, err := ParseBearerAuthorization(values[0])
	if err != nil || !CompareBearerToken(authenticator.token, provided) {
		return ErrAuthRequired
	}
	return nil
}

// ValidateRequest applies method and body rules after Authenticate. A caller
// must pass mutation=true for routes that mutate state; GET and HEAD can never
// be treated as mutation routes.
func (authenticator *Authenticator) ValidateRequest(request *http.Request, mutation bool) error {
	if err := authenticator.Authenticate(request); err != nil {
		return err
	}
	if request == nil || !methodAllowed(request.Method, mutation) {
		return ErrAuthMethod
	}
	if mutation && !jsonContentType(request.Header.Get("Content-Type")) {
		return ErrAuthContentType
	}
	return nil
}

// Middleware enforces authentication, bounded body reads, exact reader and
// writer concurrency, and route-specific deadlines. The handler is still
// responsible for route validation and safe response serialization.
func (authenticator *Authenticator) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutation := IsMutationMethod(request.Method)
		if err := authenticator.ValidateRequest(request, mutation); err != nil {
			writeAuthFailure(writer, err)
			return
		}
		if authenticator.maxBody > 0 && request.Body != nil {
			request.Body = http.MaxBytesReader(writer, request.Body, authenticator.maxBody)
		}
		release, err := authenticator.acquire(request.Context(), mutation)
		if err != nil {
			writeAuthFailure(writer, err)
			return
		}
		defer release()
		deadline := authenticator.readTimeout
		if mutation {
			deadline = authenticator.writeTimeout
		}
		ctx, cancel := contextWithTimeout(request, deadline)
		defer cancel()
		request = request.WithContext(ctx)
		next.ServeHTTP(writer, request)
	})
}

func (authenticator *Authenticator) Handler(next http.Handler) http.Handler {
	return authenticator.Middleware(next)
}

func (authenticator *Authenticator) AuthenticateRequest(request *http.Request) error {
	return authenticator.Authenticate(request)
}

func (authenticator *Authenticator) acquire(ctx context.Context, mutation bool) (func(), error) {
	if authenticator == nil {
		return func() {}, ErrAuthRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if mutation {
		select {
		case authenticator.writer <- struct{}{}:
			return func() { <-authenticator.writer }, nil
		case <-ctx.Done():
			return func() {}, ErrAuthUnavailable
		}
	}
	select {
	case authenticator.reads <- struct{}{}:
		return func() { <-authenticator.reads }, nil
	case <-ctx.Done():
		return func() {}, ErrAuthUnavailable
	}
}

func contextWithTimeout(request *http.Request, duration time.Duration) (context.Context, context.CancelFunc) {
	if request == nil {
		return context.WithTimeout(context.Background(), duration)
	}
	return context.WithTimeout(request.Context(), duration)
}

func writeAuthFailure(writer http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	switch {
	case errors.Is(err, ErrAuthHost), errors.Is(err, ErrAuthLoopback), errors.Is(err, ErrAuthOrigin), errors.Is(err, ErrAuthForwarded), errors.Is(err, ErrAuthCredentialPath):
		status = http.StatusForbidden
	case errors.Is(err, ErrAuthMethod):
		status = http.StatusMethodNotAllowed
	case errors.Is(err, ErrAuthContentType):
		status = http.StatusUnsupportedMediaType
	case errors.Is(err, ErrAuthBodyTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrAuthUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrAuthTimeout):
		status = http.StatusGatewayTimeout
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte("request rejected\n"))
}

func ValidateConfiguredHost(host string) error {
	if host == "" {
		return ErrAuthHost
	}
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		return ErrAuthHost
	}
	if !isLiteralLoopbackHost(hostname) {
		return ErrAuthLoopback
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return ErrAuthHost
	}
	if hostname == "::1" && host != "[::1]:"+port {
		return ErrAuthHost
	}
	if hostname == "127.0.0.1" && host != "127.0.0.1:"+port {
		return ErrAuthHost
	}
	return nil
}

func ValidateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return ErrAuthLoopback
	}
	if !isLiteralLoopbackHost(host) || port == "" {
		return ErrAuthLoopback
	}
	return ValidateConfiguredHost(address)
}

func IsLiteralLoopbackHost(host string) bool { return host == "127.0.0.1" || host == "::1" }

func isLiteralLoopbackHost(host string) bool { return IsLiteralLoopbackHost(host) }

func isLiteralLoopbackRemote(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	return err == nil && isLiteralLoopbackHost(host)
}

// ValidateLoopbackEndpoint is shared by CLI, hook, and MCP client adapters.
// It intentionally rejects hostname aliases, userinfo, query credentials, and
// non-HTTP schemes, so no client can quietly widen the daemon trust boundary.
func ValidateLoopbackEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ErrAuthLoopback
	}
	if err := ValidateConfiguredHost(parsed.Host); err != nil {
		return err
	}
	return nil
}

func IsMutationMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func methodAllowed(method string, mutation bool) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead:
		return !mutation
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return mutation
	default:
		return false
	}
}

func jsonContentType(value string) bool {
	if value == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

var forwardingHeaders = map[string]struct{}{
	"forwarded":           {},
	"via":                 {},
	"x-forwarded-for":     {},
	"x-forwarded-host":    {},
	"x-forwarded-proto":   {},
	"x-forwarded-port":    {},
	"x-real-ip":           {},
	"cf-connecting-ip":    {},
	"true-client-ip":      {},
	"x-cluster-client-ip": {},
}

func hasForwardingHeader(header http.Header) bool {
	for name := range header {
		if _, found := forwardingHeaders[strings.ToLower(name)]; found {
			return true
		}
	}
	return false
}

func originAllowed(header http.Header) bool {
	var values []string
	for name, candidate := range header {
		if strings.EqualFold(name, "Origin") {
			values = append(values, candidate...)
		}
	}
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	return values[0] == ""
}

var credentialNames = map[string]struct{}{
	"token": {}, "access_token": {}, "authorization": {}, "bearer": {},
}

func hasCredentialInQuery(query *url.URL) bool {
	if query == nil {
		return false
	}
	for name := range query.Query() {
		if _, found := credentialNames[strings.ToLower(name)]; found {
			return true
		}
	}
	return false
}

func hasCredentialCookie(cookies []*http.Cookie) bool {
	// The daemon has no cookie-authenticated browser surface. Rejecting every
	// parsed cookie closes both known bearer names and future cookie credential
	// names without maintaining an allowlist.
	return len(cookies) > 0
}

// RequestPolicyError is useful to adapters that need a stable diagnostic
// without exposing the underlying request. The wrapped error remains one of
// the safe sentinels above.
type RequestPolicyError struct{ cause error }

func (err *RequestPolicyError) Error() string { return "request policy rejected" }
func (err *RequestPolicyError) Unwrap() error { return err.cause }

func authErrorf(cause error) error {
	if cause == nil {
		return nil
	}
	return &RequestPolicyError{cause: cause}
}
