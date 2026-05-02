// Package props provides property extraction helpers for Blueprint modules.
// It offers functions to retrieve string, list, and boolean property values
// from parsed module definitions, with optional variable evaluation.
//
// Property values in Blueprints may contain variable references (${VAR}),
// select() expressions, and builtin function calls that require evaluation
// through a parser.Evaluator. This package provides both raw access (without
// evaluation) and evaluated access (with variable substitution).
//
// Function categories:
//   - GetStringProp / GetStringPropEval: String property retrieval
//   - GetListProp / GetListPropEval: List property retrieval
//   - GetBoolProp: Boolean property retrieval with optional evaluation
//
// These functions are used throughout the build system to extract and use
// module configuration values when generating ninja build rules. They provide
// a consistent interface for accessing module properties regardless of the
// underlying expression type.
//
// Design notes:
//   - All functions perform linear search through module properties
//   - This is acceptable because Blueprint modules typically have 10-50 properties
//   - Functions are nil-safe: nil Map field returns empty/nil immediately
//   - Type assertions ensure only matching types are returned
//   - All functions treat empty string as "not set" - callers should use
//     separate existence checks if empty values are meaningful
package props

import "minibp/lib/parser"

// GetStringProp retrieves a string property value from a module by name.
// It performs a simple lookup without variable evaluation.
// This function only matches properties that are explicitly declared as String type
// in the Blueprint definition. For properties that may contain variable
// references (e.g., "${VAR}"), use GetStringPropEval instead.
//
// Parameters:
//   - m: The module to search for the property. Must not be nil, but may have
//     a nil Map field (which indicates no properties were defined). When Map is
//     nil, the function returns an empty string immediately.
//   - name: The property name to find. Matching is case-sensitive. The property
//     must be defined as a String type in the Blueprint; properties defined as
//     other types (e.g., list, bool) are ignored.
//
// Returns:
//   - The string value if found and of type string, otherwise empty string.
//     Note that an empty string may also be returned if the property exists
//     but has an empty value (""), so callers should check for property
//     existence separately if empty values are meaningful.
func GetStringProp(m *parser.Module, name string) string {
	// Fast path: no properties defined at all
	if m.Map == nil {
		return "" // No properties defined, return empty
	}
	// O(1) lookup via property map cache
	if prop, ok := m.Map.GetPropMap()[name]; ok { // Found property by name
		// Only match String type; list, bool, map types are ignored
		if s, ok := prop.Value.(*parser.String); ok { // Ensure property is String type
			return s.Value
		}
	}
	// Property not found or wrong type: return empty string
	return ""
}

// GetStringPropEval retrieves a string property value from a module by name,
// with optional variable evaluation via an evaluator. Unlike GetStringProp,
// this function evaluates the property value through the provided Evaluator,
// resolving Blueprint variables (e.g., "${VAR}") and select() expressions
// before returning the result.
//
// Parameters:
//   - m: The module to search for the property. Must not be nil, but may have
//     a nil Map field. When Map is nil, the function returns an empty string.
//   - name: The property name to find. Case-sensitive string matching.
//   - eval: Optional evaluator for resolving variables. If nil, this function
//     behaves identically to GetStringProp and returns the raw string value.
//     When provided, the evaluator processes the property value through its
//     entire evaluation pipeline, which includes: variable substitution
//     (${VAR} or $VAR), select() resolution based on config, and builtin
//     function evaluation (e.g., path()).
//
// Returns:
//   - The evaluated string value if found and of type string, otherwise
//     empty string. Note that if the property exists but the evaluator fails
//     to resolve it (e.g., undefined variable), the raw string is returned
//     as-is rather than an error.
func GetStringPropEval(m *parser.Module, name string, eval *parser.Evaluator) string {
	// Fast path: no properties defined
	if m.Map == nil {
		return "" // No properties defined, return empty
	}
	// O(1) lookup via property map cache
	if prop, ok := m.Map.GetPropMap()[name]; ok { // Found property
		// Only match String type
		if s, ok := prop.Value.(*parser.String); ok { // Check if raw value is String type
			// Evaluate if evaluator provided, otherwise return raw value
			if eval != nil { // Evaluate if evaluator provided
				val := eval.Eval(prop.Value)
				// Check if evaluation returned a string; if eval fails,
				// type assertion fails and we fall through to return empty
				if s, ok := val.(string); ok { // Check evaluation returned string
					return s
				}
			} else {
				return s.Value
			}
		}
	}
	// Property not found or evaluation failed
	return ""
}

// GetListProp retrieves a list property value from a module by name.
// It extracts all string values from the list without variable evaluation.
// This function iterates through the List property and collects each
// element that is a String type. Non-string elements (bool, list, etc.)
// are skipped.
//
// Parameters:
//   - m: The module to search for the property. Must not be nil, but may have
//     a nil Map field (no properties). When Map is nil, returns nil immediately.
//   - name: The property name to find. Case-sensitive. The property must be
//     defined as a List type in the Blueprint; properties of other types are
//     ignored. List elements that are not strings are silently skipped.
//
// Returns:
//   - A slice of string values if found and of type list, otherwise nil.
//     Returns nil (not an empty slice) when the property is not found or
//     contains no string elements. Callers should treat nil as "not found"
//     and distinguish from empty list if needed.
func GetListProp(m *parser.Module, name string) []string {
	// Fast path: no properties defined
	if m.Map == nil {
		return nil // No properties, return nil
	}
	// O(1) lookup via property map cache
	if prop, ok := m.Map.GetPropMap()[name]; ok { // Found property
		// Must be List type
		if l, ok := prop.Value.(*parser.List); ok { // Ensure List type
			var result []string
			// Collect only string elements; other types silently ignored
			for _, v := range l.Values { // Iterate through list values
				if s, ok := v.(*parser.String); ok { // Collect only string elements
					result = append(result, s.Value)
				}
			}
			return result
		}
	}
	// Property not found or wrong type
	return nil
}

// GetListPropEval retrieves a list property value from a module by name,
// with optional variable evaluation via an evaluator. This function evaluates
// each list element through the Evaluator, resolving variables and select()
// expressions within list items.
//
// Parameters:
//   - m: The module to search for the property. Must not be nil, but may have
//     a nil Map field. When Map is nil, returns nil immediately.
//   - name: The property name to find. Case-sensitive. Property must be defined
//     as a List type in the Blueprint.
//   - eval: Optional evaluator for resolving variables. If nil, raw strings are
//     extracted without evaluation (equivalent to GetListProp). When provided,
//     each string element within the list is passed through the evaluator,
//     enabling variable substitution (e.g., "${VAR}"), select() resolution, and
//     path() function evaluation within list items.
//
// Returns:
//   - A slice of evaluated string values if found and of type list,
//     otherwise nil. Elements that fail evaluation are included as-is
//     (the raw string). Returns nil when property not found.
func GetListPropEval(m *parser.Module, name string, eval *parser.Evaluator) []string {
	// Fast path: no properties defined
	if m.Map == nil {
		return nil // No properties, return nil
	}
	// O(1) lookup via property map cache
	if prop, ok := m.Map.GetPropMap()[name]; ok { // Found property
		// Must be List type
		if l, ok := prop.Value.(*parser.List); ok { // Ensure List type
			// Use efficient batch evaluation if evaluator provided
			if eval != nil { // Use batch evaluation if available
				return parser.EvalToStringList(l, eval)
			}
			// Otherwise extract raw strings only
			var result []string
			for _, v := range l.Values { // Iterate through list values
				if s, ok := v.(*parser.String); ok { // Collect string elements
					result = append(result, s.Value)
				}
			}
			return result
		}
	}
	// Property not found
	return nil
}

// GetBoolProp retrieves a boolean property value from a module by name.
// It first checks for a direct Bool value, then attempts evaluation if
// an evaluator is provided. Unlike string properties, boolean properties
// in Blueprints often contain expressions (e.g., "!srcs" or "env.PARTITION"),
// so evaluation is more commonly needed.
//
// Parameters:
//   - m: The module to search for the property. Must not be nil, but may have
//     a nil Map field. When Map is nil, returns false immediately.
//   - name: The property name to find. Case-sensitive. Property can be defined
//     as either a Bool type (literal true/false) or a string type containing
//     a boolean expression.
//   - eval: Optional evaluator for resolving boolean expressions. If nil,
//     only direct Bool type values are checked. When provided, the evaluator
//     processes the property value through its expression evaluation pipeline,
//     supporting: boolean operators (!, &&, ||), comparison operators (==, !=,
//     <, >, <=, >=), and config-based conditionals. Note that string properties
//     like "true" or "false" need evaluation if they were defined as string type.
//
// Returns:
//   - The boolean value if found, otherwise false. Returns false for both
//     "not found" and "explicitly set to false" cases. Callers needing to
//     distinguish should check for property existence separately. Note that
//     evaluation errors (e.g., undefined variables) result in returning false.
//
// Edge cases:
//   - Undefined variables in expression return false silently.
//   - Type assertions that fail (e.g., list where bool expected) return false.
//   - Empty string property "false" evaluates to false, "true" evaluates to true,
//     but non-boolean strings like "" or "foo" evaluate based on Go truthiness:
//     empty strings are false, non-empty are true.
func GetBoolProp(m *parser.Module, name string, eval *parser.Evaluator) bool {
	// Fast path: no properties defined
	if m.Map == nil {
		return false // No properties defined, return false
	}
	// O(1) lookup via property map cache
	if prop, ok := m.Map.GetPropMap()[name]; ok { // Found property
		// First check for literal Bool type (most common for defaults)
		if b, ok := prop.Value.(*parser.Bool); ok { // Check literal Bool type first
			return b.Value
		}
		// If evaluator provided, try evaluating string expressions
		// This handles cases like: enabled: "!srcs" or enabled: "${IS_ENABLED}"
		if eval != nil { // Evaluate expression if evaluator provided
			val := eval.Eval(prop.Value)
			if b, ok := val.(bool); ok { // Check evaluation returned bool
				return b
			}
		}
	}
	// Property not found or evaluation failed
	return false
}
