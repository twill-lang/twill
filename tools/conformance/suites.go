package main

import (
	"crypto/sha256"
	"encoding/hex"
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
//
// Each line carries a signature of the divergence it excuses, not just the
// suite's name. A name on its own would excuse the suite rather than the
// finding: io_test.tw could stop dying on arr_new and start printing a wrong
// number, and the build would stay green because the name was still on the
// list. The signature is what makes the entry a claim about one divergence.
func suitesMain(argv []string) int {
	fs := flag.NewFlagSet("suites", flag.ExitOnError)
	root := fs.String("root", ".", "repository root")
	bin := fs.String("bin", "./twill", "the Go bootstrap binary")
	dirFlag := fs.String("suites", "std/tests", "directory of *_test.tw suites, relative to -root")
	allow := fs.String("allow", "testdata/conformance/suite-allow.txt", "allow-list, relative to -root")
	timeout := fs.Duration("timeout", 5*time.Minute, "per-suite time limit, per implementation")
	jobs := fs.Int("jobs", runtime.NumCPU(), "suites to run at once")
	list := fs.Bool("list", false, "print `name signature` for each diverging suite and exit 0, for seeding an allow-list")
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
				fmt.Printf("%-24s %s\n", r.name, r.signature)
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
	name string
	// agreed is byte-for-byte agreement on exit code, stdout and stderr.
	agreed bool
	// unusable marks a result the comparison cannot speak for: the scratch
	// directory could not be built, or neither side finished. Nothing on the
	// allow-list excuses one, because the allow-list excuses divergences and
	// this is the absence of a measurement.
	unusable bool
	detail   string
	// signature identifies the divergence, not the suite. See divergenceKey.
	signature string
	key       string
	bootTime  time.Duration
	selfTime  time.Duration
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
		return suiteResult{name: name, unusable: true, detail: "  staging failed: " + err.Error()}
	}
	defer os.RemoveAll(dir)

	t0 := time.Now()
	boot := bootstrap(bin, dir, name, timeout)
	t1 := time.Now()
	self := selfHosted(bin, dir, name, timeout)
	res := suiteResult{name: name, bootTime: t1.Sub(t0), selfTime: time.Since(t1)}

	// Both sides out of budget is not agreement, though comparing the two
	// timed-out outcomes says it is: neither run finished, so there is nothing
	// to compare. Saying so is the only honest verdict, and it is not one the
	// allow-list can excuse.
	if boot.timedOut && self.timedOut {
		res.unusable = true
		res.detail = "  neither implementation finished inside the budget; nothing was compared"
		return res
	}
	if boot.text() == self.text() {
		res.agreed = true
		return res
	}
	res.detail = describe(boot, self)
	res.key = divergenceKey(boot, self)
	res.signature = signatureOf(res.key)
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

// --- the signature ----------------------------------------------------------

// sourceLine masks the line number in a `file.tw:1234:` prefix. The file is
// kept, because which source reported the fault is the fact; the number is
// dropped, because editing src/eval.tw anywhere above line 2425 moves it
// without changing the divergence at all, and a signature that moved on every
// unrelated edit would train people to re-seed it without reading.
var sourceLine = regexp.MustCompile(`([A-Za-z0-9_./-]+\.tw):[0-9]+:`)

// divergenceKey reduces a disagreement to the part of it that is a property of
// the two implementations rather than of the machine.
//
// A run that hit the budget is keyed on that alone. How far it got before the
// clock stopped it is a race with the hardware: llama_test.tw prints 31 lines
// here and would print a different number on a slower runner, so keying on the
// content would make the gate fail on the CI box for no reason. What is known
// about it is that it did not finish, and that is what the entry claims.
//
// Everything else is keyed on the exit codes and on the first line of each
// stream that moved, with source line numbers masked and control bytes escaped
// -- the messages carry raw tensor bytes, and a signature is no place for a NUL.
func divergenceKey(boot, self outcome) string {
	if boot.timedOut || self.timedOut {
		return fmt.Sprintf("timeout boot=%t self=%t", boot.timedOut, self.timedOut)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "exit boot=%d self=%d", boot.code, self.code)
	for _, stream := range []struct {
		name, a, b string
	}{
		{"stdout", boot.stdout, self.stdout},
		{"stderr", boot.stderr, self.stderr},
	} {
		if stream.a == stream.b {
			continue
		}
		n, x, y := firstDifferentLine(stream.a, stream.b)
		fmt.Fprintf(&b, "\n%s line %d\n  boot: %s\n  self: %s",
			stream.name, n, normalizeLine(x), normalizeLine(y))
	}
	return b.String()
}

func normalizeLine(s string) string {
	return sourceLine.ReplaceAllString(printable(strings.TrimRight(s, "\r")), "$1:N:")
}

func signatureOf(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// --- the allow-list ---------------------------------------------------------

type allowEntry struct {
	signature string
	line      int
}

// readAllow reads `<suite> <signature>` pairs, `#` starting a comment. A line
// with only a name is refused rather than accepted as a wildcard: that was the
// old format, and reading it as "excuse this suite whatever it does" is the
// hole this format exists to close.
func readAllow(path string) (map[string]allowEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]allowEntry{}
	for i, line := range strings.Split(string(b), "\n") {
		if j := strings.IndexByte(line, '#'); j >= 0 {
			line = line[:j]
		}
		fields := strings.Fields(line)
		switch len(fields) {
		case 0:
			continue
		case 2:
			if _, dup := out[fields[0]]; dup {
				return nil, fmt.Errorf("%s:%d: %s is listed twice", path, i+1, fields[0])
			}
			out[fields[0]] = allowEntry{signature: fields[1], line: i + 1}
		default:
			return nil, fmt.Errorf("%s:%d: want `<suite> <signature>`, got %q\n"+
				"run `conformance suites -list` to print the lines this run would accept", path, i+1, strings.TrimSpace(line))
		}
	}
	return out, nil
}

func verdict(results []suiteResult, allowed map[string]allowEntry, allowPath string) int {
	var newDiv, changed, staleAllow, unusable []suiteResult
	seen := map[string]bool{}
	agreed := 0
	known := 0
	for _, r := range results {
		seen[r.name] = true
		entry, listed := allowed[r.name]
		switch {
		case r.unusable:
			unusable = append(unusable, r)
		case r.agreed && listed:
			staleAllow = append(staleAllow, r)
			agreed++
		case r.agreed:
			agreed++
		case listed && entry.signature == r.signature:
			known++
		case listed:
			changed = append(changed, r)
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

	fmt.Printf("suites %d  agreed %d  known divergences %d  unexplained %d\n",
		len(results), agreed, known, len(newDiv)+len(changed)+len(unusable))
	for _, r := range results {
		mark := "ok  "
		if r.unusable {
			mark = "????"
		} else if !r.agreed {
			mark = "DIFF"
		}
		fmt.Printf("  %s %-24s bootstrap %6.1fs  self-hosted %6.1fs\n",
			mark, r.name, r.bootTime.Seconds(), r.selfTime.Seconds())
	}

	fail := false
	for _, r := range unusable {
		fail = true
		fmt.Printf("\n=== NO MEASUREMENT: %s\n%s\n", r.name, r.detail)
	}
	for _, r := range newDiv {
		fail = true
		fmt.Printf("\n=== NEW DIVERGENCE: %s\n%s\n\nadd to %s only with a reason:\n%-24s %s\n",
			r.name, r.detail, allowPath, r.name, r.signature)
	}
	for _, r := range changed {
		fail = true
		fmt.Printf("\n=== THE DIVERGENCE CHANGED: %s\n%s\n", r.name, r.detail)
		fmt.Printf("  %s:%d says %s, this run measured %s\n",
			allowPath, allowed[r.name].line, allowed[r.name].signature, r.signature)
		fmt.Printf("  the line excuses a divergence that is no longer the one happening.\n" +
			"  read the report above, rewrite the reason, and only then update the signature.\n")
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
