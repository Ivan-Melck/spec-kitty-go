package safecommit

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Lifecycle phases (mission_runtime.lifecycle_phase.LifecyclePhase).
const (
	PhasePreConsolidation = "pre_consolidation"
	PhaseConsolidated     = "consolidated"
	PhasePublished        = "published"
)

// CodeConsolidatedContentAbsent is the E2 off-checkout refusal code.
const CodeConsolidatedContentAbsent = "CONSOLIDATED_CONTENT_ABSENT"

// PrimaryBranchNoBias is resolve_primary_branch(root, bias=False): origin's
// HEAD, the current branch only when it is a common primary name, a common
// branch present locally or on origin, else the feature-bias cascade.
func PrimaryBranchNoBias(root string) string {
	if out, err := gitx.Run(root, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/origin/"); b != "" {
			return b
		}
	}
	cur := gitx.CurrentBranch(root)
	for _, b := range commonPrimary {
		if cur == b {
			return cur
		}
	}
	for _, b := range commonPrimary {
		if gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+b) || gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+b) {
			return b
		}
	}
	return PrimaryBranch(root)
}

func mainRoot(root string) string {
	if m := project.MainRepoRoot(root); m != "" {
		return m
	}
	return root
}

// LifecyclePhase is resolve_lifecycle_phase: pre-consolidation without a
// baseline_merge_commit; consolidated (E1) while the Target Ref exists;
// published (E2) once it is gone and the mission shows terminal completion
// (a mission_number, or every WP done in the primary status log).
func LifecyclePhase(root, slug string) string {
	m := mainRoot(root)
	dir := project.Specs(m, slug)
	meta := mission.ReadMetaOrEmpty(dir)
	if strings.TrimSpace(fmt.Sprint(metaValue(meta.Get("baseline_merge_commit")))) == "" {
		return PhasePreConsolidation
	}
	if gitx.OK(m, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+TargetBranch(root, slug)) {
		return PhaseConsolidated
	}
	if v, ok := meta.Get("mission_number"); ok && v != nil {
		return PhasePublished
	}
	if snap, err := status.ReduceDir(dir); err == nil && snap != nil && snap.WorkPackages != nil && snap.WorkPackages.Len() > 0 {
		for _, id := range snap.WorkPackages.Keys() {
			if snap.Lane(id) != "done" {
				return PhasePreConsolidation
			}
		}
		return PhasePublished
	}
	return PhasePreConsolidation
}

func metaValue(v any, ok bool) any {
	if !ok || v == nil {
		return ""
	}
	return v
}

// ContentPresentAtPrimaryTip is content_present_at_primary_tip: the
// mission's meta.json is committed at the resolved Primary-Branch tip.
func ContentPresentAtPrimaryTip(root, slug string) (bool, error) {
	m := mainRoot(root)
	branch := PrimaryBranchNoBias(m)
	if !gitx.OK(m, "rev-parse", "--verify", "--quiet", branch) {
		return false, fmt.Errorf("git rev %s does not resolve to a valid revision in %s -- expected the resolved Primary Branch to be a real ref", Repr(branch), m)
	}
	return gitx.OK(m, "cat-file", "-e", branch+":kitty-specs/"+slug+"/meta.json"), nil
}

// e2ShortCircuitKind is _is_published_write_short_circuit_kind.
func e2ShortCircuitKind(kind string) bool {
	switch kind {
	case mission.KindIssueMatrix, mission.KindTracerFile, mission.KindAcceptanceMatrix, mission.KindReviewCycle, mission.KindStatusState:
		return true
	}
	return IsPrimaryKind(kind)
}

// ResolvePlacement is resolve_placement_only(root, slug, kind=kind).ref,
// including the E2 (published) CONSOLIDATED short-circuit: the Primary
// Branch when the checkout carries the consolidated content, else a
// CONSOLIDATED_CONTENT_ABSENT refusal.
func ResolvePlacement(root, slug, kind string) (string, error) {
	if e2ShortCircuitKind(kind) && LifecyclePhase(root, slug) == PhasePublished {
		branch := PrimaryBranchNoBias(mainRoot(root))
		ok, err := ContentPresentAtPrimaryTip(root, slug)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", &Error{Code: CodeConsolidatedContentAbsent, Message: fmt.Sprintf("mission %s is published (its Target Ref has been deleted) but this checkout does not carry its consolidated content; check out %s (the resolved Primary Branch) and retry.", Repr(slug), Repr(branch))}
		}
		return branch, nil
	}
	return topologyPlacement(root, slug, kind), nil
}

// IsPostConsolidationWriteTarget is
// write_seam.is_post_consolidation_write_target: the resolved ref is the
// Primary Branch and differs from the mission's stored target_branch.
func IsPostConsolidationWriteTarget(root, slug, kind, ref string) bool {
	if kind == mission.KindStatusState || kind == mission.KindDecisionLog {
		return false
	}
	if ref != PrimaryBranchNoBias(mainRoot(root)) {
		return false
	}
	return ref != TargetBranch(root, slug)
}
