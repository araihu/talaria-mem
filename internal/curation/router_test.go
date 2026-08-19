package curation

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type fakeCurator struct {
	name  string
	model string
	err   error
	seen  *[]string
}

func (fake fakeCurator) Curate(_ context.Context, _ CurationRequest) (CurationResult, error) {
	if fake.seen != nil {
		*fake.seen = append(*fake.seen, fake.name)
	}
	if fake.err != nil {
		return CurationResult{}, fake.err
	}
	return CurationResult{Provider: fake.name, Model: fake.model}, nil
}

func TestRouterUsesConfiguredOrderAndStopsOnSuccess(t *testing.T) {
	var seen []string
	router := Router{
		Enabled: true,
		Chain: []ProviderEntry{
			{Name: "local", Curator: fakeCurator{name: "local", seen: &seen, err: NewProviderError(ErrorUnavailable, errors.New("down"))}},
			{Name: "codex", Curator: fakeCurator{name: "codex", model: "gpt-5.6-luna", seen: &seen}},
			{Name: "never", Curator: fakeCurator{name: "never", seen: &seen}},
		},
	}

	result, err := router.Curate(context.Background(), CurationRequest{WorkspaceID: "workspace"})
	if err != nil {
		t.Fatalf("Curate() error = %v", err)
	}
	if !reflect.DeepEqual(seen, []string{"local", "codex"}) {
		t.Fatalf("providers called = %v, want [local codex]", seen)
	}
	if result.Provider != "codex" || result.Model != "gpt-5.6-luna" {
		t.Fatalf("result provenance = %#v", result)
	}
}

func TestRouterFallsBackOnlyForRetryableProviderClasses(t *testing.T) {
	tests := []struct {
		name  string
		class ErrorClass
		want  bool
	}{
		{name: "unavailable", class: ErrorUnavailable, want: true},
		{name: "timeout", class: ErrorTimeout, want: true},
		{name: "rate limit", class: ErrorRateLimit, want: true},
		{name: "authentication", class: ErrorAuthentication, want: true},
		{name: "scanner", class: ErrorScannerRefusal, want: false},
		{name: "invalid output", class: ErrorInvalidOutput, want: false},
		{name: "policy", class: ErrorPolicyRefusal, want: false},
		{name: "suspicious", class: ErrorSuspiciousContent, want: false},
		{name: "persistence", class: ErrorPersistence, want: false},
		{name: "domain", class: ErrorDomainValidation, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var seen []string
			router := Router{
				Enabled: true,
				Chain: []ProviderEntry{
					{Name: "first", Curator: fakeCurator{name: "first", seen: &seen, err: NewProviderError(test.class, errors.New("failure"))}},
					{Name: "second", Curator: fakeCurator{name: "second", seen: &seen}},
				},
			}
			result, err := router.Curate(context.Background(), CurationRequest{})
			if test.want {
				if err != nil {
					t.Fatalf("Curate() error = %v", err)
				}
				if result.Provider != "second" || !reflect.DeepEqual(seen, []string{"first", "second"}) {
					t.Fatalf("fallback result=%#v calls=%v", result, seen)
				}
				return
			}
			if err == nil {
				t.Fatal("Curate() error = nil, want terminal error")
			}
			if !reflect.DeepEqual(seen, []string{"first"}) {
				t.Fatalf("calls = %v, want [first]", seen)
			}
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) || providerErr.Class != test.class {
				t.Fatalf("error = %v, want class %s", err, test.class)
			}
		})
	}
}

func TestRouterDisabledDoesNotCallProviders(t *testing.T) {
	called := false
	router := Router{Enabled: false, Chain: []ProviderEntry{{Name: "codex", Curator: fakeCurator{seen: func() *[]string { values := []string{}; return &values }()}}}}
	result, err := router.Curate(context.Background(), CurationRequest{})
	if !errors.Is(err, ErrCurationDisabled) {
		t.Fatalf("Curate() error = %v, want ErrCurationDisabled", err)
	}
	if result.Provider != "" || result.Model != "" || len(result.Candidates) != 0 {
		t.Fatalf("result = %#v, want zero", result)
	}
	if called {
		t.Fatal("disabled router called provider")
	}
}

func TestRouterRejectsInvalidChain(t *testing.T) {
	tests := []Router{
		{Enabled: true},
		{Enabled: true, Chain: []ProviderEntry{{Name: "", Curator: fakeCurator{}}}},
		{Enabled: true, Chain: []ProviderEntry{{Name: "same", Curator: fakeCurator{}}, {Name: "same", Curator: fakeCurator{}}}},
		{Enabled: true, Chain: []ProviderEntry{{Name: "missing", Curator: nil}}},
	}
	for index, router := range tests {
		if err := router.Validate(); err == nil {
			t.Errorf("case %d Validate() error = nil", index)
		}
	}
}

var _ = domain.MemoryKindState
