// ninja/helpers.go - Helper functions for ninja rule generation
package ninja

import (
	"minibp/lib/parser"
	"path/filepath"
	"runtime"
	"strings"
)

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

// GetStringPropEval retrieves a string property value with variable evaluation.
func GetStringPropEval(m *parser.Module, name string, eval *parser.Evaluator) string {
	if m.Map == nil {
		return ""
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if s, ok := prop.Value.(*parser.String); ok {
				if eval != nil {
					return parser.EvalToString(s, eval)
				}
				return s.Value
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

// GetListPropEval retrieves a list property value with variable evaluation.
func GetListPropEval(m *parser.Module, name string, eval *parser.Evaluator) []string {
	if m.Map == nil {
		return nil
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if l, ok := prop.Value.(*parser.List); ok {
				if eval != nil {
					return parser.EvalToStringList(l, eval)
				}
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

// getLto retrieves the LTO mode from a module.
func getLto(m *parser.Module) string {
	return GetStringProp(m, "lto")
}

// getLocalIncludeDirs retrieves local include directories from a module.
func getLocalIncludeDirs(m *parser.Module) []string {
	return GetListProp(m, "local_include_dirs")
}

// getSystemIncludeDirs retrieves system include directories from a module.
func getSystemIncludeDirs(m *parser.Module) []string {
	return GetListProp(m, "system_include_dirs")
}

// getGoTargetVariants retrieves target variant keys from a Go module.
func getGoTargetVariants(m *parser.Module) []string {
	if m.Target == nil {
		return nil
	}
	var keys []string
	for _, p := range m.Target.Properties {
		if _, ok := p.Value.(*parser.Map); !ok {
			continue
		}
		keys = append(keys, p.Name)
	}
	return keys
}

// getGoTargetProp extracts a string property from a target variant sub-map.
func getGoTargetProp(m *parser.Module, variant, prop string) string {
	if m.Target == nil {
		return ""
	}
	for _, p := range m.Target.Properties {
		if p.Name != variant {
			continue
		}
		sub, ok := p.Value.(*parser.Map)
		if !ok {
			return ""
		}
		for _, sp := range sub.Properties {
			if sp.Name == prop {
				if s, ok := sp.Value.(*parser.String); ok {
					return s.Value
				}
			}
		}
	}
	return ""
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
	srcName := strings.TrimSuffix(clean, filepath.Ext(clean))
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	srcName = replacer.Replace(srcName)
	srcName = strings.Trim(srcName, "._")
	if srcName == "" {
		srcName = "obj"
	}
	if strings.HasPrefix(srcName, moduleName) || srcName == moduleName {
		return srcName + ".o"
	}
	return moduleName + "_" + srcName + ".o"
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
	libName := name
	if !strings.HasPrefix(name, "lib") {
		libName = "lib" + name
	}
	return libName + archSuffix + ext
}

// sharedLibOutputName generates the output name for a shared library (.so).
func sharedLibOutputName(name string, archSuffix string) string {
	return libOutputName(name, archSuffix, ".so")
}

// staticLibOutputName generates the output name for a static library (.a).
func staticLibOutputName(name string, archSuffix string) string {
	return libOutputName(name, archSuffix, ".a")
}

// getFirstSource retrieves the first source file from a module.
func getFirstSource(m *parser.Module) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return ""
	}
	return srcs[0]
}

// getData retrieves data file paths from a module.
func getData(m *parser.Module) []string {
	return GetListProp(m, "data")
}

// copyCommand returns the platform-specific copy command for ninja.
func copyCommand() string {
	if runtime.GOOS == "windows" {
		return "cmd /c copy $in $out"
	}
	return "cp $in $out"
}

// getTestOptionArgs retrieves test option arguments from a module.
func getTestOptionArgs(m *parser.Module) string {
	return strings.Join(GetMapStringListProp(GetMapProp(m, "test_options"), "args"), " ")
}

// GetMapProp retrieves a map property value from a module.
func GetMapProp(m *parser.Module, name string) *parser.Map {
	if m.Map == nil {
		return nil
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if mp, ok := prop.Value.(*parser.Map); ok {
				return mp
			}
		}
	}
	return nil
}

// GetMapStringListProp retrieves a string list property from a map.
func GetMapStringListProp(mp *parser.Map, name string) []string {
	if mp == nil {
		return nil
	}
	for _, prop := range mp.Properties {
		if prop.Name == name {
			if list, ok := prop.Value.(*parser.List); ok {
				var out []string
				for _, v := range list.Values {
					if s, ok := v.(*parser.String); ok {
						out = append(out, s.Value)
					}
				}
				return out
			}
			if s, ok := prop.Value.(*parser.String); ok {
				return []string{s.Value}
			}
		}
	}
	return nil
}
