// Package ast defines the Twill syntax tree.
package ast

// Node is any tree node.
type Node interface{ Pos() int }

// Stmt is a statement.
type Stmt interface {
	Node
	stmt()
}

// Expr is an expression.
type Expr interface {
	Node
	expr()
}

// Dim is one dimension of a shape annotation: either a concrete Size (>= 0),
// a named shape variable (Var != ""), or an anonymous unknown (both zero-ish:
// Size < 0 and Var == "", written as `_`).
type Dim struct {
	Size int    // >= 0 for a concrete size, -1 otherwise
	Var  string // non-empty for a named shape variable
}

func ConcreteDim(n int) Dim    { return Dim{Size: n} }
func VarDim(name string) Dim   { return Dim{Size: -1, Var: name} }
func AnonDim() Dim             { return Dim{Size: -1} }
func (d Dim) IsConcrete() bool { return d.Size >= 0 }

// ShapeAnno is an optional tensor-shape annotation on a parameter or return.
// An empty Dims (len 0) means a scalar (rank-0 tensor).
type ShapeAnno struct {
	Dims []Dim
}

// ConcreteDims returns the annotation as plain sizes, with -1 for any
// non-concrete dimension (variable or anonymous).
func (s ShapeAnno) ConcreteDims() []int {
	out := make([]int, len(s.Dims))
	for i, d := range s.Dims {
		if d.IsConcrete() {
			out[i] = d.Size
		} else {
			out[i] = -1
		}
	}
	return out
}

// UnitFactor is one `name^exp` term of a unit expression (e.g. USD, year^-1).
type UnitFactor struct {
	Name string
	Exp  int
}

// UnitAnno is a scalar unit expression like `USD`, `USD/year`, or `1/year`,
// stored as a product of factors.
type UnitAnno struct {
	Factors []UnitFactor
}

// Param is a function parameter with an optional annotation: a shape
// (`x: [n, 2]`), a declared record type or unit name (`m: Model`, `p: USD`), or
// a compound unit expression (`r: USD/year`).
type Param struct {
	Name     string
	Shape    *ShapeAnno // non-nil for a shape annotation
	TypeName string     // a bare name: a record type or a unit (resolved by the checker)
	Unit     *UnitAnno  // a compound unit expression (has operators)
}

type Program struct {
	// Mode is the file-level mode named by a leading `mode <name>` declaration,
	// or "" when there is none. `mode systems` selects the systems dialect the
	// self-hosted compiler is written in; the bootstrap records it and runs the
	// features it already has, so a systems-mode file built from those parses
	// and runs rather than failing on the mode line.
	Mode string
	Body []Stmt
}

// --- statements ------------------------------------------------------------

type Let struct {
	Name string
	Unit *UnitAnno // optional unit annotation: `let px: USD/share = ...`
	// A named/qualified/generic type annotation (`let d: Arr[I64] = ...`), or "".
	// Advisory, like a parameter's TypeName; a `.` or `[` after the name marks it
	// unambiguously as a type rather than a unit.
	TypeName string
	Value    Expr
	// Const marks a `const` binding rather than a `let`. It binds once: nothing
	// may be assigned through the name afterwards, which is what a file-level
	// lookup table needs, since a plain import makes that binding shared with
	// every file that imports it. See docs/roadmap.md entry 28.
	Const bool
	Line  int
}

type FnDecl struct {
	Name string
	// TypeParams are the names in `fn first[T](xs: Arr[T]) -> T`. They are in
	// scope for the signature and the body, and stand for a type the caller
	// chooses.
	TypeParams []string
	Params     []Param
	Ret        *ShapeAnno // shape return
	RetUnit    *UnitAnno  // unit return (`-> USD`)
	RetType    string     // named/qualified type return (`-> Repl`, `-> cp.Caps`); advisory
	Body       Expr       // Block or single expression
	Line       int
}

// Assign is `target = value`, where target is an lvalue: a bare name, a field
// (`obj.f = v`), or an index (`arr[i] = v`), and these compose (`a.d[i] = v`).
type Assign struct {
	Target Expr
	Value  Expr
	Line   int
}

type While struct {
	Cond Expr
	Body *Block
	Line int
}

type For struct {
	Name string
	Iter Expr
	Body *Block
	Line int
}

type Return struct {
	Value Expr // nil for a bare return
	Line  int
}

// Break and Continue are the loop-control statements, valid inside a while/for.
type Break struct{ Line int }
type Continue struct{ Line int }

type Import struct {
	Path  string
	Alias string // non-empty for `import "..." as name` (a namespaced module)
	Line  int
}

// UnitDecl declares a base unit: `unit USD`.
type UnitDecl struct {
	Name string
	Line int
}

// TypeDecl declares a record type: `type Name = { field: shape, ... }`.
type TypeDecl struct {
	Name   string
	Fields []TypeField
	Line   int
}

type TypeField struct {
	Name  string
	Shape *ShapeAnno
}

type ExprStmt struct {
	X    Expr
	Line int
}

func (s *Let) Pos() int      { return s.Line }
func (s *FnDecl) Pos() int   { return s.Line }
func (s *Assign) Pos() int   { return s.Line }
func (s *While) Pos() int    { return s.Line }
func (s *For) Pos() int      { return s.Line }
func (s *Return) Pos() int   { return s.Line }
func (s *Import) Pos() int   { return s.Line }
func (s *UnitDecl) Pos() int { return s.Line }
func (s *TypeDecl) Pos() int { return s.Line }
func (s *ExprStmt) Pos() int { return s.Line }

func (s *Let) stmt()        {}
func (s *FnDecl) stmt()     {}
func (s *Assign) stmt()     {}
func (s *While) stmt()      {}
func (s *For) stmt()        {}
func (s *Return) stmt()     {}
func (s *Import) stmt()     {}
func (s *UnitDecl) stmt()   {}
func (s *TypeDecl) stmt()   {}
func (s *EnumDecl) stmt()   {}
func (s *StructDecl) stmt() {}
func (s *ExprStmt) stmt()   {}
func (s *Break) stmt()      {}
func (s *Continue) stmt()   {}

func (s *Break) Pos() int    { return s.Line }
func (s *Continue) Pos() int { return s.Line }

// StructDecl declares a record type: `struct Name { field: Type, ... }`. Field
// types are advisory text; records are structural, so the declaration names a
// type the checker can register without constraining how a record is built.
type StructDecl struct {
	Name string
	// TypeParams are the names in `struct Box[T, U]`. They stand for types the
	// declaration does not name, and a field whose type is one of them takes
	// whatever the use site supplies. Empty for a declaration without them,
	// which is every declaration written before 1.7.
	TypeParams []string
	Fields     []StructField
	Line       int
}

type StructField struct {
	Name string
	Type string // the field's type name (advisory); may be qualified or generic
}

func (s *StructDecl) Pos() int { return s.Line }

// EnumDecl declares a sum type: `enum Name { Case, Case(Payload), ... }`. Each
// case is a variant with an optional single payload. The payload type is kept
// only as a flag and a name (advisory), since the bootstrap does not check it.
type EnumDecl struct {
	Name string
	// TypeParams are the names in `enum MyOpt[T]`; see StructDecl.TypeParams.
	TypeParams []string
	Variants   []EnumVariant
	Line       int
}

type EnumVariant struct {
	Name       string
	HasPayload bool
	Payload    string // the payload type name (advisory); "" when no payload
}

func (s *EnumDecl) Pos() int { return s.Line }

// --- expressions -----------------------------------------------------------

type NumberLit struct {
	Value float64
	// Text is the literal as written. An integer literal above 2^53 is not the
	// f64 in Value, and the runtime reads the digits to make an exact I64 of it.
	Text string
	Line int
}

type StringLit struct {
	Value string
	Line  int
}

type BoolLit struct {
	Value bool
	Line  int
}

type Ident struct {
	Name string
	Line int
}

// TensorLit holds numeric or nested-tensor elements.
type TensorLit struct {
	Elements []Expr
	Line     int
}

type ListLit struct {
	Elements []Expr
	Line     int
}

type Lambda struct {
	Params  []Param
	Ret     *ShapeAnno
	RetUnit *UnitAnno
	RetType string // named/qualified type return; advisory, like FnDecl.RetType
	Body    Expr
	Line    int
}

type Unary struct {
	Op      string
	Operand Expr
	Line    int
}

type Binary struct {
	Op    string
	Left  Expr
	Right Expr
	Line  int
}

type Call struct {
	Callee Expr
	Args   []Expr
	Line   int
}

type Index struct {
	Target Expr
	Index  Expr
	Line   int
}

// Slice is target[start:end] along the first axis. Start or End may be nil,
// meaning the beginning or the end respectively.
type Slice struct {
	Target Expr
	Start  Expr // nil = from the beginning
	End    Expr // nil = to the end
	Line   int
}

// RecordLit is a record/struct literal: { name: expr, ... }.
type RecordLit struct {
	// TypeName is the name in front of a typed literal, `Point { x: 1.0 }`, or
	// "". Records are structural, so it is advisory: the value is the same record
	// `{ ... }` builds, and the name is kept only so the printer can reproduce it.
	TypeName string
	// Base is the `..expr` of a record update, `{ ..base, x: 1 }`, and nil for a
	// plain literal. The value is a copy of Base with Fields replacing the fields
	// they name. The copy is shallow: a field holding a list or a record holds
	// the same one the base does, exactly as writing `{ x: base.x }` out by hand
	// already does.
	Base   Expr
	Fields []RecordField
	Line   int
}

type RecordField struct {
	Name  string
	Value Expr
}

// Field is record field access: target.name.
type Field struct {
	Target Expr
	Name   string
	Line   int
}

type IfExpr struct {
	Cond Expr
	Then *Block
	Else Node // *Block, *IfExpr, or nil
	Line int
}

type Block struct {
	Body    []Stmt
	Line    int // line of the opening '{'
	EndLine int // line of the closing '}'
}

func (e *NumberLit) Pos() int { return e.Line }
func (e *StringLit) Pos() int { return e.Line }
func (e *BoolLit) Pos() int   { return e.Line }
func (e *Ident) Pos() int     { return e.Line }
func (e *TensorLit) Pos() int { return e.Line }
func (e *ListLit) Pos() int   { return e.Line }
func (e *Lambda) Pos() int    { return e.Line }
func (e *Unary) Pos() int     { return e.Line }
func (e *Binary) Pos() int    { return e.Line }
func (e *Call) Pos() int      { return e.Line }
func (e *Index) Pos() int     { return e.Line }
func (e *Slice) Pos() int     { return e.Line }
func (e *RecordLit) Pos() int { return e.Line }
func (e *Field) Pos() int     { return e.Line }
func (e *IfExpr) Pos() int    { return e.Line }
func (e *Block) Pos() int     { return e.Line }

func (e *NumberLit) expr() {}
func (e *StringLit) expr() {}
func (e *BoolLit) expr()   {}
func (e *Ident) expr()     {}
func (e *TensorLit) expr() {}
func (e *ListLit) expr()   {}
func (e *Lambda) expr()    {}
func (e *Unary) expr()     {}
func (e *Binary) expr()    {}
func (e *Call) expr()      {}
func (e *Index) expr()     {}
func (e *Slice) expr()     {}
func (e *RecordLit) expr() {}
func (e *Field) expr()     {}
func (e *IfExpr) expr()    {}
func (e *Match) expr()     {}
func (e *Try) expr()       {}
func (e *Block) expr()     {}

// Try is the postfix `?`: it unwraps the success case of a Res/Opt value (the
// payload of `Ok`/`Some`) or, on a failure case (`Err`/`None`), returns that
// value from the enclosing function.
type Try struct {
	Expr Expr
	Line int
}

func (e *Try) Pos() int { return e.Line }

// Match is `match subject { pattern => body, ... }`, an expression whose value
// is the body of the arm whose pattern matched. Arms are tried in order.
type Match struct {
	Subject Expr
	Arms    []MatchArm
	Line    int
}

type MatchArm struct {
	Pattern MatchPattern
	// Guard is the `if cond` a pattern may carry: the arm matches only when the
	// pattern fits AND the guard is true, with the pattern's bindings in scope
	// for it. nil when the arm has none. A guarded arm proves nothing about
	// exhaustiveness, because whether it runs is not a property of the value's
	// shape.
	Guard Expr
	// The arm's body is a statement: an expression, a `return`, an assignment,
	// or a block. Its value (for an expression arm) is the match's value.
	Body Stmt
}

// PatKind distinguishes the three things a pattern can be. They nest: a
// variant's payload is itself a pattern, so `Ok(Some(v))` and `Ok(3)` are a
// PatVariant whose Sub is a PatVariant and a PatLiteral respectively.
type PatKind int

const (
	// PatVariant is `Name` or `Name(sub)`. An identifier starting with an
	// upper-case letter is read as a variant; that is the rule that keeps
	// `Some(x)` from reading x as a nullary variant and `Ok(None)` from
	// reading None as a binder. Every enum variant in the language and its
	// libraries is upper-case initial.
	PatVariant PatKind = iota
	// PatBinding is a lower-case identifier, which binds the value it matches,
	// or `_`, which matches without binding. Binding is "" for `_`.
	PatBinding
	// PatLiteral is a number, string or boolean written in the pattern, and
	// matches by equality. It is refutable, so it never proves a variant
	// handled.
	PatLiteral
)

// MatchPattern is one pattern. It is a tree: `_`, a binder, a literal, a
// variant, or a variant carrying another pattern.
type MatchPattern struct {
	Kind PatKind
	// Variant is the case name, for PatVariant.
	Variant string
	// Sub is the payload pattern of a PatVariant written with parentheses.
	// nil when the pattern names the variant alone, which matches the case
	// whatever it carries.
	Sub *MatchPattern
	// Binding is the name a PatBinding introduces; "" is `_`.
	Binding string
	// Lit is the literal expression of a PatLiteral: a *NumberLit, *StringLit,
	// *BoolLit, or a *Unary negation of a number.
	Lit  Expr
	Line int
}

// CatchAll reports whether the pattern matches every value, whether or not it
// names what it matched. `_` and a bare binder both end a match, which is what
// the reachability and exhaustiveness rules are written in terms of.
func (p MatchPattern) CatchAll() bool { return p.Kind == PatBinding }

// CoversCase reports whether a variant pattern handles the whole of the case
// it names -- `Some`, `Some(v)` and `Some(_)` do, `Some(3)` and `Some(Ok(v))`
// do not, because each leaves other payloads of the same case unmatched.
// Whether those narrower arms together cover the case is a question about the
// whole set of arms, and the checker answers it there.
func (p MatchPattern) CoversCase() bool {
	return p.Kind == PatVariant && (p.Sub == nil || p.Sub.CatchAll())
}

func (e *Match) Pos() int { return e.Line }

// Block is also usable as a statement body; it satisfies Stmt too so blocks
// can appear where statements are expected.
func (e *Block) stmt() {}

// TupleLit is a fixed-arity positional group written `(a, b)`. The comma is
// what makes it one: `(x)` is x in parentheses and stays grouping, and `(x,)`
// is refused by the parser rather than read as a one-element tuple, because a
// language that has to explain a trailing comma has bought nothing.
//
// A tuple is destructured or passed on whole. There is deliberately no `.0`
// and no named tuple type: a value that wants to be stored and read by name
// stays a struct, which is what keeps this from becoming a second, worse
// record. See docs/roadmap.md entry 1.
type TupleLit struct {
	Elements []Expr
	Line     int
}

func (e *TupleLit) Pos() int { return e.Line }
func (e *TupleLit) expr()    {}

// LetTuple is destructuring: `let (lo, hi) = span(xs)`. It is a separate
// statement from Let rather than a field on it, so that every consumer of the
// tree has to say what it does with one instead of quietly binding nothing.
//
// Names has at least two entries, and `_` is written for a position the
// program does not want; those bind nothing.
//
// There is no `const` form. A `const` name may not be bound a second time in
// its scope, and that rule is enforced by walking the statements of a block
// looking for the name's other bindings; teaching it about a second shape of
// binding for a guarantee nobody has asked for would be a rule half kept, so
// `const (a, b) = ...` is refused at the parser instead.
type LetTuple struct {
	Names []string
	Value Expr
	Line  int
}

func (s *LetTuple) Pos() int { return s.Line }
func (s *LetTuple) stmt()    {}
