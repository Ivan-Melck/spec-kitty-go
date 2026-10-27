package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// ChecksFailedMessage is ACCEPTANCE_CHECKS_FAILED_MESSAGE.
const ChecksFailedMessage = "Acceptance checks failed; run verify to see outstanding issues."

const historyCap = 20

var integrationBranches = map[string]bool{"main": true, "master": true, "develop": true, "development": true, "2.x": true, "3.x": true}

// ChooseMode is choose_mode.
func ChooseMode(pref, root string) string {
	switch pref {
	case "pr", "local", "checklist":
		return pref
	}
	if out := gitOut(root, "remote"); out != "" {
		return "pr"
	}
	return "local"
}

// ResolveActor is resolve_acceptance_actor.
func ResolveActor(actor string) string {
	if actor == "" {
		actor = os.Getenv("USER")
	}
	if actor == "" {
		actor = os.Getenv("USERNAME")
	}
	if actor == "" {
		actor = "system"
	}
	return strings.TrimSpace(actor)
}

// Result is AcceptanceResult.
type Result struct {
	Summary                         *Summary
	Mode, AcceptedAt, AcceptedBy    string
	ParentCommit, AcceptCommit      string
	CommitCreated                   bool
	Instructions, CleanupInstr      []string
	Notes                           []string
	Accepted, Approved, Done, Merge []string
}

// ToObject is AcceptanceResult.to_dict.
func (r *Result) ToObject() *jsonx.Object {
	canceled := []any{}
	for _, c := range r.Summary.CanceledWPs {
		canceled = append(canceled, c)
	}
	return jsonx.NewObject("accepted_at", r.AcceptedAt, "accepted_by", r.AcceptedBy, "mode", r.Mode,
		"parent_commit", nullStr(r.ParentCommit), "accept_commit", nullStr(r.AcceptCommit), "commit_created", r.CommitCreated,
		"instructions", strList(r.Instructions), "cleanup_instructions", strList(r.CleanupInstr), "notes", strList(r.Notes),
		"accepted_wps", strList(r.Accepted), "approved_wps", strList(r.Approved), "done_wps", strList(r.Done),
		"merge_pending_wps", strList(r.Merge), "canceled_wps", canceled, "summary", r.Summary.ToObject())
}

// RecordAcceptance is mission_metadata.record_acceptance.
func RecordAcceptance(featureDir, acceptedBy, mode, fromCommit, acceptCommit string) error {
	meta, err := mission.ReadMeta(featureDir)
	if err != nil {
		return err
	}
	now := clock.NowUTCISO()
	entry := jsonx.NewObject("accepted_at", now, "accepted_by", acceptedBy, "acceptance_mode", mode)
	if fromCommit != "" {
		entry.Set("accepted_from_commit", fromCommit)
	}
	if acceptCommit != "" {
		entry.Set("accept_commit", acceptCommit)
	}
	meta.Set("accepted_at", now)
	meta.Set("accepted_by", acceptedBy)
	meta.Set("acceptance_mode", mode)
	meta.Delete("accepted_from_commit")
	meta.Delete("accept_commit")
	if fromCommit != "" {
		meta.Set("accepted_from_commit", fromCommit)
	}
	if acceptCommit != "" {
		meta.Set("accept_commit", acceptCommit)
	}
	history := meta.GetList("acceptance_history")
	history = append(history, entry)
	if len(history) > historyCap {
		history = history[len(history)-historyCap:]
	}
	meta.Set("acceptance_history", history)
	return mission.WriteMeta(featureDir, meta, true)
}

func stampAcceptCommit(featureDir, sha string) bool {
	meta, err := mission.ReadMeta(featureDir)
	if err != nil {
		return false
	}
	meta.Set("accept_commit", sha)
	if h := meta.GetList("acceptance_history"); len(h) > 0 {
		if last, ok := h[len(h)-1].(*jsonx.Object); ok {
			last.Set("accept_commit", sha)
		}
	}
	return mission.WriteMeta(featureDir, meta, true) == nil
}

func stagedAny(root, rel string) bool {
	return !gitx.OK(root, "diff", "--cached", "--quiet", "--", rel)
}

// commitAcceptanceMeta is _commit_acceptance_meta.
func commitAcceptanceMeta(s *Summary, actor, mode string) (parent, accept string, created bool, err error) {
	root := s.RepoRoot
	parent, _ = gitx.HeadSHA(root)
	// FR-010 locked pre-stamp verdict re-check.
	if s.MatrixDir == "" {
		if s.MatrixSkipReason != PlanningArtifactOnlySkip {
			return parent, "", false, &Error{fmt.Sprintf("Cannot record acceptance: no acceptance-matrix gate outcome was recorded for mission %s and this is not a planning-artifact-only mission. Refusing to stamp acceptance without the FR-010 pre-stamp verdict re-check.", mission.PyRepr(s.Feature))}
		}
	} else {
		m, _ := ReadMatrix(s.MatrixDir)
		verdict := "pending"
		if m != nil {
			verdict = m.OverallVerdict()
		}
		if verdict != "pass" && verdict != VerdictPassPendingConsolidation {
			return parent, "", false, &Error{fmt.Sprintf("Acceptance matrix verdict is not ready to accept: Acceptance matrix at %s carries verdict %s, not pass; refusing to record acceptance.", s.MatrixDir, mission.PyRepr(verdict))}
		}
	}
	if err := RecordAcceptance(s.FeatureDir, actor, mode, parent, ""); err != nil {
		return parent, "", false, err
	}
	metaRel := relTo(root, filepath.Join(s.FeatureDir, mission.MetaFile))
	branch := gitx.CurrentBranch(root)
	if branch != "" && coord.IsProtected(root, s.Feature, branch) {
		// _commit_acceptance_meta_via_router: commit_for_mission with the
		// PRIMARY_METADATA kind (a protected placement refuses as
		// no_op_wrong_surface with the router's remedy).
		metaPath := filepath.Join(s.FeatureDir, mission.MetaFile)
		out := safecommit.CommitForMission(root, s.Feature, []string{metaPath}, "Accept "+s.Feature, mission.KindPrimaryMetadata, safecommit.RouterOptions{})
		if out.Status == StatusUnchanged {
			return parent, "", false, nil
		}
		if out.Status != StatusCommitted {
			diag := out.Diagnostic
			if diag == "" {
				diag = "no diagnostic available"
			}
			return parent, "", false, &Error{fmt.Sprintf("Acceptance commit failed (%s): %s", out.Status, diag)}
		}
		accept = out.CommitHash
		if accept != "" && stampAcceptCommit(s.FeatureDir, accept) {
			safecommit.CommitForMission(root, s.Feature, []string{metaPath}, "Record acceptance commit for "+s.Feature, mission.KindPrimaryMetadata, safecommit.RouterOptions{})
		}
		return parent, accept, true, nil
	}
	if _, err := gitx.Run(root, "add", metaRel); err != nil {
		return parent, "", false, err
	}
	if !stagedAny(root, metaRel) {
		return parent, "", false, nil
	}
	if _, err := gitx.Run(root, "commit", "-q", "-m", "Accept "+s.Feature, "--", metaRel); err != nil {
		return parent, "", false, err
	}
	accept, _ = gitx.HeadSHA(root)
	if accept != "" && stampAcceptCommit(s.FeatureDir, accept) {
		gitx.Run(root, "add", metaRel)
		if stagedAny(root, metaRel) {
			if _, err := gitx.Run(root, "commit", "-q", "-m", "Record acceptance commit for "+s.Feature, "--", metaRel); err != nil {
				return parent, accept, true, err
			}
		}
	}
	return parent, accept, true, nil
}

// Perform is perform_acceptance.
func Perform(s *Summary, mode, actor string, tests []string, autoCommit bool) (*Result, error) {
	if mode != "checklist" && !s.OK() {
		return nil, &Error{ChecksFailedMessage}
	}
	actor = ResolveActor(actor)
	r := &Result{Summary: s, Mode: mode, AcceptedBy: actor, AcceptedAt: clock.Now().UTC().Format("2006-01-02T15:04:05Z")}
	if autoCommit && mode != "checklist" {
		var err error
		r.ParentCommit, r.AcceptCommit, r.CommitCreated, err = commitAcceptanceMeta(s, actor, mode)
		if err != nil {
			return nil, err
		}
	}
	branch := s.Branch
	if branch == "" {
		branch = s.Feature
	}
	target := strings.TrimSpace(mission.ReadMetaOrEmpty(s.FeatureDir).GetString("target_branch"))
	integration := branch == target || (target == "" && integrationBranches[branch])
	switch mode {
	case "pr":
		if integration {
			r.Instructions = append(r.Instructions, fmt.Sprintf("Acceptance recorded on integration branch `%s`. Push and open a pull request if needed.", branch))
		} else {
			r.Instructions = append(r.Instructions, fmt.Sprintf("Review the acceptance commit on branch `%s`.", branch),
				fmt.Sprintf("Run the mission consolidation when ready: `spec-kitty consolidate --mission %s`", s.Feature),
				"After merge, run /spec-kitty-mission-review and the retrospective workflow.")
		}
	case "local":
		if integration {
			r.Instructions = append(r.Instructions, fmt.Sprintf("Acceptance recorded directly on `%s`. No merge needed.", branch))
		} else {
			r.Instructions = append(r.Instructions, fmt.Sprintf("Acceptance passed. Run the mission consolidation: `spec-kitty consolidate --mission %s`", s.Feature),
				"After merge, run /spec-kitty-mission-review and the retrospective workflow.")
		}
	default:
		r.Instructions = append(r.Instructions, fmt.Sprintf("All checks passed. Recommended next step: `spec-kitty consolidate --mission %s`.", s.Feature))
	}
	if s.WorktreeRoot != s.PrimaryRepoRoot {
		r.CleanupInstr = append(r.CleanupInstr, fmt.Sprintf("After merging, remove the worktree: `git worktree remove %s`", s.WorktreeRoot))
	}
	if !integration {
		r.CleanupInstr = append(r.CleanupInstr, fmt.Sprintf("Delete the Mission branch when done: `git branch -d %s`", branch))
	}
	if r.AcceptCommit != "" {
		r.Notes = append(r.Notes, "Acceptance commit: "+r.AcceptCommit)
	}
	if r.ParentCommit != "" {
		r.Notes = append(r.Notes, "Accepted from parent commit: "+r.ParentCommit)
	}
	if len(tests) > 0 {
		r.Notes = append(r.Notes, "Validation commands:")
		for _, t := range tests {
			r.Notes = append(r.Notes, "  - "+t)
		}
	}
	r.Accepted, r.Approved, r.Done, r.Merge = s.LaneDerivations()
	return r, nil
}

// StampBirthCutover is _stamp_birth_cutover_for_accept, reduced to the
// status_phase flip (the legacy frontmatter seed is a no-op for missions
// created by this version). A WP file the seed cannot read (no frontmatter)
// fails upstream's best-effort cutover, which then leaves status_phase alone.
func StampBirthCutover(featureDir string) error {
	meta, err := mission.ReadMeta(featureDir)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(meta.GetString("mission_id")) == "" {
		return &Error{fmt.Sprintf("Refusing to stamp birth-cutover for %s: meta.json carries no mission_id (fail-closed, NFR-003/R6 — no slug-namespaced seed fallback).", mission.PyRepr(filepath.Base(featureDir)))}
	}
	if meta.GetString("status_phase") == "1" {
		return nil
	}
	if migrate.LegacyRuntimeReadError(featureDir) != nil {
		return nil
	}
	meta.Set("status_phase", "1")
	return mission.WriteMeta(featureDir, meta, true)
}

// dirtyTracked lists tracked-modified paths under prefix in checkout.
func dirtyTracked(checkout, prefix string) []string {
	entries, err := StatusEntries(checkout)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.XY != "??" && strings.HasPrefix(e.Path, prefix) {
			out = append(out, e.Path)
		}
	}
	return out
}

// missionLedgerFiles is ledger_dirt.mission_decision_ledger_files: the dirty
// (modified or untracked) decision-ledger files of the mission, a collapsed
// untracked directory expanded to its files. The decision service's runtime
// lock is never listed: it feeds accept's residual commit, and the lock is
// kept out of every commit.
func missionLedgerFiles(entries []StatusEntry, root, slug string) []string {
	var out []string
	for _, e := range entries {
		files := []string{e.Path}
		if e.IsDir {
			files = nil
			filepath.WalkDir(filepath.Join(root, e.Path), func(p string, d os.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() {
					if rel, rerr := filepath.Rel(root, p); rerr == nil {
						files = append(files, filepath.ToSlash(rel))
					}
				}
				return nil
			})
			sort.Strings(files)
		}
		for _, p := range files {
			if isMissionLedger(StatusEntry{Path: p}, slug) && !IsDecisionsRuntimeLock(p) && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// ResidualCommit is _run_residual_acceptance_commit (nil when clean).
func ResidualCommit(root, slug string) *Outcome {
	prim := dirtyTracked(root, "kitty-specs/"+slug+"/")
	if entries, err := StatusEntries(root); err == nil {
		for _, p := range missionLedgerFiles(entries, root, slug) {
			if !slices.Contains(prim, p) {
				prim = append(prim, p)
			}
		}
	}
	sf := coord.Resolve(root, slug)
	var co []string
	if sf.Routed() {
		if st, err := os.Stat(sf.CoordDir); err == nil && st.IsDir() {
			prefix := relTo(sf.CoordWorktree, sf.CoordDir) + "/"
			co = dirtyTracked(sf.CoordWorktree, prefix)
		}
	}
	if len(prim) == 0 && len(co) == 0 {
		return nil
	}
	var files []string
	for _, p := range prim {
		files = append(files, filepath.Join(root, p))
	}
	for _, p := range co {
		files = append(files, filepath.Join(sf.CoordWorktree, p))
	}
	ref := ""
	if b := gitx.CurrentBranch(root); b != "" && !coord.IsProtected(root, slug, b) {
		ref = b
	}
	return CommitMission(root, slug, files, "Finalize acceptance artifacts for "+slug, ref)
}

// ResidualFailureDetail is _format_residual_failure_detail.
func ResidualFailureDetail(o *Outcome) string {
	parts := o.Render()
	for _, s := range o.Surfaces {
		if (s.Status == StatusRefused || s.Status == StatusError) && s.Diagnostic != "" {
			line := fmt.Sprintf("%s (%s): %s", s.Surface, s.Branch, s.Diagnostic)
			dup := false
			for _, p := range parts {
				if p == line {
					dup = true
				}
			}
			if !dup {
				parts = append(parts, line)
			}
		}
	}
	if len(parts) == 0 {
		if o.Diagnostic != "" {
			return o.Diagnostic
		}
		return "unknown error"
	}
	return strings.Join(parts, "; ")
}
