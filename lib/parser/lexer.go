// Package parser provides lexical analysis and parsing for Blueprint build definitions.
// Lexer subpackage - Tokenization of Blueprint source files.
//
// This package implements the first stage of the Blueprint build system:
// it reads raw source text and produces a stream of tokens.
// The lexer wraps Go's text/scanner package to provide Blueprint-specific tokenization.
//
// Token types:
//   - Special tokens: EOF (end of file), ILLEGAL (invalid character)
//   - Literals: IDENT (identifiers), STRING (string literals), INT (integers), BOOL (true/false)
//   - Symbols: LPAREN, RPAREN, LBRACE, RBRACE, LBRACKET, RBRACKET
//   - Operators: COLON, COMMA, PLUS, ASSIGN, PLUSEQ, UNSET, AT
//
// String support:
//   - Double-quoted strings: "hello world"
//   - Single-quoted strings: 'hello world'
//   - Raw strings: `hello world`
//   - Escape sequences: \n, \t, \\, \", etc.
//
// Error handling:
//   - Lexer errors are collected and returned separately
//   - Invalid characters are reported but scanning continues
//   - Position information is included for all errors
package parser

import (
	"fmt"
	"io"
	"strconv"
	"text/scanner"
)

// TokenType represents the type of a lexical token.
// Each token type corresponds to a specific kind of syntax element in Blueprint.
//
// Token categories:
//   - Special: EOF (end of file), ILLEGAL (unrecognized character)
//   - Literals: IDENT (variable/module names), STRING (quoted text), INT (numbers), BOOL (true/false)
//   - Grouping: LPAREN/RPAREN (function calls, grouping), LBRACE/RBRACE (modules, maps),
//     LBRACKET/RBRACKET (lists)
//   - Operators: COLON (property separator), COMMA (separator), PLUS (concatenation),
//     ASSIGN simple assignment (=), PLUSEQ (+=), UNSET (unset keyword), AT (@ binding)
//
// These token types are the fundamental building blocks that the parser uses to
// understand the syntactic structure of Blueprint source files. The lexer converts
// raw character input into a stream of these typed tokens for consumption by the parser.
type TokenType int

const (
	// Special tokens (internal markers)
	EOF     TokenType = iota // End of file marker - returned when no more input
	ILLEGAL                  // Unknown/invalid character - recorded as error but scanning continues

	// Literals - values that appear directly in source code
	IDENT  // Identifiers: variable names (my_var), module types (cc_binary), property names (srcs)
	STRING // String literals: "hello", 'hello', `raw string`
	INT    // Integer literals: 42, 100, -10
	BOOL   // Boolean literals: true, false

	// Grouping symbols - structure markers
	LPAREN   // (  Left parenthesis - for function calls and grouping
	RPAREN   // )  Right parenthesis
	LBRACE   // {  Left brace - for module blocks and maps
	RBRACE   // }  Right brace
	LBRACKET // [  Left bracket - for lists
	RBRACKET // ]  Right bracket

	// Operators and separators
	COLON  // :  Colon - property separator in maps
	COMMA  // ,  Comma - list/element separator
	PLUS   // +  Plus - concatenation operator
	ASSIGN // =  Equals - simple assignment operator
	PLUSEQ // += Plus-equals - concatenation assignment operator
	UNSET  // unset keyword - for removing property values in select
	AT     // @  At sign - for any @ var binding in select patterns
)

// Token represents a lexical token with its type, literal value, and source position.
// This is the fundamental unit of output from the lexer.
//
// Token structure:
//   - Type: The token type (TokenType enum)
//   - Literal: The actual text from the source (for identifiers, strings, numbers, symbols)
//   - Pos: Source position (filename, line, column) for error reporting
//
// Example tokens:
//   - Token{Type: IDENT, Literal: "cc_binary", Pos: file.bp:1:1}
//   - Token{Type: STRING, Literal: "\"hello\"", Pos: file.bp:2:5}
//   - Token{Type: ASSIGN, Literal: "=", Pos: file.bp:3:10}
//
// The Token struct is the primary data structure that flows between the lexer
// and parser. Each call to NextToken() returns one Token with complete information
// needed for parsing and accurate error reporting.
type Token struct {
	Type    TokenType        // The type of this token
	Literal string           // The actual text of the token (for identifiers, strings, etc.)
	Pos     scanner.Position // Source position (file, line, column) for error reporting
}

// Lexer wraps text/scanner to provide Blueprint-specific tokenization.
// It converts raw source text into a stream of Token values that the parser can consume.
//
// The lexer handles:
//   - Go-compatible string literals (double-quoted, single-quoted, raw)
//   - Integer literals (decimal integers)
//   - Identifiers (variable names, module types, property names)
//   - Keywords (true, false, unset)
//   - Symbols (parentheses, braces, brackets)
//   - Operators (=, +=, :, ,, +)
//   - Comments (skipped entirely)
//
// Token production:
//   - NextToken() returns the next token in the input stream
//   - Tokenization is incremental - tokens are produced on demand
//   - The scanner scans ahead to find token boundaries
//
// Error handling:
//   - Invalid characters are recorded in the errors slice
//   - Scanning continues after errors for incremental reporting
//
// This lexer is the first stage in the Blueprint build system pipeline.
// It sits between the raw input stream and the parser, converting character
// sequences into meaningful tokens that represent the syntactic structure
// of the Blueprint source code.
type Lexer struct {
	scanner scanner.Scanner // Underlying Go text/scanner for character scanning
	ch      rune            // Most recent rune scanned; cached for peek() and multi-char token processing
	errors  []error         // Non-fatal lexer errors (invalid chars, malformed strings); returned via Errors()
}

// NewLexer creates a new lexer from an io.Reader.
// Initializes the Go scanner with Blueprint-specific mode settings.
//
// Scanner configuration:
//   - ScanIdents: Recognize identifiers (variable names, module types)
//   - ScanInts: Recognize integer literals (42, 100, -10)
//   - ScanStrings: Recognize quoted strings ("hello", 'hello')
//   - ScanRawStrings: Recognize raw strings (`hello`)
//   - ScanComments: Skip comments entirely from the token stream
//
// Additional setup:
//   - Whitespace (space, tab, newline, carriage return) is skipped
//   - Errors are collected in the errors slice via callback
//   - Filename is stored for error reporting
//
// Parameters:
//   - r: The input reader containing Blueprint source code
//   - fileName: The name of the file being lexed (used for error messages)
//
// Returns:
//   - *Lexer: New lexer instance ready to produce tokens
//
// Edge cases:
//   - Empty input: Lexer will return EOF on first NextToken() call
//   - Invalid UTF-8: Scanner will report errors via the error callback
//
// Notes:
//   - The lexer primes itself with the first character immediately after creation
//   - Comments are skipped entirely and not emitted as tokens
//
// Example usage:
//
//	lexer := NewLexer(strings.NewReader("cc_library { srcs: [\"*.c\"] }"), "Android.bp")
//	for tok := lexer.NextToken(); tok.Type != EOF; tok = lexer.NextToken() {
//	    // Process token...
//	}
func NewLexer(r io.Reader, fileName string) *Lexer {
	l := &Lexer{}
	l.scanner.Init(r)
	l.scanner.Filename = fileName
	l.scanner.Error = func(s *scanner.Scanner, msg string) {
		l.errors = append(l.errors, fmt.Errorf("%s: %s", s.Position, msg))
	}
	// Allow scanning strings (quoted and raw) and comments.
	l.scanner.Mode = scanner.ScanIdents | scanner.ScanInts | scanner.ScanStrings | scanner.ScanRawStrings | scanner.ScanComments // Set scanner to recognize required token types
	l.scanner.Whitespace = 1<<' ' | 1<<'\t' | 1<<'\n' | 1<<'\r'                                                                  // Configure scanner to skip whitespace characters
	l.next()                                                                                                                     // Prime lexer with first character from input
	return l
}

// next advances the lexer to the next character in the input stream.
// Calls the underlying Go scanner's Scan() method to retrieve the next rune.
// After calling next(), the ch field contains the next character to be processed.
//
// Parameters: None
//
// Returns: None (updates l.ch field directly)
//
// Edge cases:
//   - At end of input, l.ch is set to scanner.EOF
//   - Whitespace and comments are skipped automatically by the underlying scanner
//
// Notes:
//   - Internal method, not intended for external use
func (l *Lexer) next() {
	l.ch = l.scanner.Scan()
}

// peek returns the next character without advancing the scanner.
// Allows the lexer to look ahead at the upcoming character to determine tokenization
// (e.g., distinguish += from + by checking for '=' after '+').
//
// Parameters: None
//
// Returns:
//   - rune: The next rune in the input, or scanner.EOF if at end of input
//
// Edge cases:
//   - Returns scanner.EOF if at end of input
//   - Does not skip whitespace or comments (relies on underlying scanner state)
//
// Notes:
//   - Internal method, not intended for external use
func (l *Lexer) peek() rune {
	return l.scanner.Peek()
}

// NextToken returns the next token from the input stream.
// This is the main entry point for the parser to consume tokens.
// Handles all token types: special tokens (EOF, ILLEGAL), literals (IDENT, STRING, INT, BOOL),
// and symbols (parentheses, braces, brackets, operators, separators).
// Automatically skips comments and whitespace.
//
// Token processing flow:
//  1. Record current source position for the token
//  2. Dispatch on current character to determine token type
//  3. Classify identifiers as keywords (bool, unset) or regular identifiers
//  4. Check for multi-character tokens (e.g., +=) via peek()
//  5. Advance to next character after processing
//  6. Return complete token
//
// Parameters: None
//
// Returns:
//   - Token: Next lexical token with type, literal value, and source position
//
// Edge cases:
//   - EOF: Returns Token with Type=EOF when no more input
//   - ILLEGAL: Returns Token with Type=ILLEGAL for unrecognized characters, records error
//   - Whitespace/comments: Skipped automatically, no tokens produced for them
//   - Identifiers "true"/"false": Returned as BOOL type, not IDENT
//   - Identifier "unset": Returned as UNSET type, not IDENT
//
// Notes:
//   - Primary method for parsers to consume lexer output
//   - Errors are collected internally and accessible via Errors()
func (l *Lexer) NextToken() Token {
	var tok Token
	tok.Pos = l.scanner.Position // Record current position for token error reporting

	switch l.ch { // Dispatch on current character to determine token type
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
		l.next()         // Advance past the '+' character
		if l.ch == '=' { // Check for += compound assignment
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
	case scanner.Comment: // Skip comments entirely from token stream
		// Comments are filtered out, no tokens produced for them
		l.next()             // Advance past the comment token
		return l.NextToken() // Recursively get next non-comment token
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
	case '\n', '\t', ' ', '\r': // Skip whitespace characters
		// Whitespace is treated as token separators
		l.next()             // Advance past whitespace
		return l.NextToken() // Recursively get next non-whitespace token
	default:
		// Unknown character - record error but continue processing
		if l.ch < 0 { // Negative character code indicates EOF
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
// Used for error reporting to pinpoint exact location of errors in source files.
// Position includes filename, line number, and column number.
//
// Parameters: None
//
// Returns:
//   - scanner.Position: Current position with file, line, column
//
// Edge cases:
//   - Position is valid even after EOF (returns last scanned position)
//
// Notes:
//   - This is the same position that would be used for the next token
func (l *Lexer) Position() scanner.Position {
	return l.scanner.Position
}

// Error creates an error with current source position information.
// Helper for generating lexer errors with file location for accurate reporting.
//
// Parameters:
//   - format: Printf-style format string
//   - args: Arguments for the format string
//
// Returns:
//   - error: Formatted as "filename:line:column: message"
//
// Edge cases:
//   - Position is the current scanner position at the time of the call
//
// Notes:
//   - This is a convenience method for creating position-aware errors
func (l *Lexer) Error(format string, args ...interface{}) error {
	return fmt.Errorf("%s: %s", l.scanner.Position, fmt.Sprintf(format, args...))
}

// Errors returns lexer diagnostics collected during scanning.
// Errors include invalid characters, malformed tokens, and scanner-reported issues.
// Collected separately so the parser can decide whether to continue or abort.
//
// Parameters: None
//
// Returns:
//   - []error: List of lexer errors; empty if no errors encountered
//
// Edge cases:
//   - Errors are non-fatal; scanning continues after errors to collect multiple issues
//
// Notes:
//   - Errors are not cleared automatically; callers should check after full parse
func (l *Lexer) Errors() []error {
	return l.errors
}

// Unquote removes quotes from a string literal.
// Wrapper around strconv.Unquote supporting Go string syntax,
// including escape sequences like \n, \t, \\, \", etc.
//
// Supported formats:
//   - Double-quoted: "hello" (supports escape sequences)
//   - Single-quoted: 'hello' (supports escape sequences)
//   - Raw: `hello` (no escape processing)
//
// Parameters:
//   - s: String literal including surrounding quotes
//
// Returns:
//   - string: Unquoted content; empty string on error
//   - error: nil on success; error for invalid escapes, unterminated strings, etc.
//
// Edge cases:
//   - Unterminated strings return an error
//   - Invalid escape sequences return an error
//   - Raw strings do not process any escape sequences
//
// Notes:
//   - This is a convenience wrapper around the standard strconv.Unquote
//
// Example:
//
//	Unquote(`"hello\nworld"`) -> "hello\nworld", nil
//	Unquote("'unterminated") -> "", error
func Unquote(s string) (string, error) {
	return strconv.Unquote(s)
}

// String returns a human-readable representation of a TokenType.
// Useful for debugging, error messages, and logging.
// Converts internal token type constants to descriptive strings.
//
// Parameters: None
//
// Returns:
//   - string: Token type name (e.g., "EOF", "IDENT") or "Token(N)" for unknown types
//
// Edge cases:
//   - Unknown TokenType values return "Token(N)" where N is the numeric value
//
// Notes:
//   - Satisfies the fmt.Stringer interface
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

// Error returns a formatted error string with position and message.
// Format is "filename:line:column: message" for easy parsing by editors.
//
// Parameters: None
//
// Returns:
//   - string: Formatted error string with position and message
//
// Edge cases:
//   - Empty message is allowed, results in "filename:line:column: "
//
// Notes:
//   - Satisfies the standard error interface
func (e *TokenError) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Message)
}

// NewTokenError creates a new token error with the given position and message.
// Convenience constructor for TokenError that implements the error interface.
//
// Parameters:
//   - pos: Source position where the error occurred
//   - msg: Error message describing the issue
//
// Returns:
//   - error: TokenError instance with specified position and message
//
// Edge cases:
//   - Empty message is allowed
//   - Position can be zero-valued if unknown
//
// Notes:
//   - The returned error is of type *TokenError, so callers can type-assert if needed
func NewTokenError(pos scanner.Position, msg string) error {
	return &TokenError{Pos: pos, Message: msg}
}
