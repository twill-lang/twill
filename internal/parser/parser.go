// Package parser builds an AST from Twill source using recursive descent
// with a Pratt loop for binary operators.
package parser

import (
	"fmt"
	"strconv"

	"github.com/twill-lang/twill/internal/ast"
	"github.com/twill-lang/twill/internal/lexer"
)

// The bitwise operators are spelled as words, and sit where their symbolic
// equivalents sit in C and Go: the shifts bind like `*`, and `xor` like `+`.
// `and` and `or` keep the low precedence they have as the boolean operators,
// which is where every line of twill already written expects them, so a bitwise
// `x and 255` still needs its parentheses next to arithmetic.
var precedence = map[string]int{
	"or": 1, "||": 1,
	"and": 2, "&&": 2,
	"==": 3, "!=": 3,
	"<": 4, "<=": 4, ">": 4, ">=": 4,
	"+": 5, "-": 5, "xor": 5, "bor": 5,
	"*": 6, "/": 6, "//": 6, "%": 6, "@": 6, "shl": 6, "shr": 6, "band": 6,
	"^": 7,
}

var rightAssoc = map[string]bool{"^": true}

// bitwiseWord is the set of operators spelled as a word rather than a symbol.
// Each is a keyword, so each is also callable, `xor(a, b)`, and parsePrimary
// turns a leading one back into an identifier for that form.
var bitwiseWord = map[string]bool{
	"and": true, "or": true,
	"band": true, "bor": true, "xor": true, "shl": true, "shr": true,
}

// Parse tokenizes and parses src into a Program.
func Parse(src string) (*ast.Program, error) {
	prog, _, err := ParseWithComments(src)
	return prog, err
}

// ParseWithComments parses src and also returns the source comments (for tools
// like the formatter).
func ParseWithComments(src string) (*ast.Program, []lexer.Comment, error) {
	toks, comments, err := lexer.TokenizeWithComments(src)
	if err != nil {
		return nil, nil, err
	}
	p := &parser{toks: toks}
	prog, perr := p.parseProgram()
	if perr != nil {
		return nil, nil, perr
	}
	return prog, comments, nil
}

type parser struct {
	toks []lexer.Token
	pos  int
	// groupDepth is how many enclosing `(...)` or `[...]` the cursor is inside.
	// The newline rules that end a statement (a line starting with `+`/`-`, or
	// with `and(`/`or(`) apply only at statement level, groupDepth 0: inside a
	// grouping there is no statement to end, so `f(a\n  + b)` continues the
	// expression rather than breaking mid-argument.
	groupDepth int
	// stmtCol is the column of the first token of the statement being parsed.
	// A line that opens with `+`/`-` is a continuation of that statement when it
	// is indented past this column, and a new statement when it lines up with it
	// or sits to its left. Indentation is what a reader already uses to tell the
	// two apart, so the parser reads it the same way.
	stmtCol int
}

func (p *parser) peek(o int) lexer.Token {
	idx := p.pos + o
	if idx >= len(p.toks) {
		idx = len(p.toks) - 1
	}
	return p.toks[idx]
}

func (p *parser) next() lexer.Token {
	t := p.toks[p.pos]
	p.pos++
	return t
}

func (p *parser) atEnd() bool { return p.peek(0).Kind == lexer.EOF }

func (p *parser) check(value string) bool {
	t := p.peek(0)
	return (t.Kind == lexer.OP || t.Kind == lexer.PUNCT || t.Kind == lexer.KEYWORD) && t.Value == value
}

func (p *parser) match(value string) bool {
	if p.check(value) {
		p.next()
		return true
	}
	return false
}

func (p *parser) expect(value string) (lexer.Token, error) {
	if p.check(value) {
		return p.next(), nil
	}
	t := p.peek(0)
	return t, p.errf(t, "expected %q but found %q", value, tokenText(t))
}

func (p *parser) errf(t lexer.Token, format string, args ...any) error {
	return &lexer.SyntaxError{Msg: fmt.Sprintf(format, args...), Line: t.Line, Col: t.Col}
}

func (p *parser) skipSeparators() {
	for p.match(";") {
	}
}

func (p *parser) parseProgram() (*ast.Program, error) {
	prog := &ast.Program{}
	p.skipSeparators()
	// A leading `mode <name>` is a file-level declaration, not a statement: it
	// names the dialect the rest of the file is written in. `mode` is not a
	// keyword (so it stays usable as an ordinary name elsewhere), so it is
	// recognised only here, only first, and only when an identifier follows it.
	if t := p.peek(0); t.Kind == lexer.IDENT && t.Value == "mode" && p.peek(1).Kind == lexer.IDENT {
		p.next()                   // 'mode'
		prog.Mode = p.next().Value // the dialect name, e.g. 'systems'
		p.skipSeparators()
	}
	for !p.atEnd() {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		prog.Body = append(prog.Body, s)
		p.skipSeparators()
	}
	return prog, nil
}

// --- statements ------------------------------------------------------------

// isAssignable reports whether an expression may sit on the left of `=`: a bare
// name, a field access, or an index, the forms that name a storage location.
func isAssignable(x ast.Expr) bool {
	switch x.(type) {
	case *ast.Ident, *ast.Field, *ast.Index:
		return true
	}
	return false
}

func (p *parser) parseStmt() (ast.Stmt, error) {
	t := p.peek(0)
	// Record where this statement begins so the continuation rule in parseBinary
	// can compare a line-leading operator against it. Statements nest (a block
	// body inside an `if` inside a statement), so the outer column is restored on
	// the way out.
	outerCol := p.stmtCol
	p.stmtCol = t.Col
	defer func() { p.stmtCol = outerCol }()
	if t.Kind == lexer.KEYWORD {
		switch t.Value {
		case "let", "const":
			return p.parseLet()
		case "fn":
			if p.peek(1).Kind == lexer.IDENT {
				return p.parseFnDecl()
			}
		case "while":
			return p.parseWhile()
		case "for":
			return p.parseFor()
		case "return":
			return p.parseReturn()
		case "break":
			return &ast.Break{Line: p.next().Line}, nil
		case "continue":
			return &ast.Continue{Line: p.next().Line}, nil
		case "import":
			return p.parseImport()
		case "enum":
			return p.parseEnumDecl()
		case "struct":
			return p.parseStructDecl()
		}
	}

	// `unit <name>` declares a unit. `unit` is not a keyword, so it stays an
	// ordinary identifier everywhere else, most importantly as a field name
	// (`unit: Opt[UnitAnno]`); only a leading `unit` with an identifier after it
	// is the declaration.
	if t.Kind == lexer.IDENT && t.Value == "unit" && p.peek(1).Kind == lexer.IDENT {
		return p.parseUnitDecl()
	}
	// `type <name> = ...` declares a record type. Like `unit`, `type` is not a
	// keyword, so it stays usable as a field name (`res.type`, `{ type: x }`);
	// only a leading `type` with an identifier after it is the declaration.
	if t.Kind == lexer.IDENT && t.Value == "type" && p.peek(1).Kind == lexer.IDENT {
		return p.parseTypeDecl()
	}

	x, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	// Assignment: `<lvalue> = expr`. `=` is not a binary operator, so parseExpr
	// stopped in front of it (and `==` was consumed as a comparison, so it never
	// reaches here). The target must be an lvalue: a name, a field, or an index.
	if p.peek(0).Kind == lexer.OP && p.peek(0).Value == "=" {
		p.next() // '='
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if !isAssignable(x) {
			return nil, p.errf(t, "cannot assign to this expression; the left of `=` must be a name, a field, or an index")
		}
		return &ast.Assign{Target: x, Value: v, Line: t.Line}, nil
	}
	return &ast.ExprStmt{X: x, Line: t.Line}, nil
}

// parseLet reads `let` and `const`, which differ only in whether the binding
// may be assigned to afterwards. The two spell the same statement, so they
// share a node and a parser and part company in the checker.
func (p *parser) parseLet() (ast.Stmt, error) {
	kw := p.next() // 'let' or 'const'
	line := kw.Line
	isConst := kw.Value == "const"
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	// Optional annotation. A unit introduces a quantity (`let px: USD/share`); a
	// name followed by `.` or `[` is a type (`let d: Arr[I64]`), advisory and
	// kept as text, the same disambiguation a parameter makes.
	var unit *ast.UnitAnno
	var typeName string
	if p.match(":") {
		if p.atFnType() {
			typeName, err = p.parseFnType()
			if err != nil {
				return nil, err
			}
		} else {
			unit, err = p.parseUnitExpr()
			if err != nil {
				return nil, err
			}
			if (p.check(".") || p.check("[")) && len(unit.Factors) == 1 && unit.Factors[0].Exp == 1 {
				typeName, err = p.typeSuffix(unit.Factors[0].Name)
				if err != nil {
					return nil, err
				}
				unit = nil
			}
		}
	}
	if _, err := p.expect("="); err != nil {
		return nil, err
	}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.Let{Name: name, Unit: unit, TypeName: typeName, Value: v, Const: isConst, Line: line}, nil
}

func (p *parser) parseFnDecl() (ast.Stmt, error) {
	line := p.next().Line // 'fn'
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	typeParams, err := p.parseTypeParams()
	if err != nil {
		return nil, err
	}
	params, ret, retUnit, retType, err := p.parseSignature()
	if err != nil {
		return nil, err
	}
	body, err := p.parseFnBody()
	if err != nil {
		return nil, err
	}
	return &ast.FnDecl{Name: name, TypeParams: typeParams, Params: params, Ret: ret, RetUnit: retUnit, RetType: retType, Body: body, Line: line}, nil
}

func (p *parser) parseWhile() (ast.Stmt, error) {
	line := p.next().Line
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.While{Cond: cond, Body: body, Line: line}, nil
}

func (p *parser) parseFor() (ast.Stmt, error) {
	line := p.next().Line
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect("in"); err != nil {
		return nil, err
	}
	iter, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.For{Name: name, Iter: iter, Body: body, Line: line}, nil
}

func (p *parser) parseReturn() (ast.Stmt, error) {
	line := p.next().Line
	// A value-less `return` is followed by the end of its block or statement, or,
	// when it is a match arm body, by the arm-separating `,` (`_ => return,`).
	if p.check("}") || p.check(";") || p.check(",") || p.atEnd() {
		return &ast.Return{Value: nil, Line: line}, nil
	}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.Return{Value: v, Line: line}, nil
}

func (p *parser) parseImport() (ast.Stmt, error) {
	line := p.next().Line
	t := p.peek(0)
	if t.Kind != lexer.STRING {
		return nil, p.errf(t, "import expects a string path")
	}
	p.next()
	imp := &ast.Import{Path: t.Value, Line: line}
	// Optional `as name` binds the module's definitions to a namespace record.
	if p.peek(0).Kind == lexer.IDENT && p.peek(0).Value == "as" {
		p.next()
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		imp.Alias = name
	}
	return imp, nil
}

// --- expressions -----------------------------------------------------------

func (p *parser) parseExpr() (ast.Expr, error) { return p.parseBinary(0) }

func (p *parser) parseBinary(minPrec int) (ast.Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek(0)
		op := t.Value
		isOp := t.Kind == lexer.OP || (t.Kind == lexer.KEYWORD && bitwiseWord[op])
		if !isOp {
			break
		}
		// A '+' or '-' that opens a new line is ambiguous: it either continues
		// this expression or starts a new statement whose first token happens to
		// be a unary minus (`-mean(x)`). Indentation decides, the same way a
		// reader decides. Indented past the column the statement began at, the
		// operator continues the expression:
		//
		//	let a = first_term
		//	  + second_term        // continues: indented past `let`
		//
		// Lined up with it (or further left), it begins a new statement:
		//
		//	let a = first_term
		//	-mean(x)               // a new statement
		//
		// Ending the previous line with the operator continues an expression too,
		// and always did; this rule adds the leading-operator form beside it.
		if p.groupDepth == 0 && (op == "+" || op == "-") && p.pos > 0 &&
			t.Line > p.toks[p.pos-1].Line && t.Col <= p.stmtCol {
			break
		}
		// A line that begins `and(`, `xor(`, `shr(` and so on is a call starting a
		// new statement, not this expression continued by that operator. Every
		// bitwise word is both an infix operator and a callable builtin, so the
		// following `(` is what distinguishes the call from a genuine
		// continuation, and indentation separates them for the same reason it
		// does above. Like the `+`/`-` rule, only at statement level: inside a
		// grouping it continues.
		if p.groupDepth == 0 && bitwiseWord[op] && p.pos > 0 &&
			t.Line > p.toks[p.pos-1].Line && p.peek(1).Value == "(" && t.Col <= p.stmtCol {
			break
		}
		prec, ok := precedence[op]
		if !ok || prec < minPrec {
			break
		}
		p.next()
		nextMin := prec + 1
		if rightAssoc[op] {
			nextMin = prec
		}
		right, err := p.parseBinary(nextMin)
		if err != nil {
			return nil, err
		}
		left = &ast.Binary{Op: op, Left: left, Right: right, Line: t.Line}
	}
	return left, nil
}

func (p *parser) parseUnary() (ast.Expr, error) {
	t := p.peek(0)
	if (t.Kind == lexer.OP && (t.Value == "-" || t.Value == "!")) ||
		(t.Kind == lexer.KEYWORD && t.Value == "not") {
		p.next()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.Unary{Op: t.Value, Operand: operand, Line: t.Line}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (ast.Expr, error) {
	x, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		// A '(' or '[' that starts a new line begins a new expression rather
		// than continuing this one as a call or index. This keeps a line like
		// `[a, b]` from being read as an index on the previous line. Keep the
		// call/index on the same line as its target to chain them.
		if (p.check("(") || p.check("[")) && p.pos > 0 && p.peek(0).Line > p.toks[p.pos-1].Line {
			break
		}
		if p.check(".") {
			line := p.next().Line // '.'
			name, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			x = &ast.Field{Target: x, Name: name, Line: line}
		} else if p.check("(") {
			line := p.peek(0).Line
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			x = &ast.Call{Callee: x, Args: args, Line: line}
		} else if p.check("[") {
			line := p.next().Line // '['
			node, err := p.parseIndexOrSlice(x, line)
			if err != nil {
				return nil, err
			}
			x = node
		} else if p.check("?") {
			// Postfix `?`: unwrap a Res/Opt success or return its failure.
			line := p.next().Line
			x = &ast.Try{Expr: x, Line: line}
		} else {
			break
		}
	}
	// A type name immediately followed by `{ field: ... }` is a typed record
	// literal, `Point { x: 1.0, y: 2.0 }`. Records are structural, so the literal
	// is the same value `{ ... }` builds; the name is carried only for printing.
	// looksLikeRecord requires `{ ident :`, which a block never begins with, so
	// this does not swallow the block of an `if`/`while` whose condition is a name.
	if name, ok := exprToName(x); ok && p.looksLikeRecord() {
		rec, err := p.parseRecordLit()
		if err != nil {
			return nil, err
		}
		rec.(*ast.RecordLit).TypeName = name
		x = rec
	}
	return x, nil
}

// exprToName renders a name or a dotted name (`Point`, `geom.Point`) as text, for
// the type in front of a typed record literal. It fails for anything else.
func exprToName(x ast.Expr) (string, bool) {
	switch e := x.(type) {
	case *ast.Ident:
		return e.Name, true
	case *ast.Field:
		base, ok := exprToName(e.Target)
		if !ok {
			return "", false
		}
		return base + "." + e.Name, true
	}
	return "", false
}

// parseIndexOrSlice parses the body of a `[...]` after the '[' is consumed. It
// produces an Index (`t[e]`) or a Slice (`t[a:b]`, with either side optional).
func (p *parser) parseIndexOrSlice(target ast.Expr, line int) (ast.Expr, error) {
	p.groupDepth++
	defer func() { p.groupDepth-- }()
	var start, end ast.Expr
	var err error
	if !p.check(":") {
		start, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	if p.match(":") {
		if !p.check("]") {
			end, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
		}
		if _, err := p.expect("]"); err != nil {
			return nil, err
		}
		return &ast.Slice{Target: target, Start: start, End: end, Line: line}, nil
	}
	if _, err := p.expect("]"); err != nil {
		return nil, err
	}
	return &ast.Index{Target: target, Index: start, Line: line}, nil
}

func (p *parser) parsePrimary() (ast.Expr, error) {
	t := p.peek(0)
	switch t.Kind {
	case lexer.NUMBER:
		p.next()
		v, err := strconv.ParseFloat(t.Value, 64)
		if err != nil {
			return nil, p.errf(t, "invalid number %q", t.Value)
		}
		return &ast.NumberLit{Value: v, Text: t.Value, Line: t.Line}, nil
	case lexer.STRING:
		p.next()
		return &ast.StringLit{Value: t.Value, Line: t.Line}, nil
	case lexer.KEYWORD:
		switch t.Value {
		case "true":
			p.next()
			return &ast.BoolLit{Value: true, Line: t.Line}, nil
		case "false":
			p.next()
			return &ast.BoolLit{Value: false, Line: t.Line}, nil
		case "if":
			return p.parseIf()
		case "match":
			return p.parseMatch()
		case "fn":
			return p.parseLambda()
		case "and", "or", "band", "bor", "xor", "shl", "shr":
			// `and` and `or` are the boolean infix operators, but spelled as a
			// call, `and(x, y)`, they are the bitwise builtins. Only a following
			// `(` selects the call; as infix they always have a left operand and
			// so never reach parsePrimary as a leading token. Emitting an Ident
			// lets parsePostfix turn it into the call.
			if p.peek(1).Value == "(" {
				p.next()
				return &ast.Ident{Name: t.Value, Line: t.Line}, nil
			}
		}
	case lexer.IDENT:
		p.next()
		return &ast.Ident{Name: t.Value, Line: t.Line}, nil
	}

	if p.check("(") {
		p.next()
		p.groupDepth++
		inner, err := p.parseExpr()
		p.groupDepth--
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(")"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	if p.check("[") {
		return p.parseTensorOrList()
	}
	if p.check("{") {
		if p.looksLikeRecord() {
			return p.parseRecordLit()
		}
		return p.parseBlock()
	}
	return nil, p.errf(t, "unexpected token %q", tokenText(t))
}

func (p *parser) parseIf() (*ast.IfExpr, error) {
	line := p.next().Line // 'if'
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	then, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	var elseBranch ast.Node
	if p.match("else") {
		if p.check("if") {
			elseBranch, err = p.parseIf()
		} else {
			elseBranch, err = p.parseBlock()
		}
		if err != nil {
			return nil, err
		}
	}
	return &ast.IfExpr{Cond: cond, Then: then, Else: elseBranch, Line: line}, nil
}

func (p *parser) parseLambda() (ast.Expr, error) {
	line := p.next().Line // 'fn'
	params, ret, retUnit, retType, err := p.parseSignature()
	if err != nil {
		return nil, err
	}
	body, err := p.parseFnBody()
	if err != nil {
		return nil, err
	}
	return &ast.Lambda{Params: params, Ret: ret, RetUnit: retUnit, RetType: retType, Body: body, Line: line}, nil
}

func (p *parser) parseTensorOrList() (ast.Expr, error) {
	line := p.next().Line // '['
	p.groupDepth++
	defer func() { p.groupDepth-- }()
	var elements []ast.Expr
	if !p.check("]") {
		first, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elements = append(elements, first)
		for p.match(",") {
			if p.check("]") {
				break
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			elements = append(elements, e)
		}
	}
	if _, err := p.expect("]"); err != nil {
		return nil, err
	}
	isTensor := len(elements) > 0
	for _, e := range elements {
		if !isNumericElem(e) {
			isTensor = false
			break
		}
	}
	if isTensor {
		return &ast.TensorLit{Elements: elements, Line: line}, nil
	}
	return &ast.ListLit{Elements: elements, Line: line}, nil
}

func (p *parser) parseBlock() (*ast.Block, error) {
	open, err := p.expect("{")
	if err != nil {
		return nil, err
	}
	blk := &ast.Block{Line: open.Line}
	p.skipSeparators()
	for !p.check("}") && !p.atEnd() {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		blk.Body = append(blk.Body, s)
		p.skipSeparators()
	}
	closeTok, err := p.expect("}")
	if err != nil {
		return nil, err
	}
	blk.EndLine = closeTok.Line
	return blk, nil
}

func (p *parser) parseFnBody() (ast.Expr, error) {
	if p.match("=") {
		return p.parseExpr()
	}
	return p.parseBlock()
}

// looksLikeRecord decides whether a `{` starts a record literal (`{ name: ...`)
// rather than a block. A block never begins with `ident :`.
func (p *parser) looksLikeRecord() bool {
	if p.peek(0).Value != "{" {
		return false
	}
	// `{}` in expression position is the empty record. Read as a block it would
	// be a block with no statements, whose value is unit -- nothing a program can
	// use -- so there is no second meaning being taken away. It matters because
	// `let seen: Dict[Str, I64] = {}` is how the empty dictionary is written, and
	// binding unit there left every later dict_set on it failing.
	if p.peek(1).Value == "}" {
		return true
	}
	// `{ ..base }` is a record update, whose base is written before any field, so
	// two dots settle it on their own. No statement begins with `.`, so a block is
	// not what is being taken away here either.
	if isDot(p.peek(1)) && isDot(p.peek(2)) {
		return true
	}
	return p.peek(1).Kind == lexer.IDENT &&
		p.peek(2).Kind == lexer.PUNCT && p.peek(2).Value == ":"
}

// isDot reports whether a token is the field-access `.`. The lexer has no `..`
// token: `.` is punctuation and a number literal only starts with one when a
// digit follows, so `..base` arrives as two of these and an identifier. Reading
// the pair here rather than in the lexer is what keeps `..` out of every other
// position in the grammar.
func isDot(t lexer.Token) bool {
	return t.Kind == lexer.PUNCT && t.Value == "."
}

// atDotDot reports whether the parser is looking at the `..` that opens a record
// update.
func (p *parser) atDotDot() bool {
	return isDot(p.peek(0)) && isDot(p.peek(1))
}

func (p *parser) parseRecordLit() (ast.Expr, error) {
	line := p.expectPunct("{").Line
	rec := &ast.RecordLit{Line: line}
	for !p.check("}") && !p.atEnd() {
		if p.atDotDot() {
			t := p.peek(0)
			// The base is the whole record being copied, so it has to be read
			// before the fields that replace parts of it. Allowing it last would
			// mean either a second rule about which wins or a literal whose
			// meaning depends on where the reader's eye lands.
			if rec.Base != nil || len(rec.Fields) > 0 {
				return nil, p.errf(t, "the base of a record update must come first, as `{ ..base, field: value }`")
			}
			p.next()
			p.next()
			base, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			rec.Base = base
			if !p.match(",") {
				break
			}
			continue
		}
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(":"); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		rec.Fields = append(rec.Fields, ast.RecordField{Name: name, Value: val})
		if !p.match(",") {
			break
		}
	}
	if _, err := p.expect("}"); err != nil {
		return nil, err
	}
	return rec, nil
}

func (p *parser) expectPunct(value string) lexer.Token {
	t, _ := p.expect(value)
	return t
}

// parseSignature parses the parameter list and an optional "-> shape" return.
func (p *parser) parseSignature() ([]ast.Param, *ast.ShapeAnno, *ast.UnitAnno, string, error) {
	params, err := p.parseParams()
	if err != nil {
		return nil, nil, nil, "", err
	}
	var ret *ast.ShapeAnno
	var retUnit *ast.UnitAnno
	var retType string
	if p.match("->") {
		if p.check("[") {
			ret, err = p.parseShapeAnno()
			if err != nil {
				return nil, nil, nil, "", err
			}
		} else if p.atFnType() {
			retType, err = p.parseFnType()
			if err != nil {
				return nil, nil, nil, "", err
			}
		} else {
			retUnit, err = p.parseUnitExpr()
			if err != nil {
				return nil, nil, nil, "", err
			}
			// A `.` or `[` after a single bare name makes it a qualified or
			// generic type, not a unit: units are never qualified or bracketed.
			// Lift it out of the unit slot into an advisory type name.
			if (p.check(".") || p.check("[")) && len(retUnit.Factors) == 1 && retUnit.Factors[0].Exp == 1 {
				retType, err = p.typeSuffix(retUnit.Factors[0].Name)
				if err != nil {
					return nil, nil, nil, "", err
				}
				retUnit = nil
			}
		}
	}
	return params, ret, retUnit, retType, nil
}

// qualify consumes `.Ident` segments after a base name and returns the dotted
// qualified name, e.g. base "cp" then ".Caps" gives "cp.Caps". It is how a
// module-qualified type name is read wherever a type annotation may appear.
func (p *parser) qualify(base string) (string, error) {
	name := base
	for p.check(".") {
		p.next() // '.'
		seg, err := p.expectIdent()
		if err != nil {
			return "", err
		}
		name += "." + seg
	}
	return name, nil
}

// typeSuffix continues a bare type name already read as `base`, consuming any
// `.name` qualification and `[...]` generic arguments, and returns the whole
// dotted/bracketed name, e.g. base "Arr" then "[I64]" gives "Arr[I64]". Type
// annotations are advisory, so the name is kept as text rather than a structure.
func (p *parser) typeSuffix(base string) (string, error) {
	name, err := p.qualify(base)
	if err != nil {
		return "", err
	}
	if p.check("[") {
		args, err := p.parseTypeArgs()
		if err != nil {
			return "", err
		}
		name += args
	}
	return name, nil
}

// parseTypeRef reads a full type reference: a name, an optional `.name`
// qualification, and optional `[...]` generic arguments, which nest.
func (p *parser) parseTypeRef() (string, error) {
	name, err := p.expectIdent()
	if err != nil {
		return "", err
	}
	return p.typeSuffix(name)
}

// parseTypeExpr parses one type in a type position: a function type, or a plain
// type reference (a name, a qualified name, or a generic). It is what a type
// argument or a struct field type is, wherever a `fn(...)` may appear alongside
// an ordinary name.
// atFnType reports whether a function type starts here, in either spelling:
// the `fn` keyword or the capitalised `Fn` that matches every other type name
// in the systems dialect.
func (p *parser) atFnType() bool {
	if p.check("fn") {
		return true
	}
	return p.peek(0).Kind == lexer.IDENT && p.peek(0).Value == "Fn" && p.peek(1).Value == "("
}

func (p *parser) parseTypeExpr() (string, error) {
	// `fn(T) -> R` is the function type. `Fn(T) -> R` is the same type: every
	// other type in the systems dialect is capitalised (I64, Str, Arr[T]), so
	// that is the spelling half the ecosystem reached for, and a capitalised
	// name in type position cannot mean anything else.
	if p.atFnType() {
		return p.parseFnType()
	}
	return p.parseTypeRef()
}

// parseFnType parses a function-type annotation `fn(T, ...) -> R`. Each argument
// and the result is itself a type, so function types nest: a higher-order
// callback is `fn(fn(F64) -> F64) -> F64`. Systems-mode type annotations are
// advisory, so this returns the type as its text form and the checker treats it
// as an unresolved name it does not check, the same as any other systems type.
// It assumes the current token is `fn`.
func (p *parser) parseFnType() (string, error) {
	p.next() // 'fn' or 'Fn'
	if _, err := p.expect("("); err != nil {
		return "", err
	}
	out := "fn("
	if !p.check(")") {
		for i := 0; ; i++ {
			t, err := p.parseTypeExpr()
			if err != nil {
				return "", err
			}
			if i > 0 {
				out += ", "
			}
			out += t
			if !p.match(",") {
				break
			}
		}
	}
	if _, err := p.expect(")"); err != nil {
		return "", err
	}
	out += ")"
	if p.match("->") {
		r, err := p.parseTypeExpr()
		if err != nil {
			return "", err
		}
		out += " -> " + r
	}
	return out, nil
}

// parseTypeArgs reads `[T, U, ...]` after a type name, each T a full type
// reference, and returns the bracketed text, e.g. "[Str, Arr[I64]]". It assumes
// the current token is "[".
func (p *parser) parseTypeArgs() (string, error) {
	p.next() // '['
	out := "["
	first := true
	for {
		a, err := p.parseTypeRef()
		if err != nil {
			return "", err
		}
		if !first {
			out += ", "
		}
		out += a
		first = false
		if !p.match(",") {
			break
		}
	}
	if _, err := p.expect("]"); err != nil {
		return "", err
	}
	return out + "]", nil
}

func (p *parser) parseParams() ([]ast.Param, error) {
	if _, err := p.expect("("); err != nil {
		return nil, err
	}
	var params []ast.Param
	if !p.check(")") {
		param, err := p.parseParam()
		if err != nil {
			return nil, err
		}
		params = append(params, param)
		for p.match(",") {
			if p.check(")") {
				break
			}
			param, err := p.parseParam()
			if err != nil {
				return nil, err
			}
			params = append(params, param)
		}
	}
	if _, err := p.expect(")"); err != nil {
		return nil, err
	}
	return params, nil
}

func (p *parser) parseUnitDecl() (ast.Stmt, error) {
	line := p.next().Line // 'unit'
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	return &ast.UnitDecl{Name: name, Line: line}, nil
}

func (p *parser) parseParam() (ast.Param, error) {
	name, err := p.expectIdent()
	if err != nil {
		return ast.Param{}, err
	}
	param := ast.Param{Name: name}
	if p.match(":") {
		// `[` starts a shape; otherwise a unit expression, or a bare name that
		// the checker resolves as a record type or a unit.
		if p.check("[") {
			shape, err := p.parseShapeAnno()
			if err != nil {
				return ast.Param{}, err
			}
			param.Shape = shape
		} else if p.atFnType() {
			// A function-typed parameter, e.g. `step: fn(Tree, Tensor) -> Tree`.
			// Advisory, kept as text like any other systems-mode type name.
			param.TypeName, err = p.parseFnType()
			if err != nil {
				return ast.Param{}, err
			}
		} else {
			u, err := p.parseUnitExpr()
			if err != nil {
				return ast.Param{}, err
			}
			if len(u.Factors) == 1 && u.Factors[0].Exp == 1 {
				param.TypeName = u.Factors[0].Name // bare name: type or unit
				// A `.` or `[` makes it a qualified or generic type name
				// (`cp.Caps`, `Arr[I64]`): units are never qualified or
				// bracketed, so the suffix is unambiguously part of a type.
				if p.check(".") || p.check("[") {
					param.TypeName, err = p.typeSuffix(param.TypeName)
					if err != nil {
						return ast.Param{}, err
					}
				}
			} else {
				param.Unit = u
			}
		}
	}
	return param, nil
}

// parseUnitExpr parses a scalar unit expression: `USD`, `USD/year`, `1/year`,
// `USD*share^-1`, `year^2`.
func (p *parser) parseUnitExpr() (*ast.UnitAnno, error) {
	anno := &ast.UnitAnno{}
	if err := p.parseUnitFactor(anno, 1); err != nil {
		return nil, err
	}
	for {
		if p.match("*") {
			if err := p.parseUnitFactor(anno, 1); err != nil {
				return nil, err
			}
		} else if p.match("/") {
			if err := p.parseUnitFactor(anno, -1); err != nil {
				return nil, err
			}
		} else {
			break
		}
	}
	return anno, nil
}

func (p *parser) parseUnitFactor(anno *ast.UnitAnno, sign int) error {
	t := p.peek(0)
	if t.Kind == lexer.NUMBER {
		p.next()
		if t.Value != "1" {
			return p.errf(t, "a numeric unit factor must be 1 (dimensionless)")
		}
		return nil
	}
	if t.Kind != lexer.IDENT {
		return p.errf(t, "expected a unit name")
	}
	p.next()
	exp := sign
	if p.match("^") {
		neg := p.match("-")
		nt := p.peek(0)
		if nt.Kind != lexer.NUMBER {
			return p.errf(nt, "expected a unit exponent")
		}
		p.next()
		k, err := strconv.Atoi(nt.Value)
		if err != nil {
			return p.errf(nt, "unit exponent must be an integer")
		}
		exp = sign * k
		if neg {
			exp = -exp
		}
	}
	anno.Factors = append(anno.Factors, ast.UnitFactor{Name: t.Value, Exp: exp})
	return nil
}

func (p *parser) parseTypeDecl() (ast.Stmt, error) {
	line := p.next().Line // 'type'
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect("="); err != nil {
		return nil, err
	}
	if _, err := p.expect("{"); err != nil {
		return nil, err
	}
	decl := &ast.TypeDecl{Name: name, Line: line}
	for !p.check("}") && !p.atEnd() {
		fieldName, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(":"); err != nil {
			return nil, err
		}
		shape, err := p.parseShapeAnno()
		if err != nil {
			return nil, err
		}
		decl.Fields = append(decl.Fields, ast.TypeField{Name: fieldName, Shape: shape})
		if !p.match(",") {
			break
		}
	}
	if _, err := p.expect("}"); err != nil {
		return nil, err
	}
	return decl, nil
}

// parseEnumDecl parses `enum Name { Case, Case(Payload), ... }`. A case is a
// name with an optional single payload type in parentheses; cases are separated
// by commas and a trailing comma is allowed.
// parseTypeParams parses the `[T, U]` a declaration may carry after its name.
// It returns nil when there is none, which is the shape of every declaration
// written before 1.7. The names are ordinary identifiers; what makes one a type
// parameter is being listed here, and it is in scope for the declaration only.
func (p *parser) parseTypeParams() ([]string, error) {
	if !p.check("[") {
		return nil, nil
	}
	p.next()
	var names []string
	for {
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		names = append(names, name)
		if p.match(",") {
			continue
		}
		if _, err := p.expect("]"); err != nil {
			return nil, err
		}
		break
	}
	return names, nil
}

func (p *parser) parseEnumDecl() (ast.Stmt, error) {
	line := p.next().Line // 'enum'
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	typeParams, err := p.parseTypeParams()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect("{"); err != nil {
		return nil, err
	}
	decl := &ast.EnumDecl{Name: name, TypeParams: typeParams, Line: line}
	for !p.check("}") && !p.atEnd() {
		vname, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		v := ast.EnumVariant{Name: vname}
		if p.check("(") {
			p.next()
			payload, err := p.parseTypeRef()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(")"); err != nil {
				return nil, err
			}
			v.HasPayload = true
			v.Payload = payload
		}
		decl.Variants = append(decl.Variants, v)
		if !p.match(",") {
			break
		}
	}
	if _, err := p.expect("}"); err != nil {
		return nil, err
	}
	return decl, nil
}

// parseStructDecl parses `struct Name { field: Type, ... }`. A field's type is a
// full type reference (a name, qualified, or generic), kept as advisory text;
// fields are comma-separated and a trailing comma is allowed.
func (p *parser) parseStructDecl() (ast.Stmt, error) {
	line := p.next().Line // 'struct'
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	typeParams, err := p.parseTypeParams()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect("{"); err != nil {
		return nil, err
	}
	decl := &ast.StructDecl{Name: name, TypeParams: typeParams, Line: line}
	for !p.check("}") && !p.atEnd() {
		fname, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(":"); err != nil {
			return nil, err
		}
		ftype, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		decl.Fields = append(decl.Fields, ast.StructField{Name: fname, Type: ftype})
		if !p.match(",") {
			break
		}
	}
	if _, err := p.expect("}"); err != nil {
		return nil, err
	}
	return decl, nil
}

// parseMatch parses `match subject { pattern => body, ... }`. Arms are separated
// by commas (trailing comma allowed); a body is a single expression or a block.
func (p *parser) parseMatch() (ast.Expr, error) {
	line := p.next().Line // 'match'
	subject, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect("{"); err != nil {
		return nil, err
	}
	m := &ast.Match{Subject: subject, Line: line}
	for !p.check("}") && !p.atEnd() {
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		// A guard is `if cond` between the pattern and the arrow. It is parsed
		// as a grouped expression for the same reason the arm body is: the
		// condition has no statement boundary of its own here.
		var guard ast.Expr
		if p.check("if") {
			p.next()
			p.groupDepth++
			g, err := p.parseExpr()
			p.groupDepth--
			if err != nil {
				return nil, err
			}
			guard = g
		}
		if _, err := p.expect("=>"); err != nil {
			return nil, err
		}
		// The arm body is a statement, so `Ok(v) => v`, `_ => return x`,
		// `Ok(b) => acc = b` and `None => { ... }` are all arms.
		//
		// A body that is not a block is parsed as if inside a grouping. The
		// rules that end a statement at a line starting with `+`/`-` read
		// indentation against the column the statement began at, and an arm
		// body's own first token sets that column, so
		//
		//	Some(v) => "got "
		//	  + str(v),
		//
		// looked like a new statement starting at `+` and was a syntax error --
		// on a continuation that is legal in every other position. Inside an arm
		// there is no statement for one to begin: what follows the body is a
		// comma or the closing brace. A block body is left alone, because the
		// statements inside it do have boundaries and want the ordinary rule.
		grouped := !p.check("{")
		if grouped {
			p.groupDepth++
		}
		body, err := p.parseStmt()
		if grouped {
			p.groupDepth--
		}
		if err != nil {
			return nil, err
		}
		m.Arms = append(m.Arms, ast.MatchArm{Pattern: pat, Guard: guard, Body: body})
		if !p.match(",") {
			break
		}
	}
	if _, err := p.expect("}"); err != nil {
		return nil, err
	}
	return m, nil
}

// parsePattern parses a match arm's pattern. A pattern is one of:
//
//	_              matches anything, binds nothing
//	name           matches anything, binds it
//	3, "s", true   matches by equality
//	Variant        matches the case, whatever it carries
//	Variant(pat)   matches the case and matches its payload against pat
//
// and the last nests, so `Ok(Some(v))` and `Err(-1)` are patterns. The rule
// that tells a variant from a binder is the initial letter: `Some` names a
// case, `some` binds. Every variant in the language and its libraries is
// upper-case initial, and a lower-case one would previously have been read as
// a case that no enum declares, which the checker refused to judge.
func (p *parser) parsePattern() (ast.MatchPattern, error) {
	t := p.peek(0)
	switch {
	case t.Kind == lexer.NUMBER, t.Kind == lexer.STRING,
		t.Kind == lexer.KEYWORD && (t.Value == "true" || t.Value == "false"),
		t.Value == "-" && p.peek(1).Kind == lexer.NUMBER:
		lit, err := p.parsePatternLiteral()
		if err != nil {
			return ast.MatchPattern{}, err
		}
		return ast.MatchPattern{Kind: ast.PatLiteral, Lit: lit, Line: t.Line}, nil
	}
	name, err := p.expectIdent()
	if err != nil {
		return ast.MatchPattern{}, err
	}
	// A variant may be written with something in front, `Opt.None` or
	// `ast.EBool(b)`, the same way it is written where a value is constructed.
	// The qualifier only says where the variant comes from, and variants are
	// resolved by name, so it is read and dropped. This runs before the
	// case rule below, because the qualifier is a type name or a module alias
	// and either may be lower-case: what decides is the name after the dot.
	if p.check(".") {
		p.next()
		variant, err := p.expectIdent()
		if err != nil {
			return ast.MatchPattern{}, err
		}
		name = variant
	} else if !startsUpper(name) {
		if p.check("(") {
			return ast.MatchPattern{}, p.errf(p.peek(0), "%s is a binding, not a variant: a case name starts with a capital letter", name)
		}
		if name == "_" {
			return ast.MatchPattern{Kind: ast.PatBinding, Line: t.Line}, nil
		}
		return ast.MatchPattern{Kind: ast.PatBinding, Binding: name, Line: t.Line}, nil
	}
	pat := ast.MatchPattern{Kind: ast.PatVariant, Variant: name, Line: t.Line}
	if p.check("(") {
		p.next()
		sub, err := p.parsePattern()
		if err != nil {
			return ast.MatchPattern{}, err
		}
		pat.Sub = &sub
		if _, err := p.expect(")"); err != nil {
			return ast.MatchPattern{}, err
		}
	}
	return pat, nil
}

// parsePatternLiteral reads the literal forms a pattern may match by equality.
// A leading `-` belongs to the number: `Err(-1)` is one pattern, not a
// negation applied to one.
func (p *parser) parsePatternLiteral() (ast.Expr, error) {
	t := p.peek(0)
	if t.Value == "-" && p.peek(1).Kind == lexer.NUMBER {
		p.next()
		n := p.peek(0)
		num, err := p.patternNumber()
		if err != nil {
			return nil, err
		}
		return &ast.Unary{Op: "-", Operand: num, Line: n.Line}, nil
	}
	switch t.Kind {
	case lexer.NUMBER:
		return p.patternNumber()
	case lexer.STRING:
		p.next()
		return &ast.StringLit{Value: t.Value, Line: t.Line}, nil
	}
	p.next()
	return &ast.BoolLit{Value: t.Value == "true", Line: t.Line}, nil
}

// patternNumber reads one numeric literal in a pattern, keeping the digits as
// written so an I64 above 2^53 compares exactly rather than through an f64.
func (p *parser) patternNumber() (*ast.NumberLit, error) {
	t := p.peek(0)
	p.next()
	v, err := strconv.ParseFloat(t.Value, 64)
	if err != nil {
		return nil, p.errf(t, "invalid number %q", t.Value)
	}
	return &ast.NumberLit{Value: v, Text: t.Value, Line: t.Line}, nil
}

// startsUpper is the variant-versus-binder rule, applied to the first byte.
// Names in twill are ASCII identifiers.
func startsUpper(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// parseShapeAnno parses "[d0, d1, ...]" where each dim is an integer or "_"
// (unknown). "[]" is a scalar.
func (p *parser) parseShapeAnno() (*ast.ShapeAnno, error) {
	if _, err := p.expect("["); err != nil {
		return nil, err
	}
	anno := &ast.ShapeAnno{Dims: []ast.Dim{}}
	if !p.check("]") {
		dim, err := p.parseDim()
		if err != nil {
			return nil, err
		}
		anno.Dims = append(anno.Dims, dim)
		for p.match(",") {
			if p.check("]") {
				break
			}
			dim, err := p.parseDim()
			if err != nil {
				return nil, err
			}
			anno.Dims = append(anno.Dims, dim)
		}
	}
	if _, err := p.expect("]"); err != nil {
		return nil, err
	}
	return anno, nil
}

func (p *parser) parseDim() (ast.Dim, error) {
	t := p.peek(0)
	if t.Kind == lexer.NUMBER {
		p.next()
		n, err := strconv.Atoi(t.Value)
		if err != nil || n < 0 {
			return ast.Dim{}, p.errf(t, "shape dimension must be a non-negative integer")
		}
		return ast.ConcreteDim(n), nil
	}
	if t.Kind == lexer.IDENT {
		// "_" is an anonymous unknown dim; any other name is a shape variable.
		p.next()
		if t.Value == "_" {
			return ast.AnonDim(), nil
		}
		return ast.VarDim(t.Value), nil
	}
	return ast.Dim{}, p.errf(t, "expected a dimension size or name")
}

func (p *parser) parseArgs() ([]ast.Expr, error) {
	if _, err := p.expect("("); err != nil {
		return nil, err
	}
	p.groupDepth++
	defer func() { p.groupDepth-- }()
	var args []ast.Expr
	if !p.check(")") {
		a, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		for p.match(",") {
			if p.check(")") {
				break
			}
			a, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
		}
	}
	if _, err := p.expect(")"); err != nil {
		return nil, err
	}
	return args, nil
}

func (p *parser) expectIdent() (string, error) {
	t := p.peek(0)
	if t.Kind == lexer.IDENT {
		p.next()
		return t.Value, nil
	}
	return "", p.errf(t, "expected identifier but found %q", tokenText(t))
}

// isNumericElem reports whether e can appear inside a tensor literal.
func isNumericElem(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.NumberLit:
		return true
	case *ast.TensorLit:
		return true
	case *ast.Unary:
		if v.Op == "-" {
			_, ok := v.Operand.(*ast.NumberLit)
			return ok
		}
	}
	return false
}

func tokenText(t lexer.Token) string {
	if t.Value != "" {
		return t.Value
	}
	switch t.Kind {
	case lexer.EOF:
		return "end of input"
	default:
		return "?"
	}
}
