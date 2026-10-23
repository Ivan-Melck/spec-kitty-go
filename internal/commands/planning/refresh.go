package planning

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// --refresh-planning-commit / --allow-orphaned (#4141, #4827): a primary-only
// lanes.json planning-pin write (mission_finalize_planning_pin.py).

// classifyPin is planning_commit_classify.classify_recorded_pin.
func (f *finalizeRun) classifyPin(recorded, tip string) string {
	switch {
	case tip == "" || recorded == "":
		return "indeterminate"
	case gitx.IsAncestor(f.root, recorded, tip):
		return "advanced"
	case gitx.OK(f.root, "cat-file", "-e", recorded+"^{commit}"):
		return "orphaned"
	}
	return "foreign"
}

func (f *finalizeRun) refuseRefresh(msg string) error {
	return f.emitErr(msg)
}

// refusePinRefresh is _refuse_planning_pin_refresh.
func (f *finalizeRun) refusePinRefresh(reason string) error {
	return f.emitErr("Cannot refresh planning_commit_sha safely: "+reason, "error_code", "PLANNING_REFRESH_FAIL_CLOSED")
}

// refreshPin is _preserve_or_capture_planning_commit_sha(refresh=True).
func (f *finalizeRun) refreshPin() (*planningPin, error) {
	if !f.executionHasBegun() {
		prev := ""
		if m, _ := lanes.Read(f.dir); m != nil && m.PlanningCommitSHA != nil {
			prev = *m.PlanningCommitSHA
		}
		tip := branchTip(f.root, f.target)
		return &planningPin{sha: tip, action: "refreshed", previous: prev, tip: tip, refresh: true}, nil
	}
	existing, _ := lanes.Read(f.dir)
	if existing == nil {
		return nil, f.emitErr(fmt.Sprintf("Cannot re-finalize mission %s: execution has begun (a WP is past 'planned') but no lanes.json exists on disk to preserve planning provenance from. Refusing to write a new lanes.json rather than guess a planning_commit_sha. Run 'spec-kitty doctor mission-state --fix --mission %s' to rebuild lanes.json from the event log.", mission.PyRepr(f.slug), f.slug))
	}
	recorded := ""
	if existing.PlanningCommitSHA != nil {
		recorded = *existing.PlanningCommitSHA
	}
	tip := branchTip(f.root, f.target)
	if tip == "" {
		return nil, f.refuseRefresh(fmt.Sprintf("Cannot refresh planning_commit_sha for mission %s: the tip of target branch %s could not be captured. Refusing to refresh rather than silently preserve or guess.", mission.PyRepr(f.slug), mission.PyRepr(f.target)))
	}
	switch f.classifyPin(recorded, tip) {
	case "foreign":
		return nil, f.refuseRefresh(fmt.Sprintf("Cannot refresh planning_commit_sha for mission %s: the recorded SHA %s is not present in this repository (object absent, not merely unreachable). Refusing to re-point to a SHA that cannot be inspected; investigate how it was recorded.", mission.PyRepr(f.slug), recorded))
	case "orphaned":
		if !f.allowOrphaned {
			return nil, f.refuseRefresh(fmt.Sprintf("Cannot refresh planning_commit_sha for mission %s: the recorded SHA %s is not an ancestor of the %s tip %s. If this is a deliberate mid-mission rebase (the recorded commit is still present, just no longer reachable from the tip), re-run with --refresh-planning-commit --allow-orphaned to re-point to the live tip. Otherwise the planning history was rewritten/diverged rather than advanced by an amendment; resolve the divergence manually. Refusing to re-point.", mission.PyRepr(f.slug), recorded, mission.PyRepr(f.target), tip))
		}
		return &planningPin{sha: tip, action: "repinned", previous: recorded, tip: tip, refresh: true}, nil
	}
	return &planningPin{sha: tip, action: "refreshed", previous: recorded, tip: tip, refresh: true}, nil
}

// refreshStatusFindings is _refresh_worktree_status_findings.
func (f *finalizeRun) refreshStatusFindings() []string {
	findings := []string{}
	add := func(label, worktree string) {
		entries, err := safecommit.StatusEntries(worktree, "all")
		if err != nil {
			detail := "git status failed"
			if ge, ok := err.(*gitx.Error); ok && strings.TrimSpace(ge.Stderr) != "" {
				detail = strings.TrimSpace(ge.Stderr)
			}
			findings = append(findings, fmt.Sprintf("%s worktree status could not be inspected: refresh could not inspect worktree %s: %s", label, worktree, detail))
			return
		}
		if len(entries) == 0 {
			return
		}
		lines := make([]string, len(entries))
		for i, e := range entries {
			if e.OrigPath != "" {
				lines[i] = e.Code() + " " + e.OrigPath + " -> " + e.Path
			} else {
				lines[i] = e.Code() + " " + e.Path
			}
		}
		if label == "primary" {
			findings = append(findings, "primary worktree has pending changes: "+strings.Join(lines, "\n"))
		} else {
			findings = append(findings, "coordination worktree has pending status/history changes: "+strings.Join(lines, "\n"))
		}
	}
	add("primary", f.root)
	s := coord.Resolve(f.root, f.slug)
	if !coord.RoutesThroughCoordination(s.Topology) {
		return findings
	}
	if s.Meta.GetString("mission_id") == "" {
		return append(findings, "coordination worktree status could not be inspected without writing mission metadata")
	}
	if s.CoordWorktree == "" {
		return findings
	}
	if _, err := os.Stat(s.CoordWorktree); err != nil {
		return findings
	}
	add("coordination", s.CoordWorktree)
	return findings
}

// preflightRefresh is _preflight_refresh_planning_commit.
func (f *finalizeRun) preflightRefresh() error {
	meta := mission.ReadMetaOrEmpty(f.dir)
	existing := meta.GetString("target_branch")
	if f.override != "" && existing != f.target {
		return f.refusePinRefresh("a target-branch override would also change primary meta.json")
	}
	if truthyValue(func() any { v, _ := meta.Get("pr_bound"); return v }()) && existing != f.target {
		return f.refusePinRefresh("legacy PR-bound metadata needs a primary meta.json repair")
	}
	if fs := f.refreshStatusFindings(); len(fs) > 0 {
		return f.refusePinRefresh(fs[0])
	}
	return nil
}

// commitPinRefresh is _commit_planning_pin_refresh (+ _locked).
func (f *finalizeRun) commitPinRefresh(pin *planningPin) error {
	if len(f.wouldModify) > 0 {
		return f.refusePinRefresh("WP frontmatter also needs finalization")
	}
	if f.tasksMDStale {
		return f.refusePinRefresh("tasks.md would need regeneration")
	}
	total, seeded, existing, _ := f.bootstrapCounts()
	if seeded > 0 {
		return f.refusePinRefresh("canonical coordination status would need bootstrap writes")
	}
	lanesPath := filepath.Join(f.dir, lanes.File)
	oldBytes, err := os.ReadFile(lanesPath)
	if err != nil {
		return f.refusePinRefresh("lanes.json could not be read: " + pyOSError(err, lanesPath))
	}
	lm, _ := lanes.Read(f.dir)
	if lm == nil {
		return f.refusePinRefresh("lanes.json is missing or unreadable")
	}
	recorded := ""
	if lm.PlanningCommitSHA != nil {
		recorded = *lm.PlanningCommitSHA
	}
	if recorded != pin.previous {
		return f.refusePinRefresh("lanes.json changed after the planning pin was captured")
	}
	if pin.sha == "" {
		return f.refusePinRefresh("the target branch tip could not be captured")
	}
	boot := jsonx.NewObject("total_wps", total, "newly_seeded", seeded, "already_initialized", existing)
	if recorded == pin.sha {
		if f.json {
			f.emitPinRefreshSuccess(pin, lm, boot, nil, nil)
		} else {
			say(f.ctx.Stdout, fmt.Sprintf("[green]✓[/green] planning_commit_sha already matches the %s tip (%s); no commit needed", f.target, pin.sha))
		}
		return nil
	}
	// _prepare_primary_pin_refresh_commit
	if safecommit.TargetBranch(f.root, f.slug) != f.target {
		return f.refusePinRefresh("lanes.json does not resolve to the planning target branch")
	}
	message := "Add tasks for mission " + f.slug
	if fs := f.refreshStatusFindings(); len(fs) > 0 {
		return f.refusePinRefresh(fs[0])
	}
	if _, err := safecommit.Preflight(f.root, f.root, f.target, []string{lanesPath}, safecommit.Options{}); err != nil {
		return f.refusePinRefresh("primary lanes.json commit preflight failed: " + err.Error())
	}
	expected := pin.tip
	if expected == "" {
		expected = pin.sha
	}
	if cur := branchTip(f.root, f.target); cur != expected {
		curRepr := cur
		if cur == "" {
			curRepr = "None"
		}
		return f.refusePinRefresh(fmt.Sprintf("planning target %s moved after pin capture (%s -> %s); retry against the new tip", mission.PyRepr(f.target), expected, curRepr))
	}
	if b, _ := os.ReadFile(lanesPath); !bytes.Equal(b, oldBytes) {
		return f.refusePinRefresh("lanes.json changed after refresh preparation; concurrent content was left untouched")
	}
	sha := pin.sha
	lm.PlanningCommitSHA = &sha
	if err := lanes.Write(f.dir, lm); err != nil {
		return f.refusePinRefresh("primary lanes.json commit failed: " + err.Error())
	}
	candidate, _ := os.ReadFile(lanesPath)
	r := safecommit.CommitForMission(f.root, f.slug, []string{lanesPath}, message, mission.KindLaneState, safecommit.RouterOptions{})
	if !f.json {
		printRendered(f.ctx.Stdout, r)
	}
	restore := func(detail string) error {
		if cur, err := os.ReadFile(lanesPath); err == nil && bytes.Equal(cur, candidate) {
			os.WriteFile(lanesPath, oldBytes, 0o644)
		} else if err == nil {
			detail += "; lanes.json changed during the failed commit and was left untouched"
		}
		return f.refusePinRefresh(detail)
	}
	if r.Status == safecommit.StatusUnchanged {
		return restore("primary lanes.json changed but the commit seam reported unchanged")
	}
	if !r.Success() {
		d := r.Diagnostic
		if d == "" {
			d = "primary lanes.json commit was refused"
		}
		return restore(d)
	}
	f.commitLanded = true
	if r.Diagnostic != "" && !f.json {
		say(f.ctx.Stdout, "[yellow]Warning:[/yellow] "+r.Diagnostic)
	}
	if f.json {
		f.emitPinRefreshSuccess(pin, lm, boot, r, []string{relOrAbs(f.root, lanesPath)})
		return nil
	}
	prev := pin.previous
	if prev == "" {
		prev = "(none)"
	}
	verb := "Refreshed"
	if pin.action == "repinned" {
		verb = "Re-pinned orphaned"
	}
	say(f.ctx.Stdout, fmt.Sprintf("[green]✓[/green] %s planning_commit_sha %s -> %s (lanes merge the %s tip at their next allocation)", verb, prev, pin.sha, f.target))
	return nil
}

// emitPinRefreshSuccess is _emit_success_report for the refresh path.
func (f *finalizeRun) emitPinRefreshSuccess(pin *planningPin, lm *lanes.Manifest, boot *jsonx.Object, r *safecommit.RouterResult, files []string) {
	commitCreated := false
	var commitHash any
	hashes, surfaces := []any{}, []any{}
	if files == nil {
		files = []string{}
	}
	if r != nil {
		commitCreated = true
		commitHash = optAny(r.CommitHash)
		for _, h := range r.CommitHashes {
			hashes = append(hashes, jsonx.NewObject("branch", h[0], "hash", h[1]))
		}
		surfaces = r.SurfacesPayload()
	}
	refsObj := jsonx.NewObject()
	for _, id := range sortedKeysObj(f.refs) {
		refsObj.Set(id, strList(f.refs[id]))
	}
	p := jsonx.NewObject("result", "success", "wp_count", len(f.workPackages), "updated_wp_count", f.updated, "modified_wps", strList(f.modified),
		"unchanged_wps", strList(f.unchanged), "preserved_wps", strList(f.preserved), "tasks_dir", f.tasksDir, "commit_created", commitCreated,
		"commit_hash", commitHash, "commit_hashes", hashes, "commit_surfaces", surfaces, "files_committed", strList(files),
		"dependencies_parsed", depsObject(f.deps, f.depOrder), "requirement_refs_parsed", refsObj,
		"bootstrap", boot, "lanes", f.lanesStatsFull(lm), "ownership_warnings", strList(f.ownershipWarn),
		"requirement_extraction_warnings", strList(nonNil(f.extractWarns)), "post_integration_acceptance_warnings", strList(f.postIntegWarn),
		"target_branch_override", jsonx.NewObject("requested", nil, "persisted", false, "committed", false, "previous_value", nil, "persist_error", nil),
		"planning_commit", jsonx.NewObject("sha", optAny(pin.sha), "action", pin.action, "previous_sha", optAny(pin.previous), "branch_tip", optAny(pin.tip)),
		"planning_commit_refresh", nil)
	if r != nil && r.Diagnostic != "" {
		p.Set("commit_diagnostic", r.Diagnostic)
	}
	for _, kv := range f.diagnostics.Pairs() {
		p.Set(kv.Key, kv.Value)
	}
	emitJSON(f.ctx.Stdout, p)
}

// lanesStatsFull is the success report's lanes block (count etc. always).
func (f *finalizeRun) lanesStatsFull(m *lanes.Manifest) *jsonx.Object {
	if m == nil {
		return jsonx.NewObject("computed", false, "count", 0, "lane_ids", []any{}, "planning_artifact_wps", []any{}, "collapse_report", nil)
	}
	return f.lanesStats(m)
}

// pyOSError renders an os error like str(OSError) for a path.
func pyOSError(err error, path string) string {
	if os.IsNotExist(err) {
		return "[Errno 2] No such file or directory: " + mission.PyRepr(path)
	}
	if os.IsPermission(err) {
		return "[Errno 13] Permission denied: " + mission.PyRepr(path)
	}
	return err.Error()
}

var _ = cli.Exit
