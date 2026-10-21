package doctrine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/glossary"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// glossaryWidth is CliConsole(width=120).
const glossaryWidth = 120

func gprint(w io.Writer, s string) { fmt.Fprintln(w, uconsole.Wrap(uconsole.Strip(s), glossaryWidth)) }
func gprintRaw(w io.Writer, s string) {
	fmt.Fprintln(w, uconsole.Wrap(s, glossaryWidth))
}

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "glossary", "Glossary management commands")
		g.Add(
			&cli.Command{
				Name:  "list",
				Short: "List all terms in glossary.",
				Flags: []*cli.Flag{
					{Name: "scope", Kind: cli.String, Help: "Filter by scope (mission_local, team_domain, audience_domain, spec_kitty_core)"},
					{Name: "status", Kind: cli.String, Help: "Filter by status (active, deprecated, draft)"},
					{Name: "json", Kind: cli.Bool, Help: "Output as JSON (machine-parseable)"},
				},
				Run: runGlossaryList,
			},
			&cli.Command{
				Name:  "conflicts",
				Short: "Display conflict history from event log.",
				Flags: []*cli.Flag{
					{Name: "mission", Kind: cli.String, Help: "Filter conflicts by mission ID"},
					{Name: "unresolved", Kind: cli.Bool, Help: "Show only unresolved conflicts"},
					{Name: "strictness", Kind: cli.String, Help: "Filter by effective strictness level (off, medium, max)"},
					{Name: "json", Kind: cli.Bool, Help: "Output as JSON (machine-parseable)"},
				},
				Run: runGlossaryConflicts,
			},
			&cli.Command{
				Name:  "resolve",
				Short: "Resolve a conflict asynchronously.",
				Flags: []*cli.Flag{{Name: "mission", Kind: cli.String, Help: "Mission ID for event log (auto-detected if omitted)"}},
				Args:  []*cli.Arg{{Name: "conflict_id", Help: "Conflict ID to resolve", Required: true, Metavar: "{conflict_id}"}},
				Run:   runGlossaryResolve,
			},
			&cli.Command{
				Name:  "show",
				Short: "Render the entity page for a glossary term.",
				Args:  []*cli.Arg{{Name: "term", Help: "Term surface name or glossary URN (e.g. 'deployment-target' or 'glossary:deployment-target')", Required: true, Metavar: "{term}"}},
				Run:   runGlossaryShow,
			},
			&cli.Command{
				Name:  "validate",
				Short: "Validate glossary seed file(s) against the schema.",
				Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Output validation results as JSON"}},
				Args:  []*cli.Arg{{Name: "path", Help: "Path to a glossary seed file (.yaml) or directory of seed files", Required: true, Metavar: "{path}"}},
				Run:   runGlossaryValidate,
			},
		)
	})
}

func glossaryError(ctx *cli.Context, jsonMode bool, code, msg string) error {
	if jsonMode {
		jsonError(ctx.Stdout, code, msg)
	} else {
		gprint(ctx.Stdout, "[red]Error: "+noEsc(msg)+"[/red]")
	}
	return cli.Exit(1)
}

func runGlossaryList(ctx *cli.Context) error {
	root := cwd()
	jsonMode := ctx.Bool("json")
	scope, status := ctx.String("scope"), ctx.String("status")
	if scope != "" && !glossary.ValidScope(scope) {
		s := append([]string(nil), glossary.AllScopes...)
		sort.Strings(s)
		return glossaryError(ctx, jsonMode, "invalid_scope", fmt.Sprintf("Invalid scope '%s'. Valid scopes: %s", scope, strings.Join(s, ", ")))
	}
	if status != "" && status != "active" && status != "deprecated" && status != "draft" {
		return glossaryError(ctx, jsonMode, "invalid_status", fmt.Sprintf("Invalid status '%s'. Valid statuses: active, deprecated, draft", status))
	}
	if st, err := os.Stat(filepath.Join(root, ".kittify", "glossaries")); err != nil || !st.IsDir() {
		return glossaryError(ctx, jsonMode, "glossary_not_initialized", "Glossary not initialized. Run 'spec-kitty init' with glossary enabled.")
	}
	all, err := glossary.LoadStore(root)
	if err != nil {
		return glossaryError(ctx, jsonMode, "glossary_load_failed", err.Error())
	}
	terms := glossary.ListTerms(all, scope, status)
	if len(terms) == 0 {
		if jsonMode {
			fmt.Fprintln(ctx.Stdout, "[]")
		} else {
			gprint(ctx.Stdout, "[dim]No terms found[/dim]")
		}
		return nil
	}
	if jsonMode {
		out := []any{}
		for _, t := range terms {
			out = append(out, jsonx.NewObject("surface", t.Surface, "scope", t.Scope, "definition", t.Definition, "status", t.Status, "confidence", t.Confidence))
		}
		jsonOut(ctx.Stdout, out, jsonx.Pretty)
		return nil
	}
	tbl := &rtable{Title: "Glossary Terms", Headers: []string{"Scope", "Term", "Definition", "Status", "Confidence"}, Right: []bool{false, false, false, false, true}}
	for _, t := range terms {
		def := t.Definition
		if r := []rune(def); len(r) > 60 {
			def = string(r[:60]) + "..."
		}
		tbl.Rows = append(tbl.Rows, []string{t.Scope, t.Surface, def, t.Status, strconv.FormatFloat(t.ConfidenceFloat(), 'f', 2, 64)})
	}
	tbl.render(ctx.Stdout, glossaryWidth)
	gprint(ctx.Stdout, fmt.Sprintf("\n[dim]Total: %d term(s)[/dim]", len(terms)))
	return nil
}

// conflictRec is one extracted conflict row.
type conflictRec struct {
	id, term, typ, severity, mission, ts, status, strictness string
}

func (c conflictRec) object() *jsonx.Object {
	return jsonx.NewObject("conflict_id", c.id, "term", c.term, "type", c.typ, "severity", c.severity, "mission_id", c.mission,
		"timestamp", c.ts, "status", c.status, "effective_strictness", c.strictness)
}

func evStr(o *jsonx.Object, k, def string) string {
	v, ok := o.Get(k)
	if !ok {
		return def
	}
	if s, isStr := v.(string); isStr {
		return s
	}
	if v == nil {
		return "None"
	}
	return fmt.Sprint(v)
}

func findingTerm(f *jsonx.Object) string {
	v, _ := f.Get("term")
	if o, ok := v.(*jsonx.Object); ok {
		return evStr(o, "surface_text", "unknown")
	}
	return fmt.Sprint(v)
}

// extractConflicts is _extract_conflicts_from_events.
func extractConflicts(events []*jsonx.Object, mission string, unresolved bool, strictness string) []conflictRec {
	var out []conflictRec
	resolved := map[string]bool{}
	type key struct{ step, term string }
	findings := map[key]*jsonx.Object{}
	checks := map[string]*jsonx.Object{}
	for _, ev := range events {
		switch ev.GetString("event_type") {
		case "SemanticCheckEvaluated":
			if b, _ := ev.Get("blocked"); truthyAny(b) {
				step := evStr(ev, "step_id", "unknown")
				checks[step] = ev
				for _, f := range ev.GetList("findings") {
					if fo, ok := f.(*jsonx.Object); ok {
						findings[key{step, findingTerm(fo)}] = fo
					}
				}
			}
		case "GlossaryClarificationRequested":
			cid := evStr(ev, "conflict_id", "")
			term := evStr(ev, "term", "unknown")
			step := evStr(ev, "step_id", "unknown")
			if cid == "" {
				continue
			}
			f := findings[key{step, term}]
			if f == nil {
				f = &jsonx.Object{}
			}
			chk := checks[step]
			if chk == nil {
				chk = &jsonx.Object{}
			}
			out = append(out, conflictRec{cid, term, evStr(f, "conflict_type", "unknown"), evStr(ev, "urgency", evStr(f, "severity", "unknown")),
				evStr(ev, "mission_id", ""), evStr(ev, "timestamp", ""), "unresolved", evStr(chk, "effective_strictness", "unknown")})
		case "GlossaryClarificationResolved":
			if cid := evStr(ev, "conflict_id", ""); cid != "" {
				resolved[cid] = true
			}
		}
	}
	for _, ev := range events {
		if ev.GetString("event_type") != "GlossaryClarificationResolved" {
			continue
		}
		cid := evStr(ev, "conflict_id", "")
		exists := false
		for _, c := range out {
			exists = exists || c.id == cid
		}
		if cid != "" && !exists {
			out = append(out, conflictRec{cid, evStr(ev, "term_surface", "unknown"), "unknown", "unknown", "", evStr(ev, "timestamp", ""), "resolved", "unknown"})
		}
	}
	var filtered []conflictRec
	for _, c := range out {
		if resolved[c.id] {
			c.status = "resolved"
		}
		if mission != "" && c.mission != mission {
			continue
		}
		if unresolved && c.status != "unresolved" {
			continue
		}
		if strictness != "" && c.strictness != strictness {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
}

func truthyAny(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.Object:
		return t.Len() > 0
	}
	return true
}

func runGlossaryConflicts(ctx *cli.Context) error {
	root := cwd()
	jsonMode := ctx.Bool("json")
	strictness := ctx.String("strictness")
	if strictness != "" && strictness != "off" && strictness != "medium" && strictness != "max" {
		return glossaryError(ctx, jsonMode, "invalid_strictness", fmt.Sprintf("Invalid strictness '%s'. Valid values: max, medium, off", strictness))
	}
	events := glossary.AllEvents(root)
	w := ctx.Stdout
	if len(events) == 0 {
		if jsonMode {
			fmt.Fprintln(w, "[]")
		} else {
			gprint(w, "[dim]No events found in glossary event log[/dim]")
		}
		return nil
	}
	cs := extractConflicts(events, ctx.String("mission"), ctx.Bool("unresolved"), strictness)
	if len(cs) == 0 {
		if jsonMode {
			fmt.Fprintln(w, "[]")
		} else {
			gprint(w, "[dim]No conflicts found[/dim]")
			gprint(w, "\n[dim]Total: 0 conflict(s)[/dim]")
		}
		return nil
	}
	if jsonMode {
		l := []any{}
		for _, c := range cs {
			l = append(l, c.object())
		}
		jsonOut(w, l, jsonx.Pretty)
		return nil
	}
	tbl := &rtable{Title: "Conflict History", Headers: []string{"Conflict ID", "Term", "Type", "Severity", "Status", "Strictness", "Mission", "Timestamp"}}
	unres := 0
	for _, c := range cs {
		id := c.id
		if r := []rune(id); len(r) > 20 {
			id = string(r[:20]) + "..."
		}
		ts := c.ts
		if r := []rune(ts); len(r) > 19 {
			ts = string(r[:19])
		}
		tbl.Rows = append(tbl.Rows, []string{id, c.term, c.typ, c.severity, c.status, c.strictness, c.mission, ts})
		if c.status == "unresolved" {
			unres++
		}
	}
	tbl.render(w, glossaryWidth)
	gprint(w, fmt.Sprintf("\n[dim]Total: %d conflict(s)[/dim]", len(cs)))
	if unres > 0 {
		gprint(w, fmt.Sprintf("[red]Unresolved: %d[/red]", unres))
	}
	return nil
}

func runGlossaryShow(ctx *cli.Context) error {
	root := cwd()
	term := ctx.Arg("term")
	surface := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(term, "glossary:")))
	// Compiled entity pages (.kittify/charter-packs/graph.yaml DRG) are not
	// supported; fall through to the seed/event-backed store like upstream
	// does when no page exists.
	all, err := glossary.LoadStore(root)
	if err != nil {
		return err
	}
	senses := glossary.Lookup(all, surface)
	w := ctx.Stdout
	if len(senses) > 0 {
		for _, s := range senses {
			gprintRaw(w, fmt.Sprintf("%s (%s)", s.Surface, s.Scope))
			gprintRaw(w, s.Definition)
			gprintRaw(w, fmt.Sprintf("Status: %s; confidence: %s", s.Status, strconv.FormatFloat(s.ConfidenceFloat(), 'f', 2, 64)))
		}
		return nil
	}
	gprint(w, "[red]Term not found:[/red] "+noEsc(term))
	gprint(w, "[dim]Run `spec-kitty glossary list` to see available terms.[/dim]")
	return cli.Exit(1)
}

func errorObjects(errs []glossary.SeedError) []any {
	out := []any{}
	for _, e := range errs {
		out = append(out, e.Object())
	}
	return out
}

func failEnvelope(result *jsonx.Object, msg string) *jsonx.Object {
	result.Set("ok", false)
	result.Set("error", jsonx.NewObject("code", "glossary_validation_failed", "message", msg))
	return result
}

func runGlossaryValidate(ctx *cli.Context) error {
	arg := ctx.Arg("path")
	jsonMode := ctx.Bool("json")
	w := ctx.Stdout
	st, err := os.Stat(arg)
	if err != nil {
		return cli.Usage(ctx.Cmd, "Invalid value for PATH: Path %s does not exist.", pyStrRepr(arg))
	}
	disp := filepath.Clean(arg)
	if !st.IsDir() {
		data, n, serr := glossary.ParseSeedFile(arg)
		_ = data
		if serr == nil {
			if jsonMode {
				jsonOut(w, jsonx.NewObject("files", []any{jsonx.NewObject("path", disp, "valid", true, "term_count", n, "errors", []any{})},
					"total_files", 1, "valid_files", 1, "invalid_files", 0), jsonx.Pretty)
			} else {
				gprint(w, fmt.Sprintf("[green]✓[/green] %s — Valid (%d terms)", noEsc(disp), n))
			}
			return nil
		}
		serr.Path = disp
		parseErr := len(serr.Errors) == 1 && strings.HasPrefix(serr.Errors[0].Message, "YAML parse error: ")
		if jsonMode {
			res := jsonx.NewObject("files", []any{jsonx.NewObject("path", disp, "valid", false, "term_count", 0, "errors", errorObjects(serr.Errors))},
				"total_files", 1, "valid_files", 0, "invalid_files", 1)
			msg := serr.Error()
			if parseErr {
				msg = serr.Errors[0].Message
			}
			jsonOut(w, failEnvelope(res, msg), jsonx.PrettyUTF8)
			return cli.Exit(1)
		}
		if parseErr {
			gprint(w, "[red]YAML parse error in "+noEsc(disp)+": "+noEsc(strings.TrimPrefix(serr.Errors[0].Message, "YAML parse error: "))+"[/red]")
			return cli.Exit(1)
		}
		gprint(w, "\n[red]Validating "+noEsc(disp)+"...[/red]\n")
		for _, e := range serr.Errors {
			gprint(w, "  [red]✗[/red] "+noEsc(e.Loc()+": "+e.Message))
		}
		gprint(w, fmt.Sprintf("\n%d error(s) in %s", len(serr.Errors), noEsc(disp)))
		return cli.Exit(1)
	}
	files, _ := filepath.Glob(filepath.Join(arg, "*.yaml"))
	sort.Strings(files)
	if len(files) == 0 {
		if jsonMode {
			jsonOut(w, jsonx.NewObject("files", []any{}, "total_files", 0, "valid_files", 0, "invalid_files", 0), jsonx.Pretty)
		} else {
			gprint(w, "[yellow]No .yaml files found in "+noEsc(disp)+"[/yellow]")
		}
		return nil
	}
	var results []any
	invalid := 0
	var scopeFiles []string
	for _, s := range glossary.AllScopes {
		scopeFiles = append(scopeFiles, s+".yaml")
	}
	for _, f := range files {
		name := filepath.Base(f)
		path := filepath.Join(disp, name)
		if !glossary.ValidScope(strings.TrimSuffix(name, ".yaml")) && !jsonMode {
			gprint(w, fmt.Sprintf("[yellow]⚠ %s: not a recognized scope filename (expected one of: %s)[/yellow]", noEsc(name), strings.Join(scopeFiles, ", ")))
		}
		_, n, serr := glossary.ParseSeedFile(f)
		if serr == nil {
			results = append(results, jsonx.NewObject("path", path, "valid", true, "term_count", n, "errors", []any{}))
			if !jsonMode {
				gprint(w, fmt.Sprintf("[green]✓[/green] %s — Valid (%d terms)", noEsc(name), n))
			}
			continue
		}
		invalid++
		results = append(results, jsonx.NewObject("path", path, "valid", false, "term_count", 0, "errors", errorObjects(serr.Errors)))
		if jsonMode {
			continue
		}
		if len(serr.Errors) == 1 && strings.HasPrefix(serr.Errors[0].Message, "YAML parse error: ") {
			gprint(w, "[red]✗ "+noEsc(name)+": "+noEsc(serr.Errors[0].Message)+"[/red]")
			continue
		}
		gprint(w, "\n[red]✗ "+noEsc(name)+":[/red]")
		for _, e := range serr.Errors {
			gprint(w, "    "+noEsc(e.Loc()+": "+e.Message))
		}
	}
	if jsonMode {
		res := jsonx.NewObject("files", results, "total_files", len(results), "valid_files", len(results)-invalid, "invalid_files", invalid)
		if invalid > 0 {
			jsonOut(w, failEnvelope(res, fmt.Sprintf("%d file(s) failed validation", invalid)), jsonx.PrettyUTF8)
		} else {
			jsonOut(w, res, jsonx.Pretty)
		}
	} else if invalid > 0 {
		gprint(w, fmt.Sprintf("\nSummary: %d of %d file(s) failed validation.", invalid, len(results)))
	} else {
		gprint(w, fmt.Sprintf("\nAll %d file(s) valid.", len(results)))
	}
	if invalid > 0 {
		return cli.Exit(1)
	}
	return nil
}

// runGlossaryResolve is the interactive async conflict resolution.
func runGlossaryResolve(ctx *cli.Context) error {
	root := cwd()
	cid := ctx.Arg("conflict_id")
	w := ctx.Stdout
	events := glossary.AllEvents(root)
	type key struct{ step, term string }
	findings := map[key]*jsonx.Object{}
	for _, ev := range events {
		if ev.GetString("event_type") == "SemanticCheckEvaluated" {
			if b, _ := ev.Get("blocked"); truthyAny(b) {
				step := evStr(ev, "step_id", "unknown")
				for _, f := range ev.GetList("findings") {
					if fo, ok := f.(*jsonx.Object); ok {
						findings[key{step, findingTerm(fo)}] = fo
					}
				}
			}
		}
	}
	var req, finding *jsonx.Object
	for _, ev := range events {
		if ev.GetString("event_type") == "GlossaryClarificationRequested" && ev.GetString("conflict_id") == cid {
			req = ev
			finding = findings[key{evStr(ev, "step_id", "unknown"), evStr(ev, "term", "unknown")}]
			break
		}
	}
	if req == nil {
		gprint(w, "[red]Error: Conflict '"+noEsc(cid)+"' not found[/red]")
		return cli.Exit(1)
	}
	in := bufio.NewReader(ctx.Stdin)
	prompt := func(text string) (string, bool) {
		fmt.Fprint(w, text+": ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", false
		}
		return strings.TrimRight(line, "\r\n"), true
	}
	for _, ev := range events {
		if ev.GetString("event_type") == "GlossaryClarificationResolved" && ev.GetString("conflict_id") == cid {
			gprint(w, "[yellow]Warning: Conflict '"+noEsc(cid)+"' already resolved[/yellow]")
			ans, ok := prompt("Resolve again? [y/N]")
			if !ok || !(strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes")) {
				return cli.Exit(0)
			}
			break
		}
	}
	term := evStr(req, "term", "unknown")
	gprint(w, "\n[bold]Conflict: "+noEsc(cid)+"[/bold]")
	gprint(w, "Term: [cyan]"+noEsc(term)+"[/cyan]")
	if finding != nil {
		gprintRaw(w, "Type: "+evStr(finding, "conflict_type", "unknown"))
		gprintRaw(w, "Severity: "+evStr(finding, "severity", "unknown"))
		gprintRaw(w, "Context: "+evStr(finding, "context", "N/A"))
	} else {
		gprintRaw(w, "Urgency: "+evStr(req, "urgency", "unknown"))
	}
	var cands []*jsonx.Object
	if finding != nil {
		for _, c := range finding.GetList("candidate_senses") {
			if co, ok := c.(*jsonx.Object); ok {
				cands = append(cands, co)
			}
		}
	}
	if len(cands) == 0 {
		for _, o := range req.GetList("options") {
			cands = append(cands, jsonx.NewObject("surface", term, "scope", "unknown", "definition", o, "confidence", 0.5))
		}
	}
	if len(cands) > 0 {
		gprint(w, "\n[bold]Candidate senses:[/bold]")
		for i, c := range cands {
			gprint(w, fmt.Sprintf("  %d. [%s] %s", i+1, evStr(c, "scope", "?"), evStr(c, "definition", "No definition")))
		}
	}
	fmt.Fprintln(w)
	opts := ""
	if len(cands) > 0 {
		opts = fmt.Sprintf("Enter 1-%d to select a candidate, ", len(cands))
	}
	opts += "'C' for custom definition, 'D' to defer"
	gprintRaw(w, opts)
	choice, ok := prompt("Resolution")
	if !ok {
		return cli.Exit(1)
	}
	mission := ctx.String("mission")
	if mission == "" {
		mission = evStr(req, "mission_id", "unknown")
	}
	if mission == "" {
		mission = "unknown"
	}
	var selected *jsonx.Object
	switch {
	case strings.ToUpper(choice) == "D":
		gprint(w, "[yellow]Conflict deferred (no resolution made)[/yellow]")
		return cli.Exit(0)
	case strings.ToUpper(choice) == "C":
		def, ok := prompt("Enter custom definition")
		if !ok {
			return cli.Exit(1)
		}
		selected = jsonx.NewObject("surface", term, "scope", "team_domain", "definition", def, "confidence", 1.0)
	default:
		n, err := strconv.Atoi(choice)
		if len(cands) == 0 || err != nil || choice == "" || strings.ContainsAny(choice, "+- ") {
			gprint(w, "[red]Error: Invalid choice '"+noEsc(choice)+"'[/red]")
			return cli.Exit(1)
		}
		if n < 1 || n > len(cands) {
			gprint(w, "[red]Error: Invalid selection '"+noEsc(choice)+"'[/red]")
			return cli.Exit(1)
		}
		selected = cands[n-1]
	}
	actor := jsonx.NewObject("actor_id", "user:cli", "actor_type", "human", "display_name", "CLI User")
	prov := jsonx.NewObject("source", "cli_resolve", "timestamp", "", "actor_id", "user:cli")
	ev := jsonx.NewObject("event_type", "GlossaryClarificationResolved", "conflict_id", cid, "term_surface", term,
		"selected_sense", selected, "actor", actor, "resolution_mode", "async", "provenance", prov, "timestamp", clock.NowUTCISO())
	if err := glossary.AppendEvent(root, mission, ev); err != nil {
		return err
	}
	if strings.ToUpper(choice) == "C" {
		scope := evStr(selected, "scope", "team_domain")
		conf, _ := selected.Get("confidence")
		se := jsonx.NewObject("event_type", "GlossarySenseUpdated", "term_surface", term, "scope", scope,
			"new_sense", jsonx.NewObject("surface", term, "scope", scope, "definition", selected.GetString("definition"), "confidence", conf, "status", "active"),
			"actor", actor, "update_type", "create", "provenance", prov, "timestamp", clock.NowUTCISO())
		if err := glossary.AppendEvent(root, mission, se); err != nil {
			return err
		}
	}
	gprint(w, "[green]Conflict resolved successfully[/green]")
	return nil
}

// noEsc marks user data interpolated into rich markup (upstream does not
// escape it either).
func noEsc(s string) string { return s }
