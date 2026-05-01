package pathutil

import "testing"

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no traversal",
			input: "foo/bar/baz",
			want:  "foo/bar/baz",
		},
		{
			name:  "single ../",
			input: "foo/../bar",
			want:  "bar", // filepath.Clean resolves foo/../bar to bar
		},
		{
			name:  "multiple ../",
			input: "foo/../bar/../baz",
			want:  "baz", // resolves to baz
		},
		{
			name:  "leading ../",
			input: "../foo",
			want:  "foo", // contains .., return base name only
		},
		{
			name:  "trailing ../",
			input: "foo/..",
			want:  ".", // filepath.Clean resolves to .
		},
		{
			name:  "consecutive ../",
			input: "../../foo",
			want:  "foo", // contains .., return base name
		},
		{
			name:  "only ../",
			input: "../",
			want:  "..", // contains .., return base name
		},
		{
			name:  "only ..\\",
			input: "..\\",
			want:  "..\\", // contains .., return base name
		},
		{
			name:  "mixed slashes",
			input: "..\\../foo/..\\bar",
			want:  "..\\bar", // contains .., return base name
		},
		{
			name:  "complex traversal",
			input: "a/b/../../c/d/../e",
			want:  "c/e", // filepath.Clean resolves correctly
		},
		{
			name:  "already clean",
			input: "a/b/c",
			want:  "a/b/c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizePath(tt.input)
			if got != tt.want {
				t.Errorf("SanitizePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
