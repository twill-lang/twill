package tensor

import (
	"math"
	"runtime"
	"testing"
)

// refDotStrict is the strict inner product spelled out by hand: four
// accumulators, every product rounded to f64 before it is added, combined as
// (s0+s1)+(s2+s3). It is the reference the strict kernel must match to the bit.
// Because every operation is an IEEE-754 f64 multiply or add with an explicit
// rounding between them, it evaluates to the same bits on every architecture, so
// pinning the kernel against it here also pins the kernel across arm64, amd64 and
// the pure-Go fallback: any path that let a fused multiply-add slip back in would
// keep a product's extra bits and diverge from this reference.
func refDotStrict(a, w []float64, k int) float64 {
	var s0, s1, s2, s3 float64
	p := 0
	for ; p+4 <= k; p += 4 {
		s0 += float64(a[p] * w[p])
		s1 += float64(a[p+1] * w[p+1])
		s2 += float64(a[p+2] * w[p+2])
		s3 += float64(a[p+3] * w[p+3])
	}
	s := (s0 + s1) + (s2 + s3)
	for ; p < k; p++ {
		s += float64(a[p] * w[p])
	}
	return s
}

// TestStrictMatMulIsBitIdentical pins the default (strict) matmul kernel to a
// hand-written, architecture-independent reference, bit-for-bit. This is the
// lock that the README's determinism claim now covers matmul: the strict kernel
// rounds every product before adding it, so the pure-Go reference, arm64 and
// amd64 all produce the same bits. Before this change arm64 fused the inner
// product into a multiply-add and answered a different low bit than amd64, and
// no byte-exact test caught it because none exercised a matmul.
func TestStrictMatMulIsBitIdentical(t *testing.T) {
	restore := FastMatmulEnabled()
	SetFastMatmul(false)
	t.Cleanup(func() { SetFastMatmul(restore) })

	for _, dims := range [][3]int{{1, 5, 3}, {4, 7, 6}, {8, 8, 8}, {3, 1, 9}, {5, 33, 4}, {2, 128, 2}} {
		m, k, n := dims[0], dims[1], dims[2]
		a := randData(m*k, 11)
		w := randData(n*k, 22) // [n,k], the NT weight layout

		// mmNT: a @ wᵀ, the fast dense path.
		gotNT := mmNT(a, m, k, w, n)
		for i := 0; i < m; i++ {
			for j := 0; j < n; j++ {
				want := refDotStrict(a[i*k:i*k+k], w[j*k:j*k+k], k)
				if got := gotNT[i*n+j]; math.Float64bits(got) != math.Float64bits(want) {
					t.Fatalf("mmNT m=%d k=%d n=%d [%d,%d]: %#x != %#x (%v vs %v)",
						m, k, n, i, j, math.Float64bits(got), math.Float64bits(want), got, want)
				}
			}
		}

		// mm: a @ b, b is [k,n] here.
		b := randData(k*n, 33)
		gotMM := mm(a, m, k, b, n)
		for i := 0; i < m; i++ {
			for j := 0; j < n; j++ {
				var want float64
				for p := 0; p < k; p++ {
					want += float64(a[i*k+p] * b[p*n+j])
				}
				if got := gotMM[i*n+j]; math.Float64bits(got) != math.Float64bits(want) {
					t.Fatalf("mm m=%d k=%d n=%d [%d,%d]: %#x != %#x",
						m, k, n, i, j, math.Float64bits(got), math.Float64bits(want))
				}
			}
		}
	}
}

// TestStrictMatMulIsCoreCountInvariant is the concurrency half of the claim: the
// strict kernel splits rows across cores, so its result must not move when the
// core count does. A large product forces the parallel path to engage.
func TestStrictMatMulIsCoreCountInvariant(t *testing.T) {
	restore := FastMatmulEnabled()
	SetFastMatmul(false)
	origProcs := runtime.GOMAXPROCS(0)
	t.Cleanup(func() {
		SetFastMatmul(restore)
		runtime.GOMAXPROCS(origProcs)
	})

	m, k, n := 96, 96, 96
	a := randData(m*k, 44)
	w := randData(n*k, 55)

	runtime.GOMAXPROCS(1)
	serialNT := mmNT(a, m, k, w, n)
	serialMM := mm(a, m, k, w, n)
	for _, p := range []int{2, 4, 16} {
		runtime.GOMAXPROCS(p)
		gotNT := mmNT(a, m, k, w, n)
		gotMM := mm(a, m, k, w, n)
		for i := range serialNT {
			if math.Float64bits(gotNT[i]) != math.Float64bits(serialNT[i]) {
				t.Fatalf("mmNT at GOMAXPROCS=%d differs at %d: %#x != %#x",
					p, i, math.Float64bits(gotNT[i]), math.Float64bits(serialNT[i]))
			}
			if math.Float64bits(gotMM[i]) != math.Float64bits(serialMM[i]) {
				t.Fatalf("mm at GOMAXPROCS=%d differs at %d", p, i)
			}
		}
	}
}

// TestFastMatMulSelectorChangesArithmetic proves the selector actually switches
// kernels: on inputs where a fused multiply-add rounds differently, the fast
// kernel must differ from the strict one, and it must still agree to tolerance.
// If this stops finding a difference the selector has become a no-op.
func TestFastMatMulSelectorChangesArithmetic(t *testing.T) {
	restore := FastMatmulEnabled()
	t.Cleanup(func() { SetFastMatmul(restore) })

	m, k, n := 8, 257, 8
	a := randData(m*k, 66)
	w := randData(n*k, 77)

	SetFastMatmul(false)
	strict := mmNT(a, m, k, w, n)
	SetFastMatmul(true)
	fast := mmNT(a, m, k, w, n)

	differed := false
	for i := range strict {
		if math.Float64bits(strict[i]) != math.Float64bits(fast[i]) {
			differed = true
		}
		if !closef(strict[i], fast[i]) {
			t.Fatalf("fast vs strict at %d out of tolerance: %v vs %v", i, fast[i], strict[i])
		}
	}
	if !differed {
		t.Fatal("fast and strict kernels produced bit-identical results everywhere; the selector or math.FMA is a no-op on this build")
	}
}
