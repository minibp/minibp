// ninja/writer.go - Ninja build file writer
package ninja

import (
	"fmt"
	"io"
	"strings"
)

type Writer struct {
	w io.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

func (w *Writer) Rule(name, command string, deps ...string) {
	fmt.Fprintf(w.w, "rule %s\n", name)
	fmt.Fprintf(w.w, "  command = %s\n", command)
	if len(deps) > 0 && deps[0] != "" {
		fmt.Fprintf(w.w, "  deps = %s\n", strings.Join(deps, " "))
	}
	fmt.Fprintln(w.w)
}

func (w *Writer) Build(output, rule string, inputs []string, deps []string) {
	fmt.Fprintf(w.w, "build %s: %s", output, rule)
	if len(inputs) > 0 {
		fmt.Fprintf(w.w, " %s", strings.Join(inputs, " "))
	}
	if len(deps) > 0 {
		fmt.Fprintf(w.w, " | %s", strings.Join(deps, " "))
	}
	fmt.Fprintln(w.w)
	fmt.Fprintln(w.w)
}

func (w *Writer) BuildWithVars(output, rule string, inputs []string, orderOnly []string, vars map[string]string) {
	fmt.Fprintf(w.w, "build %s: %s", output, rule)
	if len(inputs) > 0 {
		fmt.Fprintf(w.w, " %s", strings.Join(inputs, " "))
	}
	if len(orderOnly) > 0 {
		fmt.Fprintf(w.w, " || %s", strings.Join(orderOnly, " "))
	}
	fmt.Fprintln(w.w)
	for k, v := range vars {
		fmt.Fprintf(w.w, "  %s = %s\n", k, v)
	}
	fmt.Fprintln(w.w)
}

func (w *Writer) Variable(name, value string) {
	fmt.Fprintf(w.w, "%s = %s\n", name, value)
}

func (w *Writer) Comment(text string) {
	if text != "" {
		fmt.Fprintf(w.w, "# %s\n", text)
	} else {
		fmt.Fprintln(w.w)
	}
}

func (w *Writer) Desc(sourceDir, moduleName, action string, srcFile ...string) {
	srcStr := ""
	if len(srcFile) > 0 && srcFile[0] != "" {
		srcStr = " " + srcFile[0]
	}
	fmt.Fprintf(w.w, "# //%s:%s %s%s\n", sourceDir, moduleName, action, srcStr)
}

func (w *Writer) Subninja(path string) {
	fmt.Fprintf(w.w, "subninja %s\n\n", path)
}

func (w *Writer) Include(path string) {
	fmt.Fprintf(w.w, "include %s\n\n", path)
}

func (w *Writer) Phony(output string, inputs []string) {
	fmt.Fprintf(w.w, "build %s: phony %s\n", output, strings.Join(inputs, " "))
}

func (w *Writer) Default(targets []string) {
	fmt.Fprintf(w.w, "default %s\n", strings.Join(targets, " "))
}
