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

// How deep the walk goes, and that it goes exactly as deep as the self-hosted
// one.
//
// The bound is a file count, and the two checkers held two different ones: this
// walk followed nine files and `src/check.tw` stopped at eight, while a comment
// there said the two agreed. A chain of nine -- app.tw through eight modules
// into a theme.tw declaring `const HEX` -- was refused here and called clean
// there, which is the one thing a rule spread across two implementations may
// not do. Neither the 428-file differential sweep nor any test could see it,
// because nothing in the ecosystem imports nine deep, so the chain is built
// here rather than looked for.
//
// chainInto writes theme.tw and files enough to reach it through `files`
// imported files in total, and returns the app that imports the head of the
// chain.
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

func TestAnImportedConstNineFilesDeepIsRefused(t *testing.T) {
	dir := t.TempDir()
	app := chainInto(t, dir, 9)
	diags := checkFileIn(t, dir, "app.tw", app)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, `HEX is declared const in "theme.tw" on line 2`) {
		t.Errorf("unexpected message: %s", diags[0].Msg)
	}
}

// The far side of the same bound. Ten files is past it and the const is not
// found, which is not a compromise: a walk with no bound is a directory
// traversal, and the point of pinning the tenth is that both checkers stop
// there rather than one of them stopping first.
func TestTheImportWalkStopsPastNineFiles(t *testing.T) {
	dir := t.TempDir()
	app := chainInto(t, dir, 10)
	diags := checkFileIn(t, dir, "app.tw", app)
	if len(diags) != 0 {
		t.Fatalf("got %d diagnostics, want none past the bound: %v", len(diags), diags)
	}
}

// An aliased import of a file must not blind a later plain import of it.
//
// The walk drops what it collects under an alias inside an imported file --
// those names would be written `mid.theme.HEX` here, which no rule reads -- and
// it used to mark the file visited on the way, in the cycle guard the plain
// branch shares. So `mid.tw` importing `theme.tw` both ways left the plain
// import collecting nothing, and `HEX = 2` in the importer was accepted here
// and refused by the self-hosted checker, which does not follow an aliased
// import at all. The aliased branch now walks under a copy of the guard.
func TestAnAliasedImportDoesNotHideALaterPlainOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", themeModule)
	writeFile(t, dir, "mid.tw", "mode systems\nimport \"theme.tw\" as t\nimport \"theme.tw\"\n")
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"mid.tw\"\nHEX = arr_new()\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, `HEX is declared const in "theme.tw" on line 2`) {
		t.Errorf("unexpected message: %s", diags[0].Msg)
	}
}

// A file that imports itself under an alias is a cycle through the branch that
// gets its own copy of the guard, and the copy carries every file on the way
// in, so it stops. Without that this does not return.
func TestAnAliasedSelfImportTerminates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.tw", "mode systems\nimport \"a.tw\" as x\nconst HEX: I64 = 1\n")
	diags := checkFileIn(t, dir, "app.tw", "mode systems\nimport \"a.tw\"\nHEX = 2\n")
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(diags), diags)
	}
}

// The field write, which is the shape the self-hosted checker briefly lost when
// its walk was made lazy: an alias map created for every name asked about
// answered for `HEX` too, and a field write on a plain-imported const stopped
// there instead of reaching the map that has it. Both spellings are pinned
// here, and their exit codes are compared against the self-hosted checker's in
// internal/interp/selfhost_test.go.
func TestWritingAFieldOfAnImportedConstIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "theme.tw", "mode systems\nstruct Box { f: I64 }\nconst REC: Box = Box { f: 1 }\n")
	for _, app := range []string{
		"mode systems\nimport \"theme.tw\"\nREC.f = 2\n",
		"mode systems\nimport \"theme.tw\" as t\nt.REC.f = 2\n",
	} {
		diags := checkFileIn(t, dir, "app.tw", app)
		if len(diags) != 1 {
			t.Fatalf("got %d diagnostics for %q, want 1: %v", len(diags), app, diags)
		}
		if !strings.Contains(diags[0].Msg, "not the binding, and not an element or field of it") {
			t.Errorf("unexpected message: %s", diags[0].Msg)
		}
	}
}
