package interp_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
)

// The run differential. docs/BUGS.md's open section says the thing that would
// have caught its three divergences is "a differential harness over `run`":
// tools/diff compares `check` and `fmt` over the corpus and says nothing about
// what a program prints, so the two evaluators were free to disagree at
// runtime and did. This is that harness, at the size a Go test can hold.
//
// Each fixture under testdata/selfhost is run twice over the same bytes: once
// by the Go bootstrap, and once by the self-hosted evaluator in src/eval.tw
// driven through the self-hosted CLI in src/main.tw, which the bootstrap in
// turn interprets. Stdout is compared byte for byte and so is the runtime
// error line, because a builtin's message is as much of its behaviour as its
// answer -- a port that returns the right value under a different diagnostic
// has not matched the bootstrap.
//
// The fixtures are systems-mode programs with a `main`, since that is the
// dialect the ported names belong to, and they print rather than return so
// that the comparison has something to compare.
func TestSelfHostedRunMatchesBootstrap(t *testing.T) {
	skipUnderShort(t)
	files, err := filepath.Glob(filepath.Join("testdata", "selfhost", "*.tw"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures under testdata/selfhost")
	}
	for _, f := range files {
		f := f
		t.Run(strings.TrimSuffix(filepath.Base(f), ".tw"), func(t *testing.T) {
			// The self-hosted CLI resolves a path relative to its own source
			// directory, so both sides are handed the same absolute path. That
			// also makes the two diagnostic lines comparable, since the path is
			// part of one.
			abs, err := filepath.Abs(f)
			if err != nil {
				t.Fatal(err)
			}
			wantOut, wantDiag := runBootstrapFile(t, abs)
			gotOut, gotDiag := runSelfHostedFile(t, abs)
			if gotOut != wantOut {
				t.Errorf("stdout differs\nbootstrap:\n%s\nself-hosted:\n%s", wantOut, gotOut)
			}
			if gotDiag != wantDiag {
				t.Errorf("diagnostic differs\nbootstrap:   %q\nself-hosted: %q", wantDiag, gotDiag)
			}
		})
	}
}

// runBootstrapFile runs one fixture on the Go interpreter and returns what it
// printed and its runtime error, formatted the way cmd/twill reports one so
// that it can be compared with the self-hosted CLI's first stderr line.
func runBootstrapFile(t *testing.T, path string) (string, string) {
	t.Helper()
	var out strings.Builder
	ip := interp.New(func(s string) { out.WriteString(s); out.WriteString("\n") })
	_, _, err := ip.RunFileMain(path, []string{"twill"})
	return out.String(), formatDiag(path, err)
}

func formatDiag(path string, err error) string {
	if err == nil {
		return ""
	}
	var re *interp.RuntimeError
	if errors.As(err, &re) {
		return fmt.Sprintf("%s:%d: runtime error: %s", path, re.Line, re.Msg)
	}
	return err.Error()
}

// runSelfHostedFile runs one fixture through `src/main.tw run <path>`. The
// checker is off on this side because it is off on the other: runBootstrapFile
// calls the interpreter directly, the way cmd/twill's `run --no-check` does,
// and a fixture whose point is a runtime message the checker also catches
// would otherwise be refused here and evaluated there. What is under
// comparison is the two evaluators.
//
// program's output arrives at the interpreter's sink, since the self-hosted
// print goes through the native emit_line; the CLI's diagnostics go to the
// real stderr through write_err, so they are captured at the OS level the way
// runSelfHostedCheckOut does it. Only the first stderr line is returned: the
// rest is the source excerpt, which is the CLI's rendering rather than the
// evaluator's message.
func runSelfHostedFile(t *testing.T, path string) (string, string) {
	t.Helper()
	saved := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()
	var out strings.Builder
	ip := interp.New(func(s string) { out.WriteString(s); out.WriteString("\n") })
	_, _, runErr := ip.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "run", path, "--no-check"})
	w.Close()
	os.Stderr = saved
	stderr := <-done
	if runErr != nil {
		t.Fatalf("the self-hosted CLI itself failed: %v", runErr)
	}
	return out.String(), firstLine(stderr)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
