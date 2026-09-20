package tensor

import "unsafe"

// This file is the architecture-independent half of the fast, register-blocked
// matmul: the microkernel dispatch table, the pure-Go reference microkernels
// that every non-arm64 build uses and that the tests use as the correctness
// oracle, and the BLIS-style packing and cache-blocking framework the kernels
// run inside.
//
// A microkernel computes one MR x NR tile of C = A_panel @ B_panel, reading a
// packed A micropanel (MR x kc, column-major micro-order) and a packed B
// micropanel (kc x NR, row-major micro-order) and writing the MR x NR result
// into a small contiguous scratch buffer (C := A@B, an overwrite, not a +=).
// The framework then adds that scratch tile into the real output. Writing to a
// full contiguous scratch means the assembly never has to mask a partial edge
// tile or thread the output row stride: edges are handled by zero-padding the
// packed panels and by a bounded copy in Go. That keeps the hand-written
// assembly small and total, and it is where correctness is easiest to defend.
//
// The dispatch is a pair of function-pointer variables, one per element type.
// The pure-Go references below are the defaults; a build-tagged init (see
// matmul_kernel_arm64.go) overwrites them with the assembly kernels on arches
// that have one. Phases 2 and 3 (amd64 AVX2, then AVX-512) extend this by
// adding their own build-tagged file that assigns these same pointers, gated on
// golang.org/x/sys/cpu feature detection; nothing else in the framework changes.

// Register-tile dimensions, one pair per element type. They are the shape of the
// C tile a single microkernel invocation computes and are fixed at build time so
// the packing writes panels the kernel can consume without a runtime shape.
//
// f64: 4 rows x 8 columns. A NEON vector holds 2 f64, so 8 columns are 4 vectors
// and the 4x8 tile needs 4*4 = 16 vector accumulators, leaving the other half of
// the 32-register file for the streamed A and B loads. f32: 8 rows x 8 columns.
// A NEON vector holds 4 f32, so 8 columns are 2 vectors and the 8x8 tile is again
// 16 vector accumulators. Both were the widest tile that still left enough
// registers to hide load latency in tuning on Apple arm64.
const (
	mrF64, nrF64 = 4, 8
	mrF32, nrF32 = 8, 8
)

// microKernelF64 computes c[0:mr*nr] = a[mr x kc] @ b[kc x nr] for the f64 tile.
// a is column-major micro-order (a[p*mr+r]), b is row-major micro-order
// (b[p*nr+col]), c is row-major (c[r*nr+col]). It overwrites c.
type microKernelF64 func(kc int, a, b, c *float64)

// microKernelF32 is the same contract for the f32 tile, over float32 buffers.
type microKernelF32 func(kc int, a, b, c *float32)

// The live kernels. Defaults are the pure-Go references; an arch init may swap
// in assembly. Read through these so the tests can force the reference.
var (
	kernelF64 microKernelF64 = refMicroKernelF64
	kernelF32 microKernelF32 = refMicroKernelF32
)

// refMicroKernelF64 is the reference f64 tile: a plain triple loop the Go
// compiler can be trusted to compile correctly. It is the oracle the assembly
// kernel is tested against and the kernel every non-arm64 build actually runs.
func refMicroKernelF64(kc int, a, b, c *float64) {
	as := unsafe.Slice(a, kc*mrF64)
	bs := unsafe.Slice(b, kc*nrF64)
	cs := unsafe.Slice(c, mrF64*nrF64)
	var acc [mrF64 * nrF64]float64
	for p := 0; p < kc; p++ {
		ap := as[p*mrF64 : p*mrF64+mrF64]
		bp := bs[p*nrF64 : p*nrF64+nrF64]
		for r := 0; r < mrF64; r++ {
			ar := ap[r]
			accr := acc[r*nrF64 : r*nrF64+nrF64]
			for col := 0; col < nrF64; col++ {
				accr[col] += ar * bp[col]
			}
		}
	}
	copy(cs, acc[:])
}

// refMicroKernelF32 is the reference f32 tile.
func refMicroKernelF32(kc int, a, b, c *float32) {
	as := unsafe.Slice(a, kc*mrF32)
	bs := unsafe.Slice(b, kc*nrF32)
	cs := unsafe.Slice(c, mrF32*nrF32)
	var acc [mrF32 * nrF32]float32
	for p := 0; p < kc; p++ {
		ap := as[p*mrF32 : p*mrF32+mrF32]
		bp := bs[p*nrF32 : p*nrF32+nrF32]
		for r := 0; r < mrF32; r++ {
			ar := ap[r]
			accr := acc[r*nrF32 : r*nrF32+nrF32]
			for col := 0; col < nrF32; col++ {
				accr[col] += ar * bp[col]
			}
		}
	}
	copy(cs, acc[:])
}

// Cache-blocking parameters. The three loops that wrap the microkernel keep a
// KC-deep panel of B resident in L2 (NC wide) and a KC-deep panel of A resident
// while it streams through, the GotoBLAS/BLIS structure. The values are modest
// and chosen so an A panel (MC x KC) and a B panel (KC x NC) each sit inside a
// typical L2, without being tuned per model; the win over the current kernel
// comes from register blocking, and these only keep it from going memory-bound.
const (
	blockKC = 256
	blockMC = 128
	blockNC = 256
)

// packedGateF64 reports whether a product is large enough that the packed kernel
// is worth its packing overhead. Small products (matmul_128 and below) pay more
// to pack than they save, so the caller keeps the current fast kernel there.
func packedMatmulProfitable(m, k, n int) bool {
	if m < mrF64 || n < nrF64 || k < 64 {
		return false
	}
	// 128^3 and below stays on the existing fast kernel.
	return int64(m)*int64(k)*int64(n) > 128*128*128
}
