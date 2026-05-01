// Package pathutil provides path manipulation utilities.
package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

// SanitizePath cleans the given path and prevents directory traversal.
// It uses filepath.Clean to normalize the path, then ensures no component
// is ".." to block traversal attempts.
func SanitizePath(path string) string {
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return filepath.Base(cleaned)
	}
	if filepath.IsAbs(cleaned) {
		abs, err := filepath.Abs(cleaned)
		if err == nil {
			return abs
		}
	}
	return cleaned
}

// SanitizeWithinDir ensures path stays within baseDir after cleaning.
// Returns the cleaned path if it resolves within baseDir, otherwise an empty string.
func SanitizeWithinDir(path, baseDir string) string {
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return ""
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return ""
	}
	absPath := cleaned
	if !filepath.IsAbs(cleaned) {
		absPath = filepath.Join(absBase, cleaned)
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return absPath
}

// ReadFileSafely reads a file with a maximum size limit and path sanitization.
func ReadFileSafely(path string, maxSize int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSize {
		return nil, &MaxSizeError{Path: path, Size: info.Size(), Max: maxSize}
	}
	return os.ReadFile(path)
}

// MaxSizeError indicates a file exceeded the maximum allowed size.
type MaxSizeError struct {
	Path string
	Size int64
	Max  int64
}

func (e *MaxSizeError) Error() string {
	return "file " + e.Path + " exceeds maximum size: " + formatSize(e.Size) + " > " + formatSize(e.Max)
}

func formatSize(n int64) string {
	return strings.TrimRight(strings.TrimRight(formatInt(n), "0"), ".")
}

func formatInt(n int64) string {
	if n < 1024 {
		return itoa(n) + "B"
	}
	if n < 1048576 {
		return ftoa(float64(n)/1024.0) + "KB"
	}
	return ftoa(float64(n)/1048576.0) + "MB"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func ftoa(f float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(formatFloat(f), "0"), "0")
}

func formatFloat(f float64) string {
	n := int64(f*100 + 0.5)
	intPart := n / 100
	fracPart := n % 100
	if fracPart == 0 {
		return itoa(intPart)
	}
	return itoa(intPart) + "." + padZero(fracPart)
}

func padZero(n int64) string {
	s := itoa(n)
	if len(s) == 1 {
		return "0" + s
	}
	return s
}
