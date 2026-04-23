// Package main implements minibp, a build system that generates Ninja build files from Blueprint definitions.
// It parses .bp files, resolves dependencies, handles architecture variants, and outputs build.ninja.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	buildlib "minibp/lib/build"
	"minibp/lib/namespace"
	"minibp/lib/parser"
	"minibp/lib/props"
	applib "minibp/lib/utils"
)

// Dependency injection points for file operations.
// These default to stdlib functions but can be replaced for testing.
var (
	// openInputFile opens a file for reading.
	// Used for dependency injection in tests.
	openInputFile func(path string) (io.ReadCloser, error) = func(path string) (io.ReadCloser, error) { return os.Open(path) }

	// createOutputFile creates a file for writing.
	// Used for dependency injection in tests.
	createOutputFile func(path string) (io.WriteCloser, error) = func(path string) (io.WriteCloser, error) { return os.Create(path) }

	// parseBlueprintFile parses a Blueprint file into an AST.
	// Used for dependency injection in tests.
	parseBlueprintFile = parser.ParseFile
)

// main is the entry point for the minibp command-line tool.
// It parses command-line flags, loads Blueprint definitions, and generates a Ninja build file.
// On success, it exits with code 0; on failure, it exits with code 1 and prints an error to stderr.
// The actual logic is delegated to run() to enable testing with custom stdout/stderr streams.
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the main logic function that processes command-line arguments and generates the build file.
//
// It handles the complete build pipeline:
//  1. ParseFlags: Extracts CLI arguments (input files, output path, architecture, toolchain settings)
//  2. parseDefinitionsFromFiles: Loads and parses all Blueprint .bp files into definition AST nodes
//  3. ProcessAssignments: Evaluates variable assignments (e.g., "my_var = "value"") across all definitions
//  4. CollectModules: Extracts module definitions, applies property overrides from command-line flags
//  5. BuildNamespace: Creates namespace map for soong_namespace resolution (e.g., //namespace:module)
//  6. BuildGraph: Constructs dependency graph, handles variant merging, glob expansion
//  7. NewGenerator: Creates ninja generator with build rules for each module type
//  8. GenerateNinjaFile: Writes build.ninja to the output path
//
// The function returns a descriptive error if any step fails; errors include context about the failure point.
// Edge cases: Empty input list returns error, parse errors are aggregated, incomplete ninja files are removed on failure.
func run(args []string, stdout, stderr io.Writer) error {
	// Step 1: Parse command-line flags into run configuration.
	// This includes: -v (version), -a (scan directory), -o (output file), -arch, -host, -os,
	// -cc, -cxx, -ar, -lto, -sysroot, -ccache, -variant, -product
	// ParseRunConfig returns error for invalid flags or missing required arguments.
	cfg, err := applib.ParseRunConfig(args, stderr)
	if err != nil {
		return err
	}
	// Handle version flag: print version string and exit early.
	// This allows checking the tool version without requiring input files.
	if cfg.ShowVersion {
		fmt.Fprintf(stdout, "minibp version %s\n", applib.GetVersion())
		return nil
	}

	// Create evaluator for processing Blueprint expressions.
	// The evaluator handles:
	//   - Variable substitution: "${my_var}" references
	//   - Operators: +, -, ==, !=, &&, ||, <, <=, >, >=
	//   - Built-in functions: len(), substring(), trim()
	//   - select() expressions with architecture-specific variants
	eval := applib.NewEvaluatorFromConfig(cfg)

	// Step 2-3: Parse all Blueprint files and process variable assignments.
	// parseDefinitionsFromFiles reads each .bp file, runs the parser, and collects definitions.
	// Definitions include: module declarations, variable assignments, soong_namespace blocks.
	// ProcessAssignmentsFromDefs evaluates variable assignments in dependency order.
	allDefs, err := parseDefinitionsFromFiles(cfg.Inputs)
	if err != nil {
		return err
	}
	eval.ProcessAssignmentsFromDefs(allDefs)

	// Step 4: Collect modules from definitions.
	// CollectModulesWithNames extracts all module definitions, applies:
	//   - Command-line property overrides (-property flag)
	//   - Architecture variant filtering (arm64, arm, x86_64, etc.)
	//   - host_supported/device_supported filtering
	// Returns all modules that match the current build configuration.
	buildOpts := cfg.BuildOptions()
	modules, err := buildlib.CollectModulesWithNames(allDefs, eval, buildOpts, func(m *parser.Module, name string) string {
		return props.GetStringPropEval(m, name, eval)
	})
	if err != nil {
		return err
	}

	// Step 5: Build namespace map for soong_namespace resolution.
	// Namespaces allow module references like "//namespace:module_name" syntax.
	// The map associates namespace names with their module collections.
	// Used by the dependency graph to resolve cross-namespace references.
	namespaces := namespace.BuildMap(modules, func(m *parser.Module, name string) string {
		return props.GetStringPropEval(m, name, eval)
	})

	// Step 6: Construct dependency graph.
	// BuildGraph handles:
	//   - Resolving module dependencies (srcs, deps, lib deps)
	//   - Merging variants for multi-architecture builds
	//   - Expanding globs (e.g., "*.java") to file lists
	//   - Detecting circular dependencies
	//   - Topological sorting for correct build order
	graph := buildlib.BuildGraph(modules, namespaces, eval)

	// Step 7: Create ninja generator.
	// NewGenerator initializes rules for each module type:
	//   - cc: C/C++ compilation (libtool, link)
	//   - go: Go compilation
	//   - java: Java compilation and dex
	//   - python: Python scripts
	//   - filegroup: File group aggregation
	//   - genrule: Custom build commands
	//   - prebuilt: Pre-built libraries
	//   - defaults: Default property inheritance
	gen := buildlib.NewGenerator(graph, modules, buildOpts)

	// Step 8: Generate and write the Ninja build file.
	// generateNinjaFile writes the complete build.ninja including:
	//   - Build rules for all module types
	//   - Build statements for each module
	//   - Variable definitions (paths, flags, toolchain settings)
	// On error, incomplete output file is removed to prevent stale builds.
	if err := generateNinjaFile(cfg.OutFile, gen); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Generated %s with %d modules\n", cfg.OutFile, len(modules))
	return nil
}

// parseDefinitionsFromFiles reads and parses all Blueprint files.
//
// It opens each file path, runs the parser, and collects the resulting definitions.
// File reading uses openInputFile dependency injection for testability.
// Parse errors are collected and aggregated rather than failing immediately,
// allowing all files to be checked in one pass.
//
// Parameters:
//   - files: List of file paths to parse.
//
// Returns:
//   - allDefs: Combined slice of all definitions from all files.
//   - error: nil if all files parse successfully; otherwise aggregated parse errors.
//
// Edge cases:
//   - Missing files return error immediately.
//   - Parse errors in one file don't stop processing of other files.
//   - File handle is always closed, even on parse errors.
//   - Close errors after parse errors are reported but don't mask the original parse error.
func parseDefinitionsFromFiles(files []string) ([]parser.Definition, error) {
	var allDefs []parser.Definition
	var parseErrors []string

	for _, file := range files {
		// Open file for reading.
		// Returns error if file doesn't exist or lacks read permissions.
		f, err := openInputFile(file)
		if err != nil {
			return nil, fmt.Errorf("error opening %s: %w", file, err)
		}

		// Parse the Blueprint file into an AST.
		// The parser handles:
		//   - Lexical analysis (tokens, strings, numbers)
		//   - Syntax parsing (module declarations, property values)
		//   - Expression evaluation (variables, operators, select())
		// Returns error for syntax errors or evaluation failures.
		parsedFile, parseErr := parseBlueprintFile(f, file)
		closeErr := f.Close()
		if parseErr != nil {
			// Collect parse error rather than failing immediately.
			// This allows checking all files in one pass.
			parseErrors = append(parseErrors, fmt.Sprintf("parse error in %s: %v", file, parseErr))
			continue
		}
		if closeErr != nil {
			// Report close error but don't mask earlier parse error.
			return nil, fmt.Errorf("error closing %s: %w", file, closeErr)
		}
		// Append definitions from this file to the combined result.
		allDefs = append(allDefs, parsedFile.Defs...)
	}

	// Report all collected parse errors at once.
	if len(parseErrors) > 0 {
		return nil, fmt.Errorf("parsing failed: %s", strings.Join(parseErrors, "; "))
	}
	return allDefs, nil
}

// generateNinjaFile writes the Ninja build file to the specified path.
//
// It creates the output file, calls the generator to write build rules,
// and ensures the file is properly closed.
//
// Parameters:
//   - path: Output file path (usually "build.ninja").
//   - gen: Generator implementing Generate(io.Writer) error.
//
// Returns:
//   - nil on successful write and close.
//   - error on file creation, generation, or close failures.
//
// Edge cases:
//   - If generation fails, incomplete output file is removed.
//     This prevents stale builds that might use partially-written ninja file.
//   - Close error during cleanup doesn't mask the original generation error.
//   - Close error after successful generation is still reported.
func generateNinjaFile(path string, gen interface{ Generate(io.Writer) error }) error {
	out, err := createOutputFile(path)
	if err != nil {
		return fmt.Errorf("error creating output: %w", err)
	}

	genErr := gen.Generate(out)
	closeErr := out.Close()
	if genErr != nil {
		// Remove incomplete file to prevent stale builds.
		// Ninja will rebuild everything if the file is truncated/missing,
		// but a partial file could cause confusing errors.
		closeErr = os.Remove(path)
		if closeErr != nil {
			return fmt.Errorf("error generating ninja: %w; error removing incomplete file: %v", genErr, closeErr)
		}
		return fmt.Errorf("error generating ninja: %w", genErr)
	}
	if closeErr != nil {
		return fmt.Errorf("error closing output: %w", closeErr)
	}
	return nil
}
