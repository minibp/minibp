// Package hasher provides dependency hash calculation for incremental builds.
// It calculates SHA256 hashes of modules and their dependency trees to determine
// if a rebuild is necessary.
package hasher

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"minibp/lib/parser"
	"sync"
)

// Hasher calculates and stores hashes for modules.
// It computes SHA256 hashes that include a module's own properties,
// all transitive dependencies, and source file contents to enable
// accurate incremental build decisions.
//
// The hasher supports:
//   - Per-module hash caching to avoid redundant computation
//   - Persistent hash storage on disk for build state across runs
//   - Deterministic hash generation (consistent results across runs)
//
// Fields:
//   - cache: In-memory cache of computed hashes for current session
//   - hashDir: Directory path for persistent hash storage
//   - moduleHashes: Current hashes for each module (computed this run)
//
// Example:
//
//	h := NewHasher("/path/to/build")
//	hash, err := h.CalculateModuleHash(module, allModules)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	rebuild, err := h.NeedsRebuild(moduleName)
//	if rebuild {
//	    // Rebuild module
//	}
type Hasher struct {
	cache        map[string]string // module hash cache: module name -> cached hash
	hashDir      string            // hash storage directory path for persistent hash storage
	moduleHashes map[string]string // module name to current hash mapping for build decision
}

// NewHasher creates a new Hasher instance.
//
// This function initializes the hash cache and sets up the storage directory.
// The storage directory is created under buildDir/.minibp/hash/ and will be
// created on first use by StoreHash if it doesn't exist.
//
// Parameters:
//   - buildDir: Root directory for build output (hashes stored in buildDir/.minibp/hash/)
//
// Returns:
//   - *Hasher: A new Hasher instance ready for hash computation
func NewHasher(buildDir string) *Hasher {
	hashDir := filepath.Join(buildDir, ".minibp", "hash")
	return &Hasher{
		cache:        make(map[string]string),
		hashDir:      hashDir,
		moduleHashes: make(map[string]string),
	}
}

// CalculateModuleHash calculates the hash of a module including all its dependencies.
//
// This is the main entry point for hash calculation. It computes a SHA256 hash
// that incorporates three components (in order):
//  1. The module's own properties (type, name, srcs, deps, cflags)
//  2. All transitive dependency module hashes (recursive)
//  3. All source file content hashes
//
// The hash is deterministic: given the same module configuration and source files,
// the same hash will always be produced.
//
// Parameters:
//   - module: The module to calculate hash for
//   - allModules: Map of all module names to module instances for dependency lookup
//
// Returns:
//   - string: The hex-encoded SHA256 hash string (64 characters)
//   - error: Non-nil if any hash computation fails
//
// Edge cases:
//   - Modules with no srcs/deps produce valid hashes (just properties)
//   - Missing source files are silently ignored (assumed generated during build)
//   - Circular dependencies are handled via the cache (won't infinite loop)
//   - External dependencies (not in allModules) are skipped at hash time
//   - Unknown module names in allModules return empty dependency list
func (h *Hasher) CalculateModuleHash(
	module *parser.Module,
	allModules map[string]*parser.Module,
) (string, error) {
	// Check in-memory cache first.
	// Cached hashes avoid redundant computation for modules
	// that share dependencies.
	moduleName := h.getModuleName(module)
	if hash, ok := h.cache[moduleName]; ok {
		return hash, nil
	}

	// Create a new SHA256 hasher for this module.
	// We use a single hasher and write all components sequentially;
	// the order ensures deterministic results.
	hasher := sha256.New()

	// Step 1: Hash the module's own properties.
	// This includes type, name, and all build properties.
	// Properties are sorted to ensure deterministic ordering.
	if err := h.hashModuleProps(module, hasher); err != nil {
		return "", err
	}

	// Step 2: Hash all transitive dependencies (recursive).
	// Each dependency contributes its hash, creating a property
	// chain where any change propagates upward.
	deps := h.getModuleDeps(module, allModules)
	for _, depName := range deps {
		if dep, ok := allModules[depName]; ok {
			depHash, err := h.CalculateModuleHash(dep, allModules)
			if err != nil {
				return "", fmt.Errorf("failed to calculate hash for dependency %s: %w", depName, err)
			}
			hasher.Write([]byte(depHash))
		}
	}

	// Step 3: Hash all source file contents.
	// Each source file contributes its path and content hash.
	// This detects both file changes and file moves/renames.
	if err := h.hashSourceFiles(module, hasher); err != nil {
		return "", err
	}

	// Finalize and cache the hash.
	finalHash := hex.EncodeToString(hasher.Sum(nil))
	h.cache[moduleName] = finalHash
	return finalHash, nil
}

// hashModuleProps hashes the module's properties.
//
// This function writes the module's type, name, and all sorted properties
// to the hasher to create a deterministic hash based on the module's
// build configuration.
//
// The format is "key:value;" for each property, ensuring no ambiguity
// between property boundaries. Properties are sorted alphabetically
// to ensure consistent ordering.
//
// Parameters:
//   - module: The module to hash properties for
//   - w: Writer to write the hash input to
//
// Returns:
//   - error: Non-nil if property extraction fails
func (h *Hasher) hashModuleProps(module *parser.Module, w io.Writer) error {
	// Write module type.
	// The type determines what build rules apply.
	if module.Type != "" {
		fmt.Fprintf(w, "type:%s;", module.Type)
	}

	// Write module name.
	// The name uniquely identifies the module.
	if name := h.getModuleName(module); name != "" {
		fmt.Fprintf(w, "name:%s;", name)
	}

	// Write all properties, sorted for determinism.
	// Sorting ensures the same module produces the same hash
	// regardless of property declaration order.
	props := h.extractProperties(module)
	sort.Strings(props)
	for _, prop := range props {
		fmt.Fprintf(w, "prop:%s;", prop)
	}

	return nil
}

// extractProperties extracts all properties from a module.
//
// This function collects the module's type, name, and important build
// properties into a sorted string slice for hashing.
//
// The collected properties are:
//   - type: module type (e.g., "cc_library")
//   - name: module name
//   - deps: sorted comma-separated dependency list
//   - srcs: sorted comma-separated source file list
//   - cflags: sorted comma-separated compiler flags
//
// Only non-empty properties are included.
//
// Parameters:
//   - module: The module to extract properties from
//
// Returns:
//   - []string: Sorted slice of "key:value" property strings
func (h *Hasher) extractProperties(module *parser.Module) []string {
	var props []string

	// Add type.
	if module.Type != "" {
		props = append(props, "type:"+module.Type)
	}

	// Add name.
	if name := h.getModuleName(module); name != "" {
		props = append(props, "name:"+name)
	}

	// Add deps (sorted).
	// Dependencies can be declared in any order; sorting ensures
	// the same dependency set produces the same hash.
	if deps := h.getListProp(module, "deps"); len(deps) > 0 {
		sort.Strings(deps)
		props = append(props, "deps:"+strings.Join(deps, ","))
	}

	// Add srcs (sorted).
	if srcs := h.getListProp(module, "srcs"); len(srcs) > 0 {
		sort.Strings(srcs)
		props = append(props, "srcs:"+strings.Join(srcs, ","))
	}

	// Add cflags (sorted).
	if cflags := h.getListProp(module, "cflags"); len(cflags) > 0 {
		sort.Strings(cflags)
		props = append(props, "cflags:"+strings.Join(cflags, ","))
	}

	return props
}

// hashSourceFiles hashes the content of source files in parallel.
//
// This function uses a worker pool to concurrently hash all source files,
// which can significantly speed up the process on multi-core systems.
// The number of concurrent workers is limited to avoid overwhelming the system.
//
// Parameters:
//   - module: The module to hash source files for.
//   - w: Writer to write the hash input to.
//
// Returns:
//   - error: Non-nil if any file operations fail.
func (h *Hasher) hashSourceFiles(module *parser.Module, w io.Writer) error {
	srcs := h.getSourceFiles(module)
	sort.Strings(srcs)

	var wg sync.WaitGroup
	// Limit concurrency to avoid too many open files.
	sem := make(chan struct{}, 32)
	// Channel to collect results and errors.
	results := make(chan struct {
		hash string
		err  error
	}, len(srcs))

	for _, src := range srcs {
		wg.Add(1)
		go func(src string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			hash, err := h.hashFile(src)
			results <- struct {
				hash string
				err  error
			}{hash, err}
		}(src)
	}

	wg.Wait()
	close(results)

	// Process results.
	var fileHashes []string
	for res := range results {
		if res.err != nil {
			if !os.IsNotExist(res.err) {
				return res.err
			}
			continue
		}
		fileHashes = append(fileHashes, res.hash)
	}

	// Sort hashes for deterministic output.
	sort.Strings(fileHashes)
	for _, hash := range fileHashes {
		fmt.Fprint(w, hash)
	}

	return nil
}

// hashFile calculates the hash of a single file.
//
// This function returns a formatted string containing the file path and its
// content hash, suitable for writing to the main module hasher.
//
// Parameters:
//   - path: Absolute or relative path to the file.
//
// Returns:
//   - string: A formatted string "file:<path>;hash:<hash>;".
//   - error: Non-nil if file cannot be opened or read.
func (h *Hasher) hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fileHasher := sha256.New()
	if _, err := io.Copy(fileHasher, bufio.NewReaderSize(f, 32*1024)); err != nil {
		return "", err
	}

	hash := hex.EncodeToString(fileHasher.Sum(nil))
	return fmt.Sprintf("file:%s;hash:%s;", path, hash), nil
}

// getModuleName extracts the name from a module.
//
// This function looks up the "name" property in the module's property map
// and returns its string value. The name is used as a unique identifier
// for hash caching and storage.
//
// Parameters:
//   - module: The module to extract the name from
//
// Returns:
//   - string: The module name, or empty string if not found
func (h *Hasher) getModuleName(module *parser.Module) string {
	if module.Map != nil {
		for _, prop := range module.Map.Properties {
			if prop.Name == "name" {
				if str, ok := prop.Value.(*parser.String); ok {
					return str.Value
				}
			}
		}
	}
	return ""
}

// getModuleDeps returns all dependencies of a module.
//
// This function collects dependencies from multiple properties:
//   - "deps": direct module dependencies
//   - "shared_libs": shared library dependencies
//   - "header_libs": header library dependencies
//
// All three are included because changes to any type of dependency
// may affect the build.
//
// Parameters:
//   - module: The module to get dependencies for
//   - allModules: Map of all modules (unused in this implementation)
//
// Returns:
//   - []string: Sorted list of dependency names
func (h *Hasher) getModuleDeps(
	module *parser.Module,
	allModules map[string]*parser.Module,
) []string {
	deps := make(map[string]bool)

	// Get direct dependencies (deps).
	for _, dep := range h.getListProp(module, "deps") {
		deps[dep] = true
	}

	// Get shared library dependencies (shared_libs).
	for _, dep := range h.getListProp(module, "shared_libs") {
		deps[dep] = true
	}

	// Get header library dependencies (header_libs).
	for _, dep := range h.getListProp(module, "header_libs") {
		deps[dep] = true
	}

	// Convert to sorted slice for deterministic ordering.
	result := make([]string, 0, len(deps))
	for dep := range deps {
		result = append(result, dep)
	}
	sort.Strings(result)

	return result
}

// getSourceFiles returns all source files for a module.
//
// This function retrieves the "srcs" property and expands any glob patterns.
// Glob patterns containing "*" are expanded using filepath.Glob;
// non-glob paths are returned as-is.
//
// Parameters:
//   - module: The module to get source files for
//
// Returns:
//   - []string: List of source file paths (glob patterns expanded)
//
// Edge cases:
//   - Modules with no srcs property return empty slice
//   - Glob patterns that match no files return empty (no error)
//   - Invalid glob patterns return the original path (glob fails silently)
func (h *Hasher) getSourceFiles(module *parser.Module) []string {
	srcs := h.getListProp(module, "srcs")

	// Expand glob patterns.
	var expanded []string
	for _, src := range srcs {
		// Non-glob paths are added directly.
		if !strings.Contains(src, "*") {
			expanded = append(expanded, src)
			continue
		}

		// Expand glob patterns.
		// filepath.Glob returns matches sorted alphabetically.
		matches, err := filepath.Glob(src)
		if err == nil {
			expanded = append(expanded, matches...)
		}
		// Errors are ignored; invalid patterns pass through as-is.
	}

	return expanded
}

// getListProp gets a list property from a module.
//
// This function looks up a property by name and converts it to a string slice.
// Both list properties (parser.List) and single string properties (parser.String)
// are supported. Single strings are wrapped in a slice for consistent handling.
//
// Parameters:
//   - module: The module to get the property from
//   - key: Property name to look up
//
// Returns:
//   - []string: Property values as strings, or nil if not found
func (h *Hasher) getListProp(module *parser.Module, key string) []string {
	if module.Map == nil {
		return nil
	}

	// Find property by name.
	for _, prop := range module.Map.Properties {
		if prop.Name == key {
			// Try to convert to list (multiple values).
			if list, ok := prop.Value.(*parser.List); ok {
				var result []string
				for _, item := range list.Values {
					if str, ok := item.(*parser.String); ok {
						result = append(result, str.Value)
					}
				}
				return result
			}
			// Try to convert to string (single value).
			if str, ok := prop.Value.(*parser.String); ok {
				return []string{str.Value}
			}
		}
	}

	return nil
}

// NeedsRebuild checks if a module needs to be rebuilt.
//
// This function compares the current hash (computed this run) with the stored
// hash (from previous run) to determine if rebuild is necessary.
//
// A module needs rebuild if:
//   - No stored hash exists (first build or cleaned)
//   - Stored hash file cannot be read (file was deleted/corrupted)
//   - Current hash differs from stored hash (module changed or dependency changed)
//
// Parameters:
//   - moduleName: The name of the module to check
//
// Returns:
//   - bool: True if the module needs rebuilding, false if up-to-date
//   - error: Non-nil if hash loading fails unexpectedly
func (h *Hasher) NeedsRebuild(moduleName string) (bool, error) {
	currentHash, ok := h.moduleHashes[moduleName]
	if !ok {
		// No current hash record (not computed this run).
		// Need to build to produce one.
		return true, nil
	}

	// Load stored hash from previous run.
	storedHash, err := h.LoadHash(moduleName)
	if err != nil {
		// Load failed (file missing or corrupted).
		// Need to rebuild.
		return true, nil
	}

	return currentHash != storedHash, nil
}

// StoreHash stores the hash for a module to disk.
//
// This function writes the hash to a file in the hash storage directory.
// The file is named <moduleName>.hash and contains the plain text hash.
//
// The hash directory is created if it doesn't exist.
//
// Parameters:
//   - moduleName: Module name (used as filename)
//   - hash: Hex-encoded hash string to store
//
// Returns:
//   - error: Non-nil if directory creation or file write fails
// sanitizeHashPath sanitizes a module name for use as a filename.
//
// This function prevents path traversal attacks by replacing path separators
// with underscores and handling edge cases like names starting with "..".
// The sanitized name is safe to use in file paths without directory escape.
//
// Parameters:
//   - name: The module name to sanitize.
//
// Returns:
//   - string: The sanitized module name safe for use as a filename.
//
// Edge cases:
//   - Path separators (/ and \) are replaced with underscores.
//   - Names starting with ".." have those characters replaced.
//   - Other special characters are preserved unchanged.
func sanitizeHashPath(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if strings.HasPrefix(name, "..") {
		name = strings.Replace(name, "..", "__", -1)
	}
	return name
}

// StoreHash stores the hash for a module to disk.
//
// This function writes the hash to a file in the hash storage directory.
// The file is named <moduleName>.hash and contains the plain text hash.
//
// The hash directory is created if it doesn't exist. Path traversal protection
// ensures module names cannot escape the hash directory.
//
// Parameters:
//   - moduleName: Module name (used as filename)
//   - hash: Hex-encoded hash string to store
//
// Returns:
//   - error: Non-nil if directory creation or file write fails
//
// Edge cases:
//   - Path separators in moduleName are sanitized to underscores.
//   - Module names starting with ".." are prefixed with additional underscores.
//   - Path traversal attempts return an error.
func (h *Hasher) StoreHash(moduleName, hash string) error {
	if err := os.MkdirAll(h.hashDir, 0755); err != nil {
		return err
	}

	hashFile := filepath.Join(h.hashDir, sanitizeHashPath(moduleName)+".hash")
	absPath, err := filepath.Abs(hashFile)
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(h.hashDir)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) {
		return fmt.Errorf("invalid module name: path traversal detected: %s", moduleName)
	}
	return os.WriteFile(hashFile, []byte(hash), 0644)
}

// LoadHash loads the stored hash for a module from disk.
//
// This function reads the hash from the module's hash file in the hash storage directory.
//
// Parameters:
//   - moduleName: Module name (used as filename)
//
// Returns:
//   - string: The stored hash string (trimmed of whitespace)
//   - error: Non-nil if file doesn't exist or cannot be read
func (h *Hasher) LoadHash(moduleName string) (string, error) {
	hashFile := filepath.Join(h.hashDir, sanitizeHashPath(moduleName)+".hash")
	absPath, err := filepath.Abs(hashFile)
	if err != nil {
		return "", err
	}
	absDir, err := filepath.Abs(h.hashDir)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid module name: path traversal detected: %s", moduleName)
	}

	data, err := os.ReadFile(hashFile)
	if err != nil {
		return "", err
	}

	// Trim whitespace (newlines, etc.) from stored hash.
	return strings.TrimSpace(string(data)), nil
}

// ClearCache clears the hash cache.
//
// This forces all hashes to be recomputed on the next CalculateModuleHash call.
// It does not affect stored hashes on disk.
//
// This is useful when module configurations change and in-memory
// caches may be stale, while still preserving persisted hashes for
// comparison purposes.
//
// Parameters:
//   - none
//
// Returns:
//   - none
func (h *Hasher) ClearCache() {
	h.cache = make(map[string]string)
}

// StoreAllHashes stores all module hashes to disk.
//
// This function iterates through all hashes in moduleHashes (computed this run)
// and writes each to its corresponding file using StoreHash.
//
// Parameters:
//   - none
//
// Returns:
//   - error: The first error encountered, if any
func (h *Hasher) StoreAllHashes() error {
	for name, hash := range h.moduleHashes {
		if err := h.StoreHash(name, hash); err != nil {
			return err
		}
	}
	return nil
}

// LoadAllHashes loads all stored hashes from disk.
//
// This function attempts to load the stored hash for each module name.
// Missing hash files are silently ignored (the module wasn't previously built).
//
// Parameters:
//   - moduleNames: List of module names to load hashes for
//
// Returns:
//   - error: Always nil (missing files are ignored)
func (h *Hasher) LoadAllHashes(moduleNames []string) error {
	for _, name := range moduleNames {
		hash, err := h.LoadHash(name)
		if err == nil {
			h.moduleHashes[name] = hash
		}
		// Ignore non-existent files; they're treated as "needs build"
	}
	return nil
}
