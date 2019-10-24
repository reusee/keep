package main

import "go.starlark.net/starlark"

var starlarkThread = &starlark.Thread{
	Name: "ledger",
}

func expandExpr(str string) string {
	value, err := starlark.Eval(starlarkThread, "", str, nil)
	ce(err)
	str, ok := starlark.AsString(value)
	if !ok {
		panic("not string")
	}
	return str
}
