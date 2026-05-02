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
// Design decisions:
//   - Module interface: Minimal interface allows easy implementation by new types.
//     Each method returns a specific category of module information.
//   - BaseModule struct: Uses composition (embedding) to provide common fields.
//     Fields use trailing underscores to avoid conflicts with method names.
//   - Factory pattern: Decouples AST parsing from module creation, enabling
//     custom module types without modifying the build pipeline.
//   - Thread-safe registry: Uses RWMutex for optimal read-heavy workloads.
//
// Core components:
//   - Module interface: Unified API for accessing module properties
//   - BaseModule: Common implementation with shared fields
//   - Factory interface: Creates Module instances from parsed AST
//   - Registry: Maps module type names to factory implementations
//
// Examples:
//
//	To create a custom module type, embed BaseModule and add type-specific fields:
//
//	  type CCLibrary struct {
//	      module.BaseModule  // Inherits all Module interface methods
//	      CFlags   []string  // C/C++ compiler flags
//	      Includes []string  // Include search paths
//	  }
//
//	Factories populate BaseModule fields from parsed Blueprint properties and
//	register the module with the global registry for build processing.
package module

// Module is the interface that all module types must implement.
// It provides a unified API for accessing module properties regardless
// of the specific module type (cc_library, go_binary, java_library, etc.).
// The build system uses this interface to work with modules generically
// during dependency resolution, build graph construction, and ninja file generation.
//
// Description:
// The Module interface is the core abstraction for build modules.
// It provides methods to access:
//   - Identification: Name and Type for referencing and distinguishing modules
//   - Build configuration: Srcs and Deps for specifying what to build
//   - Custom properties: Props and GetProp for arbitrary key-value settings
//
// Implementation notes:
//   - Each method returns a specific category of module information
//   - The interface is minimal to allow easy implementation by new module types
//   - Returning slices/maps should return references to internal storage,
//     not copies, for memory efficiency
//   - Callers must NOT modify returned maps as they share storage with the module
type Module interface {
	// Name returns the unique name of this module within its package.
	// This name is used to reference this module from other modules via
	// the ":name" syntax in Blueprint files (e.g., "//path:modulename").
	//
	// Description:
	// Returns the module's unique identifier within its Blueprint package.
	// The name must be unique within a single package but different packages
	// can have modules with the same name - they are distinguished by their
	// full path (e.g., "//path/to/package:modulename").
	//
	// How it works:
	// Returns the Name_ field stored in the embedded BaseModule struct.
	// The factory sets this field from the "name" property during module creation.
	//
	// Returns:
	//   - The module's unique name within its package (string)
	Name() string

	// Type returns the module type string that identifies this module's category.
	// Common types include: "cc_library", "cc_binary", "go_library", "go_binary",
	// "java_library", "java_binary", "proto_library", "filegroup", "genrule", etc.
	//
	// Description:
	// Returns the module type identifier that determines how the module is built.
	// The type string determines:
	//   - Which factory was used to create this module
	//   - How the module is built (compiler, linker, etc.)
	//   - What ninja rules are generated for this module
	//
	// How it works:
	// Returns the Type_ field stored in the embedded BaseModule struct.
	// The factory sets this field from the AST module's Type field.
	//
	// Returns:
	//   - The module type identifier string (e.g., "cc_library", "go_binary")
	Type() string

	// Srcs returns the list of source files for this module.
	// Paths are relative to the Blueprint file location where the module is defined.
	//
	// Description:
	// Returns the source files that need to be compiled or processed for this module.
	// The returned slice may be empty if the module has no sources, for example:
	//   - A cc_library_headers module that only provides header files
	//   - A filegroup that aggregates existing files
	//   - A phony target that doesn't produce build artifacts
	//
	// How it works:
	// Returns the Srcs_ field stored in the embedded BaseModule struct.
	// The factory sets this field from the "srcs" property during module creation.
	//
	// Returns:
	//   - A slice of source file paths (may be empty but never nil)
	Srcs() []string

	// Deps returns the list of direct dependency module names.
	// Each dependency is referenced by its module name using the ":name" format,
	// or by full path "//namespace:module" for cross-namespace references.
	//
	// Description:
	// Returns the direct dependencies that this module depends on for building.
	// Important notes:
	//   - The returned slice does NOT include transitive dependencies
	//   - The build system resolves the full dependency tree separately
	//   - Dependencies are used for:
	//     - Build order (ensuring dependencies are built first)
	//     - Include path propagation (for C/C++ headers)
	//     - Link order (for linking libraries)
	//     - Runtime dependency tracking
	//
	// How it works:
	// Returns the Deps_ field stored in the embedded BaseModule struct.
	// The factory sets this field from the "deps", "shared_libs", and "header_libs"
	// properties during module creation, with deduplication.
	//
	// Returns:
	//   - A slice of dependency module names (may be empty but never nil)
	Deps() []string

	// Props returns all properties as a map for generic access.
	// This includes both built-in properties (name, srcs, deps) and any
	// additional custom properties defined in the Blueprint file.
	//
	// Description:
	// Returns all module properties as a map for generic access.
	// The map contains:
	//   - All properties from the Blueprint module definition
	//   - Values are evaluated (variables resolved, expressions computed)
	//   - Nested structures are preserved as map[string]interface{} or []interface{}
	//
	// How it works:
	// Returns a reference to the internal Properties map stored in the BaseModule struct.
	// The factory populates this map during module creation by extracting all properties
	// from the AST, converting each property value to native Go types.
	//
	// Important notes:
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
	// Description:
	// Looks up a property by name in the internal properties map.
	// This is equivalent to m.Props()[key] but provides a cleaner API.
	//
	// How it works:
	// Looks up the key in the internal BaseModule.Props_ map and returns the value.
	// If the key doesn't exist, returns nil.
	//
	// Parameters:
	//   - key: The property name to look up
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
// Description:
// BaseModule is a reusable struct that provides the common fields required
// by all module types. Module types embed BaseModule (either directly or
// via embedding another module type struct that embeds BaseModule) and add
// their specific fields.
//
// How it works:
// The struct uses Go's embedding feature to provide all Module interface
// methods through composition. Embedding types can override methods if needed,
// but typically they inherit the default implementations.
//
// Example:
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
// Design decisions:
//   - Fields use trailing underscores (Name_, Type_, etc.) to avoid conflicts
//     with method names in embedding types that might want to use the same names
//
// Edge cases:
//   - Zero-value BaseModule is valid but Name() will return empty string
//   - Nil Srcs_ or Deps_ slices are replaced with empty slices by factories
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
// Implements the Module interface's Name method to provide the module's unique identifier.
//
// Returns:
//   - The Name_ field value (string); empty if the BaseModule is zero-value
//
// Edge cases:
//   - Zero-value BaseModule: Returns empty string since Name_ is not initialized
//
// Notes:
//   - Simply returns the internal Name_ field without modification
//   - Complies with the Module interface requirement
func (m *BaseModule) Name() string { return m.Name_ }

// Type returns the module type string from the Type_ field.
// Implements the Module interface's Type method to identify the module's build category.
//
// Returns:
//   - The Type_ field value (string, e.g., "cc_library", "go_binary"); empty if zero-value BaseModule
//
// Edge cases:
//   - Zero-value BaseModule: Returns empty string since Type_ is not initialized
//
// Notes:
//   - Determines which build rules are generated for this module
//   - Simply returns the internal Type_ field without modification
func (m *BaseModule) Type() string { return m.Type_ }

// Srcs returns the list of source files from the Srcs_ field.
// Implements the Module interface's Srcs method to provide the module's source files.
//
// Returns:
//   - The Srcs_ field value ([]string); may be empty for header-only modules; never nil if created via factory
//
// Edge cases:
//   - Zero-value BaseModule: Returns nil (Srcs_ is not initialized)
//   - Factory-created modules: Srcs_ is guaranteed to be non-nil (replaced with empty slice if nil)
//
// Notes:
//   - Simply returns the internal Srcs_ field without modification
//   - Paths are relative to the Blueprint file defining the module
func (m *BaseModule) Srcs() []string { return m.Srcs_ }

// Deps returns the list of direct dependency module names from the Deps_ field.
// Implements the Module interface's Deps method to provide the module's dependencies.
//
// Returns:
//   - The Deps_ field value ([]string); may be empty if no dependencies; never nil if created via factory
//
// Edge cases:
//   - Zero-value BaseModule: Returns nil (Deps_ is not initialized)
//   - Factory-created modules: Deps_ is guaranteed to be non-nil (replaced with empty slice if nil)
//   - Dependencies are direct only, not transitive
//
// Notes:
//   - Simply returns the internal Deps_ field without modification
//   - Dependencies are referenced by ":name" or "//path:name" syntax
func (m *BaseModule) Deps() []string { return m.Deps_ }

// Props returns a reference to the internal properties map from the Props_ field.
// Implements the Module interface's Props method to provide all module properties.
//
// Returns:
//   - The Props_ field value (map[string]interface{}); may be nil if zero-value BaseModule or no properties
//
// Edge cases:
//   - Zero-value BaseModule: Returns nil (Props_ is not initialized)
//   - No custom properties: Returns a non-nil map containing only built-in properties (name, srcs, deps)
//
// Notes:
//   - Returns a reference to the internal map, not a copy
//   - Callers must NOT modify the returned map to avoid corrupting module state
//   - Contains both built-in and custom properties from the Blueprint definition
func (m *BaseModule) Props() map[string]interface{} { return m.Props_ }

// GetProp looks up a property by key in the Props_ map and returns its value.
// Implements the Module interface's GetProp method for convenient property access.
//
// Parameters:
//   - key: The property name to look up (string)
//
// Returns:
//   - The property value (interface{}: string, int, bool, []interface{}, map[string]interface{})
//   - nil if the key does not exist or Props_ is nil
//
// Edge cases:
//   - Props_ is nil: Returns nil (no properties defined)
//   - Key exists with nil value: Returns nil
//   - Key does not exist: Returns nil
//
// Notes:
//   - Use type assertions to convert the returned value to the expected type
//   - Example: cflags, ok := m.GetProp("cflags").([]string)
func (m *BaseModule) GetProp(key string) interface{} { return m.Props_[key] }
