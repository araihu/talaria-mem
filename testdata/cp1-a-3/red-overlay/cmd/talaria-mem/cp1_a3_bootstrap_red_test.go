package main

import (
	"context"
	"testing"
)

// This immutable overlay probes the T1 bootstrap boundary, not scanner
// identity. The rejected ancestor panics when the provider-free shell receives
// an unavailable output writer; the corrected shell returns a typed error.
func TestBootstrapMissingEntrypoint(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("bootstrap panicked with unavailable output: %v", recovered)
		}
	}()
	if err := Run(context.Background(), []string{"--help"}, nil, nil); err == nil {
		t.Fatal("bootstrap accepted unavailable output")
	}
}
