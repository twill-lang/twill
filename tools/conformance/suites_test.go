package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A signature has to survive the things that move without the divergence
// moving, and has to change when the divergence does. These are the two
// properties the allow-list rests on, so they are tested rather than asserted
// in a comment.
func TestDivergenceKeyIgnoresSourceLineNumbers(t *testing.T) {
	at := func(line string) outcome {
		return outcome{stderr: line + "\n", code: 1}
	}
	ok := outcome{stdout: "text passed 80 failed 0\n"}
	a := divergenceKey(ok, at(`main.tw:2425: runtime error: no match arm for {shape: [], dtype: 6}`))
	b := divergenceKey(ok, at(`main.tw:2600: runtime error: no match arm for {shape: [], dtype: 6}`))
	if a != b {
		t.Errorf("moving the fault's line in eval.tw changed the signature:\n%s\n\n%s", a, b)
	}
	c := divergenceKey(ok, at(`main.tw:2425: runtime error: no match arm for {shape: [2], dtype: 6}`))
	if a == c {
		t.Errorf("a different value in the message left the signature alone:\n%s", a)
	}
}

func TestDivergenceKeyEscapesControlBytes(t *testing.T) {
	// The self-hosted runtime quotes a tensor's raw bytes into its diagnostics,
	// so the line this is built from contains NULs.
	key := divergenceKey(outcome{}, outcome{stderr: "case.tw:1: data: \x00\x00\x01\n", code: 1})
	if strings.ContainsAny(key, "\x00\x01") {
		t.Errorf("signature key carries raw control bytes: %q", key)
	}
	if !strings.Contains(key, `\x00\x00\x01`) {
		t.Errorf("signature key lost the evidence it escaped: %q", key)
	}
}

// A run that hit the budget is keyed on that alone: how far it got first is a
// property of the machine, and two suites that both time out are the same fact
// about the self-hosted evaluator.
func TestDivergenceKeyOfATimeoutIgnoresHowFarItGot(t *testing.T) {
	boot := outcome{stdout: "line one\nline two\n"}
	near := outcome{stdout: "line one\n", timedOut: true}
	far := outcome{stdout: "line one\nline two\nline three\n", timedOut: true}
	if divergenceKey(boot, near) != divergenceKey(boot, far) {
		t.Error("the timeout signature depends on how much output the run managed first")
	}
}

func TestReadAllowRefusesABareName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allow.txt")
	if err := os.WriteFile(path, []byte("# comment\nio_test.tw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAllow(path); err == nil {
		t.Fatal("a name with no signature was accepted; that is the old format, and it excuses the whole suite")
	}
}

func TestReadAllowRefusesADuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allow.txt")
	body := "io_test.tw aaaaaaaaaaaa\nio_test.tw bbbbbbbbbbbb\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAllow(path); err == nil {
		t.Fatal("two lines for one suite were accepted; the second silently wins")
	}
}

// The four ways the gate says no, and the one way it says yes.
func TestVerdict(t *testing.T) {
	sig := "aaaaaaaaaaaa"
	listed := map[string]allowEntry{"known_test.tw": {signature: sig, line: 1}}
	for _, tc := range []struct {
		name string
		r    suiteResult
		want int
	}{
		{"a known divergence passes", suiteResult{name: "known_test.tw", signature: sig}, 0},
		{"a changed divergence fails", suiteResult{name: "known_test.tw", signature: "bbbbbbbbbbbb"}, 1},
		{"an unlisted divergence fails", suiteResult{name: "new_test.tw", signature: "cccccccccccc"}, 1},
		{"a listed suite that agrees fails", suiteResult{name: "known_test.tw", agreed: true}, 1},
		{"a suite with no measurement fails", suiteResult{name: "known_test.tw", unusable: true}, 1},
		{"an agreeing suite passes", suiteResult{name: "other_test.tw", agreed: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			all := map[string]allowEntry{}
			for k, v := range listed {
				all[k] = v
			}
			if tc.r.name != "known_test.tw" {
				// The known entry is still on the list and still has to be
				// accounted for, so give it a result of its own.
				got := verdict([]suiteResult{tc.r, {name: "known_test.tw", signature: sig}}, all, "allow.txt")
				if got != tc.want {
					t.Errorf("verdict = %d, want %d", got, tc.want)
				}
				return
			}
			if got := verdict([]suiteResult{tc.r}, all, "allow.txt"); got != tc.want {
				t.Errorf("verdict = %d, want %d", got, tc.want)
			}
		})
	}
}

// A name on the list that is not a suite is a line nobody deleted.
func TestVerdictFailsOnAGhostEntry(t *testing.T) {
	listed := map[string]allowEntry{"ghost_test.tw": {signature: "aaaaaaaaaaaa", line: 1}}
	if got := verdict([]suiteResult{{name: "real_test.tw", agreed: true}}, listed, "allow.txt"); got != 1 {
		t.Errorf("verdict = %d, want 1", got)
	}
}
