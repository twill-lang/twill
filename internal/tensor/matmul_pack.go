package tensor

// Packing and cache-blocking for the register-blocked fast matmul. This is the
// GotoBLAS/BLIS loop nest: an outer sweep over NC-wide column panels of B and
// KC-deep depth panels, packing each B panel into contiguous NR-column
// micropanels; an inner sweep over MC-tall row panels of A, packing each into
// contiguous MR-row micropanels; then the two register loops that call the
// microkernel on one MR x NR tile at a time.
//
// The packed layouts are what let the microkernel read pure contiguous streams:
// packed A stores a[p*MR+r] (MR-row column-major micropanels) and packed B
// stores b[p*NR+col] (NR-column row-major micropanels). Both are zero-padded at
// the M, N and K edges, so the microkernel always runs a full MR x NR x kc tile
// and the only edge handling is a bounded copy of the valid sub-tile back into
// C. The output buffer starts zeroed, so the KC-panel loop accumulates into C
// with a plain +=.
//
// Rows of C are split across workers, each worker running the whole nest on its
// own disjoint row range with its own pack scratch, exactly as the existing mm
// kernels split rows. That is race-free and keeps the result independent of the
// worker count.

// packAF64 packs A[ic:ic+mc, pc:pc+kc] (from a row-major [m,k]) into dst in
// MR-row column-major micropanels, zero-padding rows past mc.
func packAF64(dst, a []float64, m, k, ic, mc, pc, kc int) {
	idx := 0
	for ri := 0; ri < mc; ri += mrF64 {
		rows := mrF64
		if ri+rows > mc {
			rows = mc - ri
		}
		for p := 0; p < kc; p++ {
			col := pc + p
			for r := 0; r < mrF64; r++ {
				if r < rows {
					dst[idx] = a[(ic+ri+r)*k+col]
				} else {
					dst[idx] = 0
				}
				idx++
			}
		}
	}
}

// packBNF64 packs B[pc:pc+kc, jc:jc+nc] from a row-major [k,n] operand into dst
// in NR-column row-major micropanels, zero-padding columns past nc.
func packBNF64(dst, b []float64, k, n, pc, kc, jc, nc int) {
	idx := 0
	for cj := 0; cj < nc; cj += nrF64 {
		cols := nrF64
		if cj+cols > nc {
			cols = nc - cj
		}
		for p := 0; p < kc; p++ {
			row := pc + p
			base := row*n + jc + cj
			for c := 0; c < nrF64; c++ {
				if c < cols {
					dst[idx] = b[base+c]
				} else {
					dst[idx] = 0
				}
				idx++
			}
		}
	}
}

// packBTF64 packs the same B panel when the operand is the NT weight w [n,k],
// where B = wᵀ so B[p,j] = w[j,p]. The transpose is absorbed into the pack.
func packBTF64(dst, w []float64, k, n, pc, kc, jc, nc int) {
	idx := 0
	for cj := 0; cj < nc; cj += nrF64 {
		cols := nrF64
		if cj+cols > nc {
			cols = nc - cj
		}
		for p := 0; p < kc; p++ {
			row := pc + p
			for c := 0; c < nrF64; c++ {
				if c < cols {
					dst[idx] = w[(jc+cj+c)*k+row]
				} else {
					dst[idx] = 0
				}
				idx++
			}
		}
	}
}

// packedMatmulF64 computes C = A @ B for the fast f64 path, [m,n], where the B
// operand is either row-major [k,n] (nt=false) or the NT weight [n,k] (nt=true,
// B = wᵀ). It requires packedMatmulProfitable(m,k,n); the caller gates on that.
func packedMatmulF64(a []float64, m, k int, b []float64, n int, nt bool) []float64 {
	c := make([]float64, m*n)
	workers := workersFor(m * k * n)
	runChunks(m, workers, func(rlo, rhi int) {
		apack := make([]float64, blockMC*blockKC)
		bpack := make([]float64, blockKC*blockNC)
		var scratch [mrF64 * nrF64]float64
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
					packBTF64(bpack, b, k, n, pc, kc, jc, nc)
				} else {
					packBNF64(bpack, b, k, n, pc, kc, jc, nc)
				}
				for ic := rlo; ic < rhi; ic += blockMC {
					mc := blockMC
					if ic+mc > rhi {
						mc = rhi - ic
					}
					packAF64(apack, a, m, k, ic, mc, pc, kc)
					kernelPanelF64(apack, bpack, c, n, ic, mc, jc, nc, kc, &scratch)
				}
			}
		}
	})
	return c
}

// kernelPanelF64 runs the two register loops over one packed A panel (mc x kc)
// and packed B panel (kc x nc), calling the microkernel per MR x NR tile and
// adding each result into C[ic+.., jc+..], masked to the valid rows and columns.
func kernelPanelF64(apack, bpack, c []float64, n, ic, mc, jc, nc, kc int, scratch *[mrF64 * nrF64]float64) {
	for cj := 0; cj < nc; cj += nrF64 {
		cols := nrF64
		if cj+cols > nc {
			cols = nc - cj
		}
		bpanel := bpack[(cj/nrF64)*kc*nrF64:]
		for ri := 0; ri < mc; ri += mrF64 {
			rows := mrF64
			if ri+rows > mc {
				rows = mc - ri
			}
			apanel := apack[(ri/mrF64)*kc*mrF64:]
			kernelF64(kc, &apanel[0], &bpanel[0], &scratch[0])
			for r := 0; r < rows; r++ {
				dst := c[(ic+ri+r)*n+jc+cj:]
				src := scratch[r*nrF64:]
				for col := 0; col < cols; col++ {
					dst[col] += src[col]
				}
			}
		}
	}
}
