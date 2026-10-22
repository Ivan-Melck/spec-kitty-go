package rtyaml

import (
	"encoding/base64"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// dnode is a representer node (ruamel.yaml.nodes.Node on the dump side).
type dnode struct {
	kind    pyyaml.NodeKind
	tag     Tag
	value   string
	style   rune // 0 = None / ""
	flow    *bool
	anchor  *Anchor
	comment *pyyaml.PyList
	items   []*dnode
	pairs   [][2]*dnode
}

func tagOf(suffix string) Tag { return Tag{Suffix: suffix, Value: suffix} }

// representer is RoundTripRepresenter (default_flow_style False).
type representer struct {
	objects map[any]*dnode // represented_objects (alias-able objects)
	count   int
}

func (r *representer) aliasKey(data any) (any, bool) {
	switch x := data.(type) {
	case *Map, *Seq, *jsonx.Object:
		return x, true
	case *Str:
		return x, x.Anchor != nil
	case *Int:
		return x, x.Anchor != nil
	case *Float:
		return x, x.Anchor != nil
	case *Bool:
		return x, x.Anchor != nil
	case *Tagged:
		return x, x.Anchor != nil
	case *TimeStamp:
		return x, true
	}
	return nil, false
}

func (r *representer) data(data any) *dnode {
	key, aliasable := r.aliasKey(data)
	if aliasable {
		if n, ok := r.objects[key]; ok {
			return n
		}
	}
	var n *dnode
	reg := func(node *dnode) *dnode {
		if aliasable {
			r.objects[key] = node
			r.count++
		}
		return node
	}
	switch x := data.(type) {
	case nil:
		if r.count == 0 {
			n = r.scalar(pyyaml.TagNull, "null", 0, nil)
		} else {
			n = r.scalar(pyyaml.TagNull, "", 0, nil)
		}
	case string:
		n = r.scalar(pyyaml.TagStr, x, 0, nil)
	case *Str:
		n = reg(r.str(x))
	case bool:
		n = r.boolNode(x, nil)
	case *Bool:
		n = reg(r.boolNode(x.V, x.Anchor))
	case int:
		n = r.scalar(pyyaml.TagInt, strconv.Itoa(x), 0, nil)
	case int64:
		n = r.scalar(pyyaml.TagInt, strconv.FormatInt(x, 10), 0, nil)
	case *big.Int:
		n = r.scalar(pyyaml.TagInt, x.String(), 0, nil)
	case *Int:
		n = reg(r.intNode(x))
	case float64:
		n = r.scalar(pyyaml.TagFloat, floatRepr(x), 0, nil)
	case *Float:
		n = reg(r.floatNode(x))
	case Date:
		n = r.scalar(pyyaml.TagTS, fmt.Sprintf("%04d-%02d-%02d", x.Y, x.M, x.D), 0, nil)
	case *TimeStamp:
		n = reg(r.timestamp(x))
	case []byte:
		n = r.scalar(pyyaml.TagBinary, encodebytes(x), '|', nil)
	case *Tagged:
		n = reg(r.tagged(x))
	case *Seq:
		n = r.sequence(x, func(node *dnode) { reg(node) })
	case []any:
		n = r.sequence(&Seq{Items: x}, func(node *dnode) { reg(node) })
	case *Map:
		switch {
		case x.IsSet:
			n = r.set(x, func(node *dnode) { reg(node) })
		case x.Ordered:
			n = r.omap(x, func(node *dnode) { reg(node) })
		default:
			n = r.mapping(x, nil, func(node *dnode) { reg(node) })
		}
	case *jsonx.Object:
		m := NewMap()
		for _, kv := range x.Pairs() {
			m.Set(kv.Key, kv.Value)
		}
		n = r.mapping(m, nil, func(node *dnode) { reg(node) })
	default:
		n = r.scalar("", fmt.Sprint(x), 0, nil)
	}
	return n
}

func (r *representer) scalar(tag, value string, style rune, anchor *Anchor) *dnode {
	n := &dnode{kind: pyyaml.ScalarNode, tag: tagOf(tag), value: value, style: style, anchor: anchor}
	return n
}

func (r *representer) str(x *Str) *dnode {
	switch x.Style {
	case '|':
		n := r.scalar(pyyaml.TagStr, x.V, '|', x.Anchor)
		n.blockComment(x.Comment)
		return n
	case '>':
		data := []rune(x.V)
		for i := len(x.FoldPos) - 1; i >= 0; i-- {
			fp := x.FoldPos[i]
			if fp < len(data) && data[fp] == ' ' && fp > 0 && !isSpace(data[fp-1]) && fp+1 < len(data) && !isSpace(data[fp+1]) {
				data = append(data[:fp:fp], append([]rune{'\a'}, data[fp:]...)...)
			}
		}
		n := r.scalar(pyyaml.TagStr, string(data), '>', x.Anchor)
		n.blockComment(x.Comment)
		return n
	case '\'', '"':
		return r.scalar(pyyaml.TagStr, x.V, x.Style, x.Anchor)
	}
	return r.scalar(pyyaml.TagStr, x.V, 0, x.Anchor)
}

// blockComment is represent_scalar's [None, [value.comment]] for a block
// scalar carrying an indicator-line comment.
func (n *dnode) blockComment(c string) {
	if c != "" {
		n.comment = pyyaml.NewList(nil, pyyaml.NewList(c))
	}
}

func isSpace(r rune) bool {
	return strings.ContainsRune(" \t\n\r\v\f\u0085\u00a0\u2028\u2029", r) || r == 0x1c || r == 0x1d || r == 0x1e || r == 0x1f
}

func (r *representer) boolNode(v bool, anchor *Anchor) *dnode {
	s := "false"
	if v {
		s = "true"
	}
	return r.scalar(pyyaml.TagBool, s, 0, anchor)
}

// intNode is represent_scalar_int and friends.
func (r *representer) intNode(x *Int) *dnode {
	v := x.V
	neg := v.Sign() < 0
	abs := new(big.Int).Abs(v)
	var digits, prefix string
	switch x.Kind {
	case IntBinary:
		digits, prefix = abs.Text(2), "0b"
	case IntOctal:
		digits, prefix = abs.Text(8), "0o"
	case IntHex:
		digits, prefix = abs.Text(16), "0x"
	case IntHexCaps:
		digits, prefix = strings.ToUpper(abs.Text(16)), "0x"
	default:
		digits = abs.Text(10)
	}
	if x.Width > 0 {
		w := x.Width
		if neg {
			w-- // f'{data:0{width}d}' counts the sign
		}
		for len(digits) < w {
			digits = "0" + digits
		}
	}
	if neg {
		if x.Kind == IntScalar {
			digits = "-" + digits
		} else {
			digits = "-" + digits // format(-5, 'b') is '-101'
		}
	}
	s := digits
	if u := x.Underscore; u != nil {
		if n, _ := u[0].(int); n != 0 {
			rs := []rune(s)
			pos := len(rs) - n
			for pos > 0 {
				rs = append(rs[:pos:pos], append([]rune{'_'}, rs[pos:]...)...)
				pos -= n
			}
			s = string(rs)
		}
		if b, _ := u[1].(bool); b {
			s = "_" + s
		}
		if b, _ := u[2].(bool); b {
			s += "_"
		}
	}
	return r.scalar(pyyaml.TagInt, prefix+s, 0, x.Anchor)
}

// floatRepr is SafeRepresenter.represent_float for a plain float.
func floatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return ".nan"
	case math.IsInf(f, 1):
		return ".inf"
	case math.IsInf(f, -1):
		return "-.inf"
	}
	return strings.ToLower(pyjson.ReprFloat(f))
}

// floatNode is represent_scalar_float.
func (r *representer) floatNode(d *Float) *dnode {
	f := d.V
	switch {
	case math.IsNaN(f):
		return r.scalar(pyyaml.TagFloat, ".nan", 0, d.Anchor)
	case math.IsInf(f, 1):
		return r.scalar(pyyaml.TagFloat, ".inf", 0, d.Anchor)
	case math.IsInf(f, -1):
		return r.scalar(pyyaml.TagFloat, "-.inf", 0, d.Anchor)
	}
	var value string
	ms := d.MSign
	switch {
	case d.Exp == "" && d.Prec > 0 && d.Prec == d.Width-1:
		value = fmt.Sprintf("%s%d.", ms, absInt(f))
	case d.Exp == "":
		prec := d.Prec
		if prec < 0 {
			value = fmt.Sprintf("%s%0*d", ms, d.Width-len(ms), absInt(f))
		} else {
			value = ms + pyFormatF(math.Abs(f), d.Width-len(ms), d.Width-prec-1)
			if prec == 0 || (prec == 1 && ms != "") {
				value = strings.Replace(value, "0.", ".", 1)
			}
		}
		for len(value) < d.Width {
			value += "0"
		}
	default:
		value = expFloat(d)
	}
	if value == "" {
		value = strings.ToLower(pyjson.ReprFloat(f))
	}
	return r.scalar(pyyaml.TagFloat, value, 0, d.Anchor)
}

func absInt(f float64) int64 { return int64(math.Abs(math.Trunc(f))) }

// pyFormatF is f'{x:0{width}.{prec}f}'.
func pyFormatF(x float64, width, prec int) string {
	if prec < 0 {
		prec = 0
	}
	s := strconv.FormatFloat(x, 'f', prec, 64)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// expFloat is the exponent branch of represent_scalar_float.
func expFloat(d *Float) string {
	data := d.V
	precision := d.Width
	if d.MSign != "" {
		precision++
	}
	full := fmt.Sprintf("%*.*e", d.Width, precision, data)
	m, es, _ := strings.Cut(full, "e")
	m = strings.TrimLeft(m, " ")
	w := d.Width
	if d.Prec <= 0 {
		w = d.Width + 1
	}
	if data < 0 {
		w++
	}
	if len(m) > w {
		m = m[:w]
	}
	e, _ := strconv.Atoi(es)
	m1, m2, _ := strings.Cut(m, ".")
	need := d.Width
	if d.Prec >= 0 {
		need--
	}
	for len(m1)+len(m2) < need {
		m2 += "0"
	}
	if d.MSign != "" && data > 0 {
		m1 = "+" + m1
	}
	esgn := d.ESign
	fmtE := func(e int) string {
		s := strconv.Itoa(abs(e))
		for len(s) < d.EWidth-boolToInt(esgn || e < 0) {
			s = "0" + s
		}
		switch {
		case e < 0:
			s = "-" + s
		case esgn:
			s = "+" + s
		}
		return s
	}
	switch {
	case d.Prec < 0:
		if m2 != "0" {
			e -= len(m2)
		} else {
			m2 = ""
		}
		for len(m1)+len(m2)-boolToInt(d.MSign != "") < d.Width {
			m2 += "0"
			e--
		}
		return m1 + m2 + d.Exp + fmtE(e)
	case d.Prec == 0:
		e -= len(m2)
		return m1 + m2 + "." + d.Exp + fmtE(e)
	}
	if d.MLead0 > 0 {
		m2 = strings.Repeat("0", d.MLead0-1) + m1 + m2
		m1 = "0"
		m2 = m2[:len(m2)-d.MLead0]
		e += d.MLead0
	}
	for len(m1) < d.Prec && m2 != "" {
		m1 += m2[:1]
		m2 = m2[1:]
		e--
	}
	return m1 + "." + m2 + d.Exp + fmtE(e)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// timestamp is RoundTripRepresenter.represent_datetime (TimeStamp) or
// SafeRepresenter.represent_datetime (plain datetime).
func (r *representer) timestamp(ts *TimeStamp) *dnode {
	inter := " "
	if ts.T {
		inter = "T"
	}
	value := fmt.Sprintf("%04d-%02d-%02d%s%02d:%02d:%02d", ts.Y, ts.Mo, ts.D, inter, ts.H, ts.Mi, ts.S)
	if ts.Micro != 0 {
		value += fmt.Sprintf(".%06d", ts.Micro)
	}
	if ts.Offset != nil {
		off := *ts.Offset
		sign := "+"
		if off < 0 {
			sign, off = "-", -off
		}
		value += fmt.Sprintf("%s%02d:%02d", sign, off/60, off%60)
		if !ts.Plain && ts.TZName != "" {
			value = value[:len(value)-6] + ts.TZName
		}
	}
	return r.scalar(pyyaml.TagTS, value, 0, nil)
}

func encodebytes(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(s) > 76 {
		out.WriteString(s[:76] + "\n")
		s = s[76:]
	}
	out.WriteString(s + "\n")
	return out.String()
}

// tagged is represent_tagged_scalar.
func (r *representer) tagged(x *Tagged) *dnode {
	n := r.scalar("", x.Value, x.Style, x.Anchor)
	if x.Tag.Handle == "!!" {
		// f'{data.tag.handle} {data.tag.suffix}' (a str tag)
		n.tag = tagOf("!! " + x.Tag.Suffix)
	} else {
		n.tag = x.Tag
	}
	return n
}

func resetPre(l *pyyaml.PyList) {
	for _, v := range l.V {
		if ct, ok := v.(*pyyaml.CommentToken); ok {
			ct.PreDone = false
		}
	}
}

// containerComment is the shared head of represent_sequence/mapping: the
// container's own comment (copied) with the end comments appended.
func containerComment(ca *CommentAttr) *pyyaml.PyList {
	if ca == nil {
		return nil
	}
	var c *pyyaml.PyList
	if ca.Comment != nil {
		c = ca.Comment.Copy()
	}
	if c.Len() > 0 {
		if pre, ok := c.Get(1).(*pyyaml.PyList); ok {
			resetPre(pre)
		}
	}
	for _, v := range ca.Items {
		if pre, ok := v.Get(1).(*pyyaml.PyList); ok {
			resetPre(pre)
		}
	}
	if c != nil {
		end := pyyaml.NewList()
		for _, ct := range ca.End {
			end.Append(ct)
		}
		c.Append(end)
	}
	return c
}

// sequence is RoundTripRepresenter.represent_sequence.
func (r *representer) sequence(s *Seq, reg func(*dnode)) *dnode {
	tag := tagOf(pyyaml.TagSeq)
	if s.Tag != nil {
		tag = *s.Tag
	}
	n := &dnode{kind: pyyaml.SequenceNode, tag: tag, flow: s.Flow, anchor: s.Anchor}
	reg(n)
	n.comment = containerComment(s.CA)
	best := true
	for idx, it := range s.Items {
		ni := r.data(it)
		if ic, ok := s.CA.item(itemKey(idx)); ok {
			mergeComments(ni, ic)
		}
		if !(ni.kind == pyyaml.ScalarNode && ni.style == 0) {
			best = false
		}
		n.items = append(n.items, ni)
	}
	if n.flow == nil {
		if len(s.Items) != 0 {
			n.flow = boolPtr(false)
		} else {
			n.flow = boolPtr(best)
		}
	}
	return n
}

// mergeComments is RoundTripRepresenter.merge_comments.
func mergeComments(n *dnode, comments *pyyaml.PyList) {
	if comments == nil {
		return
	}
	if n.comment != nil {
		for idx := range comments.V {
			if idx >= n.comment.Len() {
				continue
			}
			if nc := n.comment.Get(idx); nc != nil {
				comments.Set(idx, nc)
			}
		}
	}
	n.comment = comments
}

func (r *representer) key(k any) *dnode {
	switch x := k.(type) {
	case *Seq:
		if x.keySeq {
			return r.sequence(x, func(*dnode) {})
		}
	case *Map:
		if x.keyMap {
			return r.mapping(x, boolPtr(true), func(*dnode) {})
		}
	}
	return r.data(k)
}

// mapping is RoundTripRepresenter.represent_mapping.
func (r *representer) mapping(m *Map, flow *bool, reg func(*dnode)) *dnode {
	tag := tagOf(pyyaml.TagMap)
	if m.Tag != nil && m.Tag.Value != "" {
		tag = *m.Tag
	}
	if m.Flow != nil {
		flow = m.Flow
	}
	n := &dnode{kind: pyyaml.MappingNode, tag: tag, flow: flow, anchor: m.Anchor}
	reg(n)
	n.comment = containerComment(m.CA)
	best := true
	items := m.entries
	if m.Merge != nil && len(m.Merge.Maps) > 0 {
		items = m.nonMergedItems()
	}
	count := 0
	for _, e := range items {
		count++
		nk := r.key(e.key)
		nv := r.data(e.val)
		if ic, ok := m.CA.item(e.hk); ok && ic.Len() > 0 {
			nk.comment = ic.Slice(0, 2)
			if nv.comment != nil {
				nv.comment.Set(0, ic.Get(2))
				nv.comment.Set(1, ic.Get(3))
			} else {
				nv.comment = ic.Slice(2, -1)
			}
		}
		if !(nk.kind == pyyaml.ScalarNode && nk.style == 0) {
			best = false
		}
		if !(nv.kind == pyyaml.ScalarNode && nv.style == 0) {
			best = false
		}
		n.pairs = append(n.pairs, [2]*dnode{nk, nv})
	}
	if n.flow == nil {
		if count != 0 || (m.Merge != nil && len(m.Merge.Maps) > 0) {
			n.flow = boolPtr(false)
		} else {
			n.flow = boolPtr(best)
		}
	}
	if m.Merge != nil && len(m.Merge.Maps) > 0 {
		var arg *dnode
		if m.Merge.Sequence == nil {
			arg = r.data(m.Merge.Maps[0])
		} else {
			arg = r.data(m.Merge.Sequence)
		}
		mk := r.scalar(pyyaml.TagMerge, "<<", 0, nil)
		pos := min(m.Merge.Pos, len(n.pairs))
		n.pairs = append(n.pairs[:pos:pos], append([][2]*dnode{{mk, arg}}, n.pairs[pos:]...)...)
	}
	return n
}

// set is RoundTripRepresenter.represent_set.
func (r *representer) set(m *Map, reg func(*dnode)) *dnode {
	flow := boolPtr(false)
	if m.Flow != nil {
		flow = m.Flow
	}
	n := &dnode{kind: pyyaml.MappingNode, tag: tagOf("tag:yaml.org,2002:set"), flow: flow, anchor: m.Anchor}
	reg(n)
	n.comment = containerComment(m.CA)
	for _, e := range m.entries {
		nk := r.key(e.key)
		nv := r.data(nil)
		if ic, ok := m.CA.item(e.hk); ok && ic.Len() > 0 {
			nk.comment = ic.Slice(0, 2)
		}
		nk.style = '?'
		if *flow {
			nv.style = '-'
		} else {
			nv.style = '?'
		}
		n.pairs = append(n.pairs, [2]*dnode{nk, nv})
	}
	return n
}

// omap is RoundTripRepresenter.represent_omap.
func (r *representer) omap(m *Map, reg func(*dnode)) *dnode {
	n := &dnode{kind: pyyaml.SequenceNode, tag: tagOf("tag:yaml.org,2002:omap"), flow: m.Flow, anchor: m.Anchor}
	reg(n)
	n.comment = containerComment(m.CA)
	for _, e := range m.entries {
		single := NewMap()
		single.Set(e.key, e.val)
		ni := r.mapping(single, nil, func(*dnode) {})
		if ic, ok := m.CA.item(e.hk); ok && ic.Len() > 0 {
			if pyyaml.Truthy(ic.Get(1)) {
				ni.comment = pyyaml.NewList(nil, ic.Get(1))
			}
			ni.pairs[0][0].comment = pyyaml.NewList(ic.Get(0), nil)
			if c := ni.pairs[0][1].comment; c != nil {
				c.Set(0, ic.Get(2))
				c.Set(1, ic.Get(3))
			} else {
				ni.pairs[0][1].comment = ic.Slice(2, -1)
			}
		}
		n.items = append(n.items, ni)
	}
	if n.flow == nil {
		n.flow = boolPtr(false)
	}
	return n
}
