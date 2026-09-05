package tensor

import (
	"fmt"
	"math"
	"sort"
)

// --- extra elementwise ops -------------------------------------------------

func Maximum(a, b *Tensor) (*Tensor, error) {
	return broadcastBinary(a, b,
		func(x, y float64) float64 { return math.Max(x, y) },
		func(x, y, o float64) float64 {
			if x >= y {
				return 1
			}
			return 0
		},
		func(x, y, o float64) float64 {
			if x >= y {
				return 0
			}
			return 1
		},
		zero2, zero2, zero2)
}

func Minimum(a, b *Tensor) (*Tensor, error) {
	return broadcastBinary(a, b,
		func(x, y float64) float64 { return math.Min(x, y) },
		func(x, y, o float64) float64 {
			if x <= y {
				return 1
			}
			return 0
		},
		func(x, y, o float64) float64 {
			if x <= y {
				return 0
			}
			return 1
		},
		zero2, zero2, zero2)
}

// compareOp builds a non-differentiable elementwise comparison returning 1/0.
func compareOp(name string, cmp func(x, y float64) bool) func(a, b *Tensor) (*Tensor, error) {
	return func(a, b *Tensor) (*Tensor, error) {
		return broadcastBinary(a, b,
			func(x, y float64) float64 {
				if cmp(x, y) {
					return 1
				}
				return 0
			},
			zero2, zero2, zero2, zero2, zero2)
	}
}

var (
	Greater      = compareOp("gt", func(x, y float64) bool { return x > y })
	Less         = compareOp("lt", func(x, y float64) bool { return x < y })
	GreaterEqual = compareOp("ge", func(x, y float64) bool { return x >= y })
	LessEqual    = compareOp("le", func(x, y float64) bool { return x <= y })
	EqualOp      = compareOp("eq", func(x, y float64) bool { return x == y })
	NotEqual     = compareOp("ne", func(x, y float64) bool { return x != y })
)

func Square(a *Tensor) *Tensor {
	return unary(a, true, func(x float64) float64 { return x * x }, func(x, o float64) float64 { return 2 * x },
		func(x, o float64) float64 { return 2 })
}

// Clip clamps values into [lo, hi]; the gradient passes through only the
// interior.
func Clip(a *Tensor, lo, hi float64) *Tensor {
	return unary(a, true,
		func(x float64) float64 { return math.Min(math.Max(x, lo), hi) },
		func(x, o float64) float64 {
			if x > lo && x < hi {
				return 1
			}
			return 0
		},
		zeroU)
}

// Where selects a where cond is non-zero, else b (with broadcasting). It is
// not differentiable through cond.
func Where(cond, a, b *Tensor) (*Tensor, error) {
	s1, ok := BroadcastShape(cond.Shape, a.Shape)
	if !ok {
		return nil, fmt.Errorf("where: cannot broadcast %v with %v", cond.Shape, a.Shape)
	}
	shape, ok := BroadcastShape(s1, b.Shape)
	if !ok {
		return nil, fmt.Errorf("where: cannot broadcast %v with %v", s1, b.Shape)
	}
	n := numel(shape)
	ostr := strides(shape)
	effC := effStrides(cond.Shape, shape)
	effA := effStrides(a.Shape, shape)
	effB := effStrides(b.Shape, shape)
	idx := func(o int, eff []int) int {
		ii, rem := 0, o
		for d := 0; d < len(shape); d++ {
			ii += (rem / ostr[d]) * eff[d]
			rem = rem % ostr[d]
		}
		return ii
	}
	out := make([]float64, n)
	for o := 0; o < n; o++ {
		if cond.Data[idx(o, effC)] != 0 {
			out[o] = a.Data[idx(o, effA)]
		} else {
			out[o] = b.Data[idx(o, effB)]
		}
	}
	// The result is the promotion of the two branches; the condition's dtype does
	// not enter it. A branch of a wider dtype than the result is rounded down on
	// store, so where rounds like an elementwise op rather than carrying a value
	// through unchanged.
	res := roundedBinaryResult(out, shape, Promote(a.DType(), b.DType()))
	return track2(res, a, b, func() {
		g := res.Grad
		for o := 0; o < n; o++ {
			if cond.Data[idx(o, effC)] != 0 {
				if a.RequiresGrad {
					a.ensureGrad()[idx(o, effA)] += g[o]
				}
			} else if b.RequiresGrad {
				b.ensureGrad()[idx(o, effB)] += g[o]
			}
		}
	}), nil
}

// --- axis helpers ----------------------------------------------------------

// axisSpans returns (before, length, after) for reducing/scanning an axis,
// where flat index = i*length*after + k*after + j.
func axisSpans(shape []int, axis int) (before, length, after int) {
	before, length, after = 1, shape[axis], 1
	for i := 0; i < axis; i++ {
		before *= shape[i]
	}
	for i := axis + 1; i < len(shape); i++ {
		after *= shape[i]
	}
	return
}

func removeAxis(shape []int, axis int) []int {
	out := make([]int, 0, len(shape)-1)
	out = append(out, shape[:axis]...)
	out = append(out, shape[axis+1:]...)
	return out
}

// normalizeAxis turns a possibly negative axis into an index, or says why it
// cannot.
//
// The message names the axis THE CALLER WROTE, not the half-adjusted value.
// `sum(m, -7)` on a rank-2 tensor says "axis -7 out of range for rank 2"; it
// used to say "axis -5", which is -7 plus the rank and appears nowhere in the
// program, so a reader had to reverse the adjustment to find their own input.
// src/tensor.tw's normalize_axis already reported the original, so the two
// implementations disagreed on the one input that reaches this message at all.
func normalizeAxis(axis, rank int) (int, error) {
	adjusted := axis
	if adjusted < 0 {
		adjusted += rank
	}
	if adjusted < 0 || adjusted >= rank {
		return 0, fmt.Errorf("axis %d out of range for rank %d", axis, rank)
	}
	return adjusted, nil
}

func reduceAxis(t *Tensor, axis int, mean bool) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	outN := before * after
	out := make([]float64, outN)
	scale := 1.0
	if mean {
		scale = 1.0 / float64(L)
	}
	// A narrow reduction accumulates at the accumulation dtype and rounds to the
	// result dtype once; f64 keeps the plain running sum, bit-identical to before.
	dt := reduceResultDType(t.DType(), mean)
	acc := AccDType(dt)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			s := 0.0
			base := i*L*after + j
			if dt == DTF64 {
				for k := 0; k < L; k++ {
					s += t.Data[base+k*after]
				}
				out[i*after+j] = s * scale
			} else {
				for k := 0; k < L; k++ {
					s = RoundToDType(acc, s+t.Data[base+k*after])
				}
				out[i*after+j] = RoundToDType(dt, s*scale)
			}
		}
	}
	res := &Tensor{Data: out, Shape: removeAxis(t.Shape, axis)}
	if dt != DTF64 {
		res.WithDType(dt)
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for i := 0; i < before; i++ {
			for j := 0; j < after; j++ {
				gv := res.Grad[i*after+j] * scale
				base := i*L*after + j
				for k := 0; k < L; k++ {
					gt[base+k*after] += gv
				}
			}
		}
	}), nil
}

func SumAxis(t *Tensor, axis int) (*Tensor, error)  { return reduceAxis(t, axis, false) }
func MeanAxis(t *Tensor, axis int) (*Tensor, error) { return reduceAxis(t, axis, true) }

// extremeAxis implements max/min along an axis, routing gradient to the
// selected element.
func extremeAxis(t *Tensor, axis int, wantMax bool) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	outN := before * after
	out := make([]float64, outN)
	argFlat := make([]int, outN)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			best := t.Data[base]
			bestIdx := base
			for k := 1; k < L; k++ {
				v := t.Data[base+k*after]
				if (wantMax && v > best) || (!wantMax && v < best) {
					best, bestIdx = v, base+k*after
				}
			}
			out[i*after+j] = best
			argFlat[i*after+j] = bestIdx
		}
	}
	// A selection carries a chosen element through unchanged, so it keeps the
	// input dtype and needs no rounding: the element is already a value of it.
	res := (&Tensor{Data: out, Shape: removeAxis(t.Shape, axis)}).withDTypeLike(t)
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for o := 0; o < outN; o++ {
			gt[argFlat[o]] += res.Grad[o]
		}
	}), nil
}

func MaxAxis(t *Tensor, axis int) (*Tensor, error) { return extremeAxis(t, axis, true) }
func MinAxis(t *Tensor, axis int) (*Tensor, error) { return extremeAxis(t, axis, false) }

// ArgmaxAxis returns the index of the maximum along an axis (not differentiable).
func ArgmaxAxis(t *Tensor, axis int) (*Tensor, error) { return argExtremeAxis(t, axis, true) }

// ArgminAxis returns the index of the minimum along an axis (not differentiable).
func ArgminAxis(t *Tensor, axis int) (*Tensor, error) { return argExtremeAxis(t, axis, false) }

// argExtremeAxis is the shared body. Ties go to the first occurrence, because
// the comparison is strict, and that has to match sort's stability and the
// cumulative scans: a rule about ties that differs between two operations moves
// an answer when a value is merely repeated.
func argExtremeAxis(t *Tensor, axis int, wantMax bool) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	out := make([]float64, before*after)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			best, bestK := t.Data[base], 0
			for k := 1; k < L; k++ {
				v := t.Data[base+k*after]
				if (wantMax && v > best) || (!wantMax && v < best) {
					best, bestK = v, k
				}
			}
			out[i*after+j] = float64(bestK)
		}
	}
	// An index, so i32 (docs/dtypes.md): an argmax returned in a narrow float
	// would round to the wrong element once the count passed the mantissa.
	return (&Tensor{Data: out, Shape: removeAxis(t.Shape, axis)}).WithDType(DTI32), nil
}

// FlipAxis reverses the order along one axis, keeping the shape.
//
// Differentiable, and exactly: reversing is a permutation, so the backward pass
// is the same reversal. It is its own inverse, which is worth stating because it
// makes the gradient obvious rather than something to derive.
func FlipAxis(t *Tensor, axis int) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)

	out := make([]float64, len(t.Data))
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			for k := 0; k < L; k++ {
				out[base+k*after] = t.Data[base+(L-1-k)*after]
			}
		}
	}

	res := (&Tensor{Data: out, Shape: append([]int{}, t.Shape...)}).withDTypeLike(t)
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for i := 0; i < before; i++ {
			for j := 0; j < after; j++ {
				base := i*L*after + j
				for k := 0; k < L; k++ {
					gt[base+(L-1-k)*after] += res.Grad[base+k*after]
				}
			}
		}
	}), nil
}

// MaxAll / MinAll reduce to a scalar.
func extremeAll(t *Tensor, wantMax bool) *Tensor {
	best, bestIdx := t.Data[0], 0
	for i, v := range t.Data {
		if (wantMax && v > best) || (!wantMax && v < best) {
			best, bestIdx = v, i
		}
	}
	res := Scalar(best).withDTypeLike(t)
	return track1(res, t, func() {
		if t.RequiresGrad {
			t.ensureGrad()[bestIdx] += res.Grad[0]
		}
	})
}

func MaxAll(t *Tensor) *Tensor { return extremeAll(t, true) }
func MinAll(t *Tensor) *Tensor { return extremeAll(t, false) }

// --- softmax and logsumexp -------------------------------------------------

// Softmax computes a numerically stable softmax along an axis.
func Softmax(t *Tensor, axis int) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	out := make([]float64, len(t.Data))
	// An integer softmax is not an integer, so it comes back as f32; a float keeps
	// its dtype. The exponentials and their total are held at the accumulation
	// dtype and only the quotient is stored. This is the op where a narrow
	// accumulator does the most damage: the terms span orders of magnitude by
	// construction, so a bf16 total is the largest term and nothing else. f64 keeps
	// its plain running sum, bit-identical to before.
	dt := t.DType()
	if isIntDType(dt) {
		dt = DTF32
	}
	acc := AccDType(dt)
	narrow := dt != DTF64
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			m := t.Data[base]
			for k := 1; k < L; k++ {
				if v := t.Data[base+k*after]; v > m {
					m = v
				}
			}
			sum := 0.0
			for k := 0; k < L; k++ {
				e := math.Exp(t.Data[base+k*after] - m)
				if narrow {
					e = RoundToDType(acc, e)
					sum = RoundToDType(acc, sum+e)
				} else {
					sum += e
				}
				out[base+k*after] = e
			}
			for k := 0; k < L; k++ {
				out[base+k*after] /= sum
			}
		}
	}
	res := contractionResult(out, append([]int(nil), t.Shape...), dt)
	// Forward-mode 2-jet. The jacobian is y_i(d_ij - y_j), so the first tangent
	// is yd_i = y_i(xd_i - s) with s the y-weighted mean of the input tangents.
	// Differentiating that once more along the direction gives the second, where
	// sdot is d/dt of s. The max subtraction does not appear: it cancels out of
	// y, which is all the jet reads.
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			for i := 0; i < before; i++ {
				for j := 0; j < after; j++ {
					base := i*L*after + j
					s := 0.0
					for k := 0; k < L; k++ {
						o := base + k*after
						s += out[o] * t.jet.d[o]
					}
					sdot := 0.0
					for k := 0; k < L; k++ {
						o := base + k*after
						yd := out[o] * (t.jet.d[o] - s)
						res.jet.d[o] = yd
						sdot += yd*t.jet.d[o] + out[o]*t.jet.dd[o]
					}
					for k := 0; k < L; k++ {
						o := base + k*after
						res.jet.dd[o] = res.jet.d[o]*(t.jet.d[o]-s) + out[o]*(t.jet.dd[o]-sdot)
					}
				}
			}
		}
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for i := 0; i < before; i++ {
			for j := 0; j < after; j++ {
				base := i*L*after + j
				dot := 0.0
				for k := 0; k < L; k++ {
					dot += res.Grad[base+k*after] * out[base+k*after]
				}
				for k := 0; k < L; k++ {
					s := out[base+k*after]
					gt[base+k*after] += s * (res.Grad[base+k*after] - dot)
				}
			}
		}
	}), nil
}

// LogSumExp reduces an axis with a numerically stable log-sum-exp.
func LogSumExp(t *Tensor, axis int) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	outN := before * after
	out := make([]float64, outN)
	soft := make([]float64, len(t.Data)) // softmax weights for the backward pass
	// An integer logsumexp comes back as f32; a float keeps its dtype. The total
	// is held at the accumulation dtype, as in softmax and for the same reason:
	// the terms differ by orders of magnitude on purpose. f64 keeps its plain
	// running sum, bit-identical to before.
	dt := t.DType()
	if isIntDType(dt) {
		dt = DTF32
	}
	acc := AccDType(dt)
	narrow := dt != DTF64
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			m := t.Data[base]
			for k := 1; k < L; k++ {
				if v := t.Data[base+k*after]; v > m {
					m = v
				}
			}
			sum := 0.0
			for k := 0; k < L; k++ {
				e := math.Exp(t.Data[base+k*after] - m)
				soft[base+k*after] = e
				if narrow {
					sum = RoundToDType(acc, sum+e)
				} else {
					sum += e
				}
			}
			out[i*after+j] = m + math.Log(sum)
			for k := 0; k < L; k++ {
				soft[base+k*after] /= sum
			}
		}
	}
	res := roundedBinaryResult(out, removeAxis(t.Shape, axis), dt)
	// Forward-mode. The gradient of logsumexp is softmax, so the first output
	// tangent is s, the y-weighted mean of the input tangents, and the second is
	// its derivative along the direction. y is recomputed as exp(x - out) rather
	// than read from soft: soft is exp(x - m) / sum and exp(x - out) is the same
	// value by a different operation, and recomputing is what src/tensor.tw does,
	// so the two stay bit-identical.
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			for i := 0; i < before; i++ {
				for j := 0; j < after; j++ {
					oi := i*after + j
					base := i*L*after + j
					s := 0.0
					for k := 0; k < L; k++ {
						p := base + k*after
						s += math.Exp(t.Data[p]-out[oi]) * t.jet.d[p]
					}
					ldd := 0.0
					for k := 0; k < L; k++ {
						p := base + k*after
						y := math.Exp(t.Data[p] - out[oi])
						ldd += y * (t.jet.d[p]*(t.jet.d[p]-s) + t.jet.dd[p])
					}
					res.jet.d[oi] = s
					res.jet.dd[oi] = ldd
				}
			}
		}
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for i := 0; i < before; i++ {
			for j := 0; j < after; j++ {
				g := res.Grad[i*after+j]
				base := i*L*after + j
				for k := 0; k < L; k++ {
					gt[base+k*after] += soft[base+k*after] * g
				}
			}
		}
	}), nil
}

// --- shape ops -------------------------------------------------------------

// Reshape returns a tensor with the same data and a new shape (same size).
func Reshape(t *Tensor, shape []int) (*Tensor, error) {
	if numel(shape) != len(t.Data) {
		return nil, fmt.Errorf("reshape: cannot fit %d elements into shape %v", len(t.Data), shape)
	}
	data := make([]float64, len(t.Data))
	copy(data, t.Data)
	res := (&Tensor{Data: data, Shape: append([]int(nil), shape...)}).withDTypeLike(t)
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			copy(res.jet.d, t.jet.d)
			copy(res.jet.dd, t.jet.dd)
		}
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for i := range gt {
			gt[i] += res.Grad[i]
		}
	}), nil
}

// TransposePerm permutes axes. With no axes it reverses them (a plain
// transpose).
func TransposePerm(t *Tensor, axes []int) (*Tensor, error) {
	r := len(t.Shape)
	if len(axes) == 0 {
		axes = make([]int, r)
		for i := range axes {
			axes[i] = r - 1 - i
		}
	}
	if len(axes) != r {
		return nil, fmt.Errorf("transpose: got %d axes for a rank-%d tensor", len(axes), r)
	}
	seen := make([]bool, r)
	outShape := make([]int, r)
	for i, ax := range axes {
		if ax < 0 || ax >= r || seen[ax] {
			return nil, fmt.Errorf("transpose: invalid axis permutation %v", axes)
		}
		seen[ax] = true
		outShape[i] = t.Shape[ax]
	}
	inStr := strides(t.Shape)
	outStr := strides(outShape)
	n := len(t.Data)
	out := make([]float64, n)
	// inIndex maps an output flat index back to the input flat index.
	inIndex := func(o int) int {
		rem, in := o, 0
		for i := 0; i < r; i++ {
			coord := rem / outStr[i]
			rem = rem % outStr[i]
			in += coord * inStr[axes[i]]
		}
		return in
	}
	for o := 0; o < n; o++ {
		out[o] = t.Data[inIndex(o)]
	}
	res := (&Tensor{Data: out, Shape: outShape}).withDTypeLike(t)
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			for o := 0; o < n; o++ {
				src := inIndex(o)
				res.jet.d[o] = t.jet.d[src]
				res.jet.dd[o] = t.jet.dd[src]
			}
		}
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for o := 0; o < n; o++ {
			gt[inIndex(o)] += res.Grad[o]
		}
	}), nil
}

// IndexAxis0 selects element/row `idx` along the first axis, dropping that axis:
// a 1-D tensor yields a scalar, and an N-D tensor yields a row of shape
// t.Shape[1:]. It is differentiable — gradient flows to the selected row — and
// carries forward-mode tangents, so both grad and hessian pass through `x[i]`.
func IndexAxis0(t *Tensor, idx int) (*Tensor, error) {
	if len(t.Shape) == 0 {
		return nil, fmt.Errorf("cannot index a scalar")
	}
	dim0 := t.Shape[0]
	if idx < 0 || idx >= dim0 {
		return nil, fmt.Errorf("index %d out of range [0, %d)", idx, dim0)
	}
	rowSize := 1
	for _, d := range t.Shape[1:] {
		rowSize *= d
	}
	off := idx * rowSize
	data := make([]float64, rowSize)
	copy(data, t.Data[off:off+rowSize])
	// A row selection carries its elements through unchanged, so it keeps the
	// input dtype (docs/dtypes.md).
	res := (&Tensor{Data: data, Shape: append([]int(nil), t.Shape[1:]...)}).withDTypeLike(t)
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			copy(res.jet.d, t.jet.d[off:off+rowSize])
			copy(res.jet.dd, t.jet.dd[off:off+rowSize])
		}
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for i := 0; i < rowSize; i++ {
			gt[off+i] += res.Grad[i]
		}
	}), nil
}

// SliceAxis0 returns rows [start, end) along the first axis, keeping the
// trailing dimensions. It is differentiable: gradient flows to the selected
// rows.
func SliceAxis0(t *Tensor, start, end int) (*Tensor, error) {
	if len(t.Shape) == 0 {
		return nil, fmt.Errorf("cannot slice a scalar")
	}
	dim0 := t.Shape[0]
	if start < 0 {
		start += dim0
	}
	if end < 0 {
		end += dim0
	}
	if start < 0 || end > dim0 || start > end {
		return nil, fmt.Errorf("slice [%d:%d] out of range for first dim %d", start, end, dim0)
	}
	rowSize := 1
	for _, d := range t.Shape[1:] {
		rowSize *= d
	}
	outShape := append([]int{end - start}, t.Shape[1:]...)
	data := make([]float64, (end-start)*rowSize)
	copy(data, t.Data[start*rowSize:end*rowSize])
	// A slice carries its rows through unchanged, so it keeps the input dtype.
	res := (&Tensor{Data: data, Shape: outShape}).withDTypeLike(t)
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			off := start * rowSize
			copy(res.jet.d, t.jet.d[off:off+len(data)])
			copy(res.jet.dd, t.jet.dd[off:off+len(data)])
		}
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for i := 0; i < len(data); i++ {
			gt[start*rowSize+i] += res.Grad[i]
		}
	}), nil
}

// Concat joins tensors along an axis. Shapes must match on every other axis.
func Concat(tensors []*Tensor, axis int) (*Tensor, error) {
	if len(tensors) == 0 {
		return nil, fmt.Errorf("concat: need at least one tensor")
	}
	rank := len(tensors[0].Shape)
	axis, err := normalizeAxis(axis, rank)
	if err != nil {
		return nil, err
	}
	outShape := append([]int(nil), tensors[0].Shape...)
	total := 0
	for _, t := range tensors {
		if len(t.Shape) != rank {
			return nil, fmt.Errorf("concat: mismatched ranks")
		}
		for d := 0; d < rank; d++ {
			if d != axis && t.Shape[d] != tensors[0].Shape[d] {
				return nil, fmt.Errorf("concat: shapes differ on axis %d", d)
			}
		}
		total += t.Shape[axis]
	}
	outShape[axis] = total
	before, _, after := axisSpans(outShape, axis)
	out := make([]float64, numel(outShape))
	// Copy each input into its slice of the output axis.
	type placement struct {
		t      *Tensor
		offset int
	}
	var places []placement
	off := 0
	for _, t := range tensors {
		places = append(places, placement{t, off})
		off += t.Shape[axis]
	}
	for _, pl := range places {
		L := pl.t.Shape[axis]
		for i := 0; i < before; i++ {
			for k := 0; k < L; k++ {
				for j := 0; j < after; j++ {
					out[i*total*after+(pl.offset+k)*after+j] = pl.t.Data[i*L*after+k*after+j]
				}
			}
		}
	}
	prev := make([]*Tensor, len(tensors))
	copy(prev, tensors)
	// Concatenation joins elements unchanged, so the result is the promotion of
	// the pieces' dtypes; the elements are already valid in it, so nothing rounds.
	catDT := tensors[0].DType()
	for _, t := range tensors[1:] {
		catDT = Promote(catDT, t.DType())
	}
	res := (&Tensor{Data: out, Shape: outShape}).WithDType(catDT)
	anyRG := false
	for _, t := range tensors {
		if t.RequiresGrad {
			anyRG = true
			break
		}
	}
	if recordJets && anyRG {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			for _, pl := range places {
				L := pl.t.Shape[axis]
				for i := 0; i < before; i++ {
					for k := 0; k < L; k++ {
						for j := 0; j < after; j++ {
							dst := i*total*after + (pl.offset+k)*after + j
							src := i*L*after + k*after + j
							res.jet.d[dst] = pl.t.jet.d[src]
							res.jet.dd[dst] = pl.t.jet.dd[src]
						}
					}
				}
			}
		}
	}
	return trackN(res, prev, func() {
		for _, pl := range places {
			if !pl.t.RequiresGrad {
				continue
			}
			gt := pl.t.ensureGrad()
			L := pl.t.Shape[axis]
			for i := 0; i < before; i++ {
				for k := 0; k < L; k++ {
					for j := 0; j < after; j++ {
						gt[i*L*after+k*after+j] += res.Grad[i*total*after+(pl.offset+k)*after+j]
					}
				}
			}
		}
	}), nil
}

// --- product and median ----------------------------------------------------

// Prod multiplies every element together.
func Prod(t *Tensor) *Tensor { return prodOver(t, len(t.Data), 1, 1, []int{}) }

// ProdAxis multiplies along one axis, dropping it from the shape.
func ProdAxis(t *Tensor, axis int) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	return prodOver(t, L, before, after, removeAxis(t.Shape, axis)), nil
}

// prodOver is the shared body: `before * after` independent runs of length L,
// strided by `after`. Passing before=after=1 and L=len(Data) reduces the whole
// tensor, so the two entry points above are the same code.
//
// The derivative of a product with respect to one factor is the product of the
// others, which is the whole product divided by that factor — except that the
// division is invalid exactly when the factor is zero, and that is not a rare
// case for a tensor. So the zeros are counted instead:
//
//	no zeros    every factor gets total/x
//	one zero    that factor gets the product of the rest; everything else 0
//	two or more every derivative is 0, because every product of the others
//	            still contains a zero
func prodOver(t *Tensor, L, before, after int, outShape []int) *Tensor {
	outN := before * after
	out := make([]float64, outN)
	// Product of the non-zero factors, and how many zeros, per run. Both are
	// needed by the backward pass and are free to keep while multiplying.
	nzProd := make([]float64, outN)
	zeros := make([]int, outN)

	// A narrow product runs at the accumulation dtype and rounds to the result
	// dtype once, for the same reason a sum does -- more so, since a running
	// product leaves a narrow exponent range fast (docs/dtypes.md). f64 keeps the
	// plain running product.
	dt := t.DType()
	acc := AccDType(dt)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			p := 1.0
			nz := 1.0
			z := 0
			for k := 0; k < L; k++ {
				v := t.Data[base+k*after]
				if dt == DTF64 {
					p *= v
				} else {
					p = RoundToDType(acc, p*v)
				}
				if v == 0 {
					z++
				} else {
					nz *= v
				}
			}
			idx := i*after + j
			if dt == DTF64 {
				out[idx] = p
			} else {
				out[idx] = RoundToDType(dt, p)
			}
			nzProd[idx] = nz
			zeros[idx] = z
		}
	}

	res := &Tensor{Data: out, Shape: outShape}
	if dt != DTF64 {
		res.WithDType(dt)
	}
	// Forward-mode. The first tangent is p*u with u the sum of d_k/x_k, and the
	// second follows from differentiating that. Zeros break the division, so the
	// zero count splits it into cases, the forward twin of the backward's split:
	// with one zero only that factor's derivative survives; with two, only their
	// cross term; with three or more, nothing.
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			for i := 0; i < before; i++ {
				for j := 0; j < after; j++ {
					idx := i*after + j
					base := i*L*after + j
					p := out[idx]
					nz := nzProd[idx]
					switch zeros[idx] {
					case 0:
						u, v, w := 0.0, 0.0, 0.0
						for k := 0; k < L; k++ {
							pk := base + k*after
							rk := t.jet.d[pk] / t.Data[pk]
							u += rk
							v += t.jet.dd[pk] / t.Data[pk]
							w += rk * rk
						}
						res.jet.d[idx] = p * u
						res.jet.dd[idx] = p * (u*u + v - w)
					case 1:
						q, r := -1, 0.0
						for k := 0; k < L; k++ {
							pk := base + k*after
							if t.Data[pk] == 0 {
								q = pk
							} else {
								r += t.jet.d[pk] / t.Data[pk]
							}
						}
						res.jet.d[idx] = nz * t.jet.d[q]
						res.jet.dd[idx] = 2*nz*t.jet.d[q]*r + nz*t.jet.dd[q]
					case 2:
						q1, q2 := -1, -1
						for k := 0; k < L; k++ {
							pk := base + k*after
							if t.Data[pk] == 0 {
								if q1 < 0 {
									q1 = pk
								} else {
									q2 = pk
								}
							}
						}
						res.jet.d[idx] = 0
						res.jet.dd[idx] = 2 * nz * t.jet.d[q1] * t.jet.d[q2]
					default:
						res.jet.d[idx] = 0
						res.jet.dd[idx] = 0
					}
				}
			}
		}
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for i := 0; i < before; i++ {
			for j := 0; j < after; j++ {
				idx := i*after + j
				g := res.Grad[idx]
				if g == 0 {
					continue
				}
				base := i*L*after + j
				switch zeros[idx] {
				case 0:
					for k := 0; k < L; k++ {
						p := base + k*after
						gt[p] += g * out[idx] / t.Data[p]
					}
				case 1:
					// Only the zero factor has a non-zero derivative: every
					// other one's "product of the rest" still contains it.
					for k := 0; k < L; k++ {
						p := base + k*after
						if t.Data[p] == 0 {
							gt[p] += g * nzProd[idx]
						}
					}
				}
			}
		}
	})
}

// Median returns the median of every element.
func Median(t *Tensor) *Tensor { return medianOver(t, len(t.Data), 1, 1, []int{}) }

// MedianAxis takes the median along one axis, dropping it from the shape.
func MedianAxis(t *Tensor, axis int) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	return medianOver(t, L, before, after, removeAxis(t.Shape, axis)), nil
}

// medianOver mirrors prodOver's layout.
//
// The gradient routes to whichever element was selected, the same way max and
// min do: the median of a run is one of its values, so moving any other value
// a little does not move the answer at all. An even-length run averages the two
// middle values, so each of those takes half.
//
// Selection is by sorting indices rather than values, because the backward pass
// needs to know *which* element was chosen, not just what it held.
func medianOver(t *Tensor, L, before, after int, outShape []int) *Tensor {
	outN := before * after
	out := make([]float64, outN)
	// One or two source offsets per output, and the weight each carries back.
	lo := make([]int, outN)
	hi := make([]int, outN)

	idxBuf := make([]int, L)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			for k := 0; k < L; k++ {
				idxBuf[k] = base + k*after
			}
			sort.Slice(idxBuf, func(a, b int) bool {
				return t.Data[idxBuf[a]] < t.Data[idxBuf[b]]
			})
			idx := i*after + j
			mid := L / 2
			if L%2 == 1 {
				lo[idx] = idxBuf[mid]
				hi[idx] = idxBuf[mid]
				out[idx] = t.Data[idxBuf[mid]]
			} else {
				lo[idx] = idxBuf[mid-1]
				hi[idx] = idxBuf[mid]
				out[idx] = (t.Data[idxBuf[mid-1]] + t.Data[idxBuf[mid]]) / 2
			}
		}
	}

	// A selection keeps the input dtype (docs/dtypes.md); the even-count case
	// stores the mean of the two middle elements unrounded, as the reference does.
	res := (&Tensor{Data: out, Shape: outShape}).withDTypeLike(t)
	// Forward-mode. Median is a selection, locally linear, so a tangent rides
	// with the value it selects and there is no second-order term of its own: the
	// output tangent is the input's at the middle element, or the average of the
	// two for an even length, and the same for the second tangent. It is the
	// forward twin of the backward scatter below.
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			for idx := 0; idx < outN; idx++ {
				if lo[idx] == hi[idx] {
					res.jet.d[idx] = t.jet.d[lo[idx]]
					res.jet.dd[idx] = t.jet.dd[lo[idx]]
				} else {
					res.jet.d[idx] = (t.jet.d[lo[idx]] + t.jet.d[hi[idx]]) / 2
					res.jet.dd[idx] = (t.jet.dd[lo[idx]] + t.jet.dd[hi[idx]]) / 2
				}
			}
		}
	}
	return track1(res, t, func() {
		if !t.RequiresGrad {
			return
		}
		gt := t.ensureGrad()
		for idx := 0; idx < outN; idx++ {
			g := res.Grad[idx]
			if g == 0 {
				continue
			}
			if lo[idx] == hi[idx] {
				gt[lo[idx]] += g
			} else {
				gt[lo[idx]] += g / 2
				gt[hi[idx]] += g / 2
			}
		}
	})
}

// BroadcastTo expands a tensor to `shape` under the usual right-aligned rules:
// every axis of the input must already match the target or be 1, and the input
// may have fewer axes than the target, which are prepended.
//
// Broadcasting normally happens implicitly inside a binary op, and that is
// enough right up until the shape you need to broadcast against is not one of
// the operands. The case that forces this is a reduction result: reducing axis
// 1 of a [2, 3] gives a [2], and [2] does not line up against [2, 3] because
// alignment is from the right. Naming the target shape is the way out, and it
// is also what "keepdims" is doing in other array libraries, only spelled as an
// operation rather than a flag on every reduction.
//
// The gradient is the reverse of the expansion: every output that read a given
// input element sends its gradient back to it, so a broadcast axis sums.
func BroadcastTo(t *Tensor, shape []int) (*Tensor, error) {
	if len(shape) < len(t.Shape) {
		return nil, fmt.Errorf("cannot broadcast %v to %v: fewer axes in target", t.Shape, shape)
	}
	// Checking against BroadcastShape rather than reimplementing the rule keeps
	// one definition of what is compatible. The result must be the target
	// exactly: a shape that broadcasts *with* the target but is not equal to it
	// (a 5 against a 1, say) is an expansion of the target too, which is not
	// what was asked for.
	got, ok := BroadcastShape(t.Shape, shape)
	if !ok || !shapeEqual(got, shape) {
		return nil, fmt.Errorf("cannot broadcast %v to %v", t.Shape, shape)
	}

	n := numel(shape)
	out := make([]float64, n)
	rank := len(shape)
	eff := effStrides(t.Shape, shape)

	// The source offset per output, kept because the backward pass needs the
	// same mapping and recomputing it there would mean walking the odometer
	// twice. Only kept when a gradient is actually wanted.
	var src []int
	if t.RequiresGrad {
		src = make([]int, n)
	}

	coord := make([]int, rank)
	idx := 0
	for o := 0; o < n; o++ {
		if src != nil {
			src[o] = idx
		}
		out[o] = t.Data[idx]
		for d := rank - 1; d >= 0; d-- {
			coord[d]++
			idx += eff[d]
			if coord[d] < shape[d] {
				break
			}
			coord[d] = 0
			idx -= eff[d] * shape[d]
		}
	}

	res := (&Tensor{Data: out, Shape: append([]int(nil), shape...)}).withDTypeLike(t)
	return track1(res, t, func() {
		gt := t.ensureGrad()
		for o := 0; o < n; o++ {
			gt[src[o]] += res.Grad[o]
		}
	}), nil
}

// Split cuts a tensor into consecutive pieces along one axis. It is the inverse
// of Concat: splitting with the sizes the pieces had and concatenating the
// result on the same axis gives the original back.
//
// `sizes` are the lengths of the pieces along `axis` and must add up to that
// axis exactly. A short or long list is an error rather than a silent drop,
// because a split that quietly loses a row is the kind of thing that shows up
// later as a wrong loss rather than as a crash.
//
// Each piece carries its own gradient path back into the parent's slice, so
// splitting a parameter, using the halves differently, and differentiating
// through both accumulates into one gradient.
func Split(t *Tensor, sizes []int, axis int) ([]*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	if len(sizes) == 0 {
		return nil, fmt.Errorf("split: need at least one piece")
	}
	total := 0
	for _, s := range sizes {
		if s < 0 {
			return nil, fmt.Errorf("split: negative size %d", s)
		}
		total += s
	}
	if total != t.Shape[axis] {
		return nil, fmt.Errorf("split: sizes sum to %d but axis %d has length %d",
			total, axis, t.Shape[axis])
	}

	before, L, after := axisSpans(t.Shape, axis)
	out := make([]*Tensor, len(sizes))
	offset := 0
	for p, size := range sizes {
		shape := append([]int(nil), t.Shape...)
		shape[axis] = size
		data := make([]float64, before*size*after)
		// Copied rather than aliased. A view sharing the parent's backing array
		// would make a later in-place write to one piece silently change the
		// others' idea of the parent, and nothing else in this package hands
		// out aliases.
		for i := 0; i < before; i++ {
			for k := 0; k < size; k++ {
				for j := 0; j < after; j++ {
					data[i*size*after+k*after+j] = t.Data[i*L*after+(offset+k)*after+j]
				}
			}
		}
		// Each piece carries its slice of the input through, so it keeps the dtype.
		piece := (&Tensor{Data: data, Shape: shape}).withDTypeLike(t)
		// `offset` and `size` are captured per iteration; the closure below runs
		// long after this loop has moved on.
		off, sz := offset, size
		out[p] = track1(piece, t, func() {
			gt := t.ensureGrad()
			for i := 0; i < before; i++ {
				for k := 0; k < sz; k++ {
					for j := 0; j < after; j++ {
						gt[i*L*after+(off+k)*after+j] += piece.Grad[i*sz*after+k*after+j]
					}
				}
			}
		})
		offset += size
	}
	return out, nil
}

// SplitEqual cuts a tensor into `n` equally sized pieces along `axis`, which is
// the common case and the one where spelling out the sizes is just arithmetic
// the caller should not have to do. The axis must divide evenly; numpy's
// `array_split` tolerates a remainder, and that leniency turns a wrong `n` into
// ragged output rather than an error, so it is not copied here.
func SplitEqual(t *Tensor, n, axis int) ([]*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, fmt.Errorf("split: piece count must be positive, got %d", n)
	}
	L := t.Shape[axis]
	if L%n != 0 {
		return nil, fmt.Errorf("split: axis %d has length %d, which %d does not divide evenly",
			axis, L, n)
	}
	sizes := make([]int, n)
	for i := range sizes {
		sizes[i] = L / n
	}
	return Split(t, sizes, axis)
}

// --- sorting ---------------------------------------------------------------

// Sort orders each run along an axis, ascending or descending.
//
// Differentiable, and exactly so: sorting is a permutation, and the derivative
// of a permutation is the inverse permutation. Whatever gradient arrives at the
// element now in position 3 belongs to whichever element started there, so the
// backward pass sends each one home. No approximation is involved, which is why
// this is worth having rather than something a caller fakes with argsort and a
// gather.
//
// Ties keep their original relative order, which is what makes the result
// reproducible between runs over the same data. An unstable sort would return
// the same values in a different arrangement and, because the gradient follows
// the arrangement, a different gradient.
func SortAxis(t *Tensor, axis int, descending bool) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)

	out := make([]float64, len(t.Data))
	// Where each output element came from, which is the permutation the
	// backward pass has to invert.
	origin := make([]int, len(t.Data))

	idx := make([]int, L)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			for k := range idx {
				idx[k] = k
			}
			sort.SliceStable(idx, func(a, b int) bool {
				x := t.Data[base+idx[a]*after]
				y := t.Data[base+idx[b]*after]
				if descending {
					return x > y
				}
				return x < y
			})
			for k, from := range idx {
				dst := base + k*after
				src := base + from*after
				out[dst] = t.Data[src]
				origin[dst] = src
			}
		}
	}

	// A sort is a permutation of the input, so it keeps the input dtype.
	res := (&Tensor{Data: out, Shape: append([]int(nil), t.Shape...)}).withDTypeLike(t)
	// Forward-mode: sort is a permutation, so a tangent rides with its value.
	// The output at dst took its value from origin[dst], so its tangent is the
	// input's there. This is the forward twin of the backward scatter below.
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			for dst, src := range origin {
				res.jet.d[dst] = t.jet.d[src]
				res.jet.dd[dst] = t.jet.dd[src]
			}
		}
	}
	return track1(res, t, func() {
		gt := t.ensureGrad()
		for dst, src := range origin {
			gt[src] += res.Grad[dst]
		}
	}), nil
}

// ArgsortAxis gives the indices that would sort each run along an axis.
//
// Not differentiable, and not because it was left out: the output is a set of
// positions, and moving an input value by an infinitesimal amount does not move
// an index at all until it crosses a neighbour, at which point it jumps. The
// derivative is zero almost everywhere and undefined on the boundaries, so
// there is nothing useful to propagate.
func ArgsortAxis(t *Tensor, axis int, descending bool) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	out := make([]float64, len(t.Data))

	idx := make([]int, L)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			for k := range idx {
				idx[k] = k
			}
			sort.SliceStable(idx, func(a, b int) bool {
				x := t.Data[base+idx[a]*after]
				y := t.Data[base+idx[b]*after]
				if descending {
					return x > y
				}
				return x < y
			})
			for k, from := range idx {
				out[base+k*after] = float64(from)
			}
		}
	}
	return (&Tensor{Data: out, Shape: append([]int(nil), t.Shape...)}).WithDType(DTI32), nil
}

// TopKAxis keeps the k largest values along an axis, largest first.
//
// The axis shrinks to k. This is the shape a caller wants for "the five most
// likely classes": the values, in order, with everything else dropped.
//
// Differentiable through the values that survive. A value outside the top k
// contributes nothing to the output, so its gradient is zero, which is correct
// rather than a simplification: change it a little and the output does not move.
func TopKAxis(t *Tensor, k, axis int, largest bool) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	if k <= 0 {
		return nil, fmt.Errorf("topk: k must be positive, got %d", k)
	}
	if k > L {
		return nil, fmt.Errorf("topk: k is %d but axis %d has length %d", k, axis, L)
	}

	shape := append([]int(nil), t.Shape...)
	shape[axis] = k
	out := make([]float64, before*k*after)
	origin := make([]int, before*k*after)

	idx := make([]int, L)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			for n := range idx {
				idx[n] = n
			}
			sort.SliceStable(idx, func(a, b int) bool {
				x := t.Data[base+idx[a]*after]
				y := t.Data[base+idx[b]*after]
				if largest {
					return x > y
				}
				return x < y
			})
			for n := 0; n < k; n++ {
				dst := i*k*after + n*after + j
				src := base + idx[n]*after
				out[dst] = t.Data[src]
				origin[dst] = src
			}
		}
	}

	// topk selects existing elements, so it keeps the input dtype.
	res := (&Tensor{Data: out, Shape: shape}).withDTypeLike(t)
	// Forward-mode: the k that survive carry their tangents through; the ones
	// dropped contribute nothing, exactly as in the backward pass. origin[dst]
	// is the input each kept output came from, so its tangent is the input's.
	if recordJets && t.RequiresGrad {
		res.jet = &jetState{}
		res.jet.jvp = func() {
			for dst, src := range origin {
				res.jet.d[dst] = t.jet.d[src]
				res.jet.dd[dst] = t.jet.dd[src]
			}
		}
	}
	return track1(res, t, func() {
		gt := t.ensureGrad()
		for dst, src := range origin {
			gt[src] += res.Grad[dst]
		}
	}), nil
}

// ArgTopKAxis gives the indices of the k largest values along an axis.
//
// Indices, so not differentiable, for the same reason as ArgsortAxis.
func ArgTopKAxis(t *Tensor, k, axis int, largest bool) (*Tensor, error) {
	axis, err := normalizeAxis(axis, len(t.Shape))
	if err != nil {
		return nil, err
	}
	before, L, after := axisSpans(t.Shape, axis)
	if k <= 0 {
		return nil, fmt.Errorf("argtopk: k must be positive, got %d", k)
	}
	if k > L {
		return nil, fmt.Errorf("argtopk: k is %d but axis %d has length %d", k, axis, L)
	}

	shape := append([]int(nil), t.Shape...)
	shape[axis] = k
	out := make([]float64, before*k*after)

	idx := make([]int, L)
	for i := 0; i < before; i++ {
		for j := 0; j < after; j++ {
			base := i*L*after + j
			for n := range idx {
				idx[n] = n
			}
			sort.SliceStable(idx, func(a, b int) bool {
				x := t.Data[base+idx[a]*after]
				y := t.Data[base+idx[b]*after]
				if largest {
					return x > y
				}
				return x < y
			})
			for n := 0; n < k; n++ {
				out[i*k*after+n*after+j] = float64(idx[n])
			}
		}
	}
	return (&Tensor{Data: out, Shape: shape}).WithDType(DTI32), nil
}
