// rules.go - Ninja rule interface and rule registry
//
// This file defines the BuildRule interface that all ninja rule implementations must satisfy,
// the RuleRenderContext for toolchain configuration, and utility functions for module
// references, visibility, and defaults processing.
//
// Architecture Overview:
//
//   - BuildRule interface: Defines 5 methods that each rule type must implement
//   - RuleRenderContext: Holds toolchain configuration (compiler paths, flags, LTO, sysroot)
//   - GetAllRules: Returns all registered rule implementations
//   - ApplyDefaults: Merges properties from defaults modules into target modules
//   - ModuleReference: Parses ":module" and ":module{.tag}" references
//   - Visibility: Validates visibility rules like "//visibility:public"
//
// Visibility rules supported:
//   - //visibility:public: Visible to all modules
//   - //visibility:private: Visible only within the same package
//   - //visibility:override: Override parent's visibility
//   - //visibility:legacy_public: Legacy public visibility
//   - //visibility:any_partition: Visible to any partition
//   - //package:__pkg__: Visible to current package
//   - //package:__subpackages__: Visible to current package and subpackages
package ninja

import (
	"minibp/lib/parser"
	"strings"
)

// BuildRule is the interface for all ninja rule implementations.
// Each rule type (cc_library, go_binary, java_library, etc.) must implement these methods
// to participate in the Ninja build file generation.
//
// Implementations are responsible for:
//   - Generating Ninja rule definitions (NinjaRule method)
//   - Creating build edges for modules (NinjaEdge method)
//   - Listing output files (Outputs method)
//   - Providing human-readable descriptions (Desc method)
//
// Method purpose:
//   - Name(): Unique identifier for this rule type (e.g., "cc_library", "go_binary")
//   - NinjaRule(): Returns ninja rule definitions as a string (multiple rules separated by newlines)
//   - NinjaEdge(): Returns ninja build edges for a specific module (what to build)
//   - Outputs(): Returns the output file paths produced by this rule
//   - Desc(): Returns a description string for build logging (e.g., "gcc", "jar", "go")
//
// Edge cases:
//   - Empty srcs should still produce valid rule output
//   - Missing required properties should produce error messages in Desc()
//   - Multiple architecture variants should be handled by Outputs()
type BuildRule interface {
	Name() string
	NinjaRule(ctx RuleRenderContext) string
	NinjaEdge(m *parser.Module, ctx RuleRenderContext) string
	Outputs(m *parser.Module, ctx RuleRenderContext) []string
	Desc(m *parser.Module, srcFile string) string
}

// RuleRenderContext holds the toolchain configuration used when rendering ninja rules.
// It is passed to each rule's methods to generate tool-specific commands and flags.
//
// This context is initialized from command-line flags (-cc, -cxx, -ar, -lto, -sysroot, -ccache)
// and build configuration. It provides the information needed to generate correct
// compiler invocations for each module.
//
// Fields:
//   - CC: C compiler command (e.g., "gcc", "clang", "/usr/bin/clang")
//   - CXX: C++ compiler command (e.g., "g++", "clang++", "/usr/bin/clang++")
//   - AR: Static library archiver (e.g., "ar", "gcc-ar", "llvm-ar")
//   - ArchSuffix: Architecture-specific suffix for outputs (e.g., "_arm64", "_x86_64")
//   - CFlags: Global C/C++ compiler flags (e.g., "-Wall -g")
//   - LdFlags: Global linker flags (e.g., "-lpthread")
//   - Sysroot: Cross-compilation sysroot path (e.g., "/opt/cross/arm64")
//   - Ccache: Path to ccache binary (empty if ccache is not available)
//   - Lto: Default LTO mode ("full", "thin", or "" for no LTO)
//   - GOOS: Go target operating system (e.g., "linux", "darwin", "windows")
//   - GOARCH: Go target architecture (e.g., "amd64", "arm64")
type RuleRenderContext struct {
	CC         string
	CXX        string
	AR         string
	ArchSuffix string
	CFlags     string
	LdFlags    string
	Sysroot    string
	Ccache     string
	Lto        string
	GOOS       string
	GOARCH     string
}

// DefaultRuleRenderContext returns a RuleRenderContext with default toolchain values.
//
// This provides a sensible baseline configuration that can be customized
// via command-line flags. The defaults are typical Linux development tools.
//
// Returns:
//   - RuleRenderContext with CC="gcc", CXX="g++", AR="ar"
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
//   - Go rules: go_library, go_binary, go_test
//   - Java rules: java_library, java_binary, java_library_static, java_library_host, java_binary_host, java_test, java_import
//   - Soong rules: defaults, package, soong_namespace, phony, cc_test, genrule, cc_defaults, java_defaults, go_defaults
//   - Other rules: filegroup, prebuilt_etc, prebuilt_usr_share, prebuilt_firmware, prebuilt_root, cc_prebuilt_binary, cc_prebuilt_library, cc_prebuilt_library_static, cc_prebuilt_library_shared, custom, proto_library, proto_gen, sh_binary_host, python_binary_host, python_test_host
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
//   - Non-list properties: Target module's value takes precedence
//   - Excluded properties: "name" and "defaults" are never merged
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
//   - Missing defaults module: Silently skipped
//   - Circular references: Handled naturally (defaults don't recursively apply)
//   - Empty defaults list: No-op
//   - nil Map on module: Returns early without error
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
//   - Package module not found: Returns nil
//   - No default_visibility property: Returns nil
//   - Package module is nil: Skipped silently
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
//  1. Split module path into components
//  2. Starting from full path, check each ancestor for package module
//  3. Return first default_visibility found (closest ancestor wins)
//
// Example:
//
//	modulePath = "libs/foo/bar"
//	Checks: "libs/foo/bar", "libs/foo", "libs", ""
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
//   - ModuleName: The name of the referenced module
//   - Tag: Optional tag for specific outputs (e.g., ".doc.zip", ".stripped")
//   - IsModuleRef: True if this is a module reference (starts with ":" or "//...:")
//
// Examples:
//
//	":libfoo" -> ModuleName="libfoo", Tag="", IsModuleRef=true
//	":libfoo.shared" -> ModuleName="libfoo", Tag=".shared", IsModuleRef=true
//	"//other:lib" -> ModuleName="lib", Tag="", IsModuleRef=true
type ModuleReference struct {
	ModuleName  string // The name of the referenced module
	Tag         string // Optional tag for specific outputs (e.g., ".doc.zip")
	IsModuleRef bool   // True if this is a module reference (starts with ":")
}

// ParseModuleReference parses a module reference string like ":module" or ":module{.tag}".
//
// This function handles various module reference formats:
//   - ":module" - simple module reference
//   - ":module{.tag}" - module reference with tag
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
//   - String without ":" prefix: Returns nil
//   - Malformed tag (missing closing "}"): Tag part is ignored
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
//   - ref: The parsed module reference
//   - modules: Map of all modules (name -> module)
//   - ctx: Rule render context for output name generation
//
// Returns:
//   - []string: List of output file paths, or nil if module not found
//
// Edge cases:
//   - nil reference: Returns nil
//   - Not a module reference: Returns nil
//   - Module type not registered: Returns nil
//   - Module has no outputs: Returns nil
//   - Tag ".stamp": Returns first output with ".stamp" appended
//   - Other tags: Returns first output with tag suffix appended
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
//
// Parameters:
//   - vis: Slice of visibility rules to check
//
// Returns:
//   - true if "//visibility:public" is in the list, false otherwise
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
// Private visibility restricts the module to be visible only within the
// same package. Other packages cannot reference this module.
//
// Parameters:
//   - vis: Slice of visibility rules to check
//
// Returns:
//   - true if "//visibility:private" is in the list, false otherwise
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
// This is typically used in specific build configurations.
//
// Parameters:
//   - vis: Slice of visibility rules to check
//
// Returns:
//   - true if "//visibility:override" is in the list, false otherwise
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
