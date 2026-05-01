// Package variant provides architecture and platform variant handling for Blueprint modules.
// It handles merging variant-pecific properties (architecture, host/device, multilib),
// determining module enablement based on target type, and property merging logic.
// This enables single Blueprint definitions to generate builds for multiple architectures
// and platforms (e.9., arm64, arm, x86, x86_64, host, device).
//
// Variant handling is essential for supporting cross-platform builds where the same
// source code needs to be compiled for different architectures and operating
// systems. This package provides the logic for:
//
// Architecture Variants:
//   - Different properties for arm, arm64, x86, x86_64, etc.
//   - Selected via m.Arch[arch] property Maps
//
// Host/Device Variants:
//   - m.Host for host-specific properties (Linux, macOS, Windows)
//   - m.Target for device-specific properties (Android)
//
// Multilib Variants:
//   - Different properties for 32-bit (lib32) and 64-bit (lib64) ABIs
//   - Selected based on architecture bitness
package variant

import "minibp/lib/parser"

// MergeVariantProps merges variant-specific properties into a module based on the target architecture,
// host/device configuration, and multilib settings.
//
// Description:
// This function applies architecture-specific properties from m.Arch, host-specific properties
// from m.Host, target-specific properties from m.Target, and handles multilib ABI variants (lib32/lib64).
// Properties are merged with the base module properties, with list properties being concatenated
// and other properties being overridden.
//
// How it works:
// The merge order is important as later merges override earlier ones:
//  1. Base properties from m.Map (applied first, lowest priority)
//  2. Architecture properties from m.Arch[arch] (override or add to base)
//  3. Host or Target properties (applied based on host bool)
//  4. Multilib properties (highest priority for matching ABI)
//
// This function is called for each module during build graph construction to prepare the final
// property set for the target configuration.
//
// Parameters:
//   - m: The module to merge properties into. Must have non-nil Map for merging to work.
//   - arch: Target architecture (e.9., "x86", "x86_64", "arm", "arm64")
//   - host: Whether this is a host build (true) or device build (false)
//   - eval: The evaluator for resolving property values (used for future expansion,
//     currently not used but reserved for expression evaluation)
//
// Returns:
//   - void: The module's Map is modified in place. No return value.
//
// Edge cases:
//   - Empty arch string skips architecture-specific merging
//   - Nil Arch map skips architecture merging
//   - Nil Host map skips host-specific merging
//   - Nil Target map skips target-specific merging
//   - Empty Multilib map skips multilib merging
//   - Non-matching multilib ABIs are ignored
func MergeVariantProps(m *parser.Module, arch string, host bool, eval *parser.Evaluator) {
	if arch != "" && m.Arch != nil {
		MergeMapProps(m, m.Arch[arch])
	}
	if host && m.Host != nil {
		MergeMapProps(m, m.Host)
	}
	if !host && m.Target != nil {
		MergeMapProps(m, m.Target)
	}
	if len(m.Multilib) > 0 {
		mergeMultilib(m, arch)
	}
}

// mergeMultilib merges multilib-specific properties based on the target architecture.
//
// Description:
// Multilib allows a module to define different properties for different ABIs (lib32, lib64).
// This function checks the module's Multilib map and applies properties matching the
// current architecture.
//
// How it works:
// The mapping logic is:
//   - "lib32" matches when arch is "x86" or "arm" (32-bit architectures)
//   - "lib64" matches when arch is "x86_64" or "arm64" (64-bit architectures)
//   - Any other ABI that exactly matches the architecture name
//
// Parameters:
//   - m: The module to merge multilib properties into
//   - arch: Target architecture string
//
// Returns:
//   - void: The module's Map is modified in place.
//
// Edge cases:
//   - Empty Multilib map skips merging
//   - Non-matching ABIs are skipped (properties not applied)
//   - Multiple matching ABIs (unlikely) all get applied
func mergeMultilib(m *parser.Module, arch string) {
	for abi, mlMap := range m.Multilib {
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
//
// Description:
// This implements the variant property merging logic where:
//   - List properties: Values are concatenated (added to existing list)
//   - Other properties: Values are overwritten (override replaces base)
//
// How it works:
// The function iterates through each property in the override map and either:
//   - For list types: Appends the new list values to any existing list property with the same name
//   - For other types: Overwrites the existing property value or adds the property if not found
//
// Parameters:
//   - m: The module whose base Map properties will be modified
//   - override: The Map containing properties to merge (may be nil, in which case no action is taken)
//
// Returns:
//   - void: The module's Map properties are modified in place.
//
// Edge cases:
//   - Nil override Map returns early without modification
//   - Empty override Map returns early without modification
//   - Properties in override but not in base are added
//   - List properties are always concatenated, never replaced
//   - Properties are matched by exact name (case-sensitive)
func MergeMapProps(m *parser.Module, override *parser.Map) {
	if override == nil {
		return
	}
	for _, prop := range override.Properties {
		switch prop.Value.(type) {
		case *parser.List:
			merged := false
			for _, baseProp := range m.Map.Properties {
				if baseProp.Name == prop.Name {
					if baseList, ok := baseProp.Value.(*parser.List); ok {
						if archList, ok := prop.Value.(*parser.List); ok {
							baseList.Values = append(baseList.Values, archList.Values...)
						}
					}
					merged = true
					break
				}
			}
			if !merged {
				m.Map.Properties = append(m.Map.Properties, prop)
			}
		default:
			found := false
			for i, baseProp := range m.Map.Properties {
				if baseProp.Name == prop.Name {
					m.Map.Properties[i].Value = prop.Value
					found = true
					break
				}
			}
			if !found {
				m.Map.Properties = append(m.Map.Properties, prop)
			}
		}
	}
}

// IsModuleEnabledForTarget determines whether a module should be built for the specified target type.
//
// Description:
// This checks the module's host_upported and device_upported properties to decide if the module
// should be enabled for the current build configuration.
//
// How it works:
// A module is enabled if:
//   - Both host_upported and device_upported are unset (default to true)
//   - The build type matches an enabled support flag
//
// The logic follows these rules:
//   - If neither host_upported nor device_upported is set, module is enabled for both
//   - For host builds: check host_upported flag
//   - For device builds: check device_upported flag
//
// For example:
//   - If host_upported is true and hostBuild is true -> module is enabled
//   - If device_upported is true and hostBuild is false -> module is enabled
//   - If host_upported is false and hostBuild is true -> module is disabled
//   - If device_upported is false and hostBuild is false -> module is disabled
//
// Parameters:
//   - m: The module to check
//   - hostBuild: Whether this is a host build (true) or device build (false)
//
// Returns:
//   - bool: true if the module should be built for the target, false otherwise
//
// Edge cases:
//   - Nil Map is treated as having no restrictions (enabled)
//   - Missing properties default to false (which means enabled when both unset)
func IsModuleEnabledForTarget(m *parser.Module, hostBuild bool) bool {
	hs := GetBoolProp(m, "host_supported")
	ds := GetBoolProp(m, "device_supported")
	if !hs && !ds {
		return true
	}
	if hostBuild {
		return hs
	}
	return ds
}

// GetBoolProp retrieves a boolean property value from a module's Map properties.
//
// Description:
// This function searches the module's properties for a property with the given name
// and returns its boolean value if found.
//
// Parameters:
//   - m: The module to get the property from
//   - name: The property name to search for
//
// Returns:
//   - bool: The boolean value of the property if it exists and is a Bool type,
//     false if the property is not found or is not a Bool type
//
// Edge cases:
//   - Nil Map returns false (no properties to search)
//   - Missing property name returns false
//   - Non-Bool property type returns false (type assertion fails)
func GetBoolProp(m *parser.Module, name string) bool {
	if m.Map == nil {
		return false
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if b, ok := prop.Value.(*parser.Bool); ok {
				return b.Value
			}
		}
	}
	return false
}
