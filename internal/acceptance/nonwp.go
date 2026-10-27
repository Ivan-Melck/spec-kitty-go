package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// artifactOnlyMissionType is the one mission type whose acceptance is
// vouched for by its completed runtime and declared artifacts instead of
// work packages (non_wp_runtime._ARTIFACT_ONLY_MISSION_TYPE, #4983).
const artifactOnlyMissionType = "plan"

const runtimeUnreadableMsg = "Cannot read canonical mission runtime for acceptance; no state was changed"

// runtimeFacts is runtime_bridge_io.RuntimeAcceptanceFacts: read-only facts
// from a mission's existing run and its frozen workflow.
type runtimeFacts struct {
	family          string
	wpDriven        bool
	stepIDs         []string
	completed       []string
	issued, blocked string
	pendingDecision int
}

// nonWPFacts is non_wp_runtime.NonWpRuntimeFacts: the part of an acceptance
// summary a non-WP runtime supplies.
type nonWPFacts struct {
	activity, required, missing []string
}

// readRuntimeFacts is read_runtime_acceptance_facts: it never starts,
// advances or repairs a run. nil when the mission has no indexed run.
func readRuntimeFacts(root, slug, missionID string) (*runtimeFacts, error) {
	runDir, err := indexedRunDir(root, slug, missionID)
	if err != nil || runDir == "" {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(runDir, "state.json"))
	if err != nil {
		return nil, err
	}
	snap, err := jsonx.DecodeObject(b)
	if err != nil {
		return nil, err
	}
	bound := snap.GetString("mission_slug")
	if bound == "" {
		bound = snap.GetObject("inputs").GetString("mission_slug")
	}
	if bound != slug || snap.GetString("run_id") != filepath.Base(runDir) {
		return nil, errors.New("Runtime snapshot does not belong to the requested mission/run")
	}
	frozen, err := os.ReadFile(filepath.Join(runDir, "mission_template_frozen.yaml"))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(frozen)
	if hex.EncodeToString(sum[:]) != snap.GetString("template_hash") {
		return nil, errors.New("Frozen runtime template does not match its recorded hash")
	}
	tpl, err := yamlx.ParseObject(frozen)
	if err != nil || tpl == nil {
		return nil, fmt.Errorf("invalid frozen runtime template: %v", err)
	}
	key := snap.GetString("mission_key")
	if tpl.GetObject("mission").GetString("key") != key {
		return nil, errors.New("Frozen runtime type does not match its recorded run")
	}
	f := &runtimeFacts{family: key, issued: snap.GetString("issued_step_id"), blocked: snap.GetString("blocked_reason")}
	for _, it := range tpl.GetList("steps") {
		if so, ok := it.(*jsonx.Object); ok {
			f.stepIDs = append(f.stepIDs, so.GetString("id"))
		}
	}
	f.wpDriven = slices.Contains(f.stepIDs, "implement")
	for _, v := range snap.GetList("completed_steps") {
		if s, ok := v.(string); ok {
			f.completed = append(f.completed, s)
		}
	}
	if pd := snap.GetObject("pending_decisions"); pd != nil {
		f.pendingDecision = pd.Len()
	}
	return f, nil
}

// indexedRunDir is runtime_bridge_io._resolve_run_dir_for_mission: the run
// directory recorded in .kittify/runtime/feature-runs.json, resolved
// against root (a run_dir outside the repository is refused).
func indexedRunDir(root, slug, missionID string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "runtime", "feature-runs.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	idx, err := jsonx.DecodeObject(b)
	if err != nil {
		return "", err
	}
	key := "legacy-" + slug
	if missionID != "" {
		key = missionID
	}
	entry := idx.GetObject(key)
	if entry == nil && missionID != "" {
		for _, k := range idx.Keys() {
			if c := idx.GetObject(k); c != nil && c.GetString("mission_id") == "" && c.GetString("mission_slug") == slug {
				return "", fmt.Errorf("Legacy run %s for mission %s has no mission_id", c.GetString("run_id"), slug)
			}
		}
	}
	raw := entry.GetString("run_dir")
	if entry == nil || raw == "" {
		return "", nil
	}
	dir := filepath.FromSlash(raw)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	dir = filepath.Clean(dir)
	if rel, err := filepath.Rel(realpath(root), realpath(dir)); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("run_dir %s is outside the repository", raw)
	}
	return dir, nil
}

// blockingArtifacts is runtime.resolver.required_artifacts_for: the blocking
// path patterns required at step (required_always + required_by_step[step]).
func blockingArtifacts(root, family, step string) ([]string, error) {
	m, err := orchestrator.LoadExpectedArtifacts(root, family)
	if err != nil || m == nil {
		return nil, err
	}
	specs := append(m.GetList("required_always"), m.GetObject("required_by_step").GetList(step)...)
	var out []string
	for _, s := range specs {
		if o, ok := s.(*jsonx.Object); ok {
			if o.GetBool("blocking") {
				out = append(out, o.GetString("path_pattern"))
			}
		}
	}
	return out, nil
}

// collectNonWPFacts is non_wp_runtime.collect_non_wp_runtime_facts: runtime
// facts for an artifact-only (plan) mission, or nil for the WP path.
func collectNonWPFacts(root, featureDir, statusDir string) (*nonWPFacts, error) {
	// resolve_mission_identity: an absent meta.json selects the WP path; a
	// corrupt one raises MissionMetaReadError (*migrate.MetaReadError).
	meta, err := migrate.LoadMetaFailClosed(featureDir)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil
	}
	missionType := mission.MissionType(meta)
	if missionType != artifactOnlyMissionType {
		return nil, nil
	}
	facts, err := readRuntimeFacts(root, filepath.Base(featureDir), strings.TrimSpace(meta.GetString("mission_id")))
	if err != nil {
		return nil, &Error{runtimeUnreadableMsg}
	}
	if facts == nil || facts.wpDriven {
		return nil, nil
	}
	for _, home := range []string{featureDir, statusDir} {
		if m, _ := filepath.Glob(filepath.Join(home, "tasks", "WP*.md")); len(m) > 0 {
			return nil, nil
		}
	}
	if facts.family != missionType {
		return nil, &Error{"Mission type does not match its recorded runtime; acceptance refused"}
	}
	out := &nonWPFacts{}
	if !slices.Contains(facts.stepIDs, "review") {
		out.activity = append(out.activity, "Plan runtime has no review action; acceptance refused")
	}
	var incomplete []string
	for _, id := range facts.stepIDs {
		if !slices.Contains(facts.completed, id) && !slices.Contains(incomplete, id) {
			incomplete = append(incomplete, id)
		}
	}
	sort.Strings(incomplete)
	if len(facts.stepIDs) == 0 || len(incomplete) > 0 || facts.issued != "" || facts.blocked != "" || facts.pendingDecision > 0 {
		remaining := strings.Join(incomplete, ", ")
		if remaining == "" {
			remaining = "pending runtime decision/result"
		}
		out.activity = append(out.activity, "Non-WP runtime is not complete; remaining steps: "+remaining)
	}
	required, missing := map[string]bool{}, map[string]bool{}
	present := func(name string) bool {
		st, err := os.Stat(filepath.Join(featureDir, name))
		return err == nil && st.Mode().IsRegular()
	}
	const missingPrefix = "Required artifact missing: "
	for _, step := range facts.stepIDs {
		names, err := blockingArtifacts(root, facts.family, step)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			required[n] = true
		}
		for _, failure := range mission.PlanGuardFailures(step, present) {
			if name, ok := strings.CutPrefix(failure, missingPrefix); ok {
				missing[name] = true
			} else {
				out.activity = append(out.activity, step+": "+failure)
			}
		}
	}
	if len(required) == 0 {
		out.activity = append(out.activity, "Non-WP runtime has no declared blocking artifacts; acceptance refused")
	}
	out.required, out.missing = sortedKeys(required), sortedKeys(missing)
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
