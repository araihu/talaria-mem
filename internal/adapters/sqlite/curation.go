package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite/sqlc"
	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type CurationStore struct {
	database *DB
}

// CurationQueueHealth is intentionally smaller than a job. It contains only
// aggregate queue metadata safe for status and doctor output.
type CurationQueueHealth struct {
	QueueDepth     int64
	Running        int64
	OldestAt       time.Time
	LastErrorClass curation.ErrorClass
}

func NewCurationStore(database *DB) *CurationStore {
	return &CurationStore{database: database}
}

func (store *CurationStore) Enqueue(ctx context.Context, job curation.Job) (curation.Job, bool, error) {
	if err := validateEnqueueJob(job); err != nil {
		return curation.Job{}, false, err
	}
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	if job.State == "" {
		job.State = curation.JobPending
	}
	job.Priority = normalizedPriority(job)
	job.Reasons = canonicalReasons(job)
	job.Reason = highestReason(job.Reasons)
	job.SafeErrorClass = ""
	returned := curation.Job{}
	created := false
	err := store.withTx(ctx, func(tx *sql.Tx) error {
		queries := sqlc.New(tx)
		row, err := queries.ReadCurationJobByDigestWatermark(ctx, sqlc.ReadCurationJobByDigestWatermarkParams{SessionDigest: append([]byte(nil), job.SessionDigest...), SourceWatermark: job.SourceWatermark})
		switch {
		case err == nil:
			existing, decodeErr := decodeCurationJobByDigest(row)
			if decodeErr != nil {
				return decodeErr
			}
			if existing.WorkspaceID != job.WorkspaceID {
				return domain.NewError(domain.CodeValidation, "curation identity belongs to another workspace", false)
			}
			if existing.State == curation.JobComplete || existing.State == curation.JobTerminal {
				returned = existing
				return nil
			}
			merged := mergeCurationJobs(existing, job, now)
			if err := queries.UpdateCurationJobCoalesced(ctx, sqlc.UpdateCurationJobCoalescedParams{
				Reason:                  string(merged.Reason),
				ReasonFlags:             encodeReasons(merged.Reasons),
				Priority:                int64(merged.Priority),
				ThreadLocatorCiphertext: cloneBytes(merged.ThreadLocatorCiphertext),
				SnapshotCiphertext:      cloneBytes(merged.SnapshotCiphertext),
				ExpiresAt:               formatTimestamp(merged.ExpiresAt),
				UpdatedAt:               formatTimestamp(merged.UpdatedAt),
				ID:                      merged.ID,
			}); err != nil {
				return domain.MapSQLiteError(err)
			}
			returned = merged
			return nil
		case errors.Is(err, sql.ErrNoRows):
			created = true
			if err := queries.InsertCurationJob(ctx, sqlc.InsertCurationJobParams{
				ID:                      job.ID,
				WorkspaceID:             job.WorkspaceID,
				Reason:                  string(job.Reason),
				ReasonFlags:             encodeReasons(job.Reasons),
				Priority:                int64(job.Priority),
				SessionDigest:           cloneBytes(job.SessionDigest),
				SourceWatermark:         job.SourceWatermark,
				ThreadLocatorCiphertext: cloneBytes(job.ThreadLocatorCiphertext),
				SnapshotCiphertext:      cloneBytes(job.SnapshotCiphertext),
				State:                   string(job.State),
				AttemptCount:            int64(job.AttemptCount),
				NextAttemptAt:           nullableTimestamp(job.NextAttemptAt),
				ExpiresAt:               formatTimestamp(job.ExpiresAt),
				ProviderName:            job.ProviderName,
				SafeErrorClass:          "",
				CreatedAt:               formatTimestamp(job.CreatedAt),
				UpdatedAt:               formatTimestamp(job.UpdatedAt),
			}); err != nil {
				return domain.MapSQLiteError(err)
			}
			returned = job
			return nil
		default:
			return domain.MapSQLiteError(err)
		}
	})
	if err != nil {
		return curation.Job{}, false, err
	}
	returned.Reasons = append([]curation.Reason(nil), returned.Reasons...)
	return returned, created, nil
}

func (store *CurationStore) ClaimNext(ctx context.Context, now time.Time) (curation.Job, bool, error) {
	if store == nil || store.database == nil || store.database.sql == nil {
		return curation.Job{}, false, domain.NewError(domain.CodeUnavailable, "SQLite curation store unavailable", true)
	}
	now = now.UTC()
	var claimed curation.Job
	found := false
	err := store.withTx(ctx, func(tx *sql.Tx) error {
		queries := sqlc.New(tx)
		row, err := queries.ReadClaimableCurationJob(ctx, sqlc.ReadClaimableCurationJobParams{NextAttemptAt: sql.NullString{String: formatTimestamp(now), Valid: true}, ExpiresAt: formatTimestamp(now)})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return domain.MapSQLiteError(err)
		}
		found = true
		rows, err := queries.ClaimCurationJob(ctx, sqlc.ClaimCurationJobParams{UpdatedAt: formatTimestamp(now), ID: row.ID, NextAttemptAt: sql.NullString{String: formatTimestamp(now), Valid: true}, ExpiresAt: formatTimestamp(now)})
		if err != nil {
			return domain.MapSQLiteError(err)
		}
		if rows != 1 {
			found = false
			return nil
		}
		claimed, err = decodeClaimableCurationJob(row)
		if err != nil {
			return err
		}
		claimed.State = curation.JobRunning
		claimed.AttemptCount++
		claimed.NextAttemptAt = nil
		claimed.UpdatedAt = now
		return nil
	})
	if err != nil || !found {
		return curation.Job{}, false, err
	}
	return claimed, true, nil
}

func (store *CurationStore) Retry(ctx context.Context, id string, attempt int, next time.Time, class curation.ErrorClass) error {
	if id == "" || attempt < 1 || !class.Valid() || !class.Retryable() || next.IsZero() {
		return domain.NewError(domain.CodeValidation, "invalid curation retry", false)
	}
	queries, err := store.queries()
	if err != nil {
		return err
	}
	rows, err := queries.RetryCurationJob(ctx, sqlc.RetryCurationJobParams{AttemptCount: int64(attempt), NextAttemptAt: sql.NullString{String: formatTimestamp(next.UTC()), Valid: true}, SafeErrorClass: string(class), UpdatedAt: formatTimestamp(time.Now().UTC()), ID: id})
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	if rows != 1 {
		return domain.NewError(domain.CodeRevisionConflict, "curation job is not running", false)
	}
	return nil
}

func (store *CurationStore) Finish(ctx context.Context, id string, state curation.JobState, class curation.ErrorClass) error {
	if id == "" || (state != curation.JobComplete && state != curation.JobTerminal) || (class != "" && !class.Valid()) {
		return domain.NewError(domain.CodeValidation, "invalid curation completion", false)
	}
	queries, err := store.queries()
	if err != nil {
		return err
	}
	rows, err := queries.FinishCurationJob(ctx, sqlc.FinishCurationJobParams{State: string(state), SafeErrorClass: string(class), UpdatedAt: formatTimestamp(time.Now().UTC()), ID: id})
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	if rows != 1 {
		return domain.NewError(domain.CodeRevisionConflict, "curation job is not running", false)
	}
	return nil
}

func (store *CurationStore) PurgeExpired(ctx context.Context, now time.Time) (int64, error) {
	queries, err := store.queries()
	if err != nil {
		return 0, err
	}
	rows, err := queries.PurgeExpiredCurationJobs(ctx, formatTimestamp(now.UTC()))
	if err != nil {
		return 0, domain.MapSQLiteError(err)
	}
	return rows, nil
}

func (store *CurationStore) IncrementPrompt(ctx context.Context, counter curation.SessionCounter) (curation.SessionCounter, error) {
	if err := validateSessionCounter(counter); err != nil {
		return curation.SessionCounter{}, err
	}
	if store == nil || store.database == nil || store.database.sql == nil {
		return curation.SessionCounter{}, domain.NewError(domain.CodeUnavailable, "SQLite curation store unavailable", true)
	}
	var result curation.SessionCounter
	err := store.withTx(ctx, func(tx *sql.Tx) error {
		queries := sqlc.New(tx)
		existing, err := queries.ReadCurationSessionCounter(ctx, cloneBytes(counter.SessionDigest))
		if errors.Is(err, sql.ErrNoRows) {
			// Insert path below.
		} else if err != nil {
			return domain.MapSQLiteError(err)
		} else if expiresAt, parseErr := parseTimestamp(existing.ExpiresAt); parseErr == nil && expiresAt.After(time.Now().UTC()) {
			// Existing unexpired counter is incremented by the SQL upsert.
		} else if _, err := tx.ExecContext(ctx, `DELETE FROM curation_session_counters WHERE session_digest = ?`, counter.SessionDigest); err != nil {
			return domain.MapSQLiteError(err)
		}
		if err := queries.IncrementCurationSessionCounter(ctx, sqlc.IncrementCurationSessionCounterParams{SessionDigest: cloneBytes(counter.SessionDigest), LastWatermark: counter.LastWatermark, ExpiresAt: formatTimestamp(counter.ExpiresAt.UTC())}); err != nil {
			return domain.MapSQLiteError(err)
		}
		row, err := queries.ReadCurationSessionCounter(ctx, cloneBytes(counter.SessionDigest))
		if err != nil {
			return domain.MapSQLiteError(err)
		}
		expiresAt, err := parseTimestamp(row.ExpiresAt)
		if err != nil {
			return err
		}
		result = curation.SessionCounter{SessionDigest: cloneBytes(row.SessionDigest), PromptCount: row.PromptCount, LastWatermark: row.LastWatermark, ExpiresAt: expiresAt}
		return nil
	})
	if err != nil {
		return curation.SessionCounter{}, err
	}
	return result, nil
}

func (store *CurationStore) EndSession(ctx context.Context, sessionDigest []byte) error {
	if len(sessionDigest) == 0 || len(sessionDigest) > 64 {
		return domain.NewError(domain.CodeValidation, "invalid curation session digest", false)
	}
	queries, err := store.queries()
	if err != nil {
		return err
	}
	return domain.MapSQLiteError(queries.DeleteCurationSessionCounter(ctx, cloneBytes(sessionDigest)))
}

// Health reads aggregate queue metadata without selecting payload, workspace,
// session digest, provider credential, or candidate text columns.
func (store *CurationStore) Health(ctx context.Context, now time.Time) (CurationQueueHealth, error) {
	if store == nil || store.database == nil || store.database.sql == nil {
		return CurationQueueHealth{}, domain.NewError(domain.CodeUnavailable, "SQLite curation store unavailable", true)
	}
	now = now.UTC()
	var health CurationQueueHealth
	var oldest, lastError sql.NullString
	err := store.database.sql.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN state IN ('pending', 'retry_wait') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state = 'running' THEN 1 ELSE 0 END), 0),
			MIN(CASE WHEN state IN ('pending', 'retry_wait', 'running') THEN created_at END),
			COALESCE((SELECT safe_error_class FROM curation_jobs WHERE safe_error_class <> '' ORDER BY updated_at DESC LIMIT 1), '')
		FROM curation_jobs
		WHERE expires_at > ? AND state IN ('pending', 'retry_wait', 'running')`, formatTimestamp(now)).Scan(&health.QueueDepth, &health.Running, &oldest, &lastError)
	if err != nil {
		return CurationQueueHealth{}, domain.MapSQLiteError(err)
	}
	if oldest.Valid && oldest.String != "" {
		value, err := parseTimestamp(oldest.String)
		if err != nil {
			return CurationQueueHealth{}, domain.NewError(domain.CodeUnavailable, "curation queue metadata is invalid", false)
		}
		health.OldestAt = value
	}
	if lastError.Valid {
		class := curation.ErrorClass(lastError.String)
		if class.Valid() {
			health.LastErrorClass = class
		}
	}
	return health, nil
}

func (store *CurationStore) queries() (*sqlc.Queries, error) {
	if store == nil || store.database == nil || store.database.sql == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "SQLite curation store unavailable", true)
	}
	return sqlc.New(store.database.sql), nil
}

func (store *CurationStore) withTx(ctx context.Context, operation func(*sql.Tx) error) error {
	if store == nil || store.database == nil || store.database.sql == nil {
		return domain.NewError(domain.CodeUnavailable, "SQLite curation store unavailable", true)
	}
	return store.database.withTx(ctx, operation)
}

func validateEnqueueJob(job curation.Job) error {
	if job.WorkspaceID == "" || !job.Reason.Valid() || job.Reason == curation.ReasonInline || job.SourceWatermark < 0 || len(job.SessionDigest) == 0 || len(job.SessionDigest) > 64 {
		return domain.NewError(domain.CodeValidation, "invalid curation job", false)
	}
	if job.Priority != 0 && job.Priority != curation.PriorityForReason(job.Reason) {
		return domain.NewError(domain.CodeValidation, "invalid curation priority", false)
	}
	if len(job.ThreadLocatorCiphertext) > 131072 || len(job.SnapshotCiphertext) > 262144 || job.ExpiresAt.IsZero() {
		return domain.NewError(domain.CodeValidation, "invalid curation payload or expiry", false)
	}
	if job.State != "" && !job.State.Valid() {
		return domain.NewError(domain.CodeValidation, "invalid curation job state", false)
	}
	return nil
}

func validateSessionCounter(counter curation.SessionCounter) error {
	if len(counter.SessionDigest) == 0 || len(counter.SessionDigest) > 64 || counter.LastWatermark < 0 || counter.ExpiresAt.IsZero() {
		return domain.NewError(domain.CodeValidation, "invalid curation session counter", false)
	}
	return nil
}

func mergeCurationJobs(existing, incoming curation.Job, now time.Time) curation.Job {
	merged := existing
	merged.Reasons = mergeReasons(existing.Reasons, incoming.Reasons)
	merged.Reason = highestReason(merged.Reasons)
	merged.Priority = maxInt(existing.Priority, incoming.Priority)
	if merged.Priority == 0 {
		merged.Priority = curation.PriorityForReason(merged.Reason)
	}
	merged.ThreadLocatorCiphertext = cloneBytes(incoming.ThreadLocatorCiphertext)
	merged.SnapshotCiphertext = cloneBytes(incoming.SnapshotCiphertext)
	if merged.State == curation.JobRetryWait {
		merged.State = curation.JobPending
		merged.NextAttemptAt = nil
	}
	if incoming.ExpiresAt.After(merged.ExpiresAt) {
		merged.ExpiresAt = incoming.ExpiresAt
	}
	merged.UpdatedAt = now
	return merged
}

func normalizedPriority(job curation.Job) int {
	if job.Priority > curation.PriorityForReason(job.Reason) {
		return job.Priority
	}
	return curation.PriorityForReason(job.Reason)
}

func canonicalReasons(job curation.Job) []curation.Reason {
	reasons := append([]curation.Reason(nil), job.Reasons...)
	if len(reasons) == 0 {
		reasons = []curation.Reason{job.Reason}
	}
	return mergeReasons(nil, reasons)
}

func mergeReasons(left, right []curation.Reason) []curation.Reason {
	seen := make(map[curation.Reason]struct{}, len(left)+len(right))
	for _, reason := range append(append([]curation.Reason(nil), left...), right...) {
		if reason.Valid() && reason != curation.ReasonInline {
			seen[reason] = struct{}{}
		}
	}
	result := make([]curation.Reason, 0, len(seen))
	for reason := range seen {
		result = append(result, reason)
	}
	sort.Slice(result, func(i, j int) bool {
		return curation.PriorityForReason(result[i]) < curation.PriorityForReason(result[j])
	})
	return result
}

func highestReason(reasons []curation.Reason) curation.Reason {
	var highest curation.Reason
	for _, reason := range reasons {
		if curation.PriorityForReason(reason) > curation.PriorityForReason(highest) {
			highest = reason
		}
	}
	return highest
}

func encodeReasons(reasons []curation.Reason) string {
	values := make([]string, len(reasons))
	for index, reason := range reasons {
		values[index] = string(reason)
	}
	return strings.Join(values, "|")
}

func decodeReasons(value string) ([]curation.Reason, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, "|")
	result := make([]curation.Reason, 0, len(parts))
	for _, part := range parts {
		reason := curation.Reason(part)
		if !reason.Valid() || reason == curation.ReasonInline {
			return nil, fmt.Errorf("invalid stored curation reason flags")
		}
		result = append(result, reason)
	}
	return mergeReasons(nil, result), nil
}

func decodeCurationJobByDigest(row sqlc.ReadCurationJobByDigestWatermarkRow) (curation.Job, error) {
	return decodeCurationJobValues(row.ID, row.WorkspaceID, row.Reason, row.ReasonFlags, row.Priority, row.SessionDigest, row.SourceWatermark, row.ThreadLocatorCiphertext, row.SnapshotCiphertext, row.State, row.AttemptCount, row.NextAttemptAt, row.ExpiresAt, row.ProviderName, row.SafeErrorClass, row.CreatedAt, row.UpdatedAt)
}

func decodeClaimableCurationJob(row sqlc.ReadClaimableCurationJobRow) (curation.Job, error) {
	return decodeCurationJobValues(row.ID, row.WorkspaceID, row.Reason, row.ReasonFlags, row.Priority, row.SessionDigest, row.SourceWatermark, row.ThreadLocatorCiphertext, row.SnapshotCiphertext, row.State, row.AttemptCount, row.NextAttemptAt, row.ExpiresAt, row.ProviderName, row.SafeErrorClass, row.CreatedAt, row.UpdatedAt)
}

func decodeCurationJobValues(id, workspaceID, reasonValue, reasonFlags string, priority int64, sessionDigest []byte, sourceWatermark int64, locator, snapshot []byte, stateValue string, attemptCount int64, nextAttempt sql.NullString, expiresValue, providerName, safeErrorClass, createdValue, updatedValue string) (curation.Job, error) {
	reasons, err := decodeReasons(reasonFlags)
	if err != nil {
		return curation.Job{}, domain.NewError(domain.CodeUnavailable, "stored curation reason flags are invalid", false)
	}
	if len(reasons) == 0 {
		reasons = []curation.Reason{curation.Reason(reasonValue)}
	}
	createdAt, err := parseTimestamp(createdValue)
	if err != nil {
		return curation.Job{}, err
	}
	updatedAt, err := parseTimestamp(updatedValue)
	if err != nil {
		return curation.Job{}, err
	}
	expiresAt, err := parseTimestamp(expiresValue)
	if err != nil {
		return curation.Job{}, err
	}
	job := curation.Job{ID: id, WorkspaceID: workspaceID, Reason: curation.Reason(reasonValue), Reasons: reasons, Priority: int(priority), SessionDigest: cloneBytes(sessionDigest), SourceWatermark: sourceWatermark, ThreadLocatorCiphertext: cloneBytes(locator), SnapshotCiphertext: cloneBytes(snapshot), State: curation.JobState(stateValue), AttemptCount: int(attemptCount), ExpiresAt: expiresAt, ProviderName: providerName, SafeErrorClass: curation.ErrorClass(safeErrorClass), CreatedAt: createdAt, UpdatedAt: updatedAt}
	if nextAttempt.Valid {
		next, err := parseTimestamp(nextAttempt.String)
		if err != nil {
			return curation.Job{}, err
		}
		job.NextAttemptAt = &next
	}
	return job, nil
}

func nullableTimestamp(value *time.Time) sql.NullString {
	if value == nil || value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTimestamp(value.UTC()), Valid: true}
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
