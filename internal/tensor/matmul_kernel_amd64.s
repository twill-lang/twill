//go:build amd64

#include "textflag.h"

// microKernelAvx2F64 computes the 4x8 f64 register tile:
//   c[r*8+col] = sum over p in [0,kc) of a[p*4+r] * b[p*8+col]
// a is a packed MR=4 column-major micropanel, b a packed NR=8 row-major
// micropanel, c the 4x8 row-major scratch (overwritten). A YMM register holds
// four f64, so the 8 columns are two YMM vectors and the 4x8 tile is eight YMM
// accumulators Y0..Y7 (two per row: cols 0..3 and cols 4..7). Each depth step
// loads the two b column vectors and broadcasts the four a values one at a time
// (VBROADCASTSD replicates one f64 across all four lanes), then eight FMAs fold
// one rank-1 update into the accumulators. VFMADD231PD dst := dst + s1*s2 with
// dst last in Go operand order.
//
// func microKernelAvx2F64(kc int, a, b, c *float64)
TEXT ·microKernelAvx2F64(SB), NOSPLIT, $0-32
	MOVQ kc+0(FP), AX
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), DI
	MOVQ c+24(FP), DX

	// Zero the eight accumulators.
	VXORPD Y0, Y0, Y0
	VXORPD Y1, Y1, Y1
	VXORPD Y2, Y2, Y2
	VXORPD Y3, Y3, Y3
	VXORPD Y4, Y4, Y4
	VXORPD Y5, Y5, Y5
	VXORPD Y6, Y6, Y6
	VXORPD Y7, Y7, Y7

	CMPQ AX, $0
	JEQ  storeF64

loopF64:
	VMOVUPD (DI), Y8    // b cols 0..3
	VMOVUPD 32(DI), Y9  // b cols 4..7
	ADDQ    $64, DI

	VBROADCASTSD (SI), Y10   // a row 0
	VFMADD231PD  Y8, Y10, Y0
	VFMADD231PD  Y9, Y10, Y1
	VBROADCASTSD 8(SI), Y11  // a row 1
	VFMADD231PD  Y8, Y11, Y2
	VFMADD231PD  Y9, Y11, Y3
	VBROADCASTSD 16(SI), Y12 // a row 2
	VFMADD231PD  Y8, Y12, Y4
	VFMADD231PD  Y9, Y12, Y5
	VBROADCASTSD 24(SI), Y13 // a row 3
	VFMADD231PD  Y8, Y13, Y6
	VFMADD231PD  Y9, Y13, Y7

	ADDQ $32, SI
	DECQ AX
	JNZ  loopF64

storeF64:
	VMOVUPD Y0, (DX)
	VMOVUPD Y1, 32(DX)
	VMOVUPD Y2, 64(DX)
	VMOVUPD Y3, 96(DX)
	VMOVUPD Y4, 128(DX)
	VMOVUPD Y5, 160(DX)
	VMOVUPD Y6, 192(DX)
	VMOVUPD Y7, 224(DX)
	VZEROUPPER
	RET

// microKernelAvx2F32 computes the 8x8 f32 register tile:
//   c[r*8+col] = sum over p in [0,kc) of a[p*8+r] * b[p*8+col]
// A YMM register holds eight f32, so the 8 columns are one YMM vector and the
// 8x8 tile is eight YMM accumulators Y0..Y7 (one per row). Each depth step loads
// the one b column vector and broadcasts the eight a values one at a time
// (VBROADCASTSS), then eight FMAs fold the rank-1 update.
//
// func microKernelAvx2F32(kc int, a, b, c *float32)
TEXT ·microKernelAvx2F32(SB), NOSPLIT, $0-32
	MOVQ kc+0(FP), AX
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), DI
	MOVQ c+24(FP), DX

	VXORPS Y0, Y0, Y0
	VXORPS Y1, Y1, Y1
	VXORPS Y2, Y2, Y2
	VXORPS Y3, Y3, Y3
	VXORPS Y4, Y4, Y4
	VXORPS Y5, Y5, Y5
	VXORPS Y6, Y6, Y6
	VXORPS Y7, Y7, Y7

	CMPQ AX, $0
	JEQ  storeF32

loopF32:
	VMOVUPS (DI), Y8 // b cols 0..7
	ADDQ    $32, DI

	VBROADCASTSS (SI), Y9    // a row 0
	VFMADD231PS  Y8, Y9, Y0
	VBROADCASTSS 4(SI), Y10  // a row 1
	VFMADD231PS  Y8, Y10, Y1
	VBROADCASTSS 8(SI), Y11  // a row 2
	VFMADD231PS  Y8, Y11, Y2
	VBROADCASTSS 12(SI), Y12 // a row 3
	VFMADD231PS  Y8, Y12, Y3
	VBROADCASTSS 16(SI), Y13 // a row 4
	VFMADD231PS  Y8, Y13, Y4
	VBROADCASTSS 20(SI), Y14 // a row 5
	VFMADD231PS  Y8, Y14, Y5
	VBROADCASTSS 24(SI), Y15 // a row 6
	VFMADD231PS  Y8, Y15, Y6
	VBROADCASTSS 28(SI), Y9  // a row 7
	VFMADD231PS  Y8, Y9, Y7

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
