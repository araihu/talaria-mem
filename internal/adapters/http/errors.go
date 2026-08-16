package httpadapter

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

const errorEnvelopeVersion = "talaria.error.v1"

// RequestError is the small HTTP-specific error seam used by handlers. It
// keeps safe status mapping separate from domain content and backend errors.
type RequestError struct {
	Status    int
	Code      domain.ErrorCode
	Message   string
	Retryable bool
}

func (err *RequestError) Error() string {
	if err == nil {
		return ""
	}
	return err.Message
}

func (err *RequestError) Unwrap() error { return nil }

func writeError(writer http.ResponseWriter, err error) {
	if writer == nil {
		return
	}
	requestErr := classifyError(err)
	receipt, err := uuid.Parse(receiptID(time.Now().UTC()))
	if err != nil {
		receipt = uuid.Nil
	}
	envelope := ErrorEnvelope{
		Version:   ErrorEnvelopeVersion(errorEnvelopeVersion),
		Code:      string(requestErr.Code),
		Message:   requestErr.Message,
		ReceiptId: receipt,
		Retryable: requestErr.Retryable,
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(requestErr.Status)
	_ = json.NewEncoder(writer).Encode(envelope)
}

func classifyError(err error) *RequestError {
	if err == nil {
		return &RequestError{Status: http.StatusInternalServerError, Code: domain.CodeUnavailable, Message: "internal error", Retryable: true}
	}
	if typed := new(RequestError); errors.As(err, &typed) {
		return typed
	}
	if errors.Is(err, security.ErrAuthRequired) || errors.Is(err, security.ErrTokenInvalid) {
		return &RequestError{Status: http.StatusUnauthorized, Code: domain.CodeUnavailable, Message: "authentication required", Retryable: false}
	}
	if errors.Is(err, security.ErrAuthMethod) {
		return &RequestError{Status: http.StatusMethodNotAllowed, Code: domain.CodeValidation, Message: "method not allowed", Retryable: false}
	}
	if errors.Is(err, security.ErrAuthContentType) {
		return &RequestError{Status: http.StatusUnsupportedMediaType, Code: domain.CodeValidation, Message: "JSON content type required", Retryable: false}
	}
	if errors.Is(err, security.ErrAuthBodyTooLarge) {
		return &RequestError{Status: http.StatusRequestEntityTooLarge, Code: domain.CodeValidation, Message: "request body too large", Retryable: false}
	}
	if errors.Is(err, security.ErrAuthUnavailable) {
		return &RequestError{Status: http.StatusServiceUnavailable, Code: domain.CodeUnavailable, Message: "service unavailable", Retryable: true}
	}
	if errors.Is(err, security.ErrAuthTimeout) {
		return &RequestError{Status: http.StatusGatewayTimeout, Code: domain.CodeTimeout, Message: "request timed out", Retryable: true}
	}
	if errors.Is(err, security.ErrAuthLoopback) || errors.Is(err, security.ErrAuthHost) || errors.Is(err, security.ErrAuthOrigin) || errors.Is(err, security.ErrAuthForwarded) || errors.Is(err, security.ErrAuthCredentialPath) {
		return &RequestError{Status: http.StatusForbidden, Code: domain.CodeUnavailable, Message: "request rejected", Retryable: false}
	}
	if typed := new(domain.Error); errors.As(err, &typed) {
		status := http.StatusBadRequest
		switch typed.Code() {
		case domain.CodeNotFound:
			status = http.StatusNotFound
		case domain.CodeRevisionConflict, domain.CodeIdempotencyConflict:
			status = http.StatusConflict
		case domain.CodeSecretRefusal, domain.CodeQuarantine:
			status = http.StatusUnprocessableEntity
		case domain.CodeTimeout:
			status = http.StatusGatewayTimeout
		case domain.CodeUnavailable, domain.CodeStorageFull, domain.CodeMaintenanceLock:
			status = http.StatusServiceUnavailable
		}
		return &RequestError{Status: status, Code: typed.Code(), Message: safeDomainMessage(typed.Code()), Retryable: typed.Retryable()}
	}
	return &RequestError{Status: http.StatusInternalServerError, Code: domain.CodeUnavailable, Message: "internal error", Retryable: true}
}

func safeDomainMessage(code domain.ErrorCode) string {
	// Keep wire diagnostics stable and content-free even if an upstream adapter
	// accidentally constructs a domain error with a request-derived message.
	switch code {
	case domain.CodeValidation:
		return "invalid request"
	case domain.CodeNotFound:
		return "not found"
	case domain.CodeRevisionConflict, domain.CodeIdempotencyConflict:
		return "conflict"
	case domain.CodeSecretRefusal:
		return "content refused"
	case domain.CodeQuarantine:
		return "content quarantined"
	case domain.CodeTimeout:
		return "request timed out"
	case domain.CodeStorageFull:
		return "storage capacity exhausted"
	case domain.CodeMaintenanceLock:
		return "maintenance lock unavailable"
	case domain.CodeUnavailable:
		return "service unavailable"
	}
	return "request failed"
}

func receiptID(now time.Time) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		// A receipt is diagnostic metadata; a deterministic fallback is still a
		// valid UUIDv7-shaped value and never contains request data.
		binary.BigEndian.PutUint64(value[:8], uint64(now.UnixMilli()))
	}
	millis := uint64(now.UnixMilli())
	binary.BigEndian.PutUint32(value[0:4], uint32(millis>>16))
	binary.BigEndian.PutUint16(value[4:6], uint16(millis))
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", binary.BigEndian.Uint32(value[0:4]), binary.BigEndian.Uint16(value[4:6]), binary.BigEndian.Uint16(value[6:8]), binary.BigEndian.Uint16(value[8:10]), uint64(value[10])<<40|uint64(value[11])<<32|uint64(value[12])<<24|uint64(value[13])<<16|uint64(value[14])<<8|uint64(value[15]))
}
