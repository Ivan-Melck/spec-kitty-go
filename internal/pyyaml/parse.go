package pyyaml

// A port of PyYAML 6's parser.py and composer.py: a lazy event state
// machine over the scanner, composed into a node graph.

import "github.com/spec-kitty/go-kitty3/internal/pyjson"

type eventKind int

const (
	eStreamStart eventKind = iota
	eStreamEnd
	eDocumentStart
	eDocumentEnd
	eAlias
	eScalar
	eSequenceStart
	eSequenceEnd
	eMappingStart
	eMappingEnd
)

type event struct {
	kind       eventKind
	start, end *Mark
	anchor     string
	hasAnchor  bool
	tag        string
	hasTag     bool
	implicit   [2]bool // scalar: (plain-implicit, quoted-implicit); collections: [0]
	value      string
	style      rune
	flowStyle  bool
	explicit   bool
}

type stateFn func() *event

type parser struct {
	*scanner
	currentEvent *event
	tagHandles   map[string]string
	states       []stateFn
	marks        []*Mark
	state        stateFn
}

var defaultTags = map[string]string{"!": "!", "!!": "tag:yaml.org,2002:"}

func newParser(src string, d Dialect) *parser {
	p := &parser{scanner: newScanner(src, d)}
	p.state = p.parseStreamStart
	return p
}

func (p *parser) perr(context string, cm *Mark, problem string, pm *Mark) {
	panic(&MarkedYAMLError{Class: "ParserError", Context: context, ContextMark: cm, Problem: problem, ProblemMark: pm, dialect: p.dialect})
}

func (p *parser) checkEvent(kinds ...eventKind) bool {
	if p.currentEvent == nil && p.state != nil {
		p.currentEvent = p.state()
	}
	if p.currentEvent != nil {
		if len(kinds) == 0 {
			return true
		}
		for _, k := range kinds {
			if p.currentEvent.kind == k {
				return true
			}
		}
	}
	return false
}

func (p *parser) peekEvent() *event {
	if p.currentEvent == nil && p.state != nil {
		p.currentEvent = p.state()
	}
	return p.currentEvent
}

func (p *parser) getEvent() *event {
	if p.currentEvent == nil && p.state != nil {
		p.currentEvent = p.state()
	}
	v := p.currentEvent
	p.currentEvent = nil
	return v
}

func (p *parser) popState() stateFn {
	s := p.states[len(p.states)-1]
	p.states = p.states[:len(p.states)-1]
	return s
}

func (p *parser) popMark() {
	p.marks = p.marks[:len(p.marks)-1]
}

func (p *parser) parseStreamStart() *event {
	t := p.getToken()
	p.state = p.parseImplicitDocumentStart
	return &event{kind: eStreamStart, start: t.start, end: t.end}
}

func (p *parser) parseImplicitDocumentStart() *event {
	if !p.checkToken(tDirective, tDocumentStart, tStreamEnd) {
		p.tagHandles = defaultTags
		t := p.peekToken()
		p.states = append(p.states, p.parseDocumentEnd)
		p.state = p.parseBlockNode
		return &event{kind: eDocumentStart, start: t.start, end: t.start}
	}
	return p.parseDocumentStart()
}

func (p *parser) parseDocumentStart() *event {
	for p.checkToken(tDocumentEnd) {
		p.getToken()
	}
	if !p.checkToken(tStreamEnd) {
		start := p.peekToken().start
		p.processDirectives()
		if !p.checkToken(tDocumentStart) {
			p.perr("", nil, "expected '<document start>', but found "+reprS(p.peekToken().id()), p.peekToken().start)
		}
		t := p.getToken()
		p.states = append(p.states, p.parseDocumentEnd)
		p.state = p.parseDocumentContent
		return &event{kind: eDocumentStart, start: start, end: t.end, explicit: true}
	}
	t := p.getToken()
	p.state = nil
	return &event{kind: eStreamEnd, start: t.start, end: t.end}
}

func (p *parser) parseDocumentEnd() *event {
	t := p.peekToken()
	start, end := t.start, t.start
	explicit := false
	if p.checkToken(tDocumentEnd) {
		t = p.getToken()
		end = t.end
		explicit = true
	}
	p.state = p.parseDocumentStart
	return &event{kind: eDocumentEnd, start: start, end: end, explicit: explicit}
}

func (p *parser) parseDocumentContent() *event {
	if p.checkToken(tDirective, tDocumentStart, tDocumentEnd, tStreamEnd) {
		e := p.processEmptyScalar(p.peekToken().start)
		p.state = p.popState()
		return e
	}
	return p.parseBlockNode()
}

func (p *parser) processDirectives() {
	hasVersion := false
	p.tagHandles = map[string]string{}
	for p.checkToken(tDirective) {
		t := p.getToken()
		switch t.value {
		case "YAML":
			if hasVersion {
				p.perr("", nil, "found duplicate YAML directive", t.start)
			}
			if t.major != 1 {
				p.perr("", nil, "found incompatible YAML document (version 1.* is required)", t.start)
			}
			hasVersion = true
		case "TAG":
			if _, ok := p.tagHandles[t.tagHandle]; ok {
				p.perr("", nil, "duplicate tag handle "+reprS(t.tagHandle), t.start)
			}
			p.tagHandles[t.tagHandle] = t.tagPrefix
		}
	}
	for k, v := range defaultTags {
		if _, ok := p.tagHandles[k]; !ok {
			p.tagHandles[k] = v
		}
	}
}

func (p *parser) parseBlockNode() *event { return p.parseNode(true, false) }

func (p *parser) parseFlowNode() *event { return p.parseNode(false, false) }

func (p *parser) parseBlockNodeOrIndentlessSequence() *event { return p.parseNode(true, true) }

func (p *parser) parseNode(block, indentlessSequence bool) *event {
	if p.checkToken(tAlias) {
		t := p.getToken()
		p.state = p.popState()
		return &event{kind: eAlias, anchor: t.value, hasAnchor: true, start: t.start, end: t.end}
	}
	var anchor, tag string
	var hasAnchor, hasTag bool
	var tagT *token
	var start, end, tagMark *Mark
	if p.checkToken(tAnchor) {
		t := p.getToken()
		start, end = t.start, t.end
		anchor, hasAnchor = t.value, true
		if p.checkToken(tTag) {
			t := p.getToken()
			tagMark, end = t.start, t.end
			tagT = t
		}
	} else if p.checkToken(tTag) {
		t := p.getToken()
		start, tagMark = t.start, t.start
		end = t.end
		tagT = t
		if p.checkToken(tAnchor) {
			t := p.getToken()
			end = t.end
			anchor, hasAnchor = t.value, true
		}
	}
	if tagT != nil {
		hasTag = true
		if tagT.hasHandle {
			prefix, ok := p.tagHandles[tagT.tagHandle]
			if !ok {
				p.perr("while parsing a node", start, "found undefined tag handle "+reprS(tagT.tagHandle), tagMark)
			}
			tag = prefix + tagT.suffix
		} else {
			tag = tagT.suffix
		}
	}
	if start == nil {
		start = p.peekToken().start
		end = start
	}
	implicit := !hasTag || tag == "!"
	ev := &event{anchor: anchor, hasAnchor: hasAnchor, tag: tag, hasTag: hasTag, start: start}
	if indentlessSequence && p.checkToken(tBlockEntry) {
		ev.end = p.peekToken().end
		ev.kind = eSequenceStart
		ev.implicit[0] = implicit
		p.state = p.parseIndentlessSequenceEntry
		return ev
	}
	switch {
	case p.checkToken(tScalar):
		t := p.getToken()
		ev.end = t.end
		switch {
		case (t.plain && !hasTag) || tag == "!":
			ev.implicit = [2]bool{true, false}
		case !hasTag:
			ev.implicit = [2]bool{false, true}
		default:
			ev.implicit = [2]bool{false, false}
		}
		ev.kind = eScalar
		ev.value = t.value
		ev.style = t.style
		p.state = p.popState()
	case p.checkToken(tFlowSequenceStart):
		ev.end = p.peekToken().end
		ev.kind = eSequenceStart
		ev.implicit[0] = implicit
		ev.flowStyle = true
		p.state = p.parseFlowSequenceFirstEntry
	case p.checkToken(tFlowMappingStart):
		ev.end = p.peekToken().end
		ev.kind = eMappingStart
		ev.implicit[0] = implicit
		ev.flowStyle = true
		p.state = p.parseFlowMappingFirstKey
	case block && p.checkToken(tBlockSequenceStart):
		ev.end = p.peekToken().start
		ev.kind = eSequenceStart
		ev.implicit[0] = implicit
		p.state = p.parseBlockSequenceFirstEntry
	case block && p.checkToken(tBlockMappingStart):
		ev.end = p.peekToken().start
		ev.kind = eMappingStart
		ev.implicit[0] = implicit
		p.state = p.parseBlockMappingFirstKey
	case hasAnchor || hasTag:
		ev.kind = eScalar
		ev.implicit = [2]bool{implicit, false}
		ev.end = end
		p.state = p.popState()
	default:
		node := "flow"
		if block {
			node = "block"
		}
		t := p.peekToken()
		p.perr("while parsing a "+node+" node", start, "expected the node content, but found "+reprS(t.id()), t.start)
	}
	return ev
}

func (p *parser) parseBlockSequenceFirstEntry() *event {
	t := p.getToken()
	p.marks = append(p.marks, t.start)
	return p.parseBlockSequenceEntry()
}

func (p *parser) parseBlockSequenceEntry() *event {
	if p.checkToken(tBlockEntry) {
		t := p.getToken()
		if !p.checkToken(tBlockEntry, tBlockEnd) {
			p.states = append(p.states, p.parseBlockSequenceEntry)
			return p.parseBlockNode()
		}
		p.state = p.parseBlockSequenceEntry
		return p.processEmptyScalar(t.end)
	}
	if !p.checkToken(tBlockEnd) {
		t := p.peekToken()
		p.perr("while parsing a block collection", p.marks[len(p.marks)-1], "expected <block end>, but found "+reprS(t.id()), t.start)
	}
	t := p.getToken()
	p.state = p.popState()
	p.popMark()
	return &event{kind: eSequenceEnd, start: t.start, end: t.end}
}

func (p *parser) parseIndentlessSequenceEntry() *event {
	if p.checkToken(tBlockEntry) {
		t := p.getToken()
		if !p.checkToken(tBlockEntry, tKey, tValue, tBlockEnd) {
			p.states = append(p.states, p.parseIndentlessSequenceEntry)
			return p.parseBlockNode()
		}
		p.state = p.parseIndentlessSequenceEntry
		return p.processEmptyScalar(t.end)
	}
	t := p.peekToken()
	p.state = p.popState()
	return &event{kind: eSequenceEnd, start: t.start, end: t.start}
}

func (p *parser) parseBlockMappingFirstKey() *event {
	t := p.getToken()
	p.marks = append(p.marks, t.start)
	return p.parseBlockMappingKey()
}

func (p *parser) parseBlockMappingKey() *event {
	if p.checkToken(tKey) {
		t := p.getToken()
		if !p.checkToken(tKey, tValue, tBlockEnd) {
			p.states = append(p.states, p.parseBlockMappingValue)
			return p.parseBlockNodeOrIndentlessSequence()
		}
		p.state = p.parseBlockMappingValue
		return p.processEmptyScalar(t.end)
	}
	if !p.checkToken(tBlockEnd) {
		t := p.peekToken()
		p.perr("while parsing a block mapping", p.marks[len(p.marks)-1], "expected <block end>, but found "+reprS(t.id()), t.start)
	}
	t := p.getToken()
	p.state = p.popState()
	p.popMark()
	return &event{kind: eMappingEnd, start: t.start, end: t.end}
}

func (p *parser) parseBlockMappingValue() *event {
	if p.checkToken(tValue) {
		t := p.getToken()
		if !p.checkToken(tKey, tValue, tBlockEnd) {
			p.states = append(p.states, p.parseBlockMappingKey)
			return p.parseBlockNodeOrIndentlessSequence()
		}
		p.state = p.parseBlockMappingKey
		return p.processEmptyScalar(t.end)
	}
	p.state = p.parseBlockMappingKey
	return p.processEmptyScalar(p.peekToken().start)
}

func (p *parser) parseFlowSequenceFirstEntry() *event {
	t := p.getToken()
	p.marks = append(p.marks, t.start)
	return p.parseFlowSequenceEntry(true)
}

func (p *parser) parseFlowSequenceEntryNext() *event { return p.parseFlowSequenceEntry(false) }

func (p *parser) parseFlowSequenceEntry(first bool) *event {
	if !p.checkToken(tFlowSequenceEnd) {
		if !first {
			if p.checkToken(tFlowEntry) {
				p.getToken()
			} else {
				t := p.peekToken()
				p.perr("while parsing a flow sequence", p.marks[len(p.marks)-1], "expected ',' or ']', but got "+reprS(t.id()), t.start)
			}
		}
		if p.checkToken(tKey) {
			t := p.peekToken()
			p.state = p.parseFlowSequenceEntryMappingKey
			return &event{kind: eMappingStart, start: t.start, end: t.end, implicit: [2]bool{true, false}, flowStyle: true}
		} else if !p.checkToken(tFlowSequenceEnd) {
			p.states = append(p.states, p.parseFlowSequenceEntryNext)
			return p.parseFlowNode()
		}
	}
	t := p.getToken()
	p.state = p.popState()
	p.popMark()
	return &event{kind: eSequenceEnd, start: t.start, end: t.end}
}

func (p *parser) parseFlowSequenceEntryMappingKey() *event {
	t := p.getToken()
	if !p.checkToken(tValue, tFlowEntry, tFlowSequenceEnd) {
		p.states = append(p.states, p.parseFlowSequenceEntryMappingValue)
		return p.parseFlowNode()
	}
	p.state = p.parseFlowSequenceEntryMappingValue
	return p.processEmptyScalar(t.end)
}

func (p *parser) parseFlowSequenceEntryMappingValue() *event {
	if p.checkToken(tValue) {
		t := p.getToken()
		if !p.checkToken(tFlowEntry, tFlowSequenceEnd) {
			p.states = append(p.states, p.parseFlowSequenceEntryMappingEnd)
			return p.parseFlowNode()
		}
		p.state = p.parseFlowSequenceEntryMappingEnd
		return p.processEmptyScalar(t.end)
	}
	p.state = p.parseFlowSequenceEntryMappingEnd
	return p.processEmptyScalar(p.peekToken().start)
}

func (p *parser) parseFlowSequenceEntryMappingEnd() *event {
	p.state = p.parseFlowSequenceEntryNext
	t := p.peekToken()
	return &event{kind: eMappingEnd, start: t.start, end: t.start}
}

func (p *parser) parseFlowMappingFirstKey() *event {
	t := p.getToken()
	p.marks = append(p.marks, t.start)
	return p.parseFlowMappingKey(true)
}

func (p *parser) parseFlowMappingKeyNext() *event { return p.parseFlowMappingKey(false) }

func (p *parser) parseFlowMappingKey(first bool) *event {
	if !p.checkToken(tFlowMappingEnd) {
		if !first {
			if p.checkToken(tFlowEntry) {
				p.getToken()
			} else {
				t := p.peekToken()
				p.perr("while parsing a flow mapping", p.marks[len(p.marks)-1], "expected ',' or '}', but got "+reprS(t.id()), t.start)
			}
		}
		if p.checkToken(tKey) {
			t := p.getToken()
			if !p.checkToken(tValue, tFlowEntry, tFlowMappingEnd) {
				p.states = append(p.states, p.parseFlowMappingValue)
				return p.parseFlowNode()
			}
			p.state = p.parseFlowMappingValue
			return p.processEmptyScalar(t.end)
		} else if !p.checkToken(tFlowMappingEnd) {
			p.states = append(p.states, p.parseFlowMappingEmptyValue)
			return p.parseFlowNode()
		}
	}
	t := p.getToken()
	p.state = p.popState()
	p.popMark()
	return &event{kind: eMappingEnd, start: t.start, end: t.end}
}

func (p *parser) parseFlowMappingValue() *event {
	if p.checkToken(tValue) {
		t := p.getToken()
		if !p.checkToken(tFlowEntry, tFlowMappingEnd) {
			p.states = append(p.states, p.parseFlowMappingKeyNext)
			return p.parseFlowNode()
		}
		p.state = p.parseFlowMappingKeyNext
		return p.processEmptyScalar(t.end)
	}
	p.state = p.parseFlowMappingKeyNext
	return p.processEmptyScalar(p.peekToken().start)
}

func (p *parser) parseFlowMappingEmptyValue() *event {
	p.state = p.parseFlowMappingKeyNext
	return p.processEmptyScalar(p.peekToken().start)
}

func (p *parser) processEmptyScalar(m *Mark) *event {
	return &event{kind: eScalar, implicit: [2]bool{true, false}, start: m, end: m}
}

func reprS(s string) string { return pyjson.ReprString(s) }
