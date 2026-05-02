// config.go - Config file generation for minibp.
//
// This file implements config file generation from templates, similar to
// xmake.sh's `add_configfiles` and `set_configvar` functionality.
//
// Features:
//   - Read template files (e.g., config.h.in)
//   - Replace ${VAR} with config variable values
//   - Replace ${define VAR} with #define VAR value or /* #undef VAR */
//   - Output generated files to the specified configdir
//
// Module properties:
//   - configfiles: List of template files to process
//   - configdir: Output directory for generated config files
//   - configvars: List of config variable names
//   - configvar_<name>: Value for each config variable
//
// Ninja rules generated:
//   - config_gen: Generates config file from template using sed
//
// Example Blueprint:
//
//	cc_binary {
//	    name: "app",
//	    configfiles: ["config.h.in"],
//	    configdir: "$(builddir)/include",
//	    configvars: ["HAS_PTHREAD", "VERSION"],
//	    configvar_HAS_PTHREAD: "1",
//	    configvar_VERSION: "\"1.0.0\"",
//	}
//

package ninja

import (
	"fmt"
	"path/filepath"
	"strings"

	"minibp/lib/parser"
)

// configGen implements config file generation from templates.
// It processes template files (e.g., config.h.in) and replaces placeholders
// with configuration variable values, generating output files with proper
// define/undef handling for C/C++ header configurations.
//
// This rule type handles the complete workflow of:
//   - Reading template files with ${VAR} and ${define VAR} placeholders
//   - Substituting configuration variables from module properties
//   - Generating appropriate #define or /* #undef */ directives
//   - Writing output to the specified configdir
//
// Fields: (none - stateless rule handler)
type configGen struct{}

// Name returns the module type name for config generation.
// This is used by the build system to identify and register the config_gen rule type.
//
// Returns the string "config_gen" which is the rule type identifier.
// Returns a constant string value (no edge cases).
func (r *configGen) Name() string { return "config_gen" }

// NinjaRule defines the ninja rule for config file generation.
// Defines a ninja rule that uses sed to perform variable substitution
// on template files. The rule reads the template file (${in}), applies
// sed substitution commands (${sed_args}), and writes to the output file (${out}).
//
// Parameters:
//   - ctx: The rule render context (unused for this rule definition)
//
// Returns a ninja rule definition string with sed-based substitution.
// Returns a constant string (no edge cases).
func (r *configGen) NinjaRule(ctx RuleRenderContext) string {
	return `rule config_gen
 command = sed ${sed_args} ${in} > ${out}
 description = Generating ${out}
`
}

// Outputs returns the output file paths for config generation.
// Computes the output file paths by stripping the ".in" suffix from template
// filenames and prepending the configdir. If configdir is not specified,
// defaults to "${builddir}/include".
//
// Parameters:
//   - m: The parser.Module containing configfiles and configdir properties
//   - ctx: The rule render context (unused)
//
// Returns a slice of output file paths for the generated config files.
// Returns nil if no configfiles are specified.
//
// Edge cases:
//   - Empty configfiles list returns nil
//   - Missing .in suffix: uses the template filename as-is
//   - Empty configdir defaults to "${builddir}/include"
//   - configdir path is prepended to all output filenames
func (r *configGen) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	configfiles := GetListProp(m, "configfiles")
	if len(configfiles) == 0 { // No template files specified, nothing to generate
		return nil
	}
	configdir := GetStringProp(m, "configdir")
	if configdir == "" { // Use default include directory if not specified
		configdir = "${builddir}/include"
	}

	var outputs []string
	for _, configfile := range configfiles {
		// Get the filename from the path
		// configfile is the template (e.g., "config.h.in")
		// output is the generated file (e.g., "config.h")
		filename := strings.TrimSuffix(configfile, ".in")
		if filename == configfile { // No .in suffix, use the same name
			filename = configfile
		}
		// Prepend configdir
		if configdir != "" { // Add output directory prefix to filename
			filename = fmt.Sprintf("%s/%s", configdir, filename)
		}
		outputs = append(outputs, filename)
	}
	return outputs
}

// NinjaEdge generates ninja build edges for config file generation.
// Creates build rules that use sed to substitute ${VAR} and ${define VAR}
// placeholders in template files. Each config variable is processed to generate
// appropriate #define or /* #undef */ directives based on the variable value.
//
// Parameters:
//   - m: The parser.Module containing configfiles, configvars, and configvar_* properties
//   - ctx: The rule render context providing PathPrefix for input file paths
//
// Returns a string containing ninja build edges for all config file generations.
// Returns empty string if no configfiles are specified.
//
// Edge cases:
//   - Empty configfiles list returns empty string
//   - Mismatch between configfiles and outputs: breaks loop safely
//   - Empty configdir defaults to "${builddir}/include"
//   - Variable values: empty->undef, 1/true->#define 1, 0/false->#define 0 (commented), else->#define value
//   - Fallback regex handles any remaining ${define VAR} not explicitly defined
//
// Notes:
//   - In ninja build files, literal $ must be escaped as $$, so ${ becomes $${
//   - The sed delimiter is | to avoid conflicts with file paths containing /
//   - A phony target is created for the module name for easy reference
func (r *configGen) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	configfiles := GetListProp(m, "configfiles")
	if len(configfiles) == 0 { // No template files to process
		return ""
	}

	configvars := GetListProp(m, "configvars")
	configdir := GetStringProp(m, "configdir")
	if configdir == "" { // Use default include directory if not specified
		configdir = "${builddir}/include"
	}

	var edges strings.Builder

	outputs := r.Outputs(m, ctx)
	for i, configfile := range configfiles {
		if i >= len(outputs) { // Safety check: stop if outputs don't match configfiles
			break
		}
		out := outputs[i]

		// Build sed arguments for variable substitution
		var sedArgs []string

		// Add builtin variables
		// TODO: Add OS, VERSION, VERSION_MAJOR, etc.

		// Add configvars
		for _, varName := range configvars {
			// Get the config variable value from module properties
			propName := fmt.Sprintf("configvar_%s", varName)
			value := GetStringProp(m, propName)

			// Generate sed command for ${VAR} replacement
			sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${%s}|%s|g'", varName, value))

			// Generate sed command for ${define VAR} replacement
			// In ninja, literal $ must be written as $$, so ${ becomes $${
			if value == "" { // Empty value: generate /* #undef VAR */
				sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${\\define %s}|/* #undef %s */|g'", varName, varName))
			} else if value == "1" || value == "true" { // Truthy value: generate #define VAR 1
				sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${\\define %s}|#define %s 1|g'", varName, varName))
			} else if value == "0" || value == "false" { // Falsy value: generate commented #define
				sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${\\define %s}|/* #define %s 0 */|g'", varName, varName))
			} else { // Other value: generate #define VAR value
				sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${\\define %s}|#define %s %s|g'", varName, varName, value))
			}
		}

		// Add fallback: replace any remaining ${define VAR} with /* #undef VAR */
		// In ninja, $${ becomes literal ${ in the output
		sedArgs = append(sedArgs, "-e 's|$${\\define \\([^}]*\\)}|/* #undef \\1 */|g'")

		// Write the build edge for the output file
		// Use full path for input file
		inputFile := filepath.Join(ctx.PathPrefix, configfile)
		edges.WriteString(fmt.Sprintf("build %s: config_gen %s\n sed_args = %s\n", out, inputFile, strings.Join(sedArgs, " ")))

		// Write the phony target for the module name (test_config -> build/include/config.h)
		moduleName := getName(m)
		if moduleName != "" { // Create phony alias for easy reference
			edges.WriteString(fmt.Sprintf("build %s: phony %s\n", moduleName, out))
		}
	}

	return edges.String()
}

// Desc returns a short description of the build action.
// This description is used in ninja build output to identify the current step.
//
// Parameters:
//   - m: The parser.Module (unused for this description)
//   - srcFile: The source file path (unused for this description)
//
// Returns the string "config_gen" identifying this build action.
// Returns a constant string (no edge cases).
func (r *configGen) Desc(m *parser.Module, srcFile string) string {
	return "config_gen"
}
