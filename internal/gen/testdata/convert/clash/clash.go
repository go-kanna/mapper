// Package clash names two lists that convert int to string differently.
package clash

import "github.com/go-kanna/mapper/internal/gen/testdata/convert/shared"

//kanna:convert
var _ = []any{
	shared.Defaults,
	shared.Other,
}
