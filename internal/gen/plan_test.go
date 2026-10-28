package gen_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/pkgtest"

	"github.com/go-kanna/mapper/internal/gen"
)

func TestPlans(t *testing.T) {
	t.Parallel()

	found, diags := gen.Plans(pkgtest.Load(t, map[string]string{"plan.go": `package test

import "net/netip"

type Employee struct {
	ID      string
	Name    string
	Addr    netip.Addr
	Home    Address
	Offices []Address
	Manager *Employee
	Note    *string
	Count   *int64
	Code    Code
}

type Address struct{ City string }

type Code string

type Wire struct {
	Id      string
	Name    string
	Addr    string
	Home    *AddressPB
	Offices []*AddressPB
	Manager *Wire
	Note    *string
	Count   *int32
	Code    *string
}

func (w *Wire) GetName() string {
	if w != nil {
		return w.Name
	}
	return ""
}

// GetNote reads a proto3 optional field as protoc-gen-go does, a nil
// becoming "".
func (w *Wire) GetNote() string {
	if w != nil && w.Note != nil {
		return *w.Note
	}
	return ""
}

func (w *Wire) GetCount() int32 {
	if w != nil && w.Count != nil {
		return *w.Count
	}
	return 0
}

func (w *Wire) GetCode() string {
	if w != nil && w.Code != nil {
		return *w.Code
	}
	return ""
}

type AddressPB struct{ City string }

//kanna:mapper
type _ interface {
	// Count would narrow on the way out.
	//
	//kanna:map Count=-
	ToWire(Employee) *Wire
	FromWire(w *Wire) (Employee, error)
}

//kanna:converter
var _ = []any{
	netip.ParseAddr,
	netip.Addr.String,
}

// Taken by the package, so the function written for the pair is named
// otherwise.
func addressToAddressPB() {}
`}))

	want := []string{
		"ToWire(src test.Employee) *test.Wire\n" +
			"  Id = .ID direct\n" +
			"  Name = .Name direct\n" +
			"  Addr = .Addr conv:netip.Addr.String\n" +
			"  Home = .Home call:addressToAddressPB2\n" +
			"  Offices = .Offices slice(call:addressToAddressPB2)\n" +
			"  Manager = .Manager deref(call:ToWire)\n" +
			"  Note = .Note direct\n" +
			"  Code = .Code addr(cast:string)",
		// The getter reads Name; Note, Count, and Code are read as the fields,
		// which keep their nil where the getters would make them zero, whether
		// or not the destination can hold it.
		"FromWire(w *test.Wire) test.Employee (fallible)\n" +
			"  ID = .Id direct\n" +
			"  Name = .GetName() direct\n" +
			"  Addr = .Addr conv:netip.ParseAddr\n" +
			"  Home = .Home call:addressPBToAddress\n" +
			"  Offices = .Offices slice(call:addressPBToAddress)\n" +
			"  Manager = .Manager guard(addr(call:FromWire))\n" +
			"  Note = .Note direct\n" +
			"  Count = .Count deref(addr(cast:int64))\n" +
			"  Code = .Code deref(cast:test.Code)",
		"addressToAddressPB2(src test.Address) *test.AddressPB\n  City = .City direct",
		"addressPBToAddress(src *test.AddressPB) test.Address\n  City = .City direct",
	}
	if !slices.Equal(found, want) {
		t.Errorf("plans =\n%s\nwant\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
	}
	if len(diags) > 0 {
		t.Errorf("diagnostics = %q, want none", diags)
	}
}

// A getter promoted from an embedded struct reads that struct's field, so the
// field of the same name beside it is read as it is.
func TestPlans_PromotedGetter(t *testing.T) {
	t.Parallel()

	found, diags := gen.Plans(pkgtest.Load(t, map[string]string{"test.go": `package test

type Base struct{ ID string }

func (b *Base) GetID() string { return b.ID }

type S struct {
	Base
	ID string
}

type D struct{ ID string }

//kanna:mapper
type _ interface{ F(S) D }
`}))

	if want := []string{"F(src test.S) test.D\n  ID = .ID direct"}; !slices.Equal(found, want) {
		t.Errorf("plans = %q, want %q", found, want)
	}
	if len(diags) > 0 {
		t.Errorf("diagnostics = %q, want none", diags)
	}
}

// A function written for a pair is named after its types, a plural initialism
// lowered whole: idsToIDsPB, not iDsToIDsPB.
func TestPlans_PluralInitialismInAName(t *testing.T) {
	t.Parallel()

	found, diags := gen.Plans(pkgtest.Load(t, map[string]string{"test.go": `package test

type IDs struct{ N int }

type IDsPB struct{ N int }

type S struct{ IDs IDs }

type D struct{ IDs *IDsPB }

//kanna:mapper
type _ interface{ F(S) D }
`}))

	want := []string{
		"F(src test.S) test.D\n  IDs = .IDs call:idsToIDsPB",
		"idsToIDsPB(src test.IDs) *test.IDsPB\n  N = .N direct",
	}
	if !slices.Equal(found, want) {
		t.Errorf("plans = %q, want %q", found, want)
	}
	if len(diags) > 0 {
		t.Errorf("diagnostics = %q, want none", diags)
	}
}

func TestPlans_Diagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string // after the package clause and the import of strconv
		want string
	}{
		{
			name: "a fallible conversion in a function declared without an error",
			src: "type S struct{ N string }\ntype D struct{ N int }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n\n" +
				"//kanna:converter\nvar _ = []any{strconv.Atoi}\n",
			want: "test.go:12:2: error: F has to return an error: N is converted by strconv.Atoi, which can fail\n" +
				"  declare it as F(test.S) (test.D, error)",
		},
		{
			name: "a fallible conversion of each element",
			src: "type S struct{ Ns []string }\ntype D struct{ Ns []int }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n\n" +
				"//kanna:converter\nvar _ = []any{strconv.Atoi}\n",
			want: "test.go:12:2: error: F has to return an error: Ns is converted by strconv.Atoi, which can fail",
		},
		{
			name: "a fallible conversion behind a pointer",
			src: "type S struct{ N *string }\ntype D struct{ N *int }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n\n" +
				"//kanna:converter\nvar _ = []any{strconv.Atoi}\n",
			want: "test.go:12:2: error: F has to return an error: N is converted by strconv.Atoi, which can fail",
		},
		{
			name: "a written function that can fail",
			src: "type S struct{ In In }\ntype In struct{ N string }\ntype D struct{ In Out }\ntype Out struct{ N int }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n\n" +
				"//kanna:converter\nvar _ = []any{strconv.Atoi}\n",
			want: "test.go:14:2: error: F has to return an error: In is mapped by inToOut, which can fail\n" +
				"  declare it as F(test.S) (test.D, error)",
		},
		{
			name: "nothing converts",
			src: "type Status int32\ntype Code int32\ntype S struct{ Status Status }\ntype D struct{ Status Code }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n",
			want: "test.go:14:2: error: F: cannot map test.S.Status (test.Status) to test.D.Status (test.Code)\n" +
				"  add a func(test.Status) test.Code to a //kanna:converter list",
		},
		{
			name: "a function of the package left out of the lists",
			src: "type Status int32\ntype Code int32\ntype S struct{ Status Status }\ntype D struct{ Status Code }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n\n" +
				"func statusToCode(s Status) Code { return Code(s) }\n",
			want: "test.go:14:2: error: F: cannot map test.S.Status (test.Status) to test.D.Status (test.Code)\n" +
				"  statusToCode converts it; add it to a //kanna:converter list",
		},
		{
			name: "an interface",
			src:  "type S struct{ V any }\ntype D struct{ V string }\n\n//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n",
			want: "test.go:12:2: error: F: cannot map test.S.V (any) to test.D.V (string)\n" +
				"  add a func(any) string to a //kanna:converter list\n" +
				"  an interface field is mapped only as a protobuf oneof, whose arms are single-field structs implementing it",
		},
		{
			name: "a problem in a written function",
			src: "type S struct{ Home Address }\ntype Address struct{ City string }\n" +
				"type D struct{ Home *AddressPB }\ntype AddressPB struct{ City, Etag string }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n",
			want: "test.go:14:2: error: F: Home: no source in test.Address for test.AddressPB.Etag\n" +
				"  declare AddressToAddressPB(test.Address) *test.AddressPB in the //kanna:mapper interface" +
				" and add //kanna:map Etag=-",
		},
		{
			name: "a name the package declares",
			src: "type S struct{ A string }\ntype D struct{ A string }\n\n//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n\n" +
				"func F(s S) D { return D(s) }\n",
			want: "test.go:12:2: error: F is declared in the package already, at test.go:15:6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := "package test\n\nimport \"strconv\"\n\nvar _ = strconv.Itoa\n\n" + tt.src
			_, diags := gen.Plans(pkgtest.Load(t, map[string]string{"test.go": src}))
			if !slices.ContainsFunc(diags, func(d string) bool { return strings.Contains(d, tt.want) }) {
				t.Errorf("diagnostics = %q, want one containing %q", diags, tt.want)
			}
		})
	}
}

func TestPlans_ValueTypes(t *testing.T) {
	t.Parallel()

	found, diags := gen.Plans(loadAll(t, "./testdata/checked").Packages[0])

	want := []string{
		"TodoFromRow(src checked.TodoRow) checked.Todo\n" +
			"  CompletedAt = .CompletedAt deref(addr(new:checked.NewCompletedAt(direct)))",
		"SessionFromRow(src checked.SessionRow) checked.Session (fallible)",
		"PageToLink(src checked.Page) checked.Link\n  URL = .URL get:String(conv:url.Parse)",
		"OddsFromRow(src checked.OddRow) checked.Odds (fallible)",
		"HolderFromRow(src checked.LevelRow) mid.Holder (fallible)",
	}
	if !slices.Equal(found, want) {
		t.Errorf("plans =\n%s\nwant\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
	}
	wantDiags := []string{
		"checked.go:117:2: error: SessionFromRow: cannot map checked.SessionRow.Token (*string)" +
			" to checked.Session.Token (*private.Token)\n" +
			"  add a func(*string) *private.Token to a //kanna:converter list\n" +
			"  its constructor, private.newToken, cannot be called here: it is not exported",
		// Odd has an accessor but no constructor of kanna-value's, so only the
		// function that does convert is hinted at.
		"checked.go:122:2: error: OddsFromRow: cannot map checked.OddRow.N (int) to checked.Odds.N (checked.Odd)\n" +
			"  NewOdd converts it; add it to a //kanna:converter list",
		// Package deep is seen only through package mid, without NewLevel.
		"checked.go:123:2: error: HolderFromRow: cannot map checked.LevelRow.Level (int) to mid.Holder.Level (deep.Level)\n" +
			"  add a func(int) deep.Level to a //kanna:converter list\n" +
			"  a value type of kanna-value is made by deep.NewLevel(int) (deep.Level, error), " +
			"which this package does not see; list it in a //kanna:converter list",
		"checked.go:116:2: error: TodoFromRow has to return an error:" +
			" CompletedAt is made by checked.NewCompletedAt, which can fail\n" +
			"  declare it as TodoFromRow(checked.TodoRow) (checked.Todo, error)",
		"checked.go:120:2: error: PageToLink has to return an error: URL is converted by url.Parse, which can fail\n" +
			"  declare it as PageToLink(checked.Page) (checked.Link, error)",
	}
	if !slices.EqualFunc(diags, wantDiags, strings.HasSuffix) {
		t.Errorf("diagnostics =\n%s\nwant ones ending in\n%s", strings.Join(diags, "\n"), strings.Join(wantDiags, "\n"))
	}
}

func TestUnexported(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"Address":   "address",
		"ID":        "id",
		"URLInfo":   "urlInfo",
		"IDs":       "ids",
		"URLs":      "urls",
		"IDsOf":     "idsOf",
		"X":         "x",
		"lowerCase": "lowerCase",
	} {
		if got := gen.Unexported(in); got != want {
			t.Errorf("unexported(%q) = %q, want %q", in, got, want)
		}
	}
}
