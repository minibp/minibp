package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"minibp/ninja"
	"minibp/parser"
)

var (
	openInputFile      = func(path string) (io.ReadCloser, error) { return os.Open(path) }
	createOutputFile   = func(path string) (io.WriteCloser, error) { return os.Create(path) }
	parseBlueprintFile = parser.ParseFile
)

type Graph struct {
	nodes map[string]*parser.Module
	edges map[string][]string
}

func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]*parser.Module),
		edges: make(map[string][]string),
	}
}

func (g *Graph) AddNode(name string, mod *parser.Module) {
	g.nodes[name] = mod
	if _, ok := g.edges[name]; !ok {
		g.edges[name] = []string{}
	}
}

func (g *Graph) AddEdge(from, to string) {
	if _, ok := g.edges[from]; !ok {
		g.edges[from] = []string{}
	}
	if _, ok := g.edges[to]; !ok {
		g.edges[to] = []string{}
	}
	g.edges[from] = append(g.edges[from], to)
}

func (g *Graph) TopoSort() ([][]string, error) {
	inDegree := make(map[string]int)
	for name := range g.nodes {
		inDegree[name] = 0
	}

	for from, deps := range g.edges {
		if _, ok := g.nodes[from]; !ok {
			return nil, fmt.Errorf("module '%s' referenced in dependency graph does not exist", from)
		}
		for _, to := range deps {
			if _, ok := g.nodes[to]; !ok {
				return nil, fmt.Errorf("dependency '%s' of '%s' not found", to, from)
			}
			inDegree[from]++
		}
	}

	reverseEdges := make(map[string][]string)
	for from, deps := range g.edges {
		for _, to := range deps {
			reverseEdges[to] = append(reverseEdges[to], from)
		}
	}

	var levels [][]string
	visited := make(map[string]bool)
	nodeCount := len(g.nodes)

	for len(visited) < nodeCount {
		var currentLevel []string
		for name, degree := range inDegree {
			if degree == 0 && !visited[name] {
				currentLevel = append(currentLevel, name)
			}
		}

		if len(currentLevel) == 0 {
			return nil, fmt.Errorf("circular dependency detected")
		}

		sort.Strings(currentLevel)

		levels = append(levels, currentLevel)
		for _, name := range currentLevel {
			visited[name] = true
			for _, dependent := range reverseEdges[name] {
				inDegree[dependent]--
			}
		}
	}

	return levels, nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var (
		fs       = flag.NewFlagSet("minibp", flag.ContinueOnError)
		outFile  = fs.String("o", "build.ninja", "output ninja file")
		all      = fs.Bool("a", false, "parse all .bp files in directory")
		ccFlag   = fs.String("cc", "", "C compiler (default: gcc)")
		cxxFlag  = fs.String("cxx", "", "C++ compiler (default: g++)")
		arFlag   = fs.String("ar", "", "archiver (default: ar)")
		archFlag = fs.String("arch", "", "target architecture (arm, arm64, x86, x86_64)")
		hostFlag = fs.Bool("host", false, "build for host (overrides arch)")
		osFlag   = fs.String("os", "", "target OS (linux, darwin, windows)")
	)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(fs.Args()) < 1 && !*all {
		fmt.Fprintln(stderr, "Usage: minibp [-o output] [-a] [-cc CC] [-cxx CXX] [-ar AR] [-arch ARCH] [-host] [-os OS] <file.bp | directory>")
		return fmt.Errorf("missing input path")
	}

	srcDir := "."
	if *all && len(fs.Args()) > 0 {
		srcDir = fs.Args()[0]
	} else if len(fs.Args()) > 0 {
		srcDir = filepath.Dir(fs.Args()[0])
	}

	var files []string
	if *all {
		bpFiles, err := filepath.Glob(filepath.Join(srcDir, "*.bp"))
		if err != nil {
			return fmt.Errorf("error globbing bp files: %w", err)
		}
		files = bpFiles
	} else {
		files = fs.Args()
	}

	eval := parser.NewEvaluator()
	eval.SetConfig("arch", *archFlag)
	eval.SetConfig("host", fmt.Sprintf("%v", *hostFlag))
	if *osFlag != "" {
		eval.SetConfig("os", *osFlag)
	} else {
		eval.SetConfig("os", "linux")
	}
	eval.SetConfig("target", *archFlag)

	allDefs, err := parseDefinitionsFromFiles(files)
	if err != nil {
		return err
	}

	eval.ProcessAssignmentsFromDefs(allDefs)

	modules := make(map[string]*parser.Module)
	for _, def := range allDefs {
		mod, ok := def.(*parser.Module)
		if !ok {
			continue
		}
		name := getStringPropEval(mod, "name", eval)
		if name == "" {
			continue
		}

		eval.EvalModule(mod)
		mergeVariantProps(mod, *archFlag, *hostFlag, eval)
		if err := expandGlobsInModule(mod, srcDir); err != nil {
			return fmt.Errorf("error expanding globs for module %s: %w", name, err)
		}
		modules[name] = mod
	}

	graph := NewGraph()
	for name, mod := range modules {
		graph.AddNode(name, mod)
	}

	for name, mod := range modules {
		deps := getListPropEval(mod, "deps", eval)
		for _, dep := range deps {
			depName := strings.TrimPrefix(dep, ":")
			graph.AddEdge(name, depName)
		}
		sharedLibs := getListPropEval(mod, "shared_libs", eval)
		for _, dep := range sharedLibs {
			depName := strings.TrimPrefix(dep, ":")
			graph.AddEdge(name, depName)
		}
		headerLibs := getListPropEval(mod, "header_libs", eval)
		for _, dep := range headerLibs {
			depName := strings.TrimPrefix(dep, ":")
			graph.AddEdge(name, depName)
		}
	}

	rules := ninja.GetAllRules()
	ruleMap := make(map[string]ninja.BuildRule)
	for _, r := range rules {
		ruleMap[r.Name()] = r
	}

	absOutFile, _ := filepath.Abs(*outFile)
	absBuildDir := filepath.Dir(absOutFile)
	absSourceDir, _ := filepath.Abs(srcDir)

	prefix := ""
	if absBuildDir != absSourceDir {
		relPath, err := filepath.Rel(absBuildDir, absSourceDir)
		if err == nil && relPath != "." {
			prefix = relPath + "/"
		}
	}

	outDir := filepath.Dir(absOutFile)

	gen := ninja.NewGenerator(graph, ruleMap, modules)
	gen.SetSourceDir(srcDir)
	gen.SetOutputDir(outDir)
	gen.SetPathPrefix(prefix)

	regenCmd := os.Args[0] + " -o " + *outFile
	for _, f := range files {
		regenCmd += " " + f
	}
	gen.SetRegen(regenCmd, files, *outFile)
	gen.SetWorkDir(srcDir)

	tc := ninja.DefaultToolchain()
	if *ccFlag != "" {
		tc.CC = *ccFlag
	}
	if *cxxFlag != "" {
		tc.CXX = *cxxFlag
	}
	if *arFlag != "" {
		tc.AR = *arFlag
	}
	gen.SetToolchain(tc)
	gen.SetArch(*archFlag)

	if err := generateNinjaFile(*outFile, gen); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Generated %s with %d modules\n", *outFile, len(modules))
	return nil
}

func parseDefinitionsFromFiles(files []string) ([]parser.Definition, error) {
	var allDefs []parser.Definition
	for _, file := range files {
		f, err := openInputFile(file)
		if err != nil {
			return nil, fmt.Errorf("error opening %s: %w", file, err)
		}

		parsedFile, parseErr := parseBlueprintFile(f, file)
		closeErr := f.Close()
		if parseErr != nil {
			return nil, fmt.Errorf("parse error: %w", parseErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("error closing %s: %w", file, closeErr)
		}

		allDefs = append(allDefs, parsedFile.Defs...)
	}

	return allDefs, nil
}

func generateNinjaFile(path string, gen interface{ Generate(io.Writer) error }) error {
	out, err := createOutputFile(path)
	if err != nil {
		return fmt.Errorf("error creating output: %w", err)
	}

	genErr := gen.Generate(out)
	closeErr := out.Close()
	if genErr != nil {
		return fmt.Errorf("error generating ninja: %w", genErr)
	}
	if closeErr != nil {
		return fmt.Errorf("error closing output: %w", closeErr)
	}

	return nil
}

func getStringProp(m *parser.Module, name string) string {
	return ninja.GetStringProp(m, name)
}

func getStringPropEval(m *parser.Module, name string, eval *parser.Evaluator) string {
	if m.Map == nil {
		return ""
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if s, ok := prop.Value.(*parser.String); ok {
				return s.Value
			}
			if eval != nil {
				val := eval.Eval(prop.Value)
				if s, ok := val.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

func getListProp(m *parser.Module, name string) []string {
	return ninja.GetListProp(m, name)
}

func getListPropEval(m *parser.Module, name string, eval *parser.Evaluator) []string {
	if m.Map == nil {
		return nil
	}
	for _, prop := range m.Map.Properties {
		if prop.Name == name {
			if l, ok := prop.Value.(*parser.List); ok {
				return parser.EvalToStringList(l, eval)
			}
		}
	}
	return nil
}

func mergeVariantProps(m *parser.Module, arch string, host bool, eval *parser.Evaluator) {
	if arch != "" && m.Arch != nil {
		mergeMapProps(m, m.Arch[arch])
	}
	if host && m.Host != nil {
		mergeMapProps(m, m.Host)
	}
	if !host && m.Target != nil {
		mergeMapProps(m, m.Target)
	}
}

func mergeMapProps(m *parser.Module, override *parser.Map) {
	if override == nil {
		return
	}
	for _, prop := range override.Properties {
		switch prop.Value.(type) {
		case *parser.List:
			merged := false
			for _, baseProp := range m.Map.Properties {
				if baseProp.Name == prop.Name {
					if baseList, ok := baseProp.Value.(*parser.List); ok {
						if archList, ok := prop.Value.(*parser.List); ok {
							baseList.Values = append(baseList.Values, archList.Values...)
						}
					}
					merged = true
					break
				}
			}
			if !merged {
				m.Map.Properties = append(m.Map.Properties, prop)
			}
		default:
			found := false
			for i, baseProp := range m.Map.Properties {
				if baseProp.Name == prop.Name {
					m.Map.Properties[i] = prop
					found = true
					break
				}
			}
			if !found {
				m.Map.Properties = append(m.Map.Properties, prop)
			}
		}
	}
}

func expandGlobsInModule(m *parser.Module, baseDir string) error {
	if m.Map == nil {
		return nil
	}

	for _, prop := range m.Map.Properties {
		if prop.Name == "srcs" {
			if l, ok := prop.Value.(*parser.List); ok {
				var expandedSrcs []parser.Expression
				seen := make(map[string]bool)

				for _, v := range l.Values {
					if s, ok := v.(*parser.String); ok {
						pattern := s.Value
						if strings.Contains(pattern, "*") {
							matches, err := expandGlob(pattern, baseDir)
							if err != nil {
								return err
							}
							for _, match := range matches {
								if !seen[match] {
									seen[match] = true
									expandedSrcs = append(expandedSrcs, &parser.String{Value: match})
								}
							}
						} else {
							if !seen[pattern] {
								seen[pattern] = true
								expandedSrcs = append(expandedSrcs, v)
							}
						}
					}
				}

				l.Values = expandedSrcs
			}
		}
	}

	return nil
}

func expandGlob(pattern, baseDir string) ([]string, error) {
	var result []string

	if strings.Contains(pattern, "**") {
		walkDir := recursiveGlobRoot(pattern, baseDir)

		err := filepath.Walk(walkDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			relPath, err := filepath.Rel(baseDir, path)
			if err != nil {
				return err
			}
			relPath = filepath.ToSlash(relPath)
			if matchRecursivePattern(filepath.ToSlash(pattern), relPath) {
				result = append(result, relPath)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		fullPattern := filepath.Join(baseDir, pattern)
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			relPath, err := filepath.Rel(baseDir, match)
			if err != nil {
				return nil, err
			}
			result = append(result, relPath)
		}
	}

	return result, nil
}

func recursiveGlobRoot(pattern, baseDir string) string {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	prefix := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "**" || strings.ContainsAny(part, "*?[") {
			break
		}
		prefix = append(prefix, part)
	}
	if len(prefix) == 0 {
		return baseDir
	}
	root := filepath.Join(append([]string{baseDir}, prefix...)...)
	return root
}

func matchRecursivePattern(pattern, path string) bool {
	patternParts := splitGlobParts(pattern)
	pathParts := splitGlobParts(path)
	return matchRecursiveParts(patternParts, pathParts)
}

func splitGlobParts(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func matchRecursiveParts(patternParts, pathParts []string) bool {
	if len(patternParts) == 0 {
		return len(pathParts) == 0
	}

	if patternParts[0] == "**" {
		if matchRecursiveParts(patternParts[1:], pathParts) {
			return true
		}
		if len(pathParts) == 0 {
			return false
		}
		return matchRecursiveParts(patternParts, pathParts[1:])
	}

	if len(pathParts) == 0 {
		return false
	}

	ok, err := filepath.Match(patternParts[0], pathParts[0])
	if err != nil || !ok {
		return false
	}

	return matchRecursiveParts(patternParts[1:], pathParts[1:])
}
