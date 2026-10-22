package doctrine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// charter synthesize / resynthesize / preflight.

func init() {
	registerCharterLeaf(charterSynthesizeCmd)
	registerCharterLeaf(charterResynthesizeCmd)
	registerCharterLeaf(charterPreflightCmd)
}

const adapterHelp = "Adapter to use. 'generated' (default) validates agent-authored YAML under .kittify/charter/generated/. 'fixture' is offline/testing only."

// writeRootError is resolve_charter_write_root's LinkedWorktreeCharterWriteError.
func writeRootError() error {
	cwd, _ := os.Getwd()
	top, err1 := gitx.Toplevel(cwd)
	common, err2 := gitx.CommonDir(cwd)
	if err1 != nil || err2 != nil {
		return nil
	}
	if filepath.Clean(filepath.Dir(common)) != filepath.Clean(top) {
		return fmt.Errorf("Refusing charter write from linked git worktree %s: use a repository-root checkout or dedicated clone for charter authoring.", cwd)
	}
	return nil
}

func charterSynthesizeCmd() *cli.Command {
	return &cli.Command{
		Name:  "synthesize",
		Short: "Validate and promote agent-generated project-local doctrine artifacts.",
		Long:  synthesizeLong,
		Flags: []*cli.Flag{
			{Name: "adapter", Kind: cli.String, Default: "generated", Metavar: "<str>", Help: adapterHelp},
			{Name: "dry-run", Kind: cli.Bool, Help: "Stage and validate artifacts but do not promote to live tree. Also reports the reconciliation delta --prune would remove. Wins over --prune when both are given (preview only, no write)."},
			{Name: "prune", Kind: cli.Bool, Help: "Remove on-disk content the current run no longer targets and list every deletion. Without this flag, that content is preserved (default) unless it is orphaned (backing artifact deleted), which refuses instead of silently keeping a dangling reference."},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON"},
			{Name: "skip-code-evidence", Kind: cli.Bool, Help: "Skip code-reading evidence collection."},
			{Name: "skip-corpus", Kind: cli.Bool, Help: "Skip best-practice corpus loading."},
			{Name: "dry-run-evidence", Kind: cli.Bool, Help: "Print evidence summary and exit without running synthesis."},
		},
		Run: runCharterSynthesize,
	}
}

func synthFailure(ctx *cli.Context, jsonMode bool, adapter string, warnings []any, msg string) error {
	if jsonMode {
		jsonOut(ctx.Stdout, jsonx.NewObject("result", "failure", "adapter", jsonx.NewObject("id", adapter, "version", "unknown"),
			"written_artifacts", []any{}, "warnings", append(warnings, msg)), jsonx.SortedPretty)
	} else {
		cprint(ctx.Stdout, "%s", "[red]Error:[/red] "+uconsoleEscape(msg))
	}
	return cli.Exit(1)
}

// uconsoleEscape is rich.markup.escape.
func uconsoleEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '[' {
			j := i + 1
			if j < len(s) && (s[j] >= 'a' && s[j] <= 'z' || s[j] == '/' || s[j] == '#' || s[j] == '@') {
				b.WriteString("\\[")
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func seedArtifacts(paths []string) []any {
	out := []any{}
	for _, p := range paths {
		out = append(out, jsonx.NewObject("path", p, "kind", "seed", "slug", "provenance", "artifact_id", nil))
	}
	return out
}

func runCharterSynthesize(ctx *cli.Context) error {
	w := ctx.Stdout
	jsonMode, dryRun := ctx.Bool("json"), ctx.Bool("dry-run")
	adapter := ctx.String("adapter")
	warnings := []any{}
	if err := writeRootError(); err != nil {
		if jsonMode {
			return synthFailure(ctx, true, adapter, warnings, err.Error())
		}
		cprint(ctx.Stderr, "%s", "[red]Error:[/red] "+err.Error())
		return cli.Exit(1)
	}
	root := repoRoot()
	fresh := adapter == "generated" && !charter.HasGeneratedArtifacts(root) && !ctx.Bool("dry-run-evidence") && fileExists(charter.Path(root, charter.CharterYAML))
	if fresh {
		res, err := charter.SynthesizeProjectLayer(root, app.Version, dryRun)
		if err != nil {
			return synthHandleError(ctx, jsonMode, adapter, warnings, err)
		}
		if res != nil {
			if jsonMode {
				jsonOut(w, res, jsonx.SortedPretty)
				return nil
			}
			op := "preserved"
			if dryRun {
				op = "would preserve"
			}
			cprint(w, "%s", "[green]Charter synthesis[/green]: "+op+" project doctrine and provenance.")
			for _, x := range res.GetList("warnings") {
				cprint(w, "%s", "[yellow]Warning[/yellow]: "+fmt.Sprint(x))
			}
			return nil
		}
	}
	if fresh && !charter.CatalogIsEstablished(root) {
		if dryRun {
			planned, deletes := charter.PlannedFreshPaths(), charter.PlannedFreshDeletes(root)
			if jsonMode {
				jsonOut(w, jsonx.NewObject("result", "dry_run", "adapter", jsonx.NewObject("id", "fresh-seed", "version", app.Version),
					"written_artifacts", seedArtifacts(planned), "warnings", []any{}, "success", true, "mode", "fresh_project_seed_dry_run",
					"files_planned", strs(planned), "planned_deletes", strs(deletes),
					"note", "Fresh project + --dry-run: would materialize minimal .kittify/charter-packs/ (no files written). See issue #839."), jsonx.SortedPretty)
				return nil
			}
			cprint(w, "%s", "[yellow]Charter synthesis (fresh project, dry-run)[/yellow]: would materialize minimal .kittify/charter-packs/ (no files written).")
			for _, f := range planned {
				cprintRaw(w, "  • "+f)
			}
			for _, f := range deletes {
				cprintRaw(w, "  delete "+f)
			}
			return nil
		}
		written, err := charter.MaterializeFreshProjectLayer(root, app.Version)
		if err != nil {
			return synthFailure(ctx, jsonMode, adapter, warnings, "Unexpected error: "+err.Error())
		}
		if jsonMode {
			jsonOut(w, jsonx.NewObject("result", "success", "adapter", jsonx.NewObject("id", "fresh-seed", "version", app.Version),
				"written_artifacts", seedArtifacts(written), "warnings", []any{}, "success", true, "mode", "fresh_project_seed",
				"files_written", strs(written),
				"note", "Fresh project: no agent-authored YAML under .kittify/charter/generated/. Materialized minimal .kittify/charter-packs/ so the runtime can advance (see issue #839)."), jsonx.SortedPretty)
			return nil
		}
		cprint(w, "%s", "[green]Charter synthesis (fresh project)[/green]: minimal .kittify/charter-packs/ materialized.")
		for _, f := range written {
			cprintRaw(w, "  ✓ "+f)
		}
		printSynthesisCommitReminder(w, root)
		return nil
	}
	return synthesizeMain(ctx, root, adapter)
}

func printSynthesisCommitReminder(w interface{ Write([]byte) (int, error) }, root string) {
	cprint(w, "[yellow]Synthesis artifacts written; commit provenance before continuing:[/yellow]")
	recipe := "spec-kitty safe-commit " + strings.Join(charter.SynthesisArtifactPaths, " ") + " -m \"chore: charter synthesis artifacts\""
	if b := gitx.CurrentBranch(root); b != "" {
		recipe += " --to-branch " + b
	}
	cprintRaw(w, "  "+recipe)
}

func charterResynthesizeCmd() *cli.Command {
	return &cli.Command{
		Name:  "resynthesize",
		Short: "Regenerate a bounded set of project-local doctrine artifacts (partial resynthesis).",
		Long:  resynthesizeLong,
		Flags: []*cli.Flag{
			{Name: "topic", Kind: cli.String, Metavar: "<str>", Help: "Structured topic selector: <kind>:<slug> (project-local), <drg-urn> (built-in+project graph), or <interview-section-label>."},
			{Name: "list-topics", Kind: cli.Bool, Help: "List valid structured topic selectors and exit."},
			{Name: "adapter", Kind: cli.String, Default: "generated", Metavar: "<str>", Help: adapterHelp},
			{Name: "skip-code-evidence", Kind: cli.Bool, Help: "Skip code-reading evidence collection."},
			{Name: "skip-corpus", Kind: cli.Bool, Help: "Skip best-practice corpus loading."},
			{Name: "json", Kind: cli.Bool, Help: "Output JSON"},
		},
		Run: runCharterResynthesize,
	}
}

// ------------------------------------------------------------- preflight

type preflightCheck struct{ name, state, detail, remediation string }

func (c preflightCheck) object() *jsonx.Object {
	var rem any
	if c.remediation != "" {
		rem = c.remediation
	}
	return jsonx.NewObject("name", c.name, "state", c.state, "detail", c.detail, "remediation", rem)
}

var preflightPass = map[string]bool{"fresh": true, "skipped": true, "built_in_only": true}

func buildPreflightChecks(root string) []preflightCheck {
	fr := charter.ComputeFreshness(root)
	var out []preflightCheck
	for _, l := range [][2]string{{"charter_source", "charter source"}, {"synced_bundle", "synced bundle"}, {"synthesized_drg", "synthesized DRG"}} {
		sub := fr.GetObject(l[0])
		state := sub.GetString("state")
		detail := sub.GetString("detail")
		if detail == "" {
			detail = l[1] + " is " + state
			if lc := sub.GetString("last_change"); lc != "" {
				detail += " (last_change=" + lc + ")"
			}
		}
		out = append(out, preflightCheck{l[0], state, detail, sub.GetString("remediation")})
	}
	return out
}

func blockedReason(checks []preflightCheck) string {
	var lines []string
	for _, c := range checks {
		if preflightPass[c.state] {
			continue
		}
		if c.remediation != "" {
			lines = append(lines, c.name+" "+c.state+"; run `"+c.remediation+"`")
		} else {
			lines = append(lines, c.name+" "+c.state+": "+c.detail)
		}
	}
	if len(lines) == 0 {
		return "charter preflight failed; no non-passing check found (internal inconsistency)"
	}
	return strings.Join(lines, "\n")
}

func allPass(checks []preflightCheck) bool {
	for _, c := range checks {
		if !preflightPass[c.state] {
			return false
		}
	}
	return true
}

type preflightResult struct {
	passed, applied bool
	checks          []preflightCheck
	actions         []string
	blocked         string
	warnings        []string
}

func (r preflightResult) object() *jsonx.Object {
	cs := []any{}
	for _, c := range r.checks {
		cs = append(cs, c.object())
	}
	var br any
	if r.blocked != "" {
		br = r.blocked
	}
	o := jsonx.NewObject("passed", r.passed, "checks", cs, "auto_refresh_applied", r.applied, "auto_refresh_actions", strs(r.actions), "blocked_reason", br)
	if len(r.warnings) > 0 {
		o.Set("warnings", strs(r.warnings))
	}
	return o
}

func runRefreshStep(root string, args []string) (bool, string) {
	label := strings.Join(args[1:], " ")
	timeout := 30.0
	if v := os.Getenv("SPEC_KITTY_PREFLIGHT_TIMEOUT_SECS"); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil && f > 0 {
			timeout = f
		}
	}
	bin, err := spekittyBinary(args[0])
	if err != nil {
		return false, args[0] + " not on PATH; cannot run `" + strings.Join(args, " ") + "`"
	}
	cmd := exec.Command(bin, args[1:]...)
	cmd.Dir = root
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Start(); err != nil {
		return false, args[0] + " not on PATH; cannot run `" + strings.Join(args, " ") + "`"
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(time.Duration(timeout * float64(time.Second))):
		cmd.Process.Kill()
		return false, fmt.Sprintf("`%s` timed out after %.0fs", strings.Join(args, " "), timeout)
	case err := <-done:
		if err == nil {
			return true, ""
		}
	}
	first := ""
	if l := strings.Split(strings.TrimRight(se.String(), "\n"), "\n"); se.Len() > 0 && len(l) > 0 {
		first = strings.Split(se.String(), "\n")[0]
	}
	if first == "" && so.Len() > 0 {
		l := strings.Split(strings.TrimRight(so.String(), "\n"), "\n")
		first = l[len(l)-1]
	}
	return false, strings.TrimRight(fmt.Sprintf("%s failed (exit %d): %s", label, cmd.ProcessState.ExitCode(), first), ": ")
}

// spekittyBinary resolves cmd[0]: "spec-kitty" runs this executable.
func spekittyBinary(name string) (string, error) {
	if name == "spec-kitty" {
		if exe, err := os.Executable(); err == nil {
			return exe, nil
		}
	}
	return exec.LookPath(name)
}

// pyListRepr renders a Python list-of-str repr.
func pyListRepr(l []string) string {
	q := make([]string, len(l))
	for i, s := range l {
		q[i] = dct.PyRepr(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// refreshReferences is references_refresh.refresh_references_if_needed
// (attempted, succeeded, detail).
func refreshReferences(root, cause string) (bool, bool, string) {
	hit := false
	for _, c := range strings.Split(cause, ",") {
		hit = hit || c == "synthesized_drg"
	}
	if !hit {
		return false, false, ""
	}
	args := []string{"spec-kitty", "charter", "generate", "--no-from-interview", "--json"}
	if c := charter.LoadBundle(root); c != nil {
		if cat := c.GetObject("catalog"); cat != nil {
			if m := cat.GetString("mission"); m != "" {
				args = append(args, "--mission-type", m)
			}
			if t := cat.GetString("template_set"); t != "" {
				args = append(args, "--template-set", t)
			}
		}
	}
	bin, err := spekittyBinary(args[0])
	if err != nil {
		return true, false, "`" + strings.Join(args, " ") + "` invocation failed: [Errno 2] No such file or directory: 'spec-kitty'"
	}
	cmd := exec.Command(bin, args[1:]...)
	cmd.Dir = root
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Start(); err != nil {
		return true, false, "`" + strings.Join(args, " ") + "` invocation failed: " + err.Error()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		return true, false, fmt.Sprintf("`%s` invocation failed: Command '%s' timed out after 30.0 seconds", strings.Join(args, " "), pyListRepr(args))
	case err := <-done:
		if err == nil {
			return true, true, ""
		}
	}
	out := so.String()
	for _, cand := range append([]string{out}, reverseLines(out)...) {
		if v, err := jsonx.Decode([]byte(cand)); err == nil {
			if o, ok := v.(*jsonx.Object); ok {
				if e := strings.TrimSpace(o.GetString("error")); e != "" {
					return true, false, e
				}
			}
		}
	}
	for _, stream := range []string{out, se.String()} {
		var lines []string
		for _, l := range strings.Split(stream, "\n") {
			if t := strings.TrimSpace(l); t != "" {
				lines = append(lines, t)
			}
		}
		start := -1
		for i, l := range lines {
			if strings.HasPrefix(l, "Unexpected error:") || strings.HasPrefix(l, "Error:") {
				start = i
			}
		}
		if start >= 0 {
			return true, false, strings.Join(lines[start:], " ")
		}
	}
	for _, stream := range []string{se.String(), out} {
		var lines []string
		for _, l := range strings.Split(stream, "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			return true, false, lines[len(lines)-1]
		}
	}
	return true, false, fmt.Sprintf("generate exited %d with no captured output", cmd.ProcessState.ExitCode())
}

func reverseLines(s string) []string {
	l := strings.Split(s, "\n")
	out := make([]string, 0, len(l))
	for i := len(l) - 1; i >= 0; i-- {
		out = append(out, l[i])
	}
	return out
}

func runPreflight(root string, autoRefresh bool) preflightResult {
	checks := buildPreflightChecks(root)
	res := preflightResult{checks: checks, actions: []string{}}
	switch {
	case allPass(checks):
		res.passed = true
	case !autoRefresh:
		res.blocked = blockedReason(checks)
	default:
		res = attemptAutoRefresh(root, checks)
	}
	if act, err := dct.LoadActivation(root); err == nil {
		if ap := act.Activated(dct.AgentProfile); ap != nil {
			seen := map[string]bool{}
			for _, d := range actionProfileDefaults {
				seen[d[2]] = true
			}
			for _, id := range dct.SortedKeys(seen) {
				if !ap.Has(id) {
					if wmsg := missionDefaultProfileWarning(id); wmsg != "" {
						res.warnings = append(res.warnings, wmsg)
					}
				}
			}
		}
	}
	return res
}

func attemptAutoRefresh(root string, checks []preflightCheck) preflightResult {
	res := preflightResult{checks: checks, actions: []string{}}
	lines, err := gitx.StatusPorcelain(root, ".kittify/charter/", dct.ProjectPackRoot+"/")
	if err != nil {
		res.blocked = "git status failed: " + err.Error()
		return res
	}
	var charterDirty, packDirty []string
	for _, l := range lines {
		if len(l) < 4 {
			continue
		}
		p := strings.TrimSpace(l[3:])
		if strings.HasPrefix(p, ".kittify/charter/") {
			charterDirty = append(charterDirty, p)
		}
		if strings.HasPrefix(p, dct.ProjectPackRoot+"/") {
			packDirty = append(packDirty, p)
		}
	}
	if len(lines) > 0 {
		for i, c := range res.checks {
			if (c.name == "charter_source" || c.name == "synced_bundle") && len(charterDirty) > 0 {
				res.checks[i].detail += "; uncommitted: " + strings.Join(charterDirty, ", ")
			} else if c.name == "synthesized_drg" && len(packDirty) > 0 {
				res.checks[i].detail += "; uncommitted: " + strings.Join(packDirty, ", ")
			}
		}
		res.blocked = "uncommitted generated artifacts; commit or stash and retry"
		return res
	}
	res.applied = true
	byName := map[string]preflightCheck{}
	for _, c := range checks {
		byName[c.name] = c
	}
	if byName["charter_source"].state != "fresh" || byName["synced_bundle"].state != "fresh" {
		rem := byName["charter_source"].remediation
		if rem == "" {
			rem = byName["synced_bundle"].remediation
		}
		if rem == "" {
			res.blocked = blockedReason(checks)
			return res
		}
		ok, reason := runRefreshStep(root, strings.Fields(rem))
		res.actions = append(res.actions, rem)
		if !ok {
			res.blocked = reason
			return res
		}
	}
	if byName["synthesized_drg"].state != "fresh" {
		ok, reason := runRefreshStep(root, []string{"spec-kitty", "charter", "synthesize"})
		res.actions = append(res.actions, "spec-kitty charter synthesize")
		if !ok {
			res.blocked = reason
			return res
		}
	}
	ok, reason := runRefreshStep(root, []string{"spec-kitty", "charter", "bundle", "validate"})
	res.actions = append(res.actions, "spec-kitty charter bundle validate")
	if !ok {
		res.blocked = reason
		return res
	}
	var stale []string
	for _, c := range checks {
		if !preflightPass[c.state] {
			stale = append(stale, c.name)
		}
	}
	sort.Strings(stale)
	attempted, succeeded, detail := refreshReferences(root, strings.Join(stale, ","))
	if attempted && !succeeded {
		res.blocked = "references-parity refresh failed: " + detail
		return res
	}
	if attempted && succeeded {
		ok, reason := runRefreshStep(root, []string{"spec-kitty", "charter", "synthesize"})
		res.actions = append(res.actions, "spec-kitty charter synthesize")
		if !ok {
			res.blocked = reason
			return res
		}
	}
	res.checks = buildPreflightChecks(root)
	res.passed = allPass(res.checks)
	if !res.passed {
		res.blocked = blockedReason(res.checks)
	}
	return res
}

func charterPreflightCmd() *cli.Command {
	return &cli.Command{
		Name:  "preflight",
		Short: "Verify charter-derived state before a governed session begins.",
		Long:  "Verify charter-derived state before a governed session begins.\n\nPipeline:\n\n1. Resolve the repo root (same logic as the rest of the ``charter``\n   subcommand group).\n2. Invoke :func:`run_charter_preflight`.\n3. Render JSON or a Rich summary, then exit per the contract.",
		Flags: []*cli.Flag{
			{Name: "json", Kind: cli.Bool, Help: "Emit the result as JSON (binding shape, see contracts/charter-preflight-json.md)."},
			{Name: "auto-refresh", Kind: cli.Bool, Help: "When checks fail and the worktree has no uncommitted generated artifacts, run the safe refresh sequence (charter generate / upgrade --yes -> synthesize -> bundle validate)."},
			{Name: "strict", Kind: cli.Bool, Help: "Exit non-zero on any non-fresh state (default: exit zero unless a hard error occurs)."},
		},
		Run: runCharterPreflight,
	}
}

func runCharterPreflight(ctx *cli.Context) error {
	w := ctx.Stdout
	root := repoRoot()
	res := runPreflight(root, ctx.Bool("auto-refresh"))
	if ctx.Bool("json") {
		jsonOut(w, res.object(), jsonx.Options{Indent: -1, SortKeys: true})
	} else {
		colour, head := "green", "PASSED"
		if !res.passed {
			colour, head = "red", "BLOCKED"
		}
		cprint(w, "%s", "[bold "+colour+"]Charter preflight: "+head+"[/bold "+colour+"]")
		for _, c := range res.checks {
			col := map[string]string{"fresh": "green", "skipped": "dim", "built_in_only": "cyan", "stale": "yellow", "missing": "blue", "invalid": "red"}[c.state]
			if col == "" {
				col = "white"
			}
			cprintRaw(w, fmt.Sprintf("  [%s]%14s[/%s]  %s: %s", col, strings.ToUpper(c.state), col, c.name, c.detail))
			if c.remediation != "" {
				cprintRaw(w, "                  [dim]Run: "+c.remediation+"[/dim]")
			}
		}
		if res.applied {
			cprint(w, "\n[bold]Auto-refresh:[/bold]")
			for _, a := range res.actions {
				cprintRaw(w, "  [green]ran:[/green] "+a)
			}
		}
		if res.blocked != "" {
			cprintRaw(w, "\n[red]Blocked:[/red] "+res.blocked)
		}
	}
	if res.passed || !ctx.Bool("strict") {
		return cli.Exit(0)
	}
	return cli.Exit(1)
}

var _ = rich.Print

const synthesizeLong = "Validate and promote agent-generated project-local doctrine artifacts.\n\nReads the charter interview answers, resolves synthesis targets from the\nDRG + doctrine, and writes all artifacts to ``.kittify/charter-packs/``.\n\nDoctrine generation is performed by the LLM harness (Claude Code, Codex,\nCursor, etc.) via the spk-charter-governance skill. This command\nvalidates and promotes the artifacts the agent has written.\n\nFresh-project behavior (issue #839 / WP06 T031-T033)\n----------------------------------------------------\nOn a fresh project where ``.kittify/charter/generated/`` is missing or\nempty (i.e. the LLM harness has not yet written agent artifacts), this\ncommand short-circuits the adapter pipeline and materializes the\n**minimal artifact set** the runtime requires:\n\n1. ``.kittify/charter-packs/`` — directory marker. The project-root\n   resolver (``charter.activation._project_root_candidates``) is a\n   presence-only check; an empty directory is a valid project layer.\n2. ``.kittify/charter-packs/PROVENANCE.md`` — human-readable record of the\n   fresh-project seed path, citing #839.\n\nThe runtime falls back to the built-in doctrine (``packs/built-in/``) for\nall artifact lookups until the harness writes per-target YAML and the\noperator re-runs ``synthesize`` (which then takes the normal adapter\npath). The fresh-project path is **idempotent**: re-running produces\nbytewise-identical output (T033). Charter prerequisites are still\nenforced — ``charter.md`` must exist (else ``TaskCliError`` is raised\nvia ``_build_synthesis_request``).\n\nExamples\n--------\nValidate + promote generated artifacts written by the harness::\n\n    spec-kitty charter synthesize\n\nValidate + promote with fixture adapter (offline/testing)::\n\n    spec-kitty charter synthesize --adapter fixture\n\nDry-run (stage + validate, no promote)::\n\n    spec-kitty charter synthesize --dry-run\n\nPreserve-and-warn default (WP03): a plain run never drops backed\ncontent -- it exits 0 and reports what it retained. Remove divergent\ncontent explicitly::\n\n    spec-kitty charter synthesize --prune\n\nOnly orphaned content (backing artifact deleted) or an unparseable\non-disk overlay make a plain run refuse (exit 1); backed divergence is\nalways preserved and reported, never a refusal."

const resynthesizeLong = "Regenerate a bounded set of project-local doctrine artifacts (partial resynthesis).\n\nUses a structured selector to identify the target set:\n\n- ``directive:PROJECT_001`` — regenerate a specific project directive.\n- ``tactic:how-we-apply-directive-003`` — regenerate one tactic.\n- ``directive:DIRECTIVE_003`` — regenerate every artifact whose provenance\n  references the built-in DIRECTIVE_003 URN.\n- ``testing-philosophy`` — regenerate all artifacts from that interview section.\n\nUnrelated artifacts are never touched (FR-017).\n\nExamples\n--------\nResynthesize a single tactic::\n\n    spec-kitty charter resynthesize --topic tactic:how-we-apply-directive-003\n\nResynthesize all artifacts referencing a built-in directive::\n\n    spec-kitty charter resynthesize --topic directive:DIRECTIVE_003"
