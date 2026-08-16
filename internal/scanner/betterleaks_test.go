package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

type boundaryFixture struct {
	Fields []string `json:"fields"`
	Canary string   `json:"canary"`
}

func TestBoundaryMatrix(t *testing.T) {
	var fixture boundaryFixture
	testutil.ReadJSONFixture(t, &fixture, "secrets", "boundary-matrix.json")
	scanner := newTestScanner(t)

	for _, field := range fixture.Fields {
		result := scanner.Scan(context.Background(), []ports.TextField{{Name: ports.FieldIdentifier(field), Value: fixture.Canary}})
		if result.Status != ports.ScanFinding {
			t.Errorf("boundary %s status = %s, want finding", field, result.Status)
			continue
		}
		if len(result.Findings) == 0 || result.Findings[0].RuleID == "" {
			t.Errorf("boundary %s returned no safe finding metadata", field)
		}
	}

	clean := scanner.Scan(context.Background(), []ports.TextField{{Name: "content", Value: "store the credential in the system keychain"}})
	if clean.Status != ports.ScanClean || len(clean.Findings) != 0 {
		t.Fatalf("clean boundary status = %s", clean.Status)
	}
}

func TestBoundaryResultNeverContainsCanary(t *testing.T) {
	var fixture boundaryFixture
	testutil.ReadJSONFixture(t, &fixture, "secrets", "boundary-matrix.json")
	result := newTestScanner(t).Scan(context.Background(), []ports.TextField{{Name: "error", Value: fixture.Canary}})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), fixture.Canary) {
		t.Fatal("scan result retained canary")
	}
}

func TestScannerRejectsUntrustedMetadataWithoutEchoingIt(t *testing.T) {
	result := newTestScanner(t).Scan(context.Background(), []ports.TextField{{Name: ports.FieldIdentifier("content\ncanary"), Value: testCanary()}})
	if result.Status != ports.ScanUncertain || len(result.Findings) != 0 {
		t.Fatalf("untrusted field metadata result = %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "content\\ncanary") {
		t.Fatal("untrusted field metadata echoed")
	}
}

func TestScannerRejectsUntrustedRuleAndBatchMetadata(t *testing.T) {
	scanner := newScannerWithEngine(Config{Generation: "test", Timeout: time.Second}, fakeEngine{scan: func(context.Context, string) ([]engineFinding, error) {
		return []engineFinding{{RuleID: "rule\ncanary", Start: 0, End: 1}}, nil
	}})
	if result := scanner.Scan(context.Background(), []ports.TextField{{Name: ports.FieldContent, Value: "fixture"}}); result.Status != ports.ScanUncertain {
		t.Fatalf("unsafe rule metadata status = %s", result.Status)
	}
	rules := reviewedRulesForTest(t)
	candidate, err := LoadCandidateRules(rules, GenerationForRules(rules))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.ScanBatch(context.Background(), []CandidateBatchItem{{ID: "memory canary", Fields: []ports.TextField{{Name: ports.FieldContent, Value: "safe"}}}}); err == nil {
		t.Fatal("content-tainted batch identifier accepted")
	}
}

type fakeEngine struct {
	scan func(context.Context, string) ([]engineFinding, error)
}

func (engine fakeEngine) Scan(ctx context.Context, value string) ([]engineFinding, error) {
	return engine.scan(ctx, value)
}

func TestBoundaryContainment(t *testing.T) {
	t.Run("panic", func(t *testing.T) {
		scanner := newScannerWithEngine(Config{Generation: "test", Timeout: time.Second}, fakeEngine{scan: func(context.Context, string) ([]engineFinding, error) {
			panic("canary panic detail")
		}})
		if got := scanner.Scan(context.Background(), []ports.TextField{{Name: "content", Value: "secret"}}).Status; got != ports.ScanPanic {
			t.Fatalf("panic status = %s", got)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		scanner := newScannerWithEngine(Config{Generation: "test", Timeout: time.Millisecond}, fakeEngine{scan: func(ctx context.Context, _ string) ([]engineFinding, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}})
		if got := scanner.Scan(context.Background(), []ports.TextField{{Name: "content", Value: "secret"}}).Status; got != ports.ScanTimeout {
			t.Fatalf("timeout status = %s", got)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if got := newTestScanner(t).Scan(ctx, []ports.TextField{{Name: "content", Value: "safe"}}).Status; got != ports.ScanCancellation {
			t.Fatalf("cancellation status = %s", got)
		}
	})

	t.Run("engine error", func(t *testing.T) {
		scanner := newScannerWithEngine(Config{Generation: "test", Timeout: time.Second}, fakeEngine{scan: func(context.Context, string) ([]engineFinding, error) {
			return nil, errors.New("canary engine detail")
		}})
		if got := scanner.Scan(context.Background(), []ports.TextField{{Name: "content", Value: "secret"}}).Status; got != ports.ScanError {
			t.Fatalf("error status = %s", got)
		}
	})
}

func newTestScanner(t *testing.T) *Scanner {
	t.Helper()
	scanner, err := New(Config{Generation: ReviewedRuleGeneration, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return scanner
}

func TestNoEgressEnvironment(t *testing.T) {
	disabled, err := ParseEnvironment(map[string]string{"TALARIA_SCANNER_NETWORK": "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Network != NetworkDisabled {
		t.Fatalf("network mode = %q", disabled.Network)
	}
	if _, err := ParseEnvironment(map[string]string{"TALARIA_SCANNER_NETWORK": "enabled"}); err == nil {
		t.Fatal("network scanner mode accepted")
	}
	if _, err := ParseEnvironment(map[string]string{"TALARIA_SCANNER_NETWORK": ""}); err == nil {
		t.Fatal("empty network scanner mode accepted")
	}
}

func TestReviewedRuleGenerationRejectsCallerRelabeling(t *testing.T) {
	if _, err := New(Config{Generation: "betterleaks-v1.7.4-rules-caller-label", Timeout: time.Second}); err == nil {
		t.Fatal("caller-supplied active rule label accepted")
	}
}

func TestNoEgressSource(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		contents, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{`"net/http"`, `http.Get(`, `http.Post(`, `net.Dial(`} {
			if strings.Contains(string(contents), forbidden) {
				t.Errorf("scanner source %s contains outbound primitive %s", file.Name(), forbidden)
			}
		}
	}
}

func TestNoEgressProcessBoundary(t *testing.T) {
	if os.Getenv("TALARIA_SCANNER_PROCESS_HELPER") == "1" {
		configuration, err := ParseEnvironment(map[string]string{"TALARIA_SCANNER_NETWORK": "disabled"})
		if err != nil || configuration.Network != NetworkDisabled {
			os.Exit(2)
		}
		result := newTestScanner(t).Scan(context.Background(), []ports.TextField{{Name: "content", Value: "safe process-boundary fixture"}})
		if result.Status != ports.ScanClean {
			os.Exit(3)
		}
		return
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
			connection <- struct{}{}
		}
	}()

	proxy := "http://" + listener.Addr().String()
	command := exec.Command(os.Args[0], "-test.run=^TestNoEgressProcessBoundary$", "-test.count=1")
	command.Env = append(os.Environ(),
		"TALARIA_SCANNER_PROCESS_HELPER=1",
		"TALARIA_SCANNER_NETWORK=disabled",
		"HTTP_PROXY="+proxy,
		"HTTPS_PROXY="+proxy,
		"ALL_PROXY="+proxy,
		"NO_PROXY=",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("scanner subprocess failed: %v; output=%s", err, output)
	}
	select {
	case <-connection:
		t.Fatal("Betterleaks transitive scanner attempted outbound connection")
	case <-time.After(250 * time.Millisecond):
	}
}
