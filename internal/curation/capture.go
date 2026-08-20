package curation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

const (
	MaxCurrentPromptBytes = 32 * 1024
	MaxSnapshotBytes      = security.MaxCurationSnapshotPlaintext
	MaxLocatorBytes       = security.MaxCurationLocatorPlaintext
	CurationJobRetention  = 24 * time.Hour
)

type CaptureRequest struct {
	WorkspaceID     string
	SessionID       string
	Reason          Reason
	SourceWatermark int64
}

type SanitizedSnapshot struct {
	ThreadLocator []byte
	Snapshot      []byte
}

type SnapshotSource interface {
	Capture(context.Context, CaptureRequest) (SanitizedSnapshot, error)
}

type EnqueueRequest struct {
	WorkspaceID     string
	SessionID       string
	Reason          Reason
	SourceWatermark int64
	CurrentPrompt   string
}

type EnqueueResult struct {
	Enqueued bool
	Job      Job
}

type EnqueueService struct {
	source  SnapshotSource
	scanner ports.Scanner
	cipher  security.CurationCipher
	store   JobStore
	now     func() time.Time
}

func NewEnqueueService(source SnapshotSource, scanner ports.Scanner, cipher security.CurationCipher, store JobStore, now time.Time) *EnqueueService {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return &EnqueueService{source: source, scanner: scanner, cipher: cipher, store: store, now: func() time.Time { return now.UTC() }}
}

func NewEnqueueServiceWithClock(source SnapshotSource, scanner ports.Scanner, cipher security.CurationCipher, store JobStore, clock ports.Clock) *EnqueueService {
	return &EnqueueService{source: source, scanner: scanner, cipher: cipher, store: store, now: func() time.Time {
		if clock == nil {
			return time.Now().UTC()
		}
		return clock.Now().UTC()
	}}
}

func (service *EnqueueService) Enqueue(ctx context.Context, request EnqueueRequest) (EnqueueResult, error) {
	if service == nil || service.source == nil || service.scanner == nil || service.store == nil {
		return EnqueueResult{}, NewProviderError(ErrorUnavailable, errors.New("curation enqueue unavailable"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if request.WorkspaceID == "" || request.SessionID == "" || !request.Reason.Valid() || request.Reason == ReasonInline || request.SourceWatermark < 0 {
		return EnqueueResult{}, NewProviderError(ErrorDomainValidation, errors.New("invalid curation enqueue request"))
	}
	if !utf8.ValidString(request.CurrentPrompt) || len([]byte(request.CurrentPrompt)) > MaxCurrentPromptBytes {
		return EnqueueResult{}, NewProviderError(ErrorDomainValidation, errors.New("current prompt exceeds capture limit"))
	}
	captured, err := service.source.Capture(ctx, CaptureRequest{WorkspaceID: request.WorkspaceID, SessionID: request.SessionID, Reason: request.Reason, SourceWatermark: request.SourceWatermark})
	if err != nil {
		return EnqueueResult{}, NewProviderError(ErrorUnavailable, errors.New("snapshot capture failed"))
	}
	if len(captured.ThreadLocator) == 0 || len(captured.ThreadLocator) > MaxLocatorBytes || len(captured.Snapshot) == 0 || len(captured.Snapshot) > MaxSnapshotBytes {
		return EnqueueResult{}, NewProviderError(ErrorDomainValidation, errors.New("captured snapshot exceeds bounds"))
	}
	defer clearCaptureBytes(captured.ThreadLocator)
	defer clearCaptureBytes(captured.Snapshot)
	snapshot, err := service.appendPrompt(ctx, captured.Snapshot, request.CurrentPrompt)
	if err != nil {
		return EnqueueResult{}, err
	}
	if len(snapshot) > MaxSnapshotBytes {
		return EnqueueResult{}, NewProviderError(ErrorDomainValidation, errors.New("sanitized snapshot exceeds capture limit"))
	}
	defer clearCaptureBytes(snapshot)
	digest, err := service.cipher.SessionDigest(ctx, []byte(request.SessionID))
	if err != nil {
		return EnqueueResult{}, NewProviderError(ErrorUnavailable, errors.New("session digest unavailable"))
	}
	jobID := uuid.NewString()
	metadata := JobMetadata(request.WorkspaceID, request.Reason, request.SourceWatermark)
	defer clearCaptureBytes(metadata)
	locatorCiphertext, err := service.cipher.SealLocator(ctx, jobID, metadata, captured.ThreadLocator)
	if err != nil {
		return EnqueueResult{}, NewProviderError(ErrorUnavailable, errors.New("thread locator encryption failed"))
	}
	snapshotCiphertext, err := service.cipher.SealSnapshot(ctx, jobID, metadata, snapshot)
	if err != nil {
		return EnqueueResult{}, NewProviderError(ErrorUnavailable, errors.New("snapshot encryption failed"))
	}
	now := service.now().UTC()
	job, created, err := service.store.Enqueue(ctx, Job{ID: jobID, WorkspaceID: request.WorkspaceID, Reason: request.Reason, SessionDigest: digest, SourceWatermark: request.SourceWatermark, ThreadLocatorCiphertext: locatorCiphertext, SnapshotCiphertext: snapshotCiphertext, State: JobPending, ExpiresAt: now.Add(CurationJobRetention), CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return EnqueueResult{}, NewProviderError(ErrorPersistence, errors.New("curation job persistence failed"))
	}
	return EnqueueResult{Enqueued: created, Job: job}, nil
}

func clearCaptureBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func (service *EnqueueService) appendPrompt(ctx context.Context, snapshot []byte, prompt string) ([]byte, error) {
	var envelope snapshotEnvelope
	decoder := json.NewDecoder(bytes.NewReader(snapshot))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, NewProviderError(ErrorInvalidOutput, errors.New("captured snapshot is invalid"))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, NewProviderError(ErrorInvalidOutput, errors.New("captured snapshot contains trailing data"))
	}
	if prompt != "" {
		clean, err := sanitizeCaptureField(ctx, service.scanner, ports.FieldContent, prompt)
		if err != nil {
			return nil, err
		}
		envelope.Turns = append(envelope.Turns, SnapshotTurn{Role: "user", Text: clean})
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, NewProviderError(ErrorInvalidOutput, errors.New("captured snapshot encoding failed"))
	}
	return data, nil
}

type snapshotEnvelope struct {
	Turns []SnapshotTurn `json:"turns"`
	Tools []SnapshotTool `json:"tools,omitempty"`
}

type SnapshotTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type SnapshotTool struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	ErrorSummary string `json:"error,omitempty"`
}

func sanitizeCaptureField(ctx context.Context, scanner ports.Scanner, field ports.FieldIdentifier, value string) (string, error) {
	value = strings.ToValidUTF8(value, "�")
	result := scanner.Scan(ctx, []ports.TextField{{Name: field, Value: value}})
	switch result.Status {
	case ports.ScanClean:
		return value, nil
	case ports.ScanFinding:
		redacted, err := redactCaptureFindings(value, result.Findings, field)
		if err != nil {
			return "", NewProviderError(ErrorScannerRefusal, errors.New("scanner returned invalid finding"))
		}
		rescanned := scanner.Scan(ctx, []ports.TextField{{Name: field, Value: redacted}})
		if rescanned.Status != ports.ScanClean {
			return "", NewProviderError(ErrorScannerRefusal, errors.New("scanner refused sanitized prompt"))
		}
		return redacted, nil
	default:
		return "", NewProviderError(ErrorScannerRefusal, fmt.Errorf("scanner status %s", result.Status))
	}
}

func redactCaptureFindings(value string, findings []ports.Finding, field ports.FieldIdentifier) (string, error) {
	for index := len(findings) - 1; index >= 0; index-- {
		finding := findings[index]
		if finding.Field != field || finding.Start < 0 || finding.End < finding.Start || finding.End > len(value) {
			return "", errors.New("invalid scanner finding")
		}
		value = value[:finding.Start] + "[REDACTED]" + value[finding.End:]
	}
	return value, nil
}

func JobMetadata(workspaceID string, reason Reason, watermark int64) []byte {
	return []byte(fmt.Sprintf("workspace=%s\x00reason=%s\x00watermark=%d", workspaceID, reason, watermark))
}
