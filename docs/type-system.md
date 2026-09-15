# The stage 2 type system

`docs/roadmap.md` ranks four entries as one stage: `Res[T, E]` and `Opt[T]`
(entry 1, six callers), function values with a declared type (entry 2, six
callers), `enum` with payloads and exhaustive `match` (entry 3, five callers),
and nested and generic containers (entry 9, four callers). The roadmap says they
are one piece of work and refuses to order them. This document is the design of
that piece.

It is a design, not a specification of a shipped feature. Nothing here runs.
Where a decision is provisional it says so.

Two constraints frame everything below.

The first is `docs/design.md` principle 3: small enough to read. Stage 2 is the
largest addition twill will ever take, and smallness is the value it is most
able to destroy. Every section below states what it is not adding and why.

The second is `docs/self-hosting.md` section 1.1: features here belong to `mode
systems`. A numeric-mode file never sees an `enum`, a type parameter, or a `?`.
That gate is what makes the size of this document survivable.

---

## 1. The decision this document exists to make

twill already has a form of generic. `src/check.tw` `unify` matches a
parameter's shape annotation against an argument's actual shape, records what
each name stood for, and reports a name that stood for two things:

```
fn mm(A: [n, k], B: [k, m]) -> [n, m]
```

`n`, `k` and `m` are variables. They are undeclared, scoped to the signature,
bound at the call site from ground arguments, required to be consistent across
occurrences, and substituted into the return. `src/check.tw:1529` implements
that in forty lines with one `Dict[Str, I64]` called `subst`.

Type parameters are the same thing over a different range of values. `T` in
`Arr[T]` is undeclared, scoped to the signature, bound at the call site from a
ground argument, required to be consistent across occurrences, and substituted
into the return.

So the choice is: extend the existing binder, or build a second one beside it.

**Decision: one binder.** `subst` becomes `Dict[Str, Binding]` where

```
enum Binding {
  BDim(I64),
  BType(Type),
}
```

and `unify` and `substitute` gain a case each rather than a twin.

### Why, in order of weight

**The matching problem is the same problem, and it is the easy version of it.**
Both directions match a pattern that contains variables against an actual that
contains none. Arguments at a call site are ground: an argument's type is fully
determined before the call is checked, because `infer_expr` has already run on
it. That is why `src/check.tw` needs no occurs check, no union-find, and no
substitution composition. It walks the pattern, binds or compares, and reports.
Type parameters inherit that property unchanged. Full unification is not needed
in either direction, and writing two copies of one-way matching is writing the
same forty lines twice.

**The diagnostics are one family.** The existing message is `shape variable "k"
is 3 elsewhere but 5 in argument 2`. The type version is `type variable "T" is
Str elsewhere but I64 in argument 2`. One code path produces both, with a
rendering function per kind. Two binders produce two message families that
differ in wording for no reason a user can see.

**Not unifying now means merging later, at the worst time.** Today the two
never meet, because `docs/self-hosting.md` section 1.3 turns shape checking off
in systems mode. That looks like an argument for two systems. It is the
opposite. Roadmap entry 17, "a tensor that crosses the systems and numeric
seam", has three callers and is in stage 5. When it lands, `Arr[Tensor]` exists
and `fn stack(xs: Arr[Tensor]) -> [len(xs), n, m]` is the signature someone
will write. At that point two binders have to become one, in a checker that by
then has a self-hosted implementation and a differential corpus to keep
byte-identical. Merging costs nothing now and is a project then.

**The cost of unifying is one enum and two match arms.** That is the whole
change to `src/check.tw`. It is smaller than the second `unify`.

### What is deliberately not unified: the binding form

Shape variables are undeclared. Type parameters are declared:

```
fn map_arr[A, B](xs: Arr[A], f: fn(A) -> B) -> Arr[B]
```

This is inconsistent and the inconsistency is the point. The two modes have
opposite policies about silence, and that difference is already written down in
`docs/self-hosting.md` section 1.3: numeric mode stays quiet when it cannot
determine something, systems mode makes an undetermined type an error.

A mistyped shape variable in numeric mode produces a checker that says nothing.
That is the numeric policy working as designed.

A mistyped type name, if type parameters were undeclared, produces a function
that accepts every argument. `fn parse(s: Str) -> Res[Tokn, Str]` with `Tokn`
for `Tok` would become generic in `Tokn` and check against every call. That is
the numeric policy applied where the document says it must not be. So systems
mode keeps the existing rule at `src/check.tw:1381`, that an unknown
capitalized name on a parameter is an error, and adds one escape: a name listed
in the function's `[...]` binder is a parameter rather than an error.

The binder is on the function, the struct, or the enum:

```
struct Span[T] { lo: T, hi: T }
enum Opt[T] { Some(T), None }
fn unwrap_or[T](o: Opt[T], d: T) -> T
```

There is no binder on a `let`, and no way to bind a type variable inside a
body. A body's types come from its parameters.

### Kinds, and why a name cannot mean both

A variable's kind is fixed by the syntactic slot it appears in. Inside `[...]`
in a shape annotation, a name is a dimension variable. Inside `[...]` after a
type name, a name is a type variable. The two slots are distinguishable by
what precedes the bracket, which the parser already knows, so no lexical
convention is load-bearing. The existing convention that shape variables are
lowercase and type names are capitalized (`docs/self-hosting.md` section 1.2)
survives as a convention and is not a rule.

A name used at both kinds in one signature is an error naming both positions.
It cannot arise today, because the modes are disjoint. It is specified so that
entry 17 does not have to invent an answer under pressure.

There is a third kind, and it is not offered. Units are a kind. See section 7.

---

## 2. Function types

### Syntax

A function type is `fn(` parameter types `) ->` result type. Parameter names may
be written and are ignored.

The spelling has to be settled rather than assumed, because two codebases have
already written it and they disagree. `warp/src/pipeline.tw:76` writes
`fn_map: Fn(smp.Sample) -> smp.Sample`, capitalized. `bobbin/src/suite.tw:20`
writes `body: fn(I64) -> F64`, lowercase. Neither compiles today, which is how
the divergence survived.

**Lowercase `fn`.** It is the keyword that already introduces a function, so a
function type reads as the declaration with the names removed, and every other
type in the subset is capitalized because it is a nominal name (`I64`, `Str`,
`Arr`) while this one is not a name at all. warp's files change.

```
fn(I64) -> F64
fn(a: Str, b: Str) -> Bool
fn() -> Unit
```

It appears anywhere a type appears: as a parameter's type, as a struct field's
type, as a type argument, as a return type.

```
struct Case {
  name: Str,
  body: fn(I64) -> F64,
}

fn run(body: fn(I64) -> F64, n: I64) -> F64

fn compose[A, B, C](f: fn(A) -> B, g: fn(B) -> C) -> fn(A) -> C

let handlers: Arr[fn(Str) -> Unit] = arr_new()
```

### Checking rules

Arity is part of the type. `fn(I64) -> I64` and `fn(I64, I64) -> I64` are
unrelated.

Matching is exact and structural. `fn(A) -> B` matches `fn(A2) -> B2` when
`A` matches `A2` and `B` matches `B2`. There is no variance: a
`fn(I64) -> I64` is not usable where a `fn(I64) -> Unit` is wanted, even though
discarding a result is harmless, because permitting it is the first step of a
subtyping relation and section 8 rejects that.

Parameter names in a function type are documentation. They are not part of the
type and two function types differing only in them are the same type.

A function type says nothing about what the value captures. There is one kind of
function value in twill and there is no distinction between a closure and a
function pointer at the type level. That is not an omission to fill in later;
see the next subsection.

A function type gives no identity. Two function values are equal when they are
the same value (`docs/language-guide.md`: "Functions have no structure worth
walking, so they compare by identity"), and there is no hash. That has a caller.
`warp/src/pipeline.tw:64` carries a `name` on every stage and says why:

```
# The name is not documentation: it is the stage's identity in the cache key,
# because a function value has no stable identity a program can hash... a stage
# whose behaviour changes must change its name or its version. That obligation
# is on the caller.
```

The design does not remove that obligation. Giving a closure a content hash
means hashing a captured environment that aliases mutable structs, so the hash
would change under the caller's feet, and giving it a serial number means a
cache key that changes on every process start. warp's answer is the right one
and it stays the caller's.

A function type says nothing about differentiability. `grad` takes a value and
inspects it at runtime. A `Differentiable` function type would be a second type
system layered on the first, it would have to propagate through every
higher-order signature in `std/`, and no entry in `docs/roadmap.md` asks for it.

### Closure capture, and the tape

This is already decided and the decision is in the source rather than in a
design document, so it is restated here.

**Capture is by handle.** `src/eval.tw:43` states it and gives the case that
forces it:

```
# A closure captures its defining environment by handle, not by copy. That is
# load-bearing and it is NEEDS-26: `for i in range(3) { push(fs, fn() = i) }`
# has to give three closures that see the loop's binding, and the answer depends
# on whether the capture aliases. The bootstrap aliases, so this does.
```

`struct Closure` holds `env: Env`, and `Env` holds `parent: Opt[Env]`. Calling a
closure hangs a fresh scope off `c.env`, not off the caller's scope
(`src/eval.tw:826`). Declaring the type changes none of that.

**What the tape does is orthogonal, and must stay orthogonal.** `src/eval.tw`
around line 1610 argues at length that the gradient tape has dynamic extent
rather than being threaded lexically, because whether an operation records is a
property of who called it and not of where it was written. A closure created
outside a `grad` and called inside it must record. If the tape were part of a
closure's captured state, or worse part of its type, that closure would carry
the absence of a tape it was born with and its arithmetic would silently vanish
from the graph.

So: environment capture is lexical, tape presence is dynamic, and a declared
function type touches neither. The rule for anyone extending this is that a
function type is a shape for the checker and carries no runtime state.

**The hazard, stated.** Capture by handle plus `struct` reference semantics
(`docs/self-hosting.md` section 1.2) means a stored callback observes later
mutation. A `Case.body` that closes over a `struct Config` sees the config the
harness holds at call time, not at construction time. That is what Go does and
what the bootstrap does, so it is the compatible answer, and it is a real
footgun. It is not fixed with a `move` keyword or a capture list. Two mechanisms
for one question is worse than one mechanism plus this paragraph.

### Lambda literals, and the one inference exception

Section 8 says no inference beyond call-site unification against ground
arguments. There is exactly one exception and it is here.

Under the plain rule, a lambda's parameters are not ground and so cannot be
inferred, which makes every comparator this:

```
sort_by(names, fn(a: Str, b: Str) -> Bool = a < b)
```

Entry 5 has five callers and eleven hand-written insertion sorts. Entry 2 has
six. Both hit this on their first line. So:

**A lambda literal written syntactically in an argument position whose parameter
has a declared function type takes its missing parameter types and its result
type from that function type.**

```
sort_by(names, fn(a, b) = a < b)
```

The exception is bounded on purpose. It applies to a lambda literal, not to a
name bound to a lambda earlier. It applies in argument position, not in a `let`
whose annotation is a function type, and not through a conditional. It fills in
types that are absent; it never overrides a type that was written, and a written
type that disagrees is an error rather than a coercion. A lambda in argument
position to a parameter with no declared function type is an error in systems
mode, because an unresolved type is an error there.

That is one rule, one sentence, and it is checkable by looking at the call
expression alone. It is worth the line it costs. Nothing else gets an exception.

---

## 3. `enum` with payloads, and `match`

### Syntax

```
enum Verdict {
  Faster,
  Slower,
  Same,
  Noisy,
  Missing,
  New,
}

enum Constraint {
  Exact(Version),
  Caret(Version),
  Range(VersionRange),
}
```

A variant has zero payloads or one. Not two.

**Why one.** A two-payload variant is a tuple. Twill has a tuple type since
1.12.0, for returning several values, and it is deliberately a value with no
`.0` and no names: it is destructured or passed on whole. Adding positional
multi-payload variants here would introduce positional access (`v.0`) as a
second field syntax next to `.`, for one feature. A variant that wants two
fields declares a struct and holds it:

```
struct BinOp { op: Str, lhs: Expr, rhs: Expr }
enum Expr { EBinary(BinOp), ... }
```

That is what `src/ast.tw` already does. The cost is one struct declaration per
multi-field variant and it is paid in a place where a name is wanted anyway.

Generic enums use the binder from section 1:

```
enum Opt[T] { Some(T), None }
enum Res[T, E] { Ok(T), Err(E) }
enum Json {
  JNull,
  JBool(Bool),
  JNum(F64),
  JStr(Str),
  JArray(Arr[Json]),
  JObject(Dict[Str, Json]),
}
```

A type is in scope inside its own definition, so `Json` above is legal. That is
NEEDS-90 and section 6 says what monomorphisation does about it.

### Variant names: resolving a conflict between two existing sources

`docs/self-hosting.md` section 1.2 writes patterns as `Tok.Ident(name)`,
qualified by the enum. `src/check.tw` and `src/ast.tw` write them unqualified
(`TTensor(v)`, `EBlock(blk)`, `Some(v)`, `None`), and avoid collisions with a
per-enum prefix convention (`T*` for checker types, `E*` for expressions, `S*`
for statements). Those are two different designs and both are in the repository.

**Resolution: both spellings, with a collision rule.** Declaring
`enum E { V, ... }` always introduces `E.V`. It additionally introduces bare `V`
into the enclosing scope when no other enum in scope declares `V`. Where two do,
the bare form is an error naming both enums and the qualified form still works.

This keeps `src/` compiling as written, keeps `docs/self-hosting.md`'s example
correct, and makes the failure mode a named error rather than a silent
shadowing. The prefix convention in `src/` stops being load-bearing and becomes
what it should be, a readability habit.

### `match`

```
match t {
  TTensor(v) => v.dims,
  TList(l)   => arr_new(),
  _          => arr_new(),
}
```

Arms are `pattern => expression`, comma separated. No braces around an arm, for
the reason `docs/self-hosting.md` gives: `{` already means either a block or a
record depending on whether `name:` follows, and re-entering that ambiguity in a
new production is avoidable and should be avoided.

`match` is an expression. Every arm has the same type, joined by the existing
`join` in `src/check.tw:698`, which does not widen. Two arms with different
types is an error in systems mode rather than the numeric mode `TUnknown`.

Patterns, and the whole list:

- a variant with no payload: `None`
- a variant binding its payload: `Some(v)`
- a variant with a nested pattern one level deep: `Ok(Some(v))`
- a literal `I64` or `Str`: `3`, `"let"`
- `_`

No guards. No or-patterns. No binding the whole scrutinee with `x @ pat`. No
struct destructuring in a pattern: `match` looks at the enum tag, and a struct
has one shape so there is nothing to decide. Nesting stops at one level because
`src/ast.tw` and `src/check.tw` never need two, and because the exhaustiveness
algorithm for arbitrarily nested patterns is a different and much larger
algorithm than the one below.

### Exhaustiveness

This is the point of the feature. `docs/roadmap.md` ranks it as the highest
entry where the workaround is silently wrong rather than merely ugly.

The check: collect the enum's variants, subtract the variants named by the arms,
report what is left by name. It is an error, never a warning.

```
match.tw:14: match on Verdict is not exhaustive: missing Noisy, New
```

Two supporting rules, both cheap:

- An arm naming a variant already covered is an error. Duplicate arms are always
  a mistake and the second one is dead.
- A `_` arm that covers nothing is an error. If every variant is named, `_` is
  dead code, and dead code in a dispatch is where a later reader assumes a
  default exists.

### The `_` loophole, stated rather than closed

`_` silences exhaustiveness. That is what it is for and it is exactly the hole
through which bobbin's seventh verdict falls today. No rule closes it without
banning `_` on enum scrutinees, and banning it breaks `src/check.tw`, where

```
fn as_tensor(t: Type) -> Opt[TensorType] {
  match t {
    TTensor(v) => Some(v),
    _ => None,
  }
}
```

is correct and would be nine dead arms written out.

So the distinction is real but not mechanical, and is recorded as a review rule
rather than a checker rule:

**`_` is right when every unlisted variant maps to the same answer for a reason.
`_` is wrong when the match is dispatch.** `as_tensor` projects: anything that
is not a tensor has no tensor in it, and a tenth variant does not change that.
`compare` in `bobbin/src/baseline.tw` dispatches: a seventh verdict has a
seventh behaviour and falling through to a sixth is a wrong answer with no
symptom.

An arm that cannot happen writes `abort`, not `_`. See section 6 on `Never`.

This is the weakest part of the design and it is written down as such.

---

## 4. `Opt[T]`, `Res[T, E]`, and `?`

### The types

Both are ordinary enums declared in the systems-mode prelude. Neither has
compiler magic in its definition.

```
enum Opt[T] { Some(T), None }
enum Res[T, E] { Ok(T), Err(E) }
```

`Unit` is a type with one value, written `unit`, already present in the checker
lattice as `TUnit`. `Res[Unit, Str]` is the type of a fallible operation with no
useful result, which is most of the migration in section 4.4.

### `?`

The one piece of sugar. On an expression of type `Res[T, E]`, in a function
whose declared return type is `Res[U, E]`, `e?` evaluates to `T` or returns
`Err` from the enclosing function.

```
fn load(path: Str) -> Res[Manifest, Str] {
  let src: Str = read_file(path)?
  let doc: Doc = parse_toml(src)?
  build(doc)
}
```

It also works on `Opt[T]` in a function returning `Opt[U]`, returning `None`.

Three restrictions, each with a reason:

**The error types must be identical.** No conversion, no widening. Converting
`Res[T, E1]` to `Res[T, E2]` automatically requires a relation between `E1` and
`E2`, and the way every language expresses that relation is a trait. Section 8
rejects traits. The cost is an explicit call:

```
let n: I64 = map_err(parse_i64(s), fn(e) = concat("bad count: ", e))?
```

`map_err` is a prelude function of type `fn(Res[T, E], fn(E) -> F) -> Res[T, F]`.
It is written in twill, it needs no magic, and it is a small demonstration that
the pieces of stage 2 compose rather than each needing its own mechanism.

**`?` on a `Res` in a function returning `Opt`, or the reverse, is an error.**
The conversion is real (`ok_or`, `to_opt`) and is a named function call.

**`?` is systems mode only**, like everything else here.

### Discarding a `Res` is an error

Roadmap entry 1's cost line is one sentence: "Nothing forces a caller to read the
error. That is the entire cost and it is unbounded."

So: **an expression statement whose value has type `Res[T, E]` and is discarded
is an error.** Discard deliberately with `let _ = f(x)`, where `_` is a binding
name that introduces nothing.

Without this rule `Res` is a better-documented version of the empty-string
convention and buys nothing that a comment does not. The rule costs one case in
`infer_stmt`. It is the single highest-value line in this document per line of
implementation.

`Opt` is not must-use. An `Opt` returned and ignored is usually a lookup done
for its side effect of insertion, and the false-positive rate would be high
enough to make people write `let _ =` reflexively, which trains the reflex that
defeats the `Res` rule.

### Migrating off the empty-string convention

Four codebases converged on the same convention independently: a fallible
function returns a `Str` which is empty on success.
`loom/src/checkpoint.tw:118-120` states it:

> Returns an error string, empty on success, following spool's convention: the
> subset has no Res, so a fallible function that also mutates reports through
> its return value.

The signatures, counted: `spool/src/manifest.tw:41` `validate_name`,
`manifest.tw:71` `validate_entry`, `spool/src/commands.tw:59` `lock_satisfies`,
`commands.tw:89` `cmd_install`, `commands.tw:156` `materialise`,
`spool/src/pkghash.tw:68` `verify`, `spool/src/vendor.tw:210` `build_catalog`,
`loom/src/callback.tw:287` `validate`, `callback.tw:619` `unpack_state`,
`loom/src/checkpoint.tw:121` `restore`, `bobbin/src/suite.tw:45` `validate`.

The convention is already ambiguous. `loom/src/checkpoint.tw:106-109` is a
`-> Str` that is not fallible and returns the path it wrote:

```
fn write(r: st.Run, cb_state: Arr[F64], rows: I64, path: Str, note: Str) -> Str {
  save(build(r, cb_state, rows, note), path)
  path
}
```

A reader has to know which of the two meanings a given `-> Str` carries, and
nothing in the signature says.

The caller side, `spool/src/commands.tw:93-118`:

```
fn cmd_install(dir: Str, update: Bool) -> Str {
  let p = open(dir)
  if len(p.err) > 0 { return p.err }
  let l = load_lock(p.root)
  if len(l.err) > 0 { return l.err }
  ...
    let build_err = vendor.build_catalog(p.root, p.manifest, cat)
    if len(build_err) > 0 { return build_err }
    let r = resolve.resolve(p.manifest, cat)
    if len(r.err) > 0 { return r.err }
```

That is the `?` operator, written out, nine times in one function, with the
error type erased. After:

```
fn cmd_install(dir: Str, update: Bool) -> Res[Unit, Str] {
  let p: Project = open(dir)?
  let l: Lock = load_lock(p.root)?
  ...
  vendor.build_catalog(p.root, p.manifest, cat)?
  let r: Resolution = resolve.resolve(p.manifest, cat)?
```

The `err: Str` field disappears with it. spool carries one on six structs
(`commands.tw:16`, `lockfile.tw:36`, `manifest.tw:30`, `resolve.tw:36`,
`toml.tw:38`, `vendor.tw:37`), plus `fn error_manifest(msg: Str) -> Manifest`
at `manifest.tw:33` which returns an all-empty struct with a message in it. Six
struct fields and one constructor become `Res` and are deleted. Nothing else in
this stage removes as much code.

The simple case, for the shape:

```
fn validate(s: Suite) -> Res[Unit, Str] {
  if len(s.cases) == 0 { return Err("suite has no cases") }
  Ok(unit)
}

match validate(s) {
  Ok(_)  => unit,
  Err(e) => report(e),
}
```

That is one line longer than what `bobbin/src/suite.tw:45` writes today, and the
difference is that dropping the call is now an error.

spool's variant is worse and gets more out of the change.
`spool/src/vendor.tw:82` encodes a status flag in the first byte of the returned
string, and `vendor.tw:92-98` decodes it:

```
# status byte: "!" and a message on failure, " " and the output on success.
...
fn git_ok(r: Str) -> Bool { r[0] == s.B_SPACE }
fn git_out(r: Str) -> Str { r[1:len(r)] }
```

That is `Res[Str, Str]` written in one byte of a string, and both helpers are
deleted rather than ported. It is also the workaround with the worst failure
mode in the list: a git command whose output begins with a space is
indistinguishable from a success, and one that begins with `!` is a failure.

The struct-per-call-site workaround also collapses. `loom/src/data.tw` `Batch`
and `loom/src/state.tw` `StepResult` are tuples with a name, and weft has four
of them in a library with eleven real type names. Those that exist only to carry
a value plus an error become `Res`. Those that carry two genuinely different
results keep their struct, because a struct with named fields is the right
answer for two results that are both real.

**Migration order.** `Res` cannot be adopted per call site, because changing a
function's return type changes every caller at once. It can be adopted per
function, leaves first. For spool: `toml.unquote`, then `manifest.remove_dep`,
then `vendor.commit_for`, then `vendor.git`, then the command layer. Each step
is a compiling program. The compiler names the call sites, which is the property
the change is being made to get.

### Sentinels

`Opt[T]` replaces the sentinel. A `-1` returned for "not found" becomes
`Opt[I64]`: `spool/src/strutil.tw:75` `index_of`, `warp/src/pipeline.tw:194`
`index_of_kind`, and `bobbin/src/clock.tw:133`, which returns
`ClockInfo { overhead_ns: -1, granularity_ns: -1 }` to mean "not measured" in a
field whose other values are durations.

An `ok: Bool` beside the value becomes `Opt[T]` or `Res[T, E]`.
`spool/src/semver.tw:14-22` says why it is a sentinel today:

```
struct Version {
  major: I64, minor: I64, patch: I64,
  # ok is false when parsing failed. The subset has no Opt or Res, so a
  # sentinel field is the only way to report failure without aborting; see
  # docs/needs.md.
  ok: Bool,
}
```

`warp/src/strutil.tw:28-45` has the same pair (`ParsedI64`, `ParsedF64`) and
then the line the must-use rule exists for:

```
fn parse_i64(s: Str) -> I64 = parse_i64_checked(s).value
```

That alias discards the flag at every call site in warp, silently, and
`parse_f64` at `strutil.tw:94` does it again. Nothing today reports it. Under
section 4, `parse_i64` returns `Res[I64, Str]` and the alias does not compile.

The strongest single argument for `Opt` over a sentinel is
`loom/src/callback.tw:130-136`, which explains why no sentinel was available:

```
  # False until the first observation, because there is no sentinel that is
  # safely worse than every possible metric value: a loss can be negative and
  # an accuracy cannot exceed one, so a shared "worst" constant is wrong for
  # one of them.
  seen: Bool,
```

`best: Opt[F64]` is that comment, deleted. `weft/src/live.tw:56-58` has the
unrepaired version of the same problem: `last_value` and `best` start at `0.0`
and are patched by a `steps == 1` special case at `live.tw:88`.

`src/check.tw` is already written this way and is the reference: `env_get`
returns `Opt[Type]`, `dict_get` returns `Opt[V]`, `unit_sqrt` returns a struct
carrying an `ok` because it has two real results.

---

## 5. Containers

### `Arr[T]` for any `T`

`T` may be any type the subset has, including another container, a struct, an
enum, a function type, and the type currently being declared. That is NEEDS-72
and NEEDS-90. Nothing exotic is asked for: no variance, no covariant
assignment, no element-type subsumption.

```
Arr[Arr[I64]]      # src/tensor.tw Odometer.contrib, einsum_plan
Arr[Tensor]        # src/tensor.tw concat, split, backward
Arr[Bool]          # src/tensor.tw resolve_perm
Arr[Stage]         # warp/src/pipeline.tw
Arr[fn(I64) -> F64]
```

`src/tensor.tw` is the sharpest case. Without nesting, five kernels need a
hand-rolled flattening each, which is five chances to get an index wrong in the
code that computes gradients.

### `Dict[K, V]`

`V` generalises exactly as `T` does. `K` does not.

**`K` stays `Str` or `I64`.** The argument is not that generalising is hard, it
is that it is underspecified. A `Dict` needs a hash and an equality for its key.
twill's `==` is deep structural (`docs/language-guide.md`, Equality), so
equality exists for every type, but it has two properties that make it a bad
hash key. Functions compare by identity, so a key containing a function value
hashes to a value that changes between two textually identical programs. Numbers
compare by IEEE rules, so a key containing a `NaN` is not equal to itself and can
never be looked up again. `Str` and `I64` have an obvious, stable, portable hash
and no such case, and they are the two a compiler needs: symbol tables, string
interning, keyword recognition, node ids.

Entry 31, `Dict` keyed by something else, has one caller and is in stage 5. The
shape it should take if it lands: not a general `Hash` constraint, which is a
trait, but an explicit constructor taking the two functions.

```
fn dict_with[K, V](hash: fn(K) -> I64, eq: fn(K, K) -> Bool) -> Dict[K, V]
```

That is expressible only because section 2 exists, which is one more reason the
four entries are one stage.

### What the containers fix, concretely

spool's `Catalog` is `Dict[Str, Str]` where the values are comma-separated
version lists and whole rendered manifests, re-parsed on every read. It becomes
`Dict[Str, Arr[Version]]` and `Dict[Str, Manifest]`, and a lookup stops being a
parse.

loom's `MeterSet` is two parallel `Dict[Str, F64]` plus an `Arr[Str]` of names.
It becomes one `Dict[Str, Meter]`. The parallel dicts can currently go out of
step and only a convention keeps them together.

loom's `predict` accumulates batches. Without `Arr[Tensor]` it concatenates as
it goes, which is quadratic in the number of batches and allocates the whole
output once per batch. With it, one `concat` at the end.

---

## 6. Checking, inference, and what monomorphisation actually means

### The lattice

`src/check.tw`'s `enum Type` gains cases and loses none:

```
TArr(ArrType)         # elem: Type
TDict(DictType)       # key: Type, val: Type
TEnum(EnumType)       # name: Str, args: Arr[Type]
TStruct(StructType)   # name: Str, args: Arr[Type]
TFnType(FnSig)        # params: Arr[Type], ret: Type
TVar(Str)             # a bound type parameter, inside a signature only
TNever                # see below
```

`TFn` (the existing case, holding a `FnType` with an AST body and a captured
`CheckEnv`) stays and is not the same thing as `TFnType`. `TFn` is what the
checker knows about a specific function it can see the body of, and it is what
lets `infer_user_call` check the body at the call site with concrete argument
shapes. `TFnType` is a declared signature with no body behind it. A `TFn`
matches a `TFnType` when its signature does. Merging them would mean losing the
body, and the body is where numeric mode gets most of its shape information.

### `Never`

NEEDS-73 asks for `abort` to be usable in value position. `src/tensor.tw`
`apply_binary` and `apply_unary` both end in an arm that calls `abort` because
the op was not of the kind the function handles, and that arm has to have the
same type as the others.

`abort(msg: Str) -> Never`. `Never` matches every type and is the identity of
`join`. It is one lattice element and one rule. The alternative is returning a
sentinel float and letting a wrong op compute with it, which is the exact class
of failure this stage exists to remove.

`Never` is not writable in an annotation. It is the type of `abort` and of an
arm that always returns, and nothing else.

### Inference, stated as a closed list

Systems mode infers exactly three things.

1. **A `let`'s type from its initialiser.** `let n = len(xs)` is `I64`. The
   initialiser is ground, so this is a read, not an inference.
2. **A call's type parameters from its ground arguments**, by the `unify` of
   section 1, plus the return by `substitute`.
3. **A lambda literal's parameter and result types from the declared function
   type of the argument slot it sits in**, by the single exception in section
   2.

That is all. In particular:

- No inference of a binding's type from a later use.
- No inference of a function's parameter or return types from its body. A
  systems-mode function annotates its parameters and its return, always. This is
  not a limitation, it is `docs/self-hosting.md` section 1.3: every parameter and
  return has a type that is either annotated or inferred from an annotated
  source.
- No inference of a type argument from the expected type at the use site. `let
  xs: Arr[Str] = arr_new()` needs `arr_new` to be given its parameter, and the
  rule is that a generic call with no argument to unify against writes it:
  `arr_new[Str]()`. That is ugly and it is predictable, and predictable is the
  property being bought. An annotation-directed alternative would be a fourth
  inference rule with a scope that is hard to state.

### Monomorphisation, and why it is cheaper than it sounds

`docs/self-hosting.md` says "monomorphized by the checker" and flags generics as
the item it is least confident in. The flag is warranted for the wrong reason, so
it is worth being exact.

**Monomorphisation here is a checker-only device. It does not duplicate code.**
The evaluator is dynamically typed: `src/eval.tw` dispatches on `Value`, and an
`Arr` holding `Str` and an `Arr` holding `I64` are the same runtime object with
the same operations. So instantiating `Arr[Json]` produces a type identity for
diagnostics and for field typing, and produces no second copy of anything.

That changes two of the three things the section was nervous about.

**Recursive instantiation terminates by memo.** `Json` contains `Arr[Json]`
contains `Json`. The worklist keys instantiations by their rendered type name and
skips one already present. Since instantiation allocates a type record and not a
function body, the memo table is the entire cost and it is bounded by the number
of distinct type expressions written in the program. NEEDS-90 predicted exactly
this and it is the whole answer.

**Error messages naming a synthesized type** are the remaining real cost. The
rendering has to be the source spelling (`Dict[Str, Arr[Version]]`) rather than
an internal id, and it has to be stable, which means the same discipline
`unit_string` already follows at `src/check.tw:224`: a deterministic rendering,
because two routes to the same type must produce one message and not two.

**Code size and compile time** are not costs, because nothing is duplicated.
That is the estimate correction.

### Exhaustiveness, algorithmically

Collect the scrutinee enum's variant names. Walk the arms. For a variant
pattern, remove that name and report a duplicate if it was already removed. For a
nested pattern `Ok(Some(v))`, the outer name is what is removed, and the inner
pattern is checked against the payload's enum for its own exhaustiveness only
when the outer variant appears more than once. For `_`, stop and check the
remainder is non-empty. At the end, a non-empty remainder is the error, listing
names.

Literal patterns on `I64` and `Str` never exhaust, so a `match` on those requires
`_` and the missing-`_` message says so.

---

## 7. Units of measure

twill has units and almost no other language does, so the interaction is worth
settling rather than discovering.

### The measurement first

No unit annotation appears in any `.tw` file in spool, loom, bobbin, weft or
warp, and none of their five needs files mentions units at all. Units have zero
ecosystem callers. Every other feature in this document has four or more.

That is the number this section is ranked by, and it is why the answers below
are conservative. It is not evidence that units are unwanted. It is evidence
that units are a numeric-mode feature and these are five systems-mode
codebases, which is exactly what `docs/self-hosting.md` section 1.3 predicts.

It is worth recording what they do instead, because it is a measurement of the
same kind as the rest of the roadmap. Units are carried in identifier suffixes
and comments. `bobbin/src/baseline.tw:91-93` has `median_ns: F64`, `iqr_ns:
F64`, `min_ns: I64`. `bobbin/src/clock.tw:109-113` documents `overhead_ns` and
`granularity_ns` in prose. `bobbin/src/harness.tw:63-74` writes raw nanosecond
magnitudes as bare literals (`min_total_ns: 1000000000`, then
`max_total_ns: 30000000000`, then `o.min_total_ns = 100000000`), where a
mistyped zero is invisible. And `weft/src/live.tw:86` takes `now_ms: I64` in an
otherwise nanosecond-flavoured ecosystem with nothing preventing an `_ns` value
being passed to it.

Those are the bugs a unit system prevents, in a mode where the unit system is
switched off. That is a finding for stage 5 and entry 17, not for this
document, and it is left here rather than acted on.

### The default answer, and why it is not the whole answer

Units are numeric mode. Generics are systems mode. `docs/self-hosting.md`
section 1.3 makes declaring `unit` in a systems-mode file an error rather than a
no-op, on the grounds that a silent no-op is how a user discovers six months
later that their annotations meant nothing. So today they do not meet.

That is the default answer and it is correct today. It is not sufficient,
because it leaves three questions that get answered by accident otherwise.

### 1. A unit is a kind, and unit variables are not offered

Section 1 gives the binder two kinds, `BDim` and `BType`. A unit is a third:
`src/check.tw` carries a unit as a `Dict[Str, I64]` from base name to exponent,
which is neither a dimension nor a type.

`BUnit` is deliberately not added. Generic-over-unit would look like:

```
fn twice[u](x: u) -> u = x * 2.0        # the useless half
fn area[u, v](a: u, b: v) -> u*v        # the useful half
```

The second is what anyone would actually want, and it requires the binder to
hold unit *expressions* and to do arithmetic on exponents at substitution time.
`unit_pow` at `src/check.tw:195` already requires a constant integer exponent
precisely so that `USD^x` is refused; a unit variable in an exponent position
reopens that as a small dependent system. Offering only the first form is a
trap, because it is the form nobody needs and it looks like the feature.

The escape already exists and is better. **Units get polymorphism from the
absence of an annotation.** `fn double(x) = x * 2.0` is checked by
`infer_user_call`, which carries the body's unit through to the caller
(`src/check.tw:1440` and the comment there about not erasing a unit a shape
return says nothing about). An unannotated numeric function is already unit
polymorphic, by inference, with no syntax. Adding unit variables would be a
second and worse route to a thing that works.

### 2. A type variable never binds a unit

`fn id[T](x: T) -> T` applied to a `USD` quantity binds `T` to the *type*
`TTensor` with dims `[]`, and the unit rides along inside `TensorType.unit`
rather than being what `T` stands for. That falls out of units living on
`TensorType` rather than in `enum Type`, so the representation gives the right
answer. It is stated because the wrong answer, `T` binding a unit-carrying type
and then unifying `USD` against `share` as a type mismatch, would produce a
diagnostic in the wrong family.

Concretely: a generic function is unit-transparent. It neither checks nor
destroys a unit; the unit is part of the value's type and travels with it.

### 3. The seam erases units

When roadmap entry 17 lands and a tensor crosses into systems mode, the tensor's
unit does not cross with it. Units are already erased at runtime
(`docs/language-guide.md`: "units are erased at runtime, so annotated code runs
as plain numbers with zero overhead"), and systems mode has no `unit`
declaration to name them against. So a `Arr[Tensor]` in systems mode holds
tensors whose units were checked where they were written and are dimensionless
thereafter.

This is a real loss and the alternative is worse: carrying units into a mode
where they cannot be declared means either importing unit declarations across
the mode boundary, or a unit that exists but cannot be named in a diagnostic.
Recorded here as an open question for stage 5, with erasure as the
recommendation and not the decision.

---

## 8. What this design does not add

Each of these is a thing a reader will expect and not find. The reason is given
because the reason is what stops it being added later by reflex.

**No higher-kinded types.** `Arr` cannot be passed as a parameter `F[_]`. The
consequence is that there is no one `map` over an arbitrary container; there is
`arr_map` and there is `dict_map`. That is two functions and a naming
convention, against a feature that changes what a type parameter is and that
nothing in six codebases asks for.

**No typeclasses, traits, or interfaces.** This is the largest omission and it
is argued rather than assumed, because three separate places in this document
would be shorter with them.

- `?` would convert error types via a `From` relation. Instead the error types
  must match and `map_err` is an explicit call. The call is one line and it is
  at the site where the conversion happens, which reads better than a conversion
  chosen by a resolution rule elsewhere.
- A generic `sort` would take an `Ord` constraint. Instead it takes a comparator
  argument. Roadmap entry 5 asked for a comparator parameter, not for an
  ordering constraint, and five codebases wrote it that way. The explicit
  argument is what the callers already want.
- A generic `Dict` key would take a `Hash` constraint. Instead the key type is
  fixed at `Str` or `I64`, and the general form, if it ever lands, takes the two
  functions explicitly (section 5).

The pattern is that every place a constraint would be needed, an explicit
function argument does the job, is visible at the call site, and needs no
resolution rule, no coherence rule, no orphan rule, and no answer to what happens
when two instances apply. `docs/self-hosting.md` also has a standing decision
against methods for the same reason, and traits without methods would be a
strange shape.

The cost is stated: a function generic in `T` can do nothing with a `T` except
move it. It cannot compare it, print it, or hash it. Every such operation is a
function parameter. That is a real restriction and it is the price of the
resolution machinery not existing.

**No subtyping and no variance.** `Arr[T]` is invariant. There is no top type,
no bottom type other than `Never`, and no relation between two struct types that
share fields. A struct is nominal (`docs/self-hosting.md` section 1.2), so
structural width subtyping is not even expressible.

**No inference beyond section 6's closed list of three.** Named again here
because it is the rule most likely to be relaxed one exception at a time.

**No implicit conversions of any kind.** `docs/self-hosting.md` already says
this for `I64` and `F64` and it extends: no `T` to `Opt[T]`, no `T` to
`Res[T, E]`, no auto-deref of an enum payload.

**No `_` as a type in systems mode.** The numeric-mode `_` in a shape annotation
stays. A `_` standing for an inferred type would be exactly the `TUnknown` that
section 1.3 says must not survive.

**No associated types, no where clauses, no const generics, no default type
parameters, no variadic generics.** Listed as one item because they all follow
from the three above and none has a caller.

**No pattern guards, no or-patterns, no deep nesting in patterns.** Section 3.

**No effect on numeric mode.** Nothing in this document is visible in a file
without `mode systems`. The 279 tests do not see any of it.

---

## 9. Landing order inside stage 2

The roadmap says there is no useful order inside this stage and that splitting it
produces half-features. That is right about the shipped result and wrong about
the work. Below is what can land independently, what must land together, and what
each piece needs from where.

The no-Go rule frames the second column. The Go bootstrap under `internal/` is
forbidden to change. Every piece below is therefore work in `src/`, and the
useful question is which files.

**The good news, stated first: stage 2 needs no new native primitive.** Enums,
patterns, closures, generics and monomorphisation are all front-end and
interpreter work. Nothing here needs a capability the eventual runtime does not
already owe for stage 0. That is not true of stage 3, where every entry is a
primitive.

| # | Piece | Files | Depends on | Lands with |
|---|---|---|---|---|
| 2a | Function types as a type | `src/lex.tw`, `src/parse.tw`, `src/ast.tw`, `src/check.tw`, `src/fmt.tw` | nothing | alone |
| 2b | `enum`, `match`, exhaustiveness, monomorphic | same five | nothing | alone |
| 2c | The unified binder: `Binding`, `unify`, `substitute`, kinds | `src/parse.tw`, `src/check.tw` | 2b to be useful | 2d, 2e |
| 2d | `Opt`, `Res`, `?`, must-use | `src/check.tw`, `src/eval.tw`, prelude | 2b, 2c | 2c, 2e |
| 2e | Generic containers, recursive instantiation, memo | `src/check.tw` | 2c | 2c, 2d |
| 2f | Generic structs | `src/check.tw` | 2c | alone, last |
| 2g | The lambda-literal exception | `src/check.tw` | 2a | alone |

**2a lands alone and first.** Function types have no dependency on generics or
enums. Landing them first makes entry 5's comparator writable, which deletes
eleven hand-written insertion sorts, and it unblocks bobbin's `Case.body` and
warp's `Stage.fn_map`, which are the two libraries the roadmap says have no
workaround at all rather than an expensive one. It is also the smallest of the
pieces and the one whose checker change is most contained: a new lattice case and
a structural match. It is also the piece with code already written against it,
in warp and bobbin, which means it has a test corpus on the day it lands.

**2b lands alone and second.** Monomorphic enums with exhaustive `match` need
nothing from 2c. All six of the discriminants in the roadmap's list are
payload-free or single-payload monomorphic enums: bobbin's six `VERDICT_*`,
loom's five `KIND_*`, five `SCHED_*`, three `OPT_*` and seven `HOOK_*`, spool's
three `CONSTRAINT_*`, warp's six stage kinds, weft's three `RES_*` and two
`MARK_*`, and twill's own token kinds. That is nine constant blocks read by
roughly twenty if-chains, several of them in a different file from the
declaration and one of them in a different module. Every one is fixed by 2b with
no generics involved. This is the piece that converts the roadmap's "silently
wrong" list into compile errors, and it can be had before the hard part.

**2c, 2d and 2e are one landing.** They cannot be separated and the reason is
mechanical rather than aesthetic. `Opt[T]` is a generic enum, so 2d needs 2c.
`dict_get` returns `Opt[V]`, so 2e needs 2d. `Arr[T]` with an arbitrary `T`
needs the binder, so 2e needs 2c. Any two of the three without the third leaves
`src/check.tw` unable to compile itself, since it uses all three on nearly every
line.

**2f lands last and may not land.** No codebase in the ecosystem declares a
generic struct today. `Span[T]` and `Range[T]` in weft are the closest and both
are `F64`. It is in the design because `docs/self-hosting.md` promised type
parameters on structs, and it is last because promising it and deferring it costs
nothing.

**2g lands after 2a and should not be bundled with it.** It is the one inference
exception and it deserves its own review.

### A contradiction this exercise found

Roadmap stage 0 lists `Dict[Str, V]` as milestone 1, before stage 1 and stage 2.
`docs/self-hosting.md` section 1.2 specifies `Dict` lookup as returning `Opt[V]`.
`src/check.tw` is written against that: `env_get` matches `dict_get(env.vars,
name)` with `Some`/`None` on its first use at line 303.

So milestone 1's `Dict`, as specified and as already consumed by `src/`, needs
`Opt`, which needs enums and generics, which are stage 2. **Stage 0 cannot be
completed as written before stage 2.** The resolutions are: give `Dict` a
`dict_get_or(d, k, default)` and a `dict_has` for the stage 0 interval and add
`dict_get` in 2d, or accept that milestone 1 finishes at the end of 2d. The
second is more honest and the first is what unblocks work in the meantime. This
is flagged rather than decided, because it is a scheduling call and not a design
one.

---

## 10. Worked examples

Each is a real pattern from the ecosystem. The before is what the code does
today, per `docs/roadmap.md`, which checked these against the source rather than
against the needs files.

### 10.1 A discriminant with dispatch

`bobbin/src/baseline.tw:60-65` declares six `VERDICT_*` constants as `I64`:

```
let VERDICT_PASS = 0
let VERDICT_REGRESSED = 1
let VERDICT_IMPROVED = 2
let VERDICT_INCONCLUSIVE = 3
let VERDICT_NEW = 4
let VERDICT_MISSING = 5
```

`baseline.tw:161` stores one as `verdict: I64`. Three separate if-chains read
it: `baseline.tw:67-82`, `report.tw:90-97`, and `report.tw:205-215`. Adding a
seventh compiles and does nothing in all three, and the three fall through to
different defaults.

After:

```
enum Verdict { Pass, Regressed, Improved, Inconclusive, New, Missing }

fn verdict_name(v: Verdict) -> Str {
  match v {
    Pass         => "pass",
    Regressed    => "regressed",
    Improved     => "improved",
    Inconclusive => "inconclusive",
    New          => "new",
    Missing      => "missing",
  }
}
```

Adding `Flaky` now fails to compile in all three places, by name. That is the
entire value of entry 3 and it arrives with piece 2b alone, with no generics.

The same shape appears five more times. `spool/src/semver.tw:24-33` carries
`kind: I64` with a comment giving the encoding, and `matches` at
`semver.tw:112-118` dispatches with caret as the unguarded fall-through, so
there is no arm to add a case to. `warp/src/pipeline.tw:57-71` has six stage
kinds read by chains in `pipeline.tw:280`, `pipeline.tw:390`, and across the
module boundary in `warp/src/cache.tw:105-107`. `weft/src/canvas.tw:22-24` has
three resolutions read by four separate chains, two of them one-liners
(`canvas.tw:65-66`). None of these is a hard case. All of them are 2b.

### 10.2 A discriminant with fields that only some variants read

`loom/src/callback.tw:101-112` has five `KIND_*` and five `SCHED_*` constants on
a flat `Callback` struct. The struct carries the union of every callback's
fields, so a checkpoint callback has `patience` and `min_delta` and ignores
them, and nothing stops a caller setting them and expecting an effect. The
file's own header at `callback.tw:69-75` says this:

> One flat struct with a `kind` discriminant and the union of every callback's
> fields. This is not the design anyone wants. With sum types it would be an
> enum with a payload per variant and `fire` would be a match the checker proves
> exhaustive.

`loom/src/state.tw:98-119` is the same admission in three lines:

```
struct OptState {
  # 0 sgd, 1 momentum, 2 adam. There are no sum types in the subset, so the
  # discriminant is an I64 and the unused fields are ignored rather than
  # absent. See docs/needs.md entry 3.
  kind: I64,
  m: Tree, v: Tree, t: I64, b1: F64, b2: F64, eps: F64, mu: F64,
}
```

`trainer.tw:193-204` dispatches on it inside the batch loop, with adam as the
unguarded fall-through. Seven fields, three variants, and every variant reads
some and ignores the rest.

After, the payload goes with the variant:

```
struct EarlyStopCfg { patience: I64, min_delta: F64 }
struct CheckpointCfg { dir: Str, every: I64 }

enum CallbackKind {
  EarlyStop(EarlyStopCfg),
  Checkpoint(CheckpointCfg),
  LogMetrics,
  LrSchedule(Schedule),
  Custom(fn(TrainState) -> Unit),
}
```

Three things happen at once. The fields a variant does not have become
unwritable rather than merely ignored. The `Custom` arm becomes expressible,
which it is not today. And `fire` at `callback.tw:397-417`, which is a
hand-written vtable called once per hook per callback inside the training loop,
becomes a `match` the checker proves complete.

The `Custom` arm needs 2a. This is the case where the two pieces of the stage
pay each other back: a callback framework extended by people who did not write
it is exactly where a `fn` field and an exhaustive `match` are worth more
together than apart.

The same file has the flattening that 2e removes. `callback.tw:594-616` packs
callback state into an `Arr[F64]` at a fixed stride of five, coercing an `I64`
tag, two integers, a float and a bool into one float array:

```
let PACK_STRIDE = 5
...
    push(out, f64(c.kind))
    push(out, f64(c.wait))
    push(out, c.best)
    push(out, f64(c.best_epoch))
    if c.seen { push(out, 1.0) } else { push(out, 0.0) }
```

and `unpack_state` reads the bool back as `packed[base + 4] > 0.5`. With
`Arr[CallbackState]` there is nothing to pack, no stride to keep in step
between two functions, and no float comparison standing in for a bool.

### 10.3 A comparator, and eleven sorts

Eleven hand-written insertion sorts exist in the ecosystem, two of them in this
repository under the same name (`src/check.tw:254` and `src/fmt.tw:149`).
`src/check.tw:251` says why it wrote one:

```
# Insertion sort, bytewise ascending, to match sort.Strings. The lists are two
# or three elements: a comparator-taking `sort` builtin would be a larger
# language question than this needs. See NEEDS-23.
```

After 2a, the language question is answered and the builtin has a type:

```
fn sort_by[T](xs: Arr[T], less: fn(T, T) -> Bool) -> Unit
```

and with 2g the call site is what it should be:

```
sort_by(names, fn(a, b) = str_less(a, b))
```

Three of the eleven differ only in element type: `spool/src/strutil.tw:301`
`sort_strs(xs: Arr[Str])`, `weft/src/bars.tw:184` `sorted_copy(xs: Arr[F64])`,
and `bobbin/src/stats.tw:37` `sorted(xs: Arr[I64])`, whose own comment at
`stats.tw:41` names the replacement. Those three collapse into one call.

spool's constraint is stronger than convenience. `spool/src/strutil.tw:269`
records that a lockfile is reproducible only if all four of its sorts order
identically. One `sort_by` and one comparator is one place for that to be true,
instead of four.

The same duplication shows up below sorting, where the functions are three
lines. `weft/src/fmtnum.tw:152-155` declares `max_f`, `min_f`, `max_i`, `min_i`,
and `weft/src/canvas.tw:213` and `warp/src/augment.tw:266` declare them again
verbatim. `min_of` and `max_of` over `Arr[F64]` exist three times identically in
weft alone (`bars.tw:207`, `live.tw:202`, `svg.tw:309`).
`warp/src/cache.tw:254-296` has `join_f64` beside `join_i64` and `parse_f64s`
beside `parse_i64s`, four functions where two generic ones do.

These are the cases where a generic without a comparison constraint is not
enough. `fn max[T](a: T, b: T) -> T` cannot compare two `T`s, because section 8
refuses the constraint that would let it. The honest answer is that these
collapse to `fn max_by[T](a: T, b: T, less: fn(T, T) -> Bool) -> T`, which at a
two-argument call site is longer than the three-line function it replaces, so
they do not collapse and should not. Eight duplicated one-liners survive this
stage. That is the price of no traits, priced.

### 10.4 A pipeline stage

`warp/src/pipeline.tw:76-77` already declares `fn_map: Fn(smp.Sample) ->
smp.Sample` and `fn_keep: Fn(smp.Sample) -> Bool`, and `pipeline.tw:87`
declares `get: Fn(I64) -> smp.Sample`. `bobbin/src/suite.tw:20` declares
`body: fn(I64) -> F64`. These four are the only function-typed values in the
ecosystem and neither repository compiles today. Two of five codebases have
already written the code they want and are waiting for it to become legal, which
is stronger evidence than a needs entry.

What warp runs on in the meantime is the workaround from 10.1: a six-value
`kind: I64` at `pipeline.tw:57-71`, with the transform selected by an op code
inside the loop at `pipeline.tw:280` and `pipeline.tw:390`. That is a fixed set
of built-in transforms with no way to add one, which is the library warp exists
not to be. loom has the same shape at `trainer.tw:193`, where `opt.kind` selects
the update rule inside the batch loop.

After:

```
struct Stage {
  name: Str,
  map: fn(Row) -> Row,
  keep: fn(Row) -> Bool,
}

struct Source {
  name: Str,
  get: fn(I64) -> Opt[Row],
}
```

`Source.get` closing over the buffer works because capture is by handle, which
section 2 restates and `src/eval.tw:43` already decided. The `Opt[Row]` return
is 2d: today the end of a source is a sentinel row or an empty string.

### 10.5 A catalog that re-parses

`spool/src/resolve.tw:15-26` is the clearest case in the ecosystem and names
itself as such:

```
# `versions` maps a git URL to a comma-separated list of published versions, and
# `deps` maps "url@version" to the rendered [dependencies] table of that
# package's own spool.toml. Both are strings because the subset has no nested
# containers beyond Dict[Str, V]; see docs/needs.md, this type is the clearest
# evidence for wanting Dict values that are not scalars.
struct Catalog {
  versions: Dict[Str, Str],
  deps: Dict[Str, Str],
}
```

There are three encodings stacked here. The value is a comma-separated list,
decoded by `split_byte` at `resolve.tw:53`. The key is a composite,
`fn catalog_key(git, version) -> Str { git + "@" + version }` at
`resolve.tw:43`. And `resolve.tw:91` joins all constraints on a name with `"|"`.
Every read is a parse and every write is a render.

After 2e:

```
struct Catalog {
  versions: Dict[Str, Arr[Version]],
  deps: Dict[Str, Dict[Str, Constraint]],
}
```

The composite key survives as a nested `Dict` rather than a string, which also
removes the failure mode that a git URL containing `@` corrupts the key.

`loom/src/metrics.tw:61-67` is the parallel-array version of the same want:

```
struct MeterSet {
  totals: Dict[Str, F64],
  weights: Dict[Str, F64],
  names: Arr[Str],
}
```

It becomes `Dict[Str, Meter]` plus the `names` array, which stays. The comment
above it says `names` exists because report order must be stable and the source
should not depend on the dict's insertion order. That is a separate concern from
nesting and 2e does not remove it. `update_batch` at `metrics.tw:120`, which
takes an `Arr[Str]` and an `Arr[F64]` that must stay index-aligned, does become
one `Arr[Meter]`.

### 10.6 An environment lookup

`src/check.tw:298`, already written against this design:

```
fn env_get(e: CheckEnv, name: Str) -> Opt[Type] {
  ...
  match dict_get(env.vars, name) {
    Some(t) => return Some(t),
    None => cur = env.parent,
  }
  ...
}
```

This is the case NEEDS-22 makes: Go's `v, ok := m[k]` is two returns and twill
has one. It is also the reason section 9 flags the stage 0 contradiction. Every
environment lookup in the checker is already written in a language that does not
exist yet, which is a good sign about the design and a bad sign about the
schedule.

---

## 11. What is unresolved

Listed rather than smoothed over.

**The `_` loophole in `match`.** Section 3. `_` is necessary for projections and
is a waiver for dispatch, and no rule distinguishes them. The design ships with
a review rule and an admission.

**Whether `Opt` should be must-use too.** Section 4 says no, on a
false-positive argument that is a judgement rather than a measurement, and there
is already one measurement against it. `warp/src/strutil.tw:45` is
`fn parse_i64(s: Str) -> I64 = parse_i64_checked(s).value`, which drops an
`ok: Bool` at every call site in the repository, twice over
(`parse_f64` at `strutil.tw:94` does the same). That is an `Opt`-shaped value
dropped silently, in production, in the exact way the must-use rule exists to
catch, and the rule as written would not catch it.

The counter-argument still stands: warp would satisfy a must-use `Opt` by
writing `let _ =` at the same line, and no rule stops that. What the measurement
actually argues for is that `parse_i64` should return `Res[I64, Str]` rather
than `Opt[I64]`, because a parse failure has a reason worth carrying, which
`warp/docs/needs.md` entry 6 asks for directly. That is a naming decision about
one function and not a rule about `Opt`. Left as written and flagged.

**The unit seam.** Section 7.3 recommends erasure across the systems and numeric
boundary and does not decide it, because entry 17 is in stage 5 and the decision
belongs with it.

**Stage 0's `Dict` needing stage 2's `Opt`.** Section 9. A scheduling call.

**Whether the bare-variant collision rule is right.** Section 3 permits `V`
unqualified when unique and `E.V` always. That is a compromise between two
existing sources rather than a first-principles answer, and the failure mode of
the compromise is that adding an enum to a module can turn a working bare name
into an error somewhere else in that module. The error is clear and it is at
compile time, so this is a nuisance rather than a hazard, but it is a nuisance
that a qualified-only rule would not have.

**The size of the checker change.** `docs/self-hosting.md` flags generics as the
item it is least confident estimating. Section 6 argues the estimate down on
monomorphisation specifically, because nothing is duplicated. It does not argue
down the rest, and the rest is a new lattice with seven cases, a pattern
grammar, an exhaustiveness pass, and a kinded binder, against a checker that is
2,287 lines today.
