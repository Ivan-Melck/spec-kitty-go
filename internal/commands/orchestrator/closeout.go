package orchestrator

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/consolidate"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// ── accept-mission ────────────────────────────────────────────────────────

func runAcceptMission(ctx *cli.Context) error {
	v := newVerb(ctx, "accept-mission")
	root := v.root()
	handle, actor := ctx.String("mission"), ctx.String("actor")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	snap, err := status.Materialize(m.StatusDir)
	if err != nil || snap == nil {
		snap = reduceStatus(m.StatusDir)
	}
	g := buildDepGraph(m.PrimaryDir)
	ids := map[string]bool{}
	for _, id := range g.order {
		ids[id] = true
	}
	for _, id := range snap.WorkPackages.Keys() {
		ids[id] = true
	}
	var incomplete []string
	for id := range ids {
		lane := status.ResolveLaneAlias(wpLane(snap, id))
		if lane != string(status.Approved) && lane != string(status.Done) {
			incomplete = append(incomplete, id)
		}
	}
	sort.Strings(incomplete)
	if len(incomplete) > 0 {
		return v.fail("MISSION_NOT_READY", fmt.Sprintf("Mission has %d incomplete WP(s)", len(incomplete)),
			identity(m.StatusDir, "incomplete_wps", strList(incomplete)))
	}
	sum, err := acceptance.Collect(root, m.Slug, acceptance.CollectOptions{StrictMetadata: true, MutateMatrix: true})
	if err != nil {
		return v.fail("MISSION_NOT_READY", err.Error(), orch.Identity(m.StatusDir))
	}
	if !sum.OK() {
		outstanding := jsonx.NewObject()
		for _, b := range sum.Outstanding() {
			outstanding.Set(b.Key, strList(b.Values))
		}
		return v.fail("MISSION_NOT_READY", acceptance.ChecksFailedMessage, identity(m.StatusDir,
			"outstanding", outstanding, "activity_issues", strList(sum.ActivityIssues),
			"skipped_checks", checkList(sum.Skipped), "blocked_checks", checkList(sum.Blocked)))
	}
	if err := stampAcceptance(sum, actor); err != nil {
		return v.fail("MISSION_NOT_READY", err.Error(), orch.Identity(m.StatusDir))
	}
	acceptedAt := mission.ReadMetaOrEmpty(sum.FeatureDir).GetString("accepted_at")
	_, approved, done, _ := sum.LaneDerivations()
	accepted := append(append([]string{}, approved...), done...)
	return v.ok(identity(m.StatusDir, "accepted", true, "mode", "auto", "accepted_at", acceptedAt,
		"accepted_wps", strList(accepted), "approved_wps", strList(approved), "done_wps", strList(done),
		"merge_pending_wps", strList(approved)))
}

// ── consolidate-mission ───────────────────────────────────────────────────

func runConsolidateMission(ctx *cli.Context) error {
	v := newVerb(ctx, "consolidate-mission")
	strategy := ctx.String("strategy")
	supported := []string{"merge", "rebase", "squash"}
	if strategy != "merge" && strategy != "squash" && strategy != "rebase" {
		return v.fail("UNSUPPORTED_STRATEGY", fmt.Sprintf("Strategy '%s' is not supported. Supported strategies: %s", strategy, pyjson.ReprStrings(supported)),
			jsonx.NewObject("strategy", strategy, "supported", strList(supported)))
	}
	root := v.root()
	handle := ctx.String("mission")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	target, errs := consolidate.MergePreflight(root, m.Slug, ctx.String("target"))
	if len(errs) > 0 {
		return v.fail("PREFLIGHT_FAILED", "Merge failed", identity(m.StatusDir, "target_branch", target, "errors", strList(errs)))
	}
	err = consolidate.RunLaneMerge(root, consolidate.LaneMergeOptions{Handle: handle, Slug: m.Slug, Target: target,
		Strategy: strategy, Push: ctx.Bool("push"), Log: ctx.Stderr, Console: ctx.Stderr})
	if err != nil {
		var dr *consolidate.DestructiveRefusal
		if errors.As(err, &dr) {
			code := "PREFLIGHT_FAILED"
			if orch.IsAllowedErrorCode(dr.Code) {
				code = dr.Code
			}
			data := identity(m.StatusDir, "target_branch", target, "errors", strList([]string{dr.Error()}),
				"destructive_op_error_code", dr.Code, "remediation", dr.Remediation, "worktree_path", dr.Worktree)
			if len(dr.Entries) > 0 {
				data.Set("dirty_entries", strList(dr.Entries))
			}
			return v.fail(code, "Merge refused: destructive operation safety check failed", data)
		}
		data := identity(m.StatusDir, "target_branch", target, "errors", strList([]string{err.Error()}))
		var le *consolidate.LaneMergeError
		if errors.As(err, &le) && le.TeardownCode != "" {
			data.Set("teardown_error_code", le.TeardownCode)
		}
		return v.fail("PREFLIGHT_FAILED", "Merge failed", data)
	}
	return v.ok(identity(m.StatusDir, "merged", true, "target_branch", target, "strategy", strategy, "worktree_removed", false))
}

func checkList(cs []acceptance.Check) []any {
	out := []any{}
	for _, c := range cs {
		out = append(out, jsonx.NewObject("check", c.Check, "detail", c.Detail))
	}
	return out
}

// stampAcceptance is acceptance._stamp_acceptance_record(summary, actor,
// "orchestrator", None): the FR-010 pre-stamp verdict re-check, then the
// meta.json acceptance record (no commit).
func stampAcceptance(s *acceptance.Summary, actor string) error {
	if s.MatrixDir == "" {
		if s.MatrixSkipReason != acceptance.PlanningArtifactOnlySkip {
			return fmt.Errorf("Cannot record acceptance: no acceptance-matrix gate outcome was recorded for mission %s and this is not a planning-artifact-only mission. Refusing to stamp acceptance without the FR-010 pre-stamp verdict re-check.", mission.PyRepr(s.Feature))
		}
	} else {
		mtx, _ := acceptance.ReadMatrix(s.MatrixDir)
		verdict := "pending"
		if mtx != nil {
			verdict = mtx.OverallVerdict()
		}
		if verdict != "pass" && verdict != acceptance.VerdictPassPendingConsolidation {
			return fmt.Errorf("Acceptance matrix verdict is not ready to accept: Acceptance matrix at %s carries verdict %s, not pass; refusing to record acceptance.", s.MatrixDir, mission.PyRepr(verdict))
		}
	}
	return acceptance.RecordAcceptance(s.FeatureDir, actor, "orchestrator", "", "")
}
