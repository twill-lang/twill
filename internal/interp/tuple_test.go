package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
)

// runErr runs a program that is expected to fail and returns the message.
func runErr(t *testing.T, src string) string {
	t.Helper()
	ip := interp.New(func(string) {})
	if _, err := ip.Run(src); err != nil {
		return err.Error()
	}
	t.Fatalf("expected a runtime error, source:\n%s", src)
	return ""
}

func TestTuplePrintsWithParentheses(t *testing.T) {
	_, out := run(t, "print((1.0, 2.0))")
	if len(out) != 1 || out[0] != "(1, 2)" {
		t.Errorf("got %q, want [\"(1, 2)\"]", out)
	}
	_, out = run(t, `print((1.0, "two", true))`)
	if len(out) != 1 || out[0] != "(1, two, true)" {
		t.Errorf("got %q", out)
	}
	// Tuples nest, and a nested one is printed by the same rule.
	_, out = run(t, "print(((1.0, 2.0), [3.0, 4.0]))")
	if len(out) != 1 || out[0] != "((1, 2), tensor([3, 4], shape=[2]))" {
		t.Errorf("got %q", out)
	}
}

// The comma is what makes a tuple. Without one the parentheses are grouping,
// which is what they have always been, and that must not have changed.
func TestParenthesesWithoutACommaStayGrouping(t *testing.T) {
	if got := scalar(t, "(1.0 + 2.0) * 3.0"); got != 9 {
		t.Errorf("got %v, want 9", got)
	}
	if got := scalar(t, "((((7.0))))"); got != 7 {
		t.Errorf("got %v, want 7", got)
	}
}

func TestDestructuringLetBindsEachName(t *testing.T) {
	if got := scalar(t, "let (a, b) = (3.0, 4.0)\na * b"); got != 12 {
		t.Errorf("got %v, want 12", got)
	}
	// Through a call, which is the shape the feature exists for.
	src := "fn span(xs) = (xs[0], xs[2])\nlet (lo, hi) = span([1.0, 2.0, 5.0])\nhi - lo"
	if got := scalar(t, src); got != 4 {
		t.Errorf("got %v, want 4", got)
	}
	// `_` binds nothing but still takes a position.
	if got := scalar(t, "let (a, _, c) = (1.0, 99.0, 2.0)\na + c"); got != 3 {
		t.Errorf("got %v, want 3", got)
	}
}

func TestDestructuringLetDoesNotBindUnderscore(t *testing.T) {
	msg := runErr(t, "let (a, _) = (1.0, 2.0)\n_")
	if !strings.Contains(msg, "undefined variable") {
		t.Errorf("got %q, want an undefined-variable error", msg)
	}
}

func TestTupleEqualityIsStructural(t *testing.T) {
	cases := map[string]bool{
		"(1.0, 2.0) == (1.0, 2.0)":               true,
		"(1.0, 2.0) == (1.0, 3.0)":               false,
		"(1.0, 2.0) != (1.0, 3.0)":               true,
		`("a", true) == ("a", true)`:             true,
		"((1.0, 2.0), 3.0) == ((1.0, 2.0), 3.0)": true,
		// Arity is part of identity, and a tuple is not the list of its parts.
		"(1.0, 2.0) == (1.0, 2.0, 3.0)": false,
		`(1.0, 2.0) == ["a", "b"]`:      false,
	}
	for src, want := range cases {
		_, out := run(t, "print("+src+")")
		if len(out) != 1 {
			t.Fatalf("%s: expected one printed line, got %q", src, out)
		}
		if got := out[0] == "true"; got != want {
			t.Errorf("%s: got %v, want %v", src, got, want)
		}
	}
}

// A destructuring binding says how many values it wants, and the runtime holds
// the value to that. Both refusals name the counts, because "not a tuple" on
// its own does not say what to change.
func TestDestructuringRefusesTheWrongValue(t *testing.T) {
	msg := runErr(t, "fn g() = 5.0\nlet (a, b) = g()")
	if !strings.Contains(msg, "needs a tuple of 2 values on the right") {
		t.Errorf("got %q", msg)
	}
	msg = runErr(t, "fn g() = (1.0, 2.0, 3.0)\nlet (a, b) = g()")
	if !strings.Contains(msg, "needs a tuple of 2 values, but the value has 3") {
		t.Errorf("got %q", msg)
	}
}

// The three refusals the syntax makes, each with the wording that says what to
// write instead.
//
// The `const` one is pinned to more than its first clause on purpose. Its
// original wording gave the reason as the const-rebinding rule being "checked
// over single names", and that reason stopped being true the moment a
// destructuring `let` was counted by that rule. A refusal that explains itself
// with something the checker no longer does is worse than one that does not
// explain itself, so the second half is asserted here.
func TestTupleSyntaxRefusals(t *testing.T) {
	cases := map[string]string{
		"let x = (1.0,)":       "a tuple holds at least two values",
		"let (a) = (1.0, 2.0)": "a tuple holds at least two values",
		"let x = (1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0)": "a tuple holds at most 8 values",
		"const (a, b) = (1.0, 2.0)":                             "not const: const declares a guarantee about a single name",
		"let (a, 3) = (1.0, 2.0)":                               "expected a name in a destructuring let",
	}
	for src, want := range cases {
		ip := interp.New(func(string) {})
		_, err := ip.Run(src)
		if err == nil {
			t.Errorf("%s: expected a syntax error", src)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %q, want it to contain %q", src, err.Error(), want)
		}
	}
}

// Eight is the limit and eight is allowed; the refusal starts at nine.
func TestTupleOfEightIsAllowed(t *testing.T) {
	src := "let (a, b, c, d, e, f, g, h) = (1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0)\nh"
	if got := scalar(t, src); got != 8 {
		t.Errorf("got %v, want 8", got)
	}
}

// A tuple is a container the tracer has to see through. `closeScope` reports
// the tensors escaping a statement, and one returned inside a tuple escapes
// exactly as one returned inside a list does; a scope that did not hear about
// it is the wrong-answer case liveTensors exists to prevent. The claim is the
// tracer's own: running with it on and off must print the same bytes.
func TestTracingSeesTensorsInsideATuple(t *testing.T) {
	src := "fn halves(x) = (x * 0.5, x * 2.0)\n" +
		"let w = [1.0, 2.0, 3.0]\n" +
		"let (lo, hi) = halves(w)\n" +
		"print(lo)\n" +
		"print(hi)\n" +
		"print(sum(lo) + sum(hi))\n" +
		"print(halves(w))\n"
	dir := t.TempDir()
	file := filepath.Join(dir, "tuple_trace.tw")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := runTraced(t, file, true), runTraced(t, file, false); got != want {
		t.Errorf("tracing changed the output:\n with tracing:\n%s\nwithout:\n%s", got, want)
	}
}
