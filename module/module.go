// module/module.go - Base module interface and struct
package module

// Module is the interface for all module types
type Module interface {
	Name() string
	Type() string
	Srcs() []string
	Deps() []string
	Props() map[string]interface{}
	GetProp(key string) interface{}
}

// BaseModule provides a common implementation of the Module interface
type BaseModule struct {
	Name_  string
	Type_  string
	Srcs_  []string
	Deps_  []string
	Props_ map[string]interface{}
}

func (m *BaseModule) Name() string                   { return m.Name_ }
func (m *BaseModule) Type() string                   { return m.Type_ }
func (m *BaseModule) Srcs() []string                 { return m.Srcs_ }
func (m *BaseModule) Deps() []string                 { return m.Deps_ }
func (m *BaseModule) Props() map[string]interface{}  { return m.Props_ }
func (m *BaseModule) GetProp(key string) interface{} { return m.Props_[key] }
