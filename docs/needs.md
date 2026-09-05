# What the self-hosted compiler needs from the language

The source under `src/` is twill's compiler written in twill. It runs. Since
v1.5.0 it runs on the Go bootstrap and is checked against it by
`internal/interp/selfhost_test.go`, which pins the two implementations to the
same diagnostics on the same inputs, and 1.6 added the systems-mode types the
two now agree about. The opening sentence of this file used to say the opposite,
and said it for months after it had stopped being true.

So what this file is has changed. It began as the list of reasons `src/` could
not run, and its oldest entries are therefore done: NEEDS-1 through NEEDS-24 are
almost entirely closed, and reading from the top now gives a history rather than
a queue. What remains is a backlog, and it is a mixed one. Some entries are open
language questions that nobody has answered (user-defined generics, NEEDS-4).
Some are measured costs that a workaround absorbs (NEEDS-79, NEEDS-81). Some are
deferred on the project's own evidence rather than forgotten (the GPU chain,
NEEDS-100 through NEEDS-108). The two sections at the end of the file, "What 1.6
closed" and "What is still open after 1.6", are the short version.

Each entry says what the feature is, which file and line reaches for it, and
what the Go bootstrap does in the same place. That framing is inherited and it
is now slightly wrong in one direction: for a growing number of entries the
bootstrap is the thing that gained the feature, so "what the Go side does" is
the answer rather than the gap.

Ordering is by dependency, not by priority. The `NEEDS-n` ids are referenced
from comments in `src/`.

Status key. **done** means it works and was verified against the binary or the
tests, with the date it landed. **blocking** no longer means "no file in `src/`
parses without it", because they all parse; it now means the compiler cannot do
its job correctly without it. **open** is wanted and not started. **not
blocking** is a recorded cost with a workaround in place. **superseded**,
**answered** and **deferred** mean what they say, and an entry with any of them
is kept rather than deleted, because a reader who finds the old advice needs to
be told it is old. NEEDS-87 is the model for that: it exists only to say that
NEEDS-29 is answered and that its advice will send you to write the wrong
function.

Every status line below was re-verified against `twill16.exe` in 2026-08. Where
an entry's advice is now wrong, it says so in place rather than being edited to
look as though it had always been right.

An id is permanent once assigned. Three agents appended concurrently and
collided on 68 through 72; the collision was resolved by moving one set, and
every comment in `src/` that named an id had to be re-read to find out which
entry it meant. A new entry takes the next number above the highest one in this
file and never reuses one, so a `NEEDS-n` in a comment means the same thing a
month later as it did when it was written.

---

## NEEDS-1: `mode systems` as a file-level declaration

**Status:** done for what it asks (2026-08). The declaration is recognised and
preserved on both sides (2026-08-09), and since 1.6 it selects real dialect
semantics rather than one advisory rule: in a systems-mode file the type
annotations are checked (NEEDS-49), `enum` and `match` are checked for
exhaustiveness (NEEDS-3), and `?` is checked against the enclosing signature
(NEEDS-10). What is left is the other direction, refusing systems constructs in
numeric mode, which nothing has needed yet.

The gate from `docs/self-hosting.md` section 1.1. The first non-comment line of
a file is `mode systems`; the default with no declaration is numeric mode and is
unchanged.

**Done.** `mode` is a bare identifier (not a keyword), recognised only as a
leading `mode <name>` and only when an identifier follows, so it stays usable as
an ordinary name everywhere else. It is recorded on the program rather than
parsed as a statement, and the formatter re-emits it, set off from the body by a
blank line. A systems-mode file built from features the bootstrap already has
now parses and runs instead of failing on its first line, and `twill fmt`
round-trips the mode line. This landed in the Go bootstrap
(`internal/parser/parser.go` `parseProgram`, `ast.Program.Mode`,
`internal/format/format.go`) and in the self-hosted tree in lockstep
(`src/parse.tw` `parse_program`, `ast.Program.mode`, `src/fmt.tw`), with tests
in `internal/interp/mode_test.go`.

**The mode now gates one thing (2026-08-09):** type-name resolution. In a
systems-mode file an unresolved type annotation (`n: I64`, `-> Bool`,
`c: cp.Caps`) is advisory rather than an "unknown type" error, since the
bootstrap has no such type; numeric mode is unchanged, and the unit algebra is
untouched in both. This is the first behaviour that actually depends on the mode
line, and it is what lets the self-hosted sources pass `twill check`. The rest
of the gating (refusing systems constructs in numeric mode, enabling them in
systems mode) and the constructs themselves (NEEDS-2 onward) remain.

*Progress toward the type grammar (2026-08-09):* module-qualified type names in
signatures (`fn f(c: cp.Caps) -> cp.Caps`) now parse on both sides, the single
most pervasive systems-mode construct. A `.` after a bare type name is consumed
as a qualified suffix; a qualified return is held in an advisory `RetType`. The
names are not yet resolved across modules (an unknown one is tolerated as
`tUnknown`, not checked), but the signatures no longer fail to parse. Still
unparsed: `enum`/`match`, `Res`/`Opt` with postfix `?`, generics (`Arr[T]`),
typed record literals, and leading-`and`/`or` line continuation.

## NEEDS-2: `I64` with bitwise operators

**Status:** done (2026-08, 1.6). `value.Int` is a real int64 and an `I64`
annotation on a `let`, a parameter, a return, a struct field or an enum payload
converts to it, as does `i64()`. An integer literal above 2^53 is exact
(`9007199254740993` prints itself), `Int op Int` wraps, and the bitwise builtins
are exact on all 64 bits including the sign bit, so `shl(i64(1), 63)` is
`-9223372036854775808` and round-trips. The paragraph below that says values are
float64 is the record of what was true before 1.6; it is no longer. What it
predicted is what happened: the exactness had to come first, and NEEDS-84,
NEEDS-19, NEEDS-24 and NEEDS-85 all closed or narrowed on the back of it.

*(Original status: the six operators are spelled and work (2026-08-09); a true
64-bit `I64` distinct from float64 remains.)* The bitwise builtins `and`, `or`, `xor`,
`shl`, `shr`, `bnot` operate on scalar integers, with `shr` arithmetic and shift
counts masked to 0..63. `and`/`or` share the boolean keywords' spelling but are
the bitwise builtins when called (`and(x, y)`); a line that begins `and(`/`or(`
is a new call-statement, not a boolean continuation; and bitwise-not is spelled
`bnot`, since `not` stays the boolean prefix operator (owner's call, 2026-08-09).
Landed in the Go bootstrap (parser + `internal/interp/builtins.go` + checker) and
mirrored in the self-hosted parser, checker and builtin registry;
`internal/interp/bitwise_test.go` covers the values, the preserved boolean infix,
and the line-start disambiguation. **Still missing:** values are float64, so a
full 64-bit pattern such as a sign bit (`shl(1, 63)`) is not exactly
representable and round-trips lossily above 2^53. That needs a real I64 storage
type, which is the rest of this entry and what the self-hosted eval runtime for
these builtins is waiting on.

A signed 64-bit integer distinct from float64, with `and or xor shl shr not`,
defined wrapping, and explicit `i64()` / `f64()` conversions only. The exact
semantics of those six, including that `shr` is arithmetic and that shift counts
are masked to 0..63, are specified in `docs/language-guide.md`, Operators →
Bitwise operators on `I64`; see NEEDS-85 for why they are specified there.
`src/lex.tw:131` (`is_utf8_continuation`) and `src/lex.tw:498` (`utf8_width`)
mask lead bytes with `and`, which is the whole reason the subset needs an
integer type rather than a float that happens to hold integers.

*Go bootstrap:* every numeric value is `*tensor.Tensor`. `internal/interp` has
no integer type and no bitwise operator; `builtins.go` `int` truncates a float
and returns a float.

## NEEDS-3: `enum` with payloads and exhaustive `match`

**Status:** done (2026-08, 1.7), on both implementations. 1.6 did the
exhaustiveness half; 1.7 did the pattern language, which is what the paragraph
below used to say was missing.

An `enum` with payloads is declared, constructed and matched, and the checker
reports a missing variant by name, a duplicate arm, an arm after a catch-all, a
catch-all that covers nothing, and arms that mix two enums. A pattern is now a
tree: nested patterns (`Ok(Some(v))`), literal patterns (`3`, `"hi"`, `true`,
`-1`) and guards (`Some(n) if n > 0`) all parse, check and run. A lower-case
name binds instead of naming a case, so a catch-all can say what it caught; an
upper-case name is a case, which is the rule that tells the two apart.

Exhaustiveness recurses to match: `Some(Ok(v))`, `Some(Err(e))` and `None`
cover an `Opt[Res[..]]`, and dropping one names what gets through
(`missing Some(Err)`). An arm counts only when nothing but the value's shape
decides whether it runs, so a guarded arm and a narrower nested one prove
nothing -- which is stricter than 1.6 was, and correct, since neither of those
covers the case it appears to.

*Previously, and now wrong:* "a pattern may only be a variant name with optional
binders. A literal pattern is a syntax error, a nested pattern is a syntax
error, and there are no guards. Every one of those is what a compiler written in
twill wants next."

Tagged unions, matched by pattern, exhaustiveness enforced as an error. The AST
is forty variants and the token kind is seven; `src/lex.tw:29` spells the token
kinds as `I64` constants because milestone 1 excludes sum types, and the
`kind_name` ladder immediately below it is what that costs. `src/ast.tw` does
not attempt the workaround: an AST as integer tags plus parallel arrays is not
a design, it is a transcription error waiting to happen.

*Go bootstrap:* Go interfaces plus a type switch. `internal/ast/ast.go` declares
`Node`, `Stmt` and `Expr` as interfaces with unexported marker methods, and
every consumer type-switches. There is no exhaustiveness check anywhere, which
is precisely the property the twill version is meant to gain.

## NEEDS-4: generics: `Arr[T]`, `Dict[K,V]`, `Opt[T]`, `Res[T,E]`

**Status:** done (2026-08, 1.7), on both implementations. This entry was the
largest open language question in the file for months, and the paragraph below
it is what it said while it was open, kept because a reader who finds the old
advice needs to be told it is old.

1.6 turned `Arr[T]`, `Dict[K, V]`, `Opt[T]` and `Res[T, E]` from tolerated text
into types the checker enforces. 1.7 lets a program declare its own:
`struct Box[T]`, `enum Tree[T]` and `fn first[T](xs: Arr[T]) -> T` all parse,
check and run.

**Monomorphization was not needed and is not there.** That is the part of this
entry's original advice that turned out to be wrong, and it is worth writing
down why. The runtime is a tree walker over dynamically typed values: the same
code runs whatever T is, and specialising it per instantiation would produce
identical copies. So the parameters have to reach exactly one place, the types
the checker judges against, and generics here are substitution and nothing else
-- `substParams` in `internal/checker/systems.go` and `subst_params` in
`src/check.tw`, about eighty lines each. NEEDS-90's termination worry, which
existed because monomorphization was assumed, therefore does not arise.

What is checked: a `Box[I64]`'s field is an I64; a `Tree[Str]` matched with
`Leaf(v)` binds v as a Str; substitution goes under the constructors a parameter
is written inside, so a payload declared `Arr[T]` in a `Tree[I64]` is an
`Arr[I64]`; and two uses of one declaration are different types when their
arguments differ. Arguments are inferred from the value rather than written at
the use site, since there is nowhere to write them. An unbound parameter judges
nothing, so a bare `Box` says what it said before 1.7. No bounds and no traits,
as this entry always said.

*(Original status: generic type annotations parse and check (2026-08-09);
generic type declarations and real monomorphization remain.)*

Type parameters on structs, enums and functions. No bounds, no traits,
monomorphized. Used on nearly every line of `src/`; `src/lex.tw:198` (`Lexer`
holds `Arr[Token]` and `Arr[Comment]`) is the first.

**Done.** A generic type name in an annotation position (`xs: Arr[I64]`,
`-> Res[I64, Str]`, `let d: Dict[Str, Arr[I64]]`) now parses: a `[` after a bare
type name opens a generic argument list, each argument itself a full type
reference, so nesting (`Arr[Arr[I64]]`) and qualified arguments
(`Dict[Str, ast.Expr]`) work. The whole thing is kept as advisory text, since the
bootstrap has no such type, and `twill fmt` round-trips it. `let` gained a
`TypeName` for this, and a systems-mode generic parameter is left `tUnknown`
rather than pinned to the argument, so indexing an `Arr` param is not a false
error. This unblocked `std/text.tw` (now round-trips) and moved float/random/
linalg/stats past their generic lines to the next feature (field assignment).
Landed in the Go bootstrap and mirrored in the self-hosted parser, checker and
formatter; tests in `internal/interp/generics_test.go`.

**Remaining.** Declaring a generic (`struct Box[T] { … }`, `enum Opt[T] { … }`),
and any actual type-parameter checking or monomorphization: the annotations are
tolerated, not understood.

*Go bootstrap:* Go generics for none of it. `internal/value` has `List` as
`[]Value` (heterogeneous) and `Record` as ordered string keys, and the checker
models them as `tList` and `tRecord` with no element type.

## NEEDS-5: `struct`: nominal, mutable, reference semantics

**Status:** done (2026-08). Structs are nominal, their fields are mutable in
place, and a struct is passed by handle: a function that assigns to `p.x`
through its parameter is seen by the caller, and so is an assignment through a
field of another struct (`o.i.n = ...` on an `Outer` holding an `Inner`), which
is the case NEEDS-42 added and which is verified. `Record` is still the separate
value it had to stay. `src/lex.tw:198` `Lexer` is a cursor that advances and
now advances. **The
normative text is `docs/language-guide.md`, `struct`, and what a parameter is.**
It states reference semantics for `struct`, `Arr`, `Dict` and `Bytes` together,
including mutation through a field of another struct, and it says that copying
is always explicit. `docs/design.md`, Two modes, and where they disagree, has
the reason the rule is stated rather than left to the implementation.

Fields typed, mutable in place, passed by handle. `advance(lx)` at
`src/lex.tw:240` mutates `lx.i`, `lx.line` and `lx.col` and the caller sees it.
Threading those three through every scan function and returning updated copies
would roughly double the lexer and make its diffs unreadable.

Must stay a distinct type from `Record`: `grad` over a record depends on records
not aliasing (`docs/self-hosting.md` section 1.2).

*Go bootstrap:* `value.Record` exists but is value-ish and has no declared
mutability. `interp` supports record field assignment only through rebinding.

## NEEDS-6: indexable, sliceable `Str`

**Status:** done (2026-08). `s[i]` is the byte value as an `I64`, `s[a:b]` is a
`Str`, `len(s)` is the byte length, and `+` concatenates (NEEDS-35). The
paragraph below saying a lexer written in twill cannot read its first character
is the record of what was true; it can.

`s[i]` yields the `I64` byte value, `s[a:b]` yields a `Str` copy, `len(s)` is
the byte length. `src/lex.tw` is built on this: `scan_ident` at
`src/lex.tw:378` returns `lx.src[start:lx.i]` rather than accumulating, which
is both faster and the only way the token value is guaranteed byte-identical to
the source span.

*Go bootstrap:* `value.Str` is a Go string with no index, no slice, no length
and no concatenation. A lexer written in twill today cannot read its first
character.

## NEEDS-7: `Bytes`: a growable byte buffer

**Status:** done (2026-08). `bytes_new`, `bytes_push` and `bytes_to_str` are
builtins and `src/bytes.tw` runs on them.

`bytes_new`, `bytes_push`, `bytes_to_str`. Everything the compiler prints is
built by appending. `src/bytes.tw:41` (`concat`) exists so that the rest of the
compiler never builds a string by repeated `+`, which is quadratic.

*Go bootstrap:* `strings.Builder`, used exactly this way in
`internal/lexer/lexer.go` and `internal/format/format.go`.

## NEEDS-8: `Dict[Str, V]` with insertion-ordered iteration

**Status:** done (2026-08). `dict_new`, `dict_set`, `dict_get` returning an
`Opt`, `dict_has`, `dict_del`, `dict_keys` and `len` are all builtins, and
`dict_keys` returns insertion order: setting `z`, `a`, `m` gives `[z, a, m]`,
not a sorted or a randomized order. The reason insertion order is load-bearing,
below, is unchanged and is why this was checked rather than assumed.

`dict_new`, `dict_set`, `dict_get` returning `Opt[V]`, `dict_has`, `dict_del`,
`len`, and iteration in insertion order. `src/lex.tw:149` (`keyword_table`) is
the smallest use; `src/check.tw` uses it for the environment, the declared
types and the unit table.

Insertion order is not a nicety. `docs/self-hosting.md` section 2.4 makes
`compilerB == compilerC` the bootstrap check, and a symbol table iterated in a
random order emits different output on every run.

*Go bootstrap:* Go maps, whose iteration order is deliberately randomized.
`internal/checker/checker.go` gets away with it because it sorts before
printing (`unitString` calls `sort.Strings`) and because `Record` carries its
own `Keys` slice for exactly this reason.

## NEEDS-9: a byte literal

**Status:** not blocking, and unchanged (re-checked 2026-08). There is still no
`b'x'` literal and `ch(s) = s[0]` is still the idiom. The workaround is honest
and the cost is paid once, so nothing about the argument below has moved.

There is no character literal, so `src/lex.tw` defines `ch(s) = s[0]` and writes
`let C_QUOTE = ch("\"")`. That is readable and it is a runtime call and a
one-byte allocation per constant at module load. A `b'x'` literal folding to a
constant would remove both. Low priority: the workaround is honest and the cost
is paid once.

*Go bootstrap:* Go rune literals, `'\n'` and friends, throughout the lexer.

## NEEDS-10: `Res[T, E]` and postfix `?`

**Status:** done (2026-08, 1.6), with one sharp gap that is worth more than the
rest of this entry. `Res` and `Opt` are types, `?` propagates, and the checker
reports the three misuses: `?` outside a function ("there is nothing to return
the failure from"), `?` in a function whose return type is not a `Res` or an
`Opt`, and `?` on a value that is neither. `?` yields the success payload's
type. At the top level of a file a failing `?` is now a diagnostic instead of a
silent exit 0, which is the change most likely to break someone's script and the
one most worth having.

**A gap found by probe while auditing this file, and closed before the release.**
The checker was not propagating a *block-bodied* function's declared return type
to its call sites: it typed the call `Unit`, because a block ending in `return`
evaluates to `Unit` as an expression and the call took the body's type rather
than the signature's. So

    fn g(n: I64) -> Res[I64, Str] { return Ok(n) }
    fn h(n: I64) -> Res[I64, Str] { let v = g(n)?
      return Ok(v) }

was rejected with ``` `?` needs a Res or an Opt, but the value is Unit ``` while
running correctly under `--no-check`, and `let c: Str = s(...)` was a false
error for any block-bodied `s` (NEEDS-49, NEEDS-82). A declared return type is
the contract and is now what the call produces, with the body checked against it
separately. It was worth catching: `?` on a user-defined function is the most
common shape this feature has and it was precisely the shape refused.

`src/lex.tw:294` (`tokenize`) is `Res[Arr[Token], SyntaxError]` and uses `?` to
propagate. Without it every call site of every scanner function grows an
explicit error check, and the checker in `src/check.tw`, which threads
diagnostics through about ninety functions, becomes unreadable.

The enclosing function must itself return a `Res`, checked statically.

*Go bootstrap:* Go `error` returns and `if err != nil`. `lexer.SyntaxError` is
the error value; `src/lex.tw:67` is its twill equivalent and matches its
`Error()` rendering exactly, so a message printed by either implementation is
the same string.

## NEEDS-11: `abort(msg)`

**Status:** done (2026-08). `abort(msg)` is a builtin in both modes and stops
the program with `runtime error: abort: <msg>` and the source line. The review
rule below, that every `abort` in `src/` should be unreachable for any input, is
the part of this entry that still has work in it, and it is a review rule rather
than a feature.

A panic that means a compiler bug, never a user error. Every `abort` in `src/`
should be unreachable for any input; that is a review rule and it is worth
keeping.

*Go bootstrap:* Go `panic`, used sparingly; most impossible cases in
`internal/interp` return an error instead, which conflates them with user
errors.

## NEEDS-12: `continue` in `while` and `for`

**Status:** done (2026-08-11). **The normative text is
`docs/language-guide.md`, Control flow → `break` and `continue`**, which covers
`break` as well: innermost loop only, no labels, statements rather than
expressions, no crossing a function boundary, and keywords in systems mode only
so that a numeric-mode `let break = 3` keeps working.

`break` and `continue` lex as keywords, parse to `ast.Break`/`ast.Continue`
statements, format, and run: the interpreter unwinds each to its enclosing loop
with a `breakSignal`/`continueSignal` that `runLoopBody` recovers, the same way
`returnSignal` unwinds a function (`internal/interp/interp.go`). The scanner loop
in `src/lex.tw` that this was blocking is a chain of `continue`s and reads as one.

**The last piece landed the checker rule (2026-08-11):** the checker tracks a
lexical loop depth, reset to zero at every function boundary, and reports
`break`/`continue` outside a loop -- including one inside a `fn` nested in a
loop, which cannot cross the boundary. Before this a stray `break` passed
`twill check` and reached the interpreter as an uncaught signal. Both
implementations carry it: `internal/checker/checker.go` (`loopDepth`, the
`While`/`For` and `Break`/`Continue` cases, and the two body walks
`checkFnDef`/`inferUserCall`) and `src/check.tw` in lockstep (`Checker.loop_depth`,
`infer_stmt`'s loop and control cases, and `infer_fn_body`), with tests in
`internal/checker/loopctl_test.go` and `internal/interp/selfhost_test.go` that
pin the two to the same diagnostics.

*Go bootstrap:* Go `continue`, `internal/interp/interp.go`.

## NEEDS-13: `Unit` as a value, and `unit` as its literal

**Status:** done (2026-08). `unit` is a literal, `Res[Unit, Str]` works, and
`Ok(unit)` prints as `Ok(())`. `src/lex.tw:421` `scan_string` has something to
put in the `Ok`.

`scan_string` returns `Res[Unit, SyntaxError]`, so it needs something to put in
the `Ok`. The checker already has a `tUnit` type; the value side is missing.

*Go bootstrap:* `value.Unit`, which exists (`internal/value/value.go`) but has
no source syntax.

## NEEDS-14: a `Bool` type name in annotations

**Status:** done (2026-08, 1.6). `Bool` is a type name anywhere a type is,
including the struct field `trailing: Bool` this entry was filed for, and it is
checked: a `Bool` field given a `Str` is a diagnostic. There is no conversion to
or from `I64` in either direction, as specified. **The normative text is
`docs/language-guide.md`, Systems-mode types → `Bool`.**
`Bool` is a type name in systems mode, legal anywhere a type is, with no
conversion to or from `I64` in either direction.

`src/lex.tw:61` annotates a struct field `trailing: Bool`. The parser currently
reads a bare name after `:` as a record type or a unit
(`internal/parser/parser.go` `parseParam`), so `Bool` would be resolved as a
unit and reported as undeclared.

*Go bootstrap:* `checker.tBool` exists; there is no way to write it.

## NEEDS-15: lexer divergence: non-ASCII whitespace in comments

**Status:** known, accepted divergence, **confirmed by differential test**.
`src/lex.tw:466` (`is_space`).

Go's `strings.TrimSpace` trims a Unicode space set that includes U+0085 and
U+00A0. `src/lex.tw` trims the ASCII members only, because matching the rest
needs a UTF-8 decoder in the scanner for a case that cannot occur in a comment
without the file already being unusual. If the differential harness ever trips
on it, the fix is a decoder in `trim_space`, not in the scanner.

---

## Parser

## NEEDS-16: recursive enum payloads without explicit indirection

**Status:** done (2026-08). Verified both shapes: a struct whose field is an
`Opt` of the struct itself, and an enum whose payload is an `Arr` of the enum
(`enum J { JNum(F64), JArr(Arr[J]) }` constructs, prints and matches). No `Box`
was needed, which is what the entry asked to be true rather than merely
intended. NEEDS-90 is the same property asked from `std/json.tw`.

`Expr` holds `Expr`. The subset's answer is that an enum payload is a struct and
a struct is a handle, so the recursion needs no `Box`. That has to actually be
true in the implementation, including for a payload that is the enum itself
(`Unary { operand: Expr }`).

*Go bootstrap:* interfaces are already references.

## NEEDS-17: a growable `Arr` with `pop` and `set`

**Status:** done (2026-08). `set` via indexed assignment landed 2026-08-09
(`xs[i] = v` mutates a list element in place, and `obj.f = v` a record field, on
both sides). `pop(xs)` is now a builtin: it removes the last element and returns
it. The non-quadratic growth is `push(xs, x)`, which mutates in place; `append`
still returns a new list and is still quadratic in a loop, and that is now a
choice between two builtins rather than a missing one.

`append(xs, x)` returns a new list today, so building an n-element list is
quadratic. `docs/self-hosting.md` section 1.2 makes the same argument for the
token stream.

*Go bootstrap:* Go slices with `append`.

## NEEDS-18: `f64_of_str` / `str_to_f64`

**Status:** done (2026-08), under the name `str_to_f64`. It calls Go's
`strconv.ParseFloat` directly, so the bit-exact agreement this entry is entirely
about is by construction rather than by reimplementation, and
`std/float.f64_of_str` delegates to it on the bootstrap for the reason given in
`internal/interp/builtins.go`: the pure-twill decimal parser needs exact 64-bit
integers to assemble an IEEE pattern. One deliberate divergence from
`strconv.ParseFloat`, documented at the definition: underscores are stripped
before the parse, so `str_to_f64("_1.0")` is `Some(1)` where Go's own function
is an error. That matches twill's own numeric literals, and it is written down
here because a reader of this entry would otherwise expect `strconv`'s exact
acceptance set. See NEEDS-60, which is the caller that cares.

The parser turns a NUMBER token's text into a float and must produce bit-exact
agreement with Go's `strconv.ParseFloat`, or two implementations disagree on
`0.1` and every downstream canonical dump differs. Correct decimal-to-binary
rounding is not something to reimplement in twill: this must be a runtime
primitive that calls the same conversion the Go side does.

*Go bootstrap:* `strconv.ParseFloat(t.Value, 64)` in
`internal/parser/parser.go` `parsePrimary`.

## NEEDS-19: `i64_of_str`

**Status:** done (2026-08, 1.6). `i64_of_str` returns `Opt[I64]` and the `I64`
is exact: `i64_of_str("123456789012345678")` is `Some(123456789012345678)`, not
a rounded float, which is what real `I64` (NEEDS-2) bought. A failure is `None`,
so the two implementations fail on the same inputs by returning the same
option.

The integer equivalent, matching `strconv.Atoi` including its overflow
behaviour, since `parseDim` reports "shape dimension must be a non-negative
integer" on a failure and the two implementations must fail on the same inputs.

*Go bootstrap:* `strconv.Atoi`.

## NEEDS-20: string formatting

**Status:** done in substance (2026-08), by pieces rather than by a format
verb. `str(n)` renders an `I64` as digits and an `F64` the way
`internal/value.FormatNumber` does, `str_quote(s)` is Go's `%q`, `f64_to_str` is
`strconv.FormatFloat(x, 'g', -1, 64)`, and `+` concatenates (NEEDS-35), so a
diagnostic is built by joining rather than by a template. There is no `Sprintf`
and no format string, and none is planned: the entry asked for the `%d`, `%s`
and `%q` *equivalents*, which is what these are. `src/lex.tw:478` `quote_char`'s
remaining narrowness is NEEDS-32.

`internal/checker` builds messages with `fmt.Sprintf` and the twill versions
must produce byte-identical strings, because the diagnostics are compared. The
subset needs at minimum the `%d`, `%s` and `%q` equivalents. `src/bytes.tw`
supplies the joining; what is missing is the rendering of a float the way Go's
`%g` does inside `str()`, and the Go-compatible quoting of a string, which
`src/lex.tw:478` (`quote_char`) approximates for one message only.

*Go bootstrap:* `fmt.Sprintf` throughout.

---

## Checker

## NEEDS-21: identity of a heap value (`is_same`)

**Status:** done, by both routes (2026-08). `is_same` is a builtin and compares
identity: `is_same(arr(1), arr(1))` is `false` on two structurally equal
arrays. `src/check.tw` still takes the node-id route described below, and the
argument for it is unchanged, so the two coexist rather than one replacing the
other. What is still missing is the thing NEEDS-81 asks for, which is a `Dict`
*keyed* by that identity rather than a linear scan calling `is_same`.

`internal/checker/checker.go` keys its recursion guard on `map[ast.Node]bool`,
that is, on the *identity* of an AST node, not its contents. Two structurally
identical lambdas in one file are different nodes and must not share a guard
entry. The twill version needs either pointer identity as a `Dict` key or a
unique node id assigned at parse time.

`src/check.tw` takes the second route and gives every AST node an `id: I64` at
construction, because identity-as-a-map-key is a language feature with
consequences (it leaks the collector's behaviour into the semantics) and a
serial number is not. The cost is one field on every node, and it also gives
the canonical dump a stable name for a node, so it is not purely a workaround.

*Go bootstrap:* pointer keys in a Go map.

## NEEDS-22: `Opt[T]` returned from `Dict` lookup, and `match` on it

**Status:** done (2026-08). `dict_get` returns `Opt[V]` and
`match dict_get(d, k) { Some(v) => ..., None => ... }` is the working form.

Go's `v, ok := m[k]` is two returns; twill has one. `Opt` is the whole reason
`Res`/`Opt` are in the subset.

*Go bootstrap:* the comma-ok form.

## NEEDS-23: sorting a `Arr[Str]`

**Status:** done (2026-08-11). The `sort` builtin now accepts a list of strings
and orders it bytewise, in both implementations. **The normative text is
`docs/language-guide.md`, Strings → Ordering**, which pins bytewise-unsigned
lexicographic order, shorter-is-smaller on a shared prefix, so that the eleven
hand-written sorts in the ecosystem agree with `sort.Strings` and with each
other. It also records that `<` stays undefined on `Str`, which is why
`str_greater` is a function.

`sort([...strings])` returns a new list ordered ascending, or descending with a
truthy second argument; a list has no axis, so the flag takes the axis slot the
tensor form uses. `argsort` on a list is refused (positions into a string list
have no defined meaning), a non-string element is an error, and the input list is
left untouched. The bootstrap sorts with `sort.Strings`
(`internal/interp/builtins.go` `sortStringList`); the self-hosted evaluator
reuses its own `canon_sort`, the bytewise insertion sort it already had for
record keys (`src/eval.tw` `sort_string_list`). Tests pin the ordering
(`internal/interp/sortstr_test.go`) and the cross-implementation agreement
(`internal/interp/selfhost_test.go`). The hand-written sorts can now delegate,
though `check.tw`'s `unit_string` insertion sort is left in place since it also
sorts fine.

`internal/checker/checker.go` `unitString` calls `sort.Strings` on the unit's
base names before joining, so that `USD*year^-1` renders the same regardless of
map order. The twill version must sort identically (bytewise ascending) or unit
diagnostics differ between implementations.

`src/check.tw` implements an insertion sort rather than asking for a builtin:
the lists are two or three elements, and a `sort` builtin over `Arr[Str]` is a
comparator question that the subset does not need to answer yet.

*Go bootstrap:* `sort.Strings`.

## NEEDS-24: integer division and modulo on `I64`

**Status:** done (2026-08, 1.6), and NEEDS-44 is this entry filed twice, now
also done. Verified against the binary: `/` and `//` truncate toward zero, `%`
takes the sign of the dividend, and division or modulo by zero is a runtime
error naming itself (`integer division by zero`, `integer modulo by zero`)
rather than a `Res` or a silent infinity. Note the boundary the entry does not
mention and a caller trips over: these are the `Int` operators, so `-7 / 2` on
two float literals is still `-3.5` and only `i64(-7) / i64(2)` is `-3`. `src/check.tw`
`unit_sqrt` (`v % 2`, `v / 2`) works. **The normative text is
`docs/language-guide.md`, Operators → Integer division and modulo on `I64`**,
and NEEDS-44 is the same entry filed twice. `/` truncates toward zero, `%` takes
the sign of the dividend, `MIN_I64 / -1` wraps to `MIN_I64`, and a zero divisor
aborts with a diagnostic rather than returning a `Res`.

Float division would give `1.5` where the checker needs a failure. Defined
behaviour on division by zero as an error value, per
`docs/self-hosting.md` section 1.2.

*Go bootstrap:* Go's `/` and `%` on `int`.

---

## Evaluator and tensors

## NEEDS-25: a foreign call into the native tensor core

**Status: superseded by NEEDS-68. Not blocking; the thing it asks to call does
not exist.** This entry asks for a calling convention into "the native tensor
core", which meant the Go bootstrap's `internal/tensor`. Under the no-Go rule
there is no such core to call: NEEDS-68 opens by stating that there is no
bootstrap to call into, which is why the transcendental primitives have to be
native rather than foreign. A foreign-call convention into a runtime that will
not exist is not a language feature that is missing, it is a question that was
overtaken.

What survives is a real question with a different shape: which primitives the
eventual runtime must provide, and how the checker types them. NEEDS-68 asks
exactly that for the transcendentals, and is the entry to extend. The text below
is kept for section 2.2's line between what reads twill source and what executes
it, which still holds.

*(Original text follows.)*

**Status:** blocking for `src/eval.tw`, and by design.

`docs/self-hosting.md` section 2.2 draws the line: everything that reads twill
source is twill, everything that executes it is not. `src/tensor.tw` therefore
describes the tensor semantics and the autodiff tape, and calls primitives the
native core provides for the actual arithmetic. What is missing is the calling
convention: how a twill function names a core primitive, and what the checker
believes about its type.

Without a decision here, `src/tensor.tw` is a specification of behaviour with no
route to being executed, which is what it currently is.

*Go bootstrap:* `internal/interp/builtins.go` dispatches on a name into Go
functions in `internal/tensor`.

## NEEDS-26: closures capturing a mutable environment

**Status:** answered, and the answer is by handle (2026-08). A closure over a
file-level `let` assigns to the binding it captured: two calls to a counter
lambda give 1 then 2, and the outer `let` reads 2 afterwards. That is
`interp.Env`'s parent-walking assignment, which is what the entry says the
evaluator needs to reproduce, and it now has a running implementation to
reproduce rather than a description.

`internal/interp` closes over a `*Env` with a parent pointer and assignment
walks up to the defining scope. Twill closures exist; what is unspecified is
whether a captured variable is captured by handle or by value, and the
evaluator needs by handle to reproduce the bootstrap's behaviour for
`for i in ... { fns = append(fns, fn() = i) }`.

*Go bootstrap:* `interp.Env` with `assign` walking parents
(`internal/interp/interp.go`).

## NEEDS-27: deep equality with the "different types are never equal" rule

**Status:** done (2026-08, 1.6). The different-types-are-never-equal rule
holds, `I64` compares by bits, and 1.6 fixed the case that was quietly wrong:
enum values compare structurally, so `Some(1) == Some(1)` is `true` where it had
been `false`. A payload-free case compares by case (NEEDS-70), so `Red == Red`
is `true` and `Red == Green` is `false`.

The bootstrap's rule, tested in `internal/interp/equality_test.go`: values of
different types compare unequal rather than raising. The twill version must
match it including the new subset types (I64 to I64 by bits, never equal to a
tensor).

*Go bootstrap:* `interp.valuesEqual`.

## NEEDS-28: `read_file`, `write_file`, `args`, `exit`, `write_out`

**Status:** done for the file and process surface (2026-08). `read_file`,
`write_file`, `args`, `exit`, `write_out` and `write_err` are all builtins and
all work, and 1.6 added the rest of the filesystem around them (NEEDS-91,
NEEDS-92). `src/main.tw` is a CLI and can exist.

Two things this entry lists that are still missing, so that "done" is not read
wider than it is: there is no `stdin_all` and no `read_line` (NEEDS-47), and
there is no way to start another process. The warning below, that this is the
only entry which widens what an arbitrary `.tw` file can do, was landed on
knowingly and is now a property of the language rather than a proposal.

`read_file(path) -> Res[Bytes, Str]` and the rest of the process interface from
`docs/self-hosting.md` section 1.2. `src/main.tw` is a CLI and cannot exist
without them.

This is the only entry in this file that widens what an arbitrary `.tw` file can
do, and it should be landed knowing that.

*Go bootstrap:* `os.ReadFile` and friends in `cmd/twill/`.

## NEEDS-29: a stable canonical rendering of a float

**Status: superseded by NEEDS-87. Read that entry first.** The text below is
kept because it is the record of what was believed, but its advice is now wrong
in two ways: there is no bootstrap left to call into, so "treat it as a runtime
primitive" is not an available option; and there is not one canonical rendering
but three, with different verbs and different callers, which NEEDS-87 tabulates.
`src/float.tw` answers this entry. Anyone acting on the paragraph beginning
"Treat it as a runtime primitive" will implement the wrong function.

*(Original text follows.)*

**Status:** blocking for `twill dump`, and the highest-risk item here.

The canonical dump in `testdata/` is compared byte for byte. Whatever formats a
float in `src/eval.tw` must agree with the Go side exactly, digit for digit,
including the shortest-representation rule. This is not a formatting preference,
it is the acceptance criterion of the whole differential harness.

Treat it as a runtime primitive that calls the same code the Go side calls.
Reimplementing Ryu or Grisu in twill to get a byte-identical answer is a way to
lose a month.

*Go bootstrap:* `strconv.FormatFloat` reached through the dumper in
`cmd/twill/dump.go`.

## NEEDS-30: recursion depth, and a guard on it

**Status:** half closed (2026-09). The *call* half has a counter and a
diagnostic in both evaluators, and a measured account of what that counter does
not cover; the *parse* half has neither.

Calls: `interp.DefaultMaxCallDepth` and `src/eval.tw`'s `MAX_CALL_DEPTH` are
both 10,000, and a call nested deeper than that is an ordinary twill error
naming the function, the depth and the call's line. It is checked on the way
into the frame, because a Go stack overflow is a fatal error that no recover
catches: past the limit there is nothing left to report with.

### What real programs need

Two rounds of measurement, both with the interpreter instrumented to record the
peak value of `callDepth`, one file per process, on macOS arm64 with the 1 GB
default goroutine stack. The in-repository rows were re-taken against this tree,
which is this branch merged with `main` at 1.10.0. The satellite rows and the
`std/` modules row are from the earlier round, taken 2026-09-04 against `main` at
`fcb32ae`, and were not re-run here.

Checked by the self-hosted compiler (`src/main.tw check F` on the bootstrap):

| corpus | files | deepest |
| --- | ---: | ---: |
| `src/` | 30 | **217** (`src/parse.tw`) |
| the nine satellites | 167 | 102 (`bobbin/src/report.tw`) |
| `std/`, including `std/tests/` | 48 | 77 (`std/stats.tw`) |
| `examples/` | 26 | 54 (`examples/patterns.tw`) |
| `testdata/` | 354 | 47 (`testdata/cases/interp_cumulative_62c7b46b326a4d32.tw`) |

Run on the bootstrap:

| corpus | files | deepest |
| --- | ---: | ---: |
| the nine satellites | 167 attempted, 150 ran | 18 (`selvedge/tests/registry_test.tw`) |
| `std/tests/`, including its two harnesses | 19 | 14 (`std/tests/json_test.tw`) |
| `examples/` | 26 | 8 (`examples/llama.tw`) |
| `std/` modules | 29 | 1 |
| `testdata/` | 354 | 4 (`testdata/examples/attention.tw`) |

The 17 satellite files that did not run are all `heddle`'s: they mix imports
resolved against the repository root with imports resolved against the file's
own directory, so no single working directory loads them outside `heddle`'s own
runner. All 167 were checked.

One row moved on re-measurement. The earlier round put the deepest `std/tests/`
run at 10, `llama_test.tw`; on this tree it is 14, `std/tests/json_test.tw`.
Several of those tests print their own results before the process ends, so a
reader that takes the first line of output rather than the peak line misses
them.

The deepest anything legitimate reaches is 217, and it is the self-hosted
compiler rather than a user program; the deepest thing that runs as a program is
18. The compiler's depth follows the *nesting* of the file it is checking rather
than its length: on this tree a synthetic file of 800 flat top-level `let`
bindings peaks at 14, while a single `print` of a 120-term `+` chain reaches 245.
So 217 is the deepest thing in this corpus, not a ceiling on what a checkable
file can cost.

### Where the stack runs out, which is not one number

This entry has now said two different things about a fat frame, and both were
wrong. The first said that five parameters, three locals and a nested expression
crashed at the same depth as a thin frame, because "what fills the stack is the
interpreter's own Go frames rather than anything the twill frame holds". The
second said the fat frame crashed a quarter shallower, at 110,375, so what the
frame holds is not free after all. Neither is what happens. What was varying
between the two programs, unnoticed, was the *expression around the recursive
call*: the fat one had one more layer of it, and 110,375 is exactly the
two-layer number for a thin frame as well.

Parameters and locals are free, because they live in a heap environment. What is
not free is how deeply the recursive call sits inside the expression around it,
because every enclosing operator is one more `evalExpr` frame the interpreter
holds open across the call.

Re-measured one variable at a time, bisecting where the process takes a fatal Go
stack overflow, with `TWILL_MAX_CALL_DEPTH` raised out of the way. Depths are
nested twill calls, which is what `callDepth` counts and what the limit is
compared against, so `f(n)` reaches n+1 of them:

| where the recursive call sits | deepest that returns | dies at |
| --- | ---: | ---: |
| `return f(n - 1)`, bare | 236,295 | 236,296 |
| `f(n - 1) + 1` | 150,466 | 150,467 |
| two layers | 110,375 | 110,376 |
| three layers | 87,153 | 87,154 |
| five layers | 61,342 | 61,343 |
| ten layers | 35,246 | 35,247 |
| twenty layers | 19,044 | 19,045 |
| thirty layers | 13,046 | 13,047 |
| forty layers | **9,922** | **9,923** |
| sixty layers | 6,709 | 6,710 |
| a hundred layers | 4,072 | 4,073 |
| three hundred layers | 1,373 | 1,374 |

What the frame holds does not appear in that table at all. One parameter, three,
five or eight: 150,466 every time. No locals, three, eight or sixteen: 150,466
every time. Five parameters and three locals with one operator layer: 150,466.
Five parameters and three locals with two: 110,375, the same as a thin frame
with two, which is where the second version of this entry went wrong.

Layers are not all priced alike either. Ten layers of each, against 236,295 with
none:

| ten layers of | deepest that returns |
| --- | ---: |
| `(x)` | 236,295, which is to say free; the parser keeps no node for it |
| `abs(x)` | 38,043 |
| `x + 1` | 35,246 |
| `if n > 0 { x } else { 0 }` | 29,642 |
| `[x][0]` | 24,105 |

### What 10,000 is therefore worth

The reciprocal of the depth is linear in the number of layers, so the cliff
falls away without bound as the expression gets deeper: three hundred layers of
arithmetic survive 1,373 calls. **No fixed call limit is below the crash for
every program**, and 10,000 is not an exception.

What it does buy was bisected. A runaway call still gets the diagnostic at 39
arithmetic layers, which survives 10,165, and stops getting it at 40, which
survives 9,922 and so never reaches the counter's 10,000. For the most expensive
layer measured, `[x][0]`, the boundary is 25 layers, surviving 10,271, against
26, surviving 9,893. Against that: the deepest call site written anywhere in this
repository is nested 21 expressions deep, `src/check.tw:4620`, out of 19,362
call sites in the 499 `.tw` files it holds.

The fix, for anyone who has one of these, is a `let`: bind the call and use the
name afterwards, and the frames the interpreter holds open across the call go
back to the bare-call count. Measured with the same forty layers applied to a
bound name instead of to the call itself, the depth goes back out to 236,274.

So the limit is a diagnostic for the shapes people write and not a guarantee for
every shape they could write. A runaway recursion whose call sits more than
about twenty-five expressions deep still dies the way it did before the limit
existed: checked, at forty layers, on the shipped binary at the default limit,
which exits 2 with 280 lines of Go runtime internals. That is no worse than the
state before this work, and it is what the language guide now says out loud
rather than leaving to be discovered.

Two ways to close what is left, neither taken here. The counter could be charged
for the call site's static nesting instead of one per call; both engines can
compute that identically from the AST, so the two diagnostics would stay equal,
but it changes what the number in the message means and wants its own
measurement. Or the limit could come down, since the envelope scales inversely
with it, weighed against a deepest measured legitimate depth of 217.

### Two counters, one stack

When `src/eval.tw` runs on the bootstrap, the peak outer depth for a program
that nests `k` calls is `8k + 9` exactly, measured at k = 4, 12, 52, 102 and 202. A host left at 10,000 therefore stops the inner
program at 1,248 of its own calls -- checked: 1,248 runs, 1,249 is refused --
and reports against a function inside `src/eval.tw`.

No single shared constant can fix that. Reaching L inside costs more than 8L
outside, which is more than L for every L, so the host has to be given the
larger number, and `TWILL_MAX_CALL_DEPTH` is how. The number needed was
bisected on the shipped CLI rather than derived from the slope, and re-bisected
against this tree: at `TWILL_MAX_CALL_DEPTH=80012` the host still refuses first
and names `push_text`, at `80013` the guest refuses and names the user's own
function. `TWILL_MAX_CALL_DEPTH=100000` is the documented value: above 80,013
without sitting on it, and low enough that the host survives the run, which is
what `interp.TestSelfHostedRefusalsMatchTheBootstrap` demonstrates every time it
passes. There is no single number above which it stops working, for the same
reason there is no single crash depth.

### The parse half, still open

Parsing and checking are still uncounted, and are still the exposure this entry
was opened for. The parser and the checker are recursive descent over user
input, so their depth is the input's nesting rather than the program's call
graph, and the call counter does not see them. Measured on the same machine as
the numbers above, by bisection: a `print` of a single expression of 713,916
nested parentheses parses, checks and runs, and 713,917 crashes the Go parser
with the same `runtime: goroutine stack exceeds 1000000000-byte limit` and exit
2 the runaway recursion used to give. That is far past what a written program
reaches and well within what a hostile input can send, so it is an
input-validation problem rather than a usability one, and it wants the same
treatment, a depth counter in the descent, with a diagnostic.

*Go bootstrap:* `internal/interp.Interp.MaxCallDepth`, defaulted by
`TWILL_MAX_CALL_DEPTH`, for the call half. None for the parse half; a
sufficiently nested twill file still crashes the Go parser.

## NEEDS-31: deliberate divergence: `t[]`

**Status:** decided. `src/parse.tw` `parse_index_or_slice`.

`t[]` has no start expression and no `:` to make it a slice.
`internal/parser/parser.go` builds an `ast.Index` with a nil `Index` field and
the failure surfaces later in the evaluator, pointing at the wrong place.
`src/parse.tw` refuses it at the bracket with "expected an index expression".

This is the one place the twill parser is knowingly not a copy. It is recorded
here rather than silently fixed because the differential harness will report it
as a divergence, and a divergence with no note is indistinguishable from a bug.
Either the Go parser is changed to match, or this entry is the reason the diff
is expected.

---

## Found by differential testing of the lexer

The three entries below came out of running `src/lex.tw` against
`internal/lexer/lexer.go` over 385 corpus files and 4,000 fuzzer cases. See
"Verification" at the end of this file.

## NEEDS-32: Go-compatible `%q` for a non-printable or non-ASCII character

**Status:** closed for the control range; narrowed for the rest. `src/lex.tw`
`quote_char` now escapes the ASCII control bytes exactly as Go's strconv does,
a NUL as `"\x00"`, a vertical tab as `"\v"`, the seven named escapes and
lowercase `\xHH` for the others including `0x7f`, so the case the entry leads
with agrees byte for byte. A printable byte and every byte of a valid multibyte
rune are emitted raw as before and already agreed. What remains is a
non-printable rune above `0x7f` and an invalid UTF-8 byte, which need Go's full
`%q` with its Unicode printability table (NEEDS-1) to render as the replacement
rune; those are left raw, and the lexer's "unexpected character" realistically
meets a control byte, which is now right. The original text follows.

*(Original status: open divergence. `src/lex.tw:478` (`quote_char`).)*

The message `unexpected character %q` is the lexer's only use of `%q` on source
text. Go renders a non-printable rune as an escape: a NUL byte prints as
`"\x00"` and a lone surrogate as `"\ufffd"`. `quote_char` emits the raw byte
between quotes, so the two implementations produce different bytes for a NUL,
a vertical tab, or any other non-printable input character.

Every printable case agrees, including multi-byte ones: `€` and an emoji both
round-trip. So this only fires on input that is already malformed, which is
exactly why it would survive a weak harness and needs to be written down.

Fix with the `%q` rendering asked for in NEEDS-20, not with a special case here.

*Go bootstrap:* `fmt.Sprintf("unexpected character %q", string(ch))`.

## NEEDS-33: the Go bootstrap panics on a trailing backslash at end of file

**Status:** fixed in `internal/lexer/lexer.go`. It was a bug there, not in
`src/lex.tw`.

Source ending in an unterminated string whose last byte is a backslash, for
example `x = "ab\`, makes the Go lexer index past the end of its rune slice and
panic. The string branch consumes the backslash and calls `advance()` for the
escaped character without checking that one exists.

`src/lex.tw:405` checks, and returns "unterminated string" at the opening quote,
which is the right diagnosis: the file's problem is the missing close quote, not
the backslash.

The Go lexer was fixed rather than the panic reproduced, and
`TestUnterminatedStringEndingInABackslash` in `internal/lexer/lexer_test.go`
covers four inputs including `x = "ab\`. It is kept here because it is the first
thing self-hosting found, which is the argument `docs/self-hosting.md` section 3
makes for the exercise.

---

## Verification

`src/lex.tw` was checked against `internal/lexer/lexer.go` by transcribing both
into a single executable form and comparing them, rather than by reading them
side by side. The comparison covers token kind, literal text, line and column,
the comment list including each comment's trailing flag, and the error message
and position on inputs that fail.

- **385 files**: every `.tw` file in `examples/`, `std/`, `testdata/` and
  `src/` at the time of the run. Zero divergences. The corpus grows, so the
  count is a snapshot; re-run rather than quote it.
- **4,000 fuzzer cases**, seeded, mixing random token soup with mutated slices
  of the corpus, over an alphabet that includes non-ASCII text, escape
  sequences, NUL, vertical tab, unterminated strings and every multi-character
  operator. 2,516 of the cases were error cases. Zero divergences.
- **22 targeted edge cases**. Three divergences, all of them the ones recorded
  above: NEEDS-15, NEEDS-32 and NEEDS-33.

The byte-versus-rune question the port turns on is settled by this rather than
by argument: the column counter in `src/lex.tw` skips UTF-8 continuation bytes,
and the case "multibyte then column-sensitive tokens" confirms that a token
following a multi-byte string literal lands on the same column in both
implementations.

This is not the real harness. The real one runs `src/lex.tw` on a twill runtime
and compares against the Go binary, and it cannot exist until the entries above
are implemented. Until then this is the strongest available evidence, and it is
strong enough to have found NEEDS-33.

---

# What the command line needs from the language

Appended by the CLI work. `src/term/` and `src/cli/` are the twill command line
written in twill, and they rest on everything above plus the entries here. Same
conventions: what the feature is, which file reaches for it, what the Go side
does in the same place.

## NEEDS-34 - `chr(n)` for a single byte

**Status:** done (2026-08). `chr(n)` is a builtin and is a byte rather than a
codepoint, which is the half of the request that mattered for the braille
packing: `chr(65)` is `A`. The `\x1b` string escape listed below as the
alternative was not added, and on the argument given below it should not be.

Twill string literals recognise `\n`, `\t`, `\"` and `\\` and nothing else
(`docs/language-guide.md`), so ESC (27) and BEL (7) cannot be written. Needed:
`chr(n: I64) -> Str` producing the one-byte string for `n` in 0..255, and it
must be a byte and not a codepoint, because `src/cli/banner.tw` hand-encodes
U+2800 braille as three bytes and would be encoding an encoding otherwise.

*Reaches for it:* `src/term/ansi.tw` (`esc`, `bel`), `src/cli/banner.tw`
(`braille`).

*Go bootstrap:* `internal/builtins` has no `chr`. The Go side writes the escape
introducer as a string literal.

*Alternative that would also do:* an `\x1b` escape in the lexer's string
scanner. `chr` is preferred because the braille packing needs arithmetic on the
byte anyway.

## NEEDS-35 - `Str` concatenation with `+`

**Status:** done (2026-08). `"a" + "b"` is `"ab"`, `Str + non-Str` is an error
with no coercion, and the quadratic cost of building in a loop is unchanged and
is what `src/bytes.tw` (NEEDS-7, also done) is for. The note at the end of this
entry still stands as work: `src/term/` and `src/cli/` should be moved onto the
builder now that both halves exist, which deletes the private `join` and the
private `repeat`. **The normative text is
`docs/language-guide.md`, Strings → Concatenation.** `Str + Str` exists and
produces a new `Str`; `+` between a `Str` and a non-`Str` is an error with no
coercion; and the quadratic cost of building in a loop is stated there along
with the `src/bytes.tw` builder that is the answer to it.

`docs/self-hosting.md` gives `Bytes` a `concat` and gives `Str` length, byte
indexing and slicing, but never says `Str + Str`. The CLI is almost entirely
string building, and doing it through `Bytes` would mean a conversion at every
one of several hundred sites.

*Reaches for it:* every file in `src/term/` and `src/cli/`.

*Go bootstrap:* `+` on `value.Str` is currently an error; the interpreter's
binary op dispatches only tensors.

*Note on cost:* naive concatenation in a loop is quadratic. `src/term/width.tw`
`repeat` and `src/cli/progress.tw` `bar` both build strings a cell at a time, so
either the implementation ropes them or a `Bytes` builder is exposed and these
files are rewritten against it. Flagging it now rather than discovering it on a
200-column progress bar.

*Already half answered.* `src/bytes.tw` exists and wraps exactly this surface
(`bytes_new`, `bytes_push`, `bytes_to_str`, plus `concat`, `join` and `repeat`).
The CLI should be moved onto it once the primitives land, which would delete the
private `join` in `src/term/ansi.tw` and the private `repeat` in
`src/term/width.tw`. They are written out here only because `src/term/` was
built before that file existed.

## NEEDS-36 - `arr(...)` as a literal constructor

**Status:** done (2026-08). `arr()` is the empty array and `arr(a, b, c)` the
populated one, alongside `arr_new`, `arr_push`, `arr_clear` and `pop`.

`docs/self-hosting.md` specifies `Arr[T]` with `push`, `pop`, index and `slice`,
but no way to write one down. `arr()` for empty and `arr(a, b, c)` for a
populated one, with `T` unified from the arguments.

*Reaches for it:* `src/cli/help.tw` `groups()` builds the entire help screen as
nested `arr(...)` literals; `src/cli/spinner.tw` `glyphs`.

*Go bootstrap:* `list(...)` exists and is the model to copy, but returns the
heterogeneous `value.List`.

## NEEDS-37 - `Opt[T]` and `match`, for `env`

**Status:** done (2026-08). `env(name)` returns `Opt[Str]` and the `match` on
it works, so `caps.tw` `env_or` and `has_env` have what they need and the whole
capability layer is unblocked.

`env(name) -> Opt[Str]` from `docs/self-hosting.md` section 1.2 needs the enum
and the `match` that reads it. `caps.tw` `env_or` and `has_env` are the only
uses in the CLI, but they are on the path of every command, so the whole
capability layer is behind this.

*Go bootstrap:* `os.Getenv` returning a value and a found flag.

## NEEDS-38 - `is_tty_stdout()` and `window_size()`

**Status:** done for both, with one half of one of them missing (2026-08).
`is_tty_stdout()` returns a `Bool` and `window_size()` returns
`{cols: ..., rows: ...}`, zero for unknown as asked. What is not there is the
separate question this entry raises in its own second sentence: there is no
`is_tty_stderr`, so a program cannot ask about the two streams independently and
must assume they agree. That is exactly the case the entry warned about, one a
pipe and the other not, so it is recorded rather than counted as closed.

Two runtime queries not in `docs/self-hosting.md` section 1.2, which lists only
`read_file`, `write_file`, `stdin_all`, `write_out`, `write_err`, `args`, `env`
and `exit`.

- `is_tty_stdout() -> Bool`. Whether stdout is a character device. This is the
  single most important call in the CLI: it is what keeps escape sequences out
  of a redirected log, and there is no way to infer it from the environment.
  Note that stderr needs the same question asked separately, since diagnostics
  go there while a progress bar goes to stdout, and one may be a pipe while the
  other is not.
- `window_size()` returning columns and rows, from `TIOCGWINSZ` on unix and
  `GetConsoleScreenBufferInfo` on Windows, with a zero result meaning unknown
  rather than an error.

*Reaches for it:* `src/term/caps.tw` `detect`.

*Go bootstrap:* neither exists. The current CLI writes to stdout
unconditionally.

*Not asked for:* SIGWINCH. A resize mid-frame smears one repaint and corrects
itself on the next, which is an acceptable cost for not needing a signal
interface in the language.

## NEEDS-39 - a monotonic millisecond clock

**Status:** done (2026-08, 1.6), under the name `mono_ns` rather than
`now_ms`: nanoseconds from a monotonic source. `clock_now_ms` remains and is the
**wall** clock, and keeping both is the point of the entry rather than an
accident. An NTP step backwards moves `clock_now_ms` and cannot move `mono_ns`,
so a rate or an estimate computed from the wall clock can go negative on a long
run and one computed from `mono_ns` cannot. Anything that measures an interval
wants `mono_ns`; anything that prints a time of day wants `clock_now_ms`; using
either for the other's job is a bug that only appears on long runs.

`now_ms() -> I64`, monotonic, unaffected by wall-clock adjustment. Every
animated thing in `src/cli/` is driven by it: the frame rate limit in
`src/term/frame.tw`, the spinner's delay gate, the progress bar's smoothed rate
and its estimate.

Monotonic specifically, not wall clock. An NTP step backwards during a long
training run makes a wall-clock rate negative and the estimate nonsense, and
that is a bug that only appears on long runs, which are exactly the runs where
the estimate matters.

*Threaded, not called.* No file in `src/cli/` calls it: the current time is a
parameter to every function that needs it. That is deliberate, so the renderers
stay pure and can be tested by feeding them a clock, and it means this entry is
only needed by whatever drives the loop.

*Go bootstrap:* the standard library clock, not used for any of this yet.

## NEEDS-40 - `F64` in systems mode, with `cos` and the conversions

**Status:** done (2026-08). The type question was answered first and is
unchanged below: a systems-mode scalar is not a rank-0 tensor. 1.6 supplied the
rest. `cos` and `sin` work on an `F64`, `f64()` and `i64()` convert, and the
wider float set the tensor kernels wanted landed with them (`f64_exp`,
`f64_log`, `f64_sin`, `f64_cos`, `f64_sqrt`, `f64_tanh`, `f64_pow`, `f64_mod`,
`f64_trunc`, `f64_floor`, `f64_ceil`, `f64_round`, `f64_signbit`), which is
NEEDS-68, NEEDS-65 and NEEDS-69 all at once. **The normative text
is `docs/language-guide.md`, Systems-mode types → `F64`, and what a
systems-mode scalar is.**

The answer to the second half of this entry, which is the half that was worth
asking: a systems-mode scalar is **not** a rank-0 tensor. `F64` is a machine
word with no shape, no tape entry and no allocation, so loom's `Meter.total`
does not allocate per step. The float math builtins are entry 15 of
`docs/roadmap.md` and are still open; what is now fixed is that they will take
and return `F64` rather than a rank-0 tensor.

`docs/self-hosting.md` says systems mode has no tensors, and is right, but it
does not say what a plain float is in systems mode. Two files need one:

- `src/cli/banner.tw` computes the ribbon from `cos`, because the mark is
  generated from the twist rather than pasted in as glyphs.
- `src/cli/tensor.tw` formats tensor elements and cannot do that in integers.

Needed: `F64` as a systems-mode scalar type distinct from a rank-0 tensor, the
`f64()` and `i64()` conversions already specified in section 1.2, and `cos` and
`sin` usable on it.

*Go bootstrap:* these are tensor builtins; in numeric mode a scalar is a rank-0
tensor and the distinction does not arise.

## NEEDS-41 - a read-only tensor view for the formatter

**Status:** done (2026-08), by the second of the two spellings the entry
offers. `shape(t)` gives the dimensions and `arr_of_tensor(t)` gives the
row-major elements, read-only and by copy, which is the `shape_of`/`elements_of`
pair rather than `view_of`. The copy is the point and the copy is what happens.

The REPL's job is printing tensors, and systems mode has none. `tensor.tw`
declares a `View` of dimensions plus row-major elements and formats that, so
what is needed is one bridge: a builtin that turns a numeric-mode tensor into
those two arrays.

`view_of(t)`, or a pair of `shape_of` and `elements_of`. Read-only and by copy.
The copy is the point: the formatter must not be able to alias a live tensor,
and a 512x512 matrix is elided down to nine rows anyway, so a lazy view would be
an optimisation of the wrong thing.

*Go bootstrap:* the tensor type already carries its dimensions and a flat data
slice, so this is an exposure rather than an implementation.

## NEEDS-42 - struct field mutation through a handle

**Status:** done (2026-08), including the case this entry added and a lexer
never exercises: mutation through a field of another struct. An `Outer` holding
an `Inner`, mutated as `o.i.n = o.i.n + 1` through a parameter, is visible to
the caller. So `frame.paint`, `progress.advance` and `repl.feed` all work as
written. **The normative text is `docs/language-guide.md`, `struct`,
and what a parameter is**, and it covers the case this entry adds: mutation is
visible through a field of another struct, to any depth.

Reference semantics for structs, as specified in `docs/self-hosting.md` section
1.2. Every stateful widget is a struct whose fields a function mutates and whose
caller sees the change: `frame.paint` updates `height`, `last` and `next_due`;
`progress.advance` updates `done` and the smoothed rate; `repl.feed` updates the
bracket depth.

This is already in the design, but it is worth recording that the CLI is its
second consumer after the lexer, and that the CLI needs the mutation to be
visible through a field of another struct (a `Spinner` holds a `Frame`, and
`step` mutates through it), which is a case a lexer never exercises.

## NEEDS-43 - `Arr[T]` element assignment

**Status:** done (2026-08). `xs[i] = v` assigns an element of an `Arr` and of a
list, so `width.tw` can rewrite wrapped lines in place. This is the same landing
as NEEDS-17's `set` half and NEEDS-97's, recorded three times because three
callers asked for it separately.

`docs/self-hosting.md` gives `Arr[T]` an index and a `push`, and its feature
list says "indexed assignment" while the summary table does not repeat it.
Recording the use so it is not dropped: `width.tw` rewrites wrapped lines in
place to add the continuation indent.

## NEEDS-44 - integer division and modulo on `I64`

**Status:** done (2026-08, 1.6). See NEEDS-24, which is this entry under an
earlier number and carries the verification: truncation toward zero, `%` taking
the sign of the dividend, and division or modulo by zero as a named runtime
error. The two entries were the same request filed twice and are now closed
together. **The normative text is `docs/language-guide.md`, Operators → Integer
division and modulo on `I64`.** It records the answer this entry asked for,
truncation toward zero with `%` taking the sign of the dividend, and adds the
two things this entry did not ask about and that a caller needs: numeric mode's
`%` is floored and therefore disagrees, and `shr` is `floor(a / 2^k)` and
therefore also disagrees for a negative dividend. See NEEDS-24, which is this
entry under an earlier number.

`/` and `%` exist, but on tensors they are float operations. On `I64` they must
truncate toward zero, and `%` must take the sign of the dividend. The 256-colour
quantisation in `src/term/color.tw`, the braille packing in
`src/cli/banner.tw`, and the eighth-block arithmetic in `src/cli/progress.tw`
are all exact integer arithmetic and are wrong under any other rounding.

Also needed: division by zero on `I64` as an error value rather than a panic,
which section 1.2 already specifies.

## NEEDS-45 - `str()` on `I64`

**Status:** done (2026-08). `str` on an `I64` produces the digits with no
decimal point and no exponent (`str(i64(-5))` is `-5`), so the hazard the entry
names, a trailing `.0` in every line number, does not arise. The measurement
below, that the bootstrap never emitted it either, is unchanged and is why this
entry was prospective rather than a bug. **The normative
text is `docs/language-guide.md`, Standard library → `str` on a number.**

`str(n)` for an `I64` must produce the digits with no decimal point and no
exponent. Today `str` on a scalar goes through the tensor printer, and a
trailing `.0` would land in every line number, every column count and every axis
index in every diagnostic.

*Measured, because the entry reads as though it had been:* the Go bootstrap
does not emit the trailing `.0`. `internal/value.FormatNumber` returns
`strconv.FormatInt` for any float that is integral, so `str(3)`, `str(scalar(3))`,
`str(sum([1.0, 2.0]))` and `str(len(range(3)))` all print `3`, and `twill fmt`
rewrites the literal `3.0` to `3`. The hazard is real but it is prospective: it
is what a systems-mode `str` would do if it were routed through the tensor
printer, which is what the entry is asking not to happen. The guide also pins
the `F64` rendering and the boundary between the two, since `src/fmt.tw`
`format_number` sends an integral `F64` to `str(k)` on an `I64` and the two
renderings have to agree there.

*Reaches for it:* everywhere. `src/cli/diagnostic.tw` alone uses it for the
line, the column and the gutter width.

## NEEDS-46 - `Str` equality must survive the `Str` rewrite

**Status:** the constraint held (2026-08). `Str` became a distinct indexable
value, which `docs/self-hosting.md` flagged as the medium-risk change in the
whole subset, and `==` and `!=` on `Str` still compare bytes with no case
folding and no normalization. `<` and friends are still undefined on `Str`.
Nothing to do; recorded as verified rather than as assumed. **The
normative text is `docs/language-guide.md`, Strings → Equality**, which states
it as bytes with no case folding, no normalization and no locale, and Strings →
Ordering, which says that `<` and friends stay undefined on `Str` and pins the
bytewise order the hand-written comparisons implement.

`==` and `!=` on `Str` already work by the deep-equality rule in
`docs/language-guide.md`, and `src/term/caps.tw` leans on it for every
environment comparison. `docs/self-hosting.md` flags making `Str` a distinct
indexable value as the medium-risk change in the whole subset, so this entry
exists to say that the CLI is a second consumer of that rule holding.

## NEEDS-47 - a line reader for the REPL

**Status:** open, and still not blocking (2026-08). Neither `stdin_all` nor
`read_line` exists, so the one thing this entry says the language needs is the
one thing missing. `src/cli/repl.tw` is written around not having it and the
seam below is unchanged, which is why this is open rather than blocking: a host
that reads a line can hand it to `repl.feed` today, and only a twill-hosted REPL
is stuck.

`repl.tw` owns the prompt and the framing and does not own the read loop,
because line editing, history and completion are a terminal-raw-mode problem
that does not belong in the language. Recorded so the seam is deliberate: the
host reads a line and hands it to `repl.feed`, and the only thing the language
needs is `stdin_all` or a `read_line`.

The one thing the host must get right is restoring the terminal on exit, which
`src/term/frame.tw` `abandon` covers for the frame path but which the line
reader must do for its own raw mode.

## NEEDS-48 - `write_out` and `write_err` taking a `Str`

**Status:** done (2026-08). `write_out` and `write_err` take a `Str` directly,
so the per-call-site conversion the entry was written to avoid never appeared.

Section 1.2 specifies `write_out(Bytes)`. The CLI produces `Str` everywhere and
would otherwise convert at every call site. Either overload accepts a `Str`, or
`Str` to `Bytes` is a zero-copy conversion and that is stated, because if it
copies then the progress bar allocates its whole rendered frame thirty times a
second.

---

## Runtime primitives the compiler names

These are not language features so much as the surface `src/` calls. Listed
separately because they are cheap individually and easy to lose track of.

| Primitive | Used by | Go equivalent |
|---|---|---|
| `arr_new`, `push`, indexed get and set, `len` | everywhere | Go slices |
| `dict_new`, `dict_set`, `dict_get`, `dict_has`, `dict_del`, `dict_keys`, `len` | check.tw, lex.tw, tensor.tw | Go maps, plus `Record.Keys` for order |
| `dict_or(d, k, dflt)` | check.tw unit algebra | the zero value of a Go map read |
| `dict_must(d, k)` | check.tw, eval.tw | a Go map read with a known-present key |
| `bytes_new`, `bytes_push`, `bytes_to_str` | bytes.tw | `strings.Builder` |
| `str(x)` for `I64` and `F64` | every diagnostic | `strconv`, `fmt` |
| `str_quote(s)` | check.tw, parse.tw | `%q` |
| `f64_of_str`, `i64_of_str` | parse.tw | `strconv.ParseFloat`, `strconv.Atoi` |
| `i64_of_f64`, `f64_of_i64` | check.tw, eval.tw | Go conversions |
| `f64_mod`, `f64_pow` | eval.tw | `math.Mod`, `math.Pow` |
| `and(a, b)` and the other bitwise ops | lex.tw | Go `&` |
| `read_file`, `args`, `write_out`, `write_err` | main.tw | `os` |
| `abort(msg)` | everywhere | `panic` |

## NEEDS-49: the systems-mode checker policy

**Status:** answered and implemented (2026-08, 1.6). The decision was the design
decision this entry said it was, and it went the other way from
`docs/self-hosting.md` section 1.3.

**What was chosen.** The shape checker's existing policy, *report only what is
certain*. In systems mode `I64`, `F64`, `Bool`, `Str`, `Bytes`, `Unit`,
`Arr[T]`, `Dict[K, V]`, `Opt[T]`, `Res[T, E]`, a declared `struct`, a declared
`enum` and a function type are real types, and a mismatch is reported at the
five places a type is written down: a binding with an annotation, an argument
against a parameter, a `return` against a declared return type, a struct literal
field, and an enum payload. Each has its own message, and each was verified:

    "x" is declared I64 but the value is Str
    argument 1 ("a") is declared I64 but the value is Str
    return gives I64 but the function declares Bool
    field "a" of S is declared I64 but the value is Str
    the payload of V is declared I64 but the value is Str

**What was not chosen, and why.** `docs/self-hosting.md` section 1.3 asks for
the stricter rule: an unknown surviving to the end of inference is itself a
diagnostic. That was rejected. The reason is the same one that makes the shape
checker useful rather than tiresome: this checker infers over a language with
untyped numeric mode underneath it, with builtins whose result type depends on
their arguments, and with generic annotations that are not yet declarations
(NEEDS-4). Under the strict rule every one of those produces an error that says
nothing except that the checker could not work something out, and the reader
learns to ignore the whole class. Under "report only what is certain" every
diagnostic that appears is a real disagreement between two things the programmer
wrote. The cost is stated plainly rather than hidden: an unannotated systems-mode
program is checked hardly at all, and the checker's coverage is a function of how
much of the file is annotated. That is a worse guarantee and a better tool, and
if the strict rule is ever wanted it wants NEEDS-4 finished first.

**The consequence for the original complaint.** `twill check` now passes on
every file in `src/`, so the self-hosted compiler *is* checked by the checker,
which is the uncomfortable place the entry ends by naming.

**One hole in it, found by probe while auditing this file and closed before the
release.** A block-bodied function's declared return type was not reaching its
call sites, so `let c: Str = s(...)` was a false "declared Str but the value is
Unit" whenever `s` had a `{ ... }` body. That was not the strict-versus-
permissive question but a hole in the permissive one, and it is what NEEDS-10
and NEEDS-82 both ran into. A declared return type is now what a call produces.

## NEEDS-50: an out-of-range axis in `transpose`

**Status:** done (2026-08-11). `src/check.tw` `transpose_result` and
`internal/checker/checker.go` (the `transpose` case) now report an out-of-range
permutation axis through `report_axis`/`reportAxis`, the same path every other
axis-taking builtin uses, so a `transpose(x, 0, 5)` on a rank-2 tensor is a
diagnostic (`axis out of range for [2, 3]: ...`) rather than a silent unknown
that failed only at run time. Both sides changed together and produce
byte-identical diagnostics; tests in `internal/checker/checker_test.go` and
`internal/interp/selfhost_test.go`.

## NEEDS-51: the import resolver

**Status:** done (2026-08). Both things this entry says are missing landed:
file reading is NEEDS-28, and the embedded standard library is reachable from
twill through the `module_source(path)` builtin, which resolves a `std/...` path
to the embedded source and anything else to a file relative to the importer. The
policy in the comment holds against the running binary: an unaliased
`import "std/text"` lands unqualified, an aliased one lands in a record, and a
`.ra` import is refused by name (`uses the retired .ra extension`).

The policy is written out in the comment there: relative to the importing file,
`std/` resolves to the embedded library unless `TWILL_STD` overrides, `.ra` is
refused outright, a module is evaluated once and cached, and an aliased import
lands in a record while an unaliased one lands unqualified.

What is missing is file reading (NEEDS-28) and a way to reach the embedded
standard library from twill, which the bootstrap does with `go:embed`.

## NEEDS-52: one builtin table, not two

**Status:** done. `src/builtins.tw` owns the one list. `src/check.tw` builds its
name set from `builtins.NAMES` and `src/eval.tw` `is_builtin` asks
`builtins.is_builtin_name`, so the checker's known-name set and the evaluator's
dispatch set read from the same string and cannot drift. This also closed a
latent gap: eval's `is_builtin` had been calling a `builtin_exists` that was
never defined, so a bare builtin name in expression position had no working
resolution until now.

*(Original: the checker's table is what the diagnostics are compared against;
the evaluator's would be what the dispatch uses. They were separate because
merging them before the dispatch existed would have been guessing at its
shape.)*

## NEEDS-53: the formatter

**Status:** done. `src/fmt.tw`, wired into `src/main.tw` `format_file`.

`internal/format/format.go` is ported. What is left of this entry is the
primitives the port names, which are NEEDS-76 and NEEDS-79, and the two
behaviours it inherited rather than chose, which are NEEDS-77 and NEEDS-78.

The rule that had to survive the port, because it is the one people notice:
`fmt --write` never renames a file. The retired-extension check runs before the
file is opened, so such a file is refused and left exactly as it was.

### Verification of the source formatter

`src/fmt.tw` was checked against `internal/format/format.go` by transcribing the
twill formatter, and the lexer and parser it stands on, into one executable
form, and comparing its output against the reference binary's `twill fmt`.

- **405 files** (every `.tw` file in `testdata/`, `examples/`, `std/` and
  `src/`). Zero divergences, zero idempotence failures. 23 of the 405 are
  negative fixtures that both implementations reject, with the same error kind.
- **13,000 generated programs**, over an alphabet built to hit the parts the
  corpus does not: mixed-precedence operator chains, `^` against `-`, unary
  operators over binary operands, slices with either side absent, unit
  expressions with negative and multi-digit exponents, `unit` declarations,
  lambdas with and without block bodies, own-line and trailing comments, empty
  comments, and float literals spanning the exponent thresholds. 8,230 formatted
  on both sides, byte identical, all idempotent. The remaining 4,770 were
  rejected by both, and the rejections agreed on kind: a syntax error on one
  side was a syntax error on the other, a comment the formatter cannot place was
  a refusal on both.

The one divergence the run actually found was the float exponent threshold, and
it is now NEEDS-76. It would not have been caught by the corpus, which contains
no float literal that reaches `%g`.

### Verification of the float conversions

`src/float.tw` was checked by transcribing it into one executable form and
comparing that against two independent references: a correctly rounded
conversion oracle, and the reference binary itself. The second is the one with
teeth, because it compares against the code the differential harness will
actually be diffing against, with no third party's notion of correctness in
between. Three of the reference binary's own commands expose the three
renderings, which is what made it possible:

- **`twill run`, 42,938 printed values.** A generated program of `print(x)`
  lines, compared line for line against `format_number`. Zero divergences. The
  same 42,938 literals parsed back to the same bits, zero divergences.
- **`twill run --dump=canonical`, 30,014 values.** The canonical dump's `num`
  fields against `f64_hex`. Zero divergences. Reading the dump's own hex text
  back gave the original bits in all 30,014, zero divergences.
- **`twill fmt`, 24,621 literals.** The formatted source against `f64_shortest`,
  of which 24,178 reached `%g` rather than `internal/format`'s integer fast
  path. Zero divergences.
- **116,980 cases against the oracle**, covering what the binary cannot easily
  be driven over: 40,000 random f64 bit patterns, 16,000 subnormals of both
  signs, 17,997 values on the `%.6f` half-way boundary, 12,128 integral values
  across the `int64` fast path and its edges, 3,100 values either side of the
  `%g` exponent switch, 3,619 float literals lifted out of `testdata/`,
  `examples/` and `std/`, and 24,074 parse cases including hexadecimal input,
  underscores, and the range and syntax errors. Zero divergences.

287,505 comparisons, zero divergences.

Three things that had to be right and are worth naming because each was a real
divergence at some point in the run rather than a hypothetical:

- **`print` is not `%g`.** See NEEDS-87. Reading `internal/` rather than
  assuming is what turned this up, and assuming would have made every printed
  non-integer wrong.
- **`%g`'s exponent threshold is 6.** The same finding the source formatter's
  run made, from the other side. The corpus cannot catch it: no float literal in
  the tree reaches `%g`'s exponent form at all, so a corpus-only check passes
  while being wrong. It was caught by generated values on both sides of the
  switch.
- **Underscores and hexadecimal on the parse side.** `f64_of_str` first accepted
  `_1.0`, which Go rejects, and rejected `0x1p3`, which Go accepts, because the
  slow path it is ported from relies on a validating reader that runs before it.
  Both were found by generated input, not by the corpus, which contains neither.

The structural differences that could have broken it and did not: the twill
version has no unsigned integer, so the shifts that Go runs on `uint` run
through the helpers in NEEDS-85; and it does the decimal arithmetic on ASCII
digit bytes in an `Arr[I64]` rather than a Go `[800]byte`, which changes every
index expression in the file.

### Verification of the einsum spec parser

`src/tensor.tw`'s `parse_einsum` and `einsum_output_dims` were checked against
`internal/tensor/einsum.go` by the same method: two independent transcriptions,
compared on the parsed spec, the resolved output dimensions, and the exact error
text.

- **12,080 parse cases** (hand-written specs plus 3,000 random ones over an
  alphabet containing `,`, `->`, spaces, uppercase and digits, at operand counts
  0 to 3). Zero divergences.
- **2,728 output-dimension cases**, including unknown sizes, inconsistent label
  sizes, and rank mismatches. Zero divergences.

This matters more than its size suggests, because `src/check.tw` calls the same
parser to validate a literal spec. If the two implementations disagreed, the
checker and the runtime would report different things about the same einsum, and
the corpus would show it as a checker bug rather than a shared one.

The structural differences that could have broken it and did not: the twill
version splits on bytes rather than using `strings.Split`, and it sorts the
implicit output labels with its own insertion sort rather than `sort.Slice`.
Both are places where a port silently drifts.

---

## NEEDS-54: the kernels `src/tensor.tw` does not have yet

**Status:** done. Every row of the table below is written, each with its `vjp_`
gradient rule, and `src/eval.tw` routes to them. The table is kept because it is
the checklist the port was done against.

`src/tensor.tw` implements the kernels in twill and declares its interface at
the head of the file. The builtins in `src/eval.tw` call that interface, and
these are the entries it does not have yet. Each one is named by a builtin that
would otherwise have nothing to call, and each is a kernel plus its gradient
rule, in the style of the ones already there.

| Missing | Wanted by | Go |
|---|---|---|
| `conv2d(a, b) -> Res[Tensor, Str]` | `conv2d` | `internal/tensor/conv.go` `Conv2D` |
| `maxpool2d(t, k) -> Res[Tensor, Str]` | `maxpool2d` | `internal/tensor/conv.go` `MaxPool2D` |
| `diff_axis(t, axis) -> Res[Tensor, Str]` | `diff` | `tensor.DiffAxis` |
| `roll_axis(t, shift, axis) -> Res[Tensor, Str]` | `roll` | `tensor.RollAxis` |
| `cumsum` `cumprod` `cummax` `cummin` `(t) -> Tensor` | the scan builtins with one argument | `internal/tensor/scan.go` |
| `cumsum_axis` `cumprod_axis` `cummax_axis` `cummin_axis` `(t, axis) -> Res[Tensor, Str]` | the same four with an axis | `internal/tensor/scan.go` |
| `from_nested(Nested) -> Res[Tensor, Str]`, `to_nested(t) -> Nested` | `tensor` | `tensor.FromNested`, `(*Tensor).ToNested` |
| `set_record_jets(Bool)`, `hessian(tp, root, leaf) -> Res[Tensor, Str]` | `hessian` | `internal/tensor/jet.go` |
| `new_tape`, `leaf`, `value_of`, `backward`, and the `t_` twins | `grad`, `grads`, `value_and_grad`, `jacobian`, `hessian` | the graph edges on `*Tensor` |

The tape entries are declared in `src/tensor.tw`'s header and not yet written;
they are listed here because `src/eval.tw` calls them today and because their
exact shape, `backward(tp, root, seed) -> Res[Arr[Tensor], Str]` returning leaf
gradients in creation order, is what makes the leaf ordering below `grads`
correct. `grads(loss)(W, b)` returns a list in the order the arguments were
named, and that order is the order `trace_arg` created the leaves in.

Two things about this list are load-bearing and easy to lose.

The `Res[..., Str]` returns are not decoration. `src/eval.tw` `lift` turns a
kernel's message into a runtime error carrying the call's line, and the message
text is compared byte for byte by the differential harness, so a kernel that
aborts instead of returning a message takes the line number with it.

Negative axes are normalised **inside** the kernel and not in `src/eval.tw`.
`src/tensor.tw` `normalize_axis` follows `internal/tensor/ops.go`: it adds the
rank first and then reports the *adjusted* axis if it is still out of range, so
`sum(m, -5)` on a rank-2 tensor says `axis -3 out of range for rank 2`.
Normalising on the eval side would report `-5` and diverge on the single input
that reaches the message.

## NEEDS-55: a seeded random number generator

**Status:** done (2026-08), and wider than asked. The four primitives are
there as `rng_seed`, `rng_uniform`, `rng_normal` and `rng_perm`, over the one
global stream. On top of them there is now a first-class generator as well,
`rng_open(seed)`, `rng_f64`, `rng_u53` and `rng_close`, which is what NEEDS-95
wanted and did not expect to get. Reproducibility was verified rather than
assumed: `seed(7)` twice gives the same `randn` both times.

`rng_seed(I64)`, `rng_uniform() -> F64`, `rng_normal() -> F64`,
`rng_perm(n) -> Arr[I64]`. Native: it is one generator's state for the whole
program, which is the thing the language has no way to own.

The contract is reproducibility, and it is stronger than "random": `seed(k)`
followed by the same sequence of `rand`/`randn`/`permutation` calls must give
the same numbers on every run and every platform, because that is what makes a
training run in `examples/` reproducible and what the corpus compares. The Go
side gets this from `math/rand`'s `Float64`, `NormFloat64` and `Perm` on an
explicitly seeded `*rand.Rand`, so the native core has to match those streams
bit for bit, not merely be seeded.

## NEEDS-56: the output sink for `print`

**Status:** done (2026-08). `emit_line(Str)` is a builtin and is the sink, so
`print` joins with spaces and adds nothing and the line ending belongs to
whoever supplies the sink. The argument below, that an evaluator writing to a
file descriptor could not be differentially tested at all, is why it is this
and not `write_out`.

`emit_line(Str)`. Not `write_out`: `interp.New` takes an `out func(string)` and
every caller supplies a different one. The test harness captures into a buffer,
the REPL interleaves with its own prompt, and only the `run` command writes to
stdout. The line ending belongs to the sink, which is why `print` joins with
spaces and adds nothing.

An evaluator that wrote to a file descriptor directly could not be tested by the
differential harness at all, so this is not a detail.

## NEEDS-57: value formatting

**Status: superseded by NEEDS-88. Read that entry first.** This entry asks for
`format_value` and `format_number` together and argues that neither can escape
`src/eval.tw`. Half of it is now false: `format_number(F64)` lives in
`src/float.tw`, which knows nothing about `Value` and never needed eval. Only
`format_value` still has the circular-import problem described below, and
NEEDS-88 states the remaining question accurately. The text is kept for the
reasoning about the import cycle, which is still correct as far as it goes.

*(Original text follows.)*

**Status:** blocking for `src/eval.tw`. `print`, `str`, `write_frame`, and the
`jacobian: f must return a tensor, got %s` message.

`format_value(Value) -> Str` and `format_number(F64) -> Str`, from
`internal/value`'s `Format` and `FormatNumber`. Not ported by anyone: `src/fmt.tw`
is the *source* formatter, the port of `internal/format`, so the obvious name is
taken and this needs a home of its own.

It cannot live in `src/fmt.tw` even if the name were free, because it formats a
`Value`, and `Value` is declared in `src/eval.tw`. A module holding it would
have to import eval, and eval has to call it, so either the two import each
other or it goes in `src/eval.tw`. There is no third option and the language has
no answer for the first one today.

It is also a bigger job than it looks: `FormatNumber` is the float rendering of
NEEDS-29, and every printed number in every `testdata/` expectation goes through
it.

## NEEDS-58: paths resolved against the running source file

**Status:** done (2026-08). `resolve_path(Str)` is a builtin and resolves
against the directory of the source file currently executing rather than the
process's working directory, which is the distinction the whole entry is about.
The observation that NEEDS-51's import resolver wants the same stack still
stands and they are still two things.

`resolve_path(Str) -> Str`: an absolute path unchanged, a relative one joined to
the directory of the source file currently executing, *not* the process's
working directory. `internal/interp` keeps a `srcStack` for this so that a
script reading `data.csv` next to itself works when run from anywhere.

The import resolver (NEEDS-51) needs the same stack, and they should be one
thing rather than two.

## NEEDS-59: reading and writing whole files

**Status:** done, with one shape not as requested (2026-08). `read_file` takes
a path and returns `Res[Str, Str]`, and `write_file(path, Str)` returns
`Res[Unit, Str]`. So the `Str`-rather-than-`Bytes` widening this entry argues
for is what landed, on both, and the second copy of the whole file that the
entry warns `read_csv` would otherwise allocate does not happen.

Where it differs from the request: the read is a `Res` and not the `Opt[Str]`
argued for here, so a caller like `read_csv` that reports
`read_csv: cannot read "..."` and discards the OS error does discard a message
it was handed. That is the cost the entry predicted, paid in the direction it
did not choose. `read_text_or` exists for callers that want a default instead.

NEEDS-28 has both, as `read_file(path) -> Res[Bytes, Str]` and `write_file`.
The shapes the frame builtins want differ, and the difference is not cosmetic:

- `read_file(path) -> Opt[Str]`. An option, because `read_csv` reports
  `read_csv: cannot read "..."` and discards the underlying OS error entirely,
  so a `Res` carrying a message the caller must then throw away is the wrong
  shape. `Str` rather than `Bytes` because every line of the file is then split
  and parsed as text.
- `write_file(path, Str) -> Res[Unit, Str]`, likewise taking `Str`. This is the
  same widening NEEDS-48 asks for on `write_out`, for the same reason.

Either NEEDS-28's signatures grow these, or the conversions happen at each call
site and `read_csv` allocates a second copy of the whole file.

## NEEDS-60: parsing a float the way Go does

**Status:** done (2026-08), as `str_to_f64(Str) -> Opt[F64]`. It is the same
primitive NEEDS-18 asks for, with the option return this entry needs, and it
calls `strconv.ParseFloat`, so Go's acceptance set comes with it: leading sign,
`inf`/`infinity`/`nan` case-insensitively, and hex float literals all parse.

The one difference from Go, and it is the one this entry cares about most,
because it is the difference between a corrupt column and silent numbers:
underscores are **stripped** before the parse rather than refused, so
`str_to_f64("1_0.5")` succeeds where Go's function fails. That is deliberate and
documented at the definition (it matches twill's own numeric literals), and it
means the acceptance set is a strict superset of Go's by exactly that one rule.
A CSV field containing an underscore therefore becomes a number here and an
error on the Go side.

`f64_parse(Str) -> Opt[F64]`. The runtime primitive table already lists
`f64_of_str` for the lexer, but the lexer only ever hands it text its own scanner
accepted. This one is handed arbitrary CSV fields and has to decide, so it needs
the option return and it needs Go's exact acceptance set: leading sign,
`inf`/`infinity`/`nan` case-insensitively, hex float literals, and underscores
refused. A parser that accepts a superset turns a corrupt column into silent
numbers; one that accepts a subset rejects files the bootstrap reads.

## NEEDS-61: `trim_space` over Unicode, not ASCII

**Status:** open, low priority, unchanged (re-checked 2026-08). There is no
`trim_space` primitive and no `unicode.IsSpace` predicate; `std/text.trim` is
the ASCII version. A CSV field with a non-breaking space around a number still
parses on the Go side and fails here.

`strings.TrimSpace` strips every Unicode space, including U+00A0 and the
ideographic space. The twill version strips the six ASCII ones, which is every
byte a CSV realistically contains and not every byte Go would strip. A file with
a non-breaking space around a number parses on the Go side and fails here.

Either a `trim_space` primitive with Go's semantics, or `unicode.IsSpace` as a
predicate the twill loop can call.

## NEEDS-62: `Nested`, and where it belongs

**Status:** done. `Nested` is declared in `src/tensor.tw` next to `from_nested`
and `to_nested`, and `src/eval.tw` `value_to_nested` builds a `tensor.Nested`.
The stopgap copy in `src/eval.tw` is gone.

`tensor([[1, 2], [3, 4]])` goes through an intermediate that is a number or a
list of them nested to any depth, and `tensor.FromNested` reads the shape off it
and refuses a ragged one. `src/eval.tw` declares the enum because
`src/tensor.tw` does not have it, which is the wrong place: `from_nested` and
`to_nested` are the tensor engine's and the type they speak should be too. Moving
it is a one-line change once the kernel set lands, and it is recorded so it does
not become permanent by default.

## NEEDS-63: opaque values from the native core

**Status:** done (2026-08). `gbm_fit`, `gbm_predict` and `gbm_describe` are
builtins, so the opaque-handle design below is what the runtime implements and
`internal/gbm` stays native as argued.

A fitted model is a value twill holds and cannot look inside. `src/eval.tw` adds
`VForeign(ForeignVal { kind, handle })` for it: a string naming the kind and an
opaque handle the core resolves. `gbm_predict` checks the kind, which is what
makes `gbm_predict(3, X)` report *first argument must be a model from gbm_fit*
rather than crashing in the core.

The primitives:

- `gbm_fit(Arr[F64], Arr[F64], I64, I64, GbmParams) -> Res[I64, Str]`
- `gbm_predict(I64, Arr[F64], I64, I64) -> Res[Arr[F64], Str]`

`internal/gbm` is 900 lines of tree building and it is the one part of the
builtin surface that is neither a tensor kernel nor twill's business. It stays
native. The handle has to survive `save` and `load` (NEEDS-64), which is the
part that makes it more than an integer.

What is *not* deferred: `GbmParams` and its defaults are declared in
`src/eval.tw`, because the defaults are part of what `gbm_fit(X, y)` means to
someone reading a twill program.

## NEEDS-64: `save` and `load`

**Status:** done (2026-08). `save_value` and `load_value` are builtins and
round-trip: a saved `arr(1, 2)` loads back as `Ok([1, 2])`. `save` and `load`
are the tensor-shaped pair beside them. The format is still the contract, which
is the sentence in this entry with teeth, and it is now a contract with a
running implementation on one side of it rather than none.

`save_value(Value, Str) -> Res[Unit, Str]` and
`load_value(Str) -> Res[Value, Str]`, from `internal/interp/serialize.go`.

The format is the contract: a file written by the bootstrap must load in the
self-hosted evaluator and the other way round, or a model trained before the
switch is lost. That makes this the one primitive here whose *encoding* is part
of the specification rather than an implementation detail, and it covers
tensors, lists, records and the gbm model of NEEDS-63.

Porting the encoder to twill is possible for everything except the model, so the
seam is the same one either way, and it is recorded as native for that reason.

## NEEDS-65: `f64_trunc`, `f64_floor`, `f64_ceil`, `f64_round`

**Status:** done (2026-08). `f64_trunc`, `f64_floor`, `f64_ceil` and
`f64_round` are builtins alongside `f64_mod` and `f64_pow`. The warning below is
the part worth keeping: `f64_round` is half away from zero, matching
`math.Round` and not the round-half-to-even a reader coming from Python or IEEE
would assume.

Alongside the `f64_mod` and `f64_pow` already in the runtime primitive table.
`f64_round` is half away from zero, matching `math.Round` and not the
round-half-to-even a reader coming from Python or from IEEE would assume, and
the difference shows on exactly the inputs a test would use.

## NEEDS-66: three builtins the checker's table does not know

**Status:** done (2026-08). `argsort`, `argtopk` and `split` are all in the Go
checker's `builtinNames` and all three call and run, so the failure mode this
entry describes, reported as an undefined variable by the checker and then
working when run, is gone. It was a bug on the checker's side, and it was fixed
on both sides at once as the entry required.

`argsort`, `argtopk` and `split` are defined in
`internal/interp/builtins.go` and ported in `src/eval.tw`, and they are absent
from `src/check.tw`'s `builtin_names`. A program calling one of them is reported
as an undefined variable by the checker and then works when run.

The Go checker has the same list, so fixing it means fixing both or the
diagnostics diverge. Related to NEEDS-52, but separate: NEEDS-52 is that there
are two tables, this is that they already disagree.

## NEEDS-67: mutating a struct through a parameter

**Status:** answered. It was a semantics question, not a task, and the answer is
handle semantics, which is what `src/` already assumed. **The normative text is
`docs/language-guide.md`, `struct`, and what a parameter is.** `src/eval.tw`
`gbm_opts_from_record` is correct as written and does not have to return the
params.

That function takes a `GbmParams` and assigns to its fields, and the caller
expects to see the changes. Whether it does depends on whether a struct is
passed by handle or by value, which `docs/self-hosting.md` does not say. The
same question decides whether `Env`, `Tape` and `Printer` work at all, so it is
already answered implicitly everywhere in `src/`, but it is answered by
assumption and not by a rule.

The assumption throughout `src/` is that a struct is a handle and assignment
through it is visible to the caller, exactly as a Go pointer receiver is. If the
answer turns out to be by-value, `gbm_opts_from_record` has to return the params
and several other things in `src/` break more quietly than it does.

---

## Tensor kernels and autodiff

The entries below are what `src/tensor.tw` reaches for now that the kernels and
the gradient rules are in twill rather than deferred to a native core. NEEDS-25
described the calling convention into that core; there is no core, so what it
asked for is replaced by the handful of genuine primitives listed here. Nothing
in `src/tensor.tw` needs a foreign call any more.

## NEEDS-68: the transcendental float primitives

**Status:** done (2026-08). `f64_exp`, `f64_log`, `f64_sin`, `f64_cos`,
`f64_sqrt` and `f64_tanh` are all builtins, implemented natively rather than as
foreign calls, which is what the entry argues for below. Agreement in the last
bits comes from their being Go's `math` rather than from a series expansion, so
the requirement is met by construction.

`f64_exp`, `f64_log`, `f64_sin`, `f64_cos`, `f64_sqrt`, `f64_tanh`. These have
to be native primitives, not twill: under the no-Go rule there is no bootstrap
to call into, and a series expansion written in twill would not agree with
`math.Exp` in the last bits.

Agreement in the last bits is the requirement, not a nicety. `testdata/` compares
output byte for byte after a canonical float rendering (NEEDS-29), so an `exp`
that is one ulp off turns every test touching a sigmoid into a diff. Whatever
supplies these must be the same implementation the bootstrap used, which in
practice means Go's `math` or a faithful port of it.

`f64_pow` is already in the runtime primitive table and `f64_floor` is NEEDS-65;
neither is repeated here.

*Go bootstrap:* `math.Exp` and friends, called from `internal/tensor/tensor.go`.

## NEEDS-69: `f64_signbit`

**Status:** done (2026-08). `f64_signbit` is a builtin and
`f64_signbit(-0.0)` is `true`, so `f64_max` and `f64_min` can tell the two zeros
apart. The reason it was recorded rather than skipped, that the symptom is an
infinity of the wrong sign in a gradient, is why it is worth having closed.

`math.Max(-0, +0)` is `+0`, and a comparison chain cannot tell the two zeros
apart. The only way to reproduce it is to ask which zero it is.

Low priority because the sign of a zero is invisible until something divides by
it, and it is recorded rather than skipped because when it does show up it shows
up as an infinity of the wrong sign in a gradient, which reads as a bug in the
gradient rather than in `max`.

*Go bootstrap:* `math.Signbit`.

## NEEDS-70: equality on a payload-free enum case

**Status:** done (2026-08, 1.6). `==` on two values of the same payload-free
enum compares the case: `Red == Red` is `true`, `Red == Green` is `false`. The
same change made payloads compare structurally (`Some(1) == Some(1)`, NEEDS-27),
so it went further than this entry deliberately asked for, and the question the
entry sidesteps, what a payload comparison means, was answered as deep equality
rather than left open. `is_same_op`'s string compare can go.

`Op` has forty-odd cases and none carries a payload. Asking whether a value is
`OpAdd` currently means a `match` with forty arms, or `is_same_op`, which
compares the rendered names and is both slow and a lie about what it is doing.

What is wanted is `==` on two values of the same payload-free enum, comparing
the case and nothing else. This is narrower than deriving equality for all
enums, which would have to decide what a payload comparison means, and it covers
the case that actually appears.

Without it `vjp`'s dispatch is a string compare per op per backward pass, which
is not a correctness problem and is an embarrassing one.

*Go bootstrap:* `internal/interp/builtins.go` dispatches on a string name, so it
has the same shape and the same cost, and gets away with it because the Go map
lookup is one hash.

## NEEDS-71: an `Arr` parameter aliases the caller's array

**Status:** done (2026-08). An `Arr` parameter aliases the caller's array,
verified both ways at once: a function that does `buf[0] = 42` and `push(buf, 7)`
through its parameter leaves the caller holding `[42, 2, 7]`. So `accumulate`,
`odo_step` and every kernel that fills a buffer it was handed work as written,
and the failure this entry names, a backward pass that silently returns zeros,
does not happen. The answer agrees with NEEDS-67's for structs, which it had
to. **The normative text is `docs/language-guide.md`, `struct`, and
what a parameter is**, which states the `Arr` rule and the `struct` rule in one
place precisely because `Odometer` is mutated through both at once. An `Arr`
parameter aliases; the backward pass does not return zeros.

`accumulate(cot, touched, node, buf)` mutates `cot[node].data` and expects the
caller to see it. `odo_step(odo)` advances a struct's arrays in place. If an
`Arr` parameter is copied rather than aliased, every one of those is a silent
no-op and the whole backward pass returns zeros.

This is the array half of NEEDS-67, which asks the same question about a struct.
The two answers have to agree, because `Odometer` is a struct holding arrays and
is mutated through both at once.

The bootstrap's answer is aliasing, because a Go slice is a header over shared
storage, so that is the answer `src/` is written against.

*Go bootstrap:* Go slices.

## NEEDS-72: nested containers

**Status:** done (2026-08). All three shapes verified: `Arr[Arr[I64]]` indexes
two deep, `Arr[Bool]` holds and prints, and a list of tensors indexes to a
tensor. No variance and no covariant assignment was added, which is what the
entry asked for: the element type is any type the subset has.

`docs/self-hosting.md` section 1.2 lists `Arr[T]` without saying whether `T` may
itself be a container or a struct. Every entry above needs it to be.

Nothing exotic is wanted: no variance, no covariant assignment, just the
element type being any type the subset already has. It is listed because a
straightforward reading of the section is that `Arr` holds scalars, and the
tensor kernels would then need a hand-rolled flattening for each of the five
uses above, which is five chances to get an index wrong.

*Go bootstrap:* `[][]int`, `[]*Tensor`, `[]bool`.

## NEEDS-73: `abort` in value position

**Status:** done (2026-08). `abort` is usable as an expression, so a `_ =>
abort("bad")` arm sits beside arms of type `F64` and the match checks. The
alternative the entry rejects, a sentinel float that a wrong op silently
computes with, did not have to be taken.

Both end in a `_ =>` arm that calls `abort` because the op passed was not of the
kind the function handles. The arm has to have the same type as the others,
which is `F64`, so `abort` has to be usable as an expression of any type and be
understood by the checker as never returning.

The alternative is returning a sentinel float and letting a wrong op silently
compute with it, which is worse in exactly the way this file is trying to avoid.

*Go bootstrap:* `panic` is a statement in Go and these branches are written as
an early `return` there, which is available because the Go functions are not
expression-bodied.

## NEEDS-74: rendering an `Arr[I64]` the way Go's `%v` does

**Status:** open, diagnostics only, unchanged (re-checked 2026-08). One
message, and the decision below (either the Go side switches to the shape
rendering or a second renderer exists) has not been made.

The invalid-permutation message renders the axes with `shape_string`, which
produces `[1, 0]`. `internal/tensor/ops.go` uses `%v` on a `[]int`, which
produces `[1 0]`, with spaces and no commas.

Every other shape in a diagnostic goes through `shape_string` on both sides and
matches. This one does not, because Go is printing an axis list rather than a
shape. Either the Go side switches to the shape rendering, or a second renderer
exists for it. It is one message and it is written down so the differential
harness's first complaint is not a surprise.

*Go bootstrap:* `fmt.Errorf` with `%v`.

## NEEDS-75: a tape the interpreted code records on

**Status:** done. `src/eval.tw` has `tape_push`, `tape_pop` and `tape_node_of`
over a stack of tapes with dynamic extent, and one `tr_` shim per differentiable
kernel routing to the taped twin while a tape is installed. What is left of this
entry is the two costs it exposed, NEEDS-81 and NEEDS-82.

The original text follows, because the two design decisions in it are the ones
the implementation was made to satisfy and are worth checking it against.

**Was:** blocking for `src/eval.tw`. `grad`, `grads`, `value_and_grad`,
`jacobian`, `hessian`. It is the one hole in `src/eval.tw` that is in the middle
of something rather than at its edge.

`src/tensor.tw` splits every op in two: `binary(op, a, b)` computes, and
`t_binary(tp, op, a, b)` computes and records on a tape, returning a node index.
That split is right, and it is exactly why grad is not just a matter of calling
kernels: the arithmetic that has to be recorded happens inside f, which is
interpreted twill, in code `src/eval.tw` evaluates but does not write.

Three functions stand for the missing piece:

    tape_push(tp)                  make tp the tape ops record on
    tape_pop()                     restore the previous one, or none
    tape_node_of(v) -> Opt[I64]    the node a traced value came from

The Go bootstrap needs none of them, because a `*tensor.Tensor` carries its own
graph edges: a value *is* a node, and `Backward` walks from it. Here a `Tensor`
is a shape and a buffer, node identity lives in the tape, and the association
between the two has to exist somewhere. These three are that somewhere.

Whichever way it is answered, two things follow and both are design decisions
rather than coding:

- **Every builtin needs a taped path.** `relu(x)` inside a loss has to record;
  `relu(x)` in a print statement should not, or every forward pass pins its
  whole history alive. `src/eval.tw` calls the untaped kernel today. The natural
  answer is that each builtin asks whether a tape is installed and picks the
  twin, which doubles nothing but touches every call site.
- **Nesting.** `tape_push`/`tape_pop` are a stack rather than a single slot
  because `jacobian(f)` runs f once per output element and `grad` inside a
  function passed to `map` is legal. A single slot would make the inner call
  silently steal the outer tape's recording.

The alternative, giving `VTensor` a node field, was not taken here because it
would put a gradient concept into the value model that most values never use,
and `src/tensor.tw` deliberately kept `Tensor` a shape and a buffer. Recorded so
the choice is visible rather than assumed.

## NEEDS-76: `f64_shortest`, Go's `%g` for the source formatter

**Status:** done (2026-08), as the `f64_to_str` builtin, which is literally
`strconv.FormatFloat(x, 'g', -1, 64)`, and `std/float.f64_shortest` delegates to
it on the bootstrap. So the four bullets below hold by construction rather than
by transcription, and the exponent threshold of 6 that the differential run
caught cannot drift. Checked anyway against the binary: `1234567.5` gives
`1.2345675e+06`.

`f64_shortest(F64) -> Str`, which must equal Go's
`strconv.FormatFloat(x, 'g', -1, 64)` exactly. This is not NEEDS-57's
`format_number` (that one renders a `Value` for `print`) and it is not
NEEDS-29's hexadecimal dump form. It is the decimal spelling a number literal
gets when `twill fmt` writes it back out, and it is compared byte for byte
against the Go formatter over the whole corpus.

The contract is narrower than "prints a float", and the differential run caught
the part that is easy to get wrong, so it is written down here rather than left
to the reader of `strconv`:

- Shortest digits that round-trip back to the same `F64`.
- Exponent form when the decimal exponent is `< -4` or `>= 6`. Not 21. Go uses
  a precision of 6 for this decision when the precision is "shortest".
- The exponent is at least two digits and always signed: `1e-07`, `1e+20`.
- Negative zero prints as `-0`.

Measured against the reference binary: `1234567.5` gives `1.2345675e+06`,
`123456.5` gives `123456.5`, `1e20` gives `1e+20`. `1000000` never reaches this
function, because `format_number` takes the integer path first, which is why the
threshold of 6 is invisible until a value has a fractional part.

*Go bootstrap:* `strconv.FormatFloat`, through `internal/format`'s
`formatNumber`.

## NEEDS-77: the formatter drops `unit USD`

**Status:** done (2026-08, 1.6), and fixed in both implementations in one
change, which is what this entry was waiting for and the reason it stayed open
so long. `internal/format/format.go`'s statement switch has a `*ast.UnitDecl`
case, `src/fmt.tw` has the matching one, and `twill fmt` on a file beginning
`unit USD` now prints `unit USD` instead of deleting it. A file that declares a
base unit survives a format.

`internal/format/format.go`'s statement switch has no case for `*ast.UnitDecl`,
so a `unit` declaration formats to nothing and a file that declares a base unit
loses it. `src/fmt.tw` reproduces this deliberately: the cross-agreement check
compares bytes, so fixing it in one implementation alone would turn a silently
dropped declaration into a reported divergence and bury the real problem under
a harness failure.

Fix both together, in one change, or the corpus goes red. The fix is one line on
each side, printing `unit <name>` through the same trailing-comment path every
other single-line statement uses.

## NEEDS-78: the formatter does not preserve blank lines

**Status:** done in `src/fmt.tw`, as a deliberate divergence from the bootstrap.
`maybe_blank` re-emits one blank line wherever the source had a gap of two or
more lines between consecutive statements, measured from the previous
statement's `stmt_end_line` to the leading edge of the next (its first own-line
comment, or the statement itself), so a comment sitting directly under a
statement is not mistaken for a break and a multi-line statement's own span is
not either. The three statement lists, the program body, an inline `{ }` block,
and a braced block body, all go through it.

The owner chose to let twill's formatter be the better one here rather than
reproduce `internal/format/format.go`, which drops the blanks, so the two
disagree on this point until the bootstrap is retired and the fmt goldens are
regenerated from twill when it can run. One case is left: a blank line between
two consecutive own-line comments is not preserved, because `emit_leading`
emits comments back to back; the entry's target, breaks between statements, is.

*(Original: open, cosmetic. `src/fmt.tw`, and `internal/format/format.go`
equally.)*

The printer emits one line per statement and nothing else, so the blank lines a
author put between paragraphs of a function are gone after one format. Comments
survive; the whitespace that groups them does not.

Preserving them needs the tree to carry the gap: the statement line numbers are
already there, so a gap of two or more source lines between consecutive
statements could re-emit as one blank line. That rule is not in the Go file to
copy, which is exactly why it is recorded here rather than invented in the port.
Whatever is chosen has to be chosen on both sides at once, for the same reason
as NEEDS-77.

## NEEDS-79: a `Dict` keyed by `I64`

**Status:** the workaround moved into the runtime (2026-08); the type is still
a lie. `dict_set(d, i64(3), "x")` and `dict_get(d, i64(3))` both work now, so
`src/fmt.tw` no longer has to call `str()` at every set and every get. What the
runtime does underneath is the same decimal conversion: `i64(3)` and `"3"` are
the **same key**, verified, so a `Dict` that mixes them silently collides. That
is fine for `Printer.trailing`, whose keys are all line numbers, and it is not
the "`Dict` takes any equatable key" this entry asks for. The same relaxation is
what NEEDS-81 wants for identity keys, and neither has it.

The formatter maps a source line to the trailing comment on it. The natural type
is `Dict[I64, Str]`; `docs/self-hosting.md` only specifies `Str` keys, so the
line number is rendered with `str()` at every set and every get. That is a
decimal conversion per statement printed, and it makes the key type a lie about
what the map is for.

Either `Dict` takes any equatable key, or this stays a documented workaround.
It is not blocking and it is not free: the same pattern will appear anywhere the
compiler wants to key on a node id, which `src/ast.tw` hands out precisely so it
can be keyed on.

## NEEDS-80: `twill tokens` is not a command

**Status:** recorded so it is not read as an accident. `src/main.tw`.

The earlier draft of `src/main.tw` had `twill tokens <file>` and
`twill dump --dump=tokens`, and `src/lex.tw`'s `dump_tokens` was written for
them. `cmd/twill/main.go` has neither, and this file is compared against it with
stderr byte for byte, so a command the reference binary does not have would make
`twill tokens x.tw` print a token stream on one side and
`twill: cannot read file "tokens"` on the other.

`dump_tokens` stays in `src/lex.tw` and the lexer's differential check should
call it directly rather than through the CLI. If a token dump is wanted from the
command line, it has to be added to the Go binary first.

## NEEDS-81: a map keyed by value identity

**Status:** open, a workaround is in place and it is quadratic, unchanged
(re-checked 2026-08). `is_same` exists as a builtin (NEEDS-21) so the comparison
is available, and there is still no way to *key* a `Dict` on it: `Dict` accepts
a `Str` or an `I64`, and the `I64` is a decimal string underneath (NEEDS-79).
So the backwards linear scan stands and the O(n^2) stands with it.
`src/eval.tw` `tape_node_of_tensor`.

The tape seam has to answer "which node did this tensor come from", and the
answer has to be by identity: two tensors holding equal data are different graph
nodes, and NEEDS-27's deep equality is the wrong question. The workaround is a
backwards linear scan of `tp.entries` calling `is_same`, so a forward pass over
a tape of n entries costs O(n^2) identity comparisons.

What is wanted is a `Dict` whose key is the identity of a heap value, the thing
NEEDS-21's `is_same` compares, rather than a `Str`. NEEDS-79 asks the adjacent
question for `I64` keys; the two want the same relaxation of the key type from
one concrete type to any type with an equality.

Recorded rather than fixed because the scan is obviously correct and a wrong
answer here does not fail loudly: it returns the wrong node and the gradient
comes back plausible and wrong.

*Go bootstrap:* none needed. A `*tensor.Tensor` is its own node, so the lookup
does not exist there at all, which is why this cost is new rather than ported.

## NEEDS-82: file-level state that is mutated in place

**Status:** answered, both halves (2026-08). The aliasing half was already
settled and is below. The other half is now measured rather than assumed: a
file-level `let` **may** be initialised by a call, the initialiser runs **once**
at module load, and the array it returns is **shared**, so pushing to it from
anywhere lengthens the one array and a function reading it sees the push. That
is the same measurement NEEDS-86 wanted from the immutable side, and it answers
both.

One caveat held briefly and does not any more: `let TAB: Arr[I64] = mk()` drew a
false "declared Arr[I64] but the value is Unit" when `mk` had a block body,
because the call was typed from the body rather than from the signature. Fixed
before 1.6 shipped; see NEEDS-49.

The aliasing half is settled: **`docs/language-guide.md`, `struct`, and what a
parameter is** says an `Arr` is a handle and copying is always explicit, so
pushing to `TAPES` from anywhere mutates the one array. The other half, whether
a file-level `let` may be initialised by a call and whether that call runs once,
is not answered here and is the same question as weft's entry 9 (`docs/roadmap.md`
entry 28).

`let TAPES: Arr[tensor.Tape] = arr_new()` at file level, pushed and popped for
the dynamic extent of a differentiated call. Two things have to be true for it
and `docs/self-hosting.md` says neither: a file-level `let` may be initialised
by a call rather than only by a literal, and pushing to a file-level `Arr`
mutates the one array rather than a per-reference copy. The existing file-level
bindings in `src/eval.tw` are all `Str` literals, so nothing has tested either.

The alternative, threading the tape through every `eval_*` function and every
builtin, is rejected in the comment above `TAPES` and the reason is semantic
rather than ergonomic: a threaded tape is lexical, and a closure captured before
the `grad` would then carry no tape and record nothing.

*Go bootstrap:* `internal/tensor/jet.go`'s package-level `recordJets`, set and
cleared by `internal/interp/builtins.go` around the graph build. Same lifetime,
same single-threaded assumption; a stack rather than a bool because grad nests
and `SetRecordJets` does not have to.

## NEEDS-83: the ops with no forward-mode rule

**Status:** done. Every op has a forward-mode rule now, so `hessian` works over
all of them. The rearrangements and selections, `concat`, `sort`, `topk`,
`median` and `maxpool2d`, gather their tangents by the same mapping their vjp
scatters. `softmax`, `logsumexp` and `prod` carry a genuine second-order term:
softmax and logsumexp through the jacobian `y_i(delta_ij - y_j)`, prod through
`p*(u*u + v - w)` with the zeros split into cases. `conv2d` is bilinear, so its
jet is the product rule `xdd*w + 2*xd*wd + x*wdd` over the receptive field.
`sort` and `topk` onward were added to `internal/tensor` and `src/tensor.tw` at
once, the owner having authorised the bootstrap edits, and `jet_test.go` checks
each against finite differences, with the zero and cross-term cases that finite
differences cannot reach checked against exact derivatives.

## NEEDS-84: `f64_bits` and `f64_from_bits`

**Status:** done (2026-08, 1.6). `f64_bits` and `f64_from_bits` are builtins
and are exact now that `I64` is a real int64 (NEEDS-2): `f64_bits(1.0)` is
`4607182418800017408` and `f64_from_bits` of that is `1`. Before 1.6 the pattern
came back through a float64 and the round trip lost the low bits, which is what
made this blocking rather than merely missing. `f64_bits_hi`, `f64_bits_lo` and
`f64_from_halves` are still there beside them, and are the compensation that is
no longer needed.

`f64_bits(F64) -> I64` and `f64_from_bits(I64) -> F64`, the IEEE 754 bit
pattern of a double and its inverse. Go's `math.Float64bits` and
`math.Float64frombits`, which are a reinterpretation of the same eight bytes and
not a conversion.

These are the only two primitives `src/float.tw` needs, and that is the point
worth recording. Formatting and parsing a float are entirely integer and string
work once the sign, exponent and mantissa are in hand; what cannot be written in
twill is getting at them. `i64_of_f64` is not a substitute, because it truncates
toward zero and therefore destroys exactly the information wanted.

They are also the only place the systems subset has to admit that `F64` has a
representation. Everywhere else it is a number.

*Go bootstrap:* `math.Float64bits`, reached through `strconv`.

## NEEDS-85: `shr` on `I64` is arithmetic, and there is no unsigned anything

**Status:** the semantics are decided, written down and now verified against a
real `I64` (2026-08, 1.6); the `U64` half is still open and the workaround is
still in place and still correct. `shr` is arithmetic (`shr(i64(-8), 1)` is
`-4`), `shl` shifts zeros in, and 1.6's exact 64-bit integers mean the sign bit
is now reachable: `shl(i64(1), 63)` is `-9223372036854775808` and round-trips,
where before it was lossy above 2^53. So the intermediate values this entry is
about, `10 * 2^60 + 9` and the rest, are now representable, and `ushr`, `udiv10`
and `unonzero` are compensating for the absence of an unsigned type rather than
for the absence of exact integers. They still work and they are still the named
idiom. **The normative text is `docs/language-guide.md`, Operators →
Bitwise operators on `I64`.** Read it there, not here.

The decision: `shr` is an **arithmetic** shift, `shl` shifts zeros in, and shift
counts are masked to 0..63. A logical right shift is spelled by building one,
and `src/float.tw`'s `ushr` is the named idiom.

Why it had to be decided rather than measured: the Go bootstrap does not
implement `mode systems`, `I64`, or any bitwise operator, so there was no
running implementation to appeal to. `shr` is a name the systems-mode sources
use and that nothing yet defines. Arithmetic was chosen because it is what the
`internal/strconv` original does on `int64`, because `src/float.tw` and
`std/random.tw` were already written against it and already carry the compensating
helper, and because a language whose only integer type is signed should have its
shift agree with division by a power of two.

The cost of leaving it unstated was not theoretical: loom's `src/rng.tw` had
assumed the opposite, so its splitmix64 finaliser was a different generator, and
nothing would have reported it. That is fixed in loom.

`docs/self-hosting.md` section 1.2 specifies `and or xor shl shr not` on a
two's-complement `I64` and does not say what `shr` does with the sign bit.
`src/float.tw` assumes Go's answer, which is arithmetic: `shr` on a negative
value shifts ones in from the top.

That assumption is load-bearing rather than incidental. The decimal shifts carry
intermediate values up to `10 * 2^60 + 9`, which is inside 64 bits but past
`2^63`, so the sign bit is set on values that are not negative numbers at all.
Go runs that arithmetic on `uint` and picks its shift chunk size for it. The
subset has no unsigned type, so `src/float.tw` carries three helpers:

- `ushr`, a logical right shift, built by clearing the sign bit and putting it
  back at its shifted position;
- `udiv10`, an unsigned divide by ten, built as `(x >>> 1) / 5`, which is exact
  because `floor(floor(n/2)/5)` is `floor(n/10)`;
- `unonzero`, spelled out because `x > 0` is false for precisely the values the
  loops have to keep going on.

Two things would settle this. `shr` being specified as arithmetic, so the
helpers are correct rather than lucky. And, separately, whether the subset wants
a `U64`: the answer is probably no, since three helpers in one file is a smaller
cost than a second integer type in the checker, but the decision should be made
rather than inherited.

*Go bootstrap:* `uint` in `internal/strconv/decimal.go`.

## NEEDS-86: a file-level `let` initialised by a call

**Status:** answered, and it works (2026-08). Measured rather than assumed: a
file-level `let` bound to a call runs its initialiser once at module load, before
any function that reads it, and the array it returns is shared rather than
rebuilt per read. So `left_shift` does not reconstruct a 61-entry table on every
call and the formatter's cost stays linear. What is still luck rather than design
is the ordering question this entry raises in its last sentence: nothing here
reads another of the three, and initialiser order between file-level bindings has
not been specified. The next file will not be so lucky.
`src/float.tw` `LC_CUTOFF`, `LC_DELTA`, `POWTAB`.

Three constant tables are built by a function and bound at file level:

    let LC_CUTOFF: Arr[Str] = leftcheat_cutoffs()

`src/lex.tw` already does this for its byte constants, so the form is not new,
but those are scalars from a one-line function and these are 61-element arrays
built with `push`. What is being assumed is that the initialiser runs once, at
module load, before any function that reads it, and that the array it returns is
shared rather than rebuilt per read.

If it is rebuilt per read, `left_shift` reconstructs a 61-entry table on every
call and the formatter's cost goes from linear to quadratic in the digit count,
silently. If the initialisers run in a different order than they are written,
nothing here breaks, because none of the three reads another, but that is luck
rather than design and the next file will not be so lucky.

This is the same question NEEDS-82 asks about mutable file-level state, from the
immutable side.

## NEEDS-87: NEEDS-29 is answered, and its advice is now wrong

**Status:** resolved by `src/float.tw`. Recorded because NEEDS-29 still says the
opposite and a reader will find it first.

NEEDS-29 says a canonical float rendering should be a runtime primitive calling
the same code the Go side calls, and warns that reimplementing Ryu or Grisu in
twill is a way to lose a month. That was correct advice while calling into the
bootstrap was allowed. Under the no-Go rule there is nothing to call, so the
choice is between a port and no float output at all.

`src/float.tw` is the port, and the warning was avoided rather than ignored: it
implements Go's exact multiprecision-decimal path, not Ryu. That path is
definitional rather than heuristic. Go's fast paths are only permitted to answer
when they can prove they agree with it, so porting it reproduces Go by
construction, whereas porting Ryu reproduces Go only if six hundred table
entries survive transcription, and there is no way to run twill to find the one
that did not.

Three renderings came out of reading `internal/`, not one, and this is the part
NEEDS-29 obscures by saying "a stable canonical rendering" in the singular:

| Caller | Verb | Entry point |
|---|---|---|
| `internal/value.FormatNumber`, which is `print` | `'f'`, precision 6, then trailing zeros and a trailing point trimmed, behind an integer fast path | `format_number` |
| `internal/format`, the source formatter | `'g'`, precision -1 | `f64_shortest`, NEEDS-76 |
| `cmd/twill/dump.go`, the canonical dump | `'x'`, precision -1 | `f64_hex` |

Anyone who reads NEEDS-29 and implements `%g` for `print` has written the wrong
function. `print(0.1)` is `0.1` under both, but `print(1/3)` is `0.333333` and
not `0.3333333333333333`, and `print(1e300)` is 301 digits and not `1e+300`.

Two more inherited behaviours, both confirmed against the reference binary
rather than reasoned about, because both look like bugs:

- `print(-0.0)` is `0`. Negative zero equals `float64(int64(-0.0))`, so it takes
  the integer path, which has no sign to print.
- `print(-1e-9)` is `-0`. Precision 6 gives `-0.000000` and trimming leaves the
  sign behind. Any value under half a millionth in magnitude prints as a signed
  zero.

`f64_of_str` is in the same file for the same reason: NEEDS-29's rendering has
to round-trip, and a parse that accumulates digits and multiplies by a power of
ten makes `0.1` a different float from Go's before the program runs.

## NEEDS-88: `format_number` for a `Value` still has no home

**Status:** done. `src/eval.tw` `format_value`, and it needed no new import at
all. The walk over tensors, lists, records and closures lives in eval; the
per-number rendering routes through `tensor.dt_shortest`, which eval already
imports for the dtype work and which calls `float.format_number` for f64. So the
circular-import problem NEEDS-57 feared never had to be paid: eval does not
import float, it goes through tensor, which it imports anyway. An f64 tensor
prints exactly as `internal/value.Format` does, so the goldens are untouched; a
narrow tensor prints its dtype after the shape and its elements at their own
precision (NEEDS-114). The original narrowing below is kept for the reasoning.

*(Original status: open, and narrowed. `src/eval.tw`.)*

NEEDS-57 asks for `format_value(Value) -> Str` and `format_number(F64) -> Str`
together, and argues that neither can live outside `src/eval.tw` because `Value`
is declared there and a module holding them would have to import eval while eval
calls them.

Half of that is now settled: `format_number(F64)` is in `src/float.tw`, which
imports nothing but `bytes.tw` and knows nothing about `Value`. The scalar case
never needed eval.

`format_value` genuinely does, because it walks tensors and records. So the
circular-import problem NEEDS-57 describes is real but smaller than it looked,
and the answer is that `src/eval.tw` imports `src/float.tw` and keeps only the
walk. Recorded rather than left implicit, because the obvious reading of
NEEDS-57 is that both halves have to go in eval, and putting `format_number`
there would mean the source formatter and the checker cannot reach it.

## NEEDS-89: a round-trip float rendering the standard library can call

**Status:** done, by the second option. `src/float.tw` moved to `std/float.tw`,
the one location both halves reach: an embedded std module can only import
`std/...`, and the compiler's `src/` files already may. It imports nothing from
the compiler tree now, the byte-buffer calls are the runtime primitives directly
as `std/text` uses them and `str_eq` replaces the one `bytes.equal`, so the
dependency does not invert. `std/json` `number_str`, `src/fmt.tw`, `src/tensor.tw`
and `src/buf.tw` all import `std/float`; the first two were calling `f64_shortest`
bare and dangling, and now resolve. The f64 rendering is unchanged, so nothing
that compares against the bootstrap moves.

The parse side landed too, in a follow-up. `f64_of_str` was being called bare in
`src/eval.tw` (CSV and frame reads), `src/parse.tw` (numeric literals) and
`std/json.tw`'s number reader, all dangling; each now goes through `flt.f64_of_str`
against the moved module. Two corrections fell out: `src/eval.tw` was spelling it
`f64_parse` and matching `Some`/`None`, but `f64_parse` returns a `ParsedF64`
struct and `f64_of_str` is the `Opt[F64]` it wanted; and `std/json` was handing
an `Opt[F64]` to `JNumber`, which takes an `F64`, so it now matches the option
and fails the parse on `None` rather than assuming. That closes the `f64_of_str`
reachability half of NEEDS-18 for these callers. The original text follows.

*(Original: open, blocking for `std/json.tw`. `number_str`.)*

`f64_shortest(F64) -> Str`, the shortest decimal that parses back to the same
double, reachable from `std/`.

`src/float.tw` already implements exactly this algorithm, for the source
formatter, and answers NEEDS-29 with it. The problem is not the algorithm, it is
where it lives: `src/` is the compiler and `std/` is the library the compiler
compiles, so a std module importing the compiler inverts the dependency, and the
alternative of a second copy in `std/` is the one thing NEEDS-29 warns against
by name.

The reason the obvious substitute does not work is worth stating, because it
looks like it should. `str(x)` is `internal/value.FormatNumber`: `'f'` with a
precision of 6, trailing zeros trimmed, behind an integer fast path. So
`str(1.0 / 3.0)` is `"0.333333"`, and a JSON document rendered through it and
parsed back is not the document it started as. Round-tripping is the one
property a serialiser has to have.

Either `f64_shortest` becomes a runtime primitive alongside `f64_of_str`, or
`src/float.tw` moves somewhere both halves of the tree can reach. The second is
probably right and is a layering decision rather than a coding task.

*Go bootstrap:* `strconv.FormatFloat(x, 'g', -1, 64)`, through
`internal/format`.

## NEEDS-90: an enum whose variant payload contains the enum

**Status:** done (2026-08). A type may appear inside its own payload through a
container: `enum J { JNum(F64), JArr(Arr[J]) }` declares, constructs, prints and
matches. The termination worry below did not materialise, because the
annotations are checked rather than monomorphized (NEEDS-4), so there is no
worklist to run forever yet. When user-defined generics land, the memo this
entry asks for is the thing to remember.

```
enum Json {
  ...
  JArray(Arr[Json]),
  JObject(Dict[Str, Json]),
}
```

`docs/self-hosting.md` section 1.2 specifies enums with payloads and `Arr[T]`
with an arbitrary element type, and NEEDS-72 asks for `T` to be allowed to be a
container. Neither says whether the recursion may close: whether a type may
appear inside its own payload, through a container.

It has to, and not only for JSON. `src/ast.tw` is already this shape (`Expr`
contains `Call` which contains `Arr[Expr]`) and gets away without a separate
entry because the recursion there goes through a struct. Both forms need the
same thing from the checker, which is that a type being defined is in scope
inside its own definition and that monomorphisation of `Arr[Json]` terminates
rather than instantiating forever.

Nothing exotic is wanted: the payload is behind a container, so the size is
finite and there is no infinite struct. The entry exists because the natural
reading of "monomorphized by the checker" is a worklist that would not
terminate here without a memo on the types already instantiated.

*Go bootstrap:* none. `internal/value.Value` is an interface, so recursion
through it never had to be decided.

## NEEDS-91: asking whether a path exists, without reading it

**Status:** done (2026-08, 1.6). `path_exists(Str) -> Bool` and
`path_is_dir(Str) -> Bool` are builtins, and `std/io.tw` `exists` and `is_dir`
are now one-line delegations to them. The absurd workaround described below,
reading a whole file to answer a yes-or-no question about it and listing a whole
directory to answer one about a directory, is gone from the tree.

The entry asked for "or better a `stat` returning existence, kind and size in one
call", and that is the half not taken: existence, kind, size and modification
time are four calls (`path_exists`, `path_is_dir`, `file_size`, `mtime`), so a
program that wants all four asks four times and can see the file change between
them. The invisible half the entry names, a file that exists but cannot be read
reporting differently depending on which branch answers, is improved rather than
collapsed: `path_exists` gives one answer, and permission is still not
distinguished from absence.

1.6 also added the path *string* operations the same callers wanted, which are
pure text and touch no disk: `path_join`, `path_base`, `path_dir`, `path_ext`,
`path_stem`, `path_normalize` and `path_is_abs`.

`path_exists(Str) -> Bool`, or better a `stat` returning existence, kind and
size in one call.

The runtime surface in NEEDS-28 has `read_file`, `write_file` and `list_dir`
and nothing else, so `exists` is currently: try to read the whole file, and if
that fails, list the whole parent directory and look for the base name. It is
correct. It also reads a gigabyte to answer a yes-or-no question about a
gigabyte file, and lists a hundred thousand entries to answer one about a
directory with a hundred thousand entries.

The cost is the visible half. The invisible half is that a file which exists but
cannot be read reports differently depending on which branch answers, and a
directory that exists but cannot be listed reports false. A `stat` collapses all
of that into one answer.

*Go bootstrap:* `os.Stat`.

## NEEDS-92: removing a file, and a temporary directory to put one in

**Status:** done (2026-08, 1.6), and wider than the two functions asked for.
`temp_dir(prefix)` creates a temporary directory and returns `Res[Str, Str]`
(note the argument: it is a prefix, not the nullary `temp_dir()` below), and
removal is three functions rather than one, because a caller that means to
delete a tree and a caller that means to delete a file should not be spelled the
same: `remove_file`, `remove_dir` and `remove_all`. Alongside them `mkdir_all`,
`rename` and `mtime`. All verified end to end: make a temp dir, write a file
into it, read it back, stat it, rename it, remove it, make a nested directory,
remove the tree.

So the gap this entry records is closed and `std/tests/io_test.tw` can now write
a fixture without leaving it behind. Actually doing that is a test-suite change
this entry does not cover.

*(Original request: `remove(Str) -> Res[Unit, Str]` and `temp_dir() -> Str`.)*

`docs/self-hosting.md` deliberately excludes directory operations, and for the
compiler that is right: it reads files, writes files and reports. A test suite
is the other caller, and it cannot write a fixture without leaving it behind. So
`io_test.tw` tests the path handling, which is where the bugs are, and does not
test the three-line wrappers over `read_file` and `write_file`, which is where a
runtime bug would be.

That is a real gap and it is recorded rather than papered over with a test that
writes into the source tree and hopes.

*Go bootstrap:* `os.Remove`, `os.MkdirTemp`, `testing.T.TempDir`.

## NEEDS-93: removing the last element of an `Arr`

**Status:** done (2026-08). `pop(a)` is a builtin: it shortens the array in
place and returns the element removed (the element, not an `Opt`, so a caller
must check `len` first). The consequence for `std/io.tw` `normalize` is that its
rebuild-one-element-shorter loop, and the comment there citing this entry, are
now unnecessary and should be deleted; they are still in the tree and are still
correct, just quadratic for no reason. The prediction below, that a stack is the
natural shape for half a dozen things in a compiler, is why this was worth
closing rather than tolerating.

*(Original request: `pop(a) -> Opt[T]`, or `truncate(a, n)`.)*

The primitive table has `arr_new`, `push`, indexed get and set, and `len`. There
is no way to make an array shorter, so `normalize` resolving a `..` component
rebuilds the whole stack one element shorter, which is O(n^2) over a path with
many of them. No real path has many of them, so this is a note rather than a
problem, and it is recorded because a stack is the natural shape for half a
dozen things in a compiler and every one of them will want the same operation.

The `Arr` is already growable, so the storage exists; this is the missing half
of `push`.

*Go bootstrap:* `s = s[:len(s)-1]`.

## NEEDS-94: a way to fail

**Status:** answered (2026-08), and the answer was already in the language:
`abort(msg)` (NEEDS-11) works in numeric mode as well as systems mode, stops the
program, and prints `runtime error: abort: <msg>` with the source line. So
`nn.init` can name the strategy and the caller and stop, and `std/frame.tw`,
`std/batch.tw` and `std/loss.tw` can all say the things this entry lists that
they cannot say.

The advice below is therefore now wrong in one direction and right in the other.
Wrong: "there is nothing in the language that stops a program" is false. Right:
the workaround it describes is still what `std/nn.tw:72` does, print followed by
a tensor of NaNs, and every criticism of it below still holds. The fix is a
standard-library change rather than a language one, and it has not been made.

What `abort` is not is the mechanism the last paragraph asks for, a failure that
reaches the top with a source position and that a caller could catch. `abort`
means a bug and is not catchable; `Res` and `?` (NEEDS-10) are the catchable
half, and a library deciding between them is a design question this entry does
not settle.

*(Original status: blocking, and worked around badly.)* `std/nn.tw` `init`,
`conv_init_as`.

`nn.init(strategy, nout, nin)` takes the initialisation strategy by name, so
that nobody gets Xavier when they meant He without being told. An unrecognised
name is a programming error and should stop the program with a message naming
the strategy and the caller. There is nothing in the language that stops a
program: no `error`, no `panic`, no `assert`, no way to return a failure that
cannot be ignored.

The workaround is `print` followed by a tensor of NaNs, chosen because the NaN
propagates into the first loss and is at least visible. It is still wrong in
both directions: the print goes to stdout in the middle of whatever else is
being printed, and the NaN surfaces one training step away from its cause, so
the message and the symptom are separated by everything the model did in
between.

Every module in this set has the same hole. `std/frame.tw` cannot say that a
column does not exist, `std/batch.tw` cannot say that a fold count exceeds the
row count, and `std/loss.tw` cannot say that a probability was passed where a
logit was wanted, which is the single most common mistake this library invites.

*Go bootstrap:* the interpreter's builtins return `(value.Value, error)` and the
error reaches the top with a source position attached. What is wanted is that
mechanism exposed to Twill, not a new one.

## NEEDS-95: a seeded random stream, or `permutation` taking a seed

**Status:** open, worked around, and the workaround has a side effect.
`std/batch.tw` `shuffled_indices`, `stratified_indices`,
`stratified_kfold_indices`.

Every split in `std/batch.tw` takes an explicit seed, because a split that
cannot be reconstructed makes the number measured on it unreproducible.
`permutation(n)` has no seed parameter, so the only way to honour that argument
is to call the global `seed(s)` first.

That works, and it moves the one random stream the whole program shares. A call
to `train_test_split` therefore changes every subsequent `randn`, so splitting
the data after initialising the model gives different weights than splitting
before it, for no reason the reader of that code could see. `stratified_indices`
makes it worse: it seeds once per class, so it consumes and resets the stream
several times in one call.

Either `permutation(n, seed)` and `randn(shape, seed)`, or a first-class
generator value that carries its own state and is threaded like the optimizer
state in `std/optim.tw`. The second is the better answer and the larger change;
the first would remove the surprise today.

The first-class generator already exists: `std/random`'s `Rng`, with `new_rng`
and `permutation(r, n)`.

**The mode-boundary argument below is now wrong (2026-08, 1.6).** It said that
`std/batch`, being numeric mode, could not hold an `Rng`, because `Rng` carries
`I64` state and numeric mode's only number is float64. Since 1.6 `I64` is a real
value rather than a float in disguise (NEEDS-2), and a numeric-mode file
importing `std/random`, calling `new_rng(7)` and holding the result works;
verified against the binary. The runtime also grew its own generator handles
(`rng_open`, `rng_f64`, `rng_u53`, `rng_close`) beside the global stream.

So the choice this entry frames as narrow is now easy, and it is a
standard-library change rather than a language one: `std/batch` threads an `Rng`
the way `std/optim.tw` threads optimizer state, and stops calling the global
`seed`. That is the second and better of the two answers offered above, and
nothing blocks it. Until it is done the side effect described here is real:
`train_test_split` still moves the one global stream, and `stratified_indices`
still reseeds it once per class.

*(The original paragraph, now superseded: "What blocks `std/batch` from using it
is not that it is unwritten but the mode boundary ... There is no
numeric-mode-only fix, because the only seeding a numeric program has is the
global `seed`, which is the side effect in the first place.")*

*Go bootstrap:* `internal/interp` holds a package-level `*rand.Rand`. A
per-call seed is a second `rand.New(rand.NewSource(seed))` that is not stored.

## NEEDS-96: iteration that does not materialise

**Status:** open, and a real limit on dataset size, unchanged (re-checked
2026-08). There is no generator, no `yield`, and no lazy sequence with a `next`,
so `epoch_batches` still materialises the whole epoch and `std/io` still has no
way to stream a file that does not fit in memory. This is the largest open
*language* request left that is not generics: everything else on the open list is
a primitive or a library change. `std/batch.tw`
`epoch_batches`, `eval_batches`.

`epoch_batches` returns the whole epoch as a list of `[Xb, yb]` pairs. Every
batch of every epoch exists at once, which for a dataset that fits in memory
costs one extra copy of it, and for one that does not is simply the wrong
answer.

What is wanted is a generator: a function that can yield a value and be resumed,
or a lazy sequence with a `next`. Either would let a training loop pull one
batch at a time, and would also let `std/io` stream a file that does not fit in
memory, which is the same shape of problem.

A closure over mutable state is the workaround available today, and it is worse
than the list: `fn() { i = i + 1; ... }` has no way to say it is finished except
by a sentinel value the caller has to test for, which is exactly the pattern
that ends in reading one batch past the end.

*Go bootstrap:* none. The Go interpreter builds the same list.

## NEEDS-97: assigning to an element of a list

**Status:** done (2026-08). `xs[i] = v` assigns an element of a list, in
numeric mode as well as systems mode. So `stratified_kfold_indices` can be
written the natural way, one pass dealing into k buckets, instead of k passes
each deciding whether an element belongs to the fold being built; the rewritten
loop this entry complains about is still in the tree and is now k times the work
for no reason.

Tensors still have the gap and still have the better excuse below.

*(Original status: open, worked around. Original text follows.)*

`xs[i] = v` is a syntax error. `append` is the only way to grow a list and there
is no way to replace an element of one, so an algorithm that fills k buckets by
dealing into them has to be turned inside out: `stratified_kfold_indices` makes
one pass per fold over the same shuffled per-class lists, deciding each time
whether an element belongs to the fold it is currently building. That is k times
the work for a result that one pass would produce, and the rewritten loop is
harder to read than the one it replaced.

Tensors have the same gap and a better excuse, since in-place mutation of a
tensor would have to be reflected on the tape. A list of indices carries no
gradient and has no such problem.

*Go bootstrap:* `[]value.Value` is a Go slice and assignment is assignment. The
restriction is the language's, not the runtime's.

## NEEDS-98: an empty record, and removing a field

**Status:** done, by the primitive the entry itself calls the smaller and more
general of the two (2026-08). `{}` is the empty record, not a block evaluating to
unit, and `with_field({}, "a", 1)` builds `{a: 1}`, so a record whose field names
come from a list can be constructed. That is the `record()` half, and the entry
says outright that it alone would do: given it, `without_field` is a fold over
`columns` skipping one name, and `columns` and `field` are both builtins.

`without_field` itself was not added, so a narrowing that wants to preserve field
order pays a rebuild.

What has *not* happened is the consequence: `std/frame.tw` still has no `select`,
`drop`, `rename` or `from_columns`, and `group_agg` still returns its answer
under the fixed names `key` and `value`. The language stopped being the reason in
1.6; the library has not caught up.

`with_field(rec, name, value)` builds a record with a name known at run time,
which is exactly the right primitive, and it is unusable on its own because
there is nothing to start from. `{}` is a block and evaluates to unit, so there
is no empty record, and nothing removes a field, so a record cannot be narrowed
either. Every record therefore has to be born from a literal whose field names
are in the source text.

The consequence for a column-oriented table is severe. `select(df, names)` is
the most basic operation a table has and it cannot be written: it needs a record
whose fields come from a list. `drop` is `select` of the complement. `rename` is
`select` with one name changed. `group_agg` can compute its answer but has to
return it under the fixed names `key` and `value`, because it cannot name the
columns after the ones it grouped and aggregated.

Two primitives close it, and either alone would do:

    record()                   the empty record, so with_field can build any
    without_field(rec, name)   a copy without a field, so any record can be
                               narrowed

`record()` is the smaller and more general of the two: given it, `without_field`
is a fold over `columns` skipping one name.

*Go bootstrap:* `value.Record` is an ordered map. Both operations are three
lines each and neither has a design question in it.

## NEEDS-99: string concatenation

**Status:** done (2026-08). `"colour" + "_" + str(0)` is `colour_0`: `+`
concatenates (NEEDS-35) and `str` on a number renders it without a trailing
`.0` (NEEDS-45), which is both halves of what this entry asks for. So `one_hot`
can build its own column names and stop taking them as an argument, `std/metrics.tw`
can label the rows of `describe`, and the multi-argument `print` diagnostics in
these modules can become one built string.

None of that has been done. The language gap closed in 1.6; the thirty string
literals at the call site are now the library's choice rather than its
constraint. `std/frame.tw` `one_hot`. **The normative text is
`docs/language-guide.md`, Strings → Concatenation** for `+`, and Standard
library → `str` on a number for the round-tripping rendering this entry pairs
with it. Once both exist, `one_hot` builds `colour_0` itself and stops taking
the output names as an argument.

One-hot encoding a column called `colour` over the categories 0, 1, 2 should
produce columns called `colour_0`, `colour_1` and `colour_2`. There is no `+`
on strings, no `concat` for them, and no formatting function that takes a
string and a number and returns a string, so `one_hot` takes the output names
as an argument and the caller writes them out by hand. For a category with
thirty values that is thirty string literals at the call site, and every one of
them is a chance to get the order wrong, which produces a frame whose column
names disagree with its contents and nothing that would notice.

The same gap is why `std/metrics.tw` cannot label the rows of `describe`, and
why every diagnostic message in these modules is a `print` with several
arguments rather than one built string.

Wanted: `str_concat(a, b)` or `+` on strings, and a `str` that takes a number to
a decimal string that round-trips (NEEDS-89 asks for the second half of that
already).

*Go bootstrap:* Go strings concatenate with `+`. `internal/interp` already
formats numbers for `print`.

## NEEDS-100: enumerating and opening a GPU device

**Status:** deferred, and the deferral is now visible from twill (2026-08).
Read NEEDS-108 first: it says none of NEEDS-100 through NEEDS-107 can be
implemented under the current rules, and that remains true, because NEEDS-107 is
still nothing.

What 1.6 did add is the *names*. Every signature in NEEDS-100 through NEEDS-106
is on the builtin table, and every one of them except `gpu_available` fails at
run time with `no GPU backend in this build`. `gpu_available()` returns `false`
and `gpu_device_count()` returns `0`, which is exactly the contract this entry
insists on: a machine with no GPU is the normal case, `available()` is false,
every tensor stays on the host, and every answer is unchanged. So the
graceful-degradation path is real and testable today, and the path behind it is
a stub. That is worth saying plainly, because a reader who greps the builtin
list will find fifteen GPU functions and conclude there is a backend.

`src/gpu/device.tw` `available`, `device_count`, `open`, `close`. It says that none of NEEDS-100 through NEEDS-107 can be
implemented at all under the current rules, and that the entries exist so that
the requirement is a named list rather than an open question. Every entry in
this block is a signature, not a plan.

    gpu_available() -> Bool
    gpu_device_count() -> I64
    gpu_device_open(index: I64) -> Res[I64, Str]
    gpu_device_info(dev: I64, key: Str) -> Str
    gpu_device_info_i64(dev: I64, key: Str) -> I64
    gpu_device_close(dev: I64)

`gpu_available` must not fail and must not be an error condition. A machine with
no GPU driver is the normal case, and the whole graceful-degradation story is
that `available()` is false, every tensor stays on the host, and every answer is
unchanged. It is false when the driver library is absent, when it is present and
exports nothing usable, and when it reports zero devices.

The two `_info` forms exist because twill has no sum type at a primitive
boundary and a single accessor would have to return a string for `name` and an
integer for `compute_units`. The keys `src/gpu/device.tw` asks for are `name`,
`driver`, `compute_units`, `max_group`, `has_f64` and `local_bytes`. The last
three are not diagnostics: `max_group` and `local_bytes` decide whether the
tiled matmul can be launched at all, and a kernel that exceeds either does not
run slowly, it fails to launch.

Against OpenCL this is `clGetPlatformIDs`, `clGetDeviceIDs`, `clCreateContext`,
`clCreateCommandQueue` and `clGetDeviceInfo`, flattened so twill sees one list
of devices rather than a list of platforms each holding a list of devices. The
nesting buys nothing: `docs/gpu-feasibility.md` found two platforms with one
device each on the development machine, and code wanting the fastest device
would flatten it anyway.

*Go bootstrap:* none. `internal/tensor` is `[]float64` and goroutines and has no
concept of a device.

## NEEDS-101: allocating and freeing device memory

**Status:** deferred; the name exists and the implementation does not (2026-08). See NEEDS-100: these builtins are on the table and return `no GPU backend in this build`. The signature below is what they will have, and the reasoning is what it is for. `src/gpu/device.tw` `alloc`, `free`; `src/gpu/buffer.tw`
`alloc_with_eviction`.

    gpu_alloc(dev: I64, elements: I64) -> Res[I64, Str]
    gpu_free(buf: I64)

Sized in F64 elements and not bytes, because every caller in `src/gpu/` counts
in elements and a units mismatch at this boundary reads as a wrong answer rather
than as a crash.

`gpu_alloc` must return `Err` on an out-of-memory rather than abort, and this is
the one primitive whose failure mode is designed around. The card in the
development machine has 8GB shared with the display, so a long run exhausting it
is expected rather than exceptional. `src/gpu/buffer.tw` catches the `Err`,
evicts device copies whose host copy is still valid, retries once, and then
falls back to the CPU with an identical answer. A primitive that aborted would
turn memory pressure into a failed program.

`gpu_free` returns nothing. A failure to free is not something a caller can act
on, and a `Res` here would put a `?` on every cleanup path to serve no decision.

Against OpenCL: `clCreateBuffer` with `CL_MEM_READ_WRITE`, and
`clReleaseMemObject`.

*Go bootstrap:* none.

## NEEDS-102: moving numbers to and from a device

**Status:** deferred; the name exists and the implementation does not (2026-08). See NEEDS-100: these builtins are on the table and return `no GPU backend in this build`. The signature below is what they will have, and the reasoning is what it is for. `src/gpu/device.tw` `write`, `read`, `copy`.

    gpu_write(buf: I64, dst_off: I64, src: Arr[F64]) -> Res[Unit, Str]
    gpu_read(buf: I64, src_off: I64, n: I64)         -> Res[Arr[F64], Str]
    gpu_copy(dst: I64, dst_off: I64, src: I64, src_off: I64, n: I64)
                                                     -> Res[Unit, Str]

All three blocking, in the first version. `docs/gpu.md` section 3 argues that a
non-blocking queue reports an error at a point unrelated to the op that caused
it, and that debugging a numerical difference is hard enough with the error
attached to the right line.

`gpu_read` allocates and returns a fresh `Arr[F64]` rather than filling one the
caller supplies, because twill has no way to hand out a writable window into an
existing `Arr` and pretending otherwise would put an aliasing rule into the one
place in the codebase that cannot check it.

`gpu_copy` is device to device and is not a convenience. Without it, taking a
row out of a device tensor means reading it down and writing it back up, which
is two of the boundary crossings `docs/gpu-feasibility.md` measured at roughly
80us each, to move data that never needed to leave. It is what keeps slice,
concat, index and split resident.

Note what is deliberately *not* here: an integer transfer. The elementwise
kernels need shapes and strides on the device, and those ride as `F64` and are
cast back in the kernel. A shape cannot exceed 2^53 without the tensor exceeding
any device this will run on, so the round trip is exact, and the ugliness buys
one fewer entry on this list. See `src/gpu/buffer.tw` `meta_buffer`.

Against OpenCL: `clEnqueueWriteBuffer` and `clEnqueueReadBuffer` with
`blocking = CL_TRUE`, and `clEnqueueCopyBuffer`.

*Go bootstrap:* none. A slice is already where the CPU wants it.

## NEEDS-103: compiling a kernel from source at run time

**Status:** deferred; the name exists and the implementation does not (2026-08). See NEEDS-100: these builtins are on the table and return `no GPU backend in this build`. The signature below is what they will have, and the reasoning is what it is for. `src/gpu/device.tw` `build`, `kernel`.

    gpu_program_build(dev: I64, source: Str, options: Str) -> Res[I64, Str]
    gpu_kernel(program: I64, name: Str)                    -> Res[I64, Str]

Run-time compilation from source text is the property that made OpenCL the
recommendation in `docs/gpu.md` over Vulkan, which consumes SPIR-V and would
mean either shipping precompiled binary blobs built by a toolchain that is not
present, or writing a SPIR-V emitter. Compiling from source means the kernels
are readable text in the repository, there is no build step, nothing is added to
the release matrix, and a kernel can be specialised on the shapes it is about to
run. `src/gpu/matmul.tw` uses that last property to bake its tile size in as a
compile-time constant, which is what lets the compiler unroll the inner loop and
size the local arrays statically.

The `Err` of `gpu_program_build` is the most important error message in the
backend and it must carry the driver's build log verbatim. A kernel that fails
to build fails on somebody else's driver, on hardware nobody developing twill
owns, and the log is the only evidence that will ever exist.

`options` is the compile options string, and what is absent from it is the
subject of `docs/gpu.md` section 5 rule 3. It is built in `src/gpu/source.tw` so
there is exactly one of it.

Against OpenCL: `clCreateProgramWithSource`, `clBuildProgram`,
`clGetProgramBuildInfo` for the log, and `clCreateKernel`.

*Go bootstrap:* none.

## NEEDS-104: binding kernel arguments

**Status:** deferred; the name exists and the implementation does not (2026-08). See NEEDS-100: these builtins are on the table and return `no GPU backend in this build`. The signature below is what they will have, and the reasoning is what it is for. `src/gpu/device.tw` `arg_buffer`, `arg_i64`, `arg_f64`,
`arg_local`.

    gpu_set_arg_buffer(kernel: I64, index: I64, buf: I64)  -> Res[Unit, Str]
    gpu_set_arg_i64(kernel: I64, index: I64, v: I64)       -> Res[Unit, Str]
    gpu_set_arg_f64(kernel: I64, index: I64, v: F64)       -> Res[Unit, Str]
    gpu_set_arg_local(kernel: I64, index: I64, bytes: I64) -> Res[Unit, Str]

Four setters and not one. A kernel argument is typed on the device side, and
passing an integer where a buffer was expected is undefined rather than an
error. Twill has no variadic call and no way to describe a heterogeneous
argument list, so the alternative is an encoding, and an encoding here would be
a second place for the two sides' types to disagree.

`arg_local` reserves work-group local memory for an argument the kernel declares
`__local` with no size. Only the tiled matmul uses it, and it is on the list
rather than folded away because a matmul without local-memory staging is the
untiled version, which is several times slower and is the reason a GPU is being
considered at all.

Against OpenCL: `clSetKernelArg`, four times over, with `arg_local` passing a
size and a null pointer.

*Go bootstrap:* none.

## NEEDS-105: launching a kernel

**Status:** deferred; the name exists and the implementation does not (2026-08). See NEEDS-100: these builtins are on the table and return `no GPU backend in this build`. The signature below is what they will have, and the reasoning is what it is for. `src/gpu/device.tw` `launch`.

    gpu_launch(dev: I64, kernel: I64, global: Arr[I64], local: Arr[I64])
        -> Res[Unit, Str]

`global` is the total number of work-items per dimension, 1 to 3 dimensions.
`local` is the work-group shape, or empty to let the driver choose. Empty is the
default everywhere except the tiled matmul, which needs a specific group shape
for its local-memory staging to be *correct* and not merely fast: a barrier that
only some work-items in a group reach is undefined behaviour.

Note what `gpu_launch` does not take: a stream, an event, or a dependency. There
is one queue and every launch is followed by a synchronise. That is the first
thing to change once the answers are trusted, which is why NEEDS-106 is a
separate entry rather than folded into this one.

Against OpenCL: `clEnqueueNDRangeKernel`.

*Go bootstrap:* the nearest analogue is `runChunks` in
`internal/tensor/parallel.go`, which splits an index range across goroutines.
The shape of the idea is the same and nothing else about it is.

## NEEDS-106: synchronising with a device

**Status:** deferred; the name exists and the implementation does not (2026-08). See NEEDS-100: these builtins are on the table and return `no GPU backend in this build`. The signature below is what they will have, and the reasoning is what it is for. `src/gpu/device.tw` `finish`.

    gpu_finish(dev: I64) -> Res[Unit, Str]

Blocks until every command queued on the device has completed. This is where a
kernel's error surfaces, because an enqueue that returned `Ok` has only been
accepted and not run.

It is its own entry rather than part of NEEDS-105 for a forward-looking reason.
The first version calls it after every launch, which throws away the latency
hiding a deep queue would give. Letting the queue run ahead is the single
largest easy win left in the design once the answers are trusted, and it is only
possible if launch and synchronise are separable.

Against OpenCL: `clFinish`.

*Go bootstrap:* a `sync.WaitGroup`, in the sense that both wait.

## NEEDS-107: loading a shared library and resolving a symbol at run time

**Status:** open, and the mechanism NEEDS-100 through NEEDS-106 all rest on.
Nothing landed (re-checked 2026-08): there is no `dlopen`, no `GetProcAddress`
equivalent, and no calling convention, so the fifteen names 1.6 put on the
builtin table have nothing behind them and cannot get anything behind them this
way. Deliberately not done rather than not got to: an FFI is a much larger
decision than a GPU backend, which is the argument in NEEDS-108 option 1.

The six entries above are signatures. This one is the thing that makes any of
them reachable: a way to open a shared library by name at run time and look up a
symbol in it, then call through the resulting pointer with a described
signature.

`docs/gpu-feasibility.md` established that this is the whole dependency story
for OpenCL. `OpenCL.dll` is in `System32` on the development machine because the
driver installed it, both GPUs register ICDs, and a host program that resolves
the loader with `LoadLibrary` and `GetProcAddress` and declares the dozen entry
points it needs compiled with no headers and no SDK and ran on both cards. So
what is wanted is not an SDK binding. It is `LoadLibrary` plus `GetProcAddress`,
and their equivalents elsewhere, plus a calling convention.

That is deliberately more general than a GPU. It is a foreign function
interface, and every consumer of one that twill might ever have goes through it.
It is recorded here because the GPU backend is the first concrete thing that
needs it and therefore the first thing that can say precisely what shape it must
have: pointer-sized handles, `Arr[F64]` passed as a base pointer and a length,
`I64` and `F64` scalars, and a return that is either an integer status or a
handle.

The alternative that avoids it entirely is native code compiled into the
runtime, which is the same requirement wearing a different hat and is the
subject of NEEDS-108.

*Go bootstrap:* `internal/interp/builtins.go` dispatches on a name into Go
functions in the same binary, which needs no FFI because there is no foreign
side. That is exactly the property the GPU backend does not have.

## NEEDS-108: there is nowhere for a native layer to live

**Status:** open, and it is not a language feature; unchanged in substance
(2026-08). It is still the reason NEEDS-100 through NEEDS-107 cannot be started,
and the project is still on option 3, which is what `docs/gpu-feasibility.md`
recommends on independent grounds. 1.6 moved one inch toward option 2 by putting
the fifteen names on the primitive table with stub bodies, which is the framing
that paragraph argues for: the question is now "what is on the primitive table",
and the answer is "these, unimplemented". Nothing has been measured since, so the
precondition the last option names, a real twill program that is matmul-bound at
256x256 or larger, is still not shown to exist.

Stated plainly, because the rest of `src/gpu/` and `docs/gpu.md` are written as
though it were solved and a reader should not have to infer this from their
silence:

**A GPU backend cannot exist without a foreign function interface or native code
of some kind. Under the current no-Go rule, that layer has nowhere to live.**

Nothing in `src/gpu/` closes this and no amount of further twill would. Every
kernel in `src/gpu/source.tw` is text that a driver has to compile, and every
function in `src/gpu/device.tw` is a call into a library that twill has no way
to call. The design is complete and unrunnable, and those are two separate
facts.

The options, none of which is a language feature:

1. **An FFI, which is NEEDS-107.** The most general answer and the largest. It
   gives twill a way to call anything, which is a much bigger decision than
   "should there be a GPU backend" and should not be made as a side effect of
   one.
2. **Native code in the runtime.** Whatever eventually executes twill has to be
   written in something, and that something can link the loader directly. This
   is what `internal/` does today for `math.Exp`, which NEEDS-68 already treats
   as a native primitive rather than a foreign call. Under that framing the
   fifteen entries above are fifteen more native primitives alongside `f64_exp`,
   and the question stops being "how does twill call out" and becomes "what is
   on the primitive table". That is the smaller and more likely answer.
3. **Neither, for now.** Which is where things stand, and which
   `docs/gpu-feasibility.md` recommends on independent grounds: settle f32
   first, add the matmul benchmarks at N=256, 512 and 1024 that the repository
   does not have, and find a real twill program that is matmul-bound at 256x256
   or larger before building any of this.

The value of writing the list anyway is that the requirement is now bounded.
Fifteen symbols out of a library that is already installed on any machine with a
GPU, resolved at run time, with nothing added to the build and nothing added to
the release matrix. That is a small enough number to argue about honestly, which
is the whole point of counting it.

*Go bootstrap:* it links `internal/tensor` directly, which is the option-2
answer taken without anyone having to decide it.

## NEEDS-109: `reduce_all` disagrees with the bootstrap above 8192 elements

**Status:** done. `src/tensor.tw` `reduce_all` now sums through `block_sum`,
which ports `parallelSum`: a plain running sum below 8192 elements and fixed
4096-element blocks combined in block order at or above, so twill and the
bootstrap agree to the last bit at every size, and `src/gpu/reduce.tw`
`whole_tensor_sum`, which already followed the Go form, agrees with both. The
mean scaling was corrected in the same change from `s / n` to the bootstrap's
`s * (1.0 / n)`, which rounds differently. The original divergence is described
below.

*(Original: open, a correctness divergence. `src/tensor.tw` `reduce_all`;
`internal/tensor/parallel.go` `parallelSum`; `src/gpu/reduce.tw`
`whole_tensor_sum`.)*

Found while designing the GPU reductions, and recorded rather than fixed because
`src/tensor.tw` was owned by another change at the time.

`internal/tensor/parallel.go` sums a whole tensor in two forms. Below
`minParallel = 8192` it is a plain running sum. At or above it, fixed
4096-element blocks are summed independently and their partials combined in
block order, and the comment is explicit that the block size is fixed rather
than derived from the core count so that "the result is the same on any number
of cores".

`src/tensor.tw` `reduce_all` is a plain running sum at every size, with no
blocking. So for `n >= 8192` the twill reference and the Go bootstrap produce
different last bits for the same input, today, with no GPU anywhere near it.

Three implementations cannot all be right. The Go form is the one to adopt,
because a fixed block size is a *specification* rather than an implementation
detail: it pins the answer, it is reproducible on any hardware, and it is
parallelisable, which the plain running sum is not. `src/gpu/reduce.tw` follows
the Go form for exactly that reason, and is therefore bit-identical to the
bootstrap and not to `src/tensor.tw` until this is closed.

This matters more than a last-bit divergence usually would, because `testdata/`
compares output byte for byte after a canonical float rendering (NEEDS-29). A
divergence in the last bits of a sum over 8192 or more elements is a test
failure, not a curiosity.

*Go bootstrap:* it is the correct one here. `parallelSum` in
`internal/tensor/parallel.go` is the text to port.

## NEEDS-110: dtype names in the surface language

**Status:** done on both sides (2026-08). `src/eval.tw` reads the seven names
contextually, constructors take a trailing dtype, and `.to` casts (commit
`65ebdb0`), and the Go bootstrap now does the same: `zeros(2, 2, bf16)` builds a
bf16 tensor, `dtype(x)` is `bf16`, and `x.to(f32)` casts. The last sentence of
the original status said the bootstrap does not implement it and that this would
be one of the divergences the triple build shows. That is no longer true and the
divergence is gone.

Note the shape of the contextual reading: a dtype name is only a dtype in the
dtype argument of a constructor or of `.to`, so a bare `bf16` in any other
expression position is an unknown name. That is the design below working as
intended and it surprises anyone who tries to pass one around.

`docs/dtypes.md` is the design. What is missing is the syntax. Three things,
none of them large on its own:

    bf16                       a dtype as a name, in expression position
    zeros([784, 128], bf16)    a constructor that takes one
    x.to(f32)                  the explicit cast

A dtype is not a type, and treating it as one is the mistake to avoid:
`Tensor[bf16]` as a parameterised type would make every function that takes a
tensor generic over seven dtypes and hand the checker a unification problem it
does not otherwise have. A dtype is a run-time property of a value and a name in
the term language, as it is in numpy.

The seven names are `bool`, `i8`, `i32`, `f16`, `bf16`, `f32`, `f64`. They
should be contextual rather than reserved: `f32` stays an ordinary identifier
everywhere else, and only the dtype argument of a constructor and of `.to` reads
it as one. `tensor.dtype_of_name` already maps the string to the code.

*Go bootstrap:* `internal/tensor.Tensor` is `[]float64` with no dtype at all, so
there is nothing to be contextual against. `builtins.go` `zeros` takes only a
shape.

## NEEDS-111: a packed, byte-addressable buffer

**Status:** done (2026-08). The twill side landed in commit `edb4637`:
`src/buf.tw` packs the layout and `Tensor.data` is a `Buf`, so the dtype work
saves the bytes `docs/dtypes.md` promised, 2x for f32 and i32, 4x for bf16 and
f16, 8x for i8 and bool. The four byte primitives it was waiting on are now
builtins and work: `buf_new`, `buf_len`, `buf_get8`, `buf_set8`. Everything
above a byte is twill, as designed. The packed buffer runs.

The aliasing question raised at the end of this entry is still unanswered and
still does not need to be: the primitives offer no slicing view, so two tensors
cannot share a byte range, and nothing in `src/tensor.tw` wants to.

The dtype semantics landed without it: a bf16 tensor holds bf16 values, rounded
correctly, and `twill-lang/shuttle` can now measure the error that quantisation
introduces. What it still cannot measure is a saving, because `Tensor.data` is
`Arr[F64]` and a bf16 element occupies 64 bits like everything else. Until this
lands, shuttle's report that quantisation shrinks nothing stands.

Wanted: a buffer with a byte length and typed element access.

    Buf                                 an opaque packed byte buffer
    buf_new(bytes)                      allocate, zeroed
    buf_len(b)                          length in bytes
    buf_get(b, dtype, i) -> F64         read element i, widened
    buf_set(b, dtype, i, x)             write element i, rounded

The rounding on `buf_set` is exactly `tensor.dt_round` and the widening on
`buf_get` is exactly `float.f_widen`, so this is a layout change and not a
semantics change: every kernel in `src/tensor.tw` keeps its current text with
`t.data[i]` becoming `buf_get(t.data, t.dtype, i)`. Preserving that property is
why the semantics went first.

Two consequences worth stating. Memory drops 2x for f32 and i32, 4x for bf16 and
f16, and 8x for i8 and bool, which is the whole point. And an aliasing question
appears that `Arr[F64]` never had, since a `Buf` is a byte range two tensors
could share; nothing in `src/tensor.tw` does that today because every kernel
allocates its own output, and the primitive should not offer a slicing view
until something needs one.

This is also the entry `src/gpu/buffer.tw` wants. A device upload of a bf16
tensor currently has to narrow on the way out and widen on the way back, and a
packed host buffer is the same bytes the device wants.

*Go bootstrap:* `[]float64`, one allocation per tensor. A `[]byte` read through
`encoding/binary` and `math.Float32frombits` is the direct equivalent.

## NEEDS-112: loss scaling for f16

**Status:** done in twill (commit `edb4637`). `src/tensor.tw` `backward_scaled`
and `grads_finite`, written over the existing `backward`, no new primitive. See
`docs/dtypes.md`, "Loss scaling, and the f16 story". The skip-not-clip loop
that consumes them is `src/precision.tw` in `twill-lang/loom`.

f16 has five exponent bits, so its smallest normal is 2^-14 and real gradients
go under it. bf16 does not have the problem and needs nothing here. f16 is
unusable for training without it, which is both why the design says to prefer
bf16 and why this entry exists rather than being skipped.

Two functions:

    backward_scaled(tp, root, seed, scale) -> Res[Arr[Tensor], Str]
    grads_finite(gs) -> Bool

`backward_scaled` seeds with `seed * scale`, so by the chain rule every gradient
returns scaled by exactly that factor and the caller divides it out.
`grads_finite` is the overflow check that decides whether the step happens at
all.

Together they support dynamic loss scaling: skip the step and halve the scale
when a gradient comes back non-finite, double it after a run of clean steps. The
reason to name them rather than leave the loop to callers is the skip. A
hand-written version that clips the infinity to a large finite number instead of
skipping looks like it works, trains to a worse model, and reports nothing.

Both are writable in twill today over `backward` and `binary`. They are here so
that there is one of them rather than one per caller.

*Go bootstrap:* none. `internal/tensor` has no dtype, so no gradient can
underflow in a way float64 would notice.

## NEEDS-113: dtype in the static checker

**Status:** done on BOTH checkers (2026-08). `src/check.tw` carried a dtype on
its tensor type from commit `79bc6ac`; `internal/checker` now does too, and the
two agree character for character.

The Go side needed the type the entry asks for. `tTensor` gained a dtype stored
as **code+1, so the zero value means "not known"** -- which is what each of the
sixty-odd bare `tTensor{dims: ...}` literals in that file should say, and what
`src/check.tw` spells `DT_UNKNOWN`. A dtype enters at a cast or a constructor's
trailing name, defaults to f64 for a constructor without one and for a tensor
literal, and rides through rearrangement, indexing, reductions and the unary
ops on the same rules the self-hosted checker uses. `promoteAndWarn` runs at
every binary node, before the shape rules, so both checkers emit their
diagnostics in the same order.

**The promise that made this delicate** is the second half of the entry: a
program that never wrote a dtype must draw no new diagnostic. Two rules keep
it. A bare number literal deliberately has NO dtype -- only `scalar(x)` and a
tensor literal are f64 -- so `w * 2.0` is silent where `w * scalar(2.0)` is
not. And a float-only operation on an integer input degrades to unknown rather
than claiming f32, so a chain like `exp(argmax(x))` meeting an ordinary f64
cannot make the warning fire. Verified over 405 files of `std`, `src`,
`examples` and `testdata/cases`: both checkers, byte-identical, zero new
diagnostics.

**A bug this found in the self-hosted checker**, present since the dtype work
landed and fixed here: `infer_call` strips a constructor's trailing dtype name
and passes it separately, and the constructor branch then called
`drop_last_arg` *again*. `zeros(2, 3, bf16)` became `zeros(2)` and the checker
reported its shape as `[2]` where the runtime builds `[2, 3]` -- a false
"cannot broadcast" on correct code. The list form `zeros([2, 3], bf16)` lost
its only argument and degraded to unknown instead, which is why it hid.

The checker already approximates `broadcast_shape` statically, so a shape
mismatch is a compile error. It has no equivalent for `promote`, so nothing is
reported until the program runs. Three cases go unseen:

    f16_tensor + bf16_tensor       is f32, which is correct and surprising
    i32_tensor / 2                 whether the literal promotes, and therefore
                                   whether this is integer division
    bf16_weights + f64_bias        widens the whole layer to f64 and undoes the
                                   reason the weights were narrow

The third is the one that matters. It is not an error, it is a silent
performance regression with a perfectly correct answer, and a checker that knows
dtypes is the only place it can be caught. It should be a warning and not an
error: the program means what it says.

The first two cases above are still unreported, and deliberately: `f16 + bf16`
promoting past both to f32 is correct and surprising but nothing was widened
*by an operand*, and whether `i32_tensor / 2` is integer division is a question
about the literal's dtype, which is exactly the thing left unknown to keep the
promise above.

**Severity is real now, so the warning behaves like one.** This entry asks for
"a warning and not an error: the program means what it says", and until
recently both CLIs printed every finding as `shape error:` and counted it
toward "N shape error(s); not running" -- so a lossy widening stopped the
program. `src/check.tw` had carried a `severity` field all along and noted that
main did not read it.

The Go `Diagnostic` gained the same field, and both CLIs now agree on all three
consequences:

- a warning prints as `warning:`, not `shape error:`;
- `run` prints it and runs the program anyway, counting only errors in its
  refusal;
- `check` prints it and exits 0, because a file whose only findings are
  warnings has nothing wrong with it.

An error is unchanged in every respect. `twill test` fails a suite only on an
error, and the LSP publishes a warning as LSP severity 2 rather than painting
it like a mistake.

## NEEDS-114: dtype-aware printing and parsing

**Status:** done (2026-08). The numerics landed in commits `29c8f86` and
`edb4637` and the print path is wired: a bf16 tensor holding one third prints
`tensor([0.334], shape=[1], dtype=bf16)`, which is the shortest decimal that
round-trips through bf16 rather than seventeen digits of a number that
distinguishes three, and the dtype prints alongside the shape as the last
paragraph of this entry asks. `format_value` exists (NEEDS-88), which is the
dependency the paragraph below says had to come first, and it does route each
element through `tensor.dt_shortest`. The original status follows because its
sequencing argument is the reason this landed in the order it did.

*(Original status: the numerics are done (commits `29c8f86`, `edb4637`); the
print path in `src/eval.tw` is not yet wired. `src/float.tw` now renders and parses at
a `FloatFmt`, and `src/tensor.tw` `dt_shortest`/`dt_of_str` dispatch it on the
dtype. What is left is not mechanical, because the thing it plugs into is itself a
hole: `src/eval.tw` calls `format_value` in four places and no version of it is
written yet (that is NEEDS-57). So the honest sequence is NEEDS-57 first, and
when `format_value` renders a tensor it routes each element through
`tensor.dt_shortest(t.dtype, x)` rather than `format_number`, and narrow
literals parse through `tensor.dt_of_str(dt, s)` rather than `f64_of_str`. The
dispatch both of those need is in place; the renderer that calls it is not. No
new primitive.)*

`print(x)` renders the F64 in the buffer. For a bf16 tensor that F64 is the
exact widening of a bf16 value, so it prints seventeen digits of a number that
distinguishes about three. The output is not wrong; it is unreadable, and it
claims a precision the value does not have.

Wanted: `f64_shortest` generalised to a format, so a bf16 value prints the
shortest decimal that round-trips *through bf16*. The machinery is already
there. `round_shortest` cuts the exact decimal at the first digit where no other
float of the format could round back to it, and the only format-specific things
it reads are `MANT_BITS`, `BIAS` and `IMPLICIT_BIT`, which is exactly what
`float.FloatFmt` carries.

The inverse matters too. `f64_of_str` parses at f64 and a narrow literal is then
rounded a second time. Decimal double rounding is almost always harmless, and
"almost always" is not a specification, so the parse should take the format.

The dtype should print alongside the tensor as well, since two tensors holding
`[0.33, 0.66]` in f64 and in bf16 are different values and nothing currently
distinguishes them.

*Go bootstrap:* `internal/value.FormatNumber` is float64-only. `strconv`'s
`FormatFloat` and `ParseFloat` both take a bit size already, which is the same
generalisation this asks for.

---

## What 1.6 closed

One line each, in id order. Every one was re-verified against `twill16.exe`
rather than taken from a commit message.

- **NEEDS-1** `mode systems` now selects real semantics (types, exhaustiveness,
  `?`) rather than one advisory rule.
- **NEEDS-2** `I64` is a real int64. This is the entry the rest of the release
  rests on.
- **NEEDS-3** `enum` with payloads, and `match` checked for missing variants,
  duplicate arms, arms after `_`, a useless `_`, and arms mixing two enums.
- **NEEDS-5**, **NEEDS-42**, **NEEDS-67** structs are nominal, mutable and
  passed by handle, including through a field of another struct.
- **NEEDS-6** `Str` indexes, slices, measures and concatenates.
- **NEEDS-7** `Bytes` as a growable buffer.
- **NEEDS-8** `Dict` with insertion-ordered iteration.
- **NEEDS-10** `Res`/`Opt` and `?`, checked, with a failing top-level `?` now an
  error instead of a silent exit 0. One gap remains; see below.
- **NEEDS-11** `abort`.
- **NEEDS-13** `unit` as a literal. **NEEDS-14** `Bool` as a type name.
- **NEEDS-16**, **NEEDS-72**, **NEEDS-90** recursive payloads, nested
  containers, and a type inside its own payload through a container.
- **NEEDS-17**, **NEEDS-43**, **NEEDS-93**, **NEEDS-97** `Arr` and list get
  `pop`, in-place `push` and element assignment.
- **NEEDS-18**, **NEEDS-60**, **NEEDS-76** float text conversion in both
  directions, as `str_to_f64` and `f64_to_str`, calling Go's own `strconv`.
- **NEEDS-19** `i64_of_str` exact. **NEEDS-45** `str` on an `I64`.
- **NEEDS-20** the `%d`/`%s`/`%q` equivalents, as `str`, `str_quote` and `+`.
- **NEEDS-21** `is_same`. **NEEDS-22** `Opt` from a `Dict` lookup, matched.
- **NEEDS-24** and **NEEDS-44**, which were the same entry twice: integer `/`
  and `%`, truncating toward zero, `%` signed by the dividend, zero divisor a
  named error.
- **NEEDS-26** closures capture by handle. **NEEDS-71** an `Arr` parameter
  aliases.
- **NEEDS-27**, **NEEDS-70** equality: different types never equal, enums
  structural, payload-free cases by case. `Some(1) == Some(1)` used to be false.
- **NEEDS-28**, **NEEDS-48**, **NEEDS-56**, **NEEDS-58** the process surface:
  `read_file`, `write_file`, `args`, `exit`, `write_out`/`write_err` on `Str`,
  `emit_line`, `resolve_path`.
- **NEEDS-34** `chr`. **NEEDS-35**, **NEEDS-99** `Str + Str`.
- **NEEDS-36** `arr(...)`. **NEEDS-37** `env` returning `Opt`.
- **NEEDS-38** `is_tty_stdout` and `window_size` (no `is_tty_stderr`).
- **NEEDS-39** `mono_ns`, a monotonic clock distinct from `clock_now_ms`.
- **NEEDS-40**, **NEEDS-65**, **NEEDS-68**, **NEEDS-69** `F64` as a systems
  scalar and the float primitives over it, transcendentals included.
- **NEEDS-41** the read-only tensor view, as `shape` plus `arr_of_tensor`.
- **NEEDS-49** the systems-mode checker policy, decided and implemented. See
  its entry for which of the two candidate policies was taken and why.
- **NEEDS-51** the import resolver, on `module_source`.
- **NEEDS-55** the seeded generator, global and first-class.
- **NEEDS-59** whole-file read and write. **NEEDS-63** the opaque gbm handle.
- **NEEDS-64** `save_value`/`load_value`. **NEEDS-66** the three builtins the
  checker did not know.
- **NEEDS-73** `abort` in value position.
- **NEEDS-77** the formatter no longer deletes `unit USD`, fixed in the Go
  printer and the self-hosted one in one change, which is what it was waiting
  for.
- **NEEDS-79** a `Dict` takes an `I64` key, by decimal rendering underneath.
- **NEEDS-82**, **NEEDS-86** a file-level `let` may be initialised by a call; it
  runs once and the value is shared.
- **NEEDS-84** `f64_bits`/`f64_from_bits`, exact now that `I64` is.
- **NEEDS-85** `shl(1, 63)` round-trips, so `src/float.tw`'s `ushr` helpers are
  compensating only for the missing unsigned type. They still work.
- **NEEDS-91**, **NEEDS-92** the filesystem: `path_exists`, `path_is_dir`,
  `mkdir_all`, `remove_file`, `remove_dir`, `remove_all`, `rename`, `temp_dir`,
  `cwd`, `mtime`, and the path string operations `path_join`, `path_base`,
  `path_dir`, `path_ext`, `path_stem`, `path_normalize`, `path_is_abs`.
- **NEEDS-94** answered rather than closed: `abort` stops a numeric-mode program,
  so `std/nn`'s NaN workaround is now a library debt and not a language gap.
- **NEEDS-95** answered the same way: a numeric-mode file can hold an `Rng` now,
  so the mode-boundary argument in that entry is wrong.
- **NEEDS-98** `{}` is the empty record, so `with_field` has something to build
  from.
- **NEEDS-110** dtype names, in the bootstrap as well as in twill.
- **NEEDS-111** the four byte primitives, so the packed buffer runs.
- **NEEDS-114** dtype-aware printing: a bf16 tensor prints `0.334` and its
  dtype.

Two things landed that no entry had asked for, and they belong here because they
were silent wrong answers rather than missing features. A gradient taken inside a
gradient is now refused wherever it is written rather than only as a literal
`grad(grad(f))`, and `tensor(list(...))` over gradient-tracking values is
differentiable rather than returning a zero gradient. `std/gradcheck.tw` is the
finite-difference checker that makes both testable from twill.

## What 1.7 closed

- **NEEDS-3, the pattern language.** Nested patterns, literal patterns and
  guards, with exhaustiveness rewritten to recurse and to count only the arms
  whose running depends on the value's shape.
- **NEEDS-4, user-defined generics.** `struct Box[T]`, `enum Tree[T]`,
  `fn first[T]`, checked by substitution at use sites. No monomorphization,
  because a tree walker over dynamically typed values does not need one -- which
  also answers NEEDS-90 by removing the thing that raised it.

Both on the Go bootstrap and in `src/` together, verified by comparing the two
checkers character for character over 404 files.

## What is still open after 1.6

### Language

*(The two entries that stood at the top of this list until 1.7 -- user-defined
generics, NEEDS-4, and the pattern language, NEEDS-3 -- are both closed. What
1.7 closed is its own section below.)*

- **Lazy iteration (NEEDS-96).** No generator, no `yield`, no lazy sequence.
  `epoch_batches` materialises every batch of an epoch, and `std/io` cannot
  stream a file larger than memory. This is a real limit on dataset size rather
  than an inconvenience.
- **Keys are not general (NEEDS-79, NEEDS-81).** A `Dict` key is a `Str`, or an
  `I64` that becomes one, so `i64(3)` and `"3"` collide, and there is no way to
  key on the identity `is_same` compares. The tape's node lookup stays a
  quadratic scan because of the second.
- **`trim_space` over Unicode (NEEDS-61).** ASCII only, so a non-breaking space
  around a CSV number parses on the Go side and fails here.

### Runtime

- **No process interface.** `read_file` and `write_file` exist; there is no way
  to start another process, and no `stdin_all` or `read_line` (NEEDS-47), so a
  twill-hosted REPL still cannot read its own input.
- **No ranged file read.** Reading is whole-file only (NEEDS-59): there is no
  offset-and-length read and no line-at-a-time reader, which is the same
  limitation NEEDS-96 hits from the iteration side.
- **No memory counters.** Nothing reports allocation, live bytes or peak, so
  NEEDS-111's 2x-to-8x saving is a property of the layout that no twill program
  can measure. `nbytes` reports a tensor's own size and nothing aggregates it.
- **No `stat` (NEEDS-91).** Existence, kind, size and modification time are four
  calls, so a program asking all four can see the file change between them.
- **No `is_tty_stderr` (NEEDS-38).** The two streams cannot be asked about
  separately, which is the case that entry warned about.
- **No FFI (NEEDS-107).** Deliberate. It is a much larger decision than anything
  that currently wants it.

### Tooling and libraries

- **`std/frame.tw` has not caught up (NEEDS-98, NEEDS-99).** `select`, `drop`,
  `rename` and `from_columns` are still unwritten and `group_agg` still returns
  `key` and `value`, though `{}` and `+` both exist now. The same is true of
  `std/nn`'s NaN workaround (NEEDS-94), `std/batch`'s global reseeding
  (NEEDS-95), and `std/io` `normalize`'s rebuild loop (NEEDS-93): four library
  workarounds whose reason for existing was removed and which are still there.
- **Blank lines between comments (NEEDS-78).** One case left in the formatter.
- **`%v` on an axis list (NEEDS-74).** One diagnostic, one decision unmade.
- **Parse depth (NEEDS-30).** The *call* half is closed for the shapes people
  write: both evaluators refuse a call nested past 10,000 with a diagnostic,
  though a call sitting deep enough inside its own expression still exhausts the
  host stack before the counter fires. The *parse* half is not closed at all.
  The parser and the checker are recursive descent over user input and nothing
  counts their depth, so a sufficiently nested file still crashes the Go parser
  with a stack overflow rather than being refused: 713,916 nested parentheses
  parse, 713,917 crash.
- **Initialiser order between file-level bindings (NEEDS-86).** Runs once and is
  shared, both verified. Order is unspecified and currently does not matter.

### GPU

Deferred, on the project's own measurements rather than by neglect
(NEEDS-100 through NEEDS-108). The fifteen signatures are now names on the
builtin table that return `no GPU backend in this build`; `gpu_available()` is
`false` and `gpu_device_count()` is `0`, which is the graceful-degradation
contract NEEDS-100 asks for and is the only part that works. Nothing can be
implemented behind them without NEEDS-107 or native code in the runtime, which
is NEEDS-108, and `docs/gpu-feasibility.md`'s own recommendation is to settle
f32 and find a twill program that is matmul-bound at 256x256 or larger first.
Neither has been done, so the deferral is still the right one and is recorded
here so it is read as a decision rather than as an oversight.
