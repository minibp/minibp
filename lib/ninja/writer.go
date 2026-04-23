// Package ninja provides functionality for generating Ninja build files.
// It handles writing well-formatted Ninja build file syntax with proper escaping,
// defines build rule interfaces, and manages the rule registry.
//
// Ninja is a build system that uses declarative build files with explicit dependency
// graphs. This package provides utilities for generating valid Ninja syntax including:
//   - Rule definitions (command templates)
//   - Build edges (input -> output transformations)
//   - Variable definitions
//   - Proper escaping of special characters ($ : # space)
//
// Key concepts:
//   - rule <name>: Defines a reusable command template
//   - build <out>: <rule> <in>: Declares a build edge (input -> output)
//   - <var> = <value>: Defines a variable for substitution
//   - $variable: Variable expansion (e.g., $in, $out, $flags)
//   - $$: Escaped dollar sign, $|: Order-only dependency
//
// Escaping rules:
//   - $ must be escaped as $$
//   - : must be escaped as $:
//   - # must be escaped as $#
//   - space must be escaped as $ (in paths)
package ninja

import (
	"fmt"
	"io"
	"strings"
)

// Writer wraps an io.Writer and provides methods to write Ninja build file syntax.
// It handles escaping of special characters and proper formatting for all Ninja
// syntax elements including rules, build edges, variables, and comments.
//
// The Writer is not safe for concurrent use from multiple goroutines.
// Create separate Writer instances for each output file or use proper synchronization.
type Writer struct {
	w io.Writer
}

// NewWriter creates a new Writer that writes Ninja syntax to the provided writer.
//
// The returned Writer can be used to write rules, build edges, variables, and comments.
// All output is written directly to the underlying writer without buffering.
//
// Parameters:
//   - w: The underlying io.Writer to write Ninja syntax to.
//
// Returns:
//   - A new Writer instance configured to write to the provided writer.
//
// Example:
//
//	writer := ninja.NewWriter(buildFile)
//	writer.Rule("cc", "gcc -c $in -o $out")
//	writer.Build("main.o", "cc", []string{"main.c"}, nil)
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// ninjaEscape escapes special characters in Ninja build file values.
// Ninja uses $ for variable expansion, : for separators, and # for comments.
// This function escapes these characters by prefixing them with $.
//
// Escaping rules:
//   - "$" becomes "$$" (escapes dollar sign for literal $)
//   - ":" becomes "$:" (escapes colon which is a Ninja syntax character)
//   - "#" becomes "$#" (escapes hash which starts comments in Ninja)
//
// Parameters:
//   - s: The string to escape.
//
// Returns:
//   - The escaped string with special characters properly handled.
//
// Edge cases:
//   - Empty strings return empty strings
//   - Already-escaped characters are double-escaped ($$ becomes $$$$)
//   - Does not escape spaces (use ninjaEscapePath for paths)
func ninjaEscape(s string) string {
	replacer := strings.NewReplacer(
		"$", "$$",
		":", "$:",
		"#", "$#",
	)
	return replacer.Replace(s)
}

// ninjaEscapePath escapes a path for use in Ninja build files.
// It escapes special characters and also escapes spaces.
// Spaces are escaped as "$ " to prevent them from being treated as separators
// by the Ninja parser.
//
// This function first applies ninjaEscape to handle $ : # characters,
// then replaces spaces with "$ " for path-specific escaping.
//
// Parameters:
//   - s: The path string to escape.
//
// Returns:
//   - The escaped path string safe for use in Ninja build files.
//
// Example:
//
//	"my file.c" -> "my$ file.c"
//	"$HOME/foo" -> "$$HOME/foo"
func ninjaEscapePath(s string) string {
	return strings.ReplaceAll(ninjaEscape(s), " ", "$ ")
}

// escapeList applies ninjaEscapePath to each string in the values slice.
// It returns a new slice with all values properly escaped for Ninja paths.
//
// This is a convenience function for processing lists of file paths,
// ensuring each path is properly escaped for Ninja syntax.
//
// Parameters:
//   - values: Slice of path strings to escape.
//
// Returns:
//   - A new slice with each path string escaped using ninjaEscapePath.
//
// Note:
//   - The returned slice is a newly allocated slice.
//   - The original slice is not modified.
func escapeList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, v := range values {
		result = append(result, ninjaEscapePath(v))
	}
	return result
}

// Rule writes a Ninja rule definition to the build file.
// A rule defines a command template that can be reused across multiple build edges.
// Rules are the fundamental building blocks of Ninja build files.
//
// The name parameter specifies the rule name (e.g., "cc_compile", "link", "jar").
// The command parameter specifies the command template to execute.
// Rule templates can use Ninja variables like $in (input file), $out (output file),
// $in_newer (input files newer than output), and $out_oldest (oldest output file).
//
// Additional deps can be provided to specify dependency files (e.g., .d files for
// header dependencies). When deps are specified, Ninja will track them for automatic
// rebuilds when dependencies change.
//
// Parameters:
//   - name: The rule name (e.g., "cc_compile").
//   - command: The command template to execute.
//   - deps: Optional dependency files (e.g., ".d" files for header deps).
//
// Example:
//
//	writer.Rule("cc", "gcc -c $in -o $out", "deps.mk")
//	writer.Rule("link", "gcc $in -o $out")
func (w *Writer) Rule(name, command string, deps ...string) {
	fmt.Fprintf(w.w, "rule %s\n", ninjaEscapePath(name))
	fmt.Fprintf(w.w, "  command = %s\n", ninjaEscape(command))
	// Only write deps if non-empty and not an empty string
	// Empty deps can occur when a rule doesn't track dependencies
	if len(deps) > 0 && deps[0] != "" {
		fmt.Fprintf(w.w, "  deps = %s\n", strings.Join(escapeList(deps), " "))
	}
	fmt.Fprintln(w.w)
}

// Build writes a Ninja build edge to the build file.
// A build edge defines a transformation from inputs to output using a rule.
// Build edges are the core of Ninja's dependency graph.
//
// The output parameter specifies the output file path.
// The rule parameter specifies which rule to use for building.
// The inputs parameter specifies the input files needed for this build edge.
// The deps parameter specifies additional file dependencies that trigger rebuilds
// when changed (tracked dependencies, not order-only).
//
// Parameters:
//   - output: The output file path produced by this build edge.
//   - rule: The name of the rule to use for building.
//   - inputs: Slice of input file paths required for this build.
//   - deps: Slice of additional dependency file paths that trigger rebuilds.
//
// Ninja syntax:
//   - build <output>: <rule> <inputs> | <deps>
//
// Example:
//
//	writer.Build("main.o", "cc", []string{"main.c"}, []string{"header.h"})
//	Result: build main.o: cc main.c | header.h
func (w *Writer) Build(output, rule string, inputs []string, deps []string) {
	fmt.Fprintf(w.w, "build %s: %s", ninjaEscapePath(output), ninjaEscapePath(rule))
	// Write inputs after rule name, separated by spaces
	if len(inputs) > 0 {
		fmt.Fprintf(w.w, " %s", strings.Join(escapeList(inputs), " "))
	}
	// Write tracked dependencies after | separator
	// Tracked deps cause rebuild when changed (unlike order-only deps)
	if len(deps) > 0 {
		fmt.Fprintf(w.w, " | %s", strings.Join(escapeList(deps), " "))
	}
	fmt.Fprintln(w.w)
	fmt.Fprintln(w.w)
}

// BuildWithVars writes a Ninja build edge with additional variables.
// This is similar to Build but allows specifying custom variables for this
// specific build edge. Variables are scoped to this edge only.
//
// The orderOnly parameter specifies dependencies that must be built first
// but don't cause rebuilds when they change (order-only dependencies).
// These are marked with || syntax in Ninja.
//
// Variables like "flags", "cflags", "ldflags" can be defined to pass
// custom parameters to the rule for this specific build edge.
//
// Parameters:
//   - output: The output file path.
//   - rule: The rule name.
//   - inputs: Input file paths.
//   - orderOnly: Order-only dependencies (build first, don't trigger rebuild).
//   - vars: Map of variable name to value for this edge.
//
// Ninja syntax:
//   - build <output>: <rule> <inputs> || <orderOnly>
//     <var1> = <value1>
//     <var2> = <value2>
func (w *Writer) BuildWithVars(output, rule string, inputs []string, orderOnly []string, vars map[string]string) {
	fmt.Fprintf(w.w, "build %s: %s", ninjaEscapePath(output), ninjaEscapePath(rule))
	if len(inputs) > 0 {
		fmt.Fprintf(w.w, " %s", strings.Join(escapeList(inputs), " "))
	}
	// Order-only deps use || separator; they build first but don't cause rebuilds
	if len(orderOnly) > 0 {
		fmt.Fprintf(w.w, " || %s", strings.Join(escapeList(orderOnly), " "))
	}
	fmt.Fprintln(w.w)
	// Write edge-specific variables with proper indentation
	for k, v := range vars {
		fmt.Fprintf(w.w, "  %s = %s\n", ninjaEscape(k), ninjaEscape(v))
	}
	fmt.Fprintln(w.w)
}

// Variable writes a Ninja variable definition to the build file.
// Variables can be used in rules and build edges using $variable_name syntax.
// They are fundamental to Ninja's configuration system and allow parameterization
// of build commands.
//
// The name parameter specifies the variable name (typically lowercase with underscores).
// The value parameter specifies the variable value (can contain other variables).
//
// Parameters:
//   - name: The variable name.
//   - value: The variable value.
//
// Example:
//
//	writer.Variable("cc", "gcc")
//	writer.Variable("cflags", "-Wall -g")
//	writer.Variable("srcdir", ".")
func (w *Writer) Variable(name, value string) {
	fmt.Fprintf(w.w, "%s = %s\n", ninjaEscape(name), ninjaEscape(value))
}

// Comment writes a Ninja comment to the build file.
// Comments start with # and are ignored by Ninja but useful for documentation
// and readability of generated build files.
//
// If text is empty, writes an empty line for formatting purposes (visual spacing).
//
// Parameters:
//   - text: The comment text. If empty, writes a blank line.
//
// Example:
//
//	writer.Comment("Build rules for my project")
//	writer.Comment("")  // Empty line for spacing
func (w *Writer) Comment(text string) {
	if text != "" {
		fmt.Fprintf(w.w, "# %s\n", text)
	} else {
		fmt.Fprintln(w.w)
	}
}

// Desc writes a description comment for a build edge in Ninja format.
// This follows the Bazel/Blaze style description format used by many build tools
// and IDEs for displaying build progress.
//
// The description is written as a comment that ninja -v will display when
// building the target. This provides user-friendly build progress output.
//
// Parameters:
//   - sourceDir: The source directory containing the module.
//   - moduleName: The name of the module being built.
//   - action: The action being performed (e.g., "gcc", "ar", "javac", "jar").
//   - srcFile: Optional source file for this specific action (for detailed output).
//
// Example:
//
//	writer.Desc("myproject/lib", "mylib", "gcc", "source.c")
//	Output: # //myproject/lib:mylib gcc source.c
func (w *Writer) Desc(sourceDir, moduleName, action string, srcFile ...string) {
	srcStr := ""
	if len(srcFile) > 0 && srcFile[0] != "" {
		srcStr = " " + srcFile[0]
	}
	fmt.Fprintf(w.w, "# //%s:%s %s%s\n", sourceDir, moduleName, action, srcStr)
}

// Subninja includes another Ninja build file as a sub-build.
// This allows splitting build files into modular pieces while maintaining
// a single build invocation.
//
// The included sub-ninja file runs in a subenvironment, meaning variable
// definitions don't leak back to the parent. This is different from the
// C preprocessor include model.
//
// Parameters:
//   - path: The path to the sub-Ninja file to include.
//
// Example:
//
//	writer.Subninja("subdir/build.ninja")
func (w *Writer) Subninja(path string) {
	fmt.Fprintf(w.w, "subninja %s\n\n", ninjaEscapePath(path))
}

// Include includes a Ninja build file at the point where it's invoked.
// Unlike subninja, included files are processed in place with access to
// the same variable scope (variables can leak between files).
//
// Use Include for shared rules and variables that should be available
// in the current scope. Use Subninja for modular build file organization.
//
// Parameters:
//   - path: The path to the Ninja file to include.
//
// Warning:
//   - Variables defined in included files are visible to the including file.
//   - This can cause namespace pollution in large projects.
func (w *Writer) Include(path string) {
	fmt.Fprintf(w.w, "include %s\n\n", ninjaEscapePath(path))
}

// Phony creates a phony build target that aliases other targets.
// This is useful for creating convenience targets that aggregate multiple
// outputs or provide mnemonic names for build operations.
//
// The output parameter specifies the name of the phony target.
// The inputs parameter specifies the actual targets this phony target represents.
//
// Running "ninja output" will build all the input targets.
//
// Parameters:
//   - output: The name of the phony target.
//   - inputs: The actual targets this phony target represents.
//
// Example:
//
//	writer.Phony("all", []string{"lib.a", "main"})
//	writer.Phony("test", []string{"unit_tests", "integration_tests"})
func (w *Writer) Phony(output string, inputs []string) {
	fmt.Fprintf(w.w, "build %s: phony %s\n", ninjaEscapePath(output), strings.Join(escapeList(inputs), " "))
}

// Default specifies the default targets to build when running "ninja"
// without arguments. Multiple targets can be specified; Ninja will build
// the first one by default.
//
// This is typically used to specify the main binary or library as the
// default build target.
//
// Parameters:
//   - targets: Slice of target names to build by default.
//
// Example:
//
//	writer.Default([]string{"main", "mylib"})
//	Runs: ninja -> builds "main"
func (w *Writer) Default(targets []string) {
	fmt.Fprintf(w.w, "default %s\n", strings.Join(escapeList(targets), " "))
}
