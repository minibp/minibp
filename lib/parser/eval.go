// Package parser provides lexical analysis and parsing for Blueprint build definitions.
// Eval subpackage - AST evaluation and expression processing.
//
// This file contains the Evaluator which transforms AST nodes into
// Go values. It handles variable resolution, string interpolation,
// operator evaluation, and select() conditional expressions.
// The evaluator is the bridge between the parsed AST and the build system's
// runtime data structures.
package parser

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
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
var UnsetSentinel = &struct{ name string }{name: "unset"}

// Evaluator evaluates Blueprint AST nodes into Go values.
// It maintains a map of variables and configuration values that are
// used during evaluation of expressions, property values, and select statements.
//
// The evaluator supports:
//   - Variable assignment and resolution
//   - String interpolation with ${var} syntax
//   - Binary operators (+, -, *)
//   - select() conditional expressions
//   - Configuration-specific variant selection
type Evaluator struct {
	vars         map[string]interface{} // Variable table: name -> value
	config       map[string]string      // Configuration: key (arch, os, target) -> value
	strictSelect bool                   // If true, select() with no matching case and no default is an error
	selectErrors []error                // Errors from select() evaluation when strictSelect is true
}

// NewEvaluator creates a new Evaluator with empty variable and config maps.
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
// should produce an error. When strict is true (the default), unmatched select() expressions
// will be collected in SelectErrors for later reporting.
// When strict is false, unmatched select() returns nil silently.
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
//   - string: String values
//   - int64: Integer values
//   - bool: Boolean values
//   - []string: List of strings
//   - []interface{}: Generic list
//   - map[string]interface{}: Map/dictionary
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
//   - defs: A list of definitions to process
func (e *Evaluator) ProcessAssignmentsFromDefs(defs []Definition) {
	for _, def := range defs {
		assign, ok := def.(*Assignment)
		if !ok {
			continue // Skip non-assignment definitions
		}
		val := e.Eval(assign.Value)

		// Handle += operator - concatenate to existing variable
		if assign.Assigner == "+=" {
			if existing, ok := e.vars[assign.Name]; ok {
				switch ev := existing.(type) {
				case string:
					// String concatenation: name += "suffix"
					if nv, ok := val.(string); ok {
						e.vars[assign.Name] = ev + nv
					}
				case []string:
					// List concatenation with string: name += "item"
					if nv, ok := val.(string); ok {
						e.vars[assign.Name] = append(ev, nv)
					} else if nv, ok := val.([]string); ok {
						// List concatenation with list: name += ["a", "b"]
						e.vars[assign.Name] = append(ev, nv...)
					}
				case []interface{}:
					// Generic list concatenation
					if nv, ok := val.([]interface{}); ok {
						e.vars[assign.Name] = append(ev, nv...)
					} else {
						// Append single value as interface{}
						e.vars[assign.Name] = append(ev, val)
					}
				}
			} else {
				// First += creates the variable (treat as simple assignment)
				e.vars[assign.Name] = val
			}
		} else {
			// Simple assignment (=)
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
//   - map + map: map merge
//   - '-' operator:
//   - int64 - int64: integer subtraction
//   - '*' operator:
//   - int64 * int64: integer multiplication
//
// Returns nil if the operator is not supported for the given types.
//
// Parameters:
//   - left: Left operand
//   - right: Right operand
//   - op: The operator rune ('+', '-', '*')
//
// Returns:
//   - interface{}: Result of the operation, or nil if unsupported
func evalOperator(left, right interface{}, op rune) interface{} {
	switch op {
	case '+':
		// Try integer addition first
		li, lok := left.(int64)
		ri, rok := right.(int64)
		if lok && rok {
			return li + ri
		}
		// Try string concatenation
		ls, lok := left.(string)
		rs, rok := right.(string)
		if lok && rok {
			return ls + rs
		}
		// Try list concatenation: []interface{} + []interface{}
		ll, lok := toInterfaceList(left)
		rl, rok := toInterfaceList(right)
		if lok && rok {
			return append(ll, rl...)
		}
		// Try list + scalar: []interface{} + scalar
		if lok && !rok {
			return append(ll, right)
		}
		// Try scalar + list: scalar + []interface{}
		if !lok && rok {
			return append([]interface{}{left}, rl...)
		}
		// Try map merge: map[string]interface{} + map[string]interface{}
		lm, lok := left.(map[string]interface{})
		rm, rok := right.(map[string]interface{})
		if lok && rok {
			result := make(map[string]interface{})
			for k, v := range lm {
				result[k] = v
			}
			for k, v := range rm {
				if existing, exists := result[k]; exists {
					// Recursively merge nested values
					result[k] = mergeValues(existing, v)
				} else {
					result[k] = v
				}
			}
			return result
		}
	case '-':
		// Integer subtraction
		li, lok := left.(int64)
		ri, rok := right.(int64)
		if lok && rok {
			return li - ri
		}
	case '*':
		// Integer multiplication
		li, lok := left.(int64)
		ri, rok := right.(int64)
		if lok && rok {
			return li * ri
		}
	}
	return nil
}

// toInterfaceList converts various slice types to []interface{} for unified handling.
// It handles []interface{} directly, []string by converting each element, and returns false for other types.
// This allows the operator evaluation to work with different slice representations uniformly.
//
// Parameters:
//   - v: The value to convert (must be []interface{} or []string)
//
// Returns:
//   - []interface{}: The converted slice, or nil if conversion not possible
//   - bool: true if conversion was successful
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
//   - existing: The value already in the map
//   - incoming: The new value being merged
//
// Returns:
//   - interface{}: The merged result
func mergeValues(existing, incoming interface{}) interface{} {
	// Lists are appended
	el, eok := toInterfaceList(existing)
	il, iok := toInterfaceList(incoming)
	if eok && iok {
		return append(el, il...)
	}
	// Maps are recursively merged
	em, eok := existing.(map[string]interface{})
	im, iok := incoming.(map[string]interface{})
	if eok && iok {
		result := make(map[string]interface{})
		for k, v := range em {
			result[k] = v
		}
		for k, v := range im {
			if existingVal, exists := result[k]; exists {
				result[k] = mergeValues(existingVal, v)
			} else {
				result[k] = v
			}
		}
		return result
	}
	// Scalars are overridden
	return incoming
}

// toString converts a value to its string representation.
// Handles string, []string (joined with space), and other types (formatted as %v).
// The second return value is always true for interface compatibility.
//
// Parameters:
//   - v: The value to convert
//
// Returns:
//   - string: The string representation
//   - bool: Always returns true (for interface compatibility)
func toString(v interface{}) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case []string:
		// Join string slice with spaces
		return strings.Join(s, " "), true
	default:
		// Use default formatting for other types
		return fmt.Sprintf("%v", v), true
	}
}

// interpolateString performs variable substitution in a string.
// It replaces ${variable_name} patterns with the variable's value.
// If a variable is not found, the pattern is left unchanged in the result.
// This allows strings to contain multiple variable references.
//
// Parameters:
//   - s: The string with potential ${...} interpolation patterns
//
// Returns:
//   - string: The string with variables substituted
func (e *Evaluator) interpolateString(s string) string {
	// Quick check - if no ${, return as-is
	if !strings.Contains(s, "${") {
		return s
	}

	// Replace all ${var} patterns with variable values
	return interpolationPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := interpolationPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		name := parts[1]
		val, ok := e.vars[name]
		if !ok {
			// Variable not found - leave pattern unchanged
			return match
		}
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
//   - s: The Select AST node to evaluate
//
// Returns:
//   - interface{}: The value from the matching case, or nil if no match
func (e *Evaluator) evalSelect(s *Select) interface{} {
	// Guard against empty conditions or cases
	if len(s.Conditions) == 0 || len(s.Cases) == 0 {
		return nil
	}

	// Evaluate all conditions
	condValues := make([]interface{}, len(s.Conditions))
	for i, cond := range s.Conditions {
		condValues[i] = e.evalSelectCondition(cond)
	}

	// Single variable select vs multi-variable (tuple) select
	if len(condValues) == 1 {
		return e.evalSelectSingle(s, condValues[0])
	}

	// Multi-variable select
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
//   - s: The Select AST node
//   - configValue: The evaluated configuration value to match against patterns
//
// Returns:
//   - interface{}: The value from the matching case, or nil
func (e *Evaluator) evalSelectSingle(s *Select, configValue interface{}) interface{} {
	// First pass: look for non-default pattern match
	for _, c := range s.Cases {
		for _, p := range c.Patterns {
			// Skip default and any patterns in first pass
			if e.isDefaultPattern(p) || e.isAnyPattern(p) {
				continue
			}
			// Handle unset pattern
			if e.isUnsetPattern(p) {
				if configValue == nil || configValue == "" {
					return e.evalCaseValue(c, configValue)
				}
				continue
			}
			// Exact match comparison
			if p.Value != nil && reflect.DeepEqual(e.evalPatternValue(p.Value), configValue) {
				return e.evalCaseValue(c, configValue)
			}
		}
	}

	// Second pass: look for "any" pattern
	for _, c := range s.Cases {
		for _, p := range c.Patterns {
			if e.isAnyPattern(p) && !e.isConfigUnset(configValue) {
				// Bind matched value if pattern has binding
				if p.Binding != "" {
					e.SetVar(p.Binding, configValue)
				}
				return e.Eval(c.Value)
			}
		}
	}

	// Third pass: look for default case
	for _, c := range s.Cases {
		if len(c.Patterns) == 1 && e.isDefaultPattern(c.Patterns[0]) {
			return e.evalCaseValue(c, configValue)
		}
	}

	// No match and no default - record error in strict mode
	if e.strictSelect && len(s.Conditions) > 0 {
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
//   - s: The Select AST node
//   - condValues: The list of evaluated configuration values
//
// Returns:
//   - interface{}: The value from the matching case, or nil
func (e *Evaluator) evalSelectMulti(s *Select, condValues []interface{}) interface{} {
	// First pass: look for non-default tuple pattern match
	for _, c := range s.Cases {
		// Skip cases with wrong number of patterns
		if len(c.Patterns) != len(condValues) {
			continue
		}

		allMatch := true
		hasDefault := false
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

		// Skip if mixed defaults with non-matching
		if hasDefault && !allMatch {
			continue
		}

		// Try exact match for all patterns
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
				continue
			}
			if e.isUnsetPattern(p) {
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

	// Third pass: look for default case
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
//   - c: The SelectCase to evaluate
//   - matchedValue: The value that matched the pattern (for binding)
//
// Returns:
//   - interface{}: The evaluated case value, or UnsetSentinel
func (e *Evaluator) evalCaseValue(c SelectCase, matchedValue interface{}) interface{} {
	val := e.Eval(c.Value)
	if val == UnsetSentinel {
		return UnsetSentinel
	}
	// If the value is a string and contains the matched value binding, substitute it
	if matchedValue != nil {
		// Check if any pattern in this case had a binding
		for _, p := range c.Patterns {
			if p.Binding != "" {
				e.SetVar(p.Binding, matchedValue)
			}
		}
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
// Conditions can be:
//   - Empty: uses the first argument as the value
//   - Built-in functions: target, arch, host, os - look up from config
//   - soong_config_variable(namespace, variable): Soong config variable
//   - release_flag(flag): Release flag value
//   - variant(name): Variant-specific value
//   - product_variable(name): Product variable value
//   - Custom: any other identifier looks up from config or vars
//
// Parameters:
//   - cond: The condition to evaluate
//
// Returns:
//   - interface{}: The evaluated condition value
func (e *Evaluator) evalSelectCondition(cond ConfigurableCondition) interface{} {
	// Empty condition function - use argument as value
	if cond.FunctionName == "" {
		if len(cond.Args) == 0 {
			return nil
		}
		return e.Eval(cond.Args[0])
	}

	// Handle soong_config_variable(namespace, variable)
	if cond.FunctionName == "soong_config_variable" && len(cond.Args) >= 2 {
		namespace := e.Eval(cond.Args[0])
		variable := e.Eval(cond.Args[1])
		nsStr, _ := namespace.(string)
		varStr, _ := variable.(string)
		if nsStr != "" && varStr != "" {
			key := nsStr + "." + varStr
			if val, ok := e.config[key]; ok {
				return val
			}
			// Also check vars table for soong_config values set via CLI
			if val, ok := e.vars["soong_config."+key]; ok {
				return val
			}
		}
		return nil
	}

	// Handle release_flag(flag)
	if cond.FunctionName == "release_flag" && len(cond.Args) >= 1 {
		flag := e.Eval(cond.Args[0])
		flagStr, _ := flag.(string)
		if flagStr != "" {
			key := "release." + flagStr
			if val, ok := e.config[key]; ok {
				return val
			}
		}
		return nil
	}

	// Handle variant(name)
	if cond.FunctionName == "variant" {
		if len(cond.Args) == 0 {
			return e.config["variant"]
		}
		name := e.Eval(cond.Args[0])
		if nameStr, ok := name.(string); ok && nameStr != "" {
			return e.config["variant."+nameStr]
		}
		return nil
	}

	// Handle product_variable(name)
	if cond.FunctionName == "product_variable" {
		if len(cond.Args) == 0 {
			return e.config["product"]
		}
		name := e.Eval(cond.Args[0])
		if nameStr, ok := name.(string); ok && nameStr != "" {
			return e.config["product."+nameStr]
		}
		return nil
	}

	// Check if it's a variable reference (function name is an empty string)
	if len(cond.Args) == 0 {
		if val, ok := e.vars[cond.FunctionName]; ok {
			return val
		}
	}

	// Switch on built-in function names
	switch cond.FunctionName {
	case "target":
		return e.config["target"]
	case "arch":
		return e.config["arch"]
	case "host":
		return e.config["host"]
	case "os":
		return e.config["os"]
	default:
		// Fall back to config lookup
		return e.config[cond.FunctionName]
	}
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
//   - expr: The pattern expression to evaluate
//
// Returns:
//   - interface{}: The evaluated pattern value
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
		// Return variable name as fallback for config keys
		return v.Name
	default:
		return e.Eval(expr)
	}
}

// EvalToString converts an expression to a string representation.
// If an evaluator is provided, it first evaluates the expression.
// Otherwise, it converts the raw AST node to string.
// This is useful for debugging and generating output.
//
// Parameters:
//   - expr: The expression to convert
//   - eval: Optional evaluator for evaluation
//
// Returns:
//   - string: The string representation of the expression
func EvalToString(expr Expression, eval *Evaluator) string {
	if eval != nil {
		val := eval.Eval(expr)
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)
	}
	// Handle common expression types without evaluation
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
//   - []interface{}: filters to only strings
//   - string: wrapped in a single-element list
//   - Expression: parsed as List and string elements extracted
//
// Parameters:
//   - expr: The expression to convert
//   - eval: Optional evaluator for evaluation
//
// Returns:
//   - []string: List of string values, or nil if conversion not possible
func EvalToStringList(expr Expression, eval *Evaluator) []string {
	if eval == nil {
		return EvalToStringListNoEval(expr)
	}
	val := eval.Eval(expr)
	switch v := val.(type) {
	case []string:
		return v
	case []interface{}:
		// Filter to only string values
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case string:
		return []string{v}
	default:
		return nil
	}
}

// EvalToStringListNoEval extracts string values from a List expression
// without evaluation. This is used when no evaluator is available.
//
// Parameters:
//   - expr: The List expression
//
// Returns:
//   - []string: List of string values, or nil if not a List
func EvalToStringListNoEval(expr Expression) []string {
	if l, ok := expr.(*List); ok {
		var result []string
		for _, item := range l.Values {
			if s, ok := item.(*String); ok {
				result = append(result, s.Value)
			}
		}
		return result
	}
	return nil
}

// EvalProperty evaluates a property's value and returns a new Property with the evaluated value.
// This transforms AST expression nodes into their evaluated Go values.
// The property name and position information are preserved; only the value is evaluated.
// The evaluated value is converted back to an AST node for compatibility
// with other parts of the system.
//
// Parameters:
//   - prop: The property to evaluate
//
// Returns:
//   - *Property: A new property with the evaluated value
func (e *Evaluator) EvalProperty(prop *Property) *Property {
	val := e.Eval(prop.Value)
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
		// Keep original value if conversion not possible
		newProp.Value = prop.Value
	}
	return newProp
}

// EvalModule evaluates all properties in a module, including arch/host/target overrides.
// This performs full evaluation of the module, replacing variable references,
// performing string interpolation, and evaluating select() expressions.
// The module's main properties, arch-specific overrides, host overrides, and target
// overrides are all evaluated in-place.
//
// Parameters:
//   - m: The module to evaluate (modified in place)
func (e *Evaluator) EvalModule(m *Module) {
	if m.Map == nil {
		return
	}
	// Evaluate main module properties
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
	// Evaluate host-specific overrides
	if m.Host != nil {
		var newHostProps []*Property
		for _, prop := range m.Host.Properties {
			newHostProps = append(newHostProps, e.EvalProperty(prop))
		}
		m.Host.Properties = newHostProps
	}
	// Evaluate target-specific overrides
	if m.Target != nil {
		var newTargetProps []*Property
		for _, prop := range m.Target.Properties {
			newTargetProps = append(newTargetProps, e.EvalProperty(prop))
		}
		m.Target.Properties = newTargetProps
	}
}
