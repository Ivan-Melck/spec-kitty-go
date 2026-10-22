package agentcore

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/issuematrix"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

const issueMatrixAction = "Fix the issue-matrix check before approval/merge."

// doctorHooks wires the doctor checks that live above internal/status.
func doctorHooks(m *missionCtx) status.DoctorHooks {
	return status.DoctorHooks{
		IssueMatrix: func() []status.Finding { return doctorIssueMatrix(m) },
		Sparse:      func() []status.Finding { return doctorSparse(m.Root) },
	}
}

var doctorRowIssueRe = regexp.MustCompile(`Row for issue '([^']+)'`)

// doctorIssueMatrix is status.doctor.check_issue_matrix: gating issue
// references discovered on the status surface vs the issue-matrix verdicts
// read from the issue-matrix partition (coordination worktree, else the
// coordination branch content, for coord topologies; primary otherwise).
func doctorIssueMatrix(m *missionCtx) []status.Finding {
	featureDir := m.ReadDir()
	refs, err := issuematrix.DiscoverReferences(featureDir)
	if err != nil {
		return []status.Finding{{Severity: "warning", Category: "issue_matrix", Message: "issue-matrix could not be evaluated: " + err.Error(), Action: issueMatrixAction}}
	}
	var gating []string
	referenced := map[string]bool{}
	for _, r := range refs {
		if r.Gating() {
			ref := fmt.Sprintf("#%d", r.Number)
			gating = append(gating, ref)
			referenced[ref] = true
		}
	}
	if len(gating) == 0 {
		return nil
	}
	matrixDir := m.PrimaryDir()
	var content *string
	if m.Surface.CoordBranch != "" {
		if isDir(m.Surface.CoordDir) {
			matrixDir = m.Surface.CoordDir
		} else {
			c, rerr := readIssueMatrixRef(m)
			if rerr != nil {
				return []status.Finding{{Severity: "warning", Category: "issue_matrix", Message: "issue-matrix could not be read from the coordination partition: " + rerr.Error(), Action: issueMatrixAction}}
			}
			matrixDir, content = featureDir, &c
		}
	}
	present := content != nil && *content != ""
	if content == nil {
		present = issuematrix.ArtifactPresent(matrixDir)
	}
	if !present {
		return []status.Finding{{Severity: "warning", Category: "issue_matrix",
			Message: fmt.Sprintf("Mission references GitHub issues but the issue-matrix is missing: %s.", strings.Join(gating, ", ")),
			Action:  "Create the issue-matrix and record final verdicts before approval/merge."}}
	}
	res := issuematrix.Validate(matrixDir+"/"+issuematrix.MDFile, content)
	inMatrix := map[string]bool{}
	for _, row := range res.Rows {
		inMatrix[row.Issue] = true
	}
	for _, d := range res.Diagnostics {
		if mm := doctorRowIssueRe.FindStringSubmatch(d.Message); mm != nil {
			inMatrix[mm[1]] = true
		}
	}
	var missing []string
	for ref := range referenced {
		if !inMatrix[ref] {
			missing = append(missing, ref)
		}
	}
	sort.Strings(missing)
	var out []status.Finding
	if len(missing) > 0 {
		out = append(out, status.Finding{Severity: "warning", Category: "issue_matrix",
			Message: fmt.Sprintf("issue-matrix.md is missing rows for referenced issue(s): %s.", strings.Join(missing, ", ")),
			Action:  "Add one row per referenced issue before approval/merge."})
	}
	for _, d := range res.Diagnostics {
		msg := d.Message
		if msg == "" {
			msg = "issue-matrix.md contains an unresolved issue verdict."
		}
		out = append(out, status.Finding{Severity: "warning", Category: "issue_matrix", Message: msg,
			Action: "Replace unknown/deferred issue verdicts with fixed, verified-already-fixed, a documented follow-up, or in-mission (until a later WP in this mission closes it)."})
	}
	return out
}

// readIssueMatrixRef is read_issue_matrix_ref_content for an unmaterialized
// coordination surface: issue-matrix.json, else issue-matrix.md, read from
// the coordination branch with `git show`.
func readIssueMatrixRef(m *missionCtx) (string, error) {
	root, ref := m.Root, m.Surface.CoordBranch
	if !gitx.OK(root, "rev-parse", "--verify", "--quiet", ref) {
		return "", fmt.Errorf("issue-matrix read ref %s does not resolve in %s — it has been deleted; refusing rather than falling back to the primary residue.", mission.PyRepr(ref), root)
	}
	show := func(name string) (string, string, error) {
		obj := ref + ":kitty-specs/" + m.Slug + "/" + name
		res, err := gitx.Exec(root, nil, nil, "show", obj)
		if err != nil {
			return "", obj, fmt.Errorf("git show %s could not be run in %s: %v", mission.PyRepr(obj), root, err)
		}
		if res.Code != 0 {
			return "", obj, &probeError{fmt.Sprintf("git show %s failed in %s: %s", mission.PyRepr(obj), root, strings.TrimSpace(res.Stderr))}
		}
		return res.Stdout, obj, nil
	}
	content, obj, err := show(issuematrix.JSONFile)
	if _, ok := err.(*probeError); ok {
		content, obj, err = show(issuematrix.MDFile)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("issue-matrix content at %s is empty — refusing rather than treating an empty authored set as nothing to enforce.", obj)
	}
	return content, nil
}

type probeError struct{ msg string }

func (e *probeError) Error() string { return e.msg }

// doctorSparse is status.doctor.check_sparse_checkout over scan_repo
// (primary plus lane / coordination worktrees).
func doctorSparse(root string) []status.Finding {
	rep := healthcheck.ScanSparse(root)
	if rep == nil || !rep.AnyBlocking() {
		return nil
	}
	lines := []string{"Legacy sparse-checkout state detected."}
	if rep.Primary.ConfigEnabled {
		note := ""
		if rep.Primary.PatternFilePresent {
			note = fmt.Sprintf(" (pattern file: %s, %d lines)", rep.Primary.PatternFile, rep.Primary.PatternLines)
		}
		lines = append(lines, "Primary: "+rep.Primary.Path+note)
	}
	var affected []string
	if rep.Primary.Blocking() {
		affected = append(affected, rep.Primary.Path)
	}
	var active []healthcheck.SparseState
	for _, w := range rep.Worktrees {
		if w.Blocking() {
			active = append(active, w)
			affected = append(affected, w.Path)
		}
	}
	if len(active) > 0 {
		lines = append(lines, fmt.Sprintf("Lane worktrees affected: %d", len(active)))
		for _, w := range active {
			lines = append(lines, "  "+w.Path)
		}
	}
	lines = append(lines, "Why this matters: spec-kitty v3.0+ removed sparse-checkout support but did not ship a migration. This state can cause silent data loss during mission merge and broken lane worktrees on agent action implement. See spec-kitty/spec-kitty#588.")
	return []status.Finding{{Severity: "warning", Category: "sparse_checkout", Message: strings.Join(lines, "\n"),
		Action: "Run 'spec-kitty doctor sparse-checkout --fix' to remove legacy sparse-checkout state from the primary repo and any lane worktrees. Affected paths: " + strings.Join(affected, ", ")}}
}

