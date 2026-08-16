package ports

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ActivationPhase is the durable, non-content state machine used while a
// scanner rule generation is compared, activated, or recovered. The graph is
// intentionally explicit: accepting arbitrary phase changes makes a crash
// look like a successful activation and can re-expose quarantined content.
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

// ActivationEpochAudit is the reconstructible, non-content history for one
// candidate upgrade. The live journal intentionally keeps only the current
// operation; this projection retains each epoch identity and its monotonic
// counters across restarts and later upgrades.
type ActivationEpochAudit struct {
	ActivationEpoch          string
	ActiveGeneration         string
	CandidateGeneration      string
	CandidateRuleFingerprint string
	Phase                    ActivationPhase
	RevisionWatermark        int64
	LiveMutationStarted      bool
	MutationCounts           ActivationMutationCounts
	Completed                bool
	UpdatedAt                time.Time
}

type ActivationRecord struct {
	Version int64
	// ActivationEpoch identifies one complete candidate upgrade operation.
	// It changes only on the atomic active->pending upgrade-start edge and is
	// retained in the terminal active record for historical reconstruction.
	ActivationEpoch          string
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
	ComparativeVerified      bool
	RescanVerified           bool
	MutationVerified         bool
	ProjectionVerified       bool
	ReadinessVerified        bool
	// CandidateDiscardReverified is an explicit, durable re-verification gate
	// for the only pre-live recovery state that may restore readiness.
	CandidateDiscardReverified bool
	// Historical fields preserve the prior operation's live boundary and
	// counters when a new active->pending cycle resets its working state.
	HistoricalLiveMutationStarted bool
	HistoricalMutationCounts      ActivationMutationCounts
	UpdatedAt                     time.Time
}

var (
	ErrActivationCAS        = errors.New("activation journal compare-and-swap conflict")
	ErrActivationRegression = errors.New("activation journal monotonicity violation")
)

// activationEdges is the only set of forward/recovery transitions. A phase
// may be repeated for an idempotent restart, but no unlisted edge is legal.
// Active -> pending is the one deliberate restart edge: it starts a new
// candidate and must introduce both candidate identity fields atomically.
var activationEdges = map[ActivationPhase]map[ActivationPhase]bool{
	ActivationPending: {
		ActivationQuiesced: true, ActivationFailed: true,
		ActivationCandidateDiscarded: true, ActivationRollback: true,
	},
	ActivationQuiesced: {
		ActivationRescanning: true, ActivationFailed: true,
		ActivationCandidateDiscarded: true, ActivationRollback: true,
	},
	ActivationRescanning: {
		ActivationQuarantining: true, ActivationFailed: true,
		ActivationCandidateDiscarded: true, ActivationRollback: true,
	},
	ActivationQuarantining: {
		ActivationProjectionRebuild: true, ActivationLiveMutationStarted: true,
		ActivationFailed: true, ActivationCandidateDiscarded: true,
	},
	ActivationProjectionRebuild: {
		ActivationActive: true, ActivationLiveMutationStarted: true,
		ActivationFailed: true, ActivationCandidateDiscarded: true,
	},
	ActivationActive: {
		ActivationPending: true, ActivationLiveMutationStarted: true, ActivationFailed: true,
	},
	ActivationLiveMutationStarted: {
		ActivationQuarantining: true, ActivationProjectionRebuild: true,
		ActivationActive: true, ActivationFailed: true,
	},
	ActivationFailed: {
		ActivationCandidateDiscarded: true, ActivationRollback: true,
		ActivationQuarantining: true, ActivationProjectionRebuild: true,
	},
	ActivationCandidateDiscarded: {ActivationActive: true},
	// Rollback is a terminal, readiness-blocking audit state. Recovery must
	// create a fresh candidate_discarded record and explicitly re-verify it;
	// there is no rollback -> active bypass.
	ActivationRollback: {},
}

// ValidateActivationTransition accepts either a complete next record or a
// phase for compatibility with the original port probe. New callers should
// pass an ActivationRecord so identity, CAS-adjacent, and monotonic fields
// are checked together.
func ValidateActivationTransition(previous ActivationRecord, next any) error {
	var nextRecord ActivationRecord
	switch value := next.(type) {
	case ActivationRecord:
		nextRecord = value
	case *ActivationRecord:
		if value == nil {
			return NewPortContractError("nil activation record")
		}
		nextRecord = *value
	case ActivationPhase:
		nextRecord = previous
		nextRecord.Phase = value
	case *ActivationPhase:
		if value == nil {
			return NewPortContractError("nil activation phase")
		}
		nextRecord = previous
		nextRecord.Phase = *value
	default:
		return NewPortContractError("invalid activation transition value")
	}
	return ValidateActivationRecordTransition(previous, nextRecord)
}

// ValidateActivationRecordTransition enforces phase progression, generation
// identity, ordered verification gates, monotonic watermarks/cursors/counters,
// and the irreversible live mutation boundary.
func ValidateActivationRecordTransition(previous, next ActivationRecord) error {
	if err := next.ValidateNoContent(); err != nil {
		return err
	}
	if previous.Phase != "" && !previous.Phase.Valid() {
		return NewPortContractError("invalid previous activation phase")
	}
	if !next.Phase.Valid() {
		return NewPortContractError("invalid activation phase")
	}
	if next.Version < previous.Version {
		return fmt.Errorf("%w: activation journal version rewound", ErrActivationRegression)
	}
	if previous.Phase == "" {
		if next.Phase != ActivationPending {
			return NewPortContractError("activation must start pending")
		}
	} else if previous.Phase != next.Phase && !activationEdges[previous.Phase][next.Phase] {
		return NewPortContractError(fmt.Sprintf("invalid activation transition %s to %s", previous.Phase, next.Phase))
	}

	previousLive := previous.LiveMutationStarted || previous.Phase == ActivationLiveMutationStarted
	nextLive := next.LiveMutationStarted || next.Phase == ActivationLiveMutationStarted
	allowCycleReset := previous.Phase == ActivationActive && next.Phase == ActivationPending &&
		previous.CandidateGeneration == "" && next.CandidateGeneration != "" &&
		previous.ActivationEpoch != next.ActivationEpoch
	if previousLive && !nextLive && !allowCycleReset {
		return NewPortContractError("live mutation flag cannot rewind")
	}
	if previousLive && (next.Phase == ActivationRollback || next.Phase == ActivationCandidateDiscarded) {
		return NewPortContractError("activation cannot roll back after live mutation")
	}
	allowGateReset := previous.Phase == ActivationActive && next.Phase == ActivationPending &&
		previous.CandidateGeneration == "" && next.CandidateGeneration != "" &&
		previous.ActivationEpoch != next.ActivationEpoch
	for _, gate := range []struct {
		was  bool
		now  bool
		name string
	}{
		{previous.ComparativeVerified, next.ComparativeVerified, "comparative verification"},
		{previous.RescanVerified, next.RescanVerified, "rescan verification"},
		{previous.MutationVerified, next.MutationVerified, "mutation verification"},
		{previous.ProjectionVerified, next.ProjectionVerified, "projection verification"},
		{previous.ReadinessVerified, next.ReadinessVerified, "readiness verification"},
	} {
		if gate.was && !gate.now && !allowGateReset {
			return fmt.Errorf("%w: %s rewound", ErrActivationRegression, gate.name)
		}
	}
	if next.Phase == ActivationLiveMutationStarted && !next.LiveMutationStarted {
		return NewPortContractError("live mutation phase requires live mutation flag")
	}
	if !previousLive && next.LiveMutationStarted &&
		next.Phase != ActivationLiveMutationStarted && next.Phase != ActivationQuarantining && next.Phase != ActivationProjectionRebuild {
		return NewPortContractError("live mutation flag requires an active mutation phase")
	}

	if !allowCycleReset && next.RevisionWatermark < previous.RevisionWatermark {
		return fmt.Errorf("%w: activation revision watermark rewound", ErrActivationRegression)
	}
	if !allowCycleReset && (next.MutationCounts.Quarantined < previous.MutationCounts.Quarantined ||
		next.MutationCounts.FTSRemoved < previous.MutationCounts.FTSRemoved ||
		next.MutationCounts.OutboxAdded < previous.MutationCounts.OutboxAdded ||
		next.MutationCounts.Projected < previous.MutationCounts.Projected) {
		return fmt.Errorf("%w: activation mutation counter rewound", ErrActivationRegression)
	}
	if !allowCycleReset {
		if err := validateMonotonicCursor(previous.LastProcessedID, next.LastProcessedID, "last processed ID"); err != nil {
			return err
		}
		if err := validateMonotonicCursor(previous.ResumeCursor, next.ResumeCursor, "resume cursor"); err != nil {
			return err
		}
	} else if !next.HistoricalLiveMutationStarted && previous.LiveMutationStarted {
		return NewPortContractError("active upgrade reset must preserve live mutation history")
	}
	if allowCycleReset {
		if previous.LiveMutationStarted && (!next.HistoricalLiveMutationStarted ||
			next.HistoricalMutationCounts.Quarantined < previous.MutationCounts.Quarantined ||
			next.HistoricalMutationCounts.FTSRemoved < previous.MutationCounts.FTSRemoved ||
			next.HistoricalMutationCounts.OutboxAdded < previous.MutationCounts.OutboxAdded ||
			next.HistoricalMutationCounts.Projected < previous.MutationCounts.Projected) {
			return NewPortContractError("active upgrade reset lost historical mutation state")
		}
	}
	if previous.HistoricalLiveMutationStarted && !next.HistoricalLiveMutationStarted {
		return fmt.Errorf("%w: historical live mutation flag rewound", ErrActivationRegression)
	}
	if next.HistoricalMutationCounts.Quarantined < previous.HistoricalMutationCounts.Quarantined ||
		next.HistoricalMutationCounts.FTSRemoved < previous.HistoricalMutationCounts.FTSRemoved ||
		next.HistoricalMutationCounts.OutboxAdded < previous.HistoricalMutationCounts.OutboxAdded ||
		next.HistoricalMutationCounts.Projected < previous.HistoricalMutationCounts.Projected {
		return fmt.Errorf("%w: historical activation counter rewound", ErrActivationRegression)
	}

	previousCandidate, previousFingerprint := previous.CandidateGeneration != "", previous.CandidateRuleFingerprint != ""
	nextCandidate, nextFingerprint := next.CandidateGeneration != "", next.CandidateRuleFingerprint != ""
	if previousCandidate != previousFingerprint || nextCandidate != nextFingerprint {
		return NewPortContractError("candidate generation and fingerprint must be paired")
	}

	// Same-phase CAS writes may update counters, cursors, and safe state, but
	// never rule identity. This blocks the two-write active->active promotion
	// bypass that could otherwise introduce a candidate without quiescing.
	if previous.Phase == next.Phase &&
		(previous.ActiveGeneration != next.ActiveGeneration ||
			previous.CandidateGeneration != next.CandidateGeneration ||
			previous.CandidateRuleFingerprint != next.CandidateRuleFingerprint ||
			previous.ActivationEpoch != next.ActivationEpoch) {
		return NewPortContractError("activation identity changed during same-phase CAS")
	}

	// Candidate identity can be introduced only by the atomic active->pending
	// upgrade-start edge. It cannot appear on any other edge or be relabeled.
	if !previousCandidate && nextCandidate {
		if previous.Phase != ActivationActive || next.Phase != ActivationPending || (previous.LiveMutationStarted && !allowCycleReset) ||
			previous.ActiveGeneration != next.ActiveGeneration || next.ActivationEpoch == "" ||
			next.ActivationEpoch == previous.ActivationEpoch {
			return NewPortContractError("candidate identity introduced outside upgrade start")
		}
		if next.LiveMutationStarted || next.ComparativeVerified || next.RescanVerified || next.MutationVerified ||
			next.ProjectionVerified || next.ReadinessVerified {
			return NewPortContractError("upgrade start must begin unverified and quiescent")
		}
		if next.CandidateDiscardReverified {
			return NewPortContractError("upgrade start must reset candidate discard re-verification")
		}
		if allowCycleReset && (next.LastProcessedID != "" || next.ResumeCursor != "" ||
			next.MutationCounts != (ActivationMutationCounts{})) {
			return NewPortContractError("upgrade start must reset cycle-local cursors and counters")
		}
	}
	if previousCandidate && nextCandidate && previous.ActivationEpoch != next.ActivationEpoch {
		return NewPortContractError("activation epoch changed during candidate operation")
	}
	if previousCandidate && nextCandidate &&
		(previous.CandidateGeneration != next.CandidateGeneration || previous.CandidateRuleFingerprint != next.CandidateRuleFingerprint) {
		return NewPortContractError("activation candidate identity changed")
	}
	if previousCandidate && !nextCandidate &&
		next.Phase != ActivationActive && next.Phase != ActivationCandidateDiscarded && next.Phase != ActivationRollback {
		return NewPortContractError("activation candidate cleared before terminal phase")
	}

	if next.Phase == ActivationActive && next.CandidateGeneration != "" {
		return NewPortContractError("active phase cannot retain a candidate")
	}
	if next.Phase == ActivationActive && next.ActiveGeneration == "" {
		return NewPortContractError("active phase requires an active generation")
	}
	if next.Phase == ActivationActive && next.ActivationEpoch == "" {
		return NewPortContractError("active phase requires an activation epoch")
	}
	if next.Phase == ActivationActive && previous.Phase != "" && previous.Phase != ActivationProjectionRebuild &&
		previous.Phase != ActivationActive && previous.Phase != ActivationCandidateDiscarded && previous.Phase != ActivationRollback {
		return NewPortContractError("active promotion requires completed projection rebuild")
	}
	if previous.ActiveGeneration != "" && next.ActiveGeneration != previous.ActiveGeneration {
		// Promotion is the only active identity change. It must consume the
		// exact candidate and clear both candidate identity fields atomically.
		if previous.CandidateGeneration == "" || next.Phase != ActivationActive ||
			next.ActiveGeneration != previous.CandidateGeneration || next.CandidateGeneration != "" || next.CandidateRuleFingerprint != "" {
			return NewPortContractError("activation active generation identity changed")
		}
	}

	working := next.Phase == ActivationPending || next.Phase == ActivationQuiesced ||
		next.Phase == ActivationRescanning || next.Phase == ActivationQuarantining ||
		next.Phase == ActivationProjectionRebuild || next.Phase == ActivationLiveMutationStarted || next.Phase == ActivationFailed
	if working && next.CandidateGeneration == "" {
		return NewPortContractError("candidate generation required before activation")
	}
	if (next.Phase == ActivationCandidateDiscarded || next.Phase == ActivationRollback) && next.CandidateGeneration != "" {
		return NewPortContractError("terminal pre-live recovery must clear candidate")
	}
	if next.Phase == ActivationCandidateDiscarded && next.CandidateDiscardReverified &&
		(!next.ProjectionVerified || !next.ReadinessVerified) {
		return NewPortContractError("candidate discard re-verification requires projection and readiness gates")
	}
	if previous.Phase == ActivationCandidateDiscarded && next.Phase == ActivationActive &&
		!next.CandidateDiscardReverified {
		return NewPortContractError("candidate discard recovery requires explicit re-verification")
	}
	if next.Phase == ActivationRollback && next.CandidateDiscardReverified {
		return NewPortContractError("rollback cannot claim candidate re-verification")
	}

	// The durable gates encode the required ordering. A phase name alone is
	// insufficient: a CAS must record comparative/full-rescan completion,
	// mutation-boundary completion, then projection and readiness verification.
	if next.Phase == ActivationQuarantining && (!next.ComparativeVerified || !next.RescanVerified) {
		return NewPortContractError("quarantine requires comparative and full-rescan verification")
	}
	if next.Phase == ActivationProjectionRebuild && (!next.ComparativeVerified || !next.RescanVerified || !next.MutationVerified) {
		return NewPortContractError("projection rebuild requires mutation-boundary verification")
	}
	if next.Phase == ActivationActive && (!next.ComparativeVerified || !next.RescanVerified || !next.MutationVerified ||
		!next.ProjectionVerified || !next.ReadinessVerified) {
		return NewPortContractError("active promotion requires all verification gates")
	}

	// Failed recovery is split at the live-mutation boundary. Before live
	// mutation it may only discard/roll back safely; after live mutation it may
	// resume the monotonic scrub/rebuild path and can never regress to a clean
	// pre-live phase.
	if previous.Phase == ActivationFailed && !previousLive &&
		next.Phase != ActivationFailed && next.Phase != ActivationCandidateDiscarded && next.Phase != ActivationRollback {
		return NewPortContractError("pre-live failed activation must discard or roll back")
	}
	if previous.Phase == ActivationFailed && previousLive &&
		(next.Phase == ActivationPending || next.Phase == ActivationQuiesced || next.Phase == ActivationRescanning ||
			next.Phase == ActivationCandidateDiscarded || next.Phase == ActivationRollback) {
		return NewPortContractError("post-live failed activation cannot regress")
	}

	return nil
}

func validateMonotonicCursor(previous, next, label string) error {
	if previous == "" {
		return nil
	}
	if next == "" {
		return fmt.Errorf("%w: %s rewound", ErrActivationRegression, label)
	}
	if previous == next {
		return nil
	}
	previousNumber, previousOK := trailingNumber(previous)
	nextNumber, nextOK := trailingNumber(next)
	if previousOK && nextOK {
		if nextNumber < previousNumber {
			return fmt.Errorf("%w: %s rewound", ErrActivationRegression, label)
		}
		return nil
	}
	if next < previous {
		return fmt.Errorf("%w: %s rewound", ErrActivationRegression, label)
	}
	return nil
}

func trailingNumber(value string) (int64, bool) {
	end := len(value)
	start := end
	for start > 0 && value[start-1] >= '0' && value[start-1] <= '9' {
		start--
	}
	if start == end {
		return 0, false
	}
	number, err := strconv.ParseInt(value[start:end], 10, 64)
	return number, err == nil
}

func (record ActivationRecord) ValidateNoContent() error {
	if record.Version < 0 || record.RevisionWatermark < 0 {
		return NewPortContractError("activation journal counters are negative")
	}
	if record.Phase != "" && !record.Phase.Valid() {
		return NewPortContractError("invalid activation phase")
	}
	for _, value := range []string{record.ActivationEpoch, record.ActiveGeneration, record.CandidateGeneration, record.CandidateRuleFingerprint} {
		if !utf8.ValidString(value) || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
			return NewPortContractError("activation journal metadata is unsafe")
		}
	}
	for _, value := range []string{record.LastProcessedID, record.ResumeCursor} {
		if !utf8.ValidString(value) || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
			return NewPortContractError("activation journal metadata is unsafe")
		}
	}
	if !utf8.ValidString(record.SafeError) || len(record.SafeError) > 512 || strings.ContainsAny(record.SafeError, "\r\n\x00") {
		return NewPortContractError("activation journal error is too long")
	}
	for _, count := range []int64{
		record.MutationCounts.Quarantined, record.MutationCounts.FTSRemoved,
		record.MutationCounts.OutboxAdded, record.MutationCounts.Projected,
		record.HistoricalMutationCounts.Quarantined, record.HistoricalMutationCounts.FTSRemoved,
		record.HistoricalMutationCounts.OutboxAdded, record.HistoricalMutationCounts.Projected,
	} {
		if count < 0 {
			return NewPortContractError("activation journal mutation count is negative")
		}
	}
	if (record.CandidateGeneration == "") != (record.CandidateRuleFingerprint == "") {
		return NewPortContractError("candidate generation and fingerprint must be paired")
	}
	if record.CandidateGeneration != "" {
		if err := ValidateCandidateIdentity(record.CandidateGeneration, record.CandidateRuleFingerprint); err != nil {
			return err
		}
	}
	return nil
}

// ValidateCandidateIdentity makes the digest the authority for both durable
// candidate labels. A caller cannot introduce a relabelled candidate through
// a CAS or a journal load.
func ValidateCandidateIdentity(generation, fingerprint string) error {
	if generation == "" || fingerprint == "" {
		return NewPortContractError("candidate generation and fingerprint are required")
	}
	if len(fingerprint) != 64 {
		return NewPortContractError("candidate fingerprint must be a SHA-256 digest")
	}
	if fingerprint != strings.ToLower(fingerprint) {
		return NewPortContractError("candidate fingerprint must use lowercase hexadecimal")
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		return NewPortContractError("candidate fingerprint must be hexadecimal")
	}
	if generation != "candidate-"+fingerprint {
		return NewPortContractError("candidate generation is not bound to fingerprint")
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
