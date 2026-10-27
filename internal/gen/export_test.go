package gen

import (
	"fmt"
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
