// Package utils provides utility functions for minibp including
// command-line flag parsing, version information, evaluator configuration,
// and common string processing. This package bridges the CLI layer with
// the core parser and build libraries, providing configuration injection
// and helper functions used by the main entry point.
package utils

import (
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"minibp/lib/parser"
	"minibp/lib/version"
)

// BuildOptions is a local copy of build.Options to avoid import cycles.
type BuildOptions struct {
	Arch     string
	SrcDir   string
	OutFile  string
	Inputs   []string
	Multilib []string
	CC       string
	CXX      string
	AR       string
	LTO      string
	Sysroot  string
	Ccache   string
	TargetOS string
}
// RunConfig holds the command-line configuration for a minibp execution.
// It encapsulates all flag values and derived settings needed to parse
// Blueprint files and generate Ninja build rules.
//
// CLI Flag Reference:
//
//	-o, -outfile: Output ninja file path. Default: "build.ninja". The generated
//	  file contains build rules for all discovered modules.
//	-a, -all: Parse all .bp files in srcDir (specified as first positional arg).
//	  When set, minibp scans the directory for all *.bp files rather than using
//	  explicit input files. Mutually exclusive with providing files directly.
//	-arch: Target architecture. Supported values: arm, arm64, x86, x86_64.
//	  Used for selecting appropriate toolchain and setting arch-specific config.
//	-multilib: Comma-separated target architectures for multi-arch build.
//	  Example: "arm64,x86_64" builds for both 64-bit ARM and x86_64. Each arch
//	  gets its own variant in the generated ninja rules. Requires -a flag.
//	-host: Build for the host machine (the machine running minibp). When set,
//	  overrides -arch to use host's native architecture (determined at runtime).
//	  Equivalent to setting -arch to the host's primary architecture.
//	-os: Target OS. Supported values: linux, darwin, windows, android.
//	  Affects platform-specific toolchain selection and path handling.
//	-variant: Comma-separated variant selectors used in select() evaluation.
//	  Format: key=value pairs, e.g., "image=recovery,link=shared". These are
//	  exposed as variant.<key> in Blueprint evaluation. Multiple variants can
//	  be combined to build different product configurations.
//	-product: Comma-separated product variables for Blueprint evaluation.
//	  Format: key=value pairs, e.g., "debuggable=true,board=soc_a". These are
//	  exposed as product.<key> in Blueprint evaluation and can be used in any
//	  select() or property reference.
//	-cc, -cxx, -ar: Path to C compiler, C++ compiler, and archiver respectively.
//	  If empty, defaults to the system's default compiler (gcc, g++, ar).
//	  These paths are used directly in generated ninja build rules.
//	-lto: Default LTO (Link-Time Optimization) mode. Supported values:
//	  "full" - full LTO, "thin" - thin LTO, "none" - disabled. Default: none.
//	  When set, LTO flags are added to compiler and archiver commands.
//	-sysroot: Sysroot path for cross-compilation. When set, overrides the system
//	  root directory used for finding headers and libraries. Useful for
//	  cross-compiling with a specific toolchain sysroot.
//	-ccache: Path to ccache executable. If empty: auto-detect from PATH. If "no":
//	  disable ccache entirely. When set, ninja build rules use ccache as a prefix
//	  to compiler commands for faster incremental builds.
//	-v, -version: Show version information and exit. Version includes minibp
//	  version, git commit hash, build date, and Go version.
//
// Positional Arguments:
//
//	Files or directories after flags. If -a is set, first argument is the
//	directory to scan for .bp files. Otherwise, provides explicit .bp files
//	to parse. At least one input is required without -a flag.
type RunConfig struct {
	OutFile     string   // Output ninja file path (default: build.ninja)
	All         bool     // Whether to parse all .bp files in srcDir
	CC          string   // C compiler path (default: gcc)
	CXX         string   // C++ compiler path (default: g++)
	AR          string   // Archiver path (default: ar)
	Arch        string   // Target architecture (arm, arm64, x86, x86_64)
	Multilib    []string // Comma-separated target architectures for multi-arch build
	TargetOS    string   // Target OS (linux, darwin, windows)
	Variant     string   // Comma-separated variant selectors
	Product     string   // Comma-separated product variables
	LTO         string   // Default LTO mode: full, thin, or none
	Sysroot     string   // Sysroot path for cross-compilation
	Ccache      string   // Ccache path (empty: auto-detect, 'no': disable)
	ShowVersion bool     // Whether to show version and exit
	Inputs      []string // Input Blueprint files or directories
	SrcDir      string   // Source directory to scan for .bp files
}

// ParseRunConfig parses command-line arguments and returns a RunConfig.
//
// This function defines and processes all supported CLI flags. Flag values are
// bound to the RunConfig struct fields. The function performs initial validation
// and determines the source directory and input files before returning.
//
// Flag Definitions:
//
//	-o: Output ninja file path (default: "build.ninja")
//	-a: Parse all .bp files in directory (requires directory argument)
//	-cc, -cxx, -ar: Compiler and archiver paths (defaults: gcc, g++, ar)
//	-arch: Target architecture (arm, arm64, x86, x86_64)
//	-multilib: Comma-separated architectures for multi-arch builds
//	-host: Build for host machine (overrides -arch)
//	-os: Target OS (linux, darwin, windows, android)
//	-variant: Comma-separated variant selectors (key=value format)
//	-product: Comma-separated product variables (key=value format)
//	-lto: LTO mode (full, thin, none)
//	-sysroot: Sysroot path for cross-compilation
//	-ccache: Ccache path (auto-detect if empty, "no" to disable)
//	-v: Show version and exit
//
// Returns:
//
//	A RunConfig struct populated with parsed flag values. The Inputs field
//	contains resolved Blueprint file paths. SrcDir is set to the directory
//	to scan for .bp files when -a is used.
//
// Errors:
//
//	Returns an error if flag parsing fails (invalid flag syntax).
//	Returns an error if no input is provided without the -a flag.
//	Returns an error if file collection fails (e.g., glob pattern error).
func ParseRunConfig(args []string, stderr io.Writer) (RunConfig, error) {
	cfg := RunConfig{}
	fs := flag.NewFlagSet("minibp", flag.ContinueOnError)

	// Define all CLI flags with defaults and usage strings
	// These map to RunConfig struct fields
	outFile := fs.String("o", "build.ninja", "output ninja file")
	all := fs.Bool("a", false, "parse all .bp files in directory")
	ccFlag := fs.String("cc", "", "C compiler (default: gcc)")
	cxxFlag := fs.String("cxx", "", "C++ compiler (default: g++)")
	arFlag := fs.String("ar", "", "archiver (default: ar)")
	archFlag := fs.String("arch", "", "target architecture (arm, arm64, x86, x86_64)")
	multilibFlag := fs.String("multilib", "", "comma-separated target architectures for multi-arch build (e.g. arm64,x86_64)")
	osFlag := fs.String("os", "", "target OS (linux, darwin, windows)")
	variantFlag := fs.String("variant", "", "comma-separated variant selectors (e.g. image=recovery,link=shared)")
	productFlag := fs.String("product", "", "comma-separated product variables (e.g. debuggable=true,board=soc_a)")
	ltoFlag := fs.String("lto", "", "default LTO mode: full, thin, or none")
	sysrootFlag := fs.String("sysroot", "", "sysroot path for cross-compilation")
	ccacheFlag := fs.String("ccache", "", "ccache path (empty: auto-detect, 'no': disable)")
	versionFlag := fs.Bool("v", false, "show version information")

	// Direct flag parsing errors to stderr for better UX
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	// Populate RunConfig from parsed flags
	cfg = RunConfig{
		OutFile:     *outFile,
		All:         *all,
		CC:          *ccFlag,
		CXX:         *cxxFlag,
		AR:          *arFlag,
		Arch:        *archFlag,
		Multilib:    splitCSV(*multilibFlag),
		TargetOS:    *osFlag,
		Variant:     *variantFlag,
		Product:     *productFlag,
		LTO:         *ltoFlag,
		Sysroot:     *sysrootFlag,
		Ccache:      *ccacheFlag,
		ShowVersion: *versionFlag,
		Inputs:      fs.Args(), // Remaining non-flag arguments
	}

	// Early exit for version flag - don't require input files
	if cfg.ShowVersion {
		return cfg, nil
	}

	// Validate: need at least one input file if not scanning directory
	if len(cfg.Inputs) < 1 && !cfg.All {
		fmt.Fprintln(stderr, "Usage: minibp [-o output] [-a] [-cc CC] [-cxx CXX] [-ar AR] [-arch ARCH] [-host] [-os OS] <file.bp | directory>")
		return cfg, fmt.Errorf("missing input path")
	}

	cfg.SrcDir = determineSourceDir(cfg.All, cfg.Inputs)
	files, err := collectBlueprintFiles(cfg.All, cfg.SrcDir, cfg.Inputs)
	if err != nil {
		return cfg, err
	}
	cfg.Inputs = files
	return cfg, nil
}

// NewEvaluatorFromConfig creates a parser.Evaluator configured with values from
// the run configuration (CLI flags).
//
// This function sets up the evaluator with build configuration variables
// that are used during Blueprint expression evaluation. It configures the
// arch, host, os, target, variant.* and product.* variables based on
// the RunConfig fields.
//
// Parameters:
//   - cfg: RunConfig containing CLI flag values
//
// Returns:
//   - *parser.Evaluator: Configured evaluator ready for Blueprint parsing
//
// Note: Variant and Product selectors are only used by the Evaluator
// during Blueprint parsing, not by the build pipeline.
func NewEvaluatorFromConfig(cfg RunConfig) *parser.Evaluator {
	eval := parser.NewEvaluator()
	
	// Default to current system architecture (host build)
	arch := runtime.GOARCH
	if cfg.Arch != "" {
		arch = cfg.Arch
	}
	
	eval.SetConfig("arch", arch)
	eval.SetConfig("host", "true")
	if cfg.TargetOS != "" {
		eval.SetConfig("os", cfg.TargetOS)
	} else {
		eval.SetConfig("os", "linux")
	}
	eval.SetConfig("target", arch)
	setKeyValueConfigs(eval, "variant.", cfg.Variant)
	setKeyValueConfigs(eval, "product.", cfg.Product)
	return eval
}

// BuildOptions converts the RunConfig into BuildOptions used by the
// build pipeline.
//
// This method copies all relevant configuration from RunConfig to the
// BuildOptions struct, which drives the module collection and ninja
// generation stages. The inputs and multilib slices are copied to prevent
// mutation of the original config.
//
// Fields Transferred:
//   - Arch: Target architecture (arm, arm64, x86, x86_64).
//   - Host: Boolean indicating host-only build.
//   - SrcDir: Source directory scanned for .bp files (when -a is set).
//   - OutFile: Output ninja file path.
//   - Inputs: Resolved Blueprint file paths.
//   - Multilib: Slice of architectures for multi-arch builds.
//   - CC, CXX, AR: Compiler and archiver tool paths.
//   - LTO: Link-time optimization mode.
//   - Sysroot: Cross-compilation sysroot path.
//   - Ccache: Ccache configuration ("no" to disable, path, or auto-detect).
//
// Note: Variant and Product selectors are NOT copied here because they are
// only used by the Evaluator during Blueprint parsing, not by the build
// pipeline which operates on resolved module values.
func (cfg RunConfig) BuildOptions() BuildOptions {
	arch := runtime.GOARCH
	if cfg.Arch != "" {
		arch = cfg.Arch
	}
	return BuildOptions{
		Arch:     arch,
		SrcDir:   cfg.SrcDir,
		OutFile:  cfg.OutFile,
		Inputs:   append([]string(nil), cfg.Inputs...),
		Multilib: append([]string(nil), cfg.Multilib...),
		CC:       cfg.CC,
		CXX:      cfg.CXX,
		AR:       cfg.AR,
		LTO:      cfg.LTO,
		Sysroot:  cfg.Sysroot,
		Ccache:   cfg.Ccache,
		TargetOS: cfg.TargetOS,
	}
}

// GetVersion returns a formatted version string containing the minibp
// version, git commit hash, build date, and Go version.
//
// The returned string format is:
//
//	"minibp X.Y.Z (git: ABCDEFG, built: YYYY-MM-DD, go: 1.XX.Y)"
//
// If git commit cannot be determined (git not available, not in a git repo,
// or during release builds), "unknown" is used instead of the commit hash.
// If build date cannot be determined, defaults to "2026-04-21".
//
// Parameters:
//   - (none)
//
// Returns:
//   - string: Formatted version string
//
// Note: This function is typically called when the -v flag is passed to display
// version information before parsing Blueprints.
func GetVersion() string {
	v := version.Get()
	gitCommit := v.GitCommit
	if gitCommit == "unknown" {
		if commit, err := getGitCommit(); err == nil {
			gitCommit = commit
		}
	}
	buildDate := v.BuildDate
	if buildDate == "unknown" {
		buildDate = "2026-04-21"
	}
	return fmt.Sprintf("%s (git: %s, built: %s, go: %s)", v.MinibpVer, gitCommit, buildDate, v.GoVersion)
}

// getGitCommit retrieves the current git commit hash by executing
// "git rev-parse --short HEAD". Returns an error if git is not available
// or the command fails.
//
// This is called only when version info is requested and the embedded
// git commit from build time is "unknown". This typically happens when:
//   - Running from source without building with git available
//   - Using a release archive without embedded version info
//   - In containers or CI environments without git
//
// Parameters:
//   - (none)
//
// Returns:
//   - string: Short git commit hash (7 characters)
//   - error: Error if git fails (not installed, not in git repo, or detached head)
//
// Edge cases:
//   - Returns error if not in a git repository (no .git directory)
//   - Returns error in detached HEAD state (OK but may not have commit)
//   - Stderr is suppressed; only exit code matters for success check
func getGitCommit() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// determineSourceDir determines the source directory to scan for
// Blueprint files. If -a flag is set with inputs, returns the first
// input as the source directory. Otherwise, if inputs are provided,
// returns the directory containing the first input file. Defaults
// to current directory if no inputs.
//
// Parameters:
//   - all: Whether -a flag was set to scan all files
//   - inputs: List of input paths from command-line arguments
//
// Returns:
//   - The source directory to scan for .bp files
//
// Edge cases:
//   - If all=true with no inputs, returns "." for current directory.
//   - filepath.Dir("") returns "." for empty path.
//   - For absolute paths like "/foo/bp/Android.bp", returns "/foo/bp".
//   - For relative paths like "../../bp/Android.bp", returns the normalized parent.
func determineSourceDir(all bool, inputs []string) string {
	if all && len(inputs) > 0 {
		return inputs[0]
	}
	if len(inputs) > 0 {
		return filepath.Dir(inputs[0])
	}
	return "."
}

// collectBlueprintFiles returns the input files when not scanning all
// files (-a flag). When -a is set, it uses filepath glob to find all
// .bp files in the source directory.
//
// Parameters:
//   - all: Whether -a flag was set
//   - srcDir: Source directory to scan
//   - inputs: Explicit input files from arguments
//
// Returns:
//   - Slice of .bp file paths if all=true, otherwise the original inputs
//   - Error if globbing fails (invalid pattern or permissions issue)
//
// Edge cases:
//   - Returns empty slice (not nil) if no .bp files found in srcDir.
//   - Glob pattern "*.bp" matches files directly in srcDir, not subdirectories.
//   - Case-sensitive on most systems; .bp files must have exact extension.
//   - Hidden files (starting with .) are NOT matched by default glob.
func collectBlueprintFiles(all bool, srcDir string, inputs []string) ([]string, error) {
	if !all {
		return inputs, nil
	}
	bpFiles, err := filepath.Glob(filepath.Join(srcDir, "*.bp"))
	if err != nil {
		return nil, fmt.Errorf("error globbing bp files: %w", err)
	}
	return bpFiles, nil
}

// splitCSV parses a comma-separated string into a slice of non-empty
// trimmed values. Empty parts and whitespace-only parts are filtered
// out. Returns nil for empty input.
//
// Parameters:
//   - raw: Comma-separated string (e.g., "arm64,x86_64,arm")
//
// Returns:
//   - Slice of trimmed non-empty strings
//   - nil if input is empty string
//
// Edge cases:
//   - Input "arm64, x86_64 , arm" trims spaces, returns ["arm64", "x86_64", "arm"]
//   - Input ",arm64,," with empty parts returns ["arm64"] only
//   - Input "   " (whitespace) returns nil, not empty string
//   - Input "arm64,,x86_64" with consecutive commas returns ["arm64", "x86_64"]
func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// setKeyValueConfigs parses a comma-separated string of key=value
// pairs and sets them as evaluator configs with a given prefix. Each
// entry is trimmed, and invalid entries (missing = or empty key) are
// skipped. This is used to configure variant.* and product.* variables.
//
// Parameters:
//   - eval: The parser.Evaluator to configure
//   - prefix: Prefix for config keys (e.g., "variant." or "product.")
//   - raw: Comma-separated key=value pairs (e.g., "arch=arm64,os=linux")
//
// Edge cases:
//   - Entries with multiple "=" signs use the first as delimiter:
//     "key=value=extra" sets key to "value=extra"
//   - Entries without "=" are silently skipped
//   - Empty values (key with empty string) ARE allowed and stored as ""
//   - Whitespace around keys/values is trimmed: " arch = arm64 " becomes "arch"="arm64"
//   - Order matters: duplicate keys overwrite previous values
func setKeyValueConfigs(eval *parser.Evaluator, prefix, raw string) {
	if raw == "" {
		return
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}
		eval.SetConfig(prefix+key, val)
	}
}

// SanitizePath removes '..' from a path to prevent directory traversal.
// It repeatedly replaces "../" and "..\" with an empty string until no
// more occurrences are found. This is a simple but effective way to
// mitigate path traversal vulnerabilities.
func SanitizePath(path string) string {
	for {
		cleaned := strings.ReplaceAll(path, "../", "")
		cleaned = strings.ReplaceAll(cleaned, "..\\", "")
		if cleaned == path {
			return cleaned
		}
		path = cleaned
	}
}
