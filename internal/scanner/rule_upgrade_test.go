package scanner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

func TestRuleUpgradeCandidateIsComparativeOnly(t *testing.T) {
	active := newTestScanner(t)
	rules := reviewedRulesForTest(t)
	candidate, err := LoadCandidateRules(rules, GenerationForRules(rules))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Fingerprint() == "" || candidate.Generation() != GenerationForRules(rules) {
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

func TestRuleUpgradeRejectsEmptyComparativeFixturesAndRelabeling(t *testing.T) {
	rules := reviewedRulesForTest(t)
	candidate, err := LoadCandidateRules(rules, GenerationForRules(rules))
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareCandidate(context.Background(), candidate, nil, time.Second); err == nil {
		t.Fatal("empty comparative fixture set accepted")
	}
	if _, err := LoadCandidateRules(rules, "candidate-relabeled"); err == nil {
		t.Fatal("caller relabeling accepted")
	}
}

func TestRuleUpgradeCandidateBatchIsImmutableAndNonActivating(t *testing.T) {
	rules := reviewedRulesForTest(t)
	candidate, err := LoadCandidateRules(rules, GenerationForRules(rules))
	if err != nil {
		t.Fatal(err)
	}
	identity := candidate.Identity()
	results, err := candidate.ScanBatch(context.Background(), []CandidateBatchItem{
		{ID: "memory-1", Fields: []ports.TextField{{Name: "content", Value: "safe"}}},
		{ID: "memory-2", Fields: []ports.TextField{{Name: "content", Value: testCanary()}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Result.Status != ports.ScanClean || results[1].Result.Status != ports.ScanFinding {
		t.Fatalf("candidate batch results = %+v", results)
	}
	if candidate.Identity() != identity {
		t.Fatal("candidate identity changed after batch scan")
	}
	if _, err := candidate.ScanBatch(context.Background(), nil); err == nil {
		t.Fatal("empty candidate batch accepted")
	}
}

func reviewedRulesForTest(t *testing.T) []byte {
	t.Helper()
	rules, err := reviewedRulesForUse()
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestRuleUpgradeCandidateContainmentAndActiveRecovery(t *testing.T) {
	identity := CandidateIdentity{Generation: "candidate-test", Fingerprint: "fingerprint-test"}
	for _, test := range []struct {
		name   string
		status ports.ScanStatus
		engine fakeEngine
	}{
		{name: "panic", status: ports.ScanPanic, engine: fakeEngine{scan: func(context.Context, string) ([]engineFinding, error) { panic("candidate panic canary") }}},
		{name: "timeout", status: ports.ScanTimeout, engine: fakeEngine{scan: func(ctx context.Context, _ string) ([]engineFinding, error) { <-ctx.Done(); return nil, ctx.Err() }}},
		{name: "error", status: ports.ScanError, engine: fakeEngine{scan: func(context.Context, string) ([]engineFinding, error) {
			return nil, errors.New("candidate engine canary")
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := CandidateRules{identity: identity, engine: test.engine}
			result := candidate.Scan(context.Background(), []ports.TextField{{Name: "content", Value: "fixture"}})
			if result.Status != test.status || result.Generation != identity.Generation {
				t.Fatalf("candidate result = %+v, want %s", result, test.status)
			}
		})
	}
	active := newTestScanner(t)
	if result := active.Scan(context.Background(), []ports.TextField{{Name: "content", Value: "safe reference"}}); result.Status != ports.ScanClean {
		t.Fatalf("active scanner after candidate failures = %s", result.Status)
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

func TestRuleUpgradeRejectsRestrictedSchemaBeforeFileRead(t *testing.T) {
	extension := filepath.Join(t.TempDir(), "extension.toml")
	if err := os.WriteFile(extension, []byte(`title = "extension"
[[rules]]
id = "extension"
regex = "extension"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		prefix string
	}{
		{name: "dotted extend path", prefix: "extend.path = \"" + extension + "\"\n"},
		{name: "inline extend path", prefix: "extend = { path = \"" + extension + "\" }\n"},
		{name: "prefilter expression", prefix: "prefilter = \"true\"\n"},
		{name: "rule filter expression", prefix: "filter = \"true\"\n"},
		{name: "rule path", prefix: "path = \"secret\"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rules := []byte(test.prefix + `title = "candidate"
[[rules]]
id = "candidate"
regex = "candidate_[A-Z]+"
`)
			if _, err := LoadCandidateRules(rules, GenerationForRules(rules)); !errors.Is(err, ErrInvalidRules) && !errors.Is(err, ErrUnsafeRules) {
				t.Fatalf("restricted rule schema error = %v", err)
			}
			if _, err := os.Stat(extension); err != nil {
				t.Fatalf("extension fixture was unexpectedly consumed: %v", err)
			}
		})
	}
}

func TestRuleUpgradeRejectsDottedExtendWithoutReadingFile(t *testing.T) {
	if os.Getenv("TALARIA_RULE_FIFO_HELPER") == "1" {
		rules := []byte("extend.path = \"" + os.Getenv("TALARIA_RULE_FIFO") + "\"\ntitle = \"candidate\"\n[[rules]]\nid = \"candidate\"\nregex = \"candidate_[A-Z]+\"\n")
		if _, err := LoadCandidateRules(rules, GenerationForRules(rules)); err == nil {
			os.Exit(2)
		}
		return
	}
	fifo := filepath.Join(t.TempDir(), "extension.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRuleUpgradeRejectsDottedExtendWithoutReadingFile$", "-test.count=1")
	command.Env = append(os.Environ(), "TALARIA_RULE_FIFO_HELPER=1", "TALARIA_RULE_FIFO="+fifo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("candidate validation read extension FIFO: %v\n%s", err, output)
	}
}

func TestRuleUpgradeMalformedRulesCannotExitProcess(t *testing.T) {
	if os.Getenv("TALARIA_RULE_PROCESS_HELPER") == "1" {
		rules := []byte(`title = "malformed"
[[rules]]
id = "malformed"
regex = "["
`)
		if _, err := LoadCandidateRules(rules, GenerationForRules(rules)); err == nil {
			os.Exit(2)
		}
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRuleUpgradeMalformedRulesCannotExitProcess$", "-test.count=1")
	command.Env = append(os.Environ(), "TALARIA_RULE_PROCESS_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("malformed rule helper exited: %v\n%s", err, output)
	}
}

func TestRuleUpgradeMalformedExpressionCannotExitProcess(t *testing.T) {
	if os.Getenv("TALARIA_RULE_EXPRESSION_HELPER") == "1" {
		rules := []byte(`title = "malformed expression"
prefilter = "this is not a Talaria rule field"
[[rules]]
id = "expression"
regex = "expression"
`)
		if _, err := LoadCandidateRules(rules, GenerationForRules(rules)); err == nil {
			os.Exit(2)
		}
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRuleUpgradeMalformedExpressionCannotExitProcess$", "-test.count=1")
	command.Env = append(os.Environ(), "TALARIA_RULE_EXPRESSION_HELPER=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("malformed expression helper exited: %v\n%s", err, output)
	}
}

func testCanary() string {
	return "github_pat_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
}
