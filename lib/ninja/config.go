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
type configGen struct{}

// Name returns the module type name for config generation.
func (r *configGen) Name() string { return "config_gen" }

// NinjaRule defines the ninja rule for config file generation.
func (r *configGen) NinjaRule(ctx RuleRenderContext) string {
	return `rule config_gen
 command = sed ${sed_args} ${in} > ${out}
 description = Generating ${out}
`
}

// Outputs returns the output file paths for config generation.
func (r *configGen) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	configfiles := GetListProp(m, "configfiles")
	if len(configfiles) == 0 {
		return nil
	}
	configdir := GetStringProp(m, "configdir")
	if configdir == "" {
		configdir = "${builddir}/include"
	}

	var outputs []string
	for _, configfile := range configfiles {
		// Get the filename from the path
		// configfile is the template (e.g., "config.h.in")
		// output is the generated file (e.g., "config.h")
		filename := strings.TrimSuffix(configfile, ".in")
		if filename == configfile {
			// No .in suffix, use the same name
			filename = configfile
		}
		// Prepend configdir
		if configdir != "" {
			filename = fmt.Sprintf("%s/%s", configdir, filename)
		}
		outputs = append(outputs, filename)
	}
	return outputs
}

// NinjaEdge generates ninja build edges for config file generation.
func (r *configGen) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	configfiles := GetListProp(m, "configfiles")
	if len(configfiles) == 0 {
		return ""
	}

	configvars := GetListProp(m, "configvars")
	configdir := GetStringProp(m, "configdir")
	if configdir == "" {
		configdir = "${builddir}/include"
	}

	var edges strings.Builder

	outputs := r.Outputs(m, ctx)
	for i, configfile := range configfiles {
		if i >= len(outputs) {
			break
		}
		out := outputs[i]

		// Build sed arguments for variable substitution
		var sedArgs []string

		// Add builtin variables
		// TODO: Add OS, VERSION, VERSION_MAJOR, etc.

		// Add configvars
		for _, varName := range configvars {
			// Get the config variable value
			propName := fmt.Sprintf("configvar_%s", varName)
			value := GetStringProp(m, propName)

			// Generate sed command for ${VAR} replacement
			sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${%s}|%s|g'", varName, value))

			// Generate sed command for ${define VAR} replacement
			// In ninja, literal $ must be written as $$, so ${ becomes $${
			if value == "" {
				sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${\\define %s}|/* #undef %s */|g'", varName, varName))
			} else if value == "1" || value == "true" {
				sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${\\define %s}|#define %s 1|g'", varName, varName))
			} else if value == "0" || value == "false" {
				sedArgs = append(sedArgs, fmt.Sprintf("-e 's|$${\\define %s}|/* #define %s 0 */|g'", varName, varName))
			} else {
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
		if moduleName != "" {
			edges.WriteString(fmt.Sprintf("build %s: phony %s\n", moduleName, out))
		}
	}

	return edges.String()
}

// Desc returns a short description of the build action.
func (r *configGen) Desc(m *parser.Module, srcFile string) string {
	return "config_gen"
}
