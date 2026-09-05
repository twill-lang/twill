package tensor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
)

// A gradient check over the whole differentiable operator surface.
//
// The existing gradcheck_test.go checks a hand-picked set of ops one at a time.
// This file is the exhaustive version: every operator in the package that
// carries a gradient gets a case here, and the ones that cannot carry a
// gradient are named in nonDifferentiable below so the list is closed rather
// than merely long.
//
// Method. For a case that produces out = op(x), the harness differentiates the
// scalar L(x) = sum(w * out) for a fixed deterministic cotangent w. Seeding w
// with something other than all-ones matters: an all-ones cotangent makes a
// whole class of index-shuffling bugs invisible, because a permutation of the
// output has the same sum as the output. The reverse-mode gradient dL/dx is
// then compared, element by element, against a central difference of the same
// L, Richardson-extrapolated:
//
//	D(h)  = (L(x+h) - L(x-h)) / 2h                truncation O(h^2)
//	D*    = (4*D(h/2) - D(h)) / 3                 truncation O(h^4)
//
// The extrapolation is what makes a tight tolerance honest. A plain central
// difference at the best available step still carries roughly 1e-10 relative
// error in f64, which is close enough to a real small-magnitude gradient bug
// that a failure would be ambiguous. D* lands near 1e-12, so a disagreement
// above 1e-7 relative is a defect and not the difference method breathing.
//
// The step is scaled to the point, h = hRel * max(1, |x_i|), so a coordinate of
// size 1000 is not probed with an absolute step that its own rounding swallows.
//
// Kinks. relu, abs, clip, maximum, minimum, max, min, median, sort, topk,
// cummax, cummin and maxpool2d are piecewise. At a kink the derivative does not
// exist and a central difference straddles two branches, so it reports the
// average of the one-sided slopes, which is not what any autodiff system
// returns. That is a property of finite differencing, not a bug in the
// operator, so every case for a piecewise op is sited away from its kinks: no
// input near zero for relu and abs, no input near a clip bound, no ties in a
// comparison or an ordering. Where the kink itself is the interesting part, the
// convention twill picks is asserted directly instead, in kinkConventions below.
type gradCase struct {
	name  string
	data  []float64
	shape []int
	build func(*Tensor) *Tensor
	// tol overrides the default relative tolerance for cases whose forward pass
	// is itself ill-conditioned (a long cumprod, say, where the value spans
	// orders of magnitude and the difference quotient loses digits to
	// cancellation before the derivative is ever formed).
	tol float64
}

const (
	// hRel is the relative step for the coarse difference; h/2 is the fine one.
	// 1e-4 is deliberately large for a lone central difference and is the right
	// choice here: Richardson kills the O(h^2) and O(h^4) truncation terms, so
	// the remaining error is dominated by cancellation, which a larger step
	// makes smaller.
	hRel = 1e-4
	// defaultTol is the relative disagreement above which a case fails. Measured
	// error across the cases below sits between 1e-13 and 1e-11, so this leaves
	// four orders of headroom over the observed noise floor.
	defaultTol = 1e-7
	// absFloor keeps a near-zero gradient from failing on relative error alone:
	// a true derivative of 0 against a numeric 1e-13 is a ratio of infinity and
	// an absolute difference of nothing.
	absFloor = 1e-9
)

// cotangent builds the fixed w used to reduce a case's output to a scalar. It
// is deterministic (no RNG, no seed to get out of step) and deliberately
// irregular in sign and magnitude, so that summing a permuted output does not
// give the same answer as summing the output.
func cotangent(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = math.Sin(float64(i)*1.7+0.3) * (1 + 0.25*float64(i%5))
	}
	return w
}

// runCase differentiates one case both ways and returns the worst relative
// disagreement over the coordinates of x.
func runCase(t *testing.T, c gradCase) float64 {
	t.Helper()

	// Forward once on a plain (non-grad) leaf to learn the output size, so the
	// cotangent is fixed before any differentiation happens and is the same w
	// for the analytic and the numeric pass.
	probe := c.build(New(append([]float64(nil), c.data...), c.shape))
	w := cotangent(len(probe.Data))

	loss := func(data []float64) float64 {
		out := c.build(New(append([]float64(nil), data...), c.shape))
		s := 0.0
		for i := range out.Data {
			s += w[i] * out.Data[i]
		}
		return s
	}

	leaf := Leaf(c.data, c.shape)
	out := c.build(leaf)
	wt := New(w, out.Shape)
	prod, err := Mul(out, wt)
	if err != nil {
		t.Fatalf("%s: reducing output: %v", c.name, err)
	}
	if err := Sum(prod).Backward(); err != nil {
		t.Fatalf("%s: backward: %v", c.name, err)
	}
	if leaf.Grad == nil {
		t.Fatalf("%s: no gradient reached the leaf", c.name)
	}

	tol := c.tol
	if tol == 0 {
		tol = defaultTol
	}

	worst := 0.0
	for i := range c.data {
		h := hRel * math.Max(1, math.Abs(c.data[i]))
		diff := func(step float64) float64 {
			plus := append([]float64(nil), c.data...)
			minus := append([]float64(nil), c.data...)
			plus[i] += step
			minus[i] -= step
			return (loss(plus) - loss(minus)) / (2 * step)
		}
		coarse := diff(h)
		fine := diff(h / 2)
		num := (4*fine - coarse) / 3
		ana := leaf.Grad[i]

		scale := math.Max(math.Max(math.Abs(ana), math.Abs(num)), absFloor)
		rel := math.Abs(ana-num) / scale
		if rel > worst {
			worst = rel
		}
		if rel > tol {
			t.Errorf("%s: grad[%d] analytic=%.17g numeric=%.17g relative error %.3g exceeds %g",
				c.name, i, ana, num, rel, tol)
		}
	}
	return worst
}

// mustT unwraps the (tensor, error) ops so a case body stays one expression.
func mustT(t *Tensor, err error) *Tensor {
	if err != nil {
		panic(err)
	}
	return t
}

// konst builds a non-differentiated operand for a binary case.
func konst(data []float64, shape []int) *Tensor { return New(data, shape) }

// gradCases is the operator surface. Every entry names the operator it covers,
// and the coverage assertion at the bottom of the file checks the list against
// the package's exported functions so a new operator cannot be added without
// either a case here or an explicit entry in nonDifferentiable.
func gradCases() []gradCase {
	v4 := []float64{0.7, -1.3, 2.1, -0.4}
	m23 := []float64{0.5, -1.2, 0.9, 2.3, -0.7, 1.6}
	pos4 := []float64{0.4, 1.7, 2.9, 0.9} // strictly positive, for log and sqrt

	other4 := konst([]float64{1.1, -0.6, 0.3, 2.7}, []int{4})
	row3 := konst([]float64{2.0, -1.0, 0.5}, []int{3})
	col2 := konst([]float64{1.5, -2.5}, []int{2, 1})

	return []gradCase{
		// ---- elementwise binary, every broadcasting regime ----
		{name: "add/same-shape", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Add(x, other4))
		}},
		{name: "add/scalar-rhs", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Add(x, Scalar(2.5)))
		}},
		{name: "add/scalar-lhs", data: []float64{1.4}, shape: []int{}, build: func(x *Tensor) *Tensor {
			return mustT(Add(x, other4))
		}},
		{name: "add/row-broadcast", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(Add(x, row3))
		}},
		{name: "add/col-broadcast", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(Add(x, col2))
		}},
		{name: "add/general-broadcast", data: []float64{1.2, -0.8, 0.6}, shape: []int{1, 3},
			build: func(x *Tensor) *Tensor {
				return mustT(Add(x, konst([]float64{0.4, -1.1}, []int{2, 1})))
			}},
		{name: "sub/lhs", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Sub(x, other4))
		}},
		{name: "sub/rhs", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Sub(other4, x))
		}},
		{name: "mul/same-shape", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Mul(x, other4))
		}},
		{name: "mul/col-broadcast", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(Mul(x, col2))
		}},
		{name: "div/numerator", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Div(x, other4))
		}},
		{name: "div/denominator", data: pos4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Div(other4, x))
		}},
		// mod is differentiable away from the wrap points; the inputs sit
		// mid-interval so no probe crosses one.
		{name: "mod/lhs", data: []float64{1.35, 4.62, 7.18, 2.41}, shape: []int{4},
			build: func(x *Tensor) *Tensor {
				return mustT(Mod(x, Scalar(3.0)))
			}},
		{name: "mod/rhs", data: []float64{3.0}, shape: []int{},
			build: func(x *Tensor) *Tensor {
				return mustT(Mod(konst([]float64{1.35, 4.62, 7.18, 2.41}, []int{4}), x))
			}},

		// ---- elementwise unary ----
		{name: "neg", data: v4, shape: []int{4}, build: Neg},
		{name: "square", data: v4, shape: []int{4}, build: Square},
		{name: "exp", data: v4, shape: []int{4}, build: Exp},
		{name: "log", data: pos4, shape: []int{4}, build: Log},
		// log1p is defined for x > -1, so its sample sits inside that and still
		// crosses zero; expm1 takes the ordinary spread.
		{name: "log1p", data: []float64{0.7, -0.3, 2.1, -0.4}, shape: []int{4}, build: Log1p},
		{name: "expm1", data: v4, shape: []int{4}, build: Expm1},
		{name: "sqrt", data: pos4, shape: []int{4}, build: Sqrt},
		{name: "sin", data: v4, shape: []int{4}, build: Sin},
		{name: "cos", data: v4, shape: []int{4}, build: Cos},
		{name: "tanh", data: v4, shape: []int{4}, build: Tanh},
		{name: "sigmoid", data: v4, shape: []int{4}, build: Sigmoid},
		// relu away from 0: the kink is asserted separately.
		{name: "relu", data: []float64{0.7, -1.3, 2.1, -0.4}, shape: []int{4}, build: Relu},
		{name: "pow/scalar-exponent", data: pos4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return PowScalar(x, 2.5)
		}},
		{name: "pow/negative-exponent", data: pos4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return PowScalar(x, -1.5)
		}},
		// clip with every input strictly inside or strictly outside the bounds.
		{name: "clip", data: []float64{-2.4, -0.5, 0.3, 1.9}, shape: []int{4},
			build: func(x *Tensor) *Tensor { return Clip(x, -1, 1) }},

		// ---- selection ----
		{name: "maximum", data: []float64{0.9, -1.4, 2.6, 0.1}, shape: []int{4},
			build: func(x *Tensor) *Tensor { return mustT(Maximum(x, other4)) }},
		{name: "minimum", data: []float64{0.9, -1.4, 2.6, 0.1}, shape: []int{4},
			build: func(x *Tensor) *Tensor { return mustT(Minimum(x, other4)) }},
		{name: "where/true-branch", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Where(konst([]float64{1, 0, 1, 0}, []int{4}), x, other4))
		}},
		{name: "where/false-branch", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Where(konst([]float64{1, 0, 1, 0}, []int{4}), other4, x))
		}},

		// ---- whole-tensor reductions ----
		{name: "sum", data: m23, shape: []int{2, 3}, build: Sum},
		{name: "mean", data: m23, shape: []int{2, 3}, build: Mean},
		{name: "max-all", data: m23, shape: []int{2, 3}, build: MaxAll},
		{name: "min-all", data: m23, shape: []int{2, 3}, build: MinAll},
		{name: "prod", data: []float64{0.8, -1.4, 2.2, 1.7}, shape: []int{4}, build: Prod},
		{name: "median/odd", data: []float64{0.5, -1.2, 0.9, 2.3, -0.7}, shape: []int{5}, build: Median},
		{name: "median/even", data: m23, shape: []int{2, 3}, build: Median},

		// ---- axis reductions, on both axes of a non-square matrix ----
		{name: "sum-axis/0", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(SumAxis(x, 0))
		}},
		{name: "sum-axis/1", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(SumAxis(x, 1))
		}},
		{name: "mean-axis/0", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MeanAxis(x, 0))
		}},
		{name: "mean-axis/1", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MeanAxis(x, 1))
		}},
		{name: "max-axis/0", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MaxAxis(x, 0))
		}},
		{name: "max-axis/1", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MaxAxis(x, 1))
		}},
		{name: "min-axis/0", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MinAxis(x, 0))
		}},
		{name: "min-axis/1", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MinAxis(x, 1))
		}},
		{name: "prod-axis/0", data: []float64{0.8, -1.4, 2.2, 1.7, -0.9, 1.3}, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(ProdAxis(x, 0)) }},
		{name: "prod-axis/1", data: []float64{0.8, -1.4, 2.2, 1.7, -0.9, 1.3}, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(ProdAxis(x, 1)) }},
		{name: "median-axis/0", data: []float64{0.5, -1.2, 0.9, 2.3, -0.7, 1.6, 0.2, -2.1, 1.1},
			shape: []int{3, 3}, build: func(x *Tensor) *Tensor { return mustT(MedianAxis(x, 0)) }},
		{name: "median-axis/1", data: []float64{0.5, -1.2, 0.9, 2.3, -0.7, 1.6, 0.2, -2.1, 1.1},
			shape: []int{3, 3}, build: func(x *Tensor) *Tensor { return mustT(MedianAxis(x, 1)) }},

		// ---- normalising ----
		{name: "softmax/axis-0", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(Softmax(x, 0))
		}},
		{name: "softmax/axis-1", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(Softmax(x, 1))
		}},
		{name: "logsumexp/axis-0", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(LogSumExp(x, 0))
		}},
		{name: "logsumexp/axis-1", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(LogSumExp(x, 1))
		}},

		// ---- scans ----
		{name: "cumsum", data: v4, shape: []int{4}, build: CumSum},
		{name: "cumprod", data: []float64{0.8, -1.4, 1.2, 1.7}, shape: []int{4}, build: CumProd},
		{name: "cummax", data: []float64{0.4, 1.9, 0.7, 3.2}, shape: []int{4}, build: CumMax},
		{name: "cummin", data: []float64{3.2, 0.7, 1.9, 0.4}, shape: []int{4}, build: CumMin},
		{name: "cumsum-axis/0", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(CumsumAxis(x, 0))
		}},
		{name: "cumsum-axis/1", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(CumsumAxis(x, 1))
		}},
		{name: "cumprod-axis/0", data: []float64{0.8, -1.4, 1.2, 1.7, -0.9, 1.3}, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(CumprodAxis(x, 0)) }},
		{name: "cumprod-axis/1", data: []float64{0.8, -1.4, 1.2, 1.7, -0.9, 1.3}, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(CumprodAxis(x, 1)) }},
		{name: "cummax-axis/0", data: []float64{0.4, 1.9, 0.7, 3.2, 2.6, 1.1}, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(CumMaxAxis(x, 0)) }},
		{name: "cummin-axis/1", data: []float64{3.2, 0.7, 1.9, 0.4, 2.6, 1.1}, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(CumMinAxis(x, 1)) }},

		// ---- rearranging ----
		{name: "reshape", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(Reshape(x, []int{3, 2}))
		}},
		{name: "transpose/2d", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(TransposePerm(x, []int{1, 0}))
		}},
		{name: "transpose/3d-rotate", data: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
			shape: []int{2, 3, 2}, build: func(x *Tensor) *Tensor {
				return mustT(TransposePerm(x, []int{2, 0, 1}))
			}},
		{name: "broadcast-to", data: []float64{1.4, -0.6, 2.2}, shape: []int{1, 3},
			build: func(x *Tensor) *Tensor { return mustT(BroadcastTo(x, []int{4, 3})) }},
		// sum_to is the inverse of a broadcast and the VJP every broadcasting
		// elementwise op performs inline. Both regimes are checked: a leading
		// axis that disappears, and an interior axis that collapses to 1.
		{name: "sum-to/drop-axis", data: []float64{1.4, -0.6, 2.2, 0.3, -1.1, 0.8},
			shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(SumTo(x, []int{3})) }},
		{name: "sum-to/keep-axis", data: []float64{1.4, -0.6, 2.2, 0.3, -1.1, 0.8},
			shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(SumTo(x, []int{2, 1})) }},
		{name: "flip", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(FlipAxis(x, 1))
		}},
		{name: "roll", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(RollAxis(x, 2, 1))
		}},
		{name: "diff", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(DiffAxis(x, 1))
		}},
		{name: "concat/self-and-const", data: v4, shape: []int{4}, build: func(x *Tensor) *Tensor {
			return mustT(Concat([]*Tensor{x, other4}, 0))
		}},
		{name: "concat/three-way-axis-1", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			c := konst([]float64{9, 8, 7, 6}, []int{2, 2})
			return mustT(Concat([]*Tensor{c, x, c}, 1))
		}},
		{name: "split", data: []float64{1.2, -0.5, 2.4, 0.8, -1.9, 1.1}, shape: []int{6},
			build: func(x *Tensor) *Tensor {
				parts, err := Split(x, []int{2, 4}, 0)
				if err != nil {
					panic(err)
				}
				// Recombine unevenly so the case depends on both pieces and on
				// which piece each element landed in.
				return mustT(Concat([]*Tensor{Square(parts[0]), Exp(parts[1])}, 0))
			}},
		{name: "split-equal", data: []float64{1.2, -0.5, 2.4, 0.8, -1.9, 1.1}, shape: []int{6},
			build: func(x *Tensor) *Tensor {
				parts, err := SplitEqual(x, 3, 0)
				if err != nil {
					panic(err)
				}
				return mustT(Concat([]*Tensor{Tanh(parts[0]), Square(parts[1]), Neg(parts[2])}, 0))
			}},
		{name: "slice-axis0", data: []float64{1.2, -0.5, 2.4, 0.8, -1.9}, shape: []int{5},
			build: func(x *Tensor) *Tensor { return mustT(SliceAxis0(x, 1, 4)) }},
		{name: "index-axis0", data: m23, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor { return mustT(IndexAxis0(x, 1)) }},

		// ---- ordering. No ties: a tie is a kink, and sort's gradient at a tie
		// depends on the tie-break, which is a convention rather than a slope.
		{name: "sort/ascending", data: []float64{2.4, -1.1, 0.6, 3.9}, shape: []int{4},
			build: func(x *Tensor) *Tensor { return mustT(SortAxis(x, 0, false)) }},
		{name: "sort/descending-axis-1", data: []float64{2.4, -1.1, 0.6, 3.9, 1.5, -2.8},
			shape: []int{2, 3}, build: func(x *Tensor) *Tensor { return mustT(SortAxis(x, 1, true)) }},
		{name: "topk/largest", data: []float64{2.4, -1.1, 0.6, 3.9, 1.5}, shape: []int{5},
			build: func(x *Tensor) *Tensor { return mustT(TopKAxis(x, 3, 0, true)) }},
		{name: "topk/smallest", data: []float64{2.4, -1.1, 0.6, 3.9, 1.5}, shape: []int{5},
			build: func(x *Tensor) *Tensor { return mustT(TopKAxis(x, 2, 0, false)) }},

		// ---- contraction ----
		{name: "matmul/lhs-2d", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MatMul(x, konst([]float64{1.1, -0.4, 0.7, 2.2, -1.5, 0.9}, []int{3, 2})))
		}},
		{name: "matmul/rhs-2d", data: []float64{1.1, -0.4, 0.7, 2.2, -1.5, 0.9}, shape: []int{3, 2},
			build: func(x *Tensor) *Tensor {
				return mustT(MatMul(konst(m23, []int{2, 3}), x))
			}},
		{name: "matmul/vec-dot", data: []float64{1.2, -0.5, 2.4}, shape: []int{3},
			build: func(x *Tensor) *Tensor { return mustT(MatMul(x, row3)) }},
		{name: "matmul/matvec", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MatMul(x, row3))
		}},
		{name: "matmul-nt/input", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(MatMulNT(x, konst([]float64{1.1, -0.4, 0.7, 2.2, -1.5, 0.9}, []int{2, 3})))
		}},
		{name: "matmul-nt/weight", data: []float64{1.1, -0.4, 0.7, 2.2, -1.5, 0.9}, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor {
				return mustT(MatMulNT(konst(m23, []int{2, 3}), x))
			}},
		{name: "einsum/matmul", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(Einsum("ij,jk->ik", []*Tensor{x,
				konst([]float64{1.1, -0.4, 0.7, 2.2, -1.5, 0.9}, []int{3, 2})}))
		}},
		{name: "einsum/transpose-sum", data: m23, shape: []int{2, 3}, build: func(x *Tensor) *Tensor {
			return mustT(Einsum("ij->ji", []*Tensor{x}))
		}},
		// A trace, "ii->", would be the natural case here. twill's einsum
		// refuses a label repeated within one operand, so a diagonal cannot be
		// taken this way; the refusal is explicit rather than silently wrong,
		// and is recorded as a limitation in docs/CORRECTNESS.md. The
		// partial-reduction form below covers the same backward machinery.
		{name: "einsum/contract-and-drop-an-axis", data: m23, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor {
				return mustT(Einsum("ij,j->i", []*Tensor{x, row3}))
			}},
		{name: "einsum/bilinear-three-operand", data: []float64{1.2, -0.5, 2.4, 0.8},
			shape: []int{2, 2}, build: func(x *Tensor) *Tensor {
				u := konst([]float64{1.5, -0.7}, []int{2})
				v := konst([]float64{0.4, 2.1}, []int{2})
				return mustT(Einsum("i,ij,j->", []*Tensor{u, x, v}))
			}},
		{name: "einsum/batched-contraction", data: []float64{1, 2, 3, 4, 5, 6, 7, 8},
			shape: []int{2, 2, 2}, build: func(x *Tensor) *Tensor {
				b := konst([]float64{0.3, -1.2, 2.4, 0.6, -0.8, 1.9, 0.5, -2.2}, []int{2, 2, 2})
				return mustT(Einsum("bij,bjk->bik", []*Tensor{x, b}))
			}},

		// ---- deep learning ----
		// conv2d takes [Cin, H, W] against a [Cout, Cin, KH, KW] weight: there
		// is no batch axis, so a batch is a loop in the caller.
		{name: "conv2d/input", data: seq(1*4*4, 0.37), shape: []int{1, 4, 4},
			build: func(x *Tensor) *Tensor {
				return mustT(Conv2D(x, konst([]float64{0.5, -1.1, 0.8, 0.3}, []int{1, 1, 2, 2})))
			}},
		{name: "conv2d/weight", data: []float64{0.5, -1.1, 0.8, 0.3}, shape: []int{1, 1, 2, 2},
			build: func(x *Tensor) *Tensor {
				return mustT(Conv2D(konst(seq(16, 0.37), []int{1, 4, 4}), x))
			}},
		{name: "conv2d/multi-channel", data: seq(2*4*4, 0.29), shape: []int{2, 4, 4},
			build: func(x *Tensor) *Tensor {
				w := konst(seq(3*2*2*2, 0.13), []int{3, 2, 2, 2})
				return mustT(Conv2D(x, w))
			}},
		// maxpool over distinct values so no window has a tie at its maximum.
		{name: "maxpool2d", data: seq(1*4*4, 0.41), shape: []int{1, 4, 4},
			build: func(x *Tensor) *Tensor { return mustT(MaxPool2D(x, 2)) }},
		{name: "gather/repeated-index", data: m23, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor {
				// Index 0 twice: the backward pass must scatter-add, not overwrite.
				return mustT(Gather(x, []int{0, 1, 0}))
			}},

		// ---- quantised linear. The weight is frozen by quantisation, so only
		// the activation carries a gradient; that is the path checked here.
		{name: "qlinear-i8/activation", data: m23, shape: []int{2, 3},
			build: func(x *Tensor) *Tensor {
				q, err := QuantizeI8(konst([]float64{1.1, -0.4, 0.7, 2.2, -1.5, 0.9}, []int{2, 3}))
				if err != nil {
					panic(err)
				}
				return mustT(QLinear(x, q))
			}},
		{name: "qlinear-i4/activation", data: seq(2*8, 0.19), shape: []int{2, 8},
			build: func(x *Tensor) *Tensor {
				q, err := QuantizeI4(konst(seq(3*8, 0.23), []int{3, 8}), 8)
				if err != nil {
					panic(err)
				}
				return mustT(QLinear4(x, q))
			}},

		// ---- composites, where a bug shows up only in combination ----
		{name: "composite/mlp-layer", data: seq(4*3, 0.31), shape: []int{4, 3},
			build: func(x *Tensor) *Tensor {
				w := konst(seq(2*3, 0.17), []int{2, 3})
				b := konst([]float64{0.4, -0.9}, []int{2})
				h := mustT(Add(mustT(MatMulNT(x, w)), b))
				return mustT(Softmax(Tanh(h), 1))
			}},
		{name: "composite/reused-leaf", data: v4, shape: []int{4},
			build: func(x *Tensor) *Tensor {
				// x appears three times: the gradient must accumulate, not the
				// last write win.
				return mustT(Mul(mustT(Add(x, x)), Exp(x)))
			}},
		{name: "composite/attention-scores", data: seq(3*4, 0.23), shape: []int{3, 4},
			build: func(x *Tensor) *Tensor {
				scores := mustT(MatMulNT(x, x))
				return mustT(MatMul(mustT(Softmax(scores, 1)), x))
			}},
		// A long cumprod is genuinely ill-conditioned: the value spans three
		// orders of magnitude, so the difference quotient loses digits to
		// cancellation before any derivative is formed. Loosened deliberately,
		// with the reason stated rather than the failure hidden.
		{name: "composite/long-cumprod", data: seq(12, 0.11), shape: []int{12}, tol: 1e-5,
			build: func(x *Tensor) *Tensor { return CumProd(mustT(Add(x, Scalar(1.0)))) }},
	}
}

// seq builds distinct, well-separated values: no ties for the ordering ops and
// nothing near zero for the piecewise ones.
func seq(n int, step float64) []float64 {
	d := make([]float64, n)
	for i := range d {
		d[i] = 0.3 + float64(i)*step
	}
	return d
}

func TestGradientCheckFullOperatorSet(t *testing.T) {
	cases := gradCases()
	type result struct {
		name string
		err  float64
	}
	results := make([]result, 0, len(cases))
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			worst := runCase(t, c)
			results = append(results, result{c.name, worst})
		})
	}
	// The report is the artefact: docs/BENCHMARKS.md and docs/CORRECTNESS.md
	// quote it, and `go test -run TestGradientCheckFullOperatorSet -v` regenerates it.
	sort.Slice(results, func(i, j int) bool { return results[i].err > results[j].err })
	t.Logf("gradient check: %d cases, worst relative error first", len(results))
	for _, r := range results {
		t.Logf("  %-36s %.3e", r.name, r.err)
	}
}

// nonDifferentiable names every exported operator that deliberately carries no
// gradient, with the reason. The coverage test below uses this list, so an
// operator can only be absent from the gradient check by being named here.
var nonDifferentiable = map[string]string{
	// Not an operator: it moves a value and its graph node into another Tensor
	// object, for the tracer. It computes nothing, so there is nothing to check.
	"Adopt": "moves a value between Tensor objects",
	// Deliberately has no gradient, and that is the whole operation: it copies
	// the values out of the graph so a backward pass reaching them stops. A
	// gradient check would assert the derivative it exists to remove.
	"Detach": "removes a value from the graph, which is the point",
	// Index-valued: the output is a position, which is integer and locally
	// constant, so its derivative is zero almost everywhere and undefined at the
	// ties. twill returns no gradient rather than a meaningless zero.
	"ArgmaxAxis":  "returns indices",
	"ArgminAxis":  "returns indices",
	"ArgsortAxis": "returns indices",
	"ArgTopKAxis": "returns indices",
	// Boolean-valued.
	"Greater": "returns a 0/1 mask", "Less": "returns a 0/1 mask",
	"GreaterEqual": "returns a 0/1 mask", "LessEqual": "returns a 0/1 mask",
	"EqualOp": "returns a 0/1 mask", "NotEqual": "returns a 0/1 mask",
	// Quantisation itself: it maps a float weight to a fixed-point code, a step
	// function whose derivative is zero between steps. The gradient through the
	// quantised product is checked on the activation, in qlinear-*/activation.
	"QuantizeI8": "step function; the activation path is checked via QLinear",
	"QuantizeI4": "step function; the activation path is checked via QLinear4",
	// Cast rounds to a narrower dtype: also a step function. Its numerics are
	// covered by dtype_test.go.
	"Cast": "rounding step function; covered by dtype_test.go",
	// Structural, dtype and construction helpers, not operators.
	"New": "constructor", "Scalar": "constructor", "Leaf": "constructor",
	"Filled": "constructor", "FromNested": "constructor",
	"Promote":   "dtype arithmetic",
	"AccDType":  "dtype arithmetic",
	"DTypeName": "dtype arithmetic", "DTypeOfName": "dtype arithmetic",
	"RoundToDType": "dtype arithmetic", "ShortestForDType": "formatting",
	"IsFloatDType": "dtype arithmetic", "IsIntDType": "dtype arithmetic",
	// Second-order machinery, checked against finite differences in jet_test.go.
	"Hessian": "second-order; checked in jet_test.go",
	// Forward-mode machinery rather than operators: Directional runs one jet
	// sweep over a graph the operators built, and HessianVector polarizes three
	// of those sweeps. Both are checked against finite differences and against
	// Hessian in jet_test.go, and neither has a gradient of its own to check.
	"Directional":   "forward-mode sweep; checked in jet_test.go",
	"HessianVector": "second-order; checked in jet_test.go",
}

// TestGradientCheckCoversEveryOperator closes the list. It walks the operators
// the gradient check exercises and the ones declared non-differentiable, and
// fails if the package exports a differentiable operator that appears in
// neither. Without this, "the full operator set" is a claim; with it, it is a
// property the suite enforces.
func TestGradientCheckCoversEveryOperator(t *testing.T) {
	// Operators the cases above exercise, named as the Go functions they call.
	covered := map[string]bool{}
	for _, n := range []string{
		"Add", "Sub", "Mul", "Div", "Mod", "Neg", "Square", "Exp", "Log", "Log1p", "Expm1", "Sqrt",
		"Sin", "Cos", "Tanh", "Sigmoid", "Relu", "PowScalar", "Clip",
		"Maximum", "Minimum", "Where",
		"Sum", "Mean", "MaxAll", "MinAll", "Prod", "Median",
		"SumAxis", "MeanAxis", "MaxAxis", "MinAxis", "ProdAxis", "MedianAxis",
		"Softmax", "LogSumExp",
		"CumSum", "CumProd", "CumMax", "CumMin",
		"CumsumAxis", "CumprodAxis", "CumMaxAxis", "CumMinAxis",
		"Reshape", "TransposePerm", "BroadcastTo", "SumTo", "FlipAxis", "RollAxis",
		"DiffAxis", "Concat", "Split", "SplitEqual", "SliceAxis0", "IndexAxis0",
		"SortAxis", "TopKAxis",
		"MatMul", "MatMulNT", "Einsum",
		"Conv2D", "MaxPool2D", "Gather",
		"QLinear", "QLinear4",
	} {
		covered[n] = true
	}
	exported := exportedOperators(t)
	for _, n := range exported {
		if covered[n] {
			continue
		}
		if _, ok := nonDifferentiable[n]; ok {
			continue
		}
		t.Errorf("operator %s is exported but has no gradient-check case and is not "+
			"declared non-differentiable; add a case to gradCases or an entry to nonDifferentiable", n)
	}
	// The reverse direction: a name in the covered list that no longer exists is
	// a stale case, and so is a stale exemption in nonDifferentiable.
	all := map[string]bool{}
	for _, n := range exported {
		all[n] = true
	}
	for n := range covered {
		if !all[n] {
			t.Errorf("gradient check names %s, which the package no longer exports", n)
		}
	}
	for n := range nonDifferentiable {
		if !all[n] {
			t.Errorf("nonDifferentiable names %s, which the package no longer exports", n)
		}
	}
	t.Logf("operator coverage: %d differentiable operators checked, %d declared non-differentiable, %d exported total",
		len(covered), len(nonDifferentiable), len(all))
}

// exportedOperators reads the package's own source and returns every exported
// top-level name that takes or returns a *Tensor. Reading the source rather
// than keeping a hand-written list is the point: a new operator lands in this
// list the moment it is written, so the coverage test above fails until someone
// decides whether it is differentiable. A list maintained by hand would just
// quietly stop being the full operator set.
func exportedOperators(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the tensor package: %v", err)
	}
	pkg, ok := pkgs["tensor"]
	if !ok {
		t.Fatal("the tensor package did not parse")
	}
	mentionsTensor := func(fields *ast.FieldList) bool {
		if fields == nil {
			return false
		}
		found := false
		for _, f := range fields.List {
			ast.Inspect(f.Type, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && (id.Name == "Tensor" || id.Name == "QTensor" || id.Name == "QTensorI4" || id.Name == "DType") {
					found = true
				}
				return true
			})
		}
		return found
	}
	var names []string
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue // methods are accessors, not operators
			}
			if mentionsTensor(fn.Type.Params) || mentionsTensor(fn.Type.Results) {
				names = append(names, fn.Name.Name)
			}
		}
		// The comparison operators are package-level vars built by compareOp,
		// not func declarations, so they are collected separately.
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range vs.Names {
					if !id.IsExported() || i >= len(vs.Values) {
						continue
					}
					call, ok := vs.Values[i].(*ast.CallExpr)
					if !ok {
						continue
					}
					if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "compareOp" {
						names = append(names, id.Name)
					}
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

// kinkConventions pins the value twill returns at the points where the
// derivative does not exist. Finite differences cannot adjudicate these, so
// they are asserted directly: the point is that the choice is deliberate and
// stable, not that it is the unique right answer.
func TestGradientKinkConventions(t *testing.T) {
	grad1 := func(build func(*Tensor) *Tensor, data []float64) []float64 {
		leaf := Leaf(data, []int{len(data)})
		if err := Sum(build(leaf)).Backward(); err != nil {
			t.Fatal(err)
		}
		return leaf.Grad
	}

	// relu'(0) = 0. The subgradient at 0 is any value in [0,1]; twill takes the
	// left branch, matching PyTorch and JAX.
	if g := grad1(Relu, []float64{0}); g[0] != 0 {
		t.Errorf("relu'(0) = %v, want 0", g[0])
	}
	// clip at exactly a bound gets no gradient: the condition is a strict
	// interior test (x > lo && x < hi), so the boundary belongs to the clamped
	// side. PyTorch's clamp passes the gradient through at the boundary
	// instead. Both are defensible; twill's is recorded here so a change to it
	// is a deliberate one.
	if g := grad1(func(x *Tensor) *Tensor { return Clip(x, -1, 1) }, []float64{-1, 1}); g[0] != 0 || g[1] != 0 {
		t.Errorf("clip gradient at the bounds = %v, want [0 0]", g)
	}
	// A tie in maximum sends the whole gradient to the left operand rather than
	// splitting it. PyTorch's maximum splits a tie evenly (0.5 each).
	tied := New([]float64{2.0, 2.0}, []int{2})
	leaf := Leaf([]float64{2.0, 2.0}, []int{2})
	m, err := Maximum(leaf, tied)
	if err != nil {
		t.Fatal(err)
	}
	if err := Sum(m).Backward(); err != nil {
		t.Fatal(err)
	}
	if leaf.Grad[0] != 1 || leaf.Grad[1] != 1 {
		t.Errorf("maximum gradient at a tie = %v, want [1 1] (whole gradient to the left operand)", leaf.Grad)
	}
	// A tie in a whole-tensor max sends the gradient to the first occurrence
	// only, not split across the tied maxima. PyTorch's amax splits evenly.
	leaf2 := Leaf([]float64{1.0, 3.0, 3.0}, []int{3})
	if err := Sum(MaxAll(leaf2)).Backward(); err != nil {
		t.Fatal(err)
	}
	if leaf2.Grad[0] != 0 || leaf2.Grad[1] != 1 || leaf2.Grad[2] != 0 {
		t.Errorf("max gradient at a tie = %v, want [0 1 0] (first occurrence takes it)", leaf2.Grad)
	}
	// abs is |x| = sqrt(x^2)? No: twill's abs is a dedicated builtin. At 0 the
	// square path would give 0/0; the builtin is checked here through Relu-free
	// arithmetic in the interpreter tests. Nothing to assert at the tensor layer.
}
