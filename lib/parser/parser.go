// parser/parser.go - Blueprint parser
package parser

import (
	"fmt"
	"io"
	"strconv"
	"text/scanner"
)

// Parser parses Blueprint files.
// It uses a recursive descent parsing approach, consuming tokens from the lexer
// and building an AST (Abstract Syntax Tree) representation of the Blueprint code.
// The parser handles modules, assignments, expressions, and special constructs
// like select() statements for conditional values.
type Parser struct {
	lexer     *Lexer  // The lexer used to tokenize the input
	curToken  Token   // The current token being processed
	peekToken Token   // The next token (lookahead)
	fileName  string  // Name of the file being parsed (for error reporting)
	errors    []error // List of parsing errors encountered
}

// NewParser creates a new parser from an ioReader.
// It initializes the parser with a new lexer and advances past the first two tokens
// to set up curToken and peekToken for the parsing process.
// Parameters:
//   - r: The input reader containing Blueprint source code
//   - fileName: The name of the file being parsed (used for error messages)
//
// Returns:
//
//	A new Parser instance ready to parse the input
func NewParser(r io.Reader, fileName string) *Parser {
	p := &Parser{
		lexer:    NewLexer(r, fileName),
		fileName: fileName,
		errors:   []error{},
	}
	// Initialize curToken and peekToken
	p.nextToken()
	p.nextToken()
	return p
}

// nextToken advances to the next token.
// It moves the current token to peekToken and fetches a new peekToken from the lexer.
// This is the primary mechanism for traversing the token stream during parsing.
func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.lexer.NextToken()
}

// expect checks if the current token matches the expected type.
// If it matches, it consumes the token and advances to the next token.
// If it doesn't match, it returns an error with the position and expected vs actual token types.
// Parameters:
//   - t: The expected TokenType
//
// Returns:
//   - Token: The matched token if successful
//   - error: nil if successful, otherwise an error describing the mismatch
func (p *Parser) expect(t TokenType) (Token, error) {
	if p.curToken.Type == t {
		tok := p.curToken
		p.nextToken()
		return tok, nil
	}
	return Token{}, fmt.Errorf("%s: expected %s, got %s", p.curToken.Pos, t, p.curToken.Type)
}

// expectPeek checks if the peek token matches the expected type.
// Unlike expect(), this checks the lookahead token without consuming it.
// If the peek token matches, it advances the token stream.
// Parameters:
//   - t: The expected TokenType for the peek token
//
// Returns:
//   - bool: true if the peek token matched and was consumed, false otherwise
func (p *Parser) expectPeek(t TokenType) bool {
	if p.peekToken.Type == t {
		p.nextToken()
		return true
	}
	return false
}

// Parse parses the entire input and returns a File AST node.
// It repeatedly parses definitions until the end of file is reached.
// After parsing all definitions, it also collects any lexer errors.
// Returns:
//   - *File: The parsed AST representation of the Blueprint file
//   - []error: A list of errors encountered during parsing (empty if successful)
func (p *Parser) Parse() (*File, []error) {
	file := &File{Name: p.fileName}

	for p.curToken.Type != EOF {
		def, err := p.parseDefinition()
		if err != nil {
			p.errors = append(p.errors, err)
			p.skipToNextDefinition()
		} else if def != nil {
			file.Defs = append(file.Defs, def)
		}
	}

	if len(p.errors) == 0 {
		p.errors = append(p.errors, p.lexer.Errors()...)
	}

	return file, p.errors
}

// skipToNextDefinition skips tokens until we reach a potential start of a definition.
// This is used for error recovery - when a parse error occurs, we skip forward to try
// to continue parsing subsequent definitions rather than stopping entirely.
// It skips tokens until it finds an IDENT token (which could be a module type or variable name)
// or reaches EOF.
func (p *Parser) skipToNextDefinition() {
	for p.curToken.Type != EOF && p.curToken.Type != IDENT {
		p.nextToken()
	}
}

// parseDefinition parses either a module or an assignment.
// A definition starts with an identifier (module type or variable name).
// After the identifier:
//   - If followed by LBRACE ({), it's a module definition
//   - If followed by ASSIGN (=) or PLUSEQ (+=), it's an assignment
//
// Returns:
//   - Definition: A Module or Assignment AST node
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseDefinition() (Definition, error) {
	if p.curToken.Type != IDENT {
		return nil, fmt.Errorf("%s: expected identifier, got %s", p.curToken.Pos, p.curToken.Type)
	}

	name := p.curToken.Literal
	namePos := p.curToken.Pos

	p.nextToken()

	switch p.curToken.Type {
	case LBRACE:
		// Module definition: name { ... }
		return p.parseModule(name, namePos)
	case ASSIGN, PLUSEQ:
		// Assignment: name = value or name += value
		return p.parseAssignment(name, namePos)
	default:
		return nil, fmt.Errorf("%s: unexpected token %s after identifier '%s'", p.curToken.Pos, p.curToken.Type, name)
	}
}

// parseModule parses a module definition: type { property_list }
// A module consists of a type name (like "cc_binary", "cc_library") followed by
// a block of properties enclosed in braces. Special properties "arch", "host", and
// "target" are extracted as architecture/target-specific overrides.
// Parameters:
//   - typeName: The module type name (e.g., "cc_binary")
//   - typePos: The source position of the type name
//
// Returns:
//   - *Module: The parsed module AST node
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseModule(typeName string, typePos scanner.Position) (*Module, error) {
	// Current token is LBRACE
	lbracePos := p.curToken.Pos
	p.nextToken()

	propertyList, rbracePos, err := p.parsePropertyList()
	if err != nil {
		return nil, err
	}

	// Extract arch, host, target overrides from properties.
	archProps := make(map[string]*Map)
	var hostProps *Map
	var targetProps *Map
	var filteredProps []*Property
	for _, prop := range propertyList {
		switch prop.Name {
		case "arch":
			archMap, ok := prop.Value.(*Map)
			if !ok {
				return nil, fmt.Errorf("%s: expected map value for 'arch' override", prop.ColonPos)
			}
			for _, ap := range archMap.Properties {
				archInner, ok := ap.Value.(*Map)
				if !ok {
					return nil, fmt.Errorf("%s: expected map value for arch override '%s'", ap.ColonPos, ap.Name)
				}
				archProps[ap.Name] = archInner
			}
		case "host":
			m, ok := prop.Value.(*Map)
			if !ok {
				return nil, fmt.Errorf("%s: expected map value for 'host' override", prop.ColonPos)
			}
			hostProps = m
		case "target":
			m, ok := prop.Value.(*Map)
			if !ok {
				return nil, fmt.Errorf("%s: expected map value for 'target' override", prop.ColonPos)
			}
			targetProps = m
		default:
			filteredProps = append(filteredProps, prop)
		}
	}
	mod := &Module{
		Type:    typeName,
		TypePos: typePos,
		Map:     &Map{Properties: filteredProps, LBracePos: lbracePos, RBracePos: rbracePos},
		Arch:    archProps,
		Host:    hostProps,
		Target:  targetProps,
	}

	return mod, nil
}

// parsePropertyList parses a list of properties: { property [, property] }
// Properties are key-value pairs separated by commas. Trailing commas are allowed.
// The parser reads properties until it encounters a closing brace (}).
// Returns:
//   - []*Property: List of parsed properties
//   - scanner.Position: Position of the closing right brace
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parsePropertyList() ([]*Property, scanner.Position, error) {
	properties := []*Property{}
	var rbracePos scanner.Position

	for p.curToken.Type != EOF && p.curToken.Type != RBRACE {
		prop, err := p.parseProperty()
		if err != nil {
			return nil, rbracePos, err
		}
		if prop != nil {
			properties = append(properties, prop)
		}

		if p.curToken.Type == RBRACE {
			break
		}

		// Comma separates adjacent properties; trailing commas are still allowed.
		if p.curToken.Type == COMMA {
			p.nextToken()
			continue
		}

		return nil, rbracePos, fmt.Errorf("%s: expected ',' or '}' after property", p.curToken.Pos)
	}

	if p.curToken.Type != RBRACE {
		return nil, rbracePos, fmt.Errorf("%s: expected }", p.curToken.Pos)
	}
	rbracePos = p.curToken.Pos
	p.nextToken()

	return properties, rbracePos, nil
}

// parseProperty parses a single property: name : expression
// A property consists of an identifier, followed by a colon, followed by an expression.
// The expression can be a string, integer, boolean, list, map, variable, or select statement.
// Returns:
//   - *Property: The parsed property AST node
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseProperty() (*Property, error) {
	if p.curToken.Type != IDENT {
		return nil, fmt.Errorf("%s: expected property name (identifier), got %s", p.curToken.Pos, p.curToken.Type)
	}

	name := p.curToken.Literal
	namePos := p.curToken.Pos

	p.nextToken()

	if p.curToken.Type != COLON {
		return nil, fmt.Errorf("%s: expected ':' after property name '%s'", p.curToken.Pos, name)
	}
	colonPos := p.curToken.Pos
	p.nextToken()

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
// Assignments can be simple (=) or concatenative (+=).
// For +=, the parser handles string and list concatenation differently during evaluation.
// Parameters:
//   - name: The variable name being assigned to
//   - namePos: The source position of the variable name
//
// Returns:
//   - *Assignment: The parsed assignment AST node
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseAssignment(name string, namePos scanner.Position) (*Assignment, error) {
	assigner := "="
	equalsPos := p.curToken.Pos

	if p.curToken.Type == PLUSEQ {
		assigner = "+="
	} else if p.curToken.Type != ASSIGN {
		return nil, fmt.Errorf("%s: expected '=' or '+=', got %s", p.curToken.Pos, p.curToken.Type)
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

// parseExpression parses any expression, including + operators.
// This handles left-to-right associativity for the + operator.
// For example, "a + b + c" is parsed as "(a + b) + c".
// Returns:
//   - Expression: The parsed expression AST node
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseExpression() (Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for p.curToken.Type == PLUS {
		opPos := p.curToken.Pos
		p.nextToken()

		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}

		left = &Operator{
			Args:        [2]Expression{left, right},
			Operator:    '+',
			OperatorPos: opPos,
		}
	}

	return left, nil
}

// parsePrimary parses a single primary expression (no operators).
// Primary expressions are the base units that cannot be broken down further:
//   - STRING: Quoted string literals
//   - INT: Integer literals
//   - BOOL: Boolean literals (true/false)
//   - LBRACKET: List expressions [expr, ...]
//   - LBRACE: Map expressions { prop: value, ... }
//   - IDENT: Either the "select" keyword or a variable reference
//
// Returns:
//   - Expression: The parsed primary expression
//   - error: nil if successful, otherwise a parse error
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
		if p.curToken.Literal == "select" {
			return p.parseSelect()
		}
		return p.parseVariable()
	case UNSET:
		pos := p.curToken.Pos
		p.nextToken()
		return &Unset{KeywordPos: pos}, nil
	default:
		return nil, fmt.Errorf("%s: unexpected token %s in expression", p.curToken.Pos, p.curToken.Type)
	}
}

// parseString parses a string literal.
// String literals are surrounded by quotes and may contain escape sequences.
// The parser removes the quotes and processes escape sequences using strconv.Unquote.
// Returns:
//   - *String: The parsed string AST node
//   - error: nil if successful, otherwise a parse error (e.g., unterminated string)
func (p *Parser) parseString() (*String, error) {
	pos := p.curToken.Pos
	literal := p.curToken.Literal
	p.nextToken()

	// Remove quotes from literal
	value, err := strconv.Unquote(literal)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid string literal: %v", pos, err)
	}

	return &String{
		Value:      value,
		LiteralPos: pos,
	}, nil
}

// parseInt parses an integer literal.
// Integer literals are base-10 numbers that are parsed into int64 values.
// Returns:
//   - *Int64: The parsed integer AST node
//   - error: nil if successful, otherwise a parse error (e.g., overflow)
func (p *Parser) parseInt() (*Int64, error) {
	pos := p.curToken.Pos
	literal := p.curToken.Literal
	p.nextToken()

	value, err := strconv.ParseInt(literal, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid integer literal: %v", pos, err)
	}

	return &Int64{
		Value:      value,
		LiteralPos: pos,
	}, nil
}

// parseBool parses a boolean literal.
// Boolean literals are the keywords "true" and "false".
// Returns:
//   - *Bool: The parsed boolean AST node
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
// A variable reference is an identifier that refers to a previously defined variable
// or assignment. During evaluation, the variable's value will be substituted.
// Returns:
//   - *Variable: The parsed variable reference AST node
func (p *Parser) parseVariable() (*Variable, error) {
	pos := p.curToken.Pos
	name := p.curToken.Literal
	p.nextToken()

	return &Variable{
		Name:    name,
		NamePos: pos,
	}, nil
}

// parseList parses a list: [ expression [, expression] ]
// Lists are ordered collections of expressions, separated by commas.
// Trailing commas are allowed.
// Returns:
//   - *List: The parsed list AST node
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseList() (*List, error) {
	lbracePos := p.curToken.Pos
	p.nextToken()

	values := []Expression{}
	var rbracePos scanner.Position

	for p.curToken.Type != EOF && p.curToken.Type != RBRACKET {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		values = append(values, expr)

		if p.curToken.Type == RBRACKET {
			break
		}

		// Comma separates adjacent list elements; trailing commas are still allowed.
		if p.curToken.Type == COMMA {
			p.nextToken()
			continue
		}

		return nil, fmt.Errorf("%s: expected ',' or ']' after list element", p.curToken.Pos)
	}

	if p.curToken.Type != RBRACKET {
		return nil, fmt.Errorf("%s: expected ]", p.curToken.Pos)
	}
	rbracePos = p.curToken.Pos
	p.nextToken()

	return &List{
		Values:    values,
		LBracePos: lbracePos,
		RBracePos: rbracePos,
	}, nil
}

// parseMap parses a map: { property_list }
// Maps are collections of key-value pairs enclosed in braces.
// They share the same syntax as property lists, so parsePropertyList is reused.
// Returns:
//   - *Map: The parsed map AST node
//   - error: nil if successful, otherwise a parse error
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

// parseSelect parses a select expression: select(conditions, { cases })
// Select is a conditional expression that chooses values based on configuration.
// The syntax is: select(condition, { pattern1: value1, pattern2: value2, ... })
// The first argument is a condition (like "arch", "os", "host") or a variable.
// The second argument is a map of patterns to values. The "default" pattern is used
// when no other pattern matches.
// Returns:
//   - *Select: The parsed select AST node
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseSelect() (*Select, error) {
	keywordPos := p.curToken.Pos
	p.nextToken()

	if p.curToken.Type != LPAREN {
		return nil, fmt.Errorf("%s: expected '(' after 'select'", p.curToken.Pos)
	}
	p.nextToken()

	conditions := []ConfigurableCondition{}

	// Check for tuple condition: select((arch(), os()), {...})
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
			return nil, fmt.Errorf("%s: expected ')' after tuple conditions", p.curToken.Pos)
		}
		p.nextToken()
	} else {
		cond, err := p.parseConfigurableCondition()
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, cond)
	}

	if p.curToken.Type == COMMA {
		p.nextToken()
	}

	// Parse cases: { case_pattern: value, ... }
	if p.curToken.Type != LBRACE {
		return nil, fmt.Errorf("%s: expected '{' for select cases", p.curToken.Pos)
	}
	lbracePos := p.curToken.Pos
	p.nextToken()

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

	if p.curToken.Type != RBRACE {
		return nil, fmt.Errorf("%s: expected '}' after select cases", p.curToken.Pos)
	}
	rbracePos := p.curToken.Pos
	p.nextToken()

	if p.curToken.Type != RPAREN {
		return nil, fmt.Errorf("%s: expected ')' after select cases", p.curToken.Pos)
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

// parseConfigurableCondition parses a condition for select.
// Conditions can be simple identifiers (like "arch", "os") or function calls
// with arguments (like "target(android)").
// Returns:
//   - ConfigurableCondition: The parsed condition
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseConfigurableCondition() (ConfigurableCondition, error) {
	if p.curToken.Type != IDENT {
		return ConfigurableCondition{}, fmt.Errorf("%s: expected identifier for condition", p.curToken.Pos)
	}

	funcName := p.curToken.Literal
	pos := p.curToken.Pos
	p.nextToken()

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

// parseSelectCase parses a single case in a select statement.
// A case consists of one or more patterns separated by commas, followed by a colon
// and a value expression. Multiple patterns can map to the same value.
// Example: "linux", "android": ["unix.c"]
// Returns:
//   - SelectCase: The parsed case
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseSelectCase(isTuple bool) (SelectCase, error) {
	if isTuple && p.curToken.Type == LPAREN {
		return p.parseTupleSelectCase()
	}
	return p.parseSimpleSelectCase()
}

func (p *Parser) parseTupleSelectCase() (SelectCase, error) {
	if p.curToken.Type != LPAREN {
		return SelectCase{}, fmt.Errorf("%s: expected '(' for tuple pattern in select case", p.curToken.Pos)
	}
	p.nextToken()

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
		return SelectCase{}, fmt.Errorf("%s: expected ')' after tuple pattern", p.curToken.Pos)
	}
	p.nextToken()

	if p.curToken.Type != COLON {
		return SelectCase{}, fmt.Errorf("%s: expected ':' after select pattern", p.curToken.Pos)
	}
	colonPos := p.curToken.Pos
	p.nextToken()

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

func (p *Parser) parseSimpleSelectCase() (SelectCase, error) {
	pattern, err := p.parseSelectPattern()
	if err != nil {
		return SelectCase{}, err
	}
	patterns := []SelectPattern{pattern}
	for p.curToken.Type == COMMA {
		p.nextToken()
		pattern, err := p.parseSelectPattern()
		if err != nil {
			return SelectCase{}, err
		}
		patterns = append(patterns, pattern)
	}

	if p.curToken.Type != COLON {
		return SelectCase{}, fmt.Errorf("%s: expected ':' after select pattern", p.curToken.Pos)
	}
	colonPos := p.curToken.Pos
	p.nextToken()

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

// parseSelectPattern parses a single pattern in a select case.
// A pattern is an expression that is compared against the condition value.
// Common patterns include string literals (e.g., "linux"), integer literals,
// boolean literals, or variable references like "default".
// Returns:
//   - SelectPattern: The parsed pattern
//   - error: nil if successful, otherwise a parse error
func (p *Parser) parseSelectPattern() (SelectPattern, error) {
	switch p.curToken.Type {
	case UNSET:
		pos := p.curToken.Pos
		p.nextToken()
		return SelectPattern{Value: &Unset{KeywordPos: pos}}, nil
	case AT:
		p.nextToken()
		if p.curToken.Type != IDENT {
			return SelectPattern{}, fmt.Errorf("%s: expected variable name after '@'", p.curToken.Pos)
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
				return SelectPattern{}, fmt.Errorf("%s: expected variable name after '@'", p.curToken.Pos)
			}
			binding := p.curToken.Literal
			p.nextToken()
			return SelectPattern{Value: &Variable{Name: "any", NamePos: p.curToken.Pos}, IsAny: true, Binding: binding}, nil
		}
		fallthrough
	default:
		expr, err := p.parseExpression()
		if err != nil {
			return SelectPattern{}, err
		}
		return SelectPattern{Value: expr}, nil
	}
}

// ParseFile parses a Blueprint file from an io.Reader.
// This is a convenience function that creates a parser and parses the entire file.
// Parameters:
//   - r: The input reader containing Blueprint source code
//   - fileName: The name of the file (used for error messages)
//
// Returns:
//   - *File: The parsed AST
//   - error: nil if successful, otherwise the first error encountered
func ParseFile(r io.Reader, fileName string) (*File, error) {
	parser := NewParser(r, fileName)
	file, errors := parser.Parse()
	if len(errors) > 0 {
		return file, errors[0]
	}
	return file, nil
}

// token type for parenthesis - need to add to lexer if we want full support
// For now, handle in parser with case '('
func init() {
	// Ensure the parser can handle '(' and ')' tokens
}
