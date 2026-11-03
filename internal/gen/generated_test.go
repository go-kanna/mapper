package gen_test

import (
	"errors"
	"fmt"
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

		Payment: model.Bank{Account: "0001"},
	}
}

// Functions declared for a pair of enums map the constants by name, and what the
// other enum has no value for is an error, UNSPECIFIED and the empty string too.
func TestGenerated_DeclaredEnums(t *testing.T) {
	t.Parallel()

	if got, err := mapper.ChannelToWire(model.ChannelPhone); err != nil || got != wire.Channel_CHANNEL_PHONE {
		t.Errorf("ChannelToWire(phone) = %v, %v", got, err)
	}
	if got, err := mapper.ChannelFromWire(wire.Channel_CHANNEL_EMAIL); err != nil || got != model.ChannelEmail {
		t.Errorf("ChannelFromWire(EMAIL) = %q, %v", got, err)
	}

	going := []struct {
		in   model.Channel
		want string
	}{
		{"fax", `wire.Channel has no value for "fax"`},
		{"", `wire.Channel has no value for ""`},
	}
	for _, tt := range going {
		got, err := mapper.ChannelToWire(tt.in)
		if got != wire.Channel_CHANNEL_UNSPECIFIED || err == nil || err.Error() != tt.want {
			t.Errorf("ChannelToWire(%q) = %v, %v, want UNSPECIFIED and %q", tt.in, got, err, tt.want)
		}
	}

	coming := []struct {
		in   wire.Channel
		want string
	}{
		{wire.Channel_CHANNEL_UNSPECIFIED, "model.Channel has no value for CHANNEL_UNSPECIFIED"},
		{wire.Channel(9), "model.Channel has no value for 9"},
	}
	for _, tt := range coming {
		got, err := mapper.ChannelFromWire(tt.in)
		if got != "" || err == nil || err.Error() != tt.want {
			t.Errorf("ChannelFromWire(%v) = %q, %v, want %q", tt.in, got, err, tt.want)
		}
	}
}

// A sum type goes out as the arm of the oneof its variant is named after, and
// comes back as that variant; none set is nil either way.
func TestGenerated_SumType(t *testing.T) {
	t.Parallel()

	for _, p := range []model.Payment{model.Bank{Account: "0001"}, model.Cash{Office: "Osaka"}, nil} {
		in := employee()
		in.Payment = p
		w := mapper.EmployeeToWire(in)
		switch arm := w.GetPayment().(type) {
		case *wire.Employee_Bank:
			if b, ok := p.(model.Bank); !ok || arm.Bank.Account != b.Account {
				t.Errorf("EmployeeToWire(%#v).Payment = %#v", p, arm)
			}
		case *wire.Employee_Cash:
			if c, ok := p.(model.Cash); !ok || arm.Cash.Office != c.Office {
				t.Errorf("EmployeeToWire(%#v).Payment = %#v", p, arm)
			}
		case nil:
			if p != nil {
				t.Errorf("EmployeeToWire(%#v).Payment = nil", p)
			}
		}

		got, err := mapper.EmployeeFromWire(w)
		if err != nil || got.Payment != p {
			t.Errorf("EmployeeFromWire().Payment = %#v, %v; want %#v", got.Payment, err, p)
		}
	}
}

// A pointer to a variant implements the sum type too, as the variant does so by
// value, so it goes out as the variant would, and a nil one as none.
func TestGenerated_SumTypePointer(t *testing.T) {
	t.Parallel()

	in := employee()
	in.Payment = &model.Bank{Account: "0001"}
	if arm, ok := mapper.EmployeeToWire(in).GetPayment().(*wire.Employee_Bank); !ok || arm.Bank.Account != "0001" {
		t.Errorf("EmployeeToWire(%#v).Payment = %#v", in.Payment, mapper.EmployeeToWire(in).GetPayment())
	}

	in.Payment = (*model.Cash)(nil)
	if got := mapper.EmployeeToWire(in).GetPayment(); got != nil {
		t.Errorf("EmployeeToWire(%#v).Payment = %#v, want nil", in.Payment, got)
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

// A nil maps as the empty value it points to would, wherever the destination
// cannot hold a nil, so that a rule sees it: the message and an optional field
// alike.
func TestGenerated_NilMapsAsEmpty(t *testing.T) {
	t.Parallel()

	for _, in := range []*wire.Profile{nil, {}} {
		if _, err := mapper.ProfileFromWire(in); err == nil ||
			!strings.HasPrefix(err.Error(), "map model.Profile.Nickname: rule: nickname") {
			t.Errorf("ProfileFromWire(%v) error = %v, want the nickname's rule to reject it", in, err)
		}
	}
	p, err := mapper.ProfileFromWire(&wire.Profile{Nickname: new("kanna")})
	if err != nil || p.Nickname.String() != "kanna" {
		t.Errorf("ProfileFromWire(kanna) = %+v, %v, want the nickname kanna", p, err)
	}
}

// A nil field that no rule sees leaves the destination's zero as it is.
func TestGenerated_NilFieldsLeaveTheZero(t *testing.T) {
	t.Parallel()

	for bio, want := range map[*string]string{nil: "", new("hi"): "hi"} {
		p, err := mapper.ProfileFromWire(&wire.Profile{Nickname: new("kanna"), Bio: bio})
		if err != nil || p.Bio != want {
			t.Errorf("ProfileFromWire(bio %v) = %+v, %v, want the bio %q", bio, p, err, want)
		}
	}
	g := model.MustGrade(3)
	for grade, want := range map[*model.Grade]int32{nil: 0, &g: 3} {
		if w := mapper.ProfileToWire(model.Profile{Grade: grade}); w.Grade != want {
			t.Errorf("ProfileToWire(grade %v).Grade = %d, want %d", grade, w.Grade, want)
		}
	}
}

// nil stays nil wherever the destination can hold it, and a nil message maps
// as an empty one.
func TestGenerated_Nil(t *testing.T) {
	t.Parallel()

	w := mapper.EmployeeToWire(model.Employee{})
	if w.Manager != nil || w.Offices != nil || w.ByRegion != nil || w.Note != nil || w.Contact.GetKind() != nil ||
		w.Backup != nil || w.LeftAt != nil || w.Peers != nil || w.ReviewedAt != nil || w.Former != nil ||
		w.Shift != nil {
		t.Errorf("EmployeeToWire(zero) = %+v, want nil fields and no contact arm", w)
	}
	// An enum's zero value, which stands for none, goes out as the unspecified
	// value, and comes back as itself where its function allows that.
	if w.Status != wire.Status_STATUS_UNSPECIFIED {
		t.Errorf("EmployeeToWire(zero).Status = %v, want STATUS_UNSPECIFIED", w.Status)
	}
	if e, err := mapper.EmployeeFromWire(&wire.Employee{Id: "192.0.2.1"}); err != nil || e.Status != "" {
		t.Errorf("EmployeeFromWire(no status) = %q, %v, want an empty status", e.Status, err)
	}

	got, err := mapper.EmployeeFromWire(nil)
	want, wantErr := mapper.EmployeeFromWire(&wire.Employee{})
	if fmt.Sprint(err) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
		t.Errorf("EmployeeFromWire(nil) = %+v, %v, want what an empty message maps to, %+v, %v", got, err, want, wantErr)
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
