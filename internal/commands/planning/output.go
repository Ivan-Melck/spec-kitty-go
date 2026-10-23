// Package planning registers the planning/authoring commands (area E):
// safe-commit, spec-commit, agent mission check-prerequisites / setup-plan /
// finalize-tasks / record-analysis / repair, agent decision *, agent tasks
// map-requirements / finalize-tasks, issue-matrix, agent issue-verdict,
// agent tracer-append, intake and the top-level specify/plan/tasks/research.
package planning

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// compact is json.dumps(x) (default separators, ensure_ascii).
var compact = jsonx.Options{Indent: -1, EnsureASCII: true}

func printCompact(w io.Writer, v any) { fmt.Fprintln(w, jsonx.Dumps(v, compact)) }

func printPretty(w io.Writer, v any) { fmt.Fprintln(w, jsonx.Dumps(v, jsonx.Pretty)) }

// emitJSON is mission_parsing._emit_json (spec_kitty_version appended).
func emitJSON(w io.Writer, payload *jsonx.Object) {
	if !payload.Has("spec_kitty_version") {
		payload = payload.Clone()
		payload.Set("spec_kitty_version", app.Version)
	}
	printCompact(w, payload)
}

// say is console.print with markup.
func say(w io.Writer, s string) { rich.Print(w, s) }

// sayPlain is console.print(..., markup=False).
func sayPlain(w io.Writer, s string) { rich.PrintNoMarkup(w, s) }

// printRendered is `for line in render_commit_outcome(result):
// console.print(line, markup=False)`.
func printRendered(w io.Writer, r *safecommit.RouterResult) {
	for _, l := range r.Render() {
		rich.PrintPlain(w, l)
	}
}

// cwd returns the resolved working directory.
func cwd() string {
	d, _ := os.Getwd()
	if r, err := filepath.EvalSymlinks(d); err == nil {
		return r
	}
	return d
}

func strList(xs []string) []any {
	out := make([]any, 0, len(xs))
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

func optString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// joinMessage is join_message_paragraphs.
func joinMessage(paragraphs []string) (string, error) {
	var kept []string
	for _, p := range paragraphs {
		if t := strings.TrimSpace(p); t != "" {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return "", fmt.Errorf("Commit message is empty.")
	}
	return strings.Join(kept, "\n\n"), nil
}

// helpExit2 renders a no_args_is_help group's help on stdout and exits 2
// (click's NoArgsIsHelpError).
func helpExit2(ctx *cli.Context) error {
	cli.WriteNoArgsIsHelp(ctx.Stdout, ctx.Cmd)
	return cli.Exit(2)
}

const messageHelp = "Commit message. Repeat -m to add paragraphs (joined by a blank line, the same way git does)."
