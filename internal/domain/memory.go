package domain

import "time"

type MemoryKind string

const (
	MemoryKindState               MemoryKind = "state"
	MemoryKindProcedure           MemoryKind = "procedure"
	MemoryKindFailure             MemoryKind = "failure"
	MemoryKindStandingInstruction MemoryKind = "standing_instruction"
)

func (kind MemoryKind) Valid() bool {
	switch kind {
	case MemoryKindState, MemoryKindProcedure, MemoryKindFailure, MemoryKindStandingInstruction:
		return true
	default:
		return false
	}
}

type Trust string

const (
	TrustVerified   Trust = "verified"
	TrustGenerated  Trust = "generated"
	TrustUnverified Trust = "unverified"
)

func (trust Trust) Valid() bool {
	return trust == TrustVerified || trust == TrustGenerated || trust == TrustUnverified
}

type Lifecycle string

const (
	LifecycleActive      Lifecycle = "active"
	LifecycleQuarantined Lifecycle = "quarantined"
	LifecycleForgotten   Lifecycle = "forgotten"
	LifecyclePurged      Lifecycle = "purged"
)

func (lifecycle Lifecycle) Valid() bool {
	switch lifecycle {
	case LifecycleActive, LifecycleQuarantined, LifecycleForgotten, LifecyclePurged:
		return true
	default:
		return false
	}
}

type ResolutionState string

const (
	ResolutionOpen     ResolutionState = "open"
	ResolutionResolved ResolutionState = "resolved"
)

func (state ResolutionState) Valid() bool {
	return state == ResolutionOpen || state == ResolutionResolved
}

type Provenance struct {
	Actor         string
	Source        string
	Labels        []string
	SourceLocator string
}

type RevisionRef struct {
	MemoryID   string
	RevisionID string
	Number     int64
}

type MemoryRevision struct {
	ID              string
	MemoryID        string
	Number          int64
	Kind            MemoryKind
	Title           string
	Content         string
	Tags            []string
	ResolutionState ResolutionState
	Trust           Trust
	Lifecycle       Lifecycle
	Provenance      Provenance
	CreatedAt       time.Time
}

type Memory struct {
	ID                   string
	WorkspaceID          string
	UserGlobal           bool
	Kind                 MemoryKind
	Trust                Trust
	Lifecycle            Lifecycle
	CurrentRevisionID    string
	GeneratedFingerprint string
	Pinned               bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func ValidateResolutionState(kind MemoryKind, state ResolutionState) (ResolutionState, error) {
	if !kind.Valid() {
		return "", NewError(CodeValidation, "invalid memory kind", false)
	}
	if kind != MemoryKindFailure {
		if state != "" {
			return "", NewError(CodeValidation, "resolution state is failure-only", false)
		}
		return "", nil
	}
	if state == "" {
		return ResolutionOpen, nil
	}
	if !state.Valid() {
		return "", NewError(CodeValidation, "invalid resolution state", false)
	}
	return state, nil
}

func ValidateResolutionTransition(kind MemoryKind, from, to ResolutionState) error {
	validatedFrom, err := ValidateResolutionState(kind, from)
	if err != nil {
		return err
	}
	validatedTo, err := ValidateResolutionState(kind, to)
	if err != nil {
		return err
	}
	if kind != MemoryKindFailure || !validatedFrom.Valid() || !validatedTo.Valid() {
		return NewError(CodeValidation, "invalid resolution transition", false)
	}
	return nil
}
