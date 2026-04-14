package parser

import (
	"strings"
	"testing"
)

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
	eval.ProcessAssignments(file)

	mod := file.Defs[1].(*Module)
	name := EvalToString(findProp(mod.Map, "name").Value, eval)
	if name != "hello" {
		t.Errorf("Expected name 'hello' from variable, got '%s'", name)
	}
}

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
	eval.ProcessAssignments(file)

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
	eval.ProcessAssignments(file)

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
	eval.ProcessAssignments(file)

	val, ok := eval.vars["full"].(string)
	if !ok {
		t.Fatalf("Expected string, got %T", eval.vars["full"])
	}
	if val != "lib_static" {
		t.Fatalf("Expected 'lib_static', got %q", val)
	}
}

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
	eval.ProcessAssignments(file)

	mod := file.Defs[1].(*Module)
	name := EvalToString(findProp(mod.Map, "name").Value, eval)
	if name != "hello_world" {
		t.Fatalf("Expected 'hello_world', got %q", name)
	}
}

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

func TestEvaluatorIntegerAddition(t *testing.T) {
	input := `sum = 1 + 2`

	p := NewParser(strings.NewReader(input), "test.bp")
	file, errs := p.Parse()
	if len(errs) > 0 {
		t.Fatalf("Parse errors: %v", errs)
	}

	eval := NewEvaluator()
	eval.ProcessAssignments(file)

	val, ok := eval.vars["sum"].(int64)
	if !ok {
		t.Fatalf("Expected int64, got %T", eval.vars["sum"])
	}
	if val != 3 {
		t.Fatalf("Expected 3, got %d", val)
	}
}

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
	eval.ProcessAssignments(file)

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
	eval.ProcessAssignments(file)

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

func TestEvaluatorMixedAdditionUnsupported(t *testing.T) {
	got := evalOperator(int64(1), "x", '+')
	if got != nil {
		t.Fatalf("Expected nil for unsupported mixed addition, got %v", got)
	}
}

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

func findProp(m *Map, name string) *Property {
	for _, prop := range m.Properties {
		if prop.Name == name {
			return prop
		}
	}
	return nil
}
