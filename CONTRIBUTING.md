# Contributing

twill is an early-stage language. Bug reports, small fixes and design discussion
are all welcome.

## Where new code goes

There are two implementations in this repository and they are not equals.

`internal/` and `cmd/` are the **Go bootstrap**. It is the implementation that
runs today, and it is the reference for the semantics. It is maintained, but it
is not where the language is heading.

`src/` is **twill written in twill**: lexer, parser, checker, evaluator, tensor
kernels, formatter and CLI, all in `.tw` files opening with `mode systems`. It
does not run yet, because the language cannot yet express its own compiler. That
is the work. [`docs/self-hosting.md`](docs/self-hosting.md) is the design and
[`docs/needs.md`](docs/needs.md) is the resulting list of what is missing.

**New implementation work is written in twill, in `src/`.** Go changes are for
three things: fixing a bug the port finds in the bootstrap, implementing a
`NEEDS-n` entry so that `src/` can eventually run, and keeping the existing
binary correct. A feature that exists only in Go, with no path into `src/`, is
going the wrong direction.

The most useful contribution right now is a correction to `docs/needs.md`: an
entry the language already satisfies, a workaround that is worse than described,
or a missing entry found by reading `src/`.

## Building and testing

You need Go 1.23 or newer for the bootstrap.

```bash
go build -o twill ./cmd/twill   # build the binary
go test ./...                   # run the tests
go vet ./...                    # static checks
gofmt -l .                      # should print nothing
```

**The gate is `make check`**, not the four commands above: it is
`build vet test race` plus `gofmt -l`, and the race pass is the part the four
leave out. The Makefile says why -- 1.6.5 shipped with a CI failure because the
local gate was `vet test` and gofmt while CI was that plus a race pass. Run
`make check` before you claim a change is green. `make ci` adds the two linters
CI runs, which need the network. `make bench` and `make examples` are the other
targets.

`src/` runs on the bootstrap. `./twill run src/main.tw run "$PWD/file.tw"` runs
a program through the self-hosted toolchain, and `check` and `fmt` work the same
way, so a change there can be executed rather than only read. Give the inner
path in full: the self-hosted CLI resolves a relative one against its own
directory rather than yours, so `run examples/hello.tw` answers
`twill: cannot read file "examples/hello.tw"`. The same bug makes
`examples/frames.tw` look for its CSV under `src/`.

Use `src/main.tw`, not `src/cli/main.tw`. `src/main.tw` is the plain CLI, the
one `internal/interp/selfhost_test.go` drives and the one its own header calls
byte-locked to the Go binary. `src/cli/main.tw` is the decorated front end and
it has a defect `src/main.tw` does not: it never calls a systems-mode program's
`main()`, so `mode systems / fn main() { print("from main") }` prints nothing
through it and exits zero.

**How far the two implementations actually agree.** The front end agrees and the
evaluator does not, so what a self-hosted run tells you depends on which stage
you exercised. Over all 476 `.tw` files this repository tracks -- the whole
tree, not the four top-level directories an earlier draft of this paragraph
counted -- `check` agrees on every one and `fmt` agrees on every one apart from
a by-design blank-line rule. Budget minutes, not seconds: `src/eval.tw` alone
takes about three minutes through the self-hosted checker. The evaluator
implements 120 of the 248 names in `src/builtins.tw`; the other 128 --
essentially the whole systems-mode half, arrays through `dict_*`, `bytes_*`,
`f64_*`, the file and path builtins, `rng_*`, `mem_*`, `gpu_*` and `run` --
reach an explicit "named in the builtin table but has no implementation".
`src/` therefore cannot run `src/`. Do not read a clean self-hosted `check` as
evidence that a change is correct on both sides; the measurements and how to
repeat them are in
`docs/roadmap.md`, "What the second implementation agrees on, and what it does
not".

Three things check it, and they are not the same thing:

- `internal/interp/selfhost_test.go` runs `src/` on the Go interpreter and
  compares the two implementations, `runBothWays` on printed output and
  `runSelfHostedCheck` on diagnostics. These are the bulk of `internal/interp`'s
  runtime and they are skipped under `-short`, which is why `make race` passes
  `-short` and `make test` does not. They are 60 hand-written programs, not a
  corpus: the numeric-mode ones pass and the systems-mode ones are small enough
  to stay inside the 120 implemented builtins, so none of the divergence above
  is in their reach.
- `./twill test std/tests` is the twill-level suite: 17 `*_test.tw` files run,
  about half a second. The directory holds 19 `.tw` files; `harness.tw` and
  `systems_harness.tw` are the helpers the other seventeen import.
- `tools/diff/` compares two binaries over the fixture corpus in `testdata/`.
  Nothing in CI or the Makefile runs it -- `tools/diff` appears in neither the
  `Makefile` nor `.github/workflows/ci.yml` -- and its checked-in goldens have
  drifted behind the corpus, so `-verify` reports mismatches on a clean
  checkout. Read a diff before re-recording: a real regression would appear in
  the same list. Note also what it is not: it takes `-old` and `-new` Go
  binaries, so it never looks at `src/`. The both-implementations corpus check
  `docs/self-hosting.md` milestone 1 asks for does not exist.

Nothing in CI compares the two implementations at corpus scale. If you change
`src/`, run the comparison by hand.

Two tests hold this section to the code rather than to memory.
`internal/checker/builtintable_test.go` asserts that the Go checker's
`builtinNames` and `src/builtins.tw`'s `NAMES` are the same set, and that every
count written down in the documentation is that set's real size.
`internal/interp/selfhost_gap_test.go` pins two of the divergences described
above and fails when they stop being real, naming the files whose prose then
needs updating. A failure there is good news about the evaluator and a chore for
the docs, not a regression.

## Layout

```
cmd/twill/           the command (run / check / fmt / repl)
internal/lexer/      source text -> tokens
internal/parser/     tokens -> AST
internal/ast/        AST node types
internal/tensor/     the differentiable tensor engine
internal/gbm/        native gradient-boosted trees
internal/value/      runtime values and environments
internal/interp/     the tree-walking interpreter and its builtins
internal/checker/    static shape and unit analysis
internal/format/     the source formatter (twill fmt)
src/                 the self-hosted implementation, in twill
std/                 the standard library, in twill, embedded in the binary
examples/            runnable .tw programs
testdata/            the fixture corpus the differential harness runs against
editors/vscode/      syntax highlighting
docs/                the guides, the design notes and the specification
```

## Conventions

- Run `gofmt` before committing. CI checks formatting and `go vet`.
- Every tensor operation that participates in autodiff needs a gradient-check
  test in `internal/tensor/gradcheck_test.go`, comparing the analytic gradient
  to a finite-difference estimate. A wrong gradient does not fail loudly; it
  trains slightly worse, which is why the check has to be mechanical.
- The shape checker stays conservative. Only report a diagnostic when a mismatch
  is certain, and return an unknown shape rather than guess. A false positive
  costs more trust than a missed error.
- Keep the language small. New builtins are cheap; new syntax has to earn its
  place.
- A `NEEDS-n` id is permanent once assigned. A new entry takes the next number
  above the highest one in `docs/needs.md` and never reuses one, so an id in a
  comment means the same thing a month later.

## Prose

Documentation and comments explain why, not what.

- No em dashes, anywhere, in any file.
- The language is twill and the extension is `.tw`. The old name appears only in
  a URL or in a historical CHANGELOG entry, and nowhere else.
- Short sentences. No marketing padding.

## Adding a builtin

1. Implement the operation in `internal/tensor`, with a backward pass if it is
   differentiable, and add a gradient-check test.
2. Register it in `internal/interp/builtins.go`.
3. Teach the shape checker its result shape in `internal/checker/checker.go` and
   add its name to `builtinNames`.
4. Mirror it in `src/tensor.tw` and `src/eval.tw`, or record in
   `docs/needs.md` why it cannot be mirrored yet.
5. Document it in `docs/language-guide.md`.
