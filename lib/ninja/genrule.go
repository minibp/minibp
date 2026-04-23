// genrule.go - Genrule build rule for minibp
// This file implements a generic rule that executes an arbitrary command.
// Genrule is useful for code generation, script execution, or custom build steps.
//
// Algorithm overview:
//  1. Parse module properties (cmd, srcs, outs, tool_files)
//  2. Generate ninja rule with arbitrary shell command
//  3. Handle input/output path substitution
//  4. Include additional dependencies (tool_files, deps, data)
//
// The genrule module type accepts:
//   - srcs: Source files that the command reads
//   - cmd: The command to execute
//   - outs: Output files produced by the command
//   - tool_files: Additional tools needed to run the command
//   - deps: Additional dependencies
//   - data: Data files needed at runtime
//
// Edge cases:
//   - If "outs" property is specified, use those as outputs
//   - Otherwise, generate default output as {name}.out
//   - tool_files and deps are order-only dependencies (|)
//   - data files are regular dependencies
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"strings"
)

// genrule is a generic build rule that executes arbitrary commands.
// It provides flexibility for custom build tasks such as code generation,
// script execution, or running external tools within the build pipeline.
// This implementation mirrors Bazel's genrule functionality.
//
// Genrules are powerful but should be used sparingly. They:
//   - Hide dependencies from the build system
//   - Can produce non-deterministic outputs
//   - May not support incremental builds well
//
// Best practices:
//   - Use specific rule types when available (cc_library, java_library)
//   - Use genrule only for truly custom build steps
//   - Ensure command produces deterministic outputs
type genrule struct{}

// Name returns the module type name for genrule.
// This name is used to identify genrule modules in the build system.
func (r *genrule) Name() string { return "genrule" }

// NinjaRule returns the ninja rule template for genrule command execution.
// The template defines a generic rule that runs an arbitrary shell command
// with the command string passed via the $cmd variable.
//
// The rule uses:
//   - command: $cmd (the actual command to execute)
//   - description: Shows output files being generated
//   - restat: 1 (don't rebuild if only mtime changed)
func (r *genrule) NinjaRule(ctx RuleRenderContext) string {
	return `rule genrule_command
 command = $cmd
 description = Genrule $out
 restat = 1
`
}

// Outputs returns the output files for the genrule module.
// If "outs" property is specified, those files are used as outputs.
// Otherwise, a default output is generated as {name}.out.
//
// Parameters:
//   - m: The module being evaluated
//   - ctx: Rule rendering context with architecture and toolchain info
//
// Returns:
//   - List of output file paths
func (r *genrule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	outs := GetListProp(m, "outs")
	if len(outs) > 0 {
		return outs
	}
	// Default output if no outs specified.
	return []string{name + ".out"}
}

// NinjaEdge generates the ninja build edge for genrule execution.
// It combines:
//   - Source files (srcs)
//   - Tool files and deps (order-only dependencies)
//   - The command to execute
//   - Output files
//
// Algorithm:
//  1. Get module name, sources, and command
//  2. Exit early if name or cmd is missing
//  3. Get outputs, failing if none
//  4. Collect all dependencies (deps + tool_files + data)
//  5. Build edge with sources and order-only deps
//  6. Add command to edge
//
// Edge cases:
//   - Returns empty string if name or cmd is empty
//   - Returns empty string if no outputs defined
//   - Dependencies are split: srcs are regular deps, others are order-only
func (r *genrule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	cmd := GetStringProp(m, "cmd")
	if name == "" || cmd == "" {
		return ""
	}

	outs := r.Outputs(m, ctx)
	if len(outs) == 0 {
		return ""
	}

	// Collect all dependencies: explicit deps, tool files, and runtime data.
	// tool_files and deps are order-only (|), data is regular.
	toolFiles := GetListProp(m, "tool_files")
	deps := GetListProp(m, "deps")
	data := getData(m)

	var allDeps []string
	allDeps = append(allDeps, deps...)
	allDeps = append(allDeps, toolFiles...)
	allDeps = append(allDeps, data...)

	// Build the ninja edge statement.
	var edges strings.Builder
	escapedOuts := make([]string, 0, len(outs))
	for _, out := range outs {
		escapedOuts = append(escapedOuts, ninjaEscapePath(out))
	}
	edges.WriteString(fmt.Sprintf("build %s: genrule_command %s", strings.Join(escapedOuts, " "), strings.Join(srcs, " ")))
	// Add order-only dependencies (tool_files, deps).
	if len(allDeps) > 0 {
		edges.WriteString(fmt.Sprintf(" | %s", strings.Join(allDeps, " ")))
	}
	edges.WriteString("\n")
	edges.WriteString(fmt.Sprintf(" cmd = %s\n", cmd))
	return edges.String()
}

// Desc returns a short description string for logging purposes.
func (r *genrule) Desc(m *parser.Module, srcFile string) string {
	return "genrule"
}
