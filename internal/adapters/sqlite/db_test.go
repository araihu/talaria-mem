package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

func TestSchemaActivationJournalConstraint(t *testing.T) {
	var fixture struct {
		Phase               string `json:"phase"`
		LiveMutationStarted bool   `json:"live_mutation_started"`
		Expected            string `json:"expected"`
	}
	testutil.ReadJSONFixture(t, &fixture, "sqlite", "invalid-rule-generation-transition.json")
	if fixture.Phase != "rollback" || !fixture.LiveMutationStarted || fixture.Expected == "" {
		t.Fatalf("unexpected schema fixture: %+v", fixture)
	}
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.SQL().ExecContext(context.Background(),
		"INSERT INTO rule_activation_journal(phase, live_mutation_started) VALUES (?, ?)",
		fixture.Phase, fixture.LiveMutationStarted,
	); err == nil {
		t.Fatal("rollback after live mutation accepted; schema constraint missing")
	}
}
