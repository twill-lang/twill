package value

import (
	"testing"

	"github.com/twill-lang/twill/internal/tensor"
)

// Scopes hold their first few bindings inline and spill to a map after that.
// These pin the behaviour that has to hold across the boundary.
func TestEnvInlineAndSpill(t *testing.T) {
	e := NewEnv(nil)
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	for i, n := range names {
		e.Define(n, Unit{})
		_ = i
	}
	for _, n := range names {
		if _, ok := e.Get(n); !ok {
			t.Errorf("%q was lost across the inline/map boundary", n)
		}
	}
}

func TestEnvRedefinitionLandsWhereTheNameAlreadyIs(t *testing.T) {
	// If a redefined name were appended instead of overwritten, Get would find
	// the stale copy first and the new value would never be seen.
	e := NewEnv(nil)
	e.Define("x", Unit{})
	first := TheUnit
	e.Define("x", first)
	if v, _ := e.Get("x"); v != Value(first) {
		t.Error("a redefinition did not replace the earlier binding")
	}
}

func TestEnvShadowsRatherThanOverwritingTheParent(t *testing.T) {
	parent := NewEnv(nil)
	parent.Define("x", Unit{})
	child := NewEnv(parent)
	child.Define("x", TheUnit)
	if _, ok := parent.Get("x"); !ok {
		t.Error("defining in a child removed the parent's binding")
	}
}

func TestEnvAssignReachesInlineAndMapBindings(t *testing.T) {
	e := NewEnv(nil)
	for _, n := range []string{"a", "b", "c", "d", "spilled"} {
		e.Define(n, Unit{})
	}
	for _, n := range []string{"a", "spilled"} {
		if !e.Assign(n, TheUnit) {
			t.Errorf("Assign could not reach %q", n)
		}
	}
	if e.Assign("missing", TheUnit) {
		t.Error("Assign invented a binding that was never defined")
	}
}

// A narrow-dtype tensor prints its elements at the dtype's shortest decimal and
// carries a dtype= tag; an f64 tensor is unchanged, so the goldens hold. Values
// are positive because the self-hosted reference these strings mirror has a
// NEEDS-2 sign bug on casts, but the Go rounding and rendering are correct.
func TestFormatNarrowTensor(t *testing.T) {
	bf := tensor.Cast(tensor.New([]float64{0.1, 3.14159, 1}, []int{3}), tensor.DTBF16)
	if got := Format(bf); got != "tensor([0.1, 3.14, 1], shape=[3], dtype=bf16)" {
		t.Errorf("bf16 tensor = %q", got)
	}
	i8 := tensor.Cast(tensor.New([]float64{1, 2, 3}, []int{3}), tensor.DTI8)
	if got := Format(i8); got != "tensor([1, 2, 3], shape=[3], dtype=i8)" {
		t.Errorf("i8 tensor = %q", got)
	}
	// f64 is untouched: no tag, the usual FormatNumber rendering.
	f64 := tensor.New([]float64{0.5, 1}, []int{2})
	if got := Format(f64); got != "tensor([0.5, 1], shape=[2])" {
		t.Errorf("f64 tensor changed: %q", got)
	}
	// A narrow scalar prints bare, like a scalar of any dtype.
	sc := tensor.Cast(tensor.Scalar(1.5), tensor.DTF16)
	if got := Format(sc); got != "1.5" {
		t.Errorf("narrow scalar = %q", got)
	}
}

// A float too large for an int64 must print as itself.
//
// FormatNumber used to ask `n == float64(int64(n))`, and converting an
// out-of-range float to an integer is undefined in Go: arm64 saturates to
// MaxInt64, whose float64 is the number we started with, so the guard passed
// and 2^63 printed as 9223372036854775807 -- a value no program ever held, and
// off by one from the one it did. amd64 yields MinInt64 there, the guard
// failed, and the same program printed correctly. The bug was one architecture
// only, and the test that caught it was reading as a rounding curiosity.
func TestFormatNumberDoesNotSaturateOutsideTheInt64Range(t *testing.T) {
	cases := []struct {
		n    float64
		want string
	}{
		{9223372036854775808.0, "9223372036854775808"},   // 2^63, one past MaxInt64
		{-9223372036854775808.0, "-9223372036854775808"}, // MinInt64 exactly, and in range
		{18446744073709551616.0, "18446744073709551616"}, // 2^64
		{-18446744073709551616.0, "-18446744073709551616"},
		{9007199254740993.0, "9007199254740992"}, // still rounds where f64 must
		{0, "0"},
		{-1.5, "-1.5"},
	}
	for _, c := range cases {
		if got := FormatNumber(c.n); got != c.want {
			t.Errorf("FormatNumber(%v) = %q, want %q", c.n, got, c.want)
		}
	}
}
