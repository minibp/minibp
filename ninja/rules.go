// ninja/rules.go - Ninja rule definitions for minibp
// This file defines the BuildRule interface and shared utility functions.
// Individual rule implementations are split into separate files.
package ninja

import (
	"minibp/parser"
	"path/filepath"
	"strings"
)

// BuildRule is the interface for all ninja rule implementations.
type BuildRule interface {
	Name() string
	NinjaRule(ctx RuleRenderContext) string
	NinjaEdge(m *parser.Module, ctx RuleRenderContext) string
	Outputs(m *parser.Module, ctx RuleRenderContext) []string
	Desc(m *parser.Module, srcFile string) string
}

// RuleRenderContext holds the toolchain configuration for rendering rules.
type RuleRenderContext struct {
	CC         string
	CXX        string
	AR         string
	ArchSuffix string
	CFlags     string
	LdFlags    string
}

// DefaultRuleRenderContext returns a RuleRenderContext with default toolchain values.
func DefaultRuleRenderContext() RuleRenderContext {
	return RuleRenderContext{
		CC:  "gcc",
		CXX: "g++",
		AR:  "ar",
	}
}

// GetAllRules returns all available rule implementations.
func GetAllRules() []BuildRule {
	return []BuildRule{
		// C/C++ rules
		&ccLibrary{},
		&ccLibraryStatic{},
		&ccLibraryShared{},
		&ccObject{},
		&ccBinary{},
		&cppLibrary{},
		&cppBinary{},
		&ccLibraryHeaders{},

		// Go rules
		&goLibrary{},
		&goBinary{},
		&goTest{},

		// Java rules
		&javaLibrary{},
		&javaBinary{},
		&javaLibraryStatic{},
		&javaLibraryHost{},
		&javaBinaryHost{},
		&javaTest{},
		&javaImport{},

		// Soong syntax rules
		&defaults{},
		&packageModule{},
		&soongNamespace{},

		// Other rules
		&filegroup{},
		&customRule{},
		&protoLibraryRule{},
		&protoGenRule{},
	}
}

// GetRule returns a rule by name.
func GetRule(name string) BuildRule {
	for _, r := range GetAllRules() {
		if r.Name() == name {
			return r
		}
	}
		return nil
	}
	
	// GetStringProp retrieves a string property value from a module.

func GetStringProp(m *parser.Module, name string) string {

	if m.Map == nil {

		return ""

	}

	for _, prop := range m.Map.Properties {

		if prop.Name == name {

			if s, ok := prop.Value.(*parser.String); ok {

				return s.Value

			}

		}

	}

	return ""

}

// GetStringPropEval retrieves a string property value with optional evaluation.

func GetStringPropEval(m *parser.Module, name string, eval *parser.Evaluator) string {

	if m.Map == nil {

		return ""

	}

	for _, prop := range m.Map.Properties {

		if prop.Name == name {

			if s, ok := prop.Value.(*parser.String); ok {

				return s.Value

			}

			if eval != nil {

				val := eval.Eval(prop.Value)

				if s, ok := val.(string); ok {

					return s

				}

			}

		}

	}

	return ""

}

// getBoolProp retrieves a boolean property value from a module.

func getBoolProp(m *parser.Module, name string) bool {

	if m.Map == nil {

		return false

	}

	for _, prop := range m.Map.Properties {

		if prop.Name == name {

			if b, ok := prop.Value.(*parser.Bool); ok {

				return b.Value

			}

		}

	}

	return false

}

// GetListProp retrieves a list property value from a module.

func GetListProp(m *parser.Module, name string) []string {

	if m.Map == nil {

		return nil

	}

	for _, prop := range m.Map.Properties {

		if prop.Name == name {

			if l, ok := prop.Value.(*parser.List); ok {

				var result []string

				for _, v := range l.Values {

					if s, ok := v.(*parser.String); ok {

						result = append(result, s.Value)

					}

				}

				return result

			}

		}

	}

	return nil

}

// GetListPropEval retrieves a list property value with optional evaluation.

func GetListPropEval(m *parser.Module, name string, eval *parser.Evaluator) []string {

	if m.Map == nil {

		return nil

	}

	for _, prop := range m.Map.Properties {

		if prop.Name == name {

			if l, ok := prop.Value.(*parser.List); ok {

				return parser.EvalToStringList(l, eval)

			}

		}

	}

	return nil

}

// getCflags retrieves C compiler flags from a module.

func getCflags(m *parser.Module) string {

	return strings.Join(GetListProp(m, "cflags"), " ")

}

// getCppflags retrieves C++ compiler flags from a module.

func getCppflags(m *parser.Module) string {

	return strings.Join(GetListProp(m, "cppflags"), " ")

}

// getLdflags retrieves linker flags from a module.

func getLdflags(m *parser.Module) string {

	return strings.Join(GetListProp(m, "ldflags"), " ")

}

// getGoflags retrieves Go compiler flags from a module.

func getGoflags(m *parser.Module) string {

	return strings.Join(GetListProp(m, "goflags"), " ")

}

// getJavaflags retrieves Java compiler flags from a module.

func getJavaflags(m *parser.Module) string {

	return strings.Join(GetListProp(m, "javaflags"), " ")

}

// getExportIncludeDirs retrieves exported include directories from a module.

func getExportIncludeDirs(m *parser.Module) []string {

	return GetListProp(m, "export_include_dirs")

}

// getExportedHeaders retrieves exported header files from a module.

func getExportedHeaders(m *parser.Module) []string {

	return GetListProp(m, "exported_headers")

}

// getName retrieves the module name from a module.

func getName(m *parser.Module) string {

	return GetStringProp(m, "name")

}

// getSrcs retrieves source file paths from a module.

func getSrcs(m *parser.Module) []string {

	return GetListProp(m, "srcs")

}

// formatSrcs combines source file paths into a single space-separated string.

func formatSrcs(srcs []string) string {

	return strings.Join(srcs, " ")

}

// objectOutputName generates a unique object file name for a source file.

func objectOutputName(moduleName, src string) string {

	clean := filepath.Clean(src)

	clean = strings.TrimPrefix(clean, "./")

	clean = strings.TrimPrefix(clean, "../")

	name := strings.TrimSuffix(clean, filepath.Ext(clean))

	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")

	name = replacer.Replace(name)

	name = strings.Trim(name, "._")

	if name == "" {

		name = "obj"

	}

	return moduleName + "_" + name + ".o"

}

// joinFlags combines multiple flag strings into a single space-separated string.

func joinFlags(parts ...string) string {

	filtered := make([]string, 0, len(parts))

	for _, part := range parts {

		part = strings.TrimSpace(part)

		if part != "" {

			filtered = append(filtered, part)

		}

	}

	return strings.Join(filtered, " ")

}

// libOutputName generates the output name for a library.

func libOutputName(name, archSuffix, ext string) string {

	return "lib" + name + archSuffix + ext

}

// sharedLibOutputName generates the output name for a shared library (.so).

func sharedLibOutputName(name string, archSuffix string) string {

	return libOutputName(name, archSuffix, ".so")

}

// staticLibOutputName generates the output name for a static library (.a).

func staticLibOutputName(name string, archSuffix string) string {

	return libOutputName(name, archSuffix, ".a")

}

// ApplyDefaults applies default properties from defaults modules to a target module.

// It processes the `defaults` property which contains a list of default module names.

// The function merges properties from defaults into the target module, with the

// target's own properties taking precedence.

//

// Parameters:

// - m: The target module to apply defaults to

// - modules: Map of all modules (name -> module)

//

// Example:

//

//	defaults {

//	  name: "my_defaults",

//	  cflags: ["-Wall"],

//	}

//

//	cc_binary {

//	  name: "my_binary",

//	  defaults: ["my_defaults"],

//	  cflags: ["-O2"],  // Final: ["-O2", "-Wall"]

//	}

func ApplyDefaults(m *parser.Module, modules map[string]*parser.Module) {

	if m.Map == nil {

		return

	}

	// Get the list of default module names

	defaultNames := GetListProp(m, "defaults")

	if len(defaultNames) == 0 {

		return

	}

	// Collect properties from each defaults module

	for _, defaultName := range defaultNames {

		defaultName = strings.TrimPrefix(defaultName, ":")

		defaultMod, ok := modules[defaultName]

		if !ok || defaultMod == nil {

			continue

		}

		// Verify this is actually a defaults module

		if defaultMod.Type != "defaults" {

			continue

		}

		// Merge properties from the defaults module

		if defaultMod.Map != nil {

			for _, prop := range defaultMod.Map.Properties {

				// Skip the 'name' and 'defaults' properties

				if prop.Name == "name" || prop.Name == "defaults" {

					continue

				}

				// Check if target module already has this property

				hasProp := false

				for _, targetProp := range m.Map.Properties {

					if targetProp.Name == prop.Name {

						hasProp = true

						break

					}

				}

				// Add the property if not already present

				if !hasProp {

					m.Map.Properties = append(m.Map.Properties, prop)

				}

			}

		}

	}

}

// GetDefaultVisibility retrieves the default_visibility from a package module.

// This is used to set the default visibility for modules in a package.

func GetDefaultVisibility(modules map[string]*parser.Module, packageName string) []string {

	// Look for package module in the given package

	for name, mod := range modules {

		if mod == nil || mod.Type != "package" {

			continue

		}

		// Package modules are named after their package path

		if name == packageName || strings.HasSuffix(name, "/"+packageName) {

			return GetListProp(mod, "default_visibility")

		}

	}

	return nil

}

// GetPackageDefaultVisibility gets the default_visibility for a module based on its package.

// It traverses up the package hierarchy to find the closest ancestor with default_visibility.

func GetPackageDefaultVisibility(modules map[string]*parser.Module, modulePath string) []string {

	// Try to find package module at the same level

	parts := strings.Split(modulePath, "/")

	for i := len(parts); i > 0; i-- {

		packageName := strings.Join(parts[:i], "/")

		if vis := GetDefaultVisibility(modules, packageName); vis != nil {

			return vis

		}

	}

	return nil

}

// ModuleReference represents a reference to another module's outputs.

// It can reference just the module (:module) or a specific tagged output (:module{.tag}).

type ModuleReference struct {
	ModuleName string // The name of the referenced module

	Tag string // Optional tag for specific outputs (e.g., ".doc.zip")

	IsModuleRef bool // True if this is a module reference (starts with ":")

}

// ParseModuleReference parses a module reference string like ":module" or ":module{.tag}".

// It returns a ModuleReference struct with the module name and optional tag.

// Returns nil if the string is not a valid module reference.

func ParseModuleReference(s string) *ModuleReference {

	s = strings.TrimSpace(s)

	if !strings.HasPrefix(s, ":") {

		return nil

	}

	ref := &ModuleReference{IsModuleRef: true}

	s = s[1:] // Remove leading ":"

	// Check for tag syntax: {tag}

	if strings.Contains(s, "{") && strings.HasSuffix(s, "}") {

		parts := strings.SplitN(s, "{", 2)

		ref.ModuleName = parts[0]

		tag := strings.TrimSuffix(parts[1], "}")

		ref.Tag = tag

	} else {

		ref.ModuleName = s

	}

	return ref

}

// ResolveModuleOutputs resolves a module reference to actual output file paths.

// It looks up the module in the provided modules map and returns its outputs.

// Parameters:

// - ref: The parsed module reference

// - modules: Map of all modules (name -> module)

// - ctx: Rule render context for output name generation

//

// Returns:

// - []string: List of output file paths, or nil if module not found

func ResolveModuleOutputs(ref *ModuleReference, modules map[string]*parser.Module, ctx RuleRenderContext) []string {

	if ref == nil || !ref.IsModuleRef {

		return nil

	}

	mod, ok := modules[ref.ModuleName]

	if !ok || mod == nil {

		return nil

	}

	rule := GetRule(mod.Type)

	if rule == nil {

		return nil

	}

	outputs := rule.Outputs(mod, ctx)

	if len(outputs) == 0 {

		return nil

	}

	// If no tag, return all outputs

	if ref.Tag == "" {

		return outputs

	}

	// With tag, filter or transform outputs

	// For now, return the tagged output if it exists

	if ref.Tag == ".stamp" {

		// Special case for .stamp files

		return []string{outputs[0] + ".stamp"}

	}

	// Default: return first output with tag suffix

	return []string{outputs[0] + ref.Tag}

}

// ExpandModuleReferences expands module references in a list of strings.

// It replaces strings like ":module" with actual output paths.

// Parameters:

// - items: List of strings that may contain module references

// - modules: Map of all modules

// - ctx: Rule render context

//

// Returns:

// - []string: Expanded list with module references replaced

func ExpandModuleReferences(items []string, modules map[string]*parser.Module, ctx RuleRenderContext) []string {

	var result []string

	for _, item := range items {

		ref := ParseModuleReference(item)

		if ref != nil {

			// This is a module reference, resolve it

			outputs := ResolveModuleOutputs(ref, modules, ctx)

			result = append(result, outputs...)

		} else {

			// Regular string, keep as-is

			result = append(result, item)

		}

	}

	return result

}

// IsVisibilityPublic checks if a visibility list contains "//visibility:public".

func IsVisibilityPublic(vis []string) bool {

	for _, v := range vis {

		if v == "//visibility:public" {

			return true

		}

	}

	return false

}

// IsVisibilityPrivate checks if a visibility list contains "//visibility:private".

func IsVisibilityPrivate(vis []string) bool {

	for _, v := range vis {

		if v == "//visibility:private" {

			return true

		}

	}

	return false

}

// IsVisibilityOverride checks if a visibility list contains "//visibility:override".

func IsVisibilityOverride(vis []string) bool {

	for _, v := range vis {

		if v == "//visibility:override" {

			return true

		}

	}

	return false

}

// IsValidVisibilityRule checks if a visibility rule is valid.

// Valid rules are:

// - //visibility:public

// - //visibility:private

// - //visibility:override

// - //visibility:legacy_public

// - //visibility:any_partition

// - //package:__pkg__

// - //package:__subpackages__

// - //package (shorthand for //package:__pkg__)

// - :__subpackages__ (shorthand for current package)

func IsValidVisibilityRule(rule string) bool {

	validPrefixes := []string{

		"//visibility:public",

		"//visibility:private",

		"//visibility:override",

		"//visibility:legacy_public",

		"//visibility:any_partition",
	}

	for _, prefix := range validPrefixes {

		if rule == prefix {

			return true

		}

	}

	// Check for package references

	if strings.HasPrefix(rule, "//") && (strings.HasSuffix(rule, ":__pkg__") || strings.HasSuffix(rule, ":__subpackages__")) {

		return true

	}

	// Check for shorthand :__subpackages__

	if strings.HasPrefix(rule, ":") && strings.HasSuffix(rule, ":__subpackages__") {

		return true

	}

	return false

}
