// Package ninja provides utilities for generating Ninja build files from Blueprint module definitions.
//
// This file (helpers.go) contains helper functions for extracting and transforming
// module properties into formats suitable for ninja rule generation. These functions
// bridge the gap between the parser's AST representation (parser.Module, parser.Map, etc.)
// and the string/slice values needed by the ninja Writer.
//
// The helpers are organized into several categories:
//
// Property extraction functions retrieve typed values from module property maps:
//   - GetStringProp: Extracts a string property value from a module
//   - GetStringPropEval: Extracts a string property with variable evaluation (${VAR} substitution)
//   - GetListProp: Extracts a list of strings from a module property
//   - GetListPropEval: Extracts a list of strings with variable evaluation
//   - GetMapProp: Extracts a nested map property from a module
//   - GetMapStringListProp: Extracts a string list from a nested map property
//
// Flag and include directory helpers extract build configuration from modules:
//   - getCflags, getCppflags, getLdflags: Extract compiler/linker flags (space-separated)
//   - getGoflags, getJavaflags: Extract language-specific compiler flags
//   - getLto: Extract link-time optimization mode
//   - getLocalIncludeDirs, getSystemIncludeDirs: Extract include search paths
//   - getExportIncludeDirs, getExportedHeaders: Extract exported headers for dependent modules
//
// Go-specific helpers handle Go module target variants:
//   - getGoTargetVariants: Returns list of target variant keys (e.g., "linux_amd64")
//   - getGoTargetProp: Extracts a property from a specific target variant sub-map
//
// Output name generators create unique, platform-appropriate output filenames:
//   - objectOutputName: Generates unique object file names (handles path collisions)
//   - libOutputName: Generates library names with "lib" prefix and arch suffix
//   - sharedLibOutputName: Generates shared library names (.so)
//   - staticLibOutputName: Generates static library names (.a)
//
// Convenience functions provide simplified access to common properties:
//   - getName, getSrcs, getFirstSource, getData, getBoolProp
//   - formatSrcs, joinFlags, copyCommand, getTestOptionArgs
//
// All property extraction functions gracefully handle missing properties and
// type mismatches by returning zero values (empty string, nil slice, false, etc.).
// When an evaluator is provided, variable references (${VAR}) are resolved
// at generation time, enabling dynamic build configurations.
package ninja

import (
	"minibp/lib/parser"
	"path/filepath"
	"runtime"
	"strings"
)

// GetStringProp retrieves a string property value from a module's property map.
//
// This function searches the module's top-level properties for a property with
// the given name and attempts to extract its string value. It handles type
// assertions to ensure the property is actually a string type.
//
// Property lookup is case-sensitive and matches the exact property name as
// defined in the Blueprint file. Only direct properties of the module are
// searched; nested properties in sub-maps require using GetMapProp first.
//
// Parameters:
//   - m: The parser.Module to extract the property from.
//     If nil or m.Map is nil, the function returns empty string immediately.
//   - name: The property name to look for (case-sensitive).
//     Example: "name", "srcs", "cflags".
//
// Returns:
//   - The string value if the property is found and is of type *parser.String.
//   - Empty string ("") if:
//   - The module is nil or has no property map
//   - No property with the given name exists
//   - The property exists but is not a string type (e.g., it's a list or map)
//
// Edge cases:
//   - Returns empty string (not an error) when property type doesn't match.
//     This design allows callers to use simple if-checks without error handling.
//   - Only examines m.Map.Properties; nested maps are not traversed.
//   - If multiple properties have the same name, the first one encountered is returned.
//     Property order follows the order in the Blueprint file.
//
// Example:
//
//	// In Blueprint: my_module { name: "foo", srcs: ["a.c"] }
//	name := GetStringProp(m, "name")  // Returns "foo"
//	srcs := GetStringProp(m, "srcs")  // Returns "" (not a string)
func GetStringProp(m *parser.Module, name string) string {
	// Early return if module has no property map.
	// This avoids nil pointer dereference and simplifies the search loop.
	if m.Map == nil {
		return ""
	}
	// Iterate through all properties to find a name match.
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			// Attempt type assertion to *parser.String.
			// If the property is a different type (List, Map, Bool, etc.),
			// the assertion fails and we continue searching (though typically
			// there's only one property with a given name).
			if s, ok := prop.Value.(*parser.String); ok {
				return s.Value
			}
		}
	}
	// Property not found or wrong type.
	return ""
}

// GetStringPropEval retrieves a string property value with optional variable evaluation.
//
// This function extends GetStringProp by supporting variable evaluation at
// generation time. If an evaluator is provided, any variable references
// in the form ${VAR} or ${NAMESPACE:VAR} within the string are resolved
// before returning the value.
//
// Variable evaluation enables dynamic build configurations where property
// values depend on variables defined elsewhere in the Blueprint files
// (e.g., via assignment statements like "my_var = "value"").
//
// Parameters:
//   - m: The parser.Module to extract the property from.
//     If nil or m.Map is nil, returns empty string immediately.
//   - name: The property name to look for (case-sensitive).
//   - eval: The evaluator for variable resolution.
//     If nil, behaves identically to GetStringProp (no evaluation).
//     If non-nil, calls parser.EvalToString to resolve ${VAR} references.
//
// Returns:
//   - The string value with variables resolved if eval is non-nil.
//   - The raw string value if eval is nil and property is found.
//   - Empty string ("") if:
//   - The module is nil or has no property map
//   - No property with the given name exists
//   - The property exists but is not a string type
//
// Edge cases:
//   - If eval is provided but the string contains no variable references,
//     the original string is returned unchanged (no error).
//   - If variable references cannot be resolved, behavior depends on
//     parser.EvalToString (typically keeps the reference or returns error).
//   - Only examines m.Map.Properties; nested maps require GetMapProp first.
//
// Example:
//
//	// In Blueprint: my_var = "world"
//	// In module: greeting: "hello ${my_var}"
//	// With eval: returns "hello world"
//	// Without eval: returns "hello ${my_var}"
func GetStringPropEval(m *parser.Module, name string, eval *parser.Evaluator) string {
	// Early return if module has no property map.
	if m.Map == nil {
		return ""
	}
	// Iterate through properties to find name match.
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if s, ok := prop.Value.(*parser.String); ok {
				// If evaluator is provided, resolve variable references.
				// parser.EvalToString handles ${VAR} and ${NAMESPACE:VAR} syntax.
				if eval != nil {
					return parser.EvalToString(s, eval)
				}
				// No evaluator: return raw string value as-is.
				return s.Value
			}
		}
	}
	return ""
}

// getBoolProp retrieves a boolean property value from a module's property map.
//
// This function searches for a property with the given name and attempts to
// extract its boolean value. It is commonly used for properties like
// "enabled", "shared", "host_supported", etc.
//
// Parameters:
//   - m: The parser.Module to extract the property from.
//     If nil or m.Map is nil, returns false immediately.
//   - name: The property name to look for (case-sensitive).
//
// Returns:
//   - true if the property is found and is of type *parser.Bool with value true.
//   - false if:
//   - The module is nil or has no property map
//   - No property with the given name exists
//   - The property exists but is not a boolean type
//   - The property is a boolean with value false
//
// Edge cases:
//   - Returns false (not an error) when property type doesn't match.
//     This is consistent with the behavior of other Get*Prop functions.
//   - Only examines m.Map.Properties; nested maps are not traversed.
//   - A property explicitly set to false will return false (same as not found).
//     Callers cannot distinguish between "not found" and "false" with this function.
//
// Example:
//
//	// In Blueprint: my_module { enabled: true, shared: false }
//	enabled := getBoolProp(m, "enabled")  // Returns true
//	shared := getBoolProp(m, "shared")    // Returns false
//	missing := getBoolProp(m, "unknown")   // Returns false
func getBoolProp(m *parser.Module, name string) bool {
	// Early return if module has no property map.
	if m.Map == nil {
		return false
	}
	// Iterate through properties to find name match.
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			// Attempt type assertion to *parser.Bool.
			if b, ok := prop.Value.(*parser.Bool); ok {
				return b.Value
			}
		}
	}
	// Property not found or wrong type; return zero value for bool.
	return false
}

// GetListProp retrieves a list property value from a module's property map.
//
// This function searches for a property with the given name and extracts
// its value as a list of strings. The property must be of type *parser.List,
// and each element in the list must be a *parser.String to be included
// in the result. Non-string elements are silently skipped.
//
// This is commonly used for properties like "srcs", "cflags", "deps",
// "local_include_dirs", etc., which accept a list of string values.
//
// Parameters:
//   - m: The parser.Module to extract the property from.
//     If nil or m.Map is nil, returns nil immediately.
//   - name: The property name to look for (case-sensitive).
//     Example: "srcs", "cflags", "deps".
//
// Returns:
//   - A slice of string values if the property is found and is a list type.
//     Only string elements are included; other types are silently ignored.
//     Returns nil (not an empty slice) if:
//   - The module is nil or has no property map
//   - No property with the given name exists
//   - The property exists but is not a list type
//
// Edge cases:
//   - Returns nil (not []string{}) when property not found.
//     This is a deliberate design choice to allow callers to distinguish
//     "property not set" (nil) from "property is empty list" (empty slice).
//   - Non-string elements in the list are silently dropped.
//     Example: ["a", 123, "b"] returns ["a", "b"].
//   - An empty list property (e.g., srcs: []) returns an empty slice []string{},
//     not nil. This is because the property exists and is the correct type.
//   - If multiple properties have the same name, the first one is returned.
//
// Example:
//
//	// In Blueprint: my_module { srcs: ["a.c", "b.c"], name: "foo" }
//	srcs := GetListProp(m, "srcs")  // Returns ["a.c", "b.c"]
//	names := GetListProp(m, "name")  // Returns nil (not a list)
func GetListProp(m *parser.Module, name string) []string {
	// Early return if module has no property map.
	if m.Map == nil {
		return nil
	}
	// Iterate through properties to find name match.
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if l, ok := prop.Value.(*parser.List); ok {
				// Extract string values from list elements.
				// Non-string elements are silently skipped.
				var result []string
				for _, v := range l.Values {
					if s, ok := v.(*parser.String); ok {
						result = append(result, s.Value)
					}
				}
				return result
			}
		}
	}
	// Property not found or wrong type; return nil to indicate absence.
	return nil
}

// GetListPropEval retrieves a list property value with optional variable evaluation.
//
// This function extends GetListProp by supporting variable evaluation at
// generation time. If an evaluator is provided, variable references
// (${VAR}, ${NAMESPACE:VAR}) in each list item are resolved before
// returning the values.
//
// Variable evaluation is useful for properties like "srcs" or "cflags"
// where values may reference variables defined elsewhere in Blueprint files.
//
// Parameters:
//   - m: The parser.Module to extract the property from.
//     If nil or m.Map is nil, returns nil immediately.
//   - name: The property name to look for (case-sensitive).
//   - eval: The evaluator for variable resolution.
//     If nil, behaves identically to GetListProp (no evaluation).
//     If non-nil, calls parser.EvalToStringList to resolve references.
//
// Returns:
//   - A slice of string values with variables resolved if eval is non-nil.
//   - Raw string values if eval is nil and property is found.
//   - nil (not an empty slice) if:
//   - The module is nil or has no property map
//   - No property with the given name exists
//   - The property exists but is not a list type
//
// Edge cases:
//   - Non-string elements in the list are silently dropped (same as GetListProp).
//   - If eval is provided but list items contain no variable references,
//     the original strings are returned unchanged.
//   - Returns nil (not []string{}) when property not found, allowing
//     callers to distinguish "not set" from "empty list".
//
// Example:
//
//	// In Blueprint: prefix = "src"
//	// In module: srcs: ["${prefix}/a.c", "${prefix}/b.c"]
//	// With eval: returns ["src/a.c", "src/b.c"]
//	// Without eval: returns ["${prefix}/a.c", "${prefix}/b.c"]
func GetListPropEval(m *parser.Module, name string, eval *parser.Evaluator) []string {
	// Early return if module has no property map.
	if m.Map == nil {
		return nil
	}
	// Iterate through properties to find name match.
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if l, ok := prop.Value.(*parser.List); ok {
				// If evaluator is provided, resolve variable references in all list items.
				// parser.EvalToStringList handles ${VAR} and ${NAMESPACE:VAR} syntax for each element.
				if eval != nil {
					return parser.EvalToStringList(l, eval)
				}
				// No evaluator: extract raw string values manually.
				var result []string
				for _, v := range l.Values {
					if s, ok := v.(*parser.String); ok {
						result = append(result, s.Value)
					}
				}
				return result
			}
		}
	}
	return nil
}

// getCflags retrieves C compiler flags from a module's "cflags" property.
//
// This is a convenience function that extracts the "cflags" property (a list
// of strings) and joins them into a single space-separated string suitable
// for passing to a C compiler command line.
//
// C flags typically include warning options (-Wall, -Werror), optimization
// levels (-O2), debug flags (-g), and other compiler-specific options.
//
// Parameters:
//   - m: The parser.Module to extract flags from.
//     If nil or property not found, returns empty string.
//
// Returns:
//   - A space-separated string of all C flags.
//   - Empty string ("") if:
//   - The module is nil or has no property map
//   - No "cflags" property exists
//   - The "cflags" property is not a list type
//   - The "cflags" list is empty
//
// Edge cases:
//   - Returns empty string (not nil) since the return type is string.
//     This is the zero value for strings and is safe to pass to compilers.
//   - Non-string elements in the cflags list are silently dropped.
//   - Flags are joined with a single space; extra spaces in the result
//     are harmless to most compilers.
//
// Example:
//
//	// In Blueprint: cc_library { cflags: ["-Wall", "-O2"] }
//	flags := getCflags(m)  // Returns "-Wall -O2"
func getCflags(m *parser.Module) string {
	// Join the list of C flags with spaces to create a single string
	// suitable for command-line usage (e.g., "gcc -Wall -O2 foo.c").
	return strings.Join(GetListProp(m, "cflags"), " ")
}

// getCppflags retrieves C++ compiler flags from a module's "cppflags" property.
//
// This is a convenience function that extracts the "cppflags" property (a list
// of strings) and joins them into a single space-separated string suitable
// for passing to a C++ compiler command line.
//
// C++ flags typically include warning options (-Wall), standard version
// (-std=c++17), exception handling, RTTI options, and other C++-specific settings.
//
// Parameters:
//   - m: The parser.Module to extract flags from.
//     If nil or property not found, returns empty string.
//
// Returns:
//   - A space-separated string of all C++ flags.
//   - Empty string ("") if the "cppflags" property is missing, wrong type, or empty.
//
// Edge cases:
//   - Non-string elements in the cppflags list are silently dropped.
//   - Returns empty string (not nil) which is safe to pass to compilers.
//
// Example:
//
//	// In Blueprint: cc_library { cppflags: ["-std=c++17", "-fno-exceptions"] }
//	flags := getCppflags(m)  // Returns "-std=c++17 -fno-exceptions"
func getCppflags(m *parser.Module) string {
	// Join C++ flags with spaces for command-line usage (e.g., "g++ -std=c++17 foo.cpp").
	return strings.Join(GetListProp(m, "cppflags"), " ")
}

// getLdflags retrieves linker flags from a module's "ldflags" property.
//
// This is a convenience function that extracts the "ldflags" property (a list
// of strings) and joins them into a single space-separated string suitable
// for passing to a linker command line.
//
// Linker flags typically include library search paths (-L), libraries to link
// (-l), linker scripts, and other linker-specific options (-Wl,--as-needed).
//
// Parameters:
//   - m: The parser.Module to extract flags from.
//     If nil or property not found, returns empty string.
//
// Returns:
//   - A space-separated string of all linker flags.
//   - Empty string ("") if the "ldflags" property is missing, wrong type, or empty.
//
// Edge cases:
//   - Non-string elements in the ldflags list are silently dropped.
//   - Returns empty string (not nil) which is safe to pass to linkers.
//
// Example:
//
//	// In Blueprint: cc_binary { ldflags: ["-L./lib", "-lm"] }
//	flags := getLdflags(m)  // Returns "-L./lib -lm"
func getLdflags(m *parser.Module) string {
	// Join linker flags with spaces for command-line usage (e.g., "ld -L./lib -lm").
	return strings.Join(GetListProp(m, "ldflags"), " ")
}

// getUndefines retrieves undefined macros from a module's "undefines" property.
//
// This function extracts the "undefines" property (a list of strings)
// and adds "-U" prefix to each macro name to create compiler flags
// that will undefine those macros during compilation.
//
// Parameters:
//   - m: The parser.Module to extract undefines from.
//
// Returns:
//   - A space-separated string of -U flags (e.g., "-UFOO -UBAR").
//   - Empty string ("") if the "undefines" property is missing, wrong type, or empty.
//
// Example:
//
//	// In Blueprint: cc_library { undefines: ["FOO", "BAR"] }
//	flags := getUndefines(m)  // Returns "-UFOO -UBAR"
func getUndefines(m *parser.Module) string {
	undefines := GetListProp(m, "undefines")
	if len(undefines) == 0 {
		return ""
	}
	// Add -U prefix to each macro
	for i, macro := range undefines {
		undefines[i] = "-U" + macro
	}
	return strings.Join(undefines, " ")
}

// getGoflags retrieves Go compiler flags from a module's "goflags" property.
//
// This is a convenience function that extracts the "goflags" property (a list
// of strings) and joins them into a single space-separated string suitable
// for passing to the Go compiler (go tool compile) or linker (go tool link).
//
// Go flags typically include build tags (-tags), compiler flags (-gcflags),
// or linker flags (-ldflags) for Go-specific build customization.
//
// Parameters:
//   - m: The parser.Module to extract flags from.
//     If nil or property not found, returns empty string.
//
// Returns:
//   - A space-separated string of all Go flags.
//   - Empty string ("") if the "goflags" property is missing, wrong type, or empty.
//
// Edge cases:
//   - Non-string elements in the goflags list are silently dropped.
//   - Returns empty string (not nil) which is safe to pass to Go tools.
//
// Example:
//
//	// In Blueprint: go_binary { goflags: ["-tags=linux", "-gcflags=-m"] }
//	flags := getGoflags(m)  // Returns "-tags=linux -gcflags=-m"
func getGoflags(m *parser.Module) string {
	// Join Go flags with spaces for command-line usage (e.g., "go build -tags=linux").
	return strings.Join(GetListProp(m, "goflags"), " ")
}

// getLto retrieves the Link-Time Optimization (LTO) mode from a module.
//
// LTO allows the compiler to optimize across compilation units at link time,
// enabling more aggressive optimizations but increasing build time and memory usage.
// This function reads the "lto" property from the module.
//
// Supported LTO modes:
//   - "full" or "true": Full LTO - compiles all objects into a single LTO unit
//   - "thin": Thin LTO - per-module optimization with summary linking (faster)
//   - "": No LTO (default)
//   - "off" or "false": Explicitly disable LTO
//
// Parameters:
//   - m: The parser.Module to extract the LTO mode from.
//     If nil or property not found, returns empty string.
//
// Returns:
//   - The LTO mode string as specified in the module's "lto" property.
//   - Empty string ("") if:
//   - The module is nil or has no property map
//   - No "lto" property exists
//   - The "lto" property is not a string type
//
// Edge cases:
//   - The caller is responsible for interpreting the mode string.
//     This function does not validate that the mode is a recognized value.
//   - Empty string is treated as "no LTO" by the build rules.
//
// Example:
//
//	// In Blueprint: cc_library { lto: "thin" }
//	mode := getLto(m)  // Returns "thin"
func getLto(m *parser.Module) string {
	// Simply delegate to GetStringProp for the "lto" property.
	return GetStringProp(m, "lto")
}

// getLocalIncludeDirs retrieves local include directories from a module.
//
// Local include directories are search paths that are added to the compiler's
// include path with the -I flag. These paths are typically relative to the
// module's source directory and contain headers that are local to the project.
//
// Example: If a module has local_include_dirs: ["include", "src/utils"],
// the compiler will receive -Iinclude -Isrc/utils flags.
//
// Parameters:
//   - m: The parser.Module to extract include directories from.
//     If nil or property not found, returns nil.
//
// Returns:
//   - A slice of local include directory paths from the "local_include_dirs" property.
//   - nil (not an empty slice) if:
//   - The module is nil or has no property map
//   - No "local_include_dirs" property exists
//   - The property exists but is not a list type
//
// Edge cases:
//   - Non-string elements in the list are silently dropped.
//   - Returns nil (not []string{}) when property not found, allowing
//     callers to distinguish "not set" from "empty list".
//   - Paths are returned as-is from the Blueprint file; callers may need
//     to resolve relative paths based on the module's location.
//
// Example:
//
//	// In Blueprint: cc_library { local_include_dirs: ["include"] }
//	dirs := getLocalIncludeDirs(m)  // Returns ["include"]
func getLocalIncludeDirs(m *parser.Module) []string {
	// Delegate to GetListProp for the "local_include_dirs" property.
	return GetListProp(m, "local_include_dirs")
}

// getSystemIncludeDirs retrieves system include directories from a module.
//
// System include directories are search paths for system/third-party headers
// that are added to the compiler's include path with the -isystem flag.
// Headers in system include directories are treated as system headers,
// meaning warnings are typically suppressed for them.
//
// Example: If a module has system_include_dirs: ["/usr/include", "/opt/local/include"],
// the compiler will receive -isystem /usr/include -isystem /opt/local/include flags.
//
// Parameters:
//   - m: The parser.Module to extract include directories from.
//     If nil or property not found, returns nil.
//
// Returns:
//   - A slice of system include directory paths from the "system_include_dirs" property.
//   - nil (not an empty slice) if the property is missing, wrong type, or empty.
//
// Edge cases:
//   - Non-string elements in the list are silently dropped.
//   - Returns nil (not []string{}) when property not found.
//   - Paths are returned as-is; callers may need to resolve relative paths.
//
// Example:
//
//	// In Blueprint: cc_library { system_include_dirs: ["/usr/include"] }
//	dirs := getSystemIncludeDirs(m)  // Returns ["/usr/include"]
func getSystemIncludeDirs(m *parser.Module) []string {
	// Delegate to GetListProp for the "system_include_dirs" property.
	return GetListProp(m, "system_include_dirs")
}

// getGoTargetVariants retrieves target variant keys from a Go module's Target property.
//
// Go modules can have target-specific variants that define different properties
// for different OS/architecture combinations. Each variant is represented as
// a property map under the Target section, with the property name being the
// variant identifier (e.g., "linux_amd64", "darwin_arm64").
//
// A property is considered a variant if:
//   - It has a non-nil value that is a *parser.Map (explicit variant with properties)
//   - OR it has a nil value but a non-empty name (implicit variant parsed from name)
//
// Parameters:
//   - m: The parser.Module representing a Go module.
//     The module should have a Target section with variant definitions.
//     If m is nil or m.Target is nil, returns nil immediately.
//
// Returns:
//   - A slice of target variant keys (e.g., ["linux_amd64", "darwin_arm64"]).
//   - nil (not an empty slice) if:
//   - The module is nil or has no Target section
//   - No variant properties are found
//
// Edge cases:
//   - Properties with nil values but valid names are treated as potential variants.
//     This handles cases where the variant is defined by name only (e.g., "linux_amd64: ").
//   - If a property has a non-Map value (e.g., a String or List), it's not treated as a variant.
//   - Duplicate variant names are possible if the Blueprint file has duplicates;
//     they will appear multiple times in the result.
//   - The order of keys follows the order of properties in the Target section.
//
// Example:
//
//	// In Blueprint:
//	// go_binary {
//	//   target: {
//	//     linux_amd64: { goos: "linux" },
//	//     darwin_arm64: { goos: "darwin" },
//	//   }
//	// }
//	variants := getGoTargetVariants(m)  // Returns ["linux_amd64", "darwin_arm64"]
func getGoTargetVariants(m *parser.Module) []string {
	// Early return if module has no Target section.
	if m.Target == nil {
		return nil
	}
	var keys []string
	// Iterate through all properties in the Target section.
	for _, p := range m.Target.Properties {
		// Check if the property appears to be a variant.
		// A variant typically has a Map value containing variant-specific properties.
		if p.Value != nil {
			// Property has a value: check if it's a Map (variant with properties).
			if _, ok := p.Value.(*parser.Map); ok {
				keys = append(keys, p.Name)
			}
			// Non-Map values (String, List, Bool) are not considered variants.
		} else {
			// Property has nil value: still check the Name as it might be a variant.
			// This handles cases like "linux_amd64: " where the value is empty.
			if p.Name != "" {
				keys = append(keys, p.Name)
			}
		}
	}
	return keys
}

// getGoTargetProp extracts a string property from a specific target variant sub-map.
//
// Go modules can have target-specific properties nested under variant maps
// in the Target section. This function extracts a specific property from
// a variant's property map, or infers it from the variant name if no
// explicit properties are defined.
//
// When the variant has no explicit value (nil), the function attempts to
// infer "goos" and "goarch" from the variant name itself. The variant name
// is expected to be in the format "os_arch" (e.g., "linux_amd64").
//
// Parameters:
//   - m: The parser.Module representing a Go module.
//     If nil or m.Target is nil, returns empty string immediately.
//   - variant: The target variant name to look up (e.g., "linux_amd64", "darwin_arm64").
//     This should match a property name in the Target section.
//   - prop: The property name to extract from the variant's sub-map.
//     Special values "goos" and "goarch" can be inferred from the variant name.
//
// Returns:
//   - The property value as a string if found in the variant's sub-map.
//   - For "goos" property: the OS part from the variant name (e.g., "linux" from "linux_amd64").
//   - For "goarch" property: the architecture part from the variant name (e.g., "amd64" from "linux_amd64").
//   - Empty string ("") if:
//   - The module is nil or has no Target section
//   - No variant with the given name exists
//   - The variant exists but the property is not found
//   - The property exists but is not a string type
//   - Variant name doesn't have at least 2 parts for goos/goarch inference
//
// Edge cases:
//   - If the variant has a nil value, only "goos" and "goarch" can be inferred.
//     Other property requests return empty string.
//   - Variant names with more than 2 parts (e.g., "linux_amd64_v2") will have
//     parts[0]="linux", parts[1]="amd64_v2". This may not be the intended behavior.
//   - If the variant has an explicit sub-map with "goos" or "goarch" properties,
//     those take precedence over inference from the variant name.
//
// Example:
//
//	// In Blueprint:
//	// go_library {
//	//   target: {
//	//     linux_amd64: { goos: "linux", goarch: "amd64" },
//	//     darwin_arm64: { },
//	//   }
//	// }
//	getGoTargetProp(m, "linux_amd64", "goos")   // Returns "linux" (from sub-map)
//	getGoTargetProp(m, "darwin_arm64", "goos")  // Returns "darwin" (inferred from name)
//	getGoTargetProp(m, "darwin_arm64", "foo")   // Returns "" (nil value, no "foo" to infer)
func getGoTargetProp(m *parser.Module, variant, prop string) string {
	// Early return if module has no Target section.
	if m.Target == nil {
		return ""
	}
	// Search for the variant by name in the Target section.
	for _, p := range m.Target.Properties {
		if p.Name != variant {
			continue
		}
		// If the variant has no explicit value, infer properties from the variant name.
		// This handles cases like "darwin_arm64: " where the value is nil.
		if p.Value == nil {
			// Split variant name by underscore: "linux_amd64" -> ["linux", "amd64"].
			parts := strings.Split(variant, "_")
			// Need at least 2 parts for goos and goarch inference.
			if len(parts) >= 2 {
				if prop == "goos" {
					return parts[0]
				}
				if prop == "goarch" {
					return parts[1]
				}
			}
			// Property not inferable from variant name with nil value.
			return ""
		}
		// Variant has an explicit value: it should be a Map containing properties.
		sub, ok := p.Value.(*parser.Map)
		if !ok {
			// Value exists but is not a Map (unexpected for variants).
			return ""
		}
		// Search for the requested property in the variant's sub-map.
		for _, sp := range sub.Properties {
			if sp.Name == prop {
				// Extract string value from the property.
				if s, ok := sp.Value.(*parser.String); ok {
					return s.Value
				}
			}
		}
	}
	// Variant not found or property not found in variant.
	return ""
}

// getJavaflags retrieves Java compiler flags from a module's "javaflags" property.
//
// This is a convenience function that extracts the "javaflags" property (a list
// of strings) and joins them into a single space-separated string suitable
// for passing to a Java compiler (javac) or other Java tooling.
//
// Java flags typically include release targets (-release 8), classpath (-cp),
// and other Java-specific compiler options.
//
// Parameters:
//   - m: The parser.Module to extract flags from.
//     If nil or property not found, returns empty string.
//
// Returns:
//   - A space-separated string of all Java flags.
//   - Empty string ("") if the "javaflags" property is missing, wrong type, or empty.
//
// Edge cases:
//   - Non-string elements in the javaflags list are silently dropped.
//   - Returns empty string (not nil) which is safe to pass to Java tools.
//
// Example:
//
//	// In Blueprint: java_library { javaflags: ["-release", "8"] }
//	flags := getJavaflags(m)  // Returns "-release 8"
func getJavaflags(m *parser.Module) string {
	// Join Java flags with spaces for command-line usage (e.g., "javac -release 8 Foo.java").
	return strings.Join(GetListProp(m, "javaflags"), " ")
}

// getExportIncludeDirs retrieves exported include directories from a module.
//
// Exported include directories are made available to modules that depend
// on this module. When another module depends on this one, these include
// paths are added to the dependent module's compiler flags (with -I or -isystem).
//
// This is commonly used for C/C++ header libraries that need to expose
// their headers to consumers without requiring them to know the exact path.
//
// Parameters:
//   - m: The parser.Module to extract exported include directories from.
//     If nil or property not found, returns nil.
//
// Returns:
//   - A slice of exported include directory paths from "export_include_dirs" property.
//   - nil (not an empty slice) if:
//   - The module is nil or has no property map
//   - No "export_include_dirs" property exists
//   - The property exists but is not a list type
//
// Edge cases:
//   - Non-string elements in the list are silently dropped.
//   - Returns nil (not []string{}) when property not found, allowing
//     callers to distinguish "not set" from "empty list".
//   - Paths are relative to the module's directory; callers may need
//     to resolve them based on the module's location.
//
// Example:
//
//	// In Blueprint: cc_library { export_include_dirs: ["include"] }
//	dirs := getExportIncludeDirs(m)  // Returns ["include"]
//	// Dependent modules will get -Iinclude flag when depending on this module.
func getExportIncludeDirs(m *parser.Module) []string {
	// Delegate to GetListProp for the "export_include_dirs" property.
	return GetListProp(m, "export_include_dirs")
}

// getExportCflags retrieves exported C flags from a module.
//
// Exported C flags are added to the compilation flags of modules that depend on this module.
// This allows a library to specify compiler flags that its consumers need (e.g., -D macros).
//
// Parameters:
//   - m: The parser.Module to extract exported C flags from.
//
// Returns:
//   - A slice of exported C flags from "export_cflags" property.
//   - nil (not an empty slice) if property not found or module is nil.
func getExportCflags(m *parser.Module) []string {
	return GetListProp(m, "export_cflags")
}

// getExportLdflags retrieves exported linker flags from a module.
//
// Exported linker flags are added to the linker flags of modules that depend on this module.
// This allows a library to specify linker flags that its consumers need (e.g., -L, -l flags).
//
// Parameters:
//   - m: The parser.Module to extract exported linker flags from.
//
// Returns:
//   - A slice of exported linker flags from "export_ldflags" property.
//   - nil (not an empty slice) if property not found or module is nil.
func getExportLdflags(m *parser.Module) []string {
	return GetListProp(m, "export_ldflags")
}

// getExportedHeaders retrieves exported header file paths from a module.
//
// Exported header files are made available to modules that depend on this module.
// During the build, these headers are typically installed or copied to a
// location where dependents can include them (e.g., export_include_dirs).
//
// This is commonly used when a library wants to expose specific headers
// to its consumers without exposing all internal headers.
//
// Parameters:
//   - m: The parser.Module to extract exported headers from.
//     If nil or property not found, returns nil.
//
// Returns:
//   - A slice of exported header file paths from "exported_headers" property.
//   - nil (not an empty slice) if:
//   - The module is nil or has no property map
//   - No "exported_headers" property exists
//   - The property exists but is not a list type
//
// Edge cases:
//   - Non-string elements in the list are silently dropped.
//   - Returns nil (not []string{}) when property not found.
//   - File paths are returned as-is; callers may need to resolve
//     relative paths based on the module's location.
//
// Example:
//
//	// In Blueprint: cc_library { exported_headers: ["include/foo.h"] }
//	headers := getExportedHeaders(m)  // Returns ["include/foo.h"]
func getExportedHeaders(m *parser.Module) []string {
	// Delegate to GetListProp for the "exported_headers" property.
	return GetListProp(m, "exported_headers")
}

// getName retrieves the module's name from its "name" property.
//
// This is a convenience wrapper around GetStringProp that specifically
// extracts the "name" property, which is a required property for most
// module types and serves as the module's unique identifier.
//
// Parameters:
//   - m: The parser.Module to extract the name from.
//     If nil, returns empty string immediately.
//
// Returns:
//   - The module name string from the "name" property.
//   - Empty string ("") if:
//   - The module is nil or has no property map
//   - No "name" property exists
//   - The "name" property is not a string type
//
// Edge cases:
//   - Returns empty string (not an error) when name is missing or wrong type.
//     Callers should validate that the name is non-empty if required.
//   - The name is used as a key in the build graph and for generating
//     output file names; empty names may cause issues downstream.
//
// Example:
//
//	// In Blueprint: cc_library { name: "mylib", srcs: ["foo.c"] }
//	name := getName(m)  // Returns "mylib"
func getName(m *parser.Module) string {
	// Delegate to GetStringProp for the "name" property.
	return GetStringProp(m, "name")
}

// getSrcs retrieves source file paths from a module's "srcs" property.
//
// Source files are the primary input files for compilation or processing.
// The "srcs" property is one of the most commonly used properties
// and is required for most module types (cc_library, go_binary, etc.).
//
// Parameters:
//   - m: The parser.Module to extract source files from.
//     If nil or property not found, returns nil.
//
// Returns:
//   - A slice of source file paths from the "srcs" property.
//   - nil (not an empty slice) if:
//   - The module is nil or has no property map
//   - No "srcs" property exists
//   - The property exists but is not a list type
//
// Edge cases:
//   - Non-string elements in the list are silently dropped.
//   - Returns nil (not []string{}) when property not found, allowing
//     callers to distinguish "not set" from "empty list".
//   - Glob patterns in srcs should be expanded before calling this function.
//     This function returns the raw list as defined in the Blueprint file.
//
// Example:
//
//	// In Blueprint: cc_library { srcs: ["foo.c", "bar.c"] }
//	srcs := getSrcs(m)  // Returns ["foo.c", "bar.c"]
func getSrcs(m *parser.Module) []string {
	// Delegate to GetListProp for the "srcs" property.
	return GetListProp(m, "srcs")
}

// formatSrcs combines source file paths into a single space-separated string.
//
// This is a convenience function for building command-line arguments
// that accept multiple source files. It joins the slice elements
// with a single space, creating a string suitable for passing to
// compilers or other tools.
//
// Parameters:
//   - srcs: Slice of source file paths to combine.
//     Can be nil or empty; both result in an empty string.
//
// Returns:
//   - A single space-separated string of all source paths.
//   - Empty string ("") if srcs is nil or empty.
//
// Edge cases:
//   - Returns empty string (not nil) since the return type is string.
//   - Nil slice and empty slice both produce empty string.
//   - Paths are joined with a single space; extra spaces in the
//     result are harmless to most command-line tools.
//
// Example:
//
//	formatSrcs([]string{"a.c", "b.c"})  // Returns "a.c b.c"
//	formatSrcs(nil)                      // Returns ""
//	formatSrcs([]string{})                // Returns ""
func formatSrcs(srcs []string) string {
	// Join source paths with spaces for command-line usage (e.g., "gcc a.c b.c").
	return strings.Join(srcs, " ")
}

// objectOutputName generates a unique object file name for a source file.
//
// This function ensures each source file maps to a distinct output object file,
// even when multiple source files have the same base name but different paths.
// It handles path normalization, character replacement for filesystem safety,
// and module-name prefixing for uniqueness.
//
// The name generation process:
//  1. Clean the path using filepath.Clean to resolve "." and ".." components.
//  2. Remove leading "./" and "../" prefixes for cleaner names.
//  3. Remove the file extension to get the base name.
//  4. Replace path separators (/, \) and special characters (:, space) with underscores.
//  5. Trim leading dots and underscores from the resulting name.
//  6. If the name is empty after cleaning, use "obj" as the default.
//  7. Prefix with module name if the name doesn't already start with it.
//
// This ensures that "src/foo/bar.c" and "tests/foo/bar.c" produce different
// object names (e.g., "mylib_src_foo_bar.o" and "mylib_tests_foo_bar.o").
//
// Parameters:
//   - moduleName: The name of the module containing this source file.
//     Used as a prefix to ensure uniqueness across modules.
//     Example: "mylib", "foo"
//   - src: The source file path, which can be relative or absolute,
//     with or without extension. Example: "src/foo/bar.c", "../lib/utils.c"
//
// Returns:
//   - A unique object file name ending in ".o".
//     Examples: "mylib_foo.o", "mylib_src_utils_bar.o", "mylib_obj.o"
//
// Edge cases:
//   - Empty source path after cleaning results in "obj.o" (with module prefix).
//   - Source files with no extension produce names without double dots.
//   - Paths with only dots (e.g., "././.") result in "mylib_obj.o".
//   - If srcName already starts with moduleName, no additional prefix is added
//     to avoid doubling (e.g., "mylib_mylib_foo.o" is avoided).
//   - Special characters in paths are replaced with underscores, which may
//     cause collisions if paths differ only in those characters.
//
// Example:
//
//	objectOutputName("mylib", "src/foo/bar.c")    // Returns "mylib_src_foo_bar.o"
//	objectOutputName("mylib", "tests/foo/bar.c")  // Returns "mylib_tests_foo_bar.o"
//	objectOutputName("mylib", "./bar.c")          // Returns "mylib_bar.o"
//	objectOutputName("mylib", "../lib/bar.c")     // Returns "mylib__lib_bar.o"
var srcNameReplacer = strings.NewReplacer(
	"/", "_",
	"\\", "_",
	":", "_",
	" ", "_",
)

func objectOutputName(moduleName, src string) string {
	// Normalize the path by resolving "." and ".." components.
	// This ensures consistent names regardless of how the path is written.
	clean := filepath.Clean(src)
	// Remove common relative path prefixes for cleaner object names.
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "../")
	// Remove file extension to get the base name for the object file.
	srcName := strings.TrimSuffix(clean, filepath.Ext(clean))
	// Replace path separators and special characters with underscores.
	// This ensures the name is safe for use as a filename on all platforms.
	srcName = srcNameReplacer.Replace(srcName)
	// Remove leading dots and underscores that may result from path cleaning.
	// Example: "./../foo" might become "__foo" after replacement.
	srcName = strings.Trim(srcName, "._")
	// Fallback to "obj" if the name is empty after all cleaning.
	// This handles edge cases like src being "." or "..".
	if srcName == "" {
		srcName = "obj"
	}
	// Avoid double-prefixing: if the name already starts with moduleName,
	// don't add the prefix again. This prevents "mylib_mylib_foo.o".
	if strings.HasPrefix(srcName, moduleName) || srcName == moduleName {
		return srcName + ".o"
	}
	// Prefix with module name to ensure uniqueness across modules
	// and make it easy to identify which module an object belongs to.
	return moduleName + "_" + srcName + ".o"
}

// joinFlags combines multiple flag strings into a single space-separated string.
//
// This function is used to merge compiler or linker flags from multiple sources
// (e.g., module flags + global flags + variant flags) into a single string
// suitable for command-line usage. Empty or whitespace-only parts are filtered out.
//
// Parameters:
//   - parts: Variable number of flag strings to combine.
//     Each part can be a single flag or a space-separated list of flags.
//     Example: joinFlags("-Wall", "", " -O2 ", "-g")
//
// Returns:
//   - A single space-separated string of all non-empty flags.
//   - Empty string ("") if all parts are empty or whitespace-only.
//
// Edge cases:
//   - Whitespace-only strings (e.g., "  ", "\t") are treated as empty and filtered out.
//   - Multiple spaces between flags in a single part are preserved (not normalized).
//   - If a part contains internal whitespace, it's kept as-is (e.g., "-DNAME=val").
//   - Returns empty string (not nil) since the return type is string.
//
// Example:
//
//	joinFlags("-Wall", "-O2", "")       // Returns "-Wall -O2"
//	joinFlags("", "  ")                  // Returns ""
//	joinFlags("-Wall", "-O2", "-g")      // Returns "-Wall -O2 -g"
func joinFlags(parts ...string) string {
	// Pre-allocate slice with capacity to avoid unnecessary reallocations.
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		// Trim whitespace to detect empty or whitespace-only strings.
		part = strings.TrimSpace(part)
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	// Join all non-empty parts with a single space.
	return strings.Join(filtered, " ")
}

// libOutputName generates the output file name for a library.
//
// This function ensures the library name has the conventional "lib" prefix
// (adding it if not present) and appends the architecture suffix and file
// extension to create a unique, platform-appropriate output name.
//
// The naming convention follows standard library naming:
//   - Base name: "foo" -> "libfoo"
//   - With arch suffix: "libfoo" + "_arm64" = "libfoo_arm64"
//   - With extension: "libfoo_arm64" + ".a" = "libfoo_arm64.a"
//
// Parameters:
//   - name: Base library name (e.g., "foo", "libbar").
//     If it doesn't start with "lib", the prefix is added automatically.
//   - archSuffix: Architecture suffix for multi-arch builds.
//     Typically includes a leading underscore (e.g., "_arm64", "_amd64").
//     Can be empty for single-arch builds.
//   - ext: File extension including the dot (e.g., ".a", ".so", ".dylib").
//     Should include the leading dot for consistency.
//
// Returns:
//   - Full library output name (e.g., "libfoo_arm64.a", "libbar.so").
//
// Edge cases:
//   - If name already starts with "lib", no additional prefix is added
//     (avoids "liblibfoo" situation).
//   - Empty archSuffix produces names like "libfoo.a".
//   - Empty ext produces names like "libfoo_arm64" (unusual but valid).
//
// Example:
//
//	libOutputName("foo", "_arm64", ".a")    // Returns "libfoo_arm64.a"
//	libOutputName("libbar", "_amd64", ".so") // Returns "libbar_amd64.so"
//	libOutputName("baz", "", ".a")           // Returns "libbaz.a"
func libOutputName(name, archSuffix, ext string) string {
	// Start with the base name.
	libName := name
	// Add "lib" prefix if not already present.
	// This follows the conventional library naming on Unix-like systems.
	if !strings.HasPrefix(name, "lib") {
		libName = "lib" + name
	}
	// Combine prefix, arch suffix, and extension.
	return libName + archSuffix + ext
}

// sharedLibOutputName generates the output file name for a shared library.
//
// This is a convenience function that delegates to libOutputName with the
// ".so" extension for shared libraries. On Windows, the extension would
// be ".dll" and this function would need adjustment for cross-compilation.
//
// Shared libraries (also called dynamic libraries) are linked at runtime
// and can be shared between multiple executables to save memory.
//
// Parameters:
//   - name: Base library name (e.g., "foo", "libbar").
//     The "lib" prefix is added by libOutputName if not present.
//   - archSuffix: Architecture suffix (e.g., "_arm64", "_amd64").
//     Can be empty for single-arch builds.
//
// Returns:
//   - Full shared library name with ".so" extension
//     (e.g., "libfoo_arm64.so", "libbar.so").
//
// Key design decisions:
//   - Uses ".so" extension for all platforms; cross-compilation to Windows
//     requires additional handling (outputNameForGoBinary handles this).
//   - Delegates to libOutputName to ensure consistent naming conventions.
//
// Example:
//
//	sharedLibOutputName("foo", "_arm64")  // Returns "libfoo_arm64.so"
//	sharedLibOutputName("libbar", "")     // Returns "libbar.so"
func sharedLibOutputName(name string, archSuffix string) string {
	// Delegate to libOutputName with ".so" extension for shared libraries.
	return libOutputName(name, archSuffix, ".so")
}

// staticLibOutputName generates the output file name for a static library.
//
// This is a convenience function that delegates to libOutputName with the
// ".a" extension for static libraries. Static libraries (archives) contain
// object files that are linked at compile time.
//
// Parameters:
//   - name: Base library name (e.g., "foo", "libbar").
//     The "lib" prefix is added by libOutputName if not present.
//   - archSuffix: Architecture suffix (e.g., "_arm64", "_amd64").
//     Can be empty for single-arch builds.
//
// Returns:
//   - Full static library name with ".a" extension
//     (e.g., "libfoo_arm64.a", "libbar.a").
//
// Key design decisions:
//   - Uses ".a" extension for all platforms; this is standard for Unix-like
//     systems and recognized by most toolchains on Windows too.
//   - Delegates to libOutputName to ensure consistent naming conventions.
//
// Example:
//
//	staticLibOutputName("foo", "_arm64")  // Returns "libfoo_arm64.a"
//	staticLibOutputName("libbar", "")     // Returns "libbar.a"
func staticLibOutputName(name string, archSuffix string) string {
	// Delegate to libOutputName with ".a" extension for static libraries.
	return libOutputName(name, archSuffix, ".a")
}

// getFirstSource retrieves the first source file path from a module.
//
// This is a convenience function for modules that expect a single source
// file or when only the first source is needed (e.g., for certain generators
// or when building a test binary from a single source).
//
// Parameters:
//   - m: The parser.Module to extract the first source from.
//     If nil or srcs property not found, returns empty string.
//
// Returns:
//   - The first source file path from the "srcs" property.
//   - Empty string ("") if:
//   - The module is nil or has no property map
//   - No "srcs" property exists
//   - The "srcs" property is not a list type
//   - The "srcs" list is empty
//
// Edge cases:
//   - Returns empty string (not an error) when no sources are available.
//     Callers should check for empty string if a source is required.
//   - If srcs contains non-string elements, they are silently dropped
//     by getSrcs, so the first valid string is returned.
//
// Example:
//
//	// In Blueprint: cc_binary { srcs: ["main.c", "util.c"] }
//	first := getFirstSource(m)  // Returns "main.c"
func getFirstSource(m *parser.Module) string {
	// Get the list of source files from the module.
	srcs := getSrcs(m)
	// Return empty string if no sources are available.
	if len(srcs) == 0 {
		return ""
	}
	// Return the first source file path.
	return srcs[0]
}

// getData retrieves data file paths from a module's "data" property.
//
// Data files are files that need to be available at runtime,
// typically for testing or resource loading. During the build,
// these files are typically copied to the build output directory
// or test execution directory.
//
// Parameters:
//   - m: The parser.Module to extract data files from.
//     If nil or property not found, returns nil.
//
// Returns:
//   - A slice of data file paths from the "data" property.
//   - nil (not an empty slice) if:
//   - The module is nil or has no property map
//   - No "data" property exists
//   - The property exists but is not a list type
//
// Edge cases:
//   - Non-string elements in the list are silently dropped.
//   - Returns nil (not []string{}) when property not found.
//   - Paths are returned as-is; callers may need to resolve
//     relative paths based on the module's location.
//
// Example:
//
//	// In Blueprint: go_test { data: ["testdata/input.txt"] }
//	data := getData(m)  // Returns ["testdata/input.txt"]
func getData(m *parser.Module) []string {
	// Delegate to GetListProp for the "data" property.
	return GetListProp(m, "data")
}

// copyCommand returns the platform-specific copy command for use in Ninja build rules.
//
// This function returns a command string that can be used in Ninja rules
// to copy files. The command uses Ninja's built-in variables $in (input)
// and $out (output) to specify source and destination.
//
// Platform differences:
//   - Unix/Linux/macOS: Uses the "cp" command which handles $in and $out directly.
//   - Windows: Uses "cmd /c copy" because the built-in copy command requires
//     shell interpretation and doesn't natively understand Unix-style variable syntax.
//
// Parameters: None
//
// Returns:
//   - A command string suitable for use in a Ninja rule's command line.
//     Examples:
//   - Unix: "cp $in $out"
//   - Windows: "cmd /c copy $in $out"
//
// Edge cases:
//   - The returned command expects exactly one input ($in) and one output ($out).
//     For multiple files, a different approach (like a shell loop) would be needed.
//   - On Windows, the "cmd /c" prefix spawns a command prompt to interpret the command.
//
// Example usage in a Ninja rule:
//
//	rule copy
//	  command = cp $in $out
//	  description = Copying $in to $out
func copyCommand() string {
	// Check the runtime OS to determine the appropriate copy command.
	// runtime.GOOS is set at compile time and reflects the host OS.
	if runtime.GOOS == "windows" {
		// Windows requires cmd /c to interpret the copy command.
		return "cmd /c copy $in $out"
	}
	// Unix-like systems can use cp directly.
	return "cp $in $out"
}

// getTestOptionArgs retrieves test option arguments from a module's "test_options" property.
//
// Test options are specified as a nested map property that can contain
// various settings for test execution. This function specifically extracts
// the "args" key from the "test_options" map and returns them as a
// space-separated string suitable for passing to test binaries.
//
// The "test_options" property in Blueprint might look like:
//
//	test_options {
//	  args: ["-v", "-cover", "-timeout=30s"],
//	  env: ["FOO=bar"],
//	}
//
// Parameters:
//   - m: The parser.Module to extract test options from.
//     If nil or property not found, returns empty string.
//
// Returns:
//   - A space-separated string of all test arguments from the "args" key.
//   - Empty string ("") if:
//   - The module is nil or has no property map
//   - No "test_options" property exists
//   - The "test_options" property is not a map type
//   - No "args" key exists in the test_options map
//   - The "args" value is not a list or string type
//
// Edge cases:
//   - If "args" is a single string, it's wrapped in a single-element slice.
//   - Non-string elements in the args list are silently dropped.
//   - Returns empty string (not nil) since the return type is string.
//
// Example:
//
//	// In Blueprint: go_test { test_options { args: ["-v", "-cover"] } }
//	args := getTestOptionArgs(m)  // Returns "-v -cover"
func getTestOptionArgs(m *parser.Module) string {
	// Get the "test_options" map property, then extract "args" from it.
	// GetMapStringListProp handles both list and single string values.
	return strings.Join(GetMapStringListProp(GetMapProp(m, "test_options"), "args"), " ")
}

// GetMapProp retrieves a nested map property from a module's property map.
//
// Map properties are nested property structures that contain their own
// set of key-value pairs. They are commonly used for grouped settings
// like "test_options", "target", "variants", etc.
//
// Example of a map property in Blueprint:
//
//	test_options {
//	  args: ["-v", "-cover"],
//	  env: ["FOO=bar"],
//	  timeout: "30s",
//	}
//
// Parameters:
//   - m: The parser.Module to extract the map property from.
//     If nil or m.Map is nil, returns nil immediately.
//   - name: The property name to look for (case-sensitive).
//     Example: "test_options", "target", "variants".
//
// Returns:
//   - A pointer to the parser.Map if the property is found and is a map type.
//   - nil if:
//   - The module is nil or has no property map
//   - No property with the given name exists
//   - The property exists but is not a map type
//
// Edge cases:
//   - Returns nil (not an empty map) when property not found.
//     This allows callers to distinguish "not set" from "empty map".
//   - Only examines m.Map.Properties; nested maps within the returned
//     map can be accessed directly from the returned parser.Map.
//   - If multiple properties have the same name, the first one is returned.
//
// Example:
//
//	// Get the "test_options" map property from a module
//	testOpts := GetMapProp(m, "test_options")
//	if testOpts != nil {
//	  // Extract specific properties from the map
//	  args := GetMapStringListProp(testOpts, "args")
//	}
func GetMapProp(m *parser.Module, name string) *parser.Map {
	// Early return if module has no property map.
	if m.Map == nil {
		return nil
	}
	// Iterate through properties to find name match.
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			// Attempt type assertion to *parser.Map.
			// If the property is a different type (String, List, Bool, etc.),
			// the assertion fails and we continue (though typically
			// there's only one property with a given name).
			if mp, ok := prop.Value.(*parser.Map); ok {
				return mp
			}
		}
	}
	// Property not found or wrong type; return nil.
	return nil
}

// GetMapStringListProp retrieves a string list property from a nested map.
//
// This function extracts a property from a parser.Map that can be either
// a list of strings or a single string value. This flexibility allows
// Blueprint files to specify single values without list syntax.
//
// The function handles two value types:
//   - *parser.List: Extracts all string elements into a slice.
//   - *parser.String: Wraps the single value in a one-element slice.
//
// This is particularly useful for properties like "args" in "test_options"
// where users might write either "args: "-v"" or "args: ["-v", "-cover"]".
//
// Parameters:
//   - mp: The parser.Map to extract the property from.
//     Typically obtained from GetMapProp.
//     If nil, returns nil immediately.
//   - name: The property name to look for within the map (case-sensitive).
//     Example: "args", "env", "timeout".
//
// Returns:
//   - A slice of string values if the property is found.
//     For list values, all string elements are included (non-strings dropped).
//     For single string values, returns a one-element slice.
//   - nil (not an empty slice) if:
//   - The map pointer is nil
//   - No property with the given name exists in the map
//   - The property exists but is neither a List nor a String
//   - The property is a List but contains no string elements
//
// Edge cases:
//   - Non-string elements in a List are silently dropped.
//     Example: ["a", 123, "b"] returns ["a", "b"].
//   - Returns nil (not []string{}) when property not found, allowing
//     callers to distinguish "not set" from "empty list".
//   - If multiple properties have the same name, the first one is returned.
//   - An empty list property (e.g., args: []) returns an empty slice []string{},
//     not nil. This is because the property exists and is the correct type.
//
// Example:
//
//	// In Blueprint:
//	// test_options {
//	//   args: ["-v", "-cover"],
//	//   timeout: "30s",
//	// }
//	args := GetMapStringListProp(testOpts, "args")    // Returns ["-v", "-cover"]
//	timeout := GetMapStringListProp(testOpts, "timeout")  // Returns ["30s"]
//	missing := GetMapStringListProp(testOpts, "foo")      // Returns nil
func GetMapStringListProp(mp *parser.Map, name string) []string {
	// Early return if map pointer is nil.
	if mp == nil {
		return nil
	}
	// Iterate through map properties to find name match.
	for _, prop := range mp.Properties {
		if prop.Name == name {
			// Check if the property is a List type.
			if list, ok := prop.Value.(*parser.List); ok {
				// Extract string values from list elements.
				// Non-string elements are silently dropped.
				var out []string
				for _, v := range list.Values {
					if s, ok := v.(*parser.String); ok {
						out = append(out, s.Value)
					}
				}
				return out
			}
			// Check if the property is a single String value.
			// Wrap it in a one-element slice for consistent return type.
			if s, ok := prop.Value.(*parser.String); ok {
				return []string{s.Value}
			}
		}
	}
	// Property not found or wrong type; return nil to indicate absence.
	return nil
}
