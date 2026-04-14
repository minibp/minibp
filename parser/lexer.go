// parser/lexer.go - Blueprint lexer using text/scanner
package parser

import (
	"fmt"
	"io"
	"strconv"
	"text/scanner"
)

// TokenType represents the type of a token
type TokenType int

const (
	// Special tokens
	EOF TokenType = iota
	ILLEGAL

	// Literals
	IDENT  // identifiers
	STRING // string literals
	INT    // integer literals
	BOOL   // true/false

	// Symbols
	LPAREN   // (
	RPAREN   // )
	LBRACE   // {
	RBRACE   // }
	LBRACKET // [
	RBRACKET // ]
	COLON    // :
	COMMA    // ,
	PLUS     // +
	ASSIGN   // =
	PLUSEQ   // +=
)

// Token represents a lexical token
type Token struct {
	Type    TokenType
	Literal string
	Pos     scanner.Position
}

// Lexer wraps text/scanner to provide Blueprint-specific tokenization
type Lexer struct {
	scanner scanner.Scanner
	ch      rune
	errors  []error
}

// NewLexer creates a new lexer from an io.Reader
func NewLexer(r io.Reader, fileName string) *Lexer {
	l := &Lexer{}
	l.scanner.Init(r)
	l.scanner.Filename = fileName
	l.scanner.Error = func(s *scanner.Scanner, msg string) {
		l.errors = append(l.errors, fmt.Errorf("%s: %s", s.Position, msg))
	}
	// Allow scanning strings (quoted and raw) and comments.
	l.scanner.Mode = scanner.ScanIdents | scanner.ScanInts | scanner.ScanStrings | scanner.ScanRawStrings | scanner.ScanComments
	l.scanner.Whitespace = 1<<' ' | 1<<'\t' | 1<<'\n' | 1<<'\r'
	l.next()
	return l
}

// next advances to the next character
func (l *Lexer) next() {
	l.ch = l.scanner.Scan()
}

// peek returns the next character without advancing
func (l *Lexer) peek() rune {
	return l.scanner.Peek()
}

// NextToken returns the next token from the input
func (l *Lexer) NextToken() Token {
	var tok Token
	tok.Pos = l.scanner.Position

	switch l.ch {
	case scanner.EOF:
		tok.Type = EOF
		tok.Literal = ""
	case '(':
		tok.Type = LPAREN
		tok.Literal = "("
		l.next()
	case ')':
		tok.Type = RPAREN
		tok.Literal = ")"
		l.next()
	case '{':
		tok.Type = LBRACE
		tok.Literal = "{"
		l.next()
	case '}':
		tok.Type = RBRACE
		tok.Literal = "}"
		l.next()
	case '[':
		tok.Type = LBRACKET
		tok.Literal = "["
		l.next()
	case ']':
		tok.Type = RBRACKET
		tok.Literal = "]"
		l.next()
	case ':':
		tok.Type = COLON
		tok.Literal = ":"
		l.next()
	case ',':
		tok.Type = COMMA
		tok.Literal = ","
		l.next()
	case '+':
		l.next()
		if l.ch == '=' {
			tok.Type = PLUSEQ
			tok.Literal = "+="
			l.next()
		} else {
			tok.Type = PLUS
			tok.Literal = "+"
		}
	case '=':
		tok.Type = ASSIGN
		tok.Literal = "="
		l.next()
	case scanner.Comment:
		// Skip comments and get next token
		l.next()
		return l.NextToken()
	case scanner.Int:
		tok.Type = INT
		tok.Literal = l.scanner.TokenText()
		l.next()
	case scanner.String, scanner.RawString:
		tok.Type = STRING
		tok.Literal = l.scanner.TokenText()
		l.next()
	case scanner.Ident:
		tok.Literal = l.scanner.TokenText()
		// Check for boolean literals
		if tok.Literal == "true" || tok.Literal == "false" {
			tok.Type = BOOL
		} else {
			tok.Type = IDENT
		}
		l.next()
	case '\n', '\t', ' ', '\r':
		// Skip whitespace and get next token
		l.next()
		return l.NextToken()
	default:
		if l.ch < 0 {
			tok.Type = EOF
		} else {
			tok.Type = ILLEGAL
			tok.Literal = string(l.ch)
			l.next()
		}
	}

	return tok
}

// Position returns the current scanner position
func (l *Lexer) Position() scanner.Position {
	return l.scanner.Position
}

// Error creates an error with position information
func (l *Lexer) Error(format string, args ...interface{}) error {
	return fmt.Errorf("%s: %s", l.scanner.Position, fmt.Sprintf(format, args...))
}

// Errors returns lexer diagnostics collected from text/scanner.
func (l *Lexer) Errors() []error {
	return l.errors
}

// Unquote removes quotes from a string literal
func Unquote(s string) (string, error) {
	return strconv.Unquote(s)
}

// String representation of token types
func (t TokenType) String() string {
	switch t {
	case EOF:
		return "EOF"
	case ILLEGAL:
		return "ILLEGAL"
	case IDENT:
		return "IDENT"
	case STRING:
		return "STRING"
	case INT:
		return "INT"
	case BOOL:
		return "BOOL"
	case LPAREN:
		return "LPAREN"
	case RPAREN:
		return "RPAREN"
	case LBRACE:
		return "LBRACE"
	case RBRACE:
		return "RBRACE"
	case LBRACKET:
		return "LBRACKET"
	case RBRACKET:
		return "RBRACKET"
	case COLON:
		return "COLON"
	case COMMA:
		return "COMMA"
	case PLUS:
		return "PLUS"
	case ASSIGN:
		return "ASSIGN"
	case PLUSEQ:
		return "PLUSEQ"
	default:
		return fmt.Sprintf("Token(%d)", t)
	}
}

// Helper for better error messages
type TokenError struct {
	Pos     scanner.Position
	Message string
}

func (e *TokenError) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Message)
}

// NewTokenError creates a new token error
func NewTokenError(pos scanner.Position, msg string) error {
	return &TokenError{Pos: pos, Message: msg}
}
