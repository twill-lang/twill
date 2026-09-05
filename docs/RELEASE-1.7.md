# Twill 1.7.0 -- the two open questions

1.5 made the ecosystem run. 1.6 stopped the language having pieces missing from
the middle of it. 1.7 closes the two entries `docs/needs.md` had been calling
the largest open language questions in the file, and closes them on both
implementations rather than on the Go bootstrap alone.

Nothing written before this release changes meaning. Both features are additions
at positions that were previously syntax errors, and the one rule that could
have reinterpreted existing code -- a lower-case name in a pattern now binds
instead of naming a case -- cannot, because every enum variant in the language
and its libraries is upper-case initial, and a lower-case one was a case no enum
declared, which the checker refused to judge.

## What the release is

**A pattern is a tree.** It used to be a case name and at most one binder.
Three things can now be written that could not be:

```rust
match result {
  Ok(Some(0)) => "present but empty",      # nested
  Ok(Some(v)) if v > 100 => "large",       # guarded
  Ok(Some(v)) => "present",
  Ok(None) => "absent",
  Err(msg) => msg,
}

match n {
  0 => "zero",                             # literal, on a value that is no enum
  1 => "one",
  x if x > 100 => "big",
  other => str(other),                     # a named catch-all
}
```

A case name starts with a capital letter and anything else binds. That is what
tells `Some(x)`, where x is a binder, from `Ok(None)`, where None is a case, and
it is what makes a named catch-all possible. A lower-case name applied to a
payload is refused by name rather than misread: `some(v)` says that a case name
starts with a capital letter.

**Exhaustiveness got more precise, not merely still true.** It recurses:
`Some(Ok(v))`, `Some(Err(e))` and `None` together cover an `Opt[Res[..]]`, and
dropping the `Err` arm names the value that gets through.

```
load.tw:14: match on Opt is not exhaustive: missing Some(Err)
```

The rule underneath is that an arm counts only when nothing but the value's
shape decides whether it runs. A guard is a condition the checker cannot
evaluate and a narrower nested pattern leaves the rest of its case unmatched, so
neither proves a case handled -- `Some(v) if v > 3` together with `None` is not
exhaustive, and saying so is the point of the feature rather than a limitation
of it. A position holding only literals is left unjudged, since no finite set of
them covers a number or a string; `Bool` is the one exception, because `true`
and `false` do exhaust it.

**A declaration can be written in terms of a type it does not name.**

```rust
mode systems

struct Box[T] { value: T, tag: Str }
enum Tree[T] { Leaf(T), Branch(Arr[T]), Empty }
fn first[T](xs: Arr[T]) -> T = xs[0]
```

(`mode systems` added 2026-09-04. Without it the last line answers
`unknown unit "T"`: in numeric mode a bare name in return position is read as a
unit. `docs/BUGS.md`, Open.)

`Arr`, `Dict`, `Opt` and `Res` have been generic and checked since 1.5. A
declaration in a twill program could not be: `[` after the name was a syntax
error. That was NEEDS-4, and it had been the largest open entry in the file.

**There is no monomorphization, and none is needed.** That is the part of the
original plan that turned out to be wrong, and it is worth writing down. The
runtime is a tree walker over dynamically typed values: the same code runs
whatever `T` is, so specialising per instantiation would produce identical
copies. The parameters have to reach exactly one place -- the types the checker
judges against -- so generics here are substitution and nothing else, about
eighty lines in each implementation. NEEDS-90's termination worry existed
because monomorphization was assumed, and therefore does not arise.

What that buys is that the checker knows what is in the box:

```rust
let b: Box[I64] = Box { value: 3, tag: "count" }
let s: Str = b.value      # "s" is declared Str but the value is I64
```

Substitution goes as deep as the type does, so a `Branch(xs)` arm on a
`Tree[I64]` binds `xs` as an `Arr[I64]`. Two uses of one declaration are
different types when their arguments differ, so a `Box[Str]` is refused where a
`Box[I64]` was declared, while a non-generic `struct` still compares by name
alone as it always has. Arguments are read out of the value rather than written
at the use site, because there is nowhere to write them: `Leaf(n)` with an `I64`
n is a `Tree[I64]`, and in `fn swap[A, B](p: Pair[A, B]) -> Pair[B, A]` the
literal `Pair { first: p.second, second: p.first }` is a `Pair[B, A]` by what it
is built from. A parameter left unbound judges nothing, so a bare `Box` says
exactly what it said before.

## The bug this release found in itself

**`twill fmt` would have deleted every type parameter.** A printer with no case
for `[T, U]` does not fail; it silently writes `struct Box { value: T }` -- a
program that no longer parses -- over the original under `--write`.

This is the third formatter gap found the same way: `unit USD` in 1.6, the
parentheses a postfix expression needs in 1.6.2, and now this. The pattern is
consistent enough to state as a rule: **adding syntax means adding a printer
case, and a printer with no case for a node deletes it rather than failing.**
Both printers have the case now, and a round-trip test asserts that the type
parameters and the new pattern forms both survive formatting.

## Parity

> **Note added 2026-09-04.** Read this heading as scoped to the two features
> 1.7.0 shipped, which is what the three bullets below actually claim, and not
> as a statement about the two implementations in general. Measured since:
> the front end agrees across all 476 `.tw` files this repository tracks, and
> the self-hosted evaluator implements 120 of the 248 builtin names, so it
> cannot run the systems-mode half of the language at all. `docs/roadmap.md`, "What
> the second implementation agrees on, and what it does not", has the numbers.

Both features landed on the Go bootstrap and in `src/` together. That is the
check this project exists to be able to make, and neither half can make it
alone:

- The two checkers were compared character for character on 404 files -- all of
  `std`, `src`, `examples` and `testdata/cases` -- and agree on every one.
- The two formatters produce the same bytes for the new syntax.
- The self-hosted evaluator runs nested patterns, literal patterns and guards
  with the same answers as the bootstrap.

The self-hosted checker's own exhaustiveness checking caught two of the places
that needed updating for the new `TParam` type case while the port was in
progress, which is the feature checking the implementation of itself.

## What is still open

The two entries this release closed were the top of `docs/needs.md`'s open list.
What is under them has not moved: lazy iteration (NEEDS-96), a process interface
and ranged file reads, `Dict` keys that are not strings underneath (NEEDS-79),
packed narrow storage (NEEDS-111), and the GPU chain, which stays deferred on
the project's own measurements. `docs/needs.md` has the current list.
