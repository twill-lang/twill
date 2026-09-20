//go:build amd64

package tensor

import (
	"fmt"
	"testing"

	"golang.org/x/sys/cpu"
)

// TestAmd64KernelFeatureFlags prints the amd64 CPU feature flags the kernel
// selection reads, to stdout so a CI run records plainly whether AVX-512 was
// present on the runner. When HasAVX512F is true the AVX-512 kernels were
// installed and every packed-matmul test in this package ran them; when it is
// false those kernels were compiled and vetted but not executed on this CPU, and
// the AVX2 (or reference) path ran instead.
func TestAmd64KernelFeatureFlags(t *testing.T) {
	status := "NOT executed on this CPU (AVX-512 kernels compiled and vetted only)"
	if cpu.X86.HasAVX512F {
		status = "EXECUTED on this CPU (AVX-512 kernels installed and exercised by the matmul tests)"
	}
	fmt.Printf("amd64 cpu features: HasAVX2=%v HasFMA=%v HasAVX512F=%v -> AVX-512 %s\n",
		cpu.X86.HasAVX2, cpu.X86.HasFMA, cpu.X86.HasAVX512F, status)
}
