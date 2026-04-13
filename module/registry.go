// module/registry.go - Factory registry for module creation
package module

import (
	"fmt"

	"minibp/parser"
)

// Factory creates Module instances from AST nodes
type Factory interface {
	Create(ast *parser.Module, eval *parser.Evaluator) (Module, error)
}

// Registry maps module type names to their factories
var Registry = make(map[string]Factory)

// Register adds a factory for a module type
func Register(name string, factory Factory) {
	Registry[name] = factory
}

// Lookup retrieves the factory for a module type
func Lookup(name string) Factory {
	return Registry[name]
}

// Create builds a module from AST using the appropriate factory
func Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	factory := Lookup(ast.Type)
	if factory == nil {
		return nil, fmt.Errorf("unknown module type: %s", ast.Type)
	}
	return factory.Create(ast, eval)
}
