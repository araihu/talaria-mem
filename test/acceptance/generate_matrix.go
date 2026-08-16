//go:build ignore

// This generator materializes the identity-free E matrix from the immutable
// C0 catalog. It is intentionally not part of the production binary.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	root := filepath.Clean(filepath.Join("test", "acceptance", "..", ".."))
	catalog := filepath.Join(root, "test", "acceptance", "gate_catalog.yaml")
	receipt := filepath.Join(root, "docs", "superpowers", "receipts", "talaria-mem-v0.0.1", "t15-c1-gates-green.txt")
	rows := readRows(catalog)
	commit := git("rev-parse", "HEAD")
	tree := git("rev-parse", "HEAD^{tree}")
	catalogHash := fileHash(catalog)
	receiptHash := fileHash(receipt)
	var output strings.Builder
	output.WriteString("version: 1\nmatrix: talaria-mem-v0.0.1\nrows:\n")
	for _, row := range rows {
		fmt.Fprintf(&output, "  - gate_id: %s\n", row["gate_id"])
		for _, key := range []string{"owner", "spec_section", "command", "expected_exit", "negative_fixture"} {
			if key == "expected_exit" {
				fmt.Fprintf(&output, "    %s: %s\n", key, row[key])
				continue
			}
			fmt.Fprintf(&output, "    %s: %s\n", key, quote(row[key]))
		}
		fmt.Fprintf(&output, "    receipt_path: %s\n", receipt)
		fmt.Fprintf(&output, "    gate_catalog_hash: %s\n", catalogHash)
		fmt.Fprintf(&output, "    tested_source_commit: %s\n", commit)
		fmt.Fprintf(&output, "    tested_source_tree: %s\n", tree)
		fmt.Fprintf(&output, "    receipt_hash: %s\n", receiptHash)
	}
	if err := os.WriteFile(filepath.Join(root, "test", "acceptance", "gate_matrix.yaml"), []byte(output.String()), 0o600); err != nil {
		panic(err)
	}
}

func readRows(path string) []map[string]string {
	file, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	var rows []map[string]string
	var current map[string]string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "- gate_id:") {
			if current != nil {
				rows = append(rows, current)
			}
			current = map[string]string{"gate_id": strings.TrimSpace(strings.TrimPrefix(line, "- gate_id:"))}
			continue
		}
		if current == nil {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found {
			current[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "\"")
		}
	}
	if current != nil {
		rows = append(rows, current)
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
	return rows
}

func quote(value string) string {
	return fmt.Sprintf("%q", value)
}

func fileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func git(args ...string) string {
	output, err := exec.Command("git", args...).Output()
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(output))
}
