package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// casesMain runs each checked-in case under both implementations and fails on
// any difference.
//
// It is the suites gate's smaller sibling and it exists because the two
// instruments answer different questions. `suites` runs the standard library's
// own tests, which is a broad sweep over code written for another purpose and
// which therefore needs an allow-list: most of what it finds is a divergence
// nobody has got to yet. A case here is written to pin one builtin's behaviour
// and is checked in only once both sides agree, so there is no allow-list and
// there is not going to be one. A case that diverges is a bug in the change
// that made it diverge.
//
// A case is a whole program run twice, as two processes, so the exit code and
// both streams are compared -- which is what makes it the right instrument for
// write_out, write_err and exit, none of which go through the interpreter's
// out sink and none of which an in-process comparison can see.
//
// The staging rule is the suites one: src/*.tw and the case land in one
// directory and both sides are handed the bare file name, so a path in a
// diagnostic is the same text on both sides and a difference in the output is
// a difference in behaviour.
func casesMain(argv []string) int {
	fs := flag.NewFlagSet("cases", flag.ExitOnError)
	root := fs.String("root", ".", "repository root")
	bin := fs.String("bin", "./twill", "the Go bootstrap binary")
	dirFlag := fs.String("cases", "testdata/conformance/cases", "directory of *.tw cases, relative to -root")
	timeout := fs.Duration("timeout", 2*time.Minute, "per-case time limit, per implementation")
	jobs := fs.Int("jobs", runtime.NumCPU(), "cases to run at once")
	fs.Parse(argv)

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		die(err)
	}
	absBin, err := filepath.Abs(*bin)
	if err != nil {
		die(err)
	}
	caseDir := filepath.Join(absRoot, *dirFlag)
	cases, err := findCases(caseDir)
	if err != nil {
		die(err)
	}
	if len(cases) == 0 {
		die(fmt.Errorf("no *.tw cases under %s", caseDir))
	}

	results := make([]caseResult, len(cases))
	sem := make(chan struct{}, *jobs)
	var wg sync.WaitGroup
	for i, name := range cases {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = runCase(absBin, absRoot, caseDir, name, *timeout)
		}(i, name)
	}
	wg.Wait()

	bad := 0
	for _, r := range results {
		if r.agreed {
			continue
		}
		bad++
		detail := r.detail
		if !strings.HasSuffix(detail, "\n") {
			detail += "\n"
		}
		fmt.Printf("%s\n%s", r.name, detail)
	}
	fmt.Printf("\n%d case(s), %d agreed, %d diverged\n", len(results), len(results)-bad, bad)
	if bad > 0 {
		fmt.Println("a case is checked in because both implementations agree on it, so a")
		fmt.Println("divergence here is a regression rather than an unfinished port")
		return 1
	}
	return 0
}

type caseResult struct {
	name   string
	agreed bool
	detail string
}

func findCases(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tw") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func runCase(bin, root, caseDir, name string, timeout time.Duration) caseResult {
	dir, err := stage(root, caseDir, func(string) bool { return true })
	if err != nil {
		return caseResult{name: name, detail: "  staging failed: " + err.Error() + "\n"}
	}
	defer os.RemoveAll(dir)

	boot := bootstrap(bin, dir, name, timeout)
	self := selfHosted(bin, dir, name, timeout)
	if boot.timedOut || self.timedOut {
		return caseResult{name: name, detail: fmt.Sprintf(
			"  timed out: bootstrap %t, self-hosted %t\n", boot.timedOut, self.timedOut)}
	}
	if boot.text() == self.text() {
		return caseResult{name: name, agreed: true}
	}
	return caseResult{name: name, detail: describe(boot, self)}
}
