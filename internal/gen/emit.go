package gen

import (
	"fmt"
	"go/types"
	"io"
	"strings"

	"github.com/go-kanna/internal/gofile"
)

// generator names the tool in the header of the files it writes.
const generator = "kanna-mapper"

// emit renders plans into the file generated for the package pkgName at
// pkgPath, which declares the names in declared besides.
func emit(pkgPath, pkgName string, plans []*plan, declared []string) *gofile.File {
	f := gofile.New(generator, pkgPath, pkgName)
	f.Reserve(declared...)
	for _, pl := range plans {
		f.Reserve(pl.fn.name)
	}
	for i, pl := range plans {
		if i > 0 {
			_, _ = io.WriteString(f, "\n")
		}
		e := &emitter{f: f, s: f.Scope(), pl: pl}
		e.function()
		_, _ = io.WriteString(f, e.b.String())
	}
	return f
}

// emitter renders one function.
type emitter struct {
	f  *gofile.File
	s  gofile.Scope
	pl *plan
	b  strings.Builder

	src  string // the parameter
	zero string // what the function returns, with the error, when it fails
	err  string // the error variable, once named
}

func (e *emitter) function() {
	fn := e.pl.fn
	srcT, dstT := e.typ(fn.src), e.typ(fn.dst)
	e.src = e.s.Name(e.pl.param)
	e.zero = dstT + "{}"
	lit, amp := dstT, ""
	if ptr, ok := types.Unalias(fn.dst).(*types.Pointer); ok {
		e.zero, lit, amp = "nil", e.typ(ptr.Elem()), "&"
	}

	e.doc()
	result := dstT
	if fn.fallible {
		result = "(" + dstT + ", error)"
	}
	fmt.Fprintf(&e.b, "func %s(%s %s) %s {\n", fn.name, e.src, srcT, result)
	if _, ok := types.Unalias(fn.src).(*types.Pointer); ok {
		fmt.Fprintf(&e.b, "if %s == nil {\nreturn %s\n}\n", e.src, e.ret(e.zero))
	}

	values := make([]string, len(e.pl.fields))
	for i, f := range e.pl.fields {
		values[i] = e.value(f.op, e.read(f.read), f.dst.Type(), f.dst.Name())
	}

	// A oneof is set after the literal, so a function with one builds its
	// result in a variable; any other returns the literal itself.
	oneofs := len(e.pl.dstOneofs) > 0 || len(e.pl.srcOneofs) > 0
	dst := ""
	if oneofs {
		dst = e.s.Name("dst")
		fmt.Fprintf(&e.b, "%s := %s%s{\n", dst, amp, lit)
	} else {
		fmt.Fprintf(&e.b, "return %s%s{\n", amp, lit)
	}
	for i, f := range e.pl.fields {
		fmt.Fprintf(&e.b, "%s: %s,\n", f.dst.Name(), values[i])
	}
	if !oneofs {
		fmt.Fprintf(&e.b, "}%s\n}\n", e.ret(""))
		return
	}
	e.b.WriteString("}\n")
	for _, o := range e.pl.dstOneofs {
		e.dstOneof(dst, o)
	}
	for _, o := range e.pl.srcOneofs {
		e.srcOneof(dst, o)
	}
	fmt.Fprintf(&e.b, "return %s\n}\n", e.ret(dst))
}

// doc writes the declaration's doc comment, or a sentence saying what the
// function maps.
func (e *emitter) doc() {
	fn := e.pl.fn
	if fn.decl == nil || fn.decl.doc == "" {
		fmt.Fprintf(&e.b, "// %s maps %s to %s.\n", fn.name, typeLabel(fn.src), typeLabel(fn.dst))
		return
	}
	for line := range strings.SplitSeq(fn.decl.doc, "\n") {
		if line == "" {
			e.b.WriteString("//\n")
		} else {
			fmt.Fprintf(&e.b, "// %s\n", line)
		}
	}
}

// ret spells what a return statement returns for v: v, followed by a nil error
// when the function returns one. An empty v is a value written just before.
func (e *emitter) ret(v string) string {
	if e.pl.fn.fallible {
		return v + ", nil"
	}
	return v
}

func (e *emitter) typ(t types.Type) string {
	return types.TypeString(t, e.f.Qualifier)
}

func (e *emitter) read(r read) string {
	if r.getter {
		return e.src + "." + r.name + "()"
	}
	return e.src + "." + r.name
}

// value renders o applied to val as an expression of type dstT, writing first
// the statements it needs. field names the destination field in an error.
func (e *emitter) value(o op, val string, dstT types.Type, field string) string {
	o, val = gets(o, val)
	if x, ok := e.expr(o, val); ok {
		return x
	}
	if call, ok := e.fallibleCall(o, val, field); ok {
		v := e.s.Name("v")
		fmt.Fprintf(&e.b, "%s, %s := %s\n", v, e.errVar(), call)
		e.check(field)
		return v
	}
	if a, ok := o.(opAddr); ok {
		if v, ok := e.addressable(a.elem, val, field); ok {
			return "&" + v
		}
	}
	v := e.s.Name("v")
	fmt.Fprintf(&e.b, "var %s %s\n", v, e.typ(dstT))
	e.assign(v, o, val, dstT, field)
	return v
}

// addressable writes o applied to val into a new variable when one statement
// can, a call that can fail included, and returns the variable.
func (e *emitter) addressable(o op, val, field string) (string, bool) {
	o, val = gets(o, val)
	if x, ok := e.expr(o, val); ok {
		v := e.s.Name("v")
		fmt.Fprintf(&e.b, "%s := %s\n", v, x)
		return v, true
	}
	if call, ok := e.fallibleCall(o, val, field); ok {
		v := e.s.Name("v")
		fmt.Fprintf(&e.b, "%s, %s := %s\n", v, e.errVar(), call)
		e.check(field)
		return v, true
	}
	return "", false
}

// gets applies the opGets o starts with to val, returning what is left of o
// and the value it applies to.
func gets(o op, val string) (op, string) {
	if g, ok := o.(opGet); ok {
		return gets(g.elem, method(val, "Get"))
	}
	return o, val
}

// expr renders o applied to val as one expression, when it can be one.
func (e *emitter) expr(o op, val string) (string, bool) {
	switch o := o.(type) {
	case opDirect:
		return val, true
	case opCast:
		return e.typ(o.dst) + "(" + val + ")", true
	case opConv:
		return e.callConv(o.c, val), !o.c.fallible
	case opCall:
		return o.f.name + "(" + val + ")", !o.f.fallible
	}
	return "", false
}

// fallibleCall renders o applied to val as a call returning an error too, when
// it is one. The argument of validated.New may need statements, which are
// written first, as value writes them.
func (e *emitter) fallibleCall(o op, val, field string) (string, bool) {
	switch o := o.(type) {
	case opConv:
		return e.callConv(o.c, val), o.c.fallible
	case opCall:
		return o.f.name + "(" + val + ")", o.f.fallible
	case opNew:
		arg := e.value(o.elem, val, o.value.TypeArgs().At(0), field)
		return fmt.Sprintf("%s.New[%s](%s)", e.f.Qualifier(o.value.Obj().Pkg()), e.typ(o.value.TypeArgs().At(1)), arg),
			true
	}
	return "", false
}

// callConv renders the call of c on val: a function's call, or a method's on
// val.
func (e *emitter) callConv(c conv, val string) string {
	if c.method {
		return method(val, c.fn.Name())
	}
	name := c.fn.Name()
	if q := e.f.Qualifier(c.fn.Pkg()); q != "" {
		name = q + "." + name
	}
	return name + "(" + val + ")"
}

// method renders the call of the method name on val.
func method(val, name string) string {
	if strings.HasPrefix(val, "*") {
		val = "(" + val + ")"
	}
	return val + "." + name + "()"
}

// errVar returns the name of the error variable.
func (e *emitter) errVar() string {
	if e.err == "" {
		e.err = e.s.Name("err")
	}
	return e.err
}

// check writes the return of the error just assigned, saying what field it
// was for.
func (e *emitter) check(field string) {
	fmt.Fprintf(&e.b, "if %s != nil {\nreturn %s, %s.Errorf(\"map %s.%s: %%w\", %s)\n}\n",
		e.err, e.zero, e.f.Import("fmt", "fmt"), structLabel(e.pl.fn.dst), field, e.err)
}

// assign writes the statements that set target, of type dstT, to o applied to
// val.
func (e *emitter) assign(target string, o op, val string, dstT types.Type, field string) {
	o, val = gets(o, val)
	if x, ok := e.expr(o, val); ok {
		fmt.Fprintf(&e.b, "%s = %s\n", target, x)
		return
	}
	if call, ok := e.fallibleCall(o, val, field); ok {
		v := e.s.Name("v")
		fmt.Fprintf(&e.b, "%s, %s := %s\n", v, e.errVar(), call)
		e.check(field)
		fmt.Fprintf(&e.b, "%s = %s\n", target, v)
		return
	}

	switch o := o.(type) {
	case opDeref:
		v := e.s.Name("v")
		fmt.Fprintf(&e.b, "if %s := %s; %s != nil {\n", v, val, v)
		e.assign(target, o.elem, "*"+v, dstT, field)
		e.b.WriteString("}\n")
	case opGuard:
		v := e.s.Name("v")
		fmt.Fprintf(&e.b, "if %s := %s; %s != nil {\n", v, val, v)
		e.assign(target, o.elem, v, dstT, field)
		e.b.WriteString("}\n")
	case opAddr:
		v, ok := e.addressable(o.elem, val, field)
		if !ok {
			elemT := types.Unalias(dstT).(*types.Pointer).Elem() //nolint:forcetypeassert // an opAddr's destination is a pointer
			v = e.s.Name("v")
			fmt.Fprintf(&e.b, "var %s %s\n", v, e.typ(elemT))
			e.assign(v, o.elem, val, elemT, field)
		}
		fmt.Fprintf(&e.b, "%s = &%s\n", target, v)
	case opSlice:
		elemT := o.dst.Underlying().(*types.Slice).Elem() //nolint:forcetypeassert // an opSlice's destination is a slice
		v, i, x := e.s.Name("v"), e.s.Name("i"), e.s.Name("e")
		fmt.Fprintf(&e.b, "if %s := %s; %s != nil {\n%s = make(%s, len(%s))\nfor %s, %s := range %s {\n",
			v, val, v, target, e.typ(o.dst), v, i, x, v)
		e.assign(target+"["+i+"]", o.elem, x, elemT, field)
		e.b.WriteString("}\n}\n")
	case opMap:
		elemT := o.dst.Underlying().(*types.Map).Elem() //nolint:forcetypeassert // an opMap's destination is a map
		v, k, x := e.s.Name("v"), e.s.Name("k"), e.s.Name("e")
		fmt.Fprintf(&e.b, "if %s := %s; %s != nil {\n%s = make(%s, len(%s))\nfor %s, %s := range %s {\n",
			v, val, v, target, e.typ(o.dst), v, k, x, v)
		e.assign(target+"["+k+"]", o.elem, x, elemT, field)
		e.b.WriteString("}\n}\n")
	}
}

// dstOneof writes the chain that sets the oneof o of dst from the first of its
// arms whose source is set, leaving it nil when none is.
func (e *emitter) dstOneof(dst string, o dstOneofPlan) {
	for i, a := range o.arms {
		v := e.s.Name("v")
		if i > 0 {
			e.b.WriteString("} else ")
		}
		fmt.Fprintf(&e.b, "if %s := %s; %s != nil {\n", v, e.read(a.read), v)
		val := v
		if a.deref {
			val = "*" + v
		}
		x := e.value(a.op, val, a.arm.field.Type(), o.field.Name())
		fmt.Fprintf(&e.b, "%s.%s = &%s{%s: %s}\n", dst, o.field.Name(), e.typ(a.arm.wrapper), a.arm.field.Name(), x)
	}
	e.b.WriteString("}\n")
}

// srcOneof writes the type switch that takes the arm of o that is there out of
// its wrapper, into its field of dst.
func (e *emitter) srcOneof(dst string, o srcOneofPlan) {
	v := e.s.Name("v")
	fmt.Fprintf(&e.b, "switch %s := %s.(type) {\n", v, e.read(o.read))
	for _, a := range o.arms {
		fmt.Fprintf(&e.b, "case *%s:\n", e.typ(a.arm.wrapper))
		e.assign(dst+"."+a.dst.Name(), a.op, v+"."+a.arm.field.Name(), a.dst.Type(), a.dst.Name())
	}
	e.b.WriteString("}\n")
}
