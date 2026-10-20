package lanes

import (
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// File is the lanes manifest filename.
const File = "lanes.json"

// CollapseEvent is a union-find merge event.
type CollapseEvent struct{ WPA, WPB, Rule, Evidence string }

// CollapseReport summarises merges.
type CollapseReport struct {
	Events                  []CollapseEvent
	IndependentWPsCollapsed int
}

// ToObject renders CollapseReport.to_dict().
func (c *CollapseReport) ToObject() *jsonx.Object {
	ev := []any{}
	byRule := &jsonx.Object{}
	for _, e := range c.Events {
		ev = append(ev, jsonx.NewObject("wp_a", e.WPA, "wp_b", e.WPB, "rule", e.Rule, "evidence", e.Evidence))
		n, _ := byRule.GetInt(e.Rule)
		byRule.Set(e.Rule, n+1)
	}
	return jsonx.NewObject("events", ev, "total_merges", len(c.Events),
		"independent_wps_collapsed", c.IndependentWPsCollapsed, "by_rule", byRule)
}

// Lane is an ExecutionLane.
type Lane struct {
	LaneID            string
	WPIDs             []string
	WriteScope        []string
	PredictedSurfaces []string
	DependsOnLanes    []string
	ParallelGroup     int
}

// Manifest is a LanesManifest.
type Manifest struct {
	Version             int
	MissionSlug         string
	MissionID           *string
	MissionBranch       string
	TargetBranch        string
	Lanes               []Lane
	ComputedAt          string
	ComputedFrom        string
	PlanningArtifactWPs []string
	CollapseReport      *CollapseReport
	PlanningCommitSHA   *string
}

func strs(l []string) []any {
	out := []any{}
	for _, s := range l {
		out = append(out, s)
	}
	return out
}

// ToObject renders LanesManifest.to_dict() in its fixed key order.
func (m *Manifest) ToObject() *jsonx.Object {
	lanes := []any{}
	for _, l := range m.Lanes {
		lanes = append(lanes, jsonx.NewObject(
			"lane_id", l.LaneID, "wp_ids", strs(l.WPIDs), "write_scope", strs(l.WriteScope),
			"predicted_surfaces", strs(l.PredictedSurfaces), "depends_on_lanes", strs(l.DependsOnLanes),
			"parallel_group", l.ParallelGroup))
	}
	var mid, sha any
	if m.MissionID != nil {
		mid = *m.MissionID
	}
	if m.PlanningCommitSHA != nil {
		sha = *m.PlanningCommitSHA
	}
	o := jsonx.NewObject(
		"version", m.Version, "mission_slug", m.MissionSlug, "mission_id", mid,
		"mission_branch", m.MissionBranch, "target_branch", m.TargetBranch, "lanes", lanes,
		"computed_at", m.ComputedAt, "computed_from", m.ComputedFrom,
		"planning_artifact_wps", strs(m.PlanningArtifactWPs), "planning_commit_sha", sha)
	if m.CollapseReport != nil && len(m.CollapseReport.Events) > 0 {
		o.Set("collapse_report", m.CollapseReport.ToObject())
	}
	return o
}

// Render returns json.dumps(indent=2) + "\n" (ensure_ascii default True).
func (m *Manifest) Render() []byte { return []byte(jsonx.Dumps(m.ToObject(), jsonx.Pretty) + "\n") }

// LaneForWP returns the lane holding wpID.
func (m *Manifest) LaneForWP(wpID string) *Lane {
	for i := range m.Lanes {
		for _, w := range m.Lanes[i].WPIDs {
			if w == wpID {
				return &m.Lanes[i]
			}
		}
	}
	return nil
}

func listOf(o *jsonx.Object, k string) []string {
	out := []string{}
	for _, x := range o.GetList(k) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Read loads <missionDir>/lanes.json (nil, nil when absent).
func Read(missionDir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(missionDir, File))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, err
	}
	m := &Manifest{
		MissionSlug:         o.GetString("mission_slug"),
		MissionBranch:       o.GetString("mission_branch"),
		TargetBranch:        o.GetString("target_branch"),
		ComputedAt:          o.GetString("computed_at"),
		ComputedFrom:        o.GetString("computed_from"),
		PlanningArtifactWPs: listOf(o, "planning_artifact_wps"),
	}
	m.Version, _ = o.GetInt("version")
	if s := o.GetString("mission_id"); s != "" {
		m.MissionID = &s
	}
	if s := o.GetString("planning_commit_sha"); s != "" {
		m.PlanningCommitSHA = &s
	}
	for _, x := range o.GetList("lanes") {
		lo, _ := x.(*jsonx.Object)
		if lo == nil {
			continue
		}
		l := Lane{LaneID: lo.GetString("lane_id"), WPIDs: listOf(lo, "wp_ids"), WriteScope: listOf(lo, "write_scope"),
			PredictedSurfaces: listOf(lo, "predicted_surfaces"), DependsOnLanes: listOf(lo, "depends_on_lanes")}
		l.ParallelGroup, _ = lo.GetInt("parallel_group")
		m.Lanes = append(m.Lanes, l)
	}
	if cr := o.GetObject("collapse_report"); cr != nil {
		rep := &CollapseReport{}
		rep.IndependentWPsCollapsed, _ = cr.GetInt("independent_wps_collapsed")
		for _, x := range cr.GetList("events") {
			eo, _ := x.(*jsonx.Object)
			if eo == nil {
				continue
			}
			rep.Events = append(rep.Events, CollapseEvent{eo.GetString("wp_a"), eo.GetString("wp_b"), eo.GetString("rule"), eo.GetString("evidence")})
		}
		m.CollapseReport = rep
	}
	return m, nil
}

// Write atomically writes <missionDir>/lanes.json.
func Write(missionDir string, m *Manifest) error {
	return atomicfile.Write(filepath.Join(missionDir, File), m.Render())
}
