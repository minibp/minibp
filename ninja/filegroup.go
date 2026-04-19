// ninja/filegroup.go - File group build rules for minibp
package ninja

import (
	"fmt"
	"minibp/parser"
	"path/filepath"
	"runtime"
	"strings"
)

// filegroup implements a file group rule.
type filegroup struct{}

func (r *filegroup) Name() string { return "filegroup" }

func (r *filegroup) NinjaRule(ctx RuleRenderContext) string {
	copyCmd := "cp $in $out"
	if runtime.GOOS == "windows" {
		copyCmd = "cmd /c copy $in $out"
	}
	return `rule filegroup_copy
 command = ` + copyCmd + `
`
}

func (r *filegroup) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".files"}
}

func (r *filegroup) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return ""
	}
	name := getName(m)
	if name == "" {
		return ""
	}

	// Filegroup just copies files to output directory
	var edges strings.Builder
	for _, src := range srcs {
		out := filepath.Join(name, filepath.Base(src))
		edges.WriteString(fmt.Sprintf("build %s: filegroup_copy %s\n", out, src))
	}
	return edges.String()
}

func (r *filegroup) Desc(m *parser.Module, srcFile string) string {
	return "cp"
}

// filegroupStatic implements a static file group rule.
type filegroupStatic struct{}

func (r *filegroupStatic) Name() string { return "filegroup_static" }

func (r *filegroupStatic) NinjaRule(ctx RuleRenderContext) string {
	return `rule filegroup_static
 command = cp $in $out
`
}

func (r *filegroupStatic) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + ".static"}
}

func (r *filegroupStatic) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return ""
	}
	name := getName(m)
	if name == "" {
		return ""
	}

	var edges strings.Builder
	out := name + ".static"
	edges.WriteString(fmt.Sprintf("build %s: filegroup_static %s\n", out, strings.Join(srcs, " ")))
	return edges.String()
}

func (r *filegroupStatic) Desc(m *parser.Module, srcFile string) string {
	return "cp"
}
