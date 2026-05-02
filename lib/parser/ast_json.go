// Package parser provides lexical analysis and parsing for Blueprint build definitions.
// AST JSON subpackage - JSON serialization/deserialization for AST nodes.
//
// This file implements JSON serialization and deserialization for all AST node types.
// It enables incremental parsing by caching parsed Blueprint files in JSON format.
//
// Core design decisions:
//   - Use intermediate structs (e.g., ModuleJSON) to avoid infinite recursion in MarshalJSON/UnmarshalJSON methods
//   - Convert position info (scanner.Position) to "file:line:column" format strings
//   - Add "type" field to JSON to support polymorphic deserialization
//   - Determine concrete expression type based on "type" field during deserialization
//
// Supported JSON serialization types:
//   - Top-level definitions: File, Module, Assignment
//   - Expressions: String, Int64, Bool, List, Variable, Operator, Select, Unset
//   - Auxiliary structures: Property, Map, SelectCase, SelectPattern, ConfigurableCondition
//
// JSON format serves as an intermediate cache format, stored in the .minibp/json/ directory,
// to avoid re-parsing unchanged .bp files.
//
// Usage example:
//
//	// Serialize module to JSON
//	module := &Module{Type: "cc_library", ...}
//	data, err := json.Marshal(module)
//
//	// Deserialize JSON to module
//	var module Module
//	err := json.Unmarshal(data, &module)
package parser

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"text/scanner"
)

// propertyJSONPool pools intermediate structs for Property.MarshalJSON to reduce allocations.
var propertyJSONPool = sync.Pool{
	New: func() interface{} {
		return &struct {
			Name     string     `json:"name"`
			NamePos  string     `json:"name_pos"`
			Value    Expression `json:"value"`
			ColonPos string     `json:"colon_pos"`
		}{}
	},
}

// ==================== JSON Struct Definitions: For Marshal/Unmarshal ====================

// ModuleJSON is the intermediate JSON representation struct for Module.
//
// Reasons for using an intermediate struct instead of serializing Module directly:
//   - Avoid infinite recursion in MarshalJSON method
//   - Convert scanner.Position to string format for easy JSON storage
//   - Explicitly specify JSON field names and serialization behavior
//
// JSON field descriptions:
//   - type: Module type name (e.g., "cc_library", "java_library")
//   - type_pos: Position of type name in source code ("file:line:col")
//   - map: Main property map (contains module's default properties)
//   - arch: Architecture-specific property overrides (e.g., arm64, arm, x86_64)
//   - host: Host-specific property overrides
//   - target: Target-specific property overrides
//   - multilib: Multilib property overrides (e.g., both, first, prefer32)
//   - override: Whether to override existing module definition
type ModuleJSON struct {
	Type     string          `json:"type"`               // Module type name (e.g., "cc_library")
	TypePos  string          `json:"type_pos"`           // Position of type name as "file:line:col"
	Map      *Map            `json:"map,omitempty"`      // Main property map
	Arch     map[string]*Map `json:"arch,omitempty"`     // Arch-specific overrides
	Host     *Map            `json:"host,omitempty"`     // Host-specific overrides
	Target   *Map            `json:"target,omitempty"`   // Target-specific overrides
	Multilib map[string]*Map `json:"multilib,omitempty"` // Multilib overrides
	Override bool            `json:"override,omitempty"` // Override flag
}

// MarshalJSON implements json.Marshaler for Module.
//
// Description:
//
//	Serializes the Module struct to JSON byte array.
//	Uses intermediate struct ModuleJSON to avoid infinite recursion.
//
// How it works:
//  1. Create ModuleJSON intermediate struct and copy all fields
//  2. Convert scanner.Position fields to strings via posToString()
//  3. Call json.Marshal() to serialize the intermediate struct
//
// Key conversions:
//   - scanner.Position -> "file:line:column" string (via posToString())
//   - All fields are copied to JSON struct for serialization
//   - Arch, Host, Target, Multilib maps are fully serialized
//
// Parameters:
//   - No explicit parameters, receiver is *Module
//
// Returns:
//   - []byte: JSON representation of Module
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - When m is nil, json.Marshal returns "null"
//   - Empty string fields are serialized as "" in JSON
//   - nil Map or Arch maps are omitted (omitempty)
//   - Override is not serialized when false (omitempty)
//
// Example JSON output:
//
//	{
//	  "type": "cc_library",
//	  "type_pos": "Android.bp:5:1",
//	  "map": {...},
//	  "arch": {...}
//	}
func (m *Module) MarshalJSON() ([]byte, error) {
	return json.Marshal(ModuleJSON{
		Type:     m.Type,
		TypePos:  posToString(m.TypePos),
		Map:      m.Map,
		Arch:     m.Arch,
		Host:     m.Host,
		Target:   m.Target,
		Multilib: m.Multilib,
		Override: m.Override,
	})
}

// UnmarshalJSON implements json.Unmarshaler for Module.
//
// Description:
//
//	Deserializes JSON byte array to Module struct.
//	Uses intermediate struct ModuleJSON to avoid infinite recursion.
//
// How it works:
//  1. Create ModuleJSON intermediate struct
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Copy fields from intermediate struct back to Module receiver
//  4. Convert position strings back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Invalid JSON format returns parse error
//   - Missing "type" field results in Module.Type being empty string
//   - Invalid position string format causes stringToPos() to return zero-value scanner.Position{}
//   - nil Map or map fields remain nil
//   - Boolean field false is correctly deserialized
func (m *Module) UnmarshalJSON(data []byte) error {
	var aux ModuleJSON
	if err := json.Unmarshal(data, &aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	m.Type = aux.Type
	m.TypePos = stringToPos(aux.TypePos)
	m.Map = aux.Map
	m.Arch = aux.Arch
	m.Host = aux.Host
	m.Target = aux.Target
	m.Multilib = aux.Multilib
	m.Override = aux.Override
	return nil
}

// MapJSON is the intermediate JSON representation struct for Map.
//
// Map represents a property map in a Blueprint module (key-value pairs within braces).
// Example: { name: "foo", srcs: ["a.c"], cflags: "-Wall" }
//
// JSON field descriptions:
//   - properties: Property list, ordered as they appear in source code
//   - lbrace_pos: Position of left brace "{" ("file:line:col")
//   - rbrace_pos: Position of right brace "}" ("file:line:col")
type MapJSON struct {
	Properties []*Property `json:"properties"`
	LBracePos  string      `json:"lbrace_pos"`
	RBracePos  string      `json:"rbrace_pos"`
}

// MarshalJSON implements json.Marshaler for Map.
//
// Description:
//
//	Serializes Map (property map) to JSON byte array.
//	Map represents a brace-enclosed property collection in Blueprint.
//
// How it works:
//  1. Create MapJSON intermediate struct
//  2. Copy Properties list and position info
//  3. Convert position fields via posToString()
//  4. Call json.Marshal() to serialize
//
// Parameters:
//   - No explicit parameters, receiver is *Map
//
// Returns:
//   - []byte: JSON representation of Map
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Empty Properties slice results in "properties": [] in JSON
//   - Zero-value position fields are converted to empty string
//   - nil Properties slice is serialized as null
func (m *Map) MarshalJSON() ([]byte, error) {
	return json.Marshal(MapJSON{
		Properties: m.Properties,
		LBracePos:  posToString(m.LBracePos),
		RBracePos:  posToString(m.RBracePos),
	})
}

// UnmarshalJSON implements json.Unmarshaler for Map.
//
// Description:
//
//	Deserializes JSON byte array to Map (property map).
//
// How it works:
//  1. Create MapJSON intermediate struct
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Copy Properties list
//  4. Convert position strings back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Invalid JSON format returns parse error
//   - Invalid position string format returns zero-value position
//   - Properties is null results in m.Properties being nil
func (m *Map) UnmarshalJSON(data []byte) error {
	var aux MapJSON
	if err := json.Unmarshal(data, &aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	m.Properties = aux.Properties
	m.LBracePos = stringToPos(aux.LBracePos)
	m.RBracePos = stringToPos(aux.RBracePos)
	return nil
}

// MarshalJSON implements json.Marshaler for Property.
//
// Description:
//
//	Serializes Property (key-value pair) to JSON byte array.
//	Property represents an attribute in Blueprint, such as "name: "value"" or "srcs: ["a.c"]".
//
// How it works:
//  1. Use anonymous struct as intermediate representation
//  2. Copy property name, position, and value
//  3. Convert position fields via posToString()
//  4. Value field is Expression interface type, its MarshalJSON method will be called
//
// Parameters:
//   - No explicit parameters, receiver is *Property
//
// Returns:
//   - []byte: JSON representation of Property
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - When Value is nil, "value": null in JSON
//   - Zero-value position fields are converted to empty string
//   - Property name can be empty string (though uncommon)
//
// Example JSON output:
//
//	{
//	  "name": "srcs",
//	  "name_pos": "Android.bp:10:5",
//	  "value": {"type": "list", "values": [...]},
//	  "colon_pos": "Android.bp:10:9"
//	}
func (p *Property) MarshalJSON() ([]byte, error) {
	// Get intermediate struct from pool to reduce allocations
	obj := propertyJSONPool.Get().(*struct {
		Name     string     `json:"name"`
		NamePos  string     `json:"name_pos"`
		Value    Expression `json:"value"`
		ColonPos string     `json:"colon_pos"`
	})
	defer propertyJSONPool.Put(obj)

	// Populate fields
	obj.Name = p.Name
	obj.NamePos = posToString(p.NamePos)
	obj.Value = p.Value
	obj.ColonPos = posToString(p.ColonPos)

	return json.Marshal(obj)
}

// UnmarshalJSON implements json.Unmarshaler for Property.
//
// Description:
//
//	Deserializes JSON byte array to Property (key-value pair).
//	Note: Value field uses json.RawMessage for deferred parsing to support polymorphic expression types.
//
// How it works:
//  1. Use anonymous intermediate struct with Value field as json.RawMessage
//  2. Call json.Unmarshal() to deserialize basic fields
//  3. Call unmarshalExpression() to parse concrete expression type based on "type" field
//  4. Convert position strings back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Value field is null or missing results in p.Value being nil
//   - Unknown expression types are handled as nil by unmarshalExpression
//   - Invalid position string format returns zero-value position
//   - Invalid JSON format returns parse error
func (p *Property) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Name     string          `json:"name"`
		NamePos  string          `json:"name_pos"`
		Value    json.RawMessage `json:"value"`
		ColonPos string          `json:"colon_pos"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	p.Name = aux.Name
	p.NamePos = stringToPos(aux.NamePos)
	p.ColonPos = stringToPos(aux.ColonPos)
	expr, err := unmarshalExpression(aux.Value)
	if err != nil { // Propagate error from expression deserialization
		return err
	}
	p.Value = expr
	return nil
}

// MarshalJSON implements json.Marshaler for String.
//
// Description:
//
//	Serializes String expression to JSON byte array.
//	String represents a string literal in Blueprint, such as "hello" or "src/main.c".
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "string" field
//  2. Copy string value and literal position
//  3. Convert position fields via posToString()
//  4. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "string",
//	  "value": "hello world",
//	  "literal_pos": "Android.bp:10:15"
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *String
//
// Returns:
//   - []byte: JSON representation of String expression
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Empty string is serialized as "value": ""
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish expression types during deserialization
func (s *String) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       string `json:"type"`
		Value      string `json:"value"`
		LiteralPos string `json:"literal_pos"`
	}{
		Type:       "string",
		Value:      s.Value,
		LiteralPos: posToString(s.LiteralPos),
	})
}

// UnmarshalJSON implements json.Unmarshaler for String.
//
// Description:
//
//	Deserializes JSON byte array to String expression.
//	Restores string value and literal position info from JSON.
//
// How it works:
//  1. Use anonymous intermediate struct without "type" field (checked in outer layer)
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Convert position string back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array, should contain "value" and "literal_pos" fields
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Missing "value" field results in s.Value being empty string
//   - Invalid "literal_pos" format returns zero-value position
//   - Invalid JSON format returns parse error
func (s *String) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Value      string `json:"value"`
		LiteralPos string `json:"literal_pos"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	s.Value = aux.Value
	s.LiteralPos = stringToPos(aux.LiteralPos)
	return nil
}

// MarshalJSON implements json.Marshaler for Int64.
//
// Description:
//
//	Serializes Int64 expression to JSON byte array.
//	Int64 represents an integer literal in Blueprint, such as 42 or 100.
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "int64" field
//  2. Copy integer value and literal position
//  3. Convert position fields via posToString()
//  4. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "int64",
//	  "value": 42,
//	  "literal_pos": "Android.bp:15:20"
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *Int64
//
// Returns:
//   - []byte: JSON representation of Int64 expression
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Zero value 0 is serialized normally
//   - Negative numbers are correctly serialized as JSON negative numbers
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish expression types during deserialization
func (i *Int64) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       string `json:"type"`
		Value      int64  `json:"value"`
		LiteralPos string `json:"literal_pos"`
	}{
		Type:       "int64",
		Value:      i.Value,
		LiteralPos: posToString(i.LiteralPos),
	})
}

// UnmarshalJSON implements json.Unmarshaler for Int64.
//
// Description:
//
//	Deserializes JSON byte array to Int64 expression.
//	Restores integer value and literal position info from JSON.
//
// How it works:
//  1. Use anonymous intermediate struct without "type" field (checked in outer layer)
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Convert position string back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array, should contain "value" and "literal_pos" fields
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Missing "value" field results in i.Value being 0 (int64 zero value)
//   - Invalid "literal_pos" format returns zero-value position
//   - Invalid JSON number format returns parse error
func (i *Int64) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Value      int64  `json:"value"`
		LiteralPos string `json:"literal_pos"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	i.Value = aux.Value
	i.LiteralPos = stringToPos(aux.LiteralPos)
	return nil
}

// MarshalJSON implements json.Marshaler for Bool.
//
// Description:
//
//	Serializes Bool expression to JSON byte array.
//	Bool represents a boolean literal in Blueprint, such as true or false.
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "bool" field
//  2. Copy boolean value and literal position
//  3. Convert position fields via posToString()
//  4. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "bool",
//	  "value": true,
//	  "literal_pos": "Android.bp:20:25"
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *Bool
//
// Returns:
//   - []byte: JSON representation of Bool expression
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - false value is serialized as JSON false normally
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish expression types during deserialization
func (b *Bool) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       string `json:"type"`
		Value      bool   `json:"value"`
		LiteralPos string `json:"literal_pos"`
	}{
		Type:       "bool",
		Value:      b.Value,
		LiteralPos: posToString(b.LiteralPos),
	})
}

// UnmarshalJSON implements json.Unmarshaler for Bool.
//
// Description:
//
//	Deserializes JSON byte array to Bool expression.
//	Restores boolean value and literal position info from JSON.
//
// How it works:
//  1. Use anonymous intermediate struct without "type" field (checked in outer layer)
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Convert position string back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array, should contain "value" and "literal_pos" fields
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Missing "value" field results in b.Value being false (bool zero value)
//   - Invalid "literal_pos" format returns zero-value position
//   - Invalid JSON boolean format returns parse error
func (b *Bool) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Value      bool   `json:"value"`
		LiteralPos string `json:"literal_pos"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	b.Value = aux.Value
	b.LiteralPos = stringToPos(aux.LiteralPos)
	return nil
}

// MarshalJSON implements json.Marshaler for List.
//
// Description:
//
//	Serializes List expression to JSON byte array.
//	List represents a list literal in Blueprint, such as ["a.c", "b.c"] or [1, 2, 3].
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "list" field
//  2. Copy list values and brace positions
//  3. Values field is Expression interface slice, each element's MarshalJSON method will be called
//  4. Convert position fields via posToString()
//  5. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "list",
//	  "values": [
//	    {"type": "string", "value": "a.c", ...},
//	    {"type": "string", "value": "b.c", ...}
//	  ],
//	  "lbrace_pos": "Android.bp:25:10",
//	  "rbrace_pos": "Android.bp:25:30"
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *List
//
// Returns:
//   - []byte: JSON representation of List expression
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Empty list is serialized as "values": []
//   - nil elements in Values are serialized as null
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish expression types during deserialization
func (l *List) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type      string       `json:"type"`
		Values    []Expression `json:"values"`
		LBracePos string       `json:"lbrace_pos"`
		RBracePos string       `json:"rbrace_pos"`
	}{
		Type:      "list",
		Values:    l.Values,
		LBracePos: posToString(l.LBracePos),
		RBracePos: posToString(l.RBracePos),
	})
}

// UnmarshalJSON implements json.Unmarshaler for List.
//
// Description:
//
//	Deserializes JSON byte array to List expression.
//	Note: Values field uses []json.RawMessage for deferred parsing to support polymorphic expression types.
//
// How it works:
//  1. Use anonymous intermediate struct with Values field as []json.RawMessage
//  2. Call json.Unmarshal() to deserialize basic fields
//  3. Iterate through Values, call unmarshalExpression() for each element to parse concrete expression type
//  4. Convert position strings back to scanner.Position via stringToPos()
//  5. Pre-allocate Values slice length to avoid multiple allocations
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - "values" field is null or missing results in l.Values being nil
//   - "values" is empty array results in l.Values being empty slice (length 0)
//   - Unknown expression types are handled as nil by unmarshalExpression
//   - Invalid position string format returns zero-value position
//   - Any Value element parse failure returns error immediately
func (l *List) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Values    []json.RawMessage `json:"values"`
		LBracePos string            `json:"lbrace_pos"`
		RBracePos string            `json:"rbrace_pos"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	l.LBracePos = stringToPos(aux.LBracePos)
	l.RBracePos = stringToPos(aux.RBracePos)
	// Pre-allocate slice with exact capacity to avoid multiple allocations during append.
	// Each element will be populated by unmarshalExpression which handles polymorphic types.
	l.Values = make([]Expression, len(aux.Values))
	for i, raw := range aux.Values {
		expr, err := unmarshalExpression(raw)
		if err != nil { // Propagate parse error; partial list is invalid
			return err
		}
		l.Values[i] = expr
	}
	return nil
}

// MarshalJSON implements json.Marshaler for Variable.
//
// Description:
//
//	Serializes Variable expression to JSON byte array.
//	Variable represents a variable reference in Blueprint, such as ${my_var} or $my_var.
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "variable" field
//  2. Copy variable name and name position
//  3. Convert position fields via posToString()
//  4. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "variable",
//	  "name": "my_var",
//	  "name_pos": "Android.bp:30:10"
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *Variable
//
// Returns:
//   - []byte: JSON representation of Variable expression
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Variable name can be empty string (though uncommon)
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish expression types during deserialization
func (v *Variable) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		NamePos string `json:"name_pos"`
	}{
		Type:    "variable",
		Name:    v.Name,
		NamePos: posToString(v.NamePos),
	})
}

// UnmarshalJSON implements json.Unmarshaler for Variable.
//
// Description:
//
//	Deserializes JSON byte array to Variable expression.
//	Restores variable name and name position info from JSON.
//
// How it works:
//  1. Use anonymous intermediate struct without "type" field (checked in outer layer)
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Convert position string back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array, should contain "name" and "name_pos" fields
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Missing "name" field results in v.Name being empty string
//   - Invalid "name_pos" format returns zero-value position
//   - Invalid JSON format returns parse error
func (v *Variable) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Name    string `json:"name"`
		NamePos string `json:"name_pos"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	v.Name = aux.Name
	v.NamePos = stringToPos(aux.NamePos)
	return nil
}

// MarshalJSON implements json.Marshaler for Assignment.
//
// Description:
//
//	Serializes Assignment (assignment statement) to JSON byte array.
//	Assignment represents variable assignment in Blueprint, such as "my_var = "value"" or "flags += "-Wall"".
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "assignment" field
//  2. Copy assignment name, positions, assignment operator, and value
//  3. Value field is Expression interface type, its MarshalJSON method will be called
//  4. Convert position fields via posToString()
//  5. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "assignment",
//	  "name": "my_var",
//	  "name_pos": "Android.bp:35:1",
//	  "equals_pos": "Android.bp:35:8",
//	  "assigner": "=",
//	  "value": {"type": "string", "value": "hello", ...}
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *Assignment
//
// Returns:
//   - []byte: JSON representation of Assignment
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Assigner can be "=", "+=", "-=", etc.
//   - When Value is nil, "value": null in JSON
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish definition types during deserialization
func (a *Assignment) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type      string     `json:"type"`
		Name      string     `json:"name"`
		NamePos   string     `json:"name_pos"`
		EqualsPos string     `json:"equals_pos"`
		Assigner  string     `json:"assigner"`
		Value     Expression `json:"value"`
	}{
		Type:      "assignment",
		Name:      a.Name,
		NamePos:   posToString(a.NamePos),
		EqualsPos: posToString(a.EqualsPos),
		Assigner:  a.Assigner,
		Value:     a.Value,
	})
}

// UnmarshalJSON implements json.Unmarshaler for Assignment.
//
// Description:
//
//	Deserializes JSON byte array to Assignment (assignment statement).
//	Note: Value field uses json.RawMessage for deferred parsing to support polymorphic expression types.
//
// How it works:
//  1. Use anonymous intermediate struct with Value field as json.RawMessage
//  2. Call json.Unmarshal() to deserialize basic fields
//  3. Call unmarshalExpression() to parse concrete expression type based on "type" field
//  4. Convert position strings back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Value field is null or missing results in a.Value being nil
//   - Unknown expression types are handled as nil by unmarshalExpression
//   - Missing Assigner field keeps zero value (empty string)
//   - Invalid position string format returns zero-value position
func (a *Assignment) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Name      string          `json:"name"`
		NamePos   string          `json:"name_pos"`
		EqualsPos string          `json:"equals_pos"`
		Assigner  string          `json:"assigner"`
		Value     json.RawMessage `json:"value"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	a.Name = aux.Name
	a.NamePos = stringToPos(aux.NamePos)
	a.EqualsPos = stringToPos(aux.EqualsPos)
	a.Assigner = aux.Assigner
	expr, err := unmarshalExpression(aux.Value)
	if err != nil { // Propagate error from expression deserialization
		return err
	}
	a.Value = expr
	return nil
}

// MarshalJSON implements json.Marshaler for Operator.
//
// Description:
//
//	Serializes Operator expression to JSON byte array.
//	Operator represents a binary operator expression in Blueprint, such as "a + b" or "x == y".
//
// Supported operators:
//   - Arithmetic: + (add), - (subtract)
//   - Comparison: == (equal), != (not equal), < (less), <= (less or equal), > (greater), >= (greater or equal)
//   - Logical: && (and), || (or)
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "operator" field
//  2. Copy operator and the two argument expressions
//  3. Args is a [2]Expression array, each element's MarshalJSON method will be called
//  4. Convert operator position field via posToString()
//  5. Call json.Marshal() to serialize
//
// JSON format:
//
//		{
//		  "type": "operator",
//		  "args": [
//		    {"type": "string", "value": "a", ...},
//		    {"type": "string", "value": "b", ...}
//		  ],
//		  "operator": 43,
//		  "operator_pos": "Android.bp:40:10"
//		}
//	  Note: The operator field 43 is the ASCII code value of '+'.
//
// Parameters:
//   - No explicit parameters, receiver is *Operator
//
// Returns:
//   - []byte: JSON representation of Operator expression
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Args array has fixed length of 2, even if an argument is nil it will be serialized
//   - nil arguments are serialized as null
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish expression types during deserialization
func (o *Operator) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type        string        `json:"type"`
		Args        [2]Expression `json:"args"`
		Operator    rune          `json:"operator"`
		OperatorPos string        `json:"operator_pos"`
	}{
		Type:        "operator",
		Args:        o.Args,
		Operator:    o.Operator,
		OperatorPos: posToString(o.OperatorPos),
	})
}

// UnmarshalJSON implements json.Unmarshaler for Operator.
//
// Description:
//
//	Deserializes JSON byte array to Operator expression.
//	Note: Args field uses [2]json.RawMessage for deferred parsing to support polymorphic expression types.
//
// How it works:
//  1. Use anonymous intermediate struct with Args field as [2]json.RawMessage
//  2. Call json.Unmarshal() to deserialize basic fields
//  3. Iterate through Args array, call unmarshalExpression() for each element to parse concrete expression type
//  4. Convert position string back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - When Args array length is not 2, only the first two elements are processed
//   - Unknown expression types are handled as nil by unmarshalExpression
//   - Invalid position string format returns zero-value position
//   - Any Args element parse failure returns error immediately
//   - Operator field is rune type, storing the ASCII code value of the operator
func (o *Operator) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Args        [2]json.RawMessage `json:"args"`
		Operator    rune               `json:"operator"`
		OperatorPos string             `json:"operator_pos"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	o.Operator = aux.Operator
	o.OperatorPos = stringToPos(aux.OperatorPos)
	for i, raw := range aux.Args {
		expr, err := unmarshalExpression(raw)
		if err != nil { // Propagate error; partial operator is invalid
			return err
		}
		o.Args[i] = expr
	}
	return nil
}

// MarshalJSON implements json.Marshaler for Select.
//
// Description:
//
//	Serializes Select expression to JSON byte array.
//	Select represents a conditional selection expression in Blueprint, such as select(condition, { pattern: value, ... }).
//	Used to select different property values based on conditions (e.g., architecture, OS).
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "select" field
//  2. Copy keyword position, condition list, brace positions, and case list
//  3. Conditions and Cases fields are serialized directly (they have their own JSON handling)
//  4. Convert position fields via posToString()
//  5. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "select",
//	  "keyword_pos": "Android.bp:45:5",
//	  "conditions": [...],
//	  "lbrace_pos": "Android.bp:45:30",
//	  "rbrace_pos": "Android.bp:50:1",
//	  "cases": [...]
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *Select
//
// Returns:
//   - []byte: JSON representation of Select expression
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - When Conditions or Cases is empty slice, JSON contains [] or is omitted
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish expression types during deserialization
func (s *Select) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       string                  `json:"type"`
		KeywordPos string                  `json:"keyword_pos"`
		Conditions []ConfigurableCondition `json:"conditions"`
		LBracePos  string                  `json:"lbrace_pos"`
		RBracePos  string                  `json:"rbrace_pos"`
		Cases      []SelectCase            `json:"cases"`
	}{
		Type:       "select",
		KeywordPos: posToString(s.KeywordPos),
		Conditions: s.Conditions,
		LBracePos:  posToString(s.LBracePos),
		RBracePos:  posToString(s.RBracePos),
		Cases:      s.Cases,
	})
}

// UnmarshalJSON implements json.Unmarshaler for Select.
//
// Description:
//
//	Deserializes JSON byte array to Select expression.
//	Restores all information of the conditional selection expression from JSON.
//
// How it works:
//  1. Use anonymous intermediate struct (without "type" field, checked in outer layer)
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Conditions and Cases fields are deserialized directly (they have their own JSON handling)
//  4. Convert position strings back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - When Conditions or Cases is null, corresponding fields are nil or empty slice
//   - Invalid position string format returns zero-value position
//   - Invalid JSON format returns parse error
func (s *Select) UnmarshalJSON(data []byte) error {
	aux := &struct {
		KeywordPos string                  `json:"keyword_pos"`
		Conditions []ConfigurableCondition `json:"conditions"`
		LBracePos  string                  `json:"lbrace_pos"`
		RBracePos  string                  `json:"rbrace_pos"`
		Cases      []SelectCase            `json:"cases"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	s.KeywordPos = stringToPos(aux.KeywordPos)
	s.Conditions = aux.Conditions
	s.LBracePos = stringToPos(aux.LBracePos)
	s.RBracePos = stringToPos(aux.RBracePos)
	s.Cases = aux.Cases
	return nil
}

// MarshalJSON implements json.Marshaler for ConfigurableCondition.
//
// Description:
//
//	Serializes ConfigurableCondition to JSON byte array.
//	ConfigurableCondition represents a condition configuration in select(), such as select(arch(), { "arm64": ... }).
//	Conditions can be function calls like arch(), os(), platform(), etc.
//
// How it works:
//  1. Use anonymous struct as intermediate representation
//  2. Copy position, function name, and argument list
//  3. Args field is Expression interface slice, each element's MarshalJSON method will be called
//  4. Convert position field via posToString()
//  5. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "position": "Android.bp:45:7",
//	  "function_name": "arch",
//	  "args": [
//	    {"type": "string", "value": "arm64", ...}
//	  ]
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *ConfigurableCondition
//
// Returns:
//   - []byte: JSON representation of ConfigurableCondition
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Args can be empty slice (condition function with no arguments)
//   - Zero-value position fields are converted to empty string
//   - Function name can be "arch", "os", "platform", etc.
func (c *ConfigurableCondition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Position     string       `json:"position"`
		FunctionName string       `json:"function_name"`
		Args         []Expression `json:"args"`
	}{
		Position:     posToString(c.Position),
		FunctionName: c.FunctionName,
		Args:         c.Args,
	})
}

// UnmarshalJSON implements json.Unmarshaler for ConfigurableCondition.
//
// Description:
//
//	Deserializes JSON byte array to ConfigurableCondition.
//	Note: Args field uses []json.RawMessage for deferred parsing to support polymorphic expression types.
//
// How it works:
//  1. Use anonymous intermediate struct with Args field as []json.RawMessage
//  2. Call json.Unmarshal() to deserialize basic fields
//  3. Iterate through Args, call unmarshalExpression() for each element to parse concrete expression type
//  4. Convert position string back to scanner.Position via stringToPos()
//  5. Pre-allocate Args slice length to avoid multiple allocations
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - "args" field is null or missing results in c.Args being nil
//   - "args" is empty array results in c.Args being empty slice (length 0)
//   - Unknown expression types are handled as nil by unmarshalExpression
//   - Invalid position string format returns zero-value position
//   - Any Args element parse failure returns error immediately
func (c *ConfigurableCondition) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Position     string            `json:"position"`
		FunctionName string            `json:"function_name"`
		Args         []json.RawMessage `json:"args"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	c.Position = stringToPos(aux.Position)
	c.FunctionName = aux.FunctionName
	// Pre-allocate Args slice to avoid multiple allocations.
	// Each argument is an Expression that needs polymorphic deserialization.
	c.Args = make([]Expression, len(aux.Args))
	for i, raw := range aux.Args {
		expr, err := unmarshalExpression(raw)
		if err != nil { // Propagate error; partial condition is invalid
			return err
		}
		c.Args[i] = expr
	}
	return nil
}

// MarshalJSON implements json.Marshaler for SelectCase.
//
// Description:
//
//	Serializes SelectCase (selection branch) to JSON byte array.
//	SelectCase represents a branch in a select() expression, such as "arm64": value.
//	Each branch contains a set of patterns (Patterns) and a corresponding value.
//
// How it works:
//  1. Use anonymous struct as intermediate representation
//  2. Copy pattern list, colon position, and value expression
//  3. Value field is Expression interface type, its MarshalJSON method will be called
//  4. Convert position field via posToString()
//  5. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "patterns": [{"value": ..., "is_any": false, ...}],
//	  "colon_pos": "Android.bp:46:15",
//	  "value": {"type": "string", "value": "arm64_value", ...}
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *SelectCase
//
// Returns:
//   - []byte: JSON representation of SelectCase
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - When Patterns is empty slice, JSON contains "patterns": []
//   - When Value is nil, JSON contains "value": null
//   - Zero-value position fields are converted to empty string
func (s *SelectCase) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Patterns []SelectPattern `json:"patterns"`
		ColonPos string          `json:"colon_pos"`
		Value    Expression      `json:"value"`
	}{
		Patterns: s.Patterns,
		ColonPos: posToString(s.ColonPos),
		Value:    s.Value,
	})
}

// UnmarshalJSON implements json.Unmarshaler for SelectCase.
//
// Description:
//
//	Deserializes JSON byte array to SelectCase (selection branch).
//	Note: Value field uses json.RawMessage for deferred parsing to support polymorphic expression types.
//
// How it works:
//  1. Use anonymous intermediate struct with Value field as json.RawMessage
//  2. Call json.Unmarshal() to deserialize basic fields
//  3. Call unmarshalExpression() to parse concrete expression type based on "type" field
//  4. Convert position string back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - "patterns" is null results in s.Patterns being nil
//   - Value field is null or missing results in s.Value being nil
//   - Unknown expression types are handled as nil by unmarshalExpression
//   - Invalid position string format returns zero-value position
func (s *SelectCase) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Patterns []SelectPattern `json:"patterns"`
		ColonPos string          `json:"colon_pos"`
		Value    json.RawMessage `json:"value"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	s.Patterns = aux.Patterns
	s.ColonPos = stringToPos(aux.ColonPos)
	expr, err := unmarshalExpression(aux.Value)
	if err != nil { // Propagate error from expression deserialization
		return err
	}
	s.Value = expr
	return nil
}

// MarshalJSON implements json.Marshaler for SelectPattern.
//
// Description:
//
//	Serializes SelectPattern (selection pattern) to JSON byte array.
//	SelectPattern represents a matching pattern in a select() branch, such as "arm64", * (any), or a bound variable.
//
// How it works:
//  1. Use anonymous struct as intermediate representation
//  2. Copy value expression, is-any flag, and binding name
//  3. Value field is Expression interface type, its MarshalJSON method will be called
//  4. Use omitempty tag to omit empty Binding field
//  5. Call json.Marshal() to serialize
//
// JSON format:
//
//		{
//		  "value": {"type": "string", "value": "arm64", ...},
//		  "is_any": false,
//		  "binding": "arch"
//		}
//	  Note: When is_any is true, it matches any value (wildcard)
//	  Note: Non-empty binding means the matched value is bound to a variable
//
// Parameters:
//   - No explicit parameters, receiver is *SelectPattern
//
// Returns:
//   - []byte: JSON representation of SelectPattern
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - When Value is nil (e.g., wildcard pattern), JSON contains "value": null
//   - When Binding is empty string, the field is omitted (omitempty)
//   - When is_any is false, the field is omitted (zero-value omission)
func (s *SelectPattern) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Value   Expression `json:"value"`
		IsAny   bool       `json:"is_any"`
		Binding string     `json:"binding,omitempty"`
	}{
		Value:   s.Value,
		IsAny:   s.IsAny,
		Binding: s.Binding,
	})
}

// UnmarshalJSON implements json.Unmarshaler for SelectPattern.
//
// Description:
//
//	Deserializes JSON byte array to SelectPattern (selection pattern).
//	Note: Value field uses json.RawMessage for deferred parsing to support polymorphic expression types.
//
// How it works:
//  1. Use anonymous intermediate struct with Value field as json.RawMessage
//  2. Call json.Unmarshal() to deserialize basic fields
//  3. Call unmarshalExpression() to parse concrete expression type based on "type" field
//  4. Restore IsAny and Binding flags
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Value field is null or missing results in s.Value being nil (wildcard pattern)
//   - Unknown expression types are handled as nil by unmarshalExpression
//   - Missing Binding field keeps zero value (empty string)
//   - is_any set to true means matches any value
func (s *SelectPattern) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Value   json.RawMessage `json:"value"`
		IsAny   bool            `json:"is_any"`
		Binding string          `json:"binding,omitempty"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	s.IsAny = aux.IsAny
	s.Binding = aux.Binding
	expr, err := unmarshalExpression(aux.Value)
	if err != nil { // Propagate error from expression deserialization
		return err
	}
	s.Value = expr
	return nil
}

// MarshalJSON implements json.Marshaler for Unset.
//
// Description:
//
//	Serializes Unset expression to JSON byte array.
//	Unset represents an unset value in Blueprint, used to clear previously defined property values.
//
// How it works:
//  1. Use anonymous struct as intermediate representation, add "type": "unset" field
//  2. Copy keyword position
//  3. Convert position field via posToString()
//  4. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "unset",
//	  "keyword_pos": "Android.bp:55:10"
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *Unset
//
// Returns:
//   - []byte: JSON representation of Unset expression
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Unset has no value field, only position info
//   - Zero-value position fields are converted to empty string
//   - "type" field is used to distinguish expression types during deserialization
func (u *Unset) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       string `json:"type"`
		KeywordPos string `json:"keyword_pos"`
	}{
		Type:       "unset",
		KeywordPos: posToString(u.KeywordPos),
	})
}

// UnmarshalJSON implements json.Unmarshaler for Unset.
//
// Description:
//
//	Deserializes JSON byte array to Unset expression.
//	Restores keyword position info from JSON.
//
// How it works:
//  1. Use anonymous intermediate struct (without "type" field, checked in outer layer)
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Convert position string back to scanner.Position via stringToPos()
//
// Parameters:
//   - data: JSON format byte array, should contain "keyword_pos" field
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Invalid "keyword_pos" format returns zero-value position
//   - Invalid JSON format returns parse error
//   - Unset has no value field, only position info
func (u *Unset) UnmarshalJSON(data []byte) error {
	aux := &struct {
		KeywordPos string `json:"keyword_pos"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	u.KeywordPos = stringToPos(aux.KeywordPos)
	return nil
}

// MarshalJSON implements json.Marshaler for ExecScript.
//
// Serializes ExecScript expression to JSON byte array.
// ExecScript represents a script execution during configuration phase.
//
// How it works:
//  1. Use anonymous struct as intermediate representation
//  2. Add "type": "exec_script" field to identify the expression type
//  3. Copy keyword position and command expression
//  4. Copy args slice (each element's MarshalJSON will be called)
//  5. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "type": "exec_script",
//	  "keyword_pos": "Android.bp:10:1",
//	  "command": {...},
//	  "args": [{...}, ...]
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *ExecScript
//
// Returns:
//   - []byte: JSON representation of ExecScript
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - Command field is serialized via its MarshalJSON method
//   - Args is a slice of Expression, each element's MarshalJSON will be called
//   - Empty args slice is serialized as []
//   - Position fields are converted to strings via posToString()
func (e *ExecScript) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type       string       `json:"type"`
		KeywordPos string       `json:"keyword_pos"`
		Command    Expression   `json:"command"`
		Args       []Expression `json:"args"`
	}{
		Type:       "exec_script",
		KeywordPos: posToString(e.KeywordPos),
		Command:    e.Command,
		Args:       e.Args,
	})
}

// UnmarshalJSON implements json.Unmarshaler for ExecScript.
//
// Deserializes JSON byte array to ExecScript expression.
// Restores keyword position and command/args expressions.
//
// How it works:
//  1. Use intermediate struct with raw message for command and args
//  2. Call json.Unmarshal() to deserialize into intermediate struct
//  3. Convert position string back to scanner.Position via stringToPos()
//  4. Use unmarshalExpression() to deserialize command and each arg
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - Invalid "keyword_pos" format returns zero-value position
//   - Invalid JSON format returns parse error
//   - Command and args are deserialized using unmarshalExpression()
func (e *ExecScript) UnmarshalJSON(data []byte) error {
	aux := &struct {
		KeywordPos string            `json:"keyword_pos"`
		Command    json.RawMessage   `json:"command"`
		Args       []json.RawMessage `json:"args"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	e.KeywordPos = stringToPos(aux.KeywordPos)

	// Deserialize command expression if present.
	// Command is optional in JSON (omitempty not used, but nil handled here).
	if len(aux.Command) > 0 { // Command present in JSON
		cmd, err := unmarshalExpression(aux.Command)
		if err != nil { // Propagate error from command deserialization
			return err
		}
		e.Command = cmd
	}

	// Deserialize args slice if present.
	// Use append to build slice since Args may be nil and we want to preserve that.
	if len(aux.Args) > 0 { // Args present in JSON
		e.Args = make([]Expression, 0, len(aux.Args))
		for _, raw := range aux.Args {
			arg, err := unmarshalExpression(raw)
			if err != nil { // Propagate error; partial args invalid
				return err
			}
			e.Args = append(e.Args, arg)
		}
	}

	return nil
}

// MarshalJSON implements json.Marshaler for File.
//
// Description:
//
//	Serializes File (AST root node) to JSON byte array.
//	File represents a complete AST of a Blueprint file, containing file name and definition list.
//
// How it works:
//  1. Use anonymous struct as intermediate representation
//  2. Copy file name and definition list
//  3. Defs field is Definition interface slice, each element's MarshalJSON method will be called
//  4. Call json.Marshal() to serialize
//
// JSON format:
//
//	{
//	  "name": "Android.bp",
//	  "defs": [
//	    {"type": "module", ...},
//	    {"type": "assignment", ...}
//	  ]
//	}
//
// Parameters:
//   - No explicit parameters, receiver is *File
//
// Returns:
//   - []byte: JSON representation of File
//   - error: Error during JSON serialization, if any
//
// Edge cases:
//   - When Defs is empty slice, JSON contains "defs": []
//   - nil elements in Defs are serialized as null
//   - Empty file name is serialized normally
func (f *File) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name string       `json:"name"`
		Defs []Definition `json:"defs"`
	}{
		Name: f.Name,
		Defs: f.Defs,
	})
}

// UnmarshalJSON implements json.Unmarshaler for File.
//
// Description:
//
//	Deserializes JSON byte array to File (AST root node).
//	Note: Defs field uses []json.RawMessage for deferred parsing to support polymorphic definition types.
//
// How it works:
//  1. Use anonymous intermediate struct with Defs field as []json.RawMessage
//  2. Call json.Unmarshal() to deserialize basic fields
//  3. Iterate through Defs, call unmarshalDefinition() for each element to parse concrete definition type
//  4. Pre-allocate Defs slice length to avoid multiple allocations
//
// Parameters:
//   - data: JSON format byte array
//
// Returns:
//   - error: Error during JSON deserialization, if any
//
// Edge cases:
//   - "defs" field is null or missing results in f.Defs being nil
//   - "defs" is empty array results in f.Defs being empty slice (length 0)
//   - Unknown definition types are handled as Module by unmarshalDefinition
//   - Any Defs element parse failure returns error immediately
func (f *File) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Name string            `json:"name"`
		Defs []json.RawMessage `json:"defs"`
	}{}
	if err := json.Unmarshal(data, aux); err != nil { // Unmarshal to intermediate struct
		return err
	}
	f.Name = aux.Name
	// Pre-allocate Defs slice with exact length to avoid reallocations.
	// Each element will be deserialized using unmarshalDefinition which handles
	// polymorphic definition types (Module vs Assignment).
	f.Defs = make([]Definition, len(aux.Defs))
	for i, raw := range aux.Defs {
		def, err := unmarshalDefinition(raw)
		if err != nil { // Propagate error; partial file is invalid
			return err
		}
		f.Defs[i] = def
	}
	return nil
}

// unmarshalExpression determines the expression type based on the "type" field in JSON and deserializes it.
//
// Description:
//
//	Parses JSON raw bytes, determines the concrete expression type based on the "type" field,
//	then calls the corresponding deserialization method to convert JSON to the appropriate Expression interface implementation.
//
// Supported expression types and their corresponding "type" values:
//   - "string":   String expression (string literal)
//   - "int64":    Int64 expression (integer literal)
//   - "bool":     Bool expression (boolean literal)
//   - "list":     List expression (list literal)
//   - "variable": Variable expression (variable reference)
//   - "operator": Operator expression (binary operator)
//   - "select":   Select expression (conditional selection)
//   - "unset":    Unset expression (unset value)
//
// How it works:
//  1. Check if raw bytes are empty; return nil if empty
//  2. Use intermediate struct to extract the "type" field
//  3. Use switch statement based on "type" value to call corresponding deserialization method
//  4. Each branch creates an instance of the corresponding type and calls json.Unmarshal()
//
// Parameters:
//   - raw: JSON raw bytes (json.RawMessage type), should contain "type" field
//
// Returns:
//   - Expression: Deserialized expression interface instance; returns nil for unknown types
//   - error: Error during JSON parsing, if any
//
// Edge cases:
//   - When raw is empty or length is 0, returns (nil, nil)
//   - When "type" field is missing, aux.Type is empty string
//   - Unknown type strings return (nil, nil) without error
//   - Invalid JSON format returns parse error
//   - Each type's UnmarshalJSON method may return its own errors
func unmarshalExpression(raw json.RawMessage) (Expression, error) {
	// Early return for empty input to avoid unnecessary JSON parsing.
	// Empty raw bytes typically indicate a null or missing value in the parent struct.
	if len(raw) == 0 { // Empty input; return nil to indicate missing value
		return nil, nil
	}

	// Extract the "type" field first to determine the concrete expression type.
	// Using a separate unmarshal step avoids parsing the entire JSON multiple times
	// and allows polymorphic deserialization based on the "type" field.
	aux := &struct {
		Type string `json:"type"`
	}{}
	if err := json.Unmarshal(raw, aux); err != nil { // Extract "type" field to determine expression type
		return nil, err
	}

	// Dispatch to the appropriate deserializer based on the "type" field.
	// Each case creates the corresponding expression type and deserializes the full JSON into it.
	// The raw bytes contain all fields needed by the specific expression type's UnmarshalJSON method.
	switch aux.Type {
	case "string":
		var expr String
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to String expression
			return nil, err
		}
		return &expr, nil
	case "int64":
		var expr Int64
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to Int64 expression
			return nil, err
		}
		return &expr, nil
	case "bool":
		var expr Bool
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to Bool expression
			return nil, err
		}
		return &expr, nil
	case "list":
		var expr List
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to List expression
			return nil, err
		}
		return &expr, nil
	case "variable":
		var expr Variable
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to Variable expression
			return nil, err
		}
		return &expr, nil
	case "operator":
		var expr Operator
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to Operator expression
			return nil, err
		}
		return &expr, nil
	case "select":
		var expr Select
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to Select expression
			return nil, err
		}
		return &expr, nil
	case "unset":
		var expr Unset
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to Unset expression
			return nil, err
		}
		return &expr, nil
	case "exec_script":
		var expr ExecScript
		if err := json.Unmarshal(raw, &expr); err != nil { // Deserialize to ExecScript expression
			return nil, err
		}
		return &expr, nil
	default:
		// Unknown type: return nil (graceful degradation).
		// This allows forward compatibility with new expression types.
		// Callers should handle nil Expression appropriately.
		return nil, nil
	}
}

// unmarshalDefinition determines the definition type based on the "type" field in JSON and deserializes it.
//
// Description:
//
//	Parses JSON raw bytes, determines the concrete definition type based on the "type" field,
//	then calls the corresponding deserialization method to convert JSON to the appropriate Definition interface implementation.
//
// Supported definition types and their corresponding "type" values:
//   - "assignment": Assignment definition (variable assignment statement)
//   - others/missing: Module definition (module definition, default type)
//
// Design notes:
//   - Module is the default type because most definitions are modules
//   - Assignment requires explicit "type": "assignment"
//   - This design simplifies the JSON format; modules don't need an explicit type field
//
// How it works:
//  1. Check if raw bytes are empty; return nil if empty
//  2. Use intermediate struct to extract the "type" field
//  3. Use switch statement based on "type" value:
//     - "assignment": Create Assignment instance and deserialize
//     - default: Create Module instance and deserialize
//
// Parameters:
//   - raw: JSON raw bytes (json.RawMessage type), should contain "type" field
//
// Returns:
//   - Definition: Deserialized definition interface instance
//   - error: Error during JSON parsing, if any
//
// Edge cases:
//   - When raw is empty or length is 0, returns (nil, nil)
//   - When "type" field is missing, defaults to Module
//   - Unknown type strings are treated as Module (default behavior)
//   - Invalid JSON format returns parse error
//   - Module and Assignment's UnmarshalJSON methods may return their own errors
func unmarshalDefinition(raw json.RawMessage) (Definition, error) {
	// Early return for empty input to avoid unnecessary JSON parsing.
	if len(raw) == 0 { // Empty input; return nil to indicate missing value
		return nil, nil
	}

	// Extract the "type" field to determine if this is an Assignment or Module.
	// Module is the default (no explicit "type" field needed) because most
	// definitions in Blueprint files are modules.
	aux := &struct {
		Type string `json:"type"`
	}{}
	if err := json.Unmarshal(raw, aux); err != nil { // Extract "type" field to determine definition type
		return nil, err
	}

	// Dispatch based on type field.
	// - "assignment": Explicit variable assignment (e.g., my_var = "value")
	// - default: Module definition (most common case, no type field needed)
	switch aux.Type {
	case "assignment":
		var def Assignment
		if err := json.Unmarshal(raw, &def); err != nil { // Deserialize to Assignment definition
			return nil, err
		}
		return &def, nil
	default:
		// Default to Module: simplifies JSON format for the common case.
		// Most Blueprint definitions are modules, so omitting "type" reduces noise.
		var def Module
		if err := json.Unmarshal(raw, &def); err != nil { // Deserialize to Module definition
			return nil, err
		}
		return &def, nil
	}
}

// posToString converts scanner.Position to string format.
//
// Description:
//
//	Converts scanner.Position struct to a "file:line:column" format string.
//	Used to store position information during JSON serialization.
//
// String format:
//   - Normal case: "Android.bp:10:5" means file Android.bp, line 10, column 5
//   - Zero-value position: "" empty string (when Filename is empty and Line and Column are 0)
//
// How it works:
//  1. Check if it's a zero-value position (Filename is empty and Line, Column are 0)
//  2. If zero-value, return empty string (avoids serializing meaningless "::0:0")
//  3. Otherwise use strconv.Itoa to convert line and column numbers to strings and concatenate
//
// Parameters:
//   - pos: scanner.Position struct containing filename, line number, column number
//
// Returns:
//   - string: "file:line:column" format string; returns empty string for zero-value
//
// Edge cases:
//   - Zero-value position (Filename="", Line=0, Column=0) returns empty string
//   - When only Filename is present, returns "file:0:0" (though uncommon)
//   - When line or column is 0, it's still included in the string (e.g., "file:0:5")
//   - Does not verify if the file actually exists
//
// Example:
//
//	pos := scanner.Position{Filename: "test.bp", Line: 10, Column: 5}
//	str := posToString(pos) // Returns "test.bp:10:5"
func posToString(pos scanner.Position) string {
	// Check for zero-value position to avoid serializing meaningless "::0:0".
	// This happens when position info was not set during parsing.
	if pos.Filename == "" && pos.Line == 0 && pos.Column == 0 { // Zero-value position; return empty string
		return ""
	}
	// Build "file:line:column" format string using strconv.Itoa for integer conversion.
	// This format is compatible with stringToPos() for round-trip serialization.
	return pos.Filename + ":" + strconv.Itoa(pos.Line) + ":" + strconv.Itoa(pos.Column)
}

// stringToPos converts string back to scanner.Position.
//
// Description:
//
//	Converts "file:line:column" format string back to scanner.Position struct.
//	Used to restore position information during JSON deserialization.
//
// String format:
//   - Normal case: "Android.bp:10:5" -> Position{Filename: "Android.bp", Line: 10, Column: 5}
//   - Empty string: "" -> scanner.Position{} (zero-value)
//
// How it works:
//  1. Check if string is empty; return zero-value Position if empty
//  2. Use strings.Split to split string by ":"
//  3. If split result has 3 or more parts, parse line and column numbers
//  4. Use strconv.Atoi to convert strings to integers (conversion errors are ignored)
//  5. Construct and return scanner.Position struct
//
// Parameters:
//   - s: "file:line:column" format string, or empty string
//
// Returns:
//   - scanner.Position: Converted position struct; returns zero-value on parse failure
//
// Edge cases:
//   - Empty string returns zero-value Position{}
//   - Malformed string (less than 3 parts after split) returns zero-value
//   - When line or column is not a valid number, Atoi returns 0 (no error reported)
//   - Filename part may contain ":" (e.g., absolute path), but only splits on first ":"
//   - Does not verify if the file actually exists
//
// Example:
//
//	pos := stringToPos("test.bp:10:5")
//	// pos = scanner.Position{Filename: "test.bp", Line: 10, Column: 5}
//
//	pos := stringToPos("")
//	// pos = scanner.Position{}
func stringToPos(s string) scanner.Position {
	// Empty string indicates missing or zero-value position; return zero-value.
	if s == "" { // Empty string; return zero-value position
		return scanner.Position{}
	}
	// Split on ":" to extract filename, line, and column.
	// Note: Filename may contain ":" (e.g., absolute Windows paths), but we only
	// split on the first two ":" occurrences (parts[0], parts[1], parts[2]).
	parts := strings.Split(s, ":")
	if len(parts) >= 3 { // Valid format with filename, line, and column
		// Parse line and column numbers; ignore errors (invalid numbers become 0).
		// This provides graceful degradation for malformed position strings.
		line, _ := strconv.Atoi(parts[1])
		col, _ := strconv.Atoi(parts[2])
		return scanner.Position{
			Filename: parts[0],
			Line:     line,
			Column:   col,
		}
	}
	// Malformed string (less than 3 parts): return zero-value.
	return scanner.Position{}
}
