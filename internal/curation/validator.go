package curation

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

var ErrInvalidCandidate = errors.New("invalid curation candidate")

const maxCandidates = 5

func DecodeCandidates(data []byte, allowedKinds []domain.MemoryKind) ([]Candidate, error) {
	if len(data) == 0 {
		return nil, invalidOutput("empty curator output")
	}
	if err := domain.RejectDuplicateJSONKeys(data); err != nil {
		return nil, invalidOutput("duplicate or invalid curator JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope struct {
		Candidates *[]Candidate `json:"candidates"`
	}
	if err := decoder.Decode(&envelope); err != nil || envelope.Candidates == nil {
		return nil, invalidOutput("curator output must contain candidates")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidOutput("curator output contains trailing data")
	}
	if err := ValidateCandidates(*envelope.Candidates, allowedKinds); err != nil {
		return nil, err
	}
	return append([]Candidate(nil), (*envelope.Candidates)...), nil
}

func ValidateCandidates(candidates []Candidate, allowedKinds []domain.MemoryKind) error {
	if len(candidates) > maxCandidates {
		return invalidOutput("too many curator candidates")
	}
	if len(allowedKinds) == 0 {
		allowedKinds = []domain.MemoryKind{domain.MemoryKindState, domain.MemoryKindProcedure, domain.MemoryKindFailure}
	}
	for index, candidate := range candidates {
		if !candidate.Kind.Valid() || candidate.Kind == domain.MemoryKindStandingInstruction || !containsKind(allowedKinds, candidate.Kind) {
			return invalidCandidate(index, "candidate kind is not allowed")
		}
		if strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.Content) == "" {
			return invalidCandidate(index, "candidate title and content are required")
		}
		if err := domain.ValidateMemoryText(candidate.Title, []byte(candidate.Content), candidate.Tags); err != nil {
			return invalidCandidate(index, "candidate text exceeds domain limits")
		}
		if !utf8.ValidString(candidate.Title) || !utf8.ValidString(candidate.Content) || hasControlBytes(candidate.Title) || hasControlBytes(candidate.Content) {
			return invalidCandidate(index, "candidate text is invalid")
		}
		for _, tag := range candidate.Tags {
			if hasControlBytes(tag) {
				return invalidCandidate(index, "candidate tag is invalid")
			}
		}
		if containsControlLikeText(candidate.Title) || containsControlLikeText(candidate.Content) || containsControlLikeTags(candidate.Tags) {
			return suspiciousOutput("curator candidate contains control content")
		}
		if _, err := domain.ValidateResolutionState(candidate.Kind, candidate.ResolutionState); err != nil {
			return invalidCandidate(index, "candidate resolution state is invalid")
		}
	}
	return nil
}

func CandidateFingerprint(candidate Candidate, key []byte) (string, error) {
	if len(key) == 0 {
		return "", ErrInvalidCandidate
	}
	if err := ValidateCandidates([]Candidate{candidate}, nil); err != nil {
		return "", err
	}
	normalized, err := domain.NormalizeV1(string(candidate.Kind), candidate.Title, candidate.Content, candidate.Tags)
	if err != nil {
		return "", ErrInvalidCandidate
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(normalized))
	_, _ = mac.Write([]byte("\x00resolution="))
	_, _ = mac.Write([]byte(candidate.ResolutionState))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func invalidOutput(message string) error {
	return NewProviderError(ErrorInvalidOutput, fmt.Errorf("%s: %w", message, ErrInvalidCandidate))
}

func invalidCandidate(index int, message string) error {
	return NewProviderError(ErrorInvalidOutput, fmt.Errorf("candidate %d: %s: %w", index, message, ErrInvalidCandidate))
}

func suspiciousOutput(message string) error {
	return NewProviderError(ErrorSuspiciousContent, errors.New(message))
}

func containsKind(allowed []domain.MemoryKind, candidate domain.MemoryKind) bool {
	for _, kind := range allowed {
		if kind == candidate {
			return true
		}
	}
	return false
}

func containsControlLikeTags(tags []string) bool {
	for _, tag := range tags {
		if containsControlLikeText(tag) {
			return true
		}
	}
	return false
}

func containsControlLikeText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"talaria-mem:", "</talaria", "tool_call", "tool-use", "tool_use",
		"function_call", "function-call", "approval_request", "request_approval",
		"<|tool", "<|approval",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func hasControlBytes(value string) bool {
	for _, character := range value {
		if (character < 0x20 && character != '\t' && character != '\n' && character != '\r') || character == 0x7f {
			return true
		}
	}
	return false
}
