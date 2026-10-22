package rtyaml

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// ---------------------------------------------------------------- events

type evKind int

const (
	evStreamStart evKind = iota
	evStreamEnd
	evDocStart
	evDocEnd
	evAlias
	evScalar
	evSeqStart
	evSeqEnd
	evMapStart
	evMapEnd
)

type eevent struct {
	kind     evKind
	anchor   *string
	tag      *Tag // nil = None
	implicit [3]bool
	implColl bool
	value    string
	style    rune // 0 = None / ""
	flow     *bool
	comment  *pyyaml.PyList
	nrItems  int
}

func (e *eevent) isCollStart() bool { return e.kind == evSeqStart || e.kind == evMapStart }
func (e *eevent) isCollEnd() bool   { return e.kind == evSeqEnd || e.kind == evMapEnd }
func (e *eevent) isNode() bool {
	return e.kind == evAlias || e.kind == evScalar || e.isCollStart()
}

// EmitterError is ruamel.yaml.emitter.EmitterError (and the Python errors a
// dump can raise).
type EmitterError struct{ Msg string }

func (e *EmitterError) Error() string { return e.Msg }

// ---------------------------------------------------------------- serializer

type serializer struct {
	em      *emitter
	nodes   map[*dnode]bool
	anchors map[*dnode]*string
	lastID  int
	v11     bool
}

func (s *serializer) serialize(n *dnode) {
	s.em.emit(&eevent{kind: evDocStart})
	s.anchorNode(n)
	s.node(n)
	s.em.emit(&eevent{kind: evDocEnd})
}

func (s *serializer) anchorNode(n *dnode) {
	if a, ok := s.anchors[n]; ok {
		if a == nil {
			s.anchors[n] = s.generate(n)
		}
		return
	}
	var a *string
	if n.anchor != nil && n.anchor.AlwaysDump {
		v := n.anchor.Value
		a = &v
	}
	s.anchors[n] = a
	switch n.kind {
	case pyyaml.SequenceNode:
		for _, it := range n.items {
			s.anchorNode(it)
		}
	case pyyaml.MappingNode:
		for _, p := range n.pairs {
			s.anchorNode(p[0])
			s.anchorNode(p[1])
		}
	}
}

func (s *serializer) generate(n *dnode) *string {
	if n.anchor != nil && n.anchor.Value != "" {
		v := n.anchor.Value
		return &v
	}
	s.lastID++
	v := fmt.Sprintf("id%03d", s.lastID)
	return &v
}

func (s *serializer) node(n *dnode) {
	alias := s.anchors[n]
	if s.nodes[n] {
		var style rune
		if n.style == '?' {
			style = '?'
		}
		s.em.emit(&eevent{kind: evAlias, anchor: alias, style: style})
		return
	}
	s.nodes[n] = true
	tag := n.tag
	switch n.kind {
	case pyyaml.ScalarNode:
		detected := pyyaml.ResolveRuamel(s.v11, pyyaml.ScalarNode, n.value, [2]bool{true, false})
		def := pyyaml.ResolveRuamel(s.v11, pyyaml.ScalarNode, n.value, [2]bool{false, true})
		s.em.emit(&eevent{kind: evScalar, anchor: alias, tag: &tag,
			implicit: [3]bool{tag.Value == detected, tag.Value == def, strings.HasPrefix(tag.Value, "tag:yaml.org,2002:")},
			value:    n.value, style: n.style, comment: n.comment})
	case pyyaml.SequenceNode, pyyaml.MappingNode:
		start, end := evSeqStart, evSeqEnd
		def := pyyaml.TagSeq
		if n.kind == pyyaml.MappingNode {
			start, end, def = evMapStart, evMapEnd, pyyaml.TagMap
		}
		var head, tail any
		if n.flow != nil && *n.flow && n.comment.Len() > 0 {
			head = n.comment.Get(0)
		}
		if n.comment.Len() > 2 {
			tail = n.comment.Get(2)
		}
		s.em.emit(&eevent{kind: start, anchor: alias, tag: &tag, implColl: tag.Value == def,
			flow: n.flow, comment: n.comment, nrItems: len(n.items) + len(n.pairs)})
		if n.kind == pyyaml.SequenceNode {
			for _, it := range n.items {
				s.node(it)
			}
		} else {
			for _, p := range n.pairs {
				s.node(p[0])
				s.node(p[1])
			}
		}
		s.em.emit(&eevent{kind: end, comment: pyyaml.NewList(head, tail)})
	}
}

// ---------------------------------------------------------------- emitter

const noIndent = -1 // Python None

// styleNone is the emitter's `self.style is None`.
const styleNone rune = -1

type indentEntry struct {
	val int
	seq bool
}

type analysis struct {
	scalar                                           string
	empty, multiline                                 bool
	allowFlowPlain, allowBlockPlain                  bool
	allowSingleQuoted, allowDoubleQuoted, allowBlock bool
}

// emitter is RoundTripEmitter with YAML()'s defaults (indent 2, width 80,
// allow_unicode, no explicit start / end).
type emitter struct {
	out    strings.Builder
	states []func()
	state  func()
	events []*eevent
	event  *eevent

	indents []indentEntry
	indent  int

	flowContext []string

	rootContext, sequenceContext, mappingContext, simpleKeyContext bool

	line, column         int
	whitespace           bool
	indention            bool
	compactSeqSeq        bool
	compactSeqMap        bool
	noNewline            bool
	openEnded            bool
	bestSequenceIndent   int
	bestMapIndent        int
	bestWidth            int
	sequenceDashOffset   int
	tagPrefixes          map[string]string
	preparedAnchor       *string
	preparedTag          *string
	analysis             *analysis
	style                rune
	scalarAfterIndicator bool
	v11                  bool
}

func newEmitter() *emitter {
	e := &emitter{
		indent:               noIndent,
		whitespace:           true,
		indention:            true,
		compactSeqSeq:        true,
		compactSeqMap:        true,
		bestSequenceIndent:   2,
		bestMapIndent:        2,
		bestWidth:            80,
		style:                styleNone,
		scalarAfterIndicator: true,
	}
	e.state = e.expectStreamStart
	return e
}

func (e *emitter) fail(format string, args ...any) {
	panic(&EmitterError{Msg: fmt.Sprintf(format, args...)})
}

func (e *emitter) flowLevel() int { return len(e.flowContext) }

func (e *emitter) emit(ev *eevent) {
	e.events = append(e.events, ev)
	for !e.needMoreEvents() {
		e.event = e.events[0]
		e.events = e.events[1:]
		e.state()
		e.event = nil
	}
}

func (e *emitter) needMoreEvents() bool {
	if len(e.events) == 0 {
		return true
	}
	switch e.events[0].kind {
	case evDocStart:
		return e.needEvents(1)
	case evSeqStart:
		return e.needEvents(2)
	case evMapStart:
		return e.needEvents(3)
	}
	return false
}

func (e *emitter) needEvents(count int) bool {
	level := 0
	for _, ev := range e.events[1:] {
		switch {
		case ev.kind == evDocStart || ev.isCollStart():
			level++
		case ev.kind == evDocEnd || ev.isCollEnd():
			level--
		case ev.kind == evStreamEnd:
			level = -1
		}
		if level < 0 {
			return false
		}
	}
	return len(e.events) < count+1
}

// Indents helpers.
func (e *emitter) seqSeq() bool {
	n := len(e.indents)
	return n >= 2 && e.indents[n-2].seq && e.indents[n-1].seq
}

func (e *emitter) lastSeq() bool {
	n := len(e.indents)
	return n >= 2 && e.indents[n-2].seq
}

func (e *emitter) seqFlowAlign(seqIndent, column int, preComment bool) int {
	n := len(e.indents)
	if n < 2 || !e.indents[n-1].seq {
		if n == 0 || !preComment {
			return 0
		}
	}
	base := e.indents[n-1].val
	if base == noIndent {
		base = 0
	}
	if preComment {
		return base + seqIndent
	}
	return base + seqIndent - column - 1
}

func (e *emitter) popIndent() int {
	v := e.indents[len(e.indents)-1].val
	e.indents = e.indents[:len(e.indents)-1]
	return v
}

func (e *emitter) popState() func() {
	s := e.states[len(e.states)-1]
	e.states = e.states[:len(e.states)-1]
	return s
}

func (e *emitter) increaseIndent(flow, sequence, indentless bool) {
	e.indents = append(e.indents, indentEntry{e.indent, sequence})
	if e.indent == noIndent {
		if flow {
			e.indent = noIndent // requested_indent (None)
		} else {
			e.indent = 0
		}
	} else if !indentless {
		if e.lastSeq() {
			e.indent += e.bestSequenceIndent
		} else {
			e.indent += e.bestMapIndent
		}
	}
}

// ---------------------------------------------------------------- states

func (e *emitter) expectStreamStart() {
	if e.event.kind != evStreamStart {
		e.fail("expected StreamStartEvent")
	}
	e.state = e.expectFirstDocumentStart
}

func (e *emitter) expectNothing() { e.fail("expected nothing") }

func (e *emitter) expectFirstDocumentStart() { e.expectDocumentStart(true) }

func (e *emitter) expectNextDocumentStart() { e.expectDocumentStart(false) }

func (e *emitter) expectDocumentStart(first bool) {
	switch e.event.kind {
	case evDocStart:
		e.tagPrefixes = map[string]string{"!": "!", "tag:yaml.org,2002:": "!!", "!!": "!!"}
		implicit := first && !e.checkEmptyDocument()
		if !implicit {
			e.writeIndent()
			e.writeIndicator("---", true, false, false)
		}
		e.state = e.expectDocumentRoot
	case evStreamEnd:
		if e.openEnded {
			e.writeIndicator("...", true, false, false)
			e.writeIndent()
		}
		e.state = e.expectNothing
	default:
		e.fail("expected DocumentStartEvent")
	}
}

func (e *emitter) expectDocumentEnd() {
	if e.event.kind != evDocEnd {
		e.fail("expected DocumentEndEvent")
	}
	e.writeIndent()
	e.state = e.expectNextDocumentStart
}

func (e *emitter) expectDocumentRoot() {
	e.states = append(e.states, e.expectDocumentEnd)
	e.expectNode(true, false, false, false)
}

func (e *emitter) expectNode(root, sequence, mapping, simpleKey bool) {
	e.rootContext = root
	e.sequenceContext = sequence
	forceFlowIndent := false
	e.mappingContext = mapping
	e.simpleKeyContext = simpleKey
	ev := e.event
	switch {
	case ev.kind == evAlias:
		e.expectAlias()
	case ev.kind == evScalar || ev.isCollStart():
		if e.processAnchor("&") && ev.kind == evScalar && e.sequenceContext {
			e.sequenceContext = false
		}
		if root && ev.kind == evScalar && !e.scalarAfterIndicator {
			e.writeIndent()
		}
		e.processTag()
		switch ev.kind {
		case evScalar:
			e.expectScalar()
		case evSeqStart:
			i2 := e.indention
			flowTrue := ev.flow != nil && *ev.flow
			if ev.comment.Len() > 0 {
				if ev.flow != nil && !*ev.flow {
					if e.writePostComment(ev) {
						e.indention = false
						e.noNewline = true
					}
				}
				column := e.column
				if e.writePreComment(ev) {
					if flowTrue {
						if len(e.indents) == 0 {
							panic(&EmitterError{Msg: "IndexError: list index out of range"})
						}
						forceFlowIndent = !e.indents[len(e.indents)-1].seq
					}
					e.indention = i2
					e.noNewline = !e.indention
				}
				if flowTrue {
					e.column = column
				}
			}
			if e.flowLevel() > 0 || flowTrue || e.checkEmptySequence() {
				e.expectFlowSequence(forceFlowIndent)
			} else {
				e.expectBlockSequence()
			}
			if e.seqSeq() {
				e.indention = true
				e.noNewline = false
			}
		case evMapStart:
			flowTrue := ev.flow != nil && *ev.flow
			if ev.flow != nil && !*ev.flow && ev.comment.Len() > 0 {
				e.writePostComment(ev)
			}
			if ev.comment.Len() > 0 && pyyaml.Truthy(ev.comment.Get(1)) {
				e.writePreComment(ev)
				if flowTrue && len(e.indents) > 0 {
					forceFlowIndent = !e.indents[len(e.indents)-1].seq
				}
			}
			if e.flowLevel() > 0 || flowTrue || e.checkEmptyMapping() {
				e.expectFlowMapping(ev.nrItems == 1, forceFlowIndent)
			} else {
				e.expectBlockMapping()
			}
		}
	default:
		e.fail("expected NodeEvent")
	}
}

func (e *emitter) expectAlias() {
	if e.event.anchor == nil {
		e.fail("anchor is not specified for alias")
	}
	e.processAnchor("*")
	e.state = e.popState()
}

func (e *emitter) expectScalar() {
	e.increaseIndent(true, false, false)
	e.processScalar()
	e.indent = e.popIndent()
	e.state = e.popState()
}

// flow sequences

func (e *emitter) expectFlowSequence(force bool) {
	if force {
		e.increaseIndent(true, true, false)
	}
	ind := e.seqFlowAlign(e.bestSequenceIndent, e.column, force)
	e.writeIndicator(spaces(ind)+"[", true, true, false)
	if !force {
		e.increaseIndent(true, true, false)
	}
	e.flowContext = append(e.flowContext, "[")
	e.state = e.expectFirstFlowSequenceItem
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

func (e *emitter) popFlow() string {
	p := e.flowContext[len(e.flowContext)-1]
	e.flowContext = e.flowContext[:len(e.flowContext)-1]
	return p
}

func (e *emitter) expectFirstFlowSequenceItem() {
	if e.event.kind == evSeqEnd {
		e.indent = e.popIndent()
		e.popFlow()
		e.writeIndicator("]", false, false, false)
		if e.event.comment.Len() > 0 && pyyaml.Truthy(e.event.comment.Get(0)) {
			e.writePostComment(e.event)
		} else if e.flowLevel() == 0 {
			e.writeLineBreak("")
		}
		e.state = e.popState()
		return
	}
	if e.column > e.bestWidth {
		e.writeIndent()
	}
	e.states = append(e.states, e.expectFlowSequenceItem)
	e.expectNode(false, true, false, false)
}

func (e *emitter) expectFlowSequenceItem() {
	if e.event.kind == evSeqEnd {
		e.indent = e.popIndent()
		e.popFlow()
		e.writeIndicator("]", false, false, false)
		if e.event.comment.Len() > 0 && pyyaml.Truthy(e.event.comment.Get(0)) {
			e.writePostComment(e.event)
		} else {
			e.noNewline = false
		}
		e.state = e.popState()
		return
	}
	e.writeIndicator(",", false, false, false)
	if e.column > e.bestWidth {
		e.writeIndent()
	}
	e.states = append(e.states, e.expectFlowSequenceItem)
	e.expectNode(false, true, false, false)
}

// flow mappings

func (e *emitter) expectFlowMapping(single, force bool) {
	if force {
		e.increaseIndent(true, false, false)
	}
	ind := e.seqFlowAlign(e.bestSequenceIndent, e.column, force)
	mapInit := "{"
	if single && e.flowLevel() > 0 && e.flowContext[len(e.flowContext)-1] == "[" {
		mapInit = ""
	}
	e.writeIndicator(spaces(ind)+mapInit, true, true, false)
	e.flowContext = append(e.flowContext, mapInit)
	if !force {
		e.increaseIndent(true, false, false)
	}
	e.state = e.expectFirstFlowMappingKey
}

func (e *emitter) expectFirstFlowMappingKey() {
	if e.event.kind == evMapEnd {
		e.indent = e.popIndent()
		if p := e.popFlow(); p != "{" {
			panic(&EmitterError{Msg: "AssertionError"})
		}
		e.writeIndicator("}", false, false, false)
		if e.event.comment.Len() > 0 && pyyaml.Truthy(e.event.comment.Get(0)) {
			e.writePostComment(e.event)
		} else if e.flowLevel() == 0 {
			e.writeLineBreak("")
		}
		e.state = e.popState()
		return
	}
	if e.column > e.bestWidth {
		e.writeIndent()
	}
	e.flowMappingKey()
}

func (e *emitter) flowMappingKey() {
	if e.checkSimpleKey() {
		e.states = append(e.states, e.expectFlowMappingSimpleValue)
		e.expectNode(false, false, true, true)
	} else {
		e.writeIndicator("?", true, false, false)
		e.states = append(e.states, e.expectFlowMappingValue)
		e.expectNode(false, false, true, false)
	}
}

func (e *emitter) expectFlowMappingKey() {
	if e.event.kind == evMapEnd {
		e.indent = e.popIndent()
		popped := e.popFlow()
		if popped != "" {
			e.writeIndicator("}", false, false, false)
		}
		if e.event.comment.Len() > 0 && pyyaml.Truthy(e.event.comment.Get(0)) {
			e.writePostComment(e.event)
		} else {
			e.noNewline = false
		}
		e.state = e.popState()
		return
	}
	e.writeIndicator(",", false, false, false)
	if e.column > e.bestWidth {
		e.writeIndent()
	}
	e.flowMappingKey()
}

func (e *emitter) expectFlowMappingSimpleValue() {
	// getattr(self.event, 'style', '?') != '-'
	if !(e.event.kind == evScalar || e.event.kind == evAlias) || e.event.style != '-' {
		e.writeIndicator(":", false, false, false)
	}
	e.states = append(e.states, e.expectFlowMappingKey)
	e.expectNode(false, false, true, false)
}

func (e *emitter) expectFlowMappingValue() {
	if e.column > e.bestWidth {
		e.writeIndent()
	}
	e.writeIndicator(":", true, false, false)
	e.states = append(e.states, e.expectFlowMappingKey)
	e.expectNode(false, false, true, false)
}

// block sequences

func (e *emitter) expectBlockSequence() {
	indentless := false
	if e.mappingContext {
		indentless = !e.indention
	} else if !e.compactSeqSeq && e.column != 0 {
		e.writeLineBreak("")
	}
	e.increaseIndent(false, true, indentless)
	e.state = e.expectFirstBlockSequenceItem
}

func (e *emitter) expectFirstBlockSequenceItem() { e.expectBlockSequenceItem(true) }

func (e *emitter) expectNextBlockSequenceItem() { e.expectBlockSequenceItem(false) }

func (e *emitter) expectBlockSequenceItem(first bool) {
	if !first && e.event.kind == evSeqEnd {
		if e.event.comment.Len() > 0 && pyyaml.Truthy(e.event.comment.Get(1)) {
			e.writePreComment(e.event)
		}
		e.indent = e.popIndent()
		e.state = e.popState()
		e.noNewline = false
		return
	}
	if e.event.comment.Len() > 0 && pyyaml.Truthy(e.event.comment.Get(1)) {
		e.writePreComment(e.event)
	}
	nonl := false
	if e.column == 0 {
		nonl = e.noNewline
	}
	e.writeIndent()
	e.writeIndicator(spaces(e.sequenceDashOffset)+"-", true, false, true)
	if nonl || e.sequenceDashOffset+2 > e.bestSequenceIndent {
		e.noNewline = true
	}
	e.states = append(e.states, e.expectNextBlockSequenceItem)
	e.expectNode(false, true, false, false)
}

// block mappings

func (e *emitter) expectBlockMapping() {
	if !e.mappingContext && !(e.compactSeqMap || e.column == 0) {
		e.writeLineBreak("")
	}
	e.increaseIndent(false, false, false)
	e.state = e.expectFirstBlockMappingKey
}

func (e *emitter) expectFirstBlockMappingKey() { e.expectBlockMappingKey(true) }

func (e *emitter) expectNextBlockMappingKey() { e.expectBlockMappingKey(false) }

func (e *emitter) expectBlockMappingKey(first bool) {
	if !first && e.event.kind == evMapEnd {
		if e.event.comment.Len() > 0 && pyyaml.Truthy(e.event.comment.Get(1)) {
			e.writePreComment(e.event)
		}
		e.indent = e.popIndent()
		e.state = e.popState()
		return
	}
	if e.event.comment.Len() > 0 && pyyaml.Truthy(e.event.comment.Get(1)) {
		e.writePreComment(e.event)
	}
	e.writeIndent()
	if e.checkSimpleKey() {
		if !e.event.isCollStart() {
			if e.event.kind != evAlias && e.event.style == '?' {
				e.writeIndicator("?", true, false, true)
			}
		}
		e.states = append(e.states, e.expectBlockMappingSimpleValue)
		ev := e.event
		e.expectNode(false, false, true, true)
		if ev.kind == evAlias && ev.style != '?' {
			e.out.WriteString(" ")
		}
	} else {
		e.writeIndicator("?", true, false, true)
		e.states = append(e.states, e.expectBlockMappingValue)
		e.expectNode(false, false, true, false)
	}
}

func (e *emitter) expectBlockMappingSimpleValue() {
	// getattr(self.event, 'style', None) != '?'
	hasStyle := e.event.kind == evScalar || e.event.kind == evAlias
	if !hasStyle || e.event.style != '?' {
		e.writeIndicator(":", false, false, false)
	}
	e.states = append(e.states, e.expectNextBlockMappingKey)
	e.expectNode(false, false, true, false)
}

func (e *emitter) expectBlockMappingValue() {
	e.writeIndent()
	e.writeIndicator(":", true, false, true)
	e.states = append(e.states, e.expectNextBlockMappingKey)
	e.expectNode(false, false, true, false)
}

// ---------------------------------------------------------------- checkers

func (e *emitter) checkEmptySequence() bool {
	return e.event.kind == evSeqStart && len(e.events) > 0 && e.events[0].kind == evSeqEnd
}

func (e *emitter) checkEmptyMapping() bool {
	return e.event.kind == evMapStart && len(e.events) > 0 && e.events[0].kind == evMapEnd
}

func (e *emitter) checkEmptyDocument() bool {
	if e.event.kind != evDocStart || len(e.events) == 0 {
		return false
	}
	ev := e.events[0]
	return ev.kind == evScalar && ev.anchor == nil && ev.tag == nil && ev.value == ""
}

func (e *emitter) checkSimpleKey() bool {
	length := 0
	ev := e.event
	if ev.isNode() && ev.anchor != nil {
		if e.preparedAnchor == nil {
			a := e.prepareAnchor(*ev.anchor)
			e.preparedAnchor = &a
		}
		length += utf8.RuneCountInString(*e.preparedAnchor)
	}
	if (ev.kind == evScalar || ev.isCollStart()) && ev.tag != nil {
		if e.preparedTag == nil {
			t := e.prepareTag(*ev.tag)
			e.preparedTag = &t
		}
		length += utf8.RuneCountInString(*e.preparedTag)
	}
	if ev.kind == evScalar {
		if e.analysis == nil {
			e.analysis = e.analyzeScalar(ev.value)
		}
		length += utf8.RuneCountInString(e.analysis.scalar)
	}
	if length >= 128 {
		return false
	}
	switch {
	case ev.kind == evAlias:
		return true
	case ev.kind == evSeqStart && ev.flow != nil && *ev.flow:
		return true
	case ev.kind == evMapStart && ev.flow != nil && *ev.flow:
		return true
	case ev.kind == evScalar && !(e.analysis.empty && e.style != styleNone && e.style != 0 && e.style != '\'' && e.style != '"') && !e.analysis.multiline:
		return true
	}
	return e.checkEmptySequence() || e.checkEmptyMapping()
}

// ---------------------------------------------------------------- processors

func (e *emitter) processAnchor(indicator string) bool {
	if e.event.anchor == nil {
		e.preparedAnchor = nil
		return false
	}
	if e.preparedAnchor == nil {
		a := e.prepareAnchor(*e.event.anchor)
		e.preparedAnchor = &a
	}
	if *e.preparedAnchor != "" {
		e.writeIndicator(indicator+*e.preparedAnchor, true, false, false)
		e.noNewline = false
	}
	e.preparedAnchor = nil
	return true
}

func (e *emitter) processTag() {
	ev := e.event
	tag := ev.tag
	if ev.kind == evScalar {
		if e.style == styleNone {
			e.style = e.chooseScalarStyle()
			if ev.value == "" && e.style == '\'' && tag != nil && tag.Value == pyyaml.TagNull {
				ev.value = "null"
				e.analysis = nil
				e.style = e.chooseScalarStyle()
			}
		}
		if (e.style == 0 && ev.implicit[0]) || (e.style != 0 && ev.implicit[1]) {
			e.preparedTag = nil
			return
		}
		if ev.implicit[0] && tag == nil {
			e.preparedTag = nil
			t := Tag{Suffix: "!", Value: "!"}
			tag = &t
		}
	} else if ev.implColl {
		e.preparedTag = nil
		return
	}
	if tag == nil {
		e.fail("tag is not specified")
	}
	if e.preparedTag == nil {
		t := e.prepareTag(*ev.tag)
		e.preparedTag = &t
	}
	if *e.preparedTag != "" {
		e.writeIndicator(*e.preparedTag, true, false, false)
		if e.sequenceContext && e.flowLevel() == 0 && ev.kind == evScalar {
			e.noNewline = true
		}
	}
	e.preparedTag = nil
}

func inStr(r rune, set string) bool { return strings.ContainsRune(set, r) }

func (e *emitter) chooseScalarStyle() rune {
	ev := e.event
	if ev.value == "" && ev.tag != nil && ev.tag.Handle == "!!" {
		return styleNone
	}
	if e.analysis == nil {
		e.analysis = e.analyzeScalar(ev.value)
	}
	a := e.analysis
	if ev.style == '"' {
		return '"'
	}
	if (ev.style == 0 || ev.style == '?' || ev.style == '-') && (ev.implicit[0] || !ev.implicit[2]) {
		if !(e.simpleKeyContext && (a.empty || a.multiline)) &&
			(e.flowLevel() > 0 && a.allowFlowPlain || (e.flowLevel() == 0 && a.allowBlockPlain)) {
			return 0
		}
		if ev.style == '-' {
			return 0
		}
	}
	a.allowBlock = true
	if ev.style == '|' || ev.style == '>' {
		if e.flowLevel() == 0 && !e.simpleKeyContext && a.allowBlock {
			return ev.style
		}
	}
	if ev.style == 0 && a.allowDoubleQuoted {
		if strings.ContainsAny(ev.value, "'\n") {
			return '"'
		}
	}
	if ev.style == 0 || ev.style == '\'' {
		if a.allowSingleQuoted && !(e.simpleKeyContext && a.multiline) {
			return '\''
		}
	}
	return '"'
}

func (e *emitter) processScalar() {
	ev := e.event
	if e.analysis == nil {
		e.analysis = e.analyzeScalar(ev.value)
	}
	if e.style == styleNone {
		e.style = e.chooseScalarStyle()
	}
	split := !e.simpleKeyContext
	if e.sequenceContext && e.flowLevel() == 0 {
		e.writeIndent()
	}
	switch e.style {
	case '"':
		e.writeDoubleQuoted(e.analysis.scalar, split)
	case '\'':
		e.writeSingleQuoted(e.analysis.scalar, split)
	case '>', '|':
		cmx := ""
		if pre, ok := ev.comment.Get(1).(*pyyaml.PyList); ok && pre.Len() > 0 {
			switch c := pre.Get(0).(type) {
			case string:
				cmx = c
			case *pyyaml.CommentToken:
				if c.Raw {
					cmx = c.Value
				}
			}
		}
		if e.style == '>' {
			e.writeFolded(e.analysis.scalar, cmx)
		} else {
			e.writeLiteral(e.analysis.scalar, cmx)
		}
		if ev.comment.Len() > 0 {
			if ct, ok := ev.comment.Get(0).(*pyyaml.CommentToken); ok && ct.Column >= e.indent {
				ct.Column = e.indent - 1
			}
		}
	default:
		e.writePlain(e.analysis.scalar, split)
	}
	e.analysis = nil
	e.style = styleNone
	if ev.comment.Len() > 0 {
		e.writePostComment(ev)
	}
}

func (e *emitter) prepareAnchor(anchor string) string {
	if anchor == "" {
		e.fail("anchor must not be empty")
	}
	for _, ch := range anchor {
		if strings.ContainsRune(",[]{}", ch) || !namespaceChar(ch) {
			e.fail("invalid character %s in the anchor: %s", pyRepr(string(ch)), pyRepr(anchor))
		}
	}
	return anchor
}

func namespaceChar(ch rune) bool {
	switch {
	case ch >= 0x21 && ch <= 0x7e:
		return true
	case ch >= 0xa0 && ch <= 0xd7ff:
		return true
	case ch >= 0xe000 && ch <= 0xfffd && ch != 0xfeff:
		return true
	case ch >= 0x10000 && ch <= 0x10ffff:
		return true
	}
	return false
}

func pyRepr(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		return "\"" + s + "\""
	}
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

// prepareTag is RoundTripEmitter.prepare_tag.
func (e *emitter) prepareTag(ctag Tag) string {
	tag := ctag.Value
	if tag == "" {
		e.fail("tag must not be empty")
	}
	if tag == "!" || tag == "!!" {
		return tag
	}
	handle := ctag.Handle
	suffix := ctag.Suffix
	if handle == "" {
		prefixes := make([]string, 0, len(e.tagPrefixes))
		for p := range e.tagPrefixes {
			prefixes = append(prefixes, p)
		}
		sort.Strings(prefixes)
		for _, prefix := range prefixes {
			if strings.HasPrefix(tag, prefix) && (prefix == "!" || len(prefix) < len(tag)) {
				handle = e.tagPrefixes[prefix]
				if len(prefix) <= len(suffix) {
					suffix = suffix[len(prefix):]
				} else {
					suffix = ""
				}
			}
		}
	}
	if handle != "" {
		return handle + suffix
	}
	return "!<" + suffix + ">"
}

const wsChars = "\x00 \t\r\n\u0085\u2028\u2029"
const breakChars = "\n\u0085\u2028\u2029"

func (e *emitter) analyzeScalar(scalar string) *analysis {
	if scalar == "" {
		return &analysis{scalar: scalar, empty: true, allowBlockPlain: true, allowSingleQuoted: true, allowDoubleQuoted: true}
	}
	rs := []rune(scalar)
	var blockInd, flowInd, lineBreaks, special bool
	var leadingSpace, leadingBreak, trailingSpace, trailingBreak, breakSpace, spaceBreak bool
	if strings.HasPrefix(scalar, "---") || strings.HasPrefix(scalar, "...") {
		blockInd, flowInd = true, true
	}
	preceded := true
	followed := len(rs) == 1 || inStr(rs[1], wsChars)
	prevSpace, prevBreak := false, false
	for index := 0; index < len(rs); {
		ch := rs[index]
		if index == 0 {
			if inStr(ch, "#,[]{}&*!|>'\"%@`") {
				flowInd, blockInd = true, true
			}
			if ch == '?' || ch == ':' {
				if e.v11 {
					flowInd = true
				} else if len(rs) == 1 {
					flowInd = true
				}
				if followed {
					blockInd = true
				}
			}
			if ch == '-' && followed {
				flowInd, blockInd = true, true
			}
		} else {
			if inStr(ch, ",[]{}") {
				flowInd = true
			}
			if ch == '?' && e.v11 {
				flowInd = true
			}
			if ch == ':' && followed {
				flowInd, blockInd = true, true
			}
			if ch == '#' && preceded {
				flowInd, blockInd = true, true
			}
		}
		if inStr(ch, breakChars) {
			lineBreaks = true
		}
		if !(ch == '\n' || (ch >= 0x20 && ch <= 0x7e)) {
			if (ch == 0x85 || (ch >= 0xa0 && ch <= 0xd7ff) || (ch >= 0xe000 && ch <= 0xfffd) || (ch >= 0x10000 && ch <= 0x10ffff)) && ch != 0xfeff {
				// allow_unicode
			} else {
				special = true
			}
		}
		switch {
		case ch == ' ':
			if index == 0 {
				leadingSpace = true
			}
			if index == len(rs)-1 {
				trailingSpace = true
			}
			if prevBreak {
				breakSpace = true
			}
			prevSpace, prevBreak = true, false
		case inStr(ch, breakChars):
			if index == 0 {
				leadingBreak = true
			}
			if index == len(rs)-1 {
				trailingBreak = true
			}
			if prevSpace {
				spaceBreak = true
			}
			prevSpace, prevBreak = false, true
		default:
			prevSpace, prevBreak = false, false
		}
		index++
		preceded = inStr(ch, wsChars)
		followed = index+1 >= len(rs) || inStr(rs[index+1], wsChars)
	}
	a := &analysis{scalar: scalar, multiline: lineBreaks, allowFlowPlain: true, allowBlockPlain: true,
		allowSingleQuoted: true, allowDoubleQuoted: true, allowBlock: true}
	if leadingSpace || leadingBreak || trailingSpace || trailingBreak {
		a.allowFlowPlain, a.allowBlockPlain = false, false
	}
	if trailingSpace {
		a.allowBlock = false
	}
	if breakSpace {
		a.allowFlowPlain, a.allowBlockPlain, a.allowSingleQuoted = false, false, false
	}
	if special {
		a.allowFlowPlain, a.allowBlockPlain, a.allowSingleQuoted, a.allowBlock = false, false, false, false
	} else if spaceBreak {
		a.allowFlowPlain, a.allowBlockPlain, a.allowSingleQuoted = false, false, false
		a.allowBlock = false // allow_space_break is None
	}
	if lineBreaks {
		a.allowFlowPlain, a.allowBlockPlain = false, false
	}
	if flowInd {
		a.allowFlowPlain = false
	}
	if blockInd {
		a.allowBlockPlain = false
	}
	return a
}

// ---------------------------------------------------------------- writers

func (e *emitter) write(s string) {
	e.out.WriteString(s)
}

func (e *emitter) writeIndicator(indicator string, needWhitespace, whitespace, indention bool) {
	data := indicator
	if !e.whitespace && needWhitespace {
		data = " " + indicator
	}
	e.whitespace = whitespace
	e.indention = e.indention && indention
	e.column += utf8.RuneCountInString(data)
	e.openEnded = false
	e.write(data)
}

func (e *emitter) writeIndent() {
	indent := e.indent
	if indent == noIndent {
		indent = 0
	}
	if !e.indention || e.column > indent || (e.column == indent && !e.whitespace) {
		if e.noNewline {
			e.noNewline = false
		} else {
			e.writeLineBreak("")
		}
	}
	if e.column < indent {
		e.whitespace = true
		e.write(strings.Repeat(" ", indent-e.column))
		e.column = indent
	}
}

func (e *emitter) writeLineBreak(data string) {
	if data == "" {
		data = "\n"
	}
	e.whitespace = true
	e.indention = true
	e.line++
	e.column = 0
	e.write(data)
}

func (e *emitter) writeText(data []rune) {
	e.column += len(data)
	e.write(string(data))
}

func (e *emitter) writeSingleQuoted(s string, split bool) {
	text := []rune(s)
	e.writeIndicator("'", true, false, false)
	spaces_, breaks := false, false
	start, end := 0, 0
	for end <= len(text) {
		var ch rune = -1
		if end < len(text) {
			ch = text[end]
		}
		switch {
		case spaces_:
			if ch == -1 || ch != ' ' {
				if start+1 == end && e.column > e.bestWidth && split && start != 0 && end != len(text) {
					e.writeIndent()
				} else {
					e.writeText(text[start:end])
				}
				start = end
			}
		case breaks:
			if ch == -1 || !inStr(ch, breakChars) {
				if text[start] == '\n' {
					e.writeLineBreak("")
				}
				for _, br := range text[start:end] {
					if br == '\n' {
						e.writeLineBreak("")
					} else {
						e.writeLineBreak(string(br))
					}
				}
				e.writeIndent()
				start = end
			}
		default:
			if ch == -1 || inStr(ch, " "+breakChars) || ch == '\'' {
				if start < end {
					e.writeText(text[start:end])
					start = end
				}
			}
		}
		if ch == '\'' {
			e.column += 2
			e.write("''")
			start = end + 1
		}
		if ch != -1 {
			spaces_ = ch == ' '
			breaks = inStr(ch, breakChars)
		}
		end++
	}
	e.writeIndicator("'", false, false, false)
}

var escapeReplacements = map[rune]string{
	0: "0", 7: "a", 8: "b", 9: "t", 10: "n", 11: "v", 12: "f", 13: "r", 0x1b: "e",
	'"': "\"", '\\': "\\", 0x85: "N", 0xa0: "_", 0x2028: "L", 0x2029: "P",
}

func (e *emitter) writeDoubleQuoted(s string, split bool) {
	text := []rune(s)
	e.writeIndicator("\"", true, false, false)
	start, end := 0, 0
	for end <= len(text) {
		var ch rune = -1
		if end < len(text) {
			ch = text[end]
		}
		if ch == -1 || inStr(ch, "\"\\\u0085\u2028\u2029\ufeff") ||
			!((ch >= 0x20 && ch <= 0x7e) || (ch >= 0xa0 && ch <= 0xd7ff) || (ch >= 0xe000 && ch <= 0xfffd) || (ch >= 0x10000 && ch <= 0x10ffff)) {
			if start < end {
				e.writeText(text[start:end])
				start = end
			}
			if ch != -1 {
				var data string
				if r, ok := escapeReplacements[ch]; ok {
					data = "\\" + r
				} else if ch <= 0xff {
					data = fmt.Sprintf("\\x%02X", ch)
				} else if ch <= 0xffff {
					data = fmt.Sprintf("\\u%04X", ch)
				} else {
					data = fmt.Sprintf("\\U%08X", ch)
				}
				e.column += len(data)
				e.write(data)
				start = end + 1
			}
		}
		if 0 < end && end < len(text)-1 && (ch == ' ' || start >= end) && e.column+(end-start) > e.bestWidth && split {
			needBackslash := true
			if len(text) > end {
				func() {
					defer func() { _ = recover() }()
					spacePos := indexRune(text, ' ', end, len(text))
					if spacePos < 0 {
						return // ValueError
					}
					if nl := indexRune(text, '\n', end, spacePos); nl >= 0 {
						spacePos = nl
					}
					if text[spacePos] == '\n' && text[spacePos+1] != ' ' {
						// pass
					} else if !containsAny(text[end:spacePos], "\"'") &&
						!inStr(text[spacePos+1], " \n") &&
						!(end-1 >= 0 && end+1 <= len(text) && string(text[end-1:end+1]) == "  ") &&
						start != end {
						needBackslash = false
					}
				}()
			}
			data := string(text[start:end])
			if needBackslash {
				data += "\\"
			}
			if start < end {
				start = end
			}
			e.column += utf8.RuneCountInString(data)
			e.write(data)
			e.writeIndent()
			e.whitespace = false
			e.indention = false
			if text[start] == ' ' {
				if !needBackslash {
					start++
				}
				if needBackslash {
					e.column++
					e.write("\\")
				}
			}
		}
		end++
	}
	e.writeIndicator("\"", false, false, false)
}

func indexRune(text []rune, r rune, from, to int) int {
	if to > len(text) {
		to = len(text)
	}
	for i := from; i < to; i++ {
		if text[i] == r {
			return i
		}
	}
	return -1
}

func containsAny(text []rune, set string) bool {
	for _, r := range text {
		if inStr(r, set) {
			return true
		}
	}
	return false
}

func (e *emitter) determineBlockHints(text []rune) (string, int, string) {
	indent := 0
	indicator := ""
	hints := ""
	if len(text) > 0 {
		if inStr(text[0], " "+breakChars) {
			indent = 2
			hints += "2"
		} else if e.rootContext {
			s := string(text)
			position := -1
			for _, end := range []string{"---", "..."} {
				position = -1
				pos := 0
				for position == -1 {
					k := runeFind(text, end, pos)
					if k == -1 {
						break
					}
					pos = k
					if pos != 0 && text[pos-1] != '\n' {
						pos++
						continue
					}
					if pos+3 < len(text) && inStr(text[pos+3], " \r\n") {
						position = pos
						break
					}
					pos++
				}
				if position != -1 {
					break
				}
			}
			_ = s
			if position != -1 {
				indent = 2
			}
		}
		last := text[len(text)-1]
		if !inStr(last, breakChars) {
			indicator = "-"
		} else if len(text) == 1 || inStr(text[len(text)-2], breakChars) {
			indicator = "+"
		}
	}
	hints += indicator
	return hints, indent, indicator
}

func runeFind(text []rune, sub string, from int) int {
	sr := []rune(sub)
	for i := from; i+len(sr) <= len(text); i++ {
		if string(text[i:i+len(sr)]) == sub {
			return i
		}
	}
	return -1
}

func (e *emitter) writeFolded(s string, comment string) {
	text := []rune(s)
	hints, _, indicator := e.determineBlockHints(text)
	e.writeIndicator(">"+hints+comment, true, false, false)
	if indicator == "+" {
		e.openEnded = true
	}
	e.writeLineBreak("")
	leadingSpace := true
	spaces_ := false
	breaks := true
	start, end := 0, 0
	for end <= len(text) {
		var ch rune = -1
		if end < len(text) {
			ch = text[end]
		}
		switch {
		case breaks:
			if ch == -1 || !inStr(ch, breakChars+"\a") {
				if !leadingSpace && ch != -1 && ch != ' ' && text[start] == '\n' {
					e.writeLineBreak("")
				}
				leadingSpace = ch == ' '
				for _, br := range text[start:end] {
					if br == '\n' {
						e.writeLineBreak("")
					} else {
						e.writeLineBreak(string(br))
					}
				}
				if ch != -1 {
					e.writeIndent()
				}
				start = end
			}
		case spaces_:
			if ch != ' ' {
				if start+1 == end && e.column > e.bestWidth {
					e.writeIndent()
				} else {
					e.writeText(text[start:end])
				}
				start = end
			}
		default:
			if ch == -1 || inStr(ch, " "+breakChars+"\a") {
				e.writeText(text[start:end])
				if ch == '\a' {
					if end < len(text)-1 && end+2 < len(text) && !isSpace(text[end+2]) {
						e.writeLineBreak("")
						e.writeIndent()
						end += 2
					} else {
						e.fail("unexcpected fold indicator \\a before space")
					}
				}
				if ch == -1 {
					e.writeLineBreak("")
				}
				start = end
			}
		}
		if ch != -1 {
			breaks = inStr(ch, breakChars)
			spaces_ = ch == ' '
		}
		end++
	}
}

func (e *emitter) writeLiteral(s string, comment string) {
	text := []rune(s)
	hints, bindent, indicator := e.determineBlockHints(text)
	e.writeIndicator("|"+hints+comment, true, false, false)
	if indicator == "+" {
		e.openEnded = true
	}
	e.writeLineBreak("")
	breaks := true
	start, end := 0, 0
	for end <= len(text) {
		var ch rune = -1
		if end < len(text) {
			ch = text[end]
		}
		if breaks {
			if ch == -1 || !inStr(ch, breakChars) {
				for _, br := range text[start:end] {
					if br == '\n' {
						e.writeLineBreak("")
					} else {
						e.writeLineBreak(string(br))
					}
				}
				if ch != -1 {
					if e.rootContext {
						idnx := e.indent
						if idnx == noIndent {
							idnx = 0
						}
						e.write(strings.Repeat(" ", bindent+idnx))
					} else {
						e.writeIndent()
					}
				}
				start = end
			}
		} else {
			if ch == -1 || inStr(ch, breakChars) {
				e.write(string(text[start:end]))
				if ch == -1 {
					e.writeLineBreak("")
				}
				start = end
			}
		}
		if ch != -1 {
			breaks = inStr(ch, breakChars)
		}
		end++
	}
}

func (e *emitter) writePlain(s string, split bool) {
	if e.rootContext {
		e.openEnded = true
	}
	if s == "" {
		return
	}
	text := []rune(s)
	if !e.whitespace {
		e.column++
		e.write(" ")
	}
	e.whitespace = false
	e.indention = false
	spaces_, breaks := false, false
	start, end := 0, 0
	for end <= len(text) {
		var ch rune = -1
		if end < len(text) {
			ch = text[end]
		}
		switch {
		case spaces_:
			if ch != ' ' {
				if start+1 == end && e.column >= e.bestWidth && split {
					e.writeIndent()
					e.whitespace = false
					e.indention = false
				} else {
					e.writeText(text[start:end])
				}
				start = end
			}
		case breaks:
			if !inStr(ch, breakChars) {
				if text[start] == '\n' {
					e.writeLineBreak("")
				}
				for _, br := range text[start:end] {
					if br == '\n' {
						e.writeLineBreak("")
					} else {
						e.writeLineBreak(string(br))
					}
				}
				e.writeIndent()
				e.whitespace = false
				e.indention = false
				start = end
			}
		default:
			if ch == -1 || inStr(ch, " "+breakChars) {
				data := text[start:end]
				if len(data)+e.column > e.bestWidth && e.indent != noIndent && e.column > e.indent {
					e.writeIndent()
				}
				e.writeText(data)
				start = end
			}
		}
		if ch != -1 {
			spaces_ = ch == ' '
			breaks = inStr(ch, breakChars)
		}
		end++
	}
}

// ---------------------------------------------------------------- comments

func (e *emitter) writeComment(c *pyyaml.CommentToken, pre bool) {
	value := c.Value
	if !pre && strings.HasSuffix(value, "\n") {
		value = value[:len(value)-1]
	}
	col := c.Column
	if strings.HasPrefix(c.Value, "\n") {
		col = e.column
	}
	nr := col - e.column
	if e.column != 0 && strings.TrimSpace(value) != "" && nr < 1 && !strings.HasPrefix(value, "\n") {
		nr = 1
	}
	e.write(spaces(nr) + value)
	if !pre {
		e.writeLineBreak("")
	}
}

func (e *emitter) writePreComment(ev *eevent) bool {
	if ev.comment == nil {
		return false
	}
	comments, ok := ev.comment.Get(1).(*pyyaml.PyList)
	if !ok || comments == nil {
		if ev.comment.Get(1) == nil {
			return false
		}
		panic(&EmitterError{Msg: "TypeError: object is not iterable"})
	}
	start := ev.isCollStart()
	for _, v := range comments.V {
		ct, ok := v.(*pyyaml.CommentToken)
		if !ok || ct.Raw {
			panic(&EmitterError{Msg: "AttributeError: 'str' object has no attribute 'value'"})
		}
		if start && ct.PreDone {
			continue
		}
		if e.column != 0 {
			e.writeLineBreak("")
		}
		e.writeComment(ct, true)
		if start {
			ct.PreDone = true
		}
	}
	return true
}

func (e *emitter) writePostComment(ev *eevent) bool {
	c := e.event.comment.Get(0)
	if c == nil {
		return false
	}
	ct, ok := ev.comment.Get(0).(*pyyaml.CommentToken)
	if !ok {
		panic(&EmitterError{Msg: "AttributeError: 'list' object has no attribute 'value'"})
	}
	e.writeComment(ct, false)
	return true
}
