# std/tests

Run the whole tree with the test runner, which discovers every `*_test.tw` file
under a path and reports one line per suite plus a summary:

```
twill test std/tests
```

Or run a single suite by handing the file to the interpreter:

```
twill run std/tests/nn_test.tw
```

A passing suite prints one line per check and ends with `OK`. A failing one ends
with `FAILED`, so a person can read it and `twill test` can gate on it (the
runner exits non-zero when any suite fails).

## Runs today

These are numeric-mode suites, written against the language the bootstrap
interpreter actually implements. They import `harness.tw` as a file.

| Suite | Module under test |
| --- | --- |
| `batch_test.tw` | `std/batch` |
| `frame_test.tw` | `std/frame` |
| `loss_test.tw` | `std/loss` |
| `metrics_test.tw` | `std/metrics` |
| `nn_test.tw` | `std/nn` |
| `optim_test.tw` | `std/optim` |
| `transformer_test.tw` | `std/transformer` |

`harness.tw` is the numeric-mode harness, not a suite. Running it directly does
nothing and prints nothing. It stays a file next to the suites rather than
moving into the library: a numeric-mode harness needs untyped parameters and the
tensor builtins `abs` and `max`, and none of those exist in `mode systems`,
which is where `std/test` lives.

## The systems-mode suites

These are `mode systems` and they assert through `std/test`, the standard
library's assertion module. All six pass on the bootstrap today.

| Suite | Module under test | Checks |
| --- | --- | --- |
| `io_test.tw` | `std/io` | 47 |
| `json_test.tw` | `std/json` | 76 |
| `linalg_test.tw` | `std/linalg` | 48 |
| `random_test.tw` | `std/random` | 39 |
| `stats_test.tw` | `std/stats` | 58 |
| `text_test.tw` | `std/text` | 80 |

They used to import `systems_harness.tw`, one of eleven hand-written harnesses
across this ecosystem. `std/test` replaced it and the file is gone. The counts
above are the same under both, check for check, and so is every byte the six
suites print: 351 assertions lost the leading `rp,` argument and nothing else
moved.

The Go-side regression guard is `cmd/twill/test_test.go`:
`TestNumericStdSuitesPass` runs every numeric suite, and
`TestSystemsStdSuitesPass` runs all six above, so a change to `std/test` cannot
break them silently.

### What this section used to say

Until this file was corrected it said that `random` and `text` were written
ahead of the language and failed, and that three constructs stopped the parser:
generic type annotations (`fn av(xs: Arr[F64], v: F64)`), qualified type
annotations (`let rp: t.Report = t.new_report()`) and struct field assignment
(`rp.passed = rp.passed + 1`). None of that is true of the tree it describes.
All six suites pass, all three constructs parse and run -- `stats_test.tw` and
`systems_harness.tw` were built out of them -- and `go test ./internal/format`
is green, where this file claimed six failing subtests over the same cause. The claims were measured and removed rather than
reworded, because a stale README about which tests fail is worse than none: it
teaches a reader to expect red and to stop looking.
