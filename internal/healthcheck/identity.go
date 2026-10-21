package healthcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// IdentityState is status.identity_audit.IdentityState.
type IdentityState struct {
	Path, Slug    string
	MissionID     any // string (or other JSON value) / nil
	MissionNumber *int
	State         string // assigned | pending | legacy | orphan
	Error         *string
}

// ToObject is IdentityState.to_dict().
func (s IdentityState) ToObject() *jsonx.Object {
	var num any
	if s.MissionNumber != nil {
		num = *s.MissionNumber
	}
	var e any
	if s.Error != nil {
		e = *s.Error
	}
	return jsonx.NewObject("path", s.Path, "slug", s.Slug, "mission_id", s.MissionID, "mission_number", num, "state", s.State, "error", e)
}

var prefix3 = regexp.MustCompile(`^(\d{3})-`)

func coerceNumber(v any) *int {
	switch t := v.(type) {
	case nil, bool:
		return nil
	case int:
		return &t
	case string:
		s := strings.TrimSpace(t)
		if s == "" || s == "pending" || s == "unassigned" || s == "TBD" {
			return nil
		}
		s = strings.TrimLeft(s, "0")
		if s == "" {
			z := 0
			return &z
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil
		}
		return &n
	}
	return nil
}

// ClassifyMission is status.identity_audit.classify_mission.
func ClassifyMission(dir string) IdentityState {
	slug := filepath.Base(dir)
	st := IdentityState{Path: dir, Slug: slug, State: "orphan"}
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		e := "meta.json not found"
		st.Error = &e
		return st
	}
	meta, err := migrate.LoadMetaFailClosed(dir)
	if err != nil {
		e := err.Error()
		st.Error = &e
		return st
	}
	if meta == nil {
		meta = &jsonx.Object{}
	}
	if v, ok := meta.Get("mission_id"); ok && pyjson.Truthy(v) {
		st.MissionID = v
	}
	nv, _ := meta.Get("mission_number")
	st.MissionNumber = coerceNumber(nv)
	switch {
	case st.MissionID != nil && st.MissionNumber != nil:
		st.State = "assigned"
	case st.MissionID != nil:
		st.State = "pending"
	case st.MissionNumber != nil:
		st.State = "legacy"
	}
	return st
}

// AuditRepo is status.identity_audit.audit_repo.
func AuditRepo(root string) []IdentityState {
	var out []IdentityState
	for _, n := range migrate.MissionDirs(root) {
		out = append(out, ClassifyMission(project.Specs(root, n)))
	}
	return out
}

// DuplicatePrefixes is find_duplicate_prefixes (ordered by prefix first seen).
func DuplicatePrefixes(root string) ([]string, map[string][]IdentityState) {
	groups := map[string][]IdentityState{}
	var order []string
	for _, n := range migrate.MissionDirs(root) {
		m := prefix3.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		if _, ok := groups[m[1]]; !ok {
			order = append(order, m[1])
		}
		groups[m[1]] = append(groups[m[1]], ClassifyMission(project.Specs(root, n)))
	}
	var keep []string
	for _, p := range order {
		if len(groups[p]) >= 2 {
			keep = append(keep, p)
		}
	}
	return keep, groups
}

// AmbiguousSelectors is find_ambiguous_selectors (handles in first-seen order).
func AmbiguousSelectors(states []IdentityState) ([]string, map[string][]IdentityState) {
	m := map[string][]IdentityState{}
	var order []string
	for _, s := range states {
		handles := []string{s.Slug}
		if p := prefix3.FindStringSubmatch(s.Slug); p != nil {
			handles = append(handles, p[1])
		}
		if h := mission.StripNumericPrefix(s.Slug); h != s.Slug {
			handles = append(handles, h)
		}
		seen := map[string]bool{}
		for _, h := range handles {
			if seen[h] {
				continue
			}
			seen[h] = true
			if _, ok := m[h]; !ok {
				order = append(order, h)
			}
			m[h] = append(m[h], s)
		}
	}
	var keep []string
	for _, h := range order {
		if len(m[h]) >= 2 {
			keep = append(keep, h)
		}
	}
	return keep, m
}

// TopologyRow is one doctor topology row.
type TopologyRow struct {
	Slug                   string
	Topology, Flattened    any
	Error, Finding, Remedy *string
}

// ToObject renders the row.
func (r TopologyRow) ToObject() *jsonx.Object {
	opt := func(s *string) any {
		if s == nil {
			return nil
		}
		return *s
	}
	return jsonx.NewObject("slug", r.Slug, "topology", r.Topology, "flattened", r.Flattened, "error", opt(r.Error), "finding", opt(r.Finding), "remedy", opt(r.Remedy))
}

func sp(s string) *string { return &s }

// StoredTopologyRow is _read_stored_topology.
func StoredTopologyRow(dir string) TopologyRow {
	slug := filepath.Base(dir)
	meta, err := migrate.LoadMetaFailClosed(dir)
	if err != nil {
		return TopologyRow{Slug: slug, Error: sp("corrupt json: " + err.Error())}
	}
	if meta == nil {
		return TopologyRow{Slug: slug, Error: sp("meta.json not found")}
	}
	r := TopologyRow{Slug: slug}
	r.Topology, _ = meta.Get("topology")
	r.Flattened, _ = meta.Get("flattened")
	if r.Topology == "single_branch" {
		ids, present, lerr := migrate.LaneIDs(dir)
		switch {
		case lerr != nil:
			r.Finding = sp("LANES_MANIFEST_UNREADABLE")
		case present:
			code := false
			for _, id := range ids {
				if id != migrate.PlanningLaneID {
					code = true
				}
			}
			if code && !migrate.IsMissionCompleted(dir) {
				r.Finding = sp("SINGLE_BRANCH_CODE_LANES_UNMIGRATED")
			}
		}
	}
	if r.Finding != nil {
		switch *r.Finding {
		case "SINGLE_BRANCH_CODE_LANES_UNMIGRATED":
			r.Remedy = sp("spec-kitty migrate backfill-topology --restamp-single-branch (or `spec-kitty upgrade`, which runs migration m_4_0_0rc5_single_branch_code_lanes_restamp)")
		case "LANES_MANIFEST_UNREADABLE":
			r.Remedy = sp("restore lanes.json from git history: `git checkout <ref> -- kitty-specs/" + slug + "/lanes.json`; or delete it and rebuild: `spec-kitty agent mission finalize-tasks --mission " + slug + "` if no WP has started, `spec-kitty doctor mission-state --fix --mission " + slug + "` if execution has begun")
		}
	}
	return r
}

// PrimaryDirForHandle approximates placement_seam(root, handle).read_dir(PRIMARY_METADATA).
func PrimaryDirForHandle(root, handle string) string {
	if name, err := mission.ResolveHandle(root, handle); err == nil {
		return project.Specs(root, name)
	}
	return project.Specs(root, handle)
}

// TopologyRows is _collect_topology_rows.
func TopologyRows(root string, missionHandle *string) []TopologyRow {
	if !migrate.SpecsExist(root) {
		return nil
	}
	if missionHandle != nil {
		d := PrimaryDirForHandle(root, *missionHandle)
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return []TopologyRow{StoredTopologyRow(d)}
		}
		return nil
	}
	var out []TopologyRow
	for _, n := range migrate.MissionDirs(root) {
		out = append(out, StoredTopologyRow(project.Specs(root, n)))
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
