package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"minibp/ninja"
	"minibp/parser"
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

		for i := 0; i < len(currentLevel)-1; i++ {
			for j := i + 1; j < len(currentLevel); j++ {
				if currentLevel[i] > currentLevel[j] {
					currentLevel[i], currentLevel[j] = currentLevel[j], currentLevel[i]
				}
			}
		}

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
	var (
		outFile  = flag.String("o", "build.ninja", "output ninja file")
		all      = flag.Bool("a", false, "parse all .bp files in directory")
		ccFlag   = flag.String("cc", "", "C compiler (default: gcc)")
		cxxFlag  = flag.String("cxx", "", "C++ compiler (default: g++)")
		arFlag   = flag.String("ar", "", "archiver (default: ar)")
		archFlag = flag.String("arch", "", "target architecture (arm, arm64, x86, x86_64)")
		hostFlag = flag.Bool("host", false, "build for host (overrides arch)")
		osFlag   = flag.String("os", "", "target OS (linux, darwin, windows)")
	)
	flag.Parse()

	if len(flag.Args()) < 1 && !*all {
		fmt.Fprintln(os.Stderr, "Usage: minibp [-o output] [-a] [-cc CC] [-cxx CXX] [-ar AR] [-arch ARCH] [-host] [-os OS] <file.bp | directory>")
		os.Exit(1)
	}

	srcDir := "."
	if *all && len(flag.Args()) > 0 {
		srcDir = flag.Args()[0]
	} else if len(flag.Args()) > 0 {
		srcDir = filepath.Dir(flag.Args()[0])
	}

	var files []string
	if *all {
		bpFiles, err := filepath.Glob(filepath.Join(srcDir, "*.bp"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		files = bpFiles
	} else {
		files = flag.Args()
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

	var allDefs []parser.Definition
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", file, err)
			os.Exit(1)
		}
		defer f.Close()

		parsedFile, err := parser.ParseFile(f, file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
			os.Exit(1)
		}

		allDefs = append(allDefs, parsedFile.Defs...)
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
		expandGlobsInModule(mod, srcDir)
		mergeVariantProps(mod, *archFlag, *hostFlag, eval)
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

	out, err := os.Create(*outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

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

	if err := gen.Generate(out); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating ninja: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s with %d modules\n", *outFile, len(modules))
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

func expandGlobsInModule(m *parser.Module, baseDir string) {
	if m.Map == nil {
		return
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
							matches := expandGlob(pattern, baseDir)
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

				if len(expandedSrcs) > 0 {
					l.Values = expandedSrcs
				}
			}
		}
	}
}

func expandGlob(pattern, baseDir string) []string {
	var result []string

	if strings.Contains(pattern, "**") {
		dir := baseDir
		suffix := ""
		if idx := strings.Index(pattern, "/**"); idx >= 0 {
			dir = filepath.Join(baseDir, pattern[:idx])
			suffix = pattern[idx+3:]
		}

		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, _ := filepath.Rel(baseDir, path)
			if suffix == "" || strings.HasSuffix(path, suffix) {
				result = append(result, relPath)
			}
			return nil
		})
	} else {
		fullPattern := filepath.Join(baseDir, pattern)
		matches, _ := filepath.Glob(fullPattern)
		for _, match := range matches {
			relPath, _ := filepath.Rel(baseDir, match)
			result = append(result, relPath)
		}
	}

	return result
}
