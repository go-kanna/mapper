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
	variants, _ := implementations(fset, t)
	return variants
}

// implementations returns the exported named types of the package of t that
// implement it, when it is an interface with an unexported method: by value,
// and through a pointer only, each in the order they are declared in fset.
func implementations(fset *token.FileSet, t types.Type) (byValue, byPointer []*types.Named) {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return nil, nil
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok || !slices.ContainsFunc(slices.Collect(iface.Methods()), func(m *types.Func) bool { return !m.Exported() }) {
		return nil, nil
	}
	scope := named.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() || !tn.Exported() {
			continue
		}
		v, ok := tn.Type().(*types.Named)
		switch {
		case !ok || v.TypeParams().Len() > 0 || types.IsInterface(v):
		case types.Implements(v, iface):
			byValue = append(byValue, v)
		case types.Implements(types.NewPointer(v), iface):
			byPointer = append(byPointer, v)
		}
	}
	slices.SortFunc(byValue, declOrder(fset))
	slices.SortFunc(byPointer, declOrder(fset))
	return byValue, byPointer
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
	_, byPointer := implementations(p.fset, sum)
	for _, v := range byPointer {
		p.diags = append(p.diags, diag.Errorf(at.pos, "%s: %s: *%s implements %s, but %s does not, so it is no variant",
			at.path, pair.dst.Name(), typeLabel(v), typeLabel(sum), typeLabel(v)).
			WithHints(fmt.Sprintf("declare the methods of %s on %s, not on *%s", typeLabel(sum), typeLabel(v), typeLabel(v))))
		for _, a := range armsFor(v, arms) {
			matched[a.wrapper] = true
		}
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
