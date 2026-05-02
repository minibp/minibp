// Package ninja implements Ninja build file generation for pre-built artifacts.
//
// This file provides rules for using pre-built binaries and libraries
// in the Ninja build system. Prebuilt modules allow referencing
// already-compiled artifacts without rebuilding them from source.
//
// Supported module types:
//   - prebuilt_etc: Installs files to system directories (/etc, /usr/share, /firmware)
//   - prebuilt_binary: Pre-built executable binaries with execute permissions
//   - prebuilt_library: Pre-built libraries (.a for static, .so for shared)
//   - cc_prebuilt_binary: C/C++ pre-built binary (alias for prebuilt_binary)
//   - cc_prebuilt_library: C/C++ pre-built static library
//   - cc_prebuilt_library_shared: C/C++ pre-built shared library
//
// Each prebuilt module type implements the BuildRule interface:
//   - Name() string: Returns the module type name
//   - NinjaRule(ctx) string: Returns ninja rule definitions
//   - Outputs(m, ctx) []string: Returns output file paths
//   - NinjaEdge(m, ctx) string: Returns ninja build edges
//   - Desc(m, src) string: Returns a short description for build logging
//
// Algorithm overview:
//  1. Parse module properties (src, filename/stem for output naming)
//  2. Generate copy rule to install prebuilt files
//  3. Handle architecture suffixes for multi-arch support
//  4. Set output path based on module type and subdir property
//
// Output naming logic:
//   - prebuilt_etc: Uses "filename" property, or base name of source file
//   - prebuilt_binary: Uses "stem" property, or module name
//   - prebuilt_library: Uses "stem" property, or "lib" + module name
//
// Edge cases:
//   - Missing source file returns empty outputs (no build edge generated)
//   - Path components are sanitized to prevent directory traversal
//   - Architecture suffix is appended for multi-arch builds
//   - Forward slashes are used in output paths for Ninja compatibility
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"minibp/lib/pathutil"
	"path/filepath"
	"strings"
)

// sanitizePathComponent sanitizes a string to be safe for use as a path component.
//
// It replaces path separator characters ('/' and '\') with underscores
// to prevent directory traversal attacks and ensure the string can be
// safely used as a filename or directory name component.
//
// This function is used to sanitize user-provided properties like "filename"
// and "stem" before using them in output paths. Without this sanitization,
// a malicious or accidental path like "../../etc/passwd" could escape
// the intended output directory.
//
// Parameters:
//   - s: The input string to sanitize (typically a filename or stem property).
//     Can be empty, in which case an empty string is returned.
//
// Returns:
//   - The sanitized string with path separators replaced by underscores.
//     All other characters are preserved as-is.
//
// Edge cases:
//   - Empty string returns empty string.
//   - String with no path separators is returned unchanged.
//   - Only '/' and '\' are replaced; other special characters are preserved.
//   - The function does not collapse multiple separators or trim whitespace.
//
// Key design decisions:
//   - Using underscores instead of removing separators preserves the original
//     string length and makes the transformation reversible for debugging.
//   - Only replacing '/' and '\' (not all special characters) because the
//     primary goal is preventing path traversal, not general sanitization.
func sanitizePathComponent(s string) string {
	// Map through each rune in the string, replacing path separators with underscores.
	// strings.Map applies the function to each rune and returns a new string.
	return strings.Map(func(r rune) rune {
		// Replace forward slash and backslash with underscore to prevent path traversal.
		if r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, s)
}

// prebuiltEtcRule implements BuildRule for installing pre-built files to system directories.
//
// This rule handles the "prebuilt_etc" module type, which installs configuration files,
// firmware, or other data files to system directories like /etc, /usr/share, or /firmware.
// The actual installation path is determined by the subdir field.
//
// Supported installation paths (configured via subdir):
//   - "etc" for /etc (configuration files)
//   - "usr_share" for /usr/share (architecture-independent data)
//   - "firmware" for /firmware (device firmware)
//   - "root" for / (root filesystem)
//
// The generated Ninja rule uses a simple copy command to place the file in the
// correct system directory. No compilation or linking is performed.
//
// Fields:
//   - typeName: The module type name as it appears in .bp files (e.g., "prebuilt_etc").
//     This is returned by the Name() method and used for module type registration.
//   - subdir: The installation subdirectory relative to the system root.
//     This is sanitized via pathutil.SanitizePath to prevent path traversal.
//     Examples: "etc", "usr/share", "firmware", or "" for root.
//
// Key design decisions:
//   - Using a single struct with configurable subdir instead of separate types
//     for each installation path reduces code duplication.
//   - The copy command is delegated to copyCommand() which allows the build
//     system to configure the appropriate copy tool (cp, copy, etc.).
type prebuiltEtcRule struct {
	typeName string // Module type name as used in .bp files (e.g., "prebuilt_etc")
	subdir   string // Installation subdirectory relative to system root (e.g., "etc", "usr/share")
}

// Name returns the module type name for this prebuilt_etc rule.
// This method implements the BuildRule interface.
//
// Returns:
//   - The module type name string (e.g., "prebuilt_etc").
//     This is the value of the typeName field set during rule registration.
//
// Edge cases:
//   - Returns empty string if typeName was not initialized (programmer error).
//
// Notes:
//   - The typeName is set during rule registration and should not be modified after initialization.
func (r *prebuiltEtcRule) Name() string { return r.typeName }

// NinjaRule returns the Ninja build rule definition for copying prebuilt files to system directories.
//
// This method implements the BuildRule interface. It generates a Ninja rule
// named "prebuilt_copy" that copies files from the source location to the
// target system directory. The actual copy command is obtained from copyCommand(),
// which returns the appropriate copy command for the build environment
// (e.g., "cp" on Unix, "copy" on Windows).
//
// The generated rule is a simple copy operation without additional processing.
// Unlike prebuilt_binary, this rule does not set execute permissions since
// etc files are typically configuration or data files, not executables.
//
// Parameters:
//   - ctx: The rule rendering context (currently unused for this rule type).
//     Provided for interface compatibility; may be used in future extensions
//     (e.g., to customize the copy command per build configuration).
//
// Returns:
//   - A string containing the Ninja rule definition.
//     Format: "rule prebuilt_copy\n command = <copy_command>\n"
//     Example: "rule prebuilt_copy\n command = cp $in $out\n"
//
// Edge cases:
//   - The returned rule does not include description (deps, description variables).
//     This is intentional for simplicity; the Desc() method provides build descriptions.
//   - The copy command may vary by platform; copyCommand() handles this abstraction.
//
// Key design decisions:
//   - Using a generic "prebuilt_copy" rule name rather than type-specific names
//     allows sharing the same rule across multiple prebuilt module types.
//   - The rule is regenerated for each module instance but produces identical
//     output; Ninja deduplicates identical rules automatically.
func (r *prebuiltEtcRule) NinjaRule(ctx RuleRenderContext) string {
	// Return the prebuilt_copy rule definition with the platform-appropriate copy command.
	// The rule name "prebuilt_copy" is used as a reference in NinjaEdge().
	return "rule prebuilt_copy\n command = " + copyCommand() + "\n"
}

// Outputs returns the output file paths for the prebuilt_etc module.
//
// This method implements the BuildRule interface. It determines the output
// file path for the prebuilt file based on the module's properties:
//   - If "filename" property is specified, use it as the output filename.
//   - Otherwise, derive the filename from the source file's base name.
//
// The output path is constructed by joining the sanitized subdirectory
// (from the rule's subdir field) with the filename. Path components are
// sanitized to prevent directory traversal attacks.
//
// Parameters:
//   - m: The parser.Module representing the prebuilt_etc module definition.
//     The module is expected to have a "src" property (source file) and
//     optionally a "filename" property (desired output filename).
//   - ctx: The rule rendering context, providing build configuration
//     such as architecture suffix (though not used for prebuilt_etc).
//
// Returns:
//   - A slice containing a single output file path string.
//     The path uses forward slashes for Ninja compatibility.
//     Format: "<subdir>/<filename>" or "<filename>" if subdir is empty.
//     Returns nil if no source file is defined.
//
// Edge cases:
//   - Missing source file (empty "src" property) returns nil (no outputs).
//   - Missing "filename" property falls back to source file base name.
//   - Filename is sanitized to replace path separators with underscores.
//   - Subdirectory is sanitized via pathutil.SanitizePath to prevent traversal.
//   - Empty subdir results in output path being just the filename.
//
// Key design decisions:
//   - Using filepath.ToSlash() ensures consistent path separators across
//     platforms, which is required for Ninja build file compatibility.
//   - Only returning a single output path because prebuilt_etc modules
//     produce exactly one output file per module definition.
func (r *prebuiltEtcRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	src := getFirstSource(m)
	if src == "" { // No source file defined, cannot determine output path
		return nil
	}

	filename := GetStringProp(m, "filename")
	if filename == "" { // No explicit filename, fall back to source file basename
		filename = filepath.Base(src)
	}

	filename = sanitizePathComponent(filename) // Sanitize filename to prevent path traversal

	out := filename
	if r.subdir != "" { // Append sanitized subdirectory to output path
		safeSubdir := pathutil.SanitizePath(r.subdir) // Sanitize subdir to prevent path traversal
		out = filepath.Join(safeSubdir, filename)
	}

	// Use forward slashes for ninja consistency across platforms.
	return []string{filepath.ToSlash(out)}
}

// NinjaEdge generates the Ninja build edge for the prebuilt_etc module.
//
// This method implements the BuildRule interface. It creates a Ninja build
// statement that copies the source file to the target output path using
// the "prebuilt_copy" rule defined in NinjaRule(). The build edge specifies
// the input (source file) and output (target path) for the copy operation.
//
// Parameters:
//   - m: The parser.Module representing the prebuilt_etc module definition.
//     Must have a valid "src" property for the source file.
//   - ctx: The rule rendering context, providing build configuration.
//
// Returns:
//   - A string containing the Ninja build edge statement.
//     Format: "build <output>: prebuilt_copy <input>\n"
//     Example: "build etc/myconfig.conf: prebuilt_copy //path/to/source.conf\n"
//     Returns empty string if no source file or no outputs are defined.
//
// Edge cases:
//   - Missing source file returns empty string (no build edge generated).
//   - Missing outputs (from Outputs()) returns empty string.
//   - Both source and output paths are escaped via ninjaEscapePath()
//     to handle spaces and special characters in paths.
//
// Key design decisions:
//   - Using the "prebuilt_copy" rule reference which must be defined
//     by NinjaRule() before this edge is used in the build file.
//   - Only generating a single build edge because prebuilt_etc modules
//     have exactly one source and one output.
func (r *prebuiltEtcRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Get the source file path from the module's "src" property.
	src := getFirstSource(m)

	outs := r.Outputs(m, ctx)

	if src == "" || len(outs) == 0 { // Missing source or outputs, cannot generate build edge
		return ""
	}

	// Generate the Ninja build edge statement.
	// Format: "build <output>: <rule> <input>"
	// ninjaEscapePath() ensures paths with spaces or special characters are properly quoted.
	return fmt.Sprintf("build %s: prebuilt_copy %s\n", ninjaEscapePath(outs[0]), ninjaEscapePath(src))
}

// Desc returns a short description string for the prebuilt_etc module.
//
// This method implements the BuildRule interface. The description is used
// in build logging and progress output to identify what operation is being
// performed. For prebuilt_etc modules, the operation is simply copying
// files, so "cp" (copy) is returned.
//
// Parameters:
//   - m: The parser.Module representing the module (unused for this rule type).
//     Reserved for future use if description needs to be module-specific.
//   - srcFile: The source file path (unused for this rule type).
//     Reserved for future use if description should include source info.
//
// Returns:
//   - The string "cp" indicating a copy operation.
//     This appears in Ninja's build output when the rule is executed.
//
// Edge cases:
//   - The description is constant for all prebuilt_etc modules.
//   - This method is called for logging purposes only; it does not affect build behavior.
func (r *prebuiltEtcRule) Desc(m *parser.Module, srcFile string) string { return "cp" }

// prebuiltBinaryRule implements BuildRule for pre-built executable binaries.
//
// This rule handles "prebuilt_binary" and "cc_prebuilt_binary" module types.
// It copies pre-built executable binaries to the output directory and sets
// execute permissions on the output file using chmod +x.
//
// Unlike prebuilt_etc, this rule ensures the output file is executable,
// which is required for binaries to run correctly. The copy command is
// followed by a chmod +x operation in the generated Ninja rule.
//
// The output filename is determined by:
//   - "stem" property if specified
//   - Otherwise, the module name
//
// The architecture suffix (from ctx.ArchSuffix) is appended to the output
// filename for multi-architecture builds (e.g., "mybin_arm64").
//
// Fields:
//   - typeName: The module type name as it appears in .bp files.
//     Examples: "prebuilt_binary", "cc_prebuilt_binary".
//     This is returned by the Name() method and used for module type registration.
//
// Key design decisions:
//   - Combining copy and chmod in a single rule simplifies the build edge
//     and ensures atomic permission setting with the copy operation.
//   - Using stem (or module name) rather than source file name allows
//     renaming the binary in the output independently of the source filename.
type prebuiltBinaryRule struct {
	typeName string // Module type name as used in .bp files (e.g., "prebuilt_binary")
}

// Name returns the module type name for this prebuilt_binary rule.
// This method implements the BuildRule interface.
//
// Returns:
//   - The module type name string (e.g., "prebuilt_binary").
//     This is the value of the typeName field set during rule registration.
//
// Edge cases:
//   - Returns empty string if typeName was not initialized (programmer error).
//
// Notes:
//   - The typeName is set during rule registration and should not be modified after initialization.
func (r *prebuiltBinaryRule) Name() string { return r.typeName }

// NinjaRule returns the Ninja build rule definition for copying prebuilt executable binaries.
//
// This method implements the BuildRule interface. It generates a Ninja rule
// named "prebuilt_binary_copy" that copies the binary from source to target
// and sets execute permissions on the output file. The rule executes two
// commands sequentially:
//  1. Copy the file using the platform-appropriate copy command.
//  2. Set execute permission using "chmod +x" on the output file.
//
// The execute permission is essential for binaries to run correctly.
// Without it, the copied binary would not be executable on Unix systems.
//
// Parameters:
//   - ctx: The rule rendering context (currently unused for this rule type).
//     Provided for interface compatibility; may be used in future extensions.
//
// Returns:
//   - A string containing the Ninja rule definition.
//     Format: "rule prebuilt_binary_copy\n command = <copy_command> && chmod +x $out\n"
//     Example: "rule prebuilt_binary_copy\n command = cp $in $out && chmod +x $out\n"
//
// Edge cases:
//   - The chmod command is Unix-specific; Windows builds may need adjustment.
//     The copyCommand() function handles platform differences for the copy part.
//   - If the copy command fails, chmod is not executed (due to && short-circuit).
//
// Key design decisions:
//   - Combining copy and chmod in a single rule ensures atomic permission
//     setting and simplifies the build edge (no separate chmod rule needed).
//   - Using "&&" ensures chmod only runs if copy succeeds, preventing
//     permission changes on failed copies.
func (r *prebuiltBinaryRule) NinjaRule(ctx RuleRenderContext) string {
	// Return the prebuilt_binary_copy rule with copy and chmod commands.
	// The rule name "prebuilt_binary_copy" is used as a reference in NinjaEdge().
	// "$out" is a Ninja variable representing the output file path.
	return "rule prebuilt_binary_copy\n command = " + copyCommand() + " && chmod +x $out\n"
}

// Outputs returns the output file paths for the prebuilt_binary module.
//
// This method implements the BuildRule interface. It determines the output
// binary filename based on the module's properties:
//   - If "stem" property is specified, use it as the base filename.
//   - Otherwise, use the module name as the base filename.
//
// The architecture suffix (from ctx.ArchSuffix) is appended to the stem
// for multi-architecture builds. This allows the same module to produce
// different outputs for different architectures (e.g., "mybin_arm64", "mybin_x86").
//
// Parameters:
//   - m: The parser.Module representing the prebuilt_binary module definition.
//     The module is expected to have a "src" property (source file) and
//     optionally a "stem" property (desired output filename without suffix).
//   - ctx: The rule rendering context, providing build configuration
//     including ArchSuffix for multi-arch support.
//
// Returns:
//   - A slice containing a single output file path string.
//     Format: "<stem><arch_suffix>" (e.g., "mybin_arm64").
//     Returns nil if module name or source file is missing.
//
// Edge cases:
//   - Missing module name returns nil (cannot determine output path).
//   - Missing source file returns nil (though source is not used in naming).
//   - Stem is sanitized to replace path separators with underscores.
//   - ArchSuffix may be empty for single-architecture builds.
//
// Key design decisions:
//   - Using stem (or module name) rather than source filename allows
//     renaming the binary in the output independently of the source.
//   - Architecture suffix is appended without a separator; the suffix
//     itself typically starts with underscore (e.g., "_arm64").
//   - Not including file extension because binaries typically don't have
//     extensions on Unix; Windows binaries would need special handling.
func (r *prebuiltBinaryRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Get the module name from the "name" property.
	// This is used as the default stem if "stem" property is not specified.
	name := getName(m)

	// Get the first source file from the module's "src" property.
	// Source is validated but not used in output path construction.
	src := getFirstSource(m)

	// Validate that we have both name and source before proceeding.
	if name == "" || src == "" {
		// Missing required information; cannot determine output path.
		return nil
	}

	// Determine the output stem (base filename without arch suffix).
	// Prefer explicit "stem" property; fall back to module name.
	stem := GetStringProp(m, "stem")
	if stem == "" {
		stem = name
	}

	// Sanitize the stem to prevent path traversal attacks.
	// This replaces '/' and '\' with '_' to ensure the stem is a simple filename.
	stem = sanitizePathComponent(stem)

	// Append architecture suffix for multi-arch support and return.
	// ctx.ArchSuffix is typically something like "_arm64" or "_x86" for cross-compilation.
	return []string{stem + ctx.ArchSuffix}
}

// NinjaEdge generates the Ninja build edge for the prebuilt_binary module.
//
// This method implements the BuildRule interface. It creates a Ninja build
// statement that copies the source binary to the target output path and
// sets execute permissions, using the "prebuilt_binary_copy" rule defined
// in NinjaRule(). The build edge specifies the input (source file) and
// output (target path) for the copy operation.
//
// Parameters:
//   - m: The parser.Module representing the prebuilt_binary module definition.
//     Must have a valid "src" property for the source file.
//   - ctx: The rule rendering context, providing build configuration.
//
// Returns:
//   - A string containing the Ninja build edge statement.
//     Format: "build <output>: prebuilt_binary_copy <input>\n"
//     Example: "build mybin_arm64: prebuilt_binary_copy //path/to/source.bin\n"
//     Returns empty string if no source file or no outputs are defined.
//
// Edge cases:
//   - Missing source file returns empty string (no build edge generated).
//   - Missing outputs (from Outputs()) returns empty string.
//   - Both source and output paths are escaped via ninjaEscapePath()
//     to handle spaces and special characters in paths.
//
// Key design decisions:
//   - Using the "prebuilt_binary_copy" rule reference which must be defined
//     by NinjaRule() before this edge is used in the build file.
//   - Only generating a single build edge because prebuilt_binary modules
//     have exactly one source and one output.
func (r *prebuiltBinaryRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Get the source file path from the module's "src" property.
	src := getFirstSource(m)

	// Get the output file paths from Outputs().
	// This also handles stem derivation and architecture suffix.
	outs := r.Outputs(m, ctx)

	// Validate that we have both source and outputs before generating the build edge.
	if src == "" || len(outs) == 0 {
		// Missing required information; cannot generate a valid build edge.
		return ""
	}

	// Generate the Ninja build edge statement.
	// Format: "build <output>: <rule> <input>"
	// ninjaEscapePath() ensures paths with spaces or special characters are properly quoted.
	return fmt.Sprintf("build %s: prebuilt_binary_copy %s\n", ninjaEscapePath(outs[0]), ninjaEscapePath(src))
}

// Desc returns a short description string for the prebuilt_binary module.
//
// This method implements the BuildRule interface. The description is used
// in build logging and progress output to identify what operation is being
// performed. For prebuilt_binary modules, the operation is copying
// the binary and setting execute permissions, but "cp" (copy) is used
// as a short identifier.
//
// Parameters:
//   - m: The parser.Module representing the module (unused for this rule type).
//     Reserved for future use if description needs to be module-specific.
//   - srcFile: The source file path (unused for this rule type).
//     Reserved for future use if description should include source info.
//
// Returns:
//   - The string "cp" indicating a copy operation.
//     This appears in Ninja's build output when the rule is executed.
//
// Edge cases:
//   - The description is constant for all prebuilt_binary modules.
//   - This method is called for logging purposes only; it does not affect build behavior.
func (r *prebuiltBinaryRule) Desc(m *parser.Module, srcFile string) string { return "cp" }

// prebuiltLibraryRule implements BuildRule for pre-built library files.
//
// This rule handles "prebuilt_library", "cc_prebuilt_library" (static),
// and "cc_prebuilt_library_shared" (shared) module types. It copies
// pre-compiled library files (.a for static, .so for shared) to the
// output directory without recompilation.
//
// The output filename is determined by:
//   - "stem" property if specified
//   - Otherwise, "lib" + module name (e.g., module "foo" becomes "libfoo")
//
// The architecture suffix (from ctx.ArchSuffix) and file extension (.a or .so)
// are appended to the output filename for multi-architecture builds.
//
// The ext field specifies the library file extension:
//   - ".a" for static libraries (cc_prebuilt_library)
//   - ".so" for shared libraries (cc_prebuilt_library_shared)
//
// Fields:
//   - typeName: The module type name as it appears in .bp files.
//     Examples: "prebuilt_library", "cc_prebuilt_library", "cc_prebuilt_library_shared".
//     This is returned by the Name() method and used for module type registration.
//   - ext: The library file extension including the dot.
//     Must be either ".a" or ".so" depending on the library type.
//
// Key design decisions:
//   - Using a single struct with configurable ext field instead of separate
//     types for static and shared libraries reduces code duplication.
//   - Prefixing "lib" to the module name (when stem is not specified) follows
//     Unix convention for library naming.
//   - The ext field is checked against the stem to avoid double extensions
//     (e.g., "libfoo.a.a" is prevented).
type prebuiltLibraryRule struct {
	typeName string // Module type name as used in .bp files (e.g., "prebuilt_library")
	ext      string // Library file extension including dot (e.g., ".a" for static, ".so" for shared)
}

// Name returns the module type name for this prebuilt_library rule.
//
// This method implements the BuildRule interface. The returned name
// must match the module type string used in .bp files (e.g., "prebuilt_library",
// "cc_prebuilt_library", or "cc_prebuilt_library_shared"). The name is used
// by the build system to associate modules with their corresponding rule implementations.
//
// Returns:
//   - The module type name string (e.g., "prebuilt_library").
//     This is the value of the typeName field set during rule registration.
//
// Edge cases:
//   - Returns empty string if typeName was not initialized (programmer error).
func (r *prebuiltLibraryRule) Name() string { return r.typeName }

// NinjaRule returns the Ninja build rule definition for copying prebuilt libraries.
//
// This method implements the BuildRule interface. It generates a Ninja rule
// named "prebuilt_library_copy" that copies library files from the source
// location to the target output directory. The copy command is obtained
// from copyCommand(), which returns the appropriate copy command for the
// build environment (e.g., "cp" on Unix, "copy" on Windows).
//
// Unlike prebuilt_binary, this rule does not set execute permissions
// because library files (both .a and .so) are not directly executable.
// Shared libraries (.so) need to be loadable, but the execute permission
// is not required for dynamic linking.
//
// Parameters:
//   - ctx: The rule rendering context (currently unused for this rule type).
//     Provided for interface compatibility; may be used in future extensions.
//
// Returns:
//   - A string containing the Ninja rule definition.
//     Format: "rule prebuilt_library_copy\n command = <copy_command>\n"
//     Example: "rule prebuilt_library_copy\n command = cp $in $out\n"
//
// Edge cases:
//   - The returned rule does not include description (deps, description variables).
//     This is intentional for simplicity; the Desc() method provides build descriptions.
//   - The copy command may vary by platform; copyCommand() handles this abstraction.
//
// Key design decisions:
//   - Using a generic "prebuilt_library_copy" rule name rather than type-specific
//     names allows sharing the same rule across static and shared library types.
//   - Not adding execute permission (chmod +x) because libraries should not
//     be directly executable; shared libs use the ELF loader, not direct execution.
func (r *prebuiltLibraryRule) NinjaRule(ctx RuleRenderContext) string {
	// Return the prebuilt_library_copy rule definition with the platform-appropriate copy command.
	// The rule name "prebuilt_library_copy" is used as a reference in NinjaEdge().
	return "rule prebuilt_library_copy\n command = " + copyCommand() + "\n"
}

// Outputs returns the output file paths for the prebuilt_library module.
//
// This method implements the BuildRule interface. It determines the output
// library filename based on the module's properties:
//   - If "stem" property is specified, use it as the base filename.
//   - Otherwise, construct the library name as "lib" + module name
//     (e.g., module "foo" becomes "libfoo").
//
// The architecture suffix (from ctx.ArchSuffix) and file extension (.a or .so)
// are appended to the stem for multi-architecture builds. The extension is
// only added if the stem doesn't already have it (prevents "libfoo.a.a").
//
// Parameters:
//   - m: The parser.Module representing the prebuilt_library module definition.
//     The module is expected to have a "src" property (source file) and
//     optionally a "stem" property (desired output filename without suffix/extension).
//   - ctx: The rule rendering context, providing build configuration
//     including ArchSuffix for multi-arch support.
//
// Returns:
//   - A slice containing a single output file path string.
//     Format: "<stem><arch_suffix><ext>" (e.g., "libfoo_arm64.a").
//     Returns nil if module name or source file is missing.
//
// Edge cases:
//   - Missing module name returns nil (cannot determine output path).
//   - Missing source file returns nil (though source is not used in naming).
//   - Stem is sanitized to replace path separators with underscores.
//   - If stem already ends with the correct extension (r.ext), the extension
//     is not appended again (prevents double extensions like "libfoo.a.a").
//   - ArchSuffix may be empty for single-architecture builds.
//
// Key design decisions:
//   - Prefixing "lib" to the module name (when stem is not specified) follows
//     Unix convention for library naming, making the output familiar to developers.
//   - Checking strings.HasSuffix before appending extension prevents double extensions
//     when users specify a stem that already includes the extension.
//   - Architecture suffix is inserted before the extension to maintain the
//     standard library naming pattern (e.g., "libfoo_arm64.a" not "libfoo.a_arm64").
func (r *prebuiltLibraryRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Get the module name from the "name" property.
	// This is used as the base for constructing the library name.
	name := getName(m)

	// Get the first source file from the module's "src" property.
	// Source is validated but not used in output path construction.
	src := getFirstSource(m)

	// Validate that we have both name and source before proceeding.
	if name == "" || src == "" {
		// Missing required information; cannot determine output path.
		return nil
	}

	// Determine the output stem (base filename without arch suffix and extension).
	// Prefer explicit "stem" property; fall back to "lib" + module name.
	stem := GetStringProp(m, "stem")
	if stem == "" {
		stem = "lib" + name
	}

	// Sanitize the stem to prevent path traversal attacks.
	// This replaces '/' and '\' with '_' to ensure the stem is a simple filename.
	stem = sanitizePathComponent(stem)

	// Append architecture suffix and file extension.
	// Only add extension if the stem doesn't already have it (prevents double extensions).
	if !strings.HasSuffix(stem, r.ext) {
		// Insert arch suffix before extension: "libfoo" + "_arm64" + ".a" = "libfoo_arm64.a"
		stem += ctx.ArchSuffix + r.ext
	}

	return []string{stem}
}

// NinjaEdge generates the Ninja build edge for the prebuilt_library module.
//
// This method implements the BuildRule interface. It creates a Ninja build
// statement that copies the source library file to the target output path
// using the "prebuilt_library_copy" rule defined in NinjaRule(). The build
// edge specifies the input (source file) and output (target path) for the
// copy operation.
//
// Parameters:
//   - m: The parser.Module representing the prebuilt_library module definition.
//     Must have a valid "src" property for the source file.
//   - ctx: The rule rendering context, providing build configuration.
//
// Returns:
//   - A string containing the Ninja build edge statement.
//     Format: "build <output>: prebuilt_library_copy <input>\n"
//     Example: "build libfoo_arm64.a: prebuilt_library_copy //path/to/libfoo.a\n"
//     Returns empty string if no source file or no outputs are defined.
//
// Edge cases:
//   - Missing source file returns empty string (no build edge generated).
//   - Missing outputs (from Outputs()) returns empty string.
//   - Both source and output paths are escaped via ninjaEscapePath()
//     to handle spaces and special characters in paths.
//
// Key design decisions:
//   - Using the "prebuilt_library_copy" rule reference which must be defined
//     by NinjaRule() before this edge is used in the build file.
//   - Only generating a single build edge because prebuilt_library modules
//     have exactly one source and one output.
func (r *prebuiltLibraryRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Get the source file path from the module's "src" property.
	src := getFirstSource(m)

	// Get the output file paths from Outputs().
	// This also handles stem derivation, architecture suffix, and extension.
	outs := r.Outputs(m, ctx)

	// Validate that we have both source and outputs before generating the build edge.
	if src == "" || len(outs) == 0 {
		// Missing required information; cannot generate a valid build edge.
		return ""
	}

	// Generate the Ninja build edge statement.
	// Format: "build <output>: <rule> <input>"
	// ninjaEscapePath() ensures paths with spaces or special characters are properly quoted.
	return fmt.Sprintf("build %s: prebuilt_library_copy %s\n", ninjaEscapePath(outs[0]), ninjaEscapePath(src))
}

// Desc returns a short description string for the prebuilt_library module.
//
// This method implements the BuildRule interface. The description is used
// in build logging and progress output to identify what operation is being
// performed. For prebuilt_library modules, the operation is simply copying
// library files, so "cp" (copy) is returned.
//
// Parameters:
//   - m: The parser.Module representing the module (unused for this rule type).
//     Reserved for future use if description needs to be module-specific.
//   - srcFile: The source file path (unused for this rule type).
//     Reserved for future use if description should include source info.
//
// Returns:
//   - The string "cp" indicating a copy operation.
//     This appears in Ninja's build output when the rule is executed.
//
// Edge cases:
//   - The description is constant for all prebuilt_library modules.
//   - This method is called for logging purposes only; it does not affect build behavior.
func (r *prebuiltLibraryRule) Desc(m *parser.Module, srcFile string) string { return "cp" }
