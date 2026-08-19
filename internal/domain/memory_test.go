package domain

import (
	"strings"
	"testing"
	"time"
)

func TestEnums(t *testing.T) {
	t.Parallel()

	for _, kind := range []MemoryKind{MemoryKindState, MemoryKindProcedure, MemoryKindFailure, MemoryKindStandingInstruction} {
		if !kind.Valid() {
			t.Errorf("MemoryKind(%q).Valid() = false", kind)
		}
	}
	for _, trust := range []Trust{TrustVerified, TrustGenerated, TrustUnverified} {
		if !trust.Valid() {
			t.Errorf("Trust(%q).Valid() = false", trust)
		}
	}
	for _, lifecycle := range []Lifecycle{LifecycleActive, LifecycleQuarantined, LifecycleForgotten, LifecyclePurged} {
		if !lifecycle.Valid() {
			t.Errorf("Lifecycle(%q).Valid() = false", lifecycle)
		}
	}
	if MemoryKind("future").Valid() || Trust("future").Valid() || Lifecycle("future").Valid() {
		t.Fatal("unknown enum accepted")
	}
}

func TestResolutionState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		kind       MemoryKind
		resolution ResolutionState
		want       ResolutionState
		wantErr    bool
	}{
		{name: "failure defaults open", kind: MemoryKindFailure, want: ResolutionOpen},
		{name: "failure open", kind: MemoryKindFailure, resolution: ResolutionOpen, want: ResolutionOpen},
		{name: "failure resolved", kind: MemoryKindFailure, resolution: ResolutionResolved, want: ResolutionResolved},
		{name: "failure unknown", kind: MemoryKindFailure, resolution: "future", wantErr: true},
		{name: "state rejects", kind: MemoryKindState, resolution: ResolutionOpen, wantErr: true},
		{name: "procedure rejects", kind: MemoryKindProcedure, resolution: ResolutionResolved, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateResolutionState(test.kind, test.resolution)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateResolutionState() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("ValidateResolutionState() = %q, want %q", got, test.want)
			}
		})
	}

	if err := ValidateResolutionTransition(MemoryKindFailure, ResolutionOpen, ResolutionResolved); err != nil {
		t.Fatalf("open to resolved: %v", err)
	}
	if err := ValidateResolutionTransition(MemoryKindState, "", ResolutionOpen); err == nil {
		t.Fatal("non-failure resolution transition accepted")
	}
}

func TestUUIDv7AndTimestamp(t *testing.T) {
	t.Parallel()

	if err := ValidateUUIDv7("018f1f61-7b5c-7abc-8def-0123456789ab"); err != nil {
		t.Fatalf("valid UUIDv7 rejected: %v", err)
	}
	for _, invalid := range []string{
		"018f1f61-7b5c-6abc-8def-0123456789ab",
		"018f1f61-7b5c-7abc-7def-0123456789ab",
		"not-a-uuid",
	} {
		if err := ValidateUUIDv7(invalid); err == nil {
			t.Errorf("invalid UUIDv7 %q accepted", invalid)
		}
	}

	utc := time.Date(2026, time.August, 15, 1, 2, 3, 4, time.UTC)
	if err := ValidateTimestamp(utc); err != nil {
		t.Fatalf("UTC timestamp rejected: %v", err)
	}
	if err := ValidateTimestamp(utc.In(time.FixedZone("offset", 3600))); err == nil {
		t.Fatal("non-UTC timestamp accepted")
	}
}

func TestNormalizeV1(t *testing.T) {
	t.Parallel()

	a, err := NormalizeV1("failure", "Cafe\u0301\r\n", "line 1\rline 2", []string{"z", "a\u0301"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NormalizeV1("failure", "Café\n", "line 1\nline 2", []string{"á", "z"})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("equivalent normalized values differ:\n%s\n%s", a, b)
	}
	if !strings.Contains(a, `"version":"normalization-v1"`) {
		t.Fatalf("normalization version missing: %s", a)
	}
	if _, err := NormalizeV1("future", "title", "body", nil); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := NormalizeV1("state", string([]byte{0xff}), "body", nil); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestValidateMemoryRevisionProvenanceBounds(t *testing.T) {
	t.Parallel()
	base := MemoryRevision{
		ID: "018f1f61-7b5c-7abc-8def-0123456789ab", MemoryID: "018f1f61-7b5c-7abc-8def-1123456789ab",
		Number: 1, Kind: MemoryKindState, Title: "title", Content: "body",
		Trust: TrustVerified, Lifecycle: LifecycleActive,
		CreatedAt: time.Date(2026, time.August, 15, 1, 2, 3, 4, time.UTC),
	}
	tests := []struct {
		name       string
		provenance Provenance
	}{
		{name: "too many labels", provenance: Provenance{Labels: make([]string, MaxProvenanceLabels+1)}},
		{name: "label bytes", provenance: Provenance{Labels: []string{strings.Repeat("x", MaxProvenanceLabelBytes+1)}}},
		{name: "label utf8", provenance: Provenance{Labels: []string{string([]byte{0xff})}}},
		{name: "locator bytes", provenance: Provenance{SourceLocator: strings.Repeat("x", MaxSourceLocatorBytes+1)}},
		{name: "locator utf8", provenance: Provenance{SourceLocator: string([]byte{0xff})}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.Provenance = test.provenance
			if err := ValidateMemoryRevision(candidate); err == nil {
				t.Fatal("unsafe provenance accepted")
			}
		})
	}
}

func TestValidateMemoryRevisionFailureEmptyStateDefaultsOpen(t *testing.T) {
	t.Parallel()
	revision := MemoryRevision{
		ID: "018f1f61-7b5c-7abc-8def-0123456789ab", MemoryID: "018f1f61-7b5c-7abc-8def-1123456789ab",
		Number: 1, Kind: MemoryKindFailure, Title: "title", Content: "body",
		Trust: TrustVerified, Lifecycle: LifecycleActive,
		CreatedAt: time.Date(2026, time.August, 15, 1, 2, 3, 4, time.UTC),
	}
	state, err := ValidateResolutionState(revision.Kind, revision.ResolutionState)
	if err != nil || state != ResolutionOpen {
		t.Fatalf("failure empty state = %q, %v; want open", state, err)
	}
}

func TestGeneratedTrustCannotUseStandingInstruction(t *testing.T) {
	if TrustGenerated.Valid() == false {
		t.Fatal("generated trust is invalid")
	}
	revision := MemoryRevision{
		ID: "018f1f61-7b5c-7abc-8def-0123456789ab", MemoryID: "018f1f61-7b5c-7abc-8def-1123456789ab",
		Number: 1, Kind: MemoryKindStandingInstruction, Title: "title", Content: "body",
		Trust: TrustGenerated, Lifecycle: LifecycleActive,
		CreatedAt: time.Date(2026, time.August, 15, 1, 2, 3, 4, time.UTC),
	}
	if err := ValidateMemoryRevision(revision); err == nil {
		t.Fatal("generated standing instruction accepted")
	}
}
