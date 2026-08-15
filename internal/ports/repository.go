package ports

import (
	"context"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type FTSRow struct {
	MemoryID string
	Eligible bool
	Title    string
	Content  string
	Tags     string
}

type OutboxEvent struct {
	ScopeID           string
	RevisionWatermark int64
	CreatedAt         time.Time
}

type MemoryTx interface {
	CreateMemory(ctx context.Context, memory domain.Memory) error
	CreateRevision(ctx context.Context, revision domain.MemoryRevision) error
	MoveCurrentRevision(ctx context.Context, memoryID, expectedRevisionID string, revision domain.MemoryRevision) error
	ReplaceFTSRow(ctx context.Context, row *FTSRow) error
	AppendOutbox(ctx context.Context, event OutboxEvent) error
}

type MemoryRepository interface {
	WithTx(ctx context.Context, operation func(MemoryTx) error) error
	ReadCurrent(ctx context.Context, memoryID string) (domain.Memory, domain.MemoryRevision, error)
	RebuildFTS(ctx context.Context) error
}
