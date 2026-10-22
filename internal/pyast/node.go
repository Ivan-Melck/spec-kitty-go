package pyast

// Node is an `ast` node. Kind names follow CPython (Module, FunctionDef,
// AsyncFunctionDef, ClassDef, Assert, Import, ImportFrom, Assign, Expr,
// Call, Attribute, Name, Constant, Compare, JoinedStr, FormattedValue, ...).
// Children holds every child node in CPython field order, so a
// breadth-first walk matches ast.walk.
type Node struct {
	Kind string
	Line int
	// Name is FunctionDef/ClassDef.name, Name.id, Attribute.attr and
	// keyword.arg.
	Name string
	// Constant value: IsStr with Str for str constants.
	IsStr bool
	Str   string

	Children []*Node

	// Call
	Func     *Node
	Args     []*Node
	Keywords []*Node // keyword nodes (Value = keyword value)
	// Attribute value / keyword value / Expr value / Assign value
	Value *Node
	// Compare
	Left        *Node
	Ops         []string // "In", "NotIn", "Eq", ...
	Comparators []*Node
	// Assert
	Test *Node
	// Assign
	Targets []*Node
	// List/Tuple/Set
	Elts []*Node
	// Import/ImportFrom
	Aliases []Alias
	// Module body
	Body []*Node
}

// Alias is an import alias.
type Alias struct{ Name, AsName string }

func (n *Node) add(cs ...*Node) {
	for _, c := range cs {
		if c != nil {
			n.Children = append(n.Children, c)
		}
	}
}

// Walk visits n and its descendants breadth-first (ast.walk order).
func Walk(n *Node, fn func(*Node)) {
	if n == nil {
		return
	}
	queue := []*Node{n}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		queue = append(queue, cur.Children...)
		fn(cur)
	}
}
