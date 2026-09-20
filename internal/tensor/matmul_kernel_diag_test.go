package tensor

import (
	"fmt"
	"reflect"
	"runtime"
	"testing"
)

// TestKernelSelectionDiagnostic prints, to stdout so it is visible in the CI log
// without -v, which microkernel the dispatch table installed on the running CPU.
// This is how a CI run records whether the assembly path under test actually
// executed on the runner or was only compiled and vetted: the printed function
// name is the concrete kernel every packed-matmul test in this package exercised
// through the dispatch pointers. On amd64 see also TestAmd64KernelFeatureFlags,
// which prints the AVX2/AVX-512 feature flags behind this selection.
func TestKernelSelectionDiagnostic(t *testing.T) {
	f64 := runtime.FuncForPC(reflect.ValueOf(kernelF64).Pointer()).Name()
	f32 := runtime.FuncForPC(reflect.ValueOf(kernelF32).Pointer()).Name()
	fmt.Printf("matmul kernel selection: GOARCH=%s kernelF64=%s kernelF32=%s\n",
		runtime.GOARCH, f64, f32)
}
