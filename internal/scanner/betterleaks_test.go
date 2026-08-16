package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
		result := scanner.Scan(context.Background(), []ports.TextField{{Name: field, Value: fixture.Canary}})
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
