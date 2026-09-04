package trace

import (
	"fmt"
	"os"

	"github.com/twill-lang/twill/internal/ir"
	"github.com/twill-lang/twill/internal/tensor"
)

// strict makes every placeholder a graph output rather than only the ones the
// closing scope hands out. It exists to test the liveness rule rather than to
// argue for it: docs/CODEGEN.md's design does not say which values escape a
// scope, this implementation claims it is exactly the ones inside the scope's
// value, and TWILL_TRACE_STRICT=1 is the setting under which that claim costs
// nothing to be wrong about. If the corpus produces identical output at both
// settings, the claim held on the corpus.
var strict = os.Getenv("TWILL_TRACE_STRICT") == "1"

// Err reports a failure that left the trace in a state the interpreter must not
// continue from. It should be unreachable: every constructor pre-checks the
// shapes the builder would have rejected. It is returned rather than ignored
// because a placeholder with no data is not something to carry on past.
func (t *Tracer) Err() error { return t.err }

// finish closes the builder into a graph with the given outputs.
func (t *Tracer) finish(outs []ir.Ref) (*ir.Graph, error) {
	for _, r := range outs {
		t.b.Output(r)
	}
	return t.b.Finish()
}

// replay runs the trace through internal/tensor, node by node, and patches every
// placeholder in place.
//
// This is the fallback in every sense: it is what runs when a value escapes
// mid-scope, when the graph will not compile, and when there is no C compiler on
// the machine. It calls exactly the functions the interpreter would have called,
// in exactly the order it would have called them, so the answer is the
// interpreter's answer and not an approximation of it.
//
// The one thing it does that a plain ir.Eval does not is preserve identity: the
// value computed for a node is written into the placeholder object the
// interpreter is already holding, and that object is what later nodes take as
// their operand. Autodiff is why. A placeholder that the interpreter kept a
// reference to has to be the same *tensor.Tensor that appears in the parent
// chain, or a cotangent arriving at one of them would not reach the other and
// the gradient would be quietly short by a term.
func (t *Tracer) replay() {
	if len(t.order) == 0 {
		return
	}
	t.stats.Replayed++
	// Every node now open is about to be evaluated: the graph is finished with
	// no outputs, and ir.Eval walks nodes rather than outputs, so there is no
	// liveness to skip anything by.
	t.stats.Computed += len(t.order)
	g, err := t.finish(nil)
	if err != nil {
		t.fail(err)
		return
	}
	sub := make(map[int]*tensor.Tensor, len(t.order))
	for i, ph := range t.order {
		sub[int(t.phRef[i])] = ph
	}
	_, err = ir.EvalNodesWith(g, t.params, func(i int, v *tensor.Tensor) *tensor.Tensor {
		if ph, ok := sub[i]; ok && ph != v {
			tensor.Adopt(ph, v)
			return ph
		}
		return v
	})
	if err != nil {
		t.fail(err)
		return
	}
	t.verify("replay")
}

func (t *Tracer) fail(err error) {
	if t.err == nil {
		t.err = fmt.Errorf("trace: %w", err)
	}
	// Leave nothing readable behind: a placeholder with no data panics on the
	// first read, which is loud. A placeholder filled with plausible zeros would
	// be a wrong answer, which is worse than any crash.
}

// compileAndRun is the fast path. It runs only when a scope closes, because that
// is the only moment liveness is exact.
func (t *Tracer) compileAndRun(live []*tensor.Tensor) bool {
	outRefs, outPH := t.liveRefs(live)
	if len(outRefs) == 0 {
		// Nothing escapes. There is nothing to compute and nothing to patch.
		return true
	}
	g, err := t.finish(outRefs)
	if err != nil {
		t.fail(err)
		return true
	}
	prog, ok := t.cache.Get(g, &t.stats)
	if !ok {
		return false
	}
	outs, err := prog.Run(t.params)
	if err != nil {
		return false
	}
	for k, ph := range outPH {
		ph.Data = outs[k].Data
		ph.Shape = outs[k].Shape
	}
	// The program ran, and the program is the whole graph: ir.Fuse gives every
	// unabsorbed node a region and codegen emits every region, so a node that no
	// output reads is still computed once the graph is compiled at all. The
	// deletion happened above, in the len(outRefs) == 0 return, or not at all.
	t.stats.Computed += len(t.order)
	t.stats.Compiled++
	return true
}

// liveRefs turns the values a scope hands out into graph outputs, deduplicated
// and in node order.
func (t *Tracer) liveRefs(live []*tensor.Tensor) ([]ir.Ref, []*tensor.Tensor) {
	if strict {
		live = t.order
	}
	seen := make(map[ir.Ref]bool, len(live))
	var refs []ir.Ref
	var phs []*tensor.Tensor
	for _, v := range live {
		r, ok := t.refs[v]
		if !ok || v.Data != nil || seen[r] {
			continue
		}
		seen[r] = true
		refs = append(refs, r)
		phs = append(phs, v)
	}
	return refs, phs
}

// CloseGrad ends a KindGrad scope by differentiating the trace instead of the
// tensors.
//
// docs/CODEGEN.md section 5 is the argument: a fused kernel creates none of the
// per-operation closures the interpreter's autodiff is made of, so the
// derivative has to come from a transform of the graph. ir.Grad is that
// transform and it already exists; this is the part that hands it a graph
// traced from a real .tw program and puts the answers back where the
// interpreter expects to find them, in each leaf's Grad.
//
// It reports false when the trace did not survive: the result was not a
// placeholder because something escaped, or the graph would not compile. The
// caller then runs tensor.Backward over the real tensors replay left behind,
// which is the interpreter's own path and needs nothing from here.
func (t *Tracer) CloseGrad(result *tensor.Tensor) (value float64, ok bool) {
	if !t.closing() {
		return 0, false
	}
	defer func() {
		t.reset()
		t.pop()
	}()
	if len(t.order) == 0 || t.err != nil {
		return 0, false
	}
	t.stats.Traced++
	r, isPH := t.refs[result]
	if !isPH || result.Data != nil || len(result.Shape) != 0 {
		t.replay()
		t.stats.GradSlow++
		return 0, false
	}
	fwd, err := t.finish([]ir.Ref{r})
	if err != nil {
		t.fail(err)
		return 0, false
	}
	gg, err := ir.Grad(fwd)
	if err != nil {
		t.stats.GradSlow++
		t.replayGraph(fwd)
		return 0, false
	}
	prog, hit := t.cache.Get(gg, &t.stats)
	if !hit {
		t.stats.GradSlow++
		t.replayGraph(fwd)
		return 0, false
	}
	args := append(append([]*tensor.Tensor(nil), t.params...), tensor.Scalar(1))
	outs, err := prog.Run(args)
	if err != nil {
		t.stats.GradSlow++
		t.replayGraph(fwd)
		return 0, false
	}
	// outs[0] is the forward value; outs[1+i] is d(value)/d(param i).
	for i, p := range t.params {
		if !p.RequiresGrad {
			continue
		}
		gr := outs[1+i]
		if p.Grad == nil {
			p.Grad = make([]float64, len(p.Data))
		}
		for k := range p.Grad {
			p.Grad[k] += gr.Data[k]
		}
	}
	// The result placeholder is still in the interpreter's hands.
	result.Data = outs[0].Data
	result.Shape = outs[0].Shape
	t.stats.Computed += len(t.order)
	t.stats.Compiled++
	t.stats.GradFast++
	return outs[0].Data[0], true
}

// replayGraph replays an already-finished graph, for the paths in CloseGrad that
// have consumed the builder before deciding to fall back.
func (t *Tracer) replayGraph(g *ir.Graph) {
	t.stats.Replayed++
	t.stats.Computed += len(t.order)
	sub := make(map[int]*tensor.Tensor, len(t.order))
	for i, ph := range t.order {
		sub[int(t.phRef[i])] = ph
	}
	_, err := ir.EvalNodesWith(g, t.params, func(i int, v *tensor.Tensor) *tensor.Tensor {
		if ph, ok := sub[i]; ok && ph != v {
			tensor.Adopt(ph, v)
			return ph
		}
		return v
	})
	if err != nil {
		t.fail(err)
	}
}

// OpenGrad starts a KindGrad scope with the differentiated leaves already
// registered as graph parameters. Registering them up front is what lets the
// tracer follow a value that requires gradients at all: everywhere else a
// gradient-tracking tensor is refused, because a compiled kernel builds no
// backward closure and the interpreter's autodiff would silently come back
// zero.
func (t *Tracer) OpenGrad(leaves []*tensor.Tensor) bool {
	if !t.Enabled() || t.susp > 0 || t.err != nil || len(leaves) == 0 {
		return false
	}
	for _, l := range leaves {
		if l.DType() != tensor.DTF64 || l.Data == nil {
			return false
		}
	}
	t.stats.Scopes++
	t.push()
	t.b = ir.NewBuilder()
	t.refs = make(map[*tensor.Tensor]ir.Ref, 16)
	t.byRef = make(map[ir.Ref]*tensor.Tensor, 16)
	t.kind = KindGrad
	t.broken = false
	for i, l := range leaves {
		if _, dup := t.refs[l]; dup {
			continue
		}
		r := t.b.Param("p"+itoa(i), l.Shape)
		t.params = append(t.params, l)
		t.refs[l] = r
		t.byRef[r] = l
	}
	return true
}

// AbandonGrad unwinds a grad scope that never reached its close, which is what a
// panic through the differentiated body leaves behind.
func (t *Tracer) AbandonGrad() {
	t.replay()
	t.reset()
	t.pop()
}

// frame is one suspended trace. The stack exists for exactly one situation and
// it is the situation that matters: `let delta = grad(f)(x)` opens a statement
// scope, and the grad scope inside it has to own a trace of its own, because the
// whole point is to hand ir.Grad one graph for the whole function body rather
// than a graph per `let` inside it.
//
// Pushing escapes the outer trace first, so every value the outer statement was
// still holding is real before the inner one starts. That is why the outer frame
// restores as an empty builder and why nothing has to be re-keyed: the two traces
// never share a node.
type frame struct {
	b      *ir.Builder
	refs   map[*tensor.Tensor]ir.Ref
	byRef  map[ir.Ref]*tensor.Tensor
	order  []*tensor.Tensor
	phRef  []ir.Ref
	params []*tensor.Tensor
	kind   Kind
	depth  int
}

func (t *Tracer) push() {
	t.Escape()
	t.stack = append(t.stack, frame{
		b: t.b, refs: t.refs, byRef: t.byRef,
		order:  append([]*tensor.Tensor(nil), t.order...),
		phRef:  append([]ir.Ref(nil), t.phRef...),
		params: append([]*tensor.Tensor(nil), t.params...),
		kind:   t.kind, depth: t.depth,
	})
	t.b = nil
	t.refs = nil
	t.byRef = nil
	t.order = nil
	t.phRef = nil
	t.params = nil
	t.depth = 0
}

func (t *Tracer) pop() {
	if len(t.stack) == 0 {
		return
	}
	f := t.stack[len(t.stack)-1]
	t.stack = t.stack[:len(t.stack)-1]
	t.b, t.refs, t.byRef = f.b, f.refs, f.byRef
	t.order, t.phRef, t.params = f.order, f.phRef, f.params
	t.kind, t.depth = f.kind, f.depth
}
