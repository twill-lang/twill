//go:build amd64

package tensor

import "golang.org/x/sys/cpu"

// amd64 AVX2 + FMA3 microkernels. Unlike arm64, where NEON is baseline, AVX2 and
// FMA are not present on every amd64 CPU, so init swaps in the assembly kernels
// only when both features are detected at runtime. When either is absent the
// pure-Go reference microkernels stay installed and the binary still runs (just
// without the vectorised tiles), which is why dispatch is a pointer swap rather
// than a build-tag call.
//
// The kernels compute the SAME register tiles as the arch-independent framework
// declares (4x8 f64, 8x8 f32), so they consume phase 1's packed panels and edge
// handling unchanged; nothing in matmul_pack*.go changes for amd64.
//
// Register pressure (16 YMM registers, each 4 f64 or 8 f32):
//   f64 4x8: the 8 columns are two YMM wide, so the 4x8 tile is 4*2 = 8 YMM
//   accumulators (Y0..Y7). Each depth step loads the two B column vectors (2 YMM)
//   and broadcasts the four A values one at a time (1 YMM), well inside 16.
//   f32 8x8: the 8 columns are one YMM wide, so the 8x8 tile is 8 YMM
//   accumulators (Y0..Y7). Each depth step loads one B vector (1 YMM) and
//   broadcasts the eight A values one at a time (1 YMM). Both leave half the
//   register file free to hide load and broadcast latency.

//go:noescape
func microKernelAvx2F64(kc int, a, b, c *float64)

//go:noescape
func microKernelAvx2F32(kc int, a, b, c *float32)

func init() {
	if cpu.X86.HasAVX2 && cpu.X86.HasFMA {
		kernelF64 = microKernelAvx2F64
		kernelF32 = microKernelAvx2F32
	}
}
