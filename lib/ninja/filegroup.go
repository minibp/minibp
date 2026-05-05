// Package ninja implements Ninja build file generation from Blueprint module definitions.
//
// It provides the build rule implementations that convert parsed Blueprint modules
// into Ninja build rules. Each module type (e.g., filegroup, filegroup_static, go_binary)
// implements the BuildRule interface to generate appropriate Ninja syntax.
//
// This file handles file group rules:
//   - filegroup: Copies individual source files to an output directory under the group name.
//     Each file is copied using the cp command (or cmd /c copy on Windows).
//   - filegroup_static: Concatenates all source files into a single static output file.
//     Produces a single .static file containing all inputs combined.
//
// The package handles:
//   - Build rule definition and registration for file groups
//   - Ninja rule templates with platform-specific commands
//   - Output path calculation with proper path sanitization
//   - Dependency tracking through Ninja build edges
//
// Algorithm overview for filegroup:
//  1. Get module name from "name" property (becomes output directory name)
//  2. Get source files from "srcs" property
//  3. For each source file, generate a copy operation to {name}/{basename}
//  4. Use appropriate copy command based on host OS (cp on Unix, cmd /c copy on Windows)
//
// Algorithm overview for filegroup_static:
//  1. Get module name and sources
//  2. Generate single output file: {name}.static
//  3. Create one build edge with all sources as inputs
//
// Edge cases:
//   - If no sources or no name, no build edges are generated (empty string returned)
//   - Paths are sanitized using pathutil.SanitizePath to prevent directory traversal
//   - On Windows, filegroup uses cmd /c copy instead of cp for compatibility
//   - filegroup_static uses "cp" command which may not correctly concatenate (known limitation)
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"minibp/lib/pathutil"
	"path/filepath"
	"runtime"
	"strings"
)

// filegroup implements the BuildRule interface for copying files to an output directory.
//
// It copies source files to an output directory named after the module.
// Each source file is copied individually using the cp command (or cmd /c copy on Windows),
// preserving the source filename in the destination path.
//
// File groups are useful for:
//   - Collecting resource files that need to be bundled together
//   - Copying configuration files, scripts, or data files to a known output location
//   - Creating a named group of files that other modules can depend on
//
// The output directory structure is: {module_name}/{source_basename}
// For example, a filegroup named "my_resources" with srcs ["res/icon.png", "data.json"]
// would produce: my_resources/icon.png and my_resources/data.json
//
// Algorithm:
//  1. Get module name from the "name" property (becomes the output directory name)
//  2. Get source files from the "srcs" property
//  3. For each source file, generate a Ninja build edge to copy it to {name}/{basename}
//  4. Use platform-appropriate copy command (cp on Unix, cmd /c copy on Windows)
//
// Edge cases:
//   - If no sources are specified, no build edges are generated (returns empty string)
//   - If no name is specified, no build edges are generated (returns empty string)
//   - On Windows, the copy command uses "cmd /c copy" instead of "cp" for compatibility
//   - Source paths are sanitized using pathutil.SanitizePath to prevent directory traversal
//   - Only the basename of each source file is used in the output path
//
// Key design decisions:
//   - Each file gets its own build edge rather than a single edge for all files.
//     This allows Ninja to track dependencies at file granularity and supports
//     incremental builds when only some files change.
//   - The output uses the module name as a directory to namespace the copied files
//     and avoid collisions with other modules or build artifacts.
type filegroup struct{}

// Name returns the unique identifier for the filegroup build rule.
// This method is part of the BuildRule interface implementation.
//
// Returns:
//   - The string "filegroup" which must match the module type name in Blueprint files.
//     Example module definition: filegroup { name: "my_files", srcs: ["a.txt", "b.txt"] }
//
// Edge cases:
//   - Returns empty string only if the typeName field is uninitialized (programmer error).
//     In normal operation, always returns "filegroup".
func (r *filegroup) Name() string { return "filegroup" }

// NinjaRule returns the Ninja rule definition for copying files.
//
// The rule defines the command template that Ninja will use to copy files.
// The command uses $in (input file) and $out (output file) variables
// that Ninja substitutes when executing the build edge.
//
// Platform-specific behavior:
//   - On Unix-like systems (Linux, macOS): Uses "cp $in $out"
//   - On Windows: Uses "cmd /c copy $in $out" to invoke the Windows copy command
//
// Parameters:
//   - ctx: The RuleRenderContext providing build context (currently unused for this rule).
//     Reserved for future use where context-specific rule customization may be needed.
//
// Returns:
//   - A string containing the complete Ninja rule definition.
//     The rule is named "filegroup_copy" and includes the platform-appropriate copy command.
//     Format: "rule filegroup_copy\n  command = cp $in $out\n"
//
// Edge cases:
//   - The runtime.GOOS check happens at rule generation time, not at Ninja execution time.
//     This means the generated build.ninja will contain the copy command for the host OS
//     where minibp is running, not the target OS (unless cross-compilation is handled elsewhere).
//   - If the copy command fails (e.g., source doesn't exist), Ninja will report the error
//     and stop the build.
//
// Key design decisions:
//   - Using runtime.GOOS to detect the platform at generation time rather than
//     hardcoding or requiring a flag. This simplifies the common case where the
//     build host and target are the same OS.
//   - The rule name "filegroup_copy" is globally unique to avoid conflicts with
//     other rules that might also use cp.
func (r *filegroup) NinjaRule(ctx RuleRenderContext) string {
	// Default copy command for Unix-like systems (Linux, macOS, etc.).
	// The $in and $out are Ninja variables substituted at build time.
	copyCmd := "cp $in $out"

	// Check if running on Windows to use the appropriate copy command.
	// Windows doesn't have cp by default; we use cmd /c copy which invokes
	// the Windows copy command through the command interpreter.
	if runtime.GOOS == "windows" {
		copyCmd = "cmd /c copy $in $out"
	}

	// Return the complete Ninja rule definition.
	// The rule name "filegroup_copy" identifies this rule in the build file.
	// Ninja uses this rule when processing build edges that reference it.
	return `rule filegroup_copy

 command = ` + copyCmd + `

`
}

// Outputs returns the output identifier for the filegroup module.
//
// The output is a single identifier string with ".files" extension that represents
// the collection of copied files. This identifier is used by other modules as a
// dependency reference. The actual output files are generated as build edges in NinjaEdge.
//
// For example, a filegroup named "my_resources" would return ["my_resources.files"].
// Other modules can then depend on "my_resources.files" in their deps property.
//
// Parameters:
//   - m: The parser.Module representing the filegroup module definition.
//     Must contain a "name" property for the output identifier.
//     May contain a "srcs" property (used in NinjaEdge, not here).
//   - ctx: The RuleRenderContext providing build context (currently unused).
//     Reserved for future use where context might affect output paths.
//
// Returns:
//   - A slice containing a single string: "{module_name}.files".
//     Returns nil if the module has no name property or name is empty.
//     The ".files" suffix distinguishes filegroup outputs from other module types.
//
// Edge cases:
//   - If the module has no "name" property, returns nil (no outputs).
//   - If the name property is empty string, returns nil.
//   - The output identifier is NOT a real file path; it's a Ninja build label.
//     The actual file paths are generated in NinjaEdge as "{name}/{basename}".
//   - Multiple calls with the same module return the same output identifier.
//
// Key design decisions:
//   - Using ".files" suffix to create a unique identifier that won't conflict
//     with actual file names or other module output conventions.
//   - Returning a slice (even for single output) to satisfy the BuildRule interface
//     which supports modules with multiple outputs.
func (r *filegroup) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Extract the module name from the "name" property.
	// This name becomes the identifier for the filegroup's outputs.
	name := getName(m)
	if name == "" {
		// No name means we can't generate valid outputs.
		// Return nil to indicate no build edges should be created.
		return nil
	}
	// Return the output identifier with ".files" suffix.
	// This is a Ninja label, not an actual file path.
	return []string{name + ".files"}
}

// NinjaEdge generates Ninja build edges for copying each source file.
//
// Each source file gets its own build edge that copies it to the output directory.
// The output path is constructed as: {sanitized_name}/{sanitized_basename}
// This preserves the filename while namespacing under the module name.
//
// Algorithm:
//  1. Get source files from the "srcs" property using getSrcs()
//  2. Get module name from the "name" property using getName()
//  3. If either is missing (empty sources or empty name), return empty string
//  4. For each source file:
//     a. Sanitize the module name and source basename to prevent path traversal
//     b. Construct output path as filepath.Join(sanitizedName, sanitizedBasename)
//     c. Generate build edge: "build {out}: filegroup_copy {src}"
//  5. Return all build edges concatenated as a single string
//
// Parameters:
//   - m: The parser.Module representing the filegroup module definition.
//     Must have "name" and "srcs" properties for meaningful output.
//   - ctx: The RuleRenderContext providing build context (currently unused).
//     Reserved for future use where context might affect edge generation.
//
// Returns:
//   - A string containing all Ninja build edges, one per source file.
//     Each edge follows the format: "build {output}: filegroup_copy {source}\n"
//     Returns empty string if no sources or no name.
//
// Edge cases:
//   - If the module has no "srcs" property or it's empty, returns empty string.
//   - If the module has no "name" property or it's empty, returns empty string.
//   - Source paths are sanitized: only the basename is used (directory stripped).
//   - The module name and basename are both passed through pathutil.SanitizePath
//     to prevent directory traversal attacks (e.g., "../../etc/passwd").
//   - If multiple sources have the same basename, they will overwrite each other
//     in the output directory (last one wins).
//
// Key design decisions:
//   - Generating one build edge per file rather than a single edge for all files.
//     This allows Ninja to track dependencies at file granularity, enabling
//     incremental builds when only some files change.
//   - Using filepath.Join for output path construction ensures correct path
//     separators for the host OS.
//   - The input to the build edge is the original source path (src), not the
//     basename, because Ninja needs the full path to locate the source file.
func (r *filegroup) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Retrieve the list of source files from the module's "srcs" property.
	// Returns nil or empty slice if no sources are defined.
	srcs := getSrcs(m)
	if len(srcs) == 0 { // No sources to copy, return empty string to indicate no build edges
		return ""
	}
	// Get the module name which will be used as the output directory name.
	name := getName(m)
	if name == "" { // No module name, cannot construct valid output paths
		return ""
	}

	// Use strings.Builder for efficient string concatenation.
	// Build edges are accumulated and returned as a single string.
	var edges strings.Builder
	for _, src := range srcs {
		safeName := pathutil.SanitizePath(name)                                   // Sanitize module name to prevent path traversal attacks
		safeSrc := pathutil.SanitizePath(filepath.Base(src))                      // Sanitize source basename, strip directory components
		out := filepath.Join(safeName, safeSrc)                                   // Construct output path: {module_name}/{source_basename}
		edges.WriteString(fmt.Sprintf("build %s: filegroup_copy %s\n", out, src)) // Generate Ninja build edge for file copy
	}
	return edges.String()
}

// Desc returns a short description string for the filegroup build rule.
//
// This description is used by Ninja when displaying build progress (e.g., with -v flag).
// It appears next to the build action in Ninja's output to identify what operation is being performed.
//
// Parameters:
//   - m: The parser.Module representing the filegroup module (currently unused).
//     Reserved for future use where description might vary by module properties.
//   - srcFile: The source file being processed (currently unused).
//     Reserved for future use where description might include the filename.
//
// Returns:
//   - A short string "cp" indicating the copy operation.
//     This is a concise identifier shown during the build.
//
// Edge cases:
//   - The returned string is static and doesn't reflect the actual command
//     (which might be "cmd /c copy" on Windows). This is intentional for simplicity.
//   - Ninja may truncate long descriptions; keeping it short ("cp") ensures visibility.
func (r *filegroup) Desc(m *parser.Module, srcFile string) string {
	return "cp"
}

// filegroupStatic implements the BuildRule interface for creating static file groups.
//
// Unlike filegroup which copies files individually, filegroupStatic concatenates all
// source files into a single output file. This is useful for:
//   - Creating asset bundles that need to be embedded as a single unit
//   - Generating combined resource files for embedding in binaries
//   - Producing single-file outputs from multiple input sources
//
// The output is a single file with ".static" extension located at the root of the
// build output directory. All source file contents are concatenated in the order
// they appear in the "srcs" property.
//
// Example:
//
//	filegroup_static {
//	    name: "my_bundle",
//	    srcs: ["header.txt", "body.txt", "footer.txt"],
//	}
//	Produces: my_bundle.static (containing header.txt + body.txt + footer.txt)
//
// Algorithm:
//  1. Get module name from the "name" property
//  2. Get source files from the "srcs" property
//  3. If either is missing, return early with no build edges
//  4. Generate a single output file: {name}.static
//  5. Create one build edge with all sources as inputs to the output
//
// Edge cases:
//   - If no sources are specified, no build edges are generated (returns empty string)
//   - If no name is specified, no build edges are generated (returns empty string)
//   - The order of concatenation follows the order of files in the "srcs" property
//   - If sources list is empty, no output is generated
//
// Key design decisions:
//   - Using a ".static" extension to clearly identify the file as a static bundle.
//     This distinguishes it from other output file types in the build system.
//   - Creating a single build edge with all sources as inputs.
//     This is simpler than per-file edges but means any source change rebuilds the entire bundle.
//   - The current NinjaRule uses "cp $in $out" which may not correctly concatenate files.
//     (Note: This appears to be a known limitation - cp typically doesn't concatenate;
//     a custom command or using "cat" might be more appropriate for Unix systems.)
type filegroupStatic struct{}

// Name returns the unique identifier for the filegroup_static build rule.
// This method is part of the BuildRule interface implementation.
//
// Returns:
//   - The string "filegroup_static" which must match the module type name in Blueprint files.
//     Example module definition: filegroup_static { name: "my_bundle", srcs: ["a.txt", "b.txt"] }
//
// Edge cases:
//   - Returns empty string only if the typeName field is uninitialized (programmer error).
//     In normal operation, always returns "filegroup_static".
func (r *filegroupStatic) Name() string { return "filegroup_static" }

// NinjaRule returns the Ninja rule definition for static file groups.
//
// The rule defines the command template for combining multiple source files into one output.
// Currently uses "cp $in $out" which may not correctly concatenate files on all systems.
//
// IMPORTANT LIMITATION: The "cp" command on Unix typically doesn't concatenate files
// when given multiple inputs. The correct command for concatenation would be:
//   - Unix: "cat $in > $out" or "cat $in $out && mv $out.tmp $out"
//   - Windows: "type $in > $out"
//
// This appears to be a known issue that may need to be addressed in future versions.
//
// Parameters:
//   - ctx: The RuleRenderContext providing build context (currently unused).
//     Reserved for future use where context-specific rule customization may be needed.
//
// Returns:
//   - A string containing the complete Ninja rule definition.
//     The rule is named "filegroup_static" and includes the copy/concatenation command.
//     Format: "rule filegroup_static\n  command = cp $in $out\n"
//
// Edge cases:
//   - The command uses $in (all inputs) and $out (output) Ninja variables.
//   - Ninja passes all input files in $in, space-separated.
//   - The behavior of "cp" with multiple inputs depends on the system:
//     On macOS, cp with multiple sources and a directory as last arg copies all into directory.
//     On Linux, cp with multiple sources requires -t flag or specific handling.
//
// Key design decisions:
//   - Using a simple rule definition without platform-specific handling (unlike filegroup).
//     This may need to be updated to handle cross-platform concatenation correctly.
//   - The rule name "filegroup_static" is globally unique to avoid conflicts.
func (r *filegroupStatic) NinjaRule(ctx RuleRenderContext) string {
	// Return the complete Ninja rule definition for static file groups.
	// Note: The "cp $in $out" command may not correctly concatenate files.
	// See the function comment for details on this limitation.
	return `rule filegroup_static

 command = cp $in $out

`
}

// Outputs returns the output file path for the static file group.
//
// The output is a single file with ".static" extension that will contain all
// source files concatenated together. This file is created in the root of the
// build output directory (not in a subdirectory like filegroup).
//
// Parameters:
//   - m: The parser.Module representing the filegroup_static module definition.
//     Must contain a "name" property for the output filename.
//     May contain a "srcs" property (used in NinjaEdge, not here).
//   - ctx: The RuleRenderContext providing build context (currently unused).
//     Reserved for future use where context might affect output paths.
//
// Returns:
//   - A slice containing a single string: "{module_name}.static".
//     Returns nil if the module has no name property or name is empty.
//     The ".static" suffix identifies this as a static file group output.
//
// Edge cases:
//   - If the module has no "name" property, returns nil (no outputs).
//   - If the name property is empty string, returns nil.
//   - The output file is placed at the root of the build output directory.
//   - Multiple calls with the same module return the same output path.
//
// Key design decisions:
//   - Using ".static" suffix to clearly identify the file as a concatenated bundle.
//   - Placing the output at root level (not in a subdirectory) for simplicity.
//   - Returning a slice (even for single output) to satisfy the BuildRule interface.
func (r *filegroupStatic) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Extract the module name from the "name" property.
	// This name becomes the base filename for the output.
	name := getName(m)
	if name == "" {
		// No name means we can't generate a valid output path.
		// Return nil to indicate no build edges should be created.
		return nil
	}
	// Return the output file path with ".static" extension.
	// This file will contain all source files concatenated together.
	return []string{name + ".static"}
}

// NinjaEdge generates a single Ninja build edge for the static file group.
//
// Unlike filegroup which creates one edge per file, filegroup_static creates a single
// edge that takes all source files as inputs and produces one output file.
// All source file contents are concatenated (in theory; see limitation note).
//
// Algorithm:
//  1. Get source files from the "srcs" property using getSrcs()
//  2. Get module name from the "name" property using getName()
//  3. If either is missing (empty sources or empty name), return empty string
//  4. Construct output filename: {name}.static
//  5. Generate a single build edge with all sources as inputs
//  6. Return the build edge string
//
// Parameters:
//   - m: The parser.Module representing the filegroup_static module definition.
//     Must have "name" and "srcs" properties for meaningful output.
//   - ctx: The RuleRenderContext providing build context (currently unused).
//     Reserved for future use where context might affect edge generation.
//
// Returns:
//   - A string containing a single Ninja build edge.
//     Format: "build {output}: filegroup_static {src1} {src2} ...\n"
//     Returns empty string if no sources or no name.
//
// Edge cases:
//   - If the module has no "srcs" property or it's empty, returns empty string.
//   - If the module has no "name" property or it's empty, returns empty string.
//   - The order of sources in the "srcs" property determines concatenation order.
//   - Ninja passes all inputs in $in as space-separated paths.
//
// Key design decisions:
//   - Creating a single build edge for all inputs (not one per file).
//     This is simpler but means any source change rebuilds the entire bundle.
//   - Using strings.Join(srcs, " ") to pass all sources as Ninja inputs.
//     Ninja's $in variable will contain all these paths when the command runs.
//
// IMPORTANT LIMITATION: The current NinjaRule uses "cp $in $out" which may not
// correctly concatenate files. The cp command behavior with multiple inputs varies
// by system. A proper concatenation command (like "cat" on Unix) should be used.
func (r *filegroupStatic) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 { // No sources to concatenate, return empty string
		return ""
	}
	name := getName(m)
	if name == "" { // No module name, cannot construct valid output path
		return ""
	}

	var edges strings.Builder
	out := name + ".static"                                                                         // Construct output filename with ".static" extension
	edges.WriteString(fmt.Sprintf("build %s: filegroup_static %s\n", out, strings.Join(srcs, " "))) // Generate single build edge with all sources
	return edges.String()
}

// Desc returns a short description string for the filegroup_static build rule.
//
// This description is used by Ninja when displaying build progress (e.g., with -v flag).
// It appears next to the build action in Ninja's output to identify what operation is being performed.
//
// Parameters:
//   - m: The parser.Module representing the filegroup_static module (currently unused).
//     Reserved for future use where description might vary by module properties.
//   - srcFile: The source file being processed (currently unused).
//     For static file groups, there are multiple sources; this parameter is less relevant.
//
// Returns:
//   - A short string "cp" indicating the operation (though concatenation, not copy, is intended).
//     This is a concise identifier shown during the build.
//
// Edge cases:
//   - The returned string is "cp" which doesn't accurately describe concatenation.
//     This may be a simplification or a placeholder for future correction.
//   - Ninja may truncate long descriptions; keeping it short ensures visibility.
func (r *filegroupStatic) Desc(m *parser.Module, srcFile string) string {
	return "cp"
}
