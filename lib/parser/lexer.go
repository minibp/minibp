// parser/lexer.go - Blueprint lexer using text/scanner
package parser

import (
	"fmt"
	"io"
	"strconv"
	"text/scanner"
)

// TokenType represents the type of a lexical token.
// Each token type corresponds to a specific kind of syntax element in Blueprint.
type TokenType int

const (
	// Special tokens
	EOF     TokenType = iota // End of file
	ILLEGAL                  // Unknown/invalid token

	// Literals - values that appear in the source code
	IDENT  // Identifiers: variable names, module types, property names
	STRING // String literals: "hello", `raw string`
	INT    // Integer literals: 42, 100
	BOOL   // Boolean literals: true, false

	// Symbols - punctuation and operators
	LPAREN   // (  Left parenthesis
	RPAREN   // )  Right parenthesis
	LBRACE   // {  Left brace
	RBRACE   // }  Right brace
	LBRACKET // [  Left bracket
	RBRACKET // ]  Right bracket
	COLON    // :  Colon (property separator)
	COMMA    // ,  Comma (list/property separator)
	PLUS     // + Plus (concatenation operator)
	ASSIGN   // = Equals (assignment operator)
	PLUSEQ   // += Plus-equals (concatenation assignment)
	UNSET    // unset keyword
	AT       // @ At sign (for any @ var binding in select)
)

// Token represents a lexical token with its type, literal value, and source position.
// This is the fundamental unit of output from the lexer.
type Token struct {
	Type    TokenType        // The type of this token
	Literal string           // The actual text of the token (for identifiers, strings, etc.)
	Pos     scanner.Position // Source position (file, line, column) for error reporting
}

// Lexer wraps text/scanner to provide Blueprint-specific tokenization.
// It converts raw source text into a stream of tokens that the parser can consume.
type Lexer struct {
	scanner scanner.Scanner // The underlying Go scanner
	ch      rune            // Current character being processed
	errors  []error         // List of lexer errors encountered
}

// NewLexer creates a new lexer from an ioReader.
// It initializes the Go scanner with appropriate mode settings for Blueprint:
// - ScanIdents: Recognize identifiers
// - ScanInts: Recognize integer literals
// - ScanStrings: Recognize quoted strings ("...")
// /- ScanRawStrings: Recognize raw strings (`...`)
// - ScanComments: Skip comments
// Parameters:
//   - r: The input reader containing source code
//   - fileName: The name of the file being lexed
//
// Returns:
//   - A new Lexer instance
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

// next advances to the next character in the input.
// It calls the underlying scanner's Scan() method to get the next rune.
func (l *Lexer) next() {
	l.ch = l.scanner.Scan()
}

// peek returns the next character without advancing the scanner.
// This allows the lexer to look ahead at the upcoming character
// to determine how to tokenize it (e.g., to distinguish += from +).
// Returns:
//   - The next rune in the input
func (l *Lexer) peek() rune {
	return l.scanner.Peek()
}

// NextToken returns the next token from the input.
// This is the main entry point for the parser to consume tokens.
// It handles all token types: special tokens (EOF, ILLEGAL), literals (IDENT, STRING, INT, BOOL),
// and symbols (parentheses, braces, brackets, colon, comma, operators).
// Returns:
//   - Token: The next lexical token
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
	case '@':
		tok.Type = AT
		tok.Literal = "@"
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
		switch tok.Literal {
		case "true", "false":
			tok.Type = BOOL
		case "unset":
			tok.Type = UNSET
		default:
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
			// Record error for illegal characters
			l.errors = append(l.errors, fmt.Errorf("%s: illegal character '%c'", l.scanner.Position, l.ch))
			l.next()
		}
	}

	return tok
}

// Position returns the current scanner position.
// This is used for error reporting to show exactly where in the source file an error occurred.
func (l *Lexer) Position() scanner.Position {
	return l.scanner.Position
}

// Error creates an error with position information.
// This is a helper for generating lexer errors with the current source position.
// Parameters:
//   - format: Printf-style format string
//   - args: Arguments for the format string
//
// Returns:
//   - An error with position information
func (l *Lexer) Error(format string, args ...interface{}) error {
	return fmt.Errorf("%s: %s", l.scanner.Position, fmt.Sprintf(format, args...))
}

// Errors returns lexer diagnostics collected from text/scanner.
// These are errors encountered during scanning, such as invalid characters or malformed tokens.
// Returns:
//   - []error: List of lexer errors
func (l *Lexer) Errors() []error {
	return l.errors
}

// Unquote removes quotes from a string literal.
// This is a wrapper around strconv.Unquote that handles Go string syntax.
// Parameters:
//   - s: A string literal (including quotes)
//
// Returns:
//   - string: The unquoted string content
//   - error: nil if successful, otherwise an error (e.g., invalid escape sequence)
func Unquote(s string) (string, error) {
	return strconv.Unquote(s)
}

// String returns a human-readable representation of a TokenType.
// This is useful for debugging and error messages.
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
	case UNSET:
		return "UNSET"
	case AT:
		return "AT"
	default:
		return fmt.Sprintf("Token(%d)", t)
	}
}

// Helper for better error messages

// TokenError represents an error that occurred during tokenization.
// It includes the position in the source file and a descriptive message.
type TokenError struct {
	Pos     scanner.Position // Position where the error occurred
	Message string           // Description of the error
}

// Error returns a formatted error string including the position and message.
func (e *TokenError) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Message)
}

// NewTokenError creates a new token error with the given position and message.
// Parameters:
//   - pos: The source position where the error occurred
//   - msg: The error message
//
// Returns:
//   - error: A TokenError with the specified position and message
func NewTokenError(pos scanner.Position, msg string) error {
	return &TokenError{Pos: pos, Message: msg}
}
