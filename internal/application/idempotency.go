package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// IdempotencyRecord is deliberately limited to non-content metadata. It is
// safe to persist this value in the database and to return it on replay.
type IdempotencyRecord struct {
	Caller        string
	Operation     string
	KeyDigest     string
	RequestDigest string
	TargetIDs     []string
	SafeResult    map[string]string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type idempotencyEntry struct {
	record IdempotencyRecord
}

// Idempotency is a small process-local implementation used by the service
// when the durable repository does not expose idempotency operations. The
// canonical adapter can implement IdempotencyStore and use the same digest
// contract. Raw keys and request bodies never enter this structure.
type Idempotency struct {
	mu      sync.Mutex
	entries map[string]idempotencyEntry
	clock   ports.Clock
	deriver ports.KeyDeriver
}

func NewIdempotency(clock ports.Clock, deriver ports.KeyDeriver) *Idempotency {
	return &Idempotency{entries: make(map[string]idempotencyEntry), clock: clock, deriver: deriver}
}

type IdempotencyStore interface {
	LookupIdempotency(ctx context.Context, caller, operation, keyDigest, requestDigest string, now time.Time) (record IdempotencyRecord, found bool, conflict bool, err error)
	SaveIdempotency(ctx context.Context, record IdempotencyRecord) error
	CleanupIdempotency(ctx context.Context, before time.Time) error
}

func (store *Idempotency) Lookup(ctx context.Context, caller, operation, key string, request any, now time.Time) (IdempotencyRecord, bool, bool, error) {
	keyDigest, requestDigest, err := store.Digests(ctx, caller, operation, key, request)
	if err != nil {
		return IdempotencyRecord{}, false, false, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.removeExpiredLocked(now)
	entry, ok := store.entries[entryKey(caller, operation, keyDigest)]
	if !ok {
		return IdempotencyRecord{}, false, false, nil
	}
	if entry.record.RequestDigest != requestDigest {
		return IdempotencyRecord{}, true, true, domain.NewError(domain.CodeIdempotencyConflict, "idempotency request conflict", false)
	}
	return cloneRecord(entry.record), true, false, nil
}

func (store *Idempotency) Save(ctx context.Context, record IdempotencyRecord) error {
	if err := validateIdempotencyRecord(record); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.entries[entryKey(record.Caller, record.Operation, record.KeyDigest)] = idempotencyEntry{record: cloneRecord(record)}
	return nil
}

func (store *Idempotency) Cleanup(ctx context.Context, before time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for key, entry := range store.entries {
		if !entry.record.ExpiresAt.After(before) {
			delete(store.entries, key)
		}
	}
	return nil
}

func (store *Idempotency) removeExpiredLocked(now time.Time) {
	for key, entry := range store.entries {
		if !entry.record.ExpiresAt.After(now) {
			delete(store.entries, key)
		}
	}
}

func (store *Idempotency) Digests(ctx context.Context, caller, operation, key string, request any) (string, string, error) {
	if strings.TrimSpace(caller) == "" || strings.TrimSpace(operation) == "" || key == "" {
		return "", "", domain.NewError(domain.CodeValidation, "idempotency identity is required", false)
	}
	keyMaterial := []byte("talaria-mem/idempotency/v1")
	if store.deriver != nil {
		derived, err := store.deriver.DeriveKey(ctx, ports.KeyPurposeIdempotency, ports.KeyDerivationVersion)
		if err != nil {
			return "", "", domain.NewError(domain.CodeUnavailable, "idempotency key unavailable", true)
		}
		keyMaterial = derived
	}
	keyDigest := keyedDigest(keyMaterial, "key\x00"+caller+"\x00"+operation+"\x00"+key)
	encoded, err := canonicalJSON(request)
	if err != nil {
		return "", "", domain.NewError(domain.CodeValidation, "invalid idempotency request", false)
	}
	requestDigest := keyedDigest(keyMaterial, "request\x00"+caller+"\x00"+operation+"\x00"+encoded)
	return keyDigest, requestDigest, nil
}

func keyedDigest(key []byte, value string) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

func canonicalJSON(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	// A second decode through interface{} makes map key ordering deterministic
	// once encoded by encoding/json. Structs preserve field order from their
	// declaration, which is part of the caller's request contract.
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var normalized any
	if err := json.Unmarshal(b, &normalized); err != nil {
		return "", err
	}
	return stableJSON(normalized)
}

func stableJSON(value any) (string, error) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var builder strings.Builder
		builder.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				builder.WriteByte(',')
			}
			keyJSON, _ := json.Marshal(key)
			valueJSON, err := stableJSON(typed[key])
			if err != nil {
				return "", err
			}
			builder.Write(keyJSON)
			builder.WriteByte(':')
			builder.WriteString(valueJSON)
		}
		builder.WriteByte('}')
		return builder.String(), nil
	case []any:
		var builder strings.Builder
		builder.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				builder.WriteByte(',')
			}
			encoded, err := stableJSON(item)
			if err != nil {
				return "", err
			}
			builder.WriteString(encoded)
		}
		builder.WriteByte(']')
		return builder.String(), nil
	default:
		encoded, err := json.Marshal(value)
		return string(encoded), err
	}
}

func validateIdempotencyRecord(record IdempotencyRecord) error {
	if record.Caller == "" || record.Operation == "" || record.KeyDigest == "" || record.RequestDigest == "" {
		return domain.NewError(domain.CodeValidation, "invalid idempotency record", false)
	}
	if record.ExpiresAt.IsZero() || !record.ExpiresAt.After(record.CreatedAt) {
		return domain.NewError(domain.CodeValidation, "invalid idempotency expiry", false)
	}
	if record.ExpiresAt.Sub(record.CreatedAt) > domain.IdempotencyTTL {
		return domain.NewError(domain.CodeValidation, "idempotency retention exceeds 24 hours", false)
	}
	return nil
}

func cloneRecord(record IdempotencyRecord) IdempotencyRecord {
	record.TargetIDs = append([]string(nil), record.TargetIDs...)
	if record.SafeResult != nil {
		record.SafeResult = mapsClone(record.SafeResult)
	}
	return record
}

func mapsClone(value map[string]string) map[string]string {
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func entryKey(caller, operation, keyDigest string) string {
	return caller + "\x00" + operation + "\x00" + keyDigest
}
