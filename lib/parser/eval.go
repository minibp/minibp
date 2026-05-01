// Package parser provides lexical analysis and parsing for Blueprint build definitions.
// Eval subpackage - AST evaluation and expression processing.
//
// This package implements the third stage of the Blueprint build system:
// it takes AST nodes from the parser and evaluates them to produce Go values.
// The evaluator handles variable resolution, string interpolation,
// operator evaluation, and select() conditional expressions.
//
// Supported operations:
//   - Variable assignment and reference: my_var = "value", ${my_var}
//   - String interpolation: "src/${arch}/file.c"
//   - Binary operators: +, -, *
//   - select() conditions: arch(), os(), host(), target(), variant(), etc.
//   - String/list concatenation: list + list, string + string
//   - Map merging: map properties recursively merge
//
// Evaluation flow:
//  1. ProcessAssignments: Evaluate all variable assignments in order
//  2. Eval: Evaluate any expression AST node to its Go value
//  3. evalSelect: Handle architecture/platform-specific values
//
// Error handling:
//   - Undefined variables return nil
//   - select() with no match records error in strict mode
//   - All errors include source position information
package parser

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"sync"
)

// interpolationPattern matches ${variable_name} patterns in strings.
// It is used for string interpolation where variables are substituted
// into string literals. The pattern matches ${ followed by an identifier
// (starting with letter or underscore, followed by letters, digits, underscores)
// and a closing }.
//
// Example matches:
//   - ${my_var}
//   - ${PRODUCT_NAME}
//   - ${CONFIG_ARCH}
var interpolationPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// UnsetSentinel is a special value returned when a select branch evaluates to "unset".
// Callers should check for this value and treat the property as if it was never assigned.
// This allows properties to be removed in specific variants or configurations.
//
// Edge cases: Compare using direct reference equality (== UnsetSentinel) rather than deep equality,
// as it is a sentinel pointer value.
var UnsetSentinel = &struct{ name string }{name: "unset"}

// Evaluator evaluates Blueprint AST nodes into Go values.
// It maintains a map of variables and configuration values that are
// used during evaluation of expressions, property values, and select statements.
//
// The evaluator supports:
//   - Variable assignment: SetVar(name, value) and += concatenation
//   - String interpolation: ${variable} patterns in strings
//   - Binary operators: +, -, * for integers; + for strings/lists
//   - select() conditional: Chooses values based on configuration
//   - Configuration lookup: arch, os, host, target, variant, etc.
//
// Data structures:
//   - vars: Variable table - name -> value
//   - config: Configuration - key -> value (set from build system)
//   - selectErrors: Errors from select() evaluation
//
// Evaluation model:
//   - Variables are processed in source order (dependency order)
//   - select() supports strict mode (error on match) and permissive mode
//   - All expressions are evaluated to Go native types
//
// The evaluator is the third stage in the Blueprint pipeline, sitting between
// the parser (which produces AST nodes) and the ninja generator (which consumes
// evaluated values). It transforms the abstract representation into concrete
// values that can be used in build rules.
type Evaluator struct {
	vars         map[string]interface{} // Variable table: name -> value (string, int64, bool, []interface{}, map, etc.)
	config       map[string]string      // Configuration: key (arch, os, host, target) -> value
	strictSelect bool                   // If true, unmatched select() produces error; if false, returns nil silently
	selectErrors []error                // Errors collected from select() evaluation (only in strict mode)
}

// NewEvaluator creates a new Evaluator with empty variable and config maps.
//
// The evaluator starts with no variables defined and an empty
// configuration. Variables are added via SetVar() or processed
// from Assignment nodes. Configuration is set via SetConfig()
// or from command-line flags.
//
// Returns:
//   - A new Evaluator instance ready to evaluate expressions
func NewEvaluator() *Evaluator {
	return &Evaluator{
		vars:         make(map[string]interface{}),
		config:       make(map[string]string),
		strictSelect: true,
	}
}

// SetStrictSelect controls whether select() statements without a matching case and no default
// should produce an error.
//
// When strict is true (the default):
//   - Unmatched select() expressions are collected in SelectErrors
//   - The build will fail after all evaluation is complete
//
// When strict is false:
//   - Unmatched select() returns nil silently
//   - No errors are recorded
//
// This is useful for permissive parsing or when default values
// are expected but not specified.
//
// Parameters:
//   - strict: Whether to enforce strict select() evaluation
func (e *Evaluator) SetStrictSelect(strict bool) {
	e.strictSelect = strict
}

// SelectErrors returns any errors collected from select() evaluations.
// These are errors where a select() had no matching case and no default,
// and strict mode was enabled. Callers should check this after evaluation
// and report any errors appropriately.
//
// Returns:
//   - []error: List of select() evaluation errors
func (e *Evaluator) SelectErrors() []error {
	return e.selectErrors
}

// SetVar sets a variable in the evaluator's variable table.
// Variables are set by assignment statements and can be referenced
// in expressions throughout the Blueprint file.
//
// Supported value types:
//   - string: String values like "hello"
//   - int64: Integer values like 42
//   - bool: Boolean values like true
//   - []string: List of strings like ["a", "b"]
//   - []interface{}: Generic list
//   - map[string]interface{}: Map/dictionary
//
// Variables are looked up during expression evaluation.
// The variable table is populated by ProcessAssignments().
//
// Parameters:
//   - name: The variable name
//   - value: The value to set (can be string, int64, bool, []interface{}, map, etc.)
func (e *Evaluator) SetVar(name string, value interface{}) {
	e.vars[name] = value
}

// SetConfig sets a configuration value for the evaluator.
// Configuration values are used by select() statements to determine
// which branch to take. Common config keys include "arch", "os", "host", "target".
//
// Configuration values are typically set from build system parameters
// or command-line flags.
//
// Parameters:
//   - key: The configuration key (e.g., "arch", "os")
//   - value: The configuration value (e.g., "arm", "linux")
func (e *Evaluator) SetConfig(key, value string) {
	e.config[key] = value
}

// ProcessAssignments processes all assignments in a File.
// It iterates through all definitions in the file and evaluates any assignment statements.
// Assignment statements set variables that can be referenced in subsequent module definitions.
//
// Variables are processed in order, so later assignments can reference
// earlier ones. The evaluator handles both = (simple) and += (concatenative)
// assignments.
//
// Parameters:
//   - file: The parsed Blueprint file containing definitions
func (e *Evaluator) ProcessAssignments(file *File) {
	e.ProcessAssignmentsFromDefs(file.Defs)
}

// ProcessAssignmentsFromDefs processes assignments from a list of definitions.
// This is used both for top-level assignments and for nested processing.
// It handles both simple (=) and concatenative (+=) assignments.
//
// For += assignments:
//   - String += string: appends to existing string
//   - String += list: wraps list in string context (error-prone)
//   - List += string: appends string to list
//   - List += list: concatenates lists
//
// Parameters:
//   - defs: A list of definitions to process.
//     Only *Assignment definitions are processed; others are silently skipped.
//
// Edge cases:
//   - First += on undefined variable creates the variable (treats as simple assignment)
//   - Type mismatches in += (e.g., string += list) may cause unexpected behavior
//   - Only string, []string, and []interface{} types are handled for += operator
func (e *Evaluator) ProcessAssignmentsFromDefs(defs []Definition) {
	for _, def := range defs {
		// Type assert to check if this definition is an assignment.
		// Non-assignment definitions (modules, etc.) are skipped.
		assign, ok := def.(*Assignment)
		if !ok {
			continue // Skip non-assignment definitions (modules, etc.)
		}
		// Evaluate the assignment value expression.
		// This handles variable references, string interpolation, operators, etc.
		val := e.Eval(assign.Value)

		// Handle += operator - concatenate to existing variable
		if assign.Assigner == "+=" {
			// Check if variable already exists
			if existing, ok := e.vars[assign.Name]; ok {
				// Type-switch to handle different existing value types
				switch ev := existing.(type) {
				case string:
					// String concatenation: name += "suffix"
					// Only concatenates if the new value is also a string
					if nv, ok := val.(string); ok {
						e.vars[assign.Name] = ev + nv
					}
					// If val is not string, the assignment is silently ignored
				case []string:
					// List concatenation with string: name += "item"
					if nv, ok := val.(string); ok {
						e.vars[assign.Name] = append(ev, nv)
					} else if nv, ok := val.([]string); ok {
						// List concatenation with list: name += ["a", "b"]
						e.vars[assign.Name] = append(ev, nv...)
					}
					// Other types are silently ignored
				case []interface{}:
					// Generic list concatenation
					if nv, ok := val.([]interface{}); ok {
						e.vars[assign.Name] = append(ev, nv...)
					} else {
						// Append single value as interface{} (wraps scalar in list)
						e.vars[assign.Name] = append(ev, val)
					}
				}
			} else {
				// First += creates the variable (treat as simple assignment)
				// This is a design decision to be lenient with undefined variables
				e.vars[assign.Name] = val
			}
		} else {
			// Simple assignment (=) - always replaces the existing value
			e.vars[assign.Name] = val
		}
	}
}

// Eval evaluates an Expression and returns its Go value.
// This is the main entry point for evaluating any Blueprint expression.
// It handles all expression types and performs variable resolution,
// string interpolation, and operator evaluation.
//
// Return types:
//   - string: for String literals, Variable references
//   - int64: for Int64 literals
//   - bool: for Bool literals
//   - []interface{}: for List expressions
//   - map[string]interface{}: for Map expressions
//   - nil: for undefined variables
//
// Parameters:
//   - expr: The expression to evaluate
//
// Returns:
//   - interface{}: The evaluated value (string, int64, bool, []interface{}, map[string]interface{})
func (e *Evaluator) Eval(expr Expression) interface{} {
	switch v := expr.(type) {
	case *String:
		// String literal - perform variable interpolation
		// ${variable} patterns are replaced with variable values
		return e.interpolateString(v.Value)
	case *Int64:
		// Integer literal - return as-is
		return v.Value
	case *Bool:
		// Boolean literal - return as-is
		return v.Value
	case *List:
		// List - recursively evaluate each element
		var result []interface{}
		for _, item := range v.Values {
			result = append(result, e.Eval(item))
		}
		return result
	case *Map:
		// Map - recursively evaluate each property value
		result := make(map[string]interface{})
		for _, prop := range v.Properties {
			result[prop.Name] = e.Eval(prop.Value)
		}
		return result
	case *Variable:
		// Variable reference - look up in variable table
		if val, ok := e.vars[v.Name]; ok {
			return val
		}
		// Undefined variable returns nil (will be handled by caller)
		return nil
	case *Operator:
		// Binary operator - evaluate both operands and apply operator
		left := e.Eval(v.Args[0])
		right := e.Eval(v.Args[1])
		return evalOperator(left, right, v.Operator)
	case *Select:
		// Select conditional - evaluate based on configuration
		return e.evalSelect(v)
	case *Unset:
		// Unset returns the sentinel value for caller to handle
		return UnsetSentinel
	case *ExecScript:
		// Execute script during evaluation phase
		return e.evalExecScript(v)
	default:
		// Unknown expression type returns nil
		return nil
	}
}

// evalOperator applies a binary operator to two values.
// Currently supports:
//   - '+' operator:
//   - int64 + int64: integer addition
//   - string + string: string concatenation
//   - []interface{} + []interface{}: list concatenation
//   - []interface{} + scalar: append to list
//   - scalar + []interface{}: prepend to list
//   - map + map: map merge (recursive)
//   - '-' operator:
//   - int64 - int64: integer subtraction
//   - '*' operator:
//   - int64 * int64: integer multiplication
//
// Parameters:
//   - left: Left operand (any supported type)
//   - right: Right operand (any supported type)
//   - op: The operator rune ('+', '-', '*')
//
// Returns:
//   - interface{}: Result of the operation, or nil if unsupported.
//     Return type depends on the operation (int64, string, []interface{}, map[string]interface{})
//
// Edge cases:
//   - Type mismatches return nil (e.g., string + int64)
//   - Only int64 is supported for numeric operations (not float64, uint, etc.)
//   - Map merge is recursive: nested maps are deep-merged, lists are appended
//   - List + scalar wraps the scalar into the list (appends)
//
// Key design decisions:
//   - Operator evaluation is done outside the Evaluator to be a pure function (no side effects)
//   - The order of type checks matters: int64 is checked before interface{} list to avoid misclassification
//   - Map merge uses recursive mergeValues() to handle nested structures properly
func evalOperator(left, right interface{}, op rune) interface{} {
	switch op {
	case '+':
		// Try integer addition first (most common numeric operation)
		li, lok := left.(int64)
		ri, rok := right.(int64)
		if lok && rok {
			return li + ri
		}
		// Try string concatenation (e.g., "hello" + " world")
		ls, lok := left.(string)
		rs, rok := right.(string)
		if lok && rok {
			return ls + rs
		}
		// Try list concatenation: []interface{} + []interface{}
		// Also handles []string via toInterfaceList conversion
		ll, lok := toInterfaceList(left)
		rl, rok := toInterfaceList(right)
		if lok && rok {
			return append(ll, rl...)
		}
		// Try list + scalar: []interface{} + scalar (appends scalar to list)
		if lok && !rok {
			return append(ll, right)
		}
		// Try scalar + list: scalar + []interface{} (prepends scalar to list)
		if !lok && rok {
			return append([]interface{}{left}, rl...)
		}
		// Try map merge: map[string]interface{} + map[string]interface{}
		// Maps are merged recursively; nested maps are deep-merged
		lm, lok := left.(map[string]interface{})
		rm, rok := right.(map[string]interface{})
		if lok && rok {
			result := make(map[string]interface{})
			// Copy all keys from left map
			for k, v := range lm {
				result[k] = v
			}
			// Merge right map keys; recursively merge overlapping keys
			for k, v := range rm {
				if existing, exists := result[k]; exists {
					// Recursively merge nested values (handles nested maps/lists)
					result[k] = mergeValues(existing, v)
				} else {
					result[k] = v
				}
			}
			return result
		}
	case '-':
		// Integer subtraction (only int64 supported)
		li, lok := left.(int64)
		ri, rok := right.(int64)
		if lok && rok {
			return li - ri
		}
	case '*':
		// Integer multiplication (only int64 supported)
		li, lok := left.(int64)
		ri, rok := right.(int64)
		if lok && rok {
			return li * ri
		}
	}
	// Unsupported operator or type mismatch returns nil
	return nil
}

// toInterfaceList converts various slice types to []interface{} for unified handling.
// It handles []interface{} directly, []string by converting each element, and returns false for other types.
// This allows the operator evaluation to work with different slice representations uniformly.
//
// Parameters:
//   - v: The value to convert.
//     Supported types: []interface{}, []string. Other types will fail conversion.
//
// Returns:
//   - []interface{}: The converted slice, or nil if conversion not possible
//   - bool: true if conversion was successful, false otherwise
//
// Edge cases:
//   - nil input returns (nil, false)
//   - []string with empty strings: they are preserved in the converted slice
//   - Other slice types (e.g., []int64) are not supported and return false
//
// Key design decisions:
//   - Only handles the two most common list types in Blueprint (interface{} and string)
//   - Conversion creates a new slice to avoid mutating the original
func toInterfaceList(v interface{}) ([]interface{}, bool) {
	switch l := v.(type) {
	case []interface{}:
		return l, true
	case []string:
		// Convert []string to []interface{}
		result := make([]interface{}, len(l))
		for i, s := range l {
			result[i] = s
		}
		return result, true
	default:
		return nil, false
	}
}

// mergeValues merges two values for map operations during evaluation.
// When merging maps, nested lists are appended rather than replaced.
// This implements Blueprint's semantic where map properties merge recursively:
//
//   - Lists/arrays: appended (combined)
//   - Maps: recursively merged (nested properties merged)
//   - Scalars: overridden (replaced)
//
// Parameters:
//   - existing: The value already in the map (from the left-hand side of + operator)
//   - incoming: The new value being merged (from the right-hand side of + operator)
//
// Returns:
//   - interface{}: The merged result.
//     Type depends on input: []interface{} for lists, map[string]interface{} for maps,
//     or the incoming value for scalars.
//
// Edge cases:
//   - Type mismatch (e.g., list + map) returns the incoming value (scalar override behavior)
//   - Nested maps are deep-merged recursively
//   - Nested lists within maps are appended during recursive merge
//   - Empty lists or maps are handled naturally (append to empty, or copy keys)
//
// Key design decisions:
//   - Blueprint semantics dictate that maps merge recursively while scalars are replaced
//   - List concatenation uses append(), which may retain references to original slices
//   - The incoming value wins for type mismatches (consistent with "override" semantics)
func mergeValues(existing, incoming interface{}) interface{} {
	// Lists are appended (supports both []interface{} and []string via toInterfaceList)
	el, eok := toInterfaceList(existing)
	il, iok := toInterfaceList(incoming)
	if eok && iok {
		return append(el, il...)
	}
	// Maps are recursively merged (deep merge)
	em, eok := existing.(map[string]interface{})
	im, iok := incoming.(map[string]interface{})
	if eok && iok {
		result := make(map[string]interface{})
		// Copy all keys from existing map
		for k, v := range em {
			result[k] = v
		}
		// Merge incoming keys; recursively merge overlapping keys
		for k, v := range im {
			if existingVal, exists := result[k]; exists {
				// Recursively merge nested values (handles nested maps/lists)
				result[k] = mergeValues(existingVal, v)
			} else {
				result[k] = v
			}
		}
		return result
	}
	// Scalars are overridden: incoming value replaces existing
	return incoming
}

// toString converts a value to its string representation.
// Handles string, []string (joined with space), and other types (formatted as %v).
// The second return value is always true for interface compatibility with callers
// that expect a (string, bool) pattern.
//
// Parameters:
//   - v: The value to convert.
//     Supported types: string, []string. Other types use fmt.Sprintf("%v").
//
// Returns:
//   - string: The string representation of the value
//   - bool: Always returns true (for interface compatibility with callers)
//
// Edge cases:
//   - nil value returns "nil" (via %v formatting)
//   - []string with empty elements: they are joined with spaces (e.g., "a  b")
//   - Other types (int64, bool, map, etc.) use default Go formatting
func toString(v interface{}) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case []string:
		// Join string slice with spaces (e.g., ["a", "b"] -> "a b")
		return strings.Join(s, " "), true
	default:
		// Use default Go formatting for other types (int64, bool, etc.)
		return fmt.Sprintf("%v", v), true
	}
}

// interpolateString performs variable substitution in a string.
// It replaces ${variable_name} patterns with the variable's value.
// If a variable is not found, the pattern is left unchanged in the result.
// This allows strings to contain multiple variable references.
//
// Parameters:
//   - s: The string with potential ${...} interpolation patterns.
//     Example: "src/${arch}/file.c" where arch="arm64" becomes "src/arm64/file.c"
//
// Returns:
//   - string: The string with variables substituted.
//     If no variables are found or no ${ patterns exist, returns original string.
//
// Edge cases:
//   - Variable not found: pattern is left unchanged (e.g., "${undefined}" stays as "${undefined}")
//   - Variable value is nil: substituted as "nil" (via fmt.Sprintf("%v"))
//   - Multiple references to same variable: all are substituted
//   - Nested patterns like ${${var}} are not supported (only top-level ${...} matched)
//   - Empty variable name ${} would not match the regex pattern
//
// Key design decisions:
//   - Uses pre-compiled regex interpolationPattern for efficiency (avoids recompiling on each call)
//   - Quick check for "${" avoids regex overhead for strings without interpolation
//   - Variable values are formatted with %v, which works for all Go types
func (e *Evaluator) interpolateString(s string) string {
	// Quick check - if no ${, return as-is (avoids regex overhead)
	if !strings.Contains(s, "${") {
		return s
	}

	// Replace all ${var} patterns with variable values using regex
	return interpolationPattern.ReplaceAllStringFunc(s, func(match string) string {
		// Extract variable name from the match using submatch indices
		submatches := interpolationPattern.FindStringSubmatchIndex(match)
		if len(submatches) < 4 {
			return match // Should not happen with valid regex, but safety check
		}
		// Extract variable name from captured group (indices 2 and 3)
		name := match[submatches[2]:submatches[3]]
		// Look up variable in the evaluator's variable table
		val, ok := e.vars[name]
		if !ok {
			return match // Variable not found, leave pattern unchanged
		}
		// Convert value to string using default formatting
		return fmt.Sprintf("%v", val)
	})
}

// evalSelect evaluates a select() expression.
// Select chooses different values based on configuration (arch, os, host, target).
// It evaluates the condition, then matches it against each case pattern.
// If no pattern matches, the "default" case (if present) is used.
// If still no match and strict mode is enabled, an error is recorded.
//
// Parameters:
//   - s: The Select AST node to evaluate.
//     Contains Conditions (e.g., arch()) and Cases (pattern -> value mappings).
//
// Returns:
//   - interface{}: The value from the matching case, or nil if no match.
//     May return UnsetSentinel if a case explicitly returns unset.
//
// Edge cases:
//   - Empty conditions or cases return nil immediately
//   - Single condition uses evalSelectSingle (optimized path)
//   - Multiple conditions use evalSelectMulti (tuple matching)
//   - Unmatched select in strict mode records error in e.selectErrors
//
// Key design decisions:
//   - Conditions are evaluated once and cached in condValues for reuse
//   - Separate code paths for single vs multi-condition for clarity and performance
//   - Three-pass matching algorithm (exact, any, default) is implemented in single/multi helpers
func (e *Evaluator) evalSelect(s *Select) interface{} {
	// Guard against empty conditions or cases
	if len(s.Conditions) == 0 || len(s.Cases) == 0 {
		return nil
	}

	// Evaluate all conditions once and cache results
	condValues := make([]interface{}, len(s.Conditions))
	for i, cond := range s.Conditions {
		condValues[i] = e.evalSelectCondition(cond)
	}

	// Single variable select vs multi-variable (tuple) select
	if len(condValues) == 1 {
		// Optimized path for single condition (most common case)
		return e.evalSelectSingle(s, condValues[0])
	}

	// Multi-variable select (tuple matching)
	return e.evalSelectMulti(s, condValues)
}

// evalSelectSingle handles select() evaluation with a single condition value.
// It performs a three-pass matching algorithm:
//
//	First pass: Look for non-default exact pattern matches.
//	Second pass: Look for "any" wildcard patterns (@pattern or any @var).
//	Third pass: Look for "default" fallback pattern.
//
// If no pattern matches and no default exists, in strict mode an error is recorded.
//
// Parameters:
//   - s: The Select AST node containing cases and patterns.
//     Each case has Patterns (to match) and Value (to return if matched).
//   - configValue: The evaluated configuration value to match against patterns.
//     Typically a string like "arm64", "linux", etc. Can be nil if unset.
//
// Returns:
//   - interface{}: The value from the matching case, nil if no match, or UnsetSentinel.
//     Returned value is evaluated (variables resolved, interpolation performed).
//
// Edge cases:
//   - Unset pattern matches when configValue is nil or empty string
//   - Any pattern (@pattern) matches any non-unset value and can bind to a variable
//   - Default pattern is only checked in third pass (after exact and any patterns)
//   - Multiple patterns in a case: all must match (AND logic) for single condition
//   - Pattern with binding stores matched value in evaluator's variable table
//
// Key design decisions:
//   - Three-pass algorithm ensures deterministic matching order (exact > any > default)
//   - reflect.DeepEqual is used for pattern matching to handle various types
//   - evalCaseValue is called to properly handle UnsetSentinel and variable bindings
func (e *Evaluator) evalSelectSingle(s *Select, configValue interface{}) interface{} {
	// First pass: look for non-default exact pattern match
	for _, c := range s.Cases {
		for _, p := range c.Patterns {
			// Skip default and any patterns in first pass (handled in later passes)
			if e.isDefaultPattern(p) || e.isAnyPattern(p) {
				continue
			}
			// Handle unset pattern (matches nil or empty config)
			if e.isUnsetPattern(p) {
				if configValue == nil || configValue == "" {
					return e.evalCaseValue(c, configValue)
				}
				continue
			}
			// Exact match comparison - fast-path for common string case
			if p.Value != nil {
				patternVal := e.evalPatternValue(p.Value)
				if cv, ok := configValue.(string); ok {
					if pv, ok := patternVal.(string); ok && pv == cv {
						return e.evalCaseValue(c, configValue)
					}
				} else if reflect.DeepEqual(patternVal, configValue) {
					return e.evalCaseValue(c, configValue)
				}
			}
		}
	}

	// Second pass: look for "any" pattern (wildcard match)
	for _, c := range s.Cases {
		for _, p := range c.Patterns {
			if e.isAnyPattern(p) && !e.isConfigUnset(configValue) {
				// Bind matched value to variable if pattern has binding (e.g., @var)
				if p.Binding != "" {
					e.SetVar(p.Binding, configValue)
				}
				// Evaluate the case value (may reference the bound variable)
				return e.Eval(c.Value)
			}
		}
	}

	// Third pass: look for default case (fallback)
	for _, c := range s.Cases {
		if len(c.Patterns) == 1 && e.isDefaultPattern(c.Patterns[0]) {
			return e.evalCaseValue(c, configValue)
		}
	}

	// No match and no default - record error in strict mode
	if e.strictSelect && len(s.Conditions) > 0 {
		// Build a descriptive error message with condition name and unmatched value
		condDesc := s.Conditions[0].FunctionName
		if condDesc == "" {
			condDesc = fmt.Sprintf("%v", configValue)
		}
		e.selectErrors = append(e.selectErrors, fmt.Errorf("%s: select(%s) had value %v, which was not handled by the select", s.KeywordPos, condDesc, configValue))
	}

	return nil
}

// evalSelectMulti handles select() evaluation with multiple condition values (tuple select).
// This is used when select() conditions are specified as tuples, e.g., select((arch(), os()), {...}).
// It performs a similar three-pass matching algorithm as evalSelectSingle but accounts
// for multiple condition values per case.
//
// Parameters:
//   - s: The Select AST node containing cases with pattern tuples.
//     Each case must have the same number of patterns as conditions.
//   - condValues: The list of evaluated configuration values (one per condition).
//     Example: ["arm64", "linux"] for select((arch(), os()), {...}).
//
// Returns:
//   - interface{}: The value from the matching case, nil if no match, or UnsetSentinel.
//
// Edge cases:
//   - Cases with wrong number of patterns are skipped (no partial matching)
//   - All patterns in a case must match (AND logic across tuple elements)
//   - Default pattern in tuple: only matches if all other patterns match or are default
//   - Any/unset patterns in tuple act as wildcards for their position
//
// Key design decisions:
//   - Uses three-pass algorithm consistent with evalSelectSingle
//   - Tuple matching requires exact arity match (len(patterns) == len(condValues))
//   - The second pass handles wildcards (any, unset) that were skipped in first pass
func (e *Evaluator) evalSelectMulti(s *Select, condValues []interface{}) interface{} {
	// First pass: look for non-default tuple pattern match (exact matches only)
	for _, c := range s.Cases {
		// Skip cases with wrong number of patterns (arity must match)
		if len(c.Patterns) != len(condValues) {
			continue
		}

		allMatch := true
		hasDefault := false
		// Check if any pattern is a default/any/unset (skip in first pass)
		for _, p := range c.Patterns {
			if e.isDefaultPattern(p) {
				hasDefault = true
				continue
			}
			if e.isAnyPattern(p) || e.isUnsetPattern(p) {
				hasDefault = true
				continue
			}
		}

		// Skip if mixed defaults with non-matching (defer to second pass)
		if hasDefault && !allMatch {
			continue
		}

		// Try exact match for all patterns (no wildcards)
		if !hasDefault {
			match := true
			for i, p := range c.Patterns {
				if e.isDefaultPattern(p) || e.isAnyPattern(p) {
					continue
				}
				patternVal := e.evalPatternValue(p.Value)
				if !reflect.DeepEqual(patternVal, condValues[i]) {
					match = false
					break
				}
			}
			if match {
				return e.evalCaseValue(c, nil)
			}
		}
	}

	// Second pass: look for patterns with any/unset wildcards
	for _, c := range s.Cases {
		if len(c.Patterns) != len(condValues) {
			continue
		}
		match := true
		for i, p := range c.Patterns {
			if e.isDefaultPattern(p) || e.isAnyPattern(p) {
				continue // Wildcard matches anything
			}
			if e.isUnsetPattern(p) {
				// Unset pattern matches nil or empty string
				if condValues[i] != nil && condValues[i] != "" {
					match = false
				}
				continue
			}
			patternVal := e.evalPatternValue(p.Value)
			if !reflect.DeepEqual(patternVal, condValues[i]) {
				match = false
				break
			}
		}
		if match {
			return e.evalCaseValue(c, nil)
		}
	}

	// Third pass: look for default case (single default pattern)
	for _, c := range s.Cases {
		if len(c.Patterns) == 1 && e.isDefaultPattern(c.Patterns[0]) {
			return e.evalCaseValue(c, nil)
		}
	}

	return nil
}

// evalCaseValue evaluates the value expression of a select case.
// If the case value is UnsetSentinel, it returns the sentinel directly.
// If the case had a binding (any @var pattern), it also binds the matched value
// to that variable for use in the returned value expression.
//
// Parameters:
//   - c: The SelectCase to evaluate.
//     Contains Patterns (to match) and Value (expression to evaluate if matched).
//   - matchedValue: The value that matched the pattern (for binding).
//     May be nil if no binding is needed.
//
// Returns:
//   - interface{}: The evaluated case value, or UnsetSentinel.
//     Returned value is fully evaluated (variables resolved, interpolation performed).
//
// Edge cases:
//   - UnsetSentinel in case value causes early return (property will be unset)
//   - Multiple patterns with bindings: all bindings are set (last one wins for same name)
//   - Binding is set before evaluating the case value (allows use in expression)
//
// Key design decisions:
//   - Bindings are set in the evaluator's variable table so they can be referenced
//     in the case value expression (e.g., "${var}" in the value)
//   - Evaluation happens after bindings are set to allow variable substitution
func (e *Evaluator) evalCaseValue(c SelectCase, matchedValue interface{}) interface{} {
	// Set any bindings before evaluating the value expression
	// This allows the case value to reference bound variables like ${var}
	if matchedValue != nil {
		for _, p := range c.Patterns {
			if p.Binding != "" {
				e.SetVar(p.Binding, matchedValue)
			}
		}
	}
	// Evaluate the case value expression
	val := e.Eval(c.Value)
	// Check if the evaluated value is UnsetSentinel
	if val == UnsetSentinel {
		return UnsetSentinel
	}
	return val
}

// isConfigUnset checks if a configuration value should be treated as unset.
// A value is considered unset if it is nil or an empty string.
//
// Parameters:
//   - val: The configuration value to check
//
// Returns:
//   - bool: true if the value should be treated as unset
func (e *Evaluator) isConfigUnset(val interface{}) bool {
	return val == nil || val == ""
}

// evalSelectCondition evaluates a select condition.
// Conditions can be simple identifiers (like "arch", "os") or function calls
// with arguments (like "target(android)"). The evaluator looks up the
// condition value from its configuration map or variables.
//
// Built-in condition functions:
//   - arch(): Current architecture (arm, arm64, x86, x86_64)
//   - os(): Current operating system (linux, android, darwin)
//   - host(): Whether building for host
//   - target(): Target platform
//   - variant(): Build variant (debug, release)
//   - product_variable(): Product-specific variable
//   - soong_config_variable(): Configuration variable from namespace
//   - release_flag(): Release flag check
//
// Additional functions supported:
//   - target(platform): Returns config["target"]
//   - variant(name): Returns config["variant.name"] or config["variant"]
//   - product_variable(name): Returns config["product.name"] or config["product"]
//
// Parameters:
//   - cond: The ConfigurableCondition AST node to evaluate.
//     Contains FunctionName (e.g., "arch") and optional Args (for functions with parameters).
//
// Returns:
//   - interface{}: The condition value from config, vars, or nil if not found.
//     Return type is typically string (from config), but can be other types if from vars.
//
// Edge cases:
//   - Unknown function name returns config value lookup (best-effort)
//   - Missing arguments for functions like variant() return nil or config value
//   - soong_config_variable with empty namespace or variable returns nil
//   - release_flag with empty flag name returns nil
//   - Variable references (no function call) are resolved from vars table first, then config
//
// Key design decisions:
//   - Function arguments are evaluated recursively (supports variable references in args)
//   - For soong_config_variable, checks both "namespace.variable" and "soong_config.namespace.variable"
//   - The fallback to config[FunctionName] allows extensibility for custom conditions
type selectCondHandler func(e *Evaluator, cond ConfigurableCondition) interface{}

var selectCondDispatch map[string]selectCondHandler
var selectCondInitOnce sync.Once

func initSelectCondDispatch() map[string]selectCondHandler {
	return map[string]selectCondHandler{
		"soong_config_variable": evalSoongConfigVar,
		"release_flag":          evalReleaseFlag,
		"variant":               evalVariantCond,
		"product_variable":      evalProductVar,
		"target":                evalTargetCond,
		"arch":                  evalArchCond,
		"host":                  evalHostCond,
		"os":                    evalOSCond,
	}
}

func (e *Evaluator) evalSelectCondition(cond ConfigurableCondition) interface{} {
	selectCondInitOnce.Do(func() {
		selectCondDispatch = initSelectCondDispatch()
	})

	if cond.FunctionName == "" {
		if len(cond.Args) == 0 {
			return nil
		}
		return e.Eval(cond.Args[0])
	}

	if handler, ok := selectCondDispatch[cond.FunctionName]; ok {
		return handler(e, cond)
	}

	if len(cond.Args) == 0 {
		if val, ok := e.vars[cond.FunctionName]; ok {
			return val
		}
	}
	return e.config[cond.FunctionName]
}

func evalSoongConfigVar(e *Evaluator, cond ConfigurableCondition) interface{} {
	if len(cond.Args) < 2 {
		return nil
	}
	namespace := e.Eval(cond.Args[0])
	variable := e.Eval(cond.Args[1])
	nsStr, _ := namespace.(string)
	varStr, _ := variable.(string)
	if nsStr != "" && varStr != "" {
		key := nsStr + "." + varStr
		if val, ok := e.config[key]; ok {
			return val
		}
		if val, ok := e.vars["soong_config."+key]; ok {
			return val
		}
	}
	return nil
}

func evalReleaseFlag(e *Evaluator, cond ConfigurableCondition) interface{} {
	if len(cond.Args) < 1 {
		return nil
	}
	flag := e.Eval(cond.Args[0])
	if flagStr, ok := flag.(string); ok && flagStr != "" {
		key := "release." + flagStr
		if val, ok := e.config[key]; ok {
			return val
		}
	}
	return nil
}

func evalVariantCond(e *Evaluator, cond ConfigurableCondition) interface{} {
	if len(cond.Args) == 0 {
		return e.config["variant"]
	}
	name := e.Eval(cond.Args[0])
	if nameStr, ok := name.(string); ok && nameStr != "" {
		return e.config["variant."+nameStr]
	}
	return nil
}

func evalProductVar(e *Evaluator, cond ConfigurableCondition) interface{} {
	if len(cond.Args) == 0 {
		return e.config["product"]
	}
	name := e.Eval(cond.Args[0])
	if nameStr, ok := name.(string); ok && nameStr != "" {
		return e.config["product."+nameStr]
	}
	return nil
}

func evalTargetCond(e *Evaluator, cond ConfigurableCondition) interface{} {
	return e.config["target"]
}

func evalArchCond(e *Evaluator, cond ConfigurableCondition) interface{} {
	return e.config["arch"]
}

func evalHostCond(e *Evaluator, cond ConfigurableCondition) interface{} {
	return e.config["host"]
}

func evalOSCond(e *Evaluator, cond ConfigurableCondition) interface{} {
	return e.config["os"]
}

// evalExecScript executes a script during the evaluation phase and returns its output.
// The script is executed via os/exec.Command, and its stdout is captured.
// If the output is valid JSON, it is parsed into Go types (string, number, bool, list, map).
// Otherwise, the trimmed string output is returned.
//
// Parameters:
//   - script: The ExecScript AST node containing command and arguments.
//     Command and args are evaluated as expressions before execution.
//
// Returns:
//   - interface{}: The script output.
//     Type depends on output: string (plain text), or parsed JSON value (any JSON type).
//
// Edge cases:
//   - Empty or non-string command returns empty string
//   - Non-string arguments are silently skipped (not passed to command)
//   - Script execution error returns error string (does not panic)
//   - Valid JSON output is parsed; invalid JSON falls back to plain string
//   - Trailing whitespace/newlines are trimmed from output
//
// Key design decisions:
//   - Scripts are executed synchronously during evaluation (blocking)
//   - JSON parsing allows scripts to return structured data (lists, maps)
//   - Error messages are returned as strings rather than error type (caller decides)
//   - Security note: command and args come from Blueprint files; validate before use
func (e *Evaluator) evalExecScript(script *ExecScript) interface{} {
	// Evaluate the command (first argument) to get the executable path/name
	cmdValue := e.Eval(script.Command)
	cmdStr, ok := cmdValue.(string)
	if !ok || cmdStr == "" {
		return ""
	}

	// Build the command arguments by evaluating each argument expression
	args := make([]string, 0, len(script.Args))
	for _, arg := range script.Args {
		argValue := e.Eval(arg)
		// Only string arguments are passed (other types are silently skipped)
		if argStr, ok := argValue.(string); ok {
			args = append(args, argStr)
		}
	}

	// Execute the script and capture stdout
	cmd := exec.Command(cmdStr, args...)
	output, err := cmd.Output()
	if err != nil {
		// Return error as string for now (caller can check)
		return fmt.Sprintf("exec_script error: %v", err)
	}

	// Trim the output (remove trailing whitespace/newlines)
	result := strings.TrimSpace(string(output))

	// Try to parse as JSON with nesting depth limit to prevent stack overflow
	var jsonValue interface{}
	if safeJSONUnmarshal([]byte(result), &jsonValue, 100) == nil {
		return jsonValue
	}

	// Return as plain string if not valid JSON
	return result
}

// safeJSONUnmarshal unmarshals JSON with a maximum nesting depth.
func safeJSONUnmarshal(data []byte, v *interface{}, maxDepth int) error {
	depth := 0
	for _, b := range data {
		switch b {
		case '{', '[':
			depth++
			if depth > maxDepth {
				return fmt.Errorf("JSON nesting exceeds max depth of %d", maxDepth)
			}
		case '}', ']':
			depth--
		}
	}
	return json.Unmarshal(data, v)
}

// isDefaultPattern checks if a pattern is the "default" pattern.
// The default pattern is used as a fallback when no other pattern matches.
// It can be specified as a variable named "default" or a string "default".
//
// Parameters:
//   - pattern: The pattern to check
//
// Returns:
//   - bool: true if this is a default pattern
func (e *Evaluator) isDefaultPattern(pattern SelectPattern) bool {
	if v, ok := pattern.Value.(*Variable); ok && v.Name == "default" {
		return true
	}
	if s, ok := pattern.Value.(*String); ok && s.Value == "default" {
		return true
	}
	return false
}

// isAnyPattern checks if a pattern is a wildcard pattern ("any").
// Wildcard patterns match any value and can optionally bind the matched
// value to a variable using the "any @ var" syntax.
//
// Parameters:
//   - pattern: The pattern to check
//
// Returns:
//   - bool: true if this is an any wildcard pattern
func (e *Evaluator) isAnyPattern(pattern SelectPattern) bool {
	return pattern.IsAny
}

// isUnsetPattern checks if a pattern is the "unset" pattern.
// The unset pattern matches when a configuration value is not set or is empty.
//
// Parameters:
//   - pattern: The pattern to check
//
// Returns:
//   - bool: true if this is an unset pattern
func (e *Evaluator) isUnsetPattern(pattern SelectPattern) bool {
	_, ok := pattern.Value.(*Unset)
	return ok
}

// evalPatternValue evaluates a pattern value for comparison.
// It converts the pattern expression to a comparable Go value.
// Variables are resolved from the variable table.
//
// Parameters:
//   - expr: The pattern expression to evaluate.
//     Can be String, Int64, Bool, Variable, or other expression types.
//
// Returns:
//   - interface{}: The evaluated pattern value.
//     Typically string, int64, or bool for comparison purposes.
//
// Edge cases:
//   - Variable not found in table: returns variable name as string (fallback)
//   - Other expression types: recursively evaluated via e.Eval()
//   - String/Int64/Bool literals are returned directly (no evaluation needed)
//
// Key design decisions:
//   - Variables in patterns resolve to their values for comparison
//   - Fallback to variable name allows patterns like arch() to match "arch" string
//   - Direct return for literals avoids unnecessary evaluation overhead
func (e *Evaluator) evalPatternValue(expr Expression) interface{} {
	switch v := expr.(type) {
	case *String:
		return v.Value
	case *Int64:
		return v.Value
	case *Bool:
		return v.Value
	case *Variable:
		// Variable reference - resolve from variable table
		if val, ok := e.vars[v.Name]; ok {
			return val
		}
		// Return variable name as fallback (used for config key matching)
		return v.Name
	default:
		// Other expression types: evaluate recursively
		return e.Eval(expr)
	}
}

// EvalToString converts an expression to a string representation.
// If an evaluator is provided, it first evaluates the expression.
// Otherwise, it converts the raw AST node to string without evaluation.
// This is useful for debugging, error messages, and generating output.
//
// Parameters:
//   - expr: The expression to convert.
//     Can be any Expression type (String, Variable, Int64, Bool, etc.).
//   - eval: Optional evaluator for evaluation.
//     If nil, the raw AST node is converted without evaluating variables.
//
// Returns:
//   - string: The string representation of the expression.
//     Evaluated values return their string form; raw nodes return their literal representation.
//
// Edge cases:
//   - Nil expr returns empty string
//   - Non-string evaluated values use fmt.Sprintf("%v") for formatting
//   - Variable without evaluator returns the variable name (e.g., "my_var")
//   - Unknown expression types return empty string when no evaluator is provided
//
// Key design decisions:
//   - Supports both evaluated and raw conversion paths for flexibility
//   - Bool converts to "true"/"false" (lowercase, matching Blueprint syntax)
//   - Int64 uses decimal formatting (not octal/hex)
func EvalToString(expr Expression, eval *Evaluator) string {
	// If evaluator provided, evaluate first then convert to string
	if eval != nil {
		val := eval.Eval(expr)
		if s, ok := val.(string); ok {
			return s
		}
		// Non-string types use default Go formatting
		return fmt.Sprintf("%v", val)
	}
	// Handle common expression types without evaluation (raw AST to string)
	switch v := expr.(type) {
	case *String:
		return v.Value
	case *Variable:
		return v.Name
	case *Int64:
		return fmt.Sprintf("%d", v.Value)
	case *Bool:
		if v.Value {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// EvalToStringList converts an expression to a list of strings.
// It handles various input types:
//   - []string: returned as-is
//   - []interface{}: filters to only strings (non-strings silently dropped)
//   - string: wrapped in a single-element list
//   - Expression: first evaluated, then converted
//
// Parameters:
//   - expr: The expression to convert.
//     Can be a List expression, string literal, variable reference, etc.
//   - eval: Optional evaluator for evaluation.
//     If nil, falls back to EvalToStringListNoEval (no evaluation).
//
// Returns:
//   - []string: List of string values, or nil if conversion not possible.
//     Non-string elements in lists are silently dropped (not an error).
//
// Edge cases:
//   - Empty string returns []string{""} (not nil)
//   - []interface{} with no strings returns empty slice (not nil)
//   - Other types (int64, bool, map) return nil (conversion not possible)
//   - Evaluated Value that is nil returns nil
//
// Key design decisions:
//   - Non-string items in lists are dropped silently (lenient parsing)
//   - String scalar is wrapped in a slice for consistent return type
//   - Supports both evaluated and raw conversion paths (like EvalToString)
func EvalToStringList(expr Expression, eval *Evaluator) []string {
	// If no evaluator, use the no-eval path (parse AST directly)
	if eval == nil {
		return EvalToStringListNoEval(expr)
	}
	// Evaluate the expression first, then convert the result
	val := eval.Eval(expr)
	switch v := val.(type) {
	case []string:
		return v
	case []interface{}:
		// Filter to only strings
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
			// Non-string items are silently dropped
		}
		return result
	case string:
		// Wrap single string in a slice
		return []string{v}
	default:
		// Other types (int64, bool, map) cannot be converted
		return nil
	}
}

// EvalToStringListNoEval extracts string values from a List expression
// without evaluation. This is used when no evaluator is available.
// Only *String items are extracted; other expression types are silently ignored.
//
// Parameters:
//   - expr: The Expression to convert.
//     Should be a *List expression for meaningful results.
//
// Returns:
//   - []string: List of string values from String items, or nil if not a List.
//     Returns empty slice (not nil) if List has no String items.
//
// Edge cases:
//   - Non-List expression returns nil
//   - List with non-String items: those items are silently dropped
//   - Empty List returns empty slice (not nil)
//   - Nested lists are not supported (only top-level items checked)
//
// Key design decisions:
//   - No evaluation means variable references, select(), etc. are not resolved
//   - Only extracts *String items (int64, bool, etc. are ignored)
//   - Used as fallback when evaluator is not available (e.g., parsing phase)
func EvalToStringListNoEval(expr Expression) []string {
	if l, ok := expr.(*List); ok {
		result := make([]string, 0, len(l.Values))
		for _, item := range l.Values {
			// Only extract String items; others are silently ignored
			if s, ok := item.(*String); ok {
				result = append(result, s.Value)
			}
		}
		return result
	}
	// Not a List expression
	return nil
}

// EvalProperty evaluates a property's value and returns a new Property with the evaluated value.
// This transforms AST expression nodes into their evaluated Go values.
// The property name and position information are preserved; only the value is evaluated.
// The evaluated value is converted back to an AST node for compatibility
// with other parts of the system that expect Expression types.
//
// Parameters:
//   - prop: The property to evaluate.
//     Contains Name, NamePos, ColonPos, and Value (Expression to evaluate).
//
// Returns:
//   - *Property: A new property with the evaluated value converted back to AST node.
//     Returns a new struct; original prop is not modified.
//
// Edge cases:
//   - Unsupported value types (maps, etc.) keep the original unevaluated value
//   - []interface{} with non-string/int64/bool items: those items are silently dropped
//   - Nil value: results in nil Value in returned property
//
// Key design decisions:
//   - Returns a new Property struct rather than modifying in-place (immutable design)
//   - Converts Go native types back to AST nodes for downstream compatibility
//   - Only handles common types (string, int64, bool, []interface{}); maps not supported
func (e *Evaluator) EvalProperty(prop *Property) *Property {
	// Evaluate the property value expression
	val := e.Eval(prop.Value)
	// Create new property with preserved metadata (name, positions)
	newProp := &Property{
		Name:     prop.Name,
		NamePos:  prop.NamePos,
		ColonPos: prop.ColonPos,
	}
	// Convert the evaluated value back to an AST node for compatibility
	switch v := val.(type) {
	case string:
		newProp.Value = &String{Value: v}
	case int64:
		newProp.Value = &Int64{Value: v}
	case bool:
		newProp.Value = &Bool{Value: v}
	case []interface{}:
		// Convert []interface{} back to a List AST node
		var items []Expression
		for _, item := range v {
			// Only convert supported types; others are silently dropped
			if s, ok := item.(string); ok {
				items = append(items, &String{Value: s})
			} else if i, ok := item.(int64); ok {
				items = append(items, &Int64{Value: i})
			} else if b, ok := item.(bool); ok {
				items = append(items, &Bool{Value: b})
			}
		}
		newProp.Value = &List{Values: items}
	default:
		// Keep original value if conversion not possible (e.g., map types)
		newProp.Value = prop.Value
	}
	return newProp
}

// EvalModule evaluates all properties in a module, including arch/host/target overrides.
// This performs full evaluation of the module, replacing variable references,
// performing string interpolation, and evaluating select() expressions.
// The module's main properties, arch-specific overrides, host overrides, and target
// overrides are all evaluated and replaced with their evaluated values.
//
// Parameters:
//   - m: The module to evaluate.
//     The module is modified: property values are replaced with evaluated versions.
//     Nil Map or nil override maps are handled gracefully (no-op).
//
// Edge cases:
//   - Nil m.Map: function returns immediately (nothing to evaluate)
//   - Nil Arch/Host/Target: corresponding section is skipped
//   - Empty properties slices: result is empty slice (not nil)
//   - Nested select() expressions are evaluated recursively
//
// Key design decisions:
//   - Properties are evaluated individually via EvalProperty() for consistency
//   - Each section (main, arch, host, target) is evaluated independently
//   - The original module structure is preserved; only property values are updated
func (e *Evaluator) EvalModule(m *Module) {
	// Skip if module has no property map
	if m.Map == nil {
		return
	}
	// Evaluate main module properties
	// Each property is evaluated and a new slice is created with evaluated values
	var newProps []*Property
	for _, prop := range m.Map.Properties {
		newProps = append(newProps, e.EvalProperty(prop))
	}
	m.Map.Properties = newProps

	// Evaluate arch-specific overrides (e.g., arch: { arm: { ... }, arm64: { ... } })
	if m.Arch != nil {
		for arch, archMap := range m.Arch {
			var newArchProps []*Property
			for _, prop := range archMap.Properties {
				newArchProps = append(newArchProps, e.EvalProperty(prop))
			}
			archMap.Properties = newArchProps
			m.Arch[arch] = archMap
		}
	}
	// Evaluate host-specific overrides (host: { ... })
	if m.Host != nil {
		var newHostProps []*Property
		for _, prop := range m.Host.Properties {
			newHostProps = append(newHostProps, e.EvalProperty(prop))
		}
		m.Host.Properties = newHostProps
	}
	// Evaluate target-specific overrides (target: { ... })
	if m.Target != nil {
		var newTargetProps []*Property
		for _, prop := range m.Target.Properties {
			newTargetProps = append(newTargetProps, e.EvalProperty(prop))
		}
		m.Target.Properties = newTargetProps
	}
}
