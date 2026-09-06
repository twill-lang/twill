package interp

import (
	"github.com/twill-lang/twill/internal/ir"
	"github.com/twill-lang/twill/internal/tensor"
	"github.com/twill-lang/twill/internal/trace"
	"github.com/twill-lang/twill/internal/value"
)

// This file is the interpreter's half of the tracer. The other half is
// internal/trace. The split is deliberate: the tracer knows about graphs and
// kernels and nothing about twill, and this file knows about twill and calls
// into the tracer at exactly two kinds of place.
//
// The first kind is where an operation can be recorded instead of run: the
// arithmetic in evalBinary, and the builtins in tracedBuiltin below.
//
// The second kind, and the one correctness depends on, is where a value escapes
// the trace and something is about to read it. docs/CODEGEN.md section 2 names
// them: print, an if condition, a comparison, save, any builtin with no opcode,
// a closure the tracer cannot follow. In this implementation they are enumerated
// as calls to ip.escape(), and the list is short enough to check by reading:
//
//   - Apply, before any builtin's Fn runs. This is one funnel for every builtin
//     in the language, which is why "any builtin with no opcode" costs one line
//     rather than an audit of builtins.go.
//   - compare, and every use of value.Truthy: if, while, and, or, not.
//   - evalIndex, evalSlice, and an indexed assignment.
//   - iterate, for a `for` over a tensor.
//   - a dtype cast, an I64 or unit annotation, a coerced return.
//   - the value a program returns, and a tensor literal built from evaluated
//     elements.
//
// An escape does not fail. It replays the trace through internal/tensor and
// carries on, so the reader gets the interpreter's own answer and the program
// runs. That is the property the whole design is judged on: a program the
// compiler cannot take still runs, and still runs right.

// escape forces the open trace because something is about to read a value out
// of it.
func (ip *Interp) escape() {
	if ip.tr == nil {
		return
	}
	ip.tr.Escape()
	ip.checkTrace(0)
}

func (ip *Interp) checkTrace(line int) {
	if ip.tr == nil {
		return
	}
	if err := ip.tr.Err(); err != nil {
		ip.panicf(line, "%s", err.Error())
	}
}

// stmtScope opens a per-statement trace and returns the function that closes it.
//
// A statement is the unit because it is the largest region whose live values are
// known exactly and for free: when execStmt returns, the only thing that can
// still be read is the value it produced, because every Go frame that held
// anything else has gone. Everything else in the trace is dead, and a dead value
// is one the fusion pass can decline to give memory to, which is the whole point
// (docs/CODEGEN.md section 4).
//
// A wider scope would fuse more. It would also need a liveness analysis over the
// interpreter's environments and its own Go stack, and getting that wrong
// produces a wrong answer rather than a slow one.
func (ip *Interp) stmtScope() (owned bool) {
	if ip.tr == nil {
		return false
	}
	return ip.tr.Open(trace.KindStmt)
}

// closeScope ends a scope owned by the caller, handing the tracer the values
// that escape it.
func (ip *Interp) closeScope(v value.Value) {
	if ip.tr == nil {
		return
	}
	ip.tr.Close(liveTensors(nil, v))
	ip.checkTrace(0)
}

// liveTensors collects the tensors reachable from a value. It walks the
// containers a scope's result can be (a list of gradients, a record of weights)
// and stops at a closure, because a closure holds an environment rather than a
// value and nothing in this design puts a placeholder into one that outlives the
// statement that made it.
func liveTensors(dst []*tensor.Tensor, v value.Value) []*tensor.Tensor {
	switch t := v.(type) {
	case *tensor.Tensor:
		return append(dst, t)
	case *value.List:
		for _, it := range t.Items {
			dst = liveTensors(dst, it)
		}
	case *value.Tuple:
		// A tuple is a container like any other here. A tensor returned inside
		// one escapes the statement that made it exactly as a tensor in a list
		// does, and a scope that did not hear about it is the wrong-answer case
		// this function exists to prevent.
		for _, it := range t.Items {
			dst = liveTensors(dst, it)
		}
	case *value.Record:
		for _, k := range t.Keys {
			dst = liveTensors(dst, t.Fields[k])
		}
	case *value.Dict:
		for _, k := range t.Keys {
			dst = liveTensors(dst, t.Map[k])
		}
	case *value.Variant:
		if t.HasPayload {
			dst = liveTensors(dst, t.Payload)
		}
	}
	return dst
}

// tracedBuiltin maps a builtin's name to the opcode that records it. A builtin
// not in this table is one with no opcode, and by docs/CODEGEN.md section 2 it
// forces.
//
// The table is keyed on the name the *builtin* carries, not on the identifier at
// the call site, so a program that rebinds `exp` to something of its own gets
// its own and not this. Apply reads the name off the *value.Builtin it is about
// to call, which is the only place that distinction is available.
var tracedUnary = map[string]ir.Op{
	"relu":    ir.OpRelu,
	"exp":     ir.OpExp,
	"log":     ir.OpLog,
	"sin":     ir.OpSin,
	"cos":     ir.OpCos,
	"tanh":    ir.OpTanh,
	"sigmoid": ir.OpSigmoid,
	"sqrt":    ir.OpSqrt,
	"square":  ir.OpSquare,
	"neg":     ir.OpNeg,
}

var tracedBinary = map[string]ir.Op{
	"maximum": ir.OpMaximum,
	"minimum": ir.OpMinimum,
	"matmul":  ir.OpMatMul,
}

var tracedReduce = map[string]ir.Op{
	"sum":  ir.OpSum,
	"mean": ir.OpMean,
}

// traceBuiltin tries to record a builtin call instead of running it. It reports
// false for anything it does not handle, and the caller then forces and runs the
// builtin as it always did.
//
// Every case checks its operands before it records anything. A builtin with a
// non-tensor argument, a narrow dtype, a gradient-tracking input outside a grad
// scope, or an axis given as anything but a plain number is not traced; there is
// no half-traced state to unwind because nothing is appended until the checks
// pass.
func (ip *Interp) traceBuiltin(name string, args []value.Value) (value.Value, bool) {
	if ip.tr == nil || !ip.tr.Enabled() {
		return nil, false
	}
	ten := func(i int) (*tensor.Tensor, bool) {
		if i >= len(args) {
			return nil, false
		}
		return value.AsTensor(args[i])
	}
	switch {
	case len(args) == 1 && tracedUnary[name] != ir.OpInvalid:
		x, ok := ten(0)
		if !ok {
			return nil, false
		}
		return phv(ip.tr.Unary(tracedUnary[name], x))

	case len(args) == 1 && tracedReduce[name] != ir.OpInvalid:
		x, ok := ten(0)
		if !ok {
			return nil, false
		}
		return phv(ip.tr.Reduce(tracedReduce[name], x))

	case len(args) == 2 && (name == "sum" || name == "mean"):
		x, ok := ten(0)
		if !ok {
			return nil, false
		}
		ax, ok := value.AsNumber(args[1])
		if !ok {
			return nil, false
		}
		op := ir.OpSumAxis
		if name == "mean" {
			op = ir.OpMeanAxis
		}
		return phv(ip.tr.ReduceAxis(op, x, int(ax)))

	case len(args) == 2 && tracedBinary[name] != ir.OpInvalid:
		x, ok := ten(0)
		if !ok {
			return nil, false
		}
		y, ok := ten(1)
		if !ok {
			return nil, false
		}
		if name == "matmul" {
			return phv(ip.tr.MatMul(x, y))
		}
		return phv(ip.tr.Binary(tracedBinary[name], x, y))

	case name == "pow" && len(args) == 2:
		x, ok := ten(0)
		if !ok {
			return nil, false
		}
		p, ok := value.AsNumber(args[1])
		if !ok || len(shapeOf(args[1])) != 0 {
			return nil, false
		}
		return phv(ip.tr.PowScalar(x, p))

	case name == "clip" && len(args) == 3:
		x, ok := ten(0)
		if !ok {
			return nil, false
		}
		lo, ok1 := value.AsNumber(args[1])
		hi, ok2 := value.AsNumber(args[2])
		if !ok1 || !ok2 {
			return nil, false
		}
		return phv(ip.tr.Clip(x, lo, hi))

	case name == "where" && len(args) == 3:
		c, ok := ten(0)
		if !ok {
			return nil, false
		}
		x, ok := ten(1)
		if !ok {
			return nil, false
		}
		y, ok := ten(2)
		if !ok {
			return nil, false
		}
		return phv(ip.tr.Where(c, x, y))

	case name == "transpose" && len(args) == 1:
		x, ok := ten(0)
		if !ok || len(x.Shape) != 2 {
			return nil, false
		}
		return phv(ip.tr.Transpose(x, []int{1, 0}))

	case name == "reshape" && len(args) >= 2:
		x, ok := ten(0)
		if !ok {
			return nil, false
		}
		shape, ok := intsFromArgs(args[1:])
		if !ok {
			return nil, false
		}
		return phv(ip.tr.Reshape(x, shape))
	}
	return nil, false
}

// phv boxes a placeholder into a value.Value, keeping a refusal as a nil
// interface rather than a typed nil that every caller would then have to unpick.
func phv(t *tensor.Tensor, ok bool) (value.Value, bool) {
	if !ok || t == nil {
		return nil, false
	}
	return t, true
}

// shapeOf reports a value's tensor shape, or nil.
func shapeOf(v value.Value) []int {
	switch t := v.(type) {
	case value.Num:
		return []int{}
	case *tensor.Tensor:
		return t.Shape
	}
	return nil
}

// intsFromArgs reads a shape given either as separate numbers or as one list of
// them, which is how reshape is called in std/shapes.tw and in the examples.
func intsFromArgs(args []value.Value) ([]int, bool) {
	if len(args) == 1 {
		if l, ok := args[0].(*value.List); ok {
			out := make([]int, len(l.Items))
			for i, it := range l.Items {
				n, ok := value.AsNumber(it)
				if !ok {
					return nil, false
				}
				out[i] = int(n)
			}
			return out, true
		}
		if t, ok := args[0].(*tensor.Tensor); ok && len(t.Shape) == 1 {
			out := make([]int, len(t.Data))
			for i, x := range t.Data {
				out[i] = int(x)
			}
			return out, true
		}
	}
	out := make([]int, len(args))
	for i, a := range args {
		n, ok := value.AsNumber(a)
		if !ok || len(shapeOf(a)) != 0 {
			return nil, false
		}
		out[i] = int(n)
	}
	return out, true
}

// traceBinaryOp records one of twill's infix tensor operators.
func (ip *Interp) traceBinaryOp(op string, lt, rt *tensor.Tensor) (value.Value, bool) {
	if ip.tr == nil || !ip.tr.Enabled() {
		return nil, false
	}
	switch op {
	case "+":
		return phv(ip.tr.Binary(ir.OpAdd, lt, rt))
	case "-":
		return phv(ip.tr.Binary(ir.OpSub, lt, rt))
	case "*":
		return phv(ip.tr.Binary(ir.OpMul, lt, rt))
	case "/":
		return phv(ip.tr.Binary(ir.OpDiv, lt, rt))
	case "%":
		return phv(ip.tr.Binary(ir.OpMod, lt, rt))
	case "@":
		return phv(ip.tr.MatMul(lt, rt))
	case "^":
		if len(rt.Shape) != 0 || rt.Data == nil {
			return nil, false
		}
		return phv(ip.tr.PowScalar(lt, rt.Data[0]))
	}
	return nil, false
}

// TraceStats exposes the tracer's counters, for the harnesses that report what
// actually compiled rather than what was hoped would.
func (ip *Interp) TraceStats() trace.Stats {
	if ip.tr == nil {
		return trace.Stats{}
	}
	return ip.tr.Stats()
}

// SetTracing turns the tracer on or off for this interpreter. The differential
// harness runs the same program both ways and compares.
func (ip *Interp) SetTracing(on bool) {
	if ip.tr != nil {
		ip.tr.SetEnabled(on)
	}
}

// tracedStmt runs one statement's expression inside its own trace scope.
//
// The deferred Abandon is not belt and braces. `return`, `break`, `continue`
// and every runtime error in this interpreter are Go panics, so a statement can
// leave without reaching its close, and a placeholder that outlives its trace is
// a value with no data in it. Abandon replays, which patches everything and is
// always correct, and it does nothing at all if Close already ran.
func (ip *Interp) tracedStmt(f func() value.Value) value.Value {
	if !ip.stmtScope() {
		return f()
	}
	closed := false
	defer func() {
		if !closed {
			ip.tr.Abandon()
		}
	}()
	v := f()
	ip.closeScope(v)
	closed = true
	return v
}

// truthy evaluates a condition and forces before reading it. An `if` or `while`
// condition is docs/CODEGEN.md section 2's first named escape, and it is the one
// that draws the boundary around control flow: the tracer reaches the condition,
// needs the value, and forces.
func (ip *Interp) truthy(f func() value.Value) bool {
	return value.Truthy(ip.forced(ip.tracedStmt(f)))
}

// forced forces the open trace and hands the value back. Forcing patches
// placeholders in place, so the value the caller already holds is the value it
// gets: nothing is rebound and no caller has to know a trace existed.
func (ip *Interp) forced(v value.Value) value.Value {
	ip.escape()
	return v
}
