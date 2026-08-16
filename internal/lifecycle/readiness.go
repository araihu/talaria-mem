package lifecycle

import (
	"context"
	"sort"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// ReadinessReport is safe to expose on /readyz and in diagnostics.  It
// contains bounded component labels only; driver errors and content are never
// copied into the report.
type ReadinessReport struct {
	Ready      bool     `json:"ready"`
	Blockers   []string `json:"blockers"`
	Storage    bool     `json:"storage"`
	Scanner    bool     `json:"scanner"`
	Migration  bool     `json:"migration"`
	Projection bool     `json:"projection"`
	Inventory  bool     `json:"inventory"`
}

type CheckFunc func(context.Context) error

// Readiness is the lifecycle-facing readiness graph.  Health is deliberately
// not represented here: a live process can be healthy while storage recovery
// or rule activation keeps it unready.
type Readiness struct {
	Storage    CheckFunc
	Scanner    CheckFunc
	Migration  CheckFunc
	Projection CheckFunc
	Inventory  CheckFunc
	Activation ports.ActivationJournal
}

func NewReadiness(configuration Readiness) *Readiness {
	return &configuration
}

func (readiness *Readiness) Check(ctx context.Context) (ReadinessReport, error) {
	if readiness == nil {
		return ReadinessReport{Blockers: []string{"readiness unavailable"}}, nil
	}
	report := ReadinessReport{Storage: true, Scanner: true, Migration: true, Projection: true, Inventory: true}
	checks := []struct {
		name  string
		check CheckFunc
		value *bool
	}{
		{"storage", readiness.Storage, &report.Storage},
		{"scanner", readiness.Scanner, &report.Scanner},
		{"migration", readiness.Migration, &report.Migration},
		{"projection", readiness.Projection, &report.Projection},
		{"inventory", readiness.Inventory, &report.Inventory},
	}
	for _, item := range checks {
		if item.check == nil {
			continue
		}
		if err := item.check(ctx); err != nil {
			*item.value = false
			report.Blockers = append(report.Blockers, item.name)
		}
	}
	if readiness.Activation != nil {
		blockers, err := readiness.Activation.ReadinessBlockers(ctx)
		if err != nil {
			return ReadinessReport{}, err
		}
		for _, blocker := range blockers {
			if blocker != "" {
				report.Blockers = append(report.Blockers, boundedBlocker(blocker))
			}
		}
	}
	report.Blockers = uniqueSorted(report.Blockers)
	report.Ready = len(report.Blockers) == 0 && report.Storage && report.Scanner && report.Migration && report.Projection && report.Inventory
	return report, nil
}

// Ready implements the HTTP adapter's narrow readiness dependency.
func (readiness *Readiness) Ready(ctx context.Context) (bool, string, error) {
	report, err := readiness.Check(ctx)
	if err != nil {
		return false, "readiness check failed", err
	}
	if report.Ready {
		return true, "", nil
	}
	if len(report.Blockers) == 0 {
		return false, "storage unavailable", nil
	}
	return false, report.Blockers[0], nil
}

func boundedBlocker(value string) string {
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
