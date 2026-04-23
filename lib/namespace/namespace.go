// Package namespace provides namespace resolution for Blueprint modules.
// It handles soong_namespace modules, module reference resolution with namespace
// prefixes (e.g., "//namespace:module"), override modules, and soong_config_module_type
// configuration. Namespaces control which modules are visible within different scopes
// and enable modular build configurations.
package namespace

import (
	"fmt"
	"minibp/lib/module"
	"minibp/lib/parser"
	"minibp/lib/variant"
	"strings"
)

// Info holds namespace metadata extracted from a soong_namespace module.
// It contains the list of namespace imports that define which other
// namespaces are visible within this namespace's scope.
type Info struct {
	// Imports is a list of namespace names that are imported into
	// this namespace, allowing modules in those namespaces to be
	// referenced without explicit namespace prefixes.
	Imports []string
}

// BuildMap constructs a mapping from namespace names to their metadata (Info).
// It scans the provided modules map for soong_namespace modules and extracts
// their name and imports properties. Only modules with type "soong_namespace"
// and a non-nil Map are processed.
//
// The function iterates through all modules and filters for soong_namespace type.
// For each soong_namespace module found, it extracts the namespace name using
// getStringProp and then searches the module's Map properties for an "imports" property.
// The imports property is expected to be a list of string values, each representing
// another namespace that is visible within this namespace's scope.
//
// Parameters:
//   - modules: A map from module names to their parsed Module representations
//   - getStringProp: A function to retrieve a string property from a module by name
//
// Returns:
//   - A map from namespace names to their Info struct containing import lists.
//     Namespaces without a valid name are excluded from the result.
//
// Edge cases:
//   - Modules without type "soong_namespace" are skipped
//   - Modules without a Map are skipped (nil check)
//   - Modules with empty name property are skipped
//   - Non-list imports values are ignored
//   - Non-string values within the imports list are ignored
func BuildMap(modules map[string]*parser.Module, getStringProp func(*parser.Module, string) string) map[string]*Info {
	result := make(map[string]*Info)
	// Iterate through all modules, filtering for soong_namespace type.
	// This allows multiple namespace definitions across different files.
	for _, mod := range modules {
		// Skip non-soong_namespace modules and modules without properties.
		if mod.Type != "soong_namespace" || mod.Map == nil {
			continue
		}
		// Extract namespace name from properties.
		// Empty names are skipped as they are invalid.
		name := getStringProp(mod, "name")
		if name == "" {
			continue
		}
		// Create Info struct and populate imports from the Map.
		ns := &Info{}
		// Search Map properties for the "imports" property.
		// This property contains a list of namespace names to import.
		for _, prop := range mod.Map.Properties {
			if prop.Name == "imports" {
				// Extract string values from the imports list.
				// Non-list values or non-string values are ignored.
				if l, ok := prop.Value.(*parser.List); ok {
					for _, v := range l.Values {
						if s, ok := v.(*parser.String); ok {
							ns.Imports = append(ns.Imports, s.Value)
						}
					}
				}
			}
		}
		result[name] = ns
	}
	return result
}

// ResolveModuleRef resolves a module reference that may include namespace
// prefix notation. In Blueprint, module references can be either simple
// names (e.g., "libfoo") or namespace-qualified (e.g., "//namespace:libfoo").
//
// The function handles the following reference formats:
//   - ":modulename" - shorthand for current namespace (strips leading colon)
//   - "//namespace:modulename" - fully qualified namespace reference
//   - "modulename" - returns as-is if namespace not recognized
//
// The resolution algorithm:
//  1. Strip leading colon for shorthand syntax
//  2. Check for "//" prefix indicating fully qualified reference
//  3. Extract namespace name between "//" and ":"
//  4. Verify namespace exists in the namespace map
//  5. Return module name if namespace found, otherwise return original reference
//
// Parameters:
//   - ref: The module reference string to resolve
//   - namespaces: A map of namespace names to their Info structs
//
// Returns:
//   - The resolved module name. For fully-qualified references that match an
//     existing namespace, returns only the module name portion. For other
//     references, returns the original reference with prefix removed.
//
// Edge cases:
//   - References with "//" but no ":" separator return original reference
//   - References with non-existent namespace return original reference
//   - Empty references return empty string
func ResolveModuleRef(ref string, namespaces map[string]*Info) string {
	// Strip leading colon for shorthand namespace syntax (e.g., ":libfoo").
	// This indicates reference to current namespace.
	ref = strings.TrimPrefix(ref, ":")
	// Check for fully qualified namespace syntax (e.g., "//ns:libfoo").
	// The "//" prefix indicates namespace qualification.
	if strings.HasPrefix(ref, "//") {
		// Find the separator between namespace and module name.
		// Format is "//namespace:modulename".
		sepIdx := strings.Index(ref, ":")
		if sepIdx >= 0 {
			// Extract namespace name (between "//" and ":").
			// Extract module name (after ":").
			nsName := ref[2:sepIdx]
			modName := ref[sepIdx+1:]
			// Verify namespace exists before resolving.
			// If namespace not found, return original reference.
			if _, ok := namespaces[nsName]; ok {
				return modName
			}
		}
	}
	// Return original reference if not namespace-qualified
	// or if namespace was not found in the namespace map.
	return ref
}

// ApplyOverrides applies override modules to their base modules in the module map.
// In Blueprint, override modules (marked with Override: true) modify the properties
// of an existing module rather than creating a new one. This function performs
// the merge by combining the override's properties with the base module's
// properties using variant.MergeMapProps.
//
// The override module must be a distinct module from the base (i.e., cannot
// reference itself). Both modules must have non-nil Map properties for the merge
// to occur.
//
// The merge algorithm:
//  1. Iterate through all modules looking for Override: true
//  2. For each override, find the base module by name
//  3. Skip if base doesn't exist or is the same module
//  4. Merge override Map properties into base Map using variant.MergeMapProps
//  5. Update the module map with the merged base module
//
// Parameters:
//   - modules: A map from module names to their parsed Module representations.
//     This map is modified in place as base modules are updated with override properties.
//
// Edge cases:
//   - Override module referencing non-existent base is skipped
//   - Override module referencing itself is skipped (check by pointer equality)
//   - Modules without Map properties are skipped
//   - Override with nil Map is skipped
func ApplyOverrides(modules map[string]*parser.Module) {
	// Iterate through all modules looking for override modules.
	// Override modules have Override field set to true.
	for name, ovr := range modules {
		if !ovr.Override {
			continue
		}
		// Look up the base module by name.
		// Skip if base module doesn't exist or is the same module.
		base, ok := modules[name]
		if !ok || base == ovr {
			continue
		}
		// Merge override properties into base module.
		// variant.MergeMapProps handles list concatenation
		// and property override logic.
		if base.Map != nil && ovr.Map != nil {
			variant.MergeMapProps(base, ovr.Map)
		}
		// Update the module map with merged base.
		// The override module is replaced by the merged base.
		modules[name] = base
	}
}

// ApplySoongConfigModuleTypes processes soong_config_module_type modules,
// which define custom configuration namespaces and variable templates.
// This function extracts configuration variables and registers module
// type aliases for the defined configuration namespace.
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
// Parameters:
//   - modules: A map from module names to their parsed Module representations
//   - getStringProp: A function to retrieve a string property from a module by name
//   - eval: The evaluator instance used to set configuration values
//
// Edge cases:
//   - Modules without required properties are skipped
//   - Empty module_type or typeName skips registration
//   - Non-Map vars values are ignored
//   - Non-String property values in vars are ignored
//   - Already registered types are not re-registered (Has check)
func ApplySoongConfigModuleTypes(modules map[string]*parser.Module, getStringProp func(*parser.Module, string) string, eval *parser.Evaluator) {
	// Iterate through all modules, looking for soong_config_module_type.
	// This allows custom configuration namespaces to be defined.
	for _, ct := range modules {
		// Skip non-configuration modules.
		if ct.Type != "soong_config_module_type" {
			continue
		}
		// Extract required properties for configuration.
		// module_type: the base module type to extend
		// config_namespace: namespace for configuration variables
		// name: the type name for the alias
		baseType := getStringProp(ct, "module_type")
		ns := getStringProp(ct, "config_namespace")
		typeName := getStringProp(ct, "name")
		// Skip if required properties are missing.
		if baseType == "" || typeName == "" {
			continue
		}
		// Process vars property to set configuration values.
		// The vars property is a Map of configuration variables.
		if ct.Map != nil {
			for _, prop := range ct.Map.Properties {
				if prop.Name == "vars" {
					// Extract string values from the vars Map.
					// Each property becomes a config key-value pair.
					if mp, ok := prop.Value.(*parser.Map); ok {
						for _, p := range mp.Properties {
							if s, ok := p.Value.(*parser.String); ok {
								// Format: "namespace.variable_name"
								key := fmt.Sprintf("%s.%s", ns, p.Name)
								eval.SetConfig(key, s.Value)
							}
						}
					}
				}
			}
		}
		// Register alias from config type name to base type.
		// Only register if not already registered to avoid conflicts.
		if !module.Has(typeName) {
			module.RegisterAlias(typeName, baseType)
		}
	}
}
