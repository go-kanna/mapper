// Package shared holds lists that other packages name.
package shared

import (
	"net/netip"
	"strconv"
)

// Defaults is what every package converts the same way.
//
//kanna:convert
var Defaults = []any{
	netip.ParseAddr,
	netip.Addr.String,
	strconv.Itoa,
	More,
}

// More is named by Defaults, so what it lists is one step further away.
//
//kanna:convert
var More = []any{
	strconv.Atoi,
	strconv.Itoa, // Defaults has it nearer.
}

// Other converts a pair Defaults converts too, at the same distance from a
// package naming both.
//
//kanna:convert
var Other = []any{
	Hash,
}

// Hash renders n as no one reads it.
func Hash(n int) string { return "#" + strconv.Itoa(n) }

// Hidden lists a function that no other package can call.
//
//kanna:convert
var Hidden = []any{
	parse,
}

func parse(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
