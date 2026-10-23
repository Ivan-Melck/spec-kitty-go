package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/branchctx"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lifecycle"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/reqmap"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

const setupPlanCmd = "spec-kitty agent mission setup-plan"

func init() {
	addCommands(func(_, _, missionG, _ *cli.Command) {
		missionG.Add(&cli.Command{
			Name:  "setup-plan",
			Short: "Scaffold an implementation plan template in the repository root checkout or an owned checkout.",
			Long: "Scaffold an implementation plan template in the repository root checkout or an owned checkout.\n\nThis command is designed for AI agents to call programmatically.\nCreates plan.md and commits to target branch.\n\n" +
				"Examples:\n    spec-kitty agent mission setup-plan --json\n    spec-kitty agent mission setup-plan --mission 020-my-feature --json\n\n" +
				"------------------------------------------------------------------\nThe SaaS-sync boundary gates this command used to enforce (FR-011 auth\n" +
				"refusal, boundary preflight, dossier push) were removed with the sync\ntransport (issue #5); only local planning artifacts are produced here.\n" +
				"------------------------------------------------------------------",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (e.g., '020-my-mission')"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
				ownedFlag(),
			},
			Run: runSetupPlan,
		})
	})
}

// readPathFor is _find_feature_directory (coord-aware read path).
func readPathFor(root, handle string) (string, error) {
	h := strings.TrimSpace(handle)
	if h == "" {
		return "", &detectionErr{"MISSION_CONTEXT_UNRESOLVED", "--mission <slug> is required"}
	}
	name, err := mission.ResolveHandle(root, h)
	if err != nil {
		if amb, ok := err.(*mission.SelectorAmbiguousError); ok {
			return "", &detectionErr{amb.Code(), amb.Error()}
		}
		return "", &detectionErr{"MISSION_CONTEXT_UNRESOLVED", fmt.Sprintf("%s %s; checked the coordination worktree and the primary checkout.", notFoundHandleMarker, mission.PyRepr(h))}
	}
	dir := coord.Resolve(root, name).StatusDir()
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", &detectionErr{"MISSION_CONTEXT_UNRESOLVED", fmt.Sprintf("%s %s; checked the coordination worktree and the primary checkout.", notFoundHandleMarker, mission.PyRepr(h))}
	}
	return dir, nil
}

// branchTreeRelative is _branch_tree_relative_path.
func branchTreeRelative(file, root string) string {
	if _, tree, ok := missionart.RepoTreePath(file, root); ok {
		return tree
	}
	return filepath.ToSlash(file)
}

// statusWriteDir is the STATUS write dir for lifecycle markers.
func statusWriteDir(root, slug string) string {
	if d, err := coord.Resolve(root, slug).WriteDir(); err == nil {
		return d
	}
	return project.Specs(root, slug)
}

func runSetupPlan(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	fail := func(msg string) error {
		if jsonMode {
			emitJSON(ctx.Stdout, jsonx.NewObject("error", msg))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return cli.Exit(1)
	}
	root := project.Locate("")
	if root == "" {
		return fail(projectRootNotFound)
	}
	if ctx.String("owned-checkout") != "" {
		return fail("--owned-checkout is not supported by this build")
	}
	feature := ctx.String("mission")
	resolved := feature
	if !ctx.IsSet("mission") {
		if c := listSpecCandidates(root); len(c) == 1 {
			resolved = c[0].slug
		}
	}
	featureDir, derr := readPathFor(root, resolved)
	if derr != nil {
		payload := detectionError(root, derr.Error(), feature, "PLAN_CONTEXT_UNRESOLVED", "setup-plan", []string{"--json"})
		emitDetectionError(ctx, jsonMode, payload)
		return cli.Exit(1)
	}
	slug := filepath.Base(featureDir)
	current := branchctx.CurrentBranch(root)
	if current == "" {
		return fail("Detached HEAD — checkout a branch before continuing")
	}
	primary := project.Specs(root, slug)
	metaTarget := strings.TrimSpace(mission.ReadMetaOrEmpty(primary).GetString("target_branch"))
	if !jsonMode {
		if metaTarget == "" || metaTarget == current {
			say(ctx.Stdout, fmt.Sprintf("[bold cyan]Branch:[/bold cyan] %s (target for this mission)", current))
		} else {
			say(ctx.Stdout, fmt.Sprintf("[bold yellow]Branch:[/bold yellow] on '%s', mission targets '%s'", current, metaTarget))
		}
	}
	target := current
	specFile := filepath.Join(primary, "spec.md")
	planFile := filepath.Join(primary, "plan.md")
	invoking := branchctx.CurrentBranch(project.CurrentWorktreeRoot(cwd()))
	if invoking == "" {
		invoking = target
	}
	matchTarget := invoking
	if metaTarget != "" {
		matchTarget = metaTarget
	}
	if mb := mission.ReadMetaOrEmpty(primary).GetString("mission_branch"); mb != "" {
		matchTarget = mb
	}
	inject := func(p *jsonx.Object) *jsonx.Object { return branchctx.Inject(p, target, invoking, matchTarget, "") }

	// --- spec gate ---
	if _, err := os.Stat(specFile); err != nil {
		rem := []string{"Restore the missing spec file at " + specFile, "Or select another mission explicitly: " + setupPlanCmd + " --mission <mission-slug> --json"}
		payload := jsonx.NewObject("error_code", "SPEC_FILE_MISSING", "error", fmt.Sprintf("Required spec not found for mission '%s': %s", slug, specFile),
			"mission_slug", slug, "mission_dir", featureDir, "feature_dir", featureDir, "spec_file", specFile, "remediation", strList(rem))
		if jsonMode {
			emitJSON(ctx.Stdout, payload)
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+payload.GetString("error"))
			for _, r := range rem {
				say(ctx.Stdout, "  - "+r)
			}
		}
		return cli.Exit(1)
	}
	mtype := missionart.MissionTypeFor(primary)
	committed := missionart.IsCommitted(specFile, root)
	_, tree, _ := missionart.RepoTreePath(specFile, root)
	hit := "miss"
	if committed {
		hit = "hit"
	}
	diags := []string{fmt.Sprintf("HEAD:%s (cwd=%s): %s", tree, realpathOr(root), hit)}
	substantive := missionart.IsSubstantive(specFile, "spec", mtype)
	if !(committed && substantive) {
		reason := "spec.md must be committed AND substantive before setup-plan can run. Populate the Functional Requirements (at least one FR-### row with real description content), commit spec.md, then re-run setup-plan."
		if mtype == "research" {
			reason = "spec.md must be committed AND substantive before setup-plan can run. Under the exact 'Research Question & Scope' heading, populate the primary research question and a bare '**Scope**:' line with an in-scope boundary; author at least one substantive DR/AR/QR requirement each, replacing shipped template lines; commit spec.md, then re-run setup-plan."
		}
		payload := inject(jsonx.NewObject("result", "blocked", "phase_complete", false, "blocked_reason", reason, "error_code", "SPEC_NOT_SUBSTANTIVE_OR_UNCOMMITTED",
			"mission_slug", slug, "mission_dir", featureDir, "feature_dir", featureDir, "spec_file", specFile, "spec_committed", committed,
			"spec_substantive", substantive, "spec_commit_surfaces_checked", strList(diags)))
		if jsonMode {
			emitJSON(ctx.Stdout, payload)
		} else {
			say(ctx.Stdout, "[yellow]Blocked:[/yellow] "+reason)
		}
		return nil
	}
	specText, _ := os.ReadFile(specFile)
	lintErrs, lintWarns := reqmap.LintSpec(string(specText), mtype)
	if len(lintErrs) > 0 {
		var inv []any
		for _, e := range lintErrs {
			inv = append(inv, e.Object())
		}
		rem := []string{"Use one of the recognised requirement-ID kinds: FR, NFR, C or SC.", "Write the letter suffix in lowercase (e.g. FR-006a, not FR-006A).",
			"To cite another mission's ID, write <mission-slug>#<ID> instead of declaring it here.", "Commit spec.md and re-run setup-plan."}
		msg := "spec.md declares requirement IDs that do not match the requirement-ID grammar"
		payload := jsonx.NewObject("result", "error", "phase_complete", false, "error_code", "SPEC_REQUIREMENT_IDS_INVALID", "error", msg,
			"invalid_requirement_ids", inv, "mission_slug", slug, "mission_dir", featureDir, "feature_dir", featureDir, "spec_file", specFile, "remediation", strList(rem))
		if jsonMode {
			emitJSON(ctx.Stdout, payload)
		} else {
			lines := []string{"[red]Error:[/red] " + rich.EscapeMarkup(msg)}
			for _, e := range lintErrs {
				lines = append(lines, fmt.Sprintf("  - line %d: %s (rule: %s)", e.Line, rich.EscapeMarkup(e.Token), rich.EscapeMarkup(e.Rule)))
			}
			say(ctx.Stdout, strings.Join(lines, "\n"))
		}
		return cli.Exit(1)
	}
	tmpl, ok := missionart.ResolveTemplate(missionart.PlanTemplateName(mtype), root, mtype)
	if !ok {
		return fail("Plan template not found in repository or package")
	}
	if _, err := os.Stat(planFile); err != nil {
		if err := os.WriteFile(planFile, tmpl, 0o644); err != nil {
			return fail(err.Error())
		}
	}
	sdir := statusWriteDir(root, slug)
	lifecycle.EmitArtifactPhase(sdir, lifecycle.PhaseInput{EventType: lifecycle.SpecifyCompleted, MissionSlug: slug, Actor: setupPlanCmd, ArtifactPath: branchTreeRelative(specFile, root)})
	lifecycle.EmitArtifactPhase(sdir, lifecycle.PhaseInput{EventType: lifecycle.PlanStarted, MissionSlug: slug, Actor: setupPlanCmd})

	planSub := missionart.IsSubstantive(planFile, "plan", mtype)
	var commitRes *safecommit.RouterResult
	blockedReason, scaffoldOnly := "", false
	if planSub {
		var err error
		commitRes, err = commitArtifact(ctx, jsonMode, root, slug, planFile, "plan", target)
		if err != nil {
			return fail(err.Error())
		}
		lifecycle.EmitArtifactPhase(sdir, lifecycle.PhaseInput{EventType: lifecycle.PlanCompleted, MissionSlug: slug, Actor: setupPlanCmd, ArtifactPath: branchTreeRelative(planFile, root)})
	} else {
		cur, _ := os.ReadFile(planFile)
		pristine := string(cur) == string(tmpl)
		gap, hasGap := missionart.DescribeTechnicalContextGap(string(cur), mtype)
		heading, primaryField, peer, ok := missionart.DescribePlanFieldRequirements(mtype)
		switch {
		case !ok:
			blockedReason = gap
			if !jsonMode {
				say(ctx.Stdout, "[yellow]Plan not committed:[/yellow] "+blockedReason)
			}
		case pristine && !missionart.IsCommitted(planFile, root):
			scaffoldOnly = true
			if !jsonMode {
				say(ctx.Stdout, fmt.Sprintf("[cyan]→[/cyan] Plan scaffolded at %s; populate %s and re-run setup-plan.", planFile, heading))
			}
		default:
			blockedReason = fmt.Sprintf("plan.md content is not substantive yet; populate %s with real values (%s plus at least one peer field, such as %s) — not template placeholders — and re-run setup-plan to commit.", heading, primaryField, peer)
			if hasGap {
				blockedReason += " Detail: " + gap
			}
			if !jsonMode {
				say(ctx.Stdout, "[yellow]Plan not committed:[/yellow] "+blockedReason)
			}
		}
	}
	gapPath, generators := runDocumentationWiring(ctx.Stdout, jsonMode, root, slug)
	res := "blocked"
	if planSub || scaffoldOnly {
		res = "success"
	}
	warns := []any{}
	for _, w := range lintWarns {
		warns = append(warns, w.Object())
	}
	payload := jsonx.NewObject("result", res, "phase_complete", planSub, "mission_slug", slug, "plan_file", planFile, "mission_dir", featureDir,
		"feature_dir", featureDir, "spec_file", specFile, "plan_substantive", planSub, "requirement_id_warnings", warns)
	if scaffoldOnly {
		payload.Set("scaffold_only", true)
	}
	if blockedReason != "" {
		payload.Set("blocked_reason", blockedReason)
	}
	if commitRes != nil {
		status := commitRes.Status
		payload.Set("commit_created", status == safecommit.StatusCommitted)
		payload.Set("commit_hash", optString(commitRes.CommitHash))
		payload.Set("commit_status", status)
		if status == safecommit.StatusNoOpWrongSurface && commitRes.Diagnostic != "" {
			payload.Set("commit_diagnostic", commitRes.Diagnostic)
		}
		if len(commitRes.Surfaces) > 0 {
			payload.Set("surfaces", commitRes.SurfacesPayload())
		}
	}
	if gapPath != "" {
		payload.Set("gap_analysis", gapPath)
	}
	if len(generators) > 0 {
		gl := make([]any, len(generators))
		for i, g := range generators {
			gl[i] = g
		}
		payload.Set("generators_detected", gl)
	}
	payload = inject(payload)
	if jsonMode {
		emitJSON(ctx.Stdout, payload)
		return nil
	}
	for _, w := range lintWarns {
		say(ctx.Stdout, fmt.Sprintf("[yellow]Warning:[/yellow] line %d: %s — %s", w.Line, rich.EscapeMarkup(w.Token), rich.EscapeMarkup(w.Message)))
	}
	say(ctx.Stdout, "[green]✓[/green] Plan scaffolded: "+planFile)
	return nil
}

// commitArtifact is _commit_to_branch: commit one planning artifact through
// the router and report it (human mode).
func commitArtifact(ctx *cli.Context, jsonMode bool, root, slug, file, artifactType, target string) (*safecommit.RouterResult, error) {
	msg := fmt.Sprintf("Add %s for mission %s", artifactType, slug)
	kind := mission.ArtifactKindForPath(branchTreeRelative(file, root))
	if kind == "" {
		kind = mission.KindSpec
	}
	r := safecommit.CommitForMission(root, slug, []string{file}, msg, kind, safecommit.RouterOptions{})
	if !jsonMode && len(r.Surfaces) > 0 {
		r.PrintRendered(ctx.Stdout, wrapPlain)
	}
	title := strings.ToUpper(artifactType[:1]) + artifactType[1:]
	switch r.Status {
	case safecommit.StatusCommitted:
		if !jsonMode {
			say(ctx.Stdout, fmt.Sprintf("[green]✓[/green] %s committed to %s", title, r.PlacementRef))
			if len(r.CommitHash) >= 7 {
				say(ctx.Stdout, "[dim]Commit: "+r.CommitHash[:7]+"[/dim]")
			}
		}
	case safecommit.StatusUnchanged:
		if !jsonMode {
			say(ctx.Stdout, "[dim]"+title+" unchanged, no commit needed[/dim]")
		}
	case safecommit.StatusNoOpWrongSurface:
		if !jsonMode {
			say(ctx.Stdout, "[yellow]Warning:[/yellow] "+r.Diagnostic)
		}
	default:
		d := r.Diagnostic
		if d == "" {
			d = "commit failed"
		}
		if !jsonMode {
			say(ctx.Stdout, fmt.Sprintf("[yellow]Warning:[/yellow] Failed to commit %s: %s", artifactType, d))
			rel := file
			say(ctx.Stdout, "[yellow]You may need to commit manually:[/yellow] "+rich.Strip(fmt.Sprintf("spec-kitty safe-commit %s -m %s --to-branch %s", shQuote(rel), shQuote(msg), shQuote(target))))
		}
		return nil, fmt.Errorf("%s", d)
	}
	return r, nil
}

func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./-_", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
