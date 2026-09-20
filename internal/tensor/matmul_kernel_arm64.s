//go:build arm64

#include "textflag.h"

// microKernelAsmF64 computes the 4x8 f64 register tile:
//   c[r*8+col] = sum over p in [0,kc) of a[p*4+r] * b[p*8+col]
// a is a packed MR=4 column-major micropanel, b a packed NR=8 row-major
// micropanel, c the 4x8 row-major scratch (overwritten). A NEON D2 vector holds
// two f64, so the 8 columns are four vectors and the 4x8 tile is sixteen vector
// accumulators V16..V31. Each depth step loads eight b values and broadcasts the
// four a values (VLD1R replicates one f64 into both lanes), then sixteen FMLAs
// fold one rank-1 update into the accumulators. The Go assembler has no
// by-element FMLA, so the broadcast is explicit.
//
// func microKernelAsmF64(kc int, a, b, c *float64)
TEXT ·microKernelAsmF64(SB), NOSPLIT, $0-32
	MOVD kc+0(FP), R0
	MOVD a+8(FP), R1
	MOVD b+16(FP), R2
	MOVD c+24(FP), R3

	// Zero the sixteen accumulators.
	VEOR V16.B16, V16.B16, V16.B16
	VEOR V17.B16, V17.B16, V17.B16
	VEOR V18.B16, V18.B16, V18.B16
	VEOR V19.B16, V19.B16, V19.B16
	VEOR V20.B16, V20.B16, V20.B16
	VEOR V21.B16, V21.B16, V21.B16
	VEOR V22.B16, V22.B16, V22.B16
	VEOR V23.B16, V23.B16, V23.B16
	VEOR V24.B16, V24.B16, V24.B16
	VEOR V25.B16, V25.B16, V25.B16
	VEOR V26.B16, V26.B16, V26.B16
	VEOR V27.B16, V27.B16, V27.B16
	VEOR V28.B16, V28.B16, V28.B16
	VEOR V29.B16, V29.B16, V29.B16
	VEOR V30.B16, V30.B16, V30.B16
	VEOR V31.B16, V31.B16, V31.B16

	CBZ R0, storeF64

loopF64:
	VLD1.P 64(R2), [V0.D2, V1.D2, V2.D2, V3.D2] // b[0:8]
	VLD1R.P 8(R1), [V4.D2]                       // a0
	VLD1R.P 8(R1), [V5.D2]                       // a1
	VLD1R.P 8(R1), [V6.D2]                       // a2
	VLD1R.P 8(R1), [V7.D2]                       // a3

	VFMLA V4.D2, V0.D2, V16.D2
	VFMLA V4.D2, V1.D2, V17.D2
	VFMLA V4.D2, V2.D2, V18.D2
	VFMLA V4.D2, V3.D2, V19.D2
	VFMLA V5.D2, V0.D2, V20.D2
	VFMLA V5.D2, V1.D2, V21.D2
	VFMLA V5.D2, V2.D2, V22.D2
	VFMLA V5.D2, V3.D2, V23.D2
	VFMLA V6.D2, V0.D2, V24.D2
	VFMLA V6.D2, V1.D2, V25.D2
	VFMLA V6.D2, V2.D2, V26.D2
	VFMLA V6.D2, V3.D2, V27.D2
	VFMLA V7.D2, V0.D2, V28.D2
	VFMLA V7.D2, V1.D2, V29.D2
	VFMLA V7.D2, V2.D2, V30.D2
	VFMLA V7.D2, V3.D2, V31.D2

	SUBS $1, R0, R0
	CBNZ R0, loopF64

storeF64:
	VST1.P [V16.D2, V17.D2, V18.D2, V19.D2], 64(R3)
	VST1.P [V20.D2, V21.D2, V22.D2, V23.D2], 64(R3)
	VST1.P [V24.D2, V25.D2, V26.D2, V27.D2], 64(R3)
	VST1 [V28.D2, V29.D2, V30.D2, V31.D2], (R3)
	RET

// microKernelAsmF32 computes the 8x8 f32 register tile:
//   c[r*8+col] = sum over p in [0,kc) of a[p*8+r] * b[p*8+col]
// A NEON S4 vector holds four f32, so eight columns are two vectors and the 8x8
// tile is sixteen accumulators V16..V31 (rows in consecutive pairs). Each depth
// step loads eight b values (two vectors) and broadcasts the eight a values, then
// sixteen FMLAs fold the rank-1 update.
//
// func microKernelAsmF32(kc int, a, b, c *float32)
TEXT ·microKernelAsmF32(SB), NOSPLIT, $0-32
	MOVD kc+0(FP), R0
	MOVD a+8(FP), R1
	MOVD b+16(FP), R2
	MOVD c+24(FP), R3

	VEOR V16.B16, V16.B16, V16.B16
	VEOR V17.B16, V17.B16, V17.B16
	VEOR V18.B16, V18.B16, V18.B16
	VEOR V19.B16, V19.B16, V19.B16
	VEOR V20.B16, V20.B16, V20.B16
	VEOR V21.B16, V21.B16, V21.B16
	VEOR V22.B16, V22.B16, V22.B16
	VEOR V23.B16, V23.B16, V23.B16
	VEOR V24.B16, V24.B16, V24.B16
	VEOR V25.B16, V25.B16, V25.B16
	VEOR V26.B16, V26.B16, V26.B16
	VEOR V27.B16, V27.B16, V27.B16
	VEOR V28.B16, V28.B16, V28.B16
	VEOR V29.B16, V29.B16, V29.B16
	VEOR V30.B16, V30.B16, V30.B16
	VEOR V31.B16, V31.B16, V31.B16

	CBZ R0, storeF32

loopF32:
	VLD1.P 32(R2), [V0.S4, V1.S4] // b[0:8]
	VLD1R.P 4(R1), [V2.S4]         // a0
	VLD1R.P 4(R1), [V3.S4]         // a1
	VLD1R.P 4(R1), [V4.S4]         // a2
	VLD1R.P 4(R1), [V5.S4]         // a3
	VLD1R.P 4(R1), [V6.S4]         // a4
	VLD1R.P 4(R1), [V7.S4]         // a5
	VLD1R.P 4(R1), [V8.S4]         // a6
	VLD1R.P 4(R1), [V9.S4]         // a7

	VFMLA V2.S4, V0.S4, V16.S4
	VFMLA V2.S4, V1.S4, V17.S4
	VFMLA V3.S4, V0.S4, V18.S4
	VFMLA V3.S4, V1.S4, V19.S4
	VFMLA V4.S4, V0.S4, V20.S4
	VFMLA V4.S4, V1.S4, V21.S4
	VFMLA V5.S4, V0.S4, V22.S4
	VFMLA V5.S4, V1.S4, V23.S4
	VFMLA V6.S4, V0.S4, V24.S4
	VFMLA V6.S4, V1.S4, V25.S4
	VFMLA V7.S4, V0.S4, V26.S4
	VFMLA V7.S4, V1.S4, V27.S4
	VFMLA V8.S4, V0.S4, V28.S4
	VFMLA V8.S4, V1.S4, V29.S4
	VFMLA V9.S4, V0.S4, V30.S4
	VFMLA V9.S4, V1.S4, V31.S4

	SUBS $1, R0, R0
	CBNZ R0, loopF32

storeF32:
	VST1.P [V16.S4, V17.S4], 32(R3)
	VST1.P [V18.S4, V19.S4], 32(R3)
	VST1.P [V20.S4, V21.S4], 32(R3)
	VST1.P [V22.S4, V23.S4], 32(R3)
	VST1.P [V24.S4, V25.S4], 32(R3)
	VST1.P [V26.S4, V27.S4], 32(R3)
	VST1.P [V28.S4, V29.S4], 32(R3)
	VST1 [V30.S4, V31.S4], (R3)
	RET
