// Package convert lists the conversions every mapping package of the app
// shares, so that each package names the list instead of repeating it.
package convert

import (
	"uuid"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Defaults converts what the domain and the wire spell differently everywhere:
// a UUID is a string on the wire, and a time a well-known Timestamp. The
// functions and methods are listed as they are, with no wrappers.
//
//kanna:converter
var Defaults = []any{
	uuid.UUID.String,
	uuid.Parse,
	timestamppb.New,
	(*timestamppb.Timestamp).AsTime,
}
