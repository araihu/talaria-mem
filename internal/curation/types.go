package curation

import (
	"context"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type Reason string

const (
	ReasonPeriodic   Reason = "periodic"
	ReasonPreCompact Reason = "pre_compact"
	ReasonSessionEnd Reason = "session_end"
	ReasonInline     Reason = "inline"
)

func (reason Reason) Valid() bool {
	switch reason {
	case ReasonPeriodic, ReasonPreCompact, ReasonSessionEnd, ReasonInline:
		return true
	default:
		return false
	}
}

type Candidate struct {
	Kind            domain.MemoryKind
	Title           string
	Content         string
	Tags            []string
	ResolutionState domain.ResolutionState
}

type CurationRequest struct {
	WorkspaceID   string
	Reason        Reason
	Watermark     int64
	AllowedKinds  []domain.MemoryKind
	ThreadLocator []byte
	Snapshot      []byte
}

type CurationResult struct {
	Candidates []Candidate
	Provider   string
	Model      string
}

type Curator interface {
	Curate(context.Context, CurationRequest) (CurationResult, error)
}
