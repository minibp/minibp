// Package module provides the module type system for minibp build rules.
// It defines the base Module interface that all module types must implement,
// a common BaseModule struct with shared fields, and a factory registry for creating
// modules from parsed AST nodes.
//
// The module system follows a plugin architecture where new module types can be
// added by implementing the Module interface and Factory interface, then registering
// with the global registry. This allows the build system to be extended without
// modifying core code.
//
// Core components:
//   - Module interface: Unified API for accessing module properties
//   - BaseModule: Common implementation with shared fields
//   - Factory interface: Creates Module instances from parsed AST
//   - Registry: Maps module type names to factory implementations
package module

// Module is the interface that all module types must implement.
// It provides a unified API for accessing module properties regardless
// of the specific module type (cc_library, go_binary, java_library, etc.).
// The build system uses this interface to work with modules generically
// during dependency resolution, build graph construction, and ninja file generation.
//
// Implementation notes:
//   - Each method returns a specific category of module information
//   - The interface is minimal to allow easy implementation by new module types
//   - Returning slices/maps should return references to internal storage,
//     not copies, for memory efficiency
//
// Each method provides:
// - Name/Type: identification for referencing and distinguishing module types
// - Srcs/Deps: build configuration specifying what to build and dependencies
// - Props: arbitrary key-value properties for module-specific settings
type Module interface {
	// Name returns the unique name of this module within its package.
	// This name is used to reference this module from other modules via
	// the ":name" syntax in Blueprint files (e.g., "//path:modulename").
	//
	// The name must be unique within a single Blueprint package but different
	// packages can have modules with the same name - they are distinguished
	// by their full path (e.g., "//path/to/package:modulename").
	//
	// Returns:
	//   - The module's unique name within its package
	Name() string

	// Type returns the module type string that identifies this module's category.
	// Common types include: "cc_library", "cc_binary", "go_library", "go_binary",
	// "java_library", "java_binary", "proto_library", "filegroup", "genrule", etc.
	//
	// The type string determines:
	//   - Which factory was used to create this module
	//   - How the module is built (compiler, linker, etc.)
	//   - What ninja rules are generated for this module
	//
	// Returns:
	//   - The module type identifier string
	Type() string

	// Srcs returns the list of source files for this module.
	// Paths are relative to the Blueprint file location where the module is defined.
	// The returned slice may be empty if the module has no sources, for example:
	//   - A cc_library_headers module that only provides header files
	//   - A filegroup that aggregates existing files
	//   - A phony target that doesn't produce build artifacts
	//
	// Returns:
	//   - A slice of source file paths, may be empty but never nil
	Srcs() []string

	// Deps returns the list of direct dependency module names.
	// Each dependency is referenced by its module name using the ":name" format,
	// or by full path "//namespace:module" for cross-namespace references.
	//
	// Important notes:
	//   - The returned slice does NOT include transitive dependencies
	//   - The build system resolves the full dependency tree separately
	//   - Dependencies are used for:
	//     - Build order (ensuring dependencies are built first)
	//     - Include path propagation (for C/C++ headers)
	//     - Link order (for linking libraries)
	//     - Runtime dependency tracking
	//
	// Returns:
	//   - A slice of dependency module names, may be empty but never nil
	Deps() []string

	// Props returns all properties as a map for generic access.
	// This includes both built-in properties (name, srcs, deps) and any
	// additional custom properties defined in the Blueprint file.
	//
	// The map contains:
	//   - All properties from the Blueprint module definition
	//   - Values are evaluated (variables resolved, expressions computed)
	//   - Nested structures are preserved as map[string]interface{} or []interface{}
	//
	// Implementation note:
	//   - Returns a reference to the internal property storage, not a copy
	//   - Callers should NOT modify the returned map as it affects internal state
	//
	// Returns:
	//   - A map from property names to their values (string, int, bool, []interface{}, map[string]interface{})
	Props() map[string]interface{}

	// GetProp retrieves a specific property value by key.
	// This is a convenience method for accessing a single property
	// without iterating over the entire Props map.
	//
	// Returns:
	//   - The property value if found (string, int, bool, []interface{}, or map[string]interface{})
	//   - nil if the property does not exist
	//
	// Use type assertions to convert the returned value:
	//   - str, ok := m.GetProp("cflags").(string)
	//   - flags, ok := m.GetProp("cflags").([]string)
	GetProp(key string) interface{}
}

// BaseModule provides a common implementation of the Module interface.
// It embeds the core fields that all module types share, following the
// composition pattern for code reuse.
//
// Module types embed BaseModule (either directly or via embedding another
// module type struct that embeds BaseModule) and add their specific fields.
// For example:
//
//	type CCLibrary struct {
//	    module.BaseModule  // embeds BaseModule
//	    CFlags   []string  // C/C++ specific fields
//	    Includes []string
//	}
//
// This pattern allows:
//   - Reusing common module functionality (Name, Type, Srcs, Deps, Props)
//   - Adding type-specific fields without duplicating code
//   - Maintaining the Module interface through embedding
//
// Fields use trailing underscores (Name_, Type_, etc.) to avoid conflicts
// with method names in embedding types that might want to use the same names.
type BaseModule struct {
	// Name_ is the module name, unique within its package.
	// Set from the "name" property in the Blueprint definition.
	Name_ string

	// Type_ is the module type string (e.g., "cc_library", "go_binary").
	// Identifies which factory created this module and determines build rules.
	Type_ string

	// Srcs_ is the list of source files to compile for this module.
	// Populated from the "srcs" property, may be empty for header-only libs.
	Srcs_ []string

	// Deps_ is the list of direct module dependencies.
	// Includes deps, shared_libs, and header_libs properties combined.
	Deps_ []string

	// Props_ is a map of all additional properties as key-value pairs.
	// Contains both built-in and custom properties from the Blueprint definition.
	// Values are already evaluated (variables resolved, expressions computed).
	Props_ map[string]interface{}
}

// Name returns the module name from the Name_ field.
// This implements the Module interface method Name().
//
// Returns:
//   - The Name_ field value, which is always set for valid modules
func (m *BaseModule) Name() string { return m.Name_ }

// Type returns the module type string from the Type_ field.
// This implements the Module interface method Type().
//
// Returns:
//   - The Type_ field value (e.g., "cc_library", "go_binary")
func (m *BaseModule) Type() string { return m.Type_ }

// Srcs returns the list of source files from the Srcs_ field.
// This implements the Module interface method Srcs().
//
// Returns:
//   - The Srcs_ field value, a slice that may be empty but never nil
func (m *BaseModule) Srcs() []string { return m.Srcs_ }

// Deps returns the list of dependency module names from the Deps_ field.
// This implements the Module interface method Deps().
//
// Returns:
//   - The Deps_ field value, a slice that may be empty but never nil
func (m *BaseModule) Deps() []string { return m.Deps_ }

// Props returns a reference to the internal properties map from the Props_ field.
// This implements the Module interface method Props().
//
// IMPORTANT: Returns the internal map reference, not a copy.
// Callers must not modify the returned map as it shares storage with the module.
//
// Returns:
//   - The Props_ field value, the internal map storage
func (m *BaseModule) Props() map[string]interface{} { return m.Props_ }

// GetProp looks up a property by key in the Props_ map and returns its value.
// This implements the Module interface method GetProp().
//
// Returns:
//   - The property value if the key exists in Props_
//   - nil if the key does not exist (property not defined)
//
// Edge cases:
//   - Returns nil if Props_ is nil (no properties defined)
//   - Returns nil for existing keys with nil values
func (m *BaseModule) GetProp(key string) interface{} { return m.Props_[key] }
