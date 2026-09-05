// Package interp is the tree-walking evaluator and standard library for Twill.
package interp

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/twill-lang/twill/internal/ast"
	"github.com/twill-lang/twill/internal/gbm"
	"github.com/twill-lang/twill/internal/ir"
	"github.com/twill-lang/twill/internal/parser"
	"github.com/twill-lang/twill/internal/tensor"
	"github.com/twill-lang/twill/internal/trace"
	"github.com/twill-lang/twill/internal/value"
	"github.com/twill-lang/twill/std"
)

// defaultSeed makes randomness reproducible by default — a run gives the same
// result every time unless seed(...) changes it. Determinism matters for model
// governance and audit.
const defaultSeed = 1

// stdPrefix reserves a leading "std/" for the standard library that ships
// inside the binary. What follows it is a module name, not a file path, so
// `import "std/nn"` means the same thing from any directory and never reaches
// the filesystem. Every other import path is a file, relative to the importer.
const stdPrefix = "std/"

// stdOverrideEnv points stdPrefix at a directory of .tw files instead of the
// embedded copy, for working on the library itself without rebuilding.
const stdOverrideEnv = "TWILL_STD"

// legacyExt is the source extension Twill used when the language was called
// Raster. The move to .tw is a hard break, not a deprecation: a .ra file is
// refused outright rather than read anyway, so that there is exactly one source
// extension to explain and no .ra files linger in the wild waiting on a removal
// date that nobody remembers.
const legacyExt = ".ra"

// CheckLegacyExt returns a non-nil error naming the new extension when path
// carries the retired .ra extension, and nil otherwise. Both the CLI and the
// import resolver route through it so the wording is the same either way.
func CheckLegacyExt(path string) error {
	if len(path) < len(legacyExt) || !strings.EqualFold(path[len(path)-len(legacyExt):], legacyExt) {
		return nil
	}
	renamed := path[:len(path)-len(legacyExt)] + ".tw"
	return fmt.Errorf("%q uses the retired .ra extension: Twill source files are .tw, so rename it to %q", path, renamed)
}

// DefaultMaxCallDepth is how many twill calls may be nested before the
// interpreter refuses to go further.
//
// It exists because a Go stack overflow is a *fatal* error: no recover catches
// it, the process dies and nothing the interpreter does afterwards runs. With
// the limit lifted, a three-line runaway recursion prints 424 lines of Go
// runtime traceback on this machine and exits 2, and not one of those lines
// names the user's function or the line the recursion is on. See
// TestRunawayRecursionIsRefused for how that was measured. A missing base case
// is the most likely first mistake a newcomer makes, so that crash was the most
// likely first thing they saw. A counter checked on the way in turns it into an
// ordinary twill error, which is the only way to get one at all.
//
// Both ends of the number are measured and docs/needs.md NEEDS-30 has the
// tables. Depths here are counted in nested twill calls, which is what
// callDepth counts, so f(n) reaches n+1 of them.
//
// The low end: over every .tw file in this repository, run on this interpreter
// and put through the self-hosted checker running on top of it, the deepest
// call depth anything reaches is 217, and that is the self-hosted checker on
// src/parse.tw rather than a program. Nothing run directly gets past 14. An
// earlier round measured the nine satellite repositories too and found nothing
// deeper than 18.
//
// The high end is not one number, and that has to be said plainly rather than
// rounded off. What decides where the Go stack runs out is not what the twill
// frame holds but how deeply the recursive call sits inside the expression
// around it, because every enclosing operator is another evalExpr frame held
// live across the call. Measured on this machine, macOS arm64 with Go's 1 GB
// goroutine stack, a runaway f survives 236,295 nested calls when the call is
// bare, 150,466 with one `+ 1` around it, 13,046 with thirty and 1,373 with
// three hundred.
//
// Widening the frame changes nothing whatever: one parameter or eight, no
// locals or sixteen, the cliff stays at 150,466. An earlier round of this work
// read the same evidence the other way, reporting that a fat frame of five
// parameters and three locals died a quarter shallower, at 110,375. That is the
// two-operator-layer number: its test program had one more layer of expression
// around the call than the thin one it was compared against. A thin frame with
// two layers and a fat frame with two layers both survive 110,375.
//
// So no fixed limit is uniformly below the crash, because nesting has no upper
// bound. What 10,000 buys is a bounded envelope: a runaway call still gets the
// diagnostic while it sits inside up to 39 arithmetic layers, which survives
// 10,165 calls, or 25 of the most expensive layer measured, `[x][0]`, which
// survives 10,271. The deepest call site written anywhere in this
// repository is nested 21 expressions deep. Past that envelope the fatal
// overflow is back. The limit is a diagnostic for the shapes people write, not
// a guarantee for every shape, and it never makes anything worse than it was.
const DefaultMaxCallDepth = 10000

// maxCallDepthEnv overrides DefaultMaxCallDepth for interpreters made by New.
//
// It exists for one caller: a host running an interpreter written in twill. See
// Interp.MaxCallDepth for why that case needs a different number from every
// other one, and why no single shared constant can serve both.
const maxCallDepthEnv = "TWILL_MAX_CALL_DEPTH"

// envMaxCallDepth is DefaultMaxCallDepth unless TWILL_MAX_CALL_DEPTH holds a
// positive integer. A value that is not one is ignored rather than refused:
// this is a safety limit, and failing to start because its override is
// misspelled would be a worse outcome than running at the default.
func envMaxCallDepth() int {
	n, err := strconv.Atoi(os.Getenv(maxCallDepthEnv))
	if err != nil || n < 1 {
		return DefaultMaxCallDepth
	}
	return n
}

// callDepthMessage is the refusal, in the one place both engines copy it from.
// src/eval.tw's call_depth_message is the same text and the two must agree
// character for character; TestSelfHostedRefusalsMatchTheBootstrap, in
// recursion_test.go, is what holds them together: it runs each shape of runaway
// recursion on both engines and requires the self-hosted CLI's first stderr
// line to equal the bootstrap's byte for byte, so a change made here and not in
// src/eval.tw fails there.
func callDepthMessage(name string, depth int) string {
	who := "an anonymous function"
	if name != "" {
		who = strconv.Quote(name)
	}
	return fmt.Sprintf("call depth limit reached: %s is %d calls deep, which is as deep as twill "+
		"goes. A recursion this deep is almost always a missing base case; if it is not, rewrite "+
		"it as a loop", who, depth)
}

// RuntimeError carries a source line for errors raised during evaluation.
type RuntimeError struct {
	Msg  string
	Line int
}

func (e *RuntimeError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// ExitError is what `exit(n)` raises: a request to stop the program with a
// status, not a failure to report. It unwinds like an error so that every
// enclosing frame is left the way a return would leave it, and the CLI turns it
// into the process's exit code without printing anything. A test runner that
// evaluates several files in one process catches it per file, which a direct
// os.Exit would not allow.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit(%d)", e.Code) }

// returnSignal unwinds the stack for a Twill `return`.
type returnSignal struct{ value value.Value }

// breakSignal and continueSignal unwind a loop body to its enclosing loop, the
// same way returnSignal unwinds a function. runLoopBody catches them.
type breakSignal struct{}
type continueSignal struct{}

// srcFrame says where the file currently executing came from. dir is what its
// relative paths resolve against; std marks a standard-library module, which
// lives in the binary and so has no directory of its own.
type srcFrame struct {
	dir string
	std bool
}

// Interp holds global state for a running program.
type Interp struct {
	Global   *value.Env
	out      func(string)
	srcStack []srcFrame
	loaded   map[string]bool // plain imports already loaded
	loading  map[string]bool // namespaced imports currently loading (cycle guard)
	rng      *rand.Rand      // deterministic RNG for randn/rand/seed
	Args     []string        // program arguments, exposed by the args builtin
	// gbmModels holds fitted gradient-boosting models by integer handle. The Go
	// interpreter passes a *gbm.Model as a value directly, but a twill value
	// cannot hold a native pointer, so the self-hosted evaluator refers to a
	// model by a handle into this table (a VForeign carrying the I64).
	gbmModels map[int64]*gbm.Model
	nextGbm   int64
	// variantNames is every enum case in scope, so `Opt.Some(x)` can tell a
	// qualified variant from a field access on a record that happens to share the
	// name. Variant constructors are ordinary builtins and closures once bound,
	// which is why the set is tracked rather than inferred from the value.
	variantNames map[string]bool
	// structFields is each declared struct's field types, by the struct's bare
	// name. Structs are erased at run time -- a record carries its own fields --
	// but a typed literal's declared field types are what say that `{}` in
	// `Catalog { versions: {} }` is a dictionary, so the declaration is kept for
	// that one purpose. See containerForAnnotation.
	structFields map[string]map[string]string
	// variantPayloads is each enum case's declared payload type, by case name,
	// so a payload written as a number at an `I64` case is stored as one. Only
	// the scalar conversions read it; everything else about a payload's type is
	// the checker's.
	variantPayloads map[string]string
	// gradDepth counts the reverse-mode passes currently running, so a gradient
	// taken inside one is refused rather than answered with a silent zero. See
	// gradients in builtins.go.
	gradDepth int
	// callDepth counts the closures currently executing. It is what tells a `?`
	// at the top of a file, where there is no function to return from, apart
	// from one inside a function, and it is what the recursion limit counts.
	callDepth int
	// line is the source line of the statement currently executing, which is
	// all the position a panic from inside the interpreter has to report. See
	// recovered.
	line int
	// MaxCallDepth is the recursion limit for this interpreter, set by New to
	// DefaultMaxCallDepth or to whatever TWILL_MAX_CALL_DEPTH says.
	//
	// It is a field rather than a constant because of one case: an interpreter
	// written in twill, running on this one. Two counters are then measuring the
	// same Go stack, and the outer one always reaches its limit first, because
	// each of the inner interpreter's frames costs several of the outer one's.
	// Measured on `twill run src/main.tw run prog.tw`, the outer depth is
	// 8*inner + 9 exactly, so an outer interpreter left at 10,000 cuts the inner
	// program off at 1,248 of its own calls and names a function inside
	// src/eval.tw rather than one in prog.tw.
	//
	// No single shared constant fixes that, and it is worth being precise about
	// why: if both engines refuse at L, the inner engine needs 8L+9 outer frames
	// to reach L, and the outer engine stops at L first for every positive L.
	// The only way the two can refuse the same program with the same words is
	// for the host to be given a larger number than the guest, which is what
	// this field and TWILL_MAX_CALL_DEPTH are for. The number the self-hosted
	// evaluator needs was bisected on the shipped CLI rather than derived: at
	// 80,012 the host still refuses first, at 80,013 the guest does.
	// TWILL_MAX_CALL_DEPTH=100000 is the documented value because it clears
	// 80,013 without sitting on it. There is no single number above which it
	// stops working, for the same reason there is no single crash depth: see
	// DefaultMaxCallDepth. What is known is that the host survives this one,
	// because TestSelfHostedRefusalsMatchTheBootstrap runs it.
	MaxCallDepth int
	// rngs holds the independent generator streams `rng_open` hands out, by
	// handle. A twill value cannot carry a native pointer, so a stream is named
	// by an integer, the same way a fitted gbm model is. This is separate from
	// `rng` above, which is the single global generator behind randn/rand/seed.
	rngs          map[int64]*rand.Rand
	nextRngHandle int64
	// tr is the tracer: the front end that records tensor operations into an IR
	// graph instead of running them, and compiles the graph when the value
	// escapes. See tracing.go for the interpreter's half of it and
	// internal/trace for the tracer's. Nil is never valid; tracing is switched
	// off through the tracer itself, so that the escape points stay on the same
	// code path whether it is on or not.
	tr *trace.Tracer
}

// New creates an interpreter. If out is nil, output goes to stdout.
func New(out func(string)) *Interp {
	if out == nil {
		out = func(s string) { fmt.Println(s) }
	}
	ip := &Interp{
		Global:          value.NewEnv(nil),
		out:             out,
		loaded:          map[string]bool{},
		loading:         map[string]bool{},
		rng:             rand.New(rand.NewSource(defaultSeed)),
		gbmModels:       map[int64]*gbm.Model{},
		variantNames:    map[string]bool{},
		variantPayloads: map[string]string{},
		structFields:    map[string]map[string]string{},
		rngs:            map[int64]*rand.Rand{},
		tr:              trace.New(nil),
		MaxCallDepth:    envMaxCallDepth(),
	}
	ip.installBuiltins()
	return ip
}

func (ip *Interp) panicf(line int, format string, args ...any) {
	panic(&RuntimeError{Msg: fmt.Sprintf(format, args...), Line: line})
}

func (ip *Interp) currentDir() string {
	if n := len(ip.srcStack); n > 0 && ip.srcStack[n-1].dir != "" {
		return ip.srcStack[n-1].dir
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// inStd reports whether the file currently executing is a standard-library
// module.
func (ip *Interp) inStd() bool {
	n := len(ip.srcStack)
	return n > 0 && ip.srcStack[n-1].std
}

// resolvePath makes a relative path absolute against the running script's
// directory (so file I/O is relative to the source file, not the process cwd).
func (ip *Interp) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(ip.currentDir(), path)
}

// recovered turns whatever came out of a recover into what the caller should
// return. The interpreter's own signals are handled by name; anything else is a
// fault inside the interpreter, and is rendered as a twill error rather than
// re-panicked into a Go traceback.
//
// Re-panicking was the previous behaviour, and what it produced was a goroutine
// dump addressed to someone who is not reading it: the person at the keyboard
// is running a twill program and cannot act on a Go stack. The
// line the program had reached is what they can act on, together with being
// told plainly that the fault is twill's and not theirs. The Go stack is not
// lost to whoever does have to fix it -- the panic value is quoted verbatim,
// and the failing input is in front of them.
//
// This is the second half of the recursion limit and not a replacement for it.
// A Go stack overflow is a fatal error that never reaches a recover, so the
// one fault most likely to be hit is the one fault this cannot catch. That is
// what MaxCallDepth is for.
func (ip *Interp) recovered(r any, result value.Value) (value.Value, error) {
	switch e := r.(type) {
	case *RuntimeError:
		return result, e
	case *ExitError:
		return result, e
	case returnSignal:
		return e.value, nil
	case breakSignal:
		return result, &RuntimeError{Line: ip.line, Msg: "`break` outside a loop"}
	case continueSignal:
		return result, &RuntimeError{Line: ip.line, Msg: "`continue` outside a loop"}
	default:
		// A Go runtime fault stringifies as "runtime error: ...", which after the
		// prefix below would read "internal error: runtime error: ...". One name
		// for the thing is enough, and "internal" is the word that says whose
		// fault it is.
		what := strings.TrimPrefix(fmt.Sprint(r), "runtime error: ")
		return result, &RuntimeError{Line: ip.line, Msg: fmt.Sprintf(
			"internal error: %s. This is a bug in twill, not in the program that hit it: "+
				"please report it, with this file, at https://github.com/twill-lang/twill/issues", what)}
	}
}

// Run parses and evaluates source, returning the last value.
func (ip *Interp) Run(src string) (result value.Value, err error) {
	prog, perr := parser.Parse(src)
	if perr != nil {
		return nil, perr
	}
	defer func() {
		if r := recover(); r != nil {
			result, err = ip.recovered(r, result)
		}
	}()
	result = value.TheUnit
	ip.hoistFns(prog.Body, ip.Global)
	for _, s := range prog.Body {
		result = ip.execStmt(s, ip.Global)
	}
	ip.escape()
	return result, nil
}

// RunFileMain runs a file and, if it is a systems-mode program defining a
// nullary main, calls main() as the entry point and returns its value. This is
// the two dialects' split: a numeric-mode file is its top-level statements,
// while a systems-mode program's entry is main(), the way Go and Rust spell it.
// Trailing command-line arguments are exposed through the args builtin, so
// `twill run src/main.tw check foo.tw` runs the self-hosted CLI's main() with
// ["check", "foo.tw"]. A file with no main runs its top level and nothing more.
func (ip *Interp) RunFileMain(path string, args []string) (result value.Value, ranMain bool, err error) {
	ip.Args = args
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("cannot read file %q", path)
	}
	prog, perr := parser.Parse(string(src))
	if perr != nil {
		return nil, false, perr
	}
	abs, _ := filepath.Abs(path)
	ip.pushSrc(srcFrame{dir: filepath.Dir(abs)})
	defer ip.popSrc()

	defer func() {
		if r := recover(); r != nil {
			result, err = ip.recovered(r, result)
		}
	}()
	result = value.TheUnit
	ip.hoistFns(prog.Body, ip.Global)
	for _, s := range prog.Body {
		result = ip.execStmt(s, ip.Global)
	}
	if prog.Mode == "systems" {
		if m, ok := ip.Global.Get("main"); ok {
			if c, ok := m.(*value.Closure); ok && len(c.Params) == 0 {
				result = ip.callClosure(c, nil, c.Body.Pos())
				ranMain = true
			}
		}
	}
	return result, ranMain, nil
}

// hoistFns pre-defines every top-level function in a body before the body runs,
// so a definition may call one that appears later in the file and a top-level
// `let` may call any of them. This matches the checker, which already resolves
// forward references, and how most languages treat file-level functions; without
// it a module-level `let X = f()` would fail whenever f is written below it.
func (ip *Interp) hoistFns(body []ast.Stmt, env *value.Env) {
	for _, s := range body {
		if fn, ok := s.(*ast.FnDecl); ok {
			env.Prebind(fn.Name, &value.Closure{
				Params:  paramNames(fn.Params),
				Body:    fn.Body,
				Env:     env,
				Name:    fn.Name,
				RetUnit: fn.RetUnit,
				RetType: fn.RetType,
			})
		}
	}
}

func (ip *Interp) pushSrc(f srcFrame) { ip.srcStack = append(ip.srcStack, f) }

func (ip *Interp) popSrc() { ip.srcStack = ip.srcStack[:len(ip.srcStack)-1] }

// --- statement execution ---------------------------------------------------

// isI64Anno reports whether a let's annotation is a bare `I64`. The parser puts
// a `.`-or-`[`-free annotation in the unit slot (as a one-factor unit) rather
// than TypeName, so both spellings are checked. Arr[I64], F64, a compound unit
// and no annotation are not I64.
func isI64Anno(typeName string, u *ast.UnitAnno) bool {
	if typeName == "I64" {
		return true
	}
	return u != nil && len(u.Factors) == 1 && u.Factors[0].Name == "I64" && u.Factors[0].Exp == 1
}

// bareTypeName drops a module qualifier, so "resolve.Catalog" gives "Catalog".
func bareTypeName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// annoHead returns the constructor at the head of a type annotation, so that
// "Arr[Str]" gives "Arr" and "Dict[Str, I64]" gives "Dict".
func annoHead(typeName string) string {
	if i := strings.IndexByte(typeName, '['); i >= 0 {
		return typeName[:i]
	}
	return typeName
}

// containerForAnnotation reads a literal at a container annotation as that
// container. A bracket literal is a tensor when its elements are numbers and a
// list otherwise, and a brace literal with no fields is a record: those are the
// right defaults with nothing else to go on, but an annotation is something
// else to go on.
//
//	let seen: Dict[Str, I64] = {}   // a dictionary, not an empty record
//	let want: Arr[I64] = [1]        // a list of one, not a 1-element tensor
//
// Both spellings were unusable as written -- every later dict_set failed, and
// every later arr_push failed -- which is exactly what the annotation was there
// to prevent. Only tensors of rank 0 or 1 convert, since a list is flat; a
// higher-rank tensor at an Arr annotation is left alone rather than silently
// flattened.
func containerForAnnotation(typeName string, v value.Value) (value.Value, bool) {
	switch annoHead(typeName) {
	case "Arr", "List":
		t, ok := v.(*tensor.Tensor)
		if !ok || len(t.Shape) > 1 {
			return nil, false
		}
		items := make([]value.Value, len(t.Data))
		for i, x := range t.Data {
			items[i] = value.Num(x)
		}
		return &value.List{Items: items}, true
	case "Dict":
		if r, ok := v.(*value.Record); ok && len(r.Keys) == 0 {
			return value.NewDict(), true
		}
	}
	return nil, false
}

func (ip *Interp) execStmt(s ast.Stmt, env *value.Env) value.Value {
	// The line of the statement being executed, kept only so that a fault inside
	// the interpreter can say where the program had got to. Nothing reads it on
	// the way through; see recovered.
	ip.line = s.Pos()
	switch st := s.(type) {
	case *ast.Let:
		v := ip.tracedStmt(func() value.Value { return ip.evalExpr(st.Value, env) })
		if st.TypeName != "" || st.Unit != nil {
			ip.escape()
		}
		if out, ok := containerForAnnotation(st.TypeName, v); ok {
			v = out
		}
		if out, ok := tensorForAnnotation(annoName(st.TypeName, st.Unit), v); ok {
			v = out
		}
		// A number bound at an `I64` annotation becomes an Int, and one bound at
		// `F64` a Num: the annotation is where the program says which half of the
		// language the value lives in (see int.go).
		v = ip.coerceScalarAnno(st.TypeName, st.Unit, v, st.Line)
		env.Define(st.Name, v)
		return value.TheUnit
	case *ast.FnDecl:
		env.Define(st.Name, &value.Closure{
			Params:     paramNames(st.Params),
			ParamTypes: paramTypes(st.Params),
			Body:       st.Body,
			Env:        env,
			Name:       st.Name,
			RetUnit:    st.RetUnit,
			RetType:    st.RetType,
		})
		return value.TheUnit
	case *ast.Assign:
		v := ip.tracedStmt(func() value.Value { return ip.evalExpr(st.Value, env) })
		ip.assignTo(st.Target, v, env, st.Line)
		return value.TheUnit
	case *ast.While:
		for ip.truthy(func() value.Value { return ip.evalExpr(st.Cond, env) }) {
			if ip.runLoopBody(st.Body, value.NewEnv(env)) {
				break
			}
		}
		return value.TheUnit
	case *ast.For:
		// `for i in range(n)` is the loop people write, and going through the
		// list costs a slice of n elements and n scalars before the first
		// iteration runs. For range(3000000) that is a 48 MB slice the
		// collector then walks repeatedly, which profiling put at the top of a
		// scalar loop's cost.
		//
		// Counting instead allocates nothing up front. Everything else is
		// unchanged: each iteration still gets its own scope and its own scalar,
		// so a closure that captures the loop variable captures what it did
		// before.
		if start, end, step, ok := ip.rangeLoop(st.Iter, env); ok {
			for x := start; (step > 0 && x < end) || (step < 0 && x > end); x += step {
				scope := value.NewEnv(env)
				scope.Define(st.Name, value.Num(float64(x)))
				if ip.runLoopBody(st.Body, scope) {
					break
				}
			}
			return value.TheUnit
		}
		items := ip.iterate(ip.tracedStmt(func() value.Value { return ip.evalExpr(st.Iter, env) }), st.Line)
		for _, item := range items {
			scope := value.NewEnv(env)
			scope.Define(st.Name, item)
			if ip.runLoopBody(st.Body, scope) {
				break
			}
		}
		return value.TheUnit
	case *ast.Return:
		var v value.Value = value.TheUnit
		if st.Value != nil {
			v = ip.tracedStmt(func() value.Value { return ip.evalExpr(st.Value, env) })
		}
		panic(returnSignal{value: v})
	case *ast.Break:
		panic(breakSignal{})
	case *ast.Continue:
		panic(continueSignal{})
	case *ast.Import:
		ip.doImport(st, env)
		return value.TheUnit
	case *ast.TypeDecl:
		// Types are checked statically and erased at runtime.
		return value.TheUnit
	case *ast.StructDecl:
		// A struct is a record type: checked statically, erased at runtime, since
		// a record carries its own fields and needs no declaration to be built.
		// The field types are kept anyway, so that a typed literal can read an
		// empty `{}` or `[]` at a container-typed field as that container.
		fields := make(map[string]string, len(st.Fields))
		for _, f := range st.Fields {
			fields[f.Name] = f.Type
		}
		ip.structFields[st.Name] = fields
		return value.TheUnit
	case *ast.UnitDecl:
		// Units are checked statically and erased at runtime.
		return value.TheUnit
	case *ast.EnumDecl:
		// Each case becomes a value in scope: a payload case is a one-argument
		// constructor, a payload-less case is the variant value itself. That is
		// what makes `Some(x)` a call and `None` a bare name.
		//
		// A variant is also bound in the global scope, not only the scope the enum
		// is declared in. Variant constructors are program-wide: a module that
		// declares `enum Stmt { SFn(..) }` is imported under an alias, yet other
		// modules construct `SFn(..)` unqualified. The checker already resolves
		// these across modules (see crossModuleVariant); this is the runtime half.
		for _, v := range st.Variants {
			name := v.Name
			ip.variantNames[name] = true
			var ctor value.Value
			if v.HasPayload {
				payloadType := v.Payload
				ip.variantPayloads[name] = payloadType
				ctor = &value.Builtin{Name: name, Arity: 1, Fn: func(a []value.Value) (value.Value, error) {
					p := a[0]
					if isI64Anno(payloadType, nil) || isF64Anno(payloadType, nil) {
						p = ip.coerceScalarAnno(payloadType, nil, p, st.Line)
					}
					return &value.Variant{Name: name, Payload: p, HasPayload: true}, nil
				}}
			} else {
				ctor = &value.Variant{Name: name}
			}
			env.Define(name, ctor)
			if env != ip.Global {
				ip.Global.Define(name, ctor)
			}
		}
		return value.TheUnit
	case *ast.ExprStmt:
		return ip.tracedStmt(func() value.Value { return ip.evalExpr(st.X, env) })
	case *ast.Block:
		return ip.execBlockIn(st, value.NewEnv(env))
	default:
		ip.panicf(s.Pos(), "unsupported statement")
		return value.TheUnit
	}
}

// runLoopBody runs one iteration of a loop body in the given scope, translating
// the loop-control signals: it returns true if a `break` fired (stop the loop),
// false otherwise (a normal end or a `continue`, both of which just move on).
// A returnSignal or a real panic propagates through untouched.
func (ip *Interp) runLoopBody(body *ast.Block, scope *value.Env) (brk bool) {
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case breakSignal:
				brk = true
			case continueSignal:
				brk = false
			default:
				panic(r)
			}
		}
	}()
	ip.execBlockIn(body, scope)
	return false
}

func (ip *Interp) execBlockIn(b *ast.Block, scope *value.Env) value.Value {
	var last value.Value = value.TheUnit
	for _, s := range b.Body {
		last = ip.execStmt(s, scope)
	}
	return last
}

// rangeLoop reads `range(...)` bounds straight off a for-loop's iterable, so the
// loop can count rather than walk a list that was built to be thrown away.
//
// It declines unless the call really is the builtin: a file is free to define
// its own `range`, and quietly running this one instead would be a bug nobody
// could find by reading their own source.
func (ip *Interp) rangeLoop(iter ast.Expr, env *value.Env) (start, end, step int, ok bool) {
	call, isCall := iter.(*ast.Call)
	if !isCall {
		return 0, 0, 0, false
	}
	name, isIdent := call.Callee.(*ast.Ident)
	if !isIdent || name.Name != "range" || len(call.Args) < 1 || len(call.Args) > 3 {
		return 0, 0, 0, false
	}
	// Builtins live in the environment like anything else, so the question is
	// not whether `range` is bound but whether it is still the builtin. A file
	// that defines its own must get its own.
	bound, found := env.Get("range")
	if !found {
		return 0, 0, 0, false
	}
	if b, isBuiltin := bound.(*value.Builtin); !isBuiltin || b.Name != "range" {
		return 0, 0, 0, false
	}

	bounds := make([]int, len(call.Args))
	for i, arg := range call.Args {
		n, isNum := rank0Number(ip.forced(ip.evalExpr(arg, env)))
		if !isNum {
			return 0, 0, 0, false
		}
		bounds[i] = int(n)
	}

	step = 1
	switch len(bounds) {
	case 1:
		end = bounds[0]
	case 2:
		start, end = bounds[0], bounds[1]
	case 3:
		start, end, step = bounds[0], bounds[1], bounds[2]
	}
	if step == 0 {
		// The builtin reports this as an error, and it is the builtin's to
		// report. Falling through hands it back.
		return 0, 0, 0, false
	}
	return start, end, step, true
}

func (ip *Interp) iterate(v value.Value, line int) []value.Value {
	ip.escape()
	switch t := v.(type) {
	case value.Num:
		ip.panicf(line, "can only iterate 1-D tensors")
	case *tensor.Tensor:
		if len(t.Shape) == 1 {
			out := make([]value.Value, len(t.Data))
			for i, x := range t.Data {
				// Indexing a tensor never carried the graph across, so the
				// element is a plain number and stays one.
				out[i] = value.Num(x)
			}
			return out
		}
		ip.panicf(line, "can only iterate 1-D tensors")
	case *value.List:
		return t.Items
	}
	ip.panicf(line, "value is not iterable")
	return nil
}

func (ip *Interp) doImport(st *ast.Import, env *value.Env) {
	mod, err := ip.loadModule(st.Path)
	if err != nil {
		ip.panicf(st.Line, "%s", err.Error())
	}
	prog, perr := parser.Parse(mod.src)
	if perr != nil {
		ip.panicf(st.Line, "in import %q: %s", st.Path, perr.Error())
	}
	ip.pushSrc(mod.frame)
	defer ip.popSrc()

	if st.Alias != "" {
		// Namespaced import: evaluate into a fresh module scope and bind its
		// definitions as a record under the alias. Guard against cycles.
		if ip.loading[mod.key] {
			return
		}
		ip.loading[mod.key] = true
		defer delete(ip.loading, mod.key)

		// A namespaced module gets its own load-once set, because "already
		// loaded" means "already loaded into this scope" and this scope is new.
		// Sharing the outer one meant a plain import at the top level silently
		// hollowed out any namespace that imported the same module: after
		// `import "std/optim"`, the nested plain import inside nn was skipped as
		// already loaded, so `import "std/nn" as nn` came back without any of
		// optim's names. The fresh map still guards cycles within this module.
		outerLoaded := ip.loaded
		ip.loaded = map[string]bool{}
		defer func() { ip.loaded = outerLoaded }()

		// A module scope tracks definition order, so the namespace record's
		// fields come out in declaration order instead of Go map order.
		modEnv := value.NewModuleEnv(ip.Global)
		ip.hoistFns(prog.Body, modEnv)
		for _, s := range prog.Body {
			ip.execStmt(s, modEnv)
		}
		rec := value.NewRecord()
		locals := modEnv.Locals()
		for _, name := range modEnv.LocalNames() {
			rec.Set(name, locals[name])
		}
		env.Define(st.Alias, rec)
		return
	}

	// Plain import: definitions land in the importing scope. Load each module
	// once to keep re-imports and cycles cheap.
	if ip.loaded[mod.key] {
		return
	}
	ip.loaded[mod.key] = true
	ip.hoistFns(prog.Body, env)
	for _, s := range prog.Body {
		ip.execStmt(s, env)
	}
}

// module is an import that has been located and read: its source, the key that
// identifies it for the load-once and cycle caches, and the frame to run it in.
type module struct {
	key   string
	src   string
	frame srcFrame
}

// loadModule reads an import. A "std/" path names a module of the embedded
// standard library; anything else is a file path.
func (ip *Interp) loadModule(path string) (module, error) {
	if name, ok := strings.CutPrefix(path, stdPrefix); ok {
		return loadStd(name)
	}
	if ip.inStd() {
		// A std module is embedded, so it has no directory to resolve a
		// relative path against, and an override directory must not be able to
		// pull in code from outside itself.
		return module{}, fmt.Errorf("a standard-library module may only import other std modules, not %q", path)
	}
	if err := CheckLegacyExt(path); err != nil {
		return module{}, err
	}
	abs, err := ip.resolveImport(path)
	if err != nil {
		return module{}, fmt.Errorf("cannot read import %q", path)
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return module{}, fmt.Errorf("cannot read import %q", path)
	}
	return module{key: abs, src: string(src), frame: srcFrame{dir: filepath.Dir(abs)}}, nil
}

// loadStd reads a standard-library module by name, from the override directory
// if one is configured and from the embedded copy otherwise.
func loadStd(name string) (module, error) {
	if rest, ok := strings.CutSuffix(name, ".tw"); ok {
		return module{}, fmt.Errorf("a standard-library import names a module, not a file: write %q, not %q", stdPrefix+rest, stdPrefix+name)
	}
	if !validStdName(name) {
		return module{}, fmt.Errorf("%q is not a standard-library module name", stdPrefix+name)
	}
	mod := module{key: stdPrefix + name, frame: srcFrame{std: true}}
	if dir := os.Getenv(stdOverrideEnv); dir != "" {
		src, err := os.ReadFile(filepath.Join(dir, name+".tw"))
		if err != nil {
			return module{}, fmt.Errorf("%s is set to %q, which has no module %q", stdOverrideEnv, dir, name)
		}
		mod.src = string(src)
		return mod, nil
	}
	src, ok := std.Read(name)
	if !ok {
		return module{}, fmt.Errorf("no standard-library module %q (the library has %s)", name, strings.Join(std.Names(), ", "))
	}
	mod.src = src
	return mod, nil
}

// validStdName keeps every segment of a module name a plain identifier, so it
// cannot walk out of the library into the rest of the filesystem via the
// override directory. The library groups related modules one level deep
// ("term/caps"), so a name may carry a single separator; each side of it still
// has to be an identifier, which is what rules out "..", "", and absolute paths.
func validStdName(name string) bool {
	if name == "" {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" {
			return false
		}
		for _, r := range seg {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			default:
				return false
			}
		}
	}
	return true
}

// resolveImport looks for an imported file first relative to the importing
// file, then relative to the working directory.
func (ip *Interp) resolveImport(path string) (string, error) {
	if filepath.IsAbs(path) {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return "", os.ErrNotExist
	}
	candidates := []string{filepath.Join(ip.currentDir(), path)}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, path))
	}
	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			abs, _ := filepath.Abs(cand)
			return abs, nil
		}
	}
	return "", os.ErrNotExist
}

// --- expression evaluation -------------------------------------------------

func (ip *Interp) evalExpr(e ast.Expr, env *value.Env) value.Value {
	switch ex := e.(type) {
	case *ast.NumberLit:
		if i, ok := intLiteral(ex, false); ok {
			return i
		}
		return value.Num(ex.Value)
	case *ast.StringLit:
		return value.Str(ex.Value)
	case *ast.BoolLit:
		return value.Bool(ex.Value)
	case *ast.Ident:
		v, ok := env.Get(ex.Name)
		if !ok {
			ip.panicf(ex.Line, "undefined variable %q", ex.Name)
		}
		return v
	case *ast.TensorLit:
		nested := ip.tensorNested(ex.Elements, ex.Line)
		t, err := tensor.FromNested(nested)
		if err != nil {
			ip.panicf(ex.Line, "%s", err.Error())
		}
		return t
	case *ast.ListLit:
		items := make([]value.Value, len(ex.Elements))
		for i, el := range ex.Elements {
			items[i] = ip.evalExpr(el, env)
		}
		return &value.List{Items: items}
	case *ast.Lambda:
		return &value.Closure{
			Params:     paramNames(ex.Params),
			ParamTypes: paramTypes(ex.Params),
			Body:       ex.Body,
			Env:        env,
			Name:       "",
			RetUnit:    ex.RetUnit,
			RetType:    ex.RetType,
		}
	case *ast.Unary:
		return ip.evalUnary(ex, env)
	case *ast.Binary:
		return ip.evalBinary(ex, env)
	case *ast.Call:
		return ip.evalCall(ex, env)
	case *ast.Index:
		return ip.evalIndex(ex, env)
	case *ast.Slice:
		return ip.evalSlice(ex, env)
	case *ast.RecordLit:
		rec := value.NewRecord()
		// A typed literal reads its declared field types, so `Catalog { versions:
		// {} }` gets a dictionary at a Dict-typed field, the same rule a `let`
		// annotation follows. The name is looked up unqualified: `resolve.Catalog`
		// and `Catalog` name the same declaration, and the only thing a wrong hit
		// could do is turn an empty literal into an empty container, which is what
		// the field wanted in every case where it matters.
		declared := ip.structFields[bareTypeName(ex.TypeName)]
		if declared != nil {
			rec.TypeName = bareTypeName(ex.TypeName)
		}
		for _, f := range ex.Fields {
			v := ip.evalExpr(f.Value, env)
			if t, ok := declared[f.Name]; ok {
				v = ip.coerceField(t, v, f.Value.Pos())
			}
			rec.Set(f.Name, v)
		}
		return rec
	case *ast.Field:
		if v, ok := ip.qualifiedVariant(ex.Target, ex.Name, env); ok {
			return v
		}
		return ip.recordField(ip.evalExpr(ex.Target, env), ex.Name, ex.Line)
	case *ast.IfExpr:
		if ip.truthy(func() value.Value { return ip.evalExpr(ex.Cond, env) }) {
			return ip.execBlockIn(ex.Then, value.NewEnv(env))
		}
		switch alt := ex.Else.(type) {
		case nil:
			return value.TheUnit
		case *ast.Block:
			return ip.execBlockIn(alt, value.NewEnv(env))
		case *ast.IfExpr:
			return ip.evalExpr(alt, env)
		}
		return value.TheUnit
	case *ast.Match:
		subj := ip.evalExpr(ex.Subject, env)
		for _, arm := range ex.Arms {
			// Each arm gets its own scope, and the pattern binds into it as it
			// matches: `Ok(Some(v))` puts the innermost payload in `v`. A
			// failed match leaves the scope to be discarded, so a partially
			// bound arm cannot leak a name into the next one.
			armEnv := value.NewEnv(env)
			if !ip.matchPattern(arm.Pattern, subj, armEnv) {
				continue
			}
			// The guard sees the bindings, and is the last word on whether the
			// arm runs: a false guard falls through to the arms below.
			if arm.Guard != nil && !ip.truthy(func() value.Value { return ip.evalExpr(arm.Guard, armEnv) }) {
				continue
			}
			return ip.execStmt(arm.Body, armEnv)
		}
		ip.panicf(ex.Line, "no match arm for %s", value.Format(subj))
		return value.TheUnit
	case *ast.Try:
		v := ip.evalExpr(ex.Expr, env)
		variant, ok := v.(*value.Variant)
		if !ok {
			ip.panicf(ex.Line, "`?` expects a Res or Opt value, got %s", value.Format(v))
		}
		// A success case yields its payload; a failure case is returned whole
		// from the enclosing function, which is what propagates the error.
		if variant.Name == "Ok" || variant.Name == "Some" {
			return variant.Payload
		}
		// At the top of a file there is no function to return the failure from,
		// so `?` there used to end the program quietly with exit status 0 -- the
		// one thing a failed read must not do. It is an error naming the value.
		if ip.callDepth == 0 {
			ip.panicf(ex.Line, "`?` outside a function: the value was %s", value.Format(variant))
		}
		panic(returnSignal{value: variant})
	case *ast.Block:
		return ip.execBlockIn(ex, value.NewEnv(env))
	default:
		ip.panicf(e.Pos(), "unsupported expression")
		return value.TheUnit
	}
}

func (ip *Interp) tensorNested(elements []ast.Expr, line int) []any {
	defer ip.escape()
	out := make([]any, len(elements))
	for i, e := range elements {
		switch el := e.(type) {
		case *ast.NumberLit:
			out[i] = el.Value
		case *ast.Unary:
			num, ok := el.Operand.(*ast.NumberLit)
			if el.Op == "-" && ok {
				out[i] = -num.Value
			} else {
				ip.panicf(line, "invalid element in tensor literal")
			}
		case *ast.TensorLit:
			out[i] = ip.tensorNested(el.Elements, line)
		default:
			ip.panicf(line, "invalid element in tensor literal")
		}
	}
	return out
}

func (ip *Interp) evalUnary(ex *ast.Unary, env *value.Env) value.Value {
	if ex.Op == "-" {
		// `-9223372036854775808` is MIN_I64 spelled the only way it can be: the
		// magnitude does not fit, its negation does, so the literal is read under
		// the minus rather than after it.
		if lit, ok := ex.Operand.(*ast.NumberLit); ok {
			if i, ok := intLiteral(lit, true); ok {
				return i
			}
		}
	}
	v := ip.evalExpr(ex.Operand, env)
	if ex.Op == "-" {
		if n, isNum := v.(value.Num); isNum {
			return -n
		}
		if i, isInt := v.(value.Int); isInt {
			return -i
		}
		t, ok := v.(*tensor.Tensor)
		if !ok {
			ip.panicf(ex.Line, "unary '-' expects a number/tensor")
		}
		if out, traced := phv(ip.tr.Unary(ir.OpNeg, t)); traced {
			return out
		}
		ip.escape()
		return tensor.Neg(t)
	}
	return value.Bool(!value.Truthy(ip.forced(v)))
}

func (ip *Interp) evalBinary(ex *ast.Binary, env *value.Env) value.Value {
	op := ex.Op
	// Short-circuiting logic.
	if op == "and" || op == "&&" {
		l := ip.forced(ip.evalExpr(ex.Left, env))
		if !value.Truthy(l) {
			return l
		}
		return ip.evalExpr(ex.Right, env)
	}
	if op == "or" || op == "||" {
		l := ip.forced(ip.evalExpr(ex.Left, env))
		if value.Truthy(l) {
			return l
		}
		return ip.evalExpr(ex.Right, env)
	}

	l := ip.evalExpr(ex.Left, env)
	r := ip.evalExpr(ex.Right, env)

	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		// A comparison reads the values it compares, so docs/CODEGEN.md section 2
		// makes it a forcing point. twill's comparison operators produce a Bool
		// and not a mask tensor, so there is nothing to record here even though
		// the IR carries the six comparison opcodes; those are reached through
		// `where` and through the gradient transform.
		return value.Bool(ip.compare(op, l, r, ex.Line))
	case "+", "-", "*", "/", "%", "//":
		// An I64 operation: both sides Int, or an Int and an integral number.
		// Wrapping arithmetic, truncating division, dividend-signed modulo.
		if x, y, isInt := intOperands(l, r); isInt {
			ip.escape()
			out, err := intArith(op, x, y)
			if err != nil {
				ip.panicf(ex.Line, "%s", err.Error())
			}
			return out
		}
	}
	switch op {
	// The bitwise words are the same operation infix as they are called: one
	// definition, in bitwiseInfix, so `x shr 8` and `shr(x, 8)` cannot drift.
	// `//` divides and truncates toward zero: the integer division every
	// `(n + k - 1) // k` and midpoint wants. `/` stays exact float division, so
	// the two intents are written down rather than inferred from the operands --
	// numbers all run as float64 here and there is nothing to infer from.
	case "//":
		ip.escape()
		ln, lok := rank0Number(l)
		rn, rok := rank0Number(r)
		if !lok || !rok {
			ip.panicf(ex.Line, "operator %q needs numbers", op)
		}
		if rn == 0 {
			ip.panicf(ex.Line, "integer division by zero")
		}
		return value.Num(math.Trunc(ln / rn))
	case "band", "bor", "xor", "shl", "shr":
		ip.escape()
		out, err := bitwiseInfix(op, l, r)
		if err != nil {
			ip.panicf(ex.Line, "%s", err.Error())
		}
		return out
	}

	// `+` concatenates two strings. The tensor engine has no notion of a string,
	// so this is handled before the widen to AsTensor rather than after it. Only
	// string+string concatenates; a string with anything else is still the error
	// AsTensor reports, so `"n=" + 3` stays a mistake (str() is how you mean it).
	if op == "+" {
		if ls, lok := l.(value.Str); lok {
			if rs, rok := r.(value.Str); rok {
				return value.Str(string(ls) + string(rs))
			}
		}
	}

	// Two plain numbers are the whole of an interpreted scalar loop, and going
	// through the tensor engine to add them allocates a rank-0 tensor for the
	// answer that nothing will ever differentiate. Neither operand can be
	// carrying a graph here, because a Num never has one.
	if ln, lIsNum := l.(value.Num); lIsNum {
		if rn, rIsNum := r.(value.Num); rIsNum {
			if res, handled := numArith(op, float64(ln), float64(rn)); handled {
				return res
			}
		}
	}

	lt, lok := value.AsTensor(l)
	rt, rok := value.AsTensor(r)
	if !lok || !rok {
		ip.panicf(ex.Line, "operator %q expects numbers/tensors", op)
	}

	// The traced path. A recorded operation appends an IR node and hands back a
	// placeholder carrying the shape its operands already fix, which is
	// docs/CODEGEN.md section 2's reason for tracing rather than lowering: the
	// shape is a fact about the values, not a claim the checker had to prove.
	if out, traced := ip.traceBinaryOp(op, lt, rt); traced {
		return out
	}
	ip.escape()

	var res *tensor.Tensor
	var err error
	switch op {
	case "+":
		res, err = tensor.Add(lt, rt)
	case "-":
		res, err = tensor.Sub(lt, rt)
	case "*":
		res, err = tensor.Mul(lt, rt)
	case "/":
		res, err = tensor.Div(lt, rt)
	case "%":
		res, err = tensor.Mod(lt, rt)
	case "@":
		res, err = tensor.MatMul(lt, rt)
	case "^":
		if !rt.IsScalar() {
			ip.panicf(ex.Line, "exponent must be a scalar")
		}
		res = tensor.PowScalar(lt, rt.Data[0])
	default:
		ip.panicf(ex.Line, "unknown operator %q", op)
	}
	if err != nil {
		ip.panicf(ex.Line, "%s", err.Error())
	}
	return res
}

// numArith does scalar arithmetic without building a tensor. It reports false
// for operators whose meaning is not purely scalar (`@`) or that it does not
// know, leaving those to the tensor engine so there is one definition of each.
//
// The results have to match broadcastBinary's element functions exactly, so a
// program cannot tell which path evaluated it.
func numArith(op string, x, y float64) (value.Value, bool) {
	switch op {
	case "+":
		return value.Num(x + y), true
	case "-":
		return value.Num(x - y), true
	case "*":
		return value.Num(x * y), true
	case "/":
		return value.Num(x / y), true
	case "%":
		return value.Num(x - math.Floor(x/y)*y), true
	case "^":
		return value.Num(math.Pow(x, y)), true
	}
	return nil, false
}

// rank0Number reads a value that is a single number of no shape: a plain Num,
// or a rank-0 tensor. A one-element vector is deliberately excluded, because it
// was before Num existed and shape is part of what a tensor means.
func rank0Number(v value.Value) (float64, bool) {
	switch t := v.(type) {
	case value.Num:
		return float64(t), true
	case value.Int:
		return float64(t), true
	case *tensor.Tensor:
		if t.IsScalar() {
			return t.Data[0], true
		}
	}
	return 0, false
}

func (ip *Interp) compare(op string, l, r value.Value, line int) bool {
	ip.escape()
	// Scalar comparison covers loop conditions and guards, so it reads the
	// numbers out directly rather than widening either side to a tensor first.
	// An Int on either side compares exactly as I64, so two values that differ
	// only above 2^53 are not equal through an f64 that cannot tell them apart.
	if x, y, isInt := intOperands(l, r); isInt {
		if res, ok := intCompare(op, x, y); ok {
			return res
		}
	}
	a, aok := rank0Number(l)
	b, bok := rank0Number(r)
	if aok && bok {
		switch op {
		case "==":
			return a == b
		case "!=":
			return a != b
		case "<":
			return a < b
		case "<=":
			return a <= b
		case ">":
			return a > b
		case ">=":
			return a >= b
		}
	}
	if op == "==" || op == "!=" {
		eq := deepEqual(l, r)
		if op == "==" {
			return eq
		}
		return !eq
	}
	// Two strings order by their bytes. The ordering already existed -- `sort`
	// has always accepted an Arr[Str] -- it just had no operator, so every
	// caller that wanted a sorted list of names wrote its own compare_str
	// returning -1/0/1 and compared that against zero. Byte order rather than
	// anything locale-aware is the same choice `sort` makes, and it is the one
	// that gives the same answer on every machine.
	if ls, lok := l.(value.Str); lok {
		if rs, rok := r.(value.Str); rok {
			switch op {
			case "<":
				return string(ls) < string(rs)
			case "<=":
				return string(ls) <= string(rs)
			case ">":
				return string(ls) > string(rs)
			case ">=":
				return string(ls) >= string(rs)
			}
		}
	}
	ip.panicf(line, "cannot compare these values with %q", op)
	return false
}

// deepEqual is `==` for everything the scalar fast path above does not cover.
// It compares structurally: two separately built lists or records are equal
// when their contents are, which is what makes `a == a` hold for a model's
// parameter tree. Values of different types are never equal, and functions,
// which have no structure worth walking, compare by identity.
func deepEqual(l, r value.Value) bool {
	// A number equals a rank-0 tensor holding it: whether a value went down the
	// unboxed path is an implementation detail, not something `==` may see.
	if x, y, isInt := intOperands(l, r); isInt {
		return x == y
	}
	if li, isInt := l.(value.Int); isInt {
		// An Int against a fractional number, or against a non-number.
		rn, isNum := rank0Number(r)
		return isNum && float64(li) == rn
	}
	if ri, isInt := r.(value.Int); isInt {
		ln, isNum := rank0Number(l)
		return isNum && ln == float64(ri)
	}
	if ln, ok := l.(value.Num); ok {
		rn, isNum := rank0Number(r)
		return isNum && float64(ln) == rn
	}
	if rn, ok := r.(value.Num); ok {
		ln, isNum := rank0Number(l)
		return isNum && ln == float64(rn)
	}
	switch lv := l.(type) {
	case *tensor.Tensor:
		rv, ok := r.(*tensor.Tensor)
		if !ok || !intsEqual(lv.Shape, rv.Shape) || len(lv.Data) != len(rv.Data) {
			return false
		}
		for i := range lv.Data {
			if lv.Data[i] != rv.Data[i] {
				return false
			}
		}
		return true
	case value.Bool:
		rv, ok := r.(value.Bool)
		return ok && lv == rv
	case value.Str:
		rv, ok := r.(value.Str)
		return ok && lv == rv
	case value.Unit:
		_, ok := r.(value.Unit)
		return ok
	case *value.List:
		rv, ok := r.(*value.List)
		if !ok || len(lv.Items) != len(rv.Items) {
			return false
		}
		for i := range lv.Items {
			if !deepEqual(lv.Items[i], rv.Items[i]) {
				return false
			}
		}
		return true
	case *value.Record:
		rv, ok := r.(*value.Record)
		if !ok || len(lv.Keys) != len(rv.Keys) {
			return false
		}
		// Fields are matched by name, so the order they were declared in does
		// not change the answer.
		for _, k := range lv.Keys {
			other, has := rv.Get(k)
			if !has || !deepEqual(lv.Fields[k], other) {
				return false
			}
		}
		return true
	case *value.Variant:
		// An enum value is its case and its payload: `Some(1) == Some(1)`, the
		// way `[1] == [1]`. It used to fall through to identity below, so two
		// separately built Ok(3) were unequal, which no caller expected.
		rv, ok := r.(*value.Variant)
		if !ok || lv.Name != rv.Name || lv.HasPayload != rv.HasPayload {
			return false
		}
		return !lv.HasPayload || deepEqual(lv.Payload, rv.Payload)
	case *value.Dict:
		rv, ok := r.(*value.Dict)
		if !ok || len(lv.Keys) != len(rv.Keys) {
			return false
		}
		for _, k := range lv.Keys {
			other, has := rv.Get(k)
			if !has || !deepEqual(lv.Map[k], other) {
				return false
			}
		}
		return true
	case *value.Bytes:
		rv, ok := r.(*value.Bytes)
		return ok && string(lv.Data) == string(rv.Data)
	case *value.Closure:
		rv, ok := r.(*value.Closure)
		return ok && lv == rv
	case *value.Builtin:
		rv, ok := r.(*value.Builtin)
		return ok && lv == rv
	}
	// A native opaque value (a fitted gbm model) also compares by identity.
	// Comparing interfaces whose dynamic type is uncomparable panics, so check
	// the type before trusting ==.
	lt, rt := reflect.TypeOf(l), reflect.TypeOf(r)
	if lt != rt {
		return false
	}
	return lt == nil || (lt.Comparable() && l == r)
}

func intsEqual(a, b []int) bool {
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

func (ip *Interp) evalCall(ex *ast.Call, env *value.Env) value.Value {
	// A call whose callee is a field access, target.name(args). The dtype cast
	// target.to(dt) lives here, read from the syntax before the field or the
	// arguments are evaluated -- `to` is not a real field and a dtype name is not
	// a value, so `x.to(f32)` casts even where f32 is bound (mirrors the
	// self-hosted eval_cast). Anything else is an ordinary call of a function held
	// in a record field; the target is evaluated once and reused either way.
	if fld, ok := ex.Callee.(*ast.Field); ok {
		// `Opt.Some(x)` names the variant by its enum, the same spelling a match
		// arm uses. The enum name is not a value, so this is resolved before the
		// target is evaluated -- evaluating `Opt` would be an undefined variable.
		if ctor, isVariant := ip.qualifiedVariant(fld.Target, fld.Name, env); isVariant {
			return ip.Apply(ctor, ip.evalArgs(ex.Args, env), ex.Line)
		}
		target := ip.evalExpr(fld.Target, env)
		// A cast applies to a scalar as much as to a tensor. A plain number is
		// carried as a Num rather than a rank-0 tensor -- an allocation the
		// evaluator avoids where nothing can be differentiating it -- and that is
		// an internal distinction a program cannot see, so `x.to(f32)` has to work
		// on either. A tree of f64 leaves mapped to f32 masters is the case that
		// found this: whichever leaves happened to be Num failed.
		if fld.Name == "to" {
			if t, isTensor := target.(*tensor.Tensor); isTensor {
				return ip.evalCast(t, ex)
			}
			if n, isNum := target.(value.Num); isNum {
				return ip.evalCast(tensor.Scalar(float64(n)), ex)
			}
		}
		// `xs.push(v)` calls push(xs, v). A record field holding a function is
		// still preferred, so a namespace (`m.f(x)`, an import alias) and a record
		// of closures keep their meaning; this is the fallback for everything
		// else, where the field does not exist and the name is a function that
		// takes the target first. It is the shape every caller already writes for
		// the container operations, and without it a list had a `len(xs)` and no
		// `xs.len()` for no reason a reader could see.
		if fn, ok := ip.uniformCallee(target, fld.Name, env); ok {
			args := append([]value.Value{target}, ip.evalArgs(ex.Args, env)...)
			return ip.Apply(fn, args, ex.Line)
		}
		callee := ip.recordField(target, fld.Name, ex.Line)
		return ip.Apply(callee, ip.evalArgs(ex.Args, env), ex.Line)
	}
	callee := ip.evalExpr(ex.Callee, env)
	// A trailing dtype name on a tensor constructor, `zeros([2, 3], bf16)`. The
	// name is contextual, not reserved: it counts as a dtype only on a maker, only
	// when it is one of the seven, and only when nothing in scope binds it, so a
	// program using f32 as an ordinary variable keeps its meaning. The maker builds
	// its f64 tensor from the remaining arguments and the result is cast once,
	// which rounds and tags it.
	if b, ok := callee.(*value.Builtin); ok && dtypeMakers[b.Name] && len(ex.Args) > 0 {
		if dt, ok := contextualDType(ex.Args[len(ex.Args)-1], env); ok {
			rest := ip.evalArgs(ex.Args[:len(ex.Args)-1], env)
			out := ip.Apply(callee, rest, ex.Line)
			if t, ok := out.(*tensor.Tensor); ok {
				return tensor.Cast(t, dt)
			}
			return out
		}
	}
	return ip.Apply(callee, ip.evalArgs(ex.Args, env), ex.Line)
}

// matchPattern matches one pattern against one value, defining the pattern's
// bindings in env as it goes. It reports whether the value fits.
//
// The bindings are written into env even on the way to a failure, which is
// safe because the caller gives every arm a fresh scope and throws it away
// when this returns false.
func (ip *Interp) matchPattern(pat ast.MatchPattern, v value.Value, env *value.Env) bool {
	switch pat.Kind {
	case ast.PatBinding:
		if pat.Binding != "" {
			env.Define(pat.Binding, v)
		}
		return true
	case ast.PatLiteral:
		return ip.patternLiteralEquals(pat, v)
	case ast.PatVariant:
		variant, ok := v.(*value.Variant)
		if !ok || variant.Name != pat.Variant {
			return false
		}
		if pat.Sub == nil {
			// `Some` with no parentheses matches the case and ignores what it
			// carries, which is how a payload-less case is written too.
			return true
		}
		if !variant.HasPayload {
			// A pattern asking about a payload that is not there matches
			// nothing, rather than matching a unit that was never written.
			return false
		}
		return ip.matchPattern(*pat.Sub, variant.Payload, env)
	}
	return false
}

// patternLiteralEquals compares a value against a literal written in a
// pattern. It is the same equality `==` gives, evaluated through the ordinary
// path so that an I64 written in a pattern compares exactly and a numeric
// literal compares against a tensor scalar the way the rest of the language
// does.
func (ip *Interp) patternLiteralEquals(pat ast.MatchPattern, v value.Value) bool {
	lit := ip.evalExpr(pat.Lit, ip.Global)
	return ip.compare("==", lit, v, pat.Line)
}

// qualifiedVariant resolves `Enum.Variant` to the variant itself. The qualifier
// says which enum the variant belongs to and nothing more -- variants are
// resolved by name -- so it is accepted wherever the bare name is, and answers
// only when the qualifier is not a bound value (a record field access on a real
// variable keeps its meaning) and the name is a variant.
func (ip *Interp) qualifiedVariant(target ast.Expr, name string, env *value.Env) (value.Value, bool) {
	id, isIdent := target.(*ast.Ident)
	if !isIdent {
		return nil, false
	}
	if _, bound := env.Get(id.Name); bound {
		return nil, false
	}
	if !ip.variantNames[name] {
		return nil, false
	}
	v, ok := env.Get(name)
	if !ok {
		return nil, false
	}
	return v, true
}

// uniformCallee resolves `target.name(...)` to the function `name` called with
// target as its first argument. It answers only when the target does not
// already carry that field, so a record or a module namespace keeps the field
// call it means, and only when the name is in scope as something callable.
func (ip *Interp) uniformCallee(target value.Value, name string, env *value.Env) (value.Value, bool) {
	if rec, isRec := target.(*value.Record); isRec {
		if _, has := rec.Get(name); has {
			return nil, false
		}
	}
	fn, ok := env.Get(name)
	if !ok {
		return nil, false
	}
	switch fn.(type) {
	case *value.Builtin, *value.Closure:
		return fn, true
	}
	return nil, false
}

// dtypeMakers are the tensor constructors whose trailing argument may be a dtype
// name (docs/dtypes.md). seed and permutation are the makers left out: neither
// builds a tensor.
var dtypeMakers = map[string]bool{
	"tensor": true, "scalar": true, "zeros": true, "ones": true,
	"fill": true, "randn": true, "rand": true, "eye": true,
}

// contextualDType reads an expression as a trailing dtype name: a bare
// identifier that names one of the seven dtypes and that the environment does
// not bind. A binding wins, since the same trailing position holds shape
// dimensions and an ordinary variable there has to keep meaning one.
func contextualDType(e ast.Expr, env *value.Env) (tensor.DType, bool) {
	id, ok := e.(*ast.Ident)
	if !ok {
		return 0, false
	}
	if _, bound := env.Get(id.Name); bound {
		return 0, false
	}
	return tensor.DTypeOfName(id.Name)
}

// evalArgs evaluates a call's arguments left to right.
func (ip *Interp) evalArgs(exprs []ast.Expr, env *value.Env) []value.Value {
	args := make([]value.Value, len(exprs))
	for i, a := range exprs {
		args[i] = ip.evalExpr(a, env)
	}
	return args
}

// recordField reads a field off an already-evaluated value, the shared body of
// field access whether it appears alone or as a call's callee.
func (ip *Interp) recordField(target value.Value, name string, line int) value.Value {
	rec, ok := target.(*value.Record)
	if !ok {
		ip.panicf(line, "cannot access field %q of a non-record", name)
	}
	v, ok := rec.Get(name)
	if !ok {
		ip.panicf(line, "record has no field %q", name)
	}
	return v
}

// evalCast carries out target.to(dt): the dtype name is read straight from the
// syntax, since it is contextual and not a value, and the cast rounds once.
func (ip *Interp) evalCast(t *tensor.Tensor, ex *ast.Call) value.Value {
	ip.escape()
	if len(ex.Args) != 1 {
		ip.panicf(ex.Line, "to expects 1 argument(s), got %d", len(ex.Args))
	}
	id, ok := ex.Args[0].(*ast.Ident)
	if ok {
		if dt, ok := tensor.DTypeOfName(id.Name); ok {
			return tensor.Cast(t, dt)
		}
	}
	ip.panicf(ex.Line, "to expects a dtype name (bool, i8, i32, f16, bf16, f32, f64)")
	return value.TheUnit
}

// Apply calls a closure or builtin.
func (ip *Interp) Apply(callee value.Value, args []value.Value, line int) value.Value {
	switch fn := callee.(type) {
	case *value.Builtin:
		if !fn.Variadic && fn.Arity >= 0 && fn.Arity != len(args) {
			ip.panicf(line, "%s expects %d argument(s), got %d", fn.Name, fn.Arity, len(args))
		}
		// A builtin either has an opcode, in which case it is recorded, or it has
		// none, in which case it reads its arguments and the trace forces. This is
		// the single funnel every builtin in the language goes through, which is
		// what makes "any builtin with no opcode forces" one line rather than an
		// audit of builtins.go.
		if out, traced := ip.traceBuiltin(fn.Name, args); traced {
			return out
		}
		ip.escape()
		v, err := fn.Fn(args)
		if err != nil {
			if re, ok := err.(*RuntimeError); ok {
				panic(re)
			}
			ip.panicf(line, "%s", err.Error())
		}
		return v
	case *value.Closure:
		if len(fn.Params) != len(args) {
			name := fn.Name
			if name == "" {
				name = "function"
			}
			ip.panicf(line, "%s expects %d argument(s), got %d", name, len(fn.Params), len(args))
		}
		return ip.callClosure(fn, args, line)
	default:
		ip.panicf(line, "value is not callable: %s", value.Format(callee))
		return value.TheUnit
	}
}

// callClosure calls c with args. line is the source line of the call, used to
// point the recursion refusal at the call site.
func (ip *Interp) callClosure(c *value.Closure, args []value.Value, line int) (ret value.Value) {
	// The check is on the way in, before the frame is entered, so the refusal
	// unwinds from a stack that still has room to unwind through. Checking after
	// the fact is not available: Go's stack overflow is fatal and cannot be
	// recovered from.
	if ip.callDepth >= ip.MaxCallDepth {
		ip.panicf(line, "%s", callDepthMessage(c.Name, ip.MaxCallDepth))
	}
	ip.callDepth++
	defer func() { ip.callDepth-- }()
	scope := value.NewEnv(c.Env)
	for i, p := range c.Params {
		arg := args[i]
		if i < len(c.ParamTypes) && c.ParamTypes[i] != "" {
			arg = ip.coerceScalarAnno(c.ParamTypes[i], nil, arg, c.Body.Pos())
		}
		scope.Define(p, arg)
	}
	// `-> I64` truncates the result toward zero, the same rule `let n: I64 = ...`
	// applies to a bound value. Numbers run as float64, so the integer idioms --
	// a ceiling division `(n + k - 1) / k`, a midpoint, an index -- come back
	// fractional, and a function that promised an I64 was handing one back. The
	// named return is the only place the promise is written down, so it is the
	// place to keep it. A real I64 runtime divides as integers and this is a
	// no-op.
	defer func() {
		if r := recover(); r != nil {
			rs, ok := r.(returnSignal)
			if !ok {
				panic(r)
			}
			ret = ip.coerceReturn(c, rs.value)
			return
		}
	}()
	if blk, ok := c.Body.(*ast.Block); ok {
		return ip.coerceReturn(c, ip.execBlockIn(blk, scope))
	}
	return ip.coerceReturn(c, ip.evalExpr(c.Body, scope))
}

// coerceReturn applies a closure's return annotation to the value it produced.
// A return annotation reads the value, so it forces (docs/CODEGEN.md 2.1).
func (ip *Interp) coerceReturn(c *value.Closure, v value.Value) value.Value {
	if c.RetType == "" && c.RetUnit == nil {
		return v
	}
	ip.escape()
	if isI64Anno(c.RetType, c.RetUnit) || isF64Anno(c.RetType, c.RetUnit) {
		return ip.coerceScalarAnno(c.RetType, c.RetUnit, v, c.Body.Pos())
	}
	if out, ok := tensorForAnnotation(annoName(c.RetType, c.RetUnit), v); ok {
		return out
	}
	return v
}

// annoName is the annotation's name whichever slot the parser put it in: a bare
// name arrives as a one-factor unit, a qualified or generic one as text.
func annoName(typeName string, u *ast.UnitAnno) string {
	if typeName != "" {
		return typeName
	}
	if u != nil && len(u.Factors) == 1 && u.Factors[0].Exp == 1 {
		return u.Factors[0].Name
	}
	return ""
}

// coerceField applies a struct field's declared type to a value stored in it:
// an empty literal at a container type builds the container, and a number at
// an I64 or F64 field converts, the same two rules a `let` annotation follows.
func (ip *Interp) coerceField(fieldType string, v value.Value, line int) value.Value {
	if out, converted := containerForAnnotation(fieldType, v); converted {
		return out
	}
	return ip.coerceScalarAnno(fieldType, nil, v, line)
}

// tensorForAnnotation reads a list of numbers at a `Tensor` annotation as a
// tensor. A bracket literal is a tensor when its elements are numeric literals
// and a list otherwise, so `[1.0, 2.0]` is a tensor but `[v, v]` is not, and a
// function returning the second one had annotated it `-> Tensor` and meant it:
//
//	fn row(v: F64) -> Tensor = [v, v, v, v]
//
// That returned a list, and every shape() on it downstream failed. Only a flat
// list of numbers converts; anything holding a string, a record or a nested
// list is left alone, since that is a list the caller built on purpose.
func tensorForAnnotation(name string, v value.Value) (value.Value, bool) {
	if name != "Tensor" {
		return nil, false
	}
	l, ok := v.(*value.List)
	if !ok {
		return nil, false
	}
	data := make([]float64, len(l.Items))
	for i, it := range l.Items {
		n, isNum := rank0Number(it)
		if !isNum {
			return nil, false
		}
		data[i] = n
	}
	return tensor.New(data, []int{len(data)}), true
}

// assignTo stores v into the location named by target: a variable, a record
// field, or an element of a list or flat tensor. Records and lists are
// reference values, so a field or element write is visible through every
// binding that shares them, which is what makes `a.d[i] = v` mutate the object.
func (ip *Interp) assignTo(target ast.Expr, v value.Value, env *value.Env, line int) {
	ip.escape()
	switch t := target.(type) {
	case *ast.Ident:
		if !env.Assign(t.Name, v) {
			ip.panicf(line, "cannot assign to undefined variable %q (use 'let' first)", t.Name)
		}
	case *ast.Field:
		obj := ip.evalExpr(t.Target, env)
		rec, ok := obj.(*value.Record)
		if !ok {
			ip.panicf(t.Line, "cannot set field %q of a non-record", t.Name)
		}
		// A struct's field keeps its declared type across an assignment, so
		// `lx.i = lx.i + 1` on an I64 field stays an I64.
		if rec.TypeName != "" {
			if ft, declared := ip.structFields[rec.TypeName][t.Name]; declared {
				v = ip.coerceField(ft, v, t.Line)
			}
		}
		rec.Set(t.Name, v)
	case *ast.Index:
		// `m[i][j] = v` on a tensor of rank 2 or more, resolved to one write
		// into the tensor itself.
		//
		// Indexing a tensor row hands back a copy, not a view, so the ordinary
		// path below wrote into that copy and the assignment vanished: no
		// change, no error, nothing. A list of lists is unaffected because a
		// list is a handle and the inner one is shared, which is why this was
		// only ever wrong for tensors.
		if ip.assignNestedTensor(t, v, env) {
			return
		}
		obj := ip.evalExpr(t.Target, env)
		idxVal := ip.forced(ip.evalExpr(t.Index, env))
		// A dictionary is subscripted by its key, `counts[tok] = n`, which is how
		// every caller writes it and reads the same as the list and tensor cases
		// just below. dict_set is the same store under a name.
		if d, isDict := obj.(*value.Dict); isDict {
			k, isStr := idxVal.(value.Str)
			if !isStr {
				ip.panicf(t.Line, "a dictionary key must be a string")
			}
			d.Set(string(k), v)
			return
		}
		n, ok := rank0Number(idxVal)
		if !ok {
			ip.panicf(t.Line, "index must be a scalar number")
		}
		idx := int(n)
		switch c := obj.(type) {
		case *value.List:
			if idx < 0 || idx >= len(c.Items) {
				ip.panicf(t.Line, "list index %d out of range", idx)
			}
			c.Items[idx] = v
		case *tensor.Tensor:
			s, ok := rank0Number(v)
			if !ok {
				ip.panicf(t.Line, "can only assign a scalar to a tensor element")
			}
			if idx < 0 || idx >= len(c.Data) {
				ip.panicf(t.Line, "tensor index %d out of range", idx)
			}
			c.Data[idx] = s
		default:
			ip.panicf(t.Line, "value is not indexable")
		}
	default:
		ip.panicf(line, "cannot assign to this expression")
	}
}

// assignNestedTensor handles `m[i][j]... = v` where the chain bottoms out at a
// tensor, writing the one element the indices name. It reports whether it
// handled the assignment; a single index, or a chain over anything that is not
// a tensor, is left to the ordinary path.
func (ip *Interp) assignNestedTensor(target *ast.Index, v value.Value, env *value.Env) bool {
	base, idxExprs := indexChain(target)
	if len(idxExprs) < 2 {
		return false
	}
	t, isTensor := ip.evalExpr(base, env).(*tensor.Tensor)
	if !isTensor {
		return false
	}
	if len(idxExprs) > len(t.Shape) {
		ip.panicf(target.Line, "%d indices for a tensor of rank %d", len(idxExprs), len(t.Shape))
	}
	// Row-major: each index steps by the product of the dimensions below it.
	offset, stride := 0, 1
	for _, d := range t.Shape[len(idxExprs):] {
		stride *= d
	}
	for k := len(idxExprs) - 1; k >= 0; k-- {
		n, ok := rank0Number(ip.forced(ip.evalExpr(idxExprs[k], env)))
		if !ok {
			ip.panicf(target.Line, "index must be a scalar number")
		}
		i := int(n)
		if i < 0 || i >= t.Shape[k] {
			ip.panicf(target.Line, "index %d out of range for axis %d of length %d", i, k, t.Shape[k])
		}
		offset += i * stride
		stride *= t.Shape[k]
	}
	// A full index names one element; a partial one names a row, which would be
	// a bulk write this does not do.
	if len(idxExprs) != len(t.Shape) {
		ip.panicf(target.Line, "assigning to a row needs %d indices, got %d", len(t.Shape), len(idxExprs))
	}
	x, ok := rank0Number(v)
	if !ok {
		ip.panicf(target.Line, "can only assign a scalar to a tensor element")
	}
	t.Data[offset] = x
	return true
}

// indexChain flattens `m[i][j][k]` into its base expression and the indices in
// written order.
func indexChain(e ast.Expr) (ast.Expr, []ast.Expr) {
	var idxs []ast.Expr
	for {
		ix, ok := e.(*ast.Index)
		if !ok {
			return e, idxs
		}
		idxs = append([]ast.Expr{ix.Index}, idxs...)
		e = ix.Target
	}
}

func (ip *Interp) evalIndex(ex *ast.Index, env *value.Env) value.Value {
	// Both the target and the index are read here, so both force. The force is
	// after they are evaluated and before either is looked at, which is the shape
	// every escape point in this interpreter has: the tracer reaches the value,
	// something needs its data, and the trace is replayed to supply it.
	target := ip.evalExpr(ex.Target, env)
	idxVal := ip.forced(ip.evalExpr(ex.Index, env))
	// A dictionary is subscripted by its key, the read half of the store in
	// assignTo. A missing key is an error rather than a zero: dict_get returns an
	// Opt for the caller who wants to ask, and `d[k]` is the form that says the
	// key is expected to be there.
	if d, isDict := target.(*value.Dict); isDict {
		k, isStr := idxVal.(value.Str)
		if !isStr {
			ip.panicf(ex.Line, "a dictionary key must be a string")
		}
		got, found := d.Get(string(k))
		if !found {
			ip.panicf(ex.Line, "no key %q in the dictionary", string(k))
		}
		return got
	}
	n, ok := rank0Number(idxVal)
	if !ok {
		ip.panicf(ex.Line, "index must be a scalar number")
	}
	idx := int(n)

	switch t := target.(type) {
	case *tensor.Tensor:
		return ip.indexTensor(t, idx, ex.Line)
	case *value.List:
		if idx < 0 || idx >= len(t.Items) {
			ip.panicf(ex.Line, "list index %d out of range", idx)
		}
		return t.Items[idx]
	case value.Str:
		// A Str indexes to the byte at that offset, as a number: the systems
		// lexer walks bytes with `src[i]` and compares them to byte constants.
		if idx < 0 || idx >= len(t) {
			ip.panicf(ex.Line, "string index %d out of range", idx)
		}
		return tensor.Scalar(float64(t[idx]))
	}
	ip.panicf(ex.Line, "value is not indexable")
	return value.TheUnit
}

func (ip *Interp) evalSlice(ex *ast.Slice, env *value.Env) value.Value {
	target := ip.forced(ip.evalExpr(ex.Target, env))

	dim0 := -1
	switch t := target.(type) {
	case value.Num:
		ip.panicf(ex.Line, "cannot slice a scalar")
	case *tensor.Tensor:
		if len(t.Shape) == 0 {
			ip.panicf(ex.Line, "cannot slice a scalar")
		}
		dim0 = t.Shape[0]
	case *value.List:
		dim0 = len(t.Items)
	case value.Str:
		dim0 = len(t)
	default:
		ip.panicf(ex.Line, "value is not sliceable")
	}

	start := 0
	end := dim0
	if ex.Start != nil {
		start = ip.sliceBound(ex.Start, env, ex.Line)
	}
	if ex.End != nil {
		end = ip.sliceBound(ex.End, env, ex.Line)
	}

	switch t := target.(type) {
	case *tensor.Tensor:
		res, err := tensor.SliceAxis0(t, start, end)
		if err != nil {
			ip.panicf(ex.Line, "%s", err.Error())
		}
		return res
	case *value.List:
		if start < 0 {
			start += dim0
		}
		if end < 0 {
			end += dim0
		}
		if start < 0 || end > dim0 || start > end {
			ip.panicf(ex.Line, "slice [%d:%d] out of range for length %d", start, end, dim0)
		}
		items := make([]value.Value, end-start)
		copy(items, t.Items[start:end])
		return &value.List{Items: items}
	case value.Str:
		if start < 0 {
			start += dim0
		}
		if end < 0 {
			end += dim0
		}
		if start < 0 || end > dim0 || start > end {
			ip.panicf(ex.Line, "slice [%d:%d] out of range for length %d", start, end, dim0)
		}
		return value.Str(string(t)[start:end])
	}
	return value.TheUnit
}

func (ip *Interp) sliceBound(e ast.Expr, env *value.Env, line int) int {
	n, ok := rank0Number(ip.forced(ip.evalExpr(e, env)))
	if !ok {
		ip.panicf(line, "slice bounds must be scalar numbers")
	}
	return int(n)
}

func (ip *Interp) indexTensor(t *tensor.Tensor, idx, line int) *tensor.Tensor {
	res, err := tensor.IndexAxis0(t, idx)
	if err != nil {
		ip.panicf(line, "%s", err.Error())
	}
	return res
}

// paramTypes is the annotation on each parameter as a bare name, or "" for a
// shape annotation or none: what callClosure reads to convert an `I64`/`F64`
// argument at the call.
func paramTypes(params []ast.Param) []string {
	out := make([]string, len(params))
	found := false
	for i, p := range params {
		if p.TypeName != "" {
			out[i] = p.TypeName
			found = true
		} else if p.Unit != nil && len(p.Unit.Factors) == 1 && p.Unit.Factors[0].Exp == 1 {
			out[i] = p.Unit.Factors[0].Name
			found = true
		}
	}
	if !found {
		return nil
	}
	return out
}

func paramNames(params []ast.Param) []string {
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return names
}
