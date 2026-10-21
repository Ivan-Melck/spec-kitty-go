package maintenance

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

var compactJSON = jsonx.Options{Indent: -1, EnsureASCII: true}

// resolveMissionHandle is selector_resolution.resolve_mission_handle: the
// identity ladder; unknown/ambiguous handles print (stderr, exit 2) or emit a
// compact JSON envelope (stdout, exit 1).
func resolveMissionHandle(ctx *cli.Context, root, handle string, jsonMode bool) (*mission.Resolved, error) {
	r, err := mission.ResolveIdentity(root, handle)
	if err == nil {
		return r, nil
	}
	switch e := err.(type) {
	case *mission.AmbiguousError:
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("success", false, "error_code", "MISSION_AMBIGUOUS_SELECTOR", "error", e.Error(),
				"handle", e.Handle, "candidates", e.CandidatesJSON()), compactJSON))
			return nil, cli.Exit(1)
		}
		cprint(ctx.Stderr, e.Error())
		return nil, cli.Exit(2)
	case *mission.NotFoundError:
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("success", false, "error_code", "MISSION_NOT_FOUND", "error", e.Error(), "handle", e.Handle), compactJSON))
			return nil, cli.Exit(1)
		}
		cprintf(ctx.Stderr, "[red]Error:[/red] No mission found for handle \"%s\". Check that the handle is correct and that the mission exists in kitty-specs/.", e.Handle)
		return nil, cli.Exit(2)
	}
	return nil, err
}

const (
	mergeCommitHelp = "The PR merge commit that landed the mission on its target branch. Read it off the merged PR, then supply it here; the migration verifies it against git before writing anything — it must carry the mission's kitty-specs/<slug>/meta.json, its first parent must not, and it must have landed on the target branch. Every landing shape additionally needs --attest-first-landing-commit."
	mergeTargetHelp = "The branch the PR merged into (the PR's base branch), for the landing check. Defaults to the mission's declared target_branch, else the repository's primary branch."
	mergeAttestHelp = "Attest that the supplied --merge-commit's first parent is the pre-landing target tip. Required for every landing shape: for a two-parent merge commit, attest the merge was performed ON the target branch (a merge performed on a mission or sibling branch and then fast-forwarded onto the target is graph-identical, and its first parent is an implementation commit, not the tip); for a single-parent landing (squash or corpus-first stack), attest the supplied commit was the FIRST commit of the landing. Git cannot prove either — and a wrong anchor silently under-scans the dead-code gate. The attestation is recorded in pr_merge_evidence, never presented as a git proof."
)

func newBackfillMergeCommitCmd() *cli.Command {
	return &cli.Command{
		Name:  "backfill-merge-commit",
		Short: "Record a GitHub PR's real merge commit as a mission's review baseline (#4231).",
		Long:  longHelp["migrate_backfill-merge-commit"],
		Flags: []*cli.Flag{
			{Name: "mission", Kind: cli.String, Metavar: "HANDLE", Required: true, Help: "Mission to repair (mission_id / mid8 / slug)."},
			{Name: "merge-commit", Kind: cli.String, Metavar: "SHA", Required: true, Help: mergeCommitHelp},
			{Name: "target-branch", Kind: cli.String, Help: mergeTargetHelp},
			boolFlag("attest-first-landing-commit", mergeAttestHelp),
			boolFlag("dry-run", "Verify the merge evidence and report what would be written without writing any files. The JSON shape is identical to a live run."),
			boolFlag("json", "Emit the per-mission backfill result row as structured JSON."),
		},
		Run: runBackfillMergeCommit,
	}
}

func runBackfillMergeCommit(ctx *cli.Context) error {
	wireWarn(ctx)
	root, err := locateOrError(ctx)
	if err != nil {
		return err
	}
	jsonMode := ctx.Bool("json")
	dry := groupBool(ctx, "dry-run")
	resolved, err := resolveMissionHandle(ctx, root, ctx.String("mission"), jsonMode)
	if err != nil {
		return err
	}
	slug := resolved.Slug
	var target *string
	if ctx.IsSet("target-branch") {
		s := ctx.String("target-branch")
		target = &s
	}
	attest := ctx.Bool("attest-first-landing-commit")
	action, reason := "error", ""
	var prCommit, baseline, evidence any
	func() {
		meta, merr := migrate.LoadMetaFailClosed(project.Specs(root, slug))
		if merr != nil {
			reason = fmt.Sprintf("meta.json is unreadable (%v)", merr)
			return
		}
		if meta != nil {
			if v, _ := meta.Get("baseline_merge_commit"); pyjson.Truthy(v) && strings.TrimSpace(pyjson.Str(v)) != "" {
				action, reason = "skip", "baseline_merge_commit already recorded"
				return
			}
		}
		ev, verr := migrate.VerifyPrMergeEvidence(root, slug, ctx.String("merge-commit"), target, attest)
		if verr != nil {
			reason = verr.Error()
			return
		}
		prCommit, baseline, evidence = ev.PrMergeCommit, ev.BaselineMergeCommit, ev.AnchorEvidence
		if !dry {
			if _, rerr := migrate.RecordPrMergeBaseline(root, slug, ctx.String("merge-commit"), target, attest); rerr != nil {
				reason = rerr.Error()
				return
			}
		}
		action = "wrote"
		if dry {
			action = "would_write"
		}
		if ev.AnchorEvidence == migrate.AnchorMergeCommitParentAttested {
			reason = "verified against git: the commit landed on the target branch and introduced the mission corpus; its first parent is the pre-landing target tip by your --attest-first-landing-commit attestation that the merge was performed on the target branch — git cannot prove this for a two-parent landing (an internal merge is graph-identical)"
		} else {
			reason = "verified against git: the commit landed on the target branch and introduced the mission corpus; its first parent is the pre-landing target tip by your --attest-first-landing-commit attestation — git cannot prove this for a single-parent landing"
		}
	}()
	out := ctx.Stdout
	if jsonMode {
		row := jsonx.NewObject("slug", slug, "action", action, "reason", reason, "pr_merge_commit", prCommit,
			"baseline_merge_commit", baseline, "pr_merge_evidence", evidence)
		printJSON(out, jsonx.NewObject("dry_run", dry, "results", []any{row}))
	} else {
		cprintf(out, "\n%s[bold]backfill-merge-commit summary[/bold]", dryPrefix(dry))
		cprintf(out, "  Mission               : %s", slug)
		cprintf(out, "  Action                : %s", action)
		if prCommit != nil {
			cprintf(out, "  PR merge commit       : %v", prCommit)
			cprintf(out, "  baseline_merge_commit : %v", baseline)
			cprintf(out, "  pr_merge_evidence     : %v", evidence)
		}
		cprintf(out, "  Reason                : %s", reason)
		if dry {
			cprint(out, "\n[dim]Dry run — no files were modified.[/dim]")
		}
	}
	if action == "error" {
		return cli.Exit(1)
	}
	return nil
}
