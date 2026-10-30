package gen_test

import (
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-kanna/value/rule"

	"github.com/go-kanna/mapper/internal/gen/testdata/emit/mapper"
	"github.com/go-kanna/mapper/internal/gen/testdata/emit/model"
	"github.com/go-kanna/mapper/internal/gen/testdata/emit/wire"
)

// employee sets every field the generated functions map.
func employee() model.Employee {
	return model.Employee{
		ID:       netip.MustParseAddr("192.0.2.1"),
		FullName: "Alice",
		Login:    "alice",
		HiredAt:  time.Date(2024, time.April, 1, 9, 30, 0, 0, time.UTC),
		Home:     model.Address{City: "Tokyo"},
		Offices:  []model.Address{{City: "Osaka"}, {City: "Nagoya"}},
		ByRegion: map[string]model.Address{"west": {City: "Kobe"}},
		Manager:  &model.Employee{ID: netip.MustParseAddr("192.0.2.2"), FullName: "Bob"},
		Note:     new("on leave"),
		Contact:  model.Contact{Email: new("alice@example.com")},
		Backup:   new(netip.MustParseAddr("192.0.2.9")),
		LeftAt:   new(time.Date(2026, time.March, 31, 18, 0, 0, 0, time.UTC)),
		Peers:    []netip.Addr{netip.MustParseAddr("192.0.2.3")},

		Grade:      model.MustGrade(3),
		ReviewedAt: new(model.MustReviewedAt(time.Date(2025, time.October, 1, 0, 0, 0, 0, time.UTC))),

		Status: model.StatusOnLeave,
		Former: new(model.StatusActive),
		Shift:  new(model.ShiftNight),

		Tags:  []string{"go"},
		Photo: []byte{0xff, 0xd8},
	}
}

// A slice of the same type is copied, unless the field is to be shared: changing
// what one side holds then changes the other's, in either direction.
func TestGenerated_Copies(t *testing.T) {
	t.Parallel()

	in := employee()
	w := mapper.EmployeeToWire(in)
	w.Tags[0] = "changed in the wire"
	w.Photo[0] = 0
	if in.Tags[0] != "go" {
		t.Errorf("Tags[0] = %q after the wire's was changed, want it copied, and still %q", in.Tags[0], "go")
	}
	if in.Photo[0] != 0 {
		t.Errorf("Photo[0] = %#x after the wire's was changed, want it shared, as //kanna:share has it", in.Photo[0])
	}

	back, err := mapper.EmployeeFromWire(&wire.Employee{Id: "192.0.2.1", Tags: []string{"go"}, Photo: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	if back.Tags == nil || back.Photo == nil {
		t.Fatalf("EmployeeFromWire() = %+v, want Tags and Photo", back)
	}
	if mapper.EmployeeToWire(model.Employee{}).Tags != nil {
		t.Error("Tags of the zero employee is not nil: a copy of nil should stay nil")
	}
}

func TestGenerated_RoundTrip(t *testing.T) {
	t.Parallel()

	in := employee()
	in.SyncedAt = time.Now() // tagged map:"-", so it does not come back

	w := mapper.EmployeeToWire(in)
	if w.GetId() != "192.0.2.1" || w.GetName() != "Alice" || w.Login != "alice" || w.Revision != 0 ||
		w.Status != wire.Status_STATUS_ON_LEAVE || w.Former == nil || *w.Former != wire.Status_STATUS_ACTIVE ||
		w.Shift == nil || *w.Shift != "night" {
		t.Errorf("EmployeeToWire() = %+v", w)
	}

	got, err := mapper.EmployeeFromWire(w)
	if err != nil {
		t.Fatalf("EmployeeFromWire() error = %v", err)
	}
	if want := employee(); !reflect.DeepEqual(got, want) {
		t.Errorf("round trip =\n%+v\nwant\n%+v", got, want)
	}
}

// nil stays nil wherever the destination can hold it, and a nil message maps
// to the zero value.
func TestGenerated_Nil(t *testing.T) {
	t.Parallel()

	w := mapper.EmployeeToWire(model.Employee{})
	if w.Manager != nil || w.Offices != nil || w.ByRegion != nil || w.Note != nil || w.Contact.GetKind() != nil ||
		w.Backup != nil || w.LeftAt != nil || w.Peers != nil || w.ReviewedAt != nil || w.Former != nil ||
		w.Shift != nil {
		t.Errorf("EmployeeToWire(zero) = %+v, want nil fields and no contact arm", w)
	}
	// An enum's zero value, which stands for none, goes out as the unspecified
	// value, and comes back as itself.
	if w.Status != wire.Status_STATUS_UNSPECIFIED {
		t.Errorf("EmployeeToWire(zero).Status = %v, want STATUS_UNSPECIFIED", w.Status)
	}

	got, err := mapper.EmployeeFromWire(nil)
	if err != nil || !reflect.DeepEqual(got, model.Employee{}) {
		t.Errorf("EmployeeFromWire(nil) = %+v, %v, want the zero value", got, err)
	}

	// AsTime makes a missing timestamp the Unix epoch, as the README says,
	// but a pointer to fill stays nil.
	got, err = mapper.EmployeeFromWire(&wire.Employee{Id: "192.0.2.1"})
	if err != nil || !got.HiredAt.Equal(time.Unix(0, 0)) || got.LeftAt != nil || got.Backup != nil ||
		got.ReviewedAt != nil {
		t.Errorf("EmployeeFromWire() = %+v, %v, want HiredAt the Unix epoch and LeftAt, Backup, and ReviewedAt nil",
			got, err)
	}
}

// A conversion that fails is reported with the destination field it was for,
// through every function on the way.
func TestGenerated_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   *wire.Employee
		want string
	}{
		{name: "the employee", in: &wire.Employee{Id: "nope"}, want: "map model.Employee.ID: "},
		{
			name: "behind a pointer",
			in:   &wire.Employee{Id: "192.0.2.1", Backup: new("nope")},
			want: "map model.Employee.Backup: ",
		},
		{
			name: "an element",
			in:   &wire.Employee{Id: "192.0.2.1", Peers: []string{"nope"}},
			want: "map model.Employee.Peers: ",
		},
		{
			name: "a value its rule rejects",
			in:   &wire.Employee{Id: "192.0.2.1", Grade: -1},
			want: "map model.Employee.Grade: rule: grade",
		},
		{
			name: "a value its rule rejects, behind a pointer",
			in:   &wire.Employee{Id: "192.0.2.1", ReviewedAt: &wire.Timestamp{}},
			want: "map model.Employee.ReviewedAt: rule: reviewed_at",
		},
		{
			name: "a value of an enum the domain does not have",
			in:   &wire.Employee{Id: "192.0.2.1", Status: wire.Status_STATUS_RETIRED},
			want: "map model.Employee.Status: model.Status has no value for STATUS_RETIRED",
		},
		{
			name: "a number of an enum nobody declared",
			in:   &wire.Employee{Id: "192.0.2.1", Former: new(wire.Status(9))},
			want: "map model.Employee.Former: model.Status has no value for 9",
		},
		{
			name: "a string none of the constants of an enum holds",
			in:   &wire.Employee{Id: "192.0.2.1", Shift: new("evening")},
			want: "map model.Employee.Shift: rule: inclusion",
		},
		{
			name: "the manager",
			in:   &wire.Employee{Id: "192.0.2.1", Manager: &wire.Employee{Id: "nope"}},
			want: "map model.Employee.Manager: map model.Employee.ID: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := mapper.EmployeeFromWire(tt.in); err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("EmployeeFromWire() error = %v, want it to start with %q", err, tt.want)
			}
		})
	}
}

// A rule's rejection reaches the caller as the rule's own error, wrapped.
func TestGenerated_RuleError(t *testing.T) {
	t.Parallel()

	_, err := mapper.EmployeeFromWire(&wire.Employee{Id: "192.0.2.1", Grade: -1})
	if rerr, ok := errors.AsType[*rule.Error](err); !ok || rerr.Code() != "grade" {
		t.Errorf("EmployeeFromWire() error = %v, want a *rule.Error with the code grade", err)
	}
}

func TestGenerated_Oneof(t *testing.T) {
	t.Parallel()

	// The first field set, in the oneof's order, fills the arm.
	in := employee()
	in.Contact = model.Contact{Postal: &model.Address{City: "Sapporo"}}
	w := mapper.EmployeeToWire(in)
	postal, ok := w.Contact.GetKind().(*wire.Contact_Postal)
	if !ok || postal.Postal.GetCity() != "Sapporo" {
		t.Fatalf("Contact.Kind = %#v, want the postal arm", w.Contact.GetKind())
	}

	got, err := mapper.EmployeeFromWire(w)
	if err != nil {
		t.Fatal(err)
	}
	if got.Contact.Email != nil || got.Contact.Postal == nil || got.Contact.Postal.City != "Sapporo" {
		t.Errorf("Contact = %+v, want the postal address alone", got.Contact)
	}

	// With both set, the first arm in the oneof's order wins.
	in.Contact = model.Contact{Email: new("alice@example.com"), Postal: &model.Address{City: "Sapporo"}}
	w = mapper.EmployeeToWire(in)
	if _, ok := w.Contact.GetKind().(*wire.Contact_Email); !ok {
		t.Errorf("Contact.Kind with both fields set = %#v, want the email arm", w.Contact.GetKind())
	}
}
