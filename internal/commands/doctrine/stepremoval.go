package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// This file ports `charter activate mission-type`'s FR-008 in-flight
// step-removal warnings (_emit_step_removal_warnings +
// specify_cli.charter_activate) and its FR-001 empty-sequence preflight
// (_validate_mission_type_activatable).

// missionTypeRosterCache memoises the strict layered roster for one
// invocation (upstream's functools.cache).
type missionTypeRosterCache struct {
	root   string
	roster map[string]*dct.MissionType
	err    error
	loaded bool
}

func (c *missionTypeRosterCache) get() (map[string]*dct.MissionType, error) {
	if !c.loaded {
		c.roster, c.err = dct.LayeredMissionTypes(c.root)
		c.loaded = true
	}
	return c.roster, c.err
}

// inflightLanes is charter_activate._INFLIGHT_LANES.
var inflightLanes = map[string]bool{"in_progress": true, "for_review": true, "in_review": true, "claimed": true}

type affectedWP struct{ slug, wp, lane string }

// stepRemovalWarnings returns the console lines (rich markup) of the
// in-flight step-removal warnings for activating mission type id, or the
// ValueError the resolution raises.
func stepRemovalWarnings(root, id string, rc *missionTypeRosterCache) ([]string, error) {
	// current: resolve_mission_type_context(...).action_sequence; an
	// unregistered or unresolvable type is "no previous state".
	var current []string
	registered, _ := dct.ActivatedMissionTypes(root)
	isRegistered := false
	for _, r := range registered {
		if r == id {
			isRegistered = true
		}
	}
	if isRegistered {
		roster, err := rc.get()
		if err != nil {
			return nil, err
		}
		if mt := roster[id]; mt != nil {
			current = dct.ExtendsFallbackSequence(mt, roster)
			if len(current) == 0 {
				return nil, fmt.Errorf("%s", dct.EmptyActionSequenceMessage(id, dct.MissionTypeSourceLayer(root, id)))
			}
		}
	}
	roster, err := rc.get()
	if err != nil {
		return nil, err
	}
	incoming := map[string]bool{}
	if mt := roster[id]; mt != nil {
		for _, s := range mt.ActionSequence {
			incoming[s] = true
		}
	}
	var removed []string
	for _, s := range current {
		if !incoming[s] {
			removed = append(removed, s)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	affected := collectInflight(filepath.Join(root, project.SpecsDir))
	if len(affected) == 0 {
		return nil, nil
	}
	var lines []string
	for _, step := range removed {
		lines = append(lines, fmt.Sprintf("[yellow]⚠ Step '%s' removed by mission-type override.[/yellow]", step), "  Affected missions:")
		for _, a := range affected {
			lines = append(lines, fmt.Sprintf("  - %s (%s, currently in lane '%s')", a.slug, a.wp, a.lane))
		}
	}
	return lines, nil
}

// collectInflight is _collect_inflight_missions.
func collectInflight(specs string) []affectedWP {
	ents, err := os.ReadDir(specs)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []affectedWP
	for _, n := range names {
		dir := filepath.Join(specs, n)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, status.EventsFile)); err != nil {
			continue
		}
		slug := n
		meta := mission.ReadMetaOrEmpty(dir)
		for _, k := range []string{"mission_slug", "feature_slug"} {
			if v, ok := meta.Get(k); ok && pyTruthyVal(v) {
				slug = fmt.Sprint(v)
				break
			}
		}
		snap, err := status.ReduceDir(dir)
		if err != nil || snap == nil || snap.WorkPackages == nil {
			continue
		}
		for _, kv := range snap.WorkPackages.Pairs() {
			lane := ""
			if st, ok := kv.Value.(interface{ GetString(string) string }); ok {
				lane = st.GetString("lane")
			}
			if inflightLanes[lane] {
				out = append(out, affectedWP{slug, kv.Key, lane})
			}
		}
	}
	return out
}

func pyTruthyVal(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	}
	return true
}

// validateActivatableMissionType is validate_activatable_mission_type.
func validateActivatableMissionType(root, id string, rc *missionTypeRosterCache) error {
	roster, err := rc.get()
	if err != nil {
		return err
	}
	mt := roster[id]
	if mt == nil {
		return nil
	}
	if len(dct.ExtendsFallbackSequence(mt, roster)) == 0 {
		return fmt.Errorf("%s", dct.EmptyActionSequenceMessage(id, dct.MissionTypeSourceLayer(root, id)))
	}
	return nil
}
