package maintenance

import (
	"context"
	"sort"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type ReadinessReport struct {
	Ready      bool     `json:"ready"`
	Blockers   []string `json:"blockers"`
	Storage    bool     `json:"storage"`
	Scanner    bool     `json:"scanner"`
	Migration  bool     `json:"migration"`
	Projection bool     `json:"projection"`
	Inventory  bool     `json:"inventory"`
}

type Readiness struct {
	Storage    func(context.Context) error
	Scanner    func(context.Context) error
	Migration  func(context.Context) error
	Projection func(context.Context) error
	Inventory  func(context.Context) error
	Activation ports.ActivationJournal
}

func NewReadiness() *Readiness { return &Readiness{} }

func (readiness *Readiness) Check(ctx context.Context) (ReadinessReport, error) {
	if readiness == nil {
		return ReadinessReport{Ready: false, Blockers: []string{"readiness unavailable"}}, nil
	}
	report := ReadinessReport{Storage: true, Scanner: true, Migration: true, Projection: true, Inventory: true}
	checks := []struct {
		name   string
		check  func(context.Context) error
		result *bool
	}{
		{"storage", readiness.Storage, &report.Storage}, {"scanner", readiness.Scanner, &report.Scanner},
		{"migration", readiness.Migration, &report.Migration}, {"projection", readiness.Projection, &report.Projection},
		{"inventory", readiness.Inventory, &report.Inventory},
	}
	for _, check := range checks {
		if check.check == nil {
			continue
		}
		if err := check.check(ctx); err != nil {
			*check.result = false
			report.Blockers = append(report.Blockers, check.name)
		}
	}
	if readiness.Activation != nil {
		blockers, err := readiness.Activation.ReadinessBlockers(ctx)
		if err != nil {
			return ReadinessReport{}, err
		}
		report.Blockers = append(report.Blockers, blockers...)
	}
	sort.Strings(report.Blockers)
	report.Ready = len(report.Blockers) == 0 && report.Storage && report.Scanner && report.Migration && report.Projection && report.Inventory
	return report, nil
}

// Ready is the HTTP/daemon-facing narrow shape.  The reason is a bounded
// blocker label, never a driver error or content fragment.
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

type IdempotencyCleaner interface {
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
}

func CleanupIdempotency(ctx context.Context, cleaner IdempotencyCleaner, now time.Time) (int64, error) {
	if cleaner == nil {
		return 0, ErrReceiptInvalid
	}
	return cleaner.DeleteBefore(ctx, now.UTC().Add(-24*time.Hour))
}
