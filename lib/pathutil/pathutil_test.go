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
			want:  "foo/bar",
		},
		{
			name:  "multiple ../",
			input: "foo/../bar/../baz",
			want:  "foo/bar/baz",
		},
		{
			name:  "leading ../",
			input: "../foo",
			want:  "foo",
		},
		{
			name:  "trailing ../",
			input: "foo/..",
			want:  "foo/..",
		},
		{
			name:  "consecutive ../",
			input: "../../foo",
			want:  "foo",
		},
		{
			name:  "only ../",
			input: "../",
			want:  "",
		},
		{
			name:  "only ..\\",
			input: "..\\",
			want:  "",
		},
		{
			name:  "mixed slashes",
			input: "..\\../foo/..\\bar",
			want:  "foo/bar",
		},
		{
			name:  "complex traversal",
			input: "a/b/../../c/d/../e",
			want:  "a/b/c/d/e",
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
