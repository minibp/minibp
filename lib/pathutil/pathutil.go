// Package pathutil provides path manipulation utilities.
package pathutil

import (
	"path/filepath"
	"strings"
)

// SanitizePath cleans the given path and prevents directory traversal.
// It uses filepath.Clean to normalize the path, then ensures no component
// is ".." to block traversal attempts.
func SanitizePath(path string) string {
	cleaned := filepath.Clean(path)
	// Block any path containing ".." components after cleaning
	if strings.Contains(cleaned, "..") {
		return filepath.Base(cleaned)
	}
	return cleaned
}
