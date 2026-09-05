package format_test

import (
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/format"
)

// A paragraph break between two statements is whitespace that carries meaning:
// it is how the author grouped a function into steps, and a formatter that
// deletes it makes every file it touches read as one undifferentiated run. The
// self-hosted printer has kept them since src/fmt.tw's maybe_blank landed
// (NEEDS-78); these are that rule, stated on the Go side.
//
// The rule is a gap of two or more source lines between the end of one
// statement and the leading edge of the next, where the leading edge is the
// next statement's first own-line comment if it has one. However many blank
// lines the author left, one comes out, so formatting is still a normalisation
// rather than a transcription.

func formatted(t *testing.T, src string) string {
	t.Helper()
	out, err := format.Source(src)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	return out
}

func TestFormatKeepsParagraphBreaks(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "between top-level statements",
			in:   "let a = 1\n\nlet b = 2\n",
			want: "let a = 1\n\nlet b = 2\n",
		},
		{
			name: "several blanks collapse to one",
			in:   "let a = 1\n\n\n\nlet b = 2\n",
			want: "let a = 1\n\nlet b = 2\n",
		},
		{
			name: "no gap stays no gap",
			in:   "let a = 1\nlet b = 2\n",
			want: "let a = 1\nlet b = 2\n",
		},
		{
			name: "inside a function body",
			in:   "fn f() {\n  let a = 1\n\n  let b = 2\n}\n",
			want: "fn f() {\n  let a = 1\n\n  let b = 2\n}\n",
		},
		{
			name: "the first statement of a block takes no leading blank",
			in:   "fn f() {\n\n  let a = 1\n}\n",
			want: "fn f() {\n  let a = 1\n}\n",
		},
		{
			// The gap is measured from the previous statement's *last* line, so
			// a multi-line statement's own span is not mistaken for a break.
			name: "a multi-line statement is not itself a gap",
			in:   "fn f() {\n  let a = 1\n}\nlet b = 2\n",
			want: "fn f() {\n  let a = 1\n}\nlet b = 2\n",
		},
		{
			// A comment sitting directly under a statement belongs to what
			// follows it, and there is no break above it.
			name: "a comment directly below is not a break",
			in:   "let a = 1\n# about b\nlet b = 2\n",
			want: "let a = 1\n# about b\nlet b = 2\n",
		},
		{
			// ...and when the author did leave a gap, the blank goes above the
			// comment rather than between the comment and its statement.
			name: "the blank goes above a leading comment",
			in:   "let a = 1\n\n# about b\nlet b = 2\n",
			want: "let a = 1\n\n# about b\nlet b = 2\n",
		},
		{
			// A block flattened onto one line has nowhere to put a blank, so
			// this is where preservation stops. Without the drop the join would
			// render "; ; ".
			name: "an inlined block drops it rather than emitting an empty piece",
			in:   "let x = if c { let a = 1\n\n  let b = 2\n  a } else { 0 }\n",
			want: "let x = if c { let a = 1; let b = 2; a } else { 0 }\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatted(t, tc.in); got != tc.want {
				t.Errorf("format(%q) =\n%q\nwant\n%q", tc.in, got, tc.want)
			}
		})
	}
}

// Preserving a blank line is only safe if it is stable: format twice and the
// blanks must not multiply or vanish.
func TestFormatBlankLinesAreIdempotent(t *testing.T) {
	src := `mode systems

fn f() -> I64 {
  let a: I64 = 1

  # a paragraph break
  let b: I64 = 2

  a + b
}

fn g() -> I64 = 1
`
	once := formatted(t, src)
	if once != src {
		t.Errorf("formatting a formatted file changed it:\n--- got ---\n%s\n--- want ---\n%s", once, src)
	}
	if twice := formatted(t, once); twice != once {
		t.Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", once, twice)
	}
}

// The two implementations are meant to agree byte for byte, and this is the
// list of statement kinds whose end line the Go side had no way to ask for.
// src/ast.tw's stmt_end_line answers it for a while, a for, a bare block and a
// function with a block body; everything else ends where it starts.
func TestBlankAfterEveryBlockBearingStatement(t *testing.T) {
	for _, in := range []string{
		"while c {\n  f()\n}\n\nlet a = 1\n",
		"for i in r {\n  f()\n}\n\nlet a = 1\n",
		"fn f() {\n  g()\n}\n\nlet a = 1\n",
		"fn f() {\n  while c {\n    g()\n  }\n\n  let b = 2\n}\n",
	} {
		got := formatted(t, in)
		if got != in {
			t.Errorf("format(%q) = %q, want it unchanged", in, got)
		}
		if strings.Count(got, "\n\n") != 1 {
			t.Errorf("format(%q) = %q, want exactly one blank line", in, got)
		}
	}
}
