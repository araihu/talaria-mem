package retrieval

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type retrievalClock struct{ now time.Time }

func (clock retrievalClock) Now() time.Time { return clock.now }

type retrievalScanner struct{}

func (retrievalScanner) Scan(context.Context, []ports.TextField) ports.ScanResult {
	return ports.ScanResult{Status: ports.ScanClean, Generation: "test"}
}

func retrievalCandidate(id string, created time.Time) Candidate {
	return Candidate{Memory: domain.Memory{ID: id, WorkspaceID: "workspace", Kind: domain.MemoryKindState, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: created, UpdatedAt: created}, Revision: domain.MemoryRevision{ID: id + "-revision", MemoryID: id, Number: 1, Kind: domain.MemoryKindState, Title: "title " + id, Content: "shared query content", Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: created}, RawBM25: -1}
}

func TestMatchExpressionQuotesOperatorsAndNormalizesNFC(t *testing.T) {
	expression, err := BuildMatchExpression("Cafe\u0301 OR token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expression, "\"Café\"") || strings.Contains(expression, " OR ") {
		t.Fatalf("expression = %q", expression)
	}
	if _, err := BuildMatchExpression(strings.Repeat("x", domain.MaxQueryBytes+1)); err == nil {
		t.Fatal("oversized query accepted")
	}
}

func TestScoreMathCapsAndFutureClock(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	score := ScoreMemory(-2, now.Add(-30*24*time.Hour), now, 20)
	if score.RawLexicalRelevance != 2 || math.Abs(score.NormalizedRelevance-2.0/3.0) > 1e-12 {
		t.Fatalf("score = %+v", score)
	}
	if score.FreshnessBoost < 0 || score.UsageBoost > UsageCap || score.CombinedBoost > CombinedCap {
		t.Fatalf("caps = %+v", score)
	}
	future := ScoreMemory(-1, now.Add(time.Hour), now, 0)
	if future.FreshnessBoost != FreshnessCap {
		t.Fatalf("future freshness = %v", future.FreshnessBoost)
	}
}

func TestSearchTop20OpportunitiesAndSessionDedup(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	candidates := make([]Candidate, 0, 21)
	for index := 0; index < 21; index++ {
		candidates = append(candidates, retrievalCandidate(string(rune('a'+index)), now.Add(-time.Hour)))
	}
	usage := NewUsageLedger(retrievalClock{now}, nil)
	guard := application.NewContentOutputGuard(retrievalScanner{}, nil, retrievalClock{now})
	searcher := NewSearcher(MemoryIndex{Items: candidates}, usage, guard, retrievalClock{now})
	result, err := searcher.Search(context.Background(), SearchRequest{Query: "shared", WorkspaceID: "workspace", ConsumerSession: "session-1", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Included != 2 || result.Omitted != 18 {
		t.Fatalf("result counts = %+v", result)
	}
	stats, err := usage.Stats(context.Background(), candidates[0].Memory.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Opportunities != 1 {
		t.Fatalf("top candidate opportunities = %+v", stats)
	}
	_, err = searcher.Search(context.Background(), SearchRequest{Query: "shared", WorkspaceID: "workspace", ConsumerSession: "session-1", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	stats, _ = usage.Stats(context.Background(), candidates[0].Memory.ID, now)
	if stats.Opportunities != 2 || stats.Hits != 1 {
		t.Fatalf("dedup stats = %+v", stats)
	}
}

func TestWilsonPruningGraceAndProtection(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	usage := NewUsageLedger(retrievalClock{now}, nil)
	for index := 0; index < 100; index++ {
		if err := usage.RecordOpportunity(context.Background(), "cold", now); err != nil {
			t.Fatal(err)
		}
	}
	pruner := NewPruner(usage, func() time.Time { return now }, 0.05)
	candidate := PruneCandidate{Memory: domain.Memory{ID: "cold", WorkspaceID: "workspace", Kind: domain.MemoryKindState, Trust: domain.TrustUnverified, Lifecycle: domain.LifecycleActive, CreatedAt: now.Add(-31 * 24 * time.Hour)}, Revision: domain.MemoryRevision{ID: "revision", MemoryID: "cold", Kind: domain.MemoryKindState, Trust: domain.TrustUnverified, Lifecycle: domain.LifecycleActive}}
	recommendations, err := pruner.Recommend(context.Background(), []PruneCandidate{candidate}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(recommendations) != 1 || recommendations[0].UpperBound >= 0.05 {
		t.Fatalf("recommendations = %+v", recommendations)
	}
	candidate.Memory.Pinned = true
	recommendations, err = pruner.Recommend(context.Background(), []PruneCandidate{candidate}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(recommendations) != 0 {
		t.Fatal("pinned memory recommended")
	}
	if WilsonUpperBound(0, 0) != 1 {
		t.Fatal("invalid Wilson input not conservative")
	}
}
