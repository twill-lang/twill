# Design notes

Why Twill is built the way it is, and what's left to do.

## The idea

The usual machine-learning stack, Python plus a numeric framework, was
assembled over time, not designed as a whole. Autodiff, shapes, and device
placement are all added on top of a language that came first. Twill asks a
narrower question: if you designed a language around differentiable tensor
programs from the start, what would it look like?

The answer we're trying: a small language where the tensor is the primitive
type and differentiation is a language operation rather than a library call.
The rest follows from keeping those two ideas central.

## Principles

1. Tensors first. There's no separate scalar type; a scalar is a rank-0 tensor.
   That keeps autodiff, broadcasting, and printing uniform.
2. Differentiation is built in. `grad`/`grads` are part of the language. You
   never wire up a tape or call `.backward()`.
3. Small enough to read. The implementation is about 16,000 lines of Go, and
   predictability matters more than feature count right now.
4. No dependencies. The reference implementation is plain Go with no third-party
   packages, so it builds to one binary and can be read end to end.

## Why Go

The implementation language went through a couple of iterations (an early
TypeScript prototype, then a look at Rust). Go won for this stage: it builds to
a single dependency-free binary, it's quick to compile and easy to read, and the
standard library covers everything the interpreter needs. Rust would give better
numeric performance and a real ML crate ecosystem (Burn, candle, ndarray), and
it's a reasonable target later; the language design does not depend on the host.
For a small, readable reference implementation, Go is the better fit.

## Architecture

A straightforward tree-walking interpreter:

```
source ─lexer─▶ tokens ─parser─▶ AST ─┬─ checker ─▶ shape diagnostics
                                      └─ interpreter ─▶ values
                                                 │
                                                 ▼
                                           tensor engine (autodiff)
```

- `internal/lexer`: a hand-written scanner with source positions.
- `internal/parser`: recursive descent with a Pratt loop for operators.
  Expression-oriented, so `if` and blocks produce values.
- `internal/tensor`: the autodiff engine. A tensor is a flat `[]float64` plus a
  shape; operations optionally record a reverse-mode graph.
- `internal/interp`: evaluates the AST against lexical scopes. All arithmetic
  lowers to tensor-engine ops, so any computed value can be differentiated.
- `internal/checker`: static shape inference over the AST.
- `cmd/twill`: the CLI and REPL.

## How autodiff works

Twill uses reverse-mode automatic differentiation, the same approach as PyTorch
and JAX, implemented directly in the tensor engine.

Besides computing its output, each operation can record the inputs it depended
on and a closure that pushes gradient from the output back to those inputs using
the local derivatives. That graph is only built when an input requires
gradients, so ordinary evaluation allocates nothing extra: the tape appears
only inside a `grad(...)` call.

`grad(f)` re-wraps the call's arguments as fresh gradient-tracking leaves, runs
`f` (which builds the graph as a side effect), seeds the scalar output's
gradient to 1, and walks the graph in reverse topological order. Arguments can
be nested lists, in which case the returned gradient mirrors that structure,
which is how a whole model held in a list gets differentiated at once.

So differentiation is just running the program with gradients turned on.

## The shape checker

The checker infers a shape (or "unknown") for every expression and reports a
diagnostic only when a mismatch is certain: both operand shapes fully known and
incompatible. Everything it can't determine stays unknown, so it never flags
correct dynamic code. That bias toward precision over recall is deliberate: a
checker that cries wolf gets turned off.

It knows the shapes of tensor literals, of construction builtins called with
literal sizes (`zeros(4)`, `randn(4, 2)`), and of the operations that combine
them. Optional parameter annotations give it more to work with and let it check
call sites against a declared contract. It does not follow shapes through
`grad`, loops that reshape, or values read at runtime. Those are left unknown.

## Two modes, and where they disagree

`mode systems` (designed in `docs/self-hosting.md`) is not a second language. It
is the same grammar and the same interpreter with a different set of rules made
mandatory. But three of those rules genuinely differ from numeric mode's, and a
difference that is not written down is a trap rather than a design. The
normative statements are in `docs/language-guide.md`; this is why they are what
they are.

**A scalar is a rank-0 tensor in numeric mode and is not one in systems mode.**
Principle 1 above is a numeric-mode principle and it earns its place there:
uniform scalars are what make autodiff, broadcasting and printing one mechanism
instead of two. Systems mode has no tensors, so it has nothing to be uniform
with, and the cost of pretending otherwise is measurable. A metric accumulator
running `total = total + x` once per training step would allocate a tensor and a
tape node per step if `F64` were a rank-0 tensor, and an epoch would build a
chain of them. `F64` is a machine word. The seam between the two is `f64()` and
`i64()` and nothing crosses it implicitly.

**Aggregates are handles and mutation through a parameter is visible to the
caller.** This is the rule the six ecosystem codebases were already built on,
and it is a genuine risk sitting next to `grad`, which walks a record's
structure and is correct only because records do not alias. The resolution is
that `struct` and `Record` are separate types and stay separate: mutation is
never retrofitted onto `Record`. The reason to state the rule rather than leave
it to the implementation is asymmetric failure. If aggregates copied, a training
framework would obviously not work, but `src/tensor.tw`'s reverse pass would
quietly return zeros, and a zero gradient is not an error. It is a model that
does not learn.

**`%` is floored in numeric mode and truncating in systems mode.** On tensor
data a modulo is almost always a wrap into a range, where a negative result is
the bug; on `I64` it is digit extraction and packing, ported from integer code
that assumes C's rule and the identity `(a / b) * b + a % b == a`. Neither
answer is right for both, and one rule chosen for consistency would be silently
wrong in whichever half did not get it. The related edge is that `shr` is
`floor(a / 2^k)` while `/` truncates, so replacing a division by a power of two
with a shift is valid only for a non-negative dividend.

## Known limitations (v1.9)

Deliberate, for a prototype. Two entries left this list in 1.7.0, which is worth
naming because they were the two largest: user-defined generics (`struct Box[T]`,
`enum Tree[T]`, `fn first[T](...)`) parse, check and run, and a match pattern is
a tree, with nesting, literals and guards. One rough edge is left on the first
of those: a bare type parameter in *return* position is read as a unit in
numeric mode, so `-> T` answers `unknown unit "T"` outside `mode systems`
(`docs/BUGS.md`, Open).

- Interpreted by default. There is a tracing compiler under `internal/trace`
  and `internal/codegen` which emits C and is bit-exact against the interpreter,
  and it is off, because it is slower end to end on every program measured: a
  statement is the largest region whose live values are known for free, and a
  training loop's work does not fit in one. `docs/CODEGEN.md` section 11 has the
  measurements and the five attempts that did not close the gap.
- Reverse-mode, first-order autodiff. A nested gradient is refused rather than
  answered; `hessian` is the second derivative and is exact.
- Shape checking is best-effort by design: it reports only mismatches it can
  prove and cannot infer shapes for unannotated parameters, so a mismatch that
  depends on one is caught at the call site or not at all.
- The systems-mode types are checked as of 1.6, under the same policy: a
  definite mismatch is reported and an unresolved type judges nothing. The
  stricter policy `docs/self-hosting.md` section 1.3 asks for, where a type
  still unknown at the end of inference is itself an error, is open as NEEDS-49.
- Record fields aren't mutable in place; you rebuild the record. A `struct`, in
  systems mode, is the mutable one.
- An enum variant carries at most one payload value. A variant that needs two
  things carries a `struct`.
- No named axes; broadcasting and reductions work on positional axes.
- Every float is an `f64`. A narrow dtype is a tag and a rounding rule rather
  than a layout, so quantisation shrinks nothing in memory (NEEDS-111).

## Roadmap

Roughly in order of value:

1. Push static shapes further: infer shape variables without annotations and
   catch more mismatches, moving from best-effort toward a real type system.
2. A faster backend: a bytecode VM, or lowering tensor ops to a vectorized or
   native library. Keep the interpreter as the reference.
3. More autodiff: higher-order derivatives, forward mode, batching.
4. Named axes (einsum exists; a named-axis tensor type would go further).
5. `grad(grad(f))`, which is refused today rather than answered. The optimizers
   this line used to ask for arrived: `std/optim` walks leaves with `map_leaves`
   and `zip_leaves`, so `optim.adam` works on a record of named weights and on a
   positional list alike.

## Non-goals for now

- Being a general-purpose language. Twill is aimed at differentiable numeric
  programs.
- Matching a mature framework's operator coverage on day one. The point is the
  core model; operators are easy to add once that's right.
