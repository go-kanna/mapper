package gen

import (
	"fmt"
	"go/constant"
	"go/types"
	"strings"
	"unicode"

	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/enumtype"
)

// enumPlan is the body of a function written for an enum of kanna-value and an
// enum of protobuf, in either direction: a switch from each value of the source
// to the value of the destination whose name matches its own.
type enumPlan struct {
	cases []enumCase

	// zero is what a value no case takes becomes going out to protobuf: the
	// protobuf enum's constant for 0, or nil for a literal 0. Coming back, such
	// a value is an error instead.
	zero *types.Const
}

// enumCase maps the constant src to dst, or to the zero value of the
// destination when dst is nil.
type enumCase struct {
	src, dst *types.Const
}

// enumPair reports whether src and dst are an enum of kanna-value and an enum
// of protobuf, one each, whose constants a function can map by name.
func enumPair(src, dst types.Type) bool {
	if _, ok := enumtype.Of(src); ok {
		_, ok := protoEnum(dst)
		return ok
	}
	if _, ok := protoEnum(src); ok {
		_, ok := enumtype.Of(dst)
		return ok
	}
	return false
}

// protoEnum returns t as the enum it is, if it is one as protoc-gen-go writes
// them: an int32 whose Enum method returns a pointer to a copy.
func protoEnum(t types.Type) (*types.Named, bool) {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || !types.Identical(named.Underlying(), types.Typ[types.Int32]) {
		return nil, false
	}
	obj, _, _ := types.LookupFieldOrMethod(named, false, nil, "Enum")
	fn, ok := obj.(*types.Func)
	if !ok {
		return nil, false
	}
	sig := fn.Signature()
	return named, sig.Params().Len() == 0 && sig.Results().Len() == 1 &&
		types.Identical(sig.Results().At(0).Type(), types.NewPointer(named))
}

// planEnum works out how f, written for an enumPair, maps the constants of its
// source to those of its destination, and says which constant of the enum of
// kanna-value has no counterpart. Going out, a value no case takes becomes the
// zero value of protobuf; coming back, an error, so f returns one.
func (p *planner) planEnum(f *function, at site) *plan {
	goEnum, out := enumtype.Of(f.src)
	proto, _ := protoEnum(f.dst)
	if !out {
		goEnum, _ = enumtype.Of(f.dst)
		proto, _ = protoEnum(f.src)
	}

	protoConsts := enumtype.Constants(proto)
	byKey := make(map[string]*types.Const, len(protoConsts))
	for _, c := range protoConsts {
		if k := protoKey(proto, c); byKey[k] == nil {
			byKey[k] = c
		}
	}

	ep := &enumPlan{}
	matched := make(map[int64]*types.Const) // the constant of kanna-value for each number
	for _, c := range goEnum.Constants {
		if !c.Exported() && c.Pkg() != p.conv.pkg {
			continue
		}
		pc, ok := byKey[goKey(goEnum.Named, c)]
		if !ok {
			p.diags = append(p.diags, diag.Errorf(at.pos, "%s: %s has no counterpart in %s",
				at.path, qualifiedName(c, labelQualifier), typeLabel(proto)).
				WithHints(fmt.Sprintf("add %s to it, or list a func(%s) %s in a //kanna:%s list",
					protoName(proto, protoConsts, goEnum.Named, c), typeLabel(f.src), resultLabel(f), converterKey)))
			continue
		}
		if out {
			ep.cases = append(ep.cases, enumCase{src: c, dst: pc})
		} else if n := number(pc); matched[n] == nil {
			matched[n] = c
		}
	}

	for _, pc := range protoConsts {
		if number(pc) == 0 && ep.zero == nil {
			ep.zero = pc
		}
	}
	if !out {
		f.fallible = true
		done := make(map[int64]bool)
		for _, pc := range protoConsts {
			n := number(pc)
			if done[n] {
				continue // an alias, which a switch cannot take twice
			}
			done[n] = true
			switch c := matched[n]; {
			case c != nil:
				ep.cases = append(ep.cases, enumCase{src: pc, dst: c})
			case n == 0:
				ep.cases = append(ep.cases, enumCase{src: pc})
			}
		}
	}
	return &plan{fn: f, param: "src", enum: ep}
}

// resultLabel spells what f returns, with its error if it has one.
func resultLabel(f *function) string {
	if _, out := enumtype.Of(f.src); out {
		return typeLabel(f.dst)
	}
	return "(" + typeLabel(f.dst) + ", error)"
}

func labelQualifier(p *types.Package) string { return p.Name() }

func number(c *types.Const) int64 {
	n, _ := constant.Int64Val(c.Val())
	return n
}

// goKey is what the name of c, a constant of the enum of kanna-value named, is
// matched by: its name past the type's, such as Placed for OrderStatusPlaced,
// in lower case.
func goKey(named *types.Named, c *types.Const) string {
	name := strings.TrimPrefix(c.Name(), named.Obj().Name())
	if name == "" {
		name = c.Name()
	}
	return normalize(name)
}

// protoKey is what the name of c, a constant of the protobuf enum named, is
// matched by: the name of its value past the enum's own, such as PLACED for
// ORDER_STATUS_PLACED, in lower case without underscores.
func protoKey(named *types.Named, c *types.Const) string {
	value := normalize(protoValue(named, c))
	if rest, ok := strings.CutPrefix(value, normalize(protoEnumName(named))); ok && rest != "" {
		return rest
	}
	return value
}

// protoValue returns the name the value c has in the .proto file, past the
// prefix protoc-gen-go gives its constant: the enum's Go name, such as
// OrderStatus_ in OrderStatus_ORDER_STATUS_PLACED, or the enclosing message's
// for an enum declared in one, such as Order_ in Order_STATUS_PLACED.
func protoValue(named *types.Named, c *types.Const) string {
	typeName := named.Obj().Name()
	if rest, ok := strings.CutPrefix(c.Name(), typeName+"_"); ok {
		return rest
	}
	if i := strings.LastIndex(typeName, "_"); i >= 0 {
		if rest, ok := strings.CutPrefix(c.Name(), typeName[:i+1]); ok {
			return rest
		}
	}
	return c.Name()
}

// protoEnumName returns the name of the protobuf enum named in the .proto
// file: Status for Order_Status, an enum declared in the message Order.
func protoEnumName(named *types.Named) string {
	name := named.Obj().Name()
	return name[strings.LastIndex(name, "_")+1:]
}

// protoName suggests a name for the value of the protobuf enum named that c, a
// constant of the enum of kanna-value goEnum, would match, after the style of
// consts, its constants: REFUNDED, or ORDER_STATUS_REFUNDED when they start
// with the enum's name.
func protoName(named *types.Named, consts []*types.Const, goEnum *types.Named, c *types.Const) string {
	name := strings.TrimPrefix(c.Name(), goEnum.Obj().Name())
	if name == "" {
		name = c.Name()
	}
	name = upperSnake(name)
	prefix := normalize(protoEnumName(named))
	for _, pc := range consts {
		if strings.HasPrefix(normalize(protoValue(named, pc)), prefix) {
			return upperSnake(protoEnumName(named)) + "_" + name
		}
	}
	return name
}

// normalize drops the underscores of s and lowers its case, so that names
// spelled in either language compare.
func normalize(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

// upperSnake spells a Go name as protobuf spells a value: OnLeave as ON_LEAVE,
// HTTPError as HTTP_ERROR.
func upperSnake(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if i > 0 && unicode.IsUpper(c) &&
			(!unicode.IsUpper(r[i-1]) || i+1 < len(r) && unicode.IsLower(r[i+1])) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(c))
	}
	return b.String()
}
