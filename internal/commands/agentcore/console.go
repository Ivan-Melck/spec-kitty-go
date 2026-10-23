package agentcore

import (
	"fmt"
	"io"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// Compact is json.dumps(x) with default separators (ensure_ascii=True).
var Compact = jsonx.Options{Indent: -1, EnsureASCII: true}

// richPrint is console.print(text) on the CLI console: markup, repr
// highlighting and soft wrapping at the console width.
func richPrint(w io.Writer, text string) { rich.Print(w, text) }

// printJSON writes v as compact json.dumps(v) + newline.
func printJSON(w io.Writer, v any) { fmt.Fprintln(w, jsonx.Dumps(v, Compact)) }

// emitJSON is mission_parsing._emit_json: json.dumps(payload) with
// spec_kitty_version appended when absent.
func emitJSON(w io.Writer, payload *jsonx.Object) {
	if !payload.Has("spec_kitty_version") {
		payload = payload.Clone()
		payload.Set("spec_kitty_version", app.Version)
	}
	printJSON(w, payload)
}

// consolePrint is console.print(text): rich markup, repr highlighting and
// soft wrapping (styled on a colour terminal).
func consolePrint(w io.Writer, text string) { rich.Print(w, text) }

// stripMarkup removes rich markup tags (rich.markup.render's plain text).
func stripMarkup(s string) string { return rich.Strip(s) }

// printJSONPretty writes json.dumps(v, indent=2) + newline.
func printJSONPretty(w io.Writer, v any) { fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Pretty)) }

// projectRoot locates the main project root.
func projectRoot() (string, error) {
	r := project.Locate("")
	if r == "" {
		return "", project.ErrNotFound
	}
	return r, nil
}

// resolveTasksMission resolves --mission for the agent tasks/status command
// family (tasks_shared._find_mission_slug), rendering upstream's errors.
func resolveTasksMission(ctx *cli.Context, root, handle string, jsonMode bool) (string, error) {
	if strings.TrimSpace(handle) == "" {
		msg := "--mission <slug> is required"
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("error", msg))
		} else {
			richPrint(ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return "", cli.Exit(1)
	}
	h := strings.TrimSpace(handle)
	slug, err := mission.ResolveHandle(root, h)
	if err == nil {
		return slug, nil
	}
	return "", renderResolveError(ctx, err, jsonMode)
}

// renderResolveError renders a mission.ResolveHandle failure.
func renderResolveError(ctx *cli.Context, err error, jsonMode bool) error {
	switch e := err.(type) {
	case *mission.SelectorAmbiguousError:
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("success", false, "error_code", e.Code(), "error", e.Error(),
				"handle", e.Handle, "candidates", e.Candidates))
			return cli.Exit(1)
		}
		richPrint(ctx.Stdout, "[red]Error:[/red] "+e.Error())
		return cli.Exit(2)
	case *mission.AmbiguousError:
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("success", false, "error_code", "MISSION_AMBIGUOUS_SELECTOR", "error", e.Error(),
				"handle", e.Handle, "candidates", e.CandidatesJSON()))
			return cli.Exit(1)
		}
		richPrint(ctx.Stderr, e.Error())
		return cli.Exit(2)
	case *mission.NotFoundError:
		if jsonMode {
			printJSON(ctx.Stdout, jsonx.NewObject("success", false, "error_code", "MISSION_NOT_FOUND", "error", e.Error(), "handle", e.Handle))
			return cli.Exit(1)
		}
		richPrint(ctx.Stderr, "[red]Error:[/red] "+e.HumanMessage())
		return cli.Exit(2)
	}
	return err
}
