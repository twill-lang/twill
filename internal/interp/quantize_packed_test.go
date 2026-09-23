package interp_test

import (
	"math"
	"testing"

	"github.com/twill-lang/twill/internal/interp"
	"github.com/twill-lang/twill/internal/value"
)

// quantize_packed builds an int8 weight directly from packed codes and a per-row
// scale, without reconstructing the f64 matrix. The weight it produces must be
// the one the codes and scales encode, and it must run through linear like any
// other quantized weight. The codes are offset by 128, so 138 is +10 and 123 is
// -5; with row scales 1 and 2 the weight is [[10, -5, 3], [-2, 0, 14]], and
// linear against the two unit rows reads back its first two columns.
func TestQuantizePackedBuildsTheEncodedWeight(t *testing.T) {
	src := `
let buf = bytes_new()
bytes_push(buf, 138)
bytes_push(buf, 123)
bytes_push(buf, 131)
bytes_push(buf, 127)
bytes_push(buf, 128)
bytes_push(buf, 135)
let qt = quantize_packed(bytes_to_str(buf), tensor([1.0, 2.0]), 2, 3)
linear(tensor([[1.0, 0.0, 0.0], [0.0, 1.0, 0.0]]), qt)
`
	v, _ := run(t, src)
	got, ok := value.AsTensor(v)
	if !ok {
		t.Fatalf("expected a tensor, got %s", value.Format(v))
	}
	want := []float64{10, -2, -5, 0}
	if len(got.Data) != len(want) {
		t.Fatalf("shape mismatch: got %v", got.Shape)
	}
	for i := range want {
		if math.Abs(got.Data[i]-want[i]) > 1e-9 {
			t.Fatalf("element %d: got %v, want %v", i, got.Data[i], want[i])
		}
	}
}

// A mismatched code length or scale length is a clear error, not a silent bad
// weight, since a host feeds these from a file.
func TestQuantizePackedRejectsMismatch(t *testing.T) {
	ip := interp.New(func(string) {})
	_, err := ip.Run(`
let buf = bytes_new()
bytes_push(buf, 128)
bytes_push(buf, 128)
quantize_packed(bytes_to_str(buf), tensor([1.0]), 2, 3)
`)
	if err == nil {
		t.Fatal("expected an error for rows*cols not matching the code length")
	}
}
