package main

import (
	"context"
	"io"
)

// Run is the bootstrap seam. T1 RED intentionally leaves behavior unimplemented.
func Run(context.Context, []string, io.Writer, io.Writer) error { return nil }
