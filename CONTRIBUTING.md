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

Or use the Makefile: `make build`, `make test`, `make check`, `make bench`,
`make examples`. CI additionally runs staticcheck, a dead-code check, the race
detector over `internal/tensor` and `internal/interp`, and every example as a
smoke test.

`src/` does run. The self-hosted CLI is a twill program, so the Go bootstrap can
execute it, and the path it is given is resolved relative to `src/`:

```bash
./twill run src/main.tw run "$PWD/examples/hello.tw"   # self-hosted
./twill run examples/hello.tw                          # bootstrap
```

That is what the conformance gate automates, and it is the only honest way to
review a change to `src/`:

```bash
make conformance         # regenerate docs/conformance.md, then commit it
make conformance-check   # the table is current, and the std suites still agree
make diff                # the fixture corpus still matches its goldens
```

`make conformance-check` runs every suite in `std/tests/` twice, once on each
implementation, and compares the bytes. The divergences that exist today are
listed in `testdata/conformance/suite-allow.txt`, and the ones between the
fixture corpus and its recorded goldens are in
`testdata/conformance/golden-allow.txt`. Both lists may only shrink: a
divergence that is not on the list fails the build, and so does a line on the
suite list that no longer diverges. Adding a line to either file is a change
that has to be argued for in the pull request, not a way to get a build green.

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
