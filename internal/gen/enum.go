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

// enumPlan is the body of a function for an enum of kanna-value and an enum of
// protobuf, in either direction: a switch from each value of the source to the
// value of the destination whose name matches its own.
type enumPlan struct {
	cases []enumCase

	// zero is the zero value of protobuf going out: the constant for 0, or nil
	// for a literal 0. Coming back, it is nil.
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

// planEnum works out how f, for an enumPair, maps the constants of its source to
// those of its destination, and says which constant of the enum of kanna-value
// has no counterpart. Going out, a value no case takes becomes the zero value of
// protobuf, or an error if f is declared to return one. Coming back, it is an
// error, so f has to return one, and so is protobuf's 0, unless a constant
// matches it or f allows it.
func (p *planner) planEnum(f *function, at site, param string) *plan {
	goEnum, out := enumtype.Of(f.src)
	proto, _ := protoEnum(f.dst)
	if !out {
		goEnum, _ = enumtype.Of(f.dst)
		proto, _ = protoEnum(f.src)
	}

	protoConsts := enumtype.Constants(proto, p.fset)
	byKey := make(map[string]*types.Const, len(protoConsts))
	for _, c := range protoConsts {
		if k := protoKey(proto, c); byKey[k] == nil {
			byKey[k] = c
		}
	}

	goConsts := enumtype.Constants(goEnum.Named, p.fset)
	ep := &enumPlan{}
	matched := make(map[int64]*types.Const) // the constant of kanna-value for each number
	for _, c := range goConsts {
		if !c.Exported() && c.Pkg() != p.conv.pkg {
			continue
		}
		pc, ok := byKey[goKey(goEnum.Named, c)]
		if !ok {
			d := diag.Errorf(at.pos, "%s: %s has no counterpart in %s",
				at.path, qualifiedName(c, labelQualifier), typeLabel(proto)).
				WithHints("add " + protoName(proto, protoConsts, goName(goEnum.Named, c)) + " to it")
			if name, value, ok := renamed(goEnum.Named, c, byKey); ok {
				d = d.WithHints(fmt.Sprintf("or name it %s, after its type, to match %s", name, protoValue(proto, value)))
			}
			p.diags = append(p.diags, d.WithHints(fmt.Sprintf("or list a func(%s) %s in a //kanna:%s list",
				typeLabel(f.src), resultLabel(f), converterKey)))
			continue
		}
		if out {
			ep.cases = append(ep.cases, enumCase{src: c, dst: pc})
		} else if n := number(pc); matched[n] == nil {
			matched[n] = c
		}
	}

	for _, pc := range protoConsts {
		if out && number(pc) == 0 && ep.zero == nil {
			ep.zero = pc
		}
	}
	if !out {
		if d := f.decl; d != nil && !d.fallible {
			p.diags = append(p.diags, diag.Errorf(at.pos, "%s has to return an error: %s can hold what %s has no value for",
				d.name, typeLabel(d.src), typeLabel(d.dst)).
				WithHints(fmt.Sprintf("declare it as %s(%s) (%s, error)", d.name, typeLabel(d.src), typeLabel(d.dst))))
		}
		f.fallible = true
		allowed := f.decl != nil && f.decl.allowUnspecified
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
			case n == 0 && allowed:
				ep.cases = append(ep.cases, enumCase{src: pc})
			}
		}
	}
	return &plan{fn: f, param: param, enum: ep}
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
	return normalize(goName(named, c))
}

// goName returns the name of c, a constant of the enum of kanna-value named, past
// the type's name, Placed for OrderStatusPlaced, or the whole of it for a
// constant not named after its type. What the constants share, APNs of
// APNsSandbox and APNsProduction, stays in: taken off, adding APNS_SANDBOX would
// move APNsSandbox off SANDBOX without a word.
func goName(named *types.Named, c *types.Const) string {
	if rest, ok := strings.CutPrefix(c.Name(), named.Obj().Name()); ok && rest != "" {
		return rest
	}
	return c.Name()
}

// renamed suggests a name after its type for c, a constant of the enum of
// kanna-value named that matches nothing, from the most words its name ends with
// that match a value of byKey: APNsEnvironmentSandbox for APNsSandbox, to match
// SANDBOX.
func renamed(named *types.Named, c *types.Const, byKey map[string]*types.Const) (string, *types.Const, bool) {
	if strings.HasPrefix(c.Name(), named.Obj().Name()) {
		return "", nil, false
	}
	r := []rune(c.Name())
	for i := range r {
		if !wordStart(r, i) {
			continue
		}
		if pc, ok := byKey[normalize(string(r[i:]))]; ok {
			return named.Obj().Name() + string(r[i:]), pc, true
		}
	}
	return "", nil, false
}

// wordStart reports whether a word of the Go name r starts at i, past its first:
// at a capital after anything else, or before a small letter, as Error starts
// HTTPError.
func wordStart(r []rune, i int) bool {
	return i > 0 && i < len(r) && unicode.IsUpper(r[i]) &&
		(!unicode.IsUpper(r[i-1]) || i+1 < len(r) && unicode.IsLower(r[i+1]))
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

// protoName suggests a name for the value of the protobuf enum named that a
// constant called goName would match, after the style of consts, its constants:
// REFUNDED, or ORDER_STATUS_REFUNDED when they start with the enum's name.
func protoName(named *types.Named, consts []*types.Const, goName string) string {
	name := upperSnake(goName)
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
		if wordStart(r, i) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(c))
	}
	return b.String()
}
