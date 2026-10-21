package missionstate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// repairLanesPhase is _repair_lanes_phase: the #4758 canonical-state
// recovery of a wedged lanes.json (execution begun, lanes.json absent).
func repairLanesPhase(root, dir string, snap *status.Snapshot, meta *jsonx.Object, st *repairState) error {
	path := filepath.Join(dir, lanes.File)
	before := fileFingerprint(path)
	target := defaultTargetBranch
	if v := valueOf(meta, "target_branch"); pyjson.Truthy(v) {
		target = pyjson.Str(v)
	}
	action, err := rebuildLanesIfWedged(root, dir, st, target, snap, meta)
	if err != nil {
		return err
	}
	if action == LanesRebuiltAction {
		st.recordChange(root, path, before)
	}
	if action != "" {
		st.metaActions = append(st.metaActions, action)
	}
	return nil
}

func executionHasBegun(snap *status.Snapshot) bool {
	if snap == nil || snap.WorkPackages == nil {
		return false
	}
	for _, kv := range snap.WorkPackages.Pairs() {
		o, _ := kv.Value.(*jsonx.Object)
		lane := valueOf(o, "lane")
		if lane != nil && lane != "planned" {
			return true
		}
	}
	return false
}

func rebuildLanesIfWedged(root, dir string, st *repairState, target string, snap *status.Snapshot, meta *jsonx.Object) (string, error) {
	if fileExists(filepath.Join(dir, lanes.File)) {
		return "", nil
	}
	topo := migrate.TopologyFromMeta(meta, dir)
	if topo != migrate.TopoLanes {
		return "", nil
	}
	if !executionHasBegun(snap) {
		return "", nil
	}
	type wpIn struct {
		meta *jsonx.Object
		body string
	}
	fms := map[string]wpIn{}
	tasks := filepath.Join(dir, "tasks")
	entries, _ := filepath.Glob(filepath.Join(tasks, "WP*.md"))
	for _, p := range entries {
		id := wp.IDFromFilename(p)
		if id == "" {
			continue
		}
		w, err := wp.Read(p)
		if err != nil {
			continue
		}
		fms[id] = wpIn{w.Meta(), w.Doc.Body}
	}
	if len(fms) == 0 {
		return LanesRebuildSkippedNoWPFilesAction, nil
	}
	own := map[string]*lanes.Ownership{}
	for id, f := range fms {
		mode := f.meta.GetString("execution_mode")
		owned := wp.StringList(f.meta, "owned_files")
		if mode != "" && len(owned) > 0 {
			own[id] = &lanes.Ownership{ExecutionMode: mode, OwnedFiles: owned}
		}
	}
	if len(own) == 0 {
		return LanesRebuildSkippedNoOwnedFilesAction, nil
	}
	if fileExists(filepath.Join(dir, lanes.File)) {
		return "", nil
	}
	intent := map[string]map[string]bool{}
	for id, f := range fms {
		set := map[string]bool{}
		for _, p := range wp.StringList(f.meta, "create_intent") {
			set[p] = true
		}
		intent[id] = set
	}
	if errs := literalOwnedFileErrors(root, own, intent); len(errs) > 0 {
		return "", &RepairError{fmt.Sprintf("Cannot rebuild lanes.json for mission %s: literal-path owned_files entries match zero files in the repository (%s). Fix the paths in the WP frontmatter, then re-run 'spec-kitty doctor mission-state --fix'.",
			pyjson.ReprString(st.slug), strings.Join(errs, "; "))}
	}
	graph := map[string][]string{}
	bodies := map[string]string{}
	for id, f := range fms {
		graph[id] = wp.CoerceDependencies(f.meta)
		bodies[id] = f.body
	}
	var mid *string
	if st.missionID != nil && *st.missionID != "" {
		mid = st.missionID
	}
	m, err := lanes.Compute(lanes.ComputeInput{DependencyGraph: graph, Ownership: own, MissionSlug: st.slug, TargetBranch: target,
		WPBodies: bodies, MissionID: mid, Topology: topo, MissionBranch: meta.GetString("mission_branch")})
	if err != nil {
		return "", err
	}
	if sha, err := gitx.Run(root, "rev-parse", "--verify", "refs/heads/"+target); err == nil && strings.TrimSpace(sha) != "" {
		s := strings.TrimSpace(sha)
		m.PlanningCommitSHA = &s
	}
	if err := lanes.Write(dir, m); err != nil {
		return "", err
	}
	return LanesRebuiltAction, nil
}

// literalOwnedFileErrors is the compute_and_write_lanes literal-path
// re-validation: a glob-free owned_files entry must name an existing path
// unless the WP declares it in create_intent.
func literalOwnedFileErrors(root string, own map[string]*lanes.Ownership, intent map[string]map[string]bool) []string {
	var errs []string
	for id, o := range own {
		for _, pat := range o.OwnedFiles {
			if strings.ContainsAny(pat, "*?[") || intent[id][pat] {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, pat)); err != nil {
				errs = append(errs, fmt.Sprintf("%s: owned_files path '%s' is a literal file path that matches zero files in the repository", id, pat))
			}
		}
	}
	return errs
}
