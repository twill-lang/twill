package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What the CLI does with the arguments people actually type: several paths, a
// directory, and a subcommand spelled wrong.

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExpandSourcePathsTakesFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, filepath.Join(dir, "a.tw"), "let a = 1\n")
	b := writeFile(t, filepath.Join(dir, "sub", "b.tw"), "let b = 2\n")
	writeFile(t, filepath.Join(dir, "sub", "notes.md"), "not source\n")
	// A dot-directory is skipped: `twill fmt .` in a checkout must not walk
	// into .git, and a vendored or cached tree under a dot-name is not the
	// caller's source either.
	writeFile(t, filepath.Join(dir, ".cache", "c.tw"), "let c = 3\n")

	got, err := expandSourcePaths([]string{dir})
	if err != nil {
		t.Fatalf("expandSourcePaths: %v", err)
	}
	want := []string{a, b}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("expandSourcePaths(dir) = %q, want %q", got, want)
	}
}

func TestExpandSourcePathsKeepsExplicitOrderAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, filepath.Join(dir, "a.tw"), "let a = 1\n")
	z := writeFile(t, filepath.Join(dir, "z.tw"), "let z = 1\n")

	// Named files keep the order they were named in, which is what a caller
	// piping a list expects, and a file named twice is formatted once.
	got, err := expandSourcePaths([]string{z, a, z})
	if err != nil {
		t.Fatalf("expandSourcePaths: %v", err)
	}
	if want := []string{z, a}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("expandSourcePaths = %q, want %q", got, want)
	}
}

// A path that is not there is passed through rather than rejected here, so the
// command that reads it reports the missing file the way it always has.
func TestExpandSourcePathsPassesThroughAMissingPath(t *testing.T) {
	got, err := expandSourcePaths([]string{"no/such/file.tw"})
	if err != nil {
		t.Fatalf("expandSourcePaths: %v", err)
	}
	if len(got) != 1 || got[0] != "no/such/file.tw" {
		t.Errorf("expandSourcePaths = %q, want the path back unchanged", got)
	}
}

// `twill chekc x.tw` used to try to run a program called "chekc" and report
// that it could not read the file, which sends the reader looking for a file
// that was never meant to exist. A word that is not a command, has no
// extension, is not a path and is not on disk is a typo, and saying so is the
// whole of the fix.
func TestLooksLikeAPathSeparatesTyposFromFiles(t *testing.T) {
	dir := t.TempDir()
	onDisk := writeFile(t, filepath.Join(dir, "prog"), "let a = 1\n")

	for _, tc := range []struct {
		arg  string
		want bool
	}{
		{"prog.tw", true}, // an extension makes it a file
		{"a/b", true},     // a separator makes it a path
		{"./prog", true},  // ...including a leading ./
		{onDisk, true},    // it exists, so it is whatever it is
		{"chekc", false},  // a typo
		{"build", false},  // a command this binary does not have
		{"", false},       // nothing at all
	} {
		if got := looksLikeAPath(tc.arg); got != tc.want {
			t.Errorf("looksLikeAPath(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

func TestUnknownFlagsAreNamed(t *testing.T) {
	if got := unknownFlag([]string{"fmt", "x.tw", "--write"}, fmtFlags); got != "" {
		t.Errorf("unknownFlag over known flags = %q, want none", got)
	}
	if got := unknownFlag([]string{"fmt", "x.tw", "--chekc"}, fmtFlags); got != "--chekc" {
		t.Errorf("unknownFlag = %q, want %q", got, "--chekc")
	}
	// The short spelling is a flag this command knows.
	if got := unknownFlag([]string{"fmt", "-w"}, fmtFlags); got != "" {
		t.Errorf("unknownFlag(-w) = %q, want none", got)
	}
}

// `twill fmt --check` is the CI gate: it changes nothing, names every file that
// would change, and exits non-zero when there is one. Exit 0 with no output is
// the only way to pass.
func TestFmtCheckNamesWhatWouldChangeAndFails(t *testing.T) {
	dir := t.TempDir()
	clean := writeFile(t, filepath.Join(dir, "clean.tw"), "let a = 1\n")
	dirty := writeFile(t, filepath.Join(dir, "dirty.tw"), "let   a   =   1\n")

	var out strings.Builder
	if code := formatPaths(&out, []string{clean}, fmtCheck); code != 0 {
		t.Errorf("--check over a formatted file = %d, want 0", code)
	}
	if out.String() != "" {
		t.Errorf("--check over a formatted file printed %q, want nothing", out.String())
	}

	out.Reset()
	code := formatPaths(&out, []string{dirty, clean}, fmtCheck)
	if code == 0 {
		t.Errorf("--check over an unformatted file = 0, want non-zero")
	}
	if !strings.Contains(out.String(), dirty) {
		t.Errorf("--check output = %q, want it to name %q", out.String(), dirty)
	}
	if strings.Contains(out.String(), clean) {
		t.Errorf("--check output = %q, want it to leave the formatted file out", out.String())
	}
	// It must not have written anything.
	if b, _ := os.ReadFile(dirty); string(b) != "let   a   =   1\n" {
		t.Errorf("--check rewrote the file: %q", string(b))
	}
}

// The blank-line rule is the reason --check can be believed at all: every .tw
// file in the repository failed it while the Go printer deleted paragraph
// breaks, so the gate would have had nothing to say but "everything".
func TestFmtCheckPassesAFileWithParagraphBreaks(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, filepath.Join(dir, "para.tw"), "let a = 1\n\nlet b = 2\n")
	var out strings.Builder
	if code := formatPaths(&out, []string{f}, fmtCheck); code != 0 {
		t.Errorf("--check = %d (%q), want 0", code, out.String())
	}
}

func TestFmtWriteTakesSeveralFiles(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, filepath.Join(dir, "a.tw"), "let   a   =   1\n")
	b := writeFile(t, filepath.Join(dir, "sub", "b.tw"), "let   b   =   2\n")

	var out strings.Builder
	if code := formatPaths(&out, mustExpand(t, []string{a, filepath.Join(dir, "sub")}), fmtWrite); code != 0 {
		t.Fatalf("fmt --write = %d (%q), want 0", code, out.String())
	}
	for _, f := range []string{a, b} {
		got, _ := os.ReadFile(f)
		if strings.Contains(string(got), "   ") {
			t.Errorf("%s was not formatted: %q", f, string(got))
		}
	}
}

func mustExpand(t *testing.T, paths []string) []string {
	t.Helper()
	got, err := expandSourcePaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// `twill check` over several paths reports on each and fails if any one of them
// does, rather than stopping at the first.
func TestCheckPathsVisitsEveryFileAndFailsIfAnyDoes(t *testing.T) {
	dir := t.TempDir()
	ok1 := writeFile(t, filepath.Join(dir, "ok1.tw"), "let a = zeros(2, 3)\n")
	ok2 := writeFile(t, filepath.Join(dir, "ok2.tw"), "let b = zeros(4)\n")
	bad := writeFile(t, filepath.Join(dir, "bad.tw"), "let c = zeros(2, 3) + zeros(4)\n")

	if code := checkPaths([]string{ok1, ok2}); code != 0 {
		t.Errorf("check over two clean files = %d, want 0", code)
	}
	if code := checkPaths([]string{ok1, bad, ok2}); code != 1 {
		t.Errorf("check with one bad file = %d, want 1", code)
	}
}

// An error that carries no position in the file still has to say which file it
// came from. One invocation used to mean one file and the caller knew; over a
// directory it does not. `twill fmt --check .` in this repository refuses ten
// files whose comments the formatter will not move, and the ten lines it wrote
// were identical and named none of them.
func TestFormatRefusalNamesItsFile(t *testing.T) {
	dir := t.TempDir()
	// A comment inside a multi-line list literal is one the formatter refuses
	// to move rather than silently drop.
	refusing := writeFile(t, filepath.Join(dir, "refuses.tw"), "let a = [\n  1,  # one\n  2,  # two\n]\n")
	clean := writeFile(t, filepath.Join(dir, "clean.tw"), "let b = 1\n")

	code, stderr := captureStderr(t, func() int {
		return formatPaths(io.Discard, []string{refusing, clean}, fmtCheck)
	})
	if code != 1 {
		t.Errorf("fmt --check over a file it refuses = %d, want 1", code)
	}
	want := refusing + ": error: cannot format: "
	if !strings.HasPrefix(stderr, want) {
		t.Errorf("refusal = %q, want it to start with %q", stderr, want)
	}
	if strings.Contains(stderr, clean) {
		t.Errorf("refusal = %q, want it to name only the file it refused", stderr)
	}
}

// The two positioned forms keep the file, the line and (for a syntax error) the
// column they have always had: the fix above adds a file to the third form, it
// does not restyle the two that had one.
func TestPositionedErrorsKeepTheirFileAndLine(t *testing.T) {
	dir := t.TempDir()
	bad := writeFile(t, filepath.Join(dir, "bad.tw"), "let a = (\n")

	code, stderr := captureStderr(t, func() int { return checkPaths([]string{bad}) })
	if code != 1 {
		t.Errorf("check over a file that does not parse = %d, want 1", code)
	}
	if !strings.HasPrefix(stderr, bad+":") {
		t.Errorf("syntax error = %q, want it to start with %q", stderr, bad+":")
	}
	if !strings.Contains(stderr, "syntax error:") {
		t.Errorf("syntax error = %q, want it to say so", stderr)
	}
}
