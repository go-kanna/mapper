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
			name: "a constant of an enum with no counterpart",
			src: "type Status string\n\n" +
				"const (\n\tStatusActive Status = \"active\"\n\tStatusRefunded Status = \"refunded\"\n)\n\n" +
				"type State int32\n\nconst State_STATE_ACTIVE State = 1\n\nfunc (x State) Enum() *State { return &x }\n\n" +
				"type S struct{ Status Status }\ntype D struct{ Status State }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n",
			want: "test.go:25:2: error: F: Status: test.StatusRefunded has no counterpart in test.State\n" +
				"  add STATE_REFUNDED to it, or list a func(test.Status) test.State in a //kanna:converter list",
		},
		{
			name: "an enum coming back from protobuf",
			src: "type Status string\n\nconst StatusActive Status = \"active\"\n\n" +
				"type State int32\n\nconst State_STATE_ACTIVE State = 1\n\nfunc (x State) Enum() *State { return &x }\n\n" +
				"type S struct{ Status Status }\ntype D struct{ Status State }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(D) S\n}\n",
			want: "test.go:22:2: error: F has to return an error: Status is mapped by stateToStatus, which can fail\n" +
				"  declare it as F(test.D) (test.S, error)",
		},
		{
			name: "a variant of a sum type with no arm in the oneof",
			src: "type Payment interface{ isPayment() }\n" +
				"type Bank struct{ Account string }\ntype Card struct{ Number string }\n" +
				"func (Bank) isPayment() {}\nfunc (Card) isPayment() {}\ntype S struct{ Payment Payment }\n" +
				"type BankPB struct{ Account string }\ntype CashPB struct{ Office string }\n" +
				"type isD_Payment interface{ isD_Payment() }\ntype D_Bank struct{ Bank *BankPB }\n" +
				"type D_Cash struct{ Cash *CashPB }\nfunc (*D_Bank) isD_Payment() {}\nfunc (*D_Cash) isD_Payment() {}\n" +
				"type D struct{ Payment isD_Payment }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n",
			want: "error: F: Payment: test.Card has no arm in test.isD_Payment\n" +
				"  add an arm named Card to the oneof, or holding a message of that name",
		},
		{
			name: "an arm of a oneof with no variant in the sum type",
			src: "type Payment interface{ isPayment() }\n" +
				"type Bank struct{ Account string }\ntype Card struct{ Number string }\n" +
				"func (Bank) isPayment() {}\nfunc (Card) isPayment() {}\ntype S struct{ Payment Payment }\n" +
				"type BankPB struct{ Account string }\ntype CashPB struct{ Office string }\n" +
				"type isD_Payment interface{ isD_Payment() }\ntype D_Bank struct{ Bank *BankPB }\n" +
				"type D_Cash struct{ Cash *CashPB }\nfunc (*D_Bank) isD_Payment() {}\nfunc (*D_Cash) isD_Payment() {}\n" +
				"type D struct{ Payment isD_Payment }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tG(D) (S, error)\n}\n",
			want: "error: G: Payment: test.D_Cash.Cash (arm of Payment) has no variant in test.Payment\n" +
				"  declare a variant named Cash, or map the field with a function of your own",
		},
		{
			name: "a field to share that the destination lacks",
			src: "type S struct{ Data []byte }\ntype D struct{ Data []byte }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\t//kanna:share Dat\n\tF(S) D\n}\n",
			want: "test.go:12:2: error: F: //kanna:share Dat names no field of test.D\n  did you mean Data?",
		},
		{
			name: "a field to share that holds no slice or map",
			src: "type S struct{ N int }\ntype D struct{ N int }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\t//kanna:share N\n\tF(S) D\n}\n",
			want: "test.go:12:2: error: F: //kanna:share N: N is of type int, and only a slice or a map is copied",
		},
		{
			name: "an option of the tag it does not know",
			src: "type S struct{ Data []byte `map:\",copy\"` }\ntype D struct{ Data []byte }\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tF(S) D\n}\n",
			want: `invalid map tag ",copy": the only option is share`,
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

// The constants of an enum of kanna-value and of protobuf are matched by their
// names, past the names of their types. A value in neither direction's cases
// becomes the unspecified value going out and an error coming back.
func TestPlans_Enums(t *testing.T) {
	t.Parallel()

	found, diags := gen.Plans(pkgtest.Load(t, map[string]string{"test.go": `package test

type OrderStatus string

const (
	OrderStatusPlaced  OrderStatus = "placed"
	OrderStatusOnHold  OrderStatus = "on_hold"
	OrderStatusSettled OrderStatus = "settled"
)

// Order_Status is an enum declared in the message Order, whose constants
// protoc-gen-go names after the message. CANCELLED is an alias of CANCELED.
type Order_Status int32

const (
	Order_STATUS_UNSPECIFIED Order_Status = 0
	Order_STATUS_PLACED      Order_Status = 1
	Order_STATUS_ON_HOLD     Order_Status = 2
	Order_SETTLED            Order_Status = 3
	Order_STATUS_CANCELED    Order_Status = 4
	Order_STATUS_CANCELLED   Order_Status = 4
)

func (x Order_Status) Enum() *Order_Status { return &x }

type Order struct{ Status OrderStatus }

type OrderPB struct{ Status Order_Status }

//kanna:mapper
type _ interface {
	OrderToPB(Order) OrderPB
	OrderFromPB(OrderPB) (Order, error)
}
`}))

	want := []string{
		"OrderToPB(src test.Order) test.OrderPB\n  Status = .Status call:orderStatusToOrder_Status",
		"OrderFromPB(src test.OrderPB) test.Order (fallible)\n  Status = .Status call:order_StatusToOrderStatus",
		"orderStatusToOrder_Status(src test.OrderStatus) test.Order_Status\n" +
			"  OrderStatusPlaced -> Order_STATUS_PLACED\n" +
			"  OrderStatusOnHold -> Order_STATUS_ON_HOLD\n" +
			"  OrderStatusSettled -> Order_SETTLED\n" +
			"  else -> Order_STATUS_UNSPECIFIED",
		"order_StatusToOrderStatus(src test.Order_Status) test.OrderStatus (fallible)\n" +
			`  Order_STATUS_UNSPECIFIED -> ""` + "\n" +
			"  Order_STATUS_PLACED -> OrderStatusPlaced\n" +
			"  Order_STATUS_ON_HOLD -> OrderStatusOnHold\n" +
			"  Order_SETTLED -> OrderStatusSettled",
	}
	if !slices.Equal(found, want) {
		t.Errorf("plans =\n%s\nwant\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
	}
	if len(diags) > 0 {
		t.Errorf("diagnostics = %q, want none", diags)
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

// A strict mapping fills only the destination fields named for it, by
// //kanna:map or a tag, and writes no function of its own for a pair of
// structs, whose fields it would fill unnamed.
func TestPlans_Strict(t *testing.T) {
	t.Parallel()

	const types = "type User struct {\n\tID, Name, Email, Token, Note string\n\tHome Address\n}\n" +
		"type Address struct{ City string }\n" +
		"type Public struct {\n\tID, Name string\n\tMail string `map:\"Email\"`\n\tNote string\n}\n" +
		"type Mailed struct {\n\tID string\n\tAddress string\n}\n" +
		"type Homed struct {\n\tID string\n\tHome PublicAddress\n}\n" +
		"type PublicAddress struct{ City string }\n"

	tests := []struct {
		name, methods, want string
	}{
		{
			name:    "every field named, by //kanna:map and a tag",
			methods: "\t//kanna:map ID Name Note=-\n\tToPublic(User) Public\n",
		},
		{
			name:    "a field matched by name alone",
			methods: "\t//kanna:map ID Note=-\n\tToPublic(User) Public\n",
			want: "test.go:30:2: error: ToPublic: test.Public.Name is not named; a strict mapping fills only the " +
				"fields //kanna:map or a tag names\n  add Name to the //kanna:map of ToPublic, or Name=- to leave it zero",
		},
		{
			name:    "a field named by the source's tag",
			methods: "\t//kanna:map ID\n\tToMailed(Tagged) Mailed\n",
		},
		{
			name:    "a pair of structs it would write a function for",
			methods: "\t//kanna:map ID Home\n\tToHomed(User) Homed\n",
			want: "test.go:30:2: error: ToHomed: Home: a strict mapping calls no function written for test.Address " +
				"to test.PublicAddress, whose fields it would fill unnamed\n" +
				"  declare AddressToPublicAddress(test.Address) test.PublicAddress in the interface, and name its fields",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := "package test\n\n" + types +
				"type Tagged struct {\n\tID string\n\tEmail string `map:\"Address\"`\n}\n\n" +
				"//kanna:mapper strict\ntype _ interface {\n" + tt.methods + "}\n"
			_, diags := gen.Plans(pkgtest.Load(t, map[string]string{"test.go": src}))
			if tt.want == "" {
				if len(diags) > 0 {
					t.Errorf("diagnostics = %q, want none", diags)
				}
				return
			}
			if !slices.ContainsFunc(diags, func(d string) bool { return strings.Contains(d, tt.want) }) {
				t.Errorf("diagnostics = %q, want one containing %q", diags, tt.want)
			}
		})
	}
}

// A strict mapping calls no function written for a pair of structs, whichever
// mapping wrote it, so that another mapping of the pair, declared before it or
// after it, does not change what it fills. A function declared for the pair, or
// a conversion listed for it, it calls.
func TestPlans_StrictWrittenFunctions(t *testing.T) {
	t.Parallel()

	const types = "type ChildIn struct{ Name, Secret string }\n" +
		"type ChildOut struct{ Name, Secret string }\n" +
		"type In struct{ Child ChildIn }\n" +
		"type Out struct{ Child ChildOut }\n" +
		"type InSlice struct{ Child []ChildIn }\n" +
		"type OutSlice struct{ Child []ChildOut }\n" +
		"type InMap struct{ Child map[string]ChildIn }\n" +
		"type OutMap struct{ Child map[string]ChildOut }\n" +
		"type InPointer struct{ Child *ChildIn }\n" +
		"type OutPointer struct{ Child *ChildOut }\n" +
		"func convertChild(ChildIn) ChildOut { return ChildOut{} }\n" +
		"type Status string\n" +
		"const StatusOn Status = \"on\"\n" +
		"type WireStatus int32\n" +
		"const (\n\tWireStatus_WIRE_STATUS_UNSPECIFIED WireStatus = 0\n\tWireStatus_WIRE_STATUS_ON WireStatus = 1\n)\n" +
		"func (x WireStatus) Enum() *WireStatus { return &x }\n" +
		"type InEnum struct{ Status Status }\n" +
		"type OutEnum struct{ Status WireStatus }\n\n"
	const (
		loose  = "//kanna:mapper\ntype _ interface{ Loose(In) Out }\n"
		strict = "//kanna:mapper strict\ntype _ interface {\n\t//kanna:map Child\n\tStrict(In) Out\n}\n"
		refuse = "Strict: Child: a strict mapping calls no function written for test.ChildIn to test.ChildOut, " +
			"whose fields it would fill unnamed\n" +
			"  declare ChildInToChildOut(test.ChildIn) test.ChildOut in the interface, and name its fields"
	)

	tests := []struct {
		name, decls, want string
	}{
		{name: "written for a mapping before it", decls: loose + strict, want: refuse},
		{name: "written for a mapping after it", decls: strict + loose, want: refuse},
		{
			name: "in a slice",
			decls: loose + "//kanna:mapper strict\ntype _ interface {\n\t//kanna:map Child\n" +
				"\tStrict(InSlice) OutSlice\n}\n",
			want: refuse,
		},
		{
			name: "in a map",
			decls: loose + "//kanna:mapper strict\ntype _ interface {\n\t//kanna:map Child\n" +
				"\tStrict(InMap) OutMap\n}\n",
			want: refuse,
		},
		{
			name: "through pointers",
			decls: loose + "//kanna:mapper strict\ntype _ interface {\n\t//kanna:map Child\n" +
				"\tStrict(InPointer) OutPointer\n}\n",
			want: refuse,
		},
		{
			name: "declared by the strict interface",
			decls: loose + "//kanna:mapper strict\ntype _ interface {\n\t//kanna:map Child\n\tStrict(In) Out\n" +
				"\t//kanna:map Name Secret=-\n\tChildInToChildOut(ChildIn) ChildOut\n}\n",
		},
		{
			name:  "declared by another interface",
			decls: "//kanna:mapper\ntype _ interface{ ChildInToChildOut(ChildIn) ChildOut }\n" + strict,
		},
		{
			name:  "a listed conversion",
			decls: "//kanna:converter\nvar _ = []any{convertChild}\n\n" + loose + strict,
		},
		{
			name: "an enum's, which has no fields",
			decls: "//kanna:mapper\ntype _ interface{ Loose(InEnum) OutEnum }\n" +
				"//kanna:mapper strict\ntype _ interface {\n\t//kanna:map Status\n\tStrict(InEnum) OutEnum\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := "package test\n\n" + types + tt.decls
			_, diags := gen.Plans(pkgtest.Load(t, map[string]string{"test.go": src}))
			if tt.want == "" {
				if len(diags) > 0 {
					t.Errorf("diagnostics = %q, want none", diags)
				}
				return
			}
			if !slices.ContainsFunc(diags, func(d string) bool { return strings.Contains(d, tt.want) }) {
				t.Errorf("diagnostics = %q, want one containing %q", diags, tt.want)
			}
		})
	}
}
