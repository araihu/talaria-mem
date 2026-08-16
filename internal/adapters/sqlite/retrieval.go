package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/retrieval"
)

// Index exposes the canonical SQLite FTS5 table through retrieval's narrow
// read port.  It does not duplicate ranking or eligibility; Searcher remains
// the owner of those policies.
type Index struct{ database *DB }

func NewIndex(database *DB) *Index { return &Index{database: database} }

func (index *Index) Search(ctx context.Context, matchExpression string, limit int) ([]retrieval.Candidate, error) {
	if index == nil || index.database == nil || index.database.sql == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "SQLite retrieval index unavailable", true)
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := index.database.sql.QueryContext(ctx, `
SELECT m.id, m.workspace_id, m.user_global, m.kind, m.trust, m.lifecycle,
       m.current_revision_id, m.pinned, m.created_at, m.updated_at,
       r.id, r.revision_number, r.kind, r.title, r.content, r.tags_json,
       r.resolution_state, r.trust, r.lifecycle, r.provenance_actor,
       r.provenance_source, r.provenance_labels_json, r.source_locator,
       r.created_at, bm25(memory_fts, 5.0, 1.0, 2.0, 0.0)
FROM memory_fts
JOIN memories AS m ON m.id = memory_fts.memory_id
JOIN memory_revisions AS r ON r.id = m.current_revision_id
WHERE memory_fts MATCH ?
ORDER BY bm25(memory_fts, 5.0, 1.0, 2.0, 0.0), m.id
LIMIT ?`, matchExpression, limit)
	if err != nil {
		return nil, domain.MapSQLiteError(err)
	}
	defer rows.Close()
	items := make([]retrieval.Candidate, 0, limit)
	for rows.Next() {
		item, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, domain.MapSQLiteError(err)
	}
	return items, nil
}

func (index *Index) Get(ctx context.Context, memoryID string) (retrieval.Candidate, error) {
	if index == nil || index.database == nil {
		return retrieval.Candidate{}, domain.NewError(domain.CodeUnavailable, "SQLite retrieval index unavailable", true)
	}
	memory, revision, err := NewRepository(index.database).ReadCurrent(ctx, memoryID)
	if err != nil {
		return retrieval.Candidate{}, err
	}
	return retrieval.Candidate{Memory: memory, Revision: revision, RawBM25: -1}, nil
}

type rowScanner interface{ Scan(...any) error }

func scanCandidate(row rowScanner) (retrieval.Candidate, error) {
	var (
		memoryID, kind, trust, lifecycle, currentRevisionID          string
		workspaceID                                                  sql.NullString
		userGlobal, pinned                                           int64
		createdAt, updatedAt                                         string
		revisionID                                                   string
		revisionNumber                                               int64
		revisionKind, title, content, tagsJSON                       string
		resolutionState                                              sql.NullString
		revisionTrust, revisionLifecycle                             string
		provenanceActor, provenanceSource, labelsJSON, sourceLocator string
		revisionCreatedAt                                            string
		rawBM25                                                      float64
	)
	if err := row.Scan(&memoryID, &workspaceID, &userGlobal, &kind, &trust, &lifecycle, &currentRevisionID, &pinned, &createdAt, &updatedAt, &revisionID, &revisionNumber, &revisionKind, &title, &content, &tagsJSON, &resolutionState, &revisionTrust, &revisionLifecycle, &provenanceActor, &provenanceSource, &labelsJSON, &sourceLocator, &revisionCreatedAt, &rawBM25); err != nil {
		return retrieval.Candidate{}, domain.MapSQLiteError(err)
	}
	created, err := parseTimestamp(createdAt)
	if err != nil {
		return retrieval.Candidate{}, err
	}
	updated, err := parseTimestamp(updatedAt)
	if err != nil {
		return retrieval.Candidate{}, err
	}
	revisionCreated, err := parseTimestamp(revisionCreatedAt)
	if err != nil {
		return retrieval.Candidate{}, err
	}
	var tags, labels []string
	if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
		return retrieval.Candidate{}, domain.NewError(domain.CodeUnavailable, "stored tags are invalid", false)
	}
	if err := json.Unmarshal([]byte(labelsJSON), &labels); err != nil {
		return retrieval.Candidate{}, domain.NewError(domain.CodeUnavailable, "stored provenance is invalid", false)
	}
	return retrieval.Candidate{
		Memory:   domain.Memory{ID: memoryID, WorkspaceID: workspaceID.String, UserGlobal: userGlobal == 1, Kind: domain.MemoryKind(kind), Trust: domain.Trust(trust), Lifecycle: domain.Lifecycle(lifecycle), CurrentRevisionID: currentRevisionID, Pinned: pinned == 1, CreatedAt: created, UpdatedAt: updated},
		Revision: domain.MemoryRevision{ID: revisionID, MemoryID: memoryID, Number: revisionNumber, Kind: domain.MemoryKind(revisionKind), Title: title, Content: content, Tags: tags, ResolutionState: domain.ResolutionState(resolutionState.String), Trust: domain.Trust(revisionTrust), Lifecycle: domain.Lifecycle(revisionLifecycle), Provenance: domain.Provenance{Actor: provenanceActor, Source: provenanceSource, Labels: labels, SourceLocator: sourceLocator}, CreatedAt: revisionCreated},
		RawBM25:  rawBM25,
	}, nil
}

var _ retrieval.Index = (*Index)(nil)
