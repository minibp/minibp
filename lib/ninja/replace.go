// replace.go - File replace rules for minibp.
//
// This file implements file replace rules, similar to
// xmake.sh's `{replace = {SEARCH, REPLACE}}` syntax.
//
// Features:
//   - Parse replace rules from module properties
//   - Generate ninja build edges that create replaced versions of source files
//   - Use sed commands to perform the replacement
//
// Module properties:
//   - replace_rules: List of replace rules in "SEARCH,REPLACE" format
//   - Or embedded in srcs: ["file.c", {replace: {SEARCH: "replacement", ...}}]
//
// Ninja rules generated:
//   - replace_file: Creates a replaced version of a source file
//
// Example Blueprint:
//
//	cc_binary {
//	    name: "test_replace",
//	    srcs: ["test.cpp"],
//	    replace_rules: ["HELLO_REPLACE=hello", "VERSION_REPLACE=1.0.0"],
//	}
package ninja

import (
	"fmt"
	"path/filepath"
	"strings"

	"minibp/lib/parser"
)

// replaceRule implements file replace rule generation.
type replaceRule struct{}

// Name returns the module type name for replace rules.
func (r *replaceRule) Name() string { return "replace_rule" }

// NinjaRule defines the ninja rule for file replacement.
func (r *replaceRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule replace_file
 command = sed ${sed_args} ${in} > ${out}
 description = Replacing ${in}
`
}

// Outputs returns the output file paths for replace rules.
// The output is the replaced version of the source file.
func (r *replaceRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return nil
	}
	// For each source file, generate a replaced version
	// The replaced file is stored in ${builddir}/.replaced/${filename}
	prefix := ctx.PathPrefix
	if prefix == "" {
		prefix = "."
	}
	var outputs []string
	for _, src := range srcs {
		// Get the filename from the path
		// The replaced file will be in ${builddir}/.replaced/
		output := fmt.Sprintf("%s/.replaced/%s", prefix, src)
		outputs = append(outputs, output)
	}
	return outputs
}

// NinjaEdge generates ninja build edges for file replacement.
func (r *replaceRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	srcs := getSrcs(m)
	replaceRules := GetListProp(m, "replace_rules")
	if len(srcs) == 0 || len(replaceRules) == 0 {
		return ""
	}

	var edges strings.Builder

	// Build sed arguments from replace rules
	// Each rule is in "SEARCH=replacement" format
	var sedArgs []string
	for _, rule := range replaceRules {
		// Parse "SEARCH=replacement"
		parts := strings.SplitN(rule, "=", 2)
		if len(parts) == 2 {
			search := parts[0]
			replacement := parts[1]
			sedArgs = append(sedArgs, fmt.Sprintf("-e 's|%s|%s|g'", search, replacement))
		}
	}

	// Generate build edges for each source file
	// Use ${builddir}/.replaced/ for the replaced files
	outs := r.Outputs(m, ctx)
	for i, src := range srcs {
		if i >= len(outs) {
			break
		}
		out := outs[i]
		edges.WriteString(fmt.Sprintf("build %s: replace_file %s\n sed_args = %s\n",
			out, filepath.Join(ctx.PathPrefix, src), strings.Join(sedArgs, " ")))
	}

	return edges.String()
}

// Desc returns a short description of the build action.
func (r *replaceRule) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "replace"
	}
	return "replace"
}
