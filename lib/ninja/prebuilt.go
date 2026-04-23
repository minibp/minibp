// prebuilt.go - Prebuilt module rules for minibp
// This file implements rules for using pre-built binaries and libraries.
// Prebuilt modules allow referencing already-compiled artifacts without rebuilding.
//
// Algorithm overview:
//  1. Parse module properties (src, filename/stem for output naming)
//  2. Generate copy rule to install prebuilt files
//  3. Handle architecture suffixes for multi-arch support
//  4. Set output path based on module type and subdir property
//
// Module types:
//   - prebuilt_etc: Installs files to system directories (/etc, /usr/share, /firmware)
//   - prebuilt_binary: Pre-built executable binaries
//   - prebuilt_library: Pre-built libraries (.a or .so)
//   - cc_prebuilt_binary: C/C++ pre-built binary
//   - cc_prebuilt_library: C/C++ pre-built static library
//   - cc_prebuilt_library_shared: C/C++ pre-built shared library
//
// Edge cases:
//   - Uses "filename" or "stem" property if specified for output name
//   - Otherwise derives name from source file or module name
//   - Architecture suffix added for multi-arch support
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"path/filepath"
	"strings"
)

// prebuiltEtcRule implements a prebuilt file that gets installed to system directories.
// The subdir specifies the installation directory (etc, usr/share, firmware, or root).
//
// Supported installation paths:
//   - "etc" for /etc
//   - "usr_share" for /usr/share
//   - "firmware" for /firmware
//   - "root" for /
//
// Fields:
//   - typeName: The module type name (e.g., "prebuilt_etc")
//   - subdir: Installation subdirectory
type prebuiltEtcRule struct {
	typeName string
	subdir   string
}

// Name returns the module type name for prebuilt_etc modules.
func (r *prebuiltEtcRule) Name() string { return r.typeName }

// NinjaRule returns the ninja build rule for copying prebuilt files to system directories.
// Uses the prebuilt_copy rule which copies files using the configured copy command.
// The copy command is configured based on the build environment.
func (r *prebuiltEtcRule) NinjaRule(ctx RuleRenderContext) string {
	return "rule prebuilt_copy\n command = " + copyCommand() + "\n"
}

// Outputs returns the output file paths for the prebuilt_etc module.
// If a filename property is specified, it is used as the output filename;
// otherwise, the base name of the source file is used.
// The output path includes the subdirectory prefix if configured.
//
// Returns:
//   - List of output file paths including subdirectory prefix
func (r *prebuiltEtcRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	src := getFirstSource(m)
	if src == "" {
		return nil
	}
	filename := GetStringProp(m, "filename")
	if filename == "" {
		filename = filepath.Base(src)
	}
	out := filename
	if r.subdir != "" {
		out = filepath.Join(r.subdir, filename)
	}
	// Use forward slashes for ninja consistency.
	return []string{filepath.ToSlash(out)}
}

// NinjaEdge generates the build edge for the prebuilt_etc module.
// Creates a ninja build statement that copies the source file to the target path.
//
// Edge cases:
//   - Returns empty string if no source or no outputs
func (r *prebuiltEtcRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	src := getFirstSource(m)
	outs := r.Outputs(m, ctx)
	if src == "" || len(outs) == 0 {
		return ""
	}
	return fmt.Sprintf("build %s: prebuilt_copy %s\n", ninjaEscapePath(outs[0]), ninjaEscapePath(src))
}

// Desc returns a short description string for the prebuilt_etc module.
// This is used in build logging and progress output.
func (r *prebuiltEtcRule) Desc(m *parser.Module, srcFile string) string { return "cp" }

// prebuiltBinaryRule implements a prebuilt executable binary rule.
// Prebuilt binaries are executable files that are copied to the output directory
// with executable permissions set via chmod.
//
// Fields:
//   - typeName: The module type name (e.g., "prebuilt_binary", "cc_prebuilt_binary")
type prebuiltBinaryRule struct {
	typeName string
}

// Name returns the module type name for prebuilt_binary modules.
func (r *prebuiltBinaryRule) Name() string { return r.typeName }

// NinjaRule returns the ninja build rule for copying prebuilt executable binaries.
// Uses the prebuilt_binary_copy rule which copies files and sets executable permissions.
func (r *prebuiltBinaryRule) NinjaRule(ctx RuleRenderContext) string {
	return "rule prebuilt_binary_copy\n command = " + copyCommand() + " && chmod +x $out\n"
}

// Outputs returns the output file paths for the prebuilt_binary module.
// Uses the stem property if specified, otherwise uses the module name.
// The output includes architecture suffix for multi-arch support.
//
// Returns:
//   - List containing the output binary path with arch suffix
func (r *prebuiltBinaryRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	src := getFirstSource(m)
	if name == "" || src == "" {
		return nil
	}
	stem := GetStringProp(m, "stem")
	if stem == "" {
		stem = name
	}
	return []string{stem + ctx.ArchSuffix}
}

// NinjaEdge generates the build edge for the prebuilt_binary module.
// Creates a ninja build statement that copies the source file and sets execute permission.
//
// Edge cases:
//   - Returns empty string if no source or no outputs defined
func (r *prebuiltBinaryRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	src := getFirstSource(m)
	outs := r.Outputs(m, ctx)
	if src == "" || len(outs) == 0 {
		return ""
	}
	return fmt.Sprintf("build %s: prebuilt_binary_copy %s\n", ninjaEscapePath(outs[0]), ninjaEscapePath(src))
}

// Desc returns a short description string for the prebuilt_binary module.
func (r *prebuiltBinaryRule) Desc(m *parser.Module, srcFile string) string { return "cp" }

// prebuiltLibraryRule implements a prebuilt library rule (.a or .so).
// Prebuilt libraries are pre-compiled static or shared libraries that are copied
// to the output directory without recompilation.
//
// The ext field specifies the library file extension:
//   - ".a" for static libraries
//   - ".so" for shared libraries
//
// Fields:
//   - typeName: The module type name (e.g., "prebuilt_library")
//   - ext: The library file extension (.a or .so)
type prebuiltLibraryRule struct {
	typeName string
	ext      string
}

// Name returns the module type name for prebuilt_library modules.
func (r *prebuiltLibraryRule) Name() string { return r.typeName }

// NinjaRule returns the ninja build rule for copying prebuilt libraries.
// Uses the prebuilt_library_copy rule which copies library files.
func (r *prebuiltLibraryRule) NinjaRule(ctx RuleRenderContext) string {
	return "rule prebuilt_library_copy\n command = " + copyCommand() + "\n"
}

// Outputs returns the output file paths for the prebuilt_library module.
// Uses the stem property if specified, otherwise constructs the library name as "lib<name>".
// The output includes the configured file extension and architecture suffix.
//
// Algorithm:
//  1. Get module name and first source
//  2. Exit early if missing
//  3. Use stem if specified, otherwise use "lib" + name
//  4. Append arch suffix and extension
//
// Returns:
//   - List containing the library output path
func (r *prebuiltLibraryRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	src := getFirstSource(m)
	if name == "" || src == "" {
		return nil
	}
	stem := GetStringProp(m, "stem")
	if stem == "" {
		stem = "lib" + name
	}
	if !strings.HasSuffix(stem, r.ext) {
		stem += ctx.ArchSuffix + r.ext
	}
	return []string{stem}
}

// NinjaEdge generates the build edge for the prebuilt_library module.
// Creates a ninja build statement that copies the library file to the target path.
//
// Edge cases:
//   - Returns empty string if no source or no outputs defined
func (r *prebuiltLibraryRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	src := getFirstSource(m)
	outs := r.Outputs(m, ctx)
	if src == "" || len(outs) == 0 {
		return ""
	}
	return fmt.Sprintf("build %s: prebuilt_library_copy %s\n", ninjaEscapePath(outs[0]), ninjaEscapePath(src))
}

// Desc returns a short description string for the prebuilt_library module.
func (r *prebuiltLibraryRule) Desc(m *parser.Module, srcFile string) string { return "cp" }
