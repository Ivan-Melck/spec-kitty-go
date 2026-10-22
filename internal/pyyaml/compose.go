package pyyaml

import ()

// NodeKind is the kind of a composed node.
type NodeKind int

const (
	ScalarNode NodeKind = iota
	SequenceNode
	MappingNode
)

func (k NodeKind) id() string {
	switch k {
	case SequenceNode:
		return "sequence"
	case MappingNode:
		return "mapping"
	}
	return "scalar"
}

// Node is a composed YAML node (yaml.nodes.Node).
type Node struct {
	Kind      NodeKind
	Tag       string
	Value     string // scalars
	Style     rune   // scalars: 0 plain, '\'', '"', '|', '>'
	TagHandle string // the tag's handle ("!", "!!", "!e!"), "" if none / implicit
	Items     []*Node
	Pairs     [][2]*Node
	FlowStyle bool
	Start     *Mark
	End       *Mark
}

const (
	tagPrefix = "tag:yaml.org,2002:"
	TagNull   = tagPrefix + "null"
	TagBool   = tagPrefix + "bool"
	TagInt    = tagPrefix + "int"
	TagFloat  = tagPrefix + "float"
	TagStr    = tagPrefix + "str"
	TagTS     = tagPrefix + "timestamp"
	TagSeq    = tagPrefix + "seq"
	TagMap    = tagPrefix + "map"
	TagMerge  = tagPrefix + "merge"
	TagValue  = tagPrefix + "value"
	TagBinary = tagPrefix + "binary"
)

type composer struct {
	*parser
	anchors map[string]*Node
	resolve func(kind NodeKind, value string, implicit [2]bool) string
}

func (c *composer) resolveTag(kind NodeKind, value string, implicit [2]bool) string {
	if c.resolve != nil {
		return c.resolve(kind, value, implicit)
	}
	if c.dialect.ruamel() {
		return resolveRuamel(c.v11())(kind, value, implicit)
	}
	return resolve11(kind, value, implicit)
}

func (c *composer) cerr(context string, cm *Mark, problem string, pm *Mark) {
	panic(&MarkedYAMLError{Class: "ComposerError", Context: context, ContextMark: cm, Problem: problem, ProblemMark: pm, dialect: c.dialect})
}

// getSingleNode is Composer.get_single_node.
func (c *composer) getSingleNode() *Node {
	c.getEvent()
	var doc *Node
	if !c.checkEvent(eStreamEnd) {
		doc = c.composeDocument()
	}
	if !c.checkEvent(eStreamEnd) {
		ev := c.getEvent()
		c.cerr("expected a single document in the stream", doc.Start, "but found another document", ev.start)
	}
	c.getEvent()
	return doc
}

func (c *composer) composeDocument() *Node {
	c.getEvent()
	n := c.composeNode()
	c.getEvent()
	c.anchors = map[string]*Node{}
	return n
}

func (c *composer) composeNode() *Node {
	if c.checkEvent(eAlias) {
		ev := c.getEvent()
		n, ok := c.anchors[ev.anchor]
		if !ok {
			c.cerr("", nil, "found undefined alias "+reprS(ev.anchor), ev.start)
		}
		return n
	}
	ev := c.peekEvent()
	if ev.hasAnchor {
		if first, ok := c.anchors[ev.anchor]; ok && c.dialect == PyYAML {
			c.cerr("found duplicate anchor "+reprS(ev.anchor)+"; first occurrence", first.Start, "second occurrence", ev.start)
		}
	}
	var n *Node
	switch {
	case c.checkEvent(eScalar):
		ev := c.getEvent()
		tag := ev.tag
		if !ev.hasTag || tag == "!" {
			tag = c.resolveTag(ScalarNode, ev.value, ev.implicit)
		}
		n = &Node{Kind: ScalarNode, Tag: tag, TagHandle: ev.tagHandle, Value: ev.value, Style: ev.style, Start: ev.start, End: ev.end}
		if ev.hasAnchor {
			c.anchors[ev.anchor] = n
		}
	case c.checkEvent(eSequenceStart):
		start := c.getEvent()
		tag := start.tag
		if !start.hasTag || tag == "!" {
			tag = c.resolveTag(SequenceNode, "", start.implicit)
		}
		n = &Node{Kind: SequenceNode, Tag: tag, TagHandle: start.tagHandle, FlowStyle: start.flowStyle, Start: start.start}
		if start.hasAnchor {
			c.anchors[start.anchor] = n
		}
		for !c.checkEvent(eSequenceEnd) {
			n.Items = append(n.Items, c.composeNode())
		}
		n.End = c.getEvent().end
	case c.checkEvent(eMappingStart):
		start := c.getEvent()
		tag := start.tag
		if !start.hasTag || tag == "!" {
			tag = c.resolveTag(MappingNode, "", start.implicit)
		}
		n = &Node{Kind: MappingNode, Tag: tag, TagHandle: start.tagHandle, FlowStyle: start.flowStyle, Start: start.start}
		if start.hasAnchor {
			c.anchors[start.anchor] = n
		}
		for !c.checkEvent(eMappingEnd) {
			k := c.composeNode()
			v := c.composeNode()
			n.Pairs = append(n.Pairs, [2]*Node{k, v})
		}
		n.End = c.getEvent().end
	}
	return n
}

// resolve11 is PyYAML's Resolver.resolve (YAML 1.1 implicit types).
func resolve11(kind NodeKind, value string, implicit [2]bool) string {
	if kind == ScalarNode && implicit[0] {
		switch ResolvePlain(value) {
		case "null":
			return TagNull
		case "bool":
			return TagBool
		case "int":
			return TagInt
		case "float":
			return TagFloat
		case "timestamp":
			return TagTS
		case "merge":
			return TagMerge
		case "value":
			return TagValue
		}
	}
	switch kind {
	case SequenceNode:
		return TagSeq
	case MappingNode:
		return TagMap
	}
	return TagStr
}

// run executes fn, converting the parser's panics into errors.
func run(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case *MarkedYAMLError:
				err = e
			case *ReaderError:
				err = e
			case *PyError:
				err = e
			default:
				panic(r)
			}
		}
	}()
	fn()
	return nil
}

// ComposeOptions selects the dialect of Compose.
type ComposeOptions struct {
	Dialect Dialect
	// Resolve overrides the implicit tag resolver (default: PyYAML's
	// YAML 1.1 resolver).
	Resolve func(kind NodeKind, value string, implicit [2]bool) string
}

// Compose parses src (a Python str: Marks are named "<unicode string>"
// and carry snippets) into a single-document node graph, nil for an empty
// stream. Errors are *MarkedYAMLError or *ReaderError with the library's
// exact message.
func Compose(src string, opts ComposeOptions) (*Node, error) {
	var n *Node
	err := run(func() {
		c := &composer{parser: newParser(src, opts.Dialect), anchors: map[string]*Node{}, resolve: opts.Resolve}
		n = c.getSingleNode()
	})
	return n, err
}
