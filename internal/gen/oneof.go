package gen

import (
	"cmp"
	"go/types"
	"slices"

	"github.com/go-kanna/internal/diag"
)

// oneofArm is one implementation of a protobuf oneof's interface: a struct
// wrapping the arm's value in its one field, as protoc-gen-go writes them.
type oneofArm struct {
	wrapper *types.Named
	field   *types.Var
}

// oneofArms lists the arms of the oneof t is: the structs of the interface's
// package that implement it through a pointer and wrap one exported field, in
// the order they are declared, which protoc-gen-go makes the order of the
// oneof's fields. Any other type has none.
func oneofArms(t types.Type) []oneofArm {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return nil
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok || iface.NumMethods() == 0 {
		return nil
	}

	var arms []oneofArm
	scope := named.Obj().Pkg().Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		wrapper, ok := tn.Type().(*types.Named)
		if !ok || wrapper.TypeParams().Len() > 0 {
			continue
		}
		st, ok := wrapper.Underlying().(*types.Struct)
		if !ok || st.NumFields() != 1 || !st.Field(0).Exported() || !types.Implements(types.NewPointer(wrapper), iface) {
			continue
		}
		arms = append(arms, oneofArm{wrapper: wrapper, field: st.Field(0)})
	}
	slices.SortFunc(arms, func(a, b oneofArm) int { return cmp.Compare(a.wrapper.Obj().Pos(), b.wrapper.Obj().Pos()) })
	return arms
}

// label spells the arm's field for messages, with the oneof field it belongs
// to.
func (a oneofArm) label(oneof string) string {
	return typeLabel(a.wrapper) + "." + a.field.Name() + " (arm of " + oneof + ")"
}

// oneofMatch is a destination oneof and the source field each of its arms is
// set from.
type oneofMatch struct {
	field *types.Var
	arms  []armMatch
}

type armMatch struct {
	arm oneofArm
	src *types.Var
}

// dstOneof matches the arms of the destination oneof f to the fields of the
// source, each as a destination field named as the arm's field would be.
func (m *matching) dstOneof(f *types.Var, arms []oneofArm, sources []sourceField, byField map[string]entry,
	srcStruct *types.Struct,
) (oneofMatch, bool) {
	o := oneofMatch{field: f}
	oneof := structLabel(m.dst) + "." + f.Name()
	for _, arm := range arms {
		name, label := arm.field.Name(), arm.label(oneof)
		var (
			sf sourceField
			ok bool
		)
		switch e, hasEntry := byField[name]; {
		case hasEntry && e.excluded():
			continue
		case hasEntry:
			sf, ok = m.named(e.source, sources), true
		default:
			sf, ok = m.source(name, label, nil, "", sources, srcStruct)
		}
		switch {
		case !ok:
		case sf.via != nil:
			m.diags = append(m.diags, diag.Errorf(m.at.pos, "%s: cannot set %s from %s, an arm of another oneof",
				m.at.path, label, sf.arm.label(structLabel(m.src)+"."+sf.via.Name())))
		default:
			o.arms = append(o.arms, armMatch{arm: arm, src: sf.v})
		}
	}
	return o, len(o.arms) > 0
}

// dstOneofPlan sets a destination oneof from the first of its arms whose
// source is set.
type dstOneofPlan struct {
	field *types.Var
	arms  []dstArmPlan
}

type dstArmPlan struct {
	arm  oneofArm
	read read

	// deref reports that what the read points to is converted, rather than
	// the pointer: the arm is taken only when the pointer is set.
	deref bool

	op op
}

// srcOneofPlan fills destination fields from the arms of a source oneof, one
// per arm the destination has a field for.
type srcOneofPlan struct {
	via  *types.Var
	read read
	arms []srcArmPlan
}

type srcArmPlan struct {
	arm oneofArm
	dst *types.Var
	op  op
}

// planDstOneof plans the destination oneof om of f into pl. An arm's source
// has to be able to be unset, a pointer, slice, or map, since the arm is taken
// only when it is set.
func (p *planner) planDstOneof(pl *plan, f *function, at site, om oneofMatch) {
	dp := dstOneofPlan{field: om.field}
	oneof := structLabel(f.dst) + "." + om.field.Name()
	for _, am := range om.arms {
		p.field = am.arm.field.Name()
		label := am.arm.label(oneof)
		srcLabel := structLabel(f.src) + "." + am.src.Name()

		i := slices.IndexFunc(readsOf(f.src, am.src), func(r read) bool { return nilable(r.typ) })
		if i < 0 {
			p.diags = append(p.diags, diag.Errorf(at.pos,
				"%s: cannot set %s from %s (%s): an arm is set only when its source is, which takes a pointer, slice, or map",
				at.path, label, srcLabel, typeLabel(am.src.Type())))
			continue
		}
		r := readsOf(f.src, am.src)[i]
		o, deref, ok := p.armOp(r.typ, am.arm.field.Type())
		if !ok {
			p.diags = append(p.diags, p.cannotMap(at, srcLabel, am.src.Type(), label, am.arm.field.Type()))
			continue
		}
		dp.arms = append(dp.arms, dstArmPlan{arm: am.arm, read: r, deref: deref, op: o})
	}
	if len(dp.arms) > 0 {
		pl.dstOneofs = append(pl.dstOneofs, dp)
	}
}

// armOp finds how a set source of type read becomes the arm's field. A pointer
// is converted through what it points to, the arm being taken only when it is
// set, unless the field takes the pointer itself or only the pointer converts.
func (p *planner) armOp(read, field types.Type) (op, bool, bool) {
	if ptr, ok := types.Unalias(read).(*types.Pointer); ok && !types.Identical(read, field) {
		if o, ok := p.conv.op(ptr.Elem(), field); ok {
			return o, true, true
		}
	}
	o, ok := p.conv.op(read, field)
	return o, false, ok
}

// planSrcArm plans pair, whose source is the arm of a oneof of f's source, into
// pl, beside the other arms of that oneof.
func (p *planner) planSrcArm(pl *plan, f *function, at site, pair fieldPair) {
	arm, via := pair.src.arm, pair.src.via
	o, ok := p.conv.op(arm.field.Type(), pair.dst.Type())
	if !ok {
		p.diags = append(p.diags, p.cannotMap(at, arm.label(structLabel(f.src)+"."+via.Name()), arm.field.Type(),
			structLabel(f.dst)+"."+pair.dst.Name(), pair.dst.Type()))
		return
	}

	i := slices.IndexFunc(pl.srcOneofs, func(s srcOneofPlan) bool { return s.via == via })
	if i < 0 {
		pl.srcOneofs = append(pl.srcOneofs, srcOneofPlan{via: via, read: readOf(f.src, via)})
		i = len(pl.srcOneofs) - 1
	}
	pl.srcOneofs[i].arms = append(pl.srcOneofs[i].arms, srcArmPlan{arm: arm, dst: pair.dst, op: o})
}

// readOf reads field from a src through its getter when that yields the
// field's own type, and as the field otherwise.
func readOf(src types.Type, field *types.Var) read {
	reads := readsOf(src, field)
	if i := slices.IndexFunc(reads, func(r read) bool { return types.Identical(r.typ, field.Type()) }); i >= 0 {
		return reads[i]
	}
	return reads[len(reads)-1]
}
