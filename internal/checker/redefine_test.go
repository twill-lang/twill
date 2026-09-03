package checker_test

import (
	"strings"
	"testing"
)

// A name declared twice in one file.
//
// The evaluator takes the last definition and says nothing, so an edit that
// writes a replacement above the body it was meant to replace leaves the old
// body running. twill-lang/spool#4 was exactly that, in two files at once, and
// it went through a passing test suite, a passing source gate and passing CI:
// nothing anywhere looked at whether a name was defined twice.
//
// There is no conditional compilation in this language, so there is no reading
// of a second declaration under which the author meant it.

func TestRedefinedFunctionIsRefused(t *testing.T) {
	wantOne(t, "fn f() = 1\nfn f() = 2\n", "already defined on line 1")
}

// The message has to say which one wins, because the whole failure is someone
// believing the wrong one does.
func TestRedefinitionSaysWhichOneRuns(t *testing.T) {
	diags := diagnostics(t, "fn f() = 1\nfn f() = 2\n")
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic")
	}
	msg := diags[0].Msg
	for _, want := range []string{"the later definition is the one that runs", "dead"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not say %q:\n  %s", want, msg)
		}
	}
	// It points at the redefinition, not at the original: line 2 is the line
	// someone has to look at.
	if diags[0].Line != 2 {
		t.Errorf("reported line %d, want 2 (the second declaration)", diags[0].Line)
	}
}

// It is an error, not a warning: the program runs, but it does not do what the
// file says, which is the case for refusing it.
func TestRedefinitionIsAnError(t *testing.T) {
	diags := diagnostics(t, "fn f() = 1\nfn f() = 2\n")
	if len(diags) == 0 || !diags[0].IsError() {
		t.Fatalf("want an error, got %v", diags)
	}
}

// spool's shape: the replacement written above the body it was replacing, with
// the old one still there and still winning.
func TestRedefinitionCatchesTheSpoolShape(t *testing.T) {
	wantOne(t, `mode systems
fn sort_strs(xs: Arr[Str]) -> Arr[Str] = sort(xs)
fn sort_strs(xs: Arr[Str]) -> Arr[Str] {
  let out = xs
  out
}
`, "sort_strs is already defined")
}

// Three of them report twice, once per redefinition, so deleting one does not
// hide the other.
func TestEveryRedefinitionIsReported(t *testing.T) {
	diags := diagnostics(t, "fn f() = 1\nfn f() = 2\nfn f() = 3\n")
	n := 0
	for _, d := range diags {
		if strings.Contains(d.Msg, "already defined") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("got %d redefinition diagnostics, want 2", n)
	}
}

// What must not be flagged.
func TestDistinctNamesAreFine(t *testing.T) {
	wantNone(t, "fn f() = 1\nfn g() = 2\n")
}

// A local shadowing a function is a scope, not a redefinition: the outer name
// is still reachable from everywhere else in the file.
func TestALocalNamedAfterAFunctionIsFine(t *testing.T) {
	wantNone(t, "mode systems\nfn f() -> I64 = 1\nfn g() -> I64 {\n  let f: I64 = 2\n  f\n}\n")
}

// A function may still take a builtin's name. That shadow is deliberate and
// supported -- the declaration wins over the builtin -- and only a second
// declaration of it is the mistake.
func TestShadowingABuiltinOnceIsFine(t *testing.T) {
	wantNone(t, "mode systems\nfn sort(xs: Arr[Str]) -> Arr[Str] = xs\n")
}
