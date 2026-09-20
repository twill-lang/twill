//go:build amd64

#include "textflag.h"

// AVX-512 microkernels for the fast matmul, computing the SAME register tiles
// the arch-independent framework declares (4x8 f64, 8x8 f32), so they consume
// phase 1's packed panels and edge handling unchanged. They are gated at runtime
// on cpu.X86.HasAVX512F (see matmul_kernel_amd64.go); on a CPU without AVX-512
// the init installs the AVX2 kernels instead, and on one without AVX2/FMA the
// pure-Go reference stays. This assembly is always compiled and vetted, but it
// only executes on AVX-512 hardware.
//
// Tile choice against the shared tile constants: a ZMM register holds eight f64.
// The f64 tile's eight columns are therefore exactly one ZMM, so the 4x8 tile is
// four ZMM accumulators (one per row) and each depth step is one ZMM b load, four
// a broadcasts and four FMAs. AVX2 needed two YMM loads and eight FMAs per depth
// step for the same tile, so keeping the shared 4x8 tile still halves the f64
// vector op count on AVX-512 at double the width, with no framework change.
//
// The f32 tile is eight columns wide, narrower than a ZMM's sixteen f32. The
// packed B micropanel holds exactly eight f32 per depth step, so a full 512-bit
// column load would over-read the next micropanel (and past the buffer at the
// last step). The f32 kernel therefore loads the eight columns with a 256-bit
// VMOVUPS, which zeroes the upper half of the ZMM, and accumulates in ZMM: the
// arithmetic width is the same eight lanes AVX2 uses. Widening the f32 tile to
// sixteen columns would let AVX-512 fill a ZMM, but the tile constants drive the
// shared packing consumed by the NEON and AVX2 kernels, so that would be a
// framework change to speed up a path that cannot be benchmarked on any available
// hardware; it was rejected for the shared 8x8 tile. See docs/BENCHMARKS.md.

// microKernelAvx512F64 computes the 4x8 f64 register tile:
//   c[r*8+col] = sum over p in [0,kc) of a[p*4+r] * b[p*8+col]
// a is a packed MR=4 column-major micropanel, b a packed NR=8 row-major
// micropanel, c the 4x8 row-major scratch (overwritten). Z0..Z3 are the four row
// accumulators. VFMADD231PD dst := dst + s1*s2, dst last in Go operand order.
//
// func microKernelAvx512F64(kc int, a, b, c *float64)
TEXT ·microKernelAvx512F64(SB), NOSPLIT, $0-32
	MOVQ kc+0(FP), AX
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), DI
	MOVQ c+24(FP), DX

	VXORPD Z0, Z0, Z0
	VXORPD Z1, Z1, Z1
	VXORPD Z2, Z2, Z2
	VXORPD Z3, Z3, Z3

	CMPQ AX, $0
	JEQ  storeF64

loopF64:
	VMOVUPD (DI), Z4 // b cols 0..7 (one ZMM)
	ADDQ    $64, DI

	VBROADCASTSD (SI), Z5    // a row 0
	VFMADD231PD  Z4, Z5, Z0
	VBROADCASTSD 8(SI), Z6   // a row 1
	VFMADD231PD  Z4, Z6, Z1
	VBROADCASTSD 16(SI), Z7  // a row 2
	VFMADD231PD  Z4, Z7, Z2
	VBROADCASTSD 24(SI), Z8  // a row 3
	VFMADD231PD  Z4, Z8, Z3

	ADDQ $32, SI
	DECQ AX
	JNZ  loopF64

storeF64:
	VMOVUPD Z0, (DX)
	VMOVUPD Z1, 64(DX)
	VMOVUPD Z2, 128(DX)
	VMOVUPD Z3, 192(DX)
	VZEROUPPER
	RET

// microKernelAvx512F32 computes the 8x8 f32 register tile:
//   c[r*8+col] = sum over p in [0,kc) of a[p*8+r] * b[p*8+col]
// Z0..Z7 are the eight row accumulators. The eight b columns are loaded with a
// 256-bit VMOVUPS, which zeroes the upper half of the ZMM, so the 512-bit FMA
// folds the rank-1 update over eight live lanes (the upper eight contribute
// 0*a = 0). Only the low 256 bits of each accumulator are stored.
//
// func microKernelAvx512F32(kc int, a, b, c *float32)
TEXT ·microKernelAvx512F32(SB), NOSPLIT, $0-32
	MOVQ kc+0(FP), AX
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), DI
	MOVQ c+24(FP), DX

	VXORPS Z0, Z0, Z0
	VXORPS Z1, Z1, Z1
	VXORPS Z2, Z2, Z2
	VXORPS Z3, Z3, Z3
	VXORPS Z4, Z4, Z4
	VXORPS Z5, Z5, Z5
	VXORPS Z6, Z6, Z6
	VXORPS Z7, Z7, Z7

	CMPQ AX, $0
	JEQ  storeF32

loopF32:
	VMOVUPS (DI), Y8 // b cols 0..7, zeroes upper half of Z8
	ADDQ    $32, DI

	VBROADCASTSS (SI), Z9    // a row 0
	VFMADD231PS  Z8, Z9, Z0
	VBROADCASTSS 4(SI), Z10  // a row 1
	VFMADD231PS  Z8, Z10, Z1
	VBROADCASTSS 8(SI), Z11  // a row 2
	VFMADD231PS  Z8, Z11, Z2
	VBROADCASTSS 12(SI), Z12 // a row 3
	VFMADD231PS  Z8, Z12, Z3
	VBROADCASTSS 16(SI), Z13 // a row 4
	VFMADD231PS  Z8, Z13, Z4
	VBROADCASTSS 20(SI), Z14 // a row 5
	VFMADD231PS  Z8, Z14, Z5
	VBROADCASTSS 24(SI), Z15 // a row 6
	VFMADD231PS  Z8, Z15, Z6
	VBROADCASTSS 28(SI), Z16 // a row 7
	VFMADD231PS  Z8, Z16, Z7

	ADDQ $32, SI
	DECQ AX
	JNZ  loopF32

storeF32:
	VMOVUPS Y0, (DX)
	VMOVUPS Y1, 32(DX)
	VMOVUPS Y2, 64(DX)
	VMOVUPS Y3, 96(DX)
	VMOVUPS Y4, 128(DX)
	VMOVUPS Y5, 160(DX)
	VMOVUPS Y6, 192(DX)
	VMOVUPS Y7, 224(DX)
	VZEROUPPER
	RET
