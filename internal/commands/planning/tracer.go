package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

var tracerCategories = map[string]string{"tooling-friction": "tooling-friction.md", "approach": "approach.md", "design-decisions": "design-decisions.md"}

func init() {
	addCommands(func(_, agent, _, _ *cli.Command) {
		agent.Add(&cli.Command{
			Name:  "tracer-append",
			Short: "Append a dated, attributed finding to the mission's tracer surface.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission handle (e.g. 'my-mission-01ABCDEF')"},
				{Name: "category", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Tracer category: approach | design-decisions | tooling-friction"},
				{Name: "entry", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Finding text"},
				{Name: "actor", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Attribution (required, non-empty)"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
			},
			Run: runTracerAppend,
		})
	})
}

func runTracerAppend(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	emit := func(p *jsonx.Object, ok bool) {
		if jsonMode {
			printCompact(ctx.Stdout, p)
			return
		}
		if ok {
			say(ctx.Stdout, "[green]✓[/green] "+rich.EscapeMarkup(p.GetString("row_or_entry_ref")))
		} else {
			e := p.GetString("error")
			if e == "" {
				e = "tracer-append failed"
			}
			say(ctx.Stdout, "[red]Error:[/red] "+rich.EscapeMarkup(e))
		}
	}
	errP := func(msg string) error {
		emit(jsonx.NewObject("ok", false, "kind", "TRACER_FILE", "error", msg), false)
		return cli.Exit(1)
	}
	root := project.Locate("")
	if root == "" {
		return errP("Could not locate project root")
	}
	actor := strings.TrimSpace(ctx.String("actor"))
	if actor == "" {
		return errP("tracer-append requires a non-empty --actor; a blank actor would silently blank attribution on the persisted finding (#2960).")
	}
	category := ctx.String("category")
	filename, ok := tracerCategories[category]
	if !ok {
		return errP(fmt.Sprintf("Unknown tracer category %s; expected one of ['approach', 'design-decisions', 'tooling-friction']", mission.PyRepr(category)))
	}
	handle := ctx.String("mission")
	slug := handle
	if name, err := mission.ResolveHandle(root, handle); err == nil {
		slug = name
	}
	line := strings.Join([]string{clock.Now().UTC().Format("2006-01-02"), actor, strings.TrimSpace(ctx.String("entry"))}, " · ")
	entryID := category + "-" + sha256Hex([]byte(line))[:12]
	s := coord.Resolve(root, slug)
	dir, err := s.WriteDir()
	if err != nil {
		return errP(err.Error())
	}
	path := filepath.Join(dir, "traces", filename)
	current := ""
	if b, err := os.ReadFile(filepath.Join(s.StatusDir(), "traces", filename)); err == nil {
		current = string(b)
	}
	base := current
	if base == "" {
		base = fmt.Sprintf("# Tracer: %s\n\nOne entry per finding: `YYYY-MM-DD · actor · <text>`.\n\n---\n", category)
	}
	merged := base
	present := false
	for _, l := range strings.Split(strings.ReplaceAll(base, "\r\n", "\n"), "\n") {
		if l == line {
			present = true
		}
	}
	if !present {
		merged = strings.TrimRight(base, "\n") + "\n\n" + line + "\n"
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(merged), 0o644); err != nil {
		return errP(err.Error())
	}
	r := safecommit.CommitForMission(root, slug, []string{path}, fmt.Sprintf("chore(tracer): append %s finding for %s", category, slug), mission.KindTracerFile, safecommit.RouterOptions{})
	printSurfaces := func() {
		if !jsonMode {
			r.PrintRendered(ctx.Stdout, wrapPlain)
		}
	}
	switch r.Status {
	case "refused", safecommit.StatusNoOpWrongSurface:
		d := r.Diagnostic
		if d == "" {
			d = "tracer-append refused an unroutable write target"
		}
		if r.Status == "refused" {
			p := jsonx.NewObject("ok", false, "kind", "TRACER_FILE", "error", d, "refusal", jsonx.NewObject("reason", optString(r.Diagnostic), "deferred_to", "#3033"), "surfaces", r.SurfacesPayload())
			printSurfaces()
			emit(p, false)
			return cli.Exit(1)
		}
	case safecommit.StatusError:
		d := r.Diagnostic
		if d == "" {
			d = "tracer-append commit failed"
		}
		p := jsonx.NewObject("ok", false, "kind", "TRACER_FILE", "error", d, "surfaces", r.SurfacesPayload())
		printSurfaces()
		emit(p, false)
		return cli.Exit(1)
	}
	ok = true
	for _, o := range r.Surfaces {
		if o.Status == "refused" || o.Status == "error" {
			ok = false
		}
	}
	p := jsonx.NewObject("ok", ok, "kind", "TRACER_FILE", "destination_surface", "coord", "row_or_entry_ref", entryID, "status", r.Status,
		"commit_ref", r.PlacementRef, "commit_hash", optString(r.CommitHash), "surfaces", r.SurfacesPayload())
	printSurfaces()
	emit(p, ok)
	if !ok {
		return cli.Exit(1)
	}
	return nil
}
