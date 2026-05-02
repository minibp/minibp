// Package ninja generates Ninja build files from minibp module definitions.
// It translates Blueprint (.Android.bp) module definitions into Ninja build rules and edges.
//
// Pipeline overview:
//  1. Load all modules from .bp files (done in lib/build)
//  2. Build dependency graph with topological sort
//  3. For each module, find its BuildRule implementation
//  4. Generate ninja rules (rule definitions)
//  5. Generate ninja edges (build statements)
//  6. Write to build.ninja file
//
// Key concepts:
//   - Rules: Define command templates (e.g., "compile this C file")
//   - Edges: Define build statements connecting inputs to outputs via rules
//   - Phony: Virtual targets that alias other targets
//   - Variables: $variable substitution in rules (e.g., $in, $out, $flags)
//
// The Generator uses dependency injection for testability:
//   - Graph interface for dependency ordering
//   - Map[string]BuildRule for module type implementations
//   - Map[string]*parser.Module for all module definitions
//
// This file contains the core Generator implementation, helper types,
// and utility functions for ninja generation. Key types include:
//   - Generator: Main struct that orchestrates ninja file generation
//   - Graph: Interface providing dependency ordering via TopoSort
//   - BuildRule: Interface implemented by each module type (cc, go, java, etc.)
//   - Toolchain: Struct holding compiler paths and flags
//
// Generator is the main entry point for ninja file generation.
// It coordinates the build pipeline and produces valid Ninja syntax.
// The Generate method must be called to write the build file.
package ninja

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"minibp/lib/parser"
)

// Graph is the interface needed for ninja generation.
// It provides the topological sort of module dependencies,
// allowing parallel builds within each level while maintaining correct build order.
//
// TopoSort returns modules organized by "levels" where:
//   - Modules in each level can be built in parallel
//   - Levels must be built sequentially (level N depends on level N-1)
//
// Implementation in lib/dag provides the topological sort
// algorithm that groups modules by their dependency distance.
type Graph interface {
	// TopoSort returns modules organized by build level.
	// Each level can be built in parallel, but levels must be built in order.
	TopoSort() ([][]string, error)
}

// Generator creates ninja build files from module dependency graphs.
// It orchestrates the translation from high-level module definitions
// to low-level Ninja build rules and edges.
// The generator handles all aspects of build file generation including:
//   - Rule definition rendering (compilers, linkers, archivers)
//   - Build edge generation (what to build, with what inputs)
//   - Path adjustment for cross-directory builds
//   - Include path collection for C/C++
//   - Multi-architecture builds (multilib)
//   - Java classpath and data file dependencies
//   - Phony target generation
type Generator struct {
	graph      Graph      // Dependency graph providing topological sort of modules
	rules      map[string]BuildRule // Map from module type name to BuildRule implementation
	modules    map[string]*parser.Module // Map from module name to parsed module definition
	sourceDir  string      // Root directory containing source files (Blueprint files)
	outputDir  string      // Directory where ninja runs and outputs are placed
	pathPrefix string      // Prefix to prepend to source paths for out-of-source builds
	regenCmd   string      // Command to regenerate build.ninja
	inputFiles []string    // Files that trigger regeneration
	outputFile string      // Output file for regeneration rule
	workDir    string      // Working directory for custom rules
	toolchain  Toolchain  // Compiler toolchain configuration
	arch       string      // Target architecture
	multilib   []string    // Multi-arch targets (e.g. ["arm64","x86_64"])
	targetOS   string      // Target operating system (e.g., "linux", "darwin", "windows")
}

// Toolchain holds compiler/tool configuration for cross-compilation.
// It encapsulates all tool paths and flags needed to build for a specific target.
// The Toolchain struct provides a centralized way to configure:
//   - C and C++ compilers (CC, CXX)
//   - Static library archiver (AR)
//   - Compiler and linker flags (CFlags, LdFlags)
//   - Sysroot for cross-compilation
//   - Compiler cache (ccache) for incremental build acceleration
//   - LTO mode for Link Time Optimization
type Toolchain struct {
	CC      string   // C compiler command (e.g., gcc, clang)
	CXX     string   // C++ compiler command (e.g., g++, clang++)
	AR      string   // Static library archiver (e.g., ar, llvm-ar)
	LD      string   // Linker command (e.g., ld, gold, lld); empty uses CC/CXX
	CFlags  []string // Extra global C/C++ compiler flags
	LdFlags []string // Extra global linker flags
	Sysroot string   // Target sysroot for cross-compilation
	Ccache  string   // Path to ccache binary (empty if unavailable)
	Lto     string   // Default LTO mode: "full", "thin", or ""
}

// NewGenerator creates a new Generator with the given graph and rules.
// The graph provides dependency ordering, rules map module types to implementations,
// and modules contains all the module definitions.
//
// Parameters:
//   - g: Dependency graph providing topological sort
//   - rules: Map from module type name to BuildRule implementation
//   - modules: Map from module name to module definition
//
// Returns:
//   - Generator with default settings (sourceDir=".", outputDir=".")
//
// Note:
//
//	The generator is configured with default directories and must have SetSourceDir
//	and SetOutputDir called before generating if non-default paths are needed.
func NewGenerator(g Graph, rules map[string]BuildRule, modules map[string]*parser.Module) *Generator {
	return &Generator{
		graph:     g,
		rules:     rules,
		modules:   modules,
		sourceDir: ".",
		outputDir: ".",
	}
}

// SetSourceDir sets the source directory where .bp (Blueprint) files are located.
// This is used for computing relative paths to source files.
//
// Parameters:
//   - dir: Absolute or relative path to source directory
//
// Returns: None
func (g *Generator) SetSourceDir(dir string) {
	g.sourceDir = dir
}

// SetOutputDir sets the output directory where ninja will run.
// This is used for computing relative paths from the build directory.
//
// Parameters:
//   - dir: Absolute or relative path to output directory
func (g *Generator) SetOutputDir(dir string) {
	g.outputDir = dir
}

// SetPathPrefix sets the prefix to prepend to source file paths.
// This is useful when the build directory is different from the source directory.
//
// Parameters:
//   - prefix: Path prefix to prepend (e.g., "bionic/")
func (g *Generator) SetPathPrefix(prefix string) {
	g.pathPrefix = prefix
}

// SetRegen sets the command and files for auto-regeneration of build.ninja.
// When any input file changes, ninja will re-run minibp to regenerate the build file.
//
// Parameters:
//   - cmd: Command to regenerate (e.g., "minibp -a .")
//   - files: List of files that trigger regeneration
//   - output: Output ninja file path
func (g *Generator) SetRegen(cmd string, files []string, output string) {
	g.regenCmd = cmd
	g.inputFiles = files
	g.outputFile = output
}

// SetWorkDir sets the working directory for custom rules.
// This is used by custom rules that need to glob files in the source tree.
//
// Parameters:
//   - dir: Working directory path
func (g *Generator) SetWorkDir(dir string) {
	g.workDir = dir
}

// SetToolchain sets the compiler toolchain configuration.
// This overrides the default GNU toolchain with custom compilers.
//
// Parameters:
//   - t: Toolchain with compiler paths and flags
func (g *Generator) SetToolchain(t Toolchain) {
	g.toolchain = t
}

// SetArch sets the target architecture for cross-compilation.
// This appends an architecture suffix to output binaries.
//
// Parameters:
//   - arch: Target architecture (e.g., "arm64", "x86_64")
func (g *Generator) SetArch(arch string) {
	g.arch = arch
}

// SetMultilib sets multiple target architectures for multi-arch builds.
// When set, the generator will build for all specified architectures.
//
// Parameters:
//   - archs: List of architectures to build for
func (g *Generator) SetMultilib(archs []string) {
	g.multilib = archs
}

// SetTargetOS sets the target operating system for cross-compilation.
// This is used for Go modules to set GOOS environment variable.
//
// Parameters:
//   - os: Target operating system (e.g., "linux", "darwin", "windows")
func (g *Generator) SetTargetOS(os string) {
	g.targetOS = os
}

// archsForModule returns the list of architectures to build for a given module.
// For CC modules in multilib mode, it returns all multilib architectures.
// Otherwise it returns the single configured architecture (may be "").
//
// Parameters:
//   - m: Module to get architectures for
//
// Returns:
//   - Slice of architecture names to build for
//
// Edge cases:
//   - Returns [""] if no architecture is configured
//   - Non-CC modules always use single architecture even in multilib mode
func (g *Generator) archsForModule(m *parser.Module) []string {
	if len(g.multilib) == 0 { // No multilib configured: use default single architecture
		return []string{g.arch}
	}
	if !strings.HasPrefix(m.Type, "cc_") && !strings.HasPrefix(m.Type, "cpp_") { // Non-CC/CPP modules use single arch in multilib mode
		return []string{g.arch}
	}
	return g.multilib
}

// DefaultToolchain returns a Toolchain with common GNU development tools.
// It auto-detects ccache availability.
//
// Returns:
//   - Toolchain with default gcc/g++/ar and ccache if available
//
// Edge cases:
//   - ccache is empty string if not found in PATH
func DefaultToolchain() Toolchain {
	tc := Toolchain{
		CC:  "gcc",
		CXX: "g++",
		AR:  "ar",
	}
	tc.Ccache = detectCcache()
	return tc
}

// detectCcache searches for ccache in PATH.
// Ccache is a compiler cache that speeds up incremental builds.
//
// Returns:
//   - Full path to ccache binary if found
//   - Empty string if ccache is not available
func detectCcache() string {
	exe, err := exec.LookPath("ccache")
	if err != nil {
		return ""
	}
	return exe
}

// getRelativePath returns the relative path from the output directory to a file in the source directory.
// This is used when the build directory differs from the source directory.
//
// Parameters:
//   - file: File path relative to source directory
//
// Returns:
//   - Relative path from output to file
//
// Edge cases:
//   - Returns original file if directories are the same
//   - Returns original file if absolute conversion fails
//   - Returns original file if relative calculation fails
func (g *Generator) getRelativePath(file string) string {
	if g.sourceDir == g.outputDir {
		return file
	}
	absSource, err := filepath.Abs(g.sourceDir)
	if err != nil {
		// Fallback: use original path if absolute conversion fails
		return file
	}
	absOutput, err := filepath.Abs(g.outputDir)
	if err != nil {
		// Fallback: use original path if absolute conversion fails
		return file
	}
	if rel, err := filepath.Rel(absOutput, absSource); err == nil {
		if rel == "." {
			return file
		}
		return filepath.Join(rel, file)
	}
	// Fallback: use original path if relative path calculation fails
	return file
}

// collectIncludePaths recursively collects export_include_dirs from a module and its dependencies.
// These directories are added to the compiler's include path (-I flags).
//
// It traverses cc_library_headers, header_libs, shared_libs, and deps to find all exported headers.
//
// Parameters:
//   - moduleName: Name of the module to collect includes for
//   - visited: Map to track visited modules (prevent cycles)
//
// Returns:
//   - Slice of include directory paths
//
// Algorithm:
//  1. Check if already visited (return nil to prevent infinite loop)
//  2. Mark current module as visited
//  3. If cc_library_headers module, add its export_include_dirs
//  4. Add module's own export_include_dirs
//  5. Add directories from exported_headers (.h files)
//  6. Recursively collect from header_libs, shared_libs, and deps
//
// Edge cases:
//   - Module doesn't exist: returns empty slice
//   - Circular dependencies: prevented by visited map
//   - Duplicate directories: deduplicated via seen map
func (g *Generator) collectIncludePaths(moduleName string, visited map[string]bool) []string {

	if visited[moduleName] {

		return nil

	}

	visited[moduleName] = true

	m, ok := g.modules[moduleName]

	if !ok || m == nil {

		// Module doesn't exist or is nil - return empty slice not nil

		return []string{}

	}

	var includes []string
	seen := make(map[string]bool)

	// Check if this is a cc_library_headers module
	if m.Type == "cc_library_headers" {
		dirs := getExportIncludeDirs(m)
		for _, dir := range dirs {
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	// Check if this is a config_gen module — expose configdir as include path
	if m.Type == "config_gen" {
		configdir := GetStringProp(m, "configdir")
		if configdir != "" {
			dir := filepath.ToSlash(configdir)
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	// Get direct export_include_dirs
	dirs := getExportIncludeDirs(m)
	for _, dir := range dirs {
		if !seen[dir] {
			includes = append(includes, dir)
			seen[dir] = true
		}
	}

	// Collect directories from exported_headers (individual .h files)
	exportedHeaders := getExportedHeaders(m)
	for _, h := range exportedHeaders {
		dir := filepath.Dir(h)
		if dir != "" && dir != "." && !seen[dir] {
			includes = append(includes, dir)
			seen[dir] = true
		}
	}

	// Collect from header_libs (cc_library_headers dependencies)
	headerLibs := GetListProp(m, "header_libs")
	for _, dep := range headerLibs {
		depName := strings.TrimPrefix(dep, ":")
		depIncludes := g.collectIncludePaths(depName, visited)
		for _, dir := range depIncludes {
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	// Collect from shared_libs (shared library dependencies exporting headers)
	sharedLibs := GetListProp(m, "shared_libs")
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		depIncludes := g.collectIncludePaths(depName, visited)
		for _, dir := range depIncludes {
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	// Recursively collect from deps (option B: transitive)
	deps := GetListProp(m, "deps")
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		depIncludes := g.collectIncludePaths(depName, visited)
		for _, dir := range depIncludes {
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	return includes
}

// collectExportCflags recursively collects export_cflags from a module and its dependencies.
// These flags are added to the compiler flags (CFlags) of modules that depend on this module.
//
// Parameters:
//   - moduleName: Name of the module to collect flags for
//   - visited: Map to track visited modules (prevent cycles)
//
// Returns:
//   - Slice of exported C flags (de-duplicated)
//
// Algorithm:
//  1. Check if already visited (return nil to prevent infinite loop)
//  2. Mark current module as visited
//  3. Add module's own export_cflags
//  4. Recursively collect from header_libs, shared_libs, and deps
//
// Edge cases:
//   - Module doesn't exist: returns empty slice
//   - Circular dependencies: prevented by visited map
//   - Duplicate flags: deduplicated via seen map
func (g *Generator) collectExportCflags(moduleName string, visited map[string]bool) []string {
	if visited[moduleName] {
		return nil
	}
	visited[moduleName] = true

	m, ok := g.modules[moduleName]
	if !ok || m == nil {
		return []string{}
	}

	var flags []string
	seen := make(map[string]bool)

	// Get direct export_cflags
	cfgs := getExportCflags(m)
	for _, flag := range cfgs {
		if !seen[flag] {
			flags = append(flags, flag)
			seen[flag] = true
		}
	}

	// Collect from header_libs
	headerLibs := GetListProp(m, "header_libs")
	for _, dep := range headerLibs {
		depName := strings.TrimPrefix(dep, ":")
		depFlags := g.collectExportCflags(depName, visited)
		for _, flag := range depFlags {
			if !seen[flag] {
				flags = append(flags, flag)
				seen[flag] = true
			}
		}
	}

	// Collect from shared_libs
	sharedLibs := GetListProp(m, "shared_libs")
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		depFlags := g.collectExportCflags(depName, visited)
		for _, flag := range depFlags {
			if !seen[flag] {
				flags = append(flags, flag)
				seen[flag] = true
			}
		}
	}

	// Collect from deps
	deps := GetListProp(m, "deps")
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		depFlags := g.collectExportCflags(depName, visited)
		for _, flag := range depFlags {
			if !seen[flag] {
				flags = append(flags, flag)
				seen[flag] = true
			}
		}
	}

	return flags
}

// collectExportLdflags recursively collects export_ldflags from a module and its dependencies.
// These flags are added to the linker flags (LdFlags) of modules that depend on this module.
//
// Parameters:
//   - moduleName: Name of the module to collect flags for
//   - visited: Map to track visited modules (prevent cycles)
//
// Returns:
//   - Slice of exported linker flags (de-duplicated)
//
// Algorithm:
//  1. Check if already visited (return nil to prevent infinite loop)
//  2. Mark current module as visited
//  3. Add module's own export_ldflags
//  4. Recursively collect from header_libs, shared_libs, and deps
//
// Edge cases:
//   - Module doesn't exist: returns empty slice
//   - Circular dependencies: prevented by visited map
//   - Duplicate flags: deduplicated via seen map
func (g *Generator) collectExportLdflags(moduleName string, visited map[string]bool) []string {
	if visited[moduleName] {
		return nil
	}
	visited[moduleName] = true

	m, ok := g.modules[moduleName]
	if !ok || m == nil {
		return []string{}
	}

	var flags []string
	seen := make(map[string]bool)

	// Get direct export_ldflags
	ldflags := getExportLdflags(m)
	for _, flag := range ldflags {
		if !seen[flag] {
			flags = append(flags, flag)
			seen[flag] = true
		}
	}

	// Collect from header_libs
	headerLibs := GetListProp(m, "header_libs")
	for _, dep := range headerLibs {
		depName := strings.TrimPrefix(dep, ":")
		depFlags := g.collectExportLdflags(depName, visited)
		for _, flag := range depFlags {
			if !seen[flag] {
				flags = append(flags, flag)
				seen[flag] = true
			}
		}
	}

	// Collect from shared_libs
	sharedLibs := GetListProp(m, "shared_libs")
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		depFlags := g.collectExportLdflags(depName, visited)
		for _, flag := range depFlags {
			if !seen[flag] {
				flags = append(flags, flag)
				seen[flag] = true
			}
		}
	}

	// Collect from deps
	deps := GetListProp(m, "deps")
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		depFlags := g.collectExportLdflags(depName, visited)
		for _, flag := range depFlags {
			if !seen[flag] {
				flags = append(flags, flag)
				seen[flag] = true
			}
		}
	}

	return flags
}

// Generate writes the ninja build file content to the provided writer.
//
// It implements the main ninja generation pipeline:
//  1. Write header comments and regeneration rule
//  2. Write builddir variable if not "."
//  3. Render and write ninja rules for all used module types
//  4. For each module level (from topological sort):
//     - Collect include paths for the module
//     - For each architecture in multilib:
//     - Generate ninja edge for the module
//     - Add Java classpath dependencies if needed
//     - Add data file dependencies if needed
//     - Add include directories to compile commands
//     - Generate dist edges if needed
//  5. Write clean rule if there are outputs
//  6. Write phony targets for each module
//
// Parameters:
//   - w: Writer to output ninja content to
//
// Returns:
//   - nil on successful generation
//   - error if topological sort fails or other errors occur
//
// Edge cases:
//   - Empty module list: generates minimal ninja file with comments
//   - Modules without source files: generates phony targets only
//   - Multilib builds: generates separate edges for each architecture
//   - cc_library_headers: generates no compile edge, only provides includes
func (g *Generator) Generate(w io.Writer) error {
	nw := NewWriter(w)

	nw.Comment("Generated by minibp")
	nw.Comment("")

	if g.regenCmd != "" && len(g.inputFiles) > 0 { // Write regeneration rule only if command and input files are set
		fmt.Fprintf(w, "rule regen\n command = %s\n\n", ninjaEscape(g.regenCmd))
		fmt.Fprintf(w, "build %s: regen %s\n\n", ninjaEscape(g.outputFile), strings.Join(escapeList(g.inputFiles), " "))
	}

	if g.outputDir != "." && g.outputDir != "" { // Set builddir variable for non-current output directory
		nw.Variable("builddir", ".")
		nw.Comment("")
	}

	ctx := g.ruleRenderContext()
	usedModuleTypes := g.collectUsedModuleTypes()
	writtenNinjaRules := make(map[string]bool)

	for _, moduleType := range usedModuleTypes {
		if rule, ok := g.rules[moduleType]; ok {
			ruleDef := rule.NinjaRule(ctx)
			if ruleDef == "" {
				continue
			}

			// Split multiple rules in the same definition
			// Each rule starts with "rule " prefix
			lines := strings.Split(ruleDef, "\n")
			var currentRuleName string

			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" {
					continue
				}
				// Check if this is a rule definition line
				if strings.HasPrefix(trimmed, "rule ") {
					// Extract rule name
					parts := strings.Fields(trimmed)
					if len(parts) >= 2 {
						currentRuleName = parts[1]
						// Skip if already written
						if writtenNinjaRules[currentRuleName] {
							currentRuleName = ""
							continue
						}
						writtenNinjaRules[currentRuleName] = true
					}
				}
				// Skip rule name line if we're skipping this rule
				if currentRuleName == "" {
					continue
				}
				// Add leading space for attribute lines (Ninja syntax requires this)
				if i > 0 && !strings.HasPrefix(trimmed, "rule ") {
					fmt.Fprintf(w, " %s\n", trimmed)
				} else {
					fmt.Fprintf(w, "%s\n", trimmed)
				}
			}
		}
	}

	levels, err := g.graph.TopoSort()
	if err != nil { // Topological sort failed: return error immediately
		return err
	}

	var allOutputs []string
	seenCleanOutputs := make(map[string]bool)
	sourceDir := g.sourceDir
	if sourceDir == "." {
		absPath, _ := filepath.Abs(g.sourceDir)
		sourceDir = filepath.Base(absPath)
	}

	type phonyInfo struct {
		phonyName string
		outputs   []string
	}
	var phonyEntries []phonyInfo
	seenPhony := make(map[string]bool)
	allPhonyTargets := make([]string, 0)
	seenAllTargets := make(map[string]bool)

	for _, level := range levels {
		for _, moduleName := range level {
			m, ok := g.modules[moduleName]
			if !ok || m == nil {
				continue
			}

			rule, ok := g.rules[m.Type]
			if !ok {
				continue
			}

			includes := g.collectIncludePaths(moduleName, make(map[string]bool))

			if strings.HasPrefix(m.Type, "cc_") || strings.HasPrefix(m.Type, "cpp_") {
				includeSeen := make(map[string]bool)
				for _, inc := range includes {
					includeSeen[inc] = true
				}
				if !includeSeen["."] {
					includes = append(includes, ".")
				}
				srcs := getSrcs(m)
				for _, src := range srcs {
					dir := filepath.Dir(src)
					if dir != "." && !includeSeen[dir] {
						includes = append(includes, dir)
						includeSeen[dir] = true
					}
				}
				for _, dir := range getLocalIncludeDirs(m) {
					if !includeSeen[dir] {
						includes = append(includes, dir)
						includeSeen[dir] = true
					}
				}
				relPrefix := g.getRelativePath("")
				for _, dir := range getSystemIncludeDirs(m) {
					flag := "-isystem " + dir
					if relPrefix != "" && relPrefix != "." {
						flag = "-isystem " + filepath.Join(relPrefix, dir)
					}
					if !includeSeen[flag] {
						includes = append(includes, flag)
						includeSeen[flag] = true
					}
				}
			}

			archs := g.archsForModule(m)
			for _, arch := range archs {
				archCtx := ctx
				if arch != g.arch {
					archCtx = g.ruleRenderContextForArch(arch)
				}

				// Collect export_cflags and export_ldflags from dependencies
				// Use separate visited maps to allow collecting both from same dependencies
				exportVisited1 := make(map[string]bool)
				exportCFlags := g.collectExportCflags(moduleName, exportVisited1)
				exportVisited2 := make(map[string]bool)
				exportLdFlags := g.collectExportLdflags(moduleName, exportVisited2)
				if len(exportCFlags) > 0 {
					archCtx.ExportCFlags = strings.Join(exportCFlags, " ")
				}
				if len(exportLdFlags) > 0 {
					archCtx.ExportLdFlags = strings.Join(exportLdFlags, " ")
				}

				edgeDef := rule.NinjaEdge(m, archCtx)

				if edgeDef == "" && m.Type != "cc_library_headers" {
					continue
				}

				if edgeDef != "" {
					if strings.HasPrefix(m.Type, "java_") {
						edgeDef = g.addJavaDepsToEdge(m, edgeDef)
					}
					edgeDef = g.addDataDepsToEdge(m, edgeDef, archCtx)
					edgeDef = g.addIncludesToEdge(edgeDef, includes)
					edgeDef = g.addConfigGenDepsToEdge(m, edgeDef, archCtx)
					edgeDef += g.distEdgesForModule(m, archCtx)
				}

				for _, out := range collectBuildOutputs(edgeDef) {
					if !seenCleanOutputs[out] && !strings.HasSuffix(out, ".run") && out != "build.ninja" {
						seenCleanOutputs[out] = true
						allOutputs = append(allOutputs, out)
					}
				}

				srcs := getSrcs(m)
				if len(srcs) == 0 {
					desc := rule.Desc(m, "")
					if desc != "" {
						nw.Desc(sourceDir, moduleName, desc, "")
					}
				} else {
					for _, src := range srcs {
						desc := rule.Desc(m, src)
						if desc != "" {
							nw.Desc(sourceDir, moduleName, desc, src)
						}
					}
				}

				fmt.Fprint(w, g.adjustPaths(edgeDef))

				outputs := rule.Outputs(m, archCtx)
				if len(outputs) == 0 {
					continue
				}
				skip := false
				for _, out := range outputs {
					if out == moduleName {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
				phonyName := moduleName
				if arch != "" && arch != g.arch && len(archs) > 1 {
					phonyName = moduleName + "_" + arch
				}
				// Skip config_gen type - it has proper outputs and build edges
				// Adding it to phonyEntries would override the actual build rule
				if m.Type == "config_gen" {
					if moduleName != "all" && moduleName != "clean" {
						if !seenAllTargets[phonyName] {
							seenAllTargets[phonyName] = true
							allPhonyTargets = append(allPhonyTargets, phonyName)
						}
					}
					continue
				}

				if !seenPhony[phonyName] {
					seenPhony[phonyName] = true
					escapedOutputs := make([]string, 0, len(outputs))
					for _, out := range outputs {
						escapedOutputs = append(escapedOutputs, g.adjustBuildPath(out, true))
					}
					phonyEntries = append(phonyEntries, phonyInfo{phonyName: phonyName, outputs: escapedOutputs})
				}

				if moduleName != "all" && moduleName != "clean" && m.Type != "cc_library_headers" {
					if !seenAllTargets[phonyName] {
						seenAllTargets[phonyName] = true
						allPhonyTargets = append(allPhonyTargets, phonyName)
					}
				}
			}
		}
	}

	for _, pe := range phonyEntries {
		fmt.Fprintf(w, "build %s: phony %s\n", ninjaEscapePath(pe.phonyName), strings.Join(pe.outputs, " "))
	}

	// Add 'all' target that depends on all other targets (excluding clean)
	fmt.Fprintf(w, "build all: phony %s\n\n", strings.Join(allPhonyTargets, " "))
	fmt.Fprintf(w, "default all\n\n")

	// Add 'clean' target - removes build artifacts but preserves build.ninja
	if len(allOutputs) > 0 {
		// The command for the CLEAN rule needs to handle the case where regenCmd is empty.
		// If regenCmd is not empty, it should be properly escaped.
		cleanCmd := "ninja -t clean"
		if g.regenCmd != "" {
			// Escape the regen command to prevent command injection.
			escapedRegenCmd := shellEscape(g.regenCmd)
			cleanCmd += " && " + escapedRegenCmd
		}

		fmt.Fprintf(w, "rule CLEAN\n command = %s\n\n", cleanCmd)
		fmt.Fprintf(w, "build clean: CLEAN\n")
	}

	return nw.Flush()
}

// archFlags returns compiler and linker flags for a given target architecture.
// These flags enable 32-bit or 64-bit code generation as appropriate.
//
// Parameters:
//   - arch: Target architecture name (e.g., "x86", "x86_64", "arm", "arm64")
//
// Returns:
//   - cflags: Compiler flags for the architecture
//   - ldflags: Linker flags for the architecture
//
// Edge cases:
//   - Unknown architecture: returns nil, nil
func archFlags(arch string) (cflags []string, ldflags []string) {
	switch arch {
	case "x86":
		return []string{"-m32"}, []string{"-m32"}
	case "x86_64":
		return []string{"-m64"}, []string{"-m64"}
	case "arm":
		return []string{"-march=armv7-a"}, []string{"-march=armv7-a"}
	case "arm64":
		return []string{"-march=armv8-a"}, []string{"-march=armv8-a"}
	default:
		return nil, nil
	}
}

// ruleRenderContext returns the rule render context for the generator's default architecture.
// This is a convenience method that delegates to ruleRenderContextForArch.
//
// Returns:
//   - RuleRenderContext configured for the default architecture
func (g *Generator) ruleRenderContext() RuleRenderContext {
	return g.ruleRenderContextForArch(g.arch)
}

// ruleRenderContextForArch returns the rule render context for a specific target architecture.
// This helper method configures the context with architecture-specific flags and toolchain settings.
// It is called when generating rules for each architecture in multilib mode.
//
// Parameters:
//   - arch: Target architecture name (e.g., "arm", "arm64", "x86", "x86_64")
//
// Returns:
//   - RuleRenderContext configured for the specified architecture
//
// The context includes:
//   - Compiler (CC, CXX, AR) with architecture-specific flags
//   - LTO flags if enabled
//   - Sysroot if configured
//   - GOOS/GOARCH for Go cross-compilation
func (g *Generator) ruleRenderContextForArch(arch string) RuleRenderContext {
	tc := g.toolchain
	if tc.CC == "" {
		tc.CC = "gcc"
	}
	if tc.CXX == "" {
		tc.CXX = "g++"
	}
	if tc.AR == "" {
		tc.AR = "ar"
	}
	archCFlags, archLdFlags := archFlags(arch)
	tc.CFlags = append(append([]string{}, tc.CFlags...), archCFlags...)
	tc.LdFlags = append(append([]string{}, tc.LdFlags...), archLdFlags...)

	ctx := DefaultRuleRenderContext()
	ctx.CC = tc.CC
	ctx.CXX = tc.CXX
	ctx.AR = tc.AR
	ctx.LD = tc.LD
	ctx.CFlags = strings.Join(tc.CFlags, " ")
	ctx.LdFlags = strings.Join(tc.LdFlags, " ")
	ctx.Sysroot = tc.Sysroot
	ctx.Ccache = tc.Ccache
	ctx.Lto = tc.Lto
	if arch != "" {
		ctx.ArchSuffix = "_" + arch
	}
	if ctx.Sysroot != "" {
		sysrootFlag := "--sysroot=" + ctx.Sysroot
		ctx.CFlags = strings.TrimSpace(ctx.CFlags + " " + sysrootFlag)
		ctx.LdFlags = strings.TrimSpace(ctx.LdFlags + " " + sysrootFlag)
	}
	if g.targetOS != "" {
		ctx.GOOS = g.targetOS
	}
	if arch != "" {
		ctx.GOARCH = arch
	} else {
		ctx.GOARCH = g.arch
	}
	ctx.PathPrefix = g.pathPrefix
	ctx.Modules = g.modules
	ctx.GoModulePath, ctx.GoImportPrefix = detectGoModuleContext(g.sourceDir)
	return ctx
}

var goModCache struct {
	sync.RWMutex
	m map[string]goModResult
}

type goModResult struct {
	modulePath   string // Go module path from go.mod (e.g., "github.com/user/repo")
	importPrefix string // Relative import path from module root to source directory
}

// detectGoModuleContext detects Go module context for the given source directory.
// It uses a global cache to avoid repeated filesystem lookups for the same directory.
//
// Parameters:
//   - sourceDir: Source directory to detect Go module context for
//
// Returns:
//   - modulePath: Go module path from go.mod (empty if not found)
//   - importPrefix: Relative import prefix from module root to sourceDir (empty if not found)
//
// Edge cases:
//   - Cache hit: returns cached result immediately
//   - No go.mod found in sourceDir or parent directories: returns empty strings
//   - go.mod has no valid module line: returns empty strings
func detectGoModuleContext(sourceDir string) (modulePath string, importPrefix string) {
	goModCache.RLock()
	r, ok := goModCache.m[sourceDir]
	goModCache.RUnlock()
	if ok {
		return r.modulePath, r.importPrefix
	}

	mp, ip := detectGoModuleContextUncached(sourceDir)

	goModCache.Lock()
	if goModCache.m == nil {
		goModCache.m = make(map[string]goModResult)
	}
	goModCache.m[sourceDir] = goModResult{modulePath: mp, importPrefix: ip}
	goModCache.Unlock()
	return mp, ip
}

// detectGoModuleContextUncached detects Go module context without using the global cache.
// It searches parent directories for go.mod and parses the module path.
//
// Parameters:
//   - sourceDir: Absolute path to source directory
//
// Returns:
//   - modulePath: Go module path from go.mod (empty if not found)
//   - importPrefix: Relative import prefix from module root to sourceDir (empty if not found)
//
// Edge cases:
//   - No go.mod found: returns empty strings
//   - go.mod parse error: returns empty strings
//   - sourceDir is module root: importPrefix is empty string
func detectGoModuleContextUncached(sourceDir string) (modulePath string, importPrefix string) {
	absSourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", ""
	}

	dir := absSourceDir
	for {
		goModPath := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(goModPath)
		if err == nil {
			modulePath = parseGoModulePath(string(data))
			if modulePath == "" {
				return "", ""
			}
			rel, err := filepath.Rel(dir, absSourceDir)
			if err != nil || rel == "." {
				return modulePath, ""
			}
			return modulePath, filepath.ToSlash(rel)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

// parseGoModulePath extracts the Go module path from go.mod file content.
// It parses the "module" directive from the provided go.mod content string.
//
// Parameters:
//   - goMod: Raw content of go.mod file as a string
//
// Returns:
//   - Module path string (e.g., "github.com/user/repo")
//   - Empty string if no valid module directive is found
//
// Edge cases:
//   - Empty goMod content: returns empty string
//   - No "module" line present: returns empty string
//   - Commented "module" lines (starting with //) are skipped
//   - Multiple "module" lines: returns the first valid occurrence
func parseGoModulePath(goMod string) string {
	for _, line := range strings.Split(goMod, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "module "))
	}
	return ""
}

// shellQuote wraps a command-line argument in double quotes,
// escaping any embedded double quotes with backslashes.
// This is needed for rsp files on Windows.
//
// Parameters:
//   - arg: Argument to quote
//
// Returns:
//   - Quoted and escaped argument
func shellQuote(arg string) string {
	return "\"" + strings.ReplaceAll(arg, "\"", "\\\"") + "\""
}

// cleanCommand generates the shell command to delete build outputs.
// It uses platform-specific syntax (cmd /c for Windows, rm -f for Unix).
//
// Parameters:
//   - outputs: List of file paths to delete
//
// Returns:
//   - Platform-appropriate delete command
func cleanCommand(outputs []string) string {
	quoted := make([]string, 0, len(outputs))
	for _, out := range outputs {
		quoted = append(quoted, shellQuote(out))
	}
	if runtime.GOOS == "windows" {
		return "cmd /c del /q " + strings.Join(quoted, " ")
	}
	return "rm -f " + strings.Join(quoted, " ")
}

// collectBuildOutputs extracts output file paths from a ninja edge definition.
// It parses "build" lines to find the output filenames.
//
// Parameters:
//   - edge: Ninja edge definition string
//
// Returns:
//   - List of output file paths (unescaped)
//
// Edge cases:
//   - Empty edge: returns nil
//   - No "build" prefix: returns nil
//   - Malformed lines: skipped
func collectBuildOutputs(edge string) []string {
	if edge == "" {
		return nil
	}

	var outputs []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(edge, "\n") {
		if !strings.HasPrefix(line, "build ") {
			continue
		}

		parsed, ok := parseBuildLine(line)
		if !ok {
			continue
		}

		for _, out := range parsed.Outputs {
			rawOut := ninjaUnescape(out)
			if !seen[rawOut] {
				seen[rawOut] = true
				outputs = append(outputs, rawOut)
			}
		}
	}

	return outputs
}

// parsedBuildLine represents a parsed "build" statement in ninja syntax.
// It contains the outputs, rule name, inputs, and order-only dependencies.
type parsedBuildLine struct {
	Outputs []string // Output file paths
	Rule    string   // Rule name (e.g., "cc_compile")
	Inputs  []string // Input file paths
	Deps    []string // Order-only dependencies
}

// ninjaUnescape removes ninja escape sequences from a string.
// It converts "$x" to just "x" (single character after $).
//
// Parameters:
//   - s: String with ninja escape sequences
//
// Returns:
//   - String with escapes removed
func ninjaUnescape(s string) string {
	if s == "" {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) {
			i++
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// splitNinjaEscapedFields splits a string into fields, respecting ninja $ escaping.
// Spaces and tabs separate fields, but $ escapes the separator.
//
// Parameters:
//   - s: String to split
//
// Returns:
//   - Slice of field strings
//
// Edge cases:
//   - Empty input: returns nil
//   - Trailing $: treated as literal $
func splitNinjaEscapedFields(s string) []string {
	if s == "" {
		return nil
	}

	var fields []string
	var cur strings.Builder
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '$' {
			escaped = true
			cur.WriteByte(ch)
			continue
		}
		if ch == ' ' || ch == '\t' {
			if cur.Len() > 0 {
				fields = append(fields, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(ch)
	}
	if escaped {
		cur.WriteByte('$')
	}
	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}
	return fields
}

// parseBuildLine parses a ninja "build" line into its components.
// It extracts outputs, rule name, inputs, and order-only dependencies.
//
// Parameters:
//   - line: A single line from ninja build file
//
// Returns:
//   - parsedBuildLine with components, or false if parsing fails
//
// Edge cases:
//   - Missing "build " prefix: returns false
//   - Missing ": " separator: returns false
//   - No outputs or rule: returns false
func parseBuildLine(line string) (parsedBuildLine, bool) {
	if !strings.HasPrefix(line, "build ") {
		return parsedBuildLine{}, false
	}

	body := strings.TrimPrefix(line, "build ")
	separator := strings.Index(body, ": ")
	if separator == -1 {
		return parsedBuildLine{}, false
	}

	outputs := splitNinjaEscapedFields(strings.TrimSpace(body[:separator]))
	rest := strings.TrimSpace(body[separator+1:])
	parts := splitNinjaEscapedFields(rest)
	if len(outputs) == 0 || len(parts) == 0 {
		return parsedBuildLine{}, false
	}

	parsed := parsedBuildLine{Outputs: outputs, Rule: parts[0]}
	current := &parsed.Inputs
	for _, part := range parts[1:] {
		if part == "|" {
			current = &parsed.Deps
			continue
		}
		*current = append(*current, part)
	}
	return parsed, true
}

// addIncludesToEdge adds include directories to compile commands within a ninja edge.
// It inserts -I and -isystem flags into the compile rule's flags variable.
//
// Parameters:
//   - edge: Ninja edge definition string
//   - includes: List of include directory paths
//
// Returns:
//   - Modified edge with include flags added
//
// Algorithm:
//  1. Build include flags string (one per directory)
//  2. Find the compile rule's flags line
//  3. Append include flags to that line
//  4. Handle system includes (-isystem) specially
func (g *Generator) addIncludesToEdge(edge string, includes []string) string {
	if len(includes) == 0 {
		return edge
	}

	var includeFlagParts []string
	relPrefix := g.getRelativePath("")
	for _, inc := range includes {
		if strings.HasPrefix(inc, "-isystem") {
			includeFlagParts = append(includeFlagParts, inc)
			continue
		}
		if relPrefix != "" && relPrefix != "." {
			inc = filepath.Join(relPrefix, inc)
		}
		includeFlagParts = append(includeFlagParts, "-I"+inc)
	}
	includeFlags := " " + strings.Join(includeFlagParts, " ")

	lines := strings.Split(edge, "\n")
	compileFlags := false
	for i, line := range lines {
		if strings.HasPrefix(line, "build ") {
			compileFlags = strings.Contains(line, ": cc_compile ") ||
				strings.Contains(line, ": cpp_compile ") ||
				strings.Contains(line, ": cc_compile_lto ") ||
				strings.Contains(line, ": cpp_compile_lto ")
			continue
		}
		if compileFlags && strings.Contains(line, "flags =") && !strings.Contains(line, "#") {
			lines[i] = line + includeFlags
		}
	}

	return strings.Join(lines, "\n")
}

// javaDepOutputs finds .jar output files from a module's java dependencies.
// These are used to construct the classpath for compilation.
//
// When a Java module depends on other Java libraries, this function collects
// the .jar outputs from those dependencies to build the classpath.
//
// Parameters:
//   - moduleName: Name of the module to find dependencies for
//   - ctx: Rule render context
//
// Returns:
//   - List of .jar file paths from dependencies
//
// Edge cases:
//   - Module doesn't exist: returns nil
//   - Dependencies don't produce .jar: not included in output
//   - Duplicate outputs: deduplicated via seen map
func (g *Generator) javaDepOutputs(moduleName string, ctx RuleRenderContext) []string {
	m, ok := g.modules[moduleName]
	if !ok || m == nil {
		return nil
	}

	deps := GetListProp(m, "deps")
	if len(deps) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	outputs := make([]string, 0, len(deps))
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		depMod, ok := g.modules[depName]
		if !ok || depMod == nil {
			continue
		}
		rule, ok := g.rules[depMod.Type]
		if !ok {
			continue
		}
		for _, out := range rule.Outputs(depMod, ctx) {
			if strings.HasSuffix(out, ".jar") && !seen[out] {
				seen[out] = true
				outputs = append(outputs, out)
			}
		}
	}

	return outputs
}

// addJavaDepsToEdge adds Java classpath dependencies to a Java module's ninja edge.
// It adds the .jar files from deps as both implicit inputs and classpath.
//
// Parameters:
//   - m: Module to add dependencies to
//   - edge: Ninja edge definition string
//
// Returns:
//   - Modified edge with classpath added
func (g *Generator) addJavaDepsToEdge(m *parser.Module, edge string) string {
	depJars := g.javaDepOutputs(getName(m), g.ruleRenderContext())
	if len(depJars) == 0 {
		return edge
	}

	classpath := strings.Join(depJars, string(os.PathListSeparator))
	lines := strings.Split(edge, "\n")
	classPathLineIdx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "build ") {
			parts := strings.SplitN(line, ": ", 2)
			if len(parts) != 2 {
				continue
			}
			ruleAndInputs := strings.Fields(parts[1])
			if len(ruleAndInputs) == 0 {
				continue
			}
			ruleName := ruleAndInputs[0]
			// Add jar deps as implicit deps on javac_ and jar_ build edges
			if strings.HasPrefix(ruleName, "javac_") || strings.HasPrefix(ruleName, "jar_") {
				lines[i] = line + " | " + strings.Join(depJars, " ")
			}
			continue
		}

		if strings.Contains(line, "flags =") {
			lines[i] = line + " -classpath " + classpath
		}
		// Track existing class_path line
		if strings.Contains(line, "class_path =") {
			classPathLineIdx = i
		}
	}

	// If there's an existing class_path line, append the dep jars to it
	if classPathLineIdx >= 0 {
		lines[classPathLineIdx] = lines[classPathLineIdx] + " " + classpath
	}

	return strings.Join(lines, "\n")
}

// moduleDataOutputs resolves data file references from a module's data property.
// It handles both module references (":name") and plain file paths.
//
// Data files are files needed at runtime (e.g., assets, resources) that are
// bundled with the built artifact. This function resolves module references
// to their actual output paths.
//
// Parameters:
//   - m: Module to get data outputs from
//   - ctx: Rule render context
//
// Returns:
//   - List of resolved file paths
//
// Edge cases:
//   - Module reference (":name"): Resolved to module outputs
//   - Plain file path: Used as-is
//   - Duplicate outputs: deduplicated via seen map
func (g *Generator) moduleDataOutputs(m *parser.Module, ctx RuleRenderContext) []string {
	data := getData(m)
	if len(data) == 0 {
		return nil
	}

	var outputs []string
	seen := make(map[string]bool)
	for _, item := range data {
		if ref := ParseModuleReference(item); ref != nil {
			for _, out := range ResolveModuleOutputs(ref, g.modules, ctx) {
				if !seen[out] {
					seen[out] = true
					outputs = append(outputs, out)
				}
			}
			continue
		}
		if !seen[item] {
			seen[item] = true
			outputs = append(outputs, item)
		}
	}
	return outputs
}

// addDataDepsToEdge adds data file dependencies to a module's ninja edge.
// These are files needed at runtime (e.g., assets, resources).
//
// Parameters:
//   - m: Module to add data dependencies for
//   - edge: Ninja edge definition string
//   - ctx: Rule render context
//
// Returns:
//   - Modified edge with data dependencies added
func (g *Generator) addDataDepsToEdge(m *parser.Module, edge string, ctx RuleRenderContext) string {
	dataOutputs := g.moduleDataOutputs(m, ctx)
	if len(dataOutputs) == 0 {
		return edge
	}

	lines := strings.Split(edge, "\n")
	for i, line := range lines {
		parsed, ok := parseBuildLine(line)
		if !ok {
			continue
		}
		for _, dep := range dataOutputs {
			already := false
			for _, existing := range parsed.Deps {
				if ninjaUnescape(existing) == dep {
					already = true
					break
				}
			}
			if !already {
				parsed.Deps = append(parsed.Deps, ninjaEscapePath(dep))
			}
		}

		buildLine := "build " + strings.Join(parsed.Outputs, " ") + ": " + ninjaEscapePath(parsed.Rule)
		if len(parsed.Inputs) > 0 {
			buildLine += " " + strings.Join(parsed.Inputs, " ")
		}
		if len(parsed.Deps) > 0 {
			buildLine += " | " + strings.Join(parsed.Deps, " ")
		}
		lines[i] = buildLine
	}
	return strings.Join(lines, "\n")
}

// addConfigGenDepsToEdge adds config_gen output files as implicit dependencies
// to cc compile edges. This ensures that when a generated header changes,
// the source files that include it are recompiled.
func (g *Generator) addConfigGenDepsToEdge(m *parser.Module, edge string, ctx RuleRenderContext) string {
	deps := GetListProp(m, "deps")
	var genOutputs []string
	seen := make(map[string]bool)
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		depMod, ok := g.modules[depName]
		if !ok || depMod == nil || depMod.Type != "config_gen" {
			continue
		}
		rule, ok := g.rules[depMod.Type]
		if !ok {
			continue
		}
		for _, out := range rule.Outputs(depMod, ctx) {
			if !seen[out] {
				seen[out] = true
				genOutputs = append(genOutputs, out)
			}
		}
	}
	if len(genOutputs) == 0 {
		return edge
	}

	lines := strings.Split(edge, "\n")
	for i, line := range lines {
		parsed, ok := parseBuildLine(line)
		if !ok {
			continue
		}
		isCompile := strings.HasPrefix(parsed.Rule, "cc_compile") ||
			strings.HasPrefix(parsed.Rule, "cpp_compile")
		if !isCompile {
			continue
		}
		for _, out := range genOutputs {
			already := false
			for _, existing := range parsed.Deps {
				if ninjaUnescape(existing) == out {
					already = true
					break
				}
			}
			if !already {
				parsed.Deps = append(parsed.Deps, ninjaEscapePath(out))
			}
		}
		buildLine := "build " + strings.Join(parsed.Outputs, " ") + ": " + ninjaEscapePath(parsed.Rule)
		if len(parsed.Inputs) > 0 {
			buildLine += " " + strings.Join(parsed.Inputs, " ")
		}
		if len(parsed.Deps) > 0 {
			buildLine += " | " + strings.Join(parsed.Deps, " ")
		}
		lines[i] = buildLine
	}
	return strings.Join(lines, "\n")
}

// getDistSpecs collects distribution specifications from a module's properties.
// Dist specs define how built artifacts are copied to distribution directories.
// It checks both the "dist" single map property and "dists" list of maps property.
//
// Parameters:
//   - m: Module to collect distribution specifications from
//
// Returns:
//   - Slice of dist specification maps (may be empty)
//
// Edge cases:
//   - Module has no "dist" or "dists" properties: returns empty slice
//   - "dists" property is not a list: skips invalid entries
//   - Non-map entries in "dists" list: skipped
func getDistSpecs(m *parser.Module) []*parser.Map {
	var specs []*parser.Map
	if dist := GetMapProp(m, "dist"); dist != nil {
		specs = append(specs, dist)
	}
	if m.Map == nil {
		return specs
	}
	// Iterate through module properties to find "dists" list
	for _, prop := range m.Map.Properties {
		if prop.Name != "dists" {
			continue
		}
		list, ok := prop.Value.(*parser.List)
		if !ok {
			continue
		}
		for _, item := range list.Values {
			if mp, ok := item.(*parser.Map); ok {
				specs = append(specs, mp)
			}
		}
	}
	return specs
}

// distSpecString extracts a string value from a dist specification map.
// Dist specifications define how files are distributed to installation directories.
//
// Parameters:
//   - spec: The dist specification map
//   - key: The key to extract (e.g., "dir", "dest", "suffix")
//
// Returns:
//   - The string value, or empty string if not found
func distSpecString(spec *parser.Map, key string) string {
	if spec == nil {
		return ""
	}
	for _, prop := range spec.Properties {
		if prop.Name == key {
			if s, ok := prop.Value.(*parser.String); ok {
				return s.Value
			}
		}
	}
	return ""
}

// distEdgesForModule generates ninja build edges for dist specifications.
// Dist specifications define files that should be copied to distribution directories.
// This is used for installing files to system directories (/etc, /usr/share, etc.).
//
// Parameters:
//   - m: Module to generate dist edges for
//   - ctx: Rule render context
//
// Returns:
//   - Ninja build edges for distribution files
//
// The dist edges copy built artifacts to distribution directories:
//   - Base directory is "dist"
//   - "dir" property appends to the base path
//   - "dest" property renames the first output
//   - "suffix" property changes the output extension
func (g *Generator) distEdgesForModule(m *parser.Module, ctx RuleRenderContext) string {
	specs := getDistSpecs(m)
	if len(specs) == 0 {
		return ""
	}
	rule := g.rules[m.Type]
	if rule == nil {
		return ""
	}
	outputs := rule.Outputs(m, ctx)
	if len(outputs) == 0 {
		return ""
	}

	var edges strings.Builder
	for _, spec := range specs {
		dir := distSpecString(spec, "dir")
		dest := distSpecString(spec, "dest")
		suffix := distSpecString(spec, "suffix")
		baseDir := "dist"
		if dir != "" {
			baseDir = filepath.ToSlash(filepath.Join(baseDir, dir))
		}
		for i, out := range outputs {
			target := filepath.Base(out)
			if dest != "" && i == 0 {
				target = dest
			} else if suffix != "" {
				ext := filepath.Ext(target)
				base := strings.TrimSuffix(target, ext)
				target = base + suffix + ext
			}
			distOut := filepath.ToSlash(filepath.Join(baseDir, target))
			edges.WriteString(fmt.Sprintf("build %s: prebuilt_copy %s\n", ninjaEscapePath(distOut), ninjaEscapePath(out)))
		}
	}
	return edges.String()
}

// adjustPaths updates paths in ninja edge to be relative to output directory
// shouldPrefixInputPath determines if an input path should have the path prefix prepended.
// Input paths are prefixed when they:
//   - Don't start with $ (ninja variable)
//   - Don't already have the path prefix
//   - Don't start with / (absolute path)
//   - Don't start with .. (parent directory)
//   - Are file paths that need to be relative to build dir
//   - Are not generated files (.o, .jar, .stamp, .d)
//
// Parameters:
//   - path: File path to check
//
// Returns:
//   - true if the path should be prefixed
func (g *Generator) shouldPrefixInputPath(path string) bool {
	if g.pathPrefix == "" {
		return false
	}
	if strings.HasPrefix(path, "$") ||
		strings.HasPrefix(path, g.pathPrefix) ||
		strings.HasPrefix(path, "/") ||
		strings.HasPrefix(path, "..") {
		return false
	}
	// Don't prefix generated files that are in build dir
	if strings.HasSuffix(path, ".o") ||
		strings.HasSuffix(path, ".jar") ||
		strings.HasSuffix(path, ".stamp") ||
		strings.HasSuffix(path, ".d") {
		return false
	}
	// Prefix paths that contain / OR are shared library files (.so)
	return strings.Contains(path, "/") || strings.HasSuffix(path, ".so")
}

// shouldPrefixOutputPath determines if an output path should have the path prefix prepended.
// Output paths are prefixed when they:
//   - Are not empty
//   - Don't already have the path prefix
//   - Don't start with / (absolute path)
//   - Are file paths (contain /) not module names
//
// Parameters:
//   - path: File path to check
//
// Returns:
//   - true if the path should be prefixed
func (g *Generator) shouldPrefixOutputPath(path string) bool {
	if g.pathPrefix == "" {
		return false
	}
	return path != "" &&
		!strings.HasPrefix(path, g.pathPrefix) &&
		!strings.HasPrefix(path, "/") &&
		strings.Contains(path, "/")
}

// adjustBuildPath adjusts a single build path (input or output) by prepending the path prefix.
// The path prefix is used when the build directory differs from the source directory.
//
// Parameters:
//   - path: The path to adjust
//   - isOutput: true for output paths, false for input paths
//
// Returns:
//   - Adjusted and escaped path
func (g *Generator) adjustBuildPath(path string, isOutput bool) string {
	rawPath := ninjaUnescape(path)
	if isOutput {
		if g.shouldPrefixOutputPath(rawPath) {
			rawPath = g.pathPrefix + rawPath
		}
	} else if g.shouldPrefixInputPath(rawPath) {
		rawPath = g.pathPrefix + rawPath
	}
	return ninjaEscapePath(rawPath)
}

// adjustPaths updates all paths in a ninja edge to be relative to output directory.
// If a path prefix is configured, all input and output paths are adjusted.
// This handles cases where the build directory differs from the source directory.
//
// Parameters:
//   - edge: Ninja edge definition string
//
// Returns:
//   - Edge with adjusted paths
func (g *Generator) adjustPaths(edge string) string {
	if g.pathPrefix == "" {
		return escapeBuildLines(edge)
	}

	lines := strings.Split(edge, "\n")
	var adjustedLines []string

	for _, line := range lines {
		if !strings.HasPrefix(line, "build ") {
			adjustedLines = append(adjustedLines, line)
			continue
		}

		parsed, ok := parseBuildLine(line)
		if !ok {
			adjustedLines = append(adjustedLines, line)
			continue
		}

		outputs := make([]string, 0, len(parsed.Outputs))
		for _, output := range parsed.Outputs {
			outputs = append(outputs, g.adjustBuildPath(output, true))
		}

		inputs := make([]string, 0, len(parsed.Inputs))
		for _, input := range parsed.Inputs {
			inputs = append(inputs, g.adjustBuildPath(input, false))
		}

		deps := make([]string, 0, len(parsed.Deps))
		for _, dep := range parsed.Deps {
			deps = append(deps, g.adjustBuildPath(dep, false))
		}

		buildLine := "build " + strings.Join(outputs, " ") + ": " + ninjaEscapePath(parsed.Rule)
		if len(inputs) > 0 {
			buildLine += " " + strings.Join(inputs, " ")
		}
		if len(deps) > 0 {
			buildLine += " | " + strings.Join(deps, " ")
		}
		adjustedLines = append(adjustedLines, buildLine)
	}

	return strings.Join(adjustedLines, "\n")
}

// escapeBuildLines escapes paths in ninja build lines for proper output.
// This is used when no path prefix is configured but paths still need escaping.
// It parses each build line, unescapes and re-escapes paths for ninja compatibility.
//
// Parameters:
//   - edge: Ninja edge definition string
//
// Returns:
//   - Edge with properly escaped paths
func escapeBuildLines(edge string) string {
	lines := strings.Split(edge, "\n")
	for i, line := range lines {
		parsed, ok := parseBuildLine(line)
		if !ok {
			continue
		}

		outputs := make([]string, 0, len(parsed.Outputs))
		for _, output := range parsed.Outputs {
			outputs = append(outputs, ninjaEscapePath(ninjaUnescape(output)))
		}
		inputs := make([]string, 0, len(parsed.Inputs))
		for _, input := range parsed.Inputs {
			inputs = append(inputs, ninjaEscapePath(ninjaUnescape(input)))
		}
		deps := make([]string, 0, len(parsed.Deps))
		for _, dep := range parsed.Deps {
			deps = append(deps, ninjaEscapePath(ninjaUnescape(dep)))
		}

		buildLine := "build " + strings.Join(outputs, " ") + ": " + ninjaEscapePath(parsed.Rule)
		if len(inputs) > 0 {
			buildLine += " " + strings.Join(inputs, " ")
		}
		if len(deps) > 0 {
			buildLine += " | " + strings.Join(deps, " ")
		}
		lines[i] = buildLine
	}
	return strings.Join(lines, "\n")
}

// collectUsedModuleTypes returns a deduplicated list of module types used in the build.
// This is used to determine which ninja rules need to be generated.
//
// Returns:
//   - Sorted slice of unique module type names
func (g *Generator) collectUsedModuleTypes() []string {
	seen := make(map[string]bool)
	var result []string

	for _, m := range g.modules {
		if m == nil {
			continue
		}
		if !seen[m.Type] {
			seen[m.Type] = true
			result = append(result, m.Type)
		}
	}

	if g.hasDistTargets() && !seen["prebuilt_etc"] {
		seen["prebuilt_etc"] = true
		result = append(result, "prebuilt_etc")
	}

	return result
}

// hasDistTargets returns true if any module has dist specifications.
// This is used to determine if prebuilt_etc rule needs to be generated.
//
// Returns:
//   - true if any module has dist or dists properties
func (g *Generator) hasDistTargets() bool {
	for _, m := range g.modules {
		if m == nil {
			continue
		}
		if len(getDistSpecs(m)) > 0 {
			return true
		}
	}
	return false
}

// collectRulesForModule returns all ninja rule names used by a build rule
func (g *Generator) collectRulesForModule(rule BuildRule) []string {
	// Extract rule names from NinjaRule() output
	ruleDef := rule.NinjaRule(g.ruleRenderContext())
	var rules []string
	seen := make(map[string]bool)

	lines := strings.Split(ruleDef, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "rule ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				name := parts[1]
				if !seen[name] {
					seen[name] = true
					rules = append(rules, name)
				}
			}
		}
	}

	return rules
}
