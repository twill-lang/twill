# Codegen: from tree walking to emitted GPU kernels

This was a design when it was written. Phases 1 through 3 of section 9 are now
built, and section 10 records what was built, what was measured, and where the
implementation departed from the design. Sections 1 through 9 are left as they
were written, before the work, so the design can be read against the outcome
rather than edited to match it.

## What this document is, and what it is not

`docs/gpu-feasibility.md` measured whether a GPU backend is worth building and
answered not yet. `docs/gpu.md` designed the backend that would exist when the
answer changes: which API, how buffers become resident, how the tape holds
device tensors, which kernels get written. Both are about the *runtime*, and both
assume the shape twill has today, which is one kernel per operation, dispatched
as the interpreter reaches it.

This document is about the *compiler*, and it exists because op-at-a-time
dispatch cannot pay for itself on this codebase. The number that decides it is
from `docs/gpu-feasibility.md`: the round trip costs roughly 80 microseconds per
operation and does not shrink with problem size. twill's own headline program,
`examples/montecarlo_option.tw`, is about eight elementwise operations over
200,000 elements followed by a reduction. Dispatched one at a time that is
roughly 640 microseconds of pure boundary crossing, against a measured 3.450 ms
for the whole thing on the CPU today (`docs/BENCHMARKS.md`). A backend that
launches per operation spends a fifth of the current runtime on overhead before
computing anything, and that is before the transfers.

The way out is to launch once for the whole chain, and launching once for a
chain means knowing what the chain is before running it. twill does not know
that today. Getting to know it is the subject of this document.

Read `docs/gpu.md` for the runtime. This is the layer above it.

---

## 1. What twill actually looks like today

Everything below is a consequence of these five facts, all of which were read out
of the code rather than assumed.

**There is no intermediate representation.** `internal/interp/interp.go` walks
`ast.Expr` directly. `evalBinary` takes an `*ast.Binary`, evaluates both sides
to `value.Value`, and switches on the operator string into a `tensor` package
function. An operation is discovered, executed and forgotten in one step. There
is no object representing the program's computation, which means there is
currently nothing for a compiler to consume, nothing to fuse over, and nothing
to cache a compiled kernel against. **Introducing an IR is the whole of phase one
and most of the risk.**

**A value is `any`.** `value.Value` is an empty interface; the numeric case is
`*tensor.Tensor` and the others are `value.Num`, `*value.List`, `*value.Record`,
`*value.Closure` and the rest. So the compiler cannot assume its inputs are
tensors, and the boundary between the compiled and interpreted worlds is a type
switch rather than a static property.

**A tensor is a flat `[]float64` plus a `[]int` shape, always.** From
`internal/tensor/tensor.go`. Narrow dtypes are a tag (`dtp`) and a set of
rounding rules (`docs/dtypes.md`); the storage is f64 whatever the tag says.
This matters enormously here: `docs/gpu-feasibility.md` measured 341.7 GFLOP/s
f64 against 18,010 GFLOP/s f32 on the RTX 5070, a ratio of 52.7 to 1. **Compiling
f64 kernels targets 5% of the card.** The packed layout is NEEDS-111 and it is a
hard prerequisite, not an optimisation to follow later.

**The autodiff tape is the tensor graph.** There is no tape object. Each output
`*Tensor` holds its parents inline (`p0`, `p1`, and `pRest` for einsum and
concat) and a `backward func()` closure, wired by `track1`/`track2`/`trackN` and
only when an input has `RequiresGrad`. `Backward()` walks those pointers
depth-first for a topological order and calls the closures in reverse. **A fused
forward kernel therefore destroys the backward pass unless the fusion emits a
matching fused backward kernel**, because the per-operation closures that
currently carry the derivatives will not exist. This is the hardest part of the
design and section 5 is about it.

**Parallelism is deterministic by construction.** `parallelFor` and `runChunks`
in `internal/tensor/parallel.go` split an output range into contiguous chunks
whose bodies write only their own indices, and `parallelSum` adds in fixed
4096-element blocks combined in block order, so the answer does not depend on
the core count. `docs/DECISIONS.md` entry 6 explains why this is load-bearing
and `docs/gpu.md` section 5 carries it onto the device. **The compiler inherits
the constraint: no emitted kernel may reorder a reduction.**

---

## 2. The IR

One IR, sitting between the AST and the kernels, built by the interpreter as it
evaluates rather than by a separate pass.

### Tracing, not parsing

The obvious design is a lowering pass from `ast` to the IR. It is the wrong one
here, because twill's shapes are not all statically known. The checker is
best-effort by design (`docs/DECISIONS.md` entry 3): it types `zeros(2, 3)`
precisely and `zeros(2, len(xs))` as unknown, and `docs/CORRECTNESS.md` shows a
six-line program whose shapes it cannot determine. A compiler that needs static
shapes to emit a kernel would refuse a large fraction of real programs.

So the IR is built by tracing. The interpreter runs as it does now, but instead
of calling `tensor.Add` immediately, a traced operation appends a node to a
buffer and returns a placeholder tensor carrying its shape. Shapes are concrete
because the operands are concrete values, not inferred types. When the trace has
to be forced, it is compiled and run.

This is how JAX gets its jaxprs and it is the right shape for twill for the same
reason: the language is dynamic, the values are not.

### The node

A trace node is deliberately narrow:

```
Node {
    Op      OpCode      // Add, Mul, Exp, Relu, MatMul, Sum, SumAxis, ...
    Inputs  []NodeID    // indices into the trace, or NegN for a captured buffer
    Shape   []int       // concrete, from the operand values
    DType   DType       // the existing tensor.DType
    Attrs   [2]int      // axis, k, and other small integer parameters
}
```

`OpCode` enumerates exactly the operators in `internal/tensor`, and the list is
not invented here: `TestGradientCheckCoversEveryOperator` in
`internal/tensor/fullgradcheck_test.go` already parses the package's source and
enumerates every exported operator, so the same list can generate the opcode
enum and a missing entry becomes a build failure rather than a silent fallback.

### Forcing

A trace is forced when a value escapes it: when a tensor's data is read by
`print`, by an `if` condition, by a comparison, by `save`, by any builtin with no
opcode, or when the traced program calls a closure the tracer cannot follow.
Forcing compiles the trace, runs it, and replaces the placeholders with real
tensors.

Control flow is the boundary. twill's `for` and `while` are interpreter
constructs over `value.Value`, and the tracer does not attempt to capture them:
a loop body traces, forces at the end of each iteration, and the next iteration
traces again against the same cache key. That is exactly what makes the compiled
kernel cache pay, since a training loop runs the same trace thousands of times.

---

## 3. The fusion strategy

Fusion is the point of this design, not a refinement of it. The unit of fusion
is the largest region of the trace that can be computed by one kernel launch.

### The three classes

Every operator in `internal/tensor` falls into one of three classes, and the
class determines how it fuses.

**Elementwise.** Every operation built on `broadcastBinary` or `unary`:
add, sub, mul, div, mod, neg, square, exp, log, sqrt, sin, cos, tanh, sigmoid,
relu, pow, clip, maximum, minimum, where, and the comparisons. Each output
element depends on one element of each input (after broadcasting). **Any
connected chain of these fuses into a single kernel with one loop over the output
index.** This is where the win is: the Monte Carlo pricer's whole forward pass
up to the reduction is one such chain.

**Reductions and scans.** sum, mean, max, min, prod, median, their axis
variants, softmax, logsumexp, cumsum, cumprod, cummax, cummin. **An elementwise
chain fuses *into* the producer side of a reduction** (compute the element, then
accumulate it, never materialising the intermediate), which is what turns
`mean(relu(ST - K))` into one pass. A reduction does not fuse into another
reduction, and nothing fuses across a reduction's output back into more
elementwise work in the same kernel, because the reduction needs a barrier.
Softmax and logsumexp are internally two reductions and a broadcast and get
hand-written fused kernels rather than being decomposed.

**Contractions and structural operators.** matmul, matmulNT, einsum, conv2d,
maxpool2d, gather, concat, split, reshape, transpose, broadcast_to, flip, roll,
diff, slice, sort, topk. These get their own kernels. A contraction is a tuned
kernel and fusing arbitrary work into it destroys the tuning; what fuses is the
*epilogue*, one elementwise chain applied to the contraction's output before it
is written, which is where `relu(linear(x, W) + b)` collapses from three kernels
to one. Reshape, transpose and broadcast_to are pure index arithmetic and fuse
into a consumer as an index remapping rather than as a kernel at all, which is
worth having on its own evidence: `docs/perf-baseline.md` measured
`TransposePerm` at 14% of a forward pass, all of it spent materialising copies.

### The algorithm

Walk the trace in order and grow regions greedily. A node joins the current
region when its class permits (elementwise into elementwise, elementwise into a
reduction's producer, elementwise into a contraction's epilogue), when its shape
is broadcast-compatible with the region's output shape, and when it has no
consumer outside the region. That last condition is the one that matters: a value
consumed twice must be materialised, or the fused kernel computes it twice.
Recomputing is often cheaper than a round trip to memory, so the rule is a cost
comparison and not a prohibition, but the first implementation should
materialise, because it is the version that is obviously correct.

Greedy is the right first algorithm. It is what XLA's fusion started as, the
regions it finds on the workloads in `bench/workloads` are the ones a person
would pick by hand, and it can be replaced without touching anything else.

---

## 4. Memory layout

### The packed buffer comes first

Restating the prerequisite because the design does not work without it. Today a
tensor is `[]float64` regardless of its dtype tag. Three consequences:

- f64 on the target card is 5% of its throughput
  (`docs/gpu-feasibility.md`), so a compiler emitting f64 kernels is optimising
  the wrong 5%.
- Every element is eight bytes across the PCIe boundary, doubling the transfer
  cost the fusion exists to amortise.
- `docs/BENCHMARKS.md` shows the elementwise workloads are bandwidth-bound on the
  CPU already, so f64 is costing throughput before any device is involved.

NEEDS-111, the packed layout, is therefore not a parallel workstream. It is
phase zero.

### Layout inside a fused region

Row-major, contiguous, matching `strides()` in `internal/tensor/tensor.go` and
the existing kernels. No layout assignment pass and no tiling choices in the
first version: the fused kernel walks the output in flat index order, and each
input is read through the effective strides `effStrides` already computes, which
is where broadcasting becomes free rather than a materialised expansion.

### Buffers, not tensors

`docs/gpu.md` section 3 already makes residency a property of a buffer rather
than of a tensor, and the compiler adopts that unchanged. The addition here is
that a fused region allocates output buffers for its *region* outputs only.
Intermediates inside a region never get a buffer at all, which is the memory win
that comes free with the compute win: the Monte Carlo chain currently allocates
roughly eight 200,000-element f64 buffers, 12.8 MB of traffic, and fused it
allocates one.

---

## 5. Autodiff through a fused kernel

This is the part where a design that ignored twill's actual structure would
quietly fall apart, so it is treated first among the hard problems.

The difficulty stated precisely: today the derivative of an operation lives in a
Go closure created at the moment the operation ran, capturing the operand slices
it needs (`track2` in `internal/tensor/tensor.go`, and the `da`/`db` functions
passed to `broadcastBinary`). Fusing eight operations into one kernel means those
eight closures are never created. Something has to supply the derivative.

**The answer is to differentiate the trace, not the kernel.** The trace is a
straight-line dataflow graph with concrete shapes, which is exactly the input a
source-to-source reverse-mode transform wants. So:

1. Trace the forward computation as in section 2.
2. Transform the trace into a backward trace by walking it in reverse and
   emitting, for each node, the IR nodes for its vector-Jacobian product. These
   are the same formulas the existing `da`/`db`/`backward` closures implement, so
   the transform is a transcription of code that already exists and is already
   gradient-checked, not new mathematics.
3. Fuse and compile the backward trace with the same fusion pass as the forward
   one. A chain of elementwise VJPs is itself an elementwise chain, so the
   backward pass of the Monte Carlo pricer fuses into roughly one kernel too.

Two things fall out of this that are worth stating.

**It has to fuse or it is not worth doing.** An unfused backward pass over a
fused forward pass would launch once per node and pay the 80 microseconds per
node the fusion just eliminated.

**Saved intermediates become an explicit decision.** `relu`'s backward needs the
forward input, `exp`'s needs its own output, `div`'s needs both operands. Today
the closure captures whatever it needs and the garbage collector sorts it out.
In a fused kernel an intermediate may not exist, so the transform has to decide
per value between saving it (a buffer, memory traffic) and recomputing it in the
backward kernel (arithmetic, no traffic). The first implementation should save
everything a VJP references, because it is obviously correct and matches what the
interpreter does today; recompute is an optimisation with a measurable
before-and-after.

**`hessian` does not compile in the first version.** It runs forward-mode jets
(`internal/tensor/jet.go`) through a separate `recordJets` path with its own
per-node closures, and second-order over a fused kernel is a much larger design.
Traces containing a `hessian` call force to the interpreter. `jvp` and `hvp` are
the same path and force for the same reason. `vjp` compiles no better than it
suspends: its backward sweep runs through the interpreter's per-operation
closures, which a traced body does not build, so it suspends the tracer exactly
as `jacobian` does.

---

## 6. What compiles, and what does not

### Compiles

- Every operator in the elementwise, reduction/scan, and
  contraction/structural classes of section 3. That is the whole of
  `internal/tensor`'s differentiable surface, which
  `TestGradientCheckCoversEveryOperator` enumerates.
- Straight-line numeric code: `let` bindings, arithmetic, builtin calls,
  function calls the tracer can inline.
- `grad`, `grads` and `value_and_grad`, through the trace transform of section 5.
- Loop bodies, one iteration at a time, with the compiled trace cached and
  reused across iterations. This is the case that matters, because it is what a
  training loop is.

### Does not compile, and forces to the interpreter

Each of these forces rather than fails, so a program that mixes them still runs.
The design's correctness does not depend on the list being short.

- **Data-dependent control flow.** `if` on a computed tensor value, `while` on a
  computed condition, and `for` over a computed range. The tracer reaches the
  condition, needs the value, and forces. Capturing control flow into the IR is
  the natural second version and is not attempted here.
- **`hessian`, `jvp` and `hvp`, and the forward-mode jet path**, for the reason
  in section 5. `vjp` suspends the tracer for `jacobian`'s reason.
- **Non-numeric values.** Records, lists, dicts, strings, bytes, closures,
  variants. A record of weights is unpacked by the optimiser into tensors before
  any arithmetic happens, so this costs less than it sounds like, but the tracer
  follows tensors and nothing else.
- **`mode systems` entirely.** The systems dialect is `I64`, byte strings,
  arrays, dicts, structs and file IO, and by design a scalar there is a machine
  word and not a rank-0 tensor (`docs/design.md`). There are no tensors to fuse.
- **IO, `print`, `save`, `load`, `read_csv`, `read_frame`.** All force.
- **The gradient-boosted trees in `internal/gbm`.** A separate engine with its
  own data structures and no tensor operations to fuse.
- **`einsum` with more than two operands**, in the first version. Today's
  `einsum` takes any number, but one and two operands cover every use in `std/`,
  including both contractions of multi-head attention in `std/nn.tw`. The
  general case needs a contraction-order decision, which is a real optimisation
  problem and belongs in a later version; until then a three-operand `einsum`,
  as in `examples/einsum.tw`, forces to the interpreter.

---

## 7. How correctness would be verified

The interpreter is the reference semantics. `docs/design.md` says so, and the
verification strategy is the direct consequence: **the compiler is correct when
it agrees with the interpreter, and that is a testable proposition rather than a
review criterion.**

### 7.1 Differential testing over generated programs

The machinery for this already exists twice in the repository and neither piece
needs to be invented.

`internal/checker/soundness_test.go` generates random twill programs from a small
grammar and runs both the checker and the interpreter over 4,000 of them, and
`internal/tensor/fullgradcheck_test.go` holds `gradCases()`, a table of 101 cases
covering every differentiable operator with inputs already chosen to sit away
from the kinks where finite differences are meaningless.

The compiler's differential harness is those two put together:

1. Generate a random trace by composing operators from the `gradCases` table,
   with random but shape-compatible operands. The generator is the one in
   `soundness_test.go` extended past its current six forms.
2. Evaluate it with the interpreter.
3. Evaluate it with the compiler, at every fusion setting: fusion off, greedy
   fusion, and one region per operator.
4. Compare.

The comparison is where the work is. Bit-exactness is the right bar for anything
that does not reorder arithmetic, and most of the compiler does not: an
elementwise fusion computes exactly the same operations in exactly the same order
on exactly the same values, so `exp(x) * y` fused must equal `exp(x) * y` unfused
to the last bit, and anything else is a bug. Reductions are the exception, and
`docs/DECISIONS.md` entry 6 already fixes the rule: the emitted reduction must
use the same fixed 4096-element blocking as `parallelSum`, in which case it too
is bit-exact. **The bar is bit-exactness everywhere except transcendentals**,
where a device's `exp` differs from Go's `math.Exp` in the last bits and the
comparison is a tolerance, sited on the ULP difference the device actually shows
rather than on a round number.

### 7.2 Gradient checking the compiled backward pass

The forward comparison above says the compiler computes the right value. It says
nothing about the derivative, and section 5 is where a fused implementation is
most likely to be wrong.

So the gradient-check harness runs a second time against the compiler.
`runCase` in `fullgradcheck_test.go` compares reverse-mode against a
Richardson-extrapolated central difference at 1e-7 relative tolerance; the
compiled version compares the *compiled* reverse-mode against the same finite
differences, over the same 101 cases, at the same tolerance. This is the test
that would catch a wrong VJP transcription, a saved intermediate that was
recomputed incorrectly, or an accumulation into the wrong buffer, and it costs
nothing to build because the harness is written and the cases are chosen.

A third comparison is stronger still and nearly free: compiled gradient against
interpreted gradient, bit-exact under the same rule as 7.1. Finite differences
agree to 1e-11; the interpreter agrees to the last bit, so it is the sharper
oracle wherever it applies.

### 7.3 The corpus

`TestExamplesRunClean` already runs every program in `examples/` and asserts a
clean check and a clean run. The compiled version asserts the stronger property:
every example produces byte-identical output under the compiler and under the
interpreter. The self-hosting work already established that comparing canonical
float renderings byte for byte is a workable oracle at corpus scale, and the
harness under `tools/diff/` exists to do it.

### 7.4 What this does not verify

The differential tests compare the compiler against the interpreter. If the
interpreter is wrong, they agree and both are wrong. The independent check on the
interpreter is the finite-difference gradient check in 7.2 and the Black-Scholes
closed form the Monte Carlo example is measured against, and neither is affected
by the compiler existing.

---

## 8. The benchmark that would prove it worked

One primary benchmark, chosen because it is the program twill leads with, its
correct answer is known in closed form, and it is exactly the shape fusion is for.

### The benchmark

`bench/workloads/mc_option_grad.tw`: the Monte Carlo European call from the
README, differentiated for delta and vega, 200,000 paths. Measured by the
existing `bench/cmd/twillbench` harness, unchanged, median and p99 over 30 runs
after 5 warmups.

It is the right benchmark for four reasons. The forward pass is one elementwise
chain into a reduction, so it exercises the whole of section 3 with nothing else
mixed in. The backward pass is a second such chain, so it exercises section 5.
The result is checkable against Black-Scholes, so a fast wrong answer is caught.
And it is already measured, so the before number exists and was not chosen after
seeing the after.

### The baseline, measured

From `docs/BENCHMARKS.md`, on the machine described there:

| | twill today, CPU, best of a GOMAXPROCS sweep |
|---|---|
| `mc_option_fwd` | 3.450 ms median, 4.410 ms p99 |
| `mc_option_grad` | 13.646 ms median, 16.443 ms p99 |

`docs/BENCHMARKS.md` section 7 is the caveat that goes with these: the absolute
milliseconds carry about 40% of thermal drift on this machine, so the thresholds
below are stated as ratios against a baseline re-measured in the same session as
the compiled version, not against these figures read off the page months later.

### What would count as success

Three thresholds, in increasing order of ambition. They are stated before the
work rather than after it, which is the only time such a number means anything.

**The floor, below which the project failed.** `mc_option_grad` no slower than
the interpreted baseline re-measured alongside it, with bit-exact
agreement on the forward value and the gradients agreeing with the interpreter
under 7.2. A GPU backend that is slower than the CPU is the outcome
`docs/gpu-feasibility.md` measured for op-at-a-time dispatch at these sizes, and
avoiding it is the entire justification for building a compiler rather than a
backend.

**Success: 5x on the differentiated workload.** `mc_option_grad` at or under a
fifth of the baseline, which against the 13.646 ms measured here is 2.73 ms.
This is the threshold to design toward. The reasoning behind the
number, stated so it can be argued with: `docs/gpu-feasibility.md` measured about
9x for f32 matmul with transfers included and about 15x resident, and the
elementwise chain here is bandwidth-bound rather than compute-bound, so it should
do better than the matmul figure once fused; against that, the packed f32 layout
gives 2x on bytes moved at best and the reduction does not parallelise as freely
as the elementwise part. 5x is deliberately below the optimistic estimate.

**The result that would justify the dependency.** `mc_option_grad` at a tenth of
the baseline, 1.36 ms against the 13.646 ms measured here, *and*
`elementwise_10000000` at a tenth of its own, 8.0 ms against 80.441 ms. The second is there because a design that only
wins on one hand-picked program has not been shown to generalise, and the large
elementwise workload is the one whose result the fusion strategy predicts most
directly.

### The result that would sink it

Stated because a design document that cannot fail is not a design document.

If fused `mc_option_grad` lands between the floor and 2x, the compiler is not
worth its cost. The cost is real and is enumerated in
`docs/gpu-feasibility.md` and `docs/DECISIONS.md` entry 7: a GPU dependency, the
end of the single dependency-free binary, a build matrix, and a second numeric
implementation to keep bit-exact with the first. A 2x does not buy that, and the
honest response would be to record the measurement and stop, exactly as
`docs/gpu-feasibility.md` did for the backend.

There is also a cheaper experiment that must run first, because it would change
the answer. The same tracing IR and the same fusion pass, emitting **CPU**
kernels, needs no dependency at all and captures the allocation and memory-traffic
win without the 80-microsecond boundary. `docs/BENCHMARKS.md` shows the
elementwise workloads are bandwidth-bound and that intermediate buffers are a
measurable share of the time, so a fused CPU backend should show a real gain on
its own. If it does, it is the correct next step regardless of whether the GPU
work ever happens, and phases 1 through 3 below deliver it.

---

## 9. Order of work

Each phase is independently useful and independently abandonable.

0. **NEEDS-111, the packed buffer layout.** Prerequisite. Without it the target
   is f64 and the target is worth 5% of the card.
1. **The trace IR and forcing**, with no fusion and no codegen: every node
   dispatches to the existing `internal/tensor` function. Correct when it is
   bit-identical to the interpreter across `examples/`, which is a strong test of
   the tracer alone, before any kernel exists to be blamed.
2. **The trace transform for reverse mode.** Correct when the gradient-check
   harness of 7.2 passes against traced execution.
3. **Greedy fusion, emitting CPU kernels.** The first phase with a number
   attached, and the one that decides whether the GPU phase is worth starting.
4. **The device backend**, as designed in `docs/gpu.md`, with the fused regions
   of phase 3 as its unit of dispatch rather than individual operations.
5. **Measure against section 8, and publish the result whichever way it goes.**

---

## 10. What was built, and what it measured

Phases 1 through 3 of section 9 are implemented. Phase 0 (NEEDS-111, the packed
buffer layout) is not, and phases 4 and 5 are not. Everything below is a number
from a command that was run; where a thing was not measured, it says so.

Hardware for every figure in this section: Intel Core Ultra 9 285H, 16 logical
cores, 15.4 GB, Windows 11, Go 1.26.5, gcc 16.1.0 (MinGW-W64 UCRT).

### 10.1 The IR (`internal/ir`)

A flat single-assignment dataflow graph. Nodes name values, not buffers; whether
a value gets memory is the fusion pass's decision.

The design in section 2 argues for the IR from the launch-overhead side. The
implementation is aimed somewhere else, because the previous phase's profile
says something sharper: the tree-walking interpreter is 0.035% of runtime, about
18% goes to allocating and zeroing intermediate buffers read once and discarded,
and 17% to goroutine coordination. On the CPU the intermediates are the target,
and a region is precisely a set of values that never get a buffer.

`ir.Eval` is the reference evaluator: every node dispatches to the
`internal/tensor` function the interpreter would have called, so IR semantics
are the interpreter's semantics by construction rather than by agreement. That
is what phase 1 was for, and it is bit-identical to calling the tensor package
directly.

`ir.Grad` is the reverse-mode transform of section 5, over the graph rather than
over the kernel. Each rule is a transcription of the matching `da`/`db`/`df`
function in `internal/tensor`, so it inherits that code's gradient check rather
than needing new mathematics.

`ir.Fuse` is the greedy pass of section 3, with two deliberate restrictions
noted in 10.5.

### 10.2 The CPU backend (`internal/codegen`)

Emits C99, compiles it with a C compiler found on the machine (`gcc`, `clang`,
`cc`, or `TWILL_CC`), loads the shared library, and calls in. One C function per
fused region. Every buffer is a constant offset into one flat f64 arena, because
the IR's shapes are concrete.

Three things in the emitted code exist for bit-exactness rather than for speed:
the build passes `-ffp-contract=off` so no multiply-add fuses where Go rounds
twice; reductions carry `parallelSum`'s fixed 4096-element blocking, so the
answer does not depend on the emitted code being compiled at all; and
`twill_max`, `twill_min` and `twill_mod` exist because Go's `math.Max`,
`math.Min` and `math.Mod` are not C's `fmax`, `fmin` and `fmod` on NaN, on
signed zeros, and on a negative divisor. Constants are emitted as C99 hex float
literals so the compiler reads back identical bits.

The dial-in goes through `syscall` rather than cgo, so building twill still
needs no C compiler; the compiler is a run-time option a machine either has or
does not. Loading is implemented for Windows only. Elsewhere the emitter runs
and its output can be compiled and read, and `Compile` reports that the load is
unavailable rather than pretending.

### 10.3 Verification, with the numbers

Everything here is `go test ./internal/codegen/ -v`.

**Forward, 500 generated programs.** Depth 6 to 15, drawn from the operator set,
every broadcasting regime including rank 0 and extent 1, compared against
`ir.Eval` at both fusion settings. 62 programs contained no transcendental and
matched bit for bit. 438 contained one and matched within 1e-12 relative, worst
observed 3.15e-15. 0 skipped. Every program, transcendental or not, matched
fused against unfused **bit for bit**, which is the sharpest assertion in the
suite: it is the compiler compared against itself with only fusion varying.

**The tolerance is measured, not assumed.** `TestTranscendentalULP` runs each
function over its range through Go and through the emitted code. On this
machine, over 20,001 points each: exp 1 ulp worst (2.19e-16 relative), log 1 ulp
(1.81e-16), sin 1 ulp (2.22e-16), cos 1 ulp (2.22e-16), tanh 1 ulp (2.10e-16),
sqrt 0 ulp and not one differing value, as IEEE 754 requires. The whole-program
bound of 1e-12 is four orders above that, and the extra is conditioning rather
than backend slack: the generator freely writes cos(x) - x, and a subtraction of
two nearby quantities multiplies its operands' relative error. One such
expression turned a 1-ulp cos difference into 1.48e-15 on the output.

**Gradients, the same 500 programs.** 1,606 parameter gradients in total. Every
fused gradient matched its unfused counterpart bit for bit. Against
`tensor.Backward`, 1,493 of the 1,606 were bit-identical and 113 differed, worst
3.74e-15, under a 1e-12 bar. The bar is not exact, and the reason is a property
of reverse mode rather than a defect: when a value feeds two operations its
cotangent is a sum of two contributions, floating-point addition is not
associative, and `tensor.Backward` adds them in the order its depth-first walk
visits consumers while `ir.Grad` adds them in reverse node-index order. The 113
that differ are exactly the parameters with more than one consumer.

**Gradients against finite differences, 38 curated cases.** The independent
check that survives the interpreter being wrong, since it consults no autodiff
at all: the compiled backward pass against a Richardson-extrapolated central
difference of the compiled forward pass, using the method and the constants of
`internal/tensor/fullgradcheck_test.go`. All 38 pass at 1e-7 relative; worst
4.82e-9, on a matmul into tanh into sum chain. Cases are sited away from kinks
for the same reason the cases in that file are.

**A bug this found.** `sum(where([1, 1], x * 2, x * 3))` differentiated used to
panic. `where` routes the cotangent to whichever branch its condition selected,
so a condition that selects the same branch everywhere leaves the other branch's
gradient buffer unallocated, and that branch's own closure then reads a nil
`Grad`. It is fixed in `Backward` (a node that received no cotangent has nothing
to propagate) with a regression test in `gradcheck_test.go`. It is an
interpreter bug, present before any of this work, found by the differential
harness rather than by review.

### 10.4 Speed, measured, with the caveats that go with it

`TWILL_SPEED=1 go test ./internal/codegen -run TestSpeed`. Median and p99 over
30 iterations after 5 warmups, with the two implementations **interleaved inside
one loop** rather than measured in blocks, because `docs/BENCHMARKS.md` section
7 measured 74% thermal drift on this class of machine.

| | interpreter path | compiled, fused | ratio |
|---|---|---|---|
| Monte Carlo forward, 200,000 paths | 2.612 ms median, 3.438 ms p99 | 1.048 ms median, 1.623 ms p99 | 2.49x |
| the same, differentiated | 8.240 ms median, 10.669 ms p99 | 2.215 ms median, 2.794 ms p99 | 3.72x |

Four things have to be said about those numbers or they are worth less than they
look.

They are not the benchmark section 8 specifies. That one runs
`bench/workloads/mc_option_grad.tw` through the CLI, and it cannot run yet
because there is no front end that turns a `.tw` file into IR (10.5). What is
measured is the same mathematics driven from Go, with the interpreter side being
`ir.Eval`, which calls exactly the `internal/tensor` functions the interpreter
calls. They are therefore not comparable to the 3.450 ms and 13.646 ms in
`docs/BENCHMARKS.md`, which include parsing, the CLI, and `randn`.

The compiled side is **single-threaded C** and the interpreter side is
multi-threaded Go. So the win is entirely from not allocating and not touching
intermediates, which is what the fusion pass was aimed at, and there is a
straightforward factor still on the table.

The arena numbers say where it came from. Forward: 17 kernels to 2, and
9,600,088 bytes of intermediates to 16. Differentiated: 50 kernels to 27, and
24,000,280 bytes to 9,600,168. The backward pass fuses much less well, because a
cotangent is frequently read twice and this pass materialises rather than
recomputes.

It is a CPU result and says nothing about the GPU thresholds in section 8.

### 10.5 What is not built, stated plainly

**There is no front end.** Graphs are built through `ir.Builder` from Go, and no
`.tw` file is compiled ahead of time, so the question of what fraction of the
language compiles has to be answered as a ceiling rather than as an achievement.

(Written before section 11. The tracer and forcing described here as unwritten
were built afterwards and are measured in §11, so `.tw` programs do now reach the
compiler at run time, one statement scope at a time. The ceiling below is still
the right way to read the coverage number, and §11.2.9 is where that scope turns
out to be the binding constraint.)

`ir.CoverProgram` measures that ceiling by classifying AST forms against the
opcode set. Over `examples/`, `bench/workloads/` and `std/`, 81 files, 7,349 of
10,841 nodes (67.8%) are inside the subset: 87.6% in `bench/workloads`, 72.0% in
`examples`, 65.8% in `std`. **Zero files are entirely inside it**, and that is
the number that matters most. Every example prints, and `print` forces. The
compilable region is a region *inside* a program, not a program, which is
precisely the case tracing and forcing exist to handle and precisely why the
front end is the next thing to build rather than a detail.

The largest things outside the subset, by node count: calls through a
non-identifier callee (1,152), strings (633), indexing (220), `list` (140),
`print` (100), field access (77), `len` (74), `for` (62).

**The operator set is a subset.** Elementwise arithmetic, the comparisons,
`where`, `clip`, `relu`, `square`, the transcendentals, `pow` with a scalar
exponent; `sum`, `mean`, their axis forms, and `sum_to`; `reshape`, `transpose`,
`broadcast_to`; `matmul`. Not: `einsum`, `conv2d`, `maxpool2d`, `softmax`,
`logsumexp`, `sort`, `topk`, `gather`, `concat`, `split`, the cumulative ops,
`prod`, `median`, the `max` and `min` reductions, quantisation, `hessian`.

**It is f64 only.** Phase 0 is not done, so a narrow dtype is refused rather
than compiled.

**Two fusion rules from section 3 are not implemented.** Structural ops are
materialised as their own regions rather than folded into a consumer as an index
remap, which leaves the 14% `docs/perf-baseline.md` attributes to
`TransposePerm` on the table. And a contraction takes no elementwise epilogue,
so `relu(x @ W + b)` is two regions rather than one. Both were left out because
restricting absorption to elementwise producers is what makes the emitter's
index arithmetic one `effStrides` read per input and therefore checkable by
inspection.

**Loading is Windows-only**, and the emitted code is not parallelised.

### 10.6 CUDA, for the stage after this one

Checked, not installed. `nvidia-smi` reports an NVIDIA GeForce RTX 5070 Laptop
GPU with 8,151 MiB and driver 610.88. There is no CUDA toolkit: `nvcc` is not on
PATH and the NVIDIA GPU Computing Toolkit directory under Program Files does not
exist. So the device is present and the compiler for it is not, and phase 4
needs a toolkit installed before it can start.

---

## 11. The tracer, measured

Section 10 was written when graphs were built by hand from Go, so nothing said
how much of a real program reaches the compiler. The tracer closes that, and the
answer is worse than the kernel numbers in 10.4 suggest.

Measured with `bench/cmd/tracestats`, which runs a program with tracing on and
prints the counters the tracer keeps. Same machine as section 10: Intel Core
Ultra 9 285H, 16 cores, 15.4 GB, Windows 11 build 26200, go1.26.5.

### 11.1 How much traces

20 of the 23 examples produce at least one traced node. `einsum.tw`,
`jacobian.tw` and `units.tw` produce none.

That number flatters it. The counter that matters is compiled against replayed,
because a replayed scope runs the interpreter's own path and pays the tracing
cost on top:

| program | nodes | compiled | replayed |
| --- | --- | --- | --- |
| `linreg.tw` | 3,625 | 1,200 | 5 |
| `signal_opt.tw` | 6,591 | 1,002 | 9 |
| `mlp.tw` | 36,288 | 8,000 | 10,040 |
| `nn_xor.tw` | 30,316 | 2,000 | 16,060 |
| `classifier.tw` | 25,954 | 402 | 11,858 |
| `attention.tw` | 55,165 | 82 | 37,309 |
| `gpt.tw` | 190,568 | 42 | 124,130 |

The two programs that compile nearly everything are the two smallest optimisation
loops. The larger a program gets, the more it replays: `gpt.tw` compiles 42
scopes and replays 124,130.

The `bench/workloads/*.tw` files trace **nothing at all**, which is worth knowing
before quoting any timing over them. They were written for the harness in 10.4,
which builds graphs directly, so a wall-clock comparison across them measures
process startup and nothing else.

### 11.2 Speed, which is currently a loss

Built binary, alternating `TWILL_TRACE=1` and `TWILL_TRACE=0` in one loop, 11
iterations after 3 warmups, median:

| program | traced | interpreter | ratio |
| --- | --- | --- | --- |
| `linreg.tw` | 23.9 ms | 9.7 ms | 0.41x |
| `mlp.tw` | 105.4 ms | 57.2 ms | 0.54x |
| `montecarlo_option.tw` | 30.2 ms | 23.8 ms | 0.79x |
| `signal_opt.tw` | 34.9 ms | 24.3 ms | 0.70x |
| `nn_xor.tw` | 111.1 ms | 64.4 ms | 0.58x |
| `classifier.tw` | 86.7 ms | 51.9 ms | 0.60x |

Still slower end to end on every one of them, by between a quarter and a
factor of two and a half. That is not in tension with 10.4's 2.49x and 3.72x: those timed a
compiled kernel against the interpreter with the graph already built. This times
everything the tracer costs to get there, and at these sizes the tracing, the
cache lookup, the C compilation of each new graph and the replays outweigh what
the kernels win.

### 11.2.1 What the profile said, and what it cost

Profiling the traced `linreg.tw` run put **79% of it in
`codegen.FindCompiler`**, reached through `buildShared` and almost entirely
`runtime.cgocall`. Not the C compiler: the *search* for it. `exec.LookPath`
was called once per compilation, and on Windows a miss walks every `PATH`
entry against every executable extension. Three compilations meant three
searches, and the searches cost more than gcc did.

`FindCompiler` now answers once per process. In-process, `linreg.tw` went from
36.5 ms to 5.7 ms.

The end-to-end numbers above are after that fix. Before it they were 0.26x,
0.45x and 0.52x.

### 11.2.2 One hypothesis that was wrong

`linreg.tw` opens 3,223 scopes for 3,625 nodes, roughly one node each, so
compiling a graph that small looked like it could not pay. A threshold below
which a trace replays instead of compiling was added, and swept over 0, 2, 4, 8,
16 and 32 nodes.

It made no difference. At 20 iterations and three repeats the two settings
overlap: 7.3, 9.7 and 7.7 ms with the threshold off against 5.9, 8.0 and 8.0 ms
with it at 16. The run-to-run spread is larger than the effect. The threshold was
removed rather than kept as a knob the data does not support, and this is written
down because a negative result someone else would otherwise retry is worth as
much as a positive one.

### 11.2.3 Where the escapes actually come from

`replayed` equals `escapes` exactly in every program measured, so every replay
is a mid-scope escape rather than a compilation failure. Attributing those
escapes to their call sites, and then to the check that refused the operand,
gives one answer and it is not liveness:

| program | refused for RequiresGrad | forcing escapes |
| --- | --- | --- |
| `gpt.tw` | 470,040 | 124,130 |
| `attention.tw` | 307,280 | 37,309 |
| `mlp.tw` | 112,000 | 10,036 |

`Tracer.operand` refuses any tensor with `RequiresGrad` set, because a compiled
kernel produces no backward closures and the interpreter's autodiff would
silently receive a zero. That refusal is correct. The problem is that in a
training program almost every tensor has it set, so almost every operation
outside a `grad` scope refuses, the trace breaks, and the scope replays.

This is why the two shapes of program behave so differently. `linreg.tw` and
`signal_opt.tw` do their work inside `grads(...)`, where the differentiated
inputs are registered as parameters before the body runs and never reach that
test, so they compile nearly everything: 1,200 and 1,002 compiled scopes against
5 and 9 replays. `gpt.tw` holds parameters that require gradients and computes
with them outside a grad scope, so it compiles 42 scopes and replays 124,130.

The fix is not a constant, and the cheap version of it does not exist. Three
attempts, all measured and all reverted:

- **A minimum trace size before compiling.** Swept 0 to 32 nodes, no effect
  beyond the run-to-run spread. Section 11.2.2.
- **Dropping the escape on a plain variable rebind**, which reads no data and
  looked needlessly conservative. It moved `classifier.tw` from 11,858 escapes
  to 11,854, for a change that touches a liveness invariant. Not worth the risk
  for four escapes.
- **A cold-statement cache**: after a statement has built a trace and thrown it
  away twice for this reason, stop tracing that statement. It fired, 38 times in
  `gpt.tw` and 1,998 in `mlp.tw`, and escapes did not move at all. Skipping an
  owning statement only hands ownership to the statements inside it, which then
  open their own traces and escape themselves. The work moves, it does not go.

What is left is the real change: trace a `RequiresGrad` tensor as a graph
parameter, and have the compiled path produce a backward graph so the value it
returns participates in autodiff. `ir.Grad` exists and `CloseGrad` already does
exactly this for a grad scope, so the missing pieces are narrow and known:

1. `internal/tensor` has no exported way to build a tensor with parents and a
   backward closure. `p0`, `p1`, `pRest` and `backward` are unexported, so
   `internal/trace` cannot construct one. That hook has to be added first.
2. A scope can hand out several outputs, so the backward closure has to
   accumulate cotangents into the right parameters rather than assume one.
3. It has to be gradient-checked, not just differentially tested. The failure
   mode is a zero gradient, which produces a plausible number and a model that
   silently does not learn. That is the shape of the `QLinear` bug in
   docs/BUGS.md, and the reason `TestGradientCheckCoversEveryOperator` exists.

### 11.2.5 It was attempted, and it failed the gate

The above was then built, far enough to run: `tensor.TrackCompiled` to attach a
caller-supplied backward closure, and a `compileAndRunGrad` that compiles the
forward graph, compiles `ir.Grad`'s backward graph beside it, and wires each
output to the graph's parameters with a closure that seeds that output's
cotangent, runs the backward program, and accumulates into each parameter.

Two things happened, and both are the point.

The operator-coverage test failed within seconds: `TrackCompiled` is exported,
has no gradient-check case and no entry saying it needs none. It also failed in
the reverse direction when `EnsureGrad`, a method rather than a package
function, was declared. That closure is doing exactly what it was written to do.

Then the differential tests failed on `attention.tw`, `classifier.tw`,
`cnn.tw` and others: programs that previously matched the interpreter byte for
byte no longer did. These are training programs, so a changed output means
changed gradients, and unlike `signal_opt.tw` they had matched exactly before,
which rules out reassociation. The wiring is wrong somewhere. The likeliest
candidates, in order: a parameter that is itself a placeholder from an earlier
scope, making the parent chain a cycle that `Backward`'s topological walk does
not expect; the argument order the backward graph expects against the order it
is given; and a value that is both an output and a parameter counting twice.

It was reverted. A compiled path that produces plausible numbers and wrong
gradients is worse than no compiled path, and the whole reason this repo has a
103-case gradient check and a byte-for-byte differential suite is that the
`QLinear` bug proved that class of error is invisible without them.

### 11.2.6 Where the refusals actually are, which changes the fix

A second attempt separated the two scope kinds: refuse a `RequiresGrad` operand
inside a grad scope exactly as before, and wire the backward pass only in a
statement scope. That version is correct. The full suite passes, gradient checks
included. It also fires **zero** times:

| program | refused inside a grad scope | refused in a statement scope |
| --- | --- | --- |
| `gpt.tw` | 470,040 | 0 |
| `attention.tw` | 307,280 | 0 |
| `mlp.tw` | 112,000 | 0 |

Every refusal is inside a `grads(...)` body. None is outside one. So the
statement-scope path is dead code on this corpus and was reverted, and the first
attempt's failure is explained: it changed grad-scope behaviour, which is the
only behaviour these programs exercise, and a grad scope's backward pass is
produced by `ir.Grad` over the whole body. Registering an extra gradient-tracking
tensor as a plain graph parameter cuts it out of that.

What that leaves is a narrower question than "support RequiresGrad". Inside a
grad scope, the differentiated leaves are already registered as parameters before
the body runs. The tensors being refused are the *other* gradient-tracking values
that reach the body: weights not being differentiated in this call, and values
carrying gradient state from an enclosing scope. For the current call they are
constants, so registering them as parameters is arithmetically right. What it
breaks is any outer backward pass that needed the cotangent to travel through
them, which is exactly the nested-`grad` and escaping-value case.

The obvious narrow version of that is to accept only autodiff leaves, since a
leaf's cotangent accumulates and goes no further. Counting says it is not worth
building on its own:

| program | leaf | intermediate |
| --- | --- | --- |
| `gpt.tw` | 25,000 | 445,040 |
| `attention.tw` | 15,360 | 291,920 |
| `mlp.tw` | 12,000 | 100,000 |
| `classifier.tw` | 14,000 | 50,000 |

About one refusal in twenty is a leaf. The rest are intermediates, values with
parents, whose cotangent has to carry on past them.

That is still tractable, and it says what to build. Register the refused tensor
as a graph parameter, and in the backward pass accumulate its gradient into that
tensor's own `Grad` buffer rather than discarding it. `tensor.Backward` walks
parents from the output, so an intermediate that has received a cotangent will
have its own closure called and the chain continues on the interpreter's side.
Both leaves and intermediates work under that rule, because a leaf simply has
nothing further to call.

The part that needs care is the grad scope itself. `CloseGrad` differentiates the
whole body with `ir.Grad` and returns gradients for the leaves that were
pre-registered. Any extra gradient-tracking parameter admitted this way also has
a gradient in that result, and dropping it is precisely how the first attempt
produced wrong answers. It has to be accumulated into that tensor's `Grad` too.

So: one rule for admitting the operand, one for accumulating in the ordinary
backward pass, and one for not discarding the extra gradients in `CloseGrad`.

### 11.2.7 Two of those three already existed, and the third admits nothing

`CloseGrad` already accumulates into every parameter with `RequiresGrad` set, so
the rule about not discarding the extras was never missing. That leaves the
admission rule, and it has an exact form: a gradient-tracking tensor may be
traced when the differentiated result does not reach it through the leaves being
differentiated. Independent of those leaves, it is a constant of this
differentiation and the compiled graph may read it like any other value. Reaching
them, making it a parameter severs the path and the leaf's gradient comes back
short.

That was implemented, memoised per scope over the interpreter's autodiff parents,
and it is correct: the whole suite passes, gradient checks, strict liveness mode
and fusion-off included. It also admits nothing. On `mlp.tw`, of 96,000
gradient-tracking operands reaching the test inside a grad scope, 96,000 depend
on the leaves and none is independent. Node and escape counts come out byte for
byte identical to not having the rule at all.

In hindsight that is what a training loop is. Inside `grads(loss)(w, b)` almost
every tensor is downstream of `w` or `b`, because that is what the function
computes. The gradient-tracking values that are genuinely constant there are
rare.

A measurement earlier in this section said the opposite, that 85 to 95% of them
were independent. That number was wrong. It came from instrumentation that built
the leaf set from `t.params` at a point where that slice did not stand for the
leaves, and it is recorded here as an error rather than quietly dropped, because
it is the number that made this attempt look worth making.

### 11.2.8 The mechanism of the cascade, and why fixing it alone costs more

Looking at the first refusals inside a grad scope rather than the totals shows
something the counts hid: they arrive with `params` at zero. A grad scope
registers its differentiated leaves as parameters when it opens, so that should
never be zero, and `Escape` is what empties it. It rebuilds the trace after a
mid-scope force and truncates `t.params` along with everything else.

So the first escape inside a `grads(...)` body drops the leaves, and from that
point every value descending from them is a gradient-tracking tensor that is not
a parameter, which `operand` refuses. One escape interprets the entire remainder
of the differentiated function. That is the cascade, and it is a design flaw
rather than a tuning question.

Putting the leaves back after an escape is four lines and it works: on
`mlp.tw` traced nodes go from 36,288 to 72,288 and on `classifier.tw` from
25,954 to 46,354, with every test still green.

It is also slower. `mlp.tw` traced goes from 112-123 ms to 142-144 ms, memory
from 197 MB to 261 MB, allocations from 2.17M to 2.84M. The extra work traces
and is then thrown away, because the escape points that end each scope are
unchanged: escapes rise from 10,040 to 16,040 on the same program. Reverted on
that basis.

What it establishes is where the boundary actually is, and it is not where 11.2.5
put it.

### 11.2.9 The two are not composable, and the reason is the scope

11.2.5 said the fix was a compiled backward pass for statement scopes, and
11.2.8 said restoring the leaves had to come after it. Doing both turns out to
be doing one, because 11.2.6 already showed statement scopes contain no
gradient-tracking operands at all. The compiled-backward design has nothing to
apply to.

So the honest experiment is leaves restored on its own, and then asking what is
still escaping. On `mlp.tw`, with the leaves back, of 16,040 escapes:

| site | count |
| --- | --- |
| `forced()`, a value the interpreter needs concretely | 8,016 |
| a binary operand still refused | 8,000 |
| everything else, builtins included | 24 |

Eight of those escapes are builtins. The rest are the interpreter asking for a
number. `let g = grads(loss)(w1, b1, w2, b2)` hands back a list, and the next
three statements index it, multiply by a learning rate and assign. Every one of
those forces, because a statement scope closes at the end of the statement and
the next statement starts a new trace against values that are now concrete.

That is the design boundary, stated in section 2 and reached here empirically: a
statement is the largest region whose live values are known exactly and for free.
A training loop's work does not fit in one statement. Compiling it means tracing
across statements, which needs a real liveness analysis over the interpreter's
environments and its Go stack, and section 2 declined that on the grounds that
getting it wrong produces a wrong answer rather than a slow one. Nothing measured
here argues with that.

The conclusion is therefore not that a piece of work is outstanding. It is that
the tracer, as scoped, compiles what a statement can hold: `montecarlo_option.tw`
at 1.65x with a third less memory is what that looks like when it fits, and a
training loop is what it looks like when it does not. Widening the scope is a
different project, and it is the one with the liveness analysis in it.

Where that leaves the tracer: correct, off by default, and reached mostly by
programs that do their work outside a grad scope. Making it pay for a training
loop needs the compiled path to produce a backward pass that the interpreter's
autodiff can continue from, which is the first attempt's design and is still
unbuilt. Three attempts have narrowed what it has to do; none has done it.

What these two passes establish is that the tests are sharp enough to run this
experiment safely: the first attempt was caught by the differential suite in
minutes, the second was shown to be inert by counting rather than by assuming it
worked.

Two smaller contributors, worth recording because they look like the problem and
are not: builtins with no opcode account for 93,982 of `gpt.tw`'s escapes, of
which `mean` is 28,990, and `mean` already has an opcode. It refuses for the
same reason as everything else.

### 11.2.4 The default

The tracer is off unless `TWILL_TRACE=1` asks for it. It is correct, and on
every program measured here it is slower, so defaulting it on would cost every
twill program time to buy a compiled path most of them never reach. It goes on
when 11.2.3's fix lands and the numbers say it should.

### 11.2.5 What is left

1. **`RequiresGrad` outside a grad scope**, above. Everything else is smaller.
2. **Compilation is per process for the in-memory cache**, though the shared
   library itself is already cached on disk by `buildShared`.
3. **The tracer already wins where the graph is worth compiling.** In-process,
   `montecarlo_option.tw` is 9.1 ms traced against 15.3 ms interpreted, 1.65x,
   with a third less memory. It has 37 nodes in 3 scopes. `linreg.tw` has 3,625
   nodes in 3,223. The difference between winning and losing is graph size per
   scope, not total work.

### 11.3 What this means for the GPU stage

The kernels are fast and the path to them is slow, so a GPU backend would make a
faster thing that is still reached too rarely to matter. An on-disk kernel cache
and a look at why large programs replay are both worth more than CUDA right now.

---

## 12. The barrier

`black_box(x)` returns `x`. This section is longer than the implementation
because a barrier is worth exactly what its guarantee is worth, and a guarantee
nobody wrote down is worth nothing.

### 12.1 Why it exists, and why the premise it was filed under was wrong

bobbin `docs/needs.md` entry 3 asked for "an operation the optimiser cannot see
through", and filed it with the status "twill is interpreted and removes
nothing, so this is not blocking today. It becomes blocking the day it is not."
`docs/roadmap.md` entry 30 repeated that. The mitigation meanwhile was
`bench.keep`, a twill function that returns its argument, named so that there
would be one place to fix later.

The premise was already false when it was written, and this document is the
reason it is easy to be wrong about: everything above describes a compiler, in
this repository, that deletes work. Section 4's liveness rule is the deletion.
A statement's tensor operations are recorded rather than run, and when the scope
that owns the trace closes, `trace.compileAndRun` asks which of the recorded
values are still reachable from what that scope handed out. If none are, it
computes none of them:

```go
outRefs, outPH := t.liveRefs(live)
if len(outRefs) == 0 {
    // Nothing escapes. There is nothing to compute and nothing to patch.
    return true
}
```

That is dead code elimination, and it is exactly the elimination a benchmark
walks into, because a benchmark's defining feature is that it throws its result
away.

#### What the instrument counts

`internal/trace.Stats.Computed` counts tensor operations whose arithmetic was
performed. It counts the same population as `Stats.Nodes`, which rises once per
operation the tracer records, in `Tracer.place`. So the two columns below are
two counts of one set of things, and their difference is work that was written
down and thrown away.

It is deliberately not `Compiled + Replayed`. Those two count events rather than
work -- `internal/trace/trace.go` defines them as scopes closed by running
compiled code and forces that fell back to replay -- and one of either can stand
for two thousand multiplications or for none. An earlier draft of this section
published a table derived from `Compiled + Replayed` and called the result
"nodes computed". It was not that, and its `black_box` row did not match what
running the same helper on the same program produced. The instrument is named
here so the next reader can check it rather than trust it.

`Computed` is exact rather than sampled, because a recorded operation reaches
one of two ends and both are counted where they happen:

- It is still in the open trace when something forces it, in which case it is
  computed, and so is every other operation then open. Both evaluators are
  all-or-nothing over what they are handed: `ir.Eval` walks nodes rather than
  outputs, and the backend gives every unabsorbed node its own region
  (`internal/ir/fuse.go`) and emits every region (`internal/codegen/emit.go`),
  so neither skips an operation for having no reader.
- Or the owning scope closes with nothing live, and `compileAndRun` returns at
  the fragment above, before either evaluator is reached. Nothing is computed.

The trace is emptied at every force and at every reset, so nothing is counted
twice and nothing is counted before its arithmetic ran. The claim about the two
evaluators is a claim about code that could change, so it is pinned by tests
rather than by this paragraph: `TestEvalComputesEveryNodeIncludingUnreadOnes`
and `TestTheBackendPlansEveryNodeIncludingUnreadOnes` in `internal/codegen` both
fail the day an evaluator starts skipping an unread node, which is the day this
section needs remeasuring.

#### The measurement

Three programs, each in three versions. The body is `sum(exp(x))` on a 64x64
tensor, which is two tensor operations, so a program that calls it four times
records eight.

```rust
# A: one call whose result the caller has no use for.
fn discard(x) { let y = sum(exp(x)) }
discard(big)

# B: the same call four times, which is the shape a harness runs.
# bobbin src/harness.tw is this shape: it calls `batch` as a statement,
# `batch` returns unit, and every result the body produces dies inside it.
fn body(x) { let y = sum(exp(x)) }
for i in range(4) { body(big) }

# C: four calls whose results are bound in the loop body.
fn body(x) = sum(exp(x))
for i in range(4) { let y = body(big) }
```

| program | protection | operations traced | operations computed |
|---|---|---|---|
| A, one discarded call | none | 2 | 0 |
| A | `fn keep(v) = v` | 2 | 0 |
| A | `black_box` | 2 | 2 |
| B, four discarded calls in a loop | none | 8 | 0 |
| B | `fn keep(v) = v` | 8 | 0 |
| B | `black_box` | 8 | 8 |
| C, four calls bound by a `let` | none | 8 | 8 |
| C | `fn keep(v) = v` | 8 | 8 |
| C | `black_box` | 8 | 8 |

Reproduce with `go test ./internal/interp -run TestBarrierMeasurementTable -v`.
That test asserts all nine cells and prints the table above, so a number here
that a run does not produce is a red test rather than a stale document.

#### How to read it

**The deletion is total, not partial.** In B every operation of all four
iterations is deleted. There is no residue and no iteration that survives by
accident, so a benchmark in that shape does not report a wrong duration for the
work, it reports the duration of not doing it.

**`bench.keep` is not protection, and could not have been.** Rows two and five
are identical to rows one and four. A call to a twill function is not a barrier,
because the tracer does not stop at one: it follows through the body and keeps
recording, and the values die at the same scope boundary they would have died at
anyway. `keep` was written to be the single place to fix, and it is exactly
that and nothing more.

**The shape of the harness decides this, not the shape of the body.** C traces
the same eight operations as B and computes all eight of them without any
barrier, because the owning statement there is the `let`, its value is the
body's result, and a live value at a close is not deleted. So the barrier buys
nothing in C. That is not the barrier failing; it is nothing having been
deleted to begin with. It is in the table because a table showing only the
shapes where the barrier helps would be an advertisement. What C establishes is
that a benchmark cannot tell from its body whether it is safe, which is the
argument for putting the barrier in unconditionally rather than where it looks
needed.

**A claim that was in this section and is not true:** an earlier draft said "a
loop of four iterations whose result is discarded computes three of them, not
zero", and attributed it to forcing points falling between iterations. No shape
measured produces three. It came from reading a count of forces as a count of
work, and it is recorded here rather than quietly removed because the point of
this section is that a number has to survive being re-run.

#### What was not measured

Every row was produced on a machine where the compiled path is unreachable.
`internal/codegen/load_other.go` supports dialling into emitted code on Windows
only, so `Compiled` was 0 in all nine runs and every force in the table went
through replay. `Computed` is written to give the same figure either way -- both
paths compute every node in the trace they are given, and the branch that
computes nothing is taken before either runs -- but that is the structural
argument above and the tests that pin it, not a second measurement. Running this
table on Windows with a C compiler present is what would measure it, and nobody
has.

The table also counts tensor operations only. Systems mode traces nothing, so
there is no row in it for the case section 12.3 says the barrier is a promise
with no mechanism.

### 12.2 What black_box guarantees

1. **The argument is computed before `black_box` returns.** The mechanism is
   section 2's escape rule rather than anything special: `black_box` has no
   opcode, and `Apply` forces the open trace before any builtin without one
   runs. So the value is materialised, and the work behind it is done, at the
   call.
2. **The value comes back unchanged.** Not merely equal: the same value, with
   its shape, its dtype and its place in the gradient graph. A gradient runs
   straight through a barrier. This is what makes it safe to leave in code that
   is also checked for correctness, and it is the difference between this and
   `stop_grad`, the other builtin in this language that looks like an identity
   and is not one.
3. **It is not a shape barrier.** `grad` is one (`docs/CORRECTNESS.md`), and a
   value wrapped in it loses the checking downstream. A benchmark is code nobody
   reads twice, so blinding the checker there would be the worst possible place
   to spend that trade. Shape errors are reported straight through a barrier.
4. **No pass may see through it.** It is never given an opcode, never constant
   folded, never hoisted, and never deleted for having no reader. That is a rule
   about code not yet written, so it is pinned by a test rather than by this
   paragraph. `TestBarrierMeasurementTable` in `internal/interp/barrier_test.go`
   goes red in both directions: its `black_box` rows fail if the barrier stops
   forcing, and its unprotected rows fail if the deletion the barrier guards
   against stops happening, which is the prompt to rewrite section 12.1 rather
   than to delete the barrier.

### 12.3 What it does not guarantee

- **It is not a promise about the machine.** It constrains this compiler. It
  does nothing about the CPU's caches, branch prediction or frequency scaling, a
  GPU driver's queueing, or the operating system's scheduler. A benchmark that
  measures a warm cache still measures a warm cache.
- **It does not make a benchmark correct.** Hoisting a loop-invariant body out
  of the loop is the author's mistake to avoid, by varying the input with the
  iteration index, which is what bobbin's `body: fn(I64) -> F64` is for. The
  barrier constrains the compiler, not the program.
- **In systems mode it is a promise with no mechanism yet.** Nothing here can
  delete an `I64` addition; the tracer records tensor operations. So a systems
  mode `black_box` costs one builtin call and buys the guarantee for the day
  something can, which is the day bobbin filed the entry against.
- **It is not a memory fence or a synchronisation primitive.** twill has no
  concurrency, and this name is borrowed from languages where it sometimes
  implies one. It does not imply one here.
- **The self-hosted evaluator enforces nothing**, because it has no tracer and
  no optimiser: there, `black_box` is the identity and that is all it is. The
  two implementations are required to agree on the value, not on the mechanism,
  and `TestSelfHostedBlackBox` is where that is checked.

### 12.4 For whoever writes the next pass

If you are adding an optimisation to this compiler, the rule is one line:
`black_box` is opaque. Its argument is used, its result is used, and neither
fact is derivable from anything else in the graph. Every deletion, fold, hoist
and rewrite has to stop at it. If a pass makes that inconvenient, the pass is
what changes.
