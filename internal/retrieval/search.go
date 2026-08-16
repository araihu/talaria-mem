package retrieval

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

const FTS5Tokenizer = domain.FTS5Tokenizer

type Index interface {
	Search(ctx context.Context, matchExpression string, limit int) ([]Candidate, error)
	Get(ctx context.Context, memoryID string) (Candidate, error)
}

type SearchRequest struct {
	Query           string
	WorkspaceID     string
	ConsumerSession string
	Limit           int
	MaxBytes        int
	Route           ports.FieldIdentifier
}

type SearchItem struct {
	MemoryID        string
	RevisionID      string
	WorkspaceID     string
	Kind            domain.MemoryKind
	Title           string
	Content         string
	Tags            []string
	Score           Score
	ResolutionState domain.ResolutionState
}

type SearchResult struct {
	Items    []SearchItem
	Included int
	Omitted  int
	Match    string
}

type Searcher struct {
	Index Index
	Usage UsageStore
	Guard *application.ContentOutputGuard
	Clock ports.Clock
}

func NewSearcher(index Index, usage UsageStore, guard *application.ContentOutputGuard, clock ports.Clock) *Searcher {
	return &Searcher{Index: index, Usage: usage, Guard: guard, Clock: clock}
}

func BuildMatchExpression(query string) (string, error) {
	if !utf8.ValidString(query) || len([]byte(query)) > domain.MaxQueryBytes {
		return "", domain.NewError(domain.CodeValidation, "invalid or oversized search query", false)
	}
	query = norm.NFC.String(strings.TrimSpace(query))
	if query == "" {
		return "", domain.NewError(domain.CodeValidation, "search query is required", false)
	}
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return "", domain.NewError(domain.CodeValidation, "search query is required", false)
	}
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ReplaceAll(term, "\"", "\"\"")
		parts = append(parts, "\""+term+"\"")
	}
	return strings.Join(parts, " AND "), nil
}

func (searcher *Searcher) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	match, err := BuildMatchExpression(request.Query)
	if err != nil {
		return SearchResult{}, err
	}
	if searcher == nil || searcher.Index == nil {
		return SearchResult{}, domain.NewError(domain.CodeUnavailable, "retrieval index unavailable", true)
	}
	if request.WorkspaceID == "" {
		return SearchResult{}, domain.NewError(domain.CodeValidation, "workspace is required for retrieval", false)
	}
	if searcher.Guard == nil {
		return SearchResult{}, domain.NewError(domain.CodeUnavailable, "content scanner guard unavailable", true)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > domain.MaxSearchItems {
		limit = domain.MaxSearchItems
	}
	maxBytes := request.MaxBytes
	if maxBytes <= 0 {
		maxBytes = domain.MaxSearchBytes
	}
	if maxBytes > domain.MaxSearchBytes {
		maxBytes = domain.MaxSearchBytes
	}
	route := request.Route
	if !route.Valid() {
		route = ports.FieldMCPRead
	}
	now := time.Now().UTC()
	if searcher.Clock != nil {
		now = searcher.Clock.Now().UTC()
	}
	candidates, err := searcher.Index.Search(ctx, match, 20)
	if err != nil {
		return SearchResult{}, err
	}
	eligible := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Score.RawLexicalRelevance == 0 {
			if candidate.RawBM25 != 0 {
				candidate.Score = ScoreMemory(candidate.RawBM25, candidate.Memory.CreatedAt, now, 0)
			} else {
				continue
			}
		}
		if candidate.Score.RawLexicalRelevance <= 0 || !Eligible(candidate, request.WorkspaceID) {
			continue
		}
		eligible = append(eligible, candidate)
	}
	StableRawSort(eligible)
	if len(eligible) > 20 {
		eligible = eligible[:20]
	}
	if searcher.Usage != nil {
		if request.ConsumerSession == "" {
			return SearchResult{}, domain.NewError(domain.CodeValidation, "consumer session is required", false)
		}
		for _, candidate := range eligible {
			if err := searcher.Usage.RecordOpportunity(ctx, candidate.Memory.ID, now); err != nil {
				return SearchResult{}, err
			}
		}
	}
	for index := range eligible {
		stats := UsageStats{}
		if searcher.Usage != nil {
			stats, err = searcher.Usage.Stats(ctx, eligible[index].Memory.ID, now)
			if err != nil {
				return SearchResult{}, err
			}
		}
		eligible[index].Score = ScoreMemory(-eligible[index].Score.RawLexicalRelevance, eligible[index].Memory.CreatedAt, now, stats.DistinctSessionHits)
	}
	StableSort(eligible)
	result := SearchResult{Match: match, Omitted: len(eligible)}
	for _, candidate := range eligible {
		if len(result.Items) >= limit {
			continue
		}
		item := SearchItem{MemoryID: candidate.Memory.ID, RevisionID: candidate.Revision.ID, WorkspaceID: candidate.Memory.WorkspaceID, Kind: candidate.Revision.Kind, Title: candidate.Revision.Title, Content: candidate.Revision.Content, Tags: append([]string(nil), candidate.Revision.Tags...), Score: candidate.Score, ResolutionState: candidate.Revision.ResolutionState}
		if searcher.Guard != nil {
			fields := []ports.TextField{{Name: route, Value: item.Title + "\n" + item.Content}}
			for _, tag := range item.Tags {
				fields = append(fields, ports.TextField{Name: route, Value: tag})
			}
			if _, err := searcher.Guard.Check(ctx, application.OutputRequest{Route: route, WorkspaceID: request.WorkspaceID, MemoryID: item.MemoryID, RevisionID: item.RevisionID, Fields: fields}); err != nil {
				if domain.IsCode(err, domain.CodeQuarantine) {
					continue
				}
				return SearchResult{}, err
			}
		}
		encoded, _ := json.Marshal(item)
		if len(encoded) > maxBytes || resultBytes(result.Items)+len(encoded) > maxBytes {
			continue
		}
		result.Items = append(result.Items, item)
		if searcher.Usage != nil {
			var hitErr error
			if hitStore, ok := searcher.Usage.(HitStore); ok {
				_, hitErr = hitStore.RecordHit(ctx, item.MemoryID, request.ConsumerSession, now)
			} else {
				_, hitErr = searcher.Usage.RecordDelivery(ctx, item.MemoryID, request.ConsumerSession, now)
			}
			if hitErr != nil {
				return SearchResult{}, hitErr
			}
		}
	}
	result.Included = len(result.Items)
	result.Omitted -= result.Included
	if result.Omitted < 0 {
		result.Omitted = 0
	}
	return result, nil
}

func (searcher *Searcher) Get(ctx context.Context, memoryID, workspaceID, consumerSession string, route ports.FieldIdentifier) (SearchItem, error) {
	if searcher == nil || searcher.Index == nil {
		return SearchItem{}, domain.NewError(domain.CodeUnavailable, "retrieval index unavailable", true)
	}
	if workspaceID == "" {
		return SearchItem{}, domain.NewError(domain.CodeValidation, "workspace is required for retrieval", false)
	}
	if searcher.Guard == nil {
		return SearchItem{}, domain.NewError(domain.CodeUnavailable, "content scanner guard unavailable", true)
	}
	candidate, err := searcher.Index.Get(ctx, memoryID)
	if err != nil {
		return SearchItem{}, err
	}
	if !Eligible(candidate, workspaceID) {
		return SearchItem{}, domain.NewError(domain.CodeNotFound, "memory not found", false)
	}
	now := time.Now().UTC()
	if searcher.Clock != nil {
		now = searcher.Clock.Now().UTC()
	}
	stats := UsageStats{}
	if searcher.Usage != nil {
		stats, err = searcher.Usage.Stats(ctx, memoryID, now)
		if err != nil {
			return SearchItem{}, err
		}
	}
	candidate.Score = ScoreMemory(candidate.RawBM25, candidate.Memory.CreatedAt, now, stats.DistinctSessionHits)
	item := SearchItem{MemoryID: candidate.Memory.ID, RevisionID: candidate.Revision.ID, WorkspaceID: candidate.Memory.WorkspaceID, Kind: candidate.Revision.Kind, Title: candidate.Revision.Title, Content: candidate.Revision.Content, Tags: append([]string(nil), candidate.Revision.Tags...), Score: candidate.Score, ResolutionState: candidate.Revision.ResolutionState}
	if !route.Valid() {
		route = ports.FieldMCPRead
	}
	if searcher.Guard != nil {
		if _, err := searcher.Guard.Check(ctx, application.OutputRequest{Route: route, WorkspaceID: workspaceID, MemoryID: item.MemoryID, RevisionID: item.RevisionID, Fields: []ports.TextField{{Name: route, Value: item.Title + "\n" + item.Content}}}); err != nil {
			return SearchItem{}, err
		}
	}
	if searcher.Usage != nil {
		if consumerSession == "" {
			return SearchItem{}, domain.NewError(domain.CodeValidation, "consumer session is required", false)
		}
		if _, err := searcher.Usage.RecordDelivery(ctx, memoryID, consumerSession, now); err != nil {
			return SearchItem{}, err
		}
	}
	return item, nil
}

func resultBytes(items []SearchItem) int {
	total := 0
	for _, item := range items {
		encoded, _ := json.Marshal(item)
		total += len(encoded)
	}
	return total
}

type MemoryIndex struct{ Items []Candidate }

func (index MemoryIndex) Search(ctx context.Context, matchExpression string, limit int) ([]Candidate, error) {
	terms := strings.Fields(strings.ReplaceAll(matchExpression, "\"", ""))
	candidates := make([]Candidate, 0, len(index.Items))
	for _, item := range index.Items {
		haystack := strings.ToLower(item.Revision.Title + " " + item.Revision.Content + " " + strings.Join(item.Revision.Tags, " "))
		matched := true
		for _, term := range terms {
			if !strings.Contains(haystack, strings.ToLower(term)) {
				matched = false
				break
			}
		}
		if matched {
			if item.RawBM25 == 0 {
				item.RawBM25 = -1
			}
			item.Score = ScoreMemory(item.RawBM25, item.Revision.CreatedAt, time.Now().UTC(), 0)
			candidates = append(candidates, item)
		}
	}
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, nil
}
func (index MemoryIndex) Get(ctx context.Context, memoryID string) (Candidate, error) {
	for _, item := range index.Items {
		if item.Memory.ID == memoryID {
			return item, nil
		}
	}
	return Candidate{}, domain.NewError(domain.CodeNotFound, "memory not found", false)
}
