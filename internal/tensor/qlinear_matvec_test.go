package tensor

import (
	"math"
	"testing"
)

// QLinear has a dedicated matrix-vector path for a one-row input, the shape all
// of autoregressive decoding takes, parallelised over the weight rows instead of
// the (single) input row. It must give exactly the same numbers as the general
// path, which this checks by comparing a one-row call against the first row of a
// two-row call over the same weight.
func TestQLinearMatVecMatchesGeneralPath(t *testing.T) {
	n, k := 300, 128
	w := make([]float64, n*k)
	for i := range w {
		w[i] = math.Sin(float64(i)*0.1) * 3
	}
	q, err := QuantizeI8(&Tensor{Data: w, Shape: []int{n, k}})
	if err != nil {
		t.Fatal(err)
	}

	x := make([]float64, k)
	for i := range x {
		x[i] = math.Cos(float64(i) * 0.2)
	}
	one, err := QLinear(&Tensor{Data: x, Shape: []int{1, k}}, q)
	if err != nil {
		t.Fatal(err)
	}

	two := append(append([]float64{}, x...), x...) // two identical rows
	batch, err := QLinear(&Tensor{Data: two, Shape: []int{2, k}}, q)
	if err != nil {
		t.Fatal(err)
	}

	for j := 0; j < n; j++ {
		if math.Float64bits(one.Data[j]) != math.Float64bits(batch.Data[j]) {
			t.Fatalf("row %d: matvec %v != general %v", j, one.Data[j], batch.Data[j])
		}
	}
}
