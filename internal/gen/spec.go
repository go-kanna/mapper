package gen

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
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
}

// entry is one Field=Source of a //kanna:map line.
type entry struct {
	field, source string
	pos           token.Position // the line
}

// excluded reports whether the entry is Field=-, which leaves the field zero.
func (e entry) excluded() bool {
	return e.source == "-"
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
	if d.Args != "" {
		diags = append(diags, diag.Errorf(d.Pos, "//kanna:%s takes no arguments, got %q", mapperKey, d.Args))
	}
	m, ds := directive.Find(pkg.Fset, doc, mapKey)
	if diags = append(diags, ds...); m.Found {
		diags = append(diags, diag.Errorf(m.Pos,
			"//kanna:%s goes above a method of the interface, not the interface", mapKey))
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
		if len(ds) == 0 {
			found = append(found, dc)
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
	for _, side := range []struct {
		role string
		t    types.Type
	}{{"source", src}, {"destination", dst}} {
		if problem, hints := pairTypeProblem(side.t); problem != "" {
			diags = append(diags, diag.Errorf(pos, "%s: the %s, %s, %s", name, side.role, typeLabel(side.t), problem).
				WithHints(hints...))
		}
	}

	entries, ds := mapEntries(fset, doc)
	if diags = append(diags, ds...); len(diags) > 0 {
		return decl{}, diags
	}

	param := sig.Params().At(0).Name()
	if param == "" || param == "_" {
		param = "src"
	}
	return decl{
		name:     name,
		param:    param,
		src:      src,
		dst:      dst,
		fallible: fallible,
		doc:      strings.TrimSpace(doc.Text()),
		pos:      pos,
		entries:  entries,
	}, nil
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

// mapEntries reads the //kanna:map line of doc. Whether each entry names a
// field that exists is for the function's plan to say.
func mapEntries(fset *token.FileSet, doc *ast.CommentGroup) ([]entry, []diag.Diag) {
	d, diags := directive.Find(fset, doc, mapKey)
	if !d.Found {
		return nil, diags
	}
	if d.Args == "" {
		return nil, append(diags, diag.Errorf(d.Pos, "//kanna:%s needs at least one Field=Source or Field=-", mapKey))
	}

	var entries []entry
	seen := make(map[string]bool)
	for arg := range strings.FieldsSeq(d.Args) {
		field, source, ok := strings.Cut(arg, "=")
		switch {
		case !ok:
			diags = append(diags, diag.Errorf(d.Pos, "//kanna:%s: %q is not Field=Source or Field=-", mapKey, arg))
		case !token.IsIdentifier(field):
			diags = append(diags, diag.Errorf(d.Pos, "//kanna:%s: %q is not a field name", mapKey, field))
		case source != "-" && !token.IsIdentifier(source):
			diags = append(diags, diag.Errorf(d.Pos, "//kanna:%s: %q is not a field name", mapKey, source))
		case seen[field]:
			diags = append(diags, diag.Errorf(d.Pos, "//kanna:%s: %s appears twice", mapKey, field))
		default:
			seen[field] = true
			entries = append(entries, entry{field: field, source: source, pos: d.Pos})
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
