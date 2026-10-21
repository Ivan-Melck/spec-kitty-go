package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// charter bundle validate (specify_cli.cli.commands.charter_bundle).

func init() { registerCharterLeaf(charterBundleCmd) }

func charterBundleCmd() *cli.Command {
	g := &cli.Command{Name: "bundle", Short: "Charter bundle validation commands."}
	g.InvokeWithoutCommand = true
	g.Run = func(ctx *cli.Context) error {
		cli.WriteHelp(ctx.Stdout, ctx.Cmd)
		return cli.Exit(2)
	}
	g.Add(&cli.Command{
		Name:  "validate",
		Short: "Validate the charter bundle against CharterBundleManifest v1.0.0.",
		Flags: []*cli.Flag{{Name: "json", Kind: cli.Bool, Help: "Emit structured JSON to stdout instead of a human-readable report."}},
		Run:   runBundleValidate,
	})
	return g
}

// canonicalRepoRoot is charter.resolution.resolve_canonical_repo_root.
func canonicalRepoRoot(start string) (string, error) {
	abs, _ := filepath.Abs(start)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	common, err := gitx.CommonDir(abs)
	if err != nil {
		return "", fmt.Errorf("Path PosixPath('%s') is not inside a git repository. Charter resolution requires a git-tracked project root.", abs)
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	if out, err := gitx.Run(abs, "--git-dir", common, "config", "--get", "core.worktree"); err == nil && strings.TrimSpace(out) != "" {
		wt := strings.TrimSpace(out)
		if !filepath.IsAbs(wt) {
			wt = filepath.Join(common, wt)
		}
		return filepath.Clean(wt), nil
	}
	return filepath.Dir(common), nil
}

func runBundleValidate(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonMode := ctx.Bool("json")
	cwd, _ := os.Getwd()
	root, err := canonicalRepoRoot(cwd)
	if err != nil {
		if jsonMode {
			jsonOut(w, jsonx.NewObject("result", "error", "success", false, "error", err.Error()), jsonx.Line)
		} else {
			cprint(ctx.Stderr, "%s", "[red]Error:[/red] "+err.Error())
		}
		return cli.Exit(2)
	}
	compat := ""
	if fileExists(charter.Path(root, charter.CharterYAML)) {
		compat = charter.BundleCompatibilityMessage(root)
	}
	tracked := []string{charter.CharterMD, charter.CharterYAML}
	var present, missing []string
	for _, rel := range tracked {
		if !fileExists(filepath.Join(root, rel)) || !gitx.OK(root, "ls-files", "--error-unmatch", "--", rel) {
			missing = append(missing, rel)
		} else {
			present = append(present, rel)
		}
	}
	var outScope, warnings []string
	cd := charter.Path(root, charter.Dir)
	var all []string
	filepath.WalkDir(cd, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			all = append(all, filepath.ToSlash(rel))
		}
		return nil
	})
	sortPyPaths(all)
	for _, rel := range all {
		if rel == charter.CharterMD || rel == charter.CharterYAML {
			continue
		}
		outScope = append(outScope, rel)
		if rel == ".kittify/charter/context-state.json" {
			warnings = append(warnings, "File '.kittify/charter/context-state.json' is present but out of v1.0.0 manifest scope (runtime state written by build_charter_context); leaving untouched.")
		} else {
			warnings = append(warnings, "File '"+rel+"' is present but out of v1.0.0 manifest scope; leaving untouched.")
		}
	}
	compliant := len(missing) == 0
	sidecar := charter.ProvenanceValidationErrors(root)
	synth := charter.ValidateSynthesisState(root)
	var errs []string
	if compat != "" {
		errs = append(errs, "compatibility: "+compat)
	}
	for _, e := range sidecar {
		errs = append(errs, "provenance: "+e)
	}
	for _, e := range synth.Errors {
		errs = append(errs, "synthesis_state: "+e)
	}
	passed := compliant && compat == "" && len(sidecar) == 0 && synth.Passed()
	result := "failure"
	if passed {
		result = "success"
	}
	report := jsonx.NewObject("result", result, "canonical_root", root, "manifest_schema_version", "2.0.0", "bundle_compliant", compliant,
		"tracked_files", jsonx.NewObject("expected", strs(tracked), "present", strs(present), "missing", strs(missing)),
		"derived_files", jsonx.NewObject("expected", []any{}, "present", []any{}, "missing", []any{}),
		"gitignore", jsonx.NewObject("expected_entries", []any{}, "present_entries", []any{}, "missing_entries", []any{}),
		"out_of_scope_files", strs(outScope), "warnings", strs(warnings), "errors", strs(errs),
		"synthesis_state", jsonx.NewObject("present", synth.Present, "passed", synth.Passed(), "errors", strs(synth.Errors), "warnings", strs(synth.Warnings)),
		"passed", passed)
	if jsonMode {
		jsonOut(w, report, jsonx.Pretty)
	} else {
		cprint(w, "[bold]Charter bundle validation[/bold]")
		cprintRaw(w, "  Canonical root: "+root)
		cprintRaw(w, "  Manifest schema: 2.0.0")
		cprintRaw(w, "")
		cprintRaw(w, "  Tracked files:")
		for _, rel := range tracked {
			if contains(present, rel) {
				cprint(w, "%s", "    [green]\\[OK][/green] "+rel)
			} else {
				cprint(w, "%s", "    [red]\\[MISSING][/red] "+rel)
			}
		}
		cprintRaw(w, "")
		cprintRaw(w, "  Derived files (v1.0.0 scope):")
		cprintRaw(w, "")
		cprint(w, "%s", "  Gitignore:\n    [green]\\[OK][/green] 0 required entries present")
		cprintRaw(w, "")
		if len(outScope) > 0 {
			cprintRaw(w, "  Out-of-scope files present (informational):")
			for _, rel := range outScope {
				cprintRaw(w, "    "+rel)
			}
			cprintRaw(w, "")
		}
		if compliant {
			cprint(w, "[green]Bundle is compliant (v1.0.0).[/green]")
		} else {
			cprint(w, "[red]Bundle is NOT compliant (v1.0.0).[/red]")
		}
		if synth.Present {
			if synth.Passed() {
				cprint(w, "[green]Synthesis state: valid (all artifacts have provenance).[/green]")
			} else {
				cprint(w, "[red]Synthesis state: INVALID.[/red]")
				for _, e := range synth.Errors {
					cprint(w, "%s", "  [red]• "+uconsoleEscape(e)+"[/red]")
				}
			}
		} else {
			cprint(w, "[dim]Synthesis state: not present (legacy bundle).[/dim]")
		}
		if len(errs) > 0 {
			fmt.Fprintln(ctx.Stderr)
			for _, e := range errs {
				cprint(ctx.Stderr, "%s", "[red]Validation error:[/red] "+uconsoleEscape(e))
			}
		}
	}
	if passed {
		return cli.Exit(0)
	}
	return cli.Exit(1)
}

func contains(l []string, s string) bool {
	i := sort.SearchStrings(l, s)
	if i < len(l) && l[i] == s {
		return true
	}
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
