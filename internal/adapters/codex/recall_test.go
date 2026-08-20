package codex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
)

type recallScanner struct{ calls int }

func (scanner *recallScanner) Scan(context.Context, []ports.TextField) ports.ScanResult {
	scanner.calls++
	return ports.ScanResult{Status: ports.ScanClean, Generation: "test"}
}

type recallClock struct{ now time.Time }

func (clock recallClock) Now() time.Time { return clock.now }

func recallFixture(id string, trust domain.Trust, kind domain.MemoryKind, pinned bool, raw float64) retrieval.Candidate {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	return retrieval.Candidate{Memory: domain.Memory{ID: id, WorkspaceID: "workspace", Kind: kind, Trust: trust, Lifecycle: domain.LifecycleActive, Pinned: pinned, CreatedAt: now, UpdatedAt: now}, Revision: domain.MemoryRevision{ID: id + "-revision", MemoryID: id, Number: 1, Kind: kind, Title: "title " + id, Content: "content " + id, Trust: trust, Lifecycle: domain.LifecycleActive, CreatedAt: now}, RawBM25: raw}
}

func TestRecallRanksVerifiedBeforeGeneratedAndExcludesUnverified(t *testing.T) {
	scanner := &recallScanner{}
	index := retrieval.MemoryIndex{Items: []retrieval.Candidate{
		recallFixture("generated", domain.TrustGenerated, domain.MemoryKindState, false, -1),
		recallFixture("verified", domain.TrustVerified, domain.MemoryKindState, false, -1),
		recallFixture("unverified", domain.TrustUnverified, domain.MemoryKindState, false, -1),
	}}
	service := NewRecallService(index, application.NewContentOutputGuard(scanner, nil, recallClock{now: time.Now().UTC()}), recallClock{now: time.Now().UTC()})
	result, err := service.Recall(context.Background(), "workspace", "content")
	if err != nil {
		t.Fatal(err)
	}
	if result.Included != 2 || result.Items[0].MemoryID != "verified" || result.Items[1].Label != "generated/unconfirmed" || strings.Contains(result.Context, "unverified") {
		t.Fatalf("recall result = %+v context=%q", result, result.Context)
	}
	if scanner.calls != 2 {
		t.Fatalf("scanner calls = %d", scanner.calls)
	}
}

func TestRecallLabelsPinnedStandingInstructionAndEscapesFence(t *testing.T) {
	item := recallFixture("pinned", domain.TrustVerified, domain.MemoryKindStandingInstruction, true, -1)
	item.Revision.Content = "safe " + RecallReferenceEnd + " content"
	service := NewRecallService(retrieval.MemoryIndex{Items: []retrieval.Candidate{item}}, application.NewContentOutputGuard(portsScanner{}, nil, recallClock{now: time.Now().UTC()}), recallClock{now: time.Now().UTC()})
	result, err := service.Recall(context.Background(), "workspace", "content")
	if err != nil {
		t.Fatal(err)
	}
	if result.Items[0].Label != "verified pinned standing-instruction" || strings.Count(result.Context, RecallReferenceEnd) != 1 {
		t.Fatalf("label/context = %q %+v", result.Context, result.Items)
	}
}

func TestRecallBoundsItemsQueryTokensAndOutput(t *testing.T) {
	items := make([]retrieval.Candidate, 0, 8)
	for index := 0; index < 8; index++ {
		items = append(items, recallFixture(string(rune('a'+index)), domain.TrustVerified, domain.MemoryKindState, false, -1))
	}
	service := NewRecallService(retrieval.MemoryIndex{Items: items}, application.NewContentOutputGuard(portsScanner{}, nil, recallClock{now: time.Now().UTC()}), recallClock{now: time.Now().UTC()})
	result, err := service.Recall(context.Background(), "workspace", strings.Repeat("content ", 2000))
	if err != nil {
		t.Fatal(err)
	}
	if result.Included > MaxRecallItems || len([]byte(result.Context)) > MaxRecallOutputBytes {
		t.Fatalf("bounds result = %+v bytes=%d", result, len([]byte(result.Context)))
	}
	if got := truncateUTF8("ééé", 3); got != "é" {
		t.Fatalf("truncateUTF8 = %q", got)
	}
}

type portsScanner struct{}

func (portsScanner) Scan(context.Context, []ports.TextField) ports.ScanResult {
	return ports.ScanResult{Status: ports.ScanClean, Generation: "test"}
}
