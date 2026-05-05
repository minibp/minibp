// Package module provides the module type system for minibp build rules,
// defining the various module types (C/C++, Go, Java, Proto, Custom) and
// helper functions for extracting module properties from AST.
//
// This package uses a factory pattern where each module type has a
// corresponding factory struct that knows how to parse AST properties and create
// the appropriate Module struct. This file contains both the property extraction
// helpers and all built-in module type definitions.
//
// Key design decisions:
//   - Property extraction helpers: Provide reusable functions for extracting common
//     property types from AST, handling evaluation and type conversion.
//   - Factory implementations: Each module type has a factory that knows how
//     to parse the specific properties for that type.
//   - BaseModule embedding: All module types embed BaseModule to get common functionality.
//   - Evaluation: Properties can contain expressions (variables, select())
//     that are evaluated before being stored in the module.
//
// Property extraction functions provided:
//   - extractStringList: Extract string arrays from AST properties
//   - extractString: Extract single string values
//   - extractAllProps: Extract all properties as a map
//   - extractPropValue: Convert AST expressions to Go values
//   - collectDeps: Collect and deduplicate dependencies
//   - baseModuleFromAST: Create BaseModule from parsed AST
//
// Built-in module types:
//   - C/C++: CCLibrary, CCBinary (with variants)
//   - Go: GoLibrary, GoBinary
//   - Java: JavaLibrary, JavaBinary
//   - Proto: ProtoLibrary, ProtoGen
//   - Custom: Generic custom modules
package module

import (
	"fmt"
	"minibp/lib/parser"
)

// extractStringList extracts a list of string values from an AST map property.
// It handles both literal string values and expressions that can be evaluated.
// This is one of the primary property extraction functions, used for properties
// that contain arrays of file paths or other string values (e.g., srcs, deps,
// cflags, includes). The function iterates through the AST properties looking
// for a property matching the given key. If found, it expects a parser.List
// containing string values.
//
// The function returns empty slice immediately if ast is nil (defensive check),
// then iterates through ast.Properties to find property with matching name.
// If property value is a parser.List, iterates through its values:
//   - If it's a literal parser.String, append its Value directly
//   - If it's an expression and eval is provided, evaluate it first
//   - Only append if evaluation produces a string
//
// Returns empty slice if property not found or not a list.
//
// Parameters:
//   - ast: The parser.Map containing the module properties (may be nil)
//   - key: The property name to extract (e.g., "srcs", "cflags", "includes")
//   - eval: Optional evaluator for computing expression values
//     If nil, only literal strings are extracted
//
// Returns:
//   - A slice of strings extracted from the property
//   - Empty slice if: ast is nil, property not found, or property is not a list
//
// Edge cases:
//   - Property exists but value is not a list: returns empty slice
//   - List contains non-string values: skipped silently
//   - Expression evaluation returns non-string: skipped silently
//   - Empty list property: returns empty (not nil) slice
func extractStringList(ast *parser.Map, key string, eval *parser.Evaluator) []string {
	if ast == nil { // Defensive check: return empty slice for nil AST
		return []string{}
	}

	for _, prop := range ast.Properties { // Iterate through AST properties
		if prop.Name == key { // Found matching property
			if list, ok := prop.Value.(*parser.List); ok { // Property is a list
				result := make([]string, 0, len(list.Values))
				for _, v := range list.Values { // Process each list element
					if s, ok := v.(*parser.String); ok { // Literal string value
						result = append(result, s.Value)
					} else if eval != nil { // Evaluate expression (variables, select(), etc.)
						val := eval.Eval(v)
						if s, ok := val.(string); ok { // Evaluation produced a string
							result = append(result, s)
						}
					}
				}
				return result
			}
		}
	}
	return []string{}
}

// extractString extracts a single string value from an AST map property.
// It handles both literal string values and expressions that can be evaluated.
// This function is used for properties that contain single string values,
// such as module names, paths, and identifiers (e.g., "name", "pkg", "main_class").
//
// The function returns empty string immediately if ast is nil (defensive check),
// then iterates through ast.Properties to find property with matching name.
// First tries direct type assertion to parser.String (fast path for literals).
// If not a string, tries evaluating via eval if provided.
// Returns the string result or empty string if not found.
//
// Parameters:
//   - ast: The parser.Map containing the module properties (may be nil)
//   - key: The property name to extract (e.g., "name", "pkg", "main_class")
//   - eval: Optional evaluator for computing expression values
//     If nil, only literal strings are extracted
//
// Returns:
//   - The string value if found and evaluable to string
//   - Empty string if: ast is nil, property not found, or value is not a string
//
// Edge cases:
//   - Property exists with non-string value: returns empty string
//   - Expression evaluates to non-string: returns empty string
//   - Property not found: returns empty string (not error)
func extractString(ast *parser.Map, key string, eval *parser.Evaluator) string {
	if ast == nil { // Defensive check: return empty string for nil AST
		return ""
	}
	for _, prop := range ast.Properties { // Iterate through AST properties
		if prop.Name == key { // Found matching property
			// Fast path: direct string literal
			if s, ok := prop.Value.(*parser.String); ok { // Literal string value
				return s.Value
			}
			// Slow path: evaluate expression (for variables, select(), etc.)
			if eval != nil { // Evaluator provided, try expression evaluation
				val := eval.Eval(prop.Value)
				if s, ok := val.(string); ok { // Evaluation produced a string
					return s
				}
			}
		}
	}
	return ""
}

// extractAllProps extracts all properties from an AST map and returns them
// as a Go map. This is used to capture all module properties beyond the
// built-in ones, allowing custom properties to be accessed generically.
//
// Each property value is processed through extractPropValue to handle various
// expression types (strings, ints, bools, lists, maps, variables).
// The function creates a new map and populates it with all properties from
// the AST, converting each property value via extractPropValue. This allows
// the build system to pass arbitrary properties to ninja rules.
//
// Parameters:
//   - ast: The parser.Map containing the module properties (may be nil)
//   - eval: Optional evaluator for computing expression values in property values
//
// Returns:
//   - A map from property names to their evaluated values
//   - Empty map if ast is nil (never returns nil)
//
// Property value types after extraction:
//   - parser.String -> string
//   - parser.Int64 -> int64
//   - parser.Bool -> bool
//   - parser.List -> []interface{} (recursively converted)
//   - parser.Map -> map[string]interface{} (recursively converted)
//   - parser.Variable -> variable name string
//   - Other types -> formatted string via fmt.Sprintf
func extractAllProps(ast *parser.Map, eval *parser.Evaluator) map[string]interface{} {
	props := make(map[string]interface{})
	if ast == nil { // Defensive check: return empty map for nil AST
		return props
	}
	for _, prop := range ast.Properties { // Process each property
		props[prop.Name] = extractPropValue(prop.Value, eval)
	}
	return props
}

// extractPropValue converts a parser expression to a native Go value.
// This is the core type conversion function for property values, handling
// the translation from AST nodes to Go types used throughout the build system.
//
// The function first attempts evaluation if an evaluator is provided,
// then falls back to type-specific conversion for unevaluated expressions.
// This allows variables and expressions to be resolved during property extraction.
//
// Conversion rules:
//   - parser.String: Returns the string Value directly
//   - parser.Int64: Returns the int64 Value directly
//   - parser.Bool: Returns the bool Value directly
//   - parser.List: Recursively converts each element to []interface{}
//   - parser.Map: Recursively converts each property to map[string]interface{}
//   - parser.Variable: Returns the variable name as string
//   - Other types: Formats as string via fmt.Sprintf (fallback for unknown types)
//
// Parameters:
//   - expr: The parser expression to convert (may be nil)
//   - eval: Optional evaluator for computing expression values
//     If provided and evaluation succeeds, returns the evaluated value
//
// Returns:
//   - A Go value (string, int64, bool, []interface{}, or map[string]interface{})
//   - For nil expressions: attempts evaluation, returns nil if no evaluator
//   - For unknown types: returns formatted string via fmt.Sprintf
//
// Edge cases:
//   - expr is nil: returns nil
//   - eval is nil or evaluation returns nil: falls back to type conversion
//   - Empty list: returns empty []interface{}
//   - Empty map: returns empty map[string]interface{}
func extractPropValue(expr parser.Expression, eval *parser.Evaluator) interface{} {
	// First try evaluation if evaluator provided
	// This resolves variables, select() expressions, etc.
	if eval != nil { // Evaluator provided, attempt expression evaluation
		if val := eval.Eval(expr); val != nil { // Evaluation succeeded
			return val
		}
	}

	// Fall back to type-specific conversion
	switch v := expr.(type) {
	case *parser.String:
		return v.Value
	case *parser.Int64:
		return v.Value
	case *parser.Bool:
		return v.Value
	case *parser.List:
		// Recursively convert list elements
		items := make([]interface{}, 0, len(v.Values))
		for _, item := range v.Values { // Process each list element
			items = append(items, extractPropValue(item, eval))
		}
		return items
	case *parser.Map:
		// Recursively convert map properties
		m := make(map[string]interface{}, len(v.Properties))
		for _, prop := range v.Properties { // Process each map property
			m[prop.Name] = extractPropValue(prop.Value, eval)
		}
		return m
	case *parser.Variable:
		// Return variable name as string for reference
		return v.Name
	default:
		// Fallback for unknown expression types
		return fmt.Sprintf("%v", expr)
	}
}

// collectDeps collects all dependencies from an AST module by examining multiple
// dependency-related properties and deduplicating the results.
//
// The function looks at three standard dependency property keys:
//   - "deps": Direct module dependencies (general dependencies)
//   - "shared_libs": Shared library dependencies (for runtime linking)
//   - "header_libs": Header library dependencies (for C/C++ include paths)
//
// Uses a map-based approach for O(n) deduplication complexity where
// n is the total number of dependency entries across all properties.
//
// Algorithm:
// 1. Define the dependency property keys to examine
// 2. Create a map to track seen dependencies for O(1) deduplication
// 3. For each defined dependency property key:
//   - Extract string list using extractStringList
//   - For each dependency in the list:
//   - If not seen before, mark as seen and append to result
//
// 4. Return the deduplicated result slice
//
// Parameters:
//   - ast: The parser.Map containing the module properties
//   - eval: Optional evaluator for computing expression values in dependency lists
//
// Returns:
//   - A deduplicated slice of dependency strings
//   - Empty slice if no dependencies defined (never returns nil)
//
// Edge cases:
//   - Duplicate entries across different properties: deduplicated
//   - Same entry multiple times in same property: deduplicated
//   - Invalid/non-string entries in dependency lists: skipped
func collectDeps(ast *parser.Map, eval *parser.Evaluator) []string {
	depKeys := []string{"deps", "shared_libs", "header_libs"}
	seen := make(map[string]bool)
	var deps []string
	for _, key := range depKeys { // Check each dependency property key
		for _, dep := range extractStringList(ast, key, eval) { // Process each dependency
			if !seen[dep] { // New dependency, add to result
				seen[dep] = true
				deps = append(deps, dep)
			}
		}
	}
	return deps
}

// baseModuleFromAST creates a BaseModule from a parsed AST module.
// This is the common foundation for all module types, extracting
// the name, type, sources, dependencies, and all properties.
//
// The function acts as a factory for creating the base module
// that all specialized module types embed via composition.
// It's the first step in creating any module type instance.
//
// Extracts all common fields from the AST and creates a BaseModule struct.
// Properties extracted:
//   - name: From the "name" property via extractString
//   - type: From the AST module's Type field
//   - srcs: From the "srcs" property via extractStringList
//   - deps: From collectDeps (merges deps, shared_libs, header_libs)
//   - props: All remaining properties via extractAllProps
//
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A BaseModule with all common fields populated
//
// Note:
//   - The returned BaseModule can be embedded directly in specialized types
//   - Fields are always populated (empty strings/slices for missing properties)
func baseModuleFromAST(ast *parser.Module, eval *parser.Evaluator) BaseModule {
	return BaseModule{
		Name_:  extractString(ast.Map, "name", eval),     // Module name from "name" property
		Type_:  ast.Type,                                 // Module type from AST
		Srcs_:  extractStringList(ast.Map, "srcs", eval), // Source files from "srcs"
		Deps_:  collectDeps(ast.Map, eval),               // Dependencies from deps/shared_libs/header_libs
		Props_: extractAllProps(ast.Map, eval),           // All other properties
	}
}

// ============================================================================
// C/C++ Modules
// ============================================================================

// CCLibrary represents a C/C++ library module (static, shared, or object).
// It includes compiler flags, include paths, and linker flags specific to C/C++.
// This module type is used for building libraries in multiple formats:
//
// Description:
// The same CCLibrary type handles all these variants based on the module name
// and build configuration. The ninja generator determines the output format.
//
// Variants:
//   - Static library (.a): For linking into final binaries
//   - Shared library (.so/.dll): For dynamic linking at runtime
//   - Object files (.o): For header-only libraries or partial linking
//
// Common properties:
//   - cflags: Additional C/C++ compiler flags (e.g., "-Wall", "-O3")
//   - includes: Include directories for the compiler (e.g., "-Iinclude")
//   - ldflags: Additional linker flags (e.g., "-lpthread")
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//
// Edge cases:
//   - CFlags, Includes, and LDFlags may be empty slices if properties not defined
type CCLibrary struct {
	BaseModule
	// CFlags contains additional C/C++ compiler flags.
	// These flags are passed to the compiler (gcc/clang) for all source files.
	// Examples: "-Wall", "-Wextra", "-O3", "-fPIC"
	CFlags []string
	// Includes contains include directories for the compiler.
	// These paths are searched for header files during compilation.
	// Format: either "-Idir" or just "dir" depending on ninja rule.
	Includes []string
	// LDFlags contains additional linker flags.
	// These flags are passed to the linker (ld) when creating the final artifact.
	// Examples: "-L/lib", "-lpthread", "-lm"
	LDFlags []string
}

// CCLibraryFactory creates CCLibrary instances from AST nodes.
// This factory handles all C/C++ library variants including:
//   - cc_library: Default library type
//   - cc_library_static: Explicit static library
//   - cc_library_shared: Explicit shared library
//   - cc_object: Object file only (no archiving)
//   - cc_library_headers: Header-only library
type CCLibraryFactory struct{}

// Create instantiates a CCLibrary from a parsed AST module.
//
// Description:
// It extracts C-specific properties like cflags, includes, and ldflags,
// then constructs a CCLibrary with those values embedded in the BaseModule.
//
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured CCLibrary with C/C++ specific fields populated
//   - Error is never returned by this implementation (all properties are optional)
//
// Note:
//   - CFlags, Includes, and LDFlags may be empty slices if properties not defined
func (f *CCLibraryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &CCLibrary{
		BaseModule: baseModuleFromAST(ast, eval),
		CFlags:     extractStringList(ast.Map, "cflags", eval),
		Includes:   extractStringList(ast.Map, "includes", eval),
		LDFlags:    extractStringList(ast.Map, "ldflags", eval),
	}
	return m, nil
}

// CCBinary represents a C/C++ binary (executable) module.
// It includes all CCLibrary fields plus a Static flag for controlling
// whether dependencies are linked statically or dynamically.
//
// Description:
// When Static is true, all shared library dependencies are converted to
// static library dependencies at link time. This is useful for building
// fully self-contained executables.
//
// Common properties:
//   - cflags: Additional C/C++ compiler flags
//   - includes: Include directories for the compiler
//   - ldflags: Additional linker flags
//   - static: Whether to link all dependencies statically (boolean)
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//
// Edge cases:
//   - Static field defaults to false if not explicitly set in Blueprint
type CCBinary struct {
	BaseModule
	// CFlags contains additional C/C++ compiler flags.
	CFlags []string
	// Includes contains include directories for the compiler.
	Includes []string
	// LDFlags contains additional linker flags.
	LDFlags []string
	// Static indicates whether to link all dependencies statically.
	// When true, shared library dependencies are linked statically.
	Static bool
}

// CCBinaryFactory creates CCBinary instances from AST nodes.
// This factory handles C/C++ binary module types including:
//   - cc_binary: Standard executable
//   - cpp_binary: C++ executable (alias)
//   - cc_test: C/C++ test executable
type CCBinaryFactory struct{}

// Create instantiates a CCBinary from a parsed AST module.
//
// Description:
// It extracts C-specific properties and the optional "static" boolean property.
//
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured CCBinary with C/C++ specific fields populated
//   - Error is never returned by this implementation (all properties are optional)
//
// Note:
//   - The Static field is false if not explicitly set in the Blueprint
func (f *CCBinaryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &CCBinary{
		BaseModule: baseModuleFromAST(ast, eval),
		CFlags:     extractStringList(ast.Map, "cflags", eval),
		Includes:   extractStringList(ast.Map, "includes", eval),
		LDFlags:    extractStringList(ast.Map, "ldflags", eval),
	}
	// Extract optional boolean property for static linking
	if static, ok := m.BaseModule.Props_["static"].(bool); ok {
		m.Static = static
	}
	return m, nil
}

// ============================================================================
// Go Modules
// ============================================================================

// GoLibrary represents a Go library module.
// It includes package path and import path for Go compilation.
// Go libraries are compiled as .a files that can be linked into binaries.
//
// Description:
// The Go module system differs from C/C++ in that it uses package-based
// organization rather than file-based. The "pkg" property specifies the
// filesystem path, while "importpath" specifies the import path for go mod.
//
// Common properties:
//   - pkg: The filesystem path to the Go package (e.g., "lib/foo")
//   - importpath: The import path for go mod (e.g., "example.com/lib/foo")
//   - goflags: Go compiler flags (e.g., "-gcflags=-B")
//   - ldflags: Go linker flags (e.g., "-s -w")
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
type GoLibrary struct {
	BaseModule
	// PackagePath is the filesystem path to the Go package.
	// This is the directory containing the Go source files.
	// Example: "lib/utils", "cmd/app"
	PackagePath string
	// ImportPath is the import path used for Go module compatibility.
	// This is the canonical import path for the package.
	// Example: "github.com/user/project/lib"
	ImportPath string
	// GoFlags contains Go compiler flags.
	// These are passed to the Go compiler (go build).
	// Examples: "-gcflags=-B", "-mod=readonly"
	GoFlags []string
	// LDFlags contains Go linker flags.
	// These are passed to the Go linker when building the final binary.
	// Examples: "-s -w" (strip symbols), "-X version=1.0"
	LDFlags []string
}

// GoLibraryFactory creates GoLibrary instances from AST nodes.
// This factory handles go_library module type.
type GoLibraryFactory struct{}

// Create instantiates a GoLibrary from a parsed AST module.
//
// Description:
// It extracts the "pkg", "importpath", "goflags", and "ldflags" properties.
//
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured GoLibrary with Go-specific fields populated
//   - Error is never returned by this implementation (all properties are optional)
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
func (f *GoLibraryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &GoLibrary{
		BaseModule:  baseModuleFromAST(ast, eval),
		PackagePath: extractString(ast.Map, "pkg", eval),
		ImportPath:  extractString(ast.Map, "importpath", eval),
		GoFlags:     extractStringList(ast.Map, "goflags", eval),
		LDFlags:     extractStringList(ast.Map, "ldflags", eval),
	}
	return m, nil
}

// GoBinary represents a Go binary (executable) or test module.
// It has the same properties as GoLibrary since both are Go packages.
// The distinction is in how the ninja generator handles them:
//
// Description:
//   - go_binary: Produces an executable binary
//   - go_test: Produces a test binary that links the test package
//
// The go_test variant has access to the testing package and can run
// tests as part of the build.
//
// Common properties:
//   - pkg: The filesystem path to the Go package
//   - importpath: The import path for go mod
//   - goflags: Go compiler flags
//   - ldflags: Go linker flags
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//
// Edge cases:
//   - go_test produces a binary that links the test package (not the main package)
type GoBinary struct {
	BaseModule
	// PackagePath is the filesystem path to the Go package.
	PackagePath string
	// ImportPath is the import path for go mod compatibility.
	ImportPath string
	// GoFlags contains Go compiler flags.
	GoFlags []string
	// LDFlags contains Go linker flags.
	LDFlags []string
}

// GoBinaryFactory creates GoBinary instances from AST nodes.
// Used for go_binary and go_test module types.
type GoBinaryFactory struct{}

// Create instantiates a GoBinary from a parsed AST module.
//
// Description:
// It extracts the "pkg", "importpath", "goflags", and "ldflags" properties.
//
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured GoBinary with Go-specific fields populated
//   - Error is never returned by this implementation (all properties are optional)
func (f *GoBinaryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &GoBinary{
		BaseModule:  baseModuleFromAST(ast, eval),
		PackagePath: extractString(ast.Map, "pkg", eval),
		ImportPath:  extractString(ast.Map, "importpath", eval),
		GoFlags:     extractStringList(ast.Map, "goflags", eval),
		LDFlags:     extractStringList(ast.Map, "ldflags", eval),
	}
	return m, nil
}

// ============================================================================
// Java Modules
// ============================================================================

// JavaLibrary represents a Java library module.
// It includes package name and resource directories for Java compilation.
// Java libraries are compiled as .jar files containing .class files.
//
// Description:
// The Java build system uses different conventions than C/C++ or Go.
// The "package" property refers to the Java package name, not a filesystem path.
//
// Common properties:
//   - package: The Java package name (e.g., "com.example.lib")
//   - resource_dirs: Directories containing resources (images, config files, etc.)
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
type JavaLibrary struct {
	BaseModule
	// PackageName is the Java package name.
	// This is the hierarchical package identifier, not a filesystem path.
	// Example: "com.example.lib", "org.apache.commons"
	PackageName string
	// ResourceDirs contains directories with resources.
	// Resources are non-code files included in the final JAR.
	// Examples: "res/", "assets/"
	ResourceDirs []string
}

// JavaLibraryFactory creates JavaLibrary instances from AST nodes.
// This factory handles java_library and related variants.
type JavaLibraryFactory struct{}

// Create instantiates a JavaLibrary from a parsed AST module.
//
// Description:
// It extracts the "package" and "resource_dirs" properties.
//
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured JavaLibrary with Java-specific fields populated
//   - Error is never returned by this implementation (all properties are optional)
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
func (f *JavaLibraryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &JavaLibrary{
		BaseModule:   baseModuleFromAST(ast, eval),
		PackageName:  extractString(ast.Map, "package", eval),
		ResourceDirs: extractStringList(ast.Map, "resource_dirs", eval),
	}
	return m, nil
}

// JavaBinary represents a Java binary (executable) or test module.
// It includes the main class for execution and resource directories.
// The main_class property specifies the fully qualified class name
// containing the public static void main(String[]) method.
//
// Description:
// Common properties:
//   - package: The Java package name
//   - main_class: The fully qualified class name with main() method
//   - resource_dirs: Directories containing resources
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
type JavaBinary struct {
	BaseModule
	// PackageName is the Java package name.
	PackageName string
	// MainClass is the fully qualified class name containing main().
	// This is the entry point for the Java application.
	// Example: "com.example.app.Main"
	MainClass string
	// ResourceDirs contains directories with resources.
	ResourceDirs []string
}

// JavaBinaryFactory creates JavaBinary instances from AST nodes.
// Used for java_binary, java_binary_host, and java_test module types.
type JavaBinaryFactory struct{}

// Create instantiates a JavaBinary from a parsed AST module.
//
// Description:
// It extracts the "package", "main_class", and "resource_dirs" properties.
//
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured JavaBinary with Java-specific fields populated
//   - Error is never returned by this implementation (all properties are optional)
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
func (f *JavaBinaryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &JavaBinary{
		BaseModule:   baseModuleFromAST(ast, eval),
		PackageName:  extractString(ast.Map, "package", eval),
		MainClass:    extractString(ast.Map, "main_class", eval),
		ResourceDirs: extractStringList(ast.Map, "resource_dirs", eval),
	}
	return m, nil
}

// ============================================================================
// Proto Modules
// ============================================================================

// ProtoLibrary represents a Protocol Buffer library module.
// It includes proto source files, include paths, and code generation options.
// Proto libraries compile .proto files into language-specific code using
// the protoc compiler with specified plugins.
//
// Description:
// Protocol Buffers (protobuf) is a language-neutral, platform-neutral
// mechanism for serializing structured data. The proto_library module
// type generates code in various languages from .proto definitions.
//
// Common properties:
//   - srcs: Source .proto files to compile (inherited from BaseModule)
//   - proto_paths: Additional paths to search for imported .proto files
//   - plugins: List of code generator plugins (e.g., "java", "cpp", "go")
//   - out: Output type for code generation (e.g., "lite", "proto")
//   - include_dirs: Additional include directories for proto imports
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
type ProtoLibrary struct {
	BaseModule
	// ProtoSrcs contains source .proto files to compile.
	// These files define the protocol buffer message structures.
	ProtoSrcs []string
	// ProtoPaths contains additional paths to search for imported .proto files.
	// This is used when .proto files import other .proto files.
	ProtoPaths []string
	// Plugins specifies the list of code generator plugins.
	// Common plugins: "java", "cpp", "python", "go", "gogrpc"
	Plugins []string
	// OutType specifies the output type for code generation.
	// Examples: "lite" (lite runtime), "proto" (full proto)
	OutType string
	// IncludeDirs contains additional include directories for proto imports.
	IncludeDirs []string
}

// ProtoLibraryFactory creates ProtoLibrary instances from AST nodes.
// This factory handles proto_library module type.
type ProtoLibraryFactory struct{}

// Create instantiates a ProtoLibrary from a parsed AST module.
//
// Description:
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured ProtoLibrary with proto-specific fields populated
//   - Error is never returned by this implementation (all properties are optional)
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
func (f *ProtoLibraryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &ProtoLibrary{
		BaseModule:  baseModuleFromAST(ast, eval),
		ProtoSrcs:   extractStringList(ast.Map, "srcs", eval),
		ProtoPaths:  extractStringList(ast.Map, "proto_paths", eval),
		Plugins:     extractStringList(ast.Map, "plugins", eval),
		OutType:     extractString(ast.Map, "out", eval),
		IncludeDirs: extractStringList(ast.Map, "include_dirs", eval),
	}
	return m, nil
}

// ProtoGen represents a custom proto code generator rule.
// It differs from ProtoLibrary in that it's a user-defined generator
// rather than using the standard protoc flow.
//
// Description:
// ProtoGen modules allow custom protoc plugin invocations with
// arbitrary command-line arguments. They're used when you need
// more control over the code generation process than proto_library provides.
//
// Common properties:
//   - srcs: Source .proto files to process (inherited from BaseModule)
//   - plugins: List of code generator plugins to use
//   - out: Output type specification
//   - include_dirs: Additional include directories for proto imports
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
type ProtoGen struct {
	BaseModule
	// ProtoSrcs contains source .proto files to process.
	ProtoSrcs []string
	// Plugins specifies the list of code generator plugins.
	Plugins []string
	// OutType specifies the output type specification.
	OutType string
	// IncludeDirs contains additional include directories for proto imports.
	IncludeDirs []string
}

// ProtoGenFactory creates ProtoGen instances from AST nodes.
// This factory handles proto_gen module type.
type ProtoGenFactory struct{}

// Create instantiates a ProtoGen from a parsed AST module.
//
// Description:
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured ProtoGen with proto-specific fields populated
//   - Error is never returned by this implementation (all properties are optional)
//
// Edge cases:
//   - Missing optional properties result in empty strings or empty slices
func (f *ProtoGenFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &ProtoGen{
		BaseModule:  baseModuleFromAST(ast, eval),
		ProtoSrcs:   extractStringList(ast.Map, "srcs", eval),
		Plugins:     extractStringList(ast.Map, "plugins", eval),
		OutType:     extractString(ast.Map, "out", eval),
		IncludeDirs: extractStringList(ast.Map, "include_dirs", eval),
	}
	return m, nil
}

// ============================================================================
// Custom Module
// ============================================================================

// Custom represents a generic custom module for user-defined build rules.
// This is used when no specific module type matches, providing a catch-all
// for extensibility. All properties are stored in the BaseModule props map.
//
// Description:
// Custom modules are useful for:
//   - Defining aggregate targets that group other modules
//   - Wrapper modules that don't require special compilation
//   - Future-proofing against new module types
//   - Simple filegroup-like behavior
//
// The Custom type stores everything in the generic Props_ map and relies
// on the ninja generator to handle any custom properties appropriately.
//
// Note:
//   - Embedded BaseModule provides Name, Type, Srcs, Deps, Props
//   - The type serves as a catch-all for modules without specific implementations
type Custom struct {
	BaseModule
}

// CustomFactory creates Custom instances from AST nodes.
// Used for the "custom" module type and as a fallback for unknown types.
type CustomFactory struct{}

// Create instantiates a Custom from a parsed AST module.
// All properties are stored in the embedded BaseModule's Props_ map.
//
// Description:
// Parameters:
//   - ast: The parser.Module AST node containing all module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//   - A configured Custom with all properties in the Props_ map
//   - Error is never returned by this implementation
//
// Note:
//   - The Custom type is a catch-all for modules without specific implementations
func (f *CustomFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &Custom{
		BaseModule: baseModuleFromAST(ast, eval),
	}
	return m, nil
}

// ============================================================================
// Initialization
// ============================================================================

// registerBuiltInModuleTypes registers all built-in module types with the registry.
//
// Description:
// This function is called during package initialization to set up the default
// module type factories for C/C++, Go, Java, Proto, and custom modules.
// Registration is done via the Register function which uses a mutex for
// thread-safety. This allows the function to be called during init() without
// issues even if other initialization code accesses the registry.
//
// Registered module types by language:
//   - C/C++:
//     cc_library, cc_library_static, cc_library_shared, cc_object
//     cc_binary, cpp_library, cpp_binary, cc_test, cc_library_headers
//   - Go:
//     go_library, go_binary, go_test
//   - Java:
//     java_library, java_library_static, java_library_host
//     java_binary, java_binary_host, java_test, java_import
//   - Proto:
//     proto_library, proto_gen
//   - Other:
//     filegroup, phony, sh_binary_host, python_binary_host
//     python_test_host, custom
func registerBuiltInModuleTypes() {
	// C/C++ library types - all use CCLibraryFactory
	Register("cc_library", &CCLibraryFactory{})
	Register("cc_library_static", &CCLibraryFactory{})
	Register("cc_library_shared", &CCLibraryFactory{})
	Register("cc_object", &CCLibraryFactory{})

	// C/C++ binary types - all use CCBinaryFactory
	Register("cc_binary", &CCBinaryFactory{})
	Register("cpp_library", &CCLibraryFactory{})
	Register("cpp_binary", &CCBinaryFactory{})
	Register("cc_test", &CCBinaryFactory{})

	// C/C++ headers - treated as filegroup (no compilation)
	Register("cc_library_headers", &CustomFactory{})

	// Go types - GoLibraryFactory for libs, GoBinaryFactory for binaries
	Register("go_library", &GoLibraryFactory{})
	Register("go_binary", &GoBinaryFactory{})
	Register("go_test", &GoBinaryFactory{})

	// Java library types
	Register("java_library", &JavaLibraryFactory{})
	Register("java_library_static", &JavaLibraryFactory{})
	Register("java_library_host", &JavaLibraryFactory{})
	Register("java_import", &JavaLibraryFactory{})

	// Java binary types
	Register("java_binary", &JavaBinaryFactory{})
	Register("java_binary_host", &JavaBinaryFactory{})
	Register("java_test", &JavaBinaryFactory{})

	// Proto types
	Register("proto_library", &ProtoLibraryFactory{})
	Register("proto_gen", &ProtoGenFactory{})

	// Other utility types
	Register("filegroup", &CustomFactory{})
	Register("phony", &CustomFactory{})
	Register("sh_binary_host", &CustomFactory{})
	Register("python_binary_host", &CustomFactory{})
	Register("python_test_host", &CustomFactory{})
	Register("custom", &CustomFactory{})
}

// init is the package initialization function that registers all built-in
// module types when the module package is first imported.
//
// Description:
// Go's init() mechanism ensures this runs before any other code in the
// package executes. This provides automatic registration of core module
// types without requiring explicit initialization in user code.
// The registration happens once at program startup and populates the
// global registry with factories for all standard module types. Custom
// module types can be registered by other packages in their own init()
// functions.
func init() {
	registerBuiltInModuleTypes()
}
