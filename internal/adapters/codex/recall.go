package codex

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
	"github.com/pkoukk/tiktoken-go"
)

const (
	MaxRecallItems       = 3
	MaxRecallQueryBytes  = 1024
	MaxRecallTokens      = 800
	MaxRecallOutputBytes = 8 * 1024
	RecallReferenceStart = "[[TALARIA-RECALL-REFERENCE-BEGIN]]"
	RecallReferenceEnd   = "[[TALARIA-RECALL-REFERENCE-END]]"
)

type RecallItem struct {
	MemoryID    string
	RevisionID  string
	WorkspaceID string
	Kind        domain.MemoryKind
	Trust       domain.Trust
	Pinned      bool
	Label       string
	Title       string
	Content     string
}

type RecallResult struct {
	WorkspaceID string
	Items       []RecallItem
	Included    int
	Omitted     int
	Context     string
}

type RecallProvider interface {
	Recall(context.Context, string, string) (RecallResult, error)
}

type RecallService struct {
	Index   retrieval.Index
	Guard   *application.ContentOutputGuard
	Clock   ports.Clock
	encoder *tiktoken.Tiktoken
}

func NewRecallService(index retrieval.Index, guard *application.ContentOutputGuard, clock ports.Clock) *RecallService {
	service := &RecallService{Index: index, Guard: guard, Clock: clock}
	service.encoder, _ = tiktoken.GetEncoding("cl100k_base")
	return service
}

func (service *RecallService) Recall(ctx context.Context, workspaceID, query string) (RecallResult, error) {
	if service == nil || service.Index == nil || service.Guard == nil {
		return RecallResult{WorkspaceID: workspaceID}, domain.NewError(domain.CodeUnavailable, "prompt recall unavailable", true)
	}
	if strings.TrimSpace(workspaceID) == "" {
		return RecallResult{}, domain.NewError(domain.CodeValidation, "workspace is required for recall", false)
	}
	query = truncateUTF8(query, MaxRecallQueryBytes)
	if strings.TrimSpace(query) == "" {
		return RecallResult{WorkspaceID: workspaceID, Items: []RecallItem{}, Context: "", Included: 0}, nil
	}
	if service.encoder != nil {
		tokens := service.encoder.EncodeOrdinary(query)
		if len(tokens) > MaxRecallTokens {
			query = service.encoder.Decode(tokens[:MaxRecallTokens])
		}
	}
	match, err := retrieval.BuildMatchExpression(query)
	if err != nil {
		return RecallResult{}, err
	}
	candidates, err := service.Index.Search(ctx, match, 20)
	if err != nil {
		return RecallResult{}, err
	}
	now := time.Now().UTC()
	if service.Clock != nil {
		now = service.Clock.Now().UTC()
	}
	ranked := make([]recallCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !recallEligible(candidate, workspaceID) {
			continue
		}
		if candidate.RawBM25 == 0 && candidate.Score.RawLexicalRelevance == 0 {
			continue
		}
		score := candidate.Score
		if score.RawLexicalRelevance == 0 {
			score = retrieval.ScoreMemory(candidate.RawBM25, candidate.Memory.CreatedAt, now, 0)
		}
		effective := score.FinalScore
		if candidate.Memory.Trust == domain.TrustGenerated {
			effective *= 0.85
		}
		ranked = append(ranked, recallCandidate{candidate: candidate, score: score, effective: effective})
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].score.RawLexicalRelevance != ranked[right].score.RawLexicalRelevance {
			return ranked[left].score.RawLexicalRelevance > ranked[right].score.RawLexicalRelevance
		}
		if ranked[left].effective != ranked[right].effective {
			return ranked[left].effective > ranked[right].effective
		}
		if trustRank(ranked[left].candidate.Memory.Trust) != trustRank(ranked[right].candidate.Memory.Trust) {
			return trustRank(ranked[left].candidate.Memory.Trust) < trustRank(ranked[right].candidate.Memory.Trust)
		}
		if ranked[left].candidate.Memory.Pinned != ranked[right].candidate.Memory.Pinned {
			return ranked[left].candidate.Memory.Pinned
		}
		if ranked[left].candidate.Revision.Kind != ranked[right].candidate.Revision.Kind {
			return ranked[left].candidate.Revision.Kind == domain.MemoryKindStandingInstruction
		}
		return ranked[left].candidate.Memory.ID < ranked[right].candidate.Memory.ID
	})

	result := RecallResult{WorkspaceID: workspaceID, Items: make([]RecallItem, 0, MaxRecallItems)}
	for _, item := range ranked {
		if len(result.Items) >= MaxRecallItems {
			break
		}
		candidate := item.candidate
		if _, err := service.Guard.Check(ctx, application.OutputRequest{Route: ports.FieldSessionStart, WorkspaceID: workspaceID, MemoryID: candidate.Memory.ID, RevisionID: candidate.Revision.ID, Fields: []ports.TextField{{Name: ports.FieldSessionStart, Value: candidate.Revision.Title}, {Name: ports.FieldSessionStart, Value: candidate.Revision.Content}}}); err != nil {
			if domain.IsCode(err, domain.CodeQuarantine) {
				continue
			}
			return RecallResult{}, err
		}
		recallItem := RecallItem{MemoryID: candidate.Memory.ID, RevisionID: candidate.Revision.ID, WorkspaceID: candidate.Memory.WorkspaceID, Kind: candidate.Revision.Kind, Trust: candidate.Memory.Trust, Pinned: candidate.Memory.Pinned, Label: recallLabel(candidate), Title: candidate.Revision.Title, Content: candidate.Revision.Content}
		candidateItems := append(append([]RecallItem(nil), result.Items...), recallItem)
		candidateContext := renderRecall(candidateItems)
		if len([]byte(candidateContext)) > MaxRecallOutputBytes {
			continue
		}
		result.Items = candidateItems
		result.Context = candidateContext
	}
	result.Included = len(result.Items)
	result.Omitted = len(ranked) - result.Included
	if result.Omitted < 0 {
		result.Omitted = 0
	}
	return result, nil
}

type recallCandidate struct {
	candidate retrieval.Candidate
	score     retrieval.Score
	effective float64
}

func recallEligible(candidate retrieval.Candidate, workspaceID string) bool {
	if candidate.Memory.Lifecycle != domain.LifecycleActive || candidate.Revision.Lifecycle != domain.LifecycleActive || candidate.Memory.Trust == domain.TrustUnverified || candidate.Revision.Trust == domain.TrustUnverified {
		return false
	}
	if candidate.Memory.Trust != domain.TrustVerified && candidate.Memory.Trust != domain.TrustGenerated {
		return false
	}
	if candidate.Revision.Trust != candidate.Memory.Trust {
		return false
	}
	return candidate.Memory.UserGlobal || candidate.Memory.WorkspaceID == workspaceID
}

func trustRank(trust domain.Trust) int {
	if trust == domain.TrustVerified {
		return 0
	}
	return 1
}

func recallLabel(candidate retrieval.Candidate) string {
	if candidate.Memory.Trust == domain.TrustGenerated {
		return "generated/unconfirmed"
	}
	if candidate.Memory.Pinned && candidate.Revision.Kind == domain.MemoryKindStandingInstruction {
		return "verified pinned standing-instruction"
	}
	return "verified"
}

func renderRecall(items []RecallItem) string {
	if len(items) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(RecallReferenceStart)
	builder.WriteByte('\n')
	for _, item := range items {
		builder.WriteString(fmt.Sprintf("id=%s revision=%s kind=%s trust=%s label=%s\n", item.MemoryID, item.RevisionID, item.Kind, item.Trust, item.Label))
		title := escapeRecallFence(item.Title)
		content := escapeRecallFence(item.Content)
		builder.WriteString("title: ")
		builder.WriteString(title)
		builder.WriteByte('\n')
		builder.WriteString(content)
		builder.WriteString("\n---\n")
	}
	builder.WriteString(RecallReferenceEnd)
	return builder.String()
}

func escapeRecallFence(value string) string {
	return strings.ReplaceAll(value, RecallReferenceEnd, "[TALARIA-RECALL-END-ESCAPED]")
}

func truncateUTF8(value string, maxBytes int) string {
	if len([]byte(value)) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
