package checker_test

import "testing"

// A tuple return type is a type: what a call to the function produces, and what
// a destructuring of that call binds.
func TestTupleReturnTypeFlowsToTheBinding(t *testing.T) {
	src := "mode systems\nfn f() -> (I64, Str) { return (1, \"a\") }\nlet (n, s) = f()\nlet bad: Bool = s\n"
	wantOne(t, src, `"bad" is declared Bool but the value is Str`)
	good := "mode systems\nfn f() -> (I64, Str) { return (1, \"a\") }\nlet (n, s) = f()\nlet ok: Str = s\n"
	wantNone(t, good)
}

func TestTupleBodyIsCheckedAgainstTheSignature(t *testing.T) {
	wantOne(t, "mode systems\nfn f() -> (F64, F64) = (1.0, 2.0, 3.0)\n",
		"returns (F64, F64, F64) but its signature declares (F64, F64)")
	wantOne(t, "mode systems\nfn f() -> (I64, Str) = (1, 2)\n",
		"but its signature declares (I64, Str)")
	wantNone(t, "mode systems\nfn f() -> (I64, Str) = (1, \"a\")\n")
}

func TestDestructuringArityIsChecked(t *testing.T) {
	wantOne(t, "mode systems\nfn f() -> (F64, F64) = (1.0, 2.0)\nlet (a, b, c) = f()\n",
		"this let binds 3 names, but the value is (F64, F64), which has 2")
	wantNone(t, "mode systems\nfn f() -> (F64, F64) = (1.0, 2.0)\nlet (a, b) = f()\n")
}

func TestDestructuringANonTupleIsReported(t *testing.T) {
	wantOne(t, "mode systems\nlet (a, b) = 5\n",
		"this let destructures a tuple of 2 values, but the value is F64")
	// A value the checker cannot resolve says nothing about its arity, so it
	// binds unknowns and stays quiet -- the same policy every other rule here
	// follows.
	wantNone(t, "import \"std/nn\" as nn\nlet (a, b) = nn.whatever()\n")
}

// A tuple inside a generic still judges: substParams has to descend into it, or
// the field's type stays a pair of unsubstituted parameters and Box[I64] and
// Box[Str] become the same type.
func TestTupleInsideAGenericSubstitutes(t *testing.T) {
	src := "mode systems\nstruct Pair[T] { span: (T, T) }\nlet p: Pair[I64] = Pair { span: (\"a\", \"b\") }\n"
	wantOne(t, src, `"p" is declared Pair[I64] but the value is Pair[Str]`)
	wantNone(t, "mode systems\nstruct Pair[T] { span: (T, T) }\nlet p: Pair[I64] = Pair { span: (1, 2) }\n")
}

func TestTupleIsNotANumberAndNotCallable(t *testing.T) {
	wantOne(t, "mode systems\nlet t: (I64, I64) = (1, 2)\nlet u = t + 1\n", "numbers/tensors")
	wantOne(t, "let t = (1.0, 2.0)\nlet u = t(3.0)\n", "not callable")
	wantOne(t, "mode systems\nlet t: (I64, I64) = (1, 2)\nlet b = t < 1\n", "cannot order (I64, I64)")
}

// There is deliberately no `.0`: a tuple has no named parts, so reading one is
// the same mistake as reading a field off a string.
func TestTupleHasNoFields(t *testing.T) {
	wantOne(t, "let t = (1.0, 2.0)\nlet y = t.first\n", "cannot read field")
}

func TestTupleAnnotationOnABinding(t *testing.T) {
	wantNone(t, "mode systems\nlet t: (I64, Str) = (1, \"a\")\n")
	wantOne(t, "mode systems\nlet t: (I64, Str) = (1, 2)\n",
		`"t" is declared (I64, Str) but the value is (F64, F64)`)
	wantOne(t, "mode systems\nlet t: (I64, Str) = (1, \"a\", true)\n",
		`"t" is declared (I64, Str) but the value is (F64, Str, Bool)`)
}

// A type argument is a full type on both sides now. `Arr[fn(I64) -> I64]` was a
// syntax error under the Go parser and clean under src/parse.tw, because
// parseTypeArgs read a type *reference* where parse_type_args read a type
// *expression* -- a divergence in the two parsers that predates tuples and that
// putting tuples into type-argument position is what found. Both read a type
// expression now, so a function type and a tuple may both nest there.
func TestATypeArgumentIsAFullType(t *testing.T) {
	wantNone(t, "mode systems\nlet f: Arr[fn(I64) -> I64] = arr_new()\n")
	wantNone(t, "mode systems\nlet xs: Arr[(I64, Str)] = arr_new()\n")
	wantNone(t, "mode systems\nlet d: Dict[Str, (I64, I64)] = {}\n")
}
