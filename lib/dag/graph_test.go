package dag

import (
	"reflect"
	"testing"
)

// MockModule implements module.Module interface for testing purposes.
// This struct provides a simple test double that satisfies the Module
// interface, allowing tests to create modules without implementing
// actual build logic. It stores a name and optional list of dependencies.
type MockModule struct {
	// name is the identifier for this module
	name string
	// deps stores the list of dependency names for this module
	deps []string
}

// Name returns the name of the mock module.
// Implements the module.Module interface.
func (m *MockModule) Name() string { return m.name }

// Type returns the module type identifier.
// Returns "mock" for all MockModule instances.
// Implements the module.Module interface.
func (m *MockModule) Type() string { return "mock" }

// Srcs returns the source files for this module.
// Returns nil as MockModule doesn't track sources.
// Implements the module.Module interface.
func (m *MockModule) Srcs() []string { return nil }

// Deps returns the list of dependencies for this module.
// Returns the stored deps slice which may be empty.
// Implements the module.Module interface.
func (m *MockModule) Deps() []string { return m.deps }

// Props returns module properties as a map.
// Returns nil as MockModule doesn't use properties.
// Implements the module.Module interface.
func (m *MockModule) Props() map[string]interface{} { return nil }

// GetProp retrieves a property value by key.
// Returns nil as MockModule doesn't store properties.
// Implements the module.Module interface.
func (m *MockModule) GetProp(key string) interface{} { return nil }

// TestNewGraph verifies that NewGraph creates an empty graph
// with nil modules and edges maps properly initialized.
func TestNewGraph(t *testing.T) {
	g := NewGraph()
	if g == nil {
		t.Fatal("NewGraph returned nil")
	}
	if len(g.Modules) != 0 {
		t.Error("New graph should have empty modules")
	}
}

// TestAddModule verifies that AddModule correctly adds a module to the graph.
// It creates a mock module and checks that it can be retrieved from the graph.
func TestAddModule(t *testing.T) {
	g := NewGraph()
	m := &MockModule{name: "A", deps: []string{"B"}}
	g.AddModule(m)

	if len(g.Modules) != 1 {
		t.Error("Module not added")
	}
}

// TestAddEdge verifies that AddEdge correctly establishes a dependency
// relationship between two modules. After adding edge "A" -> "B",
// A should have B as a dependency.
func TestAddEdge(t *testing.T) {
	g := NewGraph()
	g.AddEdge("A", "B")

	deps := g.GetDeps("A")
	if len(deps) != 1 || deps[0] != "B" {
		t.Errorf("Expected A to depend on B, got %v", deps)
	}
}

// TestGetDeps verifies that GetDeps returns the correct dependencies
// for a module. It tests both the case where a module has dependencies
// and the case where the module doesn't exist (should return empty slice).
func TestGetDeps(t *testing.T) {
	g := NewGraph()
	g.AddEdge("A", "B")
	g.AddEdge("A", "C")

	deps := g.GetDeps("A")
	if len(deps) != 2 {
		t.Errorf("Expected 2 deps, got %d", len(deps))
	}

	// Non-existent module returns empty slice
	deps = g.GetDeps("X")
	if len(deps) != 0 {
		t.Error("Non-existent module should return empty deps")
	}
}

// TestTopoSort verifies the topological sorting of a complex dependency graph.
// It creates a diamond-shaped dependency: A depends on B and C, both B and C depend on D.
// Expected result: [[D], [B, C], [A]] - modules grouped by levels for parallel execution.
func TestTopoSort(t *testing.T) {
	g := NewGraph()

	// A depends on B, C
	// B depends on D
	// C depends on D
	g.AddModule(&MockModule{name: "A"})
	g.AddModule(&MockModule{name: "B"})
	g.AddModule(&MockModule{name: "C"})
	g.AddModule(&MockModule{name: "D"})

	g.AddEdge("A", "B")
	g.AddEdge("A", "C")
	g.AddEdge("B", "D")
	g.AddEdge("C", "D")

	levels, err := g.TopoSort()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Expected: [[D], [B, C], [A]]
	if len(levels) != 3 {
		t.Fatalf("Expected 3 levels, got %d", len(levels))
	}

	// Level 0 should be D
	if len(levels[0]) != 1 || levels[0][0] != "D" {
		t.Errorf("Expected level 0 to be [D], got %v", levels[0])
	}

	// Level 1 should be [B, C]
	if len(levels[1]) != 2 {
		t.Errorf("Expected level 1 to have 2 nodes, got %v", levels[1])
	}

	// Level 2 should be A
	if len(levels[2]) != 1 || levels[2][0] != "A" {
		t.Errorf("Expected level 2 to be [A], got %v", levels[2])
	}
}

// TestCycleDetection verifies that TopoSort correctly detects cycles in the dependency graph.
// A cycle exists when modules depend on each other in a circular manner (e.g., A -> B -> A).
// The function should return an error when a cycle is detected since topological sorting
// is only possible for directed acyclic graphs (DAGs).
func TestCycleDetection(t *testing.T) {
	g := NewGraph()

	g.AddModule(&MockModule{name: "A"})
	g.AddModule(&MockModule{name: "B"})

	// A -> B -> A (cycle)
	g.AddEdge("A", "B")
	g.AddEdge("B", "A")

	_, err := g.TopoSort()
	if err == nil {
		t.Error("Expected cycle detection error")
	}
}

// TestTopoSortSelfDependency verifies that self-referencing dependencies (a module
// depending on itself) are detected as cycles. A self-cycle makes topological
// sorting impossible and should return an error.
func TestTopoSortSelfDependency(t *testing.T) {
	g := NewGraph()

	g.AddModule(&MockModule{name: "A"})

	// A -> A (cycle)
	g.AddEdge("A", "A")

	_, err := g.TopoSort()
	if err == nil {
		t.Error("Expected cycle detection error for self-dependency")
	}
}

// TestTopoSortMissingDependency verifies that TopoSort returns an error when a module
// depends on a non-existent module. This catches cases where a dependency is referenced
// but was never added to the graph as an actual module.
func TestTopoSortMissingDependency(t *testing.T) {
	g := NewGraph()

	g.AddModule(&MockModule{name: "A"})

	// A depends on B, but B is not a module
	g.AddEdge("A", "B")

	_, err := g.TopoSort()
	if err == nil {
		t.Error("Expected error for missing dependency")
	}
}

// TestTopoSortMissingSourceModule verifies that TopoSort returns an error when an edge
// is added from a module that was never added to the graph. This tests the validation
// that all source nodes in the dependency graph must be actual modules.
func TestTopoSortMissingSourceModule(t *testing.T) {
	g := NewGraph()
	g.AddModule(&MockModule{name: "B"})

	// The source node was never added as a module.
	g.AddEdge("A", "B")

	_, err := g.TopoSort()
	if err == nil {
		t.Fatal("Expected error for missing source module")
	}
}

// TestTopoSortLinearChain verifies topological sorting of a linear dependency chain
// where each module depends on exactly one other module (A -> B -> C -> D).
// Expected result: [[D], [C], [B], [A]] - each level contains exactly one module.
func TestTopoSortLinearChain(t *testing.T) {
	g := NewGraph()

	// A -> B -> C -> D
	g.AddModule(&MockModule{name: "A"})
	g.AddModule(&MockModule{name: "B"})
	g.AddModule(&MockModule{name: "C"})
	g.AddModule(&MockModule{name: "D"})

	g.AddEdge("A", "B")
	g.AddEdge("B", "C")
	g.AddEdge("C", "D")

	levels, err := g.TopoSort()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Expected: [[D], [C], [B], [A]]
	if len(levels) != 4 {
		t.Fatalf("Expected 4 levels, got %d", len(levels))
	}

	expected := [][]string{{"D"}, {"C"}, {"B"}, {"A"}}
	for i, exp := range expected {
		if len(levels[i]) != len(exp) || levels[i][0] != exp[0] {
			t.Errorf("Level %d expected %v, got %v", i, exp, levels[i])
		}
	}
}

// TestTopoSortIndependentModules verifies that modules with no dependencies

// are all placed at level 0 and sorted alphabetically. This tests the case

// where the graph contains only independent modules with no edges between them.

func TestTopoSortIndependentModules(t *testing.T) {

	g := NewGraph()

	// A, B, C have no dependencies

	g.AddModule(&MockModule{name: "A"})

	g.AddModule(&MockModule{name: "B"})

	g.AddModule(&MockModule{name: "C"})

	levels, err := g.TopoSort()

	if err != nil {

		t.Fatalf("Unexpected error: %v", err)

	}

	// All should be at level 0 (sorted alphabetically)

	if len(levels) != 1 {

		t.Fatalf("Expected 1 level, got %d", len(levels))

	}

	if len(levels[0]) != 3 {

		t.Errorf("Expected 3 modules at level 0, got %v", levels[0])

	}

	want := []string{"A", "B", "C"}

	if !reflect.DeepEqual(levels[0], want) {

		t.Fatalf("Expected sorted level %v, got %v", want, levels[0])

	}

}

// TestCycleDetectionErrorMessage verifies that cycle detection error message

// includes the remaining nodes in the cycle for better debugging.

func TestCycleDetectionErrorMessage(t *testing.T) {

	g := NewGraph()

	g.AddModule(&MockModule{name: "A"})

	g.AddModule(&MockModule{name: "B"})

	g.AddModule(&MockModule{name: "C"})

	// Create a cycle: A -> B -> C -> A

	g.AddEdge("A", "B")

	g.AddEdge("B", "C")

	g.AddEdge("C", "A")

	_, err := g.TopoSort()

	if err == nil {

		t.Fatal("Expected cycle detection error")

	}

	// Error message should mention remaining nodes

	errMsg := err.Error()

	// Check that error message contains all module names in the cycle

	hasA := false

	hasB := false

	hasC := false

	for _, ch := range errMsg {

		if ch == 'A' {

			hasA = true

		}

		if ch == 'B' {

			hasB = true

		}

		if ch == 'C' {

			hasC = true

		}

	}

	if !hasA || !hasB || !hasC {

		t.Errorf("Error message should mention cycle nodes A, B, C, got: %s", errMsg)

	}

	// Check that error message mentions "cycle"

	hasCycle := false

	for i := 0; i < len(errMsg)-4; i++ {

		if errMsg[i:i+5] == "cycle" {

			hasCycle = true

			break

		}

	}

	if !hasCycle {

		t.Errorf("Error message should mention 'cycle', got: %s", errMsg)

	}

}

// contains is a helper function to check if a string contains a substring

func contains(s, substr string) bool {

	return len(s) > 0 && len(substr) > 0 && (s[0] == substr[0] || s[len(s)-1] == substr[0])

}
