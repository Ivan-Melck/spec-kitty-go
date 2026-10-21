package doctrine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// actionNames is mission_runtime.ACTION_NAMES.
var actionNames = []string{"specify", "plan", "analyze", "tasks", "tasks_outline", "tasks_packages", "tasks_finalize", "implement", "review", "accept", "status"}

var missionLevelActions = map[string]bool{"specify": true, "plan": true, "analyze": true, "tasks": true, "tasks_outline": true,
	"tasks_packages": true, "tasks_finalize": true, "accept": true, "status": true}

var planningAuthoringActions = map[string]bool{"specify": true, "plan": true, "tasks": true, "tasks_outline": true, "tasks_packages": true, "tasks_finalize": true}

func init() {
	app.Register(func(root *cli.Command) {
		agent := app.Group(root, "agent", "Commands for AI agents to execute spec-kitty mission actions programmatically")
		g := app.Group(agent, "context", "Agent context management commands")
		g.InvokeWithoutCommand = true
		g.Run = func(ctx *cli.Context) error { // no_args_is_help: help, exit 2
			cli.WriteHelp(ctx.Stdout, ctx.Cmd)
			return cli.Exit(2)
		}
		g.Add(&cli.Command{
			Name:  "resolve",
			Short: "Resolve canonical feature/work-package/action context for prompt execution.",
			Flags: []*cli.Flag{
				{Name: "action", Kind: cli.String, Required: true, Help: "Action to resolve context for (" + strings.Join(actionNames, ", ") + ")"},
				{Name: "mission", Kind: cli.String, Help: "Mission slug (e.g., '020-my-mission')"},
				{Name: "wp-id", Kind: cli.String, Help: "Work package ID (e.g., WP01)"},
				{Name: "agent", Kind: cli.String, Help: "Agent name for exact command rendering"},
				{Name: "json", Kind: cli.Bool, Help: "Output results as JSON"},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: "Run against an owned checkout: a linked checkout that owns this mission. Refuses the repository root checkout, lane worktrees and coordination worktrees."},
			},
			Run: runContextResolve,
		})
	})
}

// actionContextError is mission_runtime.ActionContextError.
type actionContextError struct{ code, msg string }

func (e *actionContextError) Error() string { return e.msg }

func ctxErr(code, format string, a ...any) error {
	return &actionContextError{code, fmt.Sprintf(format, a...)}
}

func tasksCommands(slug string) *jsonx.Object {
	return jsonx.NewObject(
		"check_prerequisites", "spec-kitty agent mission check-prerequisites --json --paths-only --include-tasks --mission "+slug,
		"finalize_tasks", "spec-kitty agent mission finalize-tasks --mission "+slug+" --json")
}

func wpCommands(action, wpID, slug, agent string) *jsonx.Object {
	verb := "review"
	if action == "implement" {
		verb = "implement"
	}
	wf := "spec-kitty agent action " + verb + " " + wpID
	if agent != "" {
		wf += " --agent " + agent
	}
	o := jsonx.NewObject("workflow", wf)
	if action != "implement" {
		o.Set("approve", fmt.Sprintf(`spec-kitty agent tasks move-task %s --to approved --mission %s --note "Review passed: <summary>"`, wpID, slug))
		o.Set("reject", fmt.Sprintf("spec-kitty agent tasks move-task %s --to planned --review-feedback-file <feedback-file> --mission %s", wpID, slug))
	}
	return o
}

// execContext is the flat MissionExecutionContext.to_dict() substrate.
type execContext struct {
	action, slug, featureDir, targetBranch string
	wpID, wpFile, lane, laneID, branch     *string
	execMode, resolutionKind, workspace    *string
	deps                                   []string
	commands                               *jsonx.Object
}

func optS(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func sp(s string) *string { return &s }

func (c *execContext) object() *jsonx.Object {
	return jsonx.NewObject("success", true, "action", c.action, "mission_slug", c.slug, "feature_dir", c.featureDir,
		"target_branch", c.targetBranch, "detection_method", "explicit", "wp_id", optS(c.wpID), "wp_file", optS(c.wpFile),
		"lane", optS(c.lane), "lane_id", optS(c.laneID), "branch_name", optS(c.branch), "execution_mode", optS(c.execMode),
		"resolution_kind", optS(c.resolutionKind), "dependencies", strs(c.deps), "resolved_base", nil, "auto_merge", false,
		"workspace_path", optS(c.workspace), "commands", c.commands, "warnings", []any{}, "mission_dir", c.featureDir)
}

func runContextResolve(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	c, err := resolveActionContext(ctx)
	if err != nil {
		var ae *actionContextError
		code, msg := "UNKNOWN", err.Error()
		if errors.As(err, &ae) {
			code = ae.code
		}
		if jsonMode {
			jsonOut(ctx.Stdout, jsonx.NewObject("success", false, "error_code", code, "error", msg), jsonx.Pretty)
		} else {
			cprintRaw(ctx.Stdout, "Error: "+msg)
		}
		return cli.Exit(1)
	}
	if jsonMode {
		jsonOut(ctx.Stdout, c.object(), jsonx.Pretty)
		return nil
	}
	w := ctx.Stdout
	cprint(w, "[green]✓[/green] Resolved %s context", c.action)
	cprintRaw(w, fmt.Sprintf("  Mission: %s (explicit)", c.slug))
	cprintRaw(w, "  Feature dir: "+c.featureDir)
	cprintRaw(w, "  Target branch: "+c.targetBranch)
	if c.wpID != nil {
		cprintRaw(w, fmt.Sprintf("  Work package: %s (%s)", *c.wpID, optStr(c.lane)))
	}
	if c.workspace != nil && *c.workspace != "" {
		cprintRaw(w, "  Workspace: "+*c.workspace)
	}
	for _, kv := range c.commands.Pairs() {
		cprintRaw(w, fmt.Sprintf("  %s: %v", kv.Key, kv.Value))
	}
	return nil
}

func optStr(p *string) string {
	if p == nil {
		return "None"
	}
	return *p
}

func resolveActionContext(ctx *cli.Context) (*execContext, error) {
	root := project.Locate("")
	if root == "" {
		return nil, ctxErr("PROJECT_ROOT_UNRESOLVED", "Could not locate project root.")
	}
	root = project.MainRepoRoot(root)
	action := ctx.String("action")
	valid := false
	for _, a := range actionNames {
		valid = valid || a == action
	}
	if !valid {
		return nil, ctxErr("INVALID_ACTION", "Invalid action '%s'. Expected one of: %s.", action, strings.Join(actionNames, ", "))
	}
	handle := strings.TrimSpace(ctx.String("mission"))
	if handle == "" {
		return nil, ctxErr("MISSING_MISSION", "--mission <slug> is required")
	}
	res, err := mission.ResolveIdentity(root, handle)
	if err != nil {
		var amb *mission.AmbiguousError
		if errors.As(err, &amb) {
			return nil, ctxErr("MISSION_AMBIGUOUS_SELECTOR", "%s", err.Error())
		}
		return nil, ctxErr("MISSION_NOT_FOUND", "%s", err.Error())
	}
	slug := res.Slug
	surf := coord.Resolve(root, slug)
	readDir := surf.StatusDir()
	target := surf.TargetBranch()
	if target == "" {
		target = "main"
	}
	c := &execContext{action: action, slug: slug, featureDir: readDir, targetBranch: target, deps: []string{}}
	if missionLevelActions[action] {
		if planningAuthoringActions[action] {
			c.featureDir = surf.PrimaryDir
		}
		c.commands = tasksCommands(slug)
		return c, nil
	}
	wpID := resolveWPID(action, surf, ctx.String("wp-id"))
	if wpID == "" {
		return nil, ctxErr("WORK_PACKAGE_UNRESOLVED", "No work package available for action '%s' in feature %s.", action, slug)
	}
	tasksRoot := filepath.Join(surf.PrimaryDir, "tasks")
	if _, err := os.Stat(tasksRoot); err != nil {
		return nil, ctxErr("WORK_PACKAGE_UNRESOLVED", "Mission '%s' has no tasks directory at %s.", slug, tasksRoot)
	}
	pat := regexp.MustCompile(`^` + regexp.QuoteMeta(wpID) + `(?:[-_.]|\.md$)`)
	var matches []string
	ents, _ := os.ReadDir(tasksRoot)
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".md") || strings.ToLower(n) == "readme.md" {
			continue
		}
		if pat.MatchString(n) {
			matches = append(matches, filepath.Join(tasksRoot, n))
		}
	}
	if len(matches) == 0 {
		return nil, ctxErr("WORK_PACKAGE_UNRESOLVED", "Work package '%s' not found under kitty-specs/%s/tasks (resolved: %s under %s).", wpID, slug, tasksRoot, root)
	}
	if len(matches) > 1 {
		var rel []string
		for _, m := range matches {
			r, _ := filepath.Rel(root, m)
			rel = append(rel, r)
		}
		return nil, ctxErr("WORK_PACKAGE_UNRESOLVED", "Multiple files matched '%s'. Refine the ID or clean duplicates:\n%s", wpID, strings.Join(rel, "\n"))
	}
	w, err := wp.Read(matches[0])
	if err != nil {
		return nil, ctxErr("WORK_PACKAGE_UNRESOLVED", "%s", err.Error())
	}
	c.wpID, c.wpFile = sp(wpID), sp(matches[0])
	c.deps = wp.CoerceDependencies(w.Meta())
	if c.deps == nil {
		c.deps = []string{}
	}
	lane := string(wpLane(readDir, wpID))
	c.lane = sp(lane)
	if err := resolveWorkspace(c, root, surf, w, wpID); err != nil {
		return nil, ctxErr("WORK_PACKAGE_UNRESOLVED", "%s", err.Error())
	}
	c.commands = wpCommands(action, wpID, slug, ctx.String("agent"))
	return c, nil
}

// wpLane is _resolve_wp_lane: the reduced lane (planned when absent).
func wpLane(dir, wpID string) status.Lane {
	snap, err := status.ReduceDir(dir)
	if err != nil || snap == nil {
		return status.Planned
	}
	l := snap.Lane(wpID)
	if l == "" || l == status.Uninitialized {
		return status.Planned
	}
	if r, ok := status.ResolveLane(string(l)); ok {
		return r
	}
	return l
}

var wpFileRe = regexp.MustCompile(`^(WP\d+)`)

func resolveWPID(action string, surf *coord.Surface, explicit string) string {
	if explicit != "" {
		id, _, _ := strings.Cut(strings.ToUpper(explicit), "-")
		return id
	}
	// Upstream scans <feature_dir>/tasks where feature_dir is the status read
	// dir: under coordination topology that husk has no tasks/, so
	// auto-detection finds nothing (parity with 4.0.0rc8).
	dir := surf.StatusDir()
	files, _ := filepath.Glob(filepath.Join(dir, "tasks", "WP*.md"))
	sort.Strings(files)
	if action == "implement" {
		for _, want := range []status.Lane{status.Planned, status.InProgress} {
			for _, f := range files {
				m := wpFileRe.FindStringSubmatch(strings.TrimSuffix(filepath.Base(f), ".md"))
				if m == nil {
					continue
				}
				if wpLane(dir, m[1]) == want {
					return m[1]
				}
			}
		}
		return ""
	}
	// review: first for_review, else a review-claimed WP.
	var cands []string
	for _, f := range files {
		if w, err := wp.Read(f); err == nil {
			if id := w.String("work_package_id"); id != "" {
				cands = append(cands, id)
			}
		}
	}
	snap, _ := status.ReduceDir(dir)
	laneOf := func(id string) status.Lane {
		if snap == nil {
			return status.Planned
		}
		return snap.Lane(id)
	}
	for _, id := range cands {
		if laneOf(id) == status.ForReview {
			return id
		}
	}
	st, err := status.ReadStream(dir, "")
	if err != nil || st == nil {
		return ""
	}
	for _, id := range cands {
		l := laneOf(id)
		if l != status.InProgress && l != status.InReview {
			continue
		}
		for i := len(st.Transitions) - 1; i >= 0; i-- {
			ev := st.Transitions[i]
			if ev.WPID != id {
				continue
			}
			if ev.To == status.InReview || (ev.To == status.InProgress && ev.ReviewRef != nil && *ev.ReviewRef == "action-review-claim") {
				return id
			}
			break
		}
	}
	return ""
}

// resolveWorkspace is resolve_workspace_for_wp (non-owned arms).
func resolveWorkspace(c *execContext, root string, surf *coord.Surface, w *wp.WP, wpID string) error {
	mode := w.String("execution_mode")
	if mode == "" {
		mode = "code_change"
	}
	c.execMode = sp(mode)
	slug := surf.Slug
	man, _ := lanes.Read(surf.PrimaryDir)
	if man == nil && surf.Routed() {
		man, _ = lanes.Read(surf.CoordDir)
	}
	if mode == "planning_artifact" {
		c.resolutionKind, c.workspace, c.laneID = sp("repo_root"), sp(root), sp(lanes.PlanningLaneID)
		return nil
	}
	if man != nil {
		if l := man.LaneForWP(wpID); l != nil && l.LaneID == lanes.PlanningLaneID {
			branch := man.TargetBranch
			if coord.TopologyFromMeta(surf.Meta, surf.PrimaryDir) == "single_branch" {
				if mb := surf.Meta.GetString("mission_branch"); mb != "" {
					branch = mb
				}
			}
			c.resolutionKind, c.workspace, c.laneID, c.branch = sp("repo_root"), sp(root), sp(lanes.PlanningLaneID), sp(branch)
			return nil
		}
	}
	if wc := findWorkspaceContext(root, slug, wpID); wc != nil {
		p := filepath.Join(root, wc.GetString("worktree_path"))
		c.resolutionKind, c.workspace = sp("lane_workspace"), sp(p)
		c.branch, c.laneID = sp(wc.GetString("branch_name")), sp(wc.GetString("lane_id"))
		return nil
	}
	if man == nil {
		return fmt.Errorf("lanes.json not found in %s", surf.PrimaryDir)
	}
	l := man.LaneForWP(wpID)
	if l == nil {
		return fmt.Errorf("%s resolved to execution_mode=%s but is not assigned to any lane in %s", wpID, pyStrRepr(mode), surf.PrimaryDir)
	}
	c.resolutionKind = sp("lane_workspace")
	c.workspace = sp(lanes.WorktreePath(root, slug, l.LaneID))
	c.branch = sp(lanes.LaneBranchName(slug, l.LaneID, man.TargetBranch))
	c.laneID = sp(l.LaneID)
	return nil
}

// findWorkspaceContext is find_context_for_wp over .kittify/workspaces/*.json.
func findWorkspaceContext(root, slug, wpID string) *jsonx.Object {
	files, _ := filepath.Glob(filepath.Join(root, ".kittify", "workspaces", "*.json"))
	sort.Strings(files)
	index := map[string]*jsonx.Object{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		o, err := jsonx.DecodeObject(b)
		if err != nil || o == nil || o.GetString("mission_slug") != slug {
			continue
		}
		for _, x := range o.GetList("lane_wp_ids") {
			if s, ok := x.(string); ok {
				if _, seen := index[s]; !seen {
					index[s] = o
				}
			}
		}
		if cw := o.GetString("current_wp"); cw != "" {
			index[cw] = o
		}
		if id := o.GetString("wp_id"); id != "" {
			if _, seen := index[id]; !seen {
				index[id] = o
			}
		}
	}
	return index[wpID]
}
