package projection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/application"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type projectionGuard struct {
	status ports.ScanStatus
	calls  int
}

func (guard *projectionGuard) Check(context.Context, application.OutputRequest) (application.OutputResult, error) {
	guard.calls++
	if guard.status == ports.ScanClean {
		return application.OutputResult{Allowed: true, Status: ports.ScanClean}, nil
	}
	return application.OutputResult{Status: guard.status}, domain.NewError(domain.CodeSecretRefusal, "projection rejected", false)
}

type projectionFiles struct{}

func (projectionFiles) CreateTemp(_ context.Context, directory, prefix string, mode os.FileMode) (ports.ManagedTempFile, error) {
	file, err := os.CreateTemp(directory, prefix)
	if err != nil {
		return ports.ManagedTempFile{}, err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return ports.ManagedTempFile{}, err
	}
	return ports.ManagedTempFile{Path: file.Name(), Writer: file}, nil
}
func (projectionFiles) SyncFile(_ context.Context, path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
func (projectionFiles) ReplaceNoFollow(_ context.Context, tempPath, targetPath string, expected *ports.FileFingerprint) error {
	if expected != nil {
		current, err := fingerprint(targetPath)
		if err != nil {
			return err
		}
		if current.SHA256Hex != expected.SHA256Hex {
			return errors.New("drift")
		}
	}
	return os.Rename(tempPath, targetPath)
}
func (projectionFiles) SyncParent(_ context.Context, path string) error {
	file, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
func (projectionFiles) CleanupStaleTemps(_ context.Context, directory, prefix string, olderThan time.Time) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if len(entry.Name()) >= len(prefix) && entry.Name()[:len(prefix)] == prefix {
			info, err := entry.Info()
			if err == nil && info.ModTime().Before(olderThan) {
				_ = os.Remove(filepath.Join(directory, entry.Name()))
			}
		}
	}
	return nil
}
func (projectionFiles) FingerprintNoFollow(_ context.Context, path string) (ports.FileFingerprint, error) {
	return fingerprint(path)
}
func fingerprint(path string) (ports.FileFingerprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ports.FileFingerprint{}, err
	}
	hash := sha256.Sum256(data)
	info, err := os.Stat(path)
	if err != nil {
		return ports.FileFingerprint{}, err
	}
	return ports.FileFingerprint{SHA256Hex: hex.EncodeToString(hash[:]), Size: int64(len(data)), Mode: info.Mode().Perm()}, nil
}

var _ ports.ManagedFileStore = projectionFiles{}

type projectionSource struct{ scope ScopeDocument }

func (source projectionSource) LoadScope(context.Context, string) (ScopeDocument, error) {
	return source.scope, nil
}

type projectionOutbox struct {
	events              []ports.ProjectionRequest
	fingerprint, status string
	ack                 int
	failures            int
	safe                string
}

func (outbox *projectionOutbox) PendingProjection(context.Context, string, time.Time) ([]ports.ProjectionRequest, error) {
	return append([]ports.ProjectionRequest(nil), outbox.events...), nil
}
func (outbox *projectionOutbox) AcknowledgeProjection(context.Context, string, int64) error {
	outbox.ack++
	return nil
}
func (outbox *projectionOutbox) RecordProjectionFailure(context.Context, string, int64, int64, time.Time, string) error {
	outbox.failures++
	outbox.safe = "projection failure"
	return nil
}
func (outbox *projectionOutbox) ProjectionFingerprint(context.Context, string) (string, error) {
	return outbox.fingerprint, nil
}
func (outbox *projectionOutbox) SetProjectionState(_ context.Context, _, status, fingerprint string, _ int64) error {
	outbox.status, outbox.fingerprint = status, fingerprint
	return nil
}

func projectionMemory(id string, created time.Time) MemoryDocument {
	return MemoryDocument{Memory: domain.Memory{ID: id, WorkspaceID: "workspace", Kind: domain.MemoryKindState, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: created, UpdatedAt: created}, Revision: domain.MemoryRevision{ID: id + "-revision", MemoryID: id, Number: 1, Kind: domain.MemoryKindState, Title: id, Content: "content-" + id, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, CreatedAt: created}}
}

func TestRenderDeterministicOrderAndFingerprint(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	scope := ScopeDocument{ScopeID: "workspace", TargetPath: "/tmp/projection.md", Memories: []MemoryDocument{projectionMemory("b", now), projectionMemory("a", now)}}
	first, err := Render(scope)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(scope)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Bytes) != string(second.Bytes) || first.Fingerprint != second.Fingerprint {
		t.Fatal("render is not deterministic")
	}
	if first.MemoryCount != 2 || ParseFingerprint(first.Bytes) != first.Fingerprint {
		t.Fatalf("render metadata = %+v", first)
	}
	if string(first.Bytes) != string(append([]byte{}, first.Bytes...)) {
		t.Fatal("copy")
	}
	if !sort.StringsAreSorted([]string{"a", "b"}) {
		t.Fatal("sort sanity")
	}
}

func TestWorkerAcknowledgesOnlyAfterVerifiedReplacement(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "memory.md")
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	scope := ScopeDocument{ScopeID: "workspace", TargetPath: target, Memories: []MemoryDocument{projectionMemory("a", now)}}
	outbox := &projectionOutbox{events: []ports.ProjectionRequest{{ScopeID: "workspace", RevisionWatermark: 1}}}
	guard := &projectionGuard{status: ports.ScanClean}
	worker := NewWorker(projectionSource{scope: scope}, outbox, projectionFiles{}, guard, fixedProjectionClock{now})
	if err := worker.Project(context.Background(), ports.ProjectionRequest{ScopeID: "workspace", RevisionWatermark: 1}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || outbox.ack != 1 || outbox.status != "ready" || guard.calls != 1 {
		t.Fatalf("worker state ack=%d status=%q calls=%d", outbox.ack, outbox.status, guard.calls)
	}
	guard.status = ports.ScanFinding
	outbox.events = []ports.ProjectionRequest{{ScopeID: "workspace", RevisionWatermark: 2}}
	if err := worker.Project(context.Background(), ports.ProjectionRequest{ScopeID: "workspace", RevisionWatermark: 2}); err == nil {
		t.Fatal("finding accepted")
	}
	if outbox.ack != 1 || outbox.failures == 0 {
		t.Fatalf("finding acknowledged")
	}
}

type fixedProjectionClock struct{ now time.Time }

func (clock fixedProjectionClock) Now() time.Time { return clock.now }

var _ ports.Clock = fixedProjectionClock{}
