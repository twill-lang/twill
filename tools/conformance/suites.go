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

// suitesMain runs the standard library's own test suites under both
// implementations and compares what came back, byte for byte.
//
// The comparison is exact and the tolerance is zero, for the reason
// tools/diff/run gives: both sides do the same arithmetic in the same order
// over the same data, so a tolerance would hide exactly the bugs worth finding.
//
// The allow-list is the instrument's whole point. It starts long, because the
// self-hosted evaluator is not finished, and every line in it is a divergence
// somebody has looked at and written down. A divergence that is not on it fails
// the build, and so does a line on it that no longer diverges, because a gate
// whose exceptions never expire stops being a gate.
func suitesMain(argv []string) int {
	fs := flag.NewFlagSet("suites", flag.ExitOnError)
	root := fs.String("root", ".", "repository root")
	bin := fs.String("bin", "./twill", "the Go bootstrap binary")
	dirFlag := fs.String("suites", "std/tests", "directory of *_test.tw suites, relative to -root")
	allow := fs.String("allow", "testdata/conformance/suite-allow.txt", "allow-list, relative to -root")
	timeout := fs.Duration("timeout", 5*time.Minute, "per-suite time limit, per implementation")
	jobs := fs.Int("jobs", runtime.NumCPU(), "suites to run at once")
	list := fs.Bool("list", false, "print the diverging suite names and exit 0, for seeding an allow-list")
	fs.Parse(argv)

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		die(err)
	}
	absBin, err := filepath.Abs(*bin)
	if err != nil {
		die(err)
	}
	suiteDir := filepath.Join(absRoot, *dirFlag)
	suites, err := findSuites(suiteDir)
	if err != nil {
		die(err)
	}
	if len(suites) == 0 {
		die(fmt.Errorf("no *_test.tw suites under %s", suiteDir))
	}

	results := runSuites(absBin, absRoot, suiteDir, suites, *timeout, *jobs)

	if *list {
		for _, r := range results {
			if !r.agreed {
				fmt.Println(r.name)
			}
		}
		return 0
	}

	allowed, err := readAllow(filepath.Join(absRoot, *allow))
	if err != nil {
		die(err)
	}
	return verdict(results, allowed, *allow)
}

type suiteResult struct {
	name     string
	agreed   bool
	detail   string
	bootTime time.Duration
	selfTime time.Duration
}

func findSuites(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.tw") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func runSuites(bin, root, suiteDir string, suites []string, timeout time.Duration, jobs int) []suiteResult {
	results := make([]suiteResult, len(suites))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, name := range suites {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = runSuite(bin, root, suiteDir, name, timeout)
		}(i, name)
	}
	wg.Wait()
	return results
}

// runSuite gives each suite a scratch directory of its own, holding the
// compiler's sources and the whole suite directory. A suite that writes files
// -- io_test.tw does -- must not be able to change what another one sees, and a
// fixed layout is also what keeps a machine path out of a diagnostic.
func runSuite(bin, root, suiteDir, name string, timeout time.Duration) suiteResult {
	dir, err := stage(root, suiteDir, func(string) bool { return true })
	if err != nil {
		return suiteResult{name: name, detail: "staging failed: " + err.Error()}
	}
	defer os.RemoveAll(dir)

	t0 := time.Now()
	boot := bootstrap(bin, dir, name, timeout)
	t1 := time.Now()
	self := selfHosted(bin, dir, name, timeout)
	res := suiteResult{name: name, bootTime: t1.Sub(t0), selfTime: time.Since(t1)}
	if boot.text() == self.text() {
		res.agreed = true
		return res
	}
	res.detail = describe(boot, self)
	return res
}

// describe says how two runs of the same suite differ. It reports the exit
// codes, the streams and the timeouts separately, because "exit 0 against exit
// 1" is where a whole-output diff always stops and is never the interesting
// part: what a reader needs is the first line of output that moved.
func describe(boot, self outcome) string {
	var b strings.Builder
	if boot.timedOut || self.timedOut {
		fmt.Fprintf(&b, "  timed out: bootstrap %t, self-hosted %t\n", boot.timedOut, self.timedOut)
	}
	if boot.code != self.code {
		fmt.Fprintf(&b, "  exit code: bootstrap %d, self-hosted %d\n", boot.code, self.code)
	}
	for _, stream := range []struct {
		name, a, b string
	}{
		{"stdout", boot.stdout, self.stdout},
		{"stderr", boot.stderr, self.stderr},
	} {
		if stream.a != stream.b {
			fmt.Fprintf(&b, "  %s %s\n", stream.name,
				firstDifference("bootstrap", stream.a, "self-hosted", stream.b))
		}
	}
	if b.Len() == 0 {
		return "  the outputs match; the difference is in the exit status alone"
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- the allow-list ---------------------------------------------------------

func readAllow(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if name := strings.TrimSpace(line); name != "" {
			out[name] = true
		}
	}
	return out, nil
}

func verdict(results []suiteResult, allowed map[string]bool, allowPath string) int {
	var newDiv, staleAllow []suiteResult
	seen := map[string]bool{}
	agreed := 0
	for _, r := range results {
		seen[r.name] = true
		switch {
		case r.agreed && allowed[r.name]:
			staleAllow = append(staleAllow, r)
			agreed++
		case r.agreed:
			agreed++
		case allowed[r.name]:
			// A known divergence. Reported, not fatal.
		default:
			newDiv = append(newDiv, r)
		}
	}
	var ghosts []string
	for name := range allowed {
		if !seen[name] {
			ghosts = append(ghosts, name)
		}
	}
	sort.Strings(ghosts)

	fmt.Printf("suites %d  agreed %d  known divergences %d\n",
		len(results), agreed, len(results)-agreed)
	for _, r := range results {
		mark := "ok  "
		if !r.agreed {
			mark = "DIFF"
		}
		fmt.Printf("  %s %-24s bootstrap %6.1fs  self-hosted %6.1fs\n",
			mark, r.name, r.bootTime.Seconds(), r.selfTime.Seconds())
	}

	fail := false
	for _, r := range newDiv {
		fail = true
		fmt.Printf("\n=== NEW DIVERGENCE: %s\n%s\n", r.name, r.detail)
	}
	for _, r := range staleAllow {
		fail = true
		fmt.Printf("\n=== %s now agrees: delete its line from %s\n", r.name, allowPath)
	}
	for _, name := range ghosts {
		fail = true
		fmt.Printf("\n=== %s is on %s but is not a suite: delete its line\n", name, allowPath)
	}
	if fail {
		fmt.Print("\nFAILED\n")
		return 1
	}
	fmt.Print("\nno new divergence\n")
	return 0
}
