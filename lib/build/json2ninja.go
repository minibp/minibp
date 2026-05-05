// Package build implements the core build pipeline for minibp.
//
// It provides the transformation from parsed Blueprint definitions (from .bp files)
// to Ninja build files through a multi-stage pipeline:
//
//  1. Definition Collection: Extract all modules, assignments, and namespaces from parsed files
//  2. Variable Processing: Evaluate and register variable assignments for ${var} substitution
//  3. Module Collection: Filter enabled modules, resolve properties, expand globs, merge variants
//  4. Namespace Resolution: Build namespace map for soong_namespace cross-module references
//  5. Dependency Graph: Construct directed graph and perform topological sort
//  6. Ninja Generation: Create build rules, variables, and write build.ninja
//
// The package supports:
//   - Incremental builds via caching of parsed ASTs
//   - Architecture variants (host/target, 32/64-bit, cross-compilation)
//   - Soong-style Blueprint syntax with select() expressions
//   - Custom toolchain configuration (CC, CXX, AR, LTO, sysroot, ccache)
//
// Key entry points:
//   - GenerateFromBuildJSON: Convert merged BuildJSON to build.ninja (incremental pipeline)
//   - CollectModulesWithNames: Extract and resolve module definitions
//   - BuildGraph: Construct dependency graph for topological sorting
//   - NewGenerator: Create Ninja generator with build rules
package build

import (
	"fmt"
	"io"
	"os"

	"minibp/lib/errors"
	"minibp/lib/incremental"
	"minibp/lib/namespace"
	"minibp/lib/parser"
	"minibp/lib/props"
)

// GenerateFromBuildJSON reads a BuildJSON structure and generates a Ninja build file.
// This is the entry point for the incremental build pipeline's final stage, converting
// merged BuildJSON (from multiple .bp files) into a complete build.ninja.
//
// It implements the final conversion step of the incremental pipeline described in tasks.md:
//
//	Input -> parse -> .bp.json -> collect -> build.json -> convert -> build.ninja
//
// The function orchestrates the following steps (matching tasks.md flow):
//  1. collectAllDefs: Extracts all definitions (modules, assignments, namespaces) from all BuildJSON sources
//  2. eval.ProcessAssignmentsFromDefs: Processes variable assignments across all definitions
//  3. CollectModulesWithNames: Collects enabled modules, filters by arch/host/device, merges variants
//  4. namespace.BuildMap: Builds namespace map for soong_namespace cross-module resolution
//  5. BuildGraph: Constructs directed dependency graph and performs topological sort
//  6. NewGenerator: Creates Ninja generator with build rules, toolchain, and regeneration command
//  7. generateNinjaFile: Writes the final build.ninja to disk
//
// Parameters:
//   - buildJSON: The merged BuildJSON structure containing all parsed .bp files (from incremental.MergeToBuildJSON)
//   - opts: Build options controlling target arch, toolchain paths, output paths, and multilib settings
//   - eval: Evaluator for resolving variables, select() expressions, and property values in module definitions
//   - outputPath: Absolute or relative path to the output Ninja file (e.g., "build.ninja")
//
// Returns:
//   - nil if the entire pipeline completes successfully and build.ninja is written
//   - error if any step fails (e.g., module collection error, graph sort error, write error)
//
// Edge cases:
//   - Empty BuildJSON (no sources): collectAllDefs returns empty slice, resulting in minimal build.ninja
//   - No enabled modules after filtering: generates empty build.ninja with only header comments
//   - Module collection errors (e.g., glob expansion failure) are returned immediately
//   - Dependency graph circular references are caught during topological sort and returned as errors
func GenerateFromBuildJSON(
	buildJSON *incremental.BuildJSON,
	opts Options,
	eval *parser.Evaluator,
	outputPath string,
) (int, error) {
	// Step1: Collect all definitions from all sources in BuildJSON.
	// BuildJSON.Sources contains parsed File structures from each .bp file that was
	// merged. Each File has a Defs slice containing modules, assignments, and namespaces.
	// This flattens all definitions into a single slice for downstream processing.
	allDefs := collectAllDefs(buildJSON)

	// Step2: Process variable assignments across all collected definitions.
	// This evaluates top-level variable assignments (e.g., "my_var = "value"") and
	// registers them in the evaluator's variable map. These variables can then be
	// referenced in module properties via "${my_var}" syntax throughout the pipeline.
	if err := eval.ProcessAssignmentsFromDefs(allDefs); err != nil {
		return 0, fmt.Errorf("process assignments: %w", err)
	}

	// Step3: Collect enabled modules from all definitions.
	// This function performs several sub-steps:
	//   a. Filters definitions to only Module types (skips assignments/namespaces)
	//   b. Extracts module name using the provided name function
	//   c. Evaluates all module properties (resolving variables and select() expressions)
	//   d. Merges architecture-specific variant properties based on target arch
	//   e. Checks if module is enabled for current target (host/device support)
	//   f. Expands glob patterns (e.g., srcs: ["*.c"]) to actual file lists
	// Returns a map of canonical module names to their fully-resolved module definitions.
	modules, err := CollectModulesWithNames(allDefs, eval, opts, func(m *parser.Module, key string) string {
		return props.GetStringPropEval(m, key, eval)
	})
	if err != nil { // module collection failed
		return 0, err
	}

	// Step4: Build namespace map for soong_namespace resolution.
	// This creates a mapping from namespace-qualified names (e.g., "//myns:module")
	// to module metadata. It enables cross-namespace module references in dependency
	// properties (deps, shared_libs, header_libs, data) to be resolved correctly.
	// The name function extracts the "soong_namespace" property from each module.
	namespaces := namespace.BuildMap(modules, func(m *parser.Module, key string) string {
		return props.GetStringPropEval(m, key, eval)
	})

	// Step5: Construct the module dependency graph.
	// Creates a directed acyclic graph (DAG) where:
	//   - Nodes are enabled modules (from the modules map)
	//   - Edges represent dependencies (from deps, shared_libs, header_libs, data properties)
	//   - Namespace references (":local" or "//ns:remote") are resolved to canonical names
	// The graph is later used for topological sorting to determine build order.
	graph := BuildGraph(modules, namespaces, eval)

	// Step6: Create the Ninja generator with all build rules and configuration.
	// The generator is configured with:
	//   - Dependency graph (for build order and rule generation)
	//   - Module map (for per-module build rule emission)
	//   - Build options (arch, target OS, multilib settings)
	//   - Toolchain configuration (CC, CXX, AR, LTO, sysroot, ccache)
	//   - Regeneration command (so Ninja can re-run minibp when .bp files change)
	//   - Path prefix (relative path from build dir to source dir for file references)
	gen := NewGenerator(graph, modules, opts)

	// Step7: Generate the Ninja build file and write it to disk.
	// This invokes the generator to write all build rules, build statements, and
	// variables to the output file. If generation fails, the incomplete file is
	// removed to prevent stale builds. On success, build.ninja is ready for Ninja.
	if err := generateNinjaFile(outputPath, gen); err != nil { // ninja file generation failed
		return 0, err
	}
	return len(modules), nil
}

// collectAllDefs extracts all definitions from all sources in the BuildJSON structure.
//
// BuildJSON is the merged result of multiple .bp files (produced by incremental.MergeToBuildJSON).
// It contains a Sources slice where each element is a parsed File structure representing one .bp file.
// Each File contains a Defs slice with parser.Definition items (modules, assignments, namespaces).
//
// This function flattens all definitions from all source files into a single slice.
// The resulting slice is used by downstream steps:
//   - eval.ProcessAssignmentsFromDefs: processes variable assignments
//   - CollectModulesWithNames: filters and processes module definitions
//
// Parameters:
//   - buildJSON: The merged BuildJSON structure containing parsed File entries for each .bp file.
//     buildJSON.Sources holds all parsed files; each file's Defs contains the AST definitions.
//
// Returns:
//   - []parser.Definition: Combined slice of all definitions (modules, assignments, namespaces)
//     from all parsed .bp files in the BuildJSON. May be empty if BuildJSON has no sources.
//
// Edge cases:
//   - Empty BuildJSON.Sources: returns nil (or empty slice), which is handled gracefully by callers
//   - Files with no definitions (empty Defs): contribute nothing to the result
//   - The order of definitions follows the order of sources in BuildJSON (typically file discovery order)
func collectAllDefs(buildJSON *incremental.BuildJSON) []parser.Definition {
	// Iterate over all source files in the BuildJSON.
	// Each source is a parser.File containing the parsed AST of one .bp file.
	// The Defs field contains all top-level definitions (modules, assignments, namespaces).
	var allDefs []parser.Definition
	for _, file := range buildJSON.Sources {
		// Append all definitions from this file to the combined slice.
		// This uses variadic append to flatten the slice of definitions.
		allDefs = append(allDefs, file.Defs...)
	}
	return allDefs
}

// buildRegenCmd is defined in build.go and is called by NewGenerator (step 6 of GenerateFromBuildJSON)
// to construct the command string that Ninja will execute to regenerate build.ninja when inputs change.
//
// The function:
//   - Gets the current executable path via os.Executable()
//   - Adds -arch flag if target architecture is specified
//   - Adds -a flag if the single input is a directory (scan all .bp files)
//   - Adds -o flag with the output file path
//   - Appends all input file paths
//   - Converts executable path to forward slashes for Ninja compatibility
//
// Parameters (via Options struct passed to NewGenerator):
//   - opts.Arch: Target architecture (added as -arch flag if non-empty)
//   - opts.Inputs: List of input files/directories (used for -a detection and input list)
//   - opts.OutFile: Output path (added as -o flag)
//
// Returns (via NewGenerator which calls it):
//   - string: Complete command line for regenerating build.ninja
//
// Edge cases:
//   - Single directory input triggers -a (scan all) flag automatically
//   - Executable path is converted to forward slashes for cross-platform Ninja compatibility
//   - If os.Executable() fails, the path may be empty (Ninja will fail at regen time)

// toolchainFromOptions is defined in build.go and is called by NewGenerator (step 6 of GenerateFromBuildJSON)
// to create a Toolchain configuration from build options. The toolchain controls which compiler,
// archiver, and flags are used when generating Ninja build rules for each module.
//
// The function:
//   - Starts with DefaultToolchain() as base (platform-specific defaults)
//   - Overrides CC, CXX, AR if user provided custom paths via build options
//   - Sets LTO mode (e.g., "thin", "full") if specified
//   - Sets sysroot path for cross-compilation if specified
//   - Handles ccache: "no" disables it, any other non-empty value enables it
//
// Parameters (via Options struct passed to NewGenerator):
//   - opts.CC: C compiler path (overrides default)
//   - opts.CXX: C++ compiler path (overrides default)
//   - opts.AR: Archive tool path (overrides default)
//   - opts.LTO: LTO mode ("thin", "full", or empty to disable)
//   - opts.Sysroot: Cross-compilation sysroot path
//   - opts.Ccache: "no" to disable, or path to enable ccache wrapping
//
// Returns (via NewGenerator which calls it):
//   - ninja.Toolchain: Fully configured toolchain struct for the Ninja generator
//
// Edge cases:
//   - Ccache set to "no" explicitly clears the ccache field (disables caching)
//   - Ccache set to any other non-empty value is used as the ccache path
//   - Any tool not specified in opts retains its default value from DefaultToolchain()

// generateNinjaFile writes the Ninja build file to the specified path.
//
// This function is the final step in the build pipeline (step 7 of GenerateFromBuildJSON).
// It creates the output file, invokes the Ninja generator to write all build rules and
// statements, and ensures the file is properly closed. If generation fails, the incomplete
// file is removed to prevent stale builds that could cause confusing errors.
//
// Unlike the version in cmd/minibp/main.go, this version uses os.Create directly
// (no dependency injection) since it is called from the build package internals.
//
// Parameters:
//   - path: Output file path (usually "build.ninja").
//     The file will be truncated if it already exists.
//   - gen: Generator implementing the Generate(io.Writer) error interface.
//     The generator writes all Ninja build rules, build statements, and variables.
//
// Returns:
//   - nil on successful generation, write, and close.
//   - error if file creation fails, generation fails, or close fails.
//
// Edge cases:
//   - If generation fails, the incomplete output file is removed to prevent stale builds.
//     Ninja will rebuild everything if build.ninja is missing, but a partial file causes errors.
//   - Close error during cleanup does not mask the original generation error;
//     both are reported in the returned error message.
//   - Close error after successful generation is still reported as a distinct error.
func generateNinjaFile(path string, gen interface{ Generate(io.Writer) error }) error {
	// Create (or truncate) the output file for writing.
	// If the file already exists, it will be truncated to zero length.
	// This ensures no stale content remains from a previous build.ninja.
	out, err := os.Create(path)
	if err != nil { // failed to create output file
		// Failed to create output file - likely permission or disk space issue.
		// Use Config error for build output issues with proper context.
		return errors.Config(fmt.Sprintf("failed to create output file: %s", path)).
			WithCause(err).
			WithSuggestion("Check write permissions and available disk space")
	}

	// Invoke the generator to write all Ninja build content to the output file.
	// The generator writes:
	//   - Header comments and Ninja required version
	//   - Build rules (e.g., cc_rule, cxx_rule, link_rule)
	//   - Variable definitions (toolchain paths, flags)
	//   - Build statements for each module (ordered by topological sort)
	//   - Regeneration rule and build statement
	genErr := gen.Generate(out)

	// Close the output file. This flushes any buffered data to disk.
	// Must capture close error separately to handle it correctly below.
	closeErr := out.Close()

	if genErr != nil { // generation failed
		// Generation failed: remove the incomplete output file to prevent stale builds.
		// A partial build.ninja could cause confusing "missing rule" or "parse error" messages.
		// Removing it ensures Ninja will either regenerate or fail with a clear error.
		closeErr = os.Remove(path)
		if closeErr != nil { // both generation and removal failed
			// Both generation and removal failed; report both errors.
			return errors.Config("ninja generation failed and could not remove incomplete file").
				WithCause(genErr).
				WithSuggestion("Check disk space and permissions for output file")
		}
		return errors.Config("failed to generate ninja build file").
			WithCause(genErr).
			WithSuggestion("Check BuildJSON structure and module definitions")
	}

	// Generation succeeded but close may have failed (e.g., flush error).
	// Report the close error so the caller knows the file may be incomplete.
	if closeErr != nil { // close failed after successful generation
		return errors.Config("failed to close output file after successful generation").
			WithCause(closeErr).
			WithSuggestion("Check disk space and file system health")
	}
	return nil
}
