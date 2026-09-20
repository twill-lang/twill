package tensor

import (
	"math"
	"math/rand"
	"testing"
)

// naiveMM is a plain triple-loop a@b ([m,k]@[k,n]) used as the correctness
// oracle for the packed kernels.
func naiveMM(a []float64, m, k int, b []float64, n int) []float64 {
	c := make([]float64, m*n)
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			var s float64
			for p := 0; p < k; p++ {
				s += a[i*k+p] * b[p*n+j]
			}
			c[i*n+j] = s
		}
	}
	return c
}

// naiveNT is a@wᵀ, w is [n,k].
func naiveNT(a []float64, m, k int, w []float64, n int) []float64 {
	c := make([]float64, m*n)
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			var s float64
			for p := 0; p < k; p++ {
				s += a[i*k+p] * w[j*k+p]
			}
			c[i*n+j] = s
		}
	}
	return c
}

func randSlice(n int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = r.Float64()*2 - 1
	}
	return out
}

// relTol reports whether got and want agree to a relative/absolute tolerance
// appropriate for a reassociated f64 dot product.
func relTol(got, want, tol float64) bool {
	d := math.Abs(got - want)
	return d <= tol*(1+math.Abs(want))
}

// shapeSet spans the awkward cases: non-multiples of the MR/NR register tile and
// of the KC block, odd K, a dimension of 1, and shapes past one KC panel.
var shapeSet = [][3]int{
	{1, 1, 1}, {1, 5, 1}, {4, 8, 8}, {5, 9, 7}, {8, 8, 8},
	{3, 65, 11}, {17, 130, 9}, {64, 64, 64}, {100, 100, 100},
	{129, 257, 130}, {200, 96, 200}, {1, 300, 300}, {300, 300, 1},
	{130, 260, 130}, {256, 256, 256},
}

func TestPackedMatmulF64MatchesReference(t *testing.T) {
	for _, s := range shapeSet {
		m, k, n := s[0], s[1], s[2]
		if !packedMatmulProfitable(m, k, n) {
			// still exercise the packer directly on small shapes for coverage
		}
		a := randSlice(m*k, int64(m*7+k*13+n*17))
		b := randSlice(k*n, int64(m*3+k*5+n*11))
		w := randSlice(n*k, int64(m*19+k*23+n*29))

		gotN := packedMatmulF64(a, m, k, b, n, false)
		wantN := naiveMM(a, m, k, b, n)
		for i := range wantN {
			if !relTol(gotN[i], wantN[i], 1e-9) {
				t.Fatalf("f64 N m=%d k=%d n=%d at %d: got %v want %v", m, k, n, i, gotN[i], wantN[i])
			}
		}
		gotT := packedMatmulF64(a, m, k, w, n, true)
		wantT := naiveNT(a, m, k, w, n)
		for i := range wantT {
			if !relTol(gotT[i], wantT[i], 1e-9) {
				t.Fatalf("f64 NT m=%d k=%d n=%d at %d: got %v want %v", m, k, n, i, gotT[i], wantT[i])
			}
		}
	}
}

func TestPackedMatmulF32MatchesReference(t *testing.T) {
	for _, s := range shapeSet {
		m, k, n := s[0], s[1], s[2]
		a := randSlice(m*k, int64(m*7+k*13+n*17))
		b := randSlice(k*n, int64(m*3+k*5+n*11))
		w := randSlice(n*k, int64(m*19+k*23+n*29))

		gotN := packedMatmulF32(a, m, k, b, n, false)
		wantN := naiveMM(a, m, k, b, n)
		for i := range wantN {
			// f32 accumulation over up to k terms; tolerance scales with k.
			if !relTol(gotN[i], wantN[i], 1e-4*float64(k)) {
				t.Fatalf("f32 N m=%d k=%d n=%d at %d: got %v want %v", m, k, n, i, gotN[i], wantN[i])
			}
		}
		gotT := packedMatmulF32(a, m, k, w, n, true)
		wantT := naiveNT(a, m, k, w, n)
		for i := range wantT {
			if !relTol(gotT[i], wantT[i], 1e-4*float64(k)) {
				t.Fatalf("f32 NT m=%d k=%d n=%d at %d: got %v want %v", m, k, n, i, gotT[i], wantT[i])
			}
		}
	}
}

// TestPackedMatmulFuzzShapes throws random shapes (including tile- and
// block-straddling ones) at both kernels against the naive oracle.
func TestPackedMatmulFuzzShapes(t *testing.T) {
	r := rand.New(rand.NewSource(20260920))
	for iter := 0; iter < 60; iter++ {
		m := 1 + r.Intn(140)
		k := 1 + r.Intn(300)
		n := 1 + r.Intn(140)
		a := randSlice(m*k, r.Int63())
		w := randSlice(n*k, r.Int63())
		want := naiveNT(a, m, k, w, n)

		got := packedMatmulF64(a, m, k, w, n, true)
		for i := range want {
			if !relTol(got[i], want[i], 1e-9) {
				t.Fatalf("fuzz f64 NT m=%d k=%d n=%d at %d: got %v want %v", m, k, n, i, got[i], want[i])
			}
		}
		got32 := packedMatmulF32(a, m, k, w, n, true)
		for i := range want {
			if !relTol(got32[i], want[i], 1e-4*float64(k)) {
				t.Fatalf("fuzz f32 NT m=%d k=%d n=%d at %d: got %v want %v", m, k, n, i, got32[i], want[i])
			}
		}
	}
}

// TestKernelReferenceMatchesAsm pins any assembly microkernel to the pure-Go
// reference tile, bit-close, over random packed panels. On a build with no
// assembly kernel this compares the reference to itself and still guards the
// contract (kc edge, overwrite semantics).
func TestKernelReferenceMatchesAsm(t *testing.T) {
	r := rand.New(rand.NewSource(4242))
	for _, kc := range []int{1, 2, 3, 7, 8, 15, 64, 255, 256} {
		a := randSlice(kc*mrF64, r.Int63())
		b := randSlice(kc*nrF64, r.Int63())
		var got, want [mrF64 * nrF64]float64
		kernelF64(kc, &a[0], &b[0], &got[0])
		refMicroKernelF64(kc, &a[0], &b[0], &want[0])
		for i := range want {
			if math.Float64bits(got[i]) != math.Float64bits(want[i]) && !relTol(got[i], want[i], 1e-12) {
				t.Fatalf("f64 kernel vs ref kc=%d at %d: got %v want %v", kc, i, got[i], want[i])
			}
		}

		a32 := make([]float32, kc*mrF32)
		b32 := make([]float32, kc*nrF32)
		for i := range a32 {
			a32[i] = float32(r.Float64()*2 - 1)
		}
		for i := range b32 {
			b32[i] = float32(r.Float64()*2 - 1)
		}
		var got32, want32 [mrF32 * nrF32]float32
		kernelF32(kc, &a32[0], &b32[0], &got32[0])
		refMicroKernelF32(kc, &a32[0], &b32[0], &want32[0])
		for i := range want32 {
			if !relTol(float64(got32[i]), float64(want32[i]), 1e-4*float64(kc)) {
				t.Fatalf("f32 kernel vs ref kc=%d at %d: got %v want %v", kc, i, got32[i], want32[i])
			}
		}
	}
}
