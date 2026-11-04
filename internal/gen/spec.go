package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/directive"
	"github.com/go-kanna/internal/packages"
)

// mapperKey spells the directive //kanna:mapper, which marks an interface whose
// methods are the functions to write.
const mapperKey = "mapper"

// mapKey spells the directive //kanna:map, which says what the destination
// fields of one function read.
const mapKey = "map"

// strictOption is the argument of //kanna:mapper strict.
const strictOption = "strict"

// shareKey spells the directive //kanna:share, which names the destination
// fields whose slices and maps are handed on as they are rather than copied.
const shareKey = "share"

// allowKey spells the directive //kanna:allow, which lets a function take a value
// it would otherwise reject.
const allowKey = "allow"

// unspecifiedOption is the argument of //kanna:allow unspecified.
const unspecifiedOption = "unspecified"

// decl is a function a //kanna:mapper interface declares.
type decl struct {
	name string

	// param names the source in the generated function: the parameter's own
	// name, or "src" when it has none.
	param string

	src, dst types.Type

	// fallible reports whether the function returns (dst, error).
	fallible bool

	// doc is the method's doc comment, without its directives, or "".
	doc string

	pos     token.Position
	entries []entry

	// strict reports whether the interface is marked //kanna:mapper strict: each
	// destination field is filled only from a source named for it, by
	// //kanna:map or a tag, never matched by its name alone.
	strict bool

	// share names the destination fields of //kanna:share, whose slices and
	// maps are handed on as they are rather than copied. Only field is set.
	share []entry

	// allowUnspecified reports //kanna:allow unspecified: protobuf's 0 becomes
	// the empty string of the enum of kanna-value rather than an error.
	allowUnspecified bool
}

// entry is one Field=Source of a //kanna:map line, or Field alone, which
// approves the source the field is matched to anyway, as a strict mapping
// requires and nothing else does.
type entry struct {
	field, source string
	pos           token.Position // the line
}

// excluded reports whether the entry is Field=-, which leaves the field zero.
func (e entry) excluded() bool {
	return e.source == "-"
}

// approves reports whether the entry is Field alone, which names no source.
func (e entry) approves() bool {
	return e.source == ""
}

// errorType is the predeclared error.
var errorType = types.Universe.Lookup("error").Type()

// decls returns the functions the //kanna:mapper interfaces of pkg declare, in
// the order they are written, and says what is wrong with the rest. A
// generated file is never read, so that an earlier run's output is not taken
// for input.
func decls(pkg *packages.Package) ([]decl, []diag.Diag) {
	var (
		found []decl
		diags []diag.Diag
	)
	for _, file := range pkg.Syntax {
		if ast.IsGenerated(file) {
			continue
		}
		for _, d := range file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, _ := spec.(*ast.TypeSpec)
				doc := ts.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				fs, ds := interfaceDecls(pkg, ts, doc)
				found = append(found, fs...)
				diags = append(diags, ds...)
			}
		}
	}
	return found, append(diags, duplicates(found)...)
}

// interfaceDecls reads the declaration ts, documented by doc, when it is
// marked //kanna:mapper.
func interfaceDecls(pkg *packages.Package, ts *ast.TypeSpec, doc *ast.CommentGroup) ([]decl, []diag.Diag) {
	d, diags := directive.Find(pkg.Fset, doc, mapperKey)
	if !d.Found {
		return nil, diags
	}
	args, ds := d.Parse(directive.Spec{Words: []string{strictOption}})
	diags = append(diags, ds...)
	strict := len(args) > 0
	for _, key := range []string{mapKey, shareKey, allowKey} {
		m, ds := directive.Find(pkg.Fset, doc, key)
		if diags = append(diags, ds...); m.Found {
			diags = append(diags, diag.Errorf(m.Pos,
				"//kanna:%s goes above a method of the interface, not the interface", key))
		}
	}

	pos := pkg.Fset.Position(ts.Name.Pos())
	it, ok := ts.Type.(*ast.InterfaceType)
	switch {
	case ts.Assign.IsValid():
		return nil, append(diags, diag.Errorf(pos, "//kanna:%s marks an interface declaration, not an alias", mapperKey))
	case !ok:
		return nil, append(diags, diag.Errorf(pos, "//kanna:%s marks an interface; %s is not one", mapperKey, ts.Name.Name))
	case ts.TypeParams != nil:
		return nil, append(diags, diag.Errorf(pos, "generic interface %s is not supported", ts.Name.Name))
	case len(it.Methods.List) == 0:
		return nil, append(diags, diag.Warningf(pos, "the interface has no methods, so it declares nothing to write"))
	}

	var found []decl
	for _, f := range it.Methods.List {
		if len(f.Names) == 0 {
			diags = append(diags, diag.Errorf(pkg.Fset.Position(f.Type.Pos()),
				"an interface marked //kanna:%s cannot embed %s; declare its methods in it",
				mapperKey, types.ExprString(f.Type)))
			continue
		}
		fn, ok := pkg.TypesInfo.Defs[f.Names[0]].(*types.Func)
		if !ok {
			continue
		}
		dc, ds := declOf(pkg.Fset, fn, f.Doc)
		diags = append(diags, ds...)
		if diag.HasErrors(ds) {
			continue
		}
		dc.strict = strict
		found = append(found, dc)
		for _, e := range dc.entries {
			if e.approves() && !strict {
				d := diag.Errorf(e.pos, "//kanna:%s %s: naming a field alone means something only in a strict mapping",
					mapKey, e.field)
				diags = append(diags, d.WithHints(fmt.Sprintf("drop %s, or mark the interface //kanna:%s %s",
					e.field, mapperKey, strictOption)))
			}
		}
	}
	return found, diags
}

// declOf reads fn, a method of a //kanna:mapper interface documented by doc.
func declOf(fset *token.FileSet, fn *types.Func, doc *ast.CommentGroup) (decl, []diag.Diag) {
	name, pos, sig := fn.Name(), fset.Position(fn.Pos()), fn.Signature()

	if name == "init" {
		return decl{}, []diag.Diag{diag.Errorf(pos, "init cannot be the name of a generated function")}
	}
	if sig.Params().Len() != 1 || sig.Variadic() {
		return decl{}, []diag.Diag{diag.Errorf(pos, "%s has to take one parameter, the source", name)}
	}
	res := sig.Results()
	fallible := res.Len() == 2 && types.Identical(res.At(1).Type(), errorType)
	if res.Len() != 1 && !fallible {
		return decl{}, []diag.Diag{diag.Errorf(pos,
			"%s has to return the destination, or the destination and an error", name)}
	}

	var diags []diag.Diag
	src, dst := sig.Params().At(0).Type(), res.At(0).Type()
	enums := enumPair(src, dst)
	for _, side := range []struct {
		role string
		t    types.Type
	}{{"source", src}, {"destination", dst}} {
		if packages.Invalid(side.t) {
			diags = append(diags, packages.NotTypeChecked(pos))
			continue
		}
		if problem, hints := pairTypeProblem(side.t); problem != "" && !enums {
			diags = append(diags, diag.Errorf(pos, "%s: the %s, %s, %s", name, side.role, typeLabel(side.t), problem).
				WithHints(hints...))
		}
	}

	entries, ds := mapEntries(fset, doc)
	share, ds2 := shareEntries(fset, doc)
	allow, ds3 := allowsUnspecified(fset, doc, name, src, dst)
	diags = slices.Concat(diags, ds, ds2, ds3)
	if enums && len(entries)+len(share) > 0 {
		diags = append(diags, diag.Errorf(pos, "%s maps enums, which have no fields for //kanna:%s or //kanna:%s to name",
			name, mapKey, shareKey))
	}
	if diag.HasErrors(diags) {
		return decl{}, diags
	}

	param := sig.Params().At(0).Name()
	if param == "" || param == "_" {
		param = "src"
	}
	return decl{
		name:             name,
		param:            param,
		src:              src,
		dst:              dst,
		fallible:         fallible,
		doc:              strings.TrimSpace(doc.Text()),
		pos:              pos,
		entries:          entries,
		share:            share,
		allowUnspecified: allow,
	}, diags
}

// pairTypeProblem says why t cannot be the source or the destination of a
// mapping function, in words that complete "the source, T, ...", or returns ""
// when it can.
func pairTypeProblem(t types.Type) (string, []string) {
	named, st, ok := namedStruct(t)
	switch {
	case !ok:
		return "is not a struct or a pointer to one", nil
	case named.TypeArgs().Len() > 0:
		return "is generic, which is not supported", nil
	case !hasExportedField(st) && hasGetter(named):
		return "has no exported fields to map", []string{"types generated with protobuf's Opaque API are not supported"}
	case !hasExportedField(st):
		return "has no exported fields to map", nil
	}
	return "", nil
}

// namedStruct returns the declared struct type that t is or points to.
func namedStruct(t types.Type) (*types.Named, *types.Struct, bool) {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil, nil, false
	}
	st, ok := named.Underlying().(*types.Struct)
	return named, st, ok
}

func hasExportedField(st *types.Struct) bool {
	for f := range st.Fields() {
		if f.Exported() {
			return true
		}
	}
	return false
}

// hasGetter reports whether named has a method like the GetName that protobuf
// writes for every field.
func hasGetter(named *types.Named) bool {
	for m := range named.Methods() {
		if strings.HasPrefix(m.Name(), "Get") {
			return true
		}
	}
	return false
}

// shareEntries reads the //kanna:share line of doc: the destination fields to
// hand on as they are. Whether each names a field that exists is for the
// function's plan to say.
func shareEntries(fset *token.FileSet, doc *ast.CommentGroup) ([]entry, []diag.Diag) {
	d, diags := directive.Find(fset, doc, shareKey)
	if !d.Found {
		return nil, diags
	}
	args, ds := d.Parse(directive.Spec{Any: "<field>", AnyWord: true, Needed: true})
	diags = append(diags, ds...)
	var share []entry
	for _, a := range args {
		if !token.IsIdentifier(a.Name) {
			diags = append(diags, diag.Errorf(d.Pos, "//kanna:%s: %q is not a field name", shareKey, a.Name))
			continue
		}
		share = append(share, entry{field: a.Name, pos: d.Pos})
	}
	return share, diags
}

// allowsUnspecified reads the //kanna:allow line of doc, a method declaring
// name from src to dst, whose only use is on a function from an enum of protobuf.
func allowsUnspecified(
	fset *token.FileSet, doc *ast.CommentGroup, name string, src, dst types.Type,
) (bool, []diag.Diag) {
	d, diags := directive.Find(fset, doc, allowKey)
	if !d.Found {
		return false, diags
	}
	if _, ds := d.Parse(directive.Spec{Words: []string{unspecifiedOption}, Needed: true}); len(ds) > 0 {
		return false, append(diags, ds...)
	}
	if _, fromProto := protoEnum(src); !fromProto || !enumPair(src, dst) {
		return false, append(diags, diag.Errorf(d.Pos,
			"//kanna:%s %s goes on a function from an enum of protobuf to an enum of kanna-value, which %s is not",
			allowKey, unspecifiedOption, name))
	}
	return true, diags
}

// mapEntries reads the //kanna:map line of doc. Whether each entry names a
// field that exists is for the function's plan to say.
func mapEntries(fset *token.FileSet, doc *ast.CommentGroup) ([]entry, []diag.Diag) {
	d, diags := directive.Find(fset, doc, mapKey)
	if !d.Found {
		return nil, diags
	}
	args, ds := d.Parse(directive.Spec{Any: "<field>", AnyWord: true, AnyKey: true, Needed: true})
	diags = append(diags, ds...)
	var entries []entry
	for _, a := range args {
		switch {
		case !token.IsIdentifier(a.Name):
			diags = append(diags, diag.Errorf(d.Pos, "//kanna:%s: %q is not a field name", mapKey, a.Name))
		case a.HasValue && a.Value != "-" && !token.IsIdentifier(a.Value):
			diags = append(diags, diag.Errorf(d.Pos, "//kanna:%s: %q is not a field name", mapKey, a.Value))
		default:
			entries = append(entries, entry{field: a.Name, source: a.Value, pos: d.Pos})
		}
	}
	return entries, diags
}

// duplicates reports the functions declared under a name another interface of
// the package has taken already: they all go into one file.
func duplicates(found []decl) []diag.Diag {
	var diags []diag.Diag
	first := make(map[string]token.Position, len(found))
	for _, d := range found {
		p, ok := first[d.name]
		if !ok {
			first[d.name] = d.pos
			continue
		}
		diags = append(diags, diag.Errorf(d.pos, "%s is declared again; the first is at %s:%d:%d",
			d.name, filepath.Base(p.Filename), p.Line, p.Column))
	}
	return diags
}

// typeLabel spells t with package names, as messages do.
func typeLabel(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

// funcLabel spells fn with its package's name, as messages do.
func funcLabel(fn *types.Func) string {
	return qualifiedName(fn, func(p *types.Package) string { return p.Name() })
}
