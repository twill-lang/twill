# Changelog

## [Unreleased]

### Added

- **`twill check` and `twill fmt` take several paths, and directories.** Both
  took exactly one path, which is not how anyone invokes a checker or a
  formatter: `twill fmt src std` and `twill check .` were usage errors. A named
  file is taken as given, a directory means every `.tw` file under it
  (dot-directories skipped, so `.` does not walk into `.git`), named files keep
  their order and a file named twice is visited once. Every file is visited even
  after one fails, because running a checker once per broken file to find out how
  many there are is not a report.

  `twill run` still takes one path, on purpose. Everything after it belongs to
  the program: `scriptArgs` hands the rest to the `args` builtin, which is how
  `twill run main.tw run io_test.tw` drives the self-hosted compiler in
  `tools/conformance`. A second path and a forwarded argument are the same word
  and no rule can separate them, so forwarding wins.

- **`twill fmt --check`.** Writes nothing, prints the path of every file that
  would change, and exits 1 if there is one. `--check` together with `--write` is
  refused rather than resolved by precedence, since they are opposite answers to
  the same question. A flag neither command recognises is now named and refused
  too: silently ignoring `--chekc` would have made the new gate un-runnable in
  precisely the case it matters.

  The CI step is **not** switched on in this change. 386 of the repository's 499
  `.tw` files are not in canonical form, and almost none of that is whitespace:
  the formatter normalises `3.0` to `3` and flattens every block-structured
  expression onto one line, so turning the gate on means reflowing the tree
  first. That is a separate change with a separate diff to read.

### Changed

- **`twill fmt` keeps the blank lines between paragraphs of statements.** The Go
  printer emitted one line per statement and dropped them, so one `twill fmt
  --write` turned a function's paragraphs into an undifferentiated run.
  `src/fmt.tw` has preserved them since `maybe_blank` landed, and the two
  formatters were recorded as diverging on the point (NEEDS-78). `maybe_blank` is
  now ported into `internal/format` rather than re-invented: the same gap of two
  or more source lines, measured from the previous statement's last line
  (`ast.StmtEndLine`, itself a port of `stmt_end_line`) to the next statement's
  leading edge, which is its first own-line comment when it has one. However many
  blank lines the author left, one comes out.

  Formatting every `.tw` file in the corpus under both implementations and
  comparing bytes went from **119 divergences of 468 files to none**.

- **An unrecognised subcommand says so.** `twill chekc x.tw` reported `cannot
  read file "chekc"`, which reads as a missing file and sends the reader looking
  for one. A first word that is not a command, has no extension, has no separator
  in it and names nothing on disk is a typo, and the CLI now says
  `unknown command "chekc"` and points at `twill help`. A path still runs:
  `twill prog.tw`, `twill ./prog`, and `twill prog` where `prog` exists.

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