// ninja/cc.go - C/C++ build rules for minibp
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"path/filepath"
	"strings"
)

func detectCompilerType(srcs []string) string {
	for _, src := range srcs {
		ext := strings.ToLower(filepath.Ext(src))
		switch ext {
		case ".cpp", ".cc", ".cxx", ".c++", ".hpp", ".hxx":
			return "cpp"
		case ".c", ".h":
			continue
		}
	}
	return "cc"
}

func ccCompilerCmd(ctx RuleRenderContext, compilerType string) string {
	cc := ctx.CC
	if compilerType == "cpp" {
		cc = ctx.CXX
	}
	if ctx.Ccache != "" {
		return ctx.Ccache + " " + cc
	}
	return cc
}

func ltoFlags(lto string) (compile string, link string) {
	switch lto {
	case "full":
		return "-flto -ffat-lto-objects", "-flto -fuse-linker-plugin"
	case "thin":
		return "-flto=thin -ffat-lto-objects", "-flto=thin -fuse-linker-plugin"
	default:
		return "", ""
	}
}

func ltoArchiveCmd(ar string, lto string) string {
	if lto == "full" || lto == "thin" {
		if strings.Contains(ar, "gcc-ar") || strings.Contains(ar, "llvm-ar") {
			return ar
		}
		if strings.HasPrefix(ar, "ar") {
			return "gcc-ar"
		}
		return "gcc-ar"
	}
	return ar
}

// ccLibrary implements a C/C++ library rule that can be either static or shared.
type ccLibrary struct{}

func (r *ccLibrary) Name() string { return "cc_library" }

func (r *ccLibrary) NinjaRule(ctx RuleRenderContext) string {
	arCmd := ltoArchiveCmd(ctx.AR, ctx.Lto)
	ltoCompile, ltoLink := ltoFlags(ctx.Lto)

	rules := fmt.Sprintf(`rule cc_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

rule cc_compile_lto
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

rule cc_archive
 command = %s rcs $out @$out.rsp
 rspfile = $out.rsp
 rspfile_content = $in
 restat = true

rule cc_shared
 command = ${CC} -shared -o $out @$out.rsp $flags
 rspfile = $out.rsp
 rspfile_content = $in

rule cc_link_lto
 command = %s -o $out $in $flags %s

rule thinlto_codegen
 command = %s -flto=thin -c -fthin-link=$out.thinlto.o $in -o $out %s
`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"),
		arCmd, ctx.CC, ltoLink, ccCompilerCmd(ctx, "cc"), ltoCompile)

	return rules
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

	compilerType := detectCompilerType(srcs)
	compiler := ccCompilerCmd(ctx, compilerType)
	shared := getBoolProp(m, "shared")
	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}

	ltoCompileExtra, _ := ltoFlags(moduleLto)
	compileRule := "cc_compile"
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
	}
	archiveRule := "cc_archive"
	sharedRule := "cc_shared"

	cflags := joinFlags(ctx.CFlags, getCflags(m), getCppflags(m))
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}
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
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", obj, compileRule, src, cflags, compiler))
	}

	out := r.Outputs(m, ctx)[0]

	if shared {
		allInputs := append(objFiles, sharedInputs...)
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", out, sharedRule, strings.Join(allInputs, " "), ldflags, compiler))
	} else {
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n", out, archiveRule, strings.Join(objFiles, " ")))
	}

	if moduleLto == "thin" {
		for _, src := range srcs {
			obj := objectOutputName(name, src)
			codegen := obj + ".thinlto.o"
			edges.WriteString(fmt.Sprintf("build %s: thinlto_codegen %s\n", codegen, obj))
		}
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
	arCmd := ltoArchiveCmd(ctx.AR, ctx.Lto)
	return fmt.Sprintf(`rule cc_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

rule cc_compile_lto
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

rule cc_archive
 command = %s rcs $out @$out.rsp
 rspfile = $out.rsp
 rspfile_content = $in
 restat = true

`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"), arCmd)
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

	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}
	ltoCompileExtra, _ := ltoFlags(moduleLto)
	compileRule := "cc_compile"
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m))
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}

	var edges strings.Builder
	var objFiles []string
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", obj, compileRule, src, cflags))
	}

	out := r.Outputs(m, ctx)[0]
	edges.WriteString(fmt.Sprintf("build %s: cc_archive %s\n", out, strings.Join(objFiles, " ")))

	if moduleLto == "thin" {
		for _, src := range srcs {
			obj := objectOutputName(name, src)
			codegen := obj + ".thinlto.o"
			edges.WriteString(fmt.Sprintf("build %s: thinlto_codegen %s\n", codegen, obj))
		}
	}

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
	_, ltoLink := ltoFlags(ctx.Lto)
	linkSuffix := ""
	if ltoLink != "" {
		linkSuffix = " " + ltoLink
	}

	return fmt.Sprintf(`rule cc_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

rule cc_compile_lto
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

rule cc_shared
 command = %s -shared -o $out @$out.rsp $flags%s
 rspfile = $out.rsp
 rspfile_content = $in

rule cc_link_lto
 command = %s -o $out $in $flags%s

`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"), ctx.CC, linkSuffix, ctx.CC, linkSuffix)
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

	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}
	ltoCompileExtra, _ := ltoFlags(moduleLto)
	compileRule := "cc_compile"
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m), getCppflags(m))
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}
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
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", obj, compileRule, src, cflags))
	}

	out := r.Outputs(m, ctx)[0]
	allInputs := append(objFiles, sharedInputs...)

	linkRule := "cc_shared"
	if moduleLto != "" {
		linkRule = "cc_link_lto"
	}
	edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", out, linkRule, strings.Join(allInputs, " "), ldflags))

	if moduleLto == "thin" {
		for _, src := range srcs {
			obj := objectOutputName(name, src)
			codegen := obj + ".thinlto.o"
			edges.WriteString(fmt.Sprintf("build %s: thinlto_codegen %s\n", codegen, obj))
		}
	}

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

rule cc_compile_lto
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"))
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

	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}
	ltoCompileExtra, _ := ltoFlags(moduleLto)
	compileRule := "cc_compile"
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m))
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}

	if len(srcs) == 1 {
		out := r.Outputs(m, ctx)[0]
		return fmt.Sprintf("build %s: %s %s\n flags = %s\n", out, compileRule, srcs[0], cflags)
	}

	var edges strings.Builder
	outputs := r.Outputs(m, ctx)
	for i, src := range srcs {
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", outputs[i], compileRule, src, cflags))
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
	arCmd := ltoArchiveCmd(ctx.AR, ctx.Lto)
	_, ltoLink := ltoFlags(ctx.Lto)
	linkSuffix := ""
	if ltoLink != "" {
		linkSuffix = " " + ltoLink
	}

	return fmt.Sprintf(`rule cc_compile
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

rule cc_compile_lto
 command = %s -c $in -o $out $flags -MMD -MF $out.d
 depfile = $out.d
 deps = gcc

rule cc_link
 command = ${CC} -o $out @$out.rsp $flags%s
 rspfile = $out.rsp
 rspfile_content = $in

rule cc_link_lto
 command = ${CC} -o $out $in $flags%s

rule cc_archive
 command = %s rcs $out @$out.rsp
 rspfile = $out.rsp
 rspfile_content = $in
 restat = true

rule thinlto_codegen
 command = %s -flto=thin -c -fthin-link=$out.thinlto.o $in -o $out %s

`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"), linkSuffix, linkSuffix,
		arCmd, ccCompilerCmd(ctx, "cc"), "")
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

	compilerType := detectCompilerType(srcs)
	compiler := ccCompilerCmd(ctx, compilerType)

	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}
	ltoCompileExtra, _ := ltoFlags(moduleLto)
	compileRule := "cc_compile"
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
	}

	cflags := joinFlags(ctx.CFlags, getCflags(m), getCppflags(m))
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}
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
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", obj, compileRule, src, cflags, compiler))
	}

	out := r.Outputs(m, ctx)[0]
	allInputs := append(objFiles, libFiles...)

	linkRule := "cc_link"
	if moduleLto != "" {
		linkRule = "cc_link_lto"
	}
	edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", out, linkRule, strings.Join(allInputs, " "), linkFlags, compiler))

	if moduleLto == "thin" {
		for _, src := range srcs {
			obj := objectOutputName(name, src)
			codegen := obj + ".thinlto.o"
			edges.WriteString(fmt.Sprintf("build %s: thinlto_codegen %s\n", codegen, obj))
		}
	}

	return edges.String()
}

func (r *ccBinary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cc_link"
	}
	return "gcc"
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

func ccTestEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	cflags := joinFlags(getCflags(m), ctx.CFlags)
	linkFlags := joinFlags(getLdflags(m), ctx.LdFlags)
	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}
	compileRule := "cc_compile"
	ltoCompileFlags, ltoLinkFlags := ltoFlags(moduleLto)
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
		cflags = joinFlags(cflags, ltoCompileFlags)
		linkFlags = joinFlags(linkFlags, ltoLinkFlags)
	}
	compilerType := detectCompilerType(srcs)
	compiler := ccCompilerCmd(ctx, compilerType)
	if compilerType == "cpp" {
		cflags = joinFlags(getCppflags(m), cflags)
	}
	deps := GetListProp(m, "deps")
	sharedLibs := GetListProp(m, "shared_libs")
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
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", obj, compileRule, src, cflags, compiler))
	}
	out := name + ".test" + ctx.ArchSuffix
	allInputs := append(objFiles, libFiles...)
	linkRule := "cc_link"
	if moduleLto != "" {
		linkRule = "cc_link_lto"
	}
	edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", ninjaEscapePath(out), linkRule, strings.Join(allInputs, " "), linkFlags, compiler))
	if moduleLto == "thin" {
		for _, src := range srcs {
			obj := objectOutputName(name, src)
			codegen := obj + ".thinlto.o"
			edges.WriteString(fmt.Sprintf("build %s: thinlto_codegen %s\n", codegen, obj))
		}
	}
	return edges.String()
}
