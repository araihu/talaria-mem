package application

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type reviewStoreFixture struct{ candidates []ReviewCandidate }

func (store reviewStoreFixture) ListUnverified(_ context.Context, _ string, after time.Time, afterID string, limit int) ([]ReviewCandidate, error) {
	result := make([]ReviewCandidate, 0, limit)
	for _, candidate := range store.candidates {
		if !after.IsZero() && (candidate.Memory.CreatedAt.Before(after) || (candidate.Memory.CreatedAt.Equal(after) && candidate.Memory.ID <= afterID)) {
			continue
		}
		result = append(result, candidate)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func TestReviewQueueKeysetOrderingAndCursor(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	first := ReviewCandidate{Memory: domain.Memory{ID: "018f1f61-7b5c-7abc-8def-0123456789ab", WorkspaceID: "workspace", CreatedAt: now}, Revision: domain.MemoryRevision{ID: "018f1f61-7b5c-7abc-8def-1123456789ab", MemoryID: "018f1f61-7b5c-7abc-8def-0123456789ab", Number: 1, Kind: domain.MemoryKindState, Trust: domain.TrustUnverified, Lifecycle: domain.LifecycleActive, Title: "first", Content: "one", CreatedAt: now}}
	second := first
	second.Memory.ID = "018f1f61-7b5c-7abc-8def-2123456789ab"
	second.Revision.ID = "018f1f61-7b5c-7abc-8def-3123456789ab"
	second.Revision.MemoryID = second.Memory.ID
	second.Revision.Title = "second"
	queue := NewReviewQueue(reviewStoreFixture{candidates: []ReviewCandidate{second, first}}, NewContentOutputGuard(allowScanner{}, nil, nil))
	page, err := queue.List(context.Background(), ReviewQuery{WorkspaceID: "workspace", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Included != 1 || page.Items[0].MemoryID != first.Memory.ID || page.Next == "" {
		t.Fatalf("first page=%+v", page)
	}
	page, err = queue.List(context.Background(), ReviewQuery{WorkspaceID: "workspace", Limit: 1, Cursor: page.Next})
	if err != nil {
		t.Fatal(err)
	}
	if page.Included != 1 || page.Items[0].MemoryID != second.Memory.ID {
		t.Fatalf("second page=%+v", page)
	}
}

func TestReviewQueueRejectsInvalidCursorAndBoundsItems(t *testing.T) {
	queue := NewReviewQueue(reviewStoreFixture{}, NewContentOutputGuard(allowScanner{}, nil, nil))
	if _, err := queue.List(context.Background(), ReviewQuery{WorkspaceID: "workspace", Cursor: "%%%"}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("cursor error=%v", err)
	}
	if _, err := queue.List(context.Background(), ReviewQuery{WorkspaceID: "workspace", Limit: MaxReviewLimit + 1}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("limit error=%v", err)
	}
}
