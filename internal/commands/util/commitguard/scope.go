package commitguard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// workspaceContext is the subset of workspace.context.WorkspaceContext the
// guard reads.
type workspaceContext struct {
	WPID, MissionSlug, BranchName, LaneID string
	CurrentWP                             string
	LaneWPIDs                             []string
}

var contextRequired = []string{"wp_id", "mission_slug", "worktree_path", "branch_name", "base_branch", "base_commit",
	"dependencies", "created_at", "created_by", "vcs_backend", "lane_id", "lane_wp_ids"}

// listContexts is list_contexts(repo_root): .kittify/workspaces/*.json in
// name order, malformed files skipped. Like upstream it creates the
// workspaces directory.
func listContexts(repoRoot string) []workspaceContext {
	dir := filepath.Join(repoRoot, ".kittify", "workspaces")
	_ = os.MkdirAll(dir, 0o755)
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Slice(matches, func(i, j int) bool { return filepath.Base(matches[i]) < filepath.Base(matches[j]) })
	var out []workspaceContext
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		v, err := pyjson.LoadsBytes(b)
		if err != nil {
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			continue
		}
		lane, _ := o.Get("lane_id")
		if !pyjson.Truthy(lane) {
			continue
		}
		ids, ok := mustGet(o, "lane_wp_ids").([]any)
		if !ok {
			continue
		}
		complete := true
		for _, f := range contextRequired {
			if !o.Has(f) {
				complete = false
			}
		}
		if !complete {
			continue
		}
		c := workspaceContext{
			WPID:        pyjson.Str(mustGet(o, "wp_id")),
			MissionSlug: pyjson.Str(mustGet(o, "mission_slug")),
			LaneID:      pyjson.Str(lane),
		}
		if s, ok := mustGet(o, "branch_name").(string); ok {
			c.BranchName = s
		} else {
			c.BranchName = "\x00" // never equal to a real branch
		}
		if cw := mustGet(o, "current_wp"); pyjson.Truthy(cw) {
			c.CurrentWP = pyjson.Str(cw)
		}
		for _, id := range ids {
			c.LaneWPIDs = append(c.LaneWPIDs, pyjson.Str(id))
		}
		out = append(out, c)
	}
	return out
}

// reduceLanes approximates the canonical status reducer: the latest
// transition (by at, then event_id) per work package wins.
func reduceLanes(missionDir string, warn io.Writer) (map[string]string, error) {
	if _, err := os.Stat(status.EventsPath(missionDir)); err != nil {
		slug := filepath.Base(missionDir)
		return nil, fmt.Errorf("Canonical status not found for mission '%s'. Run 'spec-kitty agent mission finalize-tasks --mission %s' to bootstrap the event log.", slug, slug)
	}
	st, err := status.ReadStream(missionDir, "")
	if err != nil {
		return nil, err
	}
	// read_events resolves a missing mission_id through the slug's meta.json
	// and logs once per slug when that file is absent.
	warned := map[string]bool{}
	for _, e := range st.Transitions {
		if e.MissionID != nil && *e.MissionID != "" || e.MissionSlug == "" || warned[e.MissionSlug] || strings.ContainsAny(e.MissionSlug, `/\`) {
			continue
		}
		warned[e.MissionSlug] = true
		if _, err := os.Stat(filepath.Join(filepath.Dir(missionDir), e.MissionSlug, "meta.json")); err != nil && warn != nil {
			uconsole.LogWarning(warn, fmt.Sprintf("No meta.json found for mission_slug %s (orphaned event); mission_id will be None for these events", pyjson.ReprString(e.MissionSlug)))
		}
	}
	evs := append([]*status.Event{}, st.Transitions...)
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].At != evs[j].At {
			return evs[i].At < evs[j].At
		}
		return evs[i].EventID < evs[j].EventID
	})
	lanes := map[string]string{}
	for _, e := range evs {
		lanes[e.WPID] = string(e.To)
	}
	return lanes, nil
}

func diag(c workspaceContext, code, msg, wpID string) *Scope {
	return &Scope{ActiveWPID: wpID, LaneID: c.LaneID, ContextSource: "canonical_status", DiagnosticCode: code, DiagnosticMessage: code + ": " + msg}
}

// ResolveScope is _detect_ownership_scope / resolve_active_wp_for_branch.
func ResolveScope(repoRoot, branch string, warn io.Writer) *Scope {
	var matching []workspaceContext
	for _, c := range listContexts(repoRoot) {
		if c.BranchName == branch {
			matching = append(matching, c)
		}
	}
	if len(matching) == 0 {
		return &Scope{ContextSource: "absent"}
	}
	if len(matching) > 1 {
		var ids []string
		for _, c := range matching {
			ids = append(ids, c.LaneID)
		}
		sort.Strings(ids)
		return &Scope{ContextSource: "workspace_context", DiagnosticCode: "ACTIVE_WP_CONTEXT_AMBIGUOUS",
			DiagnosticMessage: "ACTIVE_WP_CONTEXT_AMBIGUOUS: Multiple workspace contexts match branch " + branch + "; lanes: " + strings.Join(ids, ", ")}
	}
	c := matching[0]
	missionDir := filepath.Join(repoRoot, "kitty-specs", c.MissionSlug)
	laneWPs := c.LaneWPIDs
	if len(laneWPs) == 0 {
		for _, id := range []string{c.CurrentWP, c.WPID} {
			if id != "" {
				laneWPs = append(laneWPs, id)
			}
		}
	}
	if st, err := os.Stat(missionDir); err != nil || !st.IsDir() {
		return diag(c, "ACTIVE_WP_CONTEXT_MISSING", "Mission directory not found: "+missionDir, "")
	}
	lanes, err := reduceLanes(missionDir, warn)
	if err != nil {
		return diag(c, "ACTIVE_WP_STATUS_UNAVAILABLE", fmt.Sprintf("Could not read canonical status for mission %s: %v", c.MissionSlug, err), "")
	}
	var active []string
	for _, id := range laneWPs {
		if lanes[id] == "in_progress" {
			active = append(active, id)
		}
	}
	if len(active) != 1 {
		cands := "none"
		if len(active) > 0 {
			cands = strings.Join(active, ", ")
		}
		var states []string
		for _, id := range laneWPs {
			l, ok := lanes[id]
			if !ok {
				l = "uninitialized"
			}
			states = append(states, id+"="+l)
		}
		return diag(c, "ACTIVE_WP_CONTEXT_AMBIGUOUS", fmt.Sprintf("Cannot prove active WP for branch %s; lane_id=%s; active candidates: %s; lane states: %s",
			branch, c.LaneID, cands, strings.Join(states, ", ")), "")
	}
	activeID := active[0]
	var warnings []string
	if c.CurrentWP != "" && c.CurrentWP != activeID {
		warnings = append(warnings, fmt.Sprintf("ACTIVE_WP_CONTEXT_STALE: workspace context current_wp=%s, canonical active_wp=%s; lane_id=%s", c.CurrentWP, activeID, c.LaneID))
	}
	path := findWPFile(filepath.Join(missionDir, "tasks"), activeID)
	if path == "" {
		return diag(c, "ACTIVE_WP_METADATA_MISSING", "Could not find task file for active_wp="+activeID, activeID)
	}
	w, err := wp.Read(path)
	if err != nil {
		return diag(c, "ACTIVE_WP_METADATA_INVALID", fmt.Sprintf("Could not read task frontmatter for active_wp=%s: %v", activeID, err), activeID)
	}
	return &Scope{OwnedFiles: w.List("owned_files"), ActiveWPID: activeID, LaneID: c.LaneID, ContextSource: "canonical_status", Warnings: warnings}
}

// findWPFile is _find_wp_file: first sorted tasks/<id>*.md.
func findWPFile(tasksDir, id string) string {
	if st, err := os.Stat(tasksDir); err != nil || !st.IsDir() {
		return ""
	}
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, id) && strings.HasSuffix(n, ".md") && !strings.HasPrefix(n, ".") {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return filepath.Join(tasksDir, names[0])
}
