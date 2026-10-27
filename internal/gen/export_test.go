package gen

import (
	"fmt"
	"go/token"
	"go/types"
	"maps"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/packages"
)

// Decls finds the functions the //kanna:mapper interfaces of pkg declare. Each
// is spelled as its method is written, with its parameter and package names,
// followed by an indented line for its //kanna:map entries and one for its doc
// when it has them. Each diagnostic is spelled as diag.Diag.String does.
func Decls(pkg *packages.Package) ([]string, []string) {
	found, diags := decls(pkg)

	out := make([]string, len(found))
	for i, d := range found {
		result := typeLabel(d.dst)
		if d.fallible {
			result = fmt.Sprintf("(%s, error)", result)
		}
		s := fmt.Sprintf("%s(%s %s) %s", d.name, d.param, typeLabel(d.src), result)
		if len(d.entries) > 0 {
			entries := make([]string, len(d.entries))
			for j, e := range d.entries {
				entries[j] = e.field + "=" + e.source
				if e.excluded() {
					entries[j] += " (excluded)"
				}
			}
			s += "\n  map: " + strings.Join(entries, ", ")
		}
		if d.doc != "" {
			s += "\n  doc: " + d.doc
		}
		out[i] = s
	}
	return out, spell(diags)
}

// ConvsRead is what Convs reads. Each conversion is spelled "label: src ->
// dst", followed by " (fallible)", " [method]", and the depth and list it came
// through when that is another package's.
type ConvsRead struct {
	ByPair, Unlisted []string

	// Missing lists the packages to load for the lists named to be read.
	Missing []string

	Diags []string
}

// Convs reads the //kanna:convert lists of pkg, which declares functions as
// Decls finds them, following the lists they name into loaded.
func Convs(pkg *packages.Package, loaded []*packages.Package) ConvsRead {
	ds, _ := decls(pkg)
	byPath := make(map[string]*packages.Package, len(loaded))
	for _, p := range loaded {
		byPath[p.PkgPath] = p
	}
	cs, missing, diags := convsOf(pkg, byPath, ds)

	r := ConvsRead{Missing: missing, Diags: spell(diags)}
	for _, c := range cs.byPair {
		r.ByPair = append(r.ByPair, describeConv(c))
	}
	for _, c := range cs.unlisted {
		r.Unlisted = append(r.Unlisted, describeConv(c))
	}
	return r
}

// Matches pairs the fields of each function pkg declares, spelling each
// function as "Name: Dst <- Src, ...".
func Matches(pkg *packages.Package) ([]string, []string) {
	ds, _ := decls(pkg)
	out := make([]string, 0, len(ds))
	var diags []diag.Diag
	for _, d := range ds {
		m, dg := matchFields(pkg.Fset, site{pos: d.pos, path: d.name}, d.src, d.dst, d.entries)
		out = append(out, d.name+": "+describeMatched(m))
		diags = append(diags, dg...)
	}
	return out, spell(diags)
}

// MatchWritten pairs the fields of src and dst, two type expressions read in
// pkg's scope, as for a function written for them on the way from path, which
// a message would suggest declaring as suggest.
func MatchWritten(pkg *packages.Package, src, dst, path, suggest string) (string, []string) {
	at := site{path: path, suggest: suggest}
	m, diags := matchFields(pkg.Fset, at, eval(pkg, src), eval(pkg, dst), nil)
	return describeMatched(m), spell(diags)
}

// describeMatched spells each pair "Dst <- Src", an arm's field after its
// wrapper, and each oneof "Kind <- [Wrapper.Field <- Src, ...]".
func describeMatched(m matched) string {
	s := make([]string, 0, len(m.pairs)+len(m.oneofs))
	for _, p := range m.pairs {
		src := p.src.v.Name()
		if p.src.via != nil {
			src = p.src.arm.wrapper.Obj().Name() + "." + src
		}
		s = append(s, p.dst.Name()+" <- "+src)
	}
	for _, o := range m.oneofs {
		arms := make([]string, len(o.arms))
		for i, a := range o.arms {
			arms[i] = a.arm.wrapper.Obj().Name() + "." + a.arm.field.Name() + " <- " + a.src.Name()
		}
		s = append(s, o.field.Name()+" <- ["+strings.Join(arms, ", ")+"]")
	}
	return strings.Join(s, ", ")
}

// eval reads the type expression expr in the scope of pkg's first file.
func eval(pkg *packages.Package, expr string) types.Type {
	tv, err := types.Eval(pkg.Fset, pkg.Types, pkg.Syntax[0].Package, expr)
	if err != nil {
		panic(err)
	}
	return tv.Type
}

// Plans plans the functions pkg declares and the ones written for them. Each
// is spelled as its signature, with " (fallible)" when it returns an error, and
// a line per field: "Dst = .Read op", with "()" after a getter.
func Plans(pkg *packages.Package) ([]string, []string) {
	ds, _ := decls(pkg)
	cs, _, _ := convsOf(pkg, nil, ds)
	plans, diags := planFuncs(pkg.Fset, ds, cs, declaredNames(pkg))

	out := make([]string, len(plans))
	for i, pl := range plans {
		var b strings.Builder
		fmt.Fprintf(&b, "%s(%s %s) %s", pl.fn.name, pl.param, typeLabel(pl.fn.src), typeLabel(pl.fn.dst))
		if pl.fn.fallible {
			b.WriteString(" (fallible)")
		}
		for _, f := range pl.fields {
			fmt.Fprintf(&b, "\n  %s = %s %s", f.dst.Name(), describeRead(f.read), describeOp(f.op))
		}
		for _, o := range pl.dstOneofs {
			fmt.Fprintf(&b, "\n  %s = oneof", o.field.Name())
			for _, a := range o.arms {
				r := describeRead(a.read)
				if a.deref {
					r = "*" + r
				}
				fmt.Fprintf(&b, "\n    %s.%s = %s %s", a.arm.wrapper.Obj().Name(), a.arm.field.Name(), r, describeOp(a.op))
			}
		}
		for _, o := range pl.srcOneofs {
			fmt.Fprintf(&b, "\n  oneof %s", describeRead(o.read))
			for _, a := range o.arms {
				fmt.Fprintf(&b, "\n    %s = %s.%s %s", a.dst.Name(), a.arm.wrapper.Obj().Name(), a.arm.field.Name(),
					describeOp(a.op))
			}
		}
		out[i] = b.String()
	}
	return out, spell(diags)
}

func describeRead(r read) string {
	if r.getter {
		return "." + r.name + "()"
	}
	return "." + r.name
}

// Emit renders the file generated for pkg, following the lists it names into
// loaded and keeping clear of declared, the top-level names pkg declares
// besides. It renders nothing when there are errors to report.
func Emit(pkg *packages.Package, loaded []*packages.Package, declared map[string]token.Position,
) ([]byte, []string, error) {
	ds, diags := decls(pkg)
	byPath := make(map[string]*packages.Package, len(loaded))
	for _, p := range loaded {
		byPath[p.PkgPath] = p
	}
	cs, _, cd := convsOf(pkg, byPath, ds)
	plans, pd := planFuncs(pkg.Fset, ds, cs, declared)
	if diags = append(append(diags, cd...), pd...); diag.HasErrors(diags) {
		return nil, spell(diags), nil
	}
	src, err := emit(pkg.PkgPath, pkg.Name, plans, slices.Sorted(maps.Keys(declared))).Bytes()
	return src, spell(diags), err
}

// declaredNames returns the top-level names pkg declares outside its
// generated files, as output.DeclaredNames reads them from disk.
func declaredNames(pkg *packages.Package) map[string]token.Position {
	generated := packages.GeneratedFiles(pkg)
	names := make(map[string]token.Position)
	for _, name := range pkg.Types.Scope().Names() {
		pos := pkg.Fset.Position(pkg.Types.Scope().Lookup(name).Pos())
		if !generated[pos.Filename] {
			names[name] = pos
		}
	}
	return names
}

// Unexported is unexported, for its own test.
var Unexported = unexported

// Ops works out how each pair of types becomes the other, in pkg: with the
// conversions its own lists name, the functions it declares, and functions
// written as the pairs need them, which later pairs can call. Each pair is two
// type expressions, src and dst, read in the scope of pkg's first file. Each
// op is spelled as describeOp does, or "none"; each function written as
// "src -> dst".
func Ops(pkg *packages.Package, pairs ...[2]string) (ops, written []string) {
	ds, _ := decls(pkg)
	cs, _, _ := convsOf(pkg, nil, ds)
	funcs := &fakeFunctions{}
	for i := range ds {
		funcs.fs = append(funcs.fs, &function{name: ds[i].name, src: ds[i].src, dst: ds[i].dst, decl: &ds[i]})
	}
	c := converter{convs: cs, funcs: funcs}

	for _, p := range pairs {
		o, ok := c.op(eval(pkg, p[0]), eval(pkg, p[1]))
		if !ok {
			ops = append(ops, "none")
			continue
		}
		s := describeOp(o)
		if o.fallible() {
			s += " (fallible)"
		}
		ops = append(ops, s)
	}
	return ops, funcs.written
}

// fakeFunctions writes a function by recording it, under a name that says
// what it maps.
type fakeFunctions struct {
	fs      []*function
	written []string
}

func (f *fakeFunctions) find(src, dst types.Type) (*function, bool) {
	i := slices.IndexFunc(f.fs, func(fn *function) bool { return samePair(fn.src, fn.dst, src, dst) })
	if i < 0 {
		return nil, false
	}
	return f.fs[i], true
}

func (f *fakeFunctions) write(src, dst types.Type) *function {
	pair := typeLabel(src) + " -> " + typeLabel(dst)
	fn := &function{name: "write(" + pair + ")", src: src, dst: dst}
	f.fs = append(f.fs, fn)
	f.written = append(f.written, pair)
	return fn
}

// describeOp spells o compactly.
func describeOp(o op) string {
	switch o := o.(type) {
	case opDirect:
		return "direct"
	case opConv:
		return "conv:" + o.c.label
	case opCall:
		return "call:" + o.f.name
	case opDeref:
		return "deref(" + describeOp(o.elem) + ")"
	case opAddr:
		return "addr(" + describeOp(o.elem) + ")"
	case opGuard:
		return "guard(" + describeOp(o.elem) + ")"
	case opSlice:
		return "slice(" + describeOp(o.elem) + ")"
	case opMap:
		return "map(" + describeOp(o.elem) + ")"
	case opCast:
		return "cast:" + typeLabel(o.dst)
	default:
		return fmt.Sprintf("unknown %T", o)
	}
}

func describeConv(c conv) string {
	s := fmt.Sprintf("%s: %s -> %s", c.label, typeLabel(c.src), typeLabel(c.dst))
	if c.fallible {
		s += " (fallible)"
	}
	if c.method {
		s += " [method]"
	}
	if c.depth > 0 {
		s += fmt.Sprintf(" [depth %d, %s]", c.depth, c.list)
	}
	return s
}

// spell spells each diagnostic as diag.Diag.String does.
func spell(diags []diag.Diag) []string {
	msgs := make([]string, len(diags))
	for i, d := range diags {
		msgs[i] = d.String()
	}
	return msgs
}
