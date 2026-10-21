package agentcore

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const planningRepoRootReason = "planning_artifact_repo_root_shared_workspace"

// staleResult is core.stale_detection.StaleCheckResult.
type staleResult struct {
	status          string // fresh | stale | not_applicable
	reason          string
	minutes         *float64
	lastCommitTime  string
	workspaceExists bool
	workspaceKind   string
	err             string
}

func (r *staleResult) isStale() bool { return r.status == "stale" }

func (r *staleResult) payload() *jsonx.Object {
	var reason, minutes, last any
	if r.reason != "" {
		reason = r.reason
	}
	if r.minutes != nil {
		minutes = *r.minutes
	}
	if r.lastCommitTime != "" {
		last = r.lastCommitTime
	}
	return jsonx.NewObject("status", r.status, "reason", reason, "minutes_since_commit", minutes, "last_commit_time", last)
}

// apply is _apply_stale_status_fields.
func (r *staleResult) apply(o *jsonx.Object) {
	p := r.payload()
	o.Set("stale", p)
	o.Set("is_stale", r.isStale())
	mv, _ := p.Get("minutes_since_commit")
	o.Set("minutes_since_commit", mv)
	o.Set("worktree_exists", r.workspaceKind != "repo_root" && r.workspaceExists)
}

// label is _render_stale_status.
func (r *staleResult) label() string {
	switch {
	case r == nil:
		return ""
	case r.status == "not_applicable" && r.reason == planningRepoRootReason:
		return "stale: n/a (repo-root planning work)"
	case r.err != "":
		return "stale: unavailable"
	case r.isStale() && r.minutes != nil:
		return "stale: " + pyFloat(*r.minutes) + "m"
	}
	return ""
}

// checkDoingStaleness is check_doing_wps_for_staleness (with the
// build_stale_fallback_results arm when workspace resolution raises).
func checkDoingStaleness(root, slug string, doing []*boardRow, threshold int, snap *status.Snapshot) map[string]*staleResult {
	out := map[string]*staleResult{}
	for _, r := range doing {
		if r.id == "" {
			continue
		}
		ws, err := lanes.ResolveWorkspace(root, slug, r.id)
		if err != nil {
			return staleFallback(doing, err)
		}
		if ws.RunsInCheckoutRoot() {
			out[r.id] = &staleResult{status: "not_applicable", reason: planningRepoRootReason, workspaceExists: ws.Exists(), workspaceKind: ws.ResolutionKind}
			continue
		}
		if !ws.Exists() {
			out[r.id] = &staleResult{status: "fresh", workspaceKind: ws.ResolutionKind}
			continue
		}
		out[r.id] = checkWPStaleness(r.id, ws.WorktreePath, threshold, snap)
	}
	return out
}

func staleFallback(doing []*boardRow, cause error) map[string]*staleResult {
	var me *lanes.MissingError
	_ = errors.As(cause, &me)
	out := map[string]*staleResult{}
	for _, r := range doing {
		if r.id == "" {
			continue
		}
		kind := "unknown"
		if v, ok := r.obj.Get("workspace_kind"); ok {
			kind = status.PyStr(v)
		}
		reason := "stale_detection_unavailable"
		if kind == "repo_root" {
			reason = planningRepoRootReason
		}
		out[r.id] = &staleResult{status: "not_applicable", reason: reason, workspaceKind: kind, err: cause.Error()}
	}
	return out
}

// checkWPStaleness is check_wp_staleness for an existing lane worktree.
func checkWPStaleness(wpID, worktree string, threshold int, snap *status.Snapshot) *staleResult {
	if _, err := os.Stat(worktree); err != nil {
		return &staleResult{status: "fresh", workspaceKind: "lane_workspace"}
	}
	var pid, baseline string
	var state *jsonx.Object
	if snap != nil {
		state = snap.WP(wpID)
	}
	if state != nil {
		if v, ok := state.Get("shell_pid"); ok && v != nil {
			pid = status.PyStr(v)
		}
		if v, ok := state.Get("shell_pid_created_at"); ok && v != nil {
			baseline = status.PyStr(v)
		}
	}
	if pid != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(pid)); err == nil && status.IsClaimingProcessAlive(n, baseline) {
			return &staleResult{status: "fresh", reason: "live_claim_process", workspaceExists: true, workspaceKind: "lane_workspace"}
		}
	}
	last, own := lastMeaningfulCommit(worktree)
	if last == "" {
		r := &staleResult{status: "fresh", workspaceExists: true, workspaceKind: "lane_workspace"}
		if own {
			r.err = "Could not determine last commit time"
		}
		return r
	}
	t, err := clock.ParseISO(last)
	if err != nil {
		return &staleResult{status: "fresh", workspaceExists: true, workspaceKind: "lane_workspace", err: err.Error()}
	}
	mins := clock.Now().Sub(t).Seconds() / 60
	rounded := status.PyRound(mins, 1)
	st := "fresh"
	if mins > float64(threshold) {
		st = "stale"
	}
	iso := last
	if strings.HasSuffix(iso, "Z") {
		iso = strings.TrimSuffix(iso, "Z") + "+00:00"
	}
	return &staleResult{status: st, minutes: &rounded, lastCommitTime: iso, workspaceExists: true, workspaceKind: "lane_workspace"}
}

// lastMeaningfulCommit is get_last_meaningful_commit_time: the %cI stamp of
// HEAD when the branch has commits beyond its merge base with the default
// branch ("" otherwise).
func lastMeaningfulCommit(worktree string) (string, bool) {
	def := defaultBranch(worktree)
	base, err := gitx.Run(worktree, "merge-base", "HEAD", def)
	if err != nil {
		return "", false
	}
	base = strings.TrimSpace(base)
	if out, err := gitx.Run(worktree, "rev-list", "--count", base+"..HEAD"); err == nil {
		if n, _ := strconv.Atoi(strings.TrimSpace(out)); n == 0 {
			return "", false
		}
	}
	out, err := gitx.Run(worktree, "log", "-1", "--format=%cI")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// defaultBranch is stale_detection.get_default_branch.
func defaultBranch(dir string) string {
	if out, err := gitx.Run(dir, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(out)
		return ref[strings.LastIndex(ref, "/")+1:]
	}
	for _, b := range []string{"main", "master", "develop"} {
		if gitx.OK(dir, "rev-parse", "--verify", b) {
			return b
		}
	}
	return "main"
}
