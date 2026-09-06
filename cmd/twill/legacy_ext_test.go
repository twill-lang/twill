package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rename from Raster to Twill is a hard break on the source extension, not
// a deprecation. A .ra file is refused even when it exists and its contents are
// a perfectly valid program, and the refusal has to name .tw, because the whole
// point of breaking rather than falling back is that the user is told once and
// then has nothing left to migrate.
func TestLegacyExtensionIsRefusedByEveryCommand(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "hello.ra")
	if err := os.WriteFile(legacy, []byte("let x = 1.0\nprint(x)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds := map[string]func(string) int{
		"run":        func(p string) int { return runFile(p, true) },
		"check":      checkOnly,
		"fmt":        func(p string) int { return formatPaths(io.Discard, []string{p}, fmtPrint) },
		"run --dump": func(p string) int { return runFileCanonical(p, true) },
	}

	for name, run := range cmds {
		t.Run(name, func(t *testing.T) {
			code, stderr := captureStderr(t, func() int { return run(legacy) })
			if code == 0 {
				t.Fatalf("%s accepted a .ra file; the extension is meant to be a hard break", name)
			}
			if !strings.Contains(stderr, ".tw") {
				t.Errorf("error must name the new extension, got %q", stderr)
			}
			if !strings.Contains(stderr, ".ra") {
				t.Errorf("error must name the extension it refused, got %q", stderr)
			}
		})
	}
}

// A .ra path is refused on its name alone, so a file that is not even there
// still gets the extension error rather than "cannot read file". The extension
// is the user's actual problem and reporting the missing file would hide it.
func TestLegacyExtensionIsRefusedBeforeReading(t *testing.T) {
	code, stderr := captureStderr(t, func() int {
		return runFile(filepath.Join(t.TempDir(), "absent.ra"), true)
	})
	if code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	if strings.Contains(stderr, "cannot read file") {
		t.Errorf("the extension error should win over the missing-file error, got %q", stderr)
	}
	if !strings.Contains(stderr, ".tw") {
		t.Errorf("error must name the new extension, got %q", stderr)
	}
}

// A .tw file is still accepted, which is what makes the test above a statement
// about the extension rather than about the temp directory.
func TestCurrentExtensionIsAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hello.tw")
	if err := os.WriteFile(path, []byte("let x = 1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stderr := captureStderr(t, func() int { return checkOnly(path) }); code != 0 {
		t.Fatalf("a .tw file was refused: exit %d, %q", code, stderr)
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns fn's
// exit code together with what it wrote.
func captureStderr(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	code := fn()

	os.Stderr = saved
	w.Close()
	out := <-done
	r.Close()
	return code, out
}
