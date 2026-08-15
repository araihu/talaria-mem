package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapREDConsumesMissingEntrypointFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "bootstrap", "missing-entrypoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Case     string `json:"case"`
		Expected string `json:"expected"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Case != "missing-entrypoint" || fixture.Expected == "" {
		t.Fatalf("unexpected bootstrap fixture: %+v", fixture)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(--help) error = %v", err)
	}
	if err := Run(context.Background(), []string{"definitely-unknown"}, &stdout, &stderr); err == nil {
		t.Fatal("Run(unknown) accepted missing bootstrap behavior")
	}
}
