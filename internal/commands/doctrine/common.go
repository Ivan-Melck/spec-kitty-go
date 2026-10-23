// Package doctrine registers the doctrine/charter/profile/Op command
// surface: charter, mission / mission-type (type inspection), profiles,
// agent profile, dispatch, profile-invocation, invocations, ops, glossary.
// See docs/parity/doctrine.md.
package doctrine

import (
	"fmt"
	"io"
	"os"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// repoRoot is upstream find_repo_root (project root, else cwd).
func repoRoot() string {
	if r := project.Locate(""); r != "" {
		return r
	}
	wd, _ := os.Getwd()
	return wd
}

// findRepoRoot is task_utils.find_repo_root: the project root, else the
// enclosing repository's main root; "" (TaskCliError) when neither exists.
func findRepoRoot() string {
	if r := project.Locate(""); r != "" {
		return r
	}
	return findRepoRootStrict()
}

// repoRootOrCrash is an uncaught find_repo_root: outside any project or
// repository the TaskCliError escapes as rich's traceback (exit 1).
func repoRootOrCrash(ctx *cli.Context, frames ...rich.Frame) (string, error) {
	if r := findRepoRoot(); r != "" {
		return r, nil
	}
	frames = append(frames, rich.Frame{Name: "find_repo_root", Line: 94})
	rich.PrintTraceback(ctx.Stderr, &rich.Exception{Type: "TaskCliError", Value: repoRootMissing, Frames: frames})
	return "", cli.Exit(1)
}

// projectRootOrExit is helpers.get_project_root_or_exit (human form).
func projectRootOrExit(ctx *cli.Context) (string, error) {
	if r := project.Locate(""); r != "" {
		return r, nil
	}
	rich.Print(ctx.Stdout, "[red]Error:[/red] Unable to locate the Spec Kitty project root (.kittify directory not found).")
	rich.Print(ctx.Stdout, "[dim]Run this command from the project root or from a mission worktree under .worktrees/<mission>/.[/dim]")
	rich.Print(ctx.Stdout, "[dim]Tip: Initialize a project with 'spec-kitty init <name>' if one does not exist.[/dim]")
	return "", cli.Exit(1)
}

func cwd() string {
	wd, _ := os.Getwd()
	return wd
}

// cprint is rich console.print (markup, highlighting, wrapping).
func cprint(w io.Writer, format string, a ...any) {
	if len(a) == 0 {
		rich.Print(w, format)
		return
	}
	rich.Print(w, fmt.Sprintf(format, a...))
}

// cprintRaw is console.print(s) of a pre-built string (no format verbs):
// markup, highlighting and wrapping like rich.
func cprintRaw(w io.Writer, s string) { rich.Print(w, s) }

// cprintPlain is console.print(s, markup=False).
func cprintPlain(w io.Writer, s string) { rich.PrintPlain(w, s) }

func jsonOut(w io.Writer, v any, opts jsonx.Options) {
	fmt.Fprintln(w, jsonx.Dumps(v, opts))
}

// jsonError is upstream json_error(code, message) emitted via
// console.emit_json (indent 2, ensure_ascii False).
func jsonError(w io.Writer, code, message string) {
	jsonOut(w, jsonx.NewObject("ok", false, "error", jsonx.NewObject("code", code, "message", message)), jsonx.PrettyUTF8)
}

func strs(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}
