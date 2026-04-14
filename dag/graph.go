// dag/graph.go - DAG dependency graph for modules
package dag

import (
	"fmt"
	"sort"

	"minibp/module"
)

// Graph represents a directed acyclic graph of module dependencies
type Graph struct {
	modules map[string]module.Module
	edges   map[string][]string // module name -> list of dependency names
}

// NewGraph creates a new empty dependency graph
func NewGraph() *Graph {
	return &Graph{
		modules: make(map[string]module.Module),
		edges:   make(map[string][]string),
	}
}

// AddModule adds a module to the graph
func (g *Graph) AddModule(m module.Module) {
	if m != nil {
		g.modules[m.Name()] = m
		// Initialize edges slice if not exists
		if _, exists := g.edges[m.Name()]; !exists {
			g.edges[m.Name()] = []string{}
		}
	}
}

// AddEdge adds a dependency edge from 'from' to 'to' (from depends on to)
func (g *Graph) AddEdge(from, to string) {
	// Ensure both modules exist in edges map
	if _, exists := g.edges[from]; !exists {
		g.edges[from] = []string{}
	}
	if _, exists := g.edges[to]; !exists {
		g.edges[to] = []string{}
	}
	g.edges[from] = append(g.edges[from], to)
}

// GetDeps returns the direct dependencies of a module
func (g *Graph) GetDeps(name string) []string {
	if deps, exists := g.edges[name]; exists {
		// Return a copy to prevent external modification
		result := make([]string, len(deps))
		copy(result, deps)
		return result
	}
	return []string{}
}

// TopoSort returns modules in topological order, grouped by levels for parallel execution
func (g *Graph) TopoSort() ([][]string, error) {
	// Calculate in-degree for each node (number of dependencies)
	inDegree := make(map[string]int)
	for name := range g.modules {
		inDegree[name] = 0
	}

	// Validate dependencies and count in-degrees
	for from, deps := range g.edges {
		if _, exists := g.modules[from]; !exists {
			return nil, fmt.Errorf("module '%s' referenced in dependency graph does not exist", from)
		}
		for _, to := range deps {
			if _, exists := g.modules[to]; !exists {
				return nil, fmt.Errorf("dependency '%s' of module '%s' does not exist", to, from)
			}
			// "from" depends on "to", so "from" has an incoming edge
			inDegree[from]++
		}
	}

	// Build reverse edges: dependency -> list of dependents that need it
	// When a dependency is processed, we notify its dependents
	reverseEdges := make(map[string][]string)
	for from, deps := range g.edges {
		for _, to := range deps {
			reverseEdges[to] = append(reverseEdges[to], from)
		}
	}

	var levels [][]string
	visited := make(map[string]bool)
	nodeCount := len(g.modules)

	for len(visited) < nodeCount {
		// Find all nodes with in-degree 0 that haven't been visited
		var currentLevel []string
		for name, degree := range inDegree {
			if degree == 0 && !visited[name] {
				currentLevel = append(currentLevel, name)
			}
		}

		// If no nodes with in-degree 0 found but not all visited, there's a cycle
		if len(currentLevel) == 0 {
			// Identify nodes in the cycle for error message
			var remaining []string
			for name := range g.modules {
				if !visited[name] {
					remaining = append(remaining, name)
				}
			}
			return nil, fmt.Errorf("cycle detected in dependency graph, remaining nodes: %v", remaining)
		}

		// Sort level for deterministic output
		sort.Strings(currentLevel)

		// Mark current level as visited
		for _, name := range currentLevel {
			visited[name] = true
		}

		// Reduce in-degree of neighbors
		for _, name := range currentLevel {
			for _, dependent := range reverseEdges[name] {
				if !visited[dependent] {
					inDegree[dependent]--
				}
			}
		}

		levels = append(levels, currentLevel)
	}

	return levels, nil
}
