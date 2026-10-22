package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/lifecycle"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/nextrt"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const closeLong = "Close a mission. Wraps FR-016 lifecycle teardown.\n\n" +
	"Without ``--discard``: fail-closed precondition (FR-004/FR-005, #4765) —\nrefuses (non-zero exit, no writes) unless the mission has a recorded merge\nbaseline (``is_mission_merged``). An all-terminal-but-unmerged mission\n(e.g. every work package cancelled) still refuses here; use ``--discard``\nto abandon it. Once merged: runs the merge-completion teardown — persists\nthe mission retrospective to its durable home and tears down the\ncoordination worktree. Idempotent after a successful ``spec-kitty consolidate``\n(which already ran the same teardown); useful when the teardown was\nskipped (e.g. the legacy plain-git/GitHub merge path) or interrupted.\nNOTE: on a merged mission without a retrospective, this generates one\n(``kitty-specs/<slug>/retrospective.yaml``) plus a ``RetrospectiveCaptured``\nevent and commits both — it is not a pure no-op in that case. Tolerates a\nmission left with an orphaned ``coordination_branch`` marker (FR-013,\n#2745) — no traceback, the mission slug is rendered once.\n\n" +
	"With ``--discard``: abandon the mission mid-flight. Deletes the\ncoordination branch and every lane branch named in\n``lanes.json``, then tears down the coordination worktree and the\noperator-visible lane worktrees. Requires confirmation unless\n``--force`` is also passed. The coordination + lane branches are\ndeleted with ``git branch -D`` (force-delete) because mid-flight\nabandonment by definition leaves uncommitted or unmerged work.\nA deleted Mission branch whose commits exist on no other branch keeps its\ntip under ``refs/spec-kitty/discarded/<branch>``; remove it with\n``git update-ref -d refs/spec-kitty/discarded/<branch>`` once it is no\nlonger needed.\n\n" +
	"Implements FR-016 from\n``kitty-specs/mission-coordination-branch-atomic-event-log-01KSPTVW``."

const followUpLong = "Record a follow-up commit or PR against a mission (FR-001).\n\nExactly one of ``--commit <40-hex>`` / ``--pr <int>`` must be supplied.\nAppends a ``FollowUpRecorded`` lifecycle event attributed to ``mission_id``.\nFail-closed (#1926): only valid once the mission has reached completion\n(merged, or all WPs terminal) — a follow-up against a not-yet-completed\nmission exits non-zero with a structured error and writes no event.\nIdempotent on its dedup key ``(mission_id, commit_sha | pr_number)`` —\nre-recording the same reference is a successful no-op."

const reopenLong = "Re-open a merged/closed mission, returning it to an actionable state (FR-002).\n\nAppends a ``MissionReopened`` lifecycle event (the authority for\nactionability — ``derive_mission_lifecycle`` reports the ``reopened``\nsurface_state) and clears the ``merged_*`` markers from ``meta.json``. Does\nNOT mutate WP lanes — the operator repositions WPs explicitly afterwards.\n\nFail-closed (NFR-004): the mission is unrecoverable when ``meta.json`` is\nabsent/corrupt OR the mission branch resolves in neither the local repo nor\nany configured remote. A missing worktree directory alone is recoverable. On\nunrecoverable input the command exits non-zero with a remediation hint and\nwrites no event / no metadata change."

func missionLeaves() []*cli.Command {
	return []*cli.Command{
		{
			Name: "run", Short: "Start (or attach to) a runtime for a project-authored custom mission definition.",
			Args: []*cli.Arg{{Name: "mission_key", Metavar: "{mission_key}", Required: true, ErrorHint: "mission_key", Help: "The reusable custom mission key."}},
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Tracked mission slug."},
				{Name: "json", Kind: cli.Bool, Negatable: true, Default: false, Help: "Emit JSON envelope to stdout instead of a rich panel."},
			},
			Run: runMissionRun,
		},
		{
			Name: "close", Short: "Close a mission. Wraps FR-016 lifecycle teardown.", Long: closeLong,
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (auto-detected from cwd if omitted)"},
				{Name: "discard", Kind: cli.Bool, Help: "Discard the mission mid-flight: delete the coordination branch + all lane branches and tear down all worktrees. Without --discard, requires that the mission has already been merged (no-op cleanup otherwise)."},
				{Name: "force", Kind: cli.Bool, Help: "Skip the confirmation prompt when --discard is set."},
				{Name: "json", Kind: cli.Bool, Help: "Emit a JSON envelope instead of a rich panel."},
			},
			Run: runMissionClose,
		},
		{
			Name: "follow-up", Short: "Record a follow-up commit or PR against a mission (FR-001).", Long: followUpLong,
			Args: []*cli.Arg{{Name: "handle", Metavar: "{handle}", Required: true, ErrorHint: "handle", Help: "Mission handle: mission_id (ULID), mid8, or slug."}},
			Flags: []*cli.Flag{
				{Name: "commit", Kind: cli.String, Metavar: "<str>", Help: "40-hex commit SHA of the follow-up."},
				{Name: "pr", Kind: cli.Int, Metavar: "<int>", Help: "Pull-request number of the follow-up."},
				{Name: "json", Kind: cli.Bool, Help: "Emit a JSON envelope instead of a rich panel."},
			},
			Run: runMissionFollowUp,
		},
		{
			Name: "reopen", Short: "Re-open a merged/closed mission, returning it to an actionable state (FR-002).", Long: reopenLong,
			Args: []*cli.Arg{{Name: "handle", Metavar: "{handle}", Required: true, ErrorHint: "handle", Help: "Mission handle: mission_id (ULID), mid8, or slug."}},
			Flags: []*cli.Flag{
				{Name: "reason", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Why the mission is being re-opened (required, audited)."},
				{Name: "json", Kind: cli.Bool, Help: "Emit a JSON envelope instead of a rich panel."},
			},
			Run: runMissionReopen,
		},
	}
}

func init() {
	app.Register(func(root *cli.Command) {
		for _, name := range []string{"mission", "mission-type"} {
			g := app.Group(root, name, "Inspect mission types for this project.")
			for _, c := range missionLeaves() {
				if g.Find(c.Name) == nil {
					g.Add(c)
				}
			}
		}
	})
}

func missionError(ctx *cli.Context, message, code string, jsonMode bool) {
	if jsonMode {
		emitJSON(ctx, jsonError(code, rich.Strip(message)))
	} else {
		rich.Print(ctx.Stdout, message)
	}
}

// ---------------------------------------------------------------------------
// mission run
// ---------------------------------------------------------------------------

func runMissionRun(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		missionError(ctx, "Error: Not in a spec-kitty project", "not_in_project", jsonMode)
		return cli.Exit(1)
	}
	key := ctx.Arg("mission_key")
	slug := ctx.String("mission")
	if st, err := os.Stat(project.Specs(root, slug)); err != nil || !st.IsDir() {
		if s, err := mission.ResolveHandle(root, slug); err == nil {
			slug = s
		}
	}
	env, code := nextrt.RunCustomMission(root, key, slug)
	if jsonMode {
		if code != 0 {
			p := jsonError(env.GetString("error_code"), env.GetString("message"))
			for _, k := range []string{"details", "warnings"} {
				if v, ok := env.Get(k); ok {
					p.Set(k, v)
				}
			}
			emitJSON(ctx, p)
		} else {
			emitJSON(ctx, env)
		}
		return cli.Exit(code)
	}
	if code == 0 {
		body := fmt.Sprintf("mission_key:  %s\nmission_slug: %s\n", env.GetString("mission_key"), env.GetString("mission_slug"))
		if id := env.GetString("mission_id"); id != "" {
			body += fmt.Sprintf("mission_id:   %s\n", id)
		}
		body += fmt.Sprintf("feature_dir:  %s\nrun_dir:      %s", env.GetString("feature_dir"), env.GetString("run_dir"))
		rich.Panel(ctx.Stdout, body+warningLines(env), "Mission Run Started")
		return nil
	}
	body := env.GetString("message")
	if d := env.GetObject("details"); d != nil {
		for _, k := range d.Keys() {
			v, _ := d.Get(k)
			body += fmt.Sprintf("\n  %s: %s", k, pyStr(v))
		}
	}
	rich.Panel(ctx.Stdout, body+warningLines(env), env.GetString("error_code"))
	return cli.Exit(code)
}

func pyStr(v any) string {
	switch t := v.(type) {
	case []any:
		var parts []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				parts = append(parts, "'"+s+"'")
			} else {
				parts = append(parts, status.PyStr(x))
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return status.PyStr(v)
}

// warningLines is _append_warning_lines.
func warningLines(env *jsonx.Object) string {
	out := ""
	for _, w := range env.GetList("warnings") {
		if o, ok := w.(*jsonx.Object); ok {
			out += fmt.Sprintf("\n[warn] %s: %s", o.GetString("code"), o.GetString("message"))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// completion predicates (status.lifecycle)
// ---------------------------------------------------------------------------

func lastReopenAt(dir string) string {
	last := ""
	for _, e := range lifecycle.ReadEvents(status.EventsPath(dir)) {
		if e.GetString("event_type") == "MissionReopened" {
			if p := e.GetObject("payload"); p != nil && p.GetString("reopened_at") > last {
				last = p.GetString("reopened_at")
			}
		}
	}
	return last
}

// isMissionMerged is is_mission_merged (reopen-aware merged_at marker).
func isMissionMerged(dir string) bool {
	at := mission.ReadMetaOrEmpty(dir).GetString("merged_at")
	if at == "" {
		return false
	}
	r := lastReopenAt(dir)
	return r == "" || r < at
}

// isMissionCompleted is is_mission_completed: merged, or every WP terminal.
func isMissionCompleted(dir string) bool {
	if mission.ReadMetaOrEmpty(dir).GetString("merged_at") != "" {
		return true
	}
	stream, err := status.ReadStream(dir, "")
	if err != nil {
		return false
	}
	snap := status.Reduce(stream.Transitions, stream.Annotations, true)
	if snap.WorkPackages.Len() == 0 {
		return false
	}
	for _, id := range snap.WorkPackages.Keys() {
		if !status.IsTerminal(snap.Lane(id)) {
			return false
		}
	}
	return lastReopenAt(dir) == ""
}

// resolveMissionHandleDir resolves a mission handle to its primary dir.
func resolveMissionHandleDir(ctx *cli.Context, root, handle string, jsonMode bool) (string, error) {
	slug, err := mission.ResolveHandle(root, handle)
	if err == nil {
		return project.Specs(root, slug), nil
	}
	switch e := err.(type) {
	case *mission.AmbiguousError:
		if jsonMode {
			p := jsonError("MISSION_AMBIGUOUS_SELECTOR", e.Error())
			p.Set("handle", e.Handle)
			p.Set("candidates", e.CandidatesJSON())
			emitJSON(ctx, p)
		} else {
			rich.Print(ctx.Stdout, "MISSION_AMBIGUOUS_SELECTOR\n"+e.Error())
		}
		return "", cli.Exit(1)
	case *mission.SelectorAmbiguousError:
		if jsonMode {
			p := jsonError(e.Code(), e.Error())
			p.Set("handle", e.Handle)
			p.Set("candidates", e.Candidates)
			emitJSON(ctx, p)
		} else {
			rich.Print(ctx.Stdout, "MISSION_AMBIGUOUS_SELECTOR\n"+e.Error())
		}
		return "", cli.Exit(1)
	}
	return project.Specs(root, handle), nil
}

func detectActor() string {
	if os.Getenv("CLAUDE_CODE_ENTRYPOINT") != "" {
		return "claude"
	}
	if os.Getenv("CODEX_CLI") != "" {
		return "codex"
	}
	return "operator"
}

// ---------------------------------------------------------------------------
// mission follow-up / reopen
// ---------------------------------------------------------------------------

var hex40 = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func runMissionFollowUp(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	commit := ctx.OptString("commit")
	hasPR := ctx.IsSet("pr")
	if (commit == nil) == !hasPR {
		missionError(ctx, "Error: supply exactly one of --commit <40-hex> or --pr <int>.", "invalid_follow_up_reference", jsonMode)
		return cli.Exit(1)
	}
	if commit != nil && !hex40.MatchString(*commit) {
		missionError(ctx, fmt.Sprintf("Error: --commit must be a 40-character hex SHA, got %s.", mission.PyRepr(*commit)), "invalid_commit_sha", jsonMode)
		return cli.Exit(1)
	}
	root := locateRoot()
	if root == "" {
		missionError(ctx, "Error: Not in a spec-kitty project", "not_in_project", jsonMode)
		return cli.Exit(1)
	}
	handle := ctx.Arg("handle")
	dir, err := resolveMissionHandleDir(ctx, root, handle, jsonMode)
	if err != nil {
		return err
	}
	slug := filepath.Base(dir)
	meta := mission.ReadMetaOrEmpty(dir)
	mid := meta.GetString("mission_id")
	if mid == "" {
		missionError(ctx, fmt.Sprintf("Error: mission has no resolvable mission_id for handle %s.\nRemediation: run `spec-kitty migrate backfill-identity`.", handle), "mission_identity_missing", jsonMode)
		return cli.Exit(1)
	}
	if !isMissionCompleted(dir) {
		missionError(ctx, fmt.Sprintf("Error: cannot record follow-up: mission %s has not completed/merged.\nRemediation: follow-ups can only be recorded after a mission has merged or all its work packages are terminal.", slug), "mission_not_completed", jsonMode)
		return cli.Exit(1)
	}
	typ := "pr"
	var commitV, prV any
	dedup := jsonx.NewObject("mission_id", mid)
	if commit != nil {
		typ = "commit"
		commitV = *commit
		dedup.Set("commit_sha", *commit)
	} else {
		prV = ctx.Int("pr")
		dedup.Set("pr_number", ctx.Int("pr"))
	}
	payload := jsonx.NewObject("mission_id", mid, "mission_slug", slug, "follow_up_type", typ, "commit_sha", commitV,
		"pr_number", prV, "recorded_by", detectActor(), "recorded_at", clock.NowUTCISO())
	ev, err := lifecycle.Persist(dir, "FollowUpRecorded", payload, mid, "Mission", nil, nil, dedup)
	if err != nil {
		missionError(ctx, "Error: "+err.Error(), "follow_up_failed", jsonMode)
		return cli.Exit(1)
	}
	if jsonMode {
		res := "recorded"
		var eid any
		if ev == nil {
			res = "duplicate"
		} else {
			eid = ev.GetString("event_id")
		}
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("commit_sha", commitV, "event_id", eid, "follow_up_type", typ,
			"mission_id", mid, "mission_slug", slug, "pr_number", prV, "result", res), jsonx.Pretty))
		return nil
	}
	ref := ""
	if commit != nil {
		ref = *commit
	} else {
		ref = "#" + strconv.Itoa(ctx.Int("pr"))
	}
	if ev == nil {
		rich.Print(ctx.Stdout, fmt.Sprintf("Follow-up %s %s already recorded for %s (no-op).", typ, ref, slug))
	} else {
		rich.Print(ctx.Stdout, fmt.Sprintf("✓ Recorded follow-up %s %s for %s.", typ, ref, slug))
	}
	return nil
}

func nilStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var mergeFields = []string{"merged_at", "merged_by", "merged_into", "merged_strategy", "merged_push", "merged_commit"}

func branchResolvable(root, branch string) bool {
	if gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) {
		return true
	}
	remotes, _ := gitx.Run(root, "remote")
	for _, r := range strings.Fields(remotes) {
		if gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/remotes/"+r+"/"+branch) {
			return true
		}
	}
	return false
}

func runMissionReopen(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		missionError(ctx, "Error: Not in a spec-kitty project", "not_in_project", jsonMode)
		return cli.Exit(1)
	}
	handle := ctx.Arg("handle")
	reason := ctx.String("reason")
	dir, err := resolveMissionHandleDir(ctx, root, handle, jsonMode)
	if err != nil {
		return err
	}
	slug := filepath.Base(dir)
	meta, merr := mission.ReadMeta(dir)
	if merr != nil || meta == nil || meta.GetString("mission_id") == "" {
		missionError(ctx, fmt.Sprintf("Error: mission is unrecoverable — meta.json is missing or corrupt for handle %s.\nRemediation: restore meta.json (or run `spec-kitty migrate backfill-identity`) before re-opening.", handle), "mission_unrecoverable", jsonMode)
		return cli.Exit(1)
	}
	mid := meta.GetString("mission_id")
	if mb := meta.GetString("mission_branch"); mb != "" && !branchResolvable(root, mb) {
		missionError(ctx, fmt.Sprintf("Error: mission is unrecoverable — branch %s resolves in neither the local repo nor any configured remote.\nRemediation: fetch the branch (`git fetch <remote> %s`) or restore it before re-opening.", mb, mb), "mission_branch_unavailable", jsonMode)
		return cli.Exit(1)
	}
	if !isMissionCompleted(dir) {
		missionError(ctx, fmt.Sprintf("Error: cannot re-open: mission %s has not completed/merged.\nRemediation: a mission can only be re-opened after it has merged or all its work packages are terminal.", slug), "mission_not_completed", jsonMode)
		return cli.Exit(1)
	}
	cleared := &jsonx.Object{}
	for _, f := range mergeFields {
		if v, ok := meta.Get(f); ok {
			cleared.Set(f, v)
		}
	}
	var cm any
	if cleared.Len() > 0 {
		cm = cleared
	}
	payload := jsonx.NewObject("mission_id", mid, "mission_slug", slug, "reason", reason, "reopened_by", detectActor(),
		"reopened_at", clock.NowUTCISO(), "cleared_merge", cm)
	ev, err := lifecycle.Persist(dir, "MissionReopened", payload, mid, "Mission", nil, nil, nil)
	if err != nil {
		missionError(ctx, "Error: "+err.Error(), "mission_reopen_failed", jsonMode)
		return cli.Exit(1)
	}
	if cleared.Len() > 0 {
		for _, f := range mergeFields {
			meta.Delete(f)
		}
		if err := mission.WriteMeta(dir, meta, false); err != nil {
			missionError(ctx, fmt.Sprintf("Error: mission %s was re-opened and the audit event was recorded, but clearing the merge markers failed (%v).\nRemediation: rerun `spec-kitty mission reopen %s` to complete marker cleanup.", slug, err, slug), "mission_reopen_clear_failed", jsonMode)
			return cli.Exit(1)
		}
	}
	if jsonMode {
		var eid any
		if ev != nil {
			eid = ev.GetString("event_id")
		}
		emitJSON(ctx, jsonx.NewObject("cleared_merge", sortedObj(cleared), "event_id", eid, "mission_id", mid,
			"mission_slug", slug, "reason", reason, "result", "reopened"))
		return nil
	}
	rich.Print(ctx.Stdout, fmt.Sprintf("✓ Mission %s re-opened and is actionable again.", slug))
	if cleared.Len() > 0 {
		keys := cleared.Keys()
		sort.Strings(keys)
		rich.Print(ctx.Stdout, "  Cleared merge markers: "+strings.Join(keys, ", "))
	}
	return nil
}

func sortedObj(o *jsonx.Object) *jsonx.Object {
	keys := o.Keys()
	sort.Strings(keys)
	out := &jsonx.Object{}
	for _, k := range keys {
		v, _ := o.Get(k)
		out.Set(k, v)
	}
	return out
}

// ---------------------------------------------------------------------------
// mission close
// ---------------------------------------------------------------------------

func runMissionClose(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	root := locateRoot()
	if root == "" {
		missionError(ctx, "Error: Not in a spec-kitty project", "not_in_project", jsonMode)
		return cli.Exit(1)
	}
	handle := ctx.String("mission")
	if handle == "" {
		if cwd, _ := os.Getwd(); cwd != "" {
			if b := lanes.ParseBranch(gitx.CurrentBranch(cwd)); b != nil {
				handle = b.MissionSlug
			}
		}
	}
	if handle == "" {
		missionError(ctx, "Error: No mission specified and no active mission detected. Use --mission <slug>.", "mission_not_specified", jsonMode)
		return cli.Exit(1)
	}
	slug := handle
	if st, err := os.Stat(project.Specs(root, handle)); err != nil || !st.IsDir() {
		s, err := mission.ResolveHandle(root, handle)
		if err != nil {
			missionError(ctx, "Mission not found: "+handle, "mission_not_found", jsonMode)
			return cli.Exit(1)
		}
		slug = s
	}
	dir := project.Specs(root, slug)
	s := coord.Resolve(root, slug)
	if ctx.Bool("discard") {
		return discardMission(ctx, root, slug, dir, s, jsonMode)
	}
	if !isMissionMerged(dir) {
		missionError(ctx, fmt.Sprintf("Error: cannot close: mission %s has not been merged.\nRemediation: this mission has no recorded merge baseline. Use `--discard` to abandon it mid-flight, or consolidate it first (`spec-kitty consolidate`) before closing.", slug), "mission_not_merged", jsonMode)
		return cli.Exit(1)
	}
	teardownCoordination(ctx, root, slug, dir, "", jsonMode)
	if jsonMode {
		emitJSON(ctx, jsonx.NewObject("ok", true, "result", "closed", "mission_slug", slug))
	} else {
		rich.Print(ctx.Stdout, fmt.Sprintf("✓ Mission %s closed.", slug))
	}
	return nil
}

func discardMission(ctx *cli.Context, root, slug, dir string, s *coord.Surface, jsonMode bool) error {
	if !ctx.Bool("force") {
		fmt.Fprintf(ctx.Stdout, "Discard mission %s? This deletes its coordination and lane branches. [y/N]: ", slug)
		var ans string
		fmt.Fscanln(ctx.Stdin, &ans)
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			rich.Print(ctx.Stdout, "Aborted.")
			return cli.Exit(1)
		}
	}
	m, _ := lanes.Read(dir)
	if m != nil {
		for _, l := range m.Lanes {
			if l.LaneID == lanes.PlanningLaneID {
				continue
			}
			wt := lanes.WorktreePath(root, slug, l.LaneID)
			if _, err := os.Stat(wt); err == nil {
				gitx.Run(root, "worktree", "remove", "--force", wt)
			}
			br := lanes.LaneBranchName(slug, l.LaneID, m.TargetBranch)
			if gitx.BranchExists(root, br) {
				gitx.Run(root, "branch", "-D", br)
			}
		}
	}
	if s.CoordWorktree != "" {
		if _, err := os.Stat(s.CoordWorktree); err == nil {
			gitx.Run(root, "worktree", "remove", "--force", s.CoordWorktree)
		}
	}
	gitx.Run(root, "worktree", "prune")
	if s.CoordBranch != "" && gitx.BranchExists(root, s.CoordBranch) {
		if tip, err := gitx.RevParse(root, s.CoordBranch); err == nil {
			gitx.Run(root, "update-ref", "refs/spec-kitty/discarded/"+s.CoordBranch, tip)
		}
		gitx.Run(root, "branch", "-D", s.CoordBranch)
	}
	meta := mission.ReadMetaOrEmpty(dir)
	meta.Delete("coordination_branch")
	meta.Delete("topology")
	meta.Set("flattened", true)
	meta.Set("discarded", true)
	if err := mission.WriteMeta(dir, meta, false); err == nil {
		coord.CommitPaths(root, fmt.Sprintf("chore(%s): flatten discarded mission metadata", slug), filepath.Join(dir, mission.MetaFile))
	}
	if jsonMode {
		emitJSON(ctx, jsonx.NewObject("ok", true, "result", "discarded", "mission_slug", slug))
	} else {
		rich.Print(ctx.Stdout, fmt.Sprintf("✓ Mission %s discarded.", slug))
	}
	return nil
}
