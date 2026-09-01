package interp_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `run` is spool docs/needs.md entry 1: the last open entry on that list, and
// the one thing between spool and fetching a package. These tests are written
// against the four things the signature promises -- stdout on a clean exit, the
// reason on anything else, a working directory that means what the rest of the
// runtime means by a path, and no shell anywhere -- rather than against a
// particular program's output, so they do not depend on which version of git or
// coreutils this machine has.
//
// Every command used here is a POSIX shell builtin or /bin utility, and the
// whole file is skipped on Windows, where none of them exist under these names.

func skipWithoutPOSIXTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the commands these tests run are POSIX utilities")
	}
}

func TestRunReportsStdoutOnACleanExit(t *testing.T) {
	skipWithoutPOSIXTools(t)
	dir := t.TempDir()
	out := runFile(t, dir, `mode systems
fn main() {
  match run("echo", ["hello", "world"], "") {
    Ok(s) => print("ok:" + s),
    Err(e) => print("err:" + e),
  }
}
`)
	// echo's trailing newline is stdout and is not trimmed: a caller parsing
	// `git rev-list` needs the bytes the program wrote, not a tidied version.
	expectLines(t, out, "ok:hello world\n")
}

// A non-zero exit is an Err even though the program ran, and stderr is the
// message. Merging stderr into Ok would splice a warning line into output a
// caller is about to parse; dropping it would leave the caller with an exit
// code and no reason.
func TestRunReportsANonZeroExitAsAnErrorCarryingStderr(t *testing.T) {
	skipWithoutPOSIXTools(t)
	dir := t.TempDir()
	out := runFile(t, dir, `mode systems
fn main() {
  match run("sh", ["-c", "echo out; echo why 1>&2; exit 3"], "") {
    Ok(s) => print("BAD ok:" + s),
    Err(e) => print("err:" + e),
  }
}
`)
	if len(out) != 1 || !strings.HasPrefix(out[0], "err:") {
		t.Fatalf("output = %q, want a single Err line", out)
	}
	// The three things a caller debugs with: which program, how it ended, and
	// what it said.
	for _, want := range []string{"sh", "3", "why"} {
		if !strings.Contains(out[0], want) {
			t.Errorf("Err message %q does not mention %q", out[0], want)
		}
	}
	if strings.Contains(out[0], "out") && !strings.Contains(out[0], "without") {
		t.Errorf("Err message %q leaked stdout into the failure", out[0])
	}
}

func TestRunReportsAProgramThatCannotStart(t *testing.T) {
	dir := t.TempDir()
	out := runFile(t, dir, `mode systems
fn main() {
  match run("twill-no-such-program-exists", [], "") {
    Ok(s) => print("BAD ok:" + s),
    Err(e) => print("err:" + e),
  }
  # An empty name is its own answer rather than a lookup failure nobody can read.
  match run("", [], "") {
    Ok(s) => print("BAD ok:" + s),
    Err(e) => print("err:" + e),
  }
}
`)
	if len(out) != 2 {
		t.Fatalf("output = %q, want two Err lines", out)
	}
	if !strings.Contains(out[0], "twill-no-such-program-exists") {
		t.Errorf("Err message %q does not name the program", out[0])
	}
	if !strings.Contains(out[1], "no program named") {
		t.Errorf("empty-name Err = %q, want it to say no program was named", out[1])
	}
}

// The working directory follows the rule read_file and write_file already
// follow, so a caller does not have to learn a second one: absolute is
// absolute, relative is relative to the running file, and empty is that file's
// directory. spool needs this to run git inside a checkout it just made.
func TestRunResolvesItsWorkingDirectoryLikeEveryOtherPath(t *testing.T) {
	skipWithoutPOSIXTools(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runFile(t, dir, `mode systems
fn main() {
  # ls in the relative directory sees the file that is in it.
  match run("ls", [], "sub") {
    Ok(s) => print("rel:" + s),
    Err(e) => print("err:" + e),
  }
  # The same command with no directory runs beside the program, where there is
  # a main.tw and no marker.
  match run("ls", ["sub"], "") {
    Ok(s) => print("base:" + s),
    Err(e) => print("err:" + e),
  }
}
`)
	expectLines(t, out, "rel:marker.txt\n", "base:marker.txt\n")
}

// TWILL_NO_EXEC is the off switch spool's entry asked for when it said this
// widening should be a considered decision. It refuses rather than aborts, so a
// program can degrade to whatever it can still do.
func TestRunRefusesEverythingWhenTheOffSwitchIsSet(t *testing.T) {
	skipWithoutPOSIXTools(t)
	t.Setenv("TWILL_NO_EXEC", "1")
	dir := t.TempDir()
	out := runFile(t, dir, `mode systems
fn main() {
  match run("echo", ["hi"], "") {
    Ok(s) => print("BAD ok:" + s),
    Err(e) => print("err:" + e),
  }
}
`)
	if len(out) != 1 || !strings.Contains(out[0], "TWILL_NO_EXEC") {
		t.Fatalf("output = %q, want an Err naming TWILL_NO_EXEC", out)
	}
}

// THE ONE THAT MATTERS.
//
// A package manager's arguments are branch names, tags and URLs out of a
// manifest a stranger wrote. `run` takes an argument vector and hands it to
// execve, so an argument containing shell metacharacters is one argument and
// nothing else. If a convenience form taking a single command string is ever
// added, this test is the thing that should stop it: it asserts that a
// semicolon in an argument reaches the program as text.
func TestRunNeverInterpretsAnArgumentAsAShellCommand(t *testing.T) {
	skipWithoutPOSIXTools(t)
	dir := t.TempDir()
	canary := filepath.Join(dir, "pwned.txt")
	out := runFile(t, dir, `mode systems
fn main() {
  # If anything on this path reached a shell, the second half runs and the
  # canary appears. It reaches echo as one argument instead.
  match run("echo", ["v1.0; touch `+canary+`"], "") {
    Ok(s) => print("ok:" + s),
    Err(e) => print("err:" + e),
  }
}
`)
	expectLines(t, out, "ok:v1.0; touch "+canary+"\n")
	if _, err := os.Stat(canary); err == nil {
		t.Fatal("an argument was interpreted by a shell: the canary file exists")
	}
}
