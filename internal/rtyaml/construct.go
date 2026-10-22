package rtyaml

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// constructor is RoundTripConstructor (comment_handling None,
// preserve_quotes True) over a composed, already validated node graph.
type constructor struct {
	built map[*pyyaml.Node]any
}

var templatedID = regexp.MustCompile(`^id(?:[0-9]{3,})$`)

func isTemplated(a string) bool { return templatedID.MatchString(a) && a != "id000" }

func boolPtr(b bool) *bool { return &b }

func (c *constructor) object(n *pyyaml.Node) any {
	if v, ok := c.built[n]; ok {
		return v
	}
	var v any
	switch {
	case n.Tag == pyyaml.TagMap && n.Kind == pyyaml.MappingNode:
		m := NewMap()
		c.built[n] = m
		c.mapping(n, m)
		c.collectionStyle(m.Len(), n, &m.Flow)
		v = m
	case n.Tag == pyyaml.TagSeq && n.Kind == pyyaml.SequenceNode:
		s := &Seq{}
		c.built[n] = s
		c.sequence(n, s)
		c.collectionStyle(len(s.Items), n, &s.Flow)
		v = s
	case n.Tag == "tag:yaml.org,2002:set" && n.Kind == pyyaml.MappingNode:
		m := NewMap()
		m.IsSet = true
		m.Flow = boolPtr(n.FlowStyle)
		c.built[n] = m
		c.setting(n, m)
		v = m
	case n.Tag == "tag:yaml.org,2002:omap" && n.Kind == pyyaml.SequenceNode:
		m := NewMap()
		m.Ordered = true
		m.Flow = boolPtr(n.FlowStyle)
		c.built[n] = m
		c.omap(n, m)
		v = m
	case n.Kind == pyyaml.ScalarNode:
		v = c.scalar(n)
		c.built[n] = v
	default:
		v = c.unknown(n)
	}
	return v
}

// collectionStyle is set_collection_style.
func (c *constructor) collectionStyle(length int, n *pyyaml.Node, flow **bool) {
	if length == 0 {
		return
	}
	*flow = boolPtr(n.FlowStyle)
}

func anchorOf(n *pyyaml.Node) *Anchor {
	if n.Anchor != "" && !isTemplated(n.Anchor) {
		return &Anchor{Value: n.Anchor}
	}
	return nil
}

// addComment is _yaml_add_comment on a container's own comment.
func caComment(ca *CommentAttr, cmt *pyyaml.PyList) {
	ca.Comment = cmt
}

// keyCommentExtend is yaml_key_comment_extend (clear=False).
func keyCommentExtend(ca *CommentAttr, key string, comment *pyyaml.PyList) {
	r, ok := ca.Items[key]
	if !ok {
		r = pyyaml.NewList(nil, nil, nil, nil)
		ca.Items[key] = r
	}
	if r.Get(1) == nil {
		r.Set(1, comment.Get(1))
	} else if l, ok := r.Get(1).(*pyyaml.PyList); ok {
		if more, ok := comment.Get(0).(*pyyaml.PyList); ok {
			l.V = append(l.V, more.V...)
		}
	}
	r.Set(0, comment.Get(0))
}

// valueCommentExtend is yaml_value_comment_extend (clear=False).
func valueCommentExtend(ca *CommentAttr, key string, comment *pyyaml.PyList) {
	r, ok := ca.Items[key]
	if !ok {
		r = pyyaml.NewList(nil, nil, nil, nil)
		ca.Items[key] = r
	}
	if r.Get(3) == nil {
		r.Set(3, comment.Get(1))
	} else if l, ok := r.Get(3).(*pyyaml.PyList); ok {
		if more, ok := comment.Get(0).(*pyyaml.PyList); ok {
			l.V = append(l.V, more.V...)
		}
	}
	r.Set(2, comment.Get(0))
}

func endExtend(ca *CommentAttr, v any) {
	ca.End = nil
	if l, ok := v.(*pyyaml.PyList); ok {
		for _, x := range l.V {
			if ct, ok := x.(*pyyaml.CommentToken); ok {
				ca.End = append(ca.End, ct)
			}
		}
	}
}

// mapping is RoundTripConstructor.construct_mapping.
func (c *constructor) mapping(n *pyyaml.Node, m *Map) {
	merge := c.flatten(n)
	if n.Comment.Len() > 0 {
		ca := m.ca()
		caComment(ca, n.Comment.Slice(0, 2))
		if n.Comment.Len() > 2 {
			endExtend(ca, n.Comment.Get(2))
		}
	}
	m.Anchor = anchorOf(n)
	var lastKey any
	lastValueIsNone := false
	first := true
	for _, kv := range n.Pairs {
		kn, vn := kv[0], kv[1]
		key := c.object(kn)
		switch x := key.(type) {
		case *Seq:
			cp := *x
			cp.keySeq = true
			cp.Flow = boolPtr(kn.FlowStyle)
			key = &cp
		case *Map:
			cp := *x
			cp.keyMap = true
			cp.Flow = boolPtr(kn.FlowStyle)
			key = &cp
		}
		val := c.object(vn)
		k := itemKey(key)
		if kn.Comment.Len() > 4 && pyyaml.Truthy(kn.Comment.Get(4)) {
			c4 := kn.Comment.Get(4)
			kn.Comment.V = append(kn.Comment.V[:4:4], kn.Comment.V[5:]...)
			if !first && lastValueIsNone {
				kn.Comment.Set(0, c4)
				valueCommentExtend(m.ca(), itemKey(lastKey), kn.Comment)
			} else {
				kn.Comment.Set(2, c4)
				keyCommentExtend(m.ca(), k, kn.Comment)
			}
			kn.Comment = nil
		}
		if kn.Comment.Len() > 0 {
			keyCommentExtend(m.ca(), k, kn.Comment)
		}
		if vn.Comment.Len() > 0 {
			valueCommentExtend(m.ca(), k, vn.Comment)
		}
		m.Set(key, val)
		lastKey, lastValueIsNone, first = key, val == nil, false
	}
	if merge != nil {
		m.addMerge(merge)
	}
}

// flatten is RoundTripConstructor.flatten_mapping (merge sources are
// constructed and kept aside; the "<<" pair is removed).
func (c *constructor) flatten(n *pyyaml.Node) *Merge {
	var mv *Merge
	for i := 0; i < len(n.Pairs); {
		k, v := n.Pairs[i][0], n.Pairs[i][1]
		switch k.Tag {
		case pyyaml.TagMerge:
			n.Pairs = append(n.Pairs[:i:i], n.Pairs[i+1:]...)
			if mv == nil {
				mv = &Merge{}
			}
			mv.Pos = i
			switch v.Kind {
			case pyyaml.MappingNode:
				if m, ok := c.object(v).(*Map); ok {
					mv.Maps = append(mv.Maps, m)
				}
			case pyyaml.SequenceNode:
				if s, ok := c.object(v).(*Seq); ok {
					mv.Sequence = s
				}
				for _, sub := range v.Items {
					if m, ok := c.object(sub).(*Map); ok {
						mv.Maps = append(mv.Maps, m)
					}
				}
			}
		case pyyaml.TagValue:
			k.Tag = pyyaml.TagStr
			i++
		default:
			i++
		}
	}
	if mv != nil && len(mv.Maps) == 0 {
		return nil
	}
	return mv
}

// sequence is construct_rt_sequence.
func (c *constructor) sequence(n *pyyaml.Node, s *Seq) {
	if n.Comment.Len() > 0 {
		ca := s.ca()
		caComment(ca, n.Comment.Slice(0, 2))
		if n.Comment.Len() > 2 {
			endExtend(ca, n.Comment.Get(2))
		}
	}
	s.Anchor = anchorOf(n)
	for idx, child := range n.Items {
		if child.Comment.Len() > 0 {
			keyCommentExtend(s.ca(), itemKey(idx), child.Comment)
			child.Comment = nil
		}
		s.Items = append(s.Items, c.object(child))
	}
}

// setting is construct_setting (keys only).
func (c *constructor) setting(n *pyyaml.Node, m *Map) {
	if n.Comment.Len() > 0 {
		ca := m.ca()
		caComment(ca, n.Comment.Slice(0, 2))
		if n.Comment.Len() > 2 {
			endExtend(ca, n.Comment.Get(2))
		}
	}
	m.Anchor = anchorOf(n)
	for _, kv := range n.Pairs {
		key := c.object(kv[0])
		c.object(kv[1])
		k := itemKey(key)
		if kv[0].Comment.Len() > 0 {
			keyCommentExtend(m.ca(), k, kv[0].Comment)
		}
		if kv[1].Comment.Len() > 0 {
			valueCommentExtend(m.ca(), k, kv[1].Comment)
		}
		m.rawSet(key, nil)
		m.own[k] = true
	}
}

// omap is construct_yaml_omap.
func (c *constructor) omap(n *pyyaml.Node, m *Map) {
	if n.Comment.Len() > 0 {
		ca := m.ca()
		caComment(ca, n.Comment.Slice(0, 2))
		if n.Comment.Len() > 2 {
			endExtend(ca, n.Comment.Get(2))
		}
	}
	for _, sub := range n.Items {
		kn, vn := sub.Pairs[0][0], sub.Pairs[0][1]
		key := c.object(kn)
		val := c.object(vn)
		k := itemKey(key)
		if kn.Comment.Len() > 0 {
			keyCommentExtend(m.ca(), k, kn.Comment)
		}
		if sub.Comment.Len() > 0 {
			keyCommentExtend(m.ca(), k, sub.Comment)
		}
		if vn.Comment.Len() > 0 {
			valueCommentExtend(m.ca(), k, vn.Comment)
		}
		m.Set(key, val)
	}
}

// unknown is construct_unknown for collections (a kept tag).
func (c *constructor) unknown(n *pyyaml.Node) any {
	tag := &Tag{Handle: n.TagHandle, Value: n.Tag, Suffix: tagSuffix(n)}
	switch n.Kind {
	case pyyaml.MappingNode:
		m := NewMap()
		m.Flow = boolPtr(n.FlowStyle)
		m.Tag = tag
		c.built[n] = m
		m.Anchor = anchorOf(n)
		// construct_mapping(node, data) with deep=False
		c.mapping(n, m)
		return m
	case pyyaml.SequenceNode:
		s := &Seq{Flow: boolPtr(n.FlowStyle), Tag: tag}
		c.built[n] = s
		s.Anchor = anchorOf(n)
		for _, child := range n.Items {
			s.Items = append(s.Items, c.object(child))
		}
		return s
	}
	return nil
}

// tagSuffix is the Tag's suffix as scanned (handle stripped).
func tagSuffix(n *pyyaml.Node) string {
	if n.TagHandle == "!!" {
		return strings.TrimPrefix(n.Tag, "tag:yaml.org,2002:")
	}
	if n.TagHandle == "!" {
		return strings.TrimPrefix(n.Tag, "!")
	}
	return n.Tag
}

func (c *constructor) scalarText(n *pyyaml.Node) string {
	if n.Style == '>' {
		return strings.ReplaceAll(n.Value, "\a", "")
	}
	return n.Value
}

// scalar is the scalar half of RoundTripConstructor.
func (c *constructor) scalar(n *pyyaml.Node) any {
	anchor := anchorOf(n)
	if anchor != nil {
		anchor.AlwaysDump = true
	}
	if n.TagHandle != "" && n.Tag == pyyaml.TagStr {
		return &Tagged{Value: n.Value, Style: n.Style, Tag: Tag{Handle: n.TagHandle, Suffix: tagSuffix(n), Value: n.Tag}, Anchor: anchor}
	}
	switch n.Tag {
	case pyyaml.TagNull:
		return nil
	case pyyaml.TagBool:
		b := map[string]bool{"yes": true, "no": false, "y": true, "n": false, "true": true, "false": false, "on": true, "off": false}[strings.ToLower(n.Value)]
		if anchor != nil {
			return &Bool{V: b, Anchor: anchor}
		}
		return b
	case pyyaml.TagInt:
		return constructInt(n.Value, anchor)
	case pyyaml.TagFloat:
		return constructFloat(n.Value, anchor)
	case pyyaml.TagTS:
		return constructTimestamp(n.Value)
	case pyyaml.TagStr:
		return c.str(n, anchor)
	case pyyaml.TagBinary:
		return &Tagged{Value: n.Value, Style: n.Style, Tag: Tag{Handle: n.TagHandle, Suffix: "binary", Value: n.Tag}}
	}
	return &Tagged{Value: c.scalarText(n), Style: n.Style, Tag: Tag{Handle: n.TagHandle, Suffix: tagSuffix(n), Value: n.Tag}, Anchor: anchor}
}

// str is construct_scalar for strings (preserve_quotes).
func (c *constructor) str(n *pyyaml.Node, anchor *Anchor) any {
	switch n.Style {
	case '|':
		return &Str{V: n.Value, Style: '|', Anchor: anchor}
	case '>':
		var folds []int
		idx := -1
		for {
			j := strings.Index(n.Value[idx+1:], "\a")
			if j < 0 {
				break
			}
			idx += 1 + j
			folds = append(folds, len([]rune(n.Value[:idx]))-len(folds))
		}
		return &Str{V: strings.ReplaceAll(n.Value, "\a", ""), Style: '>', FoldPos: folds, Anchor: anchor}
	case '\'', '"':
		return &Str{V: n.Value, Style: n.Style, Anchor: anchor}
	}
	if anchor != nil {
		return &Str{V: n.Value, Style: 'p', Anchor: anchor}
	}
	return n.Value
}

// constructInt is RoundTripConstructor.construct_yaml_int (YAML 1.2).
func constructInt(vsu string, anchor *Anchor) any {
	var underscore *[3]any
	if sx := strings.TrimRight(vsu, "_"); strings.Contains(sx, "_") {
		u := [3]any{len(sx) - strings.LastIndex(sx, "_") - 1, false, false}
		underscore = &u
	}
	vs := strings.ReplaceAll(vsu, "_", "")
	sign := int64(1)
	if vs != "" && (vs[0] == '+' || vs[0] == '-') {
		if vs[0] == '-' {
			sign = -1
		}
		vs = vs[1:]
	}
	parse := func(s string, base int) *big.Int {
		n, _ := new(big.Int).SetString(s, base)
		if n == nil {
			n = new(big.Int)
		}
		return n.Mul(n, big.NewInt(sign))
	}
	prefixed := func(kind IntKind, base int) any {
		width := 0
		if len(vs) > 2 && vs[2] == '0' {
			width = len(vs[2:])
		}
		if underscore != nil {
			underscore[1] = len(vsu) > 2 && vsu[2] == '_'
			underscore[2] = len(vsu[2:]) > 1 && vsu[len(vsu)-1] == '_'
		}
		return &Int{V: parse(vs[2:], base), Kind: kind, Width: width, Underscore: underscore, Anchor: anchor}
	}
	switch {
	case strings.HasPrefix(vs, "0b"):
		return prefixed(IntBinary, 2)
	case strings.HasPrefix(vs, "0x"):
		kind := IntHex
		for _, ch := range vs[2:] {
			if strings.ContainsRune("ABCDEF", ch) {
				kind = IntHexCaps
				break
			}
			if strings.ContainsRune("abcdef", ch) {
				break
			}
		}
		return prefixed(kind, 16)
	case strings.HasPrefix(vs, "0o"):
		return prefixed(IntOctal, 8)
	case vs != "" && vs[0] == '0':
		if underscore != nil {
			underscore[2] = len(vsu) > 1 && vsu[len(vsu)-1] == '_'
		}
		return &Int{V: parse(vs, 10), Width: len(vs), Underscore: underscore, Anchor: anchor}
	case underscore != nil:
		underscore[2] = len(vsu) > 1 && vsu[len(vsu)-1] == '_'
		return &Int{V: parse(vs, 10), Underscore: underscore, Anchor: anchor}
	case anchor != nil:
		return &Int{V: parse(vs, 10), Anchor: anchor}
	}
	n := parse(vs, 10)
	if n.IsInt64() {
		return n.Int64()
	}
	return n
}

// constructFloat is RoundTripConstructor.construct_yaml_float (YAML 1.2).
func constructFloat(vso string, anchor *Anchor) any {
	leadingZeros := func(v string) int {
		n := 0
		for _, ch := range v {
			if ch != '0' && ch != '.' {
				break
			}
			if ch == '0' {
				n++
			}
		}
		return n
	}
	vs := strings.ToLower(strings.ReplaceAll(vso, "_", ""))
	sign := 1.0
	mSign := ""
	if vs != "" && vs[0] == '-' {
		sign = -1
	}
	if vs != "" && (vs[0] == '+' || vs[0] == '-') {
		mSign = vs[:1]
		vs = vs[1:]
	}
	switch vs {
	case ".inf":
		return sign * math.Inf(1)
	case ".nan":
		return math.NaN()
	}
	f, _ := strconv.ParseFloat(vs, 64)
	f *= sign
	if strings.Contains(vs, "e") {
		sep := "e"
		if !strings.Contains(vso, "e") {
			sep = "E"
		}
		parts := strings.SplitN(vso, sep, 2)
		mantissa, exponent := parts[0], parts[1]
		width := len(mantissa)
		prec := strings.Index(mantissa, ".")
		if mSign != "" {
			width--
		}
		return &Float{V: f, Width: width, Prec: prec, MSign: mSign, MLead0: leadingZeros(mantissa), Exp: sep,
			EWidth: len(exponent), ESign: exponent != "" && (exponent[0] == '+' || exponent[0] == '-'), Anchor: anchor}
	}
	return &Float{V: f, Width: len(vso), Prec: strings.Index(vso, "."), MSign: mSign, MLead0: leadingZeros(vso), Anchor: anchor}
}

var reTimestamp = regexp.MustCompile(`^([0-9]{4})-([0-9]{1,2})-([0-9]{1,2})(?:((?:[Tt])|[ \t]+)([0-9]{1,2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]*))?(?:[ \t]*(Z|([-+])([0-9]{1,2})(?::([0-9]{2}))?))?)?$`)

// constructTimestamp is RoundTripConstructor.construct_yaml_timestamp.
func constructTimestamp(v string) any {
	m := reTimestamp.FindStringSubmatch(v)
	if m == nil {
		return v
	}
	at := func(s string) int { n, _ := strconv.Atoi(s); return n }
	y, mo, d := at(m[1]), at(m[2]), at(m[3])
	if m[5] == "" {
		return Date{y, mo, d}
	}
	ts := &TimeStamp{Y: y, Mo: mo, D: d, H: at(m[5]), Mi: at(m[6]), S: at(m[7]), T: strings.ContainsAny(m[4], "Tt")}
	if frac := m[8]; frac != "" {
		f := frac
		if len(f) > 6 {
			f = f[:6]
		}
		f += strings.Repeat("0", 6-len(f))
		ts.Micro = at(f)
		if len(frac) > 6 && frac[6] > '4' {
			ts.Micro++
		}
		if ts.Micro > 999999 {
			ts.Micro = 0
		}
	}
	switch {
	case m[10] != "":
		off := at(m[11])*60 + at(m[12])
		if m[10] == "-" {
			off = -off
		}
		ts.Offset = &off
		ts.TZName = m[9]
	case m[9] == "Z":
		zero := 0
		ts.Offset = &zero
		ts.TZName = "Z"
	}
	// without "T" or a numeric offset create_timestamp's plain datetime
	// is kept (SafeRepresenter.represent_datetime on dump)
	ts.Plain = !ts.T && m[10] == ""
	return ts
}
