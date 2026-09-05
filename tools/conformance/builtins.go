package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// The self-hosted evaluator's own words for a name it was asked to dispatch and
// could not. src/eval.tw ends its dispatch chain with this message rather than
// returning unit, which is what makes the gap measurable from outside. The name
// is captured, not just matched: probing `foo` can make the evaluator reach for
// some other unimplemented builtin on its own account, and reading that as an
// answer about `foo` would be a wrong row nobody could see was wrong.
var selfMissing = regexp.MustCompile(`builtin "([^"]+)" is named in the builtin table but has no implementation`)

// The Go bootstrap has no such fallback: a name it does not register is simply
// not bound, and evaluating it is an undefined variable.
var bootMissing = regexp.MustCompile(`undefined variable "([^"]+)"`)

// An implemented builtin called with the wrong number of arguments complains
// about the count before it does anything else. That answer is not the one this
// tool is asking for, so the probe tries again with more arguments.
var arityComplaint = regexp.MustCompile(`expects \d+ argument\(s\), got \d+`)

// namesRE pulls the shared table out of src/builtins.tw. There is exactly one
// such declaration and the file exists to hold it, so a regexp is honest here
// in a way that scraping a dispatch would not be.
var namesRE = regexp.MustCompile(`(?m)^let NAMES: Str = "([^"]*)"`)

type support int

const (
	unknown support = iota
	present
	missing
)

func (s support) String() string {
	switch s {
	case present:
		return "yes"
	case missing:
		return "no"
	}
	return "unknown"
}

type row struct {
	name         string
	boot, self   support
	selfEvidence string
	bootEvidence string
}

func builtinsMain(argv []string) int {
	fs := flag.NewFlagSet("builtins", flag.ExitOnError)
	root := fs.String("root", ".", "repository root")
	bin := fs.String("bin", "./twill", "the Go bootstrap binary")
	out := fs.String("out", "docs/conformance.md", "file to write, relative to -root")
	check := fs.Bool("check", false, "do not write; fail if the checked-in file is out of date")
	timeout := fs.Duration("timeout", 60*time.Second, "per-probe time limit")
	jobs := fs.Int("jobs", runtime.NumCPU(), "probes to run at once")
	fs.Parse(argv)

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		die(err)
	}
	absBin, err := filepath.Abs(*bin)
	if err != nil {
		die(err)
	}
	names, err := readNames(absRoot)
	if err != nil {
		die(err)
	}

	dir, err := stage(absRoot, "", nil)
	if err != nil {
		die(err)
	}
	defer os.RemoveAll(dir)

	rows := probeAll(absBin, dir, names, *timeout, *jobs)
	doc := render(rows)

	target := filepath.Join(absRoot, *out)
	if *check {
		have, err := os.ReadFile(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "conformance: cannot read %s: %v\n", *out, err)
			return 1
		}
		if string(have) != doc {
			fmt.Fprintf(os.Stderr, "conformance: %s is out of date\n%s\n", *out,
				firstDifference("on disk", string(have), "measured", doc))
			fmt.Fprintln(os.Stderr, "run `make conformance` and commit the result")
			return 1
		}
		fmt.Printf("%s is up to date (%d builtins)\n", *out, len(rows))
		return 0
	}
	if err := os.WriteFile(target, []byte(doc), 0o644); err != nil {
		die(err)
	}
	fmt.Printf("wrote %s (%d builtins, %d unimplemented self-hosted)\n",
		*out, len(rows), countBy(rows, func(r row) bool { return r.self == missing }))
	return 0
}

func readNames(root string) ([]string, error) {
	src, err := os.ReadFile(filepath.Join(root, "src", "builtins.tw"))
	if err != nil {
		return nil, err
	}
	m := namesRE.FindSubmatch(src)
	if m == nil {
		return nil, fmt.Errorf("no `let NAMES: Str = \"...\"` in src/builtins.tw")
	}
	return strings.Fields(string(m[1])), nil
}

// probeAll asks both implementations about every name. Each probe is its own
// process because the question is what the evaluator does when it reaches the
// call, and the only way to reach it is to make the call.
func probeAll(bin, dir string, names []string, timeout time.Duration, jobs int) []row {
	rows := make([]row, len(names))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			rows[i] = probe(bin, dir, name, timeout)
		}(i, name)
	}
	wg.Wait()
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows
}

// probe writes one call and reads the answer out of the two error messages.
//
// The arguments are the literal 0, and never a path or a file name, so that a
// builtin that deletes something cannot be handed anything to delete. --no-check
// is passed because the question is what the evaluator does, and the checker
// would otherwise refuse most of these calls before the evaluator saw them.
func probe(bin, dir, name string, timeout time.Duration) row {
	r := row{name: name}
	file := "probe_" + name + ".tw"
	for argc := 0; argc <= 3; argc++ {
		src := name + "(" + strings.TrimSuffix(strings.Repeat("0, ", argc), ", ") + ")\n"
		if err := os.WriteFile(filepath.Join(dir, file), []byte(src), 0o644); err != nil {
			die(err)
		}
		self := selfHosted(bin, dir, file, timeout, "--no-check")
		boot := bootstrap(bin, dir, file, timeout, "--no-check")
		if r.self == unknown {
			r.self, r.selfEvidence = classifySelf(self, name, file)
		}
		if r.boot == unknown {
			r.boot, r.bootEvidence = classifyBoot(boot, name, file)
		}
		if r.self != unknown && r.boot != unknown {
			break
		}
	}
	os.Remove(filepath.Join(dir, file))
	return r
}

func classifySelf(o outcome, name, file string) (support, string) {
	if o.timedOut {
		return unknown, "probe timed out"
	}
	if named(selfMissing, o.out(), name) {
		return missing, "eval reached its no-implementation fallback"
	}
	if o.code == 0 {
		return present, "ran"
	}
	line := diagnostic(o)
	switch {
	case line == "":
		return unknown, fmt.Sprintf("exit %d with no diagnostic", o.code)
	case arityComplaint.MatchString(line):
		return unknown, line
	case !attributable(line, file):
		// The failure is inside src/*.tw rather than in the probe, so the probe
		// says nothing about the name. Retry with more arguments.
		return unknown, line
	default:
		return present, line
	}
}

func classifyBoot(o outcome, name, file string) (support, string) {
	if o.timedOut {
		return unknown, "probe timed out"
	}
	if named(bootMissing, o.out(), name) {
		return missing, "the name is not bound in the Go interpreter"
	}
	if o.code == 0 {
		return present, "ran"
	}
	line := diagnostic(o)
	switch {
	case line == "":
		return unknown, fmt.Sprintf("exit %d with no diagnostic", o.code)
	case arityComplaint.MatchString(line):
		return unknown, line
	case !attributable(line, file):
		return unknown, line
	default:
		return present, line
	}
}

// named reports whether re matched and the name it captured is the name being
// probed. Both "missing" verdicts go through it, so neither implementation can
// be recorded as missing a builtin on the strength of a message about a
// different one.
func named(re *regexp.Regexp, out, name string) bool {
	m := re.FindStringSubmatch(out)
	return m != nil && m[1] == name
}

// diagnostic is the line a verdict is read from. Both implementations write
// their diagnostics to stderr and the program's own output to stdout, so a
// probe that prints before it fails -- and several do -- has the printed value
// as the first line of the two streams together. Reading that as the verdict
// classified a working builtin as inconclusive.
func diagnostic(o outcome) string {
	if o.stderr != "" {
		return firstLine(o.stderr)
	}
	return firstLine(o.stdout)
}

// attributable reports whether a diagnostic came from the probe file. A line
// naming one of the compiler's own sources is the self-hosted implementation
// falling over on the way to the call, which is a different fact.
func attributable(line, file string) bool {
	return line == "" || strings.HasPrefix(line, file+":")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func countBy(rows []row, pred func(row) bool) int {
	n := 0
	for _, r := range rows {
		if pred(r) {
			n++
		}
	}
	return n
}
