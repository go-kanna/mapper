package gen

import (
	"fmt"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"github.com/go-kanna/internal/diag"
)

// sumPlan fills a field from a sum type of the domain, an interface only its own
// package implements, or a protobuf oneof, either way: a type switch on the
// source picks the case its value is, and converts that value.
type sumPlan struct {
	dst  *types.Var
	read read

	// out reports whether the sum type is the source, going out to the
	// oneof; the oneof is the source coming back.
	out   bool
	cases []sumCase
}

// sumCase pairs a variant of the sum type with the arm of the oneof whose name
// matches its own, and converts the one into the other.
type sumCase struct {
	variant *types.Named
	arm     oneofArm
	op      op
}

// sumVariants lists the variants of the sum type t is: an interface with an
// unexported method, so that only its own package implements it, and the
// exported named types of that package that implement it by value, in the order
// they are declared in fset. A oneof of protobuf is implemented by its wrappers through
// pointers only, so it is none. Any other type has none either.
func sumVariants(fset *token.FileSet, t types.Type) []*types.Named {
	return implementations(fset, t).byValue
}

// impls are the named types of a package that implement an interface of it,
// each in the order they are declared.
type impls struct {
	// byValue are exported and not generic, and implement it by value.
	byValue []*types.Named
	// byPointer are exported and not generic, and implement it through a
	// pointer only.
	byPointer []*types.Named
	// unexported and generic implement it either way, but a type switch of
	// another package cannot name them.
	unexported, generic []*types.Named
}

// implementations returns the named types of the package of t that implement
// it, when it is an interface with an unexported method.
func implementations(fset *token.FileSet, t types.Type) impls {
	var is impls
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return is
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok || !slices.ContainsFunc(slices.Collect(iface.Methods()), func(m *types.Func) bool { return !m.Exported() }) {
		return is
	}
	scope := named.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		v, ok := tn.Type().(*types.Named)
		if !ok || types.IsInterface(v) {
			continue
		}
		switch {
		case v.TypeParams().Len() > 0:
			if declares(v, iface) {
				is.generic = append(is.generic, v)
			}
		case !types.Implements(types.NewPointer(v), iface):
		case !tn.Exported():
			is.unexported = append(is.unexported, v)
		case types.Implements(v, iface):
			is.byValue = append(is.byValue, v)
		default:
			is.byPointer = append(is.byPointer, v)
		}
	}
	for _, list := range [][]*types.Named{is.byValue, is.byPointer, is.unexported, is.generic} {
		slices.SortFunc(list, declOrder(fset))
	}
	return is
}

// declares reports whether the generic type v declares every method of iface,
// on itself or its pointer: whether it implements iface is left unspecified by
// go/types until it is instantiated.
func declares(v *types.Named, iface *types.Interface) bool {
	for m := range iface.Methods() {
		obj, _, _ := types.LookupFieldOrMethod(v, true, m.Pkg(), m.Name())
		if _, ok := obj.(*types.Func); !ok {
			return false
		}
	}
	return true
}

// armsFor returns the arms of arms that the variant v matches: those whose field
// is named as v is, regardless of case and underscores. The message an arm
// holds is not looked at, so that no arm wins by the order they are declared
// in.
func armsFor(v *types.Named, arms []oneofArm) []oneofArm {
	key := normalize(v.Obj().Name())
	var found []oneofArm
	for _, a := range arms {
		if normalize(a.field.Name()) == key {
			found = append(found, a)
		}
	}
	return found
}

// planSum plans pair, a field of f, when it maps a sum type to a oneof or a
// oneof to a sum type, reporting false for any other pair. Going out, a variant
// with no arm is an error; coming back, an arm with no variant is.
func (p *planner) planSum(f *function, at site, pair fieldPair) (sumPlan, bool) {
	reads := readsOf(f.src, pair.src.v)
	r := reads[0]
	srcT, dstT := pair.src.v.Type(), pair.dst.Type()

	variants, arms, out := sumVariants(p.fset, srcT), oneofArms(p.fset, dstT), true
	if len(variants) == 0 || len(arms) == 0 {
		variants, arms, out = sumVariants(p.fset, dstT), oneofArms(p.fset, srcT), false
	}
	if len(variants) == 0 || len(arms) == 0 {
		return sumPlan{}, false
	}

	sp := sumPlan{dst: pair.dst, read: r, out: out}
	matched := make(map[*types.Named]bool)

	// A value of the field could hold such a type or a pointer to it, which no
	// case can take both of.
	sum := srcT
	if !out {
		sum = dstT
	}
	is := implementations(p.fset, sum)
	noVariant := func(v *types.Named, msg, hint string) {
		p.diags = append(p.diags, diag.Errorf(at.pos, "%s: %s: %s", at.path, pair.dst.Name(), msg).WithHints(hint))
		for _, a := range armsFor(v, arms) {
			matched[a.wrapper] = true
		}
	}
	for _, v := range is.byPointer {
		noVariant(v, fmt.Sprintf("*%s implements %s, but %s does not, so it is no variant",
			typeLabel(v), typeLabel(sum), typeLabel(v)),
			fmt.Sprintf("declare the methods of %s on %s, not on *%s", typeLabel(sum), typeLabel(v), typeLabel(v)))
	}
	for _, v := range is.unexported {
		noVariant(v, fmt.Sprintf("%s implements %s, but is unexported, so it is no variant",
			typeLabel(v), typeLabel(sum)),
			fmt.Sprintf("export %s, or map the field with a function of your own", typeLabel(v)))
	}
	for _, v := range is.generic {
		noVariant(v, fmt.Sprintf("%s implements %s, but is generic, so it is no variant",
			typeLabel(v), typeLabel(sum)),
			"map the field with a function of your own")
	}
	for _, v := range variants {
		found := armsFor(v, arms)
		for _, a := range found {
			matched[a.wrapper] = true
		}
		switch {
		case len(found) == 0:
			if out {
				p.diags = append(p.diags, diag.Errorf(at.pos, "%s: %s: %s has no arm in %s", at.path, pair.dst.Name(),
					typeLabel(v), typeLabel(dstT)).
					WithHints(fmt.Sprintf("add an arm named %s to the oneof, or map the field with a function of your own",
						v.Obj().Name())))
			}
			continue
		case len(found) > 1:
			labels := make([]string, len(found))
			for i, a := range found {
				labels[i] = a.field.Name()
			}
			p.diags = append(p.diags, diag.Errorf(at.pos, "%s: %s: %s matches more than one arm of %s: %s",
				at.path, pair.dst.Name(), typeLabel(v), typeLabel(dstT), strings.Join(labels, ", ")).
				WithHints("map the field with a function of your own"))
			continue
		}
		a := found[0]
		from, to := types.Type(v), a.field.Type()
		if !out {
			from, to = to, from
		}
		o, ok := p.conv.op(from, to)
		if !ok {
			p.diags = append(p.diags, p.cannotMap(at, a.label(pair.dst.Name()), a.field.Type(), typeLabel(v), v))
			continue
		}
		sp.cases = append(sp.cases, sumCase{variant: v, arm: a, op: o})
	}
	if !out {
		for _, a := range arms {
			if !matched[a.wrapper] {
				p.diags = append(p.diags, diag.Errorf(at.pos, "%s: %s: %s has no variant in %s", at.path, pair.dst.Name(),
					a.label(pair.src.v.Name()), typeLabel(dstT)).
					WithHints(fmt.Sprintf("declare a variant named %s, or map the field with a function of your own",
						a.field.Name())))
			}
		}
	}
	return sp, true
}
