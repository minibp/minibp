// ninja/java.go - Java build rules for minibp
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"runtime"
	"strings"
)

// javaLibrary implements a Java library rule.
type javaLibrary struct{}

func (r *javaLibrary) Name() string { return "java_library" }

func (r *javaLibrary) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_lib

 command = javac -d $outdir $in $flags

rule jar_create

 command = jar cf $out -C $outdir .

`

}

func (r *javaLibrary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.jar", name)}
}

func (r *javaLibrary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	javaflags := getJavaflags(m)
	out := r.Outputs(m, ctx)[0]
	outdir := name + "_classes"

	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_lib %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create %s.stamp\n outdir = %s\n", out, name, outdir))
	return edges.String()
}

func (r *javaLibrary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaBinary implements a Java binary rule.

type javaBinary struct{}

func (r *javaBinary) Name() string {

	return "java_binary"

}

func (r *javaBinary) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_bin

	 command = javac -d $outdir $in $flags

	rule jar_create_executable

	 command = jar cfe $out $main_class -C $outdir .

	`

}

func (r *javaBinary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {

	name := getName(m)

	if name == "" {

		return nil

	}

	return []string{fmt.Sprintf("%s.jar", name)}

}

func (r *javaBinary) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {

	name := getName(m)

	srcs := getSrcs(m)

	mainClass := GetStringProp(m, "main_class")

	if name == "" || len(srcs) == 0 || mainClass == "" {

		return ""

	}

	javaflags := getJavaflags(m)

	out := r.Outputs(m, ctx)[0]

	outdir := name + "_classes"

	var edges strings.Builder

	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_bin %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))

	edges.WriteString(fmt.Sprintf("build %s: jar_create_executable %s.stamp\n outdir = %s\n main_class = %s\n", out, name, outdir, mainClass))

	return edges.String()

}

func (r *javaBinary) Desc(m *parser.Module, srcFile string) string {

	if srcFile == "" {

		return "jar"

	}

	return "javac"

}

// javaLibraryStatic implements a static Java library rule.

type javaLibraryStatic struct{}

func (r *javaLibraryStatic) Name() string {

	return "java_library_static"

}

func (r *javaLibraryStatic) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_lib



 command = javac -d $outdir $in $flags



rule jar_create



 command = jar cf $out -C $outdir .



`

}

func (r *javaLibraryStatic) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s.a.jar", name)}
}

func (r *javaLibraryStatic) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	javaflags := getJavaflags(m)
	out := r.Outputs(m, ctx)[0]
	outdir := name + "_classes"

	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_lib %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create %s.stamp\n outdir = %s\n", out, name, outdir))
	return edges.String()
}

func (r *javaLibraryStatic) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaLibraryHost implements a Java library rule for host builds.
type javaLibraryHost struct{}

func (r *javaLibraryHost) Name() string { return "java_library_host" }

func (r *javaLibraryHost) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_lib

 command = javac -d $outdir $in $flags

rule jar_create

 command = jar cf $out -C $outdir .

`

}

func (r *javaLibraryHost) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-host.jar", name)}
}

func (r *javaLibraryHost) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	javaflags := getJavaflags(m)
	out := r.Outputs(m, ctx)[0]
	outdir := name + "_classes"

	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_lib %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create %s.stamp\n outdir = %s\n", out, name, outdir))
	return edges.String()
}

func (r *javaLibraryHost) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaBinaryHost implements a Java binary rule for host builds.
type javaBinaryHost struct{}

func (r *javaBinaryHost) Name() string { return "java_binary_host" }

func (r *javaBinaryHost) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_bin

 command = javac -d $outdir $in $flags

rule jar_create_executable

 command = jar cfe $out $main_class -C $outdir .

`

}

func (r *javaBinaryHost) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-host.jar", name)}
}

func (r *javaBinaryHost) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	mainClass := GetStringProp(m, "main_class")
	if name == "" || len(srcs) == 0 || mainClass == "" {
		return ""
	}

	javaflags := getJavaflags(m)
	out := r.Outputs(m, ctx)[0]
	outdir := name + "_classes"

	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_bin %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_create_executable %s.stamp\n outdir = %s\n main_class = %s\n", out, name, outdir, mainClass))
	return edges.String()
}

func (r *javaBinaryHost) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaTest implements a Java test rule.
type javaTest struct{}

func (r *javaTest) Name() string { return "java_test" }

func (r *javaTest) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_test

 command = javac -d $outdir $in $flags

rule jar_test

 command = jar cf $out -C $outdir .

`

}

func (r *javaTest) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-test.jar", name)}
}

func (r *javaTest) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	javaflags := getJavaflags(m)
	out := r.Outputs(m, ctx)[0]
	outdir := name + "_classes"

	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_test %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_test %s.stamp\n outdir = %s\n", out, name, outdir))
	return edges.String()
}

func (r *javaTest) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaImport implements a Java import rule.
type javaImport struct{}

func (r *javaImport) Name() string { return "java_import" }

func (r *javaImport) NinjaRule(ctx RuleRenderContext) string {

	copyCmd := "cp $in $out"

	if runtime.GOOS == "windows" {

		copyCmd = "cmd /c copy $in $out"

	}

	return `rule java_import

 command = ` + copyCmd + `

`

}

func (r *javaImport) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.jar", name)}
}

func (r *javaImport) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return ""
	}

	out := r.Outputs(m, ctx)[0]
	return fmt.Sprintf("build %s: java_import %s\n", out, strings.Join(srcs, " "))
}

func (r *javaImport) Desc(m *parser.Module, srcFile string) string {
	return "cp"
}
