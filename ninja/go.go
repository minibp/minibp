// ninja/go.go - Go build rules for minibp
package ninja

import (
	"fmt"
	"minibp/parser"
	"path/filepath"
	"strings"
)

// goLibrary implements a Go library rule.
type goLibrary struct{}

func (r *goLibrary) Name() string { return "go_library" }

func (r *goLibrary) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_build_archive
 command = go build -buildmode=archive -o $out $in
`
}

func (r *goLibrary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.a", name)}
}

func (r *goLibrary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {

	name := getName(m)

	srcs := getSrcs(m)

	if name == "" || len(srcs) == 0 {

		return ""

	}

	goflags := getGoflags(m)

	ldflags := getLdflags(m)

	out := r.Outputs(m, ctx)[0]



	// Build command with ldflags if present

	var cmd string

	if ldflags != "" {

		cmd = fmt.Sprintf("go build -buildmode=archive -ldflags \"%s\" -o $out $in", ldflags)

	} else {

		cmd = "go build -buildmode=archive -o $out $in"

	}



	return fmt.Sprintf("build %s: go_build_archive %s\n flags = %s\n cmd = %s\n", out, strings.Join(srcs, " "), goflags, cmd)

}

func (r *goLibrary) Desc(m *parser.Module, srcFile string) string {
	return "go"
}

// goBinary implements a Go binary rule.
type goBinary struct{}

func (r *goBinary) Name() string { return "go_binary" }

func (r *goBinary) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_build
 command = go build -o $out $in
`
}

func (r *goBinary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name}
}

func (r *goBinary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {

	name := getName(m)

	srcs := getSrcs(m)

	deps := GetListProp(m, "deps")

	if name == "" || len(srcs) == 0 {

		return ""

	}

	goflags := getGoflags(m)

	ldflags := getLdflags(m)

	out := r.Outputs(m, ctx)[0]

	var libFiles []string

	for _, dep := range deps {

		depName := strings.TrimPrefix(dep, ":")

		libFiles = append(libFiles, depName+".a")

	}

	srcStr := strings.Join(srcs, " ")



	// Build command with ldflags if present

	var cmd string

	if ldflags != "" {

		cmd = fmt.Sprintf("go build -ldflags \"%s\" -o $out $in", ldflags)

	} else {

		cmd = "go build -o $out $in"

	}



	if len(libFiles) > 0 {

		libStr := strings.Join(libFiles, " ")

		return fmt.Sprintf("build %s: go_build %s | %s\n flags = %s\n cmd = %s\n", out, srcStr, libStr, goflags, cmd)

	}

	return fmt.Sprintf("build %s: go_build %s\n flags = %s\n cmd = %s\n", out, srcStr, goflags, cmd)

}

func (r *goBinary) Desc(m *parser.Module, srcFile string) string {
	return "go"
}

// goTest implements a Go test rule.
type goTest struct{}

func (r *goTest) Name() string { return "go_test" }

func (r *goTest) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_test
 command = go test -c -o $out $pkg
`
}

func (r *goTest) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.test", name)}
}

func (r *goTest) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {

	name := getName(m)

	srcs := getSrcs(m)

	if name == "" || len(srcs) == 0 {

		return ""

	}

	goflags := getGoflags(m)

	ldflags := getLdflags(m)

	out := r.Outputs(m, ctx)[0]

	// Extract package path from first source file

	pkgPath := "./" + filepath.Dir(srcs[0])



	// Build command with ldflags if present

	var cmd string

	if ldflags != "" {

		cmd = fmt.Sprintf("go test -ldflags \"%s\" -c -o $out $pkg", ldflags)

	} else {

		cmd = "go test -c -o $out $pkg"

	}



	return fmt.Sprintf("build %s: go_test\n pkg = %s\n flags = %s\n cmd = %s\n", out, pkgPath, goflags, cmd)

}

func (r *goTest) Desc(m *parser.Module, srcFile string) string {
	return "go test"
}
