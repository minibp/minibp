// Package build provides the core build system functionality for minibp.
// It handles module collection, dependency graph construction, variant merging,
// glob expansion, and creates a configured ninja generator for build file generation.
// The package operates in the middle layer between the parser and the ninja generator,
// transforming Blueprint module definitions into a dependency graph suitable for
// generating Ninja build rules.
package build

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"minibp/lib/glob"
	"minibp/lib/namespace"
	"minibp/lib/ninja"
	"minibp/lib/parser"
	"minibp/lib/props"
	"minibp/lib/variant"
)

// Options holds the command-line configuration options for the build system.
// It is used to configure target architecture, source directories, compilers,
// output paths, and various build system settings. These options are typically
// parsed from command-line flags in main.go and passed to the collection and
// generation functions.
type Options struct {
	Arch     string   // Target architecture (e.g., "arm64", "x86_64", "arm")
	SrcDir   string   // Source directory containing .bp files; used for glob expansion base
	OutFile  string   // Output file path for generated ninja build file (usually "build.ninja")
	Inputs   []string // Input .bp files or directories to scan; processed recursively
	Multilib []string // Multilib configurations (e.g., ["arm64-v8a", "armeabi-v7a"]); enables multi-arch builds
	CC       string   // C compiler path or command (e.g., "/usr/bin/clang", "clang-14")
	CXX      string   // C++ compiler path or command (e.g., "/usr/bin/clang++", "clang++-14")
	AR       string   // Archiver path or command (e.g., "ar", "libtool")
	LTO      string   // Link-time optimization setting ("thin", "full", or empty for none)
	Sysroot  string   // Sysroot path for toolchain (e.g., "/opt/ios-sdk")
	Ccache   string   // Ccache setting ("no" to disable, or path to ccache binary)
	TargetOS string   // Target operating system (e.g., "linux", "darwin", "windows")
}

// Graph represents a dependency graph of modules used for ninja build generation.
// Each node represents a module that can be built, and each directed edge represents
// a dependency relationship where the source module depends on the target module.
// The graph supports topological sorting to determine the correct build order, ensuring
// that all dependencies are built before the modules that depend on them.
//
// The graph implementation uses Kahn's algorithm for topological sorting, which
// processes nodes in levels where each level contains modules with no remaining
// unresolved dependencies. This allows Ninja to parallelize builds within each level.
type Graph struct {
	// nodes maps module names to their parsed module definitions.
	// The module definition contains all properties, sources, and build configuration.
	nodes map[string]*parser.Module

	// edges maps each module name to a slice of module names it depends on.
	// For example, if "libfoo" depends on "libbar", edges["libfoo"] = ["libbar"].
	// Self-referencing edges (module depending on itself) are allowed but unusual.
	edges map[string][]string
}

// NewGraph creates a new, empty dependency graph ready for population.
// The graph starts with no nodes or edges; modules are added via AddNode()
// and dependencies are added via AddEdge(). The returned graph is ready for use
// with the BuildGraph() function or for manual population.
//
// Returns:
//   - *Graph: A new graph instance with initialized empty maps.
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]*parser.Module),
		edges: make(map[string][]string),
	}
}

// AddNode adds a module node to the dependency graph.
// If the node already exists, it is updated with the new module definition;
// otherwise, a new node is created. The module name must be unique within
// the graph, but if duplicate names are added, the last one wins.
//
// Parameters:
//   - name: Unique module name identifier (e.g., "libutils", "my_app")
//   - mod: Parsed module definition containing properties and build configuration
//
// Notes:
//   - Calling AddNode with an existing name updates the module definition
//   - An empty edges slice is automatically created for new nodes
func (g *Graph) AddNode(name string, mod *parser.Module) {
	g.nodes[name] = mod
	if _, ok := g.edges[name]; !ok {
		g.edges[name] = []string{}
	}
}

// AddEdge adds a directed edge from one module to another, representing a dependency.
// The edge direction indicates that the source module depends on the target module, meaning
// the target must be built before the source. Both modules must exist in the graph
// for the edge to be added; if either doesn't exist, an empty edges slice is created.
//
// Parameters:
//   - from: Module name that has the dependency (the dependent)
//   - to: Module name that is being depended on (the dependency)
//
// Edge cases:
//   - If either module doesn't exist, an empty edges slice is created for it
//   - Multiple edges from the same source to different targets are allowed
//   - Duplicate edges (source depending on same target twice) result in duplicate entries
func (g *Graph) AddEdge(from, to string) {
	if _, ok := g.edges[from]; !ok {
		g.edges[from] = []string{}
	}
	if _, ok := g.edges[to]; !ok {
		g.edges[to] = []string{}
	}
	g.edges[from] = append(g.edges[from], to)
}

// TopoSort performs a topological sort on the dependency graph using Kahn's algorithm.
// It returns a slice of levels, where each level contains module names that can be
// built in parallel. Modules in the same level have no dependencies on each other,
// allowing Ninja to build them concurrently.
//
// The algorithm works by:
//  1. Computing in-degree for each node (number of incoming edges)
//  2. Finding all nodes with in-degree 0 (no unresolved dependencies)
//  3. Adding those nodes to the current level and removing their outgoing edges
//  4. Repeating until all nodes are processed
//
// Returns:
//   - [][]string: A slice of levels, each containing module names buildable in parallel
//   - error: Circular dependency detected, or if a referenced module doesn't exist
//
// Edge cases:
//   - Empty graph returns empty levels slice (no error)
//   - Single node with no dependencies returns one level with that node
//   - Circular dependencies return error with "circular dependency detected"
//   - Missing modules return descriptive error naming the missing module
func (g *Graph) TopoSort() ([][]string, error) {
	// Step 1: Initialize in-degree map and reverse edges map.
	inDegree := make(map[string]int)
	reverseEdges := make(map[string][]string)
	for name := range g.nodes {
		inDegree[name] = 0
	}

	for from, deps := range g.edges {
		if _, ok := g.nodes[from]; !ok {
			return nil, fmt.Errorf("module '%s' referenced in dependency graph does not exist", from)
		}
		for _, to := range deps {
			if _, ok := g.nodes[to]; !ok {
				return nil, fmt.Errorf("dependency '%s' of '%s' not found", to, from)
			}
			inDegree[to]++
			reverseEdges[from] = append(reverseEdges[from], to)
		}
	}

	// Step 2: Initialize the queue with all nodes having an in-degree of 0.
	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	var levels [][]string
	visitedCount := 0
	for len(queue) > 0 {
		// Current level consists of all nodes in the queue.
		currentLevel := make([]string, len(queue))
		copy(currentLevel, queue)
		levels = append(levels, currentLevel)
		visitedCount += len(queue)

		// Prepare the next level's queue.
		nextQueue := []string{}
		for _, u := range queue {
			// For each neighbor of u, decrement its in-degree.
			for _, v := range reverseEdges[u] {
				inDegree[v]--
				if inDegree[v] == 0 {
					nextQueue = append(nextQueue, v)
				}
			}
		}
		sort.Strings(nextQueue)
		queue = nextQueue
	}

	if visitedCount != len(g.nodes) {
		// If not all nodes were visited, there must be a cycle.
		return nil, fmt.Errorf("circular dependency detected")
	}

	return levels, nil
}

// CollectModules collects all enabled modules from a list of Blueprint definitions.
// It processes each definition, evaluates module properties, merges variant-specific
// properties, expands globs, and filters based on target (host/device) support.
// Only modules that are enabled for the current target configuration are included.
//
// The collection process:
//  1. Filter to only module-type definitions (skip soong_namespace, etc.)
//  2. Extract module name from properties
//  3. Evaluate module expressions (select, variables)
//  4. Merge architecture-specific properties
//  5. Expand glob patterns to file lists
//  6. Filter by host/device support
//
// Parameters:
//   - allDefs: All Blueprint definitions from parsed files
//   - eval: Evaluator for processing module expressions
//   - opts: Build options for variant and target filtering
//
// Returns:
//   - map[string]*parser.Module: Map of module name to definition
//   - error: Glob expansion failure, or other evaluation errors
func CollectModules(allDefs []parser.Definition, eval *parser.Evaluator, opts Options) (map[string]*parser.Module, error) {
	modules, err := CollectModulesWithNames(allDefs, eval, opts, nil)
	if err != nil {
		return nil, err
	}
	// After collecting all modules, perform a single global glob expansion.
	if err := glob.ExpandGlobs(modules, opts.SrcDir); err != nil {
		return nil, fmt.Errorf("error during global glob expansion: %w", err)
	}
	// Now that globs are cached, expand them in each module.
	for name, mod := range modules {
		if err := glob.ExpandInModule(mod, opts.SrcDir); err != nil {
			return nil, fmt.Errorf("error expanding globs for module %s: %w", name, err)
		}
	}
	return modules, nil
}

// CollectModulesWithNames collects modules using a custom name extraction function.
// This allows customized module naming strategies, such as Android's cc_library
// which derives the module name from the "shared_libs" property.
//
// The nameFunc parameter is called with each module and the property key "name".
// If nameFunc is nil, uses the default property extraction.
//
// Parameters:
//   - allDefs: All Blueprint definitions from parsed files
//   - eval: Evaluator for processing module expressions
//   - opts: Build options for variant and target filtering
//   - nameFunc: Custom function to extract module name; nil uses default
//
// Returns:
//   - map[string]*parser.Module: Map of module name to definition
//   - error: Glob expansion failure, or other evaluation errors
func CollectModulesWithNames(
	allDefs []parser.Definition,
	eval *parser.Evaluator,
	opts Options,
	nameFunc func(*parser.Module, string) string,
) (map[string]*parser.Module, error) {
	// Use default name extraction if custom function not provided.
	if nameFunc == nil {
		nameFunc = func(m *parser.Module, key string) string {
			return props.GetStringPropEval(m, key, eval)
		}
	}

	modules := make(map[string]*parser.Module)
	for _, def := range allDefs {
		mod, ok := def.(*parser.Module)
		if !ok {
			continue
		}
		name := nameFunc(mod, "name")
		if name == "" {
			continue
		}
		eval.EvalModule(mod)
		variant.MergeVariantProps(mod, opts.Arch, true, eval)
		// We will expand globs later, in a single pass.
		if !variant.IsModuleEnabledForTarget(mod, true) {
			continue
		}
		modules[name] = mod
	}

	// Perform a single global glob expansion pass.
	if err := glob.ExpandGlobs(modules, opts.SrcDir); err != nil {
		return nil, fmt.Errorf("error during global glob expansion: %w", err)
	}

	// Now apply the cached globs to each module.
	for _, mod := range modules {
		if err := glob.ExpandInModule(mod, opts.SrcDir); err != nil {
			name := nameFunc(mod, "name")
			return nil, fmt.Errorf("error expanding globs for module %s: %w", name, err)
		}
	}

	return modules, nil
}

// BuildGraph constructs a dependency graph from a collection of modules.
// It processes all module dependency properties (deps, shared_libs, header_libs, data)
// and resolves module references to create directed edges.
//
// The dependency resolution process:
//  1. Add all modules as nodes
//  2. For each module, resolve and add edges from:
//     - "deps" property: compile-time dependencies
//     - "shared_libs" property: shared library dependencies
//     - "header_libs" property: header library dependencies
//     - "data" property: runtime data dependencies
//
// Module references use ":" for same-namespace and "//" for cross-namespace
// references (e.g., ":libutils" or "//core:libcore").
//
// Parameters:
//   - modules: Map of module name to definition
//   - namespaces: Map of namespace information for cross-namespace resolution
//   - eval: Evaluator for extracting property values
//
// Returns:
//   - *Graph: Complete dependency graph with all nodes and edges
func BuildGraph(modules map[string]*parser.Module, namespaces map[string]*namespace.Info, eval *parser.Evaluator) *Graph {
	graph := NewGraph()

	// Step 1: Add all modules as nodes.
	for name, mod := range modules {
		graph.AddNode(name, mod)
	}

	// Step 2: Process all dependency properties and create edges.
	for name, mod := range modules {
		// Resolve deps: compile-time dependencies
		addResolvedDeps(graph, name, props.GetListPropEval(mod, "deps", eval), namespaces, false)
		// Resolve shared_libs: shared library linkage dependencies
		addResolvedDeps(graph, name, props.GetListPropEval(mod, "shared_libs", eval), namespaces, false)
		// Resolve header_libs: header library dependencies
		addResolvedDeps(graph, name, props.GetListPropEval(mod, "header_libs", eval), namespaces, false)
		// Resolve data: runtime data file dependencies
		addResolvedDeps(graph, name, props.GetListPropEval(mod, "data", eval), namespaces, true)
	}
	return graph
}

// addResolvedDeps resolves dependency references and adds edges to the graph.
// It processes each dependency reference, optionally filters to only module
// references (those starting with ":" or "//"), and resolves the reference
// to a module name using the namespace information.
//
// Parameters:
//   - graph: Dependency graph to add edges to
//   - from: Module name that has the dependencies
//   - deps: Slice of dependency reference strings
//   - namespaces: Map of namespace information for resolution
//   - moduleRefsOnly: If true, only process dependencies starting with ":" or "//"
func addResolvedDeps(graph *Graph, from string, deps []string, namespaces map[string]*namespace.Info, moduleRefsOnly bool) {
	for _, dep := range deps {
		// Filter to only module references when required.
		// The data property may contain file paths, not module references.
		if moduleRefsOnly && !strings.HasPrefix(dep, ":") && !strings.HasPrefix(dep, "//") {
			continue
		}
		// Resolve module reference to actual module name.
		// Handles: ":name" -> "name", "//ns:name" -> resolved name
		depName := namespace.ResolveModuleRef(dep, namespaces)
		graph.AddEdge(from, depName)
	}
}

// NewGenerator creates a ninja generator configured with the build options.
// It initializes the generator with:
//   - All build rules for supported module types
//   - Output directory and path prefix configuration
//   - Regeneration command for incremental builds
//   - Toolchain settings (compiler paths, flags, LTO)
//   - Architecture and multilib configuration
//
// Parameters:
//   - graph: Completed dependency graph
//   - modules: Map of all module definitions
//   - opts: Build options for configuration
//
// Returns:
//   - *ninja.Generator: Configured generator ready to produce build.ninja
func NewGenerator(graph *Graph, modules map[string]*parser.Module, opts Options) *ninja.Generator {
	// Build map of all available rules by name.
	ruleMap := make(map[string]ninja.BuildRule)
	for _, r := range ninja.GetAllRules() {
		ruleMap[r.Name()] = r
	}

	// Calculate output paths and path prefix.
	absOutFile, _ := filepath.Abs(opts.OutFile)
	outDir := filepath.Dir(absOutFile)
	prefix := pathPrefixForOutput(opts.SrcDir, absOutFile)

	// Create and configure the generator.
	gen := ninja.NewGenerator(graph, ruleMap, modules)
	gen.SetSourceDir(opts.SrcDir)
	gen.SetOutputDir(outDir)
	gen.SetPathPrefix(prefix)
	gen.SetRegen(buildRegenCmd(opts), opts.Inputs, opts.OutFile)
	gen.SetWorkDir(opts.SrcDir)
	gen.SetToolchain(toolchainFromOptions(opts))
	gen.SetArch(opts.Arch)
	gen.SetTargetOS(opts.TargetOS)
	// Configure multilib for multi-architecture builds (e.g., arm64-v8a + armeabi-v7a).
	if len(opts.Multilib) > 0 {
		gen.SetMultilib(opts.Multilib)
	}
	return gen
}

// pathPrefixForOutput calculates the relative path prefix from build directory
// to source directory. This prefix is used to convert absolute source file
// paths to relative paths in generated ninja rules.
//
// Returns empty string when:
//   - Build directory and source directory are the same
//   - Relative path calculation fails
//   - Resulting relative path is "."
//
// Parameters:
//   - srcDir: Source directory containing .bp files
//   - outFile: Output ninja file path
//
// Returns:
//   - string: Relative path prefix ending with "/", or empty string
func pathPrefixForOutput(srcDir, outFile string) string {
	absBuildDir := filepath.Dir(outFile)
	absSourceDir, _ := filepath.Abs(srcDir)
	// Same directory: no prefix needed.
	if absBuildDir == absSourceDir {
		return ""
	}
	relPath, err := filepath.Rel(absBuildDir, absSourceDir)
	// Error or current directory: no prefix needed.
	if err != nil || relPath == "." {
		return ""
	}
	// Convert to forward slashes for ninja compatibility.
	return filepath.ToSlash(relPath) + "/"
}

// buildRegenCmd constructs the regeneration command for ninja build rules.
// This command is run by ninja to regenerate build.ninja when input files change.
// It includes the minibp binary path and all input files/directories.
//
// The command format: "minibp -o build.ninja input1 input2 ..."
// Ninja runs this when input files are newer than build.ninja.
//
// Parameters:
//   - opts: Build options containing input files and output path
//
// Returns:
//   - string: Complete regeneration command
func buildRegenCmd(opts Options) string {
	regenCmd := os.Args[0]
	if opts.Arch != "" {
		regenCmd += " -arch " + opts.Arch
	}
	// Add -a flag if scanning a directory
	if len(opts.Inputs) == 1 {
		// Check if input is a directory
		fi, err := os.Stat(opts.Inputs[0])
		if err == nil && fi.IsDir() {
			regenCmd += " -a"
		}
	}
	regenCmd += " -o " + opts.OutFile
	if len(opts.Inputs) > 0 {
		regenCmd += " " + strings.Join(opts.Inputs, " ")
	}
	return regenCmd
}

// toolchainFromOptions creates a Toolchain configuration from build options.
// It starts with default toolchain values and overrides only the settings
// that are explicitly specified in options.
//
// Parameter priority:
//  1. Explicit options (CC, CXX, AR, etc.) override defaults
//  2. LTO setting applies only if non-empty
//  3. Ccache "no" explicitly disables; other non-empty enables
//
// Parameters:
//   - opts: Build options containing toolchain settings
//
// Returns:
//   - ninja.Toolchain: Configured toolchain with overrides applied
func toolchainFromOptions(opts Options) ninja.Toolchain {
	tc := ninja.DefaultToolchain()
	if opts.CC != "" {
		tc.CC = opts.CC
	}
	if opts.CXX != "" {
		tc.CXX = opts.CXX
	}
	if opts.AR != "" {
		tc.AR = opts.AR
	}
	if opts.Sysroot != "" {
		tc.Sysroot = opts.Sysroot
	}
	if opts.LTO != "" {
		tc.Lto = opts.LTO
	}
	// "no" explicitly disables ccache; empty string uses default.
	if opts.Ccache == "no" {
		tc.Ccache = ""
	} else if opts.Ccache != "" {
		tc.Ccache = opts.Ccache
	}
	return tc
}
