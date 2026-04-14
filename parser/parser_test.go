package parser

import (
	"strings"
	"testing"
)

// TestParseSimpleModule tests parsing a simple cc_binary module
func TestParseSimpleModule(t *testing.T) {
	input := `cc_binary {
    name: "hello",
    srcs: ["main.c"]
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	if len(file.Defs) != 1 {
		t.Fatalf("Expected 1 definition, got %d", len(file.Defs))
	}

	module, ok := file.Defs[0].(*Module)
	if !ok {
		t.Fatalf("Expected *Module, got %T", file.Defs[0])
	}

	if module.Type != "cc_binary" {
		t.Errorf("Expected module type 'cc_binary', got '%s'", module.Type)
	}

	// Check name property
	nameProp := findProperty(module.Map, "name")
	if nameProp == nil {
		t.Fatal("Missing 'name' property")
	}
	nameStr, ok := nameProp.Value.(*String)
	if !ok {
		t.Fatalf("Expected name to be *String, got %T", nameProp.Value)
	}
	if nameStr.Value != "hello" {
		t.Errorf("Expected name 'hello', got '%s'", nameStr.Value)
	}

	// Check srcs property
	srcsProp := findProperty(module.Map, "srcs")
	if srcsProp == nil {
		t.Fatal("Missing 'srcs' property")
	}
	srcsList, ok := srcsProp.Value.(*List)
	if !ok {
		t.Fatalf("Expected srcs to be *List, got %T", srcsProp.Value)
	}
	if len(srcsList.Values) != 1 {
		t.Fatalf("Expected 1 src, got %d", len(srcsList.Values))
	}
	srcStr, ok := srcsList.Values[0].(*String)
	if !ok {
		t.Fatalf("Expected src to be *String, got %T", srcsList.Values[0])
	}
	if srcStr.Value != "main.c" {
		t.Errorf("Expected src 'main.c', got '%s'", srcStr.Value)
	}
}

// TestParseWithDeps tests parsing a module with dependencies
func TestParseWithDeps(t *testing.T) {
	input := `cc_binary {
    name: "hello",
    srcs: ["main.c"],
    deps: [":lib"]
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	module, ok := file.Defs[0].(*Module)
	if !ok {
		t.Fatalf("Expected *Module, got %T", file.Defs[0])
	}

	// Check deps property
	depsProp := findProperty(module.Map, "deps")
	if depsProp == nil {
		t.Fatal("Missing 'deps' property")
	}
	depsList, ok := depsProp.Value.(*List)
	if !ok {
		t.Fatalf("Expected deps to be *List, got %T", depsProp.Value)
	}
	if len(depsList.Values) != 1 {
		t.Fatalf("Expected 1 dep, got %d", len(depsList.Values))
	}
	depStr, ok := depsList.Values[0].(*String)
	if !ok {
		t.Fatalf("Expected dep to be *String, got %T", depsList.Values[0])
	}
	if depStr.Value != ":lib" {
		t.Errorf("Expected dep ':lib', got '%s'", depStr.Value)
	}
}

// TestParseAssignment tests parsing a variable assignment
func TestParseAssignment(t *testing.T) {
	input := `foo = "bar"`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	if len(file.Defs) != 1 {
		t.Fatalf("Expected 1 definition, got %d", len(file.Defs))
	}

	assign, ok := file.Defs[0].(*Assignment)
	if !ok {
		t.Fatalf("Expected *Assignment, got %T", file.Defs[0])
	}

	if assign.Name != "foo" {
		t.Errorf("Expected variable name 'foo', got '%s'", assign.Name)
	}

	if assign.Assigner != "=" {
		t.Errorf("Expected assigner '=', got '%s'", assign.Assigner)
	}

	str, ok := assign.Value.(*String)
	if !ok {
		t.Fatalf("Expected value to be *String, got %T", assign.Value)
	}
	if str.Value != "bar" {
		t.Errorf("Expected value 'bar', got '%s'", str.Value)
	}
}

// TestParseList tests parsing a list expression
func TestParseList(t *testing.T) {
	input := `my_list = ["a", "b", "c"]`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	assign, ok := file.Defs[0].(*Assignment)
	if !ok {
		t.Fatalf("Expected *Assignment, got %T", file.Defs[0])
	}

	list, ok := assign.Value.(*List)
	if !ok {
		t.Fatalf("Expected value to be *List, got %T", assign.Value)
	}

	if len(list.Values) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(list.Values))
	}

	expected := []string{"a", "b", "c"}
	for i, exp := range expected {
		str, ok := list.Values[i].(*String)
		if !ok {
			t.Fatalf("Expected item %d to be *String, got %T", i, list.Values[i])
		}
		if str.Value != exp {
			t.Errorf("Expected item %d to be '%s', got '%s'", i, exp, str.Value)
		}
	}
}

// TestParseMultipleModules tests parsing multiple modules
func TestParseMultipleModules(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    srcs: ["foo.c"]
}

cc_binary {
    name: "app",
    srcs: ["main.c"],
    deps: [":libfoo"]
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	if len(file.Defs) != 2 {
		t.Fatalf("Expected 2 definitions, got %d", len(file.Defs))
	}

	// Check first module (cc_library)
	mod1, ok := file.Defs[0].(*Module)
	if !ok {
		t.Fatalf("Expected first def to be *Module, got %T", file.Defs[0])
	}
	if mod1.Type != "cc_library" {
		t.Errorf("Expected first module type 'cc_library', got '%s'", mod1.Type)
	}

	// Check second module (cc_binary)
	mod2, ok := file.Defs[1].(*Module)
	if !ok {
		t.Fatalf("Expected second def to be *Module, got %T", file.Defs[1])
	}
	if mod2.Type != "cc_binary" {
		t.Errorf("Expected second module type 'cc_binary', got '%s'", mod2.Type)
	}
}

// TestParseComments tests that comments are skipped
func TestParseComments(t *testing.T) {
	input := `// This is a comment
cc_binary {
    name: "hello",  // inline comment
    srcs: ["main.c"]
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	if len(file.Defs) != 1 {
		t.Fatalf("Expected 1 definition, got %d", len(file.Defs))
	}
}

// TestParseInteger tests parsing integer values
func TestParseInteger(t *testing.T) {
	input := `cc_binary {
    name: "test",
    optimization_level: 2
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	module := file.Defs[0].(*Module)
	prop := findProperty(module.Map, "optimization_level")
	if prop == nil {
		t.Fatal("Missing 'optimization_level' property")
	}

	intVal, ok := prop.Value.(*Int64)
	if !ok {
		t.Fatalf("Expected *Int64, got %T", prop.Value)
	}
	if intVal.Value != 2 {
		t.Errorf("Expected value 2, got %d", intVal.Value)
	}
}

// TestParseBoolean tests parsing boolean values
func TestParseBoolean(t *testing.T) {
	input := `cc_binary {
    name: "test",
    static: true
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	module := file.Defs[0].(*Module)
	prop := findProperty(module.Map, "static")
	if prop == nil {
		t.Fatal("Missing 'static' property")
	}

	boolVal, ok := prop.Value.(*Bool)
	if !ok {
		t.Fatalf("Expected *Bool, got %T", prop.Value)
	}
	if !boolVal.Value {
		t.Errorf("Expected value true, got false")
	}
}

// TestParseNestedMap tests parsing nested maps
func TestParseNestedMap(t *testing.T) {
	input := `cc_binary {
    name: "test",
    config: {
        debug: true
    }
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	module := file.Defs[0].(*Module)
	prop := findProperty(module.Map, "config")
	if prop == nil {
		t.Fatal("Missing 'config' property")
	}

	configMap, ok := prop.Value.(*Map)
	if !ok {
		t.Fatalf("Expected *Map, got %T", prop.Value)
	}

	if len(configMap.Properties) != 1 {
		t.Errorf("Expected 1 property in nested map, got %d", len(configMap.Properties))
	}
}

// TestParseEmptyModule tests parsing an empty module
func TestParseEmptyModule(t *testing.T) {
	input := `cc_binary {}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	if len(file.Defs) != 1 {
		t.Fatalf("Expected 1 definition, got %d", len(file.Defs))
	}

	module := file.Defs[0].(*Module)
	if len(module.Map.Properties) != 0 {
		t.Errorf("Expected 0 properties, got %d", len(module.Map.Properties))
	}
}

// TestParseEmptyList tests parsing an empty list
func TestParseEmptyList(t *testing.T) {
	input := `cc_binary {
    name: "test",
    srcs: []
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	module := file.Defs[0].(*Module)
	prop := findProperty(module.Map, "srcs")
	if prop == nil {
		t.Fatal("Missing 'srcs' property")
	}

	list, ok := prop.Value.(*List)
	if !ok {
		t.Fatalf("Expected *List, got %T", prop.Value)
	}

	if len(list.Values) != 0 {
		t.Errorf("Expected 0 items, got %d", len(list.Values))
	}
}

// TestParseError tests error handling for invalid input
func TestParseError(t *testing.T) {
	input := `cc_binary`

	p := NewParser(strings.NewReader(input), "test.bp")
	_, errs := p.Parse()

	if len(errs) == 0 {
		t.Fatal("Expected parse error for incomplete module")
	}
}

// TestParseStringEscapes tests parsing strings with escape sequences
func TestParseStringEscapes(t *testing.T) {
	input := `cc_binary {
    name: "hello\tworld",
    srcs: ["main.c"]
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	module := file.Defs[0].(*Module)
	prop := findProperty(module.Map, "name")
	str := prop.Value.(*String)
	if str.Value != "hello\tworld" {
		t.Errorf("Expected 'hello\\tworld', got '%s'", str.Value)
	}
}

func TestParseRawString(t *testing.T) {
	input := "cc_binary {\n    name: `hello\\nworld`,\n    srcs: [\"main.c\"],\n}"

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	module := file.Defs[0].(*Module)
	prop := findProperty(module.Map, "name")
	str := prop.Value.(*String)
	if str.Value != "hello\\nworld" {
		t.Errorf("Expected raw string value 'hello\\\\nworld', got %q", str.Value)
	}
}

func TestParseListTrailingComma(t *testing.T) {
	input := `my_list = ["a", "b",]`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	assign, ok := file.Defs[0].(*Assignment)
	if !ok {
		t.Fatalf("Expected *Assignment, got %T", file.Defs[0])
	}

	list, ok := assign.Value.(*List)
	if !ok {
		t.Fatalf("Expected value to be *List, got %T", assign.Value)
	}

	if len(list.Values) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(list.Values))
	}
}

func TestParseListMissingComma(t *testing.T) {
	input := `my_list = ["a" "b"]`

	p := NewParser(strings.NewReader(input), "test.bp")
	_, errs := p.Parse()

	if len(errs) == 0 {
		t.Fatal("Expected parse error for missing list comma")
	}
}

func TestParseModulePropertiesMissingComma(t *testing.T) {
	input := `cc_binary {
    name: "hello"
    srcs: ["main.c"],
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	_, errs := p.Parse()

	if len(errs) == 0 {
		t.Fatal("Expected parse error for missing property comma")
	}
}

func TestParseNestedMapPropertiesMissingComma(t *testing.T) {
	input := `cc_binary {
    name: "hello",
    config: {
        debug: true
        level: 2,
    },
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	_, errs := p.Parse()

	if len(errs) == 0 {
		t.Fatal("Expected parse error for missing nested map property comma")
	}
}

// Helper function to find a property by name
func findProperty(m *Map, name string) *Property {
	for _, prop := range m.Properties {
		if prop.Name == name {
			return prop
		}
	}
	return nil
}

// TestParseAssignmentWithPlusEqual tests += assignment
func TestParseAssignmentWithPlusEqual(t *testing.T) {
	input := `foo += "bar"`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	assign, ok := file.Defs[0].(*Assignment)
	if !ok {
		t.Fatalf("Expected *Assignment, got %T", file.Defs[0])
	}

	if assign.Assigner != "+=" {
		t.Errorf("Expected assigner '+=', got '%s'", assign.Assigner)
	}
}

func TestParseArchBlock(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    srcs: ["foo.c"],
    arch: {
        arm: {
            srcs: ["foo_arm.S"],
            cflags: ["-DARM"],
        },
        arm64: {
            cflags: ["-DARM64"],
        },
    },
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	if len(file.Defs) != 1 {
		t.Fatalf("Expected 1 definition, got %d", len(file.Defs))
	}

	mod := file.Defs[0].(*Module)
	if mod.Arch == nil {
		t.Fatal("Expected module.Arch to be non-nil")
	}

	if len(mod.Arch) != 2 {
		t.Fatalf("Expected 2 arch entries, got %d", len(mod.Arch))
	}

	armProps, ok := mod.Arch["arm"]
	if !ok {
		t.Fatal("Missing 'arm' arch entry")
	}
	if len(armProps.Properties) != 2 {
		t.Errorf("Expected 2 arm properties, got %d", len(armProps.Properties))
	}

	// arch should be extracted from main properties
	for _, prop := range mod.Map.Properties {
		if prop.Name == "arch" {
			t.Error("'arch' should not be in main properties after extraction")
		}
	}
}

func TestParseInvalidArchOverrideValue(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    arch: true,
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	_, errs := p.Parse()

	if len(errs) == 0 {
		t.Fatal("Expected parse error for invalid arch override value")
	}
}

func TestParseInvalidArchNestedOverrideValue(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    arch: {
        arm: true,
    },
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	_, errs := p.Parse()

	if len(errs) == 0 {
		t.Fatal("Expected parse error for invalid nested arch override value")
	}
}

func TestParseInvalidHostOverrideValue(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    host: false,
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	_, errs := p.Parse()

	if len(errs) == 0 {
		t.Fatal("Expected parse error for invalid host override value")
	}
}

func TestParseInvalidTargetOverrideValue(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    target: "device",
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	_, errs := p.Parse()

	if len(errs) == 0 {
		t.Fatal("Expected parse error for invalid target override value")
	}
}

func TestParseExportedHeaders(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    srcs: ["foo.c"],
    exported_headers: ["include/foo.h", "include/bar.h"],
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	prop := findProperty(mod.Map, "exported_headers")
	if prop == nil {
		t.Fatal("Missing 'exported_headers' property")
	}
	list, ok := prop.Value.(*List)
	if !ok {
		t.Fatalf("Expected *List, got %T", prop.Value)
	}
	if len(list.Values) != 2 {
		t.Fatalf("Expected 2 exported headers, got %d", len(list.Values))
	}
}

func TestParseSharedLibs(t *testing.T) {
	input := `cc_binary {
    name: "hello",
    srcs: ["main.c"],
    deps: [":libstatic"],
    shared_libs: [":libshared"],
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	depsProp := findProperty(mod.Map, "shared_libs")
	if depsProp == nil {
		t.Fatal("Missing 'shared_libs' property")
	}
	list, ok := depsProp.Value.(*List)
	if !ok {
		t.Fatalf("Expected *List, got %T", depsProp.Value)
	}
	if len(list.Values) != 1 {
		t.Fatalf("Expected 1 shared_lib, got %d", len(list.Values))
	}
}
