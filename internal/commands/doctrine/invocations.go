package doctrine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() {
	app.Register(func(root *cli.Command) {
		pi := app.Group(root, "profile-invocation", "Manage invocation records.")
		pi.Epilog = "Open:  spec-kitty dispatch \"<request>\"\n\nClose: spec-kitty profile-invocation complete --invocation-id <id> --outcome <outcome>"
		pi.Add(&cli.Command{
			Name:  "complete",
			Short: "Close an open invocation record.",
			Long:  "Close an open invocation record. --invocation-id and --outcome are required.\n\nUse --artifact (repeatable) to link output artifacts to this invocation.\nUse --commit (singular) to link the primary git commit produced.\nUse --evidence to promote a file to a Tier 2 evidence artifact.\nNote: --evidence is rejected for records that are not execution records.",
			Flags: []*cli.Flag{
				{Name: "invocation-id", Short: "i", Kind: cli.String, Required: true, Help: "Invocation ULID to close"},
				{Name: "outcome", Kind: cli.String, Required: true, Help: "done | failed | abandoned"},
				{Name: "evidence", Kind: cli.String, Help: "Path to a UTF-8 text evidence file (Tier 2 promotion); checked before the Op closes"},
				{Name: "artifact", Kind: cli.StringList, Help: "Path (repo-relative or absolute) or URL of an artifact produced by this invocation. Repeatable."},
				{Name: "commit", Kind: cli.String, Help: "Git commit SHA most directly produced by this invocation. Singular."},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON payload"},
				ownedCheckoutFlag(opRecordOwnedCheckoutHelp),
			},
			Run: runComplete,
		})
		inv := app.Group(root, "invocations", "Query local invocation records.")
		inv.Add(&cli.Command{
			Name:  "list",
			Short: "List recent invocation records from the local audit log.",
			Long:  "List recent invocation records from the local audit log.\n\n# FR-008 / T031: This command does not open an InvocationRecord at baseline.\n# If a future version of `invocations list` opens an invocation, it should use:\n#   derive_mode(\"invocations.list\")  -> ModeOfWork.QUERY\n# The mapping is reserved in _ENTRY_COMMAND_MODE (modes.py) for enforcement\n# consistency (QUERY mode disallows Tier 2 evidence promotion per FR-009).\n# TODO(future): wire derive_mode(\"invocations.list\") when InvocationRecord is opened here.\n\nRecords are returned newest-first, sorted by ``started_at`` from file\ncontent.  Use ``--profile`` to narrow to one agent profile.  Use\n``--json`` for machine-readable output.",
			Flags: []*cli.Flag{
				{Name: "profile", Short: "p", Kind: cli.String, Help: "Filter by profile ID (reads file content, not filename)"},
				{Name: "limit", Short: "n", Kind: cli.Int, Default: 20, Help: "Maximum number of records to return (default: 20)"},
				{Name: "json", Kind: cli.Bool, Help: "Emit a JSON array instead of a table"},
				ownedCheckoutFlag(opRecordListOwnedCheckoutHelp),
			},
			Run: runInvocationsList,
		})
	})
}

func runComplete(ctx *cli.Context) error {
	id := ctx.String("invocation-id")
	outcome := ctx.String("outcome")
	jsonMode := ctx.Bool("json")
	if outcome != "done" && outcome != "failed" && outcome != "abandoned" {
		return cli.Usage(ctx.Cmd, "Invalid value for --outcome: %s", fmt.Sprintf("invalid outcome %s: must be one of done, failed, abandoned", pyStrRepr(outcome)))
	}
	root, err := repoRootOrCrash(ctx, rich.Frame{Name: "complete_invocation", Line: 167}, rich.Frame{Name: "_get_repo_root", Line: 42})
	if err != nil {
		return err
	}
	// #6564: close the Op in the checkout that holds its record (the invoking
	// one first, or the owned checkout under --owned-checkout) and judge
	// admission there. An Op no checkout holds keeps the not-found error.
	invoking, err := invokingRecordRoot(ctx, root, jsonMode)
	if err != nil {
		return err
	}
	record := invocation.LocateRecordRoot(root, id, invoking)
	if record == "" {
		record = invoking
	}
	artifacts := ctx.Strings("artifact")
	opt := invocation.CompleteOptions{Evidence: ctx.OptString("evidence"), Artifacts: artifacts, Commit: ctx.OptString("commit")}
	completed, commit, err := invocation.Complete(invocation.Roots{Project: root, Record: record}, id, outcome, opt)
	if err != nil {
		var im *invocation.InvalidModeForEvidenceError
		var ac *invocation.AlreadyClosedError
		var wr *invocation.WriteRefusedError
		switch {
		case errors.As(err, &wr):
			return exitOpWriteRefused(ctx, wr, jsonMode)
		case errors.As(err, &im):
			cprintRaw(ctx.Stdout, "[red]Error:[/red] "+im.Error())
			return cli.Exit(2)
		case errors.As(err, &ac):
			if jsonMode {
				fmt.Fprintln(ctx.Stderr, jsonx.Dumps(jsonx.NewObject("error", "already_closed", "invocation_id", id), lineJSON))
			} else {
				cprintRaw(ctx.Stdout, fmt.Sprintf("[red]Error:[/red] Invocation %s is already closed.", id))
			}
			return cli.Exit(1)
		}
		fmt.Fprintln(ctx.Stderr, jsonx.Dumps(jsonx.NewObject("error", "complete_failed", "message", err.Error()), lineJSON))
		return cli.Exit(1)
	}
	if !jsonMode && commit != nil && commit.Warning != "" {
		rich.LogWarning(ctx.Stderr, commit.Warning)
	}
	if !jsonMode && commit != nil && !commit.Committed && commit.Refusal != "" && commit.Refusal != "not a git worktree" && !strings.HasPrefix(commit.Refusal, "protected branch ") {
		rich.LogWarning(ctx.Stderr, fmt.Sprintf("Op record auto-commit failed for %s: %s", id, commit.Refusal))
	}
	if !jsonMode && commit != nil && !commit.Committed {
		if commit.Skipped != "" {
			cprintRaw(ctx.Stdout, fmt.Sprintf("[dim]Op record kept on disk at kitty-ops/%s.jsonl, not committed: %s.[/dim]", id, rich.EscapeMarkup(commit.Skipped)))
		} else {
			ref := commit.Refusal
			if ref == "" {
				ref = "unknown refusal"
			}
			cprintRaw(ctx.Stdout, fmt.Sprintf("[yellow]Warning:[/yellow] Op record auto-commit was refused (%s). The completed event is on disk at kitty-ops/%s.jsonl but uncommitted. Commit the Op record yourself on the branch where it belongs.", rich.EscapeMarkup(ref), id))
		}
	}
	if jsonMode {
		links := []any{}
		for _, a := range artifacts {
			links = append(links, invocation.NormaliseRef(a, record))
		}
		var ev, cm any
		if opt.Evidence != nil {
			ev = completed.EvidenceRef
		}
		if opt.Commit != nil {
			cm = *opt.Commit
		}
		jsonOut(ctx.Stdout, jsonx.NewObject("result", "success", "invocation_id", id, "outcome", outcome, "evidence_ref", ev,
			"artifact_links", links, "commit_link", cm), jsonx.Pretty)
		return nil
	}
	cprint(ctx.Stdout, "[green]✓[/green] Invocation [bold]%s[/bold] closed.", id)
	cprint(ctx.Stdout, "  Outcome: %s", outcome)
	for _, a := range artifacts {
		cprintRaw(ctx.Stdout, "  Artifact: "+invocation.NormaliseRef(a, record))
	}
	if opt.Commit != nil && *opt.Commit != "" {
		cprintRaw(ctx.Stdout, "  Commit: "+*opt.Commit)
	}
	return nil
}

// pyStrRepr is Python repr(str).
func pyStrRepr(s string) string {
	q := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = `"`
	}
	r := strings.ReplaceAll(s, `\`, `\\`)
	if q == "'" {
		r = strings.ReplaceAll(r, "'", `\'`)
	}
	r = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(r, "\n", `\n`), "\r", `\r`), "\t", `\t`)
	return q + r + q
}

func runInvocationsList(ctx *cli.Context) error {
	projectRoot, err := repoRootOrCrash(ctx, rich.Frame{Name: "list_invocations", Line: 381}, rich.Frame{Name: "_get_repo_root", Line: 61})
	if err != nil {
		return err
	}
	// #6564: list the Op records of the checkout the command runs in (the
	// one dispatch and complete write to); under --owned-checkout, those of
	// the owned checkout (#6682). The command only reads.
	root, err := invokingRecordRoot(ctx, projectRoot, ctx.Bool("json"))
	if err != nil {
		return err
	}
	var filter string
	if p := ctx.OptString("profile"); p != nil {
		filter = *p
	}
	recs, legacy := invocation.ListRecords(root, filter, ctx.Int("limit"))
	warnLegacy := func() {
		if legacy > 0 {
			rich.Print(ctx.Stderr, fmt.Sprintf("[yellow]Warning:[/yellow] skipped %d legacy Op record(s) (pre-v2 schema). Run 'spec-kitty upgrade' to migrate kitty-ops records.", legacy))
		}
	}
	if ctx.Bool("json") {
		warnLegacy()
		l := make([]any, len(recs))
		for i, r := range recs {
			l[i] = r
		}
		jsonOut(ctx.Stdout, l, jsonx.Pretty)
		return nil
	}
	warnLegacy()
	if len(recs) == 0 {
		cprint(ctx.Stdout, "[dim]No invocation records found.[/dim]")
		return nil
	}
	t := &rtable{Title: "Recent Invocations", Headers: []string{"Invocation ID", "Profile", "Action", "Status", "Outcome", "Closed By", "Started At"},
		Styles: []string{"dim"}}
	or := func(r *jsonx.Object, k, d string) string {
		v, ok := r.Get(k)
		if !ok || v == nil {
			return d
		}
		s := fmt.Sprint(v)
		if s == "" && d == "-" {
			return d
		}
		return s
	}
	for _, r := range recs {
		id := or(r, "invocation_id", "?")
		if len([]rune(id)) > 12 {
			id = string([]rune(id)[:12]) + "…"
		}
		started := or(r, "started_at", "?")
		if started == "" {
			started = "?"
		}
		if len([]rune(started)) > 19 {
			started = string([]rune(started)[:19])
		}
		st := "yellow"
		if r.GetString("status") == "closed" {
			st = "green"
		}
		t.Rows = append(t.Rows, []string{id, or(r, "profile_id", "?"), or(r, "action", "?"), "[" + st + "]" + or(r, "status", "?") + "[/" + st + "]",
			or(r, "outcome", "-"), or(r, "closed_by", "-"), started})
	}
	t.render(ctx.Stdout, rich.Width())
	return nil
}
