# Bugs

Defects I found in twill and what fixing each one taught me. Every entry names
the commit, the mechanism, and the test that would catch it coming back.

I have kept the ones where the wrong answer looked like a right answer, because
those are the entries worth reading. A crash tells you it is broken. A gradient
that comes back as zero does not, and neither does a random number generator
that returns the same plausible value forever.

---

## 1. `grad` through a quantised linear layer silently returned zero

**Symptom.** `linear(x, quantize(W))` computed the right value and the wrong
gradient. Not an error, not a `nan`, just zeros:

```
dense value  = 21
quant value  = 21.023622
dense grad   = tensor([5, 7, 9], shape=[3])
quant grad   = tensor([0, 0, 0], shape=[3])
```

The value is right, and the quantisation error of 0.02 is right for int8. Only
the gradient is wrong, and it is wrong in the way that looks like a model that
has converged.

**Root cause.** `QLinear` and `QLinear4` in `internal/tensor/quantize.go` and
`quantize4.go` returned `&Tensor{Data: out, Shape: outShape}` and nothing else.
No `track1`, no parents, no backward closure. `Backward` walks the graph through
the `p0`/`p1`/`pRest` pointers, and this tensor had none, so the traversal simply
stopped there and every parameter upstream kept its initial zero gradient.

The reasoning behind it was sound and the conclusion did not follow from it. A
quantised weight is frozen, so the doc comment on the `quantize` builtin says
the result "is a frozen weight for `linear`, not a differentiable tensor, so it
belongs to inference, not training". That is true of the weight. The activation
`x` is an ordinary full-precision tensor, and what the kernel computes is
exactly linear in it, so the derivative with respect to it not only exists but
is exact. Freezing the weight had been implemented as detaching the whole
operation.

**How it was caught.** `TestGradientCheckFullOperatorSet` in
`internal/tensor/fullgradcheck_test.go`. It was not caught by inspection or by a
failing example. The gradient check is table-driven over every differentiable
operator and its coverage test parses the package's source to enumerate them, so
`QLinear` and `QLinear4` had to have a case or a written reason for having none.
Writing the case is what found it: the harness reported "no gradient reached the
leaf" before any tolerance was compared.

This is the whole argument for exhaustive rather than representative gradient
checking. The two quantised kernels were the least-used operators in the package
and had the worst defect in it.

**Fix.** `5344302`. Both kernels wire `track1`, and the backward pass computes
`dL/dx = g @ Wq` against the dequantised weight, which is exact because the
codes are constants.

**Regression test.**
`TestQLinearGradientReachesTheActivation` in `internal/tensor/quantize_test.go`,
which asserts the gradient equals the column sums of the dequantised weight and
fails explicitly on the all-zeros case, plus the `qlinear-i8/activation` and
`qlinear-i4/activation` cases in the full gradient check.

---

## 2. `twill check` panicked on a file ending in a backslash

**Symptom.** A source file ending in an unterminated string whose last byte is a
backslash, for example `x = "ab\`, made `twill check` die with a Go stack trace:

```
panic: runtime error: index out of range [8] with length 8
    internal/lexer.TokenizeWithComments.func2(...)
```

Neither the file nor the mistake is named. A tool embedding the lexer takes the
whole process down with it.

**Root cause.** The string branch in `internal/lexer/lexer.go` consumed the
backslash and then called `advance()` for the escaped character without checking
one existed. `advance` indexes `runes[i]` unguarded, and at end of input `i ==
len(runes)`.

**How it was caught.** By the self-hosting work, not by a user and not by the Go
test suite. `src/lex.tw` was run against `internal/lexer/lexer.go` over 385
corpus files and 4,000 seeded fuzzer cases, compared on token kind, literal text,
line, column, comments, and the error message and its position. The corpus and
the fuzzer found nothing. Targeted edge cases found three divergences, and this
was one: the twill lexer checked, and reported "unterminated string" at the
opening quote. That is the better diagnosis, since the file's problem is the
missing close quote and not the backslash. Recorded as NEEDS-33 in
`docs/needs.md`, with the resolution being to fix the Go side.

Writing the same program twice and comparing is an expensive way to find bugs
and it finds ones that nothing else does. This is the argument
`docs/self-hosting.md` makes for the exercise, and it is the concrete evidence
for it.

**Fix.** `d3176a9`. The escape branch checks for input before advancing and
returns the same "unterminated string" diagnostic `src/lex.tw` gives.

**Regression test.** `TestUnterminatedStringEndingInABackslash` in
`internal/lexer/lexer_test.go`, over four shapes of the input, plus
`TestEscapesAtTheEndOfAClosedStringStillLex` so the fix cannot be made by
breaking ordinary escapes.

---

## 3. The cumulative scans were not differentiable, and said nothing about it

**Symptom.** `grad` through `cumsum`, `cumprod`, `cummax` or `cummin` came back
zero. Where a scan was only part of an expression the answer was worse than
absent, it was wrong: `max_drawdown` divides by a `cummax` peak, so its gradient
was neither correct nor obviously broken, and `sma`, `equity`, `total_return`
and `cagr` in `std/backtest` were all in that state.

**Root cause.** All four were registered through a helper that computed the fold
in plain `float64` and returned `tensor.New`. No parents, no backward closure.
The same mechanism as bug 1 and, four years of hindsight aside, the same lesson:
an operation that returns a bare `&Tensor` is an operation that silently ends the
graph.

The comment on that helper and `docs/language-guide.md` both said the scans were
forward-only, which documented the behaviour without enforcing it. `grad`
accepted them and returned a number anyway.

**Fix.** `8c5d997`. All four moved to `internal/tensor/scan.go` as tracked
operations. `cumprod`'s backward is the division-free prefix/suffix pair, so a
zero in the series is exact rather than `0/0`; `cummax` and `cummin` record the
argmax forward and scatter to it, ties to the earlier element, matching `max` and
`argmax`.

The same commit fixed a second defect that the first was hiding. `hessian`
dereferenced nil whenever the input was not connected to the output, and
tracking the scans removed one route to it but not the cause: `floor`, `ceil`,
`round` and the comparisons still return an untracked tensor and still sever the
chain. `hessian` now checks whether the leaf is in the graph and returns zeros,
which is the correct second derivative of a function that does not depend on its
input.

**Regression test.** `TestCumulativeGradients` and
`TestCumulativeGradientThroughDrawdown` in `internal/interp/cumulative_test.go`,
`TestHessianDetachedInput` in `internal/interp/hessian_test.go`, and gradient
checks in `internal/tensor/gradcheck_test.go` and `jet_test.go`. All four scans
now also appear in the full operator gradient check.

---

## 4. `std/random` returned the same number forever

**Symptom.** Every draw from `std/random` was identical. Deterministic,
plausible, in range, and constant.

**Root cause.** xoshiro256\*\* and splitmix64 are defined on 64-bit wrapping
arithmetic. In numeric mode an `I64` is carried in an `f64`, which holds 53 bits
of mantissa, so every multiply in the seeding overflowed into the rounding
instead of wrapping. The state saturated and stopped moving.

This is the failure mode I most want recorded. It is not a crash and not an
obviously wrong value: the generator returned a number, in the right range, the
same every time, and nothing downstream complained. It is invisible until
somebody checks a variance.

It is also not fixable at that representation, only movable, which is why it is
NEEDS-2.

**Fix.** `1ec2f28`. The bit source moved to the host: `rng_open`, `rng_close`,
`rng_u53`, `rng_f64` and `rng_norm` give independent streams named by handle.
`std/random` keeps its whole surface, and only the bit source changed.

**Regression test.** `std/tests/random_test.tw`, rewritten. It had been a
documented known failure asserting an exact stream, which is a test that breaks
whenever the generator is fixed. It now asserts the properties a caller depends
on: seeds reproduce, adjacent seeds are independent, `below` is unbiased across
its range rather than merely inside it, and `normal` returns its cached
Box-Muller partner. 39 of 39 pass.

---

## 5. `num.var_axis` raised a shape error on every axis but one

**Symptom.** `var_axis(x, 1)` on a `[2, 3]` tensor failed with a shape error.
`var_axis(x, 0)` worked.

**Root cause.** The function subtracted the mean straight back from the input,
but a reduction drops the axis it reduced, so a `[2]` was being broadcast against
a `[2, 3]`. Broadcasting aligns from the right, so only a reduction over axis 0
lined back up, and by luck rather than design.

I am keeping this one because it is the cheapest kind of bug to ship: it works
on the case you tested and fails on the case you did not, and the failure is
loud, so it looks like a small bug. It is a small bug. It also meant that
`var_axis` had never been correct for the axis anyone would reduce over.

**Fix.** `00731f3`. The mean is restored to the reduced shape through a `keep`
helper, the keepdims that other array libraries put on the reduction itself. The
same commit added `broadcast_to`, since the general case, expanding against a
shape that is not one of the operands, had no expression before it.

**Regression test.** Gradient checks for `broadcast_to` in
`internal/tensor/gradcheck_test.go`, and `TestGradCheckBroadcastTo` plus the
`broadcast-to` case in the full operator check.

---

## 6. A fractional array index, from an integer division that was not one

**Symptom.** `buf_get8 index 48 out of range` from the self-hosted evaluator on
any transpose whose shape was not square. `minibatch.tw` could not train.

**Root cause.** Numbers in numeric mode run as `float64`, so integer division
comes back fractional. `src/tensor.tw`'s `transpose_origins` binds `let coord:
I64 = rem / stride` and accumulates it into a buffer offset, and a fractional
offset made `buf.get` read across an element boundary.

The Go bootstrap did not hit it, because it runs `internal/tensor`'s Go integer
division and not `tensor.tw`. So this is a bug that existed only in the second
implementation, found because there is a second implementation.

**Fix.** `15a6da6`. The annotation the source already carried is honoured: a
scalar bound at a bare `I64` is truncated toward zero at the bind, in both the Go
interpreter's `execStmt` and the self-hosted `exec_stmt`. On a real `I64`
runtime, where `/` is already integer division, this is a no-op.

**Regression test.** The differential harness: `check` over 443 corpus files and
run differentials over autodiff, jacobian, hessian, shapes and units, all
byte-compared against the Go bootstrap. `minibatch.tw` trains under the
self-hosted evaluator with loss identical to Go at epoch 0, 0.687804.

---

## 7. The scalar fast path swallowed rank-0 tensors, and with them the tape

**Symptom.** `backward: no such tape node` from the self-hosted evaluator.
`autodiff.tw`, `hessian.tw`, `mlp.tw` and `montecarlo_option.tw` all failed.

**Root cause.** `eval_binary`'s scalar fast path read both operands through
`rank0_number`, which widens a `Num` *and* a rank-0 tensor to a plain float. That
was harmless until the tensor operators were wired up, at which point it became a
correctness bug: a rank-0 tensor carries a tape node, so intercepting it here
computed `x*x*x` in plain floats and the derivative then found no node to walk
back through.

The bootstrap's fast path is `Num`-only for exactly this reason, and the reason
is a principle rather than an implementation detail: a `Num` can carry no
gradient, a tensor of any rank can. `docs/design.md` states it as the numeric
mode rule that a scalar is a rank-0 tensor, and this is what it costs to get
wrong.

**Fix.** `5d871ff`. The fast path fires only when both operands are `VNum`; a
tensor of any rank goes to the traced kernel.

**Regression test.** The example differentials. `autodiff.tw` prints
`f'(2) = 12` and the correct tensor gradients, and `mlp.tw` trains to step 1000
loss 0.005, both compared against the bootstrap.

---

## 8. Two implementations of a sum that disagreed in the last bits

**Symptom.** Golden-output tests failed above 8192 elements. Not by much: the
last digits of a printed float.

**Root cause.** `reduce_all` in `src/tensor.tw` was a plain running sum at every
size. The Go bootstrap's `parallelSum` adds in fixed 4096-element blocks and
combines the partials in block order once past 8192 elements. Floating-point
addition is not associative, so the two summation orders give different last
bits, and because the goldens compare a canonical float rendering byte for byte,
that is a test failure and not a curiosity.

The mean scaling was off the same way: the bootstrap forms `s * (1.0 / n)`, not
`s / n`, and the two round differently.

Whether this is a bug depends on a decision, and the decision is recorded in
`docs/DECISIONS.md` entry 6: parallelism never changes a result, and the fixed
block size is what pins the answer on any machine. Given that rule, an
implementation that sums in a different order is wrong.

**Fix.** `2a4b489`. `block_sum` ports the Go form, with `acc_add` standing in for
the bare `+` so narrow dtypes accumulate at their own width. On f64, where
`acc_add` is f64 addition, the order is bit-identical to `parallelSum`.

**Regression test.** The golden corpus itself, which is the point: the goldens
compare byte for byte, so any future reordering of a reduction fails
immediately rather than drifting.

---

## 9. A shape mistake that panicked instead of being reported

**Symptom.** `zeros(-2, 3)`, and the same for `ones`, `randn`, `rand`, `fill`,
`eye` and `linspace`, passed the checker and then panicked at runtime inside
`make([]float64, n)`:

```
makeslice: len out of range [recovered, repanicked]
```

which names neither the call nor the cause.

**Root cause.** No check. The checker folds these constructors to constant shapes
in order to type them, so the negative dimension was sitting right there in the
one place that could have reported it, and nothing looked at it.

This is the worst failure mode a shape mistake can have in a language whose
entire premise is that shape mistakes are reported before the program runs.

**Fix.** `1d6dcdd`. `reportNegDim` flags the first negative dimension with the
constructor named, and the constructor types as Unknown rather than as a bad
shape. Both the Go checker and the self-hosted `src/check.tw`, byte-identical on
both paths.

**Regression test.** `internal/checker/falseneg_test.go`, and the differential
harness confirms no valid constructor is over-flagged.

---

## 10. `where` with a uniform condition panicked on the backward pass

**Symptom.** This program crashes:

```
let f = fn(x) = sum(where([1.0, 1.0], x * 2.0, x * 3.0))
print(grad(f)([0.2, 0.9]))
```

with `index out of range [0] with length 0` inside `broadcastBinary`'s backward
closure. Changing the condition to `[1.0, 0.0]` makes it run. The crash needs the
condition to select the *same* branch at every element, which is why it had
survived: a mask that varies, which is what a mask usually does, never reaches it.

**Root cause.** `Where`'s backward closure routes the cotangent element by
element:

```go
if cond.Data[idx(o, effC)] != 0 {
    if a.RequiresGrad { a.ensureGrad()[idx(o, effA)] += g[o] }
} else if b.RequiresGrad {
    b.ensureGrad()[idx(o, effB)] += g[o]
}
```

`ensureGrad` is what allocates a tensor's gradient buffer, and it is called only
on the branch a given element selected. A condition that is true everywhere
therefore never allocates `b`'s buffer. `b` is still a parent, so `Backward`
reaches it in the topological order and calls its closure, and that closure opens
with `g := res.Grad` and then indexes `g[i]`. `res.Grad` is nil.

The general shape of the mistake is worth naming, because it is not specific to
`where`: an operator whose backward routes the cotangent to only *some* of its
parents leaves the others' gradient unallocated, and every closure in the package
reads its own `res.Grad` without checking. `where` is the only operator that does
this today, but nothing stopped the next one.

**How it was caught.** The codegen differential harness,
`internal/codegen/differential_test.go`. It generates random programs and
differentiates them through both the interpreter and the compiler, and one of the
500 contained `where(x > y, x, y)` over operands where the comparison came out
uniform. The panic was in the *interpreter* side of the comparison, so the
compiler found a bug in its own reference. Nothing about the compiler is
implicated; what the harness supplied was a program nobody would have written by
hand.

**Fix.** In `Backward`, skip a node whose `Grad` is nil. It received no cotangent,
so its backward would propagate zeros and there is nothing to do. That fixes the
general shape rather than the `where` instance.

**Regression test.** `TestWhereUniformConditionBackward` in
`internal/tensor/gradcheck_test.go`, both directions of the uniform condition,
asserting the surviving branch's gradient rather than only that it does not crash.

---

## 11. A function defined twice was silently the second one

**Symptom.** Neither checker said anything, and the program ran the later
definition:

```rust
mode systems
fn f() -> Str = "first"
fn f() -> Str = "second"
fn main() { print(f()) }
main()                     // second
```

**What it cost.** `twill-lang/spool` replaced four insertion sorts with calls to
the new `sort` builtin. Two of the four had their one-line replacements written
*above* the bodies they were meant to replace, and those bodies stayed. Both
files then defined the function twice, both kept running the insertion sort, and
the commit said four sorts had been replaced when two had. The tests passed, the
source gate passed and CI passed, because nothing in any of them looked at
whether a name was defined twice. `twill-lang/spool#4` is the correction.

**Root cause.** The prelude pass that registers every top-level function name
before any body is checked did exactly that and no more: a second `FnDecl` of a
name already in scope simply rebound it. The evaluator agreed, last one winning,
so the two halves of the language were consistent with each other and wrong
together.

**Fix.** That same pass now keeps the line each name was first declared on and
reports the second:

```
f is already defined on line 1; the later definition is the one that runs,
so the earlier one is dead. Delete whichever is stale, or rename one.
```

It names the winner, because the whole failure is someone believing the other
one won, and it points at the redefinition rather than the original, which is
the line to go look at. Both checkers carry it and the two messages are
byte-identical, which the differential harness requires.

There is no conditional compilation in this language, so the readings under
which somebody means a second declaration -- a platform variant, a debug
build -- do not exist, and the false-positive rate should be zero. It measured
zero: 458 `.tw` files across `src`, `std`, `testdata`, `examples` and the six
satellites, no hits. Against `spool@HEAD~1:src/strutil.tw`, the real file before
the fix, it reports line 340.

**Regression test.** `internal/checker/redefine_test.go`: the message, its line
number, its severity, one report per redefinition, and the two things that must
not trip it -- a local shadowing a function name, and a function deliberately
shadowing a builtin, which twill supports.

---

## 12. The self-hosted evaluator had no list, dictionary or string runtime

**Symptom.** A systems-mode program that used any of the names the systems
dialect is actually written in ran under the bootstrap and refused under the
self-hosted evaluator:

```
runtime error: builtin "arr_new" is named in the builtin table but has no
implementation
```

128 of the 247 names in `src/builtins.tw` were in that state, which is the
message `src/eval.tw` prints when a name reaches the end of its dispatch. Two
of them did worse than refuse, and those two are open divergences 1 and 2 from
the section below:

```rust
let a: Arr[I64] = [3, 1, 2]
print(str(a))       // bootstrap: [3, 1, 2]   self-hosted: tensor([3, 1, 2], shape=[3])
let s: Str = "abc"
print(str(len(s)))  // bootstrap: 3           self-hosted: runtime error: len expects a tensor or list
```

**Root cause.** Two different ones, which is why the entry is about a runtime
and not about a builtin.

The list, dictionary and buffer builtins had no dispatch because the evaluator
had no value to put behind them: `Value` had `VList` and `VRecord` and nothing
else, and the comment on the annotation rule said so out loud -- "a dictionary
is not one of this evaluator's values -- there is no VDict in the Value enum
above". A missing case in an enum is a quiet thing. It reads as a decision, and
what it actually was is the work not being done.

`len` and the `Arr` annotation are different: both had a case and both had the
wrong one. `len` handled a tensor and a list and refused everything else, where
the bootstrap also answers a `Str`, a `Dict` and a `Bytes`. The annotation rule
converted only the *empty* bracket literal, where the bootstrap converts any
literal of rank 0 or 1. In both, the port had stopped at the case the author
had in front of them, and nothing compared the two implementations at runtime
to say so.

**How it was caught.** Not by any gate in the repository. `tools/diff/` runs
`check` and `fmt` over 443 files and says nothing about what a program prints,
and the self-hosted tests in `internal/interp/selfhost_test.go` all invoke
`check`. So the two evaluators were free to disagree, and the divergences were
found by hand and written down rather than by anything that would notice them
again.

**Fix.** This commit. `VDict` and `VBytes` join the `Value` enum, each wrapping
the host's own `Dict` and `Bytes` so a store through one binding is seen
through every other, and 31 names are dispatched: the `Arr` family, the `Dict`
family, the two byte-buffer families, and the string primitives. Where the host
has the same primitive -- `pop`, `arr_clear`, `dict_del`, `buf_get8`,
`str_quote`, `f64_hex` -- the body delegates to it rather than reimplementing
it, which makes the answer identical by construction instead of identical by
inspection.

**Regression test.** `TestSelfHostedRunMatchesBootstrap` in
`internal/interp/selfhost_run_test.go`, which is the differential harness over
`run` that the section below asks for. Each fixture in
`internal/interp/testdata/selfhost/` is executed twice over the same bytes, by
the bootstrap and by `src/eval.tw` driven through `src/main.tw`, and stdout is
compared byte for byte and so is the runtime error line -- a message is as much
of a builtin's behaviour as its answer, and half the fixtures exist to pin one.

---

## Open

**The self-hosted evaluator still refuses 97 of the 247 builtin names.** Entry
12 above ported 31 of them and closed divergences 1 and 2 of the three recorded
here. What is left is the filesystem, the clock, the process, the RNG, the
`f64_*` scalar intrinsics, the GPU stubs and the memory counters, each of which
still ends at "named in the builtin table but has no implementation". The count
is measurable rather than estimated: run each remaining name through
`src/main.tw run` and read the first stderr line.

**`src/cli/main.tw` does not call a systems-mode program's `main`.** This is
divergence 3 of the three, and it is still open. `src/main.tw` grew a `run_main`
and calls it; the decorated driver in `src/cli/main.tw` runs the top level and
exits, so a systems-mode program run through it defines `main`, prints nothing
and succeeds.

**Diagnostics where the self-hosted evaluator words a refusal differently.**
Each is the same shape: the port improved the message and the harness compares
text. None of the four in the table is a wrong answer. Two further members of
the last row's family were added by the strings, Arr and Dict port itself, and
are listed under the table rather than folded into it, because a divergence a
change creates should be readable as such:

| program | bootstrap | `src/eval.tw` |
| --- | --- | --- |
| `a["x"]` on a list | `index must be a scalar number` | `an index must be a number` |
| `a[5]` on a 1-list | `list index 5 out of range` | `index 5 out of range for a list of length 1` |
| `true[0]` | `value is not indexable` | `cannot index a bool` |
| `for x in 1.0` | `can only iterate 1-D tensors` | `cannot iterate a number` |

Added by this port, in the same family: `for k in <dict>` and `for x in <bytes>`
answer `value is not iterable` on the bootstrap and `cannot iterate a dict` or
`cannot iterate a bytes` self-hosted. Both are refusals on both sides.

**Truthiness in a condition refuses self-hosted where the bootstrap runs.** This
is a worse shape than the wording rows above, and it is recorded separately for
that reason. `let d: Dict[Str, I64] = dict_new()  dict_set(d, "a", 1)  if d { }`
runs on the bootstrap and dies self-hosted with `condition must be a bool or a
scalar number, got a dict`. The behaviour is pre-existing for lists, records and
strings, which main already refuses the same way. The dict and bytes instances
are new here, because neither value existed self-hosted before this port. The
right fix is to decide once what a non-boolean condition means in twill and make
both evaluators agree; until then this is a refusal where the other side runs,
which is exactly what the conformance allow-list is for.

**Nested assignment through a tensor is silently dropped self-hosted.** Out of
scope for this port and recorded so it is not lost: `let m: Tensor = [[1.0,
2.0], [3.0, 4.0]]  m[0][1] = 9.0` mutates on the bootstrap and does nothing at
all self-hosted, with no error. That is a wrong answer rather than a refusal,
and it is the most serious item on this page. `src/eval.tw` has no port of the
Go `assignNested` path.

**`einsum` refuses a label repeated within one operand.** `einsum("ii->", A)`,
the trace, returns "repeated label \"i\" within one operand is not supported".
This is a refusal rather than a wrong answer, so it is a limitation and not a
bug, and it is recorded here because writing the gradient check is what found it
and the case had to be replaced with a different one. A diagonal cannot currently
be taken through `einsum`.

**The checker is incomplete, by design.** It accepts programs that fail at
runtime whenever a shape depends on a value it cannot fold.
`docs/CORRECTNESS.md` gives a six-line example and states what is and is not
being claimed.
