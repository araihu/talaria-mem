package curation

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type captureSource struct {
	calls int
	value SanitizedSnapshot
	err   error
}

func (source *captureSource) Capture(context.Context, CaptureRequest) (SanitizedSnapshot, error) {
	source.calls++
	return source.value, source.err
}

type captureScanner struct {
	status   ports.ScanStatus
	findings []ports.Finding
	calls    int
}

func (scanner *captureScanner) Scan(context.Context, []ports.TextField) ports.ScanResult {
	scanner.calls++
	return ports.ScanResult{Status: scanner.status, Findings: append([]ports.Finding(nil), scanner.findings...)}
}

type captureJobStore struct {
	calls int
	job   Job
}

func (store *captureJobStore) Enqueue(_ context.Context, job Job) (Job, bool, error) {
	store.calls++
	store.job = job
	return job, true, nil
}
func (store *captureJobStore) ClaimNext(context.Context, time.Time) (Job, bool, error) {
	return Job{}, false, nil
}
func (store *captureJobStore) Retry(context.Context, string, int, time.Time, ErrorClass) error {
	return nil
}
func (store *captureJobStore) Finish(context.Context, string, JobState, ErrorClass) error { return nil }
func (store *captureJobStore) PurgeExpired(context.Context, time.Time) (int64, error)     { return 0, nil }
func (store *captureJobStore) IncrementPrompt(context.Context, SessionCounter) (SessionCounter, error) {
	return SessionCounter{}, nil
}
func (store *captureJobStore) EndSession(context.Context, []byte) error { return nil }

func TestCaptureEnqueueCapturesBeforeReturnAndEncryptsBoundedSnapshot(t *testing.T) {
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
	source := &captureSource{value: SanitizedSnapshot{ThreadLocator: []byte(`{"thread_id":"thread-1"}`), Snapshot: []byte(`{"turns":[{"role":"assistant","text":"safe"}]}`)}}
	scanner := &captureScanner{status: ports.ScanClean}
	store := &captureJobStore{}
	service := NewEnqueueService(source, scanner, cipher, store, time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC))
	result, err := service.Enqueue(context.Background(), EnqueueRequest{WorkspaceID: "workspace", SessionID: "session-secret", Reason: ReasonPeriodic, SourceWatermark: 10, CurrentPrompt: "remember this prompt"})
	if err != nil || !result.Enqueued || source.calls != 1 || store.calls != 1 {
		t.Fatalf("enqueue result=%+v err=%v source=%d store=%d", result, err, source.calls, store.calls)
	}
	if len(store.job.SessionDigest) != 32 || bytes.Contains(store.job.SessionDigest, []byte("session-secret")) {
		t.Fatal("raw session identifier retained")
	}
	metadata := JobMetadata("workspace", ReasonPeriodic, 10)
	opened, err := cipher.OpenSnapshot(context.Background(), store.job.ID, metadata, store.job.SnapshotCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(opened, []byte("remember this prompt")) || !bytes.Contains(opened, []byte(`"role":"user"`)) {
		t.Fatalf("current prompt was not appended: %s", opened)
	}
	if bytes.Contains(store.job.SnapshotCiphertext, []byte("remember this prompt")) {
		t.Fatal("snapshot ciphertext contains plaintext prompt")
	}
}

func TestCaptureScannerRedactionAndDegradationDoNotEnqueue(t *testing.T) {
	source := &captureSource{value: SanitizedSnapshot{ThreadLocator: []byte("locator"), Snapshot: []byte(`{"turns":[]}`)}}
	scanner := &captureScanner{status: ports.ScanFinding, findings: []ports.Finding{{Field: ports.FieldContent, Start: 0, End: 6}}}
	cipher := newCaptureCipher(t)
	store := &captureJobStore{}
	service := NewEnqueueService(source, scanner, cipher, store, time.Now().UTC())
	result, err := service.Enqueue(context.Background(), EnqueueRequest{WorkspaceID: "workspace", SessionID: "session", Reason: ReasonPreCompact, SourceWatermark: 1, CurrentPrompt: "secret value"})
	if err == nil || result.Enqueued || store.calls != 0 || scanner.calls != 2 {
		t.Fatalf("finding enqueue result=%+v err=%v store=%d scans=%d", result, err, store.calls, scanner.calls)
	}
	if _, err := service.Enqueue(context.Background(), EnqueueRequest{WorkspaceID: "workspace", SessionID: "session", Reason: ReasonPreCompact, SourceWatermark: 1, CurrentPrompt: string(bytes.Repeat([]byte{'x'}, MaxCurrentPromptBytes+1))}); err == nil || store.calls != 0 {
		t.Fatal("oversized prompt accepted or enqueued")
	}
	scanner.status = ports.ScanTimeout
	if result, err := service.Enqueue(context.Background(), EnqueueRequest{WorkspaceID: "workspace", SessionID: "session", Reason: ReasonPreCompact, SourceWatermark: 1, CurrentPrompt: "safe"}); err == nil || result.Enqueued || store.calls != 0 {
		t.Fatalf("scanner degradation result=%+v err=%v store=%d", result, err, store.calls)
	}
}

func TestCaptureBoundsAndTranscriptCanary(t *testing.T) {
	source := &captureSource{value: SanitizedSnapshot{ThreadLocator: []byte("/private/transcript_path-canary"), Snapshot: bytes.Repeat([]byte{'s'}, MaxSnapshotBytes+1)}}
	service := NewEnqueueService(source, &captureScanner{status: ports.ScanClean}, newCaptureCipher(t), &captureJobStore{}, time.Now().UTC())
	if _, err := service.Enqueue(context.Background(), EnqueueRequest{WorkspaceID: "workspace", SessionID: "session", Reason: ReasonSessionEnd, SourceWatermark: 2, CurrentPrompt: "safe"}); err == nil {
		t.Fatal("oversized source snapshot accepted")
	}
	if source.calls != 1 {
		t.Fatal("capture source was not called exactly once")
	}
}

func newCaptureCipher(t *testing.T) security.CurationCipher {
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

var _ JobStore = (*captureJobStore)(nil)
