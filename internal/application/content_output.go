package application

import (
	"context"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// OutputRequest identifies a content boundary. Route values are closed
// vocabulary in the scanner port; arbitrary caller strings are rejected.
type OutputRequest struct {
	Route       ports.FieldIdentifier
	WorkspaceID string
	MemoryID    string
	RevisionID  string
	Generation  string
	Fields      []ports.TextField
}

type OutputResult struct {
	Allowed     bool
	Quarantined bool
	Status      ports.ScanStatus
	Generation  string
}

// QuarantineStore is the optional durable side of a read-time finding. It
// must atomically quarantine the exact revision, remove FTS, append outbox,
// and block projection/readiness. Implementations should be backed by the
// same SQLite transaction as the mutation.
type QuarantineStore interface {
	ScanAndQuarantine(ctx context.Context, workspaceID, memoryID, revisionID, generation string, now time.Time) error
}

// ContentOutputGuard is the one output boundary used by application,
// projection, export, review, and future adapters. It never returns finding
// fragments or content-bearing scanner diagnostics.
type ContentOutputGuard struct {
	Scanner    ports.Scanner
	Quarantine QuarantineStore
	Clock      ports.Clock
}

func NewContentOutputGuard(scanner ports.Scanner, quarantine QuarantineStore, clock ports.Clock) *ContentOutputGuard {
	return &ContentOutputGuard{Scanner: scanner, Quarantine: quarantine, Clock: clock}
}

func (guard *ContentOutputGuard) Check(ctx context.Context, request OutputRequest) (OutputResult, error) {
	if guard == nil || guard.Scanner == nil {
		return OutputResult{}, domain.NewError(domain.CodeUnavailable, "content scanner unavailable", true)
	}
	if !request.Route.Valid() {
		return OutputResult{}, domain.NewError(domain.CodeValidation, "invalid content output route", false)
	}
	if request.MemoryID == "" || request.RevisionID == "" {
		return OutputResult{}, domain.NewError(domain.CodeValidation, "content output identity is required", false)
	}
	for _, field := range request.Fields {
		if !field.Name.Valid() {
			return OutputResult{}, domain.NewError(domain.CodeValidation, "invalid scanner field", false)
		}
	}
	result := guard.Scanner.Scan(ctx, append([]ports.TextField(nil), request.Fields...))
	if !result.Status.Valid() {
		return OutputResult{}, domain.NewError(domain.CodeUnavailable, "scanner returned invalid status", true)
	}
	output := OutputResult{Status: result.Status, Generation: result.Generation}
	switch result.Status {
	case ports.ScanClean:
		output.Allowed = true
		return output, nil
	case ports.ScanFinding:
		if guard.Quarantine == nil {
			return output, domain.NewError(domain.CodeQuarantine, "content quarantined", false)
		}
		now := time.Now().UTC()
		if guard.Clock != nil {
			now = guard.Clock.Now().UTC()
		}
		if err := guard.Quarantine.ScanAndQuarantine(ctx, request.WorkspaceID, request.MemoryID, request.RevisionID, result.Generation, now); err != nil {
			return output, err
		}
		output.Quarantined = true
		return output, domain.NewError(domain.CodeQuarantine, "content quarantined", false)
	case ports.ScanTimeout:
		return output, domain.NewError(domain.CodeTimeout, "content scan timed out", true)
	case ports.ScanUncertain, ports.ScanPanic, ports.ScanCancellation, ports.ScanError:
		return output, domain.NewError(domain.CodeUnavailable, "content scan unavailable", true)
	default:
		return output, domain.NewError(domain.CodeUnavailable, "content scan unavailable", true)
	}
}

// ScanAndQuarantine is a function-shaped alias useful to adapters that keep
// the guard as a dependency rather than a concrete value.
func (guard *ContentOutputGuard) ScanAndQuarantine(ctx context.Context, request OutputRequest) (OutputResult, error) {
	return guard.Check(ctx, request)
}
