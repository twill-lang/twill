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
// `let` could not be made read-only instead. A read-only `let` was implemented
// behind a flag and swept over the ecosystem, and it refused the standard
// library's own test harness (`std/tests/harness.tw` counts passes and failures
// in a top-level binding written from inside `check`), the test harness of
// every satellite repository, warp's `examples/train.tw`, and the numeric-mode
// examples whose training loop is written at file level. So the guarantee has
// to be asked for.
//
// The corpus and the counts are in CHANGELOG.md under `const`, and they are
// there rather than here on purpose: this comment carried its own copy of them,
// the sweep was re-run, the changelog was corrected, and the copy here was left
// behind saying the old figure. A number worth quoting is worth having one home,
// so this comment quotes none.

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
// `examples/train.tw` writes its counter several lines above the binding, so a
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

// Rebinding a const in the scope that declared it.
//
// The first cut of this rule tracked constness in a per-scope map that a later
// `let` of the same name simply deleted. That made `const HEX` followed by `let
// HEX` a silent revocation, and worse, it made the outcome depend on statement
// order: the prelude registers top-level consts before the walk, so `let HEX`
// above `const HEX` was refused and `let HEX` below it was not. A guarantee that
// turns off when you move a line is not a guarantee.
//
// So a `const` is the only binding of its name in the scope that declares it,
// and a second one is refused wherever it sits.

func TestALetRebindingAConstInTheSameScopeIsRefused(t *testing.T) {
	wantOne(t, "mode systems\nconst K: I64 = 1\nlet K: I64 = 2\n", "K is declared const on line 2")
}

// The same file with the two lines swapped. This is the order dependence
// itself: before the fix the `let` below was accepted and the `let` above was
// not, so both orders are pinned.
func TestAConstRebindingALetInTheSameScopeIsRefusedToo(t *testing.T) {
	wantOne(t, "mode systems\nlet K: I64 = 1\nconst K: I64 = 2\n", "K is declared const on line 3")
}

func TestALetRebindingAConstInsideAFunctionIsRefused(t *testing.T) {
	wantOne(t, "mode systems\nfn f() {\n  const K: I64 = 1\n  let K: I64 = 2\n}\n", "K is declared const on line 3")
}

func TestALetRebindingAConstInsideABlockIsRefused(t *testing.T) {
	wantOne(t, "mode systems\nfn f(n: I64) {\n  if n > 0 {\n    const K: I64 = 1\n    let K: I64 = 2\n  }\n}\n", "K is declared const on line 4")
}

// Two `const`s of one name in one scope is the same mistake with the same
// consequence: which line the diagnostics quote depends on which one the walk
// reached last.
func TestAConstRebindingAConstIsRefused(t *testing.T) {
	wantOne(t, "mode systems\nconst K: I64 = 1\nconst K: I64 = 2\n", "K is declared const on line 2")
}

// The message names the const's line and both ways out, and it points at the
// rebinding rather than at the const, because the rebinding is the line to
// delete.
func TestTheRebindMessageSaysWhereAndWhatToDo(t *testing.T) {
	diags := diagnostics(t, "mode systems\nconst K: I64 = 1\nlet K: I64 = 2\n")
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic")
	}
	msg := diags[0].Msg
	for _, want := range []string{
		"K is declared const on line 2",
		"cannot be bound a second time in the same scope",
		"Rename one of them, or declare line 2 with let",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not say %q:\n  %s", want, msg)
		}
	}
	if diags[0].Line != 3 {
		t.Errorf("reported line %d, want 3 (the rebinding)", diags[0].Line)
	}
}

// The rebinding does not take the const's place. Refusing the `let` and then
// letting every assignment after it through would report the smaller half of
// the mistake and hide the one that matters.
func TestARefusedRebindDoesNotRevokeConstness(t *testing.T) {
	diags := diagnostics(t, "mode systems\nconst K: I64 = 1\nlet K: I64 = 2\nK = 3\n")
	var sawAssign bool
	for _, d := range diags {
		if strings.Contains(d.Msg, "nothing may be assigned through that name") {
			sawAssign = true
		}
	}
	if !sawAssign {
		t.Errorf("the assignment on line 4 was not refused; got %v", diags)
	}
}

// What must still not be flagged: an inner scope is a different scope, and a
// `let` there is a new binding rather than a rebinding.
func TestALetShadowingAConstInAnInnerScopeIsStillFine(t *testing.T) {
	wantNone(t, "mode systems\nconst K: I64 = 1\nfn f() {\n  let K: I64 = 2\n  K = 3\n}\n")
}

// Two `let`s of one name in one scope stay legal. The language allows it, the
// ecosystem is written with it, and this rule is about `const` only.
func TestTwoLetsOfOneNameAreStillFine(t *testing.T) {
	wantNone(t, "mode systems\nlet n: I64 = 1\nlet n: I64 = 2\nprint(n)\n")
}

// Two consts of the same name in sibling scopes are two bindings, not one.
func TestSiblingScopesMayEachDeclareTheName(t *testing.T) {
	wantNone(t, "mode systems\nfn f() {\n  const K: I64 = 1\n  print(K)\n}\nfn g() {\n  const K: I64 = 2\n  print(K)\n}\n")
}
