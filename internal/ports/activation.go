package ports

import (
	"context"
	"strings"
	"time"
)

type ActivationPhase string

const (
	ActivationPending             ActivationPhase = "pending"
	ActivationQuiesced            ActivationPhase = "quiesced"
	ActivationRescanning          ActivationPhase = "rescanning"
	ActivationQuarantining        ActivationPhase = "quarantining"
	ActivationProjectionRebuild   ActivationPhase = "projection_rebuild"
	ActivationActive              ActivationPhase = "active"
	ActivationCandidateDiscarded  ActivationPhase = "candidate_discarded"
	ActivationLiveMutationStarted ActivationPhase = "live_mutation_started"
	ActivationFailed              ActivationPhase = "failed"
	ActivationRollback            ActivationPhase = "rollback"
)

func (phase ActivationPhase) Valid() bool {
	switch phase {
	case ActivationPending, ActivationQuiesced, ActivationRescanning, ActivationQuarantining,
		ActivationProjectionRebuild, ActivationActive, ActivationCandidateDiscarded,
		ActivationLiveMutationStarted, ActivationFailed, ActivationRollback:
		return true
	default:
		return false
	}
}

type ActivationMutationCounts struct {
	Quarantined int64
	FTSRemoved  int64
	OutboxAdded int64
	Projected   int64
}

type ActivationRecord struct {
	Version                  int64
	ActiveGeneration         string
	CandidateGeneration      string
	Phase                    ActivationPhase
	RevisionWatermark        int64
	CandidateRuleFingerprint string
	LastProcessedID          string
	LiveMutationStarted      bool
	MutationCounts           ActivationMutationCounts
	ResumeCursor             string
	SafeError                string
	UpdatedAt                time.Time
}

func ValidateActivationTransition(previous ActivationRecord, next ActivationPhase) error {
	if !next.Valid() {
		return NewPortContractError("invalid activation phase")
	}
	if (previous.LiveMutationStarted || previous.Phase == ActivationLiveMutationStarted) &&
		(next == ActivationRollback || next == ActivationCandidateDiscarded || next == ActivationPending || next == ActivationQuiesced) {
		return NewPortContractError("activation cannot roll back after live mutation")
	}
	return nil
}

func (record ActivationRecord) ValidateNoContent() error {
	for _, value := range []string{
		record.ActiveGeneration,
		record.CandidateGeneration,
		record.CandidateRuleFingerprint,
		record.LastProcessedID,
		record.ResumeCursor,
		record.SafeError,
	} {
		if len(value) > 1024 || strings.ContainsAny(value, "\r\n") {
			return NewPortContractError("activation journal metadata is unsafe")
		}
	}
	return nil
}

type ActivationJournal interface {
	Load(ctx context.Context) (ActivationRecord, error)
	Store(ctx context.Context, expectedVersion int64, record ActivationRecord) error
	ReadinessBlockers(ctx context.Context) ([]string, error)
}

type PortContractError struct{ message string }

func NewPortContractError(message string) *PortContractError {
	return &PortContractError{message: message}
}
func (err *PortContractError) Error() string { return err.message }
