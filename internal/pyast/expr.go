package pyast

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// expressions (CPython grammar, field-ordered children)

func (p *parser) starExprListOrYield() *Node {
	if p.isKw("yield") {
		return p.yieldExpr()
	}
	return p.starExprList()
}

func (p *parser) yieldExpr() *Node {
	t := p.cur()
	p.pos++
	n := &Node{Kind: "Yield", Line: t.line}
	if p.acceptKw("from") {
		n.Kind = "YieldFrom"
		n.add(p.test())
		return n
	}
	if !p.atExprEnd() {
		n.add(p.starExprList())
	}
	return n
}

func (p *parser) atExprEnd() bool {
	t := p.cur()
	if t.kind == tNewline || t.kind == tEOF {
		return true
	}
	return t.kind == tOp && (t.val == ")" || t.val == "]" || t.val == "}" || t.val == ";" || t.val == "=" || t.val == ":")
}

// starExprList parses `a, *b, c` (a bare tuple when there is a comma).
func (p *parser) starExprList() *Node {
	line := p.cur().line
	first := p.starOrNamed()
	if !p.isOp(",") {
		return first
	}
	t := &Node{Kind: "Tuple", Line: line, Elts: []*Node{first}}
	for p.acceptOp(",") {
		if p.atTupleEnd() {
			break
		}
		t.Elts = append(t.Elts, p.starOrNamed())
	}
	t.add(t.Elts...)
	return t
}

func (p *parser) atTupleEnd() bool {
	if p.atExprEnd() {
		return true
	}
	t := p.cur()
	return t.kind == tName && (t.val == "in" || t.val == "for" || t.val == "if" || t.val == "async")
}

func (p *parser) starOrNamed() *Node {
	if p.isOp("*") {
		t := p.cur()
		p.pos++
		n := &Node{Kind: "Starred", Line: t.line}
		n.add(p.expr())
		return n
	}
	return p.namedExpr()
}

// targetList parses for-loop targets (no `in` comparisons).
func (p *parser) targetList() *Node {
	line := p.cur().line
	first := p.target()
	if !p.isOp(",") {
		return first
	}
	t := &Node{Kind: "Tuple", Line: line, Elts: []*Node{first}}
	for p.acceptOp(",") {
		if p.isKw("in") || p.isOp("=") {
			break
		}
		t.Elts = append(t.Elts, p.target())
	}
	t.add(t.Elts...)
	return t
}

func (p *parser) target() *Node {
	if p.isOp("*") {
		t := p.cur()
		p.pos++
		n := &Node{Kind: "Starred", Line: t.line}
		n.add(p.target())
		return n
	}
	return p.bitOr()
}

func (p *parser) namedExpr() *Node {
	if p.cur().kind == tName && p.peek(1).kind == tOp && p.peek(1).val == ":=" {
		name := p.cur()
		p.pos += 2
		n := &Node{Kind: "NamedExpr", Line: name.line}
		n.add(&Node{Kind: "Name", Line: name.line, Name: name.val}, p.test())
		return n
	}
	return p.test()
}

func (p *parser) test() *Node {
	if p.isKw("lambda") {
		return p.lambda()
	}
	line := p.cur().line
	body := p.orTest()
	if p.isKw("if") {
		// conditional expression (not a comprehension `if`, which callers stop on)
		save := p.pos
		p.pos++
		cond := p.orTest()
		if !p.acceptKw("else") {
			p.pos = save
			return body
		}
		orelse := p.test()
		n := &Node{Kind: "IfExp", Line: line}
		n.add(cond, body, orelse)
		return n
	}
	return body
}

func (p *parser) testNoCond() *Node {
	if p.isKw("lambda") {
		return p.lambda()
	}
	return p.orTest()
}

func (p *parser) lambda() *Node {
	t := p.cur()
	p.pos++
	n := &Node{Kind: "Lambda", Line: t.line}
	args := p.parameters(":")
	p.expectOp(":")
	n.add(args, p.test())
	return n
}

func (p *parser) orTest() *Node {
	line := p.cur().line
	first := p.andTest()
	if !p.isKw("or") {
		return first
	}
	n := &Node{Kind: "BoolOp", Line: line}
	n.add(first)
	for p.acceptKw("or") {
		n.add(p.andTest())
	}
	return n
}

func (p *parser) andTest() *Node {
	line := p.cur().line
	first := p.notTest()
	if !p.isKw("and") {
		return first
	}
	n := &Node{Kind: "BoolOp", Line: line}
	n.add(first)
	for p.acceptKw("and") {
		n.add(p.notTest())
	}
	return n
}

func (p *parser) notTest() *Node {
	if p.isKw("not") {
		t := p.cur()
		p.pos++
		n := &Node{Kind: "UnaryOp", Line: t.line}
		n.add(p.notTest())
		return n
	}
	return p.comparison()
}

func (p *parser) compOp() string {
	t := p.cur()
	if t.kind == tOp {
		switch t.val {
		case "<":
			return "Lt"
		case ">":
			return "Gt"
		case "==":
			return "Eq"
		case ">=":
			return "GtE"
		case "<=":
			return "LtE"
		case "!=":
			return "NotEq"
		}
		return ""
	}
	if t.kind == tName {
		switch t.val {
		case "in":
			return "In"
		case "not":
			if nx := p.peek(1); nx.kind == tName && nx.val == "in" {
				return "NotIn"
			}
		case "is":
			if nx := p.peek(1); nx.kind == tName && nx.val == "not" {
				return "IsNot"
			}
			return "Is"
		}
	}
	return ""
}

func (p *parser) comparison() *Node {
	line := p.cur().line
	left := p.bitOr()
	op := p.compOp()
	if op == "" {
		return left
	}
	n := &Node{Kind: "Compare", Line: line, Left: left}
	n.add(left)
	for op != "" {
		if op == "NotIn" || op == "IsNot" {
			p.pos += 2
		} else {
			p.pos++
		}
		right := p.bitOr()
		n.Ops = append(n.Ops, op)
		n.Comparators = append(n.Comparators, right)
		op = p.compOp()
	}
	n.add(n.Comparators...)
	return n
}

func (p *parser) binary(next func() *Node, ops ...string) *Node {
	line := p.cur().line
	left := next()
	for {
		t := p.cur()
		matched := false
		if t.kind == tOp {
			for _, o := range ops {
				if t.val == o {
					matched = true
					break
				}
			}
		}
		if !matched {
			return left
		}
		p.pos++
		right := next()
		n := &Node{Kind: "BinOp", Line: line}
		n.add(left, right)
		left = n
	}
}

func (p *parser) expr() *Node  { return p.bitOr() }
func (p *parser) bitOr() *Node { return p.binary(p.bitXor, "|") }
func (p *parser) bitXor() *Node {
	return p.binary(p.bitAnd, "^")
}
func (p *parser) bitAnd() *Node { return p.binary(p.shift, "&") }
func (p *parser) shift() *Node  { return p.binary(p.arith, "<<", ">>") }
func (p *parser) arith() *Node  { return p.binary(p.term, "+", "-") }
func (p *parser) term() *Node   { return p.binary(p.factor, "*", "/", "//", "%", "@") }

func (p *parser) factor() *Node {
	t := p.cur()
	if t.kind == tOp && (t.val == "+" || t.val == "-" || t.val == "~") {
		p.pos++
		n := &Node{Kind: "UnaryOp", Line: t.line}
		n.add(p.factor())
		return n
	}
	return p.power()
}

func (p *parser) power() *Node {
	line := p.cur().line
	base := p.awaitPrimary()
	if p.acceptOp("**") {
		n := &Node{Kind: "BinOp", Line: line}
		n.add(base, p.factor())
		return n
	}
	return base
}

func (p *parser) awaitPrimary() *Node {
	if p.isKw("await") {
		t := p.cur()
		p.pos++
		n := &Node{Kind: "Await", Line: t.line}
		n.add(p.primary())
		return n
	}
	return p.primary()
}

func (p *parser) primary() *Node {
	line := p.cur().line
	n := p.atom()
	for {
		switch {
		case p.isOp("."):
			p.pos++
			name := p.expectName()
			a := &Node{Kind: "Attribute", Line: line, Name: name.val, Value: n}
			a.add(n)
			n = a
		case p.isOp("("):
			p.pos++
			args, kws := p.arglist(")")
			p.expectOp(")")
			c := &Node{Kind: "Call", Line: line, Func: n, Args: args, Keywords: kws}
			c.add(n)
			c.add(args...)
			c.add(kws...)
			n = c
		case p.isOp("["):
			p.pos++
			s := &Node{Kind: "Subscript", Line: line}
			s.add(n, p.subscriptList())
			p.expectOp("]")
			n = s
		default:
			return n
		}
	}
}

func (p *parser) subscriptList() *Node {
	line := p.cur().line
	first := p.subscript()
	if !p.isOp(",") {
		return first
	}
	t := &Node{Kind: "Tuple", Line: line, Elts: []*Node{first}}
	for p.acceptOp(",") {
		if p.isOp("]") {
			break
		}
		t.Elts = append(t.Elts, p.subscript())
	}
	t.add(t.Elts...)
	return t
}

func (p *parser) subscript() *Node {
	line := p.cur().line
	var lower *Node
	if !p.isOp(":") {
		if p.isOp("*") {
			lower = p.starOrNamed()
		} else {
			lower = p.namedExpr()
		}
		if !p.isOp(":") {
			return lower
		}
	}
	n := &Node{Kind: "Slice", Line: line}
	p.expectOp(":")
	n.add(lower)
	if !p.isOp(":") && !p.isOp("]") && !p.isOp(",") {
		n.add(p.test())
	}
	if p.acceptOp(":") {
		if !p.isOp("]") && !p.isOp(",") {
			n.add(p.test())
		}
	}
	return n
}

// arglist parses call arguments up to end: positional args and keywords
// (keyword nodes carry Name=arg and Value).
func (p *parser) arglist(end string) ([]*Node, []*Node) {
	var args, kws []*Node
	for !p.isOp(end) {
		t := p.cur()
		switch {
		case p.isOp("**"):
			p.pos++
			k := &Node{Kind: "keyword", Line: t.line}
			k.Value = p.test()
			k.add(k.Value)
			kws = append(kws, k)
		case p.isOp("*"):
			p.pos++
			s := &Node{Kind: "Starred", Line: t.line}
			s.add(p.test())
			args = append(args, s)
		case t.kind == tName && p.peek(1).kind == tOp && p.peek(1).val == "=":
			p.pos += 2
			k := &Node{Kind: "keyword", Line: t.line, Name: t.val}
			k.Value = p.test()
			k.add(k.Value)
			kws = append(kws, k)
		default:
			a := p.namedExpr()
			if p.isKw("for") || p.isKw("async") {
				g := &Node{Kind: "GeneratorExp", Line: a.Line}
				g.add(a)
				g.add(p.comprehensions()...)
				a = g
			}
			args = append(args, a)
		}
		if !p.acceptOp(",") {
			break
		}
	}
	return args, kws
}

func (p *parser) comprehensions() []*Node {
	var out []*Node
	for p.isKw("for") || (p.isKw("async") && p.peek(1).val == "for") {
		c := &Node{Kind: "comprehension", Line: p.cur().line}
		if p.acceptKw("async") {
		}
		p.pos++ // for
		target := p.targetList()
		if !p.acceptKw("in") {
			p.fail("expected 'in' in comprehension")
		}
		iter := p.orTest()
		c.add(target, iter)
		for p.isKw("if") {
			p.pos++
			c.add(p.testNoCond())
		}
		out = append(out, c)
	}
	return out
}

func (p *parser) atom() *Node {
	t := p.cur()
	switch t.kind {
	case tName:
		switch t.val {
		case "None", "True", "False":
			p.pos++
			return &Node{Kind: "Constant", Line: t.line}
		}
		if keywords[t.val] {
			p.fail(fmt.Sprintf("invalid syntax: unexpected keyword %q", t.val))
		}
		p.pos++
		return &Node{Kind: "Name", Line: t.line, Name: t.val}
	case tNumber:
		p.pos++
		return &Node{Kind: "Constant", Line: t.line}
	case tString:
		p.pos++
		return p.stringAtom(t)
	case tOp:
		switch t.val {
		case "...":
			p.pos++
			return &Node{Kind: "Constant", Line: t.line}
		case "(":
			p.pos++
			if p.acceptOp(")") {
				return &Node{Kind: "Tuple", Line: t.line}
			}
			if p.isKw("yield") {
				y := p.yieldExpr()
				p.expectOp(")")
				return y
			}
			first := p.starOrNamed()
			if p.isKw("for") || p.isKw("async") {
				g := &Node{Kind: "GeneratorExp", Line: t.line}
				g.add(first)
				g.add(p.comprehensions()...)
				p.expectOp(")")
				return g
			}
			if !p.isOp(",") {
				p.expectOp(")")
				return first
			}
			tup := &Node{Kind: "Tuple", Line: t.line, Elts: []*Node{first}}
			for p.acceptOp(",") {
				if p.isOp(")") {
					break
				}
				tup.Elts = append(tup.Elts, p.starOrNamed())
			}
			p.expectOp(")")
			tup.add(tup.Elts...)
			return tup
		case "[":
			p.pos++
			l := &Node{Kind: "List", Line: t.line}
			if p.acceptOp("]") {
				return l
			}
			first := p.starOrNamed()
			if p.isKw("for") || p.isKw("async") {
				c := &Node{Kind: "ListComp", Line: t.line}
				c.add(first)
				c.add(p.comprehensions()...)
				p.expectOp("]")
				return c
			}
			l.Elts = append(l.Elts, first)
			for p.acceptOp(",") {
				if p.isOp("]") {
					break
				}
				l.Elts = append(l.Elts, p.starOrNamed())
			}
			p.expectOp("]")
			l.add(l.Elts...)
			return l
		case "{":
			p.pos++
			return p.dictOrSet(t)
		}
	}
	p.fail(fmt.Sprintf("invalid syntax near %q", t.val))
	return nil
}

func (p *parser) dictOrSet(open token) *Node {
	if p.acceptOp("}") {
		return &Node{Kind: "Dict", Line: open.line}
	}
	// first element
	if p.isOp("**") {
		return p.dictRest(open, nil)
	}
	first := p.starOrNamed()
	if p.acceptOp(":") {
		val := p.test()
		if p.isKw("for") || p.isKw("async") {
			c := &Node{Kind: "DictComp", Line: open.line}
			c.add(first, val)
			c.add(p.comprehensions()...)
			p.expectOp("}")
			return c
		}
		return p.dictRest(open, [][2]*Node{{first, val}})
	}
	if p.isKw("for") || p.isKw("async") {
		c := &Node{Kind: "SetComp", Line: open.line}
		c.add(first)
		c.add(p.comprehensions()...)
		p.expectOp("}")
		return c
	}
	s := &Node{Kind: "Set", Line: open.line, Elts: []*Node{first}}
	for p.acceptOp(",") {
		if p.isOp("}") {
			break
		}
		s.Elts = append(s.Elts, p.starOrNamed())
	}
	p.expectOp("}")
	s.add(s.Elts...)
	return s
}

func (p *parser) dictRest(open token, pairs [][2]*Node) *Node {
	d := &Node{Kind: "Dict", Line: open.line}
	first := len(pairs) == 0
	for {
		if !first {
			if !p.acceptOp(",") || p.isOp("}") {
				break
			}
		}
		first = false
		if p.acceptOp("**") {
			pairs = append(pairs, [2]*Node{nil, p.bitOr()})
			continue
		}
		k := p.test()
		p.expectOp(":")
		pairs = append(pairs, [2]*Node{k, p.test()})
	}
	p.expectOp("}")
	for _, kv := range pairs {
		d.add(kv[0])
	}
	for _, kv := range pairs {
		d.add(kv[1])
	}
	return d
}

// ---------------------------------------------------------------------------
// string literals

// stringAtom consumes adjacent string tokens (t already consumed) into a
// Constant or JoinedStr node.
func (p *parser) stringAtom(t token) *Node {
	parts := []token{t}
	for p.cur().kind == tString {
		parts = append(parts, p.cur())
		p.pos++
	}
	anyF, anyBytes := false, false
	for _, s := range parts {
		pre, _ := stringPrefix(s.val)
		if strings.ContainsAny(pre, "fF") {
			anyF = true
		}
		if strings.ContainsAny(pre, "bB") {
			anyBytes = true
		}
	}
	if anyBytes {
		return &Node{Kind: "Constant", Line: t.line}
	}
	if !anyF {
		var b strings.Builder
		for _, s := range parts {
			v, err := decodeString(s.val)
			if err != nil {
				panic(&SyntaxError{Msg: err.Error(), Line: s.line})
			}
			b.WriteString(v)
		}
		return &Node{Kind: "Constant", Line: t.line, IsStr: true, Str: b.String()}
	}
	js := &Node{Kind: "JoinedStr", Line: t.line}
	var pending strings.Builder
	pendingLine := 0
	flush := func() {
		if pending.Len() > 0 {
			js.add(&Node{Kind: "Constant", Line: pendingLine, IsStr: true, Str: pending.String()})
			pending.Reset()
		}
	}
	for _, s := range parts {
		pre, _ := stringPrefix(s.val)
		if !strings.ContainsAny(pre, "fF") {
			v, err := decodeString(s.val)
			if err != nil {
				panic(&SyntaxError{Msg: err.Error(), Line: s.line})
			}
			if pending.Len() == 0 {
				pendingLine = s.line
			}
			pending.WriteString(v)
			continue
		}
		for _, piece := range p.fstringPieces(s) {
			if piece.lit {
				if pending.Len() == 0 {
					pendingLine = piece.line
				}
				pending.WriteString(piece.text)
				continue
			}
			flush()
			js.add(piece.node)
		}
	}
	flush()
	return js
}

type fpiece struct {
	lit  bool
	text string
	node *Node
	line int
}

// fstringPieces splits one f-string token into literal text and
// FormattedValue nodes (expressions parsed recursively).
func (p *parser) fstringPieces(t token) []fpiece {
	pre, _ := stringPrefix(t.val)
	raw := strings.ContainsAny(pre, "rR")
	body := t.val[len(pre):]
	q := body[0]
	qlen := 1
	if len(body) >= 6 && body[1] == q && body[2] == q {
		qlen = 3
	}
	body = body[qlen : len(body)-qlen]
	return p.fstringBody(body, raw, t.line)
}

func (p *parser) fstringBody(body string, raw bool, line int) []fpiece {
	var out []fpiece
	var lit strings.Builder
	litStart := 0
	flushLit := func() {
		if lit.Len() > 0 {
			v, err := decodeBody(lit.String(), raw)
			if err != nil {
				panic(&SyntaxError{Msg: err.Error(), Line: line})
			}
			out = append(out, fpiece{lit: true, text: v, line: line + strings.Count(body[:litStart], "\n")})
			lit.Reset()
		}
	}
	i := 0
	for i < len(body) {
		c := body[i]
		if lit.Len() == 0 {
			litStart = i
		}
		if c == '\\' && !raw && i+1 < len(body) {
			if body[i+1] == 'N' && i+2 < len(body) && body[i+2] == '{' {
				if j := strings.IndexByte(body[i:], '}'); j > 0 {
					lit.WriteString(body[i : i+j+1])
					i += j + 1
					continue
				}
			}
			lit.WriteByte(c)
			lit.WriteByte(body[i+1])
			i += 2
			continue
		}
		if c == '{' {
			if i+1 < len(body) && body[i+1] == '{' {
				lit.WriteString("{")
				i += 2
				continue
			}
			end, exprText, spec, hasSpec := scanReplacement(body, i+1)
			if selfDocumenting(exprText) {
				if lit.Len() == 0 {
					litStart = i
				}
				lit.WriteString(strings.ReplaceAll(exprText, "\\", "\\\\"))
			}
			flushLit()
			fv := &Node{Kind: "FormattedValue", Line: line + strings.Count(body[:i], "\n")}
			e := strings.TrimSpace(exprText)
			e = strings.TrimSuffix(e, "=")
			if e != "" {
				sub, err := Parse("(" + e + ")\n")
				if err != nil {
					panic(&SyntaxError{Msg: "f-string: " + err.Error(), Line: line})
				}
				if len(sub.Body) == 1 && sub.Body[0].Value != nil {
					expr := sub.Body[0].Value
					shiftLines(expr, fv.Line-1)
					fv.add(expr)
				}
			}
			if hasSpec {
				js := &Node{Kind: "JoinedStr", Line: fv.Line}
				for _, pc := range p.fstringBody(spec, raw, fv.Line) {
					if pc.lit {
						js.add(&Node{Kind: "Constant", Line: fv.Line, IsStr: true, Str: pc.text})
					} else {
						js.add(pc.node)
					}
				}
				fv.add(js)
			}
			out = append(out, fpiece{node: fv})
			i = end
			continue
		}
		if c == '}' && i+1 < len(body) && body[i+1] == '}' {
			lit.WriteString("}")
			i += 2
			continue
		}
		lit.WriteByte(c)
		i++
	}
	flushLit()
	return out
}

func shiftLines(n *Node, d int) {
	if d == 0 {
		return
	}
	Walk(n, func(x *Node) { x.Line += d })
}

// scanReplacement scans an f-string replacement field starting after '{':
// returns the index after the closing '}', the expression text, the format
// spec and whether a spec was present.
func scanReplacement(s string, i int) (int, string, string, bool) {
	depth := 0
	start := i
	exprEnd := -1
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'' || c == '"':
			// skip nested string literal
			q := c
			if i+2 < len(s) && s[i+1] == q && s[i+2] == q {
				j := strings.Index(s[i+3:], string([]byte{q, q, q}))
				if j < 0 {
					return len(s), s[start:], "", false
				}
				i += 3 + j + 3
				continue
			}
			j := i + 1
			for j < len(s) && s[j] != q {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
			continue
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == '}':
			if depth == 0 {
				if exprEnd < 0 {
					exprEnd = i
				}
				return i + 1, cutConversion(s[start:exprEnd]), "", false
			}
			depth--
		case c == '!' && depth == 0 && i+1 < len(s) && s[i+1] != '=':
			if exprEnd < 0 {
				exprEnd = i
			}
		case c == ':' && depth == 0:
			if exprEnd < 0 {
				exprEnd = i
			}
			// format spec until matching '}'
			j := i + 1
			d := 0
			for j < len(s) {
				if s[j] == '{' {
					d++
				} else if s[j] == '}' {
					if d == 0 {
						break
					}
					d--
				}
				j++
			}
			return j + 1, cutConversion(s[start:exprEnd]), s[i+1 : j], true
		}
		i++
	}
	return len(s), s[start:], "", false
}

func cutConversion(e string) string {
	return e
}

// selfDocumenting reports an `{expr=}` replacement field.
func selfDocumenting(e string) bool {
	t := strings.TrimRight(e, " \t\n")
	if !strings.HasSuffix(t, "=") {
		return false
	}
	t = strings.TrimSuffix(t, "=")
	return !strings.HasSuffix(t, "=") && !strings.HasSuffix(t, "!") && !strings.HasSuffix(t, "<") && !strings.HasSuffix(t, ">")
}

// unicodeNames maps the \N{...} names seen in practice to their runes.
var unicodeNames = map[string]rune{
	"EM DASH": 0x2014, "EN DASH": 0x2013, "BULLET": 0x2022, "HORIZONTAL ELLIPSIS": 0x2026, "NO-BREAK SPACE": 0x00A0,
	"RIGHTWARDS ARROW": 0x2192, "LEFTWARDS ARROW": 0x2190, "CHECK MARK": 0x2713, "HEAVY CHECK MARK": 0x2714,
	"BALLOT X": 0x2717, "MULTIPLICATION SIGN": 0x00D7, "MIDDLE DOT": 0x00B7, "DEGREE SIGN": 0x00B0,
	"LEFT SINGLE QUOTATION MARK": 0x2018, "RIGHT SINGLE QUOTATION MARK": 0x2019, "LEFT DOUBLE QUOTATION MARK": 0x201C,
	"RIGHT DOUBLE QUOTATION MARK": 0x201D, "ZERO WIDTH SPACE": 0x200B, "ZERO WIDTH JOINER": 0x200D,
	"BYTE ORDER MARK": 0xFEFF, "ZERO WIDTH NO-BREAK SPACE": 0xFEFF, "REPLACEMENT CHARACTER": 0xFFFD,
	"BOX DRAWINGS LIGHT HORIZONTAL": 0x2500, "BOX DRAWINGS LIGHT VERTICAL": 0x2502, "SNOWMAN": 0x2603,
	"GREEK SMALL LETTER ALPHA": 0x03B1, "COMBINING ACUTE ACCENT": 0x0301, "LATIN SMALL LETTER E WITH ACUTE": 0x00E9,
	"NULL": 0, "ESCAPE": 0x1B, "LINE FEED": 0x0A, "CARRIAGE RETURN": 0x0D, "CHARACTER TABULATION": 0x09,
	"SPACE": 0x20, "WARNING SIGN": 0x26A0, "CROSS MARK": 0x274C, "WHITE HEAVY CHECK MARK": 0x2705,
	"COPYRIGHT SIGN": 0x00A9, "REGISTERED SIGN": 0x00AE, "TRADE MARK SIGN": 0x2122, "EURO SIGN": 0x20AC,
	"POUND SIGN": 0x00A3, "SECTION SIGN": 0x00A7, "PILCROW SIGN": 0x00B6, "INFINITY": 0x221E,
	"RIGHT-POINTING DOUBLE ANGLE QUOTATION MARK": 0x00BB, "LEFT-POINTING DOUBLE ANGLE QUOTATION MARK": 0x00AB,
	"BLACK RIGHT-POINTING TRIANGLE": 0x25B6, "SPARKLES": 0x2728, "ROCKET": 0x1F680, "PILE OF POO": 0x1F4A9,
	"GRINNING FACE": 0x1F600, "THUMBS UP SIGN": 0x1F44D, "HEAVY BLACK HEART": 0x2764,
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// decodeString decodes a complete (non-f, non-bytes) string literal token.
func decodeString(tok string) (string, error) {
	pre, _ := stringPrefix(tok)
	raw := strings.ContainsAny(pre, "rR")
	body := tok[len(pre):]
	q := body[0]
	qlen := 1
	if len(body) >= 6 && body[1] == q && body[2] == q {
		qlen = 3
	}
	return decodeBody(body[qlen:len(body)-qlen], raw)
}

func decodeBody(s string, raw bool) (string, error) {
	if raw {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch e := s[i]; e {
		case '\n':
		case '\\':
			b.WriteByte('\\')
		case '\'':
			b.WriteByte('\'')
		case '"':
			b.WriteByte('"')
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'f':
			b.WriteByte(12)
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte(11)
		case 'x':
			if i+2 < len(s)+1 && i+2 <= len(s)-1 && hexVal(s[i+1]) >= 0 && hexVal(s[i+2]) >= 0 {
				b.WriteRune(rune(hexVal(s[i+1])*16 + hexVal(s[i+2])))
				i += 2
			} else {
				return "", fmt.Errorf("truncated \\xXX escape")
			}
		case 'u', 'U':
			n := 4
			if e == 'U' {
				n = 8
			}
			if i+n >= len(s)+0 && i+n > len(s)-1 {
				if i+n > len(s)-1 {
					return "", fmt.Errorf("truncated \\%cXXXX escape", e)
				}
			}
			v := 0
			for k := 1; k <= n; k++ {
				h := hexVal(s[i+k])
				if h < 0 {
					return "", fmt.Errorf("truncated \\%cXXXX escape", e)
				}
				v = v*16 + h
			}
			b.WriteRune(rune(v))
			i += n
		case 'N':
			j := strings.IndexByte(s[i:], '}')
			if j < 0 || i+1 >= len(s) || s[i+1] != '{' {
				return "", fmt.Errorf("malformed \\N character escape")
			}
			if r, ok := unicodeNames[strings.ToUpper(s[i+2:i+j])]; ok {
				b.WriteRune(r)
			} else {
				b.WriteString("\\N" + s[i+1:i+j+1])
			}
			i += j
		default:
			if e >= '0' && e <= '7' {
				v := 0
				k := 0
				for k < 3 && i+k < len(s) && s[i+k] >= '0' && s[i+k] <= '7' {
					v = v*8 + int(s[i+k]-'0')
					k++
				}
				b.WriteRune(rune(v))
				i += k - 1
			} else {
				b.WriteByte('\\')
				b.WriteByte(e)
			}
		}
	}
	return b.String(), nil
}
