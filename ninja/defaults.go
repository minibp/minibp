// ninja/defaults.go - Defaults module support for attribute inheritance
package ninja

import (
	"minibp/parser"
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
