// Package mapper converts between the domain model and the employeev1 wire
// types.
package mapper

//go:generate go tool kanna-mapper

import (
	"github.com/go-kanna/mapper/example/convert"
	"github.com/go-kanna/mapper/example/gen/employeev1"
	"github.com/go-kanna/mapper/example/model"
)

//kanna:mapper
type _ interface {
	// EmployeeToProto converts an employee for the API.
	//
	// The server owns Revision, which the domain has no say in.
	//
	//kanna:map Revision=-
	EmployeeToProto(emp model.Employee) *employeev1.Employee

	// EmployeeFromProto converts an employee from the API. It fails when the
	// id is not a UUID.
	EmployeeFromProto(emp *employeev1.Employee) (model.Employee, error)
}

//kanna:converter
var _ = []any{
	convert.Defaults,
	statusToProto,
	statusFromProto,
}

// statusToProto is listed because the two enums number their statuses
// differently: converting the number alone would make StatusActive, 0, the
// wire's STATUS_UNSPECIFIED.
func statusToProto(s model.Status) employeev1.Status {
	switch s {
	case model.StatusActive:
		return employeev1.Status_STATUS_ACTIVE
	case model.StatusOnLeave:
		return employeev1.Status_STATUS_ON_LEAVE
	}
	return employeev1.Status_STATUS_UNSPECIFIED
}

func statusFromProto(s employeev1.Status) model.Status {
	if s == employeev1.Status_STATUS_ON_LEAVE {
		return model.StatusOnLeave
	}
	return model.StatusActive
}
