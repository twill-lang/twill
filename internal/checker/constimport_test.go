package checker_test

import (
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
// held to it.
//
// The import walk that already existed for enums collects top-level `const`
// names too. It is still the smallest thing that makes the rule checkable and
// not a module system: nothing here types a value or resolves a function.

const themeModule = `mode systems
const HEX: Arr[Str] = mk()
let SIZE: I64 = 3
fn mk() -> Arr[Str] {
  let a: Arr[Str] = arr_new()
  push(a, "#000")
  a
}
`

func TestAssigningAnImportedConstIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\"\nHEX = arr_new()\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
	for _, want := range []string{`HEX is declared const in "theme.tw" on line 2`, "nothing may be assigned through that name"} {
		if !strings.Contains(diags[0].Msg, want) {
			t.Errorf("message does not say %q:\n  %s", want, diags[0].Msg)
		}
	}
}

// The element write is the half that actually reaches other importers through
// the shared handle, so it is the half that matters most.
func TestWritingAnElementOfAnImportedConstIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\"\nHEX[0] = \"#fff\"\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
}

// Under an alias the name is written `theme.HEX`, and assigning through it
// mutates the same table.
func TestAssigningAnImportedConstThroughAnAliasIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\" as theme\ntheme.HEX = arr_new()\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, `HEX is declared const in "theme.tw" on line 2`) {
		t.Errorf("unexpected message: %s", diags[0].Msg)
	}
}

func TestWritingAnElementThroughAnAliasIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\" as theme\ntheme.HEX[0] = \"#fff\"\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
}

// A plain import brings the name in, so a top-level `let` of the same name in
// the importer is a rebinding, and the rebinding is visible to every other
// importer. Refusing it is the cross-file half of the same-scope rule.
func TestRebindingAnImportedConstIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\"\nlet HEX: Arr[Str] = arr_new()\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, "may not bind the name again") {
		t.Errorf("unexpected message: %s", diags[0].Msg)
	}
}

// A plain import of a plain import: the names reach two levels, and so does the
// rule.
func TestATransitivelyImportedConstIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	writeFile(t, dir, "mid.tw", "mode systems\nimport \"theme.tw\"\n")
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"mid.tw\"\nHEX = arr_new()\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
}

// What must not be flagged.

// An imported `let` stays writable. `const` is the opt-in and this is the whole
// reason it is a second keyword.
func TestAssigningAnImportedLetIsFine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\"\nSIZE = 4\n")
	if len(diags) != 0 {
		t.Fatalf("got %d diagnostics, want none: %v", len(diags), diags)
	}
}

// A parameter is a binding of its own, in a nearer scope, so it is mutable even
// where an imported const shares its name.
func TestAParameterNamedAfterAnImportedConstIsFine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\"\nfn f(HEX: I64) -> I64 {\n  HEX = 2\n  HEX\n}\n")
	if len(diags) != 0 {
		t.Fatalf("got %d diagnostics, want none: %v", len(diags), diags)
	}
}

// So is a local `let` inside a function.
func TestALocalLetShadowingAnImportedConstIsFine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\"\nfn f() {\n  let HEX: I64 = 1\n  HEX = 2\n}\n")
	if len(diags) != 0 {
		t.Fatalf("got %d diagnostics, want none: %v", len(diags), diags)
	}
}

// Reading one is the point of one.
func TestReadingAnImportedConstIsFine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"theme.tw\"\nfn f() -> Str = HEX[0]\n")
	if len(diags) != 0 {
		t.Fatalf("got %d diagnostics, want none: %v", len(diags), diags)
	}
}

// A field of a local record that happens to share an alias's shape is not an
// import, and writing to it is ordinary.
func TestWritingAFieldOfALocalRecordIsFine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	diags := checkFileIn(t, dir, "app.tw", `mode systems
import "theme.tw" as theme
struct Box { HEX: I64 }
fn f() {
  let b: Box = Box { HEX: 1 }
  b.HEX = 2
}
`)
	if len(diags) != 0 {
		t.Fatalf("got %d diagnostics, want none: %v", len(diags), diags)
	}
}
