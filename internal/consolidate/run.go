package consolidate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mergedriver"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/originfresh"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/retrospective"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/staleassert"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Exit is a command exit with a code (message already printed).
type Exit struct{ Code int }

func (e *Exit) Error() string { return fmt.Sprintf("exit %d", e.Code) }

func exit(code int) error { return &Exit{code} }

// Options are the consolidate command options.
type Options struct {
	Mission        string
	Target         string
	Strategy       string // "" = squash
	DeleteBranch   *bool
	RemoveWorktree *bool
	Push, DryRun   bool
	JSON, Yes      bool
	Resume         bool
	OriginCheck    *string   // --origin-check (nil = environment/default)
	SkipLanes      bool      // --skip-lanes: synthesize a no-lane manifest when lanes.json is absent
	DryRunNotes    []string  // "--attest-* is not applied with --dry-run" notices
	AllowSparse    bool      // --allow-sparse-checkout
	AttestCanceled []string  // --attest-canceled-superseded
	AttestApproved []string  // --attest-approved-reviewed
	AttestReason   string    // --attest-reason
	Stderr         io.Writer // logging records (RichHandler on stderr)
	// SkipReviewArtifact (--skip-review-artifact-check) bypasses the
	// review-artifact consistency gate, recording Note as override evidence.
	SkipReviewArtifact bool
	Note               string
}

// synthesizeNoLaneManifest is _synthesize_no_lane_manifest: one planning
// lane carrying every WP of the status snapshot, landing directly on the
// target branch.
func (r *run) synthesizeNoLaneManifest() *lanes.Manifest {
	target := r.target
	var ids []string
	if snap, err := status.ReduceDir(r.sf.StatusDir()); err == nil {
		ids = snap.WorkPackages.Keys()
		sort.Strings(ids)
	}
	mid := missionIDOf(r.root, r.slug)
	return &lanes.Manifest{Version: 1, MissionSlug: r.slug, MissionID: &mid, MissionBranch: target,
		TargetBranch: target, Lanes: []lanes.Lane{{LaneID: lanes.PlanningLaneID, WPIDs: ids}},
		ComputedAt: clock.NowUTCISO(), ComputedFrom: "skip-lanes direct-on-target synthesis (T021, FR-012)"}
}

var compact = jsonx.Options{Indent: -1, EnsureASCII: true}

func say(w io.Writer, s string) { rich.Print(w, s) }

func sayErr(w io.Writer, msg string) { rich.Print(w, "[red]Error:[/red] "+msg) }

// Retention is RetentionDecision.
type Retention struct {
	DeleteBranch, RemoveWorktree bool
	BranchSource, WorktreeSource string
	Warnings, Notices            []string
}

func resolveOne(label string, explicit *bool, raw any, present bool) (bool, string, string, string) {
	malformed := present && raw != nil
	if _, ok := raw.(bool); ok {
		malformed = false
	}
	retains := raw == true || malformed
	if explicit != nil {
		notice := ""
		if *explicit && retains {
			notice = "explicit delete overrode retention policy for " + label
		}
		return *explicit, "cli", "", notice
	}
	if raw == true {
		return false, "meta", "retention honored for " + label + " (source: meta.json)", ""
	}
	if malformed {
		return false, "meta", fmt.Sprintf("malformed retain_%s value in meta.json (%s); treated as retaining (fail-closed)", label, jsonx.Dumps(raw, compact)), ""
	}
	return true, "default", "", ""
}

// ResolveRetention is resolve_merge_retention.
func ResolveRetention(primaryDir string, del, rm *bool) Retention {
	meta := mission.ReadMetaOrEmpty(primaryDir)
	rb, okb := meta.Get("retain_branches")
	rw, okw := meta.Get("retain_worktrees")
	var r Retention
	var w1, w2, n1, n2 string
	r.DeleteBranch, r.BranchSource, w1, n1 = resolveOne("branches", del, rb, okb)
	r.RemoveWorktree, r.WorktreeSource, w2, n2 = resolveOne("worktrees", rm, rw, okw)
	for _, w := range []string{w1, w2} {
		if w != "" {
			r.Warnings = append(r.Warnings, w)
		}
	}
	for _, n := range []string{n1, n2} {
		if n != "" {
			r.Notices = append(r.Notices, n)
		}
	}
	return r
}

// ResolveMissionSlug is _resolve_mission_slug: a handle to a primary dir
// name (raw handle when unresolved), or the mission of the current branch.
func ResolveMissionSlug(root, handle string) (string, error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		p := lanes.ParseBranch(gitx.CurrentBranch(root))
		if p == nil {
			return "", nil
		}
		return p.MissionSlug, nil
	}
	if mission.SafePathSegment(handle) {
		if st, err := os.Stat(project.Specs(root, handle)); err == nil && st.IsDir() {
			return handle, nil
		}
		name, err := mission.BareModernDirName(root, handle)
		if err != nil {
			return "", err
		}
		if name != "" {
			return name, nil
		}
	}
	if r, err := mission.ResolveIdentity(root, handle); err == nil {
		return r.Slug, nil
	}
	return handle, nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Run executes the consolidate command flow after option validation.
func Run(w io.Writer, root string, o Options) error {
	strategy := o.Strategy
	if strategy == "" {
		strategy = "squash"
	}
	slug, err := ResolveMissionSlug(root, o.Mission)
	if err != nil {
		sayErr(w, err.Error())
		return exit(2)
	}
	jsonErr := func(msg string, code int) error {
		if o.JSON {
			fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("spec_kitty_version", app.Version, "error", msg), compact))
		} else {
			sayErr(w, msg)
		}
		return exit(code)
	}
	if slug != "" && !dirExists(project.Specs(root, slug)) {
		return jsonErr("Mission not found: "+slug, 1)
	}
	sf := (*coord.Surface)(nil)
	target := o.Target
	if slug != "" {
		sf = coord.Resolve(root, slug)
		if target == "" {
			target = strings.TrimSpace(sf.Meta.GetString("target_branch"))
		}
	}
	if target == "" {
		target = primaryBranch(root)
	}
	if o.JSON && !o.DryRun {
		return jsonErr("--json is currently supported with --dry-run only.", 1)
	}
	var ret Retention
	if slug != "" {
		ret = ResolveRetention(sf.PrimaryDir, o.DeleteBranch, o.RemoveWorktree)
	}
	if o.DryRun {
		return dryRun(w, root, slug, target, strategy, ret, o)
	}
	if slug == "" {
		sayErr(w, "Mission slug could not be resolved. Use --mission <slug>.")
		return exit(2)
	}
	if o.Resume {
		st := LoadState(root, missionIDOf(root, slug))
		if st == nil {
			sayErr(w, "No interrupted merge to resume.")
			return exit(1)
		}
		say(w, fmt.Sprintf("[bold cyan]Resume requested[/bold cyan] for %s (%d/%d done)", slug, len(st.CompletedWPs), len(st.WPOrder)))
	}
	r := &run{w: w, root: root, slug: slug, sf: sf, target: target, strategy: strategy, ret: ret, push: o.Push, originCheck: o.OriginCheck, skipLanes: o.SkipLanes, allowSparse: o.AllowSparse, opts: o}
	return r.execute()
}

// PrimaryBranch is resolve_primary_branch.
func PrimaryBranch(root string) string { return primaryBranch(root) }

func primaryBranch(root string) string {
	if out, err := gitx.Run(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(out), "origin/")
	}
	for _, b := range []string{"main", "master", "develop"} {
		if gitx.BranchExists(root, b) {
			return b
		}
	}
	return gitx.CurrentBranch(root)
}

func requireLanes(dir string) (*lanes.Manifest, string) {
	m, err := lanes.Read(dir)
	if err != nil {
		return nil, fmt.Sprintf("Corrupt lanes.json at %s: %v", filepath.Join(dir, lanes.File), err)
	}
	if m == nil {
		return nil, fmt.Sprintf("lanes.json is required for %s. Run 'spec-kitty agent mission finalize-tasks' to compute execution lanes, or if the mission has already begun executing, run 'spec-kitty doctor mission-state --fix --mission <slug>' to rebuild lanes.json from the event log.", dir)
	}
	return m, ""
}

func lanesJSON(m *lanes.Manifest) []any {
	return m.ToObject().GetList("lanes")
}

// NextMissionNumber is assign_next_mission_number over kitty-specs/.
func NextMissionNumber(root string) int {
	entries, _ := os.ReadDir(project.Specs(root))
	max := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := mission.ReadMeta(project.Specs(root, e.Name()))
		if err != nil {
			continue
		}
		if n, ok := mission.MissionNumber(meta); ok && n > max {
			max = n
		}
	}
	return max + 1
}

func needsNumber(primaryDir string) bool {
	meta, err := mission.ReadMeta(primaryDir)
	if err != nil {
		return false
	}
	_, ok := mission.MissionNumber(meta)
	return !ok
}

func dryRun(w io.Writer, root, slug, target, strategy string, ret Retention, o Options) error {
	for _, n := range o.DryRunNotes {
		say(w, "[yellow]Note:[/yellow] "+n)
	}
	errOut := func(msg string) error {
		if o.JSON {
			fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("spec_kitty_version", app.Version, "error", msg), compact))
		} else {
			sayErr(w, msg)
		}
		return exit(1)
	}
	if slug == "" {
		return errOut("Mission slug could not be resolved. Use --mission <slug>.")
	}
	primary := project.Specs(root, slug)
	m, msg := requireLanes(primary)
	if m == nil {
		return errOut(msg)
	}
	if pr := ProtectedStatusTargetRefusal(root, slug, m, nil); pr != nil {
		text := fmt.Sprintf("%s: %s Next step: %s", pr.Code, pr.Message, pr.NextStep)
		if o.JSON {
			fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("spec_kitty_version", app.Version, "error", text, "error_code", pr.Code), compact))
		} else {
			sayErr(w, text)
		}
		return exit(1)
	}
	var all []string
	for _, l := range m.Lanes {
		all = append(all, l.WPIDs...)
	}
	if handled, err := forecastReviewArtifacts(w, o, coord.Resolve(root, slug).StatusDir(), slug, target, all); handled {
		return err
	}
	var would any
	if needsNumber(primary) {
		would = NextMissionNumber(root)
	}
	del := ret.DeleteBranch
	onlyPlanning := true
	for _, l := range m.Lanes {
		if l.LaneID != lanes.PlanningLaneID {
			onlyPlanning = false
		}
	}
	if del && onlyPlanning && m.MissionBranch == m.TargetBranch {
		del = false
	}
	warn := []any{}
	for _, x := range ret.Warnings {
		warn = append(warn, x)
	}
	payload := jsonx.NewObject("spec_kitty_version", app.Version, "mission_slug", slug, "target_branch", target,
		"strategy", strategy, "delete_branch", del, "remove_worktree", ret.RemoveWorktree, "push", o.Push,
		"mission_branch", m.MissionBranch, "lanes", lanesJSON(m), "would_assign_mission_number", would,
		"retention", jsonx.NewObject("branch_source", ret.BranchSource, "worktree_source", ret.WorktreeSource, "warnings", warn))
	if would != nil && !o.JSON {
		say(w, fmt.Sprintf("[cyan]would assign[/cyan] mission_number=%d to mission %s", would, slug))
	}
	fmt.Fprintln(w, jsonx.Dumps(payload, compact))
	return nil
}

// ---------------------------------------------------------------------------

type run struct {
	w            io.Writer
	root         string
	slug         string
	sf           *coord.Surface
	target       string
	strategy     string
	ret          Retention
	m            *lanes.Manifest
	wps          []string
	snap         *status.Snapshot
	number       int
	baseline     string
	squash       string
	push         bool
	skipLanes    bool
	planningOnly bool
	state        *MergeState
	originCheck  *string
	originGated  bool
	coordDone    bool

	claim           *claim
	validatedTips   [][2]string
	anchorShas      []string
	windowBase      string
	allowSparse     bool
	opts            Options
	attested        []string
	verifiedLanding bool
	excluded        map[string]bool
	checkpoint      *coordCheckpoint
	// disposableFn overrides the residue classifier for the checkout being
	// judged (nil: spec-kitty's own bookkeeping only).
	disposableFn func(string) bool
}

func hasRemote(root, name string) bool {
	res, _ := gitx.Exec(root, nil, nil, "remote", "get-url", name)
	return res.Code == 0
}

var linearHistoryTokens = []string{"linear history", "non-linear", "nonlinear", "merge commits", "GH006", "protected branch hook declined", "must not contain merge commits"}

func isLinearHistoryRejection(stderr string) bool {
	h := strings.ToLower(stderr)
	for _, t := range linearHistoryTokens {
		if strings.Contains(h, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// pushPreflight is _enforce_target_branch_sync_preflight: refresh
// origin/<target> and refuse a push from a target behind or diverged from it.
func (r *run) pushPreflight() error {
	target := r.m.TargetBranch
	if !hasRemote(r.root, "origin") {
		return nil
	}
	if err := originfresh.FetchBranches(r.root, "origin", []string{target}, nil); err != nil {
		say(r.w, "[red]Error:[/red] Could not refresh target branch tracking ref before merge.")
		say(r.w, "  diagnostic_code: TARGET_BRANCH_REFRESH_FAILED")
		say(r.w, "  branch_or_work_package: "+target)
		say(r.w, "  violated_invariant: local_target_branch_must_match_tracking_branch")
		say(r.w, "  remediation:")
		say(r.w, fmt.Sprintf("  - Run: git fetch origin %s", target))
		say(r.w, "  - Resolve the fetch problem, then retry spec-kitty consolidate.")
		say(r.w, "  - Spec Kitty stopped before mutating merge state or reconstructing branches.")
		return exit(1)
	}
	if revParse(r.root, "refs/heads/"+target) == "" {
		return nil
	}
	tracking := ""
	if res, _ := gitx.Exec(r.root, nil, nil, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "--verify", "--end-of-options", target+"@{upstream}"); res.Code == 0 && strings.TrimSpace(res.Stdout) != "" {
		tracking = strings.TrimSpace(res.Stdout)
	} else if revParse(r.root, "refs/remotes/origin/"+target) != "" {
		tracking = "origin/" + target
	}
	if tracking == "" {
		return nil
	}
	res, _ := gitx.Exec(r.root, nil, nil, "rev-list", "--left-right", "--count", target+"..."+tracking)
	f := strings.Fields(res.Stdout)
	if res.Code != 0 || len(f) < 2 || f[1] == "0" {
		return nil
	}
	ahead, behind := f[0], f[1]
	state := "behind"
	if ahead != "0" {
		state = "diverged"
	}
	say(r.w, "[red]Error:[/red] Target branch is not synchronized with its tracking branch.")
	say(r.w, "  diagnostic_code: TARGET_BRANCH_NOT_SYNCHRONIZED")
	say(r.w, "  branch_or_work_package: "+target)
	say(r.w, "  violated_invariant: local_target_branch_must_match_tracking_branch")
	say(r.w, "  remediation:")
	lines := []string{
		fmt.Sprintf("Local target branch '%s' is %s relative to '%s' (%s ahead, %s behind).", target, state, tracking, ahead, behind),
		"Spec Kitty stopped before mutating merge state or reconstructing branches.",
		"Refresh remote refs: git fetch origin " + target,
		fmt.Sprintf("Inspect differences: git log --oneline --left-right --cherry-pick %s...%s", target, tracking),
		fmt.Sprintf("Inspect changed paths: git diff --name-only %s...%s", tracking, target),
	}
	if state == "diverged" {
		lines = append(lines, fmt.Sprintf("Recommended: use the focused PR path unless you verified every ahead commit belongs on '%s' now.", target),
			fmt.Sprintf("Do not run 'git push origin %s' just to satisfy this preflight; local target commits may include orchestration history or unrelated missions.", target),
			fmt.Sprintf("Only direct-push '%s' after reviewing the ahead commits and changed paths.", target))
	} else {
		lines = append(lines, fmt.Sprintf("Recommended: update local '%s' from '%s' after reviewing remote-only commits; do not push the local target branch.", target, tracking))
	}
	focused := "kitty/pr/" + r.slug + "-to-" + strings.ReplaceAll(target, "/", "-")
	lines = append(lines, fmt.Sprintf("Focused PR path: git switch -c %s %s", focused, r.m.MissionBranch),
		fmt.Sprintf("Then push it: git push -u origin %s", focused),
		fmt.Sprintf("Open a PR from %s into %s.", focused, target),
		"Do not use reset, rebase, or force-push as part of this preflight remediation.")
	for _, l := range lines {
		say(r.w, "  - "+l)
	}
	return exit(1)
}

const abortedNote = "Merge aborted before any state change. Resolve the reported condition, then re-run spec-kitty consolidate."

func (r *run) execute() error {
	if !r.originGated {
		var laneBranches []string
		if lm, err := lanes.Read(r.sf.PrimaryDir); err == nil && lm != nil {
			done := map[string]bool{}
			st := LoadState(r.root, missionIDOf(r.root, r.slug))
			if st != nil {
				for _, id := range st.CompletedWPs {
					done[id] = true
				}
			}
			laneBranches = originfresh.ApprovedLaneBranches(r.root, r.slug, lm, done)
		}
		warnings, refused := originfresh.Run(r.root, r.slug, originfresh.ResolveMode(r.originCheck), laneBranches,
			LoadState(r.root, missionIDOf(r.root, r.slug)) != nil)
		if refused != nil {
			rich.PrintPlain(r.w, refused.Message)
			return exit(1)
		}
		for _, w := range warnings {
			rich.PrintPlain(r.w, w)
		}
	}
	if err := r.warnOnMaterializationDrift(); err != nil {
		return err
	}
	if !r.allowSparse {
		if msg := SparseCheckoutRefusal(r.root, "spec-kitty consolidate"); msg != "" {
			sayErr(r.w, msg)
			return exit(1)
		}
	} else if r.opts.Stderr != nil {
		if msg := SparseOverrideWarning(r.root, "spec-kitty consolidate", r.slug, missionIDOf(r.root, r.slug), mergeActor(r.root)); msg != "" {
			rich.LogWarning(r.opts.Stderr, msg)
		}
	}
	m, msg := requireLanes(r.sf.PrimaryDir)
	if m == nil && r.skipLanes {
		if _, err := os.Stat(filepath.Join(r.sf.PrimaryDir, lanes.File)); os.IsNotExist(err) {
			m = r.synthesizeNoLaneManifest()
		}
	}
	if m == nil {
		sayErr(r.w, msg)
		return exit(1)
	}
	r.m = m
	r.planningOnly = m.MissionBranch == m.TargetBranch
	for _, l := range m.Lanes {
		if l.LaneID != lanes.PlanningLaneID {
			r.planningOnly = false
		}
	}
	for _, x := range r.ret.Warnings {
		say(r.w, "[yellow]Warning:[/yellow] "+x)
	}
	for _, x := range r.ret.Notices {
		say(r.w, "[yellow]Notice:[/yellow] "+x)
	}
	if st := LoadState(r.root, missionIDOf(r.root, r.slug)); st != nil && st.PushRequested {
		r.push = true
	}
	if r.push {
		if err := r.pushPreflight(); err != nil {
			return err
		}
	}
	if !gitx.BranchExists(r.root, m.MissionBranch) {
		base := "<base-commit>"
		if sha := revParse(r.root, "HEAD"); sha != "" {
			base = sha
			if len(base) > 12 {
				base = base[:12]
			}
		}
		sayErr(r.w, fmt.Sprintf("Missing mission branch: %s. Run: git branch %s %s", m.MissionBranch, m.MissionBranch, base))
		return exit(1)
	}
	if pr := ProtectedStatusTargetRefusal(r.root, r.slug, m, nil); pr != nil {
		sayErr(r.w, pr.Code+": "+pr.Message)
		say(r.w, "  Next step: "+pr.NextStep)
		footer := "Consolidation refused before any branch moved."
		if LoadState(r.root, missionIDOf(r.root, r.slug)) != nil {
			footer = refusedWithEarlierRecord
		}
		rich.PrintPlain(r.w, footer)
		return exit(1)
	}
	if err := r.preflight(); err != nil {
		return err
	}
	if err := r.refuseUnexplainedMoves(); err != nil {
		return err
	}
	var attErr error
	if r.attested, attErr = r.recordAttestations(r.opts.AttestCanceled, r.opts.AttestApproved, r.opts.AttestReason); attErr != nil {
		return attErr
	}
	// The review-artifact consistency gate runs before merge state is
	// loaded or created.
	{
		excluded := r.acceptablyCanceled()
		var ids []string
		for _, l := range m.Lanes {
			for _, id := range l.WPIDs {
				if !excluded[id] {
					ids = append(ids, id)
				}
			}
		}
		if err := r.enforceReviewArtifacts(ids); err != nil {
			return err
		}
	}
	// merge-ready precondition
	snap, err := status.ReduceDir(r.sf.StatusDir())
	if err != nil {
		sayErr(r.w, err.Error())
		return exit(1)
	}
	r.snap = snap
	r.excluded = r.acceptablyCanceled()
	for _, l := range m.Lanes {
		for _, id := range l.WPIDs {
			if !r.excluded[id] {
				r.wps = append(r.wps, id)
			}
		}
	}
	var missing []string
	for _, id := range r.wps {
		st := snap.WP(id)
		lane := ""
		if st != nil {
			lane = st.GetString("lane")
		}
		if lane != "approved" && lane != "done" {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		say(r.w, fmt.Sprintf("\n[red]Error:[/red] Mission is not merge-ready — WP(s) missing review approval: %s.", strings.Join(missing, ", ")))
		say(r.w, "  No lane consolidation and no mission_number bake have occurred; the mission is unchanged. Move the listed WP(s) through review (approved/done), or cancel them with operator provenance, then re-run the merge.")
		return exit(1)
	}
	mid := missionIDOf(r.root, r.slug)
	st := LoadState(r.root, mid)
	if st != nil {
		done := 0
		for _, id := range st.CompletedWPs {
			if contains(st.WPOrder, id) {
				done++
			}
		}
		say(r.w, fmt.Sprintf("[bold cyan]Resuming[/bold cyan] merge for %s (%d/%d WPs already done)", r.slug, done, len(st.WPOrder)))
	}
	say(r.w, "[bold]Lane-based merge for "+r.slug+"[/bold]")
	say(r.w, "  Mission branch: "+m.MissionBranch)
	var ids []string
	for _, l := range m.Lanes {
		ids = append(ids, l.LaneID)
	}
	say(r.w, "  Lanes: "+strings.Join(ids, ", "))
	if r.planningOnly {
		say(r.w, "  [dim]Planning-artifact-only mission: target branch already contains deliverables; branch merge steps will be skipped.[/dim]")
	}
	gatesPass := true
	for _, g := range EvaluateGates(r.root, r.sf.PrimaryDir, r.sf.StatusDir(), r.sf.StatusDir(), r.wps, LoadGatePolicy(r.root)) {
		icon := "[green]✓[/green]"
		if g.Verdict != "pass" {
			icon = "[yellow]⚠[/yellow]"
			if g.Blocking {
				icon = "[red]✗[/red]"
				gatesPass = false
			}
		}
		say(r.w, fmt.Sprintf("  %s Gate %s: %s", icon, g.Name, g.Details))
	}
	if !gatesPass {
		say(r.w, "\n[red]Error:[/red] Merge gates failed.")
		return exit(1)
	}
	isResume := st != nil
	r.verifiedLanding = isResume && st.ReconPassed != "" && st.ReconPassed == liveTip(r.root, r.m.TargetBranch)
	if err := r.captureClaim(st); err != nil {
		return err
	}
	if st == nil && mid != "" {
		st = newState(r.root, r.slug, mid, r.m.TargetBranch, r.strategy, r.m, r.wps, r.ret, r.push, r.coordRef(), r.excluded)
		st.Save(r.root)
	}
	if st != nil {
		st.captureSnapshot(r.root, r.m, r.coordRef(), isResume)
		for _, b := range missingSnapshotBranches(r.root, r.m, r.coordRef()) {
			say(r.w, fmt.Sprintf("[yellow]Warning:[/yellow] branch %s does not exist and is not snapshotted; a rollback will not cover it.", pyRepr(b)))
		}
		unexplained := st.beginAttempt(r.root)
		if len(unexplained) > 0 && r.verifiedLanding {
			var kept []string
			for _, b := range unexplained {
				if b != st.TargetBranch && b != r.coordRef() {
					kept = append(kept, b)
				}
			}
			unexplained = kept
		}
		if len(unexplained) > 0 {
			if !isResume {
				ClearState(r.root, mid)
			}
			var rows [][3]string
			tips := st.movableTips(r.root)
			for _, b := range unexplained {
				live, _ := tips.get(b)
				rows = append(rows, [3]string{b, st.restoreTarget(b), live})
			}
			return exitUnexplained(r.w, rows)
		}
	}
	r.state = st

	restore := activateDrivers(r.root)
	defer restore()
	r.baseline = revParse(r.root, "refs/heads/"+m.TargetBranch)
	r.coordDone = coordDoneOf(r)
	if r.verifiedLanding {
		// #5021: this target tip already PASSed reconciliation in an earlier
		// attempt; finish the teardown without re-running the phases.
	} else if err := r.mutationSpan(r.coordDone); err != nil {
		if e, ok := err.(*Exit); ok && e.Code != 0 && r.state != nil {
			rich.PrintPlain(r.w, r.state.Rollback(r.root).Render())
		}
		return err
	}
	if r.state != nil {
		r.state.settle(r.root, r.m.TargetBranch)
	}
	if r.strategy == "squash" {
		say(r.w, "  [green]✓[/green] Reconciliation verified: approved-WP claim integrity and squash content attribution verified (no un-attributable content on the target); per-SHA approved-reachability deferred under squash strategy.")
	} else {
		say(r.w, "  [green]✓[/green] Reconciliation verified: approved-WP commit reachability on the target (no excluded commit reachable).")
	}
	return r.afterReconcile()
}

func coordDoneOf(r *run) bool { return r.sf.Routed() && dirExists(r.sf.CoordWorktree) }

// mutationSpan is the executor's single rollback door span: lane merges
// through the reconciliation gate. A failure inside it rolls every
// snapshotted branch back.
func (r *run) mutationSpan(coordDone bool) error {
	if err := r.phase(r.mergeLanes); err != nil {
		return err
	}
	if err := r.phase(func() error {
		if err := r.bake(); err != nil {
			return err
		}
		if coordDone {
			return r.recordDone()
		}
		return nil
	}); err != nil {
		return err
	}
	if err := r.phase(r.squashIntoTarget); err != nil {
		return err
	}
	if err := r.phase(func() error {
		if !coordDone {
			if err := r.recordDone(); err != nil {
				return err
			}
		}
		if r.number > 0 {
			if r.state != nil {
				r.state.MissionNumberBaked = true
				r.state.Save(r.root)
			}
			say(r.w, fmt.Sprintf("[green]Assigned[/green] mission_number=%d to mission %s", r.number, r.slug))
		}
		return nil
	}); err != nil {
		return err
	}
	if err := r.phase(func() error { return r.doneBookkeeping(coordDone) }); err != nil {
		return err
	}
	return r.reconcile()
}

// phase runs one ref-moving phase under the post-mutation tip recorder
// (_records_post_mutation_tips).
func (r *run) phase(fn func() error) error {
	if r.state == nil {
		return fn()
	}
	entry, expected := r.state.movableTips(r.root), r.state.expectedTips()
	err := fn()
	r.state.recordPostTips(r.root, entry, expected)
	return err
}

// advance moves branch to sha, persisting the advance intent first.
func (r *run) advance(branch, sha string) error {
	if r.state != nil {
		r.state.noteIntent(r.root, branch, liveTip(r.root, branch), sha)
	}
	return advanceBranch(r.root, branch, sha)
}

// afterReconcile is the post-gate phases: stale check, push, teardown and
// the summary.
func (r *run) afterReconcile() error {
	coordDone := r.coordDone
	say(r.w, "  [dim]Running stale-assertion check...[/dim]")
	stale, staleErr := staleassert.RunCheck(r.baseline, "HEAD", r.root)
	if r.push && hasRemote(r.root, "origin") {
		res, _ := gitx.Exec(r.root, nil, nil, "push", "origin", r.m.TargetBranch)
		if res.Code != 0 {
			if isLinearHistoryRejection(res.Stderr) {
				say(r.w, "\n[yellow]Push rejected by linear-history protection.[/yellow]\nTry [cyan]spec-kitty consolidate --strategy squash[/cyan], or set [cyan]merge.strategy: squash[/cyan] in [cyan].kittify/config.yaml[/cyan].\n")
			}
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(res.Stdout)
			}
			say(r.w, "[red]Error:[/red] Push failed: "+msg)
			return exit(1)
		}
		say(r.w, fmt.Sprintf("[green]✓[/green] Pushed %s to origin", r.m.TargetBranch))
	}
	r.captureRetrospective()
	r.teardown(coordDone)
	ClearState(r.root, missionIDOf(r.root, r.slug))
	say(r.w, "\n[bold]Stale assertion findings:[/bold]")
	switch {
	case staleErr != nil || stale == nil:
		say(r.w, "  [yellow]Stale-assertion check could not run.[/yellow]")
	case len(stale.Findings) == 0:
		say(r.w, "  No likely-stale assertions detected.")
	default:
		line := func(grade string, f staleassert.Finding) {
			say(r.w, "  "+rich.EscapeMarkup(fmt.Sprintf("[%s] %s:%d — %s", grade, filepath.Base(f.TestFile), f.TestLine, f.Hint)))
		}
		var info []staleassert.Finding
		for _, f := range stale.Findings {
			switch f.Confidence {
			case "high", "medium":
				line(f.Confidence, f)
			case "info":
				info = append(info, f)
			}
		}
		if len(info) > 0 {
			say(r.w, fmt.Sprintf("  Message-content assertions skipped as info grade (%d) — review manually if diagnostic text changed:", len(info)))
			for _, f := range info {
				line("info", f)
			}
		}
		for _, f := range stale.Findings {
			if f.Confidence == "low" {
				line("low", f)
			}
		}
	}
	return nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func missionIDOf(root, slug string) string {
	return mission.ReadMetaOrEmpty(project.Specs(root, slug)).GetString("mission_id")
}

// reconcile is the merge/rebase reconciliation gate: every approved lane
// commit must be reachable from the landed target; otherwise every branch
// the run moved is rolled back and the merge refuses.
func (r *run) reconcile() error { return r.gate() }

// ---------------------------------------------------------------------------
// preflight

type dirtyEntry struct{ xy, path string }

func porcelainAll(dir string) []dirtyEntry {
	out, err := gitx.Exec(dir, nil, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil
	}
	var l []dirtyEntry
	recs := strings.Split(out.Stdout, "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		e := dirtyEntry{rec[:2], rec[3:]}
		if strings.ContainsAny(e.xy, "RC") {
			i++
		}
		l = append(l, e)
	}
	return l
}

func (r *run) disposable(p string) bool {
	if r.disposableFn != nil {
		return r.disposableFn(p)
	}
	return acceptance.IsSelfBookkeeping(p)
}

func (r *run) refuse(code, wt string, entries []string, remediation string) error {
	lines := []string{fmt.Sprintf("Refusing destructive operation (%s).", code), "  Worktree: " + wt}
	if len(entries) > 0 {
		var shown []string
		for i, e := range entries {
			if i == 20 {
				shown = append(shown, fmt.Sprintf("    ... and %d more", len(entries)-20))
				break
			}
			shown = append(shown, "    "+e)
		}
		lines = append(lines, "  Dirty entries:\n"+strings.Join(shown, "\n"))
	}
	lines = append(lines, "  Remediation: "+remediation)
	sayErr(r.w, strings.Join(lines, "\n"))
	say(r.w, "[yellow]Merge aborted before any state change.[/yellow] Resolve the reported condition, then re-run [bold]spec-kitty consolidate[/bold].")
	return exit(1)
}

const resumeNote = "then resume the operation (e.g. `spec-kitty consolidate --resume`)"

func (r *run) checkClean(wt, code string, untracked bool) error {
	var entries []string
	for _, e := range porcelainAll(wt) {
		if r.disposable(e.path) {
			continue
		}
		if e.xy == "??" {
			if untracked {
				entries = append(entries, "?? "+e.path+" (untracked local file would be discarded by worktree removal)")
			}
			continue
		}
		if e.xy == "!!" {
			continue
		}
		entries = append(entries, e.xy+" "+e.path)
	}
	if len(entries) == 0 {
		return nil
	}
	return r.refuse(code, wt, entries, fmt.Sprintf("Commit, stash, or revert the local changes in %s, %s.", wt, resumeNote))
}

func (r *run) preflight() error {
	cur := gitx.CurrentBranch(r.root)
	if cur != r.m.TargetBranch {
		lines := []string{"Refusing destructive operation (MERGE_UNSAFE_PRIMARY_OFF_TARGET).", "  Worktree: " + r.root,
			fmt.Sprintf("  Branch: currently on %s, expected %s.", mission.PyRepr(cur), mission.PyRepr(r.m.TargetBranch)),
			fmt.Sprintf("  Remediation: Checkout %s in %s before retrying (%s).", mission.PyRepr(r.m.TargetBranch), r.root, resumeNote)}
		sayErr(r.w, strings.Join(lines, "\n"))
		say(r.w, "[yellow]Merge aborted before any state change.[/yellow] Resolve the reported condition, then re-run [bold]spec-kitty consolidate[/bold].")
		return exit(1)
	}
	// Primary: tracked dirt (coordination residue and bookkeeping exempt).
	var entries []string
	for _, e := range porcelainAll(r.root) {
		if e.xy == "??" || e.xy == "!!" || r.disposable(e.path) {
			continue
		}
		if r.sf.Routed() && mission.IsCoordResidue(e.path) && strings.HasPrefix(e.path, "kitty-specs/"+r.slug+"/") {
			continue
		}
		entries = append(entries, e.xy+" "+e.path)
	}
	if len(entries) > 0 {
		return r.refuse("MERGE_UNSAFE_PRIMARY_DIRTY", r.root, entries, fmt.Sprintf("Commit, stash, or revert the local changes in %s, %s.", r.root, resumeNote))
	}
	if !r.ret.RemoveWorktree {
		return nil
	}
	for _, l := range r.m.Lanes {
		if l.LaneID == lanes.PlanningLaneID {
			continue
		}
		wt := lanes.WorktreePath(r.root, r.slug, l.LaneID)
		if dirExists(wt) {
			if err := r.checkClean(wt, "MERGE_UNSAFE_WORKTREE_DIRTY", true); err != nil {
				return err
			}
		}
	}
	if !r.ret.DeleteBranch {
		return nil
	}
	if r.sf.Routed() && dirExists(r.sf.CoordWorktree) {
		// Judged as a MISSION checkout (_as_mission_checkout): besides
		// spec-kitty's own bookkeeping, this Mission's coordination-partition
		// residue (its status log and snapshot, ...) under the stored coord
		// topology is disposable there.
		defer func(prev func(string) bool) { r.disposableFn = prev }(r.disposableFn)
		dirs := map[string]bool{r.slug: true, mission.CoordMissionDirName(r.slug, r.sf.Mid8): true}
		r.disposableFn = func(p string) bool {
			if acceptance.IsSelfBookkeeping(p) {
				return true
			}
			n := filepath.ToSlash(p)
			for d := range dirs {
				if strings.HasPrefix(n, "kitty-specs/"+d+"/") {
					return mission.IsCoordResidue(n)
				}
			}
			return false
		}
		return r.checkClean(r.sf.CoordWorktree, "MERGE_UNSAFE_WORKTREE_DIRTY", true)
	}
	return nil
}

// ---------------------------------------------------------------------------
// lane merges

func (r *run) mergeLanes() error {
	for _, l := range r.m.Lanes {
		if l.LaneID == lanes.PlanningLaneID {
			say(r.w, fmt.Sprintf("  [green]✓[/green] %s already on %s", l.LaneID, r.m.TargetBranch))
			continue
		}
		if laneFullyCanceled(l, r.excluded) {
			say(r.w, fmt.Sprintf("  [dim]Skipping %s (all WPs canceled with provenance — acceptable ending, no branch to integrate)[/dim]", l.LaneID))
			continue
		}
		branch := lanes.LaneBranchName(r.m.MissionSlug, l.LaneID, r.m.TargetBranch)
		if !gitx.BranchExists(r.root, branch) || gitx.IsAncestor(r.root, branch, r.m.MissionBranch) {
			say(r.w, fmt.Sprintf("  [dim]Skipping %s (already integrated into %s)[/dim]", l.LaneID, r.m.MissionBranch))
			continue
		}
		say(r.w, fmt.Sprintf("  [dim]Checking and merging %s...[/dim]", l.LaneID))
		if msg := r.checkStale(l, branch); msg != "" {
			say(r.w, fmt.Sprintf("  [red]✗[/red] %s: %s", l.LaneID, msg))
			return exit(1)
		}
		if err := r.mergeInto(branch, r.m.MissionBranch); err != nil {
			say(r.w, fmt.Sprintf("  [red]✗[/red] %s: %s", l.LaneID, err.Error()))
			return exit(1)
		}
		say(r.w, fmt.Sprintf("  [green]✓[/green] %s → %s", l.LaneID, r.m.MissionBranch))
	}
	return nil
}

// mergeInto is the MERGE strategy of _merge_branch_into.
func (r *run) mergeInto(source, target string) error {
	dir, cleanup, err := scratchWorktree(r.root, target, "kitty-merge-")
	if err != nil {
		return err
	}
	defer cleanup()
	res, _ := gitEnv(dir, mergeEnv(false), "merge", source, "--no-edit", "-m", fmt.Sprintf("Merge %s into %s", source, target))
	if res.Code != 0 {
		gitEnv(dir, nil, "merge", "--abort")
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		return fmt.Errorf("Merge of %s into %s failed: %s", source, target, msg)
	}
	return r.advance(target, revParse(dir, "HEAD"))
}

// ---------------------------------------------------------------------------
// mission number bake

func (r *run) bake() error {
	if !needsNumber(r.sf.PrimaryDir) {
		return nil
	}
	r.number = NextMissionNumber(r.root)
	if r.planningOnly {
		// written now (the done transitions materialize it), committed by
		// the done bookkeeping commit on the target
		if meta, err := mission.ReadMeta(r.sf.PrimaryDir); err == nil {
			meta.Set("mission_number", r.number)
			mission.WriteMeta(r.sf.PrimaryDir, meta, false)
		}
		return nil
	}
	dir, cleanup, err := scratchWorktree(r.root, r.m.MissionBranch, "kitty-numwrite-")
	if err != nil {
		return nil
	}
	defer cleanup()
	mdir := filepath.Join(dir, "kitty-specs", r.slug)
	meta, err := mission.ReadMeta(mdir)
	if err != nil {
		return r.bakeOnPrimary()
	}
	if n, ok := mission.MissionNumber(meta); ok && n == r.number {
		return nil
	}
	meta.Set("mission_number", r.number)
	if err := mission.WriteMeta(mdir, meta, false); err != nil {
		return err
	}
	rel := filepath.ToSlash(filepath.Join("kitty-specs", r.slug, mission.MetaFile))
	if _, err := gitx.Run(dir, "-c", "commit.gpgsign=false", "commit", "-q", "--only", "-m",
		fmt.Sprintf("chore(%s): assign mission_number=%d", r.slug, r.number), "--", rel); err != nil {
		return err
	}
	return r.advance(r.m.MissionBranch, revParse(dir, "HEAD"))
}

func (r *run) bakeOnPrimary() error {
	meta, err := mission.ReadMeta(r.sf.PrimaryDir)
	if err != nil {
		return nil
	}
	meta.Set("mission_number", r.number)
	if err := mission.WriteMeta(r.sf.PrimaryDir, meta, false); err != nil {
		return err
	}
	_, err = coord.CommitPaths(r.root, fmt.Sprintf("chore(%s): assign mission_number=%d", r.slug, r.number), filepath.Join(r.sf.PrimaryDir, mission.MetaFile))
	r.baseline = revParse(r.root, "refs/heads/"+r.m.TargetBranch)
	return err
}

// ---------------------------------------------------------------------------
// done transitions

func (r *run) recordDone() error {
	say(r.w, "  [dim]Recording merged work packages as done...[/dim]")
	sf := coord.Resolve(r.root, r.slug)
	snap, err := status.ReduceDir(sf.StatusDir())
	if err != nil {
		return err
	}
	completed := map[string]bool{}
	if r.state != nil {
		// _reconcile_completed_wps_for_resume: the record's completed WPs are a
		// hint, confirmed against the durable done lane.
		var confirmed []string
		for _, id := range r.state.CompletedWPs {
			if st := snap.WP(id); st != nil && st.GetString("lane") == "done" {
				confirmed = append(confirmed, id)
				completed[id] = true
			}
		}
		if len(confirmed) != len(r.state.CompletedWPs) {
			r.state.CompletedWPs = confirmed
			r.state.Save(r.root)
		}
	}
	var order []string
	for _, l := range r.m.Lanes {
		order = append(order, l.WPIDs...)
	}
	for _, id := range order {
		if r.excluded[id] {
			say(r.w, fmt.Sprintf("  [dim]Skipping %s (canceled with provenance — acceptable ending, excluded from done)[/dim]", id))
			continue
		}
		if completed[id] {
			say(r.w, fmt.Sprintf("  [dim]Skipping %s (already recorded as done)[/dim]", id))
			continue
		}
		st := snap.WP(id)
		if st == nil || st.GetString("lane") == "done" {
			r.markComplete(id)
			continue
		}
		reviewer := ""
		if rr := st.GetObject("review_result"); rr != nil {
			reviewer = rr.GetString("reviewer")
		}
		if reviewer == "" {
			reviewer = st.GetString("actor")
		}
		reason := fmt.Sprintf("Merged %s into %s", id, r.m.TargetBranch)
		req := &status.TransitionRequest{
			WPID: id, ToLane: "done", Actor: status.StrActor("merge"), Reason: &reason,
			Evidence:       &status.DoneEvidence{Review: status.ReviewApproval{Reviewer: reviewer, Verdict: "approved", Reference: "lane-approved:" + id}},
			ExecutionMode:  "worktree",
			PolicyMetadata: jsonx.NewObject("merge_phase", "lane_integrated", "target_branch", r.m.TargetBranch),
		}
		if _, err := sf.EmitTransition(req); err != nil {
			sayErr(r.w, err.Error())
			return exit(1)
		}
		r.markComplete(id)
	}
	r.clearTerminalLaneTips(sf)
	return nil
}

// markComplete is ConsolidationState.mark_wp_complete + save.
func (r *run) markComplete(id string) {
	if r.state == nil {
		return
	}
	if !contains(r.state.CompletedWPs, id) {
		r.state.CompletedWPs = append(r.state.CompletedWPs, id)
	}
	r.state.Save(r.root)
}

// clearTerminalLaneTips is the on_wp_terminal hook of the done transitions:
// a lane's tip ref is cleared once every WP of the lane is terminal.
func (r *run) clearTerminalLaneTips(sf *coord.Surface) {
	snap, err := status.ReduceDir(sf.StatusDir())
	if err != nil {
		return
	}
	for _, l := range r.m.Lanes {
		if l.LaneID == lanes.PlanningLaneID {
			continue
		}
		all := true
		for _, id := range l.WPIDs {
			lane := ""
			if st := snap.WP(id); st != nil {
				lane = st.GetString("lane")
			}
			if lane != "done" && lane != "canceled" {
				all = false
			}
		}
		if all {
			gitx.Run(r.root, "update-ref", "-d", lanes.LaneTipRefPrefix+lanes.LaneBranchName(r.m.MissionSlug, l.LaneID, r.m.TargetBranch))
		}
	}
}

// ---------------------------------------------------------------------------
// mission → target squash

func (r *run) squashIntoTarget() error {
	if r.planningOnly {
		say(r.w, fmt.Sprintf("  [dim]Skipping mission branch merge; %s is the planning artifact branch.[/dim]", r.m.TargetBranch))
		r.squash = revParse(r.root, "refs/heads/"+r.m.TargetBranch)
		return nil
	}
	say(r.w, fmt.Sprintf("  [dim]Merging mission branch into %s...[/dim]", r.m.TargetBranch))
	dir, cleanup, err := scratchWorktree(r.root, r.m.TargetBranch, "kitty-merge-")
	if err != nil {
		sayErr(r.w, err.Error())
		return exit(1)
	}
	defer cleanup()
	switch r.strategy {
	case "merge":
		return r.mergeTarget(dir)
	case "rebase":
		return r.rebaseTarget(dir)
	}
	res, _ := gitEnv(dir, mergeEnv(true), "merge", "--squash", r.m.MissionBranch)
	if res.Code != 0 {
		conflicts := unmergedPaths(dir)
		if len(conflicts) == 0 {
			sayErr(r.w, strings.TrimSpace(res.Stderr+res.Stdout))
			return exit(1)
		}
		var remaining []string
		for _, c := range conflicts {
			if strings.HasPrefix(c, "kitty-specs/"+r.slug+"/") {
				if filepath.Base(c) == "status.json" {
					if snap, err := status.MaterializeSnapshot(filepath.Join(dir, filepath.Dir(c))); err == nil {
						os.WriteFile(filepath.Join(dir, c), []byte(snap.Render()), 0o644)
					}
				} else {
					gitx.Run(dir, "checkout", "--ours", "--", c)
				}
				gitx.Run(dir, "add", "--", c)
				continue
			}
			remaining = append(remaining, c)
		}
		if len(remaining) > 0 {
			gitx.Run(dir, "reset", "-q", "--hard")
			say(r.w, "[red]Error:[/red] Mission branch content conflicts with the target branch.")
			say(r.w, "  diagnostic_code: TARGET_BRANCH_CONTENT_CONFLICT")
			say(r.w, "  mission_branch: "+r.m.MissionBranch)
			say(r.w, "  target_branch: "+r.m.TargetBranch)
			for _, c := range remaining {
				say(r.w, "  conflicting_path: "+c)
			}
			return exit(1)
		}
	}
	if !gitx.HasStagedChanges(dir) {
		sayErr(r.w, fmt.Sprintf("Squash merge of %s into %s produced no changes; target may already contain this tree. Retry with merge resume if recovering an interrupted merge.", r.m.MissionBranch, r.m.TargetBranch))
		return exit(1)
	}
	if _, err := gitx.Run(dir, "commit", "-q", "-m", fmt.Sprintf("feat(%s): squash merge of mission", r.m.MissionBranch)); err != nil {
		sayErr(r.w, "Squash commit into "+r.m.TargetBranch+" failed: "+err.Error())
		return exit(1)
	}
	r.squash = revParse(dir, "HEAD")
	if err := r.advance(r.m.TargetBranch, r.squash); err != nil {
		sayErr(r.w, err.Error())
		return exit(1)
	}
	say(r.w, fmt.Sprintf("\n[green]✓[/green] %s → %s", r.m.MissionBranch, r.m.TargetBranch))
	say(r.w, "  Commit: "+r.squash[:7])
	return nil
}

// landed advances the target to sha and prints the success lines.
func (r *run) landed(sha string) error {
	r.squash = sha
	if err := r.advance(r.m.TargetBranch, sha); err != nil {
		sayErr(r.w, err.Error())
		return exit(1)
	}
	say(r.w, fmt.Sprintf("\n[green]✓[/green] %s → %s", r.m.MissionBranch, r.m.TargetBranch))
	say(r.w, "  Commit: "+sha[:7])
	return nil
}

// mergeTarget is the MERGE strategy of the mission→target step (no-ff
// merge commit, meta.json driver in two-way mode).
func (r *run) mergeTarget(dir string) error {
	pre := revParse(dir, "HEAD")
	res, _ := gitEnv(dir, mergeEnv(true), "merge", r.m.MissionBranch, "--no-edit", "-m", fmt.Sprintf("Merge %s into %s", r.m.MissionBranch, r.m.TargetBranch))
	if res.Code != 0 {
		gitEnv(dir, nil, "merge", "--abort")
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		say(r.w, "[red]Error:[/red] "+fmt.Sprintf("Merge of %s into %s failed: %s", r.m.MissionBranch, r.m.TargetBranch, msg))
		return exit(1)
	}
	post := revParse(dir, "HEAD")
	if post == pre {
		say(r.w, "[red]Error:[/red] "+fmt.Sprintf("Merge of %s into %s produced no changes; target may already contain this tree. Retry with merge resume if recovering an interrupted merge.", r.m.MissionBranch, r.m.TargetBranch))
		return exit(1)
	}
	return r.landed(post)
}

// rebaseTarget is the REBASE strategy: rebase the mission branch onto the
// target in the scratch worktree and fast-forward the target to it.
func (r *run) rebaseTarget(dir string) error {
	fail := func(prefix string, res gitx.Result) error {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		say(r.w, "[red]Error:[/red] "+prefix+msg)
		return exit(1)
	}
	if res, _ := gitEnv(dir, mergeEnv(false), "checkout", "--detach", r.m.MissionBranch); res.Code != 0 {
		return fail(fmt.Sprintf("Failed to check out %s in merge worktree: ", r.m.MissionBranch), res)
	}
	if res, _ := gitEnv(dir, mergeEnv(false), "rebase", r.m.TargetBranch); res.Code != 0 {
		gitEnv(dir, nil, "rebase", "--abort")
		return fail(fmt.Sprintf("Rebase of %s onto %s failed: ", r.m.MissionBranch, r.m.TargetBranch), res)
	}
	return r.landed(revParse(dir, "HEAD"))
}

// ---------------------------------------------------------------------------
// target bookkeeping

func (r *run) doneBookkeeping(coordDone bool) error {
	primary := r.sf.PrimaryDir
	paths := []string{filepath.Join(primary, mission.MetaFile)}
	if coordDone {
		src := filepath.Join(r.sf.CoordDir, status.EventsFile)
		dst := status.EventsPath(primary)
		sb, _ := os.ReadFile(src)
		db, _ := os.ReadFile(dst)
		if merged, err := mergedriver.MergeEventLogTexts(string(sb), string(db)); err == nil && (len(sb) > 0 || len(db) > 0) {
			os.WriteFile(dst, []byte(merged), 0o644)
			if stream, err := status.ReadStream(primary, ""); err == nil {
				snap := status.Reduce(stream.Transitions, nil, true)
				os.WriteFile(filepath.Join(primary, "status.json"), []byte(snap.Render()), 0o644)
			}
		}
		paths = append(paths, dst, filepath.Join(primary, "status.json"))
		if r.checkpoint != nil {
			// S-B/FR-004: carry concurrent post-checkpoint coordination commits
			// forward (written, not staged: upstream commits only the status pair).
			projectPostCheckpoint(r.root, r.slug, r.checkpoint.ref, r.checkpoint.sha, primary)
		}
	}
	meta, err := mission.ReadMeta(primary)
	if err != nil {
		return err
	}
	if n, ok := mission.MissionNumber(meta); !ok && r.number > 0 {
		meta.Set("mission_number", r.number)
	} else if ok {
		_ = n
	}
	meta.Set("baseline_merge_commit", r.baseline)
	meta.Set("merged_at", clock.NowUTCISO())
	meta.Set("merged_commit", r.squash)
	if strings.TrimSpace(meta.GetString("mission_id")) != "" {
		meta.Set("status_phase", "1")
	}
	if err := mission.WriteMeta(primary, meta, false); err != nil {
		return err
	}
	if _, err := coord.CommitPaths(r.root, fmt.Sprintf("chore(%s): record done transitions for merged WPs", r.slug), existing(paths)...); err != nil {
		sayErr(r.w, err.Error())
		return exit(1)
	}
	return nil
}

func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// retrospective + teardown

func (r *run) captureRetrospective() {
	path, err := retrospective.Capture(r.root, r.slug)
	if err != nil || path == "" {
		return
	}
	coord.CommitPaths(r.root, fmt.Sprintf("chore(%s): capture mission retrospective", r.slug),
		path, status.EventsPath(r.sf.PrimaryDir))
}

func (r *run) teardown(coordDone bool) {
	if r.ret.RemoveWorktree {
		for _, l := range r.m.Lanes {
			if l.LaneID == lanes.PlanningLaneID {
				continue
			}
			wt := lanes.WorktreePath(r.root, r.slug, l.LaneID)
			if !dirExists(wt) {
				continue
			}
			if _, err := gitx.Run(r.root, "worktree", "remove", "--force", wt); err == nil {
				say(r.w, "  Removed worktree: "+filepath.Base(wt))
			}
		}
	}
	for _, c := range lanes.ListContexts(r.root) {
		_ = c
	}
	if matches, _ := filepath.Glob(filepath.Join(lanes.WorkspacesDir(r.root), r.slug+"-*.json")); r.ret.RemoveWorktree {
		for _, p := range matches {
			os.Remove(p)
		}
	}
	if !r.ret.DeleteBranch {
		return
	}
	n := 0
	for _, l := range r.m.Lanes {
		if l.LaneID == lanes.PlanningLaneID {
			continue
		}
		b := lanes.LaneBranchName(r.m.MissionSlug, l.LaneID, r.m.TargetBranch)
		if worktreeFor(r.root, b) != "" {
			continue
		}
		if _, err := gitx.Run(r.root, "branch", "-D", b); err == nil {
			n++
		}
		gitx.Run(r.root, "update-ref", "-d", "refs/spec-kitty/lane-tip/"+b)
	}
	if n > 0 {
		say(r.w, fmt.Sprintf("  Cleaned up %d lane branch(es)", n))
	}
	refs, _ := gitx.Run(r.root, "for-each-ref", "--format=%(refname)", "refs/spec-kitty/wp-base/"+r.slug+"/")
	for _, ref := range strings.Fields(refs) {
		gitx.Run(r.root, "update-ref", "-d", ref)
	}
	if r.sf.Routed() && r.ret.RemoveWorktree {
		if dirExists(r.sf.CoordWorktree) {
			gitx.Run(r.root, "worktree", "remove", "--force", r.sf.CoordWorktree)
		}
		gitx.Run(r.root, "branch", "-D", r.sf.CoordBranch)
		say(r.w, "  Cleaned up mission/coordination branch + worktree")
		r.flatten()
	} else if r.m.MissionBranch != r.m.TargetBranch && gitx.BranchExists(r.root, r.m.MissionBranch) && worktreeFor(r.root, r.m.MissionBranch) == "" {
		gitx.Run(r.root, "branch", "-D", r.m.MissionBranch)
	}
}

func (r *run) flatten() {
	meta, err := mission.ReadMeta(r.sf.PrimaryDir)
	if err != nil {
		return
	}
	meta.Delete("coordination_branch")
	meta.Delete("topology")
	meta.Set("flattened", true)
	mission.WriteMeta(r.sf.PrimaryDir, meta, false)
	coord.CommitPaths(r.root, fmt.Sprintf("chore(%s): flatten coordination metadata after branch deletion (#3086)", r.slug),
		filepath.Join(r.sf.PrimaryDir, mission.MetaFile))
}

var (
	_ = sort.Strings
)
