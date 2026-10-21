package doctrine

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/invocation"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// lineJSON is json.dumps() with default separators on one line.
var lineJSON = jsonx.Options{Indent: -1, EnsureASCII: true}

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "dispatch",
			Short: "Dispatch a request to a governed Op (canonical surface).",
			Flags: []*cli.Flag{
				{Name: "profile", Kind: cli.String, Help: "Optional profile ID. Bypasses the router — use when the request is ambiguous."},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON payload"},
				{Name: "dry-run", Kind: cli.Bool, Help: "Route the request and return the routing signal without opening an Op or writing anything."},
			},
			Args: []*cli.Arg{{Name: "request", Help: "Natural language request. The router picks the best profile.", Required: true, Metavar: "{request}"}},
			Run:  runDispatch,
		})
	})
}

// detectActor is _detect_actor.
func detectActor() string {
	if os.Getenv("CLAUDE_CODE_ENTRYPOINT") != "" {
		return "claude"
	}
	if os.Getenv("CODEX_CLI") != "" {
		return "codex"
	}
	return "operator"
}

func routingFailed(ctx *cli.Context, code, message string, cands []invocation.Alternative, suggestion string) error {
	cl := []any{}
	for _, c := range cands {
		cl = append(cl, jsonx.NewObject("profile_id", c.ProfileID, "action", c.Action, "match_reason", c.MatchReason, "confidence", c.Confidence))
	}
	fmt.Fprintln(ctx.Stderr, jsonx.Dumps(jsonx.NewObject("error", "routing_failed", "error_code", code, "message", message,
		"candidates", cl, "suggestion", suggestion), lineJSON))
	return cli.Exit(1)
}

func dispatchErr(ctx *cli.Context, err error, jsonMode bool) error {
	var re *invocation.RouterError
	var pnf *invocation.ProfileNotFoundError
	var we *invocation.WriteError
	switch {
	case errors.As(err, &re):
		return routingFailed(ctx, re.Code, re.Error(), re.Candidates, re.Suggestion)
	case errors.As(err, &pnf):
		return routingFailed(ctx, "PROFILE_NOT_FOUND", pnf.Error(), nil, "Run 'spec-kitty profiles list' to see available profiles.")
	case errors.As(err, &we):
		fmt.Fprintln(ctx.Stderr, jsonx.Dumps(jsonx.NewObject("error", "write_failed", "message", we.Error()), lineJSON))
		return cli.Exit(1)
	}
	return charterErr(ctx, jsonMode, err)
}

func runDispatch(ctx *cli.Context) error {
	root := repoRoot()
	request := ctx.Arg("request")
	hint := ctx.OptString("profile")
	jsonMode := ctx.Bool("json")
	if ctx.Bool("dry-run") {
		p, err := invocation.DryRun(root, request, hint, detectActor())
		if err != nil {
			var re *invocation.RouterError
			if errors.As(err, &re) && re.Code == "ROUTER_AMBIGUOUS" {
				amb := invocation.AmbiguousDryRunObject(re)
				if jsonMode {
					jsonOut(ctx.Stdout, amb, lineJSON)
					return nil
				}
				cprint(ctx.Stdout, "[yellow]Routing is ambiguous[/yellow] — no single winner (router_confidence: ambiguous).")
				if len(re.Candidates) > 0 {
					cprint(ctx.Stdout, "[dim]Candidates considered (%d):[/dim]", len(re.Candidates))
					for _, a := range re.Candidates {
						cprint(ctx.Stdout, "  - %s (%s, %s) — %s", a.ProfileID, a.Action, a.Confidence, a.MatchReason)
					}
				}
				cprint(ctx.Stdout, "[dim]Dry run — no Op opened, nothing written. Use --profile <id> to disambiguate.[/dim]")
				return nil
			}
			return dispatchErr(ctx, err, jsonMode)
		}
		if jsonMode {
			jsonOut(ctx.Stdout, p.DryRunObject(), lineJSON)
			return nil
		}
		renderCapsule(ctx, p, false)
		cprint(ctx.Stdout, "[dim]Dry run — no Op opened, nothing written.[/dim]")
		return nil
	}
	p, err := invocation.Invoke(root, request, hint, detectActor(), invocation.ModeTaskExecution)
	if err != nil {
		return dispatchErr(ctx, err, jsonMode)
	}
	if jsonMode {
		jsonOut(ctx.Stdout, p.Object(), jsonx.Pretty)
		return nil
	}
	renderCapsule(ctx, p, true)
	w := ctx.Stdout
	cprint(w, "\n[bold]This Op is OPEN.[/bold] After completing the work, close it with the real outcome:")
	cprint(w, "  [dim]spec-kitty profile-invocation complete --invocation-id %s --outcome <done|failed|abandoned> \\[--evidence <file>] \\[--artifact <path>] \\[--commit <sha>][/dim]", p.InvocationID)
	cprint(w, "[dim]Unclosed Ops are reported by `spec-kitty doctor ops` and swept to 'abandoned' when stale.[/dim]")
	return nil
}

// renderCapsule is _render_payload_capsule.
func renderCapsule(ctx *cli.Context, p *invocation.Payload, showID bool) {
	w := ctx.Stdout
	cprint(w, "[bold green]Profile:[/bold green] %s (%s)", p.ProfileName, p.ProfileID)
	cprint(w, "[bold]Action:[/bold] %s", p.Action)
	if p.RouterConfidence != "" {
		cprint(w, "[dim]Router confidence:[/dim] %s", p.RouterConfidence)
	}
	if len(p.Alternatives) > 0 {
		cprint(w, "[dim]Alternatives considered (%d):[/dim]", len(p.Alternatives))
		for _, a := range p.Alternatives {
			cprint(w, "  - %s (%s, %s)", a.ProfileID, a.Action, a.Confidence)
		}
	}
	if showID {
		cprint(w, "[dim]Invocation ID:[/dim] %s", p.InvocationID)
	}
	if p.EmptyCharterFallback {
		fitPanel(w, "No charter activations found in this project -- routed to the generic agent.\n"+
			"To set a governance baseline, run (see all presets with `spec-kitty charter pack list`):\n"+
			"  spec-kitty charter activate --preset minimal\n"+
			"This activates config entries -- it does not by itself make an unmatched request route to a specialist; you may still need an explicit --profile <name>.",
			"Empty Charter")
	}
	if obs := p.Glossary; obs != nil && len(obs.HighSeverity) > 0 {
		lines := []string{"High-severity terminology conflicts detected before this invocation."}
		for _, c := range obs.HighSeverity {
			set := map[string]bool{}
			for _, s := range c.Senses {
				set[s.Scope] = true
			}
			var scopes []string
			for s := range set {
				scopes = append(scopes, s)
			}
			sort.Strings(scopes)
			d := c.Term + " (" + c.Type + ")"
			if len(scopes) > 0 {
				d += " — candidate scopes: " + strings.Join(scopes, ", ")
			}
			lines = append(lines, "- "+d)
		}
		fitPanel(w, rich.Strip(strings.Join(lines, "\n")), "Glossary Warning")
	}
	if rec := p.Recommendation; rec != nil {
		fitPanel(w, rich.Strip(formatRecommendation(rec)), "Model Routing Recommendation (advisory)")
	}
	if p.ContextAvailable && p.ContextText != "" {
		fitPanel(w, rich.Strip(p.ContextText), "Governance Context")
	} else {
		cprint(w, "[yellow]Governance context unavailable.[/yellow] Run 'spec-kitty charter synthesize'.")
	}
}

// formatRecommendation is _format_recommendation.
func formatRecommendation(r *invocation.Recommendation) string {
	lines := []string{fmt.Sprintf("Task type: %s (objective: %s)", r.TaskType, r.Objective)}
	if c := r.CatalogCandidate(); c != nil {
		score := "n/a"
		if c.Score != nil {
			score = fmt.Sprintf("%.2f", *c.Score)
		}
		l := fmt.Sprintf("Catalog pick: %s (score=%s)", c.ModelID, score)
		if c.Rationale != nil && *c.Rationale != "" {
			l += " — " + *c.Rationale
		}
		lines = append(lines, l)
	}
	if c := r.ProfileCandidate(); c != nil {
		l := "Profile preference: " + c.ModelID
		if c.Effort != nil && *c.Effort != "" {
			l += " (effort=" + *c.Effort + ")"
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}
