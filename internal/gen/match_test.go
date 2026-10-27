package gen_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/pkgtest"

	"github.com/go-kanna/mapper/internal/gen"
)

func TestMatches(t *testing.T) {
	t.Parallel()

	found, diags := gen.Matches(pkgtest.Load(t, map[string]string{"match.go": `package test

type Base struct {
	Created string
	Secret  string ` + "`map:\"-\"`" + `
}

type Employee struct {
	Base
	ID       string
	FullName string ` + "`map:\"Name\"`" + `
	Nick     string
	Internal string ` + "`map:\"-\"`" + `
	hidden   string
}

type Wire struct {
	Id       string
	Name     string
	Alias    string ` + "`map:\"Nick\"`" + `
	Created  string
	Revision int32
	Skip     string ` + "`map:\"-\"`" + `
	private  int
}

type Card struct {
	Title, ID, Name string
}

//kanna:mapper
type _ interface {
	//kanna:map Revision=- Alias=Nick Skip=-
	ToWire(Employee) Wire

	//kanna:map Title=Created
	ToCard(Employee) Card

	// The entries take the place of the tags, in this function only.
	//
	//kanna:map Revision=- Alias=ID Skip=Nick Name=Nick
	ToOther(Employee) Wire
}
`}))

	want := []string{
		// Id by case, Name by the source's tag, Alias by its own tag and an
		// entry agreeing with it, Created promoted from Base; Revision left
		// out by the entry, and Skip by its tag and an entry.
		"ToWire: Id <- ID, Name <- FullName, Alias <- Nick, Created <- Created",
		"ToCard: Title <- Created, ID <- ID, Name <- FullName",
		"ToOther: Id <- ID, Name <- Nick, Alias <- ID, Created <- Created, Skip <- Nick",
	}
	if !slices.Equal(found, want) {
		t.Errorf("matches =\n%s\nwant\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
	}
	if len(diags) > 0 {
		t.Errorf("diagnostics = %q, want none", diags)
	}
}

func TestMatches_Diagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		types string // one declaration a line, from line 3
		entry string // a //kanna:map line above F, if any
		want  string
	}{
		{
			name:  "no source",
			types: "type S struct{ A string }\ntype D struct{ A, B string }",
			want: "test.go:8:2: error: F: no source in test.S for test.D.B\n" +
				"  add map:\"B\" to the field of test.S that fills it, or leave it zero with //kanna:map B=-",
		},
		{
			name:  "an embedded destination field",
			types: "type S struct{ A, B string }\ntype Inner struct{ B string }\ntype D struct{ A string; Inner }",
			want: "test.go:9:2: error: F: no source in test.S for test.D.Inner\n" +
				"  an embedded field is matched as one field, by its type's name; its fields are not",
		},
		{
			name:  "a destination tag naming nothing",
			types: "type S struct{ Name string }\ntype D struct{ Title string `map:\"Nme\"` }",
			want:  "test.go:4:16: error: map tag \"Nme\" on test.D.Title names no field of test.S\n  did you mean \"Name\"?",
		},
		{
			// A tag may name a field only another destination has, so it is no
			// error by itself; it is pointed out where it seems to be meant.
			name:  "a source tag nearly naming the field without a source",
			types: "type S struct{ X string; A string `map:\"Titl\"` }\ntype D struct{ X, Title string }",
			want: "test.go:8:2: error: F: no source in test.S for test.D.Title\n" +
				"  test.S.A is tagged map:\"Titl\"; did you mean map:\"Title\"?",
		},
		{
			name:  "two source fields tagged alike",
			types: "type S struct{ A string `map:\"X\"`; B string `map:\"X\"` }\ntype D struct{ X string }",
			want:  "test.go:3:36: error: test.S.A and test.S.B are both tagged map:\"X\"",
		},
		{
			name:  "tags that disagree",
			types: "type S struct{ A string `map:\"X\"`; B string }\ntype D struct{ X string `map:\"B\"` }",
			want:  "test.go:3:16: error: test.S.A is tagged map:\"X\", but test.D.X reads B",
		},
		{
			name:  "names alike in case",
			types: "type S struct{ IDent, IdEnt string }\ntype D struct{ Ident string }",
			want: "test.go:8:2: error: F: test.D.Ident could read test.S.IDent or test.S.IdEnt\n" +
				"  name the one with //kanna:map Ident=<field> or a map tag",
		},
		{
			name:  "an invalid tag",
			types: "type S struct{ X string }\ntype D struct{ X string `map:\"9x\"` }",
			want:  "test.go:4:16: error: invalid map tag \"9x\"",
		},
		{
			name: "a promoted field tagged out",
			types: "type Base struct{ Secret string `map:\"-\"` }\n" +
				"type S struct{ Base; Name string }\n" +
				"type D struct{ Secret, Name string }",
			want: "test.go:9:2: error: F: no source in test.S for test.D.Secret",
		},
		{
			// Reading it would panic when the pointer is nil.
			name:  "a field promoted through a pointer",
			types: "type Base struct{ Created string }\ntype S struct{ *Base }\ntype D struct{ Created string }",
			want:  "test.go:9:2: error: F: no source in test.S for test.D.Created",
		},
		{
			name:  "an entry naming no destination field",
			types: "type S struct{ A string }\ntype D struct{ A string; Revision int }",
			entry: "Revison=-",
			want:  "test.go:8:2: error: //kanna:map Revison=-: test.D has no field Revison\n  did you mean \"Revision\"?",
		},
		{
			name:  "an entry naming no source field",
			types: "type S struct{ FullName string }\ntype D struct{ Name string }",
			entry: "Name=FulName",
			want:  "test.go:8:2: error: //kanna:map Name=FulName: test.S has no field FulName\n  did you mean \"FullName\"?",
		},
		{
			name:  "an entry naming a source field tagged out",
			types: "type S struct{ A string; Secret string `map:\"-\"` }\ntype D struct{ A, X string }",
			entry: "X=Secret",
			want:  "test.go:8:2: error: //kanna:map X=Secret: test.S.Secret is tagged map:\"-\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method := "\tF(S) D\n"
			if tt.entry != "" {
				method = "\t//kanna:map " + tt.entry + "\n" + method
			}
			src := "package test\n\n" + tt.types + "\n\n//kanna:mapper\ntype _ interface {\n" + method + "}\n"
			_, diags := gen.Matches(pkgtest.Load(t, map[string]string{"test.go": src}))
			if !slices.ContainsFunc(diags, func(d string) bool { return strings.Contains(d, tt.want) }) {
				t.Errorf("diagnostics = %q, want one containing %q", diags, tt.want)
			}
		})
	}
}

// A function written for a pair nobody declared has no //kanna:map line to
// point to, so the hints say to declare it.
func TestMatchWritten(t *testing.T) {
	t.Parallel()

	pkg := pkgtest.Load(t, map[string]string{"test.go": `package test

type Address struct{ City, CITY string }

type AddressPB struct{ City, Etag string }

type Folded struct{ CiTy string }

type Tagged struct{ City string ` + "`map:\"Town\"`" + ` }
`})

	tests := []struct {
		dst  string
		want string
	}{
		{
			dst: "AddressPB",
			want: "error: Owner: Address: no source in test.Address for test.AddressPB.Etag\n" +
				"  declare AddressToPB(test.Address) test.AddressPB in the //kanna:mapper interface and add //kanna:map Etag=-",
		},
		{
			dst: "Tagged",
			want: "test.go:9:21: error: map tag \"Town\" on test.Tagged.City names no field of test.Address\n" +
				"  or declare AddressToTagged(test.Address) test.Tagged in the //kanna:mapper interface" +
				" and add //kanna:map City=<field>",
		},
		{
			dst: "Folded",
			want: "error: Owner: Address: test.Folded.CiTy could read test.Address.City or test.Address.CITY\n" +
				"  declare AddressToFolded(test.Address) test.Folded in the //kanna:mapper interface" +
				" and add //kanna:map CiTy=<field>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.dst, func(t *testing.T) {
			t.Parallel()

			suggest := "AddressTo" + strings.TrimPrefix(tt.dst, "Address") + "(test.Address) test." + tt.dst
			_, diags := gen.MatchWritten(pkg, "Address", tt.dst, "Owner: Address", suggest)
			if !slices.Equal(diags, []string{tt.want}) {
				t.Errorf("diagnostics = %q, want %q", diags, []string{tt.want})
			}
		})
	}
}
