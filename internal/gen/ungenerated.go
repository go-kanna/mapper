package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"slices"

	"github.com/go-kanna/internal/deps"
	"github.com/go-kanna/internal/directive"
	"github.com/go-kanna/internal/enumtype"
	"github.com/go-kanna/internal/valuetype"
)

// valueKey and enumKey spell //kanna:value and //kanna:enum, which mark a
// value type and an enum of kanna-value.
const (
	valueKey = "value"
	enumKey  = "enum"
)

// ungenerated returns the types t is or is made of, through pointers, slices,
// arrays and maps, that kanna-value has yet to generate: structs marked
// //kanna:value but no value type yet, and enums marked //kanna:enum with no
// constructor yet. The mark of a struct only tells a hint: what a type maps
// to is decided by what it is.
func (p *planner) ungenerated(t types.Type) []*types.Named {
	switch t := types.Unalias(t).(type) {
	case *types.Pointer:
		return p.ungenerated(t.Elem())
	case *types.Slice:
		return p.ungenerated(t.Elem())
	case *types.Array:
		return p.ungenerated(t.Elem())
	case *types.Map:
		return append(p.ungenerated(t.Key()), p.ungenerated(t.Elem())...)
	case *types.Named:
		if e, ok := enumtype.Of(t); ok && ungeneratedEnum(p.fset, e) {
			return []*types.Named{t}
		}
		if _, ok := t.Underlying().(*types.Struct); !ok {
			return nil
		}
		if _, ok := valuetype.Of(t); !ok && marked(p.fset, t.Obj(), valueKey) {
			return []*types.Named{t}
		}
	}
	return nil
}

// ungeneratedEnum reports whether e is marked //kanna:enum but has no
// constructor, as kanna-value has not generated it yet.
func ungeneratedEnum(fset *token.FileSet, e enumtype.Type) bool {
	return e.Constructor == nil && marked(fset, e.Named.Obj(), enumKey)
}

// ungeneratedHint says that named is to be a value type once kanna-value has
// run, and how to have it run first.
func ungeneratedHint(named *types.Named) string {
	key, what := valueKey, "methods"
	if _, ok := named.Underlying().(*types.Struct); !ok {
		key, what = enumKey, "constructor"
	}
	hint := fmt.Sprintf("%s is marked //kanna:%s, but kanna-value has not generated its %s yet",
		typeLabel(named), key, what)
	if _, ok := os.LookupEnv(deps.PendingEnv); !ok {
		hint += "; run kanna gen, which runs it before kanna-mapper"
	}
	return hint
}

// ungeneratedHints returns the hints of ungenerated for the types of a pair
// that cannot be mapped, one a type.
func (p *planner) ungeneratedHints(srcT, dstT types.Type) []string {
	var hints []string
	var seen []*types.Named
	for _, named := range append(p.ungenerated(srcT), p.ungenerated(dstT)...) {
		if !slices.Contains(seen, named) {
			seen = append(seen, named)
			hints = append(hints, ungeneratedHint(named))
		}
	}
	return hints
}

// marked reports whether the declaration of obj carries //kanna:<key>, reading
// the file it is in: a package loaded from export data has no syntax.
func marked(fset *token.FileSet, obj *types.TypeName, key string) bool {
	path := fset.Position(obj.Pos()).Filename
	if path == "" {
		return false
	}
	declFset := token.NewFileSet()
	f, err := parser.ParseFile(declFset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	return declMarked(declFset, f, obj.Name(), key)
}

// declMarked reports whether f declares the type name with //kanna:<key>.
func declMarked(fset *token.FileSet, f *ast.File, name, key string) bool {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.Name == name {
				doc := ts.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				d, _ := directive.Find(fset, doc, key)
				return d.Found
			}
		}
	}
	return false
}
