// Package module provides the module type system for minibp build rules.
// This file defines the factory registry that maps module type names to
// factory implementations capable of creating Module instances from parsed AST.
//
// Description:
// The registry implements a thread-safe singleton pattern for storing module
// type factories. New module types can be registered at runtime, allowing
// the build system to be extended without modifying core code.
//
// Design decisions:
//   - Thread-safe singleton: Uses RWMutex for optimal read-heavy workloads.
//     Multiple concurrent readers can access the registry simultaneously,
//     while writers get exclusive access.
//   - Factory pattern: Decouples AST parsing from module creation.
//     Each module type has a corresponding factory that knows how to
//     parse AST properties and create the appropriate Module struct.
//   - Alias support: Allows multiple module type names to share
//     the same factory implementation.
//
// Key components:
//   - Factory interface: Creates Module from parser.Module AST nodes
//   - Register/RegisterAlias: Adds new module types to the registry
//   - Lookup/Create: Retrieves factories and creates module instances
//
// Module types are registered during package initialization via init()
// functions in types.go. The built-in types include:
//   - cc_library, cc_binary, cc_test (C/C++ libraries and binaries)
//   - go_library, go_binary (Go packages)
//   - java_library, java_binary (Java compilation)
//   - filegroup (File aggregation)
//   - genrule (Custom build commands)
//   - prebuilt_* (Pre-built library references)
//   - defaults (Default property inheritance)
package module

import (
	"fmt"
	"sync"

	"minibp/lib/parser"
)

// Factory is the interface for creating Module instances from AST nodes.
// Each module type (cc_library, go_binary, java_library, etc.) has a corresponding
// factory that knows how to parse the AST properties and create the appropriate
// Module struct with all fields properly populated.
//
// Description:
// The factory pattern allows the build system to be extended with new module
// types without modifying core code - new types only need to implement this
// interface and register with the registry. This is a form of plugin architecture.
//
// How it works:
// Factories receive a parsed AST Module node containing all properties defined
// in the Blueprint file. The factory extracts these properties, creates a new
// Module struct, populates its fields, and returns the result.
//
// Example implementation:
//
//	type MyModule struct {
//	    module.BaseModule
//	    CustomField string
//	}
//
//	type MyModuleFactory struct{}
//
//	func (f *MyModuleFactory) Create(ast *parser.Module, eval *parser.Evaluator) (module.Module, error) {
//	    m := &MyModule{
//	        BaseModule: module.BaseModule{
//	            Name_:  module.ExtractString(ast.Map, "name", eval),
//	            Type_:  ast.Type,
//	            // ... other fields
//	        },
//	        CustomField: module.ExtractString(ast.Map, "custom_field", eval),
//	    }
//	    return m, nil
//	}
//
//	// Register during init
//	func init() {
//	    module.Register("my_module", &MyModuleFactory{})
//	}
//
// Parameters:
//   - ast: The parsed AST Module node containing all module properties
//   - eval: Optional Evaluator for resolving variables and expressions
//
// Returns:
//   - A Module instance with all properties populated
//   - An error if the AST is invalid or required properties are missing
//
// Error handling:
//   - Return descriptive errors for invalid property values
//   - Include the module name in error messages for debugging
//   - Validate required properties and return errors early
type Factory interface {
	Create(ast *parser.Module, eval *parser.Evaluator) (Module, error)
}

// registryMu provides thread-safe access to the registry map.
// Using RWMutex allows multiple concurrent readers (Lookup, Has, etc.)
// while blocking writers (Register, resetRegistry, etc.) only during
// actual registry modifications. This provides optimal concurrency
// for read-heavy workloads.
var (
	// registryMu is a read-write mutex protecting the registry map.
	// Writers acquire an exclusive lock; readers acquire a shared lock.
	registryMu sync.RWMutex

	// registry maps module type names to their Factory implementations.
	// The map is created empty and populated during package initialization
	// via registerBuiltInModuleTypes() in types.go.
	registry = make(map[string]Factory)
)

// Register adds a factory to the registry for a specific module type name.
//
// Description:
// This is typically called during package initialization to register all
// built-in module types (cc_library, go_binary, java_library, etc.).
// The function is thread-safe and can be called concurrently from multiple
// goroutines. However, registration during initialization is preferred to
// avoid race conditions with code that might call Lookup.
//
// How it works:
// Acquires an exclusive lock on the registry, then stores the factory in the map
// keyed by the module type name.
//
// Parameters:
//   - name: The module type string identifier (e.g., "cc_library", "go_binary")
//     This string appears in Blueprint files as the module type
//   - factory: The Factory implementation responsible for creating modules of this type
//
// Edge cases:
// Registering a factory with an existing name will replace the previous factory.
// This allows for overriding built-in module types if custom behavior is needed,
// though it should be done with caution as it affects all modules of that type.
//
// Example:
//
//	// Register a custom cc_library implementation
//	module.Register("cc_library", &MyCCLibraryFactory{})
func Register(name string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry[name] = factory
}

// Lookup retrieves a factory from the registry by module type name.
//
// Description:
// This is a read-only operation that uses a read lock for optimal concurrency.
// Multiple goroutines can safely call Lookup simultaneously.
//
// How it works:
// Acquires a shared read lock on the registry, then looks up and returns the factory.
//
// Parameters:
//   - name: The module type string to look up (e.g., "cc_library", "go_binary")
//
// Returns:
//   - The Factory registered for that type, or nil if no factory is registered
//
// Example:
//
//	factory := module.Lookup("cc_library")
//	if factory == nil {
//	    return fmt.Errorf("unknown module type: cc_library")
//	}
func Lookup(name string) Factory {
	registryMu.RLock()
	defer registryMu.RUnlock()

	return registry[name]
}

// registrySnapshot creates a shallow copy of the current registry state.
//
// Description:
// This is primarily used in tests to preserve and restore the registry
// between test cases, ensuring tests don't interfere with each other.
//
// How it works:
// Creates a new map and copies all factory references (shallow copy).
// The snapshot contains references to the same factory objects,
// not deep copies of the factories themselves.
//
// Returns:
//   - A map containing copies of all registered factory references
//
// Use with restoreRegistry() to implement test isolation:
//
//	func TestMyFeature(t *testing.T) {
//	    snapshot := module.registrySnapshot()
//	    defer module.restoreRegistry(snapshot)
//	    // ... test code that might register new types
//	}
func registrySnapshot() map[string]Factory {
	registryMu.RLock()
	defer registryMu.RUnlock()

	snapshot := make(map[string]Factory, len(registry))
	for name, factory := range registry {
		snapshot[name] = factory
	}

	return snapshot
}

// restoreRegistry replaces the current registry with a previously saved snapshot.
//
// Description:
// This is used in tests to restore the registry to a previous state, typically
// after using registrySnapshot() to save the initial state.
//
// How it works:
// Acquires an exclusive lock, then replaces the registry map with the snapshot.
// After restoration, subsequent calls to Lookup will find the same factories
// that were registered at the time the snapshot was taken.
//
// Parameters:
//   - snapshot: A map of module type names to factories, as returned by registrySnapshot()
//     If nil, the registry is cleared completely
func restoreRegistry(snapshot map[string]Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if snapshot == nil {
		// Clear the registry if snapshot is nil
		registry = make(map[string]Factory)
		return
	}

	registry = make(map[string]Factory, len(snapshot))
	for name, factory := range snapshot {
		registry[name] = factory
	}
}

// resetRegistry clears all registered factories from the registry.
//
// Description:
// This is used in tests to start with a completely clean registry state,
// ensuring no lingering registrations from previous tests.
//
// How it works:
// Acquires an exclusive lock, then replaces the registry with a new empty map.
//
// Edge cases:
// After reset, Lookup will return nil for all module types until new factories
// are registered. This is useful when testing registration behavior or when
// you want to ensure only explicitly registered types are available.
// Note: This only clears module type factories, not the registry itself.
// The registry map remains functional after clearing.
func resetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]Factory)
}

// registryLen returns the number of registered module type factories.
//
// Description:
// This is primarily used in tests to verify registration state and
// to check that expected module types have been registered.
//
// How it works:
// Acquires a shared read lock, then returns the length of the registry map.
//
// Returns:
//   - The count of registered factories in the registry
//
// Example:
//
//	func TestRegistryPopulated(t *testing.T) {
//	    count := module.registryLen()
//	    if count == 0 {
//	        t.Error("no module types registered")
//	    }
//	}
func registryLen() int {
	registryMu.RLock()
	defer registryMu.RUnlock()

	return len(registry)
}

// RegisterAlias registers a module type name as an alias for another module type.
//
// Description:
// When a module type name is looked up that matches the alias, the factory for
// the baseType is used instead. This allows multiple module type names to share
// the same factory implementation without duplicating registration code.
//
// How it works:
// First looks up the base type factory (with read lock), then registers
// the alias name as pointing to the same factory.
//
// This is useful for:
//   - Providing alternative names for the same module type (e.g., "cc_library" and "cpp_library")
//   - Supporting legacy module type names that have been renamed
//   - Creating shortcuts for verbose module type names
//
// Parameters:
//   - name: The alias module type string
//   - baseType: The base module type string to alias to
//
// Edge cases:
// Only registers alias if the base type exists. This prevents creating dangling aliases.
//
// Example:
//
//	// Both cc_library and cpp_library use the same factory
//	module.RegisterAlias("cpp_library", "cc_library")
//
//	// Now looking up "cpp_library" returns the cc_library factory
//	factory := module.Lookup("cpp_library")  // Returns cc_library factory
func RegisterAlias(name, baseType string) {
	// First, look up the base type factory (need read lock)
	registryMu.RLock()
	base := registry[baseType]
	registryMu.RUnlock()

	// Only register alias if the base type exists
	// This prevents creating dangling aliases
	if base != nil {
		Register(name, base)
	}
}

// Has checks if a module type name is registered in the registry.
//
// Description:
// This is a convenience method to check for module type existence
// without the overhead of retrieving the factory itself.
//
// How it works:
// Acquires a shared read lock, then checks if the key exists in the registry map.
//
// Parameters:
//   - name: The module type string to check
//
// Returns:
//   - true if a factory is registered for the given type
//   - false if no factory is registered (unknown module type)
//
// Example:
//
//	if !module.Has("cc_library") {
//	    return fmt.Errorf("cc_library module type not available")
//	}
func Has(name string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[name]
	return ok
}

// Create builds a Module from an AST node using the appropriate factory.
//
// Description:
// This is the main entry point for creating modules from parsed Blueprint AST.
// The function looks up the factory by the module type string in the AST
// and delegates the actual module creation to that factory.
//
// How it works:
// This is a convenience function that combines Lookup and factory.Create()
// into a single operation, handling the "unknown module type" error case.
//
// Parameters:
//   - ast: The parsed AST Module node containing all module properties
//     The Type field of the AST determines which factory is used
//   - eval: Optional Evaluator for resolving variables and expressions
//     in property values
//
// Returns:
//   - A Module instance with all properties populated from the AST
//   - An error if the module type is not registered or creation fails
//
// Error conditions:
//   - Returns error "unknown module type: <type>" if no factory is registered
//   - Forwards any error returned by the factory's Create method
//
// Example:
//
//	module, err := module.Create(parsedModule, evaluator)
//	if err != nil {
//	    return fmt.Errorf("failed to create module: %w", err)
//	}
func Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	factory := Lookup(ast.Type)
	if factory == nil {
		return nil, fmt.Errorf("unknown module type: %s", ast.Type)
	}
	return factory.Create(ast, eval)
}
