package gen_test

import (
	"slices"
	"testing"

	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/pkgtest"

	"github.com/go-kanna/mapper/internal/gen"
)

func convertPackage(t *testing.T) *packages.Package {
	t.Helper()

	return pkgtest.Load(t, map[string]string{"convert.go": `package test

import (
	"net/netip"
	"strconv"
)

type (
	Email  string
	Status int32
	Code   int32
)

type Address struct{ City string }

type AddressPB struct{ City string }

type Hidden struct{ city string }

//kanna:mapper
type _ interface {
	AddressToPB(Address) *AddressPB
}

//kanna:converter
var _ = []any{
	netip.ParseAddr,
	netip.Addr.String,
	strconv.Itoa,
	lengthOf,
}

func lengthOf(s *string) int { return len(*s) }
`})
}

func TestOps(t *testing.T) {
	t.Parallel()

	pkg := convertPackage(t)

	tests := []struct {
		src, dst string
		want     string
		written  []string
	}{
		{src: "string", dst: "string", want: "direct"},

		// What a list names comes before anything Go could do.
		{src: "netip.Addr", dst: "string", want: "conv:netip.Addr.String"},
		{src: "string", dst: "netip.Addr", want: "conv:netip.ParseAddr (fallible)"},
		{src: "int", dst: "string", want: "conv:strconv.Itoa"},

		// nil stays nil whenever the destination can hold it.
		{src: "*netip.Addr", dst: "string", want: "deref(conv:netip.Addr.String)"},
		{src: "netip.Addr", dst: "*string", want: "addr(conv:netip.Addr.String)"},
		{src: "*netip.Addr", dst: "*string", want: "deref(addr(conv:netip.Addr.String))"},
		{src: "*string", dst: "*int", want: "guard(addr(conv:lengthOf))"},

		{src: "[]netip.Addr", dst: "[]string", want: "slice(conv:netip.Addr.String)"},
		{src: "map[string]netip.Addr", dst: "map[string]string", want: "map(conv:netip.Addr.String)"},
		{src: "map[int]string", dst: "map[string]string", want: "none"},

		// Lossless on every platform, or not at all.
		{src: "int32", dst: "int64", want: "cast:int64"},
		{src: "int64", dst: "int32", want: "none"},
		{src: "int", dst: "int32", want: "none"},
		{src: "int32", dst: "int", want: "cast:int"},
		{src: "int", dst: "int64", want: "cast:int64"},
		{src: "int", dst: "uint", want: "none"},
		{src: "uint8", dst: "int16", want: "cast:int16"},
		{src: "uint16", dst: "uint32", want: "cast:uint32"},
		{src: "uint", dst: "uint64", want: "cast:uint64"},
		{src: "uint64", dst: "uint", want: "none"},
		{src: "uint32", dst: "int32", want: "none"},
		{src: "int8", dst: "uint8", want: "none"},
		{src: "uintptr", dst: "uint64", want: "none"},
		{src: "int32", dst: "float64", want: "cast:float64"},
		{src: "int64", dst: "float64", want: "none"},
		{src: "int16", dst: "float32", want: "cast:float32"},
		{src: "int32", dst: "float32", want: "none"},
		{src: "float32", dst: "float64", want: "cast:float64"},
		{src: "float64", dst: "float32", want: "none"},
		{src: "bool", dst: "string", want: "none"},
		{src: "int64", dst: "string", want: "none"},
		{src: "string", dst: "[]byte", want: "cast:[]byte"},
		{src: "[]byte", dst: "string", want: "cast:string"},

		// A declared type converts to and from a predeclared one, but not into
		// another declared type.
		{src: "Email", dst: "string", want: "cast:string"},
		{src: "string", dst: "Email", want: "cast:test.Email"},
		{src: "Status", dst: "int32", want: "cast:int32"},
		{src: "int32", dst: "Status", want: "cast:test.Status"},
		{src: "Status", dst: "Code", want: "none"},

		{src: "Address", dst: "*AddressPB", want: "call:AddressToPB"},
		{src: "*Address", dst: "*AddressPB", want: "deref(call:AddressToPB)"},
		{src: "[]Address", dst: "[]*AddressPB", want: "slice(call:AddressToPB)"},

		// A pair nobody declared is written as it appears, a pointer staying
		// a pointer.
		{
			src: "AddressPB", dst: "Address",
			want:    "call:write(test.AddressPB -> test.Address)",
			written: []string{"test.AddressPB -> test.Address"},
		},
		{
			src: "*AddressPB", dst: "Address",
			want:    "call:write(*test.AddressPB -> test.Address)",
			written: []string{"*test.AddressPB -> test.Address"},
		},
		{
			src: "[]*AddressPB", dst: "[]Address",
			want:    "slice(call:write(*test.AddressPB -> test.Address))",
			written: []string{"*test.AddressPB -> test.Address"},
		},
		{src: "Address", dst: "Hidden", want: "none"},
		{src: "Hidden", dst: "Address", want: "none"},
	}

	for _, tt := range tests {
		t.Run(tt.src+" to "+tt.dst, func(t *testing.T) {
			t.Parallel()

			ops, written := gen.Ops(pkg, [2]string{tt.src, tt.dst})
			if ops[0] != tt.want {
				t.Errorf("op = %s, want %s", ops[0], tt.want)
			}
			if !slices.Equal(written, tt.written) {
				t.Errorf("written = %q, want %q", written, tt.written)
			}
		})
	}
}

// A function written for one pair serves the same pair through pointers and
// slices afterwards.
func TestOps_ReuseWrittenFunctions(t *testing.T) {
	t.Parallel()

	ops, written := gen.Ops(convertPackage(t),
		[2]string{"AddressPB", "Address"},
		[2]string{"*AddressPB", "*Address"},
		[2]string{"[]AddressPB", "[]Address"},
	)

	want := []string{
		"call:write(test.AddressPB -> test.Address)",
		"deref(addr(call:write(test.AddressPB -> test.Address)))",
		"slice(call:write(test.AddressPB -> test.Address))",
	}
	if !slices.Equal(ops, want) {
		t.Errorf("ops = %q, want %q", ops, want)
	}
	if want := []string{"test.AddressPB -> test.Address"}; !slices.Equal(written, want) {
		t.Errorf("written = %q, want %q", written, want)
	}
}

// An enum of kanna-value maps to and from an enum of protobuf through a
// function written for the pair, and is made from a string with its
// constructor.
func TestOps_Enums(t *testing.T) {
	t.Parallel()

	pkg := pkgtest.Load(t, map[string]string{"enum.go": `package test

type Status string

const StatusActive Status = "active"

func NewStatus(v string) (Status, error) { return Status(v), nil }

type Plain string

const PlainA Plain = "a"

type StatusPB int32

const StatusPB_STATUS_ACTIVE StatusPB = 1

func (x StatusPB) Enum() *StatusPB { return &x }

type Number int32

const NumberOne Number = 1
`})

	tests := []struct {
		src, dst string
		want     string
		written  []string
	}{
		{src: "Status", dst: "StatusPB", want: "call:write(test.Status -> test.StatusPB)",
			written: []string{"test.Status -> test.StatusPB"}},
		{src: "StatusPB", dst: "Status", want: "call:write(test.StatusPB -> test.Status)",
			written: []string{"test.StatusPB -> test.Status"}},
		{src: "*StatusPB", dst: "*Status", want: "deref(addr(call:write(test.StatusPB -> test.Status)))",
			written: []string{"test.StatusPB -> test.Status"}},
		{src: "Plain", dst: "StatusPB", want: "call:write(test.Plain -> test.StatusPB)",
			written: []string{"test.Plain -> test.StatusPB"}},

		{src: "string", dst: "Status", want: "new:test.NewStatus(direct) (fallible)"},
		{src: "*string", dst: "*Status", want: "deref(addr(new:test.NewStatus(direct))) (fallible)"},
		{src: "Status", dst: "string", want: "cast:string"},

		// Without a constructor, a string is converted as it is.
		{src: "string", dst: "Plain", want: "cast:test.Plain"},
		// An int32 without Enum is no enum of protobuf.
		{src: "Status", dst: "Number", want: "none"},
		{src: "StatusPB", dst: "int32", want: "cast:int32"},
	}

	for _, tt := range tests {
		t.Run(tt.src+" to "+tt.dst, func(t *testing.T) {
			t.Parallel()

			ops, written := gen.Ops(pkg, [2]string{tt.src, tt.dst})
			if ops[0] != tt.want {
				t.Errorf("op = %s, want %s", ops[0], tt.want)
			}
			if !slices.Equal(written, tt.written) {
				t.Errorf("written = %q, want %q", written, tt.written)
			}
		})
	}
}

// A value type of kanna-value is read through its accessor and made with its
// constructor, what it holds converted as any other value.
func TestOps_ValueTypes(t *testing.T) {
	t.Parallel()

	pkg := loadAll(t, "./testdata/checked").Packages[0]

	tests := []struct{ src, dst, want string }{
		{src: "CompletedAt", dst: "time.Time", want: "get:Time(direct)"},
		{src: "time.Time", dst: "CompletedAt", want: "new:checked.NewCompletedAt(direct) (fallible)"},

		// nil stays nil, through the listed conversions.
		{src: "*CompletedAt", dst: "*Timestamp", want: "deref(get:Time(conv:NewTimestamp))"},
		{
			src: "*Timestamp", dst: "*CompletedAt",
			want: "guard(addr(new:checked.NewCompletedAt(conv:(*Timestamp).AsTime))) (fallible)",
		},
		{src: "*CompletedAt", dst: "*time.Time", want: "deref(addr(get:Time(direct)))"},
		{src: "*time.Time", dst: "*CompletedAt", want: "deref(addr(new:checked.NewCompletedAt(direct))) (fallible)"},

		// A value already checked is not checked again.
		{src: "CompletedAt", dst: "*CompletedAt", want: "addr(direct)"},
		{src: "Email", dst: "Website", want: "get:String(new:checked.NewWebsite(direct)) (fallible)"},

		{src: "int32", dst: "Count", want: "new:checked.newCount(cast:int64) (fallible)"},
		{src: "Count", dst: "int32", want: "none"},
		{src: "[]Count", dst: "[]int64", want: "slice(get:Int64(direct))"},

		// A listed conversion for the pair comes first.
		{src: "string", dst: "Email", want: "conv:parseEmail (fallible)"},

		// Another package's unexported constructor cannot be called here.
		{src: "string", dst: "private.Token", want: "none"},
		{src: "private.Token", dst: "string", want: "get:String(direct)"},

		// The shape of a value type, without what kanna-value would write.
		{src: "Odd", dst: "int", want: "get:Int(direct)"},
		{src: "int", dst: "Odd", want: "none"},
		{src: "Pair", dst: "[]string", want: "none"},
	}

	for _, tt := range tests {
		t.Run(tt.src+" to "+tt.dst, func(t *testing.T) {
			t.Parallel()

			if ops, _ := gen.Ops(pkg, [2]string{tt.src, tt.dst}); ops[0] != tt.want {
				t.Errorf("op = %s, want %s", ops[0], tt.want)
			}
		})
	}
}
