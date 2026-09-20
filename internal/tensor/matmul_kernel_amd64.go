//go:build amd64

package tensor

import "golang.org/x/sys/cpu"

// amd64 AVX2/FMA3 and AVX-512 microkernels. Unlike arm64, where NEON is
// baseline, these instruction sets are not present on every amd64 CPU, so init
// picks the best kernel the running CPU supports and swaps it in through the
// dispatch pointers. The selection is a fallback chain:
//
//	AVX-512F      -> microKernelAvx512F64 / microKernelAvx512F32
//	AVX2 + FMA    -> microKernelAvx2F64   / microKernelAvx2F32
//	neither       -> the pure-Go reference microkernels (left installed)
//
// When neither vectorised path is available the binary still runs, which is why
// dispatch is a pointer swap rather than a build-tag call.
//
// Every kernel computes the SAME register tiles as the arch-independent
// framework declares (4x8 f64, 8x8 f32), so they consume phase 1's packed panels
// and edge handling unchanged; nothing in matmul_pack*.go changes for amd64.
//
// Register pressure, AVX2 (16 YMM registers, each 4 f64 or 8 f32):
//   f64 4x8: the 8 columns are two YMM wide, so the 4x8 tile is 4*2 = 8 YMM
//   accumulators (Y0..Y7). Each depth step loads the two B column vectors (2 YMM)
//   and broadcasts the four A values one at a time (1 YMM), well inside 16.
//   f32 8x8: the 8 columns are one YMM wide, so the 8x8 tile is 8 YMM
//   accumulators (Y0..Y7). Each depth step loads one B vector (1 YMM) and
//   broadcasts the eight A values one at a time (1 YMM). Both leave half the
//   register file free to hide load and broadcast latency.
//
// Register pressure, AVX-512 (32 ZMM registers, each 8 f64 or 16 f32): the f64
// tile's 8 columns are exactly one ZMM, so the 4x8 tile is four ZMM accumulators
// and each depth step is one ZMM load, four broadcasts and four FMAs, half the
// f64 vector op count of AVX2 at double the width. The f32 tile is only 8 columns
// wide, narrower than a ZMM, so its AVX-512 kernel accumulates in ZMM but loads
// the 8 columns at 256 bits: same arithmetic width as AVX2 for f32. See the
// header of matmul_kernel_avx512_amd64.s for why the shared 8x8 tile was kept
// rather than widening f32 to 16 (which would change the shared packing).

//go:noescape
func microKernelAvx2F64(kc int, a, b, c *float64)

//go:noescape
func microKernelAvx2F32(kc int, a, b, c *float32)

//go:noescape
func microKernelAvx512F64(kc int, a, b, c *float64)

//go:noescape
func microKernelAvx512F32(kc int, a, b, c *float32)

func init() {
	switch {
	case cpu.X86.HasAVX512F:
		kernelF64 = microKernelAvx512F64
		kernelF32 = microKernelAvx512F32
	case cpu.X86.HasAVX2 && cpu.X86.HasFMA:
		kernelF64 = microKernelAvx2F64
		kernelF32 = microKernelAvx2F32
	}
}
