// Package maintenance implements the area-H commands: `materialize`, the
// `migrate` group (project-data backfills) and the mission/git-state
// `doctor` leaves.
package maintenance

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/project"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// emitJSON is console.emit_json: json.dumps(indent=2, ensure_ascii=False).
func emitJSON(w io.Writer, v any) {
	fmt.Fprintln(w, jsonx.Dumps(v, jsonx.PrettyUTF8))
}

// printJSON is print(json.dumps(payload, indent=2)) (ensure_ascii=True).
func printJSON(w io.Writer, v any) {
	fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Pretty))
}

// jsonError is json_error(code, message).
func jsonError(code, message string) *jsonx.Object {
	return jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", code, "message", message))
}

// cprint is console.print (markup stripped, soft-wrapped at the width).
func cprint(w io.Writer, s string) { rich.Print(w, s) }

// cprintf is cprint with formatting.
func cprintf(w io.Writer, format string, a ...any) { rich.Print(w, fmt.Sprintf(format, a...)) }

// errorLine is migrate_cmd._error: "[red]Error:[/red] <msg>" on stderr.
func errorLine(ctx *cli.Context, msg string) {
	cprint(ctx.Stderr, "[red]Error:[/red] "+msg)
}

var reEscapable = regexp.MustCompile(`(\\*)(\[[a-z#/@][^\[]*?\])`)

// escapeMarkup is rich.markup.escape.
func escapeMarkup(s string) string {
	s = reEscapable.ReplaceAllStringFunc(s, func(m string) string {
		sub := reEscapable.FindStringSubmatch(m)
		return sub[1] + sub[1] + `\` + sub[2]
	})
	if strings.HasSuffix(s, `\`) && !strings.HasSuffix(s, `\\`) {
		s += `\`
	}
	return s
}

// locateRootQuiet is locate_project_root() ("" when not in a project).
func locateRootQuiet() string { return project.Locate("") }

func strList(xs []string) []any {
	out := make([]any, 0, len(xs))
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

func boolFlag(name, help string) *cli.Flag {
	return &cli.Flag{Name: name, Kind: cli.Bool, Help: help}
}
