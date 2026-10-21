package planning

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/decision"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Top-level lifecycle wrappers (cli/commands/lifecycle.py): specify, plan
// and tasks delegate to `agent mission create`, `agent mission setup-plan`
// and `agent mission finalize-tasks`, then run the non-interactive
// widen interviews.

const specifyTopologyHelp = "Create-time mission shape: single_branch | lanes | coord | lanes_with_coord. Coordination-bearing shapes (coord, lanes_with_coord) mint a coordination branch; branch-flat shapes (single_branch, lanes) do not. Default: context-derived (#2581, #2602) — coord on the primary branch or with --pr-bound when coordination is reachable; lanes otherwise. single_branch only when requested explicitly (or via --owned-checkout)."

var (
	specifyQuestions = [][2]string{
		{"problem_statement", "What problem does this Mission solve?"},
		{"success_criteria", "How will we know this Mission is successful?"},
		{"scope_boundaries", "What is explicitly out of scope for this Mission?"},
	}
	planQuestions = [][2]string{
		{"approach", "What is the high-level implementation approach?"},
		{"risks", "What are the main risks or unknowns?"},
		{"dependencies", "What upstream dependencies does this plan rely on?"},
	}
)

func init() {
	addCommands(func(root, _, _, _ *cli.Command) {
		root.Add(&cli.Command{
			Name:  "specify",
			Short: "Create a feature scaffold in kitty-specs/.",
			Args:  []*cli.Arg{{Name: "mission", Metavar: "{mission}", Help: "Mission name or slug (e.g., user-authentication)", Required: true}},
			Flags: []*cli.Flag{
				{Name: "mission-type", Kind: cli.String, Metavar: "<str>", Help: "Mission type (e.g., software-dev, research)"},
				{Name: "mission", Kind: cli.String, Hidden: true, Help: "(deprecated) Use --mission-type"},
				{Name: "topology", Kind: cli.String, Metavar: "<single_branch|lanes|coord|lanes_with_coord>", Choices: []string{"single_branch", "lanes", "coord", "lanes_with_coord"}, Help: specifyTopologyHelp},
				{Name: "json", Kind: cli.Bool, Help: "Emit JSON result"},
			},
			Run: runSpecify,
		})
		root.Add(&cli.Command{
			Name:  "plan",
			Short: "Scaffold plan.md for a Mission.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (e.g., 001-user-authentication)"},
				{Name: "json", Kind: cli.Bool, Help: "Emit JSON result"},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedCheckoutHelp},
			},
			Run: runPlan,
		})
		root.Add(&cli.Command{
			Name:  "tasks",
			Short: "Finalize tasks metadata after task generation.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (e.g., 001-user-authentication)"},
				{Name: "json", Kind: cli.Bool, Help: "Emit JSON result"},
			},
			Run: runTasks,
		})
	})
}

// runSub invokes a registered command (path from the root) the way upstream
// calls the typer function directly: no group callbacks, own argv.
func runSub(ctx *cli.Context, stdout io.Writer, path []string, argv []string) int {
	top := ctx
	for top.Parent != nil {
		top = top.Parent
	}
	target := top.Cmd.Lookup(path...)
	if target == nil {
		fmt.Fprintf(ctx.Stderr, "Error: command %s is not available\n", strings.Join(path, " "))
		return 1
	}
	cp := *target
	mini := &cli.Command{Name: "spec-kitty"}
	mini.Add(&cp)
	return cli.Execute(mini, append([]string{cp.Name}, argv...), cli.IO{Stdin: ctx.Stdin, Stdout: stdout, Stderr: ctx.Stderr})
}

// canonicalRoot is core.paths.resolve_canonical_root ("" = not found).
func canonicalRoot(start string) string {
	if r, err := filepath.EvalSymlinks(start); err == nil {
		start = r
	}
	ceilings := map[string]bool{}
	for _, p := range filepath.SplitList(os.Getenv("GIT_CEILING_DIRECTORIES")) {
		if p != "" {
			if r, err := filepath.Abs(p); err == nil {
				if rr, err := filepath.EvalSymlinks(r); err == nil {
					r = rr
				}
				ceilings[r] = true
			}
		}
	}
	for cand := start; ; {
		g := filepath.Join(cand, ".git")
		if st, err := os.Stat(g); err == nil {
			if st.IsDir() {
				return cand
			}
			if m := project.MainRepoRoot(cand); m != cand {
				return m
			}
			if st, err := os.Stat(filepath.Join(cand, ".kittify")); err == nil && st.IsDir() {
				return cand
			}
		} else if ceilings[cand] {
			break
		}
		parent := filepath.Dir(cand)
		if parent == cand {
			break
		}
		cand = parent
	}
	return ""
}

// enforceInitialized is lifecycle._enforce_initialized (assert_initialized).
func enforceInitialized(ctx *cli.Context, requireSpecs, jsonMode bool) error {
	root := canonicalRoot(cwd())
	resolved := root
	if resolved == "" {
		resolved = cwd()
		if r, err := filepath.EvalSymlinks(resolved); err == nil {
			resolved = r
		}
	}
	var missing []string
	cfg := filepath.Join(resolved, ".kittify", "config.yaml")
	specs := filepath.Join(resolved, "kitty-specs")
	if root == "" {
		missing = append(missing, cfg)
		if requireSpecs {
			missing = append(missing, specs)
		}
	} else {
		if _, err := os.Stat(cfg); err != nil {
			missing = append(missing, cfg)
		}
		if st, err := os.Stat(specs); requireSpecs && (err != nil || !st.IsDir()) {
			missing = append(missing, specs)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var bullets []string
	for _, m := range missing {
		bullets = append(bullets, "  - "+m)
	}
	msg := "SPEC_KITTY_REPO_NOT_INITIALIZED: Spec Kitty is not initialized at the resolved repo root.\n  Resolved root: " + resolved +
		"\n  Missing:\n" + strings.Join(bullets, "\n") + "\nFix: cd into a Spec Kitty project, or run `spec-kitty init` from the desired project root."
	if jsonMode {
		printCompact(ctx.Stdout, jsonx.NewObject("error_code", "SPEC_KITTY_REPO_NOT_INITIALIZED", "error", msg))
	} else {
		say(ctx.Stdout, "[red]Error:[/red] "+msg)
	}
	return cli.Exit(1)
}

var nonSlugRun = regexp.MustCompile(`[^a-z0-9]+`)

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// slugifyFeatureInput is lifecycle._slugify_feature_input; the error text
// is the GuardedReadError reason.
func slugifyFeatureInput(v string) (string, string) {
	st := strings.TrimSpace(v)
	if st == "" {
		return "", "no name given"
	}
	bad := fmt.Sprintf("name %s has no usable ASCII characters", mission.PyRepr(v))
	if !isASCII(st) {
		return "", bad
	}
	slug := strings.Trim(nonSlugRun.ReplaceAllString(strings.ToLower(st), "-"), "-")
	if slug == "" {
		return "", bad
	}
	return slug, ""
}

func scaffoldNextAction(slug string) string {
	return "Open spec_file and replace the scaffold with a complete specification; then run `spec-kitty plan --mission " + slug + "`."
}

func runSpecify(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	if err := enforceInitialized(ctx, false, jsonMode); err != nil {
		return err
	}
	slug, bad := slugifyFeatureInput(ctx.Arg("mission"))
	if bad != "" {
		fmt.Fprintln(ctx.Stderr, "Error: "+bad)
		return cli.Exit(1)
	}
	argv := []string{}
	if ctx.IsSet("mission-type") || ctx.IsSet("mission") {
		canon, alias := strings.TrimSpace(ctx.String("mission-type")), strings.TrimSpace(ctx.String("mission"))
		switch {
		case canon != "" && alias != "" && canon != alias:
			return cli.Usage(ctx.Cmd, "Invalid value: Conflicting selectors: --mission-type=%s and --mission=%s were both provided with different values. --mission is a hidden deprecated alias for --mission-type; pass only --mission-type.", mission.PyRepr(canon), mission.PyRepr(alias))
		case alias != "":
			if os.Getenv("SPEC_KITTY_SUPPRESS_MISSION_TYPE_DEPRECATION") != "1" {
				say(ctx.Stderr, "[yellow]Warning:[/yellow] --mission is deprecated; use --mission-type. See: docs/migrations/mission-type-flag-deprecation.md")
			}
			if canon == "" {
				canon = alias
			}
		}
		if canon != "" {
			argv = append(argv, "--mission-type", canon)
		}
	}
	if ctx.IsSet("topology") {
		argv = append(argv, "--topology", ctx.String("topology"))
	}
	createPath := []string{"agent", "mission", "create"}
	if jsonMode {
		var buf bytes.Buffer
		code := runSub(ctx, &buf, createPath, append(append(argv, "--json", "--"), slug))
		if code != 0 {
			io.WriteString(ctx.Stdout, buf.String())
			return cli.Exit(code)
		}
		for _, line := range strings.Split(buf.String(), "\n") {
			st := strings.TrimSpace(line)
			if !strings.HasPrefix(st, "{") {
				continue
			}
			payload, err := jsonx.DecodeObject([]byte(st))
			if err != nil {
				continue
			}
			e := payload.Clone()
			s := e.GetString("mission_slug")
			if s == "" {
				s = slug
			}
			e.Set("scaffold_only", true)
			e.Set("spec_state", "scaffold_only")
			e.Set("next_action", scaffoldNextAction(s))
			e.Set("next_step", scaffoldNextAction(s))
			printCompact(ctx.Stdout, e)
			return nil
		}
		io.WriteString(ctx.Stdout, buf.String())
		return nil
	}
	if code := runSub(ctx, ctx.Stdout, createPath, append(append(argv, "--"), slug)); code != 0 {
		return cli.Exit(code)
	}
	say(ctx.Stdout, "[yellow]Scaffold-only:[/yellow] spec.md was created as a scaffold; no complete spec was authored.")
	say(ctx.Stdout, "[cyan]Next:[/cyan] "+scaffoldNextAction(slug))
	if root := project.Locate(""); root != "" {
		dirName := slug
		if name, err := mission.ResolveHandle(root, slug); err == nil {
			dirName = name
		}
		return runInterview(ctx, "specify", specifyQuestions, root, dirName, slug)
	}
	return nil
}

const missionRequiredMsg = "--mission <slug> is required"

func requireMission(ctx *cli.Context, jsonMode bool) (string, error) {
	m := strings.TrimSpace(ctx.String("mission"))
	if m == "" {
		if jsonMode {
			printCompact(ctx.Stdout, jsonx.NewObject("error", missionRequiredMsg))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+missionRequiredMsg)
		}
		return "", cli.Exit(2)
	}
	return m, nil
}

func runPlan(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	if err := enforceInitialized(ctx, true, jsonMode); err != nil {
		return err
	}
	slug, err := requireMission(ctx, jsonMode)
	if err != nil {
		return err
	}
	argv := []string{"--mission", slug}
	if jsonMode {
		argv = append(argv, "--json")
	}
	owned := ctx.String("owned-checkout")
	if ctx.IsSet("owned-checkout") {
		argv = append(argv, "--owned-checkout", owned)
	}
	if code := runSub(ctx, ctx.Stdout, []string{"agent", "mission", "setup-plan"}, argv); code != 0 {
		return cli.Exit(code)
	}
	if jsonMode || ctx.IsSet("owned-checkout") {
		return nil
	}
	root := project.Locate("")
	if root == "" {
		return nil
	}
	dir, derr := detectFeatureDir(root, slug)
	if derr != nil {
		return nil
	}
	name := filepath.Base(dir)
	return runInterview(ctx, "plan", planQuestions, root, name, name)
}

func runTasks(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	if err := enforceInitialized(ctx, true, jsonMode); err != nil {
		return err
	}
	slug, err := requireMission(ctx, jsonMode)
	if err != nil {
		return err
	}
	argv := []string{"--mission", slug}
	if jsonMode {
		argv = append(argv, "--json")
	}
	if code := runSub(ctx, ctx.Stdout, []string{"agent", "mission", "finalize-tasks"}, argv); code != 0 {
		return cli.Exit(code)
	}
	return nil
}

// isInteractive is core.env.is_interactive.
func isInteractive() bool {
	t := func(k string) bool {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	}
	if t("SPEC_KITTY_FORCE_INTERACTIVE") {
		return true
	}
	if t("SPEC_KITTY_NON_INTERACTIVE") {
		return false
	}
	return stdinIsTTY()
}

func interviewActor() string {
	if r, err := gitx.Exec("", nil, nil, "config", "user.email"); err == nil {
		if e := strings.TrimSpace(r.Stdout); e != "" {
			return e
		}
	}
	return "cli"
}

// runInterview is run_specify_interview / run_plan_interview: one decision
// per widen question, answered (or deferred when non-interactive). Every
// decision-service failure is suppressed, as upstream does.
func runInterview(ctx *cli.Context, flow string, questions [][2]string, root, dirName, entrySlug string) error {
	interactive := isInteractive()
	if !interactive {
		say(ctx.Stdout, fmt.Sprintf("[dim]Non-interactive: taking defaults for the %s interview (no prompts; questions recorded as deferred).[/dim]", flow))
	}
	actor := interviewActor()
	dc := &decision.Context{Root: root, Slug: dirName, EntrySlug: entrySlug}
	var in *bufio.Reader
	if interactive {
		in = bufio.NewReader(ctx.Stdin)
	}
	for _, q := range questions {
		id, text := q[0], q[1]
		decisionID := ""
		step := flow + "." + id
		if resp, err := dc.Open(decision.OpenRequest{Flow: flow, InputKey: id, Question: text, Options: []string{}, StepID: &step, Actor: actor}); err == nil {
			decisionID = resp.DecisionID
		}
		answer := ""
		if interactive {
			sayPlain(ctx.Stdout, "[enter]=accept default | [text]=type answer | [d]efer | [!cancel]")
			fmt.Fprintf(ctx.Stdout, "%s []: ", text)
			line, err := in.ReadString('\n')
			if err != nil && line == "" {
				return nil
			}
			answer = strings.TrimRight(line, "\r\n")
		}
		if decisionID == "" {
			continue
		}
		req := decision.TerminalRequest{DecisionID: decisionID, Actor: actor}
		switch a := strings.ToLower(strings.TrimSpace(answer)); {
		case a == "!cancel":
			r := "owner canceled during " + flow + " interview"
			req.Status, req.Rationale = decision.StatusCanceled, &r
		case a != "":
			fa := answer
			req.Status, req.FinalAnswer = decision.StatusResolved, &fa
		default:
			r := "owner deferred during " + flow + " interview"
			req.Status, req.Rationale = decision.StatusDeferred, &r
		}
		dc.Terminal(req)
	}
	return nil
}
