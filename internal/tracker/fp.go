package tracker

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// FPConnector ports spec_kitty_tracker.connectors.fp.FPConnector, driving
// the `fp` CLI directly (fp has no program gateway upstream).
type FPConnector struct {
	Workspace, Command, Cwd string
	run                     func(command []string, cwd string) (string, error)
}

var (
	fpIssueLineRe = regexp.MustCompile(`^(?P<id>[A-Za-z0-9_-]+)\s+\[(?P<status>[^\]]+)\](?:\s+\[(?P<priority>[^\]]+)\])?\s+(?P<title>.+)$`)
	fpTreeRe      = regexp.MustCompile(`^[│├└─\s]+`)
	fpIDRe        = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-[A-Za-z0-9][A-Za-z0-9-]*\b`)
)

// Name is "fp".
func (f *FPConnector) Name() string { return "fp" }

// Capabilities: fp never assigns or performs terminal transitions.
func (f *FPConnector) Capabilities() Capabilities { return Capabilities{} }

// splitLines mirrors str.splitlines().
func splitLines(s string) []string {
	var out []string
	cur := []rune{}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; r {
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', ' ', ' ':
			out = append(out, string(cur))
			cur = cur[:0]
		case '\r':
			out = append(out, string(cur))
			cur = cur[:0]
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
		default:
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func fpNormalize(line string) string {
	n := strings.TrimSpace(line)
	n = fpTreeRe.ReplaceAllString(n, "")
	return strings.TrimSpace(n)
}

func (f *FPConnector) parseLines(text string) ([]*Issue, error) {
	var out []*Issue
	for _, raw := range splitLines(text) {
		line := fpNormalize(raw)
		if line == "" {
			continue
		}
		m := fpIssueLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		issue, err := f.fromMatch(m)
		if err != nil {
			return nil, err
		}
		out = append(out, issue)
	}
	return out, nil
}

func (f *FPConnector) fromMatch(m []string) (*Issue, error) {
	id := m[1]
	key := id
	ref, err := NewRef(f.Name(), f.Workspace, id, &key, nil)
	if err != nil {
		return nil, err
	}
	issue := &Issue{
		Ref: ref, Title: strings.TrimSpace(m[4]), Status: fpToCanonicalStatus(m[2]), IssueType: "task",
		Priority: fpPriority(m[3]), Assignees: []string{}, Labels: []string{}, Links: []Link{},
		CustomFields: &jsonx.Object{}, Raw: jsonx.NewObject("line", m[0]),
	}
	if err := issue.Validate(); err != nil {
		return nil, err
	}
	return issue, nil
}

// ListIssues runs `fp issue list`.
func (f *FPConnector) ListIssues(_ *PyTime, _ *string, limit int) (*Page, error) {
	out, err := f.run([]string{f.Command, "issue", "list"}, f.Cwd)
	if err != nil {
		return nil, err
	}
	issues, err := f.parseLines(out)
	if err != nil {
		return nil, err
	}
	if len(issues) > limit {
		issues = issues[:limit]
	}
	return &Page{Items: issues}, nil
}

func fpExtractID(output string) (string, error) {
	m := fpIDRe.FindString(output)
	if m == "" {
		return "", requestError("Unable to extract fp issue id from command output")
	}
	return m, nil
}

func (f *FPConnector) parseWithID(text, id string) (*Issue, error) {
	for _, raw := range splitLines(text) {
		line := fpNormalize(raw)
		if line == "" {
			continue
		}
		if m := fpIssueLineRe.FindStringSubmatch(line); m != nil && m[1] == id {
			return f.fromMatch(m)
		}
	}
	fallback, err := fpExtractID(text)
	if err != nil {
		return nil, err
	}
	if fallback != id {
		return nil, nil
	}
	key := id
	ref, err := NewRef(f.Name(), f.Workspace, id, &key, nil)
	if err != nil {
		return nil, err
	}
	return &Issue{Ref: ref, Title: id, Status: "todo", IssueType: "task", Assignees: []string{}, Labels: []string{},
		Links: []Link{}, CustomFields: &jsonx.Object{}, Raw: jsonx.NewObject("show", text)}, nil
}

// GetIssue runs `fp issue show <id>` and `fp context <id>`.
func (f *FPConnector) GetIssue(ref ExternalRef) (*Issue, error) {
	show, err := f.run([]string{f.Command, "issue", "show", ref.ID}, f.Cwd)
	if err != nil {
		return nil, err
	}
	ctxOut, err := f.run([]string{f.Command, "context", ref.ID}, f.Cwd)
	if err != nil {
		return nil, err
	}
	parsed, err := f.parseWithID(show, ref.ID)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		page, err := f.ListIssues(nil, nil, 500)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			if it.Ref.ID == ref.ID {
				parsed = it
				break
			}
		}
		if parsed == nil {
			return nil, &TrackerError{Class: "IssueNotFoundError", Msg: "fp issue not found: " + ref.ID}
		}
	} else {
		parsed.Raw = jsonx.NewObject("show", show, "context", ctxOut)
	}
	if b := strings.TrimSpace(ctxOut); b != "" {
		parsed.Body = &b
	}
	return parsed, nil
}

// CreateIssue runs `fp issue create ...`, then applies status and links.
func (f *FPConnector) CreateIssue(issue *Issue) (*Issue, error) {
	command := []string{f.Command, "issue", "create", "--title", issue.Title}
	if issue.Body != nil && *issue.Body != "" {
		command = append(command, "--description", *issue.Body)
	}
	if issue.Parent != nil {
		command = append(command, "--parent", issue.Parent.ID)
	}
	if issue.Priority != nil {
		command = append(command, "--priority", fpPriorityName(*issue.Priority))
	}
	var depends []string
	for _, l := range issue.Links {
		if l.Type == "blocked_by" || l.Type == "relates_to" {
			depends = append(depends, l.Target.ID)
		}
	}
	if len(depends) > 0 {
		command = append(command, "--depends", strings.Join(depends, ","))
	}
	out, err := f.run(command, f.Cwd)
	if err != nil {
		return nil, err
	}
	id, err := fpExtractID(out)
	if err != nil {
		return nil, err
	}
	ref, err := NewRef(f.Name(), f.Workspace, id, nil, nil)
	if err != nil {
		return nil, err
	}
	created, err := f.GetIssue(ref)
	if err != nil {
		return nil, err
	}
	if issue.Status != "todo" {
		created, err = f.UpdateIssue(created.Ref, &Patch{Fields: []string{"status"}, Src: &Issue{Status: issue.Status}})
		if err != nil {
			return nil, err
		}
	}
	for _, l := range issue.Links {
		var target, dep string
		switch l.Type {
		case "blocks":
			target, dep = l.Target.ID, created.Ref.ID
		case "blocked_by", "relates_to":
			target, dep = created.Ref.ID, l.Target.ID
		default:
			continue
		}
		if _, err := f.run([]string{f.Command, "issue", "update", "--depends", dep, target}, f.Cwd); err != nil {
			return nil, err
		}
	}
	return created, nil
}

// UpdateIssue runs `fp issue update` (status/priority only).
func (f *FPConnector) UpdateIssue(ref ExternalRef, patch *Patch) (*Issue, error) {
	var unsupported []string
	for _, k := range []string{"assignees", "custom_fields", "labels", "issue_type", "parent"} {
		if patch.Has(k) {
			unsupported = append(unsupported, k)
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return nil, capabilityError("FPConnector does not currently support patch keys: " + strings.Join(unsupported, ", "))
	}
	if patch.Has("title") || patch.Has("body") {
		return nil, capabilityError("FPConnector update currently supports status/priority/dependencies only")
	}
	command := []string{f.Command, "issue", "update"}
	if patch.Has("status") {
		command = append(command, "--status", fpStatus(patch.Src.Status))
	}
	if patch.Has("priority") && patch.Src.Priority != nil {
		command = append(command, "--priority", fpPriorityName(*patch.Src.Priority))
	}
	if len(command) == 3 {
		return f.GetIssue(ref)
	}
	command = append(command, ref.ID)
	if _, err := f.run(command, f.Cwd); err != nil {
		return nil, err
	}
	return f.GetIssue(ref)
}

func fpToCanonicalStatus(s string) string {
	m := map[string]string{"todo": "todo", "in-progress": "in_progress", "in_progress": "in_progress", "done": "done", "blocked": "blocked",
		"in-review": "in_review", "in_review": "in_review", "canceled": "canceled", "cancelled": "canceled"}
	if v, ok := m[strings.ToLower(strings.TrimSpace(s))]; ok {
		return v
	}
	return "todo"
}

func fpStatus(s string) string {
	return map[string]string{"todo": "todo", "in_progress": "in-progress", "in_review": "in-progress", "blocked": "in-progress", "done": "done", "canceled": "done"}[s]
}

func fpPriority(v string) *int {
	n := strings.ToLower(strings.TrimSpace(v))
	if n == "" {
		return nil
	}
	if p, ok := map[string]int{"high": 1, "medium": 2, "low": 3}[n]; ok {
		return &p
	}
	p, err := strconv.Atoi(n)
	if err != nil {
		return nil
	}
	p = max(0, min(4, p))
	return &p
}

func fpPriorityName(p int) string {
	switch {
	case p <= 1:
		return "high"
	case p == 2:
		return "medium"
	}
	return "low"
}
