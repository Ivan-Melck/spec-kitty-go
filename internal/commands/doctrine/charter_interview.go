package doctrine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() { registerCharterLeaf(charterInterviewCmd) }

func charterInterviewCmd() *cli.Command {
	return &cli.Command{
		Name:  "interview",
		Short: "Capture charter interview answers for later generation.",
		Flags: []*cli.Flag{
			{Name: "mission-type", Kind: cli.String, Help: "Mission type for charter defaults (default: software-dev)"},
			{Name: "mission", Kind: cli.String, Hidden: true, Help: "(deprecated) Use --mission-type"},
			{Name: "profile", Kind: cli.String, Default: "minimal", Help: "Interview profile: minimal or comprehensive"},
			{Name: "defaults", Kind: cli.Bool, Help: "Use deterministic defaults without prompts"},
			{Name: "selected-paradigms", Kind: cli.String, Help: "Comma-separated paradigm IDs override"},
			{Name: "selected-directives", Kind: cli.String, Help: "Comma-separated directive IDs override"},
			{Name: "available-tools", Kind: cli.String, Help: "Comma-separated tool IDs override"},
			{Name: "narrow-activation", Kind: cli.Bool, Help: "Replace (not append to) activated directives, tactics and paradigms with the selection plus org-required ids"},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON"},
			{Name: "mission-slug", Kind: cli.String, Help: "Mission slug for Decision Moment paper trail (optional)"},
		},
		Run: runCharterInterview,
	}
}

// parseCSVOpt is _parse_csv_option (ok=false when the flag was not given).
func parseCSVOpt(v *string) ([]string, bool) {
	if v == nil {
		return nil, false
	}
	out := []string{}
	for _, p := range strings.Split(*v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out, true
}

func runCharterInterview(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonMode := ctx.Bool("json")
	root := findRepoRoot()
	if root == "" {
		emitCharterError(w, jsonMode, repoRootMissing, "", nil)
		return cli.Exit(1)
	}
	profile := strings.ToLower(strings.TrimSpace(ctx.String("profile")))
	if profile != "minimal" && profile != "comprehensive" {
		emitCharterError(w, jsonMode, "--profile must be 'minimal' or 'comprehensive'", "", nil)
		return cli.Exit(1)
	}
	mission := "software-dev"
	mt, alias := ctx.OptString("mission-type"), ctx.OptString("mission")
	switch {
	case mt != nil && strings.TrimSpace(*mt) != "":
		mission = strings.TrimSpace(*mt)
		if alias != nil && strings.TrimSpace(*alias) != "" && strings.TrimSpace(*alias) != mission {
			emitCharterError(w, jsonMode, fmt.Sprintf("Conflicting selectors: --mission-type=%s and --mission=%s were both provided with different values. --mission is a hidden deprecated alias for --mission-type; pass only --mission-type.", pyStrRepr(mission), pyStrRepr(strings.TrimSpace(*alias))), "", nil)
			return cli.Exit(1)
		}
	case alias != nil && strings.TrimSpace(*alias) != "":
		mission = strings.TrimSpace(*alias)
		if os.Getenv("SPEC_KITTY_SUPPRESS_MISSION_TYPE_DEPRECATION") != "1" {
			rich.Print(ctx.Stderr, "[yellow]Warning:[/yellow] --mission is deprecated; use --mission-type. See: docs/migrations/mission-type-flag-deprecation.md")
		}
	}
	iv := charter.DefaultInterview(mission, profile)
	// FR-026: pre-fill from org charter packs (best-effort, non-fatal).
	orgMsgs, orgErr := charter.ApplyOrgCharterToInterview(root, iv)
	var orgWarning any
	if orgErr != nil {
		orgWarning = orgErr.Error()
		orgMsgs = nil
		if !jsonMode {
			cprint(w, "[yellow]Org charter pre-fill skipped:[/yellow] %s", orgErr.Error())
		}
	} else if !jsonMode {
		for _, m := range orgMsgs {
			cprint(w, "[cyan]Org charter:[/cyan] %s", m)
		}
	}
	in := bufio.NewReader(ctx.Stdin)
	var askLanguages func(string) (string, error)
	if !ctx.Bool("defaults") && interviewInteractive() {
		askLanguages = func(proposal string) (string, error) {
			fmt.Fprintf(w, "Project languages (comma-separated catalog tokens, primary first; 'none' to leave unset) [%s]: ", proposal)
			answer, err := in.ReadString('\n')
			if err != nil && answer == "" {
				return "", fmt.Errorf("")
			}
			answer = strings.TrimRight(answer, "\r\n")
			if answer == "" {
				answer = proposal
			}
			return answer, nil
		}
	}
	languageSettlement, err := charter.SettleProjectLanguages(root, askLanguages, func(message string) { fmt.Fprintln(ctx.Stderr, message) })
	if err != nil {
		emitCharterError(w, jsonMode, err.Error(), "", nil)
		return cli.Exit(1)
	}
	paradigms, hasP := parseCSVOpt(ctx.OptString("selected-paradigms"))
	directives, hasD := parseCSVOpt(ctx.OptString("selected-directives"))
	tools, hasT := parseCSVOpt(ctx.OptString("available-tools"))
	if !ctx.Bool("defaults") {
		order := charter.MinimalQuestionOrder
		if profile != "minimal" {
			order = charter.QuestionOrder
		}
		// typer.prompt: end of input raises click.Abort, which the
		// command's `except Exception` reports as an empty "Unexpected
		// error" (exit 1).
		eof := false
		prompt := func(text, def string) string {
			if eof {
				return def
			}
			fmt.Fprintf(w, "%s [%s]: ", text, def)
			line, err := in.ReadString('\n')
			if err != nil && line == "" {
				eof = true
				return def
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				return def
			}
			return line
		}
		aborted := func() error {
			if jsonMode {
				emitCharterError(w, true, "", "", nil)
			} else {
				cprint(w, "[red]Unexpected error:[/red] ")
			}
			return cli.Exit(1)
		}
		answers := &jsonx.Object{}
		for _, q := range order {
			text := charter.QuestionPrompts[q]
			def := iv.Answers.GetString(q)
			cprint(w, "[dim][enter]=accept default | [text]=type answer | [d]efer | [!cancel][/dim]")
			answers.Set(q, prompt(text, def))
			if eof {
				return aborted()
			}
		}
		pDef, dDef, tDef := strings.Join(iv.SelectedParadigms, ", "), strings.Join(iv.SelectedDirectives, ", "), strings.Join(iv.AvailableTools, ", ")
		if hasP {
			pDef = strings.Join(paradigms, ", ")
		}
		if hasD {
			dDef = strings.Join(directives, ", ")
		}
		if hasT {
			tDef = strings.Join(tools, ", ")
		}
		ps, ds, ts := prompt("Selected paradigms (comma-separated)", pDef), prompt("Selected directives (comma-separated)", dDef), prompt("Available tools (comma-separated)", tDef)
		if eof {
			return aborted()
		}
		paradigms, _ = parseCSVOpt(&ps)
		directives, _ = parseCSVOpt(&ds)
		tools, _ = parseCSVOpt(&ts)
		iv = charter.ApplyOverrides(iv, answers, paradigms, directives, tools, true, true, true)
	} else {
		iv = charter.ApplyOverrides(iv, nil, paradigms, directives, tools, hasP, hasD, hasT)
	}
	path := charter.InterviewPath(root)
	if err := charter.WriteInterview(path, iv); err != nil {
		emitCharterError(w, jsonMode, err.Error(), "", nil)
		return cli.Exit(1)
	}
	wrote, err := charter.StoreLanguageSettlement(root, languageSettlement)
	if err != nil {
		emitCharterError(w, jsonMode, "Could not write the project languages to .kittify/config.yaml: "+err.Error()+". The interview answers were saved and re-running is safe; re-run the interview interactively to re-confirm the languages, because a non-interactive re-run re-detects them instead.", "", nil)
		return cli.Exit(1)
	}
	if languageSettlement.Source == "detected" && !wrote {
		languageSettlement.Languages = charter.ConfiguredLanguages(root)
		languageSettlement.Source = "stored"
		languageSettlement.ToStore = nil
		languageSettlement.Notice = nil
	}
	warnings, narrowed, err := charter.PromoteSelectionsWithOptions(root, iv, ctx.Bool("narrow-activation"))
	if err != nil {
		if ctx.Bool("narrow-activation") {
			emitCharterError(w, jsonMode, "--narrow-activation could not be applied: "+err.Error(), "", nil)
			return cli.Exit(1)
		}
		warnings = []string{"Selection promotion skipped: " + err.Error()}
		if !jsonMode {
			cprint(w, "[yellow]Selection promotion skipped:[/yellow] %s", err.Error())
		}
	}
	rel, _ := filepath.Rel(root, path)
	if jsonMode {
		jsonOut(w, jsonx.NewObject("result", "success", "success", true, "interview_path", rel, "mission", iv.Mission, "profile", iv.Profile,
			"selected_paradigms", strs(nzs(iv.SelectedParadigms)), "selected_directives", strs(nzs(iv.SelectedDirectives)),
			"available_tools", strs(nzs(iv.AvailableTools)), "languages", strs(languageSettlement.Languages), "languages_source", languageSettlement.Source, "languages_notice", languageSettlement.Notice, "org_prefill_messages", strs(nzs(orgMsgs)), "org_prefill_warning", orgWarning,
			"promotion_warnings", strs(nzs(warnings)), "narrowed", narrowed), jsonx.Pretty)
		return nil
	}
	cprint(w, "[green]Charter interview answers saved[/green]")
	cprintRaw(w, "Interview file: "+rel)
	cprintRaw(w, "Mission: "+iv.Mission)
	cprintRaw(w, "Profile: "+iv.Profile)
	if len(languageSettlement.Languages) > 0 {
		cprint(w, "Languages (%s): %s", languageSettlement.Source, strings.Join(languageSettlement.Languages, ", "))
	}
	if languageSettlement.Notice != nil {
		cprint(w, "[yellow]Notice:[/yellow] %s", uconsoleEscape(*languageSettlement.Notice))
	}
	for _, m := range warnings {
		cprint(w, "[yellow]Selection promotion:[/yellow] %s", m)
	}
	for _, kv := range narrowed.Pairs() {
		ids := []string{}
		if values, ok := kv.Value.([]any); ok {
			for _, value := range values {
				if id, ok := value.(string); ok {
					ids = append(ids, id)
				}
			}
		}
		cprint(w, "[yellow]No longer in force (%d) for %s:[/yellow] %s", len(ids), kv.Key, uconsoleEscape(strings.Join(ids, ", ")))
	}
	return nil
}

func interviewInteractive() bool {
	truthy := func(key string) bool {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	}
	if truthy("SPEC_KITTY_FORCE_INTERACTIVE") {
		return true
	}
	if truthy("SPEC_KITTY_NON_INTERACTIVE") {
		return false
	}
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func nzs(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
