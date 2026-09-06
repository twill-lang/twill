package checker_test

import (
	"strings"
	"testing"

	"github.com/twill-lang/twill/internal/checker"
	"github.com/twill-lang/twill/internal/parser"
)

func diagnostics(t *testing.T, src string) []checker.Diagnostic {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return checker.Check(prog)
}

func wantOne(t *testing.T, src, substr string) {
	t.Helper()
	diags := diagnostics(t, src)
	if len(diags) == 0 {
		t.Fatalf("expected a diagnostic containing %q, got none", substr)
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Msg, substr) {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics %v did not contain %q", diags, substr)
	}
}

func wantNone(t *testing.T, src string) {
	t.Helper()
	if diags := diagnostics(t, src); len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", diags)
	}
}

func TestCatchesMatmulMismatch(t *testing.T) {
	wantOne(t, "let A = [[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]]\nlet x = [1.0, 2.0]\nlet y = A @ x", "inner")
}

func TestCatchesElementwiseMismatch(t *testing.T) {
	wantOne(t, "let z = [1.0, 2.0] + [1.0, 2.0, 3.0]", "shape mismatch")
}

func TestCatchesRaggedLiteral(t *testing.T) {
	wantOne(t, "let m = [[1.0, 2.0], [3.0]]", "ragged")
}

func TestCatchesAnnotationMismatch(t *testing.T) {
	// f expects a length-3 vector but gets a length-2 one.
	wantOne(t, "fn f(v: [3]) = sum(v)\nlet r = f([1.0, 2.0])", "expects")
}

func TestGoodProgramHasNoDiagnostics(t *testing.T) {
	wantNone(t, `
		let A = [[1.0, 2.0], [3.0, 4.0]]
		let x = [1.0, 1.0]
		let y = A @ x + [0.5, 0.5]
		let s = mean(y * y)`)
}

func TestDynamicCodeNoFalsePositive(t *testing.T) {
	// Shapes here flow through grad/loops and cannot be fully known; the
	// checker must stay quiet rather than guess.
	wantNone(t, `
		let w = [0.0, 0.0]
		fn loss(w) = mean(w * w)
		for step in range(10) {
			let g = grad(loss)(w)
			w = w - g * 0.1
		}`)
}

// tensor(...) used to return an unknown type, which meant the shape of every
// literal was thrown away at the door and the checker had nothing to check.
func TestTensorLiteralKeepsItsShape(t *testing.T) {
	wantOne(t, `let a = tensor([[1.0, 2.0], [3.0, 4.0]])
let b = tensor([[1.0, 2.0, 3.0]])
print(a @ b)`, "inner 2 != 1")
}

func TestATensorLiteralThatFitsIsNotReported(t *testing.T) {
	// The half that decides whether anybody leaves the checker on.
	src := `let a = tensor([[1.0, 2.0], [3.0, 4.0]])
let b = tensor([[1.0], [2.0]])
print(a @ b)`
	if diags := diagnostics(t, src); len(diags) != 0 {
		t.Fatalf("a valid multiply was reported: %v", diags)
	}
}

func TestAFlatLiteralIsOneDimensional(t *testing.T) {
	wantOne(t, `let v = tensor([1.0, 2.0, 3.0])
let m = zeros(2, 2)
print(m @ v)`, "inner 2 != 3")
}

func TestARaggedLiteralIsLeftToTheRuntime(t *testing.T) {
	// It is already an error, and inventing a shape for it here would report a
	// second, imaginary problem somewhere downstream instead of the real one.
	for _, d := range diagnostics(t, `let a = tensor([[1.0, 2.0], [3.0]])
print(a @ a)`) {
		if strings.Contains(d.Msg, "inner") {
			t.Errorf("a ragged literal produced an invented shape error: %v", d.Msg)
		}
	}
}

func TestReshapeThatChangesTheElementCount(t *testing.T) {
	// The second most common shape mistake after a bad matmul, and it used to
	// reach the runtime untouched.
	wantOne(t, "let x = zeros(2, 3)\nprint(reshape(x, 4, 2))", "changes the number of elements")
}

func TestReshapeThatFitsIsQuiet(t *testing.T) {
	if diags := diagnostics(t, "let x = zeros(2, 3)\nprint(reshape(x, 3, 2))"); len(diags) != 0 {
		t.Fatalf("a valid reshape was reported: %v", diags)
	}
}

func TestAnAxisThatDoesNotExistIsReported(t *testing.T) {
	// Both reduction paths already worked this out and then returned an unknown
	// type, which silenced everything downstream too.
	wantOne(t, "let x = zeros(2, 3)\nprint(sum(x, 7))", "axis out of range")
	wantOne(t, "let x = zeros(2, 3)\nprint(argmax(x, 5))", "axis out of range")
	// transpose used to be the one axis-taking builtin that stayed silent on an
	// out-of-range axis (NEEDS-50), so a permutation naming a nonexistent axis
	// passed the check and failed only at run time.
	wantOne(t, "let x = zeros(2, 3)\nprint(transpose(x, 0, 5))", "axis out of range")
	// softmax normalises over an axis and the runtime rejects an out-of-range
	// one, but the checker preserved shape and ignored the axis argument.
	wantOne(t, "let x = zeros(2, 3)\nprint(softmax(x, 5))", "axis out of range")
}

func TestSoftmaxWithAValidAxisIsClean(t *testing.T) {
	wantNone(t, "let x = zeros(2, 3)\nlet y = softmax(x, 1)")
	wantNone(t, "let x = zeros(2, 3)\nlet y = softmax(x)")
	wantNone(t, "let x = zeros(2, 3)\nlet y = softmax(x, -1)")
}

func TestShapePreservingAxisOpsCheckTheirAxis(t *testing.T) {
	// flip, cumsum and sort take the axis in the second argument; roll puts the
	// shift first, so its axis is the third. Each is a runtime error out of range.
	wantOne(t, "let y = flip(zeros(2, 3), 5)", "axis out of range")
	wantOne(t, "let y = cumsum(zeros(2, 3), 5)", "axis out of range")
	wantOne(t, "let y = sort(zeros(2, 3), 5)", "axis out of range")
	wantOne(t, "let y = roll(zeros(2, 3), 1, 5)", "axis out of range")
}

func TestShapePreservingAxisOpsAcceptAValidAxis(t *testing.T) {
	wantNone(t, "let a = flip(zeros(2, 3), 1)\nlet b = roll(zeros(2, 3), 1, 0)\nlet c = cumsum(zeros(2, 3))\nlet d = sort(zeros(2, 3), 1)")
	// A negative axis counts from the end and is fine.
	wantNone(t, "let a = flip(zeros(2, 3), -1)")
}

func TestConv2dEnforcesItsShapeContracts(t *testing.T) {
	// The three mistakes the runtime rejects and the checker used to miss: a
	// non-rank-3 input, a non-rank-4 weight, and a channel-count mismatch.
	wantOne(t, "let y = conv2d(zeros(3), zeros(2, 3, 3, 3))", "input must be [channels, height, width]")
	wantOne(t, "let y = conv2d(zeros(3, 8, 8), zeros(4, 3, 3))", "weight must be [out, in, kh, kw]")
	wantOne(t, "let y = conv2d(zeros(3, 8, 8), zeros(4, 5, 3, 3))", "input has 3 channels but weight expects 5")
	// maxpool2d shares the [channels, height, width] input contract.
	wantOne(t, "let y = maxpool2d(zeros(8, 8), 2)", "input must be [channels, height, width]")
}

func TestAValidConv2dIsClean(t *testing.T) {
	// [3,8,8] input, [4,3,3,3] weight -> [4,6,6]; feeding the output on works.
	wantNone(t, "let y = conv2d(zeros(3, 8, 8), zeros(4, 3, 3, 3))\nlet z = maxpool2d(y, 2)")
}

func TestBroadcastToChecksCompatibility(t *testing.T) {
	// A source axis that is neither equal to the target's nor 1 cannot broadcast,
	// and broadcasting to fewer axes than the source has is its own message.
	wantOne(t, "let y = broadcast_to(zeros(4), list(2, 3))", "cannot broadcast [4] to [2, 3]")
	wantOne(t, "let y = broadcast_to(zeros(2, 3), list(3))", "fewer axes in target")
	// The separate-argument form is read too.
	wantOne(t, "let y = broadcast_to(zeros(4), 2, 3)", "cannot broadcast")
}

func TestValidBroadcastsAreClean(t *testing.T) {
	wantNone(t, "let y = broadcast_to(zeros(3), list(2, 3))")
	wantNone(t, "let y = broadcast_to(zeros(3, 1), list(3, 4))")
	wantNone(t, "let y = broadcast_to(zeros(1, 3), list(2, 3))")
}

func TestListShapeArgumentIsRead(t *testing.T) {
	// The idiomatic list(...) shape argument is understood by every shape-taking
	// builtin, so reshape's element-count check fires for it and a zeros(list...)
	// carries a known shape downstream.
	wantOne(t, "let y = reshape(zeros(6), list(2, 4))", "reshape changes the number of elements")
	wantOne(t, "let a = zeros(list(2, 3))\nlet b = a @ zeros(4, 2)", "inner 3 != 4")
	wantNone(t, "let y = reshape(zeros(6), list(2, 3))")
}

func TestAValidTransposePermutationIsClean(t *testing.T) {
	wantNone(t, "let x = zeros(2, 3)\nlet y = transpose(x, 1, 0)")
}

func TestMatmulRejectsRankThreeOperands(t *testing.T) {
	// `@` has no batched form: a rank-3 operand is a certain runtime error, and
	// the rank is known even when the sizes are not, so the checker catches it.
	wantOne(t, "let a = zeros(5, 2, 3)\nlet b = zeros(5, 3, 4)\nlet c = a @ b", "1-D or 2-D operands")
	wantOne(t, "let a = zeros(2, 3, 4)\nlet x = [1.0, 2.0, 3.0, 4.0]\nlet c = a @ x", "1-D or 2-D operands")
}

func TestMatmulAcceptsVectorsAndMatrices(t *testing.T) {
	// The 1-D and 2-D forms that `@` does support stay clean.
	wantNone(t, "let c = zeros(2, 3) @ zeros(3, 4)")
	wantNone(t, "let c = zeros(2, 3) @ [1.0, 2.0, 3.0]")
	wantNone(t, "let c = [1.0, 2.0] @ zeros(2, 3)")
}

func TestMatmulRejectsAScalarOperand(t *testing.T) {
	// A rank-0 operand is as invalid as a rank-3 one; both fail at run time.
	wantOne(t, "let c = scalar(2.0) @ zeros(3)", "1-D or 2-D operands")
}

func TestConstantIndexOutOfRangeIsReported(t *testing.T) {
	// twill indexes from 0 with no negative wraparound, so a constant index past
	// the length is the runtime's out-of-range error, caught early.
	wantOne(t, "let y = zeros(3)[3]", "index 3 out of range [0, 3)")
	wantOne(t, "let y = [1.0, 2.0, 3.0][5]", "index 5 out of range [0, 3)")
}

func TestConstantIndexInRangeIsClean(t *testing.T) {
	wantNone(t, "let y = zeros(3)[2]")
	wantNone(t, "let y = zeros(3, 4)[1]")
}

func TestANegativeAxisStillCountsFromTheEnd(t *testing.T) {
	for _, src := range []string{
		"let x = zeros(2, 3)\nprint(sum(x, -1))",
		"let x = zeros(2, 3)\nprint(mean(x, 0))",
	} {
		if diags := diagnostics(t, src); len(diags) != 0 {
			t.Errorf("%q was reported: %v", src, diags)
		}
	}
}

func TestAnUnknownNameIsReported(t *testing.T) {
	wantOne(t, "let x = 1.0\nprint(nope + x)", `unknown name "nope"`)
}

func TestAFunctionDeclaredLaterIsNotUnknown(t *testing.T) {
	// A file may call a function declared further down, and does at run time.
	// Walking strictly in order would report a name that is perfectly defined.
	src := `fn caller(x) = helper(x) * 2.0
fn helper(x) = x + 1.0
print(caller(3.0))`
	if diags := diagnostics(t, src); len(diags) != 0 {
		t.Fatalf("a forward reference was reported: %v", diags)
	}
}

func TestAnUnaliasedImportSilencesTheNameCheck(t *testing.T) {
	// It brings its names in unqualified and the checker does not read the
	// imported file, so it cannot know what those names are. Guessing would
	// report definitions that exist.
	src := "import \"std/nn\"\nlet x = 1.0\nprint(whatever_nn_defines + x)"
	if diags := diagnostics(t, src); len(diags) != 0 {
		t.Fatalf("a blind import still reported unknown names: %v", diags)
	}
}

func TestAnAliasedImportKeepsTheNameCheck(t *testing.T) {
	// Every borrowed name arrives with the alias on it, so an unqualified name
	// is still provably nothing.
	wantOne(t, "import \"std/nn\" as nn\nprint(nope)", `unknown name "nope"`)
}

func TestConcatReportsPiecesThatDoNotFit(t *testing.T) {
	wantOne(t, "let a = zeros(2, 3)\nlet b = zeros(4, 5)\nprint(concat([a, b], 0))",
		"shapes differ on axis 1")
}

func TestConcatShapeFlowsDownstream(t *testing.T) {
	// The second half of the win: concat used to return an unknown type, so a
	// whole pipeline built on one was unchecked from that point on.
	wantOne(t, `let a = zeros(2, 3)
let b = zeros(2, 3)
let c = concat([a, b], 0)
print(c @ zeros(9, 9))`, "[4, 3] @ [9, 9]")
}

func TestConcatOnAnotherAxisAddsUpThere(t *testing.T) {
	src := `let a = zeros(2, 3)
let b = zeros(2, 5)
print(concat([a, b], 1) @ zeros(8, 2))`
	if diags := diagnostics(t, src); len(diags) != 0 {
		t.Fatalf("a valid join was reported: %v", diags)
	}
}

func TestConcatWithAnAxisThatDoesNotExist(t *testing.T) {
	wantOne(t, "let a = zeros(2, 3)\nprint(concat([a, a], 9))", "axis out of range")
}

func TestConcatSaysNothingWhenAPieceIsUnknown(t *testing.T) {
	// Unknowable is not the same as wrong.
	src := "let a = zeros(2, 3)\nlet b = load(\"x.npy\")\nprint(concat([a, b], 0))"
	if diags := diagnostics(t, src); len(diags) != 0 {
		t.Fatalf("an unknowable concat was reported: %v", diags)
	}
}

// The rank-preserving flag on a reduction is a claim about shape, so the checker
// has to fold it: a kept axis lines back up against the input and a dropped one
// does not. Both halves matter -- the second is the diagnostic this feature
// exists to remove, and the first is the guarantee that removing it did not also
// remove the real one.
func TestKeepdimsIsFoldedIntoTheShape(t *testing.T) {
	// Reducing axis 1 of a [2, 3] gives a [2], and broadcasting aligns from the
	// right, so [2] against [2, 3] is a mismatch.
	wantOne(t, "let m = zeros(2, 3)\nlet r = m + sum(m, 1)", "shape mismatch")
	// Kept, it is a [2, 1], which does align.
	wantNone(t, "let m = zeros(2, 3)\nlet r = m + sum(m, 1, true)")
	wantNone(t, "let m = zeros(2, 3)\nlet r = m + mean(m, 1, true)")
	wantNone(t, "let m = zeros(2, 3)\nlet r = m + logsumexp(m, 1, true)")
	// A false flag is the dropping form, and still reports.
	wantOne(t, "let m = zeros(2, 3)\nlet r = m + sum(m, 1, false)", "shape mismatch")
	// A zero is false and a non-zero is true, matching the runtime's rule for a
	// flag written as a number.
	wantOne(t, "let m = zeros(2, 3)\nlet r = m + sum(m, 1, 0)", "shape mismatch")
	wantNone(t, "let m = zeros(2, 3)\nlet r = m + sum(m, 1, 1)")
	// An index reduction removes an axis like any other, so the flag means the
	// same thing there.
	wantOne(t, "let m = zeros(2, 3)\nlet r = m + argmax(m, 1)", "shape mismatch")
	wantNone(t, "let m = zeros(2, 3)\nlet r = m + argmax(m, 1, true)")
	wantNone(t, "let m = zeros(2, 3)\nlet r = m + argmin(m, 0, true)")
	// The axis is still checked when the flag is present, and it is still
	// checked when the flag is not a literal.
	wantOne(t, "let m = zeros(2, 3)\nlet r = sum(m, 5, true)", "axis out of range")
	wantOne(t, "let m = zeros(2, 3)\nlet r = argmax(m, 5, true)", "axis out of range")
	// A flag the checker cannot fold leaves the rank unknown rather than
	// guessing one of the two shapes and reporting against it.
	wantNone(t, "fn f(m, k) = m + sum(m, 1, k)")
	// And `sum()` is an arity error the runtime names; the checker used to
	// index argument 0 of an empty argument list and panic.
	wantNone(t, "let r = sum()")
}
