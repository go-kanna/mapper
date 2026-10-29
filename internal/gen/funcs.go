package gen

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"
	"github.com/go-kanna/internal/packages"
)

// converterKey spells the directive //kanna:converter, which marks a list of
// the functions that convert what Go cannot.
const converterKey = "converter"

// conv is a function a //kanna:converter list names, which converts src to dst.
type conv struct {
	fn *types.Func

	// method reports a method expression, which the generated code calls as
	// a method of the source.
	method bool

	src, dst types.Type
	fallible bool

	// depth is how far the entry is from the package generated into: 0 in its
	// own lists, 1 in a list one of those names, and so on.
	depth int

	// list names the list the entry is in when it is another package's, or is
	// "".
	list string

	label string // how messages name the function
	pos   token.Position
}

// convs is what converts what in one package.
type convs struct {
	// byPair holds the conversion in effect for each pair of types: the
	// nearest one.
	byPair []conv

	// unlisted holds the functions of the package that could convert but no
	// list names, for an error to point out.
	unlisted []conv
}

// lookup returns the conversion in effect from src to dst.
func (cs convs) lookup(src, dst types.Type) (conv, bool) {
	i := slices.IndexFunc(cs.byPair, func(c conv) bool { return samePair(c.src, c.dst, src, dst) })
	if i < 0 {
		return conv{}, false
	}
	return cs.byPair[i], true
}

// unlistedFor returns a function of the package that would convert src to dst
// if a list named it.
func (cs convs) unlistedFor(src, dst types.Type) (conv, bool) {
	i := slices.IndexFunc(cs.unlisted, func(c conv) bool { return samePair(c.src, c.dst, src, dst) })
	if i < 0 {
		return conv{}, false
	}
	return cs.unlisted[i], true
}

// anySlice is []any, the type of a list.
var anySlice = types.NewSlice(types.Universe.Lookup("any").Type())

// list is a variable marked //kanna:converter.
type list struct {
	obj   *types.Var
	pkg   *packages.Package
	elts  []ast.Expr
	diags []diag.Diag // what is wrong with the declaration; elts is nil then
}

// listReader follows the lists of one package into the lists they name.
type listReader struct {
	target *packages.Package
	loaded map[string]*packages.Package

	lists   map[string][]list // by package path
	read    map[*types.Var]bool
	queue   []queued
	entries []conv
	missing []string
	diags   []diag.Diag
}

type queued struct {
	l     list
	depth int
}

// convsOf reads the //kanna:converter lists of target, which declares ds, and
// follows the lists they name into loaded, which has to hold their packages
// with syntax and type information, from the same load as target. A package it
// needs but does not find is returned in missing, for the caller to load and
// call again.
func convsOf(target *packages.Package, loaded map[string]*packages.Package, ds []decl) (convs, []string, []diag.Diag) {
	r := &listReader{
		target: target,
		loaded: loaded,
		lists:  make(map[string][]list),
		read:   make(map[*types.Var]bool),
	}
	ls, unmarked := r.listsOf(target)
	r.diags = append(r.diags, unmarked...)
	for _, l := range ls {
		r.diags = append(r.diags, l.diags...)
		r.read[l.obj] = true
		if l.elts != nil {
			r.queue = append(r.queue, queued{l: l, depth: 0})
		}
	}
	for len(r.queue) > 0 {
		q := r.queue[0]
		r.queue = r.queue[1:]
		for _, e := range q.l.elts {
			r.entry(q, e)
		}
	}

	byPair := r.resolve(ds)
	slices.Sort(r.missing)
	return convs{byPair: byPair, unlisted: r.unlisted()}, slices.Compact(r.missing), r.diags
}

// listsOf returns the variables pkg marks //kanna:converter, in source order,
// and the warnings about the ones it seems to have meant to mark, such as a
// misspelled directive.
func (r *listReader) listsOf(pkg *packages.Package) ([]list, []diag.Diag) {
	if ls, ok := r.lists[pkg.PkgPath]; ok {
		return ls, nil
	}
	var (
		ls       []list
		unmarked []diag.Diag
	)
	for _, file := range pkg.Syntax {
		if ast.IsGenerated(file) {
			continue
		}
		for _, d := range file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, _ := spec.(*ast.ValueSpec)
				doc := vs.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				d, ds := directive.Find(pkg.Fset, doc, converterKey)
				if !d.Found {
					unmarked = append(unmarked, ds...)
					continue
				}
				ls = append(ls, listOf(pkg, vs, d, ds))
			}
		}
	}
	r.lists[pkg.PkgPath] = ls
	return ls, unmarked
}

// inLists returns whether a diagnostic points into an element of a
// //kanna:converter list of a package of set.
func inLists(set packages.Set) func(diag.Diag) bool {
	type span struct{ from, to token.Position }
	var spans []span
	r := &listReader{lists: make(map[string][]list)}
	for _, pkg := range set.Packages {
		ls, _ := r.listsOf(pkg)
		for _, l := range ls {
			for _, e := range l.elts {
				spans = append(spans, span{pkg.Fset.Position(e.Pos()), pkg.Fset.Position(e.End())})
			}
		}
	}
	return func(d diag.Diag) bool {
		return slices.ContainsFunc(spans, func(s span) bool {
			return d.Pos.Filename == s.from.Filename && !before(d.Pos, s.from) && before(d.Pos, s.to)
		})
	}
}

// before reports whether a is before b, in one file.
func before(a, b token.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
}

// hasLists reports whether pkg marks a variable //kanna:converter.
func hasLists(pkg *packages.Package) bool {
	ls, _ := (&listReader{lists: make(map[string][]list)}).listsOf(pkg)
	return len(ls) > 0
}

// listOf reads vs, which d marks //kanna:converter; diags are what
// directive.Find said about it.
func listOf(pkg *packages.Package, vs *ast.ValueSpec, d directive.Directive, diags []diag.Diag) list {
	obj, _ := pkg.TypesInfo.Defs[vs.Names[0]].(*types.Var)
	l := list{obj: obj, pkg: pkg, diags: diags}
	if d.Args != "" {
		l.diags = append(l.diags, diag.Errorf(d.Pos, "//kanna:%s takes no arguments, got %q", converterKey, d.Args))
	}

	var lit *ast.CompositeLit
	if len(vs.Names) == 1 && len(vs.Values) == 1 {
		lit, _ = ast.Unparen(vs.Values[0]).(*ast.CompositeLit)
	}
	if lit == nil || !types.Identical(pkg.TypesInfo.TypeOf(lit), anySlice) {
		l.diags = append(l.diags, diag.Errorf(pkg.Fset.Position(vs.Names[0].Pos()),
			"//kanna:%s marks a list: var %s = []any{...}", converterKey, vs.Names[0].Name))
	}
	if !diag.HasErrors(l.diags) {
		l.elts = lit.Elts
	}
	return l
}

// entry reads e, an element of the list q holds.
func (r *listReader) entry(q queued, e ast.Expr) {
	info := q.l.pkg.TypesInfo
	pos := q.l.pkg.Fset.Position(e.Pos())
	switch x := ast.Unparen(e).(type) {
	case *ast.FuncLit:
		r.diags = append(r.diags, diag.Errorf(pos, "a function literal cannot be listed; give it a name"))
	case *ast.IndexExpr, *ast.IndexListExpr:
		r.diags = append(r.diags, diag.Errorf(pos, "%s is generic, which is not supported", types.ExprString(e)))
	case *ast.Ident:
		r.named(q, e, info.Uses[x])
	case *ast.SelectorExpr:
		sel, ok := info.Selections[x]
		switch {
		case !ok: // a qualified identifier
			r.named(q, e, info.Uses[x.Sel])
		case sel.Kind() == types.MethodExpr:
			fn, _ := sel.Obj().(*types.Func)
			sig, _ := info.TypeOf(x).(*types.Signature)
			r.function(q, e, fn, sig, true)
		case sel.Kind() == types.MethodVal:
			fn, _ := sel.Obj().(*types.Func)
			r.diags = append(r.diags, diag.Errorf(pos, "%s is a method value; list the method expression %s instead",
				types.ExprString(e), methodLabel(fn.Signature().Recv().Type(), fn.Name(), r.qualifier())))
		default:
			r.notAFunction(pos, e)
		}
	default:
		r.notAFunction(pos, e)
	}
}

// named reads e, an element of the list q holds, which names obj, or nil
// when e names nothing that type-checks.
func (r *listReader) named(q queued, e ast.Expr, obj types.Object) {
	switch obj := obj.(type) {
	case nil:
		r.diags = append(r.diags, packages.NotTypeChecked(q.l.pkg.Fset.Position(e.Pos())))
	case *types.Func:
		r.function(q, e, obj, obj.Signature(), false)
	case *types.Var:
		if obj.Pkg() == nil || obj.Parent() != obj.Pkg().Scope() || !types.Identical(obj.Type(), anySlice) {
			r.notAFunction(q.l.pkg.Fset.Position(e.Pos()), e)
			return
		}
		r.include(q, e, obj)
	default:
		r.notAFunction(q.l.pkg.Fset.Position(e.Pos()), e)
	}
}

func (r *listReader) notAFunction(pos token.Position, e ast.Expr) {
	r.diags = append(r.diags, diag.Errorf(pos,
		"%s is not a function, a method expression, or a //kanna:%s list", types.ExprString(e), converterKey))
}

// include queues the list obj, which e names in the list q holds.
func (r *listReader) include(q queued, e ast.Expr, obj *types.Var) {
	if r.read[obj] {
		return
	}
	pkg := r.target
	if obj.Pkg() != r.target.Types {
		pkg = r.loaded[obj.Pkg().Path()]
	}
	if pkg == nil || pkg.Syntax == nil || pkg.TypesInfo == nil {
		r.missing = append(r.missing, obj.Pkg().Path())
		return
	}

	ls, _ := r.listsOf(pkg)
	i := slices.IndexFunc(ls, func(l list) bool { return l.obj == obj })
	if i < 0 {
		r.diags = append(r.diags, diag.Errorf(q.l.pkg.Fset.Position(e.Pos()),
			"%s is not a //kanna:%s list", types.ExprString(e), converterKey))
		return
	}
	l := ls[i]
	r.read[obj] = true
	r.diags = append(r.diags, l.diags...)
	if l.elts != nil {
		r.queue = append(r.queue, queued{l: l, depth: q.depth + 1})
	}
}

// function records fn, of signature sig, which e names in the list q holds; a
// method expression's signature takes the receiver first.
func (r *listReader) function(q queued, e ast.Expr, fn *types.Func, sig *types.Signature, method bool) {
	pos := q.l.pkg.Fset.Position(e.Pos())
	label := types.ExprString(e)
	src, dst, fallible, ok := convShape(sig)
	switch {
	case !ok:
		r.diags = append(r.diags, diag.Errorf(pos, "%s cannot convert: it is not func(A) B or func(A) (B, error)", label))
		return
	case !fn.Exported() && fn.Pkg() != r.target.Types:
		r.diags = append(r.diags, diag.Errorf(pos, "%s is not exported, so package %s cannot call it", label, r.target.Name))
		return
	}

	c := conv{fn: fn, method: method, src: src, dst: dst, fallible: fallible, depth: q.depth, pos: pos}
	if method {
		c.label = methodLabel(sig.Params().At(0).Type(), fn.Name(), r.qualifier())
	} else {
		c.label = qualifiedName(fn, r.qualifier())
	}
	if q.l.pkg != r.target {
		c.list = qualifiedName(q.l.obj, r.qualifier())
	}
	r.entries = append(r.entries, c)
}

// resolve keeps the nearest conversion for each pair of types, leaving out the
// pairs a function of ds maps, and reports the conversions that are equally
// near.
func (r *listReader) resolve(ds []decl) []conv {
	slices.SortStableFunc(r.entries, func(a, b conv) int { return cmp.Compare(a.depth, b.depth) })

	var kept []conv
	for _, c := range r.entries {
		if i := slices.IndexFunc(ds, func(d decl) bool { return samePair(d.src, d.dst, c.src, c.dst) }); i >= 0 {
			if c.depth == 0 {
				r.diags = append(r.diags, diag.Errorf(c.pos, "%s converts %s to %s, which %s is declared to do",
					c.label, typeLabel(c.src), typeLabel(c.dst), ds[i].name))
			}
			continue
		}
		i := slices.IndexFunc(kept, func(k conv) bool { return samePair(k.src, k.dst, c.src, c.dst) })
		if i < 0 {
			kept = append(kept, c)
			continue
		}
		switch k := kept[i]; {
		case k.fn == c.fn && k.method == c.method, k.depth < c.depth:
		default:
			r.diags = append(r.diags, diag.Errorf(c.pos, "%s and %s both convert %s to %s",
				k.label, c.label, typeLabel(c.src), typeLabel(c.dst)).WithHints(k.where()))
		}
	}
	return kept
}

// where says where c is listed, for a message about another entry.
func (c conv) where() string {
	at := fmt.Sprintf("%s:%d:%d", filepath.Base(c.pos.Filename), c.pos.Line, c.pos.Column)
	if c.list != "" {
		return fmt.Sprintf("%s is listed in %s, at %s", c.label, c.list, at)
	}
	return fmt.Sprintf("%s is listed at %s", c.label, at)
}

// unlisted returns the functions of the package that could convert and no list
// names. A generated file's are left out: they are the output.
func (r *listReader) unlisted() []conv {
	generated := packages.GeneratedFiles(r.target)
	scope := r.target.Types.Scope()

	var out []conv
	for _, name := range scope.Names() {
		fn, ok := scope.Lookup(name).(*types.Func)
		if !ok || generated[r.target.Fset.Position(fn.Pos()).Filename] ||
			slices.ContainsFunc(r.entries, func(c conv) bool { return c.fn == fn && !c.method }) {
			continue
		}
		src, dst, fallible, ok := convShape(fn.Signature())
		if !ok {
			continue
		}
		out = append(out, conv{
			fn: fn, src: src, dst: dst, fallible: fallible, label: fn.Name(),
			pos: r.target.Fset.Position(fn.Pos()),
		})
	}
	return out
}

// convShape reads sig as func(src) dst or func(src) (dst, error).
func convShape(sig *types.Signature) (src, dst types.Type, fallible, ok bool) {
	if sig.TypeParams().Len() > 0 || sig.Params().Len() != 1 || sig.Variadic() {
		return nil, nil, false, false
	}
	res := sig.Results()
	fallible = res.Len() == 2 && types.Identical(res.At(1).Type(), errorType)
	if res.Len() != 1 && !fallible {
		return nil, nil, false, false
	}
	return sig.Params().At(0).Type(), res.At(0).Type(), fallible, true
}

func samePair(src1, dst1, src2, dst2 types.Type) bool {
	return types.Identical(src1, src2) && types.Identical(dst1, dst2)
}

// qualifier names the packages other than the target by their names.
func (r *listReader) qualifier() types.Qualifier {
	return func(p *types.Package) string {
		if p == r.target.Types {
			return ""
		}
		return p.Name()
	}
}

// qualifiedName spells obj, a package-level object, as code in the target
// would.
func qualifiedName(obj types.Object, q types.Qualifier) string {
	if p := q(obj.Pkg()); p != "" {
		return p + "." + obj.Name()
	}
	return obj.Name()
}

// methodLabel spells the method expression for the method name of recv.
func methodLabel(recv types.Type, name string, q types.Qualifier) string {
	if ptr, ok := types.Unalias(recv).(*types.Pointer); ok {
		return fmt.Sprintf("(*%s).%s", types.TypeString(ptr.Elem(), q), name)
	}
	return types.TypeString(recv, q) + "." + name
}
