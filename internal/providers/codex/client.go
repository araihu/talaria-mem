package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/providers/codex/protocol"
)

type ClientConfig struct {
	Command         string
	Args            []string
	Env             []string
	Dir             string
	Model           string
	ReasoningEffort string
	Timeout         time.Duration
}

type ProtocolEvent struct {
	Method    string
	Params    json.RawMessage
	IsRequest bool
}

type AppClient struct {
	process *Process
	client  *protocol.Client
	model   string
	events  chan ProtocolEvent
}

func StartClient(parent context.Context, configuration ClientConfig) (*AppClient, error) {
	if parent == nil {
		parent = context.Background()
	}
	if configuration.Model == "" {
		configuration.Model = "gpt-5.6-luna"
	}
	if configuration.ReasoningEffort == "" {
		configuration.ReasoningEffort = "high"
	}
	if configuration.Timeout <= 0 {
		configuration.Timeout = 90 * time.Second
	}
	args := appServerArgs(configuration.Args)
	events := make(chan ProtocolEvent, 32)
	process, err := StartProcess(parent, ProcessConfig{
		Command: configuration.Command,
		Args:    args,
		Env:     configuration.Env,
		Dir:     configuration.Dir,
		Timeout: configuration.Timeout,
		Handler: appServerHandler{events: events},
	})
	if err != nil {
		return nil, err
	}
	client := &AppClient{process: process, client: protocol.NewClient(process.Connection()), model: configuration.Model, events: events}
	requestContext, cancel := context.WithTimeout(parent, configuration.Timeout)
	defer cancel()
	if _, err := client.client.Initialize(requestContext, protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "talaria-mem", Version: stringPtr("v1")}}); err != nil {
		_ = client.Close()
		return nil, classifyHandshakeError(err)
	}
	models, err := client.client.ModelList(requestContext, protocol.ModelListParams{IncludeHidden: true})
	if err != nil {
		_ = client.Close()
		return nil, classifyHandshakeError(err)
	}
	found := false
	for _, model := range models.Data {
		if model.ID == configuration.Model || model.Model == configuration.Model {
			found = true
			break
		}
	}
	if !found {
		_ = client.Close()
		return nil, curation.NewProviderError(curation.ErrorUnavailable, fmt.Errorf("configured model %q is unavailable", configuration.Model))
	}
	return client, nil
}

// appServerArgs keeps provider configuration concise: a configured Codex
// executable means the normal `codex app-server --stdio` process. Tests and
// wrapper executables may still provide explicit arguments.
func appServerArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"app-server", "--stdio"}
	}
	return append([]string(nil), args...)
}

func (client *AppClient) Model() string {
	if client == nil {
		return ""
	}
	return client.model
}

func (client *AppClient) ReadThread(ctx context.Context, threadID string) (protocol.ThreadSnapshot, error) {
	if client == nil || client.client == nil {
		return protocol.ThreadSnapshot{}, curation.NewProviderError(curation.ErrorUnavailable, errors.New("Codex client unavailable"))
	}
	response, err := client.client.ThreadRead(ctx, protocol.ThreadReadParams{ThreadID: threadID, IncludeTurns: true})
	if err != nil {
		return protocol.ThreadSnapshot{}, classifyProviderCallError(err)
	}
	return response.Thread, nil
}

func (client *AppClient) Events() <-chan ProtocolEvent {
	if client == nil {
		return nil
	}
	return client.events
}

func (client *AppClient) Close() error {
	if client == nil || client.process == nil {
		return nil
	}
	return client.process.Close()
}

func classifyHandshakeError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return curation.NewProviderError(curation.ErrorTimeout, err)
	}
	return curation.NewProviderError(curation.ErrorUnavailable, err)
}

func classifyProviderCallError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return curation.NewProviderError(curation.ErrorTimeout, err)
	}
	return curation.NewProviderError(curation.ErrorUnavailable, err)
}

func stringPtr(value string) *string { return &value }

type appServerHandler struct {
	events chan<- ProtocolEvent
}

const maxCuratorEventBytes = 128 << 10

func (handler appServerHandler) Handle(ctx context.Context, connection *jsonrpc2.Conn, request *jsonrpc2.Request) {
	if request == nil {
		return
	}
	if request.Notif && request.Method != protocol.NotificationAgentMessageDone && request.Method != protocol.NotificationTurnCompleted {
		if request.Method == protocol.NotificationError {
			select {
			case handler.events <- ProtocolEvent{Method: request.Method}:
			default:
			}
		}
		return
	}
	var params json.RawMessage
	if request.Notif && request.Params != nil && len(*request.Params) <= maxCuratorEventBytes {
		params = append([]byte(nil), (*request.Params)...)
	}
	select {
	case handler.events <- ProtocolEvent{Method: request.Method, Params: params, IsRequest: !request.Notif}:
	default:
	}
	if request.Notif {
		return
	}
	_ = connection.ReplyWithError(ctx, request.ID, &jsonrpc2.Error{Code: -32001, Message: "Codex tool and approval requests are disabled for curation"})
}
