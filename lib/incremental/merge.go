// Package incremental provides incremental build support for the minibp build system.
//
// It implements caching of parsed Blueprint files to avoid re-parsing unchanged files,
// dependency tracking via file hashes (dep.json), and an intermediate representation
// (build.json) that merges all parsed Blueprint files into a single structured format.
//
// The incremental build process works as follows:
//  1. For each .bp file, compare its current hash with the cached hash in dep.json
//  2. If unchanged, load the cached JSON AST from .minibp/json/
//  3. If modified or new, parse the file and cache the result
//  4. Merge all parsed files into a BuildJSON structure
//  5. Save the updated dependency hashes for the next run
//
// This package is used by the main minibp tool to speed up builds when only a few
// files have changed.

package incremental

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"minibp/lib/errors"
	"minibp/lib/parser"
)

// BuildJSON represents the merged build.json structure.
// It collects all parsed .bp files into a single JSON file for efficient processing.
//
// This is the intermediate format described in tasks.md step:
//
//	Input -> parse -> .bp.json -> collect -> build.json -> convert -> build.ninja
//
// The build.json allows the system to:
//   - Merge all parsed Blueprint files into one structured file
//   - Support incremental updates (only re-parse changed .bp files)
//   - Provide a stable intermediate representation for ninja generation
//
// JSON format example:
//
//	{
//	  "version": 1,
//	  "sources": {
//	    "Android.bp": { "name": "Android.bp", "defs": [...] },
//	    "subdir/Android.bp": { "name": "subdir/Android.bp", "defs": [...] }
//	  }
//	}
//
// The "sources" map uses relative paths (from workDir) as keys,
// enabling the build system to locate modules by their source file location.
type BuildJSON struct {
	Version int                     `json:"version"`
	Sources map[string]*parser.File `json:"sources"` // bpFilePath -> AST
}

// MergeToBuildJSON collects all parsed .bp files into a BuildJSON structure.
//
// This function implements the "collect" step from tasks.md:
//
//	Input -> parse -> .bp.json -> collect -> build.json -> convert -> build.ninja
//
// For each .bp file in the input list:
//   - Check if the file needs reparsing by comparing its hash with the cached dep.json
//   - If unchanged (hash matches), load the cached JSON AST from .minibp/json/
//   - If new or modified, parse the file with parser.ParseFile and save JSON to cache
//   - Store the parsed AST in BuildJSON.Sources map using relative path as key
//
// The Manager (mgr) tracks which files need reparsing by maintaining a dependency
// file (.minibp/dep.json) that stores file hashes from the previous run.
//
// Parameters:
//   - mgr: The incremental.Manager that handles caching and dependency tracking.
//     It provides NeedsReparse(), LoadJSON(), SaveJSON(), and SaveDepFile() methods.
//   - files: List of .bp file paths to process (absolute or relative to workDir).
//
// Returns:
//   - *BuildJSON: Merged structure containing all parsed files, with Sources map
//     keyed by relative paths (e.g., "Android.bp", "subdir/Android.bp").
//   - error: Non-nil if mgr.NeedsReparse fails, file cannot be read/parsed,
//     or dep file cannot be saved. Parse errors are collected and reported
//     in one batch rather than failing immediately.
//
// Edge cases:
//   - Parse errors are collected and reported in one batch (never fail immediately).
//     This allows checking all files in one pass.
//   - Cache load failures silently fall back to full parsing.
//     A corrupted cache file will cause a reparse rather than a hard error.
//   - If a cached file cannot be unmarshaled, the file is reparsed automatically.
//   - Files with identical basenames in different directories are keyed by
//     their relative path, not just the basename.
//   - Empty files list returns a valid (but empty) BuildJSON structure.
//   - Relative path calculation failure falls back to using the file basename.
func MergeToBuildJSON(mgr *Manager, files []string) (*BuildJSON, error) {
	// Initialize BuildJSON with version 1 and empty sources map.
	// Version field allows future format changes to be handled gracefully.
	buildJSON := &BuildJSON{
		Version: 1,
		Sources: make(map[string]*parser.File),
	}

	// Collect parse errors across all files rather than failing on first error.
	// This allows the user to see all syntax errors in one pass.
	var parseErrors []string

	// Iterate over all input .bp files to process them.
	for _, file := range files {
		// Step 1: Check if this file needs reparsing by comparing its current
		// hash with the hash stored in the dependency file (.minibp/dep.json).
		// Returns true if: file is new, modified, or hash is missing.
		needsReparse, err := mgr.NeedsReparse(file)
		if err != nil {
			// Fail immediately on hash check errors (file access issues).
			// Use NotFound error for file-related issues with proper context.
			return nil, errors.NotFound(file).
				WithCause(err).
				WithSuggestion("Check that the file exists and is readable")
		}

		var parsedFile *parser.File

		// Step 2: If file hasn't changed, try to load the cached JSON AST.
		// The cache is stored in .minibp/json/ directory as individual JSON files.
		if !needsReparse {
			// Try to load previously parsed AST from cache.
			// LoadJSON reads the cached JSON file and unmarshals it into a parser.File.
			cached, err := mgr.LoadJSON(file)
			if err == nil && cached != nil {
				// Cache hit: use the cached AST, skip parsing.
				parsedFile = cached
			}
			// If cache failed (err != nil or cached == nil), fall through to reparse.
			// This handles corrupted cache files gracefully.
		}

		// Step 3: If not loaded from cache, parse the file from scratch.
		if parsedFile == nil {
			// Read the entire file content into memory for parsing.
			// readFileContent is a thin wrapper around os.ReadFile.
			source, err := readFileContent(file)
			if err != nil {
				// Failed to read file contents - likely permission or file not found.
				// Use NotFound error with file path and underlying cause.
				return nil, errors.NotFound(file).
					WithCause(err).
					WithSuggestion("Check file permissions and ensure the file exists")
			}

			// Parse the Blueprint file into an AST (Abstract Syntax Tree).
			// parser.ParseFile handles:
			//   - Lexical analysis (tokens, strings, numbers)
			//   - Syntax parsing (module declarations, property values)
			//   - Expression evaluation (variables, operators, select())
			// First param (nil) means no custom file reader; uses string source instead.
			pf, parseErr := parser.ParseFile(nil, file, string(source))
			if parseErr != nil {
				// Collect parse error rather than failing immediately.
				// This allows all files to be checked in one pass.
				parseErrors = append(parseErrors, parseErr.Error())
				continue
			}
			parsedFile = pf

			// Step 4: Save the parsed AST to cache for future incremental builds.
			// This writes a JSON file to .minibp/json/ directory.
			if err := mgr.SaveJSON(file, parsedFile); err != nil {
				// Non-fatal: caching failure shouldn't stop the build.
				// The build can continue; cache will be regenerated next time.
				fmt.Printf("warning: failed to cache %s: %v\n", file, err)
			}
		}

		// Step 5: Store the parsed file in BuildJSON using relative path as key.
		// This allows modules to be located by their source file location.
		// filepath.Rel computes the relative path from workDir to file.
		relPath, _ := filepath.Rel(mgr.workDir, file)
		// Handle edge cases where Rel returns empty or "." (file is at workDir root).
		if relPath == "" || relPath == "." {
			relPath = filepath.Base(file)
		}
		buildJSON.Sources[relPath] = parsedFile
	}

	// Step 6: Persist dependency hashes for the next incremental build.
	// This updates .minibp/dep.json with current file hashes so that
	// the next run can detect which files have changed.
	if err := mgr.SaveDepFile(); err != nil {
		// Dependency file save failure - not fatal for current build
		// but will cause full reparse on next run.
		return nil, errors.Config("failed to save dependency file").
			WithCause(err).
			WithSuggestion("Check write permissions for .minibp/ directory")
	}

	// Report all collected parse errors at once.
	// This gives the user a complete list of all syntax errors.
	if len(parseErrors) > 0 {
		// Aggregate syntax errors from multiple files with proper formatting.
		// Build a comprehensive error message listing all parse failures.
		errMsg := fmt.Sprintf("parsing failed for %d file(s):", len(parseErrors))
		for _, e := range parseErrors {
			errMsg += "\n  - " + e
		}
		return nil, errors.Syntax(errMsg).
			WithSuggestion("Fix syntax errors in the listed Blueprint files and retry")
	}

	return buildJSON, nil
}

// SaveBuildJSON saves the BuildJSON structure to disk as a formatted JSON file.
//
// This function is typically called after MergeToBuildJSON to persist the merged
// AST for debugging or for use by other tools. The output file (build.json) serves
// as an intermediate representation that can be inspected or processed further.
//
// The JSON is formatted with indentation (MarshalIndent) for human readability.
// This is useful for debugging the parsed AST structure.
//
// Parameters:
//   - buildJSON: The BuildJSON structure to serialize and save.
//     Must not be nil; contains merged AST from all .bp files.
//   - path: Output file path (usually ".minibp/build.json").
//     Parent directories must exist; this function does not create them.
//
// Returns:
//   - error: JSON marshaling error (invalid types, circular refs) or
//     file write error (permissions, disk full). Wrapped with context.
//
// Edge cases:
//   - Nil buildJSON will cause panic during marshaling (caller responsibility).
//   - Invalid parser.File data (e.g., channels, functions) causes marshal error.
//   - Parent directory must exist; will fail with "no such file or directory" otherwise.
//   - File is overwritten if it already exists (not append mode).
func SaveBuildJSON(buildJSON *BuildJSON, path string) error {
	// Marshal the BuildJSON struct to formatted JSON bytes.
	// MarshalIndent produces human-readable output with 2-space indentation.
	// This makes build.json easier to inspect for debugging.
	data, err := json.MarshalIndent(buildJSON, "", "  ")
	if err != nil {
		// JSON marshaling failed - likely due to invalid types in the AST
		// (e.g., channels, functions, circular references).
		return errors.Config("failed to marshal build.json").
			WithCause(err).
			WithSuggestion("Check that the AST contains only serializable types")
	}
	// Write the JSON bytes to disk using the helper function.
	// Uses 0644 permissions (rw-r--r--).
	return writeFile(path, data)
}

// LoadBuildJSON loads a BuildJSON structure from a JSON file on disk.
//
// This function is used to reload a previously saved build.json, allowing
// the build system to skip the merge step if the build.json is up-to-date.
// However, the current pipeline typically regenerates build.json each run
// after checking incremental caches.
//
// Parameters:
//   - path: Path to the build.json file to load.
//     File must exist and be valid JSON; no auto-creation.
//
// Returns:
//   - *BuildJSON: Loaded and unmarshaled structure on success.
//     Returns nil (not an error) if file doesn't exist.
//   - error: File read error (permissions, disk issues) or
//     JSON unmarshal error (corrupt or invalid format).
//     Unmarshal errors are wrapped with context.
//
// Edge cases:
//   - Missing file: returns nil, error (from readFile/os.ReadFile).
//   - Empty file: returns unmarshal error ("unexpected end of JSON input").
//   - Corrupt JSON: returns detailed unmarshal error with context.
//   - Valid JSON with wrong structure: unmarshal succeeds but fields may be zero-valued.
//   - Version field mismatch: no error (caller should check Version field).
func LoadBuildJSON(path string) (*BuildJSON, error) {
	// Read the entire file content into memory.
	// readFile is a thin wrapper around os.ReadFile.
	data, err := readFile(path)
	if err != nil {
		// File not found or unreadable: return nil and the error.
		return nil, err
	}

	// Unmarshal JSON bytes into BuildJSON struct.
	// json.Unmarshal populates fields based on json tags (Version, Sources).
	// If JSON has extra fields, they are ignored (default Go behavior).
	var buildJSON BuildJSON
	if err := json.Unmarshal(data, &buildJSON); err != nil {
		// JSON unmarshaling failed - file is corrupt or has wrong format.
		// This usually means build.json was manually edited or is stale.
		return nil, errors.Config("failed to unmarshal build.json").
			WithCause(err).
			WithSuggestion("Delete .minibp/build.json and re-run to regenerate it")
	}

	return &buildJSON, nil
}

// readFileContent reads a file and returns its content as a byte slice.
//
// This is a convenience wrapper around readFile that provides a more
// descriptive name for the operation. It reads the entire file into memory,
// which is suitable for parsing Blueprint files (typically small text files).
//
// Parameters:
//   - path: Absolute or relative path to the file to read.
//
// Returns:
//   - []byte: File contents as a byte slice. Empty slice if file is empty (not nil).
//   - error: File access error (not found, permissions, etc.) from os.ReadFile.
//
// Edge cases:
//   - Empty file returns empty byte slice (not error, not nil).
//   - Large files: reads entire content into memory (not streaming).
//   - Symlinks: resolved by os.ReadFile to target file content.
func readFileContent(path string) ([]byte, error) {
	return readFile(path)
}

// readFile reads a file from disk and returns its contents.
//
// This is the low-level file reading function used throughout the incremental
// package. It wraps os.ReadFile to provide a consistent interface that could
// be adapted for testing (though currently not injected).
//
// Parameters:
//   - path: Absolute or relative path to the file to read.
//
// Returns:
//   - []byte: File contents as a byte slice.
//   - error: Non-nil if file cannot be read (not found, permission denied,
//     I/O error, or path is a directory).
//
// Edge cases:
//   - Path with ".." components: resolved relative to current working directory.
//   - Directory path: returns error "is a directory".
//   - File larger than available memory: returns error or panics (OS-dependent).
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// writeFile writes data to a file on disk.
//
// This is the low-level file writing function used throughout the incremental
// package. It creates or truncates the target file and writes the entire
// data slice. Parent directories must already exist.
//
// Parameters:
//   - path: Absolute or relative path to the file to write.
//   - data: Byte slice containing the data to write.
//
// Returns:
//   - error: Non-nil if file cannot be written (permissions, disk full,
//     parent directory missing, or path is a directory).
//
// Edge cases:
//   - Existing file: content is truncated and overwritten (not appended).
//   - Parent directories: must exist; will not create them automatically.
//   - Permissions: file is created with 0600 (owner read/write only) mode.
//   - Empty data: creates an empty file (truncates if exists).
//   - Symlinks: writes to target of symlink, not the symlink itself.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0600)
}
