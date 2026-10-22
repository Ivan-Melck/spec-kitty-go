package runtime

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/refadvance"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// destructiveRefused is git.destructive_guard.DestructiveOpRefused.
type destructiveRefused struct {
	code, worktree, current, remediation string
	hasBranch                            bool
	dirty                                []string
}

func (e *destructiveRefused) Error() string {
	lines := []string{fmt.Sprintf("Refusing destructive operation (%s).", e.code)}
	if e.worktree != "" {
		lines = append(lines, "  Worktree: "+e.worktree)
	}
	if e.hasBranch {
		lines = append(lines, fmt.Sprintf("  Branch: currently on %s, expected None.", pyjson.ReprString(e.current)))
	}
	if len(e.dirty) > 0 {
		var shown []string
		for i, d := range e.dirty {
			if i >= 20 {
				shown = append(shown, fmt.Sprintf("    ... and %d more", len(e.dirty)-20))
				break
			}
			shown = append(shown, "    "+d)
		}
		lines = append(lines, "  Dirty entries:\n"+strings.Join(shown, "\n"))
	}
	lines = append(lines, "  Remediation: "+e.remediation)
	return strings.Join(lines, "\n")
}

func gitRun(root string, args ...string) gitx.Result {
	r, _ := gitx.Exec(root, nil, nil, args...)
	return r
}

func existingRef(root, branch string) bool {
	return branch != "" && gitRun(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Code == 0
}

// uniqueCommits is destructive_guard._unique_commits.
func uniqueCommits(root, branch string, extraNot, ignoring []string) []string {
	skip := map[string]bool{"refs/heads/" + branch: true}
	for _, n := range ignoring {
		skip["refs/heads/"+n] = true
	}
	refs := gitRun(root, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/remotes", "refs/tags")
	var neg strings.Builder
	for _, l := range strings.Split(refs.Stdout, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && !skip[f[0]] {
			neg.WriteString("^" + f[1] + "\n")
		}
	}
	for _, x := range extraNot {
		neg.WriteString("^" + x + "\n")
	}
	r, err := gitx.Exec(root, nil, []byte(neg.String()), "rev-list", "refs/heads/"+branch, "--stdin")
	if err != nil || r.Code != 0 {
		return nil
	}
	return strings.Fields(r.Stdout)
}

func branchTipSHA(root, branch string) string {
	r := gitRun(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if r.Code != 0 {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

// guardedBranchDelete is guarded_branch_delete without operator intent:
// refused while the branch holds commits no other ref reaches.
func guardedBranchDelete(root, branch, creationBase string) (bool, error) {
	if strings.HasPrefix(branch, "-") || !existingRef(root, branch) {
		return false, nil
	}
	unique := uniqueCommits(root, branch, nil, nil)
	if len(unique) > 0 && creationBase != "" {
		unique = uniqueCommits(root, branch, []string{creationBase}, nil)
	}
	if len(unique) > 0 {
		var d []string
		for _, s := range unique {
			d = append(d, fmt.Sprintf("%s (only on %s)", s[:12], branch))
		}
		return false, &destructiveRefused{code: "BRANCH_HAS_UNIQUE_COMMITS", current: branch, hasBranch: true, dirty: d,
			remediation: fmt.Sprintf("Merge or keep branch %s; nothing was deleted.", pyjson.ReprString(branch))}
	}
	if gitRun(root, "branch", "-D", "--", branch).Code != 0 {
		return false, nil
	}
	return true, nil
}

// discardedRef is destructive_guard.discarded_ref.
func discardedRef(root, branch, tip string) string {
	ref := "refs/spec-kitty/discarded/" + branch
	r := gitRun(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if r.Code != 0 || strings.TrimSpace(r.Stdout) == tip {
		return ref
	}
	return ref + "@" + tip[:12]
}

// discardOwnBranch is _discard_own_mission_branch (DISCARD_INTENT: the tip
// of a branch holding unique commits is kept under refs/spec-kitty/discarded/).
func discardOwnBranch(ctx *cli.Context, root, branch string) (bool, error) {
	if strings.HasPrefix(branch, "-") || !existingRef(root, branch) {
		return false, nil
	}
	kept := ""
	if len(uniqueCommits(root, branch, nil, nil)) > 0 {
		tip := branchTipSHA(root, branch)
		ref := ""
		if tip != "" {
			ref = discardedRef(root, branch, tip)
		}
		if tip == "" || (gitRun(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Code != 0 && gitRun(root, "update-ref", ref, tip, "").Code != 0) {
			return false, &destructiveRefused{code: "DESTRUCTIVE_OP_ONLY_COPY", current: branch, hasBranch: true,
				remediation: fmt.Sprintf("Could not record the tip of %s under refs/spec-kitty/discarded/; nothing was deleted.", pyjson.ReprString(branch))}
		}
		kept = tip
	}
	if gitRun(root, "branch", "-D", "--", branch).Code != 0 {
		return false, nil
	}
	if kept != "" {
		rich.Print(ctx.Stdout, fmt.Sprintf("  Deleted %s (was %s); restore with: git branch %s %s", branch, kept[:12], branch, discardedRef(root, branch, kept)))
	}
	return true, nil
}

var (
	ownedMetaRe   = regexp.MustCompile(`(?:^|/)kitty-specs/[^/]+/meta\.json$`)
	legacyMetaRe  = regexp.MustCompile(`(?:^|/)\.kittify/meta\.json$`)
	auditRe       = regexp.MustCompile(`(?:^|/)\.kittify/mission-state-audit(?:/|$)`)
	derivedViewRe = regexp.MustCompile(`(?:^|/)\.kittify/derived/[^/]+/`)
	opRecordRe    = regexp.MustCompile(`(?:^|/)kitty-ops/[0-9A-HJKMNP-TV-Z]{26}\.jsonl$`)
	dossierSnapRe = regexp.MustCompile(`(?:^|/)\.kittify/dossiers/[^/]+/snapshot-latest\.json$`)
)

// selfBookkeeping is coherence.is_self_bookkeeping_churn.
func selfBookkeeping(p string) bool {
	n := strings.TrimRight(filepath.ToSlash(p), "/")
	return ownedMetaRe.MatchString(n) || legacyMetaRe.MatchString(n) || strings.HasSuffix(n, ".kittify/encoding-provenance/global.jsonl") ||
		auditRe.MatchString(n) || derivedViewRe.MatchString(n) || dossierSnapRe.MatchString(n) || opRecordRe.MatchString(n)
}

// worktreeLocalState lists the entries a `worktree remove --force` would
// destroy (tracked changes and untracked files), net of spec-kitty's own
// bookkeeping and (for a lane checkout) coordination residue.
func worktreeLocalState(wt string, lane bool) []string {
	// guarded_worktree_remove -> _dirty_entries(treat_untracked_as_dirty=True)
	// with no reset target: untracked files are listed one by one, ignored
	// entries never count, a VCS-lock-only meta.json is exempt.
	vs, err := refadvance.DirtyVerdicts(wt, refadvance.Options{UntrackedDirty: true, IsDisposable: func(p string) bool {
		return selfBookkeeping(p) || (lane && mission.IsCoordResidue(strings.TrimRight(p, "/")))
	}})
	if err != nil {
		return nil
	}
	return refadvance.Lines(vs)
}

// guardedWorktreeRemove is guarded_worktree_remove(retain=False).
func guardedWorktreeRemove(root, wt string, lane bool) (bool, error) {
	if d := worktreeLocalState(wt, lane); len(d) > 0 {
		return false, &destructiveRefused{code: "MERGE_UNSAFE_WORKTREE_DIRTY", worktree: wt, dirty: d,
			remediation: fmt.Sprintf("Commit, stash, or revert the local changes in %s, then resume the operation (e.g. `spec-kitty consolidate --resume`).", wt)}
	}
	return gitRun(root, "worktree", "remove", "--force", wt).Code == 0, nil
}

// laneBranches are the code-lane branch names of the manifest.
func laneBranches(slug string, m *lanes.Manifest) []string {
	var out []string
	for _, l := range m.Lanes {
		if l.LaneID != lanes.PlanningLaneID {
			out = append(out, lanes.LaneBranchName(slug, l.LaneID, m.TargetBranch))
		}
	}
	return out
}

// deletableMissionBranch is _deletable_mission_branch.
func deletableMissionBranch(m *lanes.Manifest) string {
	if m == nil || m.MissionBranch == "" || m.MissionBranch == m.TargetBranch {
		return ""
	}
	return m.MissionBranch
}

// mintedMissionBranch is minted_mission_branch: the stored single_branch
// topology's own mission_branch distinct from the target.
func mintedMissionBranch(meta *jsonx.Object, target string) string {
	mb := meta.GetString("mission_branch")
	if mb == "" || mb == target || meta.GetString("topology") != "single_branch" {
		return ""
	}
	return mb
}

func readYesNo(ctx *cli.Context, prompt string) bool {
	fmt.Fprint(ctx.Stdout, prompt+" [y/N]: ")
	if ctx.Stdin == nil {
		fmt.Fprintln(ctx.Stdout)
		return false
	}
	line, _ := bufio.NewReader(ctx.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// discardMission is mission close --discard (_discard_mission +
// verification + flatten + discard marker + bookkeeping commit).
func discardMission(ctx *cli.Context, root, slug, dir string, s *coord.Surface, jsonMode bool) error {
	out := func(msg string) { rich.Print(ctx.Stdout, msg) }
	metaPath := filepath.Join(dir, mission.MetaFile)
	if st, err := os.Stat(metaPath); err != nil || st.IsDir() {
		cur := gitx.CurrentBranch(root)
		if cur == "" {
			cur = "HEAD"
		}
		out(fmt.Sprintf("Error: cannot discard %s: %s is not present on the current checkout (branch %s). A protected single_branch mission lives on its own minted mission branch (`git branch --list 'kitty/*'`); check that branch out and retry.", slug, metaPath, pyjson.ReprString(cur)))
		return cli.Exit(1)
	}
	m, err := lanes.Read(dir)
	if err != nil {
		out(fmt.Sprintf("Error: cannot discard %s: lanes.json is corrupt (%v). Repair or remove it, then retry.", slug, err))
		return cli.Exit(1)
	}
	meta := mission.ReadMetaOrEmpty(dir)
	target := meta.GetString("target_branch")
	if m != nil {
		target = m.TargetBranch
	}
	minted := ""
	if target != "" {
		minted = mintedMissionBranch(meta, target)
	}
	// announce what the unguarded own-branch deletion loses
	var own []string
	if minted != "" {
		own = []string{minted}
	} else if d := deletableMissionBranch(m); d != "" {
		own = []string{d}
	}
	doomed := append([]string{}, own...)
	if m != nil {
		doomed = append(doomed, laneBranches(slug, m)...)
	}
	for _, b := range own {
		tip := branchTipSHA(root, b)
		if tip == "" {
			continue
		}
		lost := len(uniqueCommits(root, b, nil, doomed))
		note := ": every commit also exists on another branch (nothing is lost)"
		if lost > 0 {
			note = fmt.Sprintf(": %d commit(s) that exist on no other branch will be deleted", lost)
		}
		out(fmt.Sprintf("  Branch %s (tip %s)%s", b, tip[:12], note))
	}
	if !ctx.Bool("force") {
		scope, history := "the coordination branch, every lane branch, and all worktrees", "the coordination history kept there (review cycles, traces, issue matrix) unless it is already on the primary branch"
		if minted != "" {
			scope, history = "the mission branch and all worktrees", "the mission's files kept on that branch"
		}
		if !readYesNo(ctx, fmt.Sprintf("Discard mission %s? This deletes %s. Unmerged work on those branches WILL BE LOST, and so will %s.", slug, scope, history)) {
			out("Aborted.")
			return cli.Exit(1)
		}
	}
	refusal := func(err error) error {
		out(fmt.Sprintf("Error: cannot discard %s: %v", slug, err))
		return cli.Exit(1)
	}
	removeLaneWorktrees := func() error {
		if m == nil {
			return nil
		}
		removed := 0
		var names []string
		for _, l := range m.Lanes {
			if l.LaneID != lanes.PlanningLaneID {
				names = append(names, lanes.WorktreePath(root, slug, l.LaneID))
			}
		}
		sort.Strings(names)
		for _, wt := range names {
			if st, err := os.Stat(wt); err != nil || !st.IsDir() {
				continue
			}
			ok, err := guardedWorktreeRemove(root, wt, true)
			if err != nil {
				return err
			}
			if ok {
				removed++
			}
		}
		if removed > 0 {
			out(fmt.Sprintf("  Removed %d lane worktree(s)", removed))
		}
		return nil
	}
	deleteLaneBranches := func() error {
		base := ""
		if existingRef(root, m.MissionBranch) {
			base = m.MissionBranch
		} else if existingRef(root, m.TargetBranch) {
			base = m.TargetBranch
		}
		n := 0
		for _, b := range laneBranches(slug, m) {
			ok, err := guardedBranchDelete(root, b, base)
			if err != nil {
				return err
			}
			if ok {
				n++
			}
		}
		deletedMission := false
		if d := deletableMissionBranch(m); d != "" {
			ok, err := discardOwnBranch(ctx, root, d)
			if err != nil {
				return err
			}
			deletedMission = ok
		}
		var parts []string
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d lane branch(es)", n))
		}
		if deletedMission {
			parts = append(parts, "mission/coordination branch")
		}
		if len(parts) > 0 {
			out("  Deleted " + strings.Join(parts, " + "))
		}
		return nil
	}
	if minted != "" {
		if cur := gitx.CurrentBranch(root); cur != target && minted != target {
			if r := gitRun(root, "checkout", target); r.Code != 0 {
				out(fmt.Sprintf("Error: cannot discard: the write checkout is on %s and could not be switched to %s so the branch can be deleted: %s. Commit or stash the changes you want to keep, or check out %s yourself, then retry.",
					pyjson.ReprString(minted), pyjson.ReprString(target), strings.TrimSpace(r.Stderr), pyjson.ReprString(target)))
				return cli.Exit(1)
			}
		}
		if m != nil {
			if err := removeLaneWorktrees(); err != nil {
				return refusal(err)
			}
			if err := deleteLaneBranches(); err != nil {
				return refusal(err)
			}
		}
		ok, err := discardOwnBranch(ctx, root, minted)
		if err != nil {
			return refusal(err)
		}
		if ok {
			out("  Deleted mission branch " + minted)
		}
	} else {
		if mid8 := recordedMid8(dir); mid8 != "" {
			wt := lanes.CoordWorktreePath(root, slug, mid8)
			if st, err := os.Stat(wt); err == nil && st.IsDir() {
				if d := worktreeLocalState(wt, false); len(d) > 0 {
					return refusal(&destructiveRefused{code: "MERGE_UNSAFE_WORKTREE_DIRTY", worktree: wt, dirty: d,
						remediation: fmt.Sprintf("Commit, stash, or revert the local changes in %s, then resume the operation (e.g. `spec-kitty consolidate --resume`).", wt)})
				}
			}
		}
		teardownCoordination(ctx, root, slug, dir, "runtime_abandoned", false)
		if m != nil {
			if err := removeLaneWorktrees(); err != nil {
				return refusal(err)
			}
			if err := deleteLaneBranches(); err != nil {
				return refusal(err)
			}
		} else if cb := meta.GetString("coordination_branch"); cb != "" {
			if _, err := guardedBranchDelete(root, cb, target); err != nil {
				return refusal(err)
			}
			out("  Deleted coordination branch " + cb)
		}
	}
	// _verify_discard_complete
	var leaks []string
	registered := map[string]bool{}
	if wts, err := gitx.Worktrees(root); err == nil {
		for _, w := range wts {
			registered[filepath.Base(w.Path)] = true
		}
	}
	if mid8 := recordedMid8(dir); mid8 != "" {
		wt := lanes.CoordWorktreePath(root, slug, mid8)
		if _, err := os.Stat(wt); err == nil || registered[filepath.Base(wt)] {
			leaks = append(leaks, ".worktrees/"+filepath.Base(wt))
		}
	}
	var expected []string
	if m != nil {
		var names []string
		for _, l := range m.Lanes {
			if l.LaneID != lanes.PlanningLaneID {
				names = append(names, filepath.Base(lanes.WorktreePath(root, slug, l.LaneID)))
			}
		}
		sort.Strings(names)
		for _, n := range names {
			if st, err := os.Stat(filepath.Join(root, ".worktrees", n)); (err == nil && st.IsDir()) || registered[n] {
				leaks = append(leaks, ".worktrees/"+n)
			}
		}
		expected = laneBranches(slug, m)
		if d := deletableMissionBranch(m); d != "" {
			expected = append(expected, d)
		}
	} else if cb := meta.GetString("coordination_branch"); cb != "" {
		expected = []string{cb}
	}
	if minted != "" {
		expected = append(expected, minted)
	}
	seen := map[string]bool{}
	for _, b := range expected {
		if !seen[b] && existingRef(root, b) {
			leaks = append(leaks, "branch "+b)
		}
		seen[b] = true
	}
	if len(leaks) > 0 {
		out(fmt.Sprintf("Error: discard of %s did not complete — these artifacts were not removed:", slug))
		for _, l := range leaks {
			out("  - " + l)
		}
		out("Remove them manually (git worktree remove --force / git branch -D) and retry, or report this as a bug.")
		return cli.Exit(1)
	}
	// flatten + discard marker + bookkeeping commit
	meta = mission.ReadMetaOrEmpty(dir)
	if meta.Has("coordination_branch") {
		meta.Delete("coordination_branch")
		meta.Delete("topology")
		meta.Set("flattened", true)
	}
	meta.Set("discarded_at", clock.NowUTCISO())
	if err := mission.WriteMeta(dir, meta, false); err == nil {
		rel, _ := filepath.Rel(root, metaPath)
		if st := gitRun(root, "status", "--porcelain", "--", filepath.ToSlash(rel)); strings.TrimSpace(st.Stdout) != "" {
			if _, err := coord.CommitPaths(root, fmt.Sprintf("chore(%s): flatten discarded mission metadata", slug), metaPath); err != nil {
				out(fmt.Sprintf("Warning: discard of %s flattened meta.json but could not commit it (%v). meta.json is left uncommitted. From the project root, re-run `spec-kitty mission close --mission %s --discard --force` to commit it.", slug, err, slug))
			}
		}
	}
	if jsonMode {
		emitJSON(ctx, jsonx.NewObject("ok", true, "result", "discarded", "mission_slug", slug))
	} else {
		out(fmt.Sprintf("✓ Mission %s discarded.", slug))
	}
	return nil
}
