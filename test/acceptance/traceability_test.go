package acceptance

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type yamlRow map[string]string

func repositoryRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readRows(path, listKey string) ([]yamlRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows := make([]yamlRow, 0)
	var current yamlRow
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || line == listKey+":" {
			continue
		}
		if strings.HasPrefix(line, "-") {
			if current != nil {
				rows = append(rows, current)
			}
			current = make(yamlRow)
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		}
		if current == nil {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"")
		current[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		rows = append(rows, current)
	}
	return rows, nil
}

func catalogRows(t *testing.T) []yamlRow {
	t.Helper()
	rows, err := readRows(filepath.Join(repositoryRoot(), "test", "acceptance", "gate_catalog.yaml"), "gates")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func gateSet(rows []yamlRow) map[string]yamlRow {
	result := make(map[string]yamlRow, len(rows))
	for _, row := range rows {
		result[row["gate_id"]] = row
	}
	return result
}

func TestGateCatalogIntegrity(t *testing.T) {
	rows := catalogRows(t)
	if len(rows) != 31 {
		t.Fatalf("gate count=%d, want 31", len(rows))
	}
	root := repositoryRoot()
	for index, row := range rows {
		wantID := fmt.Sprintf("G%02d", index)
		if row["gate_id"] != wantID {
			t.Fatalf("gate %d=%q, want %s", index, row["gate_id"], wantID)
		}
		for _, field := range []string{"owner", "spec_section", "command", "expected_exit", "negative_fixture", "receipt_path"} {
			if strings.TrimSpace(row[field]) == "" {
				t.Fatalf("%s missing %s", wantID, field)
			}
		}
		exitCode, err := strconv.Atoi(row["expected_exit"])
		if err != nil || (exitCode != 0 && exitCode != 1) {
			t.Fatalf("%s invalid expected_exit=%q", wantID, row["expected_exit"])
		}
		for _, forbidden := range []string{"candidate_commit", "candidate_tree"} {
			if strings.Contains(row["command"], forbidden) || strings.Contains(row["receipt_path"], forbidden) {
				t.Fatalf("%s contains forbidden identity field %q", wantID, forbidden)
			}
		}
		for _, relative := range []string{row["negative_fixture"], row["receipt_path"]} {
			if filepath.IsAbs(relative) {
				t.Fatalf("%s path is absolute: %s", wantID, relative)
			}
			if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
				t.Fatalf("%s path missing %s: %v", wantID, relative, err)
			}
		}
	}
}

func TestCatalogFunctionalGates(t *testing.T) {
	for _, row := range catalogRows(t) {
		command := strings.TrimSpace(row["command"])
		if !(strings.HasPrefix(command, "go ") || strings.HasPrefix(command, "vacuum ")) {
			t.Fatalf("%s command is not an approved local gate: %q", row["gate_id"], command)
		}
	}
}

func TestDAGHeaderTraceability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs/superpowers/plans/2026-08-15-talaria-mem-v0.0.1-implementation.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for task := 1; task <= 15; task++ {
		if !strings.Contains(text, fmt.Sprintf("Task %d:", task)) {
			t.Fatalf("missing Task %d header", task)
		}
	}
}

func TestGateHeaderTraceability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs/superpowers/plans/2026-08-15-talaria-mem-v0.0.1-implementation.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for gate := 0; gate <= 30; gate++ {
		if !strings.Contains(text, fmt.Sprintf("G%02d", gate)) {
			t.Fatalf("missing G%02d traceability header", gate)
		}
	}
}

func TestSpecTraceability(t *testing.T) {
	rows, err := readRows(filepath.Join(repositoryRoot(), "test", "acceptance", "spec_traceability.yaml"), "sections")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 16 {
		t.Fatalf("section rows=%d, want 16", len(rows))
	}
	for index, row := range rows {
		if row["section"] != strconv.Itoa(index+1) || row["classification"] == "" || row["evidence"] == "" {
			t.Fatalf("invalid section trace row %v", row)
		}
	}
}

func TestRequirementTraceability(t *testing.T) {
	rows, err := readRows(filepath.Join(repositoryRoot(), "test", "acceptance", "requirement_traceability.yaml"), "requirements")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("requirement rows=%d, want 5", len(rows))
	}
	catalog := gateSet(catalogRows(t))
	plan, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs/superpowers/plans/2026-08-15-talaria-mem-v0.0.1-implementation.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		for _, field := range []string{"id", "spec_section", "owner_task", "named_test", "negative_fixture", "gate_id"} {
			if row[field] == "" {
				t.Fatalf("requirement missing %s: %v", field, row)
			}
		}
		if _, ok := catalog[row["gate_id"]]; !ok {
			t.Fatalf("requirement %s references unknown gate %s", row["id"], row["gate_id"])
		}
		if !strings.Contains(string(plan), row["id"]) || !strings.Contains(string(plan), row["named_test"]) {
			t.Fatalf("requirement %s not bidirectionally named in plan", row["id"])
		}
	}
}

func TestGateMatrix(t *testing.T) {
	path := filepath.Join(repositoryRoot(), "test", "acceptance", "gate_matrix.yaml")
	if _, err := os.Stat(path); errorsIsNotExist(err) {
		t.Skip("C0 has no E/C1 matrix")
	}
	rows, err := readRows(path, "matrix")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 31 {
		t.Fatalf("matrix rows=%d, want 31", len(rows))
	}
	catalog := gateSet(catalogRows(t))
	catalogHash := fileSHA256(filepath.Join(repositoryRoot(), "test", "acceptance", "gate_catalog.yaml"))
	currentCommit := gitRevision("HEAD")
	for _, row := range rows {
		for _, field := range []string{"gate_id", "owner", "spec_section", "command", "expected_exit", "negative_fixture", "receipt_path", "gate_catalog_hash", "tested_source_commit", "tested_source_tree", "receipt_hash"} {
			if row[field] == "" {
				t.Fatalf("matrix row missing %s: %v", field, row)
			}
		}
		if row["candidate_commit"] != "" || row["candidate_tree"] != "" {
			t.Fatalf("matrix row contains final candidate identity: %v", row)
		}
		catalogRow, ok := catalog[row["gate_id"]]
		if !ok || row["owner"] != catalogRow["owner"] || row["spec_section"] != catalogRow["spec_section"] || row["command"] != catalogRow["command"] || row["expected_exit"] != catalogRow["expected_exit"] || row["negative_fixture"] != catalogRow["negative_fixture"] {
			t.Fatalf("matrix row diverges from immutable catalog: %v", row)
		}
		if row["gate_catalog_hash"] != catalogHash || row["tested_source_commit"] == currentCommit {
			t.Fatalf("matrix source/catalog identity invalid: %v", row)
		}
		receiptPath := filepath.Join(repositoryRoot(), row["receipt_path"])
		if row["receipt_hash"] != fileSHA256(receiptPath) {
			t.Fatalf("matrix receipt hash mismatch for %s", row["gate_id"])
		}
	}
}

func TestGateMatrixCompleteness(t *testing.T) {
	path := filepath.Join(repositoryRoot(), "test", "acceptance", "gate_matrix.yaml")
	if _, err := os.Stat(path); errorsIsNotExist(err) {
		t.Skip("C0 has no E/C1 matrix")
	}
	rows, err := readRows(path, "matrix")
	if err != nil {
		t.Fatal(err)
	}
	catalog := gateSet(catalogRows(t))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, ok := catalog[row["gate_id"]]; !ok || seen[row["gate_id"]] {
			t.Fatalf("matrix gate is missing or duplicated: %v", row)
		}
		seen[row["gate_id"]] = true
	}
	if len(seen) != len(catalog) {
		t.Fatalf("matrix gates=%d catalog gates=%d", len(seen), len(catalog))
	}
}

func TestAllGates(t *testing.T) {
	TestGateCatalogIntegrity(t)
	TestCatalogFunctionalGates(t)
	TestSpecTraceability(t)
	TestRequirementTraceability(t)
}

func errorsIsNotExist(err error) bool { return err != nil && os.IsNotExist(err) }

func fileSHA256(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func gitRevision(reference string) string {
	command := exec.Command("git", "rev-parse", reference)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
