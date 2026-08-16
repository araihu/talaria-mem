package retrieval

import (
	"math"
	"sort"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const (
	TitleWeight     = 5.0
	ContentWeight   = 1.0
	TagsWeight      = 2.0
	MemoryIDWeight  = 0.0
	FreshnessCap    = 0.10
	UsageCap        = 0.20
	CombinedCap     = 0.25
	UsageSaturation = 20.0
)

type Score struct {
	RawLexicalRelevance float64
	NormalizedRelevance float64
	FreshnessBoost      float64
	UsageBoost          float64
	CombinedBoost       float64
	FinalScore          float64
}

func ScoreMemory(rawBM25 float64, createdAt, now time.Time, distinctSessionHits int64) Score {
	raw := math.Max(0, -rawBM25)
	normalized := raw / (1 + raw)
	age := now.UTC().Sub(createdAt.UTC()).Seconds()
	if age < 0 {
		age = 0
	}
	freshness := FreshnessCap * math.Pow(2, -age/(30*24*60*60))
	if freshness < 0 {
		freshness = 0
	}
	hits := float64(distinctSessionHits)
	if hits < 0 {
		hits = 0
	}
	usage := UsageCap * math.Log1p(hits) / math.Log(UsageSaturation+1)
	if usage < 0 {
		usage = 0
	}
	if usage > UsageCap {
		usage = UsageCap
	}
	combined := freshness + usage
	if combined > CombinedCap {
		combined = CombinedCap
	}
	return Score{RawLexicalRelevance: raw, NormalizedRelevance: normalized, FreshnessBoost: freshness, UsageBoost: usage, CombinedBoost: combined, FinalScore: normalized * (1 + combined)}
}

type Candidate struct {
	Memory   domain.Memory
	Revision domain.MemoryRevision
	RawBM25  float64
	Score    Score
}

func Eligible(candidate Candidate, workspaceID string) bool {
	if candidate.Memory.Trust != domain.TrustVerified || candidate.Memory.Lifecycle != domain.LifecycleActive || candidate.Revision.Trust != domain.TrustVerified || candidate.Revision.Lifecycle != domain.LifecycleActive {
		return false
	}
	if workspaceID != "" && !candidate.Memory.UserGlobal && candidate.Memory.WorkspaceID != workspaceID {
		return false
	}
	if workspaceID != "" && candidate.Memory.UserGlobal {
		return true
	}
	return true
}

func StableSort(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.Score.FinalScore != right.Score.FinalScore {
			return left.Score.FinalScore > right.Score.FinalScore
		}
		if left.Score.RawLexicalRelevance != right.Score.RawLexicalRelevance {
			return left.Score.RawLexicalRelevance > right.Score.RawLexicalRelevance
		}
		if !left.Revision.CreatedAt.Equal(right.Revision.CreatedAt) {
			return left.Revision.CreatedAt.After(right.Revision.CreatedAt)
		}
		return left.Memory.ID < right.Memory.ID
	})
}

func StableRawSort(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.Score.RawLexicalRelevance != right.Score.RawLexicalRelevance {
			return left.Score.RawLexicalRelevance > right.Score.RawLexicalRelevance
		}
		if !left.Revision.CreatedAt.Equal(right.Revision.CreatedAt) {
			return left.Revision.CreatedAt.After(right.Revision.CreatedAt)
		}
		return left.Memory.ID < right.Memory.ID
	})
}

func Explain(score Score) map[string]float64 {
	return map[string]float64{"raw_lexical_relevance": score.RawLexicalRelevance, "normalized_relevance": score.NormalizedRelevance, "freshness_boost": score.FreshnessBoost, "usage_boost": score.UsageBoost, "combined_boost": score.CombinedBoost, "final_score": score.FinalScore}
}
