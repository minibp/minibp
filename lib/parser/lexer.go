// Package parser provides lexical analysis and parsing for Blueprint build definitions.
// It implements a recursive-descent parser that transforms Blueprint source files
// into an Abstract Syntax Tree (AST) for further processing by the build system.
// The parser handles modules, assignments, expressions, and conditional constructs
// like select() statements for architecture-specific values.
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
// The Position field is used for error reporting to pinpoint exactly where
// in the source file a particular token was found.
type Token struct {
	Type    TokenType        // The type of this token
	Literal string           // The actual text of the token (for identifiers, strings, etc.)
	Pos     scanner.Position // Source position (file, line, column) for error reporting
}

// Lexer wraps text/scanner to provide Blueprint-specific tokenization.
// It converts raw source text into a stream of tokens that the parser can consume.
// The lexer handles Go-compatible string literals (both quoted and raw),
// integer literals, identifiers, and various punctuation symbols.
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
//   - A new Lexer instance ready to produce tokens
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

// next advances the lexer to the next character in the input stream.
// It calls the underlying Go scanner's Scan() method to retrieve the next rune.
// This is the fundamental operation for traversing the source text character by character.
// After calling next(), the ch field contains the next character to be processed.
func (l *Lexer) next() {
	l.ch = l.scanner.Scan()
}

// peek returns the next character without advancing the scanner.
// This allows the lexer to look ahead at the upcoming character
// to determine how to tokenize it (e.g., to distinguish += from +).
// Returns:
//   - The next rune in the input, or EOF if at end of input
func (l *Lexer) peek() rune {
	return l.scanner.Peek()
}

// NextToken returns the next token from the input.
// This is the main entry point for the parser to consume tokens.
// It handles all token types: special tokens (EOF, ILLEGAL), literals (IDENT, STRING, INT, BOOL),
// and symbols (parentheses, braces, brackets, colon, comma, operators).
// The lexer automatically skips comments and whitespace.
// Returns:
//   - Token: The next lexical token with type, literal value, and position
func (l *Lexer) NextToken() Token {
	var tok Token
	tok.Pos = l.scanner.Position

	switch l.ch {
	case scanner.EOF:
		// End of file - return special EOF token
		tok.Type = EOF
		tok.Literal = ""
	case '(':
		// Left parenthesis
		tok.Type = LPAREN
		tok.Literal = "("
		l.next()
	case ')':
		// Right parenthesis
		tok.Type = RPAREN
		tok.Literal = ")"
		l.next()
	case '{':
		// Left brace (opening block)
		tok.Type = LBRACE
		tok.Literal = "{"
		l.next()
	case '}':
		// Right brace (closing block)
		tok.Type = RBRACE
		tok.Literal = "}"
		l.next()
	case '[':
		// Left bracket (opening list)
		tok.Type = LBRACKET
		tok.Literal = "["
		l.next()
	case ']':
		// Right bracket (closing list)
		tok.Type = RBRACKET
		tok.Literal = "]"
		l.next()
	case ':':
		// Colon (property separator in maps)
		tok.Type = COLON
		tok.Literal = ":"
		l.next()
	case ',':
		// Comma (list/element separator)
		tok.Type = COMMA
		tok.Literal = ","
		l.next()
	case '+':
		// Plus operator - check for += compound assignment
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
		// Simple assignment operator
		tok.Type = ASSIGN
		tok.Literal = "="
		l.next()
	case '@':
		// At sign for variable binding in select patterns
		tok.Type = AT
		tok.Literal = "@"
		l.next()
	case scanner.Comment:
		// Skip comments and get next token
		// Comments are filtered out entirely from the token stream
		l.next()
		return l.NextToken()
	case scanner.Int:
		// Integer literal (base-10 number)
		tok.Type = INT
		tok.Literal = l.scanner.TokenText()
		l.next()
	case scanner.String, scanner.RawString:
		// Quoted string literal (single or double quotes, raw with backticks)
		tok.Type = STRING
		tok.Literal = l.scanner.TokenText()
		l.next()
	case scanner.Ident:
		// Identifier - could be keyword, variable name, or module type
		tok.Literal = l.scanner.TokenText()
		switch tok.Literal {
		case "true", "false":
			// Boolean literals
			tok.Type = BOOL
		case "unset":
			// Unset keyword for removing property values
			tok.Type = UNSET
		default:
			// Regular identifier (variable name, module type, property name)
			tok.Type = IDENT
		}
		l.next()
	case '\n', '\t', ' ', '\r':
		// Skip whitespace and get next token
		// All whitespace is treated as token separators
		l.next()
		return l.NextToken()
	default:
		// Unknown character - record error but continue processing
		if l.ch < 0 {
			// Negative character means EOF was reached
			tok.Type = EOF
		} else {
			// Illegal character - not recognized by the scanner
			tok.Type = ILLEGAL
			tok.Literal = string(l.ch)
			// Record error for illegal characters so parser can report them
			l.errors = append(l.errors, fmt.Errorf("%s: illegal character '%c'", l.scanner.Position, l.ch))
			l.next()
		}
	}

	return tok
}

// Position returns the current scanner position.
// This is used for error reporting to show exactly where in the source file an error occurred.
// The position includes filename, line number, and column number.
func (l *Lexer) Position() scanner.Position {
	return l.scanner.Position
}

// Error creates an error with position information.
// This is a helper for generating lexer errors with the current source position.
// It includes the file location in the error message for accurate error reporting.
// Parameters:
//   - format: Printf-style format string
//   - args: Arguments for the format string
//
// Returns:
//   - An error with position information formatted as "filename:line:column: message"
func (l *Lexer) Error(format string, args ...interface{}) error {
	return fmt.Errorf("%s: %s", l.scanner.Position, fmt.Sprintf(format, args...))
}

// Errors returns lexer diagnostics collected from text/scanner.
// These are errors encountered during scanning, such as invalid characters or malformed tokens.
// Lexer errors are collected and returned separately so the parser can decide
// whether to continue or abort processing.
// Returns:
//   - []error: List of lexer errors, empty if no errors encountered
func (l *Lexer) Errors() []error {
	return l.errors
}

// Unquote removes quotes from a string literal.
// This is a wrapper around strconv.Unquote that handles Go string syntax,
// including escape sequences like \n, \t, \", etc.
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
// It converts the internal token type constant to a descriptive string.
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

// TokenError represents an error that occurred during tokenization.
// It includes the position in the source file and a descriptive message.
// This allows errors to be formatted with location information for
// easy identification in the source file.
type TokenError struct {
	Pos     scanner.Position // Position where the error occurred
	Message string           // Description of the error
}

// Error returns a formatted error string including the position and message.
// The format is "filename:line:column: message" for easy parsing by editors.
func (e *TokenError) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Message)
}

// NewTokenError creates a new token error with the given position and message.
// This is a convenience constructor for TokenError that wraps the message
// in the error interface.
// Parameters:
//   - pos: The source position where the error occurred
//   - msg: The error message
//
// Returns:
//   - error: A TokenError with the specified position and message
func NewTokenError(pos scanner.Position, msg string) error {
	return &TokenError{Pos: pos, Message: msg}
}
