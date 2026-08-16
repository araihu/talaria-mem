package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

func TestReplaceFTSRowDerivesAuthoritativeEligibility(t *testing.T) {
	database := openTestDB(t)
	memoryID := "018f1f61-7b5c-7abc-8def-0123456789ab"
	seedEligibleMemory(t, database, memoryID, "canonical title", domain.TrustVerified, domain.LifecycleActive)
	repository := NewRepository(database)
	if err := repository.WithTx(context.Background(), func(tx ports.MemoryTx) error {
		return tx.ReplaceFTSRow(context.Background(), memoryID)
	}); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := database.sql.QueryRow("SELECT title FROM memory_fts WHERE memory_id = ?", memoryID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "canonical title" {
		t.Fatalf("FTS title = %q", title)
	}
	if _, err := database.sql.Exec("UPDATE memories SET trust = 'unverified' WHERE id = ?", memoryID); err != nil {
		t.Fatal(err)
	}
	if err := repository.WithTx(context.Background(), func(tx ports.MemoryTx) error {
		return tx.ReplaceFTSRow(context.Background(), memoryID)
	}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.sql.QueryRow("SELECT count(*) FROM memory_fts WHERE memory_id = ?", memoryID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("ineligible FTS row count = %d", count)
	}
}

func TestFailureRevisionEmptyStateMaterializesOpen(t *testing.T) {
	database := openTestDB(t)
	workspaceID := "018f1f61-7b5c-7abc-8def-1123456789ab"
	memoryID := "018f1f61-7b5c-7abc-8def-2123456789ab"
	revisionID := "018f1f61-7b5c-7abc-8def-3123456789ab"
	now := time.Date(2026, time.August, 15, 1, 2, 3, 4, time.UTC)
	if _, err := database.sql.Exec(`INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
		VALUES (?, 'failure-workspace', 0, ?, ?)`, workspaceID, formatTimestamp(now), formatTimestamp(now)); err != nil {
		t.Fatal(err)
	}
	revision := domain.MemoryRevision{
		ID: revisionID, MemoryID: memoryID, Number: 1, Kind: domain.MemoryKindFailure,
		Title: "failure", Content: "body", Trust: domain.TrustVerified,
		Lifecycle: domain.LifecycleActive, CreatedAt: now,
	}
	memory := domain.Memory{
		ID: memoryID, WorkspaceID: workspaceID, Kind: domain.MemoryKindFailure,
		Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := NewRepository(database).WithTx(context.Background(), func(tx ports.MemoryTx) error {
		if err := tx.CreateMemory(context.Background(), memory); err != nil {
			return err
		}
		if err := tx.CreateRevision(context.Background(), revision); err != nil {
			return err
		}
		return tx.MoveCurrentRevision(context.Background(), memoryID, "", revision)
	}); err != nil {
		t.Fatal(err)
	}
	_, got, err := NewRepository(database).ReadCurrent(context.Background(), memoryID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolutionState != domain.ResolutionOpen {
		t.Fatalf("failure resolution state = %q, want open", got.ResolutionState)
	}
}
