package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

func validHookJSON() string {
	return `{"session_id":"session-1","cwd":"/tmp/talaria","hook_event_name":"SessionStart"}`
}

func TestDecodeRequestDiscardsUnknownTranscriptAndWorkspaceOverride(t *testing.T) {
	input := `{"session_id":"session-1","cwd":"/tmp/talaria","hook_event_name":"SessionStart","workspace_id":"other","transcript_path":"/definitely/not-opened","transcript":{"content":"ignore me"}}`
	request, err := DecodeRequest(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if request.SessionID != "session-1" || request.HookName != HookName || request.WorkingDirectory != "/tmp/talaria" {
		t.Fatalf("request = %+v", request)
	}
}

func TestDecodeRequestAcceptsOfficialCodexSessionStartPayload(t *testing.T) {
	input := `{"session_id":"codex-session","transcript_path":"/tmp/codex-rollout.jsonl","cwd":"/workspace/repo","hook_event_name":"SessionStart","model":"gpt-5.6","permission_mode":"default","source":"startup"}`
	request, err := DecodeRequest(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if request.SessionID != "codex-session" || request.HookName != HookName || request.WorkingDirectory != "/workspace/repo" {
		t.Fatalf("request = %+v", request)
	}
}

func TestTranscriptCanary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "codex", "transcript-open-attempt.json"))
	if err != nil {
		t.Fatal(err)
	}
	request, err := DecodeRequest(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if request.WorkingDirectory != "/workspace/repo" || request.HookName != HookName {
		t.Fatalf("request = %+v", request)
	}
}

func TestDecodeRequestRejectsDuplicateTrailingAndOversizedInput(t *testing.T) {
	for name, input := range map[string]string{
		"duplicate":        `{"session_id":"one","session_id":"two","cwd":"/tmp","hook_event_name":"SessionStart"}`,
		"nested duplicate": `{"session_id":"session","cwd":"/tmp","hook_event_name":"SessionStart","transcript":{"x":1,"x":2}}`,
		"trailing":         validHookJSON() + ` {}`,
		"array":            `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRequest(strings.NewReader(input)); !domain.IsCode(err, domain.CodeValidation) {
				t.Fatalf("error = %v, want validation", err)
			}
		})
	}
	if _, err := DecodeRequest(strings.NewReader(validHookJSON() + strings.Repeat("x", int(MaxRequestBytes)))); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("oversized error = %v, want validation", err)
	}
}

func TestDecodeRequestRejectsInvalidHookScalars(t *testing.T) {
	for _, input := range []string{
		`{"session_id":"session","hook_event_name":"Other","cwd":"/tmp"}`,
		`{"session_id":"session","hook_event_name":"SessionStart","cwd":"relative"}`,
		`{"session_id":"session","hook_event_name":"SessionStart","cwd":"/tmp","unknown": [}`, // unknown malformed value
	} {
		if _, err := DecodeRequest(strings.NewReader(input)); !domain.IsCode(err, domain.CodeValidation) {
			t.Fatalf("input error = %v, want validation", err)
		}
	}
}
