// Command run holds two twill binaries to the same corpus and reports where
// they disagree.
//
// The comparison is exact. Both binaries do IEEE-754 arithmetic in the same
// order over the same data, so the correct tolerance for a forward value is
// zero, and starting with a tolerance would hide precisely the ordering and
// representation bugs this harness exists to find.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	var (
		corpus  = flag.String("corpus", "testdata", "corpus directory holding .tw fixtures")
		oldBin  = flag.String("old", "", "path to the reference twill binary")
		newBin  = flag.String("new", "", "path to the candidate twill binary")
		bin     = flag.String("bin", "", "single binary, for -record and -verify")
		record  = flag.Bool("record", false, "record golden outputs next to each fixture")
		verify  = flag.Bool("verify", false, "check one binary against the recorded goldens")
		doFmt   = flag.Bool("fmt", true, "check twill fmt idempotence and cross-agreement")
		doRSTR  = flag.Bool("rstr", true, "check RSTR save-file round trips")
		fuzzN   = flag.Int("fuzz", 0, "generate and diff this many random programs")
		seed    = flag.Int64("seed", 1, "fuzz seed; the same seed gives the same programs")
		timeout = flag.Duration("timeout", 120*time.Second, "per-case time limit")
		jobs    = flag.Int("jobs", runtime.NumCPU(), "cases to run at once")
	)
	flag.Parse()

	cases, err := collect(*corpus)
	if err != nil {
		fail(err)
	}
	if len(cases) == 0 {
		fail(fmt.Errorf("no .tw fixtures under %s", *corpus))
	}

	rep := &report{}
	switch {
	case *record:
		if *bin == "" {
			fail(errors.New("-record needs -bin"))
		}
		recordGoldens(rep, *bin, cases, *timeout, *jobs)
	case *verify:
		if *bin == "" {
			fail(errors.New("-verify needs -bin"))
		}
		verifyGoldens(rep, *bin, cases, *timeout, *jobs)
	default:
		if *oldBin == "" || *newBin == "" {
			fail(errors.New("need -old and -new (or -bin with -record/-verify)"))
		}
		diffCorpus(rep, *oldBin, *newBin, cases, *timeout, *jobs)
		if *doFmt {
			diffFmt(rep, *oldBin, *newBin, cases, *timeout, *jobs)
		}
		if *doRSTR {
			checkRSTR(rep, *oldBin, *newBin, *corpus, *timeout)
		}
		if *fuzzN > 0 {
			fuzz(rep, *oldBin, *newBin, *fuzzN, *seed, *timeout, *jobs)
		}
	}

	rep.print()
	if rep.divergences() > 0 {
		os.Exit(1)
	}
}

// --- running one case ------------------------------------------------------

// result is everything a fixture produced. The exit code is part of it because
// half the corpus is expected to fail, and failing differently is a divergence.
type result struct {
	stdout string
	stderr string
	code   int
}

func (r result) text() string {
	return fmt.Sprintf("exit %d\n--- stdout ---\n%s--- stderr ---\n%s", r.code, r.stdout, r.stderr)
}

// runCase executes one fixture in a scratch directory of its own. A fixture
// that writes files (the save/load ones do) must not be able to change what a
// later fixture sees, and a fixed working directory is also what keeps error
// messages free of machine-specific paths.
//
// dataDir, when set, is the fixture's own directory; its non-.tw files come
// along, because examples/frames.tw is not a test of anything without
// prices.csv next to it.
func runCase(bin, src, dataDir string, timeout time.Duration, args ...string) (result, error) {
	dir, err := os.MkdirTemp("", "twilldiff")
	if err != nil {
		return result{}, err
	}
	defer os.RemoveAll(dir)

	if dataDir != "" {
		if err := copyData(dataDir, dir); err != nil {
			return result{}, err
		}
	}
	name := "case.tw"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		return result{}, err
	}
	// The subcommand takes its path as the argument right after it, so the file
	// name goes in second and any flags follow it.
	full := append([]string{args[0], name}, args[1:]...)
	return runIn(bin, dir, timeout, full...)
}

func copyData(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tw") || strings.HasSuffix(e.Name(), ".golden") {
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

func runIn(bin, dir string, timeout time.Duration, args ...string) (result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	abs, err := filepath.Abs(bin)
	if err != nil {
		return result{}, err
	}
	cmd := exec.CommandContext(ctx, abs, args...)
	cmd.Dir = dir
	// A fixed environment, because the point is reproducibility and TWILL_STD
	// would otherwise silently swap the standard library under one binary.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()

	// The scratch directory's name is in every message that mentions a path, and
	// it changes every run, so it has to come back out or half the IO fixtures
	// would be unusable as goldens.
	res := result{stdout: redact(out.String(), dir), stderr: redact(errb.String(), dir)}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		res.code = exitErr.ExitCode()
	default:
		return res, runErr
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("timed out after %s", timeout)
	}
	return res, nil
}

// --- corpus -----------------------------------------------------------------

func collect(root string) ([]string, error) {
	var cases []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".tw") {
			cases = append(cases, path)
		}
		return nil
	})
	sort.Strings(cases)
	return cases, err
}

func goldenPath(caseFile string) string { return caseFile + ".golden" }

func eachCase(cases []string, jobs int, f func(path, src, dataDir string)) {
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for _, path := range cases {
		src, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(path, src string) {
			defer wg.Done()
			defer func() { <-sem }()
			f(path, src, filepath.Dir(path))
		}(path, string(src))
	}
	wg.Wait()
}

func recordGoldens(rep *report, bin string, cases []string, timeout time.Duration, jobs int) {
	eachCase(cases, jobs, func(path, src, dataDir string) {
		first, err := runCase(bin, src, dataDir, timeout, "run", "--dump=canonical")
		if err != nil {
			rep.add("skipped", path, err.Error())
			os.Remove(goldenPath(path))
			return
		}
		// Recording twice and discarding disagreement is the cheapest filter for
		// a fixture that reads a clock or a machine path. Such a case cannot
		// serve as a golden for anyone, so it should not be in the corpus.
		second, err := runCase(bin, src, dataDir, timeout, "run", "--dump=canonical")
		if err != nil || first.text() != second.text() {
			rep.add("nondeterministic", path, "output differed between two runs of the same binary")
			os.Remove(goldenPath(path))
			return
		}
		if err := os.WriteFile(goldenPath(path), []byte(first.text()), 0o644); err != nil {
			fail(err)
		}
		rep.count("recorded")
	})
}

func verifyGoldens(rep *report, bin string, cases []string, timeout time.Duration, jobs int) {
	eachCase(cases, jobs, func(path, src, dataDir string) {
		want, err := os.ReadFile(goldenPath(path))
		if err != nil {
			rep.add("missing golden", path, "no recorded output")
			return
		}
		got, err := runCase(bin, src, dataDir, timeout, "run", "--dump=canonical")
		if err != nil {
			rep.add("error", path, err.Error())
			return
		}
		if got.text() != string(want) {
			rep.add("golden mismatch", path, firstDifference(string(want), got.text()))
			return
		}
		rep.count("verified")
	})
}

func diffCorpus(rep *report, oldBin, newBin string, cases []string, timeout time.Duration, jobs int) {
	eachCase(cases, jobs, func(path, src, dataDir string) {
		a, errA := runCase(oldBin, src, dataDir, timeout, "run", "--dump=canonical")
		b, errB := runCase(newBin, src, dataDir, timeout, "run", "--dump=canonical")
		if errA != nil || errB != nil {
			rep.add("error", path, fmt.Sprintf("old: %v new: %v", errA, errB))
			return
		}
		if a.text() != b.text() {
			rep.add("run divergence", path, firstDifference(a.text(), b.text()))
			return
		}
		rep.count("agreed")
	})
}

func diffFmt(rep *report, oldBin, newBin string, cases []string, timeout time.Duration, jobs int) {
	eachCase(cases, jobs, func(path, src, dataDir string) {
		a, errA := runCase(oldBin, src, dataDir, timeout, "fmt")
		b, errB := runCase(newBin, src, dataDir, timeout, "fmt")
		if errA != nil || errB != nil {
			rep.add("error", path, fmt.Sprintf("fmt old: %v new: %v", errA, errB))
			return
		}
		if a.text() != b.text() {
			rep.add("fmt divergence", path, firstDifference(a.text(), b.text()))
			return
		}
		if a.code != 0 {
			// An unformattable fixture is a parse failure, which the run diff
			// already covers; idempotence has nothing to say about it.
			rep.count("fmt skipped")
			return
		}
		again, err := runCase(newBin, a.stdout, dataDir, timeout, "fmt")
		if err != nil {
			rep.add("error", path, err.Error())
			return
		}
		if again.stdout != a.stdout {
			rep.add("fmt not idempotent", path, firstDifference(a.stdout, again.stdout))
			return
		}
		rep.count("fmt agreed")
	})
}

// --- RSTR round trip --------------------------------------------------------

// rstrCases covers one value per on-disk tag. The tags are the format's whole
// surface, so a generated case per tag is what makes "files written by v1.2.0
// still load" a checkable claim rather than a hope.
var rstrCases = map[string]string{
	"tensor": `let v = [[1.0, 2.5], [-3.25, 1e300]]
save(v, "a.bin")
save(load("a.bin"), "b.bin")`,
	"scalar": `let v = 0.1 + 0.2
save(v, "a.bin")
save(load("a.bin"), "b.bin")`,
	"list": `let v = [1.0, 2.0]
save([v, v], "a.bin")
save(load("a.bin"), "b.bin")`,
	"record": `let v = { w: [1.0, 2.0], b: 0.5, tag: "hi", ok: true }
save(v, "a.bin")
save(load("a.bin"), "b.bin")`,
	"bool": `save(true, "a.bin")
save(load("a.bin"), "b.bin")`,
	"str": `save("a string with \" in it", "a.bin")
save(load("a.bin"), "b.bin")`,
	"unit": `save(print(), "a.bin")
save(load("a.bin"), "b.bin")`,
	"model": `seed(3)
let X = randn(40, 3)
let y = greater(X @ [1.0, -1.0, 0.5], 0.0)
let m = gbm_fit(X, y, { objective: "logistic", rounds: 5, max_depth: 2 })
save(m, "a.bin")
save(load("a.bin"), "b.bin")`,
}

func checkRSTR(rep *report, oldBin, newBin, corpus string, timeout time.Duration) {
	names := make([]string, 0, len(rstrCases))
	for name := range rstrCases {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		src := rstrCases[name]
		aBytes, aErr := rstrBytes(oldBin, src, timeout)
		bBytes, bErr := rstrBytes(newBin, src, timeout)
		if aErr != nil || bErr != nil {
			rep.add("error", "rstr/"+name, fmt.Sprintf("old: %v new: %v", aErr, bErr))
			continue
		}
		// Two checks in one: within a binary, saving what was loaded must
		// reproduce the file (a.bin == b.bin), and across binaries the bytes
		// must match too.
		if !bytes.Equal(aBytes[0], aBytes[1]) {
			rep.add("rstr round trip", "rstr/"+name, "old binary: re-saved bytes differ from the original")
			continue
		}
		if !bytes.Equal(bBytes[0], bBytes[1]) {
			rep.add("rstr round trip", "rstr/"+name, "new binary: re-saved bytes differ from the original")
			continue
		}
		if !bytes.Equal(aBytes[0], bBytes[0]) {
			rep.add("rstr divergence", "rstr/"+name, "the two binaries wrote different bytes")
			continue
		}
		rep.count("rstr agreed")
	}

	// The checked-in fixtures, which are the actual v1.2.0 compatibility claim.
	// The save files live in the corpus, not in examples/: .gitignore excludes
	// *.bin, so the two fixtures the rewrite plan calls checked-in were never
	// actually tracked. The corpus copies are force-added and are the ones a
	// fresh clone can rely on.
	for _, fixture := range []string{
		filepath.Join(corpus, "examples", "model.bin"),
		filepath.Join(corpus, "examples", "params.bin"),
	} {
		raw, err := os.ReadFile(fixture)
		if err != nil {
			rep.add("error", fixture, err.Error())
			continue
		}
		src := `save(load("in.bin"), "a.bin")` + "\n" + `save(load("a.bin"), "b.bin")` + "\n"
		for label, bin := range map[string]string{"old": oldBin, "new": newBin} {
			out, err := rstrFixture(bin, src, raw, timeout)
			if err != nil {
				rep.add("error", fixture, label+": "+err.Error())
				continue
			}
			if !bytes.Equal(out[0], raw) {
				rep.add("rstr round trip", fixture, label+": re-saving a v1.2.0 file changed its bytes")
				continue
			}
			if !bytes.Equal(out[0], out[1]) {
				rep.add("rstr round trip", fixture, label+": second round trip changed its bytes")
				continue
			}
			rep.count("rstr fixture agreed")
		}
	}
}

// rstrBytes runs a save/load driver and returns a.bin and b.bin.
func rstrBytes(bin, src string, timeout time.Duration) ([2][]byte, error) {
	return rstrFixture(bin, src, nil, timeout)
}

func rstrFixture(bin, src string, input []byte, timeout time.Duration) ([2][]byte, error) {
	var out [2][]byte
	dir, err := os.MkdirTemp("", "twillrstr")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(dir)

	if input != nil {
		if err := os.WriteFile(filepath.Join(dir, "in.bin"), input, 0o644); err != nil {
			return out, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "case.tw"), []byte(src), 0o644); err != nil {
		return out, err
	}
	res, err := runIn(bin, dir, timeout, "run", "case.tw")
	if err != nil {
		return out, err
	}
	if res.code != 0 {
		return out, fmt.Errorf("exit %d: %s", res.code, strings.TrimSpace(res.stderr))
	}
	for i, name := range []string{"a.bin", "b.bin"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return out, err
		}
		out[i] = b
	}
	return out, nil
}

// --- fuzzing ----------------------------------------------------------------

func fuzz(rep *report, oldBin, newBin string, n int, seed int64, timeout time.Duration, jobs int) {
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		src := generate(seed + int64(i))
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, src string) {
			defer wg.Done()
			defer func() { <-sem }()
			a, errA := runCase(oldBin, src, "", timeout, "run", "--dump=canonical")
			b, errB := runCase(newBin, src, "", timeout, "run", "--dump=canonical")
			name := fmt.Sprintf("fuzz/seed-%d", seed+int64(i))
			if errA != nil || errB != nil {
				rep.add("error", name, fmt.Sprintf("old: %v new: %v", errA, errB))
				return
			}
			if a.text() != b.text() {
				rep.add("fuzz divergence", name, src+"\n"+firstDifference(a.text(), b.text()))
				return
			}
			rep.count("fuzz agreed")
		}(i, src)
	}
	wg.Wait()
}

// --- reporting --------------------------------------------------------------

type finding struct{ kind, where, detail string }

type report struct {
	mu       sync.Mutex
	counts   map[string]int
	findings []finding
}

func (r *report) count(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = map[string]int{}
	}
	r.counts[kind]++
}

func (r *report) add(kind, where, detail string) {
	r.mu.Lock()
	r.findings = append(r.findings, finding{kind, where, detail})
	r.mu.Unlock()
	r.count(kind)
}

// divergences counts the findings that mean the two binaries are not the same
// language. A nondeterministic or skipped fixture is information, not failure.
func (r *report) divergences() int {
	n := 0
	for _, f := range r.findings {
		switch f.kind {
		case "nondeterministic", "skipped", "missing golden":
		default:
			n++
		}
	}
	return n
}

func (r *report) print() {
	kinds := make([]string, 0, len(r.counts))
	for k := range r.counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Printf("%-24s %d\n", k, r.counts[k])
	}
	sort.Slice(r.findings, func(i, j int) bool { return r.findings[i].where < r.findings[j].where })
	for _, f := range r.findings {
		fmt.Printf("\n=== %s: %s\n%s\n", f.kind, f.where, f.detail)
	}
	fmt.Printf("\n%d divergence(s)\n", r.divergences())
}

// firstDifference reports the first line where two outputs part company, which
// is the only part of a diff anyone reads when a whole corpus has moved.
func firstDifference(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var a, b string
		if i < len(wl) {
			a = wl[i]
		}
		if i < len(gl) {
			b = gl[i]
		}
		if a != b {
			return fmt.Sprintf("line %d\n  old: %s\n  new: %s", i+1, a, b)
		}
	}
	return "outputs differ only in trailing content"
}

// redact replaces the scratch path with a fixed token, in both slash styles,
// because Windows and the interpreter do not agree on which one they print.
func redact(s, dir string) string {
	// Three forms, because a path reaches the output either raw, slashed, or
	// %q-quoted with its backslashes doubled, and missing one leaves the fixture
	// looking nondeterministic for no reason.
	forms := []string{
		strings.ReplaceAll(dir, `\`, `\\`),
		dir,
		filepath.ToSlash(dir),
	}
	for _, form := range forms {
		s = strings.ReplaceAll(s, form, "<dir>")
	}
	return s
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "diff/run:", err)
	os.Exit(2)
}
