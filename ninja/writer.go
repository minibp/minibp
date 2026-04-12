// ninja/writer.go - Ninja build file writer
package ninja

import (
	"fmt"
	"io"
	"strings"
)

// Writer provides utilities for writing ninja build files
type Writer struct {
	w io.Writer
}

// NewWriter creates a new Writer that writes to the given io.Writer
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Rule writes a ninja rule definition
// Format: rule <name>
//
//	command = <command>
//	[deps = <deps>]
func (w *Writer) Rule(name, command string, deps ...string) {
	fmt.Fprintf(w.w, "rule %s\n", name)
	fmt.Fprintf(w.w, "  command = %s\n", command)
	if len(deps) > 0 && deps[0] != "" {
		fmt.Fprintf(w.w, "  deps = %s\n", strings.Join(deps, " "))
	}
	fmt.Fprintln(w.w)
}

// Build writes a ninja build edge
// Format: build <output>: <rule> <inputs> [| <deps>]
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

// Variable writes a ninja variable definition
// Format: <name> = <value>
func (w *Writer) Variable(name, value string) {
	fmt.Fprintf(w.w, "%s = %s\n", name, value)
}

// Comment writes a ninja comment
// Format: # <text>
func (w *Writer) Comment(text string) {
	if text != "" {
		fmt.Fprintf(w.w, "# %s\n", text)
	} else {
		fmt.Fprintln(w.w)
	}
}
