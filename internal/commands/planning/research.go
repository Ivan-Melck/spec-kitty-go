package planning

import (
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/assetpreserve"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// research (cli/commands/research.py): scaffold research artifacts from the
// mission type's shipped templates behind the destructive-overwrite guard.

func init() {
	addCommands(func(root, _, _, _ *cli.Command) {
		root.Add(&cli.Command{
			Name:  "research",
			Short: "Scaffold research artifacts from the mission type's shipped templates.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug to target"},
				{Name: "force", Kind: cli.Bool, Help: "Overwrite existing research artifacts"},
				{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: ownedCheckoutHelp},
			},
			Run: runResearch,
		})
	})
}

var missionChoices = map[string]string{"software-dev": "Software Dev Kitty", "research": "Deep Research Kitty"}

// --- plan_validation -------------------------------------------------------

var planTemplateMarkers = []string{"[FEATURE]", "[###-feature-name]", "[DATE]", "[link]", "[Extract from feature spec:",
	"ACTION REQUIRED: Replace the content", "[e.g., Python 3.11", "or NEEDS CLARIFICATION", "# [REMOVE IF UNUSED]",
	"[Gates determined based on charter file]", "[Document the selected structure"}

// planUnfilledError is validate_plan_filled(strict=True) ("" = filled).
func planUnfilledError(planPath, slug string) string {
	b, err := os.ReadFile(planPath)
	if err != nil || !utf8.Valid(b) {
		return ""
	}
	content := strings.TrimPrefix(string(b), "\ufeff")
	var found []string
	for _, m := range planTemplateMarkers {
		if strings.Contains(content, m) {
			found = append(found, m)
		}
	}
	if len(found) < 5 {
		return ""
	}
	disp := ""
	if slug != "" {
		disp = fmt.Sprintf(" for mission '%s'", slug)
	}
	more := ""
	if len(found) > 5 {
		more = fmt.Sprintf("\n  ... and %d more", len(found)-5)
	}
	return fmt.Sprintf("plan.md%s appears to be unfilled (template form).\nFound %d template markers:\n  - %s%s\n\nPlease complete the /spec-kitty.plan workflow inside your coding agent (Claude Code, Codex, Cursor) before proceeding to research or tasks.\nThe plan.md file must have technical details filled in, not just template placeholders.",
		disp, len(found), strings.Join(found[:5], "\n  - "), more)
}

// --- guard_destructive_overwrite (no prover, no backup) ----------------------

type overwriteVerdict struct {
	proceed            bool
	reason, diagnostic string
}

func displayRel(path, root string) string {
	if r, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.Base(path)
}

// guardDestructiveOverwrite routes through assetpreserve exactly like
// upstream's research / brief call sites: no prover, no backup parent.
func guardDestructiveOverwrite(dest, root string, substantive, authorized bool) overwriteVerdict {
	v, _ := assetpreserve.GuardDestructiveOverwrite(dest, root, substantive, authorized, nil, "")
	return overwriteVerdict{v.Proceed, v.Reason, v.Diagnostic}
}

// --- StepTracker -------------------------------------------------------------

type stepTracker struct{ t *rich.StepTracker }

func newStepTracker(title string) *stepTracker     { return &stepTracker{rich.NewStepTracker(title)} }
func (t *stepTracker) add(key, label string)       { t.t.Add(key, label) }
func (t *stepTracker) start(key string)            { t.t.Start(key, "") }
func (t *stepTracker) complete(key, detail string) { t.t.Complete(key, detail) }
func (t *stepTracker) fail(key, detail string)     { t.t.Error(key, detail) }
func (t *stepTracker) skip(key, detail string)     { t.t.Skip(key, detail) }

// render is console.print(tracker.render()).
func (t *stepTracker) render(w io.Writer) { rich.PrintTo(w, t.t.Tree()) }

// paddedPanel is console.print(Panel(content, title=..., border_style=...,
// padding=(1, 2))).
func paddedPanel(w io.Writer, content, title, border string) {
	rich.PrintTo(w, &rich.RichPanel{R: content, Title: title, BorderStyle: border, Padding: []int{1, 2}})
}

// --- command -----------------------------------------------------------------

var researchAssets = map[string]string{
	"research.md":                  "research-template.md",
	"data-model.md":                "data-model-template.md",
	"research/evidence-log.csv":    "research/evidence-log.csv",
	"research/source-register.csv": "research/source-register.csv",
}

type assetOutcome struct {
	rel                string
	created            bool
	diagnostic, reason string
}

func writeResearchAsset(rel, planningDir, root, missionType string, force bool) (assetOutcome, error) {
	dest := filepath.Join(planningDir, rel)
	mt := missionType
	if mt == "" {
		mt = "software-dev"
	}
	tmpl, ok := missionart.ResolveTemplate(researchAssets[rel], root, mt)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return assetOutcome{}, err
	}
	v := guardDestructiveOverwrite(dest, root, ok, force)
	if !v.proceed {
		return assetOutcome{rel, false, v.diagnostic, v.reason}, nil
	}
	if err := os.WriteFile(dest, tmpl, 0o644); err != nil {
		return assetOutcome{}, err
	}
	return assetOutcome{rel, true, v.diagnostic, v.reason}, nil
}

func preservedExisting(skipped []assetOutcome) bool {
	for _, s := range skipped {
		if s.reason == "unauthorized" || s.reason == "would-truncate-to-empty" {
			return true
		}
	}
	return false
}

// findRepoRoot is task_utils.find_repo_root ("" = not found).
func findRepoRoot() string {
	if r := project.Locate(""); r != "" {
		return r
	}
	start := cwd()
	for cand := start; ; {
		g := filepath.Join(cand, ".git")
		if st, err := os.Stat(g); err == nil {
			if st.IsDir() {
				if _, err := os.Stat(filepath.Join(g, "HEAD")); err == nil {
					return project.MainRepoRoot(cand)
				}
			} else if m := project.MainRepoRoot(cand); m != cand {
				return m
			}
		}
		parent := filepath.Dir(cand)
		if parent == cand {
			return ""
		}
		cand = parent
	}
}

func runResearch(ctx *cli.Context) error {
	out := ctx.Stdout
	repoRoot := findRepoRoot()
	if repoRoot == "" {
		say(out, "[red]Error:[/red] Unable to locate repository root (missing .git or .kittify).")
		return cli.Exit(1)
	}
	// #5882: honour an owned checkout (explicit --owned-checkout or flagless
	// adoption); every mission dir then resolves from the owned root.
	owned, oerr := resolveOwnedOrRefuse(ctx, repoRoot, ctx.String("mission"), false, ownedcheckout.SuccessFalseEnvelope, ownedcheckout.Options{})
	if oerr != nil {
		if _, ok := oerr.(*ownedcheckout.MetaReadError); ok {
			say(out, "[red]Error:[/red] "+oerr.Error())
			return cli.Exit(1)
		}
		return oerr
	}
	projectRoot := ""
	if owned != nil {
		repoRoot = owned.OwnedRoot
		ownedcheckout.EchoStaleCopyWarning(ctx.Stderr, owned)
		projectRoot = owned.OwnedRoot
	} else {
		projectRoot = project.Locate(repoRoot)
	}
	if projectRoot == "" {
		say(out, "[red]Error:[/red] Unable to locate the Spec Kitty project root (.kittify directory not found).")
		say(out, "[dim]Run this command from the project root or from a mission worktree under .worktrees/<mission>/.[/dim]")
		say(out, "[dim]Tip: Initialize a project with 'spec-kitty init <name>' if one does not exist.[/dim]")
		return cli.Exit(1)
	}
	t := newStepTracker("Research Phase Setup")
	t.add("project", "Locate project root")
	t.add("feature", "Resolve mission directory")
	t.add("research-md", "Ensure research.md")
	t.add("data-model", "Ensure data-model.md")
	t.add("research-csv", "Ensure research CSV stubs")
	t.add("summary", "Summarize outputs")
	fmt.Fprintln(out)
	t.start("project")
	t.complete("project", projectRoot)
	t.start("feature")
	handle := strings.TrimSpace(ctx.String("mission"))
	if handle == "" {
		return cli.Usage(ctx.Cmd, "Invalid value: --mission <slug> is required")
	}
	if msg := unsafeSegment(handle); msg != "" {
		say(out, "[red]Error:[/red] Mission slug is not a single safe path segment: "+msg)
		return cli.Exit(2)
	}
	name := handle
	if owned != nil {
		name = owned.MissionSlug
	} else if r, err := mission.ResolveHandle(repoRoot, handle); err == nil {
		name = r
	} else if _, ok := err.(*mission.SelectorAmbiguousError); ok {
		say(out, "[red]Error:[/red] "+err.Error())
		return cli.Exit(2)
	}
	featureDir := coord.Resolve(repoRoot, name).StatusDir()
	if owned != nil {
		featureDir = owned.MissionDir
	}
	if st, err := os.Stat(featureDir); err != nil || !st.IsDir() {
		say(out, "[red]Error:[/red] Mission not found: "+handle)
		return cli.Exit(1)
	}
	slug := filepath.Base(featureDir)
	planningDir := project.Specs(repoRoot, slug)
	os.MkdirAll(planningDir, 0o755)
	missionType := ""
	if meta := mission.ReadMetaOrEmpty(planningDir); meta != nil {
		missionType = meta.GetString("mission_type")
		if missionType == "" {
			missionType = meta.GetString("mission")
		}
	}
	display := missionType
	if d, ok := missionChoices[missionType]; ok {
		display = d
	}
	t.complete("feature", fmt.Sprintf("%s (%s)", planningDir, display))
	if msg := planUnfilledError(filepath.Join(planningDir, "plan.md"), slug); msg != "" {
		t.render(out)
		fmt.Fprintln(out)
		say(out, "[red]Error:[/red] "+msg)
		fmt.Fprintln(out)
		say(out, "[yellow]Next steps:[/yellow]")
		say(out, "  1. Run [cyan]/spec-kitty.plan[/cyan] inside your coding agent (Claude Code, Codex, Cursor) to fill in the technical architecture")
		say(out, "  2. Complete all [FEATURE], [DATE], and technical context placeholders")
		say(out, "  3. Remove [REMOVE IF UNUSED] sections and choose your project structure")
		say(out, fmt.Sprintf("  4. Then run [cyan]spec-kitty research --mission %s[/cyan] again", slug))
		return cli.Exit(1)
	}
	force := ctx.Bool("force")
	var created []string
	var skipped []assetOutcome
	copyAsset := func(step, label, rel string) error {
		t.start(step)
		o, err := writeResearchAsset(rel, planningDir, projectRoot, missionType, force)
		if err != nil {
			t.fail(step, err.Error())
			t.render(out)
			return cli.Exit(1)
		}
		if o.created {
			created = append(created, rel)
			t.complete(step, label)
		} else {
			skipped = append(skipped, o)
			t.skip(step, o.diagnostic)
		}
		return nil
	}
	if err := copyAsset("research-md", "research.md ready", "research.md"); err != nil {
		return err
	}
	if err := copyAsset("data-model", "data-model.md ready", "data-model.md"); err != nil {
		return err
	}
	t.start("research-csv")
	var csvErrors []string
	var csvSkipped []assetOutcome
	csvCreated := 0
	for _, rel := range []string{"research/evidence-log.csv", "research/source-register.csv"} {
		o, err := writeResearchAsset(rel, planningDir, projectRoot, missionType, force)
		if err != nil {
			csvErrors = append(csvErrors, rel+": "+err.Error())
			continue
		}
		if o.created {
			created = append(created, rel)
			csvCreated++
		} else {
			csvSkipped = append(csvSkipped, o)
		}
	}
	skipped = append(skipped, csvSkipped...)
	switch {
	case len(csvErrors) > 0:
		t.fail("research-csv", strings.Join(csvErrors, "; "))
		t.render(out)
		return cli.Exit(1)
	case csvCreated > 0:
		t.complete("research-csv", fmt.Sprintf("%d CSV template(s) ready", csvCreated))
	case preservedExisting(csvSkipped):
		t.skip("research-csv", "Existing research CSV template(s) preserved; re-run with --force to overwrite")
	default:
		t.skip("research-csv", fmt.Sprintf("No research CSV template for mission type '%s'", display))
	}
	t.start("summary")
	switch {
	case len(created) > 0:
		t.complete("summary", fmt.Sprintf("%d artifact(s) ready", len(created)))
	case preservedExisting(skipped):
		t.skip("summary", "Existing research artifact(s) preserved (not overwritten) — re-run with --force")
	default:
		t.skip("summary", fmt.Sprintf("No research template for mission type '%s' — nothing created", display))
	}
	t.render(out)
	fmt.Fprintln(out)
	skipLines := func() string {
		var ls []string
		for _, s := range skipped {
			ls = append(ls, fmt.Sprintf("- [yellow]%s[/yellow]: %s", s.rel, s.diagnostic))
		}
		return strings.Join(ls, "\n")
	}
	if len(created) > 0 {
		uniq := map[string]bool{}
		var rels []string
		for _, c := range created {
			if !uniq[c] {
				uniq[c] = true
				rels = append(rels, c)
			}
		}
		sort.Strings(rels)
		var ls []string
		for _, r := range rels {
			ls = append(ls, "- [cyan]"+r+"[/cyan]")
		}
		summary := strings.Join(ls, "\n")
		if len(skipped) > 0 {
			summary += "\n\n[yellow]Preserved / skipped:[/yellow]\n" + skipLines()
		}
		paddedPanel(out, summary, "Research Artifacts", "cyan")
	} else {
		headline := fmt.Sprintf("No research template for mission type '%s'. Nothing was created; any existing files were preserved unchanged.", display)
		if preservedExisting(skipped) {
			headline = "Existing research artifacts preserved; they are not proven package-owned and [cyan]--force[/cyan] was not given, so they were not overwritten. Re-run with [cyan]--force[/cyan] to overwrite them."
		}
		body := skipLines()
		if body == "" {
			body = "No assets processed."
		}
		paddedPanel(out, headline+"\n\n"+body, "Research Artifacts", "yellow")
	}
	fmt.Fprintln(out)
	return nil
}
