package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/providers/codex/protocol"
	providerconfig "github.com/guilhermecastro/talaria-mem/internal/providers/config"
)

type cleanRuntimeScanner struct{}

func (cleanRuntimeScanner) Scan(context.Context, []ports.TextField) ports.ScanResult {
	return ports.ScanResult{Status: ports.ScanClean}
}

func TestRuntimeSnapshotSourceMinimizesCodexThread(t *testing.T) {
	source := newRuntimeSnapshotSource(providerConfigForTest(), cleanRuntimeScanner{})
	snapshot, err := source.snapshotFromThread(context.Background(), protocol.ThreadSnapshot{
		ID: "thread-1",
		Turns: []protocol.Turn{{ID: "turn-1", Items: []protocol.ThreadItem{
			{Type: "user_message", Text: "remember this decision"},
			{Type: "shell_command", Name: "sh", Content: "do-not-persist-command", Status: "completed"},
		}}},
	}, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot.ThreadLocator) != `{"thread_id":"thread-1"}` {
		t.Fatalf("locator=%s", snapshot.ThreadLocator)
	}
	if strings.Contains(string(snapshot.Snapshot), "do-not-persist-command") || !strings.Contains(string(snapshot.Snapshot), "remember this decision") || !strings.Contains(string(snapshot.Snapshot), `"name":"sh"`) {
		t.Fatalf("snapshot was not minimized: %s", snapshot.Snapshot)
	}
}

func providerConfigForTest() providerconfig.Provider {
	return providerconfig.Provider{Command: "codex", Model: "gpt-5.6-luna", ReasoningEffort: "high", Timeout: time.Second}
}
