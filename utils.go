package main

import "fmt"
import "regexp"
import "math/big"

var (
	pt             = fmt.Printf
	blanksPattern  = regexp.MustCompile(`\s+`)
	dateSepPattern = regexp.MustCompile(`[-/.]`)
	zeroRat        = big.NewRat(0, 1)
)
