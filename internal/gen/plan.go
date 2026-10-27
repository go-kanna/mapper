package gen

import (
	"fmt"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strconv"
	"unicode"

	"github.com/go-kanna/internal/diag"
)

// plan is the body of one function: how each field of its destination is
// filled.
type plan struct {
	fn     *function
	param  string // the source parameter's name
	fields []fieldPlan
}

// fieldPlan fills one destination field.
type fieldPlan struct {
	dst  *types.Var
	read read
	op   op
}

// read is how a source field is read: the field itself, or its getter.
type read struct {
	name   string
	getter bool
	typ    types.Type // what the read yields
}

// planner plans the functions of one generated file, writing functions for the
// pairs of structs that need them as it goes.
type planner struct {
	fset  *token.FileSet
	conv  converter
	taken map[string]bool // top-level names the file cannot use

	funcs []*function
	plans []*plan
	queue []*function        // written, not planned yet
	sites map[*function]site // where a written function's problems go
	uses  []tagUse
	diags []diag.Diag

	// at and field say where the function being planned got to, for a
	// function written on the way to say where it came from.
	at    site
	field string
}

// planFuncs plans the functions ds declares and the ones written for them,
// in that order, converting with cs. declared holds the top-level names the
// package declares outside the generated file, which no function may take.
func planFuncs(fset *token.FileSet, ds []decl, cs convs, declared map[string]token.Position) ([]*plan, []diag.Diag) {
	p := &planner{fset: fset, taken: make(map[string]bool), sites: make(map[*function]site)}
	p.conv = converter{convs: cs, funcs: p}
	for name := range declared {
		p.taken[name] = true
	}

	for i := range ds {
		d := &ds[i]
		if at, ok := declared[d.name]; ok {
			p.diags = append(p.diags, diag.Errorf(d.pos, "%s is declared in the package already, at %s:%d:%d",
				d.name, filepath.Base(at.Filename), at.Line, at.Column))
			continue
		}
		p.taken[d.name] = true
		p.funcs = append(p.funcs, &function{name: d.name, src: d.src, dst: d.dst, decl: d, fallible: d.fallible})
	}

	for _, f := range slices.Clone(p.funcs) {
		p.plan(f, site{pos: f.decl.pos, path: f.decl.name}, f.decl.param, f.decl.entries)
	}
	for len(p.queue) > 0 {
		f := p.queue[0]
		p.queue = p.queue[1:]
		p.plan(f, p.sites[f], "src", nil)
	}

	p.settleErrors()
	p.diags = append(p.diags, unusedTags(p.uses)...)
	return p.plans, dedupe(p.diags)
}

// plan plans f, whose problems go to at.
func (p *planner) plan(f *function, at site, param string, entries []entry) {
	pairs, uses, diags := matchFields(p.fset, at, f.src, f.dst, entries)
	p.uses = append(p.uses, uses...)
	p.diags = append(p.diags, diags...)

	pl := &plan{fn: f, param: param}
	for _, pair := range pairs {
		p.at, p.field = at, pair.dst.Name()
		r, o, ok := p.fieldOp(f.src, pair)
		if !ok {
			p.diags = append(p.diags, p.cannotMap(at, f, pair))
			continue
		}
		pl.fields = append(pl.fields, fieldPlan{dst: pair.dst, read: r, op: o})
	}
	p.plans = append(p.plans, pl)
}

// fieldOp finds how pair.dst is filled from pair.src, a field of src.
//
// A read whose type is the destination's wins, whichever it is. Otherwise a
// getter that turns a nil field into a zero value is passed over for the field
// itself, which can hand the nil on.
func (p *planner) fieldOp(src types.Type, pair fieldPair) (read, op, bool) {
	reads := readsOf(src, pair.src)
	dst := pair.dst.Type()
	for _, r := range reads {
		if types.Identical(r.typ, dst) {
			return r, opDirect{}, true
		}
	}
	if len(reads) == 2 && nilable(reads[1].typ) && !nilable(reads[0].typ) {
		reads[0], reads[1] = reads[1], reads[0]
	}
	for _, r := range reads {
		if o, ok := p.conv.op(r.typ, dst); ok {
			return r, o, true
		}
	}
	return read{}, nil, false
}

// readsOf lists the ways to read field from a src: its getter, when src has
// one as protoc-gen-go writes them, then the field itself.
func readsOf(src types.Type, field *types.Var) []read {
	var reads []read
	obj, _, _ := types.LookupFieldOrMethod(src, true, field.Pkg(), "Get"+field.Name())
	if m, ok := obj.(*types.Func); ok {
		if sig := m.Signature(); sig.Params().Len() == 0 && sig.Results().Len() == 1 {
			reads = append(reads, read{name: m.Name(), getter: true, typ: sig.Results().At(0).Type()})
		}
	}
	return append(reads, read{name: field.Name(), typ: field.Type()})
}

func nilable(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map:
		return true
	}
	return false
}

// cannotMap reports that nothing makes pair.dst out of pair.src in f.
func (p *planner) cannotMap(at site, f *function, pair fieldPair) diag.Diag {
	srcT, dstT := pair.src.Type(), pair.dst.Type()
	d := diag.Errorf(at.pos, "%s: cannot map %s.%s (%s) to %s.%s (%s)", at.path,
		structLabel(f.src), pair.src.Name(), typeLabel(srcT), structLabel(f.dst), pair.dst.Name(), typeLabel(dstT))

	hint := fmt.Sprintf("add a func(%s) %s to a //kanna:%s list", typeLabel(srcT), typeLabel(dstT), convertKey)
	if c, ok := p.conv.convs.unlistedFor(srcT, dstT); ok {
		hint = fmt.Sprintf("%s converts it; add it to a //kanna:%s list", c.label, convertKey)
	}
	d = d.WithHints(hint)
	if isInterface(srcT) || isInterface(dstT) {
		d = d.WithHints("an interface field is mapped only as a protobuf oneof, " +
			"whose arms are single-field structs implementing it")
	}
	return d
}

func isInterface(t types.Type) bool {
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

// find returns the function, declared or written, that maps src to dst.
func (p *planner) find(src, dst types.Type) (*function, bool) {
	i := slices.IndexFunc(p.funcs, func(f *function) bool { return samePair(f.src, f.dst, src, dst) })
	if i < 0 {
		return nil, false
	}
	return p.funcs[i], true
}

// write makes the function for the pair of structs src and dst, to be planned
// after the one being planned now.
func (p *planner) write(src, dst types.Type) *function {
	f := &function{name: p.name(helperName(src, dst)), src: src, dst: dst}
	p.funcs = append(p.funcs, f)
	p.queue = append(p.queue, f)
	p.sites[f] = site{
		pos:     p.at.pos,
		path:    p.at.path + ": " + p.field,
		suggest: fmt.Sprintf("%s(%s) %s", exported(helperName(src, dst)), typeLabel(src), typeLabel(dst)),
	}
	return f
}

// name returns base, or base2, base3, and so on, whichever the file has not
// taken, and takes it.
func (p *planner) name(base string) string {
	name := base
	for i := 2; p.taken[name]; i++ {
		name = base + strconv.Itoa(i)
	}
	p.taken[name] = true
	return name
}

// helperName names the function written for src and dst after their types:
// addressToEmployeev1 for model.Address to employeev1.Address, the package
// standing in for a type name the two share.
func helperName(src, dst types.Type) string {
	s, _, _ := namedStruct(src)
	d, _, _ := namedStruct(dst)
	to := d.Obj().Name()
	if to == s.Obj().Name() {
		to = exported(d.Obj().Pkg().Name())
	}
	return unexported(s.Obj().Name()) + "To" + to
}

func exported(s string) string {
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// unexported lowers the capitals s starts with, as Go spells the word they
// make: Address becomes address, ID id, and URLInfo urlInfo.
func unexported(s string) string {
	r := []rune(s)
	n := 0
	for n < len(r) && unicode.IsUpper(r[n]) {
		n++
	}
	if n > 1 && n < len(r) {
		n-- // the last capital starts the next word
	}
	for i := range n {
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

// settleErrors works out which written functions return an error, which is
// when anything they call can fail, and reports the declared functions that
// would have to return one but do not.
func (p *planner) settleErrors() {
	for changed := true; changed; {
		changed = false
		for _, pl := range p.plans {
			if pl.fn.decl == nil && !pl.fn.fallible && pl.cause() != nil {
				pl.fn.fallible, changed = true, true
			}
		}
	}

	for _, pl := range p.plans {
		d := pl.fn.decl
		if d == nil || d.fallible {
			continue
		}
		if fp := pl.cause(); fp != nil {
			verb, by := causeOf(fp.op)
			p.diags = append(p.diags, diag.Errorf(d.pos, "%s has to return an error: %s is %s %s, which can fail",
				d.name, fp.dst.Name(), verb, by).
				WithHints(fmt.Sprintf("declare it as %s(%s) (%s, error)", d.name, typeLabel(d.src), typeLabel(d.dst))))
		}
	}
}

// cause returns the first field whose filling can fail, or nil.
func (pl *plan) cause() *fieldPlan {
	for i := range pl.fields {
		if pl.fields[i].op.fallible() {
			return &pl.fields[i]
		}
	}
	return nil
}

// causeOf names what makes o fail.
func causeOf(o op) (string, string) {
	switch o := o.(type) {
	case opConv:
		return "converted by", o.c.label
	case opCall:
		return "mapped by", o.f.name
	case opDeref:
		return causeOf(o.elem)
	case opAddr:
		return causeOf(o.elem)
	case opGuard:
		return causeOf(o.elem)
	case opSlice:
		return causeOf(o.elem)
	case opMap:
		return causeOf(o.elem)
	}
	return "", ""
}

// dedupe drops the diagnostics said already: a struct met by several
// functions has its tags read by each.
func dedupe(diags []diag.Diag) []diag.Diag {
	seen := make(map[string]bool, len(diags))
	out := diags[:0]
	for _, d := range diags {
		if key := d.String(); !seen[key] {
			seen[key] = true
			out = append(out, d)
		}
	}
	return out
}
