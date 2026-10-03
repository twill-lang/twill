package interp_test

import "testing"

// A parameter with `= expr` is optional: a call that omits it gets the default,
// evaluated in the function's own scope, and a call that gives it overrides it.

func TestOptionalParameterTakesItsDefault(t *testing.T) {
	if got := scalar(t, "fn f(a, b = 10.0) = a + b\nf(1.0)"); got != 11 {
		t.Errorf("default omitted: got %v, want 11", got)
	}
	if got := scalar(t, "fn f(a, b = 10.0) = a + b\nf(1.0, 5.0)"); got != 6 {
		t.Errorf("default overridden positionally: got %v, want 6", got)
	}
}

// A named argument binds to the parameter it names, wherever the parameter sits.

func TestNamedArgumentBindsByName(t *testing.T) {
	if got := scalar(t, "fn f(a, b = 10.0) = a + b\nf(1.0, b: 5.0)"); got != 6 {
		t.Errorf("named override: got %v, want 6", got)
	}
	// Only the named optional is set; the one before it keeps its default.
	if got := scalar(t, "fn h(x = 1.0, y = 2.0) = x * 10.0 + y\nh(y: 5.0)"); got != 15 {
		t.Errorf("named skips an earlier default: got %v, want 15", got)
	}
}

// Named arguments may be written in any order after the positional ones.

func TestNamedArgumentsAnyOrder(t *testing.T) {
	src := "fn g(a, b, c = 100.0) = a + b * 10.0 + c\ng(1.0, c: 3.0, b: 2.0)"
	if got := scalar(t, src); got != 24 {
		t.Errorf("named out of order: got %v, want 24", got)
	}
}

// A default is evaluated at the call, in the function's definition scope, so it
// can read a name that scope binds.

func TestDefaultSeesDefinitionScope(t *testing.T) {
	src := "let base = 7.0\nfn f(a, b = base) = a + b\nf(1.0)"
	if got := scalar(t, src); got != 8 {
		t.Errorf("default from definition scope: got %v, want 8", got)
	}
}
