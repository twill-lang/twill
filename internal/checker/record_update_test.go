package checker_test

import (
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/parser"
)

// A record update, `{ ..base, field: value }`, is a copy of base with the named
// fields replaced. The checker's job is to type the copy: every field the base
// has and the literal does not name keeps the base's type, and a named field
// takes the type of what replaces it.

func TestRecordUpdateKeepsTheBaseFieldTypes(t *testing.T) {
	// w is [1, 3] in the base and the update does not name it, so the copy's w is
	// [1, 3] too, and `@` with a length-2 vector is still a definite mismatch.
	wantOne(t, "let base = { w: [[1.0, 2.0, 3.0]], b: 0.5 }\nlet m = { ..base, b: 1.0 }\nlet y = m.w @ [1.0, 2.0]", "inner")
	// The same update with a conforming vector draws nothing.
	wantNone(t, "let base = { w: [[1.0, 2.0, 3.0]], b: 0.5 }\nlet m = { ..base, b: 1.0 }\nlet y = m.w @ [1.0, 2.0, 3.0]")
}

func TestRecordUpdateTakesTheReplacementType(t *testing.T) {
	// The base's w is [1, 3], the update replaces it with a [1, 2], and the
	// checker has to read the replacement rather than the base to see the error.
	wantOne(t, "let base = { w: [[1.0, 2.0, 3.0]] }\nlet m = { ..base, w: [[1.0, 2.0]] }\nlet y = m.w @ [1.0, 2.0, 3.0]", "inner")
}

func TestRecordUpdateWithNoFieldsIsACopy(t *testing.T) {
	wantNone(t, "let base = { w: [1.0, 2.0] }\nlet m = { ..base }\nlet y = sum(m.w)")
	wantOne(t, "let base = { w: [1.0, 2.0] }\nlet m = { ..base }\nlet y = m.wieght", "no field")
}

func TestRecordUpdateBaseMustBeARecord(t *testing.T) {
	wantOne(t, "mode systems\nfn f() {\n  let n: I64 = 3\n  let bad = { ..n, y: 1 }\n}\n",
		"the base of a record update must be a record, got I64")
	wantOne(t, "mode systems\nfn f() {\n  let s: Str = \"x\"\n  let bad = { ..s }\n}\n",
		"the base of a record update must be a record, got Str")
}

// The checker reports a mismatch only when it is certain. A base it cannot
// resolve to anything is not certainly a non-record, and a parameter with no
// annotation is the ordinary way to write a function that takes a model.
func TestRecordUpdateOnAnUnknownBaseIsQuiet(t *testing.T) {
	wantNone(t, "fn tweak(m) = { ..m, lr: 0.1 }\nlet out = tweak({ lr: 0.5 })")
	wantNone(t, "import \"std/nn\" as nn\nlet cfg = { ..nn, extra: 1.0 }")
}

// A field an update names that the base does not have is added, and that is not
// an error: the record it produces is the one `{ a: base.a, b: 1.0 }` produces,
// which runs. Records are structural here, so there is no declaration being
// contradicted.
func TestRecordUpdateMayAddAField(t *testing.T) {
	wantNone(t, "let base = { a: 1.0 }\nlet m = { ..base, b: 2.0 }\nlet y = m.a + m.b")
}

// A typed update is still checked against the struct's declaration, which is
// where a misspelt field on a struct is caught.
func TestTypedRecordUpdateChecksAgainstTheStruct(t *testing.T) {
	const decl = "mode systems\nstruct P { x: I64, y: I64 }\n"
	wantOne(t, decl+"fn f(base: P) -> P = P { ..base, z: 3 }\n", `P has no field "z"`)
	wantOne(t, decl+"fn f(base: P) -> P = P { ..base, x: \"no\" }\n", `field "x" of P`)
	wantNone(t, decl+"fn f(base: P) -> P = P { ..base, x: 3 }\n")
}

// A generic struct's arguments are read out of the field values a literal
// writes, and an update writes fewer of them: the ones it does not name come
// off the base, which the inference does not look at. That leaves an argument
// unbound rather than mismatched, so a correct update draws nothing, which is
// the half that matters.
func TestGenericStructUpdateIsQuiet(t *testing.T) {
	wantNone(t, "mode systems\nstruct Pair[A, B] { first: A, second: B }\n"+
		"fn bump(p: Pair[I64, Str]) -> Pair[I64, Str] = Pair { ..p, first: p.first + 1 }\n")
}

// The base is written first because it is the thing being copied. Anywhere else
// and the literal would need a second rule about which of two spellings of the
// same field wins.
func TestRecordUpdateBaseMustComeFirst(t *testing.T) {
	for _, src := range []string{
		"let base = { x: 1.0 }\nlet m = { y: 2.0, ..base }\n",
		"let base = { x: 1.0 }\nlet m = { ..base, ..base }\n",
	} {
		_, err := parser.Parse(src)
		if err == nil {
			t.Fatalf("expected a syntax error for %q, got none", src)
		}
		const want = "the base of a record update must come first, as `{ ..base, field: value }`"
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("for %q the parser said %q, want it to contain %q", src, err.Error(), want)
		}
	}
}
