package parser

import (
	"strings"
	"testing"
	"text/scanner"
)

// TestEvaluatorVariableResolution tests that variable references are correctly
// resolved during evaluation. A variable assigned before a module should be
// substituted in the module's properties.
func TestEvaluatorVariableResolution(t *testing.T) {
	input := `
foo = "hello"
cc_binary {
    name: foo,
    srcs: ["main.c"]
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	if err := eval.ProcessAssignments(file); err != nil {
		t.Fatalf("ProcessAssignments error: %v", err)
	}

	mod := file.Defs[1].(*Module)
	name := EvalToString(findProp(mod.Map, "name").Value, eval)
	if name != "hello" {
		t.Errorf("Expected name 'hello' from variable, got '%s'", name)
	}
}

// TestEvaluatorPlusEqual tests the += operator for variable concatenation.
// It verifies that += correctly appends to existing string and list variables.
func TestEvaluatorPlusEqual(t *testing.T) {
	input := `
flags = "-Wall"
flags += " -O2"
cc_binary {
    name: "test",
    cflags: [flags],
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	if err := eval.ProcessAssignments(file); err != nil {
		t.Fatalf("ProcessAssignments error: %v", err)
	}

	if val, ok := eval.vars["flags"]; ok {
		s, ok := val.(string)
		if !ok {
			t.Fatalf("Expected string, got %T", val)
		}
		if s != "-Wall -O2" {
			t.Errorf("Expected '-Wall -O2', got '%s'", s)
		}
	} else {
		t.Fatal("Variable 'flags' not found")
	}
}

// TestEvaluatorStringConcatenation tests the + operator for string concatenation
// in expressions. It verifies that "a" + "_" + "b" evaluates to "a_b".
func TestEvaluatorStringConcatenation(t *testing.T) {
	input := `cc_binary {
    name: "prefix" + "_" + "suffix",
    srcs: ["main.c"]
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	nameProp := findProp(mod.Map, "name")
	if nameProp == nil {
		t.Fatal("Missing 'name' property")
	}

	op, ok := nameProp.Value.(*Operator)
	if !ok {
		t.Fatalf("Expected *Operator, got %T", nameProp.Value)
	}
	if op.Operator != '+' {
		t.Errorf("Expected '+', got '%c'", op.Operator)
	}

	eval := NewEvaluator()
	name := EvalToString(nameProp.Value, eval)
	if name != "prefix_suffix" {
		t.Errorf("Expected 'prefix_suffix', got '%s'", name)
	}
}

// TestEvaluatorSelect tests the select() expression for conditional values.
// It verifies that the correct case is selected based on the configuration (arch).
func TestEvaluatorSelect(t *testing.T) {
	input := `cc_binary {
    name: "test",
    srcs: select(arch, {
        arm: ["arm.c"],
        arm64: ["arm64.c"],
        default: ["generic.c"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	srcsProp := findProp(mod.Map, "srcs")
	if srcsProp == nil {
		t.Fatal("Missing 'srcs' property")
	}

	sel, ok := srcsProp.Value.(*Select)
	if !ok {
		t.Fatalf("Expected *Select, got %T", srcsProp.Value)
	}
	if len(sel.Cases) != 3 {
		t.Fatalf("Expected 3 cases, got %d", len(sel.Cases))
	}

	eval := NewEvaluator()
	eval.SetConfig("arch", "arm64")
	result := eval.Eval(sel)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(list))
	}
	s, ok := list[0].(string)
	if !ok || s != "arm64.c" {
		t.Errorf("Expected 'arm64.c', got '%v'", list[0])
	}
}

// TestEvaluatorSelectDefault tests that the default case is used when no
// pattern matches the condition value. When arch is "x86_64" and only "arm"
// and "default" patterns exist, the default should be returned.
func TestEvaluatorSelectDefault(t *testing.T) {
	input := `cc_binary {
    name: "test",
    srcs: select(arch, {
        arm: ["arm.c"],
        default: ["generic.c"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	srcsProp := findProp(mod.Map, "srcs")
	sel := srcsProp.Value.(*Select)

	eval := NewEvaluator()
	eval.SetConfig("arch", "x86_64")
	result := eval.Eval(sel)
	list := result.([]interface{})
	if len(list) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(list))
	}
	s, _ := list[0].(string)
	if s != "generic.c" {
		t.Errorf("Expected 'generic.c' for unmatched arch, got '%s'", s)
	}
}

// TestEvaluatorVariableInModuleProp tests that variables can be used within
// module properties, including in lists. Variables should be resolved to their
// values during evaluation.
func TestEvaluatorVariableInModuleProp(t *testing.T) {
	input := `
base_cflags = "-Wall -Werror"
cc_library {
    name: "lib",
    srcs: ["a.c"],
    cflags: [base_cflags, "-O2"],
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	if err := eval.ProcessAssignments(file); err != nil {
		t.Fatalf("ProcessAssignments error: %v", err)
	}

	mod := file.Defs[1].(*Module)
	cflagsProp := findProp(mod.Map, "cflags")
	list := cflagsProp.Value.(*List)

	result := EvalToStringList(list, eval)
	if len(result) != 2 {
		t.Fatalf("Expected 2 cflags, got %d", len(result))
	}
	if result[0] != "-Wall -Werror" {
		t.Errorf("Expected '-Wall -Werror', got '%s'", result[0])
	}
	if result[1] != "-O2" {
		t.Errorf("Expected '-O2', got '%s'", result[1])
	}
}

func TestEvaluatorSelectWithVariantCondition(t *testing.T) {
	input := `cc_binary {
    name: "test",
    cflags: select(variant("image"), {
        "recovery": ["-DRECOVERY"],
        default: ["-DNORMAL"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	prop := findProp(mod.Map, "cflags")
	eval := NewEvaluator()
	eval.SetConfig("variant.image", "recovery")
	got := EvalToStringList(prop.Value, eval)
	if len(got) != 1 || got[0] != "-DRECOVERY" {
		t.Fatalf("Expected variant select to match recovery, got %v", got)
	}
}

func TestEvaluatorSelectWithProductVariableCondition(t *testing.T) {
	input := `cc_binary {
    name: "test",
    cflags: select(product_variable("debuggable"), {
        "true": ["-DDEBUGGABLE"],
        default: ["-DUSER"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	prop := findProp(mod.Map, "cflags")
	eval := NewEvaluator()
	eval.SetConfig("product.debuggable", "true")
	got := EvalToStringList(prop.Value, eval)
	if len(got) != 1 || got[0] != "-DDEBUGGABLE" {
		t.Fatalf("Expected product_variable select to match true, got %v", got)
	}
}

// TestEvaluatorStringInterpolationInAssignment tests string interpolation
// using ${var} syntax in assignment values. Variables in ${...} should be
// substituted with their values.
func TestEvaluatorStringInterpolationInAssignment(t *testing.T) {
	input := `
base = "lib"
full = "${base}_static"`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	if err := eval.ProcessAssignments(file); err != nil {
		t.Fatalf("ProcessAssignments error: %v", err)
	}

	val, ok := eval.vars["full"].(string)
	if !ok {
		t.Fatalf("Expected string, got %T", eval.vars["full"])
	}
	if val != "lib_static" {
		t.Fatalf("Expected 'lib_static', got %q", val)
	}
}

// TestEvaluatorStringInterpolationInModuleProp tests string interpolation
// in module property values. Variables inside ${...} should be substituted.
func TestEvaluatorStringInterpolationInModuleProp(t *testing.T) {
	input := `
suffix = "world"
cc_binary {
    name: "hello_${suffix}",
    srcs: ["main.c"],
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	if err := eval.ProcessAssignments(file); err != nil {
		t.Fatalf("ProcessAssignments error: %v", err)
	}

	mod := file.Defs[1].(*Module)
	name := EvalToString(findProp(mod.Map, "name").Value, eval)
	if name != "hello_world" {
		t.Fatalf("Expected 'hello_world', got %q", name)
	}
}

// TestEvaluatorStringInterpolationUnknownVarPreserved tests that unknown
// variable references in ${...} are preserved as-is rather than causing errors.
// This allows templates with placeholders that may be filled later.
func TestEvaluatorStringInterpolationUnknownVarPreserved(t *testing.T) {
	eval := NewEvaluator()
	got := eval.Eval(&String{Value: "pre_${missing}_post"})

	s, ok := got.(string)
	if !ok {
		t.Fatalf("Expected string, got %T", got)
	}
	if s != "pre_${missing}_post" {
		t.Fatalf("Expected unknown interpolation to be preserved, got %q", s)
	}
}

// TestEvaluatorIntegerAddition tests the + operator for integer arithmetic.
// It verifies that 1 + 2 correctly evaluates to 3.
func TestEvaluatorIntegerAddition(t *testing.T) {
	input := `sum = 1 + 2`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	if err := eval.ProcessAssignments(file); err != nil {
		t.Fatalf("ProcessAssignments error: %v", err)
	}

	val, ok := eval.vars["sum"].(int64)
	if !ok {
		t.Fatalf("Expected int64, got %T", eval.vars["sum"])
	}
	if val != 3 {
		t.Fatalf("Expected 3, got %d", val)
	}
}

// TestEvaluatorListPlusEqualList tests += with a list on the right side.
// It verifies that srcs += ["b.c", "c.c"] appends the list items to the existing list.
func TestEvaluatorListPlusEqualList(t *testing.T) {
	input := `
srcs = ["a.c"]
srcs += ["b.c", "c.c"]`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	if err := eval.ProcessAssignments(file); err != nil {
		t.Fatalf("ProcessAssignments error: %v", err)
	}

	val, ok := eval.vars["srcs"].([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", eval.vars["srcs"])
	}
	if len(val) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(val))
	}
	got := []string{val[0].(string), val[1].(string), val[2].(string)}
	want := []string{"a.c", "b.c", "c.c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Expected %v, got %v", want, got)
		}
	}
}

// TestEvaluatorListPlusEqualScalar tests += with a scalar (string) on the right side.
// It verifies that srcs += "b.c" appends a single item to the existing list.
func TestEvaluatorListPlusEqualScalar(t *testing.T) {
	input := `
srcs = ["a.c"]
srcs += "b.c"`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	if err := eval.ProcessAssignments(file); err != nil {
		t.Fatalf("ProcessAssignments error: %v", err)
	}

	val, ok := eval.vars["srcs"].([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", eval.vars["srcs"])
	}
	if len(val) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(val))
	}
	if val[0] != "a.c" || val[1] != "b.c" {
		t.Fatalf("Expected [a.c b.c], got %v", val)
	}
}

// TestEvaluatorUndefinedVariableReturnsNil tests that undefined variables
// return nil instead of their name as a string, preventing type confusion.
func TestEvaluatorUndefinedVariableReturnsNil(t *testing.T) {
	eval := NewEvaluator()
	// Reference an undefined variable
	result := eval.Eval(&Variable{Name: "undefined_var", NamePos: scanner.Position{}})
	if result != nil {
		t.Fatalf("Expected nil for undefined variable, got %v", result)
	}
}

// TestEvaluatorMixedAdditionUnsupported tests that mixing incompatible types
// in addition (e.g., int + string) returns nil rather than panicking or producing
// incorrect results.
func TestEvaluatorMixedAdditionUnsupported(t *testing.T) {
	got := evalOperator(int64(1), "x", '+')
	if got != nil {
		t.Fatalf("Expected nil for unsupported mixed addition, got %v", got)
	}
}

// TestEvaluatorSubtractionOperator tests the - operator for integer subtraction.
func TestEvaluatorSubtractionOperator(t *testing.T) {
	got := evalOperator(int64(5), int64(3), '-')
	if got != int64(2) {
		t.Fatalf("Expected 2 for 5-3, got %v", got)
	}
}

// TestEvaluatorMultiplicationOperator tests the * operator for integer multiplication.
func TestEvaluatorMultiplicationOperator(t *testing.T) {
	got := evalOperator(int64(4), int64(3), '*')
	if got != int64(12) {
		t.Fatalf("Expected 12 for 4*3, got %v", got)
	}
}

// TestEvaluatorSelectUnknownConfigFallsBackToDefault tests that when a config
// key has no value set, the default case is used. Without a config value,
// the select should fall back to the default pattern.
func TestEvaluatorSelectUnknownConfigFallsBackToDefault(t *testing.T) {
	input := `cc_binary {
    name: "test",
    srcs: select(os, {
        linux: ["linux.c"],
        default: ["generic.c"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	mod := file.Defs[0].(*Module)
	srcsProp := findProp(mod.Map, "srcs")
	result := eval.Eval(srcsProp.Value)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 || list[0] != "generic.c" {
		t.Fatalf("Expected [generic.c], got %v", list)
	}
}

// TestEvaluatorSelectWithOSCondition tests the select() function with the "os"
// condition. It verifies that different values are selected based on the os config.
func TestEvaluatorSelectWithOSCondition(t *testing.T) {
	input := `cc_binary {
    name: "test",
    srcs: select(os, {
        linux: ["linux.c"],
        default: ["generic.c"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	eval.SetConfig("os", "linux")
	mod := file.Defs[0].(*Module)
	srcsProp := findProp(mod.Map, "srcs")
	result := eval.Eval(srcsProp.Value)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 || list[0] != "linux.c" {
		t.Fatalf("Expected [linux.c], got %v", list)
	}
}

// TestEvaluatorSelectMatchesIntegerPattern tests that select() can match
// integer pattern values, not just strings. This verifies pattern matching
// works with numeric values.
func TestEvaluatorSelectMatchesIntegerPattern(t *testing.T) {
	input := `cc_binary {
    name: "test",
    srcs: select(level, {
        1: ["one.c"],
        2, 3: ["many.c"],
        default: ["generic.c"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	eval.SetVar("level", int64(3))
	mod := file.Defs[0].(*Module)
	result := eval.Eval(findProp(mod.Map, "srcs").Value)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 || list[0] != "many.c" {
		t.Fatalf("Expected [many.c], got %v", list)
	}
}

// TestEvaluatorSelectMatchesBooleanPattern tests that select() can match
// boolean pattern values (true/false). This verifies pattern matching works
// with boolean condition values.
func TestEvaluatorSelectMatchesBooleanPattern(t *testing.T) {
	input := `cc_binary {
    name: "test",
    srcs: select(enabled, {
        true: ["enabled.c"],
        false: ["disabled.c"],
        default: ["generic.c"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	eval.SetVar("enabled", true)
	mod := file.Defs[0].(*Module)
	result := eval.Eval(findProp(mod.Map, "srcs").Value)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 || list[0] != "enabled.c" {
		t.Fatalf("Expected [enabled.c], got %v", list)
	}
}

// TestEvaluatorSelectMultiPatternCase tests select() with multiple patterns
// in a single case (e.g., "linux", "android": ["unix.c"]). The evaluator should
// match either pattern and return the corresponding value.
func TestEvaluatorSelectMultiPatternCase(t *testing.T) {
	input := `cc_binary {
    name: "test",
    srcs: select(os, {
        "linux", "android": ["unix.c"],
        default: ["generic.c"],
    }),
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	eval.SetConfig("os", "android")
	mod := file.Defs[0].(*Module)
	result := eval.Eval(findProp(mod.Map, "srcs").Value)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 || list[0] != "unix.c" {
		t.Fatalf("Expected [unix.c], got %v", list)
	}
}

// TestParseHostBlock tests parsing the host: {} block for host-specific
// property overrides. The parser should extract host properties and store them
// in the Module.Host field.
func TestParseHostBlock(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    srcs: ["foo.c"],
    host: {
        cflags: ["-DHOST_BUILD"],
    },
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	if mod.Host == nil {
		t.Fatal("Expected module.Host to be non-nil")
	}
	if len(mod.Host.Properties) != 1 {
		t.Fatalf("Expected 1 host property, got %d", len(mod.Host.Properties))
	}
	if mod.Host.Properties[0].Name != "cflags" {
		t.Errorf("Expected 'cflags', got '%s'", mod.Host.Properties[0].Name)
	}
}

// TestParseTargetBlock tests parsing the target: {} block for target-specific
// property overrides. The parser should extract target properties and store them
// in the Module.Target field.
func TestParseTargetBlock(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    srcs: ["foo.c"],
    target: {
        cflags: ["-DTARGET_BUILD"],
    },
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	if mod.Target == nil {
		t.Fatal("Expected module.Target to be non-nil")
	}
	if mod.Target.Properties[0].Name != "cflags" {
		t.Errorf("Expected 'cflags', got '%s'", mod.Target.Properties[0].Name)
	}
}

// TestParseArchHostTargetTogether tests that arch, host, and target blocks
// can all appear in the same module. The parser should correctly extract
// all three types of overrides while keeping them separate.
func TestParseArchHostTargetTogether(t *testing.T) {
	input := `cc_library {
    name: "libfoo",
    srcs: ["foo.c"],
    arch: {
        arm: { cflags: ["-DARM"] },
    },
    host: {
        cflags: ["-DHOST"],
    },
    target: {
        cflags: ["-DDEVICE"],
    },
}`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()

	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	mod := file.Defs[0].(*Module)
	if mod.Arch == nil || mod.Host == nil || mod.Target == nil {
		t.Fatal("Expected arch, host, and target to all be non-nil")
	}

	for _, prop := range mod.Map.Properties {
		if prop.Name == "arch" || prop.Name == "host" || prop.Name == "target" {
			t.Errorf("'%s' should not be in main properties", prop.Name)
		}
	}
}

// findProp is a helper function that searches a map for a property by name.
// It returns nil if the property is not found.
// Parameters:
//   - m: The Map to search
//   - name: The property name to find
//
// Returns:
//   - *Property: The matching property, or nil if not found
func findProp(m *Map, name string) *Property {
	for _, prop := range m.Properties {
		if prop.Name == name {
			return prop
		}
	}
	return nil
}

func TestEvaluatorSelectMultiVariable(t *testing.T) {
	input := `cc_binary {
	name: "test",
	srcs: select((arch(), os()), {
		("arm", "linux"): ["arm_linux.c"],
		default: ["generic.c"],
	}),
}`
	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}
	mod := file.Defs[0].(*Module)
	srcsProp := findProp(mod.Map, "srcs")
	sel, ok := srcsProp.Value.(*Select)
	if !ok {
		t.Fatalf("Expected *Select, got %T", srcsProp.Value)
	}
	if len(sel.Conditions) != 2 {
		t.Fatalf("Expected 2 conditions, got %d", len(sel.Conditions))
	}
	eval := NewEvaluator()
	eval.SetConfig("arch", "arm")
	eval.SetConfig("os", "linux")
	result := eval.Eval(sel)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 || list[0] != "arm_linux.c" {
		t.Errorf("Expected [arm_linux.c], got %v", list)
	}
}

func TestEvaluatorSelectMultiVariableDefault(t *testing.T) {
	input := `cc_binary {
	name: "test",
	srcs: select((arch(), os()), {
		("arm", "linux"): ["arm_linux.c"],
		default: ["generic.c"],
	}),
}`
	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}
	mod := file.Defs[0].(*Module)
	sel := findProp(mod.Map, "srcs").Value.(*Select)
	eval := NewEvaluator()
	eval.SetConfig("arch", "x86_64")
	eval.SetConfig("os", "linux")
	result := eval.Eval(sel)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 || list[0] != "generic.c" {
		t.Errorf("Expected [generic.c] for default, got %v", list)
	}
}

func TestEvaluatorSelectUnset(t *testing.T) {
	input := `cc_binary {
	name: "test",
	enabled: select(os(), {
		"darwin": false,
		default: unset,
	}),
}`
	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}
	mod := file.Defs[0].(*Module)
	sel := findProp(mod.Map, "enabled").Value.(*Select)

	eval := NewEvaluator()
	eval.SetConfig("os", "darwin")
	result := eval.Eval(sel)
	if b, ok := result.(bool); !ok || b != false {
		t.Errorf("Expected false for darwin, got %v", result)
	}

	eval2 := NewEvaluator()
	eval2.SetConfig("os", "linux")
	result2 := eval2.Eval(sel)
	if result2 != UnsetSentinel {
		t.Errorf("Expected UnsetSentinel for linux, got %v", result2)
	}
}

func TestEvaluatorSelectAnyBinding(t *testing.T) {
	input := `cc_binary {
	name: "test",
	cflags: select(os(), {
		"linux": ["-DLINUX"],
		any @ my_os: ["-D" + my_os],
		default: ["-DGENERIC"],
	}),
}`
	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}
	mod := file.Defs[0].(*Module)
	sel := findProp(mod.Map, "cflags").Value.(*Select)

	eval := NewEvaluator()
	eval.SetConfig("os", "freebsd")
	result := eval.Eval(sel)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(list))
	}
	s, ok := list[0].(string)
	if !ok || s != "-Dfreebsd" {
		t.Errorf("Expected '-Dfreebsd' from any @ my_os binding, got '%s'", s)
	}
}

func TestEvaluatorListConcatenation(t *testing.T) {
	result := evalOperator([]interface{}{"a", "b"}, []interface{}{"c"}, '+')
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 3 || list[0] != "a" || list[1] != "b" || list[2] != "c" {
		t.Errorf("Expected [a b c], got %v", list)
	}
}

func TestEvaluatorMapMerge(t *testing.T) {
	left := map[string]interface{}{
		"a": []interface{}{"1"},
		"b": "base",
	}
	right := map[string]interface{}{
		"a": []interface{}{"2"},
		"c": "new",
	}
	result := evalOperator(left, right, '+')
	merged, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map[string]interface{}, got %T", result)
	}
	listA, ok := merged["a"].([]interface{})
	if !ok || len(listA) != 2 || listA[0] != "1" || listA[1] != "2" {
		t.Errorf("Expected merged list [1 2], got %v", merged["a"])
	}
	if merged["b"] != "base" {
		t.Errorf("Expected 'base' for key b, got %v", merged["b"])
	}
	if merged["c"] != "new" {
		t.Errorf("Expected 'new' for key c, got %v", merged["c"])
	}
}

func TestEvaluatorSelectSoongConfigVariable(t *testing.T) {
	input := `cc_binary {
	name: "test",
	cflags: select(soong_config_variable("acme", "board"), {
		"soc_a": ["-DSOC_A"],
		default: ["-DGENERIC"],
	}),
}`
	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}
	mod := file.Defs[0].(*Module)
	sel := findProp(mod.Map, "cflags").Value.(*Select)

	eval := NewEvaluator()
	eval.SetConfig("acme.board", "soc_a")
	result := eval.Eval(sel)
	list, ok := result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", result)
	}
	if len(list) != 1 || list[0] != "-DSOC_A" {
		t.Errorf("Expected [-DSOC_A] for soong_config_variable match, got %v", list)
	}
}

func TestEvaluatorSelectNoDefaultStrictError(t *testing.T) {
	input := `cc_binary {
	name: "test",
	srcs: select(arch, {
		"arm": ["arm.c"],
	}),
}`
	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}
	mod := file.Defs[0].(*Module)
	sel := findProp(mod.Map, "srcs").Value.(*Select)

	eval := NewEvaluator()
	eval.SetConfig("arch", "x86_64")
	eval.Eval(sel)
	if len(eval.SelectErrors()) == 0 {
		t.Error("Expected select error for no matching case and no default in strict mode")
	}
}

func TestEvaluatorSelectPlusConcatenation(t *testing.T) {
	input := `cc_binary {
	name: "test",
	cflags: select(os(), {
		"linux_glibc": "penguin",
		default: "unknown",
	}) + "-" + select(arch, {
		"arm": "arm",
		default: "generic",
	}),
}`
	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}
	mod := file.Defs[0].(*Module)
	cflagsProp := findProp(mod.Map, "cflags")
	eval := NewEvaluator()
	eval.SetConfig("os", "linux_glibc")
	eval.SetConfig("arch", "arm")
	result := eval.Eval(cflagsProp.Value)
	s, ok := result.(string)
	if !ok {
		t.Fatalf("Expected string, got %T", result)
	}
	if s != "penguin-arm" {
		t.Errorf("Expected 'penguin-arm', got '%s'", s)
	}
}

func TestParseUnsetInSelect(t *testing.T) {
	input := `cc_binary {
	name: "test",
	enabled: select(os(), {
		"darwin": false,
		default: unset,
	}),
}`
	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}
	mod := file.Defs[0].(*Module)
	sel := findProp(mod.Map, "enabled").Value.(*Select)
	if len(sel.Cases) != 2 {
		t.Fatalf("Expected 2 cases, got %d", len(sel.Cases))
	}
	// The second case is "default: unset" — the VALUE is unset, not the pattern
	unsetVal, ok := sel.Cases[1].Value.(*Unset)
	if !ok {
		t.Fatalf("Expected second case VALUE to be *Unset, got %T", sel.Cases[1].Value)
	}
	_ = unsetVal
}
