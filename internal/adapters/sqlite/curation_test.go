package sqlite

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/curation"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

func TestCurationStoreEnqueueCoalescesPriorityAndKeepsPayloadOpaque(t *testing.T) {
	database := openTestDB(t)
	workspaceID := seedCurationWorkspace(t, database, "enqueue")
	cipher := newTestCurationCipher(t)
	metadata := []byte("workspace=" + workspaceID)
	locator, err := cipher.SealLocator(context.Background(), "job-1", metadata, []byte("plaintext-canary-locator"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := cipher.SealSnapshot(context.Background(), "job-1", metadata, []byte("plaintext-canary-snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(24 * time.Hour)
	store := NewCurationStore(database)
	first, created, err := store.Enqueue(context.Background(), curation.Job{ID: "job-1", WorkspaceID: workspaceID, Reason: curation.ReasonPeriodic, SessionDigest: bytes.Repeat([]byte{1}, 32), SourceWatermark: 10, ThreadLocatorCiphertext: locator, SnapshotCiphertext: snapshot, ExpiresAt: expires})
	if err != nil || !created {
		t.Fatalf("first enqueue = %+v created=%v err=%v", first, created, err)
	}
	second, created, err := store.Enqueue(context.Background(), curation.Job{ID: "job-2", WorkspaceID: workspaceID, Reason: curation.ReasonSessionEnd, SessionDigest: bytes.Repeat([]byte{1}, 32), SourceWatermark: 10, ThreadLocatorCiphertext: []byte("new-locator"), SnapshotCiphertext: []byte("new-snapshot"), ExpiresAt: expires.Add(time.Hour)})
	if err != nil || created {
		t.Fatalf("second enqueue = %+v created=%v err=%v", second, created, err)
	}
	third, created, err := store.Enqueue(context.Background(), curation.Job{ID: "job-3", WorkspaceID: workspaceID, Reason: curation.ReasonPreCompact, SessionDigest: bytes.Repeat([]byte{1}, 32), SourceWatermark: 10, ThreadLocatorCiphertext: []byte("latest-locator"), SnapshotCiphertext: []byte("latest-snapshot"), ExpiresAt: expires.Add(2 * time.Hour)})
	if err != nil || created {
		t.Fatalf("third enqueue = %+v created=%v err=%v", third, created, err)
	}
	if third.ID != "job-1" || third.Priority != 3 || third.Reason != curation.ReasonPreCompact || len(third.Reasons) != 3 || string(third.SnapshotCiphertext) != "latest-snapshot" {
		t.Fatalf("coalesced job = %+v", third)
	}
	var storedLocator, storedSnapshot []byte
	if err := database.SQL().QueryRow(`SELECT thread_locator_ciphertext, snapshot_ciphertext FROM curation_jobs WHERE id = 'job-1'`).Scan(&storedLocator, &storedSnapshot); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(storedLocator, []byte("plaintext-canary")) || bytes.Contains(storedSnapshot, []byte("plaintext-canary")) {
		t.Fatal("plaintext canary persisted in curation job")
	}
	if err := store.Finish(context.Background(), "job-1", curation.JobComplete, ""); err == nil {
		t.Fatal("finish accepted non-running coalesced job")
	}
}

func TestCurationStoreClaimOrderingRetryFinishAndPurge(t *testing.T) {
	database := openTestDB(t)
	workspaceID := seedCurationWorkspace(t, database, "claim")
	store := NewCurationStore(database)
	now := time.Now().UTC()
	for index, reason := range []curation.Reason{curation.ReasonPeriodic, curation.ReasonPreCompact} {
		if _, _, err := store.Enqueue(context.Background(), curation.Job{ID: "job-" + string(rune('a'+index)), WorkspaceID: workspaceID, Reason: reason, SessionDigest: []byte{byte(index + 1)}, SourceWatermark: int64(index), ThreadLocatorCiphertext: []byte{1}, SnapshotCiphertext: []byte{2}, ExpiresAt: now.Add(4 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	claimed, found, err := store.ClaimNext(context.Background(), now)
	if err != nil || !found || claimed.Reason != curation.ReasonPreCompact || claimed.State != curation.JobRunning || claimed.AttemptCount != 1 {
		t.Fatalf("first claim = %+v found=%v err=%v", claimed, found, err)
	}
	if err := store.Retry(context.Background(), claimed.ID, 1, now.Add(time.Hour), curation.ErrorTimeout); err != nil {
		t.Fatal(err)
	}
	early, found, err := store.ClaimNext(context.Background(), now.Add(30*time.Minute))
	if err != nil || (found && early.ID == claimed.ID) {
		t.Fatalf("early retry claim = %+v found=%v err=%v", early, found, err)
	}
	if found {
		if err := store.Finish(context.Background(), early.ID, curation.JobComplete, ""); err != nil {
			t.Fatal(err)
		}
	}
	retried, found, err := store.ClaimNext(context.Background(), now.Add(2*time.Hour))
	if err != nil || !found || retried.ID != claimed.ID || retried.AttemptCount != 2 {
		t.Fatalf("retry claim = %+v found=%v err=%v", retried, found, err)
	}
	if err := store.Finish(context.Background(), retried.ID, curation.JobComplete, ""); err != nil {
		t.Fatal(err)
	}
	var locatorLength, snapshotLength int
	if err := database.SQL().QueryRow(`SELECT length(thread_locator_ciphertext), length(snapshot_ciphertext) FROM curation_jobs WHERE id = ?`, retried.ID).Scan(&locatorLength, &snapshotLength); err != nil {
		t.Fatal(err)
	}
	if locatorLength != 0 || snapshotLength != 0 {
		t.Fatalf("completed payload lengths = %d/%d", locatorLength, snapshotLength)
	}
	if _, err := database.SQL().Exec(`UPDATE curation_jobs SET expires_at = ?`, formatTimestamp(now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	count, err := store.PurgeExpired(context.Background(), now)
	if err != nil || count != 2 {
		t.Fatalf("purge count=%d err=%v", count, err)
	}
}

func TestCurationStoreRecoversStaleRunningJobAfterRestart(t *testing.T) {
	database := openTestDB(t)
	workspaceID := seedCurationWorkspace(t, database, "recovery")
	store := NewCurationStore(database)
	now := time.Now().UTC()
	if _, _, err := store.Enqueue(context.Background(), curation.Job{ID: "stale-job", WorkspaceID: workspaceID, Reason: curation.ReasonPeriodic, SessionDigest: []byte{7}, SourceWatermark: 1, ThreadLocatorCiphertext: []byte{1}, SnapshotCiphertext: []byte{2}, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	claimed, found, err := store.ClaimNext(context.Background(), now)
	if err != nil || !found || claimed.ID != "stale-job" {
		t.Fatalf("initial claim = %+v found=%v err=%v", claimed, found, err)
	}
	if _, err := database.SQL().Exec(`UPDATE curation_jobs SET updated_at = ? WHERE id = ?`, formatTimestamp(now.Add(-curationRunningLease-time.Second)), claimed.ID); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := store.ClaimNext(context.Background(), now)
	if err != nil || !found || recovered.ID != claimed.ID || recovered.State != curation.JobRunning || recovered.AttemptCount != 2 {
		t.Fatalf("recovered claim = %+v found=%v err=%v", recovered, found, err)
	}
}

func TestCurationStoreSessionCounterExpiryAndEnd(t *testing.T) {
	database := openTestDB(t)
	store := NewCurationStore(database)
	digest := bytes.Repeat([]byte{9}, 32)
	now := time.Now().UTC()
	first, err := store.IncrementPrompt(context.Background(), curation.SessionCounter{SessionDigest: digest, LastWatermark: 3, ExpiresAt: now.Add(time.Hour)})
	if err != nil || first.PromptCount != 1 || first.LastWatermark != 3 {
		t.Fatalf("first counter = %+v err=%v", first, err)
	}
	second, err := store.IncrementPrompt(context.Background(), curation.SessionCounter{SessionDigest: digest, LastWatermark: 2, ExpiresAt: now.Add(2 * time.Hour)})
	if err != nil || second.PromptCount != 2 || second.LastWatermark != 3 {
		t.Fatalf("second counter = %+v err=%v", second, err)
	}
	if err := store.EndSession(context.Background(), digest); err != nil {
		t.Fatal(err)
	}
	third, err := store.IncrementPrompt(context.Background(), curation.SessionCounter{SessionDigest: digest, LastWatermark: 8, ExpiresAt: now.Add(time.Hour)})
	if err != nil || third.PromptCount != 1 || third.LastWatermark != 8 {
		t.Fatalf("counter after end = %+v err=%v", third, err)
	}
	if _, err := database.SQL().Exec(`UPDATE curation_session_counters SET expires_at = ? WHERE session_digest = ?`, formatTimestamp(now.Add(-time.Minute)), digest); err != nil {
		t.Fatal(err)
	}
	fourth, err := store.IncrementPrompt(context.Background(), curation.SessionCounter{SessionDigest: digest, LastWatermark: 9, ExpiresAt: now.Add(time.Hour)})
	if err != nil || fourth.PromptCount != 1 || fourth.LastWatermark != 9 {
		t.Fatalf("counter after expiry = %+v err=%v", fourth, err)
	}
}

func TestCurationStoreHealthReturnsOnlyQueueMetadata(t *testing.T) {
	database := openTestDB(t)
	workspaceID := seedCurationWorkspace(t, database, "health")
	store := NewCurationStore(database)
	now := time.Now().UTC()
	if _, _, err := store.Enqueue(context.Background(), curation.Job{ID: "health-old", WorkspaceID: workspaceID, Reason: curation.ReasonPeriodic, SessionDigest: []byte{1}, SourceWatermark: 1, ThreadLocatorCiphertext: []byte("locator-canary"), SnapshotCiphertext: []byte("prompt-canary"), ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	claimed, found, err := store.ClaimNext(context.Background(), now)
	if err != nil || !found {
		t.Fatalf("claim = %+v found=%v err=%v", claimed, found, err)
	}
	if err := store.Retry(context.Background(), claimed.ID, 1, now.Add(time.Minute), curation.ErrorTimeout); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Enqueue(context.Background(), curation.Job{ID: "health-new", WorkspaceID: workspaceID, Reason: curation.ReasonPreCompact, SessionDigest: []byte{2}, SourceWatermark: 2, ThreadLocatorCiphertext: []byte("other-locator"), SnapshotCiphertext: []byte("other-prompt"), ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	health, err := store.Health(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if health.QueueDepth != 2 || health.Running != 0 || health.LastErrorClass != curation.ErrorTimeout {
		t.Fatalf("unexpected health: %+v", health)
	}
	if health.OldestAt.IsZero() || now.Sub(health.OldestAt) < 2*time.Minute {
		t.Fatalf("oldest job age missing: %+v", health)
	}
}

func seedCurationWorkspace(t *testing.T, database *DB, suffix string) string {
	t.Helper()
	workspaceID := "018f1f61-7b5c-7abc-8def-1123456789" + suffix[:2]
	_, err := database.SQL().Exec(`INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at) VALUES (?, ?, 0, '2026-08-15T00:00:00.000000000Z', '2026-08-15T00:00:00.000000000Z')`, workspaceID, "curation-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	return workspaceID
}

func newTestCurationCipher(t *testing.T) security.CurationCipher {
	t.Helper()
	root, err := security.GenerateRootKey()
	if err != nil {
		t.Fatal(err)
	}
	deriver, err := security.NewHKDFDeriver(root)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := security.NewCurationCipher(deriver)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}
