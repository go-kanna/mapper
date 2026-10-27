package gen_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/pkgtest"

	"github.com/go-kanna/mapper/internal/gen"
)

// protoOneof is a oneof as protoc-gen-go writes it, under ChangePB.Kind.
const protoOneof = `
type ChangePB struct {
	Version int64
	Kind    isChangePB_Kind
}

func (x *ChangePB) GetKind() isChangePB_Kind {
	if x != nil {
		return x.Kind
	}
	return nil
}

type isChangePB_Kind interface{ isChangePB_Kind() }

type ChangePB_Membership struct{ Membership *MembershipPB }

type ChangePB_Category struct{ Category *CategoryPB }

type ChangePB_Note struct{ Note string }

func (*ChangePB_Membership) isChangePB_Kind() {}
func (*ChangePB_Category) isChangePB_Kind()   {}
func (*ChangePB_Note) isChangePB_Kind()       {}

type MembershipPB struct{ Role string }

type CategoryPB struct{ Name string }
`

func TestPlans_Oneof(t *testing.T) {
	t.Parallel()

	found, diags := gen.Plans(pkgtest.Load(t, map[string]string{"oneof.go": `package test

// Change is the domain side: at most one of the fields after Version is set.
type Change struct {
	Version    int64
	Membership *Membership
	Category   *Category
	Note       *string
}

type Membership struct{ Role string }

type Category struct{ Name string }

//kanna:mapper
type _ interface {
	ToPB(Change) *ChangePB
	FromPB(*ChangePB) Change
}
` + protoOneof}))

	want := []string{
		// Going out, each arm is set from the field of its name, converted
		// through what the pointer points to.
		"ToPB(src test.Change) *test.ChangePB\n" +
			"  Version = .Version direct\n" +
			"  Kind = oneof\n" +
			"    ChangePB_Membership.Membership = *.Membership call:membershipToMembershipPB\n" +
			"    ChangePB_Category.Category = *.Category call:categoryToCategoryPB\n" +
			"    ChangePB_Note.Note = *.Note direct",
		// Coming back, the arm that is there fills its field.
		"FromPB(src *test.ChangePB) test.Change\n" +
			"  Version = .Version direct\n" +
			"  oneof .GetKind()\n" +
			"    Membership = ChangePB_Membership.Membership call:membershipPBToMembership\n" +
			"    Category = ChangePB_Category.Category call:categoryPBToCategory\n" +
			"    Note = ChangePB_Note.Note addr(direct)",
		"membershipToMembershipPB(src test.Membership) *test.MembershipPB\n  Role = .Role direct",
		"categoryToCategoryPB(src test.Category) *test.CategoryPB\n  Name = .Name direct",
		"membershipPBToMembership(src *test.MembershipPB) *test.Membership\n  Role = .Role direct",
		"categoryPBToCategory(src *test.CategoryPB) *test.Category\n  Name = .Name direct",
	}
	if !slices.Equal(found, want) {
		t.Errorf("plans =\n%s\nwant\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
	}
	if len(diags) > 0 {
		t.Errorf("diagnostics = %q, want none", diags)
	}
}

func TestMatches_Oneof(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string // the fields of Change, a line each
		entry  string // a //kanna:map line above ToPB, if any
		want   string
	}{
		{
			name:   "an arm left out",
			domain: "Version int64\nMembership *MembershipPB\nCategory *CategoryPB",
			entry:  "Note=-",
			want: "ToPB: Version <- Version, " +
				"Kind <- [ChangePB_Membership.Membership <- Membership, ChangePB_Category.Category <- Category]",
		},
		{
			name:   "an arm set from another field",
			domain: "Version int64\nMembership *MembershipPB\nGroup *CategoryPB\nNote *string",
			entry:  "Category=Group",
			want: "ToPB: Version <- Version, Kind <- [ChangePB_Membership.Membership <- Membership, " +
				"ChangePB_Category.Category <- Group, ChangePB_Note.Note <- Note]",
		},
		{
			name:   "the oneof left out",
			domain: "Version int64",
			entry:  "Kind=-",
			want:   "ToPB: Version <- Version",
		},
		{
			// The interface is copied, not taken apart.
			name:   "a source carrying the oneof itself",
			domain: "Version int64\nKind isChangePB_Kind",
			want:   "ToPB: Version <- Version, Kind <- Kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method := "\tToPB(Change) *ChangePB\n"
			if tt.entry != "" {
				method = "\t//kanna:map " + tt.entry + "\n" + method
			}
			src := "package test\n\ntype Change struct {\n" + tt.domain + "\n}\n\n//kanna:mapper\ntype _ interface {\n" +
				method + "}\n" + protoOneof
			found, diags := gen.Matches(pkgtest.Load(t, map[string]string{"test.go": src}))
			if !slices.Equal(found, []string{tt.want}) {
				t.Errorf("matches = %q, want %q", found, tt.want)
			}
			if len(diags) > 0 {
				t.Errorf("diagnostics = %q, want none", diags)
			}
		})
	}
}

func TestPlans_OneofDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string // after the package clause, before protoOneof
		want string
	}{
		{
			name: "an arm with no source",
			src: "type Change struct {\n\tMembership *MembershipPB\n\tCategory *CategoryPB\n}\n\n" +
				"//kanna:mapper\ntype _ interface {\n\tToPB(Change) *ChangePB\n}\n",
			want: "test.go:10:2: error: ToPB: no source in test.Change for " +
				"test.ChangePB_Note.Note (arm of test.ChangePB.Kind)\n" +
				"  add map:\"Note\" to the field of test.Change that fills it, or leave it zero with //kanna:map Note=-",
		},
		{
			name: "an arm from a value",
			src: "type Change struct {\n\tNote string\n}\n\n//kanna:mapper\ntype _ interface {\n" +
				"\t//kanna:map Membership=- Category=-\n\tToPB(Change) *ChangePB\n}\n",
			want: "test.go:10:2: error: ToPB: cannot set test.ChangePB_Note.Note (arm of test.ChangePB.Kind) " +
				"from test.Change.Note (string): an arm is set only when its source is, which takes a pointer, slice, or map",
		},
		{
			name: "an arm nothing converts",
			src: "type Change struct {\n\tNote *int\n}\n\n//kanna:mapper\ntype _ interface {\n" +
				"\t//kanna:map Membership=- Category=-\n\tToPB(Change) *ChangePB\n}\n",
			want: "test.go:10:2: error: ToPB: cannot map test.Change.Note (*int) to test.ChangePB_Note.Note " +
				"(arm of test.ChangePB.Kind) (string)",
		},
		{
			name: "an arm of the source nothing converts",
			src: "type Change struct {\n\tNote int\n}\n\n//kanna:mapper\ntype _ interface {\n" +
				"\tFromPB(*ChangePB) Change\n}\n",
			want: "test.go:9:2: error: FromPB: cannot map test.ChangePB_Note.Note (arm of test.ChangePB.Kind) (string) " +
				"to test.Change.Note (int)",
		},
		{
			name: "an arm from the arm of another oneof",
			src: "type Other struct{ Kind isOther_Kind }\n\ntype isOther_Kind interface{ isOther_Kind() }\n\n" +
				"type Other_Note struct{ Note *string }\n\nfunc (*Other_Note) isOther_Kind() {}\n\n" +
				"//kanna:mapper\ntype _ interface {\n\t//kanna:map Membership=- Category=-\n\tToPB(Other) *ChangePB\n}\n",
			want: "test.go:14:2: error: ToPB: cannot set test.ChangePB_Note.Note (arm of test.ChangePB.Kind) from " +
				"test.Other_Note.Note (arm of test.Other.Kind), an arm of another oneof",
		},
		{
			name: "an arm entry naming no arm",
			src: "type Change struct {\n\tMembership *MembershipPB\n\tCategory *CategoryPB\n\tNote *string\n}\n\n" +
				"//kanna:mapper\ntype _ interface {\n\t//kanna:map Nte=-\n\tToPB(Change) *ChangePB\n}\n",
			want: "test.go:11:2: error: //kanna:map Nte=-: test.ChangePB has no field Nte\n  did you mean \"Note\"?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, diags := gen.Plans(pkgtest.Load(t, map[string]string{"test.go": "package test\n\n" + tt.src + protoOneof}))
			if !slices.ContainsFunc(diags, func(d string) bool { return strings.Contains(d, tt.want) }) {
				t.Errorf("diagnostics = %q, want one containing %q", diags, tt.want)
			}
		})
	}
}
