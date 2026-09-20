package tensor

// The f32 packed path. Twill stores every tensor as []float64, so an f32 tensor
// holds the f64 widening of f32 values. This path is where f32 finally computes
// natively: the packing step converts each element to float32 as it copies it
// into the contiguous panels, the microkernel accumulates in float32 NEON
// registers, and the result is widened back to float64 on store. That is a true
// f32 compute path, not an f64 computation rounded afterward. It runs only on
// the fast path; the strict/default f32 contraction stays on mmAccNT.

func packAF32(dst []float32, a []float64, m, k, ic, mc, pc, kc int) {
	idx := 0
	for ri := 0; ri < mc; ri += mrF32 {
		rows := mrF32
		if ri+rows > mc {
			rows = mc - ri
		}
		for p := 0; p < kc; p++ {
			col := pc + p
			for r := 0; r < mrF32; r++ {
				if r < rows {
					dst[idx] = float32(a[(ic+ri+r)*k+col])
				} else {
					dst[idx] = 0
				}
				idx++
			}
		}
	}
}

func packBNF32(dst []float32, b []float64, k, n, pc, kc, jc, nc int) {
	idx := 0
	for cj := 0; cj < nc; cj += nrF32 {
		cols := nrF32
		if cj+cols > nc {
			cols = nc - cj
		}
		for p := 0; p < kc; p++ {
			base := (pc+p)*n + jc + cj
			for c := 0; c < nrF32; c++ {
				if c < cols {
					dst[idx] = float32(b[base+c])
				} else {
					dst[idx] = 0
				}
				idx++
			}
		}
	}
}

func packBTF32(dst []float32, w []float64, k, n, pc, kc, jc, nc int) {
	idx := 0
	for cj := 0; cj < nc; cj += nrF32 {
		cols := nrF32
		if cj+cols > nc {
			cols = nc - cj
		}
		for p := 0; p < kc; p++ {
			row := pc + p
			for c := 0; c < nrF32; c++ {
				if c < cols {
					dst[idx] = float32(w[(jc+cj+c)*k+row])
				} else {
					dst[idx] = 0
				}
				idx++
			}
		}
	}
}

// packedMatmulF32 computes C = A @ B in float32 and returns the f64-widened
// result, [m,n]. B is [k,n] (nt=false) or the NT weight [n,k] (nt=true, B = wᵀ).
func packedMatmulF32(a []float64, m, k int, b []float64, n int, nt bool) []float64 {
	c32 := make([]float32, m*n)
	workers := workersFor(m * k * n)
	runChunks(m, workers, func(rlo, rhi int) {
		apack := make([]float32, blockMC*blockKC)
		bpack := make([]float32, blockKC*blockNC)
		var scratch [mrF32 * nrF32]float32
		for jc := 0; jc < n; jc += blockNC {
			nc := blockNC
			if jc+nc > n {
				nc = n - jc
			}
			for pc := 0; pc < k; pc += blockKC {
				kc := blockKC
				if pc+kc > k {
					kc = k - pc
				}
				if nt {
					packBTF32(bpack, b, k, n, pc, kc, jc, nc)
				} else {
					packBNF32(bpack, b, k, n, pc, kc, jc, nc)
				}
				for ic := rlo; ic < rhi; ic += blockMC {
					mc := blockMC
					if ic+mc > rhi {
						mc = rhi - ic
					}
					packAF32(apack, a, m, k, ic, mc, pc, kc)
					kernelPanelF32(apack, bpack, c32, n, ic, mc, jc, nc, kc, &scratch)
				}
			}
		}
	})
	c := make([]float64, m*n)
	for i, v := range c32 {
		c[i] = float64(v)
	}
	return c
}

func kernelPanelF32(apack, bpack, c []float32, n, ic, mc, jc, nc, kc int, scratch *[mrF32 * nrF32]float32) {
	for cj := 0; cj < nc; cj += nrF32 {
		cols := nrF32
		if cj+cols > nc {
			cols = nc - cj
		}
		bpanel := bpack[(cj/nrF32)*kc*nrF32:]
		for ri := 0; ri < mc; ri += mrF32 {
			rows := mrF32
			if ri+rows > mc {
				rows = mc - ri
			}
			apanel := apack[(ri/mrF32)*kc*mrF32:]
			kernelF32(kc, &apanel[0], &bpanel[0], &scratch[0])
			for r := 0; r < rows; r++ {
				dst := c[(ic+ri+r)*n+jc+cj:]
				src := scratch[r*nrF32:]
				for col := 0; col < cols; col++ {
					dst[col] += src[col]
				}
			}
		}
	}
}
