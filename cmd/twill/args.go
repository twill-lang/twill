package main

// Argument handling for the commands that take source files.
//
// `twill check` and `twill fmt` took exactly one path, which is not how anyone
// invokes a checker or a formatter: the two things people type are a list of
// files and a directory, and both were a usage error. They take either now, and
// a directory means every .tw file under it.
//
// `twill run` is deliberately not in that set, and the reason is worth writing
// down because it looks like an omission. Everything after the script's path is
// the *program's* arguments -- scriptArgs in main.go hands them to the args
// builtin -- so `twill run main.tw run io_test.tw` runs main.tw with ["run",
// "io_test.tw"], which is exactly how tools/conformance drives the self-hosted
// compiler. A second path and a forwarded argument are the same word, and no
// rule can tell them apart. Forwarding is load-bearing and multi-path run is a
// convenience, so run keeps one path.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// expandSourcePaths turns the paths a caller typed into the list of .tw files to
// work on. A named file is taken as given, whatever it is called; a directory is
// walked for *.tw. Named files keep the order they were named in and a file
// named twice appears once, so a caller feeding a list gets its list back.
//
// A path that does not exist is passed through rather than rejected, so the
// command that opens it reports the missing file itself and the message a
// caller sees for `twill check nope.tw` does not depend on how many paths were
// on the line.
func expandSourcePaths(paths []string) ([]string, error) {
	var files []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			add(p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// A dot-directory is not the caller's source: `twill fmt .` in a
			// checkout must not walk into .git, and whatever is cached under a
			// dot-name was not written by hand.
			if d.IsDir() {
				if name := d.Name(); name != "." && strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(d.Name(), ".tw") {
				add(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// looksLikeAPath reports whether a first argument that is not a command should
// still be treated as a program to run.
//
// The question exists because `twill x.tw` runs x.tw and `twill chekc x.tw`
// used to do the same thing to a file called "chekc": it reported that it could
// not read it, which sends the reader looking for a file that was never meant to
// exist. A word with an extension, or a separator in it, or a file of that name
// on disk, is a path. A bare word that is none of those is a typo, and the CLI
// says so.
func looksLikeAPath(arg string) bool {
	if arg == "" {
		return false
	}
	if _, err := os.Stat(arg); err == nil {
		return true
	}
	if strings.ContainsRune(arg, '/') || strings.ContainsRune(arg, os.PathSeparator) {
		return true
	}
	return filepath.Ext(arg) != ""
}

// unknownFlag returns the first leading-dash word in args that is not in known,
// or "" when they all are. A flag nobody recognises has to be reported rather
// than ignored: silently dropping `--chekc` would defeat the point of having a
// `--check` that fails a build.
func unknownFlag(args []string, known map[string]bool) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := a
		if i := strings.IndexByte(a, '='); i >= 0 {
			name = a[:i]
		}
		if !known[name] {
			return a
		}
	}
	return ""
}

// --- fmt --------------------------------------------------------------------

type fmtMode int

const (
	// fmtPrint writes the formatted source to stdout, which is what `twill fmt
	// <file>` has always done and what the differential harness compares.
	fmtPrint fmtMode = iota
	// fmtWrite rewrites each file in place.
	fmtWrite
	// fmtCheck writes nothing, names every file that would change, and exits
	// non-zero if there is one.
	fmtCheck
)

var fmtFlags = map[string]bool{"--write": true, "-w": true, "--check": true}

// formatPaths formats every file in paths. The exit code is 1 if any file could
// not be read, parsed or written, and, under fmtCheck, if any file would change.
//
// Every file is visited even after one fails. A formatter that stops at the
// first problem makes the caller run it once per broken file to find out how
// many there are.
func formatPaths(w io.Writer, paths []string, mode fmtMode) int {
	code := 0
	changed := 0
	for _, path := range paths {
		out, src, err := formatOne(path)
		if err != nil {
			code = 1
			continue
		}
		switch mode {
		case fmtCheck:
			if out != src {
				changed++
				fmt.Fprintln(w, path)
			}
		case fmtWrite:
			if out == src {
				// Not rewriting an unchanged file keeps its mtime, which is what
				// every build system downstream of a `fmt --write` is watching.
				continue
			}
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "twill: cannot write file %q\n", path)
				code = 1
			}
		case fmtPrint:
			fmt.Fprint(w, out)
		}
	}
	if mode == fmtCheck && changed > 0 {
		fmt.Fprintf(os.Stderr, "twill: %d file(s) would change; run twill fmt --write\n", changed)
		code = 1
	}
	return code
}

// --- check ------------------------------------------------------------------

var checkFlags = map[string]bool{}

// checkPaths shape-checks every file in paths and returns 1 if any of them
// failed. Like formatPaths it visits all of them: the whole value of pointing a
// checker at a directory is one report covering it.
func checkPaths(paths []string) int {
	code := 0
	for _, path := range paths {
		if checkOnly(path) != 0 {
			code = 1
		}
	}
	return code
}
