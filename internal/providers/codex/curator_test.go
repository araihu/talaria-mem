package codex

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
)

func TestCuratorRunsEphemeralLunaTurnAndDecodesCandidates(t *testing.T) {
	client, err := startTestClient(t, "valid")
	if err != nil {
		t.Fatal(err)
	}
	curator := NewCurator(client, CuratorConfig{Model: "gpt-5.6-luna", ReasoningEffort: "high"})
	result, err := curator.Curate(context.Background(), curation.CurationRequest{
		WorkspaceID:   "workspace",
		Reason:        curation.ReasonPeriodic,
		Watermark:     10,
		ThreadLocator: []byte(`{"thread_id":"source-thread"}`),
		Snapshot:      []byte("bounded source snapshot"),
	})
	if err != nil {
		t.Fatalf("Curate() error = %v", err)
	}
	if result.Provider != "codex" || result.Model != "gpt-5.6-luna" || len(result.Candidates) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.Candidates[0].Kind != "state" || result.Candidates[0].Title != "Keep this" {
		t.Fatalf("candidate = %#v", result.Candidates[0])
	}
}

func TestCuratorRejectsToolOrApprovalRequestWithoutFallback(t *testing.T) {
	client, err := startTestClient(t, "tool")
	if err != nil {
		t.Fatal(err)
	}
	curator := NewCurator(client, CuratorConfig{Model: "gpt-5.6-luna", ReasoningEffort: "high"})
	_, err = curator.Curate(context.Background(), curation.CurationRequest{ThreadLocator: []byte(`{"thread_id":"source-thread"}`)})
	if !IsClass(err, curation.ErrorSuspiciousContent) {
		t.Fatalf("Curate() error = %v, want suspicious content", err)
	}
}

func TestCuratorRejectsExtraStructuredOutputKeys(t *testing.T) {
	client, err := startTestClient(t, "invalid")
	if err != nil {
		t.Fatal(err)
	}
	curator := NewCurator(client, CuratorConfig{Model: "gpt-5.6-luna", ReasoningEffort: "high"})
	_, err = curator.Curate(context.Background(), curation.CurationRequest{ThreadLocator: []byte(`{"thread_id":"source-thread"}`)})
	if !IsClass(err, curation.ErrorInvalidOutput) {
		t.Fatalf("Curate() error = %v, want invalid output", err)
	}
}

func TestCuratorReadsBoundedThreadSnapshot(t *testing.T) {
	client, err := startTestClient(t, "valid")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.ReadThread(context.Background(), "source-thread")
	if err != nil {
		t.Fatalf("ReadThread() error = %v", err)
	}
	if snapshot.ID != "source-thread" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestDecodeCandidatesRejectsUnsafeStructuredOutput(t *testing.T) {
	for name, text := range map[string]string{
		"extra key":      `{"candidates":[],"trust":"verified"}`,
		"trailing value": `{"candidates":[]} {}`,
		"duplicate key":  `{"candidates":[],"candidates":[]}`,
		"standing":       `{"candidates":[{"kind":"standing_instruction","title":"x","content":"x"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCandidates(text); err == nil {
				t.Fatal("decodeCandidates() error = nil")
			}
		})
	}
}

func startTestClient(t *testing.T, mode string) (*AppClient, error) {
	t.Helper()
	client, err := StartClient(context.Background(), ClientConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestCodexProcessHelper", "--"},
		Env: append(os.Environ(),
			"TALARIA_CODEX_PROCESS_HELPER=1",
			"TALARIA_CODEX_CURATOR_MODE="+mode,
		),
		Model:   "gpt-5.6-luna",
		Timeout: time.Second,
	})
	if err == nil {
		t.Cleanup(func() { _ = client.Close() })
	}
	return client, err
}
