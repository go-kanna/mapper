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

//kanna:convert
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
