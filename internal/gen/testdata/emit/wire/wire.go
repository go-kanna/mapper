// Package wire is the other side of the emit test, written as protoc-gen-go
// writes messages: exported fields, nil-safe getters, unexported bookkeeping,
// and a oneof.
package wire

import "time"

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
