package tensor

import (
	"fmt"
	"math"
)

// QTensor is a weight matrix stored in 8-bit form: one signed byte per element
// plus one f64 scale per row. It is the memory lever for hosting large models —
// weights are the bulk of a model, and this holds them in an eighth of the space
// (n·k bytes + n·8, versus n·k·8 for the f64 matrix) at the cost of ~0.4% error.
//
// The quantisation is symmetric and per-row (per output channel), the scheme
// that keeps a dense layer accurate: each row gets its own scale from its own
// magnitude, so a row of small weights is not crushed to zero by a large row
// elsewhere. It is deliberately inference-only — a QTensor is a frozen weight,
// carries no gradient, and never appears on the training path.
type QTensor struct {
	Q     []int8    // rows*cols, row-major
	Scale []float64 // one per row
	Rows  int
	Cols  int
}

// QuantizeI8 packs a 2-D weight [n, k] into int8 with a per-row scale. The scale
// maps a row's largest magnitude to 127, so the full int8 range is used and the
// step is as fine as symmetric 8-bit allows. A row that is all zeros gets scale
// 0 and quantises to zeros, which dequantises back to zeros exactly.
func QuantizeI8(w *Tensor) (*QTensor, error) {
	if len(w.Shape) != 2 {
		return nil, fmt.Errorf("quantize expects a 2-D weight, got rank %d", len(w.Shape))
	}
	n, k := w.Shape[0], w.Shape[1]
	q := &QTensor{Q: make([]int8, n*k), Scale: make([]float64, n), Rows: n, Cols: k}
	for j := 0; j < n; j++ {
		row := w.Data[j*k : j*k+k]
		maxAbs := 0.0
		for _, v := range row {
			if a := math.Abs(v); a > maxAbs {
				maxAbs = a
			}
		}
		if maxAbs == 0 {
			continue // scale stays 0, Q stays zero
		}
		s := maxAbs / 127
		q.Scale[j] = s
		inv := 1 / s
		base := j * k
		for p, v := range row {
			r := math.Round(v * inv)
			if r > 127 {
				r = 127
			} else if r < -128 {
				r = -128
			}
			q.Q[base+p] = int8(r)
		}
	}
	return q, nil
}

// Dequantize reconstructs the f64 weight, for tests and for printing. It is the
// exact inverse of the pack up to the rounding QuantizeI8 already applied.
func (q *QTensor) Dequantize() *Tensor {
	d := make([]float64, q.Rows*q.Cols)
	for j := 0; j < q.Rows; j++ {
		s := q.Scale[j]
		base := j * q.Cols
		for p := 0; p < q.Cols; p++ {
			d[base+p] = float64(q.Q[base+p]) * s
		}
	}
	return &Tensor{Data: d, Shape: []int{q.Rows, q.Cols}}
}

// Bytes is the packed footprint: one byte per element plus eight per row scale.
// The f64 matrix it replaces is Rows*Cols*8, so the ratio is close to 8x for any
// realistic width.
func (q *QTensor) Bytes() int { return len(q.Q) + len(q.Scale)*8 }

// QLinear computes x @ Wᵀ where W is the int8 weight q — the quantised form of
// MatMulNT. The dequantisation folds into the row scale: since W[j,p] =
// scale[j]·Q[j,p], the dot product is scale[j]·Σ x[i,p]·Q[j,p], so each output
// element needs one f64 multiply by the scale, not k of them. x stays full
// precision; only the weight is small. Four accumulators as in mmNT.
func QLinear(x *Tensor, q *QTensor) (*Tensor, error) {
	x2 := x.Shape
	if len(x.Shape) == 1 {
		x2 = []int{1, x.Shape[0]}
	}
	if len(x2) != 2 {
		return nil, fmt.Errorf("qlinear expects a 1-D or 2-D input, got rank %d", len(x.Shape))
	}
	m, k := x2[0], x2[1]
	if k != q.Cols {
		return nil, fmt.Errorf("shape mismatch in qlinear: input inner %d != weight inner %d", k, q.Cols)
	}
	n := q.Rows
	out := make([]float64, m*n)
	// The single-row case is the whole of autoregressive decoding: one new token
	// projected through every weight, and the vocabulary head. Parallelising over
	// the input rows, as the general path does, leaves it on one core because
	// there is only one row. Parallelise over the weight rows instead, so a
	// matrix-vector product uses every core.
	if m == 1 {
		xRow := x.Data[:k]
		runChunks(n, workersFor(k*n), func(lo, hi int) {
			for j := lo; j < hi; j++ {
				qRow := q.Q[j*k : j*k+k]
				var s0, s1, s2, s3 float64
				p := 0
				for ; p+4 <= k; p += 4 {
					s0 += xRow[p] * float64(qRow[p])
					s1 += xRow[p+1] * float64(qRow[p+1])
					s2 += xRow[p+2] * float64(qRow[p+2])
					s3 += xRow[p+3] * float64(qRow[p+3])
				}
				s := (s0 + s1) + (s2 + s3)
				for ; p < k; p++ {
					s += xRow[p] * float64(qRow[p])
				}
				out[j] = s * q.Scale[j]
			}
		})
	} else {
		// Cache-tiled over the weight rows, as mmNT: a panel of int8 rows stays
		// resident while the x-chunk streams through it. The int8 weight is an
		// eighth of an f64 one, so the panel budget counts bytes not elements.
		jb := blockNBytes(k, n, 1)
		runChunks(m, workersFor(m*k*n), func(lo, hi int) {
			for j0 := 0; j0 < n; j0 += jb {
				j1 := j0 + jb
				if j1 > n {
					j1 = n
				}
				for i := lo; i < hi; i++ {
					xRow := x.Data[i*k : i*k+k]
					cRow := i * n
					for j := j0; j < j1; j++ {
						qRow := q.Q[j*k : j*k+k]
						var s0, s1, s2, s3 float64
						p := 0
						for ; p+4 <= k; p += 4 {
							s0 += xRow[p] * float64(qRow[p])
							s1 += xRow[p+1] * float64(qRow[p+1])
							s2 += xRow[p+2] * float64(qRow[p+2])
							s3 += xRow[p+3] * float64(qRow[p+3])
						}
						s := (s0 + s1) + (s2 + s3)
						for ; p < k; p++ {
							s += xRow[p] * float64(qRow[p])
						}
						out[cRow+j] = s * q.Scale[j]
					}
				}
			}
		})
	}
	var outShape []int
	if len(x.Shape) == 1 {
		outShape = []int{n}
	} else {
		outShape = []int{m, n}
	}
	res := &Tensor{Data: out, Shape: outShape}
	// The weight is frozen, but the activation is not, and the gradient with
	// respect to it is exact rather than approximate: quantisation happened when
	// the weight was packed, so what this kernel computes is a plain linear map
	// x @ Wqᵀ whose constants are the dequantised codes. dL/dx = g @ Wq. Without
	// this the op returned a value with no graph behind it, so grad through a
	// quantised layer answered zero instead of failing.
	return track1(res, x, func() {
		dX := mm(res.Grad, m, n, q.Dequantize().Data, k)
		gx := x.ensureGrad()
		for i := range dX {
			gx[i] += dX[i]
		}
	}), nil
}
