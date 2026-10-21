package orchestrator

import (
	"fmt"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/cli"
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
