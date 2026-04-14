package dag

import (
	"reflect"
	"testing"
)

// MockModule implements module.Module for testing
type MockModule struct {
	name string
	deps []string
}

func (m *MockModule) Name() string                   { return m.name }
func (m *MockModule) Type() string                   { return "mock" }
func (m *MockModule) Srcs() []string                 { return nil }
func (m *MockModule) Deps() []string                 { return m.deps }
func (m *MockModule) Props() map[string]interface{}  { return nil }
func (m *MockModule) GetProp(key string) interface{} { return nil }

func TestNewGraph(t *testing.T) {
	g := NewGraph()
	if g == nil {
		t.Fatal("NewGraph returned nil")
	}
	if len(g.modules) != 0 {
		t.Error("New graph should have empty modules")
	}
}

func TestAddModule(t *testing.T) {
	g := NewGraph()
	m := &MockModule{name: "A", deps: []string{"B"}}
	g.AddModule(m)

	if len(g.modules) != 1 {
		t.Error("Module not added")
	}
}

func TestAddEdge(t *testing.T) {
	g := NewGraph()
	g.AddEdge("A", "B")

	deps := g.GetDeps("A")
	if len(deps) != 1 || deps[0] != "B" {
		t.Errorf("Expected A to depend on B, got %v", deps)
	}
}

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
