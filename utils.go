package main

import (
	"fmt"
	"math/big"
	"os/user"

	"github.com/reusee/e"
)

var (
	pt             = fmt.Printf
	zeroRat        = big.NewRat(0, 1)
	me, we, ce, he = e.New(e.WithPackage("keep"))
	isRoot         = func() bool {
		u, err := user.Current()
		ce(err)
		return u.Username == "root"
	}()
)
