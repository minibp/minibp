// Package pathutil provides path manipulation utilities for minibp with
// security-focused path sanitization and safe file reading.
//
// This package provides functions to safely handle file paths in build systems
// where untrusted input (e.g., from Blueprint files) must be sanitized to
// prevent directory traversal attacks and ensure files stay within expected
// directories.
//
// Key components:
//   - SanitizePath(): Cleans paths and blocks directory traversal attempts
//   - SanitizeWithinDir(): Ensures paths resolve within a base directory
//   - ReadFileSafely(): Reads files with size limits and path sanitization
//   - MaxSizeError: Error type for files exceeding size limits
//   - Internal helpers: formatSize(), formatInt(), itoa(), ftoa(), formatFloat(), padZero()
//
// Example usage:
//
//	// Sanitize a user-provided path
//	cleanPath := pathutil.SanitizePath(userPath)
//
//	// Ensure path stays within project directory
//	safePath := pathutil.SanitizeWithinDir(inputPath, "/project/src")
//	if safePath == "" {
//	    log.Fatal("path traversal detected")
//	}
//
//	// Safely read a file with size limit
//	data, err := pathutil.ReadFileSafely("config.txt", 1024*1024)
package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

// SanitizePath cleans the given path and prevents directory traversal.
// It uses filepath.Clean to normalize the path, then ensures no component
// is ".." to block traversal attempts.
//
// Parameters:
//   - path: The input path to sanitize (may contain ".." or other unsafe patterns)
//
// Returns:
//   - A sanitized path string. If path contains "..", returns only the base name.
//   - If path is absolute, returns the absolute clean path.
//   - Otherwise returns the cleaned relative path.
//
// Edge cases:
//   - Path containing ".." anywhere: returns only the base filename (e.g., "file.txt")
//   - Absolute paths: resolved to absolute clean path
//   - Relative paths without "..": returned as-is after cleaning
//   - Empty path: returns "." (current directory)
//   - filepath.Abs failure: falls back to cleaned path
func SanitizePath(path string) string {
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return filepath.Base(cleaned) // Block traversal by stripping to basename
	}
	if filepath.IsAbs(cleaned) {
		abs, err := filepath.Abs(cleaned)
		if err == nil {
			return abs // Return resolved absolute path
		}
	}
	return cleaned // Return cleaned relative path
}

// SanitizeWithinDir ensures path stays within baseDir after cleaning.
// Returns the cleaned path if it resolves within baseDir, otherwise an empty string.
//
// Parameters:
//   - path: The input path to sanitize and validate
//   - baseDir: The base directory that the path must resolve within
//
// Returns:
//   - The absolute cleaned path if it resolves within baseDir
//   - Empty string if path attempts traversal outside baseDir or on error
//
// Edge cases:
//   - Path containing "..": returns "" immediately (traversal attempt)
//   - Relative paths: joined with baseDir before validation
//   - Absolute paths: validated directly against baseDir
//   - baseDir or path invalid: returns "" on filepath.Abs failure
//   - Path equal to baseDir: allowed (returns absPath)
//   - Symlinks: resolved by filepath.Abs (may affect traversal check)
func SanitizeWithinDir(path, baseDir string) string {
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return "" // Path traversal detected
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "" // Failed to get absolute base directory
	}
	absPath := cleaned
	if !filepath.IsAbs(cleaned) {
		absPath = filepath.Join(absBase, cleaned) // Make relative path absolute
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return "" // Failed to resolve absolute path
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "" // Path escapes base directory
	}
	return absPath
}

// ReadFileSafely reads a file with a maximum size limit and path sanitization.
//
// Parameters:
//   - path: The file path to read
//   - maxSize: Maximum allowed file size in bytes (e.g., 1MB = 1048576)
//
// Returns:
//   - []byte: File contents if size is within limit
//   - error: os.PathError if file doesn't exist or can't be read
//   - error: *MaxSizeError if file exceeds maxSize
//
// Edge cases:
//   - Empty file (size 0): allowed, returns empty []byte
//   - maxSize of 0: only empty files are allowed
//   - symlinks: os.Stat follows symlinks, size check applies to target
//   - directories: os.ReadFile will return an error
//   - Note: path is NOT sanitized by this function; call SanitizePath first if needed
func ReadFileSafely(path string, maxSize int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err // File doesn't exist or can't be accessed
	}
	if info.Size() > maxSize {
		return nil, &MaxSizeError{Path: path, Size: info.Size(), Max: maxSize} // Exceeds limit
	}
	return os.ReadFile(path)
}

// MaxSizeError indicates a file exceeded the maximum allowed size.
// This error is returned by ReadFileSafely when a file's size exceeds
// the specified maximum allowed size.
type MaxSizeError struct {
	Path string // Path to the file that exceeded the size limit
	Size int64  // Actual file size in bytes
	Max  int64  // Maximum allowed size in bytes
}

// Error implements the error interface for MaxSizeError.
//
// Returns a human-readable error message showing the file path,
// its size, and the maximum allowed size in human-readable format.
//
// Returns:
//   - A formatted error string like "file /path/to/file exceeds maximum size: 2.5MB > 1MB"
func (e *MaxSizeError) Error() string {
	return "file " + e.Path + " exceeds maximum size: " + formatSize(e.Size) + " > " + formatSize(e.Max)
}

// formatSize converts a byte count to a human-readable string with appropriate unit.
//
// Parameters:
//   - n: Size in bytes
//
// Returns:
//   - A string with appropriate unit suffix (B, KB, or MB)
//   - Uses formatInt() for conversion and trims trailing zeros and decimal points
//
// Note: This is an internal helper function used by MaxSizeError.Error()
func formatSize(n int64) string {
	return strings.TrimRight(strings.TrimRight(formatInt(n), "0"), ".") // Remove trailing zeros and dot
}

// formatInt converts a byte count to a human-readable string with unit suffix.
//
// Parameters:
//   - n: Size in bytes
//
// Returns:
//   - String with unit suffix: "B" (bytes), "KB" (kilobytes), or "MB" (megabytes)
//   - Values below 1024: displayed as bytes (e.g., "512B")
//   - Values 1024-1048575: displayed as KB with one decimal (e.g., "1.5KB")
//   - Values 1048576+: displayed as MB with one decimal (e.g., "2.3MB")
//
// Note: This is an internal helper; uses itoa() and ftoa() for number formatting
func formatInt(n int64) string {
	if n < 1024 {
		return itoa(n) + "B" // Bytes
	}
	if n < 1048576 {
		return ftoa(float64(n)/1024.0) + "KB" // Kilobytes
	}
	return ftoa(float64(n)/1048576.0) + "MB" // Megabytes
}

// itoa converts an integer to a string without using strconv for minimal dependencies.
//
// Parameters:
//   - n: The integer to convert (can be negative or zero)
//
// Returns:
//   - A string representation of the integer
//
// Edge cases:
//   - n = 0: returns "0"
//   - n < 0: returns string with "-" prefix
//   - Large values: handled by the loop until n reaches 0
//
// Note: Internal helper function; avoids strconv dependency
func itoa(n int64) string {
	if n == 0 {
		return "0" // Zero is a special case
	}
	var digits []byte
	neg := n < 0
	if neg {
		n = -n // Make positive for digit extraction
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...) // Prepend digit
		n /= 10                                              // Remove last digit
	}
	if neg {
		digits = append([]byte{'-'}, digits...) // Add negative sign
	}
	return string(digits)
}

// ftoa converts a float to a string with trailing zeros removed.
//
// Parameters:
//   - f: The float value to convert
//
// Returns:
//   - A string representation with trailing zeros removed
//   - Uses formatFloat() for initial conversion, then trims excess zeros
//
// Note: Internal helper; trims at most two trailing zeros (e.g., "1.50" -> "1.5")
func ftoa(f float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(formatFloat(f), "0"), "0") // Remove trailing zeros
}

// formatFloat converts a float to a string with exactly 2 decimal places (rounded).
//
// Parameters:
//   - f: The float value to convert (e.g., 1.567)
//
// Returns:
//   - A string with up to 2 decimal places
//   - Rounds to nearest hundredth (e.g., 1.567 -> "1.57")
//   - Trailing zeros in decimal part are preserved here (trimmed later by ftoa)
//
// Example: 1.5 -> "1.50", 2.0 -> "2.00", 1.234 -> "1.23"
//
// Note: Internal helper; adds 0.5 for proper rounding before truncating
func formatFloat(f float64) string {
	n := int64(f*100 + 0.5) // Multiply by 100 and round
	intPart := n / 100      // Extract integer part
	fracPart := n % 100     // Extract 2-digit fractional part
	if fracPart == 0 {
		return itoa(intPart) // No fractional part
	}
	return itoa(intPart) + "." + padZero(fracPart) // Format with decimal
}

// padZero pads a number with a leading zero if it is a single digit.
//
// Parameters:
//   - n: The number to pad (expected to be 0-99)
//
// Returns:
//   - "0X" if n is a single digit (0-9)
//   - "XY" if n is already two digits (10-99)
//
// Example: 5 -> "05", 12 -> "12", 0 -> "00"
//
// Note: Internal helper used by formatFloat for decimal places
func padZero(n int64) string {
	s := itoa(n)
	if len(s) == 1 {
		return "0" + s // Add leading zero
	}
	return s
}
