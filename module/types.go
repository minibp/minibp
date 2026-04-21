// Package module provides the module type system for minibp build rules.
// This file defines the various module types (C/C++, Go, Java, Proto, Custom)
// and helper functions for extracting module properties from AST.
package module

import (
	"fmt"
	"minibp/parser"
)

// extractStringList extracts a list of string values from an AST map property.

// It handles both literal string values and expressions that can be evaluated.

// Parameters:

// - ast: The parser.Map containing the module properties

// - key: The property name to extract

// - eval: Optional evaluator for computing expression values

//

// Returns:

//

//	A slice of strings, or an empty slice if the property doesn't exist or isn't a list

func extractStringList(ast *parser.Map, key string, eval *parser.Evaluator) []string {

	if ast == nil {

		return []string{}

	}

	for _, prop := range ast.Properties {

		if prop.Name == key {

			if list, ok := prop.Value.(*parser.List); ok {

				result := make([]string, 0, len(list.Values))

				for _, v := range list.Values {

					if s, ok := v.(*parser.String); ok {

						result = append(result, s.Value)

					} else if eval != nil {

						val := eval.Eval(v)

						if s, ok := val.(string); ok {

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
// Parameters:
//   - ast: The parser.Map containing the module properties
//   - key: The property name to extract
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	The string value, or empty string if the property doesn't exist or isn't a string
func extractString(ast *parser.Map, key string, eval *parser.Evaluator) string {
	if ast == nil {
		return ""
	}
	for _, prop := range ast.Properties {
		if prop.Name == key {
			if s, ok := prop.Value.(*parser.String); ok {
				return s.Value
			}
			if eval != nil {
				val := eval.Eval(prop.Value)
				if s, ok := val.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// extractAllProps extracts all properties from an AST map and returns them as a Go map.
// Each property value is processed through extractPropValue to handle various expression types.
// Parameters:
//   - ast: The parser.Map containing the module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A map from property names to their evaluated values (strings, ints, bools, lists, maps)
func extractAllProps(ast *parser.Map, eval *parser.Evaluator) map[string]interface{} {
	props := make(map[string]interface{})
	if ast == nil {
		return props
	}
	for _, prop := range ast.Properties {
		props[prop.Name] = extractPropValue(prop.Value, eval)
	}
	return props
}

// extractPropValue converts a parser expression to a native Go value.
// It evaluates expressions if an evaluator is provided, and converts
// various AST node types to their Go equivalents.
// Parameters:
//   - expr: The parser expression to convert
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A Go value (string, int64, bool, []interface{}, or map[string]interface{})
//	formatted as a string for unknown types
func extractPropValue(expr parser.Expression, eval *parser.Evaluator) interface{} {
	if eval != nil {
		if val := eval.Eval(expr); val != nil {
			return val
		}
	}

	switch v := expr.(type) {
	case *parser.String:
		return v.Value
	case *parser.Int64:
		return v.Value
	case *parser.Bool:
		return v.Value
	case *parser.List:
		items := make([]interface{}, 0, len(v.Values))
		for _, item := range v.Values {
			items = append(items, extractPropValue(item, eval))
		}
		return items
	case *parser.Map:
		m := make(map[string]interface{}, len(v.Properties))
		for _, prop := range v.Properties {
			m[prop.Name] = extractPropValue(prop.Value, eval)
		}
		return m
	case *parser.Variable:
		return v.Name
	default:
		return fmt.Sprintf("%v", expr)
	}
}

// collectDeps collects all dependencies from an AST module by examining multiple
// dependency-related properties. It deduplicates the collected dependencies.
// The function looks at "deps", "shared_libs", and "header_libs" properties.
// Parameters:
//   - ast: The parser.Map containing the module properties
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A deduplicated slice of dependency strings
func collectDeps(ast *parser.Map, eval *parser.Evaluator) []string {
	depKeys := []string{"deps", "shared_libs", "header_libs"}
	seen := make(map[string]bool)
	var deps []string
	for _, key := range depKeys {
		for _, dep := range extractStringList(ast, key, eval) {
			if !seen[dep] {
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
// Parameters:
//   - ast: The parser.Module AST node
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A BaseModule with all common fields populated
func baseModuleFromAST(ast *parser.Module, eval *parser.Evaluator) BaseModule {
	return BaseModule{
		Name_:  extractString(ast.Map, "name", eval),
		Type_:  ast.Type,
		Srcs_:  extractStringList(ast.Map, "srcs", eval),
		Deps_:  collectDeps(ast.Map, eval),
		Props_: extractAllProps(ast.Map, eval),
	}
}

// ============================================================================
// C/C++ Modules
// ============================================================================

// CCLibrary represents a C/C++ library module (static, shared, or object).
// It includes compiler flags, include paths, and linker flags specific to C/C++.
// Fields:
//   - BaseModule: Embedded base module with common properties
//   - CFlags: Additional C/C++ compiler flags
//   - Includes: Include directories for the compiler
//   - LDFlags: Additional linker flags
type CCLibrary struct {
	BaseModule
	CFlags   []string
	Includes []string
	LDFlags  []string
}

// CCLibraryFactory creates CCLibrary instances from AST nodes.
// It extracts C-specific properties like cflags, includes, and ldflags.
type CCLibraryFactory struct{}

// Create instantiates a CCLibrary from a parsed AST module.
// Parameters:
//   - ast: The parser.Module AST node
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A configured CCLibrary and any error that occurred
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
// It includes all CCLibrary fields plus a Static flag for static linking.
// Fields:
//   - BaseModule: Embedded base module with common properties
//   - CFlags: Additional C/C++ compiler flags
//   - Includes: Include directories for the compiler
//   - LDFlags: Additional linker flags
//   - Static: Whether to link statically (if true, links all static dependencies)
type CCBinary struct {
	BaseModule
	CFlags   []string
	Includes []string
	LDFlags  []string
	Static   bool
}

// CCBinaryFactory creates CCBinary instances from AST nodes.
// It extracts C-specific properties and the "static" boolean property.
type CCBinaryFactory struct{}

// Create instantiates a CCBinary from a parsed AST module.
// Parameters:
//   - ast: The parser.Module AST node
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A configured CCBinary and any error that occurred
func (f *CCBinaryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &CCBinary{
		BaseModule: baseModuleFromAST(ast, eval),
		CFlags:     extractStringList(ast.Map, "cflags", eval),
		Includes:   extractStringList(ast.Map, "includes", eval),
		LDFlags:    extractStringList(ast.Map, "ldflags", eval),
	}
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

// Fields:

// - BaseModule: Embedded base module with common properties

// - PackagePath: The filesystem path to the Go package

// - ImportPath: The import path (used for go mod compatibility)

// - GoFlags: Go compiler flags (e.g., "-gcflags")

// - LDFlags: Go linker flags (e.g., "-ldflags")

type GoLibrary struct {

	BaseModule

	PackagePath string

	ImportPath  string

	GoFlags     []string

	LDFlags     []string

}

// GoLibraryFactory creates GoLibrary instances from AST nodes.
// It extracts the "pkg" and "importpath" properties.
type GoLibraryFactory struct{}

// Create instantiates a GoLibrary from a parsed AST module.

// Parameters:

// - ast: The parser.Module AST node

// - eval: Optional evaluator for computing expression values

//

// Returns:

//

//	A configured GoLibrary and any error that occurred

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

// Fields:

// - BaseModule: Embedded base module with common properties

// - PackagePath: The filesystem path to the Go package

// - ImportPath: The import path (used for go mod compatibility)

// - GoFlags: Go compiler flags (e.g., "-gcflags")

// - LDFlags: Go linker flags (e.g., "-ldflags")

type GoBinary struct {

	BaseModule

	PackagePath string

	ImportPath  string

	GoFlags     []string

	LDFlags     []string

}

// GoBinaryFactory creates GoBinary instances from AST nodes.
// Used for go_binary and go_test module types.
type GoBinaryFactory struct{}

// Create instantiates a GoBinary from a parsed AST module.

// Parameters:

// - ast: The parser.Module AST node

// - eval: Optional evaluator for computing expression values

//

// Returns:

//

//	A configured GoBinary and any error that occurred

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
// Fields:
//   - BaseModule: Embedded base module with common properties
//   - PackageName: The Java package name (e.g., "com.example.lib")
//   - ResourceDirs: Directories containing resources (images, config files, etc.)
type JavaLibrary struct {
	BaseModule
	PackageName  string
	ResourceDirs []string
}

// JavaLibraryFactory creates JavaLibrary instances from AST nodes.
// It extracts the "package" and "resource_dirs" properties.
type JavaLibraryFactory struct{}

// Create instantiates a JavaLibrary from a parsed AST module.
// Parameters:
//   - ast: The parser.Module AST node
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A configured JavaLibrary and any error that occurred
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
// Fields:
//   - BaseModule: Embedded base module with common properties
//   - PackageName: The Java package name
//   - MainClass: The fully qualified class name with main() method
//   - ResourceDirs: Directories containing resources
type JavaBinary struct {
	BaseModule
	PackageName  string
	MainClass    string
	ResourceDirs []string
}

// JavaBinaryFactory creates JavaBinary instances from AST nodes.
// Used for java_binary, java_binary_host, and java_test module types.
type JavaBinaryFactory struct{}

// Create instantiates a JavaBinary from a parsed AST module.
// Parameters:
//   - ast: The parser.Module AST node
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A configured JavaBinary and any error that occurred
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
// Fields:
//   - BaseModule: Embedded base module with common properties
//   - ProtoSrcs: Source .proto files to compile
//   - ProtoPaths: Additional paths to search for imported .proto files
//   - Plugins: List of code generator plugins (e.g., "java", "cpp", "go")
//   - OutType: Output type for code generation (e.g., "lite", "proto")
//   - IncludeDirs: Additional include directories for proto imports
type ProtoLibrary struct {
	BaseModule
	ProtoSrcs   []string
	ProtoPaths  []string
	Plugins     []string
	OutType     string
	IncludeDirs []string
}

// ProtoLibraryFactory creates ProtoLibrary instances from AST nodes.
type ProtoLibraryFactory struct{}

// Create instantiates a ProtoLibrary from a parsed AST module.
// Parameters:
//   - ast: The parser.Module AST node
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A configured ProtoLibrary and any error that occurred
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
// It differs from ProtoLibrary in that it's a user-defined generator.
// Fields:
//   - BaseModule: Embedded base module with common properties
//   - ProtoSrcs: Source .proto files to process
//   - Plugins: List of code generator plugins to use
//   - OutType: Output type specification
//   - IncludeDirs: Additional include directories for proto imports
type ProtoGen struct {
	BaseModule
	ProtoSrcs   []string
	Plugins     []string
	OutType     string
	IncludeDirs []string
}

// ProtoGenFactory creates ProtoGen instances from AST nodes.
type ProtoGenFactory struct{}

// Create instantiates a ProtoGen from a parsed AST module.
// Parameters:
//   - ast: The parser.Module AST node
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A configured ProtoGen and any error that occurred
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
// Fields:
//   - BaseModule: Embedded base module with all properties
type Custom struct {
	BaseModule
}

// CustomFactory creates Custom instances from AST nodes.
// Used for the "custom" module type and as a fallback for unknown types.
type CustomFactory struct{}

// Create instantiates a Custom from a parsed AST module.
// Parameters:
//   - ast: The parser.Module AST node
//   - eval: Optional evaluator for computing expression values
//
// Returns:
//
//	A configured Custom and any error that occurred
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
// This function is called during package initialization to set up the default
// module type factories for C/C++, Go, Java, Proto, and custom modules.
// Registered types include:
//   - C/C++: cc_library, cc_library_static, cc_library_shared, cc_object, cc_binary, cpp_library, cpp_binary
//   - Go: go_library, go_binary, go_test
//   - Java: java_library, java_library_static, java_library_host, java_binary, java_binary_host, java_test, java_import
//   - Other: filegroup, proto_library, proto_gen, custom
func registerBuiltInModuleTypes() {
	Register("cc_library", &CCLibraryFactory{})
	Register("cc_library_static", &CCLibraryFactory{})
	Register("cc_library_shared", &CCLibraryFactory{})
	Register("cc_object", &CCLibraryFactory{})
	Register("cc_binary", &CCBinaryFactory{})
	Register("cpp_library", &CCLibraryFactory{})
	Register("cpp_binary", &CCBinaryFactory{})
	Register("go_library", &GoLibraryFactory{})
	Register("go_binary", &GoBinaryFactory{})
	Register("go_test", &GoBinaryFactory{})
	Register("java_library", &JavaLibraryFactory{})
	Register("java_library_static", &JavaLibraryFactory{})
	Register("java_library_host", &JavaLibraryFactory{})
	Register("java_binary", &JavaBinaryFactory{})
	Register("java_binary_host", &JavaBinaryFactory{})
	Register("java_test", &JavaBinaryFactory{})
	Register("java_import", &JavaLibraryFactory{})
	Register("filegroup", &CustomFactory{})
	Register("proto_library", &ProtoLibraryFactory{})
	Register("proto_gen", &ProtoGenFactory{})
	Register("custom", &CustomFactory{})
}

// init is the package initialization function that registers all built-in
// module types when the module package is first imported.
func init() {
	registerBuiltInModuleTypes()
}
