package scanner

import (
	"context"
	_ "embed"
	"errors"
	"sync"

	betterconfig "github.com/betterleaks/betterleaks/config"
	"github.com/betterleaks/betterleaks/detect"
	"github.com/betterleaks/betterleaks/sources"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

const ReviewedRuleGeneration = "betterleaks-v1.7.4-talaria-rules-v1"

//go:embed rules/reviewed.toml
var ReviewedRules []byte

type engineFinding struct {
	RuleID string
	Start  int
	End    int
}

type scanEngine interface {
	Scan(ctx context.Context, value string) ([]engineFinding, error)
}

type betterleaksEngine struct {
	detector *detect.Detector
	mu       sync.Mutex
}

func newBetterleaksEngine(ctx context.Context, rules []byte) (*betterleaksEngine, error) {
	configuration, err := betterconfig.ParseTOML(rules, "embedded:talaria-reviewed-rules")
	if err != nil {
		return nil, ErrInvalidRules
	}
	// Zero-value ValidationOptions permanently disables validation workers,
	// environment access, provider checks, and Betterleaks HTTP validation.
	detector := detect.NewDetectorContext(ctx, configuration, detect.ValidationOptions{Enabled: false})
	return &betterleaksEngine{detector: detector}, nil
}

func (engine *betterleaksEngine) Scan(ctx context.Context, value string) ([]engineFinding, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	findings := engine.detector.DetectContext(ctx, sources.Fragment{Raw: value})
	result := make([]engineFinding, 0, len(findings))
	for _, finding := range findings {
		result = append(result, engineFinding{
			RuleID: finding.RuleID,
			Start:  max(0, finding.StartColumn-1),
			End:    max(0, finding.EndColumn),
		})
	}
	return result, nil
}

type Scanner struct {
	configuration Config
	engine        scanEngine
}

func New(configuration Config) (*Scanner, error) {
	if err := configuration.validate(); err != nil {
		return nil, err
	}
	engine, err := newBetterleaksEngine(context.Background(), ReviewedRules)
	if err != nil {
		return nil, err
	}
	return newScannerWithEngine(configuration, engine), nil
}

func newScannerWithEngine(configuration Config, engine scanEngine) *Scanner {
	return &Scanner{configuration: configuration, engine: engine}
}

func (scanner *Scanner) Generation() string { return scanner.configuration.Generation }

func (scanner *Scanner) Scan(ctx context.Context, fields []ports.TextField) ports.ScanResult {
	if err := ctx.Err(); err != nil {
		return ports.ScanResult{Status: ports.ScanCancellation, Generation: scanner.Generation()}
	}
	scanContext, cancel := context.WithTimeout(ctx, scanner.configuration.Timeout)
	defer cancel()

	result := ports.ScanResult{Status: ports.ScanClean, Generation: scanner.Generation()}
	for _, field := range fields {
		findings, status := scanner.scanField(scanContext, field.Value)
		if status != ports.ScanClean {
			if status != ports.ScanFinding {
				return ports.ScanResult{Status: status, Generation: scanner.Generation()}
			}
			result.Status = ports.ScanFinding
		}
		for _, finding := range findings {
			if finding.RuleID == "" || finding.Start < 0 || finding.End < finding.Start {
				return ports.ScanResult{Status: ports.ScanUncertain, Generation: scanner.Generation()}
			}
			result.Findings = append(result.Findings, ports.Finding{
				Field: field.Name, RuleID: finding.RuleID, Start: finding.Start, End: finding.End,
			})
		}
	}
	return result
}

type engineResponse struct {
	findings []engineFinding
	err      error
	panicked bool
}

func (scanner *Scanner) scanField(ctx context.Context, value string) ([]engineFinding, ports.ScanStatus) {
	responses := make(chan engineResponse, 1)
	go func() {
		response := engineResponse{}
		defer func() {
			if recover() != nil {
				response.findings = nil
				response.err = nil
				response.panicked = true
			}
			responses <- response
		}()
		response.findings, response.err = scanner.engine.Scan(ctx, value)
	}()

	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ports.ScanTimeout
		}
		return nil, ports.ScanCancellation
	case response := <-responses:
		if response.panicked {
			return nil, ports.ScanPanic
		}
		if response.err != nil {
			if errors.Is(response.err, context.DeadlineExceeded) {
				return nil, ports.ScanTimeout
			}
			if errors.Is(response.err, context.Canceled) {
				return nil, ports.ScanCancellation
			}
			return nil, ports.ScanError
		}
		if len(response.findings) > 0 {
			return response.findings, ports.ScanFinding
		}
		return nil, ports.ScanClean
	}
}

var _ ports.Scanner = (*Scanner)(nil)
