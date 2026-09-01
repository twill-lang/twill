// Package value defines Twill's runtime values and lexical environments.
package value

import (
	"math"
	"strconv"
	"strings"

	"github.com/twill-lang/twill/internal/ast"
	"github.com/twill-lang/twill/internal/tensor"
)

// Value is any runtime value. Concrete types: Num, *tensor.Tensor, Bool, Str,
// *List, *Closure, *Builtin, Unit.
type Value any

// Num is a plain number: one that carries no shape and no gradient history.
//
// Twill's numbers are rank-0 tensors, and a *tensor.Tensor costs two heap
// allocations (the struct and its one-element backing slice) before anything
// is computed. An interpreted scalar loop makes one of those per literal, per
// loop counter and per intermediate result, which profiling put at over half
// the runtime of `acc = acc + 1.0`. A Num boxed in a Value interface is one
// eight-byte allocation instead.
//
// The cost is that every boundary expecting a tensor has to widen: see
// AsTensor. A Num is produced only where nothing can be differentiating it, so
// widening never loses a graph. Anything that could (a grad argument, a tensor
// op, an autodiff leaf) turns it back into a rank-0 tensor first, which is why
// gradients see the same graph they always did.
type Num float64

// Int is a systems-mode I64: a two's-complement 64-bit integer held exactly.
//
// A Num is an f64 and holds an integer exactly only up to 2^53, which is why
// every hash mixer, IEEE bit pattern and 64-bit generator written in twill was
// silently wrong above that (docs/needs.md NEEDS-2). An Int is produced where
// the program said I64 -- an `I64` annotation on a binding, parameter, return,
// struct field or enum payload, the `i64()` conversion, an integer literal too
// large for an f64 to hold exactly, and any arithmetic between two Ints. It is
// never produced by a bare small literal, so numeric-mode programs, whose
// numbers are all Num, do not see it.
//
// Arithmetic on two Ints wraps, `/` truncates toward zero, `%` takes the sign
// of the dividend, and the bitwise words are exact on all 64 bits, per
// docs/language-guide.md. An Int meeting an integral Num (a literal, a length,
// a loop counter) computes as Int; meeting a fractional Num it widens to F64,
// which is what the same expression did before Int existed.
type Int int64

// MinExactNum and MaxExactNum bound the integers an f64 holds exactly. A Num
// inside them converts to Int and back without loss.
const (
	MaxExactNum = float64(1 << 53)
	MinExactNum = -MaxExactNum
)

// IntOfNum reports the Int an integral Num stands for, when it is one: a whole
// number inside the int64 range. A fractional Num, NaN, or an infinity is not
// an Int and reports false.
func IntOfNum(f float64) (int64, bool) {
	if f != math.Trunc(f) || f < -9223372036854775808.0 || f >= 9223372036854775808.0 {
		return 0, false
	}
	return int64(f), true
}

// AsInt reads a value as an I64: an Int directly, or an integral Num (or
// rank-0 tensor) that stands for one. It reports false for a fractional number
// and for anything non-numeric.
func AsInt(v Value) (int64, bool) {
	switch t := v.(type) {
	case Int:
		return int64(t), true
	case Num:
		return IntOfNum(float64(t))
	case *tensor.Tensor:
		if t.IsScalar() {
			return IntOfNum(t.Data[0])
		}
	}
	return 0, false
}

type Bool bool
type Str string
type Unit struct{}

// AsTensor widens a number to the rank-0 tensor the tensor engine works in,
// and passes a tensor through untouched. It reports false for anything that is
// not numeric.
//
// Every place that used to type-assert *tensor.Tensor goes through here, so
// adding Num did not have to change what any of them accept.
func AsTensor(v Value) (*tensor.Tensor, bool) {
	switch t := v.(type) {
	case *tensor.Tensor:
		return t, true
	case Num:
		return tensor.Scalar(float64(t)), true
	case Int:
		return tensor.Scalar(float64(t)), true
	}
	return nil, false
}

// AsNumber reads a value that is a single number, whether it arrived as a Num
// or as a one-element tensor. It does not allocate.
func AsNumber(v Value) (float64, bool) {
	switch t := v.(type) {
	case Num:
		return float64(t), true
	case Int:
		return float64(t), true
	case *tensor.Tensor:
		if len(t.Data) == 1 {
			return t.Data[0], true
		}
	}
	return 0, false
}

type List struct {
	Items []Value
}

// Record is a struct with named fields. Keys preserves declaration order for
// stable printing.
type Record struct {
	Keys   []string
	Fields map[string]Value
	// TypeName is the struct a typed literal was built from (`Lexer { ... }`),
	// or "" for a plain record. Struct types are otherwise erased at run time;
	// the name is kept so a field assignment can find the field's declared type.
	TypeName string
}

func NewRecord() *Record {
	return &Record{Fields: map[string]Value{}}
}

func (r *Record) Set(name string, v Value) {
	if _, ok := r.Fields[name]; !ok {
		r.Keys = append(r.Keys, name)
	}
	r.Fields[name] = v
}

func (r *Record) Get(name string) (Value, bool) {
	v, ok := r.Fields[name]
	return v, ok
}

// Dict is an insertion-ordered map with string keys, the systems dialect's
// Dict[Str, V]. Keys preserves declaration order for stable iteration.
type Dict struct {
	Keys []string
	Map  map[string]Value
}

func NewDict() *Dict { return &Dict{Map: map[string]Value{}} }

func (d *Dict) Set(k string, v Value) {
	if _, ok := d.Map[k]; !ok {
		d.Keys = append(d.Keys, k)
	}
	d.Map[k] = v
}

func (d *Dict) Get(k string) (Value, bool) {
	v, ok := d.Map[k]
	return v, ok
}

// Bytes is a growable byte buffer, the systems dialect's Bytes, distinct from a
// Str so building one is not quadratic.
type Bytes struct {
	Data []byte
}

type Closure struct {
	Params []string
	// ParamTypes is the annotation on each parameter, by name ("" when there is
	// none), so an `I64` or `F64` parameter converts its argument at the call the
	// way a `let` converts its bound value. Parallel to Params.
	ParamTypes []string
	Body       ast.Expr
	Env        *Env
	Name       string
	// The return annotation, kept so that `-> I64` truncates the returned value
	// the way `let n: I64 = ...` truncates a bound one. Both spellings reach the
	// annotation the same way the parser records it: a bare `I64` arrives as a
	// one-factor unit, a qualified or generic name as text.
	RetUnit *ast.UnitAnno
	RetType string
}

// Variant is one case of an enum value: a tag naming the case, and an optional
// single payload. A payload-less case (like None) has HasPayload false.
type Variant struct {
	Name       string
	Payload    Value
	HasPayload bool
}

// Builtin is a native function. Fn receives evaluated args and returns a
// value or an error.
type Builtin struct {
	Name     string
	Arity    int // -1 means variadic
	Variadic bool
	Fn       func(args []Value) (Value, error)
}

var TheUnit = Unit{}

// --- environments ----------------------------------------------------------

// envInline is how many bindings a scope holds before it needs a map.
//
// Most scopes hold one or two: a loop variable, a couple of parameters. A map
// for that allocates far more than it stores, and an interpreted loop makes a
// scope per iteration, so the map was 36% of the allocations in a scalar loop.
// Four covers the overwhelming majority; beyond it the map takes over and
// nothing else changes.
const envInline = 4

type Env struct {
	// Inline bindings, searched before the map. A linear scan of at most four
	// strings beats a hash for these sizes and costs no allocation at all.
	names  [envInline]string
	values [envInline]Value
	n      int

	vars map[string]Value // spillover, and every binding in an ordered scope
	// prebound holds hoisted top-level functions: visible to Get so a definition
	// or a top-level let may reference a function written further down, but not
	// counted as a binding, so it does not affect an ordered scope's definition
	// order (which the module-namespace record's field order comes from). The
	// real Define at the function's own declaration is what records its order.
	prebound map[string]Value
	parent   *Env
	order    []string // definition order, kept only when ordered is set
	// ordered is off for ordinary scopes: a call frame or loop body defines
	// names on every iteration, and paying for a slice append there would show
	// up in a training loop.
	ordered bool
}

// NewEnv creates a scope. The backing map is not allocated until a variable is
// defined, so scopes that only read outer variables cost nothing extra.
func NewEnv(parent *Env) *Env {
	return &Env{parent: parent}
}

// NewModuleEnv creates a scope that remembers the order its names were first
// defined in. A namespaced import snapshots such a scope into a record, and
// that record's field order has to be declaration order rather than Go map
// order for a program to be reproducible run to run.
func NewModuleEnv(parent *Env) *Env {
	return &Env{parent: parent, ordered: true}
}

func (e *Env) Get(name string) (Value, bool) {
	for env := e; env != nil; env = env.parent {
		for i := 0; i < env.n; i++ {
			if env.names[i] == name {
				return env.values[i], true
			}
		}
		if env.vars != nil {
			if v, ok := env.vars[name]; ok {
				return v, true
			}
		}
		if env.prebound != nil {
			if v, ok := env.prebound[name]; ok {
				return v, true
			}
		}
	}
	return nil, false
}

// Prebind binds name for lookup only, without recording it as a definition, so
// a hoisted function is visible before its declaration runs but does not alter
// this scope's definition order. A real Define later takes precedence.
func (e *Env) Prebind(name string, v Value) {
	if e.prebound == nil {
		e.prebound = make(map[string]Value, 8)
	}
	e.prebound[name] = v
}

// Define binds name in this scope.
func (e *Env) Define(name string, v Value) {
	// An ordered scope keeps every binding in the map, because Locals and
	// LocalNames hand that map and its order to the module snapshot.
	if e.ordered {
		if e.vars == nil {
			e.vars = make(map[string]Value, 4)
		}
		if _, redefined := e.vars[name]; !redefined {
			e.order = append(e.order, name)
		}
		e.vars[name] = v
		return
	}

	// Redefinition has to land where the name already is, or Get would find the
	// stale copy first and the new value would never be seen.
	for i := 0; i < e.n; i++ {
		if e.names[i] == name {
			e.values[i] = v
			return
		}
	}
	if e.vars != nil {
		if _, exists := e.vars[name]; exists {
			e.vars[name] = v
			return
		}
	}

	if e.n < envInline {
		e.names[e.n] = name
		e.values[e.n] = v
		e.n++
		return
	}
	if e.vars == nil {
		e.vars = make(map[string]Value, 4)
	}
	e.vars[name] = v
}

// Assign updates the nearest existing binding, returning false if none exists.
func (e *Env) Assign(name string, v Value) bool {
	for env := e; env != nil; env = env.parent {
		for i := 0; i < env.n; i++ {
			if env.names[i] == name {
				env.values[i] = v
				return true
			}
		}
		if env.vars != nil {
			if _, ok := env.vars[name]; ok {
				env.vars[name] = v
				return true
			}
		}
	}
	return false
}

// Locals returns this scope's own bindings (not parents'). Used to snapshot a
// module's definitions into a namespace record.
//
// Inline bindings are folded in, so a caller sees one scope rather than the two
// halves it happens to be stored in. Only a module scope is snapshotted and
// those keep everything in the map, so this copies nothing in practice.
func (e *Env) Locals() map[string]Value {
	if e.n == 0 {
		return e.vars
	}
	out := make(map[string]Value, e.n+len(e.vars))
	for k, v := range e.vars {
		out[k] = v
	}
	for i := 0; i < e.n; i++ {
		out[e.names[i]] = e.values[i]
	}
	return out
}

// LocalNames returns this scope's own bindings in the order they were first
// defined. It is populated only for a scope made by NewModuleEnv.
func (e *Env) LocalNames() []string { return e.order }

// --- helpers ---------------------------------------------------------------

func Truthy(v Value) bool {
	switch t := v.(type) {
	case Num:
		return t != 0
	case Int:
		return t != 0
	case *tensor.Tensor:
		if t.IsScalar() {
			return t.Data[0] != 0
		}
		return len(t.Data) > 0
	case Bool:
		return bool(t)
	case Str:
		return len(t) > 0
	case *List:
		return len(t.Items) > 0
	case *Record:
		return len(t.Keys) > 0
	case Unit:
		return false
	default:
		return true
	}
}

// Format renders a value for print and the REPL.
func Format(v Value) string {
	switch t := v.(type) {
	case Num:
		return FormatNumber(float64(t))
	case Int:
		return strconv.FormatInt(int64(t), 10)
	case *tensor.Tensor:
		// f64 renders exactly as it always has -- the differential harness compares
		// this text byte for byte against the bootstrap and every golden depends on
		// it. A narrow dtype instead prints its elements at the dtype's own shortest
		// decimal and carries a dtype= tag, so a bf16 1 is told from an i8 1.
		if t.DType() == tensor.DTF64 {
			if t.IsScalar() {
				return FormatNumber(t.Data[0])
			}
			return "tensor(" + formatNested(t.ToNested()) + ", shape=[" + joinInts(t.Shape) + "])"
		}
		dt := t.DType()
		render := func(x float64) string { return tensor.ShortestForDType(dt, x) }
		// A scalar prints as its bare value, matching a scalar of any other dtype;
		// only a shaped tensor carries the dtype= tag.
		if t.IsScalar() {
			return render(t.Data[0])
		}
		return "tensor(" + formatNestedWith(t.ToNested(), render) +
			", shape=[" + joinInts(t.Shape) + "], dtype=" + tensor.DTypeName(dt) + ")"
	case *tensor.QTensor:
		return "quantized(i8, shape=[" + joinInts([]int{t.Rows, t.Cols}) + "])"
	case *tensor.QTensorI4:
		return "quantized(i4, shape=[" + joinInts([]int{t.Rows, t.Cols}) + "])"
	case Bool:
		if t {
			return "true"
		}
		return "false"
	case Str:
		return string(t)
	case *List:
		parts := make([]string, len(t.Items))
		for i, it := range t.Items {
			parts[i] = Format(it)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Record:
		parts := make([]string, len(t.Keys))
		for i, k := range t.Keys {
			parts[i] = k + ": " + Format(t.Fields[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *Dict:
		parts := make([]string, len(t.Keys))
		for i, k := range t.Keys {
			parts[i] = k + ": " + Format(t.Map[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *Bytes:
		return string(t.Data)
	case *Variant:
		if t.HasPayload {
			return t.Name + "(" + Format(t.Payload) + ")"
		}
		return t.Name
	case *Closure:
		name := t.Name
		if name == "" {
			name = "anonymous"
		}
		return "<fn " + name + "(" + strings.Join(t.Params, ", ") + ")>"
	case *Builtin:
		return "<builtin " + t.Name + ">"
	case Unit:
		return "()"
	default:
		// Native opaque values (e.g. a fitted model) render via String() if they
		// provide one.
		if s, ok := v.(interface{ String() string }); ok {
			return s.String()
		}
		return "<value>"
	}
}

func formatNested(v any) string {
	if s, ok := v.([]any); ok {
		parts := make([]string, len(s))
		for i, item := range s {
			parts[i] = formatNested(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return FormatNumber(v.(float64))
}

// formatNestedWith is formatNested with the leaf renderer supplied, so a narrow
// tensor prints each element at its dtype's shortest decimal.
func formatNestedWith(v any, render func(float64) string) string {
	if s, ok := v.([]any); ok {
		parts := make([]string, len(s))
		for i, item := range s {
			parts[i] = formatNestedWith(item, render)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return render(v.(float64))
}

// FormatNumber prints integers without a decimal point and trims noise.
//
// The whole-number test goes through IntOfNum rather than through `int64(n)`,
// because converting a float outside the int64 range is undefined in Go and
// each architecture answers differently. On arm64 it saturates, so
// `int64(9223372036854775808.0)` is MaxInt64 and `float64` of that is the
// number we started with -- the guard passed and print answered
// 9223372036854775807, a value the program never held. On amd64 the same
// conversion yields MinInt64, the guard failed, and the number printed
// correctly. IntOfNum bounds the range first, so both machines print the same
// thing.
func FormatNumber(n float64) string {
	if i, ok := IntOfNum(n); ok {
		return strconv.FormatInt(i, 10)
	}
	s := strconv.FormatFloat(n, 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ", ")
}
