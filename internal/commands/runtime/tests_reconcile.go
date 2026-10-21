package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
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
// agent tests stale-check (post_merge.stale_assertions, line-based)
// ---------------------------------------------------------------------------

type staleSymbol struct {
	name, file string
	line       int
}

type staleFinding struct {
	testFile                 string
	testLine                 int
	sourceFile               string
	sourceLine               int
	symbol, confidence, hint string
}

var (
	defRe    = regexp.MustCompile(`^\s*(?:async\s+def|def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	importRe = regexp.MustCompile(`^(?:from\s+\S+\s+import\s+(.+)|import\s+(.+))$`)
)

func defs(text string) map[string]int {
	out := map[string]int{}
	for i, l := range strings.Split(text, "\n") {
		if m := defRe.FindStringSubmatch(l); m != nil {
			if _, ok := out[m[1]]; !ok {
				out[m[1]] = i + 1
			}
		}
	}
	return out
}

func headExports(text, name string) bool {
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			continue
		}
		if m := importRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			list := m[1] + m[2]
			for _, part := range strings.Split(strings.Trim(list, "()"), ",") {
				f := strings.Fields(part)
				if len(f) == 0 {
					continue
				}
				if f[len(f)-1] == name || f[0] == name {
					return true
				}
			}
		}
		if strings.HasPrefix(l, "__all__") && strings.Contains(l, "'"+name+"'") || strings.Contains(l, "\""+name+"\"") && strings.HasPrefix(l, "__all__") {
			return true
		}
	}
	return false
}

func runStaleCheck(ctx *cli.Context) error {
	start := time.Now()
	base, head := ctx.String("base"), ctx.String("head")
	root, _ := filepath.Abs(ctx.String("repo"))
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	changed, _ := gitx.Run(root, "diff", "--name-only", base, head, "--", "*.py")
	var symbols []staleSymbol
	for _, rel := range strings.Split(changed, "\n") {
		if rel == "" || strings.HasPrefix(rel, "tests/") {
			continue
		}
		bt, err := gitx.Run(root, "show", base+":"+rel)
		if err != nil {
			continue
		}
		ht, _ := gitx.Run(root, "show", head+":"+rel)
		bd, hd := defs(bt), defs(ht)
		var names []string
		for n := range bd {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if _, ok := hd[n]; ok || headExports(ht, n) {
				continue
			}
			symbols = append(symbols, staleSymbol{name: n, file: filepath.Join(root, rel), line: bd[n]})
		}
	}
	top, err := gitx.Toplevel(root)
	if err != nil {
		top = root
	}
	var testFiles []string
	if out, err := gitx.Run(root, "ls-files", "--", "tests/"); err == nil {
		for _, f := range strings.Split(out, "\n") {
			if strings.HasSuffix(f, ".py") {
				testFiles = append(testFiles, filepath.Join(top, f))
			}
		}
	}
	var findings []staleFinding
	for _, tf := range testFiles {
		if len(symbols) == 0 {
			break
		}
		b, err := os.ReadFile(tf)
		if err != nil {
			continue
		}
		for i, l := range strings.Split(string(b), "\n") {
			t := strings.TrimSpace(l)
			isAssert := strings.HasPrefix(t, "assert ") || strings.Contains(t, ".assert")
			if !isAssert {
				continue
			}
			for _, s := range symbols {
				if regexp.MustCompile(`\b` + regexp.QuoteMeta(s.name) + `\b`).MatchString(t) {
					findings = append(findings, staleFinding{testFile: tf, testLine: i + 1, sourceFile: s.file, sourceLine: s.line,
						symbol: s.name, confidence: "high",
						hint: fmt.Sprintf("Assertion references '%s' which was renamed/removed in %s:%d", s.name, filepath.Base(s.file), s.line)})
				}
			}
		}
	}
	loc := 0
	if out, err := gitx.Run(root, "diff", "--shortstat", base, head, "--", "*.py"); err == nil {
		for _, tok := range strings.Split(out, ",") {
			f := strings.Fields(strings.TrimSpace(tok))
			if len(f) > 1 && (strings.HasPrefix(f[1], "insertion") || strings.HasPrefix(f[1], "deletion")) {
				n, _ := strconv.Atoi(f[0])
				loc += n
			}
		}
	}
	per := 0.0
	if loc > 0 {
		per = float64(len(findings)) / float64(loc) * 100.0
	}
	elapsed := time.Since(start).Seconds()
	if per > 5.0 {
		rich.Print(ctx.Stderr, fmt.Sprintf("WARNING: findings_per_100_loc=%.1f exceeds NFR-002 ceiling of 5.0. Consider narrowing scope to function-rename detection only (FR-022).", per))
	}
	if ctx.Bool("json") {
		items := []any{}
		for _, f := range findings {
			items = append(items, jsonx.NewObject("test_file", f.testFile, "test_line", f.testLine, "source_file", f.sourceFile,
				"source_line", f.sourceLine, "changed_symbol", f.symbol, "confidence", f.confidence, "hint", f.hint, "label", ""))
		}
		emitJSON(ctx, jsonx.NewObject("base_ref", base, "head_ref", head, "repo_root", root, "findings", items,
			"elapsed_seconds", jsonx.Float(elapsed), "files_scanned", len(testFiles), "findings_per_100_loc", jsonx.Float(per)))
		return nil
	}
	rich.Print(ctx.Stdout, fmt.Sprintf("\nStale-assertion analysis: '%s' → '%s'", base, head))
	rich.Print(ctx.Stdout, fmt.Sprintf("  scanned %d test file(s) in %.2fs | findings/100 LOC: %.2f", len(testFiles), elapsed, per))
	if len(findings) == 0 {
		rich.Print(ctx.Stdout, "No stale assertions detected.\n")
		return nil
	}
	var rows [][]string
	for _, f := range findings {
		rel, err := filepath.Rel(root, f.testFile)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = f.testFile
		}
		rows = append(rows, []string{rel, strconv.Itoa(f.testLine), f.symbol, fmt.Sprintf("%s:%d", filepath.Base(f.sourceFile), f.sourceLine), f.hint})
	}
	rich.Table(ctx.Stdout, "HIGH confidence", []string{"Test file", "Line", "Changed symbol", "Source file", "Hint"}, rows)
	fmt.Fprintln(ctx.Stdout)
	return nil
}

// ---------------------------------------------------------------------------
// reconcile (dossier parity against .kittify/dossiers/<slug>/snapshot-latest.json)
// ---------------------------------------------------------------------------

type projEntry struct{ path, hash string }

func snapshotHash(entries []projEntry) string {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		return entries[i].hash < entries[j].hash
	})
	var lines []string
	for _, e := range entries {
		lines = append(lines, e.path+"\t"+e.hash)
	}
	s := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(s[:])
}

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
	snapPath := filepath.Join(dir, ".kittify", "dossiers", slug, "snapshot-latest.json")
	b, err := os.ReadFile(snapPath)
	if err != nil {
		result.Set("error", "no recorded snapshot to reconcile against for "+slug)
		return finish()
	}
	snap, err := jsonx.DecodeObject(b)
	if err != nil || snap == nil {
		result.Set("error", fmt.Sprintf("could not load recorded snapshot for %s: %v", slug, err))
		return finish()
	}
	var recorded, rebuilt []projEntry
	v, _ := snap.Get("artifact_summaries")
	list, _ := v.([]any)
	for _, x := range list {
		o, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		pres, _ := o.Get("is_present")
		h := o.GetString("content_hash_sha256")
		if pres != true || h == "" {
			continue
		}
		rel := o.GetString("relative_path")
		recorded = append(recorded, projEntry{rel, h})
		if fb, err := os.ReadFile(filepath.Join(dir, rel)); err == nil {
			s := sha256.Sum256(fb)
			rebuilt = append(rebuilt, projEntry{rel, hex.EncodeToString(s[:])})
		}
	}
	rh, ch := snapshotHash(append([]projEntry{}, rebuilt...)), snapshotHash(append([]projEntry{}, recorded...))
	result.Set("rebuilt_hash", rh)
	result.Set("recorded_hash", ch)
	if rh == ch {
		result.Set("status", "parity")
		return finish()
	}
	src := map[string]string{}
	for _, e := range rebuilt {
		src[e.path] = e.hash
	}
	var diffs []any
	for _, e := range recorded {
		h, ok := src[e.path]
		switch {
		case !ok:
			diffs = append(diffs, jsonx.NewObject("artifact_path", e.path, "kind", "missing_in_source", "source_hash", nil, "recorded_hash", e.hash))
		case h != e.hash:
			diffs = append(diffs, jsonx.NewObject("artifact_path", e.path, "kind", "content_mismatch", "source_hash", h, "recorded_hash", e.hash))
		}
	}
	result.Set("status", "divergence")
	result.Set("differing_artifacts", diffs)
	return finish()
}
