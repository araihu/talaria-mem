package curation

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type GeneratedCreator interface {
	CreateGeneratedBatch(context.Context, []application.GeneratedMutationRequest, application.GeneratedSource) ([]application.MutationResult, error)
}

type WorkerConfig struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	Now         func() time.Time
	Jitter      func(int) time.Duration
}

type WorkerResult struct {
	Found      bool
	State      JobState
	ErrorClass ErrorClass
	Retried    bool
}

type Worker struct {
	store   JobStore
	cipher  security.CurationCipher
	router  Router
	creator GeneratedCreator
	deriver ports.KeyDeriver
	config  WorkerConfig

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewWorker(store JobStore, cipher security.CurationCipher, router Router, creator GeneratedCreator, deriver ports.KeyDeriver, config WorkerConfig) *Worker {
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 3
	}
	if config.BaseBackoff <= 0 {
		config.BaseBackoff = time.Second
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = time.Minute
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Jitter == nil {
		config.Jitter = func(int) time.Duration { return 0 }
	}
	return &Worker{store: store, cipher: cipher, router: router, creator: creator, deriver: deriver, config: config}
}

func (worker *Worker) ProcessOnce(ctx context.Context) (WorkerResult, error) {
	if worker == nil || worker.store == nil || worker.creator == nil {
		return WorkerResult{}, NewProviderError(ErrorUnavailable, errors.New("curation worker unavailable"))
	}
	now := worker.config.Now().UTC()
	job, found, err := worker.store.ClaimNext(ctx, now)
	if err != nil || !found {
		return WorkerResult{Found: found}, err
	}
	result := WorkerResult{Found: true, State: job.State}
	threadLocator, snapshot, err := worker.decryptJob(ctx, job)
	if err != nil {
		result.State = JobTerminal
		result.ErrorClass = ErrorPersistence
		return result, worker.finishTerminal(ctx, job, ErrorPersistence, err)
	}
	defer clearWorkerBytes(threadLocator)
	defer clearWorkerBytes(snapshot)
	curationResult, err := worker.router.Curate(ctx, CurationRequest{WorkspaceID: job.WorkspaceID, Reason: job.Reason, Watermark: job.SourceWatermark, ThreadLocator: threadLocator, Snapshot: snapshot})
	if err != nil {
		class := workerErrorClass(err)
		if class.Retryable() && job.AttemptCount < worker.config.MaxAttempts {
			next := now.Add(worker.backoff(job.AttemptCount))
			if retryErr := worker.store.Retry(ctx, job.ID, job.AttemptCount, next, class); retryErr != nil {
				return result, retryErr
			}
			result.Retried = true
			result.State = JobRetryWait
			result.ErrorClass = class
			return result, nil
		}
		result.State = JobTerminal
		result.ErrorClass = class
		return result, worker.finishTerminal(ctx, job, class, err)
	}
	if err := ValidateCandidates(curationResult.Candidates, nil); err != nil {
		class := workerErrorClass(err)
		result.State = JobTerminal
		result.ErrorClass = class
		return result, worker.finishTerminal(ctx, job, class, err)
	}
	requests, err := worker.generatedRequests(ctx, job.WorkspaceID, curationResult.Candidates)
	if err != nil {
		result.State = JobTerminal
		result.ErrorClass = ErrorInvalidOutput
		return result, worker.finishTerminal(ctx, job, ErrorInvalidOutput, err)
	}
	if len(requests) > 0 {
		if _, err := worker.creator.CreateGeneratedBatch(ctx, requests, application.GeneratedSourceAutomatic); err != nil {
			class := workerErrorClass(err)
			result.State = JobTerminal
			result.ErrorClass = class
			return result, worker.finishTerminal(ctx, job, class, err)
		}
	}
	if err := worker.store.Finish(ctx, job.ID, JobComplete, ""); err != nil {
		return result, err
	}
	result.State = JobComplete
	return result, nil
}

func (worker *Worker) Run(ctx context.Context) error {
	if worker == nil {
		return NewProviderError(ErrorUnavailable, errors.New("curation worker unavailable"))
	}
	ctx, cancel := context.WithCancel(ctx)
	worker.mu.Lock()
	worker.cancel = cancel
	worker.done = make(chan struct{})
	done := worker.done
	worker.mu.Unlock()
	defer func() {
		cancel()
		worker.mu.Lock()
		if worker.done == done {
			close(done)
			worker.done = nil
			worker.cancel = nil
		}
		worker.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_, err := worker.ProcessOnce(ctx)
		if err != nil && ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (worker *Worker) Close() error {
	worker.mu.Lock()
	cancel, done := worker.cancel, worker.done
	worker.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	<-done
	return nil
}

func (worker *Worker) decryptJob(ctx context.Context, job Job) ([]byte, []byte, error) {
	metadata := JobMetadata(job.WorkspaceID, job.Reason, job.SourceWatermark)
	locator, err := worker.cipher.OpenLocator(ctx, job.ID, metadata, job.ThreadLocatorCiphertext)
	if err != nil {
		return nil, nil, errors.New("curation locator decryption failed")
	}
	snapshot, err := worker.cipher.OpenSnapshot(ctx, job.ID, metadata, job.SnapshotCiphertext)
	if err != nil {
		clearWorkerBytes(locator)
		return nil, nil, errors.New("curation snapshot decryption failed")
	}
	return locator, snapshot, nil
}

func (worker *Worker) generatedRequests(ctx context.Context, workspaceID string, candidates []Candidate) ([]application.GeneratedMutationRequest, error) {
	key, err := worker.fingerprintKey(ctx)
	if err != nil {
		return nil, err
	}
	defer clearWorkerBytes(key)
	seen := make(map[string]struct{}, len(candidates))
	requests := make([]application.GeneratedMutationRequest, 0, len(candidates))
	for _, candidate := range candidates {
		fingerprint, err := CandidateFingerprint(candidate, key)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[fingerprint]; exists {
			continue
		}
		seen[fingerprint] = struct{}{}
		requests = append(requests, application.GeneratedMutationRequest{WorkspaceID: workspaceID, Kind: candidate.Kind, Title: candidate.Title, Content: candidate.Content, Tags: append([]string(nil), candidate.Tags...), ResolutionState: candidate.ResolutionState, Fingerprint: fingerprint})
	}
	return requests, nil
}

func (worker *Worker) fingerprintKey(ctx context.Context) ([]byte, error) {
	if worker.deriver == nil {
		return nil, errors.New("generated fingerprint key unavailable")
	}
	key, err := worker.deriver.DeriveKey(ctx, ports.KeyPurposeGeneratedFingerprint, ports.KeyDerivationVersion)
	if err != nil || len(key) == 0 {
		return nil, errors.New("generated fingerprint key unavailable")
	}
	return key, nil
}

func (worker *Worker) finishTerminal(ctx context.Context, job Job, class ErrorClass, cause error) error {
	if class == "" || !class.Valid() {
		class = ErrorPersistence
	}
	if err := worker.store.Finish(ctx, job.ID, JobTerminal, class); err != nil {
		return err
	}
	return nil
}

func (worker *Worker) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := worker.config.BaseBackoff
	for index := 1; index < attempt; index++ {
		if delay >= worker.config.MaxBackoff/2 {
			delay = worker.config.MaxBackoff
			break
		}
		delay *= 2
	}
	if delay > worker.config.MaxBackoff {
		delay = worker.config.MaxBackoff
	}
	delay += worker.config.Jitter(attempt)
	if delay < 0 || delay > worker.config.MaxBackoff {
		return worker.config.MaxBackoff
	}
	return delay
}

func workerErrorClass(err error) ErrorClass {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr.Class.Valid() {
		return providerErr.Class
	}
	switch domain.CodeOf(err) {
	case domain.CodeSecretRefusal:
		return ErrorScannerRefusal
	case domain.CodeTimeout:
		return ErrorTimeout
	case domain.CodeUnavailable:
		return ErrorUnavailable
	default:
		return ErrorPersistence
	}
}

func clearWorkerBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
