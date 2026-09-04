// Command conformance measures how far the self-hosted implementation of twill
// has actually got, and turns the answer into something a build can fail on.
//
// The repository has two implementations: the Go bootstrap under internal/, and
// twill written in twill under src/. The lexer, parser, checker and formatter
// agree across the fixture corpus. The evaluator does not, and nothing in the
// build said so, because nothing compared them. This tool is that comparison,
// in two modes:
//
//	conformance builtins   probe every name in the shared builtin table on both
//	                       implementations and write docs/conformance.md
//	conformance suites     run the standard library's own test suites under both
//	                       and fail on any disagreement not on the allow-list
//
// Both modes work by running the two implementations rather than by reading
// their source, because a table scraped out of src/eval.tw would be a second
// copy of the dispatch and would rot the first time someone restructured it.
// Running the thing is the only claim that cannot go stale.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "builtins":
		os.Exit(builtinsMain(os.Args[2:]))
	case "suites":
		os.Exit(suitesMain(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: conformance builtins|suites [flags]")
}

// --- shared plumbing --------------------------------------------------------

// stage copies the two things a comparison needs into one scratch directory:
// the self-hosted compiler's own sources, and whatever the case under test
// needs next to it.
//
// The copy is not tidiness. The self-hosted evaluator resolves a path passed to
// `run` relative to the directory of the twill file doing the reading, which is
// src/, while the Go bootstrap resolves it relative to the working directory.
// Handing the two sides different paths would put different text in every
// diagnostic and make the whole corpus look divergent for a reason that is not
// about the language. With src/*.tw and the case in the same directory, both
// sides are given the bare file name, both print the bare file name, and a
// difference in the output is a difference in behaviour.
func stage(root, extraDir string, extraFilter func(string) bool) (string, error) {
	dir, err := os.MkdirTemp("", "twillconf")
	if err != nil {
		return "", err
	}
	if err := copyTree(filepath.Join(root, "src"), dir, func(name string) bool {
		return strings.HasSuffix(name, ".tw")
	}); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if extraDir != "" {
		if err := copyTree(extraDir, dir, extraFilter); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

func copyTree(from, to string, keep func(string) bool) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !keep(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// outcome is everything one run produced. The exit code is part of it: a case
// that fails on both sides for the same reason agrees, and one that fails on
// only one side does not.
type outcome struct {
	stdout   string
	stderr   string
	code     int
	timedOut bool
}

// text is what gets compared. The two streams are kept apart rather than
// merged: the self-hosted CLI writes its diagnostics with write_err while the
// harness prints its verdict with print, so which stream a line came out on is
// itself part of what the two implementations have to agree about, and merging
// them would hide a line that moved from one to the other.
func (o outcome) text() string {
	if o.timedOut {
		return "TIMED OUT"
	}
	return fmt.Sprintf("exit %d\n--- stdout ---\n%s--- stderr ---\n%s", o.code, o.stdout, o.stderr)
}

// out is the two streams together, for the places that only want to read the
// first diagnostic and do not care which one carried it.
func (o outcome) out() string { return o.stdout + o.stderr }

func run(bin, dir string, timeout time.Duration, args ...string) outcome {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	// A fixed environment, so that TWILL_STD cannot swap the standard library
	// under one side of the comparison and not the other.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := outcome{stdout: out.String(), stderr: errb.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.code = exitErr.ExitCode()
	}
	if ctx.Err() != nil {
		res.timedOut = true
	}
	return res
}

// bootstrap and selfHosted are the two ways to run one twill file. They differ
// only in whether the Go evaluator runs the file or runs src/main.tw, which
// runs the file.
func bootstrap(bin, dir, file string, timeout time.Duration, extra ...string) outcome {
	return run(bin, dir, timeout, append([]string{"run", file}, extra...)...)
}

func selfHosted(bin, dir, file string, timeout time.Duration, extra ...string) outcome {
	return run(bin, dir, timeout, append([]string{"run", "main.tw", "run", file}, extra...)...)
}

// firstDifference reports the first line where two outputs part company, which
// is the only part of a diff anyone reads.
func firstDifference(aLabel, a, bLabel, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("line %d\n    %s: %s\n    %s: %s", i+1, aLabel, trim(x), bLabel, trim(y))
		}
	}
	return "they differ only in trailing content"
}

func trim(s string) string {
	s = strings.TrimRight(s, "\r")
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "conformance:", err)
	os.Exit(2)
}
