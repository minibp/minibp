// Package ninja implements C/C++ build rules for minibp.
//
// This package provides the BuildRule implementations for C and C++ language
// modules in the Blueprint/Soong build system. It handles compilation, archiving,
// linking, and other build operations for C/C++ source files.
//
// Design decisions:
//   - Uses file extension detection to determine whether to use C or C++ compiler
//   - Supports both static (.a) and shared (.so) library outputs
//   - Supports LTO (Link Time Optimization) for both full and thin LTO modes
//   - Uses ccache for compiler caching when available
//   - Supports multi-architecture builds via ArchSuffix
//   - Supports architecture variants (e.g., host, target, arm64, x86_64)
//
// Key module types:
//   - cc_library: Produces either static or shared library based on "shared" property
//   - cc_library_static: Always produces static library (.a)
//   - cc_library_shared: Always produces shared library (.so)
//   - cc_object: Produces object files (.o) without archiving/linking
//   - cc_binary: Produces executable binary
//   - cc_library_headers: Header-only library for include path management
//   - cc_test: Test executable with test-specific configurations
//
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
//
// Each module type implements the BuildRule interface:
//   - Name() string: Returns the module type name
//   - NinjaRule(ctx) string: Returns ninja rule definitions
//   - Outputs(m, ctx) []string: Returns output file paths
//   - NinjaEdge(m, ctx) string: Returns ninja build edges
//   - Desc(m, src) string: Returns a short description
//
// This file provides C/C++ compilation, archiving, and linking rules
// for the Ninja build system.
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"path/filepath"
	"sort"
	"strings"
)

// detectCompilerType determines whether source files are C or C++ based on file extensions.
//
// Description:
//
//	This function scans the provided source file list and determines whether
//	the C++ compiler should be used. It checks file extensions looking for
//	C++-specific extensions.
//
// How it works:
//
//	Iterates through each source file and extracts the file extension.
//	If any file has a C++ extension, returns "cpp" immediately.
//	Otherwise returns "cc" for C files.
//
// Parameters:
//   - srcs: Slice of source file paths to check
//
// Returns:
//   - "cpp" if any source file has a C++ extension (.cpp, .cc, .cxx, .c++, .hpp, .hxx)
//   - "cc" otherwise
//
// Edge cases:
//   - Empty srcs slice: Returns "cc" (no files to check)
//   - Only .h files: Returns "cc" (.h is treated as C)
//   - Mix of C and C++ files: Returns "cpp" (C++ takes precedence)
//   - .hpp, .hxx extensions: Treated as C++ headers
func detectCompilerType(srcs []string) string {
	for _, src := range srcs {
		ext := strings.ToLower(filepath.Ext(src))
		switch ext {
		case ".cpp", ".cc", ".cxx", ".c++", ".hpp", ".hxx":
			return "cpp" // C++ extension found: use C++ compiler
		case ".c", ".h":
			continue // C extension: continue checking other files
		}
	}
	return "cc" // No C++ files found: default to C compiler
}

// ccCompilerCmd returns the C/C++ compiler command to use for compilation.
//
// Description:
//
//	This function selects the appropriate compiler command based on the
//	compiler type and optionally wraps it with ccache for compiler caching.
//
// How it works:
//  1. Select CC or CXX based on compilerType parameter
//  2. If ccache is configured, prepend ccache to the compiler command
//  3. Return the full compiler command string
//
// Parameters:
//   - ctx: RuleRenderContext containing CC, CXX, and Ccache configuration
//   - compilerType: Either "cc" for C compiler or "cpp" for C++ compiler
//
// Returns:
//   - Full compiler command string (e.g., "gcc", "g++", "ccache gcc")
//
// Edge cases:
//   - Empty Ccache string: Returns compiler without ccache prefix
//   - Invalid compilerType: Returns CC (C compiler) as default
//   - Empty ctx.CC or ctx.CXX: Uses empty string (should not happen in practice)
func ccCompilerCmd(ctx RuleRenderContext, compilerType string) string {
	cc := ctx.CC
	if compilerType == "cpp" {
		cc = ctx.CXX
	}
	if ctx.Ccache != "" {
		var b strings.Builder
		b.Grow(len(ctx.Ccache) + 1 + len(cc))
		b.WriteString(ctx.Ccache)
		b.WriteString(" ")
		b.WriteString(cc)
		return b.String()
	}
	return cc
}

// ltoFlags returns compiler and linker flags for Link Time Optimization (LTO).
//
// Description:
//
//	This function returns the appropriate compiler and linker flags for
//	enabling Link Time Optimization. LTO allows the compiler to optimize
//	across compilation units at link time.
//
// How it works:
//
//	Based on the LTO mode parameter:
//	- "full": Returns flags for full LTO with maximum optimization
//	- "thin": Returns flags for thin LTO with faster build times
//	- Other: Returns empty strings (LTO disabled)
//
// Parameters:
//   - lto: LTO mode string - "full", "thin", or "" for disabled
//
// Returns:
//   - compile: Compiler flags for LTO (e.g., "-flto -ffat-lto-objects")
//   - link: Linker flags for LTO (e.g., "-flto -fuse-linker-plugin")
//
// Edge cases:
//   - Empty or invalid lto string: Returns empty strings
//   - "full" mode: Uses -flto with -ffat-lto-objects for full LTO
//   - "thin" mode: Uses -flto=thin for thin LTO
//   - Mode is case-sensitive (must be lowercase)
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
//
// Description:
//
//	When LTO is enabled, the standard ar archiver may not be compatible
//	with LTO object files. This function returns an appropriate archiver
//	(gcc-ar or llvm-ar) that can handle LTO-enabled static libraries.
//
// How it works:
//  1. If LTO is disabled (empty string), return ar unchanged
//  2. If already using gcc-ar or llvm-ar, return unchanged
//  3. Otherwise, return gcc-ar as the default LTO-compatible archiver
//
// Parameters:
//   - ar: The default archiver command (e.g., "ar", "gcc-ar", "llvm-ar")
//   - lto: LTO mode string - "full", "thin", or "" for disabled
//
// Returns:
//   - Appropriate archiver command for LTO-enabled static libraries
//
// Edge cases:
//   - LTO disabled: Returns original ar command unchanged
//   - Already using gcc-ar: Returns as-is
//   - Already using llvm-ar: Returns as-is
//   - Using default "ar": Returns "gcc-ar" for LTO compatibility
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
//
// Description:
//
//	This is the main library type that produces either static (.a) or
//	shared (.so) libraries. The output type is determined by the "shared"
//	boolean property on the module.
//
// Design decisions:
//   - Uses "shared" property to determine output type
//   - Automatically adds "lib" prefix to library names
//   - Supports multi-architecture builds via ArchSuffix
//   - Supports LTO for both compilation and linking
//   - Uses ccache for compiler caching when available
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C/C++ compilation with dependency tracking
//   - cc_compile_lto: Compilation with LTO enabled
//   - cc_archive: Archive object files into static library
//   - cc_shared: Link object files into shared library
//   - cc_link_lto: Link with LTO enabled
//   - thinlto_codegen: Generate thin LTO intermediate files
type ccLibrary struct{}

// Name returns the module type name for cc_library.
// This name matches the module type in Blueprint files (e.g., cc_library { ... }).
//
// Returns:
//   - "cc_library" string identifying this build rule type
//
// Edge cases:
//   - None (returns constant string)
func (r *ccLibrary) Name() string { return "cc_library" }

// NinjaRule defines the ninja compilation, archiving, and linking rules for C/C++ libraries.
//
// Description:
//
//	This method generates all Ninja rules needed for compiling C/C++ source
//	files into libraries. It includes standard compilation, LTO compilation,
//	archiving, shared library linking, and LTO linking rules.
//
// How it works:
//  1. Get LTO-compatible archiver using ltoArchiveCmd
//  2. Get LTO flags using ltoFlags
//  3. Generate rule definitions for each build operation
//  4. Return all rules as a formatted string
//
// Parameters:
//   - ctx: RuleRenderContext with toolchain and flags (CC, CXX, AR, Lto, Ccache)
//
// Returns:
//   - Ninja rule definitions as formatted string
//
// Edge cases:
//   - LTO mode "thin": Includes thinlto_codegen rule
//   - ccache enabled: Compiler commands prefixed with ccache path
//   - Custom AR: Uses ltoArchiveCmd to select appropriate archiver
//   - Empty LTO: Generates standard rules only
func (r *ccLibrary) NinjaRule(ctx RuleRenderContext) string {
	arCmd := ltoArchiveCmd(ctx.AR, ctx.Lto)
	ltoCompile, ltoLink := ltoFlags(ctx.Lto)
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

rule cc_shared
  command = ${CC} -shared -o $out @$out.rsp $flags
  rspfile = $out.rsp
  rspfile_content = $in

rule cc_link_lto
 command = %s -o $out $in $flags %s

rule thinlto_codegen
 command = %s -flto=thin -c -fthin-link=$out.thinlto.o $in -o $out %s

rule ln
  command = ln -sf $in $out
  description = Creating symlink $out
`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"),
		arCmd, ctx.CC, ltoLink, ccCompilerCmd(ctx, "cc"), ltoCompile)
}

// Outputs returns the library output paths.
//
// Description:
//
//	This method returns the output file paths for the library. The output type
//	depends on the "shared" property: static (.a) or shared (.so).
//	If version property is set, shared libraries will have versioned output
//	(e.g., libfoo.so.1.0.1) and optionally a soname symlink.
//
// How it works:
//  1. Get module name from "name" property
//  2. If name is empty, return nil
//  3. Add "lib" prefix if not present
//  4. Check "shared" property to determine output type
//  5. If shared and version is set, use versioned output
//  6. Append ArchSuffix for multi-architecture builds
//
// Parameters:
//   - m: parser.Module being evaluated (must have "name" property, optionally "shared")
//   - ctx: RuleRenderContext with ArchSuffix for multi-arch builds
//
// Returns:
//   - List containing the library output path
//   - nil if module has no name
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - "foo" becomes "libfoo"
//   - "libbar" stays "libbar"
//   - shared=true: Returns .so extension (possibly versioned)
//   - shared=false or missing: Returns .a extension
//   - version property: Creates versioned shared library (e.g., libfoo.so.1.0.1)
//   - version_soname property: Creates soname symlink (e.g., libfoo.so.1)
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
		// Check for version property (version management)
		version := GetStringProp(m, "version")
		if version != "" {
			return []string{fmt.Sprintf("%s%s.so.%s", libName, suffix, version)}
		}
		return []string{fmt.Sprintf("%s%s.so", libName, suffix)}
	}
	return []string{fmt.Sprintf("%s%s.a", libName, suffix)}
}

// NinjaEdge generates ninja build edges for compiling source files and creating the library.
//
// Description:
//
//	This method generates the ninja build edges for compiling source files and
//	creating either a static or shared library. It handles architecture variants
//	by generating separate build edges for each variant.
//
// How it works:
//  1. Get module name and source files, exit early if missing
//  2. Check for architecture variants using getGoTargetVariants
//  3. If no variants, call ninjaEdgeForVariant with empty string
//  4. If variants exist, generate edges for each variant in sorted order
//
// Parameters:
//   - m: parser.Module being evaluated (must have "name", "srcs", optionally "shared", "shared_libs")
//   - ctx: RuleRenderContext with toolchain and flags
//
// Returns:
//   - Ninja build edge string for compilation and linking
//   - Empty string if module has no name or no source files
//
// Edge cases:
//   - Empty srcs or name: Returns ""
//   - Module-level LTO overrides context LTO
//   - shared_libs: Adds -l flags to ldflags
//   - C++ sources: Adds cppflags to cflags
func (r *ccLibrary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	// Exit early if module has no name or no source files.
	if name == "" || len(srcs) == 0 {
		return ""
	}

	// Check for architecture variants (e.g., host, target, arm64, x86_64).
	// If variants exist, generate separate build edges for each variant.
	variants := getGoTargetVariants(m)
	if len(variants) == 0 {
		// No variants: generate build edges for the default (empty variant).
		return r.ninjaEdgeForVariant(m, ctx, "")
	}

	// Multiple variants: generate build edges for each variant in sorted order.
	// Sorting ensures deterministic output regardless of map iteration order.
	var edges strings.Builder
	sorted := make([]string, len(variants))
	copy(sorted, variants)
	sort.Strings(sorted)
	for _, v := range sorted {
		edges.WriteString(r.ninjaEdgeForVariant(m, ctx, v))
	}
	return edges.String()
}

// ninjaEdgeForVariant generates ninja build edges for a specific variant of cc_library.
// It compiles source files, archives or links them into the final library based on shared property.
// Variant-specific toolchain settings (cc, cxx, sysroot, cflags) are applied if available.
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs", "shared", "shared_libs" properties)
//   - ctx: Rule render context with toolchain and flags (CC, CXX, CFlags, LdFlags, Lto, etc.)
//   - variant: Variant name (e.g., "host", "target_arm64"); empty for default variant
//
// Returns:
//   - Ninja build edge string for the variant (compilation + archive/link edges)
//   - Empty string if module has no name or no source files
//
// Build algorithm:
//  1. Get module name and source files, exit early if missing
//  2. Detect compiler type (C vs C++) from file extensions
//  3. Determine LTO setting (module-level overrides context)
//  4. Select compile rule (cc_compile or cc_compile_lto)
//  5. Apply variant-specific toolchain (cc, cxx, sysroot) if available
//  6. Build C/C++ flags including LTO, sysroot, variant-specific cflags
//  7. Collect shared library dependencies (shared_libs) and add -l flags
//  8. Generate compile edges for each source file
//  9. Generate archive edge (cc_archive) for static or link edge (cc_shared/cc_link_lto) for shared
//  10. Generate thinlto_codegen edges if LTO is "thin"
//
// Edge cases:
//   - Empty variant: Uses default toolchain from context
//   - Variant with custom toolchain: Overrides default CC/CXX/Sysroot
//   - Module-level LTO overrides context LTO setting
//   - Shared libraries with shared_libs: Adds -l{depName} to ldflags
//   - C++ sources: Adds cppflags to compilation flags
func (r *ccLibrary) ninjaEdgeForVariant(m *parser.Module, ctx RuleRenderContext, variant string) string {
	name := getName(m)
	srcs := getSrcs(m)

	compilerType := detectCompilerType(srcs)
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

	// Use variant-specific toolchain if available
	cc := ctx.CC
	cxx := ctx.CXX
	sysroot := ctx.Sysroot
	if variant != "" {
		if v := getGoTargetProp(m, variant, "cc"); v != "" {
			cc = v
		}
		if v := getGoTargetProp(m, variant, "cxx"); v != "" {
			cxx = v
		}
		if v := getGoTargetProp(m, variant, "sysroot"); v != "" {
			sysroot = v
		}
	}

	compiler := cc
	if compilerType == "cpp" {
		compiler = cxx
	}
	if ctx.Ccache != "" {
		var b strings.Builder
		b.Grow(len(ctx.Ccache) + 1 + len(compiler))
		b.WriteString(ctx.Ccache)
		b.WriteString(" ")
		b.WriteString(compiler)
		compiler = b.String()
	}

	// Determine linker command: use LD if specified, otherwise use compiler
	linker := compiler
	if ctx.LD != "" {
		linker = ctx.LD
		if ctx.Ccache != "" {
			var b strings.Builder
			b.Grow(len(ctx.Ccache) + 1 + len(linker))
			b.WriteString(ctx.Ccache)
			b.WriteString(" ")
			b.WriteString(linker)
			linker = b.String()
		}
	}

	// Build flags
	cflags := joinFlags(ctx.CFlags, getCflags(m), getCppflags(m), getUndefines(m))
	if variant != "" {
		if v := getGoTargetProp(m, variant, "cflags"); v != "" {
			cflags = joinFlags(cflags, v)
		}
	}
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}
	// Add sysroot to flags if specified
	if sysroot != "" {
		cflags = strings.TrimSpace(cflags + " --sysroot=" + sysroot)
	}
	// Add exported cflags from dependencies
	if ctx.ExportCFlags != "" {
		cflags = strings.TrimSpace(cflags + " " + ctx.ExportCFlags)
	}

	ldflags := joinFlags(ctx.LdFlags, getLdflags(m))
	if variant != "" {
		if v := getGoTargetProp(m, variant, "ldflags"); v != "" {
			ldflags = joinFlags(ldflags, v)
		}
	}
	// Add exported ldflags from dependencies
	if ctx.ExportLdFlags != "" {
		ldflags = strings.TrimSpace(ldflags + " " + ctx.ExportLdFlags)
	}

	var sharedInputs []string
	sharedLibs := GetListProp(m, "shared_libs")
	if shared && len(sharedLibs) > 0 {
		for _, dep := range sharedLibs {
			depName := strings.TrimPrefix(dep, ":")
			sharedInputs = append(sharedInputs, sharedLibOutputName(depName, ctx.ArchSuffix))
			ldflags = joinFlags(ldflags, "-l"+depName)
		}
	}

	// Generate compile edges for each source file.
	// Each source file is compiled to a separate .o object file with proper flags.
	// Generate compile edges for each source file.
	var edges strings.Builder
	var objFiles = make([]string, 0, len(srcs))

	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		// Build edge: compile source to object with flags and compiler variables.
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", obj, compileRule, filepath.Join(ctx.PathPrefix, src), cflags, compiler))
	}

	// Generate the final library output edge (archive or link).
	out := r.Outputs(m, ctx)[0]
	version := GetStringProp(m, "version")

	if shared {
		// Shared library: link all object files and shared library dependencies.
		allInputs := append(objFiles, sharedInputs...)
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", out, sharedRule, strings.Join(allInputs, " "), ldflags, linker))

		// Create symlinks for versioned shared libraries (similar to xmake.sh's soname mechanism).
		// xmake.sh creates: libfoo.so.1.0.1 (versioned), libfoo.so.1 (soname), libfoo.so -> soname
		if version != "" {
			// Get soname version (defaults to major version if not specified).
			versionSoname := GetStringProp(m, "version_soname")
			if versionSoname == "" {
				// Default: use major version (first component of version).
				parts := strings.Split(version, ".")
				if len(parts) > 0 {
					versionSoname = parts[0]
				}
			}

			// Get the base output name (without version suffix).
			// From Outputs(): returns fmt.Sprintf("%s%s.so.%s", libName, suffix, version)
			// We need to construct the soname and default symlinks.
			libName := out[:len(out)-len(version)-4] // Remove ".so.version"
			sonameFile := fmt.Sprintf("%s.so.%s", libName, versionSoname)
			defaultFile := fmt.Sprintf("%s.so", libName)

			// Create symlink: soname -> versioned file.
			edges.WriteString(fmt.Sprintf("build %s: ln %s\n", sonameFile, out))
			// Create symlink: default -> soname.
			edges.WriteString(fmt.Sprintf("build %s: ln %s\n", defaultFile, sonameFile))
		}
	} else {
		// Static library: archive object files into .a file.
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n", out, archiveRule, strings.Join(objFiles, " ")))
	}

	// Generate thinLTO codegen edges for incremental LTO optimization.
	// Each object file gets a corresponding .thinlto.o intermediate file.
	if moduleLto == "thin" {
		for _, src := range srcs {
			obj := objectOutputName(name, src)
			codegen := obj + ".thinlto.o"
			edges.WriteString(fmt.Sprintf("build %s: thinlto_codegen %s\n", codegen, obj))
		}
	}

	return edges.String()
}

// Desc returns a short description of the build action for ninja's progress output.
// Returns "cc_shared" for shared library linking (srcFile == "").
// Returns "ar" for static library archiving (srcFile == "").
// Returns "gcc" for individual source file compilation (srcFile != "").
//
// Parameters:
//   - m: Module being evaluated (unused in this implementation)
//   - srcFile: Source file path; empty means this is a linking/archiving step
//
// Returns:
//   - Description string for ninja's build log
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
// Even if "shared: true" is set, this rule will create a static archive.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C/C++ compilation with -MMD -MF for dependencies
//   - cc_compile_lto: Compilation with LTO enabled for whole-program optimization
//   - cc_archive: Archive object files into static library using ar or gcc-ar
//   - restat = true ensures ninja rechecks timestamp after archiving
//
// Unlike ccLibrary, this type never generates shared library rules (cc_shared, cc_link_lto).
// This is useful for creating static-only libraries that can be linked into final binaries.
type ccLibraryStatic struct{}

// Name returns the module type name for cc_library_static.
// This name matches the module type in Blueprint files (e.g., cc_library_static { ... }).
// Unlike cc_library, this type always produces static libraries regardless of "shared" property.
//
// Returns:
//   - "cc_library_static" string identifying this build rule type
//
// Edge cases:
//   - None (returns constant string)
func (r *ccLibraryStatic) Name() string { return "cc_library_static" }

// NinjaRule defines the ninja compilation and archiving rules for static libraries.
//
// The generated rules include:
//   - cc_compile: Standard C/C++ compilation with dependency tracking
//   - Uses -MMD -MF to generate .d dependency files
//   - deps = gcc tells ninja to parse GCC-style dependency files
//   - cc_compile_lto: Compilation with LTO enabled for whole-program optimization
//   - cc_archive: Archive object files into static library
//   - Uses ar rcs or gcc-ar (for LTO compatibility)
//   - restat = true tells ninja to recheck timestamp after rule execution
//
// Unlike ccLibrary, this does NOT generate shared library rules (cc_shared, cc_link_lto).
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags (CC, CXX, AR, Lto, Ccache)
//
// Returns:
//   - Ninja rule definitions as formatted string
//
// Edge cases:
//   - LTO enabled: Uses ltoArchiveCmd to select appropriate archiver (gcc-ar or llvm-ar)
//   - ccache enabled: Compiler commands are prefixed with ccache path
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

// Outputs returns the output paths for static libraries.
// Returns nil if the module has no name (invalid module).
// Output format: lib{name}{suffix}.a
// The "lib" prefix is automatically added if the name doesn't already start with "lib".
// ArchSuffix is appended for multi-architecture builds (e.g., "_arm64").
//
// Parameters:
//   - m: Module being evaluated (must have "name" property)
//   - ctx: Rule render context with architecture suffix
//
// Returns:
//   - List containing the static library output path (e.g., ["libfoo_arm64.a"])
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - Name without "lib" prefix: Automatically prepended (e.g., "foo" -> "libfoo")
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

// NinjaEdge generates ninja build edges for static library compilation and archiving.
// It compiles each source file to an object file, then archives them into a static library.
// Unlike ccLibrary, this never generates shared library edges even if "shared" property is set.
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs" properties)
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja build edge string for compilation and archiving
//   - Empty string if module has no name or no source files
//
// Build algorithm:
//  1. Get module name and source files, exit early if missing
//  2. Determine LTO setting (module-level overrides context)
//  3. Select compile rule (cc_compile or cc_compile_lto)
//  4. Add C/C++ flags including LTO flags if enabled
//  5. Generate compile edges for each source file
//  6. Generate archive edge (cc_archive) for final static library
//  7. Generate thinlto_codegen edges if LTO is "thin"
//
// Edge cases:
//   - Empty srcs or name: Returns "" (nothing to compile)
//   - Module-level LTO overrides context LTO setting
//   - C++ sources: Uses cflags only (no cppflags for static library type)
func (r *ccLibraryStatic) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	// Exit early if module has no name or no source files.
	if name == "" || len(srcs) == 0 {
		return ""
	}

	// Determine LTO setting: module-level overrides context-level.
	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}
	// Get LTO flags for compile step; link flags ignored for static libraries.
	ltoCompileExtra, _ := ltoFlags(moduleLto)
	compileRule := "cc_compile"
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
	}

	// Build C flags: combine context flags, module cflags (no cppflags for static library).
	cflags := joinFlags(ctx.CFlags, getCflags(m), getUndefines(m))
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}
	// Add exported cflags from dependencies
	if ctx.ExportCFlags != "" {
		cflags = strings.TrimSpace(cflags + " " + ctx.ExportCFlags)
	}

	// Generate compile edges for each source file.
	var edges strings.Builder
	var objFiles = make([]string, 0, len(srcs))
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		// Build edge: compile source to object with flags.
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", obj, compileRule, filepath.Join(ctx.PathPrefix, src), cflags))
	}

	// Generate archive edge to create static library from all object files.
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

// Desc returns a short description of the build action for ninja's progress output.
// Returns "ar" for static library archiving (srcFile == "").
// Returns "gcc" for individual source file compilation (srcFile != "").
//
// Parameters:
//   - m: Module being evaluated (unused in this implementation)
//   - srcFile: Source file path; empty means this is an archiving step
//
// Returns:
//   - Description string for ninja's build log
func (r *ccLibraryStatic) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "ar"
	}
	return "gcc"
}

// ccLibraryShared implements a C shared library rule.
// This module type always produces a .so (shared) library, regardless of the shared property.
// Even if "shared: false" is set, this rule will create a shared object.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C/C++ compilation with -MMD -MF for dependencies
//   - cc_compile_lto: Compilation with LTO enabled for whole-program optimization
//   - cc_shared: Link object files into shared library with -shared flag
//   - Uses response files (@$out.rsp) to handle large numbers of inputs
//   - cc_link_lto: Link with LTO enabled (requires LTO-compatible linker)
//
// Unlike ccLibrary, this type never generates static archive rules (cc_archive).
// This is useful for creating shared-only libraries for dynamic linking.
type ccLibraryShared struct{}

// Name returns the module type name for cc_library_shared.
// This name matches the module type in Blueprint files (e.g., cc_library_shared { ... }).
// Unlike cc_library, this type always produces shared libraries regardless of "shared" property.
//
// Returns:
//   - "cc_library_shared" string identifying this build rule type
//
// Edge cases:
//   - None (returns constant string)
func (r *ccLibraryShared) Name() string { return "cc_library_shared" }

// NinjaRule defines the ninja compilation and linking rules for shared libraries.
//
// The generated rules include:
//   - cc_compile: Standard C/C++ compilation with dependency tracking
//   - Uses -MMD -MF to generate .d dependency files
//   - deps = gcc tells ninja to parse GCC-style dependency files
//   - cc_compile_lto: Compilation with LTO enabled for whole-program optimization
//   - cc_shared: Link object files into shared library
//   - Uses -shared flag to create shared object
//   - Uses response files (@$out.rsp) for large numbers of inputs
//   - cc_link_lto: Link with LTO enabled
//   - Requires LTO-compatible linker (typically gold or lld)
//
// Unlike ccLibrary, this does NOT generate static archive rules (cc_archive).
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags (CC, Lto)
//
// Returns:
//   - Ninja rule definitions as formatted string
//
// Edge cases:
//   - LTO enabled: Adds LTO linker flags to cc_shared and cc_link_lto rules
//   - ccache enabled: Compiler commands are prefixed with ccache path
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

// Outputs returns the output paths for shared libraries.
// Returns nil if the module has no name (invalid module).
// Output format: lib{name}{suffix}.so
// The "lib" prefix is automatically added if the name doesn't already start with "lib".
// ArchSuffix is appended for multi-architecture builds (e.g., "_arm64").
//
// Parameters:
//   - m: Module being evaluated (must have "name" property)
//   - ctx: Rule render context with architecture suffix
//
// Returns:
//   - List containing the shared library output path (e.g., ["libfoo_arm64.so"])
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - Name without "lib" prefix: Automatically prepended (e.g., "foo" -> "libfoo")
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

// NinjaEdge generates ninja build edges for shared library compilation and linking.
// It compiles each source file to an object file, then links them into a shared library.
// Shared library dependencies (shared_libs) are linked with -l flags.
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs", optionally "shared_libs" properties)
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja build edge string for compilation and linking
//   - Empty string if module has no name or no source files
//
// Build algorithm:
//  1. Get module name and source files, exit early if missing
//  2. Determine LTO setting (module-level overrides context)
//  3. Select compile rule (cc_compile or cc_compile_lto)
//  4. Add C/C++ flags including LTO flags if enabled
//  5. Collect shared library dependencies and add -l flags to linker flags
//  6. Generate compile edges for each source file
//  7. Generate link edge (cc_shared or cc_link_lto) with all inputs
//  8. Generate thinlto_codegen edges if LTO is "thin"
//
// Edge cases:
//   - Empty srcs or name: Returns "" (nothing to compile)
//   - Module-level LTO overrides context LTO setting
//   - Shared libraries with shared_libs: Adds -l{depName} to ldflags
//   - C++ sources: Adds cppflags to compilation flags
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

	cflags := joinFlags(ctx.CFlags, getCflags(m), getCppflags(m), getUndefines(m))
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}
	// Add exported cflags from dependencies
	if ctx.ExportCFlags != "" {
		cflags = strings.TrimSpace(cflags + " " + ctx.ExportCFlags)
	}
	ldflags := joinFlags(ctx.LdFlags, getLdflags(m))
	// Add exported ldflags from dependencies
	if ctx.ExportLdFlags != "" {
		ldflags = strings.TrimSpace(ldflags + " " + ctx.ExportLdFlags)
	}

	var sharedInputs []string
	sharedLibs := GetListProp(m, "shared_libs")
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		sharedInputs = append(sharedInputs, sharedLibOutputName(depName, ctx.ArchSuffix))
		ldflags = joinFlags(ldflags, "-l"+depName)
	}

	var edges strings.Builder
	var objFiles = make([]string, 0, len(srcs))
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", obj, compileRule, filepath.Join(ctx.PathPrefix, src), cflags))
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

// Desc returns a short description of the build action for ninja's progress output.
// Returns "cc_shared" for shared library linking (srcFile == "").
// Returns "gcc" for individual source file compilation (srcFile != "").
//
// Parameters:
//   - m: Module being evaluated (unused in this implementation)
//   - srcFile: Source file path; empty means this is a linking step
//
// Returns:
//   - Description string for ninja's build log
func (r *ccLibraryShared) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cc_shared"
	}
	return "gcc"
}

// ccObject implements a C object file rule.
// This module type compiles source files to .o object files without creating a library.
// It is useful for compiling individual object files that can be linked later,
// or for modules that only need to produce object files without archiving.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C/C++ compilation with -MMD -MF for dependencies
//   - cc_compile_lto: Compilation with LTO enabled for whole-program optimization
//
// Unlike ccLibrary types, this does NOT generate archive or link rules.
// Each source file produces a separate .o output file.
type ccObject struct{}

// Name returns the module type name for cc_object.
// This name matches the module type in Blueprint files (e.g., cc_object { ... }).
// Object files are compiled individually and can be linked later into libraries or binaries.
//
// Returns:
//   - "cc_object" string identifying this build rule type
//
// Edge cases:
//   - None (returns constant string)
func (r *ccObject) Name() string { return "cc_object" }

// NinjaRule defines the ninja compilation rules for object files.
//
// The generated rules include:
//   - cc_compile: Standard C/C++ compilation with dependency tracking
//   - Uses -MMD -MF to generate .d dependency files
//   - deps = gcc tells ninja to parse GCC-style dependency files
//   - cc_compile_lto: Compilation with LTO enabled for whole-program optimization
//
// Unlike other cc types, this does NOT generate archive or link rules.
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags (CC, CXX, Ccache)
//
// Returns:
//   - Ninja rule definitions as formatted string
//
// Edge cases:
//   - ccache enabled: Compiler commands are prefixed with ccache path
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

// Outputs returns the output paths for object files.
// Returns nil if the module has no name (invalid module).
// If there is only one source file, returns a single .o file: {name}{suffix}.o
// If there are multiple source files, returns one .o file per source:
//
//	objectOutputName(name, src) for each src in srcs.
//
// Parameters:
//   - m: Module being evaluated (must have "name" and "srcs" properties)
//   - ctx: Rule render context with architecture suffix
//
// Returns:
//   - List of object file paths (e.g., ["foo_arm64.o"] or ["foo_src1.o", "foo_src2.o"])
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - Single source: Returns single output with architecture suffix
//   - Multiple sources: Returns multiple outputs, one per source file
//   - Empty srcs: Returns single output (will be empty compilation)
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

// NinjaEdge generates ninja build edges for object file compilation.
// Each source file is compiled to a separate .o object file.
// No archiving or linking is performed - only compilation.
//
// Parameters:
//   - m: Module being evaluated (must have "name" and "srcs" properties)
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja build edge string for compilation
//   - Empty string if module has no name or no source files
//
// Build algorithm:
//  1. Get module name and source files, exit early if missing
//  2. Determine LTO setting (module-level overrides context)
//  3. Select compile rule (cc_compile or cc_compile_lto)
//  4. Add C flags including LTO flags if enabled
//  5. If single source: Generate one build edge for combined output
//  6. If multiple sources: Generate one build edge per source file
//
// Edge cases:
//   - Empty srcs or name: Returns "" (nothing to compile)
//   - Module-level LTO overrides context LTO setting
//   - C++ sources: Uses cflags only (no cppflags for object type)
//   - Single source optimization: Uses combined output name {name}{suffix}.o
func (r *ccObject) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	// Exit early if module has no name or no source files.
	if name == "" || len(srcs) == 0 {
		return ""
	}

	// Determine LTO setting: module-level overrides context-level.
	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}
	// Get LTO flags for compile step; link flags ignored for object files.
	ltoCompileExtra, _ := ltoFlags(moduleLto)
	compileRule := "cc_compile"
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
	}

	// Build C flags: combine context flags and module cflags (no cppflags for object type).
	cflags := joinFlags(ctx.CFlags, getCflags(m), getUndefines(m))
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}

	// Single source optimization: use combined output name {name}{suffix}.o.
	if len(srcs) == 1 {
		out := r.Outputs(m, ctx)[0]
		return fmt.Sprintf("build %s: %s %s\n flags = %s\n", out, compileRule, srcs[0], cflags)
	}

	// Multiple sources: generate one build edge per source file.
	var edges strings.Builder
	outputs := r.Outputs(m, ctx)
	for i, src := range srcs {
		// Build edge: compile each source to its corresponding object file.
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", outputs[i], compileRule, filepath.Join(ctx.PathPrefix, src), cflags))
	}
	return edges.String()
}

// Desc returns a short description of the build action for ninja's progress output.
// Always returns "gcc" since cc_object only performs compilation, never linking.
//
// Parameters:
//   - m: Module being evaluated (unused in this implementation)
//   - srcFile: Source file path (unused, always returns "gcc")
//
// Returns:
//   - "gcc" as the description for all object file compilations
func (r *ccObject) Desc(m *parser.Module, srcFile string) string {
	return "gcc"
}

// ccBinary implements a C/C++ binary rule.
// This module type produces an executable binary from source files and dependencies.
// It supports both static and dynamic linking depending on the dependency types.
//
// NinjaRule generates these ninja rules:
//   - cc_compile: Standard C/C++ compilation with -MMD -MF for dependencies
//   - cc_compile_lto: Compilation with LTO enabled for whole-program optimization
//   - cc_link: Link object files into executable
//   - Uses response files (@$out.rsp) to handle large numbers of inputs
//   - cc_link_lto: Link with LTO enabled (requires LTO-compatible linker)
//   - cc_archive: Archive object files for static library dependencies
//   - thinlto_codegen: Generate thin LTO intermediate files for incremental builds
//
// The binary links both static libraries (deps) and shared libraries (shared_libs).
// Static libraries are linked directly, while shared libraries use -l flags.
type ccBinary struct{}

// Name returns the module type name for cc_binary.
// This name matches the module type in Blueprint files (e.g., cc_binary { ... }).
// Binaries are executable programs that can be run directly on the target system.
//
// Returns:
//   - "cc_binary" string identifying this build rule type
//
// Edge cases:
//   - None (returns constant string)
func (r *ccBinary) Name() string { return "cc_binary" }

// NinjaRule defines the ninja compilation and linking rules for binaries.
//
// The generated rules include:
//   - cc_compile: Standard C/C++ compilation with dependency tracking
//   - Uses -MMD -MF to generate .d dependency files
//   - deps = gcc tells ninja to parse GCC-style dependency files
//   - cc_compile_lto: Compilation with LTO enabled for whole-program optimization
//   - cc_link: Link object files into executable
//   - Uses response files (@$out.rsp) to handle large numbers of inputs
//   - cc_link_lto: Link with LTO enabled (requires LTO-compatible linker)
//   - Note: Different from cc_shared, uses $in directly (not response file)
//   - cc_archive: Archive object files for static library dependencies
//   - restat = true tells ninja to recheck timestamp after archiving
//   - thinlto_codegen: Generate thin LTO intermediate files
//
// Parameters:
//   - ctx: Rule render context with toolchain and flags (CC, CXX, AR, Lto, Ccache)
//
// Returns:
//   - Ninja rule definitions as formatted string
//
// Edge cases:
//   - LTO enabled: Adds LTO linker flags to cc_link_lto rule
//   - ccache enabled: Compiler commands are prefixed with ccache path
//   - Custom AR: Uses ltoArchiveCmd to select appropriate archiver for LTO
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
command = %s rcs $out $in
restat = true
rule thinlto_codegen
command = %s -flto=thin -c -fthin-link=$out.thinlto.o $in -o $out
%s
`, ccCompilerCmd(ctx, "cc"), ccCompilerCmd(ctx, "cc"), linkSuffix, linkSuffix, arCmd, ccCompilerCmd(ctx, "cc"), "")
}

// Outputs returns the output paths for binaries.
// Returns nil if the module has no name (invalid module).
// Output format: {name}{suffix}
// No file extension since binaries are platform-specific executables.
// ArchSuffix is appended for multi-architecture builds (e.g., "_arm64").
//
// Parameters:
//   - m: Module being evaluated (must have "name" property)
//   - ctx: Rule render context with architecture suffix
//
// Returns:
//   - List containing the binary output path (e.g., ["foo_arm64"])
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - No "lib" prefix added (binaries don't use library naming convention)
func (r *ccBinary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ctx.ArchSuffix}
}

// NinjaEdge generates ninja build edges for binary compilation and linking.
// It compiles each source file to an object file, then links them into an executable.
// Both static libraries (deps) and shared libraries (shared_libs) are linked.
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs", optionally "deps" and "shared_libs")
//   - ctx: Rule render context with toolchain and flags
//
// Returns:
//   - Ninja build edge string for compilation and linking
//   - Empty string if module has no name or no source files
//
// Build algorithm:
//  1. Get module name, source files, and dependencies, exit early if missing
//  2. Determine compiler type (C vs C++) from file extensions
//  3. Select compile rule (cc_compile or cc_compile_lto)
//  4. Add C/C++ flags including LTO flags if enabled
//  5. Collect static library dependencies (deps) as .a files
//  6. Collect shared library dependencies (shared_libs) and add -l flags
//  7. Generate compile edges for each source file
//  8. Generate link edge (cc_link or cc_link_lto) with all inputs
//  9. Generate thinlto_codegen edges if LTO is "thin"
//
// Edge cases:
//   - Empty srcs or name: Returns "" (nothing to compile)
//   - Module-level LTO overrides context LTO setting
//   - Static deps: Linked as implicit inputs (| separator in ninja)
//   - Shared libs: Adds -l{depName} to ldflags
//   - C++ sources: Adds cppflags to compilation flags
func (r *ccBinary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	// Exit early if module has no name or no source files.
	if name == "" || len(srcs) == 0 {
		return ""
	}

	// Check for architecture variants (e.g., host, target, arm64, x86_64).
	// If variants exist, generate separate build edges for each variant.
	variants := getGoTargetVariants(m)
	if len(variants) == 0 {
		// No variants: generate build edges for the default (empty variant).
		return r.ninjaEdgeForVariant(m, ctx, "")
	}

	// Multiple variants: generate build edges for each variant in sorted order.
	// Sorting ensures deterministic output regardless of map iteration order.
	var edges strings.Builder
	sorted := make([]string, len(variants))
	copy(sorted, variants)
	sort.Strings(sorted)
	for _, v := range sorted {
		edges.WriteString(r.ninjaEdgeForVariant(m, ctx, v))
	}
	return edges.String()
}

// ninjaEdgeForVariant generates ninja build edges for a specific variant of cc_binary.
// It compiles source files and links them into an executable binary.
// Both static libraries (deps) and shared libraries (shared_libs) are linked.
// Variant-specific toolchain settings (cc, cxx, sysroot, cflags, ldflags) are applied if available.
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs", "deps", "shared_libs" properties)
//   - ctx: Rule render context with toolchain and flags (CC, CXX, CFlags, LdFlags, Lto, etc.)
//   - variant: Variant name (e.g., "host", "target_arm64"); empty for default variant
//
// Returns:
//   - Ninja build edge string for the variant (compilation + link edges)
//   - Empty string if module has no name or no source files
//
// Build algorithm:
//  1. Get module name, source files, and dependencies, exit early if missing
//  2. Detect compiler type (C vs C++) from file extensions
//  3. Determine LTO setting (module-level overrides context)
//  4. Select compile rule (cc_compile or cc_compile_lto)
//  5. Apply variant-specific toolchain (cc, cxx, sysroot) if available
//  6. Build C/C++ flags including LTO, sysroot, variant-specific cflags
//  7. Collect static library dependencies (deps) as .a files
//  8. Collect shared library dependencies (shared_libs) and add -l flags
//  9. Generate compile edges for each source file
//  10. Generate link edge (cc_link or cc_link_lto) with all inputs
//  11. Generate thinlto_codegen edges if LTO is "thin"
//
// Edge cases:
//   - Empty variant: Uses default toolchain from context
//   - Variant with custom toolchain: Overrides default CC/CXX/Sysroot
//   - Module-level LTO overrides context LTO setting
//   - Static deps: Linked as implicit inputs (| separator in ninja)
//   - Shared libs: Adds -l{depName} to ldflags
//   - C++ sources: Adds cppflags to compilation flags
func (r *ccBinary) ninjaEdgeForVariant(m *parser.Module, ctx RuleRenderContext, variant string) string {
	name := getName(m)
	srcs := getSrcs(m)
	deps := GetListProp(m, "deps")
	sharedLibs := GetListProp(m, "shared_libs")

	compilerType := detectCompilerType(srcs)

	moduleLto := getLto(m)
	if moduleLto == "" {
		moduleLto = ctx.Lto
	}

	ltoCompileExtra, _ := ltoFlags(moduleLto)
	compileRule := "cc_compile"
	if moduleLto != "" {
		compileRule = "cc_compile_lto"
	}

	// Use variant-specific toolchain if available
	cc := ctx.CC
	cxx := ctx.CXX
	sysroot := ctx.Sysroot
	if variant != "" {
		if v := getGoTargetProp(m, variant, "cc"); v != "" {
			cc = v
		}
		if v := getGoTargetProp(m, variant, "cxx"); v != "" {
			cxx = v
		}
		if v := getGoTargetProp(m, variant, "sysroot"); v != "" {
			sysroot = v
		}
	}

	compiler := cc
	if compilerType == "cpp" {
		compiler = cxx
	}
	if ctx.Ccache != "" {
		var b strings.Builder
		b.Grow(len(ctx.Ccache) + 1 + len(compiler))
		b.WriteString(ctx.Ccache)
		b.WriteString(" ")
		b.WriteString(compiler)
		compiler = b.String()
	}

	// Determine linker command: use LD if specified, otherwise use compiler
	linker := compiler
	if ctx.LD != "" {
		linker = ctx.LD
		if ctx.Ccache != "" {
			var b strings.Builder
			b.Grow(len(ctx.Ccache) + 1 + len(linker))
			b.WriteString(ctx.Ccache)
			b.WriteString(" ")
			b.WriteString(linker)
			linker = b.String()
		}
	}

	// Build flags
	cflags := joinFlags(ctx.CFlags, getCflags(m), getCppflags(m))
	if variant != "" {
		if v := getGoTargetProp(m, variant, "cflags"); v != "" {
			cflags = joinFlags(cflags, v)
		}
	}
	if ltoCompileExtra != "" {
		cflags = strings.TrimSpace(cflags + " " + ltoCompileExtra)
	}
	// Add sysroot to flags if specified
	if sysroot != "" {
		cflags = strings.TrimSpace(cflags + " --sysroot=" + sysroot)
	}
	// Add exported cflags from dependencies
	if ctx.ExportCFlags != "" {
		cflags = strings.TrimSpace(cflags + " " + ctx.ExportCFlags)
	}

	ldflags := joinFlags(ctx.LdFlags, getLdflags(m))
	if variant != "" {
		if v := getGoTargetProp(m, variant, "ldflags"); v != "" {
			ldflags = joinFlags(ldflags, v)
		}
	}
	// Add exported ldflags from dependencies
	if ctx.ExportLdFlags != "" {
		ldflags = strings.TrimSpace(ldflags + " " + ctx.ExportLdFlags)
	}

	var libFiles []string
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		if isNonLinkableDep(depName, ctx.Modules) {
			continue
		}
		libFiles = append(libFiles, staticLibOutputName(depName, ctx.ArchSuffix))
	}
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, sharedLibOutputName(depName, ctx.ArchSuffix))
		ldflags = joinFlags(ldflags, "-l"+depName)
	}

	// Generate compile edges for each source file.
	var edges strings.Builder
	var objFiles = make([]string, 0, len(srcs))

	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		// Build edge: compile source to object with flags.
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n", obj, compileRule, filepath.Join(ctx.PathPrefix, src), cflags))
	}

	// Generate binary link edge with all object files and library dependencies.
	out := r.Outputs(m, ctx)[0]
	allInputs := append(objFiles, libFiles...)

	linkRule := "cc_link"
	if moduleLto != "" {
		linkRule = "cc_link_lto"
	}
	edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", out, linkRule, strings.Join(allInputs, " "), ldflags, linker))

	if moduleLto == "thin" {
		for _, src := range srcs {
			obj := objectOutputName(name, src)
			codegen := obj + ".thinlto.o"
			edges.WriteString(fmt.Sprintf("build %s: thinlto_codegen %s\n", codegen, obj))
		}
	}

	return edges.String()
}

// Desc returns a short description of the build action for ninja's progress output.
// Returns "cc_link" for binary linking (srcFile == "").
// Returns "gcc" for individual source file compilation (srcFile != "").
//
// Parameters:
//   - m: Module being evaluated (unused in this implementation)
//   - srcFile: Source file path; empty means this is a linking step
//
// Returns:
//   - Description string for ninja's build log
func (r *ccBinary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "cc_link"
	}
	return "gcc"
}

// ccLibraryHeaders implements a C/C++ header library rule.
// This module type provides header files for other modules to include.
// It doesn't produce compiled output but exports include directories.
// Header libraries are used to share API definitions without creating binary dependencies.
//
// NinjaRule returns empty string (no compilation rules needed).
// NinjaEdge returns empty string (no build edges needed).
// Outputs returns the header filename for dependency tracking in the build graph.
//
// This type is useful for:
//   - Defining API interfaces without implementation
//   - Sharing header-only libraries (template libraries, inline functions)
//   - Creating dependency boundaries for include path management
type ccLibraryHeaders struct{}

// Name returns the module type name for cc_library_headers.
// This name is used to match module types in Blueprint files (e.g., cc_library_headers { ... }).
// Header-only modules don't produce compiled output but provide include paths for other modules.
func (r *ccLibraryHeaders) Name() string { return "cc_library_headers" }

// NinjaRule returns empty string since header libraries don't need compilation rules.
// Header-only modules don't produce object files or libraries.
//
// Parameters:
//   - ctx: Rule render context (unused)
//
// Returns:
//   - Empty string (no rules to define)
func (r *ccLibraryHeaders) NinjaRule(ctx RuleRenderContext) string { return "" }

// Outputs returns the header filename for dependency tracking.
// Returns nil if the module has no name (invalid module).
// Output format: {name}.h (a placeholder for dependency tracking in the build graph).
//
// Parameters:
//   - m: Module being evaluated (must have "name" property)
//   - ctx: Rule render context (unused)
//
// Returns:
//   - List containing the header filename (e.g., ["foo.h"])
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - The .h file may not actually exist; it's used for dependency tracking
func (r *ccLibraryHeaders) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".h"}
}

// NinjaEdge returns empty string since header libraries don't need build edges.
// Header-only modules don't produce build artifacts that need compilation.
//
// Parameters:
//   - m: Module being evaluated (unused)
//   - ctx: Rule render context (unused)
//
// Returns:
//   - Empty string (no build edges to generate)
func (r *ccLibraryHeaders) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string { return "" }

// Desc returns empty string since there are no build actions to describe.
// Header-only modules don't perform any build steps.
//
// Parameters:
//   - m: Module being evaluated (unused)
//   - srcFile: Source file path (unused)
//
// Returns:
//   - Empty string (no description needed)
func (r *ccLibraryHeaders) Desc(m *parser.Module, srcFile string) string { return "" }

// ccTestEdge generates a ninja build edge for a C/C++ test executable.
// This is a helper function used by the ccTestRule type to generate the
// actual build edges. It compiles source files and links them into a test binary.
// The test binary has the ".test" suffix and includes test-specific configurations.
//
// Build algorithm:
//  1. Get module name and source files, exit early if missing
//  2. Determine compiler type (C vs C++) from file extensions
//  3. Get test-specific flags (cflags, ldflags)
//  4. Select compile rule (cc_compile or cc_compile_lto based on LTO)
//  5. Collect static library dependencies (deps) as .a files
//  6. Collect shared library dependencies (shared_libs) and add -l flags
//  7. Generate compile edges for each source file
//  8. Generate link edge (cc_link or cc_link_lto) with all inputs
//  9. Add test_args variable if test_options property is set
//  10. Generate thinlto_codegen edges if LTO is "thin"
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs", optionally "deps", "shared_libs", "test_options")
//   - ctx: Rule rendering context with toolchain and flags (CC, CXX, CFlags, LdFlags, Lto, etc.)
//
// Returns:
//   - Ninja build edge string for test compilation and linking
//   - Empty string if module has no name or no source files
//
// Edge cases:
//   - Empty srcs: Returns "" (nothing to compile)
//   - Missing name: Returns "" (cannot determine output path)
//   - LTO "thin": Generates additional thinlto_codegen edges for incremental LTO
//   - C++ sources: Adds cppflags to compilation flags
//   - Static deps: Linked as implicit inputs (| separator in ninja)
//   - Shared libs: Adds -l{depName} to ldflags
//   - Test options: Adds test_args variable to build edge
func ccTestEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	cflags := joinFlags(getCflags(m), getUndefines(m), ctx.CFlags)
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
	// Determine linker command: use LD if specified, otherwise use compiler
	linker := compiler
	if ctx.LD != "" {
		linker = ctx.LD
		if ctx.Ccache != "" {
			var b strings.Builder
			b.Grow(len(ctx.Ccache) + 1 + len(linker))
			b.WriteString(ctx.Ccache)
			b.WriteString(" ")
			b.WriteString(linker)
			linker = b.String()
		}
	}
	deps := GetListProp(m, "deps")
	sharedLibs := GetListProp(m, "shared_libs")
	var libFiles []string
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		if isNonLinkableDep(depName, ctx.Modules) {
			continue
		}
		libFiles = append(libFiles, staticLibOutputName(depName, ctx.ArchSuffix))
	}
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, sharedLibOutputName(depName, ctx.ArchSuffix))
		linkFlags = joinFlags(linkFlags, "-l"+depName)
	}
	// Generate compile edges for each source file.
	var edges strings.Builder
	var objFiles = make([]string, 0, len(srcs))
	for _, src := range srcs {
		obj := objectOutputName(name, src)
		objFiles = append(objFiles, obj)
		// Build edge: compile source to object with flags and compiler variables.
		edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", obj, compileRule, filepath.Join(ctx.PathPrefix, src), cflags, compiler))
	}
	// Test binary output: name.test with architecture suffix.
	out := name + ".test" + ctx.ArchSuffix
	// Combine all inputs: object files + static and shared library dependencies.
	allInputs := append(objFiles, libFiles...)
	// Select link rule based on LTO setting.
	linkRule := "cc_link"
	if moduleLto != "" {
		linkRule = "cc_link_lto"
	}
	// Build edge: link all inputs into test binary with flags and compiler variables.
	edges.WriteString(fmt.Sprintf("build %s: %s %s\n flags = %s\n CC = %s\n", ninjaEscapePath(out), linkRule, strings.Join(allInputs, " "), linkFlags, linker))
	// Add test-specific arguments if test_options property is set.
	if args := getTestOptionArgs(m); args != "" {
		edges.WriteString(fmt.Sprintf(" test_args = %s\n", args))
	}
	// Generate thinLTO codegen edges for incremental LTO optimization.
	if moduleLto == "thin" {
		for _, src := range srcs {
			obj := objectOutputName(name, src)
			codegen := obj + ".thinlto.o"
			edges.WriteString(fmt.Sprintf("build %s: thinlto_codegen %s\n", codegen, obj))
		}
	}
	return edges.String()
}
