package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/cli"
)

func init() {
	app.Register(func(root *cli.Command) {
		g := app.Group(root, "workflow", "Manage mission workflow definitions")
		rootFlag := func(help string) *cli.Flag {
			return &cli.Flag{Name: "project-root", Kind: cli.String, Metavar: "<path>", Default: ".", Help: help}
		}
		g.Add(
			&cli.Command{
				Name:  "list",
				Short: "List workflow ids available to a project.",
				Flags: []*cli.Flag{rootFlag("Project root used for .kittify workflow discovery.")},
				Run:   guarded(runList),
			},
			&cli.Command{
				Name:  "export",
				Short: "Export a resolvable workflow YAML file.",
				Args: []*cli.Arg{
					{Name: "workflow_id", Metavar: "{workflow_id}", ErrorHint: "workflow_id", Required: true, Help: "Workflow id to export."},
					{Name: "output", Metavar: "{output}", ErrorHint: "output", Required: true, Help: "Destination file or directory."},
				},
				Flags: []*cli.Flag{
					rootFlag("Project root used for .kittify workflow discovery."),
					{Name: "force", Kind: cli.Bool, Help: "Overwrite an existing destination file."},
				},
				Run: guarded(runExport),
			},
			&cli.Command{
				Name:  "import",
				Short: "Import a workflow YAML into `.kittify/overrides/workflows`.",
				Args: []*cli.Arg{
					{Name: "source", Metavar: "{source}", ErrorHint: "source", Required: true, Help: "Workflow YAML file to import."},
				},
				Flags: []*cli.Flag{
					rootFlag("Project root that receives the workflow override."),
					{Name: "force", Kind: cli.Bool, Help: "Overwrite an existing workflow file."},
				},
				Run: guarded(runImport),
			},
		)
	})
}

// guarded is the global GuardedReadError presentation hook: "Error: <msg>"
// on stderr, exit 1.
func guarded(run func(*cli.Context) error) func(*cli.Context) error {
	return func(ctx *cli.Context) error {
		err := run(ctx)
		var ge *GuardedError
		if errors.As(err, &ge) {
			fmt.Fprintf(ctx.Stderr, "Error: %s\n", ge.Msg)
			return cli.Exit(1)
		}
		return err
	}
}

// PyPath renders str(pathlib.PurePosixPath(s)): empty and "." segments and
// duplicate/trailing slashes dropped, ".." kept.
func PyPath(s string) string {
	abs := strings.HasPrefix(s, "/")
	var parts []string
	for _, p := range strings.Split(s, "/") {
		if p != "" && p != "." {
			parts = append(parts, p)
		}
	}
	out := strings.Join(parts, "/")
	switch {
	case abs:
		return "/" + out
	case out == "":
		return "."
	}
	return out
}

// resolve is Path.resolve() (non-strict): absolute, symlinks followed for
// the longest existing prefix.
func resolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	dir, base := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if dir == abs {
		return abs
	}
	return filepath.Join(resolve(dir), base)
}

func runList(ctx *cli.Context) error {
	for _, id := range List(resolve(ctx.String("project-root"))) {
		fmt.Fprintln(ctx.Stdout, id)
	}
	return nil
}

func runExport(ctx *cli.Context) error {
	id := ctx.Arg("workflow_id")
	src, err := Resolve(id, resolve(ctx.String("project-root")))
	if err != nil {
		return err
	}
	if _, err := Load(src, id); err != nil {
		return err
	}
	dest := PyPath(ctx.Arg("output"))
	if st, err := os.Stat(dest); err == nil && st.IsDir() {
		dest = filepath.Join(dest, id+".yaml")
		if strings.HasPrefix(dest, "//") {
			dest = dest[1:]
		}
	}
	if err := copyWorkflow(ctx, src, dest); err != nil {
		return err
	}
	fmt.Fprintln(ctx.Stdout, dest)
	return nil
}

func runImport(ctx *cli.Context) error {
	src := Source{Path: PyPath(ctx.Arg("source"))}
	seq, err := Load(src, "")
	if err != nil {
		return err
	}
	dest := filepath.Join(overrideRoot(resolve(ctx.String("project-root"))), seq.WorkflowID+".yaml")
	if err := copyWorkflow(ctx, src, dest); err != nil {
		return err
	}
	fmt.Fprintln(ctx.Stdout, dest)
	return nil
}

func copyWorkflow(ctx *cli.Context, src Source, dest string) error {
	if _, err := os.Stat(dest); err == nil && !ctx.Bool("force") {
		return cli.Usage(ctx.Cmd, "Invalid value: Destination exists: %s", dest)
	}
	content, err := src.Read()
	if err != nil {
		return &GuardedError{fmt.Sprintf("workflow file %s is not valid: %v", src.Path, err)}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o777); err != nil {
		return err
	}
	return atomicfile.Write(dest, content)
}
