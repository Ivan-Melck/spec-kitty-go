package tracker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Canonical vocabularies (spec_kitty_tracker.models).
var (
	CanonicalStatuses   = []string{"todo", "in_progress", "in_review", "blocked", "done", "canceled"}
	CanonicalIssueTypes = []string{"epic", "story", "task", "bug", "chore", "subtask"}
	LinkTypes           = []string{"blocks", "blocked_by", "relates_to", "duplicates", "parent_of", "child_of"}
)

// PyTime is a Python datetime: an instant plus whether it is tz-aware.
type PyTime struct {
	T     time.Time
	Aware bool
}

// ISO renders datetime.isoformat().
func (p PyTime) ISO() string {
	s := p.T.Format("2006-01-02T15:04:05")
	if us := p.T.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	if !p.Aware {
		return s
	}
	_, off := p.T.Zone()
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	s += fmt.Sprintf("%c%02d:%02d", sign, off/3600, off%3600/60)
	if off%60 != 0 {
		s += fmt.Sprintf(":%02d", off%60)
	}
	return s
}

// DateISO renders .date().isoformat().
func (p PyTime) DateISO() string { return p.T.Format("2006-01-02") }

func timeEq(a, b *PyTime) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Aware != b.Aware {
		return false
	}
	return a.T.Equal(b.T)
}

// timeLess is a < b; mixing naive and aware raises TypeError upstream.
func timeLess(a, b PyTime) (bool, error) {
	if a.Aware != b.Aware {
		return false, &Crash{Type: "TypeError", Msg: "can't compare offset-naive and offset-aware datetimes"}
	}
	return a.T.Before(b.T), nil
}

var isoRe = regexp.MustCompile(`^(\d{4})-?(\d{2})-?(\d{2})(?:.(\d{2})(?::?(\d{2})(?::?(\d{2})(?:[.,](\d+))?)?)?(Z|[+-]\d{2}(?::?\d{2}(?::?\d{2}(?:\.\d+)?)?)?)?)?$`)

// ParseISO mirrors datetime.fromisoformat (Python 3.11+) for the forms
// trackers emit; ok=false on ValueError.
func ParseISO(s string) (*PyTime, bool) {
	m := isoRe.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	atoi := func(x string) int { n, _ := strconv.Atoi(x); return n }
	y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
	h, mi, sec, ns := atoi(m[4]), atoi(m[5]), atoi(m[6]), 0
	if f := m[7]; f != "" {
		if len(f) > 6 {
			f = f[:6]
		}
		ns = atoi((f + "000000")[:6]) * 1000
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 59 {
		return nil, false
	}
	loc := time.UTC
	aware := false
	if tz := m[8]; tz != "" {
		aware = true
		if tz != "Z" {
			sign := 1
			if tz[0] == '-' {
				sign = -1
			}
			digits := strings.ReplaceAll(tz[1:], ":", "")
			if i := strings.IndexByte(digits, '.'); i >= 0 {
				digits = digits[:i]
			}
			off := atoi(digits[0:2]) * 3600
			if len(digits) >= 4 {
				off += atoi(digits[2:4]) * 60
			}
			if len(digits) >= 6 {
				off += atoi(digits[4:6])
			}
			if off >= 86400 {
				return nil, false
			}
			loc = time.FixedZone("", sign*off)
		}
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, ns, loc)
	if t.Day() != d {
		return nil, false
	}
	return &PyTime{T: t, Aware: aware}, true
}

// ExternalRef mirrors spec_kitty_tracker.models.ExternalRef.
type ExternalRef struct {
	System, Workspace, ID string
	Key, URL              *string
}

// NewRef validates like ExternalRef.__post_init__ (ValueError).
func NewRef(system, workspace, id string, key, url *string) (ExternalRef, error) {
	switch {
	case strings.TrimSpace(system) == "":
		return ExternalRef{}, &ValueError{"ExternalRef.system must not be empty"}
	case strings.TrimSpace(workspace) == "":
		return ExternalRef{}, &ValueError{"ExternalRef.workspace must not be empty"}
	case strings.TrimSpace(id) == "":
		return ExternalRef{}, &ValueError{"ExternalRef.id must not be empty"}
	}
	return ExternalRef{System: system, Workspace: workspace, ID: id, Key: key, URL: url}, nil
}

// Identity is system:workspace:id.
func (r ExternalRef) Identity() string { return r.System + ":" + r.Workspace + ":" + r.ID }

func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func refEq(a, b ExternalRef) bool {
	return a.System == b.System && a.Workspace == b.Workspace && a.ID == b.ID && strPtrEq(a.Key, b.Key) && strPtrEq(a.URL, b.URL)
}

func refPtrEq(a, b *ExternalRef) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return refEq(*a, *b)
}

// ValueError is a Python ValueError (caught by the CLI error boundary).
type ValueError struct{ Msg string }

func (e *ValueError) Error() string { return e.Msg }

// Link is CanonicalLink.
type Link struct {
	Type   string
	Target ExternalRef
}

// Issue is CanonicalIssue.
type Issue struct {
	Ref          ExternalRef
	Title        string
	Body         *string
	Status       string
	IssueType    string
	Priority     *int
	Assignees    []string
	Labels       []string
	Parent       *ExternalRef
	Links        []Link
	CustomFields *jsonx.Object
	CreatedAt    *PyTime
	UpdatedAt    *PyTime
	Raw          *jsonx.Object
}

// Validate mirrors CanonicalIssue.__post_init__.
func (i *Issue) Validate() error {
	if strings.TrimSpace(i.Title) == "" {
		return &ValueError{"CanonicalIssue.title must not be empty"}
	}
	if i.Priority != nil && (*i.Priority < 0 || *i.Priority > 4) {
		return &ValueError{"CanonicalIssue.priority must be in range 0..4"}
	}
	return nil
}

// Clone mirrors CanonicalIssue.clone.
func (i *Issue) Clone() *Issue {
	c := *i
	c.Assignees = append([]string{}, i.Assignees...)
	c.Labels = append([]string{}, i.Labels...)
	c.Links = append([]Link{}, i.Links...)
	if i.CustomFields != nil {
		c.CustomFields = i.CustomFields.Clone()
	} else {
		c.CustomFields = &jsonx.Object{}
	}
	if i.Raw != nil {
		c.Raw = i.Raw.Clone()
	}
	return &c
}

func strsEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func linksEq(a, b []Link) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type || !refEq(a[i].Target, b[i].Target) {
			return false
		}
	}
	return true
}

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func objEq(a, b *jsonx.Object) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return pyjson.Equal(a, b)
}

// fieldValue returns a CORE_ISSUE_FIELDS value for comparison/patching.
func (i *Issue) fieldEq(name string, o *Issue) bool {
	switch name {
	case "title":
		return i.Title == o.Title
	case "body":
		return strPtrEq(i.Body, o.Body)
	case "status":
		return i.Status == o.Status
	case "issue_type":
		return i.IssueType == o.IssueType
	case "priority":
		return intPtrEq(i.Priority, o.Priority)
	case "assignees":
		return strsEq(i.Assignees, o.Assignees)
	case "labels":
		return strsEq(i.Labels, o.Labels)
	case "parent":
		return refPtrEq(i.Parent, o.Parent)
	case "links":
		return linksEq(i.Links, o.Links)
	case "custom_fields":
		return objEq(orEmpty(i.CustomFields), orEmpty(o.CustomFields))
	}
	return false
}

// copyField sets field name of i from src.
func (i *Issue) copyField(name string, src *Issue) {
	switch name {
	case "title":
		i.Title = src.Title
	case "body":
		i.Body = src.Body
	case "status":
		i.Status = src.Status
	case "issue_type":
		i.IssueType = src.IssueType
	case "priority":
		i.Priority = src.Priority
	case "assignees":
		i.Assignees = src.Assignees
	case "labels":
		i.Labels = src.Labels
	case "parent":
		i.Parent = src.Parent
	case "links":
		i.Links = src.Links
	case "custom_fields":
		i.CustomFields = src.CustomFields
	}
}

func orEmpty(o *jsonx.Object) *jsonx.Object {
	if o == nil {
		return &jsonx.Object{}
	}
	return o
}

// Equal is dataclass equality.
func (i *Issue) Equal(o *Issue) bool {
	if !refEq(i.Ref, o.Ref) {
		return false
	}
	for _, f := range CoreIssueFields {
		if !i.fieldEq(f, o) {
			return false
		}
	}
	return timeEq(i.CreatedAt, o.CreatedAt) && timeEq(i.UpdatedAt, o.UpdatedAt) && objEq(i.Raw, o.Raw)
}

// CoreIssueFields is policy.CORE_ISSUE_FIELDS.
var CoreIssueFields = []string{"title", "body", "status", "issue_type", "priority", "assignees", "labels", "parent", "links", "custom_fields"}

// --- store (de)serialisation (specify_cli.tracker.store) ---

func optPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func serializeRef(r ExternalRef) *jsonx.Object {
	return jsonx.NewObject("system", r.System, "workspace", r.Workspace, "id", r.ID, "key", optPtr(r.Key), "url", optPtr(r.URL))
}

func serializeIssue(i *Issue) *jsonx.Object {
	links := []any{}
	for _, l := range i.Links {
		links = append(links, jsonx.NewObject("type", l.Type, "target", serializeRef(l.Target)))
	}
	var parent any
	if i.Parent != nil {
		parent = serializeRef(*i.Parent)
	}
	var prio any
	if i.Priority != nil {
		prio = *i.Priority
	}
	strs := func(xs []string) []any {
		out := []any{}
		for _, x := range xs {
			out = append(out, x)
		}
		return out
	}
	ts := func(t *PyTime) any {
		if t == nil {
			return nil
		}
		return t.ISO()
	}
	var raw any
	if i.Raw != nil {
		raw = i.Raw
	}
	return jsonx.NewObject(
		"ref", serializeRef(i.Ref),
		"title", i.Title,
		"body", optPtr(i.Body),
		"status", i.Status,
		"issue_type", i.IssueType,
		"priority", prio,
		"assignees", strs(i.Assignees),
		"labels", strs(i.Labels),
		"parent", parent,
		"links", links,
		"custom_fields", orEmpty(i.CustomFields),
		"created_at", ts(i.CreatedAt),
		"updated_at", ts(i.UpdatedAt),
		"raw", raw,
	)
}

func optStrField(o *jsonx.Object, k string) *string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return nil
	}
	s := pyjson.Str(v)
	return &s
}

func strOr(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	if !pyjson.Truthy(v) {
		return ""
	}
	return pyjson.Str(v)
}

func deserializeRef(o *jsonx.Object) (ExternalRef, error) {
	return NewRef(strOr(o, "system"), strOr(o, "workspace"), strOr(o, "id"), optStrField(o, "key"), optStrField(o, "url"))
}

func parseDT(v any) *PyTime {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}
	t, ok := ParseISO(s)
	if !ok {
		return nil
	}
	return t
}

func deserializeIssue(p *jsonx.Object) (*Issue, error) {
	var links []Link
	if lv, ok := p.Get("links"); ok {
		if l, ok := lv.([]any); ok {
			for _, e := range l {
				eo, ok := e.(*jsonx.Object)
				if !ok {
					continue
				}
				to, ok := func() (*jsonx.Object, bool) { v, _ := eo.Get("target"); o, ok := v.(*jsonx.Object); return o, ok }()
				if !ok {
					continue
				}
				lt := "relates_to"
				if tv, ok := eo.Get("type"); ok {
					lt = pyjson.Str(tv)
				}
				if !contains(LinkTypes, lt) {
					lt = "relates_to"
				}
				target, err := deserializeRef(to)
				if err != nil {
					return nil, err
				}
				links = append(links, Link{Type: lt, Target: target})
			}
		}
	}
	var parent *ExternalRef
	if pv, ok := p.Get("parent"); ok {
		if po, ok := pv.(*jsonx.Object); ok {
			r, err := deserializeRef(po)
			if err != nil {
				return nil, err
			}
			parent = &r
		}
	}
	var prio *int
	if pr, ok := p.Get("priority"); ok {
		switch t := pr.(type) {
		case int:
			prio = &t
		case bool:
			n := 0
			if t {
				n = 1
			}
			prio = &n
		default:
			s := pyjson.Str(pr)
			if s != "" && strings.Trim(s, "0123456789") == "" {
				n, _ := strconv.Atoi(s)
				prio = &n
			}
		}
	}
	rv, _ := p.Get("ref")
	ro, _ := rv.(*jsonx.Object)
	if ro == nil {
		return nil, &Crash{Type: "KeyError", Msg: "'ref'"}
	}
	ref, err := deserializeRef(ro)
	if err != nil {
		return nil, err
	}
	title := strOr(p, "title")
	if title == "" {
		title = "Untitled"
	}
	status := "todo"
	if v, ok := p.Get("status"); ok {
		status = pyjson.Str(v)
	}
	if !contains(CanonicalStatuses, status) {
		status = "todo"
	}
	it := "task"
	if v, ok := p.Get("issue_type"); ok {
		it = pyjson.Str(v)
	}
	if !contains(CanonicalIssueTypes, it) {
		it = "task"
	}
	strList := func(k string) []string {
		out := []string{}
		v, _ := p.Get(k)
		if l, ok := v.([]any); ok {
			for _, x := range l {
				out = append(out, pyjson.Str(x))
			}
		}
		return out
	}
	cf := &jsonx.Object{}
	if v, ok := p.Get("custom_fields"); ok {
		if o, ok := v.(*jsonx.Object); ok {
			cf = o.Clone()
		}
	}
	var raw *jsonx.Object
	if v, ok := p.Get("raw"); ok {
		if o, ok := v.(*jsonx.Object); ok {
			raw = o.Clone()
		}
	}
	cv, _ := p.Get("created_at")
	uv, _ := p.Get("updated_at")
	issue := &Issue{
		Ref: ref, Title: title, Body: optStrField(p, "body"), Status: status, IssueType: it,
		Priority: prio, Assignees: strList("assignees"), Labels: strList("labels"), Parent: parent,
		Links: links, CustomFields: cf, CreatedAt: parseDT(cv), UpdatedAt: parseDT(uv), Raw: raw,
	}
	if issue.Links == nil {
		issue.Links = []Link{}
	}
	if err := issue.Validate(); err != nil {
		return nil, err
	}
	return issue, nil
}
