// Package dependency provides advanced dependency management features including
// transitive dependency resolution, conflict detection, and dependency graph
// visualization.
//
// This package implements a complete dependency management system for the build system,
// supporting:
//
// Transitive Dependency Resolution:
//   - calculateTransitiveDeps() computes all dependencies (direct and indirect) for each module
//   - Uses DFS with visited tracking to handle cycles
//   - Deduplicates dependencies by name
//
// Version Conflict Detection:
//   - detectConflicts() identifies when different parts of the dependency tree
//     require different versions of the same dependency
//   - Provides detailed conflict information including the modules requiring each version
//
// Topological Ordering:
//   - topologicalSort() uses Kahn's algorithm to compute valid build order
//   - Ensures dependencies are built before modules that depend on them
//   - Detects circular dependencies
//
// Visualization:
//   - Visualize() generates human-readable text representation
//   - Useful for debugging and logging build issues
//
// Example usage:
//
//	graph := dependency.NewDependencyGraph()
//	graph.AddModule("app", "cc_binary", []dependency.Dependency{
//	    {Name: "libfoo", Version: "1.0"},
//	})
//	graph.AddModule("libfoo", "cc_library", nil)
//	res := graph.ResolveDependencies()
//	if res.Success {
//	    fmt.Println("Build order:", res.Order)
//	}
//
// The graph maintains two edge representations:
//   - edges: forward mapping (module -> its dependencies)
//   - reverseEdges: reverse mapping (dependency -> modules that depend on it)
//
// This bidirectional structure enables efficient queries in both directions.
package dependency

import (
	"fmt"
	"sort"
	"strings"
)

// Dependency represents a module dependency with name, version, and optional status.
// A Dependency captures the relationship between a module and another module it depends on.
//
// Dependencies are declared in module properties like "srcs", "deps", and "lib_deps".
// The build system resolves these declarations into complete dependency graphs
// for determining build order and detecting conflicts.
//
// Design notes:
//   - Version is a string to support arbitrary versioning schemes (semver, date-based, etc.).
//   - Optional dependencies don't cause build failures if missing (useful for test dependencies).
//   - Dependencies are deduplicated by name during transitive resolution.
type Dependency struct {
	// Name is the unique identifier of the dependency module. This is used
	// to look up the dependency in the module registry.
	Name string

	// Version is the version constraint or exact version string.
	// Empty string means "any version" or "latest available".
	Version string

	// Optional indicates whether this dependency is required or optional.
	// Optional dependencies don't cause build failures if missing.
	Optional bool
}

// DependencyGraph represents the complete dependency graph for a build system.
// It maintains mappings of modules to their direct dependencies and dependents,
// enabling transitive dependency resolution, conflict detection, and topological ordering.
//
// Design notes:
//   - The graph uses two edge representations to support efficient bidirectional queries.
//   - edges maps module -> its dependencies (forward edges) for dependency walking.
//   - reverseEdges maps dependency -> modules that depend on it (reverse edges) for impact analysis.
//
// The graph must be populated using AddModule before performing any
// resolution operations. The typical workflow is:
//  1. Create empty graph with NewDependencyGraph
//  2. Add modules with AddModule (which also establishes edges)
//  3. Call ResolveDependencies to compute transitive deps and topological order
//  4. Query the graph using GetDependencies or GetDependents
//
// Fields:
//   - modules: Map of module name to ModuleNode containing module metadata and dependencies
//   - edges: Map of module name to list of its direct dependency names (forward edges)
//   - reverseEdges: Map of module name to list of modules that depend on it (reverse edges)
type DependencyGraph struct {
	// modules maps module names to their ModuleNode representations.
	// Contains all modules added to the graph via AddModule.
	modules map[string]*ModuleNode

	// edges maps module names to their direct dependency names (forward edges).
	// For example, edges["A"] = ["B", "C"] means module A depends on B and C.
	edges map[string][]string

	// reverseEdges maps module names to modules that depend on them (reverse edges).
	// For example, reverseEdges["B"] = ["A"] means module A depends on B.
	reverseEdges map[string][]string
}

// ModuleNode represents a node in the dependency graph corresponding to a single module.
// Each node tracks both direct and transitive dependencies, as well as which modules depend on it.
//
// Design notes:
//   - DirectDeps are the dependencies declared in the module's properties (input).
//   - AllDeps is computed during ResolveDependencies (output, cached).
//   - Dependents are built from reverseEdges during AddModule (output, maintained).
//   - IsRoot is set to true by default; SetNonRoot() may be called to mark dependency-only modules.
//
// Fields:
//   - Name: Unique identifier of the module
//   - Type: Module type (e.g., "cc_library", "java_library")
//   - DirectDeps: Slice of direct dependencies declared by this module
//   - AllDeps: Slice of all transitive dependencies (computed by ResolveDependencies)
//   - Dependents: List of module names that depend on this module
//   - IsRoot: True if this is a root module (directly buildable), false if it's only a dependency
type ModuleNode struct {
	// Name is the unique identifier for this module.
	Name string

	// Type is the module type (e.g., "cc_library", "java_library").
	Type string

	// DirectDeps are the dependencies explicitly declared by this module.
	DirectDeps []Dependency

	// AllDeps contains all transitive dependencies (direct and indirect).
	// Computed by calculateTransitiveDeps during ResolveDependencies.
	AllDeps []Dependency

	// Dependents is the list of module names that have this module as a direct dependency.
	// Built from reverseEdges during AddModule.
	Dependents []string

	// IsRoot indicates whether this is a root module (directly buildable).
	// If false, this module is only used as a dependency of other modules.
	IsRoot bool
}

// Conflict represents a dependency version conflict detected during resolution.
//
// A conflict occurs when a module depends on the same dependency at different versions
// through different dependency paths. For example, if module A depends on
// module B version 1.0, and module C (also depended on by A) depends on
// module B version 2.0, this constitutes a version conflict.
//
// This conflict detection helps identify unsatisfiable dependency
// requirements that would cause build failures at link time.
//
// Fields:
//   - Module: The module where the conflict was detected (typically the common dependent)
//   - DepName: The dependency name that has conflicting versions
//   - Version1: First version required
//   - Version2: Second (different) version required
//   - Path1: List of modules requiring Version1 (the dependency path)
//   - Path2: List of modules requiring Version2
type Conflict struct {
	// Module is the module where the conflict was detected.
	Module string

	// DepName is the name of the dependency with conflicting versions.
	DepName string

	// Version1 is the first version requirement found.
	Version1 string

	// Version2 is the second (different) version requirement found.
	Version2 string

	// Path1 is the list of modules that require Version1.
	Path1 []string

	// Path2 is the list of modules that require Version2.
	Path2 []string
}

// Resolution represents the result of dependency resolution.
//
// The resolution process computes:
//  1. All transitive dependencies for each module
//  2. Any version conflicts between different dependency paths
//  3. A valid build order via topological sort
//
// The Resolution struct is returned by ResolveDependencies and indicates
// whether the dependency graph is valid for building.
//
// Fields:
//   - Success: True if resolution succeeded without conflicts
//   - Conflicts: Slice of detected version conflicts (may be empty)
//   - Order: Topological ordering of modules for build
type Resolution struct {
	// Success indicates whether resolution completed without errors.
	// False if circular dependencies were detected or other failures.
	Success bool

	// Conflicts contains all detected version conflicts.
	// An empty slice means no conflicts were found.
	Conflicts []Conflict

	// Order is the topologically sorted list of module names.
	// Dependencies appear before the modules that depend on them.
	Order []string
}

// NewDependencyGraph creates a new empty dependency graph.
//
// Returns a pointer to a newly initialized DependencyGraph with empty maps.
// The graph starts with no modules and must be populated using AddModule
// before any resolution or query operations can be performed.
//
// Returns:
//   - *DependencyGraph: A new empty dependency graph instance ready for AddModule calls
//
// Edge cases:
//   - The returned graph has no modules, edges, or reverse edges until AddModule is called
//   - All internal maps are initialized to empty (non-nil), preventing nil map access panics
//
// Notes:
//   - This function never returns nil; the graph is fully initialized
//   - Modules must be added via AddModule before calling ResolveDependencies or query methods
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		modules:      make(map[string]*ModuleNode),
		edges:        make(map[string][]string),
		reverseEdges: make(map[string][]string),
	}
}

// AddModule adds a module to the dependency graph with its declared dependencies.
//
// This function registers a module in the graph and establishes edges to represent
// the dependency relationships. Both forward and reverse edges are maintained:
//   - forward edges (edges): module -> list of its dependencies
//   - reverse edges (reverseEdges): dependency -> list of modules that depend on it
//
// The function should be called once for each module in the build system.
// After all modules are added, call ResolveDependencies to compute
// transitive dependencies and build order.
//
// Parameters:
//   - name: Unique identifier for the module. Must not already exist in the graph.
//   - moduleType: Type of the module (e.g., "cc_library", "java_library")
//   - deps: Slice of Dependency objects representing direct dependencies.
//     Can be empty if the module has no dependencies.
//
// How it works:
//  1. Check for duplicate module name; return early if exists.
//  2. Create ModuleNode with DirectDeps and IsRoot=true.
//  3. Add to modules map.
//  4. Initialize edges and reverseEdges entries.
//  5. For each dependency, append to edges[name] and reverseEdges[dep.Name].
//
// Edge cases:
//   - If a module with the same name already exists, this function returns early
//     without modifying the graph. This prevents duplicate entries.
//   - Empty deps slice is valid and creates a module with no dependencies.
//   - Dependencies that don't yet exist in the graph are still recorded;
//     they will be created when added via a separate call.
func (g *DependencyGraph) AddModule(name, moduleType string, deps []Dependency) {
	// Check for duplicate module name.
	// Duplicate prevention ensures each module is added exactly once.
	if _, exists := g.modules[name]; exists { // Module already exists, skip duplicate addition
		return
	}

	// Create the module node.
	// By default, IsRoot is true; later code may set it to false if the module
	// is only referenced as a dependency (not a build target).
	node := &ModuleNode{
		Name:       name,
		Type:       moduleType,
		DirectDeps: deps,
		IsRoot:     true,
	}
	g.modules[name] = node
	g.edges[name] = make([]string, 0)
	g.reverseEdges[name] = make([]string, 0)

	// Establish edges for each dependency.
	// This populates both forward and reverse edge mappings.
	// Note: reverseEdges may create entries for dependencies that don't exist yet;
	// this is acceptable as they'll be created later or may be external.
	for _, dep := range deps {
		g.edges[name] = append(g.edges[name], dep.Name)
		g.reverseEdges[dep.Name] = append(g.reverseEdges[dep.Name], name)
	}
}

// ResolveDependencies resolves all dependencies in the graph and detects conflicts.
//
// This function performs three main operations:
//  1. Calculates transitive dependencies for all modules (via calculateTransitiveDeps)
//  2. Detects version conflicts between different module paths
//  3. Computes topological ordering for build
//
// The function processes the entire graph: for each module, it traverses
// the dependency tree to collect all transitive dependencies. Then it
// analyzes whether any dependency is required at multiple different versions.
//
// This is typically called after all modules have been added to the graph
// and before generating the build file.
//
// Returns:
//   - *Resolution: Resolution result containing success status, conflicts, and build order.
//     The Success field indicates whether the graph is valid for building.
//
// How it works:
//  1. Initialize Resolution with Success=true.
//  2. For each module, call calculateTransitiveDeps to populate AllDeps.
//  3. Call detectConflicts to find version mismatches; set Success=false if found.
//  4. Call topologicalSort to compute build order; set Success=false on error.
//  5. Return the Resolution object.
//
// Edge cases:
//   - If circular dependencies are detected, topological sort fails and Success is false
//   - Modules with no dependencies are valid and appear first in the topological order
//   - Version conflicts don't prevent topological ordering; they're reported in Conflicts
//   - Empty graphs (no modules) return empty order with Success=true
func (g *DependencyGraph) ResolveDependencies() *Resolution {
	resolution := &Resolution{
		Success:   true,
		Conflicts: make([]Conflict, 0),
	}

	// Step 1: Calculate transitive dependencies for all modules.
	// This populates the AllDeps field of each ModuleNode.
	// The calculation uses DFS with visited tracking to handle cycles.
	for moduleName := range g.modules {
		g.calculateTransitiveDeps(moduleName)
	}

	// Step 2: Detect version conflicts.
	// A conflict occurs when different parts of the graph require
	// different versions of the same dependency.
	conflicts := g.detectConflicts()
	if len(conflicts) > 0 {
		resolution.Success = false
		resolution.Conflicts = conflicts
	}

	// Step 3: Compute topological order for build.
	// Topological sort produces a valid build sequence where
	// dependencies are built before the modules that depend on them.
	order, err := g.topologicalSort()
	if err != nil {
		resolution.Success = false
		return resolution
	}
	resolution.Order = order

	return resolution
}

// calculateTransitiveDeps calculates all transitive dependencies for a module.
//
// This function performs a depth-first traversal starting from the given module,
// collecting all direct and indirect dependencies. The algorithm ensures:
//   - Every dependency (direct and transitive) is included
//   - Duplicate dependencies are removed
//   - Cycles are handled via visited tracking
//
// How it works:
//  1. Uses recursive DFS with a visited set to track visited modules
//  2. For each visited module, adds all its direct dependencies
//  3. Recursively visits each dependency
//  4. Deduplicates the collected dependencies
//  5. Stores the result in node.AllDeps
//
// Parameters:
//   - moduleName: The name of the module to calculate transitive deps for
//
// Returns: No return value (modifies node.AllDeps in place)
//
// Edge cases:
//   - Unknown module names are silently skipped (no error)
//   - Cycles are handled via visited tracking (won't infinite loop)
//   - Empty dependency lists return empty AllDeps
//   - External dependencies (not in graph) are included but don't recurse
func (g *DependencyGraph) calculateTransitiveDeps(moduleName string) {
	// Track visited modules to prevent infinite recursion on cycles.
	visited := make(map[string]bool)
	var deps []Dependency

	// Inner function for recursive DFS traversal.
	// Adds each module's direct dependencies, then recurses.
	var visit func(name string)
	visit = func(name string) {
		// Skip already visited modules to handle cycles.
		if visited[name] { // Skip already visited modules to prevent infinite recursion on cycles
			return
		}
		visited[name] = true

		// Add all direct dependencies of this module.
		if node, exists := g.modules[name]; exists { // Module exists in graph, process its direct dependencies
			for _, dep := range node.DirectDeps {
				deps = append(deps, dep)
				// Recursively visit the dependency.
				visit(dep.Name)
			}
		}
	}

	// Start traversal from the target module.
	visit(moduleName)

	// Remove duplicate dependencies.
	// Dependencies may appear multiple times due to different paths,
	// so we deduplicate by name.
	seen := make(map[string]bool)
	var uniqueDeps []Dependency
	for _, dep := range deps {
		if !seen[dep.Name] { // First occurrence of dependency, add to unique list
			seen[dep.Name] = true
			uniqueDeps = append(uniqueDeps, dep)
		}
	}

	// Store the deduplicated transitive dependencies.
	if node, exists := g.modules[moduleName]; exists {
		node.AllDeps = uniqueDeps
	}
}

// detectConflicts detects version conflicts in dependencies.
//
// This function analyzes all modules in the graph to find dependencies that are
// required at different versions by different parts of the graph.
//
// The algorithm:
//  1. Build a map of required versions for each dependency:
//     (depName -> version -> [modules requiring that version])
//  2. For each dependency, check if multiple versions are required
//  3. Create Conflict entries for each dependency with version conflicts
//
// A version conflict is detected when len(versions[depName]) > 1, meaning
// different parts of the dependency tree require different versions.
//
// Returns:
//   - []Conflict: Slice of Conflict objects; may be empty if no conflicts exist
//
// Edge cases:
//   - Dependencies with only one version have no conflict
//   - Dependencies with no explicit version (empty string) are treated as versioned
//   - The conflict only records first two versions found; additional versions are ignored
func (g *DependencyGraph) detectConflicts() []Conflict {
	var conflicts []Conflict

	// Build map of required versions for each dependency.
	// Structure: depName -> version -> [modules requiring that version]
	requiredVersions := make(map[string]map[string][]string)
	for moduleName, node := range g.modules {
		for _, dep := range node.DirectDeps {
			if _, exists := requiredVersions[dep.Name]; !exists { // First time seeing this dependency, initialize version map
				requiredVersions[dep.Name] = make(map[string][]string)
			}
			requiredVersions[dep.Name][dep.Version] = append(requiredVersions[dep.Name][dep.Version], moduleName)
		}
	}

	// Scan for conflicts: dependencies required at multiple versions.
	for depName, versions := range requiredVersions {
		// Multiple versions requested = conflict.
		if len(versions) > 1 { // Multiple versions required for same dependency, conflict detected
			conflict := Conflict{
				DepName:  depName,
				Version1: "",
				Version2: "",
			}
			// Record first two conflicting versions and their paths.
			// Additional versions beyond the first two are not recorded.
			for version, modules := range versions {
				if conflict.Version1 == "" { // First conflicting version, record it
					conflict.Version1 = version
					conflict.Path1 = modules
				} else {
					conflict.Version2 = version
					conflict.Path2 = modules
					break // Only need first two for conflict resolution
				}
			}
			conflicts = append(conflicts, conflict)
		}
	}
	return conflicts
}

// topologicalSort performs topological sort on the dependency graph using Kahn's algorithm.
//
// This function computes a valid build order where dependencies are built before
// the modules that depend on them. It uses Kahn's algorithm:
//
// Algorithm steps:
//  1. Calculate in-degree for each module (count of direct dependencies)
//  2. Initialize queue with modules having in-degree 0 (no dependencies)
//  3. Process queue: for each module, find unprocessed modules that depend on it
//     and add them to queue when all their dependencies are processed
//  4. Sort queue for deterministic ordering (alphabetical)
//  5. Check for circular dependencies (not all modules processed)
//
// In--degree for a module = number of direct dependencies it has.
// A module can be built when all its dependencies have been built.
//
// Returns:
//   - []string: Topological ordering of module names (dependencies before dependents)
//   - error: Non-nil if circular dependencies are detected
//
// Edge cases:
//   - Empty graph returns empty slice with nil error
//   - Modules with no dependencies appear first in result
//   - Circular dependencies return error but may include partial order
//   - Multiple valid orderings are resolved by alphabetical sort for determinism
func (g *DependencyGraph) topologicalSort() ([]string, error) {

	// Step 1: Initialize in-degree map.
	// Each module starts with in-degree 0; we'll count dependencies next.
	// In--degree represents the number of direct dependencies a module has.
	// A module with in-degree 0 has no dependencies and can be built first.
	inDegree := make(map[string]int)

	for name := range g.modules {

		inDegree[name] = 0

	}

	// Step 2: Calculate in-degrees (dependency count).
	// A module's in-degree = number of direct dependencies it has.
	// This is simply the length of its edges list.
	// In Kahn's algorithm, we process modules with in-degree 0 first,
	// then decrement in-degree of their dependents as we process them.
	for name := range g.modules {

		inDegree[name] = len(g.edges[name])

	}

	// Step 3: Initialize queue with nodes having in-degree 0.
	// These modules have no dependencies and can be built first.
	queue := []string{}

	for name, degree := range inDegree {

		if degree == 0 { // Module has no dependencies, add to initial queue

			queue = append(queue, name)

		}

	}

	// Sort queue for deterministic ordering.
	// Alphabetical sort ensures consistent results across runs.
	sort.Strings(queue)

	result := []string{}

	// Track which modules have been added to result.
	// This is separate from queue presence for cycle detection.
	processed := make(map[string]bool)

	// Step 4: Process queue until empty.
	// This is the main loop of Kahn's algorithm.
	// In each iteration, we:
	//   1. Dequeue a module with in-degree 0 (all its dependencies are built)
	//   2. Add it to the result (build order)
	//   3. For each module that depends on it, decrement their effective in-degree
	//   4. When a dependent's in-degree reaches 0, add it to the queue
	// For simplicity, we recalculate readiness by checking if all dependencies are processed.
	for len(queue) > 0 {

		// Dequeue first element (FIFO).
		// We use slice indexing to get the first element and reslice to remove it.
		node := queue[0]

		queue = queue[1:]

		// Add to result and mark processed.
		// Once a module is in result, all its dependencies are guaranteed to be built.
		result = append(result, node)

		processed[node] = true

		// Find modules that depend on this node.
		// A module can be added to queue when all its dependencies are processed.
		// We iterate through all edges to find modules that have this node as a dependency.
		for moduleName, deps := range g.edges {

			// Skip already processed modules.
			// Once processed, a module should never be processed again.
			if processed[moduleName] { // Module already processed, skip

				continue

			}

			// Check if this module depends on the current node.
			// We need to find if node is in deps (the module's dependency list).
			hasDep := false

			for _, dep := range deps {

				if dep == node { // Found dependency on current node, mark hasDep

					hasDep = true

					break

				}

			}

			if hasDep {

				// Check if all dependencies are now processed.
				// A module is ready when every dependency appears in processed.
				// We check all deps except the current node (which we just processed).
				allProcessed := true

				for _, dep := range g.edges[moduleName] {

					if !processed[dep] && dep != node { // Dependency not processed, module not ready

						allProcessed = false

						break

					}

				}

				// Add to queue if ready and not already queued.
				// We check for duplicates to avoid processing the same module twice.
				if allProcessed { // All dependencies processed, module is ready to build

					// Check if already in queue to avoid duplicates.
					// Duplicate entries would cause the module to appear twice in result.
					found := false

					for _, q := range queue {

						if q == moduleName { // Module already in queue, skip duplicate

							found = true

							break

						}

					}

					if !found {

						queue = append(queue, moduleName)

						// Re-sort to maintain alphabetical ordering.
						// This ensures deterministic output across runs.
						sort.Strings(queue)

					}

				}

			}

		}

	}

	// Step 5: Check for circular dependencies.
	// If not all modules were processed, there's a cycle.
	if len(result) != len(g.modules) { // Not all modules processed, circular dependency exists

		return nil, fmt.Errorf("circular dependency detected")

	}

	return result, nil

}

// GetDependents returns all modules that directly depend on the given module.
//
// This function looks up the reverse edges mapping to find all modules that have
// the specified module as a direct dependency. It's useful for understanding
// the impact of changes to a module - knowing what will need to be
// rebuilt if a module changes.
//
// The reverseEdges map is maintained by AddModule whenever a dependency is added.
// It's efficient for answering "what depends on X?" queries.
//
// Parameters:
//   - moduleName: The name of the module to find dependents for.
//     Must be a valid module name in the graph.
//
// Returns:
//   - []string: List of module names that depend on this module; may be empty
//
// Edge cases:
//   - Unknown modules return empty slice (no error)
//   - Modules with no dependents return empty slice
//   - The returned slice is a direct reference to internal map; treat as read-only
func (g *DependencyGraph) GetDependents(moduleName string) []string {
	return g.reverseEdges[moduleName]
}

// GetDependencies returns all direct dependencies of a module.
//
// This function returns the forward edges (dependency names) for the given module.
// Unlike AllDeps (which includes transitive dependencies), this only returns
// the dependencies explicitly declared in the module's properties.
//
// For example, if module A declares deps = ["B", "C"], then GetDependencies("A")
// returns ["B", "C"], even if B depends on D.
//
// Parameters:
//   - moduleName: The name of the module to get dependencies for.
//
// Returns:
//   - []string: List of direct dependency names; may be nil for modules with no deps
//
// Edge cases:
//   - Unknown modules return nil (not empty slice)
//   - Modules with no dependencies return nil (not empty slice)
//   - The returned slice is a direct reference to internal map; treat as read-only
func (g *DependencyGraph) GetDependencies(moduleName string) []string {
	return g.edges[moduleName]
}

// Visualize generates a text representation of the dependency graph.
//
// This function creates a human-readable string showing all modules,
// their types, and their direct dependencies with versions.
// The output is suitable for debugging and logging.
//
// Returns:
//   - string: Multi-line text representation of the graph
//
// Output format:
//
//	ModuleName (module_type)
//	  -> dependency_name [version]
//	  -> dependency_name [version]
//	  (no dependencies)
//
// Each module entry is separated by a blank line.
// The output is not guaranteed to be in any particular order.
func (g *DependencyGraph) Visualize() string {
	var sb strings.Builder
	sb.WriteString("Dependency Graph:\n")
	sb.WriteString(strings.Repeat("-", 40) + "\n")

	for name, node := range g.modules {
		sb.WriteString(fmt.Sprintf("%s (%s)\n", name, node.Type))
		if len(node.DirectDeps) > 0 { // Module has direct dependencies, list them
			for _, dep := range node.DirectDeps {
				sb.WriteString(fmt.Sprintf(" -> %s [%s]\n", dep.Name, dep.Version))
			}
		} else { // No direct dependencies, note it
			sb.WriteString(" (no dependencies)\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// GetModule returns a module node by name.
//
// Parameters:
//   - name: The name of the module to retrieve
//
// Returns:
//   - *ModuleNode: Pointer to the module node if found, nil otherwise
//   - bool: True if the module exists in the graph
//
// Edge cases:
//   - Unknown module names return nil, false
//   - The returned node is a pointer to the internal struct; modifications affect the graph
//
// Notes:
//   - Use the boolean return value to check existence instead of nil checks
//   - The ModuleNode pointer is valid as long as the graph is not modified via AddModule
func (g *DependencyGraph) GetModule(name string) (*ModuleNode, bool) {
	node, exists := g.modules[name]
	return node, exists
}

// GetAllModules returns all module nodes in the graph.
//
// Returns a slice containing pointers to all ModuleNode objects in the graph.
// The order of modules in the slice is not guaranteed.
//
// Returns:
//   - []*ModuleNode: Slice containing all module nodes; may be empty
//
// Edge cases:
//   - Empty graph returns empty slice (not nil)
//   - The returned slice is a new slice; internal modifications are safe
//
// Notes:
//   - Modifying the returned slice does not affect the graph's internal module list
//   - To modify a module, retrieve it via GetModule and edit the struct directly
func (g *DependencyGraph) GetAllModules() []*ModuleNode {
	modules := make([]*ModuleNode, 0, len(g.modules))
	for _, node := range g.modules {
		modules = append(modules, node)
	}
	return modules
}

// String returns a string representation of the graph.
//
// This is a convenience method that calls Visualize() to generate
// the string representation. It implements the fmt.Stringer interface.
//
// Returns:
//   - string: Text representation of the dependency graph
func (g *DependencyGraph) String() string {
	return g.Visualize()
}
