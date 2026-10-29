package gen

import (
	"go/types"

	"github.com/go-kanna/internal/valuetype"
)

// function is a function of the generated file: one a //kanna:mapper interface
// declares, or one written for a pair of structs that no declaration maps.
type function struct {
	name     string
	src, dst types.Type

	// decl is the declaration, or nil for a written function.
	decl *decl

	// fallible reports whether the function returns an error too. A written
	// function's is known only once every function is planned.
	fallible bool
}

// functions finds and makes the functions an op calls.
type functions interface {
	// find returns the function that maps src to dst, declared or written.
	find(src, dst types.Type) (*function, bool)

	// write returns a function to be written for src and dst, a pair of
	// structs or pointers to them.
	write(src, dst types.Type) *function
}

// op is how a value of one type becomes a value of another. Ops nest, as
// pointers, slices, and maps do.
type op interface {
	fallible() bool
}

// opDirect assigns the value as it is.
type opDirect struct{}

// opConv calls a function a //kanna:converter list names.
type opConv struct{ c conv }

// opCall calls a function of the generated file.
type opCall struct{ f *function }

// opDeref converts what a pointer points to, leaving the destination zero when
// the pointer is nil.
type opDeref struct{ elem op }

// opAddr converts the value and takes the address of the result.
type opAddr struct{ elem op }

// opGuard hands a pointer on to elem only when it is not nil, leaving the
// destination nil otherwise.
type opGuard struct{ elem op }

// opSlice converts a slice element by element; nil stays nil.
type opSlice struct {
	dst  types.Type
	elem op
}

// opMap converts a map value by value; nil stays nil.
type opMap struct {
	dst  types.Type
	elem op
}

// opCast is a Go conversion, dst(v).
type opCast struct{ dst types.Type }

// opGet converts the value a value type of kanna-value holds, read with its
// accessor.
type opGet struct {
	accessor string
	elem     op
}

// opNew converts the value to what a value type of kanna-value holds, held,
// and makes one of it with the type's constructor, which fails when the type's
// rule rejects it.
type opNew struct {
	ctor *types.Func
	held types.Type
	elem op
}

func (opDirect) fallible() bool  { return false }
func (o opConv) fallible() bool  { return o.c.fallible }
func (o opCall) fallible() bool  { return o.f.fallible }
func (o opDeref) fallible() bool { return o.elem.fallible() }
func (o opAddr) fallible() bool  { return o.elem.fallible() }
func (o opGuard) fallible() bool { return o.elem.fallible() }
func (o opSlice) fallible() bool { return o.elem.fallible() }
func (o opMap) fallible() bool   { return o.elem.fallible() }
func (opCast) fallible() bool    { return false }
func (o opGet) fallible() bool   { return o.elem.fallible() }
func (opNew) fallible() bool     { return true }

// converter works out ops from what a package lists and declares.
type converter struct {
	pkg   *types.Package // the package generated into
	convs convs
	funcs functions
}

// op returns how a src becomes a dst, or false when nothing can make it one.
//
// A function is written for a pair of structs only when nothing else does:
// the functions there are already, declared or written, are tried through
// pointers first, so that one pair is not written twice for a pointer's sake.
func (c converter) op(src, dst types.Type) (op, bool) {
	if o, ok := c.try(src, dst, false); ok {
		return o, true
	}
	return c.try(src, dst, true)
}

// try is op, writing functions for pairs of structs only when write is set.
//
// A source pointer is dereferenced before a destination pointer is taken, so
// that nil stays nil rather than becoming a pointer to a zero value. When the
// source pointer reaches a pointer destination through a function taking the
// pointer itself, it is handed on only when it is not nil, for the same reason.
func (c converter) try(src, dst types.Type, write bool) (op, bool) {
	src, dst = types.Unalias(src), types.Unalias(dst)
	if types.Identical(src, dst) {
		return opDirect{}, true
	}
	if cv, ok := c.convs.lookup(src, dst); ok {
		return opConv{cv}, true
	}
	if f, ok := c.funcs.find(src, dst); ok {
		return opCall{f}, true
	}

	sp, srcPtr := src.(*types.Pointer)
	if srcPtr {
		if e, ok := c.try(sp.Elem(), dst, false); ok {
			return opDeref{e}, true
		}
	}
	if dp, ok := dst.(*types.Pointer); ok {
		if e, ok := c.try(src, dp.Elem(), false); ok {
			if srcPtr {
				return opGuard{opAddr{e}}, true
			}
			return opAddr{e}, true
		}
	}

	// Pointers come first, so that a value of a value type is copied to a
	// pointer to it rather than read and checked again.
	if v, ok := valuetype.Of(src); ok {
		if e, ok := c.try(v.Held, dst, write); ok {
			return opGet{accessor: v.Accessor, elem: e}, true
		}
	}
	if v, ok := valuetype.Of(dst); ok && callable(v, c.pkg) {
		if e, ok := c.try(src, v.Held, write); ok {
			return opNew{ctor: v.Constructor, held: v.Held, elem: e}, true
		}
	}

	if ss, ok := src.Underlying().(*types.Slice); ok {
		if ds, ok := dst.Underlying().(*types.Slice); ok {
			if e, ok := c.try(ss.Elem(), ds.Elem(), write); ok {
				return opSlice{dst: dst, elem: e}, true
			}
		}
	}
	if sm, ok := src.Underlying().(*types.Map); ok {
		if dm, ok := dst.Underlying().(*types.Map); ok && types.Identical(sm.Key(), dm.Key()) {
			if e, ok := c.try(sm.Elem(), dm.Elem(), write); ok {
				return opMap{dst: dst, elem: e}, true
			}
		}
	}

	if lossless(src, dst) {
		return opCast{dst}, true
	}
	if write && writable(src, dst) {
		return opCall{c.funcs.write(src, dst)}, true
	}
	return nil, false
}

// callable reports whether code in pkg can make a value of v, a value type of
// kanna-value: its constructor is loaded, and declared in pkg or exported.
func callable(v valuetype.Type, pkg *types.Package) bool {
	return v.Constructor != nil && (v.Constructor.Exported() || v.Constructor.Pkg() == pkg)
}

// writable reports whether a function can be written for src and dst: both
// are declared structs, or pointers to them, with fields to map.
func writable(src, dst types.Type) bool {
	for _, t := range []types.Type{src, dst} {
		if problem, _ := pairTypeProblem(t); problem != "" {
			return false
		}
	}
	return true
}

// lossless reports whether dst(v) is a conversion Go allows and that keeps
// every value of src as it is, on every platform. Two types declared with
// type never convert this way: their values mean what each declares, even
// when one could be written as the other.
func lossless(src, dst types.Type) bool {
	if declared(src) && declared(dst) {
		return false
	}
	su, du := src.Underlying(), dst.Underlying()
	if types.Identical(su, du) {
		return types.ConvertibleTo(src, dst)
	}
	sb, srcBasic := su.(*types.Basic)
	db, dstBasic := du.(*types.Basic)
	switch {
	case srcBasic && dstBasic:
		return widens(sb, db)
	case srcBasic:
		return sb.Info()&types.IsString != 0 && isByteSlice(du)
	case dstBasic:
		return db.Info()&types.IsString != 0 && isByteSlice(su)
	}
	return false
}

// declared reports whether t is a type declared with type, rather than a
// predeclared one or a type literal.
func declared(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Pkg() != nil
}

func isByteSlice(t types.Type) bool {
	s, ok := t.(*types.Slice)
	if !ok {
		return false
	}
	b, ok := types.Unalias(s.Elem()).Underlying().(*types.Basic)
	return ok && b.Kind() == types.Uint8
}

// widens reports whether every value of the number type s is exactly a value
// of d, on every platform.
func widens(s, d *types.Basic) bool {
	if d.Info()&types.IsFloat != 0 {
		if s.Info()&types.IsFloat != 0 {
			return s.Kind() == types.Float32 && d.Kind() == types.Float64
		}
		_, bits, signed, ok := intBits(s.Kind())
		if signed {
			bits-- // the sign takes a bit the mantissa does not need
		}
		mantissa := 24
		if d.Kind() == types.Float64 {
			mantissa = 53
		}
		return ok && bits <= mantissa
	}

	_, sMax, sSigned, sOK := intBits(s.Kind())
	dMin, _, dSigned, dOK := intBits(d.Kind())
	switch {
	case !sOK || !dOK || sSigned && !dSigned:
		return false
	case sSigned == dSigned:
		return sMax <= dMin
	default: // unsigned into signed takes one bit more
		return sMax < dMin
	}
}

// intBits returns the fewest and the most bits an integer kind has across
// platforms, and its sign. int and uint are 32 bits on some platforms and 64
// on others; uintptr is left out.
func intBits(k types.BasicKind) (fewest, most int, signed, ok bool) {
	//nolint:exhaustive // Every other kind is not an integer this considers.
	switch k {
	case types.Int8:
		return 8, 8, true, true
	case types.Int16:
		return 16, 16, true, true
	case types.Int32:
		return 32, 32, true, true
	case types.Int64:
		return 64, 64, true, true
	case types.Int:
		return 32, 64, true, true
	case types.Uint8:
		return 8, 8, false, true
	case types.Uint16:
		return 16, 16, false, true
	case types.Uint32:
		return 32, 32, false, true
	case types.Uint64:
		return 64, 64, false, true
	case types.Uint:
		return 32, 64, false, true
	}
	return 0, 0, false, false
}
