package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// lifecycleLanes is each WP's WPMetadata.lane: the authored (legacy)
// frontmatter lane, "planned" when absent. The status snapshot only
// re-points the runtime fields (shell_pid, agent, assignee), never the lane.
func (f *finalizeRun) lifecycleLanes() map[string]string {
	out := map[string]string{}
	for id, m := range f.frontmatter {
		l := m.GetString("lane")
		if l == "" {
			l = "planned"
		}
		out[id] = l
	}
	return out
}

// projectLaneInputs is _project_lane_inputs + project_finalization_eligibility:
// canceled WPs (and edges to them) leave the lane inputs; executable work
// still depending on a canceled WP is refused (STALE_CANCELED_DEPENDENCIES).
func (f *finalizeRun) projectLaneInputs(man map[string]ownManifest) error {
	lanesNow := f.lifecycleLanes()
	known := sortedKeysObj(f.frontmatter)
	canceled := map[string]bool{}
	for _, id := range known {
		if lanesNow[id] == "canceled" {
			canceled[id] = true
		}
	}
	knownSet := map[string]bool{}
	for _, id := range known {
		knownSet[id] = true
	}
	type stale struct{ dep, can string }
	staleSet := map[stale]bool{}
	f.laneDeps = map[string][]string{}
	f.laneOrder = nil
	f.laneMan = map[string]ownManifest{}
	f.laneBodies = map[string]string{}
	for _, id := range known {
		if canceled[id] {
			continue
		}
		set := map[string]bool{}
		for _, d := range f.deps[id] {
			if knownSet[d] && canceled[d] {
				staleSet[stale{id, d}] = true
			}
			if !canceled[d] {
				set[d] = true
			}
		}
		f.laneDeps[id] = sortedSet(set)
		f.laneOrder = append(f.laneOrder, id)
		if m, ok := man[id]; ok {
			f.laneMan[id] = m
		}
		if b, ok := f.bodies[id]; ok {
			f.laneBodies[id] = b
		}
	}
	f.allCanceled = len(known) > 0 && len(canceled) == len(known)
	if len(staleSet) == 0 {
		return nil
	}
	var recs []stale
	for s := range staleSet {
		recs = append(recs, s)
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].dep != recs[j].dep {
			return recs[i].dep < recs[j].dep
		}
		return recs[i].can < recs[j].can
	})
	msg := "Cannot finalize execution lanes: eligible work depends on canceled work packages."
	if f.json {
		var list []any
		for _, r := range recs {
			list = append(list, jsonx.NewObject("dependent_wp_id", r.dep, "canceled_dependency_wp_id", r.can,
				"recovery", fmt.Sprintf("Remove the dependency or repoint %s to a non-canceled prerequisite.", r.dep)))
		}
		emitJSON(f.ctx.Stdout, jsonx.NewObject("error", msg, "error_code", "STALE_CANCELED_DEPENDENCIES", "stale_canceled_dependencies", list))
	} else {
		say(f.ctx.Stdout, "[red]Error:[/red] "+msg)
		for _, r := range recs {
			say(f.ctx.Stdout, fmt.Sprintf("  - %s depends on canceled %s: Remove the dependency or repoint %s to a non-canceled prerequisite.", r.dep, r.can, r.dep))
		}
	}
	return cli.Exit(1)
}

func unreadableFrozen(detail string) error {
	return lanes.NewMembershipFrozenError([]lanes.MembershipConflict{lanes.StatusUnreadableConflict(detail, "")})
}

// startedWPs is _read_started_wp_ids: the history-started WPs, fail-closed
// (an unreadable log or surface is the status_unreadable refusal).
func (f *finalizeRun) startedWPs() (map[string]bool, error) {
	s := coord.Resolve(f.root, f.slug)
	dir := s.StatusDir()
	var stream *status.Stream
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if s.CoordBranch == "" {
			return nil, unreadableFrozen(fmt.Sprintf("The status surface %s does not exist, so the status history cannot be read.", dir))
		}
		rel := "kitty-specs/" + filepath.Base(s.CoordDir) + "/" + status.EventsFile
		out, gerr := gitx.Run(f.root, "show", "refs/heads/"+s.CoordBranch+":"+rel)
		if gerr != nil {
			return nil, unreadableFrozen(fmt.Sprintf("The coordination branch %s has no committed %s, so the status history cannot be read.", mission.PyRepr(s.CoordBranch), rel))
		}
		var rows []*jsonx.Object
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			o, derr := jsonx.DecodeObject([]byte(line))
			if derr != nil {
				return nil, unreadableFrozen(derr.Error())
			}
			rows = append(rows, o)
		}
		stream, err = status.ParseRows(rows)
		if err != nil {
			return nil, unreadableFrozen(err.Error())
		}
	} else {
		if _, err := os.Stat(filepath.Join(dir, status.EventsFile)); err != nil {
			return map[string]bool{}, nil
		}
		stream, err = status.ReadStream(dir, "")
		if err != nil {
			return nil, unreadableFrozen(err.Error())
		}
	}
	var pairs [][2]string
	for _, e := range stream.Transitions {
		pairs = append(pairs, [2]string{e.WPID, string(e.To)})
	}
	return lanes.StartedWPIDs(pairs), nil
}

// frozenPreflight is _preflight_frozen_lane_membership (#5573): before the
// first status write, refuse a re-finalize that would move started work.
func (f *finalizeRun) frozenPreflight() error {
	meta := f.metaObj()
	if coord.TopologyFromMeta(meta, f.dir) == coord.SingleBranch {
		return nil
	}
	previous, _ := lanes.Read(f.dir)
	if previous == nil {
		return nil
	}
	started, err := f.startedWPs()
	if err != nil {
		return err
	}
	out, gerr := gitx.Run(f.root, "for-each-ref", "--format=%(refname)", "refs/spec-kitty/lane-tip/")
	if gerr != nil {
		return unreadableFrozen("git could not list the recorded lane work tips in " + f.root + ": " + gerr.Error())
	}
	tipped := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "refs/spec-kitty/lane-tip/") {
			tipped[strings.TrimPrefix(l, "refs/spec-kitty/lane-tip/")] = true
		}
	}
	present := map[string]bool{}
	eligible := map[string]bool{}
	for id := range f.frontmatter {
		present[id] = true
	}
	for _, id := range f.laneOrder {
		eligible[id] = true
	}
	frozen := lanes.BuildFrozenMembership(previous, started, tipped, present, eligible)
	if frozen.IsEmpty() {
		return nil
	}
	f.frozen = frozen
	if _, err := f.computeLanes(f.laneMan, previous, nil); err != nil {
		switch err.(type) {
		case *lanes.MembershipFrozenError, *lanes.DependencyCycleError:
			return err
		}
	}
	return nil
}
