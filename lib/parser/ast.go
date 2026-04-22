// parser/ast.go - Blueprint AST definitions
package parser

import (
	"strconv"
	"text/scanner"
)

// Expression is the interface for all AST nodes that can appear in expressions.
// All expression types implement this interface to provide their source position
// and string representation. This allows the parser and evaluator to handle
// different expression types uniformly.
type Expression interface {
	String() string        // Returns a string representation of the expression
	Pos() scanner.Position // Returns the source position of this expression
}

// Module represents a module definition like cc_binary { ... } or cc_library { ... }.
// A module has a type (e.g., "cc_binary", "cc_library") and a map of properties.
// It can also have architecture-specific (arch), host-specific (host), and
// target-specific (target) property overrides.
type Module struct {
	Type    string           // Module type name (e.g., "cc_binary", "cc_library")
	TypePos scanner.Position // Source position of the type name
	Map     *Map             // Main property map (name: value, ...)
	Arch    map[string]*Map  // Arch-specific overrides: arch name -> properties (e.g., "arm" -> {srcs: ...})
	Host    *Map             // Host-specific overrides (e.g., host: {cflags: ...})
	Target  *Map             // Target-specific overrides (e.g., target: {cflags: ...})
}

// Pos returns the source position of the module type.
func (m *Module) Pos() scanner.Position { return m.TypePos }

// String returns a string representation of the module (type + properties).
func (m *Module) String() string {
	return m.Type + " " + m.Map.String()
}

// Map represents a property list { name: value, name: value, ... }.
// Maps are used for module properties and nested property structures.
// They consist of an ordered list of Property nodes.
type Map struct {
	Properties []*Property      // Ordered list of properties in the map
	LBracePos  scanner.Position // Position of the opening { brace
	RBracePos  scanner.Position // Position of the closing } brace
}

// Pos returns the position of the opening brace.
func (m *Map) Pos() scanner.Position { return m.LBracePos }

// String returns a string representation of the map (e.g., "{name: value, ...}").
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

// Property is a key-value pair: name: value.
// Properties appear in module definitions and nested maps.
// The value can be any Expression type (string, int, list, map, variable, select).
type Property struct {
	Name     string           // Property name (left side of colon)
	NamePos  scanner.Position // Position of the property name
	Value    Expression       // Property value (right side of colon)
	ColonPos scanner.Position // Position of the colon separator
}

// String returns a string representation (e.g., "name: value").
func (p *Property) String() string {
	return p.Name + ": " + p.Value.String()
}

// String represents a string literal like "hello" or `raw string`.
// String literals can contain escape sequences and are unquoted during parsing.
type String struct {
	Value      string           // The string content (without quotes)
	LiteralPos scanner.Position // Position of the string literal in source
}

// Pos returns the position of the string literal.
func (s *String) Pos() scanner.Position { return s.LiteralPos }

// String returns the string value.
func (s *String) String() string { return s.Value }

// Int64 represents an integer literal like 42 or -10.
// Integers are stored as signed 64-bit integers.
type Int64 struct {
	Value      int64            // The integer value
	LiteralPos scanner.Position // Position of the integer literal in source
}

// Pos returns the position of the integer literal.
func (i *Int64) Pos() scanner.Position { return i.LiteralPos }

// String returns the string representation of the integer.
func (i *Int64) String() string { return strconv.FormatInt(i.Value, 10) }

// Bool represents a boolean literal: true or false.
type Bool struct {
	Value      bool             // The boolean value (true or false)
	LiteralPos scanner.Position // Position of the boolean literal in source
}

// Pos returns the position of the boolean literal.
func (b *Bool) Pos() scanner.Position { return b.LiteralPos }

// String returns "true" or "false".
func (b *Bool) String() string {
	if b.Value {
		return "true"
	}
	return "false"
}

// List represents an ordered collection of values: [value1, value2, ...].
// Lists can contain any expression type as elements.
type List struct {
	Values    []Expression     // Ordered list of expressions
	LBracePos scanner.Position // Position of the opening [ bracket
	RBracePos scanner.Position // Position of the closing ] bracket
}

// Pos returns the position of the opening bracket.
func (l *List) Pos() scanner.Position { return l.LBracePos }

// String returns a string representation (e.g., "[a, b, c]").
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

// Variable is a reference to a previously defined variable: my_var.
// During evaluation, variable references are resolved to their assigned values.
type Variable struct {
	Name    string           // Variable name
	NamePos scanner.Position // Position of the variable name in source
}

// Pos returns the position of the variable name.
func (v *Variable) Pos() scanner.Position { return v.NamePos }

// String returns the variable name.
func (v *Variable) String() string { return v.Name }

// Assignment represents a variable assignment: foo = value or foo += value.
// Assignments can be simple (=) or concatenative (+=).
type Assignment struct {
	Name      string           // Variable name being assigned
	NamePos   scanner.Position // Position of the variable name
	EqualsPos scanner.Position // Position of the = or += operator
	Assigner  string           // The assignment operator: "=" or "+="
	Value     Expression       // The value being assigned
}

// Pos returns the position of the variable name.
func (a *Assignment) Pos() scanner.Position { return a.NamePos }

// String returns a string representation (e.g., "foo = "bar"").
func (a *Assignment) String() string {
	return a.Name + " " + a.Assigner + " " + a.Value.String()
}

// Operator represents a binary operation: left op right.
// Currently only the + (concatenation/addition) operator is supported.
type Operator struct {
	Args        [2]Expression    // Left and right operands
	Operator    rune             // The operator rune ('+' for now)
	OperatorPos scanner.Position // Position of the operator
}

// Pos returns the position of the operator.
func (o *Operator) Pos() scanner.Position { return o.OperatorPos }

// String returns a string representation (e.g., "a + b").
func (o *Operator) String() string {
	return o.Args[0].String() + " " + string(o.Operator) + " " + o.Args[1].String()
}

// Select represents a conditional expression: select(condition, { cases }).
// Select chooses different values based on configuration (arch, os, host, target).
// It evaluates the condition and returns the value from the matching case.
type Select struct {
	KeywordPos scanner.Position        // Position of the "select" keyword
	Conditions []ConfigurableCondition // Condition expressions
	LBracePos  scanner.Position        // Position of the opening { brace for cases
	RBracePos  scanner.Position        // Position of the closing } brace for cases
	Cases      []SelectCase            // List of cases (pattern: value)
}

// Pos returns the position of the select keyword.
func (s *Select) Pos() scanner.Position { return s.KeywordPos }

// String returns "select".
func (s *Select) String() string { return "select" }

// ConfigurableCondition represents a condition in a select expression.
// It can be a simple identifier (like "arch"), or a function call
// with arguments (like "target(android)").
type ConfigurableCondition struct {
	Position     scanner.Position // Position of the condition
	FunctionName string           // The function/identifier name (empty for direct value)
	Args         []Expression     // Arguments to the function (if any)
}

// SelectCase represents a single case in a select expression.
// A case has one or more patterns that map to a single value.
// Multiple patterns can share the same value (e.g., "linux", "android": value).
type SelectCase struct {
	Patterns []SelectPattern  // List of patterns that map to this value
	ColonPos scanner.Position // Position of the colon after patterns
	Value    Expression       // The value returned when any pattern matches
}

// SelectPattern represents a single pattern in a select case.
// A pattern is compared against the condition value to determine if this case matches.
// It also supports the "any @ var" binding syntax for wildcard pattern matching.
type SelectPattern struct {
	Value   Expression // The pattern expression (string, int, bool, variable, unset)
	IsAny   bool       // True if this is an "any" wildcard pattern
	Binding string     // Variable name to bind the matched value to (for "any @ var" syntax)
}

// Unset represents the unset keyword in a select statement.
// When a select branch evaluates to Unset, the property is treated as if it was never assigned.
type Unset struct {
	KeywordPos scanner.Position // Position of the "unset" keyword
}

func (u *Unset) Pos() scanner.Position { return u.KeywordPos }
func (u *Unset) String() string        { return "unset" }

// File represents a parsed Blueprint file.
// It contains a list of definitions (modules and assignments).
type File struct {
	Name string       // File name (for error reporting)
	Defs []Definition // List of top-level definitions in the file
}

// Definition is either a Module or an Assignment.
// This interface is implemented by both types to allow unified handling
// of top-level definitions in a file.
type Definition interface {
	def() // Private method to implement the interface
}

// def implements the Definition interface for Module.
func (m *Module) def() {}

// def implements the Definition interface for Assignment.
func (a *Assignment) def() {}
