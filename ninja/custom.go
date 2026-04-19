// ninja/custom.go - Custom and proto rules for minibp
package ninja

import (
	"fmt"
	"minibp/parser"
	"strings"
)

// customRule implements a custom build rule.
type customRule struct{}

func (r *customRule) Name() string { return "custom_rule" }

func (r *customRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule custom_command
  command = $command $in $flags
  description = Custom build $out
`
}

func (r *customRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	// Custom rules can have explicit outputs (outs property)
	outputs := GetListProp(m, "outs")
	if len(outputs) > 0 {
		return outputs
	}
	return []string{name + ".out"}
}

func (r *customRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {

	name := getName(m)

	srcs := getSrcs(m)

	command := GetStringProp(m, "cmd")

	if name == "" || command == "" {

		return ""

	}

	flags := GetStringProp(m, "flags")

	outs := r.Outputs(m, ctx)

	if len(outs) == 0 {

		return ""

	}

	out := outs[0]

	

	// Replace $out and $in with actual paths in command

	inStr := strings.Join(srcs, " ")

	actualCommand := strings.ReplaceAll(command, "$out", out)

	actualCommand = strings.ReplaceAll(actualCommand, "$in", inStr)

	

	// Escape paths for ninja build file

	// Spaces become $ ", $ " becomes $ ", etc.

	escapedOut := ninjaEscapePath(out)

	escapedSrcs := make([]string, len(srcs))

	for i, src := range srcs {

		escapedSrcs[i] = ninjaEscapePath(src)

	}

	

	var edges strings.Builder

	edges.WriteString(fmt.Sprintf("build %s: custom_command %s\n", escapedOut, strings.Join(escapedSrcs, " ")))

	edges.WriteString(fmt.Sprintf("  cmd = %s\n", actualCommand))

	if flags != "" {

		edges.WriteString(fmt.Sprintf("  flags = %s\n", flags))

	}

	edges.WriteString("\n")

	return edges.String()

}

func (r *customRule) Desc(m *parser.Module, srcFile string) string {
	return "custom"
}

// protoLibraryRule implements a proto library rule.
type protoLibraryRule struct{}

func (r *protoLibraryRule) Name() string { return "proto_library" }

func (r *protoLibraryRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule proto_compile
  command = protoc --cpp_out=$out $in
  description = Compiling proto $in
`
}

func (r *protoLibraryRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return nil
	}
	// Get the base name from the first source file
	src := srcs[0]
	baseName := strings.TrimSuffix(src, ".proto")

	// Check for out property to determine output type
	outType := GetStringProp(m, "out")
	switch outType {
	case "go":
		return []string{baseName + ".pb.go"}
	case "java":
		return []string{baseName + ".java"}
	case "py":
		return []string{baseName + "_pb2.py"}
	default:
		// Default: C++ outputs
		return []string{baseName + ".pb.h", baseName + ".pb.cc"}
	}
}

func (r *protoLibraryRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	// Get plugins and proto_paths
	plugins := GetListProp(m, "plugins")
	protoPaths := GetListProp(m, "proto_paths")
	src := srcs[0]
	baseName := strings.TrimSuffix(src, ".proto")
	var edges strings.Builder

	// Determine output type and generate appropriate command
	outType := GetStringProp(m, "out")

	// Build protoc command line
	protocCmd := "protoc"
	if len(plugins) > 0 {
		for _, plugin := range plugins {
			protocCmd += fmt.Sprintf(" --plugin=%s", plugin)
		}
	}
	edges.WriteString(fmt.Sprintf("  protoc = %s\n", protocCmd))

	// Add proto_path variable if present
	if len(protoPaths) > 0 {
		edges.WriteString(fmt.Sprintf("  proto_path = --proto_path=%s", protoPaths[0]))
		for i := 1; i < len(protoPaths); i++ {
			edges.WriteString(fmt.Sprintf(" --proto_path=%s", protoPaths[i]))
		}
		edges.WriteString("\n")
	}

	switch outType {
	case "go":
		edges.WriteString(fmt.Sprintf("build %s.pb.go: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = go\n")
	case "java":
		edges.WriteString(fmt.Sprintf("build %s.java: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = java\n")
	case "py":
		edges.WriteString(fmt.Sprintf("build %s_pb2.py: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = py\n")
	default:
		// C++ output
		edges.WriteString(fmt.Sprintf("build %s.pb.cc: proto_compile %s\n", baseName, src))
		edges.WriteString(fmt.Sprintf("build %s.pb.h: proto_compile %s\n", baseName, src))
		edges.WriteString("  out_type = cc\n")
	}
	return edges.String()
}

func (r *protoLibraryRule) Desc(m *parser.Module, srcFile string) string {
	return "protoc"
}

// protoGenRule implements a proto generation rule.
type protoGenRule struct{}

func (r *protoGenRule) Name() string { return "proto_gen" }

func (r *protoGenRule) NinjaRule(ctx RuleRenderContext) string {
	return `rule proto_gen
  command = protoc --cpp_out=$out $in
  description = Generating proto files $in
`
}

func (r *protoGenRule) Outputs(m *parser.Module, ctx RuleRenderContext) []string {
	name := getName(m)
	if name == "" {
		return nil
	}
	return []string{name + "_proto"}
}

func (r *protoGenRule) NinjaEdge(m *parser.Module, ctx RuleRenderContext) string {
	name := getName(m)
	srcs := getSrcs(m)
	if name == "" || len(srcs) == 0 {
		return ""
	}
	var edges strings.Builder
	out := r.Outputs(m, ctx)[0]
	edges.WriteString(fmt.Sprintf("build %s: proto_gen %s\n", out, strings.Join(srcs, " ")))
	return edges.String()
}

func (r *protoGenRule) Desc(m *parser.Module, srcFile string) string {
	return "protoc"
}