// ninja/defaults.go - Defaults module support for attribute inheritance
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"strings"
)

// defaults implements a defaults module that provides reusable property sets.
// Other modules can reference defaults using the `defaults: ["default_name"]` property.
type defaults struct{}

func (r *defaults) Name() string { return "defaults" }

func (r *defaults) NinjaRule(ctx RuleRenderContext) string {
	// Defaults modules don't produce any ninja rules
	return ""
}

func (r *defaults) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Defaults modules don't produce any outputs
	return nil
}

func (r *defaults) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Defaults modules don't produce any build edges
	return ""
}

func (r *defaults) Desc(m *parser.Module, srcFile string) string {
	return ""
}

// packageModule implements a package module that sets default properties for a package.
// Package modules are named after their package path (e.g., "my/package").
type packageModule struct{}

func (r *packageModule) Name() string { return "package" }

func (r *packageModule) NinjaRule(ctx RuleRenderContext) string {
	// Package modules don't produce any ninja rules
	return ""
}

func (r *packageModule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Package modules don't produce any outputs
	return nil
}

func (r *packageModule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Package modules don't produce any build edges
	return ""
}

func (r *packageModule) Desc(m *parser.Module, srcFile string) string {
	return ""
}

// soongNamespace implements a soong_namespace module for namespace management.
// Namespaces help avoid module name conflicts in large projects.
type soongNamespace struct{}

func (r *soongNamespace) Name() string { return "soong_namespace" }

func (r *soongNamespace) NinjaRule(ctx RuleRenderContext) string {
	// Namespace modules don't produce any ninja rules
	return ""
}

func (r *soongNamespace) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	// Namespace modules don't produce any outputs
	return nil
}

func (r *soongNamespace) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	// Namespace modules don't produce any build edges
	return ""
}

func (r *soongNamespace) Desc(m *parser.Module, srcFile string) string {
	return ""
}

// phonyRule implements a phony build target.
// Phony targets don't produce real files but serve as aliases for groups of outputs.
type phonyRule struct{}

func (r *phonyRule) Name() string { return "phony" }

func (r *phonyRule) NinjaRule(ctx RuleRenderContext) string {
	return ""
}

func (r *phonyRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	return nil
}

func (r *phonyRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	deps := GetListProp(m, "deps")
	if name == "" {
		return ""
	}
	var depNames []string
	for _, dep := range deps {
		depNames = append(depNames, strings.TrimPrefix(dep, ":"))
	}
	if len(depNames) > 0 {
		return fmt.Sprintf("build %s: phony %s\n", ninjaEscapePath(name), strings.Join(depNames, " "))
	}
	srcs := getSrcs(m)
	if len(srcs) > 0 {
		return fmt.Sprintf("build %s: phony %s\n", ninjaEscapePath(name), strings.Join(srcs, " "))
	}
	return fmt.Sprintf("build %s: phony\n", ninjaEscapePath(name))
}

func (r *phonyRule) Desc(m *parser.Module, srcFile string) string {
	return "phony"
}

// ccTestRule implements a cc_test build rule.
// It compiles C/C++ test sources and links them into a test binary.
type ccTestRule struct{}

func (r *ccTestRule) Name() string { return "cc_test" }

func (r *ccTestRule) NinjaRule(ctx RuleRenderContext) string {
	return ""
}

func (r *ccTestRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".test" + ctx.ArchSuffix}
}

func (r *ccTestRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	return ccTestEdge(m, ctx)
}

func (r *ccTestRule) Desc(m *parser.Module, srcFile string) string {
	return "cc_test"
}

// shBinaryHostRule implements a host-side shell script binary.
type shBinaryHostRule struct{}

func (r *shBinaryHostRule) Name() string { return "sh_binary_host" }

func (r *shBinaryHostRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule sh_copy
 command = cp $in $out && chmod +x $out
 description = Copy shell script $in
`
}

func (r *shBinaryHostRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".sh"}
}

func (r *shBinaryHostRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	out := name + ".sh"
	return fmt.Sprintf("build %s: sh_copy %s\n", ninjaEscapePath(out), ninjaEscapePath(srcs[0]))
}

func (r *shBinaryHostRule) Desc(m *parser.Module, srcFile string) string {
	return "sh_copy"
}

// pythonBinaryHostRule implements a host-side Python binary.
type pythonBinaryHostRule struct{}

func (r *pythonBinaryHostRule) Name() string { return "python_binary_host" }

func (r *pythonBinaryHostRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule python_copy
 command = cp $in $out && chmod +x $out
 description = Copy Python script $in
`
}

func (r *pythonBinaryHostRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".py"}
}

func (r *pythonBinaryHostRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	out := name + ".py"
	return fmt.Sprintf("build %s: python_copy %s\n", ninjaEscapePath(out), ninjaEscapePath(srcs[0]))
}

func (r *pythonBinaryHostRule) Desc(m *parser.Module, srcFile string) string {
	return "python_copy"
}

// pythonTestHostRule implements a host-side Python test.
type pythonTestHostRule struct{}

func (r *pythonTestHostRule) Name() string { return "python_test_host" }

func (r *pythonTestHostRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule python_test
 command = python3 $in
 description = Run Python test $in
`
}

func (r *pythonTestHostRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".test.py"}
}

func (r *pythonTestHostRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	out := name + ".test.py"
	return fmt.Sprintf("build %s: python_test %s\n", ninjaEscapePath(out), ninjaEscapePath(srcs[0]))
}

func (r *pythonTestHostRule) Desc(m *parser.Module, srcFile string) string {
	return "python_test"
}
