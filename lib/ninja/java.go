// ninja/java.go - Java build rules for minibp
// This file implements the BuildRule interface for Java language modules.
// It provides rules for building Java libraries, binaries, tests, and host variants.
//
// The Java rules support:
//   - java_library: Produces .jar files from Java sources
//   - java_binary: Produces executable .jar files with main class
//   - java_library_static: Produces static .a.jar files
//   - java_library_host: Produces host-specific .jar files
//   - java_binary_host: Produces host-specific executable .jar files
//   - java_test: Produces test .jar files
//   - java_import: Imports pre-built .jar files
//
// Build process: javac compiles .java files to .class files in a staging directory,
// then jar packages the .class files into a .jar archive.
//
// Key design decisions:
//   - Output naming: Uses "{name}.jar", "lib{name}.a.jar", "{name}-host.jar", "{name}-test.jar"
//   - Staging directory: Each module uses "{name}_classes" to isolate .class files
//   - Stamp files: Intermediate .stamp files track successful javac compilation
//   - Host variants: Use "-host" suffix to distinguish from device variants
//   - Executable JARs: Use "jar cfe" to embed main class in manifest
package ninja

import (
	"fmt"
	"minibp/lib/parser"
	"runtime"
	"strings"
)

// javaLibrary implements a Java library build rule.
// Java libraries are built by compiling Java source files and packaging them into .jar archives.
// The build process:
//   - javac compiles .java source files to .class files in a staging directory
//   - jar packages the .class files into a .jar archive
//
// This rule produces standard Java library JARs (e.g., name.jar) used as dependencies
// by other Java modules or binaries.
type javaLibrary struct{}

func (r *javaLibrary) Name() string { return "java_library" }

// NinjaRule defines the ninja compilation and archiving rules.
// Creates two rules:
//   - javac_lib: Compiles Java sources to .class files in outdir
//   - jar_create: Packages .class files from outdir into a .jar archive
func (r *javaLibrary) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_lib

  command = javac -d $outdir $in $flags

rule jar_create

  command = jar cf $out -C $outdir .

`

}

// Outputs returns the output paths for this module.
// Returns nil if the module has no name (invalid module).
// Output format: {name}.jar
func (r *javaLibrary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.jar", name)}
}

// NinjaEdge generates ninja build edges for compiling and packaging Java sources.
// Returns empty string if name is empty or no sources are provided (invalid module).
//
// Build edges generated:
//  1. {name}.stamp: Depends on source files, compiles with javac to staging directory
//  2. {name}.jar: Depends on stamp file, packages .class files with jar
//
// Edge cases:
//   - Empty srcs: Returns "" (no compilation needed)
//   - Missing name: Returns "" (cannot determine output path)
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

// Desc returns a short description of the build action for ninja's progress output.
// Returns "jar" for the final packaging step (srcFile == "").
// Returns "javac" for individual source compilations.
func (r *javaLibrary) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaBinary implements a Java binary (executable) build rule.
// Java binaries are compiled Java programs packaged as executable JARs with a designated main class.
// The build process:
//   - javac compiles .java source files to .class files in a staging directory
//   - jar cfe creates an executable JAR with the main class manifest entry
//
// Unlike javaLibrary, this rule embeds a main_class property in the JAR manifest,
// allowing the JAR to be run directly via "java -jar name.jar".
type javaBinary struct{}

func (r *javaBinary) Name() string {

	return "java_binary"

}

// NinjaRule defines the ninja compilation and executable JAR creation rules.
// Creates two rules:
//   - javac_bin: Compiles Java sources to .class files in outdir
//   - jar_create_executable: Creates executable JAR with main class in manifest
func (r *javaBinary) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_bin

 	 command = javac -d $outdir $in $flags

	rule jar_create_executable

 	 command = jar cfe $out $main_class -C $outdir .

	`

}

// Outputs returns the output paths for this module.
// Returns nil if the module has no name (invalid module).
// Output format: {name}.jar
func (r *javaBinary) Outputs(m *parser.Module, ctx RuleRenderContext) []string {

	name := getName(m)

	if name == "" {

		return nil

	}

	return []string{fmt.Sprintf("%s.jar", name)}

}

// NinjaEdge generates ninja build edges for compiling and packaging Java binaries.
// Returns empty string if name is empty, no sources provided, or main_class is missing.
//
// Build edges generated:
//  1. {name}.stamp: Depends on source files, compiles with javac to staging directory
//  2. {name}.jar: Depends on stamp file, creates executable JAR with main_class manifest
//
// Edge cases:
//   - Empty srcs: Returns "" (no sources to compile)
//   - Missing name: Returns "" (cannot determine output path)
//   - Missing main_class: Returns "" (JAR cannot be executed without main class)
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

// Desc returns a short description of the build action.
// Returns "jar" for the final packaging step.
// Returns "javac" for individual source compilations.
func (r *javaBinary) Desc(m *parser.Module, srcFile string) string {

	if srcFile == "" {

		return "jar"

	}

	return "javac"

}

// javaLibraryStatic implements a static Java library build rule.
// Static libraries are used for linking into larger Java applications or for creating
// precompiled library distributions.
//
// The output naming convention uses the "lib*.a.jar" prefix (e.g., libfoo.a.jar)
// to distinguish static libraries from regular dynamic Java libraries.
// This naming helps build systems identify statically linkable artifacts.
type javaLibraryStatic struct{}

func (r *javaLibraryStatic) Name() string {

	return "java_library_static"

}

// NinjaRule defines the ninja compilation and archiving rules.
// Identical to javaLibrary's rules since the build process is the same.
// Only the output naming differs.
func (r *javaLibraryStatic) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_lib



  command = javac -d $outdir $in $flags



rule jar_create



  command = jar cf $out -C $outdir .



`

}

// Outputs returns the output paths for static libraries.
// Output format: lib{name}.a.jar
// The ".a" prefix indicates static/archive semantics similar to Unix .a archive files.
func (r *javaLibraryStatic) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("lib%s.a.jar", name)}
}

// NinjaEdge generates ninja build edges for static library compilation.
// Build edges generated:
//  1. {name}.stamp: Compiles Java sources
//  2. lib{name}.a.jar: Packages .class files
//
// Note: The stamp file uses the simple name, not the lib*.a.jar name, for consistency
// with the build system convention of tracking compilation with simple names.
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

// Desc returns a short description of the build action.
func (r *javaLibraryStatic) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaLibraryHost implements a Java library build rule for host builds.
// Host-specific libraries are compiled to run on the build host system rather than
// the target device or emulator.
//
// The output naming convention appends "-host" suffix (e.g., name-host.jar) to
// distinguish host artifacts from target/device artifacts. This is essential
// when cross-compiling for Android, where build tools may run on Linux/Mac
// but produce artifacts for Android devices.
type javaLibraryHost struct{}

func (r *javaLibraryHost) Name() string { return "java_library_host" }

// NinjaRule defines the ninja compilation and archiving rules.
// Identical to javaLibrary's rules since the build process is the same.
// Only the output naming differs.
func (r *javaLibraryHost) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_lib

  command = javac -d $outdir $in $flags

rule jar_create

  command = jar cf $out -C $outdir .

`

}

// Outputs returns the output paths for host libraries.
// Output format: {name}-host.jar
// The "-host" suffix identifies this as a host-native artifact.
func (r *javaLibraryHost) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-host.jar", name)}
}

// NinjaEdge generates ninja build edges for host library compilation.
// Build edges generated:
//  1. {name}.stamp: Compiles Java sources
//  2. {name}-host.jar: Packages .class files
//
// Host variants are used for build tools, generators, and utilities that must
// run on the build host during the build process.
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

// Desc returns a short description of the build action.
func (r *javaLibraryHost) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaBinaryHost implements a Java binary build rule for host builds.
// Host-specific binaries are compiled to run on the build host system rather than
// the target device or emulator.
//
// Like javaBinary, this produces executable JARs with a main class manifest,
// but the output uses the "-host" suffix (e.g., name-host.jar) to identify
// it as a host-native artifact. Used for build tools, generators, and other
// utilities that must run during the host-side build process.
type javaBinaryHost struct{}

func (r *javaBinaryHost) Name() string { return "java_binary_host" }

// NinjaRule defines the ninja compilation and executable JAR creation rules.
// Identical to javaBinary's rules since the build process is the same.
// Only the output naming differs.
func (r *javaBinaryHost) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_bin

  command = javac -d $outdir $in $flags

rule jar_create_executable

  command = jar cfe $out $main_class -C $outdir .

`

}

// Outputs returns the output paths for host binaries.
// Output format: {name}-host.jar
// The "-host" suffix identifies this as a host-native executable artifact.
func (r *javaBinaryHost) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-host.jar", name)}
}

// NinjaEdge generates ninja build edges for host binary compilation.
// Build edges generated:
//  1. {name}.stamp: Compiles Java sources
//  2. {name}-host.jar: Creates executable JAR with main_class manifest
//
// Edge cases:
//   - Missing main_class: Returns "" (host binaries still need a main class)
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

// Desc returns a short description of the build action.
func (r *javaBinaryHost) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaTest implements a Java test build rule.
// Java tests are compiled test classes packaged as test JARs with test option support.
//
// The output naming convention uses the "-test" suffix (e.g., name-test.jar) to
// identify test artifacts. Supports test-specific flags and arguments via the
// test_options and test_config properties. Test JARs are typically executed
// by test runners like JUnit or Android's test framework.
type javaTest struct{}

func (r *javaTest) Name() string { return "java_test" }

// NinjaRule defines the ninja compilation and test JAR creation rules.
// Creates two rules:
//   - javac_test: Compiles test sources with test-specific flags
//   - jar_test: Packages test .class files into a test JAR
func (r *javaTest) NinjaRule(ctx RuleRenderContext) string {

	return `rule javac_test

  command = javac -d $outdir $in $flags

rule jar_test

  command = jar cf $out -C $outdir .

`

}

// Outputs returns the output paths for test modules.
// Output format: {name}-test.jar
// The "-test" suffix identifies this as a test artifact.
func (r *javaTest) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s-test.jar", name)}
}

// NinjaEdge generates ninja build edges for test compilation.
// Build edges generated:
//  1. {name}.stamp: Compiles test sources
//  2. {name}-test.jar: Packages test .class files
//  3. Optional test_args: Additional arguments for test execution
//
// The test_args variable is passed to the test runner during execution.
func (r *javaTest) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}

	javaflags := getJavaflags(m)
	out := r.Outputs(m, ctx)[0]
	outdir := name + "_classes"
	testArgs := getTestOptionArgs(m)

	var edges strings.Builder
	edges.WriteString(fmt.Sprintf("build %s.stamp: javac_test %s\n outdir = %s\n flags = %s\n", name, strings.Join(srcs, " "), outdir, javaflags))
	edges.WriteString(fmt.Sprintf("build %s: jar_test %s.stamp\n outdir = %s\n", out, name, outdir))
	if testArgs != "" {
		edges.WriteString(fmt.Sprintf(" test_args = %s\n", testArgs))
	}
	return edges.String()
}

// Desc returns a short description of the build action.
func (r *javaTest) Desc(m *parser.Module, srcFile string) string {
	if srcFile == "" {
		return "jar"
	}
	return "javac"
}

// javaImport implements a prebuilt JAR import rule.
// This rule copies pre-built .jar files into the build tree without recompilation.
// It is used for importing external JAR dependencies or precompiled libraries
// that should not be rebuilt from source.
//
// The rule copies source JAR files directly to the output location, supporting
// cross-platform builds by using "cp" on Unix or "cmd /c copy" on Windows.
// This allows prebuilt binaries to be integrated into the dependency graph.
type javaImport struct{}

func (r *javaImport) Name() string { return "java_import" }

// NinjaRule defines the ninja copy rule.
// Selects the appropriate copy command based on OS:
//   - Unix/Linux/Mac: Uses "cp $in $out"
//   - Windows: Uses "cmd /c copy $in $out"
func (r *javaImport) NinjaRule(ctx RuleRenderContext) string {

	copyCmd := "cp $in $out"

	if runtime.GOOS == "windows" {

		copyCmd = "cmd /c copy $in $out"

	}

	return `rule java_import

  command = ` + copyCmd + `

`

}

// Outputs returns the output paths for imported JARs.
// Output format: {name}.jar
// The imported JAR maintains its original name in the build tree.
func (r *javaImport) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{fmt.Sprintf("%s.jar", name)}
}

// NinjaEdge generates ninja build edges for importing prebuilt JARs.
// Returns empty string if no sources are provided (nothing to import).
//
// Build edges generated:
//
//	{name}.jar: Depends on source JAR(s), copies to output location
//
// Edge cases:
//   - Empty srcs: Returns "" (no files to import)
func (r *javaImport) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	srcs := getSrcs(m)
	if len(srcs) == 0 {
		return ""
	}

	out := r.Outputs(m, ctx)[0]
	return fmt.Sprintf("build %s: java_import %s\n", out, strings.Join(srcs, " "))
}

// Desc returns a short description of the build action.
func (r *javaImport) Desc(m *parser.Module, srcFile string) string {
	return "cp"
}
