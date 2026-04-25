// ninja/ninja_test.go - Tests for ninja package
package ninja

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"

	"minibp/lib/dag"
	"minibp/lib/parser"
)

type dagMockModule struct {
	name string
}

func (m *dagMockModule) Name() string                   { return m.name }
func (m *dagMockModule) Type() string                   { return "mock" }
func (m *dagMockModule) Srcs() []string                 { return nil }
func (m *dagMockModule) Deps() []string                 { return nil }
func (m *dagMockModule) Props() map[string]interface{}  { return nil }
func (m *dagMockModule) GetProp(key string) interface{} { return nil }

// mockRule implements BuildRule for testing
type mockRule struct {
	name string
}

func (r *mockRule) Name() string {
	return r.name
}

func (r *mockRule) NinjaRule(ctx RuleRenderContext) string {
	return "rule " + r.name + "\n  command = echo " + r.name + "\n"
}

func (r *mockRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	if m == nil {
		return ""
	}
	return "build " + getName(m) + ": " + r.name + " " + formatSrcs(getSrcs(m)) + "\n"
}

func (r *mockRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	return []string{getName(m)}
}

func (r *mockRule) Desc(m *parser.Module, srcFile string) string {
	return "mock"
}

func TestNewWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if w == nil {
		t.Fatal("NewWriter returned nil")
	}
}

func TestWriterComment(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Comment("Test comment")
	output := buf.String()
	if !strings.Contains(output, "# Test comment") {
		t.Errorf("Expected comment in output, got: %s", output)
	}
}

func TestWriterVariable(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Variable("key", "value")
	output := buf.String()
	if !strings.Contains(output, "key = value") {
		t.Errorf("Expected variable in output, got: %s", output)
	}
}

func TestWriterRule(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Rule("cc", "gcc -c $in -o $out")
	output := buf.String()
	if !strings.Contains(output, "rule cc") {
		t.Errorf("Expected rule cc in output, got: %s", output)
	}
	if !strings.Contains(output, "command = gcc") {
		t.Errorf("Expected command in output, got: %s", output)
	}
}

func TestWriterBuild(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Build("out.o", "cc", []string{"in.c"}, nil)
	output := buf.String()
	if !strings.Contains(output, "build out.o: cc in.c") {
		t.Errorf("Expected build edge in output, got: %s", output)
	}
}

func TestWriterEscapesSpecialCharacters(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Build("out dir/file:name", "cc", []string{"src dir/$in.c"}, nil)
	output := buf.String()
	if !strings.Contains(output, "out$ dir/file$:name") {
		t.Fatalf("Expected escaped output path, got: %s", output)
	}
	if !strings.Contains(output, "src$ dir/$$in.c") {
		t.Fatalf("Expected escaped input path, got: %s", output)
	}
}

func TestCleanCommandQuotesOutputs(t *testing.T) {
	cmd := cleanCommand([]string{"out dir/app", "lib$name.a"})
	if runtime.GOOS == "windows" {
		if !strings.Contains(cmd, "cmd /c del /q") {
			t.Fatalf("Expected windows clean command, got: %s", cmd)
		}
	} else {
		if !strings.Contains(cmd, "rm -f") {
			t.Fatalf("Expected unix clean command, got: %s", cmd)
		}
	}
	if !strings.Contains(cmd, "\"out dir/app\"") || !strings.Contains(cmd, "\"lib$name.a\"") {
		t.Fatalf("Expected quoted outputs in clean command, got: %s", cmd)
	}
}

func TestCollectBuildOutputs(t *testing.T) {
	edge := "build foo.o: cc_compile foo.c\n flags = -Wall\nbuild app libapp.map: cc_link foo.o\n"
	outputs := collectBuildOutputs(edge)
	want := []string{"foo.o", "app", "libapp.map"}
	if len(outputs) != len(want) {
		t.Fatalf("Expected %d outputs, got %d: %v", len(want), len(outputs), outputs)
	}
	for i, out := range want {
		if outputs[i] != out {
			t.Fatalf("Expected output %d to be %q, got %q", i, out, outputs[i])
		}
	}
}

func TestGeneratorAddsDataDepsToBuildEdge(t *testing.T) {
	graph := dag.NewGraph()
	graph.AddModule(&dagMockModule{name: "payload"})
	graph.AddModule(&dagMockModule{name: "runner"})
	graph.AddEdge("runner", "payload")

	modules := map[string]*parser.Module{
		"payload": {
			Type: "filegroup",
			Map: &parser.Map{Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "payload"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "assets/config.json"},
				}}},
			}},
		},
		"runner": {
			Type: "python_test_host",
			Map: &parser.Map{Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "runner"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "runner_test.py"},
				}}},
				{Name: "data", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: ":payload"},
				}}},
				{Name: "test_options", Value: &parser.Map{Properties: []*parser.Property{
					{Name: "args", Value: &parser.List{Values: []parser.Expression{
						&parser.String{Value: "--verbose"},
					}}},
				}}},
			}},
		},
	}

	rules := map[string]BuildRule{
		"filegroup":        &filegroup{},
		"python_test_host": &pythonTestHostRule{},
	}

	var buf bytes.Buffer
	gen := NewGenerator(graph, rules, modules)
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "build runner.test.py: python_test runner_test.py | payload.files") {
		t.Fatalf("Expected data dependency on payload.files, got: %s", out)
	}
	if !strings.Contains(out, "args = --verbose") {
		t.Fatalf("Expected python test args from test_options, got: %s", out)
	}
}

func TestGeneratorAddsDistEdges(t *testing.T) {
	graph := dag.NewGraph()
	graph.AddModule(&dagMockModule{name: "tool"})

	modules := map[string]*parser.Module{
		"tool": {
			Type: "cc_prebuilt_binary",
			Map: &parser.Map{Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "tool"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "prebuilts/tool"},
				}}},
				{Name: "dist", Value: &parser.Map{Properties: []*parser.Property{
					{Name: "dir", Value: &parser.String{Value: "artifacts"}},
					{Name: "dest", Value: &parser.String{Value: "tool-release"}},
				}}},
			}},
		},
	}

	rules := map[string]BuildRule{
		"cc_prebuilt_binary": &prebuiltBinaryRule{typeName: "cc_prebuilt_binary"},
		"prebuilt_etc":       &prebuiltEtcRule{typeName: "prebuilt_etc", subdir: "etc"},
	}

	var buf bytes.Buffer
	gen := NewGenerator(graph, rules, modules)
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "build dist/artifacts/tool-release: prebuilt_copy tool") {
		t.Fatalf("Expected dist edge, got: %s", out)
	}
}

func TestGeneratorAddsDistSuffix(t *testing.T) {
	graph := dag.NewGraph()
	graph.AddModule(&dagMockModule{name: "libfoo"})

	modules := map[string]*parser.Module{
		"libfoo": {
			Type: "cc_prebuilt_library_shared",
			Map: &parser.Map{Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "foo"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "prebuilts/libfoo.so"},
				}}},
				{Name: "dist", Value: &parser.Map{Properties: []*parser.Property{
					{Name: "dir", Value: &parser.String{Value: "symbols"}},
					{Name: "suffix", Value: &parser.String{Value: "-dbg"}},
				}}},
			}},
		},
	}

	rules := map[string]BuildRule{
		"cc_prebuilt_library_shared": &prebuiltLibraryRule{typeName: "cc_prebuilt_library_shared", ext: ".so"},
		"prebuilt_etc":               &prebuiltEtcRule{typeName: "prebuilt_etc", subdir: "etc"},
	}

	var buf bytes.Buffer
	gen := NewGenerator(graph, rules, modules)
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "build dist/symbols/libfoo-dbg.so: prebuilt_copy libfoo.so") {
		t.Fatalf("Expected dist suffix edge, got: %s", out)
	}
}

func TestJavaImportRuleUsesPlatformCopyCommand(t *testing.T) {
	r := &javaImport{}
	rule := r.NinjaRule(DefaultRuleRenderContext())
	if runtime.GOOS == "windows" {
		if !strings.Contains(rule, "cmd /c copy") {
			t.Fatalf("Expected windows copy command, got: %s", rule)
		}
	} else {
		if !strings.Contains(rule, "cp $in $out") {
			t.Fatalf("Expected unix copy command, got: %s", rule)
		}
	}
}

func TestNewGenerator(t *testing.T) {
	g := dag.NewGraph()
	rules := make(map[string]BuildRule)
	modules := make(map[string]*parser.Module)
	gen := NewGenerator(g, rules, modules)
	if gen == nil {
		t.Fatal("NewGenerator returned nil")
	}
}

func TestGeneratorGenerate(t *testing.T) {
	g := dag.NewGraph()

	// Create a mock module
	m := &parser.Module{
		Type: "mock",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "hello"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "main.c"},
				}}},
			},
		},
	}

	// Setup rules
	rules := map[string]BuildRule{
		"mock": &mockRule{name: "mock"},
	}

	// Setup modules
	modules := map[string]*parser.Module{
		"hello": m,
	}

	gen := NewGenerator(g, rules, modules)

	var buf bytes.Buffer
	err := gen.Generate(&buf)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()

	// Check header
	if !strings.Contains(output, "Generated by minibp") {
		t.Errorf("Expected header in output, got: %s", output)
	}

	// Check rule definition
	if !strings.Contains(output, "rule mock") {
		t.Errorf("Expected rule definition in output, got: %s", output)
	}
}

func TestGeneratorWithCCLibrary(t *testing.T) {
	g := dag.NewGraph()

	m := &parser.Module{
		Type: "cc_library",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "mylib"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "lib.c"},
				}}},
			},
		},
	}

	rules := map[string]BuildRule{
		"cc_library": &ccLibrary{},
	}

	modules := map[string]*parser.Module{
		"mylib": m,
	}

	gen := NewGenerator(g, rules, modules)

	var buf bytes.Buffer
	err := gen.Generate(&buf)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "rule cc_compile") {
		t.Errorf("Expected cc_compile rule in output, got: %s", output)
	}

	if !strings.Contains(output, "rule cc_archive") {
		t.Errorf("Expected cc_archive rule in output, got: %s", output)
	}

	if !strings.Contains(output, "depfile = $out.d") {
		t.Errorf("Expected depfile in cc_compile rule, got: %s", output)
	}

	if !strings.Contains(output, "deps = gcc") {
		t.Errorf("Expected 'deps = gcc' in cc_compile rule, got: %s", output)
	}

	if !strings.Contains(output, "-MMD -MF $out.d") {
		t.Errorf("Expected -MMD -MF in compile command, got: %s", output)
	}
}

func TestGeneratorPhonyTargets(t *testing.T) {
	g := dag.NewGraph()

	m := &parser.Module{
		Type: "cc_library",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "mylib"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "main.c"},
				}}},
			},
		},
	}

	rules := map[string]BuildRule{
		"cc_library": &ccLibrary{},
	}

	modules := map[string]*parser.Module{
		"mylib": m,
	}

	g.AddModule(&dagMockModule{name: "mylib"})

	gen := NewGenerator(g, rules, modules)

	var buf bytes.Buffer
	err := gen.Generate(&buf)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()

	if !strings.Contains(output, "build mylib: phony libmylib.a") {
		t.Errorf("Expected phony target for mylib, got: %s", output)
	}
}

func TestGeneratorAppliesToolchainAndArchFlags(t *testing.T) {
	g := dag.NewGraph()

	m := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "app"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "main.c"},
				}}},
				{Name: "cflags", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "-DMODULE"},
				}}},
				{Name: "ldflags", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "-pthread"},
				}}},
			},
		},
	}

	rules := map[string]BuildRule{
		"cc_binary": &ccBinary{},
	}

	modules := map[string]*parser.Module{
		"app": m,
	}
	g.AddModule(&dagMockModule{name: "app"})

	gen := NewGenerator(g, rules, modules)
	gen.SetToolchain(Toolchain{
		CC:      "clang",
		CXX:     "clang++",
		AR:      "llvm-ar",
		CFlags:  []string{"--sysroot=/opt/sdk", "-DTOOLCHAIN"},
		LdFlags: []string{"-fuse-ld=lld"},
	})
	gen.SetArch("x86_64")

	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "command = clang -c $in -o $out $flags -MMD -MF $out.d") {
		t.Fatalf("Expected configured compiler in compile rule, got: %s", output)
	}
	if !strings.Contains(output, "command = ${CC} -o $out @$out.rsp $flags") {
		t.Fatalf("Expected rspfile-based link rule, got: %s", output)
	}
	if !strings.Contains(output, "flags = --sysroot=/opt/sdk -DTOOLCHAIN -m64 -DMODULE -I.\n") {
		t.Fatalf("Expected merged compile flags in edge, got: %s", output)
	}
	if !strings.Contains(output, "flags = -fuse-ld=lld -m64 -pthread\n") {
		t.Fatalf("Expected merged link flags in edge, got: %s", output)
	}
	if strings.Contains(output, "build app_x86_64: cc_link app_main.o\n flags = -fuse-ld=lld -m64 -pthread -I.") {
		t.Fatalf("Expected compile include paths to be excluded from link edge, got: %s", output)
	}
	if !strings.Contains(output, "build app_x86_64: cc_link app_main.o") {
		t.Fatalf("Expected arch-suffixed binary output, got: %s", output)
	}
	if !strings.Contains(output, "build app_main.o: cc_compile main.c") {
		t.Fatalf("Expected object compile edge, got: %s", output)
	}
	if got := os.Getenv("MINIBP_CFLAGS"); got != "" {
		t.Fatalf("Expected generation to avoid MINIBP_CFLAGS mutation, got %q", got)
	}
	if got := os.Getenv("MINIBP_LDFLAGS"); got != "" {
		t.Fatalf("Expected generation to avoid MINIBP_LDFLAGS mutation, got %q", got)
	}
}

func TestGeneratorEnvIsolationAcrossInstances(t *testing.T) {
	newGenerator := func(cc, arch string) *Generator {
		g := dag.NewGraph()
		m := &parser.Module{Type: "cc_binary", Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "main.c"}}}},
		}}}
		rules := map[string]BuildRule{"cc_binary": &ccBinary{}}
		modules := map[string]*parser.Module{"app": m}
		g.AddModule(&dagMockModule{name: "app"})

		gen := NewGenerator(g, rules, modules)
		gen.SetToolchain(Toolchain{CC: cc})
		gen.SetArch(arch)
		return gen
	}

	var first bytes.Buffer
	if err := newGenerator("clang", "x86_64").Generate(&first); err != nil {
		t.Fatalf("first Generate failed: %v", err)
	}
	firstOut := first.String()
	if !strings.Contains(firstOut, "command = clang -c $in -o $out $flags -MMD -MF $out.d") {
		t.Fatalf("Expected first generator to use clang, got: %s", firstOut)
	}
	if !strings.Contains(firstOut, "build app_x86_64: cc_link app_main.o") {
		t.Fatalf("Expected first generator to use x86_64 suffix, got: %s", firstOut)
	}

	var second bytes.Buffer
	if err := newGenerator("zig cc", "arm64").Generate(&second); err != nil {
		t.Fatalf("second Generate failed: %v", err)
	}
	secondOut := second.String()
	if !strings.Contains(secondOut, "command = zig cc -c $in -o $out $flags -MMD -MF $out.d") {
		t.Fatalf("Expected second generator to use zig cc, got: %s", secondOut)
	}
	if !strings.Contains(secondOut, "build app_arm64: cc_link app_main.o") {
		t.Fatalf("Expected second generator to use arm64 suffix, got: %s", secondOut)
	}
	if strings.Contains(secondOut, "clang") || strings.Contains(secondOut, "app_x86_64") {
		t.Fatalf("Expected second generator output to avoid first generator state, got: %s", secondOut)
	}

	var third bytes.Buffer
	if err := newGenerator("", "").Generate(&third); err != nil {
		t.Fatalf("third Generate failed: %v", err)
	}
	thirdOut := third.String()
	if !strings.Contains(thirdOut, "command = gcc -c $in -o $out $flags -MMD -MF $out.d") {
		t.Fatalf("Expected default compiler after isolated renders, got: %s", thirdOut)
	}
	if strings.Contains(thirdOut, "app_x86_64") || strings.Contains(thirdOut, "app_arm64") {
		t.Fatalf("Expected default generator output to avoid prior arch state, got: %s", thirdOut)
	}
	if got := os.Getenv("MINIBP_CC"); got != "" {
		t.Fatalf("Expected generation to avoid MINIBP_CC mutation, got %q", got)
	}
}

func TestGeneratorCleanTargetUsesBuildOutputs(t *testing.T) {
	g := dag.NewGraph()

	lib := &parser.Module{
		Type: "cc_library",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "mylib"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "src/main.c"},
					&parser.String{Value: "src/util.c"},
				}}},
			},
		},
	}

	headers := &parser.Module{
		Type: "cc_library_headers",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "hdrs"}},
			},
		},
	}

	rules := map[string]BuildRule{
		"cc_library":         &ccLibrary{},
		"cc_library_headers": &ccLibraryHeaders{},
	}

	modules := map[string]*parser.Module{
		"mylib": lib,
		"hdrs":  headers,
	}

	g.AddModule(&dagMockModule{name: "mylib"})
	g.AddModule(&dagMockModule{name: "hdrs"})

	gen := NewGenerator(g, rules, modules)

	var buf bytes.Buffer
	err := gen.Generate(&buf)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "build all: phony") {
		t.Fatalf("Expected all target in output: %s", output)
	}
	if !strings.Contains(output, "build clean: clean") {
		t.Fatalf("Expected clean target in output: %s", output)
	}
	if !strings.Contains(output, "rule clean") {
		t.Fatalf("Expected clean rule in output: %s", output)
	}
}

func TestWriterSubninja(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Subninja("path/to/other.ninja")
	output := buf.String()
	if !strings.Contains(output, "subninja path/to/other.ninja") {
		t.Errorf("Expected subninja directive, got: %s", output)
	}
}

func TestWriterInclude(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Include("rules.ninja")
	output := buf.String()
	if !strings.Contains(output, "include rules.ninja") {
		t.Errorf("Expected include directive, got: %s", output)
	}
}

func TestWriterPhony(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Phony("mylib.a", []string{"mylib.o", "helper.o"})
	output := buf.String()
	if !strings.Contains(output, "build mylib.a: phony mylib.o helper.o") {
		t.Errorf("Expected phony target, got: %s", output)
	}
}

func TestWriterDefault(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Default([]string{"app", "libfoo.a"})
	output := buf.String()
	if !strings.Contains(output, "default app libfoo.a") {
		t.Errorf("Expected default targets, got: %s", output)
	}
}

func TestWriterBuildWithVars(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.BuildWithVars("out.o", "cc", []string{"in.c"}, []string{"generated.h"}, map[string]string{
		"flags": "-Wall -O2",
	})
	output := buf.String()
	if !strings.Contains(output, "build out.o: cc in.c || generated.h") {
		t.Errorf("Expected build edge with order-only deps, got: %s", output)
	}
	if !strings.Contains(output, "flags = -Wall -O2") {
		t.Errorf("Expected variable in build edge, got: %s", output)
	}
}

func TestProtoLibraryRule(t *testing.T) {
	r := &protoLibraryRule{}
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

	outs := r.Outputs(m, DefaultRuleRenderContext())
	if len(outs) != 2 || outs[0] != "api.pb.h" || outs[1] != "api.pb.cc" {
		t.Errorf("Expected [api.pb.h, api.pb.cc], got %v", outs)
	}

	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "protoc") {
		t.Errorf("Expected protoc in edge, got: %s", edge)
	}
	if !strings.Contains(edge, "api.proto") {
		t.Errorf("Expected api.proto in edge, got: %s", edge)
	}
	if !strings.Contains(edge, "out_type = cc") {
		t.Errorf("Expected out_type = cc in edge, got: %s", edge)
	}
}

func TestProtoLibraryGoOutput(t *testing.T) {
	r := &protoLibraryRule{}
	m := &parser.Module{
		Type: "proto_library",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "myproto"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "api.proto"},
				}}},
				{Name: "out", Value: &parser.String{Value: "go"}},
			},
		},
	}

	outs := r.Outputs(m, DefaultRuleRenderContext())
	if len(outs) != 1 || outs[0] != "api.pb.go" {
		t.Errorf("Expected [api.pb.go], got %v", outs)
	}
}

func TestProtoLibraryJavaOutput(t *testing.T) {
	r := &protoLibraryRule{}
	m := &parser.Module{
		Type: "proto_library",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "myproto"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "api.proto"},
				}}},
				{Name: "out", Value: &parser.String{Value: "java"}},
			},
		},
	}

	outs := r.Outputs(m, DefaultRuleRenderContext())
	if len(outs) != 1 || outs[0] != "api.java" {
		t.Errorf("Expected [api.java], got %v", outs)
	}
}

func TestProtoLibraryWithPlugins(t *testing.T) {
	r := &protoLibraryRule{}
	m := &parser.Module{
		Type: "proto_library",
		Map: &parser.Map{
			Properties: []*parser.Property{
				{Name: "name", Value: &parser.String{Value: "myproto"}},
				{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "api.proto"},
				}}},
				{Name: "plugins", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "protoc-gen-go"},
				}}},
				{Name: "proto_paths", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "src/proto"},
				}}},
			},
		},
	}

	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "--plugin=protoc-gen-go") {
		t.Errorf("Expected plugin flag in edge, got: %s", edge)
	}
	if !strings.Contains(edge, "--proto_path=src/proto") {
		t.Errorf("Expected proto_path in edge, got: %s", edge)
	}
}

func TestCCSharedLibraryIncludesSharedDeps(t *testing.T) {
	r := &ccLibrary{}
	m := &parser.Module{
		Type: "cc_library",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "shared", Value: &parser.Bool{Value: true}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "app.c"}}}},
			{Name: "shared_libs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: ":base"}}}},
		}},
	}

	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "build libapp.so: cc_shared app_app.o libbase.so") {
		t.Fatalf("Expected shared library input dependency in edge, got: %s", edge)
	}
	if !strings.Contains(edge, "-lbase") {
		t.Fatalf("Expected linker flag for shared dep, got: %s", edge)
	}
}

func TestCCLibraryObjectOutputsAreUniqueForDuplicateBasenames(t *testing.T) {
	r := &ccLibrary{}
	m := &parser.Module{
		Type: "cc_library",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "dupes"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "src/foo/util.c"},
				&parser.String{Value: "tests/foo/util.c"},
			}}},
		}},
	}

	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "build dupes_src_foo_util.o: cc_compile src/foo/util.c") {
		t.Fatalf("Expected unique object output for first source, got: %s", edge)
	}
	if !strings.Contains(edge, "build dupes_tests_foo_util.o: cc_compile tests/foo/util.c") {
		t.Fatalf("Expected unique object output for second source, got: %s", edge)
	}
	if !strings.Contains(edge, "build libdupes.a: cc_archive dupes_src_foo_util.o dupes_tests_foo_util.o") {
		t.Fatalf("Expected archive to use both unique object outputs, got: %s", edge)
	}
}

func TestCCObjectMultiSourceProducesOneOutputPerSource(t *testing.T) {
	r := &ccObject{}
	m := &parser.Module{
		Type: "cc_object",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "bundle"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "src/foo.c"},
				&parser.String{Value: "src/bar.c"},
			}}},
		}},
	}

	outputs := r.Outputs(m, DefaultRuleRenderContext())
	if len(outputs) != 2 || outputs[0] != "bundle_src_foo.o" || outputs[1] != "bundle_src_bar.o" {
		t.Fatalf("Expected one object output per source, got: %v", outputs)
	}

	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "build bundle_src_foo.o: cc_compile src/foo.c") {
		t.Fatalf("Expected compile edge for first source, got: %s", edge)
	}
	if !strings.Contains(edge, "build bundle_src_bar.o: cc_compile src/bar.c") {
		t.Fatalf("Expected compile edge for second source, got: %s", edge)
	}
	if strings.Contains(edge, "cc_compile src/foo.c src/bar.c") {
		t.Fatalf("Expected separate compile edges for multi-source cc_object, got: %s", edge)
	}
}

func TestGeneratorAddsIncludesFromSharedLibs(t *testing.T) {
	g := dag.NewGraph()

	base := &parser.Module{
		Type: "cc_library_shared",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "base"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "base.c"},
			}}},
			{Name: "export_include_dirs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "include/base"},
			}}},
			{Name: "exported_headers", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "generated/base/api.h"},
			}}},
		}},
	}

	app := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "app.c"},
			}}},
			{Name: "shared_libs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: ":base"},
			}}},
		}},
	}

	rules := map[string]BuildRule{
		"cc_library_shared": &ccLibraryShared{},
		"cc_binary":         &ccBinary{},
	}
	modules := map[string]*parser.Module{
		"base": base,
		"app":  app,
	}

	g.AddModule(&dagMockModule{name: "base"})
	g.AddModule(&dagMockModule{name: "app"})
	g.AddEdge("app", "base")

	gen := NewGenerator(g, rules, modules)

	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "build app_app.o: cc_compile app.c") {
		t.Fatalf("Expected app compile edge in output, got: %s", output)
	}
	if !strings.Contains(output, "-Iinclude/base") {
		t.Fatalf("Expected shared lib export include dir in output, got: %s", output)
	}
	if !strings.Contains(output, "-Igenerated/base") {
		t.Fatalf("Expected shared lib exported header dir in output, got: %s", output)
	}
	if strings.Contains(output, "build app: cc_link app_app.o libbase.so\n flags = -lbase -Iinclude/base") ||
		strings.Contains(output, "build app: cc_link app_app.o libbase.so\n flags = -lbase -Igenerated/base") {
		t.Fatalf("Expected shared library include paths to stay on compile edge, got: %s", output)
	}
}

func TestCCBinarySeparatesCompileAndLinkFlags(t *testing.T) {
	r := &ccBinary{}
	m := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "app.c"}}}},
			{Name: "cflags", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "-O2"}}}},
			{Name: "ldflags", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "-pthread"}}}},
			{Name: "shared_libs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: ":base"}}}},
		}},
	}

	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "build app_app.o: cc_compile app.c\n flags = -O2\n") {
		t.Fatalf("Expected compile edge to use only cflags, got: %s", edge)
	}
	if !strings.Contains(edge, "build app: cc_link app_app.o libbase.so\n flags = -pthread -lbase\n") {
		t.Fatalf("Expected link edge to use only ldflags plus shared libs, got: %s", edge)
	}
	if strings.Contains(edge, "cc_link app_app.o libbase.so\n flags = -O2") {
		t.Fatalf("Expected cflags to be excluded from link edge, got: %s", edge)
	}
}

func TestGeneratorAppliesToolchainFlagsToCAndCppEdges(t *testing.T) {
	g := dag.NewGraph()
	ccModule := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "capp"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "app.c"}}}},
			{Name: "cflags", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "-O2"}}}},
			{Name: "ldflags", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "-pthread"}}}},
		}},
	}
	cppModule := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "cppapp"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "app.cpp"}}}},
			{Name: "cflags", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "-fPIC"}}}},
			{Name: "cppflags", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "-std=c++20"}}}},
			{Name: "ldflags", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "-Wl,--as-needed"}}}},
		}},
	}
	rules := map[string]BuildRule{
		"cc_binary":  &ccBinary{},
		"cc_library": &ccLibrary{},
		"cc_object":  &ccObject{},
		"cc_archive": &ccLibraryStatic{},
		"cc_link":    &ccBinary{},
	}
	modules := map[string]*parser.Module{
		"capp":   ccModule,
		"cppapp": cppModule,
	}

	g.AddModule(&dagMockModule{name: "capp"})
	g.AddModule(&dagMockModule{name: "cppapp"})

	gen := NewGenerator(g, rules, modules)
	gen.SetToolchain(Toolchain{
		CC:      "gcc",
		CXX:     "g++",
		AR:      "ar",
		CFlags:  []string{"-DGLOBAL"},
		LdFlags: []string{"-Wl,--gc-sections"},
	})

	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "build capp_app.o: cc_compile app.c\n flags = -DGLOBAL -O2") {
		t.Fatalf("Expected cc compile edge to include global and module cflags, got: %s", output)
	}
	if !strings.Contains(output, "build capp: cc_link capp_app.o\n flags = -Wl,--gc-sections -pthread") {
		t.Fatalf("Expected cc link edge to include global and module ldflags, got: %s", output)
	}
	if strings.Contains(output, "build capp: cc_link capp_app.o\n flags = -DGLOBAL") {
		t.Fatalf("Expected cc compile flags to be excluded from link edge, got: %s", output)
	}
	// Check that C files use gcc compiler
	if !strings.Contains(output, "CC = gcc\nbuild capp:") {
		t.Fatalf("Expected C binary to use gcc compiler, got: %s", output)
	}
	// Check that C++ files use g++ compiler
	if !strings.Contains(output, "CC = g++\nbuild cppapp:") {
		t.Fatalf("Expected C++ binary to use g++ compiler, got: %s", output)
	}
	if !strings.Contains(output, "build cppapp_app.o: cc_compile app.cpp\n flags = -DGLOBAL -fPIC -std=c++20") {
		t.Fatalf("Expected cc compile edge to include global cflags, module cflags, and cppflags, got: %s", output)
	}
	if !strings.Contains(output, "build cppapp: cc_link cppapp_app.o\n flags = -Wl,--gc-sections -Wl,--as-needed") {
		t.Fatalf("Expected cc link edge to include global and module ldflags, got: %s", output)
	}
	if strings.Contains(output, "build cppapp: cc_link cppapp_app.o\n flags = -DGLOBAL") {
		t.Fatalf("Expected cc compile flags to be excluded from link edge, got: %s", output)
	}
}

func TestGeneratorDeduplicatesCustomRulesForSameCommand(t *testing.T) {
	g := dag.NewGraph()

	modA := &parser.Module{Type: "custom", Map: &parser.Map{Properties: []*parser.Property{
		{Name: "name", Value: &parser.String{Value: "a"}},
		{Name: "outs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "a.out"}}}},
		{Name: "cmd", Value: &parser.String{Value: "touch $out"}},
	}}}
	modB := &parser.Module{Type: "custom", Map: &parser.Map{Properties: []*parser.Property{
		{Name: "name", Value: &parser.String{Value: "b"}},
		{Name: "outs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "b.out"}}}},
		{Name: "cmd", Value: &parser.String{Value: "touch $out"}},
	}}}

	rules := map[string]BuildRule{"custom": &customRule{}}
	modules := map[string]*parser.Module{"a": modA, "b": modB}

	g.AddModule(&dagMockModule{name: "a"})
	g.AddModule(&dagMockModule{name: "b"})

	gen := NewGenerator(g, rules, modules)
	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if strings.Count(output, "rule custom_command") != 1 {
		t.Fatalf("Expected one shared custom rule definition, got output: %s", output)
	}
	if strings.Count(output, "build a.out: custom_command") != 1 || strings.Count(output, "build b.out: custom_command") != 1 {
		t.Fatalf("Expected both build edges to reuse shared custom rule, got output: %s", output)
	}
	if !strings.Contains(output, " cmd = touch a.out") || !strings.Contains(output, " cmd = touch b.out") {
		t.Fatalf("Expected per-edge custom commands, got output: %s", output)
	}
}

func TestJavaLibraryEdgeIncludesDependencyClasspathAndJarInput(t *testing.T) {
	r := &javaLibrary{}
	m := &parser.Module{
		Type: "java_library",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "src/com/example/App.java"},
			}}},
			{Name: "deps", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: ":libjava"},
			}}},
		}},
	}
	dep := &parser.Module{
		Type: "java_library",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "libjava"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "src/com/example/Util.java"},
			}}},
		}},
	}

	g := dag.NewGraph()
	rules := map[string]BuildRule{"java_library": r}
	modules := map[string]*parser.Module{"app": m, "libjava": dep}
	gen := NewGenerator(g, rules, modules)

	edge := gen.addJavaDepsToEdge(m, r.NinjaEdge(m, DefaultRuleRenderContext()))
	classpath := "libjava.jar"
	if os.PathListSeparator != ':' {
		classpath = strings.ReplaceAll(classpath, ":", string(os.PathListSeparator))
	}
	if !strings.Contains(edge, "flags =  -classpath "+classpath) {
		t.Fatalf("Expected javac flags to include dependency classpath, got: %s", edge)
	}
	if !strings.Contains(edge, "build app.stamp: javac_lib src/com/example/App.java | libjava.jar") {
		t.Fatalf("Expected javac edge to depend on dependency jar, got: %s", edge)
	}
	if !strings.Contains(edge, "build app.jar: jar_create app.stamp | libjava.jar") {
		t.Fatalf("Expected jar edge to depend on dependency jar, got: %s", edge)
	}
}

func TestGeneratorPreservesJavaJarDependencyPaths(t *testing.T) {
	g := dag.NewGraph()

	lib := &parser.Module{
		Type: "java_library",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "libjava"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "src/com/example/Util.java"},
			}}},
		}},
	}
	app := &parser.Module{
		Type: "java_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "javapp"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "src/com/example/Main.java"},
			}}},
			{Name: "main_class", Value: &parser.String{Value: "com.example.Main"}},
			{Name: "deps", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: ":libjava"},
			}}},
		}},
	}

	rules := map[string]BuildRule{
		"java_library": &javaLibrary{},
		"java_binary":  &javaBinary{},
	}
	modules := map[string]*parser.Module{
		"libjava": lib,
		"javapp":  app,
	}

	g.AddModule(&dagMockModule{name: "libjava"})
	g.AddModule(&dagMockModule{name: "javapp"})
	g.AddEdge("javapp", "libjava")

	gen := NewGenerator(g, rules, modules)
	gen.SetPathPrefix("examples/")

	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "build javapp.stamp: javac_bin examples/src/com/example/Main.java | libjava.jar") {
		t.Fatalf("Expected generated javac edge to retain jar dependency path, got: %s", output)
	}
	if strings.Contains(output, "examples/libjava.jar") {
		t.Fatalf("Expected generated jar dependency to remain an output path, got: %s", output)
	}
	if !strings.Contains(output, "-classpath libjava.jar") {
		t.Fatalf("Expected generated javac flags to include dependency classpath, got: %s", output)
	}
}

func TestGeneratorRewritesExplicitRelativeOutputsForExternalNinja(t *testing.T) {
	g := dag.NewGraph()

	m := &parser.Module{Type: "custom", Map: &parser.Map{Properties: []*parser.Property{
		{Name: "name", Value: &parser.String{Value: "copy_assets"}},
		{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
			&parser.String{Value: "assets/config.json"},
		}}},
		{Name: "outs", Value: &parser.List{Values: []parser.Expression{
			&parser.String{Value: "out/config.json"},
		}}},
		{Name: "cmd", Value: &parser.String{Value: "cp $in $out"}},
	}}}

	rules := map[string]BuildRule{"custom": &customRule{}}
	modules := map[string]*parser.Module{"copy_assets": m}

	g.AddModule(&dagMockModule{name: "copy_assets"})

	gen := NewGenerator(g, rules, modules)
	gen.SetPathPrefix("examples/")

	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "build examples/out/config.json: custom_command examples/assets/config.json") {
		t.Fatalf("Expected explicit relative output and input paths to be rewritten for external ninja, got: %s", output)
	}
	if !strings.Contains(output, "cmd = cp assets/config.json out/config.json") {
		t.Fatalf("Expected custom command payload to remain source-relative, got: %s", output)
	}
}

func TestGeneratorEscapesRewrittenPathsWithSpacesAndSpecialCharacters(t *testing.T) {
	g := dag.NewGraph()

	m := &parser.Module{Type: "custom", Map: &parser.Map{Properties: []*parser.Property{
		{Name: "name", Value: &parser.String{Value: "copy_assets"}},
		{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
			&parser.String{Value: "assets dir/input$file:name.txt"},
		}}},
		{Name: "outs", Value: &parser.List{Values: []parser.Expression{
			&parser.String{Value: "out dir/file:name$.txt"},
		}}},
		{Name: "cmd", Value: &parser.String{Value: "cp $in $out"}},
	}}}

	rules := map[string]BuildRule{"custom": &customRule{}}
	modules := map[string]*parser.Module{"copy_assets": m}

	g.AddModule(&dagMockModule{name: "copy_assets"})

	gen := NewGenerator(g, rules, modules)
	gen.SetPathPrefix("examples/")

	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "build examples/out$ dir/file$:name$$.txt: custom_command examples/assets$ dir/input$$file$:name.txt") {
		t.Fatalf("Expected rewritten build edge to preserve and escape paths, got: %s", output)
	}
	if !strings.Contains(output, "cmd = cp assets dir/input$file:name.txt out dir/file:name$.txt") {
		t.Fatalf("Expected custom command payload to remain unescaped and source-relative, got: %s", output)
	}
	if strings.Contains(output, "build examples/out dir/file:name$.txt: custom_command examples/assets dir/input$file:name.txt") {
		t.Fatalf("Expected build edge paths to be ninja-escaped, got: %s", output)
	}
}

func TestGeneratorPreservesNativeLinkInputsForExternalNinja(t *testing.T) {
	g := dag.NewGraph()

	base := &parser.Module{Type: "cc_library_shared", Map: &parser.Map{Properties: []*parser.Property{
		{Name: "name", Value: &parser.String{Value: "base"}},
		{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
			&parser.String{Value: "base.c"},
		}}},
	}}}
	app := &parser.Module{Type: "cc_binary", Map: &parser.Map{Properties: []*parser.Property{
		{Name: "name", Value: &parser.String{Value: "app"}},
		{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
			&parser.String{Value: "app.c"},
		}}},
		{Name: "shared_libs", Value: &parser.List{Values: []parser.Expression{
			&parser.String{Value: ":base"},
		}}},
	}}}

	rules := map[string]BuildRule{
		"cc_library_shared": &ccLibraryShared{},
		"cc_binary":         &ccBinary{},
	}
	modules := map[string]*parser.Module{
		"base": base,
		"app":  app,
	}

	g.AddModule(&dagMockModule{name: "base"})
	g.AddModule(&dagMockModule{name: "app"})
	g.AddEdge("app", "base")

	gen := NewGenerator(g, rules, modules)
	gen.SetSourceDir("examples")
	gen.SetOutputDir("out")
	gen.SetPathPrefix("../examples/")

	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "build app: cc_link app_app.o ../examples/libbase.so") {
		t.Fatalf("Expected shared library link input to be rewritten for external ninja, got: %s", output)
	}
	if strings.Contains(output, "build app: cc_link app_app.o libbase.so") {
		t.Fatalf("Expected external ninja to avoid source-tree-relative shared library inputs, got: %s", output)
	}
	if !strings.Contains(output, "build libbase.so: cc_shared base_base.o") {
		t.Fatalf("Expected generated shared library output to remain local build output, got: %s", output)
	}
}

func TestApplyDefaultsMergesListsAdditively(t *testing.T) {
	defaults := &parser.Module{
		Type: "defaults",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "my_defaults"}},
			{Name: "cflags", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "-Wall"},
			}}},
		}},
	}
	target := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "defaults", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "my_defaults"},
			}}},
			{Name: "cflags", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "-O2"},
			}}},
		}},
	}
	modules := map[string]*parser.Module{
		"my_defaults": defaults,
	}
	ApplyDefaults(target, modules)

	cflags := GetListProp(target, "cflags")
	if len(cflags) != 2 {
		t.Fatalf("Expected 2 cflags after additive merge, got %d: %v", len(cflags), cflags)
	}
	if cflags[0] != "-O2" || cflags[1] != "-Wall" {
		t.Fatalf("Expected ['-O2' '-Wall'] (target first, then defaults), got %v", cflags)
	}
}

func TestPhonyRuleGeneratesEdge(t *testing.T) {
	r := &phonyRule{}
	m := &parser.Module{
		Type: "phony",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "all"}},
			{Name: "deps", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: ":app"},
				&parser.String{Value: ":lib"},
			}}},
		}},
	}
	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "build all: phony app lib") {
		t.Fatalf("Expected phony edge with deps, got: %s", edge)
	}
}

func TestShBinaryHostRule(t *testing.T) {
	r := &shBinaryHostRule{}
	m := &parser.Module{
		Type: "sh_binary_host",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "run_tests"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "scripts/run_tests.sh"},
			}}},
		}},
	}
	outs := r.Outputs(m, DefaultRuleRenderContext())
	if len(outs) != 1 || outs[0] != "run_tests.sh" {
		t.Fatalf("Expected [run_tests.sh], got %v", outs)
	}
	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "build run_tests.sh: sh_copy scripts/run_tests.sh") {
		t.Fatalf("Expected sh_copy edge, got: %s", edge)
	}
}

func TestPythonBinaryHostRule(t *testing.T) {
	r := &pythonBinaryHostRule{}
	m := &parser.Module{
		Type: "python_binary_host",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "tool"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "tools/main.py"},
			}}},
		}},
	}
	outs := r.Outputs(m, DefaultRuleRenderContext())
	if len(outs) != 1 || outs[0] != "tool.py" {
		t.Fatalf("Expected [tool.py], got %v", outs)
	}
}

func TestCCTestRule(t *testing.T) {
	r := &ccTestRule{}
	m := &parser.Module{
		Type: "cc_test",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "foo_test"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "foo_test.c"},
			}}},
		}},
	}
	outs := r.Outputs(m, DefaultRuleRenderContext())
	if len(outs) != 1 || outs[0] != "foo_test.test" {
		t.Fatalf("Expected [foo_test.test], got %v", outs)
	}
	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "build foo_test.test: cc_link foo_test_foo_test.o") {
		t.Fatalf("Expected cc_test link edge, got: %s", edge)
	}
}

func TestCCTestRuleIncludesTestOptionsArgs(t *testing.T) {
	r := &ccTestRule{}
	m := &parser.Module{
		Type: "cc_test",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "foo_test"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "foo_test.c"},
			}}},
			{Name: "test_options", Value: &parser.Map{Properties: []*parser.Property{
				{Name: "args", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "--gtest_filter=Foo.*"},
				}}},
			}}},
		}},
	}
	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "test_args = --gtest_filter=Foo.*") {
		t.Fatalf("Expected cc_test test_args, got: %s", edge)
	}
}

func TestJavaTestIncludesTestOptionsArgs(t *testing.T) {
	r := &javaTest{}
	m := &parser.Module{
		Type: "java_test",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "foo_test"}},
			{Name: "srcs", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "FooTest.java"},
			}}},
			{Name: "test_options", Value: &parser.Map{Properties: []*parser.Property{
				{Name: "args", Value: &parser.List{Values: []parser.Expression{
					&parser.String{Value: "--tests=FooTest"},
				}}},
			}}},
		}},
	}
	edge := r.NinjaEdge(m, DefaultRuleRenderContext())
	if !strings.Contains(edge, "test_args = --tests=FooTest") {
		t.Fatalf("Expected java_test test_args, got: %s", edge)
	}
}

func TestApplyDefaultsAddsMissingProps(t *testing.T) {
	defaults := &parser.Module{
		Type: "defaults",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "my_defaults"}},
			{Name: "cflags", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "-Wall"},
			}}},
			{Name: "ldflags", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "-lm"},
			}}},
		}},
	}
	target := &parser.Module{
		Type: "cc_binary",
		Map: &parser.Map{Properties: []*parser.Property{
			{Name: "name", Value: &parser.String{Value: "app"}},
			{Name: "defaults", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "my_defaults"},
			}}},
			{Name: "cflags", Value: &parser.List{Values: []parser.Expression{
				&parser.String{Value: "-O2"},
			}}},
		}},
	}
	modules := map[string]*parser.Module{
		"my_defaults": defaults,
	}
	ApplyDefaults(target, modules)

	cflags := GetListProp(target, "cflags")
	if len(cflags) != 2 {
		t.Fatalf("Expected 2 cflags, got %d: %v", len(cflags), cflags)
	}
	ldflags := GetListProp(target, "ldflags")
	if len(ldflags) != 1 || ldflags[0] != "-lm" {
		t.Fatalf("Expected ldflags ['-lm'] from defaults, got %v", ldflags)
	}
}

func TestModuleReferenceParsesNamespacedRef(t *testing.T) {
	ref := ParseModuleReference("//vendor/acme:libfoo")
	if ref == nil || !ref.IsModuleRef {
		t.Fatal("Expected namespaced module reference")
	}
	if ref.ModuleName != "libfoo" {
		t.Fatalf("Expected module name 'libfoo', got '%s'", ref.ModuleName)
	}
}

func TestGeneratorSeparatesCustomRulesForDifferentCommands(t *testing.T) {
	g := dag.NewGraph()

	modA := &parser.Module{Type: "custom", Map: &parser.Map{Properties: []*parser.Property{
		{Name: "name", Value: &parser.String{Value: "a"}},
		{Name: "outs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "a.out"}}}},
		{Name: "cmd", Value: &parser.String{Value: "touch $out"}},
	}}}
	modB := &parser.Module{Type: "custom", Map: &parser.Map{Properties: []*parser.Property{
		{Name: "name", Value: &parser.String{Value: "b"}},
		{Name: "outs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "b.out"}}}},
		{Name: "cmd", Value: &parser.String{Value: "cp in out"}},
	}}}

	rules := map[string]BuildRule{"custom": &customRule{}}
	modules := map[string]*parser.Module{"a": modA, "b": modB}

	g.AddModule(&dagMockModule{name: "a"})
	g.AddModule(&dagMockModule{name: "b"})

	gen := NewGenerator(g, rules, modules)
	var buf bytes.Buffer
	if err := gen.Generate(&buf); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := buf.String()
	if strings.Count(output, "rule custom_command") != 1 {
		t.Fatalf("Expected one shared custom rule definition, got output: %s", output)
	}
	if !strings.Contains(output, " cmd = touch a.out") || !strings.Contains(output, " cmd = cp in out") {
		t.Fatalf("Expected distinct per-edge commands, got output: %s", output)
	}
}
