package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type ReviewCandidate struct {
	Memory   domain.Memory
	Revision domain.MemoryRevision
}

type ReviewStore interface {
	ListUnverified(context.Context, string, time.Time, string, int) ([]ReviewCandidate, error)
}

type ReviewQuery struct {
	WorkspaceID string
	Limit       int
	Cursor      string
}

type ReviewPage struct {
	Version  string         `json:"version"`
	Items    []ReviewResult `json:"items"`
	Included int            `json:"included"`
	Omitted  int            `json:"omitted"`
	Next     string         `json:"next_cursor,omitempty"`
}

type reviewCursor struct {
	CreatedAt time.Time `json:"created_at"`
	MemoryID  string    `json:"memory_id"`
}

type ReviewQueue struct {
	Store ReviewStore
	Guard *ContentOutputGuard
}

func NewReviewQueue(store ReviewStore, guard *ContentOutputGuard) *ReviewQueue {
	return &ReviewQueue{Store: store, Guard: guard}
}

func EncodeReviewCursor(createdAt time.Time, memoryID string) string {
	value, _ := json.Marshal(reviewCursor{CreatedAt: createdAt.UTC(), MemoryID: memoryID})
	return base64.RawURLEncoding.EncodeToString(value)
}

func DecodeReviewCursor(value string) (time.Time, string, error) {
	if value == "" {
		return time.Time{}, "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", domain.NewError(domain.CodeValidation, "invalid review cursor", false)
	}
	var cursor reviewCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.MemoryID == "" || cursor.CreatedAt.IsZero() {
		return time.Time{}, "", domain.NewError(domain.CodeValidation, "invalid review cursor", false)
	}
	return cursor.CreatedAt.UTC(), cursor.MemoryID, nil
}

func (queue *ReviewQueue) List(ctx context.Context, query ReviewQuery) (ReviewPage, error) {
	if queue == nil || queue.Store == nil || queue.Guard == nil {
		return ReviewPage{}, domain.NewError(domain.CodeUnavailable, "review queue unavailable", true)
	}
	if query.WorkspaceID == "" {
		return ReviewPage{}, domain.NewError(domain.CodeValidation, "workspace is required", false)
	}
	limit := query.Limit
	if limit == 0 {
		limit = DefaultReviewLimit
	}
	if limit < 1 || limit > MaxReviewLimit {
		return ReviewPage{}, domain.NewError(domain.CodeValidation, "review limit is out of range", false)
	}
	afterCreatedAt, afterMemoryID, err := DecodeReviewCursor(query.Cursor)
	if err != nil {
		return ReviewPage{}, err
	}
	candidates, err := queue.Store.ListUnverified(ctx, query.WorkspaceID, afterCreatedAt, afterMemoryID, MaxReviewLimit+1)
	if err != nil {
		return ReviewPage{}, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if !candidates[i].Memory.CreatedAt.Equal(candidates[j].Memory.CreatedAt) {
			return candidates[i].Memory.CreatedAt.Before(candidates[j].Memory.CreatedAt)
		}
		return candidates[i].Memory.ID < candidates[j].Memory.ID
	})
	page := ReviewPage{Version: "talaria.review.v1", Items: make([]ReviewResult, 0, limit)}
	for _, candidate := range candidates {
		if candidate.Memory.WorkspaceID != query.WorkspaceID || candidate.Revision.Trust == domain.TrustVerified || candidate.Revision.Lifecycle != domain.LifecycleActive {
			continue
		}
		if len(page.Items) >= limit {
			page.Omitted++
			continue
		}
		fields := revisionFields(candidate.Revision)
		result, err := queue.Guard.Check(ctx, OutputRequest{Route: ports.FieldCLIRead, WorkspaceID: candidate.Memory.WorkspaceID, MemoryID: candidate.Memory.ID, RevisionID: candidate.Revision.ID, Fields: fields})
		if err != nil {
			return ReviewPage{}, err
		}
		if !result.Allowed {
			return ReviewPage{}, domain.NewError(domain.CodeQuarantine, "content quarantined", false)
		}
		item := ReviewResult{MemoryID: candidate.Memory.ID, RevisionID: candidate.Revision.ID, RevisionNumber: candidate.Revision.Number, WorkspaceID: candidate.Memory.WorkspaceID, Kind: candidate.Revision.Kind, Trust: candidate.Revision.Trust, Lifecycle: candidate.Revision.Lifecycle, Title: candidate.Revision.Title, Content: candidate.Revision.Content, Tags: append([]string(nil), candidate.Revision.Tags...), ResolutionState: candidate.Revision.ResolutionState}
		encoded, _ := json.Marshal(item)
		if len(encoded) > MaxReviewBytes {
			page.Omitted++
			continue
		}
		page.Items = append(page.Items, item)
	}
	page.Included = len(page.Items)
	if len(candidates) > 0 && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		for _, candidate := range candidates {
			if candidate.Memory.ID == last.MemoryID {
				page.Next = EncodeReviewCursor(candidate.Memory.CreatedAt, candidate.Memory.ID)
				break
			}
		}
	}
	return page, nil
}
