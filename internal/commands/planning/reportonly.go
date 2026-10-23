package planning

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/rich"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Port of specify_cli.git.report_transaction: `agent mission
// record-analysis --report-only` commits only the analysis report, refusing
// dirty material inputs, and records a qualification receipt under the git
// dir.

// analysisRender is render_analysis_report's (result, content).
type analysisRender struct {
	path, content, sha string
	result             *jsonx.Object // AnalysisReportResult.to_dict()
}

// renderAnalysisReport is render_analysis_report.
func renderAnalysisReport(dir, root, body, agent string, material *jsonx.Object, token string) (*analysisRender, error) {
	for _, n := range missionart.HashInputNames {
		if !fileExists(filepath.Join(dir, n)) {
			return nil, errors.New("Required artifact missing: " + filepath.Join(dir, n))
		}
	}
	slug := filepath.Base(dir)
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
	if material != nil {
		for _, kv := range material.Pairs() {
			inputs.Set(kv.Key, kv.Value)
		}
	}
	verdict, counts, findings, reportBody, ok, perr := parseFindingsCarrier(body)
	if perr != nil {
		return nil, perr
	}
	if !ok {
		verdict = "unknown"
		counts = jsonx.NewObject("critical", nil, "high", nil, "medium", nil, "low", nil, "info", nil)
		findings = []any{}
		reportBody = body
	}
	if agent == "" {
		agent = "unknown"
	}
	front := jsonx.NewObject("schema_version", 1, "artifact_type", "spec-kitty.analysis-report", "command", "/spec-kitty.analyze", "mission_slug", mslug,
		"mission_id", mid, "generated_at", clock.NowUTCISO(), "analyzer_agent", agent, "input_artifacts", inputs, "verdict", verdict,
		"issue_counts", counts, "findings", findings)
	if token != "" {
		front.Set("report_transaction", token)
		front.Set("material_manifest_version", 1)
	}
	if !strings.HasSuffix(reportBody, "\n") {
		reportBody += "\n"
	}
	content := "---\n" + yamlx.Emit(front) + "---\n\n" + reportBody
	path := filepath.Join(dir, "analysis-report.md")
	result := jsonx.NewObject("path", path, "mission_slug", mslug, "mission_id", mid, "input_artifacts", inputs,
		"verdict", verdict, "issue_counts", counts, "findings", findings, "stale", false)
	return &analysisRender{path: path, content: content, sha: sha256Hex([]byte(content)), result: result}, nil
}

func gitOut(root string, args ...string) (string, error) {
	res, err := gitx.Exec(root, nil, nil, append([]string{"--literal-pathspecs"}, args...)...)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", &gitx.Error{Args: args, Dir: root, Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr}
	}
	return res.Stdout, nil
}

var tokenRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
var commitRe = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

func receiptPath(root, token string) (string, error) {
	if !tokenRe.MatchString(token) {
		return "", errors.New("Invalid report transaction identifier")
	}
	gd, err := gitOut(root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(strings.TrimSpace(gd), "spec-kitty-report-transactions", token+".json"), nil
}

// ReportIsQualified is report_transaction.report_is_qualified: a missing or
// mismatching receipt fails closed.
func ReportIsQualified(root, report string, token any) bool {
	t, ok := token.(string)
	if !ok {
		return false
	}
	rp, err := receiptPath(root, t)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(rp)
	if err != nil {
		return false
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		return false
	}
	commit := o.GetString("commit")
	if !commitRe.MatchString(commit) {
		return false
	}
	if _, err := gitOut(root, "merge-base", "--is-ancestor", commit, "HEAD"); err != nil {
		return false
	}
	rel := relOrAbs(root, report)
	cur, err := os.ReadFile(report)
	if err != nil {
		return false
	}
	shown, err := gitOut(root, "show", commit+":"+rel)
	return err == nil && o.GetString("state") == "qualified" && o.GetString("report") == rel && o.GetString("sha256") == sha256Hex(cur) && shown == string(cur)
}

func dirtyPathSet(root string) (map[string]bool, error) {
	entries, err := safecommit.StatusEntries(root, "all")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range entries {
		out[e.Path] = true
		if e.OrigPath != "" {
			out[e.OrigPath] = true
		}
	}
	return out, nil
}

// indexSnapshot is _index: every stage-0 'H' entry except the report.
func indexSnapshot(root, report string) (string, error) {
	out, err := gitOut(root, "ls-files", "--stage", "-v", "-z")
	if err != nil {
		return "", err
	}
	var keep []string
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		head, p, _ := strings.Cut(rec, "\t")
		f := strings.Fields(head)
		if len(f) != 4 || f[0] != "H" || f[3] != "0" {
			return "", errors.New("Unsupported index flags, sparse entry or unresolved conflict")
		}
		if p != report {
			keep = append(keep, rec)
		}
	}
	return strings.Join(keep, "\x00"), nil
}

// workingSnapshot is _working: the bytes/mode of every unrelated dirty path.
func workingSnapshot(root, report string) (string, error) {
	dirty, err := dirtyPathSet(root)
	if err != nil {
		return "", err
	}
	delete(dirty, report)
	keys := make([]string, 0, len(dirty))
	for k := range dirty {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var rows []string
	for _, rel := range keys {
		p := filepath.Join(root, rel)
		st, lerr := os.Lstat(p)
		switch {
		case lerr == nil && st.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			rows = append(rows, fmt.Sprintf("%s\x00%s\x00%d", rel, target, st.Mode()))
		case lerr == nil && st.Mode().IsRegular():
			rows = append(rows, fmt.Sprintf("%s\x00%s\x00%d", rel, sha256File(p), st.Mode()))
		case lerr != nil && os.IsNotExist(lerr):
			rows = append(rows, rel+"\x00absent\x000")
		default:
			return "", errors.New("Unsupported dirty directory or submodule")
		}
	}
	return strings.Join(rows, "\n"), nil
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return hex.EncodeToString(b[:])
}

// splitCarrierMeta is _split_carrier's metadata half (nil when the content
// has no leading mapping frontmatter).
func splitCarrierMeta(content string) (*jsonx.Object, string) {
	if !strings.HasPrefix(content, "---") {
		return nil, content
	}
	lines := pySplitlines(content)
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return nil, content
	}
	v, err := yamlx.Parse([]byte(strings.Join(lines[1:closing], "\n")))
	o, ok := v.(*jsonx.Object)
	if err != nil || !ok || o == nil {
		return nil, content
	}
	return o, strings.TrimLeft(strings.Join(lines[closing+1:], "\n"), "\n")
}

func reportSemantics(content string) (string, bool) {
	meta, body := splitCarrierMeta(content)
	if meta == nil {
		return "", false
	}
	m := meta.Clone()
	m.Delete("generated_at")
	m.Delete("report_transaction")
	return jsonx.Dumps(m, jsonx.Options{Indent: -1, SortKeys: true}) + "\x00" + body, true
}

// reportTxn is the transaction's typed outcome (payload + router result).
type reportTxn struct {
	payload *jsonx.Object
	router  *safecommit.RouterResult
}

// recordReportTransaction is record_report_transaction.
func recordReportTransaction(root, dir, body, agent, targetBranch string, owned *ownedcheckout.Fact) *reportTxn {
	report := filepath.Join(dir, "analysis-report.md")
	relative := relOrAbs(root, report)
	message := "docs(record-analysis): record analysis report for mission " + filepath.Base(dir)
	token := newToken()
	wrote := false
	committed := ""
	head := ""
	var refused *safecommit.RouterResult
	fail := func(err error) *reportTxn {
		if wrote && committed == "" && head != "" {
			if obs, e := gitOut(root, "rev-parse", "HEAD"); e == nil && strings.TrimSpace(obs) != head {
				committed = strings.TrimSpace(obs)
			}
		}
		statusText := "failed_before_write"
		if committed != "" {
			statusText = "committed_unqualified"
		} else if wrote {
			statusText = "written_uncommitted"
		}
		p := jsonx.NewObject("success", false, "commit_status", statusText, "commit_hash", optAny(committed), "error", err.Error(),
			"remediation", "Preserve concurrent work, inspect the retained report and commit, reconcile material inputs, then rerun analysis and report-only recording.")
		if refused != nil {
			p.Set("surfaces", refused.SurfacesPayload())
		}
		return &reportTxn{payload: p, router: refused}
	}
	if _, _, _, _, _, err := parseFindingsCarrier(body); err != nil {
		return fail(err)
	}
	cursor := root
	for _, part := range strings.Split(relative, "/") {
		cursor = filepath.Join(cursor, part)
		if isSymlinkPath(cursor) {
			return fail(errors.New("Report destination contains a symlink"))
		}
	}
	if status.GitOperationInProgress(root) {
		return fail(errors.New("Active Git operation in progress"))
	}
	placement := safecommit.Placement(root, filepath.Base(dir), mission.KindAnalysisReport)
	if owned != nil {
		placement = safecommit.OwnedPlacement(owned, mission.KindAnalysisReport)
	}
	if placement != targetBranch {
		return fail(errors.New("Analysis report placement changed before preflight"))
	}
	if _, err := safecommit.Preflight(root, root, targetBranch, []string{report}, safecommit.Options{Owned: owned}); err != nil {
		return fail(err)
	}
	inputs, err := missionart.CollectMaterialInputs(dir, root)
	if err != nil {
		return fail(err)
	}
	material := map[string]bool{relative: true}
	for _, kv := range inputs.Pairs() {
		if p := kv.Value.(*jsonx.Object).GetString("path"); p != "" {
			material[p] = true
		}
	}
	dirty, err := dirtyPathSet(root)
	if err != nil {
		return fail(err)
	}
	relevant := map[string]bool{}
	for p := range dirty {
		if material[p] {
			relevant[p] = true
		}
	}
	trackedOut, err := gitOut(root, "ls-files", "-z")
	if err != nil {
		return fail(err)
	}
	tracked := map[string]bool{}
	for _, p := range strings.Split(trackedOut, "\x00") {
		tracked[p] = true
	}
	for p := range material {
		if p != relative && isRegularFile(filepath.Join(root, p)) && !tracked[p] {
			relevant[p] = true
		}
	}
	if len(relevant) > 0 {
		keys := make([]string, 0, len(relevant))
		for k := range relevant {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return &reportTxn{payload: jsonx.NewObject("success", false, "commit_status", "failed_before_write", "error_code", "DIRTY_ANALYSIS_INPUT", "dirty_paths", strList(keys))}
	}
	if h, err := gitOut(root, "rev-parse", "HEAD"); err == nil {
		head = strings.TrimSpace(h)
	} else {
		return fail(err)
	}
	index, err := indexSnapshot(root, relative)
	if err != nil {
		return fail(err)
	}
	working, err := workingSnapshot(root, relative)
	if err != nil {
		return fail(err)
	}
	preview, err := renderAnalysisReport(dir, root, body, agent, inputs, token)
	if err != nil {
		return fail(err)
	}
	sameInputs := func() bool {
		cur, err := missionart.CollectMaterialInputs(dir, root)
		return err == nil && jsonx.Dumps(cur, jsonx.Options{Indent: -1}) == jsonx.Dumps(inputs, jsonx.Options{Indent: -1})
	}
	unchangedState := func() bool {
		h, _ := gitOut(root, "rev-parse", "HEAD")
		i, ierr := indexSnapshot(root, relative)
		w, werr := workingSnapshot(root, relative)
		return strings.TrimSpace(h) == head && ierr == nil && i == index && werr == nil && w == working && sameInputs()
	}
	if existing, err := os.ReadFile(report); err == nil {
		meta, _ := splitCarrierMeta(string(existing))
		if meta != nil {
			tok, _ := meta.Get("report_transaction")
			es, ok1 := reportSemantics(string(existing))
			rs, ok2 := reportSemantics(preview.content)
			if ReportIsQualified(root, report, tok) && ok1 && ok2 && es == rs {
				now, _ := os.ReadFile(report)
				if !unchangedState() || !bytes.Equal(now, existing) {
					return fail(errors.New("Repository changed during unchanged-report verification"))
				}
				p := preview.result.Clone()
				p.Set("success", true)
				p.Set("commit_status", "unchanged")
				p.Set("commit_hash", nil)
				return &reportTxn{payload: p}
			}
		}
	}
	rp, err := receiptPath(root, token)
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(rp), 0o755); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(rp, []byte(jsonx.Dumps(jsonx.NewObject("state", "pending", "report", relative), jsonx.Options{Indent: -1, EnsureASCII: true})), 0o644); err != nil {
		return fail(err)
	}
	result, err := renderAnalysisReport(dir, root, body, agent, inputs, token)
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(report, []byte(result.content), 0o644); err != nil {
		return fail(err)
	}
	wrote = true
	if sha256File(report) != result.sha {
		return fail(errors.New("Report changed after rendering; retained report is unqualified"))
	}
	if status.GitOperationInProgress(root) {
		return fail(errors.New("Active Git operation in progress"))
	}
	if !unchangedState() {
		return fail(errors.New("Repository changed before report commit; retained report is unqualified"))
	}
	outcome := safecommit.CommitForMission(root, filepath.Base(dir), []string{report}, message, mission.KindAnalysisReport, safecommit.RouterOptions{Owned: owned})
	if outcome.Status != safecommit.StatusCommitted || outcome.CommitHash == "" || !outcome.Success() {
		refused = outcome
		msg := outcome.Diagnostic
		if msg == "" {
			switch {
			case outcome.Status == safecommit.StatusCommitted && outcome.CommitHash == "":
				msg = "Report commit did not complete: committed status without a resolved commit hash"
			case outcome.Status == safecommit.StatusCommitted:
				msg = "Report committed on its own surface, but a sibling surface was refused or errored"
			default:
				msg = "Report commit did not complete: " + outcome.Status
			}
		}
		return fail(errors.New(msg))
	}
	committed = outcome.CommitHash
	parents, _ := gitOut(root, "rev-list", "--parents", "-n", "1", committed)
	changed := safecommit.CommittedPaths(root, committed)
	nowHead, _ := gitOut(root, "rev-parse", "HEAD")
	shown, _ := gitOut(root, "show", committed+":"+relative)
	cur, _ := os.ReadFile(report)
	i, ierr := indexSnapshot(root, relative)
	w, werr := workingSnapshot(root, relative)
	if strings.Join(strings.Fields(parents), " ") != committed+" "+head || len(changed) != 1 || changed[0] != relative ||
		strings.TrimSpace(nowHead) != committed || ierr != nil || i != index || werr != nil || w != working || !sameInputs() ||
		sha256Hex(cur) != result.sha || shown != string(cur) {
		return fail(errors.New("Post-commit verification failed; retained commit requires recovery"))
	}
	_ = os.WriteFile(rp, []byte(jsonx.Dumps(jsonx.NewObject("state", "qualified", "report", relative, "sha256", result.sha, "commit", committed),
		jsonx.Options{Indent: -1, EnsureASCII: true})), 0o644)
	p := result.result.Clone()
	p.Set("success", true)
	p.Set("commit_status", "committed")
	p.Set("commit_hash", committed)
	p.Set("surfaces", outcome.SurfacesPayload())
	return &reportTxn{payload: p, router: outcome}
}

func isSymlinkPath(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// runReportOnly is record_analysis's --report-only leg (_run_report_only).
func runReportOnly(ctx *cli.Context, root, slug string, jsonMode bool, fail func(string) error, owned *ownedcheckout.Fact) error {
	var body []byte
	var err error
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
	target := safecommit.TargetBranch(root, slug)
	if owned != nil {
		dir = owned.MissionDir
		target = owned.WriteBranch
	}
	txn := recordReportTransaction(root, dir, string(body), ctx.String("agent"), target, owned)
	if owned != nil {
		txn.payload.Set("stale_repository_root_copy", ownedcheckout.StaleCopyValue(owned, nil))
	}
	if jsonMode {
		emitJSON(ctx.Stdout, txn.payload)
	} else {
		rich.PrintPretty(ctx.Stdout, txn.payload)
		if txn.router != nil && surfaceNeedsWarningPlain(txn.router) {
			txn.router.PrintRendered(ctx.Stdout, wrapPlain)
		}
	}
	if v, _ := txn.payload.Get("success"); v != true {
		return cli.Exit(1)
	}
	return nil
}
