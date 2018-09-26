package main

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
)

var (
	pt      = fmt.Printf
	zeroRat = big.NewRat(0, 1)
)

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format, args...)
	pcs := make([]uintptr, 64)
	n := runtime.Callers(2, pcs)
	pcs = pcs[:n]
	frames := runtime.CallersFrames(pcs)
	type Entry struct {
		File string
		Line int
		Func string
	}
	var entries []Entry
	for {
		frame, more := frames.Next()
		entries = append(entries, Entry{
			File: filepath.Base(frame.File),
			Line: frame.Line,
			Func: frame.Function,
		})
		if !more {
			break
		}
	}
	maxFileLen := 0
	for _, entry := range entries {
		if l := len(entry.File); l > maxFileLen {
			maxFileLen = l
		}
	}
	formatStr := fmt.Sprintf("%%%ds:%%-6d - %%s\n", maxFileLen+4)
	for _, entry := range entries {
		fmt.Fprintf(os.Stderr, formatStr,
			entry.File,
			entry.Line,
			entry.Func,
		)
	}
	os.Exit(-1)
}
