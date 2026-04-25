package utils

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	buildlib "minibp/lib/build"
	"minibp/lib/parser"
)

func makeSelectArch(arch string) *parser.Select {
	return &parser.Select{
		Conditions: []parser.ConfigurableCondition{{FunctionName: "arch"}},
		Cases: []parser.SelectCase{
			{
				Patterns: []parser.SelectPattern{{Value: &parser.String{Value: arch}}},
				Value:    &parser.List{Values: []parser.Expression{&parser.String{Value: arch + ".c"}}},
			},
			{
				Patterns: []parser.SelectPattern{{Value: &parser.Variable{Name: "default"}}},
				Value:    &parser.List{Values: []parser.Expression{&parser.String{Value: "generic.c"}}},
			},
		},
	}
}

func evalSelectArch(eval *parser.Evaluator, arch string) string {
	result := eval.Eval(makeSelectArch(arch))
	list, ok := result.([]interface{})
	if !ok || len(list) != 1 {
		return ""
	}
	s, _ := list[0].(string)
	return s
}

func makeSelectOS(os string) *parser.Select {
	return &parser.Select{
		Conditions: []parser.ConfigurableCondition{{FunctionName: "os"}},
		Cases: []parser.SelectCase{
			{
				Patterns: []parser.SelectPattern{{Value: &parser.String{Value: os}}},
				Value:    &parser.List{Values: []parser.Expression{&parser.String{Value: os + ".c"}}},
			},
			{
				Patterns: []parser.SelectPattern{{Value: &parser.Variable{Name: "default"}}},
				Value:    &parser.List{Values: []parser.Expression{&parser.String{Value: "generic.c"}}},
			},
		},
	}
}

func evalSelectOS(eval *parser.Evaluator, os string) string {
	result := eval.Eval(makeSelectOS(os))
	list, ok := result.([]interface{})
	if !ok || len(list) != 1 {
		return ""
	}
	s, _ := list[0].(string)
	return s
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b , c", []string{"a", "b", "c"}},
		{"a,,b", []string{"a", "b"}},
		{",,", []string{}},
	}
	for _, tc := range tests {
		got := splitCSV(tc.input)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestDetermineSourceDir(t *testing.T) {
	tests := []struct {
		all    bool
		inputs []string
		want   string
	}{
		{true, []string{"src"}, "src"},
		{true, []string{"."}, "."},
		{false, []string{"src/main.bp"}, "src"},
		{false, []string{"Android.bp"}, "."},
		{false, nil, "."},
	}
	for _, tc := range tests {
		got := determineSourceDir(tc.all, tc.inputs)
		if got != tc.want {
			t.Errorf("determineSourceDir(%v, %v) = %q, want %q", tc.all, tc.inputs, got, tc.want)
		}
	}
}

func TestCollectBlueprintFilesNonAll(t *testing.T) {
	inputs := []string{"a.bp", "b.bp"}
	result, err := collectBlueprintFiles(false, ".", inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(result, inputs) {
		t.Fatalf("expected %v, got %v", inputs, result)
	}
}

func TestCollectBlueprintFilesAll(t *testing.T) {
	tmpDir := t.TempDir()
	for _, name := range []string{"a.bp", "b.bp"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("// test"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := collectBlueprintFiles(true, tmpDir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 .bp files, got %d", len(result))
	}
}

func TestParseRunConfigMissingInput(t *testing.T) {
	var stderr bytes.Buffer
	_, err := ParseRunConfig(nil, &stderr)
	if err == nil {
		t.Fatal("expected error for missing input path")
	}
}

func TestParseRunConfigVersionFlag(t *testing.T) {
	var stderr bytes.Buffer
	cfg, err := ParseRunConfig([]string{"-v"}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ShowVersion {
		t.Error("expected ShowVersion to be true")
	}
}

func TestParseRunConfigWithFile(t *testing.T) {
	var stderr bytes.Buffer
	cfg, err := ParseRunConfig([]string{"Android.bp"}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OutFile != "build.ninja" {
		t.Errorf("expected default outfile, got %q", cfg.OutFile)
	}
	if len(cfg.Inputs) != 1 || cfg.Inputs[0] != "Android.bp" {
		t.Errorf("expected Inputs [Android.bp], got %v", cfg.Inputs)
	}
}

func TestParseRunConfigWithFlags(t *testing.T) {
	var stderr bytes.Buffer
	cfg, err := ParseRunConfig([]string{
		"-o", "out.ninja",
		"-arch", "arm64",
		"-os", "linux",
		"-cc", "clang",
		"-cxx", "clang++",
		"-ar", "llvm-ar",
		"-lto", "thin",
		"-sysroot", "/sys",
		"-ccache", "no",
		"-variant", "image=recovery",
		"-product", "debuggable=true",
		"Android.bp",
	}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OutFile != "out.ninja" {
		t.Errorf("expected outfile out.ninja, got %q", cfg.OutFile)
	}
	if cfg.Arch != "arm64" {
		t.Errorf("expected arch arm64, got %q", cfg.Arch)
	}
	if cfg.TargetOS != "linux" {
		t.Errorf("expected os linux, got %q", cfg.TargetOS)
	}
	if cfg.CC != "clang" {
		t.Errorf("expected cc clang, got %q", cfg.CC)
	}
	if cfg.CXX != "clang++" {
		t.Errorf("expected cxx clang++, got %q", cfg.CXX)
	}
	if cfg.AR != "llvm-ar" {
		t.Errorf("expected ar llvm-ar, got %q", cfg.AR)
	}
	if cfg.LTO != "thin" {
		t.Errorf("expected lto thin, got %q", cfg.LTO)
	}
	if cfg.Sysroot != "/sys" {
		t.Errorf("expected sysroot /sys, got %q", cfg.Sysroot)
	}
	if cfg.Ccache != "no" {
		t.Errorf("expected ccache no, got %q", cfg.Ccache)
	}
	if cfg.Variant != "image=recovery" {
		t.Errorf("expected variant image=recovery, got %q", cfg.Variant)
	}
	if cfg.Product != "debuggable=true" {
		t.Errorf("expected product debuggable=true, got %q", cfg.Product)
	}
}

func TestNewEvaluatorFromConfigSetsArch(t *testing.T) {
	cfg := RunConfig{Arch: "arm64"}
	eval := NewEvaluatorFromConfig(cfg)
	if got := evalSelectArch(eval, "arm64"); got != "arm64.c" {
		t.Errorf("expected arch=arm64 to select arm64.c, got %q", got)
	}
}

func TestNewEvaluatorFromConfigDefaultOS(t *testing.T) {
	cfg := RunConfig{}
	eval := NewEvaluatorFromConfig(cfg)
	if got := evalSelectOS(eval, "linux"); got != "linux.c" {
		t.Errorf("expected default os=linux, got %q", got)
	}
}

func TestNewEvaluatorFromConfigCustomOS(t *testing.T) {
	cfg := RunConfig{TargetOS: "darwin"}
	eval := NewEvaluatorFromConfig(cfg)
	if got := evalSelectOS(eval, "darwin"); got != "darwin.c" {
		t.Errorf("expected os=darwin, got %q", got)
	}
}

func TestNewEvaluatorFromConfigVariant(t *testing.T) {
	cfg := RunConfig{Variant: "image=recovery"}
	eval := NewEvaluatorFromConfig(cfg)

	sel := &parser.Select{
		Conditions: []parser.ConfigurableCondition{
			{FunctionName: "variant", Args: []parser.Expression{&parser.String{Value: "image"}}},
		},
		Cases: []parser.SelectCase{
			{
				Patterns: []parser.SelectPattern{{Value: &parser.String{Value: "recovery"}}},
				Value:    &parser.List{Values: []parser.Expression{&parser.String{Value: "recovery.c"}}},
			},
			{
				Patterns: []parser.SelectPattern{{Value: &parser.Variable{Name: "default"}}},
				Value:    &parser.List{Values: []parser.Expression{&parser.String{Value: "generic.c"}}},
			},
		},
	}
	result := eval.Eval(sel)
	list, ok := result.([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("expected []interface{} with 1 item, got %v", result)
	}
	if list[0] != "recovery.c" {
		t.Errorf("expected recovery.c from variant select, got %v", list[0])
	}
}

func TestBuildOptionsCopiesFields(t *testing.T) {
	cfg := RunConfig{
		Arch:     "arm64",
		SrcDir:   "/src",
		OutFile:  "build.ninja",
		Inputs:   []string{"Android.bp"},
		Multilib: []string{"arm64", "x86_64"},
		CC:       "gcc",
		CXX:      "g++",
		AR:       "ar",
		LTO:      "none",
		Sysroot:  "/sys",
		Ccache:   "/usr/bin/ccache",
	}
	opts := cfg.BuildOptions()
	want := buildlib.Options{
		Arch:     "arm64",
		SrcDir:   "/src",
		OutFile:  "build.ninja",
		Inputs:   []string{"Android.bp"},
		Multilib: []string{"arm64", "x86_64"},
		CC:       "gcc",
		CXX:      "g++",
		AR:       "ar",
		LTO:      "none",
		Sysroot:  "/sys",
		Ccache:   "/usr/bin/ccache",
	}
	if !reflect.DeepEqual(opts, want) {
		t.Fatalf("expected %v, got %v", want, opts)
	}
}

func TestBuildOptionsDoesNotAliasSlices(t *testing.T) {
	cfg := RunConfig{
		Inputs:   []string{"a.bp"},
		Multilib: []string{"arm64"},
	}
	opts := cfg.BuildOptions()
	opts.Inputs[0] = "modified.bp"
	opts.Multilib[0] = "modified"
	if cfg.Inputs[0] == "modified.bp" {
		t.Error("BuildOptions should not share Inputs slice with RunConfig")
	}
	if cfg.Multilib[0] == "modified" {
		t.Error("BuildOptions should not share Multilib slice with RunConfig")
	}
}

func TestSetKeyValueConfigsSkipsInvalid(t *testing.T) {
	eval := NewEvaluatorFromConfig(RunConfig{})

	sel := &parser.Select{
		Conditions: []parser.ConfigurableCondition{
			{FunctionName: "variant", Args: []parser.Expression{&parser.String{Value: "link"}}},
		},
		Cases: []parser.SelectCase{
			{
				Patterns: []parser.SelectPattern{{Value: &parser.String{Value: "shared"}}},
				Value:    &parser.List{Values: []parser.Expression{&parser.String{Value: "shared.c"}}},
			},
			{
				Patterns: []parser.SelectPattern{{Value: &parser.Variable{Name: "default"}}},
				Value:    &parser.List{Values: []parser.Expression{&parser.String{Value: "generic.c"}}},
			},
		},
	}

	cfg := RunConfig{Variant: "link=shared"}
	eval2 := NewEvaluatorFromConfig(cfg)
	result := eval2.Eval(sel)
	list, ok := result.([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("expected []interface{} with 1 item, got %v", result)
	}
	if list[0] != "shared.c" {
		t.Errorf("expected shared.c from variant select, got %v", list[0])
	}

	result = eval.Eval(sel)
	list, ok = result.([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("expected default from empty variant, got %v", result)
	}
	if list[0] != "generic.c" {
		t.Errorf("expected generic.c for unset variant, got %v", list[0])
	}
}
