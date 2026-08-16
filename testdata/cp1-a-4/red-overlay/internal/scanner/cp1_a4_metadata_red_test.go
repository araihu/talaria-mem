package scanner

import (
	"context"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

type cp1A4MetadataFixture struct {
	Field   string `json:"field"`
	RuleID  string `json:"rule_id"`
	BatchID string `json:"batch_id"`
}

// T4 RED is bound to metadata containment. The rejected source echoed
// caller-controlled field/rule/batch identifiers into scanner results.
func TestRuleUpgradeCP1A4MetadataCannotEchoCallerContent(t *testing.T) {
	var fixture cp1A4MetadataFixture
	testutil.ReadJSONFixture(t, &fixture, "secrets", "cp1-a-4-metadata-canary.json")
	scanner := newScannerWithEngine(Config{Generation: "test", Timeout: 1_000_000_000}, fakeEngine{scan: func(context.Context, string) ([]engineFinding, error) {
		return []engineFinding{{RuleID: fixture.RuleID, Start: 0, End: 1}}, nil
	}})
	result := scanner.Scan(context.Background(), []ports.TextField{{Name: fixture.Field, Value: "fixture"}})
	if result.Status != ports.ScanUncertain || len(result.Findings) != 0 {
		t.Fatalf("unsafe metadata result = %+v", result)
	}
	rules := reviewedRulesForTest(t)
	candidate, err := LoadCandidateRules(rules, GenerationForRules(rules))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.ScanBatch(context.Background(), []CandidateBatchItem{{ID: fixture.BatchID, Fields: []ports.TextField{{Name: "content", Value: "safe"}}}}); err == nil {
		t.Fatal("caller-controlled batch identifier accepted")
	}
}
