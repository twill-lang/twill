package main

import "testing"

// The probe reads its verdict out of a diagnostic, and both implementations
// write diagnostics to stderr and the program's own output to stdout. Reading
// the first line of the two streams together made a builtin that prints before
// it fails look like a builtin the probe could not reach.
func TestAProbeThatPrintsBeforeItFailsIsStillAttributed(t *testing.T) {
	o := outcome{stdout: "1\n", stderr: "probe_len.tw:1: runtime error: len expects a tensor or list\n", code: 1}
	got, ev := classifySelf(o, "len", "probe_len.tw")
	if got != present {
		t.Errorf("classifySelf = %v (%s), want present", got, ev)
	}
}

// A probe of one name can make the evaluator reach for another builtin it has
// not implemented. That message is an answer about the other name.
func TestAMissingMessageAboutAnotherNameIsNotAnAnswer(t *testing.T) {
	const other = `probe_foo.tw:1: runtime error: builtin "arr_new" is named in the builtin table but has no implementation`
	if got, _ := classifySelf(outcome{stderr: other + "\n", code: 1}, "foo", "probe_foo.tw"); got == missing {
		t.Error("foo was recorded as unimplemented on the strength of a message about arr_new")
	}
	const own = `probe_foo.tw:1: runtime error: builtin "foo" is named in the builtin table but has no implementation`
	if got, _ := classifySelf(outcome{stderr: own + "\n", code: 1}, "foo", "probe_foo.tw"); got != missing {
		t.Errorf("classifySelf = %v, want missing", got)
	}
}

func TestBootMissingIsNamedToo(t *testing.T) {
	const other = `probe_foo.tw:1: runtime error: undefined variable "bar"`
	if got, _ := classifyBoot(outcome{stderr: other + "\n", code: 1}, "foo", "probe_foo.tw"); got == missing {
		t.Error("foo was recorded as unbound on the strength of a message about bar")
	}
}

// A fault inside src/*.tw is the self-hosted implementation falling over on the
// way to the call, which is a different fact from the one being asked about.
func TestAFaultInsideTheCompilerIsInconclusive(t *testing.T) {
	o := outcome{stderr: "main.tw:2425: runtime error: no match arm for {shape: []}\n", code: 1}
	if got, _ := classifySelf(o, "clip", "probe_clip.tw"); got != unknown {
		t.Errorf("classifySelf = %v, want unknown", got)
	}
}

// Nothing that reaches the generated document may carry a byte that turns it
// into a binary file: a tensor's raw elements arrive inside a diagnostic.
func TestCellEscapesControlBytes(t *testing.T) {
	got := cell("data: \x00\x00, dtype: 6")
	if want := `data: \x00\x00, dtype: 6`; got != want {
		t.Errorf("cell = %q, want %q", got, want)
	}
}
