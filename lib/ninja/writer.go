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
//
// The Writer type provides methods for writing all Ninja syntax elements.
// All output is written directly to the underlying writer without buffering.
//
// This file contains the Writer implementation for generating valid Ninja syntax.
// Writer provides methods for rules, build edges, variables, comments, and phony targets.
package ninja

import (
	"bufio"
	"bytes"
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
//
// The Writer provides methods for each Ninja syntax element:
//   - Rule(): Define command templates
//   - Build(): Create build edges
//   - BuildWithVars(): Create build edges with edge-local variables
//   - Variable(): Define variables
//   - Comment(): Add comments
//   - Desc(): Add build descriptions for ninja -v output
//   - Subninja(): Include sub-build files
//   - Include(): Include shared rules
//   - Phony(): Create phony targets
//   - Default(): Set default build targets
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
//
// Note:
//   - Non-buffered writers are wrapped with a 64KB bufio.Writer for performance.
//   - *bytes.Buffer and *strings.Builder are not buffered to avoid double buffering.
func NewWriter(w io.Writer) *Writer {
	switch w.(type) {
	case *bytes.Buffer, *strings.Builder:
		// Don't buffer strings.Builder or bytes.Buffer
	default:
	if _, ok := w.(*bufio.Writer); !ok { // Wrap non-buffered writers with 64KB buffer
		w = bufio.NewWriterSize(w, 64*1024)
	}
	}
	return &Writer{w: w}
}

// Flush writes any buffered data to the underlying writer.
// If the underlying writer is a *bufio.Writer (automatically added for non-buffered writers
// in NewWriter), this calls its Flush method to persist all data. For other writer types
// (*bytes.Buffer, *strings.Builder, pre-wrapped bufio.Writer), this is a no-op.
//
// You should call Flush before closing the underlying writer to ensure all generated
// Ninja syntax is written. The 64KB buffer added in NewWriter requires flushing to empty.
//
// Returns:
//   - error: nil if flush succeeded, or the error from the underlying bufio.Writer.Flush.
//
// Edge cases:
//   - Non-bufio.Writer underlying writers return nil immediately.
//   - Errors from underlying Flush are propagated without modification.
//   - Multiple calls are safe; subsequent calls may error if the writer is closed.
func (w *Writer) Flush() error {
	if bw, ok := w.w.(*bufio.Writer); ok {
		return bw.Flush()
	}
	return nil
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
//
// Key design decisions:
//   - Uses strings.NewReplacer via the package-level ninjaEscapeReplacer for efficiency.
//     The replacer is created once and reused across all calls, avoiding allocation overhead.
//   - This function does NOT escape spaces because spaces in Ninja have different meanings
//     in different contexts (separators vs. path components). Use ninjaEscapePath for paths.
//
// ninjaEscapeReplacer is a pre-initialized string replacer for escaping Ninja special characters.
// It is defined as a package-level variable to avoid re-creating the replacer on every
// call to ninjaEscape, improving performance for frequent escape operations.
// The replacer handles three critical Ninja escape sequences:
//   - "$" → "$$" (escape dollar sign for literal $ in Ninja)
//   - ":" → "$:" (escape colon which is a Ninja syntax separator)
//   - "#" → "$#" (escape hash which starts comments in Ninja)
var ninjaEscapeReplacer = strings.NewReplacer(
	"$", "$$",
	":", "$:",
	"#", "$#",
)

func ninjaEscape(s string) string {
	return ninjaEscapeReplacer.Replace(s)
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

// shellEscape escapes a string for safe inclusion in a shell command.
// It wraps the string in single quotes and escapes any contained single quotes.
// This prevents the shell from interpreting special characters in the string.
//
// The escaping strategy:
//  1. If the string is empty, returns "”" (empty quoted string).
//  2. If the string contains no special characters, returns it unchanged (fast path).
//  3. Otherwise, wraps in single quotes and escapes internal single quotes as '\”.
//
// Examples:
//   - "hello world" → "'hello world'" (spaces are safe inside single quotes)
//   - "it's a trap" → "'it'\\”s a trap'" (single quote becomes '\”)
//   - "file$name" → "'file$name'" (dollar is safe inside single quotes)
//   - "" → "”" (empty string gets empty quotes)
//
// Parameters:
//   - s: The string to escape for shell usage.
//
// Returns:
//   - The shell-safe escaped string.
//
// Edge cases:
//   - Empty string returns "”" to represent an empty argument.
//   - Strings without special characters are returned unchanged (zero allocation).
//   - Already-quoted strings may be double-quoted (caller should avoid double-escaping).
//
// Key design decisions:
//   - Uses single quotes instead of double quotes because single quotes preserve
//     all characters literally (no variable expansion, no escape sequences).
//   - The fast-path check avoids allocating memory for safe strings.
//   - Does not escape newlines or other control characters beyond the listed set;
//     callers should sanitize input if needed.
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	// Check if the string contains any characters that need escaping.
	// This is a performance optimization to avoid unnecessary allocation.
	needsEscape := false
	for _, r := range s {
		if r == '\'' || r == '"' || r == '\\' || r == '$' || r == '`' ||
			r == '!' || r == '|' || r == '&' || r == ';' || r == '<' || r == '>' ||
			r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' ||
			r == '~' || r == '*' || r == '?' || r == '#' || r == '\n' || r == '\r' {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return s
	}

	// The logic for escaping is to replace every ' with '\''
	// and then wrap the entire string in single quotes.
	// For example, "it's a trap" becomes "'it'\\''s a trap'".
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
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
//
// Note:
//   - Writes an empty line after the rule for formatting.
//   - Deps are written only if the first deps entry is non-empty.
func (w *Writer) Rule(name, command string, deps ...string) {
	fmt.Fprintf(w.w, "rule %s\n", ninjaEscapePath(name))
	fmt.Fprintf(w.w, "  command = %s\n", ninjaEscape(command))
	// Only write deps if non-empty and not an empty string
	// Empty deps can occur when a rule doesn't track dependencies
	if len(deps) > 0 && deps[0] != "" { // Only write deps if non-empty and not empty string
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
//
// Note:
//   - Writes two empty lines after the build edge for formatting.
//   - Inputs and tracked deps are space-separated in the build line.
func (w *Writer) Build(output, rule string, inputs []string, deps []string) {
	fmt.Fprintf(w.w, "build %s: %s", ninjaEscapePath(output), ninjaEscapePath(rule))
	// Write inputs after rule name, separated by spaces
	if len(inputs) > 0 { // Write inputs after rule name, separated by spaces
		fmt.Fprintf(w.w, " %s", strings.Join(escapeList(inputs), " "))
	}
	// Write tracked dependencies after | separator
	// Tracked deps cause rebuild when changed (unlike order-only deps)
	if len(deps) > 0 { // Write tracked dependencies after | separator
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
//
// Note:
//   - Writes an empty line after the build edge for formatting.
//   - Order-only deps use || separator and don't trigger rebuilds.
func (w *Writer) BuildWithVars(output, rule string, inputs []string, orderOnly []string, vars map[string]string) {
	fmt.Fprintf(w.w, "build %s: %s", ninjaEscapePath(output), ninjaEscapePath(rule))
	if len(inputs) > 0 { // Write inputs after rule name, separated by spaces
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
