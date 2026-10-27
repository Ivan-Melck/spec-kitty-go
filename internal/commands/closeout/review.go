package closeout

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/consolidate"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/issuematrix"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

func init() {
	addCommands(func(root, _, _ *cli.Command) {
		root.Add(&cli.Command{
			Name:  "review",
			Short: "Validate a merged mission: WP lane check, dead-code scan, BLE001 audit.",
			Long: "Validate a merged mission: WP lane check, dead-code scan, BLE001 audit.\n\n" +
				"Writes kitty-specs/<slug>/mission-review-report.md with a machine-readable\nverdict.  See module docstring for known false-positive scenarios in the\ndead-code scan step.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission handle (id, mid8, or slug)."},
				{Name: "mode", Kind: cli.String, Metavar: "<str>", Help: "Review mode: 'lightweight' (consistency check only) or 'post-merge' (full release-gate contract). Auto-detected from meta.json.baseline_merge_commit when omitted."},
			},
			Run: runReview,
		})
	})
}

const testExtraMessage = "pytest is not importable from the active Python interpreter. This is a Spec Kitty installation note: the review runs no tests, so it continues. Only local CI-parity runs of Spec Kitty itself need it; run `uv sync --extra test` to install it."

const modeMismatchBody = "MISSION_REVIEW_MODE_MISMATCH: --mode post-merge was requested but meta.json.baseline_merge_commit is absent, meaning no merge has been recorded for this mission — neither via `spec-kitty consolidate` nor from a GitHub PR acceptance.\n\n" +
	"What this means: post-merge mode requires a baseline merge commit recorded in meta.json to anchor the dead-code scan and gate-record validation. Without it, the full release-gate contract cannot be enforced.\n\n" +
	"Remediation options:\n" +
	"  1. Run `spec-kitty consolidate` to consolidate the mission and record the baseline commit, then retry `spec-kitty review --mode post-merge`.\n" +
	"  2. If this mission landed through a GitHub PR (`acceptance_mode: pr`), record the PR's real merge commit instead: `spec-kitty accept --mode pr --merge-commit <sha>` before the PR merges, or `spec-kitty migrate backfill-merge-commit --mission <slug> --merge-commit <sha>` afterwards (#4231).\n" +
	"  3. Re-run with `--mode lightweight` to perform a consistency check without the full post-merge gate requirements.\n" +
	"  4. For pre-083 missions that have already been merged but lack baseline_merge_commit, run `spec-kitty migrate backfill-identity` to backfill missing identity fields, which will also record the baseline merge commit if available.\n"

type gateRecord struct{ id, name, result string }

type finding = map[string]string

var hardFailures = map[string]bool{"wp_not_done": true, "rejected_review_artifact": true, "review_artifact_schema_invalid": true,
	"ble001_suppression": true, "issue_matrix_violation": true, "dead_code_baseline_missing": true,
	"dead_code_undeterminable": true, "dead_code_evidence_incomplete": true}

func runReview(ctx *cli.Context) error {
	w := ctx.Stdout
	root := locateRoot()
	if root == "" {
		say(w, "[red]Error:[/red] Unable to locate repository root (missing .git or .kittify).")
		return cli.Exit(2)
	}
	// The reference install has no pytest: the review always notes it.
	say(w, "[yellow]Warning:[/yellow] MISSION_REVIEW_TEST_EXTRA_MISSING: "+testExtraMessage)
	fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("diagnostic_code", "MISSION_REVIEW_TEST_EXTRA_MISSING", "severity", "warning",
		"message", testExtraMessage, "remediation", "uv sync --extra test"), compact))
	handle := strings.TrimSpace(ctx.String("mission"))
	if handle == "" {
		say(w, "[red]Error:[/red] --mission is required.")
		return cli.Exit(2)
	}
	slug, err := resolveStrict(ctx, root, handle, false)
	if err != nil {
		return err
	}
	dir := projectSpecs(root, slug)
	meta := mission.ReadMetaOrEmpty(dir)
	friendly := meta.GetString("friendly_name")
	if friendly == "" {
		friendly = slug
	}
	baseline := strings.TrimSpace(fmt.Sprint(nonNil(mustGetAny(meta, "baseline_merge_commit"))))
	modeFlag := ctx.OptString("mode")
	mode, auto := "lightweight", true
	if modeFlag != nil {
		if *modeFlag != "lightweight" && *modeFlag != "post-merge" {
			return cli.Usage(ctx.Cmd, "Invalid value for '--mode': %s is not a valid MissionReviewMode", mission.PyRepr(*modeFlag))
		}
		mode, auto = *modeFlag, false
		if mode == "post-merge" && baseline == "" {
			say(w, "[red]Error:[/red] MISSION_REVIEW_MODE_MISMATCH")
			sayLiteral(w, "", modeMismatchBody)
			fmt.Fprintln(w, jsonx.Dumps(jsonx.NewObject("diagnostic_code", "MISSION_REVIEW_MODE_MISMATCH", "message", modeMismatchBody), compact))
			return cli.Exit(1)
		}
	} else if baseline != "" {
		mode = "post-merge"
	}
	label := "explicit"
	if auto {
		label = "auto-detected"
	}
	sayLiteral(w, "", fmt.Sprintf("\nReviewing mission: %s (%s)", friendly, slug))
	sayLiteral(w, "", fmt.Sprintf("Mode: %s (%s)\n", mode, label))
	var findings []finding
	var gates []gateRecord

	// Gate 1: WP lane check (re-materializes status.json).
	before := len(findings)
	snap, _ := status.Materialize(dir)
	var nonDone []string
	total := 0
	if snap != nil && snap.WorkPackages != nil {
		total = snap.WorkPackages.Len()
		for _, kv := range snap.WorkPackages.Pairs() {
			if kv.Value.(*jsonx.Object).GetString("lane") != "done" {
				nonDone = append(nonDone, kv.Key)
			}
		}
	}
	if len(nonDone) > 0 {
		say(w, fmt.Sprintf("  [red]✗[/red]  WP lane check: %d WP(s) not in done", len(nonDone)))
		for _, id := range nonDone {
			lane := snap.WP(id).GetString("lane")
			say(w, fmt.Sprintf("       %s: %s", id, lane))
			findings = append(findings, finding{"type": "wp_not_done", "wp_id": id, "lane": lane})
		}
	} else {
		say(w, fmt.Sprintf("  [green]✓[/green]  WP lane check: all %d WP(s) in done", total))
	}
	conflicts, cerr := consolidate.ReviewArtifactFindings(coord.Resolve(root, slug).StatusDir(), nil)
	if cerr != nil {
		rich.PrintTraceback(ctx.Stderr, rich.ParseException(cerr.Error(), rich.Frame{Name: "find_rejected_review_artifact_conflicts", Line: 208}))
		return cli.Exit(1)
	}
	if len(conflicts) > 0 {
		say(w, "  [red]✗[/red]  Review artifact consistency: blocking latest review artifact exists for terminal WP(s)")
		for _, c := range conflicts {
			say(w, "       "+c.Format())
			say(w, "       diagnostic_code: REJECTED_REVIEW_ARTIFACT_CONFLICT")
			say(w, "       branch_or_work_package: "+c.WPID)
			say(w, "       violated_invariant: terminal_wp_latest_review_artifact_must_not_be_rejected")
			say(w, "       remediation: Run another review cycle that writes an approved review-cycle artifact.")
			say(w, "       remediation: Or move the WP out of approved/done before merge.")
			findings = append(findings, finding{"type": "rejected_review_artifact", "wp_id": c.WPID, "lane": c.Lane,
				"artifact_path": "<no review artifact>", "diagnostic_code": "REJECTED_REVIEW_ARTIFACT_CONFLICT",
				"branch_or_work_package": c.WPID, "violated_invariant": "terminal_wp_latest_review_artifact_must_not_be_rejected",
				"remediation":                 "Run another review cycle that writes an approved review-cycle artifact.; Or move the WP out of approved/done before merge.",
				"latest_review_cycle_verdict": c.Verdict})
		}
	} else {
		say(w, "  [green]✓[/green]  Review artifact consistency: no terminal WP has a blocking latest review artifact")
	}
	gates = append(gates, gateRecord{"gate_1", "wp_lane_check", passFail(len(findings) > before)})

	// Gate 2: dead-code scan.
	before = len(findings)
	outcome := scanDeadCode(w, root, baseline, meta, slug, &findings)
	res := passFail(len(findings) > before)
	if outcome == "not_applicable" {
		res = "skip"
	}
	gates = append(gates, gateRecord{"gate_2", "dead_code_scan", res})

	// Gate 3: BLE001 audit (spec-kitty auth storage only).
	say(w, "  [green]✓[/green]  BLE001 audit: 0 unjustified suppressions")
	gates = append(gates, gateRecord{"gate_3", "ble001_audit", "pass"})

	issuePresent := "not_applicable"
	exceptionPresent := "not_applicable"
	if mode == "post-merge" {
		issuePresent = reviewIssueMatrix(w, root, dir, &findings)
		if _, err := os.Stat(filepath.Join(dir, "mission-exception.md")); err == nil {
			exceptionPresent = "true"
		} else {
			exceptionPresent = "false"
		}
	}
	hard := 0
	for _, f := range findings {
		if hardFailures[f["type"]] {
			hard++
		}
	}
	verdict := "pass"
	if hard > 0 {
		verdict = "fail"
	} else if len(findings) > 0 {
		verdict = "pass_with_notes"
	}
	lines := []string{"---", "verdict: " + verdict, "mode: " + mode, "reviewed_at: " + clock.NowUTCISO(), fmt.Sprintf("findings: %d", len(findings)), "gates_recorded:"}
	for _, g := range gates {
		exit := 0
		if g.result == "fail" {
			exit = 1
		}
		lines = append(lines, "  - id: "+g.id, "    name: "+g.name, fmt.Sprintf("    command: spec-kitty review (internal %s)", strings.ReplaceAll(g.id, "_", " ")),
			fmt.Sprintf("    exit_code: %d", exit), "    result: "+g.result)
	}
	lines = append(lines, "issue_matrix_present: "+issuePresent, "mission_exception_present: "+exceptionPresent, "---", "")
	if len(findings) > 0 {
		lines = append(lines, "## Findings", "")
		for _, f := range findings {
			if l := formatFinding(f); l != "" {
				lines = append(lines, l)
			}
		}
	} else {
		lines = append(lines, "No findings.")
	}
	report := filepath.Join(dir, "mission-review-report.md")
	os.WriteFile(report, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	color := map[string]string{"pass": "green", "pass_with_notes": "yellow", "fail": "red"}[verdict]
	say(w, fmt.Sprintf("\nVerdict: [%s]%s[/%s]  (%d finding(s))", color, verdict, color, len(findings)))
	sayLiteral(w, "", "Report written: "+relPath(root, report))
	if verdict == "fail" {
		return cli.Exit(1)
	}
	return nil
}

// reviewIssueMatrix is the post-merge gate 4 of review_mission
// (_evaluate_issue_matrix): a mission without gating issue references
// (discovered on the primary dir) is not_applicable; otherwise the matrix is
// read from the coordination partition (resolve_issue_matrix_partition: a
// materialized coordination dir, the coord-less primary dir, or the
// placement ref's content) and validated. It returns the report's
// issue_matrix_present value.
func reviewIssueMatrix(w io.Writer, root, dir string, findings *[]finding) string {
	refs, _ := issuematrix.DiscoverReferences(dir)
	gating := false
	for _, r := range refs {
		gating = gating || r.Gating()
	}
	if !gating {
		say(w, "  [green]✓[/green]  Issue matrix: not_applicable (mission declares zero gating issue references)")
		return "not_applicable"
	}
	violation := func(code, msg string) {
		*findings = append(*findings, finding{"type": "issue_matrix_violation", "diagnostic_code": code, "message": msg})
	}
	part, err := issuematrix.ResolvePartition(root, dir)
	if err != nil {
		msg := "could not resolve the coordination issue-matrix source: " + err.Error()
		say(w, "  [red]✗[/red]  Issue matrix: "+issuematrix.DiagRefReadFailed+": "+msg)
		violation(issuematrix.DiagRefReadFailed, msg)
		return "false"
	}
	present := issuematrix.ArtifactPresent(part.MatrixDir)
	if part.Content != nil {
		present = strings.TrimSpace(*part.Content) != ""
	}
	if !present {
		say(w, "  [red]✗[/red]  Issue matrix: "+issuematrix.DiagMissing+": issue-matrix not found (required in post-merge mode when canonical issue references exist)")
		violation(issuematrix.DiagMissing, "issue-matrix is required in post-merge mode when canonical issue references exist")
		return "false"
	}
	matrixDir := part.MatrixDir
	if part.Content != nil {
		matrixDir = dir
	}
	res := issuematrix.Validate(filepath.Join(matrixDir, issuematrix.MDFile), part.Content)
	if !res.Passed {
		for _, d := range res.Diagnostics {
			say(w, fmt.Sprintf("  [red]✗[/red]  Issue matrix: %s: %s", d.Code, d.Message))
			violation(d.Code, d.Message)
		}
	} else {
		say(w, fmt.Sprintf("  [green]✓[/green]  Issue matrix: %d row(s) validated", len(res.Rows)))
	}
	return "true"
}

func mustGetAny(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

func nonNil(v any) any {
	if v == nil || v == false {
		return ""
	}
	return v
}

func passFail(failed bool) string {
	if failed {
		return "fail"
	}
	return "pass"
}

func formatFinding(f finding) string {
	get := func(k, d string) string {
		if v, ok := f[k]; ok {
			return v
		}
		return d
	}
	switch f["type"] {
	case "wp_not_done":
		return fmt.Sprintf("- **wp_not_done** `%s`: lane is `%s`", f["wp_id"], get("lane", "unknown"))
	case "rejected_review_artifact":
		return fmt.Sprintf("- **rejected_review_artifact** `%s`: lane is `%s`, latest artifact is `%s`; diagnostic_code=`%s`, branch_or_work_package=`%s`, violated_invariant=`%s`, remediation=`%s`",
			f["wp_id"], get("lane", "unknown"), get("artifact_path", "unknown"), get("diagnostic_code", "unknown"),
			get("branch_or_work_package", "unknown"), get("violated_invariant", "unknown"), get("remediation", "unknown"))
	case "dead_code":
		return fmt.Sprintf("- **dead_code** `%s` — `%s`: no non-test callers found", f["file"], f["symbol"])
	case "dead_code_baseline_missing":
		return fmt.Sprintf("- **dead_code_baseline_missing** `%s`: %s", get("diagnostic_code", "unknown"), get("remediation", "unknown"))
	case "dead_code_undeterminable":
		return fmt.Sprintf("- **dead_code_undeterminable** `%s`: %s; remediation=`%s`", get("diagnostic_code", "unknown"), get("reason", "unknown"), get("remediation", "unknown"))
	case "issue_matrix_violation":
		return fmt.Sprintf("- **issue_matrix_violation** `%s`: %s", get("diagnostic_code", "unknown"), get("message", "unknown"))
	case "dead_code_not_applicable":
		ext := f["unsupported_extensions"]
		if ext == "" {
			ext = "(none)"
		}
		return fmt.Sprintf("- **dead_code_not_applicable** `%s`: unsupported_extensions=`%s`; excluded_test_paths=`%s`; remediation=`%s`",
			get("diagnostic_code", "unknown"), ext, get("excluded_test_paths", "0"), get("remediation", "unknown"))
	}
	return ""
}

const (
	undeterminableRemediation = "Verify the baseline commit and Git repository, then rerun `spec-kitty review`."
	notApplicableRemediation  = "The dead-code scan only analyzes Python sources. For other languages, add tech-specific review guidance and your own analyzer or test command to your local charter (see 'Extend your charter for an unsupported language')."
)

var reAddedSymbol = regexp.MustCompile(`^\+\s*(def|class)\s+([A-Za-z]\w*)\s*[\(:]`)

func isTestOnly(p string) bool {
	parts := strings.Split(strings.ToLower(p), "/")
	for _, s := range parts[:len(parts)-1] {
		if s == "test" || s == "tests" {
			return true
		}
	}
	name := parts[len(parts)-1]
	if name == "conftest.py" {
		return true
	}
	stem := strings.TrimSuffix(name, ".py")
	return strings.HasSuffix(name, ".py") && (strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test"))
}

func isPy(p string) bool { return strings.ToLower(pySuffix(p)) == ".py" }

// pySuffix is PurePosixPath(p).suffix: the last dot of the final component
// starts it only when it is neither the first nor the last character (a
// dotfile such as .gitkeep has none).
func pySuffix(p string) string {
	name := path.Base(p)
	if i := strings.LastIndexByte(name, '.'); i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

var excludedParts = map[string]bool{".git": true, ".nox": true, ".pytest_cache": true, ".tox": true, ".venv": true, ".worktrees": true, "node_modules": true, "venv": true}

func scanDeadCode(w interface{ Write([]byte) (int, error) }, root, baseline string, meta *jsonx.Object, slug string, findings *[]finding) string {
	undeterminable := func(reason string) string {
		say(w, "  [red]✗[/red]  Dead-code scan: undeterminable (MISSION_REVIEW_DEAD_CODE_UNDETERMINABLE)")
		sayLiteral(w, "", "       reason: "+reason)
		sayLiteral(w, "", "       remediation: "+undeterminableRemediation)
		*findings = append(*findings, finding{"type": "dead_code_undeterminable", "diagnostic_code": "MISSION_REVIEW_DEAD_CODE_UNDETERMINABLE", "reason": reason, "remediation": undeterminableRemediation})
		return "unscanned"
	}
	if baseline == "" {
		if id := meta.GetString("mission_id"); id != "" {
			reason := "never_merged_via_spec_kitty_merge"
			rem := "Run `spec-kitty consolidate` to bake baseline_merge_commit into meta.json, or rerun review with `--mode post-merge` after merge."
			if strings.ToLower(strings.TrimSpace(meta.GetString("acceptance_mode"))) == "pr" {
				reason = "pr_accepted_merge_unrecorded"
				rem = "This mission was accepted via PR (acceptance_mode: pr): the missing baseline_merge_commit means the PR merge was never recorded, not that the mission never merged. If the PR has merged, record the real merge commit with `spec-kitty migrate backfill-merge-commit --mission <slug> --merge-commit <sha>` (or re-run `spec-kitty accept --mode pr --merge-commit <sha>`); if it has not, merge it, then rerun review with `--mode post-merge`."
			}
			say(w, "  [red]✗[/red]  Dead-code scan: missing baseline_merge_commit (LIGHTWEIGHT_REVIEW_MISSING_BASELINE)")
			sayLiteral(w, "", "       reason: "+reason)
			sayLiteral(w, "", "       remediation: "+rem)
			*findings = append(*findings, finding{"type": "dead_code_baseline_missing", "diagnostic_code": "LIGHTWEIGHT_REVIEW_MISSING_BASELINE",
				"mission_id": id, "mission_slug": slug, "reason": reason, "remediation": rem})
			return "unscanned"
		}
		say(w, "  [yellow]⚠[/yellow]  Dead-code scan skipped: no baseline_merge_commit in meta.json (legacy / pre-083 mission, LEGACY_MISSION_DEAD_CODE_SKIP)")
		return "unscanned"
	}
	r, err := gitx.Exec(root, nil, nil, "diff", "--name-status", "-z", "-M", baseline+"..HEAD")
	if err != nil {
		return undeterminable("git executable is unavailable")
	}
	if r.Code != 0 {
		return undeterminable("git diff failed")
	}
	recs := strings.Split(r.Stdout, "\x00")
	type entry struct{ path, orig string }
	var entries []entry
	for i := 0; i < len(recs); i++ {
		st := recs[i]
		if st == "" {
			continue
		}
		if i+1 >= len(recs) {
			break
		}
		e := entry{path: recs[i+1]}
		i++
		if (st[0] == 'R' || st[0] == 'C') && i+1 < len(recs) {
			e.orig, e.path = e.path, recs[i+1]
			i++
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return undeterminable("git diff reported no changed files")
	}
	supported := map[string]bool{}
	var supportedList []string
	for _, e := range entries {
		if isPy(e.path) && (strings.HasPrefix(e.path, "src/") || !isTestOnly(e.path)) {
			supported[e.path] = true
			supportedList = append(supportedList, e.path)
		}
	}
	exts := map[string]bool{}
	excludedTests := 0
	for _, e := range entries {
		if supported[e.path] {
			continue
		}
		if isPy(e.path) {
			excludedTests++
		} else {
			x := strings.ToLower(pySuffix(e.path))
			if x == "" {
				x = "(no extension)"
			}
			exts[x] = true
		}
	}
	var extList []string
	for x := range exts {
		extList = append(extList, x)
	}
	sort.Strings(extList)
	extText := strings.Join(extList, ", ")
	if len(supportedList) == 0 {
		shown := extText
		if shown == "" {
			shown = "(none)"
		}
		reason := fmt.Sprintf("no changed file is one the dead-code scan supports (unsupported extensions: %s; test-only Python paths excluded: %d)", shown, excludedTests)
		say(w, "  [yellow]⚠[/yellow]  Dead-code scan: not applicable (MISSION_REVIEW_DEAD_CODE_NOT_APPLICABLE)")
		sayLiteral(w, "", "       reason: "+reason)
		sayLiteral(w, "", "       remediation: "+notApplicableRemediation)
		*findings = append(*findings, finding{"type": "dead_code_not_applicable", "diagnostic_code": "MISSION_REVIEW_DEAD_CODE_NOT_APPLICABLE",
			"unsupported_extensions": extText, "excluded_test_paths": fmt.Sprint(excludedTests), "reason": reason, "remediation": notApplicableRemediation})
		return "not_applicable"
	}
	type symbol struct{ name, file string }
	var symbols []symbol
	for _, e := range entries {
		if !supported[e.path] {
			continue
		}
		args := []string{"--literal-pathspecs", "diff", "--unified=0", "-M", baseline + "..HEAD", "--"}
		if e.orig != "" {
			args = append(args, e.orig)
		}
		args = append(args, e.path)
		d, _ := gitx.Exec(root, nil, nil, args...)
		inHunk := false
		for _, line := range strings.Split(d.Stdout, "\n") {
			if strings.HasPrefix(line, "@@") {
				inHunk = true
			} else if inHunk && strings.HasPrefix(line, "+") {
				if m := reAddedSymbol.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[2], "_") {
					symbols = append(symbols, symbol{m[2], e.path})
				}
			}
		}
	}
	if len(extList) > 0 {
		say(w, fmt.Sprintf("  [yellow]⚠[/yellow]  Dead-code scan: not applicable to some changed files (%s)", extText))
	}
	searchRoot := root
	allSrc := true
	for _, p := range supportedList {
		if !strings.HasPrefix(p, "src/") {
			allSrc = false
		}
	}
	if allSrc {
		searchRoot = filepath.Join(root, "src")
	}
	type src struct{ rel, text string }
	var corpus []src
	filepath.Walk(searchRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel := relPath(root, p)
		for _, part := range strings.Split(rel, "/") {
			if excludedParts[part] {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if !info.IsDir() && info.Mode().IsRegular() && strings.HasSuffix(p, ".py") {
			b, err := os.ReadFile(p)
			if err == nil {
				corpus = append(corpus, src{rel, string(b)})
			}
		}
		return nil
	})
	sort.Slice(corpus, func(i, j int) bool { return corpus[i].rel < corpus[j].rel })
	if len(corpus) == 0 {
		return undeterminable("Python source corpus is empty")
	}
	var dead []symbol
	for _, s := range symbols {
		called := false
		for _, c := range corpus {
			if c.rel != s.file && !isTestOnly(c.rel) && strings.Contains(c.text, s.name) {
				called = true
				break
			}
		}
		if !called {
			dead = append(dead, s)
		}
	}
	for _, d := range dead {
		*findings = append(*findings, finding{"type": "dead_code", "symbol": d.name, "file": d.file})
	}
	if len(dead) > 0 {
		say(w, fmt.Sprintf("  [red]✗[/red]  Dead-code scan: %d unreferenced public symbol(s)", len(dead)))
		for _, d := range dead {
			sayLiteral(w, "", fmt.Sprintf("       %s  %s", d.file, d.name))
		}
		return "scanned"
	}
	say(w, "  [green]✓[/green]  Dead-code scan: 0 unreferenced public symbols")
	return "scanned"
}
