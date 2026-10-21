package runtime

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
)

const (
	modelHelp   = "Dispatch-resolved model asserted against the correlated Op record (requires --invocation-id; never the frontmatter recommendation)"
	profileHelp = "Agent profile id — a dispatch registry / Op record profile or a local charter profile (the same ids `agent profile show` resolves). When omitted, the work package's frontmatter agent_profile is used."
	invHelp     = "Correlated Op record ULID whose mission, WP, action, profile, and model are authoritative"
	ownedHelp   = "Not yet supported. Refused: owned checkouts use 'spec-kitty next --owned-checkout' and 'spec-kitty agent tasks move-task --owned-checkout' instead."
)

const implementLong = `Display work package prompt with implementation instructions.

This command outputs the full work package prompt content so agents can
immediately see what to implement, without navigating the file system.

Automatically moves WP from planned to in_progress (requires --agent to track who is working).

Examples:
    spec-kitty agent action implement WP01 --agent claude
    spec-kitty agent action implement WP02 --agent claude
    spec-kitty agent action implement wp01 --agent codex
    spec-kitty agent action implement --agent gemini  # auto-detects first planned WP`

const reviewLong = `Display work package prompt with review instructions.

This command outputs the full work package prompt (including any review
feedback from previous reviews) so agents can review the implementation.

Automatically moves WP from for_review to in_review (requires --agent to track who is reviewing).

Examples:
    spec-kitty agent action review WP01 --agent claude
    spec-kitty agent action review wp02 --agent codex
    spec-kitty agent action review --agent gemini  # auto-detects first for_review WP`

func init() {
	app.Register(func(root *cli.Command) {
		agent := app.Group(root, "agent", agentHelp)
		action := app.Group(agent, "action", "Mission action commands that display prompts and instructions for agents")
		action.NoArgsIsError = true
		action.Add(&cli.Command{
			Name: "implement", Short: "Display work package prompt with implementation instructions.", Long: implementLong,
			Args: []*cli.Arg{{Name: "wp_id", Metavar: "wp_id", Help: "Work package ID (e.g., WP01, wp01, WP01-slug) - auto-detects first planned if omitted"}},
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "agent", Kind: cli.String, Metavar: "<str>", Help: "Agent name (required for auto-move to in_progress). With --force, name the agent that will implement: it is recorded as the implementer of record."},
				{Name: "model", Kind: cli.String, Metavar: "<str>", Help: modelHelp},
				{Name: "profile", Kind: cli.String, Metavar: "<str>", Help: profileHelp},
				{Name: "invocation-id", Kind: cli.String, Metavar: "<str>", Help: invHelp},
				{Name: "force", Kind: cli.Bool, Help: "Operator override: take a WP out of for_review, in_review or approved. Requires --note. Refused for a WP in any other lane."},
				{Name: "note", Kind: cli.String, Metavar: "<str>", Help: "Why the review lane is being left; recorded in the status event. Only valid with --force."},
				{Name: "allow-sparse-checkout", Kind: cli.Bool, Help: "Proceed even if legacy sparse-checkout state is detected. Use of this override is logged. Does not bypass the commit-time data-loss backstop."},
				{Name: "acknowledge-not-bulk-edit", Kind: cli.Bool, Help: "Suppress the bulk-edit inference warning when spec language resembles a bulk edit but the mission is not one."},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedHelp},
			},
			Run: runActionImplement,
		}, &cli.Command{
			Name: "review", Short: "Display work package prompt with review instructions.", Long: reviewLong,
			Args: []*cli.Arg{{Name: "wp_id", Metavar: "wp_id", Help: "Work package ID (e.g., WP01) - auto-detects first for_review if omitted"}},
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug"},
				{Name: "agent", Kind: cli.String, Metavar: "<str>", Help: "Agent name (required for auto-move to in_progress)"},
				{Name: "model", Kind: cli.String, Metavar: "<str>", Help: modelHelp},
				{Name: "profile", Kind: cli.String, Metavar: "<str>", Help: profileHelp},
				{Name: "invocation-id", Kind: cli.String, Metavar: "<str>", Help: invHelp},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedHelp},
			},
			Run: runActionReview,
		})
	})
}

// findMissionSlug is workflow._find_mission_slug.
func findMissionSlug(ctx *cli.Context, root, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		ctx.Println("Error: --mission <slug> is required")
		return "", cli.Exit(1)
	}
	if st, err := os.Stat(project.Specs(root, raw)); err == nil && st.IsDir() {
		return raw, nil
	}
	return resolveMission(ctx.Stdout, ctx.Stderr, root, raw, false)
}

// profileVersion is workflow._resolved_profile_version.
func profileVersion(root string) func(string) (string, error) {
	return func(id string) (string, error) {
		cat := doctrine.LoadProfileCatalog(root)
		p := cat.Resolve(id)
		if p == nil {
			ids := cat.IDs()
			sort.Strings(ids)
			var quoted []string
			for _, x := range ids {
				quoted = append(quoted, "'"+x+"'")
			}
			return "", fmt.Errorf("Profile '%s' not found. Available: [%s]", id, strings.Join(quoted, ", "))
		}
		for _, k := range []string{"schema-version", "schema_version"} {
			if v, ok := p.Data.Get(k); ok && v != nil {
				return status.PyStr(v), nil
			}
		}
		return "1.0", nil
	}
}

// actionBinding collects the dispatch binding options shared by
// implement/review; they are resolved at the claim point.
func actionBinding(ctx *cli.Context, root string) *workflow.BindingRequest {
	return &workflow.BindingRequest{Model: ctx.OptString("model"), Profile: ctx.OptString("profile"),
		InvocationID: ctx.OptString("invocation-id"), ProfileVersion: profileVersion(root)}
}

func runActionImplement(ctx *cli.Context) error {
	o := workflow.NewOut(ctx.Stdout, ctx.Stderr)
	if ctx.IsSet("owned-checkout") {
		ctx.Eprintf("Error: --owned-checkout is not supported by this build.\n")
		return cli.Exit(1)
	}
	force := ctx.Bool("force")
	note := ctx.OptString("note")
	if force {
		if note == nil || strings.TrimSpace(*note) == "" {
			o.Print("Error: " + status.ForceNoteRequired)
			return cli.Exit(1)
		}
	} else if note != nil {
		o.Print("Error: --note only applies together with --force")
		return cli.Exit(1)
	}
	agent := ctx.String("agent")
	if force && agent == "" {
		o.Print("Error: --force requires --agent (it becomes the implementer of record)")
		return cli.Exit(1)
	}
	root := locateRoot()
	if root == "" {
		o.Print("Error: Could not locate project root")
		return cli.Exit(1)
	}
	slug, err := findMissionSlug(ctx, root, ctx.String("mission"))
	if err != nil {
		return err
	}
	rc := &workflow.Receipts{}
	err = workflow.ActionImplement(o, rc, root, slug, workflow.ActionImplementOptions{
		Root: root, Mission: slug, WPID: ctx.Arg("wp_id"), Agent: agent, Force: force, Note: note,
		Dispatch: actionBinding(ctx, root),
	}, nil)
	if workflow.IsFixModeDone(err) {
		return nil
	}
	rc.PrintSummary(o, "implement")
	return exitFrom(err)
}

func runActionReview(ctx *cli.Context) error {
	o := workflow.NewOut(ctx.Stdout, ctx.Stderr)
	if ctx.IsSet("owned-checkout") {
		ctx.Eprintf("Error: --owned-checkout is not supported by this build.\n")
		return cli.Exit(1)
	}
	root := locateRoot()
	if root == "" {
		o.Print("Error: Could not locate project root")
		return cli.Exit(1)
	}
	slug, err := findMissionSlug(ctx, root, ctx.String("mission"))
	if err != nil {
		return err
	}
	rc := &workflow.Receipts{}
	err = workflow.ActionReviewDispatch(o, rc, root, slug, ctx.Arg("wp_id"), ctx.String("agent"), nil, actionBinding(ctx, root))
	rc.PrintSummary(o, "review")
	return exitFrom(err)
}
