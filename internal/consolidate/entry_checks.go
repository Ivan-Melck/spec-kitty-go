package consolidate

// Entry preflights of the consolidate run that refuse before any state is
// written: the sparse-checkout preflight (git/sparse_checkout.py
// require_no_sparse_checkout) and the protected status-target refusal
// (preflight.refuse_protected_status_target, #5385).

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// SparseCheckoutRefusal is SparseCheckoutPreflightError's message ("" when
// no blocking sparse-checkout state is active).
func SparseCheckoutRefusal(root, command string) string {
	rep := healthcheck.ScanSparse(root)
	if !rep.AnyBlocking() {
		return ""
	}
	var affected []string
	if rep.Primary.Blocking() {
		affected = append(affected, "  "+rep.Primary.Path)
	}
	for _, w := range rep.Worktrees {
		if w.Blocking() {
			affected = append(affected, "  "+w.Path)
		}
	}
	return command + " aborted: legacy sparse-checkout state detected.\n" +
		"\nAffected paths:\n" + strings.Join(affected, "\n") + "\n" +
		"\nThis repository has core.sparseCheckout=true configured, which\n" +
		"v3.x spec-kitty does not handle correctly and which has caused\n" +
		"silent data loss in prior mission merges (see\n" +
		"spec-kitty/spec-kitty#588).\n" +
		"\nFix:\n" +
		"  spec-kitty doctor sparse-checkout --fix\n" +
		"\nIf you have an intentional sparse configuration and understand\n" +
		"the risk, you may pass --allow-sparse-checkout to proceed. Use of\n" +
		"this override is logged at WARNING level."
}

// PolicyRefusal is a workflow-mutation-policy Refused verdict.
type PolicyRefusal struct{ Code, Message, NextStep string }

func pendingDoneWPs(sf *coord.Surface, m *lanes.Manifest, excluded map[string]bool) []string {
	cur := map[string]string{}
	if snap, err := status.ReduceDir(sf.StatusDir()); err == nil {
		for _, id := range snap.WorkPackages.Keys() {
			if st := snap.WP(id); st != nil {
				cur[id] = st.GetString("lane")
			}
		}
	}
	var out []string
	for _, l := range m.Lanes {
		for _, id := range l.WPIDs {
			if !excluded[id] && cur[id] != "done" {
				out = append(out, id)
			}
		}
	}
	return out
}

// ProtectedStatusTargetRefusal is refuse_protected_status_target: the policy
// refusal the consolidation's done bookkeeping would hit on a coordination-
// less mission whose recorded target is protected (nil when none).
func ProtectedStatusTargetRefusal(root, slug string, m *lanes.Manifest, excluded map[string]bool) *PolicyRefusal {
	sf := coord.Resolve(root, slug)
	pending := pendingDoneWPs(sf, m, excluded)
	if len(pending) == 0 || !sf.Transactional() || strings.TrimSpace(sf.Meta.GetString("coordination_branch")) != "" {
		return nil
	}
	ref := strings.TrimSpace(sf.Meta.GetString("target_branch"))
	if ref == "" {
		ref = m.TargetBranch
	}
	op := pyRepr("status transition " + pending[0])
	if !branchExists(root, ref) {
		return &PolicyRefusal{"DESTINATION_REF_NOT_FOUND",
			fmt.Sprintf("Refusing to record %s: destination_ref %s does not exist in %s.", op, pyRepr(ref), root),
			"Confirm the branch exists locally ('git branch --list'), or create it first."}
	}
	if !coord.IsProtected(root, slug, ref) {
		return nil
	}
	return &PolicyRefusal{"PROTECTED_BRANCH_REFUSED",
		fmt.Sprintf("Refusing to record %s: destination ref %s is on this project's protected branch list. ", op, pyRepr(ref)) +
			"This mission uses a lanes or single-branch topology, which mints no coordination worktree, so bookkeeping cannot be redirected to one.",
		fmt.Sprintf("Commit bookkeeping to a non-protected branch, or declare %s unprotected for this repo by setting %s to a truthy value.", pyRepr(ref), coord.HatchEnv)}
}
