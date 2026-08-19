package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

const maxSnapshotBytes = 128 << 10

type SnapshotTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type SnapshotTool struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	ErrorSummary string `json:"error,omitempty"`
}

type SnapshotInput struct {
	Turns []SnapshotTurn
	Tools []SnapshotTool

	// These fields are intentionally not serialized. They make the boundary
	// explicit for adapters that still receive a complete host event.
	TranscriptPath string
	RawToolInput   string
}

type snapshotWire struct {
	Turns []SnapshotTurn `json:"turns"`
	Tools []SnapshotTool `json:"tools,omitempty"`
}

func BuildSnapshot(ctx context.Context, input SnapshotInput, scanner ports.Scanner) ([]byte, error) {
	if scanner == nil {
		return nil, errors.New("snapshot scanner unavailable")
	}
	wire := snapshotWire{Turns: make([]SnapshotTurn, 0, len(input.Turns)), Tools: make([]SnapshotTool, 0, len(input.Tools))}
	for _, turn := range input.Turns {
		if strings.TrimSpace(turn.Role) == "" {
			return nil, errors.New("snapshot turn role is required")
		}
		text, err := sanitizeField(ctx, scanner, ports.FieldContent, turn.Text)
		if err != nil {
			return nil, err
		}
		wire.Turns = append(wire.Turns, SnapshotTurn{Role: turn.Role, Text: text})
	}
	for _, tool := range input.Tools {
		name, err := sanitizeField(ctx, scanner, ports.FieldContent, tool.Name)
		if err != nil {
			return nil, err
		}
		status, err := sanitizeField(ctx, scanner, ports.FieldContent, tool.Status)
		if err != nil {
			return nil, err
		}
		errorSummary, err := sanitizeField(ctx, scanner, ports.FieldError, tool.ErrorSummary)
		if err != nil {
			return nil, err
		}
		wire.Tools = append(wire.Tools, SnapshotTool{Name: name, Status: status, ErrorSummary: errorSummary})
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("marshal sanitized snapshot: %w", err)
	}
	if len(data) > maxSnapshotBytes {
		return nil, fmt.Errorf("sanitized snapshot exceeds %d bytes", maxSnapshotBytes)
	}
	return data, nil
}

func sanitizeField(ctx context.Context, scanner ports.Scanner, field ports.FieldIdentifier, value string) (string, error) {
	value = strings.ToValidUTF8(value, "�")
	result := scanner.Scan(ctx, []ports.TextField{{Name: field, Value: value}})
	switch result.Status {
	case ports.ScanClean:
		return value, nil
	case ports.ScanFinding:
		redacted, err := redactFindings(value, result.Findings, field)
		if err != nil {
			return "", err
		}
		rescanned := scanner.Scan(ctx, []ports.TextField{{Name: field, Value: redacted}})
		if rescanned.Status != ports.ScanClean {
			return "", fmt.Errorf("snapshot scanner refused redacted field")
		}
		return redacted, nil
	default:
		return "", fmt.Errorf("snapshot scanner unavailable: %s", result.Status)
	}
}

func redactFindings(value string, findings []ports.Finding, field ports.FieldIdentifier) (string, error) {
	for index := len(findings) - 1; index >= 0; index-- {
		finding := findings[index]
		if finding.Field != field || finding.Start < 0 || finding.End < finding.Start || finding.Start > len(value) {
			return "", errors.New("snapshot scanner returned invalid finding")
		}
		end := finding.End
		if end > len(value) {
			return "", errors.New("snapshot scanner returned out-of-bounds finding")
		}
		value = value[:finding.Start] + "[REDACTED]" + value[end:]
	}
	return value, nil
}
