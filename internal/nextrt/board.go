package nextrt

import (
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/dossier"
	"github.com/spec-kitty/go-kitty3/internal/missionstate"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/workflow"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// missionCtx is the resolved mission placement for one `next` call.
type missionCtx struct {
	Root, Slug    string
	Primary       string // task board (PRIMARY) dir
	StatusDir     string // status read dir (coordination dir when materialized)
	Meta          *jsonx.Object
	MissionType   string
	MissionID     string
	Surface       *coord.Surface
	MissionNumber string
}

func newMissionCtx(root, slug string) *missionCtx {
	s := coord.Resolve(root, slug)
	mt := mission.MissionType(s.Meta)
	if mt == "" {
		mt = "software-dev"
	}
	return &missionCtx{Root: root, Slug: slug, Primary: s.PrimaryDir, StatusDir: s.StatusDir(), Meta: s.Meta,
		MissionType: mt, MissionID: s.MissionID, Surface: s, MissionNumber: mission.MissionNumberString(nil, slug)}
}

var wpStemRe = regexp.MustCompile(`^(WP\d+)`)

func wpFiles(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "tasks", "WP*.md"))
	sort.Strings(m)
	return m
}

func wpIDOf(p string) string {
	stem := filepath.Base(p)
	stem = stem[:len(stem)-len(filepath.Ext(stem))]
	if m := wpStemRe.FindStringSubmatch(stem); m != nil {
		return m[1]
	}
	return stem
}

func snapshotOf(dir string) (*status.Snapshot, bool) {
	if _, err := os.Stat(status.EventsPath(dir)); err != nil {
		return status.Reduce(nil, nil, true), false
	}
	stream, err := status.ReadStream(dir, "")
	if err != nil {
		return status.Reduce(nil, nil, true), false
	}
	return status.Reduce(stream.Transitions, stream.Annotations, true), true
}

// computeProgress is decision._compute_wp_progress (nil without WP files).
func computeProgress(taskDir, statusDir string) *jsonx.Object {
	files := wpFiles(taskDir)
	if len(files) == 0 {
		return nil
	}
	snap, _ := snapshotOf(statusDir)
	c := map[string]int{}
	for _, f := range files {
		c["total"]++
		lane := status.Genesis
		if st := snap.WP(wpIDOf(f)); st != nil {
			lane = status.Lane(st.GetString("lane"))
		}
		if lane == status.Genesis || lane == status.Uninitialized || lane == "" {
			continue
		}
		switch {
		case lane == status.Done:
			c["done"]++
		case lane == status.Approved:
			c["approved"]++
		case status.ProgressBucket(lane) == "in_flight" && lane != status.Blocked:
			c["in_progress"]++
		case status.ProgressBucket(lane) == "review":
			c["for_review"]++
		case status.ProgressBucket(lane) == "not_started":
			c["planned"]++
		}
	}
	p := status.ComputeProgress(snap)
	return jsonx.NewObject("total_wps", c["total"], "done_wps", c["done"], "approved_wps", c["approved"],
		"in_progress_wps", c["in_progress"], "planned_wps", c["planned"], "for_review_wps", c["for_review"],
		"weighted_percentage", jsonx.Float(status.PyRound(p.Percentage, 1)))
}

// firstWPByLane is _find_first_wp_by_lane.
func firstWPByLane(taskDir, statusDir, lane string) string {
	snap, _ := snapshotOf(statusDir)
	target := status.ResolveLaneAlias(lane)
	for _, f := range wpFiles(taskDir) {
		id := wpIDOf(f)
		st := snap.WP(id)
		if st == nil {
			continue
		}
		l := status.ResolveLaneAlias(st.GetString("lane"))
		if l == string(status.Genesis) || l == string(status.Uninitialized) {
			continue
		}
		if l == target {
			return id
		}
	}
	return ""
}

func acceptable(st *jsonx.Object) (bool, bool) {
	if st == nil {
		return false, false
	}
	lane := st.GetString("lane")
	switch lane {
	case "approved":
		return true, false
	case "done":
		return true, true
	case "canceled":
		return st.GetString("reason_source") == "operator", false
	}
	return false, false
}

// boardStep is _finalized_task_board_override_step ("" = none).
func boardStep(m *missionCtx, progress *jsonx.Object) string {
	if progress == nil {
		return ""
	}
	if n, _ := progress.GetInt("total_wps"); n <= 0 {
		return ""
	}
	if st, err := os.Stat(filepath.Join(m.Primary, "tasks.md")); err != nil || st.IsDir() {
		return ""
	}
	if id, _ := workflow.PreviewClaimableWP(m.Root, m.Slug); id != "" {
		return "implement"
	}
	for _, l := range []string{"claimed", "in_progress"} {
		if firstWPByLane(m.Primary, m.StatusDir, l) != "" {
			return "implement"
		}
	}
	if firstWPByLane(m.Primary, m.StatusDir, "for_review") != "" {
		return "review"
	}
	if firstWPByLane(m.Primary, m.StatusDir, "in_review") != "" {
		return "blocked:review_in_progress"
	}
	snap, ok := snapshotOf(m.StatusDir)
	if !ok {
		return "blocked:no_actionable_wp"
	}
	total, _ := progress.GetInt("total_wps")
	done, acc := 0, 0
	for _, f := range wpFiles(m.Primary) {
		a, d := acceptable(snap.WP(wpIDOf(f)))
		if a {
			acc++
		}
		if d {
			done++
		}
	}
	if acc == total {
		if done == total {
			return "done"
		}
		return "accept"
	}
	return "blocked:no_actionable_wp"
}

// taskSurfaceError is _wp_task_surface_error.
func taskSurfaceError(m *missionCtx) string {
	snap, ok := snapshotOf(m.StatusDir)
	if !ok {
		return ""
	}
	ids := snap.WorkPackages.Keys()
	sort.Strings(ids)
	tasks := filepath.Join(m.Primary, "tasks")
	for _, id := range ids {
		if snap.Lane(id) == status.Canceled {
			continue
		}
		expected := filepath.Join(tasks, id+".md")
		recovery := fmt.Sprintf("Restore or regenerate the primary task for %s at %s (or a titled %s file under %s), then rerun `spec-kitty next --mission %s`. If %s was removed on purpose, move it to canceled instead: `spec-kitty agent tasks move-task %s --to canceled --mission %s`.",
			id, expected, id, tasks, m.Slug, id, id, m.Slug)
		if st, err := os.Stat(tasks); err != nil || !st.IsDir() {
			return fmt.Sprintf("Canonical WORK_PACKAGE_TASK directory for %s is missing at %s; expected %s. %s", id, tasks, expected, recovery)
		}
		var found string
		for _, f := range wpFiles(m.Primary) {
			stem := filepath.Base(f)
			stem = stem[:len(stem)-3]
			if stem == id || len(stem) > len(id) && stem[:len(id)+1] == id+"-" {
				found = f
				break
			}
		}
		if found == "" {
			return fmt.Sprintf("Canonical WORK_PACKAGE_TASK file for %s is missing at %s. %s", id, expected, recovery)
		}
		// read_authored_wp_frontmatter: FrontmatterManager.read first.
		if msg := missionstate.FrontmatterReadError(found); msg != "" {
			return fmt.Sprintf("Could not read canonical WORK_PACKAGE_TASK file %s: %s. %s", found, msg, recovery)
		}
		w, err := wp.Read(found)
		if err != nil {
			return fmt.Sprintf("Could not read canonical WORK_PACKAGE_TASK file %s: %v. %s", found, err, recovery)
		}
		// read_authored_wp_frontmatter validates against WPMetadata.
		if msg, _ := dossier.AuthoredWPValidationError(found); msg != "" {
			return fmt.Sprintf("Could not read canonical WORK_PACKAGE_TASK file %s: %s. %s", found, msg, recovery)
		}
		if decl := w.String("work_package_id"); decl != id {
			return fmt.Sprintf("Canonical WORK_PACKAGE_TASK file %s declares work_package_id %s; expected %s, found %s. %s", found, decl, id, decl, recovery)
		}
	}
	return ""
}

// terminalVerdict is committed_authority.mission_terminal_verdict.
func terminalVerdict(root, slug string) string {
	primary := project.Specs(root, slug)
	meta, err := mission.ReadMeta(primary)
	if err != nil || meta == nil {
		return "none"
	}
	if v, ok := meta.Get("mission_number"); !ok || v == nil {
		return "none"
	}
	snap, ok := snapshotOf(primary)
	if !ok || snap.WorkPackages.Len() == 0 {
		return "none"
	}
	for _, id := range snap.WorkPackages.Keys() {
		if a, _ := acceptable(snap.WP(id)); !a {
			return "blocked_conflict"
		}
	}
	return "terminal"
}
