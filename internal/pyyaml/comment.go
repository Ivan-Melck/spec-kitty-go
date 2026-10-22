package pyyaml

// Comment data of ruamel.yaml's round-trip loader (comment_handling None,
// "version 0" comments): tokens, events and nodes carry a Python list
// [post, pre, ...] whose slots hold a CommentToken, a list of them, or
// None. PyList keeps Python's shared, mutable list semantics.

// CommentToken is ruamel's CommentToken. Raw marks the bare string a
// block scalar's indicator-line comment is stored as.
type CommentToken struct {
	Value   string
	Start   *Mark
	End     *Mark
	Column  int // start_mark.column (the emitter may move it)
	Raw     bool
	PreDone bool
}

// PyList is a Python list.
type PyList struct{ V []any }

// NewList returns a list holding v.
func NewList(v ...any) *PyList { return &PyList{V: append([]any(nil), v...)} }

// Len is len(list) (0 for nil).
func (l *PyList) Len() int {
	if l == nil {
		return 0
	}
	return len(l.V)
}

// Get is list[i] (nil when out of range).
func (l *PyList) Get(i int) any {
	if l == nil || i < 0 || i >= len(l.V) {
		return nil
	}
	return l.V[i]
}

// Set is list[i] = v (growing the list with None as needed).
func (l *PyList) Set(i int, v any) {
	for len(l.V) <= i {
		l.V = append(l.V, nil)
	}
	l.V[i] = v
}

// Append is list.append(v).
func (l *PyList) Append(v any) { l.V = append(l.V, v) }

// Copy is list[:].
func (l *PyList) Copy() *PyList {
	if l == nil {
		return nil
	}
	return NewList(l.V...)
}

// Slice is list[a:b] (b < 0: to the end).
func (l *PyList) Slice(a, b int) *PyList {
	if l == nil {
		return NewList()
	}
	if b < 0 || b > len(l.V) {
		b = len(l.V)
	}
	if a > b {
		a = b
	}
	return NewList(l.V[a:b]...)
}

// Truthy is bool(list) for a slot value (None, empty list are false).
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case *PyList:
		return x != nil && len(x.V) > 0
	case *CommentToken:
		return x != nil
	}
	return true
}

// Comment returns the token-comment slot i as a CommentToken (nil if not one).
func CommentAt(l *PyList, i int) *CommentToken {
	c, _ := l.Get(i).(*CommentToken)
	return c
}

// ListAt returns slot i as a list (nil if not one).
func ListAt(l *PyList, i int) *PyList {
	c, _ := l.Get(i).(*PyList)
	return c
}

func newCommentToken(value string, start, end *Mark) *CommentToken {
	c := &CommentToken{Value: value, Start: start, End: end}
	if start != nil {
		c.Column = start.Column
	}
	return c
}

// ---------------------------------------------------------------- token comments

func (t *token) addPostComment(c *CommentToken) {
	if t.cmt == nil {
		t.cmt = NewList(nil, nil)
	}
	t.cmt.Set(0, c)
}

func (t *token) addPreComments(cs *PyList) {
	if t.cmt == nil {
		t.cmt = NewList(nil, nil)
	}
	t.cmt.Set(1, cs)
}

// moveOldComment is Token.move_old_comment.
func (t *token) moveOldComment(target *token, empty bool) {
	c := t.cmt
	if c == nil {
		return
	}
	if target == nil || target.kind == tStreamEnd || target.kind == tDocumentStart {
		return
	}
	t.cmt = nil
	tc := target.cmt
	if tc.Len() == 0 { // `not tc`
		if empty {
			c = NewList(c.Get(0), c.Get(1), nil, nil, c.Get(0))
		}
		target.cmt = c
		return
	}
	if (Truthy(c.Get(0)) && Truthy(tc.Get(0))) || (Truthy(c.Get(1)) && Truthy(tc.Get(1))) {
		cl, ok1 := c.Get(1).(*PyList)
		tl, ok2 := tc.Get(1).(*PyList)
		if ok1 && ok2 {
			cl.V = append(cl.V, tl.V...)
		} else {
			panic(&PyError{"NotImplementedError", "overlap in comment"})
		}
	}
	if Truthy(c.Get(0)) {
		tc.Set(0, c.Get(0))
	}
	if Truthy(c.Get(1)) {
		tc.Set(1, c.Get(1))
	}
}

// splitOldComment is Token.split_old_comment.
func (t *token) splitOldComment() *PyList {
	c := t.cmt
	if c == nil || c.Get(0) == nil {
		return nil
	}
	ret := NewList(c.Get(0), nil)
	if c.Get(1) == nil {
		t.cmt = nil
	}
	return ret
}
