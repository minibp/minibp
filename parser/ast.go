// parser/ast.go - Blueprint AST definitions
package parser

import (
	"strconv"
	"text/scanner"
)

// Expression is the interface for all AST nodes
type Expression interface {
	String() string
	Pos() scanner.Position
}

// Module represents a module definition like cc_binary { ... }
type Module struct {
	Type    string
	TypePos scanner.Position
	Map     *Map
	Arch    map[string]*Map // arch-specific overrides: arch name -> properties
	Host    *Map            // host-specific overrides
	Target  *Map            // target-specific overrides
}

func (m *Module) Pos() scanner.Position { return m.TypePos }
func (m *Module) String() string {
	return m.Type + " " + m.Map.String()
}

// Map represents a property list { name: value, ... }
type Map struct {
	Properties []*Property
	LBracePos  scanner.Position
	RBracePos  scanner.Position
}

func (m *Map) Pos() scanner.Position { return m.LBracePos }
func (m *Map) String() string {
	result := "{"
	for i, p := range m.Properties {
		if i > 0 {
			result += ", "
		}
		result += p.String()
	}
	result += "}"
	return result
}

// Property is a key-value pair
type Property struct {
	Name     string
	NamePos  scanner.Position
	Value    Expression
	ColonPos scanner.Position
}

func (p *Property) String() string {
	return p.Name + ": " + p.Value.String()
}

// String represents a string literal
type String struct {
	Value      string
	LiteralPos scanner.Position
}

func (s *String) Pos() scanner.Position { return s.LiteralPos }
func (s *String) String() string        { return s.Value }

// Int64 represents an integer
type Int64 struct {
	Value      int64
	LiteralPos scanner.Position
}

func (i *Int64) Pos() scanner.Position { return i.LiteralPos }
func (i *Int64) String() string        { return strconv.FormatInt(i.Value, 10) }

// Bool represents true/false
type Bool struct {
	Value      bool
	LiteralPos scanner.Position
}

func (b *Bool) Pos() scanner.Position { return b.LiteralPos }
func (b *Bool) String() string {
	if b.Value {
		return "true"
	}
	return "false"
}

// List represents [value1, value2, ...]
type List struct {
	Values    []Expression
	LBracePos scanner.Position
	RBracePos scanner.Position
}

func (l *List) Pos() scanner.Position { return l.LBracePos }
func (l *List) String() string {
	result := "["
	for i, v := range l.Values {
		if i > 0 {
			result += ", "
		}
		result += v.String()
	}
	result += "]"
	return result
}

// Variable is a reference like my_var
type Variable struct {
	Name    string
	NamePos scanner.Position
}

func (v *Variable) Pos() scanner.Position { return v.NamePos }
func (v *Variable) String() string        { return v.Name }

// Assignment represents foo = value or foo += value
type Assignment struct {
	Name      string
	NamePos   scanner.Position
	EqualsPos scanner.Position
	Assigner  string // "=" or "+="
	Value     Expression
}

func (a *Assignment) Pos() scanner.Position { return a.NamePos }
func (a *Assignment) String() string {
	return a.Name + " " + a.Assigner + " " + a.Value.String()
}

// Operator represents a + b
type Operator struct {
	Args        [2]Expression
	Operator    rune
	OperatorPos scanner.Position
}

func (o *Operator) Pos() scanner.Position { return o.OperatorPos }
func (o *Operator) String() string {
	return o.Args[0].String() + " " + string(o.Operator) + " " + o.Args[1].String()
}

// Select represents conditional values
type Select struct {
	KeywordPos scanner.Position
	Conditions []ConfigurableCondition
	LBracePos  scanner.Position
	RBracePos  scanner.Position
	Cases      []SelectCase
}

func (s *Select) Pos() scanner.Position { return s.KeywordPos }
func (s *Select) String() string        { return "select" }

type ConfigurableCondition struct {
	Position     scanner.Position
	FunctionName string
	Args         []Expression
}

type SelectCase struct {
	Patterns []SelectPattern
	ColonPos scanner.Position
	Value    Expression
}

type SelectPattern struct {
	Value   Expression
	Binding Variable
}

// File represents a parsed Blueprint file
type File struct {
	Name string
	Defs []Definition
}

// Definition is either a Module or Assignment
type Definition interface {
	def()
}

func (m *Module) def()     {}
func (a *Assignment) def() {}
