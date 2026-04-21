// ninja/cc.go - C/C++ build rules for minibp
package ninja

import (
	"fmt"
	"minibp/parser"
	"path/filepath"
	"strings"
)

// detectCompilerType detects the compiler type based on source file extensions.
// Returns "cc" for C files, "cpp" for C++ files, defaulting to "cc".
func detectCompilerType(srcs []string) string {
	for _, src := range srcs {
		ext := strings.ToLower(filepath.Ext(src))
		switch ext {
		case ".cpp", ".cc", ".cxx", ".c++", ".hpp", ".hxx":
			return "cpp"
		case ".c", ".h":
			// Continue to check other files, C++ files take precedence
			continue
		}
	}
	return "cc"
}

// ccLibrary implements a C/C++ library rule that can be either static or shared.

// It automatically detects the compiler type based on source file extensions.

type ccLibrary struct{}



func (r *ccLibrary) Name() string {

	return "cc_library"

}



func (r *ccLibrary) NinjaRule(ctx RuleRenderContext) string {



	// Auto-detect compiler type - use CXX if any C++ files are present



	return fmt.Sprintf(`rule cc_compile



 command = %s -c $in -o $out $flags -MMD -MF $out.d



 depfile = $out.d



 deps = gcc



rule cc_archive



 command = %s rcs $out $in



rule cc_shared



 command = %s -shared -o $out $in $flags



`, ctx.CXX, ctx.AR, ctx.CXX)



}

func (r *ccLibrary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	suffix := ctx.ArchSuffix
	if getBoolProp(m, "shared") {
		return []string{fmt.Sprintf("lib%s%s.so", name, suffix)}
	}
	return []string{fmt.Sprintf("lib%s%s.a", name, suffix)}
}

func (r *ccLibrary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {

	name := getName(m)

	srcs := getSrcs(m)

	if name == "" || len(srcs) == 0 {

		return ""

	}



			// Auto-detect compiler type



			_ = detectCompilerType(srcs) // Use result to select compiler flags if needed



			compileRule := "cc_compile"



			archiveRule := "cc_archive"



			sharedRule := "cc_shared"



	shared := getBoolProp(m, "shared")

	cflags := joinFlags(ctx.CFlags, getCflags(m))

	ldflags := joinFlags(ctx.LdFlags, getLdflags(m))

	var sharedInputs []string

	sharedLibs := GetListProp(m, "shared_libs")

	if shared && len(sharedLibs) > 0 {

		for _, dep := range sharedLibs {

			depName := strings.TrimPrefix(dep, ":")

			sharedInputs = append(sharedInputs, sharedLibOutputName(depName, ctx.ArchSuffix))

			ldflags = joinFlags(ldflags, "-l"+depName)

		}

	}



	var edges strings.Builder

	var objFiles []string

	for _, src := range srcs {

		obj := objectOutputName(name, src)

		objFiles = append(objFiles, obj)

		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", obj, compileRule, src, cflags))

	}

	out := r.Outputs(m, ctx)[0]

	if shared {

		allInputs := append(objFiles, sharedInputs...)

		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", out, sharedRule, strings.Join(allInputs, " "), ldflags))

	} else {

		edges.WriteString(fmt.Sprintf("build %s: %s %s\n", out, archiveRule, strings.Join(objFiles, " ")))

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

// ccLibraryStatic implements a C static library rule (always produces .a).
type ccLibraryStatic struct{}

func (r *ccLibraryStatic) Name() string { return "cc_library_static" }

func (r *ccLibraryStatic) NinjaRule(ctx RuleRenderContext) string {
	return fmt.Sprintf(`rule cc_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc
rule cc_archive
 command = %s rcs $out $in
`, ctx.CC, ctx.AR)
}

func (r *ccLibraryStatic) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s%s.a", name, ctx.ArchSuffix)}
}

func (r *ccLibraryStatic) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m))

	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}

	out := r.Outputs(m, ctx)[0]
	edges.WriteString(fmt.Sprintf("build %s: cc_archive %s\n", out, strings.Join(objFiles, " ")))
	return edges.String()
}

func (r *ccLibraryStatic) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "ar"
	}
	return "gcc"
}

// ccLibraryShared implements a C shared library rule (always produces .so).
type ccLibraryShared struct{}

func (r *ccLibraryShared) Name() string { return "cc_library_shared" }

func (r *ccLibraryShared) NinjaRule(ctx RuleRenderContext) string {
	return fmt.Sprintf(`rule cc_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc
rule cc_shared
 command = %s -shared -o $out $in $flags
`, ctx.CC, ctx.CC)
}

func (r *ccLibraryShared) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s%s.so", name, ctx.ArchSuffix)}
}

func (r *ccLibraryShared) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m))
	ldflags := joinFlags(ctx.LdFlags, getLdflags(m))

	var sharedInputs []string
	sharedLibs := GetListProp(m, "shared_libs")
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		sharedInputs = append(sharedInputs, sharedLibOutputName(depName, ctx.ArchSuffix))
		ldflags = joinFlags(ldflags, "-l"+depName)
	}

	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}

	out := r.Outputs(m, ctx)[0]
	allInputs := append(objFiles, sharedInputs...)
	edges.WriteString(fmt.Sprintf("build %s: cc_shared %s\n flags = %s\n", out, strings.Join(allInputs, " "), ldflags))
	return edges.String()
}

func (r *ccLibraryShared) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cc_shared"
	}
	return "gcc"
}

// ccObject implements a C object file rule.
type ccObject struct{}

func (r *ccObject) Name() string { return "cc_object" }

func (r *ccObject) NinjaRule(ctx RuleRenderContext) string {
	return fmt.Sprintf(`rule cc_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc
`, ctx.CC)
}

func (r *ccObject) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" {
		return nil
	}
	if len(srcs) <= 1 {
		return []string{fmt.Sprintf("%s%s.o", name, ctx.ArchSuffix)}
	}
	outputs := make([]string, 0, len(srcs))
	for _, src := range srcs {
		outputs = append(outputs, objectOutputName(name, src))
	}
	return outputs
}

func (r *ccObject) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m))
	if len(srcs) == 1 {
		out := r.Outputs(m, ctx)[0]
		return fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", out, srcs[0], cflags)
	}

	var edges strings.Builder
	outputs := r.Outputs(m, ctx)
	for i, src := range srcs {
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", outputs[i], src, cflags))
	}
	return edges.String()
}

func (r *ccObject) Desc(m *parser.Module, srcFile string) string {
	return "gcc"
}

// ccBinary implements a C/C++ binary rule.
type ccBinary struct{}

func (r *ccBinary) Name() string { return "cc_binary" }

func (r *ccBinary) NinjaRule(ctx RuleRenderContext) string {
	return fmt.Sprintf(`rule cc_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc
rule cc_link
 command = %s -o $out $in $flags
`, ctx.CC, ctx.CC)
}

func (r *ccBinary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ctx.ArchSuffix}
}

func (r *ccBinary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	deps := GetListProp(m, "deps")
	sharedLibs := GetListProp(m, "shared_libs")
	if name == "" || len(srcs) == 0 {
		return ""
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m))
	ldflags := joinFlags(ctx.LdFlags, getLdflags(m))
	linkFlags := ldflags

	var libFiles []string
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, staticLibOutputName(depName, ctx.ArchSuffix))
	}
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, sharedLibOutputName(depName, ctx.ArchSuffix))
		linkFlags = joinFlags(linkFlags, "-l"+depName)
	}

	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cc_compile %s\n flags = %s\n", obj, src, cflags))
	}

	out := r.Outputs(m, ctx)[0]
	allInputs := append(objFiles, libFiles...)
	edges.WriteString(fmt.Sprintf("build %s: cc_link %s\n flags = %s\n", out, strings.Join(allInputs, " "), linkFlags))
	return edges.String()
}

func (r *ccBinary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cc_link"
	}
	return "gcc"
}

// cppLibrary implements a C++ library rule.
type cppLibrary struct{}

func (r *cppLibrary) Name() string { return "cpp_library" }

func (r *cppLibrary) NinjaRule(ctx RuleRenderContext) string {
	return fmt.Sprintf(`rule cpp_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc
rule cpp_archive
 command = %s rcs $out $in
rule cpp_shared
 command = %s -shared -o $out $in $flags
`, ctx.CXX, ctx.AR, ctx.CXX)
}

func (r *cppLibrary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	suffix := ctx.ArchSuffix
	if getBoolProp(m, "shared") {
		return []string{fmt.Sprintf("lib%s%s.so", name, suffix)}
	}
	return []string{fmt.Sprintf("lib%s%s.a", name, suffix)}
}

func (r *cppLibrary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	shared := getBoolProp(m, "shared")
	cflags := getCflags(m)
	cppflags := getCppflags(m)
	ldflags := getLdflags(m)
	allFlags := joinFlags(cflags, cppflags)

	var sharedInputs []string
	if shared {
		sharedLibs := GetListProp(m, "shared_libs")
		for _, dep := range sharedLibs {
			depName := strings.TrimPrefix(dep, ":")
			sharedInputs = append(sharedInputs, sharedLibOutputName(depName, ctx.ArchSuffix))
			ldflags = joinFlags(ldflags, "-l"+depName)
		}
	}

	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cpp_compile %s\n flags = %s\n", obj, src, allFlags))
	}

	out := r.Outputs(m, ctx)[0]
	if shared {
		allInputs := append(objFiles, sharedInputs...)
		edges.WriteString(fmt.Sprintf("build %s: cpp_shared %s\n flags = %s\n", out, strings.Join(allInputs, " "), ldflags))
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

// cppBinary implements a C++ binary rule.
type cppBinary struct{}

func (r *cppBinary) Name() string { return "cpp_binary" }

func (r *cppBinary) NinjaRule(ctx RuleRenderContext) string {
	return fmt.Sprintf(`rule cpp_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc
rule cpp_link
 command = %s -o $out $in $flags
`, ctx.CXX, ctx.CXX)
}

func (r *cppBinary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ctx.ArchSuffix}
}

func (r *cppBinary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	deps := GetListProp(m, "deps")
	sharedLibs := GetListProp(m, "shared_libs")
	if name == "" || len(srcs) == 0 {
		return ""
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m))
	cppflags := getCppflags(m)
	ldflags := joinFlags(ctx.LdFlags, getLdflags(m))
	allFlags := joinFlags(cflags, cppflags)
	linkFlags := ldflags

	var libFiles []string
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, staticLibOutputName(depName, ctx.ArchSuffix))
	}
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, sharedLibOutputName(depName, ctx.ArchSuffix))
		linkFlags = joinFlags(linkFlags, "-l"+depName)
	}

	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: cpp_compile %s\n flags = %s\n", obj, src, allFlags))
	}

	out := r.Outputs(m, ctx)[0]
	allInputs := append(objFiles, libFiles...)
	edges.WriteString(fmt.Sprintf("build %s: cpp_link %s\n flags = %s\n", out, strings.Join(allInputs, " "), linkFlags))
	return edges.String()
}

func (r *cppBinary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cpp_link"
	}
	return "g++"
}

// ccLibraryHeaders implements a C/C++ header library rule.
type ccLibraryHeaders struct{}

func (r *ccLibraryHeaders) Name() string                           { return "cc_library_headers" }
func (r *ccLibraryHeaders) NinjaRule(ctx RuleRenderContext) string { return "" }
func (r *ccLibraryHeaders) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".h"}
}
func (r *ccLibraryHeaders) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string { return "" }
func (r *ccLibraryHeaders) Desc(m *parser.Module, srcFile string) string             { return "" }
