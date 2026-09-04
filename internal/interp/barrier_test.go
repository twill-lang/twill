package interp_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
)

// The compiler barrier: docs/roadmap.md entry 30, bobbin docs/needs.md entry 3.
//
// bobbin filed that entry against a language it described as one that "removes
// nothing", and asked for the barrier anyway on the grounds that the day it
// stops being true is the day every published number is wrong. The entry was
// half out of date when it was written. This interpreter already removes work:
// under TWILL_TRACE=1 a statement's tensor operations are recorded rather than
// run, and when the scope closes with nothing live, trace.compileAndRun finds
// no outputs and computes none of them. That is not a hypothetical optimiser
// and it is not a corner: it is the path docs/CODEGEN.md says goes on by
// default once section 11.2.3 lands.
//
// So these tests come in two halves, and the first half is the one that gives
// the second its meaning. TestDiscardedWorkIsDeletedWithoutTheBarrier shows the
// deletion happening, including through the workaround bobbin is using today.
// TestTheBarrierKeepsDiscardedWorkAlive shows black_box stopping it.

// tracedWork runs a program with the tracer on and reports how many operations
// it recorded and how many of those were ever actually computed. Compiled and
// Replayed are the only two ways a traced node turns into arithmetic; a node
// that is in neither was written down and thrown away.
func tracedWork(t *testing.T, src string) (nodes, computed int) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "bench.tw")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ip := interp.New(func(string) {})
	ip.SetTracing(true)
	if _, _, err := ip.RunFileMain(file, nil); err != nil {
		t.Fatalf("run: %v\nsource:\n%s", err, src)
	}
	s := ip.TraceStats()
	return s.Nodes, s.Compiled + s.Replayed
}

// A benchmark harness in miniature: a body whose result the harness has no use
// for, called from a function that returns nothing. That is the shape, not a
// contrivance to provoke the tracer. bobbin src/harness.tw calls `batch` as a
// statement, `batch` returns unit, and every result the benchmark body produces
// dies inside it.
const benchShape = `%s
fn discard(x) { let y = %s }
let big = ones(64, 64)
discard(big)
`

// The premise of entry 30, checked rather than assumed. If this test ever
// fails, the tracer stopped deleting discarded work and the argument written in
// docs/CODEGEN.md needs rewriting; it is not a reason to delete the barrier,
// which is a promise about every future optimiser and not only this one.
func TestDiscardedWorkIsDeletedWithoutTheBarrier(t *testing.T) {
	for _, c := range []struct{ name, prelude, body string }{
		{"nothing at all", "", "sum(exp(x))"},
		// bobbin's `fn keep(x: F64) -> F64 = x`, which is what entry 30 was
		// filed to replace. A call to a twill function is not a barrier: the
		// tracer follows straight through it, so the work dies exactly as it
		// does with no protection at all.
		{"a named identity function", "fn keep(v) = v", "keep(sum(exp(x)))"},
	} {
		t.Run(c.name, func(t *testing.T) {
			nodes, computed := tracedWork(t, fmt.Sprintf(benchShape, c.prelude, c.body))
			if nodes == 0 {
				t.Fatalf("nothing was traced, so this program no longer measures anything")
			}
			if computed != 0 {
				t.Errorf("%d of %d traced operations were computed; this test's premise "+
					"is that discarded work is deleted, and it no longer is", computed, nodes)
			}
		})
	}
}

// The barrier itself. black_box has no opcode, so Apply forces the open trace
// before it runs, and the work that produced its argument is computed rather
// than dropped. Giving black_box an opcode, or teaching a later pass to fold it
// away, fails here.
func TestTheBarrierKeepsDiscardedWorkAlive(t *testing.T) {
	nodes, computed := tracedWork(t, fmt.Sprintf(benchShape, "", "black_box(sum(exp(x)))"))
	if nodes == 0 {
		t.Fatalf("nothing was traced, so this program no longer measures anything")
	}
	if computed == 0 {
		t.Errorf("%d traced operations, none computed: black_box did not keep the value alive", nodes)
	}
}

// The other half of the contract, and the half a benchmark depends on every
// time it runs rather than only when an optimiser exists: the value comes back
// unchanged. A barrier that perturbed the number would corrupt every result it
// was meant to protect.
func TestTheBarrierReturnsItsArgumentUnchanged(t *testing.T) {
	dir := t.TempDir()
	out := runFile(t, dir, `print(black_box(3.5))
print(black_box("abc"))
print(black_box([1.0, 2.0, 3.0]))
print(shape(black_box(zeros(2, 3))), dtype(black_box(zeros(2, 2, bf16))))
print(sum(black_box(ones(2, 2))))
`)
	expectLines(t, out,
		"3.5",
		"abc",
		"tensor([1, 2, 3], shape=[3])",
		"[2, 3] bf16",
		"4",
	)
}

// Unlike stop_grad, which is the other builtin in this language that looks like
// an identity and is not one, black_box is transparent to autodiff. It stops an
// optimiser, not a gradient: d/dx sum(black_box(x) * x) is 2x, the same answer
// as without it, where stop_grad in that position would give x.
func TestTheBarrierIsNotAGradientBarrier(t *testing.T) {
	dir := t.TempDir()
	out := runFile(t, dir, `fn plain(x) = sum(x * x)
fn boxed(x) = sum(black_box(x) * x)
let x = [1.0, 2.0, 3.0]
print(plain(x), boxed(x))
print(grad(plain)(x))
print(grad(boxed)(x))
`)
	expectLines(t, out,
		"14 14",
		"tensor([2, 4, 6], shape=[3])",
		"tensor([2, 4, 6], shape=[3])",
	)
}

// Systems mode, where the values are not tensors. Nothing in this interpreter
// can delete an I64 addition today, so here the barrier is a promise rather
// than a mechanism, and what has to hold is that it is the identity on every
// kind of value a benchmark body can return.
func TestTheBarrierIsTheIdentityInSystemsMode(t *testing.T) {
	dir := t.TempDir()
	out := runFile(t, dir, `mode systems
fn main() {
  let n: I64 = black_box(7)
  let s: Str = black_box("abc")
  let xs: Arr[I64] = black_box([1, 2, 3])
  print(str(n))
  print(s)
  print(str(len(xs)))
  print(str(black_box(n) + 1))
}
`)
	expectLines(t, out, "7", "abc", "3", "8")
}
