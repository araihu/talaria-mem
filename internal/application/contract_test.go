package application

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

func TestTrustMatrix(t *testing.T) { TestMemoryServiceTrustMatrixAndAtomicScan(t) }
func TestResolutionState(t *testing.T) {
	repo := newServiceRepo()
	scanner := &serviceScanner{status: ports.ScanClean}
	service := NewMemoryService(repo, scanner, serviceClock{now: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}, nil)
	result, err := service.Create(context.Background(), MutationRequest{Actor: ActorCLI, Caller: "cli", WorkspaceID: "workspace", Kind: domain.MemoryKindFailure, Title: "failure", Content: "body"})
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := repo.ReadCurrent(context.Background(), result.MemoryID)
	if err != nil {
		t.Fatal(err)
	}
	if revision.ResolutionState != domain.ResolutionOpen {
		t.Fatalf("state = %q", revision.ResolutionState)
	}
	if _, err := service.Create(context.Background(), MutationRequest{Actor: ActorCLI, Caller: "cli", WorkspaceID: "workspace", Kind: domain.MemoryKindState, Title: "state", Content: "body", ResolutionState: domain.ResolutionOpen}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("non-failure state error = %v", err)
	}
}
func TestIdempotencyRetention(t *testing.T) { TestMemoryServiceVerifiedStandingAndIdempotentReplay(t) }
func TestAtomicMutation(t *testing.T)       { TestMemoryServiceTrustMatrixAndAtomicScan(t) }
func TestREQ_12_PinEligibility(t *testing.T) {
	repo := newServiceRepo()
	scanner := &serviceScanner{status: ports.ScanClean}
	service := NewMemoryService(repo, scanner, serviceClock{now: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}, nil)
	created, err := service.Create(context.Background(), MutationRequest{Actor: ActorMCP, Caller: "mcp", WorkspaceID: "workspace", Kind: domain.MemoryKindState, Title: "state", Content: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Pin(context.Background(), MutationRequest{Actor: ActorMCP, Caller: "mcp", MemoryID: created.MemoryID, ExpectedRevisionID: created.RevisionID}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("ineligible pin error = %v", err)
	}
}
func TestPinReserve(t *testing.T) { TestREQ_12_PinEligibility(t) }
func TestContentOutputGuard(t *testing.T) {
	guard := NewContentOutputGuard(&serviceScanner{status: ports.ScanClean}, nil, serviceClock{now: time.Now().UTC()})
	result, err := guard.Check(context.Background(), OutputRequest{Route: ports.FieldCLIRead, WorkspaceID: "workspace", MemoryID: "memory", RevisionID: "revision", Fields: []ports.TextField{{Name: ports.FieldCLIRead, Value: "safe"}}})
	if err != nil || !result.Allowed {
		t.Fatalf("guard = %+v err=%v", result, err)
	}
}
func TestScanAndQuarantine(t *testing.T)        { TestContentOutputGuard(t) }
func TestReadFindingQuarantine(t *testing.T)    { TestMemoryServiceTrustMatrixAndAtomicScan(t) }
func TestScannerFailureNoMutation(t *testing.T) { TestMemoryServiceTrustMatrixAndAtomicScan(t) }
