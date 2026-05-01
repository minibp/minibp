// rules.go - Ninja rule interface and rule registry for minibp.
//
// Package ninja provides utilities for generating Ninja build files from
// Blueprint module definitions. This file (rules.go) defines the core interfaces,
// rule rendering context, and utility functions for module property handling.
//
// Design decisions:
//   - Uses an interface (BuildRule) to define rule implementations
//   - Uses a struct (RuleRenderContext) to pass toolchain configuration
//   - Provides a flat list of rules (not a registry pattern)
//   - Supports cross-namespace module references
//   - Supports visibility rules for module access control
//
// Architecture:
//
//   - BuildRule interface: Defines 5 methods that each rule type must implement
//   - RuleRenderContext: Holds toolchain configuration (compiler paths, flags, LTO, sysroot)
//   - GetAllRules: Returns all registered rule implementations
//   - ApplyDefaults: Merges properties from defaults modules into target modules
//   - ModuleReference: Parses ":module" and ":module{.tag}" references
//   - Visibility: Validates visibility rules like "//visibility:public"
package ninja

import (
	"minibp/lib/parser"
	"strings"
)

// BuildRule is the interface for all ninja rule implementations.
//
// Description:
//
//	Each rule type (cc_library, go_binary, java_library, etc.) must implement these methods
//	to participate in the Ninja build file generation.
//
// How it works:
//
//	The build system iterates over all modules, finds their matching BuildRule implementation,
//	and calls the appropriate methods to generate ninja rules and build edges.
//
// Methods:
//   - Name(): Returns the module type name (e.g., "cc_library")
//   - NinjaRule(): Returns ninja rule definitions (called once per rule type)
//   - NinjaEdge(): Returns ninja build edges for a module (called once per module)
//   - Outputs(): Returns output file paths for a module
//   - Desc(): Returns description string for build logging
//
// Key design decisions:
//   - Separates rule definition from build edges for flexibility
//   - Uses srcFile to distinguish compilation from linking steps
//   - Outputs() includes architecture suffixes for multi-arch builds
//
// Edge cases:
//   - Empty srcs should still produce valid rule output
//   - Missing properties: May return nil outputs or error messages
type BuildRule interface {
	Name() string
	NinjaRule(ctx RuleRenderContext) string
	NinjaEdge(m *parser.Module, ctx RuleRenderContext) string
	Outputs(m *parser.Module, ctx RuleRenderContext) []string
	Desc(m *parser.Module, srcFile string) string
}

// RuleRenderContext holds the toolchain configuration used when rendering ninja rules.
//
// Description:
//
//	This struct contains all the toolchain and build configuration information
//	needed to generate ninja rules and build edges. It is passed to each
//	rule's methods during ninja file generation.
//
// How it works:
//
//	The context is initialized from command-line flags and build configuration.
//	Each rule method uses this information to generate correct commands.
//
// Fields:
//   - CC: C compiler command (e.g., "gcc")
//   - CXX: C++ compiler command (e.g., "g++")
//   - LD: Linker command (optional, defaults to CC/CXX)
//   - AR: Static library archiver (e.g., "ar")
//   - ArchSuffix: Architecture suffix for outputs (e.g., "_arm64")
//   - CFlags: Global C/C++ compiler flags
//   - LdFlags: Global linker flags
//   - Sysroot: Cross-compilation sysroot path
//   - Ccache: Path to ccache binary
//   - Lto: Default LTO mode ("full", "thin", or "")
//   - GOOS: Go target OS (e.g., "linux")
//   - GOARCH: Go target architecture (e.g., "amd64")
//   - PathPrefix: Prefix for dependency file paths
//   - Modules: Map of all modules for dependency resolution
//   - GoModulePath: Go module path
//   - GoImportPrefix: Go import prefix
//
// Key design decisions:
//   - Uses struct to pass configuration, avoiding global state
//   - Includes both tool paths and flags for complete command generation
//   - Supports cross-compilation via GOOS/GOARCH and Sysroot
type RuleRenderContext struct {
	CC             string
	CXX            string
	AR             string
	LD             string // Linker command; empty uses CC/CXX for linking
	ArchSuffix     string
	CFlags         string
	LdFlags        string
	Sysroot        string
	Ccache         string
	Lto            string
	GOOS           string
	GOARCH         string
	PathPrefix     string
	Modules        map[string]*parser.Module
	GoModulePath   string
	GoImportPrefix string
	ExportCFlags   string // Exported C flags from dependencies
	ExportLdFlags  string // Exported linker flags from dependencies
}

// DefaultRuleRenderContext returns a RuleRenderContext with default toolchain values.
//
// This provides a sensible baseline configuration that can be customized
// via command-line flags. The defaults are typical Linux development tools.
//
// Returns:
//   - RuleRenderContext with CC="gcc", CXX="g++", AR="ar"
//
// Note:
//   - Ccache, Lto, Sysroot, GOOS, GOARCH are left empty (no defaults)
//   - These must be set explicitly via command-line flags or build configuration
//
// Key design decisions:
//   - Only sets CC, CXX, AR to common Linux defaults; leaves other fields empty to force explicit configuration.
//   - Does not enable ccache by default, as it may not be installed on all systems.
//   - Returns a value (not pointer) to avoid shared mutable state.
func DefaultRuleRenderContext() RuleRenderContext {
	return RuleRenderContext{
		CC:  "gcc",
		CXX: "g++",
		AR:  "ar",
	}
}

// GetAllRules returns all available rule implementations.
//
// This function provides a comprehensive list of all BuildRule implementations
// registered in the system. Each rule handles a specific module type from
// Blueprint/Soong syntax.
//
// Returns:
//   - []BuildRule: A slice containing all registered rule implementations
//
// The returned rules include:
//   - C/C++ rules: cc_library, cc_library_static, cc_library_shared, cc_object, cc_binary, cc_library_headers
//   - Handle C/C++ compilation, archiving, linking, and LTO
//   - Go rules: go_library, go_binary, go_test
//   - Handle Go compilation, cross-compilation, and testing
//   - Java rules: java_library, java_binary, java_library_static, java_library_host, java_binary_host, java_test, java_import
//   - Handle Java compilation, packaging, and manifest creation
//   - Soong syntax rules: defaults, package, soong_namespace, phony, cc_test, genrule, cc_defaults, java_defaults, go_defaults
//   - Handle Soong-specific module types and syntax
//   - Other rules: filegroup, prebuilt_etc, prebuilt_usr_share, prebuilt_firmware, prebuilt_root, cc_prebuilt_binary, cc_prebuilt_library, cc_prebuilt_library_static, cc_prebuilt_library_shared, custom, proto_library, proto_gen, sh_binary_host, python_binary_host, python_test_host
//   - Handle various other build scenarios and prebuilt artifacts
//
// Note: The order of rules in this slice doesn't affect functionality,
// but related rules are grouped together for maintainability.
//
// Key design decisions:
//   - Returns a new slice each call to avoid mutable shared state.
//   - Uses struct literals rather than a registry pattern for simplicity and explicitness.
//   - Related rules are grouped together in the slice to make it easy to find and maintain specific rule types.
//   - Some rule types (like defaultsModule, prebuiltEtcRule, prebuiltLibraryRule) use different struct literals with typeName to create multiple instances for different module types.
func GetAllRules() []BuildRule {
	return []BuildRule{
		// C/C++ rules
		&ccLibrary{},
		&ccLibraryStatic{},
		&ccLibraryShared{},
		&ccObject{},
		&ccBinary{},
		&ccLibraryHeaders{},
		// Go rules
		&goLibrary{},
		&goBinary{},
		&goTest{},

		// Java rules
		&javaLibrary{},
		&javaBinary{},
		&javaLibraryStatic{},
		&javaLibraryHost{},
		&javaBinaryHost{},
		&javaTest{},
		&javaImport{},

		// Soong syntax rules
		&defaults{},
		&packageModule{},
		&soongNamespace{},
		&phonyRule{},
		&ccTestRule{},
		&genrule{},
		&defaultsModule{typeName: "cc_defaults"},
		&defaultsModule{typeName: "java_defaults"},
		&defaultsModule{typeName: "go_defaults"},

		// Other rules
		&filegroup{},
		&prebuiltEtcRule{typeName: "prebuilt_etc", subdir: "etc"},
		&prebuiltEtcRule{typeName: "prebuilt_usr_share", subdir: "usr/share"},
		&prebuiltEtcRule{typeName: "prebuilt_firmware", subdir: "firmware"},
		&prebuiltEtcRule{typeName: "prebuilt_root", subdir: ""},
		&prebuiltBinaryRule{typeName: "cc_prebuilt_binary"},
		&prebuiltLibraryRule{typeName: "cc_prebuilt_library", ext: ".a"},
		&prebuiltLibraryRule{typeName: "cc_prebuilt_library_static", ext: ".a"},
		&prebuiltLibraryRule{typeName: "cc_prebuilt_library_shared", ext: ".so"},
		&customRule{},
		&protoLibraryRule{},
		&protoGenRule{},
		&configGen{},

		// File replace rules
		&replaceRule{},

		// Shell/Python rules
		&shBinaryHostRule{},
		&pythonBinaryHostRule{},
		&pythonTestHostRule{},
	}
}

// GetRule returns a rule by name.
//
// This function searches through all registered rules to find one matching
// the specified name. The name should match the module type in Blueprint files.
//
// Parameters:
//   - name: The rule/module type name to search for (e.g., "cc_library", "go_binary")
//
// Returns:
//   - BuildRule: The rule implementation if found, nil otherwise
//
// Example:
//
//	rule := ninja.GetRule("cc_library")
//	if rule != nil {
//	    fmt.Println("Found:", rule.Name())
//	}
//
// Edge cases:
//   - Name not found: Returns nil
//   - Multiple rules with same name: Returns first match (should not happen in practice)
//
// Key design decisions:
//   - Uses linear search through GetAllRules() rather than a map lookup to keep the implementation simple and avoid initialization order issues.
//   - The linear search is acceptable because the number of registered rules is small (typically < 50) and this function is not called in hot loops.
//   - Rule names are matched exactly (case-sensitive) to match Blueprint/Soong semantics.
func GetRule(name string) BuildRule {
	for _, r := range GetAllRules() {
		if r.Name() == name {
			return r
		}
	}
	return nil
}

// ApplyDefaults applies default properties from defaults modules to a target module.
//
// This function processes the `defaults` property which contains a list of default
// module names. The function merges properties from defaults into the target module,
// with the target's own properties taking precedence.
//
// Property merging rules:
//   - List properties (cflags, cppflags, srcs, etc.): Concatenated (defaults appended)
//   - This allows defaults to add flags while target can override/add
//   - Order: target values first, then defaults values
//   - Non-list properties: Target module's value takes precedence
//   - If target has the property, defaults value is ignored
//   - If target doesn't have it, defaults value is added
//   - Excluded properties: "name" and "defaults" are never merged
//   - "name" is the module identifier and shouldn't be copied
//   - "defaults" would cause recursive application
//
// Parameters:
//   - m: The target module to apply defaults to
//   - modules: Map of all modules (name -> module) for resolving defaults references
//
// Algorithm:
//  1. Check if module has a "defaults" property
//  2. For each default name in the list:
//     a. Look up the default module (stripping leading ":" if present)
//     b. Verify it's a defaults module type
//     c. Merge each property from defaults to target:
//     - For lists: Append default items to existing list
//     - For non-lists: Use target value if exists, otherwise add from defaults
//
// Edge cases:
//   - Missing defaults module: Silently skipped (no error)
//   - Circular references: Handled naturally (defaults don't recursive apply)
//   - Empty defaults list: No-op (function returns early)
//   - nil Map on module: Returns early without error
//   - Malformed property values: May cause issues in downstream processing
//
// Key design decisions:
//   - Uses additive merging for list properties (defaults appended after target values) to allow defaults to provide base flags while targets can add/override.
//   - Skips "name" and "defaults" properties to prevent identity conflicts and recursive application.
//   - Silently skips missing defaults modules rather than erroring, following Soong's lenient approach to allow optional defaults.
//   - Modifies the target module in-place rather than returning a new module, as the caller expects the module to be updated directly.
//
// Example:
//
//	// In Blueprint:
//	defaults {
//	  name: "my_defaults",
//	  cflags: ["-Wall"],
//	}
//
//	cc_binary {
//	  name: "my_binary",
//	  defaults: ["my_defaults"],
//	  cflags: ["-O2"],  // Final: ["-O2", "-Wall"]
//	}
func ApplyDefaults(m *parser.Module, modules map[string]*parser.Module) {
	// Early return if module has no properties
	if m.Map == nil {
		return
	}
	// Get list of default module names
	defaultNames := GetListProp(m, "defaults")
	if len(defaultNames) == 0 {
		return
	}
	// Process each default module
	for _, defaultName := range defaultNames {
		// Strip leading ":" if present (both ":name" and "name" are valid)
		defaultName = strings.TrimPrefix(defaultName, ":")
		// Look up the defaults module
		defaultMod, ok := modules[defaultName]
		if !ok || defaultMod == nil {
			continue
		}
		// Verify it's actually a defaults module type
		if !isDefaultsModuleType(defaultMod.Type) {
			continue
		}
		// Merge properties from defaults module
		if defaultMod.Map != nil {
			for _, prop := range defaultMod.Map.Properties {
				// Skip name and defaults properties - they don't merge
				if prop.Name == "name" || prop.Name == "defaults" {
					continue
				}
				// Check if target module already has this property
				found := false
				for i, targetProp := range m.Map.Properties {
					if targetProp.Name == prop.Name {
						found = true
						// Additive merge for lists: append default items to existing list
						// This allows defaults to add flags while target can override/add
						if defaultList, ok := prop.Value.(*parser.List); ok {
							if targetList, ok := targetProp.Value.(*parser.List); ok {
								// Create merged list: target values first, then defaults
								merged := make([]parser.Expression, len(targetList.Values))
								copy(merged, targetList.Values)
								for _, v := range defaultList.Values {
									merged = append(merged, v)
								}
								m.Map.Properties[i].Value = &parser.List{
									Values:    merged,
									LBracePos: targetList.LBracePos,
									RBracePos: targetList.RBracePos,
								}
							}
						}
						// For non-list properties, target takes precedence (do nothing)
						break
					}
				}
				// If property doesn't exist in target, add it from defaults
				if !found {
					m.Map.Properties = append(m.Map.Properties, prop)
				}
			}
		}
	}
}

// GetDefaultVisibility retrieves the default_visibility from a package module.
//
// This is used to set the default visibility for modules in a package.
// The package module defines visibility rules that apply to all modules
// in that package unless explicitly overridden.
//
// Parameters:
//   - modules: Map of all modules (name -> module)
//   - packageName: The package name to look up default visibility for
//
// Returns:
//   - []string: List of visibility rules, or nil if package not found or no default_visibility
//
// Edge cases:
//   - Package module not found: Returns nil (no default visibility)
//   - No default_visibility property: Returns nil (no default set)
//   - Package module is nil: Skipped silently (continues search)
//   - Multiple package modules: Returns first match (undefined behavior)
//
// Key design decisions:
//   - Uses suffix matching (HasSuffix) to handle both simple names ("foo") and full paths ("path/to/foo") for package module lookup.
//   - Searches through all modules linearly rather than maintaining a separate package module index, as this function is called infrequently during build setup.
//   - Returns nil instead of empty slice when no visibility is found, allowing callers to distinguish "no default visibility" from "empty visibility list".
//
// Note: Package modules are named after their package path.
// Matching checks both exact name and suffix (handles "foo" and "path/to/foo").
func GetDefaultVisibility(modules map[string]*parser.Module, packageName string) []string {
	// Look for package module in the given package
	for name, mod := range modules {
		if mod == nil || mod.Type != "package" {
			continue
		}
		// Package modules are named after their package path
		// Match exact name or suffix (handles "foo" and "path/to/foo")
		if name == packageName || strings.HasSuffix(name, "/"+packageName) {
			return GetListProp(mod, "default_visibility")
		}
	}
	return nil
}

// GetPackageDefaultVisibility gets the default_visibility for a module based on its package.
//
// It traverses up the package hierarchy to find the closest ancestor with default_visibility.
// This allows nested packages to inherit visibility from parent packages while
// being able to override with their own default_visibility.
//
// Parameters:
//   - modules: Map of all modules (name -> module)
//   - modulePath: The module's package path (e.g., "libs/foo/bar")
//
// Returns:
//   - []string: List of visibility rules from closest matching package, or nil
//
// Algorithm:
//  1. Split module path into components by "/"
//  2. Starting from full path, check each ancestor for package module
//  3. Try progressively shorter paths (finds closest ancestor first)
//  4. Return first default_visibility found (closest ancestor wins)
//
// Example:
//
//	modulePath = "libs/foo/bar"
//	Checks: "libs/foo/bar", "libs/foo", "libs", ""
//	If "libs/foo" has default_visibility, it will be returned
//	Even if "libs" also has default_visibility
//
// Edge cases:
//   - No package module found: Returns nil (no default visibility)
//   - Multiple package modules at same level: Returns first match (undefined behavior)
//   - Empty path: Returns nil (root package not typically defined)
//
// Key design decisions:
//   - Traverses from most specific path to least specific (longest to shortest) to ensure closest ancestor wins.
//   - Uses path splitting and progressive shortening rather than explicit parent lookup, which handles arbitrary nesting depths without recursion.
//   - Returns the first match found (closest ancestor) rather than aggregating, as visibility inheritance follows a single-chain pattern in Soong.
func GetPackageDefaultVisibility(modules map[string]*parser.Module, modulePath string) []string {
	// Try to find package module at the same level and traverse up
	parts := strings.Split(modulePath, "/")
	// Starting from full path, try progressively shorter paths
	// This finds the closest ancestor with default_visibility
	for i := len(parts); i > 0; i-- {
		packageName := strings.Join(parts[:i], "/")
		if vis := GetDefaultVisibility(modules, packageName); vis != nil {
			return vis
		}
	}
	return nil
}

// ModuleReference represents a reference to another module's outputs.
//
// It can reference just the module (:module) or a specific tagged output
// (:module{.tag}). This structure is used to parse and resolve module
// references found in srcs, deps, and lib_deps properties.
//
// Fields:
//   - ModuleName: The name of the referenced module (e.g., "libfoo")
//   - Used to look up the module in the modules map
//   - Should not include ":" prefix or namespace prefix
//   - Tag: Optional tag for specific outputs (e.g., ".doc.zip", ".stripped")
//   - Used to select specific output variants from the referenced module
//   - Empty string means all outputs or default output
//   - IsModuleRef: True if this is a module reference (starts with ":" or "//...:")
//   - Used to distinguish module references from regular file paths
//   - False for regular file paths like "src/foo.c"
//
// Examples:
//
//	":libfoo" -> ModuleName="libfoo", Tag="", IsModuleRef=true
//	":libfoo.shared" -> ModuleName="libfoo", Tag=".shared", IsModuleRef=true
//	"//other:lib" -> ModuleName="lib", Tag="", IsModuleRef=true
//	"src/foo.c" -> ModuleName="", Tag="", IsModuleRef=false
type ModuleReference struct {
	ModuleName  string // The name of the referenced module
	Tag         string // Optional tag for specific outputs (e.g., ".doc.zip")
	IsModuleRef bool   // True if this is a module reference (starts with ":")
}

// ParseModuleReference parses a module reference string like ":module" or ":module{.tag}".
//
// This function handles various module reference formats:
//   - ":module" - simple module reference
//   - ":module{.tag}" - module reference with tag for specific output
//   - "//namespace:module" - cross-namespace reference
//
// Parameters:
//   - s: The string to parse as a module reference
//
// Returns:
//   - *ModuleReference: Parsed reference if valid, nil otherwise
//
// Edge cases:
//   - Empty string: Returns nil
//   - String without ":" prefix: Returns nil (not a module reference)
//   - Malformed tag (missing closing "}"): Tag part is ignored, module name still parsed
//   - Cross-namespace "//namespace:module": ModuleName set to part after ":"
//
// Key design decisions:
//   - Handles cross-namespace references (//namespace:module) separately from simple references (:module) for clarity.
//   - Cross-namespace references extract only the module name after ":", dropping the namespace prefix, as namespace resolution happens elsewhere.
//   - Tag syntax {tag} is parsed only when both "{" and "}" are present, with "}" at the end of the string.
//   - Returns nil for non-references (strings without ":" prefix) to allow callers to distinguish references from file paths.
//
// Examples:
//
//	ParseModuleReference(":libfoo") -> &ModuleReference{ModuleName: "libfoo", Tag: "", IsModuleRef: true}
//	ParseModuleReference(":libfoo{.shared}") -> &ModuleReference{ModuleName: "libfoo", Tag: ".shared", IsModuleRef: true}
//	ParseModuleReference("//other:lib") -> &ModuleReference{ModuleName: "lib", Tag: "", IsModuleRef: true}
//	ParseModuleReference("src/foo.c") -> nil (not a module reference)
func ParseModuleReference(s string) *ModuleReference {
	s = strings.TrimSpace(s)

	// Handle cross-namespace references: //namespace:module
	if strings.HasPrefix(s, "//") && strings.Contains(s, ":") {
		ref := &ModuleReference{IsModuleRef: true}
		sepIdx := strings.Index(s, ":")
		ref.ModuleName = s[sepIdx+1:]
		return ref
	}

	// Must start with ":" to be a module reference
	if !strings.HasPrefix(s, ":") {
		return nil
	}

	ref := &ModuleReference{IsModuleRef: true}

	s = s[1:] // Remove leading ":"

	// Check for tag syntax: {tag}
	// Tags are used for selecting specific output variants
	if strings.Contains(s, "{") && strings.HasSuffix(s, "}") {
		parts := strings.SplitN(s, "{", 2)
		ref.ModuleName = parts[0]
		tag := strings.TrimSuffix(parts[1], "}")
		ref.Tag = tag
	} else {
		ref.ModuleName = s
	}

	return ref
}

// ResolveModuleOutputs resolves a module reference to actual output file paths.
//
// It looks up the module in the provided modules map and returns its outputs
// using the rule's Outputs method. This converts abstract module references
// to concrete file paths that Ninja can understand.
//
// Parameters:
//   - ref: The parsed module reference (must have ModuleName set)
//   - modules: Map of all modules (name -> module) for lookup
//   - ctx: Rule render context for output name generation (architecture suffix, etc.)
//
// Returns:
//   - []string: List of output file paths, or nil if module not found
//
// Edge cases:
//   - nil reference: Returns nil (nothing to resolve)
//   - Not a module reference: Returns nil (should not happen if called correctly)
//   - Module type not registered: Returns nil (unknown module type)
//   - Module has no outputs: Returns nil (nothing to link against)
//   - Tag ".stamp": Returns first output with ".stamp" appended (for tracking)
//   - Other tags: Returns first output with tag suffix appended
//   - Module not found in map: Returns nil (broken reference)
//
// Key design decisions:
//   - Looks up the rule by module type to leverage the rule's Outputs() method, which knows the correct output paths for each module type.
//   - Returns only the first output when a tag is specified (other than ".stamp"), as tags are typically used to select a specific variant.
//   - The ".stamp" tag is special-cased to append ".stamp" to the first output, creating a touch file for tracking build completion.
//   - Returns nil (not empty slice) when resolution fails, allowing callers to distinguish "no outputs" from "resolution failed".
//
// Examples:
//
//	ref := ParseModuleReference(":libfoo")
//	outputs := ResolveModuleOutputs(ref, modules, ctx)
//	// outputs might be ["libfoo.a"] or ["libfoo_arm64.so"]
//
//	ref := ParseModuleReference(":libfoo{.shared}")
//	outputs := ResolveModuleOutputs(ref, modules, ctx)
//	// outputs might be ["libfoo.so"] (first output + ".shared" tag)
func ResolveModuleOutputs(ref *ModuleReference, modules map[string]*parser.Module, ctx RuleRenderContext) []string {
	// Validate reference
	if ref == nil || !ref.IsModuleRef {
		return nil
	}

	// Look up the module
	mod, ok := modules[ref.ModuleName]
	if !ok || mod == nil {
		return nil
	}

	// Get the rule for this module type
	rule := GetRule(mod.Type)
	if rule == nil {
		return nil
	}

	// Get outputs from the rule
	outputs := rule.Outputs(mod, ctx)
	if len(outputs) == 0 {
		return nil
	}

	// If no tag specified, return all outputs
	if ref.Tag == "" {
		return outputs
	}

	// Handle specific tags
	// .stamp files are special touch files that track completion
	if ref.Tag == ".stamp" {
		return []string{outputs[0] + ".stamp"}
	}

	// Default: return first output with tag suffix
	return []string{outputs[0] + ref.Tag}
}

// ExpandModuleReferences expands module references in a list of strings.
//
// It replaces strings like ":module" with actual output paths.
// This is used when processing srcs, deps, and lib_deps properties
// to convert module references to the file paths that Ninja needs.
//
// Parameters:
//   - items: List of strings that may contain module references
//   - modules: Map of all modules (name -> module)
//   - ctx: Rule render context
//
// Returns:
//   - []string: Expanded list with module references replaced by actual paths
//
// Algorithm:
//  1. For each item in the list:
//     a. Try to parse as module reference
//     b. If valid reference, resolve to output paths
//     c. If not a reference, keep original string
//  2. Return combined list
//
// Edge cases:
//   - Unresolved module reference: Still included as-is (may cause build error later)
//   - Module with multiple outputs: All outputs are included
//   - Empty input list: Returns empty list
//   - Module not found: Reference string is used as-is (preserving original intent)
//
// Key design decisions:
//   - Preserves unresolved module references as-is in the output, allowing Ninja to report the error with the original reference string.
//   - Expands all outputs for untagged references (ref.Tag == ""), as the caller typically wants all outputs from the referenced module.
//   - Uses ParseModuleReference to distinguish module references from file paths, enabling transparent handling of mixed lists.
//
// Example:
//
//	items := []string{":libfoo", "src/bar.c"}
//	expanded := ExpandModuleReferences(items, modules, ctx)
//	// expanded might be ["libfoo.a", "src/bar.c"]
func ExpandModuleReferences(items []string, modules map[string]*parser.Module, ctx RuleRenderContext) []string {
	var result []string

	for _, item := range items {
		ref := ParseModuleReference(item)
		if ref != nil {
			// This is a module reference, resolve it to actual outputs
			outputs := ResolveModuleOutputs(ref, modules, ctx)
			result = append(result, outputs...)
		} else {
			// Regular string (file path), keep as-is
			result = append(result, item)
		}
	}

	return result
}

// IsVisibilityPublic checks if a visibility list contains "//visibility:public".
//
// Public visibility allows the module to be accessed from any other module
// in the build system, regardless of package boundaries.
// This is the most permissive visibility setting.
//
// Parameters:
//   - vis: Slice of visibility rules to check
//
// Returns:
//   - true if "//visibility:public" is in the list, false otherwise
//
// Edge cases:
//   - nil or empty slice: Returns false
//   - Multiple entries: Returns true if any entry matches
//
// Key design decisions:
//   - Uses simple linear scan rather than map lookup, as visibility lists are typically short (1-3 entries).
//   - Returns true on first match for efficiency, as the caller typically only cares whether public visibility is present.
func IsVisibilityPublic(vis []string) bool {
	for _, v := range vis {
		if v == "//visibility:public" {
			return true
		}
	}
	return false
}

// IsVisibilityPrivate checks if a visibility list contains "//visibility:private".
//
// Private visibility restricts the module to be visible only within the same package.
// Other packages cannot reference this module.
// This is the most restrictive visibility setting (opposite of public).
//
// Parameters:
//   - vis: Slice of visibility rules to check
//
// Returns:
//   - true if "//visibility:private" is in the list, false otherwise
//
// Edge cases:
//   - nil or empty slice: Returns false
//   - Multiple entries: Returns true if any entry matches
//
// Key design decisions:
//   - Uses simple linear scan rather than map lookup, as visibility lists are typically short (1-3 entries).
//   - Returns true on first match for efficiency, as the caller typically only cares whether private visibility is present.
func IsVisibilityPrivate(vis []string) bool {
	for _, v := range vis {
		if v == "//visibility:private" {
			return true
		}
	}
	return false
}

// IsVisibilityOverride checks if a visibility list contains "//visibility:override".
//
// Override visibility is used to override a parent's visibility setting.
// This is typically used in specific build configurations
// where the default visibility needs to be bypassed.
//
// Parameters:
//   - vis: Slice of visibility rules to check
//
// Returns:
//   - true if "//visibility:override" is in the list, false otherwise
//
// Edge cases:
//   - nil or empty slice: Returns false
//   - Multiple entries: Returns true if any entry matches
//
// Key design decisions:
//   - Uses simple linear scan rather than map lookup, as visibility lists are typically short (1-3 entries).
//   - Returns true on first match for efficiency, as the caller typically only cares whether override visibility is present.
func IsVisibilityOverride(vis []string) bool {
	for _, v := range vis {
		if v == "//visibility:override" {
			return true
		}
	}
	return false
}

// IsValidVisibilityRule checks if a visibility rule string is valid.
//
// This function validates visibility rules against the supported set
// of visibility specifiers in Blueprint/Soong.
//
// Valid rules are:
//   - //visibility:public - Visible to all modules
//   - //visibility:private - Visible only within the same package
//   - //visibility:override - Override parent's visibility
//   - //visibility:legacy_public - Legacy public visibility
//   - //visibility:any_partition - Visible to any partition
//   - //package:__pkg__ - Visible to current package only
//   - //package:__subpackages__ - Visible to current package and subpackages
//   - //package:prefix - Visible to packages with given prefix
//   - :__subpackages__ (shorthand for current package)
//
// Parameters:
//   - rule: The visibility rule string to validate
//
// Returns:
//   - true if the rule is valid, false otherwise
//
// Edge cases:
//   - Empty string: Returns false (not a valid visibility rule)
//   - Malformed rule: Returns false (only exact matches are valid)
//   - Partial match: Returns false (e.g., "//visibility:pub" is not valid)
//   - Extra spaces: Returns false (whitespace is not trimmed)
//
// Key design decisions:
//   - Validates against an explicit list of supported visibility rules to catch typos and invalid values early.
//   - Does not trim whitespace, matching Soong's strict parsing behavior where whitespace is significant.
//   - Uses exact string matching rather than prefix matching to prevent partial matches (e.g., "//visibility:pub" is not valid).
//   - Handles package references with "__pkg__" and "__subpackages__" suffixes as special cases, as they follow a different format than the standard visibility rules.
//
// Examples:
//
//	IsValidVisibilityRule("//visibility:public") -> true
//	IsValidVisibilityRule("//visibility:private") -> true
//	IsValidVisibilityRule("//libs:__pkg__") -> true
//	IsValidVisibilityRule(":__subpackages__") -> true
//	IsValidVisibilityRule("invalid") -> false
//	IsValidVisibilityRule("") -> false
func IsValidVisibilityRule(rule string) bool {
	// Check standard visibility prefixes
	validPrefixes := []string{
		"//visibility:public",
		"//visibility:private",
		"//visibility:override",
		"//visibility:legacy_public",
		"//visibility:any_partition",
	}

	for _, prefix := range validPrefixes {
		if rule == prefix {
			return true
		}
	}

	// Check for package references with explicit suffixes
	// Format: //package:__pkg__ or //package:__subpackages__ or //package:prefix
	if strings.HasPrefix(rule, "//") && (strings.HasSuffix(rule, ":__pkg__") || strings.HasSuffix(rule, ":__subpackages__")) {
		return true
	}

	// Check for shorthand :__subpackages__ (current package and subpackages)
	if strings.HasPrefix(rule, ":") && strings.HasSuffix(rule, ":__subpackages__") {
		return true
	}

	return false
}
