// Package build provides the core orchestration logic for the minibp build system.
// It coordinates the parsing, dependency resolution, and Ninja generation components
// to form a complete build pipeline. Its primary responsibilities include:
//   - Collecting and filtering enabled modules from parsed Blueprint definitions
//   - Constructing and topologically sorting module dependency graphs
//   - Configuring Ninja generators with build options and toolchain settings
//   - Resolving module references across namespaces and dependency edges
//
// This package sits between the CLI layer (cmd/minibp) and lower-level libraries
// (parser, ninja, dag, namespace, props, glob, variant), acting as the glue that
// connects these components to produce valid, deterministic Ninja build files.
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

// Options holds all configuration parameters required to drive the minibp build pipeline.
// These values are typically parsed from command-line flags by the CLI layer and passed
// to build functions to control module selection, output paths, toolchain settings,
// and target architecture.
type Options struct {
	// Arch specifies the target CPU architecture (e.g., "arm64", "x86_64").
	// Used to filter architecture-specific variants and configure toolchain flags.
	Arch string
	// SrcDir is the root source directory containing Blueprint files.
	// Used as the base path for resolving relative file references and glob patterns.
	SrcDir string
	// OutFile is the path to the output Ninja build file (e.g., "build.ninja").
	OutFile string
	// Inputs lists the Blueprint files or directories to parse.
	// If a directory is provided (with -a flag), all .bp files in it will be scanned.
	Inputs []string
	// Multilib specifies multilib build modes (e.g., ["lib64", "lib32"]).
	// Controls generation of 32-bit and 64-bit library variants when supported.
	Multilib []string
	// CC is the path to the C compiler executable (e.g., "aarch64-linux-gnu-gcc").
	// Overrides the default system C compiler for the target architecture.
	CC string
	// CXX is the path to the C++ compiler executable (e.g., "aarch64-linux-gnu-g++").
	// Overrides the default system C++ compiler for the target architecture.
	CXX string
	// AR is the path to the archive tool executable (e.g., "aarch64-linux-gnu-ar").
	// Overrides the default system archive tool for creating static libraries.
	AR string
	// LD is the path to the linker executable (e.g., "ld", "gold", "lld").
	// If empty, the compiler (CC/CXX) is used for linking.
	LD string
	// LTO specifies link-time optimization settings (e.g., "thin", "full").
	// Controls whether LTO is enabled and which variant to use during linking.
	LTO string
	// Sysroot is the path to the target system root for cross-compilation.
	// Provides headers and libraries for the target OS/architecture during compilation.
	Sysroot string
	// Ccache is the path to the ccache executable, or "no" to disable caching.
	// If set to a non-empty value other than "no", ccache will wrap compilation commands.
	Ccache string
	// TargetOS specifies the target operating system (e.g., "android", "linux").
	// Used to filter OS-specific modules and configure target-appropriate flags.
	TargetOS string
}

// BuildOptions is a type alias for Options to avoid circular import dependencies.
// The utils package (minibp/lib/utils) requires access to build configuration types,
// but importing build from utils would create an import cycle. This alias breaks
// the cycle by allowing utils to reference BuildOptions without importing build directly.
type BuildOptions = Options

// Graph represents a directed acyclic graph (DAG) of build modules and their dependencies.
// Nodes correspond to build modules (identified by unique names), and edges represent
// direct dependencies where one module requires another to be built first. This graph
// is used to determine a valid, parallelizable build order via topological sorting.
type Graph struct {
	// nodes maps canonical module names to their parsed Module definitions.
	// All enabled modules collected from Blueprint files are stored here.
	nodes map[string]*parser.Module
	// edges maps module names to the list of module names they depend on.
	// For example, edges["foo"] = ["bar"] means module "foo" depends on "bar".
	edges map[string][]string
}

// NewGraph creates and returns a new, empty dependency graph with initialized
// node and edge maps. The returned graph is ready to have nodes and edges added.
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]*parser.Module),
		edges: make(map[string][]string),
	}
}

// AddNode adds a module to the dependency graph as a new node.
// If the node already exists, its module reference is overwritten.
// It also ensures the node has an entry in the edges map to track dependencies,
// initializing an empty dependency list if one does not already exist.
//
// Parameters:
//   - name: Unique identifier of the module to add to the graph.
//   - mod: Parsed module definition containing properties, metadata, and dependencies.
func (g *Graph) AddNode(name string, mod *parser.Module) {
	g.nodes[name] = mod
	if _, ok := g.edges[name]; !ok {
		g.edges[name] = []string{}
	}
}

// AddEdge adds a directed dependency edge from module "from" to module "to".
// This indicates that module "from" depends on module "to" (to must be built first).
// It ensures both source and destination nodes have entries in the edges map,
// even if they were not explicitly added via AddNode (though missing nodes will
// cause validation errors during topological sort).
//
// Parameters:
//   - from: Name of the dependent module (source of the edge, requires the dependency).
//   - to: Name of the dependency module (target of the edge, required by "from").
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
// It returns a slice of build levels, where each level contains module names that can
// be built in parallel (all dependencies of modules in level N are in levels < N).
// If a circular dependency is detected, or a referenced module does not exist in the
// graph (excluding valid external references), an error is returned.
//
// Returns:
//   - [][]string: Slice of build levels, each a lexicographically sorted list of module names.
//     Modules in earlier levels have no dependencies on modules in later levels.
//   - error: Non-nil if circular dependencies are detected, or a referenced module
//     is missing from the graph. External dependencies (prefixed with ":" or "//") that
//     are not in the graph are ignored during dependency counting but still recorded as edges.
//
// Edge cases:
//   - Modules with no dependencies are placed in the first level.
//   - Each level's modules are sorted lexicographically to ensure deterministic output.
//   - If a module references a non-existent external dependency (starts with ":" or "//"),
//     an error is returned immediately.
func (g *Graph) TopoSort() ([][]string, error) {
	// depCount tracks the number of unresolved dependencies for each node.
	// A node with depCount 0 has all dependencies satisfied and is ready to build.
	depCount := make(map[string]int)
	for name := range g.nodes {
		depCount[name] = 0
	}

	// dependentOf maps a node to the list of nodes that depend on it.
	// When a node's depCount reaches 0, all nodes in dependentOf[node] have their
	// dependency count decremented, as one of their dependencies is now satisfied.
	dependentOf := make(map[string][]string)

	for from, deps := range g.edges {
		// Validate that the source module exists in the graph.
		if _, ok := g.nodes[from]; !ok {
			return nil, fmt.Errorf("module '%s' referenced in dependency graph does not exist", from)
		}
		// Set the dependency count for the source module to the number of edges (deps).
		depCount[from] = len(deps)
		for _, to := range deps {
			// Check if the target dependency exists in the graph.
			if _, ok := g.nodes[to]; !ok {
				// Skip non-module references (e.g., file paths) that don't start with ":" or "//".
				if !strings.HasPrefix(to, ":") && !strings.HasPrefix(to, "//") {
					continue
				}
				// External module references (":module" or "//namespace:module") that are missing are errors.
				return nil, fmt.Errorf("dependency '%s' of '%s' not found", to, from)
			}
			// Record that "from" depends on "to" by adding "from" to to's dependent list.
			dependentOf[to] = append(dependentOf[to], from)
		}
	}

	// Initialize the queue with all nodes that have zero unresolved dependencies.
	// These are modules with no dependencies, ready to be built in the first level.
	var queue []string
	for name, count := range depCount {
		if count == 0 {
			queue = append(queue, name)
		}
	}
	// Sort the initial queue lexicographically to ensure deterministic build order.
	sort.Strings(queue)

	var levels [][]string
	visitedCount := 0
	// Process nodes level by level. Each iteration handles one build level.
	for len(queue) > 0 {
		// Current level contains all nodes with satisfied dependencies at this stage.
		currentLevel := make([]string, len(queue))
		copy(currentLevel, queue)
		levels = append(levels, currentLevel)
		visitedCount += len(queue)

		nextQueue := []string{}
		// For each node in the current level, propagate dependency resolution to dependents.
		for _, u := range queue {
			for _, v := range dependentOf[u] {
				// Decrement the dependency count for each dependent of u.
				depCount[v]--
				if depCount[v] == 0 {
					// All dependencies of v are satisfied; add to next level queue.
					nextQueue = append(nextQueue, v)
				}
			}
		}
		// Sort the next queue lexicographically for deterministic order.
		sort.Strings(nextQueue)
		queue = nextQueue
	}

	// If not all nodes were visited, there is a circular dependency.
	if visitedCount != len(g.nodes) {
		return nil, fmt.Errorf("circular dependency detected")
	}

	return levels, nil
}

// CollectModules collects all enabled modules from parsed Blueprint definitions.
// It filters out disabled modules, merges architecture variants, expands glob
// patterns in module properties, and returns a map of module name to module definition.
// This is a convenience wrapper around CollectModulesWithNames that uses the default
// name extraction logic (reading the "name" property from each module).
//
// Parameters:
//   - allDefs: Slice of all parsed Blueprint definitions (modules, assignments, namespaces).
//   - eval: Evaluator for resolving variables and select() expressions in module properties.
//   - opts: Build configuration options controlling filtering, path resolution, and variants.
//
// Returns:
//   - map[string]*parser.Module: Map of enabled module names to their parsed definitions.
//   - error: Non-nil if glob expansion fails for any module or during global expansion.
func CollectModules(allDefs []parser.Definition, eval *parser.Evaluator, opts Options) (map[string]*parser.Module, error) {
	modules, err := CollectModulesWithNames(allDefs, eval, opts, nil)
	if err != nil {
		return nil, err
	}
	if err := glob.ExpandGlobs(modules, opts.SrcDir); err != nil {
		return nil, fmt.Errorf("error during global glob expansion: %w", err)
	}
	for name, mod := range modules {
		if err := glob.ExpandInModule(mod, opts.SrcDir); err != nil {
			return nil, fmt.Errorf("error expanding globs for module %s: %w", name, err)
		}
	}
	return modules, nil
}

// CollectModulesWithNames collects enabled modules from Blueprint definitions using a
// custom function for extracting module names. This allows callers to override the
// default name resolution logic (e.g., for testing or custom naming schemes).
// It handles variant merging, module enablement checks, glob expansion, and
// returns only modules that are enabled for the current build configuration.
//
// Parameters:
//   - allDefs: Slice of all parsed Blueprint definitions.
//   - eval: Evaluator for property evaluation and variant resolution.
//   - opts: Build configuration options controlling filtering and path resolution.
//   - nameFunc: Optional custom function to extract a module's name from its definition.
//     The function takes a module pointer and a property key, returns the name string.
//     If nil, the default logic (props.GetStringPropEval with key "name") is used.
//
// Returns:
//   - map[string]*parser.Module: Map of enabled module names to their definitions.
//   - error: Non-nil if glob expansion fails for any module or globally.
//
// Edge cases:
//   - Modules with empty names are skipped.
//   - Modules disabled for the current target (via host_supported/device_supported)
//     are excluded from the result.
//   - Glob expansion is performed globally first, then per-module to resolve all file patterns.
func CollectModulesWithNames(
	allDefs []parser.Definition,
	eval *parser.Evaluator,
	opts Options,
	nameFunc func(*parser.Module, string) string,
) (map[string]*parser.Module, error) {
	// Use default name extraction logic if no custom function is provided:
	// extract the "name" property from the module using the evaluator.
	if nameFunc == nil {
		nameFunc = func(m *parser.Module, key string) string {
			return props.GetStringPropEval(m, key, eval)
		}
	}

	modules := make(map[string]*parser.Module)
	// Iterate over all parsed definitions, filtering for module definitions only.
	// Non-module definitions (variable assignments, namespace blocks) are ignored.
	for _, def := range allDefs {
		mod, ok := def.(*parser.Module)
		if !ok {
			continue
		}
		// Extract module name using the provided or default name function.
		name := nameFunc(mod, "name")
		if name == "" {
			continue
		}
		// Evaluate all properties in the module, resolving variables and select() expressions.
		eval.EvalModule(mod)
		// Merge architecture-specific variant properties into the module based on target arch.
		variant.MergeVariantProps(mod, opts.Arch, true, eval)
		// Skip modules that are not enabled for the current build target.
		if !variant.IsModuleEnabledForTarget(mod, true) {
			continue
		}
		modules[name] = mod
	}

	// Expand global glob patterns (e.g., wildcard file references) across all modules first.
	if err := glob.ExpandGlobs(modules, opts.SrcDir); err != nil {
		return nil, fmt.Errorf("error during global glob expansion: %w", err)
	}

	// Expand module-specific glob patterns (e.g., in srcs, header_libs properties).
	for _, mod := range modules {
		if err := glob.ExpandInModule(mod, opts.SrcDir); err != nil {
			name := nameFunc(mod, "name")
			return nil, fmt.Errorf("error expanding globs for module %s: %w", name, err)
		}
	}

	return modules, nil
}

// BuildGraph constructs a module dependency graph from a collection of enabled modules.
// It adds all modules as graph nodes, then processes common dependency properties
// (deps, shared_libs, header_libs, data) to add directed edges between dependent modules.
// Namespace references are resolved via the provided namespace map before adding edges.
//
// Parameters:
//   - modules: Map of module names to their parsed definitions (output of CollectModules).
//   - namespaces: Map of namespace names to metadata for resolving cross-namespace references.
//   - eval: Evaluator for resolving property values in dependency lists.
//
// Returns:
//   - *Graph: Fully constructed dependency graph ready for topological sorting.
func BuildGraph(modules map[string]*parser.Module, namespaces map[string]*namespace.Info, eval *parser.Evaluator) *Graph {
	graph := NewGraph()

	// Add all enabled modules as nodes in the dependency graph.
	for name, mod := range modules {
		graph.AddNode(name, mod)
	}

	// Process common dependency properties for each module to add graph edges.
	// Dependencies are resolved via namespace lookup before adding edges to the graph.
	// Only module references (prefixed with ":" or "//") are processed for deps properties.
	for name, mod := range modules {
		addResolvedDeps(graph, name, props.GetListPropEval(mod, "deps", eval), namespaces, true)
		addResolvedDeps(graph, name, props.GetListPropEval(mod, "shared_libs", eval), namespaces, true)
		addResolvedDeps(graph, name, props.GetListPropEval(mod, "header_libs", eval), namespaces, true)
		addResolvedDeps(graph, name, props.GetListPropEval(mod, "data", eval), namespaces, true)
	}
	return graph
}

// addResolvedDeps resolves a list of dependency references and adds corresponding edges
// to the dependency graph. It handles both local module references and cross-namespace
// references (prefixed with ":" or "//") by resolving them via the namespace map.
//
// Parameters:
//   - graph: The dependency graph to add edges to.
//   - from: Name of the dependent module (source of the edge, requires the dependencies).
//   - deps: List of dependency references (module names or namespace-qualified references).
//   - namespaces: Map of namespaces for resolving cross-namespace references.
//   - moduleRefsOnly: If true, only process references starting with ":" or "//";
//     other values (e.g., file paths) are skipped to avoid adding invalid edges.
func addResolvedDeps(graph *Graph, from string, deps []string, namespaces map[string]*namespace.Info, moduleRefsOnly bool) {
	for _, dep := range deps {
		// Skip non-module references if moduleRefsOnly is enabled.
		// This filters out file paths or other non-module dependency values.
		if moduleRefsOnly && !strings.HasPrefix(dep, ":") && !strings.HasPrefix(dep, "//") {
			continue
		}
		// Resolve the dependency reference to a canonical module name using namespaces,
		// then add the directed edge to the graph.
		depName := namespace.ResolveModuleRef(dep, namespaces)
		graph.AddEdge(from, depName)
	}
}

// NewGenerator creates and configures a Ninja generator for converting module definitions
// into Ninja build rules and statements. It initializes the generator with build rules
// for all supported module types, sets up path prefixes, toolchain configuration,
// and regeneration commands.
//
// Parameters:
//   - graph: Topologically sorted dependency graph (passed to the generator for build order).
//   - modules: Map of module names to their definitions, passed to the generator.
//   - opts: Build configuration options controlling output paths, toolchain, and target.
//
// Returns:
//   - *ninja.Generator: Configured generator ready to produce Ninja build output.
func NewGenerator(graph *Graph, modules map[string]*parser.Module, opts Options) *ninja.Generator {
	// Build a map of rule names to rule implementations for quick lookup.
	// This allows the generator to find the correct build rule for each module type.
	ruleMap := make(map[string]ninja.BuildRule)
	for _, r := range ninja.GetAllRules() {
		ruleMap[r.Name()] = r
	}

	// Calculate absolute paths for output file and source directory to determine path prefix.
	absOutFile, _ := filepath.Abs(opts.OutFile)
	outDir := filepath.Dir(absOutFile)
	prefix := pathPrefixForOutput(opts.SrcDir, absOutFile)

	gen := ninja.NewGenerator(graph, ruleMap, modules)
	gen.SetSourceDir(opts.SrcDir)
	gen.SetOutputDir(outDir)
	// Set the path prefix to reference source files from the build output directory.
	gen.SetPathPrefix(prefix)
	// Configure the regeneration command: Ninja will re-run this to update build.ninja when needed.
	gen.SetRegen(buildRegenCmd(opts), opts.Inputs, opts.OutFile)
	gen.SetWorkDir(opts.SrcDir)
	// Apply toolchain settings from build options, falling back to defaults.
	gen.SetToolchain(toolchainFromOptions(opts))
	gen.SetArch(opts.Arch)
	gen.SetTargetOS(opts.TargetOS)
	if len(opts.Multilib) > 0 {
		gen.SetMultilib(opts.Multilib)
	}
	return gen
}

// pathPrefixForOutput calculates the relative path prefix from the build output directory
// to the source directory. This prefix is used in Ninja build rules to reference source
// files relative to the build directory. Returns an empty string if the build and source
// directories are the same, or if any path calculation fails.
//
// Parameters:
//   - srcDir: Root source directory containing Blueprint files.
//   - outFile: Path to the output Ninja file (used to determine the build output directory).
//
// Returns:
//   - string: Relative path from build directory to source directory, with trailing slash
//     and forward slashes (Ninja-compatible), or empty string if no prefix is needed.
//
// Edge cases:
//   - If the build directory (parent of outFile) equals the source directory, returns empty.
//   - Any error in path calculation (absolute path failure, relative path failure) returns empty.
func pathPrefixForOutput(srcDir, outFile string) string {
	// Get absolute path of the build output directory (parent directory of the Ninja file).
	absBuildDir, err := filepath.Abs(filepath.Dir(outFile))
	if err != nil {
		return ""
	}
	// Get absolute path of the source directory.
	absSourceDir, err := filepath.Abs(srcDir)
	if err != nil {
		return ""
	}

	// If build and source directories are the same, no prefix is needed.
	if absBuildDir == absSourceDir {
		return ""
	}

	// Calculate relative path from build directory to source directory.
	relPath, err := filepath.Rel(absBuildDir, absSourceDir)
	if err != nil {
		return ""
	}

	// If relative path is current directory, no prefix needed.
	if relPath == "." {
		return ""
	}

	// Convert to forward slashes for Ninja compatibility and add trailing slash.
	return filepath.ToSlash(relPath) + "/"
}

// buildRegenCmd constructs the command string that Ninja will execute to regenerate
// the build.ninja file when it detects changes to input Blueprint files. The command
// includes the minibp executable name, architecture flag, output path, and input files.
//
// Parameters:
//   - opts: Build configuration options containing executable flags and paths.
//
// Returns:
//   - string: Complete command line for regenerating the Ninja build file, with forward slashes.
//
// Edge cases:
//   - If a single input is a directory, the -a (scan all) flag is added automatically.
//   - Input files are joined with spaces; the executable path uses forward slashes for Ninja compatibility.
func buildRegenCmd(opts Options) string {
	// Get the executable name from the command line arguments (os.Executable()).
	exe, _ := os.Executable()

	// Use forward slashes for the executable path (Ninja expects POSIX-style paths).
	regenCmd := filepath.ToSlash(exe)
	if opts.Arch != "" {
		regenCmd += " -arch " + opts.Arch
	}
	if opts.TargetOS != "" {
		regenCmd += " -os " + opts.TargetOS
	}
	// Check if the single input is a directory to add -a (scan all) flag.
	if len(opts.Inputs) == 1 {
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

// toolchainFromOptions creates a Toolchain configuration struct from the provided
// build options. It starts with default toolchain settings and overrides them
// with any user-specified values from the build options.
//
// Parameters:
//   - opts: Build configuration options containing toolchain executable paths and settings.
//
// Returns:
//   - ninja.Toolchain: Configured toolchain with resolved executable paths.
//
// Edge cases:
//   - If Ccache is set to "no", the ccache field is explicitly cleared to disable caching.
//   - If Ccache is set to a non-empty value other than "no", it is used as the ccache path.
//   - Default toolchain values are used for any tools not specified in the build options.
func toolchainFromOptions(opts Options) ninja.Toolchain {
	// Start with default toolchain settings for the target platform.
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
	if opts.LD != "" {
		tc.LD = opts.LD
	}
	if opts.Sysroot != "" {
		tc.Sysroot = opts.Sysroot
	}
	if opts.LTO != "" {
		tc.Lto = opts.LTO
	}
	// Handle Ccache: "no" disables it, any other non-empty value enables it.
	if opts.Ccache == "no" {
		tc.Ccache = ""
	} else if opts.Ccache != "" {
		tc.Ccache = opts.Ccache
	}
	return tc
}
