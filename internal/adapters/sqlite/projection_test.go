package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

func TestProjectionStoreDurableOutboxAndReadiness(t *testing.T) {
	database := openTestDB(t)
	const scopeID = "018f1f61-7b5c-7abc-8def-1123456789ab"
	seedEligibleMemory(t, database, "018f1f61-7b5c-7abc-8def-0123456789ab", "projection", domain.TrustVerified, domain.LifecycleActive)
	store := NewProjectionStore(database, t.TempDir())
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	if err := NewRepository(database).WithTx(context.Background(), func(tx ports.MemoryTx) error {
		return tx.AppendOutbox(context.Background(), ports.OutboxEvent{ScopeID: scopeID, RevisionWatermark: 0, CreatedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := store.LoadScope(context.Background(), scopeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scope.Memories) != 1 || scope.TargetPath == "" {
		t.Fatalf("scope = %+v", scope)
	}
	pending, err := store.PendingProjection(context.Background(), scopeID, now)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if err := store.SetProjectionState(context.Background(), scopeID, "ready", "sha256:test", 0); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ProjectionFingerprint(context.Background(), scopeID); err != nil || got != "sha256:test" {
		t.Fatalf("fingerprint=%q err=%v", got, err)
	}
	if err := store.AcknowledgeProjection(context.Background(), scopeID, 0); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingProjection(context.Background(), scopeID, now)
	if err != nil || len(pending) != 0 {
		t.Fatalf("ack pending=%+v err=%v", pending, err)
	}
	if err := store.SetProjectionState(context.Background(), scopeID, "blocked", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.ProjectionReady(context.Background()); err == nil {
		t.Fatal("blocked projection reported ready")
	}
}
