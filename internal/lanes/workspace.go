package lanes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// WorkspaceContext is the persisted lane workspace context
// (.kittify/workspaces/<slug>-<lane>.json).
type WorkspaceContext struct {
	WPID         string
	MissionSlug  string
	WorktreePath string // relative to the repo root
	BranchName   string
	BaseBranch   string
	BaseCommit   *string
	Dependencies []string
	CreatedAt    string
	CreatedBy    string
	VCSBackend   string
	LaneID       string
	LaneWPIDs    []string
	CurrentWP    *string
	LaneTestEnv  *jsonx.Object
}

// ToObject renders WorkspaceContext.to_dict() (asdict order).
func (c *WorkspaceContext) ToObject() *jsonx.Object {
	var bc, cur, env any
	if c.BaseCommit != nil {
		bc = *c.BaseCommit
	}
	if c.CurrentWP != nil {
		cur = *c.CurrentWP
	}
	if c.LaneTestEnv != nil {
		env = c.LaneTestEnv
	}
	return jsonx.NewObject("wp_id", c.WPID, "mission_slug", c.MissionSlug, "worktree_path", c.WorktreePath,
		"branch_name", c.BranchName, "base_branch", c.BaseBranch, "base_commit", bc, "dependencies", strs(c.Dependencies),
		"created_at", c.CreatedAt, "created_by", c.CreatedBy, "vcs_backend", c.VCSBackend, "lane_id", c.LaneID,
		"lane_wp_ids", strs(c.LaneWPIDs), "current_wp", cur, "lane_test_env", env)
}

// WorkspacesDir is <root>/.kittify/workspaces.
func WorkspacesDir(root string) string { return project.Kittify(root, "workspaces") }

// SaveContext writes the context as json.dumps(indent=2) + "\n".
func SaveContext(root string, c *WorkspaceContext) (string, error) {
	p := filepath.Join(WorkspacesDir(root), WorktreeDirName(c.MissionSlug, c.LaneID)+".json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, atomicfile.WriteMode(p, []byte(jsonx.Dumps(c.ToObject(), jsonx.Pretty)+"\n"), 0o644)
}

func contextFromObject(o *jsonx.Object) *WorkspaceContext {
	c := &WorkspaceContext{
		WPID: o.GetString("wp_id"), MissionSlug: o.GetString("mission_slug"), WorktreePath: o.GetString("worktree_path"),
		BranchName: o.GetString("branch_name"), BaseBranch: o.GetString("base_branch"), CreatedAt: o.GetString("created_at"),
		CreatedBy: o.GetString("created_by"), VCSBackend: o.GetString("vcs_backend"), LaneID: o.GetString("lane_id"),
		Dependencies: listOf(o, "dependencies"), LaneWPIDs: listOf(o, "lane_wp_ids"), LaneTestEnv: o.GetObject("lane_test_env"),
	}
	if s := o.GetString("base_commit"); s != "" && s != "unknown" {
		c.BaseCommit = &s
	}
	if s := o.GetString("current_wp"); s != "" {
		c.CurrentWP = &s
	}
	return c
}

// ListContexts loads every valid context (sorted by file name).
func ListContexts(root string) []*WorkspaceContext {
	matches, _ := filepath.Glob(filepath.Join(WorkspacesDir(root), "*.json"))
	sort.Strings(matches)
	var out []*WorkspaceContext
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		o, err := jsonx.DecodeObject(b)
		if err != nil || o == nil || o.GetString("lane_id") == "" {
			continue
		}
		if v, _ := o.Get("lane_wp_ids"); func() bool { _, ok := v.([]any); return !ok }() {
			continue
		}
		out = append(out, contextFromObject(o))
	}
	return out
}

// FindContextForWP is find_context_for_wp (lane contexts expanded to all
// lane WPs, current_wp wins).
func FindContextForWP(root, missionSlug, wpID string) *WorkspaceContext {
	index := map[string]*WorkspaceContext{}
	for _, c := range ListContexts(root) {
		if c.MissionSlug != missionSlug {
			continue
		}
		for _, w := range c.LaneWPIDs {
			if _, ok := index[w]; !ok {
				index[w] = c
			}
		}
		if c.CurrentWP != nil {
			index[*c.CurrentWP] = c
		}
		if c.WPID != "" {
			if _, ok := index[c.WPID]; !ok {
				index[c.WPID] = c
			}
		}
	}
	return index[wpID]
}

// Workspace is ResolvedWorkspace.
type Workspace struct {
	MissionSlug    string
	WPID           string
	ExecutionMode  string // code_change | planning_artifact
	ResolutionKind string // repo_root | lane_workspace
	WorkspaceName  string
	WorktreePath   string
	BranchName     string // "" = None
	LaneID         string
	LaneWPIDs      []string
	Context        *WorkspaceContext
}

// RunsInCheckoutRoot reports a checkout-root resolution (repo_root, or an
// owned_checkout workspace).
func (w *Workspace) RunsInCheckoutRoot() bool {
	return w.ResolutionKind == "repo_root" || w.ResolutionKind == OwnedCheckoutKind
}

// StatusExecutionMode is the status-event execution_mode stamp.
func (w *Workspace) StatusExecutionMode() string {
	if w.RunsInCheckoutRoot() {
		return "direct_repo"
	}
	return "worktree"
}

// Exists reports a usable workspace (lane worktrees need a .git entry).
func (w *Workspace) Exists() bool {
	if w.RunsInCheckoutRoot() {
		st, err := os.Stat(w.WorktreePath)
		return err == nil && st.IsDir()
	}
	_, err := os.Stat(filepath.Join(w.WorktreePath, ".git"))
	return err == nil
}

// WPExecutionMode reads the WP's execution_mode (default code_change).
func WPExecutionMode(missionDir, wpID string) (string, error) {
	p, err := wp.Find(missionDir, wpID)
	if err != nil {
		return "", err
	}
	if p == "" {
		return "", fmt.Errorf("Work package %s not found in %s", wpID, wp.TasksDir(missionDir))
	}
	w, err := wp.Read(p)
	if err != nil {
		return "", err
	}
	if m := strings.TrimSpace(w.String("execution_mode")); m != "" {
		return m, nil
	}
	return "code_change", nil
}

// ResolveWorkspace is resolve_workspace_for_wp (non-owned arms):
// planning_artifact -> repo root (no branch); lane-planning -> repo root on
// the single-branch write ref; persisted context; code lane worktree.
func ResolveWorkspace(root, missionSlug, wpID string) (*Workspace, error) {
	primary := project.Specs(root, missionSlug)
	mode, err := WPExecutionMode(primary, wpID)
	if err != nil {
		return nil, err
	}
	planningName := missionSlug + "-" + PlanningLaneID
	manifest, merr := Read(primary)
	if mode == "planning_artifact" {
		w := &Workspace{MissionSlug: missionSlug, WPID: wpID, ExecutionMode: mode, ResolutionKind: "repo_root",
			WorkspaceName: planningName, WorktreePath: root, LaneID: PlanningLaneID, LaneWPIDs: []string{}}
		if merr == nil && manifest != nil {
			if l := manifest.LaneForWP(wpID); l != nil {
				w.LaneWPIDs = append([]string{}, l.WPIDs...)
			}
		}
		return w, nil
	}
	if merr == nil && manifest != nil {
		if l := manifest.LaneForWP(wpID); l != nil && l.LaneID == PlanningLaneID {
			return &Workspace{MissionSlug: missionSlug, WPID: wpID, ExecutionMode: mode, ResolutionKind: "repo_root",
				WorkspaceName: planningName, WorktreePath: root, BranchName: SingleBranchWriteRef(root, missionSlug, manifest.TargetBranch),
				LaneID: PlanningLaneID, LaneWPIDs: append([]string{}, l.WPIDs...)}, nil
		}
	}
	if c := FindContextForWP(root, missionSlug, wpID); c != nil {
		p := filepath.Join(root, c.WorktreePath)
		return &Workspace{MissionSlug: missionSlug, WPID: wpID, ExecutionMode: mode, ResolutionKind: "lane_workspace",
			WorkspaceName: filepath.Base(p), WorktreePath: p, BranchName: c.BranchName, LaneID: c.LaneID,
			LaneWPIDs: append([]string{}, c.LaneWPIDs...), Context: c}, nil
	}
	if merr != nil {
		return nil, merr
	}
	if manifest == nil {
		return nil, &MissingError{Dir: primary}
	}
	l := manifest.LaneForWP(wpID)
	if l == nil {
		return nil, fmt.Errorf("%s resolved to execution_mode=%s but is not assigned to any lane in %s", wpID, mission.PyRepr(mode), primary)
	}
	return &Workspace{MissionSlug: missionSlug, WPID: wpID, ExecutionMode: mode, ResolutionKind: "lane_workspace",
		WorkspaceName: WorktreeDirName(missionSlug, l.LaneID), WorktreePath: WorktreePath(root, missionSlug, l.LaneID),
		BranchName: LaneBranchName(missionSlug, l.LaneID, manifest.TargetBranch), LaneID: l.LaneID,
		LaneWPIDs: append([]string{}, l.WPIDs...)}, nil
}

// MissingError is MissingLanesError.
type MissingError struct{ Dir string }

func (e *MissingError) Error() string {
	return fmt.Sprintf("lanes.json not found in %s", e.Dir)
}

// SingleBranchWriteRef is resolve_single_branch_write_ref: meta.mission_branch
// for a stored single_branch mission, else the manifest target branch.
func SingleBranchWriteRef(root, missionSlug, target string) string {
	meta := mission.ReadMetaOrEmpty(project.Specs(root, missionSlug))
	if meta.GetString("topology") == "single_branch" {
		if b := strings.TrimSpace(meta.GetString("mission_branch")); b != "" {
			return b
		}
	}
	return target
}
