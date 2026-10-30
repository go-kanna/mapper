// Package model is the domain side of the emit test: the types an application
// owns and tags.
package model

import (
	"net/netip"
	"time"

	"github.com/go-kanna/value/rule"
)

// Login is a declared string, which converts to and from a plain one.
type Login string

// Grade holds what the wire holds, once its rule accepts it.
//
//kanna:value
type Grade struct{ n int32 }

func (Grade) validate(n int32) error {
	if n < 0 {
		return rule.Fail("grade")
	}
	return nil
}

// ReviewedAt holds a time from 2000 on, which the wire carries as a
// timestamp.
//
//kanna:value
type ReviewedAt struct{ t time.Time }

func (ReviewedAt) validate(t time.Time) error {
	if t.Year() < 2000 {
		return rule.Fail("reviewed_at")
	}
	return nil
}

// Status is an enum of kanna-value, which maps to the wire's enum by the names
// of their constants.
//
//kanna:enum
type Status string

const (
	StatusActive  Status = "active"
	StatusOnLeave Status = "on_leave"
)

// Shift is an enum of kanna-value that the wire carries as an optional string,
// held to its constants by its constructor on the way back.
//
//kanna:enum
type Shift string

const (
	ShiftDay   Shift = "day"
	ShiftNight Shift = "night"
)

// Employee is the aggregate the functions map.
type Employee struct {
	// ID stands in for a UUID: rendered as a string, and parsed back from one,
	// which can fail.
	ID netip.Addr

	FullName string `map:"Name"`
	Login    Login
	HiredAt  time.Time
	Home     Address
	Offices  []Address
	ByRegion map[string]Address
	Manager  *Employee
	Note     *string
	Contact  Contact
	Backup   *netip.Addr
	LeftAt   *time.Time
	Peers    []netip.Addr

	Grade      Grade
	ReviewedAt *ReviewedAt

	Status Status
	Former *Status
	Shift  *Shift

	// Tags is copied either way; Photo, tagged to be shared, is handed over as
	// it is coming back, as //kanna:share has it going out.
	Tags  []string
	Photo []byte `map:",share"`

	// SyncedAt never crosses the wire.
	SyncedAt time.Time `map:"-"`
}

// Address shares its name with the wire's, so the functions written for it are
// told apart by package.
type Address struct {
	City string
}

// Contact has at most one field set: the wire's oneof.
type Contact struct {
	Email  *string
	Postal *Address
}
