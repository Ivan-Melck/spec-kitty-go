package healthcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// ---------------------------------------------------------------------------
// Workspace husks (status/doctor_husks.py)
// ---------------------------------------------------------------------------

// HuskEntry is one .worktrees/ entry lacking a .git entry.
type HuskEntry struct {
	Path       string // relative to the repo root
	Registered *bool  // nil when registration is unknown
}

// HuskReport is the husk scan result.
type HuskReport struct {
	WorktreesDir      string
	Husks             []HuskEntry
	RegistrationError *string
}

// Healthy reports no husks and no registration error.
func (r *HuskReport) Healthy() bool { return len(r.Husks) == 0 && r.RegistrationError == nil }

// ToObject is HuskReport.to_dict().
func (r *HuskReport) ToObject() *jsonx.Object {
	hs := []any{}
	for _, h := range r.Husks {
		var reg any
		if h.Registered != nil {
			reg = *h.Registered
		}
		hs = append(hs, jsonx.NewObject("path", h.Path, "registered", reg))
	}
	var re any
	if r.RegistrationError != nil {
		re = *r.RegistrationError
	}
	return jsonx.NewObject("worktrees_dir", r.WorktreesDir, "healthy", r.Healthy(), "registration_error", re, "husks", hs)
}

// RegisteredWorktrees is registered_worktree_paths (resolved paths).
func RegisteredWorktrees(root string) (map[string]bool, *string) {
	res, err := gitx.Exec(root, nil, nil, "worktree", "list", "--porcelain")
	if err != nil || res.Code != 0 {
		detail := ""
		if err == nil {
			detail = strings.TrimSpace(res.Stderr)
			if detail == "" {
				detail = strings.TrimSpace(res.Stdout)
			}
		}
		if detail == "" {
			detail = "exit 1"
		}
		e := "git worktree list --porcelain failed: " + detail
		return nil, &e
	}
	out := map[string]bool{}
	for _, l := range strings.Split(res.Stdout, "\n") {
		if strings.HasPrefix(l, "worktree ") {
			out[resolve(strings.TrimSpace(l[len("worktree "):]))] = true
		}
	}
	return out, nil
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	a, _ := filepath.Abs(p)
	return a
}

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func sortedChildren(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// ScanWorkspaceHusks is scan_workspace_husks.
func ScanWorkspaceHusks(root string) *HuskReport {
	wd := filepath.Join(root, project.WorktreesDir)
	rep := &HuskReport{WorktreesDir: wd}
	if !isDir(wd) {
		return rep
	}
	reg, regErr := RegisteredWorktrees(root)
	rep.RegistrationError = regErr
	for _, n := range sortedChildren(wd) {
		p := filepath.Join(wd, n)
		if !isDir(p) || exists(filepath.Join(p, ".git")) {
			continue
		}
		h := HuskEntry{Path: filepath.Join(project.WorktreesDir, n)}
		if regErr == nil {
			b := reg[resolve(p)]
			h.Registered = &b
		}
		rep.Husks = append(rep.Husks, h)
	}
	return rep
}

// HuskFix is HuskFixResult.
type HuskFix struct {
	Removed, SkippedRegistered, SkippedAppearedValid, SkippedUnsafe []string
}

// ToObject is HuskFixResult.to_dict().
func (f *HuskFix) ToObject() *jsonx.Object {
	l := func(x []string) []any {
		out := []any{}
		for _, s := range x {
			out = append(out, s)
		}
		return out
	}
	return jsonx.NewObject("removed", l(f.Removed), "skipped_registered", l(f.SkippedRegistered), "skipped_appeared_valid", l(f.SkippedAppearedValid), "skipped_unsafe", l(f.SkippedUnsafe))
}

func hasLocalState(dir string) bool {
	found := false
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && p != dir && !info.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// FixWorkspaceHusks is fix_workspace_husks; a registration error is returned
// as an error string (WorkspaceHuskRegistrationError).
func FixWorkspaceHusks(root string) (*HuskReport, *HuskFix, *string) {
	rep := ScanWorkspaceHusks(root)
	if rep.RegistrationError != nil {
		return rep, nil, rep.RegistrationError
	}
	fix := &HuskFix{}
	for _, h := range rep.Husks {
		if h.Registered != nil && *h.Registered {
			fix.SkippedRegistered = append(fix.SkippedRegistered, h.Path)
			continue
		}
		p := filepath.Join(root, h.Path)
		if exists(filepath.Join(p, ".git")) {
			fix.SkippedAppearedValid = append(fix.SkippedAppearedValid, h.Path)
			continue
		}
		if hasLocalState(p) {
			fix.SkippedUnsafe = append(fix.SkippedUnsafe, h.Path)
			continue
		}
		os.RemoveAll(p)
		fix.Removed = append(fix.Removed, h.Path)
	}
	return rep, fix, nil
}

// RefreshStaleCoordWorktrees fast-forwards registered `*-coord` worktrees to
// their branch tip; returns (path, outcome) pairs.
func RefreshStaleCoordWorktrees(root string) [][2]string {
	wd := filepath.Join(root, project.WorktreesDir)
	reg, regErr := RegisteredWorktrees(root)
	if !isDir(wd) || regErr != nil {
		return nil
	}
	var out [][2]string
	for _, n := range sortedChildren(wd) {
		p := filepath.Join(wd, n)
		if !isDir(p) || !strings.HasSuffix(n, "-coord") || !reg[resolve(p)] {
			continue
		}
		branch, err := gitx.Run(p, "symbolic-ref", "--short", "HEAD")
		if err != nil || branch == "" {
			out = append(out, [2]string{p, "skip_detached"})
			continue
		}
		head, _ := gitx.Run(p, "rev-parse", "HEAD")
		tip, _ := gitx.Run(root, "rev-parse", "refs/heads/"+branch)
		if head == "" || tip == "" || head == tip || !gitx.OK(root, "merge-base", "--is-ancestor", head, tip) {
			out = append(out, [2]string{p, "already_current"})
			continue
		}
		if gitx.OK(p, "merge", "--ff-only", "refs/heads/"+branch) {
			out = append(out, [2]string{p, "refreshed"})
		} else {
			out = append(out, [2]string{p, "failed"})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Sparse checkout (git/sparse_checkout.py scan_repo)
// ---------------------------------------------------------------------------

// SparseState is SparseCheckoutState.
type SparseState struct {
	Path               string
	ConfigEnabled      bool
	PatternFile        string
	PatternFilePresent bool
	PatternLines       int
	Managed            bool
}

// Blocking is is_blocking.
func (s SparseState) Blocking() bool { return s.ConfigEnabled && !s.Managed }

// SparseReport is SparseCheckoutScanReport.
type SparseReport struct {
	Primary   SparseState
	Worktrees []SparseState
}

// AnyBlocking reports any blocking state.
func (r *SparseReport) AnyBlocking() bool {
	if r.Primary.Blocking() {
		return true
	}
	for _, w := range r.Worktrees {
		if w.Blocking() {
			return true
		}
	}
	return false
}

func patternLines(p string) []string {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func scanSparsePath(p string, worktree bool) SparseState {
	st := SparseState{Path: p}
	if v, err := gitx.Run(p, "config", "--get", "core.sparseCheckout"); err == nil && strings.ToLower(strings.TrimSpace(v)) == "true" {
		st.ConfigEnabled = true
	}
	if !worktree {
		st.PatternFile = filepath.Join(p, ".git", "info", "sparse-checkout")
	} else if gd, err := gitx.Run(p, "rev-parse", "--git-dir"); err == nil && gd != "" {
		if !filepath.IsAbs(gd) {
			gd = resolve(filepath.Join(p, gd))
		}
		st.PatternFile = filepath.Join(gd, "info", "sparse-checkout")
	}
	if st.PatternFile != "" && exists(st.PatternFile) {
		st.PatternFilePresent = true
		st.PatternLines = len(patternLines(st.PatternFile))
	}
	return st
}

var laneSuffix = regexp.MustCompile(`^lane-[a-z0-9]+$`)

// ScanSparse is scan_repo.
func ScanSparse(root string) *SparseReport {
	rep := &SparseReport{Primary: scanSparsePath(root, false)}
	type policy struct{ slug, dir string }
	var policies []policy
	for _, n := range sortedChildren(project.Specs(root)) {
		meta, err := mission.ReadMeta(project.Specs(root, n))
		if err != nil || meta == nil {
			continue
		}
		cb := meta.GetString("coordination_branch")
		slug := meta.GetString("mission_slug")
		if slug == "" {
			slug = meta.GetString("slug")
		}
		mid := meta.GetString("mission_id")
		if cb == "" || slug == "" || len(mid) < 8 {
			continue
		}
		policies = append(policies, policy{slug, mission.CoordMissionDirName(slug, mid[:8])})
	}
	reg, _ := RegisteredWorktrees(root)
	wd := filepath.Join(root, project.WorktreesDir)
	for _, n := range sortedChildren(wd) {
		p := filepath.Join(wd, n)
		if !isDir(p) || !exists(filepath.Join(p, ".git")) {
			continue
		}
		st := scanSparsePath(p, true)
		if st.ConfigEnabled {
			var cands []policy
			var laneID string
			for _, pol := range policies {
				if strings.HasPrefix(n, pol.slug+"-") && laneSuffix.MatchString(strings.TrimPrefix(n, pol.slug+"-")) {
					cands = append(cands, pol)
					laneID = strings.TrimPrefix(n, pol.slug+"-")
				}
			}
			if len(cands) == 1 && laneID != lanes.PlanningLaneID && st.PatternFilePresent && reg[resolve(p)] &&
				gitx.CurrentBranch(p) == lanes.LaneBranchName(cands[0].slug, laneID, "") {
				have := map[string]bool{}
				for _, l := range patternLines(st.PatternFile) {
					have[l] = true
				}
				d := cands[0].dir
				if have["/*"] && have["!kitty-specs/"+d+"/status.events.jsonl"] && have["!kitty-specs/"+d+"/status.json"] {
					st.Managed = true
				}
			}
		}
		rep.Worktrees = append(rep.Worktrees, st)
	}
	return rep
}
