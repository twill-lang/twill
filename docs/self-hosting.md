# Self-hosting twill

The decision is taken: the language is renamed to **twill**, files carry the
extension **.tw**, and the implementation will be written in twill. This
document designs that. It does not re-argue the decision.

It supersedes `docs/rewrite-plan.md` on the target question only. That document
rejected self-hosting, and the objection it raised is the one this design has to
answer, so it is worth quoting rather than skipping past:

> Twill today cannot express its own compiler. It has no pointers, no mutable
> aggregates beyond record field assignment, no byte or char type, no sum types
> or pattern matching, no arbitrary file IO, and no way to write a lexer that is
> not fighting the type system.

That is correct and it is still correct. Everything else in the rewrite plan
survives unchanged and is depended on here: the staging discipline, the
differential harness in `tools/diff/`, the fixture corpus in `testdata/`, the
byte-exact canonical dump, the RSTR compatibility constraint, and the effort
model. Read it first. This document is the branch that starts where its section
1 said "not the target of this rewrite", and the honest framing is that
self-hosting is a **strictly larger** project than the Rust rewrite, not an
alternative to it, because it needs most of the Rust rewrite as a substrate.

The real project is not "write a compiler in twill". The real project is
**designing the systems subset of twill**. The compiler is downstream of that,
and is the easy half.

## 1. The systems subset

### 1.1 The governing decision: file-level modes

Every feature below is additive and every one of them risks the 279 existing
tests. The mechanism that makes the risk manageable is a single decision taken
up front:

**A `.tw` file declares its mode on its first non-comment line. The default is
numeric mode, which is exactly the language documented in
`docs/language-guide.md` with nothing added. A file beginning with `mode
systems` gets the subset described here, and gives up units of measure and
implicit dynamic typing in exchange.**

Not two languages. One grammar, one AST, one interpreter, one checker, with a
mode flag that decides which rules are mandatory. The systems subset is not a
dialect users must learn: a numeric user never writes `mode systems` and never
sees any of it.

This is the only structure under which the 279 tests are safe by construction
rather than by inspection, because no existing file declares the mode, so no
existing file changes meaning. It is also the only structure that keeps one
`twill fmt` and one editor grammar. The cost is a permanent seam in the
implementation and a language reference with two halves, which is real and is
paid every time a feature is added to either half.

Everything from here is what `mode systems` turns on.

### 1.2 The features

For each: what it is, why a compiler needs it, what it costs the numeric
language, and whether it can land without breaking the suite.

#### I64: a signed 64-bit integer, distinct from float64

*What.* A separate scalar type `I64`. Two's complement, exactly 64 bits.
Wrapping is **defined, not trapped**: `add`, `sub`, `mul` wrap silently, shifts
mask the shift count to 0..63, and division by zero is an error value, not a
panic. Bitwise `and or xor shl shr not` on I64. Explicit conversions only:
`i64(f)` truncates toward zero, `f64(n)` widens and may lose precision above
2^53. No implicit conversion in either direction, ever.

*Why a compiler needs it.* Source offsets, line numbers, token kinds, string
lengths, array indices, hash values, and bytecode operands are all integers.
float64 holds indices exactly up to 2^53 and a compiler will never exceed that,
so the argument is not range. It is that hashing, bit packing, and bytecode
encoding are bit operations, and doing them on a float is either impossible or
a source of the exact class of bug that "stability" is supposed to eliminate.

*Cost to the numeric language.* None if the mode gate holds. Numeric mode never
sees I64 as a value type. The cost is in the runtime: `value.Value` gains a
case, every switch in `interp` and `builtins` gains a default arm that rejects
it, and the equality walk in `equality_test.go` gains a rule (I64 compares to
I64 by bits; I64 is never equal to a tensor, following the existing
"different types are never equal, not an error" rule).

*Naming conflict, concrete.* `int` is already a builtin function that truncates
a scalar (`builtins.go`). The type must therefore **not** be spelled `int`.
This is why every type name in the subset is capitalized: `I64`, `Byte`,
`Bytes`, `Str`, `Arr`, `Dict`, `Opt`, `Res`. Capitalized identifiers are
currently used only for user record types (`type Model = {...}`), and a
systems-mode file that declares a struct named `I64` is a shadowing error the
checker reports. Similarly `map` and `len` and `list` and `append` are builtins,
so the dictionary type is `Dict[K, V]`, never `map`.

*Suite risk.* Low. New type, new mode, no existing token reused.

#### Byte, Bytes, and a Str that is not a tensor

*What.* `Byte` is an I64 constrained to 0..255 at construction. `Bytes` is a
growable, mutable, heap-allocated sequence of bytes with `len`, byte indexing
returning I64, byte assignment, `push`, slicing that returns a copy, and
`concat`. `Str` is an **immutable byte string** with O(1) length and O(1) byte
indexing, convertible to and from `Bytes` by copy. There is no character type,
no rune, and no unicode normalization anywhere. A `Str` is bytes that print.

*Why a compiler needs it.* A lexer reads source bytes and compares them against
literals. Today `value.Str` is a Go string with no indexing, no slicing, no
concatenation operator, no length, and no way to get a byte out of it, so a
lexer in twill today cannot read its first character. `Bytes` is also how object
files, the bytecode blob, and the RSTR reader are expressed.

*The unicode decision, argued.* The subset treats source as bytes and does not
decode UTF-8. Identifiers stay `[A-Za-z_][A-Za-z0-9_]*`, which is already the
rule in `docs/language-guide.md`, so byte-level lexing is not a restriction, it
is the existing specification. Multi-byte sequences inside string literals and
comments pass through untouched because the lexer only looks for the closing
quote and the newline, both of which are ASCII and cannot appear inside a UTF-8
continuation byte. This is correct rather than convenient, and it is why it is
safe. If twill ever wants unicode identifiers, that is a decoder in the
front end and not a change to this type.

*Cost to the numeric language.* Small and arguably positive. Numeric mode gains
`Str` indexing and slicing, which people already want for parsing CSV headers.
It must **not** gain implicit `Str` to tensor coercion.

*Suite risk.* Medium, and it is the one place to be careful. `Str` is currently
`type Str string` and is compared, printed, and stored. Making it a distinct
indexable value with a length is fine; the risk is `print` and `str()` output
changing by one byte for some value, which the canonical dump corpus in
`testdata/` catches immediately. Land it behind that corpus, not before it.

#### Sum types with exhaustive match

*What.*

```
enum Tok {
  Ident(Str),
  Num(F64),
  Punct(Str),
  Eof,
}

match t {
  Tok.Ident(name) => ...,
  Tok.Num(v)      => ...,
  Tok.Punct(p)    => ...,
  Tok.Eof         => ...,
}
```

Tagged unions with payloads, matched by pattern, **exhaustiveness enforced by
the checker as an error, not a warning**. Nested patterns to one level, literal
patterns on I64 and Str, and `_` as the catch-all. No guards in v1.

*Why a compiler needs it.* A token is one of about thirty things. An AST node is
one of about forty. A type is one of a dozen. An IR instruction is one of a
hundred. Every one of those is a sum, and the entire reason a compiler written
in a language with sums is maintainable is that adding a case makes the compiler
list every place that must change. That property, and not speed, is the strongest
argument in this whole document for the language being written in itself: without
exhaustive matching, adding an AST node to twill is a hunt, and the hunt is
exactly what "stability" is meant to prevent.

*Cost to the numeric language.* This is the largest single addition and the one
that most changes what twill is. Two new keywords (`enum`, `match`), a pattern
grammar, a new `Value` case, a new checker type, and formatting rules in
`internal/format`. `match` is contextual: it is a keyword only at expression
position in a systems-mode file, so `let match = 3` still works in numeric mode.

*Suite risk.* Low mechanically, high in review. The grammar additions do not
touch existing productions. The risk is that the pattern-matching parser
introduces an ambiguity with record literals, since `{` already means either a
block or a record depending on whether `name:` follows. Use `=>` arms with
comma separators, never braces, specifically to avoid re-entering that
ambiguity.

#### Arr[T]: a typed growable array

*What.* `Arr[T]` is a heap-allocated, mutable, growable, homogeneous array with
`len`, index, indexed assignment, `push`, `pop`, `slice` (copying), and
amortized O(1) growth. Distinct from `*tensor.Tensor` (which is numeric,
n-dimensional, and differentiable) and from `value.List` (which is
heterogeneous and, in practice, immutable-by-convention).

*Why a compiler needs it.* Token streams, statement lists, scope stacks,
constant pools, and instruction buffers. `append(xs, x)` today returns a new
list; building a 200,000-token stream that way is quadratic and would make the
self-hosted lexer slower than the file it is reading.

*Cost to the numeric language.* A third sequence type, which is a genuine cost
to the language's smallness. `docs/design.md` principle is a small readable
language and this violates it. The mitigation is the mode gate: numeric mode
still has exactly two sequence types.

*Suite risk.* Low. New type.

#### Dict[K, V]: a hash map

*What.* `Dict[Str, V]` and `Dict[I64, V]` only. Insert, lookup returning
`Opt[V]`, delete, `len`, and iteration in **insertion order**, matching the
existing `value.Record.Keys` discipline.

*Why a compiler needs it.* Symbol tables, string interning, keyword recognition,
label resolution. Every one of these is a map and none of them is expressible
today; `Record` has statically-known field names only.

*Insertion order, argued.* Go's map iteration is deliberately randomized and the
existing interpreter avoids depending on it (`Record` carries `Keys` precisely
so that "printing or iterating a namespace gives the same result on every run").
A self-hosted compiler that iterates a symbol table in a random order emits
different bytecode on every run, and the triple-build check in section 2 then
fails for a reason that has nothing to do with a bug. Deterministic iteration is
not a nicety here, it is a precondition for the bootstrap to be checkable.

*Cost to the numeric language.* None; a hash map is uninteresting to array code.
*Suite risk.* Low.

#### Struct: nominal, mutable, typed records

*What.* `struct Lexer { src: Str, pos: I64, line: I64 }`, constructed by name,
fields typed, fields mutable in place, and **reference semantics**: passing a
struct passes a handle, and mutating a field is visible to the caller.

*Why a compiler needs it.* A lexer is a cursor that advances. A parser holds a
token position it moves. Threading `(src, pos, line)` through every function and
returning updated copies is possible but it makes the compiler twice as long and
the diffs unreadable.

*No methods.* Recommended: free functions plus the existing namespaced import
(`import "std/lex" as lex` then `lex.next(s)`) give the same call-site reading
as methods with none of the receiver-resolution machinery, no vtables, and no
interface question. Add methods later if the compiler's own source proves they
are needed, which is exactly the kind of question dogfooding is supposed to
answer.

*Cost to the numeric language.* Reference semantics are new and they are
genuinely dangerous next to a language where `Record` is value-ish and `grad`
walks record structure. Keep `Record` and `Struct` separate types. Do not
retrofit mutation onto `Record`, because `grad(loss)({w:..., b:...})` returning
a record depends on records not aliasing.

*Suite risk.* Low if kept separate, high if unified. Do not unify them.

#### No pointers, and garbage collection stays

*What is deliberately not added:* address-of, pointer arithmetic, manual
allocation, free, and any notion of a memory address. `Arr`, `Dict`, `Struct`,
`Bytes`, and enum payloads are heap values with handle semantics, collected by
the host runtime.

*Why.* The rewrite plan lists "no pointers" as a blocker for self-hosting. It is
not one. Go's compiler uses pointers because Go has them; a compiler needs
**mutable aliasable aggregates and recursive types**, which is what the four
types above provide. Adding a pointer type would import an ownership question
that the numeric half of the language has no answer to and no use for. Recursive
data (an AST) is expressed as an enum whose payload is a struct, which is the
same shape Rust uses and needs no explicit indirection syntax.

*The cost, stated plainly.* The self-hosted compiler is a garbage-collected
program and will allocate more than a hand-written C or Rust one. For a compiler
this is the right trade, and it is what OCaml, Go, and Java compilers do.

#### Res[T, E]: errors that are values

*What.* `enum Res[T, E] { Ok(T), Err(E) }` and `enum Opt[T] { Some(T), None }`,
both ordinary enums with no compiler magic, plus one piece of sugar: postfix
`?` on a `Res`-typed expression returns early with the `Err` if it is one. The
enclosing function must itself return a `Res`, checked statically.

*Why a compiler needs it.* A compiler's normal output on bad input is a
diagnostic, not a crash. Today the interpreter's error path is Go `error`
returns internally and the CLI prints them; a twill program has no way to
express "this failed and here is why" other than by convention. A self-hosted
checker that panics on a malformed program is worse than the one it replaces.

*Panics stay, and are narrowed.* A panic (`abort(msg)`) remains, and means a
**compiler bug**, never a user error. That distinction is worth enforcing in
review: every `abort` in the self-hosted compiler should be unreachable by any
input.

*Cost to the numeric language.* One operator (`?`), gated to systems mode. The
generics needed to express `Res[T, E]` are the real cost; see below.

*Suite risk.* Low.

#### Generics, minimally

`Arr[T]`, `Dict[K, V]`, `Opt[T]`, and `Res[T, E]` cannot be spelled without
type parameters. This is the item most likely to be underestimated.

*Recommended scope:* type parameters on structs, enums, and functions. **No
bounds, no constraints, no traits, no inference beyond unifying against argument
types at the call site.** Monomorphized by the checker. That is enough for a
compiler and it is roughly the smallest generic system that is not a lie.

*Uncertainty, flagged.* This is the feature I am least confident in the estimate
for. Unconstrained monomorphization is easy in principle and every language that
has shipped it has discovered a long tail (recursive instantiation, error
messages that name a synthesized type, the interaction with the existing shape
variables in annotations, which look like type parameters and are not). Budget
generously and expect the checker work to dominate.

#### File IO and process interface

`read_file(path) -> Res[Bytes, Str]`, `write_file(path, Bytes) -> Res[Unit, Str]`,
`stdin_all()`, `write_out(Bytes)`, `write_err(Bytes)`, `args() -> Arr[Str]`,
`exit(I64)`. Nothing else. No sockets, no directories beyond a `list_dir`, no
environment beyond `env(name) -> Opt[Str]`. A compiler reads files, writes
files, and reports.

> **This section's "nothing else" no longer holds, and the heading was right
> before the text was.** `run(program, argv, dir) -> Res[Str, Str]` landed after
> spool `docs/needs.md` entry 1 spent every other entry on that list getting
> delivered while this one stayed open: a package manager fetches by running
> `git`, and neither route out of here was open to it. It is a process
> interface and not a network one -- there are still no sockets -- and it takes
> an argument vector rather than a command line, so there is no shell on the
> path. The toolchain wants it anyway to drive a linker.

*Cost to the numeric language.* This is the only item that widens the security
surface of running a `.tw` file, and it should be said out loud: today a twill
program can read a CSV and write a model. After this it can read and write
arbitrary files. That is what a compiler is, and there is no version of
self-hosting where it is avoidable.

### 1.3 What the shape checker keeps checking

The existing checker is best-effort by design: it "stays quiet when a shape
can't be determined". That policy is right for numeric code and wrong for a
compiler, where an unchecked type is a bug found at runtime in a program that
compiles other programs.

**In systems mode the checker becomes mandatory and total.** Every binding,
parameter, field, and return has a type that is either annotated or inferred
from an annotated source, and an unresolved type is an **error**, not silence.
This is a different checker policy over the same checker.

Keeps working, unchanged:

- The whole numeric-mode analysis: shapes, broadcast rules, shape variables,
  `@` inner-dimension checks, record field existence, declared record types.
- The existing lattice mostly extends rather than changes. `internal/checker`
  already has `tTensor tUnknown tBool tStr tUnit tList tRecord tFn tBuiltin`;
  the subset adds `tI64 tBytes tArr tDict tStruct tEnum` and a type-parameter
  binder. Nothing is removed.
- Exhaustiveness of `match`, which is a new check and a cheap one: collect the
  enum's variants, subtract the arms, report what is left by name.

Cannot keep working, and should not try:

- **Shape checking does not apply to the subset.** A compiler has no tensors.
  `Arr[T]` has a length, not a shape, and its length is dynamic by construction.
  Any attempt to track array lengths statically is a dependent type system and
  is out of scope. The checker's shape machinery is simply inactive in systems
  mode, which is honest and is cheaper than pretending.
- **Units of measure are unavailable in systems mode.** Units annotate scalar
  float64 quantities. There is no unit of a token. Declaring `unit` in a
  systems-mode file is an error rather than a no-op, because a silent no-op is
  how a user discovers six months later that their annotations meant nothing.
- **`tUnknown` is not permitted to survive.** In numeric mode it means "stay
  quiet". In systems mode reaching a `tUnknown` at the end of inference is a
  diagnostic. Same lattice, opposite policy, and this is the single most
  important line in this section.

### 1.4 Summary table

| Feature | Numeric mode cost | 279-test risk |
|---|---|---|
| `I64`, bitwise ops, defined wrapping | none (gated) | low |
| `Byte`, `Bytes`, indexable `Str` | `Str` gains indexing | **medium**: `print`/`str` output must not move |
| `enum` + exhaustive `match` | two keywords, pattern grammar, formatter | low mechanically |
| `Arr[T]` | a third sequence type | low |
| `Dict[K,V]`, insertion-ordered | none | low |
| `struct` with reference semantics | a second record-like type | low if kept separate from `Record`, high if unified |
| `Res`/`Opt` + `?` | one operator | low |
| generics, unbounded, monomorphized | checker complexity | low mechanically, **high in schedule** |
| file and process IO | widened capability of any `.tw` file | low |
| mandatory typing in systems mode | none | none by construction |

## 2. The bootstrap chain

### 2.1 What the self-hosted implementation targets

This has to be settled before anything else, because "compiles itself" is
undefined until it is. The current implementation is a tree-walking interpreter.
There is nothing for a self-hosted compiler to emit.

Three candidates.

**Emit C.** Rejected. It moves the dependency onto the user: anyone running
`twill run x.tw` would need a C compiler. That destroys the property
`docs/design.md` principle 4 exists to protect, which is that a user installs
one binary and needs no toolchain. It also makes the release pipeline worse
rather than better, and it makes error line numbers a two-stage mapping problem
for no gain.

**Emit native machine code.** Rejected. It means an x86-64 backend, an aarch64
backend, object file emitters for ELF, Mach-O and PE, and either a linker
dependency or a linker. That is three or four separate multi-month projects, and
none of them makes twill more stable, which is the stated reason for doing any of
this. Revisit in five years or never.

**Emit bytecode for a VM. Recommended.** One backend. The bytecode is
platform-independent, which means the artifact compared in the triple-build
check is **identical on every platform**, and that is worth more to this project
than it sounds: a byte-for-byte bootstrap check that only holds on one OS is a
much weaker check. Cross-platform release stays what it is today, a loop over
five targets building the native core.

This is also already the rewrite plan's Stage 1 and Stage 2 (a register-based
bytecode VM, and a serialized module format carrying bytecode, constants, and
resolved shape information). The self-hosting path does not need a new artifact
format. It needs that one.

### 2.2 The layering, stated honestly

The result is **partial self-hosting**, and calling it anything else would be
dishonest:

```
  twill source (.tw)
        |
        v
  front end: lexer, parser, checker, bytecode emitter    <- written in twill
        |
        v
  module format (bytecode + constants + resolved shapes)
        |
        v
  native core: VM, tensor engine, autodiff tape,
  ~90 builtins, RSTR serializer, gbm, GC, threads        <- NOT written in twill
```

The native core is Rust, per `docs/rewrite-plan.md`, and that plan is a
prerequisite of this one rather than a competitor to it.

The core is not self-hosted, and it should not be. The tensor engine wants SIMD
intrinsics, a memory allocator, and control over layout; twill has none of those
and adding them would mean designing a second, lower systems subset with
pointers, which section 1.2 deliberately refused. This is not a compromise
peculiar to twill. rustc is self-hosted and its backend is LLVM in C++. Go's
compiler is Go and its runtime's hot paths are assembly. Every self-hosted
language draws this line somewhere; the only choice is where and whether you say
so.

Where it is drawn here: **everything that reads twill source is twill;
everything that executes it is not.** That line is defensible, it is exactly the
part where dogfooding pays (the compiler is the largest twill program in
existence and finds the language's flaws), and it is the part where a foreign
toolchain being installed actually irritates a contributor.

The zero-dependency promise survives intact for the user, because the shipped
binary is the native core with the front end's bytecode **embedded**, exactly as
`std/embed.go` embeds the standard library today. One binary, no toolchain.

The promise does **not** survive for the maintainer, and this is the cost the
rewrite plan named correctly: to build twill from source you need a prior twill
binary, or its bytecode blob checked into the tree. Section 2.4 decides which.

### 2.3 The stages

Stage numbering continues `docs/rewrite-plan.md`, whose Stages 0 to 3 are
prerequisites and are not re-costed here.

**Prerequisite P (already underway).** Rewrite-plan Stage 0: the canonical
dumper, the extracted fixture corpus in `testdata/`, the differential runner in
`tools/diff/`. Two agents are landing this now. Nothing below is safe without
it.

**Prerequisite Q.** Rewrite-plan Stages 1 and 2: the bytecode instruction set,
the VM, and the serialized module format. This is the artifact the self-hosted
compiler emits. It cannot be skipped and it cannot be designed later, because
the shape of the module format determines what the twill-side emitter has to
build.

**Stage S1: the rename.** Section 4. Land first, land alone, land before any
of the below, so the old name is not baked into a second implementation.

**Stage S2: specify the systems subset.** A document, a grammar, and a
conformance corpus of `.tw` files with expected outputs and expected errors,
added to `testdata/` and run by `tools/diff/`. No implementation. The deliverable
is that someone other than the author could implement it.

**Stage S3: implement the subset in Go-twill.** The Go implementation is the
bootstrap compiler, the same role OCaml played for Rust and C played for Go.
Extend `lexer`, `parser`, `ast`, `checker` (mode-gated mandatory typing),
`value`, `interp`, `format`, and the builtin set. Ends when the S2 conformance
corpus passes and the 279 existing tests are still green under `go test ./...`,
with the `testdata/` canonical dumps unchanged byte for byte. That last clause is
the actual acceptance criterion; the test count is a proxy for it.

**Stage S4: write twill-in-twill, run by Go-twill.** The front end, in
systems-mode `.tw`: lexer, parser, formatter, checker, bytecode emitter. It runs
interpreted, on top of the Go implementation, and it is slow, and that is fine.
Ends when it compiles the whole `examples/` and `std/` corpus to module files
that the VM executes to the same canonical dumps the Go front end produces. That
is a differential check against the existing harness, not a new kind of test.

**Stage S5: the triple build.** The standard check, spelled out for this
project:

- `stage1` = the front end's source, interpreted by Go-twill, compiling the
  front end's source. Output: `compilerA` (a module file).
- `stage2` = `compilerA`, run on the VM, compiling the front end's source.
  Output: `compilerB`.
- `stage3` = `compilerB`, run on the VM, compiling the front end's source.
  Output: `compilerC`.

**The check is `compilerB == compilerC`, byte for byte.** It is not
`compilerA == compilerB`, and expecting that is the usual mistake: `compilerA`
was produced by a different producer (the Go interpreter's emitter path) and may
legitimately differ in, for example, constant pool ordering. `B == C` is the
fixed point, and reaching it proves the compiler compiles itself to a stable
artifact.

What makes this check achievable rather than aspirational is determinism, and
the two places it can be lost are already known: `Dict` iteration order (fixed
by section 1.2's insertion-order rule) and any use of the seeded PRNG in the
compiler (there is none, and there should be a test asserting there is none).

`stage3` is also where the release artifact comes from: `compilerC` is the
bytecode blob embedded in the shipped binary.

**Stage S6: the Go implementation's fate.** Recommendation: **keep it, frozen,
as an oracle. Do not retire it.**

The argument for deletion is maintenance cost, and it is real: every language
change after S6 would have to be written twice. The argument against deletion is
stronger. `tools/diff/` compares two binaries. After self-hosting there is only
one, and the differential harness that made every stage of this project safe
stops working the day the second implementation is deleted. Freezing the Go
implementation at the last pre-self-hosting version and running the corpus
against it forever costs nothing per language change (the frozen oracle is not
updated; new features simply are not covered by it) and keeps a byte-exact
regression check on everything that existed before.

Move it to `reference/` and say in `CHANGELOG.md` what it is for. Delete it when
the corpus it can still run has stopped being interesting, which will be years.

### 2.4 The bootstrap artifact problem

A self-hosted compiler needs a prior compiler. There are three ways to supply it
and they differ in how much trust they require.

1. **Check the bytecode blob into git.** Simple, works offline, and means
   `git clone && build` works with nothing installed. Cost: a binary artifact in
   the tree that nobody can review, which is the trusting-trust problem in its
   purest form.
2. **Download a pinned, checksummed release binary.** Keeps the tree clean.
   Cost: the build now needs the network and a hosting service that must exist
   forever.
3. **Keep the Go implementation buildable and bootstrap through it every time.**
   Cost: a Go toolchain is a build dependency forever, which is most of what
   self-hosting was supposed to remove.

Recommended: **(1) as the default, with (3) as a documented full-source
bootstrap path.** The checked-in blob makes the common case trivial; the
existence of a reproducible route from Go source to that exact blob makes the
blob auditable by anyone who cares to. Requiring the blob to be reproducible
from source in CI is a one-job check and it turns a trust problem into a build
problem. This is also why S6 recommends keeping the Go implementation: it is the
escape hatch from the trusting-trust problem, not only an oracle.

## 3. What self-hosting buys and costs

The stated reason is stability. Taken at face value, self-hosting does not
deliver stability, and it is worth being precise about what it does deliver.

### Genuine

- **The compiler becomes the largest twill program that exists.** Roughly 8,000
  to 12,000 lines of twill exercising the language every day, versus a `std/`
  that is 543 lines. Every ergonomic flaw, every missing composition, every
  error message that does not say what is wrong is hit by the person who can fix
  it. This is the strongest real benefit and it is a benefit to the *language*,
  not to the implementation.
- **A language change that is painful to implement is felt immediately.** The
  feedback loop between "this feature is convenient" and "this feature is
  horrible to compile" collapses to zero.
- **No foreign toolchain for a contributor.** After S5, changing the parser
  needs a twill binary and a text editor. Today it needs Go, and after the Rust
  rewrite it would need `rustup`. For a project whose defining virtue is one
  binary and no dependencies, this is the version of that virtue that applies to
  the maintainer as well as the user.
- **The systems subset is independently useful.** `Arr`, `Dict`, indexable
  strings, sum types and real error values are things numeric users will use for
  data wrangling regardless of whether the compiler is ever written.

### Not delivered

- **Self-hosting does not make the language faster.** Speed comes from the
  bytecode VM and the value representation, per the rewrite plan's measurements,
  and those live in the native core which is not self-hosted. If anything, a
  front end written in interpreted-then-bytecoded twill is slower than the Go
  front end, and compile times will regress. Expect that and measure it.
- **Self-hosting does not make the language safer or more correct.** A bug in
  the twill parser is exactly as much a bug as a bug in the Go parser. The 279
  tests and the differential corpus do the correctness work, and they would do
  it for any implementation language.
- **Self-hosting makes debugging harder.** When the compiler miscompiles itself,
  the tool you debug with is the tool that is broken. This is a real and
  recurring cost, and the mitigation is the frozen Go oracle in S6.
- **Self-hosting does not remove the dependency, it changes its shape.** Section
  2.4. The Rust plan's criticism on this point stands.
- **It does not reduce total code.** After S6 the tree holds a Rust native core,
  a twill front end, and a frozen Go reference. That is three implementations
  where there is one today.

The honest summary: self-hosting is a **language design** investment paid for by
a large **implementation** cost. If the goal is a better twill, it is a good
investment. If the goal is a more stable twill compiler, the fixture corpus and
the differential harness deliver that at a twentieth of the price, and they are
being built right now.

### Effort

One experienced engineer, full time. Comparable basis to the rewrite plan's 33
to 45 weeks.

| Stage | Weeks | Confidence |
|---|---|---|
| P. Spec freeze, corpus, diff harness (rewrite-plan Stage 0) | 2 to 3 | High; in progress |
| Q. Bytecode ISA, VM, module format (rewrite-plan Stages 1 to 2) | 8 to 12 | Medium |
| S1. Rename | 1 | High |
| S2. Specify the systems subset | 4 to 6 | Medium; design work, not typing |
| S3. Implement the subset in Go-twill | 12 to 16 | **Low**; generics and the checker mode dominate |
| S4. Write the front end in twill | 20 to 26 | Medium; checker is 8 to 10 of these alone |
| S5. Triple build, determinism, bootstrap artifact, release | 4 to 6 | Low; bootstrap plumbing always overruns |
| S6. Freeze the oracle, docs, CHANGELOG | 2 | High |
| **New work (S1 to S6)** | **43 to 57** | |
| **Including prerequisites P and Q** | **53 to 72** | Call it thirteen to eighteen months |

Add the rewrite plan's Rust native core (its Stages 3 to 6, 23 to 30 weeks) if
the goal is also to leave Go, and the total is two years. That number is not a
scare tactic, it is the same arithmetic the rewrite plan did, applied to a
larger target.

Two multipliers to keep in view. The rewrite plan's "add 20 percent if the
language keeps gaining features, which it will" applies here **twice**, because
S3 and S4 both have to absorb every new feature. And the S3 estimate is the one I
am least sure of: implementing unbounded generics plus a mandatory-typing mode
inside a checker built around a "stay quiet when unsure" policy could be 12 weeks
or 20, and the way to find out early is the milestone in section 5.

**Delegable to agents.** The rename sweep. The lexer in twill. Fixture and
conformance corpus authoring. The bytecode emitter for the simple statement
forms. Per-builtin porting. Documentation. Roughly 35 percent by volume.

**Not delegable.** The systems subset design (S2). The checker mode policy and
generic instantiation (S3). Determinism auditing for the triple build. The
bootstrap artifact trust decision.

## 4. The rename

**Status: done, 2026-08-07, in one commit.** This section is kept as the record
of what was decided and what was actually carried out, because two of its
recommendations were overruled by the owner and the reasoning for the rest is
still load-bearing. Where the executed decision differs from the recommendation
below, the executed decision is marked.

### What was done

**Module and packages**
- [x] `go mod edit -module github.com/twill-lang/twill`, and every import path
      rewritten. Go treats this as a new module, not a version bump, so no `/v2`
      suffix. `go build ./... && go test ./...` was the gate.
      **Overruled:** this section originally proposed
      `github.com/fabric-ml/twill`. The owner kept the `twill-lang` path, so the
      rename is a rename and not also an ownership transfer.
- [x] `cmd/raster/` to `cmd/twill/`. Binary name `twill`. `Makefile`,
      `.github/workflows/ci.yml`, `.github/workflows/release.yml` (five target
      names and the `sha256sum` step).
- [x] `RASTER_STD` to `TWILL_STD`.
- [x] Every `raster` string in CLI help, `version`, usage, error prefixes and the
      REPL banner.

**Extension, .ra to .tw**
- [x] `internal/interp/interp.go`: the import resolver. **Overruled**, see
      "`.ra` is a hard break" below: rather than becoming a two-extension
      resolver, it refuses `.ra` outright through `interp.CheckLegacyExt`.
- [x] `std/embed.go`: the `go:embed` pattern and both suffix operations.
- [x] `std/*.ra` to `std/*.tw` (6 files).
- [x] `examples/*.ra` to `examples/*.tw` (19 files). `examples/*.bin` and
      `prices.csv` untouched, and both `.bin` fixtures still load.
- [x] Glob patterns in `internal/format/format_test.go`,
      `internal/interp/examples_test.go`, `internal/interp/format_run_test.go`.
- [x] Temp-file names in `interp_test.go`, `import_test.go`, `io_test.go`,
      `record_test.go`, `frame_test.go`.
- [x] `tools/corpus/extract/main.go` fixture naming and `tools/diff/run/`
      (`main.go` case filenames and suffix filters, `gen.go`).
- [x] `testdata/` fixture filenames, 735 files including the `.golden` pairs.
      Fixture and golden were renamed and rewritten together, so the canonical
      dumps are unchanged in content.
- [x] `editors/vscode/`: `package.json` language id and `extensions`,
      `syntaxes/raster.tmLanguage.json` to `twill.tmLanguage.json` and its
      `scopeName`, `language-configuration.json`, `README.md`.

**Docs**
- [x] `README.md`, `CONTRIBUTING.md`, `docs/language-guide.md`, `docs/design.md`,
      `docs/tutorial.md`, `docs/finance.md`, `docs/gpu-feasibility.md`,
      `docs/rewrite-plan.md` (identifiers renamed, arguments left alone).
- [x] `CHANGELOG.md`: history was **not** rewritten. Entries below the new one
      still say Raster because they happened to Raster. One entry at the top
      records the rename, the extension change, the hard break and the unchanged
      magic.

**GitHub**
- [ ] **Overruled and deliberately not done.** The repository is still named
      `raster` and the git remote is unchanged. The owner does the rename in the
      GitHub UI if and when he wants it; nothing in the tree depends on it, and
      the Go module path no longer matches the repository name, which is legal
      and only matters if the module is ever fetched by path.
- [x] Release asset names change with the binary name. The previous release's
      assets keep their old names, which is correct.

### RSTR: the magic did not change

**`examples/model.bin` and `examples/params.bin` and every file users have saved
keep loading, because the magic stays `RSTR` and the version stays 1. No `TWIL`
magic was introduced.** The reasoning, unchanged and now recorded in a comment
at the definition in `internal/interp/serialize.go`:

Changing it would require the reader to accept both, and a reader that accepts
both is strictly worse than a reader that accepts one. The four bytes are not
user-visible; nothing in the language, the CLI, or the docs exposes them. A file
format named after a former name is entirely ordinary (`PK` in zip, the chunk
names in PNG, `ELF`). The bytes are a compatibility contract and the contract has
no opinion about product naming.

If the format ever changes for a real reason (a new tag, a wider index), that is
the moment to introduce magic `TWIL` at version 2 and write a reader that accepts
`RSTR` v1 and `TWIL` v2. Coupling the on-disk break to an actual on-disk change
costs one dual-path reader instead of two.

### `.ra` is a hard break, not a deprecation

**Decided by the owner, overruling this document.** The original recommendation
was to keep `.ra` readable by `run`, `check`, `fmt` and `import` for two minor
releases and then remove it. That is not what was built.

What was built: `interp.CheckLegacyExt` refuses any `.ra` path, case
insensitively and on the name alone, before the file is even opened, and returns
an error naming the `.tw` file the user should rename it to. Both the CLI (all
four of `run`, `check`, `fmt` and `--dump=canonical`) and the import resolver
route through it, so the wording is identical either way. Tests cover every
command, the import path, the case insensitivity, and the fact that a `.ra` name
is refused ahead of the missing-file error, because the extension is the user's
real problem and "cannot read file" would hide it.

The argument that lost: a user with `import "helpers.ra"` in a working program
should not have it stop running because the language was renamed. The argument
that won: two extensions is two things to explain forever, a removal date is a
promise somebody has to remember to keep, and a fallback invites `.ra` files to
keep being written. Refusing loudly once, with the fix in the error message, is a
smaller total cost than a migration that never ends. There is no deprecation
window and there is nothing to remove later.

One detail that survives from the original recommendation and is easy to get
wrong: `twill fmt --write` must never rename a file. A formatter that renames
files is a formatter people stop trusting. It does not; it refuses the `.ra`
file and leaves it alone.

## 5. The first milestone

**The rename, plus enough of the systems subset to write a real lexer, plus a
lexer for twill written in twill whose token stream is compared against the Go
lexer's over every `.tw` file in the tree. Eight to ten weeks.**

Scope, exactly:

1. Stage S1 in full. The tree is `twill`, `.tw`, the module path is
   `github.com/twill-lang/twill`, both `.bin` fixtures still load, all 279 tests
   green, `testdata/` canonical dumps unchanged.
2. `mode systems` as a file-level declaration, with the mandatory-typing policy
   in the checker.
3. Four features only: `I64` with bitwise ops and defined wrapping; `Str` with
   length, byte indexing and slicing; `Arr[T]`; `Dict[Str, V]` with
   insertion-ordered iteration. Plus `struct` and `read_file`.
4. **No sum types, no `match`, no generics beyond the two built-in containers,
   no `Res`, no `?`.** A lexer is deliberately the piece that needs least of the
   subset. A token kind can be an `I64` constant for now, and the ugliness of
   that is itself information.
5. `std/lex.tw`: a complete twill lexer, in twill, roughly 300 lines, matching
   `internal/lexer/lexer.go` including the newline-continuation rule in
   `docs/language-guide.md` (a leading `+`, `-`, `(` or `[` starts a new
   statement), which is the one genuinely subtle piece of the scanner.
6. A `tools/diff/` mode that runs both lexers over every `.tw` file in
   `examples/`, `std/`, and `testdata/` and requires **identical token streams,
   byte for byte, including line numbers**, as a required CI check.

Why this is the right first milestone:

- **It is verifiable without judgement.** The token stream either matches over
  about forty real files or it does not. It reuses the harness the other agents
  are building rather than inventing a new kind of test.
- **It is useful if the project stalls.** The rename is done. The numeric
  language permanently gains typed growable arrays, hash maps, integers and real
  string handling, which are things data code wants and which the language has
  no other route to. None of it is wasted if the compiler is never written.
- **It answers the schedule's biggest unknown early.** Section 3 flags S3 as the
  least confident estimate. This milestone is S3 in miniature, on the four
  features that are easiest to specify, and it produces a measured rate for the
  remaining ones. If four container and scalar features take sixteen weeks
  instead of eight, the 43-to-57-week figure is wrong by a factor and you know it
  in month two rather than month twelve.
- **It stops before the expensive commitment.** Sum types, exhaustive matching
  and generics are the features that change what twill is, and they are what the
  parser needs. Milestone 2 is the parser and it is the real go/no-go. Reaching
  the end of milestone 1 and deciding not to continue leaves the language better
  and the tree clean, which is the property a first milestone should have.

Done means: `twill` is the binary, `.tw` is the extension, `go test ./...` is
green, the differential corpus reports zero divergences, and
`tools/diff/run --lexers --corpus testdata/` exits zero.
