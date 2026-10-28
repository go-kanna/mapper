// Package hidden names a list holding a function it cannot call.
package hidden

import "github.com/go-kanna/mapper/internal/gen/testdata/convert/shared"

//kanna:converter
var _ = []any{
	shared.Hidden,
}
