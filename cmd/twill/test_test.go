package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSummaryLine(t *testing.T) {
	cases := []struct {
		line      string
		p, f      int
		wantMatch bool
	}{
		{"nn_test passed 52 failed 0", 52, 0, true},
		{"random passed 17 failed 18", 17, 18, true},
		{"  frame_test passed 54 failed 0  ", 54, 0, true},
		{"just some output", 0, 0, false},
		{"passed but no number failed 3", 0, 0, false},
	}
	for _, c := range cases {
		p, f, ok := parseSummaryLine(strings.TrimSpace(c.line))
		if ok != c.wantMatch || (ok && (p != c.p || f != c.f)) {
			t.Errorf("parseSummaryLine(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.line, p, f, ok, c.p, c.f, c.wantMatch)
		}
	}
}

// writeTemp writes a .tw file into a temp dir and returns its path.
func writeTemp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "x_test.tw")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunOneTestFilePasses(t *testing.T) {
	// A file whose harness summary reports zero failures passes.
	res := runOneTestFile(writeTemp(t, "print(\"x passed 3 failed 0\")\nprint(\"OK\")\n"))
	if !res.ok {
		t.Fatalf("expected pass, got fail: %q %q", res.output, res.errMsg)
	}
	if !res.counted || res.checksP != 3 || res.checksF != 0 {
		t.Fatalf("expected counts (3,0), got (%d,%d) counted=%v", res.checksP, res.checksF, res.counted)
	}
}

func TestRunOneTestFileFailsOnMarker(t *testing.T) {
	// A FAILED marker, or a nonzero failed count, is a failing file even though
	// the program itself ran without error.
	res := runOneTestFile(writeTemp(t, "print(\"x passed 2 failed 1\")\nprint(\"FAILED\")\n"))
	if res.ok {
		t.Fatalf("expected fail, got pass")
	}
	if res.checksF != 1 {
		t.Fatalf("expected 1 failed check, got %d", res.checksF)
	}
}

func TestRunOneTestFileFailsOnRuntimeError(t *testing.T) {
	// An out-of-range index aborts; a file that cannot finish is a failing file.
	res := runOneTestFile(writeTemp(t, "let xs = [1.0]\nprint(xs[9])\n"))
	if res.ok {
		t.Fatalf("expected fail on runtime error, got pass")
	}
	if res.errMsg == "" {
		t.Fatalf("expected an error message")
	}
}

func TestRunOneTestFileFailsOnShapeError(t *testing.T) {
	res := runOneTestFile(writeTemp(t, "let a = [1.0, 2.0]\nlet b = [1.0, 2.0, 3.0]\nlet c = a + b\n"))
	if res.ok {
		t.Fatalf("expected fail on shape error, got pass")
	}
	if !strings.Contains(res.errMsg, "shape error") {
		t.Fatalf("expected a shape-error message, got %q", res.errMsg)
	}
}

func TestDiscoverFindsTestFilesRecursively(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a_test.tw", "sub/b_test.tw", "notatest.tw", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("print(1)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := discoverTestFiles([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 *_test.tw files, got %d: %v", len(files), files)
	}
}

// The numeric-mode standard-library suites are the regression guard: they run on
// the bootstrap as-is and must all pass. The systems-mode suites (io, json,
// random, ...) are excluded here because some are written ahead of the language
// -- random needs true 64-bit integers the f64 bootstrap does not have -- and
// their status is tracked separately, not as a regression. See std/tests/README.
func TestNumericStdSuitesPass(t *testing.T) {
	root := filepath.Join("..", "..", "std", "tests")
	files, err := discoverTestFiles([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(strings.TrimSpace(firstLine(string(src))), "mode systems") {
			continue // systems-mode suite: best-effort, guarded elsewhere
		}
		ran++
		res := runOneTestFile(f)
		if !res.ok {
			t.Errorf("%s failed:\n%s\n%s", filepath.Base(f), res.output, res.errMsg)
		}
	}
	if ran == 0 {
		t.Fatal("found no numeric std test suites to run")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// --- std/test, the other half of the runner ---------------------------------
//
// `twill test` reads a file's verdict out of what the file printed, so the
// harness contract has been part of the toolchain's contract since 1.8 while
// the toolchain shipped nothing that satisfied it. `std/test` is what satisfies
// it, and these are the tests that the two halves actually meet: a suite
// written against std/test is run through the same runOneTestFile the CLI uses,
// and the verdict and the counts have to come back right.
//
// Asserting this from inside a twill suite is not possible -- a suite cannot
// record a failing check and still report itself green -- which is why the
// negative case lives here in Go, where the failure is the fixture's and the
// assertion is the harness's.

const stdTestMixedFixture = `mode systems

import "std/test" as t

fn main() -> I64 {
  t.check("a true condition passes", 1 == 1)
  t.equal_str("two equal strings pass", "ab", "ab")
  t.equal_i64("two equal integers pass", 4, 4)
  t.near("a difference exactly at the tolerance passes", 1.0, 1.5, 0.5)
  t.near("a tolerance of zero demands exact equality", 2.0, 2.0, 0.0)
  t.check("a false condition fails", 1 == 2)
  t.fail("fail records why", "the reason it failed")
  t.equal_str("unequal strings fail", "got", "want")
  t.equal_i64("unequal integers fail", 3, 4)
  t.near("a difference past the tolerance fails", 1.0, 2.0, 0.5)
  t.report("fixture")
}
`

func TestStdTestReportsFailuresToTheRunner(t *testing.T) {
	res := runOneTestFile(writeTemp(t, stdTestMixedFixture))
	if res.ok {
		t.Fatalf("a suite with five failing checks was reported as passing:\n%s", res.output)
	}
	if !res.counted {
		t.Fatalf("the runner found no summary line in:\n%s", res.output)
	}
	if res.checksP != 5 || res.checksF != 5 {
		t.Fatalf("counts are (%d passed, %d failed), want (5, 5):\n%s",
			res.checksP, res.checksF, res.output)
	}
	if !strings.Contains(res.output, "\nFAILED\n") {
		t.Errorf("the FAILED marker is not on a line of its own:\n%s", res.output)
	}
	// Each failing form has to say enough to diagnose it without opening the
	// file. `fail` keeps the reason, which is the whole of why it exists, and
	// the two equality forms show both sides.
	for _, want := range []string{
		"  FAIL  a false condition fails",
		"        the reason it failed",
		"        got:  got",
		"        want: want",
		"        got:  3",
		"        want: 4",
		"        want: 2 within 0.5",
	} {
		if !strings.Contains(res.output, want) {
			t.Errorf("the output does not carry %q:\n%s", want, res.output)
		}
	}
}

func TestStdTestReportsAPassingSuiteToTheRunner(t *testing.T) {
	res := runOneTestFile(writeTemp(t, `mode systems

import "std/test" as t

fn main() -> I64 {
  t.check("a true condition passes", 1 == 1)
  t.equal_str("two equal strings pass", "ab", "ab")
  t.equal_i64("two equal integers pass", 4, 4)
  t.near("a difference inside the tolerance passes", 1.0, 1.25, 0.5)
  t.report("fixture")
}
`))
	if !res.ok {
		t.Fatalf("a suite whose checks all pass was reported as failing:\n%s\n%s",
			res.output, res.errMsg)
	}
	if !res.counted || res.checksP != 4 || res.checksF != 0 {
		t.Fatalf("counts are (%d passed, %d failed) counted=%v, want (4, 0) counted=true:\n%s",
			res.checksP, res.checksF, res.counted, res.output)
	}
	if !strings.Contains(res.output, "\nOK\n") {
		t.Errorf("a green suite must end with the OK marker:\n%s", res.output)
	}
	// A passing suite prints its summary and nothing else. Six hundred `ok`
	// lines is how a harness trains people to stop reading its output.
	if strings.Contains(res.output, "FAIL") {
		t.Errorf("a green suite printed a FAIL line:\n%s", res.output)
	}
}

// The systems-mode suites in std/tests are excluded from TestNumericStdSuitesPass
// above, so until now nothing in Go asserted that any of them ran. They are the
// adopters of std/test, and all six pass on the bootstrap; this pins them, so a
// change to std/test cannot break them silently. std/tests/README said two of
// them failed, which was not true of the tree either before this change or
// after it -- measured, not reworded.
func TestSystemsStdSuitesPass(t *testing.T) {
	for _, name := range []string{
		"io_test.tw", "json_test.tw", "linalg_test.tw",
		"random_test.tw", "stats_test.tw", "text_test.tw",
	} {
		name := name
		t.Run(name, func(t *testing.T) {
			res := runOneTestFile(filepath.Join("..", "..", "std", "tests", name))
			if !res.ok {
				t.Errorf("%s failed:\n%s\n%s", name, res.output, res.errMsg)
			}
		})
	}
}
