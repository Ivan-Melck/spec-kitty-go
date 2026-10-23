package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func init() {
	addCommands(func(_, _, missionG, _ *cli.Command) {
		missionG.Add(&cli.Command{
			Name:  "record-analysis",
			Short: "Persist `/spec-kitty.analyze` output as `analysis-report.md`.",
			Flags: []*cli.Flag{
				{Name: "mission", Kind: cli.String, Metavar: "<str>", Help: "Mission slug (e.g., '020-my-mission')"},
				{Name: "input-file", Kind: cli.String, Metavar: "<str>", Default: "-", Help: "Markdown report path, or '-' to read report from stdin"},
				{Name: "agent", Kind: cli.String, Metavar: "<str>", Help: "Agent name that produced the analysis report"},
				{Name: "json", Kind: cli.Bool, Help: "Output JSON format"},
				{Name: "report-only", Kind: cli.Bool, Help: "Commit only the report while preserving unrelated work; require clean material inputs"},
				ownedFlag(),
			},
			Run: runRecordAnalysis,
		})
	})
}

var (
	ulidJSONLRe   = regexp.MustCompile(`^kitty-ops/[0-9A-HJKMNP-TV-Z]{26}\.jsonl$`)
	missionMetaRe = regexp.MustCompile(`(?:^|/)kitty-specs/[^/]+/meta\.json$`)
)

// isSelfBookkeepingChurn is coherence.is_self_bookkeeping_churn.
func isSelfBookkeepingChurn(p string) bool {
	p = strings.TrimPrefix(filepath.ToSlash(p), "./")
	switch {
	case missionMetaRe.MatchString(p), strings.HasSuffix("/"+p, "/.kittify/meta.json"),
		p == ".kittify/encoding-provenance/global.jsonl", ulidJSONLRe.MatchString(p),
		strings.Contains(p, "/.kittify/dossiers/") && strings.HasSuffix(p, "/snapshot-latest.json"):
		return true
	}
	return false
}

func sha256File(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return sha256Hex(b)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

var (
	checkboxRe   = regexp.MustCompile(`(?m)^(\s*[-*]\s*)\[[ xX]\]`)
	pipeStatusRe = regexp.MustCompile(`\|(\s*)\[[ xXDP]\](\s*)\|`)
)

func normalizeTasksMD(t string) string {
	t = checkboxRe.ReplaceAllString(t, "${1}[ ]")
	// the lookahead keeps the closing pipe available for the next match
	for {
		n := pipeStatusRe.ReplaceAllString(t, "|${1}[ ]${2}|")
		if n == t {
			return n
		}
		t = n
	}
}

func artifactHashEntry(path, root string) *jsonx.Object {
	if _, err := os.Stat(path); err != nil {
		return jsonx.NewObject("path", path, "sha256", nil)
	}
	rel, _ := filepath.Rel(root, path)
	if filepath.Base(path) == "tasks.md" {
		b, _ := os.ReadFile(path)
		return jsonx.NewObject("path", filepath.ToSlash(rel), "sha256", sha256Hex([]byte(normalizeTasksMD(string(b)))))
	}
	return jsonx.NewObject("path", filepath.ToSlash(rel), "sha256", sha256File(path))
}

func inputArtifactHashes(dir, root string) *jsonx.Object {
	o := jsonx.NewObject()
	for _, n := range []string{"spec.md", "plan.md", "tasks.md"} {
		o.Set(n, artifactHashEntry(filepath.Join(dir, n), root))
	}
	charter := jsonx.NewObject("path", nil, "sha256", nil)
	for _, c := range []string{".kittify/charter/charter.yaml", ".kittify/charter/charter.md"} {
		if p := filepath.Join(root, c); fileExists(p) {
			charter = jsonx.NewObject("path", c, "sha256", sha256File(p))
			break
		}
	}
	o.Set("charter", charter)
	return o
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

var severities = []string{"low", "medium", "high", "critical"}

// parseFindingsCarrier is parse_structured_findings: (verdict, counts,
// findings, body, ok, err).
func parseFindingsCarrier(body string) (string, *jsonx.Object, []any, string, bool, error) {
	if !strings.HasPrefix(body, "---") {
		return "", nil, nil, body, false, nil
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return "", nil, nil, "", false, fmt.Errorf("Malformed analysis-findings carrier: opening '---' has no closing '---'.")
	}
	parsed, err := yamlx.Parse([]byte(strings.Join(lines[1:closing], "\n")))
	if err != nil {
		return "", nil, nil, "", false, fmt.Errorf("Invalid YAML in analysis-findings carrier: %v", err)
	}
	carrier := jsonx.ToObject(parsed)
	if carrier == nil || carrier.GetString("schema") != "analysis-findings/v1" {
		return "", nil, nil, body, false, nil
	}
	remainder := strings.TrimLeft(strings.Join(lines[closing+1:], "\n"), "\n")
	raw, _ := carrier.Get("findings")
	list, ok := raw.([]any)
	if raw != nil && !ok {
		return "", nil, nil, "", false, fmt.Errorf("analysis-findings 'findings' must be a list.")
	}
	tally := map[string]int{}
	var findings []any
	for _, e := range list {
		eo := jsonx.ToObject(e)
		if eo == nil {
			return "", nil, nil, "", false, fmt.Errorf("Each analysis-findings entry must be a mapping.")
		}
		sev := eo.GetString("severity")
		valid := false
		for _, s := range severities {
			if s == sev {
				valid = true
			}
		}
		if !valid {
			v, _ := eo.Get("severity")
			return "", nil, nil, "", false, fmt.Errorf("Unknown finding severity %s; allowed (canonical): ['critical', 'high', 'low', 'medium'].", pyReprAny(v))
		}
		tally[sev]++
		g := func(k string) any { v, _ := eo.Get(k); return v }
		findings = append(findings, jsonx.NewObject("id", g("id"), "severity", sev, "category", g("category"), "summary", g("summary")))
	}
	counts := jsonx.NewObject()
	info := 0
	if dv, ok := carrier.Get("counts"); ok && dv != nil {
		d := jsonx.ToObject(dv)
		if d == nil {
			return "", nil, nil, "", false, fmt.Errorf("analysis-findings 'counts' must be a mapping.")
		}
		for _, k := range d.Keys() {
			if k != "info" && tally[k] == 0 && !contains(severities, k) {
				return "", nil, nil, "", false, fmt.Errorf("Unknown counts keys ['%s']; allowed: ['critical', 'high', 'info', 'low', 'medium'].", k)
			}
		}
		for _, s := range severities {
			n, ok := d.GetInt(s)
			if !ok {
				n = 0
			}
			if n != tally[s] {
				return "", nil, nil, "", false, fmt.Errorf("counts['%s']=%d does not equal findings tally %d.", s, n, tally[s])
			}
		}
		info, _ = d.GetInt("info")
	}
	// Upstream iterates a frozenset of severities (hash order); a fixed
	// canonical order is used here.
	for _, s := range []string{"critical", "high", "medium", "low"} {
		counts.Set(s, tally[s])
	}
	counts.Set("info", info)
	verdict := "ready"
	if tally["high"] > 0 || tally["critical"] > 0 {
		verdict = "blocked"
	}
	if h := carrier.GetString("verdict_hint"); h != "" && h != verdict {
		return "", nil, nil, "", false, fmt.Errorf("verdict_hint '%s' disagrees with the computed verdict '%s' (verdict is derived from findings severities; correct the hint or the findings).", h, verdict)
	}
	if findings == nil {
		findings = []any{}
	}
	return verdict, counts, findings, remainder, true, nil
}

func pyReprAny(v any) string {
	if s, ok := v.(string); ok {
		return mission.PyRepr(s)
	}
	if v == nil {
		return "None"
	}
	return fmt.Sprint(v)
}

func runRecordAnalysis(ctx *cli.Context) error {
	jsonMode := ctx.Bool("json")
	fail := func(msg string) error {
		if jsonMode {
			emitJSON(ctx.Stdout, jsonx.NewObject("error", msg, "success", false))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+msg)
		}
		return cli.Exit(1)
	}
	root := project.Locate("")
	if root == "" {
		return fail("Could not locate project root")
	}
	if ctx.String("owned-checkout") != "" {
		return fail("--owned-checkout is not supported by this build")
	}
	reportOnly := ctx.Bool("report-only")
	feature := ctx.String("mission")
	featureDir, derr := readPathFor(root, feature)
	if derr != nil {
		var args []string
		if jsonMode {
			args = []string{"--json"}
		}
		payload := detectionError(root, derr.Error(), feature, "MISSION_CONTEXT_UNRESOLVED", "record-analysis", args)
		if jsonMode {
			emitJSON(ctx.Stdout, payload)
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+payload.GetString("error"))
		}
		return cli.Exit(1)
	}
	slug := filepath.Base(featureDir)
	if reportOnly {
		return runReportOnly(ctx, root, slug, jsonMode, fail)
	}
	// write preflight: refuse a pre-existing dirty working tree.
	cwdRoot := project.CurrentWorktreeRoot(cwd())
	if cwdRoot == "" {
		cwdRoot = root
	}
	entries, err := safecommit.StatusEntries(cwdRoot, "")
	if err != nil {
		return fail(err.Error())
	}
	s := coord.Resolve(root, slug)
	var dirty []string
	for _, e := range entries {
		sides := []string{e.Path}
		if e.OrigPath != "" {
			sides = []string{e.OrigPath, e.Path}
		}
		for _, p := range sides {
			if isSelfBookkeepingChurn(p) {
				continue
			}
			if coord.RoutesThroughCoordination(s.Topology) && strings.HasPrefix(p, "kitty-specs/"+slug+"/") && mission.IsCoordResidue(p) {
				continue
			}
			dirty = append(dirty, p)
		}
	}
	if len(dirty) > 0 {
		msg := "Refusing to record analysis report with pre-existing dirty working tree."
		if jsonMode {
			emitJSON(ctx.Stdout, jsonx.NewObject("success", false, "error_code", "DIRTY_WORKTREE", "error", msg, "dirty_paths", strList(dirty),
				"remediation", strList([]string{"Commit or stash existing changes, then rerun /spec-kitty.analyze."})))
		} else {
			say(ctx.Stdout, "[red]Error:[/red] "+msg)
			for _, p := range dirty {
				say(ctx.Stdout, "  - "+p)
			}
		}
		return cli.Exit(1)
	}
	var body []byte
	if in := ctx.String("input-file"); in == "-" {
		body, err = io.ReadAll(ctx.Stdin)
	} else {
		body, err = os.ReadFile(in)
	}
	if err != nil {
		return fail(fmt.Sprintf("[Errno 2] No such file or directory: %s", mission.PyRepr(ctx.String("input-file"))))
	}
	if strings.TrimSpace(string(body)) == "" {
		return fail("Analysis report body is empty")
	}
	dir := project.Specs(root, slug)
	for _, n := range []string{"spec.md", "plan.md", "tasks.md"} {
		if !fileExists(filepath.Join(dir, n)) {
			return fail("Required artifact missing: " + filepath.Join(dir, n))
		}
	}
	meta := mission.ReadMetaOrEmpty(dir)
	mslug := meta.GetString("mission_slug")
	if mslug == "" || !mission.SafePathSegment(mslug) {
		mslug = slug
	}
	var mid any
	if v := meta.GetString("mission_id"); v != "" {
		mid = v
	}
	inputs := inputArtifactHashes(dir, root)
	verdict, counts, findings, reportBody, ok, perr := parseFindingsCarrier(string(body))
	if perr != nil {
		return fail(perr.Error())
	}
	if !ok {
		verdict = "unknown"
		counts = jsonx.NewObject("critical", nil, "high", nil, "medium", nil, "low", nil, "info", nil)
		findings = []any{}
		reportBody = string(body)
	}
	agent := ctx.String("agent")
	if agent == "" {
		agent = "unknown"
	}
	front := jsonx.NewObject("schema_version", 1, "artifact_type", "spec-kitty.analysis-report", "command", "/spec-kitty.analyze", "mission_slug", mslug,
		"mission_id", mid, "generated_at", clock.NowUTCISO(), "analyzer_agent", agent, "input_artifacts", inputs, "verdict", verdict,
		"issue_counts", counts, "findings", findings)
	if !strings.HasSuffix(reportBody, "\n") {
		reportBody += "\n"
	}
	content := "---\n" + yamlx.Emit(front) + "---\n\n" + reportBody
	path := filepath.Join(dir, "analysis-report.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fail(err.Error())
	}
	r := safecommit.CommitForMission(root, slug, []string{path}, "docs(record-analysis): record analysis report for mission "+slug, mission.KindAnalysisReport, safecommit.RouterOptions{})
	payload := jsonx.NewObject("success", true, "result", "success", "path", path, "mission_slug", mslug, "mission_id", mid, "input_artifacts", inputs,
		"verdict", verdict, "issue_counts", counts, "findings", findings, "stale", false)
	if len(r.Surfaces) > 0 {
		payload.Set("surfaces", r.SurfacesPayload())
	}
	if jsonMode {
		emitJSON(ctx.Stdout, payload)
		return nil
	}
	if surfaceNeedsWarningPlain(r) {
		printRendered(ctx.Stdout, r)
	}
	rel, _ := filepath.Rel(root, path)
	say(ctx.Stdout, "[green]✓[/green] Analysis report persisted: "+rel)
	return nil
}

func surfaceNeedsWarningPlain(r *safecommit.RouterResult) bool {
	for _, o := range r.Surfaces {
		if o.Status != safecommit.StatusCommitted && o.Status != safecommit.StatusUnchanged {
			return true
		}
	}
	return false
}

var _ = sort.Strings
