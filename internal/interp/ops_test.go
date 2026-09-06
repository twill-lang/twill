package interp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
)

// These reuse the run/scalar helpers from interp_test.go (same test package).

func TestBroadcastingRowVector(t *testing.T) {
	// A row vector broadcasts across the rows of a matrix.
	src := "let m = [[1.0, 2.0], [3.0, 4.0]] + [10.0, 20.0]\nm[1][1]"
	if got := scalar(t, src); got != 24 {
		t.Errorf("got %v, want 24", got)
	}
}

func TestBroadcastingColumnVector(t *testing.T) {
	src := "let m = [[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]] * [[10.0], [100.0]]\nm[1][2]"
	if got := scalar(t, src); got != 600 {
		t.Errorf("got %v, want 600", got)
	}
}

func TestAxisReductions(t *testing.T) {
	if got := scalar(t, "sum([[1.0, 2.0], [3.0, 4.0]], 0)[1]"); got != 6 {
		t.Errorf("sum axis0 got %v", got)
	}
	if got := scalar(t, "mean([[1.0, 2.0], [3.0, 4.0]], 1)[0]"); got != 1.5 {
		t.Errorf("mean axis1 got %v", got)
	}
	if got := scalar(t, "max([[1.0, 9.0], [3.0, 4.0]], 0)[1]"); got != 9 {
		t.Errorf("max axis0 got %v", got)
	}
}

func TestSoftmaxSumsToOne(t *testing.T) {
	if got := scalar(t, "sum(softmax([1.0, 2.0, 3.0, 4.0], 0))"); got < 0.9999 || got > 1.0001 {
		t.Errorf("softmax sum got %v, want 1", got)
	}
}

func TestArgmax(t *testing.T) {
	if got := scalar(t, "argmax([3.0, 1.0, 9.0, 2.0], 0)"); got != 2 {
		t.Errorf("argmax got %v", got)
	}
}

func TestWhere(t *testing.T) {
	if got := scalar(t, "where([1.0, 0.0, 1.0], [7.0, 7.0, 7.0], [9.0, 9.0, 9.0])[1]"); got != 9 {
		t.Errorf("where got %v", got)
	}
}

func TestReshapeAndTranspose(t *testing.T) {
	if got := scalar(t, "reshape([1.0, 2.0, 3.0, 4.0], 2, 2)[1][0]"); got != 3 {
		t.Errorf("reshape got %v", got)
	}
	if got := scalar(t, "transpose([[1.0, 2.0], [3.0, 4.0]])[0][1]"); got != 3 {
		t.Errorf("transpose got %v", got)
	}
}

func TestConcat(t *testing.T) {
	src := "let a = [[1.0, 2.0]]\nlet b = [[3.0, 4.0]]\nconcat([a, b], 0)[1][0]"
	if got := scalar(t, src); got != 3 {
		t.Errorf("concat got %v", got)
	}
}

func TestFoldAndAppend(t *testing.T) {
	if got := scalar(t, "fold(fn(a, b) = a + b, 0.0, [1.0, 2.0, 3.0, 4.0])"); got != 10 {
		t.Errorf("fold got %v", got)
	}
	if got := scalar(t, "len(append([1.0, 2.0], 3.0))"); got != 3 {
		t.Errorf("append got %v", got)
	}
}

func TestGradThroughBroadcastAndSoftmax(t *testing.T) {
	// d/dx sum(softmax(x)) == 0 (softmax outputs always sum to 1).
	src := "grad(fn(x) = sum(softmax(x, 0)))([1.0, 2.0, 3.0])[0]"
	if got := scalar(t, src); got < -1e-9 || got > 1e-9 {
		t.Errorf("grad got %v, want ~0", got)
	}
}

// The rank-preserving flag leaves the reduced axis in at length 1 instead of
// dropping it, for every reduction that takes an axis. The shape is the whole
// point, so it is what is asserted; the values are asserted to be the ones the
// dropping form already produced, since the flag is a reshape and nothing else.
func TestReductionsKeepTheAxisWhenAsked(t *testing.T) {
	const m = "[[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]]"
	shapes := map[string][]float64{
		"shape(sum(" + m + ", 1))":             {2},
		"shape(sum(" + m + ", 1, true))":       {2, 1},
		"shape(sum(" + m + ", 1, 1))":          {2, 1},
		"shape(sum(" + m + ", 1, false))":      {2},
		"shape(mean(" + m + ", 0, true))":      {1, 3},
		"shape(max(" + m + ", -1, true))":      {2, 1},
		"shape(min(" + m + ", 1, true))":       {2, 1},
		"shape(prod(" + m + ", 1, true))":      {2, 1},
		"shape(median(" + m + ", 1, true))":    {2, 1},
		"shape(argmax(" + m + ", 1, true))":    {2, 1},
		"shape(argmin(" + m + ", 0, true))":    {1, 3},
		"shape(logsumexp(" + m + ", 1, true))": {2, 1},
	}
	for src, want := range shapes {
		for i, d := range want {
			if got := scalar(t, fmt.Sprintf("%s[%d]", src, i)); got != d {
				t.Errorf("%s dimension %d = %v, want %v", src, i, got, d)
			}
		}
		if got := scalar(t, "len("+src+")"); got != float64(len(want)) {
			t.Errorf("%s has %v dimensions, want %d", src, got, len(want))
		}
	}
	// The numbers are the dropping form's, moved into a longer shape.
	if got := scalar(t, "sum("+m+", 1, true)[1][0]"); got != 15 {
		t.Errorf("kept sum = %v, want 15", got)
	}
	if got := scalar(t, "argmax("+m+", 1, true)[0][0]"); got != 2 {
		t.Errorf("kept argmax = %v, want 2", got)
	}
	// What the flag is for: a [2, 1] broadcasts back against a [2, 3] and a [2]
	// does not, because alignment is from the right.
	if got := scalar(t, "sum("+m+" - sum("+m+", 1, true) / 3.0)"); got != 0 {
		t.Errorf("row-centred sum = %v, want 0", got)
	}
	// It is a reduction followed by a reshape, so the gradient still flows.
	if got := scalar(t, "sum(grad(fn(v) = sum(sum(v, 1, true)))("+m+"))"); got != 6 {
		t.Errorf("gradient of a kept sum = %v, want 6", got)
	}
	// flip and diff do not remove an axis, so a third argument is still the
	// arity error it always was.
	_, err := interp.New(func(string) {}).Run("print(flip(" + m + ", 1, true))")
	if err == nil || !strings.Contains(err.Error(), "flip expects (tensor[, axis])") {
		t.Errorf("flip with a third argument: got %v", err)
	}
}
