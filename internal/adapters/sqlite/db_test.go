package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := OpenForMaintenance(context.Background(), filepath.Join(directory, "talaria.db"), testManagedPathPolicy{}, testPrepareManagedDatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	return database
}

func TestSchema(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	for pragma, want := range map[string]int{
		"foreign_keys":  1,
		"secure_delete": 1,
		"auto_vacuum":   2,
	} {
		var got int
		if err := database.SQL().QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %d, want %d", pragma, got, want)
		}
	}
	var journalMode string
	if err := database.SQL().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("PRAGMA journal_mode = %q, want WAL", journalMode)
	}

	wantTables := []string{
		"workspaces", "workspace_bindings", "workspace_redirects", "memory_aliases",
		"memories", "memory_revisions", "memory_fts", "usage_daily", "usage_lifetime",
		"outbox", "projection_state", "skill_promotions", "deletion_receipts",
		"purge_operations", "managed_backups", "idempotency_requests", "migration_journal",
		"rule_activation_journal", "activation_epoch_audit",
	}
	for _, table := range wantTables {
		var count int
		if err := database.SQL().QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_master WHERE (type = 'table' OR type = 'view') AND name = ?", table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("table %s count = %d, want 1", table, count)
		}
	}
}

func TestSchemaResolutionStateConstraint(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	seedMemory := func(id, kind string) {
		t.Helper()
		_, err := database.SQL().ExecContext(ctx, `
			INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
			VALUES ('018f1f61-7b5c-7abc-8def-1123456789ab', 'workspace', 0,
			'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')
			ON CONFLICT(id) DO NOTHING`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.SQL().ExecContext(ctx, `
			INSERT INTO memories(id, workspace_id, user_global, kind, trust, lifecycle, created_at, updated_at)
			VALUES (?, '018f1f61-7b5c-7abc-8def-1123456789ab', 0, ?, 'verified', 'active',
			'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`, id, kind)
		if err != nil {
			t.Fatal(err)
		}
	}

	seedMemory("018f1f61-7b5c-7abc-8def-0123456789ab", "failure")
	if _, err := database.SQL().ExecContext(ctx, `
		INSERT INTO memory_revisions(id, memory_id, revision_number, kind, title, content,
		tags_json, trust, lifecycle, created_at)
		VALUES ('018f1f61-7b5c-7abc-8def-0123456789ac', '018f1f61-7b5c-7abc-8def-0123456789ab',
		1, 'failure', 'title', 'body', '[]', 'verified', 'active',
		'2026-08-15T00:00:00.000000000Z')`); err == nil {
		t.Fatal("failure revision without resolution_state accepted")
	}

	seedMemory("018f1f61-7b5c-7abc-8def-0123456789ad", "state")
	if _, err := database.SQL().ExecContext(ctx, `
		INSERT INTO memory_revisions(id, memory_id, revision_number, kind, title, content,
		tags_json, resolution_state, trust, lifecycle, created_at)
		VALUES ('018f1f61-7b5c-7abc-8def-0123456789ae', '018f1f61-7b5c-7abc-8def-0123456789ad',
		1, 'state', 'title', 'body', '[]', 'open', 'verified', 'active',
		'2026-08-15T00:00:00.000000000Z')`); err == nil {
		t.Fatal("non-failure revision with resolution_state accepted")
	}
}

func TestSchemaRestartPersistence(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "restart.db")
	database, err := OpenForMaintenance(context.Background(), path, testManagedPathPolicy{}, testPrepareManagedDatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.SQL().Exec(`INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
		VALUES ('018f1f61-7b5c-7abc-8def-1123456789ab', 'persisted', 0,
		'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(context.Background(), path, testManagedPathPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var name string
	if err := database.SQL().QueryRow("SELECT name FROM workspaces").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "persisted" {
		t.Fatalf("workspace after restart = %q", name)
	}
}

func TestOpenIsNonMutatingAndDoesNotCreate(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "ordinary.db")
	database, err := OpenForMaintenance(context.Background(), path, testManagedPathPolicy{}, testPrepareManagedDatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	var versionBefore int
	if err := database.sql.QueryRow("PRAGMA user_version").Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	ordinary, err := Open(context.Background(), path, testManagedPathPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ordinary.Close(); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(directory, "missing.db")
	if _, err := Open(context.Background(), missing, testManagedPathPolicy{}); err == nil {
		t.Fatal("ordinary Open created or accepted missing database")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("ordinary Open created missing path: %v", err)
	}
	var versionAfter int
	maintenance, err := OpenForMaintenance(context.Background(), path, testManagedPathPolicy{}, testPrepareManagedDatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	if err := maintenance.sql.QueryRow("PRAGMA user_version").Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if versionAfter != versionBefore {
		t.Fatalf("ordinary Open changed schema version: %d -> %d", versionBefore, versionAfter)
	}
}

func TestSchemaContractRejectsMissingNonFTSObjects(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "tampered.db")
	database, err := OpenForMaintenance(context.Background(), path, testManagedPathPolicy{}, testPrepareManagedDatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.sql.Exec("DROP TABLE outbox"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(context.Background(), path, testManagedPathPolicy{}); err == nil {
		opened.Close()
		t.Fatal("ordinary open accepted missing non-FTS table")
	}
}

func TestSchemaContractRejectsMissingNonFTSIndex(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "tampered-index.db")
	database, err := OpenForMaintenance(context.Background(), path, testManagedPathPolicy{}, testPrepareManagedDatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.sql.Exec("DROP INDEX idempotency_requests_expiry"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(context.Background(), path, testManagedPathPolicy{}); err == nil {
		opened.Close()
		t.Fatal("ordinary open accepted missing non-FTS index")
	}
}

func TestMaintenanceOpenRejectsTamperedSchemaContract(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "tampered-maintenance.db")
	database, err := OpenForMaintenance(context.Background(), path, testManagedPathPolicy{}, testPrepareManagedDatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.sql.Exec("DROP TABLE projection_state"); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := OpenForMaintenance(context.Background(), path, testManagedPathPolicy{}, testPrepareManagedDatabaseFile); err == nil {
		reopened.Close()
		t.Fatal("maintenance open accepted tampered schema contract")
	}
}

func TestBackupReconcilePhaseVocabulary(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	for _, phase := range []string{"pre_effect", "quarantine_renamed", "file_fsynced", "deleted", "inventory_rebuilt", "receipt_complete"} {
		_, err := database.sql.ExecContext(ctx, `
			INSERT INTO purge_operations(
				id, operation_identity, operation_kind, phase, receipt_digest,
				created_at, updated_at
			) VALUES (?, ?, 'backup_reconcile', ?, X'01',
				'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`,
			"018f1f61-7b5c-7abc-8def-0123456789"+phase[:2], "op-"+phase, phase)
		if err != nil {
			t.Fatalf("backup phase %s rejected: %v", phase, err)
		}
	}
	if _, err := database.sql.ExecContext(ctx, `
		INSERT INTO purge_operations(
			id, operation_identity, operation_kind, phase, receipt_digest,
			created_at, updated_at
		) VALUES ('018f1f61-7b5c-7abc-8def-0123456789ff', 'op-invalid',
			'backup_reconcile', 'effect_applied', X'01',
			'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`); err == nil {
		t.Fatal("invalid backup-reconcile phase accepted")
	}
	if _, err := database.sql.ExecContext(ctx, `
		INSERT INTO purge_operations(
			id, operation_identity, operation_kind, phase, receipt_digest,
			safe_error, created_at, updated_at
		) VALUES ('018f1f61-7b5c-7abc-8def-0123456789fe', 'op-unsafe',
			'backup_reconcile', 'pre_effect', X'01', 'line
break',
			'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`); err == nil {
		t.Fatal("unsafe backup-reconcile error accepted")
	}
}

func TestMemoryPurgePhaseVocabulary(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	phases := []string{"reserved", "purge_pending", "pre_effect", "effect_applied", "post_effect", "complete", "failed"}
	for index, phase := range phases {
		id := fmt.Sprintf("018f1f61-7b5c-7abc-8def-01234567%04x", index)
		_, err := database.sql.ExecContext(ctx, `
			INSERT INTO purge_operations(
				id, operation_identity, operation_kind, phase, receipt_digest,
				created_at, updated_at
			) VALUES (?, ?, 'memory_purge', ?, X'01',
				'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`, id, "memory-"+phase, phase)
		if err != nil {
			t.Fatalf("memory purge phase %s rejected: %v", phase, err)
		}
	}
	if _, err := database.sql.ExecContext(ctx, `
		INSERT INTO purge_operations(
			id, operation_identity, operation_kind, phase, receipt_digest,
			created_at, updated_at
		) VALUES ('018f1f61-7b5c-7abc-8def-01234567ffff', 'memory-invalid',
			'memory_purge', 'quarantine_renamed', X'01',
			'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`); err == nil {
		t.Fatal("backup-only purge phase accepted for memory purge")
	}
}

func TestFTSTokenizer(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	var schema string
	if err := database.SQL().QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'memory_fts'",
	).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "tokenize='"+domain.FTS5Tokenizer+"'") {
		t.Fatalf("FTS schema tokenizer drift: %s", schema)
	}
	var secureDelete int
	if err := database.SQL().QueryRowContext(ctx,
		"SELECT v FROM memory_fts_config WHERE k = 'secure-delete'",
	).Scan(&secureDelete); err != nil {
		t.Fatal(err)
	}
	if secureDelete != 1 {
		t.Fatalf("FTS secure-delete = %d", secureDelete)
	}
}

func TestFTSAndUnicodeDiacritic(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	seedEligibleMemory(t, database, "018f1f61-7b5c-7abc-8def-0123456789ab", "Café", domain.TrustVerified, domain.LifecycleActive)
	if err := NewRepository(database).RebuildFTS(ctx); err != nil {
		t.Fatal(err)
	}

	var memoryID string
	if err := database.SQL().QueryRowContext(ctx,
		"SELECT memory_id FROM memory_fts WHERE memory_fts MATCH ?", "cafe",
	).Scan(&memoryID); err != nil {
		t.Fatal(err)
	}
	if memoryID != "018f1f61-7b5c-7abc-8def-0123456789ab" {
		t.Fatalf("memory ID = %q", memoryID)
	}
}

func TestFTSEligibility(t *testing.T) {
	database := openTestDB(t)
	seedEligibleMemory(t, database, "018f1f61-7b5c-7abc-8def-0123456789ab", "eligible", domain.TrustVerified, domain.LifecycleActive)
	seedEligibleMemory(t, database, "018f1f61-7b5c-7abc-8def-0123456789ac", "unverified", domain.TrustUnverified, domain.LifecycleActive)
	seedEligibleMemory(t, database, "018f1f61-7b5c-7abc-8def-0123456789ad", "quarantined", domain.TrustVerified, domain.LifecycleQuarantined)

	if err := NewRepository(database).RebuildFTS(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := database.SQL().QueryContext(context.Background(), "SELECT memory_id FROM memory_fts ORDER BY memory_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 1 || ids[0] != "018f1f61-7b5c-7abc-8def-0123456789ab" {
		t.Fatalf("eligible FTS IDs = %v", ids)
	}
}

func TestIndexSearchWildcardListsEligibleRows(t *testing.T) {
	database := openTestDB(t)
	seedEligibleMemory(t, database, "018f1f61-7b5c-7abc-8def-0123456789ab", "eligible", domain.TrustVerified, domain.LifecycleActive)
	seedEligibleMemory(t, database, "018f1f61-7b5c-7abc-8def-0123456789ac", "second", domain.TrustVerified, domain.LifecycleActive)
	if err := NewRepository(database).RebuildFTS(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err := NewIndex(database).Search(context.Background(), "*", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("wildcard result count = %d, want 2", len(items))
	}
	if items[0].RawBM25 != -1 || items[1].RawBM25 != -1 {
		t.Fatalf("wildcard raw BM25 values = %v, %v; want -1", items[0].RawBM25, items[1].RawBM25)
	}
}

func TestIndexListSessionStartScopesAllCandidates(t *testing.T) {
	database := openTestDB(t)
	targetWorkspace := "018f1f61-7b5c-7abc-8def-1123456789ab"
	foreignWorkspace := "018f1f61-7b5c-7abc-8def-1123456789ac"
	seedEligibleMemoryInWorkspace(t, database, "018f1f61-7b5c-7abc-8def-0123456789ab", "target", targetWorkspace, domain.TrustVerified, domain.LifecycleActive)
	pinnedID := "018f1f61-7b5c-7abc-8def-112345670000"
	for index := 0; index <= domain.MaxSessionStartItems; index++ {
		memoryID := fmt.Sprintf("018f1f61-7b5c-7abc-8def-11234567%04x", index)
		seedEligibleMemoryInWorkspace(t, database, memoryID, "target-extra", targetWorkspace, domain.TrustVerified, domain.LifecycleActive)
	}
	if _, err := database.SQL().ExecContext(context.Background(), "UPDATE memories SET kind = 'standing_instruction', pinned = 1 WHERE id = ?", pinnedID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(context.Background(), "UPDATE memory_revisions SET kind = 'standing_instruction' WHERE memory_id = ?", pinnedID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < domain.MaxSessionStartItems; index++ {
		memoryID := fmt.Sprintf("018f1f61-7b5c-7abc-8def-01234567%04x", index)
		seedEligibleMemoryInWorkspace(t, database, memoryID, "foreign", foreignWorkspace, domain.TrustVerified, domain.LifecycleActive)
	}
	seedGlobalMemory(t, database, "018f1f61-7b5c-7abc-8def-0123456789ad", "global")
	if err := NewRepository(database).RebuildFTS(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err := NewIndex(database).ListSessionStart(context.Background(), targetWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	wantCount := 2 + domain.MaxSessionStartItems + 1
	if len(items) != wantCount {
		t.Fatalf("session-start candidate count = %d, want %d scoped candidates", len(items), wantCount)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Memory.ID] = true
	}
	if !seen["018f1f61-7b5c-7abc-8def-0123456789ab"] || !seen[pinnedID] || !seen["018f1f61-7b5c-7abc-8def-0123456789ad"] {
		t.Fatalf("session-start candidates = %v", seen)
	}
}

func seedEligibleMemory(t *testing.T, database *DB, memoryID, title string, trust domain.Trust, lifecycle domain.Lifecycle) {
	seedEligibleMemoryInWorkspace(t, database, memoryID, title, "018f1f61-7b5c-7abc-8def-1123456789ab", trust, lifecycle)
}

func seedEligibleMemoryInWorkspace(t *testing.T, database *DB, memoryID, title, workspaceID string, trust domain.Trust, lifecycle domain.Lifecycle) {
	t.Helper()
	ctx := context.Background()
	revisionID := memoryID + "-revision"
	err := database.WithTx(ctx, func(tx *sql.Tx) error {
		workspaceName := "workspace-" + workspaceID[len(workspaceID)-4:]
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
		VALUES (?, ?, 0, '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')
		ON CONFLICT(id) DO NOTHING`, workspaceID, workspaceName); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memories(id, workspace_id, user_global, kind, trust, lifecycle, created_at, updated_at)
		VALUES (?, ?, 0, 'state', ?, ?, '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z');
	`, memoryID, workspaceID, trust, lifecycle); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_revisions(id, memory_id, revision_number, kind, title, content, tags_json, trust, lifecycle, created_at)
		VALUES (?, ?, 1, 'state', ?, 'body', '[]', ?, ?, '2026-08-15T00:00:00.000000000Z');
	`, revisionID, memoryID, title, trust, lifecycle); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE memories SET current_revision_id = ? WHERE id = ?", revisionID, memoryID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedGlobalMemory(t *testing.T, database *DB, memoryID, title string) {
	t.Helper()
	ctx := context.Background()
	revisionID := memoryID + "-revision"
	err := database.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO memories(id, workspace_id, user_global, kind, trust, lifecycle, created_at, updated_at)
		VALUES (?, NULL, 1, 'state', 'verified', 'active', '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`, memoryID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_revisions(id, memory_id, revision_number, kind, title, content, tags_json, trust, lifecycle, created_at)
		VALUES (?, ?, 1, 'state', ?, 'body', '[]', 'verified', 'active', '2026-08-15T00:00:00.000000000Z')`, revisionID, memoryID, title); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE memories SET current_revision_id = ? WHERE id = ?", revisionID, memoryID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestActivationJournal(t *testing.T) {
	database := openTestDB(t)
	_, err := database.SQL().ExecContext(context.Background(), `
		INSERT INTO rule_activation_journal(id, active_generation, phase, revision_watermark,
		live_mutation_started, quarantined_count, fts_removed_count, outbox_added_count,
		projected_count, updated_at)
		VALUES (1, 'rules-v1', 'not-a-phase', 0, 0, 0, 0, 0, 0, '2026-08-15T00:00:00.000000000Z')
	`)
	if err == nil {
		t.Fatal("invalid activation phase accepted")
	}
}

type sqliteFullError struct{ calls *int }

func (err sqliteFullError) Error() string { return "database or disk is full" }
func (err sqliteFullError) Code() int {
	*err.calls++
	return 13
}

func TestSQLiteFull(t *testing.T) {
	database := openTestDB(t)
	calls := 0
	database.beforeCommit = func() error { return sqliteFullError{calls: &calls} }
	err := database.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
			VALUES ('018f1f61-7b5c-7abc-8def-1123456789ab', 'workspace', 0,
			'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`)
		return err
	})
	if !domain.IsCode(err, domain.CodeStorageFull) {
		t.Fatalf("WithTx SQLITE_FULL = %v", err)
	}
	if calls != 1 {
		t.Fatalf("SQLITE_FULL attempts = %d, want 1", calls)
	}
	var count int
	if err := database.SQL().QueryRow("SELECT count(*) FROM workspaces").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial mutation committed: %d workspaces", count)
	}
}

func TestTransaction(t *testing.T) {
	database := openTestDB(t)
	wantErr := errors.New("injected statement failure")
	err := database.WithTx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
			VALUES ('018f1f61-7b5c-7abc-8def-1123456789ab', 'workspace', 0,
			'2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTx error = %v", err)
	}
	var count int
	if err := database.SQL().QueryRow("SELECT count(*) FROM workspaces").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rollback left %d rows", count)
	}
}
