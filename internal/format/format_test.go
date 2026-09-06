package format_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/format"
	"github.com/twill-lang/twill/internal/parser"
)

func TestFormatExamplesRoundTrip(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.tw"))
	files2, _ := filepath.Glob(filepath.Join("..", "..", "std", "*.tw"))
	files = append(files, files2...)
	if len(files) == 0 {
		t.Fatal("no .tw files found")
	}
	for _, f := range files {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			// Formatting must produce parseable output.
			out1, err := format.Source(string(src))
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			if _, err := parser.Parse(out1); err != nil {
				t.Fatalf("formatted output does not re-parse: %v\n---\n%s", err, out1)
			}
			// Formatting must be idempotent.
			out2, err := format.Source(out1)
			if err != nil {
				t.Fatalf("re-format: %v", err)
			}
			if out1 != out2 {
				t.Errorf("not idempotent for %s:\n--- first ---\n%s\n--- second ---\n%s", f, out1, out2)
			}
		})
	}
}

func TestFormatPreservesPrecedence(t *testing.T) {
	// The tree must be preserved: grouping that changes associativity needs
	// parentheses in the output.
	cases := map[string]string{
		"let x = 1 + 2 * 3":         "let x = 1 + 2 * 3\n",
		"let x = (1 + 2) * 3":       "let x = (1 + 2) * 3\n",
		"let x = a - (b - c)":       "let x = a - (b - c)\n",
		"let x = a - b - c":         "let x = a - b - c\n",
		"let x = (2 ^ 3) ^ 2":       "let x = (2 ^ 3) ^ 2\n",
		"let x = 2 ^ 3 ^ 2":         "let x = 2 ^ 3 ^ 2\n",
		"let x = -(a + b)":          "let x = -(a + b)\n",
		"let x = not (a and b)":     "let x = not (a and b)\n",
		"let x = (a + b) * (c - d)": "let x = (a + b) * (c - d)\n",
	}
	for in, want := range cases {
		got, err := format.Source(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != want {
			t.Errorf("format(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatBasics(t *testing.T) {
	cases := map[string]string{
		"let  x=1+2*3":                      "let x = 1 + 2 * 3\n",
		"fn f(x)=x*x":                       "fn f(x) = x * x\n",
		"fn g(A:[n,k],B:[k,m])->[n,m]{A@B}": "fn g(A: [n, k], B: [k, m]) -> [n, m] {\n  A @ B\n}\n",
		"let r={a:1,b:2}":                   "let r = { a: 1, b: 2 }\n",
		"type M={w:[2],b:[]}":               "type M = { w: [2], b: [] }\n",
	}
	for in, want := range cases {
		got, err := format.Source(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != want {
			t.Errorf("format(%q) =\n%q\nwant\n%q", in, got, want)
		}
	}
}

// The formatter must not change what a program computes. Both of these did.

// An integer literal is printed from its digits. Through the f64 the parser
// produced, 9007199254740993 came back as ...992, 1234567890123456789 as
// ...768, and MAX_I64 as the float 9.223372036854776e+18. Since 1.6 an I64
// literal is an exact value, so that was a semantic change written to disk by
// `twill fmt --write`.
func TestFormatPreservesLargeIntegerLiterals(t *testing.T) {
	for _, lit := range []string{
		"9223372036854775807",  // MAX_I64
		"-9223372036854775808", // MIN_I64, which is a minus over a magnitude no positive int64 holds
		"9007199254740993",     // 2^53 + 1, the first integer an f64 cannot hold
		"1234567890123456789",
		"4611686018427387904",
	} {
		src := "mode systems\nlet x: I64 = " + lit + "\n"
		out, err := format.Source(src)
		if err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		if want := "let x: I64 = " + lit; !strings.Contains(out, want) {
			t.Errorf("formatting %s gave %q, want it to contain %q", lit, out, want)
		}
	}
}

// A float still normalises the way every golden expects.
func TestFormatStillNormalisesNumbers(t *testing.T) {
	out, err := format.Source("let a = 3.0\nlet b = 1e-3\nlet c = 1.5e10\nlet d = 0.5\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"let a = 3", "let b = 0.001", "let c = 15000000000", "let d = 0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q is missing %q", out, want)
		}
	}
}

// Parentheses a postfix operator needs are kept. Dropping them silently changed
// the program: `(x + y).to(i8)` cast y alone, `(p + q).field` read q's field,
// and `(m + n)[0]` indexed n. shuttle's src/quant.tw is a real file it happened
// to, and the damage only showed up as a second `fmt` producing different text.
func TestFormatKeepsParenthesesAPostfixNeeds(t *testing.T) {
	for _, src := range []string{
		"let a = (x + y).to(i8)\n",
		"let b = (p + q).field\n",
		"let c = (m + n)[0]\n",
		"let d = (m + n)[0:2]\n",
		"let e = (f + g)(x)\n",
	} {
		out, err := format.Source(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if out != src {
			t.Errorf("formatting %q gave %q; the parentheses are load-bearing", src, out)
		}
	}
}

// A printer with no case for a declaration's type parameters deletes them,
// which turns a generic program into one that means something else -- the same
// failure `unit USD` had in 1.6. Both halves of the round trip are checked:
// the parameters survive, and the pattern language's nesting and guards do too.
func TestTypeParametersSurviveFormatting(t *testing.T) {
	src := "mode systems\nstruct Pair[A, B] { first: A, second: B }\nenum Tree[T] { Leaf(T), Empty }\nfn swap[A, B](p: Pair[A, B]) -> Pair[B, A] = Pair { first: p.second, second: p.first }\n"
	got, err := format.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"struct Pair[A, B]", "enum Tree[T]", "fn swap[A, B]"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output lost %q:\n%s", want, got)
		}
	}
}

func TestPatternsSurviveFormatting(t *testing.T) {
	src := "mode systems\nfn f(o: Opt[Res[I64, Str]]) -> Str {\n  match o { Some(Ok(3)) => \"three\", Some(Ok(n)) if n > 0 => \"pos\", Some(Ok(n)) => \"ok\", Some(Err(e)) => e, None => \"none\" }\n}\n"
	got, err := format.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Some(Ok(3))", "Some(Ok(n)) if n > 0", "Some(Err(e))"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output lost %q:\n%s", want, got)
		}
	}
}

// `const` and `let` are one node with a flag, so the formatter is the place
// where the distinction is easiest to lose. Printing a `const` as a `let` would
// turn a refusal into silence, and `twill fmt --write` would do it in place.
func TestConstSurvivesFormatting(t *testing.T) {
	cases := map[string]string{
		"const  K=1": "const K = 1\n",
		"mode systems\nconst K: I64 = 1\nlet n: I64 = 0\n": "mode systems\n\nconst K: I64 = 1\nlet n: I64 = 0\n",
	}
	for in, want := range cases {
		got, err := format.Source(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != want {
			t.Errorf("format(%q) = %q, want %q", in, got, want)
		}
	}
}

// A record update's base is part of the literal, not a field, so it is the one
// piece of a `{ ... }` the printer can silently drop: nothing else in the node
// is missing, the output still parses, and the program means something else.
// The count test in corpus_test.go counts statements, not expressions, so this
// is where that loss would show.
func TestRecordUpdateSurvivesFormatting(t *testing.T) {
	cases := map[string]string{
		"let m = {..base}":              "let m = { ..base }\n",
		"let m = {..base,b:2.5}":        "let m = { ..base, b: 2.5 }\n",
		"let m = P{..base,b:2.5}":       "let m = P { ..base, b: 2.5 }\n",
		"let m = {..outer.inner,b:2.5}": "let m = { ..outer.inner, b: 2.5 }\n",
		"let m = {..mk(2.5),b:2.5}":     "let m = { ..mk(2.5), b: 2.5 }\n",
		"let m = {..{a: 2.5},b:2.5}":    "let m = { ..{ a: 2.5 }, b: 2.5 }\n",
	}
	for in, want := range cases {
		got, err := format.Source(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got != want {
			t.Errorf("format(%q) = %q, want %q", in, got, want)
		}
		// What it prints has to parse back, or `twill fmt --write` breaks the file.
		if _, err := parser.Parse(got); err != nil {
			t.Errorf("formatted %q does not re-parse: %v", got, err)
		}
	}
}
