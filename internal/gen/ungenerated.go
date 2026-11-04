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
	"github.com/go-kanna/internal/valuetype"
)

// valueKey spells //kanna:value, which marks a value type of kanna-value.
const valueKey = "value"

// ungenerated returns the structs t is or is made of, through pointers, slices,
// arrays and maps, that are marked //kanna:value but are no value type yet, as
// kanna-value has not generated their methods. The mark only tells a hint:
// what a type maps to is decided by what it is.
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
		if _, ok := t.Underlying().(*types.Struct); !ok {
			return nil
		}
		if _, ok := valuetype.Of(t); !ok && marked(p.fset, t.Obj(), valueKey) {
			return []*types.Named{t}
		}
	}
	return nil
}

// ungeneratedHint says that named is to be a value type once kanna-value has
// run, and how to have it run first.
func ungeneratedHint(named *types.Named) string {
	hint := fmt.Sprintf("%s is marked //kanna:%s, but kanna-value has not generated its methods yet",
		typeLabel(named), valueKey)
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
