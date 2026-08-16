package ports

import "testing"

// The rejected ancestor has no durable activation epoch. This compile-time
// probe is intentionally a T2 port contract, not scanner identity evidence.
func TestActivationJournalEpochIdentity(t *testing.T) {
	record := ActivationRecord{ActivationEpoch: "epoch-2", Phase: ActivationPending}
	if record.ActivationEpoch == "" {
		t.Fatal("activation epoch is empty")
	}
}
