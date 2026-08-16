package ports

import (
	"strings"
	"testing"
)

// T2 RED binds two rejected activation protections to one executable overlay:
// rollback is terminal, and a loaded candidate cannot contain an orphan or
// uppercase digest identity.
func TestActivationJournalCP1A4RejectsUnsafeRecoveryAndIdentity(t *testing.T) {
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
