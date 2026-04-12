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

func getStringProp(m *parser.Module, name string) string {
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

func getListProp(m *parser.Module, name string) []string {
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

func getCflags(m *parser.Module) string { return strings.Join(getListProp(m, "cflags"), " ") }
func getCppflags(m *parser.Module) string { return strings.Join(getListProp(m, "cppflags"), " ") }
func getLdflags(m *parser.Module) string { return strings.Join(getListProp(m, "ldflags"), " ") }
func getGoflags(m *parser.Module) string { return strings.Join(getListProp(m, "goflags"), " ") }
func getJavaflags(m *parser.Module) string { return strings.Join(getListProp(m, "javaflags"), " ") }
func getName(m *parser.Module) string { return getStringProp(m, "name") }
func getSrcs(m *parser.Module) []string { return getListProp(m, "srcs") }

// ============================================================================
// cc_library - C library (static by default, shared if shared: true)
// ============================================================================
type ccLibrary struct{}

func (r *ccLibrary) Name() string { return "cc_library" }
func (r *ccLibrary) NinjaRule() string {
	return `rule cc_compile
 command = gcc -c $in -o $out $flags
rule cc_archive
 command = ar rcs $out $in
rule cc_shared
 command = gcc -shared -o $out $in $flags
`
}

func (r *ccLibrary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	if getBoolProp(m, "shared") {
		return []string{fmt.Sprintf("lib%s.so", name)}
	}
	return []string{fmt.Sprintf("lib%s.a", name)}
}

func (r *ccLibrary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	shared := getBoolProp(m, "shared")
	cflags := getCflags(m)
	var edges strings.Builder
	var objFiles []string

	for _, src := range srcs {
		obj := strings.TrimSuffix(src, ".c")
		obj = strings.TrimSuffix(obj, ".cc") + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}

	out := r.Outputs(m)[0]
	if shared {
		edges.WriteString(fmt.Sprintf("build %s: cc_shared %s\n flags = %s\n", out, strings.Join(objFiles, " "), getLdflags(m)))
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
type ccLibraryStatic struct{}

func (r *ccLibraryStatic) Name() string { return "cc_library_static" }
func (r *ccLibraryStatic) NinjaRule() string {
	return `rule cc_compile
 command = gcc -c $in -o $out $flags
rule cc_archive
 command = ar rcs $out $in
`
}
func (r *ccLibraryStatic) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s.a", name)}
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
		obj := strings.TrimSuffix(src, ".c")
		obj = strings.TrimSuffix(obj, ".cc") + ".o"
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
type ccLibraryShared struct{}

func (r *ccLibraryShared) Name() string { return "cc_library_shared" }
func (r *ccLibraryShared) NinjaRule() string {
	return `rule cc_compile
 command = gcc -c $in -o $out $flags
rule cc_shared
 command = gcc -shared -o $out $in $flags
`
}
func (r *ccLibraryShared) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s.so", name)}
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
		obj := strings.TrimSuffix(src, ".c")
		obj = strings.TrimSuffix(obj, ".cc") + ".o"
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
	return `rule cc_compile
 command = gcc -c $in -o $out $flags
`
}
func (r *ccObject) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.o", name)}
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
	return `rule cc_compile
 command = gcc -c $in -o $out $flags
rule cc_link
 command = gcc -o $out $in $flags
`
}
func (r *ccBinary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name}
}
func (r *ccBinary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
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
	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		obj := strings.TrimSuffix(src, ".c") + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}
	out := r.Outputs(m)[0]
	edges.WriteString(fmt.Sprintf("build %s: cc_link %s\n flags = %s\n", out, strings.Join(objFiles, " "), allFlags))
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
	return `rule cpp_compile
 command = g++ -c $in -o $out $flags
rule cpp_archive
 command = ar rcs $out $in
rule cpp_shared
 command = g++ -shared -o $out $in $flags
`
}
func (r *cppLibrary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	if getBoolProp(m, "shared") {
		return []string{fmt.Sprintf("lib%s.so", name)}
	}
	return []string{fmt.Sprintf("lib%s.a", name)}
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
		obj := strings.TrimSuffix(src, ".cpp")
		obj = strings.TrimSuffix(obj, ".cc")
		obj = strings.TrimSuffix(obj, ".cxx") + ".o"
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
	return `rule cpp_compile
 command = g++ -c $in -o $out $flags
rule cpp_link
 command = g++ -o $out $in $flags
`
}
func (r *cppBinary) Outputs(m *parser.Module) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name}
}
func (r *cppBinary) NinjaEdge(m *parser.Module) string {
	name := getName(m)
	srcs := getSrcs(m)
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
	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		obj := strings.TrimSuffix(src, ".cpp")
		obj = strings.TrimSuffix(obj, ".cc")
		obj = strings.TrimSuffix(obj, ".cxx") + ".o"
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cpp_compile %s\n flags = %s\n", obj, src, allFlags))
	}
	out := r.Outputs(m)[0]
	edges.WriteString(fmt.Sprintf("build %s: cpp_link %s\n flags = %s\n", out, strings.Join(objFiles, " "), ldflags))
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
	if name == "" || len(srcs) == 0 {
		return ""
	}
	goflags := getGoflags(m)
	out := r.Outputs(m)[0]
	return fmt.Sprintf("build %s: go_build %s\n flags = %s\n", out, strings.Join(srcs, " "), goflags)
}
func (r *goBinary) Desc(m *parser.Module, srcFile string) string { return "go" }

// ============================================================================
// go_test
// ============================================================================
type goTest struct{}

func (r *goTest) Name() string { return "go_test" }
func (r *goTest) NinjaRule() string {
	return `rule go_test
 command = go test -c -o $out $in
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
	return fmt.Sprintf("build %s: go_test %s\n flags = %s\n", out, strings.Join(srcs, " "), goflags)
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
	mainClass := getStringProp(m, "main_class")
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
	mainClass := getStringProp(m, "main_class")
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

func (r *filegroup) Name() string { return "filegroup" }
func (r *filegroup) NinjaRule() string { return "" }
func (r *filegroup) Outputs(m *parser.Module) []string { return nil }
func (r *filegroup) NinjaEdge(m *parser.Module) string { return "" }
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
	return getListProp(m, "outs")
}
func (r *customRule) NinjaEdge(m *parser.Module) string {
	srcs := getListProp(m, "srcs")
	outs := getListProp(m, "outs")
	cmd := getStringProp(m, "cmd")
	if len(outs) == 0 || cmd == "" {
		return ""
	}
	outStr := strings.Join(outs, " ")
	srcStr := strings.Join(srcs, " ")
	if srcStr == "" {
		return fmt.Sprintf("build %s: custom_command\n cmd = %s\n", outStr, cmd)
	}
	return fmt.Sprintf("build %s: custom_command %s\n cmd = %s\n", outStr, srcStr, cmd)
}
func (r *customRule) Desc(m *parser.Module, srcFile string) string { return "custom" }

// GetAllRules returns all available rule implementations
func GetAllRules() []BuildRule {
	return []BuildRule{
		&ccLibrary{}, &ccLibraryStatic{}, &ccLibraryShared{}, &ccObject{}, &ccBinary{},
		&cppLibrary{}, &cppBinary{},
		&goLibrary{}, &goBinary{}, &goTest{},
		&javaLibrary{}, &javaLibraryStatic{}, &javaLibraryHost{}, &javaBinary{}, &javaBinaryHost{}, &javaTest{}, &javaImport{},
		&filegroup{}, &customRule{},
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
