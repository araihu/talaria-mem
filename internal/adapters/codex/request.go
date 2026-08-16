package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const (
	// HookName is the only Codex hook installed by v0.0.1.
	HookName = "SessionStart"

	// MaxRequestBytes bounds the complete hook event. Unknown fields are
	// skipped while decoding; they are never copied into an application value.
	MaxRequestBytes = domain.MaxHTTPRequestBodyBytes
	maxFieldBytes   = 512
)

// Request is the deliberately narrow SessionStart input. Codex may include
// transcript or other future fields in the event; DecodeRequest discards them
// before this value reaches application code.
type Request struct {
	EventID          string `json:"event_id"`
	SessionID        string `json:"session_id"`
	HookName         string `json:"hook_name"`
	WorkingDirectory string `json:"working_directory"`
}

type SessionStartRequest = Request

// ParseRequest decodes one bounded hook event without retaining unknown JSON
// values. It is useful for tests and clients that already own the body bytes.
func ParseRequest(data []byte) (Request, error) {
	return DecodeRequest(bytes.NewReader(data))
}

// DecodeRequest accepts only the four SessionStart fields. It rejects
// duplicate keys, malformed/trailing JSON, oversized input, and invalid
// scalar values. Unknown values are skipped token-by-token, so a transcript
// path or transcript body cannot be opened, logged, persisted, or exposed by
// this adapter.
func DecodeRequest(reader io.Reader) (Request, error) {
	if reader == nil {
		return Request{}, domain.NewError(domain.CodeValidation, "hook input is required", false)
	}
	limited := &io.LimitedReader{R: reader, N: MaxRequestBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.UseNumber()

	token, err := decoder.Token()
	if err != nil {
		return Request{}, domain.NewError(domain.CodeValidation, "invalid hook JSON", false)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return Request{}, domain.NewError(domain.CodeValidation, "hook input must be a JSON object", false)
	}

	var request Request
	seen := make(map[string]struct{}, 4)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return Request{}, domain.NewError(domain.CodeValidation, "invalid hook object", false)
		}
		key, ok := keyToken.(string)
		if !ok {
			return Request{}, domain.NewError(domain.CodeValidation, "invalid hook field", false)
		}
		if _, duplicate := seen[key]; duplicate {
			return Request{}, domain.NewError(domain.CodeValidation, "duplicate hook field", false)
		}
		seen[key] = struct{}{}

		switch key {
		case "event_id":
			if err := decodeString(decoder, &request.EventID); err != nil {
				return Request{}, err
			}
		case "session_id":
			if err := decodeString(decoder, &request.SessionID); err != nil {
				return Request{}, err
			}
		case "hook_name":
			if err := decodeString(decoder, &request.HookName); err != nil {
				return Request{}, err
			}
		case "working_directory":
			if err := decodeString(decoder, &request.WorkingDirectory); err != nil {
				return Request{}, err
			}
		default:
			if err := skipValue(decoder); err != nil {
				return Request{}, domain.NewError(domain.CodeValidation, "invalid unknown hook field", false)
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return Request{}, domain.NewError(domain.CodeValidation, "invalid hook object close", false)
	}
	if limited.N == 0 {
		return Request{}, domain.NewError(domain.CodeValidation, "hook input is too large", false)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil && token != nil {
			return Request{}, domain.NewError(domain.CodeValidation, "trailing hook JSON", false)
		}
		return Request{}, domain.NewError(domain.CodeValidation, "invalid trailing hook JSON", false)
	}
	if err := validateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func decodeString(decoder *json.Decoder, destination *string) error {
	var value string
	if err := decoder.Decode(&value); err != nil {
		return domain.NewError(domain.CodeValidation, "hook field must be a string", false)
	}
	if !utf8.ValidString(value) || len([]byte(value)) > maxFieldBytes || strings.ContainsAny(value, "\x00\r\n") {
		return domain.NewError(domain.CodeValidation, "hook field is invalid", false)
	}
	*destination = value
	return nil
}

func skipValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	var closeDelimiter json.Delim
	switch delimiter {
	case '{':
		closeDelimiter = '}'
	case '[':
		closeDelimiter = ']'
	default:
		return errors.New("invalid JSON delimiter")
	}
	var keys map[string]struct{}
	if delimiter == '{' {
		keys = make(map[string]struct{})
	}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			if _, ok := key.(string); !ok {
				return errors.New("invalid JSON object key")
			}
			keyString := key.(string)
			if _, duplicate := keys[keyString]; duplicate {
				return errors.New("duplicate JSON object key")
			}
			keys[keyString] = struct{}{}
		}
		if err := skipValue(decoder); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != closeDelimiter {
		return errors.New("invalid JSON delimiter close")
	}
	return nil
}

func validateRequest(request Request) error {
	if request.EventID == "" || request.SessionID == "" || request.HookName == "" || request.WorkingDirectory == "" {
		return domain.NewError(domain.CodeValidation, "event, session, hook, and working directory are required", false)
	}
	if request.HookName != HookName {
		return domain.NewError(domain.CodeValidation, "unsupported hook", false)
	}
	if _, err := absoluteWorkingDirectory(request.WorkingDirectory); err != nil {
		return err
	}
	return nil
}
