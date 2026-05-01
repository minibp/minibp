package incremental

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"minibp/lib/parser"
)

// TestNewManager tests manager creation and directory setup.
func TestNewManager(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected manager to be non-nil")
	}

	// Check that .minibp/json/ was created
	jsonDir := filepath.Join(tmpDir, ".minibp", "json")
	if _, err := os.Stat(jsonDir); os.IsNotExist(err) {
		t.Error("expected .minibp/json/ to be created")
	}
}

// TestNeedsReparse tests the incremental hash checking.
func TestNeedsReparse(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, _ := NewManager(tmpDir)

	bpFile := filepath.Join(tmpDir, "test.bp")
	os.WriteFile(bpFile, []byte("cc_library { name: \"test\" }"), 0644)

	// First check: file is new, should need reparse
	needs, err := mgr.NeedsReparse(bpFile)
	if err != nil {
		t.Fatalf("NeedsReparse failed: %v", err)
	}
	if !needs {
		t.Error("expected new file to need reparse")
	}

	// Second check: file unchanged, should not need reparse
	needs, err = mgr.NeedsReparse(bpFile)
	if err != nil {
		t.Fatalf("NeedsReparse failed: %v", err)
	}
	if needs {
		t.Error("expected unchanged file to not need reparse")
	}

	// Modify file: should need reparse
	os.WriteFile(bpFile, []byte("cc_library { name: \"test2\" }"), 0644)
	// Ensure mtime changes (filesystem may have low precision)
	time.Sleep(1 * time.Second)
	needs, err = mgr.NeedsReparse(bpFile)
	if err != nil {
		t.Fatalf("NeedsReparse failed: %v", err)
	}
	if !needs {
		t.Error("expected modified file to need reparse")
	}
}

// TestSaveAndLoadJSON tests caching parsed AST as JSON.
func TestSaveAndLoadJSON(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, _ := NewManager(tmpDir)

	bpFile := filepath.Join(tmpDir, "test.bp")

	// Create a sample AST
	ast := &parser.File{
		Name: "test.bp",
		Defs: []parser.Definition{
			&parser.Module{
				Type: "cc_library",
				Map: &parser.Map{
					Properties: []*parser.Property{
						{Name: "name", Value: &parser.String{Value: "testlib"}},
					},
				},
			},
		},
	}

	// Save JSON
	if err := mgr.SaveJSON(bpFile, ast); err != nil {
		t.Fatalf("SaveJSON failed: %v", err)
	}

	// Load JSON
	loaded, err := mgr.LoadJSON(bpFile)
	if err != nil {
		t.Fatalf("LoadJSON failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded AST to be non-nil")
	}
	if loaded.Name != "test.bp" {
		t.Errorf("expected name 'test.bp', got '%s'", loaded.Name)
	}
	if len(loaded.Defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(loaded.Defs))
	}

	// Check that the module was correctly serialized/deserialized
	mod, ok := loaded.Defs[0].(*parser.Module)
	if !ok {
		t.Fatal("expected module definition")
	}
	if mod.Type != "cc_library" {
		t.Errorf("expected type 'cc_library', got '%s'", mod.Type)
	}
}

// TestSaveDepFile tests persistence of dependency hashes.
func TestSaveDepFile(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, _ := NewManager(tmpDir)

	bpFile := filepath.Join(tmpDir, "test.bp")
	os.WriteFile(bpFile, []byte("cc_library { name: \"test\" }"), 0644)

	// Trigger a hash calculation
	mgr.NeedsReparse(bpFile)

	// Save dep file
	if err := mgr.SaveDepFile(); err != nil {
		t.Fatalf("SaveDepFile failed: %v", err)
	}

	// Check file exists
	depFile := filepath.Join(tmpDir, ".minibp", "dep.json")
	if _, err := os.Stat(depFile); os.IsNotExist(err) {
		t.Error("expected dep.json to be created")
	}

	// Create new manager and check it loads the hashes
	mgr2, _ := NewManager(tmpDir)
	needs, err := mgr2.NeedsReparse(bpFile)
	if err != nil {
		t.Fatalf("NeedsReparse on new manager failed: %v", err)
	}
	if needs {
		t.Error("expected file to not need reparse after loading dep.json")
	}
}

// TestJsonFilePath tests JSON cache file path generation.
func TestJsonFilePath(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, _ := NewManager(tmpDir)

	// Test simple file
	bpFile := filepath.Join(tmpDir, "test.bp")
	jsonPath := mgr.jsonFilePath(bpFile)
	if !strings.HasSuffix(jsonPath, ".json") {
		t.Errorf("expected .json suffix, got '%s'", jsonPath)
	}
	// Path should be within .minibp/json/
	if !strings.Contains(jsonPath, ".minibp/json/") {
		t.Errorf("expected path in .minibp/json/, got '%s'", jsonPath)
	}

	// Test nested file (should sanitize path separators)
	bpFile2 := filepath.Join(tmpDir, "subdir", "test.bp")
	jsonPath2 := mgr.jsonFilePath(bpFile2)
	// Should contain __ to indicate sanitized separator
	if !strings.Contains(jsonPath2, "__") {
		t.Errorf("expected sanitized path with __, got '%s'", jsonPath2)
	}
}

// TestParseFileIntegration tests the full incremental parsing flow.
func TestParseFileIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, _ := NewManager(tmpDir)

	bpContent := `
cc_library {
    name: "testlib",
    srcs: ["test.c"],
}
`
	bpFile := filepath.Join(tmpDir, "test.bp")
	os.WriteFile(bpFile, []byte(bpContent), 0644)

	// First parse: should need reparse
	needs, _ := mgr.NeedsReparse(bpFile)
	if !needs {
		t.Error("expected first parse to need reparse")
	}

	// Simulate parsing and saving
	ast := &parser.File{
		Name: "test.bp",
		Defs: []parser.Definition{
			&parser.Module{
				Type: "cc_library",
				Map: &parser.Map{
					Properties: []*parser.Property{
						{Name: "name", Value: &parser.String{Value: "testlib"}},
						{Name: "srcs", Value: &parser.List{Values: []parser.Expression{&parser.String{Value: "test.c"}}}},
					},
				},
			},
		},
	}
	mgr.SaveJSON(bpFile, ast)

	// Second check: should not need reparse
	needs, _ = mgr.NeedsReparse(bpFile)
	if needs {
		t.Error("expected unchanged file to not need reparse")
	}

	// Load from cache
	loaded, err := mgr.LoadJSON(bpFile)
	if err != nil {
		t.Fatalf("LoadJSON failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected cached AST")
	}

	// Verify loaded content
	if len(loaded.Defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(loaded.Defs))
	}
}

// TestSanitizeName tests path sanitization.
func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"test.bp", "test.bp"},
		{"subdir/test.bp", "subdir__test.bp"},
		{"/absolute/path/test.bp", "__absolute__path__test.bp"},
	}

	for _, tt := range tests {
		result := sanitizeName(tt.input)
		// Check that no path separators remain
		if strings.Contains(result, "/") || strings.Contains(result, "\\") {
			t.Errorf("sanitizeName(%s) = %s, should not contain path separators", tt.input, result)
		}
	}
}
