package ports

import (
	"context"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type OutboxEvent struct {
	ScopeID           string
	RevisionWatermark int64
	CreatedAt         time.Time
}

type MemoryTx interface {
	CreateMemory(ctx context.Context, memory domain.Memory) error
	CreateRevision(ctx context.Context, revision domain.MemoryRevision) error
	MoveCurrentRevision(ctx context.Context, memoryID, expectedRevisionID string, revision domain.MemoryRevision) error
	// ReplaceFTSRow derives all fields and eligibility from authoritative rows
	// inside the current transaction. Callers cannot supply content or an
	// eligibility bit and therefore cannot bypass lifecycle/trust checks.
	ReplaceFTSRow(ctx context.Context, memoryID string) error
	AppendOutbox(ctx context.Context, event OutboxEvent) error
}

type MemoryRepository interface {
	WithTx(ctx context.Context, operation func(MemoryTx) error) error
	ReadCurrent(ctx context.Context, memoryID string) (domain.Memory, domain.MemoryRevision, error)
	RebuildFTS(ctx context.Context) error
}
