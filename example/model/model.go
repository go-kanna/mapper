// Package model holds the hand-written domain types.
package model

import (
	"time"
	"uuid"
)

// Status is the domain's own enum. The wire has one too, numbered differently,
// so the two convert only through the functions the mapper package lists.
type Status int

const (
	StatusActive Status = iota
	StatusOnLeave
)

// Employee is the domain-side aggregate.
type Employee struct {
	// Matched to the wire's Id without a tag: names that differ only in case
	// still find each other. The conversions come from the shared list in
	// package convert.
	ID uuid.UUID

	// The domain calls this FullName where the wire says Name, so the tag spells
	// out which field it pairs with. Without it, the wire's Name would be
	// reported as having no source rather than guessed at.
	FullName string `map:"Name"`

	HiredAt   time.Time
	Status    Status
	Address   Address
	Nicknames []string
	Contact   Contact

	// Local bookkeeping that never crosses the wire. Without the tag, the
	// function coming back would fail to generate: the wire has nothing to fill
	// it from.
	SyncedAt time.Time `map:"-"`
}

// Address is a nested value object. No function is declared for it: the
// mapper writes one for each direction.
type Address struct {
	City   string
	Street string
}

// Contact is one way to reach the employee: at most one field is set, which is
// the shape the wire's oneof maps onto. Each field pairs with the arm of the
// same name.
type Contact struct {
	Email  *string
	Postal *Address
}
