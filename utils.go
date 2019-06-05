package main

import (
	"fmt"
	"math/big"
	"os/user"

	"github.com/reusee/e2"
)

var (
	pt      = fmt.Printf
	zeroRat = big.NewRat(0, 1)
	me      = e2.Default.WithName("keep").WithStack()
	ce, he  = e2.New(me)
	isRoot  = func() bool {
		u, err := user.Current()
		ce(err)
		return u.Username == "root"
	}()
)
