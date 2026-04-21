// ninja/go.go - Go build rules for minibp
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"path/filepath"
	"sort"
	"strings"
)

// goLibrary implements a Go library rule.
type goLibrary struct{}

func (r *goLibrary) Name() string { return "go_library" }

func (r *goLibrary) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_build_archive
 command = env ${GOOS_GOARCH} go build -buildmode=archive -o $out $in

`
}

func (r *goLibrary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	suffix := goVariantSuffix(m, ctx)
	return []string{fmt.Sprintf("%s%s.a", name, suffix)}
}

func (r *goLibrary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	variants := getGoTargetVariants(m)
	if len(variants) == 0 {
		return r.ninjaEdgeForVariant(m, ctx, "", "")
	}

	var edges strings.Builder
	sorted := make([]string, len(variants))
	copy(sorted, variants)
	sort.Strings(sorted)
	for _, v := range sorted {
		goos := getGoTargetProp(m, v, "goos")
		goarch := getGoTargetProp(m, v, "goarch")
		edges.WriteString(r.ninjaEdgeForVariant(m, ctx, goos, goarch))
	}
	return edges.String()
}

func (r *goLibrary) ninjaEdgeForVariant(m *parser.Module, ctx RuleRenderContext, goos, goarch string) string {
	name := getName(m)
	srcs := getSrcs(m)
	goflags := getGoflags(m)
	ldflags := getLdflags(m)

	suffix := ""
	envVar := ""
	if goos != "" || goarch != "" {
		parts := []string{}
		if goos != "" {
			parts = append(parts, "GOOS="+goos)
		}
		if goarch != "" {
			parts = append(parts, "GOARCH="+goarch)
		}
		envVar = strings.Join(parts, " ")
		suffix = "_" + goos + "_" + goarch
	}

	out := fmt.Sprintf("%s%s.a", name, suffix)

	var cmd string
	if ldflags != "" {
		cmd = fmt.Sprintf("go build -buildmode=archive -ldflags \"%s\" -o $out $in", ldflags)
	} else {
		cmd = "go build -buildmode=archive -o $out $in"
	}

	if envVar != "" {
		cmd = envVar + " " + cmd
	}

	return fmt.Sprintf("build %s: go_build_archive %s\n flags = %s\n cmd = %s\n GOOS_GOARCH = %s\n",
		out, strings.Join(srcs, " "), goflags, cmd, envVar)
}

func (r *goLibrary) Desc(m *parser.Module, srcFile string) string {
	return "go"
}

// goBinary implements a Go binary rule.
type goBinary struct{}

func (r *goBinary) Name() string { return "go_binary" }

func (r *goBinary) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_build
 command = env ${GOOS_GOARCH} go build -o $out $in

`
}

func (r *goBinary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	suffix := goVariantSuffix(m, ctx)
	return []string{name + suffix}
}

func (r *goBinary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	variants := getGoTargetVariants(m)
	if len(variants) == 0 {
		return r.ninjaEdgeForVariant(m, ctx, "", "")
	}

	var edges strings.Builder
	sorted := make([]string, len(variants))
	copy(sorted, variants)
	sort.Strings(sorted)
	for _, v := range sorted {
		goos := getGoTargetProp(m, v, "goos")
		goarch := getGoTargetProp(m, v, "goarch")
		edges.WriteString(r.ninjaEdgeForVariant(m, ctx, goos, goarch))
	}
	return edges.String()
}

func (r *goBinary) ninjaEdgeForVariant(m *parser.Module, ctx RuleRenderContext, goos, goarch string) string {
	name := getName(m)
	srcs := getSrcs(m)
	deps := GetListProp(m, "deps")
	goflags := getGoflags(m)
	ldflags := getLdflags(m)

	suffix := ""
	envVar := ""
	if goos != "" || goarch != "" {
		parts := []string{}
		if goos != "" {
			parts = append(parts, "GOOS="+goos)
		}
		if goarch != "" {
			parts = append(parts, "GOARCH="+goarch)
		}
		envVar = strings.Join(parts, " ")
		suffix = "_" + goos + "_" + goarch
	}

	out := name + suffix

	var libFiles []string
	for _, dep := range deps {
		depName := strings.TrimPrefix(dep, ":")
		libFiles = append(libFiles, depName+".a")
	}

	srcStr := strings.Join(srcs, " ")

	var cmd string
	if ldflags != "" {
		cmd = fmt.Sprintf("go build -ldflags \"%s\" -o $out $in", ldflags)
	} else {
		cmd = "go build -o $out $in"
	}

	if envVar != "" {
		cmd = envVar + " " + cmd
	}

	if len(libFiles) > 0 {
		libStr := strings.Join(libFiles, " ")
		return fmt.Sprintf("build %s: go_build %s | %s\n flags = %s\n cmd = %s\n GOOS_GOARCH = %s\n",
			out, srcStr, libStr, goflags, cmd, envVar)
	}

	return fmt.Sprintf("build %s: go_build %s\n flags = %s\n cmd = %s\n GOOS_GOARCH = %s\n",
		out, srcStr, goflags, cmd, envVar)
}

func (r *goBinary) Desc(m *parser.Module, srcFile string) string {
	return "go"
}

// goTest implements a Go test rule.
type goTest struct{}

func (r *goTest) Name() string { return "go_test" }

func (r *goTest) NinjaRule(ctx RuleRenderContext) string {
	return `rule go_test
 command = env ${GOOS_GOARCH} go test -c -o $out $pkg

`
}

func (r *goTest) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	suffix := goVariantSuffix(m, ctx)
	return []string{fmt.Sprintf("%s%s.test", name, suffix)}
}

func (r *goTest) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	variants := getGoTargetVariants(m)
	if len(variants) == 0 {
		return r.ninjaEdgeForVariant(m, ctx, "", "")
	}

	var edges strings.Builder
	sorted := make([]string, len(variants))
	copy(sorted, variants)
	sort.Strings(sorted)
	for _, v := range sorted {
		goos := getGoTargetProp(m, v, "goos")
		goarch := getGoTargetProp(m, v, "goarch")
		edges.WriteString(r.ninjaEdgeForVariant(m, ctx, goos, goarch))
	}
	return edges.String()
}

func (r *goTest) ninjaEdgeForVariant(m *parser.Module, ctx RuleRenderContext, goos, goarch string) string {
	name := getName(m)
	srcs := getSrcs(m)
	goflags := getGoflags(m)
	ldflags := getLdflags(m)
	pkgPath := "./" + filepath.Dir(srcs[0])

	suffix := ""
	envVar := ""
	if goos != "" || goarch != "" {
		parts := []string{}
		if goos != "" {
			parts = append(parts, "GOOS="+goos)
		}
		if goarch != "" {
			parts = append(parts, "GOARCH="+goarch)
		}
		envVar = strings.Join(parts, " ")
		suffix = "_" + goos + "_" + goarch
	}

	out := fmt.Sprintf("%s%s.test", name, suffix)

	var cmd string
	if ldflags != "" {
		cmd = fmt.Sprintf("go test -ldflags \"%s\" -c -o $out $pkg", ldflags)
	} else {
		cmd = "go test -c -o $out $pkg"
	}

	if envVar != "" {
		cmd = envVar + " " + cmd
	}

	return fmt.Sprintf("build %s: go_test\n pkg = %s\n flags = %s\n cmd = %s\n GOOS_GOARCH = %s\n",
		out, pkgPath, goflags, cmd, envVar)
}

func (r *goTest) Desc(m *parser.Module, srcFile string) string {
	return "go test"
}

// goVariantSuffix returns the output suffix for a Go target variant.
func goVariantSuffix(m *parser.Module, ctx RuleRenderContext) string {
	if ctx.GOOS != "" && ctx.GOARCH != "" {
		return "_" + ctx.GOOS + "_" + ctx.GOARCH
	}
	return ""
}
