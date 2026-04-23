// custom.go - Custom and proto rules for minibp
// This file implements custom build rules and protocol buffer rules.
//
// Algorithm overview:
//  1. custom_rule: Execute arbitrary commands with $in/$out substitution
//  2. proto_library: Generate language-specific code from .proto files
//  3. proto_gen: Generic proto generation artifact
//
// Module types:
//   - custom_rule: Executes an arbitrary command with specified inputs and outputs
//   - proto_library: Compiles .proto files to language-specific generated code
//   - proto_gen: A variant for proto file generation
//
// Custom rules allow flexible build steps by specifying:
//   - srcs: Input source files
//   - cmd: Shell command to execute ($in and $out are substituted)
//   - outs: Output files produced
//   - flags: Additional flags passed to the command
//
// Proto rules support:
//   - out: Output language (go, java, py, or cc for C++)
//   - plugins: protoc plugins to use
//   - proto_paths: Include paths for proto imports
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"strings"
)

// customRule implements a custom build rule.
// It executes an arbitrary shell command with input/output substitution.
// Variables $in and $out in the cmd are replaced with actual paths.
//
// Custom rules allow flexible build steps by specifying:
//   - srcs: Input source files
//   - cmd: Shell command to execute ($in and $out are substituted)
//   - outs: Output files produced
//   - flags: Additional flags passed to the command
//
// This rule type is used for the custom_rule module type, which provides
// a flexible way to invoke arbitrary build commands within the ninja
// build system. Users can specify any shell command and define inputs and
// outputs, making it suitable for code generation, script execution, or
// custom tool invocations that don't fit other rule types.
//
// Algorithm:
//  1. Get module name, sources, and command
//  2. Exit early if name or cmd is missing
//  3. Get outputs (explicit or default {name}.out)
//  4. Replace $in and $out with actual paths in command
//  5. Generate ninja edge with escaped paths
//
// Edge cases:
//   - Returns empty string if name or command is empty
//   - Returns empty string if no outputs defined
//   - $in can expand to multiple files (space-separated)
//   - Paths are escaped for ninja compatibility
type customRule struct{}

func (r *customRule) Name() string { return "custom_rule" }

func (r *customRule) NinjaRule(ctx RuleRenderContext) string {

	return `rule custom_command

 command = $command $in $flags

 description = Custom build $out

`

}

func (r *customRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	// Custom rules can have explicit outputs (outs property)
	outputs := GetListProp(m, "outs")
	if len(outputs) > 0 {
		return outputs
	}
	return []string{name + ".out"}
}

func (r *customRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {

	name := getName(m)

	srcs := getSrcs(m)

	command := GetStringProp(m, "cmd")

	if name == "" || command == "" {

		return ""

	}

	flags := GetStringProp(m, "flags")

	outs := r.Outputs(m, ctx)

	if len(outs) == 0 {

		return ""

	}

	out := outs[0]

	// Replace $out and $in with actual paths in command

	inStr := strings.Join(srcs, " ")

	actualCommand := strings.ReplaceAll(command, "$out", out)

	actualCommand = strings.ReplaceAll(actualCommand, "$in", inStr)

	// Escape paths for ninja build file

	// Spaces become $ ", $ " becomes $ ", etc.

	escapedOut := ninjaEscapePath(out)

	escapedSrcs := make([]string, len(srcs))

	for i, src := range srcs {

		escapedSrcs[i] = ninjaEscapePath(src)

	}

	var edges strings.Builder

	edges.WriteString(fmt.Sprintf("build %s: custom_command %s\n", escapedOut, strings.Join(escapedSrcs, " ")))

	edges.WriteString(fmt.Sprintf("  cmd = %s\n", actualCommand))

	if flags != "" {

		edges.WriteString(fmt.Sprintf("  flags = %s\n", flags))

	}

	edges.WriteString("\n")

	return edges.String()

}

func (r *customRule) Desc(m *parser.Module, srcFile string) string {
	return "custom"
}

// protoLibraryRule implements a protocol buffer compilation rule.
// It compiles .proto files to language-specific generated code.
//
// Proto library rules support the proto_library module type and generate
// code in various languages based on the "out" property:
//   - "go": Generates Go protocol buffer code (.pb.go)
//   - "java": Generates Java protocol buffer code (.java)
//   - "py": Generates Python protocol buffer code (_pb2.py)
//   - "cc" or default: Generates C++ header and implementation (.pb.h, .pb.cc)
//
// Additional properties:
//   - plugins: List of protoc plugins to use (e.g., "protoc-gen-go")
//   - proto_paths: Include paths for proto imports (--proto_path flags)
//
// This rule invokes the protoc compiler with appropriate flags for
// the target language and processes .proto source files.
//
// Algorithm:
//  1. Get module name and sources (.proto files)
//  2. Exit early if missing
//  3. Determine output type from "out" property
//  4. Generate corresponding output file extensions
//  5. Build protoc command with appropriate flags
//  6. Generate ninja edge for each output file
//
// Edge cases:
//   - Multiple output languages not supported in single module
//   - Default produces C++ outputs (.pb.h and .pb.cc)
//   - Proto paths are passed as --proto_path flags to protoc
type protoLibraryRule struct{}

func (r *protoLibraryRule) Name() string { return "proto_library" }

// NinjaRule returns the ninja rule template for protocol buffer compilation.
// Currently configured for C++ output by default; other languages
// require custom rule definitions.
func (r *protoLibraryRule) NinjaRule(ctx RuleRenderContext) string {

	return `rule proto_compile

 command = protoc --cpp_out=$out $in

 description = Compiling proto $in

`

}

// Outputs returns the output files for proto compilation based on the "out" property.
// Multiple output files can be generated depending on language:
//
//   - "go": {basename}.pb.go
//   - "java": {basename}.java
//   - "py": {basename}_pb2.py
//   - default/cc: {basename}.pb.h, {basename}.pb.cc
//
// Parameters:
//   - m: The module being evaluated
//   - ctx: Rule rendering context
//
// Returns:
//   - List of generated source file paths
func (r *protoLibraryRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return nil
	}
	// Get the base name from the first source file
	src := srcs[0]
	baseName := strings.TrimSuffix(src, ".proto")

	// Check for out property to determine output type
	outType := GetStringProp(m, "out")
	switch outType {
	case "go":
		return []string{baseName + ".pb.go"}
	case "java":
		return []string{baseName + ".java"}
	case "py":
		return []string{baseName + "_pb2.py"}
	default:
		// Default: C++ outputs
		return []string{baseName + ".pb.h", baseName + ".pb.cc"}
	}
}

// NinjaEdge generates the ninja build edge for proto compilation.
//
// Algorithm:
//  1. Get module name and sources
//  2. Exit early if missing
//  3. Get plugins and proto_paths properties
//  4. Build protoc command with plugin flags
//  5. Add proto_path variables for each search path
//  6. Generate build statements based on output type
//
// Edge cases:
//   - Returns empty string if name or sources missing
//   - Multiple proto_paths become multiple --proto_path flags
//   - Each output file gets its own build statement
func (r *protoLibraryRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	// Get plugins and proto_paths
	plugins := GetListProp(m, "plugins")
	protoPaths := GetListProp(m, "proto_paths")
	src := srcs[0]
	baseName := strings.TrimSuffix(src, ".proto")
	var edges strings.Builder

	// Determine output type and generate appropriate command
	outType := GetStringProp(m, "out")

	// Build protoc command line
	protocCmd := "protoc"
	if len(plugins) > 0 {
		for _, plugin := range plugins {
			protocCmd += fmt.Sprintf(" --plugin=%s", plugin)
		}
	}
	edges.WriteString(fmt.Sprintf("  protoc = %s\n", protocCmd))

	// Add proto_path variable if present
	if len(protoPaths) > 0 {
		edges.WriteString(fmt.Sprintf("  proto_path = --proto_path=%s", protoPaths[0]))
		for i := 1; i < len(protoPaths); i++ {
			edges.WriteString(fmt.Sprintf(" --proto_path=%s", protoPaths[i]))
		}
		edges.WriteString("\n")
	}

	switch outType {
	case "go":
		edges.WriteString(fmt.Sprintf("build %s.pb.go: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = go\n")
	case "java":
		edges.WriteString(fmt.Sprintf("build %s.java: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = java\n")
	case "py":
		edges.WriteString(fmt.Sprintf("build %s_pb2.py: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = py\n")
	default:
		// C++ output
		edges.WriteString(fmt.Sprintf("build %s.pb.cc: proto_compile %s\n", baseName, src))
		edges.WriteString(fmt.Sprintf("build %s.pb.h: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = cc\n")
	}
	return edges.String()
}

func (r *protoLibraryRule) Desc(m *parser.Module, srcFile string) string {
	return "protoc"
}

// protoGenRule implements a custom proto code generator.
// It provides a variant for proto file generation with customizable output.
//
// This rule implements the proto_gen module type, which is a flexible variant
// of proto compilation. Unlike protoLibraryRule, protoGenRule produces
// a generic output target that can be further customized. It uses the
// protoc compiler to generate code but wraps the output in a generic
// artifact name.
//
// The rule takes .proto files as sources and produces a single output
// artifact with "_proto" suffix. This allows downstream rules to depend on
// the generated proto artifacts without requiring knowledge of the
// specific output languages or files.
//
// Algorithm:
//  1. Get module name and sources
//  2. Exit early if missing
//  3. Generate output as {name}_proto
//  4. Generate ninja edge with proto_gen rule
//
// Edge cases:
//   - Returns empty string if name or sources missing
//   - Output is a generic artifact for further processing
type protoGenRule struct{}

func (r *protoGenRule) Name() string { return "proto_gen" }

// NinjaRule returns the ninja rule template for generic proto generation.
func (r *protoGenRule) NinjaRule(ctx RuleRenderContext) string {

	return `rule proto_gen

 command = protoc --cpp_out=$out $in

 description = Generating proto files $in

`

}

// Outputs returns the generic proto output artifact.
// Output is {name}_proto for downstream consumption.
func (r *protoGenRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + "_proto"}
}

// NinjaEdge generates the ninja build edge for proto generation.
//
// Edge cases:
//   - Returns empty string if name or sources missing
func (r *protoGenRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	var edges strings.Builder
	out := r.Outputs(m, ctx)[0]
	edges.WriteString(fmt.Sprintf("build %s: proto_gen %s\n", out, strings.Join(srcs, " ")))
	return edges.String()
}

func (r *protoGenRule) Desc(m *parser.Module, srcFile string) string {
	return "protoc"
}
