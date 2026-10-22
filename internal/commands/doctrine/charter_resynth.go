package doctrine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

// This file ports `charter activate/deactivate --resynthesize`
// (activate.run_full_synthesize, _resynthesis_preflight.preflight_resynthesis)
// and the activate-time tension warnings (_render_tension_warnings).

// insideGitWorktree is generate._is_inside_git_worktree.
func insideGitWorktree(root string) bool {
	out, err := gitx.Run(root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// interviewPreflightError is generate._load_interview_for_generate with
// from_interview=True: the missing / malformed answers refusals.
func interviewPreflightError(root string) error {
	answers := charter.InterviewPath(root)
	if charter.ReadInterview(answers) != nil {
		return nil
	}
	rel, _ := filepath.Rel(root, answers)
	if _, err := os.Stat(answers); err == nil {
		return fmt.Errorf("Charter interview answers at %s are present but malformed — the file is not readable as a mapping (invalid YAML, or a non-mapping top-level value). Repair or delete it, re-run `spec-kitty charter interview --defaults` to rewrite it, or pass `--no-from-interview` to generate from defaults.", rel)
	}
	return fmt.Errorf("No charter interview answers found at %s. Run `/spec-kitty.charter` inside your coding agent (Claude Code, Codex, Cursor) so it can capture guidance, run `spec-kitty charter interview --defaults` here for a canned bootstrap, or pass `--no-from-interview` to generate from defaults explicitly.", rel)
}

// preflightResynthesis is preflight_resynthesis: resolve the complete future
// activation set before any activation write.
func preflightResynthesis(root, kind, id string, scope *cascadeScope) error {
	if !insideGitWorktree(root) {
		return errors.New("Resynthesis requires a Git repository. Run `git init` before activation.")
	}
	if err := interviewPreflightError(root); err != nil {
		return err
	}
	requested := map[string][]string{}
	include := func(token, ident string) {
		if token == dct.MissionTypeToken {
			return
		}
		key := dct.YAMLKey(token)
		requested[key] = append(requested[key], ident)
	}
	include(kind, id)
	if scope != nil && kind != dct.MissionTypeToken {
		if src := charter.SourceURN(root, kind, id); src != "" {
			if g, err := charter.LoadOrgValidatedGraph(root, charter.GraphOptions{}); err == nil {
				act, _ := charter.CascadeRefs(g, src)
				ix := charter.NewConfigIDIndex(root)
				for k, ids := range act {
					if !scope.selects(k) {
						continue
					}
					for _, cid := range ids {
						include(strings.ReplaceAll(k, "_", "-"), ix.ConfigID(k+":"+cid))
					}
				}
			}
		}
	}
	sel, err := charter.FutureSelections(root, requested)
	if err != nil {
		return err
	}
	if err := charter.PreflightActivationRoots(root, sel); err != nil {
		return err
	}
	if err := charter.PreflightProjectLayer(root, app.Version); err != nil {
		return fmt.Errorf("Cannot resynthesize the requested activation: %s", err.Error())
	}
	return nil
}

// runFullSynthesize is run_full_synthesize: `charter generate --force`
// (from the interview, profile minimal) then `charter synthesize` (adapter
// generated, no prune), both scoped to root.
func runFullSynthesize(ctx *cli.Context, root string) error {
	if cwd, err := os.Getwd(); err == nil {
		if err := os.Chdir(root); err == nil {
			defer os.Chdir(cwd)
		}
	}
	io := cli.IO{Stdin: ctx.Stdin, Stdout: ctx.Stdout, Stderr: ctx.Stderr}
	if err := cli.Invoke(charterGenerateCmd(), []string{"--from-interview", "--profile", "minimal", "--force"}, io); err != nil {
		return err
	}
	return cli.Invoke(charterSynthesizeCmd(), []string{"--adapter", "generated"}, io)
}

// renderTensionWarnings is activate._render_tension_warnings.
func renderTensionWarnings(ctx *cli.Context, root string) {
	findings, ok := charter.ScanUnreconciledTensions(root)
	if !ok {
		return
	}
	for _, f := range findings {
		cprint(ctx.Stdout, "[yellow]Warning[/yellow]: %s is in tension with %s. Resolve by: (1) deactivating one side, or (2) activating a reconciler that bridges both.", f.A, f.B)
	}
}

// loadCascadeGraph is the activate/deactivate cascade's
// load_validated_graph(repo_root, org_roots=chain, org_fragments=...): a
// DRGValidationError is not caught upstream (an uncaught traceback); a load
// failure skips the cascade render (nil graph, nil error).
func loadCascadeGraph(ctx *cli.Context, root string) (*dct.Graph, error) {
	g, err := charter.LoadOrgValidatedGraph(root, charter.GraphOptions{})
	if err != nil {
		if charter.IsUnexpectedError(err) {
			return nil, pyCrash(ctx, charter.PyExceptionClass(err), err.Error())
		}
		return nil, nil
	}
	return g, nil
}
