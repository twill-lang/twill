package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A line on the golden allow-list excuses one kind of finding against one
// fixture. The kind is the point: the list has to keep covering a golden that
// is still stale and stop covering a fixture that has stopped running.
func TestAllowedKindIsChecked(t *testing.T) {
	fixture := filepath.FromSlash("testdata/examples/mlp.tw")
	rep := &report{allowed: map[string]string{fixture: "golden-mismatch"}}

	rep.add("golden mismatch", fixture, "line 15")
	if n := rep.divergences(); n != 0 {
		t.Errorf("the finding the line records was not excused: %d divergence(s)", n)
	}

	rep.add("missing golden", fixture, "no recorded output")
	if n := rep.divergences(); n != 1 {
		t.Errorf("a golden that has gone missing was excused by a stale-golden line: %d divergence(s)", n)
	}
}

func TestReadAllowRefusesAPathWithNoKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allow.txt")
	if err := os.WriteFile(path, []byte("# comment\ntestdata/examples/mlp.tw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAllow(path); err == nil {
		t.Fatal("a bare path was accepted; that is the old format, and it excuses the fixture whatever happens to it")
	}
}

func TestReadAllowRefusesADuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allow.txt")
	body := "a.tw golden-mismatch\na.tw error\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAllow(path); err == nil {
		t.Fatal("two lines for one fixture were accepted; the second silently wins")
	}
}

// Nondeterministic and skipped stay informational: the first is a fixture that
// disagrees with itself and the second is one the harness had no way to run,
// and neither says the two binaries are a different language.
func TestNondeterminismIsNotADivergence(t *testing.T) {
	rep := &report{}
	rep.add("nondeterministic", "a.tw", "twice, differently")
	rep.add("skipped", "b.tw", "no way to run it")
	if n := rep.divergences(); n != 0 {
		t.Errorf("divergences = %d, want 0", n)
	}
}
