package scanner

import (
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

func TestRuleUpgrade(t *testing.T) {
	var fixture struct {
		CandidateGeneration string `json:"candidate_generation"`
		Expected            string `json:"expected"`
	}
	testutil.ReadJSONFixture(t, &fixture, "secrets", "rule-upgrade-uncertain.json")
	if fixture.CandidateGeneration == "" || fixture.Expected == "" {
		t.Fatalf("unexpected scanner fixture: %+v", fixture)
	}
	if _, err := LoadCandidateRules([]byte("uncertain candidate"), fixture.CandidateGeneration); err == nil {
		t.Fatal("uncertain candidate accepted; expected discard without activation")
	}
}
