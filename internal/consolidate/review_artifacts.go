package consolidate

// The review-artifact consistency gate (post_merge.review_artifact_consistency):
// a terminal (approved/done) WP whose event-sourced review verdict is
// changes_requested, without a complete arbiter override, blocks the merge
// and the dry-run forecast.

import (
	"fmt"
	"io"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const (
	rejectedReviewArtifactConflict  = "REJECTED_REVIEW_ARTIFACT_CONFLICT"
	rejectedReviewArtifactInvariant = "terminal_wp_latest_review_artifact_must_not_be_rejected"
)

var rejectedReviewArtifactRemediation = []string{
	"Run another review cycle that writes an approved review-cycle artifact.",
	"Or move the WP out of approved/done before merge.",
}

// ReviewArtifactFinding is RejectedReviewArtifactFinding (artifact_path is
// always None and cycle_number 0 since the pure-event repoint).
type ReviewArtifactFinding struct{ WPID, Lane, Verdict string }

// Format is format_review_artifact_finding.
func (f ReviewArtifactFinding) Format() string {
	return fmt.Sprintf("%s is lane '%s', but the event-sourced review verdict is '%s' and no on-disk review artifact exists.", f.WPID, f.Lane, f.Verdict)
}

// Diagnostic is review_artifact_finding_diagnostic.
func (f ReviewArtifactFinding) Diagnostic() *jsonx.Object {
	rem := make([]any, len(rejectedReviewArtifactRemediation))
	for i, s := range rejectedReviewArtifactRemediation {
		rem[i] = s
	}
	return jsonx.NewObject("diagnostic_code", rejectedReviewArtifactConflict, "branch_or_work_package", f.WPID,
		"violated_invariant", rejectedReviewArtifactInvariant, "remediation", rem, "lane", f.Lane,
		"latest_review_cycle_path", nil, "latest_review_cycle_verdict", f.Verdict, "review_cycle_number", 0)
}

// completeOverride is ReviewOverride.from_dict(state["review"]).complete
// (fields str()-coerced; a missing key or a non-mapping is no override).
func completeOverride(state *jsonx.Object) bool {
	rv := state.GetObject("review")
	if rv == nil {
		return false
	}
	for _, k := range []string{"at", "actor", "wp_id", "reason"} {
		v, ok := rv.Get(k)
		if !ok || pyjson.Str(v) == "" {
			return false
		}
	}
	return true
}

// eventVerdict is _event_sourced_gate_verdict: str(review_result.verdict)
// when the slot holds a decodable ReviewResult, ok=false otherwise.
func eventVerdict(state *jsonx.Object) (string, bool) {
	rr := state.GetObject("review_result")
	if rr == nil {
		return "", false
	}
	for _, k := range []string{"reviewer", "verdict", "reference"} {
		if !rr.Has(k) {
			return "", false
		}
	}
	v, _ := rr.Get("verdict")
	return pyjson.Str(v), true
}

// ReviewArtifactFindings is find_rejected_review_artifact_conflicts over the
// STATUS_STATE dir (materialize_snapshot: read errors propagate). wpIDs nil
// selects every WP of the snapshot, sorted.
func ReviewArtifactFindings(statusDir string, wpIDs []string) ([]ReviewArtifactFinding, error) {
	snap, err := status.MaterializeSnapshot(statusDir)
	if err != nil {
		return nil, err
	}
	selected := wpIDs
	if len(selected) == 0 {
		selected = append([]string(nil), snap.WorkPackages.Keys()...)
		sort.Strings(selected)
	}
	var out []ReviewArtifactFinding
	for _, id := range selected {
		state := snap.WP(id)
		if state == nil {
			continue
		}
		lane := pyjson.Str(valueOf(state, "lane", ""))
		if lane != "approved" && lane != "done" {
			continue
		}
		if completeOverride(state) {
			continue
		}
		v, ok := eventVerdict(state)
		if !ok || v != "changes_requested" {
			continue
		}
		out = append(out, ReviewArtifactFinding{WPID: id, Lane: lane, Verdict: v})
	}
	return out, nil
}

func valueOf(o *jsonx.Object, k string, def any) any {
	if v, ok := o.Get(k); ok {
		return v
	}
	return def
}

// printReviewArtifactBlock is the human rendering shared by the forecast and
// _enforce_review_artifact_consistency.
func printReviewArtifactBlock(w io.Writer, findings []ReviewArtifactFinding, slug string) {
	say(w, "[red]Error:[/red] Review artifact consistency gate failed.")
	for _, f := range findings {
		say(w, "  - "+f.Format())
		say(w, "    diagnostic_code: "+rejectedReviewArtifactConflict)
		say(w, "    branch_or_work_package: "+f.WPID)
		say(w, "    violated_invariant: "+rejectedReviewArtifactInvariant)
		say(w, "    latest_review_cycle_path: None")
		say(w, "    latest_review_cycle_verdict: "+f.Verdict)
		for _, l := range rejectedReviewArtifactRemediation {
			say(w, "    remediation: "+l)
		}
	}
	say(w, "  Mission: "+slug)
}

// forecastReviewArtifacts is _preview_review_artifacts +
// _emit_review_artifact_block: (handled, err). An unreadable status surface
// is an uncaught exception, like upstream.
func forecastReviewArtifacts(w io.Writer, o Options, statusDir, slug, target string, wpIDs []string) (bool, error) {
	findings, err := ReviewArtifactFindings(statusDir, wpIDs)
	if err != nil {
		return true, uncaught(o.Stderr, err, forecastFrames)
	}
	if len(findings) == 0 {
		return false, nil
	}
	if o.JSON {
		diags := make([]any, len(findings))
		for i, f := range findings {
			diags[i] = f.Diagnostic()
		}
		fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("spec_kitty_version", app.Version, "mission_slug", slug, "target_branch", target,
			"blocked", true, "blockers", diags, "diagnostic_code", rejectedReviewArtifactConflict), compact))
		return true, exit(1)
	}
	printReviewArtifactBlock(w, findings, slug)
	return true, exit(1)
}

// enforceReviewArtifacts is _enforce_review_artifact_consistency (with the
// --skip-review-artifact-check escape hatch recorded as committed
// ReviewOverride evidence on the status surface).
func (r *run) enforceReviewArtifacts(wpIDs []string) error {
	findings, err := ReviewArtifactFindings(r.sf.StatusDir(), wpIDs)
	if err != nil {
		return uncaught(r.opts.Stderr, err, mergeFrames)
	}
	if len(findings) == 0 {
		return nil
	}
	if r.opts.SkipReviewArtifact {
		note := r.opts.Note
		if note == "" {
			note = "review-artifact gate skipped via --skip-review-artifact-check"
		}
		recordSkipEvidence(r.w, r.sf, r.root, findings, note)
		return nil
	}
	printReviewArtifactBlock(r.w, findings, r.slug)
	return exit(1)
}

func recordSkipEvidence(w io.Writer, sf *coord.Surface, root string, findings []ReviewArtifactFinding, note string) {
	actor := mergeActor(root)
	stamp := clock.Now().UTC().Format("2006-01-02T15:04:05Z")
	say(w, "[yellow]⚠️  Review-artifact consistency gate BYPASSED via --skip-review-artifact-check.[/yellow]")
	say(w, "    Reason (recorded as override evidence): "+note)
	for _, f := range findings {
		say(w, "    - "+f.WPID+": skip recorded as override evidence")
		delta := status.Delta{Review: &status.ReviewOverride{At: stamp, Actor: actor, WPID: f.WPID, Reason: note}}
		sf.EmitAnnotationAt(f.WPID, delta, status.StrActor(actor), true, stamp)
	}
}

var _ = rich.Print
