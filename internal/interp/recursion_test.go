package interp_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
	"github.com/twill-lang/twill/internal/value"
)

// A runaway recursion is refused with an ordinary twill error rather than
// taking the process down with a Go stack overflow. What that crash looked like
// was measured rather than remembered: running the program below on this
// machine (macOS arm64, Go 1.27, the 1 GB default goroutine stack) with the
// limit lifted, via TWILL_MAX_CALL_DEPTH=100000000, prints nothing on stdout
// and 424 lines on stderr, the same 424 on every repeat. They are `fatal error:
// stack overflow` and a runtime traceback: 96 of the lines name the
// interpreter's own Go frames, and one reads `...1478067 frames elided...`. The
// process exits 2, so it is not statusless -- but 2 is the status the CLI uses
// for a usage error, so the crash was not distinguishable by status from a typo
// on the command line, and no line of it names the user's function or the line
// the recursion is on. A stack overflow is a fatal error that no recover
// catches, so the depth counter is what turns that into a diagnostic, and it
// has to be a refusal *before* the stack runs out, not a rescue after.
func TestRunawayRecursionIsRefused(t *testing.T) {
	const src = `fn f(n) {
  if n == 0 { return 0 }
  f(n - 1) + 1
}
print(f(1000000))
`
	ip := interp.New(func(string) {})
	_, err := ip.Run(src)
	if err == nil {
		t.Fatal("a recursion with no reachable base case was answered, not refused")
	}
	re, ok := err.(*interp.RuntimeError)
	if !ok {
		t.Fatalf("error is %T, not a *interp.RuntimeError: %v", err, err)
	}
	// The message names the function, says how deep it got, and the error
	// carries the line of the call so the CLI can point at it.
	for _, want := range []string{`"f"`, fmt.Sprint(interp.DefaultMaxCallDepth), "call depth limit"} {
		if !strings.Contains(re.Msg, want) {
			t.Errorf("message %q does not mention %q", re.Msg, want)
		}
	}
	if re.Line != 3 {
		t.Errorf("error is reported at line %d; the recursive call is on line 3", re.Line)
	}
}

// The case just under the limit still runs. A limit that also refuses the
// deepest legitimate program is not a diagnostic, it is a new bug.
func TestRecursionJustUnderTheLimitStillRuns(t *testing.T) {
	src := fmt.Sprintf(`fn depth(n) {
  if n == 0 { return 0 }
  depth(n - 1) + 1
}
print(depth(%d))
`, interp.DefaultMaxCallDepth-1)
	var out strings.Builder
	ip := interp.New(func(s string) { out.WriteString(s) })
	if _, err := ip.Run(src); err != nil {
		t.Fatalf("a recursion one call short of the limit was refused: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != fmt.Sprint(interp.DefaultMaxCallDepth-1) {
		t.Errorf("depth(%d) printed %q", interp.DefaultMaxCallDepth-1, got)
	}
}

// An anonymous function has no name to print, so it is named by what it is.
func TestRunawayRecursionInAnAnonymousFunction(t *testing.T) {
	const src = `let go = fn(n) = 0
go = fn(n) = go(n + 1)
print(go(0))
`
	ip := interp.New(func(string) {})
	_, err := ip.Run(src)
	if err == nil {
		t.Fatal("a runaway anonymous recursion was answered, not refused")
	}
	if !strings.Contains(err.Error(), "an anonymous function") {
		t.Errorf("message %q does not name the anonymous function", err.Error())
	}
}

// Mutual recursion is caught the same way; the counter is the interpreter's,
// not any one function's.
func TestMutualRecursionIsRefused(t *testing.T) {
	const src = `fn ping(n) = pong(n + 1)
fn pong(n) = ping(n + 1)
print(ping(0))
`
	ip := interp.New(func(string) {})
	_, err := ip.Run(src)
	if err == nil {
		t.Fatal("mutual recursion with no base case was answered, not refused")
	}
	if !strings.Contains(err.Error(), "call depth limit") {
		t.Errorf("message %q is not the depth refusal", err.Error())
	}
}

// The limit is a property of the interpreter instance, so an embedder that runs
// an interpreter written in twill on top of this one can raise it. See the
// field's documentation for why that is needed at all.
func TestMaxCallDepthIsSettable(t *testing.T) {
	const src = `fn f(n) {
  if n == 0 { return 0 }
  f(n - 1) + 1
}
print(f(50))
`
	ip := interp.New(func(string) {})
	ip.MaxCallDepth = 10
	_, err := ip.Run(src)
	if err == nil {
		t.Fatal("a 50-deep recursion was answered at a limit of 10")
	}
	if !strings.Contains(err.Error(), "is 10 calls deep") {
		t.Errorf("message %q does not report the lowered limit", err.Error())
	}
}

// The counter counts calls, and it counts them the same wherever the call sits
// inside the expression around it.
//
// This is worth pinning because the two quantities are easy to confuse, and the
// first version of this work confused them. How much of the host's Go stack a twill
// frame costs depends almost entirely on how deeply the recursive call is nested
// in its own expression, since every enclosing operator is another evalExpr
// frame held open across the call; see DefaultMaxCallDepth for the measurements.
// The refusal must not inherit any of that. It fires at the same call count for
// a bare `f(n - 1)` and for one buried forty operators deep, and it says the
// same thing.
func TestTheRefusalIsIndependentOfExpressionNesting(t *testing.T) {
	const limit = 200
	for _, layers := range []int{0, 1, 10, 40} {
		t.Run(fmt.Sprintf("%d layers", layers), func(t *testing.T) {
			call := "f(n - 1)"
			for i := 0; i < layers; i++ {
				call += " + 1"
			}
			// 500 is deeper than the limit and nowhere near the host's stack, so
			// what this measures is the counter and not the machine.
			src := fmt.Sprintf("fn f(n) {\n  if n == 0 { return 0 }\n  %s\n}\nprint(f(500))\n", call)
			ip := interp.New(func(string) {})
			ip.MaxCallDepth = limit
			_, err := ip.Run(src)
			if err == nil {
				t.Fatal("a recursion past the limit was answered, not refused")
			}
			re, ok := err.(*interp.RuntimeError)
			if !ok {
				t.Fatalf("error is %T, not a *interp.RuntimeError: %v", err, err)
			}
			want := fmt.Sprintf(`call depth limit reached: "f" is %d calls deep`, limit)
			if !strings.HasPrefix(re.Msg, want) {
				t.Errorf("message %q does not start with %q", re.Msg, want)
			}
			if re.Line != 3 {
				t.Errorf("error is reported at line %d; the recursive call is on line 3", re.Line)
			}
		})
	}
}

// Any panic that is not one of the interpreter's own signals comes back as a
// twill error, not a Go trace. A builtin that faults is a bug in twill, and the
// person who hits it is running a twill program: they should be told which line
// of their program was executing and that it is not their fault, not handed a
// goroutine dump. The stack overflow this file's other tests are about is the
// one fault this cannot cover, which is why the depth limit exists as well.
func TestInterpreterPanicBecomesATwillError(t *testing.T) {
	ip := interp.New(func(string) {})
	ip.Global.Define("boom", &value.Builtin{
		Name:  "boom",
		Arity: 0,
		Fn: func([]value.Value) (value.Value, error) {
			var empty []int
			_ = empty[3] // a genuine Go runtime fault, the way a buggy builtin would fault
			return value.TheUnit, nil
		},
	})
	_, err := ip.Run("let x = 1\nlet y = boom()\n")
	if err == nil {
		t.Fatal("a faulting builtin returned without an error")
	}
	re, ok := err.(*interp.RuntimeError)
	if !ok {
		t.Fatalf("error is %T, not a *interp.RuntimeError: %v", err, err)
	}
	for _, want := range []string{"internal error", "index out of range", "bug in twill"} {
		if !strings.Contains(re.Msg, want) {
			t.Errorf("message %q does not mention %q", re.Msg, want)
		}
	}
	if re.Line != 2 {
		t.Errorf("the fault is reported at line %d; the call is on line 2", re.Line)
	}
}

// selfHostedHostDepth is what TWILL_MAX_CALL_DEPTH has to be for the
// self-hosted evaluator to reach its own limit before the bootstrap under it
// reaches the bootstrap's.
//
// It is not a taste. Running the self-hosted evaluator on the bootstrap puts
// two counters over one Go stack, and the outer depth grows by 8 for each of
// the inner engine's frames (measured; see Interp.MaxCallDepth). The host limit
// at which the inner engine gets to refuse first was found by bisecting the
// shipped CLI, and re-bisected on this tree, which carries the self-hosted
// filesystem/clock/process port: 80,012 is not enough and 80,013 is. Any host
// below that refuses first, and at 80,012 it names push_text inside
// src/eval.tw. 100,000 clears 80,013 and stays under the 147,816 where the
// bootstrap's own stack gives out for this shape of program.
const selfHostedHostDepth = 100000

// recursionCases are the programs the two engines are held to. Each is a
// runaway recursion of a different shape, because "the two messages agree" was
// previously checked on one shape -- a named top-level function -- and one
// shape does not cover the two things the message is built from: which closure
// is named, and which line the call is on.
var recursionCases = []struct {
	name string
	src  string
	// line is where the refusal must be reported, so that a message which
	// agrees but points at the wrong place still fails.
	line int
}{
	{"named function", `fn f(n) {
  if n == 0 { return 0 }
  f(n - 1) + 1
}
print(f(1000000))
`, 3},
	{"anonymous function", `let go = fn(n) = 0
go = fn(n) = go(n + 1)
print(go(0))
`, 2},
	{"mutual recursion", `fn ping(n) = pong(n + 1)
fn pong(n) = ping(n + 1)
print(ping(0))
`, 2},
	// Three functions, not two: with an even limit a two-cycle always refuses
	// the same one of the pair, so a counter that is off by one between the
	// engines still agrees. A three-cycle does not let that hide.
	{"three-way mutual recursion", `fn a(n) = b(n + 1)
fn b(n) = c(n + 1)
fn c(n) = a(n + 1)
print(a(0))
`, 1},
	// The recursive call is made by a builtin, not by twill code, so the depth
	// the two engines count has to survive a trip out through map and back.
	{"recursion through a builtin", `fn f(n) = map(fn(x) = f(x + 1), [n])
print(f(0))
`, 1},
	// The call spans lines. Both engines must name the line the call starts on,
	// which is not the line the argument is on and not the line it closes on.
	{"call spanning several lines", `fn f(n) {
  if n == 0 { return 0 }
  f(
    n - 1
  ) + 1
}
print(f(1000000))
`, 3},
	// A closure reached through a record field, which is the anonymous case
	// again by a different route into the evaluator.
	{"closure in a record field", `let r = { go: fn(n) = 0 }
r = { go: fn(n) = r.go(n + 1) }
print(r.go(0))
`, 2},
}

// selfHostedStderr runs the self-hosted CLI on source and returns what it wrote
// to the real stderr. The self-hosted CLI's diagnostics go through write_err to
// os.Stderr rather than through the interpreter's sink, so they are captured at
// the OS level; the temporary path is rewritten to placeholder so a caller can
// compare against a message built for a path of its own.
func selfHostedStderr(t *testing.T, source, placeholder string, hostDepth int) string {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()
	ip := interp.New(func(string) {})
	ip.MaxCallDepth = hostDepth
	_, ranMain, runErr := ip.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "run", target})
	w.Close()
	os.Stderr = saved
	out := <-done
	if runErr != nil {
		t.Fatalf("self-hosted CLI errored: %v", runErr)
	}
	if !ranMain {
		t.Fatal("self-hosted main did not run")
	}
	return strings.ReplaceAll(out, target, placeholder)
}

// The two engines refuse the same program with the same words, on every shape
// of runaway recursion in recursionCases, and point at the same line.
//
// The message is written twice -- once in Go, once in twill -- so this is what
// keeps the two copies equal; a change to one that is not made to the other
// fails here. The previous version of this test compared one program and
// compared it with strings.Contains, which passes on a message that agrees
// about the words and differs about the function or the line. This one builds
// the diagnostic the CLI would print for the bootstrap's error and requires the
// self-hosted CLI's first line to equal it byte for byte.
//
// The host's limit is raised to selfHostedHostDepth for the reason that
// constant documents, and that is not a test-only fiction: the same run from a
// shell is
//
//	TWILL_MAX_CALL_DEPTH=100000 twill run src/main.tw run prog.tw
//
// which is what TestMaxCallDepthComesFromTheEnvironment covers.
func TestSelfHostedRefusalsMatchTheBootstrap(t *testing.T) {
	skipUnderShort(t)
	for _, tc := range recursionCases {
		t.Run(tc.name, func(t *testing.T) {
			goIP := interp.New(func(string) {})
			goIP.MaxCallDepth = interp.DefaultMaxCallDepth
			_, goErr := goIP.Run(tc.src)
			if goErr == nil {
				t.Fatal("the bootstrap answered a runaway recursion")
			}
			goRE, ok := goErr.(*interp.RuntimeError)
			if !ok {
				t.Fatalf("bootstrap error is %T: %v", goErr, goErr)
			}
			if goRE.Line != tc.line {
				t.Errorf("the bootstrap reports line %d; the call is on line %d",
					goRE.Line, tc.line)
			}

			// The first line of what cmd/twill prints for this error, which is
			// also the first line the self-hosted CLI prints for its own.
			want := fmt.Sprintf("INPUT:%d: runtime error: %s", goRE.Line, goRE.Msg)
			out := selfHostedStderr(t, tc.src, "INPUT", selfHostedHostDepth)
			got := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
			if got != want {
				t.Errorf("the two engines disagree.\nbootstrap:   %s\nself-hosted: %s", want, got)
			}
		})
	}
}

// The boundary, on both engines: a program that nests exactly MaxCallDepth
// calls runs and answers, and one that nests a single call more is refused.
//
// `f(n)` reaches n+1 frames, so f(DefaultMaxCallDepth-1) is the deepest program
// the limit admits. Testing only the refusal leaves the other edge unmeasured,
// and a limit that also refuses the deepest legitimate program is not a
// diagnostic, it is a new bug.
func TestRecursionAtTheLimitAndOneBeyond(t *testing.T) {
	src := func(n int) string {
		return fmt.Sprintf(`fn depth(n) {
  if n == 0 { return 0 }
  depth(n - 1) + 1
}
print(depth(%d))
`, n)
	}
	atTheLimit := interp.DefaultMaxCallDepth - 1

	var out strings.Builder
	ip := interp.New(func(s string) { out.WriteString(s) })
	if _, err := ip.Run(src(atTheLimit)); err != nil {
		t.Fatalf("a program nesting exactly %d calls was refused: %v",
			interp.DefaultMaxCallDepth, err)
	}
	if got := strings.TrimSpace(out.String()); got != fmt.Sprint(atTheLimit) {
		t.Errorf("depth(%d) printed %q", atTheLimit, got)
	}

	over := interp.New(func(string) {})
	_, err := over.Run(src(atTheLimit + 1))
	if err == nil {
		t.Fatalf("a program nesting %d calls, one past the limit, was answered",
			interp.DefaultMaxCallDepth+1)
	}
	if !strings.Contains(err.Error(), "call depth limit") {
		t.Errorf("message %q is not the depth refusal", err.Error())
	}
}

// The same boundary on the self-hosted evaluator, and it prints what the
// bootstrap prints on both sides of it.
func TestSelfHostedRecursionAtTheLimitAndOneBeyond(t *testing.T) {
	skipUnderShort(t)
	src := func(n int) string {
		return fmt.Sprintf(`fn depth(n) {
  if n == 0 { return 0 }
  depth(n - 1) + 1
}
print(depth(%d))
`, n)
	}
	atTheLimit := interp.DefaultMaxCallDepth - 1

	// At the limit: it runs, and prints the bootstrap's answer.
	var goOut strings.Builder
	goIP := interp.New(func(s string) { goOut.WriteString(s) })
	goIP.MaxCallDepth = interp.DefaultMaxCallDepth
	if _, err := goIP.Run(src(atTheLimit)); err != nil {
		t.Fatalf("the bootstrap refused a %d-deep recursion: %v", atTheLimit+1, err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "atlimit.tw")
	if err := os.WriteFile(target, []byte(src(atTheLimit)), 0o644); err != nil {
		t.Fatal(err)
	}
	var selfOut strings.Builder
	selfIP := interp.New(func(s string) { selfOut.WriteString(s) })
	selfIP.MaxCallDepth = selfHostedHostDepth
	if _, ranMain, err := selfIP.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "run", target}); err != nil {
		t.Fatalf("self-hosted CLI errored: %v", err)
	} else if !ranMain {
		t.Fatal("self-hosted main did not run")
	}
	if goOut.String() != selfOut.String() {
		t.Fatalf("at the limit: go = %q, self = %q", goOut.String(), selfOut.String())
	}
	if !strings.Contains(goOut.String(), fmt.Sprint(atTheLimit)) {
		t.Errorf("a %d-deep recursion printed %q", atTheLimit+1, goOut.String())
	}

	// One past it: refused, with the bootstrap's words and the bootstrap's line.
	overIP := interp.New(func(string) {})
	overIP.MaxCallDepth = interp.DefaultMaxCallDepth
	_, goErr := overIP.Run(src(atTheLimit + 1))
	if goErr == nil {
		t.Fatal("the bootstrap answered a program one call past the limit")
	}
	goRE := goErr.(*interp.RuntimeError)
	want := fmt.Sprintf("INPUT:%d: runtime error: %s", goRE.Line, goRE.Msg)
	out := selfHostedStderr(t, src(atTheLimit+1), "INPUT", selfHostedHostDepth)
	got := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	if got != want {
		t.Errorf("one past the limit, the two engines disagree.\nbootstrap:   %s\nself-hosted: %s",
			want, got)
	}
}

// TWILL_MAX_CALL_DEPTH sets the limit for interpreters made by New. It is the
// only way a host can give a guest interpreter more depth than itself, and
// without it `twill run src/main.tw run prog.tw` cannot print what
// `twill run prog.tw` prints for the same prog.tw -- see Interp.MaxCallDepth
// for the arithmetic that makes a single shared constant unable to do it.
//
// A value that is not a positive integer is ignored rather than refused. This
// is a safety limit; refusing to start because its override is misspelled would
// be a worse outcome than starting at the default.
func TestMaxCallDepthComesFromTheEnvironment(t *testing.T) {
	for _, tc := range []struct {
		set  string
		want int
	}{
		{"100000", 100000},
		{"1", 1},
		{"", interp.DefaultMaxCallDepth},
		{"0", interp.DefaultMaxCallDepth},
		{"-5", interp.DefaultMaxCallDepth},
		{"lots", interp.DefaultMaxCallDepth},
	} {
		t.Run(fmt.Sprintf("%q", tc.set), func(t *testing.T) {
			t.Setenv("TWILL_MAX_CALL_DEPTH", tc.set)
			if got := interp.New(func(string) {}).MaxCallDepth; got != tc.want {
				t.Errorf("TWILL_MAX_CALL_DEPTH=%q gave MaxCallDepth %d, want %d",
					tc.set, got, tc.want)
			}
		})
	}

	// And it is the limit that is actually enforced, not just a field that got
	// written: at 20 a 50-deep recursion is refused and says 20.
	t.Setenv("TWILL_MAX_CALL_DEPTH", "20")
	_, err := interp.New(func(string) {}).Run(`fn f(n) {
  if n == 0 { return 0 }
  f(n - 1) + 1
}
print(f(50))
`)
	if err == nil {
		t.Fatal("a 50-deep recursion was answered at a limit of 20")
	}
	if !strings.Contains(err.Error(), "is 20 calls deep") {
		t.Errorf("message %q does not report the limit the environment set", err.Error())
	}
}
