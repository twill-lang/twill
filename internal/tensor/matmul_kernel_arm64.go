//go:build arm64

package tensor

// arm64 NEON microkernels. NEON is baseline on every arm64 CPU, so there is no
// feature flag to check: init unconditionally swaps the pure-Go references for
// the assembly kernels. Phase 2/3 arches (amd64 AVX2, AVX-512) add their own
// build-tagged file that assigns these same pointers, there gated on
// golang.org/x/sys/cpu detection, which is why dispatch is a pointer swap rather
// than a build-tag call.

//go:noescape
func microKernelAsmF64(kc int, a, b, c *float64)

//go:noescape
func microKernelAsmF32(kc int, a, b, c *float32)

func init() {
	kernelF64 = microKernelAsmF64
	kernelF32 = microKernelAsmF32
}
