// Package lexer turns Twill source text into a token stream.
package lexer

import (
	"fmt"
	"strings"
)

type Kind int

const (
	NUMBER Kind = iota
	STRING
	IDENT
	KEYWORD
	OP
	PUNCT
	EOF
)

type Token struct {
	Kind  Kind
	Value string
	Line  int
	Col   int
}

// SyntaxError is a lexing or parsing error carrying a source position.
type SyntaxError struct {
	Msg  string
	Line int
	Col  int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("line %d:%d: %s", e.Line, e.Col, e.Msg)
}

var keywords = map[string]bool{
	"let": true, "const": true, "fn": true, "if": true, "else": true,
	"while": true, "for": true, "in": true, "return": true,
	"import": true, "true": true, "false": true,
	"and": true, "or": true, "not": true,
	"xor": true, "shl": true, "shr": true,
	"band": true, "bor": true,
	"enum": true, "match": true, "struct": true,
	"break": true, "continue": true,
}

// multiOps are matched greedily before single-character operators.
var multiOps = []string{"==", "!=", "<=", ">=", "->", "=>", "&&", "||", "//"}

// Comment is a source comment, kept so tools like the formatter can preserve
// it. Trailing is true when code precedes it on the same line.
type Comment struct {
	Line     int
	Text     string
	Trailing bool
}

// Tokenize scans src into tokens, ending with an EOF token.
func Tokenize(src string) ([]Token, error) {
	toks, _, err := TokenizeWithComments(src)
	return toks, err
}

// TokenizeWithComments is like Tokenize but also returns the comments found.
func TokenizeWithComments(src string) ([]Token, []Comment, error) {
	var toks []Token
	var comments []Comment
	runes := []rune(src)
	i, line, col := 0, 1, 1

	peek := func(o int) rune {
		if i+o < len(runes) {
			return runes[i+o]
		}
		return 0
	}
	advance := func() rune {
		ch := runes[i]
		i++
		if ch == '\n' {
			line++
			col = 1
		} else {
			col++
		}
		return ch
	}

	for i < len(runes) {
		ch := runes[i]

		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			advance()
			continue
		}
		if ch == '#' {
			cl := line
			advance() // consume '#'
			var sb strings.Builder
			for i < len(runes) && peek(0) != '\n' {
				sb.WriteRune(advance())
			}
			trailing := len(toks) > 0 && toks[len(toks)-1].Line == cl
			comments = append(comments, Comment{Line: cl, Text: strings.TrimSpace(sb.String()), Trailing: trailing})
			continue
		}

		startLine, startCol := line, col

		if isDigit(ch) || (ch == '.' && isDigit(peek(1))) {
			var sb strings.Builder
			for i < len(runes) && isDigit(peek(0)) {
				sb.WriteRune(advance())
			}
			if peek(0) == '.' {
				sb.WriteRune(advance())
				for i < len(runes) && isDigit(peek(0)) {
					sb.WriteRune(advance())
				}
			}
			if peek(0) == 'e' || peek(0) == 'E' {
				sb.WriteRune(advance())
				if peek(0) == '+' || peek(0) == '-' {
					sb.WriteRune(advance())
				}
				for i < len(runes) && isDigit(peek(0)) {
					sb.WriteRune(advance())
				}
			}
			toks = append(toks, Token{NUMBER, sb.String(), startLine, startCol})
			continue
		}

		if isIdentStart(ch) {
			var sb strings.Builder
			for i < len(runes) && isIdentPart(peek(0)) {
				sb.WriteRune(advance())
			}
			name := sb.String()
			kind := IDENT
			if keywords[name] {
				kind = KEYWORD
			}
			toks = append(toks, Token{kind, name, startLine, startCol})
			continue
		}

		if ch == '"' {
			advance()
			var sb strings.Builder
			for i < len(runes) && peek(0) != '"' {
				c := advance()
				if c == '\\' {
					// The backslash may be the last byte in the file, in which
					// case there is no escaped character to read and advance
					// would index past the end. The file's problem is the
					// missing close quote, not the backslash, so report that:
					// the same diagnosis the self-hosted lexer gives, which is
					// how this was found (docs/needs.md, NEEDS-33).
					if i >= len(runes) {
						return nil, nil, &SyntaxError{"unterminated string", startLine, startCol}
					}
					sb.WriteRune(unescape(advance()))
				} else {
					sb.WriteRune(c)
				}
			}
			if i >= len(runes) {
				return nil, nil, &SyntaxError{"unterminated string", startLine, startCol}
			}
			advance() // closing quote
			toks = append(toks, Token{STRING, sb.String(), startLine, startCol})
			continue
		}

		two := string(ch)
		if peek(1) != 0 {
			two = string([]rune{ch, peek(1)})
		}
		if contains(multiOps, two) {
			advance()
			advance()
			toks = append(toks, Token{OP, two, startLine, startCol})
			continue
		}

		if strings.ContainsRune("+-*/%@^<>=!?", ch) {
			advance()
			toks = append(toks, Token{OP, string(ch), startLine, startCol})
			continue
		}
		// '.' is field access here; number literals like 3.14 and .5 were
		// already handled by the number branch above.
		if strings.ContainsRune("()[]{},:;.", ch) {
			advance()
			toks = append(toks, Token{PUNCT, string(ch), startLine, startCol})
			continue
		}

		return nil, nil, &SyntaxError{fmt.Sprintf("unexpected character %q", string(ch)), startLine, startCol}
	}

	toks = append(toks, Token{EOF, "", line, col})
	return toks, comments, nil
}

func isDigit(ch rune) bool { return ch >= '0' && ch <= '9' }
func isIdentStart(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}
func isIdentPart(ch rune) bool { return isIdentStart(ch) || isDigit(ch) }

func unescape(ch rune) rune {
	switch ch {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	default:
		return ch
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
