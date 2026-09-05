package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
// This test does not run the self-hosted evaluator and costs nothing.
//
// It asserts the invariant and not the size. An earlier version of this file
// also held the documentation's quoted count to the table's real size, with the
// implemented/unimplemented split written in as two constants. Those constants
// were stale within one merge, and a test that has to be hand-edited whenever
// someone ports a builtin is a test that will be edited wrongly. The counts now
// live in docs/BUGS.md, next to the probe that re-derives them, and nowhere
// else.

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
