// Package model is the domain side of the emit test: the types an application
// owns and tags.
package model

import (
	"net/netip"
	"time"
)

// Login is a declared string, which converts to and from a plain one.
type Login string

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
