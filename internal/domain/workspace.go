package domain

import "time"

type Workspace struct {
	ID                string
	Name              string
	RevisionWatermark int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type WorkspaceRedirect struct {
	SourceWorkspaceID string
	TargetWorkspaceID string
	CreatedAt         time.Time
}

type MemoryAlias struct {
	AliasMemoryID     string
	CanonicalMemoryID string
	CreatedAt         time.Time
}
