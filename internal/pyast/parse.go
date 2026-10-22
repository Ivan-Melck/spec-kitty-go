package pyast

import (
	"fmt"
	"strings"
)

type parser struct {
	toks []token
	pos  int
}

// Parse parses Python source into a Module node.
func Parse(src string) (mod *Node, err error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(*SyntaxError); ok {
				mod, err = nil, se
				return
			}
			panic(r)
		}
	}()
	mod = &Node{Kind: "Module", Line: 1}
	for p.cur().kind != tEOF {
		if p.cur().kind == tNewline {
			p.pos++
			continue
		}
		mod.Body = append(mod.Body, p.statement()...)
	}
	mod.add(mod.Body...)
	return mod, nil
}

func (p *parser) cur() token { return p.toks[p.pos] }

func (p *parser) peek(n int) token {
	if p.pos+n < len(p.toks) {
		return p.toks[p.pos+n]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) fail(msg string) {
	panic(&SyntaxError{Msg: msg, Line: p.cur().line})
}

func (p *parser) isOp(v string) bool { t := p.cur(); return t.kind == tOp && t.val == v }

func (p *parser) isKw(v string) bool { t := p.cur(); return t.kind == tName && t.val == v }

func (p *parser) acceptOp(v string) bool {
	if p.isOp(v) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) acceptKw(v string) bool {
	if p.isKw(v) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) expectOp(v string) token {
	if !p.isOp(v) {
		p.fail(fmt.Sprintf("expected %q, got %q", v, p.cur().val))
	}
	t := p.cur()
	p.pos++
	return t
}

func (p *parser) expectName() token {
	if p.cur().kind != tName {
		p.fail(fmt.Sprintf("expected name, got %q", p.cur().val))
	}
	t := p.cur()
	p.pos++
	return t
}

var keywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true, "elif": true, "else": true, "except": true,
	"finally": true, "for": true, "from": true, "global": true, "if": true, "import": true, "in": true, "is": true,
	"lambda": true, "nonlocal": true, "not": true, "or": true, "pass": true, "raise": true, "return": true, "try": true,
	"while": true, "with": true, "yield": true,
}

// ---------------------------------------------------------------------------
// statements

func (p *parser) statement() []*Node {
	t := p.cur()
	if t.kind == tOp && t.val == "@" {
		return []*Node{p.decorated()}
	}
	if t.kind == tName {
		switch t.val {
		case "def":
			return []*Node{p.funcdef(nil, t.line, false)}
		case "class":
			return []*Node{p.classdef(nil)}
		case "if":
			return []*Node{p.ifStmt()}
		case "while":
			return []*Node{p.whileStmt()}
		case "for":
			return []*Node{p.forStmt(false, t.line)}
		case "try":
			return []*Node{p.tryStmt()}
		case "with":
			return []*Node{p.withStmt(false, t.line)}
		case "async":
			nx := p.peek(1)
			if nx.kind == tName {
				switch nx.val {
				case "def":
					p.pos++
					return []*Node{p.funcdef(nil, t.line, true)}
				case "for":
					p.pos++
					return []*Node{p.forStmt(true, t.line)}
				case "with":
					p.pos++
					return []*Node{p.withStmt(true, t.line)}
				}
			}
		case "match":
			if p.looksLikeSoftCompound() {
				return []*Node{p.matchStmt()}
			}
		}
	}
	return p.simpleStatements()
}

// looksLikeSoftCompound reports a `match <subject>:` header (soft keyword).
func (p *parser) looksLikeSoftCompound() bool {
	nx := p.peek(1)
	if nx.kind == tNewline || nx.kind == tEOF {
		return false
	}
	if nx.kind == tOp && (nx.val == "=" || nx.val == "." || nx.val == "(" && false || strings.HasSuffix(nx.val, "=") && nx.val != "==" || nx.val == "," || nx.val == ")" || nx.val == ":") {
		return false
	}
	depth := 0
	for i := p.pos + 1; i < len(p.toks); i++ {
		tk := p.toks[i]
		if tk.kind == tNewline || tk.kind == tEOF {
			last := p.toks[i-1]
			return depth == 0 && last.kind == tOp && last.val == ":"
		}
		if tk.kind == tOp {
			switch tk.val {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			case "=":
				if depth == 0 {
					return false
				}
			}
		}
	}
	return false
}

func (p *parser) block() []*Node {
	p.expectOp(":")
	if p.cur().kind == tNewline {
		p.pos++
		for p.cur().kind == tNewline {
			p.pos++
		}
		if p.cur().kind != tIndent {
			p.fail("expected an indented block")
		}
		p.pos++
		var body []*Node
		for p.cur().kind != tDedent && p.cur().kind != tEOF {
			if p.cur().kind == tNewline {
				p.pos++
				continue
			}
			body = append(body, p.statement()...)
		}
		if p.cur().kind == tDedent {
			p.pos++
		}
		return body
	}
	return p.simpleStatements()
}

func (p *parser) decorated() *Node {
	var decos []*Node
	for p.isOp("@") {
		p.pos++
		decos = append(decos, p.namedExpr())
		if p.cur().kind != tNewline {
			p.fail("expected newline after decorator")
		}
		p.pos++
	}
	t := p.cur()
	switch {
	case p.isKw("def"):
		return p.funcdef(decos, t.line, false)
	case p.isKw("async") && p.peek(1).val == "def":
		p.pos++
		return p.funcdef(decos, t.line, true)
	case p.isKw("class"):
		return p.classdef(decos)
	}
	p.fail("expected def or class after decorator")
	return nil
}

func (p *parser) funcdef(decos []*Node, line int, async bool) *Node {
	kw := p.cur()
	p.pos++ // def
	if !async {
		line = kw.line
	}
	name := p.expectName()
	n := &Node{Kind: "FunctionDef", Line: line, Name: name.val}
	if async {
		n.Kind = "AsyncFunctionDef"
	}
	var tparams []*Node
	if p.isOp("[") {
		tparams = p.typeParams()
	}
	p.expectOp("(")
	args := p.parameters(")")
	p.expectOp(")")
	var returns *Node
	if p.acceptOp("->") {
		returns = p.test()
	}
	body := p.block()
	n.add(args)
	n.add(body...)
	n.add(decos...)
	n.add(returns)
	n.add(tparams...)
	return n
}

func (p *parser) typeParams() []*Node {
	p.expectOp("[")
	var out []*Node
	for !p.isOp("]") {
		t := &Node{Kind: "TypeVar", Line: p.cur().line}
		p.acceptOp("*")
		p.acceptOp("**")
		t.Name = p.expectName().val
		if p.acceptOp(":") {
			t.add(p.test())
		}
		if p.acceptOp("=") {
			t.add(p.test())
		}
		out = append(out, t)
		if !p.acceptOp(",") {
			break
		}
	}
	p.expectOp("]")
	return out
}

// parameters parses a def/lambda parameter list up to (not including) end.
func (p *parser) parameters(end string) *Node {
	args := &Node{Kind: "arguments", Line: p.cur().line}
	var plain, varargs, kwonly, kwdefaults, kwarg, defaults []*Node
	star := false
	for !p.isOp(end) {
		switch {
		case p.acceptOp("/"):
		case p.acceptOp("**"):
			a := p.param(end != ":")
			kwarg = append(kwarg, a)
		case p.acceptOp("*"):
			star = true
			if !p.isOp(",") && !p.isOp(end) {
				varargs = append(varargs, p.param(end != ":"))
			}
		default:
			a := p.param(end != ":")
			var def *Node
			if p.acceptOp("=") {
				def = p.test()
			}
			if star {
				kwonly = append(kwonly, a)
				if def != nil {
					kwdefaults = append(kwdefaults, def)
				}
			} else {
				plain = append(plain, a)
				if def != nil {
					defaults = append(defaults, def)
				}
			}
		}
		if !p.acceptOp(",") {
			break
		}
	}
	args.add(plain...)
	args.add(varargs...)
	args.add(kwonly...)
	args.add(kwdefaults...)
	args.add(kwarg...)
	args.add(defaults...)
	return args
}

func (p *parser) param(annotated bool) *Node {
	name := p.expectName()
	a := &Node{Kind: "arg", Line: name.line, Name: name.val}
	if annotated && p.acceptOp(":") {
		if p.acceptOp("*") {
			a.add(p.expr())
		} else {
			a.add(p.test())
		}
	}
	return a
}

func (p *parser) classdef(decos []*Node) *Node {
	kw := p.cur()
	p.pos++
	name := p.expectName()
	n := &Node{Kind: "ClassDef", Line: kw.line, Name: name.val}
	var tparams []*Node
	if p.isOp("[") {
		tparams = p.typeParams()
	}
	var bases, kws []*Node
	if p.acceptOp("(") {
		bases, kws = p.arglist(")")
		p.expectOp(")")
	}
	body := p.block()
	n.add(bases...)
	n.add(kws...)
	n.add(body...)
	n.add(decos...)
	n.add(tparams...)
	return n
}

func (p *parser) ifStmt() *Node {
	kw := p.cur()
	p.pos++
	n := &Node{Kind: "If", Line: kw.line}
	test := p.namedExpr()
	body := p.block()
	var orelse []*Node
	if p.isKw("elif") {
		orelse = []*Node{p.ifStmt()}
	} else if p.isKw("else") {
		p.pos++
		orelse = p.block()
	}
	n.add(test)
	n.add(body...)
	n.add(orelse...)
	return n
}

func (p *parser) whileStmt() *Node {
	kw := p.cur()
	p.pos++
	n := &Node{Kind: "While", Line: kw.line}
	test := p.namedExpr()
	body := p.block()
	var orelse []*Node
	if p.acceptKw("else") {
		orelse = p.block()
	}
	n.add(test)
	n.add(body...)
	n.add(orelse...)
	return n
}

func (p *parser) forStmt(async bool, line int) *Node {
	p.pos++ // for
	n := &Node{Kind: "For", Line: line}
	if async {
		n.Kind = "AsyncFor"
	}
	target := p.targetList()
	if !p.acceptKw("in") {
		p.fail("expected 'in'")
	}
	iter := p.starExprList()
	body := p.block()
	var orelse []*Node
	if p.acceptKw("else") {
		orelse = p.block()
	}
	n.add(target, iter)
	n.add(body...)
	n.add(orelse...)
	return n
}

func (p *parser) tryStmt() *Node {
	kw := p.cur()
	p.pos++
	n := &Node{Kind: "Try", Line: kw.line}
	body := p.block()
	var handlers, orelse, final []*Node
	for p.isKw("except") {
		h := &Node{Kind: "ExceptHandler", Line: p.cur().line}
		p.pos++
		if p.acceptOp("*") {
			n.Kind = "TryStar"
		}
		if !p.isOp(":") {
			h.add(p.test())
			if p.isOp(",") {
				// except A, B: (py2) is invalid in py3 except as tuple-less form
				p.fail("multiple exception types must be parenthesized")
			}
			if p.acceptKw("as") {
				p.expectName()
			}
		}
		h.add(p.block()...)
		handlers = append(handlers, h)
	}
	if p.acceptKw("else") {
		orelse = p.block()
	}
	if p.acceptKw("finally") {
		final = p.block()
	}
	n.add(body...)
	n.add(handlers...)
	n.add(orelse...)
	n.add(final...)
	return n
}

func (p *parser) withStmt(async bool, line int) *Node {
	p.pos++ // with
	n := &Node{Kind: "With", Line: line}
	if async {
		n.Kind = "AsyncWith"
	}
	var items []*Node
	parenthesized := false
	if p.isOp("(") {
		// parenthesized with-items if the matching ')' is followed by ':'
		depth := 0
		for i := p.pos; i < len(p.toks); i++ {
			tk := p.toks[i]
			if tk.kind == tOp {
				if tk.val == "(" || tk.val == "[" || tk.val == "{" {
					depth++
				} else if tk.val == ")" || tk.val == "]" || tk.val == "}" {
					depth--
					if depth == 0 {
						nx := p.toks[i+1]
						parenthesized = nx.kind == tOp && nx.val == ":"
						break
					}
				}
			}
			if tk.kind == tNewline {
				break
			}
		}
	}
	if parenthesized {
		p.pos++
		for !p.isOp(")") {
			items = append(items, p.withItem())
			if !p.acceptOp(",") {
				break
			}
		}
		p.expectOp(")")
	} else {
		for {
			items = append(items, p.withItem())
			if !p.acceptOp(",") {
				break
			}
		}
	}
	body := p.block()
	n.add(items...)
	n.add(body...)
	return n
}

func (p *parser) withItem() *Node {
	it := &Node{Kind: "withitem", Line: p.cur().line}
	it.add(p.test())
	if p.acceptKw("as") {
		it.add(p.target())
	}
	return it
}

func (p *parser) matchStmt() *Node {
	kw := p.cur()
	p.pos++
	n := &Node{Kind: "Match", Line: kw.line}
	n.add(p.starExprList())
	p.expectOp(":")
	if p.cur().kind != tNewline {
		p.fail("expected newline after match")
	}
	p.pos++
	for p.cur().kind == tNewline {
		p.pos++
	}
	if p.cur().kind != tIndent {
		p.fail("expected indented case block")
	}
	p.pos++
	for p.cur().kind != tDedent && p.cur().kind != tEOF {
		if p.cur().kind == tNewline {
			p.pos++
			continue
		}
		if !p.isKw("case") {
			p.fail("expected case")
		}
		c := &Node{Kind: "match_case", Line: p.cur().line}
		p.pos++
		c.add(p.pattern())
		if p.acceptKw("if") {
			c.add(p.namedExpr())
		}
		c.add(p.block()...)
		n.add(c)
	}
	if p.cur().kind == tDedent {
		p.pos++
	}
	return n
}

// pattern parses a match-case pattern loosely (until the guard / colon).
func (p *parser) pattern() *Node {
	n := &Node{Kind: "MatchPattern", Line: p.cur().line}
	depth := 0
	for {
		t := p.cur()
		if t.kind == tNewline || t.kind == tEOF {
			p.fail("unterminated case pattern")
		}
		if depth == 0 && t.kind == tOp && t.val == ":" {
			return n
		}
		if depth == 0 && t.kind == tName && t.val == "if" {
			return n
		}
		if t.kind == tOp {
			switch t.val {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			}
		}
		if t.kind == tString {
			p.pos++
			c := p.stringAtom(t)
			n.add(c)
			continue
		}
		p.pos++
	}
}

func (p *parser) simpleStatements() []*Node {
	var out []*Node
	for {
		out = append(out, p.smallStatement())
		if !p.acceptOp(";") {
			break
		}
		if p.cur().kind == tNewline {
			break
		}
	}
	if p.cur().kind != tNewline && p.cur().kind != tEOF {
		p.fail(fmt.Sprintf("invalid syntax near %q", p.cur().val))
	}
	if p.cur().kind == tNewline {
		p.pos++
	}
	return out
}

func (p *parser) smallStatement() *Node {
	t := p.cur()
	line := t.line
	if t.kind == tName {
		switch t.val {
		case "pass", "break", "continue":
			p.pos++
			return &Node{Kind: strings.Title(t.val), Line: line}
		case "return":
			p.pos++
			n := &Node{Kind: "Return", Line: line}
			if !p.atStmtEnd() {
				n.add(p.starExprList())
			}
			return n
		case "raise":
			p.pos++
			n := &Node{Kind: "Raise", Line: line}
			if !p.atStmtEnd() {
				n.add(p.test())
				if p.acceptKw("from") {
					n.add(p.test())
				}
			}
			return n
		case "global", "nonlocal":
			p.pos++
			for {
				p.expectName()
				if !p.acceptOp(",") {
					break
				}
			}
			return &Node{Kind: strings.Title(t.val), Line: line}
		case "del":
			p.pos++
			n := &Node{Kind: "Delete", Line: line}
			for {
				n.add(p.expr())
				if !p.acceptOp(",") || p.atStmtEnd() {
					break
				}
			}
			return n
		case "assert":
			p.pos++
			n := &Node{Kind: "Assert", Line: line}
			n.Test = p.test()
			n.add(n.Test)
			if p.acceptOp(",") {
				n.add(p.test())
			}
			return n
		case "import":
			p.pos++
			n := &Node{Kind: "Import", Line: line}
			for {
				name := p.dottedName()
				a := Alias{Name: name}
				if p.acceptKw("as") {
					a.AsName = p.expectName().val
				}
				n.Aliases = append(n.Aliases, a)
				if !p.acceptOp(",") {
					break
				}
			}
			return n
		case "from":
			p.pos++
			n := &Node{Kind: "ImportFrom", Line: line}
			for p.isOp(".") || p.isOp("...") {
				p.pos++
			}
			if !p.isKw("import") {
				p.dottedName()
			}
			if !p.acceptKw("import") {
				p.fail("expected import")
			}
			if p.acceptOp("*") {
				n.Aliases = append(n.Aliases, Alias{Name: "*"})
				return n
			}
			paren := p.acceptOp("(")
			for {
				if paren && p.isOp(")") {
					break
				}
				a := Alias{Name: p.expectName().val}
				if p.acceptKw("as") {
					a.AsName = p.expectName().val
				}
				n.Aliases = append(n.Aliases, a)
				if !p.acceptOp(",") {
					break
				}
			}
			if paren {
				p.expectOp(")")
			}
			return n
		case "type":
			nx := p.peek(1)
			if nx.kind == tName && (p.peek(2).val == "=" || p.peek(2).val == "[") {
				p.pos++
				n := &Node{Kind: "TypeAlias", Line: line}
				n.add(&Node{Kind: "Name", Line: nx.line, Name: nx.val})
				p.pos++
				if p.isOp("[") {
					n.add(p.typeParams()...)
				}
				p.expectOp("=")
				n.add(p.test())
				return n
			}
		}
	}
	// expression statement / assignment
	first := p.starExprListOrYield()
	if p.isOp("=") {
		n := &Node{Kind: "Assign", Line: line}
		targets := []*Node{first}
		var value *Node
		for p.acceptOp("=") {
			v := p.starExprListOrYield()
			targets = append(targets, v)
		}
		value = targets[len(targets)-1]
		targets = targets[:len(targets)-1]
		n.Targets, n.Value = targets, value
		n.add(targets...)
		n.add(value)
		return n
	}
	if t := p.cur(); t.kind == tOp && len(t.val) >= 2 && strings.HasSuffix(t.val, "=") && t.val != "==" && t.val != "<=" && t.val != ">=" && t.val != "!=" {
		p.pos++
		n := &Node{Kind: "AugAssign", Line: line}
		n.add(first, p.starExprListOrYield())
		return n
	}
	if p.acceptOp(":") {
		n := &Node{Kind: "AnnAssign", Line: line}
		n.add(first, p.test())
		if p.acceptOp("=") {
			n.add(p.starExprListOrYield())
		}
		return n
	}
	n := &Node{Kind: "Expr", Line: line, Value: first}
	n.add(first)
	return n
}

func (p *parser) atStmtEnd() bool {
	t := p.cur()
	return t.kind == tNewline || t.kind == tEOF || (t.kind == tOp && t.val == ";")
}

func (p *parser) dottedName() string {
	parts := []string{p.expectName().val}
	for p.acceptOp(".") {
		parts = append(parts, p.expectName().val)
	}
	return strings.Join(parts, ".")
}
