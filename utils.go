package main

import "fmt"
import "regexp"
import "math/big"

var (
	pt            = fmt.Printf
	blanksPattern = regexp.MustCompile(`\s+`)
	zeroRat       = big.NewRat(0, 1)
)
