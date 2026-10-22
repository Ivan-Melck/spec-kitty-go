package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/dossier"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/staleassert"
)

const staleCheckLong = `Detect test assertions likely invalidated by source changes between two refs.

Compares BASE..HEAD and reports assertions in the test suite that reference
symbols (functions, classes, string literals) that were renamed or removed in
the source diff.  Uses AST analysis only — no regex on test text, no test
execution.

Confidence levels:
  high   — identifier referenced directly inside Assert or assert* call
  medium — identifier appears anywhere in an assertion node
  low    — string literal matches a Constant in an assertion-bearing position
  info   — message-content check (literal checked against captured
           diagnostic text); skipped from CI noise, surfaced for manual
           review (#3957)`

func init() {
	app.Register(func(root *cli.Command) {
		agent := app.Group(root, "agent", agentHelp)
		tests := app.Group(agent, "tests", "Test-related commands for AI agents")
		tests.NoArgsIsError = true
		tests.Add(&cli.Command{
			Name: "stale-check", Short: "Detect test assertions likely invalidated by source changes between two refs.", Long: staleCheckLong,
			Flags: []*cli.Flag{
				{Name: "base", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Base git ref for the diff"},
				{Name: "head", Kind: cli.String, Metavar: "<str>", Default: "HEAD", Help: "Head git ref for the diff"},
				{Name: "repo", Kind: cli.String, Metavar: "<path>", Default: ".", Help: "Repository root (default: current directory)"},
				{Name: "json", Kind: cli.Bool, Help: "Emit JSON instead of human-readable text"},
			},
			Run: runStaleCheck,
		})
		root.Add(&cli.Command{
			Name: "reconcile", Short: "Reconcile a mission dossier against its recorded snapshot (exit 0=parity, non-zero=divergence).",
			Long: "Reconcile a mission dossier against its recorded snapshot (exit 0=parity, non-zero=divergence).",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Required: true, Help: "Mission slug to reconcile against its recorded snapshot"},
				{Name: "json", Kind: cli.Bool, Help: "Emit a machine-readable JSON result"},
			},
			Run: runReconcile,
		})
	})
}

// ---------------------------------------------------------------------------
// agent tests stale-check (post_merge.stale_assertions via internal/staleassert)
// ---------------------------------------------------------------------------

func runStaleCheck(ctx *cli.Context) error {
	base, head := ctx.String("base"), ctx.String("head")
	root, _ := filepath.Abs(ctx.String("repo"))
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	rep, err := staleassert.RunCheck(base, head, root)
	if err != nil {
		ctx.Eprintf("Error: %v\n", err)
		return cli.Exit(1)
	}
	if rep.FindingsPer100LOC > staleassert.FPCeiling {
		rich.Print(ctx.Stderr, fmt.Sprintf("WARNING: findings_per_100_loc=%.1f exceeds NFR-002 ceiling of %.1f. Consider narrowing scope to function-rename detection only (FR-022).", rep.FindingsPer100LOC, staleassert.FPCeiling))
	}
	if ctx.Bool("json") {
		items := []any{}
		for _, f := range rep.Findings {
			items = append(items, jsonx.NewObject("test_file", f.TestFile, "test_line", f.TestLine, "source_file", f.SourceFile,
				"source_line", f.SourceLine, "changed_symbol", f.ChangedSymbol, "confidence", f.Confidence, "hint", f.Hint, "label", f.Label))
		}
		fmt.Fprintln(ctx.Stdout, jsonx.Dumps(jsonx.NewObject("base_ref", base, "head_ref", head, "repo_root", root, "findings", items,
			"elapsed_seconds", jsonx.Float(rep.ElapsedSeconds), "files_scanned", rep.FilesScanned, "findings_per_100_loc", jsonx.Float(rep.FindingsPer100LOC)), compact))
		return nil
	}
	rich.Print(ctx.Stdout, fmt.Sprintf("\nStale-assertion analysis: %s → %s", pyjson.ReprString(base), pyjson.ReprString(head)))
	rich.Print(ctx.Stdout, fmt.Sprintf("  scanned %d test file(s) in %.2fs | findings/100 LOC: %.2f", rep.FilesScanned, rep.ElapsedSeconds, rep.FindingsPer100LOC))
	if len(rep.Findings) == 0 {
		rich.Print(ctx.Stdout, "No stale assertions detected.\n")
		return nil
	}
	for _, level := range []string{"high", "medium", "low", "info"} {
		var rows [][]string
		for _, f := range rep.Findings {
			if f.Confidence != level {
				continue
			}
			rel, err := filepath.Rel(root, f.TestFile)
			if err != nil || strings.HasPrefix(rel, "..") {
				rel = f.TestFile
			}
			rows = append(rows, []string{rel, strconv.Itoa(f.TestLine), f.ChangedSymbol, fmt.Sprintf("%s:%d", filepath.Base(f.SourceFile), f.SourceLine), f.Hint})
		}
		if len(rows) == 0 {
			continue
		}
		title := strings.ToUpper(level) + " confidence"
		if level == "info" {
			title = "INFO — message-content assertions (skipped from CI noise; review manually if diagnostic text changed)"
		}
		var buf strings.Builder
		rich.Table(&buf, title, []string{"Test file", "Line", "Changed symbol", "Source file", "Hint"}, rows)
		fmt.Fprint(ctx.Stdout, rightJustifyColumn(buf.String(), 1))
	}
	fmt.Fprintln(ctx.Stdout)
	return nil
}

// rightJustifyColumn right-aligns column col of a rendered table's body
// rows (rich justify="right").
func rightJustifyColumn(table string, col int) string {
	lines := strings.Split(table, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "│") {
			continue
		}
		parts := strings.Split(l, "│")
		if len(parts) < col+3 {
			continue
		}
		cell := parts[col+1]
		w := len([]rune(cell))
		v := strings.TrimSpace(cell)
		if v == "" {
			continue
		}
		parts[col+1] = strings.Repeat(" ", w-len([]rune(v))-1) + v + " "
		lines[i] = strings.Join(parts, "│")
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// reconcile (dossier parity against .kittify/dossiers/<slug>/snapshot-latest.json)
// ---------------------------------------------------------------------------

func runReconcile(ctx *cli.Context) error {
	slug := ctx.String("mission")
	jsonMode := ctx.Bool("json")
	result := jsonx.NewObject("mission", slug, "status", "error", "rebuilt_hash", nil, "recorded_hash", nil,
		"differing_artifacts", []any{}, "error", nil)
	finish := func() error {
		st := result.GetString("status")
		if jsonMode {
			emitJSON(ctx, result)
		} else {
			switch st {
			case "parity":
				rich.Print(ctx.Stdout, fmt.Sprintf("PARITY %s — dossier matches the recorded snapshot", slug))
			case "divergence":
				v, _ := result.Get("differing_artifacts")
				l, _ := v.([]any)
				rich.Print(ctx.Stdout, fmt.Sprintf("DIVERGENCE %s — %d artifact(s) differ:", slug, len(l)))
				for _, x := range l {
					o := x.(*jsonx.Object)
					rich.Print(ctx.Stdout, fmt.Sprintf("  • %s (%s)", o.GetString("artifact_path"), o.GetString("kind")))
				}
			default:
				rich.Print(ctx.Stdout, fmt.Sprintf("ERROR %s — %s", slug, result.GetString("error")))
			}
		}
		switch st {
		case "parity":
			return nil
		case "divergence":
			return cli.Exit(1)
		}
		return cli.Exit(2)
	}
	root := locateRoot()
	if root == "" {
		result.Set("error", "not in a spec-kitty project (no project root resolved)")
		return finish()
	}
	dir := coord.Resolve(root, slug).StatusDir()
	if _, err := os.Stat(dir); err != nil {
		result.Set("error", "mission dossier not found: "+slug)
		return finish()
	}
	snap, err := dossier.LoadSnapshot(dir, slug)
	if err != nil {
		result.Set("error", fmt.Sprintf("could not load recorded snapshot for %s: %v", slug, err))
		return finish()
	}
	if snap == nil {
		result.Set("error", "no recorded snapshot to reconcile against for "+slug)
		return finish()
	}
	source, err := dossier.SourceProjection(dir)
	if err != nil {
		result.Set("error", fmt.Sprintf("could not rebuild dossier from source for %s: %v", slug, err))
		return finish()
	}
	res := dossier.Reconcile(source, dossier.RecordedProjection(snap))
	opt := func(p *string) any {
		if p == nil {
			return nil
		}
		return *p
	}
	result.Set("status", res.Status)
	result.Set("rebuilt_hash", opt(res.RebuiltHash))
	result.Set("recorded_hash", opt(res.RecordedHash))
	diffs := []any{}
	for _, d := range res.Differing {
		diffs = append(diffs, jsonx.NewObject("artifact_path", d.Path, "kind", d.Kind, "source_hash", opt(d.SourceHash), "recorded_hash", opt(d.Record)))
	}
	result.Set("differing_artifacts", diffs)
	if res.Error != "" {
		result.Set("error", res.Error)
	}
	return finish()
}
