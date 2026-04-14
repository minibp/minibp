package parser

import (
	"fmt"
	"regexp"
	"strings"
)

var interpolationPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type Evaluator struct {
	vars   map[string]interface{}
	config map[string]string
}

func NewEvaluator() *Evaluator {
	return &Evaluator{
		vars:   make(map[string]interface{}),
		config: make(map[string]string),
	}
}

func (e *Evaluator) SetVar(name string, value interface{}) {
	e.vars[name] = value
}

func (e *Evaluator) SetConfig(key, value string) {
	e.config[key] = value
}

func (e *Evaluator) ProcessAssignments(file *File) {
	e.ProcessAssignmentsFromDefs(file.Defs)
}

func (e *Evaluator) ProcessAssignmentsFromDefs(defs []Definition) {
	for _, def := range defs {
		assign, ok := def.(*Assignment)
		if !ok {
			continue
		}
		val := e.Eval(assign.Value)
		if assign.Assigner == "+=" {
			if existing, ok := e.vars[assign.Name]; ok {
				switch ev := existing.(type) {
				case string:
					if nv, ok := val.(string); ok {
						e.vars[assign.Name] = ev + nv
					}
				case []string:
					if nv, ok := val.(string); ok {
						e.vars[assign.Name] = append(ev, nv)
					} else if nv, ok := val.([]string); ok {
						e.vars[assign.Name] = append(ev, nv...)
					}
				case []interface{}:
					if nv, ok := val.([]interface{}); ok {
						e.vars[assign.Name] = append(ev, nv...)
					} else {
						e.vars[assign.Name] = append(ev, val)
					}
				}
			} else {
				e.vars[assign.Name] = val
			}
		} else {
			e.vars[assign.Name] = val
		}
	}
}

func (e *Evaluator) Eval(expr Expression) interface{} {
	switch v := expr.(type) {
	case *String:
		return e.interpolateString(v.Value)
	case *Int64:
		return v.Value
	case *Bool:
		return v.Value
	case *List:
		var result []interface{}
		for _, item := range v.Values {
			result = append(result, e.Eval(item))
		}
		return result
	case *Map:
		result := make(map[string]interface{})
		for _, prop := range v.Properties {
			result[prop.Name] = e.Eval(prop.Value)
		}
		return result
	case *Variable:
		if val, ok := e.vars[v.Name]; ok {
			return val
		}
		return v.Name
	case *Operator:
		left := e.Eval(v.Args[0])
		right := e.Eval(v.Args[1])
		return evalOperator(left, right, v.Operator)
	case *Select:
		return e.evalSelect(v)
	default:
		return nil
	}
}

func evalOperator(left, right interface{}, op rune) interface{} {
	if op == '+' {
		li, lok := left.(int64)
		ri, rok := right.(int64)
		if lok && rok {
			return li + ri
		}
		ls, lok := left.(string)
		rs, rok := right.(string)
		if lok && rok {
			return ls + rs
		}
	}
	return nil
}

func toString(v interface{}) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case []string:
		return strings.Join(s, " "), true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

func (e *Evaluator) interpolateString(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}

	return interpolationPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := interpolationPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		name := parts[1]
		val, ok := e.vars[name]
		if !ok {
			return match
		}
		return fmt.Sprintf("%v", val)
	})
}

func (e *Evaluator) evalSelect(s *Select) interface{} {
	if len(s.Conditions) == 0 || len(s.Cases) == 0 {
		return nil
	}

	cond := s.Conditions[0]
	var configValue string

	if cond.FunctionName == "" {
		if v, ok := cond.Args[0].(*Variable); ok {
			if val, ok := e.vars[v.Name]; ok {
				configValue = fmt.Sprintf("%v", val)
			} else {
				configValue = v.Name
			}
		}
	} else {
		switch cond.FunctionName {
		case "target":
			configValue = e.config["target"]
		case "arch":
			configValue = e.config["arch"]
		case "host":
			configValue = e.config["host"]
		case "os":
			configValue = e.config["os"]
		default:
			configValue = e.config[cond.FunctionName]
		}
	}

	for _, c := range s.Cases {
		for _, p := range c.Patterns {
			if p.Value != nil {
				patStr := e.evalPattern(p.Value)
				if patStr == configValue {
					return e.Eval(c.Value)
				}
			}
		}
	}

	for _, c := range s.Cases {
		if len(c.Patterns) == 1 {
			if v, ok := c.Patterns[0].Value.(*Variable); ok && v.Name == "default" {
				return e.Eval(c.Value)
			}
			if s, ok := c.Patterns[0].Value.(*String); ok && s.Value == "default" {
				return e.Eval(c.Value)
			}
		}
	}

	return nil
}

func (e *Evaluator) evalPattern(expr Expression) string {
	switch v := expr.(type) {
	case *String:
		return v.Value
	case *Variable:
		if val, ok := e.vars[v.Name]; ok {
			return fmt.Sprintf("%v", val)
		}
		return v.Name
	default:
		return ""
	}
}

func EvalToString(expr Expression, eval *Evaluator) string {
	if eval != nil {
		val := eval.Eval(expr)
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)
	}
	switch v := expr.(type) {
	case *String:
		return v.Value
	case *Variable:
		return v.Name
	case *Int64:
		return fmt.Sprintf("%d", v.Value)
	case *Bool:
		if v.Value {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func EvalToStringList(expr Expression, eval *Evaluator) []string {
	if eval == nil {
		return EvalToStringListNoEval(expr)
	}
	val := eval.Eval(expr)
	switch v := val.(type) {
	case []string:
		return v
	case []interface{}:
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case string:
		return []string{v}
	default:
		return nil
	}
}

func EvalToStringListNoEval(expr Expression) []string {
	if l, ok := expr.(*List); ok {
		var result []string
		for _, item := range l.Values {
			if s, ok := item.(*String); ok {
				result = append(result, s.Value)
			}
		}
		return result
	}
	return nil
}

func (e *Evaluator) EvalProperty(prop *Property) *Property {
	val := e.Eval(prop.Value)
	newProp := &Property{
		Name:     prop.Name,
		NamePos:  prop.NamePos,
		ColonPos: prop.ColonPos,
	}
	switch v := val.(type) {
	case string:
		newProp.Value = &String{Value: v}
	case int64:
		newProp.Value = &Int64{Value: v}
	case bool:
		newProp.Value = &Bool{Value: v}
	case []interface{}:
		var items []Expression
		for _, item := range v {
			if s, ok := item.(string); ok {
				items = append(items, &String{Value: s})
			} else if i, ok := item.(int64); ok {
				items = append(items, &Int64{Value: i})
			} else if b, ok := item.(bool); ok {
				items = append(items, &Bool{Value: b})
			}
		}
		newProp.Value = &List{Values: items}
	default:
		newProp.Value = prop.Value
	}
	return newProp
}

func (e *Evaluator) EvalModule(m *Module) {
	if m.Map == nil {
		return
	}
	var newProps []*Property
	for _, prop := range m.Map.Properties {
		newProps = append(newProps, e.EvalProperty(prop))
	}
	m.Map.Properties = newProps

	if m.Arch != nil {
		for arch, archMap := range m.Arch {
			var newArchProps []*Property
			for _, prop := range archMap.Properties {
				newArchProps = append(newArchProps, e.EvalProperty(prop))
			}
			archMap.Properties = newArchProps
			m.Arch[arch] = archMap
		}
	}
	if m.Host != nil {
		var newHostProps []*Property
		for _, prop := range m.Host.Properties {
			newHostProps = append(newHostProps, e.EvalProperty(prop))
		}
		m.Host.Properties = newHostProps
	}
	if m.Target != nil {
		var newTargetProps []*Property
		for _, prop := range m.Target.Properties {
			newTargetProps = append(newTargetProps, e.EvalProperty(prop))
		}
		m.Target.Properties = newTargetProps
	}
}
