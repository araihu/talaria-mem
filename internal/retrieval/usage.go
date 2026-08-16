package retrieval

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type UsageStats struct {
	Hits                int64
	Opportunities       int64
	DistinctSessionHits int64
	DetailedDays        int
}

type UsageStore interface {
	RecordOpportunity(ctx context.Context, memoryID string, at time.Time) error
	RecordDelivery(ctx context.Context, memoryID, consumerSession string, at time.Time) (countedHit bool, err error)
	Stats(ctx context.Context, memoryID string, now time.Time) (UsageStats, error)
	Cleanup(ctx context.Context, now time.Time) error
}

// HitStore separates opportunity accounting from delivered-hit accounting.
// Search uses this optional interface after recording the complete top-20
// pre-boost opportunity set.
type HitStore interface {
	RecordHit(ctx context.Context, memoryID, consumerSession string, at time.Time) (countedHit bool, err error)
}

type dailyUsage struct{ Hits, Opportunities int64 }
type lifetimeUsage struct{ Hits, Opportunities int64 }
type sessionHit struct{ At time.Time }

// UsageLedger is a deterministic reference implementation. The SQLite
// adapter can map these operations to usage_daily/usage_lifetime/
// usage_session_hits without changing scoring semantics.
type UsageLedger struct {
	mu       sync.Mutex
	clock    ports.Clock
	deriver  ports.KeyDeriver
	daily    map[string]map[string]dailyUsage
	lifetime map[string]lifetimeUsage
	sessions map[string]map[string]sessionHit
}

func NewUsageLedger(clock ports.Clock, deriver ports.KeyDeriver) *UsageLedger {
	return &UsageLedger{clock: clock, deriver: deriver, daily: map[string]map[string]dailyUsage{}, lifetime: map[string]lifetimeUsage{}, sessions: map[string]map[string]sessionHit{}}
}

func (ledger *UsageLedger) RecordOpportunity(ctx context.Context, memoryID string, at time.Time) error {
	if memoryID == "" {
		return domain.NewError(domain.CodeValidation, "usage memory identity is required", false)
	}
	day := at.UTC().Format("2006-01-02")
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.daily[memoryID] == nil {
		ledger.daily[memoryID] = map[string]dailyUsage{}
	}
	usage := ledger.daily[memoryID][day]
	usage.Opportunities++
	ledger.daily[memoryID][day] = usage
	lifetime := ledger.lifetime[memoryID]
	lifetime.Opportunities++
	ledger.lifetime[memoryID] = lifetime
	return ensureUsageInvariant(usage)
}

func (ledger *UsageLedger) RecordDelivery(ctx context.Context, memoryID, consumerSession string, at time.Time) (bool, error) {
	if memoryID == "" || consumerSession == "" {
		return false, domain.NewError(domain.CodeValidation, "usage consumer identity is required", false)
	}
	day := at.UTC().Format("2006-01-02")
	digest, err := ledger.consumerDigest(ctx, consumerSession)
	if err != nil {
		return false, err
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.daily[memoryID] == nil {
		ledger.daily[memoryID] = map[string]dailyUsage{}
	}
	usage := ledger.daily[memoryID][day]
	usage.Opportunities++
	counted := false
	if ledger.sessions[memoryID] == nil {
		ledger.sessions[memoryID] = map[string]sessionHit{}
	}
	if _, found := ledger.sessions[memoryID][digest]; !found {
		ledger.sessions[memoryID][digest] = sessionHit{At: at.UTC()}
		usage.Hits++
		lifetime := ledger.lifetime[memoryID]
		lifetime.Hits++
		ledger.lifetime[memoryID] = lifetime
		counted = true
	}
	ledger.daily[memoryID][day] = usage
	if err := ensureUsageInvariant(usage); err != nil {
		return false, err
	}
	return counted, nil
}

func (ledger *UsageLedger) RecordHit(ctx context.Context, memoryID, consumerSession string, at time.Time) (bool, error) {
	if memoryID == "" || consumerSession == "" {
		return false, domain.NewError(domain.CodeValidation, "usage consumer identity is required", false)
	}
	digest, err := ledger.consumerDigest(ctx, consumerSession)
	if err != nil {
		return false, err
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.daily[memoryID] == nil {
		ledger.daily[memoryID] = map[string]dailyUsage{}
	}
	day := at.UTC().Format("2006-01-02")
	usage := ledger.daily[memoryID][day]
	if ledger.sessions[memoryID] == nil {
		ledger.sessions[memoryID] = map[string]sessionHit{}
	}
	if _, found := ledger.sessions[memoryID][digest]; found {
		return false, nil
	}
	ledger.sessions[memoryID][digest] = sessionHit{At: at.UTC()}
	usage.Hits++
	lifetime := ledger.lifetime[memoryID]
	lifetime.Hits++
	ledger.lifetime[memoryID] = lifetime
	ledger.daily[memoryID][day] = usage
	if err := ensureUsageInvariant(usage); err != nil {
		return false, err
	}
	return true, nil
}

func (ledger *UsageLedger) Stats(ctx context.Context, memoryID string, now time.Time) (UsageStats, error) {
	if memoryID == "" {
		return UsageStats{}, domain.NewError(domain.CodeValidation, "usage memory identity is required", false)
	}
	cutoff := now.UTC().Add(-90 * 24 * time.Hour)
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	stats := UsageStats{}
	for day, usage := range ledger.daily[memoryID] {
		parsed, err := time.Parse("2006-01-02", day)
		if err != nil {
			continue
		}
		if parsed.UTC().Add(24 * time.Hour).After(cutoff) {
			stats.Hits += usage.Hits
			stats.Opportunities += usage.Opportunities
			stats.DetailedDays++
		}
	}
	for _, hit := range ledger.sessions[memoryID] {
		if hit.At.After(cutoff) {
			stats.DistinctSessionHits++
		}
	}
	return stats, nil
}

func (ledger *UsageLedger) Cleanup(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().Add(-90 * 24 * time.Hour)
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	for memoryID, days := range ledger.daily {
		for day := range days {
			parsed, err := time.Parse("2006-01-02", day)
			if err != nil || !parsed.UTC().Add(24*time.Hour).After(cutoff) {
				delete(days, day)
			}
		}
		for digest, hit := range ledger.sessions[memoryID] {
			if !hit.At.After(cutoff) {
				delete(ledger.sessions[memoryID], digest)
			}
		}
		if len(days) == 0 {
			delete(ledger.daily, memoryID)
		}
	}
	return nil
}
func (ledger *UsageLedger) CleanupAtStartup(ctx context.Context, now time.Time) error {
	return ledger.Cleanup(ctx, now)
}
func (ledger *UsageLedger) CleanupDaily(ctx context.Context, now time.Time) error {
	return ledger.Cleanup(ctx, now)
}

func (ledger *UsageLedger) consumerDigest(ctx context.Context, session string) (string, error) {
	key := []byte("talaria-mem/session/v1")
	if ledger.deriver != nil {
		derived, err := ledger.deriver.DeriveKey(ctx, ports.KeyPurposeSession, ports.KeyDerivationVersion)
		if err != nil {
			return "", domain.NewError(domain.CodeUnavailable, "usage session key unavailable", true)
		}
		key = derived
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("session\x00" + session))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func ensureUsageInvariant(value dailyUsage) error {
	if value.Hits < 0 || value.Opportunities < 0 || value.Hits > value.Opportunities {
		return domain.NewError(domain.CodeUnavailable, "usage invariant violated", false)
	}
	return nil
}
