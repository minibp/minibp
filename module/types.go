package module

import (
	"minibp/parser"
)

func extractStringList(ast *parser.Map, key string, eval *parser.Evaluator) []string {
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
					} else if eval != nil {
						val := eval.Eval(v)
						if s, ok := val.(string); ok {
							result = append(result, s)
						}
					}
				}
				return result
			}
		}
	}
	return nil
}

func extractString(ast *parser.Map, key string, eval *parser.Evaluator) string {
	if ast == nil {
		return ""
	}
	for _, prop := range ast.Properties {
		if prop.Name == key {
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

func extractAllProps(ast *parser.Map, eval *parser.Evaluator) map[string]interface{} {
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
				} else if eval != nil {
					val := eval.Eval(item)
					if s, ok := val.(string); ok {
						list = append(list, s)
					}
				}
			}
			props[prop.Name] = list
		default:
			if eval != nil {
				val := eval.Eval(v)
				if val != nil {
					props[prop.Name] = val
				}
			}
		}
	}
	return props
}

func baseModuleFromAST(ast *parser.Module, eval *parser.Evaluator) BaseModule {
	return BaseModule{
		Name_:  extractString(ast.Map, "name", eval),
		Type_:  ast.Type,
		Srcs_:  extractStringList(ast.Map, "srcs", eval),
		Deps_:  extractStringList(ast.Map, "deps", eval),
		Props_: extractAllProps(ast.Map, eval),
	}
}

// ============================================================================
// C/C++ Modules
// ============================================================================

type CCLibrary struct {
	BaseModule
	CFlags   []string
	Includes []string
	LDFlags  []string
}

type CCLibraryFactory struct{}

func (f *CCLibraryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &CCLibrary{
		BaseModule: baseModuleFromAST(ast, eval),
		CFlags:     extractStringList(ast.Map, "cflags", eval),
		Includes:   extractStringList(ast.Map, "includes", eval),
		LDFlags:    extractStringList(ast.Map, "ldflags", eval),
	}
	return m, nil
}

type CCBinary struct {
	BaseModule
	CFlags   []string
	Includes []string
	LDFlags  []string
	Static   bool
}

type CCBinaryFactory struct{}

func (f *CCBinaryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &CCBinary{
		BaseModule: baseModuleFromAST(ast, eval),
		CFlags:     extractStringList(ast.Map, "cflags", eval),
		Includes:   extractStringList(ast.Map, "includes", eval),
		LDFlags:    extractStringList(ast.Map, "ldflags", eval),
	}
	if static, ok := m.BaseModule.Props_["static"].(bool); ok {
		m.Static = static
	}
	return m, nil
}

// ============================================================================
// Go Modules
// ============================================================================

type GoLibrary struct {
	BaseModule
	PackagePath string
	ImportPath  string
}

type GoLibraryFactory struct{}

func (f *GoLibraryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &GoLibrary{
		BaseModule:  baseModuleFromAST(ast, eval),
		PackagePath: extractString(ast.Map, "pkg", eval),
		ImportPath:  extractString(ast.Map, "importpath", eval),
	}
	return m, nil
}

type GoBinary struct {
	BaseModule
	PackagePath string
	ImportPath  string
}

type GoBinaryFactory struct{}

func (f *GoBinaryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &GoBinary{
		BaseModule:  baseModuleFromAST(ast, eval),
		PackagePath: extractString(ast.Map, "pkg", eval),
		ImportPath:  extractString(ast.Map, "importpath", eval),
	}
	return m, nil
}

// ============================================================================
// Java Modules
// ============================================================================

type JavaLibrary struct {
	BaseModule
	PackageName  string
	ResourceDirs []string
}

type JavaLibraryFactory struct{}

func (f *JavaLibraryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &JavaLibrary{
		BaseModule:   baseModuleFromAST(ast, eval),
		PackageName:  extractString(ast.Map, "package", eval),
		ResourceDirs: extractStringList(ast.Map, "resource_dirs", eval),
	}
	return m, nil
}

type JavaBinary struct {
	BaseModule
	PackageName  string
	MainClass    string
	ResourceDirs []string
}

type JavaBinaryFactory struct{}

func (f *JavaBinaryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &JavaBinary{
		BaseModule:   baseModuleFromAST(ast, eval),
		PackageName:  extractString(ast.Map, "package", eval),
		MainClass:    extractString(ast.Map, "main_class", eval),
		ResourceDirs: extractStringList(ast.Map, "resource_dirs", eval),
	}
	return m, nil
}

// ============================================================================
// Proto Modules
// ============================================================================

type ProtoLibrary struct {
	BaseModule
	ProtoSrcs   []string
	ProtoPaths  []string
	Plugins     []string
	OutType     string
	IncludeDirs []string
}

type ProtoLibraryFactory struct{}

func (f *ProtoLibraryFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &ProtoLibrary{
		BaseModule:  baseModuleFromAST(ast, eval),
		ProtoSrcs:   extractStringList(ast.Map, "srcs", eval),
		ProtoPaths:  extractStringList(ast.Map, "proto_paths", eval),
		Plugins:     extractStringList(ast.Map, "plugins", eval),
		OutType:     extractString(ast.Map, "out", eval),
		IncludeDirs: extractStringList(ast.Map, "include_dirs", eval),
	}
	return m, nil
}

type ProtoGen struct {
	BaseModule
	ProtoSrcs   []string
	Plugins     []string
	OutType     string
	IncludeDirs []string
}

type ProtoGenFactory struct{}

func (f *ProtoGenFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &ProtoGen{
		BaseModule:  baseModuleFromAST(ast, eval),
		ProtoSrcs:   extractStringList(ast.Map, "srcs", eval),
		Plugins:     extractStringList(ast.Map, "plugins", eval),
		OutType:     extractString(ast.Map, "out", eval),
		IncludeDirs: extractStringList(ast.Map, "include_dirs", eval),
	}
	return m, nil
}

// ============================================================================
// Custom Module
// ============================================================================

type Custom struct {
	BaseModule
}

type CustomFactory struct{}

func (f *CustomFactory) Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	m := &Custom{
		BaseModule: baseModuleFromAST(ast, eval),
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
	Register("proto_library", &ProtoLibraryFactory{})
	Register("proto_gen", &ProtoGenFactory{})
	Register("custom", &CustomFactory{})
}
