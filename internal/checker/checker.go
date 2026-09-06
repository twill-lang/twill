// Package checker performs best-effort static shape analysis of a Twill
// program. It infers tensor shapes where it can and reports a diagnostic only
// when a mismatch is certain (both operand shapes fully known and
// incompatible). Anything it cannot determine is left as Unknown, so dynamic
// code never produces a false alarm.
package checker

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/twill-lang/twill/internal/ast"
	"github.com/twill-lang/twill/internal/tensor"
)

// Severity separates the findings that stop a program from the ones that only
// describe it. Almost everything the checker reports is an error: a shape, a
// type or a unit the program cannot have. A warning is different in kind --
// the program means what it says and will run, it will just run in a way the
// author probably did not intend. The values match src/check.tw's SEV_*.
type Severity int

const (
	SevError   Severity = 0
	SevWarning Severity = 1
)

// Diagnostic is a single shape/type finding.
type Diagnostic struct {
	Msg      string
	Line     int
	Severity Severity
}

// IsError reports whether a diagnostic is one that should stop the program.
func (d Diagnostic) IsError() bool { return d.Severity == SevError }

// CountErrors is how many of a run's diagnostics are errors. A caller deciding
// whether to run the program asks this rather than len(diags), because a
// warning is not a refusal.
func CountErrors(diags []Diagnostic) int {
	n := 0
	for _, d := range diags {
		if d.IsError() {
			n++
		}
	}
	return n
}

// Check analyses a program and returns any diagnostics found.
func Check(prog *ast.Program) []Diagnostic {
	c := newChecker(prog)
	env := c.prelude(prog)
	for _, s := range prog.Body {
		c.inferStmt(s, env)
	}
	return dedupe(c.diags)
}

// newChecker makes a checker for a program. It is apart from Check so the REPL
// can describe a single expression against the same setup rather than a second
// copy of it (see Describe).
func newChecker(prog *ast.Program) *checker {
	c := &checker{stack: map[ast.Node]bool{}, types: map[string]tRecord{}, units: map[string]bool{},
		systems: prog.Mode == "systems"}
	c.enums = map[string][]string{"Opt": {"Some", "None"}, "Res": {"Ok", "Err"}}
	c.variantOwner = map[string]string{"Some": "Opt", "None": "Opt", "Ok": "Res", "Err": "Res"}
	c.payloads = map[string]Type{"Some": nil, "Ok": nil, "Err": nil}
	c.structDecls = map[string]*ast.StructDecl{}
	c.typeParams = map[string][]string{}
	c.activeParams = map[string]bool{}
	return c
}

// prelude registers everything a body may refer to before the walk reaches it:
// declared types and units, the builtins, the enums' cases, and the names of
// the file's own functions and top-level lets. It returns the scope they are
// in.
func (c *checker) prelude(prog *ast.Program) *checkEnv {
	// Register declared record types and units first so forward references resolve.
	for _, s := range prog.Body {
		switch d := s.(type) {
		case *ast.TypeDecl:
			c.registerType(d)
		case *ast.UnitDecl:
			c.units[d.Name] = true
		case *ast.StructDecl:
			if len(d.TypeParams) > 0 {
				c.typeParams[d.Name] = d.TypeParams
			}
			// A struct registers as a nominal record type. Its field types are
			// parsed once every struct and enum name is known (structFieldTypes,
			// below), since a field may name either.
			fields := map[string]Type{}
			for _, f := range d.Fields {
				fields[f.Name] = tUnknown{}
			}
			c.types[d.Name] = tRecord{fields: fields, name: d.Name}
		}
	}
	env := newEnv(nil)
	for name := range builtinNames {
		env.define(name, tBuiltin{name})
	}
	// The built-in enums' cases are values with types: Some/Ok/Err are
	// constructors, None is an Opt.
	env.define("Some", tCtor{enum: "Opt", variant: "Some"})
	env.define("Ok", tCtor{enum: "Res", variant: "Ok"})
	env.define("Err", tCtor{enum: "Res", variant: "Err"})
	env.define("None", tEnum{name: "Opt"})
	// `unit` is the Unit value, not a function: a match arm or a function body
	// that ends in it evaluates to Unit.
	env.define("unit", tUnit{})
	// An import with no alias brings its names in unqualified, and this checker
	// does not read the imported file, so it cannot know what those names are.
	// One of them is enough to make every unknown name unreliable, so the whole
	// check stands down for the file rather than reporting a definition it
	// simply cannot see. `import "std/nn" as nn` keeps the check, since then
	// every borrowed name arrives with the alias on it.
	for _, st := range prog.Body {
		if imp, ok := st.(*ast.Import); ok {
			if imp.Alias == "" {
				c.blindImport = true
			} else {
				c.aliasedImport = true
			}
		}
	}
	// Every top-level function name, before any body is checked. A file may call
	// a function declared further down and does at run time, so a checker that
	// walks strictly in order would report a name that is perfectly well
	// defined. The line above says forward references resolve, and until now
	// that was true of types and units but not of functions.
	//
	// The declaration is not typed here, only named: checkFnDef does the real
	// work when the walk reaches it, and this is about knowing the name exists.
	// A declaration also wins over a builtin of the same name. Builtins were just
	// defined into this environment, so a plain "already seen?" guard would let
	// the builtin beat the file's own function -- but only for calls written
	// above the declaration, since reaching the declaration rebinds the name.
	// That made a shadow legal or illegal depending on where in the file it was
	// called from, and reported the builtin's arity against the user's function.
	//
	// A name declared twice in one file is refused here. The evaluator takes the
	// last definition, silently, which reads as the first one having been
	// replaced when it has only been shadowed: twill-lang/spool#4 fixed two
	// functions that had their replacements written above the bodies they were
	// meant to replace, and kept running the old ones through a passing test
	// suite, a passing source gate and passing CI. There is no conditional
	// compilation in this language, so a second declaration of one name in one
	// file is an edit that went wrong every time.
	firstLine := map[string]int{}
	for _, s := range prog.Body {
		if fn, ok := s.(*ast.FnDecl); ok {
			if at, dup := firstLine[fn.Name]; dup {
				c.report(fn.Line, "%s is already defined on line %d; the later definition is the one that runs, so the earlier one is dead. Delete whichever is stale, or rename one.", fn.Name, at)
				continue
			}
			firstLine[fn.Name] = fn.Line
			t, seen := env.get(fn.Name)
			if _, isBuiltin := t.(tBuiltin); !seen || isBuiltin {
				env.define(fn.Name, tUnknown{})
			}
		}
	}
	// Enum cases are names too: `Some`/`None`/`Ok`/`Err` and every user variant
	// must resolve wherever they are constructed, so they are defined before any
	// body is checked. The type they belong to is left advisory.
	// This file's own enums. An imported one may already be registered (see
	// loadImportedEnums); a file's own declaration wins, so it is registered
	// first and registerEnum leaves an existing entry alone.
	for _, s := range prog.Body {
		if ed, ok := s.(*ast.EnumDecl); ok {
			delete(c.enums, ed.Name)
			c.registerEnumFrom(ed, true)
		}
	}
	// With every enum and struct named, their payload and field types can be
	// parsed, and each variant bound as the value it is: a constructor for a
	// payload case, the enum itself for a bare one.
	for _, s := range prog.Body {
		if ed, ok := s.(*ast.EnumDecl); ok {
			if len(ed.TypeParams) > 0 {
				c.typeParams[ed.Name] = ed.TypeParams
			}
			done := c.withParams(ed.TypeParams)
			for _, v := range ed.Variants {
				if v.HasPayload {
					c.payloads[v.Name] = c.parseType(v.Payload)
				}
			}
			done()
		}
	}
	c.structFieldTypes(prog)
	for _, s := range prog.Body {
		if ed, ok := s.(*ast.EnumDecl); ok {
			for _, v := range ed.Variants {
				if _, seen := env.get(v.Name); seen && !isBuiltinCtor(v.Name) {
					continue
				}
				if t, ok := c.enumValueType(v.Name); ok {
					env.define(v.Name, t)
				} else {
					env.define(v.Name, tUnknown{})
				}
			}
		}
	}
	// Top-level `let` names too: a module-level constant may be referenced by a
	// function declared above its definition line (the compiler's own DTYPE_MAKERS
	// table is used this way). As with functions this only asserts the name
	// exists; inferStmt still types the binding when the walk reaches it, and
	// evaluation order is the evaluator's concern, not the checker's.
	//
	// A top-level `const` is registered as one here rather than when the walk
	// reaches it, because a function declared above the binding may assign to
	// it, and that assignment is checked when its declaration is reached. warp's
	// `examples/train.tw` has exactly that shape: `train_step` writes `STEPS`
	// several lines above the `let STEPS` that binds it.
	for _, s := range prog.Body {
		switch lt := s.(type) {
		case *ast.Let:
			if _, seen := env.get(lt.Name); !seen {
				env.define(lt.Name, tUnknown{})
			}
			if lt.Const {
				if _, already := env.consts[lt.Name]; !already {
					env.consts[lt.Name] = lt.Line
				}
			}
		case *ast.LetTuple:
			// Each name a destructuring binding introduces is a top-level name
			// too, so a function written above it may refer to one.
			for _, name := range lt.Names {
				if name == "_" {
					continue
				}
				if _, seen := env.get(name); !seen {
					env.define(name, tUnknown{})
				}
			}
		}
	}
	// The file's top level is a scope like any other, so a second binding of a
	// const name here is refused the same way one inside a block is.
	c.reportConstRebinds(prog.Body)
	return env
}

// isBuiltinCtor reports whether a name is one of the built-in enums' cases,
// which a user enum may re-declare (a file's own `enum Opt`) and then owns.
func isBuiltinCtor(name string) bool {
	return name == "Some" || name == "None" || name == "Ok" || name == "Err"
}

// dedupe removes repeated diagnostics (a body checked at definition and again
// at a call site can report the same finding twice).
func dedupe(diags []Diagnostic) []Diagnostic {
	if len(diags) < 2 {
		return diags
	}
	seen := map[string]bool{}
	out := diags[:0]
	for _, d := range diags {
		key := fmt.Sprintf("%d\x00%s", d.Line, d.Msg)
		if !seen[key] {
			seen[key] = true
			out = append(out, d)
		}
	}
	return out
}

type checker struct {
	diags []Diagnostic
	stack map[ast.Node]bool  // user functions currently being inferred
	types map[string]tRecord // declared record types
	units map[string]bool    // declared base units
	// Set when a file imports without an alias, which makes any unknown name
	// unprovable. See Check.
	blindImport bool
	// Set when a file imports under an alias. An enum's variant constructors are
	// registered program-wide at run time, so a constructor declared in an
	// aliased module (`SFn`, `EBlock`) is in scope unqualified even though this
	// single-file checker never read that module. Used only to soften the
	// unknown-name report for a capitalized constructor name. See inferExpr.
	aliasedImport bool
	// enums is every enum declared in the file, by name, each as its variant
	// names in declaration order; variantOwner maps a variant back to its enum.
	// Opt and Res are built in and always present. Both are what a match is
	// checked for exhaustiveness against.
	enums        map[string][]string
	variantOwner map[string]string

	// typeParams is the `[T, U]` a struct or enum declares, by declaration
	// name. activeParams is the set in scope right now -- while a declaration's
	// own field and payload types are being read, and while a generic
	// function's signature and body are being checked -- which is what makes a
	// bare `T` there resolve to the parameter rather than to an unknown name.
	typeParams   map[string][]string
	activeParams map[string]bool
	// payloads is each payload-carrying variant's declared payload type; a
	// payload-less variant is absent. structDecls keeps each struct's
	// declaration for its field order.
	payloads    map[string]Type
	structDecls map[string]*ast.StructDecl
	// Set for a `mode systems` file. The systems dialect names types the
	// bootstrap has no definition for (I64, Str, Bool, and module-qualified
	// names like cp.Caps), so an unresolved type annotation is advisory there
	// rather than the error it is in numeric mode.
	systems bool
	// Lexical count of enclosing loops for the statement being checked, reset to
	// zero at every function boundary. A `break`/`continue` is a diagnostic when
	// this is zero: outside any loop, or inside a function nested in one, which
	// the language forbids crossing. See inferStmt (Break/Continue, While/For)
	// and the two body walks (checkFnDef, inferUserCall).
	loopDepth int
	// inCallee is set while the callee of a call is being inferred, so that
	// `a.push(v)` -- uniform call syntax for `push(a, v)` -- is not reported as
	// reading a field of an array.
	inCallee bool
	// fnRet is the return annotation of each function whose body is being
	// checked, innermost last: what a `?` is checked against. A function with
	// no return annotation pushes "", which judges nothing; the top of a file
	// has an empty stack, where `?` has no function to return from.
	fnRet []string
}

func (c *checker) registerType(td *ast.TypeDecl) {
	fields := map[string]Type{}
	for _, f := range td.Fields {
		fields[f.Name] = tTensor{dims: f.Shape.ConcreteDims()}
	}
	c.types[td.Name] = tRecord{fields: fields}
}

func (c *checker) report(line int, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{Msg: fmt.Sprintf(format, args...), Line: line, Severity: SevError})
}

// warn records a finding that describes the program rather than refusing it.
// Mirrors src/check.tw's warn.
func (c *checker) warn(line int, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{Msg: fmt.Sprintf(format, args...), Line: line, Severity: SevWarning})
}

// --- types -----------------------------------------------------------------

type Type interface{ isType() }

// unitMap is a physical unit as base-name -> integer exponent. A nil/empty map
// is dimensionless. Units are checked statically and erased at runtime.
type unitMap map[string]int

// tTensor dims: a value of -1 means an unknown size; an empty slice is a scalar.
// unit is the quantity's physical unit (nil = dimensionless).
type tTensor struct {
	dims []int
	unit unitMap
	// dtp is the element dtype, stored as code+1 so that the ZERO VALUE means
	// "not known" -- which is what every one of the many bare tTensor{dims:...}
	// literals in this file should say, and what src/check.tw spells DT_UNKNOWN.
	// Read it with dtype() and set it with withDType(); never touch it directly.
	dtp uint8
}

// dtUnknown is src/check.tw's DT_UNKNOWN: the checker could not name the dtype.
// Everything degrades to it rather than guessing, because a wrong dtype here
// would report a widening the program does not have.
const dtUnknown tensor.DType = -1

// dtype reads the element dtype, or dtUnknown.
func (t tTensor) dtype() tensor.DType {
	if t.dtp == 0 {
		return dtUnknown
	}
	return tensor.DType(t.dtp - 1)
}

// withDType returns the tensor type with its dtype replaced. An unknown dtype
// clears the field, so "unknown" has exactly one representation.
func (t tTensor) withDType(dt tensor.DType) tTensor {
	if dt == dtUnknown {
		t.dtp = 0
		return t
	}
	t.dtp = uint8(dt) + 1
	return t
}

// argDType is the dtype of an argument that is a tensor, or dtUnknown. Several
// rearrangement builtins need exactly that and nothing else about it.
func argDType(argTypes []Type, i int) tensor.DType {
	if i < len(argTypes) {
		if t, ok := argTypes[i].(tTensor); ok {
			return t.dtype()
		}
	}
	return dtUnknown
}

// dtypeKnown mirrors src/check.tw's dtype_known.
func dtypeKnown(dt tensor.DType) bool { return dt >= 0 }

// setDType puts a dtype on a type when it is a tensor, and passes anything else
// through untouched. Mirrors src/check.tw set_dtype.
func setDType(res Type, dt tensor.DType) Type {
	if t, ok := res.(tTensor); ok {
		return t.withDType(dt)
	}
	return res
}

// promoteDType is the widen-only lattice, degrading to unknown when either side
// is. Mirrors src/check.tw promote_dtype.
func promoteDType(a, b tensor.DType) tensor.DType {
	if !dtypeKnown(a) || !dtypeKnown(b) {
		return dtUnknown
	}
	return tensor.Promote(a, b)
}

// promoteAndWarn is promoteDType plus the one warning this checker emits: a
// narrow float silently widened by a wider one. `bf16_weights + f64_bias` is
// f64, which is a perfectly correct answer that undoes the reason the weights
// were narrow, and the checker is the only place it can be seen before it runs
// (NEEDS-113).
//
// Only a float widened by a wider float qualifies. An integer meeting a float
// keeps the float unchanged, which is the lattice doing its job, and f16
// meeting bf16 promotes past both to f32, where neither operand was the wider
// one. Both stay silent. Byte-identical to src/check.tw promote_and_warn.
func (c *checker) promoteAndWarn(line int, a, b tensor.DType) tensor.DType {
	dt := promoteDType(a, b)
	if !dtypeKnown(dt) || a == b {
		return dt
	}
	if !tensor.IsFloatDType(a) || !tensor.IsFloatDType(b) {
		return dt
	}
	if dt != a && dt != b {
		return dt
	}
	narrow := a
	if dt == a {
		narrow = b
	}
	c.warn(line, "dtype widening: %s and %s promote to %s, which undoes the reason the %s operand is narrow",
		tensor.DTypeName(a), tensor.DTypeName(b), tensor.DTypeName(dt), tensor.DTypeName(narrow))
	return dt
}

// floatResultDType is the dtype of an operation that cannot return an integer:
// exp, sqrt, mean and their kin. The runtime promotes an integer input to f32.
// The checker instead declines to know it: claiming f32 would be true, and it
// would also let the widening warning fire on a program that never wrote a
// dtype, through a chain like exp(argmax(x)) meeting an ordinary f64. Zero new
// diagnostics on dtype-free programs is the harder promise, so an integer input
// degrades to unknown. Mirrors src/check.tw float_result_dtype.
func floatResultDType(dt tensor.DType) tensor.DType {
	if !dtypeKnown(dt) || tensor.IsIntDType(dt) {
		return dtUnknown
	}
	return dt
}

type tUnknown struct{}
type tBool struct{}
type tStr struct{}
type tUnit struct{}
type tList struct{ elems []Type } // nil elems: unknown contents
type tRecord struct {
	fields map[string]Type
	name   string // the struct's name for a nominal type; "" for a plain record
	// args are the type arguments a generic struct was used with: the I64 in
	// `Box[I64]`. Empty for every non-generic struct, which is what keeps two
	// of those comparing by name exactly as they did before 1.7.
	args []Type
}
type tFn struct {
	node    ast.Node
	params  []ast.Param
	ret     *ast.ShapeAnno
	retUnit *ast.UnitAnno
	retType string // the named return type as written (`Res[I64, Str]`), or ""
	body    ast.Expr
	env     *checkEnv
}
type tBuiltin struct{ name string }

func (tTensor) isType()  {}
func (tUnknown) isType() {}
func (tBool) isType()    {}
func (tStr) isType()     {}
func (tUnit) isType()    {}
func (tList) isType()    {}
func (tRecord) isType()  {}
func (tFn) isType()      {}
func (tBuiltin) isType() {}

func scalar() tTensor { return tTensor{dims: []int{}} }

// --- units ------------------------------------------------------------------

func (u unitMap) clean() unitMap {
	for k, v := range u {
		if v == 0 {
			delete(u, k)
		}
	}
	if len(u) == 0 {
		return nil
	}
	return u
}

func unitEqual(a, b unitMap) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func unitMul(a, b unitMap) unitMap {
	r := unitMap{}
	for k, v := range a {
		r[k] += v
	}
	for k, v := range b {
		r[k] += v
	}
	return r.clean()
}

func unitDiv(a, b unitMap) unitMap {
	r := unitMap{}
	for k, v := range a {
		r[k] += v
	}
	for k, v := range b {
		r[k] -= v
	}
	return r.clean()
}

func unitPow(a unitMap, k int) unitMap {
	if k == 0 || len(a) == 0 {
		return nil
	}
	r := unitMap{}
	for name, v := range a {
		r[name] = v * k
	}
	return r.clean()
}

// unitSqrt halves each exponent; ok is false if any is odd.
func unitSqrt(a unitMap) (unitMap, bool) {
	r := unitMap{}
	for name, v := range a {
		if v%2 != 0 {
			return nil, false
		}
		r[name] = v / 2
	}
	return r.clean(), true
}

func unitString(u unitMap) string {
	if len(u) == 0 {
		return "1"
	}
	names := make([]string, 0, len(u))
	for k := range u {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		if u[n] == 1 {
			parts[i] = n
		} else {
			parts[i] = fmt.Sprintf("%s^%d", n, u[n])
		}
	}
	return strings.Join(parts, "*")
}

func unitFromAnno(a *ast.UnitAnno) unitMap {
	r := unitMap{}
	for _, f := range a.Factors {
		r[f.Name] += f.Exp
	}
	return r.clean()
}

// resolveUnit builds a unit map from an annotation and reports any factor that
// names a unit which was never declared with `unit NAME`.
func (c *checker) resolveUnit(a *ast.UnitAnno, line int) unitMap {
	for _, f := range a.Factors {
		if !c.units[f.Name] {
			c.report(line, "unknown unit %q (declare it with `unit %s`)", f.Name, f.Name)
		}
	}
	return unitFromAnno(a)
}

func isScalar(t tTensor) bool { return len(t.dims) == 0 }

func fullyKnown(t tTensor) bool {
	for _, d := range t.dims {
		if d < 0 {
			return false
		}
	}
	return true
}

func dimsString(t tTensor) string {
	s := "["
	for i, d := range t.dims {
		if i > 0 {
			s += ", "
		}
		if d < 0 {
			s += "_"
		} else {
			s += fmt.Sprintf("%d", d)
		}
	}
	return s + "]"
}

// --- environment -----------------------------------------------------------

type checkEnv struct {
	vars map[string]Type
	// consts is the subset of vars bound by `const`, each with the line it was
	// bound on. Kept per scope rather than per name so that a `let` shadowing a
	// `const` in an inner scope is an ordinary mutable binding.
	consts map[string]int
	parent *checkEnv
}

func newEnv(parent *checkEnv) *checkEnv {
	return &checkEnv{vars: map[string]Type{}, consts: map[string]int{}, parent: parent}
}

func (e *checkEnv) get(name string) (Type, bool) {
	for env := e; env != nil; env = env.parent {
		if t, ok := env.vars[name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (e *checkEnv) define(name string, t Type) { e.vars[name] = t }

// constLine reports whether a name resolves to a `const` binding, and the line
// it was bound on. It stops at the first scope that binds the name, so a `let`
// of the same name in a nearer scope shadows the const and is mutable.
func (e *checkEnv) constLine(name string) (int, bool) {
	for env := e; env != nil; env = env.parent {
		if _, ok := env.vars[name]; ok {
			line, isConst := env.consts[name]
			return line, isConst
		}
	}
	return 0, false
}

func (e *checkEnv) assign(name string, t Type) {
	for env := e; env != nil; env = env.parent {
		if _, ok := env.vars[name]; ok {
			env.vars[name] = t
			return
		}
	}
	e.define(name, t)
}

// --- statements ------------------------------------------------------------

func (c *checker) inferStmt(s ast.Stmt, env *checkEnv) {
	switch st := s.(type) {
	case *ast.Let:
		rhs := c.inferExpr(st.Value, env)
		if st.TypeName != "" || (st.Unit != nil && c.isAdvisoryTypeAnno(st.Unit)) {
			// A type annotation: the binding is declared to be that type, and the
			// value has to be one. In systems mode the name is one of the dialect's
			// types; elsewhere it is a record type or unknown, and judged only if
			// known.
			want := c.parseType(annoText(st.TypeName, st.Unit))
			what := fmt.Sprintf("%q", st.Name)
			if c.checkAssignable(st.Line, what, want, rhs) {
				c.fractionalLiteralAtInt(st.Line, what, want, st.Value)
			}
			env.define(st.Name, bindingType(want, rhs))
		} else if st.Unit != nil && !c.isAdvisoryTypeAnno(st.Unit) {
			want := c.resolveUnit(st.Unit, st.Line)
			if t, ok := rhs.(tTensor); ok {
				if len(t.unit) != 0 && !unitEqual(t.unit, want) {
					c.report(st.Line, "%q declares unit %s but the value has unit %s",
						st.Name, unitString(want), unitString(t.unit))
				}
				env.define(st.Name, tTensor{dims: t.dims, unit: want})
			} else {
				env.define(st.Name, tTensor{dims: []int{}, unit: want})
			}
		} else {
			env.define(st.Name, rhs)
		}
		// `const` binds in this scope, whichever of the three branches above did
		// the binding. A top-level one is already recorded by the prelude; this
		// is what makes a `const` inside a function work, and what lets an inner
		// `let` of the same name shadow an outer `const` and stay mutable.
		//
		// Nothing here revokes constness. A second binding of the name in this
		// same scope is refused outright by reportConstRebinds, which is what
		// keeps the guarantee from depending on statement order.
		if st.Const {
			env.consts[st.Name] = st.Line
		}
	case *ast.LetTuple:
		c.checkLetTuple(st, env)
	case *ast.FnDecl:
		env.define(st.Name, tFn{node: st, params: st.Params, ret: st.Ret, retUnit: st.RetUnit, retType: st.RetType, body: st.Body, env: env})
		// Check the body once at definition using the parameter annotations, so
		// mistakes are caught even if the function is never called. Unannotated
		// parameters are unknown, so this only reports definite errors.
		c.checkFnDef(st, env)
	case *ast.Assign:
		v := c.inferExpr(st.Value, env)
		// A `const` is bound once, and the name is the whole of the guarantee:
		// `HEX = other()` swaps the table, and `HEX[0] = "#000"` edits the one
		// everybody is reading, so both are refused. What cannot be refused here
		// is a mutation that does not go through the name, `push(HEX, x)` or a
		// function handed the handle, because `Arr` has reference semantics and
		// nothing tracks where a handle goes. docs/language-guide.md says so
		// where `const` is introduced; docs/roadmap.md entry 28.
		if base, ok := lvalueBase(st.Target); ok {
			if at, isConst := env.constLine(base.Name); isConst {
				c.report(st.Line, "%s is declared const on line %d, so nothing may be assigned through that name: not the binding, and not an element or field of it. Bind a new name for the changed value, or declare it with let if it is meant to change.", base.Name, at)
			}
		}
		if id, ok := st.Target.(*ast.Ident); ok {
			env.assign(id.Name, v)
		} else {
			// A field or index target: infer it so a malformed one is still
			// checked, but there is no name to rebind. A struct field keeps its
			// declared type across the assignment, so the value is checked
			// against it.
			target := c.inferExpr(st.Target, env)
			if fld, ok := st.Target.(*ast.Field); ok {
				if rec, ok := c.inferExpr(fld.Target, env).(tRecord); ok && rec.name != "" {
					what := fmt.Sprintf("field %q of %s", fld.Name, rec.name)
					if c.checkAssignable(st.Line, what, target, v) {
						c.fractionalLiteralAtInt(st.Line, what, target, st.Value)
					}
				}
			}
		}
	case *ast.While:
		c.inferExpr(st.Cond, env)
		c.loopDepth++
		c.inferBlock(st.Body, newEnv(env))
		c.loopDepth--
	case *ast.For:
		iter := c.inferExpr(st.Iter, env)
		scope := newEnv(env)
		scope.define(st.Name, elemType(iter))
		c.loopDepth++
		c.inferBlock(st.Body, scope)
		c.loopDepth--
	case *ast.Return:
		if st.Value != nil {
			got := c.inferExpr(st.Value, env)
			c.checkReturnType(st.Line, got, st.Value)
		}
	case *ast.Import:
		// Imported definitions are resolved at runtime. For a namespaced
		// import, bind the alias so field access on it stays unknown rather
		// than "undefined".
		if st.Alias != "" {
			env.define(st.Alias, tUnknown{})
		}
	case *ast.TypeDecl, *ast.UnitDecl, *ast.EnumDecl, *ast.StructDecl:
		// Already registered in the pre-pass.
	case *ast.Break:
		if c.loopDepth == 0 {
			c.report(st.Line, "break outside a loop")
		}
	case *ast.Continue:
		if c.loopDepth == 0 {
			c.report(st.Line, "continue outside a loop")
		}
	case *ast.ExprStmt:
		c.inferExpr(st.X, env)
	case *ast.Block:
		c.inferBlock(st, newEnv(env))
	}
}

func (c *checker) inferBlock(b *ast.Block, env *checkEnv) Type {
	c.reportConstRebinds(b.Body)
	var last Type = tUnit{}
	for _, s := range b.Body {
		if es, ok := s.(*ast.ExprStmt); ok {
			last = c.inferExpr(es.X, env)
		} else {
			c.inferStmt(s, env)
			last = tUnit{}
		}
	}
	return last
}

// reportConstRebinds refuses a second binding of a name that a `const` binds in
// the same statement list.
//
// The rule exists because the alternative is a silent revocation. Constness is
// held in a per-scope map, and the first cut of it had a plain `let` of the same
// name delete the entry, so `const HEX` followed by `let HEX` turned the
// guarantee off with nothing said. It was order-dependent on top of that: the
// prelude registers top-level consts before the walk starts, so a `let` above
// the `const` was refused and a `let` below it was not. Moving a line changed
// what the checker promised.
//
// Order is not consulted here at all: the whole list is scanned for the first
// `const` binding of each name, and every other binding of those names is
// reported, whichever side of it they sit on. Two plain `let`s of one name stay
// legal, because that is an idiom this language allows and this rule is about
// `const`. An inner scope is not this list, so shadowing is untouched.
//
// A destructuring `let` is a binding too, and it is counted here for the same
// reason a plain one is. The first cut of tuples looked only at *ast.Let, so
// `const A = 1.0` followed by `let (A, b) = (2.0, 3.0)` rebound A with nothing
// said while `let A = 2.0` on the same line was refused with the full message.
// A rule that a second shape of binding walks around is not a rule.
func (c *checker) reportConstRebinds(body []ast.Stmt) {
	// Indices rather than lines: two statements can share a line, so a line
	// number cannot tell the const's own binding apart from a second one.
	first := map[string]int{}
	for i, s := range body {
		if lt, ok := s.(*ast.Let); ok && lt.Const {
			if _, already := first[lt.Name]; !already {
				first[lt.Name] = i
			}
		}
	}
	if len(first) == 0 {
		return
	}
	// A const's own binding is the one at `first[name]`, and only a *ast.Let can
	// be it: there is no `const (a, b) = ...`. So a LetTuple naming a const name
	// is always a second binding, with no index to excuse it.
	rebind := func(name string, line, at int) {
		decl := body[at].(*ast.Let)
		c.report(line, "%s is declared const on line %d, so the name cannot be bound a second time in the same scope: a second binding would take its place and everything after it would be assignable again. Rename one of them, or declare line %d with let if the name is meant to change.", name, decl.Line, decl.Line)
	}
	for i, s := range body {
		switch b := s.(type) {
		case *ast.Let:
			at, isConst := first[b.Name]
			if !isConst || at == i {
				continue
			}
			rebind(b.Name, b.Line, at)
		case *ast.LetTuple:
			for _, name := range b.Names {
				if name == "_" {
					continue
				}
				at, isConst := first[name]
				if !isConst {
					continue
				}
				rebind(name, b.Line, at)
			}
		}
	}
}

// reportRepeatedTupleNames refuses `let (a, a) = (1.0, 2.0)`.
//
// Positional binding has no way to say what a repeat means. Nothing merges the
// two values and nothing reads them apart, so the second `define` simply lands
// on top of the first and the last position wins, which is a typo answered with
// a number rather than a diagnostic. `_` repeats freely, because `_` binds
// nothing and is the written way to skip a position.
//
// It runs before the value is inferred so that a program with both faults reads
// in the order it was written: the names are wrong on their own terms, whatever
// the right-hand side turns out to be.
func (c *checker) reportRepeatedTupleNames(st *ast.LetTuple) {
	seen := map[string]bool{}
	for _, name := range st.Names {
		if name == "_" {
			continue
		}
		if seen[name] {
			c.report(st.Line, "this let binds %s twice, and the later position would take the earlier one's place with nothing said. Rename one of them, or write _ for a position whose value the program does not want.", name)
			continue
		}
		seen[name] = true
	}
}

// checkLetTuple judges `let (lo, hi) = span(xs)`. The names are bound whatever
// happens, so a mistake here reports once rather than once more for every use
// of a name the destructuring did not manage to bind.
func (c *checker) checkLetTuple(st *ast.LetTuple, env *checkEnv) {
	c.reportRepeatedTupleNames(st)
	rhs := c.inferExpr(st.Value, env)
	bind := func(t Type) {
		for _, name := range st.Names {
			if name == "_" {
				continue
			}
			env.define(name, t)
		}
	}
	tup, ok := rhs.(tTuple)
	if !ok {
		// Only a definite mismatch is reported. An unknown right-hand side --
		// a call into a module this single-file checker never read -- says
		// nothing about how many values come back, so it binds unknowns and
		// stays quiet, which is the policy everywhere else in this file.
		if !isUnknownType(rhs) {
			c.report(st.Line, "this let destructures a tuple of %d values, but the value is %s",
				len(st.Names), c.typeString(rhs))
		}
		bind(tUnknown{})
		return
	}
	if len(tup.elems) != len(st.Names) {
		c.report(st.Line, "this let binds %d names, but the value is %s, which has %d",
			len(st.Names), c.typeString(tup), len(tup.elems))
		bind(tUnknown{})
		return
	}
	for i, name := range st.Names {
		if name == "_" {
			continue
		}
		env.define(name, tup.elems[i])
	}
}

// lvalueBase is the name an assignment target ultimately reaches: `x` for `x`,
// for `x.f`, for `x[i]` and for `a.d[i]`. An assignment through a call or any
// other expression has no base name, and reports nothing.
func lvalueBase(target ast.Expr) (*ast.Ident, bool) {
	for {
		switch t := target.(type) {
		case *ast.Ident:
			return t, true
		case *ast.Field:
			target = t.Target
		case *ast.Index:
			target = t.Target
		default:
			return nil, false
		}
	}
}

// elemType returns the element type produced by iterating a value.
func elemType(t Type) Type {
	switch v := t.(type) {
	case tTensor:
		if len(v.dims) == 1 {
			return scalar()
		}
		if len(v.dims) > 1 {
			return tTensor{dims: v.dims[1:]}
		}
	case tList:
		return tUnknown{}
	case tArr:
		if v.elem != nil {
			return v.elem
		}
	}
	return tUnknown{}
}

// --- expressions -----------------------------------------------------------

func startsUpper(s string) bool {
	return s != "" && s[0] >= 'A' && s[0] <= 'Z'
}

// isQualifiedVariant reports whether `Target.Name` is a variant named by its
// enum rather than a field read. The qualifier has to be a name the environment
// does not bind (a real value keeps its field access) and the field has to
// resolve as a variant.
func (c *checker) isQualifiedVariant(target ast.Expr, name string, env *checkEnv) bool {
	id, ok := target.(*ast.Ident)
	if !ok {
		return false
	}
	if _, bound := env.get(id.Name); bound {
		return false
	}
	// Both halves are type-and-variant names, which are capitalised; that is what
	// separates `Opt.Some` from a field read on a name the checker cannot see.
	if !startsUpper(id.Name) || !startsUpper(name) {
		return false
	}
	if _, bound := env.get(name); bound {
		return true
	}
	return c.crossModuleVariant(name)
}

// crossModuleVariant reports whether an unresolved name is plausibly an enum
// variant constructor borrowed from an aliased import. Variant constructors are
// registered program-wide at run time and are capitalized by convention, so a
// capitalized unknown in a systems file that imports another module cannot be
// proven undefined from this file alone. A lowercase unknown (a value or a
// function) is still a typo and still reported.
func (c *checker) crossModuleVariant(name string) bool {
	if !c.systems || !c.aliasedImport || name == "" {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

func (c *checker) inferExpr(e ast.Expr, env *checkEnv) Type {
	switch ex := e.(type) {
	case *ast.NumberLit:
		return scalar()
	case *ast.StringLit:
		return tStr{}
	case *ast.BoolLit:
		return tBool{}
	case *ast.Ident:
		if t, ok := env.get(ex.Name); ok {
			return t
		}
		// A name that resolves to nothing is a typo, and it was the one mistake
		// the checker could see plainly and said nothing about. Builtins are not
		// in the environment because they are not values, so they are checked
		// against the table instead.
		if !builtinNames[ex.Name] && !c.blindImport && !c.crossModuleVariant(ex.Name) {
			c.report(ex.Line, "unknown name %q", ex.Name)
		}
		return tUnknown{}
	case *ast.TensorLit:
		return c.inferTensorLit(ex)
	case *ast.ListLit:
		elems := make([]Type, len(ex.Elements))
		for i, el := range ex.Elements {
			elems[i] = c.inferExpr(el, env)
		}
		return tList{elems: elems}
	case *ast.TupleLit:
		elems := make([]Type, len(ex.Elements))
		for i, el := range ex.Elements {
			elems[i] = c.inferExpr(el, env)
		}
		return tTuple{elems: elems}
	case *ast.Lambda:
		return tFn{node: ex, params: ex.Params, ret: ex.Ret, retUnit: ex.RetUnit, retType: ex.RetType, body: ex.Body, env: env}
	case *ast.Unary:
		return c.inferUnary(ex, env)
	case *ast.Binary:
		return c.inferBinary(ex, env)
	case *ast.Call:
		return c.inferCall(ex, env)
	case *ast.Index:
		return c.inferIndex(ex, env)
	case *ast.Slice:
		return c.inferSlice(ex, env)
	case *ast.RecordLit:
		return c.inferRecordLit(ex, env)
	case *ast.Field:
		// `Opt.Some` names a variant by its enum. The enum name is not a value, so
		// inferring the target would report it as unknown; the qualifier is read
		// and dropped, exactly as the parser does in a pattern.
		if c.isQualifiedVariant(ex.Target, ex.Name, env) {
			return tUnknown{}
		}
		target := c.inferExpr(ex.Target, env)
		if rec, ok := target.(tRecord); ok {
			if ft, ok := rec.fields[ex.Name]; ok {
				return ft
			}
			c.report(ex.Line, "record has no field %q", ex.Name)
			return tUnknown{}
		}
		// A field of something that cannot have one. The record case was
		// checked and this one was not, though the checker knows the type just
		// as well: `let x: I64 = 5` followed by `x.foo` reached the runtime.
		//
		// Not when it is a call's callee, though: `a.push(v)` is uniform call
		// syntax for `push(a, v)` and works on any value, so a field there is a
		// function name and not a field at all. A tensor is excluded for the
		// same family of reasons -- `t.to(f32)` is written this way.
		if !c.inCallee && isDefiniteNonRecord(target) {
			c.report(ex.Line, "cannot read field %q of %s", ex.Name, c.typeString(target))
			return tUnknown{}
		}
		return tUnknown{}
	case *ast.IfExpr:
		c.inferExpr(ex.Cond, env)
		then := c.inferBlock(ex.Then, newEnv(env))
		switch alt := ex.Else.(type) {
		case *ast.Block:
			els := c.inferBlock(alt, newEnv(env))
			return join(then, els)
		case *ast.IfExpr:
			return join(then, c.inferExpr(alt, env))
		default:
			// No else, so the value is the branch's when the condition holds
			// and `()` when it does not. Unit is the honest half to report: it
			// is the one a caller is not expecting, and it is what makes
			// `fn f() -> Str { if b { "yes" } }` a function that can hand back
			// a unit where it promised a string.
			return tUnit{}
		}
	case *ast.Match:
		subject := c.inferExpr(ex.Subject, env)
		c.checkMatchArms(ex)
		// Each arm is checked in its own scope with the pattern's binding present,
		// typed as the payload when the subject's type says what that is. The
		// match's type is left unknown rather than unified across arms, which
		// keeps the dialect's records-and-enums permissive.
		for _, arm := range ex.Arms {
			armEnv := newEnv(env)
			c.bindPattern(arm.Pattern, subject, armEnv)
			if arm.Guard != nil {
				c.inferExpr(arm.Guard, armEnv)
			}
			c.inferStmt(arm.Body, armEnv)
		}
		return tUnknown{}
	case *ast.Try:
		// `?` yields the success payload: a Res[T, E] gives a T and an Opt[T] a
		// T, so what it is bound to is checked against that.
		subject := c.inferExpr(ex.Expr, env)
		c.checkTryContext(ex.Line)
		if en, ok := subject.(tEnum); ok && (en.name == "Res" || en.name == "Opt") {
			if len(en.args) > 0 && en.args[0] != nil {
				return en.args[0]
			}
			return tUnknown{}
		}
		if _, unknown := subject.(tUnknown); !unknown {
			c.report(ex.Line, "`?` needs a Res or an Opt, but the value is %s", c.typeString(subject))
		}
		return tUnknown{}
	case *ast.Block:
		return c.inferBlock(ex, newEnv(env))
	}
	return tUnknown{}
}

// withParams brings a declaration's type parameters into scope and returns the
// function that takes them out again. They nest by saving what they shadow, so
// a `T` that was already a parameter of an enclosing declaration comes back
// afterwards rather than disappearing.
func (c *checker) withParams(params []string) func() {
	if len(params) == 0 {
		return func() {}
	}
	shadowed := make(map[string]bool, len(params))
	for _, p := range params {
		shadowed[p] = c.activeParams[p]
		c.activeParams[p] = true
	}
	return func() {
		for p, was := range shadowed {
			if was {
				c.activeParams[p] = true
			} else {
				delete(c.activeParams, p)
			}
		}
	}
}

// checkTryContext is the rule for `?`: it returns the failure from the
// enclosing function, so that function must be one, and it must return a Res
// or an Opt for the failure to be returnable. A function with no return
// annotation is not judged.
func (c *checker) checkTryContext(line int) {
	if len(c.fnRet) == 0 {
		c.report(line, "`?` outside a function: there is nothing to return the failure from")
		return
	}
	ret := c.fnRet[len(c.fnRet)-1]
	if ret == "" {
		return
	}
	head := ret
	if i := strings.IndexByte(head, '['); i >= 0 {
		head = head[:i]
	}
	if head != "Res" && head != "Opt" {
		c.report(line, "`?` in a function that returns %s: the failure it propagates needs a Res or Opt return type", ret)
	}
}

// retTypeName is a function's named return type whichever slot the parser put
// it in: a qualified or generic name arrives as text, a bare one as a
// one-factor unit annotation. "" when there is none.
func retTypeName(retType string, u *ast.UnitAnno) string {
	if retType != "" {
		return retType
	}
	if u != nil && len(u.Factors) == 1 && u.Factors[0].Exp == 1 {
		return u.Factors[0].Name
	}
	return ""
}

// checkMatchArms is the exhaustiveness check: every variant of the matched enum
// has an arm, or a `_` stands for the rest. The enum is the one that owns the
// variants the arms name, so the subject's static type is not needed; a match
// naming variants of two enums, a variant twice, or a `_` after every variant
// is already listed, is reported too. A variant this file cannot see -- one
// declared in an aliased module -- leaves the match unjudged, since the
// checker reports only what it can prove (docs/type-system.md, "match").
func (c *checker) checkMatchArms(m *ast.Match) {
	owner := ""
	covered := map[string]bool{}
	catchAllAt := -1
	// judgeable is the set of arms exhaustiveness is computed from: the ones
	// whose running depends on nothing but the value's shape. A guard is a
	// condition this checker cannot evaluate, so a guarded arm is checked for
	// reachability and then set aside.
	var judgeable, before []ast.MatchPattern
	for i, arm := range m.Arms {
		pat := arm.Pattern
		if catchAllAt >= 0 {
			c.report(pat.Line, "unreachable match arm: %s comes after %s, which already matches everything", patternText(pat), patternText(m.Arms[catchAllAt].Pattern))
		}
		if arm.Guard == nil {
			if pat.CatchAll() && catchAllAt < 0 {
				// What makes a catch-all redundant is the arms BEFORE it
				// covering everything already; the arms after it are
				// unreachable and prove nothing. So the tally stops here.
				before = append([]ast.MatchPattern(nil), judgeable...)
				catchAllAt = i
			}
			judgeable = append(judgeable, pat)
			if pat.CatchAll() {
				continue
			}
		}
		if pat.Kind != ast.PatVariant {
			// A literal says nothing about which enum is being matched, and a
			// binder under a guard covers nothing.
			continue
		}
		if arm.Guard == nil && pat.CoversCase() {
			if covered[pat.Variant] {
				c.report(pat.Line, "duplicate match arm: %s is already handled", pat.Variant)
			}
			covered[pat.Variant] = true
		}
		en, known := c.variantOwner[pat.Variant]
		if !known || en == "" {
			// Not an enum this file declares (or an ambiguous name): the match
			// cannot be judged.
			return
		}
		if owner == "" {
			owner = en
		} else if owner != en {
			c.report(pat.Line, "match arm %s belongs to enum %s, but the earlier arms match %s", pat.Variant, en, owner)
			return
		}
	}
	if owner == "" {
		return
	}
	tally := judgeable
	if catchAllAt >= 0 {
		tally = before
	}
	missing, judged := c.missingCases(tally)
	if !judged {
		return
	}
	if catchAllAt >= 0 {
		if len(missing) == 0 {
			c.report(m.Arms[catchAllAt].Pattern.Line, "unreachable match arm: %s matches nothing, every variant of %s is already handled", patternText(m.Arms[catchAllAt].Pattern), owner)
		}
		return
	}
	if len(missing) > 0 {
		c.report(m.Line, "match on %s is not exhaustive: missing %s", owner, strings.Join(missing, ", "))
	}
}

// missingCases answers, for the set of patterns offered at one position, which
// values reach none of them. It recurses into payloads, so
// `Some(Ok(v)), Some(Err(e)), None` is exhaustive over `Opt[Res[..]]` while
// dropping the `Err` arm reports the value that gets through by name,
// `Some(Err(...))`.
//
// The second result is whether the question could be answered at all. A
// position holding only literals, or naming a variant of an enum this file
// cannot see, is not judged -- the checker reports what it can prove, and a
// guess here would be a false "not exhaustive" on a correct program.
func (c *checker) missingCases(pats []ast.MatchPattern) ([]string, bool) {
	byVariant := map[string][]ast.MatchPattern{}
	whole := map[string]bool{}
	bools := map[bool]bool{}
	literals := false
	owner := ""
	for _, pat := range pats {
		switch pat.Kind {
		case ast.PatBinding:
			// A binder or `_` at this position takes every value there is.
			return nil, true
		case ast.PatLiteral:
			literals = true
			if b, ok := pat.Lit.(*ast.BoolLit); ok {
				bools[b.Value] = true
			}
			continue
		}
		en, known := c.variantOwner[pat.Variant]
		if !known || en == "" || (owner != "" && en != owner) {
			return nil, false
		}
		owner = en
		if pat.Sub == nil || pat.Sub.CatchAll() {
			whole[pat.Variant] = true
			continue
		}
		byVariant[pat.Variant] = append(byVariant[pat.Variant], *pat.Sub)
	}
	if owner == "" {
		if bools[true] && bools[false] {
			// Two literals do cover a Bool, and it is the only domain small
			// enough for that to be true.
			return nil, true
		}
		if literals {
			// Literals over a domain that is not Bool leave values behind
			// however many are written, and `...` is what those values are
			// called in the diagnostic.
			return []string{"..."}, true
		}
		return nil, false
	}
	var missing []string
	for _, v := range c.enums[owner] {
		if whole[v] {
			continue
		}
		subs, any := byVariant[v]
		if !any {
			missing = append(missing, v)
			continue
		}
		inner, judged := c.missingCases(subs)
		if !judged {
			return nil, false
		}
		for _, m := range inner {
			missing = append(missing, v+"("+m+")")
		}
	}
	return missing, true
}

// patternText names a pattern in a diagnostic. It is deliberately shallow: the
// diagnostics that use it are about which arm, not about what the arm looks
// like all the way down.
func patternText(pat ast.MatchPattern) string {
	switch pat.Kind {
	case ast.PatBinding:
		if pat.Binding == "" {
			return "`_`"
		}
		return "`" + pat.Binding + "`"
	case ast.PatLiteral:
		return "this literal pattern"
	}
	if pat.Sub != nil {
		return pat.Variant + "(...)"
	}
	return pat.Variant
}

// elementCount multiplies a shape out, when every dimension is known.
//
// A negative dimension is this checker's way of saying "not known yet", and one
// of those makes the product meaningless rather than small, so it reports
// nothing instead of a number that would produce a confident wrong answer.
// reportNegDim flags a constructor asked for a negative dimension. Untyped, it
// reaches the runtime as a make([]float64, n) with n < 0 and panics there, which
// surfaces as an unhelpful "makeslice: len out of range" rather than a shape
// error naming the offending call. Caught here, it reads like every other shape
// diagnostic.
func (c *checker) reportNegDim(line int, name string, dims []int) bool {
	for _, d := range dims {
		if d < 0 {
			c.report(line, "%s: dimension %d is negative", name, d)
			return true
		}
	}
	return false
}

func elementCount(dims []int) (int, bool) {
	n := 1
	for _, d := range dims {
		if d < 0 {
			return 0, false
		}
		n *= d
	}
	return n, true
}

// listDims reads the shape of a nested list literal, when it is one a tensor
// can be built from.
//
// Ragged input reports no shape rather than the first row's. `[[1, 2], [3]]` is
// an error at run time, and inventing a shape for it here would produce a
// second, imaginary error somewhere downstream instead of the real one.
func listDims(t Type) ([]int, bool) {
	lst, ok := t.(tList)
	if !ok || lst.elems == nil {
		return nil, false
	}
	if len(lst.elems) == 0 {
		return []int{0}, true
	}

	// Every element has to agree, both on being a list and on its shape, or the
	// literal is ragged.
	if _, nested := lst.elems[0].(tList); nested {
		first, ok := listDims(lst.elems[0])
		if !ok {
			return nil, false
		}
		for _, el := range lst.elems[1:] {
			d, ok := listDims(el)
			if !ok || !sameDims(d, first) {
				return nil, false
			}
		}
		return append([]int{len(lst.elems)}, first...), true
	}

	// A flat list of scalars is one dimension. A scalar here is a tensor with
	// no dimensions, which is how a number types in this checker.
	for _, el := range lst.elems {
		e, ok := el.(tTensor)
		if !ok || len(e.dims) != 0 {
			return nil, false
		}
	}
	return []int{len(lst.elems)}, true
}

func sameDims(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *checker) inferTensorLit(ex *ast.TensorLit) Type {
	// `[]` evaluates to an empty list, not to a tensor of shape [0]: the
	// evaluator has no elements to read a shape from, so it builds the
	// container that does not need one. Typing it as a tensor made the checker
	// confidently wrong at the dimension-0 boundary, which is exactly where a
	// shape checker is supposed to earn its keep -- `sum([])` checked clean and
	// then failed at run time.
	if len(ex.Elements) == 0 {
		return tList{}
	}
	var dims []int
	ok := true
	var walk func(elems []ast.Expr, depth int)
	walk = func(elems []ast.Expr, depth int) {
		if depth == len(dims) {
			dims = append(dims, len(elems))
		} else if dims[depth] != len(elems) {
			ok = false
		}
		for _, el := range elems {
			if inner, isT := el.(*ast.TensorLit); isT {
				walk(inner.Elements, depth+1)
			}
		}
	}
	walk(ex.Elements, 0)
	if !ok {
		c.report(ex.Line, "ragged tensor literal: rows have inconsistent lengths")
		return tUnknown{}
	}
	// A tensor literal is f64, the documented default (docs/dtypes.md): narrow
	// dtypes are always asked for and never inferred. A BARE number literal is
	// deliberately not given one -- its eventual dtype is still an open
	// question -- which is why `w * 2.0` is silent and `w * scalar(2.0)` is not.
	return tTensor{dims: dims}.withDType(tensor.DTF64)
}

func (c *checker) inferUnary(ex *ast.Unary, env *checkEnv) Type {
	t := c.inferExpr(ex.Operand, env)
	if ex.Op == "-" {
		if tt, ok := t.(tTensor); ok {
			return tt
		}
		if _, ok := t.(tInt); ok {
			return t
		}
		return tUnknown{}
	}
	return tBool{}
}

func (c *checker) inferBinary(ex *ast.Binary, env *checkEnv) Type {
	op := ex.Op
	if op == "and" || op == "or" || op == "&&" || op == "||" {
		c.inferExpr(ex.Left, env)
		c.inferExpr(ex.Right, env)
		return tUnknown{}
	}
	// The bitwise words take two integers and give one back. They are scalar-only
	// (the builtin form truncates each operand to I64), so the result is a scalar
	// whatever the operands were inferred as.
	// `//` is scalar integer division; like the bitwise words it takes two
	// numbers and gives one back.
	if op == "//" {
		c.inferExpr(ex.Left, env)
		c.inferExpr(ex.Right, env)
		return tTensor{}
	}
	if op == "band" || op == "bor" || op == "xor" || op == "shl" || op == "shr" {
		c.inferExpr(ex.Left, env)
		c.inferExpr(ex.Right, env)
		return tTensor{}
	}
	l := c.inferExpr(ex.Left, env)
	r := c.inferExpr(ex.Right, env)

	// An I64 is a scalar to the shape and unit rules; what is I64 about it is
	// the result type. Two I64s (or an I64 and a whole-number literal) give an
	// I64 for the arithmetic operators; an I64 against a fraction, or against a
	// scalar the checker cannot tell is whole, is F64.
	_, lInt := l.(tInt)
	_, rInt := r.(tInt)
	if lInt {
		l = scalar()
	}
	if rInt {
		r = scalar()
	}
	intResult := lInt && rInt || (lInt && isWholeLiteral(ex.Right)) || (rInt && isWholeLiteral(ex.Left))

	lt, lok := l.(tTensor)
	rt, rok := r.(tTensor)

	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		if lok && rok && !unitEqual(lt.unit, rt.unit) {
			c.report(ex.Line, "comparing incompatible units %s and %s", unitString(lt.unit), unitString(rt.unit))
		}
		// `==` and `!=` are deep equality and are defined on everything.
		// Ordering is not: it is numbers and strings, and anything else is the
		// runtime's "cannot compare these values". An `Opt[I64]` compared
		// against 0 is the shape that mistake takes -- it is what a call that
		// used to return -1 looks like after it starts returning an Opt -- so
		// it is worth catching where it is written.
		if op != "==" && op != "!=" {
			if bad, ok := c.unorderable(l); ok {
				c.report(ex.Line, "cannot order %s with %q: ordering is numbers and strings", bad, op)
			} else if bad, ok := c.unorderable(r); ok {
				c.report(ex.Line, "cannot order %s with %q: ordering is numbers and strings", bad, op)
			}
		}
		return tBool{}
	}

	// `+` concatenates strings. When either side is a string the result is a
	// string, provided the other side is a string too (or unknown, left advisory).
	// A string added to a definite number or tensor is still the error below.
	if op == "+" {
		_, lStr := l.(tStr)
		_, rStr := r.(tStr)
		if lStr || rStr {
			_, lUnk := l.(tUnknown)
			_, rUnk := r.(tUnknown)
			if (lStr || lUnk) && (rStr || rUnk) {
				return tStr{}
			}
			c.report(ex.Line, "operator %q joins two strings or two numbers, not a mix", op)
			return tUnknown{}
		}
	}

	// A definite non-tensor operand to arithmetic is a type error.
	if isDefiniteNonTensor(l) || isDefiniteNonTensor(r) {
		c.report(ex.Line, "operator %q needs numbers/tensors", op)
		return tUnknown{}
	}
	if !lok || !rok {
		return tUnknown{}
	}

	// The dtype the runtime would produce, and the widening warning when a
	// narrow float meets a wider one. Every remaining operator goes through
	// tensor.Promote at run time, `^` included; comparisons produced bool above
	// and never reach this. Computed before the shape rules so the two checkers
	// emit their diagnostics in the same order.
	dt := c.promoteAndWarn(ex.Line, lt.dtype(), rt.dtype())

	switch op {
	case "@":
		res, msg := matmulResult(lt, rt)
		if msg != "" {
			c.report(ex.Line, "%s", msg)
			return tUnknown{}
		}
		return setDType(withUnit(res, unitMul(lt.unit, rt.unit)), dt)
	case "^":
		return setDType(c.powUnit(lt, ex), dt)
	default: // + - * / %
		res, msg := elementwiseResult(lt, rt)
		if msg != "" {
			c.report(ex.Line, "%s", msg)
			return tUnknown{}
		}
		if intResult {
			return tInt{}
		}
		return setDType(withUnit(res, c.arithUnit(op, lt, rt, ex.Line)), dt)
	}
}

// isWholeLiteral reports whether an expression is an integer literal (or a
// negated one), which computes as an I64 beside an I64.
func isWholeLiteral(e ast.Expr) bool {
	if u, ok := e.(*ast.Unary); ok && u.Op == "-" {
		e = u.Operand
	}
	lit, ok := e.(*ast.NumberLit)
	// Trunc, not a round trip through int64: converting a float at or above
	// 2^63 to an int64 is out of range and lands on MIN_I64, which made
	// MAX_I64 read as not-whole and quietly turned `mx + 1` into F64
	// arithmetic. See fractionalLiteralAtInt.
	return ok && lit.Value == math.Trunc(lit.Value) && !strings.ContainsAny(lit.Text, ".eE")
}

// withUnit attaches a unit to a tTensor result, leaving other types unchanged.
func withUnit(res Type, u unitMap) Type {
	if t, ok := res.(tTensor); ok {
		t.unit = u
		return t
	}
	return res
}

func (c *checker) arithUnit(op string, lt, rt tTensor, line int) unitMap {
	switch op {
	case "+", "-", "%":
		if !unitEqual(lt.unit, rt.unit) {
			c.report(line, "unit mismatch: %s %s %s", unitString(lt.unit), op, unitString(rt.unit))
		}
		return lt.unit
	case "*":
		return unitMul(lt.unit, rt.unit)
	case "/":
		return unitDiv(lt.unit, rt.unit)
	}
	return nil
}

func (c *checker) powUnit(lt tTensor, ex *ast.Binary) Type {
	if len(lt.unit) == 0 {
		return lt
	}
	k, ok := constInt(ex.Right)
	if !ok {
		c.report(ex.Line, "a quantity with unit %s can only be raised to a constant integer power", unitString(lt.unit))
		return withUnit(lt, nil)
	}
	return withUnit(lt, unitPow(lt.unit, k))
}

func (c *checker) inferIndex(ex *ast.Index, env *checkEnv) Type {
	t := c.inferExpr(ex.Target, env)
	c.inferExpr(ex.Index, env)
	switch v := t.(type) {
	case tTensor:
		if isScalar(v) {
			c.report(ex.Line, "cannot index a scalar")
			return tUnknown{}
		}
		// A constant index into a known-length axis is bounds-checkable: twill
		// indexes from 0 with no negative wraparound, so anything outside
		// [0, len) is the error the runtime raises.
		if v.dims[0] >= 0 {
			if idx, ok := constInt(ex.Index); ok && (idx < 0 || idx >= v.dims[0]) {
				c.report(ex.Line, "index %d out of range [0, %d)", idx, v.dims[0])
			}
		}
		if len(v.dims) == 1 {
			return tTensor{dims: []int{}, unit: v.unit}.withDType(v.dtype())
		}
		// Indexing selects, so the element type is the one it selected from.
		return tTensor{dims: v.dims[1:], unit: v.unit}.withDType(v.dtype())
	case tList:
		return tUnknown{}
	case tArr:
		if v.elem != nil {
			return v.elem
		}
		return tUnknown{}
	case tDict:
		if v.val != nil {
			return v.val
		}
		return tUnknown{}
	case tStr:
		// A Str indexes to the byte at that offset, as a number.
		return tInt{}
	}
	return tUnknown{}
}

func (c *checker) inferSlice(ex *ast.Slice, env *checkEnv) Type {
	t := c.inferExpr(ex.Target, env)
	if ex.Start != nil {
		c.inferExpr(ex.Start, env)
	}
	if ex.End != nil {
		c.inferExpr(ex.End, env)
	}
	switch v := t.(type) {
	case tTensor:
		if len(v.dims) == 0 {
			c.report(ex.Line, "cannot slice a scalar")
			return tUnknown{}
		}
		first := -1
		s, sok := 0, true
		if ex.Start != nil {
			s, sok = constInt(ex.Start)
		}
		e, eok := 0, false
		if ex.End != nil {
			e, eok = constInt(ex.End)
		}
		// Bounds check, when the first dim and both endpoints are known. It
		// mirrors the runtime: a negative endpoint counts from the end, then the
		// slice must satisfy 0 <= start <= end <= dim0. This reached the runtime
		// as "slice [a:b] out of range for first dim n", a message the checker can
		// raise before the program runs.
		dim0 := v.dims[0]
		if dim0 >= 0 && sok {
			as := s
			if as < 0 {
				as += dim0
			}
			ae := dim0
			if eok {
				ae = e
				if ae < 0 {
					ae += dim0
				}
			}
			if as < 0 || ae > dim0 || as > ae {
				c.report(ex.Line, "slice [%d:%d] out of range for first dim %d", as, ae, dim0)
				return tUnknown{}
			}
			first = ae - as
		} else if eok && sok && e-s >= 0 {
			first = e - s
		}
		return tTensor{dims: append([]int{first}, v.dims[1:]...), unit: v.unit}
	case tList:
		return tList{}
	case tArr:
		return v
	case tStr:
		return tStr{}
	}
	return tUnknown{}
}

// --- calls -----------------------------------------------------------------

func (c *checker) inferCall(ex *ast.Call, env *checkEnv) Type {
	// x.to(f32) casts x, and here is only a call whose callee is a field access
	// named `to` with one dtype-name argument. The dims and unit ride through
	// unchanged -- a cast changes the values' format, not what they mean -- and
	// the dtype name is read from the syntax rather than inferred, so it is not
	// flagged as an unknown variable. A target that is not a visible tensor makes
	// it unknown and says nothing, leaving the runtime to reject an uncastable to.
	if t, ok := c.inferCast(ex, env); ok {
		return t
	}
	savedInCallee := c.inCallee
	c.inCallee = true
	callee := c.inferExpr(ex.Callee, env)
	c.inCallee = savedInCallee
	// A trailing dtype name on a maker, `zeros([2, 3], bf16)`, is contextual: it
	// counts only on a maker and only when nothing in scope binds it. Strip it so
	// the maker infers its shape from the rest -- the dtype changes the element
	// type, which the shape checker does not track, not the shape -- and so the
	// arity check counts the arguments the maker actually received.
	callEx := ex
	ctorDType := dtUnknown
	if b, ok := callee.(tBuiltin); ok && dtypeMakerNames[b.name] && len(ex.Args) > 0 {
		if dt := dtypeToken(env, ex.Args[len(ex.Args)-1]); dtypeKnown(dt) {
			ctorDType = dt
			callEx = &ast.Call{Callee: ex.Callee, Args: ex.Args[:len(ex.Args)-1], Line: ex.Line}
		}
	}
	argTypes := make([]Type, len(callEx.Args))
	for i, a := range callEx.Args {
		argTypes[i] = c.inferExpr(a, env)
	}

	switch fn := callee.(type) {
	case tBuiltin:
		if t, ok := systemsBuiltinResult(fn.name, argTypes); ok {
			// The arity check the shape cases would have made still applies.
			if arity, known := builtinArity[fn.name]; known && len(callEx.Args) != arity {
				c.report(ex.Line, "%s expects %d argument(s), got %d", fn.name, arity, len(callEx.Args))
				return tUnknown{}
			}
			return t
		}
		// The maker's trailing dtype is passed in rather than stamped on the
		// result, because only the constructor cases should take it: stamping
		// every builtin's result would overwrite a dtype propagated from an
		// argument.
		return c.inferBuiltinCall(fn.name, callEx, argTypes, ctorDType)
	case tFn:
		return c.inferUserCall(fn, callEx, argTypes)
	case tCtor:
		return c.callCtor(fn, ex.Line, argTypes, callEx.Args)
	case tFnType:
		if len(fn.params) != len(argTypes) {
			c.report(ex.Line, "function expects %d argument(s), got %d", len(fn.params), len(argTypes))
			return tUnknown{}
		}
		for i, p := range fn.params {
			c.checkAssignable(ex.Line, fmt.Sprintf("argument %d", i+1), p, argTypes[i])
		}
		return fn.ret
	case tUnknown:
		return tUnknown{}
	case tList, tBool, tStr, tUnit, tRecord, tInt, tArr, tDict, tEnum, tBytes, tTuple:
		c.report(ex.Line, "value is not callable")
		return tUnknown{}
	}
	return tUnknown{}
}

// dtypeMakerNames are the tensor constructors whose trailing argument may be a
// dtype name (docs/dtypes.md); it matches the interpreter's dtypeMakers.
var dtypeMakerNames = map[string]bool{
	"tensor": true, "scalar": true, "zeros": true, "ones": true,
	"fill": true, "randn": true, "rand": true, "eye": true,
}

// inferCast recognises x.to(dt): a call whose callee is a `to` field access with
// one bare dtype-name argument that the environment does not bind (a binding
// wins, since the name could be an ordinary variable). It returns the target's
// tensor type with the shape and unit unchanged, or unknown for a non-tensor
// target; ok is false when this is not a to-cast, so the caller falls through to
// ordinary inference. It mirrors the self-hosted infer_cast.
func (c *checker) inferCast(ex *ast.Call, env *checkEnv) (Type, bool) {
	fld, ok := ex.Callee.(*ast.Field)
	if !ok || fld.Name != "to" || len(ex.Args) != 1 {
		return nil, false
	}
	id, ok := ex.Args[0].(*ast.Ident)
	if !ok {
		return nil, false
	}
	if _, bound := env.get(id.Name); bound {
		return nil, false
	}
	dt, isDType := tensor.DTypeOfName(id.Name)
	if !isDType {
		return nil, false
	}
	if t, ok := c.inferExpr(fld.Target, env).(tTensor); ok {
		return tTensor{dims: t.dims, unit: t.unit}.withDType(dt), true
	}
	return tUnknown{}, true
}

// dtypeToken is the dtype an argument names, or dtUnknown when it does not name
// one. It is the checker's copy of the interpreter's contextual rule and reads a
// name as a dtype under exactly the same three conditions: the argument is a
// bare identifier, it is one of the seven names, and the environment does not
// bind it. That last condition is what keeps `f32` an ordinary identifier
// everywhere it holds a value, so `let f32 = x; zeros(f32)` is a shape and not a
// dtype -- matching how the program will actually run. Mirrors src/check.tw
// dtype_token.
func dtypeToken(env *checkEnv, e ast.Expr) tensor.DType {
	id, ok := e.(*ast.Ident)
	if !ok {
		return dtUnknown
	}
	if _, bound := env.get(id.Name); bound {
		return dtUnknown
	}
	dt, isDType := tensor.DTypeOfName(id.Name)
	if !isDType {
		return dtUnknown
	}
	return dt
}

// paramType gives a parameter's type from its annotation alone (for checking a
// body at definition). Shape variables and unannotated params are unknown.
func (c *checker) paramType(p ast.Param) Type {
	switch {
	case p.Unit != nil:
		return tTensor{dims: []int{}, unit: unitFromAnno(p.Unit)}
	case p.TypeName != "":
		if t, ok := c.types[p.TypeName]; ok {
			return t
		}
		if c.units[p.TypeName] {
			return tTensor{dims: []int{}, unit: unitMap{p.TypeName: 1}}
		}
		return c.parseType(p.TypeName)
	case p.Shape != nil:
		return tTensor{dims: p.Shape.ConcreteDims()}
	default:
		return tUnknown{}
	}
}

// checkReturnUnit reports if the body's unit disagrees with a declared unit
// return.
func (c *checker) checkReturnUnit(line int, name string, retUnit *ast.UnitAnno, bodyType Type) {
	want := unitFromAnno(retUnit)
	if got, ok := bodyType.(tTensor); ok && !unitEqual(got.unit, want) {
		who := "function"
		if name != "" {
			who = fmt.Sprintf("function %q", name)
		}
		c.report(line, "%s returns unit %s but its signature declares %s",
			who, unitString(got.unit), unitString(want))
	}
}

func (c *checker) checkFnDef(fn *ast.FnDecl, env *checkEnv) {
	if c.stack[fn] {
		return
	}
	// A generic function's parameters are in scope for its signature and its
	// body alike, so `fn first[T](xs: Arr[T]) -> T` reads both the annotation
	// and the return type in terms of T.
	doneParams := c.withParams(fn.TypeParams)
	defer doneParams()
	scope := newEnv(env)
	for _, p := range fn.Params {
		if p.Unit != nil {
			c.resolveUnit(p.Unit, fn.Line) // report undeclared unit names
		}
		scope.define(p.Name, c.paramType(p))
	}
	if fn.RetUnit != nil && !c.isAdvisoryTypeAnno(fn.RetUnit) {
		c.resolveUnit(fn.RetUnit, fn.Line)
	}
	c.stack[fn] = true
	// A function body is a fresh scope for loop control: a `break` in a `fn`
	// written inside a loop is an error, not a way out of that loop.
	savedDepth := c.loopDepth
	c.loopDepth = 0
	c.fnRet = append(c.fnRet, retTypeName(fn.RetType, fn.RetUnit))
	var bodyType Type
	if blk, ok := fn.Body.(*ast.Block); ok {
		bodyType = c.inferBlock(blk, scope)
	} else {
		bodyType = c.inferExpr(fn.Body, scope)
	}
	c.fnRet = c.fnRet[:len(c.fnRet)-1]
	c.loopDepth = savedDepth
	delete(c.stack, fn)

	if fn.Ret != nil {
		expected := tTensor{dims: fn.Ret.ConcreteDims()}
		if got, ok := bodyType.(tTensor); ok && fullyKnown(expected) && fullyKnown(got) && !shapeMatch(expected, got) {
			c.report(fn.Line, "function %q returns %s but its signature declares %s",
				fn.Name, dimsString(got), dimsString(expected))
		}
	}
	if fn.RetUnit != nil && !c.isAdvisoryTypeAnno(fn.RetUnit) {
		c.checkReturnUnit(fn.Line, fn.Name, fn.RetUnit, bodyType)
	}
	c.checkBodyReturnType(fn.Line, fn.Name, retTypeName(fn.RetType, fn.RetUnit), bodyType, fn.Body)
}

// checkBodyReturnType compares what a function body evaluates to against its
// declared named return type. A block whose last statement is not an
// expression (it returned early, or ends in a loop) evaluates to Unit and is
// not judged, since its `return` statements were checked where they stand.
func (c *checker) checkBodyReturnType(line int, name, retType string, bodyType Type, body ast.Expr) {
	if retType == "" {
		return
	}
	want := c.parseType(retType)
	if _, isUnit := bodyType.(tUnit); isUnit {
		if _, wantUnit := want.(tUnit); wantUnit {
			return
		}
		// A body that evaluates to Unit against a declared value type is a
		// function that falls off its end, and that is worth reporting -- but
		// only when there is no `return` in it to have produced the value
		// instead. `return` statements are checked where they stand, and a
		// body ending in a loop or an if-without-else evaluates to Unit while
		// still returning properly from inside.
		//
		// This skipped the whole case, so `fn name(b: Bool) -> Str { if b {
		// "yes" } }` returned () silently and the caller got a unit where it
		// had been promised a string.
		if body == nil || hasValueReturn(body) {
			return
		}
	}
	who := "function"
	if name != "" {
		who = fmt.Sprintf("function %q", name)
	}
	if !assignable(want, bodyType) {
		c.report(line, "%s returns %s but its signature declares %s", who, c.typeString(bodyType), c.typeString(want))
	}
}

// hasValueReturn reports whether a function body contains a `return` carrying a
// value, anywhere that is not inside a nested function -- a nested function's
// returns are its own.
func hasValueReturn(e ast.Expr) bool {
	found := false
	var walkStmt func(ast.Stmt)
	var walkExpr func(ast.Expr)
	walkStmt = func(s ast.Stmt) {
		if found || s == nil {
			return
		}
		switch st := s.(type) {
		case *ast.Return:
			if st.Value != nil {
				found = true
			}
		case *ast.Block:
			for _, in := range st.Body {
				walkStmt(in)
			}
		case *ast.While:
			walkStmt(st.Body)
		case *ast.For:
			walkStmt(st.Body)
		case *ast.ExprStmt:
			walkExpr(st.X)
		case *ast.Let:
			walkExpr(st.Value)
		case *ast.LetTuple:
			walkExpr(st.Value)
		case *ast.Assign:
			walkExpr(st.Value)
		}
	}
	walkExpr = func(x ast.Expr) {
		if found || x == nil {
			return
		}
		switch ex := x.(type) {
		case *ast.Block:
			for _, in := range ex.Body {
				walkStmt(in)
			}
		case *ast.IfExpr:
			walkStmt(ex.Then)
			if ex.Else != nil {
				switch alt := ex.Else.(type) {
				case *ast.Block:
					walkStmt(alt)
				case *ast.IfExpr:
					walkExpr(alt)
				}
			}
		case *ast.Match:
			for _, arm := range ex.Arms {
				walkStmt(arm.Body)
			}
		}
	}
	walkExpr(e)
	return found
}

// checkReturnType is checkBodyReturnType for an explicit `return e`, against
// the return type of the innermost function being checked.
func (c *checker) checkReturnType(line int, got Type, e ast.Expr) {
	if len(c.fnRet) == 0 || c.fnRet[len(c.fnRet)-1] == "" {
		return
	}
	want := c.parseType(c.fnRet[len(c.fnRet)-1])
	if !assignable(want, got) {
		c.report(line, "return gives %s but the function declares %s", c.typeString(got), c.typeString(want))
	}
}

// inferRecordLit types a record literal, including the update form
// `{ ..base, f: v }`. The update's type is the base's fields with the named ones
// replacing what they name, which is the value the evaluator builds.
//
// A field named in an update that the base does not have is not reported. The
// record it produces is the one `{ a: base.a, b: 1 }` produces and runs exactly
// as well, and records here are structural, so refusing it would be a diagnostic
// on a correct program -- the one kind of mistake this checker is not allowed to
// make. A typed update, `P { ..base, b: 1 }`, is still checked against P's
// declaration by checkStructLit below, which is where a misspelt field on a
// struct is caught.
//
// An untyped update produces a plain record rather than the base's struct type.
// Its fields are what a later field access is answered from, and a name it kept
// would be a nominal claim about a value the update may have added a field to.
func (c *checker) inferRecordLit(ex *ast.RecordLit, env *checkEnv) Type {
	fields := map[string]Type{}
	// The base is inferred first because it is written first: a diagnostic from
	// inside it should come before one from a field that replaces part of it.
	if ex.Base != nil {
		base := c.inferExpr(ex.Base, env)
		if rec, ok := base.(tRecord); ok {
			for name, t := range rec.fields {
				fields[name] = t
			}
		} else if isDefiniteNonRecord(base) {
			c.report(ex.Line, "the base of a record update must be a record, got %s", c.typeString(base))
		}
	}
	for _, f := range ex.Fields {
		fields[f.Name] = c.inferExpr(f.Value, env)
	}
	if ex.TypeName != "" {
		return c.checkStructLit(ex, fields)
	}
	return tRecord{fields: fields}
}

// checkStructLit types a typed record literal `Name { f: v, ... }` against
// its struct declaration: every field named must exist and its value must be
// of the declared type. The literal's type is the struct.
func (c *checker) checkStructLit(ex *ast.RecordLit, fields map[string]Type) Type {
	name := ex.TypeName
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	decl, ok := c.types[name]
	if !ok {
		return tRecord{fields: fields}
	}
	if _, isStruct := c.structDecls[name]; isStruct {
		// A generic struct's literal says its arguments by what it is built
		// from: `Pair { first: b, second: a }` is a `Pair[B, A]`. They are read
		// out of the field values first, because checking the fields against
		// the parameters themselves would compare a value against `A` -- a name
		// that stands for whatever the literal decides -- and report every
		// correct literal as a mismatch.
		binding := map[string]Type{}
		if len(c.typeParams[name]) > 0 {
			for _, f := range ex.Fields {
				if want, declared := decl.fields[f.Name]; declared {
					inferParams(want, fields[f.Name], binding)
				}
			}
			for _, p := range c.typeParams[name] {
				if _, bound := binding[p]; !bound {
					binding[p] = tUnknown{}
				}
			}
			decl = substParams(decl, binding).(tRecord)
			args := make([]Type, len(c.typeParams[name]))
			for i, p := range c.typeParams[name] {
				args[i] = binding[p]
			}
			decl.args = args
		}
		for _, f := range ex.Fields {
			want, declared := decl.fields[f.Name]
			if !declared {
				c.report(ex.Line, "%s has no field %q", name, f.Name)
				continue
			}
			what := fmt.Sprintf("field %q of %s", f.Name, name)
			if c.checkAssignable(ex.Line, what, want, fields[f.Name]) {
				c.fractionalLiteralAtInt(ex.Line, what, want, f.Value)
			}
		}
	}
	return decl
}

// isAdvisoryTypeAnno reports whether a single-name annotation (on a parameter,
// a return, or a `let`) is a type name rather than a unit. Only in a systems-
// mode file: a name like `I64`, `Bool` or `Str`, which no `unit` declaration
// introduced, is a type the dialect writes, and is advisory, since the bootstrap
// has no such type. A `USD` that a `unit` declaration introduced stays a unit
// and is checked; in numeric mode nothing is advisory, so an undeclared unit is
// still reported; and a compound annotation (`USD/year`) is always a unit.
func (c *checker) isAdvisoryTypeAnno(u *ast.UnitAnno) bool {
	if len(u.Factors) != 1 || u.Factors[0].Exp != 1 || c.units[u.Factors[0].Name] {
		return false
	}
	if c.systems {
		return true
	}
	// In numeric mode a bare name is ordinarily a unit, and an undeclared one is
	// a typo worth reporting. A name that is one of the dialect's own types is
	// not: `let b: Bool = true` is a correct numeric-mode program that ran fine,
	// and the checker rejected it with "unknown unit \"Bool\" (declare it with
	// `unit Bool`)" -- a false positive in the default mode, giving advice that
	// would make the program worse. The types exist at run time in both modes;
	// only the annotation syntax is documented as systems-mode.
	return isKnownTypeName(u.Factors[0].Name)
}

// annoHeadName is a type annotation's head: `Arr` for `Arr[I64]`, and the whole
// text when there is no argument list.
func annoHeadName(text string) string {
	if i := strings.IndexByte(text, '['); i >= 0 {
		return text[:i]
	}
	return text
}

// isKnownTypeName reports whether a bare annotation names one of the language's
// types rather than a unit of measure.
func isKnownTypeName(name string) bool {
	switch name {
	case "I64", "F64", "Bool", "Str", "Bytes", "Byte", "Unit", "Arr", "Dict",
		"Opt", "Res", "Tensor", "List":
		return true
	}
	return false
}

func (c *checker) inferUserCall(fn tFn, ex *ast.Call, argTypes []Type) Type {
	if len(fn.params) != len(argTypes) {
		c.report(ex.Line, "function expects %d argument(s), got %d", len(fn.params), len(argTypes))
		return tUnknown{}
	}
	// subst maps shape variables (n, k, ...) to the concrete sizes learned from
	// the arguments, so a variable used in several places must agree.
	subst := map[string]int{}
	scope := newEnv(fn.env)
	for i, p := range fn.params {
		switch {
		case p.Unit != nil:
			want := unitFromAnno(p.Unit)
			c.checkArgUnit(ex.Line, i, p.Name, want, argTypes[i])
			scope.define(p.Name, tTensor{dims: []int{}, unit: want})
		case p.TypeName != "":
			if expected, ok := c.types[p.TypeName]; ok {
				c.checkRecordArg(ex.Line, i, p.Name, expected, argTypes[i])
				scope.define(p.Name, expected) // field access in the body is typed
			} else if c.units[p.TypeName] {
				want := unitMap{p.TypeName: 1}
				c.checkArgUnit(ex.Line, i, p.Name, want, argTypes[i])
				scope.define(p.Name, tTensor{dims: []int{}, unit: want})
			} else {
				// A systems-mode type (I64, Str, Arr[I64], fn(I64) -> F64) or a
				// name from another module (cp.Caps). The argument is checked
				// against the declared type when the checker knows it, and the
				// parameter is bound as the declared type -- not as whatever the
				// argument happened to be, so a body indexing an Arr[I64] is not
				// judged by a scalar a test passed in. In numeric mode an unknown
				// type name is still reported.
				// A name that is one of the language's own types is a type in
				// either mode, so numeric mode does not report it as unknown --
				// the same correction the `let` annotation needed.
				if c.systems || isKnownTypeName(annoHeadName(p.TypeName)) {
					want := c.parseType(p.TypeName)
					what := fmt.Sprintf("argument %d (%q)", i+1, p.Name)
					if c.checkAssignable(ex.Line, what, want, argTypes[i]) && i < len(ex.Args) {
						c.fractionalLiteralAtInt(ex.Line, what, want, ex.Args[i])
					}
					scope.define(p.Name, want)
				} else {
					c.report(ex.Line, "unknown type %q on parameter %q", p.TypeName, p.Name)
					scope.define(p.Name, argTypes[i])
				}
			}
		case p.Shape != nil:
			if got, ok := argTypes[i].(tTensor); ok {
				c.unify(ex.Line, i, p.Name, p.Shape.Dims, got.dims, subst)
				scope.define(p.Name, got) // use the concrete arg shape in the body
			} else {
				scope.define(p.Name, tTensor{dims: p.Shape.ConcreteDims()})
			}
		default:
			scope.define(p.Name, argTypes[i])
		}
	}
	// Guard against infinite recursion during inference.
	if c.stack[fn.node] {
		if fn.ret != nil {
			return tTensor{dims: substitute(fn.ret.Dims, subst)}
		}
		if fn.retUnit != nil && !c.isAdvisoryTypeAnno(fn.retUnit) {
			return tTensor{dims: []int{}, unit: unitFromAnno(fn.retUnit)}
		}
		return tUnknown{}
	}
	c.stack[fn.node] = true
	// The callee's body is a fresh function scope for loop control, even when the
	// call itself sits inside a loop. See checkFnDef.
	savedDepth := c.loopDepth
	c.loopDepth = 0
	c.fnRet = append(c.fnRet, retTypeName(fn.retType, fn.retUnit))
	var bodyType Type
	if blk, ok := fn.body.(*ast.Block); ok {
		bodyType = c.inferBlock(blk, scope)
	} else {
		bodyType = c.inferExpr(fn.body, scope)
	}
	c.fnRet = c.fnRet[:len(c.fnRet)-1]
	c.loopDepth = savedDepth
	delete(c.stack, fn.node)
	c.checkBodyReturnType(ex.Line, "", retTypeName(fn.retType, fn.retUnit), bodyType, fn.body)

	if fn.ret != nil {
		expected := tTensor{dims: substitute(fn.ret.Dims, subst)}
		if got, ok := bodyType.(tTensor); ok {
			if fullyKnown(expected) && fullyKnown(got) && !shapeMatch(expected, got) {
				c.report(ex.Line, "function returns %s but its signature declares %s",
					dimsString(got), dimsString(expected))
			}
			expected.unit = got.unit // carry the body's unit through a shape return
		}
		return expected
	}
	if fn.retUnit != nil && !c.isAdvisoryTypeAnno(fn.retUnit) {
		c.checkReturnUnit(ex.Line, "", fn.retUnit, bodyType)
		return tTensor{dims: []int{}, unit: unitFromAnno(fn.retUnit)}
	}
	// A declared return type is what the call produces, whatever walking the
	// body concluded. That matters for a block body ending in `return`, which
	// evaluates to Unit as an expression: the body of
	//
	//	fn read(p: Str) -> Res[Str, Str] { return read_file(p) }
	//
	// infers Unit, and taking that as the call's type made `read(p)?` report
	// that `?` needs a Res -- on the commonest shape the feature has. The
	// signature is the contract; the body is checked against it separately in
	// checkBodyReturnType and at each `return`.
	if declared := c.parseType(retTypeName(fn.retType, fn.retUnit)); !isUnknownType(declared) {
		return declared
	}
	return bodyType
}

func isUnknownType(t Type) bool {
	_, ok := t.(tUnknown)
	return ok
}

// checkArgUnit reports if a scalar argument's unit disagrees with a unit
// parameter's declaration.
func (c *checker) checkArgUnit(line, argIdx int, name string, want unitMap, arg Type) {
	// A dimensionless value (a bare literal, or a computed quantity that carries
	// no unit) is adopted into the parameter's unit; only a value that already
	// carries a conflicting unit is an error.
	if got, ok := arg.(tTensor); ok && len(got.unit) != 0 && !unitEqual(got.unit, want) {
		c.report(line, "argument %d (%q) has unit %s but the signature expects %s",
			argIdx+1, name, unitString(got.unit), unitString(want))
	}
}

// checkRecordArg verifies a record argument against a declared record type:
// every declared field must be present with a matching shape.
func (c *checker) checkRecordArg(line, argIdx int, name string, expected tRecord, arg Type) {
	rec, ok := arg.(tRecord)
	if !ok {
		if _, isUnknown := arg.(tUnknown); !isUnknown {
			c.report(line, "argument %d (%q) should be a record", argIdx+1, name)
		}
		return
	}
	for field, ft := range expected.fields {
		got, present := rec.fields[field]
		if !present {
			c.report(line, "argument %d (%q) is missing field %q", argIdx+1, name, field)
			continue
		}
		want, wok := ft.(tTensor)
		gt, gok := got.(tTensor)
		if wok && gok && fullyKnown(want) && fullyKnown(gt) && !shapeMatch(want, gt) {
			c.report(line, "argument %d (%q) field %q is %s but the type declares %s",
				argIdx+1, name, field, dimsString(gt), dimsString(want))
		}
	}
}

// unify checks an argument's shape against a parameter's annotation, recording
// shape variables in subst and reporting any definite mismatch.
func (c *checker) unify(line, argIdx int, name string, pattern []ast.Dim, actual []int, subst map[string]int) {
	if len(pattern) != len(actual) {
		// Only a definite mismatch when the argument's rank is fully known,
		// which it is here (actual comes from a tTensor).
		c.report(line, "argument %d (%q) has rank %d but the signature expects rank %d",
			argIdx+1, name, len(actual), len(pattern))
		return
	}
	for i, pd := range pattern {
		ad := actual[i]
		switch {
		case pd.IsConcrete():
			if ad >= 0 && ad != pd.Size {
				c.report(line, "argument %d (%q) axis %d is %d but the signature expects %d",
					argIdx+1, name, i, ad, pd.Size)
			}
		case pd.Var != "":
			if ad >= 0 {
				if prev, ok := subst[pd.Var]; ok && prev != ad {
					c.report(line, "shape variable %q is %d elsewhere but %d in argument %d",
						pd.Var, prev, ad, argIdx+1)
				} else {
					subst[pd.Var] = ad
				}
			}
		}
	}
}

// substitute resolves an annotation's dims using known shape variables,
// leaving -1 for anything still unknown.
func substitute(dims []ast.Dim, subst map[string]int) []int {
	out := make([]int, len(dims))
	for i, d := range dims {
		switch {
		case d.IsConcrete():
			out[i] = d.Size
		case d.Var != "":
			if v, ok := subst[d.Var]; ok {
				out[i] = v
			} else {
				out[i] = -1
			}
		default:
			out[i] = -1
		}
	}
	return out
}

// inferBuiltinCall types a builtin call. ctorDType is the dtype a constructor's
// trailing name asked for, or dtUnknown; a constructor without one builds f64,
// the documented default (docs/dtypes.md).
func (c *checker) inferBuiltinCall(name string, ex *ast.Call, argTypes []Type, dt tensor.DType) Type {
	ctorDType := tensor.DTF64
	if dtypeKnown(dt) {
		ctorDType = dt
	}
	// Every builtin registered with a fixed arity rejects the wrong argument
	// count at runtime; that is decidable here, and a call reaches this function
	// only when the name is the real builtin (a same-named user function resolves
	// to a tFn and never arrives), so a shadow keeps its own arity. Variadic
	// builtins -- reshape, transpose, the reductions, conv2d, sort, and the rest
	// that take an optional axis or a run of dimensions -- are absent from the
	// table and left to their per-shape cases.
	if arity, ok := builtinArity[name]; ok && len(ex.Args) != arity {
		c.report(ex.Line, "%s expects %d argument(s), got %d", name, arity, len(ex.Args))
		return tUnknown{}
	}
	// A builtin that works on tensors, handed something that is definitely not
	// one. The runtime rejects these and the checker knew the type; it was most
	// visible at `[]`, an empty list reaching sum, mean, exp or matmul.
	if tensorOnlyBuiltins[name] && len(argTypes) > 0 {
		// Only the operands, not the trailing arguments: reshape and
		// broadcast_to take their shape as a list of dimensions, and an axis or
		// a count is an ordinary number. Argument 0 is the tensor in every one
		// of these, and matmul's second operand is one too.
		n := 1
		if name == "matmul" || name == "dot" || name == "linear" {
			n = 2
		}
		for i := 0; i < n && i < len(argTypes); i++ {
			if isDefiniteNonTensor(argTypes[i]) {
				c.report(ex.Line, "%s expects a tensor, but argument %d is %s", name, i+1, c.typeString(argTypes[i]))
				return tUnknown{}
			}
		}
	}
	switch name {
	case "stop_grad":
		// Shape, unit and dtype ride through: it changes what the value
		// remembers, not what it is.
		if len(argTypes) == 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				return t
			}
		}
		return tUnknown{}
	case "relu":
		// Shape, unit and dtype preserved.
		if len(argTypes) >= 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				return t
			}
		}
		return tUnknown{}
	case "abs":
		// abs preserves shape and unit, but not an integer dtype; see
		// floatResultDType for why that is unknown here rather than f32.
		if len(argTypes) >= 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				return t.withDType(floatResultDType(t.dtype()))
			}
		}
		return tUnknown{}
	case "softmax":
		// Shape and unit preserved, but softmax normalises over one axis and the
		// runtime rejects an out-of-range one, so a constant axis is checked here
		// the same way the reductions check theirs.
		return c.axisPreserveResult(ex, argTypes, 1)
	case "exp", "log", "log1p", "expm1", "sin", "cos", "tanh", "sigmoid":
		// Transcendental functions need a dimensionless argument.
		if len(argTypes) >= 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				if len(t.unit) != 0 {
					c.report(ex.Line, "%s expects a dimensionless argument but got unit %s", name, unitString(t.unit))
				}
				// result is dimensionless
				return tTensor{dims: t.dims}.withDType(floatResultDType(t.dtype()))
			}
		}
		return tUnknown{}
	case "sqrt":
		if len(argTypes) >= 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				u, ok := unitSqrt(t.unit)
				if !ok {
					c.report(ex.Line, "sqrt of unit %s is not a whole unit", unitString(t.unit))
				}
				return tTensor{dims: t.dims, unit: u}.withDType(floatResultDType(t.dtype()))
			}
		}
		return tUnknown{}
	case "square":
		if len(argTypes) >= 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				// A square is a product of the value with itself, so it keeps an
				// integer dtype rather than becoming a float.
				return tTensor{dims: t.dims, unit: unitPow(t.unit, 2)}.withDType(t.dtype())
			}
		}
		return tUnknown{}
	case "int":
		return tTensor{dims: []int{}}
	case "item":
		// item reads the sole element out, so a tensor with a statically known
		// count other than one is the runtime error, caught here.
		if len(argTypes) >= 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				if n, ok := elementCount(t.dims); ok && n != 1 {
					c.report(ex.Line, "item expects a single-element tensor, got %s", dimsString(t))
				}
			}
		}
		return scalar()
	case "len":
		return scalar()
	case "sum", "mean", "max", "min", "prod", "median":
		return c.reduceResult(name, ex, argTypes)
	case "argmax", "argmin", "logsumexp":
		return c.axisReduceResult(ex, argTypes)
	case "maximum", "minimum", "greater", "less", "greater_equal", "less_equal", "equal":
		return c.broadcastTwo(ex, argTypes)
	case "where":
		return c.broadcastWhere(ex, argTypes)
	case "clip":
		if len(argTypes) >= 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				return t
			}
		}
		return tUnknown{}
	case "reshape", "broadcast_to":
		if len(ex.Args) >= 2 {
			if dims, ok := constShape(ex.Args[1:]); ok {
				// A reshape has to preserve the element count, and when both
				// sides are known that is arithmetic the checker can do. This
				// is the second most common shape mistake after a bad matmul
				// and it used to reach the runtime untouched.
				if name == "reshape" {
					if in, ok := argTypes[0].(tTensor); ok {
						if from, ok := elementCount(in.dims); ok {
							// A negative target dimension is not an inferred axis:
							// twill has no -1 reshape, so it is a certain runtime
							// error the checker can name, rather than the silent
							// pass elementCount(dims) gives it (it bails on any
							// negative). Saying why spares the numpy reflex.
							neg := false
							for _, d := range dims {
								if d < 0 {
									neg = true
								}
							}
							if neg {
								c.report(ex.Line,
									"reshape: cannot fit %d elements into %s; twill has no -1 dimension inference",
									from, dimsString(tTensor{dims: dims}))
							} else if to, ok := elementCount(dims); ok && from != to {
								c.report(ex.Line,
									"reshape changes the number of elements: %s has %d, %s needs %d",
									dimsString(tTensor{dims: in.dims}), from, dimsString(tTensor{dims: dims}), to)
							}
						}
					}
				}
				if name == "broadcast_to" {
					// A source shape broadcasts to the target when, right-aligned,
					// each of its axes is 1 or equals the target's, and it has no
					// more axes than the target. Both known here means the runtime
					// error is one the checker can raise instead.
					if in, ok := argTypes[0].(tTensor); ok {
						if len(in.dims) > len(dims) {
							c.report(ex.Line, "cannot broadcast %s to %s: fewer axes in target",
								dimsString(tTensor{dims: in.dims}), dimsString(tTensor{dims: dims}))
						} else if !dimsBroadcastable(in.dims, dims) {
							c.report(ex.Line, "cannot broadcast %s to %s",
								dimsString(tTensor{dims: in.dims}), dimsString(tTensor{dims: dims}))
						}
					}
				}
				// Rearrangement: the element type is untouched.
				return tTensor{dims: dims}.withDType(argDType(argTypes, 0))
			}
		}
		return tUnknown{}
	case "einsum":
		return c.inferEinsum(ex, argTypes)
	case "concat":
		return c.inferConcat(ex, argTypes)
	case "fold":
		return tUnknown{}
	case "append", "enumerate", "columns":
		return tList{}
	case "split":
		// split(t, n | sizes[, axis]) is concat's inverse. When the axis length,
		// the piece count or the explicit sizes are all constant, the same
		// mismatches the runtime raises are knowable here: an axis out of range, a
		// count that does not divide the axis evenly, or sizes that do not sum to
		// it. Anything dynamic is left as an unknown-length list.
		if t, ok := argTypes[0].(tTensor); ok && len(t.dims) > 0 {
			axis := 0
			if len(ex.Args) >= 3 {
				a, ok := constInt(ex.Args[2])
				if !ok {
					return tList{}
				}
				axis = a
			}
			if axis < 0 {
				axis += len(t.dims)
			}
			if axis < 0 || axis >= len(t.dims) {
				c.reportAxis(ex, t)
				return tUnknown{}
			}
			L := t.dims[axis]
			if len(ex.Args) >= 2 {
				// A list literal, or a list(...) call, means explicit sizes; a bare
				// integer means that many equal pieces.
				if sizes, ok := constSplitSizes(ex.Args[1]); ok {
					if len(sizes) == 0 {
						c.report(ex.Line, "split: need at least one piece")
						return tUnknown{}
					}
					total := 0
					for _, s := range sizes {
						if s < 0 {
							c.report(ex.Line, "split: negative size %d", s)
							return tUnknown{}
						}
						total += s
					}
					if L >= 0 && total != L {
						c.report(ex.Line, "split: sizes sum to %d but axis %d has length %d", total, axis, L)
						return tUnknown{}
					}
				} else if n, ok := constInt(ex.Args[1]); ok {
					if n <= 0 {
						c.report(ex.Line, "split: piece count must be positive, got %d", n)
						return tUnknown{}
					}
					if L >= 0 && L%n != 0 {
						c.report(ex.Line, "split: axis %d has length %d, which %d does not divide evenly", axis, L, n)
						return tUnknown{}
					}
				}
			}
		}
		return tList{}
	case "write_frame":
		return tUnit{}
	case "scalar":
		// scalar(x) really is f64 (tensor.tw), unlike a bare literal.
		return scalar().withDType(tensor.DTF64)
	case "pow":
		if len(argTypes) >= 1 {
			if t, ok := argTypes[0].(tTensor); ok {
				if len(t.unit) == 0 {
					return t
				}
				if len(ex.Args) >= 2 {
					if k, ok := constInt(ex.Args[1]); ok {
						return tTensor{dims: t.dims, unit: unitPow(t.unit, k)}
					}
				}
				c.report(ex.Line, "pow of a quantity with unit %s needs a constant integer exponent", unitString(t.unit))
				return tTensor{dims: t.dims}
			}
		}
		return tUnknown{}
	case "matmul", "dot":
		if len(argTypes) == 2 {
			a, aok := argTypes[0].(tTensor)
			b, bok := argTypes[1].(tTensor)
			if aok && bok {
				res, msg := matmulResult(a, b)
				if msg != "" {
					c.report(ex.Line, "%s", msg)
					return tUnknown{}
				}
				return withUnit(res, unitMul(a.unit, b.unit))
			}
		}
		return tUnknown{}
	case "linear":
		if len(argTypes) == 2 {
			a, aok := argTypes[0].(tTensor)
			w, wok := argTypes[1].(tTensor)
			if aok && wok {
				res, msg := linearResult(a, w)
				if msg != "" {
					c.report(ex.Line, "%s", msg)
					return tUnknown{}
				}
				return withUnit(res, unitMul(a.unit, w.unit))
			}
		}
		return tUnknown{}
	case "quantize":
		// Quantisation packs a 2-D weight; any other rank is the runtime error,
		// and a tensor argument always carries its rank here.
		if len(argTypes) >= 1 {
			if w, ok := argTypes[0].(tTensor); ok && len(w.dims) != 2 {
				c.report(ex.Line, "quantize expects a 2-D weight, got rank %d", len(w.dims))
				return tUnknown{}
			}
		}
		// A quantised weight is an opaque frozen value, not a shaped tensor; it
		// only ever flows into `linear`, whose quantised branch needs no shape
		// from the checker. Typing it Unknown keeps it from being used in tensor
		// arithmetic by accident while not inventing a type the language lacks.
		return tUnknown{}
	case "nbytes":
		return scalar()
	case "dtype":
		// The element type as its surface name, a string.
		return tStr{}
	case "shape":
		return tList{}
	case "transpose":
		if t, ok := argTypes[0].(tTensor); ok {
			if len(ex.Args) == 1 {
				rev := make([]int, len(t.dims))
				for i := range t.dims {
					rev[i] = t.dims[len(t.dims)-1-i]
				}
				return tTensor{dims: rev}.withDType(t.dtype())
			}
			axes := make([]int, 0, len(ex.Args)-1)
			for _, a := range ex.Args[1:] {
				ax, ok := constInt(a)
				if !ok {
					return tUnknown{}
				}
				axes = append(axes, ax)
			}
			if len(axes) == len(t.dims) {
				perm := make([]int, len(axes))
				seen := make([]bool, len(t.dims))
				for i, ax := range axes {
					if ax < 0 || ax >= len(t.dims) {
						// An axis outside the rank is the same mistake every other
						// axis-taking builtin reports through reportAxis; transpose
						// stayed silent, so a permutation naming a nonexistent axis
						// passed the check and failed only at run time.
						c.reportAxis(ex, t)
						return tUnknown{}
					}
					if seen[ax] {
						// A repeated axis leaves another unnamed, so it is not a
						// permutation; the runtime rejects it word for word.
						c.report(ex.Line, "transpose: invalid axis permutation %s", intsBracketed(axes))
						return tUnknown{}
					}
					seen[ax] = true
					perm[i] = t.dims[ax]
				}
				return tTensor{dims: perm}.withDType(t.dtype())
			} else if len(t.dims) > 0 {
				// A permutation must name every axis exactly once, so a count
				// other than the rank is a certain error the runtime raises.
				c.report(ex.Line, "transpose: got %d axes for a rank-%d tensor", len(axes), len(t.dims))
				return tUnknown{}
			}
		}
		return tUnknown{}
	case "zeros", "ones", "randn", "rand":
		if dims, ok := constShape(ex.Args); ok {
			if c.reportNegDim(ex.Line, name, dims) {
				return tUnknown{}
			}
			return tTensor{dims: dims}.withDType(ctorDType)
		}
		return tUnknown{}
	case "fill":
		if len(ex.Args) >= 1 {
			if dims, ok := constShape(ex.Args[1:]); ok {
				if c.reportNegDim(ex.Line, name, dims) {
					return tUnknown{}
				}
				return tTensor{dims: dims}.withDType(ctorDType)
			}
		}
		return tUnknown{}
	case "eye":
		if len(ex.Args) == 1 {
			if n, ok := constInt(ex.Args[0]); ok {
				if c.reportNegDim(ex.Line, name, []int{n}) {
					return tUnknown{}
				}
				return tTensor{dims: []int{n, n}}.withDType(ctorDType)
			}
		}
		return tUnknown{}
	case "linspace":
		// A 1-D tensor whose length is the third argument, known when it is a
		// literal.
		if len(ex.Args) == 3 {
			if n, ok := constInt(ex.Args[2]); ok {
				if c.reportNegDim(ex.Line, name, []int{n}) {
					return tUnknown{}
				}
				return tTensor{dims: []int{n}}.withDType(ctorDType)
			}
		}
		return tTensor{dims: []int{-1}}.withDType(ctorDType)
	case "arange":
		// A 1-D tensor whose length depends on the start, stop and step values,
		// which the checker does not evaluate, so the rank is known and the size
		// is not.
		return tTensor{dims: []int{-1}}.withDType(ctorDType)
	case "range", "list", "map", "zip", "permutation":
		return tList{}
	case "print":
		return tUnit{}
	case "save":
		return tUnit{}
	case "load":
		// The loaded value's type depends on the file, so leave it unknown.
		return tUnknown{}
	case "str":
		return tStr{}
	case "tensor":
		// A tensor built from a literal has a shape the checker can read, and
		// reading it is most of what makes the checker worth running. The
		// mistake people actually make is `tensor([[1, 2], [3, 4]]) @
		// tensor([[1, 2, 3]])`, and it used to pass, because this returned
		// unknown for every argument. Anything that is not a literal still does.
		if len(argTypes) == 1 {
			// A tensor literal was already given a shape on the way in, and
			// `tensor(...)` around it was throwing that away.
			if t, ok := argTypes[0].(tTensor); ok {
				return t
			}
			if dims, ok := listDims(argTypes[0]); ok {
				return tTensor{dims: dims}
			}
		}
		return tUnknown{}
	case "grad", "grads", "value_and_grad", "jacobian", "hessian", "jvp", "vjp", "hvp":
		// These return values whose shape depends on runtime data; treat the
		// result as unknown so downstream code is not falsely flagged.
		return tUnknown{}
	case "floor", "ceil", "round":
		// Elementwise rounding preserves the input's shape.
		if t, ok := argTypes[0].(tTensor); ok {
			return tTensor{dims: t.dims}
		}
		return tUnknown{}
	case "cumsum", "cumprod", "cummax", "cummin", "flip", "sort":
		// A cumulative scan, a reversal, or a sort preserve the input's shape and
		// take the axis in the second argument; an out-of-range one is the error
		// the runtime raises. (sort of a list is handled by returning unknown
		// here, since its argument is not a tensor.)
		return c.axisPreserveResult(ex, argTypes, 1)
	case "topk", "argtopk":
		// Take the k largest (or smallest) values along an axis. The result has
		// the same shape but with that axis shortened to k, so a k larger than the
		// axis -- or a non-positive k, or an out-of-range axis -- is the runtime
		// error, all knowable here when k, the axis and the dims are constant.
		// The axis defaults to the last one; smallest is a flag we ignore.
		if t, ok := argTypes[0].(tTensor); ok && len(t.dims) > 0 {
			axis := len(t.dims) - 1
			if len(ex.Args) > 2 {
				a, ok := constInt(ex.Args[2])
				if !ok {
					return tUnknown{}
				}
				axis = a
			}
			if axis < 0 {
				axis += len(t.dims)
			}
			if axis < 0 || axis >= len(t.dims) {
				c.reportAxis(ex, t)
				return tUnknown{}
			}
			if len(ex.Args) > 1 {
				if k, ok := constInt(ex.Args[1]); ok {
					L := t.dims[axis]
					if k <= 0 {
						c.report(ex.Line, "%s: k must be positive, got %d", name, k)
						return tUnknown{}
					}
					if L >= 0 && k > L {
						c.report(ex.Line, "%s: k is %d but axis %d has length %d", name, k, axis, L)
						return tUnknown{}
					}
					dims := make([]int, len(t.dims))
					copy(dims, t.dims)
					dims[axis] = k
					return tTensor{dims: dims}
				}
			}
		}
		return tUnknown{}
	case "roll":
		// roll puts the shift first, so its axis is the third argument.
		return c.axisPreserveResult(ex, argTypes, 2)
	case "diff":
		// diff takes successive differences along an axis (second argument),
		// shrinking that axis by one. An out-of-range axis is the runtime error;
		// it was previously unchecked while flip, roll and the scans were not.
		if t, ok := argTypes[0].(tTensor); ok && len(t.dims) > 0 {
			// The axis defaults to the last one; only a constant is foldable.
			a := len(t.dims) - 1
			if len(ex.Args) > 1 {
				ax, ok := constInt(ex.Args[1])
				if !ok {
					return tUnknown{}
				}
				a = ax
			}
			if a < 0 {
				a += len(t.dims)
			}
			if a < 0 || a >= len(t.dims) {
				c.reportAxis(ex, t)
				return tUnknown{}
			}
			// Successive differences need at least two elements along the axis;
			// one leaves nothing to subtract, the error the runtime raises.
			if L := t.dims[a]; L >= 0 && L < 2 {
				c.report(ex.Line, "diff needs at least 2 elements along axis %d, got %d", a, L)
				return tUnknown{}
			}
			dims := make([]int, len(t.dims))
			copy(dims, t.dims)
			if dims[a] > 0 {
				dims[a]--
			}
			return tTensor{dims: dims}
		}
		return tUnknown{}
	case "gather":
		// The index selects rows, so it must be a 1-D list of positions; a rank-2
		// or higher index is the runtime error, and its rank is known here.
		if len(argTypes) >= 2 {
			if idx, ok := argTypes[1].(tTensor); ok && len(idx.dims) > 1 {
				c.report(ex.Line, "gather expects a 1-D tensor or list of indices, got %s", dimsString(idx))
				return tUnknown{}
			}
		}
		// Selecting rows keeps the trailing dims. The row count is usually dynamic,
		// but a constant index list pins it -- and lets every position be checked
		// against the first dimension, the out-of-range error the runtime raises.
		if t, ok := argTypes[0].(tTensor); ok && len(t.dims) >= 1 {
			dims := make([]int, len(t.dims))
			dims[0] = -1
			copy(dims[1:], t.dims[1:])
			if len(ex.Args) >= 2 {
				if idx, ok := constIndexList(ex.Args[1]); ok {
					if n := t.dims[0]; n >= 0 {
						for _, ix := range idx {
							if ix < 0 || ix >= n {
								c.report(ex.Line, "gather: index %d out of range [0, %d)", ix, n)
								return tUnknown{}
							}
						}
					}
					dims[0] = len(idx)
				}
			}
			return tTensor{dims: dims}
		}
		return tUnknown{}
	case "conv2d":
		return c.convResult(ex, argTypes)
	case "maxpool2d":
		// [C, H, W] -> [C, H/k, W/k]; only the channel count is statically known.
		// The arity (exactly the tensor and the window) is settled by the fixed
		// table above; here a non-rank-3 input is the error the runtime raises.
		if t, ok := argTypes[0].(tTensor); ok {
			if len(t.dims) != 3 {
				c.report(ex.Line, "maxpool2d: input must be [channels, height, width], got %s", dimsString(t))
			} else {
				dims := []int{t.dims[0], -1, -1}
				// A constant window settles the pooled height and width, and the
				// two runtime rejections: a window below one, or one so large it
				// pools nothing (h/k or w/k rounds to zero).
				if k, ok := constInt(ex.Args[1]); ok {
					if k < 1 {
						c.report(ex.Line, "maxpool2d: window must be >= 1, got %d", k)
						return tUnknown{}
					}
					h, w := t.dims[1], t.dims[2]
					if (h >= 0 && h/k == 0) || (w >= 0 && w/k == 0) {
						c.report(ex.Line, "maxpool2d: window %d is larger than input %dx%d", k, h, w)
						return tUnknown{}
					}
					if h >= 0 {
						dims[1] = h / k
					}
					if w >= 0 {
						dims[2] = w / k
					}
				}
				return tTensor{dims: dims}
			}
		}
		return tTensor{dims: []int{-1, -1, -1}}
	case "gbm_fit":
		// An opaque model value.
		return tUnknown{}
	case "gbm_predict":
		// One score per row of the feature matrix (the second argument).
		if len(argTypes) == 2 {
			if t, ok := argTypes[1].(tTensor); ok && len(t.dims) >= 1 {
				return tTensor{dims: []int{t.dims[0]}}
			}
		}
		return tTensor{dims: []int{-1}}
	}
	return tUnknown{}
}

// inferEinsum validates a literal einsum spec and, when the input shapes are
// known, resolves the output shape.
func (c *checker) inferEinsum(ex *ast.Call, argTypes []Type) Type {
	if len(ex.Args) < 2 {
		return tUnknown{}
	}
	lit, ok := ex.Args[0].(*ast.StringLit)
	if !ok {
		return tUnknown{}
	}
	inSubs, outSub, err := tensor.ParseEinsum(lit.Value, len(ex.Args)-1)
	if err != nil {
		c.report(ex.Line, "%s", err.Error())
		return tUnknown{}
	}
	dims := make([][]int, len(inSubs))
	for i := range inSubs {
		t, ok := argTypes[i+1].(tTensor)
		if !ok {
			// Spec is valid but an operand's shape is unknown: known rank only.
			od := make([]int, len(outSub))
			for j := range od {
				od[j] = -1
			}
			return tTensor{dims: od}
		}
		dims[i] = t.dims
	}
	out, err := tensor.EinsumOutputDims(inSubs, outSub, dims)
	if err != nil {
		c.report(ex.Line, "%s", err.Error())
		return tUnknown{}
	}
	return tTensor{dims: out}
}

// convResult infers the output shape of conv2d: input [Cin, H, W] and weight
// [Cout, Cin, KH, KW] give [Cout, H-KH+1, W-KW+1], with any unknown dimension
// left as -1. The three shape contracts the runtime enforces -- a rank-3 input,
// a rank-4 weight, and matching channel counts -- are the mistakes people make
// wiring a net together, so a statically knowable violation is reported here
// with the message the runtime would have raised.
func (c *checker) convResult(ex *ast.Call, argTypes []Type) Type {
	dims := []int{-1, -1, -1}
	if len(argTypes) != 2 {
		return tTensor{dims: dims}
	}
	in, okIn := argTypes[0].(tTensor)
	w, okW := argTypes[1].(tTensor)
	if okIn && len(in.dims) != 3 {
		c.report(ex.Line, "conv2d: input must be [channels, height, width], got %s", dimsString(in))
	}
	if okW && len(w.dims) != 4 {
		c.report(ex.Line, "conv2d: weight must be [out, in, kh, kw], got %s", dimsString(w))
	}
	if okIn && len(in.dims) == 3 && okW && len(w.dims) == 4 {
		if in.dims[0] >= 0 && w.dims[1] >= 0 && in.dims[0] != w.dims[1] {
			c.report(ex.Line, "conv2d: input has %d channels but weight expects %d", in.dims[0], w.dims[1])
		}
	}
	if okW && len(w.dims) == 4 {
		dims[0] = w.dims[0] // Cout
		if okIn && len(in.dims) == 3 {
			// A kernel wider or taller than the input gives H-KH+1 < 1: an empty
			// output, which the runtime rejects. When both spatial pairs are known
			// this is certain, so it is named here rather than left as the silent
			// negative dimension the arithmetic below would otherwise return.
			if in.dims[1] >= 0 && in.dims[2] >= 0 && w.dims[2] >= 0 && w.dims[3] >= 0 &&
				(w.dims[2] > in.dims[1] || w.dims[3] > in.dims[2]) {
				c.report(ex.Line, "conv2d: kernel %dx%d is larger than input %dx%d",
					w.dims[2], w.dims[3], in.dims[1], in.dims[2])
			}
			if in.dims[1] >= 0 && w.dims[2] >= 0 {
				dims[1] = in.dims[1] - w.dims[2] + 1
			}
			if in.dims[2] >= 0 && w.dims[3] >= 0 {
				dims[2] = in.dims[2] - w.dims[3] + 1
			}
		}
	}
	return tTensor{dims: dims}
}

// reduceResult handles sum/mean/max/min: no axis reduces to a scalar; a
// constant axis over a known shape removes that dimension.
// reportAxis names an axis that does not exist on the tensor it was given.
//
// This was detected and then swallowed: both reduction paths already worked out
// that the axis was out of range and returned an unknown type, which silences
// everything downstream as well. Detecting a mistake and saying nothing is the
// worst of the three options.
// inferConcat works out the shape of a concatenation, and reports the pieces
// that cannot be joined.
//
// Worth doing twice over: the mismatch itself reached the runtime, and the
// unknown type it used to return blinded everything downstream of it as well, so
// a whole pipeline built on a concat was unchecked from that point on.
func (c *checker) inferConcat(ex *ast.Call, argTypes []Type) Type {
	if len(argTypes) < 1 {
		return tUnknown{}
	}
	// A written-out empty list has nothing to join, which the runtime rejects
	// with "need at least one tensor". Only the literal is certain -- a list
	// whose length is dynamic may well be non-empty at runtime -- so this keys
	// on the syntax, not the inferred element count.
	if len(ex.Args) >= 1 {
		if ll, ok := ex.Args[0].(*ast.ListLit); ok && len(ll.Elements) == 0 {
			c.report(ex.Line, "concat: need at least one tensor")
			return tUnknown{}
		}
	}
	lst, ok := argTypes[0].(tList)
	if !ok || len(lst.elems) == 0 {
		return tUnknown{}
	}

	// Every piece has to be a tensor whose shape is known. One that is not
	// makes the result unknowable rather than wrong, so this says nothing.
	parts := make([]tTensor, 0, len(lst.elems))
	for _, el := range lst.elems {
		t, ok := el.(tTensor)
		if !ok || len(t.dims) == 0 {
			return tUnknown{}
		}
		for _, d := range t.dims {
			if d < 0 {
				return tUnknown{}
			}
		}
		parts = append(parts, t)
	}

	rank := len(parts[0].dims)
	for _, p := range parts[1:] {
		if len(p.dims) != rank {
			c.report(ex.Line, "concat needs pieces of the same rank: %s has %d, %s has %d",
				dimsString(parts[0]), rank, dimsString(p), len(p.dims))
			return tUnknown{}
		}
	}

	axis := 0
	if len(ex.Args) >= 2 {
		ax, ok := constInt(ex.Args[1])
		if !ok {
			return tUnknown{}
		}
		axis = ax
	}
	if axis < 0 {
		axis += rank
	}
	if axis < 0 || axis >= rank {
		c.reportAxis(ex, parts[0])
		return tUnknown{}
	}

	// Every axis but the joined one has to agree. This is the runtime's rule and
	// its wording, so a reader who has seen one message recognises the other.
	total := 0
	for _, p := range parts {
		total += p.dims[axis]
		for i := range p.dims {
			if i != axis && p.dims[i] != parts[0].dims[i] {
				c.report(ex.Line, "concat: shapes differ on axis %d: %s and %s",
					i, dimsString(parts[0]), dimsString(p))
				return tUnknown{}
			}
		}
	}

	dims := append([]int{}, parts[0].dims...)
	dims[axis] = total
	return tTensor{dims: dims, unit: parts[0].unit}
}

func (c *checker) reportAxis(ex *ast.Call, t tTensor) {
	// A scalar has no axes at all, so the "numbered 0 to -1" the general form
	// would produce is nonsense. It is also the case the axis checks used to
	// skip entirely -- each guarded on rank > 0 before validating -- so
	// `sum(1.0, 0)` reached the runtime.
	if len(t.dims) == 0 {
		c.report(ex.Line, "a scalar has no axes, so there is no axis %s to reduce over",
			axisText(ex))
		return
	}
	c.report(ex.Line, "axis out of range for %s: it has %d %s, numbered 0 to %d",
		dimsString(t), len(t.dims), plural(len(t.dims), "axis", "axes"), len(t.dims)-1)
}

// axisText renders the axis argument as written, for the scalar diagnostic.
func axisText(ex *ast.Call) string {
	if len(ex.Args) > 1 {
		if n, ok := constInt(ex.Args[1]); ok {
			return fmt.Sprintf("%d", n)
		}
	}
	return "there"
}

// rank0Axis reports an axis given for a scalar, which has none. Returns true
// when it reported, so the caller stops.
func (c *checker) rank0Axis(ex *ast.Call, argTypes []Type) bool {
	if len(argTypes) < 2 {
		return false
	}
	t, ok := argTypes[0].(tTensor)
	if !ok || len(t.dims) != 0 {
		return false
	}
	if _, isConst := constInt(ex.Args[1]); !isConst {
		return false
	}
	c.reportAxis(ex, t)
	return true
}

// dimsBroadcastable reports whether src, right-aligned against target, can
// broadcast to it: every known axis pair is equal or the source's is 1. The
// caller has already established that src has no more axes than target. An
// unknown dimension on either side is not a certain mismatch and is skipped.
func dimsBroadcastable(src, target []int) bool {
	for i := 1; i <= len(src); i++ {
		sd := src[len(src)-i]
		td := target[len(target)-i]
		if sd < 0 || td < 0 {
			continue
		}
		if sd != td && sd != 1 {
			return false
		}
	}
	return true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (c *checker) reduceResult(name string, ex *ast.Call, argTypes []Type) Type {
	var u unitMap // reductions preserve the input's unit
	rdt := dtUnknown
	if t, ok := argTypes[0].(tTensor); ok {
		u = t.unit
		rdt = t.dtype()
	}
	// A sum, a max or a prod stores the input's dtype. A mean or a median need
	// not be an integer (tensor.reduceResultDType), and floatResultDType says
	// why an integer input degrades to unknown rather than claiming f32.
	if name == "mean" || name == "median" {
		rdt = floatResultDType(rdt)
	}
	if len(argTypes) == 1 {
		return tTensor{dims: []int{}, unit: u}.withDType(rdt)
	}
	if len(argTypes) == 2 {
		if c.rank0Axis(ex, argTypes) {
			return tUnknown{}
		}
		if t, ok := argTypes[0].(tTensor); ok && len(t.dims) > 0 {
			if ax, ok := constInt(ex.Args[1]); ok {
				if ax < 0 {
					ax += len(t.dims)
				}
				if ax >= 0 && ax < len(t.dims) {
					return tTensor{dims: removeDim(t.dims, ax), unit: u}.withDType(rdt)
				}
				c.reportAxis(ex, t)
			}
		}
	}
	return tUnknown{}
}

// axisPreserveResult validates the optional axis of a shape-preserving axis op
// and returns the input shape and unit unchanged. axisArg is the argument index
// the axis is passed in -- 1 for softmax/flip/cumsum, 2 for roll, which puts the
// shift first. A constant axis outside the rank is the error the runtime raises;
// a non-constant axis, or an unknown input, is left alone.
func (c *checker) axisPreserveResult(ex *ast.Call, argTypes []Type, axisArg int) Type {
	if len(argTypes) == 0 {
		return tUnknown{}
	}
	t, ok := argTypes[0].(tTensor)
	if !ok {
		return tUnknown{}
	}
	if len(ex.Args) > axisArg && len(t.dims) > 0 {
		if ax, ok := constInt(ex.Args[axisArg]); ok {
			if ax < 0 {
				ax += len(t.dims)
			}
			if ax < 0 || ax >= len(t.dims) {
				c.reportAxis(ex, t)
			}
		}
	}
	return t
}

// axisReduceResult handles argmax/logsumexp, which always reduce one axis
// (default: the last).
func (c *checker) axisReduceResult(ex *ast.Call, argTypes []Type) Type {
	t, ok := argTypes[0].(tTensor)
	if !ok || len(t.dims) == 0 {
		return tUnknown{}
	}
	axis := len(t.dims) - 1
	if len(ex.Args) == 2 {
		ax, ok := constInt(ex.Args[1])
		if !ok {
			return tUnknown{}
		}
		axis = ax
	}
	if axis < 0 {
		axis += len(t.dims)
	}
	if axis < 0 || axis >= len(t.dims) {
		c.reportAxis(ex, t)
		return tUnknown{}
	}
	return tTensor{dims: removeDim(t.dims, axis)}
}

func (c *checker) broadcastTwo(ex *ast.Call, argTypes []Type) Type {
	if len(argTypes) != 2 {
		return tUnknown{}
	}
	a, aok := argTypes[0].(tTensor)
	b, bok := argTypes[1].(tTensor)
	if !aok || !bok {
		return tUnknown{}
	}
	res, msg := elementwiseResult(a, b)
	if msg != "" {
		c.report(ex.Line, "%s", msg)
		return tUnknown{}
	}
	return res
}

func (c *checker) broadcastWhere(ex *ast.Call, argTypes []Type) Type {
	if len(argTypes) != 3 {
		return tUnknown{}
	}
	a, aok := argTypes[1].(tTensor)
	b, bok := argTypes[2].(tTensor)
	if !aok || !bok {
		return tUnknown{}
	}
	res, msg := elementwiseResult(a, b)
	if msg != "" {
		c.report(ex.Line, "%s", msg)
		return tUnknown{}
	}
	// The condition is broadcast against the chosen elements at runtime, so a
	// shape that cannot broadcast is a shape error the checker can raise here
	// rather than leaving it to the interpreter.
	if cond, ok := argTypes[0].(tTensor); ok {
		if resT, ok := res.(tTensor); ok {
			combined, condMsg := elementwiseResult(cond, resT)
			if condMsg != "" {
				c.report(ex.Line, "%s", condMsg)
				return tUnknown{}
			}
			res = combined
		}
	}
	return res
}

func removeDim(dims []int, axis int) []int {
	out := make([]int, 0, len(dims)-1)
	out = append(out, dims[:axis]...)
	out = append(out, dims[axis+1:]...)
	return out
}

// --- shape rules -----------------------------------------------------------

func shapeMatch(a, b tTensor) bool {
	if len(a.dims) != len(b.dims) {
		return false
	}
	for i := range a.dims {
		if a.dims[i] != b.dims[i] {
			return false
		}
	}
	return true
}

// broadcastDims applies NumPy broadcasting to two shapes that may contain
// unknown dimensions (-1). It returns the result dims, or ok=false only when a
// mismatch is certain (both dims known, unequal, and neither is 1).
func broadcastDims(a, b tTensor) ([]int, bool) {
	ra, rb := len(a.dims), len(b.dims)
	r := ra
	if rb > r {
		r = rb
	}
	out := make([]int, r)
	for i := 0; i < r; i++ {
		da, db := 1, 1
		aKnown, bKnown := true, true
		if i < ra {
			da = a.dims[ra-1-i]
			aKnown = da >= 0
		}
		if i < rb {
			db = b.dims[rb-1-i]
			bKnown = db >= 0
		}
		var d int
		switch {
		case !aKnown && !bKnown:
			d = -1
		case !aKnown:
			if db == 1 {
				d = -1
			} else {
				d = db
			}
		case !bKnown:
			if da == 1 {
				d = -1
			} else {
				d = da
			}
		case da == db:
			d = da
		case da == 1:
			d = db
		case db == 1:
			d = da
		default:
			return nil, false
		}
		out[r-1-i] = d
	}
	return out, true
}

func elementwiseResult(a, b tTensor) (Type, string) {
	dims, ok := broadcastDims(a, b)
	if !ok {
		return nil, fmt.Sprintf("shape mismatch: %s vs %s cannot broadcast", dimsString(a), dimsString(b))
	}
	return tTensor{dims: dims}, ""
}

func matmulResult(a, b tTensor) (Type, string) {
	// `@` is a plain matrix product: it takes 1-D or 2-D operands and has no
	// batched form, so an operand that is a scalar (rank 0) or rank 3 or higher
	// is not an unknown shape to leave alone, it is a certain runtime error. The
	// rank is structural and known even when the individual sizes are not, so
	// this is caught here rather than only when the program runs.
	if len(a.dims) < 1 || len(a.dims) > 2 || len(b.dims) < 1 || len(b.dims) > 2 {
		return nil, fmt.Sprintf("@ (matmul) requires 1-D or 2-D operands, got %s @ %s", dimsString(a), dimsString(b))
	}
	a2 := a.dims
	if len(a.dims) == 1 {
		a2 = []int{1, a.dims[0]}
	}
	b2 := b.dims
	if len(b.dims) == 1 {
		b2 = []int{b.dims[0], 1}
	}
	if len(a2) != 2 || len(b2) != 2 {
		return tUnknown{}, ""
	}
	k, k2 := a2[1], b2[0]
	if k >= 0 && k2 >= 0 && k != k2 {
		return nil, fmt.Sprintf("shape mismatch in @: %s @ %s (inner %d != %d)", dimsString(a), dimsString(b), k, k2)
	}
	m, n := a2[0], b2[1]
	switch {
	case len(a.dims) == 1 && len(b.dims) == 1:
		return scalar(), ""
	case len(a.dims) == 1:
		return tTensor{dims: []int{n}}, ""
	case len(b.dims) == 1:
		return tTensor{dims: []int{m}}, ""
	default:
		return tTensor{dims: []int{m, n}}, ""
	}
}

// linearResult types linear(x, W) = x @ Wᵀ. x is 1-D [k] or 2-D [m,k]; W is the
// dense weight, 2-D [n,k] in [nout, nin] layout. The contracted dim is the last
// of each. It mirrors matmulResult's handling of unknown (-1) dims.
func linearResult(a, w tTensor) (Type, string) {
	if len(a.dims) < 1 || len(a.dims) > 2 {
		return nil, fmt.Sprintf("linear requires a 1-D or 2-D input, got %s", dimsString(a))
	}
	if len(w.dims) != 2 {
		return nil, fmt.Sprintf("linear requires a 2-D weight, got %s", dimsString(w))
	}
	k := a.dims[len(a.dims)-1]
	n, k2 := w.dims[0], w.dims[1]
	if k >= 0 && k2 >= 0 && k != k2 {
		return nil, fmt.Sprintf("shape mismatch in linear: %s @ %sᵀ (inner %d != %d)", dimsString(a), dimsString(w), k, k2)
	}
	if len(a.dims) == 1 {
		return tTensor{dims: []int{n}}, ""
	}
	return tTensor{dims: []int{a.dims[0], n}}, ""
}

// join returns a if a and b agree, otherwise Unknown.
func join(a, b Type) Type {
	at, aok := a.(tTensor)
	bt, bok := b.(tTensor)
	if aok && bok && shapeMatch(at, bt) {
		return at
	}
	return tUnknown{}
}

// unorderable names a type that `<` and its relatives are not defined on, when
// the checker is sure of it. A type it cannot resolve reports false, so nothing
// is judged on a guess.
func (c *checker) unorderable(t Type) (string, bool) {
	switch v := t.(type) {
	case tEnum, tArr, tDict, tRecord, tList, tBytes, tUnit, tBool, tCtor, tFnType, tFn, tBuiltin, tTuple:
		return c.typeString(t), true
	case tTensor:
		// Ordering is scalar-only, so a tensor of known rank above 0 cannot be
		// ordered and the runtime refuses it. `where(A > 0.0, A, B)` is the
		// shape this takes in practice -- the masking idiom every array library
		// has -- and it failed at run time for every non-scalar A while the
		// checker, which knew the rank, said nothing. `greater(A, 0.0)` is the
		// elementwise form and yields the mask.
		if len(v.dims) > 0 && fullyKnown(v) {
			return c.typeString(t), true
		}
	}
	return "", false
}

// isDefiniteNonRecord reports whether a type certainly has no fields. A tensor
// is left out on purpose: `t.to(f32)` is a field access syntactically.
func isDefiniteNonRecord(t Type) bool {
	switch t.(type) {
	case tInt, tStr, tBool, tBytes, tUnit, tArr, tDict, tEnum, tList, tTuple:
		return true
	}
	return false
}

func isDefiniteNonTensor(t Type) bool {
	switch t.(type) {
	case tBool, tStr, tUnit, tList, tRecord, tFn, tBuiltin, tArr, tDict, tEnum, tBytes, tCtor, tFnType, tTuple:
		return true
	}
	return false
}

// --- literal shape extraction ---------------------------------------------

func constInt(e ast.Expr) (int, bool) {
	// A negated literal like `-1` parses as a unary minus over a number, not as a
	// negative NumberLit, so read through it. This is what lets the reshape check
	// see the -1 in `reshape(x, -1, 4)` — the numpy habit twill does not honour —
	// rather than giving up on a shape it cannot fold.
	if u, ok := e.(*ast.Unary); ok && u.Op == "-" {
		if v, ok := constInt(u.Operand); ok {
			return -v, true
		}
	}
	if n, ok := e.(*ast.NumberLit); ok {
		iv := int(n.Value)
		if float64(iv) == n.Value {
			return iv, true
		}
	}
	return 0, false
}

// constShape reads a shape from integer-literal arguments, or a single list
// literal of integer literals.
func constShape(args []ast.Expr) ([]int, bool) {
	if len(args) == 1 {
		if lst, ok := args[0].(*ast.ListLit); ok {
			return constIntElems(lst.Elements)
		}
		// list(2, 3): the idiomatic shape argument, a call to the list builtin
		// with integer literals. Reading it lets reshape's count check and
		// broadcast_to's compatibility check fire for `reshape(x, list(2, 3))`,
		// the form the standard library and examples actually use, not only the
		// separate-argument form.
		if call, ok := args[0].(*ast.Call); ok {
			if id, ok := call.Callee.(*ast.Ident); ok && id.Name == "list" {
				return constIntElems(call.Args)
			}
		}
	}
	return constIntElems(args)
}

// constSplitSizes reads split's second argument as an explicit list of sizes:
// a list literal or a list(...) call of integer literals. A bare integer is the
// equal-pieces count, a different case, so this reports ok=false for it.
func constSplitSizes(e ast.Expr) ([]int, bool) {
	if lst, ok := e.(*ast.ListLit); ok {
		return constIntElems(lst.Elements)
	}
	// An all-numeric bracket literal parses as a tensor, not a list, so sizes
	// written [2, 4] arrive here as a TensorLit; the runtime reads it as a 1-D
	// tensor of lengths just the same.
	if tl, ok := e.(*ast.TensorLit); ok {
		return constIntElems(tl.Elements)
	}
	if call, ok := e.(*ast.Call); ok {
		if id, ok := call.Callee.(*ast.Ident); ok && id.Name == "list" {
			return constIntElems(call.Args)
		}
	}
	return nil, false
}

// intsBracketed renders a list of integers the way Go prints an []int with %v,
// space-separated in square brackets, so a diagnostic quoting a permutation
// matches the runtime's wording exactly.
func intsBracketed(xs []int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range xs {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d", x)
	}
	b.WriteByte(']')
	return b.String()
}

// constIndexList reads gather's index argument as a constant list of integers:
// a bracket literal (list or all-numeric tensor), a list(...) call, or the
// tensor([...]) form the standard library tends to write. Anything else -- a
// variable, a computed expression -- is not foldable and returns ok=false.
func constIndexList(e ast.Expr) ([]int, bool) {
	if idx, ok := constSplitSizes(e); ok {
		return idx, true
	}
	if call, ok := e.(*ast.Call); ok {
		if id, ok := call.Callee.(*ast.Ident); ok && id.Name == "tensor" && len(call.Args) == 1 {
			return constIndexList(call.Args[0])
		}
	}
	return nil, false
}

// constIntElems reads a run of expressions as integer literals, all or nothing.
func constIntElems(elems []ast.Expr) ([]int, bool) {
	dims := make([]int, len(elems))
	for i, el := range elems {
		n, ok := constInt(el)
		if !ok {
			return nil, false
		}
		dims[i] = n
	}
	return dims, true
}

// tensorOnlyBuiltins are the builtins whose arguments the runtime requires to
// be tensors or numbers. Deliberately a short, checked list rather than
// everything: len, map, fold, concat, append, sort, zip, enumerate and the rest
// take lists on purpose, and reporting those would be the false positive that
// makes a checker worth turning off.
var tensorOnlyBuiltins = map[string]bool{
	"sum": true, "mean": true, "prod": true, "median": true, "max": true, "min": true,
	"argmax": true, "argmin": true, "exp": true, "log": true, "sin": true, "cos": true,
	"tanh": true, "sigmoid": true, "sqrt": true, "square": true, "abs": true, "relu": true,
	"log1p": true, "expm1": true,
	"softmax": true, "logsumexp": true, "transpose": true, "reshape": true, "shape": true,
	"matmul": true, "dot": true, "linear": true, "broadcast_to": true, "cumsum": true,
	"cumprod": true, "flip": true, "roll": true, "diff": true,
}

var builtinNames = map[string]bool{
	"print": true, "relu": true, "exp": true, "log": true, "sin": true,
	// The accurate-near-zero pair, differentiable like exp and log.
	"log1p": true, "expm1": true,
	// SHA-256 of a Str and of a Bytes, lower-case hex.
	"sha256": true, "sha256_bytes": true,
	"cos": true, "tanh": true, "sigmoid": true, "sqrt": true, "sum": true, "prod": true, "median": true,
	"mean": true, "abs": true, "pow": true, "matmul": true, "dot": true, "linear": true, "quantize": true, "nbytes": true, "dtype": true,
	"grad": true, "grads": true, "stop_grad": true, "value_and_grad": true, "map": true, "zip": true,
	// The barrier. docs/roadmap.md entry 30.
	"black_box": true,
	"tensor":    true, "scalar": true, "zeros": true, "ones": true, "fill": true,
	"randn": true, "rand": true, "eye": true, "linspace": true, "arange": true, "transpose": true, "shape": true,
	"len": true, "item": true, "range": true, "list": true, "str": true,
	"square": true, "maximum": true, "minimum": true, "greater": true,
	"less": true, "greater_equal": true, "less_equal": true, "equal": true,
	"where": true, "clip": true, "max": true, "min": true, "argmax": true, "argmin": true, "flip": true, "roll": true, "diff": true,
	"softmax": true, "logsumexp": true, "sort": true, "topk": true, "argtopk": true, "reshape": true, "broadcast_to": true, "concat": true, "split": true,
	"fold": true, "append": true, "enumerate": true, "read_csv": true,
	"einsum": true, "map_leaves": true, "zip_leaves": true, "seed": true,
	"read_frame": true, "write_frame": true, "columns": true, "field": true,
	"with_field": true, "gbm_fit": true, "gbm_predict": true,
	"cumsum": true, "cumprod": true, "cummax": true, "cummin": true,
	"conv2d": true, "maxpool2d": true, "save": true, "load": true,
	"gather": true, "permutation": true, "int": true,
	"floor": true, "ceil": true, "round": true, "jacobian": true, "hessian": true,
	"jvp": true, "vjp": true, "hvp": true,
	// Bitwise ops on I64. `and`/`or` are also the boolean keywords, but a call by
	// that name is the bitwise builtin; `bnot` is bitwise complement.
	"exit": true, "arr_of_tensor": true, "all_finite": true, "file_size": true, "numel": true,
	"rng_open": true, "rng_close": true, "rng_u53": true, "rng_f64": true, "rng_norm": true,
	"f64_bits_hi": true, "f64_bits_lo": true, "f64_from_halves": true,
	"read_text_or": true, "write_text_or": true,
	"and": true, "or": true, "band": true, "bor": true,
	"xor": true, "shl": true, "shr": true, "bnot": true,
	// `ushr` is the logical right shift. It is a call and not an operator, so it
	// is absent from the infix tables in the lexer, the parser and the formatter.
	"ushr": true,
	// Built-in Res and Opt cases, and `unit`, the Unit value's name.
	"Ok": true, "Err": true, "Some": true, "None": true, "unit": true,
	// Filesystem and paths (internal/interp/fs.go).
	"path_exists": true, "path_is_dir": true, "mkdir_all": true, "remove_file": true,
	"remove_dir": true, "rename": true, "remove_all": true, "mtime": true, "mono_ns": true,
	"read_file_at": true, "mem_counters_available": true, "mem_allocs": true,
	"mem_bytes": true, "mem_live_bytes": true, "mem_tensors": true, "temp_dir": true, "cwd": true,
	"path_join": true, "path_base": true, "path_dir": true, "path_ext": true,
	"path_stem": true, "path_normalize": true, "path_is_abs": true,
	// Scalar f64 math, conversions and IEEE bit access for the systems dialect.
	"f64_sqrt": true, "f64_exp": true, "f64_log": true, "f64_log1p": true,
	"f64_expm1": true, "f64_sin": true,
	"f64_cos": true, "f64_floor": true, "f64_trunc": true, "f64_pow": true,
	"f64_of_i64": true, "i64_of_f64": true, "f64_bits": true,
	"f64_from_bits": true, "f64_signbit": true,
	// Systems collections: growable list, ordered dict, byte buffer.
	"arr_new": true, "push": true, "arr_push": true, "pop": true,
	"dict_new": true, "dict_set": true, "dict_get": true, "dict_has": true,
	"dict_must": true, "dict_or": true, "dict_keys": true,
	"bytes_new": true, "bytes_push": true, "bytes_to_str": true, "abort": true,
	"dict_del": true, "buf_new": true, "buf_get8": true, "buf_set8": true,
	"buf_len": true, "argsort": true,
	"f64_ceil": true, "f64_round": true, "f64_tanh": true, "f64_mod": true,
	"i64": true, "f64": true,
	// Systems I/O and string parsing.
	"write_out": true, "write_err": true, "read_file": true, "write_file": true,
	"list_dir": true, "resolve_path": true, "str_quote": true, "i64_of_str": true,
	// Starting a program. spool docs/needs.md entry 1.
	"run": true,
	// Diagnostics, seeded rng, identity, argv and value persistence.
	"emit_line": true, "rng_seed": true, "rng_uniform": true, "rng_normal": true,
	"rng_perm": true, "is_same": true, "args": true, "save_value": true, "load_value": true,
	// List literal-as-call, in-place clear, byte/char strings, GPU probe.
	"arr": true, "arr_clear": true, "chr": true, "slice": true, "gpu_available": true,
	"gpu_device_count": true, "is_tty_stdout": true, "window_size": true,
	// GPU device FFI boundary (no backend in this build; see builtins.go).
	"gpu_device_open": true, "gpu_device_info": true, "gpu_device_close": true,
	"gpu_alloc": true, "gpu_free": true, "gpu_write": true, "gpu_read": true,
	"gpu_copy": true, "gpu_program_build": true, "gpu_kernel": true,
	"gpu_set_arg_buffer": true, "gpu_set_arg_local": true, "gpu_launch": true,
	"gpu_finish": true, "gpu_device_info_i64": true, "env": true,
	"gpu_set_arg_i64": true, "gpu_set_arg_f64": true, "clock_now_ms": true,
	"str_to_f64": true, "f64_to_str": true, "num_to_text": true, "module_source": true, "f64_hex": true, "gbm_describe": true,
}

// builtinArity lists the builtins the runtime registers with a fixed number of
// arguments, mirroring the def(name, n, ...) calls in the interpreter. A call
// with the wrong count is a certain runtime error, so it is caught statically.
// Variadic builtins (those the interpreter registers with -1, taking an optional
// axis, a run of dimensions, or a trailing flag) are deliberately absent; their
// per-shape cases handle what can be known. Keep this in step with the runtime.
var builtinArity = map[string]int{
	// nullary
	"args": 0, "arr_new": 0, "bytes_new": 0, "clock_now_ms": 0, "dict_new": 0,
	"gpu_available": 0, "gpu_device_count": 0, "is_tty_stdout": 0, "rng_normal": 0, "mono_ns": 0,
	"mem_counters_available": 0, "mem_allocs": 0, "mem_bytes": 0,
	"mem_live_bytes": 0, "mem_tensors": 0,
	"rng_uniform": 0, "window_size": 0, "cwd": 0,
	// unary -- elementwise math (unaryOp / elemOp) and the rest
	"relu": 1, "exp": 1, "log": 1, "log1p": 1, "expm1": 1, "sin": 1, "cos": 1, "tanh": 1, "sigmoid": 1,
	"sha256": 1, "sha256_bytes": 1, "f64_log1p": 1, "f64_expm1": 1,
	"sqrt": 1, "square": 1, "floor": 1, "ceil": 1, "round": 1,
	"abort": 1, "abs": 1, "arr_clear": 1, "bnot": 1, "buf_len": 1, "buf_new": 1,
	"bytes_to_str": 1, "chr": 1, "columns": 1, "dict_keys": 1, "emit_line": 1,
	"enumerate": 1, "env": 1, "eye": 1, "f64_bits": 1, "f64_from_bits": 1,
	"f64_hex": 1, "f64_of_i64": 1, "f64_signbit": 1, "f64_to_str": 1,
	"gbm_describe": 1, "grad": 1, "grads": 1, "hessian": 1, "dtype": 1, "i64_of_f64": 1,
	"i64_of_str": 1, "int": 1, "item": 1, "jacobian": 1, "jvp": 1, "vjp": 1, "hvp": 1,
	"len": 1, "list_dir": 1,
	"load": 1, "load_value": 1, "module_source": 1, "nbytes": 1, "num_to_text": 1,
	"permutation": 1, "pop": 1, "read_csv": 1, "read_file": 1, "read_frame": 1,
	"rng_perm": 1, "rng_seed": 1, "rng_open": 1, "rng_close": 1, "rng_u53": 1, "rng_f64": 1, "rng_norm": 1, "scalar": 1, "seed": 1, "shape": 1, "str": 1,
	"str_quote": 1, "str_to_f64": 1, "tensor": 1, "value_and_grad": 1,
	"write_err": 1, "write_out": 1, "exit": 1, "arr_of_tensor": 1, "all_finite": 1, "file_size": 1, "numel": 1, "stop_grad": 1,
	"black_box":   1,
	"f64_bits_hi": 1, "f64_bits_lo": 1,
	"path_exists": 1, "path_is_dir": 1, "mkdir_all": 1, "remove_file": 1, "remove_dir": 1,
	"temp_dir": 1, "remove_all": 1, "mtime": 1, "path_base": 1, "path_dir": 1, "path_ext": 1, "path_stem": 1,
	"path_normalize": 1, "path_is_abs": 1,
	// binary -- elementwise/tensor pairs (binTensor), bit ops (bitOp), and the rest
	"matmul": 2, "dot": 2, "conv2d": 2, "maximum": 2, "minimum": 2, "greater": 2,
	"less": 2, "greater_equal": 2, "less_equal": 2, "equal": 2,
	"read_text_or": 2, "write_text_or": 2, "f64_from_halves": 2, "rename": 2,
	"and": 2, "or": 2, "band": 2, "bor": 2, "xor": 2, "shl": 2, "shr": 2, "ushr": 2,
	"append": 2, "arr_push": 2, "buf_get8": 2, "bytes_push": 2, "concat": 2,
	"dict_del": 2, "dict_get": 2, "dict_has": 2, "dict_must": 2, "f64_mod": 2,
	"f64_pow": 2, "field": 2, "gather": 2, "is_same": 2, "linear": 2, "map": 2,
	"map_leaves": 2, "maxpool2d": 2, "pow": 2, "push": 2, "save": 2,
	"save_value": 2, "write_file": 2, "write_frame": 2, "zip_leaves": 2,
	// ternary
	"arange": 3, "buf_set8": 3, "read_file_at": 3, "clip": 3, "dict_or": 3, "dict_set": 3, "fold": 3,
	"linspace": 3, "run": 3, "slice": 3, "where": 3, "with_field": 3,
}
