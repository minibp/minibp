// Package build provides the core orchestration logic for the minibp build system.
// It coordinates the parsing, dependency resolution, and Ninja generation components
// to form a complete build pipeline. Its primary responsibilities include:
//
//   - Collecting and filtering enabled modules from parsed Blueprint definitions
//
//     The collection phase iterates over all parsed Blueprint definitions,
//     evaluates module properties (resolving variables and select() expressions),
//     merges architecture-specific variant properties, and filters out modules
//     that are disabled for the current build target. Modules with empty names
//     or those marked as host-only/device-only for mismatched targets are skipped.
//
//   - Constructing and topologically sorting module dependency graphs
//
//     After modules are collected, their dependencies are resolved by processing
//     common dependency properties (deps, shared_libs, header_libs, data).
//     Cross-namespace references are resolved via namespace lookup before edges
//     are added. The resulting directed graph is topologically sorted using
//     Kahn's algorithm, producing parallelizable build levels where modules
//     in the same level have no interdependencies.
//
//   - Configuring Ninja generators with build options and toolchain settings
//
//     The generator is initialized with all supported build rules, path prefixes
//     calculated from relative output/source directories, a toolchain configured
//     from options (compiler paths, LTO settings, sysroot, ccache), and a
//     regeneration command so Ninja can re-run minibp when Blueprint files change.
//
//   - Resolving module references across namespaces and dependency edges
//
//     Module references in dependency properties support local references (":lib")
//     and cross-namespace references ("//namespace:lib"). The namespace.Resolver
//     is used to canonicalize these references before adding graph edges.
//
// This package sits between the CLI layer (cmd/minibp) and lower-level libraries
// (parser, ninja, dag, namespace, props, glob, variant), acting as the glue that
// connects these components to produce valid, deterministic Ninja build files.
// It deliberately avoids depending on the dag package to keep the dependency
// graph implementation modular.
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
//
// All fields are optional unless noted. Zero values trigger appropriate defaults
// in downstream functions (e.g., toolchain defaults in NewGenerator).
type Options struct {
	// Arch specifies the target CPU architecture (e.g., "arm64", "x86_64").
	// Used to filter architecture-specific variants and configure toolchain flags.
	// Empty string uses the host architecture or default toolchain.
	Arch string

	// SrcDir is the root source directory containing Blueprint files.
	// Used as the base path for resolving relative file references and glob patterns.
	// Must be an absolute path or relative to the current working directory.
	SrcDir string

	// OutFile is the path to the output Ninja build file (e.g., "build.ninja").
	// The parent directory of this path determines the build output directory,
	// from which the relative path prefix to the source directory is calculated.
	OutFile string

	// Inputs lists the Blueprint files or directories to parse.
	// If a directory is provided (with -a flag), all .bp files in it will be scanned.
	// Files are processed in order; duplicate paths are handled by the parser.
	Inputs []string

	// Multilib specifies multilib build modes (e.g., ["lib64", "lib32"]).
	// Controls generation of 32-bit and 64-bit library variants when supported.
	// Empty slice means no multilib support.
	Multilib []string

	// CC is the path to the C compiler executable (e.g., "aarch64-linux-gnu-gcc").
	// Overrides the default system C compiler for the target architecture.
	// Empty string uses the default toolchain C compiler.
	CC string

	// CXX is the path to the C++ compiler executable (e.g., "aarch64-linux-gnu-g++").
	// Overrides the default system C++ compiler for the target architecture.
	// Empty string uses the default toolchain C++ compiler.
	CXX string

	// AR is the path to the archive tool executable (e.g., "aarch64-linux-gnu-ar").
	// Overrides the default system archive tool for creating static libraries.
	// Empty string uses the default toolchain archiver.
	AR string

	// LD is the path to the linker executable (e.g., "ld", "gold", "lld").
	// If empty, the compiler (CC/CXX) is used for linking.
	// If set, this tool is used for the final link step instead of the compiler.
	LD string

	// LTO specifies link-time optimization settings (e.g., "thin", "full").
	// Controls whether LTO is enabled and which variant to use during linking.
	// Empty string means no LTO.
	LTO string

	// Sysroot is the path to the target system root for cross-compilation.
	// Provides headers and libraries for the target OS/architecture during compilation.
	// Empty string means the system sysroot is used.
	Sysroot string

	// Ccache is the path to the ccache executable, or "no" to disable caching.
	// If set to a non-empty value other than "no", ccache will wrap compilation commands.
	// If "no", ccache is explicitly disabled even if defaults would enable it.
	// If empty, the default toolchain ccache behavior is used.
	Ccache string

	// TargetOS specifies the target operating system (e.g., "android", "linux").
	// Used to filter OS-specific modules and configure target-appropriate flags.
	// Empty string uses the host OS or default settings.
	TargetOS string
}

// BuildOptions is a type alias for Options to avoid circular import dependencies.
// The utils package (minibp/lib/utils) requires access to build configuration types
// for reporting and logging, but importing build from utils would create an import
// cycle (build -> parser -> utils -> build). This alias allows utils to reference
// BuildOptions without importing the build package directly, breaking the cycle.
//
// Note: This is a compile-time alias, not a wrapper. Type identity is preserved,
// meaning utils can declare variables of type BuildOptions that are interchangeable with Options.
type BuildOptions = Options

// Graph represents a directed acyclic graph (DAG) of build modules and their dependencies.
// Nodes correspond to build modules (identified by unique names), and edges represent
// direct dependencies where one module requires another to be built first. This graph
// is used to determine a valid, parallelizable build order via topological sorting.
//
// Design notes:
//   - The graph is mutable: nodes and edges can be added after creation.
//     However, no operations require removal or modification of existing entries.
//   - Both nodes and edges maps are initialized lazily on first access to avoid
//     unnecessary allocations for empty graphs.
//   - Missing nodes are detected during TopoSort: any edge referencing a
//     non-existent node (except external references starting with ":" or "//")
//     causes an error.
//
// Nodes and edges are populated by BuildGraph after module collection:
//   - All enabled modules are added as nodes via AddNode.
//   - Dependency properties (deps, shared_libs, header_libs, data) are
//     processed to add edges via AddEdge.
type Graph struct {
	// nodes maps canonical module names to their parsed Module definitions.
	// All enabled modules collected from Blueprint files are stored here.
	// The map key is the module name, which is unique within the graph.
	nodes map[string]*parser.Module

	// edges maps module names to the list of module names they depend on.
	// For example, edges["foo"] = ["bar"] means module "foo" depends on "bar",
	// so "bar" must be built before "foo". Each module name has an entry
	// in the edges map (empty slice if no dependencies).
	edges map[string][]string
}

// NewGraph creates and returns a new, empty dependency graph with initialized
// node and edge maps. The returned graph is ready to have nodes and edges added.
//
// Returns:
//   - *Graph: Newly created graph with empty nodes and edges maps.
//
// How it works:
//   - Allocates both maps using make(map[string]*parser.Module) and make(map[string][]string).
//   - No nodes or edges are present until AddNode/AddEdge are called.
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
//     This name is used as the canonical key in nodes and edges maps.
//   - mod: Parsed module definition containing properties, metadata, and dependencies.
//
// How it works:
//   - Sets nodes[name] = mod, overwriting any existing entry.
//   - If edges[name] does not exist, initializes it to an empty slice.
//
// Edge cases:
//   - Overwriting an existing node preserves the old module reference.
//     This can happen if the same module is defined multiple times (handled upstream).
//   - Empty name is accepted but will cause issues during TopoSort (detected then).
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
//
// How it works:
//   - Initializes edges[from] to an empty slice if absent.
//   - Initializes edges[to] to an empty slice if absent.
//   - Appends "to" to edges[from]'s dependency list.
//
// Edge cases:
//   - Adding an edge to a module not in nodes is allowed at this stage.
//     TopoSort validates that all referenced modules exist.
//   - External module references (":module" or "//namespace:module") are
//     accepted but flagged as missing by TopoSort.
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
// Description:
//
//	Kahn's algorithm produces a valid build order by processing nodes with
//	zero remaining dependencies first, then removing their outgoing edges
//	and repeating until all nodes are processed.
//
// How it works:
//  1. Initialize depCount for each node to the number of its dependencies (edges count).
//  2. Build dependentOf: reverse mapping from dependency to its dependents.
//  3. Initialize queue with all nodes having depCount 0 (no dependencies).
//  4. Sort queue lexicographically for deterministic output.
//  5. Process queue level by level:
//     - Dequeue all nodes at current level (dependencies satisfied).
//     - For each node, decrement depCount of its dependents.
//     - When a dependent's depCount reaches 0, add it to the next level queue.
//  6. If not all nodes were visited, a circular dependency exists.
//
// Parameters: None (operates on the receiver graph).
//
// Returns:
//   - [][]string: Slice of build levels, each a lexicographically sorted list of module names.
//     Modules in earlier levels have no dependencies on modules in later levels.
//   - error: Non-nil if circular dependencies are detected, or a referenced module
//     is missing from the graph. External dependencies (prefixed with ":" or "//")
//     that are not in the graph are ignored during dependency counting but still recorded as edges.
//
// Edge cases:
//   - Modules with no dependencies are placed in the first level.
//   - Each level's modules are sorted lexicographically to ensure deterministic output.
//   - If a module references a non-existent external dependency (starts with ":" or "//"),
//     an error is returned immediately.
//   - If not all nodes have edges entries (possible if only AddNode was called),
//     they are treated as having zero dependencies.
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
// Description:
//
//	This function coordinates the module collection pipeline:
//	1. Calls CollectModulesWithNames to evaluate properties, merge variants, and filter enabled modules.
//	2. Expands global glob patterns across all modules.
//	3. Expands per-module glob patterns within each module's properties.
//
// Parameters:
//   - allDefs: Slice of all parsed Blueprint definitions (modules, assignments, namespaces).
//   - eval: Evaluator for resolving variables and select() expressions in module properties.
//   - opts: Build configuration options controlling filtering, path resolution, and variants.
//
// Returns:
//   - map[string]*parser.Module: Map of enabled module names to their parsed definitions.
//   - error: Non-nil if glob expansion fails for any module or during global expansion.
//
// Edge cases:
//   - Modules with empty names are skipped.
//   - Modules disabled for the current target are excluded from the result.
//   - Glob expansion errors are propagated as errors.
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
// Description:
//
//	This is the core module collection function. It:
//	1. Iterates over all parsed definitions, filtering for module definitions.
//	2. Extracts module name using the provided or default name function.
//	3. Evaluates all module properties (resolving variables and select()).
//	4. Merges architecture-specific variant properties.
//	5. Checks if the module is enabled for the current build target.
//	6. Expands glob patterns globally and per-module.
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
//   - Non-module definitions (variable assignments, namespace blocks) are ignored.
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
// Description:
//
//	This function builds the module dependency graph used for topological sorting.
//	It:
//	1. Creates a new empty graph via NewGraph.
//	2. Adds all modules as nodes via AddNode.
//	3. For each module, processes dependency properties and adds edges.
//
// Parameters:
//   - modules: Map of module names to their parsed definitions (output of CollectModules).
//   - namespaces: Map of namespace names to metadata for resolving cross-namespace references.
//   - eval: Evaluator for resolving property values in dependency lists.
//
// Returns:
//   - *Graph: Fully constructed dependency graph ready for topological sorting.
//
// How it works:
//   - For each module, calls addResolvedDeps for deps, shared_libs, header_libs, and data.
//   - addResolvedDeps resolves namespace references before adding edges.
//
// Edge cases:
//   - Modules with no dependency properties have no outgoing edges.
//   - External module references (":module", "//namespace:module") are resolved
//     but may not exist in the modules map (handled by TopoSort).
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
// Description:
//
//	For each dependency reference in the list:
//	1. If moduleRefsOnly is true, skip non-module references (file paths).
//	2. Resolve the namespace reference to a canonical module name.
//	3. Add the directed edge to the graph.
//
// Parameters:
//   - graph: The dependency graph to add edges to.
//   - from: Name of the dependent module (source of the edge, requires the dependencies).
//   - deps: List of dependency references (module names or namespace-qualified references).
//   - namespaces: Map of namespaces for resolving cross-namespace references.
//   - moduleRefsOnly: If true, only process references starting with ":" or "//";
//     other values (e.g., file paths) are skipped to avoid adding invalid edges.
//
// How it works:
//   - Uses namespace.ResolveModuleRef to canonicalize references like ":lib" or "//ns:lib".
//   - Calls graph.AddEdge for each resolved dependency name.
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
// Description:
//
//	This function sets up the complete Ninja generation environment:
//	1. Builds a map of rule names to rule implementations.
//	2. Calculates path prefixes for relative file references.
//	3. Creates and configures the generator with all settings.
//
// Parameters:
//   - graph: Topologically sorted dependency graph (passed to the generator for build order).
//   - modules: Map of module names to their definitions, passed to the generator.
//   - opts: Build configuration options controlling output paths, toolchain, and target.
//
// Returns:
//   - *ninja.Generator: Configured generator ready to produce Ninja build output.
//
// How it works:
//   - ruleMap: Maps rule names (e.g., "cc_library") to rule implementations.
//   - pathPrefix: Relative path from build output dir to source dir (e.g., "../src/").
//   - Toolchain: Built from opts using toolchainFromOptions.
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
// files relative to the build directory.
//
// Description:
//
//	Computes the relative path from the Ninja output directory (parent of outFile)
//	to the source directory. This allows Ninja rules to reference source files
//	using paths like "prefix/Android.bp" instead of absolute paths.
//
// Parameters:
//   - srcDir: Root source directory containing Blueprint files.
//   - outFile: Path to the output Ninja file (used to determine the build output directory).
//
// Returns:
//   - string: Relative path from build directory to source directory, with trailing slash
//     and forward slashes (Ninja-compatible), or empty string if no prefix is needed.
//
// How it works:
//  1. Get absolute path of build output directory (parent of outFile).
//  2. Get absolute path of source directory.
//  3. If they are the same, return empty string (no prefix needed).
//  4. Otherwise, compute relative path and convert to forward slashes.
//
// Edge cases:
//   - If the build directory (parent of outFile) equals the source directory, returns empty.
//   - Any error in path calculation (absolute path failure, relative path failure) returns empty.
//   - If relative path is ".", returns empty.
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
// Description:
//
//	Builds a command-line invocation of the minibp binary with all flags needed
//	to reproduce the current build configuration. Ninja uses this command
//	in a rule that re-runs when Blueprint files change.
//
// Parameters:
//   - opts: Build configuration options containing executable flags and paths.
//
// Returns:
//   - string: Complete command line for regenerating the Ninja build file, with forward slashes.
//
// How it works:
//  1. Gets executable path from os.Executable().
//  2. Adds architecture and OS flags if set.
//  3. Adds -a flag if single input is a directory.
//  4. Adds output file path.
//  5. Appends input file list.
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
// Description:
//
//	Constructs a complete Toolchain by starting with platform defaults and then
//	applying user-specified overrides from the Options struct. This ensures
//	all required fields are populated even if the user only specified a subset.
//
// Parameters:
//   - opts: Build configuration options containing toolchain executable paths and settings.
//
// Returns:
//   - ninja.Toolchain: Configured toolchain with resolved executable paths.
//
// How it works:
//  1. Start with default toolchain settings via ninja.DefaultToolchain().
//  2. Override individual fields if the corresponding Option field is non-empty.
//  3. Handle Ccache special cases: "no" disables, non-empty enables.
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
