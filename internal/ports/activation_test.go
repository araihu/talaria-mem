package ports

import (
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

func TestActivationJournal(t *testing.T) {
	var fixture struct {
		Case     string   `json:"case"`
		Required []string `json:"required"`
	}
	testutil.ReadJSONFixture(t, &fixture, "domain", "missing-port-contract.json")
	if fixture.Case != "missing-port-contract" || len(fixture.Required) != 3 {
		t.Fatalf("unexpected domain fixture: %+v", fixture)
	}
	var keyDeriver KeyDeriver
	var fileStore ManagedFileStore
	var journal ActivationJournal
	if keyDeriver != nil || fileStore != nil || journal != nil {
		t.Fatal("T2 contract stubs unexpectedly instantiated")
	}
	if err := ValidateActivationTransition(ActivationRecord{Phase: ActivationPending}, ActivationActive); err == nil {
		t.Fatal("pending to active transition accepted; missing activation contract behavior")
	}
}
