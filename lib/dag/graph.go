// Package dag provides functionality for building and analyzing
// directed acyclic graphs (DAGs) of module dependencies.
// This is used for determining build order and parallel execution of modules.
//
// The package implements Kahn's algorithm for topological sorting,
// organizing modules into levels where modules at each level can be
// built in parallel. This enables efficient build parallelization
// while respecting dependency ordering.
package dag

import (
	"fmt"
	"sort"

	"minibp/lib/module"
)

// Graph represents a directed acyclic graph of module dependencies.
// The graph tracks which modules exist and their dependency relationships,
// enabling topological sorting for build order determination.
//
// The graph uses two internal data structures:
//   - modules: maps module names to their Module objects
//   - edges: maps module names to lists of their direct dependencies
//
// The edges direction follows the build dependency:
// if A depends on B, then edge goes from A -> B.
// This means B must be built before A.
type Graph struct {
	// modules stores all modules in the graph, keyed by module name.
	// Used for validation and module lookup.
	modules map[string]module.Module

	// edges stores dependency relationships: key is dependent module,
	// value is list of dependencies that must be built first.
	// For example, if A depends on B and C, edges["A"] = ["B", "C"].
	edges map[string][]string
}

// NewGraph creates a new empty dependency graph.
// Returns a pointer to a newly initialized Graph with empty maps
// for both modules and edges. This is the starting point for building
// a dependency graph - modules and edges can be added using
// AddModule and AddEdge methods respectively.
//
// Returns:
//   - *Graph: A newly initialized graph with no modules or edges.
//
// Example:
//
//	graph := dag.NewGraph()
//	graph.AddModule(moduleA)
//	graph.AddModule(moduleB)
//	graph.AddEdge("A", "B")  // A depends on B
func NewGraph() *Graph {
	return &Graph{
		modules: make(map[string]module.Module),
		edges:   make(map[string][]string),
	}
}

// AddModule adds a module to the graph.
// The module must implement the module.Module interface.
// If the provided module is nil, no action is taken.
// After adding, the module can be referenced by its Name() for
// adding edges and retrieving dependencies.
//
// This method also initializes an empty edges list for the module
// if one doesn't already exist, allowing for modules with no dependencies.
//
// Parameters:
//   - m: The module to add. Must not be nil. The module's Name()
//     is used as the key in the modules map.
//
// Edge cases:
//   - If m is nil, the function returns without modification.
//   - If module with same name exists, it is replaced.
//   - Module with no dependencies still gets empty edges list.
func (g *Graph) AddModule(m module.Module) {
	if m != nil {
		g.modules[m.Name()] = m
		// Initialize edges slice if not exists
		// This ensures module exists in edges map even with no deps
		if _, exists := g.edges[m.Name()]; !exists {
			g.edges[m.Name()] = []string{}
		}
	}
}

// AddEdge adds a dependency edge from 'from' to 'to'.
// This represents that the module 'from' depends on module 'to'.
// In other words, 'to' must be built/processed before 'from'.
//
// This method ensures both modules exist in the edges map by
// initializing empty slices if they don't already exist.
// It then appends 'to' to the list of dependencies for 'from'.
//
// Parameters:
//   - from: the name of the dependent module (the module that depends on another).
//   - to: the name of the dependency module (the module that must be processed first).
//
// Note: AddEdge does not validate that the modules actually exist in the
// graph; this validation is performed during TopoSort.
// This allows adding edges before all modules are registered.
//
// Edge cases:
//   - Either module doesn't exist in edges map: creates empty slice.
//   - from == to (self-dependency): allowed here, caught in TopoSort.
//   - Duplicate edge: results in duplicate dependency (handled in TopoSort).
func (g *Graph) AddEdge(from, to string) {
	// Ensure both modules exist in edges map
	// Initialize empty slices for new modules
	if _, exists := g.edges[from]; !exists {
		g.edges[from] = []string{}
	}
	if _, exists := g.edges[to]; !exists {
		g.edges[to] = []string{}
	}
	// Add dependency relationship
	g.edges[from] = append(g.edges[from], to)
}

// GetDeps returns the direct dependencies of a module by name.
// A copy of the dependency slice is returned to prevent external
// modification of the internal state.
//
// Parameters:
//   - name: the name of the module to get dependencies for.
//
// Returns:
//   - A slice of strings containing the names of all direct dependencies.
//   - An empty slice if the module doesn't exist or has no dependencies.
//
// Edge cases:
//   - Module name doesn't exist: returns empty slice (no error).
//   - Module has no dependencies: returns empty slice.
//   - Returned slice is a copy; modifications don't affect internal state.
func (g *Graph) GetDeps(name string) []string {
	if deps, exists := g.edges[name]; exists {
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
	for name := range g.modules {
		inDegree[name] = 0
	}

	// Step 2: Validate dependencies and count in-degrees
	// For each module, count its dependencies and validate they exist
	for from, deps := range g.edges {
		// Validate that the module itself exists
		if _, exists := g.modules[from]; !exists {
			return nil, fmt.Errorf("module '%s' referenced in dependency graph does not exist", from)
		}
		// Validate each dependency exists
		for _, to := range deps {
			if _, exists := g.modules[to]; !exists {
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
	for from, deps := range g.edges {
		for _, to := range deps {
			reverseEdges[to] = append(reverseEdges[to], from)
		}
	}

	// Step 4: Kahn's algorithm - process levels iteratively
	// Each iteration finds modules ready to build (in-degree = 0)
	var levels [][]string
	visited := make(map[string]bool)
	nodeCount := len(g.modules)

	// Continue processing until all modules have been assigned to a level
	for len(visited) < nodeCount {
		// Find all nodes with in-degree 0 that haven't been visited
		// These are modules whose dependencies have all been processed
		var currentLevel []string
		for name, degree := range inDegree {
			if degree == 0 && !visited[name] {
				currentLevel = append(currentLevel, name)
			}
		}

		// Cycle detection: no nodes with in-degree 0 but nodes remain
		// This indicates a cycle in the dependency graph
		if len(currentLevel) == 0 {
			// Collect remaining unvisited nodes for error message
			var remaining []string
			for name := range g.modules {
				if !visited[name] {
					remaining = append(remaining, name)
				}
			}
			// Build descriptive error message with cycle info
			cycleInfo := fmt.Sprintf("cycle detected in dependency graph involving modules: %v", remaining)
			// Add dependency chain information to help debugging
			for _, name := range remaining {
				deps := g.GetDeps(name)
				if len(deps) > 0 {
					cycleInfo += fmt.Sprintf("; %s depends on %v", name, deps)
				}
			}
			return nil, fmt.Errorf("%s", cycleInfo)
		}

		// Sort level for deterministic output
		// This ensures consistent ordering across runs
		// Critical for reproducible builds
		sort.Strings(currentLevel)

		// Mark current level as visited
		// These modules are now "processed" and ready
		for _, name := range currentLevel {
			visited[name] = true
		}

		// Reduce in-degree of neighbors
		// For each module processed at this level, inform its dependents
		// that one less dependency needs to be processed
		for _, name := range currentLevel {
			for _, dependent := range reverseEdges[name] {
				if !visited[dependent] {
					inDegree[dependent]--
				}
			}
		}

		// Add this level to the results
		// All modules in currentLevel can be executed in parallel
		levels = append(levels, currentLevel)
	}

	return levels, nil
}
