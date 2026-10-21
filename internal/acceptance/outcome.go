package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// compact is json.dumps defaults (", " / ": " separators, ensure_ascii).
var compact = jsonx.Options{Indent: -1, EnsureASCII: true}

// Surface outcome statuses and reason codes (coordination/commit_outcome.py).
const (
	StatusCommitted         = "committed"
	StatusUnchanged         = "unchanged"
	StatusRefused           = "refused"
	StatusError             = "error"
	ReasonNoChanges         = "no_op_no_changes"
	ReasonAlreadyCommitted  = "no_op_already_committed"
	ReasonProtectedBranch   = "PROTECTED_BRANCH_REFUSED"
	ReasonCoordRecordInRoot = "COORD_RECORD_IN_ROOT_CHECKOUT"
)

// PathFate is one path's disposition within a SurfaceOutcome.
type PathFate struct {
	Path       string
	Reason     string
	OwningPath *string
}

// SurfaceOutcome is one partition group's commit outcome.
type SurfaceOutcome struct {
	Surface    string // "primary" | "coordination"
	Branch     string
	Status     string
	CommitHash string // "" = null
	Committed  []string
	Skipped    []PathFate
	Refused    []PathFate
	Diagnostic string // "" = null
}

// Outcome is a commit-router / write-seam result.
type Outcome struct {
	Status             string
	DestinationSurface string
	CommitHash         string
	Diagnostic         string
	Surfaces           []SurfaceOutcome
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func fatesJSON(fs []PathFate) []any {
	out := []any{}
	for _, f := range fs {
		var op any
		if f.OwningPath != nil {
			op = *f.OwningPath
		}
		out = append(out, jsonx.NewObject("path", f.Path, "reason", f.Reason, "owning_path", op))
	}
	return out
}

// SurfacesJSON is commit_outcome_payload(result)["surfaces"].
func (o *Outcome) SurfacesJSON() []any {
	out := []any{}
	for _, s := range o.Surfaces {
		committed := []any{}
		for _, c := range s.Committed {
			committed = append(committed, c)
		}
		out = append(out, jsonx.NewObject("surface", s.Surface, "branch", s.Branch, "status", s.Status,
			"commit_hash", nullable(s.CommitHash), "committed", committed, "skipped", fatesJSON(s.Skipped),
			"refused", fatesJSON(s.Refused), "diagnostic", nullable(s.Diagnostic)))
	}
	return out
}

// Render is render_commit_outcome.
func (o *Outcome) Render() []string {
	var lines []string
	for _, s := range o.Surfaces {
		label := fmt.Sprintf("%s (%s)", s.Surface, s.Branch)
		glyph := "✗"
		if s.Status == StatusCommitted || s.Status == StatusUnchanged {
			glyph = "✓"
		}
		switch {
		case s.Status == StatusCommitted:
			h := s.CommitHash
			if len(h) > 7 {
				h = h[:7]
			}
			lines = append(lines, fmt.Sprintf("%s %s: committed %s — %d files", glyph, label, h, len(s.Committed)))
		case s.Status == StatusUnchanged:
			lines = append(lines, fmt.Sprintf("%s %s: unchanged", glyph, label))
		case s.Status == StatusError && len(s.Refused) == 0:
			suffix := ""
			if s.Diagnostic != "" {
				suffix = " — " + s.Diagnostic
			}
			lines = append(lines, fmt.Sprintf("%s %s: error%s", glyph, label, suffix))
		case s.Status == StatusRefused && len(s.Refused) == 0:
			lines = append(lines, fmt.Sprintf("%s %s: refused", glyph, label))
		}
		for _, f := range s.Refused {
			lines = append(lines, fmt.Sprintf("✗ %s: refused — %s: %s", label, f.Path, f.Reason))
		}
		for _, f := range s.Skipped {
			if f.Reason == ReasonCoordRecordInRoot {
				lines = append(lines, fmt.Sprintf("✓ %s: skipped — %s: %s", label, f.Path, f.Reason))
			}
		}
	}
	return lines
}

// ExitCode is commit_outcome_exit_code.
func (o *Outcome) ExitCode() int {
	for _, s := range o.Surfaces {
		if s.Status == StatusRefused || s.Status == StatusError {
			return 1
		}
	}
	return 0
}

// relTo renders p relative to root (posix), falling back to p.
func relTo(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return p
	}
	return filepath.ToSlash(r)
}

// commitGroup stages and commits paths (absolute) in checkout on branch,
// producing one SurfaceOutcome.
func commitGroup(root, checkout, surface, branch, message string, paths []string, protected bool) SurfaceOutcome {
	out := SurfaceOutcome{Surface: surface, Branch: branch, Committed: []string{}}
	disp := make([]string, len(paths))
	rel := make([]string, len(paths))
	for i, p := range paths {
		disp[i] = relTo(root, p)
		rel[i] = relTo(checkout, p)
	}
	if protected {
		out.Status = StatusRefused
		for _, d := range disp {
			out.Refused = append(out.Refused, PathFate{Path: d, Reason: ReasonProtectedBranch})
		}
		out.Diagnostic = fmt.Sprintf("Refusing to commit to protected branch %s", mission.PyRepr(branch))
		return out
	}
	if head := gitx.CurrentBranch(checkout); head != branch {
		out.Status = StatusError
		out.Diagnostic = fmt.Sprintf("safe_commit: worktree %s HEAD is %s, expected %s. Run `git -C %s checkout %s` first.",
			checkout, mission.PyRepr(head), mission.PyRepr(branch), checkout, branch)
		return out
	}
	var existing []string
	for _, r := range rel {
		if _, err := os.Lstat(filepath.Join(checkout, r)); err == nil || gitx.OK(checkout, "ls-files", "--error-unmatch", "--", r) {
			existing = append(existing, r)
		}
	}
	if len(existing) > 0 {
		if _, err := gitx.Run(checkout, append([]string{"add", "-A", "--"}, existing...)...); err != nil {
			out.Status = StatusError
			out.Diagnostic = err.Error()
			return out
		}
	}
	changed := map[string]bool{}
	if len(existing) > 0 {
		names, _ := gitx.Run(checkout, append([]string{"diff", "--cached", "--name-only", "--"}, existing...)...)
		for _, n := range strings.Split(strings.TrimSpace(names), "\n") {
			if n != "" {
				changed[n] = true
			}
		}
	}
	if len(changed) == 0 {
		out.Status = StatusUnchanged
		for _, d := range disp {
			out.Skipped = append(out.Skipped, PathFate{Path: d, Reason: ReasonNoChanges})
		}
		return out
	}
	var commitPaths []string
	for i, r := range rel {
		if changed[r] {
			commitPaths = append(commitPaths, r)
			out.Committed = append(out.Committed, disp[i])
		} else {
			out.Skipped = append(out.Skipped, PathFate{Path: disp[i], Reason: ReasonNoChanges})
		}
	}
	if _, err := gitx.Run(checkout, append([]string{"commit", "-q", "-m", message, "--"}, commitPaths...)...); err != nil {
		out.Status = StatusError
		out.Committed = []string{}
		out.Diagnostic = err.Error()
		return out
	}
	sha, _ := gitx.HeadSHA(checkout)
	out.Status = StatusCommitted
	out.CommitHash = sha
	return out
}

// legacyStatus folds per-surface statuses into the router's top-level one.
func legacyStatus(ss []SurfaceOutcome) (string, string, string) {
	status, hash, diag := StatusUnchanged, "", ""
	for _, s := range ss {
		switch s.Status {
		case StatusCommitted:
			if status == StatusUnchanged {
				status = StatusCommitted
			}
			hash = s.CommitHash
		case StatusRefused, StatusError:
			status = s.Status
			diag = s.Diagnostic
		}
	}
	return status, hash, diag
}

// CommitArtifact is write_artifact for a coordination-partition artifact
// kind (acceptance matrix, status views): files under the coordination
// mission dir commit on the coordination branch, other files on the
// primary target. paths are absolute.
func CommitArtifact(root, slug string, paths []string, message string) *Outcome {
	sf := coord.Resolve(root, slug)
	return routeCommit(root, sf, paths, message, "")
}

// CommitMission is commit_for_mission with an optional accept-only
// primaryRef override for the PRIMARY group.
func CommitMission(root, slug string, paths []string, message, primaryRef string) *Outcome {
	return routeCommit(root, coord.Resolve(root, slug), paths, message, primaryRef)
}

func routeCommit(root string, sf *coord.Surface, paths []string, message, primaryRef string) *Outcome {
	var prim, co []string
	for _, p := range paths {
		if sf.Routed() && strings.HasPrefix(p, sf.CoordWorktree+string(filepath.Separator)) {
			co = append(co, p)
		} else {
			prim = append(prim, p)
		}
	}
	res := &Outcome{}
	target := sf.TargetBranch()
	if len(prim) > 0 {
		branch := target
		if primaryRef != "" {
			branch = primaryRef
		}
		if branch == "" {
			branch = gitx.CurrentBranch(root)
		}
		protected := primaryRef == "" && coord.IsProtected(root, sf.Slug, branch)
		res.Surfaces = append(res.Surfaces, commitGroup(root, root, "primary", branch, message, prim, protected))
		res.DestinationSurface = branch
	}
	if len(co) > 0 {
		res.Surfaces = append(res.Surfaces, commitGroup(root, sf.CoordWorktree, "coordination", sf.CoordBranch, message, co, false))
		res.DestinationSurface = sf.CoordBranch
	}
	res.Status, res.CommitHash, res.Diagnostic = legacyStatus(res.Surfaces)
	return res
}

// MatrixWriteDir is placement_seam(...).write_dir(ACCEPTANCE_MATRIX): the
// coordination mission dir (materialized) for coordination topologies, the
// primary mission dir otherwise.
func MatrixWriteDir(root, slug string) (string, error) {
	return coord.Resolve(root, slug).WriteDir()
}

// MatrixReadDir is the gate's read-side resolution (no materialization).
func MatrixReadDir(root, slug string) string {
	return coord.Resolve(root, slug).StatusDir()
}

// WriteAndCommitMatrix is write_and_commit_acceptance_matrix.
func WriteAndCommitMatrix(root, slug, dir string, m *Matrix, message string) (*Outcome, error) {
	p, err := WriteMatrix(dir, m)
	if err != nil {
		return nil, err
	}
	return CommitArtifact(root, slug, []string{p}, message), nil
}
