package gen_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/pkgtest"

	"github.com/go-kanna/mapper/internal/gen"
)

func TestConvs(t *testing.T) {
	t.Parallel()

	pkg := pkgtest.Load(t, map[string]string{"convert.go": `package test

import (
	"net/netip"
	"strconv"
	"time"
)

//kanna:converter
var _ = []any{
	netip.ParseAddr,
	netip.Addr.String,
	(*time.Location).String,
	formatCount,
	common,
	common, // read once
}

// A list of the package applies whether another names it or not.
//
//kanna:converter
var common = []any{
	strconv.Atoi,
}

func formatCount(n int64) string { return strconv.FormatInt(n, 10) }

// No list names it, so it is only pointed out.
func parseCount(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

func sum(a, b int) int { return a + b }
`})

	got := gen.Convs(pkg, nil)

	want := []string{
		"netip.ParseAddr: string -> netip.Addr (fallible)",
		"netip.Addr.String: netip.Addr -> string [method]",
		"(*time.Location).String: *time.Location -> string [method]",
		"formatCount: int64 -> string",
		"strconv.Atoi: string -> int (fallible)",
	}
	if !slices.Equal(got.ByPair, want) {
		t.Errorf("conversions =\n%s\nwant\n%s", strings.Join(got.ByPair, "\n"), strings.Join(want, "\n"))
	}
	if want := []string{"parseCount: string -> int64 (fallible)"}; !slices.Equal(got.Unlisted, want) {
		t.Errorf("unlisted = %q, want %q", got.Unlisted, want)
	}
	if len(got.Missing) > 0 || len(got.Diags) > 0 {
		t.Errorf("missing = %q, diagnostics = %q, want none", got.Missing, got.Diags)
	}
}

func TestConvs_Shared(t *testing.T) {
	t.Parallel()

	const sharedPath = "github.com/go-kanna/mapper/internal/gen/testdata/convert/shared"

	// Loaded on its own, the package that names shared.Defaults sees shared
	// only as an import, without the syntax its lists are read from.
	set := loadAll(t, "./testdata/convert/user")
	if got, want := gen.Convs(set.Packages[0], set.Packages).Missing, []string{sharedPath}; !slices.Equal(got, want) {
		t.Fatalf("missing = %q, want %q", got, want)
	}

	set = loadAll(t, "./testdata/convert/user", "./testdata/convert/shared")
	got := gen.Convs(find(t, set, "user"), set.Packages)
	want := []string{
		"itoa: int -> string",
		"netip.ParseAddr: string -> netip.Addr (fallible) [depth 1, shared.Defaults]",
		"netip.Addr.String: netip.Addr -> string [method] [depth 1, shared.Defaults]",
		"strconv.Atoi: string -> int (fallible) [depth 2, shared.More]",
	}
	if !slices.Equal(got.ByPair, want) {
		t.Errorf("conversions =\n%s\nwant\n%s", strings.Join(got.ByPair, "\n"), strings.Join(want, "\n"))
	}
	if len(got.Missing) > 0 || len(got.Diags) > 0 {
		t.Errorf("missing = %q, diagnostics = %q, want none", got.Missing, got.Diags)
	}
}

func TestConvs_SharedDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pkg  string
		want string
	}{
		{
			// Reported where the shared list names it.
			pkg:  "hidden",
			want: "shared.go:42:2: error: parse is not exported, so package hidden cannot call it",
		},
		{
			pkg: "clash",
			want: "shared.go:32:2: error: strconv.Itoa and shared.Hash both convert int to string\n" +
				"  strconv.Itoa is listed in shared.Defaults, at shared.go:15:2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.pkg, func(t *testing.T) {
			t.Parallel()

			set := loadAll(t, "./testdata/convert/"+tt.pkg, "./testdata/convert/shared")
			diags := gen.Convs(find(t, set, tt.pkg), set.Packages).Diags
			if !slices.EqualFunc(diags, []string{tt.want}, strings.HasSuffix) {
				t.Errorf("diagnostics = %q, want one ending in %q", diags, tt.want)
			}
		})
	}
}

func loadAll(t *testing.T, patterns ...string) packages.Set {
	t.Helper()

	set, err := packages.Load(patterns, packages.Config{TypesInfo: true})
	if err != nil || len(set.Diags) > 0 {
		t.Fatalf("load %q: %v %v", patterns, err, set.Diags)
	}
	return set
}

func find(t *testing.T, set packages.Set, name string) *packages.Package {
	t.Helper()

	i := slices.IndexFunc(set.Packages, func(p *packages.Package) bool { return p.Name == name })
	if i < 0 {
		t.Fatalf("package %s not loaded", name)
	}
	return set.Packages[i]
}

func TestConvs_Diagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "not a list",
			src:  "//kanna:converter\nvar list = []string{\"a\"}\n",
			want: "test.go:4:5: error: //kanna:converter marks a list: var list = []any{...}",
		},
		{
			name: "no value",
			src:  "//kanna:converter\nvar list []any\n",
			want: "test.go:4:5: error: //kanna:converter marks a list: var list = []any{...}",
		},
		{
			name: "arguments",
			src:  "//kanna:converter strict\nvar _ = []any{}\n",
			want: `test.go:3:1: error: //kanna:converter takes no arguments, got "strict"`,
		},
		{
			name: "misspelled",
			src:  "//kanna:convertr\nvar _ = []any{}\n",
			want: `did you mean "//kanna:converter"?`,
		},
		{
			name: "a function literal",
			src:  "//kanna:converter\nvar _ = []any{\n\tfunc(s string) int { return len(s) },\n}\n",
			want: "test.go:5:2: error: a function literal cannot be listed; give it a name",
		},
		{
			name: "a method value",
			src:  "import \"net/netip\"\n\nvar addr netip.Addr\n\n//kanna:converter\nvar _ = []any{\n\taddr.String,\n}\n",
			want: "test.go:9:2: error: addr.String is a method value; list the method expression netip.Addr.String instead",
		},
		{
			name: "a generic function",
			src:  "func same[T any](v T) T { return v }\n\n//kanna:converter\nvar _ = []any{\n\tsame[int],\n}\n",
			want: "test.go:7:2: error: same[int] is generic, which is not supported",
		},
		{
			name: "not a function",
			src:  "var limit = 3\n\n//kanna:converter\nvar _ = []any{\n\tlimit,\n}\n",
			want: "test.go:7:2: error: limit is not a function, a method expression, or a //kanna:converter list",
		},
		{
			name: "a constant",
			src:  "const limit = 3\n\n//kanna:converter\nvar _ = []any{\n\tlimit,\n}\n",
			want: "test.go:7:2: error: limit is not a function, a method expression, or a //kanna:converter list",
		},
		{
			name: "a field",
			src:  "var cfg struct{ Parse func(string) int }\n\n//kanna:converter\nvar _ = []any{\n\tcfg.Parse,\n}\n",
			want: "test.go:7:2: error: cfg.Parse is not a function, a method expression, or a //kanna:converter list",
		},
		{
			name: "a call",
			src:  "import \"strconv\"\n\n//kanna:converter\nvar _ = []any{\n\tstrconv.Itoa(1),\n}\n",
			want: "test.go:7:2: error: strconv.Itoa(1) is not a function, a method expression, or a //kanna:converter list",
		},
		{
			name: "a list without the directive",
			src:  "var plain = []any{}\n\n//kanna:converter\nvar _ = []any{\n\tplain,\n}\n",
			want: "test.go:7:2: error: plain is not a //kanna:converter list",
		},
		{
			name: "not a conversion",
			src:  "import \"strconv\"\n\n//kanna:converter\nvar _ = []any{\n\tstrconv.FormatInt,\n}\n",
			want: "test.go:7:2: error: strconv.FormatInt cannot convert: it is not func(A) B or func(A) (B, error)",
		},
		{
			name: "a second result that is not an error",
			src:  "import \"unicode/utf8\"\n\n//kanna:converter\nvar _ = []any{\n\tutf8.DecodeRuneInString,\n}\n",
			want: "test.go:7:2: error: utf8.DecodeRuneInString cannot convert: it is not func(A) B or func(A) (B, error)",
		},
		{
			name: "two conversions equally near",
			src: "import \"strconv\"\n\n//kanna:converter\nvar _ = []any{\n\tstrconv.Itoa,\n\titoa,\n}\n\n" +
				"func itoa(n int) string { return strconv.Itoa(n) }\n",
			want: "test.go:8:2: error: strconv.Itoa and itoa both convert int to string\n" +
				"  strconv.Itoa is listed at test.go:7:2",
		},
		{
			name: "two lists converting alike",
			src: "import \"strconv\"\n\n//kanna:converter\nvar _ = []any{strconv.Itoa}\n\n" +
				"//kanna:converter\nvar _ = []any{itoa}\n\nfunc itoa(n int) string { return strconv.Itoa(n) }\n",
			want: "test.go:9:15: error: strconv.Itoa and itoa both convert int to string\n" +
				"  strconv.Itoa is listed at test.go:6:15",
		},
		{
			name: "a pair a declared function maps",
			src: "type E struct{ N int }\n\ntype W struct{ N int }\n\n//kanna:mapper\ntype _ interface{ ToW(E) W }\n\n" +
				"func toW(e E) W { return W(e) }\n\n//kanna:converter\nvar _ = []any{\n\ttoW,\n}\n",
			want: "test.go:14:2: error: toW converts test.E to test.W, which ToW is declared to do",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			diags := gen.Convs(pkgtest.Load(t, map[string]string{"test.go": "package test\n\n" + tt.src}), nil).Diags
			if !slices.ContainsFunc(diags, func(d string) bool { return strings.Contains(d, tt.want) }) {
				t.Errorf("diagnostics = %q, want one containing %q", diags, tt.want)
			}
		})
	}
}
