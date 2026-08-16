package projection

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type ScopeSource interface {
	LoadScope(ctx context.Context, scopeID string) (ScopeDocument, error)
}

type OutboxStore interface {
	PendingProjection(ctx context.Context, scopeID string, now time.Time) ([]ports.ProjectionRequest, error)
	AcknowledgeProjection(ctx context.Context, scopeID string, revisionWatermark int64) error
	RecordProjectionFailure(ctx context.Context, scopeID string, revisionWatermark int64, attemptCount int64, nextAttemptAt time.Time, safeError string) error
	ProjectionFingerprint(ctx context.Context, scopeID string) (string, error)
	SetProjectionState(ctx context.Context, scopeID, status, fingerprint string, revisionWatermark int64) error
}

type OutputGuard interface {
	Check(ctx context.Context, request application.OutputRequest) (application.OutputResult, error)
}

type Worker struct {
	Source    ScopeSource
	Outbox    OutboxStore
	Files     ports.ManagedFileStore
	Guard     OutputGuard
	Renderer  *Renderer
	Clock     ports.Clock
	Failpoint Failpoint
	mu        sync.Mutex
	locks     map[string]chan struct{}
	attempts  map[string]int64
}

func NewWorker(source ScopeSource, outbox OutboxStore, files ports.ManagedFileStore, guard OutputGuard, clock ports.Clock) *Worker {
	return &Worker{Source: source, Outbox: outbox, Files: files, Guard: guard, Renderer: NewRenderer(guard), Clock: clock, locks: map[string]chan struct{}{}, attempts: map[string]int64{}}
}

func (worker *Worker) Project(ctx context.Context, request ports.ProjectionRequest) error {
	if worker == nil || worker.Source == nil || worker.Outbox == nil || worker.Files == nil || worker.Guard == nil {
		return domain.NewError(domain.CodeUnavailable, "projection dependencies unavailable", true)
	}
	if request.ScopeID == "" {
		return domain.NewError(domain.CodeValidation, "projection scope is required", false)
	}
	if !worker.acquire(request.ScopeID) {
		return domain.NewError(domain.CodeMaintenanceLock, "projection scope is busy", true)
	}
	defer worker.release(request.ScopeID)
	now := worker.now()
	events, err := worker.Outbox.PendingProjection(ctx, request.ScopeID, now)
	if err != nil {
		return worker.fail(ctx, request.ScopeID, request.RevisionWatermark, err)
	}
	watermark := request.RevisionWatermark
	for _, event := range events {
		if event.RevisionWatermark > watermark {
			watermark = event.RevisionWatermark
		}
	}
	scope, err := worker.Source.LoadScope(ctx, request.ScopeID)
	if err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	if request.RevisionWatermark > watermark {
		watermark = request.RevisionWatermark
	}
	renderer := worker.Renderer
	if renderer == nil {
		renderer = NewRenderer(worker.Guard)
	}
	rendered, err := renderer.Render(ctx, scope)
	if err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	if worker.Failpoint != nil {
		if err := worker.Failpoint.Hit(FailpointBeforeTemp); err != nil {
			return worker.fail(ctx, request.ScopeID, watermark, err)
		}
	}
	directory := filepath.Dir(scope.TargetPath)
	prefix := ".talaria-mem-projection-"
	if err := worker.Files.CleanupStaleTemps(ctx, directory, prefix, now.Add(-time.Hour)); err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	temp, err := worker.Files.CreateTemp(ctx, directory, prefix, ports.ManagedFileMode)
	if err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	closeTemp := true
	defer func() {
		if closeTemp {
			_ = temp.Writer.Close()
		}
	}()
	if _, err := temp.Writer.Write(rendered.Bytes); err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	if err := temp.Writer.Close(); err != nil {
		closeTemp = false
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	closeTemp = false
	if worker.Failpoint != nil {
		if err := worker.Failpoint.Hit(FailpointBeforeFileSync); err != nil {
			return worker.fail(ctx, request.ScopeID, watermark, err)
		}
	}
	if err := worker.Files.SyncFile(ctx, temp.Path); err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	if worker.Failpoint != nil {
		if err := worker.Failpoint.Hit(FailpointBeforeRename); err != nil {
			return worker.fail(ctx, request.ScopeID, watermark, err)
		}
	}
	if storedFingerprint, err := worker.Outbox.ProjectionFingerprint(ctx, scope.ScopeID); err == nil && storedFingerprint != "" {
		if currentFingerprint, fingerprintErr := worker.Files.FingerprintNoFollow(ctx, scope.TargetPath); fingerprintErr == nil {
			current := currentFingerprint.SHA256Hex
			if len(current) > 7 && current[:7] == "sha256:" {
				current = current[7:]
			}
			stored := storedFingerprint
			if len(stored) > 7 && stored[:7] == "sha256:" {
				stored = stored[7:]
			}
			if current != stored {
				_ = worker.Outbox.SetProjectionState(ctx, scope.ScopeID, "drifted", storedFingerprint, watermark)
				return worker.fail(ctx, request.ScopeID, watermark, domain.NewError(domain.CodeUnavailable, "projection drift detected", false))
			}
		}
	}
	var expected *ports.FileFingerprint
	if current, err := worker.Files.FingerprintNoFollow(ctx, scope.TargetPath); err == nil {
		expected = &current
	} else if !errors.Is(err, fs.ErrNotExist) && !isNotFound(err) {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	if err := worker.Files.ReplaceNoFollow(ctx, temp.Path, scope.TargetPath, expected); err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	if worker.Failpoint != nil {
		if err := worker.Failpoint.Hit(FailpointBeforeDirectorySync); err != nil {
			return worker.fail(ctx, request.ScopeID, watermark, err)
		}
	}
	if err := worker.Files.SyncParent(ctx, scope.TargetPath); err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	if worker.Failpoint != nil {
		if err := worker.Failpoint.Hit(FailpointBeforeVerify); err != nil {
			return worker.fail(ctx, request.ScopeID, watermark, err)
		}
	}
	fingerprint, err := worker.Files.FingerprintNoFollow(ctx, scope.TargetPath)
	if err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	actualFileHash := fingerprint.SHA256Hex
	if len(actualFileHash) > 7 && actualFileHash[:7] == "sha256:" {
		actualFileHash = actualFileHash[7:]
	}
	if actualFileHash == "" || actualFileHash != rendered.FileSHA256 {
		return worker.fail(ctx, request.ScopeID, watermark, domain.NewError(domain.CodeUnavailable, "projection fingerprint verification failed", false))
	}
	if err := worker.Outbox.SetProjectionState(ctx, scope.ScopeID, "ready", rendered.FileSHA256, watermark); err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	if worker.Failpoint != nil {
		if err := worker.Failpoint.Hit(FailpointBeforeAcknowledge); err != nil {
			return worker.fail(ctx, request.ScopeID, watermark, err)
		}
	}
	if err := worker.Outbox.AcknowledgeProjection(ctx, scope.ScopeID, watermark); err != nil {
		return worker.fail(ctx, request.ScopeID, watermark, err)
	}
	worker.mu.Lock()
	delete(worker.attempts, scope.ScopeID)
	worker.mu.Unlock()
	return nil
}

func (worker *Worker) acquire(scopeID string) bool {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	lock, ok := worker.locks[scopeID]
	if !ok {
		lock = make(chan struct{}, 1)
		worker.locks[scopeID] = lock
	}
	select {
	case lock <- struct{}{}:
		return true
	default:
		return false
	}
}
func (worker *Worker) release(scopeID string) {
	worker.mu.Lock()
	lock := worker.locks[scopeID]
	worker.mu.Unlock()
	if lock != nil {
		<-lock
	}
}
func (worker *Worker) now() time.Time {
	if worker.Clock != nil {
		return worker.Clock.Now().UTC()
	}
	return time.Now().UTC()
}
func (worker *Worker) fail(ctx context.Context, scopeID string, watermark int64, err error) error {
	if worker.Outbox == nil {
		return err
	}
	worker.mu.Lock()
	worker.attempts[scopeID]++
	attempt := worker.attempts[scopeID]
	worker.mu.Unlock()
	delay := time.Second
	for index := int64(1); index < attempt && delay < time.Hour; index++ {
		delay *= 2
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	next := worker.now().Add(delay)
	safe := sanitizeError(err)
	_ = worker.Outbox.RecordProjectionFailure(ctx, scopeID, watermark, attempt, next, safe)
	_ = worker.Outbox.SetProjectionState(ctx, scopeID, "blocked", "", watermark)
	return err
}
func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	var typed *domain.Error
	if errors.As(err, &typed) {
		return string(typed.Code())
	}
	return "projection failure"
}
func isNotFound(err error) bool { return domain.IsCode(err, domain.CodeNotFound) }

var _ ports.Projector = (*Worker)(nil)
