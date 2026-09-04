package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The Go checker's builtinNames and the self-hosted src/builtins.tw NAMES are
// meant to be one table in two spellings. src/builtins.tw says so in its own
// header: a name eval dispatches that is missing from NAMES is reported as
// unknown though it works, and a name in NAMES that eval does not dispatch
// checks clean and fails at run time. Nothing held the two spellings together
// until this file; bug 3 in docs/roadmap.md was exactly this drift, three names
// present on one side and absent on the other, and it was found by reading.
//
// The documentation quotes the size of the table -- "247 names", "128 of the
// 247" -- in five files. A number quoted in prose goes stale silently, which is
// the failure this whole documentation pass exists to undo, so the count is
// asserted against the table rather than trusted.
//
// This test does not run the self-hosted evaluator and costs nothing.

// selfHostedNames reads the NAMES word list out of src/builtins.tw.
func selfHostedNames(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "..", "src", "builtins.tw")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	m := regexp.MustCompile(`(?m)^let NAMES: Str = "([^"]*)"$`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("no `let NAMES: Str = \"...\"` line in %s", path)
	}
	return strings.Fields(string(m[1]))
}

func TestTheTwoBuiltinTablesHoldTheSameNames(t *testing.T) {
	self := selfHostedNames(t)
	inSelf := map[string]bool{}
	for _, n := range self {
		if inSelf[n] {
			t.Errorf("src/builtins.tw NAMES lists %q twice", n)
		}
		inSelf[n] = true
	}

	var onlyGo, onlySelf []string
	for n := range builtinNames {
		if !inSelf[n] {
			onlyGo = append(onlyGo, n)
		}
	}
	for n := range inSelf {
		if !builtinNames[n] {
			onlySelf = append(onlySelf, n)
		}
	}
	sort.Strings(onlyGo)
	sort.Strings(onlySelf)
	if len(onlyGo) != 0 {
		t.Errorf("in the Go checker's builtinNames and not in src/builtins.tw NAMES: %v\n"+
			"such a name is dispatched by one implementation and reported unknown by the other", onlyGo)
	}
	if len(onlySelf) != 0 {
		t.Errorf("in src/builtins.tw NAMES and not in the Go checker's builtinNames: %v", onlySelf)
	}
}

// docsQuotingTheCount are the files that state the size of the builtin table in
// prose, as "247 names", "119 of the 247", "the other 128".
var docsQuotingTheCount = []string{
	"README.md",
	"CONTRIBUTING.md",
	filepath.Join("docs", "roadmap.md"),
	filepath.Join("docs", "BUGS.md"),
	filepath.Join("docs", "CORRECTNESS.md"),
}

// selfHostedImplemented and selfHostedUnimplemented are the split of the table
// measured on 2026-09-04 by generating a call for every name and running it
// through src/main.tw: how many the self-hosted evaluator dispatches, and how
// many reach "named in the builtin table but has no implementation". They are
// not derived here -- deriving them costs 247 self-hosted runs -- but they are
// held to summing to the table, so a table that grows cannot leave the prose
// quietly describing a smaller one.
const (
	selfHostedImplemented   = 119
	selfHostedUnimplemented = 128
)

// A three-digit number written within this many characters of the word
// "builtin" is taken to be talking about the builtin table.
const nearBuiltin = 80

// blankFencedCode replaces the body of every ``` fence with spaces, keeping the
// file's offsets. Transcripts inside a fence carry line numbers -- the pasted
// `src/main.tw:154` failure is three digits sitting next to the word "builtin"
// -- and those are not counts of anything.
func blankFencedCode(text string) string {
	out := []byte(text)
	lines := strings.SplitAfter(text, "\n")
	inFence, at := false, 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		} else if inFence {
			for i := at; i < at+len(line); i++ {
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
		}
		at += len(line)
	}
	return string(out)
}

func TestTheDocumentedBuiltinCountIsTheRealOne(t *testing.T) {
	want := len(selfHostedNames(t))
	if want != len(builtinNames) {
		t.Fatalf("the two tables are different sizes (%d self-hosted, %d Go); "+
			"TestTheTwoBuiltinTablesHoldTheSameNames says which names", want, len(builtinNames))
	}
	if selfHostedImplemented+selfHostedUnimplemented != want {
		t.Fatalf("the documented split %d implemented + %d unimplemented is %d, "+
			"and the table holds %d. Re-run the probe described in docs/roadmap.md, "+
			"\"What the second implementation agrees on, and what it does not\", and "+
			"update both this constant pair and the prose in %v",
			selfHostedImplemented, selfHostedUnimplemented,
			selfHostedImplemented+selfHostedUnimplemented, want, docsQuotingTheCount)
	}

	allowed := map[int]bool{want: true, selfHostedImplemented: true, selfHostedUnimplemented: true}
	threeDigit := regexp.MustCompile(`\b(\d{3})\b`)
	checked := 0
	for _, rel := range docsQuotingTheCount {
		src, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		text := blankFencedCode(string(src))
		lower := strings.ToLower(text)
		for _, loc := range threeDigit.FindAllStringIndex(text, -1) {
			lo := loc[0] - nearBuiltin
			if lo < 0 {
				lo = 0
			}
			hi := loc[1] + nearBuiltin
			if hi > len(text) {
				hi = len(text)
			}
			if !strings.Contains(lower[lo:hi], "builtin") {
				continue
			}
			n, err := strconv.Atoi(text[loc[0]:loc[1]])
			if err != nil {
				continue
			}
			checked++
			if !allowed[n] {
				t.Errorf("%s writes %d next to the word \"builtin\"; the table holds %d, "+
					"of which %d are implemented self-hosted and %d are not.\ncontext: %s",
					rel, n, want, selfHostedImplemented, selfHostedUnimplemented,
					strings.Join(strings.Fields(text[lo:hi]), " "))
			}
		}
	}
	if checked == 0 {
		t.Errorf("no documentation file states the builtin count any more; "+
			"either the prose changed or this test stopped finding it (table holds %d)", want)
	}
}
