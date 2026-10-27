package doctrine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() { registerCharterLeaf(charterGenerateCmd) }

const charterMDSeed = "# Project Charter — curated companion\n\n<!--\nThis file is a hand-authored, display-only companion to the authoritative\n`.kittify/charter/charter.yaml`. It holds the human *rationale* behind your\nproject's governance and doctrine.\n\nIt is NEVER a charter-resolving input, and `charter generate` NEVER overwrites\nit — this starter was written only because the file was absent. Edit it freely.\n-->\n"

func charterGenerateCmd() *cli.Command {
	return &cli.Command{
		Name:  "generate",
		Short: "Generate charter bundle from interview answers + doctrine references.",
		Long:  "Generate charter bundle from interview answers + doctrine references.\n\nBehavior contract (issue #841 / WP06 T029-T030):\n\n- On success in a git working tree, the produced ``.kittify/charter/charter.md``\n  is auto-staged via ``git add`` so a subsequent ``charter bundle validate``\n  finds it tracked without any operator ``git add`` between the two\n  commands. Staging (not committing) matches the parity contract — the\n  ``bundle validate`` tracked-files check keys on ``git ls-files``.\n- When the cwd is not inside a git working tree, ``generate`` exits\n  non-zero before any side effect with an actionable error message that\n  names the remediation (``git init``).\n- When ``.kittify/charter/charter.md`` is a symlink, ``generate`` exits\n  non-zero before interview/default loading, compilation, sync,\n  gitignore updates, or staging. Update the symlink target directly or\n  replace it with a regular runtime charter.",
		Flags: []*cli.Flag{
			{Name: "mission-type", Kind: cli.String, Help: "Mission type for template-set defaults"},
			{Name: "mission", Kind: cli.String, Hidden: true, Help: "(deprecated) Use --mission-type"},
			{Name: "template-set", Kind: cli.String, Help: "Override charter offering template set (must exist in packaged charter offering missions)"},
			{Name: "from-interview", Kind: cli.Bool, Negatable: true, Default: true, Help: "Load interview answers if present"},
			{Name: "profile", Kind: cli.String, Default: "minimal", Help: "Default profile when no interview is available"},
			{Name: "force", Short: "f", Kind: cli.Bool, Help: "Overwrite existing charter bundle"},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON"},
			{Name: "owned-checkout", Kind: cli.String, Metavar: "<path>", Help: "Run against an owned checkout: a linked checkout that owns this mission. Refuses the repository root checkout, lane worktrees and coordination worktrees."},
			{Name: "mission-handle", Kind: cli.String, Help: "Mission identity owned by the explicitly selected checkout"},
		},
		Run: catchCharterLoad(runCharterGenerate),
	}
}

func runCharterGenerate(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonMode := ctx.Bool("json")
	fail := func(msg string) error {
		emitCharterError(w, jsonMode, msg, "", nil)
		return cli.Exit(1)
	}
	if ctx.IsSet("mission-handle") && !ctx.IsSet("owned-checkout") {
		return fail("--mission-handle requires --owned-checkout")
	}
	root := findRepoRoot()
	if root == "" {
		return fail(repoRootMissing)
	}
	if ctx.IsSet("owned-checkout") {
		// Opt in to validated ownership (explicit claim only; no adoption).
		claim := ctx.String("owned-checkout")
		owned, oerr := ownedcheckout.ResolveOrAdopt(root, &claim, ctx.String("mission-handle"), cwd(), ownedcheckout.Options{})
		if oerr != nil {
			if e, ok := oerr.(*ownedcheckout.Error); ok {
				ownedcheckout.Emit(w, ctx.Stderr, e, jsonMode, ownedcheckout.SuccessFalseEnvelope)
				return cli.Exit(1)
			}
			return fail(oerr.Error())
		}
		if err := validateOwnedCharterDestinations(owned.OwnedRoot); err != nil {
			return fail(err.Error())
		}
		root = owned.OwnedRoot
	}
	if out, err := gitx.Run(root, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return fail("charter generate requires a git repository. Initialize one with `git init` (so the produced charter.md can be auto-tracked and accepted by `charter bundle validate`).")
	}
	mdPath := charter.Path(root, charter.CharterMD)
	if fi, err := os.Lstat(mdPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fail(fmt.Sprintf("Refusing to overwrite symlinked charter at %s. Remove the symlink or update the symlink target directly.", mdPath))
	}
	var missionType string
	mt, alias := ctx.OptString("mission-type"), ctx.OptString("mission")
	switch {
	case mt != nil && strings.TrimSpace(*mt) != "":
		missionType = strings.TrimSpace(*mt)
	case alias != nil && strings.TrimSpace(*alias) != "":
		missionType = strings.TrimSpace(*alias)
		if os.Getenv("SPEC_KITTY_SUPPRESS_MISSION_TYPE_DEPRECATION") != "1" {
			rich.Print(ctx.Stderr, "[yellow]Warning:[/yellow] --mission is deprecated; use --mission-type. See: docs/migrations/mission-type-flag-deprecation.md")
		}
	}
	answers := charter.InterviewPath(root)
	rel, _ := filepath.Rel(root, answers)
	var iv *charter.Interview
	source := "interview"
	if ctx.Bool("from-interview") {
		iv = charter.ReadInterview(answers)
		if iv == nil {
			if _, err := os.Stat(answers); err == nil {
				return fail(fmt.Sprintf("Charter interview answers at %s are present but malformed — the file is not readable as a mapping (invalid YAML, or a non-mapping top-level value). Repair or delete it, re-run `spec-kitty charter interview --defaults` to rewrite it, or pass `--no-from-interview` to generate from defaults.", rel))
			}
			return fail(fmt.Sprintf("No charter interview answers found at %s. Run `/spec-kitty.charter` inside your coding agent (Claude Code, Codex, Cursor) so it can capture guidance, run `spec-kitty charter interview --defaults` here for a canned bootstrap, or pass `--no-from-interview` to generate from defaults explicitly.", rel))
		}
	}
	mission := missionType
	if iv == nil {
		source = "defaults"
		if mission == "" {
			mission = "software-dev"
			if prior := charter.ReadInterview(answers); prior != nil && prior.Mission != "" {
				mission = prior.Mission
			}
		}
		iv = charter.DefaultInterview(mission, strings.ToLower(strings.TrimSpace(ctx.String("profile"))))
	} else if mission == "" {
		mission = iv.Mission
	}
	if err := charter.ProvisionMissionTypes(root); err != nil {
		return fail(err.Error())
	}
	c, err := charter.CompileCharterWith(root, mission, iv, ctx.String("template-set"), source == "interview")
	if err != nil {
		var sm *charter.SynthesisMembershipUnreadableError
		if errors.As(err, &sm) && !jsonMode {
			// #6454: not a ValueError, so generate's `except Exception`
			// renders str(exc) (the code) as an unexpected error.
			cprint(w, "[red]Unexpected error:[/red] %s", uconsoleEscape(err.Error()))
			return cli.Exit(1)
		}
		return fail(err.Error())
	}
	files, err := charter.WriteCompiled(root, c)
	if err != nil {
		return fail(err.Error())
	}
	if source == "defaults" {
		os.MkdirAll(charter.Path(root, ".kittify/charter/library"), 0o755)
	}
	if _, err := os.Stat(mdPath); err != nil {
		os.WriteFile(mdPath, []byte(charterMDSeed), 0o644)
	}
	charter.SyncCharterIfPresent(root) // staleness check through the encoding chokepoint
	for _, f := range []string{".kittify/charter/charter.md", ".kittify/charter/charter.yaml", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			continue
		}
		if _, err := gitx.Run(root, "add", "--force", "--", f); err != nil {
			return fail(fmt.Sprintf("Failed to stage charter file %s. %v", f, err))
		}
	}
	mdRel, _ := filepath.Rel(root, mdPath)
	if jsonMode {
		unres := []any{}
		for _, u := range c.Unresolved {
			unres = append(unres, u)
		}
		jsonOut(w, jsonx.NewObject("result", "success", "success", true, "charter_path", mdRel, "interview_source", source,
			"mission", c.Mission, "template_set", c.TemplateSet, "selected_paradigms", strs(c.Paradigms),
			"selected_directives", strs(c.Directives), "available_tools", strs(c.AvailableTools),
			"references_count", len(c.References), "library_files", []any{}, "files_written", strs(files),
			"diagnostics", strs(nzs(c.Diagnostics)), "unresolved_references", unres), jsonx.Pretty)
		return nil
	}
	cprint(w, "[green]Charter generated and synced[/green]")
	cprintRaw(w, "Charter: "+mdRel)
	cprintRaw(w, "Mission: "+c.Mission)
	cprintRaw(w, "Template set: "+c.TemplateSet)
	if len(c.Diagnostics) > 0 {
		cprintRaw(w, "Diagnostics:")
		for _, d := range c.Diagnostics {
			cprintRaw(w, "  - "+d)
		}
	}
	cprintRaw(w, "Files written:")
	for _, f := range files {
		cprintRaw(w, "  ✓ "+f)
	}
	return nil
}

// validateOwnedCharterDestinations is _validate_owned_charter_destinations:
// the whole generator write batch must stay inside P (no symlinked
// ancestors, regular files / directories where they exist).
func validateOwnedCharterDestinations(root string) error {
	config := filepath.Join(root, ".kittify", "config.yaml")
	charterDir := filepath.Join(root, ".kittify", "charter")
	files := []string{config, filepath.Join(root, ".gitignore"), filepath.Join(charterDir, "charter.md"), filepath.Join(charterDir, "charter.yaml")}
	dirs := []string{charterDir, filepath.Join(charterDir, "library")}
	for _, p := range append(append([]string{}, files...), dirs...) {
		if err := assertSafeCharterOutput(p, root); err != nil {
			return err
		}
	}
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && !st.Mode().IsRegular() {
			return fmt.Errorf("Owned charter file destination is not a regular file")
		}
	}
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && !st.IsDir() {
			return fmt.Errorf("Owned charter directory destination is not a directory")
		}
	}
	if st, err := os.Stat(charterDir); err == nil && st.IsDir() {
		var walkErr error
		filepath.Walk(charterDir, func(p string, _ os.FileInfo, err error) error {
			if err == nil && walkErr == nil && p != charterDir {
				walkErr = assertSafeCharterOutput(p, root)
			}
			return nil
		})
		if walkErr != nil {
			return walkErr
		}
	}
	return nil
}

// assertSafeCharterOutput is _assert_safe_charter_output_dir(path,
// repo_root=root): no component below root may be a symlink.
func assertSafeCharterOutput(p, root string) error {
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("Charter output directory %s is outside repository root %s.", p, root)
	}
	cur := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		cur = filepath.Join(cur, part)
		if st, err := os.Lstat(cur); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Charter output path %s is a symlink. Replace it with a normal .kittify/charter directory before running charter generate.", cur)
		}
	}
	return nil
}
