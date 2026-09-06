package ir

import (
	"fmt"
	"sort"

	"github.com/twill-lang/twill/internal/ast"
)

// Coverage answers one question, and it is the question a compiler that only
// handles part of a language has to keep answering out loud: how much of a
// program is inside the subset the IR can represent?
//
// It is a static classification of an AST, not a compilation. There is no
// AST-to-IR front end yet: graphs are built through the Builder, and the tracer
// docs/CODEGEN.md section 2 describes -- the one that would sit inside the
// interpreter and record operations as they run -- is not written. So Coverage
// measures the ceiling rather than the achievement: a form it accepts is one
// the IR has an opcode for, and a form it rejects is one the compiler could not
// handle even with a perfect front end.
//
// Reading a Coverage number therefore needs the qualification that comes with
// it: In counts expression nodes whose form is representable, Out counts nodes
// whose form is not, and Reasons says why for each kind of rejection. Nothing
// here claims a program ran through the backend.
type Coverage struct {
	In      int
	Out     int
	Reasons map[string]int
	// Clean reports whether every node in the program is inside the subset,
	// which is the stronger and more useful per-file property.
	Clean bool
}

// Fraction is the share of classified nodes inside the subset.
func (c Coverage) Fraction() float64 {
	if c.In+c.Out == 0 {
		return 0
	}
	return float64(c.In) / float64(c.In+c.Out)
}

// TopReasons returns the rejection reasons, most frequent first.
func (c Coverage) TopReasons() []string {
	keys := make([]string, 0, len(c.Reasons))
	for k := range c.Reasons {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if c.Reasons[keys[i]] != c.Reasons[keys[j]] {
			return c.Reasons[keys[i]] > c.Reasons[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf("%s x%d", k, c.Reasons[k])
	}
	return out
}

// Add merges another coverage result.
func (c *Coverage) Add(o Coverage) {
	c.In += o.In
	c.Out += o.Out
	if c.Reasons == nil {
		c.Reasons = map[string]int{}
	}
	for k, v := range o.Reasons {
		c.Reasons[k] += v
	}
}

// compilableBuiltins are the builtin calls with an opcode behind them. The list
// is the operator set in ir.go and nothing else; anything absent forces to the
// interpreter, which is the behaviour docs/CODEGEN.md section 6 specifies.
var compilableBuiltins = map[string]bool{
	"exp": true, "log": true, "sqrt": true, "sin": true, "cos": true,
	"tanh": true, "sigmoid": true, "relu": true, "square": true, "abs": true,
	"neg": true, "pow": true, "clip": true, "where": true,
	"maximum": true, "minimum": true,
	"sum": true, "mean": true, "sum_axis": true, "mean_axis": true,
	"reshape": true, "transpose": true, "broadcast_to": true, "matmul": true,
	"zeros": true, "ones": true, "full": true,
}

var compilableBinary = map[string]bool{
	"+": true, "-": true, "*": true, "/": true, "%": true, "^": true, "@": true,
}

// CoverProgram classifies a parsed program.
func CoverProgram(p *ast.Program) Coverage {
	c := &Coverage{Reasons: map[string]int{}, Clean: true}
	if p.Mode == "systems" {
		// The systems dialect has no tensors at all: a scalar there is a machine
		// word, by design (docs/design.md). There is nothing to fuse and no
		// partial credit to give.
		c.reject("mode systems")
		return *c
	}
	for _, s := range p.Body {
		c.stmt(s)
	}
	return *c
}

func (c *Coverage) accept()              { c.In++ }
func (c *Coverage) reject(reason string) { c.Out++; c.Reasons[reason]++; c.Clean = false }

func (c *Coverage) stmt(s ast.Stmt) {
	switch st := s.(type) {
	case *ast.Let:
		c.accept()
		c.expr(st.Value)
	case *ast.ExprStmt:
		c.accept()
		c.expr(st.X)
	case *ast.Return:
		c.accept()
		if st.Value != nil {
			c.expr(st.Value)
		}
	case *ast.FnDecl:
		c.accept()
		c.expr(st.Body)
	case *ast.Block:
		c.accept()
		for _, x := range st.Body {
			c.stmt(x)
		}
	case *ast.Assign:
		// Rebinding is not itself uncompilable, but nothing in this stage tracks
		// mutable state through a trace, so it is counted out.
		c.reject("assignment")
		c.expr(st.Value)
	case *ast.While:
		c.reject("while")
	case *ast.For:
		c.reject("for")
	case *ast.Import:
		c.reject("import")
	case *ast.TypeDecl, *ast.UnitDecl, *ast.EnumDecl, *ast.StructDecl:
		c.reject("declaration")
	case *ast.Break, *ast.Continue:
		c.reject("loop control")
	case *ast.LetTuple:
		// A destructuring binding is not compilable here for the same reason a
		// tuple literal is not: this stage has one value per node.
		c.reject("destructuring let")
		c.expr(st.Value)
	default:
		c.reject(fmt.Sprintf("%T", s))
	}
}

func (c *Coverage) expr(e ast.Expr) {
	switch ex := e.(type) {
	case nil:
		return
	case *ast.NumberLit:
		c.accept()
	case *ast.Ident:
		c.accept()
	case *ast.TensorLit:
		c.accept()
		for _, x := range ex.Elements {
			c.expr(x)
		}
	case *ast.Unary:
		if ex.Op == "-" {
			c.accept()
		} else {
			c.reject("unary " + ex.Op)
		}
		c.expr(ex.Operand)
	case *ast.Binary:
		if compilableBinary[ex.Op] {
			c.accept()
		} else {
			c.reject("operator " + ex.Op)
		}
		c.expr(ex.Left)
		c.expr(ex.Right)
	case *ast.Call:
		if id, ok := ex.Callee.(*ast.Ident); ok {
			switch {
			case compilableBuiltins[id.Name]:
				c.accept()
			case id.Name == "grad" || id.Name == "grads" || id.Name == "value_and_grad":
				// The gradient transform in grad.go is exactly this, so the call
				// itself is in the subset; whether its body is depends on the body.
				c.accept()
			case id.Name == "hessian":
				// Second order runs forward-mode jets through a separate path with
				// its own per-node closures. docs/CODEGEN.md section 5 puts it out
				// of scope and it stays out.
				c.reject("hessian")
			default:
				// A call to a user function is in the subset only if its body is,
				// and the body is classified where it is declared. A call to an
				// unlisted builtin is not.
				c.reject("call " + id.Name)
			}
		} else {
			c.reject("computed callee")
		}
		for _, a := range ex.Args {
			c.expr(a)
		}
	case *ast.Block:
		c.accept()
		for _, s := range ex.Body {
			c.stmt(s)
		}
	case *ast.Lambda:
		c.accept()
		c.expr(ex.Body)
	case *ast.IfExpr:
		c.reject("if")
	case *ast.StringLit:
		c.reject("string")
	case *ast.ListLit:
		c.reject("list")
	case *ast.TupleLit:
		c.reject("tuple")
	case *ast.RecordLit:
		c.reject("record")
	case *ast.Field:
		c.reject("field access")
	case *ast.Index:
		c.reject("index")
	case *ast.Slice:
		c.reject("slice")
	case *ast.Match:
		c.reject("match")
	case *ast.Try:
		c.reject("try")
	case *ast.BoolLit:
		c.reject("bool")
	default:
		c.reject(fmt.Sprintf("%T", e))
	}
}
