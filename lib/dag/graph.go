// Package dag provides functionality for building and analyzing
// directed acyclic graphs (DAGs) of module dependencies.
// This is used for determining build order and parallel execution of modules.
//
// The package implements Kahn's algorithm for topological sorting,
// organizing modules into levels where modules at each level can be
// built in parallel. This enables efficient build parallelization
// while respecting dependency ordering.
//
// Key features:
//   - Graph data structure for module dependency tracking
//   - Kahn's algorithm for topological sorting
//   - Level-based grouping for parallel execution
//   - Cycle detection with detailed error messages
//
// Topological sorting produces a build order where:
//   - Dependencies are built before the modules that depend on them
//   - Modules at the same level can be built in parallel
//   - No circular dependencies exist
//
// This package is distinct from the dependency package:
//   - dag: Handles module-to-module dependencies (what depends on what)
//   - dependency: Handles library-level version constraints
package dag

import (
	"fmt"
	"sort"

	"minibp/lib/module"
)

// Graph represents a directed acyclic graph of module dependencies.
// It is the core data structure for tracking build order and enabling
// parallel execution of modules in the minibp build system.
//
// The graph is used by the build system to:
//   - Determine the correct build order for modules
//   - Enable parallel execution by grouping modules into levels
//   - Detect circular dependencies that would make building impossible
//   - Validate that all referenced modules exist in the build configuration
//
// The graph uses two internal data structures:
//   - modules: Maps module names to their Module objects for validation and lookup.
//     This ensures all referenced modules are properly registered before building.
//   - edges: Maps module names to lists of their direct dependencies.
//     The edge direction follows build dependency: if A depends on B,
//     then edge goes from A -> B, meaning B must be built before A.
//
// Example: If module "app" depends on "lib1" and "lib2",
// the edges map would contain: edges["app"] = ["lib1", "lib2"].
// This means "lib1" and "lib2" must be built before "app".
//
// The edges direction is critical for correct build order:
//   - from: The dependent module (the module that depends on others)
//   - to: The dependency module (the module that must be built first)
//   - edges[from] contains all modules that "from" depends on
type Graph struct {
	// Modules is the collection of all registered modules in the graph,
	// keyed by module name. This map is used for validating that all
	// referenced modules exist and for looking up module objects during
	// dependency resolution. Only modules added via AddModule are stored here.
	Modules map[string]module.Module

	// Edges represents the dependency relationships between modules.
	// The map key is the dependent module (the module that depends on others),
	// and the value is a list of dependency modules that must be built first.
	// For example, if module A depends on modules B and C, then
	// edges["A"] = ["B", "C"], meaning B and C must be built before A.
	//
	// The edge direction follows build dependencies: an edge from X to Y
	// means Y must be completed before X can begin. This allows the
	// topological sort to produce a valid build order.
	Edges map[string][]string
}

// NewGraph creates and returns a new empty dependency graph.
// This is the constructor function for the Graph type and serves as
// the starting point for building a module dependency graph.
//
// The returned graph contains two initialized empty maps:
//   - modules: Stores registered Module objects keyed by module name
//   - edges: Stores dependency relationships keyed by dependent module name
//
// After creating the graph, use AddModule to register modules and
// AddEdge to define dependency relationships between them.
//
// Returns:
//   - *Graph: A pointer to a newly initialized Graph instance.
//     The graph is ready to accept modules and edges immediately.
//     Returns a non-nil pointer; the maps are initialized and ready for use.
//
// Example usage:
//
//	graph := dag.NewGraph()
//	graph.AddModule(moduleA)
//	graph.AddModule(moduleB)
//	graph.AddEdge("A", "B")  // Module A depends on module B
func NewGraph() *Graph {
	return &Graph{
		Modules: make(map[string]module.Module),
		Edges:   make(map[string][]string),
	}
}

// AddModule registers a module with the dependency graph.
// This method adds a module to the graph's module registry, making it
// available for dependency tracking and topological sorting.
//
// The module must implement the module.Module interface, which provides
// the Name() method used as the unique identifier in the graph.
// After registration, the module can be referenced by name when adding
// edges or querying dependencies.
//
// This method also ensures the module has an entry in the edges map
// by initializing an empty dependency slice if one doesn't exist.
// This design allows modules to be registered before their dependencies
// are defined, providing flexibility in graph construction order.
//
// Parameters:
//   - m: The module to register with the graph.
//     Must implement the module.Module interface.
//     The module's Name() method is used as the key in the modules map.
//     If a module with the same name already exists, it will be replaced.
//
// Returns:
//   - This method does not return a value.
//   - On success, the module is added to g.Modules and g.Edges.
//   - If m is nil, the method returns immediately without modification.
//
// Edge cases:
//   - If m is nil: the function returns without any modification to the graph.
//   - If module with same name exists: the existing entry is replaced (overwritten).
//   - Module with no dependencies: still gets an empty edges list entry.
//   - Module Name() returns empty string: creates entry with empty string key.
//   - Concurrent access: not thread-safe; external synchronization required.
func (g *Graph) AddModule(m module.Module) {
	if m != nil {
		g.Modules[m.Name()] = m
		// Initialize edges slice if not exists
		// This ensures module exists in edges map even with no deps
		if _, exists := g.Edges[m.Name()]; !exists {
			g.Edges[m.Name()] = []string{}
		}
	}
}

// AddEdge adds a dependency relationship between two modules in the graph.
// This method records that the module named 'from' depends on the module named 'to',
// meaning 'to' must be built or processed before 'from' during the build process.
//
// The method ensures both modules exist in the edges map by initializing
// empty dependency slices if they do not already exist. This design allows
// adding edges before all modules are registered with AddModule, providing
// flexibility in the order of graph construction.
//
// After calling AddEdge("A", "B"), the edges map will contain an entry
// where edges["A"] includes "B", indicating A depends on B.
//
// Parameters:
//   - from: The name of the dependent module (the module that depends on another).
//     This module requires 'to' to be built before it can be built.
//   - to: The name of the dependency module (the module that must be processed first).
//     This module must be built before 'from' can be built.
//
// Important notes:
//   - This method does not validate that the modules actually exist in the
//     graph's modules map; validation is performed during TopoSort.
//   - Duplicate edges are allowed here and will result in duplicate entries
//     in the dependency list; TopoSort handles this by processing unique modules.
//   - Self-dependencies (from == to) are allowed here but will be detected
//     as cycles during topological sorting.
//
// Edge cases:
//   - If either module doesn't exist in edges map: creates an empty slice for it.
//   - If from == to (self-dependency): allowed here, detected as cycle in TopoSort.
//   - If duplicate edge exists: appends duplicate entry (handled in TopoSort).
//   - Empty module names: will create entries with empty string keys.
func (g *Graph) AddEdge(from, to string) {
	// Ensure both modules exist in edges map
	// Initialize empty slices for new modules
	if _, exists := g.Edges[from]; !exists {
		g.Edges[from] = []string{}
	}
	if _, exists := g.Edges[to]; !exists {
		g.Edges[to] = []string{}
	}
	// Add dependency relationship
	g.Edges[from] = append(g.Edges[from], to)
}

// GetDeps returns a copy of the direct dependencies for a named module.
// This method retrieves all modules that the specified module directly depends on.
// A copy of the internal dependency slice is returned to prevent external code
// from modifying the graph's internal state, ensuring data integrity.
//
// The returned dependencies represent modules that must be built before
// the queried module. For example, if module "app" depends on "lib1" and "lib2",
// calling GetDeps("app") returns ["lib1", "lib2"].
//
// Parameters:
//   - name: The name of the module to query for dependencies.
//     This should be the name returned by module.Name() for registered modules.
//
// Returns:
//   - []string: A slice containing the names of all direct dependencies.
//     The order matches the order in which dependencies were added via AddEdge.
//     Returns a new slice (copy) each time, so modifications to the returned
//     slice do not affect the internal graph state.
//   - If the module name is not found in the edges map, returns an empty slice.
//   - If the module exists but has no dependencies, returns an empty slice.
//
// Edge cases:
//   - Module name doesn't exist in graph: returns empty slice (no error raised).
//   - Module exists but has no dependencies: returns empty slice (not nil).
//   - Returned slice is a copy; modifications don't affect internal state.
//   - Concurrent access: not thread-safe; external synchronization required.
func (g *Graph) GetDeps(name string) []string {
	if deps, exists := g.Edges[name]; exists {
		// Return a copy to prevent external modification
		// This protects the internal graph state
		result := make([]string, len(deps))
		copy(result, deps)
		return result
	}
	return []string{}
}

// TopoSort returns modules in topological order, grouped by levels for parallel execution.
// This function performs a Kahn's algorithm-based topological sort and organizes
// the result into levels where modules at the same level can be executed in parallel.
//
// The algorithm works as follows:
//  1. Calculate in-degree (number of dependencies) for each module
//  2. Build reverse edges to track which modules depend on each module
//  3. Iteratively find all modules with in-degree 0 (modules with no remaining dependencies)
//  4. Process these modules, reduce in-degree of their dependents, and move to next level
//  5. Continue until all modules are processed or a cycle is detected
//
// Returns:
//   - [][]string: A slice of slices, where each inner slice represents a level.
//     Modules at the same level have no dependencies on each other and can run in parallel.
//     Level 0 contains modules with no dependencies (leaf nodes), level 1 contains
//     modules that depend only on level 0 modules, and so on.
//   - error: Returns an error if there's a cycle in the graph (which would make it
//     impossible to determine a valid build order), or if referenced modules don't exist.
//
// Example return value: [["D"], ["B", "C"], ["A"]] means:
//   - Level 0: D (no dependencies)
//   - Level 1: B and C (both depend only on D)
//   - Level 2: A (depends on both B and C)
//
// Edge cases:
//   - Empty graph returns empty levels slice.
//   - Graph with only independent modules returns single level.
//   - Self-dependency (A depends on A) detected as cycle.
//   - Cycle involving multiple modules detected with dependency chain info.
func (g *Graph) TopoSort() ([][]string, error) {
	// Step 1: Calculate in-degree for each node
	// In-degree represents how many dependencies a module has
	// that haven't been processed yet
	inDegree := make(map[string]int)
	for name := range g.Modules {
		inDegree[name] = 0
	}

	// Step 2: Validate dependencies and count in-degrees
	// For each module, count its dependencies and validate they exist
	for from, deps := range g.Edges {
		// Validate that the module itself exists
		if _, exists := g.Modules[from]; !exists {
			return nil, fmt.Errorf("module '%s' referenced in dependency graph does not exist", from)
		}
		// Validate each dependency exists
		for _, to := range deps {
			if _, exists := g.Modules[to]; !exists {
				return nil, fmt.Errorf("dependency '%s' of module '%s' does not exist", to, from)
			}
			// "from" depends on "to", so "from" has an incoming edge
			// Increment in-degree for dependent module
			inDegree[from]++
		}
	}

	// Step 3: Build reverse edges: dependency -> list of dependents
	// When a dependency is processed, we inform its dependents
	// This allows reducing in-degree of dependents when dependency completes
	reverseEdges := make(map[string][]string)
	for from, deps := range g.Edges {
		for _, to := range deps {
			reverseEdges[to] = append(reverseEdges[to], from)
		}
	}

	// Step 4: Kahn's algorithm with queue - process levels iteratively
	// Use a queue to track nodes with in-degree 0, avoiding O(V²) scans
	var levels [][]string
	processed := make(map[string]bool)
	nodeCount := len(g.Modules)

	// Initialize queue with all nodes that have in-degree 0
	queue := make([]string, 0, nodeCount)
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}

	// Process level by level
	for len(queue) > 0 {
		// Current level: all nodes with in-degree 0 at this iteration
		currentLevel := make([]string, len(queue))
		copy(currentLevel, queue)
		queue = queue[:0] // Reset queue for next level

		// Sort level for deterministic output (critical for reproducible builds)
		sort.Strings(currentLevel)
		levels = append(levels, currentLevel)

		// Process each node in current level
		for _, name := range currentLevel {
			processed[name] = true
			// Reduce in-degree of dependents; add to queue if in-degree reaches 0
			for _, dependent := range reverseEdges[name] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					queue = append(queue, dependent)
				}
			}
		}
	}

	// Cycle detection: if not all nodes were processed, a cycle exists
	if len(processed) < nodeCount {
		var remaining []string
		for name := range g.Modules {
			if !processed[name] {
				remaining = append(remaining, name)
			}
		}
		cycleInfo := fmt.Sprintf("cycle detected in dependency graph involving modules: %v", remaining)
		for _, name := range remaining {
			deps := g.GetDeps(name)
			if len(deps) > 0 {
				cycleInfo += fmt.Sprintf("; %s depends on %v", name, deps)
			}
		}
		return nil, fmt.Errorf("%s", cycleInfo)
	}

	return levels, nil
}
