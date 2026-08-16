package retrieval

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const (
	DefaultPruneThreshold         = 0.05
	PruneGraceAge                 = 30 * 24 * time.Hour
	PruneGraceOpportunities int64 = 20
	WilsonZ                       = 1.64485362695
)

type PruneCandidate struct {
	Memory       domain.Memory
	Revision     domain.MemoryRevision
	UserAuthored bool
	VerifiedAt   time.Time
}

type PruneRecommendation struct {
	MemoryID      string
	WorkspaceID   string
	UpperBound    float64
	Hits          int64
	Opportunities int64
	Reason        string
}

type Pruner struct {
	Usage     UsageStore
	Clock     func() time.Time
	Threshold float64
}

func NewPruner(usage UsageStore, clock func() time.Time, threshold float64) *Pruner {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	if threshold <= 0 || threshold >= 1 {
		threshold = DefaultPruneThreshold
	}
	return &Pruner{Usage: usage, Clock: clock, Threshold: threshold}
}

func WilsonUpperBound(hits, opportunities int64) float64 {
	if opportunities <= 0 || hits < 0 || hits > opportunities {
		return 1
	}
	n := float64(opportunities)
	p := float64(hits) / n
	z := WilsonZ
	denominator := 1 + z*z/n
	numerator := p + z*z/(2*n) + z*math.Sqrt(p*(1-p)/n+z*z/(4*n*n))
	return numerator / denominator
}

func (pruner *Pruner) Recommend(ctx context.Context, candidates []PruneCandidate, includeVerified bool) ([]PruneRecommendation, error) {
	if pruner == nil || pruner.Usage == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "usage store unavailable", true)
	}
	now := pruner.Clock().UTC()
	recommendations := make([]PruneRecommendation, 0)
	for _, candidate := range candidates {
		if candidate.Memory.ID == "" {
			continue
		}
		if candidate.Memory.Pinned || candidate.Memory.Kind == domain.MemoryKindStandingInstruction || (candidate.Memory.Kind == domain.MemoryKindFailure && candidate.Revision.ResolutionState == domain.ResolutionOpen) {
			continue
		}
		age := now.Sub(candidate.Memory.CreatedAt.UTC())
		if age < PruneGraceAge {
			continue
		}
		stats, err := pruner.Usage.Stats(ctx, candidate.Memory.ID, now)
		if err != nil {
			return nil, err
		}
		if stats.Opportunities < PruneGraceOpportunities {
			continue
		}
		if candidate.UserAuthored && !includeVerified {
			continue
		}
		verifiedAt := candidate.VerifiedAt
		if verifiedAt.IsZero() {
			verifiedAt = candidate.Memory.UpdatedAt
		}
		if candidate.Revision.Trust == domain.TrustVerified && !includeVerified && !verifiedAt.IsZero() && now.Sub(verifiedAt.UTC()) < PruneGraceAge {
			continue
		}
		upper := WilsonUpperBound(stats.Hits, stats.Opportunities)
		if upper >= pruner.Threshold {
			continue
		}
		recommendations = append(recommendations, PruneRecommendation{MemoryID: candidate.Memory.ID, WorkspaceID: candidate.Memory.WorkspaceID, UpperBound: upper, Hits: stats.Hits, Opportunities: stats.Opportunities, Reason: "optimistic 95% hit-rate bound below retention threshold"})
	}
	sort.Slice(recommendations, func(i, j int) bool {
		if recommendations[i].UpperBound != recommendations[j].UpperBound {
			return recommendations[i].UpperBound < recommendations[j].UpperBound
		}
		return recommendations[i].MemoryID < recommendations[j].MemoryID
	})
	return recommendations, nil
}

func (pruner *Pruner) Explain(ctx context.Context, candidate PruneCandidate) (PruneRecommendation, error) {
	if pruner == nil || pruner.Usage == nil {
		return PruneRecommendation{}, domain.NewError(domain.CodeUnavailable, "usage store unavailable", true)
	}
	stats, err := pruner.Usage.Stats(ctx, candidate.Memory.ID, pruner.Clock().UTC())
	if err != nil {
		return PruneRecommendation{}, err
	}
	return PruneRecommendation{MemoryID: candidate.Memory.ID, WorkspaceID: candidate.Memory.WorkspaceID, UpperBound: WilsonUpperBound(stats.Hits, stats.Opportunities), Hits: stats.Hits, Opportunities: stats.Opportunities, Reason: "pruning uses 90-day opportunities and Wilson upper bound"}, nil
}

type ForgetFunc func(ctx context.Context, memoryID string) error

func (pruner *Pruner) Apply(ctx context.Context, recommendations []PruneRecommendation, forget ForgetFunc) error {
	if forget == nil {
		return domain.NewError(domain.CodeValidation, "prune apply requires explicit forget operation", false)
	}
	for _, recommendation := range recommendations {
		if err := forget(ctx, recommendation.MemoryID); err != nil {
			return err
		}
	}
	return nil
}
