package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// ── contract-version ──────────────────────────────────────────────────────

func contractVersionCmd() *cli.Command {
	return &cli.Command{
		Name:  "contract-version",
		Short: "Return the current API contract version.",
		Long:  "Return the current API contract version.\n\nPass --provider-version to check compatibility before running state-mutating commands.",
		Flags: []*cli.Flag{
			str("provider-version", "Caller's provider version; returns CONTRACT_VERSION_MISMATCH if below minimum", false),
			str("require-capability", "Require a supported Python command before performing work", false),
		},
		Run: runContractVersion,
	}
}

func runContractVersion(ctx *cli.Context) error {
	v := newVerb(ctx, "contract-version")
	if pv := optString(ctx, "provider-version"); pv != nil {
		ver, ok := orch.ParseVersion(*pv)
		if !ok {
			return v.fail("CONTRACT_VERSION_MISMATCH", fmt.Sprintf("Provider version %s is not a valid version string", pyjson.ReprString(*pv)),
				jsonx.NewObject("provider_version", *pv))
		}
		min, _ := orch.ParseVersion(orch.MinProviderVersion)
		if ver.Less(min) {
			return v.fail("CONTRACT_VERSION_MISMATCH",
				fmt.Sprintf("Provider version %s is below minimum %s", pyjson.ReprString(*pv), pyjson.ReprString(orch.MinProviderVersion)),
				jsonx.NewObject("provider_version", *pv, "min_supported_provider_version", orch.MinProviderVersion, "api_version", orch.ContractVersion))
		}
	}
	if rc := optString(ctx, "require-capability"); rc != nil {
		supported := false
		for _, c := range orch.Commands {
			if c == *rc {
				supported = true
			}
		}
		if !supported {
			return v.fail("UNSUPPORTED_CAPABILITY", "The Python delivery profile does not support this capability", jsonx.NewObject("capability", *rc))
		}
	}
	return v.ok(jsonx.NewObject(
		"api_version", orch.ContractVersion,
		"min_supported_provider_version", orch.MinProviderVersion,
		"delivery_profile", orch.DeliveryProfile(),
	))
}

// ── shared status / dependency helpers ────────────────────────────────────

// depGraph is build_dependency_graph: tasks/WP*.md (sorted) -> dependencies.
type depGraph struct {
	order []string
	deps  map[string][]string
}

func buildDepGraph(planningDir string) *depGraph {
	g := &depGraph{deps: map[string][]string{}}
	files, _ := filepath.Glob(filepath.Join(planningDir, "tasks", "WP*.md"))
	sort.Strings(files)
	for _, f := range files {
		base := filepath.Base(f)
		if len(base) < 4 || base[2] < '0' || base[2] > '9' || base[3] < '0' || base[3] > '9' {
			continue
		}
		id := base[:4]
		var deps []string
		if w, err := wp.Read(f); err == nil {
			if fid := strings.TrimSpace(w.String("work_package_id")); fid != "" {
				id = fid
			}
			deps = w.Dependencies()
		}
		if deps == nil {
			deps = []string{}
		}
		if _, seen := g.deps[id]; !seen {
			g.order = append(g.order, id)
		}
		g.deps[id] = deps
	}
	return g
}

func strList(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func reduceStatus(statusDir string) *status.Snapshot {
	snap, err := status.ReduceDir(statusDir)
	if err != nil || snap == nil {
		return &status.Snapshot{WorkPackages: jsonx.NewObject(), Summary: jsonx.NewObject()}
	}
	return snap
}

func wpLane(snap *status.Snapshot, id string) string {
	if st := snap.WP(id); st != nil {
		if v, ok := st.Get("lane"); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return string(status.Planned)
}

// unsatisfiedDeps is dependency_readiness_for_wp(..., provenance=snapshot).
func unsatisfiedDeps(snap *status.Snapshot, deps []string) []string {
	var out []string
	for _, dep := range deps {
		lane := status.ResolveLaneAlias(wpLane(snap, dep))
		switch lane {
		case string(status.Approved), string(status.Done):
			continue
		case string(status.Canceled):
			if st := snap.WP(dep); st != nil && st.GetString("reason_source") == "operator" {
				continue
			}
		}
		out = append(out, dep)
	}
	return out
}

// ── mission-state ─────────────────────────────────────────────────────────

func missionStateCmd() *cli.Command {
	return &cli.Command{
		Name:  "mission-state",
		Short: "Return the full state of a mission (all WPs, lanes, dependencies).",
		Flags: []*cli.Flag{missionFlag()},
		Run:   runMissionState,
	}
}

func runMissionState(ctx *cli.Context) error {
	v := newVerb(ctx, "mission-state")
	root := v.root()
	m, err := v.mission(root, ctx.String("mission"))
	if err != nil {
		return err
	}
	snap := reduceStatus(m.StatusDir)
	g := buildDepGraph(m.PrimaryDir)
	ids := map[string]bool{}
	if entries, err := os.ReadDir(filepath.Join(m.PrimaryDir, "tasks")); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				if mm := orch.WPIDPrefix.FindStringSubmatch(strings.TrimSuffix(e.Name(), ".md")); mm != nil {
					ids[mm[1]] = true
				}
			}
		}
	}
	for _, id := range g.order {
		ids[id] = true
	}
	for _, id := range snap.WorkPackages.Keys() {
		ids[id] = true
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	var wps []any
	for _, id := range sorted {
		var lane any = string(status.Planned)
		if st := snap.WP(id); st != nil {
			if l, ok := st.Get("lane"); ok {
				lane = l
			}
		}
		deps := g.deps[id]
		if deps == nil {
			deps = []string{}
		}
		wps = append(wps, jsonx.NewObject("wp_id", id, "lane", lane, "dependencies", strList(deps), "last_actor", nil))
	}
	if wps == nil {
		wps = []any{}
	}
	return v.ok(identity(m.StatusDir, "summary", summaryObject(snap), "work_packages", wps))
}

func summaryObject(snap *status.Snapshot) *jsonx.Object {
	if snap.Summary != nil && snap.Summary.Len() > 0 {
		return snap.Summary
	}
	o := jsonx.NewObject()
	for _, l := range status.DisplayLanes {
		o.Set(string(l), 0)
	}
	return o
}

// ── list-ready ────────────────────────────────────────────────────────────

func listReadyCmd() *cli.Command {
	return &cli.Command{
		Name:  "list-ready",
		Short: "List WPs that are ready to start (planned and all deps approved or done).",
		Flags: []*cli.Flag{missionFlag()},
		Run:   runListReady,
	}
}

func runListReady(ctx *cli.Context) error {
	v := newVerb(ctx, "list-ready")
	root := v.root()
	m, err := v.mission(root, ctx.String("mission"))
	if err != nil {
		return err
	}
	snap := reduceStatus(m.StatusDir)
	g := buildDepGraph(m.PrimaryDir)
	ready := []any{}
	for _, id := range g.order {
		var lane any = string(status.Planned)
		if st := snap.WP(id); st != nil {
			if l, ok := st.Get("lane"); ok {
				lane = l
			}
		}
		ls, _ := lane.(string)
		if status.ProgressBucket(status.Lane(status.ResolveLaneAlias(ls))) != "not_started" {
			continue
		}
		if len(unsatisfiedDeps(snap, g.deps[id])) > 0 {
			continue
		}
		ready = append(ready, jsonx.NewObject("wp_id", id, "lane", lane, "dependencies_satisfied", true))
	}
	return v.ok(identity(m.StatusDir, "ready_work_packages", ready))
}

// ── resolve-workspace ─────────────────────────────────────────────────────

func resolveWorkspaceCmd() *cli.Command {
	return &cli.Command{
		Name:  "resolve-workspace",
		Short: "Read-only: resolve a WP's lane workspace_path + prompt_path (+ lane fields).",
		Long: "Read-only: resolve a WP's lane workspace_path + prompt_path (+ lane fields).\n\n" +
			"Does NOT allocate/create/validate-clean/transition — the read-only companion\n" +
			"of ``start-implementation`` for a WP already past implementation (e.g. a\n" +
			"``for_review`` WP an external orchestrator wants to review on resume, where\n" +
			"calling start-implementation would wrongly re-transition it). Contract >= 1.2.0.",
		Flags: []*cli.Flag{missionFlag(), str("wp", helpWP, true)},
		Run:   runResolveWorkspace,
	}
}

// startWorkspace is _StartWorkspace.
type startWorkspace struct {
	path        string
	laneID      string // "" = not a lane WP
	laneBranch  string
	laneBaseRef string
}

func (s *startWorkspace) executionMode() string {
	if s != nil && s.laneID == lanes.PlanningLaneID {
		return "direct_repo"
	}
	return "worktree"
}

func (s *startWorkspace) addLaneFields(o *jsonx.Object) {
	if s.laneID != "" {
		o.Set("lane_id", s.laneID)
		o.Set("lane_branch", s.laneBranch)
		o.Set("lane_base_ref", s.laneBaseRef)
	}
}

// laneAssignment is _lane_assignment_or_legacy: the manifest + lane, or the
// legacy bare-path workspace.
func laneAssignment(m *orch.Mission, handle, wpID string) (*lanes.Manifest, *lanes.Lane, *startWorkspace) {
	manifest, err := lanes.Read(m.PrimaryDir)
	if err != nil || manifest == nil {
		return nil, nil, &startWorkspace{path: lanes.WorktreePath(m.Root, handle, wpID)}
	}
	l := manifest.LaneForWP(wpID)
	if l == nil {
		return nil, nil, &startWorkspace{path: lanes.WorktreePath(m.Root, handle, wpID)}
	}
	return manifest, l, nil
}

// existingWorkspace is _resolve_existing_workspace (read-only).
func existingWorkspace(m *orch.Mission, handle, wpID string) *startWorkspace {
	manifest, l, legacy := laneAssignment(m, handle, wpID)
	if legacy != nil {
		return legacy
	}
	base := lanes.LaneBaseRef(m.Root, m.Slug, manifest)
	if l.LaneID == lanes.PlanningLaneID {
		return &startWorkspace{path: m.Root, laneID: l.LaneID,
			laneBranch: lanes.SingleBranchWriteRef(m.Root, m.Slug, manifest.TargetBranch), laneBaseRef: base}
	}
	return &startWorkspace{path: lanes.WorktreePath(m.Root, handle, l.LaneID), laneID: l.LaneID,
		laneBranch: lanes.LaneBranchName(handle, l.LaneID, manifest.TargetBranch), laneBaseRef: base}
}

func runResolveWorkspace(ctx *cli.Context) error {
	v := newVerb(ctx, "resolve-workspace")
	root := v.root()
	handle, wpID := ctx.String("mission"), ctx.String("wp")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	wpPath := orch.ResolveWPFile(filepath.Join(m.PrimaryDir, "tasks"), wpID)
	if wpPath == "" {
		return v.wpNotFound(wpID, handle)
	}
	ws := existingWorkspace(m, handle, wpID)
	data := identity(m.StatusDir, "wp_id", wpID, "workspace_path", ws.path, "prompt_path", wpPath)
	ws.addLaneFields(data)
	return v.ok(data)
}

func (v *verb) wpNotFound(wpID, handle string) error {
	return v.fail("WP_NOT_FOUND", fmt.Sprintf("Work package '%s' not found in %s", wpID, handle), nil)
}
