package module

import (
	"testing"

	"minibp/parser"
)

// MockFactory implements Factory interface for testing
type MockFactory struct{}

func (m *MockFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	return &BaseModule{
		Name_: getStringFromAST(ast, "name"),
		Type_: ast.Type,
	}, nil
}

func getStringFromAST(ast *parser.Module, name string) string {
	if ast.Map == nil {
		return ""
	}
	for _, prop := range ast.Map.Properties {
		if prop.Name == name {
			if s, ok := prop.Value.(*parser.String); ok {
				return s.Value
			}
		}
	}
	return ""
}

// TestBaseModuleCreation tests creating a BaseModule
func TestBaseModuleCreation(t *testing.T) {
	m := &BaseModule{
		Name_:  "test",
		Type_:  "cc_binary",
		Srcs_:  []string{"main.c"},
		Deps_:  []string{"lib1"},
		Props_: map[string]interface{}{"key": "value"},
	}

	if m.Name() != "test" {
		t.Errorf("Expected Name() to return 'test', got '%s'", m.Name())
	}
	if m.Type() != "cc_binary" {
		t.Errorf("Expected Type() to return 'cc_binary', got '%s'", m.Type())
	}
	if len(m.Srcs()) != 1 || m.Srcs()[0] != "main.c" {
		t.Errorf("Expected Srcs() to return ['main.c'], got %v", m.Srcs())
	}
	if len(m.Deps()) != 1 || m.Deps()[0] != "lib1" {
		t.Errorf("Expected Deps() to return ['lib1'], got %v", m.Deps())
	}
	if m.GetProp("key") != "value" {
		t.Errorf("Expected GetProp('key') to return 'value', got %v", m.GetProp("key"))
	}
}

// TestRegistryRegister tests registering factories
func TestRegistryRegister(t *testing.T) {
	// Clear registry before test
	Registry = make(map[string]Factory)

	f := &MockFactory{}

	Register("mock_type", f)

	if len(Registry) != 1 {
		t.Fatalf("Expected 1 registered type, got %d", len(Registry))
	}

	factory := Lookup("mock_type")
	if factory == nil {
		t.Fatal("Expected to find factory for 'mock_type'")
	}
}

// TestRegistryLookupUnknown tests looking up unregistered types
func TestRegistryLookupUnknown(t *testing.T) {
	// Clear registry
	Registry = make(map[string]Factory)

	factory := Lookup("unknown_type")
	if factory != nil {
		t.Error("Expected nil for unregistered type")
	}
}

// FactoryA and FactoryB implement Factory for testing
type TestFactoryA struct{}
type TestFactoryB struct{}

func (f *TestFactoryA) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	return &BaseModule{Name_: "A", Type_: "type_a"}, nil
}

func (f *TestFactoryB) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	return &BaseModule{Name_: "B", Type_: "type_b"}, nil
}

// TestRegistryMultipleTypes tests registering multiple factories
func TestRegistryMultipleTypes(t *testing.T) {
	// Clear registry
	Registry = make(map[string]Factory)

	Register("type_a", &TestFactoryA{})
	Register("type_b", &TestFactoryB{})

	if len(Registry) != 2 {
		t.Fatalf("Expected 2 registered types, got %d", len(Registry))
	}

	if Lookup("type_a") == nil {
		t.Error("Expected to find factory for 'type_a'")
	}
	if Lookup("type_b") == nil {
		t.Error("Expected to find factory for 'type_b'")
	}
}

func getListFromAST(ast *parser.Module, name string) []string {
	if ast.Map == nil {
		return nil
	}
	for _, prop := range ast.Map.Properties {
		if prop.Name == name {
			if l, ok := prop.Value.(*parser.List); ok {
				var result []string
				for _, v := range l.Values {
					if s, ok := v.(*parser.String); ok {
						result = append(result, s.Value)
					}
				}
				return result
			}
		}
	}
	return nil
}

// TestCreateModuleFromAST tests creating a module from AST
func TestCreateModuleFromAST(t *testing.T) {
	// Clear registry
	Registry = make(map[string]Factory)

	// Register cc_binary factory
	Register("cc_binary", &CCBinaryFactory{})

	// Create AST for cc_binary module
	ast := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{
					Name:  "name",
					Value: &parser.String{Value: "myapp"},
				},
				{
					Name: "srcs",
					Value: &parser.List{
						Values: []parser.Expression{
							&parser.String{Value: "main.c"},
							&parser.String{Value: "util.c"},
						},
					},
				},
				{
					Name: "deps",
					Value: &parser.List{
						Values: []parser.Expression{
							&parser.String{Value: ":lib1"},
						},
					},
				},
			},
		},
	}

	module, err := Create(ast, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if module.Name() != "myapp" {
		t.Errorf("Expected name 'myapp', got '%s'", module.Name())
	}
	if module.Type() != "cc_binary" {
		t.Errorf("Expected type 'cc_binary', got '%s'", module.Type())
	}
	if len(module.Srcs()) != 2 {
		t.Errorf("Expected 2 sources, got %d", len(module.Srcs()))
	}
	if len(module.Deps()) != 1 || module.Deps()[0] != ":lib1" {
		t.Errorf("Expected deps [':lib1'], got %v", module.Deps())
	}
}

// TestCreateUnknownType tests creating module with unknown type
func TestCreateUnknownType(t *testing.T) {
	// Clear registry
	Registry = make(map[string]Factory)

	ast := &parser.Module{Type: "unknown_type"}
	_, err := Create(ast, nil)
	if err == nil {
		t.Error("Expected error for unknown module type")
	}
}
