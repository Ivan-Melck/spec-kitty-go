package agentcore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission/create"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

const createLong = "Create new mission directory structure in the project root checkout.\n\n" +
	"This command is designed for AI agents to call programmatically.\n" +
	"Creates mission directory in kitty-specs/ and commits to the current branch.\n\n" +
	"Examples:\n    spec-kitty agent mission create \"new-dashboard\" --json"

const topologyHelp = "Create-time mission shape: single_branch | lanes | coord | lanes_with_coord. Coordination-bearing shapes (coord, lanes_with_coord) mint a coordination branch and materialize its worktree immediately, seeding it with the mission's creation events (MissionCreated, SpecifyStarted) so the coordination surface is live from birth (#5440); branch-flat shapes (single_branch, lanes) do neither. Default: context-derived (#2581, #2602) — coord on the primary branch or with --pr-bound when coordination is reachable; lanes otherwise. single_branch only when requested explicitly (or via --owned-checkout)."

func init() {
	addCommands(func(missionG, _, _ *cli.Command) {
		missionG.Add(&cli.Command{
			Name:  "create",
			Short: "Create new mission directory structure in the project root checkout.",
			Long:  createLong,
			Args:  []*cli.Arg{{Name: "mission_slug", Metavar: "{mission_slug}", Help: "Mission slug (e.g., 'user-auth')", Required: true}},
			Flags: []*cli.Flag{
				{Name: "mission-type", Kind: cli.String, Metavar: "<str>", Help: "Mission type (e.g., 'documentation', 'software-dev')"},
				{Name: "mission", Kind: cli.String, Hidden: true, Help: "(deprecated) Use --mission-type"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
				{Name: "target-branch", Kind: cli.String, Metavar: "<str>", Help: "Target branch (defaults to current branch)"},
				{Name: "friendly-name", Kind: cli.String, Metavar: "<str>", Help: "Human-friendly mission title"},
				{Name: "purpose-tldr", Kind: cli.String, Metavar: "<str>", Help: "One-line stakeholder TLDR for the mission"},
				{Name: "purpose-context", Kind: cli.String, Metavar: "<str>", Help: "Short stakeholder-facing paragraph for the mission"},
				{Name: "pr-bound", Kind: cli.Bool, Negatable: true, Help: "Mark mission as PR-bound (gate fires on merge_target_branch)"},
				{Name: "topology", Kind: cli.String, Metavar: "<single_branch|lanes|coord|lanes_with_coord>", Choices: []string{"single_branch", "lanes", "coord", "lanes_with_coord"}, Help: topologyHelp},
				{Name: "branch-strategy", Kind: cli.String, Metavar: "<str>", Help: "Branch-strategy gate control (e.g., 'already-confirmed' to bypass the prompt)"},
				{Name: "start-branch", Kind: cli.String, Metavar: "<str>", Help: "Create or switch to this branch before mission files are written"},
				{Name: "force-recreate-coordination-branch", Kind: cli.Bool, Help: "Delete and recreate the per-mission coordination branch if it already exists and has diverged from the target. Operator escape hatch; never used by automation."},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: "Run against an owned checkout: a linked checkout that owns this mission. Refuses the repository root checkout, lane worktrees and coordination worktrees."},
				{Name: "retain-branches", Kind: cli.Bool, Help: "Opt this mission's branches out of post-merge cleanup deletion."},
				{Name: "retain-worktrees", Kind: cli.Bool, Help: "Opt this mission's worktrees out of post-merge cleanup deletion."},
				{Name: "commit-to-target", Kind: cli.Bool, Negatable: true, Help: "single_branch only: skip the protected-target mission-branch mint (#5100 FR-008) and commit directly onto --target-branch, even when it is protected. Persisted; honoured through ProtectionPolicy for every later write."},
				{Name: "allow-duplicate", Aliases: []string{"allow-dup"}, Kind: cli.Bool, Help: "Escape hatch for the idempotency guard (#4033): create a second mission even though a LIVE prior mission shares this slug and mission type. Abandoned priors (canceled, genesis, or spec never committed) never need this flag."},
			},
			Run: runCreate,
		})
		missionG.Add(&cli.Command{
			Name:  "branch-context",
			Short: "Return deterministic branch contract for planning-stage prompts.",
			Flags: []*cli.Flag{
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
				{Name: "target-branch", Kind: cli.String, Metavar: "<str>", Help: "Planned landing branch (defaults to current branch)"},
			},
			Run: runBranchContext,
		})
	})
}

// --- branch contract --------------------------------------------------------

func utcStamp() string { return clock.Now().UTC().Format("2006-01-02T15:04:05Z") }

// injectBranchContract is _inject_branch_contract.
func injectBranchContract(payload *jsonx.Object, target, current, primary string, matchTarget *string, expectedCheckout string) *jsonx.Object {
	e := payload.Clone()
	rv := e.GetObject("runtime_vars")
	if rv == nil {
		rv = &jsonx.Object{}
	} else {
		rv = rv.Clone()
	}
	now := rv.GetString("now_utc_iso")
	if now == "" {
		now = utcStamp()
	}
	cur := strings.TrimSpace(current)
	if cur == "" {
		cur = target
	}
	expected := expectedCheckout
	if expected == "" {
		expected = target
	}
	ref := expected
	if matchTarget != nil {
		ref = *matchTarget
	}
	matches := cur == ref
	summary := fmt.Sprintf("Current branch at workflow start: %s. Planning/base branch for this mission: %s. Completed changes must merge into %s.", cur, target, target)
	rv.Set("now_utc_iso", now)
	rv.Set("current_branch", cur)
	rv.Set("target_branch", target)
	rv.Set("base_branch", target)
	rv.Set("planning_base_branch", target)
	rv.Set("merge_target_branch", target)
	rv.Set("branch_matches_target", matches)
	rv.Set("branch_strategy_summary", summary)
	bc := jsonx.NewObject("current_branch", cur, "target_branch", target, "base_branch", target, "planning_base_branch", target,
		"merge_target_branch", target, "expected_checkout_branch", expected, "matches_target", matches, "branch_strategy_summary", summary)
	for _, kv := range []jsonx.KV{{Key: "current_branch", Value: cur}, {Key: "CURRENT_BRANCH", Value: cur}, {Key: "target_branch", Value: target},
		{Key: "base_branch", Value: target}, {Key: "TARGET_BRANCH", Value: target}, {Key: "BASE_BRANCH", Value: target},
		{Key: "planning_base_branch", Value: target}, {Key: "PLANNING_BASE_BRANCH", Value: target}, {Key: "merge_target_branch", Value: target},
		{Key: "MERGE_TARGET_BRANCH", Value: target}, {Key: "EXPECTED_TARGET_BRANCH", Value: target}, {Key: "EXPECTED_BASE_BRANCH", Value: target},
		{Key: "branch_matches_target", Value: matches}, {Key: "BRANCH_MATCHES_TARGET", Value: matches}, {Key: "branch_strategy_summary", Value: summary}} {
		e.Set(kv.Key, kv.Value)
	}
	e.Set("runtime_vars", rv)
	e.Set("NOW_UTC_ISO", now)
	e.Set("branch_context", bc)
	if primary != "" {
		isPrimary := cur == primary
		strategy, reason := "stay", fmt.Sprintf("You are on '%s', which is not the primary branch '%s'; staying on it is fine.", cur, primary)
		if isPrimary {
			strategy = "feature-branch"
			reason = fmt.Sprintf("You are on the primary branch '%s'. PR-bound missions should start on a dedicated topic branch so planning artifacts are not committed to '%s'.", primary, primary)
		}
		bc.Set("primary_branch", primary)
		bc.Set("current_is_primary", isPrimary)
		bc.Set("recommended_strategy", strategy)
		bc.Set("reason", reason)
		rv.Set("primary_branch", primary)
		rv.Set("current_is_primary", isPrimary)
		rv.Set("recommended_strategy", strategy)
		e.Set("primary_branch", primary)
		e.Set("PRIMARY_BRANCH", primary)
		e.Set("current_is_primary", isPrimary)
		e.Set("CURRENT_IS_PRIMARY", isPrimary)
		e.Set("recommended_strategy", strategy)
		e.Set("RECOMMENDED_STRATEGY", strategy)
		e.Set("branch_recommendation_reason", reason)
	}
	return e
}

var commonPrimary = []string{"main", "master", "develop"}

func originHead(root string) string {
	out, err := gitx.Run(root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/origin/")
}

// resolvePrimaryBranch is git_ops.resolve_primary_branch.
func resolvePrimaryBranch(root, current string, bias bool) string {
	if o := originHead(root); o != "" {
		return o
	}
	if current == "" {
		current = gitx.CurrentBranch(root)
	}
	if bias {
		if current != "" && current != "HEAD" {
			return current
		}
	} else {
		for _, c := range commonPrimary {
			if c == current {
				return current
			}
		}
	}
	for _, b := range commonPrimary {
		if bias {
			if gitx.OK(root, "rev-parse", "--verify", b) {
				return b
			}
		} else if gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+b) || gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+b) {
			return b
		}
	}
	if bias {
		return "main"
	}
	return resolvePrimaryBranch(root, "", true)
}

const rootNotFound = "Could not locate project root. Run from within spec-kitty repository."

func cwd() string {
	d, _ := os.Getwd()
	if r, err := filepath.EvalSymlinks(d); err == nil {
		return r
	}
	return d
}

func runBranchContext(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	failMsg := func(msg string) error {
		if jsonMode {
			emitJSON(ctx.Stdout, jsonx.NewObject("error", msg))
		} else {
			consolePrint(ctx.Stdout, "Error: "+msg)
		}
		return cli.Exit(1)
	}
	root := project.Locate("")
	if root == "" {
		return failMsg(rootNotFound)
	}
	if !gitx.IsRepo(root) {
		return failMsg("Not in a git repository. Branch context requires git.")
	}
	caller := project.CurrentWorktreeRoot(cwd())
	if caller == "" || project.MainRepoRoot(caller) != project.MainRepoRoot(root) {
		caller = root
	}
	current := gitx.CurrentBranch(caller)
	if current == "" || current == "HEAD" {
		return failMsg("Must be on a branch to resolve branch context (detached HEAD detected).")
	}
	primary := resolvePrimaryBranch(root, current, false)
	target := current
	source := "current_branch"
	if ctx.IsSet("target-branch") && ctx.String("target-branch") != "" {
		source = "cli_arg"
		if t := strings.TrimSpace(ctx.String("target-branch")); t != "" {
			target = t
		}
	}
	payload := jsonx.NewObject("result", "success", "repo_root", root, "target_branch_source", source,
		"next_step", "Use this deterministic branch contract during specify/plan prompts; do not rediscover branch state inside the LLM.")
	e := injectBranchContract(payload, target, current, primary, nil, "")
	if jsonMode {
		emitJSON(ctx.Stdout, e)
		return nil
	}
	pb := func(b bool) string {
		if b {
			return "True"
		}
		return "False"
	}
	consolePrint(ctx.Stdout, "Current branch: "+e.GetString("current_branch"))
	consolePrint(ctx.Stdout, "Planning/base branch: "+e.GetString("planning_base_branch"))
	consolePrint(ctx.Stdout, "Merge target: "+e.GetString("merge_target_branch"))
	consolePrint(ctx.Stdout, "Matches target: "+pb(e.GetBool("branch_matches_target")))
	consolePrint(ctx.Stdout, "Primary branch: "+e.GetString("primary_branch"))
	consolePrint(ctx.Stdout, "On primary branch: "+pb(e.GetBool("current_is_primary")))
	consolePrint(ctx.Stdout, "Recommended strategy: "+e.GetString("recommended_strategy"))
	consolePrint(ctx.Stdout, e.GetString("branch_recommendation_reason"))
	return nil
}

// --- create -------------------------------------------------------------------

const startTargetMismatch = "--start-branch and --target-branch must match because mission creation stores one planning branch. Omit --target-branch for the recommended PR-bound feature-branch flow."

func switchToStartBranch(root, start string) error {
	branch := strings.TrimSpace(start)
	if branch == "" {
		return fmt.Errorf("--start-branch requires a non-empty branch name.")
	}
	if root == "" {
		return fmt.Errorf("%s", rootNotFound)
	}
	r, _ := gitx.Exec(root, nil, nil, "check-ref-format", "--branch", branch)
	if r.Code != 0 {
		detail := strings.TrimSpace(r.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(r.Stdout)
		}
		if detail == "" {
			detail = "invalid branch name"
		}
		return fmt.Errorf("Invalid --start-branch '%s': %s", branch, detail)
	}
	if gitx.CurrentBranch(root) == branch {
		return nil
	}
	exists := gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) || gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	args := []string{"switch", branch}
	if !exists {
		args = []string{"switch", "-c", branch}
	}
	r, _ = gitx.Exec(root, nil, nil, args...)
	if r.Code != 0 {
		detail := strings.TrimSpace(r.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(r.Stdout)
		}
		if detail == "" {
			detail = "unknown git error"
		}
		return fmt.Errorf("Failed to switch to --start-branch '%s': %s", branch, detail)
	}
	return nil
}

type startRollback struct {
	root, start, origBranch, origCommit string
	preexisted                          bool
}

func captureStartRollback(root, start string) *startRollback {
	start = strings.TrimSpace(start)
	if root == "" || start == "" {
		return nil
	}
	head, err := gitx.HeadSHA(root)
	if err != nil {
		return nil
	}
	return &startRollback{root: root, start: start, origBranch: gitx.CurrentBranch(root), origCommit: head,
		preexisted: gitx.OK(root, "show-ref", "--verify", "--quiet", "refs/heads/"+start)}
}

func (s *startRollback) restore() {
	if s == nil {
		return
	}
	if s.origBranch != "" && s.origBranch != "HEAD" {
		gitx.Run(s.root, "switch", s.origBranch)
	} else {
		gitx.Run(s.root, "switch", "--detach", s.origCommit)
	}
	if !s.preexisted && s.start != s.origBranch {
		if tip, err := gitx.RevParse(s.root, "refs/heads/"+s.start); err == nil && tip == s.origCommit {
			gitx.Run(s.root, "branch", "-D", s.start)
		}
	}
}

func runCreate(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	slug := ctx.Arg("mission_slug")
	root := project.Locate("")
	errOut := func(payload *jsonx.Object, human string) error {
		if jsonMode {
			emitJSON(ctx.Stdout, payload)
		} else {
			consolePrint(ctx.Stdout, human)
		}
		return cli.Exit(1)
	}
	if ctx.IsSet("owned-checkout") {
		msg := "--owned-checkout is not supported by this build."
		return errOut(jsonx.NewObject("error", msg), "Error: "+msg)
	}
	var rb *startRollback
	if ctx.IsSet("start-branch") {
		rb = captureStartRollback(root, ctx.String("start-branch"))
	}
	failed := func(err error) error {
		rb.restore()
		return err
	}
	if ctx.IsSet("start-branch") {
		start := ctx.String("start-branch")
		tb := strings.TrimSpace(ctx.String("target-branch"))
		if tb != "" && tb != strings.TrimSpace(start) {
			return failed(errOut(jsonx.NewObject("error_code", "START_BRANCH_TARGET_MISMATCH", "error", startTargetMismatch,
				"start_branch", start, "target_branch", ctx.String("target-branch")), "Error: "+startTargetMismatch))
		}
		if err := switchToStartBranch(root, start); err != nil {
			return failed(errOut(jsonx.NewObject("error_code", "START_BRANCH_FAILED", "error", err.Error(), "start_branch", start), "Error: "+err.Error()))
		}
	}
	missionType := ctx.String("mission-type")
	if ctx.IsSet("mission") {
		alias := ctx.String("mission")
		if ctx.IsSet("mission-type") && missionType != alias {
			msg := fmt.Sprintf("Conflicting values: --mission-type=%s and --mission=%s. Use --mission-type <name>.", missionType, alias)
			return failed(errOut(jsonx.NewObject("error", msg), "Error: "+msg))
		}
		missionType = alias
	}
	current := ""
	if root != "" {
		current = gitx.CurrentBranch(root)
		if current == "HEAD" {
			current = ""
		}
	}
	prBound := ctx.Bool("pr-bound")
	target := ctx.String("target-branch")
	mergeTarget := target
	if mergeTarget == "" {
		mergeTarget = current
	}
	strategy := ctx.String("branch-strategy")
	if strategy == "" && ctx.IsSet("start-branch") {
		strategy = "already-confirmed"
	}
	if prBound && mergeTarget != "" && current != "" && current == mergeTarget && strategy != "already-confirmed" {
		if jsonMode {
			emitJSON(ctx.Stdout, jsonx.NewObject("error_code", "BRANCH_STRATEGY_CONFIRMATION_REQUIRED",
				"error", "PR-bound mission creation requires explicit branch-strategy confirmation in --json mode.",
				"branch_strategy_gate", "confirmation_required", "current_branch", current, "merge_target_branch", mergeTarget,
				"remediation", "Pass `--branch-strategy already-confirmed` or run without --json to confirm interactively."))
			return failed(cli.Exit(1))
		}
		// Non-interactive stdin: typer.confirm reads EOF → abort.
		fmt.Fprintf(ctx.Stdout, "You are on '%s', which is the mission's merge target. PR-bound missions usually live on a topic branch. Proceed anyway? [y/N]: ", current)
		if !readYes(ctx) {
			fmt.Fprintln(ctx.Stdout)
			consolePrint(ctx.Stdout, "Aborted: Mission creation aborted by operator at branch-strategy gate. Switch to a topic branch or pass `--branch-strategy already-confirmed`.")
			return failed(cli.Exit(1))
		}
	}
	topology := ctx.String("topology")
	if topology == "" {
		topology = defaultTopology(root, current, prBound)
	}
	var fn, tldr, pctx *string
	if ctx.IsSet("friendly-name") {
		s := ctx.String("friendly-name")
		fn = &s
	}
	if ctx.IsSet("purpose-tldr") {
		s := ctx.String("purpose-tldr")
		tldr = &s
	}
	if ctx.IsSet("purpose-context") {
		s := ctx.String("purpose-context")
		pctx = &s
	}
	res, err := create.Create(create.Options{
		Root: root, Cwd: cwd(), Slug: slug, MissionType: missionType, TargetBranch: target,
		FriendlyName: fn, PurposeTLDR: tldr, PurposeContext: pctx, PRBound: prBound, Topology: topology,
		ForceRecreateCoord: ctx.Bool("force-recreate-coordination-branch"), RetainBranches: ctx.Bool("retain-branches"),
		RetainWorktrees: ctx.Bool("retain-worktrees"), CommitToTarget: ctx.Bool("commit-to-target"), AllowDuplicate: ctx.Bool("allow-duplicate"),
	})
	if err != nil {
		return failed(renderCreateError(ctx, err, jsonMode, slug))
	}
	return emitCreateResult(ctx, res, missionType, jsonMode)
}

func readYes(ctx *cli.Context) bool {
	if ctx.Stdin == nil {
		return false
	}
	buf := make([]byte, 64)
	n, _ := ctx.Stdin.Read(buf)
	ans := strings.ToLower(strings.TrimSpace(string(buf[:n])))
	return ans == "y" || ans == "yes"
}

func defaultTopology(root, current string, prBound bool) string {
	if root == "" || current == "" {
		return create.Coord
	}
	primary := resolvePrimaryBranch(root, current, true)
	if prBound {
		if create.ProtectedBranches(root)[primary] || current == primary {
			return create.Coord
		}
		return create.Lanes
	}
	if current == primary {
		return create.Coord
	}
	return create.Lanes
}

func renderCreateError(ctx *cli.Context, err error, jsonMode bool, slug string) error {
	switch e := err.(type) {
	case *create.DivergedError:
		if jsonMode {
			p := jsonx.NewObject("error", e.Error())
			for _, kv := range e.ToObject().Pairs() {
				p.Set(kv.Key, kv.Value)
			}
			emitJSON(ctx.Stdout, p)
		} else {
			consolePrint(ctx.Stdout, "Error: "+e.Error())
		}
	case *create.Error:
		if jsonMode {
			p := jsonx.NewObject("error", e.Msg)
			if e.ErrorCode != "" && e.ErrorCode != "ACTIVE_CHARTER_CONFIG" {
				p.Set("error_code", e.ErrorCode)
			}
			emitJSON(ctx.Stdout, p)
		} else {
			consolePrint(ctx.Stdout, "Error: "+e.Msg)
			if strings.Contains(strings.ToLower(e.Msg), "worktree") {
				if main := project.Locate(""); main != "" {
					consolePrint(ctx.Stdout, "\nRun from the main repository instead:")
					consolePrint(ctx.Stdout, "  cd "+main)
					consolePrint(ctx.Stdout, "  spec-kitty agent mission create "+slug)
				}
			}
		}
	default:
		if jsonMode {
			emitJSON(ctx.Stdout, jsonx.NewObject("error", err.Error()))
		} else {
			consolePrint(ctx.Stdout, "Error: "+err.Error())
		}
	}
	return cli.Exit(1)
}

func strList(l []string) []any {
	out := []any{}
	for _, s := range l {
		out = append(out, s)
	}
	return out
}

func emitCreateResult(ctx *cli.Context, r *create.Result, missionType string, jsonMode bool) error {
	meta := r.Meta
	if !jsonMode {
		consolePrint(ctx.Stdout, fmt.Sprintf("Branch: %s (target for this mission)", r.TargetBranch))
		if missionType == "documentation" {
			consolePrint(ctx.Stdout, "→ Documentation state initialized in meta.json")
		}
		consolePrint(ctx.Stdout, "✓ Mission created: "+r.MissionSlug)
		consolePrint(ctx.Stdout, "   Title: "+meta.GetString("friendly_name"))
		consolePrint(ctx.Stdout, "   TLDR: "+meta.GetString("purpose_tldr"))
		consolePrint(ctx.Stdout, "   Context: "+meta.GetString("purpose_context"))
		consolePrint(ctx.Stdout, "   Directory: "+r.FeatureDir)
		metaFile := filepath.Join(r.FeatureDir, "meta.json")
		committed := true
		for _, u := range r.UncommittedFiles {
			if u == metaFile {
				committed = false
			}
		}
		if committed {
			consolePrint(ctx.Stdout, "   Meta committed to "+r.TargetBranch+"; spec.md scaffold left untracked")
		} else {
			consolePrint(ctx.Stdout, fmt.Sprintf("   Meta not committed: the scaffold commit to %s was refused (protected or unavailable target branch); kitty-specs/%s/ is left on disk, untracked", r.TargetBranch, r.MissionSlug))
			consolePrint(ctx.Stdout, "   Planning artifacts must land on a topic branch, or land via the mission lane worktree — switch to a topic branch first, or re-run 'agent mission create --start-branch <topic-branch>'.")
		}
		consolePrint(ctx.Stdout, "   Scaffold only: run /spec-kitty.specify <intent> in your agent, or edit and commit spec.md before planning.")
		return nil
	}
	spec := filepath.Join(r.FeatureDir, "spec.md")
	unc := []any{}
	for _, p := range r.UncommittedFiles {
		reason, cmd := "Generated scaffold could not be committed to the protected or unavailable target branch.", "commit from a non-protected topic branch"
		if p == spec {
			reason, cmd = "Scaffolded empty at create time; populated and committed with substantive content later (#846).", "/spec-kitty.specify"
		}
		unc = append(unc, jsonx.NewObject("path", p, "reason", reason, "responsible_command", cmd))
	}
	str := func(k string) string {
		v, ok := meta.Get(k)
		if !ok || v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
	var coordBranch any
	if r.CoordinationBranch != "" {
		coordBranch = r.CoordinationBranch
	}
	mb, _ := meta.Get("mission_branch")
	ctt, _ := meta.Get("commit_to_target")
	payload := jsonx.NewObject(
		"result", "success",
		"mission_slug", r.MissionSlug,
		"mission_number", nil,
		"mission_id", str("mission_id"),
		"mission_type", str("mission_type"),
		"slug", str("slug"),
		"friendly_name", str("friendly_name"),
		"purpose_tldr", str("purpose_tldr"),
		"purpose_context", str("purpose_context"),
		"mission_dir", r.FeatureDir,
		"feature_dir", r.FeatureDir,
		"spec_file", spec,
		"meta_file", filepath.Join(r.FeatureDir, "meta.json"),
		"created_at", str("created_at"),
		"created_files", strList(r.CreatedFiles),
		"mission_branch", mb,
		"commit_to_target", ctt,
		"uncommitted_artifacts", unc,
		"write_mode", "update_existing_files",
		"scaffold_only", true,
		"requires_agent_authoring", true,
		"plan_guard", "SPEC_NOT_SUBSTANTIVE_OR_UNCOMMITTED",
		"next_step", "Created scaffold only. Run `/spec-kitty.specify <intent>` in your agent or edit and commit spec_file before `spec-kitty plan`.",
		"origin_binding", jsonx.NewObject("attempted", false, "succeeded", false, "error", nil),
		"coordination_branch", coordBranch,
		"coordination_branch_created", r.CoordinationBranchCreated,
		"topology", str("topology"),
	)
	emitJSON(ctx.Stdout, injectBranchContract(payload, r.TargetBranch, r.CurrentBranch, "", nil, ""))
	return nil
}
