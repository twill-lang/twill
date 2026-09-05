package interp_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
)

// The self-hosted evaluator does not agree with the bootstrap, and the whole of
// README.md, CONTRIBUTING.md, docs/roadmap.md, docs/BUGS.md and
// docs/CORRECTNESS.md now says so in detail. That is a claim about today, and a
// claim about today rots: when someone closes the gap, the documentation
// becomes wrong in the other direction and nothing says so.
//
// So the divergence is pinned. Each test below asserts that a specific,
// documented disagreement is still real, and fails when it stops being real
// with a message naming the files to update. A failure here is good news about
// the evaluator and a chore for the prose; it is not a regression.
//
// Two runs, not two hundred and forty-eight. The full probe -- generating a
// call for every name in src/builtins.tw and counting how many reach "named in
// the builtin table but has no implementation", which is 128 of 248 -- is done
// by hand and written down in docs/roadmap.md. Repeating it in the suite would
// roughly double the cost of the slowest file in the repository to re-derive a
// number that only moves when someone is already editing src/eval.tw.

// selfHostedCLIOutput runs a program through src/main.tw, the plain self-hosted
// CLI, and returns what the program printed and what the CLI wrote to stderr.
// src/main.tw and not src/cli/main.tw: the decorated CLI never calls a
// systems-mode main(), which is its own documented defect.
func selfHostedCLIOutput(t *testing.T, source string) (printed, stderr string) {
	t.Helper()
	skipUnderShort(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

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
	ip := interp.New(func(s string) { out.WriteString(s) })
	// The self-hosted CLI reports a failing program by writing a diagnostic and
	// returning an exit status, so an error here would be the CLI itself
	// breaking rather than the program failing. Either way the test wants to
	// see the output, so the error is not fatal.
	_, _, _ = ip.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "run", target})

	w.Close()
	os.Stderr = saved
	return out.String(), <-done
}

// goOutput runs the same program on the bootstrap the way `twill run` does.
func goOutput(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	ip := interp.New(func(s string) { out.WriteString(s) })
	if _, _, err := ip.RunFileMain(target, nil); err != nil {
		t.Fatalf("the bootstrap failed to run the program: %v\nsource:\n%s", err, source)
	}
	return out.String()
}

const docsToUpdate = "README.md, CONTRIBUTING.md, docs/roadmap.md " +
	"(\"What the second implementation agrees on, and what it does not\"), " +
	"docs/BUGS.md and docs/CORRECTNESS.md"

// `len` on a `Str` is the smallest of the runtime divergences and the one every
// document quotes, because src/lex.tw depends on it when the bootstrap runs it.
func TestSelfHostedLenOfAStrStillDivergesFromTheBootstrap(t *testing.T) {
	const src = "mode systems\nfn main() {\n  let s: Str = \"abc\"\n  print(str(len(s)))\n}\n"

	if got := goOutput(t, src); got != "3" {
		t.Fatalf("the bootstrap printed %q, want %q; this test's premise is gone", got, "3")
	}
	printed, stderr := selfHostedCLIOutput(t, src)
	if printed == "3" {
		t.Fatalf("the self-hosted evaluator now answers len on a Str, which it did not on 2026-09-04.\n"+
			"That is a fix, not a regression. Update the divergence recorded in %s.", docsToUpdate)
	}
	if !strings.Contains(stderr, "len expects a tensor or list") {
		t.Errorf("self-hosted len failed differently than documented.\nprinted %q\nstderr %q\n"+
			"Update the wording in %s.", printed, stderr, docsToUpdate)
	}
}

// The systems-mode container builtins are the bulk of the 128 and the reason
// src/ cannot run src/: src/main.tw itself stops on dict_new.
func TestSelfHostedStillHasNoSystemsModeContainers(t *testing.T) {
	// The call has to satisfy the checker's fixed-arity table first, or the
	// program never reaches the evaluator at all: buf_new(n) takes one.
	for _, call := range []string{"dict_new()", "arr_new()", "bytes_new()", "buf_new(4)"} {
		name := call[:strings.Index(call, "(")]
		src := "mode systems\nfn main() {\n  let x = " + call + "\n}\n"
		printed, stderr := selfHostedCLIOutput(t, src)
		if !strings.Contains(stderr, "is named in the builtin table but has no implementation") {
			t.Errorf("%s is no longer unimplemented in src/eval.tw.\n"+
				"printed %q\nstderr %q\n"+
				"That is a fix, not a regression: the count of 128 of 248 and the "+
				"claim that src/ cannot run src/ are now stale in %s.",
				name, printed, stderr, docsToUpdate)
		}
	}
}
