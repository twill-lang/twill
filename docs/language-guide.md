# Twill language guide

This is the reference for Twill v1.7. The language is small, so this is short.

## Running programs

```bash
twill path/to/program.tw    # shape-check, then run
twill run path/to/program.tw
twill check path/to/program.tw   # shape-check only
twill fmt path/to/program.tw     # canonically format (add --write to edit in place)
twill                             # REPL
```

`twill fmt` reprints a program in a canonical style, preserving comments. It
refuses rather than move a comment it can't place.

Pass `--no-check` to run without the static shape check. In the REPL, each line's
value is printed; `:help` and `:quit` do the obvious things.

## Lexical structure

- Comments run from `#` to end of line.
- Whitespace is insignificant, with one exception: a token that could either
  continue the previous line or start a new one is resolved by indentation.
  A line that opens with `+` or `-` **continues** the previous expression when it
  is indented past the column the statement began at, and **starts a new
  statement** when it lines up with that column or sits to its left:

  ```
  let total = base_rate
    + adjustment          # continues: indented past `let`
    - discount

  let x = f(a)
  -mean(y)                # a new statement: lines up with `let`
  ```

  The same rule settles a line that opens with a bitwise word followed by `(`,
  such as `xor(a, b)`, which is a call starting a new statement rather than the
  previous expression continued by `xor`. A leading `(` or `[` always begins a
  new expression rather than a call or index on the previous line. Ending a line
  *with* the operator continues an expression too, and always did.
- Identifiers match `[A-Za-z_][A-Za-z0-9_]*`.
- Numbers are floating point: `3`, `3.14`, `1e-3`, `.5`.
- Strings use double quotes with `\n`, `\t`, `\"`, `\\` escapes.

## Values

| Type | Example | Notes |
| --- | --- | --- |
| Tensor | `3.0`, `[1.0, 2.0]`, `[[1.0],[2.0]]` | The core type. Scalars are rank-0 tensors. |
| Bool | `true`, `false` | From comparisons and logic. |
| String | `"hello"` | For `print` and messages. |
| List | `range(5)`, `[grad(f), 2]` | Heterogeneous; from `[...]` of non-numbers, `list(...)`, or `range`. |
| Record | `{ w: [1.0], b: 0.0 }` | Named fields; access with `.`. |
| Function | `fn(x) = x + 1` | Closures capture their scope. |
| Unit | `()` | The result of `print`, loops, etc. |

A bracketed literal whose elements are all numbers (or nested numeric brackets)
is a tensor. If any element isn't numeric, it's a list. Build a tensor from
computed values with `tensor([...])`.

```rust
[1.0, 2.0, 3.0]           # tensor, shape [3]
[[1.0, 2.0], [3.0, 4.0]]  # tensor, shape [2, 2]
[grad(f), "x", true]      # list
```

## Systems-mode types

A file whose first non-comment line is `mode systems` gets the subset designed
in `docs/self-hosting.md`. Annotation is mandatory there, so every type has to
have a name that can be written. This is the complete list of those names.

| Name | What it is |
| --- | --- |
| `I64` | signed 64-bit integer, two's complement, wrapping |
| `F64` | IEEE 754 binary64 scalar, **not** a rank-0 tensor |
| `Bool` | `true` or `false`, the result of a comparison or of `and`/`or`/`not` |
| `Byte` | an `I64` constrained to 0..255 at construction |
| `Bytes` | a growable, mutable byte sequence |
| `Str` | an immutable byte string, O(1) length and byte indexing |
| `Arr[T]` | a growable, mutable, homogeneous array |
| `Dict[K, V]` | a hash map, `K` is `Str` or `I64`, iterated in insertion order |
| `Opt[T]`, `Res[T, E]` | the two standard enums |
| a `struct` name | a nominal record declared with `struct` |
| `Unit` | the type of `()` |

`docs/self-hosting.md` section 1.2 lists all of these except `Bool` and `F64`,
and section 1.3 makes annotation mandatory, which is a contradiction rather than
an omission: a `Bool` field cannot be declared and a `F64` cannot be declared,
so no file that has either can be written at all. Both are named here.

A parameter or a field may also be annotated with a **function type**,
`fn(I64) -> F64`, which is what a callback is declared as.

### The annotations are checked

Until 1.6 these names were advisory: the checker knew `I64` was a type rather
than a unit and nothing further, so `let x: I64 = "hello"` passed the check and
whatever happened next happened at run time. They are real now, and a definite
mismatch is a diagnostic:

```
lexer.tw:14: shape error: "kind" is declared I64 but the value is Str
lexer.tw:22: shape error: field "src" of Lexer is declared Str but the value is F64
lexer.tw:31: shape error: argument 2 ("n") is declared I64 but the value is Bool
lexer.tw:38: shape error: function "peek" returns Opt[I64] but its signature declares I64
```

It is checked at a binding, an argument, a return, a struct field both at
construction and at every later assignment, and an enum payload.

**The policy is the shape checker's, not a stricter one.** A mismatch is
reported only when both sides are known and cannot be the same value; anything
the checker cannot resolve -- a type from another module, an unbound type
parameter, a value whose type is unknown -- judges nothing rather than
guessing. `docs/self-hosting.md` section 1.3 asks for the opposite policy, where
a type that is still unknown at the end of inference is itself an error, and
that is deliberately not what this is: it would make every unannotated
expression in a mostly-annotated file an error, and a checker that is wrong
often gets turned off. The stricter policy stays open as `docs/needs.md`
NEEDS-49.

**`I64` and `F64` stand for each other, and one case does not.** An `I64`
annotation converts the value, so `let mid: I64 = (lo + hi) / 2` truncates and
is meant to; a scalar of either kind is therefore accepted where the other is
declared. The exception is a written fraction: `let n: I64 = 2.5` is reported,
because truncating a literal somebody typed produces a number nobody typed.

### `Bool`

`Bool` is a type name in systems mode, spelled exactly like that, and it is
legal anywhere a type is: a parameter, a return type, a `struct` field, a `let`
annotation, and an `Arr` or `Dict` element type.

```rust
struct Tok { kind: I64, text: Str, trailing: Bool }

fn is_space(c: I64) -> Bool = c == 32 or c == 9
```

There is no conversion between `Bool` and `I64` in either direction, and none is
implied by a comparison. `if` and `while` take a `Bool` and nothing else, so an
`I64` used as a condition is a checker error rather than a test against zero.
Numeric mode is unaffected: there a comparison still yields the `Bool` value
described under [Values](#values), and `Bool` is not a name the checker knows,
because numeric mode has no annotations to write it in.

### `F64`, and what a systems-mode scalar is

**A systems-mode scalar is not a tensor.** `F64` is a plain immutable 64-bit
float, held by value, with no shape, no gradient, no tape entry, and no
allocation. `mode systems` has no tensor type at all, so a rank-0 tensor is not
merely discouraged there, it cannot be named.

This is the answer to a question that was priced before it was asked. loom's
`src/metrics.tw` accumulates a running total once per training step. If a
systems-mode scalar were a rank-0 tensor, every `total = total + x` would
allocate a tensor and a tape node, and an epoch would build a chain of them; the
accumulator would cost more than the model. It does not. `Meter.total: F64` is a
machine word and `+` on it is an instruction.

The two halves of the language are separated by a conversion and not by a
coincidence of representation:

- `f64(n)` widens an `I64` to `F64`, losing precision above 2^53.
- `i64(x)` narrows an `F64` to `I64`, **truncating toward zero**, and is
  undefined outside the `I64` range.
- There is no implicit conversion in either direction, ever, and no implicit
  conversion between `F64` and a numeric-mode tensor. Crossing that seam is
  entry 17 of `docs/roadmap.md` and is deliberately not answered here.

`F64` carries the ordinary IEEE rules, which are the same rules numeric mode
already has: `NaN != NaN`, division by zero gives an infinity rather than
failing, and `-0.0` and `0.0` compare equal while being different values. The
comparison operators are defined on `F64` and return `Bool`.

Arithmetic is `+ - * /` and `%`. `%` on `F64` is the floored modulo numeric mode
already uses (see [Integer division and modulo](#integer-division-and-modulo-on-i64)
for why the `I64` rule is different). The transcendental functions on `F64` are
entry 15 of the roadmap and are not specified here; what is specified is that
when they arrive they take and return `F64`, not a rank-0 tensor.

Numeric mode is unaffected. There a scalar is still a rank-0 tensor, which is
principle 1 in `docs/design.md` and is what keeps autodiff, broadcasting and
printing uniform. The two answers differ because the two modes want different
things, and the mode gate is what lets both be true.

## Operators

Lowest to highest precedence:

| Operators | Meaning |
| --- | --- |
| `or` / `\|\|`, `and` / `&&` | short-circuiting logic |
| `==` `!=` `<` `<=` `>` `>=` | comparison (returns Bool); see [Equality](#equality) |
| `+` `-` | add / subtract (elementwise) |
| `*` `/` `%` `@` | multiply / divide / modulo (elementwise), matmul (`@`) |
| `^` | power (right-associative, scalar exponent) |
| unary `-`, `not` / `!` | negation, logical not |

Elementwise operators broadcast NumPy-style: a scalar against a tensor, a row
vector across a matrix, a column vector down its rows, and so on. Two shapes
combine when, aligned from the right, each pair of dimensions is equal or one of
them is 1. `@` covers vector·vector (dot), matrix·vector, vector·matrix, and
matrix·matrix.

### Integer division

`/` is exact division and always gives a float: `7 / 2` is `3.5`. `//` divides
and truncates toward zero, which is the integer division a count, an index or a
midpoint wants:

```
7 // 2        # 3
-7 // 2       # -3, truncated toward zero, not floored
(n + k - 1) // k   # the ceiling of n/k
314 % 100     # 14, the matching remainder
```

Both intents are written down rather than inferred from the operands, because
every number runs as an `F64` and there is nothing to infer from. `x // 0` is an
error, where `x / 0` is an infinity.

An `I64` annotation also truncates, and it does so in both places it can appear
— a binding and a return:

```
let mid: I64 = (lo + hi) / 2          # truncated at the binding
fn ceil_div(n: I64, k: I64) -> I64 {  # truncated at the return
  (n + k - 1) / k
}
```

### Bitwise operators on `I64`

These belong to `mode systems` and to `I64` only. There is no bitwise operator
on `F64` and no unsigned integer type. Each is available infix and as a call,
and the two spellings are the same operation.

| Infix | Call | Meaning |
| --- | --- | --- |
| `a band b` | `band(a, b)` | bitwise AND |
| `a bor b` | `bor(a, b)` | bitwise OR |
| `a xor b` | `xor(a, b)` | bitwise XOR |
| | `bnot(a)` | bitwise complement, every bit flipped |
| `a shl k` | `shl(a, k)` | left shift, zeros shifted in at the bottom |
| `a shr k` | `shr(a, k)` | **arithmetic** right shift, sign bit shifted in |

The shifts bind like `*` and `xor`/`bor` bind like `+`, which is where their
symbolic equivalents sit in C and Go.

**The bitwise AND and OR are `band` and `bor`, not `and` and `or`.** `and` and
`or` infix are the short-circuiting *boolean* operators and return an operand,
so `x and 255` is `255` for any non-zero `x` — a silent wrong answer, not an
error. The bitwise meaning has its own name for the same reason the bitwise
complement is `bnot` and not `not`. `and(a, b)` and `or(a, b)` in call form
remain the bitwise operations, for the code that already wrote them that way.

`I64` is two's complement and exactly 64 bits. `and`, `or`, `xor` and `not` are
defined bit by bit on that representation and have nothing to say about sign.
`shl` discards bits shifted off the top, so it wraps, and it is the same
operation for negative and non-negative operands.

**`shr` is arithmetic, not logical.** With a negative left operand it shifts
copies of the sign bit in from the top, so `shr(-8, 1)` is `-4` and `shr(-1, k)`
is `-1` for every `k`. Equivalently, `shr(a, k)` is `floor(a / 2^k)` for every
`a`, which is what makes it the right shift for arithmetic and the wrong one for
bit manipulation.

This matters more often than it looks, because the subset has no unsigned type,
so any 64-bit quantity that is conceptually unsigned is carried in an `I64` and
has its top bit set half the time. Hash mixers, IEEE 754 bit patterns and
multiprecision limbs are all in that position.

**Shift counts.** `k` is masked to its low six bits, so the effective count is
`k and 63`. A count of 64 or more therefore wraps rather than saturating to zero
or to the sign, and a negative count is masked into the same 0..63 range rather
than shifting the other way. Both are almost always a bug at the call site; the
masking exists so the operation is total and platform-independent, not because
either is a useful thing to write. Do not rely on it. Where a shift count is
computed, range-check it.

#### Getting a logical right shift

There is no `ushr` operator. Build one. `src/float.tw`'s `ushr` is the idiom,
and it is what every caller in the ecosystem should use or copy:

```rust
let SIGN_BIT: I64 = shl(1, 63)

fn ushr(x: I64, k: I64) -> I64 {
  if k == 0 { return x }
  if x >= 0 { return shr(x, k) }
  or(shr(and(x, not(SIGN_BIT)), k), shl(1, 63 - k))
}
```

Clearing the sign bit makes the value non-negative, where `shr` is already the
logical shift, and the bit is then put back at the position it would have
shifted to. The `k == 0` guard is not decoration: without it `shl(1, 63 - k)`
would be `shl(1, 63)`, which sets the sign bit rather than clearing nothing.

The same construction appears in `std/random.tw` for splitmix64 and xoshiro.
Anything porting a reference implementation written over `uint64` needs it,
because such a reference's `>>` is logical and `shr` is not.

Related: `docs/needs.md` NEEDS-2 (the type and its operators) and NEEDS-85 (why
this is specified here rather than left to the eventual implementation).

### Integer division and modulo on `I64`

When both operands are `I64`, `/` is integer division and `%` is its remainder.
Neither promotes to `F64` and neither yields a fraction.

**A bare literal is not an `I64`.** An `I64` is a value the program said was
one -- through an annotation, `i64()`, arithmetic on one, or a literal too large
for an `f64` to hold -- so `7 / 2` written out is `3.5` in either mode, and
`a / b` is `3` when `a` and `b` are bound at `I64`. That is what keeps numeric
mode's arithmetic exactly as it was, and it means the two ways to ask for
integer division are an annotation, which converts at the binding, and `//`,
which truncates whatever it is given:

```
let mid: I64 = (lo + hi) / 2    # truncated at the binding
let half = 7 // 2               # 3, said at the operator
```

Mixing an `I64` and an `F64` with a fractional value gives an `F64`, which is
what the same expression computed before `I64` was a distinct type.

**Rounding.** `/` truncates toward zero and `%` takes the sign of the
**dividend**. That is Go's rule, C99's rule and Rust's rule, and the identity it
preserves is the one worth naming:

```
(a / b) * b + a % b == a        for every a, and every b that is not 0
```

| Expression | Value |
| --- | --- |
| `7 / 2`, `7 % 2` | `3`, `1` |
| `-7 / 2`, `-7 % 2` | `-3`, `-1` |
| `7 / -2`, `7 % -2` | `-3`, `1` |
| `-7 / -2`, `-7 % -2` | `3`, `-1` |

**This is not the numeric-mode rule, and the difference is deliberate.** In
numeric mode `%` is the floored modulo, `x - floor(x / y) * y`, so `-7 % 3` is
`2` there and `-1` here. Numeric mode wants the floored answer because a modulo
on tensor data is nearly always a wrap into a range, where a negative result is
a bug. Systems mode wants the truncating answer because the code that uses it is
digit extraction, quantisation and packing, all of which are ports of integer
code that assumes it. Two modes, two rules, one sentence each, rather than one
rule that is wrong for one of them. When a systems-mode program really wants the
floored answer, write `((a % b) + b) % b`.

**`shr` is not division.** `shr(a, k)` is `floor(a / 2^k)` and `/` truncates, so
the two agree for a non-negative `a` and disagree for a negative one:
`shr(-7, 1)` is `-4` and `-7 / 2` is `-3`. Replacing a division by a power of two
with a shift is a valid rewrite only when the dividend cannot be negative. This
is the sharpest edge in the integer half of the subset and it is the reason both
rules are written on the same page.

**Overflow.** `/` wraps like every other `I64` operation rather than trapping,
so `MIN_I64 / -1` is `MIN_I64` and `MIN_I64 % -1` is `0`. It is the only
division that overflows.

**Division by zero.** `a / 0` and `a % 0` abort the program with a diagnostic
naming the operation and its position. They do not return `0`, they do not
return a NaN, and they do not return a `Res`.

`docs/self-hosting.md` section 1.2 says "division by zero is an error value, not
a panic", and this narrows that sentence rather than restating it. What section
1.2 is ruling out is the Go bootstrap's behaviour, a host-language crash with a
stack trace and no source position, and that is ruled out. What it must not be
read as asking for is `/` returning `Res[I64, Str]`: that would put a `?` on
every arithmetic expression in the self-hosted compiler, and a language where
`i + 1` is fallible is a worse language than one where a zero divisor is a bug
that stops. A caller that expects a zero divisor tests for it, which is one line
at the one place it can happen rather than a type change at every place it
cannot.

## Equality

`==` and `!=` are **deep structural comparison**. Two values are equal when they
have the same type and the same contents, all the way down:

```rust
[1.0, 2.0] == [1.0, 2.0]                       # true (tensors)
[1.0, "x"] == [1.0, "x"]                       # true (lists, element by element)
{ w: [1.0], b: 0.5 } == { w: [1.0], b: 0.5 }   # true (records, field by field)
```

The details:

- A tensor's **shape is part of its value**: `[[1.0, 2.0], [3.0, 4.0]]` and
  `[1.0, 2.0, 3.0, 4.0]` hold the same numbers but are not equal. Numbers compare
  by IEEE rules, so a tensor holding a `NaN` is not equal to itself.
- Lists compare elementwise, and must be the same length.
- Records compare field by field, **matched by name**, so declaration order
  doesn't change the answer: `{ a: 1.0, b: 2.0 } == { b: 2.0, a: 1.0 }`. A record
  with an extra field is not equal.
- Values of different types are never equal: `[1.0] == 1.0` is false, not an
  error.
- Functions have no structure worth walking, so they compare by **identity**: a
  function equals itself, and two separately written `fn(x) = x` do not.
- `!=` is exactly the negation of `==`.

The ordering operators (`<`, `<=`, `>`, `>=`) are only defined on scalars;
applying one to a list, record, string, or non-scalar tensor is an error.

For elementwise comparison of two tensors into a tensor of 1s and 0s, use the
`equal` builtin. `==` on tensors gives one Bool for the whole value.

## Strings

A `Str` is an immutable byte string. It is bytes that print, not text: there is
no character type, no rune, and no unicode normalization anywhere in the
language. Everything below follows from that and holds in both modes.

### Equality

`==` on two `Str` values is true when they have the same length and the same
bytes, in order. There is no case folding, no whitespace trimming, no unicode
normalization, and no locale. Two strings that a person would call the same word
are equal only if they are the same bytes, so a decomposed and a precomposed
form of the same accented letter are different strings, and that is the answer
rather than a limitation: a lexer that compares source bytes against literals
needs byte equality and would be wrong under any other rule.

This is the general deep-equality rule from [Equality](#equality) applied to
`Str`, not a special case, so the surrounding rules come with it. A `Str` is
never equal to a value of another type, and that is `false` rather than an
error: `"1" == 1` is false. `!=` is exactly the negation.

`src/term/caps.tw` leans on this for every environment comparison, and
`docs/needs.md` NEEDS-46 records it as a constraint on the `Str` rewrite: making
`Str` a distinct indexable value must not change what `==` means.

### Ordering

**`<`, `<=`, `>` and `>=` order two `Str` values by their bytes**, as of 1.5.1.
They were scalar-only before that, and every codebase that wanted a sorted list
of names had written its own `compare_str` returning -1, 0 or 1 and compared
that against zero -- three of them, independently.

The ordering is the one those hand-written comparisons implemented, written down
here so that eleven copies of it in six repositories agree:

> Compare byte by byte from index 0. At the first index where the two differ,
> the string with the smaller byte value is smaller. If one string runs out
> first and every byte matched, the shorter one is smaller. Bytes are compared
> as unsigned values in 0..255.

That is Go's `sort.Strings` and it is lexicographic on bytes, not on characters:
for ASCII it is alphabetical with uppercase before lowercase, and for anything
else it is UTF-8 code-point order, which is a well-defined order and not a
linguistic one. Any eventual `str_less` builtin or comparator means exactly
this.

The reason ordering is a function rather than an operator, when equality is an
operator, is that equality has one obvious meaning on bytes and ordering has
several plausible ones. `<` on a string would read as "alphabetically before",
which is a promise about language that a byte comparison does not keep. A named
function is a place to put that sentence.

`sort` applies this order directly to a list of strings: `sort(["b", "a"])` is
`["a", "b"]`, and a truthy second argument sorts descending. It returns a new
list and leaves its argument untouched. It orders a list of numbers the same
way, and anything else through a comparison the caller supplies -- see
**Sorting a list** below.

A comparison between a `Str` and a value of another type is still an error. The
ordering is defined on bytes, and there is no byte order between a string and a
number to appeal to.

### Concatenation

**`Str + Str` exists and produces a new `Str`.** It is the one overload of `+`
that is not numeric, and the result is the left operand's bytes followed by the
right operand's.

```rust
"col_" + name + "_0"
```

`+` on a `Str` and a non-`Str` is an error, with no coercion in either
direction. A number is converted with `str()` first, deliberately: an implicit
`str()` inside `+` would make `1 + 2` ambiguous the moment either side came from
a dictionary, and the explicit call is one function name in exchange for that.

`docs/self-hosting.md` gave `Bytes` a `concat` and gave `Str` length, indexing
and slicing, and never said how two strings join. Every codebase in the
ecosystem assumed `+` and spool calls it the single most-used operation in its
source, so this ratifies what was already assumed rather than overriding it.

**Building a string in a loop with `+` is quadratic.** Each `+` allocates and
copies the whole left operand, so `out = out + piece` across n pieces copies
O(n^2) bytes. This is stated here because it is the shape everything reaches for
first and it is affordable exactly until it is not: weft builds a frame of a live
plot from a few hundred pieces at 30 repaints a second, and twill's own
`src/cli/progress.tw` builds a bar a cell at a time. Use the `Bytes` builder for
those:

```rust
let out: Bytes = bytes.new()
bytes.push_text(out, piece)      # amortized O(1) per push
bytes.to_str(out)                # one copy, at the end
```

`src/bytes.tw` wraps that surface (`new`, `push`, `push_text`, `to_str`, plus
`concat`, `join` and `repeat`), and it is where a renderer's inner loop belongs.
`+` is for the outer one.

## Bindings and assignment

```rust
let x = 10     # new binding in the current scope
x = x + 1      # reassign an existing binding (error if not yet bound)
```

`let` always introduces a new variable. Plain assignment updates the nearest
existing binding, which is what makes training loops work.

## Functions

```rust
fn square(x) = x * x       # expression body
fn norm(v) {               # block body; last expression is returned
  let s = sum(v * v)
  sqrt(s)
}
let inc = fn(x) = x + 1    # anonymous function
```

Functions are values and close over their environment:

```rust
fn adder(n) = fn(x) = x + n
let add5 = adder(5)
add5(10)                   # 15
```

`return` exits early; a bare `return` yields `()`.

Parameters may carry shape annotations, and a function may declare its return
shape. These are checked statically (see below); at runtime they're ignored.

```rust
fn matvec(A: [3, 2], x: [2]) -> [3] {
  A @ x
}
```

A dimension can be a concrete size, `_` for an unknown, or a name (a shape
variable). A name used in more than one place must stand for the same size, so
the checker can tie shapes together and verify the return:

```rust
fn matmul2(A: [n, k], B: [k, m]) -> [n, m] {
  A @ B
}
```

Here `k` must match between `A` and `B`, and the result is checked against
`[n, m]`.

### Recursion, and how deep it goes

Functions may recurse, and there is a limit: 10,000 nested calls. Past it the
program is refused with an ordinary error naming the function and the call's
line.

```
$ twill run fact.tw
fact.tw:2: runtime error: call depth limit reached: "fact" is 10000 calls deep,
which is as deep as twill goes. A recursion this deep is almost always a
missing base case; if it is not, rewrite it as a loop
  2 |   n * fact(n - 1)
```

The limit is there because the alternative is not a deeper recursion, it is a
crash: the evaluator runs on the host's stack, and running out of it takes the
whole process down with nothing an error handler can catch. 10,000 is about
forty-six times the deepest recursion measured anywhere in this repository, its
standard library, its test corpus and its nine satellite projects, where the
deepest is the self-hosted compiler checking `src/parse.tw` at 217 nested calls
and no user program passes 18. So a program that reaches 10,000 has almost
certainly lost its base case. Recursion is not the language's loop; a `while`
costs no stack at all.

`TWILL_MAX_CALL_DEPTH` overrides the number for one run. There is exactly one
reason to reach for it, and it is not "my program needs more stack": an
interpreter written in twill, running on this one, spends several of the host's
frames for each of its own, so the host has to be given a larger limit than the
guest before the guest can ever reach its own. That is why

```
TWILL_MAX_CALL_DEPTH=100000 twill run src/main.tw run prog.tw
```

prints for `prog.tw` exactly what `twill run prog.tw` prints, and why the plain
form does not: without it the host stops first and reports against a function
inside `src/eval.tw`. Set it past 150,000 and the stack overflow the limit
exists to prevent comes back.

## Control flow

`if` is an expression:

```rust
let sign = if x > 0.0 { 1.0 } else if x < 0.0 { -1.0 } else { 0.0 }
```

`while` and `for` are statements:

```rust
while i < n { i = i + 1 }

for k in range(5) { print(k) }      # over a list
for xi in [1.0, 2.0, 3.0] { ... }   # over a 1-D tensor
```

### `break` and `continue`

In systems mode a loop body may contain `break` and `continue`.

```rust
while i < len(src) {
  let c: I64 = src[i]
  if c == 32 { i = i + 1  continue }
  if c == 35 { break }
  push(toks, scan(lx))
}
```

- `break` leaves the innermost enclosing loop immediately.
- `continue` skips the rest of the body and begins the next iteration: in a
  `while` that means re-evaluating the condition, and in a `for` that means
  advancing to the next element. In neither case does it re-run anything already
  run in this iteration, so a `continue` in a `while` whose counter is advanced
  at the bottom of the body is an infinite loop, and that is the caller's bug and
  not a special case here.
- Both bind to the **innermost** enclosing loop. There are no labels and no
  multi-level break. A loop that wants to leave two levels sets a flag or is a
  function that returns.
- Both are statements, not expressions. A loop still evaluates to `()`, so
  neither carries a value and `break x` is a syntax error.
- Neither crosses a function boundary. A `fn` written inside a loop body is a
  new scope for this purpose, and a `break` in it is an error rather than a way
  to leave the loop that lexically encloses it. Use `return`.
- Both outside any loop are a checker error naming the statement.

**They are keywords in systems mode only**, at statement position, which follows
the rule `match` already uses. A numeric-mode file that writes `let break = 3`
keeps working, which is why the mode gate is worth the sentence: nothing in
`docs/language-guide.md`'s numeric half changes meaning.

Five parsers in the ecosystem were written against these and none of them could
point at a rule. `docs/needs.md` NEEDS-12 has the cost: twill's own scanner loop
is a chain of `continue`s and nests eight deep rewritten as nested `else`, and
bobbin's sampling loop carries a `done` flag for a loop with four exit
conditions.

## Indexing and slicing

```rust
let v = [10.0, 20.0, 30.0]
v[0]                  # 10 (scalar)

let m = [[1.0, 2.0], [3.0, 4.0]]
m[1]                  # tensor([3, 4], shape=[2]), a row
m[1][0]               # 3
```

Indexing a tensor along the first axis returns a scalar (rank-1) or a slice
(higher rank). Lists index directly.

Slicing takes a half-open range along the first axis; either bound may be
omitted. Both indexing (`x[i]`) and slicing (`x[a:b]`) a tensor are
differentiable: gradient flows to the selected element or rows.

```rust
v[1:3]                # tensor([20, 30], shape=[2])
v[:2]                 # first two elements
v[1:]                 # from index 1 to the end
m[0:1]                # the first row, kept as a [1, 2] tensor
range(10)[2:5]        # works on lists too
```

## Differentiation

```rust
grad(f)            # -> function returning df/d(arg0)
grads(f)           # -> function returning [df/d(arg0), df/d(arg1), ...]
value_and_grad(f)  # -> function returning [f(args), df/d(arg0)]
jacobian(f)        # -> function returning the [m, n] Jacobian of a vector output
hessian(f)         # -> function returning the [n, n] Hessian of a scalar output
jvp(f)             # -> function of (x, v) returning [f(x), J v]
vjp(f)             # -> function of (x, v) returning [f(x), vᵀ J]
hvp(f)             # -> function of (x, v) returning H v
```

`grad`, `grads`, and `value_and_grad` require the differentiated function to
return a scalar; a gradient has the same shape as the argument it corresponds to,
including nested lists. `jacobian(f)(x)` instead takes a function with a *vector*
output and returns the full matrix of partials, where row `i` is the gradient of
output `i`, computed by one reverse-mode pass per output. See
`examples/jacobian.tw`.

```rust
grad(fn(x) = x * x)(4.0)                 # 8
grad(fn(w) = sum(w * w))([1.0, 2.0])     # [2, 4]

let g = grads(fn(a, b) = sum(a * b))([1.0, 2.0], [3.0, 4.0])
g[0]   # [3, 4]   d/da
g[1]   # [1, 2]   d/db
```

Differentiable primitives: `+ - * / % @ ^`, `relu`, `sigmoid`, `tanh`, `exp`,
`log`, `sin`, `cos`, `sqrt`, `sum`, `mean`, `abs`, `pow`.

`hessian(f)(x)` gives the exact matrix of second partial derivatives of a scalar
function, by second-order autodiff via forward-mode jets (see `examples/hessian.tw`
for Newton's method). It supports functions built from arithmetic, the unary
math functions, `matmul`, `sum`, `mean`, and the structural ops indexing
(`x[i]`), slicing (`x[a:b]`), `reshape`, `transpose`, `concat`, and `gather`; a
function using an op outside this set raises a clear error. The reverse-mode `grad` remains
first-order, so a nested gradient is not supported: it is refused with an error
naming `hessian`, rather than returning the zero it would otherwise compute. The
gradient `grad` hands back is a plain value with no history, so differentiating
it again differentiates a constant.

### The two primitives underneath: `jvp` and `vjp`

The five operations above are conveniences over two passes, and those two passes
are nameable in their own right. Reach for them when you want a derivative in one
direction rather than all of them, or when you are writing the derivative rule for
something the language does not already have.

```rust
jvp(f)(x, v)   # [f(x), the Jacobian of f at x times v]      forward mode
vjp(f)(x, v)   # [f(x), v times the Jacobian of f at x]      reverse mode
```

Both return a two-element list rather than only the derivative, matching
`value_and_grad`, because the value at the point you differentiated is almost
always wanted and computing it twice is waste.

`jvp(f)(x, v)` is forward mode. `v` is a **tangent**: it lives in the input's
space, so it must have the same structure and the same shapes as `x` -- the same
record fields, the same list lengths, the same tensor shapes -- and the answer has
the shape of `f(x)`. For a scalar `f` it is the directional derivative
`grad(f)(x) . v`, computed without ever forming `grad(f)(x)`.

`vjp(f)(x, v)` is reverse mode. `v` is a **cotangent**: it lives in the output's
space, so it must have the shape of `f(x)`, and the answer has the structure of
`x`. `grad(f)(x)` is exactly `vjp(f)(x, 1.0)` on a scalar output, which is all
`grad` has ever been.

```rust
fn f(x) = sum(x * x * x)
vjp(f)([1.0, 2.0, 3.0], 1.0)          # [36, [3, 12, 27]] -- the same as grad
jvp(f)([1.0, 2.0, 3.0], [0.5, -1.0, 2.0])   # [36, 43.5]  -- grad . v, in one pass
```

Both follow a parameter tree the way `grad` does, so a record of model weights is
a legal input; `jvp`'s tangent is then a record with the same fields, and `vjp`'s
result is. See `examples/jvp.tw`.

**What they cost.** `jvp` is one evaluation of `f` and nothing else, whatever the
size of the input, which is why forward mode is the cheap direction when a
function has few inputs and many outputs. `vjp` is one evaluation plus one
backward sweep, the same cost as `grad`, which is the cheap direction when a
function has many inputs and one output -- the machine-learning case, and the
reason `grad` is reverse mode (`docs/DECISIONS.md`, decision 1). Getting the full
`jacobian(f)(x)` costs one `vjp` per output row, so when what you need is `J v` or
`vᵀ J`, forming the matrix first is the expensive way to get it.

**The cotangent is an argument, not a closure.** In JAX, `vjp(f)(x)` returns the
value together with a *pullback* function you call later, once per cotangent,
reusing the graph. twill cannot offer that, and the reason is decision 2: the tape
is the tensor graph, so there is no recorded program to replay. A retained
pullback would pin the whole graph, would accumulate into the same per-node
gradient buffers on a second call rather than replacing them, and would outlive
the graph-building region the interpreter opens around a differentiated call. A
closure that quietly gives a different answer the second time is the plausible
wrong number this language refuses to return, so the cotangent is a second
argument and each call is one pass.

**Ops without a forward rule.** Reverse mode covers more operations than forward
mode does; `einsum` is one that has a gradient but no jet. `jvp` and `hvp` on such
a function report it by name rather than propagating a zero tangent, because a
zero tangent is a plausible derivative and an error is not. `vjp` on the same
function works.

### `hvp`: curvature without the matrix

```rust
hvp(f)(x, v)   # H v, for a scalar f, with the shape of x
```

`hessian(f)(x)` builds the whole `[n, n]` matrix. Second-order optimisation almost
never wants the matrix: Newton-CG, trust-region methods and Gauss-Newton all only
ever ask for the Hessian times a vector, and `hvp` is that product directly.
`examples/jvp.tw` runs conjugate gradients on it for a Newton step.

**What it costs, plainly.** `2n+1` forward passes for an `n`-element input, not
the single forward-over-reverse pass JAX charges. Decision 2 again: twill's
reverse pass is not re-differentiable, so the gradient cannot be sent back through
forward mode, and the exact second-order machinery here is the forward 2-jet.
What `hvp` does buy over `hessian(f)(x) @ v` is still real -- `n(n+1)/2` passes
become `2n+1`, and the `[n, n]` result is never allocated -- but it is a constant
factor better, not an asymptotic one, and a model with many parameters is out of
reach for both. It supports the same operations `hessian` does and refuses the
same ones.

The refusal covers the nesting wherever it is written, not only the literal
`grad(grad(f))`. Putting a function between the two --

```rust
let g = grad(f)
grad(fn(x) = sum(g(x)) * 2.0)   # refused
```

-- is the same mistake and used to return zeros silently, which is the worst way
for a derivative to be wrong. The nesting `hessian` and `jacobian` do themselves
is a different thing and still works.

## Shape checking

`twill check` (and the check that runs before `twill run`) infers tensor shapes
and reports mismatches it can prove. It stays quiet when a shape can't be
determined, so dynamic code doesn't produce false alarms.

```
$ twill check bad.tw
bad.tw:3: shape error: shape mismatch in @: [2, 3] @ [2] (inner 3 != 2)
```

Annotations (`[3, 2]`, `[2]`, `[]`, `_` for unknown, or named shape variables)
let you state a contract that the checker enforces at call sites and against the
function body. A shape variable used more than once must resolve to the same
size. Annotated function bodies are also checked at their definition, so a
mistake is caught even if the function is never called.

## Units of measure

Declare a base unit at the top level with `unit`, then annotate scalar
quantities with it. The checker tracks units through arithmetic and reports a
mismatch, the same way it does for shapes, but units are erased at runtime, so
annotated code runs as plain numbers with zero overhead.

```rust
unit USD
unit share

fn notional(px: USD/share, qty: share) -> USD { px * qty }
```

An annotation is a single unit (`USD`) or a compound expression: a product
(`USD*share`), a quotient (`USD/year`), or a power (`year^-1`, `USD^2`). The
checker applies the natural rules:

- `*` multiplies units, `/` divides them, and `^` with a constant integer
  exponent raises them (`sqrt` halves them).
- `+`, `-`, `%`, and comparisons require both sides to share a unit. Adding
  `USD` to `share` is an error.
- `matmul`/`dot` multiply the operand units; indexing and slicing preserve them.
- `exp`, `log`, `sin`, `cos`, `tanh`, and `sigmoid` require a dimensionless
  argument (their result is dimensionless).

A bare numeric literal is dimensionless. To give a value a unit, annotate the
`let` that binds it: the literal is adopted into the declared unit:

```rust
let price: USD/share = 150.0
let qty: share = 200.0
let value = notional(price, qty)   # inferred: USD
```

Naming a unit that was never declared (a typo like `USD/yr`) is a checker error.
Code with no unit annotations is entirely dimensionless and unaffected.

## Records

A record groups named fields. Fields are accessed with `.`.

```rust
let p = { w: [1.0, 2.0], b: 0.5 }
p.w                   # tensor([1, 2], shape=[2])
p.b                   # 0.5
{ inner: { x: 3.0 } }.inner.x   # 3
```

`grad` follows record structure: if a loss takes a record of parameters, the
gradient is a record with the same fields.

```rust
fn loss(m) = sum(m.w) + m.b
grad(loss)({ w: [1.0, 2.0], b: 0.5 })   # { w: [1, 1], b: 1 }
```

This makes a record a natural container for a model's parameters. A `{` starts a
record only when it is followed by `name:`; otherwise it is a block.

A record type can be declared and used to annotate a parameter. The checker then
verifies that the record passed in has the declared fields with the declared
shapes, and that field accesses name real fields:

```rust
type Model = { w: [3, 2], b: [3] }

fn predict(m: Model, x: [2]) -> [3] {
  m.w @ x + m.b
}
```

Accessing a field a record doesn't have (`m.wieght`) is a checker error, whether
the record is a literal or a declared type.

## `struct`, and what a parameter is

`struct` is a systems-mode type, declared by name, with typed fields that are
mutable in place. It is a **different type from `Record`** and the two are not
unified, deliberately: `grad` walks a record's structure and depends on records
not aliasing, so mutation is not retrofitted onto them.

```rust
struct Lexer { src: Str, i: I64, line: I64 }
```

### The rule

**A `struct` has reference semantics. Passing one passes a handle, not a copy.
Assigning to a field of a parameter mutates the caller's struct, and the caller
sees it.** The same holds for `Arr`, `Dict` and `Bytes`, and it holds through
any number of levels: mutating a field of a struct reached through a field of
another struct is visible at the outermost handle.

```rust
fn advance(lx: Lexer) {
  lx.i = lx.i + 1
  if lx.src[lx.i] == 10 { lx.line = lx.line + 1 }
}

let lx: Lexer = Lexer { src: text, i: 0, line: 1 }
advance(lx)
# lx.i is 1 here, not 0.
```

Copying is always explicit. There is no implicit copy at a call, at an
assignment, at a `push` into an `Arr`, or at a return. `let b = a` on a struct
binds a second name to the one struct, and mutating through either is visible
through the other. A copy is made by writing one.

`F64`, `I64`, `Bool`, `Byte` and `Str` are the values that are not handles.
They are immutable, so the distinction is unobservable for them, which is the
point: the line between the two halves of the language is exactly the line
between mutable aggregates and immutable scalars, and there is nothing in
between to remember.

### Why this is written down

Three codebases were built on this and none of them could point at a rule.
`docs/self-hosting.md` section 1.2 says a struct has reference semantics, and
then says nothing about what happens when a function assigns to a field of a
parameter, and nothing at all about `Arr`. That is the gap this closes, and it
is the whole of it: nobody was asking for a feature.

The cost of leaving it open is not evenly distributed, which is why it is worth
a paragraph rather than a line. If the answer had been by-value, loom's `fit`
could not advance the run it was given and every function in loom's
`src/metrics.tw` would have to return a new meter, which is loud: nothing works
and it is obvious that nothing works. `src/tensor.tw`'s is the quiet one.
`accumulate(cot, touched, node, buf)` mutates `cot[node].data` and expects the
caller to see it, so if an `Arr` parameter were copied the mutation would be a
no-op, the backward pass would return zeros, and a gradient of zero is not an
error. It is a model that does not learn, and the search for the reason starts at
the learning rate.

This is also why the `Arr` half and the `struct` half must give the same answer
and are stated together. `Odometer` is a struct holding arrays and is mutated
through both at once; two different answers would make it work in one direction
and not the other.

### What the Go bootstrap does

The bootstrap agrees, as far as it can be asked. `internal/value`'s aggregates
are `*Record` and `*List`, Go pointers, and the interpreter passes them to a
call without copying, so aggregates are already handles there.

What the bootstrap does not have is any syntax that mutates one. Field
assignment (`p.b = 1.0`) is a parse error, `Arr` element assignment is
`docs/needs.md` NEEDS-43, and the only builtins that look like mutation are
`append` and `with_field`, both of which return a new value and leave the
original alone. So the reference-versus-value distinction is currently
unobservable from a twill program, and the rule above is a decision about
systems mode rather than a measurement of numeric mode. It is the decision every
existing caller already assumed, which is the evidence for it being the right
one.

`Record` in numeric mode keeps its own rule and is unaffected: fields are not
mutable in place, and you rebuild the record.

## `enum`, and `match`

An `enum` is a type with a fixed set of cases, each of which may carry one
value. It is how a systems-mode program says "one of these, and nothing else",
which is the thing an integer tag cannot say.

```rust
enum Verdict { Faster, Slower, Same, Noisy }

enum Tok { Ident(Str), Num(F64), Punct(Str), Eof }
```

**A case carries zero payloads or one. Not two.** Twill has no tuple type, and
adding positional payloads would introduce `v.0` as a second field syntax beside
`.name`. A case that needs several values carries a struct:

```rust
struct BinOp { op: Str, lhs: Expr, rhs: Expr }
enum Expr { ENum(F64), EBinary(BinOp) }
```

The payload may be the enum being declared, directly or through a container, so
a tree is an ordinary declaration and needs no explicit indirection:

```rust
enum Json {
  JNull,
  JBool(Bool),
  JNum(F64),
  JStr(Str),
  JArray(Arr[Json]),
  JObject(Dict[Str, Json]),
}
```

A case is written bare (`Eof`) or qualified by its enum (`Tok.Eof`), and the two
are the same thing. The qualifier is read and dropped; it is there for the reader
and for the case where two enums in scope share a case name, which is otherwise
ambiguous.

`Opt[T]` and `Res[T, E]` are ordinary enums that happen to be built in:

```rust
enum Opt[T] { Some(T), None }
enum Res[T, E] { Ok(T), Err(E) }
```

### `match`

```rust
fn describe(t: Tok) -> Str {
  match t {
    Ident(name) => "identifier " + name,
    Num(v) => "number " + str(v),
    Punct(p) => p,
    Eof => "end of input",
  }
}
```

Arms are `pattern => expression`, comma separated, with no braces around each
arm -- braces would re-enter the block-versus-record ambiguity a `{` already
carries. `match` is an expression, so it has a value, and it is a keyword only
at expression position in a systems-mode file: a numeric-mode file that writes
`let match = 3` keeps working.

### What a pattern is

A pattern is one of five things, and the last nests:

| Pattern | Matches |
| --- | --- |
| `_` | anything, binding nothing |
| `name` | anything, binding it |
| `3`, `"hi"`, `true`, `-1` | that value, by the equality `==` gives |
| `Some` | the case, whatever it carries |
| `Some(pat)` | the case, when its payload matches `pat` |

```rust
match result {
  Ok(Some(0)) => "present but empty",
  Ok(Some(v)) if v > 100 => "large",
  Ok(Some(v)) => "present",
  Ok(None) => "absent",
  Err(msg) => msg,
}
```

**A case name starts with a capital letter; anything else binds.** That is the
rule that tells `Some(x)`, where x is a binder, from `Ok(None)`, where None is a
case. It also means a catch-all can say what it caught -- `other => str(other)`
is a `_` with a name -- and that `some(v)` is refused, by name, as a binding
used where a case was meant.

**A guard is `if cond` between the pattern and the arrow.** It sees the
pattern's bindings, and a false guard falls through to the arms below rather
than failing the match. Because a literal pattern matches by ordinary equality,
a `match` over numbers or strings needs no enum written around it:

```rust
fn classify(n) {
  match n {
    0 => "zero",
    1 => "one",
    x if x > 100 => "big",
    _ => "other",
  }
}
```

**A `match` must cover every case.** The checker knows the enum's cases and
reports the ones with no arm, by name:

```
model.tw:14: match on Verdict is not exhaustive: missing Noisy
```

That is an error and not a warning, and it is the reason to have an enum at all:
adding a case to a declaration makes every `match` that has not been updated say
so, at check time, instead of one of them falling through at run time in a month.
Four related mistakes are reported the same way: an arm that repeats a case, an
arm after `_` (which already matched everything), a `_` when every case is
already handled, and arms that name cases of two different enums.

A `_` is right when every unlisted case maps to the same answer for a reason. It
is wrong when the match is dispatch, because then it is the thing that stops the
compiler telling you about the case you have not written yet.

**Exhaustiveness recurses, and counts only what it can prove.** `Some(Ok(v))`,
`Some(Err(e))` and `None` together cover an `Opt[Res[..]]`; drop the `Err` arm
and the diagnostic names the value that gets through:

```
load.tw:14: match on Opt is not exhaustive: missing Some(Err)
```

An arm counts towards this only when nothing but the value's shape decides
whether it runs. A guard is a condition the checker cannot evaluate, and a
narrower nested pattern leaves the rest of its case unmatched, so neither
proves a case handled -- `Some(v) if v > 3` and `None` is *not* exhaustive, and
saying so is the point. A position holding only literals is left unjudged,
since a finite set of them cannot cover a number or a string. `Bool` is the one
exception: `true` and `false` do exhaust it.

## Type parameters

A `struct`, an `enum` or a `fn` may be written in terms of a type it does not
name. The parameters go in `[]` after the name, and stand for whatever the use
site supplies.

```rust
struct Box[T] { value: T, tag: Str }
enum Tree[T] { Leaf(T), Branch(Arr[T]), Empty }

fn first[T](xs: Arr[T]) -> T = xs[0]
```

**They are erased.** The runtime is the same code whatever `T` is, so nothing is
specialised and nothing is generated: the parameters exist for the checker and
disappear before the program runs. What they buy is that the checker knows what
is in the box:

```rust
let b: Box[I64] = Box { value: 3, tag: "count" }
let s: Str = b.value      # "s" is declared Str but the value is I64
```

Substitution goes as deep as the type does, so a `Branch(xs)` arm on a
`Tree[I64]` binds `xs` as an `Arr[I64]`. Two uses of one declaration are
different types when their arguments differ -- a `Box[Str]` is refused where a
`Box[I64]` was declared -- while a non-generic `struct` still compares by name
alone, as it always has.

**Arguments are read out of the value, not written at the use site**, because
there is nowhere to write them: a constructor and a struct literal take no type
arguments. `Leaf(n)` with an `I64` n is a `Tree[I64]`, and inside
`fn swap[A, B](p: Pair[A, B]) -> Pair[B, A]`, the literal
`Pair { first: p.second, second: p.first }` is a `Pair[B, A]` by what it is
built from.

A parameter the use site leaves off is unknown and judges nothing, so a bare
`Box` is exactly as informative as it was before type parameters existed. There
are no bounds and no traits.

## `Opt`, `Res`, and `?`

An operation that can fail returns a `Res`, and one that may have nothing to
return gives an `Opt`. Neither is special to the compiler; what is special is
one piece of syntax for the thing every caller does with them.

**Postfix `?` unwraps a success and returns a failure from the enclosing
function.**

```rust
fn load(path: Str) -> Res[Config, Str] {
  let text: Str = read_file(path)?
  let doc: Doc = parse(text)?
  Ok(build(doc))
}
```

Without it that function is three `match` statements deep and the interesting
line is at the bottom of them. With it the failure path is a character wide and
the success path reads straight down.

The rules, all checked:

- The enclosing function must return a `Res` or an `Opt` for the failure to be
  returned in. `?` in a function returning `I64` is an error naming the return
  type, and `?` at the top level of a file, where there is no function at all,
  is an error too. It used to end the program quietly with status 0, which is
  the one thing a failed read must not do.
- `?` on something that is neither a `Res` nor an `Opt` is an error.
- The error types must match. There is no automatic conversion between them,
  because that needs a trait system and twill does not have one; convert at the
  seam instead.
- What `?` yields is the success payload, and it is typed: `let n: I64 =
  read_file(p)?` is a diagnostic, because `read_file` gives back a `Str`.

`abort(msg)` is the other way to fail, and it means something different. A `Res`
is for what a caller may reasonably handle: a missing file, a malformed line, a
number that does not parse. `abort` is for what no caller can handle because it
should not be possible -- an invariant broken inside the implementation. Every
`abort` in the self-hosted compiler should be unreachable by any input; a `Res`
is what a user's mistake produces.

Making arithmetic fallible was considered and rejected. `a / 0` on an `I64`
aborts naming the operation and its position, rather than returning
`Res[I64, Str]`, because a language where `i + 1` needs a `?` is a worse
language than one where a zero divisor is a bug that stops.

## Imports

There are two kinds of import path, and the spelling tells you which is which.

```rust
import "std/nn"             # a standard-library module (ships inside the binary)
import "helpers.tw"         # a file, relative to the importing file
```

A path beginning with `std/` names a **module** of the standard library, not a
file: no extension, no directory, and it means the same thing from anywhere,
because the library is compiled into the `twill` binary. `std/` is reserved, so a
directory called `std` next to your program does not shadow it. Every other path
is a **file**, resolved relative to the importing file first, then the working
directory; `import "./std/local.tw"` reaches a real directory named `std`.

Either kind can be namespaced:

```rust
import "std/nn"             # drops the module's definitions into this scope
import "std/nn" as nn       # binds them as a namespace record instead
```

A plain import evaluates the module and adds its top-level definitions to the
importing scope; each module loads once, so re-imports and cycles are fine. An
`as name` import instead evaluates it into its own scope and binds a record of
its definitions under `name`, so you call `nn.dense(...)`. That record's fields
are in the module's declaration order, so printing or iterating a namespace gives
the same result on every run.

A standard-library module may only import other `std/` modules. It has no
directory of its own to resolve a relative path against.

To work on the library itself without rebuilding, set `TWILL_STD` to a directory
of `.tw` files; it replaces the embedded library wholesale, so `import "std/nn"`
reads `$TWILL_STD/nn.tw`. Unset it and you are back to the copy in the binary.

## Standard library

Elementwise math (differentiable): `relu`, `sigmoid`, `tanh`, `exp`, `log`,
`sin`, `cos`, `sqrt`, `square`, `abs`, `pow(x, p)`, `clip(x, lo, hi)`.

Elementwise combine: `maximum(a, b)`, `minimum(a, b)`, `where(cond, a, b)`, and
the comparisons `greater`, `less`, `greater_equal`, `less_equal`, `equal`
(each returns a tensor of 1s and 0s).

Reductions: `sum`, `mean`, `max`, `min`, `prod` and `median` reduce the whole
tensor to a scalar, or one axis with a second argument (`sum(t, 0)`).
`argmax(t[, axis])` gives the index of the maximum.

All of them are differentiable, including the two order-based ones, though what
that means is worth being clear about. `median` routes the whole gradient to
whichever element was selected, the way `max` does, and splits it in half
between the middle two when the run has even length. `prod` gives each factor
the product of the others, which is the total divided by that factor, except
where a factor is zero and the division is not available. There, a single zero
takes the product of the rest and everything else gets nothing, and two or more
zeros flatten the gradient entirely, because every product of the others still
contains a zero. `softmax(t[, axis])` and `logsumexp(t[, axis])` default to
the last axis.

`split(t, n | sizes[, axis])` is the inverse of `concat`, returning a list of
pieces. A number means that many equal pieces (`split(x, 2, 1)` halves the
columns) and a list means those exact lengths (`split(x, list(1, 3), 1)`). The
axis defaults to 0. The sizes must account for the axis exactly and an equal
split must divide evenly; both are errors rather than ragged output, because a
split that quietly loses a row shows up later as a wrong loss rather than as a
crash. Each piece keeps its own gradient path, so
`concat(split(t, 2, 1), 1)` is `t` in both directions.

`broadcast_to(t, ...shape)` expands a tensor to a given shape under the usual
right-aligned rules, where every axis must already match or be 1. It is what
you need after a reduction: reducing axis 1 of a `[2, 3]` gives a `[2]`, and
`[2]` will not broadcast back against `[2, 3]`, because alignment is from the
right. `broadcast_to(reshape(mu, list(2, 1)), list(2, 3))` puts it back. Other
array libraries spell this as `keepdims=True` on the reduction itself; here it
is an operation, and `num.keep` wraps the two steps.

Sorting: `sort(t[, axis[, descending]])` and `argsort` give the values and the
positions; `topk(t, k[, axis[, smallest]])` and `argtopk` keep the k largest,
largest first, shrinking that axis to k. All four default to the last axis,
because sorting a matrix almost always means sorting each row. The flags are
numbers, since a comparison in Twill already yields 1 and 0.

`sort` and `topk` are differentiable and exactly so. Sorting is a permutation
and the derivative of a permutation is its inverse: whatever gradient arrives at
the element now in a position belongs to whichever element started there. A
value outside the top k does not move the output at all, so its gradient is
zero, which is correct rather than a simplification. The sort is stable, so ties
keep their original order and therefore their own gradients.

`argmin(t[, axis])` is `argmax`'s counterpart, and `flip(t[, axis])` reverses
along an axis. `flip` is differentiable and exactly so, since a reversal is a
permutation and is its own inverse, which makes the backward pass the same
reversal. All three default to the last axis. Ties in `argmax` and `argmin` go to
the first occurrence, the same rule the cumulative extremes and the sort use.

`roll(t, shift[, axis])` shifts along an axis and wraps what falls off the end
back to the start; `diff(t[, axis])` is the difference between neighbours,
shortening that axis by one. Both are differentiable. A positive shift moves
elements towards the end, so `roll(x, 1)` is the previous value and
`x - roll(x, 1)` compares a series with its own past. `diff` shortens rather than
pads, because there is no honest first difference: a zero there claims "no
change" about data that does not exist, and it is exactly the claim whatever
consumes the series next will believe.

`argsort` and `argtopk` are not differentiable, and not by omission: an index
does not move when an input moves slightly, then jumps when two values cross.
The derivative is zero almost everywhere and undefined on the boundaries.

Cumulative scans (preserving length): `cumsum`, `cumprod`, `cummax`, `cummin`.
These build signals, equity curves, and running peaks, and they are
differentiable: `cumsum` and `cumprod` have exact gradients (`cumprod` handles
zeros in the series), and `cummax`/`cummin` send each output's gradient to the
element the running extreme came from, ties going to the earlier one.

Each takes an optional axis: `cumsum(t)` scans the tensor's elements in order
and `cumsum(t, 1)` scans along axis 1, one run per row, keeping the shape. The
split follows the reductions, where `sum(t)` covers everything and `sum(t, 0)`
works per axis. On a 1-D tensor, which is what a sequence is, the two forms are
the same thing, so the axis is a widening rather than a second meaning.
Elementwise rounding `floor`, `ceil`, and `round` are forward-only (their
derivative is zero wherever it exists), handy for turning random draws into
integer ids.

Linear algebra / shape: `matmul(a, b)` / `dot(a, b)` (same as `@`),
`transpose(t[, ...axes])`, `reshape(t, ...shape)`, `concat(list, axis)`,
`einsum(spec, ...tensors)`.

Indexing / batching: `gather(x, indices)` selects rows of `x` (its first axis)
by an index list or 1-D tensor, and is differentiable: the gradient scatters
back to the selected rows, so repeated indices (embedding lookups) accumulate.
`permutation(n)` returns a seeded random ordering of `0..n-1` (for shuffling),
and `int(x)` truncates a scalar toward zero.

Convolutions (differentiable): `conv2d(input, weight)` is a 2-D cross-correlation
with `input` shaped `[Cin, H, W]` and `weight` shaped `[Cout, Cin, KH, KW]`,
producing `[Cout, H-KH+1, W-KW+1]` (valid padding, unit stride).
`maxpool2d(input, k)` does non-overlapping `k×k` max pooling over each channel of
a `[C, H, W]` tensor. `grad` flows through both, so a convolutional net trains
like any other model. See `examples/cnn.tw`.

`einsum` is a general Einstein-summation contraction and is differentiable:

```rust
einsum("ij,jk->ik", A, B)   # matrix multiply
einsum("ij->ji", A)         # transpose
einsum("ij->i", A)          # sum over the second axis
einsum("i,ij,j->", x, W, y) # bilinear form x' W y
```

Each label names an axis; repeated labels across operands are summed, and only
the labels in the output remain. Omitting `->` keeps the labels that appear once.
(A label repeated within one operand, a trace or diagonal, is not supported
yet.)

Construction: `tensor(list)`, `scalar(x)`, `zeros(...shape)`, `ones(...shape)`,
`fill(value, ...shape)`, `eye(n)`, `linspace(start, stop, n)` (n points, both
endpoints included), `arange(start, stop, step)` (half-open, like `range` with a
float step), `randn(...shape)` (standard normal), `rand(...shape)` (uniform),
`seed(n)`. Shapes may be separate args or a list.
Randomness is **deterministic by default**: a program gives the same result
every run, and `seed(n)` chooses the starting point. That reproducibility
matters for model governance and audit.

Lists / higher-order: `range(...)`, `list(...)`, `map(f, xs)`, `zip(...)`,
`fold(f, init, xs)`, `append(xs, x)`, `enumerate(xs)`, `len(x)`.

### Sorting a list

```rust
sort(xs)                     # ascending, by the elements' own order
sort(xs, true)               # descending
sort(xs, fn(a, b) = a.n < b.n)   # by a comparison: does a come before b?
```

Two kinds of element have an order of their own: strings, by the byte
comparison above, and numbers, whether they arrived as `I64` or `F64`. Anything
else -- records, lists, an index into a second array -- needs the caller to say
what the order is, and a list that mixes strings with numbers says so rather
than picking one.

**Every form is stable.** Elements the comparison calls equal come back in the
order they went in. That is not a nicety: `skein` assigns token ids from a
sorted vocabulary, so an unstable sort would make the ids depend on the sort's
internals rather than on the corpus, and building the same vocabulary twice
would give two answers.

The comparison takes the two elements rather than a key, because the case that
needs it most is sorting one array by comparing through another:

```rust
let words: Arr[Str] = ["kilo", "a", "the", "be"]
let idx: Arr[I64] = [0, 1, 2, 3]
sort(idx, fn(x, y) = len(words[x]) < len(words[y]))   # [1, 3, 2, 0]
```

A comparison that is not a consistent order produces an order nobody wanted; it
cannot corrupt the list or fail the sort. `sort` returns a new list either way
and never reorders its argument.

Trees (tensors nested in lists/records): `map_leaves(f, tree)` applies `f` to
every tensor leaf; `zip_leaves(f, trees)` walks a list of same-shaped trees in
parallel, calling `f` with the list of leaves at each position. Optimizers use
these, so they work on any model structure.

Files and paths: `read_file(path)` and `write_file(path, text)` return a `Res`;
`path_exists`, `path_is_dir` and `mtime` ask about a path without reading it
(`mtime` is whole seconds, or -1 when the path cannot be read, the convention
`file_size` follows); `mkdir_all`, `remove_file`, `remove_dir`, `remove_all`,
`rename` and `temp_dir` change what is there, each returning a `Res`; `cwd()`
and `list_dir(path)` report. `remove_file` and `remove_dir` each refuse the
other's argument, because the recursive one is the dangerous one and a caller
should have to name it; `remove_all` is that name, and it succeeds on a path
that was already gone.

Programs: `run(program, argv, dir) -> Res[Str, Str]` starts a program, waits
for it, and answers with what it wrote to stdout -- `Ok` only when it exited 0,
and `Err` otherwise, carrying the program's name, how it ended, and its stderr.
`dir` follows the same rule as every other path here, and `""` means beside the
running program.

**There is no shell, and there will not be one.** The program and its arguments
stay separate values all the way to the operating system: nothing is parsed,
split on whitespace, or glob-expanded, so an argument containing `;` is one
argument. That is the property a package manager needs, because its arguments
are tags and URLs out of a manifest a stranger wrote, and it is the reason no
single-string convenience form exists.

The environment is inherited whole -- which is the point, since the reason to
shell out to `git` at all is to borrow the credentials, proxy and host keys the
user already has. Because that is a real widening of what running a `.tw` file
can do, setting `TWILL_NO_EXEC` to anything non-empty makes every `run` answer
`Err` without starting anything, so a caller degrades rather than dies.

`path_join`, `path_base`, `path_dir`, `path_ext`, `path_stem`,
`path_normalize` and `path_is_abs` are string handling and touch nothing. They
emit a forward slash on every platform -- a program's paths are written in its
source, and one that renders them differently on Windows writes a different
manifest there -- and they read a backslash as a separator, so a path handed
over by the operating system still splits.

Time: `mono_ns()` is a monotonic clock in nanoseconds, whose zero point is
arbitrary and whose differences are the only thing that means anything.
`clock_now_ms()` is the wall clock, which is what to print in a log and what
not to measure a duration with, since it steps when the system's time is
corrected.

Measurement: `black_box(x)` returns `x` and is the one place a program can
stand to say that a value was read. A benchmark throws its body's result away,
and a thrown-away result is work a compiler may delete; wrapping it makes the
value escape, so the work that produced it is done. It is the identity on every
type, in both modes, and it preserves shape, dtype and gradients, so inserting
one cannot change an answer -- only, at most, a timing. What it guarantees and
what it does not is `docs/CODEGEN.md` section 12, and the short version is that
it stops the elimination this implementation actually performs and is a promise
about the ones it does not perform yet. It is not `stop_grad`: a gradient runs
straight through a barrier.

Gradient checking: `std/gradcheck` compares `grad(f)` against a difference
quotient, for a tensor argument (`check_at`) or a whole parameter tree
(`check_tree`). It is a development tool, costing two evaluations of `f` per
element, and it is what to reach for when a model trains worse than it should.

Inspection: `shape(t)`, `item(t)`, `dtype(t)`, `str(x)`, `print(...)`. `dtype(t)`
returns the name of a tensor's dtype as a string, one of `"f64"`, `"f32"`,
`"bf16"`, `"f16"`, `"i32"`, `"i8"`, `"bool"`; it is the read-back the `to` cast
and the dtype constructors otherwise have no inverse for.

### `str` on a number

**`str(n)` for an `I64` is the digits and nothing else: no decimal point, no
trailing `.0`, no exponent, no thousands separator, no padding, and a leading
`-` exactly when the value is negative.** `str(0)` is `"0"`. `str(-7)` is
`"-7"`. The most negative `I64` renders in full as
`"-9223372036854775808"`, which is the one value whose negation is not
representable and therefore the one a digit loop gets wrong.

This is stated because `str` on a scalar goes through the tensor printer, and a
`.0` from that path would land in every line number, every column count and
every axis index in every diagnostic the self-hosted compiler emits. It is not a
formatting preference, it is the difference between `lex.tw:294:12` and
`lex.tw:294.0:12.0`.

`str(x)` for an `F64` prints a whole number with no decimal point and everything
else in Go's shortest round-tripping `%g`, which is what the bootstrap's
`internal/value.FormatNumber` does and what `src/float.tw`'s `format_number`
reimplements. The switch to exponent form happens at decimal exponent below -4
or at or above 6, so `1000000` prints as `1000000` and `1234567.5` prints as
`1.2345675e+06`.

The two rules meet where an `F64` holds an integral value, and `format_number`
routes that case to the `I64` renderer on purpose, so a whole number prints the
same however it was computed.

`str` does no padding and no alignment. `str(7)` is `"7"` and never `"  7"`.
Column alignment is a caller's job until a formatting builtin exists
(`docs/needs.md` NEEDS-20), and bobbin's `pad_left` and `pad_right` are what
that looks like meanwhile.

A caveat worth knowing before it is discovered: in **numeric mode** there is no
`I64`, so an integer is a float64 and `str` is exact only up to 2^53.
`str(123456789012345678)` prints `123456789012345680`. Systems mode is where an
integer is an integer, and this is one of the reasons it exists.

Data: `read_csv(path)` loads a file of numeric rows (comma- or
whitespace-separated, `#` lines skipped) into a `[rows, cols]` tensor.

Persistence: `save(value, path)` writes any value, whether a tensor, a record or list
of tensors (a model's whole parameter tree), or a fitted `gbm` model, to a file
in an exact binary format, and `load(path)` reads it back. Paths are relative to
the running script. This is the deploy path: train once, `save` the model, and
ship it with the single binary for inference (`examples/save_load.tw`).

Frames: a frame is a record whose fields are named column tensors, so field
access, slicing, and `grad` all work on it. `read_frame(path)` loads a CSV whose
first row is a header into such a record; `write_frame(frame, path)` writes one
back. `columns(rec)` lists the field names, `field(rec, name)` looks one up by
string, and `with_field(rec, name, value)` returns a copy with a field set. See
`examples/frames.tw`.

Gradient-boosted trees: `gbm_fit(X, y)` (or `gbm_fit(X, y, opts)`) trains a
native gradient-boosting model on a `[n, d]` feature matrix and an `[n]`
target/label vector, and `gbm_predict(model, X)` scores a `[n, d]` matrix into an
`[n]` vector. `opts` is a record of hyperparameters: `rounds`, `learning_rate`,
`max_depth`, `min_leaf`, `lambda`, `gamma`, and `objective` (`"squared"` for
regression, `"logistic"` for binary classification, where predictions are
probabilities). The engine is pure Go and deterministic. See `examples/gbm.tw`.

Libraries written in Twill ship inside the binary and are imported as
`std/<module>`: `std/nn` (layers including `dense`, `conv`, `embed`, and
`self_attention`; activations, initializers, losses), `std/optim` (SGD,
momentum, Adam), `std/data` (`standardize`, `train_test_split`, `shuffle` for
real training loops, see `examples/minibatch.tw`), and `std/backtest`
(returns, moving averages, equity curves, drawdown, Sharpe, Sortino, volatility,
CAGR). Their sources are the `.tw` files in `std/` in the repository. The
optimizers are container-agnostic: the same `sgd_step`/`adam_step`
update a model held in a positional list or a named record. The backtest Sharpe
and Sortino are differentiable in the return series, so a smooth signal can be
tuned by gradient ascent through the backtest (`examples/signal_opt.tw`).

## Example

```rust
# Fit y = X w + b by gradient descent.
let X = [[1.0, 1.0], [2.0, 1.0], [1.0, 3.0]]
let y = [-0.5, 1.5, -6.5]

fn loss(w, b) {
  let err = X @ w + b - y
  mean(err * err)
}

let w = [0.0, 0.0]
let b = 0.0
for step in range(400) {
  let g = grads(loss)(w, b)
  w = w - g[0] * 0.05
  b = b - g[1] * 0.05
}
print("w =", w, "b =", b)
```
