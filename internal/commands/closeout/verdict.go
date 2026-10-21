package closeout

import (
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/acceptance"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

const ownedCheckoutHelp = "Run against an owned checkout: a linked checkout that owns this mission. Refuses the repository root checkout, lane worktrees and coordination worktrees."

func verdictCommand() *cli.Command {
	return &cli.Command{
		Name:  "acceptance-verdict",
		Short: "Record an acceptance-criterion verdict, or register/execute a negative invariant (FR-007/FR-008) — exactly one of ``--criterion``/ ``--negative-invariant``, both routed through the WP03 write seam.",
		Flags: []*cli.Flag{
			{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission slug, mid8, or mission_id"},
			{Name: "criterion", Kind: cli.String, Metavar: "<str>", Help: "Acceptance criterion id (e.g. FR-001); mutually exclusive with --negative-invariant"},
			{Name: "result", Kind: cli.String, Metavar: "<str>", Help: "pass | fail | pending (required with --criterion)"},
			{Name: "verification-method", Kind: cli.String, Metavar: "<str>", Help: "Criterion mode: how this was verified (updates proof_type). Negative-invariant mode: grep_absence | route_check | custom_command (required with --negative-invariant)."},
			{Name: "actor", Kind: cli.String, Metavar: "<str>", Help: "Actor recording this verdict"},
			{Name: "evidence", Kind: cli.String, Metavar: "<str>", Help: "Evidence reference (URL/path/etc.)"},
			{Name: "negative-invariant", Kind: cli.String, Metavar: "<str>", Help: "Negative invariant id to register/execute (FR-007/FR-008); mutually exclusive with --criterion"},
			{Name: "description", Kind: cli.String, Metavar: "<str>", Help: "Negative invariant description (required with --negative-invariant)"},
			{Name: "verification-command", Kind: cli.String, Metavar: "<str>", Help: "grep pattern or command verifying the invariant's absence"},
			{Name: "scope", Kind: cli.String, Metavar: "<str>", Help: "Whitespace-separated repo-relative search root(s); grep_absence only"},
			{Name: "execute", Kind: cli.Bool, Negatable: true, Default: true, Help: "Run the invariant's verification immediately after registering (FR-008; default: on)"},
			{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedCheckoutHelp},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
		},
		Run: runAcceptanceVerdict,
	}
}

func init() {
	addCommands(func(_, agent, missionG *cli.Command) {
		agent.Add(verdictCommand())
		missionG.Add(verdictCommand())
	})
}

func verdictError(ctx *cli.Context, jsonMode bool, msg string) {
	if jsonMode {
		printJSON(ctx.Stdout, jsonx.NewObject("success", false, "error", msg))
	} else {
		sayLiteral(ctx.Stdout, "Error: ", msg)
	}
}

func runAcceptanceVerdict(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	criterion, invariant := ctx.OptString("criterion"), ctx.OptString("negative-invariant")
	result, description, method := ctx.OptString("result"), ctx.OptString("description"), ctx.OptString("verification-method")
	fail2 := func(msg string) error { verdictError(ctx, jsonMode, msg); return cli.Exit(2) }
	switch {
	case criterion != nil && invariant != nil:
		return fail2("--criterion and --negative-invariant are mutually exclusive")
	case criterion == nil && invariant == nil:
		return fail2("one of --criterion or --negative-invariant is required")
	}
	if criterion != nil {
		if result == nil {
			return fail2("--result is required with --criterion")
		}
		if *result != "pass" && *result != "fail" && *result != "pending" {
			return fail2(fmt.Sprintf("--result must be one of fail, pass, pending; got %s", mission.PyRepr(*result)))
		}
	}
	if invariant != nil {
		if description == nil {
			return fail2("--description is required with --negative-invariant")
		}
		if method == nil {
			return fail2("--verification-method is required with --negative-invariant")
		}
	}
	root := locateRoot()
	if root == "" {
		verdictError(ctx, jsonMode, "Could not locate project root")
		return cli.Exit(1)
	}
	if ctx.IsSet("owned-checkout") {
		verdictError(ctx, jsonMode, "--owned-checkout is not supported by this build.")
		return cli.Exit(1)
	}
	slug, err := resolveStrict(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return err
	}
	dir, err := acceptance.MatrixWriteDir(root, slug)
	if err != nil {
		verdictError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	matrix, err := acceptance.ReadMatrix(dir)
	if err != nil {
		verdictError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	if matrix == nil {
		verdictError(ctx, jsonMode, fmt.Sprintf("No acceptance-matrix.json found for mission %s. Run `spec-kitty agent mission finalize-tasks` to scaffold it first.", mission.PyRepr(slug)))
		return cli.Exit(1)
	}
	var row *jsonx.Object
	var entryKey, entryID, message, recorded string
	if invariant != nil {
		row = acceptance.NewInvariant(*invariant, *description, *method, ctx.OptString("verification-command"), ctx.OptString("scope"))
		matrix.SetInvariant(row)
		if ctx.Bool("execute") {
			matrix.NegativeInvariants = acceptance.EnforceNegativeInvariants(root, matrix.NegativeInvariants, nil)
			for _, n := range matrix.NegativeInvariants {
				if n.GetString("invariant_id") == *invariant {
					row = n
				}
			}
		}
		entryKey, entryID = "negative_invariant", *invariant
		message = fmt.Sprintf("chore(acceptance): register negative invariant %s for %s", *invariant, slug)
		recorded = row.GetString("result")
	} else {
		existing := matrix.Criterion(*criterion)
		if existing == nil {
			ids := []string{}
			for _, c := range matrix.Criteria {
				ids = append(ids, c.GetString("criterion_id"))
			}
			avail := "(none)"
			if len(ids) > 0 {
				avail = joinSorted(ids)
			}
			verdictError(ctx, jsonMode, fmt.Sprintf("Unknown criterion %s for mission %s. Available criteria: %s", mission.PyRepr(*criterion), mission.PyRepr(slug), avail))
			return cli.Exit(1)
		}
		row = acceptance.UpdateCriterion(existing, *result, method, ctx.OptString("actor"), ctx.OptString("evidence"))
		entryKey, entryID = "criterion", *criterion
		message = fmt.Sprintf("chore(acceptance): record %s=%s for %s", *criterion, *result, slug)
		recorded = *result
	}
	// Locked re-read + splice of this invocation's own row.
	fresh, err := acceptance.ReadMatrix(dir)
	if err != nil {
		verdictError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	if fresh == nil {
		fresh = &acceptance.Matrix{MissionSlug: slug}
	}
	if invariant != nil {
		fresh.SetInvariant(row)
	} else {
		fresh.SetCriterion(row)
	}
	out, err := acceptance.WriteAndCommitMatrix(root, slug, dir, fresh, message)
	if err != nil {
		verdictError(ctx, jsonMode, err.Error())
		return cli.Exit(1)
	}
	failMsg := ""
	switch {
	case out.Status == acceptance.StatusRefused:
		failMsg = fmt.Sprintf("Could not route the acceptance-verdict write for %s: %s", mission.PyRepr(slug), orDefault(out.Diagnostic, "unroutable target"))
	case out.Status == acceptance.StatusError:
		failMsg = fmt.Sprintf("Failed to commit acceptance verdict for %s: %s", mission.PyRepr(slug), orDefault(out.Diagnostic, "unknown error"))
	case out.ExitCode() != 0:
		failMsg = fmt.Sprintf("A per-surface commit for %s was refused; see 'surfaces' for detail.", mission.PyRepr(slug))
	}
	if failMsg != "" {
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("success", false, "error", failMsg, "surfaces", out.SurfacesJSON()))
		} else {
			for _, l := range out.Render() {
				sayLiteral(ctx.Stdout, "", l)
			}
			sayLiteral(ctx.Stdout, "Error: ", failMsg)
		}
		return cli.Exit(1)
	}
	verdict := fresh.OverallVerdict()
	if jsonMode {
		var hash any
		if out.CommitHash != "" {
			hash = out.CommitHash
		}
		printJSON(ctx.Stdout, jsonx.NewObject("success", true, "mission", slug, entryKey, entryID, "result", recorded,
			"overall_verdict", verdict, "write_status", out.Status, "destination_surface", out.DestinationSurface,
			"commit_hash", hash, "surfaces", out.SurfacesJSON()))
		return nil
	}
	for _, l := range out.Render() {
		sayLiteral(ctx.Stdout, "", l)
	}
	if invariant != nil {
		say(ctx.Stdout, fmt.Sprintf("[green]✓[/green] negative invariant %s=%s recorded for %s (overall_verdict=%s, write=%s)", entryID, recorded, slug, verdict, out.Status))
	} else {
		say(ctx.Stdout, fmt.Sprintf("[green]✓[/green] %s=%s recorded for %s (overall_verdict=%s, write=%s)", entryID, recorded, slug, verdict, out.Status))
	}
	return nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
