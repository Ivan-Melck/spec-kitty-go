package migrate

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Anchor evidence classes (consolidation.baseline).
const (
	AnchorMergeCommitParentAttested = "merge-commit-parent-attested"
	AnchorCorpusParentAttested      = "corpus-parent-attested"
)

// PrMergeEvidenceError is PrMergeEvidenceError / BaselineMergeCommitError.
type PrMergeEvidenceError struct{ Msg string }

func (e *PrMergeEvidenceError) Error() string { return e.Msg }

// PrMergeEvidence is the verified landing evidence.
type PrMergeEvidence struct {
	PrMergeCommit, BaselineMergeCommit, AnchorEvidence string
}

var hexSHARe = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

func revVerify(root, rev string) (string, error) {
	res, err := gitx.Exec(root, nil, nil, "rev-parse", "--verify", rev)
	if err != nil || res.Code != 0 {
		detail := ""
		if err == nil {
			detail = strings.TrimSpace(res.Stderr)
		}
		if detail == "" {
			detail = "git rev-parse failed"
		}
		return "", &PrMergeEvidenceError{fmt.Sprintf("cannot resolve %s to a commit in this repository (%s); fetch the target branch first, then retry", pyjson.ReprString(rev), detail)}
	}
	return strings.TrimSpace(res.Stdout), nil
}

var commonPrimaryBranches = []string{"main", "master", "develop"}

// PrimaryBranch is git_ops.resolve_primary_branch.
func PrimaryBranch(root string, bias bool) string {
	if out, err := gitx.Run(root, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		if o := strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/origin/"); o != "" {
			return o
		}
	}
	current := gitx.CurrentBranch(root)
	if bias {
		if current != "" && current != "HEAD" {
			return current
		}
	} else {
		for _, c := range commonPrimaryBranches {
			if c == current {
				return current
			}
		}
	}
	for _, b := range commonPrimaryBranches {
		if bias {
			if gitx.OK(root, "rev-parse", "--verify", b) {
				return b
			}
		} else if gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+b) || gitx.OK(root, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+b) {
			return b
		}
	}
	if bias {
		return "main"
	}
	return PrimaryBranch(root, true)
}

// VerifyPrMergeEvidence is consolidation.baseline.verify_pr_merge_evidence.
func VerifyPrMergeEvidence(root, slug, mergeCommit string, targetRef *string, attest bool) (*PrMergeEvidence, error) {
	fail := func(f string, a ...any) error { return &PrMergeEvidenceError{fmt.Sprintf(f, a...)} }
	supplied := strings.TrimSpace(mergeCommit)
	if supplied == "" {
		return nil, fail("no merge commit was supplied")
	}
	if !hexSHARe.MatchString(supplied) {
		return nil, fail("%s is not a commit SHA (expected a hexadecimal SHA, full or abbreviated, as shown on the merged PR)", pyjson.ReprString(supplied))
	}
	pr, err := revVerify(root, supplied+"^{commit}")
	if err != nil {
		return nil, err
	}
	metaRel := "kitty-specs/" + slug + "/meta.json"
	if !gitx.OK(root, "cat-file", "-e", pr+":"+metaRel) {
		return nil, fail("commit %s does not carry %s; it is not the commit that landed mission %s", pr, metaRel, slug)
	}
	base, err := revVerify(root, pr+"^1")
	if err != nil {
		return nil, err
	}
	if gitx.OK(root, "cat-file", "-e", base+":"+metaRel) {
		return nil, fail("commit %s is not the landing commit for mission %s: %s already exists at its first parent %s. Supply the commit that introduced the mission corpus on the target branch", pr, slug, metaRel, base)
	}
	if gitx.OK(root, "cat-file", "-e", base+":kitty-specs/"+slug) {
		return nil, fail("commit %s is not the landing commit for mission %s: kitty-specs/%s/ already exists at its first parent %s (staged corpus — part of the mission landed before its meta.json). Anchoring there would skip the gate for the already-landed part; supply the commit that introduced the mission corpus", pr, slug, slug, base)
	}
	var target string
	if targetRef != nil {
		target = strings.TrimSpace(*targetRef)
		if target == "" {
			return nil, fail("the supplied target branch is empty")
		}
		if strings.HasPrefix(target, "-") {
			return nil, fail("%s is not a branch name (it looks like a git option)", pyjson.ReprString(target))
		}
	} else {
		meta, err := LoadMetaFailClosed(project.Specs(root, slug))
		if err != nil {
			return nil, fail("cannot read mission %s's declared target branch to verify the PR landing (%v).", slug, err)
		}
		declared := ""
		if meta != nil {
			if v, ok := meta.Get("target_branch"); ok && pyjson.Truthy(v) {
				declared = pyjson.Str(v)
			}
		}
		if declared != "" {
			if strings.HasPrefix(declared, "-") {
				return nil, fail("mission %s's declared target_branch %s is not a branch name", slug, pyjson.ReprString(declared))
			}
			target = declared
		} else {
			target = PrimaryBranch(root, false)
		}
	}
	if _, err := revVerify(root, target+"^{commit}"); err != nil {
		return nil, err
	}
	if !gitx.OK(root, "merge-base", "--is-ancestor", pr, target) {
		return nil, fail("commit %s is not on target branch %s: it never landed there. Supply the commit that landed mission %s on %s — the merge or squash commit shown on the merged PR. If the landing is newer than this checkout, fetch %s first; if the PR targeted a different branch, pass it explicitly (--target-branch)", pr, pyjson.ReprString(target), slug, target, target)
	}
	isMerge := gitx.OK(root, "rev-parse", "--verify", "--quiet", pr+"^2")
	if !attest {
		if isMerge {
			return nil, fail("commit %s is a merge commit, but two parents alone do not prove its first parent %s is the pre-landing target tip: a merge performed on the target branch is graph-identical to an internal merge (the corpus branch merged into the implementation branch, the target then fast-forwarded to the result), whose first parent is an implementation commit — anchoring there would silently skip the dead-code gate for that work. If this merge was performed ON the target branch, pass --attest-first-landing-commit to record that operator attestation. Full forge commit-list evidence is tracked in #4277.", pr, base)
		}
		return nil, fail("commit %s has a single parent, so git cannot prove its first parent %s is the pre-landing target tip: for a squash or corpus-first stack landing the parent IS the tip, but for a landing whose implementation commits preceded the corpus the parent is an earlier same-PR commit, and the two are graph-identical — no git check separates them. Recording it anyway would anchor the dead-code scan at the wrong tip and silently skip part of the gate. If the supplied commit was the first commit of the landing, pass --attest-first-landing-commit to record that operator attestation. Full forge commit-list evidence is tracked in #4277.", pr, base)
	}
	ev := AnchorCorpusParentAttested
	if isMerge {
		ev = AnchorMergeCommitParentAttested
	}
	return &PrMergeEvidence{PrMergeCommit: pr, BaselineMergeCommit: base, AnchorEvidence: ev}, nil
}

// RecordPrMergeBaseline is record_pr_merge_baseline_for_mission: verify,
// then stamp baseline_merge_commit / merged_at / merged_commit and the
// pr_merge_commit / pr_merge_evidence provenance on the primary meta.json.
func RecordPrMergeBaseline(root, slug, mergeCommit string, targetRef *string, attest bool) (*PrMergeEvidence, error) {
	dir := project.Specs(root, slug)
	ev, err := VerifyPrMergeEvidence(root, slug, mergeCommit, targetRef, attest)
	if err != nil {
		return nil, err
	}
	meta, err := LoadMetaFailClosed(dir)
	if err != nil {
		return nil, &PrMergeEvidenceError{fmt.Sprintf("cannot record a PR merge baseline for %s: meta.json is invalid (%v).", filepath.Base(dir), err)}
	}
	modern := false
	if meta != nil {
		if v, ok := meta.Get("mission_id"); ok && pyjson.Truthy(v) && strings.TrimSpace(pyjson.Str(v)) != "" {
			modern = true
		}
	}
	name := filepath.Base(dir)
	if meta == nil {
		if modern {
			return nil, &PrMergeEvidenceError{fmt.Sprintf("Cannot record baseline_merge_commit for modern mission %s: meta.json is missing.", name)}
		}
		warnf("Cannot record baseline_merge_commit for %s: meta.json is missing", name)
	} else {
		baseline := ev.BaselineMergeCommit
		if _, err := UpdateMeta(dir, func(fresh *jsonx.Object) bool {
			changed := false
			if v, _ := fresh.Get("baseline_merge_commit"); baseline != "" && !(pyjson.Truthy(v) && strings.TrimSpace(pyjson.Str(v)) != "") {
				fresh.Set("baseline_merge_commit", baseline)
				changed = true
			}
			if v, _ := fresh.Get("merged_at"); !pyjson.Truthy(v) || strings.TrimSpace(pyjson.Str(v)) == "" {
				fresh.Set("merged_at", clock.NowUTCISO())
				fresh.Set("merged_commit", ev.PrMergeCommit)
				changed = true
			}
			return changed
		}); err != nil {
			return nil, &PrMergeEvidenceError{err.Error()}
		}
	}
	if _, err := UpdateMeta(dir, func(fresh *jsonx.Object) bool {
		v1, _ := fresh.Get("pr_merge_commit")
		v2, _ := fresh.Get("pr_merge_evidence")
		c1 := pyjson.Truthy(v1) && strings.TrimSpace(pyjson.Str(v1)) != ""
		c2 := pyjson.Truthy(v2) && strings.TrimSpace(pyjson.Str(v2)) != ""
		if !c1 {
			fresh.Set("pr_merge_commit", ev.PrMergeCommit)
		}
		if !c2 {
			fresh.Set("pr_merge_evidence", ev.AnchorEvidence)
		}
		return !(c1 && c2)
	}); err != nil {
		if _, ok := err.(*MetaReadError); ok {
			return nil, &PrMergeEvidenceError{fmt.Sprintf("cannot stamp pr_merge_commit for %s: meta.json is invalid (%v).", name, err)}
		}
		return nil, &PrMergeEvidenceError{fmt.Sprintf("cannot stamp pr_merge_commit for %s: meta.json is missing.", name)}
	}
	return ev, nil
}
