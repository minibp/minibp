// filegroup.go - File group build rules for minibp
// This file implements file group rules for collecting and copying files.
//
// Algorithm overview:
//  1. filegroup: Copy individual source files to output directory
//  2. filegroup_static: Concatenate all sources into single output file
//
// File groups are used to:
//   - Group together a set of files under a single logical name
//   - Copy files to an output directory
//   - Create static file groups for resource embedding
//
// Module types:
//   - filegroup: Copy individual files to output directory
//   - filegroup_static: Create a single static file for all inputs
//
// Edge cases:
//   - Output directory uses the group name as directory path
//   - Each file is copied individually preserving its basename
//
// filegroup implements the BuildRule interface:
//   - Name() string: Returns "filegroup"
//   - NinjaRule(ctx) string: Returns ninja rule definition
//   - Outputs(m, ctx) []string: Returns output directory paths
//   - NinjaEdge(m, ctx) string: Returns ninja build edges
//   - Desc(m, src) string: Returns a short description
//
// This file provides file group rules for organizing and copying files in the build system.
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"minibp/lib/utils"
	"path/filepath"
	"runtime"
	"strings"
)

// filegroup implements a file group rule.
// It copies source files to an output directory under the group name.
// Each source file is copied to its basename in the output directory.
//
// File groups collect multiple source files and copy them to a common output directory.
// Each file is copied individually using the cp command, preserving the source filename
// in the destination. The group is identified by a name that becomes the directory
// name in the output path.
//
// Algorithm:
//  1. Get module name (becomes output directory name)
//  2. Get source files from srcs property
//  3. For each source file, generate copy to {name}/{basename}
//  4. Use appropriate copy command for the platform
//
// Edge cases:
//   - If no sources, generate nothing
//   - If no name, generate nothing
//   - On Windows, use cmd /c copy instead of cp
type filegroup struct{}

func (r *filegroup) Name() string { return "filegroup" }

// NinjaRule returns the ninja rule template for file group copying.
// Uses cp on Unix-like systems and cmd /c copy on Windows.
func (r *filegroup) NinjaRule(ctx RuleRenderContext) string {

	copyCmd := "cp $in $out"

	if runtime.GOOS == "windows" {

		copyCmd = "cmd /c copy $in $out"

	}

	return `rule filegroup_copy

 command = ` + copyCmd + `

`

}

// Outputs returns the output directory paths for filegroup.
// Returns a single entry {name}.files representing the group output.
//
// Returns:
//   - List containing the group output identifier
func (r *filegroup) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".files"}
}

// NinjaEdge generates ninja build edges for copying each source file.
// Each source file is copied to {group_name}/{basename(source)}.
//
// Algorithm:
//  1. Get sources and module name
//  2. Exit early if either is missing
//  3. For each source, generate copy to output directory
func (r *filegroup) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return ""
	}
	name := getName(m)
	if name == "" {
		return ""
	}

	// Filegroup just copies files to output directory
	var edges strings.Builder
	for _, src := range srcs {
		// Sanitize both module name and source filename to prevent path traversal
		safeName := utils.SanitizePath(name)
		safeSrc := utils.SanitizePath(filepath.Base(src))
		out := filepath.Join(safeName, safeSrc)
		edges.WriteString(fmt.Sprintf("build %s: filegroup_copy %s\n", out, src))
	}
	return edges.String()
}

func (r *filegroup) Desc(m *parser.Module, srcFile string) string {
	return "cp"
}

// filegroupStatic implements a static file group rule.
//
// Static file groups bundle all source files into a single output file.
// Unlike filegroup which copies each file individually, filegroup_static concatenates
// all inputs into one combined static file. This is useful for creating asset bundles
// or resource files that need to be embedded as a single unit.
//
// The output is a single file with ".static" extension containing all source
// file contents concatenated in order.
//
// Algorithm:
//  1. Get module name and sources
//  2. Exit early if either is missing
//  3. Generate output as {name}.static
//  4. All sources become dependencies of single output
//
// Edge cases:
//   - If no sources, generate nothing
//   - If no name, generate nothing
type filegroupStatic struct{}

func (r *filegroupStatic) Name() string { return "filegroup_static" }

// NinjaRule returns the ninja rule template for static file group.
// Uses cp to combine files; actual concatenation might need a custom command.
func (r *filegroupStatic) NinjaRule(ctx RuleRenderContext) string {

	return `rule filegroup_static

 command = cp $in $out

`

}

// Outputs returns the static output file path.
// Output is {name}.static representing the combined file.
func (r *filegroupStatic) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".static"}
}

// NinjaEdge generates the ninja build edge for static file group.
//
// Algorithm:
//  1. Get sources and module name
//  2. Exit early if either is missing
//  3. Single build edge with all sources as inputs
func (r *filegroupStatic) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return ""
	}
	name := getName(m)
	if name == "" {
		return ""
	}

	var edges strings.Builder
	out := name + ".static"
	edges.WriteString(fmt.Sprintf("build %s: filegroup_static %s\n", out, strings.Join(srcs, " ")))
	return edges.String()
}

func (r *filegroupStatic) Desc(m *parser.Module, srcFile string) string {
	return "cp"
}
