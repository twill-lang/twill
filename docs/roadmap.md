# The twill language roadmap

## Why this list is ranked by caller count

Six codebases were written in twill by six agents who could not see each other's
work. Each one hit walls and wrote them down: `docs/needs.md` in this repository
for the self-hosted compiler, the CLI and the standard library, and the same
file in `twill-lang/spool` (package manager), `twill-lang/loom` (training
framework), `twill-lang/bobbin` (benchmarking), `twill-lang/weft` (plotting) and
`twill-lang/warp` (data pipelines).

That independence is the only thing that makes this document worth more than a
wish list. A feature one agent asked for once is one program's taste. A feature
five programs reached for separately, in five different problem domains, having
never read each other's source, is a property of the language. So the organising
principle here is the number of independent callers, not severity, not
implementation cost, and not the order the entries were written in.

The count is of the six codebases, named. Where a codebase depends on a feature
without filing an entry for it, that is said rather than counted, because a
silent dependency is weaker evidence than a written one.

Each entry gives what the feature is, who needs it and where, what the code does
today instead, and what that costs. The last part is the one that gets skipped
and it is the one that justifies the work. An ugly workaround is a measurement.

Everything here was checked against the source rather than copied from the
needs files. Where a needs file was wrong, the correction is in "Contradictions
in the sources" at the end.

---

## Status, as of 1.9.0 plus unreleased `main` (checked 2026-09-04)

This document was written when none of it was built, and it went stale without
saying so: entries kept being delivered and only two of them (5 and 21) were
ever marked. Twenty-six of the thirty-two are now delivered, two are half
delivered, and four are untouched. One of the twenty-six, entry 30, is delivered
on `main` and unreleased; the other twenty-five are in a tagged release.

The ranking is left exactly as it was. It is an argument about evidence, not a
queue of open work, and rewriting the order now that the work is done would
destroy the only thing the document is for. What has changed is that every row
carries the release that delivered it, and every entry that is delivered says so
in its own first line. The workaround each entry describes is written in the
present tense throughout; read those as the state at the time of filing, because
a workaround stays in a satellite repository until that repository adopts the
builtin, which is a separate piece of work from shipping it.

Delivery was verified by running the current binary, not by reading the
changelog, which is how the two half-done entries and the still-open `record()`
were caught. The release named in each row is where the feature first shipped,
taken from the changelog and, where the changelog was silent, from the tag the
defining commit first appears in. The four still open are 24, 28, 29 and 32.
The two half done are 17 and 31.

"Delivered" throughout means delivered by the Go bootstrap, which is the binary
users run. It does not mean delivered by both implementations: the section below
measures where the two agree and where they do not, and the answer is that the
front end agrees across the corpus and the evaluator does not.

---

## What the second implementation agrees on, and what it does not

Measured 2026-09-04, on this branch merged with `main`, by running both sides
over **every `.tw` file this repository tracks**, found with
`git ls-files '*.tw'` and therefore recursive rather than top level.
"Bootstrap" below means `./twill <command> <file>`. "Self-hosted" means
`./twill run src/main.tw <command> <file>`, which is the entry point
`internal/interp/selfhost_test.go` uses and the one `src/main.tw`'s own header
calls byte-locked to the Go binary.

No file count appears in this section, deliberately. An earlier draft of it gave
one, and the number was the four directory names `testdata/cases`, `examples`,
`std` and `src` read as top-level globs, which silently dropped everything in
their subdirectories and everything under `bench/`. The corpus grows with every
fixture anyone adds, so a sentence with a count in it is a sentence that goes
wrong on its own. The claim here is about every file `git ls-files '*.tw'`
reports, and the way to re-derive it is to run both commands over that list.

### The front end agrees

`check` agrees on every file in the corpus, exit status for exit status,
`src/eval.tw` included.

It is not fast, and how not fast matters to anyone repeating the run. Some
files take minutes through the self-hosted checker against well under a second
on the bootstrap; `src/eval.tw` and `src/check.tw` are both over a minute.
Length is not the whole story -- `std/linalg.tw` is longer than `src/fmt.tw` and
finishes in about a second while `src/fmt.tw` takes several -- so the slow set
cannot be predicted from size. A harness with a
per-file cap in the tens of seconds reports the slow files as timeouts rather
than as agreement, which is a different answer from disagreement and must not be
read as one. Give the cap minutes.

`fmt` agrees on every file once blank lines are set aside. Most files are
byte-identical. The rest differ only in a by-design blank-line rule, and in
every one of them the extra blank lines are on the self-hosted side, never the
Go side; deleting blank lines from both outputs makes every file match exactly,
and no token differs anywhere in the corpus. The rule is wider than the case
that was first looked at: the self-hosted printer keeps a blank line above a
comment block, which is the `examples/hello.tw` case, and it also keeps one
above a plain declaration, which is what `bench/matmul.tw` shows, where the only
two differences in the file are the blank lines above `fn gflops` and above
`fn bench`. Both shapes are the same divergence and neither reaches a token.

### The evaluator does not

`src/eval.tw` dispatches a builtin by name and ends its chain with
`"builtin ... is named in the builtin table but has no implementation"`. A large
minority of the names in `src/builtins.tw` still reach it. `docs/BUGS.md` entry
12 records the port that closed the lists, dictionaries, byte buffers and string
primitives, and its Open section carries what is left -- the filesystem, the
clock, the process, the RNG, the `f64_*` scalar intrinsics, the GPU stubs and
the memory counters -- with the count as measured and the one-line probe that
re-derives it. That is the place to read it: a split quoted in two documents
goes stale in one of them.

What matters here is the shape rather than the count. The missing set is the
systems half of the language, and the systems half is the dialect `src/` is
itself written in, so the shortest statement of the gap is that `src/` cannot
run `src/`:

```
$ ./twill run src/main.tw run "$PWD/src/main.tw" run "$PWD/examples/hello.tw"
<repo>/src/main.tw:47: runtime error: builtin "args" is named in the builtin table but has no implementation
  47 |   let a: Arr[Str] = drop_first(args())
```

(`<repo>` is the checkout's absolute path. The inner paths are absolute because
the self-hosted CLI resolves a relative one against its own directory rather
than the caller's. That is the same bug `examples/frames.tw` hits, below.)

The compiler is written in the half of the language the compiler cannot
evaluate. That is the distance left to a Go-free binary, and no count of
agreeing numeric-mode programs measures it.

Over `examples/`, three divergences reproduce today and each is a different
kind:

- **`examples/gbm.tw`** runs on both sides, exits 0 on both, and prints a
  different number: test regression RMSE `0.660285` on the bootstrap against
  `0.659657` self-hosted, reproducible across runs. That is a disagreement in
  the fourth decimal, not the 1-ULP float noise the documentation had been
  describing, and it is the kind that announces nothing.
- **`examples/save_load.tw`** reloads a model with `load` and the self-hosted
  `gbm_predict` refuses it: `first argument must be a model from gbm_fit`. A
  refusal, so it announces itself.
- **`examples/frames.tw`** is not a semantic divergence at all: the self-hosted
  CLI resolves the program's relative data path against the CLI's own directory,
  so it looks for `src/prices.csv` and reports that it cannot open it. A path
  bug in the CLI.

`src/cli/main.tw`, the decorated CLI, has a defect of its own that
`src/main.tw` does not: a systems-mode program's `main()` is never called.
`mode systems / fn main() { print("from main") }` prints nothing and **exits
zero** through `src/cli/main.tw`, and prints `from main` through `src/main.tw`.
That is the exact program `internal/interp/selfhost_test.go`'s
`TestSelfHostedRunsSystemsModeMain` asserts, and the test passes, because the
test drives `src/main.tw`. `CONTRIBUTING.md` had been telling readers to use
`src/cli/main.tw`.

### Nothing runs the comparison at corpus scale

`internal/interp/selfhost_test.go` and `internal/interp/selfhost_run_test.go`
are differential tests over `check` and over `run`, and CI does run them under
`go test ./...`. They are hand-written programs and a directory of fixtures, not
a corpus, so a divergence that only a real program reaches is not in their
range: none of the three `examples/` divergences above is.

`tools/diff/run` is the corpus-scale harness, and two things are true of it.
It is referenced by neither the `Makefile` nor `.github/workflows/ci.yml`, so
nothing ever runs it. And it compares two *Go* binaries (`-old` against `-new`,
or `-bin` against recorded goldens), so even if it were wired up it would not
look at `src/` at all. `docs/self-hosting.md` milestone 1 asks for a
`tools/diff/` mode that runs both lexers over the corpus and requires identical
token streams "as a required CI check", and its "Done means" line names
`tools/diff/run --lexers --corpus testdata/`. There is no `--lexers` flag in
`tools/diff/run`. That mode does not exist, and neither does any wider one over
`run`.

### How to read the cost lines in this document

Where an entry prices a feature as work in two implementations, that price is
honest for the lexer, the parser, the checker and the formatter, which do agree
across the corpus. It is not honest for the evaluator. A systems-mode builtin
added to the Go interpreter and to `src/builtins.tw`'s `NAMES` checks clean on
both sides and runs on one, which is the failure mode `src/builtins.tw`'s own
header comment says keeping a single list makes impossible. Keeping one list
makes the two *tables* agree; it says nothing about the dispatch behind them.
`internal/checker/builtintable_test.go` now holds the two tables to the same
names, which is the part a test can hold.

---

## The ranking

| # | Feature | Callers | Repos | Delivered |
|---|---|---|---|---|
| 1 | `Res[T, E]`, `Opt[T]`, or any way to return two values | 6 | twill, spool, loom, bobbin, weft, warp | 1.3.0, checked in 1.6.0; tuple returns unreleased |
| 2 | Function values with a declared type, as parameters and struct fields | 6 | twill, spool, loom, bobbin, weft, warp | 1.5.0 spelled, 1.7.0 as values |
| 3 | `enum` with payloads and exhaustive `match` | 5 | twill, spool, loom, bobbin, warp | 1.3.0, exhaustive 1.6.0, patterns 1.7.0 |
| 4 | The bitwise operators, spelled, and `shr` on a negative `I64` defined | 5 | twill, spool, loom, weft, warp | 1.3.0, infix in 1.5.0 |
| 5 | A sort, or a comparison-function parameter | 5 | twill, spool, loom, bobbin, weft | 1.9.0 |
| 6 | Writing files, directories, and `stat` | 5 | twill, spool, bobbin, weft, warp | 1.5.0, completed in 1.6.0 |
| 7 | A test runner | 5 | twill, loom, bobbin, weft, warp | 1.5.0 |
| 8 | `F64` as a first-class systems-mode type | 4 | twill, loom, weft, warp | 1.6.0, both checkers by 1.6.7 |
| 9 | Nested and generic containers | 4 | twill, spool, loom, warp | 1.3.0, user-defined generics 1.7.0 |
| 10 | `continue` and `break` | 4 | twill, spool, loom, bobbin | 1.4.0 |
| 11 | A monotonic clock | 4 | twill, loom, bobbin, weft | 1.6.0 |
| 12 | Reference semantics for `struct` and `Arr` parameters, stated | 3 | twill, spool, loom | written down, `docs/language-guide.md` |
| 13 | `Str` concatenation, and a way to build one that is not quadratic | 3 | twill, spool, weft | 1.4.0 |
| 14 | `src/term/` reachable from an installed package | 3 | loom, bobbin, weft | 1.5.0, as `std/term` |
| 15 | The float math builtins in systems mode | 3 | twill, weft, warp | 1.4.0 |
| 16 | Number formatting: `str(I64)`, padding, fixed-point | 3 | twill, loom, bobbin | 1.6.0, padding in `std/text` |
| 17 | A tensor that crosses the systems and numeric seam | 3 | twill, loom, warp | **half**: nameable, judges nothing |
| 18 | A seeded generator that is a value rather than a global | 3 | twill, loom, warp | 1.5.1.1 |
| 19 | Number parsing: `parse_i64`, `parse_f64` | 2 | twill, warp | 1.4.0, exact `i64_of_str` in 1.6.0 |
| 20 | `chr(I64) -> Str` | 2 | twill, weft | 1.4.0 |
| 21 | A process interface, or an HTTPS client | 2 | spool, warp | 1.8.0, process half only |
| 22 | `Bool` as a name that can be written in an annotation | 2 | twill, spool | 1.6.0 |
| 23 | `Bytes` distinct from `Str` | 2 | twill, warp | 1.4.0 the builtins, 1.6.0 the checked type |
| 24 | Iteration that does not materialise | 2 | twill, warp | **open** |
| 25 | A way to fail that cannot be ignored | 1 | twill | 1.4.0, `abort` |
| 26 | Allocation and memory counters | 1 | bobbin | 1.6.0 |
| 27 | Ranged reads | 1 | warp | 1.6.0 |
| 28 | Immutable top-level bindings | 1 | weft | **half**: `const` in one file, cross-file open |
| 29 | Optional and named arguments, or record update | 1 | weft | **open** |
| 30 | A compiler barrier | 1 | bobbin | unreleased, `black_box` |
| 31 | `Dict` keyed by something other than `Str` | 1 | twill | **half**: `I64` keys, not identity |
| 32 | An empty record, and removing a field | 1 | twill | **open** |

---

### 1. `Res[T, E]`, `Opt[T]`, or any way to return two values

> **Delivered, 1.3.0, and checked from 1.6.0.** `Opt[T]` and `Res[T, E]` are
> built in, `?` propagates a failure, and `match` reads either. `?` outside a
> function or in a function that does not return one of them is a checker error
> rather than a runtime surprise. The three workarounds below are no longer
> forced; each satellite carries its own until it adopts the replacement.
>
> **The third workaround is answered separately, by tuple returns (unreleased).**
> `Res` addressed failure, and a struct declared for one call site is not a
> failure: `fn span(xs) -> (F64, F64)` and `let (lo, hi) = span(xs)` are what
> `Batch`, `StepResult` and weft's four span types were standing in for. A tuple
> holds two to eight values, is destructured or passed on whole, and has no `.0`
> and no name -- a value that wants to be stored and read by name stays a
> struct, which is the distinction the workaround was blurring. See
> `docs/language-guide.md`, "Tuples".

**Six callers.** twill `docs/needs.md` NEEDS-10 (`src/lex.tw:294`, `tokenize`)
and NEEDS-22 (every environment lookup in `src/check.tw`). spool entry 10
(`Manifest`, `Lock`, `Resolution`, `Doc`, plus `toml.unquote`,
`manifest.remove_dep`, `vendor.git`, `vendor.commit_for`). loom entry 4
(`src/trainer.tw` `fit`, `src/checkpoint.tw` `restore`, `src/callback.tw`
`validate` and `unpack_state`, `src/data.tw` `take` and `split`). bobbin entry
10 (`src/baseline.tw` `find`, `src/suite.tw` `validate`). weft entry 13
(`src/scale.tw` `Span`, `src/heatmap.tw` `Range`). warp entry 6, which asks for
`parse_i64` and `parse_f64` to return `Res` specifically.

**Today.** Three separate workarounds, all in production at once.

The common one: a fallible function returns a `Str` that is empty on success.
loom `src/checkpoint.tw:118` names it as following spool's convention. bobbin
`src/suite.tw` `validate` does the same. warp `src/datasets.tw:131` returns an
empty string when every file verifies. Four codebases converged on the same
convention independently, which is evidence for the feature and not for the
convention.

The bad one: spool encodes a status flag in the first byte of the returned
string. `" "` means success with the value following, `"!"` means failure with
the message following. `git_ok` and `git_out` exist only to make that
survivable.

The third: a function that wants to return two values declares a struct for that
one call site. loom `src/data.tw` `Batch` and `src/state.tw` `StepResult` are
both tuples with a name. weft has four of them for the same reason, in a library
with eleven real type names.

**Cost.** Nothing forces a caller to read the error. That is the entire cost and
it is unbounded: every call site is a place a failure can be dropped, and the
compiler is silent about all of them. The struct-per-call-site workaround costs
type names rather than correctness, and it is what makes `weft`'s type list
twice as long as its concept list.

### 2. Function values with a declared type, as parameters and struct fields

> **Delivered.** The type is spellable from 1.5.0, as `fn(A, B) -> C` and
> `Fn(A, B) -> C`, wherever an annotation may appear, including a struct field.
> Function values themselves landed in 1.7.0, which is what made entry 5's
> comparison expressible: `sort(items, fn(p, q) = p.n < q.n)` runs today.

**Six callers.** loom entry 3 is the clearest statement: `src/trainer.tw` `fit`
takes `step` and `eval_batch`, `predict` takes `forward`, `default_step` takes
`loss_fn`. bobbin entry 5: `src/harness.tw` `run`, `batch` and `auto_inner` all
take `body: fn(I64) -> F64`, and `src/suite.tw` `Case.body` is a stored
function. warp entry 1: `src/pipeline.tw` `Stage.fn_map`, `Stage.fn_keep`,
`Source.get`, and `src/datasets.tw` `idx_source` and `csv_source`, where
`Source.get` closes over the loaded buffer so closures are needed and not only
function pointers. twill NEEDS-26 asks the adjacent question for `src/eval.tw`:
whether a captured variable is captured by handle or by value. spool entry 12
and weft entry 8 both reach for it in the narrower form of a comparator
parameter.

**Today.** bobbin's `src/suite.tw` is unwritable and is written anyway, as a
specification of a type it cannot express. warp would have to become a fixed set
of built-in transforms with no way to add one. loom would have to hide the
update rule inside the trainer, which is the thing loom exists not to be.

**Cost.** For bobbin and warp this is not a workaround with a cost, it is a
different and worse library. A benchmark harness that cannot be handed code
benchmarks nothing. A pipeline that cannot be handed a transform is a fixed
list. Neither has a workaround at all, which is why both filed it as blocking
rather than painful.

This entry is also the prerequisite for entry 5. Every hand-written sort in the
ecosystem exists because a comparator cannot be passed.

### 3. `enum` with payloads and exhaustive `match`

> **Delivered.** `enum` with a payload and `match` landed in 1.3.0,
> exhaustiveness in 1.6.0, and 1.7.0 turned a pattern into a tree: nested
> patterns, literal patterns and guards. A payload is one value per variant, so
> a variant carrying two things carries a struct.

**Five callers.** twill NEEDS-3 (`src/ast.tw` is forty variants; `src/lex.tw:29`
spells token kinds as `I64` constants and the `kind_name` ladder below it is the
cost) and NEEDS-70 (`Op` has forty-odd payload-free cases). spool entry 9
(`src/toml.tw` `Pair`, `src/semver.tw` `Constraint`). loom entry 5
(`src/callback.tw` `Callback.kind` and `Callback.sched`, `src/state.tw`
`OptState.kind`). bobbin entry 11 (`src/baseline.tw` `verdict_name` and
`compare`, `src/report.tw` `human_comparison`). warp assumes it in its baseline
rather than filing an entry, which is why warp's blocking list is shorter than
it should be.

**Today.** Verified by reading the source, not the needs files. Six discriminants
are `I64` constants dispatched by if-chain:

- `bobbin/src/baseline.tw:60-65`, six `VERDICT_*` constants, two if-chains over
  them in two different files.
- `loom/src/callback.tw:101-105`, five `KIND_*` constants.
- `loom/src/callback.tw:108-112`, five `SCHED_*` constants.
- `loom/src/state.tw:117-119`, three `OPT_*` constants.
- `spool/src/semver.tw:31-33`, three `CONSTRAINT_*` constants.
- twill `src/lex.tw:29`, the token kinds.

There is a seventh: `loom/src/callback.tw:82-88`, seven `HOOK_*` constants, with
`fire` dispatching on them.

spool's `src/toml.tw` uses a different shape for the same thing: a `Pair` struct
holding both a `value: Str` and a `fields: Dict[Str, Str]`, with an
`is_table: Bool` saying which one is real.

**Cost.** Adding a variant compiles and silently does nothing. Nothing forces
the if-chain to grow an arm. bobbin says a seventh verdict falls through to
"missing" in one place and to the default marker in the other, which are two
different wrong answers to the same addition. loom's is worse because a callback
framework is extended by people who did not write it, and because the flat
struct carries fields that some variants never read: a checkpoint callback has
`patience` and `min_delta` and ignores them, and nothing stops a caller setting
them and expecting an effect.

spool's two-field `Pair` has the mirror cost: nothing stops a caller reading the
field that is not real.

This is the highest-ranked entry where the workaround is silently wrong rather
than merely ugly. See "Where the workaround is silently wrong".

### 4. The bitwise operators, spelled, and `shr` on a negative `I64` defined

> **Delivered, and the sign question is answered.** The operators landed as
> calls in 1.3.0 and infix in 1.5.0: `band`, `bor`, `xor`, `shl`, `shr`, `bnot`.
> **`shr` is arithmetic**, so `shr(-16, 2)` is `-4`, and
> `docs/language-guide.md` says so and gives the `ushr` idiom for the logical
> one. That settles the contradiction recorded at the end of this document: loom
> assumed logical and this repository assumed arithmetic, and arithmetic won.

**Five callers.** twill NEEDS-2 (`src/lex.tw:131` and `src/lex.tw:498` mask
lead bytes) and NEEDS-85 (`src/float.tw` `ushr`, `udiv10`, `unonzero`). spool
entry 6 (`src/sha256.tw`, `src/strutil.tw`). loom entry 7 (`src/rng.tw` `mix`,
`epoch_seed`, `derive`). weft entry 6 (`src/canvas.tw` `set_bit`, `bit_set`).
warp entry 7 (`src/rng.tw` `mix`, `next`).

`docs/self-hosting.md` section 1.2 lists "bitwise `and or xor shl shr not` on
I64" and stops there. It does not say whether they are infix operators or
builtin calls, how they bind against arithmetic and comparison, whether they
share a spelling with the boolean operators, or what `shr` does with the sign
bit.

**Today.** Five codebases guessed, and they did not all guess the same way.
spool assumes infix with a prefix `not`, and parenthesises every subexpression
in `src/sha256.tw` rather than relying on an undecided precedence. loom writes
them as builtin calls, `xor(a, b)` and `shr(z, 30)`, on the grounds that `and`
and `or` are already the logical operators. weft avoids them entirely: it sets a
bit with `mask + pow2(bit)` guarded by a test and reads one with
`(mask / pow2(bit)) % 2 == 1`, which is a loop and a divide where the hardware
has one instruction, on the hottest path in the library. warp masks everything
to 32 bits after every step so it never shifts a negative value, throwing away
half the width of the type on the hottest path in the loader. twill's
`src/float.tw` assumes the opposite of loom and carries three helpers to work
around it.

**Cost.** The measured cost is weft's divide-and-modulo bit test and warp's
32-bit masking, both on hot paths, both paid for an unwritten rule. The unmeasured
cost is that loom and twill assumed opposite answers to the same question. See
"Where the workaround is silently wrong": this is the strongest single result
the exercise produced.

Cheap to fix. Writing the answer down costs one paragraph in
`docs/language-guide.md`. Deciding the answer is the work.

### 5. A sort, or a comparison-function parameter

> **Delivered, 1.9.0.** `sort` orders numbers as well as strings and takes a comparison
> as its second argument: `sort(xs, fn(a, b) = a.n < b.n)`. Every form is stable.
> The comparison takes two elements rather than a key, which is what skein's
> index sort needs, and it only became expressible once function values landed
> (item 2). Everything below is what this entry said while it was open, and the
> eleven hand-written sorts it counts are still in those repositories until each
> one adopts the builtin.

**Five callers.** spool entry 12, loom entry 11, bobbin entry 9, weft entry 8,
and twill, which does not file an entry and has three of them anyway.

**Today.** Eleven hand-written insertion sorts, counted in the source:

- `spool/src/strutil.tw:301` `sort_strs`
- `spool/src/manifest.tw:100` `sort_deps`
- `spool/src/lockfile.tw:58` `sort_entries`
- `spool/src/resolve.tw:230` `sort_versions`
- `loom/src/callback.tw:268` `ordered`, by `(order, index)`
- `bobbin/src/stats.tw:37` `sorted`
- `bobbin/src/baseline.tw:143` `put`, an insert into a sorted array
- `weft/src/bars.tw:184` `sorted_copy`
- twill `src/check.tw:254` `sort_strings`
- twill `src/fmt.tw:149` `sort_strings`, a second copy in the same repository
- twill `src/tensor.tw:1352` `sort_offsets`

bobbin's needs file says seven. That is spool's four plus loom's one plus its own
two, and it omits weft's and twill's three because bobbin could not see them.
The real number is eleven, and two of them are in the same repository under the
same name.

**Cost.** Three costs, and they are different from each other.

Correctness surface: spool's lockfile is reproducible only if four separate
sorts all order identically, and `src/strutil.tw:269` says exactly this, that
every sort must go through one comparison function so a lockfile sorted on two
machines is the same file. Four implementations of the ordering is four places
for that to break.

Speed: bobbin `src/stats.tw` is the hot path of the whole tool. Every summary
sorts its samples, and an insertion sort over a thousand samples is a million
comparisons in an interpreter. bobbin names a builtin sort as the single largest
speedup available to it. weft's is quadratic over the caller's entire sample and
is acceptable only because a histogram is drawn once rather than per frame,
which weft correctly notes is not a property to rely on.

Volume: eleven copies of eleven lines is a hundred and twenty lines of code that
one `sort_by` deletes.

### 6. Writing files, directories, and `stat`

> **Delivered, 1.5.0 and 1.6.0.** `write_file`, `write_text_or` and one path
> resolution rule landed in 1.5.0; `path_exists`, `path_is_dir`, `mkdir_all`,
> `list_dir`, `remove_file`, `remove_dir`, `remove_all`, `rename`, `mtime`,
> `file_size`, `temp_dir` and `cwd` by 1.6.0. `list_dir` answers a `Res`, so a
> directory that cannot be read is a value the caller has to read rather than an
> empty list.

**Five callers.** spool entries 2 and 3 (`src/commands.tw` writes the lockfile,
the manifest and the vendor README; `src/vendor.tw` needs `list_dir`, `is_dir`,
`path_exists`, `mkdir_all`, `remove_all`). bobbin entry 4 (`src/report.tw`
`render_baseline` produces the text and has nowhere to put it). weft entry 5
(`examples/loss.tw`, and the SVG export). warp entry 4 (`src/cache.tw`,
`src/datasets.tw` `verify`, `src/stream.tw`), which needs `rename` to be atomic
within a directory and `mtime` for `cache.is_fresh`. twill NEEDS-28, NEEDS-59,
NEEDS-91 and NEEDS-92.

**Today.** `read_file` is in milestone 1 and `write_file` is not, so parsing a
stored baseline is writable and writing one is not. bobbin calls that a strange
half and it is. twill's `std/io.tw` `exists` is the worst of it: with no `stat`,
it tries to read the whole file, and if that fails it lists the whole parent
directory looking for the base name.

**Cost.** spool cannot write a lockfile, which makes it a linter. bobbin cannot
store a baseline, which makes every comparison a comparison against nothing.
warp's cache cannot rename atomically, so an entry half written when a run is
interrupted claims by its key to be complete and every later run reads it.
twill's `exists` reads a gigabyte to answer a yes-or-no question about a
gigabyte file, and reports differently for a file that exists but cannot be read
depending on which branch answers.

### 7. A test runner

**Delivered, 1.5.0 (written 2026-08-11).** `twill test [path ...]` discovers every
`*_test.tw` under the given paths, runs each, and reports pass/fail with a
summary, exiting non-zero on any failure -- so a new suite is in the run the
moment it exists, with no CI list to maintain (`cmd/twill/test.go`). The shared
runner replaces the per-file shell loop; the harness the suites already use is
unchanged. What remains for the other repositories is to depend on it rather than
carry their own copy.

**Five callers.** loom entry 15, bobbin entry 8, weft entry 12, warp entry 13,
and this repository, which has `std/tests/harness.tw` for the same reason. spool
is cited by weft and warp as having recorded it and did not; see
"Contradictions in the sources".

**Today.** Five copies of a hand-rolled harness, one per repository, each a
counter with an `equal` family and a `report` that calls `exit(1)`. Their line
counts are 69, 68, 65, 65 and 50. Every test file is a program that has to be
run individually, and there is no way to run a suite except a shell loop.

**Cost.** A new test file is invisible to CI until someone adds it to the
workflow by hand, which is a failure mode with no symptom: the test passes
because it never ran. loom's CI workflow has no loop at all, because there is
nothing yet to loop over.

The duplication itself is the smaller cost and is often overstated. The five
files are not byte-identical and have already drifted: loom's has a `near` with
a caller-supplied tolerance that spool's does not, and spool's imports
`../src/strutil.tw` while the others do not. Drift between five copies of a
test harness means two repositories can disagree about what a float comparison
means, which is exactly the divergence a shared runner prevents.

### 8. `F64` as a first-class systems-mode type

> **Delivered, 1.6.0, on both checkers by 1.6.7.** `F64` is one of the checked
> systems-mode types, with the four operations, comparison, `f64_mod`, and
> `f64()` and `i64()` in both directions. On the performance half of loom entry
> 9: 1.3.0 stopped a number that needs no gradient from being a heap-allocated
> tensor, which is the allocation that question was about.

**Four callers.** weft entry 1 (`src/scale.tw` is F64 arithmetic from top to
bottom; every source file). warp entry 2 (`src/sample.tw`, `src/augment.tw`,
`src/strutil.tw`). loom entry 9 (`src/metrics.tw` `update` and `count`,
`src/report.tw` `fixed`, `src/callback.tw` `lr_at` and `pack_state`). twill
NEEDS-40 (`src/cli/banner.tw` and `src/cli/tensor.tw`). bobbin depends on it in
`src/harness.tw`'s `body: fn(I64) -> F64` and in `src/stats.tw` without filing
an entry.

`docs/self-hosting.md` names `F64` once, as an enum payload in a token example,
and specifies nothing else. Section 1.2 is about `I64`.

**Today.** Every one of these files writes `F64` in an annotation that the
document does not authorise, and assumes the four operations, comparison, `%`,
and `f64()` and `i64()` in both directions.

**Cost.** weft states the diagnosis best: the systems subset was designed around
a compiler, where integers are the whole job, and the one numeric type a plot
needs is the one the subset does not describe. warp says the same from the other
end: a data loader's payload is floats.

The unpriced risk is loom entry 9's second half. If the answer turns out to be
that a systems-mode scalar is still a rank-0 tensor, then `Meter.total` is a
tensor, every accumulation allocates, and an epoch of accumulation builds a
chain of them. That is a performance answer worth knowing before the interpreter
finds out.

### 9. Nested and generic containers

> **Delivered, 1.3.0 and 1.7.0.** Generic annotations parse and check from
> 1.3.0, and 1.7.0 added user-declared generics: `struct Box[T]`, `enum Tree[T]`
> and `fn first[T](xs: Arr[T]) -> Opt[T]`. `Arr[Arr[I64]]` nests and indexes.
> There is no monomorphization and none is needed, since the runtime is the same
> code whatever `T` is.

**Four callers.** twill NEEDS-72 (`Arr[Arr[I64]]` in `Odometer.contrib` and
`einsum_plan`, `Arr[Tensor]` in `concat`, `split` and `backward`, `Arr[Bool]` in
`resolve_perm`) and NEEDS-90 (`std/json.tw`, where `Json` contains `Arr[Json]`).
spool entry 11 (`src/resolve.tw` `Catalog`). loom entries 12 and 13
(`src/metrics.tw` `MeterSet`, `src/trainer.tw` `predict`). warp entry 12
(`Arr[Stage]`, `Arr[File]`, `Arr[smp.Batch]`).

**Today.** spool's `Catalog` is `Dict[Str, Str]` where the values are
comma-separated version lists and whole rendered manifests, re-parsed on every
read. loom's `MeterSet` is two parallel `Dict[Str, F64]` plus an `Arr[Str]` of
names, where it wants one `Dict[Str, Meter]`.

**Cost.** spool re-parses a manifest on every catalog read, which turns a lookup
into a parse. loom's parallel dicts can go out of step and only a convention
keeps them together. loom's `predict` has a worse fallback: if `Arr` cannot hold
tensors it must concatenate as it goes, which is quadratic in the number of
batches and allocates the whole output once per batch.

twill's is the sharpest: if `Arr[T]` cannot nest, the tensor kernels need a
hand-rolled flattening for each of five uses, which is five chances to get an
index wrong in the code that computes gradients.

### 10. `continue` and `break`

> **Delivered, 1.4.0.** Both work in `while` and `for`, and the checker enforces
> the scoping rules rather than leaving a stray `break` to the interpreter. They
> are systems-mode keywords, so a numeric-mode program that uses `break` as a
> name is unaffected.

**Four callers.** twill NEEDS-12 (`src/lex.tw:305` and the whole scanner loop).
spool entry 8 (`src/toml.tw`, `src/manifest.tw`, `src/lockfile.tw`,
`src/resolve.tw`, `src/commands.tw`). loom entry 14 (`src/callback.tw` `fire_*`,
`src/metrics.tw` `has`). bobbin entry 6 (`src/harness.tw`, the sampling loop).

**Today.** Every parser grows a level of nesting per line shape. twill's lexer
would nest eight deep rewritten as nested `else`. bobbin uses a `done` flag for
a loop with four exit conditions.

**Cost.** Readability, which is the thing `docs/self-hosting.md` says the port
is buying. bobbin's is honest about the limit: the `done` flag reads acceptably
in a loop with nothing after the check and would not in a loop with real work
there.

Cheap. This is a parser and an interpreter change with no design question in it,
and it is the highest-ranked entry that is purely mechanical.

### 11. A monotonic clock

> **Delivered, 1.6.0.** `mono_ns()` returns nanoseconds as an `I64` from a clock
> that only goes forward, which is the shape this entry argues for in its last
> line: milliseconds are a divide away, and the reverse is not true.
> `clock_now_ms` remains for the wall time a log line wants.

**Four callers.** bobbin entry 1 (`mono_ns`, `src/clock.tw`, and therefore every
timing in the repository). twill NEEDS-39 (`now_ms`, `src/term/frame.tw`,
`src/cli/spinner.tw`, `src/cli/progress.tw`). weft entry 4 (`now_ms`,
`src/live.tw` `push`). loom entry 16 (`src/report.tw`).

**Today.** bobbin measures nothing. weft passes the time in from the caller,
which is the right shape for a test and the wrong shape for a training loop that
has no reason to know what time it is. loom prints a progress fill with no
estimate. twill's CLI threads the time as a parameter to every function that
needs it, deliberately, so the renderers stay pure, which means only the driver
needs this.

**Cost.** bobbin's requirements are on the runtime and not preferences, and each
one has a failure mode. A wall clock corrected mid-run produces a negative
duration, and a negative duration moves the median in the direction that looks
like an improvement. A millisecond clock measures a microsecond operation as
zero, and a harness given zeros reports a median of zero and an IQR of zero,
which reads as a perfectly stable, infinitely fast operation. An `F64` loses
nanosecond resolution after about 104 days of uptime, silently, and the loss
looks like quantisation.

For loom the cost is smaller and real: the useful part of a progress bar for a
400-epoch run is the time remaining, not the fill. For weft a wall clock that
steps backwards over an NTP correction makes the repaint limiter refuse to paint
for as long as the step.

Satisfy once, in nanoseconds and `I64`. Milliseconds are a divide away from
nanoseconds; the reverse is not true.

### 12. Reference semantics for `struct` and `Arr` parameters, stated

> **Delivered: the sentence exists.** `docs/language-guide.md`, under "`struct`,
> and what a parameter is", states the rule the three codebases were guessing
> at: a `struct` has reference semantics, passing one passes a handle, assigning
> to a field of a parameter mutates the caller's struct, and the same holds for
> `Arr`, `Dict` and `Bytes`. It also states the exception that makes the rule
> safe: `Record` is a different type and is not unified with `struct`, because
> `grad` walks a record's structure and depends on records not aliasing.

**Three callers.** twill NEEDS-5, NEEDS-42, NEEDS-67, NEEDS-71 and NEEDS-82.
spool entry 14. loom entry 10.

`docs/self-hosting.md` section 1.2 says structs have reference semantics. It
does not say what happens when a function assigns to a field of a parameter, and
it says nothing at all about `Arr`.

**Today.** Every codebase assumes the reference answer and none of them can
point at the rule. twill's CLI needs the mutation visible through a field of
another struct, since a `Spinner` holds a `Frame` and `step` mutates through it,
which a lexer never exercises.

**Cost.** If the answer is by-value, loom's `fit` cannot advance the run it was
given, no callback can record anything, and every function in `src/metrics.tw`
has to return a new meter. twill NEEDS-71 is worse: `accumulate` mutates
`cot[node].data` and expects the caller to see it, so if an `Arr` parameter is
copied the whole backward pass returns zeros.

Nobody is asking for a feature here. They are asking for a sentence. The cost of
not writing it is that three codebases are built on an assumption and the
assumption is only checked when the interpreter exists.

### 13. `Str` concatenation, and a way to build one that is not quadratic

> **Delivered, 1.4.0.** `a + b` joins two strings, and the non-quadratic half is
> there too: `bytes_new`, `bytes_push` and `bytes_to_str` build a string by
> appending, and `buf_new`, `buf_get8`, `buf_set8` and `buf_len` are the fixed
> buffer underneath. The smaller gap in this entry's last paragraph is closed as
> well: `chr(I64) -> Str` turns a byte back into a one-byte string, so spool's
> nibble table is no longer the only way.

**Three callers.** twill NEEDS-35, NEEDS-7 and NEEDS-99. spool entry 5. weft
entry 7. Every other repository uses `+` on strings without filing an entry,
because they assumed it works.

**Today.** `docs/self-hosting.md` gives `Bytes` a `concat` and gives `Str`
length, indexing and slicing, and never says how two `Str` values are joined.
spool assumes `+` and calls it the single most-used operation in its source.

`src/bytes.tw` in this repository already wraps the right surface. `src/term/`
and `src/cli/` were written before it existed and build strings by concatenation
anyway.

**Cost.** weft prices it: a frame of a live plot is built by repeated
`out = out + piece` across a few hundred pieces, and at 30 repaints a second for
six hours that is the difference between a plot and a plot that is also a
benchmark. twill NEEDS-35 has the same problem in `src/term/width.tw` `repeat`
and `src/cli/progress.tw` `bar`, which build a string a cell at a time.

There is a related smaller gap. spool has no way to turn a byte value back into
a one-byte `Str` and works around it with a sixteen-entry `HEX` table indexed by
nibble, which is fine for hex and would not be fine for anything else. twill
NEEDS-99 is the same hole seen from `std/frame.tw`: `one_hot` cannot build the
column name `colour_0`, so it takes the output names as an argument and the
caller writes thirty string literals by hand, each one a chance to get the order
wrong and produce a frame whose column names disagree with its contents.

### 14. `src/term/` reachable from an installed package

> **Delivered, 1.5.0, and by the route this entry asks for.** The import rule
> was not widened. `src/term/` was promoted into the standard library as
> `std/term`, embedded in the binary alongside every other `std` module, so
> `import "std/term/color"` works from any directory with nothing vendored:
> `ansi`, `box`, `caps`, `color`, `frame`, `theme` and `width`. weft's
> `../twill_modules/...` path and the two capability ladders are no longer
> needed by anything.

**Three callers.** loom entry 8 (`src/report.tw`), bobbin entry 7
(`src/report.tw`), weft entry 11 (`src/canvas.tw`, `src/chart.tw`,
`src/theme.tw`, `src/live.tw`, and every test).

**Today.** twill resolves a non-`std/` import as a path relative to the
importing file, so only `std/` modules are reachable from an installed package.
`src/term/` is not one. loom and bobbin therefore have no colour and no
capability detection. weft does the other thing: it imports
`../twill_modules/twill/src/term/caps.tw` out of the copy spool vendors, which
works and hard-codes an internal directory layout twill has never promised.

**Cost.** bobbin marks a regression with `!!` and an improvement with `++`,
because two ASCII characters are the only emphasis available to it, and a
regression in a table of forty benchmarks is something a person finds by eye.
weft's alternative was to copy the capability ladder, and two implementations of
what `NO_COLOR` means is exactly the failure a shared file prevents.

All three rejected duplication for the same reason and reached three different
bad answers. The fix is not to widen the import rule. It is to promote
capability detection, the palette and the determinate bar into `std/term`.

### 15. The float math builtins in systems mode

> **Delivered, 1.4.0.** `f64_sqrt`, `f64_exp`, `f64_log`, `f64_sin`, `f64_cos`,
> `f64_tanh`, `f64_pow`, `f64_mod`, `f64_floor`, `f64_ceil`, `f64_round` and
> `f64_trunc` are systems-mode builtins, and they are the one implementation
> this entry insists on rather than a second copy. weft's infinity guess has a
> real answer now: `f64_bits` and `all_finite` read the representation instead
> of comparing against `1.0e308`.

**Three callers.** twill NEEDS-68 (`f64_exp`, `f64_log`, `f64_sin`, `f64_cos`,
`f64_sqrt`, `f64_tanh`, for `src/tensor.tw`) and NEEDS-40 (`cos` for
`src/cli/banner.tw`). weft entry 2 (`log` and `exp` for `src/chart.tw`'s log
axis, `cbrt` and `sturges` in `src/bars.tw`, NaN and infinity detection in
`src/fmtnum.tw`). warp entry 3 (`sqrt`, `log`, `cos` for Box-Muller in
`src/augment.tw`).

**Today.** These exist in numeric mode as differentiable tensor operations.
Whether they exist on a systems-mode `F64` is unspecified. weft detects NaN with
`v != v`, which is correct, and infinity by comparing against `1.0e308`, which
is a guess about the representation.

**Cost.** warp has no workaround: Box-Muller needs those three and Gaussian
noise is not optional in an augmentation library. twill's requirement is
stricter than the others and drives the implementation: `testdata/` compares
output byte for byte, so an `exp` that is one ulp off turns every test touching
a sigmoid into a diff. Whatever supplies these has to be one implementation, not
three.

weft's infinity guess is the silently-wrong part. A NaN loss is the most
important event in a training run and weft goes to trouble to keep it visible,
so detecting one should not rest on a magic constant.

### 16. Number formatting: `str(I64)`, padding, fixed-point

> **Delivered, 1.6.0 for the primitive and `std` for the rest.** `I64` became a
> real 64-bit integer in 1.6.0, so `str(42)` is `42` and not `42.0`, which is
> what NEEDS-45 was about: no trailing `.0` in a line number. Padding and
> fixed-point are library functions rather than builtins:
> `std/text.pad_left`/`pad_right`, `std/term/width` for the display-width
> version, and `std/float.f64_fixed(x, prec)`. `num_to_text` and `f64_to_str`
> are the primitives underneath.

**Three callers.** twill NEEDS-45 and NEEDS-20. bobbin entry 12
(`src/report.tw` `pad_left` and `pad_right`, `src/clock.tw` `fixed` and
`pad_zero`). loom `src/report.tw` `fixed`, which is bobbin's function copied.

**Today.** Four hand-rolled formatting helpers in bobbin, one of which
reimplements fixed-point decimal output character by character, and one of them
duplicated into loom.

**Cost.** Column alignment is not a nicety in a benchmark table: varying width
is what hides the differences the table exists to show. twill NEEDS-45 is the
sharper version, because `str` on a scalar currently goes through the tensor
printer and a trailing `.0` would land in every line number, every column count
and every axis index in every diagnostic.

### 17. A tensor that crosses the systems and numeric seam

> **Half delivered, and the missing half is the one that matters.** `Tensor` is
> a name a systems-mode annotation may write, and the checker accepts it. It
> resolves to unknown (`internal/checker/systems.go`, the `case "Tensor"` arm),
> deliberately: the name says only that the value is a tensor, and the shape
> checker's own annotations are where a shape is said. So a tensor crosses the
> seam **untyped**, and warp's "the shape check comes free from the existing
> checker" does not follow yet. The design question this entry calls the largest
> on warp's list is still open.

**Three callers.** warp entry 11 (`src/sample.tw`, the whole file). loom entry 2,
which is the same question wearing a different hat: loom needs a name for "a
tensor, or a list or record nesting tensors" and writes `Tree` everywhere.
twill NEEDS-41 (`src/cli/tensor.tw` and the REPL need a read-only view).

**Today.** warp's sample is a flat `Arr[F64]` plus an `Arr[I64]` shape, which is
a tensor written out longhand. loom writes `Tree` in an annotation for a type
that has no name in the type language, so every function in `src/trainer.tw`,
`src/state.tw` and `src/checkpoint.tw` is unwritable as specified.

**Cost.** warp's consumers rebuild the tensor at the boundary, and the shape can
disagree with the buffer because nothing checks it. If a tensor can cross the
seam, `sample.tw` collapses to two fields and the shape check comes free from
the existing checker.

warp names this the largest design question on its list and it is right. `mode
systems` was defined by what a compiler needs, and a data loader is the first
program that wants both halves at once. loom is the second.

### 18. A seeded generator that is a value rather than a global

> **Delivered, 1.5.1.1.** `rng_open(seed)` returns a generator that is a value,
> read with `rng_u53`, `rng_f64`, `rng_norm` and `rng_perm` and released with
> `rng_close`. The global `seed(n)` still exists for a program that wants one
> stream. **The primitive is delivered and this repository has not adopted it**:
> `std/batch.tw` still calls `seed(s)` and `seed(s + c)` in
> `shuffled_indices` and `stratified_indices`, so the shared-stream cost this
> entry prices is still being paid by the library that filed it. That is
> adoption work, not language work.

**Three callers.** loom entry 6 (`src/rng.tw`, and therefore `src/trainer.tw`
and `src/checkpoint.tw`). twill NEEDS-55 and NEEDS-95 (`std/batch.tw`
`shuffled_indices`, `stratified_indices`, `stratified_kfold_indices`). warp,
which ships its own `src/rng.tw` rather than filing an entry.

**Today.** `seed(n)` sets a position and nothing reads it. loom reseeds at the
top of every epoch from `mix(base, epoch)` so that epoch 10 is identical whether
it is the tenth epoch of a run or the first after a resume. twill's
`std/batch.tw` calls the global `seed(s)` to honour a per-split seed argument.

**Cost.** loom's is stated and bounded: a run can only be checkpointed on an
epoch boundary. twill's is not bounded. A call to `train_test_split` moves the
one random stream the whole program shares, so splitting the data after
initialising the model gives different weights than splitting before it, for no
reason visible in the code. `stratified_indices` seeds once per class and so
consumes and resets the stream several times in one call.

loom names the deeper problem: with a global generator, an evaluation pass that
drew a single random number would shift every subsequent training batch, and the
only reason `src/trainer.tw` gets away with it is that its evaluation path draws
nothing. That is a property maintained by inspection.

### 19. Number parsing: `parse_i64`, `parse_f64`

> **Delivered, 1.4.0, exact in 1.6.0, under different names.** They are
> `i64_of_str(s) -> Opt[I64]` and `str_to_f64(s) -> Opt[F64]`, so the failure is
> in the return type rather than in a sentinel. `i64_of_str` returning an exact
> integer rather than a float was part of 1.6.0's `I64` work. The names are the
> reason this stayed unnoticed: a needs file looking for `parse_i64` finds
> nothing.

**Two callers.** warp entry 6 (`src/strutil.tw`, and through it every reader in
the library). twill NEEDS-18, NEEDS-19 and NEEDS-60.

**Today.** warp ships its own decimal and exponent parser, a hundred lines.

**Cost.** warp's note is the one worth keeping: it accumulates the fraction as
an integer and divides once, because adding `digit / 10^k` as it goes rounds at
every step and the error shows in the eighth digit, which is exactly where a
cached value gets compared against a freshly computed one and fails. Every
program reading a CSV will otherwise write this again and it will be subtly
different every time.

twill's constraint is tighter: `f64_of_str` must match Go's acceptance set
exactly, because a parser that accepts a superset turns a corrupt column into
silent numbers and one that accepts a subset rejects files the bootstrap reads.

### 20. `chr(I64) -> Str`

> **Delivered, 1.4.0.** `chr(65)` is `"A"`, a byte and not a codepoint, which is
> what both callers asked for. `std/term/ansi` emits real escape sequences
> through it.

**Two callers.** twill NEEDS-34 (`src/term/ansi.tw` `esc` and `bel`,
`src/cli/banner.tw` `braille`). weft entry 3 (`src/canvas.tw` `braille`).

Both need a byte and not a codepoint, because both hand-encode U+2800 braille as
three bytes and would be encoding an encoding otherwise. Twill string literals
recognise `\n`, `\t`, `\"` and `\\` and nothing else, so ESC cannot be written
at all.

**Cost.** weft's is concrete: without `chr` the entire braille canvas becomes a
256-entry lookup table of literals. twill's is that nothing in `src/term/` emits
an escape sequence.

`src/term/ansi.tw` already calls `chr(27)`, so this repository's own sources
assume it exists. It is not in the self-hosting builtin list.

### 21. A process interface, or an HTTPS client

> **The process interface is delivered, 1.8.0.** `run(program, argv, dir) -> Res[Str, Str]`
> exists, with the signature spool entry 1 asked for. It takes an argument vector
> and never a shell, it inherits the environment so that shelling out to git
> keeps borrowing the user's credentials, and `TWILL_NO_EXEC` turns it off --
> which is this entry's last paragraph answered rather than deferred. An HTTPS
> client is still not built, so warp's half of this entry stands: the choice
> between the two was settled in favour of the smaller surface, not deleted.
> Everything below is what the entry said while it was open.

**Two callers.** spool entry 1 (`src/vendor.tw`). warp entries 9 and 10
(`src/datasets.tw`).

**Today.** spool fetches nothing. warp prints the URL and the expected size and
asks the user to fetch the file, then asks them to gunzip it.

**Cost.** spool is a package manager that cannot fetch a package. warp's cost is
smaller and warp is the less unhappy of the two: a data-loading library that
silently downloads 170 megabytes surprises people, and the verification step is
worth more than the fetching step. But `warp.get("cifar-10")` cannot exist, and
the manual gunzip is an extra step in every getting-started guide forever.

Both argue for the process interface over sockets, on the grounds that it is the
smaller ask and that the self-hosted toolchain will want it for driving a linker
anyway. Shelling out to git borrows the user's existing credentials, proxies and
host keys, which is what lets a package manager avoid taking on a network stack.

This entry widens what running a `.tw` file can do more than anything else in
this document, and it should be a considered decision rather than a side effect
of wanting a package manager. Ranked here by caller count; it should be
scheduled by that sentence.

### 22. `Bool` as a name that can be written in an annotation

> **Delivered, 1.6.0.** `Bool` is one of the checked systems-mode type names, so
> `let flag: Bool = true` and `trailing: Bool` are what they look like. 1.6.2
> closed the numeric-mode half, where a type name written in an annotation was
> being read as an undeclared unit.

**Two callers.** twill NEEDS-14 (`src/lex.tw:61` annotates `trailing: Bool`).
spool entry 7 (a dozen places).

Section 1.2 names `I64`, `Byte`, `Bytes`, `Str`, `Arr`, `Dict`, `Opt` and `Res`
and never names `Bool`, while section 1.3 makes annotation mandatory. The
checker has `tBool` and there is no way to write it. The parser reads a bare
name after `:` as a record type or a unit, so `Bool` resolves as a unit and is
reported as undeclared.

**Cost.** None yet, because nothing runs. It is trivially blocking for both, and
it is the cheapest entry in this document.

### 23. `Bytes` distinct from `Str`

> **Delivered in two halves: the builtins in 1.4.0, the checked type in 1.6.0.**
> 1.4.0 gave `Bytes` a runtime identity of its own, `value.Bytes`, reached
> through `bytes_new`, `bytes_push` and `bytes_to_str`, with the fixed buffer
> (`buf_new`, `buf_get8`, `buf_set8`, `buf_len`) beside it. It did not give the
> checker one: the quoted type name `"Bytes"` -- the form a checker matches an
> annotation against, as distinct from the `value.Bytes` the runtime already had
> -- occurs in no Go file under `internal/` at either `v1.4.0` or `v1.5.0`, so an
> annotation could not name the type and no mismatch against it could be
> reported. `Bytes` became a checked type in 1.6.0,
> in `fe2966c` ("checker: give systems mode a real type system", first tagged
> `v1.6.0`), which is the commit that adds `tBytes` to the lattice and the
> `case "Bytes"` that reads it out of an annotation. This entry previously said
> "Delivered, 1.4.0. `Bytes` is its own checked type", which credited 1.4.0 with
> work 1.6.0 did.
>
> The conversion is a call, which is the point: the place where a file's
> contents are trusted as UTF-8 is written down in the program.

**Two callers.** twill NEEDS-7 (`src/bytes.tw`). warp entry 8
(`src/datasets.tw` `read_idx` and `be32`, `src/stream.tw`).

**Today.** warp indexes IDX files as a byte string, because `Str` in the subset
is bytes that print.

**Cost.** The type says "text" about data that is not. warp is right that it
matters at exactly one place, which is where a file is read and something has to
decide whether to trust it as UTF-8. twill's need is different and larger: the
whole of `src/bytes.tw` exists so that the compiler never builds a string by
repeated `+`.

### 24. Iteration that does not materialise

> **Open.** There is no iterator protocol. `for x in ...` takes a list or a
> 1-D tensor and is eager, and `std/batch.tw`'s `epoch_batches` still returns
> the whole epoch. The prerequisite this entry names is now met, since `Opt`
> arrived in 1.3.0, so the `next() -> Opt[T]` shape is buildable; what has not
> happened is choosing the protocol.

**Two callers.** twill NEEDS-96 (`std/batch.tw` `epoch_batches`,
`eval_batches`). warp entry 14 (`src/pipeline.tw` `Iter`, `src/stream.tw`
`next_line`).

**Today.** `epoch_batches` returns the whole epoch as a list of pairs, so every
batch of every epoch exists at once. Every consumer of a warp pipeline writes
the same `while true { match next_batch(it) { ... } }`.

**Cost.** For a dataset that fits in memory, one extra copy of it. For one that
does not, it is simply the wrong answer, which is the same limit warp's
`src/stream.tw` exists to remove. The closure-over-mutable-state workaround is
worse than the list: it has no way to say it is finished except a sentinel the
caller must test, which is the pattern that ends in reading one batch past the
end. warp names the same hazard from the other side: the `Opt.None` arm is where
someone eventually forgets to break.

### 25 to 32: single-caller entries

Ranked below the rest by the organising principle, and not dismissed by it. Two
of them are blocking for the caller that filed them.

Five of these eight are delivered -- 25, 26, 27 and 30, and half of 31 -- and
three are open: 28, 29 and 32. Each entry below says which.

**25. A way to fail that cannot be ignored**, **delivered, 1.4.0** (twill
NEEDS-94, `std/nn.tw` `init`). `abort(message)` stops the program with the
message, and `Res` from entry 1 covers the recoverable half, which is the
outcome this entry's placement in stage 5 predicted. There is no `assert`
builtin; `if cond { abort(...) }` is it. **`std/nn.tw` has not adopted it**:
`init` still prints and returns NaNs for an unknown strategy, and its comment
still says there is no way to raise an error, so the cost described below is
still being paid by the module that filed the entry. What follows is what the
entry said while it was open. There was no `error`, no `panic`, no `assert`, and no way to return a
failure that cannot be ignored. `nn.init` takes the strategy by name so nobody
gets Xavier when they meant He without being told, and an unrecognised name is
answered by a `print` followed by a tensor of NaNs. The print lands in the middle
of whatever else is being printed and the NaN surfaces one training step away
from its cause, so the message and the symptom are separated by everything the
model did in between. `std/frame.tw` cannot say a column does not exist,
`std/batch.tw` cannot say a fold count exceeds the row count, and `std/loss.tw`
cannot say a probability was passed where a logit was wanted, which is the most
common mistake the library invites. One caller by the count, four modules by the
evidence.

**26. Allocation and memory counters**, **delivered, 1.6.0** (bobbin entry 2).
All five exist, `mem_counters_available` included, so the "cannot measure" case
answers honestly rather than printing a zero. Five calls:
`mem_counters_available`, `mem_allocs`, `mem_bytes`, `mem_live_bytes`,
`mem_tensors`. A language that cannot report its own allocation count cannot be
profiled from inside. `mem_allocs` catches a regression that moves where code
allocates; `mem_bytes` catches the same count of larger allocations, which is
what a wrong intermediate shape looks like; `mem_tensors` is the twill-specific
one and the most useful, because one avoidable temporary per element is the
difference between a benchmark that fits in cache and one that does not. Until
they exist every reporter omits the memory columns. The `available` flag is not
optional: printing a zero for "cannot measure" is the most misleading thing a
profiler can do.

**27. Ranged reads**, **delivered, 1.6.0** (warp entry 5, `src/stream.tw`
`fill`). One function,
`read_file_at(path, offset, count)`, answering a `Res`. It is the entire content of "streaming"
and every other part of `stream.tw` is written against it. The smallest possible
addition that makes out-of-core data possible: no file handles, no seeking API.

**28. Immutable top-level bindings** (weft entry 9). **Partly delivered:
`const` binds, and is not yet enforced across a file boundary.**
`src/canvas.tw` `QUADRANTS`, `src/theme.tw` `DENSITY`, `src/sparkline.tw`
`LEVELS`, `src/svg.tw` `HEX` are lookup tables that any importer can reassign,
because `Arr` has reference semantics and `let` binds a handle. A library whose
palette can be reassigned by a caller has no way to keep the promise its theme
file makes about which colour means what. This is the mirror of twill NEEDS-86,
which asks whether a file-level `let` initialised by a call runs once, from the
same uncertainty about what a file-level binding is.

weft asked for either a `const` or a read-only top-level `let`, and the choice
between them was the design question this entry was parked on. It is answered by
measurement rather than taste: a read-only `let` was implemented behind a flag
and swept over the 545 `.tw` files under `src/`, `std/`, `testdata/` and
`examples/` here plus the five satellites this entry counts (spool, loom,
bobbin, weft, warp), and it refused 45 of them. Among the refusals are this
repository's own `std/tests/harness.tw` (the pass and fail counters, written
from inside `check`), the same harness in every one of the five satellites,
warp's `examples/train.tw`, this repository's own `src/eval.tw`, fourteen
`testdata/cases` fixtures, and twelve numeric-mode programs under `examples/`
whose training loop is written at file level: `attention.tw`, `classifier.tw`,
`cnn.tw`, `gpt.tw`, `hessian.tw`, `linreg.tw`, `llama.tw`, `minibatch.tw`,
`mlp.tw`, `nn_xor.tw`, `records.tw` and `signal_opt.tw`, ten of which are
mirrored again under `testdata/examples/`. Top-level mutation is an idiom here,
not an accident, so the guarantee has to be asked for. `const` is that keyword:
it binds wherever `let` does, and in the file that declares it both checkers
refuse an assignment through the name -- the binding itself, an element of it, a
field of it, and any nesting of those. A second binding of a const name in the
same scope is refused too, so the guarantee cannot be revoked with nothing said.

**A caller in another file can still assign to an imported `const`, and that is
this entry's actual complaint.** It is not delivered. A plain `import` copies the
name into the importing scope and the handle is shared, so a second file's
`HEX = ...` and `HEX[0] = ...` are both still accepted by both checkers, and
both are still what every other importer then reads. What `const` catches today
is a library breaking its own promise inside its own file, which is not what weft
reported.

A cross-file rule was written and withdrawn rather than shipped. It rode on the
Go checker's import walk -- the walk that exists so a `match` on an enum
declared in another module can be judged exhaustive -- and changing that walk
broke it. A file importing nine or more siblings where a later one declared an
enum stopped being followed, so a non-exhaustive `match` that `main` refuses was
accepted, and whether it was accepted depended on the order the sibling imports
were written in. The same change gave every nested aliased import its own copy
of the cycle guard, which made the walk exponential in aliased fan-out. Neither
was reachable from the ecosystem -- no `.tw` file in the swept corpus makes more
than two plain imports -- so neither the differential sweep nor any test found
them. `internal/checker/imports.go` is now
byte-identical to the file on `main`, and `check()` in `src/check.tw` reads one
file as it always did.

Closing this entry properly means a cross-file rule that does not ride on the
enum walk. Three things are open, and they are separate problems:

- **The binding across a file boundary**, which is the report. It needs the
  checker to know an imported file's top-level `const` names, without changing
  how the enum walk visits files, and it needs the self-hosted checker to learn
  the same thing: `src/check.tw` reads one file and no imported ones at all.
- **`const` is not a deep freeze.** It guards what is written through the name,
  so `HEX[0] = ...` is refused, but `push(HEX, x)` is not, and neither is a
  function handed the handle. Closing that needs a frozen aggregate, or an
  effects rule about where a handle may go, and neither is a checker rule about
  one binding.
- **The self-hosted checker still reads no imported enums.** A `match` on an
  enum declared in another module is unjudged there while the Go checker judges
  it. That gap is older than this entry and is not closed by it.

**29. Optional and named arguments, or record update**, **open** (weft entry
10). Neither form exists: a call site takes positional arguments only, and there
is no update expression over a record. The two halves are not the same size, and
the entry's "or" is doing real work: record update is one new expression form,
while named arguments reach every arity check in both implementations and every
builtin, whose arities are declared as word lists with no parameter names in
them at all. A chart
has a dozen settings and almost every caller changes two. The constructor takes
the three that are always given and the rest are mutated on afterwards, so every
optional setting is a statement rather than an argument and no configuration can
be built by a pure expression. `fix_y` exists purely to give two of those
mutations a name.

**30. A compiler barrier** (bobbin entry 3). **Delivered, and the entry's
premise was wrong.** `black_box(x)` returns `x`, in both modes and on both
implementations.

The entry, and this paragraph before it was rewritten, said the barrier was not
blocking "because twill is interpreted and removes nothing". twill removes
something. `TWILL_TRACE=1` turns on the tracer described in `docs/CODEGEN.md`,
and its liveness rule is dead code elimination: a scope that closes with no live
tensor computes none of the operations it recorded. Under it, a benchmark body
whose result is discarded is not slow, it does not run. bobbin's `bench.keep`
does not stop that, because a call to a twill function is not a barrier and the
tracer follows straight through it; measured, the two are indistinguishable, two
operations traced and zero computed either way. The tracer is off by default and
`docs/CODEGEN.md` section 11.2.4 says it goes on when the numbers say it should,
so this was a wall that was going to be walked into with no warning and no
error, only a number that had got faster.

Filed early on purpose, and the early filing is what makes the fix a one-line
change at `bobbin/src/harness.tw:178` rather than an audit, which was the whole
argument for filing it. What was retracted is the "not blocking today", not the
entry. What the barrier does and does not guarantee is `docs/CODEGEN.md` section
12; the short version is that it stops the elimination this compiler performs,
it is not a shape barrier and not a gradient barrier, and it says nothing about
the machine underneath.

**31. `Dict` keyed by something other than `Str`**, **half delivered** (twill
NEEDS-79 and NEEDS-81). `Dict[I64, V]` works: `dict_set(d, 7, "seven")` and
`dict_must(d, 7)` do what they look like, so NEEDS-79's `str()` at every set and
get is gone from the source. It is not gone from the machine: a dict key is a
string internally and an integer key maps to its decimal string
(`internal/interp/builtins.go`, `dictKey`), so the conversion moved rather than
disappeared. NEEDS-81's half does not follow at all, because that key is by
value: keying by identity, which is what the tape's scan wants, is a different
request wearing the same words.

What follows is what the entry said while it was open. The formatter renders a
line number with `str()` at every set and
every get, which is a decimal conversion per statement printed. The tape's
`tape_node_of_tensor` is worse: a backwards linear scan calling `is_same`, so a
forward pass over a tape of n entries costs O(n^2) identity comparisons. Both
want the same relaxation of the key type.

**32. An empty record, and removing a field**, **open** (twill NEEDS-98).
`record()` is still an unknown name and `{}` is still a block. `dict_del`
arrived in 1.4.0 and removes a *dict* key, which is not this: a record's fields
are not a dict. `{}` is a block
and evaluates to unit, so there is no empty record, and nothing removes a field.
`with_field` builds a record with a name known at run time and is unusable
because there is nothing to start from. The consequence is that `std/frame.tw`
has no `select`, `drop`, `rename` or `from_columns`, and `group_agg` returns its
answer under the fixed names `key` and `value`. `select(df, names)` is the most
basic operation a table has. Two primitives close it and either alone would do.

---

## Stages

> **Stages 0 to 4 are done, and stage 5 is what is left.** This section is kept
> as it was written, because the constraints it argues are the reason the work
> landed in the order it did, and because a plan is worth more as a record than
> as a rewrite. Read the tense as the plan's, not as today's, and read the
> ranking table above for what is delivered: it carries a release per entry and
> is the one authoritative list. This section is an order, not a status.

Ranking by caller count says what to build. It does not say what to build first,
because the entries are not independent. This section is the ordering, and the
constraints are stated rather than implied.

### Stage 0: milestone 1

`mode systems`, `I64`, indexable and sliceable `Str`, `Arr[T]`, `Dict[Str, V]`
with insertion-ordered iteration, `struct`, `read_file`. Already designed in
`docs/self-hosting.md`. Every needs file names it as the baseline. Nothing else
in this document can be attempted first.

### Stage 1: write down what is already assumed

Entries 4, 12, 22, and the `F64` half of entry 8. Plus twill NEEDS-45
(`str(I64)`), NEEDS-36 (`arr(...)`), NEEDS-43 and NEEDS-93 (`Arr` element
assignment and `pop`), and entry 10 (`continue` and `break`).

Everything in this stage is either a sentence in `docs/language-guide.md` or a
mechanical parser change. None of it has a design question except the sign
behaviour of `shr`, and that question has to be answered anyway.

This stage is first for a reason that has nothing to do with cost. Six
codebases guessed at these and did not all guess the same way. Every day this
stage is not done, more code is written against a guess, and the guesses are
already in conflict. Entry 4 alone has three different spellings in the wild and
two contradictory assumptions about `shr`.

Nothing runs at the end of stage 1. That is expected.

### Stage 2: the type system

Entries 1, 2, 3, and 9. Generics, `enum` with payloads and exhaustive `match`,
`Opt` and `Res` with `?`, function values with a declared type and closures, and
containers whose element type may be any type.

These are one stage because they are one piece of work. `Res[T, E]` needs
generics. `Opt[T]` returned from a `Dict` lookup needs generics and `enum`.
`match` is what reads either of them. A comparator parameter needs function
types. There is no useful order inside this stage and splitting it produces
half-features.

**Ordering constraint.** This stage is the prerequisite for the largest amount
of other work. Entry 5's sort wants entry 2's comparator. Entry 19's
`parse_f64` wants entry 1's `Res`. Entry 24's iteration wants entry 3's `Opt`.
Entry 6's file operations want `Res` for their error returns, and warp's needs
file writes their signatures that way already.

**Ordering constraint.** This is also the stage that makes the existing code
correct rather than merely runnable. Six discriminant if-chains stay silently
wrong until `match` is exhaustive. The empty-string error convention stays
unchecked until `Res` exists. Landing stage 3 before stage 2 would produce six
running programs with the same six latent bugs, which is worse than six programs
that do not run, because a program that does not run cannot be trusted by
accident.

At the end of stage 2 nothing runs either, because nothing can do IO.

### Stage 3: the runtime surface

Entries 5, 6, 8, 11, 13, 15, 16, 18, 19, 20, 23, and 32. A sort builtin taking a
comparator. File and directory operations with `stat`. `F64` with its arithmetic
and its math builtins. A monotonic nanosecond clock. `Str` concatenation and a
growable buffer. `str(I64)` and the float renderings. Number parsing. `chr`.
`Bytes`. `record()`.

Each of these is a primitive with a signature and no design question, once stage
2 has settled the types they return. They are grouped because they are
independent of each other and can be landed in any order or in parallel.

**This is the stage where four of the six codebases start running.**

- **weft** runs. It needs stage 1's `F64` annotation, stage 3's `F64`
  arithmetic, `log` and `exp`, `chr`, and the clock. It does not need the
  process interface and it does not need file IO for the library, only for the
  examples, because every function in `src/` returns a string by design.
- **loom** runs. It needs stage 2's function values and `Tree`, and stage 3's
  `F64` and RNG. Mid-epoch checkpointing waits for entry 18's held generator;
  epoch-boundary checkpointing works without it.
- **bobbin** runs and measures time. It needs stage 2's function values for
  `Case.body` and stage 3's clock. It does not report memory until stage 4.
- **twill's own `src/`** runs. It needs all of stage 2 plus stage 3's float
  primitives, and additionally NEEDS-84 (`f64_bits`, `f64_from_bits`), NEEDS-68,
  NEEDS-55 and NEEDS-25's replacement.

spool and warp do not run at the end of stage 3. spool cannot fetch. warp can
read a local decompressed IDX file and cannot stream one.

### Stage 4: the tooling surface

Entries 7, 14, 21, 26, 27, and 30. The test runner. `std/term`. The process
interface. The memory counters. Ranged reads. The compiler barrier.

**This is the stage where all six run.**

- **spool** runs, on entry 21.
- **warp** runs fully, on entry 27 for streaming and entry 21 for fetching and
  decompressing. Without 21 it runs with a manual download step, which is the
  state warp is least unhappy about.

Entry 7 belongs here rather than earlier for a scheduling reason: a test runner
is worth most once there are tests that pass, and five hand-rolled harnesses do
work in the meantime. Entry 21 belongs here rather than earlier for a different
reason, which is that it widens what a `.tw` file can do and should be decided
deliberately rather than pulled forward by a dependency.

Entry 14 is cheap and unblocks three codebases' output quality at once. It is
the best value in this stage.

### Stage 5: the design questions

Entries 17, 24, 25, 28, 29, and 31. The tensor across the seam. Generators. A
way to fail. `const` (the keyword landed; entry 28's cross-file half is still
open). Named arguments. `Dict` keyed by identity.

These are last because each needs a decision rather than an implementation, and
because none of them stops a codebase running. Entry 17 is the largest of them
and the one worth starting early even if it lands late: `mode systems` was
defined by what a compiler needs, and two of the six codebases want both halves
of the language at once.

Entry 25 is the odd one here. It is blocking for `std/nn.tw`, and it is placed
in stage 5 only because `Res` from stage 2 answers most of it. If `Res` turns
out not to cover the abort case, this moves to stage 2.

### Summary of the milestone that matters

| Stage | What it delivers | Codebases running |
|---|---|---|
| 0 | milestone 1 | 0 |
| 1 | the assumptions written down | 0 |
| 2 | the type system | 0 |
| 3 | the runtime surface | 4 of 6: twill, loom, bobbin, weft |
| 4 | the tooling surface | 6 of 6 |
| 5 | the design questions | 6 of 6, better |

Which of these features is delivered is the ranking table's column above, not
this one. The right-hand column here was never a measurement taken in this
repository: it is what the plan predicted
each stage would unlock in six repositories it cannot see, and it is left as the
prediction it was rather than restated as a result.

---

## Where the workaround is silently wrong

Most of the workarounds in this document are ugly and honest. A hand-written
sort is slow and correct. A struct standing in for a tuple is verbose and
correct. Those cost time and readability and nothing else.

The ones below are different. Each produces a plausible wrong answer with no
symptom at the point of the mistake. They are a different kind of debt and they
should be paid first within their stage.

> **Status.** The language now answers most of these. 6's second half needs
> entry 31's identity keying and is still open, and 7 is a decision rather than a
> debt. 2 is settled in favour of arithmetic;
> `f64_signbit` in 8 is a builtin; `all_finite` and `f64_bits` answer 9. Each
> item still describes a repository's source, though, and a workaround does not
> disappear when its replacement ships. These are the ten places to look first
> in an adoption pass.

**1. Enum discriminants as `I64` constants with if-chains.** Six of them, listed
under entry 3. Adding a variant compiles and silently does nothing. bobbin's
seventh verdict would fall through to "missing" in one place and to the default
marker in another, which are two different wrong answers to one addition. loom's
flat `Callback` carries fields that some variants never read, so a caller can set
`patience` on a checkpoint callback and get no effect and no error. spool's
`Pair` holds both a scalar value and a table and nothing stops a caller reading
the one that is not real. Fixed by stage 2.

**2. `shr` on a negative `I64` is unspecified, and two codebases assumed
opposite answers.** loom `src/rng.tw` assumes a logical shift and says
splitmix64's finaliser is wrong with an arithmetic one, so every seed derived
from a negative base seed would be wrong. This repository's `src/float.tw`
assumes Go's answer, which is arithmetic, and builds `ushr`, `udiv10` and
`unonzero` on top of that assumption, where the decimal shifts carry values past
`2^63` with the sign bit set on numbers that are not negative. Both cannot be
right. Whichever way the question is answered, one of these two files is wrong
today, silently, and the wrongness surfaces as a random number stream that
differs by platform or as a float that formats to the wrong digits. This is the
single most valuable finding in the exercise and it exists only because the two
files were written independently. Fixed by stage 1.

**3. The empty-string error convention.** Four codebases return a `Str` that is
empty on success. The compiler does not make anyone read it. spool's variant is
worse: the first byte of the returned string is a status flag, `" "` or `"!"`,
so a caller who forgets to strip it gets a value with a space in front of it and
no error. Fixed by stage 2.

**4. Sentinel returns.** bobbin `src/baseline.tw` `find` returns `-1`. It is a
valid `I64`, nothing forces a caller to check it, and an unchecked `-1` indexes
from the end of an array in many languages. Fixed by stage 2's `Opt`.

**5. `Arr` parameters, if they copy.** twill NEEDS-71. `accumulate` mutates
`cot[node].data` and expects the caller to see it, and `odo_step` advances a
struct's arrays in place. If an `Arr` parameter is copied rather than aliased,
every one of those is a no-op and the whole backward pass returns zeros. A
gradient of zero is not an error, it is a model that does not learn. Fixed by
stage 1 stating the rule.

**6. Identity by linear scan on the tape.** twill NEEDS-81.
`tape_node_of_tensor` scans backwards calling `is_same`. It is quadratic, which
is the visible cost. The invisible cost is that a wrong answer does not fail
loudly: it returns the wrong node and the gradient comes back plausible and
wrong. Fixed by entry 31.

**7. Forward-mode replay applied to a rearrangement.** twill NEEDS-83. The
shared replay path applies the forward kernel to the tangent buffer, which for a
sort would sort the tangents. That is a plausible wrong answer rather than a
loud one, which is why sort, topk and concat report unsupported instead. The
entry is here because the temptation to "just let the replay path handle it" is
exactly the mistake it is guarding against.

**8. `f64_signbit`, or its absence.** twill NEEDS-69. `math.Max(-0, +0)` is
`+0` and a comparison chain cannot tell the two zeros apart. The sign of a zero
is invisible until something divides by it, and when it shows up it shows up as
an infinity of the wrong sign in a gradient, which reads as a bug in the gradient
rather than in `max`.

**9. Infinity detected by comparing against `1.0e308`.** weft `src/fmtnum.tw`.
The NaN test, `v != v`, is correct. The infinity test is a guess about the
representation. A NaN loss is the most important event in a training run and
weft goes to trouble to keep it visible, so the detection should not rest on a
magic constant.

**10. The global random stream, moved as a side effect.** twill NEEDS-95.
`std/batch.tw` honours its per-split seed argument by calling the global
`seed(s)`, so `train_test_split` changes every subsequent `randn`. Splitting the
data after initialising the model gives different weights than splitting before
it, and nothing in the code says so. `stratified_indices` seeds once per class,
so one call consumes and resets the stream several times. loom names the same
hazard: an evaluation pass that drew one random number would shift every
subsequent training batch, and the only reason `src/trainer.tw` is safe is that
its evaluation path draws nothing, which is a property maintained by inspection.

**11. Reporting zero for "cannot measure".** bobbin entry 2's `available` flag
exists for this and it is worth naming as a general rule. A profiler that prints
a zero when it cannot measure is worse than one that prints nothing, and the same
applies to the clock: a millisecond clock timing a microsecond operation reports
a median of zero and an IQR of zero, which reads as a perfectly stable,
infinitely fast operation.

---

## Bugs this exercise found in the Go bootstrap

Three. **Two are fixed and one is still live, in a worse form than described
here.** The ruling that no further Go changes would be made did not hold, and
the record of that is the changelog: 1.6 through 1.9 are Go releases. Each entry
below now opens with its status; the cost paragraphs are left as written,
because the cost is what decided the ruling.

**1. The lexer panics on a file ending in an unterminated string whose last byte
is a backslash.** **Fixed in 1.6.0.** The escape branch checks for input before
advancing and reports "unterminated string" at the opening quote, which is the
diagnosis `src/lex.tw` gave and the reason this was found at all;
`TestUnterminatedStringEndingInABackslash` holds it. `docs/BUGS.md` entry 2 is
the full write-up. What follows is the state at the time of filing.
`internal/lexer/lexer.go`, recorded as NEEDS-33. Source ending
in `x = "ab\` makes the lexer index past the end of its rune slice. The string
branch consumes the backslash and calls `advance()` for the escaped character
without checking that one exists.

*Cost to a user:* a crash with a Go stack trace instead of a diagnostic, on a
file that is merely mistyped. There is no recovery and no line number. A crash
is also indistinguishable from a bug in the user's environment, so the user has
no way to know the fix is to add a quote. `src/lex.tw:405` checks, and reports
"unterminated string" at the opening quote, which is the right diagnosis: the
file's problem is the missing close quote, not the backslash. So the self-hosted
lexer is already correct here and the bootstrap is not.

**2. The einsum gradient is wrong for a bare summed axis.** **Still open, and
the symptom has changed: it now panics rather than answering zeros.** Checked on
2026-09-04 against the current binary. This program

```rust
let A = tensor([[1.0, 2.0], [3.0, 4.0]])
fn f(x) = sum(einsum("ij->i", x))
print(grad(f)(A))
```

dies with `panic: runtime error: index out of range [0] with length 0` inside
`einsumRaw`, reached from `Einsum`'s backward closure. The `continue` quoted
below is still in the source and is still wrong, but it is no longer what
happens: the label the backward einsum cannot size now sizes to zero instead of
returning an error, so the loop that discards the error is never reached and the
contraction indexes an empty output buffer.

A crash is a better failure than a silent zero, so the *cost* paragraph below
overstates today's damage while the *defect* is unchanged. It is still the
serious one, because it is on the ordinary path: summing an axis away is what
einsum is for. It is not in `docs/BUGS.md`'s numbered list, which is a gap in
that document rather than in this one.

What follows is the mechanism as filed. `internal/tensor/einsum.go`, in
`Einsum`'s backward closure. Confirmed by
reading the code rather than inferred.

The gradient of an einsum is another einsum with the operand's subscript as the
output. For `einsum("ij->i", A)` that means asking `einsumRaw` for an output
subscript `ij` from inputs whose only subscript is `i`. The label `j` appears in
no input, so `EinsumOutputDims` cannot size it and returns an error. The backward
closure's response to that error is:

```go
gp, err := einsumRaw(gSubs, inSubs[p], gInputs)
if err != nil {
	continue
}
```

The error is discarded and the loop moves on. The operand's gradient is never
accumulated and stays at whatever it was, which for the first backward pass is
zero.

*Cost to a user:* this is the serious one. Nothing is printed. Nothing fails.
The forward pass is correct, the loss is correct, the training loop runs, and
the parameter behind that einsum simply never updates. A wrong gradient is
invisible until a model underperforms for no apparent reason, and the search for
the reason starts at the learning rate, the initialisation and the data, because
those are the things that usually cause it. The einsum is the last place anyone
looks, and it looks right when they get there, because the forward result is
right. Every einsum that sums an axis away without a matching output label hits
this, and summing an axis away is the ordinary case.

**3. The checker's builtin table is missing `argsort`, `argtopk` and `split`.**
**Fixed.** `argsort` went into `internal/checker`'s table in 1.4.0 and all three
are there now. The two tables were extracted and compared on 2026-09-04:
`internal/checker/checker.go`'s `builtinNames` and `src/builtins.tw`'s `NAMES`
hold **exactly the same names**, with nothing on either side the other
lacks, which is the "fix both tables together" this entry ends by asking for.
What follows is the state at the time of filing.
`internal/checker`, mirrored in `src/check.tw` `builtin_names`, recorded as
NEEDS-66. All three are defined in `internal/interp/builtins.go` and work when
run.

*Cost to a user:* a program that calls one of them is reported as an undefined
variable by the checker and then works. That is the worst possible ordering: the
error message says the function does not exist, which sends the user to look for
a typo or a missing import, and there is neither. The user's most likely response
is to stop using the builtin. Three working builtins are effectively undocumented
and unreachable through any workflow that checks before it runs.

Fixing this one means fixing both tables together, or the diagnostics diverge
between the implementations, which is why it is not a one-line change on the
twill side alone.

---

## Contradictions in the sources

Recorded because a needs file is evidence and evidence with a known error rate
is worth more than evidence without one.

**`tests/harness.tw` is not byte-identical.** bobbin entry 8 calls its copy "the
third byte-identical copy of that file in the ecosystem" and loom entry 15 says
"the same file exists in spool". The five copies have five different hashes and
five different lengths: 69, 68, 65, 65 and 50 lines. spool's imports
`../src/strutil.tw` and lacks the tolerance-taking `near` that loom's has. The
duplication is real and the argument for a test runner is unaffected. The claim
of identity is false, and the drift it hides is a better argument than the
identity would have been.

**spool has no test-runner entry.** weft entry 12 says "Same gap spool records"
and warp entry 13 says "Same gap spool and weft record". spool's `docs/needs.md`
has fourteen entries and none of them is a test runner, despite spool shipping
`tests/harness.tw`. Two agents cited a source that does not say what they say it
says. The feature still has four written callers and this repository, so the
ranking is unchanged.

**The insertion sort count is wrong in three places.** bobbin entry 9 says
seven, which is spool's four plus loom's one plus its own two. loom entry 11 says
five. Neither could see weft's or this repository's three. The counted total is
eleven, and two of those are in this repository under the same name in two
different files, `src/check.tw:254` and `src/fmt.tw:149`. Each undercount is
correct from where it was written, which is the point: no single agent could see
the real number, and that is the argument for consolidating.

**loom and this repository assume opposite `shr` semantics.** **Settled:
arithmetic.** loom entry 7 assumed logical and stated that splitmix64 is wrong
with arithmetic. twill NEEDS-85 assumed arithmetic, because that is Go's answer,
and built three helpers on it. Arithmetic is what shipped and
`docs/language-guide.md` says so in terms, with `shr(-8, 1) == -4` and the
`ushr` idiom written out for the logical one, so a program that needs the
logical shift has a supported way to get it. This is the finding the document
ranked highest, and it wanted an answer rather than a ranking.

**Three spellings of the bitwise operators.** **Settled: both, deliberately.**
spool entry 6 assumed infix with a prefix `not`. loom entry 7 wrote builtin
calls, `xor(a, b)`. weft entry 6 asked for either and said it did not mind
which. `band`, `bor`, `xor`, `shl` and `shr` are infix operators and are also
callable as builtins, and complement is `bnot`, so two of the three spellings
are right and the third differs only in the name of the complement.

**warp's baseline is not the others' baseline.** warp's needs file states its
baseline as milestone 1 including `enum` with exhaustive `match`, `Opt` and
`Res`. Every other file states milestone 1 as excluding them. warp's blocking
list is therefore shorter than it should be and its entries 1 through 5 assume
type-system features the other five filed as blocking. Read warp's list as
resting on stage 2 rather than stage 0.

**NEEDS-29 and NEEDS-87 give opposite advice.** NEEDS-29 says a canonical float
rendering should be a runtime primitive calling the same code the Go side calls,
and warns against a port. NEEDS-87 says that advice was correct only while
calling into the bootstrap was allowed, and that under the no-Go rule the choice
is between a port and no float output. NEEDS-87 says this about itself, so it is
a self-documented contradiction rather than an undetected one, but a reader
reaches NEEDS-29 first and fifty-eight entries earlier.

**NEEDS-57 and NEEDS-88 disagree about where `format_number` lives.** NEEDS-57
argues both halves must live in `src/eval.tw`. NEEDS-88 narrows it: only
`format_value` needs eval, and `format_number` is already in `src/float.tw`.
Again self-documented, and again the obvious reading of the earlier entry is the
wrong one.

**NEEDS-25 describes a native tensor core that no longer exists.** It asks for a
calling convention into a native core and marks it blocking and by design. The
preamble to NEEDS-68 says there is no core, that the kernels and gradient rules
are in twill, and that nothing in `src/tensor.tw` needs a foreign call any more.
NEEDS-25 is still marked blocking.

**One thing that is not a contradiction and reads like one.** bobbin entry 13
declines to add a significance test to `src/baseline.tw` and explains why: a
t-test assumes a normality timings do not have, and a Mann-Whitney U on a few
hundred samples reports significance for differences far below what anyone would
act on. That is a decision recorded so it is not relitigated, not a missing
feature, and it should not be read as one.

---

## What this document does not rank

Cost. Nothing here is estimated in hours, because no entry has been attempted
and an estimate with no attempt behind it is a number that later gets treated as
a commitment. The stages carry the ordering, and within a stage the caller count
carries the priority.

Nothing here is a promise about a release either. It was a queue, ordered by
evidence, and the evidence was that six programs were written and six programs
did not run. That is no longer the state: 1.3 through 1.9 delivered all but
seven of these entries, and the sentence is kept because it is the argument that
got them delivered, not because it still describes the language.

---

## After the queue: distribution

This is recorded here so it is not forgotten, and kept out of the ranked queue
above because it depends on the queue being finished rather than competing with
it. The directive is that twill should be installed the way a language is
installed, not the way a script is: a Windows installer, a macOS package, a
Linux package for the common managers, each dropping a single `twill` binary and
putting it on the path, with an uninstaller that removes it cleanly.

Nothing about this can start until self-hosting closes. There is no artifact to
wrap until the triple build produces one native binary that compiles twill
without the Go bootstrap; an installer around the bootstrap would be shipping the
thing the bootstrap exists to retire. So the order is fixed: the language
feature queue first, the self-compilation milestone second, and only then the
packaging. It is a real goal and a late one, and writing it down now is the whole
of the work it needs today.
