package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	httpadapter "github.com/guilhermecastro/talaria-mem/internal/adapters/http"
	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type AuthenticationError struct{}

func (AuthenticationError) Error() string { return "authentication required" }

// HTTPClient is the ordinary CLI transport. It accepts only a literal
// loopback endpoint, rejects redirects, and never opens SQLite.
type HTTPClient struct {
	Endpoint string
	Token    string
	Client   *http.Client
}

func NewHTTPClient(endpoint, token string) (*HTTPClient, error) {
	if err := security.ValidateLoopbackEndpoint(endpoint); err != nil {
		return nil, err
	}
	if !security.ValidateBearerTokenSyntax(token) {
		return nil, security.ErrTokenInvalid
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || transport == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "control transport unavailable", true)
	}
	transport = transport.Clone()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return &HTTPClient{Endpoint: strings.TrimRight(endpoint, "/"), Token: token, Client: client}, nil
}

func (client *HTTPClient) Create(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	var result application.MutationResult
	err := client.do(ctx, http.MethodPost, "/control/v1/memory", request, &result)
	return result, err
}
func (client *HTTPClient) Update(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	var result application.MutationResult
	err := client.do(ctx, http.MethodPut, "/control/v1/memory/"+url.PathEscape(request.MemoryID), request, &result)
	return result, err
}
func (client *HTTPClient) Confirm(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	var result application.MutationResult
	err := client.do(ctx, http.MethodPost, "/control/v1/memory/"+url.PathEscape(request.MemoryID)+"/confirm", request, &result)
	return result, err
}
func (client *HTTPClient) Pin(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	var result application.MutationResult
	err := client.do(ctx, http.MethodPost, "/control/v1/memory/"+url.PathEscape(request.MemoryID)+"/pin", request, &result)
	return result, err
}
func (client *HTTPClient) Forget(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	var result application.MutationResult
	err := client.do(ctx, http.MethodPost, "/control/v1/memory/"+url.PathEscape(request.MemoryID)+"/forget", request, &result)
	return result, err
}
func (client *HTTPClient) Restore(ctx context.Context, request application.MutationRequest) (application.MutationResult, error) {
	var result application.MutationResult
	err := client.do(ctx, http.MethodPost, "/control/v1/memory/"+url.PathEscape(request.MemoryID)+"/restore", request, &result)
	return result, err
}
func (client *HTTPClient) Review(ctx context.Context, memoryID, workspaceID string) (application.ReviewResult, error) {
	var result application.ReviewResult
	err := client.do(ctx, http.MethodGet, "/control/v1/memory/"+url.PathEscape(memoryID)+"/review?workspace_id="+url.QueryEscape(workspaceID), nil, &result)
	return result, err
}
func (client *HTTPClient) Explain(ctx context.Context, memoryID string) (application.Explanation, error) {
	var result application.Explanation
	err := client.do(ctx, http.MethodGet, "/control/v1/memory/"+url.PathEscape(memoryID)+"/explain", nil, &result)
	return result, err
}
func (client *HTTPClient) Search(ctx context.Context, request retrieval.SearchRequest) (retrieval.SearchResult, error) {
	var wire httpadapter.MemorySearchResponse
	limit := request.Limit
	var limitPtr *int
	if limit > 0 {
		limitPtr = &limit
	}
	err := client.do(ctx, http.MethodPost, "/control/v1/memory/search", httpadapter.MemorySearchRequest{Query: request.Query, WorkspaceId: request.WorkspaceID, SessionId: request.ConsumerSession, Limit: limitPtr}, &wire)
	if err != nil {
		return retrieval.SearchResult{}, err
	}
	items := make([]retrieval.SearchItem, 0, len(wire.Items))
	for _, item := range wire.Items {
		items = append(items, itemFromWire(item))
	}
	return retrieval.SearchResult{Items: items, Included: wire.Included, Omitted: wire.Omitted}, nil
}
func (client *HTTPClient) Get(ctx context.Context, memoryID, workspaceID, session string, _ ports.FieldIdentifier) (retrieval.SearchItem, error) {
	var wire httpadapter.MemoryItem
	path := "/control/v1/memory/" + url.PathEscape(memoryID) + "?workspace_id=" + url.QueryEscape(workspaceID)
	request, err := client.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return retrieval.SearchItem{}, err
	}
	request.Header.Set("Mcp-Session-Id", session)
	if err := client.doRequest(request, &wire); err != nil {
		return retrieval.SearchItem{}, err
	}
	return itemFromWire(wire), nil
}

func itemFromWire(item httpadapter.MemoryItem) retrieval.SearchItem {
	var resolutionState domain.ResolutionState
	if item.ResolutionState != nil {
		resolutionState = domain.ResolutionState(*item.ResolutionState)
	}
	var score float64
	if item.Score != nil {
		score = float64(*item.Score)
	}
	return retrieval.SearchItem{MemoryID: item.MemoryId.String(), RevisionID: item.RevisionId.String(), WorkspaceID: item.WorkspaceId, Kind: domain.MemoryKind(item.Kind), Title: item.Title, Content: item.Content, Tags: append([]string(nil), item.Tags...), ResolutionState: resolutionState, Score: retrieval.Score{FinalScore: score}}
}

func (client *HTTPClient) do(ctx context.Context, method, path string, input, output any) error {
	request, err := client.newRequest(ctx, method, path, input)
	if err != nil {
		return err
	}
	return client.doRequest(request, output)
}

func (client *HTTPClient) newRequest(ctx context.Context, method, path string, input any) (*http.Request, error) {
	if client == nil || client.Client == nil {
		return nil, errCommandUnavailable
	}
	if err := security.ValidateLoopbackEndpoint(client.Endpoint); err != nil {
		return nil, err
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, domain.NewError(domain.CodeValidation, "invalid request", false)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.Endpoint+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func (client *HTTPClient) doRequest(request *http.Request, output any) error {
	response, err := client.Client.Do(request)
	if err != nil {
		return domain.NewError(domain.CodeUnavailable, "control service unavailable", true)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, domain.MaxHTTPRequestBodyBytes+1))
	if err != nil || int64(len(data)) > domain.MaxHTTPRequestBodyBytes {
		return domain.NewError(domain.CodeUnavailable, "control response unavailable", true)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return AuthenticationError{}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope httpadapter.ErrorEnvelope
		if json.Unmarshal(data, &envelope) == nil {
			return wireError(envelope)
		}
		return domain.NewError(domain.CodeUnavailable, "control request failed", response.StatusCode >= 500)
	}
	if output == nil {
		return nil
	}
	if err := domain.RejectDuplicateJSONKeys(data); err != nil {
		return domain.NewError(domain.CodeUnavailable, "invalid control response", true)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return domain.NewError(domain.CodeUnavailable, "invalid control response", true)
	}
	return nil
}

func wireError(envelope httpadapter.ErrorEnvelope) error {
	code := domain.ErrorCode(envelope.Code)
	if code == "" {
		code = domain.CodeUnavailable
	}
	if code == domain.CodeUnavailable && envelope.Message == "authentication required" {
		return AuthenticationError{}
	}
	return domain.NewError(code, safeWireError(code), envelope.Retryable)
}

func safeWireError(code domain.ErrorCode) string {
	switch code {
	case domain.CodeValidation:
		return "invalid request"
	case domain.CodeNotFound:
		return "not found"
	case domain.CodeRevisionConflict, domain.CodeIdempotencyConflict:
		return "conflict"
	case domain.CodeSecretRefusal:
		return "content refused"
	case domain.CodeQuarantine:
		return "content quarantined"
	default:
		return "control service unavailable"
	}
}

var _ MemoryClient = (*HTTPClient)(nil)
