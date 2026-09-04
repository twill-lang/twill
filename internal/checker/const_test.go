package checker_test

import (
	"strings"
	"testing"
)

// `const`: a binding that may not be assigned to.
//
// A plain `import` drops a file's top-level definitions into the importing
// scope and they stay the one binding, so a library's lookup table is writable
// by anything that imports it. weft's `QUADRANTS`, `DENSITY`, `LEVELS` and `HEX`
// are the reported case, and a palette a caller can replace is a theme file that
// cannot keep the promise it makes about which colour means what.
// docs/roadmap.md entry 28.
//
// `let` could not be made read-only instead. A sweep of 643 `.tw` files found
// module-level mutable state in the standard library's own test harness
// (`std/tests/harness.tw` counts passes and failures in a top-level binding
// written from inside `check`), in warp's `examples/train.tw`, and in fourteen
// numeric-mode examples whose training loop is written at file level. So the
// guarantee has to be asked for.

func TestAssigningAConstIsRefused(t *testing.T) {
	wantOne(t, "mode systems\nconst K: I64 = 1\nfn f() {\n  K = 2\n}\n", "declared const on line 2")
}

// The message says where the binding was made, because that is the line to go
// look at, and what to do about it, because the two ways out (a new name, or a
// `let`) are both one edit.
func TestConstMessageSaysWhereAndWhatToDo(t *testing.T) {
	diags := diagnostics(t, "mode systems\nconst K: I64 = 1\nfn f() {\n  K = 2\n}\n")
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic")
	}
	msg := diags[0].Msg
	for _, want := range []string{
		"K is declared const on line 2",
		"nothing may be assigned through that name",
		"Bind a new name for the changed value, or declare it with let",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not say %q:\n  %s", want, msg)
		}
	}
	// It points at the assignment, not at the binding: line 4 is the line that
	// has to change.
	if diags[0].Line != 4 {
		t.Errorf("reported line %d, want 4 (the assignment)", diags[0].Line)
	}
}

func TestAssigningAConstIsAnError(t *testing.T) {
	diags := diagnostics(t, "mode systems\nconst K: I64 = 1\nK = 2\n")
	if len(diags) == 0 || !diags[0].IsError() {
		t.Fatalf("want an error, got %v", diags)
	}
}

// weft's shape: a lookup table built by a call and bound at file level, with a
// function further down reaching out and replacing it.
func TestConstCatchesTheWeftShape(t *testing.T) {
	wantOne(t, `mode systems
const HEX: Arr[Str] = mk()
fn mk() -> Arr[Str] {
  let a: Arr[Str] = arr_new()
  push(a, "#000")
  a
}
fn rethemed() {
  HEX = mk()
}
`, "HEX is declared const on line 2")
}

// An element or a field reached through the name is refused too. Swapping the
// table and editing the table are the same failure for a caller reading it, and
// `const HEX` followed by `HEX[0] = ...` in one file contradicts itself.
func TestAssigningThroughAConstIsRefused(t *testing.T) {
	wantOne(t, `mode systems
const HEX: Arr[Str] = mk()
fn mk() -> Arr[Str] {
  let a: Arr[Str] = arr_new()
  push(a, "#000")
  a
}
fn edit() {
  HEX[0] = "#fff"
}
`, "HEX is declared const on line 2")
}

// A function declared above the binding still sees it as const. The prelude
// registers top-level names before any body is checked, and warp's
// `examples/train.tw` writes its counter eight lines above the binding, so a
// rule that waited for the walk would have a hole exactly there.
func TestAConstIsConstAboveItsOwnLine(t *testing.T) {
	wantOne(t, "mode systems\nfn f() {\n  K = 2\n}\nconst K: I64 = 1\n", "declared const on line 5")
}

// A `const` inside a function is a local constant, and it is the enclosing
// scope's, not the file's.
func TestALocalConstIsRefusedToo(t *testing.T) {
	wantOne(t, "mode systems\nfn f() {\n  const K: I64 = 1\n  K = 2\n}\n", "declared const on line 3")
}

// What must not be flagged.

// `let` is unchanged. This is the whole reason `const` is a second keyword
// rather than a new meaning for `let`: the standard library's test harness and
// every numeric-mode training loop assign to a top-level binding.
func TestAssigningALetIsFine(t *testing.T) {
	wantNone(t, "mode systems\nlet n: I64 = 0\nfn bump() {\n  n = n + 1\n}\n")
}

func TestATopLevelLetAssignedAtTopLevelIsFine(t *testing.T) {
	wantNone(t, "mode systems\nlet n: I64 = 0\nn = n + 1\n")
}

// A `let` in a nearer scope is a different binding, so it is mutable even
// though the outer name is const.
func TestALetShadowingAConstIsFine(t *testing.T) {
	wantNone(t, "mode systems\nconst K: I64 = 1\nfn f() {\n  let K: I64 = 2\n  K = 3\n}\n")
}

// A parameter is a binding of its own too.
func TestAParameterNamedAfterAConstIsFine(t *testing.T) {
	wantNone(t, "mode systems\nconst K: I64 = 1\nfn f(K: I64) {\n  K = 2\n}\n")
}

// Reading a const is the point of one.
func TestReadingAConstIsFine(t *testing.T) {
	wantNone(t, "mode systems\nconst K: I64 = 1\nfn f() -> I64 = K + 1\n")
}

// A const in numeric mode too: the keyword is not gated on the dialect, since
// the binding it makes means the same thing in both.
func TestConstWorksInNumericMode(t *testing.T) {
	wantNone(t, "const LR = 0.01\nfn step(w) = w - LR\n")
	wantOne(t, "const LR = 0.01\nfn step(w) {\n  LR = 0.02\n}\n", "declared const on line 1")
}
