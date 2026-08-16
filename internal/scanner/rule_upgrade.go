package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	betterconfig "github.com/betterleaks/betterleaks/config"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// CandidateIdentity is the complete non-content identity of a candidate
// rule set. Generation is derived from the exact rule bytes; callers cannot
// relabel the same bytes and accidentally activate a different generation.
type CandidateIdentity struct {
	Generation  string
	Fingerprint string
}

// CandidateService is scanner-owned and comparative-only. It has no storage
// or activation methods, so T13 can rescan active data without mutating the
// canonical database or the active scanner.
type CandidateService interface {
	Identity() CandidateIdentity
	Scan(ctx context.Context, fields []ports.TextField) ports.ScanResult
	ScanBatch(ctx context.Context, batch []CandidateBatchItem) ([]CandidateBatchResult, error)
	Compare(ctx context.Context, fixtures []ComparativeFixture, timeout time.Duration) error
}

type CandidateBatchItem struct {
	ID     string
	Fields []ports.TextField
}

type CandidateBatchResult struct {
	ID     string
	Result ports.ScanResult
}

type CandidateRules struct {
	identity CandidateIdentity
	engine   scanEngine
}

var safeBatchID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func (candidate CandidateRules) Generation() string  { return candidate.identity.Generation }
func (candidate CandidateRules) Fingerprint() string { return candidate.identity.Fingerprint }
func (candidate CandidateRules) Identity() CandidateIdentity {
	return candidate.identity
}

func (candidate CandidateRules) Scan(ctx context.Context, fields []ports.TextField) ports.ScanResult {
	if candidate.engine == nil {
		return ports.ScanResult{Status: ports.ScanError, Generation: candidate.Generation()}
	}
	scanner := newScannerWithEngine(Config{Generation: candidate.Generation(), Timeout: 2 * time.Second}, candidate.engine)
	return scanner.Scan(ctx, fields)
}

func (candidate CandidateRules) ScanBatch(ctx context.Context, batch []CandidateBatchItem) ([]CandidateBatchResult, error) {
	if candidate.engine == nil || len(batch) == 0 {
		return nil, ErrComparativeFailed
	}
	results := make([]CandidateBatchResult, 0, len(batch))
	for _, item := range batch {
		if !safeBatchID.MatchString(item.ID) || len(item.Fields) == 0 || !validFields(item.Fields) {
			return nil, ErrComparativeFailed
		}
		results = append(results, CandidateBatchResult{ID: item.ID, Result: candidate.Scan(ctx, item.Fields)})
	}
	return results, nil
}

func (candidate CandidateRules) Compare(ctx context.Context, fixtures []ComparativeFixture, timeout time.Duration) error {
	return compareCandidate(ctx, candidate, fixtures, timeout)
}

// GenerationForRules derives the only accepted generation label for a byte
// sequence. The full SHA-256 is retained in the fingerprint; the generation
// prefix keeps diagnostics bounded while remaining collision-resistant for a
// rule identity label.
func GenerationForRules(contents []byte) string {
	fingerprint := sha256.Sum256(contents)
	return "candidate-" + hex.EncodeToString(fingerprint[:])
}

func LoadCandidateRules(contents []byte, generation string) (CandidateRules, error) {
	if len(contents) == 0 || strings.TrimSpace(generation) == "" || len(generation) > 128 {
		return CandidateRules{}, ErrInvalidRules
	}
	if err := validateTalariaRuleSchema(contents); err != nil {
		return CandidateRules{}, err
	}
	fingerprintBytes := sha256.Sum256(contents)
	fingerprint := hex.EncodeToString(fingerprintBytes[:])
	if generation != GenerationForRules(contents) {
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
	return CandidateRules{
		identity: CandidateIdentity{Generation: generation, Fingerprint: fingerprint},
		engine:   engine,
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
	return compareCandidate(ctx, candidate, fixtures, timeout)
}

func compareCandidate(ctx context.Context, candidate CandidateRules, fixtures []ComparativeFixture, timeout time.Duration) error {
	if candidate.engine == nil || candidate.Generation() == "" || candidate.Fingerprint() == "" || timeout <= 0 {
		return ErrInvalidRules
	}
	if len(fixtures) == 0 {
		return ErrComparativeFailed
	}
	for _, fixture := range fixtures {
		if strings.TrimSpace(fixture.Name) == "" || len(fixture.Fields) == 0 {
			return ErrComparativeFailed
		}
		if !validFields(fixture.Fields) {
			return ErrComparativeFailed
		}
	}
	scanner := newScannerWithEngine(Config{Generation: candidate.Generation(), Timeout: timeout}, candidate.engine)
	for _, fixture := range fixtures {
		result := scanner.Scan(ctx, fixture.Fields)
		if result.Generation != candidate.Generation() || result.Status != fixture.Expected {
			return ErrComparativeFailed
		}
		if result.Status != ports.ScanClean && result.Status != ports.ScanFinding {
			return errors.Join(ErrComparativeFailed, errors.New("uncertain candidate result"))
		}
	}
	return nil
}

func validFields(fields []ports.TextField) bool {
	for _, field := range fields {
		if !field.Name.Valid() {
			return false
		}
	}
	return true
}
