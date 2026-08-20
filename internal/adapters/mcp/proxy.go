package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

var (
	ErrProxyUnavailable = errors.New("MCP proxy unavailable")
	ErrProxyInput       = errors.New("MCP proxy input invalid")
)

type ProxyConfig struct {
	Endpoint  string
	TokenPath string
	Input     io.Reader
	Output    io.Writer
	Client    *http.Client
}

type Proxy struct {
	endpoint  string
	tokenPath string
	input     io.Reader
	output    io.Writer
	client    *http.Client
	session   string
}

func NewProxy(config ProxyConfig) (*Proxy, error) {
	if err := security.ValidateLoopbackEndpoint(config.Endpoint); err != nil {
		return nil, err
	}
	if config.TokenPath == "" || config.Input == nil || config.Output == nil {
		return nil, ErrProxyInput
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Proxy{endpoint: strings.TrimRight(config.Endpoint, "/"), tokenPath: config.TokenPath, input: config.Input, output: config.Output, client: client}, nil
}

func (proxy *Proxy) Run(ctx context.Context) error {
	if proxy == nil || proxy.client == nil {
		return ErrProxyUnavailable
	}
	token, err := security.LoadBearerToken(proxy.tokenPath)
	if err != nil {
		return ErrProxyUnavailable
	}
	scanner := bufio.NewScanner(proxy.input)
	scanner.Buffer(make([]byte, 4096), domain.MaxHTTPRequestBodyBytes+1)
	for scanner.Scan() {
		frame := append([]byte(nil), scanner.Bytes()...)
		if len(frame) == 0 || len(frame) > domain.MaxHTTPRequestBodyBytes {
			return ErrProxyInput
		}
		if err := proxy.forward(ctx, token, frame); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return ErrProxyInput
	}
	return nil
}

func (proxy *Proxy) forward(parent context.Context, token string, frame []byte) error {
	if err := domain.RejectDuplicateJSONKeys(frame); err != nil {
		return ErrProxyInput
	}
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil || envelope.JSONRPC != "2.0" || envelope.Method == "" {
		return ErrProxyInput
	}
	request, err := http.NewRequestWithContext(parent, http.MethodPost, proxy.endpoint, strings.NewReader(string(frame)))
	if err != nil {
		return ErrProxyUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if proxy.session != "" {
		request.Header.Set("Mcp-Session-Id", proxy.session)
	}
	response, err := proxy.client.Do(request)
	if err != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		return ErrProxyUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ErrProxyUnavailable
	}
	if session := response.Header.Get("Mcp-Session-Id"); session != "" {
		if !safeSession(session) {
			return ErrProxyUnavailable
		}
		proxy.session = session
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, domain.MaxHTTPRequestBodyBytes+1))
	if err != nil || len(body) > domain.MaxHTTPRequestBodyBytes {
		return ErrProxyUnavailable
	}
	defer clearProxyBytes(body)
	if len(body) == 0 {
		return nil
	}
	if err := domain.RejectDuplicateJSONKeys(body); err != nil {
		return ErrProxyUnavailable
	}
	if _, err := proxy.output.Write(append(body, '\n')); err != nil {
		return ErrProxyUnavailable
	}
	return nil
}

func clearProxyBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
