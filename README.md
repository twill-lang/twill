<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/twill-lang/twill/main/assets/twill-wordmark-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="https://raw.githubusercontent.com/twill-lang/twill/main/assets/twill-wordmark.svg">
    <img alt="twill" src="https://raw.githubusercontent.com/twill-lang/twill/main/assets/twill-wordmark.svg" width="340">
  </picture>
</p>

<p align="center">
  <b>A small language where tensors are the primitive, <code>grad</code> is built in,<br>
  and a shape mistake is an error you see before the program runs.</b>
</p>

<p align="center">
  <a href="https://github.com/twill-lang/twill/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/twill-lang/twill/ci.yml?branch=main&style=flat-square&label=CI&labelColor=12332C&color=7FE3C4"></a>
  <a href="https://github.com/twill-lang/twill/releases"><img alt="release" src="https://img.shields.io/github/v/release/twill-lang/twill?sort=semver&style=flat-square&labelColor=12332C&color=4FB79B"></a>
  <a href="go.mod"><img alt="go 1.23+" src="https://img.shields.io/badge/go-1.23%2B-D2F0E4?style=flat-square&labelColor=12332C"></a>
  <img alt="dependencies: none" src="https://img.shields.io/badge/dependencies-none-A8DCCB?style=flat-square&labelColor=12332C">
  <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-7FE3C4?style=flat-square&labelColor=12332C"></a>
</p>

---

Most machine-learning code is a language plus a numeric framework bolted on top.
twill goes the other way. Tensors are the built-in data type, differentiation is
a language operation rather than a library call, and a static checker reads your
shapes before anything executes.

Here is the whole of it. This prices a European call by Monte Carlo and gets its
delta and vega by differentiating the pricer, with no bumping and no second
library:

```rust
seed(42)
let Z = randn(200000)                              # fixed shocks: the price is smooth in its inputs

fn call_price(S0, K, r, sigma, T) {
  let drift = (r - 0.5 * sigma * sigma) * T
  let ST = S0 * exp(drift + sigma * sqrt(T) * Z)   # simulated terminal prices
  exp(-r * T) * mean(relu(ST - K))                 # discounted expected payoff
}

let price = call_price(100.0, 100.0, 0.05, 0.2, 1.0)
let delta = grad(fn(s) = call_price(s, 100.0, 0.05, 0.2, 1.0))(100.0)
let vega  = grad(fn(v) = call_price(100.0, 100.0, 0.05, v, 1.0))(0.2)
```

```
$ twill examples/montecarlo_option.tw
European call, S0=100 K=100 r=5% vol=20% T=1y, MC paths: 200000
  price = 10.442696  (Black-Scholes 10.4506)
  delta = 0.636269  (Black-Scholes 0.6368)
  vega  = 37.488476   (Black-Scholes 37.524)
```

`grad` went through 200,000 simulated paths, a `relu` payoff and a mean, and
landed on the closed-form Greeks. No tape object, no `requires_grad`, no
`.backward()`. The full program is [`examples/montecarlo_option.tw`](examples/montecarlo_option.tw).

This is an early prototype; the current release is v1.9.0, and as of v1.4.0
the twill compiler written in twill runs on the Go bootstrap and reproduces the
reference across every stage (see [twill is being written in
twill](#twill-is-being-written-in-twill)). The reference implementation is a
single Go binary with no dependencies, so it is quick to build.

How fast it is, where the time goes and how it compares against PyTorch on the
same mathematics are measured in [docs/BENCHMARKS.md](docs/BENCHMARKS.md).
[docs/CORRECTNESS.md](docs/CORRECTNESS.md) is the evidence for `grad` and for
the checker.

## Contents

- [Shape errors, before the program runs](#shape-errors-before-the-program-runs)
- [Why](#why)
- [Install](#install)
- [Run](#run)
- [Differentiation](#differentiation)
- [The language in a few lines](#the-language-in-a-few-lines)
- [Units of measure](#units-of-measure)
- [Tensors and operations](#tensors-and-operations)
- [The standard library](#the-standard-library)
- [What twill is built out for](#what-twill-is-built-out-for)
- [twill is being written in twill](#twill-is-being-written-in-twill)
- [What is not done yet](#what-is-not-done-yet)
- [Repository layout](#repository-layout)
- [Documentation](#documentation)
- [License](#license)

## Shape errors, before the program runs

The single most useful thing twill does is refuse to start. `twill check` infers
tensor shapes across the whole program and reports the ones that cannot line up:

```
$ twill check bad.tw
bad.tw:3: shape error: shape mismatch in @: [2, 3] @ [2] (inner 3 != 2)
  3 | let y = A @ x
```

Function parameters can carry shape annotations, which turn a contract into
something the checker enforces at every call site:

```rust
fn matvec(A: [3, 2], x: [2]) -> [3] {
  A @ x
}
```

Break that contract and you get the mistake at the call and at the line it
breaks, both before anything runs:

```
$ twill check model.tw
model.tw:6: shape error: argument 2 ("x") axis 0 is 3 but the signature expects 2
  6 | let out = matvec(A, [1.0, 2.0, 3.0])
model.tw:2: shape error: shape mismatch in @: [3, 2] @ [3] (inner 2 != 3)
  2 |   A @ x
```

Use `[2]` for a vector, `[3, 2]` for a matrix, `[]` for a scalar, and `_` for a
dimension you do not want to pin down. A dimension can also be a name, a shape
variable: a name used more than once must be the same size, which is what ties
shapes together across a signature and lets the checker verify the return type of
`fn mm(A: [n, k], B: [k, m]) -> [n, m]`.

The checker only flags a mismatch when it is certain. Code whose shapes depend on
runtime values is left alone rather than guessed at, so a clean run means what it
says:

```
$ twill check examples/shapes.tw
examples/shapes.tw: no shape problems found
```

## Why

Autodiff as a runtime library, shapes known only once you run, and a layer of
glue between the math and the program are all consequences of the same thing: the
framework arrived after the language. twill is an experiment in the other
direction, a language built around differentiable tensor programs from the start.
Three things fall out of it.

**Tensors are the primitive.** Every number is a rank-0 tensor, vectors and
matrices are literals, and `@` is matrix multiply. Broadcasting follows NumPy
rules, and the gradients broadcast back correctly.

**`grad` is a builtin backed by a real reverse-mode engine.** It follows the
structure of its argument, so a model held in a list gets a matching list of
gradients back, and a model held in a record gets a record.

**Shapes and units are checked statically.** `[2,3] @ [4]` is an error you see
before the program runs, not a stack trace forty minutes into training.

The language is deliberately small, and the reference implementation is about
25,000 lines of Go with no dependencies, of which the differentiable tensor
engine is 4,700, the interpreter 6,900 and the static checker 4,700. Another
14,000 lines are tests. Large tensor operations run across CPU cores,
deterministically: parallelism never changes a result.

## Install

Download a prebuilt binary for your platform from the
[releases page](https://github.com/twill-lang/twill/releases) and put it on your
`PATH`. With a Go toolchain (1.23 or newer) you can also:

```bash
go install github.com/twill-lang/twill/cmd/twill@latest
```

Or build from source:

```bash
git clone https://github.com/twill-lang/twill.git
cd twill
go build -o twill ./cmd/twill
```

## Run

```bash
twill examples/autodiff.tw      # run a program
twill check examples/shapes.tw  # shape-check without running
twill fmt examples/hello.tw     # print canonically formatted source
twill test std/tests            # run every *_test.tw under a path
twill                           # start the REPL (multi-line aware)
```

The REPL keeps reading until brackets balance, so block-body functions can be
defined interactively. Without installing, `go run ./cmd/twill <file.tw>` works
too, and `go test ./...` runs the suite.

## Differentiation

| Builtin | Returns |
| --- | --- |
| `grad(f)` | a function computing `df/d(arg0)`, for scalar or tensor args |
| `grads(f)` | a function returning the gradient of every argument, as a list |
| `value_and_grad(f)` | a function returning `[f(x), df/d(arg0)]` |
| `jacobian(f)` | a function returning the full `[m, n]` Jacobian of a vector output |
| `hessian(f)` | a function returning the `[n, n]` Hessian of a scalar output |

`grad`, `grads` and `value_and_grad` differentiate a scalar output, as a loss
does. `jacobian` handles a vector output and returns every partial derivative at
once ([`examples/jacobian.tw`](examples/jacobian.tw)). `hessian` gives exact
second derivatives, via forward-mode jets over the core ops, which is enough for
Newton's method:

```
$ twill examples/hessian.tw
Hessian of xᵀAx  (equals A + Aᵀ):
tensor([[4, 1], [1, 12]], shape=[2, 2])
step 0   t = 1.5   f'(t) = 5.5
step 1   t = 1.238095   f'(t) = 1.162833
step 2   t = 1.144277   f'(t) = 0.127467
step 3   t = 1.131153   f'(t) = 0.002356
step 4   t = 1.130901   f'(t) = 0.000001
step 5   t = 1.130901   f'(t) = 0
converged to a minimum where f'(t) is ~0
```

The autodiff graph is only built while a value is being differentiated, so
ordinary evaluation does not pay for it.

## The language in a few lines

```rust
# Comments start with '#'.

let a = 3.0
let v = [1.0, 2.0, 3.0]           # a vector, shape [3]
let m = [[1.0, 2.0], [3.0, 4.0]]  # a matrix, shape [2, 2]

let d  = v @ v                    # dot product -> 14
let mv = m @ [1.0, 1.0]           # matrix-vector -> [3, 7]

# A function is one expression or a block; the last expression is returned.
fn rms(t) {
  let n = len(t)
  sqrt(sum(t * t) / n)
}

# Loops, for training code.
let total = 0.0
for i in range(10) { total = total + i }

# Differentiation.
fn energy(w) = sum(relu(w) * relu(w)) / 2.0
let g = grad(energy)([-1.0, 2.0, -3.0, 4.0])   # [0, 2, 0, 4]
```

The [language guide](docs/language-guide.md) covers everything, and the
[design notes](docs/design.md) explain how it works and what is next.

## Units of measure

Scalars carry units too. Declare base units, annotate quantities, and the checker
tracks units through arithmetic, so price times quantity is money but dollars
plus shares is refused. Units are erased at runtime and cost nothing.

```rust
unit USD
unit share

fn notional(px: USD/share, qty: share) -> USD { px * qty }

let price: USD/share = 150.0
let value = notional(price, 200.0)   # USD
```

```
$ twill check bad.tw
bad.tw:6: shape error: unit mismatch: USD*share^-1 + share
  6 | let bad = price + qty
```

See [`examples/units.tw`](examples/units.tw) and the
[language guide](docs/language-guide.md#units-of-measure).

## Tensors and operations

Elementwise ops broadcast NumPy-style, a row vector across a matrix, a column
against rows, a scalar against anything. Beyond arithmetic and `@` the builtins
cover:

| Group | Operations |
| --- | --- |
| Elementwise | `relu`, `sigmoid`, `tanh`, `exp`, `log`, `sqrt`, `square`, `abs`, `clip` |
| Normalizing | `softmax`, `logsumexp` |
| Selection | `maximum`, `minimum`, `where`, and elementwise comparisons |
| Reductions | `sum`, `mean`, `max`, `min`, `prod`, `median`, `argmax`, `argmin` (optional axis) |
| Rearranging | `flip`, `roll`, `diff`, `reshape`, `broadcast_to`, `transpose`, `concat`, `split` |
| Sorting | `sort`, `argsort`, `topk`, `argtopk` |
| Scans | `cumsum`, `cumprod`, `cummax`, `cummin` (optional axis) |
| Contraction | `einsum("ij,jk->ik", A, B)`, differentiable and general |
| Deep learning | `conv2d`, `maxpool2d`, `gather` |

Tensors and lists also support differentiable first-axis slicing (`v[1:3]`,
`m[:2]`). The [language guide](docs/language-guide.md) has the full list.

## The standard library

The `std/` libraries are written in twill itself and compiled into the binary, so
`import "std/nn"` works from any directory with nothing to install alongside it.

| Module | Contents |
| --- | --- |
| `std/nn` | dense layers, activations (`gelu`, `softplus`, ...), He and Xavier initializers, losses including softmax cross-entropy, `nn.conv`, multi-head causal attention |
| `std/transformer` | the GPT-style decoder: pre-norm blocks, tied embedding head, next-token loss, greedy generation, over the `std/nn` pieces |
| `std/optim` | SGD, momentum and Adam, over a model held as a positional list or a named record |
| `std/data` | standardizing, train/test splitting, minibatching |
| `std/backtest` | returns, moving averages, equity curves, drawdown, Sharpe, Sortino, CAGR |
| `std/num`, `std/shapes` | numerics and rearrangements the builtins leave out |

The optimizers walk a model's tensor leaves with `map_leaves` and `zip_leaves`,
which is why the same `optim.adam` works on a list of matrices and on a record of
named weights. A library imported as a namespace, `import "std/nn" as nn`, exposes
its fields in declaration order, so the same program prints the same thing every
run.

Load your own data with `read_csv("data.csv")`, which gives a `[rows, cols]`
tensor, or `read_frame("data.csv")`, which reads a header CSV as a *frame*: a
record of named column tensors, so `df.close`, slicing and `grad` all work on it.
Trained models persist with `save(model, "model.bin")` and `load("model.bin")`.
Tensors, scalars, strings, lists and records of them, and fitted
gradient-boosted forests round-trip bit for bit, so you can train once and ship
the model with the single binary for inference. Functions and closures do not
serialise and `save` rejects them.

Randomness is deterministic by default and seeded, so a program reproduces
exactly. `seed(n)` picks the starting point. That is a claim about one machine:
Go's `math.Exp` differs by one ULP between arm64 and amd64, so a program that
calls `exp` can differ in the last bit across architectures, and an iterative
method can turn that into a visible difference. `docs/CORRECTNESS.md` section 4
has the measurement.

## What twill is built out for

The same `grad`, the same checker and the same binary cover the stack:

| Example | What it does |
| --- | --- |
| [`nn_xor.tw`](examples/nn_xor.tw) | a small net, with `grad` taken over the whole parameter list |
| [`classifier.tw`](examples/classifier.tw) | a 3-class MLP with softmax cross-entropy and Adam |
| [`cnn.tw`](examples/cnn.tw) | a convolutional net, conv to relu to max-pool to dense, trained end to end with the kernel included |
| [`attention.tw`](examples/attention.tw) | a self-attention sequence classifier; `grad` differentiates the attention softmax and the learned embeddings together |
| [`minibatch.tw`](examples/minibatch.tw) | a full training loop: standardize, split, reshuffled minibatches each epoch |
| [`gbm.tw`](examples/gbm.tw) | gradient-boosted trees, native and deterministic, no XGBoost |
| [`records.tw`](examples/records.tw) | parameters in a record, so `grad` returns a record of gradients |
| [`frames.tw`](examples/frames.tw) | realized volatility from a price series, over a named-column frame |
| [`backtest.tw`](examples/backtest.tw) | a vectorized backtester built on the cumulative builtins |
| [`signal_opt.tw`](examples/signal_opt.tw) | tuning a trading signal by gradient ascent *straight through* the backtest |

`signal_opt.tw` is the one worth a second look. Because the Sharpe ratio is
differentiable in the return series, the backtest is a function you can climb,
which a plain Python backtest cannot do without reaching for JAX.
[docs/finance.md](docs/finance.md) sets out where twill aims to beat a Python
stack for financial ML, and how.

The checker also verifies declared record types, so a model's fields and shapes
are part of the contract:

```rust
type Model = { w: [3, 2], b: [3] }
fn predict(m: Model, x: [2]) -> [3] { m.w @ x + m.b }
```

## twill is being written in twill

> **As of v1.4.0 this runs. The twill compiler written in twill executes on the
> Go bootstrap and matches the reference across every stage.**

The reference implementation is Go. The second one is twill: the lexer, parser,
checker, evaluator, tensor kernels, formatter and CLI, written in the language
itself under `src/`. As of v1.4.0 the whole `src/`+`std/` tree type-checks clean
and runs on the Go bootstrap: `twill check` matched the Go command byte-for-byte
on every corpus file and `twill fmt` on every one it formats, bar a by-design
blank-line divergence. Those runs were counted at v1.4.0, at 443 and 89 files;
the corpus has grown since and the counts are a snapshot, not a running total.
`tools/diff` re-runs the comparison. The self-hosted evaluator runs the entire
example corpus,
autodiff, jacobians, hessians, neural-net training, CNNs, attention, gradient
boosting and Monte Carlo pricing, with output identical to `twill run` save a
couple of 1-ULP float-accumulation differences. It runs on the bootstrap rather
than as its own Go-free binary; bootstrapping to a standalone twill-built
compiler is the next step.

The paragraph above is about the front end, and about the example corpus, and
it is worth being exact about where the agreement stops.
[`docs/conformance.md`](docs/conformance.md) is generated by running both
implementations rather than by reading either, and it says that 118 of the 247
names in the shared builtin table are dispatched by `src/eval.tw` and 128 are
not. A program that reaches for `read_file`, `arr_new`, `push`, `pop` or any of
the `f64_*` scalar functions checks clean on both sides, runs on the bootstrap,
and is a runtime error self-hosted. The gap is not only missing names: of the 17
suites in `std/tests/`, 5 produce identical bytes under both implementations and
12 do not, and `gradcheck_test.tw` passes 19 of 19 on the bootstrap and 17 of 19
self-hosted because the self-hosted `numeric_grad` is wrong in the eighth
significant figure rather than the sixteenth. All of that is on two checked-in
allow-lists that `make conformance-check` and `make diff` enforce, so a new
divergence fails the build and the lists can only get shorter.

Designing the subset a compiler needs was the point of doing it. A `.tw` file
declares its mode on the first line, and `mode systems` turns that subset on: a
real 64-bit integer with defined wrapping, byte strings, arrays, dictionaries,
structs, file reading. Designing it is the actual project; the compiler is
downstream of it and is the easy half. Writing the compiler first is how you find out what the subset
has to be, instead of guessing.

The output so far is two things.

**A specification of what the language still needs.** Every wall the port hits is
written down in [`docs/needs.md`](docs/needs.md), one numbered entry per feature,
naming the file and line that reaches for it and what the Go bootstrap does in the
same place. Implementing an entry is then a matter of making twill do what Go
already does there. It is a work queue, ordered by dependency.

**Bugs in the reference implementation.** `src/lex.tw` was run against
`internal/lexer/lexer.go` over the whole corpus, 385 files at the time, and
4,000 seeded fuzzer cases,
compared on token kind, literal text, line, column, the comment list, and the
error message and its position. Zero divergences on the corpus and the fuzzer,
three on targeted edge cases. One of the three is a bug in the Go lexer: source
ending in an unterminated string whose last byte is a backslash makes it index
past the end of its rune slice and panic. The twill lexer checks, and reports
"unterminated string" at the opening quote, which is the better diagnosis. It is
recorded as `NEEDS-33` and fixed in the Go lexer, with
`TestUnterminatedStringEndingInABackslash` covering it.

The design, including why file-level modes are the mechanism and what each
feature costs the numeric language, is in
[`docs/self-hosting.md`](docs/self-hosting.md).

[spool](https://github.com/twill-lang/spool), the package manager, is the same
experiment run a second time: a real program written against the subset, with its
own list of what is missing.

## What is not done yet

This is a prototype, and some of it is deliberately left for later.

- It is interpreted. Tensor ops loop in Go, and there is no vectorized or GPU
  backend. The interpreter is the reference for the semantics.
  [docs/gpu-feasibility.md](docs/gpu-feasibility.md) measures what a GPU backend
  would actually buy and recommends against it for now.
- **There is a compiler, and it is off.** `TWILL_TRACE=1` turns on a tracer that
  records tensor operations as the interpreter runs them, compiles the graph to
  C and calls it. It is correct, and on every program measured it is *slower*
  end to end, between a quarter and a factor of two and a half
  ([docs/CODEGEN.md](docs/CODEGEN.md) §11.2). That is a scope boundary rather
  than a bug: a statement is the largest region whose live values are known
  exactly and for free, and a training loop does not fit in one statement, so a
  loop traces and then escapes on the next statement instead of compiling. Where
  the work does fit in a statement it wins, `montecarlo_option.tw` at 1.65x with
  a third less memory. Widening it means tracing across statements, which needs a
  real liveness analysis over the interpreter's environments and its Go stack.
  That is a different project and it is not started. Five attempts and three
  reverts are written up in §11 rather than summarized here.
- Autodiff is reverse-mode and first-order. `grad(grad(f))` is refused rather
  than silently answered with zero; use `hessian` for second derivatives.
- The shape checker is best-effort, not a full type system. It catches
  mismatches when shapes are statically knowable and stays quiet otherwise.
- Imports are files and `std/` modules. There is no package manager yet, and no
  versioning of third-party libraries.
- The self-hosted compiler runs on the Go bootstrap, not yet as its own Go-free
  binary. Bootstrapping to a standalone twill-built compiler is the next step.
- The self-hosted evaluator implements 118 of the 247 builtins, and disagrees
  with the bootstrap on 12 of the 17 standard-library suites.
  [`docs/conformance.md`](docs/conformance.md) has the list, regenerated from a
  real run by `make conformance`.

The [design notes](docs/design.md) go into the roadmap.

## Repository layout

```
cmd/twill/           the `twill` command (run / check / fmt / repl)
internal/lexer/      source text -> tokens
internal/parser/     tokens -> AST
internal/ast/        AST node types
internal/tensor/     the differentiable tensor engine
internal/gbm/        native gradient-boosted trees
internal/value/      runtime values and environments
internal/interp/     the tree-walking interpreter and its builtins
internal/checker/    static shape and unit analysis
internal/format/     the source formatter (twill fmt)
src/                 the self-hosted implementation, written in twill
std/                 the standard library, written in twill and embedded
examples/            runnable .tw programs
editors/vscode/      syntax highlighting for .tw files
assets/              the mark, the wordmark and the icons
docs/                the guides, the design notes and the specification
```

## Documentation

Start at [docs/README.md](docs/README.md), which indexes the lot. The short
version:

| Document | For |
| --- | --- |
| [tutorial.md](docs/tutorial.md) | from nothing to a trained model |
| [tutorial-systems.md](docs/tutorial-systems.md) | `mode systems`, ending in a working parser |
| [language-guide.md](docs/language-guide.md) | the reference |
| [design.md](docs/design.md) | why it is built this way, and the roadmap |
| [self-hosting.md](docs/self-hosting.md) | the systems subset, and the port |
| [conformance.md](docs/conformance.md) | which builtins each implementation actually runs |
| [needs.md](docs/needs.md) | what the language still has to provide |
| [finance.md](docs/finance.md) | the financial-ML case, assessed honestly |
| [brand.md](docs/brand.md) | the mark, the palette and the asset rules |

Bug reports, small fixes and design discussion are all welcome. See
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE).
