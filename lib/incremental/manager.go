// Package incremental implements incremental build functionality for caching and reusing
// parsed Blueprint file results.
//
// This package provides the Manager type to manage incremental parsing state:
//   - Tracks which .bp files have changed (by comparing file hashes)
//   - Caches parsed ASTs as JSON files (stored in .minibp/json/ directory)
//   - Maintains dependency file .minibp/dep.json to record file hashes
//
// Main workflow:
//  1. Create Manager and load existing dependency hashes (if any)
//  2. Check each .bp file for reparsing needs (NeedsReparse)
//  3. If file unchanged, load from JSON cache (LoadJSON)
//  4. If file changed or first seen, reparse and save cache (SaveJSON)
//  5. Finally save updated dependency hashes (SaveDepFile)
//
// Example:
//
//	mgr, err := incremental.NewManager("/path/to/project")
//	if err != nil { return err }
//	needsReparse, _ := mgr.NeedsReparse("foo.bp")
//	if needsReparse {
//	    // Parse file and cache
//	    mgr.SaveJSON("foo.bp", parsedFile)
//	} else {
//	    // Load from cache
//	    cached, _ := mgr.LoadJSON("foo.bp")
//	}
//	mgr.SaveDepFile()
package incremental

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"minibp/lib/parser"
)

// DepFile represents the data structure of the .minibp/dep.json dependency file.
//
// This file records the SHA256 hash of each .bp file to determine if the file has changed
// during the next build, thus deciding whether reparsing is needed.
//
// Example JSON format:
//
//	{
//	  "version": 1,
//	  "hashes": {
//	    "src/foo/Android.bp": "a1b2c3d4...",
//	    "src/bar/Android.bp": "e5f6g7h8..."
//	  }
//	}
//
// Fields:
//   - Version: Dependency file format version, currently 1
//   - Hashes: Map from .bp file path to its SHA256 hash value
type DepFile struct {
	Version int               `json:"version"`
	Hashes  map[string]string `json:"hashes"` // bpFilePath -> sha256hex
}

// Manager manages incremental parsing state.
//
// Manager is responsible for tracking which .bp files have changed and caching
// parsed ASTs as JSON files. It determines if a file needs reparsing by
// comparing the file's SHA256 hash.
//
// Main responsibilities:
//   - Manage cache files under .minibp/ directory
//   - Record and maintain hash values for each .bp file
//   - Provide methods to check if files need reparsing
//   - Provide methods to save and load JSON cache
//
// Workflow:
//  1. Create necessary directories on initialization (.minibp/ and .minibp/json/)
//  2. Attempt to load existing dep.json file to restore previous hash records
//  3. Check each file for reparsing needs during build process
//  4. Save updated dependency information after build completes
type Manager struct {
	workDir string            // project root (where .minibp lives)
	jsonDir string            // .minibp/json/
	depFile string            // .minibp/dep.json
	hashes  map[string]string // in-memory copy of dep.json hashes
}

// NewManager creates a new incremental build manager.
//
// This function initializes a Manager instance and sets up necessary cache directories.
// If .minibp/ or .minibp/json/ directories don't exist, they will be created automatically.
// It also attempts to load an existing dep.json file to restore previous file hash records.
//
// Parameters:
//   - workDir: Project root directory path where .minibp directory will be created
//
// Returns:
//   - *Manager: Initialized Manager instance
//   - error: Returns error if directory creation fails; loading dep.json failure won't return error (starts fresh)
//
// Edge cases:
//   - If .minibp/dep.json doesn't exist or has invalid format, starts fresh (clears hash records)
//   - Directory creation failure returns error immediately
//   - Even if dep.json fails to load, a usable Manager instance is returned
//
// Example:
//
//	mgr, err := incremental.NewManager("/home/user/myproject")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// mgr is now ready to use, will manage cache under /home/user/myproject/.minibp/
func NewManager(workDir string) (*Manager, error) {
	// Construct cache directory and dependency file paths.
	// jsonDir stores cached ASTs as JSON files for incremental builds.
	// depFile stores file hashes to detect which .bp files have changed.
	jsonDir := filepath.Join(workDir, ".minibp", "json")
	depFile := filepath.Join(workDir, ".minibp", "dep.json")

	// Initialize Manager instance with work directory and cache paths.
	// The hashes map starts empty and will be populated from dep.json if it exists.
	m := &Manager{
		workDir: workDir,
		jsonDir: jsonDir,
		depFile: depFile,
		hashes:  make(map[string]string), // Initialize empty hash map to store file hashes for incremental builds
	}

	// Create .minibp/json/ directory if it doesn't exist.
	// MkdirAll creates all directories in the path that don't exist.
	// Permission 0755 allows owner read/write/execute, others read/execute.
	if err := os.MkdirAll(jsonDir, 0755); err != nil {
		return nil, fmt.Errorf("create json dir: %w", err)
	}

	// Attempt to load existing dependency file.
	// If file doesn't exist or is corrupted, don't return error; start fresh (clear hashes).
	// This ensures the build won't fail even if the cache is corrupted or missing.
	if err := m.loadDepFile(); err != nil {
		// If dep.json doesn't exist or has invalid format, start fresh.
		// This ensures build won't fail even if cache is corrupted.
		m.hashes = make(map[string]string)
	}

	return m, nil
}

// loadDepFile loads the .minibp/dep.json dependency file.
//
// This function reads the dep.json file from disk and parses its JSON content,
// loading the file hash values recorded in the file into memory (m.hashes).
// The dep file contains version information and file hashes for incremental builds.
//
// How it works:
//  1. Checks file size to prevent OOM from oversized files (10MB limit)
//  2. Reads the entire file content
//  3. Parses JSON into the DepFile struct
//  4. Copies the Hashes map into memory
//
// Returns:
//   - error: Returns error if file read fails, file is too large, or JSON parsing fails
//   - File not found: Returns os.PathError
//   - File too large: Returns error if dep.json exceeds 10MB
//   - Invalid JSON format: Returns json.UnmarshalError
//   - Success: Returns nil
//
// Edge cases:
//   - If dep.json exists but Hashes field is null, initializes to empty map
//   - If dep.json exists but format version doesn't match, still attempts to load (only uses Hashes field)
//   - Caller should handle returned error, typically choosing to start fresh rather than fail
//   - Empty dep.json file returns empty hash map (not an error)
//
// Example dep.json content:
//
//	{
//	  "version": 1,
//	  "hashes": {
//	    "src/foo.bp": "a1b2c3d4e5f6..."
//	  }
//	}
func (m *Manager) loadDepFile() error {
	// Check file size before reading to prevent OOM
	info, err := os.Stat(m.depFile)
	if err != nil {
		return err
	}
	const maxDepFileSize = 10 << 20 // 10MB limit
	if info.Size() > maxDepFileSize {
		return fmt.Errorf("dep file too large: %d bytes (max %d)", info.Size(), maxDepFileSize)
	}

	// Read raw content of dep.json file.
	// Returns error if file doesn't exist or cannot be read.
	data, err := os.ReadFile(m.depFile)
	if err != nil {
		return err
	}

	// Parse JSON content into DepFile struct.
	// Returns error if JSON format is invalid or missing required fields.
	var dep DepFile
	if err := json.Unmarshal(data, &dep); err != nil {
		return err
	}

	// Copy loaded hash values into memory.
	m.hashes = dep.Hashes
	// Handle case where Hashes is null (JSON "hashes": null).
	// JSON unmarshaling sets map to nil when the JSON value is null.
	if m.hashes == nil {
		m.hashes = make(map[string]string)
	}
	return nil
}

// SaveDepFile saves current dependency hashes to .minibp/dep.json file.
//
// This function serializes the in-memory file hash records (m.hashes) to JSON format
// and writes to the dep.json file. Uses MarshalIndent to generate formatted JSON
// for human readability and version control.
//
// Returns:
//   - error: Returns error if JSON serialization fails or file write fails
//   - Serialization failure: Returns json.MarshalError
//   - Write failure: Returns os.PathError or permission error
//   - Success: Returns nil
//
// Edge cases:
//   - If m.hashes is empty map, still writes a valid JSON file (hashes as empty object)
//   - File permission set to 0640 (owner read/write, group read-only)
//   - Write failure doesn't rollback previous state
//
// Example dep.json output:
//
//	{
//	  "version": 1,
//	  "hashes": {
//	    "src/foo.bp": "a1b2c3d4...",
//	    "src/bar.bp": "e5f6g7h8..."
//	  }
//	}
func (m *Manager) SaveDepFile() error {
	// Construct DepFile struct with current hash and mtime values for serialization.
	// Version is set to 1; this allows future format changes with backward compatibility.
	dep := DepFile{
		Version: 1,
		Hashes:  m.hashes,
	}
	// Serialize to formatted JSON using two-space indentation.
	// MarshalIndent produces human-readable JSON for debugging and version control.
	data, err := json.MarshalIndent(dep, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dep file: %w", err)
	}
	// Write dep.json file with 0640 permissions (owner r/w, group read-only).
	// Overwrites existing file if present; creates new file if not.
	return os.WriteFile(m.depFile, data, 0640)
}

// hashFile computes the SHA256 hash of the specified file's content.
//
// This function opens the file, reads its entire content, and computes the SHA256 hash.
// The hash value is returned as a hexadecimal string (64 characters long).
//
// Parameters:
//   - path: File path to compute hash for
//
// Returns:
//   - string: SHA256 hash of file content (hex lowercase string)
//   - error: Returns error if file open or read fails
//   - File not found: Returns os.PathError
//   - Read failure: Returns io.ReadError
//   - Success: Returns nil
//
// Edge cases:
//   - Empty file hash: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
//   - Large files are read in streaming fashion, not loaded entirely into memory
//   - File open failure returns error immediately
//
// Example:
//
//	hash, err := mgr.hashFile("src/foo.bp")
//	if err != nil {
//	    return err
//	}
//	fmt.Println(hash) // Output similar to: a1b2c3d4e5f6...
func (m *Manager) hashFile(path string) (string, error) {
	// Open file for reading.
	// Returns error if file doesn't exist or lacks read permissions.
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file for hash: %w", err)
	}
	// Ensure file is closed when function returns.
	// Using defer ensures closure even if errors occur during reading.
	defer f.Close()

	// Initialize SHA256 hash calculator.
	// The hash is computed incrementally as data is streamed to it.
	h := sha256.New()
	// Stream file content to hash calculator.
	// io.Copy reads from file and writes to hash, computing digest incrementally.
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash file content: %w", err)
	}
	// Compute final hash value and format as lowercase hex string (64 chars).
	// Sum(nil) finalizes the hash and returns the digest bytes.
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// NeedsReparse checks if the specified .bp file needs reparsing.
//
// This function computes the current SHA256 hash of the file and compares it
// with the stored hash value in memory. If the file is first seen or content
// has changed, reparsing is needed.
//
// Parameters:
//   - bpFile: Path to the .bp file to check
//
// Returns:
//   - bool: true means reparsing needed, false means file unchanged and cache can be used
//   - error: Returns error if file hash computation fails (returns true, error in this case)
//
// Cases requiring reparsing:
//   - First build: No stored hash value for this file
//   - File modified: Current hash doesn't match stored hash
//   - Hash computation failed: Returns error (caller decides how to handle)
//
// Edge cases:
//   - If file hash computation fails, returns true (conservative strategy: reparse rather than use potentially stale cache)
//   - First-seen file automatically updates hash value in memory
//   - File content change updates hash in memory (but not persisted, need to call SaveDepFile)
//   - Even if file is read-only or empty, hash is computed normally
//
// Example:
//
//	needsReparse, err := mgr.NeedsReparse("src/foo/Android.bp")
//	if err != nil {
//	    // Handle error (file may not exist)
//	    return err
//	}
//	if needsReparse {
//	    // Need to reparse file
//	    parsedFile, _ := parser.ParseFile(...)
//	    mgr.SaveJSON("src/foo/Android.bp", parsedFile)
//	} else {
//	    // Can use cache
//	    cached, _ := mgr.LoadJSON("src/foo/Android.bp")
//	}
func (m *Manager) NeedsReparse(bpFile string) (bool, error) {
	// Compute current hash value of the file (skip mtime check for debugging)
	currentHash, err := m.hashFile(bpFile)
	if err != nil {
		// Hash computation failed; conservatively assume reparse is needed.
		return true, fmt.Errorf("hash %s: %w", bpFile, err)
	}

	// Check if we have a stored hash value for this file.
	storedHash, hashExists := m.hashes[bpFile]
	if !hashExists {
		// First time seeing this file; record hash and signal reparse needed.
		m.hashes[bpFile] = currentHash
		return true, nil
	}

	// Compare stored hash with current hash to detect changes.
	if storedHash != currentHash {
		// File has changed; update hash and signal reparse needed.
		m.hashes[bpFile] = currentHash
		return true, nil
	}

	// File unchanged; cache can be used.
	return false, nil
}

// jsonFilePath returns the JSON cache file path for the specified .bp file.
//
// This function converts .bp file path to cache file path using these rules:
//  1. First try to get relative path from work directory (ensures path stability)
//  2. Replace path separators (/ or \) with __ to avoid directory traversal issues
//  3. Use sanitizeName to further clean special characters in filename
//  4. Final file is placed under .minibp/json/ directory with .json extension
//
// Parameters:
//   - bpFile: Path to .bp file (can be absolute or relative)
//
// Returns:
//   - string: Full path to the JSON cache file
//
// Edge cases:
//   - If relative path cannot be computed, falls back to original path
//   - Special characters in path (like :*?"<>|) are replaced with _
//   - Path separators on different OS are handled correctly
//
// Example:
//
//	mgr.workDir = "/home/user/project"
//	mgr.jsonFilePath("/home/user/project/src/foo/Android.bp")
//	// Returns: /home/user/project/.minibp/json/src__foo__Android.bp.json
//
//	mgr.jsonFilePath("src/bar.bp")
//	// Returns: /home/user/project/.minibp/json/src__bar.bp.json
func (m *Manager) jsonFilePath(bpFile string) string {
	// Try to get the path relative to work directory for cache key stability.
	// This ensures cache hits even if the absolute path changes but relative path stays the same.
	rel, err := filepath.Rel(m.workDir, bpFile)
	if err != nil {
		// Cannot compute relative path (e.g., different drives on Windows); use original path.
		rel = bpFile
	}
	// Replace path separators with __ to avoid directory traversal issues in filenames.
	// This ensures the cache filename is safe across different operating systems.
	sanitized := strings.ReplaceAll(rel, string(filepath.Separator), "__")
	sanitized = strings.ReplaceAll(sanitized, "/", "__")
	// Construct final cache path: .minibp/json/<sanitized_name>.json
	return filepath.Join(m.jsonDir, sanitizeName(sanitized)+".json")
}

// sanitizeName ensures filename is safe and contains no characters that could cause
// path traversal or filesystem issues.
//
// This function iterates through each character in the input string and replaces
// the following special characters with underscore (_):
//   - Path separators: / and \
//   - Windows forbidden characters: : * ? " < > |
//
// This prevents security issues (like path traversal attacks) from malicious or
// accidental file paths, or problems on filesystems that don't support certain characters.
//
// Parameters:
//   - name: Filename or path string to sanitize
//
// Returns:
//   - string: Sanitized safe filename
//
// Edge cases:
//   - Empty string returns empty string
//   - If no special characters, returns original string
//   - All matching special characters are replaced, not just the first
//   - Unicode characters not in special character list are preserved
//
// Example:
//
//	sanitizeName("src/foo:bar.bp")
//	// Returns: src_foo_bar.bp
//
//	sanitizeName("C:\\Users\\test\\file.bp")
//	// Returns: C__Users_test__file.bp
//
//	sanitizeName("normal_file.bp")
//	// Returns: normal_file.bp (no change)
func sanitizeName(name string) string {
	// Use strings.Map to iterate over each character and apply replacement.
	// This approach handles Unicode characters correctly via rune iteration.
	result := strings.Map(func(r rune) rune {
		// Check if character is a special character that needs replacement.
		// These characters can cause issues with file paths on various operating systems.
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			return '_'
		}
		return r
	}, name)
	return result
}

// SaveJSON saves the parsed File AST to a JSON cache file.
//
// This function serializes the parser.File struct to formatted JSON and writes
// to the cache directory. Cache file path is determined by jsonFilePath method,
// typically located under .minibp/json/ directory.
//
// Parameters:
//   - bpFile: Original .bp file path, used to generate cache filename
//   - file: Parsed AST (Abstract Syntax Tree) to cache
//
// Returns:
//   - error: Returns error if JSON serialization fails or file write fails
//   - Serialization failure: Returns json.MarshalError
//   - Write failure: Returns os.PathError or permission error
//   - Success: Returns nil
//
// Edge cases:
//   - If target directory doesn't exist, returns error (should be created in NewManager)
//   - File permission set to 0644 (owner read/write, group and others read-only)
//   - If cache file already exists, it will be overwritten
//   - Serialization uses indented format for debugging and version control
//
// Example:
//
//	parsedFile, _ := parser.ParseFile(reader, "src/foo.bp", source)
//	err := mgr.SaveJSON("src/foo.bp", parsedFile)
//	if err != nil {
//	    // Cache failure shouldn't block build, can log warning
//	    fmt.Fprintf(os.Stderr, "warning: failed to cache: %v\n", err)
//	}
func (m *Manager) SaveJSON(bpFile string, file *parser.File) error {
	// Get the cache file path for this .bp file.
	jsonPath := m.jsonFilePath(bpFile)

	// Serialize the AST to formatted JSON with two-space indentation.
	// This produces human-readable JSON for debugging and version control.
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ast to json: %w", err)
	}

	// Write the serialized AST to the cache file with 0600 permissions (owner only).
	return os.WriteFile(jsonPath, data, 0600)
}

// LoadJSON loads a previously parsed File AST from JSON cache file.
//
// This function attempts to read the JSON cache for the specified .bp file
// and deserializes it into a parser.File struct. If the cache file doesn't
// exist or is corrupted, returns nil (not an error, just cache miss).
//
// Parameters:
//   - bpFile: Original .bp file path, used to locate cache file
//
// Returns:
//   - *parser.File: Parsed AST if cache exists and is valid; otherwise nil
//   - error: Returns error if JSON deserialization fails; cache miss returns nil
//
// Edge cases:
//   - Cache file doesn't exist: Returns nil, nil (cache miss, not an error)
//   - Cache file corrupted or invalid JSON format: Returns error
//   - Cache file exists but content is empty: Returns error (empty JSON can't be deserialized)
//   - If dep.json and cache are inconsistent (e.g., cache manually deleted), triggers reparse
//
// Example:
//
//	cached, err := mgr.LoadJSON("src/foo.bp")
//	if err != nil {
//	    // Cache corrupted, need to handle error
//	    return err
//	}
//	if cached == nil {
//	    // Cache miss, need to reparse
//	    parsedFile, _ := parser.ParseFile(...)
//	    mgr.SaveJSON("src/foo.bp", parsedFile)
//	} else {
//	    // Use cached AST
//	    processFile(cached)
//	}
func (m *Manager) LoadJSON(bpFile string) (*parser.File, error) {
	// Get the cache file path for this .bp file.
	jsonPath := m.jsonFilePath(bpFile)

	// Try to read the cache file.
	// A missing file is not an error; it just means cache miss.
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		// Cache miss (file doesn't exist); this is not an error.
		// Caller should reparse the file and call SaveJSON to cache it.
		return nil, nil
	}

	// Deserialize JSON content back into parser.File struct.
	// Returns error if JSON format is invalid or doesn't match the struct.
	var file parser.File
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("unmarshal cached ast: %w", err)
	}

	return &file, nil
}

// UpdateHash updates the stored hash value for the specified .bp file.
//
// This function computes the current SHA256 hash of the file and updates
// the in-memory hash table. Note: This function only updates the in-memory
// hash value and does not automatically persist to dep.json.
// To persist changes, call SaveDepFile().
//
// Parameters:
//   - bpFile: Path to the .bp file to update hash for
//
// Returns:
//   - error: Returns error if file hash computation fails
//   - File not found: Returns os.PathError
//   - Read failure: Returns io.ReadError
//   - Success: Returns nil
//
// Edge cases:
//   - If file is first seen, adds to hash table
//   - If file already has hash value, overwrites with new value
//   - Hash is recomputed and updated even if unchanged
//   - Caller must manually call SaveDepFile() to persist
//
// When to use:
//   - When manually modifying .bp files and wanting to update hash without reparsing
//   - Usually not needed after NeedsReparse already updated memory hash
//   - Mainly for external scenarios requiring manual hash updates
//
// Example:
//
//	err := mgr.UpdateHash("src/foo.bp")
//	if err != nil {
//	    return err
//	}
//	// Remember to persist
//	mgr.SaveDepFile()
func (m *Manager) UpdateHash(bpFile string) error {
	// Compute current hash value of the file.
	// This recomputes the hash even if the file hasn't changed.
	hash, err := m.hashFile(bpFile)
	if err != nil {
		return err
	}
	// Update the in-memory hash value for this file.
	// Note: This does not persist to disk; call SaveDepFile() to persist.
	m.hashes[bpFile] = hash
	return nil
}
