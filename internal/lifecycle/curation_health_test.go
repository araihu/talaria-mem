package lifecycle

import (
	"context"
	"strings"
	"testing"
)

type fakeCurationHealthSource struct {
	health CurationHealth
}

func (source fakeCurationHealthSource) CurationStatus(context.Context) (CurationHealth, error) {
	return source.health, nil
}

func TestStatusIncludesSafeCurationHealth(t *testing.T) {
	service := NewStatusService(nil, "")
	service.Curation = fakeCurationHealthSource{health: CurationHealth{
		Enabled:             true,
		Degraded:            true,
		QueueDepth:          2,
		Running:             1,
		OldestJobAgeSeconds: 17,
		LastErrorClass:      "timeout",
		Providers: []CurationProviderStatus{{
			Name: "codex", Type: "codex", Configured: true, Available: false,
		}},
	}}
	report, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Curation.Enabled || !report.Curation.Degraded || report.Curation.QueueDepth != 2 || report.Curation.Running != 1 {
		t.Fatalf("unexpected curation status: %+v", report.Curation)
	}
	if len(report.Curation.Providers) != 1 || report.Curation.Providers[0].Name != "codex" || report.Curation.Providers[0].Available {
		t.Fatalf("unexpected provider status: %+v", report.Curation.Providers)
	}
}

func TestDoctorIncludesSafeCurationHealthWithoutSecrets(t *testing.T) {
	doctor := NewDoctor(DoctorConfig{Curation: fakeCurationHealthSource{health: CurationHealth{
		Enabled:        true,
		Degraded:       true,
		LastErrorClass: "authentication",
		Providers:      []CurationProviderStatus{{Name: "local", Type: "openai_compatible", Configured: true, Available: true}},
	}}})
	report, err := doctor.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Curation.Enabled || !report.Curation.Degraded || report.Curation.LastErrorClass != "authentication" {
		t.Fatalf("unexpected curation doctor report: %+v", report.Curation)
	}
	serialized := report.Curation.String()
	for _, secret := range []string{"prompt-canary", "locator-canary", "response-canary", "bearer-canary"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("curation diagnostic leaked %q: %s", secret, serialized)
		}
	}
}
