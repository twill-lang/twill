# Dtypes

Twill stores every tensor element as a float64. This document is the design that
stops it, and `src/tensor.tw` is the implementation.

The reason it comes before the other performance work rather than after it is
that three separate efforts are all blocked on the same fact:

- **A GPU backend is not viable on f64.** Measured on this machine's RTX 5070,
  f64 runs at 341.7 GFLOP/s against 18,010 for f32, a 52.7:1 penalty.
  `docs/gpu-feasibility.md` records the measurement and its recommendation
  already names f32 as the larger win.
- **Quantisation buys nothing.** `twill-lang/shuttle` implements int8 and f16
  quantisation and correctly reports that it shrinks nothing, because the
  storage is f64 either way.
- **Bandwidth is the real limit.** Tensor work is memory-bound far more often
  than it is compute-bound, and f64 costs twice f32 and four times bf16 for
  every byte moved.

## What this change does and does not do

It lands the *semantics* of a dtype: which values exist, what a store rounds to,
what an operation promotes to, and what it accumulates in — in full, across
every forward op and in both implementations (see "What has landed" below). It
does not yet land the *layout*: the buffer is still `Arr[F64]`, one F64 per
element.

The two are separable because of one invariant, which `src/tensor.tw` enforces
in `mk` and which every kernel depends on:

> Every element of a tensor's buffer is exactly the F64 widening of a value
> representable in that tensor's dtype.

A bf16 tensor therefore holds bf16 values today, and will hold them in sixteen
bits each when NEEDS-111 lands, with no kernel changing in between. What is not
saved yet is memory: shuttle stays right about the bytes until NEEDS-111, and
stops being right that quantisation does nothing observable, because the error
quantisation introduces is now measurable from twill.

Doing it in this order is deliberate. The layout needs a byte-addressable buffer
primitive from the runtime and cannot be specified without one. The numerics
need nothing, can be written now, and are the part that is wrong in most
implementations.

## The dtype set

Seven, and no more:

| dtype  | bits | exponent | mantissa | what it is for |
| ------ | ---- | -------- | -------- | -------------- |
| `f64`  | 64   | 11       | 52       | the default; exactness, and everything that exists today |
| `f32`  | 32   | 8        | 23       | the working dtype of real training; every accumulator |
| `bf16` | 16   | 8        | 7        | halved bandwidth with f32's range; trains without loss scaling |
| `f16`  | 16   | 5        | 10       | more precision than bf16 in a much narrower range; needs loss scaling |
| `i32`  | 32   | -        | -        | indices, counts, the output of argmax and argsort |
| `i8`   | 8    | -        | -        | quantised weights and activations |
| `bool` | 8    | -        | -        | masks, and the output of every comparison |

What is deliberately absent: f8 in either of its two encodings, because nothing
in twill can currently train a model large enough for it to matter and each
encoding is another rounding path to verify; int16 and int64, because nothing
asks for them and an index that does not fit in i32 does not fit in a tensor
either; and complex, which is a different design.

`bool` and `i8` are stored in eight bits rather than one and four because a
sub-byte element needs bit addressing in the buffer and in every kernel, and the
factor of eight is not worth that until something needs it.

### bf16 against f16, honestly

They are the same size and they are not interchangeable.

bf16 is f32 with 16 mantissa bits removed. It keeps f32's eight exponent bits,
so its range is f32's range: roughly 1e-38 to 3e38. It keeps seven explicit
mantissa bits, so it carries about three significant decimal digits. Converting
f32 to bf16 and back changes a value by up to 0.4% and never overflows or
underflows a value f32 could hold.

f16 has five exponent bits and ten mantissa bits. It carries about three and a
half significant decimal digits, better than bf16, in a range of roughly 6e-5 to
65504, which is dramatically narrower.

The consequence for training is not symmetric and it is not subtle:

- **bf16 trains without loss scaling.** Gradients that are small stay
  representable, because the exponent range is f32's. This is the whole reason
  bf16 exists and the reason it won.
- **f16 does not.** Real gradients routinely land under 6e-5, where f16
  subnormals lose precision, and under 6e-8, where they are zero. A parameter
  whose gradient flushed to zero stops learning and reports nothing. In the
  other direction, an intermediate above 65504 becomes an infinity, and one
  infinity minus another is a NaN that propagates through the whole model in one
  step.

So f16 is supported here only together with its loss-scaling story, which is
below and is part of the design rather than an afterthought.

### The default for a literal is f64

`[1.0, 2.0]` is an f64 tensor. This is not the answer a machine-learning
library would give, and the reason is specific to twill rather than general.

The differential harness compares program output byte for byte against the Go
bootstrap, which computes in float64. `print(x)` goes through `format_number` in
`src/float.tw` and the whole acceptance criterion for the self-hosted compiler
is that the two agree on every digit. Making f32 the default literal dtype
changes the printed value of most expressions in `testdata/`, so it invalidates
every golden before it improves any model.

There is a second reason, weaker but real: twill is used for statistics and for
data frames as much as for models, and those uses want f64 and would be
surprised to lose it silently.

So narrow dtypes are always asked for and never inferred:

```
let w = zeros([784, 128], bf16)      # a constructor takes the dtype
let x = data.to(f32)                 # an explicit cast
```

The default should move to f32 when the goldens are regenerated against a
dtype-aware bootstrap, and that is a decision with a migration attached, not a
default to flip. Until then, a program that wants f32 says f32.

### The cast is spelled `.to(dt)`

`x.to(f32)`, lowering to `tensor.cast(t, DT_F32)`. It is a method rather than a
function because it reads left to right in a pipeline, which is where casts
actually appear: `logits.to(f32).softmax(-1)`.

There is no implicit narrowing anywhere. Promotion (below) only ever widens. A
value is narrowed because the program said to narrow it.

A cast rounds **once, from the source value**. Casting bf16 to f16 goes directly
and not through f32: two roundings disagree with one on exactly the values that
land halfway after the first, and that class of bug shows up as a handful of
wrong elements and no other symptom.

## Promotion

Mixed-dtype operations promote both operands to one dtype and produce it. The
rules, in order:

1. Same dtype: that dtype.
2. Two integer kinds: the wider. `bool` is the narrowest integer kind, so
   `bool + i8` is `i8`.
3. An integer kind with a float kind: **the float kind, unchanged.** `bf16 * 2`
   is bf16 and not f32. An integer operand is nearly always a small literal, and
   widening the tensor to accommodate it defeats the reason the tensor was
   narrow.
4. `f16` with `bf16`: **f32.** Neither contains the other; f16 has three more
   mantissa bits and bf16 has five more exponent bits. Picking either would
   silently discard whichever property the other operand was chosen for.
5. Otherwise the wider float: `f64 > f32 > {bf16, f16}`.

Two exceptions, both about what an operation *means* rather than what it is
given:

- A **comparison** produces `bool` whatever it was handed.
- An operation whose result is an **index** produces `i32`: `argmax`, `argmin`,
  `argsort`, `argtopk`. An argmax returned in bf16 would round to the nearest
  power of two above 256 and start naming the wrong element.
- An operation whose result **cannot be an integer** promotes an integer input
  to f32: `mean`, `median`, `softmax`, `logsumexp`, `exp`, `log`, `sqrt`, and
  the rest of the transcendentals. `neg`, `relu`, `square` and `clip` preserve
  integrality and keep the input dtype.

Promotion never narrows, which is what lets a rearrangement kernel copy elements
into a promoted result without re-rounding them.

## Accumulation

**This is the rule that decides whether the whole thing works.**

A tensor has a dtype. An *operation class* has an accumulation dtype. Asking a
tensor what it accumulates in is already the wrong question, because the same
tensor accumulates differently in a matmul than in an elementwise add.

| operation class | accumulates in | stores in |
| --------------- | -------------- | --------- |
| elementwise unary and binary | the result dtype (a single operation; see below) | the result dtype |
| contraction: `matmul`, `einsum`, `conv2d` | f32 for f16, bf16 and f32; f64 for f64; i32 for integers | the result dtype |
| reduction: `sum`, `mean`, `prod`, `logsumexp`, `sum_to_shape` | same | the result dtype |
| normalisation: `softmax` | same | the result dtype |
| scan: `cumsum`, `cumprod` | same, carried across the run | the result dtype, per element |
| selection: `max`, `min`, `median`, `sort`, `topk`, `maxpool` | nothing accumulates | the input dtype, unrounded |

In one line: **anything narrower than f32 accumulates in f32.**

### Why, concretely

A bf16 matmul with an inner dimension of 1024 adds a thousand products. bf16
carries eight significand bits, so once the running sum exceeds about 256 times
the size of the next term, the term rounds away entirely and the sum stops
growing. The answer is not imprecise; it is wrong, and it is wrong in the
direction that makes a model appear to train and then plateau for no visible
reason. Accumulating in f32 and rounding once on store fixes it, and it costs
nothing that matters, because the accumulator is a register and not the thing
that fills memory or crosses a bus.

The same argument holds harder for `prod`, where the running exponent is the sum
of the terms' exponents and leaves bf16's range long before it leaves f32's, and
for `softmax` and `logsumexp`, whose terms differ by orders of magnitude on
purpose.

### How the reference implementation gets it exactly right

Every kernel in `src/tensor.tw` computes in F64 and rounds. That is exactly the
narrower format's own arithmetic, not an approximation of it, because of one
theorem: a double rounding is harmless when the wide format carries at least
2p+2 bits of the narrow format's p. F64's 53 clears that bar for f32's 24, for
f16's 11 and for bf16's 8.

So:

- A single elementwise operation computed in F64 and rounded to the result dtype
  is bit-identical to the same operation performed in the result dtype. This is
  why the elementwise row of the table above needs no accumulator at all.
- An accumulation is made exact by rounding to the accumulation dtype after
  every step. `acc_add(dt, acc, x) = dt_round(dt, acc + x)` is an
  `dt`-precision addition, so a loop of them is an `dt`-precision accumulator,
  and it will agree bit for bit with a backend that really does keep an f32
  register.

That last property is the one that makes a future GPU backend checkable against
the reference rather than merely similar to it.

## Autodiff

**A gradient is never narrower than f32.**

`grad_dtype(f64) = f64`. `grad_dtype(anything else) = f32`.

The reason is dynamic range, not accuracy. A cotangent is a sum over every use
of a value, so it accumulates across a batch and across every broadcast axis,
and it is routinely orders of magnitude smaller than the activation it belongs
to. f16's smallest normal is 2^-14; real gradients reach it and go under. A
gradient that underflows to zero is a parameter that stops learning with nothing
reported anywhere.

This is the master-weights arrangement that mixed-precision training already
uses in practice: the forward pass runs narrow, the gradients and the optimizer
state stay f32, and the narrow weights are a rounded copy of the f32 master.

It is decided in exactly two places in the implementation, on purpose:

- `backward` allocates each node's cotangent at `grad_dtype(node.out.dtype)`.
- `accumulate` rounds each contribution to the cotangent's own dtype as it adds
  it, and it is the only function that stores a gradient.

Every `vjp_` rule therefore computes at full width into a plain buffer and never
mentions a dtype. A rule that narrowed on its own would be rounding an
intermediate, which is the thing the accumulation rule exists to forbid.

An integer or boolean leaf gets a zero gradient of dtype f32, so shapes and
dtypes still line up for a caller that zips parameters with gradients.

Jacobians, jets and the Hessian follow the same rule. The Hessian follows it
with the most at stake: it is computed from a divided difference, which
subtracts two nearly equal numbers, and that is where precision is lost fastest.

## Loss scaling, and the f16 story

bf16 needs none of this. Its exponent range is f32's, gradients that are
representable in f32 are representable in bf16, and the design above already
keeps the gradients themselves in f32. A bf16 forward pass trains as-is.

f16 needs all of it. Even with f32 gradients, an f16 *forward* pass overflows at
65504, which a sum of squares in a normalisation layer reaches routinely, and
underflows at 6e-5, which activations after a few multiplications reach as well.
The standard fix, and the one specified here:

1. Multiply the loss by a scale S before seeding the backward pass. By the chain
   rule every gradient is scaled by exactly S, so gradients that would have
   underflowed land inside the representable range.
2. Divide every gradient by S before the optimizer sees it.
3. If any gradient came back non-finite, the scale was too large: **skip the
   step entirely** and halve S. Skipping matters. Clipping an infinity to a
   large finite number is a plausible-looking update in an arbitrary direction.
4. After a run of successful steps with no overflow, double S. This is dynamic
   loss scaling, and it converges on the largest scale the model tolerates
   without needing to be tuned.

The surface this needs, and the reason NEEDS-112 exists:

```
backward_scaled(tp, root, seed, scale) -> Res[Arr[Tensor], Str]
grads_finite(gs) -> Bool
```

A caller can write the loop by hand today with `binary(OpMul, ...)` and a scan
for non-finite values, and the reason to name it is that a hand-written version
that forgets step 3 looks like it works.

**If you are choosing between them, choose bf16.** f16's extra three mantissa
bits do not pay for the machinery above, and the machinery is only ever as good
as the one caller who forgets it.

## What is checked, and how

`src/float.tw` holds the conversions. There is one narrowing routine and one
widening routine, parameterised by exponent and mantissa width, rather than
three of each: the rounding step is the hard part and it should exist once.

Rounding is round-to-nearest, ties-to-even, in both directions. bf16 is
emphatically **not** the top 16 bits of an f32. That truncation is
round-toward-zero, and it is wrong in a way that does not average out: every
magnitude it changes, it shrinks. Over a tensor the errors sum instead of
cancelling, and a weight matrix quantised that way is systematically smaller
than the one it came from.

Since systems-mode twill cannot be executed, both routines were transcribed into
Python and checked against two independent references: an exact rational
round-to-nearest-even, and numpy for f32 and f16. Measured, not asserted:

| what | cases | divergences |
| ---- | ----- | ----------- |
| narrowing vs exact-rational reference (f32, bf16, f16) | 3,057,582 | 0 |
| narrowing vs numpy (f32, f16) | 2,038,388 | 0 |
| widen and round-trip: all 65,536 f16, all 65,536 bf16, 1,992,134 f32 | 2,123,206 | 0 |
| NaN and infinity, both directions, all three formats | 600,015 | 0 |

The case set is not random alone. It includes every f16 and every bf16 bit
pattern widened to f64; both zeros; both infinities; NaN with a payload whose
low bits would vanish under truncation; the largest and smallest normals and
subnormals of all four formats; the overflow boundary of each format and its
neighbours; and, per format and per binade, the exact halfway point between two
representable values together with the f64 values one and two ulps either side
of it, which is the round-to-nearest-even tie case and the one a transcription
gets wrong.

The harness lives in the scratchpad and not in the repo, because it tests a
Python transcription rather than the twill source, and a test that cannot run
against the thing it claims to test does not belong next to it. What it does
establish is that the algorithm is right, which is the part that cannot be found
by reading.

## What has landed

The numerics are complete and byte-identical across both implementations. Both
the self-hosted tensor library (`src/tensor.tw`) and the Go bootstrap
(`internal/tensor`) now agree, element for element, on every forward op that
touches a dtype:

(`src/tensor.tw` is the tensor library written in twill, not the self-hosted
evaluator, and this comparison runs it on the Go bootstrap. The self-hosted
evaluator, `src/eval.tw`, cannot run `src/tensor.tw` at all: the builtins it is
written on are among the ones `src/eval.tw` does not implement. See
`docs/roadmap.md`, "What the second implementation agrees on, and what it does
not", and `docs/BUGS.md` entry 12.)

- **construction and the cast** — `zeros(shape, bf16)`, `.to(dt)`, and the
  dtype-suffixed makers (NEEDS-110, landed).
- **elementwise and unary** — promotion of the operands, one rounding on store;
  an integer-preserving op (`neg`, `square`, `clip`) keeps its kind.
- **reductions and scans** — `sum`/`mean`/`prod`, `cumsum`/`cumprod`,
  `softmax`/`logsumexp`, each accumulating at the accumulation dtype (f32 for
  anything narrower) and storing the result dtype; `mean` of an integer promotes
  to f32.
- **selections and rearrangements** — `max`/`min`/`median`, `sort`/`topk`,
  indexing, slicing, `split`, `gather`, `reshape`/`transpose`/`flip`/`roll`,
  `concat` (promoting its pieces) carry the input dtype through unchanged;
  `where` promotes its two branches; `argmax`/`argsort`/`argtopk` produce i32.
- **the four contractions** — `matmul`/`linear`, `einsum`, `conv2d` accumulate
  in f32 for a narrow input and round to the promotion of the operands.
- **printing** — a narrow tensor prints the value it holds, not the f64 it was
  widened to, via the shortest decimal that re-rounds to the stored value
  (NEEDS-114, landed).

f64 is untouched throughout: `Promote(f64, f64) = f64` routes it past every
narrow path, so its goldens stay bit-for-bit as before.

## What is still open

`docs/needs.md`, in dependency order:

- **101** a packed, byte-addressable buffer. Until this lands, no memory is
  saved and shuttle's report stands. The buffer invariant above is what lets
  this land with no kernel changing.
- **102** `backward_scaled` and `grads_finite`, the loss-scaling surface above.
- **113** dtype in the static checker, so `f16 + bf16` is known to be f32, and a
  lossy widening is flagged, before it runs. The self-hosted checker already
  tracks a tensor's dtype; the Go checker's type system does not yet carry one,
  so this is the one place the two still differ in what they can prove.

## See also

- `docs/gpu-feasibility.md`, whose f64 conclusion is conditional on this work.
- `src/float.tw`, the conversions.
- `src/tensor.tw`, the dtype machinery and every kernel that respects it.
