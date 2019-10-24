package main

import "go.starlark.net/starlark"

var starlarkThread = &starlark.Thread{
	Name: "ledger",
}

func expandExpr(str string) string {
	value, err := starlark.Eval(starlarkThread, "", str, nil)
	ce(err)
	return value.String()
}
