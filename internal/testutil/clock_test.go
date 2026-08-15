package testutil

import (
	"testing"
	"time"
)

func TestFixedClockNowAndAdvance(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 15, 12, 30, 0, 123, time.UTC)
	clock := NewFixedClock(start)
	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %s, want %s", got, start)
	}

	clock.Advance(90 * time.Second)
	want := start.Add(90 * time.Second)
	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("Now() after Advance = %s, want %s", got, want)
	}
}
