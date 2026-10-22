package pyast

import (
	"sort"
	"strings"
)

// Def is a function/class definition (name, lineno).
type Def struct {
	Name string
	Line int
}

// Defs returns every FunctionDef/AsyncFunctionDef/ClassDef in walk order.
func Defs(mod *Node) []Def {
	var out []Def
	Walk(mod, func(n *Node) {
		switch n.Kind {
		case "FunctionDef", "AsyncFunctionDef", "ClassDef":
			out = append(out, Def{n.Name, n.Line})
		}
	})
	return out
}

// StrConst is a string Constant (value, lineno).
type StrConst struct {
	Value string
	Line  int
}

// StrConstants returns every str Constant under n in walk order.
func StrConstants(n *Node) []StrConst {
	var out []StrConst
	Walk(n, func(x *Node) {
		if x.Kind == "Constant" && x.IsStr {
			out = append(out, StrConst{x.Str, x.Line})
		}
	})
	return out
}

// IsAssertionBearing reports an Assert statement or a `<x>.assert*(...)`
// call.
func IsAssertionBearing(n *Node) bool {
	if n.Kind == "Assert" {
		return true
	}
	return n.Kind == "Call" && n.Func != nil && n.Func.Kind == "Attribute" && strings.HasPrefix(n.Func.Name, "assert")
}

// NamesIn returns Name ids and Attribute attrs under n.
func NamesIn(n *Node) map[string]bool {
	out := map[string]bool{}
	Walk(n, func(x *Node) {
		if x.Kind == "Name" || x.Kind == "Attribute" {
			out[x.Name] = true
		}
	})
	return out
}

// StrValuesIn returns the str Constant values under n.
func StrValuesIn(n *Node) map[string]bool {
	out := map[string]bool{}
	Walk(n, func(x *Node) {
		if x.Kind == "Constant" && x.IsStr {
			out[x.Str] = true
		}
	})
	return out
}

// SortedKeys returns the sorted keys of a set.
func SortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ImportBindsName is ast_analysis.imports.import_binds_name.
func ImportBindsName(n *Node, name string) bool {
	for _, a := range n.Aliases {
		if n.Kind == "Import" {
			bound := a.AsName
			if bound == "" {
				bound = strings.SplitN(a.Name, ".", 2)[0]
			}
			if bound == name {
				return true
			}
		} else if a.Name == name || (a.AsName != "" && a.AsName == name) {
			return true
		}
	}
	return false
}

// AssignmentListsDunderAll is assignment_lists_dunder_all.
func AssignmentListsDunderAll(n *Node, name string) bool {
	if n.Kind != "Assign" {
		return false
	}
	ok := false
	for _, t := range n.Targets {
		if t.Kind == "Name" && t.Name == "__all__" {
			ok = true
		}
	}
	if !ok || n.Value == nil {
		return false
	}
	switch n.Value.Kind {
	case "List", "Tuple", "Set":
	default:
		return false
	}
	for _, e := range n.Value.Elts {
		if e.Kind == "Constant" && e.IsStr && e.Str == name {
			return true
		}
	}
	return false
}
