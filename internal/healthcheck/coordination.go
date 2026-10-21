package healthcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Coordination + sparse-checkout health (cli/commands/_coordination_doctor.py).
//
// Deviation: stranded coordination-revert markers
// (.kittify/runtime/merge/*/state.json pending_coord_reconcile) are reported
// only when unparseable; a parseable marker's committed-ref coherence is not
// re-derived and `--fix` does not revert strands.

// CoordFinding is DoctorFinding.
type CoordFinding struct {
	Severity, Message string
	NextStep, Code    *string
	Extra             *jsonx.Object
}

// ToObject renders the JSON row.
func (f CoordFinding) ToObject() *jsonx.Object {
	opt := func(s *string) any {
		if s == nil {
			return nil
		}
		return *s
	}
	ex := f.Extra
	if ex == nil {
		ex = &jsonx.Object{}
	}
	return jsonx.NewObject("severity", f.Severity, "message", f.Message, "next_step", opt(f.NextStep), "error_code", opt(f.Code), "extra", ex)
}

func cf(sev, msg string, next, code *string) CoordFinding {
	return CoordFinding{Severity: sev, Message: msg, NextStep: next, Code: code}
}

const (
	workspaceRecoveryCmd = "spec-kitty doctor workspaces --fix"
	coordBranchAbsent    = "Flatten the mission: remove the `coordination_branch` key from meta.json (the coordination topology was never activated). Then run `spec-kitty migrate backfill-topology` to re-derive and persist the topology."
	staleFixBlocked      = "COORDINATION_BRANCH_STALE_FIX_BLOCKED"
)

var gitVerRe = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

func gitVersionFindings() []CoordFinding {
	out, err := gitx.Run("", "--version")
	m := gitVerRe.FindStringSubmatch(strings.TrimSpace(out))
	if err != nil || m == nil {
		return []CoordFinding{cf("error", "Could not detect git version. spec-kitty requires git >= 2.25.", sp("Install or upgrade git to >= 2.25."), sp("GIT_VERSION_UNDETECTABLE"))}
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	if maj < 2 || (maj == 2 && min < 25) {
		f := cf("error", fmt.Sprintf("git %d.%d is older than the required 2.25. Sparse-checkout exclusion of status files requires the modern non-cone surface.", maj, min),
			sp("Upgrade git to >= 2.25 — see https://git-scm.com/downloads."), sp("GIT_VERSION_TOO_OLD"))
		f.Extra = jsonx.NewObject("detected", fmt.Sprintf("%d.%d", maj, min))
		return []CoordFinding{f}
	}
	return []CoordFinding{cf("ok", fmt.Sprintf("git %d.%d satisfies the >= 2.25 requirement.", maj, min), nil, nil)}
}

func trackedWorktreesFindings(root string) []CoordFinding {
	out, err := gitx.Run(root, "ls-files", "--", ".worktrees")
	if err != nil {
		return nil
	}
	var tracked []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, ".worktrees/") {
			tracked = append(tracked, l)
		}
	}
	if len(tracked) == 0 {
		return []CoordFinding{cf("ok", "No tracked content under .worktrees/.", nil, nil)}
	}
	preview := tracked
	if len(preview) > 10 {
		preview = preview[:10]
	}
	f := cf("error", fmt.Sprintf("%d tracked file(s) under .worktrees/ — this is execution scratch space and must never be committed. Tracked content here drives the #1772 merge-staging failures.", len(tracked)),
		sp("Remove it from version control: `git rm -r --cached .worktrees/` then commit, and ensure `.worktrees/` is gitignored."), sp("TRACKED_WORKTREES_CONTENT"))
	f.Extra = jsonx.NewObject("tracked", strs(preview), "tracked_count", len(tracked), "truncated", len(tracked) > 10)
	return []CoordFinding{f}
}

func strandedMarkerFindings(root string) []CoordFinding {
	var out []CoordFinding
	base := project.Kittify(root, "runtime", "merge")
	for _, n := range sortedChildren(base) {
		b, err := os.ReadFile(filepath.Join(base, n, "state.json"))
		if err != nil {
			continue
		}
		v, err := pyjson.Loads(string(b))
		if err != nil {
			continue
		}
		st, _ := v.(*jsonx.Object)
		marker, ok := valueOf(st, "pending_coord_reconcile").(*jsonx.Object)
		if !ok {
			continue
		}
		parsed := true
		for _, k := range []string{"coord_ref", "captured_sha", "coord_worktree"} {
			if s, ok := valueOf(marker, k).(string); !ok || s == "" {
				parsed = false
			}
		}
		if l, ok := valueOf(marker, "stranded_wp_ids").([]any); !ok || len(l) == 0 {
			parsed = false
		}
		if !parsed {
			f := cf("warning", fmt.Sprintf("Mission %s carries a `pending_coord_reconcile` marker that could not be parsed into repair inputs.", pyjson.Repr(valueOf(st, "mission_slug"))),
				sp("A `pending_coord_reconcile` marker could not be parsed into repair inputs (missing coord_ref/captured_sha/coord_worktree or an empty strand). Inspect `.kittify/runtime/merge/<mission_id>/state.json` and clear or repair the marker."),
				sp("COORDINATION_RECONCILE_MARKER_UNPARSEABLE"))
			f.Extra = jsonx.NewObject("mission_id", valueOf(st, "mission_id"), "mission_slug", valueOf(st, "mission_slug"))
			out = append(out, f)
		}
	}
	return out
}

type coordIdentity struct{ branch, slug, id string }

func coordinationIdentity(meta *jsonx.Object) (*coordIdentity, bool) {
	b, ok := valueOf(meta, "coordination_branch").(string)
	if !ok || b == "" {
		return nil, false
	}
	slug := valueOf(meta, "mission_slug")
	if !pyjson.Truthy(slug) {
		slug = valueOf(meta, "slug")
	}
	s, ok1 := slug.(string)
	id, ok2 := valueOf(meta, "mission_id").(string)
	if !ok1 || !ok2 {
		return &coordIdentity{}, true
	}
	return &coordIdentity{b, s, id}, true
}

func coordShort(slug, id string) string {
	if m := mission.ResolveMid8(slug, id); m != "" {
		return m
	}
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func revParse(dir, ref string) string {
	out, err := gitx.Run(dir, "rev-parse", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func isFF(root, anc, desc string) bool {
	return anc != "" && desc != "" && anc != desc && gitx.OK(root, "merge-base", "--is-ancestor", anc, desc)
}

func headFinding(wt, branch string) *CoordFinding {
	actual, err := gitx.Run(wt, "symbolic-ref", "HEAD")
	if err != nil {
		actual = "<detached>"
	}
	actual = strings.TrimSpace(actual)
	if actual == "refs/heads/"+branch || strings.TrimPrefix(actual, "refs/heads/") == branch {
		return nil
	}
	f := cf("warning", fmt.Sprintf("Coordination worktree %s is on %s, expected %s.", wt, pyjson.ReprString(actual), pyjson.ReprString(branch)),
		sp(fmt.Sprintf("Inspect the worktree, then check out the coordination branch there: `git -C %s checkout %s`.", wt, branch)), sp("COORDINATION_WORKTREE_BRANCH_MISMATCH"))
	return &f
}

func dirty(wt string) bool {
	out, err := gitx.Run(wt, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

func remoteOnlySteps(root, slug, branch string) string {
	return fmt.Sprintf("Run in order: `git -C %s fetch`, then `git -C %s branch %s origin/%s`, then `spec-kitty doctor coordination --mission %s --fix`.", root, root, branch, branch, slug)
}

func coordHealth(root string, meta *jsonx.Object, metaPath string) []CoordFinding {
	ci, ok := coordinationIdentity(meta)
	if !ok {
		return nil
	}
	if ci.slug == "" || ci.id == "" {
		return []CoordFinding{cf("warning", "meta.json carries coordination_branch but is missing mission_slug/mission_id; coord worktree health cannot be verified.", sp("Run `spec-kitty doctor identity --json` for details."), sp("COORDINATION_META_INCOMPLETE"))}
	}
	short := coordShort(ci.slug, ci.id)
	wt := lanes.CoordWorktreePath(root, ci.slug, short)
	if !exists(wt) {
		if !migrate.CoordBranchExists(root, ci.branch) {
			f := cf("warning", fmt.Sprintf("Coordination worktree %s is missing for mission %s and the declared coordination branch %s does not exist in git (never created or deleted).", wt, pyjson.ReprString(ci.slug), pyjson.ReprString(ci.branch)),
				sp(coordBranchAbsent), sp("COORDINATION_WORKTREE_NEVER_CREATED"))
			f.Extra = jsonx.NewObject("meta_path", metaPath)
			return []CoordFinding{f}
		}
		next := fmt.Sprintf("Run: `spec-kitty doctor coordination --mission %s --fix` (or, manually: `git -C %s worktree add %s %s`)", ci.slug, root, wt, ci.branch)
		if !gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+ci.branch) {
			next = remoteOnlySteps(root, ci.slug, ci.branch)
		}
		f := cf("warning", fmt.Sprintf("Coordination worktree %s is missing for mission %s (the branch %s exists).", wt, pyjson.ReprString(ci.slug), pyjson.ReprString(ci.branch)), &next, sp("COORDINATION_WORKTREE_MISSING"))
		f.Extra = jsonx.NewObject("recovery_args", []any{"git", "-C", root, "worktree", "add", wt, ci.branch}, "mission_slug", ci.slug, "mid8", short, "coord_branch", ci.branch)
		return []CoordFinding{f}
	}
	var out []CoordFinding
	if h := headFinding(wt, ci.branch); h != nil {
		out = append(out, *h)
	}
	if dirty(wt) {
		out = append(out, cf("warning", fmt.Sprintf("Coordination worktree %s has uncommitted changes.", wt), sp("Commit or discard the changes inside the coord worktree before next implement/review."), sp("COORDINATION_WORKTREE_DIRTY")))
	}
	head, tip := revParse(wt, "HEAD"), revParse(root, "refs/heads/"+ci.branch)
	if isFF(root, head, tip) {
		out = append(out, cf("warning", fmt.Sprintf("Coordination worktree %s is behind the coord branch %s tip (fast-forward available).", wt, pyjson.ReprString(ci.branch)),
			sp(fmt.Sprintf("Run `%s` to refresh it (fast-forwards stale coord worktrees).", workspaceRecoveryCmd)), sp("COORDINATION_WORKTREE_STALE")))
	}
	if len(out) == 0 {
		out = append(out, cf("ok", fmt.Sprintf("Coordination worktree %s is healthy.", wt), nil, nil))
	}
	return out
}

func laneSparseDrift(root string, meta *jsonx.Object) []CoordFinding {
	ci, ok := coordinationIdentity(meta)
	if !ok || ci.slug == "" || ci.id == "" {
		return nil
	}
	short := coordShort(ci.slug, ci.id)
	d := mission.CoordMissionDirName(ci.slug, short)
	expected := []string{"!kitty-specs/" + d + "/status.events.jsonl", "!kitty-specs/" + d + "/status.json", "/*"}
	wd := filepath.Join(root, project.WorktreesDir)
	if !exists(wd) {
		return nil
	}
	list, _ := gitx.Run(root, "worktree", "list", "--porcelain")
	var out []CoordFinding
	hint := fmt.Sprintf("Run `%s` to restore.", workspaceRecoveryCmd)
	for _, n := range sortedChildren(wd) {
		suffix := strings.TrimPrefix(n, ci.slug+"-")
		if suffix == n || !laneSuffix.MatchString(suffix) {
			continue
		}
		p := filepath.Join(wd, n)
		if !strings.Contains(list, resolve(p)) {
			continue
		}
		gp, err := gitx.Run(p, "rev-parse", "--git-path", "info/sparse-checkout")
		if err != nil {
			out = append(out, cf("warning", fmt.Sprintf("Could not resolve sparse-checkout path for %s.", p), sp(fmt.Sprintf("Run `%s`.", workspaceRecoveryCmd)), sp("LANE_SPARSE_CHECKOUT_DRIFT")))
			continue
		}
		if !filepath.IsAbs(gp) {
			gp = filepath.Join(p, gp)
		}
		b, err := os.ReadFile(gp)
		if err != nil {
			out = append(out, cf("warning", fmt.Sprintf("Lane worktree %s is missing the sparse-checkout policy that excludes status files.", p), &hint, sp("LANE_SPARSE_CHECKOUT_DRIFT")))
			continue
		}
		have := map[string]bool{}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				have[l] = true
			}
		}
		var missing []string
		for _, e := range expected {
			if !have[e] {
				missing = append(missing, e)
			}
		}
		if len(missing) > 0 {
			f := cf("warning", fmt.Sprintf("Lane worktree %s sparse-checkout is missing %d expected pattern(s): %s.", p, len(missing), pyjson.ReprStrings(missing)), &hint, sp("LANE_SPARSE_CHECKOUT_DRIFT"))
			f.Extra = jsonx.NewObject("missing_patterns", strs(missing))
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		out = append(out, cf("ok", "All lane worktrees carry the expected sparse-checkout policy.", nil, nil))
	}
	return out
}

func coordVsTarget(root string, meta *jsonx.Object) (branch, target, csha, tsha string, ok bool) {
	ci, isCoord := coordinationIdentity(meta)
	if !isCoord || ci.slug == "" || ci.id == "" {
		return
	}
	t, isStr := valueOf(meta, "target_branch").(string)
	if !isStr || t == "" {
		return
	}
	c, ts := revParse(root, "refs/heads/"+ci.branch), revParse(root, "refs/heads/"+t)
	if c == "" || ts == "" {
		return
	}
	return ci.branch, t, c, ts, true
}

func stalenessFindings(root string, meta *jsonx.Object) []CoordFinding {
	branch, target, c, t, ok := coordVsTarget(root, meta)
	if !ok || c == t || isFF(root, t, c) {
		return nil
	}
	if isFF(root, c, t) {
		return []CoordFinding{cf("warning", fmt.Sprintf("Coordination branch %s is behind target branch %s (fast-forward available).", pyjson.ReprString(branch), pyjson.ReprString(target)),
			sp("Run `spec-kitty doctor coordination --fix` to fast-forward it."), sp("COORDINATION_BRANCH_STALE_VS_TARGET"))}
	}
	dirName := valueOf(meta, "mission_slug")
	if !pyjson.Truthy(dirName) {
		dirName = valueOf(meta, "slug")
	}
	if s, ok := dirName.(string); ok && s != "" && expectedDivergence(root, branch, target, s) {
		return nil
	}
	return []CoordFinding{cf("warning", fmt.Sprintf("Coordination branch %s has diverged from target branch %s and cannot be fast-forwarded automatically.", pyjson.ReprString(branch), pyjson.ReprString(target)),
		sp("Inspect and reconcile manually; `spec-kitty doctor coordination --fix` will refuse to mutate a diverged coordination branch."), sp("COORDINATION_BRANCH_DIVERGED_VS_TARGET"))}
}

// CollectCoordination is _collect_coordination_findings over mission dir names.
func CollectCoordination(root string, names []string, staleness bool) []CoordFinding {
	var out []CoordFinding
	out = append(out, gitVersionFindings()...)
	out = append(out, trackedWorktreesFindings(root)...)
	out = append(out, strandedMarkerFindings(root)...)
	for _, n := range names {
		dir := project.Specs(root, n)
		meta, err := mission.ReadMeta(dir)
		if err != nil || meta == nil {
			continue
		}
		out = append(out, coordHealth(root, meta, filepath.Join(dir, "meta.json"))...)
		out = append(out, laneSparseDrift(root, meta)...)
		if staleness {
			out = append(out, stalenessFindings(root, meta)...)
		}
	}
	return out
}

func remoteSkipReason(root, branch string) string {
	remotes, _ := gitx.Run(root, "remote")
	for _, r := range strings.Fields(remotes) {
		res, err := gitx.Exec(root, nil, nil, "ls-remote", "--heads", r, branch)
		if err != nil || res.Code != 0 {
			return "could not be verified against the remote (network error or timeout); leaving meta.json unchanged until connectivity is restored -- not flattened"
		}
		if strings.TrimSpace(res.Stdout) != "" {
			return "still exists on a remote; run `git fetch` to materialize it locally -- not flattened"
		}
	}
	return ""
}

// FixCoordination applies the --fix handlers; it prints progress lines via
// say and returns warning/error findings to fold into the post-fix report.
func FixCoordination(root string, names []string, findings []CoordFinding, say func(string)) []CoordFinding {
	var extra []CoordFinding
	var fixed []string
	for _, f := range findings {
		if f.Code == nil || *f.Code != "COORDINATION_WORKTREE_NEVER_CREATED" || f.Extra == nil {
			continue
		}
		mp := f.Extra.GetString("meta_path")
		if mp == "" {
			continue
		}
		dir := filepath.Dir(mp)
		meta := mission.ReadMetaOrEmpty(dir)
		branch, _ := valueOf(meta, "coordination_branch").(string)
		if branch == "" {
			continue
		}
		if r := remoteSkipReason(root, branch); r != "" {
			say(fmt.Sprintf("[yellow]Skipped:[/yellow] %s/meta.json -- coordination branch %s %s", filepath.Base(dir), pyjson.ReprString(branch), r))
			continue
		}
		meta.Delete("coordination_branch")
		meta.Delete("topology")
		meta.Set("flattened", true)
		mission.WriteMeta(dir, meta, false)
		fixed = append(fixed, filepath.Base(dir))
	}
	for _, s := range fixed {
		say(fmt.Sprintf("[green]Flattened:[/green] removed coordination_branch from %s/meta.json", s))
	}
	if len(fixed) > 0 {
		for _, s := range fixed {
			slug := s
			migrate.BackfillTopology(root, false, &slug, true)
		}
		say("[green]Topology backfilled.[/green] Run `spec-kitty doctor coordination` to verify.")
	}
	for _, f := range findings {
		if f.Code == nil || *f.Code != "COORDINATION_WORKTREE_MISSING" || f.Extra == nil {
			continue
		}
		slug, branch := f.Extra.GetString("mission_slug"), f.Extra.GetString("coord_branch")
		if slug == "" {
			continue
		}
		args, _ := valueOf(f.Extra, "recovery_args").([]any)
		wt := ""
		if len(args) >= 7 {
			wt = pyjson.Str(args[5])
		}
		if !gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) {
			next := remoteOnlySteps(root, slug, branch)
			ff := cf("warning", fmt.Sprintf("Could not materialize the coordination worktree for mission %s: the coordination branch %s exists only as a remote-tracking ref. %s", pyjson.ReprString(slug), pyjson.ReprString(branch), next), &next, f.Code)
			ff.Extra = jsonx.NewObject("mission_slug", slug)
			extra = append(extra, ff)
			continue
		}
		gitx.Run(root, "worktree", "prune")
		os.MkdirAll(filepath.Dir(wt), 0o755)
		if _, err := gitx.Run(root, "worktree", "add", wt, branch); err != nil {
			next := fmt.Sprintf("git -C %s worktree add %s %s", root, wt, branch)
			ff := cf("warning", fmt.Sprintf("Could not materialize the coordination worktree for mission %s: %s", pyjson.ReprString(slug), next), &next, f.Code)
			ff.Extra = jsonx.NewObject("mission_slug", slug)
			extra = append(extra, ff)
			continue
		}
		say(fmt.Sprintf("[green]Materialized:[/green] coordination worktree for mission %s.", pyjson.ReprString(slug)))
	}
	// Gap-1 fast-forward
	for _, n := range names {
		meta, err := mission.ReadMeta(project.Specs(root, n))
		if err != nil || meta == nil {
			continue
		}
		branch, target, c, t, ok := coordVsTarget(root, meta)
		if !ok || c == t || isFF(root, t, c) {
			continue
		}
		blocked := func(reason string) {
			msg := fmt.Sprintf("Refusing to fast-forward: coordination branch %s is %s target branch %s — `--fix` mutates nothing.", pyjson.ReprString(branch), reason, pyjson.ReprString(target))
			if d, err := gitx.Run(root, "diff", branch, target); err == nil && d != "" {
				msg += "\n" + d + "\n"
			}
			extra = append(extra, cf("error", msg, sp("Inspect the diff above and reconcile manually; `--fix` will not mutate a diverged, dirty, or mismatched coordination worktree."), sp(staleFixBlocked)))
		}
		if !isFF(root, c, t) {
			blocked("diverged from")
			continue
		}
		ci, _ := coordinationIdentity(meta)
		wt := lanes.CoordWorktreePath(root, ci.slug, coordShort(ci.slug, ci.id))
		if !exists(wt) {
			continue
		}
		if h := headFinding(wt, branch); h != nil {
			actual, err := gitx.Run(wt, "symbolic-ref", "HEAD")
			if err != nil {
				actual = "<detached>"
			}
			extra = append(extra, cf("error", fmt.Sprintf("Refusing to fast-forward coordination branch %s: coordination worktree %s is on %s, not %s. `--fix` mutates nothing.", pyjson.ReprString(branch), wt, pyjson.ReprString(strings.TrimSpace(actual)), pyjson.ReprString(branch)),
				h.NextStep, sp(staleFixBlocked)))
			continue
		}
		if dirty(wt) {
			blocked("not cleanly fast-forwardable vs")
			continue
		}
		if _, err := gitx.Run(wt, "merge", "--ff-only", t); err != nil {
			extra = append(extra, cf("error", fmt.Sprintf("Refusing to fast-forward: coordination branch %s failed to fast-forward to target branch %s — %v", pyjson.ReprString(branch), pyjson.ReprString(target), err), nil, sp(staleFixBlocked)))
			continue
		}
		say(fmt.Sprintf("[green]Fast-forwarded:[/green] coordination branch %s (%s -> %s) to match target %s.", pyjson.ReprString(branch), c[:8], t[:8], pyjson.ReprString(target)))
	}
	return extra
}
