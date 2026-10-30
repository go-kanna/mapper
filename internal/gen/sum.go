package gen

import (
	"cmp"
	"fmt"
	"go/types"
	"slices"

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
// they are declared. A oneof of protobuf is implemented by its wrappers through
// pointers only, so it is none. Any other type has none either.
func sumVariants(t types.Type) []*types.Named {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return nil
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok || !slices.ContainsFunc(slices.Collect(iface.Methods()), func(m *types.Func) bool { return !m.Exported() }) {
		return nil
	}
	var variants []*types.Named
	scope := named.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() || !tn.Exported() {
			continue
		}
		v, ok := tn.Type().(*types.Named)
		if ok && v.TypeParams().Len() == 0 && !types.IsInterface(v) && types.Implements(v, iface) {
			variants = append(variants, v)
		}
	}
	slices.SortFunc(variants, func(a, b *types.Named) int { return cmp.Compare(a.Obj().Pos(), b.Obj().Pos()) })
	return variants
}

// armFor returns the arm of arms that the variant v matches: the one whose field,
// or the message it holds, is named as v is, regardless of case and underscores.
func armFor(v *types.Named, arms []oneofArm) (oneofArm, bool) {
	key := normalize(v.Obj().Name())
	for _, a := range arms {
		if normalize(a.field.Name()) == key {
			return a, true
		}
		if m := namedOf(a.field.Type()); m != nil && normalize(m.Obj().Name()) == key {
			return a, true
		}
	}
	return oneofArm{}, false
}

// planSum plans pair, a field of f, when it maps a sum type to a oneof or a
// oneof to a sum type, reporting false for any other pair. Going out, a variant
// with no arm is an error; coming back, an arm with no variant is.
func (p *planner) planSum(f *function, at site, pair fieldPair) (sumPlan, bool) {
	reads := readsOf(f.src, pair.src.v)
	r := reads[0]
	srcT, dstT := pair.src.v.Type(), pair.dst.Type()

	variants, arms, out := sumVariants(srcT), oneofArms(dstT), true
	if len(variants) == 0 || len(arms) == 0 {
		variants, arms, out = sumVariants(dstT), oneofArms(srcT), false
	}
	if len(variants) == 0 || len(arms) == 0 {
		return sumPlan{}, false
	}

	sp := sumPlan{dst: pair.dst, read: r, out: out}
	matched := make(map[*types.Named]bool)
	for _, v := range variants {
		a, ok := armFor(v, arms)
		if !ok {
			if out {
				p.diags = append(p.diags, diag.Errorf(at.pos, "%s: %s: %s has no arm in %s", at.path, pair.dst.Name(),
					typeLabel(v), typeLabel(dstT)).
					WithHints(fmt.Sprintf("add an arm named %s to the oneof, or holding a message of that name",
						v.Obj().Name())))
			}
			continue
		}
		matched[a.wrapper] = true
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
