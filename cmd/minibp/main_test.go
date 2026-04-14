package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestExpandGlobRecursiveExtension(t *testing.T) {
	baseDir := t.TempDir()
	writeTestFile(t, filepath.Join(baseDir, "root.go"))
	writeTestFile(t, filepath.Join(baseDir, "nested", "child.go"))
	writeTestFile(t, filepath.Join(baseDir, "nested", "child.txt"))

	got := expandGlob("**/*.go", baseDir)
	sort.Strings(got)

	want := []string{"nested/child.go", "root.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandGlob returned %v, want %v", got, want)
	}
}

func TestExpandGlobRecursiveUnderPrefix(t *testing.T) {
	baseDir := t.TempDir()
	writeTestFile(t, filepath.Join(baseDir, "src", "root.go"))
	writeTestFile(t, filepath.Join(baseDir, "src", "deep", "child.go"))
	writeTestFile(t, filepath.Join(baseDir, "other", "outside.go"))

	got := expandGlob("src/**/*.go", baseDir)
	sort.Strings(got)

	want := []string{"src/deep/child.go", "src/root.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandGlob returned %v, want %v", got, want)
	}
}

func TestExpandGlobNonRecursive(t *testing.T) {
	baseDir := t.TempDir()
	writeTestFile(t, filepath.Join(baseDir, "root.go"))
	writeTestFile(t, filepath.Join(baseDir, "nested", "child.go"))

	got := expandGlob("*.go", baseDir)
	sort.Strings(got)

	want := []string{"root.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandGlob returned %v, want %v", got, want)
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
