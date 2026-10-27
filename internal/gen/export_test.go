package gen

import (
	"fmt"
	"go/types"
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

	at := pkg.Syntax[0].Package
	for _, p := range pairs {
		src, err := types.Eval(pkg.Fset, pkg.Types, at, p[0])
		if err != nil {
			panic(err)
		}
		dst, err := types.Eval(pkg.Fset, pkg.Types, at, p[1])
		if err != nil {
			panic(err)
		}
		o, ok := c.op(src.Type, dst.Type)
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
