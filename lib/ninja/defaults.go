// ninja/defaults.go - Defaults and meta-module types for minibp
// This file implements several meta-module types that don't produce build outputs
// but instead provide configuration and organization for other modules.
//
// Algorithm overview:
//  1. Detect module type at registration time (defaults, cc_defaults, etc.)
//  2. These modules serve as property containers that other modules inherit
//  3. During evaluation, referenced defaults properties are merged into child modules
//  4. Some types (phony, cc_test) produce actual build edges but of special nature
//
// Module types:
//   - defaults: Reusable property sets that other modules can inherit
//   - cc_defaults, java_defaults, go_defaults: Language-specific defaults
//   - package: Sets default properties for modules in a package
//   - soong_namespace: Manages namespace boundaries
//   - phony: Creates virtual alias targets
//   - cc_test: C/C++ test binary rule
//   - sh_binary_host: Host shell script binary
//   - python_binary_host, python_test_host: Host Python scripts
//
// Property inheritance:
//   - Modules reference defaults via the `defaults: ["name"]` property
//   - Properties from defaults are merged into the referencing module
//   - Language-specific defaults apply only to matching module types
//
// Each meta-module type implements the BuildRule interface:
//   - Name() string: Returns the module type name
//   - NinjaRule(ctx) string: Returns ninja rule definitions (usually empty)
//   - Outputs(m, ctx) []string: Returns output file paths (usually nil)
//   - NinjaEdge(m, ctx) string: Returns ninja build edges (usually empty)
//   - Desc(m, src) string: Returns a short description (usually empty)
//
// This file provides default property containers and meta-module types
// for the Ninja build system.
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"strings"
)

// defaults provides reusable property sets that can be inherited by other modules.
// Modules reference defaults via the `defaults: ["name"]` property. This enables
// common configurations (cflags, ldflags, etc.) to be defined once and shared
// across multiple modules, promoting DRY principles in build configurations.
//
// This rule type doesn't generate any build outputs; it serves as a property
// container that other modules can reference. During module evaluation,
// the build system merges properties from referenced defaults into the
// child module's properties, allowing centralized configuration management.
//
// Edge cases:
//   - Modules can reference multiple defaults; they are merged in order
//   - Child module properties override duplicate properties from defaults
//   - List properties (e.g., cflags, srcs) are combined, not replaced
type defaults struct{}

// Name returns the module type name for defaults modules.
// This is used by the build system to identify and register the defaults rule type.
//
// Returns the string "defaults" which is the rule type identifier.
// Returns a constant string value (no edge cases).
func (r *defaults) Name() string { return "defaults" }

// isDefaultsModuleType checks if a module type is a defaults variant.
// Valid defaults types are: defaults, cc_defaults, java_defaults, go_defaults.
// This is used to identify which module types serve as property containers
// rather than actual build rule producers.
//
// Returns:
//
//	true if the typeName is one of the recognized defaults types
//	false otherwise
func isDefaultsModuleType(typeName string) bool {
	switch typeName {
	case "defaults", "cc_defaults", "java_defaults", "go_defaults":
		return true
	default:
		return false
	}
}

// NinjaRule returns an empty string because defaults modules don't produce build rules.
// Defaults modules are property containers that are inherited by other modules
// during evaluation, not build targets that generate ninja rules.
//
// Parameters:
//   - ctx: The rule render context (unused)
//
// Returns an empty string since no ninja rule is needed.
// Returns a constant empty string (no edge cases).
func (r *defaults) NinjaRule(ctx RuleRenderContext) string {
	// Defaults modules don't produce any ninja rules.
	// They only serve as property containers for inheritance.
	return ""
}

// Outputs returns nil because defaults modules don't produce build outputs.
// Defaults modules are meta-modules that only provide properties for inheritance.
// They do not compile, link, or generate any files.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns nil since no output files are generated.
// Returns nil (no edge cases).
func (r *defaults) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Defaults modules don't produce any outputs.
	return nil
}

// NinjaEdge returns an empty string because defaults modules don't have build edges.
// Build edges define actual build actions; defaults modules only exist to be
// inherited by other modules during property evaluation phase.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns an empty string since no build edges are needed.
// Returns a constant empty string (no edge cases).
func (r *defaults) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Defaults modules don't produce any build edges.
	return ""
}

// Desc returns an empty description since defaults are meta-modules only.
// Defaults modules don't perform build actions, so no description is needed
// for ninja build output.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - srcFile: The source file path (unused)
//
// Returns an empty string since no build action description is needed.
// Returns a constant empty string (no edge cases).
func (r *defaults) Desc(m *parser.Module, srcFile string) string {
	// No description needed for property-only modules.
	return ""
}

// defaultsModule represents language-specific defaults modules such as
// cc_defaults, java_defaults, and go_defaults. These provide language-aware
// default properties that are inherited by modules of the corresponding language.
//
// Language-specific defaults differ from generic defaults in that they apply
// automatically to modules of the matching type (cc_library inherits from cc_defaults).
// This allows language-specific compiler flags and settings to be centralized.
//
// Fields:
//   - typeName: The module type name (e.g., "cc_defaults", "java_defaults", "go_defaults")
//     This determines which language modules will inherit these properties
type defaultsModule struct {
	typeName string
}

// Name returns the language-specific defaults module type name.
// Returns the typeName field which identifies the language this defaults applies to.
//
// Returns the module type name string (e.g., "cc_defaults", "java_defaults").
// Returns the value of r.typeName field (no edge cases).
func (r *defaultsModule) Name() string { return r.typeName }

// NinjaRule returns an empty string because defaults modules don't produce build rules.
// They only serve as property containers for inheritance by other modules.
// Language-specific defaults (cc_defaults, etc.) are evaluated during property
// merging, not during ninja rule generation.
//
// Parameters:
//   - ctx: The rule render context (unused)
//
// Returns an empty string since no ninja rule is needed.
// Returns a constant empty string (no edge cases).
func (r *defaultsModule) NinjaRule(ctx RuleRenderContext) string { return "" }

// Outputs returns nil because defaults modules don't produce outputs.
// Language-specific defaults modules are property containers only.
// They do not generate any build artifacts or output files.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns nil since no output files are generated.
// Returns nil (no edge cases).
func (r *defaultsModule) Outputs(m *parser.Module, ctx RuleRenderContext) []string { return nil }

// NinjaEdge returns an empty string because defaults modules don't have build edges.
// Language-specific defaults are evaluated during property inheritance phase,
// not during ninja build edge generation.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns an empty string since no build edges are needed.
// Returns a constant empty string (no edge cases).
func (r *defaultsModule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string { return "" }

// Desc returns an empty description since these are meta-modules only.
// Language-specific defaults don't perform build actions.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - srcFile: The source file path (unused)
//
// Returns an empty string since no build action description is needed.
// Returns a constant empty string (no edge cases).
func (r *defaultsModule) Desc(m *parser.Module, srcFile string) string { return "" }

// packageModule sets default properties for all modules within a package.
// It is named after the package path (e.g., "my/package") and allows
// package-level defaults to be applied to all modules in that directory,
// providing a way to enforce consistent settings across a package.
//
// Package modules are special meta-modules that apply to all modules in the same
// directory. Unlike defaults which require explicit references, package
// properties are automatically inherited by all modules in that directory.
//
// This enables package-level configuration such as:
//   - Common compiler flags for all sources in a package
//   - Shared include paths
//   - Package-specific build settings
//
// Edge cases:
//   - Package properties are merged before module-specific properties
//   - Module properties override conflicting package properties
//   - Nested packages don't automatically inherit from parent packages
type packageModule struct{}

// Name returns the module type name for package modules.
// This is used by the build system to identify and register the package rule type.
//
// Returns the string "package" which is the rule type identifier.
// Returns a constant string value (no edge cases).
func (r *packageModule) Name() string { return "package" }

// NinjaRule returns an empty string because package modules don't produce build rules.
// Package modules set default properties for modules in a directory,
// but do not generate any ninja build rules themselves.
//
// Parameters:
//   - ctx: The rule render context (unused)
//
// Returns an empty string since no ninja rule is needed.
// Returns a constant empty string (no edge cases).
func (r *packageModule) NinjaRule(ctx RuleRenderContext) string {
	// Package modules don't produce any ninja rules.
	return ""
}

// Outputs returns nil because package modules don't produce outputs.
// Package modules apply properties to other modules in the same directory,
// but do not generate any output files themselves.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns nil since no output files are generated.
// Returns nil (no edge cases).
func (r *packageModule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Package modules don't produce any outputs.
	return nil
}

// NinjaEdge returns an empty string because package modules don't have build edges.
// Package modules are property containers that affect modules in the same directory
// during property evaluation, not during ninja build edge generation.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns an empty string since no build edges are needed.
// Returns a constant empty string (no edge cases).
func (r *packageModule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Package modules don't produce any build edges.
	return ""
}

// Desc returns an empty description since package modules are meta-modules only.
// Package modules don't perform build actions, so no description is needed.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - srcFile: The source file path (unused)
//
// Returns an empty string since no build action description is needed.
// Returns a constant empty string (no edge cases).
func (r *packageModule) Desc(m *parser.Module, srcFile string) string {
	return ""
}

// soongNamespace defines namespace boundaries in a Soong build system.
// Namespaces prevent module name conflicts in large projects by scoping
// module references. Modules can reference modules in other namespaces
// using the `//namespace:module` syntax, enabling modular build organization.
//
// Namespace modules create logical boundaries within the build system.
// This prevents name collisions when multiple independent teams or
// components define modules with the same name.
//
// Syntax for cross-namespace references:
//   - //namespace:module - References a module in another namespace
//   - :module - References a module in the current namespace
//
// Edge cases:
//   - The root namespace is implicit and doesn't require a module
//   - Namespace resolution follows a specific search order
//   - Cycles in namespace references are not allowed
type soongNamespace struct{}

// Name returns the module type name for soong namespace modules.
// This is used by the build system to identify and register the soong_namespace rule type.
//
// Returns the string "soong_namespace" which is the rule type identifier.
// Returns a constant string value (no edge cases).
func (r *soongNamespace) Name() string { return "soong_namespace" }

// NinjaRule returns an empty string because namespace modules don't produce build rules.
// Namespace modules define scoping boundaries for module names,
// but do not generate any ninja build rules.
//
// Parameters:
//   - ctx: The rule render context (unused)
//
// Returns an empty string since no ninja rule is needed.
// Returns a constant empty string (no edge cases).
func (r *soongNamespace) NinjaRule(ctx RuleRenderContext) string {
	// Namespace modules don't produce any ninja rules.
	return ""
}

// Outputs returns nil because namespace modules don't produce outputs.
// Namespace modules define logical boundaries for module name resolution,
// but do not generate any output files.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns nil since no output files are generated.
// Returns nil (no edge cases).
func (r *soongNamespace) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Namespace modules don't produce any outputs.
	return nil
}

// NinjaEdge returns an empty string because namespace modules don't have build edges.
// Namespace modules are scoping constructs that affect module name resolution,
// not build targets that generate ninja edges.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns an empty string since no build edges are needed.
// Returns a constant empty string (no edge cases).
func (r *soongNamespace) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Namespace modules don't produce any build edges.
	return ""
}

// Desc returns an empty description since namespace modules are meta-modules only.
// Namespace modules don't perform build actions, so no description is needed.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - srcFile: The source file path (unused)
//
// Returns an empty string since no build action description is needed.
// Returns a constant empty string (no edge cases).
func (r *soongNamespace) Desc(m *parser.Module, srcFile string) string {
	return ""
}

// phonyRule creates virtual alias targets that don't produce real build outputs.
// Phony targets aggregate dependencies under a single logical name, useful for
// grouping related outputs (e.g., "all_tests" that depends on multiple test binaries).
// The target depends on modules listed in the `deps` property or source files.
//
// Phony rules are useful for:
//   - Creating alias targets that group multiple related outputs
//   - Defining "all" targets that depend on multiple build targets
//   - Virtual targets without actual compilation
//
// Algorithm:
//  1. Get the module name as the phony target name
//  2. Check the "deps" property for module dependencies
//  3. If no deps, fall back to source files (srcs property)
//  4. Generate a ninja phony rule that depends on all inputs
//
// Edge cases:
//   - If neither deps nor srcs are specified, create an empty phony target
//   - Module names starting with ":" are trimmed for ninja compatibility
type phonyRule struct{}

// Name returns the module type name for phony targets.
// This is used by the build system to identify and register the phony rule type.
//
// Returns the string "phony" which is the rule type identifier.
// Returns a constant string value (no edge cases).
func (r *phonyRule) Name() string { return "phony" }

// NinjaRule returns an empty string because phony uses ninja's built-in rule.
// Ninja has a built-in "phony" rule that creates dummy targets.
// No custom rule definition is needed.
//
// Parameters:
//   - ctx: The rule render context (unused)
//
// Returns an empty string since ninja's built-in phony rule is used.
// Returns a constant empty string (no edge cases).
func (r *phonyRule) NinjaRule(ctx RuleRenderContext) string {
	// Uses ninja's built-in phony rule, no custom rule needed.
	return ""
}

// Outputs returns nil because phony targets don't produce real output files.
// Phony targets are virtual aliases that group dependencies without generating files.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - ctx: The rule render context (unused)
//
// Returns nil since no output files are generated.
// Returns nil (no edge cases).
func (r *phonyRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Phony targets don't produce real output files.
	return nil
}

// NinjaEdge generates ninja build edges for phony targets.
// Creates a phony target that depends on either explicit dependencies (deps property)
// or source files (srcs property). The phony target serves as a virtual alias
// that groups multiple build outputs under a single name.
//
// Parameters:
//   - m: The parser.Module containing name, deps, and srcs properties
//   - ctx: The rule render context (unused)
//
// Returns a ninja build edge string for the phony target.
// Returns empty string if the module has no name.
//
// Edge cases:
//   - Empty name returns empty string (no target to create)
//   - Module references with ":" prefix are trimmed for ninja compatibility
//   - Explicit deps take priority over srcs
//   - If neither deps nor srcs exist, creates an empty phony target
//   - Output path is escaped for ninja compatibility
func (r *phonyRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	deps := GetListProp(m, "deps")
	if name == "" { // Module must have a name to create phony target
		return ""
	}
	// Convert module references (remove leading ":" prefix).
	var depNames []string
	for _, dep := range deps {
		depNames = append(depNames, strings.TrimPrefix(dep, ":")) // Trim ":" prefix from module refs
	}
	// Prefer explicit dependencies over source files.
	if len(depNames) > 0 { // Use explicit deps if specified
		return fmt.Sprintf("build %s: phony %s\n", ninjaEscapePath(name), strings.Join(depNames, " "))
	}
	// Fall back to source files if no deps specified.
	srcs := getSrcs(m)
	if len(srcs) > 0 { // Use source files as dependencies
		return fmt.Sprintf("build %s: phony %s\n", ninjaEscapePath(name), strings.Join(srcs, " "))
	}
	// Create empty phony target if no inputs.
	return fmt.Sprintf("build %s: phony\n", ninjaEscapePath(name))
}

// Desc returns the description for phony build actions.
// This description is used in ninja build output to identify the current step.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - srcFile: The source file path (unused)
//
// Returns the string "phony" identifying this build action.
// Returns a constant string value (no edge cases).
func (r *phonyRule) Desc(m *parser.Module, srcFile string) string {
	return "phony"
}

// ccTestRule defines the build rule for C/C++ test executables. It compiles
// C/C++ source files and links them into a test binary with the `.test` suffix.
// The rule handles test-specific configurations and produces executable outputs
// for running unit tests in the build system.
//
// The cc_test module type compiles C/C++ sources and produces a test executable.
// Test executables differ from regular binaries in that:
//   - Output file has ".test" suffix
//   - May have test-specific flags and configurations
//   - Can depend on test frameworks and testing libraries
//
// Algorithm:
//  1. Get the module name as the test binary name
//  2. Append ".test" suffix with architecture-specific variant
//  3. Generate ninja edge using cc_test edge function
//
// Edge cases:
//   - Module must have a name, otherwise no output is generated
//   - Architecture suffix comes from the build context
type ccTestRule struct{}

// Name returns the module type name for C/C++ test modules.
// This is used by the build system to identify and register the cc_test rule type.
//
// Returns the string "cc_test" which is the rule type identifier.
// Returns a constant string value (no edge cases).
func (r *ccTestRule) Name() string { return "cc_test" }

// NinjaRule returns an empty string because cc_test uses shared cc rules.
// The actual compilation uses the shared cc_test edge generation function.
// No separate ninja rule definition is needed.
//
// Parameters:
//   - ctx: The rule render context (unused)
//
// Returns an empty string since shared cc rules are used.
// Returns a constant empty string (no edge cases).
func (r *ccTestRule) NinjaRule(ctx RuleRenderContext) string {
	// No custom rule needed; uses cc_test edge generation.
	return ""
}

// Outputs returns the output file paths for C/C++ test binaries.
// The output filename is the module name with ".test" suffix and
// architecture-specific variant suffix (e.g., "mytest.test.arm64").
//
// Parameters:
//   - m: The parser.Module containing the module name
//   - ctx: The rule render context providing ArchSuffix for architecture variant
//
// Returns a slice containing the test binary output path.
// Returns nil if the module has no name.
//
// Edge cases:
//   - Empty name returns nil (no output without a name)
//   - ArchSuffix from context determines architecture variant in output path
func (r *ccTestRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" { // Module must have a name to generate output
		return nil
	}
	// Output is the module name with ".test" suffix and arch variant.
	return []string{name + ".test" + ctx.ArchSuffix}
}

// NinjaEdge generates ninja build edges for C/C++ test binaries.
// Delegates to the shared ccTestEdge function which handles the actual
// compilation and linking of C/C++ test sources with test-specific configurations.
//
// Parameters:
//   - m: The parser.Module containing test source files and properties
//   - ctx: The rule render context for build configuration
//
// Returns the ninja build edge string from ccTestEdge.
// Returns the result of ccTestEdge (no additional edge cases).
func (r *ccTestRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	return ccTestEdge(m, ctx)
}

// Desc returns the description for C/C++ test build actions.
// This description is used in ninja build output to identify the current step.
//
// Parameters:
//   - m: The parser.Module (unused)
//   - srcFile: The source file path (unused)
//
// Returns the string "cc_test" identifying this build action.
// Returns a constant string value (no edge cases).
func (r *ccTestRule) Desc(m *parser.Module, srcFile string) string {
	return "cc_test"
}

// shBinaryHostRule creates host-side shell script binaries. The rule copies
// the source shell script to the output location and sets executable permissions.
// This enables shell scripts to be integrated into the build graph as first-class
// build targets with proper dependency tracking.
//
// The sh_binary_host module type copies a shell script to the output
// directory and makes it executable. This allows shell scripts to:
//   - Be part of the build dependency graph
//   - Have proper dependency tracking
//   - Be installed with correct permissions
//
// Algorithm:
//  1. Get the module name for output filename
//  2. Get the first source file as the input
//  3. Generate copy rule with chmod +x for executable permissions
//
// Edge cases:
//   - Module must have both name and source file
//   - If no sources, generates nothing
type shBinaryHostRule struct{}

// Name returns the module type name for shell script binary modules.
// This is used by the build system to identify and register the sh_binary_host rule type.
//
// Returns the string "sh_binary_host" which is the rule type identifier.
// Returns a constant string value (no edge cases).
func (r *shBinaryHostRule) Name() string { return "sh_binary_host" }

// NinjaRule defines the ninja rule for copying shell scripts.
// Defines a rule that copies the source shell script to the output location
// and sets executable permissions using chmod +x.
//
// Parameters:
//   - ctx: The rule render context (unused)
//
// Returns a ninja rule definition string for shell script copying.
// Returns a constant string (no edge cases).
func (r *shBinaryHostRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule sh_copy
 command = cp $in $out && chmod +x $out
 description = Copy shell script $in
`
}

// Outputs returns the output file path for shell script binaries.
// The output filename is the module name with ".sh" suffix.
//
// Parameters:
//   - m: The parser.Module containing the module name
//   - ctx: The rule render context (unused)
//
// Returns a slice containing the shell script output path.
// Returns nil if the module has no name.
//
// Edge cases:
//   - Empty name returns nil (no output without a name)
//   - Output filename always has ".sh" suffix
func (r *shBinaryHostRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" { // Module must have a name to generate output
		return nil
	}
	return []string{name + ".sh"}
}

// NinjaEdge generates ninja build edges for shell script binaries.
// Creates a build edge that copies the source shell script to the output
// and makes it executable using the sh_copy rule.
//
// Parameters:
//   - m: The parser.Module containing name and srcs properties
//   - ctx: The rule render context (unused)
//
// Returns a ninja build edge string for copying the shell script.
// Returns empty string if the module has no name or no source files.
//
// Edge cases:
//   - Empty name or no sources returns empty string
//   - Only the first source file is used (srcs[0])
//   - Output path is escaped for ninja compatibility
func (r *shBinaryHostRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 { // Module must have name and source file
		return ""
	}
	out := name + ".sh"
	return fmt.Sprintf("build %s: sh_copy %s\n", ninjaEscapePath(out), ninjaEscapePath(srcs[0]))
}

func (r *shBinaryHostRule) Desc(m *parser.Module, srcFile string) string {
	return "sh_copy"
}

// pythonBinaryHostRule creates host-side Python script binaries. The rule copies
// the source Python script to the output location with `.py` suffix and sets
// executable permissions. This allows Python scripts to participate in the build
// graph as standard build targets with dependency management.
//
// The python_binary_host module type copies a Python script to the output
// directory and makes it executable. Similar to sh_binary_host but for Python.
//
// Algorithm:
//  1. Get the module name for output filename
//  2. Get the first source file as the input
//  3. Generate copy rule with chmod +x for executable permissions
//
// Edge cases:
//   - Module must have both name and source file
//   - Output filename has ".py" suffix
type pythonBinaryHostRule struct{}

func (r *pythonBinaryHostRule) Name() string { return "python_binary_host" }

func (r *pythonBinaryHostRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule python_copy
 command = cp $in $out && chmod +x $out
 description = Copy Python script $in
`
}

func (r *pythonBinaryHostRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".py"}
}

func (r *pythonBinaryHostRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	out := name + ".py"
	return fmt.Sprintf("build %s: python_copy %s\n", ninjaEscapePath(out), ninjaEscapePath(srcs[0]))
}

func (r *pythonBinaryHostRule) Desc(m *parser.Module, srcFile string) string {
	return "python_copy"
}

// pythonTestHostRule defines the build rule for host-side Python tests. The rule
// executes Python test scripts using python3 with the `.test.py` output suffix.
// Test arguments can be passed via the `test_options` property. This rule enables
// Python-based test suites to be integrated into the build system's test execution.
//
// The python_test_host module type runs a Python test script.
// Tests are executed using python3 with optional arguments.
//
// Properties:
//   - srcs: Source Python script to execute
//   - test_options: Additional arguments passed to the test script
//
// Algorithm:
//  1. Get the module name for output filename
//  2. Get the first source file as the test script
//  3. Generate python_test rule with optional args
//
// Edge cases:
//   - Module must have both name and source file
//   - Output filename has ".test.py" suffix
//   - test_options can be empty
type pythonTestHostRule struct{}

func (r *pythonTestHostRule) Name() string { return "python_test_host" }

func (r *pythonTestHostRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule python_test
 command = python3 $in $args
 description = Run Python test $in
`
}

func (r *pythonTestHostRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".test.py"}
}

func (r *pythonTestHostRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	out := name + ".test.py"
	args := getTestOptionArgs(m)
	edge := fmt.Sprintf("build %s: python_test %s\n", ninjaEscapePath(out), ninjaEscapePath(srcs[0]))
	if args != "" {
		edge += fmt.Sprintf(" args = %s\n", args)
	}
	return edge
}

func (r *pythonTestHostRule) Desc(m *parser.Module, srcFile string) string {
	return "python_test"
}
