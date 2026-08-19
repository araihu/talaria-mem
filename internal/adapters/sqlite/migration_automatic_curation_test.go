package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func TestAutomaticCurationSchemaSupportsGeneratedTrustAndQueue(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	var generatedFingerprint int
	if err := database.SQL().QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('memories') WHERE name = 'generated_fingerprint'`).Scan(&generatedFingerprint); err != nil {
		t.Fatal(err)
	}
	if generatedFingerprint != 1 {
		t.Fatal("generated_fingerprint column missing")
	}
	for _, table := range []string{"curation_jobs", "curation_session_counters"} {
		var count int
		if err := database.SQL().QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d", table, count)
		}
	}
	_, err := database.SQL().ExecContext(ctx, `
		INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
		VALUES ('018f1f61-7b5c-7abc-8def-1123456789ab', 'generated-workspace', 0, '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.SQL().ExecContext(ctx, `
		INSERT INTO memories(id, workspace_id, user_global, kind, trust, lifecycle, generated_fingerprint, pinned, created_at, updated_at)
		VALUES ('018f1f61-7b5c-7abc-8def-0123456789ab', '018f1f61-7b5c-7abc-8def-1123456789ab', 0, 'state', 'generated', 'active', 'fingerprint', 0, '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.SQL().ExecContext(ctx, `
		INSERT INTO memory_revisions(id, memory_id, revision_number, kind, title, content, tags_json, trust, lifecycle, created_at)
		VALUES ('018f1f61-7b5c-7abc-8def-0123456789ac', '018f1f61-7b5c-7abc-8def-0123456789ab', 1, 'state', 'generated title', 'generated body', '[]', 'generated', 'active', '2026-08-15T00:00:00.000000000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(ctx, `UPDATE memories SET current_revision_id = '018f1f61-7b5c-7abc-8def-0123456789ac' WHERE id = '018f1f61-7b5c-7abc-8def-0123456789ab'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(ctx, `INSERT INTO memory_fts(title, content, tags, memory_id) VALUES ('generated title', 'generated body', '[]', '018f1f61-7b5c-7abc-8def-0123456789ab')`); err != nil {
		t.Fatal(err)
	}
	var ftsCount int
	if err := database.SQL().QueryRowContext(ctx, `SELECT count(*) FROM memory_fts WHERE memory_id = '018f1f61-7b5c-7abc-8def-0123456789ab'`).Scan(&ftsCount); err != nil {
		t.Fatal(err)
	}
	if ftsCount != 1 {
		t.Fatalf("generated FTS count = %d, want 1", ftsCount)
	}
	_, err = database.SQL().ExecContext(ctx, `INSERT INTO curation_jobs(id, workspace_id, reason, priority, session_digest, source_watermark, state, expires_at, created_at, updated_at) VALUES ('job', '018f1f61-7b5c-7abc-8def-1123456789ab', 'periodic', 1, X'01', 10, 'pending', '2026-08-16T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(ctx, `INSERT INTO curation_jobs(id, workspace_id, reason, priority, session_digest, source_watermark, state, expires_at, created_at, updated_at) VALUES ('job-duplicate', '018f1f61-7b5c-7abc-8def-1123456789ab', 'periodic', 1, X'01', 10, 'pending', '2026-08-16T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`); err == nil {
		t.Fatal("duplicate session watermark accepted")
	}
	_ = sql.ErrNoRows
}

func TestAutomaticCurationDownMigrationGuardsGeneratedRowsAndActiveJobs(t *testing.T) {
	tests := []struct {
		name string
		seed func(*testing.T, *DB)
	}{
		{
			name: "generated-memory",
			seed: func(t *testing.T, database *DB) {
				seedGeneratedMigrationMemory(t, database)
			},
		},
		{
			name: "active-job",
			seed: func(t *testing.T, database *DB) {
				ctx := context.Background()
				_, err := database.SQL().ExecContext(ctx, `INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at) VALUES ('018f1f61-7b5c-7abc-8def-1123456789ac', 'job-workspace', 0, '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`)
				if err != nil {
					t.Fatal(err)
				}
				_, err = database.SQL().ExecContext(ctx, `INSERT INTO curation_jobs(id, workspace_id, reason, priority, session_digest, source_watermark, state, expires_at, created_at, updated_at) VALUES ('job-active', '018f1f61-7b5c-7abc-8def-1123456789ac', 'periodic', 1, X'02', 1, 'running', '2026-08-16T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`)
				if err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openTestDB(t)
			test.seed(t, database)
			if err := applyDownMigrations(context.Background(), database.SQL()); err == nil {
				t.Fatal("down migration accepted protected automatic-curation state")
			}
			var version int
			var dirty bool
			if err := database.SQL().QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
				t.Fatal(err)
			}
			if version != int(embeddedMigrationTarget) || dirty {
				t.Fatalf("guard failure changed migration authority: version=%d dirty=%v", version, dirty)
			}
		})
	}
}

func seedGeneratedMigrationMemory(t *testing.T, database *DB) {
	t.Helper()
	ctx := context.Background()
	workspaceID := "018f1f61-7b5c-7abc-8def-1123456789ad"
	memoryID := "018f1f61-7b5c-7abc-8def-0123456789ae"
	revisionID := "018f1f61-7b5c-7abc-8def-0123456789af"
	if _, err := database.SQL().ExecContext(ctx, `INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at) VALUES (?, 'generated-memory-workspace', 0, '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(ctx, `INSERT INTO memories(id, workspace_id, user_global, kind, trust, lifecycle, generated_fingerprint, pinned, created_at, updated_at) VALUES (?, ?, 0, 'state', 'generated', 'active', 'guard-fingerprint', 0, '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`, memoryID, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(ctx, `INSERT INTO memory_revisions(id, memory_id, revision_number, kind, title, content, tags_json, trust, lifecycle, created_at) VALUES (?, ?, 1, 'state', 'generated', 'body', '[]', 'generated', 'active', '2026-08-15T00:00:00.000000000Z')`, revisionID, memoryID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL().ExecContext(ctx, `UPDATE memories SET current_revision_id = ? WHERE id = ?`, revisionID, memoryID); err != nil {
		t.Fatal(err)
	}
	if err := NewRepository(database).RebuildFTS(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.SQL().QueryRowContext(ctx, `SELECT count(*) FROM memory_fts WHERE memory_id = ?`, memoryID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("generated FTS count = %d", count)
	}
}
