package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	betterconfig "github.com/betterleaks/betterleaks/config"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type CandidateRules struct {
	generation  string
	fingerprint string
	engine      scanEngine
}

func (candidate CandidateRules) Generation() string  { return candidate.generation }
func (candidate CandidateRules) Fingerprint() string { return candidate.fingerprint }

func LoadCandidateRules(contents []byte, generation string) (CandidateRules, error) {
	if strings.TrimSpace(generation) == "" || len(generation) > 128 {
		return CandidateRules{}, ErrInvalidRules
	}
	if unsafeRuleConfiguration(contents) {
		return CandidateRules{}, ErrUnsafeRules
	}
	configuration, err := betterconfig.ParseTOML(contents, "candidate:talaria-rules")
	if err != nil || configuration == nil || len(configuration.Rules) == 0 {
		return CandidateRules{}, ErrInvalidRules
	}
	if configuration.Extend.Path != "" || configuration.Extend.URL != "" || configuration.Extend.UseDefault {
		return CandidateRules{}, ErrUnsafeRules
	}
	for _, rule := range configuration.Rules {
		if rule.ValidateExpr != "" {
			return CandidateRules{}, ErrUnsafeRules
		}
	}
	engine, err := newBetterleaksEngine(context.Background(), contents)
	if err != nil {
		return CandidateRules{}, err
	}
	fingerprint := sha256.Sum256(contents)
	return CandidateRules{
		generation: generation, fingerprint: hex.EncodeToString(fingerprint[:]), engine: engine,
	}, nil
}

func unsafeRuleConfiguration(contents []byte) bool {
	normalized := strings.ToLower(string(contents))
	for _, forbidden := range []string{
		"validate =", "validate=", "http.", "https://", "env(", "[extend]", "provider",
	} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return false
}

type ComparativeFixture struct {
	Name     string
	Fields   []ports.TextField
	Expected ports.ScanStatus
}

func CompareCandidate(ctx context.Context, candidate CandidateRules, fixtures []ComparativeFixture, timeout time.Duration) error {
	if candidate.engine == nil || candidate.Generation() == "" || candidate.Fingerprint() == "" || timeout <= 0 {
		return ErrInvalidRules
	}
	scanner := newScannerWithEngine(Config{Generation: candidate.Generation(), Timeout: timeout}, candidate.engine)
	for _, fixture := range fixtures {
		result := scanner.Scan(ctx, fixture.Fields)
		if result.Status != fixture.Expected {
			return fmt.Errorf("%w: %s", ErrComparativeFailed, fixture.Name)
		}
		if result.Status != ports.ScanClean && result.Status != ports.ScanFinding {
			return errors.Join(ErrComparativeFailed, errors.New("uncertain candidate result"))
		}
	}
	return nil
}
