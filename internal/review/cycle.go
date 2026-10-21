// Package review implements the review-cycle evidence artifacts
// (kitty-specs/<mission>/tasks/<wp-slug>/review-cycle-N.md): allocation,
// writing, commit + read-back durability, pointers and synthetic refs
// (specify_cli.review.cycle / artifacts).
package review

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Synthetic review_ref tokens (#4327).
func SyntheticReviewRef(wpID string) string   { return "review:" + wpID }
func SyntheticApprovalRef(wpID string) string { return "approval:" + wpID }
func SyntheticAutoApprovalRef(wpID, date string) string {
	return "auto-approval:" + wpID + ":" + date
}

// Error is ReviewCycleError.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

var cycleFileRe = regexp.MustCompile(`^review-cycle-([1-9][0-9]*)\.md$`)

// Pointer is build_review_cycle_pointer.
func Pointer(missionSlug, wpSlug, filename string) string {
	return "review-cycle://" + missionSlug + "/" + wpSlug + "/" + filename
}

// Outcome is VerdictPersistenceOutcome.
type Outcome struct {
	Classification string // durable | local_only | persistence_failed
	Durable        bool
	EvidenceRef    *string
	DestinationRef *string
	Reason         *string
}

// Cycle is CreatedRejectedReviewCycle.
type Cycle struct {
	ArtifactPath string
	Pointer      string
	CycleNumber  int
	Result       status.ReviewResult
	Outcome      Outcome
}

// CreateInput carries create_rejected_review_cycle's inputs. Exactly one of
// FeedbackSource / Body must be set.
type CreateInput struct {
	Surface             *coord.Surface
	WPID, WPSlug        string
	FeedbackSource      string
	Body                string
	Reviewer            string
	Verdict             string // "approved" | "rejected"
	ReproductionCommand *string
	Commit              bool // commit router available (auto-commit)
}

// WriteRoot returns (surface root, mission dir) review cycles are written
// to: the coordination worktree when routed, else the primary checkout.
func WriteRoot(s *coord.Surface) (string, string, error) {
	if s.Routed() {
		if err := s.EnsureWorktree(); err == nil {
			return s.CoordWorktree, s.CoordDir, nil
		} else if err != coord.ErrNoCoordBranch {
			return "", "", err
		}
	}
	return s.Root, s.PrimaryDir, nil
}

// DestinationRef is the governed REVIEW_CYCLE write target ref.
func DestinationRef(s *coord.Surface) string {
	if s.Routed() {
		return s.CoordBranch
	}
	return s.TargetBranch()
}

// NextCycleNumber is ReviewCycleArtifact.next_cycle_number over dirs.
func NextCycleNumber(dirs ...string) (int, error) {
	max := 0
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasPrefix(n, "review-cycle-") || !strings.HasSuffix(n, ".md") {
				continue
			}
			m := cycleFileRe.FindStringSubmatch(n)
			if m == nil {
				return 0, &Error{Msg: fmt.Sprintf("Cannot determine next review cycle number: unparseable review-cycle file %s", n)}
			}
			if v, _ := strconv.Atoi(m[1]); v > max {
				max = v
			}
		}
	}
	return max + 1, nil
}

// RenderArtifact renders ReviewCycleArtifact.write's file content.
func RenderArtifact(cycle int, wpID, missionSlug, reviewer, reviewedAt string, repro *string, body string) string {
	var r any
	if repro != nil {
		r = *repro
	}
	front := jsonx.NewObject("affected_files", []any{}, "cycle_number", cycle, "mission_slug", missionSlug,
		"reproduction_command", r, "reviewed_at", reviewedAt, "reviewer_agent", reviewer, "wp_id", wpID)
	out := "---\n" + yamlx.Emit(front) + "---\n"
	if body != "" {
		out += "\n" + body
	}
	return out
}

func resolveBody(in CreateInput, subDir string) (string, error) {
	if in.FeedbackSource == "" {
		if strings.TrimSpace(in.Body) == "" {
			return "", &Error{Msg: "Review feedback body is empty"}
		}
		return in.Body, nil
	}
	src := in.FeedbackSource
	st, err := os.Stat(src)
	if err != nil {
		return "", &Error{Msg: "Review feedback file not found: " + src}
	}
	if st.IsDir() {
		return "", &Error{Msg: "Review feedback path is not a file: " + src}
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	body := string(b)
	if strings.TrimSpace(body) == "" {
		return "", &Error{Msg: "Review feedback file is empty: " + src}
	}
	if abs, err := filepath.Abs(src); err == nil {
		if sd, err := filepath.Abs(subDir); err == nil && filepath.Dir(abs) == sd && cycleFileRe.MatchString(filepath.Base(abs)) {
			return "", &Error{Msg: fmt.Sprintf("feedback_source is this WP's own review-cycle artifact (%s); pass the underlying reviewer feedback instead of a prior review-cycle artifact.", filepath.Base(abs))}
		}
	}
	if looksLikeArtifact(body) {
		return "", &Error{Msg: fmt.Sprintf("feedback_source content parses as a review-cycle artifact (%s); pass distinct reviewer feedback instead of a prior review-cycle artifact's content.", filepath.Base(src))}
	}
	return body, nil
}

func looksLikeArtifact(text string) bool {
	doc, err := fm.Parse(text)
	if err != nil || doc == nil || doc.Meta == nil {
		return false
	}
	for _, k := range []string{"cycle_number", "wp_id", "mission_slug", "reviewer_agent", "reviewed_at"} {
		if !doc.Meta.Has(k) {
			return false
		}
	}
	return true
}

func strp(s string) *string { return &s }

// Create is create_rejected_review_cycle: allocate review-cycle-N.md on the
// REVIEW_CYCLE write surface, write it, and (when in.Commit) commit it
// ("chore: Record review-cycle-N (<verdict>) for WPnn on <mission>") and
// verify the committed bytes at the destination ref.
func Create(in CreateInput) (*Cycle, error) {
	s := in.Surface
	surfaceRoot, missionDir, err := WriteRoot(s)
	if err != nil {
		return nil, err
	}
	subDir := filepath.Join(missionDir, "tasks", in.WPSlug)
	primarySub := filepath.Join(s.PrimaryDir, "tasks", in.WPSlug)
	body, err := resolveBody(in, subDir)
	if err != nil {
		return nil, err
	}
	n, err := NextCycleNumber(subDir, primarySub)
	if err != nil {
		return nil, err
	}
	filename := fmt.Sprintf("review-cycle-%d.md", n)
	reviewer := in.Reviewer
	if reviewer == "" {
		reviewer = "unknown"
	}
	path := filepath.Join(subDir, filename)
	content := RenderArtifact(n, in.WPID, s.Slug, reviewer, clock.Now().UTC().Format("2006-01-02T15:04:05Z"), in.ReproductionCommand, body)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	f.WriteString(content)
	f.Close()
	rel, _ := filepath.Rel(surfaceRoot, path)
	evidence := filepath.ToSlash(rel)
	dest := DestinationRef(s)
	c := &Cycle{ArtifactPath: path, Pointer: Pointer(s.Slug, in.WPSlug, filename), CycleNumber: n}
	verdict := "changes_requested"
	if in.Verdict == "approved" {
		verdict = "approved"
	}
	c.Result = status.ReviewResult{Reviewer: reviewer, Verdict: verdict, Reference: c.Pointer, FeedbackPath: strp(path)}
	if !in.Commit {
		c.Outcome = Outcome{Classification: "local_only", EvidenceRef: strp(evidence), Reason: strp("no_auto_commit")}
		return c, nil
	}
	v := in.Verdict
	if v == "" {
		v = "rejected"
	}
	msg := fmt.Sprintf("chore: Record review-cycle-%d (%s) for %s on %s", n, v, in.WPID, s.Slug)
	if _, err := coord.CommitPaths(surfaceRoot, msg, path); err != nil {
		c.Outcome = Outcome{Classification: "persistence_failed", EvidenceRef: strp(evidence), DestinationRef: strp(dest), Reason: strp("commit_error")}
		return c, nil
	}
	got, err := gitx.Run(s.Root, "show", dest+":"+evidence)
	if err == nil && bytes.Equal([]byte(strings.TrimRight(got, "\n")), []byte(strings.TrimRight(content, "\n"))) {
		c.Outcome = Outcome{Classification: "durable", Durable: true, EvidenceRef: strp(evidence), DestinationRef: strp(dest)}
	} else {
		reason := "destination_readback_mismatch"
		if err != nil {
			reason = "destination_readback_missing"
		}
		c.Outcome = Outcome{Classification: "persistence_failed", EvidenceRef: strp(evidence), DestinationRef: strp(dest), Reason: strp(reason)}
	}
	return c, nil
}
