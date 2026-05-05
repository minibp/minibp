// Package ninja implements Go build rules for minibp.
// This file provides compilation and linking rules for Go modules defined in Blueprint files.
// It handles the complete Go build pipeline: compiling Go sources with the go toolchain,
// and linking them into archives (.a) or executables.
//
// The Go rules support:
//   - go_library: Produces Go archive files (.a) for linking into other Go packages
//   - go_binary: Produces standalone executables for the target platform
//   - go_test: Produces test executables compiled with `go test -c`
//
// Key features:
//   - Cross-compilation via GOOS/GOARCH environment variables
//   - Multiple target variants via target { ... } properties
//   - Build flags (goflags) and linker flags (ldflags)
//   - Dependency resolution via deps property (links .a files)
//
// Build process overview:
//  1. go build -buildmode=archive compiles sources into .a archives (libraries)
//  2. go build compiles sources into standalone executables (binaries)
//  3. go test -c compiles test sources into test executables
//
// Key design decisions:
//   - Output naming: Uses "{name}{suffix}" for binaries, "{name}{suffix}.a" for libraries
//   - Variants: Cross-compilation targets specified via target { goos, goarch }
//   - Suffix format: "_{goos}_{goarch}" for variant-specific outputs
//   - Dependency linking: .a files linked via implicit dependencies (| separator in ninja)
//   - Package path: For tests, derived from first source file's directory
//
// Each Go module type implements the BuildRule interface:
//   - Name() string: Returns the module type name
//   - NinjaRule(ctx) string: Returns ninja rule definitions for go build commands
//   - Outputs(m, ctx) []string: Returns output file paths
//   - NinjaEdge(m, ctx) string: Returns ninja build edges for compilation/linking
//   - Desc(m, src) string: Returns a short description for ninja's progress output
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// goLibrary implements a Go library rule.
// Go libraries produce .a archive files that can be linked into binaries.
// They can have multiple target variants for cross-compilation.
//
// Supported properties:
//   - name: The library name (used for output file name)
//   - srcs: Source files to compile
//   - goflags: Additional flags passed to the Go compiler
//   - ldflags: Linker flags injected via -ldflags
//   - target: Map of target variants with goos/goarch properties
//
// Target variants example in Blueprint:
//
//	go_library {
//	  name: "mylib",
//	  srcs: ["mylib.go"],
//	  target: {
//	    linux_amd64: {
//	      goos: "linux",
//	      goarch: "amd64",
//	    },
//	    windows_386: {
//	      goos: "windows",
//	      goarch: "386",
//	    },
//	  },
//	}
//
// Implements the BuildRule interface:
//   - Name() string: Returns "go_library"
//   - NinjaRule(ctx) string: Returns ninja rule for go build -buildmode=archive
//   - Outputs(m, ctx) []string: Returns "{name}{suffix}.a"
//   - NinjaEdge(m, ctx) string: Returns ninja build edges for compilation
//   - Desc(m, src) string: Returns "go" as description
type goLibrary struct{}

// Name returns the module type name for go_library.
// This name is used to match module types in Blueprint files (e.g., go_library { ... }).
// Go libraries produce .a archives that can be linked into Go binaries.
func (r *goLibrary) Name() string { return "go_library" }

// NinjaRule defines the ninja compilation rule for Go archives.
// Uses "go build -buildmode=archive" to produce .a files.
// Environment variables ${GOOS_GOARCH} control cross-compilation target.
//
// The rule uses env command to set GOOS/GOARCH environment variables:
//
//	env ${GOOS_GOARCH} go build -buildmode=archive -o $out $in
//
// Parameters:
//   - ctx: Rule render context (not used directly, but required by interface)
//
// Returns:
//   - Ninja rule definition as formatted string
//
// Edge cases:
//   - This rule doesn't use toolchain context (always uses system Go toolchain)
//   - GOOS_GOARCH variable is set per-variant in NinjaEdge
func (r *goLibrary) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_build_archive
  command = $cmd

`
}

// Outputs returns the output paths for Go libraries.
// Returns nil if the module has no name (invalid module).
// Output format: {name}{suffix}.a
// Suffix is "_{goos}_{goarch}" when cross-compiling, empty otherwise.
//
// Parameters:
//   - m: Module being evaluated (must have "name" property)
//   - ctx: Rule render context with GOOS and GOARCH for cross-compilation
//
// Returns:
//   - List containing the Go archive output path (e.g., ["foo.a"] or ["foo_linux_amd64.a"])
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - No cross-compilation: Returns "{name}.a" without suffix
//   - Cross-compilation: Returns "{name}_{goos}_{goarch}.a" with context values
func (r *goLibrary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	goos, goarch, isCrossCompile := goosAndArch(ctx)
	if !isCrossCompile {
		return []string{fmt.Sprintf("%s.a", name)}
	}
	return []string{fmt.Sprintf("%s_%s_%s.a", name, goos, goarch)}
}

// NinjaEdge generates ninja build edges for Go library compilation.
// Handles multiple target variants for cross-compilation.
//
// Build algorithm:
//  1. Get module name and source files, exit early if missing
//  2. Get target variants from "target" property
//  3. If no variants, generate single edge for host platform
//     - Uses goos/goarch from context (or runtime defaults)
//  4. If variants exist, generate one edge per variant
//     - Sort variants alphabetically for deterministic output
//  5. Each variant calls ninjaEdgeForVariant
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs", optionally "target" properties)
//   - ctx: Rule render context with GOOS/GOARCH for default cross-compilation
//
// Returns:
//   - Ninja build edge string for compilation (may be multi-line for multiple variants)
//   - Empty string if module has no name or no source files
//
// Edge cases:
//   - Empty srcs or name: Returns "" (nothing to compile)
//   - No variants: Uses host platform (context GOOS/GOARCH or runtime defaults)
//   - Multiple variants: Generates sorted edges for deterministic output
//   - Variant with empty goos/goarch: Uses runtime.GOOS/GOARCH as defaults
func (r *goLibrary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	variants := getGoTargetVariants(m)

	// Always generate host build first (no variant)
	goos, goarch, isCrossCompile := goosAndArch(ctx)
	if !isCrossCompile {
		goos = ""
		goarch = ""
	}
	hostEdge := r.ninjaEdgeForVariant(m, ctx, goos, goarch)

	if len(variants) == 0 {
		return hostEdge
	}

	// Add host build first, then variant builds
	var edges strings.Builder
	edges.WriteString(hostEdge)

	sorted := make([]string, len(variants))
	copy(sorted, variants)
	sort.Strings(sorted)
	for _, v := range sorted {
		goos := getGoTargetProp(m, v, "goos")
		goarch := getGoTargetProp(m, v, "goarch")
		edges.WriteString(r.ninjaEdgeForVariant(m, ctx, goos, goarch))
	}
	return edges.String()
}

// ninjaEdgeForVariant generates a build edge for a specific Go target variant.
// Called once per variant or once for the host platform if no variants exist.
//
// Parameters:
//   - goos: Target operating system (e.g., "linux", "windows", "darwin")
//   - goarch: Target architecture (e.g., "amd64", "arm64", "386")
//
// Build edge format:
//
//	{name}{suffix}.a: Depends on source files
//	  flags = goflags
//	  cmd = [GOOS=X GOARCH=Y] go build -buildmode=archive [-ldflags "..."] -o $out $in
//	  GOOS_GOARCH = GOOS=X GOARCH=Y
//
// The GOOS_GOARCH variable is used by the ninja rule to set environment variables.
// The cmd variable provides the full command for display in ninja's output.
//
// Edge cases:
//   - Empty goos/goarch: No environment variables set, empty suffix
//   - Empty ldflags: Uses standard build command without -ldflags
//   - Non-empty ldflags: Injects -ldflags before -o using escapeLdflags
func (r *goLibrary) ninjaEdgeForVariant(m *parser.Module, ctx RuleRenderContext, goos, goarch string) string {
	name := getName(m)
	srcs := getSrcs(m)
	goflags := GetListProp(m, "goflags")
	ldflags := GetListProp(m, "ldflags")

	envVar, suffix, _, _ := goVariantEnvVars(goos, goarch)
	out := fmt.Sprintf("%s%s.a", name, suffix)
	cmd := goBuildCmd(envVar, goflags, ldflags, "-buildmode=archive", out, goPackageArg(m, ctx))

	return fmt.Sprintf("build %s: go_build_archive %s\n cmd = %s\n GOOS_GOARCH = %s\n",
		out, strings.Join(goSourceInputs(srcs, ctx.PathPrefix), " "), cmd, envVar)
}

// Desc returns a short description of the build action for ninja's progress output.
// Always returns "go" since go_library only performs Go compilation.
//
// Parameters:
//   - m: Module being evaluated (unused in this implementation)
//   - srcFile: Source file path (unused, always returns "go")
//
// Returns:
//   - "go" as the description for all Go library compilations
func (r *goLibrary) Desc(m *parser.Module, srcFile string) string {
	return "go"
}

// goBinary implements a Go binary rule.
// Go binaries are standalone executable files produced by the Go compiler.
// Unlike libraries, binaries are linked with all dependencies into a single output.
//
// Supported properties:
//   - name: The binary name (used for output file name)
//   - srcs: Source files to compile
//   - deps: List of go_library dependencies (linked as .a files)
//   - goflags: Additional flags passed to the Go compiler
//   - ldflags: Linker flags injected via -ldflags
//   - target: Map of target variants with goos/goarch properties
//
// Use cases:
//   - Command-line tools
//   - Server applications
//   - Build utilities and tools
//
// Implements the BuildRule interface:
//   - Name() string: Returns "go_binary"
//   - NinjaRule(ctx) string: Returns ninja rule for go build
//   - Outputs(m, ctx) []string: Returns "{name}{suffix}"
//   - NinjaEdge(m, ctx) string: Returns ninja build edges
//   - Desc(m, src) string: Returns "go" as description
type goBinary struct{}

// Name returns the module type name for go_binary.
// This name is used to match module types in Blueprint files (e.g., go_binary { ... }).
// Go binaries are standalone executables that can be run directly.
func (r *goBinary) Name() string { return "go_binary" }

// NinjaRule defines the ninja rules for Go binaries.
// Uses three rules:
//  1. go_build_archive: Compile main package to .a (go build -buildmode=archive)
//  2. go_write_importcfg: Write importcfg file with dep mappings
//  3. go_link: Link all .a files using go tool link
//
// Environment variables ${GOOS_GOARCH} control cross-compilation target.
//
// Parameters:
//   - ctx: Rule render context (not used directly, but required by interface)
//
// Returns:
//   - Ninja rule definitions as formatted string
//
// Edge cases:
//   - These rules don't use toolchain context (always uses system Go toolchain)
//   - GOOS_GOARCH variable is set per-variant in NinjaEdge
func (r *goBinary) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_build
  command = $cmd

rule go_build_archive
  command = $cmd

rule go_write_importcfg
  command = $cmd

rule go_link
  command = $cmd

`
}

// Outputs returns the output paths for Go binaries.
// Returns nil if the module has no name (invalid module).
// Output format: {name}{suffix}
// No file extension since Go binaries are platform-specific executables.
// Suffix is "_{goos}_{goarch}" when cross-compiling, empty otherwise.
//
// Parameters:
//   - m: Module being evaluated (must have "name" property)
//   - ctx: Rule render context with GOOS and GOARCH for cross-compilation
//
// Returns:
//   - List containing the Go binary output path (e.g., ["foo"] or ["foo_linux_amd64"])
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - No cross-compilation: Returns "{name}" without suffix
//   - Cross-compilation: Returns both ["{name}_{host_os}_{host_arch}", "{name}_{target_os}_{target_arch}"]
func outputNameForGoBinary(name, goos, goarch string) string {
	if goos == "windows" {
		return name + "_" + goos + "_" + goarch + ".exe"
	}
	return name + "_" + goos + "_" + goarch
}

func (r *goBinary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	goos, goarch, isCrossCompile := goosAndArch(ctx)
	if !isCrossCompile {
		return []string{name}
	}
	// Return both host and target outputs (with .exe for windows)
	hostOutput := outputNameForGoBinary(name, runtime.GOOS, runtime.GOARCH)
	targetOutput := outputNameForGoBinary(name, goos, goarch)
	return []string{hostOutput, targetOutput}
}

// NinjaEdge generates ninja build edges for Go binary compilation and linking.
// Handles multiple target variants for cross-compilation.
//
// Build algorithm:
//  1. Get module name and source files, exit early if missing
//  2. Get target variants from "target" property
//  3. If no variants, generate single edge for host platform
//     - Uses goos/goarch from context (or runtime defaults)
//  4. If variants exist, generate one edge per variant
//     - Sort variants alphabetically for deterministic output
//  5. Each variant calls ninjaEdgeForVariant
//  6. Dependencies (.a files) are linked as implicit inputs
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs", optionally "deps" and "target")
//   - ctx: Rule render context with GOOS/GOARCH for default cross-compilation
//
// Returns:
//   - Ninja build edge string for compilation and linking (may be multi-line)
//   - Empty string if module has no name or no source files
//
// Edge cases:
//   - Empty srcs or name: Returns "" (nothing to compile)
//   - No variants: Uses host platform (context GOOS/GOARCH or runtime defaults)
//   - No deps: Generates edge without implicit dependencies
//   - Multiple variants: Generates sorted edges for deterministic output
//   - Variant with empty goos/goarch: Uses runtime.GOOS/GOARCH as defaults
func (r *goBinary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	variants := getGoTargetVariants(m)
	_, _, isCrossCompile := goosAndArch(ctx)

	// Always generate host build first (using runtime platform)
	hostSuffix := "_" + runtime.GOOS + "_" + runtime.GOARCH
	hostEdge := r.buildGoBuild(m, ctx, name, srcs, hostSuffix)

	// If no explicit variants and no cross-compile context, just use simple build without suffix
	if len(variants) == 0 && !isCrossCompile {
		return r.buildGoBuild(m, ctx, name, srcs, "")
	}

	// If cross-compiling or has variants, include both host and target builds
	if len(variants) == 0 {
		// No explicit variants but cross-compiling - generate target build too
		var edges strings.Builder
		edges.WriteString(hostEdge)
		edges.WriteString(r.buildCrossCompile(m, ctx, name, srcs, ctx.GOOS, ctx.GOARCH))
		return edges.String()
	}

	// Has explicit variants - generate host + variants
	var edges strings.Builder
	edges.WriteString(hostEdge)

	sorted := make([]string, len(variants))
	copy(sorted, variants)
	sort.Strings(sorted)
	for _, v := range sorted {
		goos := getGoTargetProp(m, v, "goos")
		goarch := getGoTargetProp(m, v, "goarch")
		edges.WriteString(r.buildCrossCompile(m, ctx, name, srcs, goos, goarch))
	}
	return edges.String()
}

// buildGoBuild generates native Go binary build using regular go build
func (r *goBinary) buildGoBuild(m *parser.Module, ctx RuleRenderContext, name string, srcs []string, suffix string) string {
	goflags := GetListProp(m, "goflags")
	ldflags := GetListProp(m, "ldflags")
	pkg := goPackageArg(m, ctx)
	sourceInputs := goSourceInputs(srcs, ctx.PathPrefix)

	outName := name + suffix

	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s: go_build %s\n cmd = %s\n GOOS_GOARCH = \n",
		outName,
		strings.Join(sourceInputs, " "),
		goBuildCmd("", goflags, ldflags, "", outName, pkg)))
	return edges.String()
}

// buildCrossCompile generates cross-compiled Go binary build with GOOS/GOARCH
func (r *goBinary) buildCrossCompile(m *parser.Module, ctx RuleRenderContext, name string, srcs []string, goos, goarch string) string {
	goflags := GetListProp(m, "goflags")
	ldflags := GetListProp(m, "ldflags")

	envVar, suffix, _, _ := goVariantEnvVars(goos, goarch)
	if goos == "windows" {
		suffix += ".exe"
	}
	out := name + suffix
	pkg := goPackageArg(m, ctx)
	sourceInputs := goSourceInputs(srcs, ctx.PathPrefix)

	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s: go_build %s\n cmd = %s\n GOOS_GOARCH = %s\n",
		out,
		strings.Join(sourceInputs, " "),
		goBuildCmd(envVar, goflags, ldflags, "", out, pkg),
		envVar))
	return edges.String()
}

// Uses the importcfg-based linking approach (per tasks.md):
//  1. Compile main package to .a (go build -buildmode=archive)
//  2. Generate importcfg file with all dependency mappings
//  3. Link all .a files using go tool link
//
// Build edges generated:
//
//	{name}{suffix}.a: go_build_archive main_sources...
//	  flags = goflags
//	  cmd = [GOOS=X GOARCH=Y] go build -buildmode=archive -o $out $in
//	  GOOS_GOARCH = GOOS=X GOARCH=Y
//
//	importcfg_{name}{suffix}: generate_importcfg ...
//	  Generates importcfg file with all dep -> .a mappings
//
//	{name}{suffix}: go_link importcfg_{name}{suffix} || {name}{suffix}.a dep1.a dep2.a ...
//	  flags = goflags
//	  cmd = [GOOS=X GOARCH=Y] go tool link -importcfg $in -buildmode=exe -o $out {name}{suffix}.a
//	  GOOS_GOARCH = GOOS=X GOARCH=Y
//
// Parameters:
//   - goos: Target operating system (e.g., "linux", "windows")
//   - goarch: Target architecture (e.g., "amd64", "arm64")
//
// Edge cases:
//   - Empty goos/goarch: No environment variables set, no suffix
//   - Empty ldflags: Uses standard link command without -ldflags
//   - Non-empty ldflags: Injects -ldflags using escapeLdflags
//   - Empty deps: Only main.a is linked
func (r *goBinary) ninjaEdgeForVariant(m *parser.Module, ctx RuleRenderContext, goos, goarch string) string {
	name := getName(m)
	srcs := getSrcs(m)
	goflags := GetListProp(m, "goflags")
	ldflags := GetListProp(m, "ldflags")

	envVar, suffix, _, _ := goVariantEnvVars(goos, goarch)
	if goos == "windows" {
		suffix += ".exe"
	}
	out := name + suffix
	mainArchive := out + ".a"
	importcfgFile := "importcfg_" + out
	depArchives := collectGoDependencyArchives(m, ctx, suffix)

	// Edge 1: Compile main package to .a
	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s: go_build_archive %s\n cmd = %s\n GOOS_GOARCH = %s\n\n",
		mainArchive,
		strings.Join(goSourceInputs(srcs, ctx.PathPrefix), " "),
		goBuildCmd(envVar, goflags, ldflags, "-buildmode=archive", mainArchive, goPackageArg(m, ctx)),
		envVar))

	// Edge 2: Generate importcfg file
	edges.WriteString(fmt.Sprintf("build %s: go_write_importcfg %s\n cmd = %s\n\n",
		importcfgFile,
		strings.Join(goSourceInputs(srcs, ctx.PathPrefix), " "),
		goImportcfgCmd(envVar, m, ctx, importcfgFile, mainArchive, depArchives)))

	// Edge 3: Link using go tool link
	depFiles := make([]string, 0, len(depArchives))
	for _, dep := range depArchives {
		depFiles = append(depFiles, dep.Archive)
	}
	depClause := " | " + importcfgFile
	if len(depFiles) > 0 {
		depClause += " " + strings.Join(depFiles, " ")
	}
	edges.WriteString(fmt.Sprintf("build %s: go_link %s%s\n cmd = %s\n GOOS_GOARCH = %s\n",
		out, mainArchive, depClause, goLinkCmd(envVar, ldflags, out, importcfgFile, mainArchive), envVar))

	return edges.String()
}

// Desc returns a short description of the build action for ninja's progress output.
// Always returns "go" since go_binary only performs Go compilation/linking.
//
// Parameters:
//   - m: Module being evaluated (unused in this implementation)
//   - srcFile: Source file path (unused, always returns "go")
//
// Returns:
//   - "go" as the description for all Go binary builds
func (r *goBinary) Desc(m *parser.Module, srcFile string) string {
	return "go"
}

// goTest implements a Go test rule.
// Go test binaries are compiled test executables produced by `go test -c`.
// Test files are identified by the _test.go suffix convention.
//
// Supported properties:
//   - name: The test binary name (used for output file name)
//   - srcs: Source files to compile (including _test.go files)
//   - goflags: Additional flags passed to `go test`
//   - ldflags: Linker flags injected via -ldflags
//   - target: Map of target variants with goos/goarch properties
//
// Unlike goBinary, tests use `go test -c` which:
//   - Automatically includes test dependencies
//   - Compiles test files (*_test.go)
//   - Produces a standalone test executable
//
// Implements the BuildRule interface:
//   - Name() string: Returns "go_test"
//   - NinjaRule(ctx) string: Returns ninja rule for go test -c
//   - Outputs(m, ctx) []string: Returns "{name}{suffix}.test"
//   - NinjaEdge(m, ctx) string: Returns ninja build edges
//   - Desc(m, src) string: Returns "go test" as description
type goTest struct{}

// Name returns the module type name for go_test.
// This name is used to match module types in Blueprint files (e.g., go_test { ... }).
// Go test binaries are compiled with `go test -c` and include test frameworks.
func (r *goTest) Name() string { return "go_test" }

// NinjaRule defines the ninja test compilation rule.
// Uses `go test -c` to compile test executables.
// Environment variables ${GOOS_GOARCH} control cross-compilation target.
//
// The rule uses env command to set GOOS/GOARCH environment variables:
//
//	env ${GOOS_GOARCH} go test -c -o $out $pkg
//
// Note: Unlike go build, go test -c takes a package path ($pkg) not source files.
//
// Parameters:
//   - ctx: Rule render context (not used directly, but required by interface)
//
// Returns:
//   - Ninja rule definition as formatted string
//
// Edge cases:
//   - This rule doesn't use toolchain context (always uses system Go toolchain)
//   - GOOS_GOARCH variable is set per-variant in NinjaEdge
func (r *goTest) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_test
  command = $cmd

`
}

// Outputs returns the output paths for Go test binaries.
// Returns nil if the module has no name (invalid module).
// Output format: {name}{suffix}.test
// The ".test" extension identifies test executables.
// Suffix is "_{goos}_{goarch}" when cross-compiling, empty otherwise.
//
// Parameters:
//   - m: Module being evaluated (must have "name" property)
//   - ctx: Rule render context with GOOS and GOARCH for cross-compilation
//
// Returns:
//   - List containing the Go test binary output path (e.g., ["foo.test"] or ["foo_linux_amd64.test"])
//
// Edge cases:
//   - Empty name: Returns nil (cannot determine output path)
//   - No cross-compilation: Returns "{name}.test" without suffix
//   - Cross-compilation: Returns "{name}_{goos}_{goarch}.test" with context values
func (r *goTest) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	goos, goarch, isCrossCompile := goosAndArch(ctx)
	if !isCrossCompile {
		return []string{fmt.Sprintf("%s.test", name)}
	}
	// Return both host and target outputs
	// Windows test executables need .exe
	hostOutput := fmt.Sprintf("%s_%s_%s.test", name, runtime.GOOS, runtime.GOARCH)
	targetOutput := fmt.Sprintf("%s_%s_%s.test", name, goos, goarch)
	if runtime.GOOS == "windows" {
		hostOutput = fmt.Sprintf("%s_%s_%s.test.exe", name, runtime.GOOS, runtime.GOARCH)
	}
	if goos == "windows" {
		targetOutput = fmt.Sprintf("%s_%s_%s.test.exe", name, goos, goarch)
	}
	return []string{hostOutput, targetOutput}
}

// NinjaEdge generates ninja build edges for Go test compilation.
// Handles multiple target variants for cross-compilation.
//
// Build algorithm:
//  1. Get module name and source files, exit early if missing
//  2. Get target variants from "target" property
//  3. If no variants, generate single edge for host platform
//     - Uses goos/goarch from context (or runtime defaults)
//  4. If variants exist, generate one edge per variant
//     - Sort variants alphabetically for deterministic output
//  5. Each variant calls ninjaEdgeForVariant
//
// Note: Unlike goBinary, tests use pkg parameter (directory path) instead of
// individual source files, since `go test -c` expects a package path.
//
// Parameters:
//   - m: Module being evaluated (must have "name", "srcs", optionally "target" properties)
//   - ctx: Rule render context with GOOS/GOARCH for default cross-compilation
//
// Returns:
//   - Ninja build edge string for test compilation (may be multi-line for multiple variants)
//   - Empty string if module has no name or no source files
//
// Edge cases:
//   - Empty srcs or name: Returns "" (nothing to compile)
//   - No variants: Uses host platform (context GOOS/GOARCH or runtime defaults)
//   - Multiple variants: Generates sorted edges for deterministic output
//   - Variant with empty goos/goarch: Uses runtime.GOOS/GOARCH as defaults
func (r *goTest) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	variants := getGoTargetVariants(m)
	_, _, isCrossCompile := goosAndArch(ctx)

	// Generate host build first (using runtime platform)
	hostSuffix := "_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		hostSuffix += ".exe"
	}
	hostEdge := r.ninjaEdgeForVariant(m, ctx, runtime.GOOS, runtime.GOARCH)

	// If no explicit variants and no cross-compile context, just use simple build without suffix
	if len(variants) == 0 && !isCrossCompile {
		return r.ninjaEdgeForVariant(m, ctx, "", "")
	}

	// If cross-compiling or has variants, include both host and target builds
	if len(variants) == 0 {
		// No explicit variants but cross-compiling - generate target build too
		targetSuffix := "_" + ctx.GOOS + "_" + ctx.GOARCH
		if ctx.GOOS == "windows" {
			targetSuffix += ".exe"
		}
		var edges strings.Builder
		edges.WriteString(hostEdge)
		edges.WriteString(r.ninjaEdgeForVariant(m, ctx, ctx.GOOS, ctx.GOARCH))
		return edges.String()
	}

	// Has explicit variants - generate host + variants
	var edges strings.Builder
	edges.WriteString(hostEdge)

	sorted := make([]string, len(variants))
	copy(sorted, variants)
	sort.Strings(sorted)
	for _, v := range sorted {
		goos := getGoTargetProp(m, v, "goos")
		goarch := getGoTargetProp(m, v, "goarch")
		edges.WriteString(r.ninjaEdgeForVariant(m, ctx, goos, goarch))
	}
	return edges.String()
}

// ninjaEdgeForVariant generates a build edge for a specific Go test variant.
//
// The package path is derived from the first source file's directory:
//  1. Get the first source file from srcs
//  2. Extract its directory using filepath.Dir
//  3. Prepend "./" to get a relative package path
//
// Example: srcs[0] = "foo/bar_test.go"
//
//	pkgPath = "./" + filepath.Dir("foo/bar_test.go") = "./foo"
//
// Build edge format:
//
//	{name}{suffix}.test: go_test
//	  pkg = ./package_directory
//	  flags = goflags
//	  cmd = [GOOS=X GOARCH=Y] go test [-ldflags "..."] -c -o $out $pkg
//	  GOOS_GOARCH = GOOS=X GOARCH=Y
//
// Note: Unlike go build, go test -c takes a package path not source files.
//
// Parameters:
//   - goos: Target operating system (e.g., "linux", "windows")
//   - goarch: Target architecture (e.g., "amd64", "arm64")
//
// Edge cases:
//   - Empty goos/goarch: No environment variables set, no suffix
//   - Empty ldflags: Uses standard go test -c command without -ldflags
//   - Non-empty ldflags: Injects -ldflags using escapeLdflags
//   - First src must exist: Uses srcs[0] to derive package directory
func (r *goTest) ninjaEdgeForVariant(m *parser.Module, ctx RuleRenderContext, goos, goarch string) string {
	name := getName(m)
	srcs := getSrcs(m)
	goflags := GetListProp(m, "goflags")
	ldflags := GetListProp(m, "ldflags")

	envVar, suffix, _, _ := goVariantEnvVars(goos, goarch)
	out := fmt.Sprintf("%s%s.test", name, suffix)

	// For windows, test executables need .exe in the command line
	if goos == "windows" {
		out = name + suffix + ".test.exe"
	}

	cmd := goTestCmd(envVar, goflags, ldflags, out, goPackageArg(m, ctx))

	return fmt.Sprintf("build %s: go_test %s\n cmd = %s\n GOOS_GOARCH = %s\n",
		out, strings.Join(goSourceInputs(srcs, ctx.PathPrefix), " "), cmd, envVar)
}

// Desc returns a short description of the build action for ninja's progress output.
// Always returns "go test" since go_test performs Go test compilation.
//
// Parameters:
//   - m: Module being evaluated (unused in this implementation)
//   - srcFile: Source file path (unused, always returns "go test")
//
// Returns:
//   - "go test" as the description for all Go test compilations
func (r *goTest) Desc(m *parser.Module, srcFile string) string {
	return "go test"
}

// escapeLdflags escapes special characters in ldflags for use in ninja build files.
// Ninja uses $ for variables, so $ must be esaped as $$.
// Other characters that need escaping:
//   - Backslash (\): Escaped as \\ to prevent interpretation as escape character
//   - Double quote ("): Escaped as \" to preserve quotes in the string
//   - Backtick (`): Escaped as \` for shell compatibility
//   - Semicolon (;): Escaped as \; to prevent command separation
//
// Parameters:
//   - ldflags: The ldflags string to escape
//
// Returns:
//   - The escaped string safe for use in ninja variable assignments
//
// Edge cases:
//   - Empty string: Returns empty string unchanged
//   - String without special characters: Returns original string unchanged
func escapeLdflags(ldflags string) string {
	ldflags = strings.ReplaceAll(ldflags, `\`, `\\`)
	ldflags = strings.ReplaceAll(ldflags, `"`, `\"`)
	ldflags = strings.ReplaceAll(ldflags, "$", `\$`)
	ldflags = strings.ReplaceAll(ldflags, "`", "\\`")
	ldflags = strings.ReplaceAll(ldflags, ";", `\;`)
	return ldflags
}

// goDependencyArchive represents a Go library dependency with its associated metadata.
// Used when linking Go binaries to track all dependent libraries, their Go import paths,
// and the paths to their compiled .a archive files.
type goDependencyArchive struct {
	Name       string // Module name of the dependency (e.g., "mylib")
	ImportPath string // Go import path of the dependency (e.g., "github.com/user/mylib")
	Archive    string // Path to the compiled .a archive file (e.g., "mylib_linux_amd64.a")
}

// goBuildCmd constructs the command string for go build operations.
// Assembles the full go build command with environment variables, flags, and output paths.
//
// Parameters:
//   - envVar: Environment variable string for GOOS/GOARCH (empty for native builds)
//   - goflags: List of additional flags to pass to the Go compiler
//   - ldflags: List of linker flags to inject via -ldflags
//   - buildMode: Build mode flag (e.g., "-buildmode=archive", empty for default binary)
//   - out: Output file path for the compiled artifact
//   - pkg: Go package path to compile (e.g., "./mylib")
//
// Returns:
//   - Fully assembled go build command string ready for execution
//
// Edge cases:
//   - Empty envVar: No environment variables are prepended to the command
//   - Empty goflags/ldflags: Respective flags are omitted from the command
//   - Empty buildMode: Default build mode is used (no -buildmode flag)
//   - pkg must be a valid Go package path for compilation
func goBuildCmd(envVar string, goflags, ldflags []string, buildMode, out, pkg string) string {
	var parts []string
	if envVar != "" { // Prepend environment variables if cross-compiling
		parts = append(parts, "env", envVar)
	}
	parts = append(parts, "go", "build")
	if joined := strings.Join(goflags, " "); joined != "" { // Add goflags if provided
		parts = append(parts, joined)
	}
	if buildMode != "" { // Add build mode flag if specified
		parts = append(parts, buildMode)
	}
	if joined := strings.Join(ldflags, " "); joined != "" { // Add ldflags if provided
		parts = append(parts, "-ldflags", shellWord(joined))
	}
	parts = append(parts, "-o", shellWord(out), shellWord(pkg))
	return strings.Join(parts, " ")
}

// goTestCmd constructs the command string for go test -c operations.
// Assembles the full go test command to compile test executables with the go toolchain.
//
// Parameters:
//   - envVar: Environment variable string for GOOS/GOARCH (empty for native builds)
//   - goflags: List of additional flags to pass to the Go test tool
//   - ldflags: List of linker flags to inject via -ldflags
//   - out: Output file path for the test executable
//   - pkg: Go package path containing test files (e.g., "./mylib")
//
// Returns:
//   - Fully assembled go test command string ready for execution
//
// Edge cases:
//   - Empty envVar: No environment variables are prepended to the command
//   - Empty goflags/ldflags: Respective flags are omitted from the command
//   - pkg must be a valid Go package path containing test files
func goTestCmd(envVar string, goflags, ldflags []string, out, pkg string) string {
	var parts []string
	if envVar != "" { // Prepend environment variables if cross-compiling
		parts = append(parts, "env", envVar)
	}
	parts = append(parts, "go", "test")
	if joined := strings.Join(goflags, " "); joined != "" { // Add goflags if provided
		parts = append(parts, joined)
	}
	if joined := strings.Join(ldflags, " "); joined != "" { // Add ldflags if provided
		parts = append(parts, "-ldflags", shellWord(joined))
	}
	parts = append(parts, "-c", "-o", shellWord(out), shellWord(pkg))
	return strings.Join(parts, " ")
}

// goLinkCmd constructs the command string for go tool link operations.
// Assembles the full link command to link compiled .a archives into a Go executable.
//
// Parameters:
//   - envVar: Environment variable string for GOOS/GOARCH (empty for native builds)
//   - ldflags: List of linker flags to inject
//   - out: Output file path for the linked executable
//   - importcfg: Path to the importcfg file for dependency resolution
//   - archive: Path to the main package .a archive to link
//
// Returns:
//   - Fully assembled go link command string ready for execution
//
// Edge cases:
//   - Empty envVar: No environment variables are prepended to the command
//   - Empty ldflags: -ldflags flag is omitted from the command
//   - importcfg must be a valid importcfg file generated by goImportcfgCmd
func goLinkCmd(envVar string, ldflags []string, out, importcfg, archive string) string {
	var parts []string
	if envVar != "" { // Prepend environment variables if cross-compiling
		parts = append(parts, "env", envVar)
	}
	parts = append(parts, "go", "tool", "link", "-importcfg", shellWord(importcfg))
	if joined := strings.Join(ldflags, " "); joined != "" { // Add ldflags if provided
		parts = append(parts, joined)
	}
	parts = append(parts, "-buildmode=exe", "-o", shellWord(out), shellWord(archive))
	return strings.Join(parts, " ")
}

// goImportcfgCmd constructs the command string to generate a Go importcfg file.
// The importcfg file maps package import paths to their corresponding .a archive files,
// used by the Go linker to resolve dependencies.
//
// Parameters:
//   - envVar: Environment variable string for GOOS/GOARCH (empty for native builds)
//   - m: Module being evaluated (used to get package path)
//   - ctx: Rule render context (used to get module dependencies)
//   - out: Output path for the generated importcfg file
//   - mainArchive: Path to the main package's .a archive
//   - deps: List of goDependencyArchive entries for all dependent libraries
//
// Returns:
//   - Fully assembled shell command string to generate the importcfg file
//
// Edge cases:
//   - Empty envVar: No environment variables are prepended to the go list command
//   - No deps: Only the main package entry is written to the importcfg
//   - Existing importcfg file is overwritten on each run
func goImportcfgCmd(envVar string, m *parser.Module, ctx RuleRenderContext, out, mainArchive string, deps []goDependencyArchive) string {
	pkg := goPackageArg(m, ctx)
	template := "{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}}"
	commands := []string{
		fmt.Sprintf("%s > %s", goShellCmd(envVar, "go", "list", "-deps", "-f", template, pkg), shellWord(out)),
	}
	for _, dep := range deps {
		commands = append(commands, fmt.Sprintf(
			"printf '%%s\\n' %s >> %s",
			shellWord(fmt.Sprintf("packagefile %s=%s", dep.ImportPath, dep.Archive)),
			shellWord(out),
		))
	}
	commands = append(commands, fmt.Sprintf(
		"printf '%%s\\n' %s >> %s",
		shellWord(fmt.Sprintf("packagefile %s=%s", goImportPath(m, ctx), mainArchive)),
		shellWord(out),
	))
	return strings.Join(commands, " && ")
}

// goShellCmd constructs a shell-safe command string for Go toolchain operations.
// Prepends environment variables if provided, then escapes and joins all arguments.
//
// Parameters:
//   - envVar: Environment variable string for GOOS/GOARCH (empty to omit)
//   - args: List of command arguments to join (empty arguments are skipped)
//
// Returns:
//   - Shell-safe command string with escaped arguments
//
// Edge cases:
//   - Empty envVar: No environment variable prefix is added
//   - Empty args: Returns empty string (or envVar if set)
//   - Empty string arguments are skipped from the final command
func goShellCmd(envVar string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	if envVar != "" { // Prepend environment variables if cross-compiling
		parts = append(parts, "env", envVar)
	}
	for _, arg := range args {
		if arg == "" { // Skip empty arguments continue
			continue
		}
		parts = append(parts, shellWord(arg))
	}
	return strings.Join(parts, " ")
}

// shellWord escapes a string to be a single shell word, safe for use in ninja commands.
// Handles special characters that have meaning in shell or ninja variable syntax.
//
// Parameters:
//   - s: The string to escape
//
// Returns:
//   - Escaped string safe for use as a single shell argument
//
// Edge cases:
//   - Empty string: Returns "”" (empty quoted string)
//   - String with no special characters: Returns the original string unchanged
//   - String with spaces, quotes, $, etc.: Wrapped in single quotes with internal quotes escaped
func shellWord(s string) string {
	if s == "" { // Return empty quoted string for empty input
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool { // Check if string contains special shell characters
		switch r {
		case ' ', '\t', '\n', '\r', '\'', '"', '$', '&', ';', '(', ')', '<', '>', '|', '*', '?', '[', ']', '{', '}', '!', '`':
			return true
		}
		return false
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// goSourceInputs prepends the path prefix to all source file paths and normalizes slashes.
// Ensures all source paths are relative to the build root with forward slashes.
//
// Parameters:
//   - srcs: List of raw source file paths from the module
//   - pathPrefix: Prefix to prepend to each source path (e.g., "src")
//
// Returns:
//   - List of normalized, prefixed source file paths
//
// Edge cases:
//   - Empty srcs: Returns empty slice
//   - Empty pathPrefix: No prefix is added to source paths
//   - Backslashes in paths are converted to forward slashes
func goSourceInputs(srcs []string, pathPrefix string) []string {
	inputs := make([]string, 0, len(srcs))
	for _, src := range srcs {
		inputs = append(inputs, filepath.ToSlash(filepath.Join(pathPrefix, src))) // Normalize and prefix source path
	}
	return inputs
}

// goPackageArg constructs the Go package argument for go build/test commands.
// Derives the package path from the module's "pkg" property or source file directory,
// then normalizes it with the path prefix and forward slashes.
//
// Parameters:
//   - m: Module being evaluated (checks "pkg" property first)
//   - ctx: Rule render context (provides path prefix)
//
// Returns:
//   - Normalized Go package path string (e.g., "./mylib", ".")
//
// Edge cases:
//   - No "pkg" property and no srcs: Returns "."
//   - Package path starting with . or /: Returned as-is after prefix
//   - Other paths: Prepended with "./" to make them relative
func goPackageArg(m *parser.Module, ctx RuleRenderContext) string {
	pkg := getGoPackagePath(m)
	pkg = filepath.ToSlash(filepath.Clean(filepath.Join(ctx.PathPrefix, pkg)))
	if pkg == "" || pkg == "." { // Use "." for empty or current directory package
		return "."
	}
	if strings.HasPrefix(pkg, ".") || strings.HasPrefix(pkg, "/") { // Return as-is for relative/absolute paths
		return pkg
	}
	return "./" + pkg
}

// getGoPackagePath derives the Go package path from the module's properties or source files.
// Priority: "pkg" property > directory of first source file > "."
//
// Parameters:
//   - m: Module being evaluated
//
// Returns:
//   - Derived Go package path (forward-slashed, cleaned)
//
// Edge cases:
//   - "pkg" property set: Uses that value (trimmed and cleaned)
//   - No "pkg" and no srcs: Returns "."
//   - First source file's directory is empty/.: Returns "."
func getGoPackagePath(m *parser.Module) string {
	if pkg := strings.TrimSpace(GetStringProp(m, "pkg")); pkg != "" { // Use explicit pkg property if set
		return filepath.ToSlash(filepath.Clean(pkg))
	}
	srcs := getSrcs(m)
	if len(srcs) == 0 { // Fall back to "." if no source files
		return "."
	}
	dir := filepath.Dir(srcs[0])
	if dir == "" || dir == "." { // Use "." for empty/current directory
		return "."
	}
	return filepath.ToSlash(filepath.Clean(dir))
}

func goImportPath(m *parser.Module, ctx RuleRenderContext) string {
	if importPath := strings.TrimSpace(GetStringProp(m, "importpath")); importPath != "" {
		return importPath
	}

	pkg := getGoPackagePath(m)
	parts := make([]string, 0, 2)
	if ctx.GoImportPrefix != "" {
		parts = append(parts, strings.Trim(ctx.GoImportPrefix, "/"))
	}
	if pkg != "" && pkg != "." {
		parts = append(parts, strings.Trim(pkg, "/"))
	}
	rel := strings.Join(parts, "/")
	if ctx.GoModulePath != "" {
		if rel == "" {
			return ctx.GoModulePath
		}
		return strings.TrimRight(ctx.GoModulePath, "/") + "/" + rel
	}
	if rel != "" {
		return rel
	}
	if pkg != "" && pkg != "." {
		return pkg
	}
	return getName(m)
}

func collectGoDependencyArchives(m *parser.Module, ctx RuleRenderContext, suffix string) []goDependencyArchive {
	if len(ctx.Modules) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	deps := make([]goDependencyArchive, 0)
	var visit func(string)
	visit = func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true

		depModule := ctx.Modules[name]
		if depModule == nil {
			return
		}
		for _, dep := range GetListProp(depModule, "deps") {
			visit(strings.TrimPrefix(dep, ":"))
		}
		if depModule.Type != "go_library" {
			return
		}
		deps = append(deps, goDependencyArchive{
			Name:       name,
			ImportPath: goImportPath(depModule, ctx),
			Archive:    name + suffix + ".a",
		})
	}

	for _, dep := range GetListProp(m, "deps") {
		visit(strings.TrimPrefix(dep, ":"))
	}

	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Name < deps[j].Name
	})
	return deps
}

// goosAndArch returns the GOOS and GOARCH values from context, with defaults from runtime.
// It also returns whether this is a cross-compilation scenario.
// If goos/goarch are different from runtime, they're considered cross-compilation.
//
// The returned (goos, goarch) values are normalized:
//   - Empty goarch defaults to runtime.GOARCH
//   - Empty goos defaults to runtime.GOOS
//
// The isCrossCompile return value indicates whether the target differs
// from the host platform (for output suffix generation).
//
// Parameters:
//   - ctx: Rule render context containing GOOS and GOARCH values
//
// Returns:
//   - goos: The target operating system (normalized with default)
//   - goarch: The target architecture (normalized with default)
//   - isCrossCompile: true if target differs from host platform
//
// Edge cases:
//   - Empty GOOS/GOARCH in context: Uses runtime.GOOS/GOARCH as defaults
//   - Same as runtime: isCrossCompile returns false (native build)
//   - Different from runtime: isCrossCompile returns true (cross-compilation)
func goosAndArch(ctx RuleRenderContext) (goos, goarch string, isCrossCompile bool) {
	goos = ctx.GOOS
	goarch = ctx.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos == "" {
		goos = runtime.GOOS
	}
	isCrossCompile = goarch != runtime.GOARCH || goos != runtime.GOOS
	return
}

// goVariantEnvVars builds the environment variable string and suffix for Go targets.
// It accepts goos/goarch (which may be empty strings) and returns:
//   - envVar: The GOOS/GOARCH environment variable string (e.g., "GOOS=linux GOARCH=amd64")
//   - suffix: The output suffix (e.g., "_linux_amd64", or "" if no cross-compilation)
//   - normGoos, normGoarch: goos/goarch with defaults filled in
//
// The envVar is used by the ninja rule to set environment variables for go build.
// The suffix is appended to output file names for variant identification.
// The normalized values are used for consistent suffix generation.
//
// Parameters:
//   - goos: Target operating system (may be empty)
//   - goarch: Target architecture (may be empty)
//
// Returns:
//   - envVar: Environment variable string for GOOS/GOARCH (empty if no cross-compilation)
//   - suffix: Output file suffix (empty if no cross-compilation)
//   - normGoos: Normalized GOOS (with default filled in)
//   - normGoarch: Normalized GOARCH (with default filled in)
//
// Edge cases:
//   - Both empty: Returns all empty strings (native build)
//   - Only goos set: Returns only GOOS= env var
//   - Only goarch set: Returns only GOARCH= env var
//   - Both set: Returns combined "GOOS=X GOARCH=Y" env var
//   - Empty values default to runtime.GOOS/GOARCH for normalization
func goVariantEnvVars(goos, goarch string) (envVar string, suffix string, normGoos, normGoarch string) {
	normGoos = goos
	normGoarch = goarch
	if normGoarch == "" {
		normGoarch = runtime.GOARCH
	}
	if normGoos == "" {
		normGoos = runtime.GOOS
	}
	if goos != "" || goarch != "" {
		parts := []string{}
		if goos != "" {
			parts = append(parts, "GOOS="+goos)
		}
		if goarch != "" {
			parts = append(parts, "GOARCH="+goarch)
		}
		envVar = strings.Join(parts, " ")
		suffix = "_" + normGoos + "_" + normGoarch
	}
	return
}
