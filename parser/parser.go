// parser/parser.go - Blueprint parser
package parser

import (
	"fmt"
	"io"
	"strconv"
	"text/scanner"
)

// Parser parses Blueprint files
type Parser struct {
	lexer     *Lexer
	curToken  Token
	peekToken Token
	fileName  string
	errors    []error
}

// NewParser creates a new parser from an io.Reader
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

// nextToken advances to the next token
func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.lexer.NextToken()
}

// expect checks if the current token matches the expected type
func (p *Parser) expect(t TokenType) (Token, error) {
	if p.curToken.Type == t {
		tok := p.curToken
		p.nextToken()
		return tok, nil
	}
	return Token{}, fmt.Errorf("%s: expected %s, got %s", p.curToken.Pos, t, p.curToken.Type)
}

// expectPeek checks if the peek token matches the expected type
func (p *Parser) expectPeek(t TokenType) bool {
	if p.peekToken.Type == t {
		p.nextToken()
		return true
	}
	return false
}

// Parse parses the input and returns a File
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

// skipToNextDefinition skips tokens until we reach a potential start of a definition
func (p *Parser) skipToNextDefinition() {
	for p.curToken.Type != EOF && p.curToken.Type != IDENT {
		p.nextToken()
	}
}

// parseDefinition parses either a module or an assignment
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

// parsePropertyList parses a list of properties: { property [,] }
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

// parseAssignment parses: name (= | +=) expression
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

// parseExpression parses any expression, including + operators
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

// parsePrimary parses a single primary expression (no operators)
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
	default:
		return nil, fmt.Errorf("%s: unexpected token %s in expression", p.curToken.Pos, p.curToken.Type)
	}
}

// parseString parses a string literal
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

// parseInt parses an integer literal
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

// parseBool parses a boolean literal
func (p *Parser) parseBool() (*Bool, error) {
	pos := p.curToken.Pos
	literal := p.curToken.Literal
	p.nextToken()

	return &Bool{
		Value:      literal == "true",
		LiteralPos: pos,
	}, nil
}

// parseVariable parses a variable reference
func (p *Parser) parseVariable() (*Variable, error) {
	pos := p.curToken.Pos
	name := p.curToken.Literal
	p.nextToken()

	return &Variable{
		Name:    name,
		NamePos: pos,
	}, nil
}

// parseList parses a list: [ expression [,] ]
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
func (p *Parser) parseSelect() (*Select, error) {
	keywordPos := p.curToken.Pos
	p.nextToken()

	if p.curToken.Type != LPAREN {
		return nil, fmt.Errorf("%s: expected '(' after 'select'", p.curToken.Pos)
	}
	p.nextToken()

	// Parse conditions (variable or first case condition)
	conditions := []ConfigurableCondition{}
	if p.curToken.Type != LBRACE {
		// First argument is conditions variable/expression
		cond, err := p.parseConfigurableCondition()
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, cond)

		if p.curToken.Type == COMMA {
			p.nextToken()
		}
	}

	// Parse cases: { case_pattern: value, ... }
	if p.curToken.Type != LBRACE {
		return nil, fmt.Errorf("%s: expected '{' for select cases", p.curToken.Pos)
	}
	lbracePos := p.curToken.Pos
	p.nextToken()

	cases := []SelectCase{}
	for p.curToken.Type != EOF && p.curToken.Type != RBRACE {
		caseItem, err := p.parseSelectCase()
		if err != nil {
			return nil, err
		}
		cases = append(cases, caseItem)

		// Optional comma after case
		if p.curToken.Type == COMMA {
			p.nextToken()
		}
	}

	if p.curToken.Type != RBRACE {
		return nil, fmt.Errorf("%s: expected '}' after select cases", p.curToken.Pos)
	}
	rbracePos := p.curToken.Pos
	p.nextToken()

	// Expect closing paren
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

// parseConfigurableCondition parses a condition for select
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

// parseSelectCase parses a single case in a select
func (p *Parser) parseSelectCase() (SelectCase, error) {
	// Parse a single pattern (simplified for now)
	pattern, err := p.parseSelectPattern()
	if err != nil {
		return SelectCase{}, err
	}
	patterns := []SelectPattern{pattern}

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

// parseSelectPattern parses a single pattern in a select case
func (p *Parser) parseSelectPattern() (SelectPattern, error) {
	// For simplicity, we handle the common cases
	expr, err := p.parseExpression()
	if err != nil {
		return SelectPattern{}, err
	}

	return SelectPattern{
		Value: expr,
	}, nil
}

// ParseFile parses a Blueprint file from a reader
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
