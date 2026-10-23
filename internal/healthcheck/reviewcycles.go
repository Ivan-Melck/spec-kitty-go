package healthcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/placement"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Stranded review-cycle classes (doctor review-cycle-reconcile).
const (
	DeletedCoordBranchClass = "deleted_coord_branch_absorption"
	LiveCoordPreADRClass    = "live_coord_pre_adr_primary_record"
)

// StrandedFinding is StrandedRecordFinding.
type StrandedFinding struct {
	MissionSlug, WPID, RetiredResolver, RetiringFR, StrandedClass, ResolvedDirectory string
	RecordPaths                                                                      []string
}

// ReconcileReport is MissionReconciliationReport.
type ReconcileReport struct {
	MissionSlug   string
	StrandedClass string
	Findings      []StrandedFinding
}

// ToObject renders the JSON row.
func (r ReconcileReport) ToObject() *jsonx.Object {
	fs := []any{}
	for _, f := range r.Findings {
		rp := []any{}
		for _, p := range f.RecordPaths {
			rp = append(rp, p)
		}
		fs = append(fs, jsonx.NewObject("mission_slug", f.MissionSlug, "wp_id", f.WPID, "retired_resolver", f.RetiredResolver,
			"retiring_fr", f.RetiringFR, "stranded_class", f.StrandedClass, "resolved_directory", f.ResolvedDirectory, "record_paths", rp))
	}
	return jsonx.NewObject("mission_slug", r.MissionSlug, "stranded_class", r.StrandedClass, "clean", len(r.Findings) == 0, "findings", fs)
}

type shape struct {
	name, fr string
	dirs     func(primary, wpID, wpSlug string) []string
}

var retiredShapes = []shape{
	{"post_merge/review_artifact_consistency.py::_artifact_dirs_for_wp", "FR-007", func(p, id, _ string) []string {
		tasks := filepath.Join(p, "tasks")
		var out []string
		exact := filepath.Join(tasks, id)
		if isDir(exact) {
			out = append(out, exact)
		}
		for _, n := range sortedChildren(tasks) {
			c := filepath.Join(tasks, n)
			if isDir(c) && len(n) > len(id) && n[:len(id)+1] == id+"-" {
				out = append(out, c)
			}
		}
		return out
	}},
	{"review/cycle.py::_review_cycle_wp_dir", "FR-003", func(p, _, slug string) []string {
		c := filepath.Join(p, "tasks", slug)
		if isDir(c) {
			return []string{c}
		}
		return nil
	}},
}

func init() {
	bare := func(p, id, _ string) []string {
		c := filepath.Join(p, "tasks", id)
		if isDir(c) {
			return []string{c}
		}
		return nil
	}
	for _, n := range []string{"review/arbiter.py::_find_review_cycle_artifact", "review/arbiter.py::persist_arbiter_decision", "review/arbiter.py::get_arbiter_overrides_for_wp"} {
		retiredShapes = append(retiredShapes, shape{n, "FR-009", bare})
	}
}

var wpIDPrefix = regexp.MustCompile(`^(WP\d+)`)

func recordsAt(dir string) []string {
	a, _ := filepath.Glob(filepath.Join(dir, "review-cycle-*.md"))
	sort.Strings(a)
	b, _ := filepath.Glob(filepath.Join(dir, "arbiter-override-*.json"))
	sort.Strings(b)
	return append(a, b...)
}

// resolveReviewCycleDir is _resolve_canonical_review_cycle_dir:
// placement_seam(...).read_dir(REVIEW_CYCLE), absorbing
// CoordinationBranchDeleted / StatusReadPathNotFound to PRIMARY.
func resolveReviewCycleDir(root, slug, primary string) (string, string) {
	resolved, err := placement.ReadDir(root, slug, placement.Coord)
	if err != nil {
		return primary, DeletedCoordBranchClass
	}
	if samePath(resolved, primary) {
		return resolved, ""
	}
	return resolved, LiveCoordPreADRClass
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

func hasMerged(dir string) bool {
	m := mission.ReadMetaOrEmpty(dir)
	v, _ := m.Get("merged_at")
	s, _ := v.(string)
	return s != ""
}

// ReviewCycleReconcile is _collect_reports over dirs (mission dir names).
func ReviewCycleReconcile(root string, names []string) []ReconcileReport {
	var out []ReconcileReport
	for _, n := range names {
		if meta, err := mission.ReadMeta(project.Specs(root, n)); err != nil || meta == nil {
			continue
		}
		// _primary_feature_dir: read_dir(WORK_PACKAGE_TASK)
		dir, err := placement.PrimaryDir(root, n)
		if err != nil {
			dir = project.Specs(root, n)
		}
		resolved, class := resolveReviewCycleDir(root, n, dir)
		if class == "" {
			continue
		}
		rep := ReconcileReport{MissionSlug: n, StrandedClass: class}
		tasks := filepath.Join(dir, "tasks")
		for _, c := range sortedChildren(tasks) {
			if !isDir(filepath.Join(tasks, c)) {
				continue
			}
			m := wpIDPrefix.FindStringSubmatch(c)
			if m == nil {
				continue
			}
			for _, sh := range retiredShapes {
				for _, cd := range sh.dirs(dir, m[1], c) {
					recs := recordsAt(cd)
					if len(recs) == 0 {
						continue
					}
					rep.Findings = append(rep.Findings, StrandedFinding{MissionSlug: n, WPID: m[1], RetiredResolver: sh.name, RetiringFR: sh.fr,
						StrandedClass: class, ResolvedDirectory: resolved, RecordPaths: recs})
				}
			}
		}
		out = append(out, rep)
	}
	return out
}

var _ = os.Stat
