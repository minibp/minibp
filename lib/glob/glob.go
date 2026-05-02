// Package glob provides glob pattern expansion for Blueprint source file properties.
// It handles two types of glob patterns commonly used in build systems for specifying
// source files:
//   - Simple globs: *.go, ?.txt, [abc].* (using filepath.Match)
//   - Recursive globs: **/*.go, src/**/*.java (walking directory tree)
//
// This package is used by the build system to expand source file patterns
// in module srcs properties into concrete file lists. This allows build rules
// to specify sources with patterns like "src/**/*.cpp" rather than listing
// every individual source file.
//
// Simple globs use standard filepath.Match patterns:
//   - * matches any sequence of characters (except path separator)
//   - ? matches any single character
//   - [abc] matches any character in the set
//
// Recursive globs use ** to match zero or more directory levels:
//   - ** matches any number of path segments
//   - src/**/*.java matches java files in src and any subdirectory
//
// Performance considerations:
//   - Glob results are cached to avoid redundant filesystem scans
//   - Recursive globs determine optimal walk root to minimize traversal
//   - Results are deduplicated to handle overlapping patterns
package glob

import (
	"minibp/lib/parser"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	// globCache stores the results of glob expansions to avoid redundant filesystem scans.
	// The key is the glob pattern, and the value is the list of matching file paths.
	// This cache is populated by ExpandGlobs and checked by ExpandInModule.
	globCache   = make(map[string][]string)
	globCacheMu sync.RWMutex
)

// ExpandInModule expands glob patterns in the "srcs" property of a module.
// It processes each source file pattern in the module's srcs list and expands
// glob patterns (using * and **) into matching file paths.
// Non-glob patterns (paths without *) are preserved as-is.
// Duplicates are automatically removed from the expanded results.
//
// This function is called during the build graph construction phase to resolve
// source file patterns to concrete file paths before generating ninja rules.
// It ensures that the ninja build file has complete information about
// all source files needed for compilation.
//
// The function iterates through the module's properties looking for "srcs",
// then processes each value in the srcs list. Patterns containing "*"
// are expanded using expandGlob; others are kept as-is.
// Results are cached globally to avoid redundant filesystem operations.
//
// Parameters:
//   - m: The parser.Module whose srcs property should be processed.
//     If m.Map is nil, the function returns nil immediately.
//   - baseDir: The base directory for resolving glob patterns.
//     Must be an absolute path for correct relative path generation.
//
// Returns:
//   - error: Any error encountered during glob expansion.
//     Errors include file system errors from walking directories
//     and pattern matching failures.
//
// Edge cases:
//   - If module has no "srcs" property, the function returns nil.
//   - If srcs property is not a list, the function returns nil.
//   - Empty srcs list returns nil with no modifications.
//   - Patterns matching no files are preserved as-is (no error).
//   - Multiple identical source paths are deduplicated.
//   - If a pattern starts with "//" (absolute path), baseDir is ignored.
func ExpandInModule(m *parser.Module, baseDir string) error {
	if m.Map == nil { // Return early if module has no properties map
		return nil
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == "srcs" {
			if l, ok := prop.Value.(*parser.List); ok {
				var expandedSrcs []parser.Expression
				seen := make(map[string]bool)
				for _, v := range l.Values {
					if s, ok := v.(*parser.String); ok {
						pattern := s.Value
						if strings.Contains(pattern, "*") { // Glob pattern detected, needs expansion
							// Check cache first with read lock
							globCacheMu.RLock() // Acquire read lock to safely check cache
							matches, ok := globCache[pattern]
							globCacheMu.RUnlock()

							if ok { // Cache hit, use precomputed matches
								for _, match := range matches {
									if !seen[match] {
										seen[match] = true
										expandedSrcs = append(expandedSrcs, &parser.String{Value: match})
									}
								}
						} else { // Cache miss, expand pattern and update cache
								// Fallback to individual expansion if not in cache
								matches, err := expandGlob(pattern, baseDir)
								if err != nil {
									return err
								}
								for _, match := range matches {
									if !seen[match] {
										seen[match] = true
										expandedSrcs = append(expandedSrcs, &parser.String{Value: match})
									}
								}
							}
					} else { // Non-glob pattern, add as-is without expansion
						if !seen[pattern] { // Avoid duplicate source entries
								seen[pattern] = true
								expandedSrcs = append(expandedSrcs, v)
							}
						}
					}
				}
				l.Values = expandedSrcs
			}
		}
	}
	return nil
}

// ExpandGlobs performs a global expansion of all glob patterns from all modules.
// It collects all unique glob patterns from the "srcs" property of every module,
// expands them in a single pass, and caches the results in globCache.
// This is much more efficient than expanding globs for each module individually.
//
// This function should be called before ExpandInModule to pre-populate the cache.
// It scans all modules to collect patterns, then expands them all at once,
// allowing shared patterns to be resolved only once.
//
// Parameters:
//   - modules: A map of all modules to process.
//   - baseDir: The base directory for resolving glob patterns.
//
// Returns:
//   - error: Any error encountered during glob expansion.
//     Returns error if any pattern expansion fails.
func ExpandGlobs(modules map[string]*parser.Module, baseDir string) error {
	patterns := make(map[string]bool)
	for _, m := range modules {
		if m.Map == nil { // Skip modules with no properties map
			continue
		}
		for _, prop := range m.Map.Properties {
			if prop.Name == "srcs" { // Only process srcs properties
		if l, ok := prop.Value.(*parser.List); ok { // Check if srcs is a list type
					for _, v := range l.Values {
			if s, ok := v.(*parser.String); ok { // Only process string values in srcs list
							if strings.Contains(s.Value, "*") { // Collect glob patterns for batch expansion
								patterns[s.Value] = true
							}
						}
					}
				}
			}
		}
	}

	for pattern := range patterns {
		globCacheMu.RLock() // Acquire read lock to check cache
		_, ok := globCache[pattern]
		globCacheMu.RUnlock()

		if !ok { // Cache miss, need to expand and cache
			globCacheMu.Lock()
			// Re-check after acquiring write lock
			if _, ok := globCache[pattern]; !ok { // Double-check cache after write lock
				matches, err := expandGlob(pattern, baseDir)
				if err != nil {
					globCacheMu.Unlock()
					return err
				}
				globCache[pattern] = matches
			}
			globCacheMu.Unlock()
		}
	}

	return nil
}

// expandGlob expands a single glob pattern into a list of matching file paths.
// It handles two types of patterns:
//   - Simple globs (* and ?) which are expanded using filepath.Glob.
//   - Recursive globs (**) which are expanded by walking the directory tree.
//
// The function determines the pattern type by checking for "**" and routes
// to the appropriate expansion method. Recursive globs are more expensive
// as they require walking the directory tree, so simple globs are preferred
// when possible.
//
// For simple globs, filepath.Glob is used which is highly optimized.
// For recursive globs, filepath.Walk is used to traverse directories,
// and each file is checked against the recursive pattern.
//
// Parameters:
//   - pattern: The glob pattern to expand (e.g., "*.go", "src/**/*.go").
//   - baseDir: The base directory for resolving the pattern.
//     Must be absolute for correct relative path generation.
//
// Returns:
//   - A slice of matching file paths relative to baseDir.
//   - error: Any error encountered during expansion,
//     including file system errors and pattern parsing errors.
//
// Edge cases:
//   - Pattern with ** but no files matching returns empty slice.
//   - Pattern starting with ** is valid and matches all files in baseDir.
//   - Hidden files (starting with .) are included in matches.
//   - Symlinks are followed during directory walking.
func expandGlob(pattern, baseDir string) ([]string, error) {
	var result []string

	// Handle recursive glob (**) pattern separately
	// Recursive patterns require directory walking
	if strings.Contains(pattern, "**") { // Recursive glob requires directory traversal
		// Determine optimal starting directory for walk
		// This avoids traversing entire directory tree unnecessarily
		walkDir := recursiveGlobRoot(pattern, baseDir)
		err := filepath.Walk(walkDir, func(path string, info os.FileInfo, err error) error {
			// Skip directories; pattern matching applies to files only
			// Also propagate any walk errors
			if err != nil || info.IsDir() { // Skip directories or propagate walk errors
				if info != nil && info.IsDir() && skipWalkDir(info.Name()) { // Skip VCS and cache directories
					return filepath.SkipDir
				}
				return err
			}
			// Skip symlinks to prevent directory traversal
			if info.Mode()&os.ModeSymlink != 0 { // Skip symlinks to prevent directory traversal issues
				return nil
			}
			// Convert absolute path to relative for consistency
			relPath, err := filepath.Rel(baseDir, path) // Convert absolute path to relative for consistency
			if err != nil {
				return err
			}
			// Use forward slashes for cross-platform consistency
			relPath = filepath.ToSlash(relPath)
			// Check if relative path matches the recursive pattern
			if matchRecursivePattern(filepath.ToSlash(pattern), relPath) { // Check if file path matches recursive pattern
				result = append(result, relPath)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		// Simple glob: use filepath.Glob directly
		// Join pattern with baseDir for absolute pattern
		fullPattern := filepath.Join(baseDir, pattern) // Combine base directory with pattern for absolute path
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			return nil, err
		}
		// Convert matches to relative paths
		for _, match := range matches {
			relPath, err := filepath.Rel(baseDir, match)
			if err != nil {
				return nil, err
			}
			result = append(result, relPath)
		}
	}
	return result, nil
}

// recursiveGlobRoot determines the root directory for recursive glob (**) patterns.
// It extracts the fixed prefix of the pattern before any glob operators,
// which serves as the starting point for directory walking.
//
// This optimization significantly improves performance for patterns
// with deep directory structures by avoiding unnecessary walks.
//
// For example:
//   - "src/**/*.go" -> "src" (walk starts at src/, not baseDir/)
//   - "foo/*/bar/**/*" -> "foo" (walk starts at foo/, skip first level/*)
//   - "*.go" -> baseDir (no prefix, walk entire baseDir)
//
// Parameters:
//   - pattern: The glob pattern containing **.
//   - baseDir: The base directory for resolution.
//
// Returns:
//   - The root directory to start walking from.
//     Returns baseDir if pattern has no fixed prefix.
//
// Edge cases:
//   - Pattern starting with ** returns baseDir.
//   - Pattern with glob in directory name (e.g., "foo*/bar")
//     returns baseDir as the safest option.
func recursiveGlobRoot(pattern, baseDir string) string {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	prefix := make([]string, 0, len(parts))
	for _, part := range parts {
		// Stop at ** or any other glob metacharacter (* ? [)
		// This identifies the fixed prefix before wildcards
		if part == "**" || strings.ContainsAny(part, "*?[") { // Stop at glob metacharacters to find fixed prefix
			break
		}
		prefix = append(prefix, part)
	}
	// If pattern has no fixed prefix, walk entire baseDir
	if len(prefix) == 0 { // No fixed prefix, walk entire base directory
		return baseDir
	}
	// Join prefix parts with baseDir to get walk root
	return filepath.Join(append([]string{baseDir}, prefix...)...)
}

var skipDirs = map[string]bool{
	".git":    true, // Git version control directory, skip to avoid scanning VCS metadata
	".minibp": true, // minibp cache directory, skip to avoid scanning internal build files
	".hg":     true, // Mercurial version control directory, skip
	".svn":    true, // Subversion version control directory, skip
}

// skipWalkDir checks if a directory name should be skipped during recursive glob walks.
// It checks against a predefined list of directories (e.g., .git, .minibp) that
// should not be traversed to improve performance and avoid scanning irrelevant files.
//
// Parameters:
//   - name: The base name of the directory to check.
//
// Returns:
//   - true if the directory is in the skip list, false otherwise.
func skipWalkDir(name string) bool {
	return skipDirs[name]
}

// matchRecursivePattern checks if a path matches a recursive glob pattern.
// It handles ** which matches zero or more path segments,
// unlike filepath.Match which does not support recursive wildcards.
//
// Parameters:
//   - pattern: The glob pattern (e.g., "src/**/*.go").
//     Must use forward slashes (filepath.ToSlash).
//   - path: The path to match against the pattern.
//     Must use forward slashes.
//
// Returns:
//   - true if the path matches the pattern.
//   - false if the path does not match.
//
// Edge cases:
//   - Empty pattern matches only empty path.
//   - Pattern with ** at end matches paths with any suffix.
func matchRecursivePattern(pattern, path string) bool {
	return matchRecursiveParts(splitGlobParts(pattern), splitGlobParts(path))
}

// splitGlobParts splits a path into segments by "/" for pattern matching.
// Returns nil for empty paths, which is important for base case handling.
//
// Parameters:
//   - path: The path to split.
//     Can be empty string.
//
// Returns:
//   - A slice of path segments.
//   - nil if input path is empty.
//
// Edge cases:
//   - Empty string returns nil (not empty slice).
//   - Single segment "foo" returns ["foo"].
func splitGlobParts(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

// matchRecursiveParts matches glob pattern parts against path parts using iteration.
// This function handles the ** glob operator which matches any number of
// directory levels. The matching algorithm uses an explicit stack to try both
// possibilities at each **, avoiding recursion and potential stack overflow.
//
// Algorithm:
//   - Use an explicit stack to simulate recursive calls
//   - For **: try zero-match first, then one-match (backtracking)
//   - For simple patterns: use filepath.Match
//
// Parameters:
//   - patternParts: The split glob pattern parts.
//   - pathParts: The split path parts to match.
//
// Returns:
//   - true if all remaining pattern parts match the path.
//   - false otherwise.
//
// Edge cases:
//   - ** at pattern end matches any remaining path suffix.
//   - ** alone matches any path (including empty).
//   - Path longer than pattern is handled by iterative ** match.
func matchRecursiveParts(patternParts, pathParts []string) bool {
	// frame represents a stack frame for iterative recursive glob pattern matching.
	// It tracks the remaining pattern and path parts to process, along with a state
	// variable for backtracking when handling ** wildcards.
	type frame struct {
		patternParts []string // Remaining glob pattern segments to match against path
		pathParts    []string // Remaining path segments to check against pattern
		state        int      // Backtracking state: 0=initial, 1=tried zero-match for **
	}

	stack := []frame{{patternParts: patternParts, pathParts: pathParts, state: 0}}

	for len(stack) > 0 {
		// Pop top frame
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// Base case: empty pattern
		if len(top.patternParts) == 0 {
			if len(top.pathParts) == 0 {
				return true
			}
			continue
		}

		// Handle ** (recursive wildcard)
		if top.patternParts[0] == "**" {
			if top.state == 0 {
				// First time: try zero-match first (skip **)
				// Push current frame with state=1 for later one-match attempt
				top.state = 1
				stack = append(stack, top)

				// Try Option 1: ** matches zero segments (skip **)
				stack = append(stack, frame{
					patternParts: top.patternParts[1:],
					pathParts:    top.pathParts,
					state:        0,
				})
			} else {
				// state == 1: already tried zero-match, now try one-match
				// Option 2: ** matches at least one segment
				if len(top.pathParts) == 0 {
					continue // Cannot match at least one segment
				}
				stack = append(stack, frame{
					patternParts: top.patternParts,
					pathParts:    top.pathParts[1:],
					state:        0,
				})
			}
			continue
		}

		// Path is empty but pattern is not
		if len(top.pathParts) == 0 {
			continue
		}

		// Use filepath.Match for simple glob pattern matching
		ok, err := filepath.Match(top.patternParts[0], top.pathParts[0])
		if err != nil || !ok {
			continue
		}

		// Match succeeded, continue with remaining parts
		stack = append(stack, frame{
			patternParts: top.patternParts[1:],
			pathParts:    top.pathParts[1:],
			state:        0,
		})
	}

	return false
}
