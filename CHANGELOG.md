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

  What it replaced was 424 lines of Go runtime traceback on stderr, nothing on
  stdout, and exit code 2. Not one of those 424 lines named the user's function
  or the line the recursion was on, and 2 is the status `twill` uses for "you
  invoked it wrong" (`twill run` with no file exits 2), so the crash was not
  even distinguishable by status from a typo on the command line. A Go stack
  overflow is a *fatal* error: no recover catches it, so the only way to get a
  diagnostic at all is to refuse before the stack runs out. `fn fact(n) = n *
  fact(n - 1)` with the base case forgotten is the most likely first mistake
  anyone makes in a new language, and it was the most likely first thing they
  saw. It now exits 1, like any other program that failed.

  10,000 is far above what working code needs, measured rather than assumed:
  across all 66 `.tw` files in this repository that run as programs the deepest
  recursion any of them reaches is 14 nested calls, median 3, and the deeper
  recursion here is the self-hosted compiler, which peaks at 217 on the `src/`
  files measured, checking `src/parse.tw`.

  **The limit is a diagnostic, not a guarantee, and that is worth reading before
  relying on it.** What decides where the host's stack runs out is not what the
  twill frame holds, which costs nothing measurable, but how deeply the
  recursive call sits inside the expression around it: every enclosing operator
  is one more evaluator frame held open across the call. On this machine a
  runaway survives 233,013 nested calls when the call is bare, 147,815 with one
  `+ 1` around it, 12,739 with thirty layers and 1,340 with three hundred.
  Because expression nesting has no upper bound, **no fixed call limit is below
  the crash for every program**, and 10,000 is not an exception. What it does
  cover was bisected, and it depends on the shape as well as the count: a
  runaway nested inside up to 38 layers of flat arithmetic (`1 + 1 + ... + f(n)`),
  which survives 10,174 calls, or 24 layers of `[x][0]`, the most expensive
  layer measured, which survives 10,357. Deeper than that and the fatal overflow
  is back, which is no worse than before this change but is not what the limit
  promises. The remedy, for anyone who has such a shape, is to bind the call to
  a `let` and use the name: the same 39 layers then reach 232,993 again.
  `docs/needs.md` NEEDS-30 has the tables and the two ways to close the gap that
  were not taken here.

- **`TWILL_MAX_CALL_DEPTH`, so the two engines can refuse a program with the
  same words.** Running the self-hosted evaluator on the bootstrap puts two
  counters over one stack, and the outer depth is `8*inner + 9` exactly, so the
  host stops first and names a function inside `src/eval.tw`. No shared constant
  fixes that: reaching L inside costs 8L+9 outside, which is more than L for
  every L. The host has to be handed the larger number.

  ```
  TWILL_MAX_CALL_DEPTH=100000 twill run src/main.tw run prog.tw
  ```

  prints for `prog.tw` exactly the bytes `twill run prog.tw` prints. 100,000 is
  above the 80,013 the self-hosted evaluator needs, bisected on the shipped CLI
  rather than derived, and not so far above it that the host runs out of stack
  on the way.

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

  A top-level `break` under `--no-check` was the same thing in 15 lines
  (`panic: (interp.breakSignal)`) and now says ``  `break` outside a loop``.
  Both used to exit 2, twill's code for a bad invocation; both now exit 1.

  The person at the keyboard is running a twill program and cannot act on a Go
  stack. This is the second half of the recursion limit and not a replacement
  for it: the one fault most likely to be hit, a stack overflow, is the one
  fault a recover cannot catch.

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