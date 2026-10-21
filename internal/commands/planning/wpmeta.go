package planning

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/reqmap"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// wpMetaFields is WPMetadata's field list (declaration order).
var wpMetaFields = []string{"work_package_id", "title", "dependencies", "base_branch", "base_commit", "created_at", "planning_base_branch",
	"merge_target_branch", "branch_strategy", "requirement_refs", "tracker_refs", "priority", "execution_mode", "owned_files", "create_intent",
	"authoritative_surface", "scope", "task_type", "subtasks", "phase", "phases", "assignee", "agent", "model", "agent_profile", "role",
	"shell_pid", "shell_pid_created_at", "history", "lane", "feature_slug", "activity_log", "review_status", "reviewed_by", "approved_by",
	"reviewer", "reviewer_agent", "reviewer_shell_pid", "review_feedback", "review_feedback_file", "subtitle", "description",
	"estimated_duration", "tags", "mission_id", "mission_number", "mission_slug", "status", "wp_code", "work_package_title"}

var wpListFields = map[string]bool{"dependencies": true, "requirement_refs": true, "tracker_refs": true, "owned_files": true,
	"create_intent": true, "subtasks": true, "history": true, "tags": true}

// runtimeFields are re-pointed to the event-sourced snapshot on read.
var runtimeFields = map[string]bool{"shell_pid": true, "shell_pid_created_at": true, "agent": true, "assignee": true}

// wpMetaDump is WPMetadata(frontmatter).model_dump(exclude_none=True) for a
// WP read through read_wp_frontmatter (runtime fields from a snapshot
// without claims). Returns an error for unknown fields (extra="forbid").
func wpMetaDump(meta *jsonx.Object) (*jsonx.Object, error) {
	known := map[string]bool{}
	for _, f := range wpMetaFields {
		known[f] = true
	}
	for _, k := range meta.Keys() {
		if !known[k] {
			return nil, fmt.Errorf("1 validation error for WPMetadata\n%s\n  Extra inputs are not permitted", k)
		}
	}
	src := meta.Clone()
	if !src.Has("title") && src.Has("work_package_title") {
		v, _ := src.Get("work_package_title")
		src.Set("title", v)
	}
	if v, ok := src.Get("dependencies"); ok {
		if s, ok := v.(string); ok {
			st := strings.TrimSpace(s)
			out := []any{}
			if st != "[]" {
				for _, p := range strings.Split(st, ",") {
					if p = strings.TrimSpace(p); p != "" {
						out = append(out, p)
					}
				}
			}
			src.Set("dependencies", out)
		}
	}
	if v, ok := src.Get("requirement_refs"); ok {
		if s, ok := v.(string); ok {
			src.Set("requirement_refs", strList(reqmap.TokenizeRefs(s)))
		}
	}
	if v, ok := src.Get("tracker_refs"); ok {
		if s, ok := v.(string); ok {
			out := []any{}
			for _, p := range strings.Split(s, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			src.Set("tracker_refs", out)
		}
	}
	if v, ok := src.Get("phase"); ok && v != nil {
		if _, isStr := v.(string); !isStr {
			src.Set("phase", fmt.Sprint(v))
		}
	}
	out := &jsonx.Object{}
	for _, f := range wpMetaFields {
		if runtimeFields[f] {
			continue
		}
		v, ok := src.Get(f)
		if !ok {
			if wpListFields[f] {
				out.Set(f, []any{})
			}
			continue
		}
		if v == nil {
			if wpListFields[f] {
				out.Set(f, []any{})
			}
			continue
		}
		out.Set(f, v)
	}
	return out, nil
}

// writeWPMetaDump renders the dumped frontmatter like FrontmatterManager
// (WP_FIELD_ORDER first, block style) keeping w's body.
func writeWPMetaDump(w *wp.WP, dumped *jsonx.Object) error {
	doc := &fm.Doc{Meta: dumped, Body: w.Doc.Body}
	return fm.WriteFileAtomic(w.Path, []byte(fm.Render(doc, fm.WPFieldOrder)), 0o644)
}
