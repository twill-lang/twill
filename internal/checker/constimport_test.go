package checker_test

import (
	"strconv"
	"strings"
	"testing"
)

// `const` across a file boundary.
//
// This is weft entry 9 as it was actually reported, and the first cut of
// `const` did not close it: `src/theme.tw` declaring `const HEX`, `app.tw` doing
// `import "theme.tw"` and then `HEX = arr_new()`, checked clean and ran, and the
// replacement was what every other importer then read. A guarantee a library
// makes about its own palette is worth nothing if only the library's own file is
// held to it. docs/roadmap.md entry 28.

const themeModule = `mode systems
struct Box { f: I64 }
const HEX: Arr[Str] = mk()
const REC: Box = Box { f: 1 }
let SIZE: I64 = 3
fn mk() -> Arr[Str] {
  let a: Arr[Str] = arr_new()
  push(a, "#000")
  a
}
`

// Every shape the rule judges, in one table: the binding, an element of it and
// a field of it, each written plainly and under an alias, plus the top-level
// rebinding by a plain and by a destructuring `let`; and, on the other side, an
// imported `let`, a parameter, a local `let`, a read, and a field of a local
// record that happens to share the name.
func TestImportedConstShapes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	cases := []struct {
		name string
		app  string
		want string // a substring of the one diagnostic, or "" for none
	}{
		{"assignment", "import \"theme.tw\"\nHEX = arr_new()\n", `HEX is declared const in "theme.tw" on line 3, so nothing may be assigned through that name`},
		{"element", "import \"theme.tw\"\nHEX[0] = \"#fff\"\n", "not the binding, and not an element or field of it"},
		{"field", "import \"theme.tw\"\nREC.f = 2\n", `REC is declared const in "theme.tw" on line 4`},
		{"alias assignment", "import \"theme.tw\" as t\nt.HEX = arr_new()\n", `HEX is declared const in "theme.tw" on line 3`},
		{"alias element", "import \"theme.tw\" as t\nt.HEX[0] = \"#fff\"\n", `HEX is declared const in "theme.tw" on line 3`},
		{"alias field", "import \"theme.tw\" as t\nt.REC.f = 2\n", `REC is declared const in "theme.tw" on line 4`},
		{"rebinding", "import \"theme.tw\"\nlet HEX: Arr[Str] = arr_new()\n", "may not bind the name again"},
		{"destructuring rebinding", "import \"theme.tw\"\nlet (HEX, n) = (arr_new(), 1)\n", "may not bind the name again"},
		{"an imported let", "import \"theme.tw\"\nSIZE = 4\n", ""},
		{"a local let", "import \"theme.tw\"\nfn f() {\n  let HEX: I64 = 1\n  HEX = 2\n}\n", ""},
		{"a parameter", "import \"theme.tw\"\nfn f(HEX: I64) -> I64 {\n  HEX = 2\n  HEX\n}\n", ""},
		{"a read", "import \"theme.tw\"\nfn f() -> Str = HEX[0]\n", ""},
		{"a local record's field", "import \"theme.tw\" as t\nstruct B { HEX: I64 }\nfn f() {\n  let b: B = B { HEX: 1 }\n  b.HEX = 2\n}\n", ""},
	}
	for _, tc := range cases {
		diags := checkFileIn(t, dir, "app.tw", "mode systems\n"+tc.app)
		if tc.want == "" {
			if len(diags) != 0 {
				t.Errorf("%s: got %d diagnostics, want none: %v", tc.name, len(diags), diags)
			}
			continue
		}
		if len(diags) != 1 {
			t.Errorf("%s: got %d diagnostics, want 1: %v", tc.name, len(diags), diags)
			continue
		}
		if !strings.Contains(diags[0].Msg, tc.want) {
			t.Errorf("%s: message does not say %q:\n  %s", tc.name, tc.want, diags[0].Msg)
		}
		if diags[0].Line != 3 {
			t.Errorf("%s: reported line %d, want 3 (the write)", tc.name, diags[0].Line)
		}
	}
}

// A plain import of a plain import: the names reach two levels, and so does the
// rule. An aliased import inside the imported file is not followed, because its
// names would be written `mid.t.HEX` here, which no rule reads.
func TestATransitivelyImportedConstIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	writeFile(t, dir, "mid.tw", "mode systems\nimport \"theme.tw\"\n")
	writeFile(t, dir, "aliased.tw", "mode systems\nimport \"theme.tw\" as t\n")
	if diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"mid.tw\"\nHEX = arr_new()\n"); len(diags) != 1 {
		t.Fatalf("through a plain import: got %d diagnostics, want 1: %v", len(diags), diags)
	}
	if diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"aliased.tw\"\nHEX = arr_new()\n"); len(diags) != 0 {
		t.Fatalf("through an aliased import: got %d diagnostics, want none: %v", len(diags), diags)
	}
}

// Check, the pure entry point, reads no files, so the same program handed over
// as a string is judged on its own: the rule needs CheckFile.
func TestCheckWithoutAFileReadsNoImports(t *testing.T) {
	wantNone(t, "mode systems\nimport \"theme.tw\"\nHEX = arr_new()\n")
}

// chainInto writes theme.tw and enough files to reach it through `files`
// imported files in total, and returns the app that imports the head of the
// chain. The walk counts levels, not files, so the number of siblings a file
// imports cannot move the bound; only the length of the chain can.
func chainInto(t *testing.T, dir string, files int) string {
	t.Helper()
	writeFile(t, dir, "theme.tw", "mode systems\nconst HEX: I64 = 1\n")
	prev := "theme.tw"
	for i := files - 1; i >= 1; i-- {
		name := "m" + strconv.Itoa(i) + ".tw"
		writeFile(t, dir, name, "mode systems\nimport \""+prev+"\"\n")
		prev = name
	}
	return "mode systems\nimport \"" + prev + "\"\nHEX = 2\n"
}

// Eight levels are followed and nine are not. The far side is pinned too: a
// walk with no bound is a directory traversal, and the point of pinning the
// ninth is that both checkers stop there rather than one of them stopping
// first. internal/interp/selfhost_test.go holds the self-hosted checker to the
// same two numbers.
func TestTheConstWalkFollowsEightLevels(t *testing.T) {
	dir := t.TempDir()
	if diags := checkFileIn(t, dir, "app.tw", chainInto(t, dir, 8)); len(diags) != 1 {
		t.Fatalf("eight levels: got %d diagnostics, want 1: %v", len(diags), diags)
	}
	if diags := checkFileIn(t, dir, "app.tw", chainInto(t, dir, 9)); len(diags) != 0 {
		t.Fatalf("nine levels: got %d diagnostics, want none past the bound: %v", len(diags), diags)
	}
}

// A cycle between imported files, and a file that imports itself, both stop.
func TestTheConstWalkStopsOnACycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.tw", "mode systems\nimport \"b.tw\"\nconst HEX: I64 = 1\n")
	writeFile(t, dir, "b.tw", "mode systems\nimport \"a.tw\"\nimport \"b.tw\"\n")
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"b.tw\"\nHEX = 2\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
}

// The enum walk is untouched by the const walk. This is the program that the
// first attempt broke: a file importing nine siblings where the last declares
// the enum, and an app matching on it non-exhaustively. Whether the match was
// judged depended on the order the siblings were written in.
func TestTheEnumWalkStillFollowsEveryOneOfNineSiblings(t *testing.T) {
	dir := t.TempDir()
	var mid strings.Builder
	mid.WriteString("mode systems\n")
	for i := 1; i <= 9; i++ {
		name := "s" + strconv.Itoa(i) + ".tw"
		body := "mode systems\nconst K" + strconv.Itoa(i) + ": I64 = 1\n"
		if i == 9 {
			body = "mode systems\nenum Colour { Red, Green, Blue }\n"
		}
		writeFile(t, dir, name, body)
		mid.WriteString("import \"" + name + "\"\n")
	}
	writeFile(t, dir, "mid.tw", mid.String())
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"mid.tw\"\nfn f(c: Colour) -> I64 {\n  match c {\n    Red => 1,\n    Green => 2,\n  }\n}\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want the non-exhaustive match: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, "Blue") {
		t.Errorf("unexpected message: %s", diags[0].Msg)
	}
}
