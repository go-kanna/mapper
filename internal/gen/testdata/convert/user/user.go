// Package user lists the shared conversions, and one of its own in place of
// one of them.
package user

import (
	"strconv"

	"github.com/go-kanna/mapper/internal/gen/testdata/convert/shared"
)

//kanna:converter
var _ = []any{
	shared.Defaults,
	itoa, // nearer than the strconv.Itoa that shared.Defaults brings
}

func itoa(n int) string { return "#" + strconv.Itoa(n) }
