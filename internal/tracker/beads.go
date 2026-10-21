package tracker

import (
	"math"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// BeadsConnector ports spec_kitty_tracker.connectors.beads.BeadsConnector,
// driving the `bd` CLI through the program gateway.
type BeadsConnector struct {
	Workspace, Command, Cwd string
	run                     func(command []string, cwd string) (string, error)
}

// Name is "beads".
func (b *BeadsConnector) Name() string { return "beads" }

// Capabilities: Beads never assigns or performs terminal transitions.
func (b *BeadsConnector) Capabilities() Capabilities { return Capabilities{} }

// ListIssues runs `bd --json list` (not cursor-paginated).
func (b *BeadsConnector) ListIssues(since *PyTime, _ *string, limit int) (*Page, error) {
	command := []string{b.Command, "--json", "list"}
	if since != nil {
		command = append(command, "--updated-after", since.DateISO())
	}
	out, err := b.run(command, b.Cwd)
	if err != nil {
		return nil, err
	}
	payload, err := parseJSONOutput(out)
	if err != nil {
		return nil, err
	}
	var data []any
	switch t := payload.(type) {
	case []any:
		data = t
	case *jsonx.Object:
		v, ok := t.Get("items")
		if !ok {
			data = nil
		} else if l, ok := v.([]any); ok {
			data = l
		} else if o, ok := v.(*jsonx.Object); ok {
			for _, k := range o.Keys() {
				data = append(data, k)
			}
		}
	default:
		return nil, &Crash{Type: "AttributeError", Msg: "'" + pyjson.TypeName(payload) + "' object has no attribute 'get'"}
	}
	var issues []*Issue
	for _, item := range data {
		o, ok := item.(*jsonx.Object)
		if !ok {
			continue
		}
		issue, err := b.toCanonical(o, nil)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	if len(issues) > limit {
		issues = issues[:limit]
	}
	return &Page{Items: issues}, nil
}

// GetIssue runs `bd --json show <id>`.
func (b *BeadsConnector) GetIssue(ref ExternalRef) (*Issue, error) {
	out, err := b.run([]string{b.Command, "--json", "show", ref.ID}, b.Cwd)
	if err != nil {
		return nil, err
	}
	payload, err := parseJSONOutput(out)
	if err != nil {
		return nil, err
	}
	var candidate any = payload
	if l, ok := payload.([]any); ok {
		candidate = nil
		if len(l) > 0 {
			candidate = l[0]
		}
	}
	c, ok := candidate.(*jsonx.Object)
	if !ok {
		return nil, &TrackerError{Class: "IssueNotFoundError", Msg: "Beads issue not found: " + ref.ID}
	}
	if iv, ok := c.Get("issue"); ok {
		if io, ok := iv.(*jsonx.Object); ok {
			return b.toCanonical(io, c)
		}
	}
	return b.toCanonical(c, nil)
}

func (b *BeadsConnector) ref(id string) (ExternalRef, error) {
	return NewRef(b.Name(), b.Workspace, id, nil, nil)
}

// CreateIssue runs `bd --json create ...` then re-reads the issue.
func (b *BeadsConnector) CreateIssue(issue *Issue) (*Issue, error) {
	prio := 2
	if issue.Priority != nil {
		prio = *issue.Priority
	}
	command := []string{b.Command, "--json", "create", issue.Title, "--type", beadsType(issue.IssueType), "--priority", strconv.Itoa(prio)}
	if issue.Body != nil && *issue.Body != "" {
		command = append(command, "--description", *issue.Body)
	}
	if issue.Parent != nil {
		command = append(command, "--parent", issue.Parent.ID)
	}
	for _, l := range issue.Labels {
		command = append(command, "--label", l)
	}
	out, err := b.run(command, b.Cwd)
	if err != nil {
		return nil, err
	}
	payload, err := parseJSONOutput(out)
	if err != nil {
		return nil, err
	}
	id, err := beadsExtractID(payload)
	if err != nil {
		return nil, err
	}
	ref, err := b.ref(id)
	if err != nil {
		return nil, err
	}
	if issue.Status != "todo" && issue.Status != "in_progress" {
		st := &Issue{Status: issue.Status}
		if _, err := b.UpdateIssue(ref, &Patch{Fields: []string{"status"}, Src: st}); err != nil {
			return nil, err
		}
	}
	return b.GetIssue(ref)
}

// UpdateIssue runs `bd --json update <id> ...` (Beads never assigns,
// closes or edits custom fields/links).
func (b *BeadsConnector) UpdateIssue(ref ExternalRef, patch *Patch) (*Issue, error) {
	switch {
	case patch.Has("assignees"):
		return nil, capabilityError("beads: assignment is host-owned")
	case patch.Has("custom_fields"):
		return nil, capabilityError("beads: custom_fields is read-only")
	case patch.Has("links"):
		return nil, capabilityError("beads: links is read-only")
	}
	src := patch.Src
	command := []string{b.Command, "--json", "update", ref.ID}
	if patch.Has("status") {
		if src.Status == "done" || src.Status == "canceled" {
			return nil, capabilityError("beads: terminal transition is host-owned")
		}
		command = append(command, "--status", beadsStatus(src.Status))
	}
	if patch.Has("priority") && src.Priority != nil {
		command = append(command, "--priority", strconv.Itoa(*src.Priority))
	}
	if patch.Has("title") {
		command = append(command, "--title", src.Title)
	}
	if patch.Has("body") {
		body := ""
		if src.Body != nil {
			body = *src.Body
		}
		command = append(command, "--description", body)
	}
	if patch.Has("issue_type") {
		command = append(command, "--type", beadsType(src.IssueType))
	}
	if patch.Has("labels") {
		for _, l := range src.Labels {
			command = append(command, "--set-labels", l)
		}
	}
	if patch.Has("parent") {
		if src.Parent == nil {
			command = append(command, "--parent", "")
		} else {
			command = append(command, "--parent", src.Parent.ID)
		}
	}
	if len(command) == 4 {
		return b.GetIssue(ref)
	}
	if _, err := b.run(command, b.Cwd); err != nil {
		return nil, err
	}
	return b.GetIssue(ref)
}

func beadsExtractID(payload any) (string, error) {
	switch t := payload.(type) {
	case *jsonx.Object:
		if v, ok := t.Get("id"); ok {
			if s, ok := v.(string); ok {
				return s, nil
			}
		}
		if v, ok := t.Get("issue"); ok {
			if o, ok := v.(*jsonx.Object); ok {
				if id, ok := o.Get("id"); ok {
					if s, ok := id.(string); ok {
						return s, nil
					}
				}
			}
		}
	case []any:
		if len(t) > 0 {
			if o, ok := t[0].(*jsonx.Object); ok {
				if id, ok := o.Get("id"); ok {
					if s, ok := id.(string); ok {
						return s, nil
					}
				}
			}
		}
	}
	return "", requestError("Unable to determine issue id from beads response")
}

func pyInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(strings.ReplaceAll(t, "_", "")))
		return n, err == nil
	}
	return 0, false
}

func (b *BeadsConnector) toCanonical(item, details *jsonx.Object) (*Issue, error) {
	get := func(o *jsonx.Object, k string) any {
		if o == nil {
			return nil
		}
		v, _ := o.Get(k)
		return v
	}
	var labels []string
	if l, ok := get(item, "labels").([]any); ok {
		for _, x := range l {
			labels = append(labels, pyjson.Str(x))
		}
	}
	var assignees []string
	if a, ok := get(item, "assignee").(string); ok && a != "" {
		assignees = []string{a}
	}
	var parent *ExternalRef
	if p, ok := get(item, "parent").(string); ok && p != "" {
		r, err := b.ref(p)
		if err != nil {
			return nil, err
		}
		parent = &r
	}
	links := []Link{}
	var deps any
	if details != nil {
		deps = get(details, "dependencies")
	}
	if deps == nil {
		deps = get(item, "dependencies")
	}
	if l, ok := deps.([]any); ok {
		for _, d := range l {
			do, ok := d.(*jsonx.Object)
			if !ok {
				continue
			}
			target := ""
			for _, k := range []string{"id", "depends_on_id", "dependsOnId"} {
				if v := get(do, k); pyjson.Truthy(v) {
					target = pyjson.Str(v)
					break
				}
			}
			if target == "" {
				continue
			}
			depType := "blocks"
			for _, k := range []string{"dependency_type", "type"} {
				if v := get(do, k); pyjson.Truthy(v) {
					depType = pyjson.Str(v)
					break
				}
			}
			lt, err := beadsLinkType(depType)
			if err != nil {
				return nil, err
			}
			r, err := b.ref(target)
			if err != nil {
				return nil, err
			}
			links = append(links, Link{Type: lt, Target: r})
		}
	}
	cf := &jsonx.Object{}
	if v, ok := item.Get("metadata"); ok && v != nil {
		cf.Set("metadata", v)
	}
	if details != nil && details.Len() > 0 {
		if v, ok := details.Get("comments"); ok && v != nil {
			cf.Set("comments", v)
		}
	}
	raw := item.Clone()
	if details != nil && details.Len() > 0 {
		raw.Set("details", details.Clone())
	}
	id, ok := get(item, "id").(string)
	if !ok || strings.TrimSpace(id) == "" {
		return nil, payloadError("Beads issue payload missing a non-empty id")
	}
	title, ok := get(item, "title").(string)
	if !ok || strings.TrimSpace(title) == "" {
		return nil, payloadError("Beads issue payload missing a non-empty title")
	}
	var body *string
	if v := get(item, "description"); pyjson.Truthy(v) {
		s := pyjson.Str(v)
		body = &s
	}
	statusRaw := "open"
	if v := get(item, "status"); pyjson.Truthy(v) {
		statusRaw = pyjson.Str(v)
	}
	status, err := beadsToCanonicalStatus(statusRaw)
	if err != nil {
		return nil, err
	}
	typeRaw := "task"
	if v := get(item, "issue_type"); pyjson.Truthy(v) {
		typeRaw = pyjson.Str(v)
	}
	it, err := beadsToCanonicalType(typeRaw)
	if err != nil {
		return nil, err
	}
	var prio *int
	if n, ok := pyInt(get(item, "priority")); ok {
		n = max(0, min(4, n))
		prio = &n
	}
	var url *string
	if u, ok := get(item, "external_ref").(string); ok && u != "" {
		url = &u
	}
	key := id
	ref, err := NewRef(b.Name(), b.Workspace, id, &key, url)
	if err != nil {
		return nil, err
	}
	if labels == nil {
		labels = []string{}
	}
	if assignees == nil {
		assignees = []string{}
	}
	issue := &Issue{
		Ref: ref, Title: title, Body: body, Status: status, IssueType: it, Priority: prio,
		Assignees: assignees, Labels: labels, Parent: parent, Links: links, CustomFields: cf,
		CreatedAt: trackerParseDatetime(get(item, "created_at")), UpdatedAt: trackerParseDatetime(get(item, "updated_at")),
		Raw: raw,
	}
	if err := issue.Validate(); err != nil {
		return nil, err
	}
	return issue, nil
}

// trackerParseDatetime is spec_kitty_tracker.mapping.parse_datetime.
func trackerParseDatetime(v any) *PyTime {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	t, ok := ParseISO(strings.ReplaceAll(s, "Z", "+00:00"))
	if !ok {
		return nil
	}
	return t
}

func beadsToCanonicalStatus(s string) (string, error) {
	m := map[string]string{"open": "todo", "in_progress": "in_progress", "blocked": "blocked", "deferred": "todo", "closed": "done", "pinned": "in_progress", "tombstone": "canceled"}
	if v, ok := m[strings.ToLower(s)]; ok {
		return v, nil
	}
	return "", payloadError("Unknown Beads status: " + pyjson.ReprString(s))
}

func beadsStatus(s string) string {
	return map[string]string{"todo": "open", "in_progress": "in_progress", "in_review": "in_progress", "blocked": "blocked", "done": "closed", "canceled": "closed"}[s]
}

func beadsToCanonicalType(s string) (string, error) {
	m := map[string]string{"epic": "epic", "feature": "story", "task": "task", "bug": "bug", "chore": "chore", "decision": "chore"}
	if v, ok := m[strings.ToLower(s)]; ok {
		return v, nil
	}
	return "", payloadError("Unknown Beads issue_type: " + pyjson.ReprString(s))
}

func beadsType(t string) string {
	return map[string]string{"epic": "epic", "story": "feature", "task": "task", "bug": "bug", "chore": "chore", "subtask": "task"}[t]
}

func beadsLinkType(dep string) (string, error) {
	switch strings.ToLower(dep) {
	case "blocks":
		return "blocked_by", nil
	case "related", "relates_to", "relates", "discovered-from":
		return "relates_to", nil
	case "duplicates":
		return "duplicates", nil
	case "parent-child":
		return "child_of", nil
	}
	return "", payloadError("Unknown Beads dependency_type: " + pyjson.ReprString(dep))
}
