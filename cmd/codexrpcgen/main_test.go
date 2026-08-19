package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadManifestRejectsDuplicateAndMalformedMethods(t *testing.T) {
	for name, contents := range map[string]string{
		"wrong arity": `{"initialize":["v1/InitializeParams"]}`,
		"empty method": `{"": ["v1/InitializeParams", "v1/InitializeResponse"]}`,
		"bad method": `{"not a method":["v1/InitializeParams", "v1/InitializeResponse"]}`,
		"bad type": `{"initialize":["v1/InitializeParams", ""]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "methods.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadManifest(path); err == nil {
				t.Fatal("LoadManifest() error = nil")
			}
		})
	}
}

func TestLoadManifestReturnsDeterministicMethods(t *testing.T) {
	path := filepath.Join(t.TempDir(), "methods.json")
	contents := `{"turn/start":["v2/TurnStartParams","v2/TurnStartResponse"],"initialize":["v1/InitializeParams","v1/InitializeResponse"]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	want := []Method{
		{Name: "initialize", Params: "v1/InitializeParams", Result: "v1/InitializeResponse"},
		{Name: "turn/start", Params: "v2/TurnStartParams", Result: "v2/TurnStartResponse"},
	}
	if !reflect.DeepEqual(manifest.Methods, want) {
		t.Fatalf("methods = %#v, want %#v", manifest.Methods, want)
	}
}

func TestBuildCuratedSchemaRequiresEveryManifestRoot(t *testing.T) {
	schemaDir := t.TempDir()
	for _, name := range []string{"v1/InitializeParams", "v1/InitializeResponse"} {
		path := filepath.Join(schemaDir, name+".json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"title":"x","type":"object"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := filepath.Join(t.TempDir(), "methods.json")
	if err := os.WriteFile(manifestPath, []byte(`{"initialize":["v1/InitializeParams","v1/InitializeResponse"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildCuratedSchema(schemaDir, manifest); err == nil {
		t.Fatal("BuildCuratedSchema() error = nil")
	}
}

func TestManifestJSONIsObject(t *testing.T) {
	var value any
	if err := json.Unmarshal([]byte(`[]`), &value); err != nil {
		t.Fatal(err)
	}
	if _, ok := value.([]any); !ok {
		t.Fatalf("decoded = %T", value)
	}
}
