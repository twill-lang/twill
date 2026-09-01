package tensor

import (
	"math"
	"runtime"
	"testing"
)

// The README claims parallelism never changes a result. The implementation is
// built for it, fixed 4096-element blocks combined in block order, but until
// this test nothing ran the same reduction under different core counts and
// compared. A claim about concurrency that no test varies concurrency for is a
// claim about the code someone read, not the code that runs.
func TestReductionsAreIdenticalAtEveryCoreCount(t *testing.T) {
	sizes := []int{minParallel - 1, minParallel, sumChunk*3 + 7, 1 << 20}
	procs := []int{1, 2, 3, 16}

	original := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(original) })

	for _, n := range sizes {
		data := make([]float64, n)
		for i := range data {
			// Mixed magnitudes, so a changed summation order shows up as a
			// changed answer rather than being absorbed by the rounding.
			data[i] = math.Sin(float64(i)) * math.Pow(10, float64(i%17)-8)
		}

		var want float64
		for i, p := range procs {
			runtime.GOMAXPROCS(p)
			got := parallelSum(data)
			if i == 0 {
				want = got
				continue
			}
			if got != want {
				t.Errorf("n=%d: sum at GOMAXPROCS=%d is %v, at %d it is %v, difference %g",
					n, p, got, procs[0], want, got-want)
			}
		}
	}
}

// Sum is the exported path onto parallelSum, so the property has to hold there
// too or the claim is about an internal function nobody calls.
func TestSumIsIdenticalAtEveryCoreCount(t *testing.T) {
	original := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(original) })

	n := 1 << 20
	data := make([]float64, n)
	for i := range data {
		data[i] = math.Cos(float64(i)) * float64(i%1000)
	}
	a := New(data, []int{n})

	runtime.GOMAXPROCS(1)
	serial := Sum(a)
	for _, p := range []int{2, 4, 16} {
		runtime.GOMAXPROCS(p)
		if got := Sum(a); got.Data[0] != serial.Data[0] {
			t.Errorf("Sum at GOMAXPROCS=%d is %v, serial is %v", p, got.Data[0], serial.Data[0])
		}
	}
}

// The same claim, one machine at a time: a gradient accumulation must not keep
// the extra bits of a fused multiply-add.
//
// Go lets a compiler contract `x*y + z` into one fused operation, and arm64
// takes it where amd64 does not, so `g += d * cotangent` -- the shape every
// backward loop in this package is written in -- answered differently on Apple
// silicon than on x86. Nothing here varies the architecture; what it pins is
// that the accumulation agrees with the same arithmetic done in two rounded
// steps, which is what a machine without FMA does and what internal/ir's
// gradient transform builds out of separate nodes.
func TestGradientAccumulationRoundsEveryProduct(t *testing.T) {
	const n = 1024
	s0 := Leaf([]float64{1.7320508075688772}, nil) // a scalar broadcast over the vector
	xs := make([]float64, n)
	ws := make([]float64, n)
	for i := range xs {
		// Magnitudes chosen so an unrounded product survives into the sum, and a
		// cotangent that is not 1: multiplying by one rounds the same either way
		// and would make this test unable to fail.
		xs[i] = math.Sin(float64(i)*0.31+0.2) * (1 + float64(i%7)/3)
		ws[i] = math.Cos(float64(i)*0.17+0.9) * (1 + float64(i%5)/7)
	}
	x := New(append([]float64(nil), xs...), []int{n})
	w := New(append([]float64(nil), ws...), []int{n})

	prod, err := Mul(s0, x)
	if err != nil {
		t.Fatal(err)
	}
	weighted, err := Mul(prod, w)
	if err != nil {
		t.Fatal(err)
	}
	Sum(weighted).Backward()

	// What the loop should have computed: d/ds0 is the sum of x_i * w_i, each
	// product rounded to f64 before it is added, exactly as `float64(a*b)`
	// spells it. Summed here in the same order the accumulator walks.
	var want float64
	for i := range xs {
		want += float64(xs[i] * ws[i])
	}
	if got := s0.Grad[0]; math.Float64bits(got) != math.Float64bits(want) {
		t.Errorf("d/ds0 = %v, want %v (bits %#x vs %#x): a product reached the accumulator unrounded",
			got, want, math.Float64bits(got), math.Float64bits(want))
	}
}
