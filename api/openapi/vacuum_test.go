package openapi

import (
	"os"
	"strings"
	"testing"
)

func TestContractVacuumConfigurationIsOffline(t *testing.T) {
	data, err := os.ReadFile("vacuum.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "extends: []") || !strings.Contains(text, "rules: {}") {
		t.Fatal("unexpected Vacuum configuration")
	}
	if strings.Contains(text, "http://") || strings.Contains(text, "https://") {
		t.Fatal("Vacuum configuration contains an outbound URL")
	}
}
