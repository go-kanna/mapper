package gen

import (
	"fmt"
	"strings"

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
	msgs := make([]string, len(diags))
	for i, d := range diags {
		msgs[i] = d.String()
	}
	return out, msgs
}
