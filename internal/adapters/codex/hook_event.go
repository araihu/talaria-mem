package codex

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const (
	HookUserPromptSubmit = "UserPromptSubmit"
	HookPreCompact       = "PreCompact"
	HookSessionEnd       = "SessionEnd"
	MaxHookPromptBytes   = 32 * 1024
)

// HookEvent is the minimal retained representation of a Codex hook event.
// Transcript paths, tool payloads, and all unknown fields are deliberately
// absent from this type.
type HookEvent struct {
	SessionID        string `json:"session_id"`
	HookName         string `json:"hook_event_name"`
	WorkingDirectory string `json:"cwd"`
	CurrentPrompt    string `json:"current_prompt,omitempty"`
	SourceWatermark  int64  `json:"source_watermark,omitempty"`
}

func (event HookEvent) Valid() bool {
	if event.SessionID == "" || event.WorkingDirectory == "" || event.SourceWatermark < 0 {
		return false
	}
	switch event.HookName {
	case HookName, HookUserPromptSubmit, HookPreCompact, HookSessionEnd:
		return true
	default:
		return false
	}
}

func ParseHookEvent(data []byte) (HookEvent, error) {
	return DecodeHookEvent(bytes.NewReader(data))
}

func DecodeHookEvent(reader io.Reader) (HookEvent, error) {
	if reader == nil {
		return HookEvent{}, domain.NewError(domain.CodeValidation, "hook input is required", false)
	}
	limited := &io.LimitedReader{R: reader, N: MaxRequestBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return HookEvent{}, domain.NewError(domain.CodeValidation, "invalid hook JSON", false)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return HookEvent{}, domain.NewError(domain.CodeValidation, "hook input must be a JSON object", false)
	}

	var event HookEvent
	seen := make(map[string]struct{}, 5)
	promptSeen := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return HookEvent{}, domain.NewError(domain.CodeValidation, "invalid hook object", false)
		}
		key, ok := keyToken.(string)
		if !ok {
			return HookEvent{}, domain.NewError(domain.CodeValidation, "invalid hook field", false)
		}
		if _, duplicate := seen[key]; duplicate {
			return HookEvent{}, domain.NewError(domain.CodeValidation, "duplicate hook field", false)
		}
		seen[key] = struct{}{}
		switch key {
		case "session_id":
			if err := decodeHookString(decoder, &event.SessionID, 256, false); err != nil {
				return HookEvent{}, err
			}
		case "hook_event_name":
			if err := decodeHookString(decoder, &event.HookName, 64, false); err != nil {
				return HookEvent{}, err
			}
		case "cwd":
			if err := decodeHookString(decoder, &event.WorkingDirectory, 4096, false); err != nil {
				return HookEvent{}, err
			}
		case "current_prompt", "prompt":
			if promptSeen {
				return HookEvent{}, domain.NewError(domain.CodeValidation, "duplicate hook prompt field", false)
			}
			promptSeen = true
			if err := decodeHookString(decoder, &event.CurrentPrompt, MaxHookPromptBytes, true); err != nil {
				return HookEvent{}, err
			}
		case "source_watermark":
			if err := decoder.Decode(&event.SourceWatermark); err != nil || event.SourceWatermark < 0 {
				return HookEvent{}, domain.NewError(domain.CodeValidation, "source watermark is invalid", false)
			}
		default:
			if err := skipValue(decoder); err != nil {
				return HookEvent{}, domain.NewError(domain.CodeValidation, "invalid unknown hook field", false)
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return HookEvent{}, domain.NewError(domain.CodeValidation, "invalid hook object close", false)
	}
	if limited.N == 0 {
		return HookEvent{}, domain.NewError(domain.CodeValidation, "hook input is too large", false)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil && token != nil {
			return HookEvent{}, domain.NewError(domain.CodeValidation, "trailing hook JSON", false)
		}
		return HookEvent{}, domain.NewError(domain.CodeValidation, "invalid trailing hook JSON", false)
	}
	if err := validateHookEvent(event); err != nil {
		return HookEvent{}, err
	}
	return event, nil
}

func decodeHookString(decoder *json.Decoder, destination *string, maxBytes int, allowNewlines bool) error {
	var value string
	if err := decoder.Decode(&value); err != nil {
		return domain.NewError(domain.CodeValidation, "hook field must be a string", false)
	}
	if !utf8.ValidString(value) || len([]byte(value)) > maxBytes || strings.ContainsRune(value, '\x00') || (!allowNewlines && strings.ContainsAny(value, "\r\n")) {
		return domain.NewError(domain.CodeValidation, "hook field is invalid", false)
	}
	*destination = value
	return nil
}

func validateHookEvent(event HookEvent) error {
	if !event.Valid() {
		return domain.NewError(domain.CodeValidation, "hook event is invalid", false)
	}
	if _, err := absoluteWorkingDirectory(event.WorkingDirectory); err != nil {
		return err
	}
	return nil
}
