package codex

import (
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

func TestParseHookEventKeepsOnlyBoundedFields(t *testing.T) {
	event, err := ParseHookEvent([]byte(`{"session_id":"s","cwd":"/workspace","hook_event_name":"UserPromptSubmit","current_prompt":"remember this","source_watermark":9,"transcript_path":"/must/not/open","transcript":{"secret":"discard"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.SessionID != "s" || event.WorkingDirectory != "/workspace" || event.HookName != HookUserPromptSubmit || event.CurrentPrompt != "remember this" || event.SourceWatermark != 9 {
		t.Fatalf("event = %+v", event)
	}
}

func TestParseHookEventAcceptsExactNamesAndRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{HookName, HookUserPromptSubmit, HookPreCompact, HookSessionEnd} {
		if _, err := ParseHookEvent([]byte(`{"session_id":"s","cwd":"/tmp","hook_event_name":"` + name + `"}`)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := ParseHookEvent([]byte(`{"session_id":"s","cwd":"/tmp","hook_event_name":"Other"}`)); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("unknown event error = %v", err)
	}
}

func TestParseHookEventRejectsDuplicateOversizedAndTrailingInput(t *testing.T) {
	for _, input := range []string{
		`{"session_id":"s","session_id":"other","cwd":"/tmp","hook_event_name":"SessionEnd"}`,
		`{"session_id":"s","cwd":"/tmp","hook_event_name":"SessionEnd","unknown":{"x":1,"x":2}}`,
		`{"session_id":"s","cwd":"/tmp","hook_event_name":"SessionEnd"}{}`,
		`{"session_id":"s","cwd":"/tmp","hook_event_name":"UserPromptSubmit","prompt":"one","current_prompt":"two"}`,
	} {
		if _, err := ParseHookEvent([]byte(input)); !domain.IsCode(err, domain.CodeValidation) {
			t.Fatalf("input %q error = %v", input, err)
		}
	}
	oversized := `{"session_id":"s","cwd":"/tmp","hook_event_name":"UserPromptSubmit","current_prompt":"` + strings.Repeat("x", MaxHookPromptBytes+1) + `"}`
	if _, err := ParseHookEvent([]byte(oversized)); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("oversized prompt error = %v", err)
	}
}

func TestParseHookEventDoesNotRetainTranscriptPath(t *testing.T) {
	event, err := ParseHookEvent([]byte(`{"session_id":"s","cwd":"/tmp","hook_event_name":"PreCompact","transcript_path":"TRANSCRIPT_PATH_CANARY"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(event.SessionID+event.WorkingDirectory+event.CurrentPrompt, "TRANSCRIPT_PATH_CANARY") {
		t.Fatal("transcript path retained")
	}
}
