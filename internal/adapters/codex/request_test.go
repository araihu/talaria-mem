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
	return `{"event_id":"event-1","session_id":"session-1","hook_name":"SessionStart","working_directory":"/tmp/talaria"}`
}

func TestDecodeRequestDiscardsUnknownTranscriptAndWorkspaceOverride(t *testing.T) {
	input := `{"event_id":"event-1","session_id":"session-1","hook_name":"SessionStart","working_directory":"/tmp/talaria","workspace_id":"other","transcript_path":"/definitely/not-opened","transcript":{"content":"ignore me"}}`
	request, err := DecodeRequest(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if request.EventID != "event-1" || request.SessionID != "session-1" || request.HookName != HookName || request.WorkingDirectory != "/tmp/talaria" {
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
		"duplicate":        `{"event_id":"one","event_id":"two","session_id":"s","hook_name":"SessionStart","working_directory":"/tmp"}`,
		"nested duplicate": `{"event_id":"event","session_id":"session","hook_name":"SessionStart","working_directory":"/tmp","transcript":{"x":1,"x":2}}`,
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
		`{"event_id":"event","session_id":"session","hook_name":"Other","working_directory":"/tmp"}`,
		`{"event_id":"event","session_id":"session","hook_name":"SessionStart","working_directory":"relative"}`,
		`{"event_id":"event","session_id":"session","hook_name":"SessionStart","working_directory":"/tmp","unknown": [}`, // unknown malformed value
	} {
		if _, err := DecodeRequest(strings.NewReader(input)); !domain.IsCode(err, domain.CodeValidation) {
			t.Fatalf("input error = %v, want validation", err)
		}
	}
}
