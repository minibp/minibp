// module/types.go - Concrete module types
package module

import (
	"minibp/parser"
)

// extractStringList extracts string values from an AST List
func extractStringList(ast *parser.Map, key string) []string {
	if ast == nil {
		return nil
	}
	for _, prop := range ast.Properties {
		if prop.Name == key {
			if list, ok := prop.Value.(*parser.List); ok {
				result := make([]string, 0, len(list.Values))
				for _, v := range list.Values {
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

// extractString extracts a string value from an AST Map
func extractString(ast *parser.Map, key string) string {
	if ast == nil {
		return ""
	}
	for _, prop := range ast.Properties {
		if prop.Name == key {
			if s, ok := prop.Value.(*parser.String); ok {
				return s.Value
			}
		}
	}
	return ""
}

// extractAllProps extracts all properties into a map
func extractAllProps(ast *parser.Map) map[string]interface{} {
	props := make(map[string]interface{})
	if ast == nil {
		return props
	}
	for _, prop := range ast.Properties {
		switch v := prop.Value.(type) {
		case *parser.String:
			props[prop.Name] = v.Value
		case *parser.Int64:
			props[prop.Name] = v.Value
		case *parser.Bool:
			props[prop.Name] = v.Value
		case *parser.List:
			list := make([]string, 0, len(v.Values))
			for _, item := range v.Values {
				if s, ok := item.(*parser.String); ok {
					list = append(list, s.Value)
				}
			}
			props[prop.Name] = list
		}
	}
	return props
}

// baseModuleFromAST creates a BaseModule from AST data
func baseModuleFromAST(ast *parser.Module) BaseModule {
	return BaseModule{
		Name_:  extractString(ast.Map, "name"),
		Type_:  ast.Type,
		Srcs_:  extractStringList(ast.Map, "srcs"),
		Deps_:  extractStringList(ast.Map, "deps"),
		Props_: extractAllProps(ast.Map),
	}
}

// ============================================================================
// C/C++ Modules
// ============================================================================

// CCLibrary represents a C/C++ library module
type CCLibrary struct {
	BaseModule
	CFlags   []string
	Includes []string
	LDFlags  []string
}

// CCLibraryFactory creates CCLibrary modules
type CCLibraryFactory struct{}

func (f *CCLibraryFactory) Create(ast *parser.Module) (Module, error) {
	m := &CCLibrary{
		BaseModule: baseModuleFromAST(ast),
		CFlags:     extractStringList(ast.Map, "cflags"),
		Includes:   extractStringList(ast.Map, "includes"),
		LDFlags:    extractStringList(ast.Map, "ldflags"),
	}
	return m, nil
}

// CCBinary represents a C/C++ binary executable module
type CCBinary struct {
	BaseModule
	CFlags   []string
	Includes []string
	LDFlags  []string
	Static   bool
}

// CCBinaryFactory creates CCBinary modules
type CCBinaryFactory struct{}

func (f *CCBinaryFactory) Create(ast *parser.Module) (Module, error) {
	m := &CCBinary{
		BaseModule: baseModuleFromAST(ast),
		CFlags:     extractStringList(ast.Map, "cflags"),
		Includes:   extractStringList(ast.Map, "includes"),
		LDFlags:    extractStringList(ast.Map, "ldflags"),
	}
	if static, ok := m.BaseModule.Props_["static"].(bool); ok {
		m.Static = static
	}
	return m, nil
}

// ============================================================================
// Go Modules
// ============================================================================

// GoLibrary represents a Go library module
type GoLibrary struct {
	BaseModule
	PackagePath string
	ImportPath  string
}

// GoLibraryFactory creates GoLibrary modules
type GoLibraryFactory struct{}

func (f *GoLibraryFactory) Create(ast *parser.Module) (Module, error) {
	m := &GoLibrary{
		BaseModule:  baseModuleFromAST(ast),
		PackagePath: extractString(ast.Map, "pkg"),
		ImportPath:  extractString(ast.Map, "importpath"),
	}
	return m, nil
}

// GoBinary represents a Go binary executable module
type GoBinary struct {
	BaseModule
	PackagePath string
	ImportPath  string
}

// GoBinaryFactory creates GoBinary modules
type GoBinaryFactory struct{}

func (f *GoBinaryFactory) Create(ast *parser.Module) (Module, error) {
	m := &GoBinary{
		BaseModule:  baseModuleFromAST(ast),
		PackagePath: extractString(ast.Map, "pkg"),
		ImportPath:  extractString(ast.Map, "importpath"),
	}
	return m, nil
}

// ============================================================================
// Java Modules
// ============================================================================

// JavaLibrary represents a Java library module
type JavaLibrary struct {
	BaseModule
	PackageName  string
	ResourceDirs []string
}

// JavaLibraryFactory creates JavaLibrary modules
type JavaLibraryFactory struct{}

func (f *JavaLibraryFactory) Create(ast *parser.Module) (Module, error) {
	m := &JavaLibrary{
		BaseModule:   baseModuleFromAST(ast),
		PackageName:  extractString(ast.Map, "package"),
		ResourceDirs: extractStringList(ast.Map, "resource_dirs"),
	}
	return m, nil
}

// JavaBinary represents a Java binary executable module
type JavaBinary struct {
	BaseModule
	PackageName  string
	MainClass    string
	ResourceDirs []string
}

// JavaBinaryFactory creates JavaBinary modules
type JavaBinaryFactory struct{}

func (f *JavaBinaryFactory) Create(ast *parser.Module) (Module, error) {
	m := &JavaBinary{
		BaseModule:   baseModuleFromAST(ast),
		PackageName:  extractString(ast.Map, "package"),
		MainClass:    extractString(ast.Map, "main_class"),
		ResourceDirs: extractStringList(ast.Map, "resource_dirs"),
	}
	return m, nil
}

// ============================================================================
// Custom Module
// ============================================================================

// Custom represents a user-defined custom module
type Custom struct {
	BaseModule
}

// CustomFactory creates Custom modules
type CustomFactory struct{}

func (f *CustomFactory) Create(ast *parser.Module) (Module, error) {
	m := &Custom{
		BaseModule: baseModuleFromAST(ast),
	}
	return m, nil
}

// ============================================================================
// Initialization
// ============================================================================

func init() {
	Register("cc_library", &CCLibraryFactory{})
	Register("cc_binary", &CCBinaryFactory{})
	Register("go_library", &GoLibraryFactory{})
	Register("go_binary", &GoBinaryFactory{})
	Register("java_library", &JavaLibraryFactory{})
	Register("java_binary", &JavaBinaryFactory{})
	Register("custom", &CustomFactory{})
}
