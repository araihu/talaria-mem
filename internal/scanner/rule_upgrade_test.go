package scanner

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

func TestRuleUpgradeCandidateIsComparativeOnly(t *testing.T) {
	var fixture struct {
		Boundary []string `json:"boundary"`
		Expected string   `json:"expected"`
	}
	testutil.ReadJSONFixture(t, &fixture, "secrets", "canary-in-log-or-error.json")
	if len(fixture.Boundary) != 2 || fixture.Expected == "" {
		t.Fatalf("unexpected scanner green fixture: %+v", fixture)
	}
	active := newTestScanner(t)
	candidate, err := LoadCandidateRules(ReviewedRules, "candidate-v2")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Fingerprint() == "" || candidate.Generation() != "candidate-v2" {
		t.Fatal("candidate identity incomplete")
	}
	fixtures := []ComparativeFixture{
		{Name: "clean", Fields: []ports.TextField{{Name: "content", Value: "safe reference"}}, Expected: ports.ScanClean},
		{Name: "finding", Fields: []ports.TextField{{Name: "content", Value: testCanary()}}, Expected: ports.ScanFinding},
	}
	if err := CompareCandidate(context.Background(), candidate, fixtures, time.Second); err != nil {
		t.Fatal(err)
	}
	if active.Generation() != ReviewedRuleGeneration {
		t.Fatal("candidate comparison activated candidate rules")
	}
}

func TestRuleUpgradeRejectsNetworkValidation(t *testing.T) {
	unsafe := []byte(`title = "unsafe"
[[rules]]
id = "unsafe"
regex = '''token_[A-Z0-9]{20}'''
validate = '''http.get("https://example.com")'''
`)
	if _, err := LoadCandidateRules(unsafe, "candidate-unsafe"); err == nil {
		t.Fatal("network validation rule accepted")
	}
}

func testCanary() string {
	return "github_pat_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
}
