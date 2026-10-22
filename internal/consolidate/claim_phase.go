package consolidate

// The claim and gate phases of the consolidate run (phase_claim /
// phase_gate): capture the reconciliation claim before the first mutation,
// and verify the landed target against it before any teardown.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	consolidateAbortCommand  = "spec-kitty consolidate --abort"
	nothingTornDown          = "Nothing was torn down"
	refusedWithEarlierRecord = "Consolidation refused; this run moved no branch, but an earlier attempt may have. " +
		"Run `spec-kitty consolidate --abort` to restore the branches it recorded."
)

// coordRef is the status-surface branch (the coordination branch under a
// coordination topology, the target otherwise).
func (r *run) coordRef() string {
	if r.sf.Routed() {
		return r.m.MissionBranch
	}
	return r.m.TargetBranch
}

// refuseUnexplainedMoves is _refuse_unexplained_branch_moves: the read-only
// pre-check of a re-run or resume against moves the record cannot explain.
func (r *run) refuseUnexplainedMoves() error {
	st := LoadState(r.root, missionIDOf(r.root, r.slug))
	if st == nil || len(st.PreRefs) == 0 {
		return nil
	}
	rows := st.unexplainedBranches(r.root)
	if len(rows) > 0 {
		if live := liveTip(r.root, st.TargetBranch); live != "" && st.ReconPassed == live {
			var kept [][3]string
			for _, row := range rows {
				if row[0] != st.TargetBranch && row[0] != st.PreCoordRef {
					kept = append(kept, row)
				}
			}
			rows = kept
		}
	}
	if len(rows) > 0 {
		return exitUnexplained(r.w, rows)
	}
	return nil
}

func sortStrings(xs []string) { sort.Strings(xs) }

// captureClaim is _capture_reconciliation_claim (pre-mutation).
func (r *run) captureClaim(st *MergeState) error {
	if st != nil {
		if _, err := os.Stat(filepath.Join(stateDir(r.root, st.MissionID), postFixMarker)); err != nil {
			sayErr(r.w, "a pre-fix in-flight merge state was detected (no reconciliation marker). "+
				"The terminus reconciliation guarantees cannot be retro-applied to it. "+
				"Run `spec-kitty consolidate --abort` to clear the stale state, verify the target "+
				"branch and lane tips by hand, then start a fresh `spec-kitty consolidate`.")
			return exit(1)
		}
	}
	if sha := revParse(r.root, r.coordRef()); sha != "" {
		r.checkpoint = &coordCheckpoint{r.coordRef(), sha}
	}
	coordBase := ""
	if st != nil && st.PreCoord != "" {
		coordBase = st.PreCoord
	} else if sha := revParse(r.root, r.coordRef()); sha != "" {
		coordBase = sha
	} else {
		coordBase = r.m.MissionBranch
	}
	window := ""
	if st != nil && st.PreTarget != "" {
		window = st.PreTarget
	} else {
		window = revParse(r.root, r.m.TargetBranch)
	}
	r.windowBase = window
	c, err := buildClaim(claimInput{root: r.root, featureDir: r.sf.StatusDir(), m: r.m, coordBase: coordBase, windowBase: window, excluded: r.excluded})
	if err != nil {
		fmt.Fprintf(r.w, "\nError: Reconciliation refused (fail-closed): a git probe failed while building the approved-WP claim: %s. %s and no refs/worktrees were mutated. Resolve the underlying git issue, then re-run the merge.\n", err.Error(), nothingTornDown)
		return exit(1)
	}
	r.claim = c
	r.validatedTips = c.boundLaneTips
	r.anchorShas = nil
	for _, ref := range []string{r.m.MissionBranch, window, coordBase} {
		if ref == "" {
			continue
		}
		if sha, err := resolveCommit(r.root, ref); err == nil && !contains(r.anchorShas, sha) {
			r.anchorShas = append(r.anchorShas, sha)
		}
	}
	if refusal := c.claimIntegrityRefusal(); refusal != "" && !r.verifiedLanding {
		return r.refuseClaim(refusal, st)
	}
	return nil
}

// refuseClaim is _exit_on_claim_integrity_refusal.
func (r *run) refuseClaim(refusal string, st *MergeState) error {
	sep := " "
	if strings.Contains(refusal, "\n") {
		sep = "\n"
	}
	moved := st != nil && st.movedBranches(r.root)
	before := ", before any change"
	change := "No branch, worktree or status record was changed by this run."
	if len(r.attested) > 0 {
		change = "No branch or worktree was changed by this run; only the operator attestation(s) for " + strings.Join(r.attested, ", ") + " were recorded."
	}
	footer := change + " Fix the cause, then re-run; " +
		"if an earlier attempt left partial state, run `" + consolidateAbortCommand + "` first."
	if moved {
		before = ""
		recorded := ""
		if len(r.attested) > 0 {
			recorded = " Only the operator attestation(s) for " + strings.Join(r.attested, ", ") + " were recorded by this run."
		}
		footer = refusedWithEarlierRecord + st.earlierMovesSentences(r.root) + recorded + " Then fix the cause and re-run."
	}
	fmt.Fprintf(r.w, "\nError: Reconciliation refused (fail-closed) at claim time%s: %s.%s%s\n", before, strings.TrimRight(refusal, "."), sep, footer)
	return exit(1)
}

// gate is _phase_reconcile_before_teardown's verdict (nil on PASS).
func (r *run) gate() error {
	if r.claim == nil {
		return nil
	}
	c := *r.claim
	if r.strategy == "squash" {
		c.verifyReachability = false
	}
	approvedByLane := map[string][]string{}
	approved := map[string]bool{}
	for _, a := range c.approved {
		approved[a.wp] = true
	}
	for _, l := range r.m.Lanes {
		for _, id := range l.WPIDs {
			if approved[id] {
				approvedByLane[l.LaneID] = append(approvedByLane[l.LaneID], id)
			}
		}
	}
	verified := liveTip(r.root, r.m.TargetBranch)
	var res verifyResult
	if refusal, err := laneTipsMovedRefusal(r.root, r.m, r.validatedTips, r.anchorShas, c.planningPrefix, approvedByLane); err != nil {
		res = refused("a git probe failed while re-checking the lane tips against their approval: " + err.Error())
	} else if refusal != "" {
		res = refused(refusal)
	} else {
		res = c.verify(r.root, r.m.TargetBranch)
	}
	if res.status == "pass" {
		if err := r.squashProjectionProof(); err != nil {
			return err
		}
		if r.state != nil && verified != "" {
			r.state.ReconPassed = verified
			r.state.Save(r.root)
		}
		return nil
	}
	fmt.Fprintln(r.w, "\nError: "+res.guidance())
	return exit(1)
}
