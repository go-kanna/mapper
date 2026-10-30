package gen

import (
	"fmt"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/suggest"
)

// tagKey is the struct tag that renames a field or leaves it out: map:"Name",
// map:"-".
const tagKey = "map"

// site is where the problems of one function are reported: at the declaration
// that led to it, with the path from there.
type site struct {
	pos token.Position

	// path names the function from the declaration: "EmployeeToProto", or
	// "EmployeeToProto: Address" for one written for a field of it.
	path string

	// suggest is empty for a declared function, whose //kanna:map line the
	// hints point to. For a written one, it is the declaration to suggest
	// instead, such as "AddressToProto(model.Address) *employeev1.Address".
	suggest string
}

// fieldPair is a destination field and the source that fills it.
type fieldPair struct {
	dst *types.Var
	src sourceField
}

// sourceField is a field of the source struct that a destination field can be
// matched to, with the destination field its tag names, if any. The field of a
// oneof's arm is one too, under its own name: via is then the oneof field it
// is reached through.
type sourceField struct {
	v   *types.Var
	tag string
	via *types.Var
	arm oneofArm
}

// matched is what matchFields pairs.
type matched struct {
	pairs  []fieldPair
	oneofs []oneofMatch
}

// matching pairs the fields of one function.
type matching struct {
	fset     *token.FileSet
	at       site
	src, dst types.Type
	diags    []diag.Diag
}

// matchFields pairs each exported field of dst's struct with the source that
// fills it. A field left out, by map:"-" or by a //kanna:map entry, has no
// pair; a field without a source is an error. A protobuf oneof of the
// destination is matched arm by arm instead.
//
// Each destination field takes the first of: its entry, its own tag, a source
// field tagged with its name, the same name, the same name in another case,
// and a field promoted from an embedded struct under the same name. An entry
// is for this function only, so it takes the place of the tags.
func matchFields(fset *token.FileSet, at site, src, dst types.Type, entries []entry) (matched, []diag.Diag) {
	m := &matching{fset: fset, at: at, src: src, dst: dst}
	_, srcStruct, _ := namedStruct(src)
	_, dstStruct, _ := namedStruct(dst)

	sources := m.sourceFields(srcStruct)
	byField := m.entries(entries, sources, srcStruct, dstStruct)

	var out matched
	for i := range dstStruct.NumFields() {
		f := dstStruct.Field(i)
		if !f.Exported() {
			continue
		}
		tag, excluded := m.tag(dstStruct, i)
		e, hasEntry := byField[f.Name()]
		switch {
		case hasEntry && e.excluded(), !hasEntry && excluded:
			continue
		case hasEntry:
			out.pairs = append(out.pairs, fieldPair{dst: f, src: m.named(e.source, sources)})
			continue
		}

		// An interface the source carries too is copied as it is, rather than
		// taken apart as a oneof.
		if arms := oneofArms(f.Type()); len(arms) > 0 && !carriesType(sources, f.Type()) {
			if o, ok := m.dstOneof(f, arms, sources, byField, srcStruct); ok {
				out.oneofs = append(out.oneofs, o)
			}
			continue
		}
		label := structLabel(dst) + "." + f.Name()
		if sf, ok := m.source(f.Name(), label, f, tag, sources, srcStruct); ok {
			out.pairs = append(out.pairs, fieldPair{dst: f, src: sf})
		}
	}

	return out, m.diags
}

// sourceFields lists the exported fields of st that take part in mapping,
// followed by the arms of its oneofs, so that a field wins a name over an arm.
func (m *matching) sourceFields(st *types.Struct) []sourceField {
	var fields, arms []sourceField
	for i := range st.NumFields() {
		f := st.Field(i)
		if !f.Exported() {
			continue
		}
		tag, excluded := m.tag(st, i)
		if excluded {
			continue
		}
		fields = append(fields, sourceField{v: f, tag: tag})
		for _, arm := range oneofArms(f.Type()) {
			arms = append(arms, sourceField{v: arm.field, via: f, arm: arm})
		}
	}
	return append(fields, arms...)
}

// carriesType reports whether a field of sources, not an arm, is of type t.
func carriesType(sources []sourceField, t types.Type) bool {
	return slices.ContainsFunc(sources, func(sf sourceField) bool {
		return sf.via == nil && types.Identical(sf.v.Type(), t)
	})
}

// tag reads the map tag of field i of st: the field it names, or that it is
// map:"-". A tag naming no valid field is an error, and the field is then left
// out.
func (m *matching) tag(st *types.Struct, i int) (string, bool) {
	tag, ok := reflect.StructTag(st.Tag(i)).Lookup(tagKey)
	tag, opt, _ := strings.Cut(tag, ",")
	switch {
	case !ok:
		return "", false
	case opt != "" && opt != "share":
		m.diags = append(m.diags, diag.Errorf(m.pos(st.Field(i)), "invalid map tag %q: the only option is share",
			reflect.StructTag(st.Tag(i)).Get(tagKey)))
		return "", true
	case tag == "" && opt != "":
		return "", false
	case tag == "-":
		return "", true
	case !token.IsIdentifier(tag):
		m.diags = append(m.diags, diag.Errorf(m.pos(st.Field(i)), "invalid map tag %q", tag))
		return "", true
	}
	return tag, false
}

// entries indexes the //kanna:map entries by destination field, reporting the
// ones that name no field, or a source field left out of mapping.
func (m *matching) entries(entries []entry, sources []sourceField, srcStruct, dstStruct *types.Struct,
) map[string]entry {
	byField := make(map[string]entry, len(entries))
	for _, e := range entries {
		ok := true
		if dsts := destinationNames(dstStruct); !slices.Contains(dsts, e.field) {
			m.diags = append(m.diags, noSuchField(e, e.field, structLabel(m.dst), dsts))
			ok = false
		}
		switch {
		case e.excluded(), slices.ContainsFunc(sources, func(sf sourceField) bool { return sf.v.Name() == e.source }):
		case hasField(srcStruct, e.source):
			m.diags = append(m.diags, diag.Errorf(e.pos, "//kanna:%s %s=%s: %s.%s is tagged map:\"-\"",
				mapKey, e.field, e.source, structLabel(m.src), e.source))
			ok = false
		case !m.promoted(e.source):
			m.diags = append(m.diags, noSuchField(e, e.source, structLabel(m.src), exportedNames(srcStruct)))
			ok = false
		}
		if ok {
			byField[e.field] = e
		}
	}
	return byField
}

func noSuchField(e entry, name, owner string, names []string) diag.Diag {
	d := diag.Errorf(e.pos, "//kanna:%s %s=%s: %s has no field %s", mapKey, e.field, e.source, owner, name)
	if s, ok := suggest.Closest(name, names); ok {
		d = d.WithHints(fmt.Sprintf("did you mean %q?", s))
	}
	return d
}

// named returns the source an entry names, which entries has found to exist.
func (m *matching) named(name string, sources []sourceField) sourceField {
	if i := slices.IndexFunc(sources, func(sf sourceField) bool { return sf.v.Name() == name }); i >= 0 {
		return sources[i]
	}
	v, _ := m.promotedField(name)
	return sourceField{v: v}
}

// source finds the source for the destination field name, which label spells
// for messages; tagged is the field carrying its tag, which names tag or is "".
func (m *matching) source(name, label string, tagged *types.Var, tag string, sources []sourceField,
	srcStruct *types.Struct,
) (sourceField, bool) {
	var chosen *sourceField
	if tag != "" {
		if i := slices.IndexFunc(sources, func(sf sourceField) bool { return sf.v.Name() == tag }); i >= 0 {
			chosen = &sources[i]
		} else if v, ok := m.promotedField(tag); ok {
			chosen = &sourceField{v: v}
		} else {
			d := diag.Errorf(m.pos(tagged), "map tag %q on %s names no field of %s", tag, label, structLabel(m.src))
			if s, ok := suggest.Closest(tag, exportedNames(srcStruct)); ok {
				d = d.WithHints(fmt.Sprintf("did you mean %q?", s))
			}
			m.diags = append(m.diags, d.WithHints(m.overrideHint(name)))
			return sourceField{}, false
		}
	}

	var named []sourceField
	for _, sf := range sources {
		if sf.tag == name {
			named = append(named, sf)
		}
	}
	switch {
	case len(named) > 1:
		m.diags = append(m.diags, diag.Errorf(m.pos(named[1].v), "%s.%s and %s.%s are both tagged map:%q",
			structLabel(m.src), named[0].v.Name(), structLabel(m.src), named[1].v.Name(), name))
		return sourceField{}, false
	case len(named) == 1 && chosen != nil && chosen.v != named[0].v:
		m.diags = append(m.diags, diag.Errorf(m.pos(named[0].v), "%s.%s is tagged map:%q, but %s reads %s",
			structLabel(m.src), named[0].v.Name(), name, label, chosen.v.Name()))
		return sourceField{}, false
	case len(named) == 1:
		return named[0], true
	case chosen != nil:
		return *chosen, true
	}

	var folds []sourceField
	for _, sf := range sources {
		switch {
		case sf.tag != "":
		case sf.v.Name() == name:
			return sf, true
		case strings.EqualFold(sf.v.Name(), name):
			folds = append(folds, sf)
		}
	}
	switch len(folds) {
	case 0:
	case 1:
		return folds[0], true
	default:
		m.diags = append(m.diags, diag.Errorf(m.at.pos, "%s: %s could read %s.%s or %s.%s",
			m.at.path, label, structLabel(m.src), folds[0].v.Name(),
			structLabel(m.src), folds[1].v.Name()).WithHints(m.nameItHint(name)))
		return sourceField{}, false
	}

	if v, ok := m.promotedField(name); ok {
		return sourceField{v: v}, true
	}
	m.unmapped(name, label, tagged != nil && tagged.Embedded(), sources)
	return sourceField{}, false
}

// promotedField returns the field named name that src promotes from an
// embedded struct. A tag on it disqualifies it: the tag says the field is not
// simply name, and it is reported unmapped instead of copied against it. So
// does a pointer on the way, which reading through would panic on when nil,
// and an embedded field tagged map:"-", which takes no part in mapping.
func (m *matching) promotedField(name string) (*types.Var, bool) {
	named, _, _ := namedStruct(m.src)
	obj, index, _ := types.LookupFieldOrMethod(m.src, true, named.Obj().Pkg(), name)
	v, ok := obj.(*types.Var)
	if !ok || len(index) < 2 || !v.Exported() {
		return nil, false
	}

	st, ok := embeddedStruct(m.src, index[:len(index)-1])
	if !ok {
		return nil, false
	}
	if tagName(st.Tag(index[len(index)-1])) != "" {
		return nil, false
	}
	return v, true
}

func (m *matching) promoted(name string) bool {
	_, ok := m.promotedField(name)
	return ok
}

// embeddedStruct walks the embedded structs path leads through from t to the
// one declaring the field at the end of it, reporting false when one of them
// is embedded as a pointer or tagged map:"-".
func embeddedStruct(t types.Type, path []int) (*types.Struct, bool) {
	_, st, _ := namedStruct(t)
	for _, i := range path {
		next, ok := st.Field(i).Type().Underlying().(*types.Struct)
		if !ok || tagName(st.Tag(i)) == "-" {
			return nil, false
		}
		st = next
	}
	return st, true
}

// unmapped reports the destination field name, which label spells, as having
// no source. A source field whose tag nearly names it is pointed out: a tag
// may name a field only some destinations have, so one naming none of this
// destination's is no error by itself.
func (m *matching) unmapped(name, label string, embedded bool, sources []sourceField) {
	d := diag.Errorf(m.at.pos, "%s: no source in %s for %s", m.at.path, structLabel(m.src), label)
	if embedded {
		d = d.WithHints("an embedded field is matched as one field, by its type's name; its fields are not")
	}
	var tags []string
	for _, sf := range sources {
		if sf.tag != "" {
			tags = append(tags, sf.tag)
		}
	}
	if tag, ok := suggest.Closest(name, tags); ok {
		i := slices.IndexFunc(sources, func(sf sourceField) bool { return sf.tag == tag })
		d = d.WithHints(fmt.Sprintf("%s.%s is tagged map:%q; did you mean map:%q?",
			structLabel(m.src), sources[i].v.Name(), tag, name))
	}
	m.diags = append(m.diags, d.WithHints(m.leaveOutHint(name)))
}

// leaveOutHint says how to give field a source or leave it zero.
func (m *matching) leaveOutHint(field string) string {
	if m.at.suggest != "" {
		return fmt.Sprintf("declare %s in the //kanna:%s interface and add //kanna:%s %s=-",
			m.at.suggest, mapperKey, mapKey, field)
	}
	return fmt.Sprintf("add map:%q to the field of %s that fills it, or leave it zero with //kanna:%s %s=-",
		field, structLabel(m.src), mapKey, field)
}

// nameItHint says how to say which field fills field.
func (m *matching) nameItHint(field string) string {
	if m.at.suggest != "" {
		return fmt.Sprintf("declare %s in the //kanna:%s interface and add //kanna:%s %s=<field>",
			m.at.suggest, mapperKey, mapKey, field)
	}
	return fmt.Sprintf("name the one with //kanna:%s %s=<field> or a map tag", mapKey, field)
}

// overrideHint says how this function alone can read another field into
// field.
func (m *matching) overrideHint(field string) string {
	if m.at.suggest != "" {
		return fmt.Sprintf("or declare %s in the //kanna:%s interface and add //kanna:%s %s=<field>",
			m.at.suggest, mapperKey, mapKey, field)
	}
	return fmt.Sprintf("or override it in %s with //kanna:%s %s=<field>", m.at.path, mapKey, field)
}

func (m *matching) pos(v *types.Var) token.Position {
	return m.fset.Position(v.Pos())
}

// destinationNames lists what a //kanna:map entry can name in st: its exported
// fields, and the fields of the arms of its oneofs.
func destinationNames(st *types.Struct) []string {
	names := exportedNames(st)
	for f := range st.Fields() {
		if f.Exported() {
			for _, arm := range oneofArms(f.Type()) {
				names = append(names, arm.field.Name())
			}
		}
	}
	return names
}

// hasField reports whether st has an exported field called name.
func hasField(st *types.Struct, name string) bool {
	for f := range st.Fields() {
		if f.Exported() && f.Name() == name {
			return true
		}
	}
	return false
}

func exportedNames(st *types.Struct) []string {
	var names []string
	for f := range st.Fields() {
		if f.Exported() {
			names = append(names, f.Name())
		}
	}
	return names
}

// structLabel spells the declared struct t is or points to.
func structLabel(t types.Type) string {
	named, _, _ := namedStruct(t)
	return typeLabel(named)
}

// tagName returns the field the map tag of a field names, or "-", without its
// options.
func tagName(tag string) string {
	name, _, _ := strings.Cut(reflect.StructTag(tag).Get(tagKey), ",")
	return name
}

// sharedByTag reports whether v, a field of the struct t or of a struct it
// embeds, is tagged map:",share", or map:"Name,share".
func sharedByTag(t types.Type, v *types.Var) bool {
	_, st, ok := namedStruct(t)
	if !ok {
		return false
	}
	_, index, _ := types.LookupFieldOrMethod(t, true, v.Pkg(), v.Name())
	for i, n := range index {
		if n >= st.NumFields() {
			return false
		}
		if i == len(index)-1 {
			_, opt, _ := strings.Cut(reflect.StructTag(st.Tag(n)).Get(tagKey), ",")
			return opt == "share"
		}
		next, ok := types.Unalias(st.Field(n).Type()).Underlying().(*types.Struct)
		if !ok {
			return false
		}
		st = next
	}
	return false
}
