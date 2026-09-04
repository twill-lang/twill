package codegen_test

import (
	"github.com/twill-lang/twill/internal/codegen"
	"testing"

	"github.com/twill-lang/twill/internal/ir"
	"github.com/twill-lang/twill/internal/tensor"
)

// deadBranch builds a graph whose output reads one of two chains and ignores the
// other. Both chains are the same length, so the two evaluators can be asked the
// same question: does a node no output reads still get computed?
//
// The answer has to be yes for internal/trace's Computed counter to mean what
// docs/CODEGEN.md section 12.1 says it means. That counter rises by the number
// of nodes open in a trace whenever anything evaluates them, on the grounds that
// neither evaluator skips a node for being unread. If either ever starts
// skipping, section 12.1's table is measuring the wrong thing and these tests
// are where that is noticed.
func deadBranch(t *testing.T) *ir.Graph {
	t.Helper()
	b := ir.NewBuilder()
	x := b.Param("x", []int{4})
	live := b.Unary(ir.OpExp, b.Unary(ir.OpNeg, x))
	dead := b.Unary(ir.OpExp, b.Unary(ir.OpNeg, b.Unary(ir.OpLog, x)))
	_ = dead
	b.Output(live)
	g, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// The replay evaluator. This one is observed rather than inferred: EvalNodesWith
// calls bind once per node with the value it just computed, so an unread node
// that was skipped would simply not appear.
func TestEvalComputesEveryNodeIncludingUnreadOnes(t *testing.T) {
	g := deadBranch(t)
	seen := make([]bool, len(g.Nodes))
	arg := tensor.New([]float64{1, 2, 3, 4}, []int{4})
	_, err := ir.EvalNodesWith(g, []*tensor.Tensor{arg}, func(i int, v *tensor.Tensor) *tensor.Tensor {
		if v == nil || (g.Nodes[i].Op != ir.OpParam && v.Data == nil) {
			t.Errorf("node %%%d (%s) was visited with no value", i, g.Nodes[i].Op)
		}
		seen[i] = true
		return v
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range g.Nodes {
		if !seen[i] {
			t.Errorf("node %%%d (%s) was never evaluated; ir.Eval now skips nodes no "+
				"output reads, and internal/trace's Computed counter overcounts by "+
				"exactly those nodes", i, g.Nodes[i].Op)
		}
	}
}

// The compiled evaluator. What is observed here is the plan and the emitted C,
// because Program.Run hands back only the graph's outputs and a node no output
// reads has nowhere to show up in them. Neither needs a C compiler or the
// shared-library loader, which matters: internal/codegen/load_other.go supports
// loading on Windows only, so on every other machine the compiled path is
// unreachable and a test that compiled would skip rather than check.
//
// Owner is the right thing to look at even so. internal/codegen/emit.go emits
// one kernel per region and nothing else, so a node inside a region is a node
// the emitted C computes, and a node with no owner is a node no kernel touches.
// The arena is the second half of the same fact: a node the backend had decided
// not to compute would not be given room to compute into.
func TestTheBackendPlansEveryNodeIncludingUnreadOnes(t *testing.T) {
	g := deadBranch(t)
	for _, c := range []struct {
		name string
		plan *ir.Plan
		// fused: an absorbed node is computed inside its consumer's loop and is
		// deliberately given no buffer, so only the unfused plan can be asked
		// for a buffer per node. Being computed is the claim here, not being
		// materialised, and Owner is what says so in both.
		fused bool
	}{
		{"unfused", ir.FuseOff(g), false},
		{"fused", ir.Fuse(g), true},
	} {
		for i := range g.Nodes {
			if c.plan.Owner[i] < 0 {
				t.Errorf("%s: node %%%d (%s) is in no region, so no kernel computes it; "+
					"the backend has started eliminating unread nodes and "+
					"docs/CODEGEN.md section 12.1 needs remeasuring", c.name, i, g.Nodes[i].Op)
			}
		}
		_, lay, err := codegen.Emit(c.plan)
		if err != nil {
			t.Fatalf("%s: emit: %v", c.name, err)
		}
		if c.fused {
			continue
		}
		if got, want := lay.Total, 4*len(g.Nodes); got != want {
			t.Errorf("unfused: arena holds %d f64 for a %d-node graph of 4-element "+
				"values, want %d; a node the emitter skipped would show up here as "+
				"missing room", got, len(g.Nodes), want)
		}
	}
}
