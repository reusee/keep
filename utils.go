package main

import "fmt"
import "regexp"
import "math/big"

var (
	pt                = fmt.Printf
	blanksPattern     = regexp.MustCompile(`\s+`)
	zeroRat           = big.NewRat(0, 1)
	inlineDatePattern = regexp.MustCompile(`@[0-9]{4}[/.-][0-9]{2}[/.-][0-9]{2}`)
)
