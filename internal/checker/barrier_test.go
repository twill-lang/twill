package checker_test

import "testing"

// The compiler barrier is a barrier to an optimiser and to nothing else. In
// particular it is not a barrier to the checker, and that is a decision rather
// than an accident: docs/CORRECTNESS.md names `grad` as a shape barrier, where
// wrapping an expression loses every shape error inside it. Doing the same to
// black_box would trade a benchmark's shape checking for nothing, since a
// benchmark is code nobody reads twice and is exactly where a silent shape
// mismatch survives longest.

// The type comes straight back out, so an annotation downstream of the barrier
// is checked against the type that went in.
func TestBarrierPassesItsTypeThrough(t *testing.T) {
	wantNone(t, "mode systems\nfn f() -> I64 = black_box(3)")
	wantNone(t, "mode systems\nfn f() -> Str = black_box(\"a\")")
	wantOne(t, "mode systems\nfn f() -> Str = black_box(3)", "Str")
}

// The shape rides through too: this is docs/CORRECTNESS.md's own example of a
// mismatch, with the barrier inserted where `grad` would have hidden it.
func TestBarrierIsNotAShapeBarrier(t *testing.T) {
	wantOne(t, "let h = fn(v) = sum(zeros(2, 3) @ black_box(v))\nprint(h(zeros(2)))", "inner")
	wantOne(t, "let z = black_box([1.0, 2.0]) + [1.0, 2.0, 3.0]", "shape mismatch")
}

// One argument, and the message is the one every other fixed-arity builtin
// gives. The self-hosted checker reports the same sentence.
func TestBarrierTakesExactlyOneArgument(t *testing.T) {
	wantOne(t, "print(black_box())", "black_box expects 1 argument(s), got 0")
	wantOne(t, "print(black_box(1.0, 2.0))", "black_box expects 1 argument(s), got 2")
}
