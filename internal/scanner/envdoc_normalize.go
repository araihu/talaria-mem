//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: envdoc_normalize.go <path>")
	}

	path := os.Args[1]
	info, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	content = append(bytes.TrimRight(content, "\n"), '\n')
	content = bytes.ReplaceAll(content, []byte("\n - "), []byte("\n- "))
	content = bytes.ReplaceAll(content, []byte(".  "), []byte(". "))
	if err := os.WriteFile(path, content, info.Mode().Perm()); err != nil {
		panic(fmt.Errorf("write normalized environment document: %w", err))
	}
}
