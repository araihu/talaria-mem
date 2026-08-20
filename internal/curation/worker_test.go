package curation

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type workerStore struct {
	jobs map[string]*Job
}

func newWorkerStore(job Job) *workerStore { return &workerStore{jobs: map[string]*Job{job.ID: &job}} }

func (store *workerStore) Enqueue(_ context.Context, job Job) (Job, bool, error) {
	if existing, ok := store.jobs[job.ID]; ok {
		return *existing, false, nil
	}
	store.jobs[job.ID] = &job
	return job, true, nil
}
func (store *workerStore) ClaimNext(_ context.Context, now time.Time) (Job, bool, error) {
	for _, job := range store.jobs {
		if (job.State == JobPending || job.State == JobRetryWait) && (job.NextAttemptAt == nil || !job.NextAttemptAt.After(now)) && job.ExpiresAt.After(now) {
			job.State = JobRunning
			job.AttemptCount++
			job.NextAttemptAt = nil
			return *job, true, nil
		}
	}
	return Job{}, false, nil
}
func (store *workerStore) Retry(_ context.Context, id string, attempt int, next time.Time, class ErrorClass) error {
	job, ok := store.jobs[id]
	if !ok || job.State != JobRunning {
		return errors.New("job not running")
	}
	job.State, job.AttemptCount, job.NextAttemptAt, job.SafeErrorClass = JobRetryWait, attempt, &next, class
	return nil
}
func (store *workerStore) Finish(_ context.Context, id string, state JobState, class ErrorClass) error {
	job, ok := store.jobs[id]
	if !ok || job.State != JobRunning {
		return errors.New("job not running")
	}
	job.State, job.SafeErrorClass = state, class
	job.ThreadLocatorCiphertext, job.SnapshotCiphertext = nil, nil
	return nil
}
func (store *workerStore) PurgeExpired(context.Context, time.Time) (int64, error) { return 0, nil }
func (store *workerStore) IncrementPrompt(context.Context, SessionCounter) (SessionCounter, error) {
	return SessionCounter{}, nil
}
func (store *workerStore) EndSession(context.Context, []byte) error { return nil }

type workerCurator struct {
	results []CurationResult
	errors  []error
	calls   int
	result  int
}

func (curator *workerCurator) Curate(context.Context, CurationRequest) (CurationResult, error) {
	index := curator.calls
	curator.calls++
	if index < len(curator.errors) && curator.errors[index] != nil {
		return CurationResult{}, curator.errors[index]
	}
	if curator.result >= len(curator.results) {
		return CurationResult{}, errors.New("missing fake curation result")
	}
	result := curator.results[curator.result]
	curator.result++
	return result, nil
}

type workerCreator struct {
	requests []application.GeneratedMutationRequest
	err      error
}

func (creator *workerCreator) CreateGeneratedBatch(_ context.Context, requests []application.GeneratedMutationRequest, _ application.GeneratedSource) ([]application.MutationResult, error) {
	creator.requests = append(creator.requests, requests...)
	if creator.err != nil {
		return nil, creator.err
	}
	return make([]application.MutationResult, len(requests)), nil
}

func TestWorkerRetriesProviderThenCreatesAndErasesPayload(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	cipher, deriver := newWorkerCrypto(t)
	job := encryptedWorkerJob(t, cipher, now)
	store := newWorkerStore(job)
	curator := &workerCurator{errors: []error{NewProviderError(ErrorTimeout, errors.New("temporary"))}, results: []CurationResult{{Candidates: []Candidate{{Kind: domain.MemoryKindState, Title: "state", Content: "body"}}}}}
	creator := &workerCreator{}
	worker := NewWorker(store, cipher, Router{Enabled: true, Chain: []ProviderEntry{{Name: "fake", Curator: curator}}}, creator, deriver, WorkerConfig{Now: func() time.Time { return now }, BaseBackoff: time.Second, MaxBackoff: time.Minute})
	first, err := worker.ProcessOnce(context.Background())
	if err != nil || !first.Found || !first.Retried || first.State != JobRetryWait || first.ErrorClass != ErrorTimeout {
		t.Fatalf("first worker result=%+v err=%v", first, err)
	}
	worker.config.Now = func() time.Time { return now.Add(2 * time.Second) }
	second, err := worker.ProcessOnce(context.Background())
	if err != nil || !second.Found || second.State != JobComplete || len(creator.requests) != 1 {
		t.Fatalf("second worker result=%+v err=%v requests=%+v", second, err, creator.requests)
	}
	if payload := store.jobs[job.ID].SnapshotCiphertext; len(payload) != 0 {
		t.Fatalf("completed snapshot retained: %d bytes", len(payload))
	}
}

func TestWorkerInvalidOutputAndDecryptFailureAreTerminal(t *testing.T) {
	now := time.Now().UTC()
	cipher, deriver := newWorkerCrypto(t)
	job := encryptedWorkerJob(t, cipher, now)
	store := newWorkerStore(job)
	creator := &workerCreator{}
	curator := &workerCurator{results: []CurationResult{{Candidates: []Candidate{{Kind: domain.MemoryKindStandingInstruction, Title: "bad", Content: "bad"}}}}}
	worker := NewWorker(store, cipher, Router{Enabled: true, Chain: []ProviderEntry{{Name: "fake", Curator: curator}}}, creator, deriver, WorkerConfig{Now: func() time.Time { return now }})
	result, err := worker.ProcessOnce(context.Background())
	if err != nil || result.State != JobTerminal || store.jobs[job.ID].SafeErrorClass != ErrorInvalidOutput {
		t.Fatalf("invalid output result=%+v err=%v job=%+v", result, err, store.jobs[job.ID])
	}
	badJob := encryptedWorkerJob(t, cipher, now)
	badJob.ID = "job-bad"
	badJob.SnapshotCiphertext[0] ^= 1
	store = newWorkerStore(badJob)
	worker = NewWorker(store, cipher, Router{Enabled: true, Chain: []ProviderEntry{{Name: "fake", Curator: curator}}}, creator, deriver, WorkerConfig{Now: func() time.Time { return now }})
	result, err = worker.ProcessOnce(context.Background())
	if err != nil || result.State != JobTerminal || store.jobs[badJob.ID].SafeErrorClass != ErrorPersistence {
		t.Fatalf("decrypt failure result=%+v err=%v job=%+v", result, err, store.jobs[badJob.ID])
	}
}

func TestWorkerDeduplicatesCandidatesBeforeAtomicCreate(t *testing.T) {
	now := time.Now().UTC()
	cipher, deriver := newWorkerCrypto(t)
	job := encryptedWorkerJob(t, cipher, now)
	store := newWorkerStore(job)
	candidate := Candidate{Kind: domain.MemoryKindProcedure, Title: "Run", Content: "run command", Tags: []string{"ops"}}
	curator := &workerCurator{results: []CurationResult{{Candidates: []Candidate{candidate, candidate}}}}
	creator := &workerCreator{}
	worker := NewWorker(store, cipher, Router{Enabled: true, Chain: []ProviderEntry{{Name: "fake", Curator: curator}}}, creator, deriver, WorkerConfig{Now: func() time.Time { return now }})
	if result, err := worker.ProcessOnce(context.Background()); err != nil || result.State != JobComplete || len(creator.requests) != 1 {
		t.Fatalf("dedupe result=%+v err=%v requests=%d", result, err, len(creator.requests))
	}
}

func TestWorkerUsesOrderedFallbackInsideOneAttempt(t *testing.T) {
	now := time.Now().UTC()
	cipher, deriver := newWorkerCrypto(t)
	job := encryptedWorkerJob(t, cipher, now)
	store := newWorkerStore(job)
	first := &workerCurator{errors: []error{NewProviderError(ErrorTimeout, errors.New("primary unavailable"))}}
	second := &workerCurator{results: []CurationResult{{Candidates: []Candidate{{Kind: domain.MemoryKindState, Title: "fallback", Content: "safe"}}}}}
	creator := &workerCreator{}
	worker := NewWorker(store, cipher, Router{Enabled: true, Chain: []ProviderEntry{{Name: "primary", Curator: first}, {Name: "fallback", Curator: second}}}, creator, deriver, WorkerConfig{Now: func() time.Time { return now }})
	result, err := worker.ProcessOnce(context.Background())
	if err != nil || result.State != JobComplete || result.Retried || len(creator.requests) != 1 {
		t.Fatalf("fallback result=%+v err=%v requests=%d", result, err, len(creator.requests))
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("provider calls primary=%d fallback=%d", first.calls, second.calls)
	}
}

func TestWorkerChainExhaustionRetriesAndExpirySkips(t *testing.T) {
	now := time.Now().UTC()
	cipher, deriver := newWorkerCrypto(t)
	job := encryptedWorkerJob(t, cipher, now)
	store := newWorkerStore(job)
	first := &workerCurator{errors: []error{NewProviderError(ErrorUnavailable, errors.New("primary down"))}}
	second := &workerCurator{errors: []error{NewProviderError(ErrorRateLimit, errors.New("fallback busy"))}}
	worker := NewWorker(store, cipher, Router{Enabled: true, Chain: []ProviderEntry{{Name: "primary", Curator: first}, {Name: "fallback", Curator: second}}}, &workerCreator{}, deriver, WorkerConfig{Now: func() time.Time { return now }, BaseBackoff: time.Second})
	result, err := worker.ProcessOnce(context.Background())
	if err != nil || !result.Retried || result.State != JobRetryWait || result.ErrorClass != ErrorRateLimit {
		t.Fatalf("exhaustion result=%+v err=%v", result, err)
	}

	expired := encryptedWorkerJob(t, cipher, now.Add(-time.Hour))
	expired.ID = "job-expired"
	expired.ExpiresAt = now.Add(-time.Minute)
	store = newWorkerStore(expired)
	worker = NewWorker(store, cipher, Router{Enabled: true, Chain: []ProviderEntry{{Name: "unused", Curator: second}}}, &workerCreator{}, deriver, WorkerConfig{Now: func() time.Time { return now }})
	result, err = worker.ProcessOnce(context.Background())
	if err != nil || result.Found {
		t.Fatalf("expired result=%+v err=%v", result, err)
	}
}

func TestWorkerScannerRefusalIsTerminalAndErasesPayload(t *testing.T) {
	now := time.Now().UTC()
	cipher, deriver := newWorkerCrypto(t)
	job := encryptedWorkerJob(t, cipher, now)
	store := newWorkerStore(job)
	curator := &workerCurator{results: []CurationResult{{Candidates: []Candidate{{Kind: domain.MemoryKindState, Title: "state", Content: "safe"}}}}}
	creator := &workerCreator{err: domain.NewError(domain.CodeSecretRefusal, "scanner refused", false)}
	worker := NewWorker(store, cipher, Router{Enabled: true, Chain: []ProviderEntry{{Name: "fake", Curator: curator}}}, creator, deriver, WorkerConfig{Now: func() time.Time { return now }})
	result, err := worker.ProcessOnce(context.Background())
	if err != nil || result.State != JobTerminal || result.ErrorClass != ErrorScannerRefusal {
		t.Fatalf("scanner refusal result=%+v err=%v", result, err)
	}
	if len(store.jobs[job.ID].SnapshotCiphertext) != 0 || len(store.jobs[job.ID].ThreadLocatorCiphertext) != 0 {
		t.Fatal("terminal job retained encrypted payload")
	}
}

func encryptedWorkerJob(t *testing.T, cipher security.CurationCipher, now time.Time) Job {
	t.Helper()
	job := Job{ID: "job-worker", WorkspaceID: "workspace", Reason: ReasonPeriodic, SessionDigest: bytes.Repeat([]byte{1}, 32), SourceWatermark: 7, State: JobPending, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
	metadata := JobMetadata(job.WorkspaceID, job.Reason, job.SourceWatermark)
	var err error
	job.ThreadLocatorCiphertext, err = cipher.SealLocator(context.Background(), job.ID, metadata, []byte(`{"thread_id":"thread"}`))
	if err != nil {
		t.Fatal(err)
	}
	job.SnapshotCiphertext, err = cipher.SealSnapshot(context.Background(), job.ID, metadata, []byte(`{"turns":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func newWorkerCrypto(t *testing.T) (security.CurationCipher, ports.KeyDeriver) {
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
	return cipher, deriver
}

var _ JobStore = (*workerStore)(nil)
