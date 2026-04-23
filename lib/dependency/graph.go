// Package dependency provides advanced dependency management features including
// transitive dependency resolution, conflict detection, and dependency graph
// visualization.
package dependency

import (
	"fmt"
	"sort"
	"strings"
)

// Dependency represents a module dependency with name, version, and optional status.
// Name is the unique identifier of the dependency module.
// Version specifies the version constraint or exact version.
// Optional indicates whether this dependency is required or optional.
type Dependency struct {
	Name     string
	Version  string
	Optional bool
}

// DependencyGraph represents the complete dependency graph for a build system.
// It maintains mappings of modules to their direct dependencies and dependents,
// enabling transitive dependency resolution, conflict detection, and topological ordering.
//
// The graph uses two edge representations:
//   - edges: forward mapping (module -> its dependencies) for dependency resolution
//   - reverseEdges: reverse mapping (dependency -> modules that depend on it) for dependent lookup
//
// Fields:
//   - modules: Map of module name to ModuleNode containing module metadata and dependencies
//   - edges: Map of module name to list of its direct dependency names (forward edges)
//   - reverseEdges: Map of module name to list of modules that depend on it (reverse edges)
type DependencyGraph struct {
	modules      map[string]*ModuleNode
	edges        map[string][]string // module -> dependencies
	reverseEdges map[string][]string // module -> dependents
}

// ModuleNode represents a node in the dependency graph corresponding to a single module.
//
// Each node tracks both direct and transitive dependencies:
//   - DirectDeps: dependencies explicitly declared in the module
//   - AllDeps: all transitive dependencies (computed by ResolveDependencies)
//
// Fields:
//   - Name: Unique identifier of the module
//   - Type: Module type (e.g., "cc_library", "java_library")
//   - DirectDeps: Slice of direct dependencies declared by this module
//   - AllDeps: Slice of all transitive dependencies (computed by ResolveDependencies)
//   - Dependents: List of module names that depend on this module
//   - IsRoot: True if this is a root module (directly buildable), false if it's only a dependency
type ModuleNode struct {
	Name       string
	Type       string
	DirectDeps []Dependency
	AllDeps    []Dependency // Transitive dependencies
	Dependents []string     // Modules that depend on this module
	IsRoot     bool         // True if this is a root module (not a dependency)
}

// Conflict represents a dependency version conflict detected during resolution.
//
// A conflict occurs when a dependency is required at different versions
// by modules in different parts of the dependency tree.
//
// Fields:
//   - Module: The module where the conflict was detected
//   - DepName: The dependency name that has conflicting versions
//   - Version1: First version required
//   - Version2: Second (different) version required
//   - Path1: List of modules requiring Version1
//   - Path2: List of modules requiring Version2
type Conflict struct {
	Module   string
	DepName  string
	Version1 string
	Version2 string
	Path1    []string
	Path2    []string
}

// Resolution represents the result of dependency resolution.
//
// The resolution process:
//  1. Calculates all transitive dependencies for each module
//  2. Detects version conflicts between different dependency paths
//  3. Computes a valid build order via topological sort
//
// Fields:
//   - Success: True if resolution succeeded without conflicts
//   - Conflicts: Slice of detected version conflicts
//   - Order: Topological ordering of modules for build
type Resolution struct {
	Success   bool
	Conflicts []Conflict
	Order     []string // Topological order
}

// NewDependencyGraph creates a new empty dependency graph.
//
// Returns a pointer to a newly initialized DependencyGraph with empty maps.
// The graph starts with no modules and must be populated using AddModule
// before any resolution or query operations can be performed.
//
// Returns:
//   - *DependencyGraph: A new empty dependency graph instance
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
// Parameters:
//   - name: Unique identifier for the module. Must not already exist in the graph.
//   - moduleType: Type of the module (e.g., "cc_library", "java_library")
//   - deps: Slice of Dependency objects representing direct dependencies
//
// Returns: No return value
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
	if _, exists := g.modules[name]; exists {
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
//  1. Calculates transitive dependencies for all modules
//  2. Detects version conflicts between different module paths
//  3. Computes topological ordering for build
//
// The function processes the entire graph: for each module, it traverses
// the dependency tree to collect all transitive dependencies. Then it
// analyzes whether any dependency is required at multiple different versions.
//
// Returns:
//   - *Resolution: Resolution result containing success status, conflicts, and build order
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
// The algorithm:
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
		if visited[name] {
			return
		}
		visited[name] = true

		// Add all direct dependencies of this module.
		if node, exists := g.modules[name]; exists {
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
		if !seen[dep.Name] {
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
			if _, exists := requiredVersions[dep.Name]; !exists {
				requiredVersions[dep.Name] = make(map[string][]string)
			}
			requiredVersions[dep.Name][dep.Version] = append(requiredVersions[dep.Name][dep.Version], moduleName)
		}
	}

	// Scan for conflicts: dependencies required at multiple versions.
	for depName, versions := range requiredVersions {
		// Multiple versions requested = conflict.
		if len(versions) > 1 {
			conflict := Conflict{
				DepName:  depName,
				Version1: "",
				Version2: "",
			}
			// Record first two conflicting versions and their paths.
			// Additional versions beyond the first two are not recorded.
			for version, modules := range versions {
				if conflict.Version1 == "" {
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
// In-degree for a module = number of direct dependencies it has.
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
	inDegree := make(map[string]int)

	for name := range g.modules {

		inDegree[name] = 0

	}

	// Step 2: Calculate in-degrees (dependency count).
	// A module's in-degree = number of direct dependencies it has.
	// This is simply the length of its edges list.
	for name := range g.modules {

		inDegree[name] = len(g.edges[name])

	}

	// Step 3: Initialize queue with nodes having in-degree 0.
	// These modules have no dependencies and can be built first.
	queue := []string{}

	for name, degree := range inDegree {

		if degree == 0 {

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
	for len(queue) > 0 {

		// Dequeue first element (FIFO).
		node := queue[0]

		queue = queue[1:]

		// Add to result and mark processed.
		result = append(result, node)

		processed[node] = true

		// Find modules that depend on this node.
		// A module can be added to queue when all its dependencies are processed.
		for moduleName, deps := range g.edges {

			// Skip already processed modules.
			if processed[moduleName] {

				continue

			}

			// Check if this module depends on the current node.
			hasDep := false

			for _, dep := range deps {

				if dep == node {

					hasDep = true

					break

				}

			}

			if hasDep {

				// Check if all dependencies are now processed.
				// A module is ready when every dependency appears in processed.
				allProcessed := true

				for _, dep := range g.edges[moduleName] {

					if !processed[dep] && dep != node {

						allProcessed = false

						break

					}

				}

				// Add to queue if ready and not already queued.
				if allProcessed {

					// Check if already in queue to avoid duplicates.
					found := false

					for _, q := range queue {

						if q == moduleName {

							found = true

							break

						}

					}

					if !found {

						queue = append(queue, moduleName)

						// Re-sort to maintain alphabetical ordering.
						sort.Strings(queue)

					}

				}

			}

		}

	}

	// Step 5: Check for circular dependencies.
	// If not all modules were processed, there's a cycle.
	if len(result) != len(g.modules) {

		return nil, fmt.Errorf("circular dependency detected")

	}

	return result, nil

}

// GetDependents returns all modules that directly depend on the given module.
//
// This function looks up the reverse edges mapping to find all modules that have
// the specified module as a direct dependency.
//
// The reverseEdges map is maintained by AddModule whenever a dependency is added.
// It's efficient for answering "what depends on X?" queries.
//
// Parameters:
//   - moduleName: The name of the module to find dependents for
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
// the dependencies explicitly declared in the module.
//
// Parameters:
//   - moduleName: The name of the module to get dependencies for
//
// Returns:
//   - []string: List of direct dependency names; may be empty
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
//	 -> dependency_name [version]
//	 -> dependency_name [version]
//	 (no dependencies)
//
// Each module entry is separated by a blank line.
// The output is not guaranteed to be in any particular order.
func (g *DependencyGraph) Visualize() string {
	var sb strings.Builder
	sb.WriteString("Dependency Graph:\n")
	sb.WriteString(strings.Repeat("-", 40) + "\n")

	for name, node := range g.modules {
		sb.WriteString(fmt.Sprintf("%s (%s)\n", name, node.Type))
		if len(node.DirectDeps) > 0 {
			for _, dep := range node.DirectDeps {
				sb.WriteString(fmt.Sprintf(" -> %s [%s]\n", dep.Name, dep.Version))
			}
		} else {
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
