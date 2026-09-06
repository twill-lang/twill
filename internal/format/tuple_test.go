package format_test

import (
	"testing"

	"github.com/twill-lang/twill/internal/format"
	"github.com/twill-lang/twill/internal/parser"
)

// A tuple prints as it was written, in every position it may appear: a value, a
// return type, a parameter type, a binding's annotation, and a destructuring.
// The formatter is one half of a differential pair, so what it emits has to
// re-parse into the same thing.
func TestTupleRoundTrips(t *testing.T) {
	src := "mode systems\n" +
		"fn span(xs: Arr[F64]) -> (F64, F64) = (xs[0], xs[1])\n" +
		"fn take(p: (I64, Str), q: fn(I64) -> (I64, I64)) -> (I64, (Str, Bool)) = q(1)\n" +
		"let pair: (I64, Str) = (1, \"a\")\n" +
		"let (lo, hi) = span([1.0, 2.0])\n" +
		"let (u, _, w) = (1, 2, 3)\n" +
		"let nested = ((1, 2), (3, 4))\n"
	out, err := format.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"fn span(xs: Arr[F64]) -> (F64, F64) = (xs[0], xs[1])",
		"fn take(p: (I64, Str), q: fn(I64) -> (I64, I64)) -> (I64, (Str, Bool)) = q(1)",
		"let pair: (I64, Str) = (1, \"a\")",
		"let (lo, hi) = span([1, 2])",
		"let (u, _, w) = (1, 2, 3)",
		"let nested = ((1, 2), (3, 4))",
	} {
		if !containsLine(out, want) {
			t.Errorf("formatted output has no line %q:\n%s", want, out)
		}
	}
	if _, err := parser.Parse(out); err != nil {
		t.Fatalf("formatted output does not re-parse: %v\n%s", err, out)
	}
	again, err := format.Source(out)
	if err != nil {
		t.Fatal(err)
	}
	if again != out {
		t.Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out, again)
	}
}

// Grouping parentheses carry no node, so the formatter puts back only the ones
// precedence needs -- and a tuple, which is a node, keeps its own.
func TestGroupingIsNotMistakenForATuple(t *testing.T) {
	out, err := format.Source("let x = (1.0 + 2.0) * 3.0\n")
	if err != nil {
		t.Fatal(err)
	}
	if !containsLine(out, "let x = (1 + 2) * 3") {
		t.Errorf("got:\n%s", out)
	}
}

func containsLine(s, want string) bool {
	for _, line := range splitLines(s) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
