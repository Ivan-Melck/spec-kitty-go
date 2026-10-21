// Package lint implements `spec-kitty lint`: run ruff and mypy on one file
// (path from argv or from a harness hook JSON payload on stdin).
package lint

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

func init() {
	app.Register(func(root *cli.Command) {
		root.Add(&cli.Command{
			Name:  "lint",
			Short: "Run ruff and mypy on a file and report errors.",
			Long: "Run ruff and mypy on a file and report errors.\n\n" +
				"This command is designed to be used as a post-edit hook for AI agents,\n" +
				"providing immediate feedback on linting and type-checking violations.\n" +
				"When invoked without a path (the wired hook form), the target file is read\n" +
				"from the harness JSON payload on stdin.",
			Args: []*cli.Arg{{Name: "file_path", Metavar: "[file_path]", Help: "File to lint/type-check; omit to read the path from a hook stdin payload"}},
			Flags: []*cli.Flag{
				{Name: "json", Kind: cli.Bool, Help: "Output in JSON format for AI agents"},
				{Name: "fix", Kind: cli.Bool, Help: "Attempt to automatically fix lint errors"},
			},
			Run: run,
		})
	})
}

var mypySummary = regexp.MustCompile(`^(Success:|Found \d+ error)`)

// PyPathStr renders str(pathlib.PurePosixPath(p)): duplicate slashes and
// "." components collapse, a trailing slash is dropped.
func PyPathStr(p string) string {
	if p == "" {
		return "."
	}
	abs := strings.HasPrefix(p, "/")
	lead := ""
	if abs {
		lead = "/"
		if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
			lead = "//"
		}
	}
	var parts []string
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." {
			continue
		}
		parts = append(parts, s)
	}
	out := lead + strings.Join(parts, "/")
	if out == "" {
		return "."
	}
	return out
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// tool runs name args in dir; ok is false when the executable is missing.
func tool(dir, name string, args ...string) (code int, stdout, stderr string, ok bool) {
	path, err := exec.LookPath(name)
	if err != nil {
		return 0, "", "", false
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		return 0, "", "", false
	}
	return code, o.String(), e.String(), true
}

func rawOutput(stdout, stderr string) string {
	if s := strings.TrimSpace(stdout); s != "" {
		return s
	}
	return strings.TrimSpace(stderr)
}

// RunRuff is _run_ruff.
func RunRuff(path, root string, fix bool) []string {
	args := []string{"check"}
	if fix {
		args = append(args, "--fix")
	}
	code, out, errOut, ok := tool(root, "ruff", append(args, path)...)
	if !ok {
		return []string{"ruff not found in PATH. Please install it with 'pip install ruff'."}
	}
	if code != 0 {
		if raw := rawOutput(out, errOut); raw != "" {
			return splitLines(raw)
		}
	}
	return []string{}
}

// RunMypy is _run_mypy.
func RunMypy(path, root string) []string {
	code, out, errOut, ok := tool(root, "mypy", "--strict", "--ignore-missing-imports", "--no-error-summary", path)
	if !ok {
		return []string{"mypy not found in PATH. Please install it with 'pip install mypy'."}
	}
	res := []string{}
	if code != 0 {
		if raw := rawOutput(out, errOut); raw != "" {
			for _, l := range splitLines(raw) {
				if !mypySummary.MatchString(l) {
					res = append(res, l)
				}
			}
		}
	}
	return res
}

// PathFromPayload is _path_from_payload.
func PathFromPayload(payload any) string {
	o, ok := payload.(*jsonx.Object)
	if !ok {
		return ""
	}
	var cands []any
	add := func(src *jsonx.Object) {
		v, _ := src.Get("file_path")
		cands = append(cands, v)
		if l, _ := src.Get("file_paths"); pyjson.Truthy(l) {
			if list, ok := l.([]any); ok {
				cands = append(cands, list...)
			}
		}
	}
	if ti, ok := mustGet(o, "tool_input").(*jsonx.Object); ok {
		add(ti)
	}
	add(o)
	for _, c := range cands {
		if s, ok := c.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func mustGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func targetFromStdin(stdin io.Reader) string {
	if f, ok := stdin.(*os.File); ok && isTerminal(f) {
		return ""
	}
	if stdin == nil {
		return ""
	}
	b, _ := io.ReadAll(stdin)
	raw := strings.TrimSpace(string(b))
	if raw == "" {
		return ""
	}
	v, err := pyjson.Loads(raw)
	if err != nil {
		return ""
	}
	return PathFromPayload(v)
}

func line(o *jsonx.Object) string {
	return jsonx.Dumps(o, jsonx.Options{Indent: -1, EnsureASCII: true})
}

func run(ctx *cli.Context) error {
	jsonOut := ctx.Bool("json")
	fp := ""
	if ctx.HasArg("file_path") {
		fp = ctx.Arg("file_path")
	} else {
		fp = targetFromStdin(ctx.Stdin)
		if fp == "" {
			if jsonOut {
				ctx.Println(line(jsonx.NewObject("skipped", true, "reason", "No file path provided")))
			} else {
				rich.Print(ctx.Stdout, "[dim]Skipping:[/dim] no file path provided.")
			}
			return nil
		}
	}
	shown := PyPathStr(fp)
	if _, err := os.Stat(fp); err != nil {
		if jsonOut {
			ctx.Println(line(jsonx.NewObject("error", "File "+shown+" does not exist")))
		} else {
			rich.Print(ctx.Stdout, "[red]Error:[/red] File [cyan]"+shown+"[/cyan] does not exist.")
		}
		return cli.Exit(1)
	}
	if pySuffix(shown) != ".py" {
		if jsonOut {
			ctx.Println(line(jsonx.NewObject("skipped", true, "reason", "Not a Python file", "file", shown)))
		} else {
			rich.Print(ctx.Stdout, "[dim]Skipping:[/dim] [cyan]"+shown+"[/cyan] is not a Python file.")
		}
		return nil
	}
	root := project.Locate("")
	if root == "" {
		root, _ = os.Getwd()
	}
	target, _ := filepath.Abs(fp)
	if r, err := filepath.EvalSymlinks(target); err == nil {
		target = r
	}
	ruffErrs := RunRuff(target, root, ctx.Bool("fix"))
	mypyErrs := RunMypy(target, root)
	failed := len(ruffErrs)+len(mypyErrs) > 0
	switch {
	case jsonOut:
		ctx.Println(jsonx.Dumps(jsonx.NewObject("file", shown, "success", !failed, "ruff_errors", strList(ruffErrs), "mypy_errors", strList(mypyErrs)), jsonx.Pretty))
	case !failed:
		rich.Print(ctx.Stdout, "[green]✓[/green] [cyan]"+shown+"[/cyan] passed all checks.")
	default:
		w := ctx.Stdout
		rich.Print(w, "[red]✗[/red] [cyan]"+shown+"[/cyan] failed code quality checks:")
		if len(ruffErrs) > 0 {
			rich.Print(w, "\n[bold]Ruff Violations:[/bold]")
			for _, e := range ruffErrs {
				rich.Print(w, "  "+e)
			}
		}
		if len(mypyErrs) > 0 {
			rich.Print(w, "\n[bold]Mypy Type Errors:[/bold]")
			for _, e := range mypyErrs {
				rich.Print(w, "  "+e)
			}
		}
		rich.Print(w, "\n[yellow]Tip:[/yellow] Fix these errors before proceeding to ensure high code quality.")
	}
	if failed {
		return cli.Exit(1)
	}
	return nil
}

// pySuffix is PurePosixPath.suffix.
func pySuffix(p string) string {
	name := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		name = p[i+1:]
	}
	if name == "." || name == ".." || strings.HasSuffix(name, ".") {
		return ""
	}
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return ""
	}
	return name[i:]
}

func strList(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}
