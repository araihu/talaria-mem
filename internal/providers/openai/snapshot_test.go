package openai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type snapshotScanner struct {
	find string
}

func (scanner snapshotScanner) Scan(_ context.Context, fields []ports.TextField) ports.ScanResult {
	result := ports.ScanResult{Status: ports.ScanClean, Generation: "test"}
	for _, field := range fields {
		if scanner.find == "" || !strings.Contains(field.Value, scanner.find) {
			continue
		}
		start := strings.Index(field.Value, scanner.find)
		result.Status = ports.ScanFinding
		result.Findings = append(result.Findings, ports.Finding{Field: field.Name, RuleID: "test-secret", Start: start, End: start + len(scanner.find)})
	}
	return result
}

func TestBuildSnapshotExcludesRawToolInputsAndTranscriptPaths(t *testing.T) {
	snapshot, err := BuildSnapshot(context.Background(), SnapshotInput{
		Turns:          []SnapshotTurn{{Role: "user", Text: "remember the safe detail"}, {Role: "assistant", Text: "done"}},
		Tools:          []SnapshotTool{{Name: "terminal", Status: "failed", ErrorSummary: "bounded error"}},
		TranscriptPath: "/private/transcript.jsonl",
		RawToolInput:   "rm -rf /",
	}, snapshotScanner{})
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if strings.Contains(string(snapshot), "transcript.jsonl") || strings.Contains(string(snapshot), "rm -rf") {
		t.Fatalf("snapshot retained excluded data: %s", snapshot)
	}
	var decoded map[string]any
	if err := json.Unmarshal(snapshot, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["turns"] == nil || decoded["tools"] == nil {
		t.Fatalf("snapshot = %s", snapshot)
	}
}

func TestSnapshotInputCannotMarshalRawHostFields(t *testing.T) {
	data, err := json.Marshal(SnapshotInput{TranscriptPath: "/private/transcript", RawToolInput: "secret-command"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "transcript") || strings.Contains(string(data), "secret-command") {
		t.Fatalf("raw host fields were serialized: %s", data)
	}
}

func TestBuildSnapshotRedactsAndRescansFindings(t *testing.T) {
	snapshot, err := BuildSnapshot(context.Background(), SnapshotInput{
		Turns: []SnapshotTurn{{Role: "assistant", Text: "safe secret-value"}},
	}, snapshotScanner{find: "secret-value"})
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if strings.Contains(string(snapshot), "secret-value") || !strings.Contains(string(snapshot), "[REDACTED]") {
		t.Fatalf("snapshot = %s", snapshot)
	}
}

func TestBuildSnapshotRejectsScannerUncertaintyAndHardLimit(t *testing.T) {
	uncertain := scannerStatus{status: ports.ScanUncertain}
	if _, err := BuildSnapshot(context.Background(), SnapshotInput{Turns: []SnapshotTurn{{Role: "user", Text: "value"}}}, uncertain); err == nil {
		t.Fatal("BuildSnapshot() error = nil for scanner uncertainty")
	}
	large := SnapshotInput{Turns: []SnapshotTurn{{Role: "user", Text: strings.Repeat("x", maxSnapshotBytes)}}}
	if _, err := BuildSnapshot(context.Background(), large, snapshotScanner{}); err == nil {
		t.Fatal("BuildSnapshot() error = nil for oversized snapshot")
	}
}

type scannerStatus struct{ status ports.ScanStatus }

func (scanner scannerStatus) Scan(context.Context, []ports.TextField) ports.ScanResult {
	return ports.ScanResult{Status: scanner.status, Generation: "test"}
}
