package interp_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/checker"
	"github.com/twill-lang/twill/internal/format"
	"github.com/twill-lang/twill/internal/interp"
	"github.com/twill-lang/twill/internal/lexer"
	"github.com/twill-lang/twill/internal/parser"
	"github.com/twill-lang/twill/internal/value"
)

// The self-hosted compiler is twill written in twill (src/main.tw and the
// modules it imports). Running it on the Go bootstrap and asking it to check a
// file exercises the whole front end -- lexer, parser and checker, all in twill
// -- end to end. Its exit code is the milestone under guard: 0 for a clean file,
// 1 for one with a shape error, matching what the Go `check` command returns.
// Every test in this file runs the self-hosted compiler, which means the Go
// interpreter interpreting src/*.tw -- the slowest thing in the repository by a
// wide margin. They are skipped in short mode, which is not a way of running
// them less: CI's ordinary `go test` runs every one of them on every build. It
// is the race pass that passes -short, and the race detector is looking for
// data races in the parallel tensor kernels, which is not what a single-
// threaded tree walk over a twill source file exercises. Running them there
// bought nothing and cost the pass its 25-minute budget.
func skipUnderShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the self-hosted differential run in short mode")
	}
}

// runSelfHostedCheckOut is runSelfHostedCheck plus what the CLI wrote. The
// self-hosted CLI's diagnostics go through write_err to the real os.Stderr,
// bypassing the interpreter's sink, so they are captured at the OS level the
// way cmd/twill/test.go captures a suite's output. A test that reads only the
// exit code cannot tell a warning from a clean file, since neither fails.
func runSelfHostedCheckOut(t *testing.T, source string) (int, string) {
	t.Helper()
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
	code := runSelfHostedCheck(t, source)
	w.Close()
	os.Stderr = saved
	return code, <-done
}

func runSelfHostedCheck(t *testing.T, source string) int {
	skipUnderShort(t)
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	ip := interp.New(func(string) {})
	result, ranMain, err := ip.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "check", target})
	if err != nil {
		t.Fatalf("self-hosted CLI errored: %v", err)
	}
	if !ranMain {
		t.Fatal("self-hosted main did not run")
	}
	n, ok := value.AsNumber(result)
	if !ok {
		t.Fatalf("self-hosted main returned a non-number: %v", result)
	}
	return int(n)
}

// runBothWays evaluates a program on the Go interpreter directly and on the
// self-hosted evaluator (src/main.tw run) and returns the printed output of
// each. The self-hosted evaluator is meant to match the bootstrap byte for
// byte, so a builtin added to one is not done until it agrees on the other.
func runBothWays(t *testing.T, source string) (goOut, selfOut string) {
	skipUnderShort(t)
	t.Helper()
	var goBuf strings.Builder
	goIP := interp.New(func(s string) { goBuf.WriteString(s) })
	if _, err := goIP.Run(source); err != nil {
		t.Fatalf("Go interpreter errored: %v", err)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var selfBuf strings.Builder
	selfIP := interp.New(func(s string) { selfBuf.WriteString(s) })
	if _, ranMain, err := selfIP.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "run", target}); err != nil {
		t.Fatalf("self-hosted CLI errored: %v", err)
	} else if !ranMain {
		t.Fatal("self-hosted main did not run")
	}
	return goBuf.String(), selfBuf.String()
}

// A list of strings sorts by the bytewise order docs/language-guide.md pins
// (NEEDS-23), on both the Go bootstrap and the self-hosted evaluator, and the
// two must agree. Uppercase sorts before lowercase (byte value), a shorter
// prefix sorts first, and the optional descending flag reverses the result.
func TestSelfHostedSortsAStringListLikeTheBootstrap(t *testing.T) {
	for _, src := range []string{
		`print(sort(["banana", "apple", "cherry", "Apple"]))`,
		`print(sort(["b", "a", "c"], true))`,
		`print(sort(["ab", "a", "abc"]))`,
		`print(sort(list("ab", "a", "abc"), true))`,
	} {
		goOut, selfOut := runBothWays(t, src+"\n")
		if goOut != selfOut {
			t.Fatalf("sort output diverged for %q:\n  go:   %q\n  self: %q", src, goOut, selfOut)
		}
	}
}

// linspace and arange are new construction builtins; the self-hosted evaluator
// must produce the same tensors as the bootstrap.
func TestSelfHostedConstructionBuiltinsMatch(t *testing.T) {
	for _, src := range []string{
		`print(linspace(0.0, 1.0, 5))`,
		`print(linspace(-2.0, 2.0, 9))`,
		`print(linspace(3.0, 3.0, 1))`,
		`print(arange(0.0, 2.0, 0.5))`,
		`print(arange(1.0, 10.0, 2.0))`,
		`print(arange(5.0, 5.0, 1.0))`,
	} {
		goOut, selfOut := runBothWays(t, src+"\n")
		if goOut != selfOut {
			t.Fatalf("output diverged for %q:\n  go:   %q\n  self: %q", src, goOut, selfOut)
		}
	}
}

func TestSelfHostedCheckCleanFile(t *testing.T) {
	if code := runSelfHostedCheck(t, "mode systems\nfn add(a: I64, b: I64) -> I64 = a + b\n"); code != 0 {
		t.Fatalf("check of a clean file exited %d, want 0", code)
	}
}

func TestSelfHostedCheckShapeError(t *testing.T) {
	src := "let a = [1.0, 2.0]\nlet b = [1.0, 2.0, 3.0]\nlet c = a + b\n"
	if code := runSelfHostedCheck(t, src); code != 1 {
		t.Fatalf("check of a mismatched file exited %d, want 1", code)
	}
}

// A shape error that hinges on the value of a numeric literal -- an axis out of
// range -- exercises the number parser end to end: the self-hosted lexer reads
// the token "7", the parser turns it into the value 7, and the checker compares
// it to the rank. Before str_to_f64 the literal parsed to garbage and this
// error was silently missed, so this guards that numbers reach the checker
// intact.
func TestSelfHostedCheckCatchesBadAxis(t *testing.T) {
	if code := runSelfHostedCheck(t, "let x = zeros(2, 3)\nlet y = sum(x, 7)\n"); code != 1 {
		t.Fatalf("check of an out-of-range axis exited %d, want 1", code)
	}
}

// topk/argtopk shorten an axis to k, so a k past the axis length is a runtime
// error the self-hosted checker must catch too -- and argtopk was unreachable
// until its name was added to the front end, so this also guards that.
// The self-hosted checker gained the systems-mode type layer in 1.6.7, so the
// dialect's own types are now proved on both sides. Each case here is a
// program the Go checker rejects (or accepts); the self-hosted checker must
// reach the same verdict, since a type the bootstrap catches and the
// self-hosted one waves through is exactly the parity gap this layer closed.
func TestSelfHostedCheckSystemsTypes(t *testing.T) {
	bad := []string{
		"mode systems\nlet x: I64 = \"hello\"\n",
		"mode systems\nlet s: Str = 42\n",
		"mode systems\nlet b: Bool = 1\n",
		"mode systems\nlet y: I64 = 2.5\n",
		"mode systems\nlet xs: Arr[I64] = [\"hello\"]\n",
		"mode systems\nlet o: Opt[I64] = Some(\"s\")\n",
		"mode systems\nlet r: Res[I64, Str] = read_file(\"x\")\n",
		"mode systems\nfn f(x: Bool) -> I64 { x }\n",
		"mode systems\nfn h(n: I64) -> I64 {\n  if n > 0 { return \"neg\" }\n  n\n}\n",
		"mode systems\nfn takes(a: Str) -> Unit { print(a) }\nlet r = takes(3)\n",
		"mode systems\nstruct Lexer { src: Str, i: I64 }\nlet lx: Lexer = Lexer { src: 3, i: 0 }\n",
		"mode systems\nstruct Lexer { src: Str, i: I64 }\nlet lx: Lexer = Lexer { src: \"a\", i: 0, nope: 1 }\n",
		"mode systems\nstruct Lexer { src: Str, i: I64 }\nlet lx: Lexer = Lexer { src: \"a\", i: 0 }\nlx.i = \"x\"\n",
		"mode systems\nenum Tok { Num(I64), Word(Str), Eof }\nlet t: Tok = Num(\"x\")\n",
		"mode systems\nlet x: I64 = 5\nlet y = x.foo\n",
		"mode systems\nlet o: Opt[I64] = Some(1)\nlet b = o < 1\n",
		"mode systems\nlet s: Str = \"a\"\nlet t = s?\n",
	}
	for _, src := range bad {
		if code := runSelfHostedCheck(t, src); code != 1 {
			t.Errorf("check of %q exited %d, want 1", src, code)
		}
	}
	good := []string{
		"mode systems\nlet n: I64 = 3\nlet m: F64 = 3\nlet k: I64 = m / 2\n",
		"mode systems\nlet n: I64 = 9223372036854775807\n",
		"mode systems\nfn takes(a: Str) -> Unit { print(a) }\nlet s: Str = \"x\"\nlet r = takes(s)\n",
		"mode systems\nfn head(xs: Arr[I64]) -> I64 = xs[0]\nlet ys: Arr[I64] = arr_new()\nlet r = head(ys)\n",
		"mode systems\nfn idc(c: cp.Caps) -> cp.Caps = c\nlet x = 1.0\nlet r = idc(x)\n",
		"mode systems\nlet r: Res[Str, Str] = read_file(\"x\")\nlet o: Opt[I64] = i64_of_str(\"3\")\n",
		"mode systems\nenum Tok { Num(I64), Word(Str), Eof }\nlet t: Tok = Num(3)\nmatch t { Num(n) => print(n + 1), Word(w) => print(w + \"!\"), Eof => print(\"e\") }\n",
		"let b: Bool = true\nlet r = b\n",
		"let n: I64 = 3\nlet r = n\n",
	}
	for _, src := range good {
		if code := runSelfHostedCheck(t, src); code != 0 {
			t.Errorf("check of %q exited %d, want 0", src, code)
		}
	}
}

// A `fn(...)` type annotation is a parameter type the ecosystem writes and the
// self-hosted parser could not read: it reported a syntax error where the
// bootstrap parsed the signature, so nine repositories' worth of files were
// never checked self-hosted at all. The annotation is advisory text on both
// sides, so what this guards is that it parses and that a call through it is
// judged by its arity.
func TestSelfHostedCheckFunctionTypeAnnotations(t *testing.T) {
	good := "mode systems\nfn apply(g: fn(I64) -> Str, n: I64) -> Str = g(n)\n"
	if code := runSelfHostedCheck(t, good); code != 0 {
		t.Errorf("check of a function-typed parameter exited %d, want 0", code)
	}
	bad := "mode systems\nfn apply(g: fn(I64) -> Str) -> Str = g(1)\nlet r = apply(1)\n"
	if code := runSelfHostedCheck(t, bad); code != 1 {
		t.Errorf("check of a number passed where a function is declared exited %d, want 1", code)
	}
}

func TestSelfHostedCheckCatchesTopKBounds(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = topk(zeros(3), 5)\n"); code != 1 {
		t.Fatalf("check of topk k past the axis exited %d, want 1", code)
	}
	if code := runSelfHostedCheck(t, "let y = argtopk(zeros(3), 5)\n"); code != 1 {
		t.Fatalf("check of argtopk k past the axis exited %d, want 1", code)
	}
	if code := runSelfHostedCheck(t, "let y = topk(zeros(3), 2)\n"); code != 0 {
		t.Fatalf("check of a valid topk exited %d, want 0", code)
	}
}

// split was unreachable until its name was added to the front end; both
// checkers must now accept a valid split rather than call it an unknown name.
func TestSelfHostedCheckAcceptsSplit(t *testing.T) {
	if code := runSelfHostedCheck(t, "let x = zeros(6)\nlet p = split(x, 2, 0)\n"); code != 0 {
		t.Fatalf("check of a valid split exited %d, want 0", code)
	}
	// The same bounds mismatches the runtime raises are caught statically, and the
	// self-hosted checker must agree: sizes that do not sum, and a bad divisor.
	if code := runSelfHostedCheck(t, "let x = zeros(6)\nlet p = split(x, [2, 2], 0)\n"); code != 1 {
		t.Fatalf("check of a split with sizes that do not sum exited %d, want 1", code)
	}
	if code := runSelfHostedCheck(t, "let x = zeros(7)\nlet p = split(x, 3, 0)\n"); code != 1 {
		t.Fatalf("check of a split count that does not divide exited %d, want 1", code)
	}
}

// The fixed-arity table catches the wrong argument count for any builtin the
// runtime registers with a set arity; the self-hosted checker owns the same
// table and must agree. A same-named user function keeps its own arity, and a
// variadic builtin is not constrained.
func TestSelfHostedCheckCatchesFixedArity(t *testing.T) {
	if code := runSelfHostedCheck(t, "let x = zeros(3, 8, 8)\nlet y = maxpool2d(x)\n"); code != 1 {
		t.Fatalf("check of maxpool2d with one argument exited %d, want 1", code)
	}
	if code := runSelfHostedCheck(t, "let y = clip(zeros(3), 0.0)\n"); code != 1 {
		t.Fatalf("check of clip with two arguments exited %d, want 1", code)
	}
	if code := runSelfHostedCheck(t, "let x = zeros(3, 8, 8)\nlet y = maxpool2d(x, 2)\n"); code != 0 {
		t.Fatalf("check of a valid maxpool2d exited %d, want 0", code)
	}
	// A variadic builtin (transpose takes an optional permutation) is not fixed.
	if code := runSelfHostedCheck(t, "let y = transpose(zeros(2, 3))\n"); code != 0 {
		t.Fatalf("check of a variadic transpose exited %d, want 0", code)
	}
}

// dtype(t) names a tensor's element type. Every tensor is f64 until a cast or a
// dtype-carrying constructor exists, so both interpreters must return "f64" and
// agree; this also guards that the self-hosted builtin is reachable (it was dead
// code, missing from the INSPECT dispatch list) and that Go recognises the name
// (it rejected it as unknown before dtype landed in the Go bootstrap).
func TestSelfHostedDtypeBuiltinMatches(t *testing.T) {
	goOut, selfOut := runBothWays(t, "let x = zeros(2, 3)\nprint(dtype(x))\nprint(dtype(sum(x)))\n")
	if goOut != selfOut {
		t.Fatalf("dtype output differs: Go %q vs self-hosted %q", goOut, selfOut)
	}
	if goOut != "f64f64" {
		t.Fatalf("dtype output = %q, want f64 twice", goOut)
	}
}

// An elementwise op produces the promoted dtype and rounds each element to it;
// both interpreters must agree. A comparison yields the promoted dtype, not bool
// (Go's compareOp returns a plain 0/1 tensor), so it is tested here too.
func TestSelfHostedElementwisePromotionMatches(t *testing.T) {
	src := "print(fill(0.1, 2, bf16) + ones(2, bf16))\n" +
		"print(ones(2, i8) + ones(2, i8))\n" +
		"print(greater(fill(1.0, 2, bf16), fill(2.0, 2, bf16)))\n" +
		"print(zeros(2) + ones(2))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("elementwise dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// A contraction accumulates in f32 for narrow operands and produces the promoted
// dtype. Both the general odometer and the batched-matmul fast path are covered;
// the values here are exact in bf16, so both interpreters agree byte for byte.
func TestSelfHostedContractionDtypeMatches(t *testing.T) {
	src := "let a = tensor([[1.0, 2.0]]).to(bf16)\nlet b = tensor([[1.0], [1.0]]).to(bf16)\n" +
		"print(a @ b)\nprint(dtype(a @ b))\n" +
		"print(einsum(\"ij,jk->ik\", a, b))\n" +
		"let q = reshape(tensor([1.0, 1.0, 1.0, 1.0]), 1, 2, 2).to(bf16)\n" +
		"print(einsum(\"bxc,byc->bxy\", q, q))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("contraction dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// Softmax accumulates its exponentials at the accumulation dtype and stores a
// narrow quotient; an integer input comes back as f32. Both interpreters must
// agree on the dtype and, for a bf16 result, on the printed values.
func TestSelfHostedSoftmaxDtypeMatches(t *testing.T) {
	src := "let x = tensor([1.0, 2.0, 3.0, 4.0]).to(bf16)\n" +
		"print(dtype(softmax(x, 0)))\nprint(softmax(x, 0))\n" +
		"print(dtype(softmax(ones(3, i8), 0)))\n" +
		"print(dtype(softmax(zeros(4), 0)))\n" +
		"print(dtype(logsumexp(x, 0)))\nprint(logsumexp(x, 0))\n" +
		"print(dtype(logsumexp(ones(3, i8), 0)))\nprint(logsumexp(zeros(4), 0))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("softmax dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// Scans keep the input dtype: cumsum/cumprod accumulate at the accumulation
// dtype and store the result dtype, cummax/cummin carry the chosen element
// through, and diff is a single rounded subtraction. Both interpreters agree.
func TestSelfHostedScanDtypeMatches(t *testing.T) {
	src := "let x = tensor([1.0, 2.0, 3.0, 4.0]).to(bf16)\n" +
		"print(dtype(cumsum(x)))\nprint(cumsum(x))\n" +
		"print(dtype(cumprod(x)))\nprint(cumprod(x))\n" +
		"print(dtype(cummax(x)))\nprint(cummax(x))\n" +
		"print(dtype(diff(x, 0)))\nprint(diff(x, 0))\n" +
		"print(cumsum(zeros(3)))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("scan dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// A rearrangement carries elements through unchanged, so it keeps the input
// dtype (concat promotes its pieces); both interpreters must agree.
func TestSelfHostedRearrangementDtypeMatches(t *testing.T) {
	src := "let x = tensor([1.0, 2.0, 3.0, 4.0]).to(bf16)\n" +
		"print(dtype(transpose(reshape(x, 2, 2))))\nprint(dtype(flip(x, 0)))\n" +
		"print(dtype(gather(x, [0, 2])))\n" +
		"print(dtype(concat([tensor([1.0]).to(bf16), tensor([2.0]).to(bf16)], 0)))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("rearrangement dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// Selections and slices carry their chosen elements through unchanged, so they
// keep the input dtype; where promotes its two branches. Both interpreters must
// agree on the tag and the printed values.
func TestSelfHostedSelectionDtypeMatches(t *testing.T) {
	src := "let x = tensor([3.0, 1.0, 4.0, 1.5]).to(bf16)\n" +
		"print(dtype(sort(x)))\nprint(sort(x))\n" +
		"print(dtype(topk(x, 2)))\nprint(topk(x, 2))\n" +
		"let m = reshape(tensor([1.0, 2.0, 3.0, 4.0, 5.0, 6.0]), 3, 2).to(bf16)\n" +
		"print(dtype(m[1]))\nprint(m[1])\n" +
		"print(dtype(m[0:2]))\n" +
		"let w = where(tensor([1.0, 0.0]), tensor([2.0, 2.0]).to(bf16), tensor([9.0, 9.0]).to(bf16))\n" +
		"print(dtype(w))\nprint(w)\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("selection dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// A reduction runs at the accumulation dtype and stores the result dtype, and a
// selection (max/min/median) carries the input dtype through unrounded. Both
// interpreters must agree, including mean of an integer promoting to f32.
func TestSelfHostedReductionPromotionMatches(t *testing.T) {
	src := "print(dtype(sum(fill(0.1, 4, bf16))))\nprint(dtype(mean(ones(4, i8))))\n" +
		"print(dtype(prod(fill(2.0, 3, bf16))))\nprint(dtype(max(fill(0.1, 4, bf16))))\n" +
		"print(sum(fill(0.1, 4, bf16)))\nprint(median(fill(0.1, 4, bf16)))\nprint(sum(zeros(4)))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("reduction dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// A unary op keeps a float operand's dtype and rounds to it; both interpreters
// must agree, and f64 is left untouched.
func TestSelfHostedUnaryPromotionMatches(t *testing.T) {
	src := "print(relu(fill(0.5, 2, bf16)))\nprint(exp(fill(1.0, 2, bf16)))\n" +
		"print(square(fill(0.1, 2, bf16)))\nprint(relu(zeros(2)))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("unary dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// An operation whose result is an index produces i32 (docs/dtypes.md), so
// argmax/argsort/argtopk print with a dtype=i32 tag. This closes a long-standing
// divergence: the self-hosted evaluator always tagged them and the Go bootstrap,
// with no dtype, did not.
func TestSelfHostedIndexOpsAreI32(t *testing.T) {
	src := "print(argmax(tensor([[3.0, 1.0], [2.0, 5.0]]), 1))\n" +
		"print(argsort(tensor([3.0, 1.0, 4.0])))\n" +
		"print(argtopk(tensor([3.0, 1.0, 4.0, 1.5]), 2))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("index-op dtype differs:\n Go  %q\n self %q", goOut, selfOut)
	}
	if !strings.Contains(goOut, "dtype=i32") {
		t.Fatalf("expected an i32 tag, got %q", goOut)
	}
}

// Printing a narrow tensor shows the dtype tag and the dtype's shortest decimal,
// and both interpreters must agree. Positive values only: the self-hosted cast
// has a NEEDS-2 sign bug, so negatives are not a valid parity reference.
func TestSelfHostedNarrowPrintMatches(t *testing.T) {
	src := "print(fill(0.1, 3, bf16))\nprint(fill(3.14159, 2, f16))\nprint(ones(2, i8))\nprint(zeros(2, 2))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("narrow print differs:\n Go  %q\n self %q", goOut, selfOut)
	}
}

// A dtype-carrying constructor tags its tensor, and dtype() reads the tag back;
// both interpreters must agree on the name for every maker and both arities.
func TestSelfHostedConstructorDtypeMatches(t *testing.T) {
	src := "print(dtype(zeros(2, 3, bf16)))\nprint(dtype(eye(3, f32)))\n" +
		"print(dtype(scalar(1.5, i8)))\nprint(dtype(ones(4)))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("constructor dtype differs: Go %q vs self-hosted %q", goOut, selfOut)
	}
	if goOut != "bf16f32i8f64" {
		t.Fatalf("constructor dtype output = %q", goOut)
	}
}

// quantize packs a 2-D weight; the self-hosted checker owns the same rank rule
// even though its evaluator leaves quantize unimplemented (it types the result
// Unknown), so the check must still fire and a valid weight must pass.
func TestSelfHostedCheckCatchesQuantizeRank(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = quantize(zeros(5))\n"); code != 1 {
		t.Fatalf("check of quantize on a rank-1 weight exited %d, want 1", code)
	}
	if code := runSelfHostedCheck(t, "let y = quantize(zeros(4, 8))\n"); code != 0 {
		t.Fatalf("check of quantize on a 2-D weight exited %d, want 0", code)
	}
}

// A constant gather index past the first dimension is the runtime's
// out-of-range error; the self-hosted checker must fold it just as the Go one.
func TestSelfHostedCheckCatchesGatherIndex(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = gather(zeros(3, 2), [7])\n"); code != 1 {
		t.Fatalf("check of a gather index out of range exited %d, want 1", code)
	}
	if code := runSelfHostedCheck(t, "let y = gather(zeros(3, 2), [2, 0])\n"); code != 0 {
		t.Fatalf("check of an in-range gather exited %d, want 0", code)
	}
}

// An empty list handed to concat has nothing to join; the self-hosted checker
// must reject it just as the Go one does, keying on the literal.
func TestSelfHostedCheckCatchesEmptyConcat(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = concat([], 0)\n"); code != 1 {
		t.Fatalf("check of an empty concat exited %d, want 1", code)
	}
	if code := runSelfHostedCheck(t, "let y = concat([zeros(2), ones(3)], 0)\n"); code != 0 {
		t.Fatalf("check of a valid concat exited %d, want 0", code)
	}
}

// Loop-control checking (docs/language-guide.md, break/continue) is one of the
// non-shape diagnostics the checker owns, so the self-hosted checker must agree
// with the Go one on it: a break outside any loop is an error, a break inside a
// loop is clean, and a break in a function nested in a loop is an error because
// it cannot cross the function boundary.
func TestSelfHostedCheckCatchesBreakOutsideLoop(t *testing.T) {
	if code := runSelfHostedCheck(t, "let x = 1\nbreak\n"); code != 1 {
		t.Fatalf("check of a break outside a loop exited %d, want 1", code)
	}
}

func TestSelfHostedCheckAllowsBreakInsideLoop(t *testing.T) {
	if code := runSelfHostedCheck(t, "for i in range(3) {\n  if i == 1 { break }\n}\n"); code != 0 {
		t.Fatalf("check of a break inside a loop exited %d, want 0", code)
	}
}

func TestSelfHostedCheckCatchesBreakAcrossFunctionBoundary(t *testing.T) {
	src := "for i in range(3) {\n  let f = fn() { break }\n  f()\n}\n"
	if code := runSelfHostedCheck(t, src); code != 1 {
		t.Fatalf("check of a break across a function boundary exited %d, want 1", code)
	}
}

// transpose is the axis-taking builtin that used to stay silent on an
// out-of-range axis (NEEDS-50). The self-hosted checker must report it exactly
// as the Go one does, so a permutation naming a nonexistent axis fails the check
// in both.
func TestSelfHostedCheckCatchesBadTransposeAxis(t *testing.T) {
	if code := runSelfHostedCheck(t, "let x = zeros(2, 3)\nlet y = transpose(x, 0, 5)\n"); code != 1 {
		t.Fatalf("check of an out-of-range transpose axis exited %d, want 1", code)
	}
}

func TestSelfHostedCheckAllowsValidTranspose(t *testing.T) {
	if code := runSelfHostedCheck(t, "let x = zeros(2, 3)\nlet y = transpose(x, 1, 0)\n"); code != 0 {
		t.Fatalf("check of a valid transpose exited %d, want 0", code)
	}
}

// `@` has no batched form: the self-hosted checker rejects a rank-3 operand
// exactly as the Go one does, and passes the 2-D form.
func TestSelfHostedCheckRejectsRankThreeMatmul(t *testing.T) {
	if code := runSelfHostedCheck(t, "let a = zeros(5, 2, 3)\nlet b = zeros(5, 3, 4)\nlet c = a @ b\n"); code != 1 {
		t.Fatalf("check of a rank-3 matmul exited %d, want 1", code)
	}
}

func TestSelfHostedCheckAllowsTwoDMatmul(t *testing.T) {
	if code := runSelfHostedCheck(t, "let c = zeros(2, 3) @ zeros(3, 4)\n"); code != 0 {
		t.Fatalf("check of a 2-D matmul exited %d, want 0", code)
	}
}

// softmax's out-of-range axis is a diagnostic in the self-hosted checker too,
// and a valid axis is clean.
func TestSelfHostedCheckRejectsBadSoftmaxAxis(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = softmax(zeros(2, 3), 5)\n"); code != 1 {
		t.Fatalf("check of a bad softmax axis exited %d, want 1", code)
	}
}

func TestSelfHostedCheckAllowsGoodSoftmaxAxis(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = softmax(zeros(2, 3), 1)\n"); code != 0 {
		t.Fatalf("check of a valid softmax axis exited %d, want 0", code)
	}
}

// conv2d's three shape contracts are enforced by the self-hosted checker too.
func TestSelfHostedCheckRejectsBadConv2d(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = conv2d(zeros(3, 8, 8), zeros(4, 5, 3, 3))\n"); code != 1 {
		t.Fatalf("check of a conv2d channel mismatch exited %d, want 1", code)
	}
}

func TestSelfHostedCheckAllowsValidConv2d(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = conv2d(zeros(3, 8, 8), zeros(4, 3, 3, 3))\n"); code != 0 {
		t.Fatalf("check of a valid conv2d exited %d, want 0", code)
	}
}

// The shape-preserving axis ops (flip/cumsum/sort/roll) validate their axis in
// the self-hosted checker too, including roll's shift-first argument order.
func TestSelfHostedCheckRejectsBadRollAxis(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = roll(zeros(2, 3), 1, 5)\n"); code != 1 {
		t.Fatalf("check of a bad roll axis exited %d, want 1", code)
	}
}

func TestSelfHostedCheckAllowsValidFlip(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = flip(zeros(2, 3), 1)\n"); code != 0 {
		t.Fatalf("check of a valid flip exited %d, want 0", code)
	}
}

// broadcast_to compatibility and the list(...) shape form are checked by the
// self-hosted checker too.
func TestSelfHostedCheckRejectsBadBroadcast(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = broadcast_to(zeros(4), list(2, 3))\n"); code != 1 {
		t.Fatalf("check of an incompatible broadcast exited %d, want 1", code)
	}
}

func TestSelfHostedCheckReadsListShapeForReshape(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = reshape(zeros(6), list(2, 4))\n"); code != 1 {
		t.Fatalf("check of a bad reshape via list(...) exited %d, want 1", code)
	}
}

func TestSelfHostedCheckAllowsValidBroadcast(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = broadcast_to(zeros(3), list(2, 3))\n"); code != 0 {
		t.Fatalf("check of a valid broadcast exited %d, want 0", code)
	}
}

// A constant out-of-range index is caught by the self-hosted checker too.
func TestSelfHostedCheckRejectsOutOfRangeIndex(t *testing.T) {
	if code := runSelfHostedCheck(t, "let y = zeros(3)[3]\n"); code != 1 {
		t.Fatalf("check of an out-of-range index exited %d, want 1", code)
	}
}

func TestSelfHostedCheckRejectsScalarMatmul(t *testing.T) {
	if code := runSelfHostedCheck(t, "let c = scalar(2.0) @ zeros(3)\n"); code != 1 {
		t.Fatalf("check of a scalar matmul exited %d, want 1", code)
	}
}

// I64 semantics -- exact values above 2^53, wrapping arithmetic, truncating
// division, dividend-signed modulo, and bitwise words on the sign bit -- must
// print identically on the bootstrap and the self-hosted evaluator.
func TestSelfHostedI64Matches(t *testing.T) {
	src := `mode systems
let big: I64 = 9007199254740993
print(big)
print(9223372036854775807)
print(-9223372036854775808)
print(9223372036854775807 + 1)
print(-9223372036854775808 - 1)
let neg: I64 = -7
print(neg / 2)
print(neg % 2)
print(7 % neg)
print(neg // 2)
let m: I64 = -9223372036854775808
print(m / (0 - 1))
print(i64(2.9))
print(f64(big))
print(str(big))
print(big == 9007199254740992)
print(big > 9007199254740992)
print(big + 1)
print(big + 0.5)
print(1 shl 63)
print((1 shl 63) shr 1)
print(bnot(0))
print(band(m, -1))
fn half(x: I64) -> I64 { x / 2 }
print(half(-9))
`
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("I64 output diverged:\n  go:   %q\n  self: %q", goOut, selfOut)
	}
}

// The exhaustiveness check on match: the self-hosted checker reports a missing
// variant and accepts a covered enum, matching the Go checker's verdicts.
func TestSelfHostedCheckCatchesNonExhaustiveMatch(t *testing.T) {
	bad := "mode systems\nenum V { A, B, C }\nfn f(v: V) -> I64 {\n  match v { A => 1, B => 2 }\n}\n"
	if code := runSelfHostedCheck(t, bad); code != 1 {
		t.Fatalf("check of a non-exhaustive match exited %d, want 1", code)
	}
	good := "mode systems\nenum V { A, B, C }\nfn f(v: V) -> I64 {\n  match v { A => 1, B => 2, C => 3 }\n}\n"
	if code := runSelfHostedCheck(t, good); code != 0 {
		t.Fatalf("check of an exhaustive match exited %d, want 0", code)
	}
}

// The `?` context rule: a top-level `?`, or one in a function whose return
// type is not Res or Opt, is a diagnostic in the self-hosted checker too.
func TestSelfHostedCheckCatchesTryContext(t *testing.T) {
	top := "mode systems\nfn p() -> Res[I64, Str] { Ok(1) }\nlet v = p()?\n"
	if code := runSelfHostedCheck(t, top); code != 1 {
		t.Fatalf("check of a top-level `?` exited %d, want 1", code)
	}
	wrong := "mode systems\nfn p() -> Res[I64, Str] { Ok(1) }\nfn bad() -> I64 { p()? }\n"
	if code := runSelfHostedCheck(t, wrong); code != 1 {
		t.Fatalf("check of `?` in an I64-returning function exited %d, want 1", code)
	}
	good := "mode systems\nfn p() -> Res[I64, Str] { Ok(1) }\nfn ok() -> Res[I64, Str] {\n  let v: I64 = p()?\n  Ok(v)\n}\n"
	if code := runSelfHostedCheck(t, good); code != 0 {
		t.Fatalf("check of `?` in a Res-returning function exited %d, want 0", code)
	}
}

// runBothWaysAsCLI compares the two engines the way a user meets them: through
// RunFileMain on each side. runBothWays runs the Go half with Run, which
// evaluates a top level and nothing else, and that is symmetric only for a
// numeric-mode program -- exactly the kind it is used for. A systems-mode
// program has an entry point, so comparing it needs both sides to look for one.
func runBothWaysAsCLI(t *testing.T, source string) (goOut, selfOut string) {
	skipUnderShort(t)
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	var goBuf strings.Builder
	goIP := interp.New(func(s string) { goBuf.WriteString(s) })
	if _, _, err := goIP.RunFileMain(target, nil); err != nil {
		t.Fatalf("Go interpreter errored: %v", err)
	}

	var selfBuf strings.Builder
	selfIP := interp.New(func(s string) { selfBuf.WriteString(s) })
	if _, ranMain, err := selfIP.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "run", target}); err != nil {
		t.Fatalf("self-hosted CLI errored: %v", err)
	} else if !ranMain {
		t.Fatal("self-hosted main did not run")
	}
	return goBuf.String(), selfBuf.String()
}

// A systems-mode program's entry point is main(), which is what RunFileMain
// documents and what the self-hosted CLI did not do: `twill run` on a
// systems-mode file executed the declarations and stopped, so the self-hosted
// side could not run the dialect the self-hosted compiler is written in. Every
// program the rest of this harness compares is numeric mode, where there is no
// main to call, which is why nothing caught it.
func TestSelfHostedRunsSystemsModeMain(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, "mode systems\nfn main() {\n  print(\"from main\")\n}\n")
	if goOut != "from main" || selfOut != goOut {
		t.Fatalf("go = %q, self = %q, want both %q", goOut, selfOut, "from main")
	}
}

// A file with no main, or whose main takes arguments, runs its top level and
// nothing more -- on both sides.
func TestSelfHostedSystemsModeWithoutAMain(t *testing.T) {
	for _, src := range []string{
		"mode systems\nprint(\"top level\")\n",
		"mode systems\nprint(\"top level\")\nfn main(x: I64) {\n  print(\"not the entry\")\n}\n",
	} {
		goOut, selfOut := runBothWaysAsCLI(t, src)
		if goOut != "top level" || selfOut != goOut {
			t.Errorf("go = %q, self = %q, want both %q\nsource:\n%s", goOut, selfOut, "top level", src)
		}
	}
}

// Control flow crossing an expression boundary. The self-hosted evaluator
// reports each step's outcome as a Flow value rather than unwinding with a
// panic the way the bootstrap does, and every expression boundary used to
// collapse that Flow to a plain value -- so a `return` inside an `if` computed
// its value and threw it away, a `break` inside an `if` never left its loop,
// and a failing `?` became the expression's value and the function carried on.
// All three are silent wrong answers, which is the worst thing a second
// implementation of a language can produce.

func TestSelfHostedReturnInsideAnIf(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, `mode systems
fn f() -> I64 {
  if true { return 9 }
  0
}
fn main() { print(f()) }
`)
	if goOut != "9" || selfOut != goOut {
		t.Fatalf("go = %q, self = %q, want both %q", goOut, selfOut, "9")
	}
}

func TestSelfHostedBreakInsideAnIf(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, `mode systems
fn main() {
  let i: I64 = 0
  while i < 3 {
    if i == 1 { break }
    print(i)
    i = i + 1
  }
  print("done")
}
`)
	if goOut != "0done" || selfOut != goOut {
		t.Fatalf("go = %q, self = %q, want both %q", goOut, selfOut, "0done")
	}
}

// `?` returns the failure from the enclosing function, from wherever it is
// written: at the top of a body, inside an `if`, and inside a loop.
func TestSelfHostedTryPropagates(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, `mode systems
fn ok1() -> Res[I64, Str] { Ok(7) }
fn bad() -> Res[I64, Str] { Err("no") }
fn nothing() -> Opt[I64] { None }
fn chain() -> Res[I64, Str] {
  let a: I64 = ok1()?
  let b: I64 = ok1()?
  Ok(a + b)
}
fn stops() -> Res[I64, Str] {
  let a: I64 = ok1()?
  let b: I64 = bad()?
  print("must not print")
  Ok(a + b)
}
fn inif(c: Bool) -> Res[I64, Str] {
  if c {
    let v: I64 = bad()?
    return Ok(v)
  }
  Ok(1)
}
fn inloop() -> Res[I64, Str] {
  let i: I64 = 0
  while i < 3 {
    let v: I64 = bad()?
    i = i + 1
  }
  Ok(i)
}
fn opt() -> Opt[I64] {
  let v: I64 = nothing()?
  Some(v + 1)
}
fn main() {
  match chain() { Ok(v) => print("chain", v), Err(e) => print("chain err", e) }
  match stops() { Ok(v) => print("stops", v), Err(e) => print("stops err", e) }
  match inif(true) { Ok(v) => print("inif", v), Err(e) => print("inif err", e) }
  match inloop() { Ok(v) => print("inloop", v), Err(e) => print("inloop err", e) }
  match opt() { Some(v) => print("opt", v), None => print("opt none") }
}
`)
	if selfOut != goOut {
		t.Fatalf("go = %q, self = %q", goOut, selfOut)
	}
	for _, want := range []string{"chain 14", "stops err no", "inif err no", "inloop err no", "opt none"} {
		if !strings.Contains(goOut, want) {
			t.Errorf("output %q is missing %q", goOut, want)
		}
	}
	if strings.Contains(goOut, "must not print") {
		t.Error("a failing `?` did not stop the function")
	}
}

// A `return` from inside a match arm reaches the function, and the arm's own
// value is still its value when it does not return.
func TestSelfHostedReturnInsideAMatchArm(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, `mode systems
fn f(o: Opt[I64]) -> I64 {
  match o {
    Some(v) => {
      if v > 0 { return v * 10 }
      6
    },
    None => 7,
  }
}
fn main() { print(f(Some(4)), f(Some(0)), f(None)) }
`)
	if goOut != "40 6 7" || selfOut != goOut {
		t.Fatalf("go = %q, self = %q, want both %q", goOut, selfOut, "40 6 7")
	}
}

// jvp, vjp and hvp are language operations, so the reference implementation owes
// the same answers as the bootstrap. The source below is the whole contract in
// one file: a scalar output and a tensor one, a record parameter tree and a
// nested list, the cross-checks against grad, jacobian and hessian, and the two
// zero-derivative cases where a refusal would be stricter than grad.
func TestSelfHostedJVPVJPHVPMatch(t *testing.T) {
	src := `let p = { w: [[1.0, 2.0], [3.0, 4.0]], b: [0.5, -0.5] }
let X = [[1.0, 2.0], [0.5, 1.5]]
fn loss(p) = sum(square(tanh(X @ p.w + p.b))) + sum(exp(p.b * 0.5))
let dp = { w: [[0.1, -0.2], [0.3, 0.05]], b: [1.0, -1.0] }
print(grads(loss)(p))
print(jvp(loss)(p, dp))
print(vjp(loss)(p, 1.0))
let xs = list([1.0, 2.0], [0.3, -0.7, 1.5])
fn g(x) = sum(square(x[0])) + sum(exp(x[1] * 0.5))
print(jvp(g)(xs, list([1.0, -0.5], [0.25, 2.0, -1.0])))
print(vjp(g)(xs, 1.0))
fn h(x) = x * x
print(jvp(h)([1.0, 2.0, 3.0], [0.5, -1.0, 2.0]))
print(vjp(h)([1.0, 2.0, 3.0], [1.0, 1.0, 1.0]))
print(jacobian(h)([1.0, 2.0, 3.0]))
fn q(x) = sum(x * x * x)
print(hvp(q)([1.0, 2.0, 3.0], [0.5, -1.0, 2.0]))
print(hessian(q)([1.0, 2.0, 3.0]))
fn c(x) = sum(square(floor(x)))
print(jvp(c)([1.5, 2.5], [1.0, 1.0]))
print(hvp(c)([1.5, 2.5], [1.0, 1.0]))
fn ignores(x) = 3.0
print(jvp(ignores)([1.5, 2.5], [1.0, 1.0]))
print(hvp(ignores)([1.5, 2.5], [1.0, 1.0]))
`
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("jvp/vjp/hvp output differs:\nGo:   %q\nself: %q", goOut, selfOut)
	}
	if !strings.Contains(goOut, "tensor([3, -12, 36], shape=[3])") {
		t.Errorf("hvp of sum(x³) at [1,2,3] along [0.5,-1,2] should be 6*x*v: %q", goOut)
	}
}

// The refusals are part of the contract too: a program the bootstrap refuses has
// to be refused by the reference as well, or the same source passes on one and
// fails on the other. The self-hosted CLI reports a runtime error by printing it
// and returning 1, and the exit code is what is checked here; the messages
// themselves are pinned against the bootstrap's in jvp_test.go.
func TestSelfHostedJVPRefusalsMatch(t *testing.T) {
	for _, src := range []string{
		"fn f(x) = sum(square(x))\nprint(hvp(grad(f))([1.0, 2.0], [1.0, 0.0]))\n",
		"fn f(p) = sum(p.w)\nprint(jvp(f)({ w: [1.0] }, { b: [1.0] }))\n",
		"fn f(x) = sum(x[0]) + sum(x[1])\nprint(jvp(f)(list([1.0], [2.0]), list([1.0])))\n",
		"fn f(x) = sum(square(x))\nprint(jvp(f)([1.0, 2.0], [1.0, 2.0, 3.0]))\n",
		"fn f(x) = sum(square(x))\nprint(vjp(f)([1.0, 2.0], [1.0, 2.0]))\n",
		"fn f(x) = x * x\nprint(hvp(f)([1.0, 2.0], [1.0, 0.0]))\n",
		"fn f(x) = sum(einsum(\"ij,jk->ik\", x, x))\nprint(hvp(f)([[1.0, 2.0], [3.0, 4.0]], [[1.0, 0.0], [0.0, 0.0]]))\n",
	} {
		ip := interp.New(func(string) {})
		if _, err := ip.Run(src); err == nil {
			t.Errorf("the bootstrap answered %q instead of refusing it", src)
		}
		if code := runSelfHostedRun(t, src); code == 0 {
			t.Errorf("the self-hosted evaluator answered %q instead of refusing it", src)
		}
	}
}

// runSelfHostedRun runs a program through the self-hosted CLI and returns its
// exit code: 0 for a program that ran, 1 for one it refused.
func runSelfHostedRun(t *testing.T, src string) int {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ip := interp.New(func(string) {})
	result, ranMain, err := ip.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "run", target})
	if err != nil {
		t.Fatalf("self-hosted CLI errored: %v", err)
	}
	if !ranMain {
		t.Fatal("self-hosted main did not run")
	}
	n, ok := value.AsNumber(result)
	if !ok {
		t.Fatalf("self-hosted main returned a non-number: %v", result)
	}
	return int(n)
}

// A gradient taken inside a gradient is refused by both engines, with the same
// words. The bootstrap has carried a depth counter since the refusal was
// written; the self-hosted evaluator had only the direct check on
// `grad(grad(f))`, which misses the nesting written the long way round -- a
// `let g = grad(f)` with a user function between the two. It answered [0, 0],
// which is the silent zero docs/DECISIONS.md says the refusal exists to
// prevent.
func TestSelfHostedRefusesAGradientInsideAGradient(t *testing.T) {
	dir := t.TempDir()
	const src = `fn g(x) = sum(x * x)
fn outer(x) = sum(grad(g)(x))
print(grad(outer)([1.0, 2.0]))
`
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	goIP := interp.New(func(string) {})
	_, _, goErr := goIP.RunFileMain(target, nil)
	if goErr == nil {
		t.Fatal("the Go bootstrap accepted a gradient inside a gradient")
	}

	var selfOut strings.Builder
	selfIP := interp.New(func(s string) { selfOut.WriteString(s) })
	if _, _, err := selfIP.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "run", target}); err != nil {
		t.Fatalf("self-hosted CLI errored: %v", err)
	}
	// It must not have printed an answer, and least of all a zero.
	if got := selfOut.String(); got != "" {
		t.Errorf("the self-hosted evaluator answered %q; it must refuse", got)
	}
	if !strings.Contains(goErr.Error(), "gradient taken inside a gradient") {
		t.Errorf("go error = %v", goErr)
	}
}

// The ordinary derivatives are unaffected by that guard, on both engines.
func TestSelfHostedDerivativesStillAgree(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, `fn f(x) = sum(x * x * x)
let x = [1.0, 2.0, 3.0]
let v = [1.0, 0.0, 0.0]
print(grad(f)(x))
print(value_and_grad(f)(x))
print(jvp(f)(x, v))
print(vjp(f)(x, 1.0))
print(hvp(f)(x, v))
print(hessian(f)([1.0, 2.0]))
print(jacobian(fn(y) = y * y)([1.0, 2.0]))
`)
	if goOut != selfOut {
		t.Fatalf("go = %q, self = %q", goOut, selfOut)
	}
	if !strings.Contains(goOut, "3, 12, 27") {
		t.Errorf("output %q does not contain the expected gradient", goOut)
	}
}

// stop_grad puts a value outside the graph: the same numbers, no history, so a
// gradient reaching it stops. It is what a stabilisation needs when the rewrite
// it performs is not an exact algebraic identity, and it is how a
// straight-through estimator is written.
func TestSelfHostedStopGrad(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, `fn plain(x) = sum(x * x)
fn stopped(x) = sum(x * stop_grad(x))
let x = [1.0, 2.0, 3.0]
print(plain(x), stopped(x))
print(grad(plain)(x))
print(grad(stopped)(x))
fn ste(x) = sum(x + stop_grad(floor(x) - x))
print(ste([1.7, 2.3]), grad(ste)([1.7, 2.3]))
print(shape(stop_grad(zeros(2, 3))), dtype(stop_grad(zeros(2, 2, bf16))))
`)
	if goOut != selfOut {
		t.Fatalf("go = %q, self = %q", goOut, selfOut)
	}
	for _, want := range []string{
		"14 14",                        // the forward value is unchanged
		"tensor([2, 4, 6], shape=[3])", // d/dx sum(x*x) = 2x
		"tensor([1, 2, 3], shape=[3])", // the stopped factor contributes nothing, so it is x
		"3 tensor([1, 1], shape=[2])",  // the estimator: floor forward, identity backward
		"[2, 3] bf16",                  // shape and dtype ride through
	} {
		if !strings.Contains(goOut, want) {
			t.Errorf("output %q is missing %q", goOut, want)
		}
	}
}

// black_box is the compiler barrier, docs/roadmap.md entry 30. What the two
// implementations have to agree on is the value, because that is all a program
// can see: the Go interpreter's barrier has a tracer to stop and the
// self-hosted evaluator has none, so the mechanisms differ on purpose and the
// output must not. It is checked here in the same place as stop_grad, its near
// neighbour and its opposite: stop_grad looks like an identity and changes the
// gradient, black_box is an identity and changes neither the value nor the
// gradient.
func TestSelfHostedBlackBox(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, `fn plain(x) = sum(x * x)
fn boxed(x) = sum(black_box(x) * x)
let x = [1.0, 2.0, 3.0]
print(plain(x), boxed(x))
print(grad(plain)(x))
print(grad(boxed)(x))
print(black_box(3.5), black_box("abc"))
print(black_box([1.0, 2.0]))
print(shape(black_box(zeros(2, 3))), dtype(black_box(zeros(2, 2, bf16))))
`)
	if goOut != selfOut {
		t.Fatalf("go = %q, self = %q", goOut, selfOut)
	}
	for _, want := range []string{
		"14 14",                        // the barrier does not move the value
		"tensor([2, 4, 6], shape=[3])", // and it does not move the gradient either
		"3.5 abc",
		"[2, 3] bf16", // shape and dtype ride through
	} {
		if !strings.Contains(goOut, want) {
			t.Errorf("output %q is missing %q", goOut, want)
		}
	}
}

// The same tensor passed as two arguments gets a gradient for each. A node is
// found again by tensor identity, so two leaves holding the same object were
// indistinguishable: every operand inside f resolved to whichever was
// registered last, the whole gradient landed on the second parameter, and the
// first came back zeros. The bootstrap was always right; this is the reference
// implementation agreeing with it.
func TestSelfHostedGradsWithAliasedArguments(t *testing.T) {
	goOut, selfOut := runBothWaysAsCLI(t, `let a = [1.0, 2.0]
let b = [3.0, 4.0]
print(grads(fn(p, q) = sum(p * q))(a, b))
let c = [1.0, 2.0]
print(grads(fn(p, q) = sum(p * q))(c, c))
`)
	if goOut != selfOut {
		t.Fatalf("go = %q, self = %q", goOut, selfOut)
	}
	// d/dp = q and d/dq = p, so aliasing gives each parameter the other's
	// value, which is its own.
	if !strings.Contains(goOut, "[tensor([1, 2], shape=[2]), tensor([1, 2], shape=[2])]") {
		t.Errorf("output %q does not carry a gradient for both aliased parameters", goOut)
	}
}

// The dtype widening warning (NEEDS-113) was the last thing the self-hosted
// checker knew that the bootstrap did not: `bf16 * scalar(2.0)` is f64, a
// correct answer that undoes the reason the operand was narrow. Both checkers
// report it now, and -- since it is a warning and not an error -- neither lets
// it stop the program. The harder half is that everything which is NOT a lossy
// widening stays silent, so a program that never wrote a dtype gains nothing.
func TestSelfHostedCheckDTypeWidening(t *testing.T) {
	warns := []string{
		// A narrow float widened by a wider one, through a maker and through a cast.
		"mode systems\nfn f() {\n  let w = zeros(2, 3, bf16)\n  let y = w * scalar(2.0)\n}\n",
		"mode systems\nfn f() {\n  let a = zeros(2, 3).to(f16)\n  let b = zeros(2, 3).to(f32)\n  let y = a + b\n}\n",
	}
	for _, src := range warns {
		code, out := runSelfHostedCheckOut(t, src)
		if code != 0 {
			t.Errorf("check of %q exited %d, want 0: a warning does not fail a check", src, code)
		}
		if !strings.Contains(out, "warning: dtype widening:") {
			t.Errorf("check of %q did not report the widening as a warning; got %q", src, out)
		}
	}
	silent := []string{
		// Same dtype: not a widening.
		"mode systems\nfn f() {\n  let a = zeros(2, 3, bf16)\n  let b = ones(2, 3).to(bf16)\n  let y = a * b\n}\n",
		// f16 meeting bf16 promotes past both to f32, so neither was the wider one.
		"mode systems\nfn f() {\n  let a = zeros(2, 3, f16)\n  let b = zeros(2, 3, bf16)\n  let y = a * b\n}\n",
		// An integer meeting a float keeps the float: the lattice doing its job.
		"mode systems\nfn f() {\n  let a = zeros(2, 3, i8)\n  let b = zeros(2, 3, bf16)\n  let y = a * b\n}\n",
		// No dtype anywhere. A bare literal deliberately has none, so this is the
		// promise that dtype-free programs gain nothing.
		"mode systems\nfn f() {\n  let a = zeros(2, 3)\n  let y = a * ones(2, 3) + 1.0\n}\n",
		// A maker keeps every argument before its dtype as shape: the two checkers
		// used to disagree here because the self-hosted one stripped the trailing
		// name twice and read zeros(2, 3, bf16) as [2].
		"mode systems\nfn f() {\n  let a = zeros(2, 3, bf16)\n  let b = ones(2, 3, bf16)\n  let y = a * b\n}\n",
	}
	for _, src := range silent {
		code, out := runSelfHostedCheckOut(t, src)
		if code != 0 {
			t.Errorf("check of %q exited %d, want 0", src, code)
		}
		if strings.Contains(out, "warning") {
			t.Errorf("check of %q reported a warning it should not have: %q", src, out)
		}
	}
	// An error still refuses, and still calls itself a shape error.
	code, out := runSelfHostedCheckOut(t, "mode systems\nlet bad: Str = 42\n")
	if code != 1 {
		t.Errorf("check of a type error exited %d, want 1", code)
	}
	if !strings.Contains(out, "shape error:") {
		t.Errorf("a type error should print as a shape error; got %q", out)
	}
}

// `const` across the seam. The rule that refuses an assignment to one lives in
// both checkers, and the differential harness compares their messages byte for
// byte, so the Go checker's own text is what the self-hosted one has to print.
// Taking the expected message from checker.Check rather than writing it as a
// literal is the point: a reworded diagnostic on either side fails this test
// instead of quietly splitting the two implementations.
func TestSelfHostedRefusesAssigningAConst(t *testing.T) {
	const src = `mode systems
const HEX: Arr[Str] = mk()
fn mk() -> Arr[Str] {
  let a: Arr[Str] = arr_new()
  push(a, "#000")
  a
}
fn rethemed() {
  HEX = mk()
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	diags := checker.Check(prog)
	if len(diags) != 1 {
		t.Fatalf("the Go checker reported %d diagnostics, want 1: %v", len(diags), diags)
	}
	code, out := runSelfHostedCheckOut(t, src)
	if code != 1 {
		t.Errorf("self-hosted check exited %d, want 1", code)
	}
	if !strings.Contains(out, diags[0].Msg) {
		t.Errorf("the two checkers disagree.\n  go:   %s\n  self: %s", diags[0].Msg, out)
	}
}

// The other half: a `const` nothing assigns to is a clean file on both sides,
// and it evaluates the same. `const` is a binding at run time and nothing more,
// so the two evaluators have nothing to disagree about, which is worth pinning
// because the self-hosted evaluator reads the same AST node as a `let`.
func TestSelfHostedRunsAConstLikeTheBootstrap(t *testing.T) {
	if code := runSelfHostedCheck(t, "mode systems\nconst K: I64 = 41\nfn f() -> I64 = K + 1\n"); code != 0 {
		t.Errorf("check of a const nobody assigns to exited %d, want 0", code)
	}
	// No `main` here on purpose: the two CLIs disagree about whether they call
	// one (docs/BUGS.md, Open), and this is a test about `const`.
	goOut, selfOut := runBothWays(t, "mode systems\nconst GREETING: Str = \"hello\"\nfn shout() -> Str = GREETING\nprint(shout())\n")
	if goOut != selfOut {
		t.Fatalf("const output diverged:\n  go:   %q\n  self: %q", goOut, selfOut)
	}
	if !strings.Contains(goOut, "hello") {
		t.Fatalf("the const was not readable at run time: %q", goOut)
	}
}

// The rebinding rule across the seam. `const HEX` followed by `let HEX` in the
// same scope revoked constness with nothing said, and which way round the two
// lines sat decided whether the checker noticed, so both checkers now refuse
// the second binding. The expected text comes from checker.Check rather than a
// literal for the same reason as the test above: the two must not drift.
func TestSelfHostedRefusesRebindingAConst(t *testing.T) {
	const src = `mode systems
const HEX: I64 = 1
let HEX: I64 = 2
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	diags := checker.Check(prog)
	if len(diags) != 1 {
		t.Fatalf("the Go checker reported %d diagnostics, want 1: %v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Msg, "cannot be bound a second time") {
		t.Fatalf("the Go checker reported something else: %s", diags[0].Msg)
	}
	code, out := runSelfHostedCheckOut(t, src)
	if code != 1 {
		t.Errorf("self-hosted check exited %d, want 1", code)
	}
	if !strings.Contains(out, diags[0].Msg) {
		t.Errorf("the two checkers disagree.\n  go:   %s\n  self: %s", diags[0].Msg, out)
	}
}

// The rank-preserving flag is a shape change, and a shape change is exactly the
// kind of thing the two implementations can transcribe differently: one of them
// composes reduce-then-reshape in the interpreter and the other in the tensor
// kernel. Printing the result compares the shape, the values, the dtype tag and
// the diagnostics in one go.
func TestSelfHostedKeepdimsMatches(t *testing.T) {
	src := "let m = tensor([[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])\n" +
		"print(sum(m, 1, true))\nprint(sum(m, 1, false))\nprint(sum(m, 1, 1))\n" +
		"print(mean(m, 0, true))\nprint(max(m, -1, true))\nprint(min(m, 1, true))\n" +
		"print(prod(m, 1, true))\nprint(median(m, 1, true))\n" +
		"print(argmax(m, 1, true))\nprint(argmin(m, 0, true))\n" +
		"print(logsumexp(m, 1, true))\n" +
		"print(m - sum(m, 1, true) / 3.0)\n" +
		"print(grad(fn(v) = sum(sum(v, 1, true)))(m))\n"
	goOut, selfOut := runBothWays(t, src)
	if goOut != selfOut {
		t.Fatalf("keepdims differs:\n Go   %q\n self %q", goOut, selfOut)
	}
	if !strings.Contains(goOut, "shape=[2, 1]") {
		t.Fatalf("expected a kept axis in the output, got %q", goOut)
	}
}

// The record update across the seam. `{ ..base, f: v }` is parsed, checked,
// printed and evaluated by two implementations, and the conformance gate
// compares their output byte for byte, so every one of those has to agree. The
// evaluator is the half with a decision in it: what the copy shares with its
// base.
func TestSelfHostedRunsARecordUpdateLikeTheBootstrap(t *testing.T) {
	for _, src := range []string{
		// A field the update does not name keeps the base's value.
		"let base = { a: 3.0, b: 4.0 }\nprint({ ..base, b: 10.0 })\n",
		// No fields at all: a plain copy.
		"let base = { a: 3.0, b: 4.0 }\nprint({ ..base })\n",
		// Field order is the base's, and an added field goes after it.
		"let base = { z: 1.0, a: 2.0 }\nprint({ ..base, a: 3.0, m: 4.0 })\n",
		// The base is not written through.
		"let base = { a: 3.0 }\nlet m = { ..base, a: 99.0 }\nprint(base)\n",
		// A base that is itself an expression rather than a name.
		"fn mk(v) = { a: v, b: 0.0 }\nprint({ ..mk(2.0), b: 5.0 })\n",
		// Nested: the inner record is shared, which print cannot show but the
		// two implementations still have to agree about.
		"let inner = { x: 1.0 }\nlet base = { i: inner, n: 0.0 }\nprint({ ..base, n: 2.0 })\n",
		// A typed update, where the struct name rides along with the copy.
		"mode systems\nstruct P { x: I64, y: I64 }\nlet base: P = P { x: 1, y: 2 }\nprint(P { ..base, y: 5 })\n",
		// In a match arm, which is the position docs/self-hosting.md section 1.2
		// warns about: `{` there is a record and never a block, and `..` must not
		// give either parser a second reading of it.
		"mode systems\nstruct P { x: I64, y: I64 }\nfn pick(o: Opt[P], d: P) -> P {\n  match o {\n    Some(p) => P { ..p, y: 9 },\n    None => P { ..d },\n  }\n}\nlet d: P = P { x: 1, y: 2 }\nprint(pick(Some(d), d))\nprint(pick(None, d))\n",
		// The same value `with_field` builds, which is how a one-field update was
		// written before this syntax existed. Both implementations have that
		// builtin, so this is a claim about all four halves at once.
		"let base = { a: 1.0, b: 2.0 }\nprint({ ..base, b: 3.0 } == with_field(base, \"b\", 3.0))\n",
		// The shallow copy, observed: the pushed element is visible through the
		// base, on both sides.
		"mode systems\nlet base = { tags: arr_new(), n: 1 }\nlet m = { ..base, n: 2 }\npush(m.tags, \"shared\")\nprint(len(base.tags))\nprint(m.n)\n",
	} {
		goOut, selfOut := runBothWays(t, src)
		if goOut != selfOut {
			t.Errorf("record update output diverged for %q:\n  go:   %q\n  self: %q", src, goOut, selfOut)
		}
	}
}

// A base that is definitely not a record is a checker error, and the message is
// taken from checker.Check rather than written out here for the reason the
// `const` tests above give: a reworded diagnostic on either side has to fail a
// test rather than quietly split the two implementations.
func TestSelfHostedRefusesANonRecordUpdateBase(t *testing.T) {
	const src = `mode systems
fn f() {
  let n: I64 = 3
  let bad = { ..n, y: 1 }
}
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	diags := checker.Check(prog)
	if len(diags) != 1 {
		t.Fatalf("the Go checker reported %d diagnostics, want 1: %v", len(diags), diags)
	}
	code, out := runSelfHostedCheckOut(t, src)
	if code != 1 {
		t.Errorf("self-hosted check exited %d, want 1", code)
	}
	if !strings.Contains(out, diags[0].Msg) {
		t.Errorf("the two checkers disagree.\n  go:   %s\n  self: %s", diags[0].Msg, out)
	}
}

// The same for the parser: `..` anywhere but first is refused, in the same
// words, by both. This one is a syntax error rather than a diagnostic, so the
// text comes off the Go parser's error.
func TestSelfHostedRefusesAMisplacedRecordUpdateBase(t *testing.T) {
	const src = `let base = { x: 1.0 }
let m = { y: 2.0, ..base }
`
	_, err := parser.Parse(src)
	if err == nil {
		t.Fatal("the Go parser accepted a base written last")
	}
	syn, ok := err.(*lexer.SyntaxError)
	if !ok {
		t.Fatalf("expected a *lexer.SyntaxError, got %T", err)
	}
	code, out := runSelfHostedCheckOut(t, src)
	if code != 1 {
		t.Errorf("self-hosted check exited %d, want 1", code)
	}
	if !strings.Contains(out, syn.Msg) {
		t.Errorf("the two parsers disagree.\n  go:   %s\n  self: %s", syn.Msg, out)
	}
}

// The formatter is the third implementation of the syntax, and it too is
// doubled. `twill fmt` on either side must print the same text, or a formatted
// file means one thing to one implementation and another to the other.
func TestSelfHostedFormatsARecordUpdateLikeTheBootstrap(t *testing.T) {
	skipUnderShort(t)
	const src = `let base = { a: 1.5, b: 2.5 }
let m = { ..base, b: 3.5 }
let c = { ..base }
`
	want, err := format.Source(src)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "input.tw")
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// The self-hosted CLI prints formatted source through write_out, which goes
	// to the real os.Stdout rather than the interpreter's sink, so it is captured
	// at the OS level the way runSelfHostedCheckOut captures stderr.
	saved := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()
	ip := interp.New(func(string) {})
	_, ranMain, runErr := ip.RunFileMain(filepath.Join("..", "..", "src", "main.tw"),
		[]string{"twill", "fmt", target})
	w.Close()
	os.Stdout = saved
	got := <-done
	if runErr != nil {
		t.Fatalf("self-hosted CLI errored: %v", runErr)
	}
	if !ranMain {
		t.Fatal("self-hosted main did not run")
	}
	if got != want {
		t.Errorf("the two formatters disagree.\n  go:\n%s\n  self:\n%s", want, got)
	}
}

// docs/language-guide.md tells the reader what a third argument to the three
// shape-preserving ops does, and it does not do the same thing for all three.
// `flip` and `diff` refuse it; `softmax` has never counted its arguments and
// ignores it, so a `keepdims` written on a softmax is silently nothing. That
// asymmetry is older than the flag and is documented rather than fixed, which
// only works if it is pinned: this is the test that turns the paragraph red if
// either half of it stops being true, on either implementation.
func TestSelfHostedThirdArgumentToShapePreservingOps(t *testing.T) {
	const m = "let m = tensor([[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])\n"

	// softmax ignores it, and both implementations ignore it identically: the
	// answer is the two-argument one, shape and all.
	plainGo, plainSelf := runBothWays(t, m+"print(softmax(m, 1))\n")
	if plainGo != plainSelf {
		t.Fatalf("softmax(m, 1) differs:\n Go   %q\n self %q", plainGo, plainSelf)
	}
	for _, src := range []string{
		m + "print(softmax(m, 1, true))\n",
		m + "print(softmax(m, 1, true, 9))\n",
	} {
		goOut, selfOut := runBothWays(t, src)
		if goOut != selfOut {
			t.Errorf("%q differs:\n Go   %q\n self %q", src, goOut, selfOut)
		}
		if goOut != plainGo {
			t.Errorf("%q should be softmax(m, 1) ignoring the extra argument, got %q want %q",
				src, goOut, plainGo)
		}
	}

	// flip and diff refuse it, on both implementations, with the arity message.
	for _, tc := range []struct{ src, msg string }{
		{m + "print(flip(m, 1, true))\n", "flip expects (tensor[, axis])"},
		{m + "print(diff(m, 1, true))\n", "diff expects (tensor[, axis])"},
	} {
		ip := interp.New(func(string) {})
		_, err := ip.Run(tc.src)
		if err == nil {
			t.Errorf("the bootstrap accepted %q instead of refusing it", tc.src)
		} else if !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("the bootstrap refused %q with %q, want it to contain %q", tc.src, err, tc.msg)
		}
		if code := runSelfHostedRun(t, tc.src); code == 0 {
			t.Errorf("the self-hosted evaluator accepted %q instead of refusing it", tc.src)
		}
	}
}
