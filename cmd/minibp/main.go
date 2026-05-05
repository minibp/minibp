// Package main implements minibp, a build system that generates Ninja build files from Blueprint definitions.
// It parses .bp files, resolves dependencies, handles architecture variants, and outputs build.ninja.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	buildlib "minibp/lib/build"
	"minibp/lib/errors"
	"minibp/lib/incremental"
	"minibp/lib/namespace"
	"minibp/lib/parser"
	"minibp/lib/pathutil"
	"minibp/lib/props"
	applib "minibp/lib/utils"
)

// Dependency injection points for file operations.
// These default to stdlib functions but can be replaced for testing.
var (
	// openInputFile opens a file for reading.
	// Used for dependency injection in tests.
	openInputFile func(path string) (io.ReadCloser, error) = func(path string) (io.ReadCloser, error) {
		f, err := os.Open(filepath.Clean(path))
		if err != nil {
			return nil, err
		}
		cleanPath := pathutil.SanitizePath(path)
		if cleanPath != path {
			f.Close()
			return nil, errors.Config("invalid path: contains '..'").
				WithSuggestion("Use absolute paths or paths within the project directory")
		}
		return f, nil
	}

	// createOutputFile creates a file for writing.
	// Used for dependency injection in tests.
	createOutputFile func(path string) (io.WriteCloser, error) = func(path string) (io.WriteCloser, error) { return os.Create(path) }

	// parseBlueprintFile parses a Blueprint file into an AST.
	// Used for dependency injection in tests.
	parseBlueprintFile = parser.ParseFile
)

// main is the entry point for the minibp command-line tool.
//
// It parses command-line flags, loads Blueprint definitions, and generates a Ninja build file.
// The actual logic is delegated to run() to enable testing with custom stdout/stderr streams.
//
// Exit codes:
//   - 0: Success (build.ninja generated successfully).
//   - 1: Failure (error printed to stderr).
//
// The function passes os.Args[1:] (excluding program name) to run(),
// along with the standard stdout and stderr streams.
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

	// Step 2-3: Parse all Blueprint files (with incremental caching) and process variable assignments.
	// When .minibp/ exists, unchanged .bp files are loaded from cached JSON
	// instead of being reparsed. New or modified .bp files are parsed and cached.
	incManager, err := incremental.NewManager(cfg.SrcDir)
	if err != nil {
		// Failed to create incremental manager - likely output path issue.
		return errors.Config("failed to create incremental manager").
			WithCause(err).
			WithSuggestion("Check that the source directory exists and is accessible")
	}

	// Determine if we should use build.json for multi-file builds.
	// build.json is an intermediate JSON representation that merges all .bp files
	// into a single structured format, enabling faster incremental builds when
	// dealing with multiple Blueprint files.
	//
	// Conditions for using build.json:
	//   - cfg.All is true: user requested processing all .bp files in the source tree
	//   - len(cfg.Inputs) > 1: user provided multiple .bp files explicitly
	//
	// For a single .bp file, we skip build.json and use the original flow
	// (parseDefinitionsIncrementally) for simplicity and backward compatibility.
	useBuildJSON := cfg.All || len(cfg.Inputs) > 1

	var allDefs []parser.Definition
	if useBuildJSON {
		// Multiple .bp files: use build.json intermediate representation.
		//
		// MergeToBuildJSON performs the following steps:
		//   1. For each input .bp file, check if it needs reparsing (via incremental cache)
		//   2. Parse modified/new files and load cached AST for unchanged files
		//   3. Merge all definitions into a single BuildJSON structure
		//   4. The BuildJSON contains all modules, variable assignments, and dependency info
		//
		// This approach is more efficient than processing files individually when
		// dealing with multiple .bp files, as it creates a unified representation
		// that can be directly consumed by the generator.
		buildJSON, err := incremental.MergeToBuildJSON(incManager, cfg.Inputs)
		if err != nil {
			return err
		}

		// Save build.json to .minibp/build.json for caching and debugging.
		// This file can be inspected to understand the merged build structure
		// and is used for incremental builds in subsequent runs.
		buildJSONPath := filepath.Join(cfg.SrcDir, ".minibp", "build.json")
		if err := incremental.SaveBuildJSON(buildJSON, buildJSONPath); err != nil {
			// Failed to save build.json - not fatal but degrades incremental behavior.
			return errors.Config("failed to save build.json").
				WithCause(err).
				WithSuggestion("Check write permissions for .minibp/ directory")
		}

		// Generate ninja build file directly from the BuildJSON representation.
		//
		// GenerateFromBuildJSON performs the complete build pipeline:
		//   1. Process variable assignments from the BuildJSON
		//   2. Collect modules and apply property overrides
		//   3. Build namespace map for soong_namespace resolution
		//   4. Construct dependency graph with variant handling
		//   5. Create ninja generator and write build.ninja
		//
		// Parameters:
		//   - buildJSON: The merged BuildJSON structure from all .bp files
		//   - buildOpts: Build configuration (arch, compilers, sysroot, etc.)
		//   - eval: Expression evaluator for variable substitution and select() expressions
		//   - cfg.OutFile: Output path for the generated build.ninja file
		buildOpts := toBuildOptions(cfg.BuildOptions())
		numModules, err := buildlib.GenerateFromBuildJSON(buildJSON, buildOpts, eval, cfg.OutFile)
		if err != nil {
			return err
		}

		fmt.Fprintf(stdout, "Generated %s with %d modules\n", cfg.OutFile, numModules)
		return nil
	}

	// Single .bp file: use original flow (skip build.json).
	//
	// When processing a single .bp file, we bypass the build.json intermediate
	// representation and use the traditional pipeline:
	//   - Parse the file incrementally (with caching via .minibp/json/)
	//   - Process variable assignments to enable ${var} substitution
	//   - Collect modules, build namespace, construct graph, generate ninja
	//
	// This path is simpler and more efficient for single-file scenarios,
	// maintaining backward compatibility with earlier versions of minibp.
	allDefs, err = parseDefinitionsIncrementally(incManager, cfg.Inputs)
	if err != nil {
		return err
	}

	// Process variable assignments from all definitions.
	// This evaluates and registers all assignment statements (e.g., "my_var = "value"")
	// found in the parsed Blueprint files. After this call, the evaluator can
	// resolve ${my_var} references in module properties and expressions.
	// Variable scopes are handled per-file, with later assignments overriding earlier ones.
	if err := eval.ProcessAssignmentsFromDefs(allDefs); err != nil {
		return fmt.Errorf("process assignments: %w", err)
	}

	// Persist dependency hashes for next incremental run.
	// SaveDepFile writes the current file hashes to .minibp/dep.json,
	// allowing the next run to detect which files were modified.
	// Without this, all files would be reparsed on every run.
	if err := incManager.SaveDepFile(); err != nil {
		return fmt.Errorf("save dep file: %w", err)
	}

	// Step 4: Collect modules from definitions.
	// This extracts all module definitions (e.g., cc_library, cc_binary) from the parsed AST.
	// For each module, it:
	//   - Evaluates property values (resolving ${var} and select() expressions)
	//   - Applies property overrides from command-line flags (if any)
	//   - Handles architecture-specific variants (host, target, 32/64-bit)
	//   - Expands globs in srcs, headers, and other file list properties
	//
	// The provided nameGetter function extracts the "name" property from each module,
	// which is used as the module's unique identifier in the build graph.
	buildOpts := toBuildOptions(cfg.BuildOptions())
	modules, err := buildlib.CollectModulesWithNames(allDefs, eval, buildOpts, func(m *parser.Module, name string) string {
		return props.GetStringPropEval(m, name, eval)
	})
	if err != nil {
		return err
	}

	// Step 5: Build namespace map for soong_namespace resolution.
	// This creates a mapping from module names to their fully-qualified names
	// (e.g., "//namespace:module_name"). Namespaces allow modules in different
	// directories to reference each other without name collisions.
	// The nameGetter extracts the namespace property from each module.
	namespaces := namespace.BuildMap(modules, func(m *parser.Module, name string) string {
		return props.GetStringPropEval(m, name, eval)
	})

	// Step 6: Construct dependency graph.
	// This builds the complete dependency tree by:
	//   - Resolving "deps" properties in each module to find dependencies
	//   - Handling variant merging (e.g., combining host and target variants)
	//   - Expanding glob patterns to find actual source files
	//   - Validating that all referenced modules exist in the namespace
	// The resulting graph is used by the generator to create proper build order.
	graph := buildlib.BuildGraph(modules, namespaces, eval)

	// Step 7: Create ninja generator.
	// The generator converts the dependency graph into Ninja build rules.
	// For each module, it generates:
	//   - Build rules (e.g., compile, link commands)
	//   - Implicit dependencies (headers, libraries)
	//   - Output files (objects, binaries, libraries)
	// The generator uses buildOpts for compiler paths, flags, and sysroot settings.
	gen := buildlib.NewGenerator(graph, modules, buildOpts)

	// Step 8: Generate and write the Ninja build file.
	// This calls the generator to write all build rules to the output file.
	// If generation fails, the incomplete file is removed to prevent stale builds.
	if err := generateNinjaFile(cfg.OutFile, gen); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Generated %s with %d modules\n", cfg.OutFile, len(modules))
	return nil
}

// toBuildOptions converts applib.BuildOptions to buildlib.Options.
//
// This function maps the CLI configuration options (parsed from command-line flags)
// to the internal build library options struct used by the generator.
// It serves as an adapter between the public API (applib) and the internal build
// library (buildlib), allowing the two packages to evolve independently.
//
// Parameters:
//   - opts: The BuildOptions struct from applib containing CLI-parsed settings.
//     Fields include compiler paths, architecture, sysroot, and other build flags.
//
// Returns:
//   - buildlib.Options: The corresponding options struct for the build library.
//     All fields are copied by value; no references are retained.
//
// Edge cases:
//   - Zero-value opts produces a zero-value Options (caller should validate first).
//   - The SrcDir field is used by buildlib for resolving relative paths.
//   - Multilib affects how 32/64-bit variants are handled during module collection.
func toBuildOptions(opts applib.BuildOptions) buildlib.Options {
	return buildlib.Options{
		Arch:     opts.Arch,
		SrcDir:   opts.SrcDir,
		OutFile:  opts.OutFile,
		Inputs:   opts.Inputs,
		Multilib: opts.Multilib,
		CC:       opts.CC,
		CXX:      opts.CXX,
		AR:       opts.AR,
		LD:       opts.LD,
		LTO:      opts.LTO,
		Sysroot:  opts.Sysroot,
		Ccache:   opts.Ccache,
		TargetOS: opts.TargetOS,
	}
}

// parseDefinitionsFromFiles reads and parses all Blueprint files.
//
// It opens each file path, runs the parser, and collects the resulting definitions.
// File reading uses openInputFile dependency injection for testability.
// Parse errors are collected and aggregated rather than failing immediately,
// allowing all files to be checked in one pass.
//
// Note: Each file is opened twice:
//  1. First open: Read entire content into memory for error reporting context.
//  2. Second open: Parser reads from the file handle to build the AST.
//     This design ensures the parser has a clean io.Reader while we retain
//     the source text for displaying line numbers and context in error messages.
//
// Parameters:
//   - files: List of file paths to parse.
//     Paths can be relative to the current directory or absolute.
//     Non-.bp files may be accepted but could cause parse errors.
//
// Returns:
//   - allDefs: Combined slice of all definitions from all files.
//     The order of definitions matches the order of files and their appearance.
//   - error: nil if all files parse successfully; otherwise aggregated parse errors.
//     I/O errors (open, read) cause immediate failure with no aggregation.
//
// Edge cases:
//   - Missing files return error immediately (before processing other files).
//   - Parse errors in one file don't stop processing of other files.
//   - File handle is always closed, even on parse errors.
//   - Close errors after parse errors are reported but don't mask the original parse error.
//   - Empty files produce valid but empty parser.File (no definitions).
func parseDefinitionsFromFiles(files []string) ([]parser.Definition, error) {
	var allDefs []parser.Definition
	var parseErrors []string

	for _, file := range files {
		// Open file for reading.
		// Uses openInputFile dependency injection to allow mocking in tests.
		// Returns error if file doesn't exist, lacks read permissions,
		// or contains directory traversal attempts (e.g., "../").
		f, err := openInputFile(file)
		if err != nil {
			return nil, errors.NotFound(file).
				WithCause(err).
				WithSuggestion("Check that the file exists and has read permissions")
		}

		source, readErr := io.ReadAll(f)
		f.Close()
		if readErr != nil {
			return nil, errors.NotFound(file).
				WithCause(readErr).
				WithSuggestion("Check file integrity and disk health")
		}

		parsedFile, parseErr := parseBlueprintFile(strings.NewReader(string(source)), file, string(source))
		if parseErr != nil {
			parseErrors = append(parseErrors, parseErr.Error())
			continue
		}
		// Append definitions from this file to the combined result.
		// Each parser.File contains a Defs slice with all definitions in that file.
		allDefs = append(allDefs, parsedFile.Defs...)
	}

	// Report all collected parse errors at once.
	// Errors are joined with "; " separator for readability.
	if len(parseErrors) > 0 {
		return nil, fmt.Errorf("parsing failed: %s", strings.Join(parseErrors, "; "))
	}
	return allDefs, nil
}

// parseDefinitionsIncrementally reads Blueprint files with caching.
//
// For each .bp file:
//   - If unchanged (hash matches dep.json), load the cached JSON AST.
//   - If new or modified, parse the file and save the JSON AST to .minibp/json/.
//
// The manager tracks which files need reparsing by comparing file hashes.
// Hash comparison is based on file content, not modification time, ensuring
// accurate change detection even when files are restored from backup.
//
// Parameters:
//   - mgr: The incremental.Manager that handles caching.
//     It maintains the dep.json file with file hashes and .minibp/json/ with cached ASTs.
//   - files: List of .bp file paths to process.
//     Paths should be relative to the source directory or absolute.
//
// Returns:
//   - allDefs: Combined slice of all definitions from all files (cached or parsed).
//     Each file contributes its Defs slice; the order matches the input file order.
//   - error: Non-nil if any file cannot be read, parsed, or cached.
//     Parse errors are aggregated; other errors (I/O) cause immediate failure.
//
// Edge cases:
//   - Parse errors are collected and reported in one batch (never fail immediately).
//     This allows users to see all syntax errors at once rather than fixing one at a time.
//   - Cache load failures silently fall back to full parsing.
//     Corrupted cache files are automatically handled by reparsing.
//   - If a cached file cannot be unmarshaled, the file is reparsed.
//   - I/O errors (e.g., file not found) cause immediate return with error.
//   - Empty files produce no definitions but are not errors.
func parseDefinitionsIncrementally(mgr *incremental.Manager, files []string) ([]parser.Definition, error) {
	var allDefs []parser.Definition
	var parseErrors []string

	for _, file := range files {
		// Check if file needs reparsing by comparing its current hash
		// against the stored hash in .minibp/dep.json.
		// Returns true if: file is new, modified, or hash is missing.
		needsReparse, err := mgr.NeedsReparse(file)
		if err != nil {
			return nil, fmt.Errorf("check reparse %s: %w", file, err)
		}

		var parsedFile *parser.File

		// Attempt to use cached version if file hasn't changed.
		// The cache contains the JSON-serialized AST from a previous successful parse.
		if !needsReparse {
			// Try to load from cache.
			// LoadJSON reads the cached JSON file from .minibp/json/<hash>.json
			// and deserializes it into a parser.File structure.
			cached, err := mgr.LoadJSON(file)
			if err == nil && cached != nil {
				// Successfully loaded from cache; reuse previous parse result.
				parsedFile = cached
			}
			// If cache failed (file missing, corrupted JSON, etc.),
			// fall through to reparse the file from scratch.
		}

		// If we don't have a parsed file (either needs reparse or cache miss),
		// parse the file from scratch.
		if parsedFile == nil {
			f, err := openInputFile(file)
			if err != nil {
				return nil, fmt.Errorf("error opening %s: %w", file, err)
			}

			source, readErr := io.ReadAll(f)
			f.Close()
			if readErr != nil {
				return nil, fmt.Errorf("error reading %s: %w", file, readErr)
			}

			pf, parseErr := parseBlueprintFile(strings.NewReader(string(source)), file, string(source))
			if parseErr != nil {
				parseErrors = append(parseErrors, parseErr.Error())
				continue
			}
			parsedFile = pf

			// Save to cache for future incremental builds.
			// SaveJSON serializes the parsed AST to JSON and stores it in .minibp/json/.
			// The filename is based on the file's content hash, so identical content
			// shares the same cache file.
			if err := mgr.SaveJSON(file, parsedFile); err != nil {
				// Non-fatal: caching failure shouldn't stop the build.
				// The build will proceed without caching this file.
				fmt.Fprintf(os.Stderr, "warning: failed to cache %s: %v\n", file, err)
			}
		}

		// Append definitions from this file to the combined result.
		// Each file's Defs slice contains module definitions and assignments.
		allDefs = append(allDefs, parsedFile.Defs...)
	}

	// Report all collected parse errors at once.
	// This allows the user to fix all syntax errors in one pass.
	if len(parseErrors) > 0 {
		// Aggregate syntax errors from all files with proper formatting.
		errMsg := fmt.Sprintf("parsing failed for %d file(s):", len(parseErrors))
		for _, e := range parseErrors {
			errMsg += "\n  - " + e
		}
		return nil, errors.Syntax(errMsg).
			WithSuggestion("Fix syntax errors in the listed Blueprint files and retry")
	}
	return allDefs, nil
}

// generateNinjaFile writes the Ninja build file to the specified path.
//
// It creates the output file, calls the generator to write build rules,
// and ensures the file is properly closed.
//
// The function uses a two-phase error handling approach:
//  1. If generation fails: remove the incomplete file to prevent stale builds.
//  2. If close fails after successful generation: report the close error.
//
// Parameters:
//   - path: Output file path (usually "build.ninja").
//     The file will be truncated if it already exists.
//   - gen: Generator implementing Generate(io.Writer) error.
//     The generator writes all build rules, variables, and defaults to the writer.
//
// Returns:
//   - nil on successful write and close.
//   - error on file creation, generation, or close failures.
//     Errors are wrapped with context for debugging.
//
// Edge cases:
//   - If generation fails, incomplete output file is removed.
//     This prevents Ninja from using a partial build.ninja which could
//     cause confusing build errors or undefined behavior.
//   - Close error during cleanup doesn't mask the original generation error.
//     Both errors are reported if removal also fails.
//   - Close error after successful generation is still reported.
//     A failed flush/sync should not be silently ignored.
//   - If the output file cannot be created (e.g., directory doesn't exist),
//     the error is returned immediately without attempting generation.
func generateNinjaFile(path string, gen interface{ Generate(io.Writer) error }) error {
	// Create (or truncate) the output file.
	// Uses createOutputFile dependency injection for testability.
	// If the file already exists, it will be truncated to zero length.
	out, err := createOutputFile(path)
	if err != nil {
		// Failed to create output file - likely permission or disk space issue.
		return errors.Config(fmt.Sprintf("failed to create output file: %s", path)).
			WithCause(err).
			WithSuggestion("Check write permissions and available disk space")
	}

	// Generate the Ninja build file content.
	// This calls the generator's Generate method which writes all build rules.
	// The output is written to the file handle; errors may occur during
	// generation or during write operations (disk full, etc.).
	genErr := gen.Generate(out)

	// Close the output file.
	// Closing flushes any buffered data to disk and releases the file handle.
	// Close can return an error (e.g., flush failure) even if writes succeeded.
	closeErr := out.Close()

	if genErr != nil {
		// Generation failed: remove the incomplete file.
		// A partial build.ninja can cause confusing errors in Ninja.
		// By removing it, we ensure either a complete file exists or none does.
		// Ninja's behavior with missing build.ninja is well-defined (rebuild).
		closeErr = os.Remove(path)
		if closeErr != nil {
			// Both generation and removal failed.
			// Report both errors so the user knows what happened.
			return errors.Config("ninja generation failed and could not remove incomplete file").
				WithCause(genErr).
				WithSuggestion("Check disk space and permissions for output file")
		}
		return errors.Config("failed to generate ninja build file").
			WithCause(genErr).
			WithSuggestion("Check BuildJSON structure and module definitions")
	}

	// Generation succeeded; report any close error.
	// A close error at this point typically means the final flush failed
	// (e.g., disk full when writing buffered data).
	if closeErr != nil {
		return errors.Config("failed to close output file after successful generation").
			WithCause(closeErr).
			WithSuggestion("Check disk space and file system health")
	}
	return nil
}
