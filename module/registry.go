// module/registry.go - Factory registry for module creation
package module

import (
	"fmt"
	"sync"

	"minibp/parser"
)

// Factory creates Module instances from AST nodes
type Factory interface {
	Create(ast *parser.Module, eval *parser.Evaluator) (Module, error)
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Factory)
)

// Register adds a factory for a module type
func Register(name string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry[name] = factory
}

// Lookup retrieves the factory for a module type
func Lookup(name string) Factory {
	registryMu.RLock()
	defer registryMu.RUnlock()

	return registry[name]
}

func registrySnapshot() map[string]Factory {
	registryMu.RLock()
	defer registryMu.RUnlock()

	snapshot := make(map[string]Factory, len(registry))
	for name, factory := range registry {
		snapshot[name] = factory
	}

	return snapshot
}

func restoreRegistry(snapshot map[string]Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry = make(map[string]Factory, len(snapshot))
	for name, factory := range snapshot {
		registry[name] = factory
	}
}

func resetRegistry() {
	restoreRegistry(nil)
}

func registryLen() int {
	registryMu.RLock()
	defer registryMu.RUnlock()

	return len(registry)
}

// Create builds a module from AST using the appropriate factory
func Create(ast *parser.Module, eval *parser.Evaluator) (Module, error) {
	factory := Lookup(ast.Type)
	if factory == nil {
		return nil, fmt.Errorf("unknown module type: %s", ast.Type)
	}
	return factory.Create(ast, eval)
}
