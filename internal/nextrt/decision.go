// Package nextrt implements `spec-kitty next`: the mission run store
// (.kittify/runtime), the DAG planner over the frozen mission-runtime
// template, the finalized-task-board override, the analysis gate and the
// decision envelope (runtime.next).
package nextrt

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Decision kinds.
const (
	KindStep             = "step"
	KindDecisionRequired = "decision_required"
	KindBlocked          = "blocked"
	KindTerminal         = "terminal"
	KindQuery            = "query"
)

// Guard-failure messages and analysis error codes (runtime_bridge_cores).
const (
	MissingArtifactMessage  = "Required artifact missing: %s"
	MissingTaskFilesMessage = "Required: at least one tasks/WP*.md file"
	AnalysisMissingMessage  = "Analysis report missing: analysis-report.md has not been recorded"
	AnalysisStaleMessage    = "Analysis report is stale: %s"
	AnalysisReportMissing   = "ANALYSIS_REPORT_MISSING"
	AnalysisReportStale     = "ANALYSIS_REPORT_STALE"
	AnalyzeStepID           = "analyze"
	mergedDoneReason        = "All work packages are done"
)

// Decision is runtime.next.decision.Decision.
type Decision struct {
	Kind, Agent, MissionSlug, Mission, MissionState, Timestamp string
	Action, WPID, WorkspacePath, PromptFile, Reason            *string
	GuardFailures                                              []string
	GuardFailurePaths                                          *jsonx.Object
	Progress                                                   *jsonx.Object
	Origin                                                     *jsonx.Object
	RunID, StepID, DecisionID, InputKey, Question              *string
	Options                                                    []string
	IsQuery                                                    bool
	PreviewStep                                                *string
	MissionNumber                                              string
	MissionType                                                string
	ErrorCode                                                  *string
}

func sp(s string) *string { return &s }

func spOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func pv(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// ToObject is Decision.to_dict().
func (d *Decision) ToObject() *jsonx.Object {
	gf := []any{}
	for _, g := range d.GuardFailures {
		gf = append(gf, g)
	}
	gfp := d.GuardFailurePaths
	if gfp == nil {
		gfp = &jsonx.Object{}
	}
	origin := d.Origin
	if origin == nil {
		origin = &jsonx.Object{}
	}
	var progress, options any
	if d.Progress != nil {
		progress = d.Progress
	}
	if d.Options != nil {
		l := []any{}
		for _, o := range d.Options {
			l = append(l, o)
		}
		options = l
	}
	mt := d.MissionType
	if mt == "" {
		mt = d.Mission
	}
	o := jsonx.NewObject("kind", d.Kind, "agent", agentValue(d.Agent), "mission_slug", d.MissionSlug,
		"mission_number", d.MissionNumber, "mission_type", mt, "mission", d.Mission, "mission_state", d.MissionState,
		"timestamp", d.Timestamp, "action", pv(d.Action), "wp_id", pv(d.WPID), "workspace_path", pv(d.WorkspacePath),
		"prompt_file", pv(d.PromptFile), "reason", pv(d.Reason), "guard_failures", gf, "guard_failure_paths", gfp,
		"progress", progress, "origin", origin, "run_id", pv(d.RunID), "step_id", pv(d.StepID),
		"decision_id", pv(d.DecisionID), "input_key", pv(d.InputKey), "question", pv(d.Question), "options", options,
		"is_query", d.IsQuery, "preview_step", pv(d.PreviewStep))
	if d.ErrorCode != nil {
		o.Set("error_code", *d.ErrorCode)
	}
	return o
}

func agentValue(a string) any {
	if a == "" {
		return nil
	}
	return a
}

// materialize is step_or_blocked: a step whose prompt file does not exist
// on disk degrades to blocked (keeping the envelope's reason).
func materialize(d *Decision) *Decision {
	if d.Kind == KindQuery {
		d.IsQuery = true
	}
	if d.Kind != KindStep {
		return d
	}
	if d.PromptFile == nil || *d.PromptFile == "" {
		d.Kind = KindBlocked
		if d.Reason == nil {
			d.Reason = sp("prompt_file_not_resolvable")
		}
		return d
	}
	if st, err := os.Stat(*d.PromptFile); err != nil || !st.Mode().IsRegular() {
		d.Kind = KindBlocked
		d.Reason = sp("prompt_file_not_resolvable")
		d.PromptFile = nil
		return d
	}
	d.Reason = nil
	return d
}
