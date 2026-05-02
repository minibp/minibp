// Package ninja provides custom build rules and protocol buffer compilation rules for the Ninja build system.
//
// This file implements three module types that extend the standard build rules
// (go_binary, go_library, go_test) with support for arbitrary commands and
// protocol buffer compilation:
//
//  1. custom_rule: Executes arbitrary shell commands with $in/$out substitution.
//     Allows users to define flexible build steps without writing Go code.
//     Supports specifying input files (srcs), output files (outs), commands (cmd),
//     and additional flags.
//
//  2. proto_library: Compiles .proto files to language-specific generated code.
//     Supports Go, Java, Python, and C++ output through the "out" property.
//     Can use protoc plugins and custom proto include paths.
//
//  3. proto_gen: A simplified variant for proto file generation.
//     Produces a generic output artifact ({name}_proto) for downstream consumption.
//     Useful when the exact output files are not known in advance.
//
// All module types implement the BuildRule interface, which defines the contract
// between the build system and individual rule implementations:
//   - Name() string: Returns the module type name (e.g., "custom_rule")
//   - NinjaRule(ctx) string: Returns ninja rule definitions (the "rule" block)
//   - Outputs(m, ctx) []string: Returns expected output file paths
//   - NinjaEdge(m, ctx) string: Returns ninja build edges (the "build" block)
//   - Desc(m, src) string: Returns a short description for build output
//
// The custom_rule module type is particularly flexible, allowing users to invoke
// any command-line tool as part of the build. The special variables $in and $out
// in the command string are replaced with actual file paths at generation time.
//
// Example Blueprint usage:
//
//	custom_rule {
//	    name: "generate_something",
//	    srcs: ["input.txt"],
//	    cmd: "python $in > $out",
//	    outs: ["output.txt"],
//	}
//
//	proto_library {
//	    name: "my_proto",
//	    srcs: ["my.proto"],
//	    out: "go",
//	}
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"strings"
)

// customRule implements the BuildRule interface for custom build rules.
// It allows users to define arbitrary shell commands as part of the build process,
// with automatic substitution of input ($in) and output ($out) file paths.
//
// This rule type is used for the "custom_rule" module type in Blueprint files.
// Unlike the Go-specific build rules (go_binary, go_library, go_test), custom_rule
// can invoke any command-line tool, making it suitable for:
//   - Code generation tools (yacc, protoc, thrift, etc.)
//   - Script execution (Python, Shell, Perl scripts)
//   - File transformation tools (sed, awk, custom processors)
//   - Any build step that doesn't fit the standard Go build pipeline
//
// Module properties:
//   - name: Unique identifier for the module (required)
//   - srcs: Input source files (substituted as $in in the command)
//   - cmd: Shell command to execute (required, supports $in and $out variables)
//   - outs: Output files produced (optional, defaults to {name}.out)
//   - flags: Additional flags passed to the command (optional)
//
// Variable substitution in cmd:
//   - $in: Replaced with space-separated list of input files (from srcs)
//   - $out: Replaced with the primary output file (first element of outs)
//
// The generated ninja rule uses a two-step command construction:
//  1. The rule definition sets up: command = $command $in $flags
//  2. The build edge provides the actual command string via the "cmd" variable
//
// Algorithm overview for NinjaEdge:
//  1. Validate module has name and command; return empty if missing
//  2. Collect source files and output files
//  3. Replace $in and $out placeholders in the command string
//  4. Escape all paths for ninja compatibility (spaces, special characters)
//  5. Generate build edge with proper variable bindings
//
// Key design decisions:
//   - Using a generic "custom_command" rule that takes the actual command via variable
//     allows multiple custom_rule modules to share a single ninja rule definition
//   - Paths are escaped using ninjaEscapePath to handle spaces and special characters
//   - The $in variable in the final command uses escaped paths, while the build edge
//     lists sources with proper ninja escaping
//
// Edge cases:
//   - Returns empty string from NinjaEdge if name or command is empty (invalid module)
//   - Returns empty string if no outputs defined (outs empty and no default)
//   - $in can expand to multiple files, which appear space-separated in the command
//   - If outs is not specified, defaults to {name}.out as a single output
//   - Flags property is optional; if empty, no flags are added to the command
//   - All file paths are escaped for ninja compatibility using ninjaEscapePath
type customRule struct{}

// Name returns the module type name for this rule.
//
// Returns:
//   - "custom_rule": The identifier used in Blueprint files to define custom build rules.
//     This is the string users write as the module type (e.g., custom_rule { ... }).
func (r *customRule) Name() string { return "custom_rule" }

// NinjaRule returns the ninja rule definition for custom commands.
//
// The rule defines a template that can be reused by multiple custom_rule modules.
// It uses variable substitution ($command, $in, $flags) so that each build edge
// can provide its own values for these variables.
//
// The rule template includes:
//   - command: The actual command to execute, built from $command $in $flags
//   - description: A human-readable description shown during the build ("Custom build $out")
//
// Key design decisions:
//   - Using a single shared rule ("custom_command") for all custom_rule modules
//     reduces ninja file size and improves parse time
//   - The command is split into $command (the tool and arguments) and $flags (extra flags)
//     to allow flexible command construction
//   - Description shows the output file ($out) to help identify which build step is running
//
// Parameters:
//   - ctx: The rule rendering context (currently unused for custom_rule,
//     but required by the BuildRule interface).
//
// Returns:
//   - A string containing the ninja "rule" block definition.
//     The returned string includes trailing newlines for proper formatting
//     when concatenated with other ninja definitions.
func (r *customRule) NinjaRule(ctx RuleRenderContext) string {

	return `rule custom_command

 command = $command $in $flags

 description = Custom build $out

`

}

// Outputs returns the expected output files for a custom_rule module.
//
// The output files are determined by the "outs" property in the module definition.
// If "outs" is not specified, a default output file ({name}.out) is used.
//
// Parameters:
//   - m: The module being evaluated. Must contain a valid "name" property.
//   - ctx: The rule rendering context (currently unused, but required by interface).
//
// Returns:
//   - A slice of output file paths. Returns nil if the module has no name
//     (invalid module) or if outputs cannot be determined.
//
// Edge cases:
//   - Returns nil if module name is empty (module is invalid).
//   - If "outs" property is empty or not specified, defaults to []string{name + ".out"}.
//   - The "outs" property can contain multiple files (for commands that produce multiple outputs).
//   - Output paths are returned as-is from the "outs" property without path resolution.
func (r *customRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" { // Validate module has a name
		return nil
	}
	// Custom rules can have explicit outputs (outs property).
	// GetListProp returns a slice of strings from the "outs" property.
	outputs := GetListProp(m, "outs")
	if len(outputs) > 0 { // Use explicit outputs if specified
		return outputs
	}
	// Default output: use module name with ".out" extension.
	// This provides a reasonable default when the user doesn't specify outputs.
	return []string{name + ".out"}
}

// NinjaEdge generates the ninja build edge for a custom_rule module.
//
// The build edge connects the input files (srcs) to the output files (outs)
// using the custom_command rule defined in NinjaRule. It performs variable
// substitution ($in, $out) in the command string and escapes paths for ninja.
//
// Algorithm:
//  1. Retrieve module name and validate it exists
//  2. Collect source files (srcs) and the command (cmd)
//  3. Exit early if name or command is missing (invalid module)
//  4. Retrieve optional flags and output files
//  5. Replace $out with the primary output file path
//  6. Replace $in with space-separated input file paths
//  7. Escape all paths for ninja compatibility
//  8. Generate build edge with variable bindings for cmd and flags
//
// Parameters:
//   - m: The module being processed. Must have "name" and "cmd" properties.
//   - ctx: The rule rendering context (currently unused, but required by interface).
//
// Returns:
//   - A string containing the ninja "build" edge definition, or empty string
//     if the module is invalid (missing name, command, or outputs).
//
// Edge cases:
//   - Returns empty string if module name is empty (invalid module).
//   - Returns empty string if "cmd" property is empty (nothing to execute).
//   - Returns empty string if no outputs are defined (outs empty and no default).
//   - $in can expand to multiple space-separated files in the command.
//   - The command string can reference $in and $out multiple times.
//   - Paths are escaped using ninjaEscapePath to handle spaces and special characters.
//   - The escaped version of $in (with escaped paths) is used in the final command,
//     while the build edge lists sources with proper ninja escaping.
//
// Key design decisions:
//   - Two forms of $in substitution: unescaped (for display in command) and
//     escaped (for actual execution). The escaped version handles special characters.
//   - Only the first output file (outs[0]) is used for $out substitution.
//     Additional outputs are listed in the build edge but not substituted in the command.
//   - The "cmd" variable in the build edge contains the command with $in already
//     replaced with escaped paths, allowing ninja to handle spaces in filenames.
func (r *customRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {

	// Get module name - used for identification and default output.
	name := getName(m)

	// Get source files - these will be substituted for $in in the command.
	srcs := getSrcs(m)

	// Get the command to execute - required for custom rules.
	// The command can contain $in and $out placeholders.
	command := GetStringProp(m, "cmd")

	// Validate required properties: name and cmd must be present.
	if name == "" || command == "" { // Validate required properties: name and cmd must be present
		return ""
	}

	// Get optional flags - additional arguments passed to the command.
	flags := GetStringProp(m, "flags")

	// Get output files - either from "outs" property or default.
	outs := r.Outputs(m, ctx)

	// Validate outputs exist.
	if len(outs) == 0 { // Validate outputs exist
		return ""
	}

	// Use the first output file for $out substitution in the command.
	out := outs[0]

	// Build the $in string: space-separated list of input files (unescaped).
	// This is used for $in substitution in the command string for display.
	inStr := strings.Join(srcs, " ")
	// Build the escaped version of $in for actual command execution.
	// Escaped paths handle spaces and special characters in filenames.
	escapedInStr := strings.Join(escapeList(srcs), " ")

	// Perform variable substitution in the command string.
	// First replace $out with the actual output file path.
	actualCommand := strings.ReplaceAll(command, "$out", out)
	// Then replace $in with the unescaped input file list (for display).
	actualCommand = strings.ReplaceAll(actualCommand, "$in", inStr)

	// Escape the output file path for ninja compatibility.
	escapedOut := ninjaEscapePath(out)

	// Escape all source file paths for the build edge.
	// The build edge lists sources with proper ninja escaping.
	escapedSrcs := make([]string, len(srcs))

	for i, src := range srcs { // Escape all source file paths for the build edge
		escapedSrcs[i] = ninjaEscapePath(src)
	}

	var edges strings.Builder

	// Write the build edge: "build <outputs>: custom_command <inputs>"
	// The rule name "custom_command" must match the rule defined in NinjaRule.
	edges.WriteString(fmt.Sprintf("build %s: custom_command %s\n", escapedOut, strings.Join(escapedSrcs, " ")))

	// Write the "cmd" variable binding.
	// The command uses escaped input paths for proper ninja execution.
	edges.WriteString(fmt.Sprintf("  cmd = %s\n", strings.ReplaceAll(actualCommand, "$in", escapedInStr)))

	// Write the "flags" variable binding if flags are specified.
	if flags != "" { // Write the "flags" variable binding if flags are specified
		edges.WriteString(fmt.Sprintf("  flags = %s\n", flags))
	}

	// Add trailing newline to separate from next build edge.
	edges.WriteString("\n")

	return edges.String()

}

// Desc returns a short description for custom_rule build steps.
//
// This description is used in build output and error messages to identify
// the type of build rule being executed.
//
// Parameters:
//   - m: The module being built (unused for custom_rule).
//   - srcFile: The source file being processed (unused for custom_rule).
//
// Returns:
//   - "custom": A short identifier for custom build rules.
func (r *customRule) Desc(m *parser.Module, srcFile string) string {
	return "custom"
}

// protoLibraryRule implements the BuildRule interface for protocol buffer compilation.
// It compiles .proto files into language-specific generated code using the protoc compiler.
//
// This rule handles the "proto_library" module type in Blueprint files.
// Protocol Buffers (protobuf) is a language-neutral serialization format;
// this rule invokes the protoc tool to generate code in the target language.
//
// Supported output languages (specified via the "out" property):
//   - "go": Generates Go code (*.pb.go) using protoc-gen-go plugin
//   - "java": Generates Java code (*.java)
//   - "py": Generates Python code (*_pb2.py)
//   - "cc" or default: Generates C++ header (*.pb.h) and implementation (*.pb.cc)
//
// Module properties:
//   - name: Unique identifier for the module (required)
//   - srcs: Input .proto files (required, at least one)
//   - out: Output language ("go", "java", "py", "cc", or default for C++)
//   - plugins: List of protoc plugins to use (e.g., "protoc-gen-go")
//   - proto_paths: Include paths for proto imports (mapped to --proto_path flags)
//
// The generated ninja rules can invoke protoc with various flags:
//   - --cpp_out: For C++ output
//   - --go_out: For Go output (via plugin)
//   - --java_out: For Java output
//   - --python_out: For Python output
//   - --plugin: Specify custom protoc plugins
//   - --proto_path: Add directories to the proto import search path
//
// Example Blueprint usage:
//
//	proto_library {
//	    name: "my_proto",
//	    srcs: ["mymessage.proto"],
//	    out: "go",
//	    plugins: ["protoc-gen-go"],
//	}
//
// Algorithm overview for NinjaEdge:
//  1. Validate module has name and source files
//  2. Retrieve optional properties: plugins, proto_paths
//  3. Determine output type from "out" property
//  4. Build the protoc command with appropriate flags
//  5. Add plugin flags for each specified plugin
//  6. Add proto_path flags for each include directory
//  7. Generate build edges based on output type (may generate multiple for C++)
//
// Key design decisions:
//   - Currently, only a single .proto file is processed per module (uses srcs[0]).
//     This simplifies the output file naming and build edge generation.
//   - The default output is C++ because protoc natively supports C++ without plugins.
//   - Plugin support allows extending protoc with custom code generators.
//   - proto_paths are added as variables in the build edge, allowing per-module
//     customization of the proto import search path.
//
// Edge cases:
//   - Multiple output languages in a single module are not supported;
//     use separate proto_library modules for each language.
//   - Default output (when "out" is not specified) is C++ (.pb.h and .pb.cc).
//   - Proto paths are passed as --proto_path flags to protoc.
//   - If plugins are specified, they are added as --plugin flags.
//   - The rule expects at least one .proto file in srcs; returns empty if none.
//   - Only the first source file is used for output file naming (baseName calculation).
type protoLibraryRule struct{}

// Name returns the module type name for this rule.
//
// Returns:
//   - "proto_library": The identifier used in Blueprint files to define
//     protocol buffer compilation modules. This is the string users write
//     as the module type (e.g., proto_library { ... }).
func (r *protoLibraryRule) Name() string { return "proto_library" }

// NinjaRule returns the ninja rule template for protocol buffer compilation.
//
// The rule defines a template for running the protoc compiler.
// Currently, the rule is configured for C++ output by default (--cpp_out),
// but the actual output type is determined by the "out_type" variable
// set in each build edge.
//
// The rule template includes:
//   - command: Currently shows --cpp_out, but with proper variable bindings
//     in the build edge, other output types can be supported
//   - description: A human-readable description shown during the build
//
// Note: This is a simplified rule definition. In a production build system,
// different output languages might have separate rules with appropriate
// protoc flags (--go_out, --java_out, --python_out, etc.).
//
// Parameters:
//   - ctx: The rule rendering context (currently unused for proto_library,
//     but required by the BuildRule interface).
//
// Returns:
//   - A string containing the ninja "rule" block definition.
//     The returned string includes trailing newlines for proper formatting.
func (r *protoLibraryRule) NinjaRule(ctx RuleRenderContext) string {

	return `rule proto_compile

 command = protoc --cpp_out=$out $in

 description = Compiling proto $in

`

}

// Outputs returns the expected output files for proto compilation.
//
// The output files are determined by the "out" property in the module definition.
// The base name is derived from the first .proto source file (without the .proto extension).
//
// Output file mapping:
//   - "go": Generates {baseName}.pb.go (single file)
//   - "java": Generates {baseName}.java (single file)
//   - "py": Generates {baseName}_pb2.py (single file, following Python protobuf convention)
//   - "cc" or default: Generates {baseName}.pb.h and {baseName}.pb.cc (two files)
//
// Parameters:
//   - m: The module being evaluated. Must have a valid "name" property
//     and at least one source file in "srcs".
//   - ctx: The rule rendering context (currently unused, but required by interface).
//
// Returns:
//   - A slice of expected output file paths. Returns nil if the module
//     has no name or no source files.
//
// Edge cases:
//   - Returns nil if module name is empty (invalid module).
//   - Returns nil if no source files are specified in "srcs".
//   - Only the first source file is used for base name calculation.
//   - C++ output (default) produces two files: .pb.h and .pb.cc.
//   - The ".proto" extension is stripped from the source file to get the base name.
//
// Key design decisions:
//   - Using only the first source file for naming simplifies the common case
//     of single .proto file modules. For multiple files, consider splitting
//     into separate proto_library modules.
//   - Python uses "_pb2.py" suffix to match the protoc generator's convention
//     (the "2" indicates the proto2/proto3 syntax version compatibility).
func (r *protoLibraryRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 { // Validate module has name and source files
		return nil
	}
	// Get the base name from the first source file.
	// Only the first .proto file is used for output naming.
	src := srcs[0]
	baseName := strings.TrimSuffix(src, ".proto")

	// Check for "out" property to determine output type.
	// This property specifies the target language for code generation.
	outType := GetStringProp(m, "out")
	switch outType {
	case "go":
		// Go protobuf generates a single .pb.go file.
		return []string{baseName + ".pb.go"}
	case "java":
		// Java protobuf generates a single .java file.
		return []string{baseName + ".java"}
	case "py":
		// Python protobuf generates a single _pb2.py file.
		// The "_pb2" suffix is the standard convention from protoc.
		return []string{baseName + "_pb2.py"}
	default:
		// Default: C++ outputs.
		// C++ protobuf generates both a header (.pb.h) and implementation (.pb.cc).
		return []string{baseName + ".pb.h", baseName + ".pb.cc"}
	}
}

// NinjaEdge generates the ninja build edge for protocol buffer compilation.
//
// This method creates the build edge that invokes protoc with appropriate flags
// for the target language. It handles plugins, proto include paths, and
// generates build statements based on the output type.
//
// Algorithm:
//  1. Validate module has name and source files; return empty if missing
//  2. Retrieve "plugins" and "proto_paths" properties
//  3. Extract base name from the first .proto source file
//  4. Build the base protoc command string
//  5. Add --plugin flags for each specified protoc plugin
//  6. Set the "protoc" variable with the full command
//  7. Add "proto_path" variable with --proto_path flags for import resolution
//  8. Generate build edges based on output type (switch on "out" property)
//
// Parameters:
//   - m: The module being processed. Must have "name" and at least one
//     source file in "srcs".
//   - ctx: The rule rendering context (currently unused, but required by interface).
//
// Returns:
//   - A string containing the ninja build edge definition(s), or empty string
//     if the module is invalid (missing name or sources).
//
// Edge cases:
//   - Returns empty string if module name is empty (invalid module).
//   - Returns empty string if no source files are specified (nothing to compile).
//   - Multiple proto_paths become multiple --proto_path flags in the command.
//   - Each output file gets its own build statement (especially for C++ which has two outputs).
//   - Plugin paths are shell-escaped to handle spaces and special characters.
//   - Only the first source file is used for the build edge (srcs[0]).
//
// Key design decisions:
//   - The "protoc" variable is set in the build edge to allow per-module
//     customization of the protoc command (plugins, flags, etc.).
//   - The "proto_path" variable collects all import search paths for protoc.
//   - For C++ output, two separate build edges are generated (one for .pb.cc,
//     one for .pb.h), both depending on the same source file.
//   - The "out_type" variable is set in each build edge for potential use
//     in customizing the protoc command or for debugging.
//
// Note: There appears to be duplicate code in the current implementation
// (lines setting "protoc" and "proto_path" variables are written twice).
// This may be intentional for compatibility or a bug that should be addressed.
func (r *protoLibraryRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 { // Validate module has name and source files
		return ""
	}
	// Get plugins and proto_paths from module properties.
	// plugins: List of protoc plugins to use (e.g., "protoc-gen-go")
	// proto_paths: Include directories for proto import resolution
	plugins := GetListProp(m, "plugins")
	protoPaths := GetListProp(m, "proto_paths")
	src := srcs[0]
	baseName := strings.TrimSuffix(src, ".proto")
	var edges strings.Builder

	// Determine output type from "out" property.
	// This affects which build edges are generated.
	outType := GetStringProp(m, "out")

	// Build the base protoc command line.
	// Start with "protoc" and add plugin flags if specified.
	protocCmd := "protoc"
	if len(plugins) > 0 { // Add --plugin flags for each specified protoc plugin
		for _, plugin := range plugins {
			// Escape plugin path to handle spaces and special characters.
			protocCmd += fmt.Sprintf(" --plugin=%s", shellEscape(plugin))
		}
	}
	// Set the protoc command variable (first occurrence).
	edges.WriteString(fmt.Sprintf(" protoc = %s\n", protocCmd))

	// Add proto_path variable for import resolution.
	// Each proto_path becomes a --proto_path= flag for protoc.
	if len(protoPaths) > 0 { // Add --proto_path flags for each include directory
		edges.WriteString(fmt.Sprintf(" proto_path = --proto_path=%s", shellEscape(protoPaths[0])))
		for i := 1; i < len(protoPaths); i++ {
			edges.WriteString(fmt.Sprintf(" --proto_path=%s", shellEscape(protoPaths[i])))
		}
	}
	// Duplicate: Set the protoc command variable again (second occurrence).
	// This may be intentional or a bug in the original code.
	edges.WriteString(fmt.Sprintf("  protoc = %s\n", protocCmd))

	// Add proto_path variable if present (duplicate section).
	// This creates a second "proto_path" variable binding with different formatting.
	if len(protoPaths) > 0 { // Add duplicate proto_path variable with different formatting
		edges.WriteString(fmt.Sprintf("  proto_path = --proto_path=%s", protoPaths[0]))
		for i := 1; i < len(protoPaths); i++ {
			edges.WriteString(fmt.Sprintf(" --proto_path=%s", protoPaths[i]))
		}
		edges.WriteString("\n")
	}

	// Generate build edges based on output type.
	switch outType {
	case "go":
		// Go output: single .pb.go file.
		edges.WriteString(fmt.Sprintf("build %s.pb.go: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = go\n")
	case "java":
		// Java output: single .java file.
		edges.WriteString(fmt.Sprintf("build %s.java: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = java\n")
	case "py":
		// Python output: single _pb2.py file.
		edges.WriteString(fmt.Sprintf("build %s_pb2.py: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = py\n")
	default:
		// C++ output (default): two files (.pb.cc and .pb.h).
		// Generate separate build edges for each output file.
		edges.WriteString(fmt.Sprintf("build %s.pb.cc: proto_compile %s\n", baseName, src))
		edges.WriteString(fmt.Sprintf("build %s.pb.h: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = cc\n")
	}
	return edges.String()
}

// Desc returns a short description for proto_library build steps.
//
// This description is used in build output and error messages to identify
// that the build step is running the protoc compiler.
//
// Parameters:
//   - m: The module being built (unused for proto_library).
//   - srcFile: The source file being processed (unused for proto_library).
//
// Returns:
//   - "protoc": A short identifier for protocol buffer compilation steps.
func (r *protoLibraryRule) Desc(m *parser.Module, srcFile string) string {
	return "protoc"
}

// protoGenRule implements the BuildRule interface for generic protocol buffer code generation.
// It provides a simplified variant of proto compilation that produces a generic output artifact.
//
// This rule handles the "proto_gen" module type in Blueprint files.
// Unlike protoLibraryRule which generates language-specific code (Go, Java, Python, C++),
// protoGenRule produces a single generic output target ({name}_proto) that can be used
// as an intermediate artifact for further processing by downstream build rules.
//
// Use cases for proto_gen:
//   - When the exact output files are not known in advance
//   - When multiple downstream rules need to consume proto artifacts
//   - When custom post-processing of proto outputs is required
//   - As an intermediate step in a multi-stage build pipeline
//
// Module properties:
//   - name: Unique identifier for the module (required)
//   - srcs: Input .proto files (required, at least one)
//   - (Other properties from proto_library like "plugins", "proto_paths" could be added)
//
// The output artifact naming convention ({name}_proto) allows downstream rules
// to reference the proto generation output without knowing the specific output
// file names that protoc would generate for different languages.
//
// Example Blueprint usage:
//
//	proto_gen {
//	    name: "my_proto_gen",
//	    srcs: ["mymessage.proto"],
//	}
//
// Then in another module:
//
//	custom_rule {
//	    name: "process_proto",
//	    srcs: [":my_proto_gen"],
//	    cmd: "process $in > $out",
//	    outs: ["processed.txt"],
//	}
//
// Algorithm overview for NinjaEdge:
//  1. Validate module has name and source files
//  2. Get the generic output path ({name}_proto)
//  3. Generate build edge using the proto_gen rule
//
// Key design decisions:
//   - Producing a single generic output artifact simplifies dependency tracking
//     for downstream rules that don't need to know the exact proto output files.
//   - The generic output name ({name}_proto) uses a suffix that indicates
//     it's a proto-generated artifact, making it easy to identify in the build graph.
//   - This rule currently uses the same protoc command as protoLibraryRule
//     (--cpp_out), but the output is treated as a generic artifact.
//
// Edge cases:
//   - Returns empty string from NinjaEdge if name is empty (invalid module).
//   - Returns empty string if no source files are specified (nothing to compile).
//   - Output is a generic artifact ({name}_proto) for further processing.
//   - The output artifact is not language-specific; downstream rules must
//     know how to handle the actual protoc output format.
//   - Only the first output from r.Outputs() is used in the build edge.
type protoGenRule struct{}

// Name returns the module type name for this rule.
//
// Returns:
//   - "proto_gen": The identifier used in Blueprint files to define
//     generic proto generation modules. This is the string users write
//     as the module type (e.g., proto_gen { ... }).
func (r *protoGenRule) Name() string { return "proto_gen" }

// NinjaRule returns the ninja rule template for generic proto generation.
//
// The rule defines a template for running the protoc compiler to generate
// protocol buffer code. Currently configured for C++ output (--cpp_out),
// but the output is treated as a generic artifact rather than language-specific code.
//
// The rule template includes:
//   - command: Runs protoc with C++ output flags
//   - description: A human-readable description shown during the build
//
// Note: Unlike proto_library, this rule doesn't use the "out_type" variable
// since the output is a generic artifact, not language-specific code.
//
// Parameters:
//   - ctx: The rule rendering context (currently unused for proto_gen,
//     but required by the BuildRule interface).
//
// Returns:
//   - A string containing the ninja "rule" block definition.
//     The returned string includes trailing newlines for proper formatting.
func (r *protoGenRule) NinjaRule(ctx RuleRenderContext) string {

	return `rule proto_gen

 command = protoc --cpp_out=$out $in

 description = Generating proto files $in

`

}

// Outputs returns the generic proto output artifact for downstream consumption.
//
// Unlike protoLibraryRule which generates language-specific files (.pb.go, .java, etc.),
// this method returns a single generic artifact named {name}_proto.
// This allows downstream rules to depend on the proto generation output
// without needing to know the exact output file names.
//
// Parameters:
//   - m: The module being evaluated. Must have a valid "name" property.
//   - ctx: The rule rendering context (currently unused, but required by interface).
//
// Returns:
//   - A slice containing a single output path: {name}_proto.
//     Returns nil if the module has no name (invalid module).
//
// Edge cases:
//   - Returns nil if module name is empty (invalid module).
//   - The output artifact name uses "_proto" suffix to indicate it's a
//     proto-generated artifact, making it identifiable in the build graph.
//
// Key design decisions:
//   - Using a single generic output artifact simplifies dependency tracking
//     for downstream rules that consume proto outputs.
//   - The "_proto" suffix (rather than ".proto" extension) avoids confusion
//     with actual .proto source files and clearly indicates a generated artifact.
func (r *protoGenRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" { // Validate module has a name
		return nil
	}
	return []string{name + "_proto"}
}

// NinjaEdge generates the ninja build edge for generic proto generation.
//
// This method creates a build edge that compiles .proto files into a generic
// output artifact. The output is not language-specific; it's a single artifact
// that downstream rules can further process or depend on.
//
// Algorithm:
//  1. Validate module has name and source files; return empty if missing
//  2. Get the generic output artifact path from Outputs()
//  3. Generate a single build edge using the proto_gen rule
//  4. List all source files as inputs to the build edge
//
// Parameters:
//   - m: The module being processed. Must have "name" and at least one
//     source file in "srcs".
//   - ctx: The rule rendering context (currently unused, but required by interface).
//
// Returns:
//   - A string containing the ninja build edge definition, or empty string
//     if the module is invalid (missing name or sources).
//
// Edge cases:
//   - Returns empty string if module name is empty (invalid module).
//   - Returns empty string if no source files are specified (nothing to compile).
//   - Only the first output from Outputs() is used (should be the only output).
//   - All source files are listed as inputs to the build edge.
//   - Source files are joined with spaces for the ninja build edge syntax.
//
// Key design decisions:
//   - Using a simple build edge with all sources as inputs allows
//     protoc to process multiple .proto files that may have interdependencies.
//   - The generic output artifact ({name}_proto) is treated as a single file
//     by ninja, which may not accurately reflect the actual protoc output
//     (which could be multiple files for C++ output).
func (r *protoGenRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 { // Validate module has name and source files
		return ""
	}
	var edges strings.Builder
	// Get the generic output artifact from Outputs().
	// This should return a single element: {name}_proto.
	out := r.Outputs(m, ctx)[0]
	// Generate build edge: "build <output>: proto_gen <inputs>"
	// The rule name "proto_gen" must match the rule defined in NinjaRule.
	edges.WriteString(fmt.Sprintf("build %s: proto_gen %s\n", out, strings.Join(srcs, " ")))
	return edges.String()
}

// Desc returns a short description for proto_gen build steps.
//
// This description is used in build output and error messages to identify
// that the build step is running the protoc compiler for generic proto generation.
//
// Parameters:
//   - m: The module being built (unused for proto_gen).
//   - srcFile: The source file being processed (unused for proto_gen).
//
// Returns:
//   - "protoc": A short identifier for protocol buffer compilation steps.
//     Same as proto_library since both use the protoc compiler.
func (r *protoGenRule) Desc(m *parser.Module, srcFile string) string {
	return "protoc"
}
