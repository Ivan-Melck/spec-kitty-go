package agentcore

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Compact is json.dumps(x) with default separators (ensure_ascii=True).
var Compact = jsonx.Options{Indent: -1, EnsureASCII: true}

// richPrint writes text the way rich's Console.print does for plain text:
// every line is soft-wrapped at the console width (COLUMNS).
func richPrint(w io.Writer, text string) {
	width := cli.Width()
	for _, line := range strings.Split(text, "\n") {
		for _, l := range RichWrap(line, width) {
			fmt.Fprintln(w, l)
		}
	}
}

// RichWrap greedily wraps one line at word boundaries (rich divide_line);
// words longer than width are folded.
func RichWrap(line string, width int) []string {
	if utf8.RuneCountInString(line) <= width || width <= 0 {
		return []string{strings.TrimRight(line, " ")}
	}
	// split into words keeping trailing whitespace with each word
	var words []string
	cur := ""
	inSpace := false
	for _, r := range line {
		if r == ' ' {
			inSpace = true
			cur += string(r)
			continue
		}
		if inSpace {
			words = append(words, cur)
			cur = ""
			inSpace = false
		}
		cur += string(r)
	}
	if cur != "" {
		words = append(words, cur)
	}
	var out []string
	lineBuf := ""
	for _, wd := range words {
		trimmed := strings.TrimRight(wd, " ")
		if utf8.RuneCountInString(lineBuf)+utf8.RuneCountInString(trimmed) <= width {
			lineBuf += wd
			continue
		}
		if lineBuf != "" {
			out = append(out, strings.TrimRight(lineBuf, " "))
			lineBuf = ""
		}
		for utf8.RuneCountInString(strings.TrimRight(wd, " ")) > width {
			rs := []rune(wd)
			out = append(out, string(rs[:width]))
			wd = string(rs[width:])
		}
		lineBuf = wd
	}
	if strings.TrimRight(lineBuf, " ") != "" || len(out) == 0 {
		out = append(out, strings.TrimRight(lineBuf, " "))
	}
	return out
}

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

// consolePrint renders rich text with markup tags stripped and soft wrapping.
func consolePrint(w io.Writer, text string) { richPrint(w, stripMarkup(text)) }

var markupRe = regexp.MustCompile(`\[/?(?:bold|dim|red|green|yellow|cyan|blue|magenta|white|italic|underline)(?: [a-z]+)*\]`)

func stripMarkup(s string) string { return markupRe.ReplaceAllString(s, "") }

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
			richPrint(ctx.Stdout, "Error: "+msg)
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
		richPrint(ctx.Stdout, "Error: "+e.Error())
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
		richPrint(ctx.Stderr, "Error: "+e.HumanMessage())
		return cli.Exit(2)
	}
	return err
}
