package interp_test

import (
	"math"
	"testing"
)

// log1p and expm1 are the accurate-near-zero forms of log(1+x) and exp(x)-1,
// and they are ordinary differentiable elementwise ops: a gradient flows
// through them the way it does through log and exp.
//
// The point of having them at all is the cancellation the naive spellings
// suffer. At x = 1e-16 the sum 1+x rounds to exactly 1, so log(1+x) is 0 and
// the answer 1e-16 is lost completely; exp(x)-1 loses it the same way. That is
// what the first test asserts, and it asserts it structurally -- zero against
// non-zero -- rather than to a tolerance, because a bound tight enough to
// separate 1e-16 from 0 would be a bound on the last bit, and
// docs/CORRECTNESS.md section 4 says the last bit of a libm function is the
// architecture's and not twill's.

func TestLog1pAndExpm1KeepWhatTheNaiveFormsCancel(t *testing.T) {
	if got := scalar(t, "item(log(1.0 + 1e-16))"); got != 0 {
		t.Fatalf("log(1 + 1e-16) = %v, want the cancellation this test is about", got)
	}
	if got := scalar(t, "item(log1p(1e-16))"); got != 1e-16 {
		t.Errorf("log1p(1e-16) = %v, want 1e-16", got)
	}
	if got := scalar(t, "item(exp(1e-16) - 1.0)"); got != 0 {
		t.Fatalf("exp(1e-16) - 1 = %v, want the cancellation this test is about", got)
	}
	if got := scalar(t, "item(expm1(1e-16))"); got != 1e-16 {
		t.Errorf("expm1(1e-16) = %v, want 1e-16", got)
	}
}

// The values themselves, away from zero, against Go's math. A relative
// tolerance and not equality: both sides call the platform's libm and section 4
// of docs/CORRECTNESS.md records that its last bit moves between arm64 and
// amd64.
func TestLog1pAndExpm1Values(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"item(log1p(0.0))", 0},
		{"item(log1p(1.0))", math.Log1p(1)},
		{"item(log1p(7.5))", math.Log1p(7.5)},
		{"item(log1p(0.0 - 0.25))", math.Log1p(-0.25)},
		{"item(expm1(0.0))", 0},
		{"item(expm1(1.0))", math.Expm1(1)},
		{"item(expm1(0.0 - 2.0))", math.Expm1(-2)},
	}
	for _, c := range cases {
		got := scalar(t, c.src)
		if math.Abs(got-c.want) > 1e-12*(1+math.Abs(c.want)) {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// Elementwise over a tensor, and the shape is preserved.
func TestLog1pAndExpm1AreElementwise(t *testing.T) {
	out := run15(t, `
let x = [[0.0, 1.0], [3.0, 7.0]]
print(shape(log1p(x)))
print(shape(expm1(x)))
print(item(log1p(x)[1][1] - log1p(7.0)))
`)
	expectLines(t, out, "[2, 2]", "[2, 2]", "0")
}

// The gradient rules. d/dx log1p(x) is 1/(1+x) and d/dx expm1(x) is exp(x), and
// both are checked against the closed form at points where the closed form is
// well conditioned. The tolerance is loose for the reason above; it is still
// four orders tighter than the difference between these rules and the wrong
// ones (1/x for log1p, or exp(x)-1 for expm1), which is what the test has to
// separate.
func TestLog1pAndExpm1Gradients(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"grad(fn(x) = sum(log1p(x)))([0.0, 1.0, 3.0])[0]", 1.0},
		{"grad(fn(x) = sum(log1p(x)))([0.0, 1.0, 3.0])[1]", 0.5},
		{"grad(fn(x) = sum(log1p(x)))([0.0, 1.0, 3.0])[2]", 0.25},
		{"grad(fn(x) = sum(expm1(x)))([0.0, 1.0, 2.0])[0]", 1.0},
		{"grad(fn(x) = sum(expm1(x)))([0.0, 1.0, 2.0])[1]", math.Exp(1)},
		{"grad(fn(x) = sum(expm1(x)))([0.0, 1.0, 2.0])[2]", math.Exp(2)},
	}
	for _, c := range cases {
		got := scalar(t, c.src)
		if math.Abs(got-c.want) > 1e-9*(1+math.Abs(c.want)) {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// The second derivative, through hessian. log1p” is -1/(1+x)^2 and expm1” is
// exp(x), and the second-order rules are a separate table in the tensor package
// from the first-order ones, so a wrong one there is invisible to the test
// above.
func TestLog1pAndExpm1SecondDerivatives(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"hessian(fn(x) = sum(log1p(x)))([1.0, 3.0])[0][0]", -0.25},
		{"hessian(fn(x) = sum(log1p(x)))([1.0, 3.0])[1][1]", -1.0 / 16.0},
		{"hessian(fn(x) = sum(expm1(x)))([0.0, 1.0])[0][0]", 1.0},
		{"hessian(fn(x) = sum(expm1(x)))([0.0, 1.0])[1][1]", math.Exp(1)},
	}
	for _, c := range cases {
		got := scalar(t, c.src)
		if math.Abs(got-c.want) > 1e-9*(1+math.Abs(c.want)) {
			t.Errorf("%s = %v, want %v", c.src, got, c.want)
		}
	}
}

// The systems-mode scalars, beside f64_log and f64_exp.
func TestF64Log1pAndF64Expm1(t *testing.T) {
	out := runI64(t, `
print(f64_to_str(f64_log1p(0.0)))
print(f64_to_str(f64_expm1(0.0)))
print(f64_to_str(f64_log1p(1e-16)))
print(f64_to_str(f64_expm1(1e-16)))
print(f64_log(1.0 + 1e-16) == 0.0)
print(f64_exp(1e-16) - 1.0 == 0.0)
`)
	expectLines(t, out, "0", "0", "1e-16", "1e-16", "true", "true")
}
