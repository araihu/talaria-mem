package testutil

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// FixturePath returns an absolute path below the repository testdata directory.
func FixturePath(t testing.TB, elements ...string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test fixture root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "testdata"))
	parts := append([]string{root}, elements...)
	return filepath.Join(parts...)
}

// ReadJSONFixture decodes a repository fixture and rejects trailing JSON.
func ReadJSONFixture(t testing.TB, target any, elements ...string) {
	t.Helper()
	file, err := os.Open(FixturePath(t, elements...))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			t.Fatal("fixture contains trailing JSON value")
		}
		t.Fatal(err)
	}
}
