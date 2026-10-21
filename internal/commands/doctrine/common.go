// Package doctrine registers the doctrine/charter/profile/Op command
// surface: charter, mission / mission-type (type inspection), profiles,
// agent profile, dispatch, profile-invocation, invocations, ops, glossary.
// See docs/parity/doctrine.md.
package doctrine

import (
	"fmt"
	"io"
	"os"

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

func cwd() string {
	wd, _ := os.Getwd()
	return wd
}

// cprint is rich console.print (markup stripped, wrapped).
func cprint(w io.Writer, format string, a ...any) {
	if len(a) == 0 {
		rich.Print(w, format)
		return
	}
	rich.Print(w, fmt.Sprintf(format, a...))
}

// cprintRaw prints without markup processing (markup=False) but wrapped.
func cprintRaw(w io.Writer, s string) {
	fmt.Fprintln(w, rich.Wrap(s, rich.Width()))
}

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
