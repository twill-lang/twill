# Changelog

## [Unreleased]

### Added

- **A recursion limit, so a missing base case is a twill error and not a Go
  crash.** Both evaluators refuse a call nested more than 10,000 deep:

  ```
  bad.tw:2: runtime error: call depth limit reached: "fact" is 10000 calls
  deep, which is as deep as twill goes. A recursion this deep is almost always
  a missing base case; if it is not, rewrite it as a loop
    2 |   n * fact(n - 1)
  ```

  What it replaced was 424 lines of Go runtime internals and no exit status. A
  Go stack overflow is a *fatal* error: no recover catches it, so the only way
  to get a diagnostic at all is to refuse before the stack runs out. `fn fact(n)
  = n * fact(n - 1)` with the base case forgotten is the most likely first
  mistake anyone makes in a new language, and it was the most likely first thing
  they saw.

  The number is measured. The deepest legitimate recursion anywhere in this
  repository or the nine satellites is the self-hosted compiler checking
  `src/parse.tw`, which nests 217 calls; everything in `examples/`,
  `std/tests/`, `testdata/` and the satellites' own tests and examples stays
  under 30. The bootstrap's stack gives out between 80,000 and 120,000 nested
  calls. 10,000 is about 46x above the deepest real program and about 8x below
  the crash.

### Changed

- **A fault inside the interpreter is reported as a twill error, not a Go
  traceback.** `argmax(zeros(0))` used to print a goroutine dump; it now prints
  the line the program had reached and says whose bug it is:

  ```
  bad.tw:1: runtime error: internal error: index out of range [0] with length 0.
  This is a bug in twill, not in the program that hit it: please report it, with
  this file, at https://github.com/twill-lang/twill/issues
    1 | print(argmax(zeros(0)))
  ```

  The person at the keyboard is running a twill program and cannot act on a Go
  stack. This is the second half of the recursion limit and not a replacement
  for it: the one fault most likely to be hit, a stack overflow, is the one
  fault a recover cannot catch.

- **A function defined twice in one file is now refused.** Both checkers report
  the second declaration, saying which one runs:

  ```
  f is already defined on line 1; the later definition is the one that runs,
  so the earlier one is dead. Delete whichever is stale, or rename one.
  ```

  The evaluator took the last definition and said nothing, so a replacement
  written above the body it was meant to replace left the old body running and
  the file read as though it did not. spool shipped exactly that in two files
  during the 1.9.0 sort adoption, through a passing test suite, a passing source
  gate and passing CI. There is no conditional compilation in this language, so
  a second declaration of one name in one file is an edit that went wrong; a
  sweep of 458 `.tw` files across the ecosystem found no case that was not.
  `docs/BUGS.md` entry 11.

## [1.9.0] - 2026-09-03

The release the ecosystem's eleven hand-written sorts were waiting for. Minor
rather than patch because `sort` takes an argument it used to refuse; nothing it
accepted before changes meaning.

### Added

- **`sort` orders more than strings, and takes a comparison.** `docs/roadmap.md`
  ranks missing features by how many of six independently written codebases hit
  each wall, and this one had written its own sort five times over: spool four
  insertion sorts, bobbin two, weft one, loom one, and skein a bottom-up merge
  sort over an index array. Only a list of strings could delegate to the builtin
  -- `sort([3, 1, 2])` on an `Arr[I64]` failed with "sort on a list expects every
  element to be a string" -- so none of them could.

  ```rust
  sort(xs)                          # ascending, by the elements' own order
  sort(xs, true)                    # descending
  sort(xs, fn(a, b) = a.n < b.n)    # by a comparison: does a come before b?
  ```

  Numbers order as numbers, `I64` and `F64` alike. A list of anything else needs
  the comparison, and a list mixing strings with numbers says so rather than
  picking an order nobody asked for.

  **Every form is stable**, which is a correctness property here rather than a
  nicety: skein assigns token ids from a sorted vocabulary, so an unstable sort
  would make the ids a function of the sort's internals rather than of the
  corpus. The test that pins it is deliberately twenty-four elements long, and
  the first version of it was not -- three elements with one tie passed against
  `sort.Slice`, because Go falls back to insertion sort under about a dozen
  elements and insertion sort happens to be stable.

  The comparison takes two elements rather than a key, because the case that
  needs it most is skein's: sorting an index array by comparing through a second
  array the closure captures. That was only expressible once function values
  landed in 1.7.

## [1.8.0] - 2026-09-01

The release the satellites were waiting for: `twill-lang/spool` cannot fetch a
package without a process interface and `twill-lang/warp` cannot fetch a
dataset, and both had branches ready against an unreleased compiler. Minor
rather than patch because `run` is a new builtin; nothing in 1.7.1 changes
meaning.

### Added

- **A process interface: `run(program, argv, dir) -> Res[Str, Str]`.** spool's
  `docs/needs.md` had fourteen entries, thirteen of them delivered by 1.7.1, and
  entry 1 was the one still open: a package manager fetches by running `git
  clone`, `git rev-list` and `git checkout`, and no builtin started a program,
  so `src/vendor.tw` called a `run` that did not exist and every git source died
  with `undefined variable "run"`. The signature is the one that entry asked
  for, `Res` included.

  `Ok` carries stdout, and only on an exit status of 0. Everything else is
  `Err`: a program that could not start, a signal, a non-zero exit. stderr is
  the `Err` message and is never merged into `Ok`, because a caller parsing
  `git rev-list` output must not find a warning line spliced into it. `dir`
  resolves the way every other path in the runtime resolves, and `""` means
  beside the running program.

  **There is no shell on this path and adding one later would be a regression.**
  The program and its arguments stay separate values all the way to `execve`, so
  an argument reaches the program as text -- which is what a package manager
  needs, since its arguments are tags and URLs out of a manifest a stranger
  wrote. `TestRunNeverInterpretsAnArgumentAsAShellCommand` runs `echo` with an
  argument that would create a file if anything interpreted it, and fails if the
  file appears.

  The environment is inherited whole, deliberately: borrowing the user's
  credentials, proxy and host keys is the reason to shell out to git rather than
  speak the protocol, and an allowlist here would break authentication against a
  private repository. spool's entry asked for that widening to be a considered
  decision rather than a side effect, so it comes with an off switch:
  `TWILL_NO_EXEC`, set to anything non-empty, makes every `run` answer `Err`
  without starting anything -- an `Err` and not an abort, so a program degrades
  to what it can still do.

  Both checkers learned it together, so the bootstrap and the self-hosted
  toolchain agree on its arity and its type.

### Fixed

- **Two numbers that were wrong on Apple silicon and right on x86.** Both were
  standing test failures on arm64 that CI, which runs amd64, could not see.

  A gradient accumulation is written `g += d * cotangent` in every backward loop
  in `internal/tensor`, and Go permits a compiler to contract `x*y + z` into a
  fused multiply-add. arm64 takes that permission and amd64 does not, so the
  product kept its extra bits on one machine and not the other, and the same
  differentiated program answered two numbers one ULP apart. The compiler's
  gradient transform builds the multiply and the reduction as separate IR nodes,
  so its arithmetic could not fuse, and `TestGradTransformMatchesTensorBackward`
  compares the two bit for bit -- the test was doing its job for a year on the
  wrong architecture to notice. Each product now rounds where the language says
  rounding happens, an explicit conversion, and the two agree everywhere.
  Nothing moves on amd64.

  `FormatNumber` asked `n == float64(int64(n))` to decide whether a float was a
  whole number, and converting a float outside the int64 range is undefined in
  Go. On arm64 it saturates, so `int64(9223372036854775808.0)` is `MaxInt64`,
  whose `float64` is the number we started with -- the guard passed and
  `print(f64(9223372036854775807))` answered **9223372036854775807**, a value no
  program ever held. It goes through `IntOfNum`, which bounds the range first.

## [1.7.1] - 2026-08-21

A checker release. 1.7 gave the language its pattern language and its generics;
this closes the last thing the two checkers disagreed about, and makes the one
warning either of them emits behave like a warning.

### Added

- **The Go checker knows dtypes, and reports a lossy widening.** `src/check.tw`
  has carried a dtype on its tensor type since the dtype work landed and emits
  one warning -- a narrow float silently widened by a wider one, which is a
  perfectly correct answer that undoes the reason the operand was narrow
  (NEEDS-113). `internal/checker` had one numeric type and reported nothing, so
  the two checkers disagreed on every program that wrote a dtype.

  They now agree character for character:

  ```
  weights.tw:4: shape error: dtype widening: bf16 and f64 promote to f64, which
  undoes the reason the bf16 operand is narrow
  ```

  The dtype is stored as code+1 so the zero value means "not known", which is
  what each of the sixty-odd bare `tTensor{dims: ...}` literals in that file
  should say. It enters at a cast or a constructor's trailing name, defaults to
  f64 for a constructor without one and for a tensor literal, and rides through
  rearrangement, indexing, reductions and the unary operations on the rules the
  self-hosted checker already used.

  The hard half was reporting nothing else. A bare number literal deliberately
  carries no dtype -- only `scalar(x)` and a tensor literal are f64 -- and a
  float-only operation on an integer input degrades to unknown rather than
  claiming f32, so no chain of ordinary arithmetic can reach the warning. Both
  checkers were compared over 405 files of `std`, `src`, `examples` and
  `testdata/cases`: identical output, and not one new diagnostic on a program
  that never mentioned a dtype.

- **A warning no longer stops the program.** The checker has always had two
  kinds of finding -- almost everything it reports is an error, a shape or a
  type or a unit the program cannot have, while a dtype widening describes a
  program that means what it says and will run. Both CLIs printed them the same
  way and counted them the same way, so the one warning refused to run the
  program it was only commenting on. `src/check.tw` carried a `severity` field
  and recorded that main did not read it.

  It reads it now, and so does the Go bootstrap, which gained the field:

  - a warning prints as `warning:` rather than `shape error:`;
  - `run` prints it and runs the program, counting only errors in its refusal;
  - `check` prints it and exits 0, so a warnings-only file passes a CI gate;
  - `twill test` fails a suite only on an error;
  - the language server publishes it as LSP severity 2 (Warning) rather than
    painting it like a mistake to fix before running.

  An error is unchanged in every respect, and the two CLIs remain
  byte-identical on both kinds.

### Fixed

- **The self-hosted checker read `zeros(2, 3, bf16)` as shape `[2]`.**
  `infer_call` strips a constructor's trailing dtype name and passes it along
  separately; the constructor branch then stripped it a second time, taking a
  real dimension with it. The checker reported a shape the runtime does not
  build, so a correct program was told its shapes could not broadcast. The list
  form `zeros([2, 3], bf16)` lost its only argument and degraded to unknown
  instead, which is why this went unseen. Found by putting the two checkers
  side by side on dtype programs, which is what the new diagnostic needed.

## [1.7.0] - 2026-08-20

The two things the language was missing from the middle. 1.5 made the ecosystem
run and 1.6 stopped the language having holes in it; this one closes the two
entries `docs/needs.md` had been calling the largest open questions, and it
closes them on both implementations rather than on the bootstrap alone.

### Added

- **The pattern language (NEEDS-3).** A pattern used to be a variant name and at
  most one binder. It is now a tree, and three things are written that could not
  be written before:

  - **Nested patterns.** `Ok(Some(v))`, `Some(Err(e))`, `Leaf(Branch(xs))`. The
    payload of a case is itself a pattern, and it nests as far as the value
    does.
  - **Literal patterns.** `3 => ...`, `"hi" => ...`, `true => ...`, `Err(-1)`.
    A literal matches by the same equality `==` gives, which means a `match`
    over numbers or strings needs no enum written around it.
  - **Guards.** `Some(v) if v > 10 => ...`. The guard sees the pattern's
    bindings and is the last word on whether the arm runs; a false guard falls
    through to the arms below rather than failing the match.

  A lower-case name now binds the value it matches instead of naming a case, so
  `other => ...` is a `_` that says what it caught. An upper-case name is a
  case. That rule is what lets `Some(x)` read x as a binder and `Ok(None)` read
  None as a case, and every enum variant in the language and its libraries is
  upper-case initial, so no existing program changes meaning. A lower-case name
  applied to a payload is refused by name: `some(v)` says that a case name
  starts with a capital letter.

  Exhaustiveness was rewritten to match, and it is more precise than it was
  rather than merely still true. It now recurses: `Some(Ok(v))`, `Some(Err(e))`
  and `None` together cover an `Opt[Res[..]]`, and dropping the `Err` arm names
  the value that gets through -- `missing Some(Err)` -- instead of saying the
  match is fine. The rule underneath is that an arm counts only when nothing but
  the value's shape decides whether it runs, so a guarded arm and a narrower
  nested one prove nothing, and a position holding only literals is left
  unjudged unless it is a Bool, which two literals do exhaust.

- **User-defined generics (NEEDS-4).** `struct Box[T]`, `enum Tree[T]` and
  `fn first[T](xs: Arr[T]) -> T` parse, check and run. `Arr`, `Dict`, `Opt` and
  `Res` have been generic and checked since 1.5; a declaration in a twill
  program could not be, and `[` after the name was a syntax error.

  There is no monomorphization, and none is needed: the runtime is the same code
  whatever T is, so the parameters have to reach the types the checker judges
  against and nothing else. They do reach them. A `Box[I64]`'s field is an I64,
  not an unknown; a `Tree[Str]` matched with `Leaf(v)` binds v as a Str;
  substitution goes under the constructors a parameter is written inside, so a
  payload declared `Arr[T]` in a `Tree[I64]` is an `Arr[I64]`; and two uses of
  one declaration are different types when their arguments differ, so a
  `Box[Str]` is refused where a `Box[I64]` was declared. Non-generic structs
  still compare by name exactly as before.

  A value's arguments are read back out of what it is built from rather than
  written at the use site, because there is nowhere to write them: `Leaf(n)`
  with an I64 n is a `Tree[I64]`, and `Pair { first: b, second: a }` in a
  `swap[A, B]` is a `Pair[B, A]`. A parameter the use site left unbound
  substitutes to an unknown and judges nothing, so a bare `Box` says exactly
  what it said before 1.7.

### Fixed

- **`twill fmt` would have deleted every type parameter.** A printer with no
  case for `[T, U]` does not fail, it silently emits `struct Box { value: T }`
  -- a program that no longer parses, written over the original under
  `--write`. This is the third time a formatter gap has been found this way
  (`unit USD` in 1.6, the parentheses a postfix needs in 1.6.2), so both
  printers now have the case and a round-trip test asserts the parameters and
  the new pattern forms both survive.

- **A module-qualified pattern kept working.** `ast.EBool(b)` resolves its
  qualifier before the new upper-case rule is applied, because the qualifier is
  a module alias or a type name and either may be lower-case: what decides is
  the name after the dot. Without that ordering the self-hosted checker, which
  is written in that style throughout, stopped parsing.

### Parity

Both features landed on the Go bootstrap and in `src/` together, which is the
check this project exists to be able to make. The two checkers were compared
character for character on 404 files -- all of `std`, `src`, `examples` and
`testdata/cases` -- and agree on every one, and the two formatters produce the
same bytes for the new syntax.

## [1.6.7] - 2026-08-20

### Added

- **The self-hosted checker has the systems-mode type layer.** Until now
  `src/check.tw` knew nine types and none of them was `I64`, `Arr` or `Dict`,
  so `let x: I64 = "hello"` was caught by the Go checker and waved through by
  the self-hosted one. The two checkers are supposed to prove the same things,
  and on systems-mode annotations they did not.

  Ported from `internal/checker/systems.go`, which is the specification: the
  seven type cases, the annotation parser, `assignable` with its deliberate
  softness (`I64` and `F64` stand for each other, an unknown judges nothing),
  and the wiring into let annotations, parameters, declared return types,
  struct fields, enum payloads, ordering, and the builtins whose result type is
  fixed.

  Two things had to be fixed underneath it. The self-hosted parser could not
  parse `fn(I64) -> I64` in a type position at all, so the checker could never
  have seen a function type to judge it; it now parses function types wherever
  a type may appear, and they nest. And `src/builtins.tw` was missing four
  names the Go interpreter has had all along -- `f64_bits_hi`, `f64_bits_lo`,
  `f64_from_halves`, `arr_of_tensor` -- a parity gap that predates this work.

  The bar was byte-identical diagnostics, not merely equivalent ones, because
  equivalent-but-differently-worded is how the two implementations drift apart
  where nothing is watching. 594 files were checked by both -- all of `std`,
  `src`, `examples`, `testdata/cases` and the nine ecosystem repositories --
  and the two checkers agree on every one, character for character.

## [1.6.6] - 2026-08-19

### Fixed

- **The race pass fits its budget again.** 1.6.5's CI timed out at 25 minutes
  on `go test -race -short`, and the cause was the suite having grown rather
  than anything being slow: the two model-training examples take fifteen
  seconds each where every other example is milliseconds, and the corpus is
  walked twice, once as written and once as formatted.

  `-short` now skips those two examples and the self-hosted differential runs.
  That is not running them less -- the ordinary CI pass has no `-short` and runs
  every one of them on every build. It is that the race detector is looking for
  races in the parallel tensor kernels, and a single-threaded tree walk over a
  twill source file is not where those live. The short pass went from 164
  seconds to 46, and 252 of the 306 tests still run in it.

  Recorded because the first attempt was wrong and the measurement said so:
  cloning a gradient leaf only when the tape already held that object, to avoid
  the copy, was **slower** than cloning unconditionally -- the backwards scan
  that decides costs more than the copy it saves. The unconditional clone
  stands.

## [1.6.5] - 2026-08-19

### Added

- **`stop_grad(x)`** -- the same values, outside the graph, so a gradient
  reaching it stops. It is what a stabilisation needs when the rewrite it
  performs is not an exact algebraic identity: `std/nn`'s `rms_norm` gets away
  without it because its reassociation *is* exact and the extra terms cancel,
  while a rescaling that merely improves conditioning does not, and
  differentiating through one gives the derivative of the trick rather than of
  the function. It is also how a straight-through estimator, a target network
  and a moving average are each written.

### Fixed

- **`grads(f)(c, c)` -- one tensor passed as two arguments -- put the whole
  gradient on the second parameter and returned zeros for the first**, in the
  self-hosted evaluator. A node is found again by tensor identity, so two
  leaves holding the same object were indistinguishable and every operand
  inside `f` resolved to whichever was registered last. A leaf now owns a copy.
  The bootstrap was always correct.

## [1.6.4] - 2026-08-19

The release where the checker stopped having holes in it, and where twill got
the autodiff primitives and the model pieces a current transformer is built
from.

### Added

- **`jvp`, `vjp` and `hvp`**, the operations `grad` is built from.
  `jvp(f)(x, v)` is forward mode -- the value and the Jacobian times a tangent,
  in one pass, at the cost of that pass. `vjp(f)(x, v)` is reverse mode, and is
  what `grad` is with the cotangent fixed at 1. `hvp(f)(x, v)` is the Hessian
  times a vector without building the Hessian.

  Two things are deliberately **not** what JAX does, and the guide says so.
  `vjp` takes the cotangent as an argument rather than returning a pullback
  closure: the tape is the tensor graph, so a retained pullback would pin it,
  the gradient buffers accumulate rather than replace, and a closure that
  answers differently the second time is the plausible wrong number this
  project refuses. And `hvp` costs 2n+1 forward passes rather than one
  forward-over-reverse, because `grad`'s output has no history and
  `jvp(grad(f))` would return a silent zero -- it is a constant-factor win over
  building the matrix, not an asymptotic one.

  Checked against central differences, against each other, and against
  `jacobian`: worst discrepancy 3.2e-07 on a second central difference,
  everything else at or below 5.7e-10, and the adjoint identity exact to
  4.4e-16. `examples/jvp.tw` solves `Hz = g` by conjugate gradients using
  nothing but `hvp`.

- **The modern transformer, in `std`.** `rms_norm` (Llama-style, no mean
  subtraction, and it survives 1e200 where the naive form silently returns
  zeros), rotary position embeddings, `swiglu`/`geglu`, and `std/sample.tw`
  with temperature, top-k, top-p and a categorical draw through a seeded
  stream. `std/llama.tw` is the model assembled, with `examples/llama.tw`
  running it end to end.

  RoPE uses the **half-split** pairing, which is what released checkpoints are
  trained against; the interleaved convention is incompatible and produces
  fluent nonsense rather than an error, so an exact-value test pins it.

### Fixed

- **`m[i][j] = v` on a tensor of rank 2 or more wrote nothing.** Indexing a row
  hands back a copy, so the write went into the copy and vanished, with no
  change and no error. Lists of lists were never affected, because a list is a
  handle -- it was only ever wrong for the tensors numerical code is made of.
- **Five checker holes**: `[]` is a list rather than a tensor of shape [0], so
  `sum([])` is caught; a scalar has no axes, so `sum(1.0, 0)` is caught;
  `slice` is typed as the Str slice it is rather than propagating a wrong Arr
  type; reading a field of something that cannot have one is reported; and a
  file's own enum claims its variant names, where an imported enum sharing a
  name used to disable exhaustive matching for it with no diagnostic.
- **A gradient inside a gradient is refused by the self-hosted evaluator too.**
  It had only the direct check on `grad(grad(f))` and answered [0, 0] to the
  nesting written the long way round -- the silent zero the refusal exists to
  prevent.

### Known

The self-hosted `grads` is wrong when the same tensor is passed as two
arguments: the whole gradient lands on the second parameter. Distinct arguments
agree, and the bootstrap is correct.

## [Unreleased]

### Added

- **`jvp`, `vjp` and `hvp`: the composable primitives underneath `grad`.**
  `grad`, `grads`, `value_and_grad`, `jacobian` and `hessian` were always
  conveniences over two passes, and the two passes were not nameable. They are
  now.

  `jvp(f)(x, v)` is forward mode: `[f(x), J v]` in one evaluation of `f`, with
  the tangent `v` carrying the input's structure and shapes -- records, nested
  lists, tensors -- and the answer carrying `f(x)`'s shape.
  `vjp(f)(x, v)` is reverse mode: `[f(x), vᵀ J]` in one evaluation plus one
  backward sweep, with the cotangent `v` carrying `f(x)`'s shape and the answer
  carrying `x`'s structure. `grad(f)(x)` is `vjp(f)(x, 1.0)`, which is all it
  ever was.
  `hvp(f)(x, v)` is `H v` for a scalar `f`, without allocating the `[n, n]`
  matrix -- what Newton-CG and trust-region methods actually ask for.

  Both interpreters carry them, byte for byte, and `examples/jvp.tw` runs
  conjugate gradients on `hvp` for a Newton step.

  Two things are *not* what JAX offers, and both are decision 2 rather than an
  omission. `vjp` takes the cotangent as an argument instead of returning a
  pullback closure, because the tape is the tensor graph: there is no recorded
  program to replay, a retained pullback would accumulate into the same
  gradient buffers on its second call, and a closure that quietly answers
  differently the second time is the plausible wrong number this project
  refuses. And `hvp` costs `2n+1` forward passes rather than one
  forward-over-reverse pass, because the reverse pass is not re-differentiable;
  it beats `hessian(f)(x) @ v`'s `n(n+1)/2` passes by a constant factor and
  never allocates the matrix, and the guide says so rather than implying an
  asymptotic win it does not have.

## [1.6.3] - 2026-08-19

Three silent wrong answers in the self-hosted evaluator, all one cause, and the
one the source had already written down as waiting on it.

### Fixed

- **Control flow crosses an expression boundary.** The self-hosted evaluator
  reports each step's outcome as a `Flow` value rather than unwinding with a
  panic the way the bootstrap does -- deliberately, because a panic used as
  normal control flow is what `docs/self-hosting.md` narrowed `abort` to
  exclude. But every expression boundary collapsed that `Flow` to a plain
  value, and an `if` is an expression, so:

  `if c { return 9 }` computed the 9 and threw it away, and the function
  carried on to return something else. `if c { break }` never left its loop.
  And a failing `?` became the expression's value, so a function whose first
  step failed ran to the end with an `Err` bound to a variable that was
  supposed to hold an `I64`.

  Statement-position `if` and `match` now execute rather than evaluate, through
  `exec_if` and `exec_match_stmt`, which hand the `Flow` on. A failing `?`
  parks its failure and the next statement boundary turns it into a return --
  the same unwinding the bootstrap's panic does, expressed as a value.

  The three were the worst kind of defect a second implementation of a language
  can have: not a refusal, but a different answer, quietly.

  `src/eval.tw`'s own comment on `?` named this fix and said the feature was
  waiting on it.

- Four differential tests pin all three, comparing the two engines through
  their CLIs on `return` and `break` inside an `if`, `return` inside a match
  arm, and `?` failing at the top of a body, inside an `if` and inside a loop.

## [1.6.2] - 2026-08-19

Six bugs, found by two techniques that the repository had never applied to
itself: running the checker and the interpreter over the same program and
diffing, and running the Go bootstrap and the self-hosted compiler over the same
program and diffing. Neither half can find these alone, which is why they
survived a release.

### Fixed

- **`twill fmt` no longer changes what a program computes.** Two ways, both of
  which wrote the damage to disk under `--write`:

  An integer literal was printed through the f64 the parser produced, so
  `9007199254740993` came back as `...992`, `1234567890123456789` as `...768`,
  and `9223372036854775807` -- MAX_I64 -- as the float `9.223372036854776e+18`.
  Before 1.6 those were f64 values anyway; since 1.6 an `I64` literal is exact,
  so the formatter was silently changing the program. It prints from the digits
  now.

  Parentheses a postfix operator needs were dropped: `(x + y).to(i8)` became
  `x + y.to(i8)`, which casts `y` alone; `(p + q).field` read `q`'s field; and
  `(m + n)[0]` indexed `n`. shuttle's `src/quant.tw` is a real file this
  happened to, and it only surfaced because a second `fmt` produced different
  text from the first.

- **A function that falls off its end is reported.** `fn name(b: Bool) -> Str {
  if b { "yes" } }` returns `()`, and the check skipped every body that
  evaluated to Unit on the grounds that its `return` statements were checked
  where they stand -- but a body with no `return` at all also evaluates to Unit,
  so the commonest shape of the mistake was the one case never judged. An
  `if` with no `else` now types as Unit, which is the honest half of what it
  can produce.

- **Ordering a non-scalar tensor is reported.** `where(A > 0.0, A, B)` -- the
  masking idiom every array library has -- failed at run time for every
  non-scalar `A` while the checker, which knew the rank, said nothing.
  `greater(A, 0.0)` is the elementwise form that yields the mask.

- **A type name is not an unknown unit in numeric mode.** `let b: Bool = true`
  was rejected with "unknown unit \"Bool\" (declare it with `unit Bool`)" -- a
  false positive in the default mode, advising a change that would make the
  program worse. The types exist at run time in both modes; only the annotation
  syntax is documented as belonging to systems mode. A name that is not one of
  the language's types is still an undeclared unit and still reported.

- **The self-hosted checker knows the builtins 1.6 added.** All twenty-four of
  the filesystem, path, clock and memory primitives were added to the Go side
  and not to `src/builtins.tw`, so the self-hosted checker rejected the
  repository's own standard library: `std/io.tw` and `std/random.tw` both failed
  it on names the language has.

- **Both CLIs report the same version.** The self-hosted one said `1.5.1.1`,
  two releases behind.

## [1.6.1] - 2026-08-19

Two bugs in 1.6.0, both found by comparing the Go bootstrap against the
self-hosted compiler rather than by either one on its own.

### Fixed

- **`MAX_I64` is not a fraction.** `let mx: I64 = 9223372036854775807` was
  refused, with a message calling the commonest constant in the subset a
  decimal. The check read fractionality from a round trip through `int64`, and
  for a value at or above 2^63 that is an out-of-range conversion: it lands on
  `MIN_I64`, so the comparison disagreed with itself. It reads the value against
  `math.Trunc` now, which has no range to fall out of and reads `2.5e3` as the
  whole number it is. The same round trip decided whether an operand was an
  integer for `I64` arithmetic, so `mx + 1` was quietly computing in floating
  point.

  The interpreter's own tests never caught it because they do not run the
  checker, and `twill run` does.

- **The self-hosted CLI runs a systems-mode program.** A systems-mode
  program's entry point is `main()`, which the bootstrap documents and does;
  `src/main.tw` executed the top level and stopped, so the self-hosted CLI could
  not run the dialect the self-hosted compiler is itself written in. The
  differential harness compares numeric-mode programs, where there is no `main`
  to call, which is why nothing reported it. It also compared the Go half's
  top-level evaluation against the self-hosted half's full CLI, an asymmetry
  that only numeric mode hides; the new tests compare the two CLIs.

## [1.6.0] - 2026-08-19

The completeness release. Four things were true of twill before it and are not
now: an `I64` was a float, a systems-mode annotation was a comment, a `match`
could silently fail to cover its cases, and two different mistakes in autodiff
answered with a zero instead of an error.

1.6.0 is 1.6.0-rc2 with nothing added. The two candidates below are what it is
made of, kept as they were written rather than merged, because they record
something worth keeping: rc1 was the release, and rc2 is what nine
repositories found when they were moved onto rc1 and used it. Every one of
those findings came from code that was trying to do its job, and none of them
was reachable from twill's own sources.

Between rc2 and this tag the compiler did not change. What changed is that the
nine repositories now run their suites against it in CI rather than on one
developer's machine: 60 suites, nine repositories, green.

## [1.6.0-rc2] - 2026-08-18

Everything here came out of putting 1.6 to work: the nine ecosystem
repositories were moved onto the release, and what they hit is what is below.
Dogfooding is the only way most of it would have been found -- none of these
was reachable from twill's own sources.

### Added

- **`twill lsp`**, a language server. Diagnostics republished as you type,
  formatting, and hover reporting the inferred type and shape. Hover is the one
  worth having: in a tensor-first language the question you actually have is
  what shape something is, and it is answered from the checker without running
  anything, so `logits @ w` costs nothing to ask about and a gigabyte to
  evaluate. No completion, deliberately -- `docs/roadmap.md`'s own advice is not
  to build one before the semantic information is reliable.
  `editors/README.md` sets it up for Neovim and Helix, and says plainly why
  there is no VS Code client yet.
- **`read_file_at(path, offset, count)`**, a ranged read. `read_file` returns
  the whole file, so a reader following a growing log read all of it again on
  every poll and one processing a file larger than memory could not run at all.
  A short read at the end is not an error.
- **`mem_allocs`, `mem_bytes`, `mem_live_bytes`, `mem_counters_available`**, the
  counters bobbin designed its memory module around and then could not call.
  `mem_tensors` is -1, meaning not counted: counting tensors is an atomic
  increment at forty-odd construction sites on the hot path of every numeric
  program, which is a tax on everyone not benchmarking for a number that moves
  `mem_allocs` anyway.
- **`remove_all`, `rename`, `mtime`** and a **monotonic `mono_ns()`**, the names
  the ecosystem had already written. `rename_path` is `rename`; it was
  introduced in this cycle and nothing outside it can depend on the old
  spelling.

### Fixed

- **A `match` on an enum from another module is checked for exhaustiveness.**
  The checker reads one file, so it did not know an imported enum's other cases
  and said nothing -- and matching on an imported enum is how the ecosystem is
  written, so the check silently did nothing in exactly the place it was most
  wanted. An import is now followed for its enum declarations and nothing else.
  `Check` is unchanged and still pure; `CheckFile` is the entry point that reads.
- **A `match` arm's expression continues onto the next line.**

      Some(v) => "got "
        + str(v),

  was a syntax error, on a continuation legal in every other position. The arm
  body's own first token was setting the column the continuation rule measures
  against. Two repositories hit it on the same day and each extracted a helper
  function to work around it.
- **An `I64` can be saved.** `value.Int` went into the runtime and not into the
  save format, so `save` on anything holding one failed -- which is loom's whole
  checkpoint-to-disk path. It writes as eight bytes under its own tag rather
  than as an f64, which would have worked and corrupted exactly the values above
  2^53 that an `I64` exists for. Files with no `I64` in them are byte for byte
  what they were, and every older file still reads.
- **Ordering is type-checked.** `find() < 0`, where `find` returns an
  `Opt[I64]`, checked clean and failed at run time -- precisely the shape the
  mistake takes when a function that used to return -1 starts returning an Opt,
  so the checker could not be trusted to find the call sites of that migration.
  `==` and `!=` are deep equality and stay defined on everything.
- **A release candidate publishes as a prerelease.** `v1.6.0-rc1` was published
  as the repository's Latest release, ahead of the stable version, so anyone
  landing on the page was offered a candidate as the recommended download.

## [1.6.0-rc1] - 2026-08-18

The completeness release. 1.5 made the ecosystem run; this one makes the
language stop having pieces missing from the middle of it. Four things were
true of twill before this release and are not now: an `I64` was a float, a
systems-mode annotation was a comment, a `match` could silently not cover its
cases, and two different mistakes in autodiff answered with a zero instead of an
error.

Nothing in numeric mode changes. A program with no `mode systems` line and no
type annotations behaves exactly as it did, which is what the mode gate is for.

### Added

- **`I64` is a real 64-bit integer.** It was an `f64` that happened to hold an
  integer, so it held 53 bits: `9007199254740993` printed as
  `9007199254740992`, `MAX_I64 + 1` did not wrap, `shl(1, 63)` could not be
  represented, and every hash mixer and 64-bit generator anyone wrote in twill
  was quietly wrong. That is `docs/needs.md` NEEDS-2, the oldest open entry in
  the file, and it is closed.

  An exact `Int` arises where the program said `I64` -- an annotation on a
  binding, a parameter, a return, a struct field or an enum payload, the `i64()`
  conversion, an integer literal too large for an `f64` to hold, and any
  arithmetic between two of them -- and never from a bare small literal, which
  is what leaves numeric mode alone. Arithmetic wraps; `/` and `//` truncate
  toward zero and `%` takes the sign of the dividend, per the guide; division
  and modulo by zero are errors rather than an infinity and a NaN; the bitwise
  words are exact on all 64 bits including the sign; and comparisons and
  dictionary keys are exact above 2^53. A property test runs 300 random pairs
  through every operator against Go's `int64`.

  `docs/language-guide.md` specified all of this in 1.5 and the implementation
  did not have it. The specification did not move.

- **`match` must cover its cases.** The checker knows each enum's cases and
  names the ones with no arm:

      model.tw:14: shape error: match on Verdict is not exhaustive: missing Noisy, New

  With four related mistakes reported the same way: an arm that repeats a case,
  an arm after `_`, a `_` when every case is already handled, and arms naming
  cases of two different enums. This is the reason to have an enum: adding a
  case now makes every `match` that has not been updated say so, at check time.
  NEEDS-3.

- **The systems-mode types are checked** (NEEDS-49). `I64`, `F64`, `Bool`,
  `Str`, `Bytes`, `Unit`, `Arr[T]`, `Dict[K, V]`, `Opt[T]`, `Res[T, E]`, a
  struct by name, an enum by name and a declared function type are types the
  checker knows, and a definite mismatch is reported at a binding, an argument,
  a return, a struct field at construction and at assignment, and an enum
  payload. `let x: I64 = "hello"` used to pass.

  The policy is the shape checker's: report only what is certain, and judge
  nothing where a type is unresolved. `docs/self-hosting.md` section 1.3 asks
  for the stricter one, where an unknown surviving inference is itself an error;
  that is not this, and the reason is written down in the guide.

- **`?` is checked** (NEEDS-10). `?` outside a function, in a function that does
  not return a `Res` or an `Opt`, or on a value that is neither, is a
  diagnostic; and what it yields is typed, so `let n: I64 = read_file(p)?`
  reports rather than waits. A failing `?` at the top level of a file used to
  end the program with status 0 and no message, which is the one thing a failed
  read must not do.

- **The rest of the filesystem** (NEEDS-91, NEEDS-92). `path_exists`,
  `path_is_dir`, `mtime`, `mkdir_all`, `remove_file`, `remove_dir`,
  `remove_all`, `rename`, `temp_dir` and `cwd`, each returning a `Res` where it
  can fail for a reason a caller may handle and a `Bool` where "it is not there"
  is the answer. A program could read a file and write one and that was all: it
  could not make a directory to write into, remove what it wrote, or ask whether
  a path existed without reading the whole file to find out. selvedge has seven
  leftover `tmp_` files committed to its repository because a test had no way to
  clean up after itself.

  And the path operations, which are string handling and touch nothing:
  `path_join`, `path_base`, `path_dir`, `path_ext`, `path_stem`,
  `path_normalize`, `path_is_abs`. They emit a forward slash on every platform,
  because a program's paths are written in its source and one that renders them
  differently on Windows writes a different manifest there.

- **`mono_ns()`**, a clock that only goes forward (NEEDS-39). `clock_now_ms` is
  the wall clock and steps when the system's time is corrected, so a duration
  measured across a correction is wrong by the correction -- for a benchmark,
  the difference between a number and a fiction.

- **`std/gradcheck`**, a gradient checker. `grad(f)` gives an answer very
  quickly and there is nothing about a wrong one that looks wrong: a model with
  a bad gradient does not crash, it trains to a worse loss than it should have,
  and the search for why starts at the learning rate. `check_at(f, x)` and
  `check_tree(f, params)` compare the gradient against a central difference
  quotient -- the same derivative reached by a different route, which is why it
  is deliberately not built out of `grad` -- and report the relative error they
  were reached from, because 1e-3 and 1e+0 mean different things.

- **`twill doctor`**, which answers the question a bug report starts with, and
  checks the things that are wrong quietly: a stale binary earlier on `PATH`
  than the new one, a `TWILL_STD` left pointing at last month's working copy, a
  standard library that does not load. **`twill test --filter <sub>`** runs the
  suites whose path contains a substring. **`twill --version --verbose`** prints
  the build. In the REPL, **`:type`** and **`:shape`** answer from the checker
  without running anything, which for a tensor-first language is the most useful
  question there is: `:shape randn(4096, 4096) @ w` costs nothing here and a
  gigabyte there.

### Fixed

- **A gradient taken inside a gradient is refused wherever it is written.**
  `grad(grad(f))` was already refused, by inspecting the argument, so the same
  mistake with a function between the two gradients passed the check and
  returned zeros. Reverse mode is first-order and the value it hands back has no
  history; differentiating it again differentiates a constant. `hessian` and
  `jacobian` nest legitimately and are unaffected.

- **`tensor(list(...))` no longer drops a gradient.** It copied the numbers out
  into a fresh buffer, which has no gradient rule, so `jacobian` of a function
  that built its output that way returned a matrix of zeros -- silently. A list
  holding a value under differentiation is assembled through `concat` now. A
  list of plain numbers takes the old path unchanged.

- **`twill fmt` no longer deletes `unit` declarations.** The printer had no case
  for one, so its statement switch fell through silently and `--write` removed
  the declaration from the file; every annotation naming that unit then failed
  to check. Fixed in both the Go printer and the self-hosted one at once, which
  is what NEEDS-77's note was waiting for. A corpus test over 461 files now
  holds the formatter to parsing, idempotence, keeping every comment, and
  keeping every statement.

- **`std/io.read_text` and `read_lines` failed on every call.** They were
  written against a `read_file` returning `Bytes` and it returns a `Str`. The
  checker found it: `?` yields the success payload's type now, so the mismatch
  is a diagnostic rather than a runtime error attributed to the caller's file.

- **Enum values compare structurally.** `Some(1) == Some(1)` was `false`, and
  two separately built `Ok(3)` were not equal, because a payload-carrying
  variant fell through to pointer identity. Dictionaries and byte buffers
  compare by contents for the same reason.

- **`i64_of_str` returns an exact integer.** It handed its answer back as an
  `f64`, rounding above 2^53 -- the values that are the only reason to parse an
  `I64` from text.

### Changed

- `rename_path` is spelled **`rename`**, matching the group it belongs to and
  the name the ecosystem had already written. It was introduced in this release
  and nothing outside it can depend on the old spelling.

- `std/io.exists` asks the filesystem instead of reading the whole file, and
  `std/io.is_dir` asks instead of listing the directory.

### Compatibility

Numeric mode is untouched: `%` is still floored there, `/` is still exact
division, and no annotation is required or checked, so a numeric-mode program
runs as it did. The 60 ecosystem test suites across the nine repositories pass
unchanged, and `twill check` over all nine reports 10 unresolved names, all of
them primitives that genuinely do not exist yet, down from 31 before this
release.

Systems-mode code can newly fail to check, which is the point of the release,
and every diagnostic names what it found and what was declared. Three behaviours
change at run time for a program that was relying on them: an `I64` division or
modulo by zero is an error rather than an infinity or a NaN, `%` on two `I64`s
takes the sign of the dividend rather than the divisor, and a failing `?` at the
top level of a file stops with a message rather than exiting 0.

## [1.5.1.1] - 2026-08-15

`std/random` draws from the host, and an annotation now settles what a bracket
literal means. Together these took the ecosystem from 45 of 60 test suites
passing to 57, with seven of the nine repositories fully green.

### Added

- **An annotation chooses the container, in the two cases the literal cannot.**
  A bracket literal is a tensor when its elements are numeric literals and a
  list otherwise, which makes `[1.0, 2.0]` a tensor and `[v, v]` a list. That is
  the right default with nothing else to go on, and wrong wherever the author
  said what they meant:

      let want: Arr[I64] = [1]           // a list of one, not a 1-element tensor
      fn row(v: F64) -> Tensor = [v, v]  // a tensor, not a list

  Both were unusable as written -- the first failed on every later `arr_push`,
  the second on every later `shape` -- which is what the annotation was there to
  prevent. This extends the rule 1.5.0 introduced for `{}` at a `Dict`
  annotation and 1.5.1 for a struct field. Only flat lists of numbers convert at
  a `Tensor` annotation; a list holding a string or a record is one the caller
  built on purpose.

### Changed

- **`std/random` is backed by host generator streams** rather than implementing
  xoshiro256**/splitmix64 in twill. Both algorithms are defined on 64-bit
  wrapping arithmetic and an `I64` here is carried in an `f64`, so it holds 53
  bits: every multiply overflowed into the rounding, the state saturated, and
  the generator returned the same value forever. It was deterministic and
  plausible and completely wrong, which is the worst way for a generator to
  fail. That is NEEDS-2, and it is not fixable at this representation.

  New builtins `rng_open`, `rng_close`, `rng_u53`, `rng_f64` and `rng_norm`
  give independent streams named by handle, the same way a fitted gbm model is.
  `rng_open` is what the existing global `rng_seed`/`rng_uniform` could not
  provide: a sampler wants several streams alive at once, seeded separately.

  The caller-visible contract is unchanged — `new_rng(seed)` is reproducible run
  to run and two seeds are independent from the first draw — and the rest of the
  module is still twill, because the interesting part of a random library is the
  rejection loop in `below` and the retained Box-Muller spare in `normal`, not
  the bit mixer. What is gone is the ability to read the algorithm in the source
  and the guarantee that a stream is identical across implementations.

  **This changes every recorded draw.** A saved result that depended on the old
  stream will not reproduce — but the old stream was a constant, so there was
  nothing worth reproducing.

### Fixed

- `std/tests/random_test.tw` now passes, for the first time. It had been a
  documented known-failure. Its exact-stream assertions were replaced by the
  properties a caller can actually depend on: that a seed reproduces, that
  adjacent seeds are independent, that `below` is unbiased across its whole
  range rather than merely in range, and that `normal` returns the cached
  partner of its Box-Muller pair. Pinning the host's stream would assert an
  implementation detail of whatever Go ships.


## [1.5.1] - 2026-08-15

The rest of the ecosystem sweep. 1.5.0 took the nine sibling repositories from
8 of 60 test suites passing to 34; this takes them to 45, with six of the nine
fully green. As before, the changes here are the shared causes rather than
per-repository patches.

### Added

- **Strings order by byte**, so `<`, `<=`, `>` and `>=` work on them. The
  ordering already existed — `sort` has always accepted an `Arr[Str]` — and only
  the operator was missing, so three repositories had each written their own
  `compare_str` returning -1/0/1 and compared that against zero.
- **`all_finite(x)`**: whether every element is a real number, reaching into a
  list or a record because a gradient arrives as a tree. Mixed-precision
  training needs it and cannot express it — a NaN compares false against
  everything, including itself — so a loss scale had no way to detect the
  overflow it exists to find.
- **`numel(t)`**, the product of the shape, at any rank.
- **`file_size(path)`**, in bytes, or -1 when the path cannot be read. A
  streaming reader detecting that its file was rewritten underneath it wants a
  number to compare, and a missing file is a change like any other.

### Changed

- **A cast applies to a scalar**, not only to a tensor. A plain number is
  carried as an unboxed value rather than a rank-0 tensor, which is an internal
  distinction a program cannot see, and `x.to(f32)` failed on whichever leaves
  of a tree happened to be unboxed.
- **`arr_of_tensor` accepts any rank**, copying elements in row-major order. The
  rank-1 restriction bought nothing — the operation is a buffer copy and the
  shape is recoverable with `shape(t)` — and refused the flattening of a
  parameter matrix, which is the main use.
- **An empty literal at a container-typed struct field builds that container**,
  the same rule a `let` annotation already followed. Struct field types are
  erased at run time, so the declaration is now kept for this one purpose:
  `Catalog { versions: {} }` gets a dictionary.

### Fixed

Bugs found in the ecosystem that were the language's fault, or the language's
to prevent:

- Integer division in nine more places across the libraries, where a float
  quotient had been silently wrong: a learning-rate schedule that decayed on
  `epoch / step` applied one drop too many at every epoch, and a canvas
  computed a fractional cell index and read out of range.
- `not(x)` where `bnot(x)` was meant, in two more hand-written bit routines.


## [1.5.0] - 2026-08-14

The release that made the ecosystem run. Every change below came from the same
place: nine sibling repositories were written against twill, and most of them
did not work. The failures were not nine problems but about a dozen, each shared
by several repositories, and each a place where the language asked for something
no one would naturally write. `docs/needs.md` in each repository had been asking
for most of them for months.

### Added

- **Leading-operator line continuation**: a line that opens with `+` or `-` now
  continues the previous expression when it is indented past the statement it
  continues, and starts a new statement when it lines up with it. Indentation is
  what a reader already uses to tell the two apart. This one rule was the single
  largest blocker in the ecosystem: seven of the nine repositories failed to
  parse without it, because a wrapped string concatenation or a long polynomial
  is written this way by everyone.
- **The bitwise operators, spelled and infix**: `band`, `bor`, `xor`, `shl` and
  `shr` are now infix operators as well as callable builtins, with the shifts
  binding like `*` and `xor`/`bor` like `+`. `band`/`bor` exist because `and`
  and `or` are the *boolean* operators infix and the *bitwise* ones when called,
  which meant `x and 255` silently evaluated to `255`. Four repositories had
  asked for the spelling to be fixed; every one of them had this bug.
- **`std/term`, the terminal layer as a standard-library module**: `src/term/`
  and the palette moved to `std/term/` and are importable as `std/term/caps`,
  `std/term/ansi`, `std/term/theme`, `std/term/box` and the rest. Four
  repositories were reaching into a vendored checkout by relative path, which is
  why eight of their suites could not load at all. The standard library now
  carries one level of grouping, and `TWILL_STD` still overrides it.
- **`std/hash`, a SHA-256 that is correct**: promoted from spool's copy, and
  fixed. Three repositories had hand-written this algorithm and all three
  produced wrong digests. Two causes: `not(x)` is the boolean negation where the
  algorithm wants the bitwise one, and the 32-bit rotate overflowed the 53 bits
  an f64-backed I64 actually holds, so `rotr` now masks before it shifts.
  Verified against the standard vectors.
- **Dictionary subscripting**: `d[key]` and `d[key] = value` on a `Dict`, the
  read and write every caller was already writing.
- **Uniform call syntax**: `xs.push(v)` calls `push(xs, v)` when the target has
  no such field, so a container reads the same way whichever spelling is used.
  Record fields and module namespaces are unaffected.
- **Qualified variants**: `Opt.Some(x)` and `Opt.None` are accepted where the
  bare variant is, in expressions and in match patterns alike.
- **`Fn(T) -> R` as a spelling of the function type**, alongside `fn(T) -> R`.
  Every other type in the systems dialect is capitalised, so this is what half
  the ecosystem reached for.
- **`exit(n)`**: stops the program with a status. Without it a failing test
  harness printed its failures and returned zero, so CI passed on a red suite.
  `twill test` reads it as the suite's own verdict rather than as a crash.
- **`arr_of_tensor`, `read_text_or` and `write_text_or`**: the tensor-to-array
  copy on heddle's sampling hot path, and file reads and writes for the caller
  who has already decided what a failure means.
- **`//`, integer division**: divides and truncates toward zero, where `/` stays
  exact float division. Every number runs as an `F64`, so a `(n + k - 1) / k`
  came back fractional and the integer idioms — ceiling divisions, midpoints,
  digit extraction — were quietly wrong. This was weft printing `3.14.14` for
  `3.14`, loom counting `3.25` batches, and a `` escape in the compiler's own
  lexer coming out as a fractional byte.
- **`f64_bits_hi`, `f64_bits_lo` and `f64_from_halves`**: an f64's 64-bit pattern
  does not fit in an f64, so `f64_bits(0.1)` came back 102 short and nothing
  serialised through it reloaded bit for bit. Each 32-bit half is under 2^53 and
  so is exact, which is what makes a bit-for-bit format expressible at all.
  selvedge's save format now round-trips every double, subnormals included, and
  still writes byte-identical output.

### Changed

- **`arr_push` returns the list it appended to**, so it reads as an expression as
  well as a statement. The ecosystem used it in expression position 53 times and
  in statement position once.
- **An empty literal at a container annotation builds that container**:
  `let seen: Dict[Str, I64] = {}` is a dictionary and `let xs: Arr[Str] = []` is
  a list. `{}` in expression position is now the empty record rather than an
  empty block, which had no useful meaning.
- **A `-> I64` return truncates its value**, the same rule `let n: I64 = ...`
  already applied to a bound one. The two annotations had disagreed: the binding
  truncated and the return did not, so a function that promised an `I64` handed
  back a fraction.
- **File paths resolve one way**: `read_file`, `write_file` and the `*_or` pair
  now resolve relative to the running source file, as `save` and `load` already
  did. A program that saved and then read the same path was reaching two
  different files.

### Fixed

- **A function declaration now wins over a builtin of the same name.** Builtins
  are defined into the environment before the file's own declarations are
  hoisted, so a shadowing function was only honoured for calls written *below*
  it; calls above it resolved to the builtin and were reported against the
  builtin's arity. This was a false positive in the shape checker, the language's
  headline feature, and it was firing on the compiler's own source.


## [Unreleased]

### Added

- **`linspace` and `arange` construction builtins** (2026-08-11): the two tensor
  constructors a numerical program reaches for first are now built in, alongside
  `zeros`/`ones`/`eye`. `linspace(start, stop, n)` gives `n` points with both
  endpoints included (the last set to `stop` exactly); `arange(start, stop, step)`
  gives the half-open sequence, the float-stepped, tensor-returning sibling of
  `range`. Both require their arguments explicitly, matching twill's no-hidden-
  defaults style, and both landed in the Go bootstrap and the self-hosted
  evaluator and checker together, with the length statically known for a literal
  `linspace` count. Cross-implementation parity tests included.
- **`std/num` and `std/shapes`: common elementwise and repeat helpers**
  (2026-08-11): `num.sign` (−1/0/1, differentiable with a zero gradient),
  `num.any`/`num.all`/`num.count_nonzero` (reading a tensor as a mask), and
  `shapes.tile`/`tile1` (repeat along an axis, any rank, gradient-transparent).
  All pure twill composed from existing ops; covered by `std/tests/num_test.tw`.
- **`std/num`: matrix diagonals and products** (2026-08-11): `outer(a, b)` (outer
  product), `diagonal(m)` (the `(i,i)` entries), `trace(m)`, and `diag(v)` (a
  matrix with `v` on its diagonal). Each is composed from `eye`/`einsum`/the
  reductions, so all are differentiable -- `grad(fn(v) = trace(diag(v)))` works
  -- and stay vectorised. Covered by a new `std/tests/num_test.tw`, which the
  test runner and the CI regression guard now include.
- **`twill test`: a test runner** (2026-08-11): discovers every `*_test.tw` file
  under the given paths (or the working directory), runs each on a fresh
  interpreter, and reports one line per suite with its check counts plus a
  summary, exiting non-zero if any suite failed. A file's verdict follows the
  harness contract std/tests documents -- an error, or a `FAILED` marker, fails
  the suite. Output is captured at the OS level so a suite that prints through
  `write_out` is caught too. This removes the by-hand CI suite list that made a
  new test file invisible until someone remembered to register it (roadmap #7,
  five callers). A Go-side guard (`cmd/twill/test_test.go`) runs the numeric
  standard-library suites so a regression in them fails `go test`. Running it
  surfaced that `std/tests/random` (systems-mode, needs true 64-bit integers)
  and one `text` UTF-8 case fail on the f64 bootstrap -- known, and now visible.
- **`sort` orders a list of strings** (2026-08-11): `sort(["banana", "apple"])`
  returns a new list in the bytewise-unsigned lexicographic order
  `docs/language-guide.md` pins (uppercase before lowercase, shorter prefix
  first) -- the order Go's `sort.Strings` gives -- with a truthy second argument
  for descending. Previously `sort` accepted only tensors and the ecosystem
  hand-wrote the string comparison in eleven places. `argsort` on a list is
  refused, a non-string element errors, and the input is left unmutated. Landed
  in the Go bootstrap and the self-hosted evaluator (which reuses its existing
  `canon_sort`) together, with a cross-implementation parity test. Closes
  `docs/needs.md` NEEDS-23.

### Fixed

- **The checker enforces `conv2d` and `maxpool2d` shape contracts**
  (2026-08-11): the three mistakes the runtime rejects and the checker used to
  wave through -- a `conv2d` input that is not `[channels, height, width]`, a
  weight that is not `[out, in, kh, kw]`, a channel count that disagrees between
  them, and a `maxpool2d` input that is not rank 3 -- are now diagnostics with
  the runtime's own wording, caught before the net runs. Both implementations,
  byte-identical; the valid CNN paths are unaffected.
- **The checker rejects a rank-3-or-higher `@`** (2026-08-11): `@` is a plain
  matrix product with no batched form -- the interpreter rejects a rank-3 operand
  at run time -- but the checker returned an unknown shape and stayed silent, so
  `zeros(5,2,3) @ zeros(5,3,4)` passed the check and failed only when run. The
  rank is structural and known even when the sizes are not, so it is now a
  diagnostic (`@ (matmul) requires 1-D or 2-D operands, got ...`). Fixed in the
  Go bootstrap and the self-hosted `src/check.tw` together; the 1-D and 2-D forms
  are unaffected.
- **`std/tests/text`: a corrupted UTF-8 test literal** (2026-08-11): the "lone
  continuation byte is not valid utf8" case held the bytes `c2 80` -- a *valid*
  two-byte encoding of U+0080, the classic Latin-1-to-UTF-8 mojibake of a lone
  `80` -- so `char_valid` correctly returned true and the assertion that it be
  false failed. twill source is UTF-8 with no byte escape, so the byte is now
  produced with `chr(128)`, which is what the test meant. Surfaced by `twill
  test`; the suite passes 80/0.
- **A constant out-of-range index is caught, and a scalar `@` operand**
  (2026-08-11): `zeros(3)[3]` (twill indexes from 0 with no negative
  wraparound) is now the runtime's `index 3 out of range [0, 3)` raised at check
  time, whenever the index is a literal and the axis length is known. And a
  rank-0 operand to `@` (`scalar(2.0) @ x`) joins the rank-3 case as a reported
  error, since matmul takes only 1-D and 2-D operands. Both implementations.
- **`broadcast_to` compatibility, and `list(...)` shapes, are checked**
  (2026-08-11): `broadcast_to(x, target)` where the source cannot broadcast to
  the target (a source axis that is neither 1 nor equal to the target's, or more
  axes than the target) is now a diagnostic with the runtime's wording, instead
  of passing the check. In the same change the checker learned to read the
  idiomatic `list(2, 3)` shape argument -- previously only the separate-argument
  form (`reshape(x, 2, 3)`) was understood -- so `reshape`'s element-count check,
  the new broadcast check, and the shape of `zeros(list(2, 3))` all now hold for
  the form the standard library and examples actually write. Both implementations,
  byte-identical.
- **Every axis-taking builtin now checks its axis** (2026-08-11): `flip`,
  `roll`, `cumsum`, `cumprod`, `cummax`, `cummin` and a tensor `sort` took an
  optional axis the checker ignored, so an out-of-range one (`flip(x, 5)` on a
  rank-2 tensor, or `roll(x, 1, 5)` with roll's shift-first argument order)
  passed the check and failed only at run time. All are now validated through one
  shared path, in both implementations. This closes the gap the `transpose` and
  `softmax` fixes opened: the axis argument is checked wherever a builtin takes
  one.
- **`transpose` and `softmax` report an out-of-range axis** (2026-08-11): a
  permutation that names an axis outside the tensor's rank
  (`transpose(x, 0, 5)` on a rank-2 tensor), or a `softmax(x, 5)` over an axis
  that does not exist, is now a checker diagnostic
  (`axis out of range for [2, 3]: ...`) rather than a silent unknown that failed
  only at run time. These were the axis-taking builtins that stayed quiet;
  they now report through the same path as `sum`, `argmax` and `concat`. Fixed
  in the Go bootstrap and the self-hosted `src/check.tw` together, producing
  byte-identical diagnostics. Closes `docs/needs.md` NEEDS-50.
- **The checker enforces the `break`/`continue` scoping rules** (2026-08-11):
  a `break` or `continue` outside any loop, or inside a function nested in a
  loop, is now a diagnostic (`break outside a loop`) rather than passing
  `twill check` and reaching the interpreter as an uncaught unwind signal. The
  checker tracks a lexical loop depth reset to zero at every function boundary,
  so loop control binds to the innermost enclosing loop and never crosses a
  `fn`, matching `docs/language-guide.md`. The rule lands in both the Go
  bootstrap (`internal/checker`) and the self-hosted `src/check.tw` in lockstep,
  which the self-hosted checker tests confirm produce byte-identical
  diagnostics. Closes the last open piece of `docs/needs.md` NEEDS-12.

### Added

- **`std/transformer`: the GPT-style decoder, in Twill** (2026-08-11): a new
  standard-library module that composes `std/nn`'s attention, layernorm, gelu and
  embedding pieces into the canonical decoder-only transformer. It ships the
  pre-norm block (`block`, `block_params`), the whole model
  (`gpt_params`, `gpt_logits`) with a weight-tied output head and learned
  positional embeddings, the next-token training loss (`gpt_loss`,
  `gpt_loss_batch`), greedy `generate`, and a `num_params` walk. The entire
  stack -- embeddings, stacked masked-attention blocks and the tied head --
  differentiates in a single reverse pass, so a full training step is
  `grad(gpt_loss_batch)(params)` and the existing optimizers walk the gradient
  unchanged. The module is a few dozen lines of Twill over `std/nn`; it adds no
  primitives.
- **`examples/gpt.tw`**: a character-level GPT that tokenises a string, trains
  on its shifted windows with Adam, and greedily samples the model back into
  text. On a small repeated corpus it drives the loss from 3.0 to 0.15 and
  continues a prompt in the corpus's own words.
- **`std/tests/transformer_test.tw`**: checks the parameter count against a
  hand-derived total, the `[T, vocab]` logit shape, the causal property (an
  earlier position's logits are bit-for-bit invariant to a later token), block
  shape preservation, and that the stack differentiates and learns a memorisable
  cycle.

## [1.4.0] - 2026-08-11

The self-hosting release. The twill compiler written in twill (`src/*.tw`) now
runs on the Go bootstrap and performs every stage -- lex, parse, check, format,
**evaluate**, and the differential canonical dump -- matching the Go reference.
`twill check` matches byte-for-byte on all 443 corpus files; `twill fmt` on all
89 (bar a by-design blank-line divergence); and the self-hosted evaluator runs
the entire example corpus -- autodiff, jacobians, hessians, neural-net training,
CNNs, attention, gradient boosting, Monte Carlo pricing -- with output identical
to `twill run`, save a couple of 1-ULP float-accumulation differences and the
save/load of a foreign gbm model. The whole `src/`+`std/` tree type-checks clean.

Getting there added a systems runtime the evaluator needs and closed the
numeric, formatting, evaluation and dtype gaps between the two implementations;
the entries below are that work.

### Added

- **Native float text conversion unblocks self-hosted numbers** (2026-08-10):
  two primitives, `str_to_f64` (parse) and `f64_to_str` (shortest `%g`), that
  `std/float`'s `f64_of_str` and `f64_shortest` delegate to. The pure-twill
  decimal machinery they also contain assembles IEEE bit patterns with 64-bit
  integer arithmetic the float64-backed bootstrap cannot do exactly, so every
  numeric literal the self-hosted compiler read or printed came out wrong; the
  primitives are the same `strconv` calls the Go bootstrap uses, so numbers now
  round-trip. With this the self-hosted `check` matches the Go command
  byte-for-byte on all 443 corpus files (was 432), and `fmt` reproduces every
  numeric literal correctly. The twill decimal code is kept as the reference for
  a future exact-I64 runtime.

- **Self-hosting: the front end runs on the bootstrap** (2026-08-10): the twill
  compiler written in twill (`src/main.tw` and the modules it imports) now runs
  on the Go bootstrap and checks and formats real files. A differential sweep of
  the whole corpus (443 files) has `check` matching the Go command byte-for-byte
  on 432; `fmt` reproduces the Go layout structurally. The one thing keeping both
  from full parity is that the bootstrap has no exact 64-bit integer (its numbers
  are float64), so the self-hosted number parser cannot assemble an IEEE bit
  pattern above 2^53 and every reprinted or axis-valued numeric literal comes out
  wrong; the checks and format that do not read a numeric value match exactly.
  (`fmt` also preserves blank lines between statements by design, a deliberate
  divergence from Go `fmt`.) A systems-mode program
  now has `main()` as its entry point (invoked after the top level, the way Go
  and Rust spell it), with command-line words after the script exposed through
  the `args` builtin, so `twill run src/main.tw check foo.tw` drives the
  self-hosted CLI. `run` and `repl` remain stubbed in the self-hosted CLI (they
  need the evaluator's native core, a later step). Enabling changes: top-level
  functions are hoisted so a definition or a module-level `let` may call one
  written below it (order-preserving, via a prebind that does not disturb a
  namespace's field order); enum variant constructors are program-global at run
  time, matching the checker; `len`, indexing and slicing work on strings (and
  `len` on dicts and bytes); dict keys may be integers (`Dict[I64, V]`);
  `read_file` yields a `Str`.

- **`break` and `continue`** (NEEDS-12, 2026-08-10): loop control in `while` and
  `for`, unwound to the enclosing loop by the same signal mechanism `return`
  uses. Both sides.

- **`unit` as a value** (NEEDS-13, 2026-08-10): the name `unit` resolves to the
  Unit value, so a systems-mode arm like `None => unit` and any nothing-valued
  expression has a spelling. `unit` stays a field name elsewhere; only the bare
  name resolves. Both sides.

- **String `+` concatenation** (2026-08-10): `a + b` joins two strings, in the
  interp and the checker. Two strings give a string; a string with a definite
  number or tensor is still an error (`str(n)` is how a number joins a string).
  This is what the terminal and CLI code use instead of nested `bytes.concat`.
  Both sides.

- **Terminal, list and GPU-binding builtins** (2026-08-10): `arr` (list literal
  as a call), `arr_clear`, `chr` (one-byte string), `slice` (clamped byte
  substring), `env` (variable as `Opt[Str]`), `is_tty_stdout`, `window_size`,
  `clock_now_ms`, and the `gpu_*` device FFI boundary (no backend in this build;
  each fails loudly). With these the entire `src/`, `std/` and `examples/` tree,
  including the `cli/`, `term/` and `gpu/` subfolders, type-checks under the
  bootstrap. Both sides.

- **Systems runtime primitives** (2026-08-10): `emit_line`, seeded scalar RNG
  (`rng_seed`/`rng_uniform`/`rng_normal`/`rng_perm`), reference identity
  (`is_same`), argv (`args`), and value persistence (`save_value`/`load_value`),
  registered in the shared builtin table so both the checker and the evaluator
  know them. Both sides.

### Changed

- **Top-level `let` forward references** (2026-08-10): a module-level constant may
  be used above its definition line, matching the hoisting already granted to
  functions and enum variants. The checker asserts the name exists; evaluation
  order stays the evaluator's concern. Both sides. This resolves the compiler's
  own `DTYPE_MAKERS` self-reference.

- **Cross-module enum constructors** (2026-08-10): a capitalized constructor name
  borrowed from an aliased import (e.g. `SFn`, `EBlock` from `ast.tw`) no longer
  reports as unknown. Variant constructors register program-wide at run time, so
  a single-file checker cannot prove one absent; a lowercase unknown (a value or
  function typo) is still reported. Both sides.

### Fixed

- **`jacobian` output dtype scoping** (2026-08-10): the self-hosted `jacobian`
  read the output tensor's dtype after the loop that bound it, where the binding
  was out of scope. The dtype is now captured in a loop-outer variable.

## [1.3.0] - 2026-08-10

The systems-mode front end. `mode systems` is the dialect the compiler is being
rewritten in, and the bootstrap now parses, type-checks and formats all of it:
every file in `src/`, `std/`, `examples/` and `testdata/` goes through the front
end. Enums and `match`, `struct` declarations, generics in annotations,
`Res`/`Opt` with postfix `?`, field and index assignment, bitwise operators, and
typed record literals all land, each mirrored into the self-hosted compiler in
lockstep. Running the self-hosted compiler on the bootstrap is the next step and
is not yet done; the array/ML language the bootstrap has always been is
unchanged and fully backward compatible.

### Fixed

- **A line-leading `+`/`-` continues an expression inside a grouping**
  (2026-08-09). The rule that a line starting with `+`/`-` (or `and(`/`or(`)
  begins a new statement now applies only at statement level; inside a `(...)`,
  a call's arguments, or a `[...]`, there is no statement to end, so
  `f(a\n  + b)` continues the argument rather than breaking with "expected `)`".
  A `group_depth` counter on the parser gates the newline rules. Unblocked
  `std/stats.tw`. Both sides.

### Fixed

- **Three latent syntax bugs in the self-hosted sources** (2026-08-09), found now
  that the bootstrap parses far enough to reach them and `src/*.tw` was never in
  the format test corpus: a `report(bytes.concat(...))` in `src/check.tw` was
  missing a close paren; `src/tensor.tw` had two assignment right-hand sides
  continued with a leading `+` (against the trailing-operator rule the language
  documents), rewritten to trailing `+`; and `src/eval.tw` had one stray extra
  `}` after `apply_to_tensor`. With these, **every file in `src/`, `std/`,
  `examples/` and `testdata/` parses**, the whole self-hosted compiler included.

- **A value-less `return` may end at a `,`** (2026-08-09), so a match arm body
  can be a bare return: `TUnknown => return,`. Previously `parseReturn` treated
  only `}`, `;` and end-of-input as terminators and tried to read the `,` as the
  return value.

### Changed

- **`type` is now a contextual keyword** (2026-08-09), like `unit`: a declaration
  only when it leads `type <name> = ...`, and an ordinary identifier everywhere
  else, most importantly as a field name (`res.type`, `{ type: x }`). Both sides.

- **`unit` is now a contextual keyword** (2026-08-09): a declaration only when it
  leads `unit <name>`, and an ordinary identifier everywhere else, most
  importantly as a record field or key name (`unit: Opt[UnitAnno]`, which the
  compiler's own `Param` and checker structs use). `unit USD` declarations are
  unchanged. Both sides. This is the same contextual treatment `mode` already
  has.

### Added

- **`struct` declarations** (NEEDS-5, 2026-08-09): `struct Mat { rows: I64,
  cols: I64, data: Arr[F64] }`. The whole self-hosted compiler declares its
  types this way, yet the bootstrap had no `struct`, so `struct Foo { ... }`
  parsed as stray identifiers and a record literal and failed the check. It is
  now a declaration: field types are full type references (name, qualified, or
  generic), the checker registers the name as a record type whose fields exist
  and whose field types are advisory (so `m.field` is checked for existence),
  and it is erased at runtime since a record is structural and built with a
  (typed) record literal. Go bootstrap (lexer keyword, parser, checker, interp,
  formatter) with tests in `internal/interp/struct_test.go`; self-hosted mirror
  follows. Clears the `struct` blocker in the compiler's own sources and in
  `std/linalg.tw`, `std/random.tw`, `std/float.tw`; what remains on those is the
  systems-mode runtime primitive library (`arr_new`, `dict_new`, ...).

- **Postfix `?` and built-in `Res`/`Opt`** (2026-08-09): `read_file(path)?`. The
  operator unwraps the success case of a `Res`/`Opt` value (the payload of `Ok`
  or `Some`) or returns its failure case (`Err`/`None`) from the enclosing
  function, which is how an error propagates. `Ok`, `Err`, `Some` are built-in
  one-argument constructors and `None` a built-in value, so the family works
  without a declaration. Go bootstrap (lexer `?`, parser postfix, `ast.Try`,
  interp, checker, formatter) with tests in `internal/interp/try_test.go`;
  self-hosted mirror follows. This was the last parse blocker in the std format
  corpus, which now round-trips clean end to end (`internal/format` passes).

- **Typed record literals** (2026-08-09): `Point { x: 1.0, y: 2.0 }`,
  `geom.Point { ... }`. A type name in front of a record literal is now parsed;
  records are structural, so the value is the same one `{ ... }` builds and the
  name is advisory, kept only so `twill fmt` reproduces it. The `{ ident :`
  shape that marks a record is one a block never begins with, so a condition
  ending in a name (`if p.x > 0 { ... }`) is not mistaken for a typed literal, no
  parser mode flag needed. Go bootstrap (parser, `ast.RecordLit.TypeName`,
  formatter) with tests in `internal/interp/typed_record_test.go`; self-hosted
  mirror follows. Unblocked `std/float.tw`; the std format corpus is now down to
  two parse failures (io, json), both on postfix `?`.

- **`enum` declarations and `match` expressions** (NEEDS-3, 2026-08-09): the sum
  type and its eliminator, the foundation the whole `Res`/`Opt` family rests on.
  `enum Name { Case, Case(Payload), ... }` declares the type; each case is a
  value in scope (a one-argument constructor when it carries a payload, the
  variant itself when it does not), so `Some(x)` is a call and `None` a bare
  name. `match subject { pattern => body, ... }` inspects the case, binds its
  payload (`Some(v) => ...`), runs the arm; `_` is the wildcard and an arm body
  is a statement, so an expression, a `return`, an assignment or a block all
  work. Values render as `Some(5)` / `None`. Go bootstrap (lexer `=>` plus the
  two keywords, parser, `value.Variant`, interp, checker, formatter) with tests
  in `internal/interp/enum_test.go`; self-hosted mirror follows. Unblocked
  `src/main.tw`; `std/float.tw` advances to typed record literals.

- **Assignment to a field or an index** (2026-08-09): `obj.f = v`, `arr[i] = v`,
  and the composing forms (`a.d[i] = v`, `xs[0].n = v`). Assignment now targets
  any lvalue expression, a name, a field, or an index, rather than only a bare
  name; the parser parses the left side as an expression and requires it to be
  one of those three forms. Records and lists are reference values, so a field
  or element write mutates the object every binding shares. Unblocked
  `std/random.tw` and `std/linalg.tw` (both round-trip now). Go bootstrap
  (parser, interp, checker, formatter) with tests in
  `internal/interp/assign_test.go`, mirrored in the self-hosted tree.

- **Generic type annotations parse and check** (NEEDS-4, part, 2026-08-09):
  `xs: Arr[I64]`, `-> Res[I64, Str]`, `let d: Dict[Str, Arr[I64]]`. A `[` after a
  bare type name opens a generic argument list, each argument a full type
  reference, so nesting (`Arr[Arr[I64]]`) and qualified arguments
  (`Dict[Str, ast.Expr]`) work; the whole name is kept as advisory text and
  `twill fmt` round-trips it. `let` gained a `TypeName`, and a systems-mode
  generic parameter is left unknown rather than pinned to its argument, so
  indexing an `Arr` param is not a false error. Unblocked `std/text.tw`
  (round-trips now) and moved four more std files to their next blocker. Go
  bootstrap + self-hosted parser/checker/formatter; tests in
  `internal/interp/generics_test.go`. Generic *declarations* and real
  monomorphization remain.

- **Bitwise operators on I64** (NEEDS-2, part, 2026-08-09): `and`, `or`, `xor`,
  `shl`, `shr` (2-arg) and `bnot` (1-arg), on scalar integers, `shr` arithmetic,
  shift counts masked to 0..63. `and`/`or` keep the boolean keywords' spelling
  but are the bitwise builtins when called; a line beginning `and(`/`or(` is a
  new call-statement rather than a boolean continuation of the line above;
  bitwise-not is `bnot`, since `not` stays the boolean prefix operator. Values
  are still float64, so a full 64-bit pattern round-trips lossily above 2^53 (a
  real I64 storage type is the remainder of NEEDS-2). Go bootstrap + self-hosted
  parser/checker/registry; `std/float.tw` and `std/random.tw` updated to `bnot`.
  Tests in `internal/interp/bitwise_test.go`.

- **`mode systems` gates type-name resolution** (2026-08-09), its first
  semantic effect, now also covering `let` annotations (`let n: I64 = …`). In a systems-mode file an unresolved type annotation
  (`n: I64`, `-> Bool`, `-> Str`, `c: cp.Caps`) is advisory: the bootstrap has
  no such type, so the parameter takes its argument's type and nothing is
  reported. In numeric mode the same name is still an "unknown type" error, and
  the unit algebra is untouched in both modes, so a declared-unit return like
  `-> USD` is still checked. This is what lets the self-hosted sources, whose
  every signature is written in `I64`/`Str`/`Bool` and qualified names, pass
  `twill check`. Landed in the Go bootstrap (`internal/checker`) and the
  self-hosted checker (`src/check.tw`) in lockstep, with tests in
  `internal/checker/systems_test.go`.

- **Module-qualified type names parse in signatures** (2026-08-09). `fn f(c:
  cp.Caps) -> cp.Caps` was a hard syntax error, because the type grammar reuses
  the unit-expression parser, which stops at the `.`. A `.` after a single bare
  name is now read as a qualified type (units are never qualified, so it is
  unambiguous): the suffix is consumed and the dotted name kept. A qualified
  return goes into a new advisory `RetType` on the function rather than a unit
  slot, so no spurious unit check runs; a qualified parameter extends its
  `TypeName`. Both are advisory, matching the structural records the bootstrap
  already has, so an unresolved qualified name is tolerated rather than an
  error. `twill fmt` round-trips them. Landed in the Go bootstrap and the
  self-hosted tree (`src/parse.tw`, `src/ast.tw`, `src/fmt.tw`) in lockstep,
  with tests in `internal/interp/qualtype_test.go`.

- **`mode systems` is recognised as a file-level declaration** (NEEDS-1,
  2026-08-09). A leading `mode <name>` is recorded on the program and re-emitted
  by the formatter, set off from the body by a blank line. `mode` stays a
  perfectly ordinary identifier everywhere else, since it is recognised only
  first and only when an identifier follows. A systems-mode file built from
  features the bootstrap already has now parses and runs rather than failing on
  its first line, and `twill fmt` round-trips it. Landed in the Go bootstrap and
  the self-hosted tree (`src/parse.tw`, `src/fmt.tw`) in lockstep. The mode does
  not yet gate which constructs are legal; that is the remaining work behind the
  entry.

### Renamed

- **The language is now Twill, and its source extension is now `.tw`**
  (2026-08-07). The Go module path is `github.com/twill-lang/twill`, the binary
  and every CLI message is `twill`, the standard library override environment
  variable is `TWILL_STD`, and `examples/`, `std/` and the whole fixture corpus
  carry `.tw`.

  **`.ra` is a hard break, not a deprecation.** There is no fallback and no
  removal date: a `.ra` path handed to `twill run`, `check`, `fmt` or
  `--dump=canonical`, or named by an `import`, is refused with an error that
  tells you the extension is now `.tw`. One extension is one thing to explain,
  and refusing loudly today is kinder than leaving `.ra` files in the wild
  waiting on a deletion nobody remembers to do. Rename your files.

  **The `RSTR` on-disk magic did not change**, and neither did the save format
  version. Those four bytes are a compatibility contract rather than branding,
  they are not visible from the language, the CLI or the docs, and every file
  already saved keeps loading. `docs/self-hosting.md` sets out the reasoning. If
  the format ever changes for a real reason, that is the moment to introduce a
  `TWIL` magic at version 2.

  **Entries below this one still say Raster, and that is correct.** They record
  what happened to a project that was called Raster at the time. Nothing about
  the rename is retroactive.

  Not done here, and left to the repository owner: the GitHub repository itself
  is still named `raster`, so clones and remotes are unaffected by this release.

### Changed

- **A number that needs no gradient is not a heap-allocated tensor.** Every
  Raster number was a rank-0 `*tensor.Tensor`, which costs two allocations (the
  struct and its one-element backing slice) before any arithmetic happens. A
  scalar loop paid that for every literal, every loop counter and every
  intermediate result: seven allocations per iteration, and over half the
  runtime in the allocator and collector.

  Values that cannot be carrying a gradient are now a `value.Num`, a bare
  float64, which costs one eight-byte box instead of two allocations. Measured
  on `for i in range(3000000) { acc = acc + 1.0 }`, both binaries built and run
  interleaved in one container: 1389 ms to 574 ms, 59% faster, 21.0 million
  allocations down to 12.0 million and 1.94 GB down to 648 MB.

  The cost is that every boundary wanting a tensor has to widen back to one:
  gradient tracking, tensor ops, indexing, printing, saving, and the pytree
  walks an optimiser uses. That widening is what keeps gradients exactly what
  they were, and it is centralised in `value.AsTensor` so a new boundary cannot
  quietly forget it. `grad` and `hessian` over a plain scalar argument have
  tests, because a missed widening there does not fail loudly, it answers zero.

  Which representation a value has is not observable: a `Num` and the rank-0
  tensor holding it compare equal, print the same, and save to the same bytes.
  The scalar arithmetic is a second implementation of the same six operators,
  so a test evaluates each one both ways and requires exact agreement.

  An earlier attempt to give `Tensor` an inline `[1]float64` backing array was
  rejected: it removed 20% of the allocations but moved the clock 1%, and it
  added 8 bytes to every tensor including the large ones that dominate real
  memory.

- **Two scalars with no gradient take a direct path.** The general binary op
  charged a scalar addition a broadcast computation, a `parallelFor` over one
  element, and a slice allocation for that element. Doing the arithmetic is
  cheaper than deciding how to do it. 736 ms to 655 ms, 288 MB and three million
  allocations fewer.

  Gated on gradients and forward-mode both being off, so there is still exactly
  one copy of the backward closure. Gradients go through the path they always
  did.

- **A scope holds its first four bindings inline.** Every scope allocated a map
  on its first binding, and an interpreted loop makes a scope per iteration, so
  the map was 36% of the allocations in a scalar loop while holding one name.
  Four inline slots with a linear scan cover almost every scope; beyond that the
  map takes over and nothing else changes.

  Measured on the same loop: 883 ms to 675 ms, 24% faster, 576 MB and six million
  allocations fewer. With the range change below, the loop is 45% faster than it
  was.

- **`for i in range(n)` counts instead of building a list.** The loop used to
  materialise every element first: `range(3000000)` allocated a 48 MB slice and
  three million scalars before the first iteration ran, and the collector then
  walked that slice for the rest of the loop. Profiling a scalar loop put object
  scanning at the top.

  Measured on `for i in range(3000000) { acc = acc + 1.0 }`: 1224 ms to 989 ms,
  19% faster, with 90 MB less allocated per run.

  Nothing else changes. Each iteration still gets its own scope and its own
  scalar, so a closure that captures the loop variable captures what it always
  did, and a file that defines its own `range` gets its own. Both have tests,
  because the fast path is a second implementation of the same loop and the two
  must not drift apart.

### Fixed

- **`grad(grad(f))` is refused instead of answering zero.** The guide already
  said nested reverse mode was unsupported; the implementation returned 0 anyway.
  The gradient `grad` returns is a plain value with no history, so differentiating
  it again differentiates a constant. A second derivative that is silently zero
  is the worst way for one to be wrong, because nothing about the result invites
  a check. The error names `hessian`, which computes it correctly.

- **`tensor(...)` no longer discards the shape of its argument.** It returned an
  unknown type, so `tensor([[1.0, 2.0], [3.0, 4.0]]) @ tensor([[1.0, 2.0, 3.0]])`
  passed the checker and failed at run time. The literal already had a shape; the
  call was throwing it away at the door.

  A ragged literal still reports nothing here. It is already an error, and
  inventing a shape for it would produce a second, imaginary error downstream
  instead of the real one.

- **A reshape that changes the element count is reported.** `reshape(zeros(2, 3),
  4, 2)` asks for eight elements from six. The message names both counts, since
  the useful question is which one is wrong.

- **`concat` has a shape.** It returned unknown, which cost twice: pieces that
  cannot be joined reached the runtime, and everything downstream of a concat was
  unchecked from that point on. `concat([zeros(2, 3), zeros(2, 3)], 0)` is a
  `[4, 3]` now, and a later multiply against it is checked like any other. A
  piece whose own shape is unknown still reports nothing, because unknowable is
  not the same as wrong.

- **An unknown name is reported.** `print(nope + 1.0)` used to pass. Forward
  references still resolve, because a file may call a function declared further
  down and does at run time. A file with an unaliased `import` stands the check
  down entirely: that form brings names in unqualified, the checker does not read
  the imported file, and reporting a name it simply cannot see would be worse
  than reporting nothing. An aliased import keeps the check.

- **An axis that does not exist is reported.** Both reduction paths already
  worked out that the axis was out of range and then returned an unknown type,
  which silenced everything downstream as well. Detecting a mistake and saying
  nothing is the worst of the three options. Negative axes still count from the
  end.

## [1.2.0] - 2026-08-06

### Added

- **`roll` and `diff`**: a wrapping shift along an axis, and the difference
  between neighbours. Both differentiable. `diff` shortens the axis rather than
  padding it, since a zero first difference is a claim about data that is not
  there.

- **`argmin` and `flip`**: `argmin` was missing while `argmax` was not, which is
  a gap rather than a decision. `flip` reverses along an axis and is
  differentiable exactly, a reversal being a permutation that is its own
  inverse. Ties in `argmax` and `argmin` go to the first occurrence, matching
  `cummax`/`cummin` and the stable sort.

- **Axis-aware cumulative scans**: `cumsum`, `cumprod`, `cummax` and `cummin`
  now take an optional axis and scan along it, keeping the shape. Without one
  they scan the elements in order as before, so nothing that worked changes.

  `cumprod`'s gradient rebuilds the product with the element left out rather
  than dividing the running product by it. The division is the obvious form and
  is wrong exactly when the element is zero, which is not a rare value for a
  tensor. `cummax`/`cummin` send each output's gradient to the element the
  running extreme came from, with ties going to the earlier one, matching the
  flat scans and matching sort's stability.

- **Sorting**: `sort`, `argsort`, `topk` and `argtopk`, axis-aware, defaulting
  to the last axis. `sort` and `topk` are differentiable exactly: a sort is a
  permutation, so the backward pass is its inverse, and a value outside the top
  k contributes nothing so its gradient is zero. The sort is stable, which
  matters more than it looks: an unstable one returns the same values in a
  different arrangement, and the gradient follows the arrangement.

## [Unreleased]

### Added

- **Function types in annotations** (2026-08-11): `fn(A, B) -> C` is now a type
  the parser accepts wherever a type annotation may appear -- a parameter, a
  return, a `let`, or a struct field -- so a callback can be typed
  (`step: fn(Tree, st.OptState) -> st.StepResult`). The types nest, so a
  higher-order callback (`fn(fn(F64) -> F64) -> F64`) parses, and a qualified
  result name is carried through. Like every systems-mode type it is advisory:
  it parses, formats and round-trips, and the checker leaves it unresolved rather
  than checking it. This was the single largest blocker to the training and
  inference packages parsing on the bootstrap (loom, heddle, shuttle, selvedge),
  and with it plus `arr_push` the package sources that check clean went from 36
  to 86 of 123.
- **`arr_push` builtin** (2026-08-11): the append that the `arr_new`/`arr_clear`
  family was missing, which several systems-mode callers reach for by that name.
  Identical to `push` -- appends to a growable array in place and returns unit.

### Fixed

- **`std/shapes` no longer works around a restriction that does not exist.**
  `numel` was recursive, and the comment above it said `fold` does not take the
  value `shape` returns. It does: a shape is an ordinary list, and `fold`, `len`
  and indexing all work on one. `numel` is a fold now, and `prod_dims` keeps its
  recursion for the reason that is actually true, which is that `flatten_from`
  needs the product of a tail of the shape and `fold` cannot skip a prefix.

## [1.1.0] - 2026-08-04

### Added

- **`prod` and `median` reductions**, both axis-aware and both differentiable,
  bringing the built-in set to `sum`, `mean`, `max`, `min`, `prod`, `median`.
  They exist as built-ins rather than as `std` functions for the same reason
  the others do: neither can be composed out of what already existed without a
  gradient rule written by hand. `prod` handles zero factors explicitly instead
  of dividing by them, and `median` sorts indices rather than values so the
  backward pass knows which element it picked.

- **`broadcast_to(t, ...shape)`**, differentiable, expanding a tensor to a named
  shape. Broadcasting was already implicit inside every binary op, which covers
  everything until the shape you need to expand against is not one of the
  operands, the case being a reduction result you want to subtract from what
  you reduced. The gradient sums over each broadcast axis.

- **`split(t, n | sizes[, axis])`**, differentiable, the inverse of `concat`.
  A number gives that many equal pieces, a list gives those exact lengths, and
  each piece routes its gradient back into its own slice of the parent. Sizes
  that do not account for the axis exactly, and equal splits that do not divide
  evenly, are errors rather than ragged output. Pieces are copies, not views,
  matching the rest of the package.

- **Sample statistics in `std/num`**: `var_s`, `var_s_axis`, `std_s`,
  `std_s_axis`, `cov_s` and `corr_s`, dividing by n - 1. The population versions
  divide by n, which understates the spread when the tensor is a sample rather
  than the whole population, because the mean subtracted was measured from that
  same sample. A single element is left uncorrected instead of dividing by zero.

### Fixed

- **`num.var_axis` and `num.std_axis` no longer fail on any axis but the first.**
  They subtracted the mean straight back from the input, and a reduction drops
  the axis it reduced, so `var_axis(x, 1)` on a [2, 3] tried to broadcast a [2]
  against a [2, 3] and raised a shape error. Broadcasting aligns from the right,
  so only a reduction over axis 0 lined back up, and it did so by luck. The mean
  is restored to the reduced shape with `broadcast_to` now.

## [1.0.1] - 2026-08-03

### Fixed

- **A plain import no longer hollows out a namespace imported after it.** The
  load-once set was global, but "already loaded" means "already loaded into
  this scope", and a namespaced module's scope is new. So after
  `import "std/optim"`, the nested plain import inside nn was skipped as
  already loaded and its names never reached the module scope:
  `import "std/nn" as nn` came back missing `zeros_like`, `sgd_step`,
  `momentum_step` and `adam_step`, purely because of what had been imported
  before it. The same bug made a second namespace over one module come back
  empty. Each namespaced module gets its own load-once set now, which still
  guards cycles within that module.

## [1.0.0] - 2026-08-03

Cumulative scans are differentiable, equality is structural, imports are deterministic, and the standard library ships inside the binary.

## 0.28.0

Breaking. Three semantics that would be expensive to correct after a 1.0
stability promise, fixed now.

**The standard library ships inside the binary, and `std/` is its own namespace.**

- `import "std/nn"`: no extension, no directory. A path starting with `std/`
  names a module of the standard library, which is compiled into the `raster`
  binary with `go:embed`, so the import means the same thing from any working
  directory and an installed binary can find it. Before, `std/` was read off the
  disk relative to the importing file or the process cwd, so a `raster` on your
  `PATH` could not import the library it was built with. Every other import path
  is still a file.
- `std/` is reserved: a directory named `std` next to your program does not
  shadow the library. A real local file is still reachable as
  `import "./std/local.ra"`.
- A standard-library module may only import other `std/` modules. Embedded
  sources have no directory of their own to resolve a relative path against.
- `RASTER_STD=<dir>` is the escape hatch: it replaces the embedded library
  wholesale, so `import "std/nn"` reads `$RASTER_STD/nn.ra`. Meant for working
  on the library itself without rebuilding.
- **Migration.** `import "std/nn.ra"` and `import "../std/nn.ra"` become
  `import "std/nn"`; likewise for `optim`, `data`, and `backtest`. The old
  extension spelling is rejected with an error naming the new one
  (`a standard-library import names a module, not a file: write "std/nn"`), so
  the fix is mechanical:
  `sed -i -E 's|"(\.\./)*std/([a-z_]+)\.ra"|"std/\2"|g' *.ra`. Imports of your
  own files are unaffected.

**`==` and `!=` are deep structural comparison.**

- They used to answer `false` for every list and every record, including
  `a == a`, because the comparison fell through for those types and silently
  reported "not equal" rather than failing. Lists now compare elementwise,
  records field by field matched by name (so declaration order does not change
  the answer), and both recurse.
- A tensor's shape is now part of its value: `[[1.0, 2.0], [3.0, 4.0]]` and
  `[1.0, 2.0, 3.0, 4.0]` hold the same numbers and are no longer equal. This is
  the one change here that can flip a `true` to a `false`.
- `()` equals `()`. Functions compare by identity, a function equals itself,
  two separately written `fn(x) = x` do not. Values of different types are
  never equal, which is an answer rather than an error. `!=` is the negation of
  `==` in every case.
- Ordering (`<`, `<=`, `>`, `>=`) is unchanged: still scalars only, still an
  error on anything else.

**A namespaced import's field order is now declaration order.**

- `import "std/nn" as nn` built its namespace record by ranging over a Go map,
  so `columns(nn)` and `print(nn)` came out in a different order on every run.
  Reproducibility is the point of this language; a record whose field order is
  random is not that. A module scope now records the order its names were first
  defined in, including names it picked up from its own plain imports, and the
  namespace record follows it.

## 0.27.0

Differentiable cumulative scans.

- `cumsum`, `cumprod`, `cummax`, and `cummin` are now differentiable. Before,
  they returned an untracked tensor, so `grad` through a scan silently came back
  zero, and where a scan was only part of an expression (`max_drawdown`, which
  divides by a `cummax` peak) the gradient was not zero but wrong. `cumprod`'s
  backward pass avoids dividing by an input, so a zero in the series is exact.
  `cummax`/`cummin` route each output's gradient to the element the running
  extreme came from, ties going to the earlier one, matching `max`/`argmax`.
- More of `std/backtest.ra` is therefore differentiable end to end: `sma`
  (prefix sums), `equity` and `total_return` (cumulative product), `cagr`, and
  `max_drawdown` (running peak) join Sharpe and Sortino.
- Backed by new tracked `CumSum`/`CumProd`/`CumMax`/`CumMin` tensor ops, each
  with a forward-mode jet, so `hessian` flows through a scan too (it used to
  crash on one, because the scan detached the input from the graph entirely).

Also fixed:

- `hessian` no longer panics when the input is not connected to the output at
  all. Making the scans differentiable removed one way to reach that, but not
  the cause: any forward-only builtin (`floor`, `ceil`, `round`, the
  comparisons) returns an untracked tensor, so walking back from the output
  never reaches the leaf and the jet state it seeded was never allocated. It
  now returns zeros, which is both the right answer for a function whose output
  does not depend differentiably on its input and what `grad` already returned
  for the same expression.

## 0.26.0

Differentiable element indexing.

- `x[i]` (indexing a single element or row) is now differentiable, gradient
  flows to the indexed component, and `hessian` passes through it too. Before,
  element indexing silently broke the gradient graph (it returned an untracked
  tensor, so `grad` through `x[i]` was zero); slicing `x[i:i+1]` was the only
  working form. Both now work.
- Backed by a new tracked `IndexAxis0` tensor op (with a forward-mode jet);
  `x[i]` in the interpreter routes through it.

## 0.25.0

Second-order autodiff through structural ops.

- `hessian` now flows through the linear/structural ops: slicing (`x[a:b]`),
  `reshape`, `transpose`, `concat`, and `gather`, so component-wise functions
  and reshaping objectives get exact Hessians too (previously they errored).
- `examples/hessian.ra` adds a component-wise case: the Hessian of
  `(x0-x1)² + x1²` computed through slicing is `[[2,-2],[-2,4]]`.
- Extended the finite-difference cross-checks to cover slice+concat (with cross
  terms), reshape+transpose, and gather (with a repeated index).

## 0.24.1

Internal QA for the second-order engine, no API or behavior change.

- Forward-mode (jet) closures are now wired only while a Hessian is being
  computed, and the per-node jet state is boxed behind one pointer, so ordinary
  training and `grad` are back to their pre-v0.24 speed and memory (the v0.24.0
  release regressed the training hot path ~18% time / ~23% memory).
- Added finite-difference cross-checks for the Hessian across broadcasting,
  division, the general broadcast path, transcendentals, and matmul.

## 0.24.0

Second-order autodiff: exact Hessians.

- New `hessian(f)(x)` returns the matrix of second partial derivatives of a
  scalar function, exact (not finite differences). Together with `grad` it
  enables Newton's method and curvature analysis.
- Implemented as forward-mode 2-jets: each supported op now propagates a first
  and second directional derivative alongside its value, and a full Hessian
  follows by seeding basis directions and polarization. The reverse-mode engine
  is untouched, every existing gradient check still passes.
- Supported ops: `+ - * / %`, the unary math (`exp`, `log`, `sin`, `cos`,
  `tanh`, `sigmoid`, `sqrt`, `square`, `relu`, `abs`, `pow`, `neg`),
  `matmul`/`@`, `sum`, `mean`, and comparisons. A function using an op outside
  this set raises a clear error rather than returning a wrong Hessian.
- New example `hessian.ra`: the Hessian of a quadratic form recovers `A + Aᵀ`,
  and Newton's method minimizes a function with quadratic convergence.

## 0.23.0

Full Jacobians: differentiation beyond scalar outputs.

- New `jacobian(f)(x)` returns the whole `[m, n]` matrix of partial derivatives
  of a vector output `f(x)` (length `m`) with respect to a vector input `x`
  (length `n`), every output's sensitivity to every input, exact, by one
  reverse-mode pass per output. This is the reverse-mode Jacobian (`jacrev`).
- New example `jacobian.ra`: the Jacobian of a linear map recovers its matrix,
  and a nonlinear map matches its analytic derivatives. Uses include
  risk/sensitivity matrices, input attribution, and Jacobian regularization.
- (Second-order autodiff (`grad(grad(f))` and Hessians) remains future work: it
  needs a re-differentiable reverse pass, a dedicated engine change.)

## 0.22.0

Sequences and attention: embeddings and a transformer, not just tables and
images.

- `std/nn.ra` gains `embed(table, ids)` (a differentiable embedding lookup built
  on `gather`, so embeddings are learned), `embedding_init`, and
  `self_attention(Wq, Wk, Wv, X)`: single-head self-attention, the core
  transformer operation, differentiable end to end.
- New elementwise builtins `floor`, `ceil`, `round` (forward-only), e.g. to turn
  random draws into integer token ids.
- New example `attention.ra`: a self-attention sequence classifier (embed →
  self-attention → pool → dense) trained with Adam; `grad` differentiates the
  attention softmax and the embeddings together.
- Raster now spans tabular (boosted trees), vision (CNNs), and sequences
  (attention), one autodiff engine, one static checker, one binary.

## 0.21.0

Data pipeline and real minibatch training.

- New differentiable builtin `gather(x, indices)` selects rows of `x` by an index
  list or 1-D tensor (gradient scatter-adds back, so repeated indices, e.g.
  embedding lookups, accumulate correctly). Gradient-checked.
- New `permutation(n)` returns a seeded random ordering of `0..n-1`, and `int(x)`
  truncates a scalar, together enabling reproducible shuffling and sizing.
- New `std/data.ra`: `standardize` (per-column z-score, returns the transform),
  `apply_standardize`, `train_test_split`, and `shuffle` (features/labels kept
  aligned).
- New example `minibatch.ra`: a genuine training loop, standardize, hold out a
  test set, then train a classifier with Adam over reshuffled minibatches each
  epoch (96%+ held-out accuracy). This is the mechanics real models train with,
  not full-batch toys.

## 0.20.0

Model persistence: train once, save, and deploy.

- New builtins `save(value, path)` and `load(path)` write and read any value,
  tensors, records, lists (a model's whole pytree), scalars, strings, bools, and
  fitted gradient-boosted models, in a compact, exact binary format (float64
  bit patterns round-trip bit-for-bit).
- `gbm.Model` now implements `encoding.BinaryMarshaler`/`BinaryUnmarshaler`, so a
  trained forest can be persisted and reloaded with identical predictions.
- New example `save_load.ra`: trains a classifier, saves it, loads it back, and
  confirms the reloaded model predicts identically; a neural net's parameter
  record round-trips too.
- Paths resolve relative to the running script (like `read_frame`), via a shared
  `resolvePath` helper.

## 0.19.0

Convolutional neural networks: general deep learning, not just MLPs.

- New differentiable builtins `conv2d(input, weight)` and `maxpool2d(input, k)`.
  `conv2d` is a 2-D cross-correlation (`input` `[Cin, H, W]`, `weight`
  `[Cout, Cin, KH, KW]` → `[Cout, H-KH+1, W-KW+1]`); `maxpool2d` does
  non-overlapping `k×k` max pooling per channel. Both have gradient-checked
  backward passes (input and weight), so `grad` trains a conv net end-to-end.
- New `std/nn.ra` helpers: `conv` (a conv layer with per-channel bias) and
  `conv_init` (He-initialized kernel + zero bias).
- New example `cnn.ra`: a real conv net (conv → relu → max-pool → dense →
  sigmoid) that learns to tell vertical from horizontal bars in noisy images,
  trained with Adam over the whole model (the nested conv kernel included).
- The checker infers conv/pool output shapes where the inputs are known.
- Positioning: Raster now spans the ML stack: neural nets (incl. CNNs),
  gradient-boosted trees, and backtesting, in one dependency-free binary.

## 0.18.0

Gradient-optimized trading signals.

- Because the backtest Sharpe is differentiable in the return series and a smooth
  signal's returns are differentiable in its weights, `grad` gives the gradient
  of Sharpe with respect to the weights, so a signal can be tuned by gradient
  ascent, straight through a backtest. This is the kind of end-to-end autodiff a
  plain Python backtest can't do without JAX.
- New example `signal_opt.ra`: learns a linear signal's weights on a synthetic
  market by climbing its annualized Sharpe, recovering the true signal direction
  and turning a negative-Sharpe asset into a positive-Sharpe strategy.
- New `sortino(r, periods)` in `std/backtest.ra`: the annualized Sortino ratio
  (downside-deviation-adjusted, differentiable).
- Internal: removed dead code and added a CI lint job (`deadcode` + `staticcheck`)
  so it can't return.

## 0.17.0

Backtesting toolkit (finance roadmap #6).

- New cumulative-scan builtins: `cumsum`, `cumprod`, `cummax`, `cummin`: the
  vectorized primitives for signals, equity curves, and running peaks.
- New `std/backtest.ra` library: `returns`/`log_returns`, `sma` (moving average
  via prefix sums), `equity` (cumulative-product equity curve), `max_drawdown`,
  `sharpe`, `ann_vol`, `total_return`, and `cagr`. The Sharpe ratio is
  differentiable in the return series, so a smooth signal can be tuned by
  gradient ascent.
- New example `backtest.ra`: a long-only k-day momentum strategy on a synthetic
  price series, reported against buy-and-hold (total return, CAGR, vol, Sharpe,
  max drawdown), with the position lagged a day to avoid look-ahead.
- This completes the finance roadmap in `docs/finance.md` (#1–#6).

## 0.16.0

Native gradient-boosted trees (finance roadmap #5).

- A pure-Go gradient boosting engine (`internal/gbm`) using the second-order
  (Newton) formulation, so squared-error regression and logistic binary
  classification share one tree builder. No XGBoost, no Python, no native deps;
  it stays a single static binary.
- `gbm_fit(X, y)` or `gbm_fit(X, y, opts)` trains on a `[n, d]` feature matrix
  and an `[n]` target/label vector. `opts` is a record of hyperparameters:
  `rounds`, `learning_rate`, `max_depth`, `min_leaf`, `lambda`, `gamma`, and
  `objective` (`"squared"` or `"logistic"`). It returns an opaque model.
- `gbm_predict(model, X)` scores a `[n, d]` matrix, returning `[n]` raw scores
  for regression or probabilities for a logistic model.
- Deterministic: exact-greedy splits with pre-sorted features, and the
  per-feature split search parallelizes across cores while reducing in fixed
  order, so fits are bit-identical run to run regardless of scheduling.
- New example `gbm.ra`: a train/test split on a synthetic loan book, fitting a
  logistic default classifier and a regression model on the same features.

## 0.15.0

Units of measure (finance roadmap #4).

- Declare base units with `unit USD` (like `type`, top-level). Annotate scalars
  with a unit or a compound unit expression: `px: USD/share`, `rate: USD/year`,
  `t: year^-1`, `-> USD`.
- The checker tracks units through arithmetic: `*` multiplies them, `/` divides,
  `+`/`-`/comparisons require a match, `^` with a constant exponent raises them,
  and `sqrt` halves them. `exp`/`log`/`sin`/`cos`/`tanh`/`sigmoid` require a
  dimensionless argument. `matmul`/`dot` multiply the operand units.
- Introduce a unit on a value with a `let` annotation (`let px: USD/share =
  150.0`); bare numeric literals are dimensionless and adopted into the
  declared unit. Undeclared unit names in an annotation are reported.
- Units are checked statically and fully erased at runtime: annotated code
  computes the same plain numbers with zero overhead, and unannotated code is
  unaffected (everything is dimensionless).
- New example `units.ra`: notional value and accrued interest, with the checker
  proving price × quantity is money and rejecting dollars + shares.

## 0.14.0

Data frames (finance roadmap #3).

- A frame is a record whose fields are named column tensors, so field access,
  slicing, and `grad` all work on it, and a numeric time column makes it a time
  series. No new type, and it composes with everything.
- `read_frame(path)` loads a CSV with a header row into such a record;
  `write_frame(frame, path)` writes one back.
- `columns(rec)` lists the field names, `field(rec, name)` looks one up by
  string, and `with_field(rec, name, value)` returns a copy with a field set.
- New example `frames.ra`: loads prices, computes daily log returns and realized
  (annualized) volatility, and adds a column with `with_field`.
- Kept pure Go / zero dependencies, Parquet/Arrow would need a third-party
  module, so it's deferred.

## 0.13.0

Faster core numerics (finance roadmap #2).

- Full reductions (`sum`/`mean`) run across cores and stay deterministic:
  fixed-size blocks are summed independently and their partials combined in a
  fixed order, so the result is the same on any number of cores. ~3.3x on a
  million-element reduction.
- The backward passes for same-shape elementwise and unary ops run across cores
  too (each goroutine writes a disjoint slice of the gradient), so gradient
  computation on large tensors uses all cores, not just the forward pass.
- Together with v0.12's parallel forward ops, both the value and the gradient of
  large-tensor work (Monte Carlo, backtesting) are now multicore. Matmul is
  already row-parallel and cache-friendly; explicit blocking is left for later.

## 0.12.0

Multicore tensor ops (finance roadmap #1).

- Large elementwise, unary, and matmul forward passes now run across CPU cores.
  Each goroutine writes a disjoint slice of the output, so it's race-free and
  **bit-identical to a serial run**, parallelism never changes a program's
  result, and randomness stays deterministic. Small tensors (typical training
  parameters) run serially, below a size threshold.
- Measured scaling on large ops (1 core -> 16): `exp` ~3.8x, 256x256 matmul
  ~4.5x, elementwise add ~2x (memory-bandwidth bound). This is the biggest
  pure-Go speed lever for the Monte-Carlo and backtesting workloads.
- CI now runs the suite under the race detector.

## 0.11.0

Deterministic randomness, and the first finance step.

- Randomness is now deterministic by default: a program gives the same result
  every run. `seed(n)` chooses the starting point. Reproducibility like this is
  what model governance and audit require. (Because runs are reproducible, the
  formatter's behavior-equivalence test now covers the stochastic examples too.)
- New example `montecarlo_option.ra`: prices a European call by Monte Carlo and
  computes its Greeks (delta, vega) by autodiff, matching Black-Scholes closed
  form, with no bump-and-revalue.
- `docs/finance.md`: an honest assessment of where Raster can beat a Python
  stack for financial ML under a pure-Go, no-native-deps constraint, and the
  roadmap to get there.

## 0.10.0

A formatter, and a tape tweak.

- `raster fmt` reprints a program in a canonical style and preserves comments
  (leading and trailing). It parenthesizes only where needed to keep the
  operator tree, is idempotent, and refuses rather than move a comment it can't
  place. Add `--write` to format in place.
- The lexer now records comments (`TokenizeWithComments`), and the parser
  exposes them (`ParseWithComments`).
- Autodiff tape: parent pointers for the common one/two-input ops are stored
  inline instead of in a per-op slice, trimming an allocation per differentiable
  op. Measured as a small net win on the training benchmark; the tree-walking
  interpreter is otherwise near its floor, so a real speed jump needs a
  vectorized/bytecode backend (tracked in the roadmap). Gradient checks still
  pass.

## 0.9.0

Container-agnostic optimizers (pytrees).

- `map_leaves(f, tree)` and `zip_leaves(f, trees)` walk the tensor leaves nested
  inside lists and records, preserving structure.
- The standard optimizers (`sgd_step`, `momentum_step`, `adam_step`, and
  `zeros_like`) are rewritten on top of them, so the same code trains a model
  whether its parameters are a positional list or a named record.
- `examples/records.ra` now trains its record-based model with the library's
  Adam instead of a hand-written update.

## 0.8.0

Einsum and earlier error detection.

- `einsum(spec, ...tensors)`: a differentiable Einstein-summation primitive.
  Covers matrix multiply (`"ij,jk->ik"`), transpose (`"ij->ji"`), reductions
  (`"ij->i"`, `"ij->"`), bilinear forms, and general contractions. The gradient
  of an einsum is itself an einsum, so it backprops exactly. Repeated labels
  within a single operand (traces/diagonals) are not supported yet.
- The checker infers an einsum's output shape from a literal spec and known
  input shapes, and reports malformed specs and rank mismatches.
- The checker now checks each function body at its definition, using the
  parameter annotations, so shape mistakes, field typos, and return mismatches
  are caught even in functions that are never called. Unannotated parameters
  stay unknown, so this adds no false positives.
- New example `einsum.ra`.

## 0.7.0

Declared record types and a faster interpreter.

- Declared record types: `type Model = { w: [3, 2], b: [3] }`. A parameter can be
  typed with it (`fn f(m: Model)`), and the checker verifies the record passed
  in has the right fields with the right shapes.
- Field typos are caught: accessing a field a record doesn't have is a checker
  error (`record has no field "wong"`).
- Performance: elementwise/unary ops skip building a backward closure when no
  input needs a gradient (parameter updates and other forward-only math). A
  100-step linear-regression training loop dropped from ~372us to ~300us per run
  (~19%) with ~800 fewer allocations; environments also allocate their map
  lazily. All gradient-check tests still pass.
- Examples are now run in-process by the test suite, so `go test` covers them
  end to end (not just the built binary).

## 0.6.0

Records and modules.

- Records with named fields: `{ w: [1.0, 2.0], b: 0.5 }`, accessed with `.`
  (`p.w`). A `{` is a record when followed by `name:`, otherwise a block.
- `grad` follows record structure: differentiating a loss over a record of
  parameters returns a record of gradients with the same fields, so a model can
  live in a record instead of a positional list.
- Namespaced imports: `import "std/nn.ra" as nn` binds the module's definitions
  as a record, called as `nn.dense(...)`. Plain `import` still shares scope.
- The checker understands records and field access, and reports records used as
  numbers or called as functions.
- New example `records.ra`: an XOR net whose parameters live in a record,
  trained via a namespaced import of the nn library.

## 0.5.0

Slicing, shape variables, and a faster tensor engine.

- Slicing: `v[1:3]`, `v[:2]`, `v[2:]`, and `m[0:2]` along the first axis, for
  both tensors and lists. Tensor slicing is differentiable.
- Shape annotations can use named dimensions (shape variables). A name used in
  more than one place must resolve to the same size, so the checker ties shapes
  together and verifies the declared return, e.g.
  `fn mm(A: [n, k], B: [k, m]) -> [n, m]`.
- The checker also reports argument rank mismatches against an annotation.
- Performance: the elementwise/broadcast path was rewritten to avoid per-element
  division: fast paths for equal shapes and scalar operands, and an
  odometer-style walk for general broadcasting (~3x on the broadcast benchmark).
  All gradient-check tests still pass.
- The email in the git history was replaced with a GitHub noreply address.

## 0.4.0

Usability and distribution.

- `read_csv(path)` loads a file of numeric rows into a `[rows, cols]` tensor.
- The REPL handles multi-line input: it keeps reading until brackets balance,
  so you can define block-body functions interactively.
- Prebuilt binaries for Linux, macOS, and Windows are attached to each release;
  `go install github.com/twill-lang/raster/cmd/raster@latest` also works.
- A release workflow builds and publishes the binaries on a version tag.
- Added a getting-started tutorial (`docs/tutorial.md`).

## 0.3.0

Broadcasting, many more operations, real optimizers, and better tooling.

- Full NumPy-style broadcasting for elementwise ops, with correct gradients
  (matrix + row vector, column broadcasting, etc.).
- New differentiable ops: `square`, `clip`, `maximum`, `minimum`, `where`,
  `softmax`, `logsumexp`, `reshape`, `transpose` (arbitrary axes), `concat`,
  and elementwise comparisons (`greater`, `less`, `equal`, ...).
- Axis-aware reductions: `sum`, `mean`, `max`, `min`, and `argmax` take an
  optional axis argument.
- List helpers: `fold`, `append`, `enumerate` (plus the existing `map`, `zip`).
- Standard library: `std/optim.ra` adds SGD, momentum, and Adam over parameter
  lists; `std/nn.ra` gains initializers (He, Xavier), `gelu`, `softplus`, and
  softmax cross-entropy (`cross_entropy`, `onehot`).
- New example `classifier.ra`: a 3-class MLP trained with softmax cross-entropy
  and Adam.
- The shape checker understands broadcasting and the new ops.
- CLI errors now show the source line and a caret.
- A parser rule so a `(` or `[` starting a new line begins a new expression,
  matching the existing rule for `+`/`-`.
- Gradient-check tests (finite differences) for every op, plus benchmarks.

## 0.2.0

- Reimplemented in Go as a single dependency-free binary (from the earlier
  TypeScript prototype).
- Static shape checking with optional parameter/return shape annotations.
- An `nn` library written in Raster, loadable via a new `import` statement.
- `grad`/`grads` differentiate through list-structured arguments.
- `map`/`zip` builtins.

## 0.1.0

- First prototype (TypeScript): lexer, parser, tree-walking interpreter, a
  reverse-mode autodiff tensor engine, and the `grad` family.
