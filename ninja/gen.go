// ninja/gen.go - Ninja file generator
// This file generates Ninja build files from the module dependency graph.
// It translates minibp module definitions into Ninja syntax for building.
package ninja

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"minibp/parser"
)

// Graph is the interface needed for ninja generation.
// It provides the topological sort of module dependencies.
type Graph interface {
	// TopoSort returns modules organized by build level.
	// Each level can be built in parallel, but levels must be built in order.
	TopoSort() ([][]string, error)
}

// Generator creates ninja build files from module dependency graphs.
// It orchestrates the translation from high-level module definitions
// to low-level Ninja build rules and edges.
type Generator struct {
	graph      Graph                     // Dependency graph for topological sorting
	rules      map[string]BuildRule      // Map of rule name to rule implementation
	modules    map[string]*parser.Module // All modules in the build
	sourceDir  string                    // Source directory where .bp files are located
	outputDir  string                    // Output directory where ninja runs
	pathPrefix string                    // Prefix to prepend to source file paths
	regenCmd   string                    // Command to regenerate build.ninja
	inputFiles []string                  // Files that trigger regeneration
	outputFile string                    // Output file for regeneration rule
	workDir    string                    // Working directory for custom rules
	toolchain  Toolchain                 // Compiler toolchain configuration
	arch       string                    // Target architecture
}

// Toolchain holds compiler/tool configuration for cross-compilation.
// This allows targeting different architectures and using different compilers.
type Toolchain struct {
	CC      string   // C compiler command (e.g., gcc, clang)
	CXX     string   // C++ compiler command (e.g., g++, clang++)
	AR      string   // Static library archiver (e.g., ar, llvm-ar)
	CFlags  []string // Extra global C/C++ compiler flags
	LdFlags []string // Extra global linker flags
}

// NewGenerator creates a new Generator with the given graph and rules.
// The graph provides dependency ordering, rules map module types to implementations,
// and modules contains all the module definitions.
func NewGenerator(g Graph, rules map[string]BuildRule, modules map[string]*parser.Module) *Generator {
	return &Generator{
		graph:     g,
		rules:     rules,
		modules:   modules,
		sourceDir: ".",
		outputDir: ".",
	}
}

// SetSourceDir sets the source directory where .bp (Blueprint) files are located.
// This is used for computing relative paths to source files.
func (g *Generator) SetSourceDir(dir string) {
	g.sourceDir = dir
}

// SetOutputDir sets the output directory where ninja will run.
// This is used for computing relative paths from the build directory.
func (g *Generator) SetOutputDir(dir string) {
	g.outputDir = dir
}

// SetPathPrefix sets the prefix to prepend to source file paths.
// This is useful when the build directory is different from the source directory.
func (g *Generator) SetPathPrefix(prefix string) {
	g.pathPrefix = prefix
}

// SetRegen sets the command and files for auto-regeneration of build.ninja.
// When any input file changes, ninja will re-run minibp to regenerate the build file.
func (g *Generator) SetRegen(cmd string, files []string, output string) {
	g.regenCmd = cmd
	g.inputFiles = files
	g.outputFile = output
}

// SetWorkDir sets the working directory for custom rules.
// This is used by custom rules that need to glob files in the source tree.
func (g *Generator) SetWorkDir(dir string) {
	g.workDir = dir
}

// SetToolchain sets the compiler toolchain configuration.
// This overrides the default GNU toolchain with custom compilers.
func (g *Generator) SetToolchain(t Toolchain) {
	g.toolchain = t
}

// SetArch sets the target architecture for cross-compilation.
// This appends an architecture suffix to output binaries.
func (g *Generator) SetArch(arch string) {
	g.arch = arch
}

// DefaultToolchain returns a Toolchain with common GNU development tools.
// This is used when no custom toolchain is specified.
func DefaultToolchain() Toolchain {
	return Toolchain{
		CC:  "gcc",
		CXX: "g++",
		AR:  "ar",
	}
}

// getRelativePath returns the relative path from the output directory to a file in the source directory.
// This is used when the build directory differs from the source directory.
func (g *Generator) getRelativePath(file string) string {
	if g.sourceDir == g.outputDir {
		return file
	}
	absSource, _ := filepath.Abs(g.sourceDir)
	absOutput, _ := filepath.Abs(g.outputDir)
	if rel, err := filepath.Rel(absOutput, absSource); err == nil {
		if rel == "." {
			return file
		}
		return filepath.Join(rel, file)
	}
	return file
}

// collectIncludePaths recursively collects export_include_dirs from a module and its dependencies.
// These directories are added to the compiler's include path (-I flags).
// It traverses cc_library_headers, header_libs, shared_libs, and deps to find all exported headers.
func (g *Generator) collectIncludePaths(moduleName string, visited map[string]bool) []string {
	if visited[moduleName] {
		return nil
	}
	visited[moduleName] = true

	m, ok := g.modules[moduleName]
	if !ok || m == nil {
		return nil
	}

	var includes []string
	seen := make(map[string]bool)

	// Check if this is a cc_library_headers module
	if m.Type == "cc_library_headers" {
		dirs := getExportIncludeDirs(m)
		for _, dir := range dirs {
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	// Get direct export_include_dirs
	dirs := getExportIncludeDirs(m)
	for _, dir := range dirs {
		if !seen[dir] {
			includes = append(includes, dir)
			seen[dir] = true
		}
	}

	// Collect directories from exported_headers (individual .h files)
	exportedHeaders := getExportedHeaders(m)
	for _, h := range exportedHeaders {
		dir := filepath.Dir(h)
		if dir != "" && dir != "." && !seen[dir] {
			includes = append(includes, dir)
			seen[dir] = true
		}
	}

	// Collect from header_libs (cc_library_headers dependencies)
	headerLibs := GetListProp(m, "header_libs")
	for _, dep := range headerLibs {
		depName := strings.TrimPrefix(dep, ":")
		depIncludes := g.collectIncludePaths(depName, visited)
		for _, dir := range depIncludes {
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	// Collect from shared_libs (shared library dependencies exporting headers)
	sharedLibs := GetListProp(m, "shared_libs")
	for _, dep := range sharedLibs {
		depName := strings.TrimPrefix(dep, ":")
		depIncludes := g.collectIncludePaths(depName, visited)
		for _, dir := range depIncludes {
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	// Recursively collect from deps (option B: transitive)
	deps := GetListProp(m, "deps")
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		depIncludes := g.collectIncludePaths(depName, visited)
		for _, dir := range depIncludes {
			if !seen[dir] {
				includes = append(includes, dir)
				seen[dir] = true
			}
		}
	}

	return includes
}

// Generate writes the ninja build file content to the provided writer
func (g *Generator) Generate(w io.Writer) error {
	nw := NewWriter(w)

	nw.Comment("Generated by minibp")
	nw.Comment("")

	if g.regenCmd != "" && len(g.inputFiles) > 0 {
		fmt.Fprintf(w, "rule regen\n command = %s\n\n", ninjaEscape(g.regenCmd))
		fmt.Fprintf(w, "build %s: regen %s\n\n", ninjaEscape(g.outputFile), strings.Join(escapeList(g.inputFiles), " "))
	}

	if g.outputDir != "." && g.outputDir != "" {
		nw.Variable("builddir", ".")
		nw.Comment("")
	}

	ctx := g.ruleRenderContext()

	usedModuleTypes := g.collectUsedModuleTypes()

	writtenNinjaRules := make(map[string]bool)

	for _, moduleType := range usedModuleTypes {
		if rule, ok := g.rules[moduleType]; ok {
			ruleDef := rule.NinjaRule(ctx)
			if ruleDef == "" {
				continue
			}
			parts := strings.Split(ruleDef, "rule ")
			for i, part := range parts {
				if i == 0 && part == "" {
					continue
				}
				if part == "" {
					continue
				}
				lines := strings.SplitN(part, "\n", 2)
				ninjaRuleName := strings.TrimSpace(lines[0])
				if ninjaRuleName != "" && !writtenNinjaRules[ninjaRuleName] {
					writtenNinjaRules[ninjaRuleName] = true
					fmt.Fprintf(w, "rule %s", strings.TrimRight(part, " \t"))
				}
			}
		}
	}

	levels, err := g.graph.TopoSort()
	if err != nil {
		return err
	}

	var allOutputs []string
	seenCleanOutputs := make(map[string]bool)

	for _, level := range levels {
		for _, moduleName := range level {
			m, ok := g.modules[moduleName]
			if !ok || m == nil {
				continue
			}

			rule, ok := g.rules[m.Type]
			if !ok {
				continue
			}

			visited := make(map[string]bool)
			includes := g.collectIncludePaths(moduleName, visited)

			if strings.HasPrefix(m.Type, "cc_") || strings.HasPrefix(m.Type, "cpp_") {
				seen := make(map[string]bool)
				for _, inc := range includes {
					seen[inc] = true
				}
				if !seen["."] {
					includes = append(includes, ".")
				}
				srcs := getSrcs(m)
				for _, src := range srcs {
					dir := filepath.Dir(src)
					if dir != "." && !seen[dir] {
						includes = append(includes, dir)
						seen[dir] = true
					}
				}
			}

					sourceDir := g.sourceDir

					if sourceDir == "." {

						absPath, _ := filepath.Abs(g.sourceDir)

						sourceDir = filepath.Base(absPath)

					}

					edgeDef := rule.NinjaEdge(m, ctx)

					if edgeDef == "" && m.Type != "cc_library_headers" {

						continue

					}

			if edgeDef != "" {
				if strings.HasPrefix(m.Type, "java_") {
					edgeDef = g.addJavaDepsToEdge(m, edgeDef)
				}
				edgeDef = g.addIncludesToEdge(edgeDef, includes)
			}

			for _, out := range collectBuildOutputs(edgeDef) {
				if !seenCleanOutputs[out] {
					seenCleanOutputs[out] = true
					allOutputs = append(allOutputs, out)
				}
			}

			srcs := getSrcs(m)
			if len(srcs) == 0 {
				desc := rule.Desc(m, "")
				if desc != "" {
					nw.Desc(sourceDir, moduleName, desc, "")
				}
			} else {
				for _, src := range srcs {
					desc := rule.Desc(m, src)
					if desc != "" {
						nw.Desc(sourceDir, moduleName, desc, src)
					}
				}
			}

			fmt.Fprint(w, g.adjustPaths(edgeDef))
		}
	}

	if len(allOutputs) > 0 {
		fmt.Fprintf(w, "\nrule clean\n command = %s\n", cleanCommand(allOutputs))
		fmt.Fprintf(w, "\nbuild clean: clean\n")
	}

	for _, level := range levels {
		for _, moduleName := range level {
			m, ok := g.modules[moduleName]
			if !ok || m == nil {
				continue
			}
			rule, ok := g.rules[m.Type]
			if !ok {
				continue
			}
			edgeDef := rule.NinjaEdge(m, ctx)
			if edgeDef == "" && m.Type != "cc_library_headers" {
				continue
			}
			if edgeDef == "" {
				continue
			}
			outputs := rule.Outputs(m, ctx)
			if len(outputs) == 0 {
				continue
			}
			skip := false
			for _, out := range outputs {
				if out == moduleName {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			escapedOutputs := make([]string, 0, len(outputs))
			for _, out := range outputs {
				escapedOutputs = append(escapedOutputs, g.adjustBuildPath(out, true))
			}
			fmt.Fprintf(w, "build %s: phony %s\n", ninjaEscapePath(moduleName), strings.Join(escapedOutputs, " "))
		}
	}

	return nil
}

func archFlags(arch string) (cflags []string, ldflags []string) {
	switch arch {
	case "x86":
		return []string{"-m32"}, []string{"-m32"}
	case "x86_64":
		return []string{"-m64"}, []string{"-m64"}
	case "arm":
		return []string{"-march=armv7-a"}, []string{"-march=armv7-a"}
	case "arm64":
		return []string{"-march=armv8-a"}, []string{"-march=armv8-a"}
	default:
		return nil, nil
	}
}

func (g *Generator) ruleRenderContext() RuleRenderContext {
	tc := g.toolchain
	if tc.CC == "" {
		tc.CC = "gcc"
	}
	if tc.CXX == "" {
		tc.CXX = "g++"
	}
	if tc.AR == "" {
		tc.AR = "ar"
	}
	archCFlags, archLdFlags := archFlags(g.arch)
	tc.CFlags = append(append([]string{}, tc.CFlags...), archCFlags...)
	tc.LdFlags = append(append([]string{}, tc.LdFlags...), archLdFlags...)

	ctx := DefaultRuleRenderContext()
	ctx.CC = tc.CC
	ctx.CXX = tc.CXX
	ctx.AR = tc.AR
	ctx.CFlags = strings.Join(tc.CFlags, " ")
	ctx.LdFlags = strings.Join(tc.LdFlags, " ")
	if g.arch != "" {
		ctx.ArchSuffix = "_" + g.arch
	}
	return ctx
}

func shellQuote(arg string) string {
	return "\"" + strings.ReplaceAll(arg, "\"", "\\\"") + "\""
}

func cleanCommand(outputs []string) string {
	quoted := make([]string, 0, len(outputs))
	for _, out := range outputs {
		quoted = append(quoted, shellQuote(out))
	}
	if runtime.GOOS == "windows" {
		return "cmd /c del /q " + strings.Join(quoted, " ")
	}
	return "rm -f " + strings.Join(quoted, " ")
}

func collectBuildOutputs(edge string) []string {
	if edge == "" {
		return nil
	}

	var outputs []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(edge, "\n") {
		if !strings.HasPrefix(line, "build ") {
			continue
		}

		parsed, ok := parseBuildLine(line)
		if !ok {
			continue
		}

		for _, out := range parsed.Outputs {
			rawOut := ninjaUnescape(out)
			if !seen[rawOut] {
				seen[rawOut] = true
				outputs = append(outputs, rawOut)
			}
		}
	}

	return outputs
}

type parsedBuildLine struct {
	Outputs []string
	Rule    string
	Inputs  []string
	Deps    []string
}

func ninjaUnescape(s string) string {
	if s == "" {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '$' && i+1 < len(s) {
			i++
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func splitNinjaEscapedFields(s string) []string {
	if s == "" {
		return nil
	}

	var fields []string
	var cur strings.Builder
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '$' {
			escaped = true
			cur.WriteByte(ch)
			continue
		}
		if ch == ' ' || ch == '\t' {
			if cur.Len() > 0 {
				fields = append(fields, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(ch)
	}
	if escaped {
		cur.WriteByte('$')
	}
	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}
	return fields
}

func parseBuildLine(line string) (parsedBuildLine, bool) {
	if !strings.HasPrefix(line, "build ") {
		return parsedBuildLine{}, false
	}

	body := strings.TrimPrefix(line, "build ")
	separator := strings.Index(body, ": ")
	if separator == -1 {
		return parsedBuildLine{}, false
	}

	outputs := splitNinjaEscapedFields(strings.TrimSpace(body[:separator]))
	rest := strings.TrimSpace(body[separator+1:])
	parts := splitNinjaEscapedFields(rest)
	if len(outputs) == 0 || len(parts) == 0 {
		return parsedBuildLine{}, false
	}

	parsed := parsedBuildLine{Outputs: outputs, Rule: parts[0]}
	current := &parsed.Inputs
	for _, part := range parts[1:] {
		if part == "|" {
			current = &parsed.Deps
			continue
		}
		*current = append(*current, part)
	}
	return parsed, true
}

// addIncludesToEdge adds include directories only to compile commands.
func (g *Generator) addIncludesToEdge(edge string, includes []string) string {
	if len(includes) == 0 {
		return edge
	}

	includeFlags := ""
	relPrefix := g.getRelativePath("")
	for _, inc := range includes {
		if relPrefix != "" && relPrefix != "." {
			inc = filepath.Join(relPrefix, inc)
		}
		includeFlags += " -I" + inc
	}

	// Add to flags variable in edge
	lines := strings.Split(edge, "\n")
	compileFlags := false
	for i, line := range lines {
		if strings.HasPrefix(line, "build ") {
			compileFlags = strings.Contains(line, ": cc_compile ") || strings.Contains(line, ": cpp_compile ")
			continue
		}
		if compileFlags && strings.Contains(line, "flags =") && !strings.Contains(line, "#") {
			// Append includes to compile flags without affecting link/archive steps.
			lines[i] = line + includeFlags
		}
	}

	return strings.Join(lines, "\n")
}

func (g *Generator) javaDepOutputs(moduleName string, ctx RuleRenderContext) []string {
	m, ok := g.modules[moduleName]
	if !ok || m == nil {
		return nil
	}

	deps := GetListProp(m, "deps")
	if len(deps) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	outputs := make([]string, 0, len(deps))
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		depMod, ok := g.modules[depName]
		if !ok || depMod == nil {
			continue
		}
		rule, ok := g.rules[depMod.Type]
		if !ok {
			continue
		}
		for _, out := range rule.Outputs(depMod, ctx) {
			if strings.HasSuffix(out, ".jar") && !seen[out] {
				seen[out] = true
				outputs = append(outputs, out)
			}
		}
	}

	return outputs
}

func (g *Generator) addJavaDepsToEdge(m *parser.Module, edge string) string {
	depJars := g.javaDepOutputs(getName(m), g.ruleRenderContext())
	if len(depJars) == 0 {
		return edge
	}

	classpath := strings.Join(depJars, string(os.PathListSeparator))
	lines := strings.Split(edge, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "build ") {
			parts := strings.SplitN(line, ": ", 2)
			if len(parts) != 2 {
				continue
			}
			ruleAndInputs := strings.Fields(parts[1])
			if len(ruleAndInputs) == 0 {
				continue
			}
			ruleName := ruleAndInputs[0]
			if strings.HasPrefix(ruleName, "javac_") || strings.HasPrefix(ruleName, "jar_") {
				lines[i] = line + " | " + strings.Join(depJars, " ")
			}
			continue
		}

		if strings.Contains(line, "flags =") {
			lines[i] = line + " -classpath " + classpath
		}
	}

	return strings.Join(lines, "\n")
}

// adjustPaths updates paths in ninja edge to be relative to output directory
func (g *Generator) shouldPrefixInputPath(path string) bool {
	return !strings.HasPrefix(path, "$") &&
		!strings.HasPrefix(path, g.pathPrefix) &&
		!strings.HasPrefix(path, "/") &&
		!strings.HasPrefix(path, "..") &&
		!strings.HasSuffix(path, ".o") &&
		!strings.HasSuffix(path, ".jar") &&
		!strings.HasSuffix(path, ".stamp")
}

func (g *Generator) shouldPrefixOutputPath(path string) bool {
	return path != "" &&
		!strings.HasPrefix(path, g.pathPrefix) &&
		!strings.HasPrefix(path, "/") &&
		!strings.HasPrefix(path, "..") &&
		strings.Contains(path, "/")
}

func (g *Generator) adjustBuildPath(path string, isOutput bool) string {
	rawPath := ninjaUnescape(path)
	if isOutput {
		if g.shouldPrefixOutputPath(rawPath) {
			rawPath = g.pathPrefix + rawPath
		}
	} else if g.shouldPrefixInputPath(rawPath) {
		rawPath = g.pathPrefix + rawPath
	}
	return ninjaEscapePath(rawPath)
}

func (g *Generator) adjustPaths(edge string) string {
	if g.pathPrefix == "" {
		return escapeBuildLines(edge)
	}

	lines := strings.Split(edge, "\n")
	var adjustedLines []string

	for _, line := range lines {
		if !strings.HasPrefix(line, "build ") {
			adjustedLines = append(adjustedLines, line)
			continue
		}

		parsed, ok := parseBuildLine(line)
		if !ok {
			adjustedLines = append(adjustedLines, line)
			continue
		}

		outputs := make([]string, 0, len(parsed.Outputs))
		for _, output := range parsed.Outputs {
			outputs = append(outputs, g.adjustBuildPath(output, true))
		}

		inputs := make([]string, 0, len(parsed.Inputs))
		for _, input := range parsed.Inputs {
			inputs = append(inputs, g.adjustBuildPath(input, false))
		}

		deps := make([]string, 0, len(parsed.Deps))
		for _, dep := range parsed.Deps {
			deps = append(deps, g.adjustBuildPath(dep, false))
		}

		buildLine := "build " + strings.Join(outputs, " ") + ": " + ninjaEscapePath(parsed.Rule)
		if len(inputs) > 0 {
			buildLine += " " + strings.Join(inputs, " ")
		}
		if len(deps) > 0 {
			buildLine += " | " + strings.Join(deps, " ")
		}
		adjustedLines = append(adjustedLines, buildLine)
	}

	return strings.Join(adjustedLines, "\n")
}

func escapeBuildLines(edge string) string {
	lines := strings.Split(edge, "\n")
	for i, line := range lines {
		parsed, ok := parseBuildLine(line)
		if !ok {
			continue
		}

		outputs := make([]string, 0, len(parsed.Outputs))
		for _, output := range parsed.Outputs {
			outputs = append(outputs, ninjaEscapePath(ninjaUnescape(output)))
		}
		inputs := make([]string, 0, len(parsed.Inputs))
		for _, input := range parsed.Inputs {
			inputs = append(inputs, ninjaEscapePath(ninjaUnescape(input)))
		}
		deps := make([]string, 0, len(parsed.Deps))
		for _, dep := range parsed.Deps {
			deps = append(deps, ninjaEscapePath(ninjaUnescape(dep)))
		}

		buildLine := "build " + strings.Join(outputs, " ") + ": " + ninjaEscapePath(parsed.Rule)
		if len(inputs) > 0 {
			buildLine += " " + strings.Join(inputs, " ")
		}
		if len(deps) > 0 {
			buildLine += " | " + strings.Join(deps, " ")
		}
		lines[i] = buildLine
	}
	return strings.Join(lines, "\n")
}

// collectUsedModuleTypes returns a deduplicated list of module types used
func (g *Generator) collectUsedModuleTypes() []string {
	seen := make(map[string]bool)
	var result []string

	for _, m := range g.modules {
		if m == nil {
			continue
		}
		if !seen[m.Type] {
			seen[m.Type] = true
			result = append(result, m.Type)
		}
	}

	return result
}

// collectRulesForModule returns all ninja rule names used by a build rule
func (g *Generator) collectRulesForModule(rule BuildRule) []string {
	// Extract rule names from NinjaRule() output
	ruleDef := rule.NinjaRule(g.ruleRenderContext())
	var rules []string
	seen := make(map[string]bool)

	lines := strings.Split(ruleDef, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "rule ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				name := parts[1]
				if !seen[name] {
					seen[name] = true
					rules = append(rules, name)
				}
			}
		}
	}

	return rules
}
