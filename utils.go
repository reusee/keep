package main

import (
	"fmt"
	"math/big"
	"os/user"

	"github.com/reusee/e"
)

var (
	pt      = fmt.Printf
	zeroRat = big.NewRat(0, 1)
	me      = e.Default.WithName("keep").WithStack()
	ce, he  = e.New(me)
	isRoot  = func() bool {
		u, err := user.Current()
		ce(err)
		return u.Username == "root"
	}()
)
