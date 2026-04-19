// ninja/soong_test.go - Tests for Soong syntax features
package ninja

import (
	"minibp/parser"
	"strings"
	"testing"
)

// TestDefaultsModule tests that defaults modules are recognized
func TestDefaultsModule(t *testing.T) {
	r := &defaults{}
	if r.Name() != "defaults" {
		t.Errorf("Expected name 'defaults', got '%s'", r.Name())
	}

	// Defaults should not produce outputs or edges
	m := &parser.Module{
		Type: "defaults",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "my_defaults"}},
				{Name: "cflags", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "-Wall"},
				}}},
			},
		},
	}

	ctx := DefaultRuleRenderContext()

	// Should not produce outputs
	outs := r.Outputs(m, ctx)
	if outs != nil {
		t.Errorf("Expected nil outputs, got %v", outs)
	}

	// Should not produce edges
	edge := r.NinjaEdge(m, ctx)
	if edge != "" {
		t.Errorf("Expected empty edge, got '%s'", edge)
	}
}

// TestPackageModule tests that package modules are recognized
func TestPackageModule(t *testing.T) {
	r := &packageModule{}
	if r.Name() != "package" {
		t.Errorf("Expected name 'package', got '%s'", r.Name())
	}

	m := &parser.Module{
		Type: "package",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "default_visibility", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: ":__subpackages__"},
				}}},
			},
		},
	}

	ctx := DefaultRuleRenderContext()

	// Should not produce outputs
	outs := r.Outputs(m, ctx)
	if outs != nil {
		t.Errorf("Expected nil outputs, got %v", outs)
	}

	// Should not produce edges
	edge := r.NinjaEdge(m, ctx)
	if edge != "" {
		t.Errorf("Expected empty edge, got '%s'", edge)
	}
}

// TestSoongNamespace tests that soong_namespace modules are recognized
func TestSoongNamespace(t *testing.T) {
	r := &soongNamespace{}
	if r.Name() != "soong_namespace" {
		t.Errorf("Expected name 'soong_namespace', got '%s'", r.Name())
	}

	m := &parser.Module{
		Type: "soong_namespace",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "imports", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "hardware/google/pixel"},
				}}},
			},
		},
	}

	ctx := DefaultRuleRenderContext()

	// Should not produce outputs
	outs := r.Outputs(m, ctx)
	if outs != nil {
		t.Errorf("Expected nil outputs, got %v", outs)
	}

	// Should not produce edges
	edge := r.NinjaEdge(m, ctx)
	if edge != "" {
		t.Errorf("Expected empty edge, got '%s'", edge)
	}
}

// TestParseModuleReference tests parsing of module references
func TestParseModuleReference(t *testing.T) {
	tests := []struct {
		input   string
		valid   bool
		modName string
		tag     string
	}{
		{":my_module", true, "my_module", ""},
		{":my_proto{.h}", true, "my_proto", ".h"},
		{":my_proto{.doc.zip}", true, "my_proto", ".doc.zip"},
		{"not_a_ref", false, "", ""},
	}

	for _, test := range tests {
		ref := ParseModuleReference(test.input)
		if test.valid {
			if ref == nil {
				t.Errorf("Expected valid reference for '%s', got nil", test.input)
				continue
			}
			if ref.ModuleName != test.modName {
				t.Errorf("Expected module name '%s', got '%s'", test.modName, ref.ModuleName)
			}
			if ref.Tag != test.tag {
				t.Errorf("Expected tag '%s', got '%s'", test.tag, ref.Tag)
			}
		} else {
			if ref != nil && ref.IsModuleRef {
				t.Errorf("Expected nil or non-module ref for invalid input '%s', got %v", test.input, ref)
			}
		}
	}
}

// TestExpandModuleReferences tests expansion of module references
func TestExpandModuleReferences(t *testing.T) {
	modules := map[string]*parser.Module{
		"my_proto": {
			Type: "proto_library",
			Map: &parser.Map{
				Properties: []*parser.Property{
					{Name: "name", Value: &parser.String{Value: "my_proto"}},
					{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
						&parser.String{Value: "api.proto"},
					}}},
				},
			},
		},
	}

	ctx := DefaultRuleRenderContext()
	items := []string{":my_proto", "regular_string", ":my_proto{.h}"}
	expanded := ExpandModuleReferences(items, modules, ctx)

	// Should expand module references - at least 3 items (some may expand to multiple)
	if len(expanded) < 3 {
		t.Errorf("Expected expanded references, got %v", expanded)
	}

	// Regular string should remain (somewhere in the expanded list)
	foundRegular := false
	for _, item := range expanded {
		if item == "regular_string" {
			foundRegular = true
			break
		}
	}
	if !foundRegular {
		t.Errorf("Expected 'regular_string' to be preserved in expanded list")
	}
}

// TestVisibilityHelpers tests visibility helper functions
func TestVisibilityHelpers(t *testing.T) {
	public := []string{"//visibility:public"}
	private := []string{"//visibility:private"}
	override := []string{"//visibility:override"}
	mixed := []string{"//some/package:__pkg__", "//visibility:public"}

	if !IsVisibilityPublic(public) {
		t.Error("Expected public visibility to be detected")
	}
	if !IsVisibilityPrivate(private) {
		t.Error("Expected private visibility to be detected")
	}
	if !IsVisibilityOverride(override) {
		t.Error("Expected override visibility to be detected")
	}

	// Mixed should still be detected as containing public
	// (IsVisibilityPublic checks if any rule is public)
	if !IsVisibilityPublic(mixed) {
		t.Error("Mixed visibility containing public should be detected as public")
	}
}

// TestApplyDefaults tests applying defaults to modules
func TestApplyDefaults(t *testing.T) {
	modules := map[string]*parser.Module{
		"my_defaults": {
			Type: "defaults",
			Map: &parser.Map{
				Properties: []*parser.Property{
					{Name: "name", Value: &parser.String{Value: "my_defaults"}},
					{Name: "cflags", Value: &parser.List{Values: []parser.Expression{
						&parser.String{Value: "-Wall"},
					}}},
					{Name: "shared_libs", Value: &parser.List{Values: []parser.Expression{
						&parser.String{Value: "libcutils"},
					}}},
				},
			},
		},
		"my_binary": {
			Type: "cc_binary",
			Map: &parser.Map{
				Properties: []*parser.Property{
					{Name: "name", Value: &parser.String{Value: "my_binary"}},
					{Name: "defaults", Value: &parser.List{Values: []parser.Expression{
						&parser.String{Value: "my_defaults"},
					}}},
					{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
						&parser.String{Value: "main.cpp"},
					}}},
				},
			},
		},
	}

	// Apply defaults
	target := modules["my_binary"]
	ApplyDefaults(target, modules)

	// Check if defaults were applied
	cflags := GetListProp(target, "cflags")
	if len(cflags) == 0 {
		t.Error("Expected cflags to be applied from defaults")
	} else if cflags[0] != "-Wall" {
		t.Errorf("Expected '-Wall' from defaults, got '%s'", cflags[0])
	}
}

// TestGetPackageDefaultVisibility tests package default visibility
func TestGetPackageDefaultVisibility(t *testing.T) {
	modules := map[string]*parser.Module{
		"my/package": {
			Type: "package",
			Map: &parser.Map{
				Properties: []*parser.Property{
					{Name: "default_visibility", Value: &parser.List{Values: []parser.Expression{
						&parser.String{Value: ":__subpackages__"},
					}}},
				},
			},
		},
	}

	vis := GetPackageDefaultVisibility(modules, "my/package")
	if vis == nil || len(vis) == 0 {
		t.Error("Expected default visibility to be found")
	} else if vis[0] != ":__subpackages__" {
		t.Errorf("Expected ':__subpackages__', got '%s'", vis[0])
	}
}

// TestIsValidVisibilityRule tests visibility rule validation
func TestIsValidVisibilityRule(t *testing.T) {
	validRules := []string{
		"//visibility:public",
		"//visibility:private",
		"//visibility:override",
		"//visibility:legacy_public",
		"//visibility:any_partition",
		"//some/package:__pkg__",
		"//some/package:__subpackages__",
		":__subpackages__",
	}

	invalidRules := []string{
		"not_a_rule",
		"//invalid",
		":invalid",
	}

	for _, rule := range validRules {
		if !IsValidVisibilityRule(rule) {
			t.Errorf("Expected '%s' to be valid", rule)
		}
	}

	for _, rule := range invalidRules {
		if IsValidVisibilityRule(rule) {
			t.Errorf("Expected '%s' to be invalid", rule)
		}
	}
}

// TestProtoLibraryRuleName tests that proto_library rule has correct name
func TestProtoLibraryRuleName(t *testing.T) {
	r := &protoLibraryRule{}
	if r.Name() != "proto_library" {
		t.Errorf("Expected name 'proto_library', got '%s'", r.Name())
	}
}

// TestProtoLibraryRuleOutputs tests proto_library outputs
func TestProtoLibraryRuleOutputs(t *testing.T) {
	r := &protoLibraryRule{}

	// Test C++ output (default)
	m := &parser.Module{
		Type: "proto_library",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "myproto"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "api.proto"},
				}}},
			},
		},
	}

	ctx := DefaultRuleRenderContext()
	outs := r.Outputs(m, ctx)

	if len(outs) != 2 {
		t.Errorf("Expected 2 outputs for C++, got %d: %v", len(outs), outs)
	}
	if !strings.HasSuffix(outs[0], ".pb.h") || !strings.HasSuffix(outs[1], ".pb.cc") {
		t.Errorf("Expected .pb.h and .pb.cc outputs, got %v", outs)
	}
}
