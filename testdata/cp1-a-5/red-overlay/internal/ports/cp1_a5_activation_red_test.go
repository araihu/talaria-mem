package ports

import (
	"strings"
	"testing"
)

// T2 RED binds rollback terminality and canonical candidate identity to the
// rejected activation protocol source.
func TestActivationJournalCP1A5RejectsUnsafeRecoveryAndIdentity(t *testing.T) {
	if err := ValidateActivationTransition(
		ActivationRecord{Phase: ActivationRollback},
		ActivationActive,
	); err == nil {
		t.Fatal("rollback was allowed to restore active readiness")
	}
	digest := strings.Repeat("a", 64)
	if err := (ActivationRecord{CandidateRuleFingerprint: digest}).ValidateNoContent(); err == nil {
		t.Fatal("orphan candidate fingerprint was accepted")
	}
	upper := strings.Repeat("A", 64)
	if err := (ActivationRecord{CandidateGeneration: "candidate-" + upper, CandidateRuleFingerprint: upper}).ValidateNoContent(); err == nil {
		t.Fatal("uppercase candidate identity was accepted")
	}
}
