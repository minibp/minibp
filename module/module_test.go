package module

import (
	"reflect"
	"strconv"
	"sync"
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
	resetRegistry()

	f := &MockFactory{}

	Register("mock_type", f)

	if registryLen() != 1 {
		t.Fatalf("Expected 1 registered type, got %d", registryLen())
	}

	factory := Lookup("mock_type")
	if factory == nil {
		t.Fatal("Expected to find factory for 'mock_type'")
	}
}

// TestRegistryLookupUnknown tests looking up unregistered types
func TestRegistryLookupUnknown(t *testing.T) {
	resetRegistry()

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
	resetRegistry()

	Register("type_a", &TestFactoryA{})
	Register("type_b", &TestFactoryB{})

	if registryLen() != 2 {
		t.Fatalf("Expected 2 registered types, got %d", registryLen())
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
	resetRegistry()

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
	resetRegistry()

	ast := &parser.Module{Type: "unknown_type"}
	_, err := Create(ast, nil)
	if err == nil {
		t.Error("Expected error for unknown module type")
	}
}

func TestCreateModulePreservesDependencyFields(t *testing.T) {
	resetRegistry()
	Register("cc_binary", &CCBinaryFactory{})

	ast := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "deps", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: ":static"}}}},
			{Name: "shared_libs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: ":shared"}}}},
			{Name: "header_libs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: ":headers"}}}},
		}},
	}

	m, err := Create(ast, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	want := []string{":static", ":shared", ":headers"}
	if !reflect.DeepEqual(m.Deps(), want) {
		t.Fatalf("Expected deps %v, got %v", want, m.Deps())
	}
}

func TestCreateModulePreservesStructuredProps(t *testing.T) {
	resetRegistry()
	Register("cc_binary", &CCBinaryFactory{})

	ast := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "config", Value: &parser.Map{Properties: []*parser.Property{
				{Name: "enabled", Value: &parser.Bool{Value: true}},
				{Name: "level", Value: &parser.Int64{Value: 2}},
			}}},
			{Name: "features", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "fast"},
				&parser.Int64{Value: 7},
				&parser.Bool{Value: true},
			}}},
		}},
	}

	m, err := Create(ast, nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	config, ok := m.GetProp("config").(map[string]interface{})
	if !ok {
		t.Fatalf("Expected config map, got %T", m.GetProp("config"))
	}
	if config["enabled"] != true || config["level"] != int64(2) {
		t.Fatalf("Unexpected config contents: %v", config)
	}

	features, ok := m.GetProp("features").([]interface{})
	if !ok {
		t.Fatalf("Expected features []interface{}, got %T", m.GetProp("features"))
	}
	want := []interface{}{"fast", int64(7), true}
	if !reflect.DeepEqual(features, want) {
		t.Fatalf("Expected features %v, got %v", want, features)
	}
}

func TestCoreSupportedModuleTypesAreRegistered(t *testing.T) {
	snapshot := registrySnapshot()
	defer restoreRegistry(snapshot)

	resetRegistry()
	registerBuiltInModuleTypes()

	tests := []struct {
		moduleType string
		wantType   string
	}{
		{moduleType: "cc_library", wantType: "cc_library"},
		{moduleType: "cc_library_static", wantType: "cc_library_static"},
		{moduleType: "cc_library_shared", wantType: "cc_library_shared"},
		{moduleType: "cc_object", wantType: "cc_object"},
		{moduleType: "cc_binary", wantType: "cc_binary"},
		{moduleType: "cpp_library", wantType: "cpp_library"},
		{moduleType: "cpp_binary", wantType: "cpp_binary"},
		{moduleType: "go_library", wantType: "go_library"},
		{moduleType: "go_binary", wantType: "go_binary"},
		{moduleType: "go_test", wantType: "go_test"},
		{moduleType: "java_library", wantType: "java_library"},
		{moduleType: "java_library_static", wantType: "java_library_static"},
		{moduleType: "java_library_host", wantType: "java_library_host"},
		{moduleType: "java_binary", wantType: "java_binary"},
		{moduleType: "java_binary_host", wantType: "java_binary_host"},
		{moduleType: "java_test", wantType: "java_test"},
		{moduleType: "java_import", wantType: "java_import"},
		{moduleType: "filegroup", wantType: "filegroup"},
		{moduleType: "custom", wantType: "custom"},
	}

	for _, tc := range tests {
		t.Run(tc.moduleType, func(t *testing.T) {
			if Lookup(tc.moduleType) == nil {
				t.Fatalf("Expected factory for %q", tc.moduleType)
			}

			m, err := Create(&parser.Module{
				Type: tc.moduleType,
				Map: &parser.Map{Properties: []*parser.Property{{
					Name:  "name",
					Value: &parser.String{Value: "mod"},
				}}},
			}, nil)
			if err != nil {
				t.Fatalf("Create failed for %q: %v", tc.moduleType, err)
			}
			if m.Type() != tc.wantType {
				t.Fatalf("Expected created module type %q, got %q", tc.wantType, m.Type())
			}
		})
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	snapshot := registrySnapshot()
	defer restoreRegistry(snapshot)

	resetRegistry()

	const workers = 32

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			name := "type_" + strconv.Itoa(i)
			Register(name, &MockFactory{})

			if Lookup(name) == nil {
				t.Errorf("Expected to find factory for %q", name)
			}
		}(i)
	}

	wg.Wait()

	if registryLen() != workers {
		t.Fatalf("Expected %d registered types, got %d", workers, registryLen())
	}

	for i := 0; i < workers; i++ {
		name := "type_" + strconv.Itoa(i)
		if Lookup(name) == nil {
			t.Fatalf("Expected to find factory for %q", name)
		}
	}
}
