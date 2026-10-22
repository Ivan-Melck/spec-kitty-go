package closeout

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/originfresh"
)

const originCheckHelp = "Refuse (enforce, the default) or only warn (warn) when the mission's status evidence is behind or unreachable on its remote; off contacts nothing and accepts stale evidence. Overrides SPEC_KITTY_ORIGIN_CHECK. --no-commit and --diagnose always warn."

func init() {
	addCommands(func(root, _, missionG *cli.Command) {
		root.Add(&cli.Command{
			Name:  "accept",
			Short: "Validate mission readiness before merging to main.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug to accept"},
				{Name: "mode", Kind: cli.String, Metavar: "<str>", Default: "auto", Help: "Acceptance mode: auto, pr, local, or checklist"},
				{Name: "actor", Kind: cli.String, Metavar: "<str>", Help: "Name to record as the acceptance actor"},
				{Name: "test", Kind: cli.StringList, Metavar: "<str>", Help: "Validation command executed (repeatable)"},
				{Name: "json", Kind: cli.Bool, Help: "Emit JSON instead of formatted text"},
				{Name: "lenient", Kind: cli.Bool, Help: "Skip strict metadata validation and downgrade missing path-convention checks to warnings"},
				{Name: "no-commit", Kind: cli.Bool, Help: "Report acceptance readiness without writing metadata or status changes"},
				{Name: "diagnose", Kind: cli.Bool, Help: "Diagnose acceptance blockers without writing metadata or matrix artifacts"},
				{Name: "allow-fail", Kind: cli.Bool, Help: "Return checklist even when issues remain"},
				{Name: "normalize-encoding", Kind: cli.Bool, Negatable: true, Default: false, Help: "Repair acceptance-artifact encoding (Windows-1252/Latin-1 -> UTF-8) before validating."},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedCheckoutHelp},
				{Name: "origin-check", Kind: cli.String, Metavar: "<function>", Choices: []string{"enforce", "warn", "off"}, Help: originCheckHelp},
				{Name: "merge-commit", Kind: cli.String, Metavar: "SHA", Help: "With --mode pr: record this PR merge commit as the mission's post-merge review baseline. The commit is verified against git before anything is written — it must carry kitty-specs/<slug>/meta.json, its first parent must not, and it must have landed on the target branch. Every landing shape additionally needs --attest-first-landing-commit."},
				{Name: "target-branch", Kind: cli.String, Metavar: "<str>", Help: "With --merge-commit: the branch the PR merged into (the PR's base branch). Defaults to the mission's declared target_branch, else the repository's primary branch."},
				{Name: "attest-first-landing-commit", Kind: cli.Bool, Help: "With --merge-commit: attest that the supplied commit's first parent is the pre-landing target tip — for a two-parent merge commit, that the merge was performed ON the target branch (an internal merge fast-forwarded onto the target is graph-identical, and its first parent is an implementation commit); for a single-parent landing (squash or corpus-first stack), that it was the first commit of the landing. Required for every landing shape: git cannot prove either, and a wrong anchor silently under-scans the dead-code gate. The attestation is recorded in pr_merge_evidence, never presented as a git proof."},
			},
			Run: func(ctx *cli.Context) error {
				return runAccept(ctx, acceptOpts{
					mission: ctx.String("mission"), mode: ctx.String("mode"), actor: ctx.String("actor"), tests: ctx.Strings("test"),
					json: ctx.Bool("json"), lenient: ctx.Bool("lenient"), noCommit: ctx.Bool("no-commit"), diagnose: ctx.Bool("diagnose"),
					allowFail: ctx.Bool("allow-fail"), owned: ctx.IsSet("owned-checkout"), mergeCommit: ctx.String("merge-commit"),
					targetBranch: ctx.OptString("target-branch"), originCheck: ctx.OptString("origin-check"), attest: ctx.Bool("attest-first-landing-commit"),
					normalizeEncoding: ctx.Bool("normalize-encoding"),
				})
			},
		})
		missionG.Add(&cli.Command{
			Name:  "accept",
			Short: "Perform mission acceptance workflow.",
			Long: "Perform mission acceptance workflow.\n\nThis command:\n1. Validates all tasks are in 'done' lane\n2. Runs acceptance checks from checklist files\n3. Creates acceptance report\n4. Marks mission as ready for merge\n\n" +
				"Wrapper for top-level accept command with agent-specific defaults.\n\nExamples:\n    # Run acceptance workflow\n    spec-kitty agent mission accept --mission 077-my-mission\n\n" +
				"    # With JSON output for agents\n    spec-kitty agent mission accept --mission 077-my-mission --json\n\n    # Lenient mode (skip strict validation)\n    spec-kitty agent mission accept --mission 077-my-mission --lenient --json",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (required in multi-mission repos)"},
				{Name: "mode", Kind: cli.String, Metavar: "<str>", Default: "auto", Help: "Acceptance mode: auto, pr, local, checklist"},
				{Name: "json", Kind: cli.Bool, Help: "Output results as JSON for agent parsing"},
				{Name: "lenient", Kind: cli.Bool, Help: "Skip strict metadata validation"},
				{Name: "no-commit", Kind: cli.Bool, Help: "Skip auto-commit (report only)"},
				{Name: "diagnose", Kind: cli.Bool, Help: "Diagnose acceptance blockers without mutation"},
				{Name: "merge-commit", Kind: cli.String, Metavar: "SHA", Help: "With --mode pr: record this PR merge commit as the mission's post-merge review baseline (#4231)."},
				{Name: "target-branch", Kind: cli.String, Metavar: "<str>", Help: "With --merge-commit: the branch the PR merged into. Defaults to the mission's declared target_branch, else the repository's primary branch."},
				{Name: "attest-first-landing-commit", Kind: cli.Bool, Help: "With --merge-commit: attest the supplied commit's first parent is the pre-landing target tip (#4231). Required for every landing shape."},
			},
			Run: func(ctx *cli.Context) error {
				return runAccept(ctx, acceptOpts{
					mission: ctx.String("mission"), mode: ctx.String("mode"), json: ctx.Bool("json"), lenient: ctx.Bool("lenient"),
					noCommit: ctx.Bool("no-commit"), diagnose: ctx.Bool("diagnose"), mergeCommit: ctx.String("merge-commit"), agent: true,
					targetBranch: ctx.OptString("target-branch"), attest: ctx.Bool("attest-first-landing-commit"),
				})
			},
		})
	})
}

type acceptOpts struct {
	mission, mode, actor, mergeCommit            string
	tests                                        []string
	json, lenient, noCommit, diagnose, allowFail bool
	owned, agent                                 bool
	targetBranch, originCheck                    *string
	attest                                       bool
	normalizeEncoding                            bool
}

type acceptRun struct {
	ctx        *cli.Context
	json       bool
	tr         *tracker
	advisories []any
}

// fail is accept._fail: one error on the active output lane, then exit.
func (r *acceptRun) fail(msg string, code int, step string, extra *jsonx.Object) error {
	r.report(msg, step, extra)
	return cli.Exit(code)
}

func (r *acceptRun) report(msg, step string, extra *jsonx.Object) {
	if r.json {
		o := jsonx.NewObject("error", msg)
		if extra != nil {
			for _, kv := range extra.Pairs() {
				o.Set(kv.Key, kv.Value)
			}
		}
		printJSON(r.ctx.Stdout, o)
		return
	}
	if r.tr != nil && step != "" {
		r.tr.fail(step, msg)
		r.tr.render(r.ctx.Stdout)
	}
	sayLiteral(r.ctx.Stdout, "Error: ", msg)
}

func runAccept(ctx *cli.Context, o acceptOpts) error {
	r := &acceptRun{ctx: ctx, json: o.json, advisories: []any{}}
	root := locateRoot()
	if root == "" {
		return r.fail("Could not locate project root", 1, "", nil)
	}
	if o.owned {
		return r.fail("--owned-checkout is not supported by this build.", 1, "", nil)
	}
	r.tr = newTracker("Mission Acceptance")
	if !o.json {
		r.tr.add("detect", "Identify mission slug")
		r.tr.add("verify", "Run readiness checks")
		fmt.Fprintln(ctx.Stdout)
		r.tr.start("detect")
	}
	if o.mission == "" {
		return r.fail("--mission <slug> is required", 2, "detect", nil)
	}
	slug, err := resolveWithBareFold(ctx, root, o.mission, o.json)
	if err != nil {
		return err
	}
	if !o.json {
		r.tr.complete("detect", slug)
	}
	mode := acceptance.ChooseMode(strings.ToLower(orDefault(o.mode, "auto")), root)
	commitRequired := mode != "checklist" && !o.noCommit && !o.diagnose
	if !o.json {
		if commitRequired {
			r.tr.add("commit", "Record acceptance metadata")
		}
		if o.diagnose {
			r.tr.add("guide", "Report diagnostics")
		} else {
			r.tr.add("guide", "Share next steps")
		}
	}
	var prEvidence *migrate.PrMergeEvidence
	if o.mergeCommit != "" {
		if mode != "pr" {
			return r.fail(fmt.Sprintf("--merge-commit is only valid with --mode pr (resolved mode: %s). A %s acceptance records its baseline_merge_commit through `spec-kitty consolidate`, not here.", mode, mode), 2, "", nil)
		}
		ev, err := migrate.VerifyPrMergeEvidence(root, slug, o.mergeCommit, o.targetBranch, o.attest)
		if err != nil {
			return r.fail(fmt.Sprintf("Cannot record PR merge for %s: %v", slug, err), 1, "", nil)
		}
		prEvidence = ev
	}
	setting := originfresh.ResolveMode(o.originCheck)
	if o.noCommit || o.diagnose {
		setting = originfresh.ReadOnly
	}
	originWarnings, refused := originfresh.Run(root, slug, setting, nil, false)
	if refused != nil {
		rows := []any{}
		for _, v := range refused.Verdicts {
			rows = append(rows, v.Payload())
		}
		codes := []any{}
		for _, c := range refused.Codes {
			codes = append(codes, c)
		}
		return r.fail(refused.Message, 1, "", jsonx.NewObject("error_code", refused.Code, "error_codes", codes, "origin_freshness", rows))
	}
	for _, w := range originWarnings {
		fmt.Fprintln(ctx.Stderr, "Warning: "+w)
	}
	advisories := []any{}
	for _, w := range originWarnings {
		advisories = append(advisories, w)
	}
	r.advisories = advisories
	if !o.json {
		r.tr.start("verify")
	}
	sum, err := r.collectWithOptionalRepair(root, slug, acceptance.CollectOptions{StrictMetadata: !o.lenient, MutateMatrix: !o.diagnose}, o.normalizeEncoding)
	if err != nil {
		return r.fail(err.Error(), 1, "verify", nil)
	}
	if !o.json {
		if sum.OK() {
			r.tr.complete("verify", "ready")
		} else {
			r.tr.complete("verify", "issues found")
		}
	}
	if o.diagnose {
		if o.json {
			p := sum.ToObject()
			sum.AddLaneDerivations(p)
			p.Set("diagnose", true)
			p.Set("advisories", r.advisories)
			printJSONPretty(ctx.Stdout, p)
			return nil
		}
		r.tr.start("guide")
		r.tr.complete("guide", "diagnostics ready")
		r.tr.render(ctx.Stdout)
		printDiagnosis(ctx, sum)
		return nil
	}
	if mode == "checklist" {
		r.reportSummary(sum, false)
		if sum.OK() {
			return nil
		}
		return cli.Exit(1)
	}
	if !sum.OK() {
		r.reportSummary(sum, true)
		if !o.allowFail && !o.json {
			say(ctx.Stdout, "\n[red]Outstanding acceptance issues detected. Resolve them before merging or rerun with --allow-fail for a checklist-only report.[/red]")
		}
		return cli.Exit(1)
	}
	// perform + post-acceptance steps
	if commitRequired && !o.json {
		r.tr.start("commit")
	}
	res, perr := acceptance.Perform(sum, mode, o.actor, o.tests, commitRequired)
	if perr != nil {
		step := ""
		if commitRequired {
			step = "commit"
		}
		r.report(perr.Error(), step, nil)
	} else if commitRequired && !o.json {
		if res.CommitCreated {
			r.tr.complete("commit", "commit created")
		} else {
			r.tr.complete("commit", "no changes")
		}
	}
	var stampErr, prErr error
	var residual *acceptance.Outcome
	prRecorded := false
	if commitRequired {
		if perr == nil {
			stampErr = acceptance.StampBirthCutover(sum.FeatureDir)
			if prEvidence != nil {
				if _, err := migrate.RecordPrMergeBaseline(root, slug, o.mergeCommit, o.targetBranch, o.attest); err != nil {
					prErr = err
				} else {
					prRecorded = true
				}
			}
		}
		residual = acceptance.ResidualCommit(root, slug)
	}
	if perr != nil || res == nil {
		return cli.Exit(1)
	}
	if stampErr != nil {
		return r.fail("Birth-cutover stamp refused: "+stampErr.Error(), 1, "", nil)
	}
	if prErr != nil {
		return r.fail("PR merge recording failed: "+prErr.Error(), 1, "", nil)
	}
	if residual != nil && residual.ExitCode() != 0 {
		step := ""
		if commitRequired {
			step = "commit"
		}
		msg := fmt.Sprintf("Residual artifact commit failed: Residual acceptance artifact commit failed for %s: %s", slug, acceptance.ResidualFailureDetail(residual))
		return r.fail(msg, 1, step, jsonx.NewObject("residual_commit", jsonx.NewObject("surfaces", residual.SurfacesJSON())))
	}
	if prEvidence != nil {
		if prRecorded {
			res.Notes = append(res.Notes, fmt.Sprintf("PR merge recorded: baseline_merge_commit %s (PR merge commit %s; anchor evidence %s)",
				prEvidence.BaselineMergeCommit, prEvidence.PrMergeCommit, prEvidence.AnchorEvidence))
		} else {
			res.Notes = append(res.Notes, "--merge-commit verified against this repository; re-run without --no-commit to record it as the review baseline")
		}
	}
	if o.json {
		p := res.ToObject()
		p.Set("advisories", r.advisories)
		if residual != nil {
			p.Set("residual_commit", jsonx.NewObject("surfaces", residual.SurfacesJSON()))
		}
		printJSONPretty(ctx.Stdout, p)
		return nil
	}
	r.tr.start("guide")
	r.tr.complete("guide", "instructions ready")
	r.tr.render(ctx.Stdout)
	printSummary(ctx, sum)
	printResult(ctx, res)
	if residual != nil {
		for _, l := range residual.Render() {
			sayLiteral(ctx.Stdout, "", l)
		}
	}
	return nil
}

func (r *acceptRun) reportSummary(sum *acceptance.Summary, raw bool) {
	if r.json {
		p := sum.ToObject()
		if !raw {
			sum.AddLaneDerivations(p)
		}
		p.Set("advisories", r.advisories)
		printJSONPretty(r.ctx.Stdout, p)
		return
	}
	printSummary(r.ctx, sum)
}

func printWarnings(ctx *cli.Context, sum *acceptance.Summary) {
	if len(sum.Warnings) == 0 {
		return
	}
	say(ctx.Stdout, "\n[bold yellow]Warnings[/bold yellow]")
	for _, w := range sum.Warnings {
		say(ctx.Stdout, "[yellow]- "+w+"[/yellow]")
	}
}

func printSummary(ctx *cli.Context, sum *acceptance.Summary) {
	var rows [][]string
	for _, l := range acceptance.LaneOrder {
		items := sum.Lanes[l]
		disp := "-"
		if len(items) > 0 {
			disp = strings.Join(items, ", ")
		}
		rows = append(rows, []string{l, fmt.Sprint(len(items)), disp})
	}
	simpleTable(ctx.Stdout, "Work Packages by Lane", []string{"Lane", "Count", "Work Packages"}, rows, map[int]bool{1: true})
	out := sum.Outstanding()
	if len(out) > 0 {
		say(ctx.Stdout, "\n[bold red]Outstanding items[/bold red]")
		for _, b := range out {
			say(ctx.Stdout, "[red]- "+b.Key+"[/red]")
			for _, v := range b.Values {
				say(ctx.Stdout, "    • "+v)
			}
		}
	} else {
		say(ctx.Stdout, "\n[green]No outstanding acceptance issues detected.[/green]")
	}
	printWarnings(ctx, sum)
}

func printDiagnosis(ctx *cli.Context, sum *acceptance.Summary) {
	failed := sum.FailedChecks()
	if len(failed) > 0 {
		say(ctx.Stdout, "\n[bold red]Failed checks[/bold red]")
		for _, f := range failed {
			say(ctx.Stdout, "[red]- "+f.Check+"[/red]: "+f.Detail)
		}
	} else {
		say(ctx.Stdout, "\n[green]No failed acceptance checks detected.[/green]")
	}
	if len(sum.Skipped) > 0 {
		say(ctx.Stdout, "\n[bold yellow]Skipped checks[/bold yellow]")
		for _, f := range sum.Skipped {
			say(ctx.Stdout, "[yellow]- "+f.Check+"[/yellow]: "+f.Detail)
		}
	}
	if len(sum.Blocked) > 0 {
		say(ctx.Stdout, "\n[bold yellow]Blocked checks[/bold yellow]")
		for _, f := range sum.Blocked {
			say(ctx.Stdout, "[yellow]- "+f.Check+"[/yellow]: "+f.Detail)
		}
	}
	printWarnings(ctx, sum)
	if len(sum.RecommendedFixOrder) > 0 {
		say(ctx.Stdout, "\n[bold]Recommended fix order[/bold]")
		for i, f := range sum.RecommendedFixOrder {
			say(ctx.Stdout, fmt.Sprintf("  %d. %s", i+1, f))
		}
	}
}

func printResult(ctx *cli.Context, r *acceptance.Result) {
	say(ctx.Stdout, fmt.Sprintf("\n[bold]Acceptance metadata[/bold]\n• Mission: %s\n• Accepted at: %s\n• Accepted by: %s", r.Summary.Feature, r.AcceptedAt, r.AcceptedBy))
	if r.AcceptCommit != "" {
		say(ctx.Stdout, "• Acceptance commit: "+r.AcceptCommit)
	}
	if r.ParentCommit != "" {
		say(ctx.Stdout, "• Parent commit: "+r.ParentCommit)
	}
	if !r.CommitCreated {
		say(ctx.Stdout, "• Commit status: no changes were committed (dry-run)")
	}
	if len(r.Accepted) > 0 {
		say(ctx.Stdout, "• Accepted WPs: "+strings.Join(r.Accepted, ", "))
	}
	if len(r.Merge) > 0 {
		say(ctx.Stdout, "• Merge-pending WPs: "+strings.Join(r.Merge, ", "))
	}
	if len(r.Done) > 0 {
		say(ctx.Stdout, "• Already merged WPs: "+strings.Join(r.Done, ", "))
	}
	for _, sec := range []struct {
		title string
		items []string
	}{{"Next steps", r.Instructions}, {"Cleanup", r.CleanupInstr}} {
		if len(sec.items) > 0 {
			say(ctx.Stdout, "\n[bold]"+sec.title+"[/bold]")
			for i, it := range sec.items {
				say(ctx.Stdout, fmt.Sprintf("  %d. %s", i+1, it))
			}
		}
	}
	if len(r.Notes) > 0 {
		say(ctx.Stdout, "\n[bold]Notes[/bold]")
		for _, n := range r.Notes {
			say(ctx.Stdout, "  - "+n)
		}
	}
}
