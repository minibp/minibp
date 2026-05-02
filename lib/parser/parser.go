// Package parser provides lexical analysis and parsing for Blueprint build definitions.
//
// This package implements the second stage of the Blueprint build system:
// it takes a stream of tokens from the lexer and produces an Abstract Syntax Tree (AST).
// The parser uses a recursive descent approach, building parse trees top-down
// by following the grammar rules defined for Blueprint source files.
//
// The parser handles:
//   - Module definitions: cc_binary { ... }, cc_library { ... }, etc.
//   - Variable assignments: my_var = "value", my_list += ["item"]
//   - Expressions: strings, integers, booleans, lists, maps
//   - select() conditional expressions for architecture-specific values
//   - Property overrides: arch: {...}, host: {...}, target: {...}, multilib: {...}
//
// Grammar overview:
//
//	File        -> Definition*
//	Definition  -> Module | Assignment
//	Module      -> IDENT LBRACE PropertyList RBRACE
//	Assignment  -> IDENT (ASSIGN | PLUSEQ) Expression
//	Expression  -> Primary (PLUS Primary)*
//	Primary     -> STRING | INT | BOOL | LIST | MAP | IDENT | select()
//
// Error handling:
//
//	Parse errors are collected and aggregated rather than failing immediately.
//	This allows users to fix multiple issues in a single pass.
//	The parser uses error recovery to skip to the next definition after an error.
//
// Example usage:
//
//	r := strings.NewReader("cc_binary { name: \"myapp\" }")
//	file, errs := ParseFile(r, "Android.bp")
//	if len(errs) > 0 {
//	    // Handle parse errors
//	}
//
// The parser is the second stage in the Blueprint pipeline, consuming tokens
// from the lexer and producing AST nodes that represent the syntactic structure
// of the Blueprint source code.
package parser

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/scanner"

	"minibp/lib/errors"
)

// Parser parses Blueprint files.
// It uses a recursive descent parsing approach, consuming tokens from the lexer
// and building an AST (Abstract Syntax Tree) representation of the Blueprint code.
//
// The parser handles:
//   - Modules: type { property_list }
//   - Assignments: name = value or name += value
//   - Expressions: literals, variables, operators, select()
//   - Property overrides: arch:, host:, target:, multilib:, override:
//
// The parser maintains lookahead tokens (curToken and peekToken) to enable
// grammar decisions that require looking ahead more than one token.
// It also provides error recovery by skipping to the next definition when
// a parse error occurs, allowing multiple errors to be reported in a single pass.
//
// Token management:
//   - curToken: The current token being processed
//   - peekToken: The next token (lookahead) for grammar look-ahead
//   - nextToken(): Advances both curToken and peekToken forward
//
// Error handling:
//   - Errors are collected in the errors slice rather than failing immediately
//   - skipToNextDefinition() provides error recovery
//   - All errors are returned with source position information
type Parser struct {
	lexer     *Lexer  // The lexer used to tokenize the input
	curToken  Token   // The current token being processed
	peekToken Token   // The next token (lookahead) for grammar look-ahead
	fileName  string  // Name of the file being parsed (for error reporting)
	source    string  // Source content for error line display
	errors    []error // List of parsing errors encountered
}

// NewParser creates a new Parser from an io.Reader.
// It initializes the parser with a new lexer for the given input source
// and advances past the first two tokens to set up curToken and peekToken.
//
// This two-token initialization is required because the recursive descent
// parser often needs to look ahead one token to make grammar decisions.
// For example, when parsing an identifier, the parser needs to know
// whether the next token is LBRACE (module) or ASSIGN (assignment).
//
// Setup process:
//  1. Create lexer with the input reader and filename
//  2. Call nextToken() twice to fill curToken and peekToken
//  3. Parser is now ready to parse
//
// Parameters:
//   - r: The input io.Reader containing Blueprint source code
//   - fileName: The name of the file being parsed (for error messages)
//
// Returns:
//   - A new Parser instance ready to parse the input
//
// Edge cases:
//   - If source variadic is provided, the first element is used as source text for error reporting.
//   - If source is not provided, line content in error messages will be empty.
//   - The lexer is initialized with the given reader and filename.
//
// Note:
//   - The parser does not take ownership of the reader; the caller is responsible for closing it.
func NewParser(r io.Reader, fileName string, source ...string) *Parser {
	src := ""
	if len(source) > 0 { // Use provided source text for error line display
		src = source[0]
	}
	p := &Parser{
		lexer:    NewLexer(r, fileName),
		fileName: fileName,
		source:   src,
		errors:   []error{},
	}
	// Initialize curToken and peekToken by advancing twice
	// This sets up the initial state for the recursive descent parser
	p.nextToken() // Advance to first token (fill curToken)
	p.nextToken() // Advance to second token (fill peekToken)
	return p
}

// nextToken advances the parser to the next token in the input stream.
// It performs a "shift" operation: curToken becomes the previous token,
// peekToken becomes the current token, and a new peekToken is fetched
// from the lexer.
//
// This is the fundamental token advancement mechanism for the recursive
// descent parser. Each call to nextToken() consumes one token and makes
// the next token available for inspection via peekToken.
//
// Token flow:
//
//	Before: curToken=A, peekToken=B, lexer.position=C
//	After:  curToken=B, peekToken=C, lexer.position=D
//
// Parameters: None
//
// Returns: None
//
// Edge cases:
//   - If the lexer has no more tokens, peekToken will be set to EOF.
//   - Repeated calls after EOF will keep curToken and peekToken as EOF.
//
// Note:
//   - All parsing functions use this method to consume tokens; no other token advancement should be used.
func (p *Parser) nextToken() {
	p.curToken = p.peekToken // Shift current token to previous peek token
	p.peekToken = p.lexer.NextToken() // Fetch new peek token from lexer
}

// expect checks if the current token matches the expected type.
// If it matches, it consumes the token (via nextToken()) and returns the matched token.
// If it doesn't match, it returns an error with the position and expected vs actual token types.
//
// This method is used when the grammar requires a specific token type.
// For example, after parsing a property name, the parser expects a COLON token.
//
// Error message includes:
//   - Source position of the current token
//   - Expected token type
//   - Actual token type found
//
// Parameters:
//   - t: The expected TokenType (e.g., LBRACE, ASSIGN, COLON)
//
// Returns:
//   - Token: The matched token if successful
//   - error: nil if successful, otherwise an error describing the mismatch
//
// Edge cases:
//   - If the current token is EOF, returns an error with position (0,0) if not set.
//   - The error includes the file name, line, and column of the current token.
//
// Note:
//   - If the token matches, it is consumed (nextToken() is called) before returning.
func (p *Parser) expect(t TokenType) (Token, error) {
	if p.curToken.Type == t { // Current token matches expected type
		tok := p.curToken
		p.nextToken() // Consume the matched token
		return tok, nil
	}
	err := errors.Syntax(fmt.Sprintf("expected %s, got %s", t, p.curToken.Type)).
		WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column)
	return Token{}, err
}

// expectPeek checks if the peek token (lookahead) matches the expected type.
// Unlike expect(), this checks the lookahead token without consuming it.
// The token is consumed only if it matches.
//
// This is used when we need to look ahead to make a parsing decision,
// but don't want to commit to consuming the token yet.
// For example, to decide if something is a module vs assignment,
// we need to look at peekToken without consuming it.
//
// Parameters:
//   - t: The expected TokenType for the peek token
//
// Returns:
//   - bool: true if the peek token matched and was consumed, false otherwise
//
// Edge cases:
//   - If peekToken is EOF, returns false.
//   - If peekToken matches, the token is consumed, advancing curToken and peekToken.
//
// Note:
//   - Unlike expect(), this does not consume the token unless it matches.
func (p *Parser) expectPeek(t TokenType) bool {
	if p.peekToken.Type == t { // Peek token matches expected type
		p.nextToken() // Consume the peek token
		return true
	}
	return false
}

// Parse parses the entire input and returns a File AST node.
//
// It repeatedly parses definitions until the end of file is reached.
// After parsing all definitions, it collects any lexer errors to ensure
// all issues are reported to the caller in a single pass.
//
// Parse flow:
//  1. Create an empty File node with the filename
//  2. Loop: parseDefinition() until EOF token
//  3. On error: collect error and call skipToNextDefinition() for error recovery
//  4. On success: append definition to file.Defs
//  5. After loop: collect lexer errors if no parser errors occurred
//  6. Return File and errors
//
// Error handling:
//   - Parse errors are collected rather than failing immediately.
//     This allows users to see all syntax errors at once rather than fixing one at a time.
//   - Error recovery via skipToNextDefinition() continues parsing after errors.
//     The parser skips forward to the next IDENT token (potential definition start).
//   - Lexer errors are included in the final error list only if there are no parser errors.
//     This avoids duplicate error reporting for the same issue.
//
// Parameters:
//   - None (operates on the parser's internal state set during NewParser).
//     The parser must be initialized with NewParser() before calling Parse().
//
// Returns:
//   - *File: The parsed AST representation of the Blueprint file.
//     May contain partially parsed definitions if errors were encountered.
//     The File.Defs slice contains all successfully parsed definitions.
//   - []error: A list of errors encountered during parsing.
//     Empty slice if parsing succeeded with no errors.
//     Errors include source position information for error reporting.
//
// Edge cases:
//   - Empty input (no tokens except EOF) returns an empty File with no definitions.
//   - Input with only comments returns an empty File (comments are skipped by lexer).
//   - Parse errors don't stop the parser; it continues to find more errors.
//   - If skipToNextDefinition() can't find a recovery point, parsing stops at EOF.
//   - Lexer errors are only reported when there are no parser errors
//     (to avoid cascading error messages for the same issue).
//
// Note:
//   - The returned File may contain partially parsed definitions if errors occurred.
func (p *Parser) Parse() (*File, []error) {
	file := &File{Name: p.fileName}

	// Parse definitions until EOF
	// Each definition is either a module or an assignment
	for p.curToken.Type != EOF { // Parse until end of file
		def, err := p.parseDefinition()
		if err != nil { // Collect error and perform recovery
			p.errors = append(p.errors, err)
			p.skipToNextDefinition()
		} else if def != nil { // Successfully parsed definition
			file.Defs = append(file.Defs, def)
		}
	}

	// Include lexer errors in the final error list
	// This captures issues like invalid characters detected during scanning
	if len(p.errors) == 0 { // Only add lexer errors if no parser errors occurred
		p.errors = append(p.errors, p.lexer.Errors()...)
	}

	return file, p.errors
}

// skipToNextDefinition skips tokens until we reach a potential start of a definition.
// This is used for error recovery - when a parse error occurs, we skip forward
// to try to continue parsing subsequent definitions rather than stopping entirely.
//
// It skips tokens until it finds an IDENT token (which could be a module type
// or variable name) or reaches EOF. This allows the parser to
// recover from syntax errors and continue processing the rest of the file.
//
// Error recovery strategy:
//   - After a parse error, the parser's current token might be in the middle of
//     an incomplete definition (e.g., after a missing comma or bracket).
//   - By skipping to the next IDENT token, we attempt to find the start of
//     the next definition, allowing the parser to continue with minimal loss.
//   - This design enables reporting multiple errors in a single pass, which
//     is more user-friendly than failing on the first error.
//
// Example error recovery:
//
//	my_module { srcs: ["file.c", }
//	         ^ parse error here (missing quote or bracket)
//	another_module { }  <- skipToNextDefinition skips to here
//
// Edge cases:
//   - If already at EOF, the function returns immediately (no tokens to skip).
//   - If at an IDENT token when called, the function returns without skipping
//     (the IDENT is the start of the next definition).
//   - If no IDENT token exists before EOF, the function stops at EOF.
//   - The function does not consume the IDENT token it finds; it leaves
//     the parser positioned at that token for the next parse attempt.
//
// Parameters: None
//
// Returns: None
//
// Note:
//   - This function only moves forward; it never backtracks or re-parses tokens.
func (p *Parser) skipToNextDefinition() {
	for p.curToken.Type != EOF && p.curToken.Type != IDENT { // Skip until EOF or IDENT (start of definition)
		p.nextToken()
	}
}

// parseDefinition parses either a module or an assignment definition.
//
// A definition starts with an identifier (module type or variable name).
// After the identifier, the parser looks at the next token to determine
// the definition type:
//   - If followed by LBRACE ({), it's a module definition
//   - If followed by ASSIGN (=) or PLUSEQ (+=), it's an assignment
//
// Grammar:
//
//	Definition -> IDENT (LBRACE Module | (ASSIGN | PLUSEQ) Assignment)
//
// Token flow:
//  1. Verify current token is IDENT (otherwise return error)
//  2. Record name and position for error reporting
//  3. Advance to next token
//  4. Check token to decide definition type (LBRACE vs ASSIGN/PLUSEQ)
//  5. Dispatch to parseModule() or parseAssignment()
//
// Parameters:
//   - None (operates on parser's current token state)
//     The parser must be positioned at an IDENT token when calling this method.
//
// Returns:
//   - Definition: A Module or Assignment AST node, or nil on error.
//     The returned interface will be either *Module or *Assignment.
//   - error: nil if successful, otherwise a parse error with position information.
//     Errors include the line content and caret position for display.
//
// Edge cases:
//   - If current token is not IDENT, returns error with suggestion to use identifier.
//   - If token after IDENT is not LBRACE, ASSIGN, or PLUSEQ, returns error.
//   - Module names and variable names share the same IDENT token type;
//     the distinction is made by the following token.
//
// Examples:
//
//	cc_binary { ... }     -> parseModule (IDENT + LBRACE)
//	my_var = "value"      -> parseAssignment (IDENT + ASSIGN)
//	my_list += ["item"]   -> parseAssignment (IDENT + PLUSEQ)
//
// Note:
//   - This function consumes the IDENT token before checking the next token.
func (p *Parser) parseDefinition() (Definition, error) {
	if p.curToken.Type != IDENT { // First token must be identifier (module type or variable name)
		return nil, errors.Syntax(fmt.Sprintf("expected identifier, got %s", p.curToken.Type)).
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithContent(p.lineContent(p.curToken.Pos.Line)).
			WithContentCaret(len(p.curToken.Literal)).
			WithSuggestion("Module or variable name must be an unquoted identifier")
	}

	// Record the name and its position for error reporting
	name := p.curToken.Literal
	namePos := p.curToken.Pos

	p.nextToken() // Consume the IDENT token

	// Decide what kind of definition based on the token after the name
	switch p.curToken.Type {
	case LBRACE:
		// Module definition: name { ... }
		// Examples: cc_binary { ... }, cc_library { ... }
		return p.parseModule(name, namePos)
	case ASSIGN, PLUSEQ:
		// Assignment: name = value or name += value
		// Examples: my_var = "value", my_list += ["item"]
		return p.parseAssignment(name, namePos)
	default:
		return nil, errors.Syntax(fmt.Sprintf("unexpected token %s after identifier '%s'", p.curToken.Type, name)).
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column)
	}
}

// parseModule parses a module definition: type { property_list }
//
// A module consists of a type name (like "cc_binary", "cc_library") followed by
// a block of properties enclosed in braces. The module type determines how the
// module will be built by the ninja generator.
//
// Special properties are extracted from the property list and stored separately:
//   - "arch": Architecture-specific overrides (arm, arm64, x86, x86_64)
//   - "host": Host-specific overrides (when building for the host machine)
//   - "target": Target-specific overrides (when building for the target device)
//   - "multilib": Multilib overrides (lib32, lib64 for 32/64-bit builds)
//   - "override": Override flag for replacing existing module definitions
//
// These special properties are removed from the main property list and stored
// in separate fields of the Module struct for variant matching during the
// build phase.
//
// Parameters:
//   - typeName: The module type name (e.g., "cc_binary", "cc_library", "go_binary").
//     This is the identifier that appeared before the opening brace.
//   - typePos: The source position of the type name for error reporting.
//
// Returns:
//   - *Module: The parsed module AST node with all properties organized.
//     The Module struct contains the type, main properties (Map), and
//     separate fields for arch/host/target/multilib/override properties.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - Empty module body ({}) is valid and produces a Module with no properties.
//   - Special properties (arch, host, target, multilib) must have map values;
//     otherwise, a syntax error is returned with a suggestion.
//   - The "override" property, if present, must be a boolean value.
//   - Multiple special properties of the same type are merged during parsing.
//   - Regular properties and special properties can be interleaved in any order.
//
// Examples:
//
//	cc_binary {                              // typeName = "cc_binary"
//	    name: "my_tool",                     // regular property
//	    srcs: ["main.c"],                    // regular property
//	    arch: { arm: { srcs: ["arm.c"] } }  // special property (extracted)
//	}
//
// Note:
//   - Special properties are removed from the main property list and stored separately.
func (p *Parser) parseModule(typeName string, typePos scanner.Position) (*Module, error) {
	// Current token is LBRACE - consume the opening brace
	lbracePos := p.curToken.Pos
	p.nextToken() // Consume opening brace

	// Parse the property list inside the braces
	propertyList, rbracePos, err := p.parsePropertyList()
	if err != nil {
		return nil, err
	}

	// Extract arch, host, target, and multilib overrides from properties.
	// These special properties are removed from the main property list
	// and stored separately for variant matching during build.
	archProps := make(map[string]*Map)
	var hostProps *Map
	var targetProps *Map
	multilibProps := make(map[string]*Map)
	var overrideFound bool
	var filteredProps []*Property

	// Process each property to extract special override properties
	for _, prop := range propertyList {
		switch prop.Name {
		case "arch":
			// Architecture-specific overrides: arch: { arm: {...}, arm64: {...} }
			archMap, ok := prop.Value.(*Map)
			if !ok {
				return nil, errors.Syntax("expected map value for 'arch' override").
					WithLocation(p.fileName, prop.ColonPos.Line, prop.ColonPos.Column).
					WithSuggestion("arch: requires map value like arch: { arm: {...} }")
			}
			for _, ap := range archMap.Properties {
				archInner, ok := ap.Value.(*Map)
				if !ok {
					return nil, errors.Syntax(fmt.Sprintf("expected map value for arch override '%s'", ap.Name)).
						WithLocation(p.fileName, ap.ColonPos.Line, ap.ColonPos.Column).
						WithSuggestion("Architecture variant requires map value")
				}
				archProps[ap.Name] = archInner
			}
		case "host":
			// Host-specific overrides: host: { ... }
			m, ok := prop.Value.(*Map)
			if !ok {
				return nil, errors.Syntax("expected map value for 'host' override").
					WithLocation(p.fileName, prop.ColonPos.Line, prop.ColonPos.Column).
					WithSuggestion("host: requires map value like host: { ... }")
			}
			hostProps = m
		case "target":
			// Target-specific overrides: target: { ... }
			m, ok := prop.Value.(*Map)
			if !ok {
				return nil, errors.Syntax("expected map value for 'target' override").
					WithLocation(p.fileName, prop.ColonPos.Line, prop.ColonPos.Column).
					WithSuggestion("target: requires map value like target: { ... }")
			}
			targetProps = m
		case "multilib":
			// Multilib overrides: multilib: { lib32: {...}, lib64: {...} }
			mlMap, ok := prop.Value.(*Map)
			if !ok {
				return nil, errors.Syntax("expected map value for 'multilib' override").
					WithLocation(p.fileName, prop.ColonPos.Line, prop.ColonPos.Column).
					WithSuggestion("multilib: requires map value like multilib: { lib32: {...} }")
			}
			for _, mp := range mlMap.Properties {
				mlInner, ok := mp.Value.(*Map)
				if !ok {
					return nil, errors.Syntax(fmt.Sprintf("expected map value for multilib override '%s'", mp.Name)).
						WithLocation(p.fileName, mp.ColonPos.Line, mp.ColonPos.Column).
						WithSuggestion("Multilib variant requires map value")
				}
				multilibProps[mp.Name] = mlInner
			}
		case "override":
			// Override flag: override: true
			if b, ok := prop.Value.(*Bool); ok {
				overrideFound = b.Value
			}
		default:
			// Regular property - keep in main property list
			filteredProps = append(filteredProps, prop)
		}
	}

	// Create the module with extracted properties
	mod := &Module{
		Type:     typeName,
		TypePos:  typePos,
		Map:      &Map{Properties: filteredProps, LBracePos: lbracePos, RBracePos: rbracePos},
		Arch:     archProps,
		Host:     hostProps,
		Target:   targetProps,
		Multilib: multilibProps,
		Override: overrideFound,
	}

	return mod, nil
}

// parsePropertyList parses a list of properties inside braces: { property [, property] }
//
// Properties are key-value pairs separated by commas. Trailing commas are allowed
// (e.g., "name: "a", srcs: ["b"]," is valid).
// The parser reads properties until it encounters a closing brace (}) or EOF.
//
// Grammar:
//
//	PropertyList -> [ Property (COMMA Property)* [COMMA] ] RBRACE
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned after the opening LBRACE when calling this method.
//
// Returns:
//   - []*Property: List of parsed properties in order of appearance.
//     Empty slice if no properties are found before the closing brace.
//   - scanner.Position: Position of the closing right brace (}).
//     Valid only if parsing succeeded (no error returned).
//   - error: nil if successful, otherwise a parse error.
//     Errors include position information and suggestions for fixing syntax.
//
// Edge cases:
//   - Empty property list ({}) is valid and returns an empty slice.
//   - Trailing commas before the closing brace are allowed.
//   - If neither comma nor closing brace is found after a property,
//     returns an error pointing to the last property parsed.
//   - If closing brace is missing at EOF, returns an error with suggestion.
//   - Properties must be separated by commas; missing commas produce an error.
//
// Key design decisions:
//   - Error position points to the last property when comma/brace is missing,
//     helping users identify where the syntax error occurred.
//   - The function does not consume the token after RBRACE;
//     the caller is responsible for advancing past it if needed.
func (p *Parser) parsePropertyList() ([]*Property, scanner.Position, error) {
	properties := []*Property{}
	var rbracePos scanner.Position
	var lastProp *Property

	// Parse properties until we hit the closing brace or EOF
	for p.curToken.Type != EOF && p.curToken.Type != RBRACE {
		prop, err := p.parseProperty()
		if err != nil {
			return nil, rbracePos, err
		}
		if prop != nil {
			properties = append(properties, prop)
			lastProp = prop
		}

		// Check if we've reached the closing brace
		if p.curToken.Type == RBRACE {
			break
		}

		// Comma separates adjacent properties; trailing commas are still allowed.
		if p.curToken.Type == COMMA {
			p.nextToken()
			continue
		}

		// Error if neither comma nor closing brace - point to last property
		var errPos scanner.Position
		errContent := p.lineContent(p.curToken.Pos.Line)
		caretLen := 0
		if lastProp != nil {
			errPos = lastProp.NamePos
			errContent = p.lineContent(lastProp.NamePos.Line)
			caretLen = len(lastProp.Name)
		} else {
			errPos = p.curToken.Pos
		}
		return nil, rbracePos, errors.Syntax("expected ',' or '}' after property").
			WithLocation(p.fileName, errPos.Line, errPos.Column).
			WithContent(errContent).
			WithContentCaret(caretLen).
			WithSuggestion("Properties must be separated by commas")
	}

	// Verify we found the closing brace
	if p.curToken.Type != RBRACE {
		return nil, rbracePos, errors.Syntax("expected }").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithContent(p.lineContent(p.curToken.Pos.Line)).
			WithContentCaret(1).
			WithSuggestion("Module block should end with '}'")
	}
	rbracePos = p.curToken.Pos
	p.nextToken()

	return properties, rbracePos, nil
}

// parseProperty parses a single property: name : expression
//
// A property consists of an identifier (the property name), followed by a colon,
// followed by an expression (the property value). Properties appear inside
// module definitions and map literals.
//
// Property names must be valid identifiers (not quoted strings).
// The expression can be any valid expression type: string, integer, boolean,
// list, map, variable reference, select statement, or exec_script call.
//
// Grammar:
//
//	Property -> IDENT COLON Expression
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at an IDENT token when calling this method.
//
// Returns:
//   - *Property: The parsed property AST node.
//     Contains the name, name position, value expression, and colon position.
//   - error: nil if successful, otherwise a parse error with position information.
//     Errors include line content and caret position for display.
//
// Edge cases:
//   - Property name must be an IDENT token; quoted strings are not valid property names.
//   - Missing colon after property name produces an error with suggestion.
//   - The value expression can be any valid expression type.
//   - Empty values (just colon) are not valid; parseExpression will fail.
//
// Examples:
//
//	name: "my_module"        -> string value
//	srcs: ["a.c", "b.c"]    -> list value
//	flags: "-Wall" + custom  -> expression with operator
//	arch: { arm: {...} }     -> map value
func (p *Parser) parseProperty() (*Property, error) {
	if p.curToken.Type != IDENT {
		return nil, errors.Syntax(fmt.Sprintf("expected property name (identifier), got %s", p.curToken.Type)).
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithContent(p.lineContent(p.curToken.Pos.Line)).
			WithContentCaret(len(p.curToken.Literal)).
			WithSuggestion("Property names must be identifiers (unquoted names like name, srcs)")
	}

	name := p.curToken.Literal
	namePos := p.curToken.Pos

	p.nextToken()

	// Verify colon separator
	if p.curToken.Type != COLON {
		return nil, errors.Syntax(fmt.Sprintf("expected ':' after property name '%s'", name)).
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithContent(p.lineContent(p.curToken.Pos.Line)).
			WithContentCaret(1).
			WithSuggestion("Property name must be followed by ':'")
	}
	colonPos := p.curToken.Pos
	p.nextToken()

	// Parse the property value expression
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	return &Property{
		Name:     name,
		NamePos:  namePos,
		Value:    expr,
		ColonPos: colonPos,
	}, nil
}

// parseAssignment parses an assignment statement: name (= | +=) expression
//
// Assignments define variables that can be referenced later using ${var} syntax.
// There are two types of assignment operators:
//   - "=" (ASSIGN): Simple assignment, sets the variable to the expression value
//   - "+=" (PLUSEQ): Concatenative assignment, appends to existing value
//
// For += assignments, the evaluator handles concatenation differently based on type:
//   - String += String: Appends the right string to the left string
//   - List += List: Appends elements of right list to left list
//   - List += Non-list: Appends the single element to the list
//   - Other combinations may cause evaluation errors
//
// Grammar:
//
//	Assignment -> IDENT (ASSIGN | PLUSEQ) Expression
//
// Parameters:
//   - name: The variable name being assigned to (already parsed by parseDefinition).
//   - namePos: The source position of the variable name for error reporting.
//
// Returns:
//   - *Assignment: The parsed assignment AST node.
//     Contains the name, name position, assignment operator, equals position, and value.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - The operator must be either = or +=; other tokens produce an error.
//   - The value expression can be any valid expression type.
//   - Empty values (just operator, no expression) are caught by parseExpression.
//   - Variable names are case-sensitive identifiers.
//
// Examples:
//
//	my_var = "hello"                  -> simple string assignment
//	my_list = ["a", "b"]             -> list assignment
//	my_list += ["c"]                  -> list concatenation
//	flags = "-Wall" + extra_flags     -> expression assignment
func (p *Parser) parseAssignment(name string, namePos scanner.Position) (*Assignment, error) {
	assigner := "="
	equalsPos := p.curToken.Pos

	if p.curToken.Type == PLUSEQ {
		assigner = "+="
	} else if p.curToken.Type != ASSIGN {
		return nil, errors.Syntax(fmt.Sprintf("expected '=' or '+=', got %s", p.curToken.Type)).
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithContent(p.lineContent(p.curToken.Pos.Line)).
			WithContentCaret(len(p.curToken.Literal)).
			WithSuggestion("Assignment operator should be '=' or '+='")
	}
	p.nextToken()

	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	return &Assignment{
		Name:      name,
		NamePos:   namePos,
		EqualsPos: equalsPos,
		Assigner:  assigner,
		Value:     expr,
	}, nil
}

// parseExpression parses an expression, handling the + operator for concatenation/addition.
//
// This is the entry point for parsing all expression types. It first parses
// a primary expression (left side), then checks for + operators to handle
// binary operations with left-to-right associativity.
//
// The + operator can perform different operations depending on the types:
//   - Integer + Integer: Arithmetic addition (int64 + int64)
//   - String + String: String concatenation (string + string)
//   - List + List: List concatenation (list + list)
//   - Other combinations: May cause evaluation errors later
//
// Examples:
//
//	"hello" + "world"      -> string concatenation
//	1 + 2                  -> integer addition (3)
//	["a"] + ["b"]          -> list concatenation (["a", "b"])
//	a + b + c              -> parsed as "(a + b) + c" (left-to-right)
//
// Grammar:
//
//	Expression -> Primary (PLUS Primary)*
//
// Parameters:
//   - None (operates on parser's current token state).
//
// Returns:
//   - Expression: The parsed expression AST node.
//     Can be a simple expression (string, int, etc.) or an Operator node
//     if + operators were present.
//   - error: nil if successful, otherwise a parse error.
//
// Edge cases:
//   - A single primary expression without + operator returns just that expression.
//   - Multiple + operators are left-associative: "a + b + c" = "(a + b) + c".
//   - The + operator must be followed by a valid primary expression;
//     otherwise, parsePrimary() will return an error.
//   - Type checking of operands is done during evaluation, not parsing.
func (p *Parser) parseExpression() (Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	// Handle + operator for concatenation/addition
	// Uses left-to-right associativity
	for p.curToken.Type == PLUS {
		opPos := p.curToken.Pos
		p.nextToken()

		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}

		// Create binary operator node
		left = &Operator{
			Args:        [2]Expression{left, right},
			Operator:    '+',
			OperatorPos: opPos,
		}
	}

	return left, nil
}

// parsePrimary parses a single primary expression (the base unit, no operators).
//
// Primary expressions are the building blocks of all expressions.
// They cannot be broken down further by the expression parser.
//
// Token types and their corresponding parse functions:
//   - STRING: Quoted string literals -> parseString()
//   - INT: Integer literals -> parseInt()
//   - BOOL: Boolean literals (true/false) -> parseBool()
//   - LBRACKET: List expressions [expr, ...] -> parseList()
//   - LBRACE: Map expressions { prop: value, ... } -> parseMap()
//   - IDENT: Either "select" keyword, "exec_script" keyword, or variable reference
//   - UNSET: Unset keyword for removing property values -> returns Unset node
//
// Grammar:
//
//	Primary -> STRING | INT | BOOL | LBRACKET List | LBRACE Map | IDENT | UNSET
//
// Parameters:
//   - None (operates on parser's current token state).
//
// Returns:
//   - Expression: The parsed primary expression AST node.
//     The actual type depends on the token: *String, *Int64, *Bool, *List,
//     *Map, *Variable, *Select, *ExecScript, or *Unset.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - For IDENT tokens, special handling for "select" and "exec_script" keywords.
//   - Unknown IDENT tokens are treated as variable references (resolved during evaluation).
//   - The UNSET keyword creates an Unset node (used to clear property values).
//   - Unexpected token types produce an error with suggestion for valid expressions.
//
// Examples:
//
//	"hello"           -> *String
//	42                -> *Int64
//	true              -> *Bool
//	["a", "b"]        -> *List
//	{key: "value"}    -> *Map
//	my_var            -> *Variable
//	select(...)       -> *Select
//	exec_script(...)  -> *ExecScript
//	unset             -> *Unset
func (p *Parser) parsePrimary() (Expression, error) {
	switch p.curToken.Type {
	case STRING:
		return p.parseString()
	case INT:
		return p.parseInt()
	case BOOL:
		return p.parseBool()
	case LBRACKET:
		return p.parseList()
	case LBRACE:
		return p.parseMap()
	case IDENT:
		// Check for select() or exec_script() keyword vs variable reference
		if p.curToken.Literal == "select" {
			return p.parseSelect()
		}
		if p.curToken.Literal == "exec_script" {
			return p.parseExecScript()
		}
		return p.parseVariable()
	case UNSET:
		// Unset keyword for removing property values
		pos := p.curToken.Pos
		p.nextToken()
		return &Unset{KeywordPos: pos}, nil
	default:
		return nil, errors.Syntax(fmt.Sprintf("unexpected token %s in expression", p.curToken.Type)).
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithContent(p.lineContent(p.curToken.Pos.Line)).
			WithContentCaret(len(p.curToken.Literal)).
			WithSuggestion("Expression value expected (string, list, or map)")
	}
}

// parseString parses a string literal token.
//
// String literals are surrounded by quotes and may contain escape sequences.
// The parser removes the quotes and processes escape sequences using strconv.Unquote.
// Both single-quoted ('...') and double-quoted ("...") strings are supported,
// as well as raw strings (if supported by the lexer).
//
// Grammar:
//
//	String -> STRING
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at a STRING token when calling this method.
//
// Returns:
//   - *String: The parsed string AST node.
//     Contains the unquoted string value and the position of the literal.
//   - error: nil if successful, otherwise a parse error.
//     Errors can occur if the string literal is malformed (e.g., unterminated quotes).
//
// Edge cases:
//   - Escape sequences (e.g., \n, \t, \") are processed by strconv.Unquote.
//   - Empty strings ("") are valid and produce a String with empty value.
//   - Malformed escape sequences cause an error with position information.
//   - The token is consumed (nextToken called) after parsing.
//
// Examples:
//
//	"hello"       -> String{Value: "hello"}
//	'world'       -> String{Value: "world"} (if single quotes supported)
//	"hello\n"     -> String{Value: "hello\n"} (with newline)
func (p *Parser) parseString() (*String, error) {
	pos := p.curToken.Pos
	literal := p.curToken.Literal
	p.nextToken()

	// Remove quotes from literal and process escape sequences
	value, err := strconv.Unquote(literal)
	if err != nil {
		return nil, errors.Syntax(fmt.Sprintf("invalid string literal: %v", err)).
			WithLocation(p.fileName, pos.Line, pos.Column).
			WithContent(p.lineContent(pos.Line)).
			WithContentCaret(len(literal)).
			WithSuggestion("String literal must be properly quoted")
	}

	return &String{
		Value:      value,
		LiteralPos: pos,
	}, nil
}

// parseInt parses an integer literal token.
//
// Integer literals are base-10 numbers (optionally negative) that are parsed
// into int64 values. The parser uses strconv.ParseInt with base 10 and 64-bit width.
//
// Grammar:
//
//	Int -> INT
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at an INT token when calling this method.
//
// Returns:
//   - *Int64: The parsed integer AST node.
//     Contains the int64 value and the position of the literal.
//   - error: nil if successful, otherwise a parse error.
//     Errors can occur if the literal overflows int64 or has invalid format.
//
// Edge cases:
//   - Negative numbers are supported (lexer produces a single token for "-123").
//   - Leading zeros are allowed (e.g., "007" parses to 7).
//   - Overflow beyond int64 range causes a parse error.
//   - The token is consumed (nextToken called) after parsing.
//
// Examples:
//
//	42      -> Int64{Value: 42}
//	-1      -> Int64{Value: -1}
//	0       -> Int64{Value: 0}
//	9999999999999999999  -> error (overflow)
func (p *Parser) parseInt() (*Int64, error) {
	pos := p.curToken.Pos
	literal := p.curToken.Literal
	p.nextToken()

	value, err := strconv.ParseInt(literal, 10, 64)
	if err != nil {
		return nil, errors.Syntax(fmt.Sprintf("invalid integer literal: %v", err)).
			WithLocation(p.fileName, pos.Line, pos.Column).
			WithContent(p.lineContent(pos.Line)).
			WithContentCaret(len(literal)).
			WithSuggestion("Integer must be a valid number")
	}

	return &Int64{
		Value:      value,
		LiteralPos: pos,
	}, nil
}

// parseBool parses a boolean literal.
//
// Boolean literals are the keywords "true" and "false".
// They are case-sensitive and must be lowercase as written in the Blueprint source.
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at a BOOL token when calling this method.
//
// Returns:
//   - *Bool: The parsed boolean AST node containing the boolean value and position.
//     Returns true if the literal was "true", false if "false".
//   - error: This function never returns an error.
//     Both "true" and "false" are valid boolean literals.
//
// Edge cases:
//   - The BOOL token type is only created for the exact strings "true" or "false"
//     by the lexer, so no validation is needed here.
//   - The token is consumed (nextToken called) after reading the literal.
func (p *Parser) parseBool() (*Bool, error) {
	pos := p.curToken.Pos
	literal := p.curToken.Literal
	p.nextToken()

	return &Bool{
		Value:      literal == "true",
		LiteralPos: pos,
	}, nil
}

// parseVariable parses a variable reference.
//
// A variable reference is an identifier that refers to a previously defined variable
// or assignment. During evaluation (not during parsing), the variable's value will
// be substituted for the reference.
//
// Variable references appear in expressions and can be used in property values,
// select() conditions, and other expression contexts. They are resolved by the
// evaluator using the assignments collected from the parsed definitions.
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at an IDENT token that is not "select" or "exec_script".
//
// Returns:
//   - *Variable: The parsed variable reference AST node.
//     Contains the variable name and its source position.
//   - error: This function never returns an error.
//     Any IDENT token is a valid variable reference (validation happens during evaluation).
//
// Edge cases:
//   - The identifier "select" and "exec_script" are handled separately by parsePrimary()
//     and will not reach this function under normal flow.
//   - Variables are not validated during parsing; undefined variables are caught
//     during the evaluation phase (when processing assignments and module properties).
//   - Variable names are case-sensitive and follow the same rules as identifiers
//     in the lexer (letters, digits, underscore; must start with letter or underscore).
func (p *Parser) parseVariable() (*Variable, error) {
	pos := p.curToken.Pos
	name := p.curToken.Literal
	p.nextToken()

	return &Variable{
		Name:    name,
		NamePos: pos,
	}, nil
}

// parseList parses a list literal: [ expression [, expression] ]
//
// Lists are ordered collections of expressions, separated by commas.
// Trailing commas are allowed (e.g., ["a", "b",] is valid).
// List elements can be any valid expression: strings, integers, booleans,
// other lists, maps, variables, or select() statements.
//
// Grammar:
//
//	List -> LBRACKET [ Expression (COMMA Expression)* [COMMA] ] RBRACKET
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at a LBRACKET token when calling this method.
//
// Returns:
//   - *List: The parsed list AST node containing all element expressions.
//     The LBracePos and RBracePos fields store the positions of '[' and ']'.
//   - error: nil if successful, otherwise a parse error.
//     Errors include position information and suggestions for fixing syntax.
//
// Edge cases:
//   - Empty list ([]) is valid and returns a List with empty Values slice.
//   - Trailing commas before the closing bracket are allowed.
//   - Single-element lists don't require a trailing comma.
//   - Nested lists are supported (lists can contain other lists as elements).
//   - If the closing bracket is missing, returns an error with suggestion.
//   - If neither comma nor closing bracket is found after an element,
//     returns an error indicating the syntax issue.
//
// Examples:
//
//	[]                  -> empty list
//	["a"]               -> single element
//	["a", "b"]          -> multiple elements
//	["a", "b",]         -> trailing comma allowed
//	[1, [2, 3], "c"]   -> nested lists supported
func (p *Parser) parseList() (*List, error) {
	lbracePos := p.curToken.Pos
	p.nextToken()

	values := []Expression{}
	var rbracePos scanner.Position

	// Parse list elements until closing bracket
	for p.curToken.Type != EOF && p.curToken.Type != RBRACKET {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		values = append(values, expr)

		// Check for closing bracket
		if p.curToken.Type == RBRACKET {
			break
		}

		// Comma separates adjacent elements; trailing commas are still allowed.
		if p.curToken.Type == COMMA {
			p.nextToken()
			continue
		}

		return nil, errors.Syntax("expected ',' or ']' after list element").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithContent(p.lineContent(p.curToken.Pos.Line)).
			WithContentCaret(len(p.curToken.Literal)).
			WithSuggestion("List elements must be separated by commas")
	}

	// Verify closing bracket
	if p.curToken.Type != RBRACKET {
		return nil, errors.Syntax("expected ]").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithContent(p.lineContent(p.curToken.Pos.Line)).
			WithContentCaret(1).
			WithSuggestion("List should end with ']'")
	}
	rbracePos = p.curToken.Pos
	p.nextToken()

	return &List{
		Values:    values,
		LBracePos: lbracePos,
		RBracePos: rbracePos,
	}, nil
}

// parseMap parses a map literal: { property_list }
//
// Maps are collections of key-value pairs enclosed in braces.
// They share the same syntax as property lists inside module definitions,
// so the internal parsePropertyList() function is reused.
//
// Maps can be used as values in assignments or properties:
//
//	my_map = { key1: "value1", key2: "value2" }
//
// Grammar:
//
//	Map -> LBRACE PropertyList RBRACE
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at a LBRACE token when calling this method.
//
// Returns:
//   - *Map: The parsed map AST node containing the property list.
//     The LBracePos and RBracePos fields store the positions of '{' and '}'.
//   - error: nil if successful, otherwise a parse error.
//
// Edge cases:
//   - Empty map ({}) is valid and returns a Map with empty Properties slice.
//   - Trailing commas before the closing brace are allowed.
//   - Map keys must be identifiers (not quoted strings).
//   - Map values can be any valid expression.
//   - Duplicate keys in the same map are allowed at parse time;
//     handling duplicates is done during evaluation.
//
// Key design decisions:
//   - Reusing parsePropertyList() avoids duplicating the property parsing logic.
//     This works because maps and module property lists have identical syntax.
//   - Maps are represented as *Map with Properties slice, same as the internal
//     structure of a Module, allowing code reuse in the evaluator.
func (p *Parser) parseMap() (*Map, error) {
	lbracePos := p.curToken.Pos
	p.nextToken()

	propertyList, rbracePos, err := p.parsePropertyList()
	if err != nil {
		return nil, err
	}

	return &Map{
		Properties: propertyList,
		LBracePos:  lbracePos,
		RBracePos:  rbracePos,
	}, nil
}

// parseSelect parses a select() conditional expression.
//
// Select is a powerful conditional expression that chooses values based on
// configuration conditions. It evaluates condition functions (like arch(), os())
// and matches the result against case patterns to determine the value.
//
// Syntax:
//
//	select(condition, { pattern1: value1, pattern2: value2, ..., default: value })
//
// The first argument is a condition or tuple of conditions.
// The second argument is a map of patterns to values. The "default" pattern
// is used when no other pattern matches.
//
// Select supports several advanced features:
//   - Tuple conditions: select((arch(), os()), { ... }) for multi-condition matching
//   - Unset patterns: select(arch(), { unset: value }) matches when config is not set
//   - Any patterns: select(arch(), { any: value }) matches any value
//   - Any @var binding: select(arch(), { any @var: value }) binds matched value to variable
//
// Example usage:
//
//	srcs: select(arch(), {
//	    arm: ["arm.c"],
//	    arm64: ["arm64.c"],
//	    default: ["common.c"],
//	})
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at a "select" IDENT token when calling this method.
//
// Returns:
//   - *Select: The parsed select AST node.
//     Contains keyword position, conditions array, brace positions, and cases.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - Missing parentheses after "select" produces an error.
//   - Tuple conditions are enclosed in double parentheses: select((a(), b()), {...})
//   - The "default" pattern is a special case that matches when nothing else does.
//   - The "unset" pattern matches when the condition evaluates to nil or empty.
//   - The "any" pattern matches any value; with @var it binds the value to a variable.
//
// Key design decisions:
//   - Conditions are parsed first, then cases, allowing the parser to know
//     if it's a tuple select (affecting how cases are parsed).
//   - Case patterns can be simple (one pattern) or tuple (multiple patterns)
//     depending on whether the select has multiple conditions.
func (p *Parser) parseSelect() (*Select, error) {
	keywordPos := p.curToken.Pos
	p.nextToken()

	// Expect opening parenthesis after select keyword
	if p.curToken.Type != LPAREN {
		return nil, errors.Syntax("expected '(' after 'select'").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("select() requires parentheses")
	}
	p.nextToken()

	conditions := []ConfigurableCondition{}

	// Check for tuple condition: select((arch(), os()), {...})
	// When conditions are enclosed in extra parens, multiple conditions are evaluated together
	if p.curToken.Type == LPAREN {
		p.nextToken()
		for p.curToken.Type != EOF && p.curToken.Type != RPAREN {
			cond, err := p.parseConfigurableCondition()
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, cond)
			if p.curToken.Type == COMMA {
				p.nextToken()
			}
		}
		if p.curToken.Type != RPAREN {
			return nil, errors.Syntax("expected ')' after tuple conditions").
				WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
				WithSuggestion("Tuple conditions must be closed with ')'")
		}
		p.nextToken()
	} else {
		// Single condition
		cond, err := p.parseConfigurableCondition()
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, cond)
	}

	// Expect comma between conditions and cases
	if p.curToken.Type == COMMA {
		p.nextToken()
	}

	// Parse cases: { case_pattern: value, ... }
	if p.curToken.Type != LBRACE {
		return nil, errors.Syntax("expected '{' for select cases").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("select() needs cases like { arch: value }")
	}
	lbracePos := p.curToken.Pos
	p.nextToken()

	// Parse each case in the select
	cases := []SelectCase{}
	for p.curToken.Type != EOF && p.curToken.Type != RBRACE {
		caseItem, err := p.parseSelectCase(len(conditions) > 1)
		if err != nil {
			return nil, err
		}
		cases = append(cases, caseItem)

		if p.curToken.Type == COMMA {
			p.nextToken()
		}
	}

	// Verify closing braces and parenthesis
	if p.curToken.Type != RBRACE {
		return nil, errors.Syntax("expected '}' after select cases").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("select() cases block should end with '}'")
	}
	rbracePos := p.curToken.Pos
	p.nextToken()

	if p.curToken.Type != RPAREN {
		return nil, errors.Syntax("expected ')' after select cases").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("select() should end with ')'")
	}
	p.nextToken()

	return &Select{
		KeywordPos: keywordPos,
		Conditions: conditions,
		LBracePos:  lbracePos,
		RBracePos:  rbracePos,
		Cases:      cases,
	}, nil
}

// parseExecScript parses an exec_script() call for running external scripts during configuration.
//
// The exec_script() function allows Blueprint files to run external scripts
// during the parsing/evaluation phase (not during the ninja build phase).
// The script is executed with the provided arguments, and its output can be
// used in variable assignments or expressions.
//
// Grammar:
//
//	exec_script -> "exec_script" LPAREN expression [COMMA expression_list] RPAREN
//
// Examples:
//
//	exec_script("detect_arch.sh")
//	exec_script("get_flag.sh", "arg1", "arg2")
//	exec_script("config.sh", "--prefix=/usr")
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at an "exec_script" IDENT token when calling this method.
//
// Returns:
//   - *ExecScript: The parsed exec_script AST node.
//     Contains keyword position, command expression, and optional arguments.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - The command (first argument) is required; missing parentheses produce an error.
//   - Optional arguments are comma-separated expressions after the command.
//   - The closing parenthesis is required; missing produces an error.
//   - The script execution happens during evaluation, not during parsing.
//     This function only parses the syntax, not executes the script.
//
// Key design decisions:
//   - Arguments are parsed as expressions to allow variable references and string concatenation.
//   - The command itself is an expression, allowing dynamic script names (though unusual).
//   - Execution is deferred to evaluation phase for better error handling and caching.
func (p *Parser) parseExecScript() (*ExecScript, error) {
	keywordPos := p.curToken.Pos
	p.nextToken()

	// Expect opening parenthesis
	if p.curToken.Type != LPAREN {
		return nil, errors.Syntax("expected '(' after 'exec_script'").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("exec_script() requires parentheses")
	}
	p.nextToken()

	// Parse the command (first argument, required)
	command, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	// Parse optional arguments (comma-separated expressions)
	args := []Expression{}
	if p.curToken.Type == COMMA {
		p.nextToken()
		for p.curToken.Type != EOF && p.curToken.Type != RPAREN {
			arg, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.curToken.Type == COMMA {
				p.nextToken()
			}
		}
	}

	// Expect closing parenthesis
	if p.curToken.Type != RPAREN {
		return nil, errors.Syntax("expected ')' after exec_script arguments").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("exec_script() must end with ')'")
	}
	p.nextToken()

	return &ExecScript{
		KeywordPos: keywordPos,
		Command:    command,
		Args:       args,
	}, nil
}

// parseConfigurableCondition parses a condition function call for select() statements.
//
// Conditions determine what value to match against in select() cases.
// They can be simple identifiers (like "arch", "os") or function calls
// with arguments (like "target(android)", "product_variable(my_var)").
//
// Built-in condition functions:
//   - arch(): Current architecture (arm, arm64, x86, x86_64)
//   - os(): Current operating system (linux, android, darwin, windows)
//   - host(): Whether building for the host machine (true/false)
//   - target(): Target platform identifier
//   - variant(): Build variant (debug, release, eng)
//   - product_variable(): Product-specific variable lookup
//   - soong_config_variable(): Configuration variable from Soong namespace
//   - release_flag(): Release flag check (true/false)
//
// Grammar:
//
//	ConfigurableCondition -> IDENT [(Expression (COMMA Expression)*)]
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at an IDENT token when calling this method.
//
// Returns:
//   - ConfigurableCondition: The parsed condition with function name, position, and arguments.
//     If no parentheses follow, Args will be empty.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - Condition names must be valid identifiers (not quoted strings).
//   - Arguments inside parentheses are optional; bare identifiers are valid (e.g., "arch").
//   - Arguments are parsed as expressions, allowing variables and string literals.
//   - Missing closing parenthesis after arguments produces an error.
//   - Empty parentheses "func()" are valid and produce an empty Args slice.
//
// Examples:
//
//	arch          -> ConfigurableCondition{FunctionName: "arch", Args: []}
//	arch()        -> ConfigurableCondition{FunctionName: "arch", Args: []}
//	target(android) -> ConfigurableCondition{FunctionName: "target", Args: [...]}
func (p *Parser) parseConfigurableCondition() (ConfigurableCondition, error) {
	if p.curToken.Type != IDENT {
		return ConfigurableCondition{}, errors.Syntax("expected identifier for condition").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("Use condition function like arch(), os()")
	}

	funcName := p.curToken.Literal
	pos := p.curToken.Pos
	p.nextToken()

	// Parse arguments if parentheses follow the function name
	args := []Expression{}
	if p.curToken.Type == LPAREN {
		p.nextToken()
		for p.curToken.Type != EOF && p.curToken.Type != RPAREN {
			arg, err := p.parseExpression()
			if err != nil {
				return ConfigurableCondition{}, err
			}
			args = append(args, arg)
			if p.curToken.Type == COMMA {
				p.nextToken()
			}
		}
		if p.curToken.Type == RPAREN {
			p.nextToken()
		}
	}

	return ConfigurableCondition{
		Position:     pos,
		FunctionName: funcName,
		Args:         args,
	}, nil
}

// parseSelectCase parses a single case in a select() statement.
//
// A case consists of one or more patterns (comma-separated) followed by a colon
// and a value expression. Multiple patterns can map to the same value,
// allowing concise handling of multiple matching conditions.
//
// Examples:
//
//	"linux": ["unix.c"]                  -> single pattern
//	"linux", "android": ["unix.c"]       -> multiple patterns, same value
//	(default): ["default.c"]              -> tuple pattern (when isTuple is true)
//
// Parameters:
//   - isTuple: True if the parent select() has multiple conditions (tuple select).
//     When true, patterns are parsed as tuples enclosed in parentheses.
//     When false, patterns are simple (single values or comma-separated).
//
// Returns:
//   - SelectCase: The parsed case with patterns and value.
//     Patterns slice contains one or more SelectPattern values.
//     Value is the expression to return when any pattern matches.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - At least one pattern is required before the colon.
//   - Multiple patterns are separated by commas without parentheses (for simple select).
//   - For tuple select (isTuple=true), patterns are enclosed in parentheses: (arm, linux): value
//   - The colon and value expression are required after patterns.
//
// Key design decisions:
//   - The isTuple parameter avoids having to re-parse or look ahead to determine
//     if parentheses are needed around patterns.
//   - Delegates to parseTupleSelectCase() or parseSimpleSelectCase() based on isTuple.
func (p *Parser) parseSelectCase(isTuple bool) (SelectCase, error) {
	// Handle tuple patterns (multiple values in parentheses)
	if isTuple && p.curToken.Type == LPAREN {
		return p.parseTupleSelectCase()
	}
	return p.parseSimpleSelectCase()
}

// parseTupleSelectCase parses a tuple case in a select() statement.
//
// A tuple case has multiple patterns enclosed in parentheses, matching multiple
// conditions in a select() with tuple conditions.
//
// Syntax:
//
//	( pattern1, pattern2, ... ): value
//
// This is used when select() has multiple conditions, e.g.:
//
//	select((arch(), os()), {
//	    (arm, linux): ["arm_linux.c"],
//	    (arm64, android): ["arm64_android.c"],
//	})
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at a LPAREN token when calling this method.
//
// Returns:
//   - SelectCase: The parsed case with tuple patterns and a value.
//     Patterns slice contains SelectPattern values for each tuple element.
//     ColonPos stores the position of the colon after the tuple.
//     Value is the expression to return when all patterns match.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - At least one pattern is required inside the parentheses.
//   - Patterns are separated by commas.
//   - The closing parenthesis after patterns is required.
//   - The colon and value expression are required after the tuple.
//
// Examples:
//
//	(arm, linux): ["arm_linux.c"]     -> tuple with 2 patterns
//	(x86_64,): ["x64.c"]             -> tuple with trailing comma (if supported)
func (p *Parser) parseTupleSelectCase() (SelectCase, error) {
	if p.curToken.Type != LPAREN {
		return SelectCase{}, errors.Syntax("expected '(' for tuple pattern in select case").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("Tuple pattern needs parentheses like (arm, linux)")
	}
	p.nextToken()

	// Parse each pattern in the tuple
	var patterns []SelectPattern
	for p.curToken.Type != EOF && p.curToken.Type != RPAREN {
		pattern, err := p.parseSelectPattern()
		if err != nil {
			return SelectCase{}, err
		}
		patterns = append(patterns, pattern)
		if p.curToken.Type == COMMA {
			p.nextToken()
		}
	}

	if p.curToken.Type != RPAREN {
		return SelectCase{}, errors.Syntax("expected ')' after tuple pattern").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("Tuple pattern must be closed with ')'")
	}
	p.nextToken()

	// Expect colon before value
	if p.curToken.Type != COLON {
		return SelectCase{}, errors.Syntax("expected ':' after select pattern").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("Pattern must be followed by ':' and value")
	}
	colonPos := p.curToken.Pos
	p.nextToken()

	// Parse the value expression
	value, err := p.parseExpression()
	if err != nil {
		return SelectCase{}, err
	}

	return SelectCase{
		Patterns: patterns,
		ColonPos: colonPos,
		Value:    value,
	}, nil
}

// parseSimpleSelectCase parses a simple (non-tuple) case in a select statement.
//
// A simple case has one or more patterns separated by commas, followed by a colon
// and a value expression. Multiple patterns can map to the same value,
// allowing concise handling of multiple matching conditions.
//
// Examples:
//
//	"linux": ["unix.c"]                    -> single pattern
//	"linux", "android": ["unix.c"]         -> multiple patterns, same value
//	"arm", "arm64": ["arm_common.c"]       -> multiple architectures
//	default: ["common.c"]                  -> default pattern
//
// Parameters:
//   - None (operates on parser's current token state).
//     The parser must be positioned at the first pattern token when calling this method.
//
// Returns:
//   - SelectCase: The parsed case with one or more patterns and a value.
//     Patterns slice contains SelectPattern values for each pattern.
//     ColonPos stores the position of the colon after the patterns.
//     Value is the expression to return when any pattern matches.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - At least one pattern is required before the colon.
//   - Multiple patterns are separated by commas (no parentheses).
//   - The colon and value expression are required after the patterns.
//   - Patterns can be any valid expression: strings, variables, "default", "unset", "any".
//
// Key design decisions:
//   - Multiple patterns mapping to the same value avoids duplicating the value expression.
//   - The function accumulates patterns in a slice until it finds a colon.
func (p *Parser) parseSimpleSelectCase() (SelectCase, error) {
	// Parse first pattern
	pattern, err := p.parseSelectPattern()
	if err != nil {
		return SelectCase{}, err
	}
	patterns := []SelectPattern{pattern}

	// Parse additional patterns separated by commas
	for p.curToken.Type == COMMA {
		p.nextToken()
		pattern, err := p.parseSelectPattern()
		if err != nil {
			return SelectCase{}, err
		}
		patterns = append(patterns, pattern)
	}

	// Expect colon before value
	if p.curToken.Type != COLON {
		return SelectCase{}, errors.Syntax("expected ':' after select pattern").
			WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
			WithSuggestion("Pattern must be followed by ':' and value")
	}
	colonPos := p.curToken.Pos
	p.nextToken()

	// Parse the value expression
	value, err := p.parseExpression()
	if err != nil {
		return SelectCase{}, err
	}

	return SelectCase{
		Patterns: patterns,
		ColonPos: colonPos,
		Value:    value,
	}, nil
}

// parseSelectPattern parses a single pattern in a select() case.
//
// A pattern is an expression that is compared against the condition value
// during evaluation. Patterns can be literals (strings, integers, booleans),
// variable references, or special keywords.
//
// Special pattern types:
//   - "unset": Matches when the configuration value is not set or empty
//   - "any": Matches any value (wildcard)
//   - "any" @ var: Matches any value and binds it to a variable for use in the value expression
//   - @var: Shorthand for "any @ var" (bind matched value to variable)
//
// Regular patterns are parsed as expressions (strings, integers, booleans, variables).
//
// Parameters:
//   - None (operates on parser's current token state).
//
// Returns:
//   - SelectPattern: The parsed pattern.
//     Contains the pattern value (Expression), IsAny flag for "any" patterns,
//     and optional Binding for "@var" patterns.
//   - error: nil if successful, otherwise a parse error with position information.
//
// Edge cases:
//   - UNSET keyword creates an Unset node as the pattern value.
//   - AT token (@) followed by IDENT creates a binding pattern (any @ var).
//   - "any" followed by AT creates a binding pattern (any @ var).
//   - Other IDENT tokens are treated as expressions (parsed via parseExpression).
//   - Default pattern is typically a string literal "default".
//
// Examples:
//
//	"linux"           -> SelectPattern{Value: String{"linux"}}
//	unset             -> SelectPattern{Value: Unset{}}
//	any               -> SelectPattern{Value: Variable{"any"}, IsAny: true}
//	any @arch         -> SelectPattern{Value: Variable{"any"}, IsAny: true, Binding: "arch"}
//	@myvar            -> SelectPattern{Value: Variable{"any"}, IsAny: true, Binding: "myvar"}
func (p *Parser) parseSelectPattern() (SelectPattern, error) {
	switch p.curToken.Type {
	case UNSET:
		// Unset pattern - matches nil or empty configuration value
		pos := p.curToken.Pos
		p.nextToken()
		return SelectPattern{Value: &Unset{KeywordPos: pos}}, nil
	case AT:
		// @ prefix for binding: @variable
		p.nextToken()
		if p.curToken.Type != IDENT {
			return SelectPattern{}, errors.Syntax("expected variable name after '@'").
				WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
				WithSuggestion("Use @variable to bind matched value")
		}
		binding := p.curToken.Literal
		p.nextToken()
		return SelectPattern{Value: &Variable{Name: "any", NamePos: p.curToken.Pos}, IsAny: true, Binding: binding}, nil
	case IDENT:
		// Check for "any @ var" pattern
		if p.curToken.Literal == "any" && p.peekToken.Type == AT {
			p.nextToken() // consume "any"
			p.nextToken() // consume "@"
			if p.curToken.Type != IDENT {
				return SelectPattern{}, errors.Syntax("expected variable name after '@'").
					WithLocation(p.fileName, p.curToken.Pos.Line, p.curToken.Pos.Column).
					WithSuggestion("Use @variable to bind matched value")
			}
			binding := p.curToken.Literal
			p.nextToken()
			return SelectPattern{Value: &Variable{Name: "any", NamePos: p.curToken.Pos}, IsAny: true, Binding: binding}, nil
		}
		fallthrough
	default:
		// Regular expression as pattern
		expr, err := p.parseExpression()
		if err != nil {
			return SelectPattern{}, err
		}
		return SelectPattern{Value: expr}, nil
	}
}

// ParseFile parses a Blueprint file from an io.Reader.
// This is a convenience function that creates a parser and parses the entire file.
// It handles all setup work and error handling so callers don't need to deal with the parser directly.
//
// Parameters:
//   - r: The input io.Reader containing Blueprint source code.
//     If nil, the source parameter is used to create a reader (if provided).
//   - fileName: The name of the file being parsed (used for error messages and source mapping).
//   - source: Optional variadic string containing the raw source code.
//     If provided, it is used for error reporting (displaying line content) and as a fallback
//     if the reader is nil. Only the first element is used if multiple are provided.
//
// Returns:
//   - *File: The parsed AST representation of the Blueprint file.
//     May contain partially parsed definitions if errors were encountered.
//   - error: nil if parsing succeeded with no errors; otherwise the first error encountered.
//     Note: Multiple parse errors are aggregated during parsing, but only the first is returned here.
//
// Edge cases:
//   - If r is nil and no source is provided, returns an error indicating the reader is invalid.
//   - If r is nil but source is provided, a strings.NewReader is created from source[0].
//   - Empty input returns a valid File with no definitions.
//   - Lexer errors are included in the aggregated parse errors.
func ParseFile(r io.Reader, fileName string, source ...string) (*File, error) {
	if r == nil {
		if len(source) > 0 {
			r = strings.NewReader(source[0])
		} else {
			return nil, fmt.Errorf("ParseFile: reader is nil and no source provided for %s", fileName)
		}
	}
	parser := NewParser(r, fileName, source...)
	file, errors := parser.Parse()
	if len(errors) > 0 {
		return file, errors[0]
	}
	return file, nil
}

// lineContent returns the content of the specified line (1-indexed) from the source text.
// This is used to provide context in error messages, allowing users to see the
// exact line of code that caused a parse error.
//
// The function splits the stored source text by newline characters and returns
// the requested line. Line numbers are 1-indexed to match typical text editor
// line numbering and scanner position reporting.
//
// Parameters:
//   - line: The 1-indexed line number to retrieve.
//     Line 1 corresponds to the first line of the source file.
//
// Returns:
//   - string: The content of the specified line, without the trailing newline character.
//     Returns empty string if the line number is out of range or source is not available.
//
// Edge cases:
//   - If p.source is empty (not provided during parser creation), returns empty string.
//   - If line is <= 0, returns empty string (line numbers are 1-indexed).
//   - If line exceeds the number of lines in the source, returns empty string.
//   - Trailing newline characters are stripped by strings.Split behavior
//     (the last line may or may not have a trailing newline depending on the input).
func (p *Parser) lineContent(line int) string {
	if p.source == "" || line <= 0 {
		return ""
	}
	lines := strings.Split(p.source, "\n")
	if line > len(lines) {
		return ""
	}
	return lines[line-1]
}

// init is called when the parser package is imported.
//
// Currently a no-op but reserved for future package-level initialization.
// Possible future uses:
//   - Initializing lexer keyword tables
//   - Registering built-in functions for select() conditions
//   - Setting up any package-level state
//
// Note: Package initialization is automatic in Go; this function is called
// before any other code in the package is executed.
func init() {
	// Reserved for package initialization
	// No initialization needed currently
}
