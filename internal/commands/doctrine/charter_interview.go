package doctrine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
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
	root := repoRoot()
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
			uconsole.Print(ctx.Stderr, "[yellow]Warning:[/yellow] --mission is deprecated; use --mission-type. See: docs/migrations/mission-type-flag-deprecation.md")
		}
	}
	iv := charter.DefaultInterview(mission, profile)
	paradigms, hasP := parseCSVOpt(ctx.OptString("selected-paradigms"))
	directives, hasD := parseCSVOpt(ctx.OptString("selected-directives"))
	tools, hasT := parseCSVOpt(ctx.OptString("available-tools"))
	if !ctx.Bool("defaults") {
		order := charter.MinimalQuestionOrder
		if profile != "minimal" {
			order = charter.QuestionOrder
		}
		in := bufio.NewReader(ctx.Stdin)
		prompt := func(text, def string) string {
			fmt.Fprintf(w, "%s [%s]: ", text, def)
			line, err := in.ReadString('\n')
			line = strings.TrimRight(line, "\r\n")
			if (err != nil && line == "") || line == "" {
				return def
			}
			return line
		}
		answers := &jsonx.Object{}
		for _, q := range order {
			text := charter.QuestionPrompts[q]
			def := iv.Answers.GetString(q)
			cprint(w, "[dim][enter]=accept default | [text]=type answer | [d]efer | [!cancel][/dim]")
			answers.Set(q, prompt(text, def))
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
	warnings, err := charter.PromoteSelections(root, iv)
	if err != nil {
		warnings = []string{"Selection promotion skipped: " + err.Error()}
		if !jsonMode {
			cprint(w, "[yellow]Selection promotion skipped:[/yellow] %s", err.Error())
		}
	}
	rel, _ := filepath.Rel(root, path)
	if jsonMode {
		jsonOut(w, jsonx.NewObject("result", "success", "success", true, "interview_path", rel, "mission", iv.Mission, "profile", iv.Profile,
			"selected_paradigms", strs(nzs(iv.SelectedParadigms)), "selected_directives", strs(nzs(iv.SelectedDirectives)),
			"available_tools", strs(nzs(iv.AvailableTools)), "org_prefill_messages", []any{}, "org_prefill_warning", nil,
			"promotion_warnings", strs(nzs(warnings))), jsonx.Pretty)
		return nil
	}
	cprint(w, "[green]Charter interview answers saved[/green]")
	cprintRaw(w, "Interview file: "+rel)
	cprintRaw(w, "Mission: "+iv.Mission)
	cprintRaw(w, "Profile: "+iv.Profile)
	for _, m := range warnings {
		cprint(w, "[yellow]Selection promotion:[/yellow] %s", m)
	}
	return nil
}

func nzs(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
