// Package wire is the other side of the emit test, written as protoc-gen-go
// writes messages: exported fields, nil-safe getters, unexported bookkeeping,
// and a oneof.
package wire

import (
	"strconv"
	"time"
)

// Timestamp stands in for timestamppb.Timestamp.
type Timestamp struct {
	Seconds int64
}

// NewTimestamp is timestamppb.New.
func NewTimestamp(t time.Time) *Timestamp {
	return &Timestamp{Seconds: t.Unix()}
}

// AsTime is (*timestamppb.Timestamp).AsTime: nil becomes the Unix epoch.
func (x *Timestamp) AsTime() time.Time {
	if x == nil {
		return time.Unix(0, 0).UTC()
	}
	return time.Unix(x.Seconds, 0).UTC()
}

// Status is an enum as protoc-gen-go writes one. RETIRED has no counterpart
// in the domain.
type Status int32

const (
	Status_STATUS_UNSPECIFIED Status = 0
	Status_STATUS_ACTIVE      Status = 1
	Status_STATUS_ON_LEAVE    Status = 2
	Status_STATUS_RETIRED     Status = 3
)

var Status_name = map[int32]string{
	0: "STATUS_UNSPECIFIED",
	1: "STATUS_ACTIVE",
	2: "STATUS_ON_LEAVE",
	3: "STATUS_RETIRED",
}

func (x Status) Enum() *Status {
	p := new(Status)
	*p = x
	return p
}

func (x Status) String() string {
	if name, ok := Status_name[int32(x)]; ok {
		return name
	}
	return strconv.Itoa(int(x))
}

type Employee struct {
	state    int
	Id       string
	Name     string
	Login    string
	HiredAt  *Timestamp
	Home     *Address
	Offices  []*Address
	ByRegion map[string]*Address
	Manager  *Employee
	Note     *string
	Contact  *Contact
	Backup   *string
	LeftAt   *Timestamp
	Peers    []string
	Revision int32

	Grade      int32
	ReviewedAt *Timestamp

	Status Status
	Former *Status
	Shift  *string

	Tags  []string
	Photo []byte

	Payment isEmployee_Payment
}

func (x *Employee) GetPayment() isEmployee_Payment {
	if x != nil {
		return x.Payment
	}
	return nil
}

type isEmployee_Payment interface {
	isEmployee_Payment()
}

type Employee_Bank struct {
	Bank *Bank
}

type Employee_Cash struct {
	Cash *Cash
}

func (*Employee_Bank) isEmployee_Payment() {}

func (*Employee_Cash) isEmployee_Payment() {}

type Bank struct {
	state   int
	Account string
}

type Cash struct {
	state  int
	Office string
}

// GetBackup reads a proto3 optional field: nil becomes "".
func (x *Employee) GetBackup() string {
	if x != nil && x.Backup != nil {
		return *x.Backup
	}
	return ""
}

func (x *Employee) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Employee) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Employee) GetHiredAt() *Timestamp {
	if x != nil {
		return x.HiredAt
	}
	return nil
}

// GetNote reads a proto3 optional field: nil becomes "".
func (x *Employee) GetNote() string {
	if x != nil && x.Note != nil {
		return *x.Note
	}
	return ""
}

type Address struct {
	state int
	City  string
}

func (x *Address) GetCity() string {
	if x != nil {
		return x.City
	}
	return ""
}

type Contact struct {
	state int
	Kind  isContact_Kind
}

func (x *Contact) GetKind() isContact_Kind {
	if x != nil {
		return x.Kind
	}
	return nil
}

type isContact_Kind interface {
	isContact_Kind()
}

type Contact_Email struct {
	Email string
}

type Contact_Postal struct {
	Postal *Address
}

func (*Contact_Email) isContact_Kind() {}

func (*Contact_Postal) isContact_Kind() {}
