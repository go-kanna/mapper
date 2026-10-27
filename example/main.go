// Command example maps a domain value onto the wire types and back, showing
// that what kanna-mapper generates is ordinary Go you call directly.
//
// Regenerate with:
//
//	go generate ./...
package main

import (
	"fmt"
	"os"
	"time"
	"uuid"

	"github.com/go-kanna/mapper/example/gen/employeev1"
	"github.com/go-kanna/mapper/example/mapper"
	"github.com/go-kanna/mapper/example/model"
)

func main() {
	emp := model.Employee{
		ID: uuid.New(),
		// The domain name; the wire calls it Name, which the map tag pairs up.
		FullName:  "Alice",
		HiredAt:   time.Date(2024, time.April, 1, 9, 30, 0, 0, time.UTC),
		Status:    model.StatusActive,
		Address:   model.Address{City: "Tokyo", Street: "1-2-3"},
		Nicknames: []string{"al", "ali"},
		// One of the contact fields is set; the wire carries it as a oneof.
		Contact:  model.Contact{Email: new("alice@example.com")},
		SyncedAt: time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
	}

	// The UUID becomes a string and the time a Timestamp through the shared
	// list in package convert; the status through the functions the mapper
	// package lists, so StatusActive does not turn into STATUS_UNSPECIFIED.
	wire := mapper.EmployeeToProto(emp)
	fmt.Fprintf(os.Stdout, "to wire:   id=%s hired=%s status=%d city=%s\n",
		wire.GetId(), wire.GetHiredAt().AsTime().Format(time.DateOnly), wire.GetStatus(), wire.GetAddress().GetCity())

	// Coming back can fail, because parsing a UUID can, so the function is
	// declared to return an error, which names the field it came from.
	back, err := mapper.EmployeeFromProto(wire)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stdout, "from wire: id=%s name=%s hired=%s same id=%t\n",
		back.ID, back.FullName, back.HiredAt.Format(time.DateOnly), back.ID == emp.ID)

	// The set contact field became the matching arm of the oneof, and comes
	// back as the same field; the other stays nil in both directions.
	fmt.Fprintf(os.Stdout, "oneof:     email=%s postal set=%t back email=%s postal nil=%t\n",
		wire.GetContact().GetEmail(), wire.GetContact().GetPostal() != nil, *back.Contact.Email, back.Contact.Postal == nil)

	// A nil message maps to the zero value rather than panicking.
	empty, err := mapper.EmployeeFromProto(nil)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stdout, "from nil:  zero value: %t\n", empty.ID == uuid.Nil() && empty.FullName == "")

	// SyncedAt is tagged map:"-", so it takes no part in either direction and
	// comes back zero however it was set.
	fmt.Fprintf(os.Stdout, "excluded:  SyncedAt dropped: %t\n", back.SyncedAt.IsZero())

	// A malformed id is reported with the field that produced it.
	if _, err := mapper.EmployeeFromProto(&employeev1.Employee{Id: "not-a-uuid"}); err != nil {
		fmt.Fprintln(os.Stdout, "bad id:   ", err)
	}
}
