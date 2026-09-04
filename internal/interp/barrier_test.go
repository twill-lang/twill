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
// run, and when the owning scope closes with nothing live, trace.compileAndRun
// finds no outputs and computes none of them. That is not a hypothetical
// optimiser and it is not a corner: it is the path docs/CODEGEN.md says goes on
// by default once section 11.2.3 lands.
//
// TestBarrierMeasurementTable is where that is measured. It is also the source
// of the table in docs/CODEGEN.md section 12.1: the test prints the table it
// asserts, so the published numbers cannot drift from the ones a run produces
// without this test going red first.

// tracedWork runs a program with the tracer on and reports two counts of the
// same population: tensor operations the tracer recorded, and how many of those
// operations were computed.
//
// Both come from internal/trace's counters and both count nodes. Stats.Nodes
// rises once per recorded operation, in Tracer.place. Stats.Computed rises by
// the number of operations open in the trace at the moment something evaluates
// them, and by nothing else.
//
// It is not Compiled+Replayed, which is what an earlier version of this helper
// returned. Those two count events rather than work: internal/trace/trace.go
// documents them as "scopes closed by running compiled code" and "forces that
// fell back to replay", and one of either can stand for two thousand
// multiplications or for none. A helper that returns them cannot answer "how
// much work was performed", which is the only question this file is asking.
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
	return s.Nodes, s.Computed
}

// The three program shapes, and the reason there are three rather than one.
//
// The deletion this file is about happens at the close of the scope that *owns*
// the trace, and internal/interp/tracing.go opens one per statement, so which
// statement owns it decides whether anything is live when it closes. That makes
// the shape of the benchmark, not the shape of its body, the thing that decides
// whether work survives. A single shape would have published a rule that holds
// in one of these three.
const (
	// stmtShape: a body whose result the caller has no use for, called as a
	// statement. bobbin src/harness.tw is this shape: it calls `batch` as a
	// statement, `batch` returns unit, and every result the benchmark body
	// produces dies inside it. The owning scope is `discard(big)`, its value is
	// unit, and nothing is live.
	stmtShape = `%s
fn discard(x) { let y = %s }
let big = ones(64, 64)
discard(big)
`
	// loopStmtShape: the same call, four times. This is the shape a benchmark
	// harness actually runs, and it is the one whose numbers matter.
	loopStmtShape = `%s
fn body(x) { let y = %s }
let big = ones(64, 64)
for i in range(4) { body(big) }
`
	// loopLetShape: four iterations that bind the result to a `let` in the loop
	// body. Here the `let` is the owning statement and its value is the body's
	// result, so the result is live at the close and nothing is deleted. It is
	// in this table because it is the shape where the barrier buys nothing, and
	// a table that showed only the two shapes where it helps would be a
	// advertisement rather than a measurement.
	loopLetShape = `%s
fn body(x) = %s
let big = ones(64, 64)
for i in range(4) { let y = body(big) }
`
)

// TestBarrierMeasurementTable measures every combination of the three shapes
// above with three protections, asserts all nine, and prints the result as the
// table docs/CODEGEN.md section 12.1 publishes.
//
// It carries both halves of the argument. The rows with `computed` at 0 are the
// premise of roadmap entry 30, checked rather than assumed: if one of them ever
// fails, the tracer stopped deleting discarded work and section 12.1 needs
// rewriting, which is not a reason to delete the barrier. The rows where
// black_box raises `computed` to `traced` are the barrier working; giving
// black_box an opcode, or teaching a later pass to fold it away, fails there.
// The three loopLetShape rows assert that the barrier changes nothing, which is
// the row that keeps the other six honest.
func TestBarrierMeasurementTable(t *testing.T) {
	protections := []struct{ name, prelude, body string }{
		{"unprotected", "", "sum(exp(x))"},
		// bobbin's `fn keep(x: F64) -> F64 = x`, which is what entry 30 was
		// filed to replace. A call to a twill function is not a barrier: the
		// tracer follows straight through it, so the work dies exactly as it
		// does with no protection at all.
		{"through a named identity function", "fn keep(v) = v", "keep(sum(exp(x)))"},
		{"through black_box", "", "black_box(sum(exp(x)))"},
	}
	shapes := []struct {
		name, tmpl string
		// want[i] is {traced, computed} for protections[i].
		want [3][2]int
	}{
		{"one discarded call", stmtShape, [3][2]int{{2, 0}, {2, 0}, {2, 2}}},
		{"four discarded calls in a loop", loopStmtShape, [3][2]int{{8, 0}, {8, 0}, {8, 8}}},
		{"four calls bound by a let in a loop", loopLetShape, [3][2]int{{8, 8}, {8, 8}, {8, 8}}},
	}

	t.Log("| program | protection | operations traced | operations computed |")
	t.Log("|---|---|---|---|")
	for _, sh := range shapes {
		for i, p := range protections {
			traced, computed := tracedWork(t, fmt.Sprintf(sh.tmpl, p.prelude, p.body))
			t.Logf("| %s | %s | %d | %d |", sh.name, p.name, traced, computed)
			want := sh.want[i]
			if traced != want[0] || computed != want[1] {
				t.Errorf("%s, %s: traced %d and computed %d, want %d and %d; "+
					"docs/CODEGEN.md section 12.1 publishes the wanted pair and is now wrong",
					sh.name, p.name, traced, computed, want[0], want[1])
			}
		}
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
