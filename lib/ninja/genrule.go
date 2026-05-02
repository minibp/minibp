// Package ninja provides Ninja build file generation for the minibp build system.
//
// This file (genrule.go) implements the genrule module type, which is a generic
// build rule that executes arbitrary shell commands. Genrules are useful for:
//   - Code generation (e.g., protobuf, thrift, custom code generators)
//   - Script execution (e.g., preprocessing, postprocessing steps)
//   - Custom build steps that don't fit into specialized rule types
//
// The genrule module type mirrors Bazel's genrule functionality and accepts
// the following properties:
//   - srcs: Source files that the command reads (regular dependencies)
//   - cmd: The shell command to execute (supports variable substitution)
//   - outs: Output files produced by the command (required for build edges)
//   - tool_files: Additional tools needed to run the command (order-only deps)
//   - deps: Additional dependencies (order-only deps)
//   - data: Data files needed at runtime (regular dependencies)
//
// Algorithm overview:
//  1. Parse module properties from the Blueprint definition
//  2. Generate a ninja rule template with $cmd variable for command execution
//  3. Handle input/output path substitution in build edges
//  4. Categorize dependencies: srcs/data are regular, tool_files/deps are order-only
//  5. Output the complete ninja build edge with all dependencies and command
//
// Edge cases:
//   - If "outs" property is specified, those files are used as outputs
//   - If "outs" is empty, a default output is generated as {module_name}.out
//   - tool_files and deps are treated as order-only dependencies (|) in ninja
//   - data files are treated as regular dependencies (affects build order)
//   - If "cmd" is empty, no build edge is generated (silent skip)
//
// Genrules are powerful but should be used sparingly because they:
//   - Hide dependencies from the build system (less visible dependency graph)
//   - Can produce non-deterministic outputs if commands aren't idempotent
//   - May not support incremental builds well (full re-execution on any change)
//
// Best practices:
//   - Use specific rule types when available (cc_library, go_binary, etc.)
//   - Use genrule only for truly custom build steps
//   - Ensure commands produce deterministic outputs
//   - Prefer explicit dependencies over implicit file system scanning
//
// The genrule type implements the BuildRule interface:
//   - Name() string: Returns "genrule" as the module type identifier
//   - NinjaRule(ctx) string: Returns the ninja rule definition string
//   - Outputs(m, ctx) []string: Returns the list of output file paths
//   - NinjaEdge(m, ctx) string: Returns the complete ninja build edge
//   - Desc(m, src) string: Returns a short human-readable description
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"strings"
)

// genrule is a generic build rule that executes arbitrary shell commands.
//
// It provides flexibility for custom build tasks such as code generation,
// script execution, or running external tools within the build pipeline.
// This implementation mirrors Bazel's genrule functionality, allowing users
// to define custom build steps using shell commands.
//
// The genrule type supports the following Blueprint properties:
//   - name: Unique identifier for the module (inherited from base module)
//   - srcs: Source files that the command reads (space-separated list)
//   - cmd: The shell command to execute (string, supports variable substitution)
//   - outs: Output files produced by the command (space-separated list)
//   - tool_files: Additional tools needed to run the command (order-only deps)
//   - deps: Additional dependencies (order-only deps)
//   - data: Data files needed at runtime (regular dependencies)
//
// Genrules are powerful but should be used sparingly because they:
//   - Hide dependencies from the build system (less visible dependency graph)
//   - Can produce non-deterministic outputs if commands aren't idempotent
//   - May not support incremental builds well (full re-execution on any change)
//   - Bypass the type-safe build rule system
//
// Best practices:
//   - Use specific rule types when available (cc_library, go_binary, etc.)
//   - Use genrule only for truly custom build steps
//   - Ensure commands produce deterministic outputs
//   - Explicitly declare all input files in srcs, tool_files, or deps
//   - Avoid using absolute paths; use relative paths within the build context
//   - Test commands manually before adding to Blueprint files
//
// Key design decisions:
//   - Uses order-only dependencies (|) for tool_files and deps to avoid
//     unnecessary rebuilds when tools change without affecting outputs
//   - Sets restat=1 in the ninja rule to skip rebuilds when only mtime changes
//   - Shell-escapes the command to prevent injection attacks and syntax errors
//   - Generates default output ({name}.out) when no outs property is specified
type genrule struct{}

// Name returns the module type name for genrule.
//
// This name is used to identify genrule modules in the build system.
// When parsing Blueprint files, the parser looks for "genrule { ... }" blocks
// and associates them with this rule type.
//
// Returns:
//   - "genrule": The constant string identifier for this module type.
//     This must match the name used in Blueprint files (case-sensitive).
//
// Example Blueprint usage:
//
//	genrule {
//	    name: "my_generated_file",
//	    cmd: "echo 'hello' > $out",
//	    outs: ["output.txt"],
//	}
func (r *genrule) Name() string { return "genrule" }

// NinjaRule returns the ninja rule template for genrule command execution.
//
// The template defines a generic rule that runs an arbitrary shell command.
// The actual command is passed via the $cmd variable, which is set in the
// build edge (not in the rule definition). This allows different genrule
// modules to use different commands while sharing the same rule definition.
//
// The rule uses:
//   - command: $cmd (the actual command to execute, set per build edge)
//   - description: Shows "Genrule" followed by output files being generated
//   - restat: 1 (don't rebuild if only mtime changed, not content)
//
// The restat=1 setting is important because:
//   - It tells Ninja to check the file content hash after command execution
//   - If the output file's content hasn't changed, downstream builds are skipped
//   - This is useful for code generators that may not always produce changes
//   - Without restat, Ninja would always rebuild downstream dependencies
//
// Parameters:
//   - ctx: Rule rendering context containing architecture and toolchain info.
//     Currently unused in this rule, but provided for interface compatibility.
//     Future implementations may use ctx for platform-specific command variants.
//
// Returns:
//   - A string containing the complete ninja rule definition.
//     The returned string includes trailing newline for proper formatting
//     when concatenated with other rules in the build.ninja file.
//
// Example output:
//
//	rule genrule_command
//	 command = $cmd
//	 description = Genrule $out
//	 restat = 1
//
// Key design decisions:
//   - Uses a single shared rule (genrule_command) for all genrule instances
//     to reduce duplication in build.ninja
//   - Command is a variable ($cmd) rather than inline to support
//     shell escaping and special characters in commands
//   - Description shows output files ($out) to help users understand
//     what each build step is doing during ninja execution
func (r *genrule) NinjaRule(ctx RuleRenderContext) string {
	return `rule genrule_command
 command = $cmd
 description = Genrule $out
 restat = 1
`
}

// Outputs returns the output files for the genrule module.
//
// If the "outs" property is specified in the Blueprint definition, those files
// are used as the output paths. Otherwise, a default output file is generated
// using the module name with a ".out" extension (e.g., "my_rule.out").
//
// The output files are used by Ninja to:
//   - Determine build order (outputs become inputs for downstream rules)
//   - Track when a rule needs to be re-executed (based on output mtime/hash)
//   - Display in the build description (shown in "Genrule $out")
//
// Parameters:
//   - m: The module being evaluated.
//     Contains the Blueprint properties like "outs", "name", etc.
//     Must not be nil; behavior is undefined for nil modules.
//   - ctx: Rule rendering context with architecture and toolchain info.
//     Currently unused in this method, but provided for interface compatibility.
//     Future implementations may use ctx for platform-specific output paths.
//
// Returns:
//   - List of output file paths (as strings).
//     Returns nil if the module has no name (invalid module).
//     Returns the "outs" property value if specified and non-empty.
//     Returns a single-element slice with default name if "outs" is empty.
//
// Edge cases:
//   - Returns nil if module name is empty (module is malformed).
//     This prevents generating build edges for invalid modules.
//   - If "outs" property is an empty list, generates default output.
//     This ensures every genrule produces at least one output file.
//   - The "outs" property is not validated for path correctness.
//     Invalid paths will cause ninja build errors at runtime.
//   - Default output uses ".out" extension regardless of content type.
//     Users should specify "outs" explicitly for non-generic outputs.
//
// Example Blueprint usage and resulting outputs:
//
//	genrule {
//	    name: "generate_foo",
//	    cmd: "generate_foo.sh > $out",
//	    outs: ["foo.txt"],
//	}
//	// Outputs: ["foo.txt"]
//
//	genrule {
//	    name: "simple_rule",
//	    cmd: "echo hello > $out",
//	}
//	// Outputs: ["simple_rule.out"]
func (r *genrule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" { // Validate module has a name
		return nil
	}
	outs := GetListProp(m, "outs")
	if len(outs) > 0 { // Use explicitly specified output files from "outs" property
		return outs
	}
	// Default output if no outs specified.
	// Generates {module_name}.out as a fallback to ensure the rule has outputs.
	return []string{name + ".out"}
}

// NinjaEdge generates the complete ninja build edge for genrule execution.
//
// A ninja build edge connects inputs (sources and dependencies) to outputs
// through a build rule (in this case, genrule_command defined in NinjaRule).
// This method constructs the full edge statement including all inputs,
// outputs, order-only dependencies, and the command to execute.
//
// The generated edge follows this ninja syntax:
//
//	build <outputs>: <rule> <inputs> | <order-only-deps>
//	 <variables>
//
// Algorithm:
//  1. Extract module name, source files, and command from the module properties.
//  2. Validate required fields (name and cmd must be non-empty).
//  3. Get output files using Outputs() method (uses "outs" property or default).
//  4. Collect all dependencies: explicit deps, tool_files, and data files.
//  5. Categorize dependencies: srcs+data are regular, tool_files+deps are order-only.
//  6. Escape all paths for ninja syntax (spaces, special characters).
//  7. Shell-escape the command to prevent injection and syntax errors.
//  8. Build and return the complete edge string.
//
// Parameters:
//   - m: The module being processed.
//     Contains all Blueprint properties (name, srcs, cmd, outs, tool_files, deps, data).
//     Must not be nil; behavior is undefined for nil modules.
//   - ctx: Rule rendering context with architecture and toolchain info.
//     Currently unused in this method, but provided for interface compatibility.
//     Future implementations may use ctx for platform-specific path resolution.
//
// Returns:
//   - A complete ninja build edge string if all required fields are present.
//     The string includes newline characters for proper formatting.
//   - An empty string if validation fails (missing name, cmd, or outputs).
//     No error is returned; silent skip allows other modules to continue building.
//
// Edge cases:
//   - Returns empty string if module name is empty (invalid module).
//     This prevents generating edges for malformed Blueprint definitions.
//   - Returns empty string if "cmd" property is empty.
//     A genrule without a command cannot produce outputs.
//   - Returns empty string if no outputs are defined (and no default generated).
//     This can happen if Outputs() returns nil or empty slice.
//   - Empty srcs list is valid: command may not need input files.
//   - Empty allDeps (no tool_files, deps, or data) is valid: no order-only deps emitted.
//   - Path escaping handles spaces and special characters in file paths.
//     This ensures ninja correctly parses paths with unusual characters.
//   - Shell escaping handles quotes, spaces, and special chars in commands.
//     This prevents shell injection and syntax errors during execution.
//
// Dependencies are categorized as follows:
//   - Regular dependencies (affect build order): srcs, data
//   - Order-only dependencies (don't affect rebuild): tool_files, deps
//
// Order-only dependencies (| in ninja) are used for tool_files and deps because:
//   - Changes to tools shouldn't trigger rebuilds if outputs haven't changed
//   - This enables better incremental builds when only tools are updated
//   - The restat=1 setting in the rule handles output change detection
//
// Example output:
//
//	build output.txt: genrule_command input.txt | //tools:my_tool data_file
//	 cmd = echo "hello" > $out
//
// Key design decisions:
//   - Uses strings.Builder for efficient string concatenation.
//     This avoids repeated string allocations for large dependency lists.
//   - Separates dependency collection from edge building for clarity.
//     This makes the code easier to maintain and debug.
//   - Shell-escapes the command to handle special characters safely.
//     Without escaping, commands with spaces or quotes would fail.
//   - Does not validate that srcs files actually exist.
//     Ninja will report missing inputs at build time, not generation time.
func (r *genrule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Extract basic module properties needed for the build edge.
	// name is used for logging/debugging (not directly in edge).
	// srcs are the regular dependencies (inputs to the command).
	// cmd is the shell command to execute (will be shell-escaped).
	name := getName(m)
	srcs := getSrcs(m)
	cmd := GetStringProp(m, "cmd")

	// Validate required fields before proceeding.
	// Both name and cmd are essential: name identifies the module for debugging,
	// and cmd is the actual command that produces the outputs.
	if name == "" || cmd == "" { // Validate required fields before proceeding
		return ""
	}

	// Get output files for this genrule.
	// Uses the Outputs() method which checks "outs" property or generates default.
	outs := r.Outputs(m, ctx)
	if len(outs) == 0 { // Validate outputs are defined
		return ""
	}

	// Collect all dependencies: explicit deps, tool files, and runtime data.
	// These are categorized as order-only dependencies (|) in the ninja edge.
	// tool_files and deps are order-only: changes don't trigger rebuilds.
	// data is treated as regular dependency here (affects build order).
	toolFiles := GetListProp(m, "tool_files")
	deps := GetListProp(m, "deps")
	data := getData(m)

	// Combine all order-only dependencies into a single slice.
	// Order doesn't matter for dependencies in ninja build edges.
	var allDeps []string
	allDeps = append(allDeps, deps...)
	allDeps = append(allDeps, toolFiles...)
	allDeps = append(allDeps, data...)

	// Build the ninja edge statement.
	// Uses strings.Builder for efficient string concatenation.
	var edges strings.Builder

	// Escape all output paths for ninja syntax.
	// This handles spaces, special characters, and ninja-specific escaping.
	escapedOuts := make([]string, 0, len(outs))
	for _, out := range outs {
		escapedOuts = append(escapedOuts, ninjaEscapePath(out))
	}

	// Write the build edge header: "build <outputs>: <rule> <inputs>"
	// Outputs and inputs are space-separated; ninja parses them accordingly.
	edges.WriteString(fmt.Sprintf("build %s: genrule_command %s", strings.Join(escapedOuts, " "), strings.Join(srcs, " ")))

	// Add order-only dependencies (tool_files, deps) after the pipe (|).
	// Order-only deps don't affect whether the output is rebuilt,
	// but they must exist before the command runs.
	if len(allDeps) > 0 { // Add order-only dependencies (tool_files, deps) after the pipe (|)
		edges.WriteString(fmt.Sprintf(" | %s", strings.Join(allDeps, " ")))
	}
	edges.WriteString("\n")

	// Add the command variable for this specific build edge.
	// The command is shell-escaped to handle special characters safely.
	// This uses shellEscape() to prevent injection and syntax errors.
	edges.WriteString(fmt.Sprintf(" cmd = %s\n", shellEscape(cmd)))
	return edges.String()
}

// Desc returns a short description string for logging and debugging purposes.
//
// This description is used by the build system to provide human-readable
// information about the module during processing. It may be displayed in
// logs, error messages, or debugging output to help identify which module
// is being processed.
//
// Parameters:
//   - m: The module being described.
//     Currently unused in this implementation, but provided for interface
//     compatibility. Future implementations may include module name or
//     output files in the description.
//   - srcFile: The source Blueprint file path containing this module.
//     Currently unused in this implementation, but may be used in the
//     future to provide context about where the module was defined.
//
// Returns:
//   - A constant string "genrule" identifying this as a genrule module type.
//     The returned string is short and unambiguous, suitable for log output
//     and error messages.
//
// Edge cases:
//   - Returns the same string regardless of module properties.
//     This is intentional: the description is meant to identify the rule type,
//     not the specific module instance.
//   - Both parameters (m and srcFile) are ignored in this implementation.
//     This is acceptable because the interface requires them for other rule
//     types that may need module-specific or file-specific descriptions.
//
// Example usage in logging:
//
//	module := // ... a genrule module
//	fmt.Printf("Processing: %s\n", rule.Desc(module, "BUILD.bp"))
//	// Output: Processing: genrule
//
// Key design decisions:
//   - Returns a constant string rather than a dynamic description.
//     This keeps the implementation simple and consistent across all
//     genrule instances. If module-specific descriptions are needed,
//     this method can be updated to include the module name.
//   - Does not include the module name in the description.
//     The caller is responsible for providing additional context if needed.
func (r *genrule) Desc(m *parser.Module, srcFile string) string {
	return "genrule"
}
