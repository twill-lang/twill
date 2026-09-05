// Package format pretty-prints an Aster AST back into canonical source. The
// output re-parses to an equivalent program and is idempotent.
package format

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/twill-lang/twill/internal/ast"
	"github.com/twill-lang/twill/internal/lexer"
	"github.com/twill-lang/twill/internal/parser"
)

// Source parses src and returns the canonically formatted text. It refuses
// (with an error) rather than drop any comment it can't place.
func Source(src string) (string, error) {
	prog, comments, err := parser.ParseWithComments(src)
	if err != nil {
		return "", err
	}
	out := format(prog, comments)
	// Safety: the formatted output must carry the same comments as the input.
	_, outComments, err := parser.ParseWithComments(out)
	if err != nil {
		return "", fmt.Errorf("internal: formatted output did not re-parse: %w", err)
	}
	if !sameComments(comments, outComments) {
		return "", fmt.Errorf("cannot format: a comment sits somewhere the formatter can't preserve (e.g. inside an inline block); left unchanged")
	}
	return out, nil
}

func format(prog *ast.Program, comments []lexer.Comment) string {
	p := &printer{trailing: map[int]string{}}
	for _, c := range comments {
		if c.Trailing {
			p.trailing[c.Line] = c.Text
		} else {
			p.own = append(p.own, c)
		}
	}
	sort.SliceStable(p.own, func(i, j int) bool { return p.own[i].Line < p.own[j].Line })
	// The mode declaration leads the file, set off from the body by a blank line
	// the way every systems-mode source in the tree writes it.
	if prog.Mode != "" {
		p.b.WriteString("mode " + prog.Mode + "\n")
		if len(prog.Body) > 0 {
			p.b.WriteString("\n")
		}
	}
	for _, s := range prog.Body {
		p.stmt(s, 0)
	}
	p.emitLeading(0, 1<<30) // flush trailing own-line comments at end of file
	out := p.b.String()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func sameComments(a, b []lexer.Comment) bool {
	if len(a) != len(b) {
		return false
	}
	texts := func(cs []lexer.Comment) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Text
		}
		sort.Strings(out)
		return out
	}
	ta, tb := texts(a), texts(b)
	for i := range ta {
		if ta[i] != tb[i] {
			return false
		}
	}
	return true
}

type printer struct {
	b        strings.Builder
	own      []lexer.Comment // own-line comments, sorted by line
	ownIdx   int
	trailing map[int]string // line -> trailing comment text
}

// emitLeading writes any pending own-line comments that come before beforeLine.
func (p *printer) emitLeading(indent, beforeLine int) {
	for p.ownIdx < len(p.own) && p.own[p.ownIdx].Line < beforeLine {
		p.line(indent, commentText(p.own[p.ownIdx].Text))
		p.ownIdx++
	}
}

func commentText(text string) string {
	if text == "" {
		return "#"
	}
	return "# " + text
}

func (p *printer) line(indent int, text string) {
	p.b.WriteString(strings.Repeat("  ", indent))
	p.b.WriteString(text)
	p.b.WriteByte('\n')
}

// lineC writes a statement line, appending its trailing comment if any.
func (p *printer) lineC(indent int, text string, srcLine int) {
	if t, ok := p.trailing[srcLine]; ok {
		text += "  " + commentText(t)
	}
	p.line(indent, text)
}

func (p *printer) stmt(s ast.Stmt, indent int) {
	p.emitLeading(indent, s.Pos())
	switch st := s.(type) {
	case *ast.Let:
		name := st.Name
		if st.Unit != nil {
			name += ": " + p.unitAnno(st.Unit)
		} else if st.TypeName != "" {
			name += ": " + st.TypeName
		}
		kw := "let "
		if st.Const {
			kw = "const "
		}
		p.lineC(indent, kw+name+" = "+p.expr(st.Value), st.Line)
	case *ast.LetTuple:
		p.lineC(indent, "let ("+strings.Join(st.Names, ", ")+") = "+p.expr(st.Value), st.Line)
	case *ast.Assign:
		p.lineC(indent, p.expr(st.Target)+" = "+p.expr(st.Value), st.Line)
	case *ast.FnDecl:
		p.fnDecl(st, indent)
	case *ast.While:
		p.blockStmt(indent, "while "+p.expr(st.Cond), st.Body, st.Line)
	case *ast.For:
		p.blockStmt(indent, "for "+st.Name+" in "+p.expr(st.Iter), st.Body, st.Line)
	case *ast.Return:
		if st.Value == nil {
			p.lineC(indent, "return", st.Line)
		} else {
			p.lineC(indent, "return "+p.expr(st.Value), st.Line)
		}
	case *ast.Import:
		if st.Alias != "" {
			p.lineC(indent, "import "+strconv.Quote(st.Path)+" as "+st.Alias, st.Line)
		} else {
			p.lineC(indent, "import "+strconv.Quote(st.Path), st.Line)
		}
	case *ast.UnitDecl:
		// A `unit` declaration had no case here at all, so the formatter printed
		// every other statement and dropped this one: `twill fmt --write` on a
		// file declaring USD deleted the declaration, and every annotation that
		// named it then failed to check. docs/needs.md NEEDS-77.
		p.lineC(indent, "unit "+st.Name, st.Line)
	case *ast.TypeDecl:
		fields := make([]string, len(st.Fields))
		for i, f := range st.Fields {
			fields[i] = f.Name + ": " + p.shape(f.Shape)
		}
		p.lineC(indent, "type "+st.Name+" = { "+strings.Join(fields, ", ")+" }", st.Line)
	case *ast.EnumDecl:
		cases := make([]string, len(st.Variants))
		for i, v := range st.Variants {
			cases[i] = v.Name
			if v.HasPayload {
				cases[i] += "(" + v.Payload + ")"
			}
		}
		p.lineC(indent, "enum "+st.Name+typeParams(st.TypeParams)+" { "+strings.Join(cases, ", ")+" }", st.Line)
	case *ast.StructDecl:
		fields := make([]string, len(st.Fields))
		for i, f := range st.Fields {
			fields[i] = f.Name + ": " + f.Type
		}
		p.lineC(indent, "struct "+st.Name+typeParams(st.TypeParams)+" { "+strings.Join(fields, ", ")+" }", st.Line)
	case *ast.Break:
		p.lineC(indent, "break", st.Line)
	case *ast.Continue:
		p.lineC(indent, "continue", st.Line)
	case *ast.ExprStmt:
		p.lineC(indent, p.expr(st.X), st.Line)
	case *ast.Block:
		for _, inner := range st.Body {
			p.stmt(inner, indent)
		}
	}
}

func (p *printer) fnDecl(fn *ast.FnDecl, indent int) {
	sig := "fn " + fn.Name + typeParams(fn.TypeParams) + "(" + p.params(fn.Params) + ")" + p.retPart(fn.Ret, fn.RetUnit, fn.RetType)
	if blk, ok := fn.Body.(*ast.Block); ok {
		p.blockStmt(indent, sig, blk, fn.Line)
		return
	}
	p.lineC(indent, sig+" = "+p.expr(fn.Body), fn.Line)
}

// blockStmt prints `<header> {` ... `}` with the body indented, emitting any
// comments that fall inside the block.
func (p *printer) blockStmt(indent int, header string, blk *ast.Block, headerLine int) {
	p.lineC(indent, header+" {", headerLine)
	for _, s := range blk.Body {
		p.stmt(s, indent+1)
	}
	p.emitLeading(indent+1, blk.EndLine) // comments before the closing brace
	p.line(indent, "}")
}

func (p *printer) params(params []ast.Param) string {
	parts := make([]string, len(params))
	for i, prm := range params {
		s := prm.Name
		switch {
		case prm.Shape != nil:
			s += ": " + p.shape(prm.Shape)
		case prm.TypeName != "":
			s += ": " + prm.TypeName
		case prm.Unit != nil:
			s += ": " + p.unitAnno(prm.Unit)
		}
		parts[i] = s
	}
	return strings.Join(parts, ", ")
}

// retPart prints the `-> ...` return annotation, which is either a shape or a
// unit (never both).
func (p *printer) retPart(r *ast.ShapeAnno, u *ast.UnitAnno, retType string) string {
	if r != nil {
		return " -> " + p.shape(r)
	}
	if u != nil {
		return " -> " + p.unitAnno(u)
	}
	if retType != "" {
		return " -> " + retType
	}
	return ""
}

// unitAnno renders a unit expression as `USD`, `USD/share`, `USD/year^2`, or
// `1/year`, grouping positive-exponent factors as the numerator and
// negative-exponent factors as the denominator.
func (p *printer) unitAnno(u *ast.UnitAnno) string {
	var num, den []string
	for _, f := range u.Factors {
		switch {
		case f.Exp == 0:
			continue
		case f.Exp == 1:
			num = append(num, f.Name)
		case f.Exp > 0:
			num = append(num, f.Name+"^"+strconv.Itoa(f.Exp))
		case f.Exp == -1:
			den = append(den, f.Name)
		default:
			den = append(den, f.Name+"^"+strconv.Itoa(-f.Exp))
		}
	}
	numS := "1"
	if len(num) > 0 {
		numS = strings.Join(num, "*")
	}
	if len(den) == 0 {
		return numS
	}
	return numS + "/" + strings.Join(den, "*")
}

func (p *printer) shape(s *ast.ShapeAnno) string {
	dims := make([]string, len(s.Dims))
	for i, d := range s.Dims {
		switch {
		case d.IsConcrete():
			dims[i] = strconv.Itoa(d.Size)
		case d.Var != "":
			dims[i] = d.Var
		default:
			dims[i] = "_"
		}
	}
	return "[" + strings.Join(dims, ", ") + "]"
}

// --- expressions -----------------------------------------------------------

func (p *printer) expr(e ast.Expr) string {
	switch ex := e.(type) {
	case *ast.NumberLit:
		return formatNumberLit(ex)
	case *ast.StringLit:
		return strconv.Quote(ex.Value)
	case *ast.BoolLit:
		if ex.Value {
			return "true"
		}
		return "false"
	case *ast.Ident:
		return ex.Name
	case *ast.TensorLit:
		return "[" + p.exprList(ex.Elements) + "]"
	case *ast.ListLit:
		return "[" + p.exprList(ex.Elements) + "]"
	case *ast.TupleLit:
		return "(" + p.exprList(ex.Elements) + ")"
	case *ast.RecordLit:
		parts := make([]string, len(ex.Fields))
		for i, f := range ex.Fields {
			parts[i] = f.Name + ": " + p.expr(f.Value)
		}
		prefix := ""
		if ex.TypeName != "" {
			prefix = ex.TypeName + " "
		}
		return prefix + "{ " + strings.Join(parts, ", ") + " }"
	case *ast.Lambda:
		sig := "fn(" + p.params(ex.Params) + ")" + p.retPart(ex.Ret, ex.RetUnit, ex.RetType)
		if blk, ok := ex.Body.(*ast.Block); ok {
			return sig + " " + p.inlineBlock(blk)
		}
		return sig + " = " + p.expr(ex.Body)
	case *ast.Unary:
		if ex.Op == "not" {
			return "not " + p.operand(ex.Operand)
		}
		// MIN_I64 is spelled as a minus over 9223372036854775808, a magnitude
		// no positive int64 holds, so the literal alone cannot be printed from
		// its digits and the sign has to be read with it.
		if ex.Op == "-" {
			if lit, ok := ex.Operand.(*ast.NumberLit); ok && lit.Text != "" && isDigits(lit.Text) {
				if n, err := strconv.ParseInt("-"+lit.Text, 10, 64); err == nil {
					return strconv.FormatInt(n, 10)
				}
			}
		}
		return ex.Op + p.operand(ex.Operand)
	case *ast.Binary:
		return p.parenChild(ex.Left, ex.Op, false) + " " + ex.Op + " " +
			p.parenChild(ex.Right, ex.Op, true)
	case *ast.Call:
		return p.postfixTarget(ex.Callee) + "(" + p.exprList(ex.Args) + ")"
	case *ast.Index:
		return p.postfixTarget(ex.Target) + "[" + p.expr(ex.Index) + "]"
	case *ast.Slice:
		lo, hi := "", ""
		if ex.Start != nil {
			lo = p.expr(ex.Start)
		}
		if ex.End != nil {
			hi = p.expr(ex.End)
		}
		return p.postfixTarget(ex.Target) + "[" + lo + ":" + hi + "]"
	case *ast.Field:
		return p.postfixTarget(ex.Target) + "." + ex.Name
	case *ast.IfExpr:
		return p.ifExpr(ex)
	case *ast.Match:
		return p.matchExpr(ex)
	case *ast.Try:
		return p.expr(ex.Expr) + "?"
	case *ast.Block:
		return p.inlineBlock(ex)
	}
	return "?"
}

// operand parenthesizes a binary expression under a unary operator so
// `-(a + b)` / `not (a and b)` keep their meaning.
// postfixTarget formats the thing a postfix operator applies to: the target of
// an index, a slice or a field access, and a call's callee. Anything that binds
// looser than a postfix keeps the parentheses it was written with.
//
// Without this the parentheses were dropped and the program changed meaning
// silently: `(x + y).to(i8)` became `x + y.to(i8)`, which casts y alone;
// `(p + q).field` read q's field; `(m + n)[0]` indexed n. A formatter rewriting
// what a program computes is the worst thing a formatter can do, and
// shuttle's src/quant.tw is a real file it happened to.
func (p *printer) postfixTarget(e ast.Expr) string {
	switch e.(type) {
	case *ast.Binary, *ast.Unary, *ast.IfExpr, *ast.Match, *ast.Lambda:
		return "(" + p.expr(e) + ")"
	}
	return p.expr(e)
}

func (p *printer) operand(e ast.Expr) string {
	if _, ok := e.(*ast.Binary); ok {
		return "(" + p.expr(e) + ")"
	}
	return p.expr(e)
}

var precedence = map[string]int{
	"or": 1, "||": 1, "and": 2, "&&": 2,
	"==": 3, "!=": 3, "<": 4, "<=": 4, ">": 4, ">=": 4,
	"+": 5, "-": 5, "xor": 5, "bor": 5, "*": 6, "/": 6, "//": 6, "%": 6, "@": 6, "shl": 6, "shr": 6, "band": 6, "^": 7,
}

// parenChild formats a binary operand, adding parentheses only when needed to
// preserve the operator tree's precedence and associativity.
func (p *printer) parenChild(child ast.Expr, parentOp string, isRight bool) string {
	cb, ok := child.(*ast.Binary)
	if !ok {
		return p.expr(child)
	}
	childPrec, parentPrec := precedence[cb.Op], precedence[parentOp]
	need := childPrec < parentPrec
	if childPrec == parentPrec {
		rightAssoc := parentOp == "^"
		if isRight != rightAssoc {
			need = true
		}
	}
	s := p.expr(child)
	if need {
		return "(" + s + ")"
	}
	return s
}

func (p *printer) exprList(es []ast.Expr) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = p.expr(e)
	}
	return strings.Join(parts, ", ")
}

// inlineBlock prints a block on one logical line for expression contexts, using
// a nested printer for the body.
func (p *printer) ifExpr(ex *ast.IfExpr) string {
	s := "if " + p.expr(ex.Cond) + " " + p.inlineBlock(ex.Then)
	switch alt := ex.Else.(type) {
	case *ast.Block:
		s += " else " + p.inlineBlock(alt)
	case *ast.IfExpr:
		s += " else " + p.ifExpr(alt)
	}
	return s
}

// typeParams renders the `[T, U]` a declaration may carry. It is empty for a
// declaration without them, so nothing changes for the code written before
// they existed -- and it is not optional: a printer with no case for them
// deletes them, which turns every generic program into one that means
// something else.
func typeParams(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// matchExpr renders a match inline, `match subject { pattern => body, ... }`, the
// same one-line canonical form the printer gives every block-structured
// expression. A block-valued arm is inlined with `{ ...; ... }`.
func (p *printer) matchExpr(ex *ast.Match) string {
	arms := make([]string, len(ex.Arms))
	for i, arm := range ex.Arms {
		pat := p.pattern(arm.Pattern)
		if arm.Guard != nil {
			pat += " if " + p.expr(arm.Guard)
		}
		arms[i] = pat + " => " + p.inlineStmt(arm.Body)
	}
	return "match " + p.expr(ex.Subject) + " { " + strings.Join(arms, ", ") + " }"
}

// pattern renders a match pattern, nesting the way it was parsed.
func (p *printer) pattern(pat ast.MatchPattern) string {
	switch pat.Kind {
	case ast.PatBinding:
		if pat.Binding == "" {
			return "_"
		}
		return pat.Binding
	case ast.PatLiteral:
		return p.expr(pat.Lit)
	}
	if pat.Sub == nil {
		return pat.Variant
	}
	return pat.Variant + "(" + p.pattern(*pat.Sub) + ")"
}

// inlineStmt renders a statement on one line, for a match arm body. The common
// arm shapes (an expression, a return, an assignment, a block) have direct
// single-line forms; anything else falls back to buffer output joined with `;`.
func (p *printer) inlineStmt(s ast.Stmt) string {
	switch st := s.(type) {
	case *ast.ExprStmt:
		return p.expr(st.X)
	case *ast.Return:
		if st.Value == nil {
			return "return"
		}
		return "return " + p.expr(st.Value)
	case *ast.Assign:
		return p.expr(st.Target) + " = " + p.expr(st.Value)
	case *ast.Block:
		return p.inlineBlock(st)
	}
	sub := &printer{}
	sub.stmt(s, 0)
	lines := strings.Split(strings.TrimRight(sub.b.String(), "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "; ")
}

// inlineBlock renders a block as `{ ... }`. Statements are separated by `;` so
// the result stays a single formatted line inside an expression.
func (p *printer) inlineBlock(blk *ast.Block) string {
	if len(blk.Body) == 0 {
		return "{}"
	}
	sub := &printer{}
	for _, s := range blk.Body {
		sub.stmt(s, 0)
	}
	lines := strings.Split(strings.TrimRight(sub.b.String(), "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return "{ " + strings.Join(lines, "; ") + " }"
}

// formatNumberLit prints a numeric literal, from its digits when it has them.
//
// An integer literal is not printed through the f64 the parser produced,
// because an f64 holds integers exactly only to 2^53 and the formatter must not
// change the program. It was doing exactly that: 9007199254740993 came back as
// ...992, 1234567890123456789 as ...768, and 9223372036854775807 -- MAX_I64 --
// as the float 9.223372036854776e+18. Before 1.6 those values were f64 anyway
// and the damage was already done at parse time; now an I64 literal is an exact
// value, so this is a semantic change written to disk by `twill fmt --write`.
//
// Only a literal whose digits fit an int64 takes this path. Anything else --
// a fraction, an exponent form, a magnitude past int64 -- is a float and prints
// as one, so `3.0` still normalises to `3` the way every golden expects.
func formatNumberLit(lit *ast.NumberLit) string {
	if lit.Text != "" && isDigits(lit.Text) {
		if n, err := strconv.ParseInt(lit.Text, 10, 64); err == nil {
			return strconv.FormatInt(n, 10)
		}
	}
	return formatNumber(lit.Value)
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func formatNumber(n float64) string {
	if n == math.Trunc(n) && n >= -9223372036854775808.0 && n < 9223372036854775808.0 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'g', -1, 64)
}
