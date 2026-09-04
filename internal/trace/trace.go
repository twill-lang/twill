// Package trace is twill's front end for the compiler: the layer that turns a
// running interpreter into an IR graph, and turns that graph back into values.
//
// docs/CODEGEN.md section 2 says why it is a tracer and not a lowering pass.
// twill's checker is best-effort by design and cannot type zeros(2, len(xs)),
// so a compiler that needed static shapes would refuse a large fraction of real
// programs. A tracer does not need them: it runs the interpreter as it is, and
// every operand it sees is a concrete value with a concrete shape. What it
// changes is that a traced operation appends an ir.Node and returns a
// placeholder instead of calling internal/tensor straight away.
//
// # The placeholder
//
// A placeholder is a *tensor.Tensor with the right Shape and no Data. It is the
// same Go type as a real tensor because every value in the interpreter is
// value.Value and the boundary between compiled and interpreted has to be a
// runtime question, not a static one. Identity is by pointer: the tracer keeps a
// map from placeholder to ir.Ref, so a value the interpreter passed around,
// bound to a name, and handed back into another operation is still the same
// node.
//
// Nothing outside this package may read a placeholder's Data. That is not a
// convention, it is the invariant the whole design rests on, and Section
// "Forcing" is how it is kept.
//
// # Forcing, and the two ways it happens
//
// A trace is forced when a value escapes it. There are exactly two forms, and
// the difference between them is the whole of the liveness problem.
//
//   - Closing a scope. The interpreter opens a scope, evaluates, and closes it
//     with the value the scope produced. At that moment liveness is exact and
//     needs no analysis: the only placeholders that can still be read are the
//     ones inside the value being handed out, because every Go frame that held
//     anything else has returned. Those become the graph's outputs, everything
//     else is dead, and the fusion pass is free to give the dead values no
//     memory at all. This is the fast path, and it is the only path that
//     compiles.
//
//   - An escape in the middle of a scope: print, an if condition, a comparison,
//     save, a builtin with no opcode, a closure the tracer cannot follow. Here
//     liveness is not known, because a placeholder may be sitting in a Go local
//     three frames up. So this path does not try to know it: it replays the
//     whole trace through internal/tensor, materialising every node, and patches
//     every placeholder in place. That is exactly what the interpreter would
//     have done, node for node, so it is correct by construction and it is also
//     slower than the fast path by the cost of having built a graph. It is the
//     fallback, and it is what makes "if a program cannot compile it must still
//     be right" true rather than hoped for.
//
// Replay is also the fallback when there is no C compiler on the machine, when
// the emitter refuses a graph, and when a dtype is not f64. Every one of those
// ends in the same place: run the interpreter's own functions and get the
// interpreter's own answer.
//
// # Control flow
//
// Not captured, per docs/CODEGEN.md section 2. `for` and `while` are interpreter
// constructs over value.Value. A loop body opens a scope per statement, forces
// at the end of it, and the next iteration traces the same statement again and
// hits the compiled-kernel cache. That cache is what makes the design pay: a
// training loop runs the same trace thousands of times.
package trace

import (
	"os"
	"strconv"

	"github.com/twill-lang/twill/internal/ir"
	"github.com/twill-lang/twill/internal/tensor"
)

// Kind distinguishes the two scope shapes the interpreter opens.
type Kind uint8

const (
	// KindStmt is one statement. It closes with the statement's value.
	KindStmt Kind = iota
	// KindGrad is the body of a grad/grads/value_and_grad call. It closes with
	// the body's result and differentiates the graph rather than the tensors.
	KindGrad
)

// Tracer holds the trace currently being built. There is one per interpreter.
// At most one trace is open at a time: a statement scope opened inside an
// already-open trace is a no-op marker, so the grad scope keeps the whole
// function body in one graph rather than losing it to the `let`s inside.
type Tracer struct {
	on    bool
	cache *Cache

	// The open trace, or nil.
	b      *ir.Builder
	refs   map[*tensor.Tensor]ir.Ref
	byRef  map[ir.Ref]*tensor.Tensor
	order  []*tensor.Tensor // placeholders, in creation order
	phRef  []ir.Ref         // the node each placeholder stands for
	params []*tensor.Tensor // real inputs, in ir param order
	kind   Kind
	depth  int  // nested scope markers
	broken bool // the builder failed; the trace can only be replayed
	susp   int  // tracing suspended (hessian's forward-mode path)
	err    error
	stack  []frame

	stats Stats
}

// Stats counts what happened, so the report can be a measurement rather than a
// claim. Nothing here is inferred.
type Stats struct {
	Scopes    int // scopes opened
	Traced    int // scopes that produced at least one traced node
	Compiled  int // scopes closed by running compiled code
	Replayed  int // forces that fell back to replay
	Escapes   int // mid-scope escapes
	CacheHit  int
	CacheMiss int
	Nodes     int // total traced nodes
	Computed  int // traced nodes whose arithmetic was actually performed
	GradFast  int // grad scopes that ran the compiled forward+backward graph
	GradSlow  int // grad scopes that fell back to tensor.Backward
}

// Computed is the counter to read when the question is how much work a program
// did, and it is deliberately not Compiled or Replayed. Those two count events:
// a scope that closed by running compiled code, and a force that fell back to
// replay. One of either can stand for two thousand multiplications or for none.
// Computed counts the same population Nodes does -- traced operations, one per
// place() -- and rises only when those operations are handed to something that
// evaluates them.
//
// It is exact rather than estimated, because a traced node reaches exactly one
// of three fates and each is counted at the moment it happens:
//
//   - It is in the trace when a force runs it, in which case every node then
//     open is computed. Both evaluators are all-or-nothing over the nodes they
//     are given: ir.Eval walks every node in the graph it is handed, and the
//     backend emits a region for every node in the plan (internal/ir/fuse.go
//     gives every unabsorbed node a region, and internal/codegen/emit.go emits
//     every region), so neither skips a node for being unread. That is what
//     TestEveryNodeHandedToAnEvaluatorIsComputed pins.
//   - It is live at a scope close, which is the case above.
//   - It is dead at a scope close, in which case compileAndRun finds no outputs,
//     returns before compiling anything, and the node is never computed.
//
// The trace is emptied at every force and at every reset, so no node is counted
// twice and none is counted before its arithmetic ran.

// Stats returns a copy of the counters.
func (t *Tracer) Stats() Stats { return t.stats }

// Enabled reports whether tracing is on.
func (t *Tracer) Enabled() bool { return t != nil && t.on }

// New builds a tracer. TWILL_TRACE turns it on or off, and it is off unless
// asked for.
//
// It is correct, and on the programs measured in docs/CODEGEN.md section 11 it
// is between 1.2 and 2.5 times slower than the interpreter, because almost every
// operation in a training program touches a tensor that tracks gradients and so
// refuses to trace. Defaulting to on would make every twill program slower to
// buy a compiled path most of them never reach. It goes on by default when
// section 11.2.3's fix lands and the numbers say it should.
func New(cache *Cache) *Tracer {
	on := false
	if v := os.Getenv("TWILL_TRACE"); v != "" {
		b, err := strconv.ParseBool(v)
		on = err == nil && b
	}
	if cache == nil {
		cache = NewCache()
	}
	return &Tracer{on: on, cache: cache}
}

// SetEnabled forces tracing on or off, for tests.
func (t *Tracer) SetEnabled(on bool) { t.on = on }

// Open starts a scope. It returns true if this call owns the trace and must
// therefore be the one to Close it; a nested scope returns false and does
// nothing, because the outer trace is the one that will be forced.
func (t *Tracer) Open(k Kind) bool {
	if !t.Enabled() || t.susp > 0 {
		return false
	}
	t.stats.Scopes++
	if t.b != nil {
		// A trace is already open, so this scope is not the one that will force
		// it. Nothing is recorded here: the outer owner closes, and until it does
		// the inner statements go on appending to its graph.
		return false
	}
	t.b = ir.NewBuilder()
	t.refs = make(map[*tensor.Tensor]ir.Ref, 16)
	t.byRef = make(map[ir.Ref]*tensor.Tensor, 16)
	t.order = t.order[:0]
	t.phRef = t.phRef[:0]
	t.params = t.params[:0]
	t.kind = k
	t.broken = false
	return true
}

// Abandon closes the owning scope by replaying it. The interpreter defers this
// on every scope it owns, so a panic unwinding through a scope (a `return`, a
// `break`, a runtime error) cannot leave a placeholder unpatched.
func (t *Tracer) Abandon() {
	if t.b == nil {
		return
	}
	t.replay()
	t.reset()
}

// Escape forces the whole open trace by replay. Every escape point in the
// interpreter calls this before it reads anything.
func (t *Tracer) Escape() {
	if t == nil || t.b == nil || len(t.order) == 0 {
		return
	}
	t.stats.Escapes++
	t.replay()
	// The trace is emptied but the scope stays open, so the statement can go on
	// tracing after the escape.
	t.b = ir.NewBuilder()
	t.refs = make(map[*tensor.Tensor]ir.Ref, 16)
	t.byRef = make(map[ir.Ref]*tensor.Tensor, 16)
	t.order = t.order[:0]
	t.phRef = t.phRef[:0]
	t.params = t.params[:0]
	t.broken = false
}

func (t *Tracer) reset() {
	t.b = nil
	t.refs = nil
	t.byRef = nil
	t.order = t.order[:0]
	t.phRef = t.phRef[:0]
	t.params = t.params[:0]
	t.depth = 0
	t.broken = false
}

// closeScope ends a nested marker, or reports that this is the owning close.
func (t *Tracer) closing() bool { return t.b != nil }

// Close ends the owning scope. live names the placeholders that escape it, in
// the order the caller wants them; everything else in the trace is dead and the
// fusion pass may give it no memory. The values are patched in place, so the
// caller's own references become real tensors and nothing has to be rebound.
func (t *Tracer) Close(live []*tensor.Tensor) {
	if !t.closing() {
		return
	}
	defer t.reset()
	if len(t.order) == 0 {
		return
	}
	t.stats.Traced++
	if t.broken || !t.compileAndRun(live) {
		t.replay()
	}
}

// -------------------------------------------------------------------------
// Building the trace
// -------------------------------------------------------------------------

// operand returns the ir.Ref for a tensor, registering a real tensor as a graph
// parameter the first time it is seen.
//
// It refuses a tensor that requires gradients, because a compiled kernel
// produces no backward closures and the interpreter's autodiff would silently
// get a zero. In a KindGrad scope the differentiated inputs are registered as
// parameters before the body runs, so they are already in the map and never
// reach this test.
func (t *Tracer) operand(x *tensor.Tensor) (ir.Ref, bool) {
	if r, ok := t.refs[x]; ok {
		return r, true
	}
	if x.RequiresGrad || x.DType() != tensor.DTF64 || x.Data == nil {
		return 0, false
	}
	r := t.b.Param("p"+strconv.Itoa(len(t.params)), x.Shape)
	t.params = append(t.params, x)
	t.refs[x] = r
	t.byRef[r] = x
	return r, true
}

// place records a new node and returns the placeholder standing for it.
func (t *Tracer) place(r ir.Ref) *tensor.Tensor {
	if t.b.Err() != nil {
		t.broken = true
	}
	// The builder folds a no-op: BroadcastTo to the same shape returns its
	// operand's ref rather than a new node. Handing back a second placeholder for
	// one node would split the value's identity in two, and a cotangent arriving
	// at one of them would never reach the other.
	if existing, ok := t.byRef[r]; ok {
		return existing
	}
	ph := &tensor.Tensor{Shape: append([]int(nil), t.b.Shape(r)...)}
	t.refs[ph] = r
	t.byRef[r] = ph
	t.order = append(t.order, ph)
	t.phRef = append(t.phRef, r)
	t.stats.Nodes++
	return ph
}

func (t *Tracer) ready() bool {
	return t != nil && t.on && t.susp == 0 && t.b != nil && !t.broken && t.err == nil
}

// Suspend turns tracing off until the matching Resume. hessian runs forward-mode
// 2-jets through its own per-node closures (internal/tensor/jet.go), and a
// traced operation would build no jet at all, so the second derivative would
// come back as whatever an unrecorded node contributes, which is nothing.
// docs/CODEGEN.md section 5 says hessian does not compile in the first version;
// this is that sentence, enforced.
func (t *Tracer) Suspend() {
	if t == nil {
		return
	}
	t.Escape()
	t.susp++
}

// Resume undoes a Suspend.
func (t *Tracer) Resume() {
	if t != nil && t.susp > 0 {
		t.susp--
	}
}

// matmulOK reports whether the IR builder would accept these operand shapes,
// checked here so the builder never fails and a half-built trace never has to be
// unwound.
func matmulOK(as, bs []int) bool {
	a2, b2 := as, bs
	if len(as) == 1 {
		a2 = []int{1, as[0]}
	}
	if len(bs) == 1 {
		b2 = []int{bs[0], 1}
	}
	return len(a2) == 2 && len(b2) == 2 && a2[1] == b2[0]
}

func itoa(i int) string { return strconv.Itoa(i) }

// Unary traces a one-operand elementwise op.
func (t *Tracer) Unary(op ir.Op, x *tensor.Tensor) (*tensor.Tensor, bool) {
	if !t.ready() {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	return t.place(t.b.Unary(op, rx)), !t.broken
}

// Binary traces a two-operand broadcasting elementwise op.
func (t *Tracer) Binary(op ir.Op, x, y *tensor.Tensor) (*tensor.Tensor, bool) {
	if !t.ready() {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	ry, ok := t.operand(y)
	if !ok {
		return nil, false
	}
	if _, ok := ir.BroadcastShape(x.Shape, y.Shape); !ok {
		return nil, false
	}
	return t.place(t.b.Binary(op, rx, ry)), !t.broken
}

// PowScalar traces x ** p.
func (t *Tracer) PowScalar(x *tensor.Tensor, p float64) (*tensor.Tensor, bool) {
	if !t.ready() {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	return t.place(t.b.PowScalar(rx, p)), !t.broken
}

// Clip traces clip(x, lo, hi).
func (t *Tracer) Clip(x *tensor.Tensor, lo, hi float64) (*tensor.Tensor, bool) {
	if !t.ready() {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	return t.place(t.b.Clip(rx, lo, hi)), !t.broken
}

// Where traces where(cond, a, b).
func (t *Tracer) Where(c, x, y *tensor.Tensor) (*tensor.Tensor, bool) {
	if !t.ready() {
		return nil, false
	}
	rc, ok := t.operand(c)
	if !ok {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	ry, ok := t.operand(y)
	if !ok {
		return nil, false
	}
	s1, ok := ir.BroadcastShape(c.Shape, x.Shape)
	if !ok {
		return nil, false
	}
	if _, ok := ir.BroadcastShape(s1, y.Shape); !ok {
		return nil, false
	}
	return t.place(t.b.Where(rc, rx, ry)), !t.broken
}

// Reduce traces sum/mean over every element.
func (t *Tracer) Reduce(op ir.Op, x *tensor.Tensor) (*tensor.Tensor, bool) {
	if !t.ready() {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	switch op {
	case ir.OpSum:
		return t.place(t.b.Sum(rx)), !t.broken
	case ir.OpMean:
		return t.place(t.b.Mean(rx)), !t.broken
	}
	return nil, false
}

// ReduceAxis traces sum/mean along one axis.
func (t *Tracer) ReduceAxis(op ir.Op, x *tensor.Tensor, axis int) (*tensor.Tensor, bool) {
	if !t.ready() {
		return nil, false
	}
	if axis < 0 {
		axis += len(x.Shape)
	}
	if axis < 0 || axis >= len(x.Shape) {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	switch op {
	case ir.OpSumAxis:
		return t.place(t.b.SumAxis(rx, axis)), !t.broken
	case ir.OpMeanAxis:
		return t.place(t.b.MeanAxis(rx, axis)), !t.broken
	}
	return nil, false
}

// MatMul traces x @ y.
func (t *Tracer) MatMul(x, y *tensor.Tensor) (*tensor.Tensor, bool) {
	if !t.ready() {
		return nil, false
	}
	if !matmulOK(x.Shape, y.Shape) {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	ry, ok := t.operand(y)
	if !ok {
		return nil, false
	}
	return t.place(t.b.MatMul(rx, ry)), !t.broken
}

// Reshape traces a reshape.
func (t *Tracer) Reshape(x *tensor.Tensor, shape []int) (*tensor.Tensor, bool) {
	if !t.ready() || ir.Numel(x.Shape) != ir.Numel(shape) {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	return t.place(t.b.Reshape(rx, shape)), !t.broken
}

// Transpose traces an axis permutation.
func (t *Tracer) Transpose(x *tensor.Tensor, perm []int) (*tensor.Tensor, bool) {
	if !t.ready() || len(perm) != len(x.Shape) {
		return nil, false
	}
	rx, ok := t.operand(x)
	if !ok {
		return nil, false
	}
	return t.place(t.b.Transpose(rx, perm)), !t.broken
}
