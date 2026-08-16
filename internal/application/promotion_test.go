package application

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type promotionSourceFixture struct {
	memory   domain.Memory
	revision domain.MemoryRevision
}

func (source promotionSourceFixture) ReadCurrent(context.Context, string) (domain.Memory, domain.MemoryRevision, error) {
	return source.memory, source.revision, nil
}

func promotionFixture() promotionSourceFixture {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	return promotionSourceFixture{
		memory:   domain.Memory{ID: "018f1f61-7b5c-7abc-8def-0123456789ab", WorkspaceID: "workspace", Kind: domain.MemoryKindProcedure, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: now, UpdatedAt: now},
		revision: domain.MemoryRevision{ID: "018f1f61-7b5c-7abc-8def-1123456789ab", MemoryID: "018f1f61-7b5c-7abc-8def-0123456789ab", Number: 2, Kind: domain.MemoryKindProcedure, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, Title: "Deploy", Content: "Run deploy", CreatedAt: now},
	}
}

func TestPromotionRequiresVerifiedActiveProcedureAndReceipt(t *testing.T) {
	source := promotionFixture()
	files := &managedStoreFixture{files: make(map[string][]byte)}
	service := NewPromotionService(source, NewContentOutputGuard(allowScanner{}, nil, nil), files, testClock{now: source.memory.CreatedAt})
	dryRun, err := service.DryRun(context.Background(), PromotionRequest{MemoryID: source.memory.ID, ExpectedRevisionID: source.revision.ID, OutputPath: "/tmp/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || dryRun.Receipt.ID == "" || dryRun.Receipt.ContentFingerprint == "" {
		t.Fatalf("dry-run=%+v", dryRun)
	}
	if _, err := service.Apply(context.Background(), PromotionRequest{ApplyReceipt: &dryRun.Receipt}); err != nil {
		t.Fatal(err)
	}
	if len(files.files["/tmp/SKILL.md"]) == 0 {
		t.Fatal("skill output missing")
	}
	if _, err := service.Apply(context.Background(), PromotionRequest{ApplyReceipt: &dryRun.Receipt}); err == nil {
		t.Fatal("receipt replay accepted")
	}
}

func TestPromotionRejectsChangedRevisionAndSecrets(t *testing.T) {
	source := promotionFixture()
	source.revision.Kind = domain.MemoryKindState
	service := NewPromotionService(source, NewContentOutputGuard(allowScanner{}, nil, nil), &managedStoreFixture{files: make(map[string][]byte)}, testClock{now: source.memory.CreatedAt})
	if _, err := service.DryRun(context.Background(), PromotionRequest{MemoryID: source.memory.ID, ExpectedRevisionID: source.revision.ID, OutputPath: "/tmp/SKILL.md"}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("kind error=%v", err)
	}
	source.revision.Kind = domain.MemoryKindProcedure
	service = NewPromotionService(source, NewContentOutputGuard(allowScanner{status: "finding"}, nil, nil), &managedStoreFixture{files: make(map[string][]byte)}, testClock{now: source.memory.CreatedAt})
	if _, err := service.DryRun(context.Background(), PromotionRequest{MemoryID: source.memory.ID, ExpectedRevisionID: source.revision.ID, OutputPath: "/tmp/SKILL.md"}); !domain.IsCode(err, domain.CodeQuarantine) && !domain.IsCode(err, domain.CodeSecretRefusal) {
		t.Fatalf("scanner error=%v", err)
	}
}

type testClock struct{ now time.Time }

func (clock testClock) Now() time.Time { return clock.now }
