// Package mapper declares the functions of the emit test. mapper_gen.go is the
// golden file, which the tests call.
package mapper

import (
	"net/netip"

	"github.com/go-kanna/mapper/internal/gen/testdata/emit/model"
	"github.com/go-kanna/mapper/internal/gen/testdata/emit/wire"
)

//kanna:mapper
type _ interface {
	// EmployeeToWire converts an employee for the API.
	//
	// The server sets Revision.
	//
	//kanna:map Revision=-
	//kanna:share Photo
	EmployeeToWire(model.Employee) *wire.Employee
	EmployeeFromWire(e *wire.Employee) (model.Employee, error)
}

//kanna:converter
var _ = []any{
	netip.ParseAddr,
	netip.Addr.String,
	wire.NewTimestamp,
	(*wire.Timestamp).AsTime,
}
