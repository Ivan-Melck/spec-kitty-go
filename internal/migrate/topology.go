package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/placement"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Mission topologies (MissionTopology values).
const (
	TopoSingleBranch   = "single_branch"
	TopoLanes          = "lanes"
	TopoCoord          = "coord"
	TopoLanesWithCoord = "lanes_with_coord"
)

// PlanningLaneID is the canonical repo-root planning lane.
const PlanningLaneID = "lane-planning"

// StoredTopology is MissionTopology.from_stored(meta["topology"]): "" when
// absent or not one of the four values.
func StoredTopology(meta *jsonx.Object) string {
	if meta == nil {
		return ""
	}
	s, ok := meta.Get("topology")
	if !ok {
		return ""
	}
	switch v, _ := s.(string); v {
	case TopoSingleBranch, TopoLanes, TopoCoord, TopoLanesWithCoord:
		return v
	}
	return ""
}

// ClassifyTopology is classify_topology(coordination_branch, has_lanes).
func ClassifyTopology(hasCoord, hasLanes bool) string {
	switch {
	case hasCoord && hasLanes:
		return TopoLanesWithCoord
	case hasCoord:
		return TopoCoord
	case hasLanes:
		return TopoLanes
	}
	return TopoSingleBranch
}

// RoutesThroughCoordination reports coord / lanes_with_coord.
func RoutesThroughCoordination(t string) bool { return t == TopoCoord || t == TopoLanesWithCoord }

// LanesError is CorruptLanesError.
type LanesError struct{ Msg string }

func (e *LanesError) Error() string { return e.Msg }

// LaneIDs reads lanes.json like read_lanes_json + LanesManifest.from_dict and
// returns the lane ids; (nil, false, nil) when the file is absent.
func LaneIDs(dir string) ([]string, bool, error) {
	p := filepath.Join(dir, "lanes.json")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, true, &LanesError{fmt.Sprintf("lanes.json at %s is corrupt or malformed: %v", p, err)}
	}
	corrupt := func(detail string) error {
		return &LanesError{fmt.Sprintf("lanes.json at %s is corrupt or malformed: %s", p, detail)}
	}
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, true, corrupt(err.Error())
	}
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil, true, corrupt(err.Error())
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		if _, isList := v.([]any); isList {
			return nil, true, corrupt("list indices must be integers or slices, not str")
		}
		return nil, true, corrupt(fmt.Sprintf("'%s' object is not subscriptable", pyjson.TypeName(v)))
	}
	for _, k := range []string{"mission_slug", "version", "mission_branch", "target_branch", "lanes"} {
		if !o.Has(k) {
			return nil, true, corrupt(pyjson.ReprString(k))
		}
	}
	lanesV, _ := o.Get("lanes")
	list, ok := lanesV.([]any)
	if !ok {
		return nil, true, corrupt(fmt.Sprintf("'%s' object is not iterable", pyjson.TypeName(lanesV)))
	}
	var ids []string
	for _, l := range list {
		lo, ok := l.(*jsonx.Object)
		if !ok {
			return nil, true, corrupt("string indices must be integers, not 'str'")
		}
		for _, k := range []string{"lane_id", "wp_ids"} {
			if !lo.Has(k) {
				return nil, true, corrupt(pyjson.ReprString(k))
			}
		}
		id, _ := lo.Get("lane_id")
		ids = append(ids, pyjson.Str(id))
	}
	for _, k := range []string{"computed_at", "computed_from"} {
		if !o.Has(k) {
			return nil, true, corrupt(pyjson.ReprString(k))
		}
	}
	return ids, true, nil
}

// HasCodeLanes is _has_lanes: lanes.json carries a non-planning lane
// (corrupt or absent → false).
func HasCodeLanes(dir string) bool {
	ids, _, err := LaneIDs(dir)
	if err != nil {
		return false
	}
	for _, id := range ids {
		if id != PlanningLaneID {
			return true
		}
	}
	return false
}

// DeriveTopology is _derive_topology(meta, dir).
func DeriveTopology(meta *jsonx.Object, dir string) string {
	v, _ := meta.Get("coordination_branch")
	return ClassifyTopology(pyjson.Truthy(v), HasCodeLanes(dir))
}

// TopologyFromMeta is topology_from_meta: stored value, else the runtime
// reading of the derived cell (single_branch reads as lanes).
func TopologyFromMeta(meta *jsonx.Object, dir string) string {
	if s := StoredTopology(meta); s != "" {
		return s
	}
	t := DeriveTopology(meta, dir)
	if t == TopoSingleBranch {
		return TopoLanes
	}
	return t
}

// CoordBranchExists is surface_resolver._coord_branch_exists (see
// placement.CoordBranchExists).
func CoordBranchExists(root, branch string) bool { return placement.CoordBranchExists(root, branch) }

// TopologyResult is TopologyBackfillResult.
type TopologyResult struct {
	Slug     string
	Action   string // wrote | skip | error
	Topology *string
	Reason   *string
}

// BackfillMissionTopology is backfill_mission_topology.
func BackfillMissionTopology(dir string, dryRun, runtimeReading bool) TopologyResult {
	slug := filepath.Base(dir)
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return TopologyResult{Slug: slug, Action: "skip", Reason: strPtr("meta.json not found")}
	}
	meta, err := LoadMetaFailClosed(dir)
	if err != nil {
		warnf("Corrupt meta.json in %s: %v", slug, err)
		return TopologyResult{Slug: slug, Action: "error", Reason: strPtr("corrupt json: " + err.Error())}
	}
	if meta == nil {
		meta = &jsonx.Object{}
	}
	if s := StoredTopology(meta); s != "" {
		return TopologyResult{Slug: slug, Action: "skip", Topology: strPtr(s), Reason: strPtr("topology already present")}
	}
	topo := DeriveTopology(meta, dir)
	if runtimeReading && topo == TopoSingleBranch {
		topo = TopoLanes
	}
	if RoutesThroughCoordination(topo) {
		if v, _ := meta.Get("coordination_branch"); pyjson.Truthy(v) {
			root := filepath.Dir(filepath.Dir(dir))
			if !CoordBranchExists(root, pyjson.Str(v)) {
				warnf("Skipping topology backfill for %s: coordination_branch %s is absent from git (never created or deleted). Flatten the mission (remove coordination_branch from meta.json) or create the branch before backfilling. #2250", slug, pyjson.Repr(v))
				return TopologyResult{Slug: slug, Action: "skip", Reason: strPtr("coordination_branch absent from git")}
			}
		}
	}
	if !dryRun {
		UpdateMeta(dir, func(fresh *jsonx.Object) bool {
			if StoredTopology(fresh) != "" {
				return false
			}
			fresh.Set("topology", topo)
			if !fresh.Has("flattened") {
				fresh.Set("flattened", false)
			}
			return true
		})
	}
	return TopologyResult{Slug: slug, Action: "wrote", Topology: strPtr(topo)}
}

// BackfillTopology is backfill_topology_repo.
func BackfillTopology(root string, dryRun bool, missionSlug *string, runtimeReading bool) []TopologyResult {
	var out []TopologyResult
	for _, name := range scopedMissionDirs(root, missionSlug) {
		out = append(out, BackfillMissionTopology(project.Specs(root, name), dryRun, runtimeReading))
	}
	return out
}

// RestampResult is backfill_topology.RestampResult.
type RestampResult struct {
	Slug   string
	Action string // restamped | skip | error
	Reason *string
}

// RestampSingleBranch is restamp_single_branch_with_code_lanes.
func RestampSingleBranch(root string, dryRun bool, missionSlug *string) []RestampResult {
	var out []RestampResult
	for _, name := range scopedMissionDirs(root, missionSlug) {
		dir := project.Specs(root, name)
		meta, err := LoadMetaFailClosed(dir)
		if err != nil {
			out = append(out, RestampResult{name, "error", strPtr("corrupt meta.json: " + err.Error())})
			continue
		}
		if meta == nil {
			out = append(out, RestampResult{name, "skip", strPtr("no meta.json")})
			continue
		}
		if TopologyFromMeta(meta, dir) != TopoSingleBranch {
			out = append(out, RestampResult{name, "skip", strPtr("topology is not single_branch")})
			continue
		}
		ids, present, lerr := LaneIDs(dir)
		if lerr != nil {
			out = append(out, RestampResult{name, "skip", strPtr("lanes.json unreadable: " + lerr.Error())})
			continue
		}
		code := false
		for _, id := range ids {
			if id != PlanningLaneID {
				code = true
			}
		}
		if !present || !code {
			out = append(out, RestampResult{name, "skip", strPtr("no code lanes")})
			continue
		}
		if IsMissionCompleted(dir) {
			out = append(out, RestampResult{name, "skip", strPtr("terminal (archived) mission — nothing runs it")})
			continue
		}
		if !dryRun {
			UpdateMeta(dir, func(fresh *jsonx.Object) bool { fresh.Set("topology", TopoLanes); return true })
		}
		out = append(out, RestampResult{name, "restamped", nil})
	}
	return out
}

func splitNonEmpty(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
