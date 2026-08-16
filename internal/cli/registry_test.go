package cli

import (
	"context"
	"testing"
)

func TestCLIContractRegistryExplicitAndDeterministic(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Command{Name: "status", Run: func(context.Context, []string, Output) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Command{Name: "status", Run: func(context.Context, []string, Output) error { return nil }}); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	if names := registry.Names(); len(names) != 1 || names[0] != "status" {
		t.Fatalf("names=%v", names)
	}
}
