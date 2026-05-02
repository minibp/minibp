// Package namespace provides namespace resolution for Blueprint modules,
// handling soong_namespace definitions, module reference resolution with namespace
// prefixes (e.g., "//namespace:module"), override module application, and
// soong_config_module_type processing.
//
// This package enables modular build configurations by controlling module visibility
// across different namespaces. Key features include:
//   - Namespace metadata extraction from soong_namespace modules
//   - Fully qualified module reference resolution (//ns:module)
//   - Override module application to modify base module properties
//   - Custom module type registration via soong_config_module_type
//
// Namespaces act as containers for modules: modules within the same namespace
// can reference each other directly, while cross-namespace references require
// the //namespace:module prefix notation.
//
// The package provides the following core functions:
//   - BuildMap: Constructs namespace metadata map from parsed modules
//   - ResolveModuleRef: Resolves namespace-prefixed module references
//   - ApplyOverrides: Applies override modules to their base modules
//   - ApplySoongConfigModuleTypes: Processes custom module type definitions
package namespace

import (
	"fmt"
	"minibp/lib/module"
	"minibp/lib/parser"
	"minibp/lib/variant"
	"strings"
)

// Info represents namespace metadata extracted from a soong_namespace module.
//
// Description:
// Info holds the list of namespace imports that define which other namespaces are visible
// within this namespace's scope. A namespace in Blueprint acts as a container for modules,
// controlling module visibility and enabling modular build configurations.
//
// How it works:
// Modules within a namespace can reference each other directly, while modules in other
// namespaces require explicit namespace prefixes (e.9., "//otherns:module").
//
// Fields:
//   - Imports: List of namespace names that are imported into this namespace, allowing modules
//     in those namespaces to be referenced without explicit namespace prefixes
//
// Edge cases:
//   - Empty Imports slice indicates no other namespaces are imported
//   - Namespace without imports property results in empty Imports
//   - Namespace with empty name property is invalid and skipped during BuildMap
type Info struct {
	Imports []string // List of namespace names imported into this namespace
}

// BuildMap constructs a mapping from namespace names to their metadata (Info).
//
// Description:
// It scans the provided modules map for soong_namespace modules and extracts their name and imports properties.
// Only modules with type "soong_namespace" and a non-nil Map are processed.
//
// How it works:
// The function iterates through all modules and filters for soong_namespace type. For each soong_namespace
// module found, it extracts the namespace name using getStringProp and then searches the module's Map
// properties for an "imports" property. The imports property is expected to be a list of string
// values, each representing another namespace that is visible within this namespace's scope.
//
// This function is called during the build pipeline (Step 5) to create the namespace map used
// for resolving module references like "//ns:module".
//
// Parameters:
//   - modules: A map from module names to their parsed Module representations.
//     This map is created by buildlib.CollectModulesWithNames during the build process.
//   - getStringProp: A function to retrieve a string property from a module by name.
//     This allows flexible property access with optional evaluation.
//
// Returns:
//   - map[string]*Info: A map from namespace names to their Info struct containing import lists.
//     Namespaces without a valid name are excluded from the result.
//     Returns empty map if no soong_namespace modules are found.
//
// Edge cases:
//   - Modules without type "soong_namespace" are skipped
//   - Modules without a Map are skipped (nil check)
//   - Modules with empty name property are skipped
//   - Non-list imports values are ignored
//   - Non-string values within the imports list are ignored
func BuildMap(modules map[string]*parser.Module, getStringProp func(*parser.Module, string) string) map[string]*Info {
	result := make(map[string]*Info) // Initialize empty namespace map
	for _, mod := range modules {    // Iterate through all parsed modules
		if mod.Type != "soong_namespace" || mod.Map == nil {
			continue // Skip non-soong_namespace modules or those without properties
		}
		name := getStringProp(mod, "name") // Extract namespace name from module properties
		if name == "" {
			continue // Skip namespaces with empty name
		}
		ns := &Info{}                             // Initialize new namespace info struct
		for _, prop := range mod.Map.Properties { // Iterate through module properties to find imports
			if prop.Name == "imports" {
				// Check for imports property
				if l, ok := prop.Value.(*parser.List); ok {
					// Ensure imports is a list type
					for _, v := range l.Values { // Iterate through import list values
						if s, ok := v.(*parser.String); ok {
							// Collect only string import values
							ns.Imports = append(ns.Imports, s.Value) // Add valid import to namespace
						}
					}
				}
			}
		}
		result[name] = ns // Store namespace info in result map
	}
	return result
}

// ResolveModuleRef resolves a module reference that may include namespace prefix notation.
//
// Description:
// In Blueprint, module references can be either simple names (e.9., "libfoo") or
// namespace-qualified (e.9., "//namespace:libfoo").
//
// How it works:
// The function handles the following reference formats:
//   - ":modulename" - shorthand for current namespace (strips leading colon).
//     This indicates reference to a module in the current namespace context.
//   - "//namespace:modulename" - fully qualified namespace reference.
//     This explicitly specifies which namespace contains the target module.
//   - "modulename" - returns as-is if namespace not recognized.
//     Used for modules in the default/visibility namespace.
//
// The resolution algorithm:
//  1. Strip leading colon for shorthand syntax (":modulename" -> "modulename")
//  2. Check for "//" prefix indicating fully qualified reference
//  3. Extract namespace name between "//" and ":"
//  4. Verify namespace exists in the namespace map
//  5. Return module name if namespace found, otherwise return original reference
//
// This function is used by the dependency graph to resolve module references in srcs, deps,
// and lib_deps properties during build graph construction.
//
// Parameters:
//   - ref: The module reference string to resolve. Can be in any of the formats described above.
//     Must not be empty for meaningful resolution.
//   - namespaces: A map of namespace names to their Info structs. This map is created by
//     BuildMap and contains all defined namespaces.
//
// Returns:
//   - string: The resolved module name. For fully-qualified references that match an existing
//     namespace, returns only the module name portion. For other references, returns the original
//     reference with prefix removed. Returns empty string if input ref is empty.
//
// Edge cases:
//   - References with "//" but no ":" separator return original reference
//   - References with non-existent namespace return original reference
//   - Empty references return empty string
func ResolveModuleRef(ref string, namespaces map[string]*Info) string {
	ref = strings.TrimPrefix(ref, ":") // Strip leading colon for shorthand syntax
	if strings.HasPrefix(ref, "//") {  // Check for fully qualified namespace reference
		sepIdx := strings.Index(ref, ":") // Find ":" separator between namespace and module
		if sepIdx >= 0 {                  // Ensure separator exists
			nsName := ref[2:sepIdx]              // Extract namespace name from reference
			modName := ref[sepIdx+1:]            // Extract module name from reference
			if _, ok := namespaces[nsName]; ok { // Verify namespace exists in map
				return modName // Return resolved module name
			}
		}
	}
	return ref // Return original reference if resolution failed
}

// ApplyOverrides applies override modules to their base modules in the module map.
//
// Description:
// In Blueprint, override modules (marked with Override: true) modify the properties of an
// existing module rather than creating a new one. This function performs the merge by combining
// the override's properties with the base module's properties using variant.MergeMapProps.
//
// How it works:
// The override mechanism allows build configurations to modify module properties without creating
// entirely new modules. Common use cases include enabling debug builds, changing compiler
// flags for specific configurations, or adding conditional dependencies.
//
// The override module must be a distinct module from the base (i.e., cannot reference itself).
// Both modules must have non-nil Map properties for the merge to occur.
//
// The merge algorithm:
//  1. Iterate through all modules looking for Override: true
//  2. For each override, find the base module by name
//  3. Skip if base doesn't exist or is the same module
//  4. Merge override Map properties into base Map using variant.MergeMapProps
//  5. Update the module map with the merged base module
//
// This function is called during build graph construction (Step 6) to apply any module
// overrides before generating the Ninja build file.
//
// Parameters:
//   - modules: A map from module names to their parsed Module representations.
//     This map is modified in place as base modules are updated with override properties.
//
// Returns:
//   - void: The modules map is modified in place.
//
// Edge cases:
//   - Override module referencing non-existent base is skipped
//   - Override module referencing itself is skipped (check by pointer equality)
//   - Modules without Map properties are skipped
//   - Override with nil Map is skipped
func ApplyOverrides(modules map[string]*parser.Module) {
	for name, ovr := range modules { // Iterate through all modules to find overrides
		if !ovr.Override {
			continue // Skip non-override modules
		}
		base, ok := modules[name] // Look up base module by name
		if !ok || base == ovr {
			continue // Skip non-existent base or self-reference
		}
		if base.Map != nil && ovr.Map != nil { // Ensure both modules have properties to merge
			variant.MergeMapProps(base, ovr.Map) // Merge override properties into base
		}
		modules[name] = base // Update module map with merged base
	}
}

// ApplySoongConfigModuleTypes processes soong_config_module_type modules.
//
// Description:
// These modules define custom configuration namespaces and variable templates. This function extracts
// configuration variables and registers module type aliases for the defined configuration namespace.
//
// How it works:
// soong_config_module_type allows build configurations to define custom module types that wrap
// existing module types with pre-configured properties. This is useful for creating
// platform-specific variants, toolchain configurations, or language-specific shortcuts.
//
// For each soong_config_module_type module:
//  1. Extracts the base module type, config namespace, and type name
//  2. Processes the "vars" property to set configuration values
//  3. Registers an alias from the config type name to the base type
//
// The processing algorithm:
//  1. Iterate through all modules looking for soong_config_module_type
//  2. Extract required properties: module_type, config_namespace, name
//  3. Process vars Map to set configuration values in evaluator
//  4. Register type alias if not already registered
//
// This function is called during build pipeline initialization to register custom
// module types before module collection begins.
//
// Parameters:
//   - modules: A map from module names to their parsed Module representations
//   - getStringProp: A function to retrieve a string property from a module by name
//   - eval: The evaluator instance used to set configuration values.
//     Configuration variables are set using eval.SetConfig for later resolution.
//
// Returns:
//   - void: Module type aliases are registered in the module registry.
//
// Edge cases:
//   - Modules without required properties are skipped
//   - Empty module_type or typeName skips registration
//   - Non-Map vars values are ignored
//   - Non-String property values in vars are ignored
//   - Already registered types are not re-registered (Has check)
func ApplySoongConfigModuleTypes(modules map[string]*parser.Module, getStringProp func(*parser.Module, string) string, eval *parser.Evaluator) {
	for _, ct := range modules { // Iterate through modules to find config types
		if ct.Type != "soong_config_module_type" {
			continue // Skip non-config-module-type modules
		}
		baseType := getStringProp(ct, "module_type") // Extract base module type
		ns := getStringProp(ct, "config_namespace")  // Extract config namespace
		typeName := getStringProp(ct, "name")        // Extract custom type name
		if baseType == "" || typeName == "" {
			continue // Skip if required properties missing
		}
		if ct.Map != nil { // Check if module has properties
			for _, prop := range ct.Map.Properties { // Iterate through properties to find vars
				if prop.Name == "vars" { // Check for vars property
					if mp, ok := prop.Value.(*parser.Map); ok { // Ensure vars is a map type
						for _, p := range mp.Properties { // Iterate through vars map entries
							if s, ok := p.Value.(*parser.String); ok { // Collect string var values
								key := fmt.Sprintf("%s.%s", ns, p.Name) // Build config key (namespace.var)
								eval.SetConfig(key, s.Value)            // Set config value in evaluator
							}
						}
					}
				}
			}
		}
		if !module.Has(typeName) { // Register alias only if not already registered
			module.RegisterAlias(typeName, baseType) // Register custom type alias
		}
	}
}
