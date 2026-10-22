package status

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// Finding is one status doctor finding.
type Finding struct {
	Severity, Category string
	WPID               string // "" = None
	Message, Action    string
}

// ToObject renders the JSON finding row.
func (f Finding) ToObject() *jsonx.Object {
	var id any
	if f.WPID != "" {
		id = f.WPID
	}
	return jsonx.NewObject("severity", f.Severity, "category", f.Category, "wp_id", id, "message", f.Message, "recommended_action", f.Action)
}

var blankableSlots = []string{"shell_pid_created_at", "agent", "assignee", "role", "agent_profile", "agent_profile_version", "model", "provider"}

func snapshotForDoctor(dir string, stream *Stream) *jsonx.Object {
	if b, err := os.ReadFile(filepath.Join(dir, SnapshotFile)); err == nil {
		if o, err := jsonx.DecodeObject(b); err == nil {
			return o
		}
	}
	if len(stream.Transitions) > 0 {
		return Reduce(stream.Transitions, nil, true).ToObject()
	}
	return nil
}

// DoctorHooks supplies the checks that need packages above status: the
// issue-matrix check (check_issue_matrix) and the repo-level sparse-checkout
// scan (check_sparse_checkout). A nil hook falls back to the built-in
// primary-only sparse probe / no issue-matrix finding.
type DoctorHooks struct {
	IssueMatrix func() []Finding
	Sparse      func() []Finding
}

// RunDoctor is status.doctor.run_doctor (the optional hooks carry the
// issue-matrix and sparse-checkout checks).
func RunDoctor(dir, slug, root string, claimedDays, inProgressDays int, hooks ...DoctorHooks) ([]Finding, error) {
	var h DoctorHooks
	if len(hooks) > 0 {
		h = hooks[0]
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("Feature directory does not exist: %s", dir)
	}
	var findings, membership []Finding
	stream, err := ReadStream(dir, "")
	if err != nil {
		abs, _ := filepath.Abs(EventsPath(dir))
		findings = append(findings, Finding{"error", "event_log_error", "", fmt.Sprintf("Cannot read canonical status event log %s: %v", abs, err),
			"Preserve the log and snapshot as evidence. Restore trusted status history or correct file access, then rerun status doctor."})
	} else {
		snap := snapshotForDoctor(dir, stream)
		findings = append(findings, checkUninitialized(dir, snap)...)
		if snap != nil {
			findings = append(findings, checkStaleClaims(snap, claimedDays, inProgressDays)...)
			findings = append(findings, checkOrphanWorkspaces(root, slug, snap)...)
			if drift, err := ValidateMaterializationDrift(dir); err == nil {
				for _, d := range drift {
					findings = append(findings, Finding{"warning", "materialization_drift", "", d, "Run 'spec-kitty agent status materialize' to regenerate status.json from the canonical event log."})
				}
			}
			findings = append(findings, checkBlankedSlots(snap)...)
		}
		findings = append(findings, checkSelfApproval(dir)...)
		if len(stream.Transitions) > 0 {
			membership = checkWPFiles(dir, Reduce(stream.Transitions, nil, true))
		}
	}
	if h.IssueMatrix != nil {
		findings = append(findings, h.IssueMatrix()...)
	}
	if h.Sparse != nil {
		findings = append(findings, h.Sparse()...)
	} else {
		findings = append(findings, checkSparseCheckout(root)...)
	}
	findings = append(findings, checkDuplicateKeys(dir)...)
	return append(findings, membership...), nil
}

func checkUninitialized(dir string, snap *jsonx.Object) []Finding {
	if snap != nil && snap.GetObject("work_packages") != nil && snap.GetObject("work_packages").Len() > 0 {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "tasks", "WP*.md"))
	if len(files) == 0 {
		return nil
	}
	return []Finding{{"warning", "uninitialized_status", "", fmt.Sprintf("Mission has %d work package(s) defined but canonical status is not initialized (event log missing/empty).", len(files)),
		"Run `spec-kitty agent mission finalize-tasks` to bootstrap the event log."}}
}

func checkStaleClaims(snap *jsonx.Object, claimedDays, inProgressDays int) []Finding {
	var out []Finding
	wps := snap.GetObject("work_packages")
	if wps == nil {
		return nil
	}
	now := clock.Now().UTC()
	for _, kv := range wps.Pairs() {
		st, _ := kv.Value.(*jsonx.Object)
		if st == nil {
			continue
		}
		at := st.GetString("last_transition_at")
		if at == "" {
			continue
		}
		t, err := clock.ParseISO(at)
		if err != nil {
			continue
		}
		days := int(now.Sub(t.UTC()) / (24 * time.Hour))
		actor := "unknown"
		if v, ok := st.Get("actor"); ok {
			actor = PyStr(v)
		}
		lane := st.GetString("lane")
		if lane == string(Claimed) && days > claimedDays {
			out = append(out, Finding{"warning", "stale_claim", kv.Key, fmt.Sprintf("%s has been in 'claimed' for %d days (threshold: %d days). Actor: %s", kv.Key, days, claimedDays, actor),
				fmt.Sprintf("Either begin work on %s (move to in_progress) or release the claim (move back to planned).", kv.Key)})
		}
		if lane == string(InProgress) && days > inProgressDays {
			out = append(out, Finding{"warning", "stale_claim", kv.Key, fmt.Sprintf("%s has been in 'in_progress' for %d days (threshold: %d days). Actor: %s", kv.Key, days, inProgressDays, actor),
				fmt.Sprintf("Check if %s is blocked (move to blocked with reason) or complete the work (move to for_review).", kv.Key)})
		}
	}
	return out
}

var laneDirRe = regexp.MustCompile(`^(.+)-(lane-[a-z]+)$`)

func checkOrphanWorkspaces(root, slug string, snap *jsonx.Object) []Finding {
	wps := snap.GetObject("work_packages")
	if wps == nil || wps.Len() == 0 {
		return nil
	}
	for _, kv := range wps.Pairs() {
		st, _ := kv.Value.(*jsonx.Object)
		if st == nil || !IsTerminal(Lane(st.GetString("lane"))) {
			return nil
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, project.WorktreesDir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if m := laneDirRe.FindStringSubmatch(e.Name()); e.IsDir() && m != nil && m[1] == slug {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []Finding
	for _, n := range names {
		p := filepath.Join(root, project.WorktreesDir, n)
		out = append(out, Finding{"warning", "orphan_workspace", "", fmt.Sprintf("Worktree '%s' exists but all WPs in '%s' are terminal (canceled, done). Path: %s", n, slug, p),
			"Remove the orphan worktree: git worktree remove " + n})
	}
	return out
}

func checkBlankedSlots(snap *jsonx.Object) []Finding {
	var out []Finding
	wps := snap.GetObject("work_packages")
	if wps == nil {
		return nil
	}
	for _, kv := range wps.Pairs() {
		st, _ := kv.Value.(*jsonx.Object)
		if st == nil || IsTerminal(Lane(st.GetString("lane"))) {
			continue
		}
		for _, slot := range blankableSlots {
			v, ok := st.Get(slot)
			if s, isStr := v.(string); !ok || !isStr || s != "" {
				continue
			}
			if slot == "agent" && truthyAny(valueOr(st, "implementer_of_record", nil)) {
				continue
			}
			out = append(out, Finding{"error", "blanked_runtime_slot", kv.Key,
				fmt.Sprintf("%s runtime slot '%s' is an empty string — recorded attribution was blanked (corrupt canonical state, #2960).", kv.Key, slot),
				fmt.Sprintf("Run `spec-kitty doctor mission-state --fix --mission <slug>` to repair %s's '%s' slot, or drop the blanking annotation from the event log; the reducer treats '' as a no-op so a fresh non-empty annotation restores it.", kv.Key, slot)})
		}
	}
	return out
}

func checkSelfApproval(dir string) []Finding {
	rows, err := ReadRows(dir)
	if err != nil {
		return nil
	}
	var out []Finding
	for _, r := range rows {
		if r.GetString("event_type") != "ReviewerSelfApproval" {
			continue
		}
		p := r.GetObject("payload")
		if p == nil {
			p = &jsonx.Object{}
		}
		or := func(o *jsonx.Object, k, def string) string {
			if v, ok := o.Get(k); ok && truthyAny(v) {
				return PyStr(v)
			}
			return def
		}
		w := or(p, "wp_id", or(r, "aggregate_id", ""))
		label := w
		if label == "" {
			label = "A work package"
		}
		out = append(out, Finding{"warning", "review_independence", w,
			fmt.Sprintf("%s used self-review fallback: %s approved after intended reviewer %s failed (%s).", label, or(p, "implementing_actor", "unknown"), or(p, "intended_reviewer", "unknown"), or(p, "failure_reason", "reviewer_failed")),
			"Re-review with an independent reviewer before merge/release."})
	}
	return out
}

func checkWPFiles(dir string, snap *Snapshot) []Finding {
	var out []Finding
	files := map[string]string{}
	entries, _ := os.ReadDir(filepath.Join(dir, "tasks"))
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "WP") && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := wp.Read(filepath.Join(dir, "tasks", n))
		id := ""
		if err == nil {
			id = strings.TrimSpace(w.Doc.Meta.GetString("work_package_id"))
		}
		if err != nil || id == "" {
			out = append(out, Finding{"error", "wp_file_drift", "", fmt.Sprintf("Cannot read work-package identity from tasks/%s.", n), "Repair the prompt frontmatter, then rerun status doctor."})
			continue
		}
		if first, ok := files[id]; ok && first != n {
			out = append(out, Finding{"error", "wp_file_drift", id, fmt.Sprintf("tasks/%s and tasks/%s both declare %s.", first, n, id), "Keep one prompt per work package."})
			continue
		}
		files[id] = n
	}
	var evIDs []string
	for _, k := range snap.WorkPackages.Keys() {
		evIDs = append(evIDs, k)
	}
	sort.Strings(evIDs)
	for _, id := range evIDs {
		if _, ok := files[id]; ok {
			continue
		}
		sev := "error"
		if IsTerminal(snap.Lane(id)) {
			sev = "warning"
		}
		out = append(out, Finding{sev, "wp_file_drift", id, id + " exists in the event log but has no prompt in tasks/.", "Restore the work-package prompt or reconcile its event-log lifecycle."})
	}
	var fileIDs []string
	for id := range files {
		if snap.WP(id) == nil {
			fileIDs = append(fileIDs, id)
		}
	}
	sort.Strings(fileIDs)
	for _, id := range fileIDs {
		out = append(out, Finding{"error", "wp_file_drift", id, id + " has a prompt in tasks/ but no event-log status.", "Run `spec-kitty migrate backfill-wp-status` to seed missing status events."})
	}
	return out
}

func checkSparseCheckout(root string) []Finding {
	v, err := gitx.Run(root, "config", "--get", "core.sparseCheckout")
	if err != nil || strings.TrimSpace(strings.ToLower(v)) != "true" {
		return nil
	}
	return []Finding{{"warning", "sparse_checkout", "", "Legacy sparse-checkout state detected.\nPrimary: " + root + "\nWhy this matters: spec-kitty v3.0+ removed sparse-checkout support but did not ship a migration. This state can cause silent data loss during mission merge and broken lane worktrees on agent action implement. See spec-kitty/spec-kitty#588.",
		"Run 'spec-kitty doctor sparse-checkout --fix' to remove legacy sparse-checkout state from the primary repo and any lane worktrees. Affected paths: " + root}}
}

var topKeyRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):(.*)$`)

func checkDuplicateKeys(dir string) []Finding {
	var paths []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".md") {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	var out []Finding
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		if len(lines) == 0 || lines[0] != "---" {
			continue
		}
		seen := map[string][]int{}
		var order []string
		for i := 1; i < len(lines) && lines[i] != "---"; i++ {
			if m := topKeyRe.FindStringSubmatch(lines[i]); m != nil {
				if _, ok := seen[m[1]]; !ok {
					order = append(order, m[1])
				}
				seen[m[1]] = append(seen[m[1]], i+1)
			}
		}
		sort.Strings(order)
		for _, k := range order {
			if len(seen[k]) < 2 {
				continue
			}
			var nums []string
			for _, n := range seen[k] {
				nums = append(nums, fmt.Sprint(n))
			}
			out = append(out, Finding{"warning", "duplicate_frontmatter_key", "",
				fmt.Sprintf("%s has a duplicate frontmatter key '%s' (lines %s) — invalid YAML that fails closed at the frontmatter boundary and will trip an upgrade.", filepath.Base(p), k, strings.Join(nums, ", ")),
				"Run 'spec-kitty doctor mission-state --fix' to repair duplicate-key artifacts (keep-last-non-empty, batch-atomic)."})
		}
	}
	return out
}
