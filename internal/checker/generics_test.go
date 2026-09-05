package checker_test

import "testing"

// User-defined generics (NEEDS-4, 1.7). There is no monomorphization: the
// runtime is the same code whatever T is, so a declaration's type parameters
// only ever have to reach the types the checker judges against. What the tests
// below pin is that they do reach them -- that `Box[I64]`'s field is an I64 and
// not an unknown -- and that an unbound parameter still judges nothing.

const boxDecl = "mode systems\nstruct Box[T] { value: T, tag: Str }\n"
const treeDecl = "mode systems\nenum Tree[T] { Leaf(T), Branch(Arr[T]), Empty }\n"

func TestGenericFieldTakesTheArgument(t *testing.T) {
	wantOne(t, boxDecl+"fn f(b: Box[I64]) -> Str {\n  let s: Str = b.value\n  s\n}",
		"\"s\" is declared Str but the value is I64")
}

func TestGenericFieldWithTheRightTypeIsSilent(t *testing.T) {
	wantNone(t, boxDecl+"fn f(b: Box[Str]) -> Str {\n  let s: Str = b.value\n  s\n}")
}

// Substitution goes under the constructors a parameter is written inside, so a
// payload declared `Arr[T]` in a Tree[I64] is an Arr[I64].
func TestSubstitutionReachesInsideAType(t *testing.T) {
	wantOne(t, treeDecl+"fn f(t: Tree[I64]) -> Str {\n  match t { Branch(xs) => { let s: Str = xs; s }, Leaf(v) => \"leaf\", Empty => \"empty\" }\n}",
		"\"s\" is declared Str but the value is Arr[I64]")
}

// Two uses of the same generic struct are different types when their arguments
// differ. Before 1.7 records compared by name alone, which is still what every
// non-generic struct does.
func TestSameStructDifferentArgumentsDoNotMatch(t *testing.T) {
	wantOne(t, boxDecl+"fn f() -> Str {\n  let b: Box[I64] = Box { value: \"oops\", tag: \"t\" }\n  b.tag\n}",
		"\"b\" is declared Box[I64] but the value is Box[Str]")
}

// A constructor reads the argument back out of its payload: `Leaf(x)` is a
// Tree of whatever x is, without the program writing the argument anywhere.
// The payload here is an F64 rather than an I64 because a bare literal is one:
// since 1.6 an Int arises only where the program said I64.
func TestConstructorInfersTheArgument(t *testing.T) {
	wantOne(t, treeDecl+"fn f() -> Str {\n  let t: Tree[Str] = Leaf(7)\n  \"x\"\n}",
		"\"t\" is declared Tree[Str] but the value is Tree[F64]")
}

// And the same constructor with an I64 payload does say I64, which is what
// makes the argument the value's and not the literal syntax's.
func TestConstructorInfersAnIntArgument(t *testing.T) {
	wantOne(t, treeDecl+"fn f(n: I64) -> Str {\n  let t: Tree[Str] = Leaf(n)\n  \"x\"\n}",
		"\"t\" is declared Tree[Str] but the value is Tree[I64]")
}

// A literal says its arguments by what it is built from, which is the only way
// a generic value can be constructed: checking its fields against the
// parameters themselves would report every correct literal as a mismatch.
func TestGenericLiteralInfersItsArguments(t *testing.T) {
	wantNone(t, "mode systems\nstruct Pair[A, B] { first: A, second: B }\nfn swap[A, B](p: Pair[A, B]) -> Pair[B, A] = Pair { first: p.second, second: p.first }")
}

// A parameter that reached a judgement was never substituted, which means the
// use site did not say what it stands for. It judges nothing, so a generic
// declaration whose caller left an argument off is not refused.
func TestUnboundParameterJudgesNothing(t *testing.T) {
	wantNone(t, boxDecl+"fn f(b: Box) -> Str {\n  let s: Str = b.value\n  s\n}")
}

// A type parameter is in scope for the signature and the body, so `T` there is
// the parameter and not an unknown name.
func TestParameterIsInScopeForTheSignature(t *testing.T) {
	wantNone(t, "mode systems\nfn first[T](xs: Arr[T]) -> T = xs[0]")
}

// The parameters belong to their declaration and nowhere else: a `T` outside
// one is an ordinary unknown name, not a type variable left in scope.
func TestParametersDoNotEscapeTheirDeclaration(t *testing.T) {
	wantNone(t, "mode systems\nstruct Box[T] { value: T }\nfn g(x: T) -> Str = \"a\"")
}

// The rule above holds in systems mode. In numeric mode it does not: units are
// a numeric-mode feature, a bare name in return position is resolved as a unit,
// and the declaration's own parameters are not consulted first. Every
// neighbouring form of the same parameter is accepted, which is what makes the
// one refusal surprising rather than a deliberate restriction.
//
// This pins the behaviour rather than blessing it. docs/BUGS.md, Open, has the
// entry; fixing it should turn this test red, and the fix belongs with the
// documentation in docs/language-guide.md's "Type parameters" section and
// docs/RELEASE-1.7.md, which both print the numeric-mode form.
func TestABareTypeParameterInReturnPositionIsAUnitInNumericMode(t *testing.T) {
	wantOne(t, "fn first[T](xs: Arr[T]) -> T = xs[0]", `unknown unit "T"`)
	wantOne(t, "fn pick[Elem](a: Elem, b: Elem) -> Elem = a", `unknown unit "Elem"`)

	// The forms that are accepted, so a fix that widens the rule too far is
	// caught here too rather than only in the guide.
	wantNone(t, "fn first[T](xs: Arr[T]) = xs[0]")
	wantNone(t, "fn dup[T](xs: Arr[T]) -> Arr[T] = xs")
	wantNone(t, "fn take[T](x: T) = x")
	wantNone(t, "struct Box[T] { value: T, tag: Str }")
	wantNone(t, "enum Tree[T] { Leaf(T), Branch(Arr[T]), Empty }")
}
