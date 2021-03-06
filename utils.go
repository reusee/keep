package main

import (
	"fmt"
	"math/big"
	"os/user"

	"github.com/reusee/e/v2"
)

var (
	pt      = fmt.Printf
	zeroRat = big.NewRat(0, 1)
	oneCent = big.NewRat(1, 100)
	me      = e.Default.WithName("keep").WithStack()
	ce, he  = e.New(me)
	isRoot  = func() bool {
		u, err := user.Current()
		ce(err)
		return u.Username == "root"
	}()
)

type (
	any = interface{}
)
