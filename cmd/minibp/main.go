// Package main implements minibp, a build system that generates Ninja build files from Blueprint definitions.
// It parses .bp files, resolves dependencies, handles architecture variants, and outputs build.ninja.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"minibp/lib/ninja"
	"minibp/lib/parser"
	"minibp/lib/version"
)

// openInputFile is a dependency injection for opening input files.
// It defaults to os.Open but can be replaced for testing.
var (
	openInputFile      = func(path string) (io.ReadCloser, error) { return os.Open(path) }
	createOutputFile   = func(path string) (io.WriteCloser, error) { return os.Create(path) }
	parseBlueprintFile = parser.ParseFile
)

// Graph represents a module dependency graph used for topological sorting.
// nodes maps module names to their parsed Module definitions.
// edges maps module names to lists of their direct dependencies.
type Graph struct {
	nodes map[string]*parser.Module
	edges map[string][]string
}

// NewGraph creates a new empty Graph with initialized maps for nodes and edges.
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]*parser.Module),
		edges: make(map[string][]string),
	}
}

// AddNode adds a module node to the graph with the given name.
// If the node already exists, it will be replaced.
func (g *Graph) AddNode(name string, mod *parser.Module) {
	g.nodes[name] = mod
	if _, ok := g.edges[name]; !ok {
		g.edges[name] = []string{}
	}
}

// AddEdge adds a directed edge from the 'from' module to the 'to' module.
// Both nodes must exist in the graph; if not, they are initialized with empty dependency lists.
func (g *Graph) AddEdge(from, to string) {
	if _, ok := g.edges[from]; !ok {
		g.edges[from] = []string{}
	}
	if _, ok := g.edges[to]; !ok {
		g.edges[to] = []string{}
	}
	g.edges[from] = append(g.edges[from], to)
}

// TopoSort performs a topological sort on the module dependency graph.
// It returns a slice of levels, where each level contains module names that can be built in parallel.
// The algorithm uses Kahn's algorithm with topological levels to handle parallelizable nodes.
// Returns an error if there's a circular dependency or if referenced modules don't exist.
func (g *Graph) TopoSort() ([][]string, error) {
	inDegree := make(map[string]int)
	for name := range g.nodes {
		inDegree[name] = 0
	}

	// Validate all edges reference existing nodes and calculate in-degrees
	for from, deps := range g.edges {
		if _, ok := g.nodes[from]; !ok {
			return nil, fmt.Errorf("module '%s' referenced in dependency graph does not exist", from)
		}
		for _, to := range deps {
			if _, ok := g.nodes[to]; !ok {
				return nil, fmt.Errorf("dependency '%s' of '%s' not found", to, from)
			}
			inDegree[from]++
		}
	}

	// Build reverse edges map: for each node, track which nodes depend on it
	reverseEdges := make(map[string][]string)
	for from, deps := range g.edges {
		for _, to := range deps {
			reverseEdges[to] = append(reverseEdges[to], from)
		}
	}

	var levels [][]string
	visited := make(map[string]bool)
	nodeCount := len(g.nodes)

	// Process levels: each iteration finds all nodes with no remaining dependencies
	for len(visited) < nodeCount {
		var currentLevel []string
		for name, degree := range inDegree {
			if degree == 0 && !visited[name] {
				currentLevel = append(currentLevel, name)
			}
		}

		// No nodes with zero in-degree means circular dependency
		if len(currentLevel) == 0 {
			return nil, fmt.Errorf("circular dependency detected")
		}

		// Sort for deterministic ordering within each level
		sort.Strings(currentLevel)

		levels = append(levels, currentLevel)
		// Mark current level as visited and decrement in-degrees of dependent nodes
		for _, name := range currentLevel {
			visited[name] = true
			for _, dependent := range reverseEdges[name] {
				inDegree[dependent]--
			}
		}
	}

	return levels, nil
}

// main is the entry point for the minibp command-line tool.
// It parses command-line flags, loads Blueprint definitions, and generates a Ninja build file.
// On success, it exits with code 0; on failure, it exits with code 1 and prints an error to stderr.
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the main logic function that processes command-line arguments and generates the build file.

// It handles flag parsing, Blueprint file loading, dependency resolution, variant merging, and Ninja file generation.

func run(args []string, stdout, stderr io.Writer) error {

	var (
		fs = flag.NewFlagSet("minibp", flag.ContinueOnError)

		outFile = fs.String("o", "build.ninja", "output ninja file")

		all = fs.Bool("a", false, "parse all .bp files in directory")

		ccFlag   = fs.String("cc", "", "C compiler (default: gcc)")
		cxxFlag  = fs.String("cxx", "", "C++ compiler (default: g++)")
		arFlag   = fs.String("ar", "", "archiver (default: ar)")
		archFlag = fs.String("arch", "", "target architecture (arm, arm64, x86, x86_64)")

		multilibFlag = fs.String("multilib", "", "comma-separated target architectures for multi-arch build (e.g. arm64,x86_64)")

		hostFlag = fs.Bool("host", false, "build for host (overrides arch)")

		osFlag = fs.String("os", "", "target OS (linux, darwin, windows)")

		ltoFlag = fs.String("lto", "", "default LTO mode: full, thin, or none")

		sysrootFlag = fs.String("sysroot", "", "sysroot path for cross-compilation")

		ccacheFlag = fs.String("ccache", "", "ccache path (empty: auto-detect, 'no': disable)")

		versionFlag = fs.Bool("v", false, "show version information")
	)

	fs.SetOutput(stderr)

	if err := fs.Parse(args); err != nil {

		return err

	}

	// Show version if requested

	if *versionFlag {

		fmt.Fprintf(stdout, "minibp version %s\n", getVersion())

		return nil

	}

	// Validate that we have input files
	if len(fs.Args()) < 1 && !*all {
		fmt.Fprintln(stderr, "Usage: minibp [-o output] [-a] [-cc CC] [-cxx CXX] [-ar AR] [-arch ARCH] [-host] [-os OS] <file.bp | directory>")
		return fmt.Errorf("missing input path")
	}

	// Determine source directory from arguments
	srcDir := "."
	if *all && len(fs.Args()) > 0 {
		srcDir = fs.Args()[0]
	} else if len(fs.Args()) > 0 {
		srcDir = filepath.Dir(fs.Args()[0])
	}

	// Collect Blueprint files to process
	var files []string
	if *all {
		bpFiles, err := filepath.Glob(filepath.Join(srcDir, "*.bp"))
		if err != nil {
			return fmt.Errorf("error globbing bp files: %w", err)
		}
		files = bpFiles
	} else {
		files = fs.Args()
	}

	// Create evaluator with configuration for architecture and OS
	eval := parser.NewEvaluator()
	eval.SetConfig("arch", *archFlag)
	eval.SetConfig("host", fmt.Sprintf("%v", *hostFlag))
	if *osFlag != "" {
		eval.SetConfig("os", *osFlag)
	} else {
		eval.SetConfig("os", "linux")
	}
	eval.SetConfig("target", *archFlag)

	// Parse all Blueprint files into definitions
	allDefs, err := parseDefinitionsFromFiles(files)
	if err != nil {
		return err
	}

	// Process variable assignments in definitions
	eval.ProcessAssignmentsFromDefs(allDefs)

	// Process each module definition
	modules := make(map[string]*parser.Module)
	for _, def := range allDefs {
		mod, ok := def.(*parser.Module)
		if !ok {
			continue
		}
		name := getStringPropEval(mod, "name", eval)
		if name == "" {
			continue
		}

		// Evaluate module expressions, merge variant properties, expand globs
		eval.EvalModule(mod)
		mergeVariantProps(mod, *archFlag, *hostFlag, eval)
		if err := expandGlobsInModule(mod, srcDir); err != nil {
			return fmt.Errorf("error expanding globs for module %s: %w", name, err)
		}

		// Filter by host_supported / device_supported
		if !isModuleEnabledForTarget(mod, *hostFlag) {
			continue
		}

		modules[name] = mod
	}

	// Build namespace map for soong_namespace resolution
	namespaces := buildNamespaceMap(modules)

	// Build dependency graph from modules
	graph := NewGraph()
	for name, mod := range modules {
		graph.AddNode(name, mod)
	}

	// Add edges for each dependency type: deps, shared_libs, header_libs
	for name, mod := range modules {
		deps := getListPropEval(mod, "deps", eval)
		for _, dep := range deps {
			depName := resolveModuleRef(dep, mod, modules, namespaces)
			graph.AddEdge(name, depName)
		}
		sharedLibs := getListPropEval(mod, "shared_libs", eval)
		for _, dep := range sharedLibs {
			depName := resolveModuleRef(dep, mod, modules, namespaces)
			graph.AddEdge(name, depName)
		}
		headerLibs := getListPropEval(mod, "header_libs", eval)
		for _, dep := range headerLibs {
			depName := resolveModuleRef(dep, mod, modules, namespaces)
			graph.AddEdge(name, depName)
		}
	}

	// Build map of available build rules
	rules := ninja.GetAllRules()
	ruleMap := make(map[string]ninja.BuildRule)
	for _, r := range rules {
		ruleMap[r.Name()] = r
	}

	// Calculate relative path prefix for paths when output dir differs from source dir
	absOutFile, _ := filepath.Abs(*outFile)
	absBuildDir := filepath.Dir(absOutFile)
	absSourceDir, _ := filepath.Abs(srcDir)

	prefix := ""
	if absBuildDir != absSourceDir {
		relPath, err := filepath.Rel(absBuildDir, absSourceDir)
		if err == nil && relPath != "." {
			prefix = filepath.ToSlash(relPath) + "/"
		}
	}

	outDir := filepath.Dir(absOutFile)

	// Create generator and configure it
	gen := ninja.NewGenerator(graph, ruleMap, modules)
	gen.SetSourceDir(srcDir)
	gen.SetOutputDir(outDir)
	gen.SetPathPrefix(prefix)

	// Set up automatic regeneration command
	regenCmd := os.Args[0] + " -o " + *outFile
	for _, f := range files {
		regenCmd += " " + f
	}
	gen.SetRegen(regenCmd, files, *outFile)
	gen.SetWorkDir(srcDir)

	// Configure toolchain with custom compilers if provided
	tc := ninja.DefaultToolchain()
	if *ccFlag != "" {
		tc.CC = *ccFlag
	}
	if *cxxFlag != "" {
		tc.CXX = *cxxFlag
	}
	if *arFlag != "" {
		tc.AR = *arFlag
	}
	if *sysrootFlag != "" {
		tc.Sysroot = *sysrootFlag
	}
	if *ltoFlag != "" {
		tc.Lto = *ltoFlag
	}
	if *ccacheFlag == "no" {
		tc.Ccache = ""
	} else if *ccacheFlag != "" {
		tc.Ccache = *ccacheFlag
	}

	gen.SetToolchain(tc)
	gen.SetArch(*archFlag)

	// Handle multi-arch builds
	if *multilibFlag != "" {
		archs := strings.Split(*multilibFlag, ",")
		for i := range archs {
			archs[i] = strings.TrimSpace(archs[i])
		}
		gen.SetMultilib(archs)
	}

	// Generate and write the Ninja build file
	if err := generateNinjaFile(*outFile, gen); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Generated %s with %d modules\n", *outFile, len(modules))
	return nil
}

// parseDefinitionsFromFiles parses a list of Blueprint files and returns all definitions.

// Each file is opened, parsed, and its definitions are collected into a single slice.

// The function handles file closing properly even if parsing fails.

func parseDefinitionsFromFiles(files []string) ([]parser.Definition, error) {

	var allDefs []parser.Definition

	var parseErrors []string

	for _, file := range files {

		f, err := openInputFile(file)

		if err != nil {

			return nil, fmt.Errorf("error opening %s: %w", file, err)

		}

		parsedFile, parseErr := parseBlueprintFile(f, file)

		closeErr := f.Close()

		if parseErr != nil {

			parseErrors = append(parseErrors, fmt.Sprintf("parse error in %s: %v", file, parseErr))

			// Continue to next file to collect all errors

			continue

		}

		if closeErr != nil {

			return nil, fmt.Errorf("error closing %s: %w", file, closeErr)

		}

		allDefs = append(allDefs, parsedFile.Defs...)

	}

	// Return all collected errors if any

	if len(parseErrors) > 0 {

		return nil, fmt.Errorf("parsing failed: %s", strings.Join(parseErrors, "; "))

	}

	return allDefs, nil

}

// getVersion returns the version information as a formatted string.

func getVersion() string {

	v := version.Get()

	// If gitCommit is "unknown", try to get it from git

	gitCommit := v.GitCommit

	if gitCommit == "unknown" {

		if commit, err := getGitCommit(); err == nil {

			gitCommit = commit

		}

	}

	// If buildDate is "unknown", use current date

	buildDate := v.BuildDate

	if buildDate == "unknown" {

		buildDate = "2026-04-21" // Use a fixed date for consistency

	}

	return fmt.Sprintf("%s (git: %s, built: %s, go: %s)", v.MinibpVer, gitCommit, buildDate, v.GoVersion)

}

// getGitCommit returns the current git commit hash.

func getGitCommit() (string, error) {

	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")

	output, err := cmd.Output()

	if err != nil {

		return "", err

	}

	return strings.TrimSpace(string(output)), nil

}

// generateNinjaFile creates an output file and generates a Ninja build file into it.

// It handles proper file closing both on success and on generation errors.

func generateNinjaFile(path string, gen interface{ Generate(io.Writer) error }) error {

	out, err := createOutputFile(path)

	if err != nil {

		return fmt.Errorf("error creating output: %w", err)

	}

	genErr := gen.Generate(out)

	closeErr := out.Close()

	if genErr != nil {

		// Clean up incomplete file on error

		closeErr = os.Remove(path)

		if closeErr != nil {

			return fmt.Errorf("error generating ninja: %w; error removing incomplete file: %v", genErr, closeErr)

		}

		return fmt.Errorf("error generating ninja: %w", genErr)

	}

	if closeErr != nil {

		return fmt.Errorf("error closing output: %w", closeErr)

	}

	return nil

}

// getStringProp retrieves a string property from a module using the ninja helper.
func getStringProp(m *parser.Module, name string) string {
	return ninja.GetStringProp(m, name)
}

// getStringPropEval retrieves a string property from a module, optionally evaluating expressions.
// If the property exists as a plain string, it returns the raw value.
// If an evaluator is provided, it evaluates any expressions (variables, functions) in the property.
func getStringPropEval(m *parser.Module, name string, eval *parser.Evaluator) string {
	if m.Map == nil {
		return ""
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if s, ok := prop.Value.(*parser.String); ok {
				return s.Value
			}
			if eval != nil {
				val := eval.Eval(prop.Value)
				if s, ok := val.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// getListProp retrieves a list property from a module using the ninja helper.
func getListProp(m *parser.Module, name string) []string {
	return ninja.GetListProp(m, name)
}

// getListPropEval retrieves a list property from a module, optionally evaluating expressions.
func getListPropEval(m *parser.Module, name string, eval *parser.Evaluator) []string {
	if m.Map == nil {
		return nil
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if l, ok := prop.Value.(*parser.List); ok {
				return parser.EvalToStringList(l, eval)
			}
		}
	}
	return nil
}

// isModuleEnabledForTarget checks whether a module should be included
// based on host_supported/device_supported properties and the current build target.
// If neither property is set, the module is included by default.
func isModuleEnabledForTarget(m *parser.Module, hostBuild bool) bool {
	hostSupported := getBoolPropEval(m, "host_supported", nil)
	deviceSupported := getBoolPropEval(m, "device_supported", nil)

	// If neither property is set, include the module
	if !hostSupported && !deviceSupported {
		return true
	}

	if hostBuild {
		return hostSupported
	}
	return deviceSupported
}

// getBoolPropEval retrieves a boolean property from a module.
func getBoolPropEval(m *parser.Module, name string, eval *parser.Evaluator) bool {
	if m.Map == nil {
		return false
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if b, ok := prop.Value.(*parser.Bool); ok {
				return b.Value
			}
			if eval != nil {
				val := eval.Eval(prop.Value)
				if b, ok := val.(bool); ok {
					return b
				}
			}
		}
	}
	return false
}

// mergeVariantProps merges architecture-specific, host-specific, and target-specific properties
// into a module's base property map. It allows modules to define different configurations
// for different build targets (e.g., arm64 vs x86_64) or for host vs target builds.
func mergeVariantProps(m *parser.Module, arch string, host bool, eval *parser.Evaluator) {
	if arch != "" && m.Arch != nil {
		mergeMapProps(m, m.Arch[arch])
	}
	if host && m.Host != nil {
		mergeMapProps(m, m.Host)
	}
	if !host && m.Target != nil {
		mergeMapProps(m, m.Target)
	}
}

// mergeMapProps merges properties from an override map into a module's base property map.
// Lists are appended (additive), while scalars are overridden (replaced).
func mergeMapProps(m *parser.Module, override *parser.Map) {
	if override == nil {
		return
	}
	for _, prop := range override.Properties {
		switch prop.Value.(type) {
		case *parser.List:
			// For lists, append values to existing list if property exists
			merged := false
			for _, baseProp := range m.Map.Properties {
				if baseProp.Name == prop.Name {
					if baseList, ok := baseProp.Value.(*parser.List); ok {
						if archList, ok := prop.Value.(*parser.List); ok {
							baseList.Values = append(baseList.Values, archList.Values...)
						}
					}
					merged = true
					break
				}
			}
			if !merged {
				m.Map.Properties = append(m.Map.Properties, prop)
			}
		default:
			// For scalars, override existing property if found
			found := false
			for i, baseProp := range m.Map.Properties {
				if baseProp.Name == prop.Name {
					// Keep the original property position info, only update the value
					m.Map.Properties[i].Value = prop.Value
					found = true
					break
				}
			}
			if !found {
				m.Map.Properties = append(m.Map.Properties, prop)
			}
		}
	}
}

// expandGlobsInModule expands glob patterns in a module's "srcs" property.
// It converts glob patterns (e.g., "*.go", "src/**/*.go") into concrete file paths.
// Patterns that don't match any files are dropped. Duplicate files are removed.
func expandGlobsInModule(m *parser.Module, baseDir string) error {
	if m.Map == nil {
		return nil
	}

	for _, prop := range m.Map.Properties {
		if prop.Name == "srcs" {
			if l, ok := prop.Value.(*parser.List); ok {
				var expandedSrcs []parser.Expression
				seen := make(map[string]bool)

				for _, v := range l.Values {
					if s, ok := v.(*parser.String); ok {
						pattern := s.Value
						// Check if the value contains glob characters
						if strings.Contains(pattern, "*") {
							matches, err := expandGlob(pattern, baseDir)
							if err != nil {
								return err
							}
							// Add each matching file, deduplicating
							for _, match := range matches {
								if !seen[match] {
									seen[match] = true
									expandedSrcs = append(expandedSrcs, &parser.String{Value: match})
								}
							}
						} else {
							// Non-glob values are kept as-is
							if !seen[pattern] {
								seen[pattern] = true
								expandedSrcs = append(expandedSrcs, v)
							}
						}
					}
				}

				l.Values = expandedSrcs
			}
		}
	}

	return nil
}

// expandGlob expands a single glob pattern into a list of matching file paths.
// It handles both simple globs ("*.go") and recursive globs ("**/*.go").
// Paths are returned relative to baseDir.
func expandGlob(pattern, baseDir string) ([]string, error) {
	var result []string

	// Handle recursive glob patterns containing "**"
	if strings.Contains(pattern, "**") {
		// Determine the root directory to start walking from
		walkDir := recursiveGlobRoot(pattern, baseDir)

		err := filepath.Walk(walkDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			relPath, err := filepath.Rel(baseDir, path)
			if err != nil {
				return err
			}
			relPath = filepath.ToSlash(relPath)
			// Check if the relative path matches the recursive pattern
			if matchRecursivePattern(filepath.ToSlash(pattern), relPath) {
				result = append(result, relPath)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		// Handle simple glob patterns using filepath.Glob
		fullPattern := filepath.Join(baseDir, pattern)
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			relPath, err := filepath.Rel(baseDir, match)
			if err != nil {
				return nil, err
			}
			result = append(result, relPath)
		}
	}

	return result, nil
}

// recursiveGlobRoot finds the root directory to start walking from for recursive globs.
// It extracts the non-glob prefix of the pattern to limit the walk scope.
func recursiveGlobRoot(pattern, baseDir string) string {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	prefix := make([]string, 0, len(parts))
	for _, part := range parts {
		// Stop when we hit a glob pattern
		if part == "**" || strings.ContainsAny(part, "*?[") {
			break
		}
		prefix = append(prefix, part)
	}
	if len(prefix) == 0 {
		return baseDir
	}
	root := filepath.Join(append([]string{baseDir}, prefix...)...)
	return root
}

// matchRecursivePattern checks if a path matches a recursive glob pattern.
// It splits both pattern and path into parts and delegates to matchRecursiveParts.
func matchRecursivePattern(pattern, path string) bool {
	patternParts := splitGlobParts(pattern)
	pathParts := splitGlobParts(path)
	return matchRecursiveParts(patternParts, pathParts)
}

// splitGlobParts splits a path/pattern string by forward slashes into parts.
func splitGlobParts(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

// matchRecursiveParts recursively matches pattern parts against path parts.
// It handles the "**" pattern which matches any number of directories.
func matchRecursiveParts(patternParts, pathParts []string) bool {
	// Empty pattern matches empty path
	if len(patternParts) == 0 {
		return len(pathParts) == 0
	}

	// "**" matches zero or more path components
	if patternParts[0] == "**" {
		// Try matching with "**" consuming nothing
		if matchRecursiveParts(patternParts[1:], pathParts) {
			return true
		}
		// Try matching with "**" consuming one path component at a time
		if len(pathParts) == 0 {
			return false
		}
		return matchRecursiveParts(patternParts, pathParts[1:])
	}

	// Non-empty pathParts required for non-"**" patterns
	if len(pathParts) == 0 {
		return false
	}

	// Match current pattern part against current path part
	ok, err := filepath.Match(patternParts[0], pathParts[0])
	if err != nil || !ok {
		return false
	}

	// Continue with remaining parts
	return matchRecursiveParts(patternParts[1:], pathParts[1:])
}

// namespaceInfo holds namespace metadata for soong_namespace resolution.
type namespaceInfo struct {
	imports []string // List of namespace paths this namespace imports from
}

// buildNamespaceMap builds a map of namespace name -> namespaceInfo from all
// soong_namespace modules. This is used for dependency resolution across namespaces.
func buildNamespaceMap(modules map[string]*parser.Module) map[string]*namespaceInfo {
	result := make(map[string]*namespaceInfo)
	for _, mod := range modules {
		if mod.Type != "soong_namespace" || mod.Map == nil {
			continue
		}
		name := getStringPropEval(mod, "name", nil)
		if name == "" {
			continue
		}
		ns := &namespaceInfo{}
		for _, prop := range mod.Map.Properties {
			if prop.Name == "imports" {
				if l, ok := prop.Value.(*parser.List); ok {
					for _, v := range l.Values {
						if s, ok := v.(*parser.String); ok {
							ns.imports = append(ns.imports, s.Value)
						}
					}
				}
			}
		}
		result[name] = ns
	}
	return result
}

// resolveModuleRef resolves a module reference string (e.g., ":libfoo" or
// "//namespace:libfoo") to a plain module name. For global references
// (//namespace:name), it verifies the namespace exists. For local references
// (:name), it falls back to simple name lookup.
func resolveModuleRef(ref string, fromMod *parser.Module, modules map[string]*parser.Module, namespaces map[string]*namespaceInfo) string {
	ref = strings.TrimPrefix(ref, ":")
	// Check for global reference: //namespace:module
	if strings.HasPrefix(ref, "//") {
		sepIdx := strings.Index(ref, ":")
		if sepIdx >= 0 {
			nsName := ref[2:sepIdx]
			modName := ref[sepIdx+1:]
			if _, ok := namespaces[nsName]; ok {
				return modName
			}
		}
	}
	return ref
}
