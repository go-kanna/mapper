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

// fieldPair is a destination field and the source field that fills it.
type fieldPair struct {
	dst, src *types.Var
}

// sourceField is a field of the source struct that a destination field can be
// matched to, with the destination field its tag names, if any.
type sourceField struct {
	v   *types.Var
	tag string
}

// tagUse is a tag of a source field, as the destination of one function met
// it. Whether the tag names a field at all depends on every destination its
// struct is mapped to; see unusedTags.
type tagUse struct {
	field    *types.Var
	tag      string
	pos      token.Position
	src, dst string   // the structs, as messages spell them
	names    []string // the exported fields of the destination
}

func (u tagUse) hit() bool {
	return slices.Contains(u.names, u.tag)
}

// matching pairs the fields of one function.
type matching struct {
	fset     *token.FileSet
	at       site
	src, dst types.Type
	diags    []diag.Diag
}

// matchFields pairs each exported field of dst's struct with the field of src's
// struct that fills it. A field left out, by map:"-" or by a //kanna:map entry,
// has no pair; a field without a source is an error. It also returns how the
// tags of the source met the destination.
//
// Each destination field takes the first of: its entry, its own tag, a source
// field tagged with its name, the same name, the same name in another case,
// and a field promoted from an embedded struct under the same name. An entry
// is for this function only, so it takes the place of the tags.
func matchFields(fset *token.FileSet, at site, src, dst types.Type, entries []entry,
) ([]fieldPair, []tagUse, []diag.Diag) {
	m := &matching{fset: fset, at: at, src: src, dst: dst}
	_, srcStruct, _ := namedStruct(src)
	_, dstStruct, _ := namedStruct(dst)

	sources := m.sourceFields(srcStruct)
	byField := m.entries(entries, sources, srcStruct, dstStruct)

	var pairs []fieldPair
	for i := range dstStruct.NumFields() {
		f := dstStruct.Field(i)
		if !f.Exported() {
			continue
		}
		tag, excluded := m.tag(dstStruct, i)
		e, hasEntry := byField[f.Name()]
		var (
			v  *types.Var
			ok bool
		)
		switch {
		case hasEntry && e.excluded(), !hasEntry && excluded:
			continue
		case hasEntry:
			v, ok = m.named(e.source)
		default:
			v, ok = m.source(f, tag, sources, srcStruct)
		}
		if ok {
			pairs = append(pairs, fieldPair{dst: f, src: v})
		}
	}

	return pairs, m.tagUses(sources, dstStruct), m.diags
}

// sourceFields lists the exported fields of st that take part in mapping.
func (m *matching) sourceFields(st *types.Struct) []sourceField {
	var fields []sourceField
	for i := range st.NumFields() {
		f := st.Field(i)
		if !f.Exported() {
			continue
		}
		tag, excluded := m.tag(st, i)
		if !excluded {
			fields = append(fields, sourceField{v: f, tag: tag})
		}
	}
	return fields
}

// tag reads the map tag of field i of st: the field it names, or that it is
// map:"-". A tag naming no valid field is an error, and the field is then left
// out.
func (m *matching) tag(st *types.Struct, i int) (string, bool) {
	tag, ok := reflect.StructTag(st.Tag(i)).Lookup(tagKey)
	switch {
	case !ok:
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
		if !hasField(dstStruct, e.field) {
			m.diags = append(m.diags, noSuchField(e, e.field, structLabel(m.dst), dstStruct))
			ok = false
		}
		switch {
		case e.excluded(), slices.ContainsFunc(sources, func(sf sourceField) bool { return sf.v.Name() == e.source }):
		case hasField(srcStruct, e.source):
			m.diags = append(m.diags, diag.Errorf(e.pos, "//kanna:%s %s=%s: %s.%s is tagged map:\"-\"",
				mapKey, e.field, e.source, structLabel(m.src), e.source))
			ok = false
		case !m.promoted(e.source):
			m.diags = append(m.diags, noSuchField(e, e.source, structLabel(m.src), srcStruct))
			ok = false
		}
		if ok {
			byField[e.field] = e
		}
	}
	return byField
}

func noSuchField(e entry, name, owner string, st *types.Struct) diag.Diag {
	d := diag.Errorf(e.pos, "//kanna:%s %s=%s: %s has no field %s", mapKey, e.field, e.source, owner, name)
	if s, ok := suggest.Closest(name, exportedNames(st)); ok {
		d = d.WithHints(fmt.Sprintf("did you mean %q?", s))
	}
	return d
}

// named returns the source field an entry names, which entries has found to
// exist.
func (m *matching) named(name string) (*types.Var, bool) {
	_, st, _ := namedStruct(m.src)
	for f := range st.Fields() {
		if f.Name() == name {
			return f, true
		}
	}
	return m.promotedField(name)
}

// source finds the source field for f, whose tag names tag or is "".
func (m *matching) source(f *types.Var, tag string, sources []sourceField, srcStruct *types.Struct,
) (*types.Var, bool) {
	var chosen *types.Var
	if tag != "" {
		for _, sf := range sources {
			if sf.v.Name() == tag {
				chosen = sf.v
				break
			}
		}
		if chosen == nil {
			chosen, _ = m.promotedField(tag)
		}
		if chosen == nil {
			d := diag.Errorf(m.pos(f), "map tag %q on %s.%s names no field of %s",
				tag, structLabel(m.dst), f.Name(), structLabel(m.src))
			if s, ok := suggest.Closest(tag, exportedNames(srcStruct)); ok {
				d = d.WithHints(fmt.Sprintf("did you mean %q?", s))
			}
			m.diags = append(m.diags, d.WithHints(m.overrideHint(f.Name())))
			return nil, false
		}
	}

	var tagged []*types.Var
	for _, sf := range sources {
		if sf.tag == f.Name() {
			tagged = append(tagged, sf.v)
		}
	}
	switch {
	case len(tagged) > 1:
		m.diags = append(m.diags, diag.Errorf(m.pos(tagged[1]), "%s.%s and %s.%s are both tagged map:%q",
			structLabel(m.src), tagged[0].Name(), structLabel(m.src), tagged[1].Name(), f.Name()))
		return nil, false
	case len(tagged) == 1 && chosen != nil && chosen != tagged[0]:
		m.diags = append(m.diags, diag.Errorf(m.pos(tagged[0]),
			"%s.%s is tagged map:%q, but %s.%s reads %s", structLabel(m.src), tagged[0].Name(), f.Name(),
			structLabel(m.dst), f.Name(), chosen.Name()))
		return nil, false
	case len(tagged) == 1:
		return tagged[0], true
	case chosen != nil:
		return chosen, true
	}

	var folds []*types.Var
	for _, sf := range sources {
		switch {
		case sf.tag != "":
		case sf.v.Name() == f.Name():
			return sf.v, true
		case strings.EqualFold(sf.v.Name(), f.Name()):
			folds = append(folds, sf.v)
		}
	}
	switch len(folds) {
	case 0:
	case 1:
		return folds[0], true
	default:
		m.diags = append(m.diags, diag.Errorf(m.at.pos, "%s: %s.%s could read %s.%s or %s.%s",
			m.at.path, structLabel(m.dst), f.Name(), structLabel(m.src), folds[0].Name(),
			structLabel(m.src), folds[1].Name()).WithHints(m.nameItHint(f.Name())))
		return nil, false
	}

	if v, ok := m.promotedField(f.Name()); ok {
		return v, true
	}
	m.unmapped(f)
	return nil, false
}

// promotedField returns the field named name that src promotes from an
// embedded struct. A tag on it disqualifies it: the tag says the field is not
// simply name, and it is reported unmapped instead of copied against it. So
// does a pointer on the way, which reading through would panic on when nil.
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
	if _, tagged := reflect.StructTag(st.Tag(index[len(index)-1])).Lookup(tagKey); tagged {
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
// is embedded as a pointer.
func embeddedStruct(t types.Type, path []int) (*types.Struct, bool) {
	_, st, _ := namedStruct(t)
	for _, i := range path {
		next, ok := st.Field(i).Type().Underlying().(*types.Struct)
		if !ok {
			return nil, false
		}
		st = next
	}
	return st, true
}

// unmapped reports f, a destination field with no source.
func (m *matching) unmapped(f *types.Var) {
	d := diag.Errorf(m.at.pos, "%s: no source in %s for %s.%s",
		m.at.path, structLabel(m.src), structLabel(m.dst), f.Name())
	if f.Embedded() {
		d = d.WithHints("an embedded field is matched as one field, by its type's name; its fields are not")
	}
	m.diags = append(m.diags, d.WithHints(m.leaveOutHint(f.Name())))
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

// tagUses returns how the tags among sources met dstStruct.
func (m *matching) tagUses(sources []sourceField, dstStruct *types.Struct) []tagUse {
	var uses []tagUse
	for _, sf := range sources {
		if sf.tag != "" {
			uses = append(uses, tagUse{
				field: sf.v, tag: sf.tag, pos: m.pos(sf.v),
				src: structLabel(m.src), dst: structLabel(m.dst), names: exportedNames(dstStruct),
			})
		}
	}
	return uses
}

// unusedTags reports the source tags that name a field of none of the
// destinations their structs are mapped to.
func unusedTags(uses []tagUse) []diag.Diag {
	type use struct {
		first tagUse
		dsts  []string
		names []string
		hit   bool
	}
	var order []*types.Var
	byField := make(map[*types.Var]*use)
	for _, u := range uses {
		a, ok := byField[u.field]
		if !ok {
			a = &use{first: u}
			byField[u.field] = a
			order = append(order, u.field)
		}
		a.hit = a.hit || u.hit()
		if !slices.Contains(a.dsts, u.dst) {
			a.dsts = append(a.dsts, u.dst)
			a.names = append(a.names, u.names...)
		}
	}

	var diags []diag.Diag
	for _, f := range order {
		a := byField[f]
		if a.hit {
			continue
		}
		d := diag.Errorf(a.first.pos, "map tag %q on %s.%s names no field of %s",
			a.first.tag, a.first.src, f.Name(), strings.Join(a.dsts, " or "))
		if s, ok := suggest.Closest(a.first.tag, a.names); ok {
			d = d.WithHints(fmt.Sprintf("did you mean %q?", s))
		}
		diags = append(diags, d)
	}
	return diags
}

func (m *matching) pos(v *types.Var) token.Position {
	return m.fset.Position(v.Pos())
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
