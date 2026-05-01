// Package parser provides lexical analysis and parsing for Blueprint build definitions.
// AST subpackage - Abstract Syntax Tree node definitions.
//
// This package defines all the AST node types used to represent Blueprint
// source code after parsing. Each type corresponds to a construct in
// the Blueprint language.
//
// The AST is the intermediate representation produced by the parser and
// consumed by the evaluator. It preserves the syntactic structure and source
// position information needed for accurate error reporting.
//
// AST node categories:
//   - Files: File (root node containing definitions)
//   - Definitions: Module, Assignment (top-level constructs)
//   - Expressions: String, Int64, Bool, List, Map, Variable, Operator, Select, Unset
//   - Supporting: Property, Map (key-value structures)
//
// All expression types implement the Expression interface:
//   - String(): Returns string representation for debugging
//   - Pos(): Returns source position for error reporting
//
// Design notes:
//   - Properties are stored in ordered slices to preserve source order
//   - Position information is kept for error messages
//   - Select has dedicated condition and case structures for conditional evaluation
//   - The AST is mutable - nodes may be modified during evaluation
package parser

import (
	"strconv"
	"text/scanner"
)

// Expression is the interface for all AST nodes that can appear in expressions.
// All expression types implement this interface to provide their source position
// and string representation. This allows the parser and evaluator to handle
// different expression types uniformly.
//
// The Expression interface enables:
//   - Unified string representation for debugging
//   - Source position for error reporting
//   - Generic expression handling in the evaluator
//
// Implementers:
//   - String, Int64, Bool: Literal values
//   - List, Map: Composite types
//   - Variable: Variable reference
//   - Operator: Binary operation
//   - Select: Conditional expression
//   - Unset: Unset keyword
type Expression interface {
	String() string        // Returns a string representation of the expression
	Pos() scanner.Position // Returns the source position of this expression
}

// Module represents a module definition like cc_binary { ... } or cc_library { ... }.
// A module has a type (e.g., "cc_binary", "cc_library") and a map of properties.
// It can also have architecture-specific (arch), host-specific (host), and
// target-specific (target) property overrides that apply to different
// build configurations.
//
// Modules are the primary building block in Blueprint. Each module
// represents a compiled artifact or other buildable target.
type Module struct {
	Type     string           // Module type name (e.g., "cc_binary", "cc_library")
	TypePos  scanner.Position // Source position of the type name
	Map      *Map             // Main property map (name: value, ...)
	Arch     map[string]*Map  // Arch-specific overrides: arch name -> properties
	Host     *Map             // Host-specific overrides (for host: {...})
	Target   *Map             // Target-specific overrides (for target: {...})
	Multilib map[string]*Map  // Multilib overrides: "lib32"/"lib64" -> properties
	Override bool             // True if this module has override: true
}

// Pos returns the source position of the module type.
// Used for error reporting and source navigation.
func (m *Module) Pos() scanner.Position { return m.TypePos }

// String returns a string representation of the module (type + properties).
// Format: "type { prop1: value1, prop2: value2, ... }"
func (m *Module) String() string {
	return m.Type + " " + m.Map.String()
}

// Map represents a property list { name: value, name: value, ... }.
// Maps are used for module properties and nested property structures.
// They consist of an ordered list of Property nodes, preserving the
// order in which properties were defined in the source.
type Map struct {
	Properties []*Property          // Ordered list of properties in the map
	LBracePos  scanner.Position     // Position of the opening { brace
	RBracePos  scanner.Position     // Position of the closing } brace
	propMap    map[string]*Property // Lazily initialized cache for O(1) lookup
}

// Pos returns the position of the opening brace.
// Used for error reporting.
func (m *Map) Pos() scanner.Position { return m.LBracePos }

// GetPropMap returns a map of property names to Property pointers for O(1) lookup.
// The map is lazily initialized and cached. Subsequent calls return the cached map.
func (m *Map) GetPropMap() map[string]*Property {
	if m.propMap == nil {
		m.propMap = make(map[string]*Property, len(m.Properties))
		for _, prop := range m.Properties {
			m.propMap[prop.Name] = prop
		}
	}
	return m.propMap
}

// String returns a string representation of the map.
// Format: "{ prop1: value1, prop2: value2, ... }"
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
// Properties are the fundamental way to configure modules in Blueprint.
type Property struct {
	Name     string           // Property name (left side of colon)
	NamePos  scanner.Position // Position of the property name
	Value    Expression       // Property value (right side of colon)
	ColonPos scanner.Position // Position of the colon separator
}

// String returns a string representation.
// Format: "name: value"
func (p *Property) String() string {
	return p.Name + ": " + p.Value.String()
}

// String represents a string literal like "hello" or `raw string`.
// String literals can contain escape sequences and are unquoted during parsing.
// They support Go-compatible escape sequences like \n, \t, \\, etc.
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
// They are used for numeric properties like version numbers,
// timeouts, and other numeric configurations.
type Int64 struct {
	Value      int64            // The integer value
	LiteralPos scanner.Position // Position of the integer literal in source
}

// Pos returns the position of the integer literal.
func (i *Int64) Pos() scanner.Position { return i.LiteralPos }

// String returns the string representation of the integer.
func (i *Int64) String() string { return strconv.FormatInt(i.Value, 10) }

// Bool represents a boolean literal: true or false.
// Booleans are used for flags and conditional properties.
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
// They are used for source file lists, library dependencies, and
// other multi-valued properties.
type List struct {
	Values    []Expression     // Ordered list of expressions
	LBracePos scanner.Position // Position of the opening [ bracket
	RBracePos scanner.Position // Position of the closing ] bracket
}

// Pos returns the position of the opening bracket.
func (l *List) Pos() scanner.Position { return l.LBracePos }

// String returns a string representation.
// Format: "[ value1, value2, ... ]"
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
// Variables are defined using assignment statements (my_var = value)
// and can be referenced in expressions throughout the Blueprint file.
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
// Simple assignments set a variable to a value, while concatenative
// assignments append to an existing list or string.
//
// Assignments are processed before modules, allowing variables to be
// used in module property values.
type Assignment struct {
	Name      string           // Variable name being assigned
	NamePos   scanner.Position // Position of the variable name
	EqualsPos scanner.Position // Position of the = or += operator
	Assigner  string           // The assignment operator: "=" or "+="
	Value     Expression       // The value being assigned
}

// Pos returns the position of the variable name.
func (a *Assignment) Pos() scanner.Position { return a.NamePos }

// String returns a string representation.
// Format: "foo = value" or "foo += value"
func (a *Assignment) String() string {
	return a.Name + " " + a.Assigner + " " + a.Value.String()
}

// Operator represents a binary operation: left op right.
// Currently only the + (concatenation/addition) operator is supported.
// The + operator performs different operations based on operand types:
// - int64 + int64: integer addition
// - string + string: string concatenation
// - list + list: list concatenation
// - list + scalar: append scalar to list
// - map + map: map merge with recursive value merging
type Operator struct {
	Args        [2]Expression    // Left and right operands
	Operator    rune             // The operator rune ('+' for now)
	OperatorPos scanner.Position // Position of the operator
}

// Pos returns the position of the operator.
func (o *Operator) Pos() scanner.Position { return o.OperatorPos }

// String returns a string representation.
// Format: "a + b"
func (o *Operator) String() string {
	return o.Args[0].String() + " " + string(o.Operator) + " " + o.Args[1].String()
}

// Select represents a conditional expression: select(condition, { cases }).
// Select chooses different values based on configuration (arch, os, host, target).
// It evaluates the condition and returns the value from the matching case.
//
// The select() function enables architecture-specific and platform-specific
// configuration, allowing a single property to have different values
// depending on the build target.
//
// Structure:
//
//	select(condition, {
//	    pattern1: value1,
//	    pattern2: value2,
//	    default: default_value
//	})
type Select struct {
	KeywordPos scanner.Position        // Position of the "select" keyword
	Conditions []ConfigurableCondition // Condition expressions (arch, os, host, etc.)
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
//
// Built-in conditions:
//   - arch(): Current architecture (arm, arm64, x86, x86_64)
//   - os(): Operating system (linux, android, darwin)
//   - host(): Host platform (darwin, linux, windows)
//   - target(): Target OS
//   - variant(): Build variant (debug, release)
//   - product_variable(name): Product variable value
//   - soong_config_variable(ns, var): Soong config variable
//   - release_flag(name): Check release flag
type ConfigurableCondition struct {
	Position     scanner.Position // Position of the condition
	FunctionName string           // The function/identifier name (empty for direct value)
	Args         []Expression     // Arguments to the function (if any)
}

// SelectCase represents a single case in a select expression.
// A case has one or more patterns that map to a single value.
// Multiple patterns can share the same value (e.g., "linux", "android": value).
// The special patterns "default" and "any" provide fallback and wildcard matching.
type SelectCase struct {
	Patterns []SelectPattern  // List of patterns that map to this value
	ColonPos scanner.Position // Position of the colon after patterns
	Value    Expression       // The value returned when any pattern matches
}

// SelectPattern represents a single pattern in a select case.
// A pattern is compared against the condition value to determine if this case matches.
// It also supports the "any @ var" binding syntax for wildcard pattern matching,
// where the matched value is bound to a variable for use in the value expression.
type SelectPattern struct {
	Value   Expression // The pattern expression (string, int, bool, variable, unset)
	IsAny   bool       // True if this is an "any" wildcard pattern
	Binding string     // Variable name to bind the matched value to (for "any @ var" syntax)
}

// Unset represents the unset keyword in a select statement.
// When a select branch evaluates to Unset, the property is treated as if it was never assigned.
// This is useful for removing inherited property values in variants.
type Unset struct {
	KeywordPos scanner.Position // Position of the "unset" keyword
}

// Pos returns the position of the unset keyword.
func (u *Unset) Pos() scanner.Position { return u.KeywordPos }

// String returns "unset".
func (u *Unset) String() string { return "unset" }

// ExecScript represents an exec_script() call for running scripts during configuration.
// The script is executed during Blueprint parsing/evaluation phase (not ninja build phase),
// and its output (stdout) is captured and used as the expression value.
// Supports JSON parsing for structured output.
//
// Usage in Blueprint:
//
//	value = exec_script("detect_arch.sh")
//	cflags: ["-DARCH=" + exec_script("get_flag.sh", "arg1")]
//
// The output is trimmed. If the output is valid JSON, it can be parsed into
// structured data (string, number, boolean, list, map).
type ExecScript struct {
	KeywordPos scanner.Position // Position of "exec_script" keyword
	Command    Expression       // Script path/command (string expression)
	Args       []Expression     // Arguments to the script (list of string expressions)
}

// Pos returns the position of the exec_script keyword.
func (e *ExecScript) Pos() scanner.Position { return e.KeywordPos }

// String returns "exec_script" as the representation.
func (e *ExecScript) String() string { return "exec_script" }

// File represents a parsed Blueprint file.
// It contains a list of definitions (modules and assignments) found
// at the top level of the file.
type File struct {
	Name string       // File name (for error reporting)
	Defs []Definition // List of top-level definitions in the file
}

// Definition is either a Module or an Assignment.
// This interface is implemented by both types to allow unified handling
// of top-level definitions in a file.
//
// The Definition interface is used internally to distinguish
// between the two main constructs in Blueprint files.
type Definition interface {
	def() // Private method to implement the interface
}

// def implements the Definition interface for Module.
// Module is a top-level module definition.
func (m *Module) def() {}

// def implements the Definition interface for Assignment.
// Assignment is a variable assignment statement.
func (a *Assignment) def() {}
