// Package variant provides architecture and platform variant handling for Blueprint modules.
// It handles merging variant-specific properties (architecture, host/device, multilib),
// determining module enablement based on target type, and property merging logic.
// This enables single Blueprint definitions to generate builds for multiple architectures
// and platforms (e.g., arm64, arm, x86, x86_64, host, device).
package variant

import "minibp/lib/parser"

// MergeVariantProps merges variant-specific properties into a module based on the target architecture,
// host/device configuration, and multilib settings. This function applies architecture-specific properties
// from m.Arch, host-specific properties from m.Host, target-specific properties from m.Target, and
// handles multilib ABI variants (lib32/lib64). Properties are merged with the base module properties,
// with list properties being concatenated and other properties being overridden.
//
// The merge order is important as later merges override earlier ones:
//  1. Base properties from m.Map (applied first, lowest priority)
//  2. Architecture properties from m.Arch[arch] (override or add to base)
//  3. Host or Target properties (applied based on host bool)
//  4. Multilib properties (highest priority for matching ABI)
//
// Parameters:
//   - m: The module to merge properties into
//   - arch: Target architecture (e.g., "x86", "x86_64", "arm", "arm64")
//   - host: Whether this is a host build (true) or device build (false)
//   - eval: The evaluator for resolving property values (used for future expansion)
//
// Edge cases:
//   - Empty arch string skips architecture-specific merging
//   - Nil Arch map skips architecture merging
//   - Nil Host map skips host-specific merging
//   - Nil Target map skips target-specific merging
//   - Empty Multilib map skips multilib merging
//   - Non-matching multilib ABIs are ignored
func MergeVariantProps(m *parser.Module, arch string, host bool, eval *parser.Evaluator) {
	// Merge architecture-specific properties.
	// These properties apply only to the target architecture.
	// Empty arch or nil Arch map skips this step.
	if arch != "" && m.Arch != nil {
		MergeMapProps(m, m.Arch[arch])
	}
	// Merge host-specific properties for host builds.
	// Host properties apply when building for host (e.g., Linux, macOS).
	if host && m.Host != nil {
		MergeMapProps(m, m.Host)
	}
	// Merge target-specific properties for device builds.
	// Target properties apply when building for device (e.g., Android).
	if !host && m.Target != nil {
		MergeMapProps(m, m.Target)
	}
	// Merge multilib properties for ABI variants.
	// This handles lib32/lib64 variants.
	if len(m.Multilib) > 0 {
		mergeMultilib(m, arch)
	}
}

// mergeMultilib merges multilib-specific properties based on the target architecture.
// Multilib allows a module to define different properties for different ABIs (lib32, lib64).
// This function checks the module's Multilib map and applies properties matching the current architecture.
//
// The mapping logic is:
//   - "lib32" matches when arch is "x86" or "arm" (32-bit architectures)
//   - "lib64" matches when arch is "x86_64" or "arm64" (64-bit architectures)
//   - Any other ABI that exactly matches the architecture name
//
// Parameters:
//   - m: The module to merge multilib properties into
//   - arch: Target architecture string
//
// Edge cases:
//   - Empty Multilib map skips merging
//   - Non-matching ABIs are skipped (properties not applied)
//   - Multiple matching ABIs (unlikely) all get applied
func mergeMultilib(m *parser.Module, arch string) {
	// Iterate through all multilib ABI variants.
	// Each ABI maps to a different set of properties.
	for abi, mlMap := range m.Multilib {
		// Check if this ABI matches the target architecture.
		// Logic:
		//   - lib32 matches x86 or arm (32-bit)
		//   - lib64 matches x86_64 or arm64 (64-bit)
		//   - Direct match for other ABIs
		switch {
		case abi == "lib32" && (arch == "x86" || arch == "arm"):
			MergeMapProps(m, mlMap)
		case abi == "lib64" && (arch == "x86_64" || arch == "arm64"):
			MergeMapProps(m, mlMap)
		case abi == arch:
			MergeMapProps(m, mlMap)
		}
	}
}

// MergeMapProps merges properties from an override Map into a module's base Map.
// This implements the variant property merging logic where:
//   - List properties: Values are concatenated (added to existing list)
//   - Other properties: Values are overwritten (override replaces base)
//
// The function iterates through each property in the override map and either:
//   - For list types: Appends the new list values to any existing list property with the same name
//   - For other types: Overwrites the existing property value or adds the property if not found
//
// Parameters:
//   - m: The module whose base Map properties will be modified
//   - override: The Map containing properties to merge (may be nil, in which case no action is taken)
//
// Edge cases:
//   - Nil override Map returns early without modification
//   - Empty override Map returns early without modification
//   - Properties in override but not in base are added
//   - List properties are always concatenated, never replaced
//   - Properties are matched by exact name (case-sensitive)
func MergeMapProps(m *parser.Module, override *parser.Map) {
	// Early return for nil override.
	// No work needed if there's nothing to merge.
	if override == nil {
		return
	}
	// Iterate through each property in the override Map.
	// Determine merge strategy based on property type.
	for _, prop := range override.Properties {
		switch prop.Value.(type) {
		case *parser.List:
			// List properties are concatenated with existing values.
			// Find existing list property with same name.
			merged := false
			for _, baseProp := range m.Map.Properties {
				if baseProp.Name == prop.Name {
					// Both must be List type for concatenation.
					if baseList, ok := baseProp.Value.(*parser.List); ok {
						if archList, ok := prop.Value.(*parser.List); ok {
							// Append override values to base values.
							baseList.Values = append(baseList.Values, archList.Values...)
						}
					}
					merged = true
					break
				}
			}
			// Add as new property if no matching list found.
			if !merged {
				m.Map.Properties = append(m.Map.Properties, prop)
			}
		default:
			// Non-list properties are overwritten.
			// Find existing property with same name.
			found := false
			for i, baseProp := range m.Map.Properties {
				if baseProp.Name == prop.Name {
					// Replace the property value.
					m.Map.Properties[i].Value = prop.Value
					found = true
					break
				}
			}
			// Add as new property if not found.
			if !found {
				m.Map.Properties = append(m.Map.Properties, prop)
			}
		}
	}
}

// IsModuleEnabledForTarget determines whether a module should be built for the specified target type.
// This checks the module's host_supported and device_supported properties to decide if the module
// should be enabled for the current build configuration.
//
// A module is enabled if:
//   - Both host_supported and device_supported are unset (default to true)
//   - The build type matches an enabled support flag
//
// The logic follows these rules:
//   - If neither host_supported nor device_supported is set, module is enabled for both
//   - For host builds: check host_supported flag
//   - For device builds: check device_supported flag
//
// For example:
//   - If host_supported is true and hostBuild is true -> module is enabled
//   - If device_supported is true and hostBuild is false -> module is enabled
//   - If host_supported is false and hostBuild is true -> module is disabled
//   - If device_supported is false and hostBuild is false -> module is disabled
//
// Parameters:
//   - m: The module to check
//   - hostBuild: Whether this is a host build (true) or device build (false)
//
// Returns:
//   - true if the module should be built for the target, false otherwise
//
// Edge cases:
//   - Nil Map is treated as having no restrictions (enabled)
//   - Missing properties default to false (which means enabled when both unset)
func IsModuleEnabledForTarget(m *parser.Module, hostBuild bool) bool {
	// Get host_supported and device_supported properties.
	// Missing properties return false (treated as unset).
	hs := GetBoolProp(m, "host_supported")
	ds := GetBoolProp(m, "device_supported")
	// If both are unset (both false), module is enabled for all builds.
	// This is the default behavior when properties aren't specified.
	if !hs && !ds {
		return true
	}
	// For host builds, check host_supported.
	// For device builds, check device_supported.
	if hostBuild {
		return hs
	}
	return ds
}

// GetBoolProp retrieves a boolean property value from a module's Map properties.
// This function searches the module's properties for a property with the given name
// and returns its boolean value if found.
//
// Parameters:
//   - m: The module to get the property from
//   - name: The property name to search for
//
// Returns:
//   - The boolean value of the property if it exists and is a Bool type
//   - false if the property is not found or is not a Bool type
//
// Edge cases:
//   - Nil Map returns false (no properties to search)
//   - Missing property name returns false
//   - Non-Bool property type returns false (type assertion fails)
func GetBoolProp(m *parser.Module, name string) bool {
	// Early return for nil Map.
	// No properties to search if Map is nil.
	if m.Map == nil {
		return false
	}
	// Search through all properties for matching name.
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			// Check if property value is a Bool type.
			// Type assertion validates the property type.
			if b, ok := prop.Value.(*parser.Bool); ok {
				return b.Value
			}
		}
	}
	// Property not found or not a Bool type.
	return false
}
