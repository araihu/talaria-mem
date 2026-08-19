package codex

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/providers/codex/protocol"
)

type notificationCollector struct {
	ch chan protocol.Notification
}

func (collector notificationCollector) Handle(_ context.Context, _ *jsonrpc2.Conn, request *jsonrpc2.Request) {
	if request == nil || !request.Notif || request.Params == nil {
		return
	}
	collector.ch <- protocol.Notification{Method: request.Method, Params: *request.Params}
}

func TestClientCallsGeneratedProtocolMethods(t *testing.T) {
	process, err := StartProcess(context.Background(), ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env:     append(os.Environ(), "TALARIA_CODEX_PROCESS_HELPER=1"),
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("StartProcess() error = %v", err)
	}
	defer process.Close()
	client := protocol.NewClient(process.Connection())
	response, err := client.Initialize(context.Background(), protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "talaria-mem"}})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if response.UserAgent != "test" {
		t.Fatalf("Initialize() response = %#v", response)
	}
	if _, err := client.ModelList(context.Background(), protocol.ModelListParams{}); err != nil {
		t.Fatalf("ModelList() error = %v", err)
	}
}

func TestProcessDeliversNotificationsToHandler(t *testing.T) {
	notifications := make(chan protocol.Notification, 1)
	process, err := StartProcess(context.Background(), ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env:     append(os.Environ(), "TALARIA_CODEX_PROCESS_HELPER=1", "TALARIA_CODEX_PROCESS_NOTIFY=1"),
		Handler: notificationCollector{ch: notifications},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("StartProcess() error = %v", err)
	}
	defer process.Close()
	client := protocol.NewClient(process.Connection())
	if _, err := client.Initialize(context.Background(), protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "talaria-mem"}}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	select {
	case notification := <-notifications:
		if notification.Method != protocol.NotificationTurnStarted {
			t.Fatalf("notification method = %q", notification.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("notification not delivered")
	}
}

func TestProcessRejectsInboundRequests(t *testing.T) {
	process, err := StartProcess(context.Background(), ProcessConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env:     append(os.Environ(), "TALARIA_CODEX_PROCESS_HELPER=1", "TALARIA_CODEX_PROCESS_REQUEST=1"),
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("StartProcess() error = %v", err)
	}
	defer process.Close()
	client := protocol.NewClient(process.Connection())
	if _, err := client.Initialize(context.Background(), protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "talaria-mem"}}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if process.Err() != nil {
		t.Fatalf("process.Err() = %v", process.Err())
	}
}

func TestStartClientPerformsHandshakeAndRequiresConfiguredModel(t *testing.T) {
	client, err := StartClient(context.Background(), ClientConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env:     append(os.Environ(), "TALARIA_CODEX_PROCESS_HELPER=1"),
		Model:   "gpt-5.6-luna",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("StartClient() error = %v", err)
	}
	defer client.Close()
	if client.Model() != "gpt-5.6-luna" {
		t.Fatalf("Model() = %q", client.Model())
	}
}

func TestStartClientRejectsMissingConfiguredModel(t *testing.T) {
	_, err := StartClient(context.Background(), ClientConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env:     append(os.Environ(), "TALARIA_CODEX_PROCESS_HELPER=1"),
		Model:   "missing-model",
		Timeout: time.Second,
	})
	if !IsClass(err, ErrorUnavailable) {
		t.Fatalf("StartClient() error = %v, want unavailable", err)
	}
}

func TestStartClientMapsHandshakeTimeout(t *testing.T) {
	_, err := StartClient(context.Background(), ClientConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env:     append(os.Environ(), "TALARIA_CODEX_PROCESS_HELPER=1", "TALARIA_CODEX_PROCESS_HOLD=1"),
		Model:   "gpt-5.6-luna",
		Timeout: 20 * time.Millisecond,
	})
	if !IsClass(err, curation.ErrorTimeout) {
		t.Fatalf("StartClient() error = %v, want timeout", err)
	}
}
