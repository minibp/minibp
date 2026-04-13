// ninja/rules.go - Ninja rule definitions for minibp
package ninja

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"minibp/parser"
)

// BuildRule is the interface for all ninja rule implementations
type BuildRule interface {
	Name() string
	NinjaRule() string
	NinjaEdge(m *parser.Module) string
	Outputs(m *parser.Module) []string
	Desc(m *parser.Module, srcFile string) string
}

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

func getCflags(m *parser.Module) string              { return strings.Join(GetListProp(m, "cflags"), " ") }
func getCppflags(m *parser.Module) string            { return strings.Join(GetListProp(m, "cppflags"), " ") }
func getLdflags(m *parser.Module) string             { return strings.Join(GetListProp(m, "ldflags"), " ") }
func getGoflags(m *parser.Module) string             { return strings.Join(GetListProp(m, "goflags"), " ") }
func getJavaflags(m *parser.Module) string           { return strings.Join(GetListProp(m, "javaflags"), " ") }
func getExportIncludeDirs(m *parser.Module) []string { return GetListProp(m, "export_include_dirs") }
func getExportedHeaders(m *parser.Module) []string   { return GetListProp(m, "exported_headers") }
func getName(m *parser.Module) string                { return GetStringProp(m, "name") }
func getSrcs(m *parser.Module) []string              { return GetListProp(m, "srcs") }
func formatSrcs(srcs []string) string                { return strings.Join(srcs, " ") }

func getCC() string {
	if v := os.Getenv("MINIBP_CC"); v != "" {
		return v
	}
	return "gcc"
}

func getCXX() string {
	if v := os.Getenv("MINIBP_CXX"); v != "" {
		return v
	}
	return "g++"
}

func getAR() string {
	if v := os.Getenv("MINIBP_AR"); v != "" {
		return v
	}
	return "ar"
}

func getArchSuffix() string {
	return os.Getenv("MINIBP_ARCH_SUFFIX")
}

// ============================================================================
// cc_library - C library (static by default, shared if shared: true)
// ============================================================================
type ccLibrary struct{}

func (r *ccLibrary) Name() string { return "cc_library" }
func (r *ccLibrary) NinjaRule() string {
	return fmt.Sprintf(`rule cc_compile
  command = %s -c $in -o $out $flags -MMD -MF $out.d
  depfile = $out.d
  deps = gcc

rule cc_archive
  command = %s rcs $out $in

rule cc_shared
  command = %s -shared -o $out $in $flags
 `, getCC(), getAR(), getCC())
}

type ccLibraryStatic struct{}

func (r *ccLibraryStatic) Name() string { return "cc_library_static" }
func (r *ccLibraryStatic) NinjaRule() string {
	return fmt.Sprintf(`rule cc_compile
  command = %s -c $in -o $out $flags -MMD -MF $out.d
  depfile = $out.d
  deps = gcc

rule cc_archive
  command = %s rcs $out $in
 `, getCC(), getAR())
}

type ccLibraryShared struct{}

func (r *ccLibraryShared) Name() string { return "cc_library_shared" }
func (r *ccLibraryShared) NinjaRule() string {
	return fmt.Sprintf(`rule cc_compile
  command = %s -c $in -o $out $flags -MMD -MF $out.d
  depfile = $out.d
  deps = gcc

rule cc_shared
  command = %s -shared -o $out $in $flags
 `, getCC(), getCC())
}

func (r *ccLibrary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	suffix := getArchSuffix()
	if getBoolProp(m, "shared") {
		return []string{fmt.Sprintf("lib%s%s.so", name, suffix)}
	}
	return []string{fmt.Sprintf("lib%s%s.a", name, suffix)}
}

func (r *ccLibrary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	shared := getBoolProp(m, "shared")
	cflags := getCflags(m)
	ldflags := getLdflags(m)
	sharedLibs := GetListProp(m, "shared_libs")
	if shared && len(sharedLibs) > 0 {
		for _, dep := range sharedLibs {
			depName := strings.TrimPrefix(dep, ":")
			ldflags += " -l" + depName
		}
	}
	var edges strings.Builder
	var objFiles []string

	for _, src := range srcs {
		// Generate unique object file name: {name}_{basename}.o
		base := filepath.Base(src)
		obj := strings.TrimSuffix(base, ".c")
		obj = strings.TrimSuffix(obj, ".cc")
		obj = name + "_" + obj + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}

	out := r.Outputs(m)[0]
	if shared {
		edges.WriteString(fmt.Sprintf("build %s: cc_shared %s\n flags = %s\n", out, strings.Join(objFiles, " "), ldflags))
	} else {
		edges.WriteString(fmt.Sprintf("build %s: cc_archive %s\n", out, strings.Join(objFiles, " ")))
	}
	return edges.String()
}

func (r *ccLibrary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		if getBoolProp(m, "shared") {
			return "cc_shared"
		}
		return "ar"
	}
	return "gcc"
}

// ============================================================================
// cc_library_static
// ============================================================================
func (r *ccLibraryStatic) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s%s.a", name, getArchSuffix())}
}
func (r *ccLibraryStatic) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	cflags := getCflags(m)
	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		base := filepath.Base(src)
		obj := strings.TrimSuffix(base, ".c")
		obj = strings.TrimSuffix(obj, ".cc")
		obj = name + "_" + obj + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}
	out := r.Outputs(m)[0]
	edges.WriteString(fmt.Sprintf("build %s: cc_archive %s\n", out, strings.Join(objFiles, " ")))
	return edges.String()
}
func (r *ccLibraryStatic) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "ar"
	}
	return "gcc"
}

// ============================================================================
// cc_library_shared
// ============================================================================
func (r *ccLibraryShared) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s%s.so", name, getArchSuffix())}
}
func (r *ccLibraryShared) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	cflags := getCflags(m)
	ldflags := getLdflags(m)
	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		base := filepath.Base(src)
		obj := strings.TrimSuffix(base, ".c")
		obj = strings.TrimSuffix(obj, ".cc")
		obj = name + "_" + obj + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}
	out := r.Outputs(m)[0]
	edges.WriteString(fmt.Sprintf("build %s: cc_shared %s\n flags = %s\n", out, strings.Join(objFiles, " "), ldflags))
	return edges.String()
}
func (r *ccLibraryShared) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cc_shared"
	}
	return "gcc"
}

// ============================================================================
// cc_object
// ============================================================================
type ccObject struct{}

func (r *ccObject) Name() string { return "cc_object" }
func (r *ccObject) NinjaRule() string {
	return fmt.Sprintf(`rule cc_compile
  command = %s -c $in -o $out $flags -MMD -MF $out.d
  depfile = $out.d
  deps = gcc
 `, getCC())
}
func (r *ccObject) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s%s.o", name, getArchSuffix())}
}
func (r *ccObject) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	cflags := getCflags(m)
	out := r.Outputs(m)[0]
	return fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", out, strings.Join(srcs, " "), cflags)
}
func (r *ccObject) Desc(m *parser.Module, srcFile string) string { return "gcc" }

// ============================================================================
// cc_binary
// ============================================================================
type ccBinary struct{}

func (r *ccBinary) Name() string { return "cc_binary" }
func (r *ccBinary) NinjaRule() string {
	return fmt.Sprintf(`rule cc_compile
  command = %s -c $in -o $out $flags -MMD -MF $out.d
  depfile = $out.d
  deps = gcc

rule cc_link
  command = %s -o $out $in $flags
 `, getCC(), getCC())
}
func (r *ccBinary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + getArchSuffix()}
}
func getLibOutputName(name string) string {
	return "lib" + name + getArchSuffix() + ".a"
}

func getSharedLibOutputName(name string) string {
	return "lib" + name + getArchSuffix() + ".so"
}

func (r *ccBinary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	deps := GetListProp(m, "deps")
	sharedLibs := GetListProp(m, "shared_libs")
	if name == "" || len(srcs) == 0 {
		return ""
	}
	cflags := getCflags(m)
	ldflags := getLdflags(m)
	allFlags := cflags
	if ldflags != "" {
		if allFlags != "" {
			allFlags += " "
		}
		allFlags += ldflags
	}
	var libFiles []string
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, getLibOutputName(depName))
	}
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, getSharedLibOutputName(depName))
		allFlags += " -l" + depName
	}
	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		base := filepath.Base(src)
		obj := strings.TrimSuffix(base, ".c") + "_" + name + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}
	out := r.Outputs(m)[0]
	allInputs := append(objFiles, libFiles...)
	edges.WriteString(fmt.Sprintf("build %s: cc_link %s\n flags = %s\n", out, strings.Join(allInputs, " "), allFlags))
	return edges.String()
}
func (r *ccBinary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cc_link"
	}
	return "gcc"
}

// ============================================================================
// cpp_library
// ============================================================================
type cppLibrary struct{}

func (r *cppLibrary) Name() string { return "cpp_library" }
func (r *cppLibrary) NinjaRule() string {
	return fmt.Sprintf(`rule cpp_compile
  command = %s -c $in -o $out $flags -MMD -MF $out.d
  depfile = $out.d
  deps = gcc

rule cpp_archive
  command = %s rcs $out $in

rule cpp_shared
  command = %s -shared -o $out $in $flags
 `, getCXX(), getAR(), getCXX())
}
func (r *cppLibrary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	suffix := getArchSuffix()
	if getBoolProp(m, "shared") {
		return []string{fmt.Sprintf("lib%s%s.so", name, suffix)}
	}
	return []string{fmt.Sprintf("lib%s%s.a", name, suffix)}
}
func (r *cppLibrary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	shared := getBoolProp(m, "shared")
	cflags := getCflags(m)
	cppflags := getCppflags(m)
	allFlags := cflags
	if cppflags != "" {
		if allFlags != "" {
			allFlags += " "
		}
		allFlags += cppflags
	}
	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		base := filepath.Base(src)
		obj := strings.TrimSuffix(base, ".cpp")
		obj = strings.TrimSuffix(obj, ".cc")
		obj = strings.TrimSuffix(obj, ".cxx")
		obj = name + "_" + obj + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cpp_compile %s\n flags = %s\n", obj, src, allFlags))
	}
	out := r.Outputs(m)[0]
	if shared {
		edges.WriteString(fmt.Sprintf("build %s: cpp_shared %s\n flags = %s\n", out, strings.Join(objFiles, " "), getLdflags(m)))
	} else {
		edges.WriteString(fmt.Sprintf("build %s: cpp_archive %s\n", out, strings.Join(objFiles, " ")))
	}
	return edges.String()
}
func (r *cppLibrary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		if getBoolProp(m, "shared") {
			return "cpp_shared"
		}
		return "ar"
	}
	return "g++"
}

// ============================================================================
// cpp_binary
// ============================================================================
type cppBinary struct{}

func (r *cppBinary) Name() string { return "cpp_binary" }
func (r *cppBinary) NinjaRule() string {
	return fmt.Sprintf(`rule cpp_compile
  command = %s -c $in -o $out $flags -MMD -MF $out.d
  depfile = $out.d
  deps = gcc

rule cpp_link
  command = %s -o $out $in $flags
 `, getCXX(), getCXX())
}
func (r *cppBinary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + getArchSuffix()}
}
func (r *cppBinary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	deps := GetListProp(m, "deps")
	sharedLibs := GetListProp(m, "shared_libs")
	if name == "" || len(srcs) == 0 {
		return ""
	}
	cflags := getCflags(m)
	cppflags := getCppflags(m)
	ldflags := getLdflags(m)
	allFlags := cflags
	if cppflags != "" {
		if allFlags != "" {
			allFlags += " "
		}
		allFlags += cppflags
	}
	var libFiles []string
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, getLibOutputName(depName))
	}
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, getSharedLibOutputName(depName))
		allFlags += " -l" + depName
	}
	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		base := filepath.Base(src)
		obj := strings.TrimSuffix(base, ".cpp")
		obj = strings.TrimSuffix(obj, ".cc")
		obj = strings.TrimSuffix(obj, ".cxx")
		obj = name + "_" + obj + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cpp_compile %s\n flags = %s\n", obj, src, allFlags))
	}
	out := r.Outputs(m)[0]
	allInputs := append(objFiles, libFiles...)
	edges.WriteString(fmt.Sprintf("build %s: cpp_link %s\n flags = %s\n", out, strings.Join(allInputs, " "), ldflags))
	return edges.String()
}

func (r *cppBinary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cpp_link"
	}
	return "g++"
}

// ============================================================================
// go_library
// ============================================================================
type goLibrary struct{}

func (r *goLibrary) Name() string { return "go_library" }
func (r *goLibrary) NinjaRule() string {
	return `rule go_build_archive
 command = go build -buildmode=archive -o $out $in
`
}
func (r *goLibrary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.a", name)}
}
func (r *goLibrary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	goflags := getGoflags(m)
	out := r.Outputs(m)[0]
	return fmt.Sprintf("build %s: go_build_archive %s\n flags = %s\n", out, strings.Join(srcs, " "), goflags)
}
func (r *goLibrary) Desc(m *parser.Module, srcFile string) string { return "go" }

// ============================================================================
// go_binary
// ============================================================================
type goBinary struct{}

func (r *goBinary) Name() string { return "go_binary" }
func (r *goBinary) NinjaRule() string {
	return `rule go_build
 command = go build -o $out $in
`
}
func (r *goBinary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name}
}
func (r *goBinary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	deps := GetListProp(m, "deps")
	if name == "" || len(srcs) == 0 {
		return ""
	}
	goflags := getGoflags(m)
	out := r.Outputs(m)[0]

	var libFiles []string
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, depName+".a")
	}

	srcStr := strings.Join(srcs, " ")
	if len(libFiles) > 0 {
		libStr := strings.Join(libFiles, " ")
		return fmt.Sprintf("build %s: go_build %s | %s\n flags = %s\n", out, srcStr, libStr, goflags)
	}
	return fmt.Sprintf("build %s: go_build %s\n flags = %s\n", out, srcStr, goflags)
}
func (r *goBinary) Desc(m *parser.Module, srcFile string) string { return "go" }

// ============================================================================
// go_test
// ============================================================================
type goTest struct{}

func (r *goTest) Name() string { return "go_test" }
func (r *goTest) NinjaRule() string {
	return `rule go_test
 command = go test -c -o $out $pkg
`
}

func (r *goTest) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.test", name)}
}
func (r *goTest) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	goflags := getGoflags(m)
	out := r.Outputs(m)[0]
	// Extract package path from first source file
	// Convert "dag/graph_test.go" to "./dag"
	pkgPath := "./" + filepath.Dir(srcs[0])

	// Build the test binary
	// Use go test -c which requires a package path
	return fmt.Sprintf("build %s: go_test\n pkg = %s\n flags = %s\n", out, pkgPath, goflags)
}
func (r *goTest) Desc(m *parser.Module, srcFile string) string { return "go test" }

// ============================================================================
// java_library
// ============================================================================
type javaLibrary struct{}

func (r *javaLibrary) Name() string { return "java_library" }
func (r *javaLibrary) NinjaRule() string {
	return `rule javac_lib
  command = javac -d $outdir $in $flags

rule jar_create
  command = jar cf $out -C $outdir .
`
}
func (r *javaLibrary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.jar", name)}
}
func (r *javaLibrary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	javaflags := getJavaflags(m)
	out := r.Outputs(m)[0]
	outdir := name + "_classes"
	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_lib %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create %s.stamp\n outdir = %s\n", out, name, outdir))
	return edges.String()
}
func (r *javaLibrary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// ============================================================================
// java_binary
// ============================================================================
type javaBinary struct{}

func (r *javaBinary) Name() string { return "java_binary" }
func (r *javaBinary) NinjaRule() string {
	return `rule javac_bin
  command = javac -d $outdir $in $flags

rule jar_create_executable
  command = jar cfe $out $main_class -C $outdir .
`
}
func (r *javaBinary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.jar", name)}
}
func (r *javaBinary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	mainClass := GetStringProp(m, "main_class")
	if name == "" || len(srcs) == 0 || mainClass == "" {
		return ""
	}
	javaflags := getJavaflags(m)
	out := r.Outputs(m)[0]
	outdir := name + "_classes"
	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_bin %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create_executable %s.stamp\n outdir = %s\n main_class = %s\n", out, name, outdir, mainClass))
	return edges.String()
}
func (r *javaBinary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// ============================================================================
// java_library_static
// ============================================================================
type javaLibraryStatic struct{}

func (r *javaLibraryStatic) Name() string { return "java_library_static" }
func (r *javaLibraryStatic) NinjaRule() string {
	return `rule javac_lib
  command = javac -d $outdir $in $flags

rule jar_create
  command = jar cf $out -C $outdir .
`
}
func (r *javaLibraryStatic) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s.a.jar", name)}
}
func (r *javaLibraryStatic) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	javaflags := getJavaflags(m)
	out := r.Outputs(m)[0]
	outdir := name + "_classes"
	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_lib %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create %s.stamp\n outdir = %s\n", out, name, outdir))
	return edges.String()
}
func (r *javaLibraryStatic) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// ============================================================================
// java_library_host
// ============================================================================
type javaLibraryHost struct{}

func (r *javaLibraryHost) Name() string { return "java_library_host" }
func (r *javaLibraryHost) NinjaRule() string {
	return `rule javac_lib
  command = javac -d $outdir $in $flags

rule jar_create
  command = jar cf $out -C $outdir .
`
}
func (r *javaLibraryHost) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-host.jar", name)}
}
func (r *javaLibraryHost) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	javaflags := getJavaflags(m)
	out := r.Outputs(m)[0]
	outdir := name + "_classes"
	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_lib %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create %s.stamp\n outdir = %s\n", out, name, outdir))
	return edges.String()
}
func (r *javaLibraryHost) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// ============================================================================
// java_binary_host
// ============================================================================
type javaBinaryHost struct{}

func (r *javaBinaryHost) Name() string { return "java_binary_host" }
func (r *javaBinaryHost) NinjaRule() string {
	return `rule javac_bin
  command = javac -d $outdir $in $flags

rule jar_create_executable
  command = jar cfe $out $main_class -C $outdir .
`
}
func (r *javaBinaryHost) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-host.jar", name)}
}
func (r *javaBinaryHost) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	mainClass := GetStringProp(m, "main_class")
	if name == "" || len(srcs) == 0 || mainClass == "" {
		return ""
	}
	javaflags := getJavaflags(m)
	out := r.Outputs(m)[0]
	outdir := name + "_classes"
	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_bin %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create_executable %s.stamp\n outdir = %s\n main_class = %s\n", out, name, outdir, mainClass))
	return edges.String()
}
func (r *javaBinaryHost) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// ============================================================================
// java_test
// ============================================================================
type javaTest struct{}

func (r *javaTest) Name() string { return "java_test" }
func (r *javaTest) NinjaRule() string {
	return `rule javac_test
  command = javac -d $outdir $in $flags

rule jar_test
  command = jar cf $out -C $outdir .
`
}
func (r *javaTest) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-test.jar", name)}
}
func (r *javaTest) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	javaflags := getJavaflags(m)
	out := r.Outputs(m)[0]
	outdir := name + "_classes"
	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_test %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_test %s.stamp\n outdir = %s\n", out, name, outdir))
	return edges.String()
}
func (r *javaTest) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// ============================================================================
// java_import
// ============================================================================
type javaImport struct{}

func (r *javaImport) Name() string { return "java_import" }
func (r *javaImport) NinjaRule() string {
	return `rule java_import
 command = cp $in $out
`
}
func (r *javaImport) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.jar", name)}
}
func (r *javaImport) NinjaEdge(m *parser.Module) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return ""
	}
	out := r.Outputs(m)[0]
	return fmt.Sprintf("build %s: java_import %s\n", out, strings.Join(srcs, " "))
}
func (r *javaImport) Desc(m *parser.Module, srcFile string) string { return "cp" }

// ============================================================================
// filegroup
// ============================================================================
type filegroup struct{}

func (r *filegroup) Name() string                                 { return "filegroup" }
func (r *filegroup) NinjaRule() string                            { return "" }
func (r *filegroup) Outputs(m *parser.Module) []string            { return nil }
func (r *filegroup) NinjaEdge(m *parser.Module) string            { return "" }
func (r *filegroup) Desc(m *parser.Module, srcFile string) string { return "filegroup" }

// ============================================================================
// custom
// ============================================================================
type customRule struct{}

func (r *customRule) Name() string { return "custom" }
func (r *customRule) NinjaRule() string {
	return `rule custom_command
 command = $cmd
`
}
func (r *customRule) Outputs(m *parser.Module) []string {
	return GetListProp(m, "outs")
}
func (r *customRule) NinjaEdge(m *parser.Module) string {
	return customRuleEdge(m, "")
}

func customRuleEdge(m *parser.Module, workDir string) string {
	srcs := GetListProp(m, "srcs")
	outs := GetListProp(m, "outs")
	cmd := GetStringProp(m, "cmd")
	excludeDirs := GetListProp(m, "exclude_dirs")
	if len(outs) == 0 || cmd == "" {
		return ""
	}
	outStr := strings.Join(outs, " ")
	srcStr := strings.Join(srcs, " ")

	actualCmd := cmd
	actualCmd = strings.ReplaceAll(actualCmd, "$in", srcStr)
	actualCmd = strings.ReplaceAll(actualCmd, "$out", outStr)

	if len(excludeDirs) > 0 && workDir != "" {
		excluded := make(map[string]bool)
		for _, dir := range excludeDirs {
			excluded[dir] = true
		}
		var pkgList []string
		filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || !info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(workDir, path)
			if rel == "." || strings.HasPrefix(rel, ".") {
				return nil
			}
			parts := strings.SplitN(rel, string(filepath.Separator), 2)
			if len(parts) > 0 && excluded[parts[0]] {
				return filepath.SkipDir
			}
			files, _ := os.ReadDir(path)
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".go") && !strings.HasPrefix(f.Name(), "_") && !strings.HasSuffix(f.Name(), "_test.go") {
					pkgList = append(pkgList, "./"+rel)
					break
				}
			}
			return nil
		})
		actualCmd = strings.ReplaceAll(actualCmd, "./...", strings.Join(pkgList, " "))
	}

	hash := 0
	for _, c := range actualCmd {
		hash = hash*31 + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	ruleName := fmt.Sprintf("custom_cmd_%d", hash%10000)

	var result strings.Builder
	result.WriteString(fmt.Sprintf("rule %s\n command = %s\n\n", ruleName, actualCmd))
	if srcStr == "" {
		result.WriteString(fmt.Sprintf("build %s: %s\n", outStr, ruleName))
	} else {
		result.WriteString(fmt.Sprintf("build %s: %s %s\n", outStr, ruleName, srcStr))
	}

	return result.String()
}
func (r *customRule) Desc(m *parser.Module, srcFile string) string { return "custom" }

// GetAllRules returns all available rule implementations
// ============================================================================
// cc_library_headers - Header library (exports headers for other modules)
// ============================================================================
type ccLibraryHeaders struct{}

func (r *ccLibraryHeaders) Name() string      { return "cc_library_headers" }
func (r *ccLibraryHeaders) NinjaRule() string { return "" }
func (r *ccLibraryHeaders) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".h"}
}
func (r *ccLibraryHeaders) NinjaEdge(m *parser.Module) string {
	return ""
}
func (r *ccLibraryHeaders) Desc(m *parser.Module, srcFile string) string { return "" }

// ============================================================================
// proto_library - Protocol Buffer library
// ============================================================================
type protoLibraryRule struct{}

func (r *protoLibraryRule) Name() string { return "proto_library" }
func (r *protoLibraryRule) NinjaRule() string {
	return `rule protoc
  command = protoc --proto_path=. $proto_paths $include_flags $plugin_flags --$out_type_out=$proto_out $in
  description = PROTOC $in
`
}
func (r *protoLibraryRule) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	outType := GetStringProp(m, "out")
	if outType == "" {
		outType = "cc"
	}
	srcs := getSrcs(m)
	var outs []string
	for _, src := range srcs {
		base := strings.TrimSuffix(filepath.Base(src), ".proto")
		switch outType {
		case "cc":
			outs = append(outs, base+".pb.h", base+".pb.cc")
		case "go":
			outs = append(outs, base+".pb.go")
		case "java":
			outs = append(outs, base+".java")
		case "python":
			outs = append(outs, base+"_pb2.py")
		default:
			outs = append(outs, base+".pb."+outType)
		}
	}
	return outs
}
func (r *protoLibraryRule) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	outType := GetStringProp(m, "out")
	if outType == "" {
		outType = "cc"
	}
	protoPaths := GetListProp(m, "proto_paths")
	plugins := GetListProp(m, "plugins")
	includeDirs := GetListProp(m, "include_dirs")

	protoPathFlags := ""
	for _, p := range protoPaths {
		protoPathFlags += " --proto_path=" + p
	}

	includeFlags := ""
	for _, d := range includeDirs {
		includeFlags += " --proto_path=" + d
	}

	pluginFlags := ""
	for _, pl := range plugins {
		pluginFlags += " --plugin=" + pl
	}

	protoOut := name + "_proto_out"

	outs := r.Outputs(m)
	if len(outs) == 0 {
		return ""
	}

	return fmt.Sprintf("build %s: protoc %s\n proto_paths = %s\n include_flags = %s\n plugin_flags = %s\n out_type = %s\n proto_out = %s\n",
		strings.Join(outs, " "),
		strings.Join(srcs, " "),
		protoPathFlags,
		includeFlags,
		pluginFlags,
		outType,
		protoOut,
	)
}
func (r *protoLibraryRule) Desc(m *parser.Module, srcFile string) string { return "protoc" }

// ============================================================================
// proto_gen - Protocol Buffer code generation
// ============================================================================
type protoGenRule struct{}

func (r *protoGenRule) Name() string { return "proto_gen" }
func (r *protoGenRule) NinjaRule() string {
	return `rule protoc_gen
  command = protoc --proto_path=. $proto_paths $include_flags $plugin_flags --$out_type_out=$proto_out $in
  description = PROTOC $in
`
}
func (r *protoGenRule) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	outType := GetStringProp(m, "out")
	if outType == "" {
		outType = "cc"
	}
	srcs := getSrcs(m)
	var outs []string
	for _, src := range srcs {
		base := strings.TrimSuffix(filepath.Base(src), ".proto")
		switch outType {
		case "cc":
			outs = append(outs, name+"_"+base+".pb.h", name+"_"+base+".pb.cc")
		case "go":
			outs = append(outs, name+"_"+base+".pb.go")
		default:
			outs = append(outs, name+"_"+base+".pb."+outType)
		}
	}
	return outs
}
func (r *protoGenRule) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	outType := GetStringProp(m, "out")
	if outType == "" {
		outType = "cc"
	}
	protoPaths := GetListProp(m, "proto_paths")
	plugins := GetListProp(m, "plugins")
	includeDirs := GetListProp(m, "include_dirs")

	protoPathFlags := ""
	for _, p := range protoPaths {
		protoPathFlags += " --proto_path=" + p
	}

	includeFlags := ""
	for _, d := range includeDirs {
		includeFlags += " --proto_path=" + d
	}

	pluginFlags := ""
	for _, pl := range plugins {
		pluginFlags += " --plugin=" + pl
	}

	protoOut := name + "_proto_out"

	outs := r.Outputs(m)
	if len(outs) == 0 {
		return ""
	}

	return fmt.Sprintf("build %s: protoc_gen %s\n proto_paths = %s\n include_flags = %s\n plugin_flags = %s\n out_type = %s\n proto_out = %s\n",
		strings.Join(outs, " "),
		strings.Join(srcs, " "),
		protoPathFlags,
		includeFlags,
		pluginFlags,
		outType,
		protoOut,
	)
}
func (r *protoGenRule) Desc(m *parser.Module, srcFile string) string { return "protoc" }

func GetAllRules() []BuildRule {
	return []BuildRule{
		&ccLibrary{}, &ccLibraryStatic{}, &ccLibraryShared{}, &ccObject{}, &ccBinary{},
		&cppLibrary{}, &cppBinary{}, &ccLibraryHeaders{},
		&goLibrary{}, &goBinary{}, &goTest{},
		&javaLibrary{}, &javaLibraryStatic{}, &javaLibraryHost{}, &javaBinary{}, &javaBinaryHost{}, &javaTest{}, &javaImport{},
		&filegroup{}, &customRule{},
		&protoLibraryRule{}, &protoGenRule{},
	}
}

// GetRule returns a rule by name
func GetRule(name string) BuildRule {
	for _, r := range GetAllRules() {
		if r.Name() == name {
			return r
		}
	}
	return nil
}

// ExpandGlob expands glob patterns
func ExpandGlob(patterns []string, exclude []string) []string {
	var result []string
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		if strings.Contains(pattern, "**") {
			dir := "."
			suffix := ""
			if idx := strings.Index(pattern, "/**"); idx >= 0 {
				dir = pattern[:idx]
				suffix = pattern[idx+3:]
			}
			filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if suffix == "" || strings.HasSuffix(path, suffix) {
					if !seen[path] {
						for _, ex := range exclude {
							if matched, _ := filepath.Match(ex, path); matched {
								return nil
							}
						}
						result = append(result, path)
						seen[path] = true
					}
				}
				return nil
			})
		} else {
			matches, _ := filepath.Glob(pattern)
			for _, m := range matches {
				if !seen[m] {
					result = append(result, m)
					seen[m] = true
				}
			}
		}
	}
	return result
}
