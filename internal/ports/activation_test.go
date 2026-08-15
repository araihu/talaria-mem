package ports

import (
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

func TestActivationJournalPhaseContract(t *testing.T) {
	t.Parallel()
	var fixture struct {
		Case   string   `json:"case"`
		Reject []string `json:"reject"`
	}
	testutil.ReadJSONFixture(t, &fixture, "domain", "unsafe-follow-or-invalid-phase.json")
	if fixture.Case != "unsafe-follow-or-invalid-phase" || len(fixture.Reject) != 3 {
		t.Fatalf("unexpected activation fixture: %+v", fixture)
	}

	valid := []ActivationPhase{
		ActivationPending,
		ActivationQuiesced,
		ActivationRescanning,
		ActivationQuarantining,
		ActivationProjectionRebuild,
		ActivationActive,
		ActivationCandidateDiscarded,
		ActivationLiveMutationStarted,
		ActivationFailed,
		ActivationRollback,
	}
	for _, phase := range valid {
		if !phase.Valid() {
			t.Errorf("phase %q invalid", phase)
		}
	}

	beforeMutation := ActivationRecord{Phase: ActivationRescanning}
	if err := ValidateActivationTransition(beforeMutation, ActivationRollback); err != nil {
		t.Fatalf("pre-mutation rollback rejected: %v", err)
	}
	afterMutation := ActivationRecord{Phase: ActivationLiveMutationStarted, LiveMutationStarted: true}
	if err := ValidateActivationTransition(afterMutation, ActivationRollback); err == nil {
		t.Fatal("post-mutation rollback accepted")
	}
	if err := ValidateActivationTransition(afterMutation, ActivationRescanning); err != nil {
		t.Fatalf("post-mutation resume rejected: %v", err)
	}

	record := ActivationRecord{SafeError: "scanner unavailable", ResumeCursor: "cursor-1"}
	if err := record.ValidateNoContent(); err != nil {
		t.Fatalf("safe journal record rejected: %v", err)
	}
}
