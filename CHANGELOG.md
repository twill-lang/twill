# Changelog

## [Unreleased]

### Added

- **Rank-preserving reductions.** Every builtin that removes an axis takes a
  third argument that leaves it in at length 1 instead, which is what other
  array libraries spell `keepdims`:

  ```
  sum(t, 1)          # a [2, 3] becomes a [2]
  sum(t, 1, true)    # a [2, 3] becomes a [2, 1]
  ```

  The point is the shape. Broadcasting aligns from the right, so a `[2]` does
  not line back up against the `[2, 3]` it was reduced from and a `[2, 1]` does;
  `t - mean(t, 1, true)` centres each row, where `t - mean(t, 1)` was a shape
  error. `num.keep` and `nn.keepdim`, which wrapped the reshape and the
  `broadcast_to` by hand, still work and are still the way to expand against a
  shape that is not one of the operands.

  It applies to `sum`, `mean`, `max`, `min`, `prod`, `median`, `logsumexp`,
  `argmax` and `argmin`. `argmax` and `argmin` return positions rather than
  values, and that changes nothing about what the flag means, because it is a
  claim about the shape and not about the numbers in it: `argmax(t, 1, true)`
  is a `[2, 1]` of indices, which is the shape you need to compare against the
  input those indices point into. `softmax` and `flip` preserve the shape they
  were given and `diff` shortens an axis rather than removing one, so none of
  those three takes the flag. What they do with a third argument is not uniform
  and is unchanged here: `flip(t, 1, true)` and `diff(t, 1, true)` are refused,
  while `softmax(t, 1, true)` runs and ignores it, as `softmax` has always
  ignored a trailing argument on both implementations. That is documented in
  `docs/language-guide.md` and pinned by a test rather than tightened, because
  tightening it would turn calls the corpus has always accepted into failures.

  The flag is positional, because twill has no named arguments (roadmap entry
  29). Unlike `sort`'s `descending` and `topk`'s `smallest`, which are numbers,
  it may be written as a Bool or as a number: `sort(t, 0, true)` is still a
  runtime error and `sum(t, 0, true)` is not.

  Both implementations compose it the same way, as the reduction followed by a
  reshape, so no reduction's backward pass has to know the flag exists and the
  gradient is unchanged. Both checkers fold it into the static shape, so the
  broadcast error the flag exists to remove is gone at check time and the one it
  does not remove is still reported.

### Fixed

- **`sum()` with no arguments panicked the Go checker** instead of reaching the
  arity error the runtime already had for it. Every reduction did. The
  self-hosted checker did not, so it was also a divergence.
- **Tuple returns, and destructuring `let`.** A function with two things to say
  and no name for either returns them both:

  ```rust
  fn span(xs: Arr[F64]) -> (F64, F64) { ... }

  let (lo, hi) = span(xs)
  ```

  That is `docs/roadmap.md` entry 1's third workaround, the one `Res` did not
  answer: the entry records loom's `Batch` and `StepResult`, and weft's four
  type names in a library with eleven concepts, as structs declared for a single
  call site because a function could return one value.

  **The comma is what decides.** `(x)` is `x` in parentheses and stays grouping;
  `(x, y)` is a tuple. `(x,)` is refused rather than invented:

  ```
  a tuple holds at least two values: (x,) is not a one-element tuple, and (x)
  is x in parentheses
  ```

  A tuple holds two to eight values. The ninth is refused, and says why:

  ```
  a tuple holds at most 8 values, but this one has 9; a value with that many
  parts wants a struct, whose fields have names
  ```

  **A tuple is destructured or passed on whole.** There is deliberately no `.0`
  and no named tuple type. Reading a part by name is an error, printing one
  gives `(1, 2)`, and equality is structural and starts with arity, so a 2-tuple
  is never equal to a 3-tuple and a tuple is never equal to a list holding the
  same values.

  `(F64, F64)` is a type wherever an annotation may appear -- a return, a
  parameter, a binding, a struct field, a type argument -- and tuple types nest.
  `substParams` descends into one, so `struct Pair[T] { span: (T, T) }` at
  `Pair[I64]` has an `(I64, I64)` field rather than a pair of unsubstituted
  parameters that judge nothing.

  All of it lands on both implementations: the parser, the checker, the
  evaluator and the formatter, in Go and in `src/*.tw`, with the diagnostics
  written the same on both sides. `TestSelfHostedCheckTuples` and
  `TestSelfHostedTupleSyntaxRefusals` compare the diagnostic text and not only
  the exit code, and `TestSelfHostedTupleEvaluationMatches` compares printed
  output byte for byte.

  A tuple is also not a pytree: `grad`, `map_leaves` and `zip_leaves` treat one
  as an opaque leaf rather than walking into it, and `save` refuses it by name
  (`save: cannot save a value of this kind ((1, 2))`). The tracer does walk
  one, which is not optional: a tensor returned inside a tuple escapes its
  statement exactly as a tensor in a list does, and a `liveTensors` that had
  not been taught about tuples crashed the traced run rather than slowing it.
  `TestTracingSeesTensorsInsideATuple` is that case.

  **A destructuring `let` is a binding, and both rules that govern one apply.**
  `const A = 1.0` followed by `let (A, b) = (2.0, 3.0)` is refused with the
  same message `let A = 2.0` gets, and `let (a, a) = (1.0, 2.0)` is refused by
  name. Both were holes in the first cut of this change: both were silent, both
  implementations agreed on the wrong answer -- `print(A)` gave 2 under each,
  and the repeated name let the last position win under each -- so the
  conformance gate could not see either, which is what agreement between two
  implementations is worth on its own. `_` is exempt from both, because `_`
  binds nothing.

  The parser's refusal of `const (a, b) = ...` is reworded as part of that. It
  used to give its reason as the const-rebinding rule being "checked over single
  names", which counting a destructuring `let` makes untrue; it now says that
  `const` declares a guarantee about a single name and nothing yet asks to
  declare several at once, and adds that a name a destructuring `let` binds is
  still refused when a `const` in the same scope already binds it. A refusal
  that explains itself with something the checker no longer does is worse than
  one that does not explain itself.

  **What is not delivered:** a tuple pattern in `match`, a tuple element
  coerced by its annotation (`-> (I64, I64)` does not turn a `Num` into an
  `Int` the way `-> I64` does), tuples as pytree containers, `save` of a
  tuple, and `const (a, b) = ...`, which is refused at the parser: a `const`
  declares a guarantee about one name, and the positions of a tuple are not
  that.

### Fixed

- **A type argument is a full type under both front ends.**
  `Arr[fn(I64) -> I64]` was a syntax error to the Go parser and clean to
  `src/parse.tw`: `parseTypeArgs` read a type *reference* where
  `parse_type_args` read a type *expression*. That is a divergence in the two
  parsers older than this change, and putting a tuple into type-argument
  position is what found it. Both read a type expression now, so a function
  type and a tuple both nest there.

## [1.11.0] - 2026-09-05

### Added

- **`std/test`, the assertions the test runner already assumed.** `twill test`
  shipped in 1.8 and solved discovery only: it finds every `*_test.tw` under a
  path, runs each on a fresh interpreter, and reads the file's verdict out of
  what the file printed. That last part is a contract, and until now the
  toolchain defined it and shipped nothing that satisfied it. Every repository
  in the ecosystem wrote its own satisfier by hand instead: eleven harness files
  and 914 lines by `wc -l`, nine of them the near-identical satellite copies
  (711 lines) and two more here.

  The module is `mode systems` and exports six names: `check(name, ok)`,
  `equal_str`, `equal_i64`, `near(name, got, want, tol)`, `fail(name, why)` and
  `report(suite) -> I64`. A suite is then

  ```
  mode systems

  import "std/test" as t

  fn main() -> I64 {
    t.equal_i64("two and two is four", 2 + 2, 4)
    t.report("arith")
  }
  ```

  **`near` takes the tolerance and never defaults it.** That is the one place
  the eleven copies could quietly disagree about what a float comparison means,
  and a default is always wrong for something: 1e-12 rejects a legitimate result
  from an erfc approximation good to 1e-7, and 1e-7 accepts a broken LU. The
  comparison is `<=`, so a tolerance of `0.0` asserts exact equality rather than
  asserting nothing that can hold.

  **`report` prints the two markers `cmd/twill/test.go` greps for** and nothing
  else: `<suite> passed <p> failed <f>`, then `OK` or `FAILED`. The word order
  is not cosmetic. The runner reads the field *after* each keyword, so
  `spool: 3 passed, 1 failed` -- the spelling all nine satellite copies settled
  on -- matches neither signal, and `twill test` reports such a suite with no
  counts beside it. The verdict still comes out right, because those copies end
  in `exit(1)`, but a suite whose summary the runner could read would not have
  needed the exit. `cmd/twill/test_test.go` pins both markers against a fixture
  suite with deliberate failures in it, which is the only place the negative
  path can be asserted: a suite cannot record a failing check and still report
  itself green.

  **`report` returns the exit status rather than raising it.** A suite ends
  `t.report(...)` as the last expression of `fn main() -> I64`, so the status is
  the file's own return value and the harness needs no `exit`.

### Changed

- **The six systems-mode standard-library suites now import `std/test`**, and
  `std/tests/systems_harness.tw` is gone with them. 351 assertions lost the
  leading `rp,` argument and nothing else moved: all six suites print
  byte-identical stdout and stderr before and after, and assert the same number
  of checks -- `io` 47, `json` 76, `linalg` 48, `random` 39, `stats` 58,
  `text` 80.
  `std/tests/harness.tw` stays where it is: it is the numeric-mode harness, and
  it needs untyped parameters and the tensor builtins `abs` and `max`, none of
  which exist in `mode systems`.

  **The conformance allow-list shrank by three.** `io_test.tw`, `json_test.tw`
  and `text_test.tw` now produce identical bytes under both implementations,
  exit code included, and their lines are gone from
  `testdata/conformance/suite-allow.txt`. The old harness wrote its summary with
  `write_out` and its failures with `write_err`, and `src/eval.tw` dispatches
  neither, so all three suites had been dying on the way out of a run that
  otherwise agreed line for line. `std/test` prints through `print`, which is
  dispatched. `make conformance-check` counted 5 suites agreeing before this
  change and 8 after, with 9 known divergences and none unexplained.

- **Record update: `S { ..base, field: value }`.** An expression whose value is
  a copy of `base` with the named fields replaced, so a record or a struct can
  be configured in one place instead of by a run of assignments after the
  constructor:

  ```rust
  let tuned = { ..base, lr: 0.1 }
  fn styled(d: Chart, t: Str) -> Chart = Chart { ..d, title: t, fix_y: true }
  ```

  This is `docs/roadmap.md` entry 29 (weft entry 10), which asked for optional
  and named arguments *or* record update. The entry is now half: the update is
  delivered, named arguments are not, and the reason the entry offered the
  choice is the reason this is the half that shipped -- an update is one
  expression form, while named arguments reach every arity check in both
  implementations and every builtin, whose arities are word lists with no
  parameter names in them.

  **The copy is shallow.** A field holding a container hands over the same
  container, so a `push` through the copy is visible from the base. That is not
  a rule invented for `..`: it is what `{ tags: base.tags, n: 2 }` written out
  by hand already does, and what the `with_field` builtin does, and
  `{ ..base, n: 2 } == with_field(base, "n", 2)` holds on both implementations.
  A deep copy here would have made one spelling of a record literal mean
  something the other does not. `docs/language-guide.md` says so under
  "Record update", with the program that shows it.

  The base is written first and only once; a `..` anywhere else in the literal
  is a syntax error, which is what keeps a literal from having two spellings of
  one field with a rule about which wins. A base that is definitely not a
  record is a checker error, and a base the checker cannot resolve is a runtime
  error rather than a refusal, which is the checker's standing contract about
  what it is certain of.

  A field an update names that the base does not have is added rather than
  refused. Records here are structural and the result is the record
  `{ a: base.a, b: 1.0 }` already builds, so refusing it would be a diagnostic
  on a program that runs. A *typed* update is still checked against its
  declaration, so `Chart { ..d, ttile: "x" }` is refused exactly as
  `Chart { ttile: "x" }` is.

  Nothing changes meaning: `..` was not legal in any position in a record
  literal before this, so every program that parsed still parses and still
  means what it did. The syntax is read by the parser rather than the lexer --
  `.` is punctuation and a number only starts with one when a digit follows --
  so no other position in the grammar sees a new token.

  All four halves are doubled and agree: the parser, the checker, the printer
  and the evaluator, in `internal/` and in `src/*.tw`. The two new diagnostics
  are byte-identical between them, and the differential tests in
  `internal/interp/selfhost_test.go` take the expected text from
  `parser.Parse` and `checker.Check` rather than writing it out, so a rewording
  on either side fails the build instead of splitting the implementations.

- **`ushr(x, k)`, the logical right shift.** `shr` is arithmetic, so a value
  with its sign bit set smears ones down from the top; `ushr` reads the pattern
  as unsigned and fills with zeros. `ushr(0 - 1, 1)` is `9223372036854775807`
  where `shr(0 - 1, 1)` is `-1`. The count is masked to 0..63 the way `shl` and
  `shr` mask theirs, so `ushr(x, 0)` is `x` and every count is defined. It is a
  call and not an operator: `ushr` is not a reserved word, and nothing in the
  lexer, the parser or the formatter changed.

  `std/float.tw` builds the shift by hand out of four operations and a sign
  test, and `docs/language-guide.md` carried that idiom as the answer. The
  helper stays where it is, so nothing importing it breaks; the guide now gives
  the builtin first. An earlier draft of this entry named `std/random.tw`
  alongside it, which is wrong: that file has no logical right shift, only a
  `shl` for a mask. The guide makes the same wrong claim at line 306, which is
  main's error and not this change's.

- **`sha256(s)` and `sha256_bytes(b)`, SHA-256 in lower-case hex.**
  `std/hash.tw` remains the specification and the vector suite -- it is the same
  digest written in twill over I64, and it is what says the answer is right --
  and these are the same digest at machine speed. The two are held together by
  a test that compares them over every message length from 0 to 70 bytes, which
  walks both padding boundaries. The digest is a format constant: spool's
  lockfiles, selvedge's archives and warp's cache keys each store one and read
  it back later, so a builtin that disagreed with `std/hash.tw` would stop those
  artefacts verifying.

  Two names rather than one polymorphic builtin, because the argument's type is
  the thing a caller gets wrong: `sha256` takes a `Str` and `sha256_bytes` a
  `Bytes`, and neither accepts the other.

- **`log1p` and `expm1`, differentiable elementwise, with `f64_log1p` and
  `f64_expm1` beside `f64_log` and `f64_exp`.** `log1p(x)` is `log(1 + x)` and
  `expm1(x)` is `exp(x) - 1` computed without forming the sum or the
  difference. At `x = 1e-16` the sum rounds to exactly 1, so `log(1 + x)` is 0
  and the input is gone; `log1p(x)` answers `1e-16`. The gradient rules are
  `1/(1 + x)` and `exp(x)`, with second derivatives `-1/(1 + x)^2` and `exp(x)`,
  so they differentiate to second order like the ops they sit beside.

  All three land in both implementations, except the two systems-mode scalars:
  `f64_log1p` and `f64_expm1` are dispatched by the Go bootstrap and not by
  `src/eval.tw`, which is exactly where `f64_log` and `f64_exp` already stand.
  `docs/conformance.md` records it.
- **The filesystem, the clock and the process, in the self-hosted evaluator.**
  40 more of the names in `src/builtins.tw` are dispatched by `src/eval.tw`,
  leaving 57 of 248 refused where 97 were before: the three output sinks, the
  file and path builtins, `temp_dir`, `cwd`, `mtime`, the two clocks, `env`,
  `args`, `exit`, `abort`, `window_size`, `is_tty_stdout` and `run`. A
  systems-mode program that did any I/O ran on the bootstrap and refused
  self-hosted until now; three standard-library suites left the conformance
  allow-list because of it, and nothing replaced them. `docs/conformance.md` is
  regenerated and has the list.

  **A relative path in a program now means the same file under both
  implementations.** The Go interpreter resolves one against the directory of
  the file doing the reading, which self-hosted was `src/eval.tw` rather than
  the program, so a delegation that stopped at the syscall would have closed 40
  refusals by opening a silent wrong answer. `src/main.tw` records the file it
  is running and the argument vector the bootstrap would have passed, and
  `src/eval.tw` resolves against those.

- **`make conformance-check` runs the cases under `testdata/conformance/cases/`
  as well as the standard-library suites.** Each case is a whole program run
  twice as two processes, with the exit code and both streams compared byte for
  byte and no allow-list, which is the only way to see `write_out`, `write_err`
  and `exit`: none of them goes through the interpreter's output sink, so an
  in-process comparison cannot observe them.

## [1.10.0] - 2026-09-05

### Added

- **`const`, a binding that cannot be assigned to.** It is written wherever a
  `let` can be, and both checkers refuse an assignment through the name: the
  binding itself, an element of it, a field of it, and any nesting of those
  (`REC.d[0] = ...`) alike:

  ```
  HEX is declared const on line 2, so nothing may be assigned through that
  name: not the binding, and not an element or field of it. Bind a new name
  for the changed value, or declare it with let if it is meant to change.
  ```

  **A `const` is also the only binding of its name in the scope that declares
  it.** A second `let` of that name there is refused rather than quietly taking
  the const's place:

  ```
  HEX is declared const on line 1, so the name cannot be bound a second time
  in the same scope: a second binding would take its place and everything
  after it would be assignable again. Rename one of them, or declare line 1
  with let if the name is meant to change.
  ```

  That case was a silent revocation and, worse, an order-dependent one:
  top-level consts are registered before the walk starts, so a `let` above the
  `const` was refused and a `let` below it was not. Two plain `let`s of one name
  stay legal, and an inner scope is still a different scope.

  The rule lives in the Go checker and in `src/check.tw`, word for word. The
  two refusal tests in `internal/interp/selfhost_test.go` take the expected text
  from `checker.Check` rather than writing it as a literal, so a reworded
  diagnostic on either side fails the build instead of splitting the two
  implementations.

  **What is not delivered: a caller in another file can still assign to an
  imported `const`.** That is `docs/roadmap.md` entry 28's actual complaint --
  weft's `HEX`, `QUADRANTS`, `DENSITY` and `LEVELS` are lookup tables an
  importer replaces -- and this change does not answer it. A plain `import`
  copies the name into the importing scope and the handle is shared, so a
  second file's `HEX = ...` or `HEX[0] = ...` is still accepted by both
  checkers and is still what every other importer then reads. What `const`
  refuses today is a write in the file the binding was declared in, which
  catches a library breaking its own promise rather than a caller breaking it.
  Entry 28 stays open.

  A cross-file rule was written and is withdrawn. It rode on the Go checker's
  import walk, the walk that exists for cross-module enum exhaustiveness, and
  changing that walk broke it: a file importing nine or more siblings where a
  later one declared an enum stopped being followed, so a non-exhaustive
  `match` that `main` refuses was accepted, and whether it was accepted
  depended on the order the imports were written in. The same change made an
  aliased-import walk exponential in fan-out. `internal/checker/imports.go` is
  therefore byte-identical to the file on `main`, and `check()` in
  `src/check.tw` reads one file as it always did.

  `let` was **not** made read-only at the top level instead, which is the other
  half of what weft asked for. A read-only `let` was implemented behind a flag
  and swept over the 545 `.tw` files under `src/`, `std/`, `testdata/` and
  `examples/` here plus the five satellite repositories entry 28 counts (spool,
  loom, bobbin, weft, warp): it refused 45 of them, including this repository's
  own `std/tests/harness.tw` and `src/eval.tw`, the test harness in every one
  of the five satellites, warp's `examples/train.tw`, fourteen `testdata/cases`
  fixtures, and twelve numeric-mode programs under `examples/` whose training
  loop is written at file level, ten of which are mirrored again under
  `testdata/examples/`. Making `let` read-only would have refused all of them,
  so the guarantee is asked for rather than imposed.

  One further limit is deliberate and is written down in
  `docs/language-guide.md` under **`const`**. It is not a deep freeze:
  `HEX[0] = ...` is refused but `push(HEX, x)` is not, and neither is a
  function handed the handle, because `Arr` has reference semantics and nothing
  tracks where a handle goes.