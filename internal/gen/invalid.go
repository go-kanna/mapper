package gen

import "go/types"

// notTypeChecked is what is said of a declaration, a list element, or a field
// that does not type-check. Generation stops on it, and the run leaves it out
// of what it reports, as the type error behind it says more.
const notTypeChecked = "does not type-check"

// invalidType reports whether t does not type-check, such as *Missing for an
// undefined Missing.
func invalidType(t types.Type) bool {
	switch t := t.(type) {
	case *types.Basic:
		return t.Kind() == types.Invalid
	case *types.Pointer:
		return invalidType(t.Elem())
	case *types.Slice:
		return invalidType(t.Elem())
	case *types.Array:
		return invalidType(t.Elem())
	case *types.Map:
		return invalidType(t.Key()) || invalidType(t.Elem())
	case *types.Chan:
		return invalidType(t.Elem())
	}
	return false
}
