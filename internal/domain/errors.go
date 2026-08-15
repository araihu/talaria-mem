package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type ErrorCode string

const (
	CodeValidation          ErrorCode = "validation"
	CodeNotFound            ErrorCode = "not-found"
	CodeRevisionConflict    ErrorCode = "revision-conflict"
	CodeIdempotencyConflict ErrorCode = "idempotency-conflict"
	CodeSecretRefusal       ErrorCode = "secret-refusal"
	CodeQuarantine          ErrorCode = "quarantine"
	CodeTimeout             ErrorCode = "timeout"
	CodeUnavailable         ErrorCode = "unavailable"
	CodeStorageFull         ErrorCode = "storage-full"
	CodeMaintenanceLock     ErrorCode = "maintenance-lock"
)

type Error struct {
	code      ErrorCode
	message   string
	retryable bool
}

func NewError(code ErrorCode, message string, retryable bool) *Error {
	return &Error{code: code, message: message, retryable: retryable}
}

func (err *Error) Error() string   { return err.message }
func (err *Error) Code() ErrorCode { return err.code }
func (err *Error) Retryable() bool { return err.retryable }

func CodeOf(err error) ErrorCode {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code()
	}
	return ""
}

func IsCode(err error, code ErrorCode) bool { return CodeOf(err) == code }

func IsRetryable(err error) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Retryable()
}

type sqliteCoder interface {
	Code() int
}

const sqliteFull = 13

func MapSQLiteError(err error) error {
	var coded sqliteCoder
	if errors.As(err, &coded) && coded.Code()&0xff == sqliteFull {
		return NewError(CodeStorageFull, "storage capacity exhausted", false)
	}
	return err
}

func RejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return NewError(CodeValidation, "invalid JSON", false)
	}
	if err := checkJSONValue(decoder, token); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return NewError(CodeValidation, "invalid trailing JSON", false)
	}
	return nil
}

func checkJSONValue(decoder *json.Decoder, token json.Token) error {
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return NewError(CodeValidation, "invalid JSON object", false)
			}
			key, ok := keyToken.(string)
			if !ok {
				return NewError(CodeValidation, "invalid JSON object key", false)
			}
			if _, exists := keys[key]; exists {
				return NewError(CodeValidation, fmt.Sprintf("duplicate JSON key %q", key), false)
			}
			keys[key] = struct{}{}
			valueToken, err := decoder.Token()
			if err != nil {
				return NewError(CodeValidation, "invalid JSON value", false)
			}
			if err := checkJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return NewError(CodeValidation, "invalid JSON array", false)
			}
			if err := checkJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
	default:
		return NewError(CodeValidation, "invalid JSON delimiter", false)
	}
	if _, err := decoder.Token(); err != nil {
		return NewError(CodeValidation, "invalid JSON close delimiter", false)
	}
	return nil
}
