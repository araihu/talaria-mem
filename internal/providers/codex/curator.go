package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/providers/codex/protocol"
)

type CuratorConfig struct {
	Model           string
	ReasoningEffort string
	Timeout         time.Duration
}

type Curator struct {
	client          *AppClient
	model           string
	reasoningEffort string
	timeout         time.Duration
}

func NewCurator(client *AppClient, configuration CuratorConfig) *Curator {
	if configuration.Model == "" {
		configuration.Model = "gpt-5.6-luna"
	}
	if configuration.ReasoningEffort == "" {
		configuration.ReasoningEffort = "high"
	}
	if configuration.Timeout <= 0 {
		configuration.Timeout = 90 * time.Second
	}
	return &Curator{client: client, model: configuration.Model, reasoningEffort: configuration.ReasoningEffort, timeout: configuration.Timeout}
}

func (curator *Curator) Curate(parent context.Context, request curation.CurationRequest) (curation.CurationResult, error) {
	if curator == nil || curator.client == nil {
		return curation.CurationResult{}, curation.NewProviderError(curation.ErrorUnavailable, errors.New("Codex curator unavailable"))
	}
	threadID, err := decodeThreadLocator(request.ThreadLocator)
	if err != nil {
		return curation.CurationResult{}, curation.NewProviderError(curation.ErrorDomainValidation, err)
	}
	temporary, err := os.MkdirTemp("", "talaria-mem-curation-")
	if err != nil {
		return curation.CurationResult{}, curation.NewProviderError(curation.ErrorUnavailable, err)
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o700); err != nil {
		return curation.CurationResult{}, curation.NewProviderError(curation.ErrorUnavailable, err)
	}
	ctx, cancel := context.WithTimeout(parent, curator.timeout)
	defer cancel()
	fork, err := curator.client.client.ThreadFork(ctx, protocol.ThreadForkParams{
		ThreadID:         threadID,
		Cwd:              temporary,
		Ephemeral:        true,
		Model:            curator.model,
		Sandbox:          "read-only",
		ApprovalPolicy:   "never",
		BaseInstructions: "You are a Talaria-Mem curator. Do not use tools or request approvals. Return only the requested JSON object.",
	})
	if err != nil {
		return curation.CurationResult{}, classifyProviderCallError(err)
	}
	threadID = fork.Thread.ID
	if threadID == "" {
		return curation.CurationResult{}, curation.NewProviderError(curation.ErrorInvalidOutput, errors.New("Codex fork returned no thread id"))
	}
	outputSchema := protocol.TurnStartParamsOutputSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"candidates"},
		"properties": map[string]any{
			"candidates": map[string]any{
				"type":     "array",
				"maxItems": 5,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []any{"kind", "title", "content"},
					"properties": map[string]any{
						"kind":             map[string]any{"type": "string", "enum": []any{"state", "procedure", "failure"}},
						"title":            map[string]any{"type": "string"},
						"content":          map[string]any{"type": "string"},
						"tags":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"resolution_state": map[string]any{"type": "string", "enum": []any{"open", "resolved"}},
					},
				},
			},
		},
	}
	prompt := curatorPrompt(request)
	turn, err := curator.client.client.TurnStart(ctx, protocol.TurnStartParams{
		ThreadID:       threadID,
		Input:          []protocol.UserInput{{Type: "text", Text: prompt}},
		Model:          curator.model,
		Effort:         curator.reasoningEffort,
		Cwd:            temporary,
		ApprovalPolicy: "never",
		OutputSchema:   &outputSchema,
	})
	if err != nil {
		return curation.CurationResult{}, classifyProviderCallError(err)
	}
	_ = turn
	text, err := curator.waitForOutput(ctx)
	if err != nil {
		return curation.CurationResult{}, err
	}
	candidates, err := decodeCandidates(text)
	if err != nil {
		return curation.CurationResult{}, err
	}
	allowed := request.AllowedKinds
	if len(allowed) == 0 {
		allowed = []domain.MemoryKind{domain.MemoryKindState, domain.MemoryKindProcedure, domain.MemoryKindFailure}
	}
	for _, candidate := range candidates {
		if !containsKind(allowed, candidate.Kind) {
			return curation.CurationResult{}, curation.NewProviderError(curation.ErrorInvalidOutput, fmt.Errorf("candidate kind %q is not allowed", candidate.Kind))
		}
	}
	return curation.CurationResult{Candidates: candidates, Provider: "codex", Model: curator.model}, nil
}

func (curator *Curator) waitForOutput(ctx context.Context) (string, error) {
	var output string
	for {
		select {
		case <-ctx.Done():
			return "", curation.NewProviderError(curation.ErrorTimeout, ctx.Err())
		case <-curator.client.process.Done():
			if err := curator.client.process.Err(); err != nil {
				return "", err
			}
			return "", curation.NewProviderError(curation.ErrorUnavailable, errors.New("Codex process closed"))
		case event := <-curator.client.Events():
			if event.IsRequest {
				_ = curator.client.Close()
				return "", curation.NewProviderError(curation.ErrorSuspiciousContent, errors.New("Codex requested a tool or approval"))
			}
			switch event.Method {
			case protocol.NotificationAgentMessageDone:
				var params struct {
					Item protocol.ThreadItem `json:"item"`
				}
				if err := json.Unmarshal(event.Params, &params); err != nil {
					return "", curation.NewProviderError(curation.ErrorInvalidOutput, err)
				}
				if value := threadItemText(params.Item); value != "" {
					output = value
				}
			case protocol.NotificationTurnCompleted:
				if output == "" {
					var params struct {
						Turn protocol.Turn `json:"turn"`
					}
					if err := json.Unmarshal(event.Params, &params); err == nil {
						for _, item := range params.Turn.Items {
							if value := threadItemText(item); value != "" {
								output = value
							}
						}
					}
				}
				if output == "" {
					return "", curation.NewProviderError(curation.ErrorInvalidOutput, errors.New("Codex completed without curator output"))
				}
				return output, nil
			case protocol.NotificationError:
				return "", curation.NewProviderError(curation.ErrorUnavailable, errors.New("Codex turn failed"))
			}
		}
	}
}

type wireCandidate struct {
	Kind            domain.MemoryKind      `json:"kind"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	Tags            []string               `json:"tags,omitempty"`
	ResolutionState domain.ResolutionState `json:"resolution_state,omitempty"`
}

func decodeCandidates(text string) ([]curation.Candidate, error) {
	if strings.Contains(text, "talaria-mem:") || strings.Contains(text, "</talaria") {
		return nil, curation.NewProviderError(curation.ErrorSuspiciousContent, errors.New("curator output contains control fence"))
	}
	if err := domain.RejectDuplicateJSONKeys([]byte(text)); err != nil {
		return nil, curation.NewProviderError(curation.ErrorInvalidOutput, err)
	}
	var result struct {
		Candidates *[]wireCandidate `json:"candidates"`
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || result.Candidates == nil {
		if err == nil {
			err = errors.New("missing candidates")
		}
		return nil, curation.NewProviderError(curation.ErrorInvalidOutput, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, curation.NewProviderError(curation.ErrorInvalidOutput, errors.New("extra JSON value after candidates"))
	}
	if len(*result.Candidates) > 5 {
		return nil, curation.NewProviderError(curation.ErrorInvalidOutput, errors.New("too many candidates"))
	}
	candidates := make([]curation.Candidate, len(*result.Candidates))
	for index, candidate := range *result.Candidates {
		if !candidate.Kind.Valid() || candidate.Kind == domain.MemoryKindStandingInstruction || candidate.Title == "" || candidate.Content == "" {
			return nil, curation.NewProviderError(curation.ErrorInvalidOutput, fmt.Errorf("candidate %d is invalid", index))
		}
		candidates[index] = curation.Candidate{Kind: candidate.Kind, Title: candidate.Title, Content: candidate.Content, Tags: candidate.Tags, ResolutionState: candidate.ResolutionState}
	}
	return candidates, nil
}

func decodeThreadLocator(data []byte) (string, error) {
	var locator struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal(data, &locator); err != nil || locator.ThreadID == "" {
		return "", errors.New("invalid Codex thread locator")
	}
	return locator.ThreadID, nil
}

func curatorPrompt(request curation.CurationRequest) string {
	return fmt.Sprintf("Reason: %s\nWatermark: %d\nSource snapshot:\n%s\n\nReturn exactly one JSON object with key candidates. Each candidate must contain kind, title, content, and optional tags/resolution_state. Do not return standing instructions, trust, pin, global scope, tools, or approvals.", request.Reason, request.Watermark, request.Snapshot)
}

func threadItemText(item protocol.ThreadItem) string {
	for _, value := range []any{item.Text, item.Content, item.Message} {
		if text, ok := value.(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func containsKind(kinds []domain.MemoryKind, target domain.MemoryKind) bool {
	for _, kind := range kinds {
		if kind == target {
			return true
		}
	}
	return false
}
