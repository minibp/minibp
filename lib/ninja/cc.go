// Package ninja implements C/C++ build rules for minibp.
// This file implements the BuildRule interface for C and C++ language modules.
// It provides rules for compiling C/C++ source files into libraries, binaries, and object files.
// The key module types are: cc_library, cc_library_static, cc_library_shared, cc_object, cc_binary, and cc_library_headers.
//
// Algorithm overview:
//  1. Detect whether source files are C or C++ based on file extensions
//  2. Generate compile rules that invoke the C/C++ compiler with appropriate flags
//  3. For libraries, archive object files into .a (static) or link into .so (shared)
//  4. For binaries, link object files with library dependencies
//  5. Support LTO (Link Time Optimization) via -flto flags
//  6. Support ccache for faster incremental builds
//
// Key functions:
//   - detectCompilerType: Determine C vs C++ based on file extensions
//   - ccCompilerCmd: Get compiler command with optional ccache wrapper
//   - ltoFlags: Get LTO flags for compilation and linking
//   - ltoArchiveCmd: Get appropriate archiver for LTO static libraries
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"path/filepath"
	"strings"
)

// detectCompilerType determines whether source files are C or C++ based on file extensions.
// It returns "cpp" if any source file has a C++ extension (.cpp, .cc, .cxx, .c++, .hpp, .hxx).
// Otherwise it returns "cc" for C files. Header files (.h) are treated as C unless a C++ specific extension is used.
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

// ccCompilerCmd returns the C/C++ compiler command to use for compilation.
// It selects between CC (C compiler) and CXX (C++ compiler) based on compilerType.
// If ccache is configured in the context, it prepends ccache to the compiler command.
// This enables compiler caching for faster incremental builds.
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

// ltoFlags returns compiler and linker flags for Link Time Optimization (LTO).
// LTO can be "full" for full LTO, "thin" for thin LTO, or "" for no LTO.
// Full LTO provides maximum optimization but takes longer to build.
// Thin LTO provides faster builds with good optimization.
// Returns empty strings if LTO is not enabled.
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

// ltoArchiveCmd returns the archiver command for creating static libraries with LTO.
// When LTO is enabled and the default ar is not suitable, it returns gcc-ar or llvm-ar.
// This ensures proper LTO symbol resolution in static libraries.
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

// ccLibrary implements a C/C++ library rule.
// This is the main library type that produces either .a (static) or .so (shared) libraries.
// The output type is determined by the "shared" boolean property on the module.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C/C++ compilation
//   - cc_compile_lto: Compilation with LTO enabled
//   - cc_archive: Archive object files into static library
//   - cc_shared: Link object files into shared library
//   - cc_link_lto: Link with LTO enabled
//   - thinlto_codegen: Generate thin LTO intermediate files
//
// Outputs returns the library filename:
//   - lib{name}{suffix}.so for shared libraries
//   - lib{name}{suffix}.a for static libraries
type ccLibrary struct{}

// Name returns the module type name for cc_library.
func (r *ccLibrary) Name() string { return "cc_library" }

// NinjaRule defines the ninja compilation, archiving, and linking rules for C/C++ libraries.
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja rule definitions as formatted string
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
	command = %s rcs $out $in
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

// Outputs returns the library output paths.
// For shared libraries (with "shared" property), returns lib{name}{suffix}.so.
// For static libraries, returns lib{name}{suffix}.a.
// Returns nil if the module has no name.
func (r *ccLibrary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	suffix := ctx.ArchSuffix
	libName := name
	if !strings.HasPrefix(name, "lib") {
		libName = "lib" + name
	}
	if getBoolProp(m, "shared") {
		return []string{fmt.Sprintf("%s%s.so", libName, suffix)}
	}
	return []string{fmt.Sprintf("%s%s.a", libName, suffix)}
}

// NinjaEdge generates ninja build edges for compiling source files and creating the library.
// It compiles each source file to an object file, then archives or links them into the final library.
// For shared libraries, it links object files into a shared object (.so).
// For static libraries, it archives object files into a static archive (.a).
// When LTO is "thin", it also generates thinlto_codegen edges for intermediate object files.
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

// ccLibraryStatic implements a C static library rule.
// This module type always produces a .a (static) library, regardless of the shared property.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C compilation
//   - cc_compile_lto: Compilation with LTO enabled
//   - cc_archive: Archive object files into static library
type ccLibraryStatic struct{}

// Name returns the module type name for cc_library_static.
func (r *ccLibraryStatic) Name() string { return "cc_library_static" }

// NinjaRule defines the ninja compilation and archiving rules for static libraries.
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja rule definitions as formatted string
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
command = %s rcs $out $in
restat = true
`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"), arCmd)
}

func (r *ccLibraryStatic) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	libName := name
	if !strings.HasPrefix(name, "lib") {
		libName = "lib" + name
	}
	return []string{fmt.Sprintf("%s%s.a", libName, ctx.ArchSuffix)}
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

// ccLibraryShared implements a C shared library rule.
// This module type always produces a .so (shared) library.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C compilation
//   - cc_compile_lto: Compilation with LTO enabled
//   - cc_shared: Link object files into shared library
//   - cc_link_lto: Link with LTO enabled
type ccLibraryShared struct{}

// Name returns the module type name for cc_library_shared.
func (r *ccLibraryShared) Name() string { return "cc_library_shared" }

// NinjaRule defines the ninja compilation and linking rules for shared libraries.
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja rule definitions as formatted string
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
	libName := name
	if !strings.HasPrefix(name, "lib") {
		libName = "lib" + name
	}
	return []string{fmt.Sprintf("%s%s.so", libName, ctx.ArchSuffix)}
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
// This module type compiles source files to .o object files without creating a library.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C compilation
//   - cc_compile_lto: Compilation with LTO enabled
type ccObject struct{}

// Name returns the module type name for cc_object.
func (r *ccObject) Name() string { return "cc_object" }

// NinjaRule defines the ninja compilation rules for object files.
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja rule definitions as formatted string
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
// This module type produces an executable binary from source files and dependencies.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C/C++ compilation
//   - cc_compile_lto: Compilation with LTO enabled
//   - cc_link: Link object files into executable
//   - cc_link_lto: Link with LTO enabled
//   - cc_archive: Archive object files for static libs
//   - thinlto_codegen: Generate thin LTO intermediate files
type ccBinary struct{}

// Name returns the module type name for cc_binary.
func (r *ccBinary) Name() string { return "cc_binary" }

// NinjaRule defines the ninja compilation and linking rules for binaries.
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja rule definitions as formatted string
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
  command = ${CC} -o $out $in $flags%s
rule cc_link_lto
command = ${CC} -o $out $in $flags%s
rule cc_archive
command = %s rcs $out $in
restat = true
rule thinlto_codegen
command = %s -flto=thin -c -fthin-link=$out.thinlto.o $in -o $out
%s
`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"), linkSuffix, linkSuffix, arCmd, ccCompilerCmd(ctx, "cc"), "")
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
// This module type provides header files for other modules to include.
// It doesn't produce compiled output but exports include directories.
//
// NinjaRule returns empty string (no compilation rules needed).
// NinjaEdge returns empty string (no build edges needed).
// Outputs returns the header filename for dependency tracking.
type ccLibraryHeaders struct{}

// Name returns the module type name for cc_library_headers.
func (r *ccLibraryHeaders) Name() string { return "cc_library_headers" }

// NinjaRule returns empty (no compilation needed for headers).
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
	if args := getTestOptionArgs(m); args != "" {
		edges.WriteString(fmt.Sprintf(" test_args = %s\n", args))
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
