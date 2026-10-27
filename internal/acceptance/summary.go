package acceptance

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/wp"
)

// LaneOrder is task_utils.LANES.
var LaneOrder = []string{"planned", "claimed", "in_progress", "for_review", "in_review", "approved", "done", "blocked", "canceled"}

var legacyNotDoneLanes = []string{"planned", "claimed", "doing", "in_progress", "for_review"}

var laneBlockerHints = []struct{ lane, hint string }{
	{"in_review", "review is still in progress; complete the review and move the work package to approved or done"},
	{"blocked", "work package is blocked; resolve the blocker and move the work package to approved or done"},
	{"canceled", "work package is canceled; operator-authored cancellation provenance required — reopen or replace it, then move the work package to approved or done"},
}

// Error is AcceptanceError (exit 1, message rendered as-is).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

// Check is an AcceptanceCheckDiagnostic.
type Check struct{ Check, Detail string }

func checksJSON(cs []Check) []any {
	out := []any{}
	for _, c := range cs {
		out = append(out, jsonx.NewObject("check", c.Check, "detail", c.Detail))
	}
	return out
}

// WPState is WorkPackageState.
type WPState struct {
	ID, Lane, Title, Path string
	LatestLane            any // string or nil
	Metadata              *jsonx.Object
	OperatorProvenance    bool
}

// Summary is AcceptanceSummary.
type Summary struct {
	Feature, RepoRoot, FeatureDir, TasksDir string
	Branch                                  string // "" = detached (null)
	WorktreeRoot, PrimaryRepoRoot           string
	Lanes                                   map[string][]string
	WorkPackages                            []WPState
	MetadataIssues, ActivityIssues          []string
	UncheckedTasks, NeedsClarification      []string
	MissingArtifacts, OptionalMissing       []string
	GitDirty, PathViolations, Warnings      []string
	Skipped, Blocked                        []Check
	RecommendedFixOrder                     []string
	CanceledWPs                             []*jsonx.Object
	MatrixDir                               string
	MatrixSkipReason                        string
}

// PlanningArtifactOnlySkip is PLANNING_ARTIFACT_ONLY_SKIP_REASON.
const PlanningArtifactOnlySkip = "planning_artifact_only"

func acceptableEnding(lane string, prov bool) bool {
	return lane == "approved" || lane == "done" || (lane == "canceled" && prov)
}

func (s *Summary) provenance() map[string]bool {
	m := map[string]bool{}
	for _, w := range s.WorkPackages {
		m[w.ID] = w.OperatorProvenance
	}
	return m
}

func (s *Summary) laneKeys() []string {
	keys := append([]string{}, LaneOrder...)
	for k := range s.Lanes {
		known := false
		for _, l := range LaneOrder {
			if l == k {
				known = true
			}
		}
		if !known {
			keys = append(keys, k)
		}
	}
	return keys
}

// AllDone is AcceptanceSummary.all_done.
func (s *Summary) AllDone() bool {
	prov := s.provenance()
	saw, delivered := false, false
	for _, lane := range s.laneKeys() {
		for _, id := range s.Lanes[lane] {
			saw = true
			if !acceptableEnding(lane, prov[id]) {
				return false
			}
			if acceptableEnding(lane, false) {
				delivered = true
			}
		}
	}
	return !saw || delivered
}

// OK is AcceptanceSummary.ok.
func (s *Summary) OK() bool {
	return s.AllDone() && len(s.MetadataIssues) == 0 && len(s.ActivityIssues) == 0 && len(s.UncheckedTasks) == 0 &&
		len(s.NeedsClarification) == 0 && len(s.MissingArtifacts) == 0 && len(s.GitDirty) == 0 && len(s.PathViolations) == 0
}

type Bucket struct {
	Key    string
	Values []string
}

// Outstanding is AcceptanceSummary.outstanding (ordered, non-empty only).
func (s *Summary) Outstanding() []Bucket {
	prov := s.provenance()
	var notDone []string
	for _, l := range legacyNotDoneLanes {
		notDone = append(notDone, s.Lanes[l]...)
	}
	var blockers []string
	for _, h := range laneBlockerHints {
		for _, id := range s.Lanes[h.lane] {
			if h.lane == "canceled" && prov[id] {
				continue
			}
			blockers = append(blockers, fmt.Sprintf("%s: canonical lane is '%s'; %s.", id, h.lane, h.hint))
		}
	}
	var nothing []string
	saw, delivered, blocked := false, false, false
	for _, lane := range s.laneKeys() {
		for _, id := range s.Lanes[lane] {
			saw = true
			if !acceptableEnding(lane, prov[id]) {
				blocked = true
			}
			if acceptableEnding(lane, false) {
				delivered = true
			}
		}
	}
	if !blocked && saw && !delivered {
		nothing = []string{"mission delivered nothing: every work package is canceled; none reached approved or done"}
	}
	all := []Bucket{{"not_done", notDone}, {"lane_blockers", blockers}, {"delivered_nothing", nothing},
		{"metadata", s.MetadataIssues}, {"activity", s.ActivityIssues}, {"unchecked_tasks", s.UncheckedTasks},
		{"needs_clarification", s.NeedsClarification}, {"missing_artifacts", s.MissingArtifacts},
		{"git_dirty", s.GitDirty}, {"path_violations", s.PathViolations}}
	var out []Bucket
	for _, b := range all {
		if len(b.Values) > 0 {
			out = append(out, b)
		}
	}
	return out
}

// FailedChecks is AcceptanceSummary.failed_checks.
func (s *Summary) FailedChecks() []Check {
	var out []Check
	for _, b := range s.Outstanding() {
		for _, v := range b.Values {
			out = append(out, Check{b.Key, v})
		}
	}
	return out
}

func strList(xs []string) []any {
	out := []any{}
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// LanesJSON renders the lane buckets in LANES order.
func (s *Summary) LanesJSON() *jsonx.Object {
	o := &jsonx.Object{}
	for _, l := range s.laneKeys() {
		if v, ok := s.Lanes[l]; ok {
			o.Set(l, strList(v))
		}
	}
	return o
}

// ToObject is AcceptanceSummary.to_dict.
func (s *Summary) ToObject() *jsonx.Object {
	meta := mission.ReadMetaOrEmpty(s.FeatureDir)
	slug := meta.GetString("mission_slug")
	if slug == "" {
		slug = meta.GetString("slug")
	}
	if slug == "" || !mission.SafePathSegment(slug) {
		slug = filepath.Base(s.FeatureDir)
	}
	var number any
	if n, ok := mission.MissionNumber(meta); ok {
		number = n
	}
	wps := []any{}
	for _, w := range s.WorkPackages {
		wps = append(wps, jsonx.NewObject("id", w.ID, "lane", w.Lane, "title", w.Title, "path", w.Path,
			"latest_lane", w.LatestLane, "has_lane_entry", w.LatestLane != nil, "metadata", w.Metadata))
	}
	canceled := []any{}
	for _, c := range s.CanceledWPs {
		canceled = append(canceled, c)
	}
	return jsonx.NewObject(
		"mission_slug", slug, "mission_number", number, "mission_type", mission.MissionType(meta),
		"branch", nullStr(s.Branch), "repo_root", s.RepoRoot, "feature_dir", s.FeatureDir, "tasks_dir", s.TasksDir,
		"worktree_root", s.WorktreeRoot, "primary_repo_root", s.PrimaryRepoRoot, "lanes", s.LanesJSON(),
		"work_packages", wps, "metadata_issues", strList(s.MetadataIssues), "activity_issues", strList(s.ActivityIssues),
		"unchecked_tasks", strList(s.UncheckedTasks), "needs_clarification", strList(s.NeedsClarification),
		"missing_artifacts", strList(s.MissingArtifacts), "optional_missing", strList(s.OptionalMissing),
		"git_dirty", strList(s.GitDirty), "path_violations", strList(s.PathViolations), "warnings", strList(s.Warnings),
		"failed_checks", checksJSON(s.FailedChecks()), "skipped_checks", checksJSON(s.Skipped),
		"blocked_checks", checksJSON(s.Blocked), "recommended_fix_order", strList(s.RecommendedFixOrder),
		"canceled_wps", canceled, "all_done", s.AllDone(), "ok", s.OK())
}

// LaneDerivations is acceptance_lane_derivations (accepted/approved/done/merge_pending).
func (s *Summary) LaneDerivations() (accepted, approved, done, pending []string) {
	approved = append([]string{}, s.Lanes["approved"]...)
	done = append([]string{}, s.Lanes["done"]...)
	accepted = append(append([]string{}, approved...), done...)
	return accepted, approved, done, approved
}

// AddLaneDerivations appends the lane derivation keys to payload.
func (s *Summary) AddLaneDerivations(o *jsonx.Object) {
	a, ap, d, p := s.LaneDerivations()
	o.Set("accepted_wps", strList(a))
	o.Set("approved_wps", strList(ap))
	o.Set("done_wps", strList(d))
	o.Set("merge_pending_wps", strList(p))
}

// ---------------------------------------------------------------------------
// Collection
// ---------------------------------------------------------------------------

// StatusEntry is one `git status --porcelain=v1 -z` record.
type StatusEntry struct {
	XY, Path, Orig string
	IsDir          bool
}

// Display is StatusEntry.display().
func (e StatusEntry) Display() string {
	shown := e.Path
	if e.IsDir {
		shown += "/"
	}
	if e.Orig != "" {
		return fmt.Sprintf("%s %s -> %s", e.XY, e.Orig, shown)
	}
	return e.XY + " " + shown
}

// StatusEntries runs git status --porcelain=v1 -z in dir.
func StatusEntries(dir string) ([]StatusEntry, error) {
	r, err := gitx.Exec(dir, nil, nil, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, err
	}
	recs := bytes.Split([]byte(r.Stdout), []byte{0})
	var out []StatusEntry
	for i := 0; i < len(recs); i++ {
		rec := string(recs[i])
		if len(rec) < 4 {
			continue
		}
		e := StatusEntry{XY: rec[:2], Path: rec[3:]}
		if strings.HasSuffix(e.Path, "/") {
			e.IsDir = true
			e.Path = strings.TrimSuffix(e.Path, "/")
		}
		if strings.ContainsAny(e.XY, "RC") && i+1 < len(recs) {
			i++
			e.Orig = strings.TrimSuffix(string(recs[i]), "/")
		}
		out = append(out, e)
	}
	return out, nil
}

var (
	reOpRecord    = regexp.MustCompile(`(?:^|/)kitty-ops/[0-9A-HJKMNP-TV-Z]{26}\.jsonl$`)
	reStateAudit  = regexp.MustCompile(`(?:^|/)\.kittify/mission-state-audit(?:/|$)`)
	reMissionMeta = regexp.MustCompile(`(?:^|/)kitty-specs/[^/]+/meta\.json$`)
	reLegacyMeta  = regexp.MustCompile(`(?:^|/)\.kittify/meta\.json$`)
	reEncProv     = regexp.MustCompile(`\.kittify/encoding-provenance/global\.jsonl$`)
	reDerivedView = regexp.MustCompile(`(?:^|/)\.kittify/derived/[^/]+/[^/]+$`)
	reDossier     = regexp.MustCompile(`(?:^|/)\.kittify/dossiers/.+/snapshot-latest\.json$`)
)

// IsSelfBookkeeping is coherence.is_self_bookkeeping_churn.
func IsSelfBookkeeping(p string) bool {
	n := strings.TrimRight(filepath.ToSlash(p), "/")
	return reMissionMeta.MatchString(n) || reLegacyMeta.MatchString(n) || reOpRecord.MatchString(n) ||
		reStateAudit.MatchString(n) || reEncProv.MatchString(n) || reDerivedView.MatchString(n) || reDossier.MatchString(n)
}

// isAcceptOwnWrite is _is_accept_pipeline_own_write.
func isAcceptOwnWrite(p, slug string) bool {
	if !strings.HasPrefix(filepath.ToSlash(p), "kitty-specs/"+slug+"/") && !strings.Contains(filepath.ToSlash(p), "/kitty-specs/"+slug+"/") {
		return false
	}
	k := mission.ArtifactKindForPath(p)
	return k == mission.KindAcceptanceMatrix || (k == mission.KindStatusState && filepath.Base(p) == "status.json")
}

func isMissionLedger(e StatusEntry, slug string) bool {
	n := filepath.ToSlash(e.Path)
	prefix := "kitty-specs/" + slug + "/decisions"
	return n == prefix || strings.HasPrefix(n, prefix+"/")
}

// IsCoordResidueFor is is_coord_residue_churn scoped to the mission.
func IsCoordResidueFor(p, slug string) bool {
	n := filepath.ToSlash(p)
	if !strings.HasPrefix(n, "kitty-specs/"+slug+"/") {
		return false
	}
	return mission.IsCoordResidue(n)
}

// DirtyGate is _accept_dirty_gate.
func DirtyGate(entries []StatusEntry, slug string, routed bool) []string {
	var out []string
	for _, e := range entries {
		p := e.Path
		if isAcceptOwnWrite(p, slug) || IsSelfBookkeeping(p) || isMissionLedger(e, slug) {
			continue
		}
		if strings.HasPrefix(p, "kitty-specs/"+slug+"/") && strings.HasSuffix(p, ".bak") {
			continue
		}
		if routed && IsCoordResidueFor(p, slug) {
			continue
		}
		out = append(out, e.Display())
	}
	return out
}

func gitOut(dir string, args ...string) string {
	r, err := gitx.Exec(dir, nil, nil, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

var reUnchecked = regexp.MustCompile(`^-\s*\[\s*\]\s*(T\d{3,})\b`)

// UncheckedSubtaskRows is iter_unchecked_subtask_rows.
func UncheckedSubtaskRows(text string) []string {
	var out []string
	fence := false
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if reUnchecked.MatchString(s) {
			out = append(out, s)
		}
	}
	return out
}

func readStrict(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(b), bom), nil
}

func clarificationIDs(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		ms := strings.Index(line, "[NEEDS CLARIFICATION:")
		if ms < 0 {
			continue
		}
		me := strings.Index(line[ms:], "]")
		if me < 0 {
			continue
		}
		me += ms
		cs := strings.Index(line[me:], "<!--")
		if cs < 0 {
			continue
		}
		cs += me
		ce := strings.Index(line[cs+4:], "-->")
		if ce < 0 {
			continue
		}
		body := line[cs+4 : cs+4+ce]
		di := strings.Index(body, "decision_id:")
		if di < 0 {
			continue
		}
		if f := strings.Fields(body[di+len("decision_id:"):]); len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}

func needsClarification(files []string) []string {
	var out []string
	for _, f := range files {
		text, err := readStrict(f)
		if err != nil {
			continue
		}
		ids := clarificationIDs(text)
		if len(ids) == 0 {
			continue
		}
		statusByID := map[string]string{}
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(f), "decisions", "index.json")); err == nil {
			if o, err := jsonx.DecodeObject(b); err == nil {
				for _, e := range o.GetList("entries") {
					if eo, ok := e.(*jsonx.Object); ok {
						statusByID[eo.GetString("decision_id")] = eo.GetString("status")
					}
				}
			}
		}
		for _, id := range ids {
			st, ok := statusByID[id]
			if !ok || st == "open" || st == "deferred" {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// MissionConfig is the subset of mission.yaml the acceptance gate reads.
type MissionConfig struct {
	Name     string
	Domain   string
	Required []string
	Optional []string
	Paths    [][2]string // ordered key -> path
}

// LoadMissionConfig resolves <type>/mission.yaml through the project,
// global and packaged tiers.
func LoadMissionConfig(root, missionType string) *MissionConfig {
	if missionType == "" {
		missionType = "software-dev"
	}
	cands := []string{
		filepath.Join(root, ".kittify", "overrides", "missions", missionType, "mission.yaml"),
		filepath.Join(root, ".kittify", "missions", missionType, "mission.yaml"),
		filepath.Join(project.GlobalKittify(), "missions", missionType, "mission.yaml"),
	}
	var data []byte
	for _, c := range cands {
		if b, err := os.ReadFile(c); err == nil {
			data = b
			break
		}
	}
	if data == nil {
		b, err := readAsset("specify_cli/missions/" + missionType + "/mission.yaml")
		if err != nil {
			return nil
		}
		data = b
	}
	o, err := parseYAMLObject(data)
	if err != nil || o == nil {
		return nil
	}
	mc := &MissionConfig{Name: o.GetString("name"), Domain: o.GetString("domain")}
	if a := o.GetObject("artifacts"); a != nil {
		mc.Required = anyStrings(a.GetList("required"))
		mc.Optional = anyStrings(a.GetList("optional"))
	}
	if p := o.GetObject("paths"); p != nil {
		for _, kv := range p.Pairs() {
			if s, ok := kv.Value.(string); ok {
				mc.Paths = append(mc.Paths, [2]string{kv.Key, s})
			}
		}
	}
	return mc
}

func anyStrings(l []any) []string {
	var out []string
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func normToken(t string) string { return strings.Trim(strings.TrimSpace(t), "/") }

func suggestDirs(missing []string) []string {
	var out, dirs []string
	for _, p := range missing {
		base := filepath.Base(p)
		switch {
		case strings.HasSuffix(p, "/"):
			out = append(out, "mkdir -p "+p)
			dirs = append(dirs, p)
		case strings.Contains(base, "."):
			if parent := filepath.Dir(p); parent != "." && parent != "" {
				out = append(out, fmt.Sprintf("mkdir -p %s && touch %s", parent, p))
			} else {
				out = append(out, "touch "+p)
			}
		default:
			out = append(out, "mkdir -p "+p)
		}
	}
	if len(dirs) > 1 {
		out = append([]string{"Create directories in one go: mkdir -p " + strings.Join(dirs, " ")}, out...)
	}
	return out
}

// pathConventions is evaluate_path_conventions (no contracts waiver / no
// research prefix / no project overrides).
func pathConventions(mc *MissionConfig, root, planningDir string, strict bool, candidateRoots []string) ([]string, string, map[string]bool) {
	if mc == nil || len(mc.Paths) == 0 {
		return nil, "", nil
	}
	artifactTokens := map[string]bool{}
	for _, t := range append(append([]string{}, mc.Required...), mc.Optional...) {
		artifactTokens[normToken(t)] = true
	}
	var missing, warns, missingArtifacts []string
	for _, kv := range mc.Paths {
		key, rel := kv[0], kv[1]
		full := filepath.Join(root, rel)
		tagged := false
		if filepath.IsAbs(rel) {
			full = rel
		} else if artifactTokens[normToken(rel)] {
			full = filepath.Join(planningDir, rel)
			tagged = true
		}
		if _, err := os.Stat(full); err == nil {
			continue
		}
		if !tagged && !filepath.IsAbs(rel) {
			found := false
			for _, c := range candidateRoots {
				if _, err := os.Stat(filepath.Join(c, rel)); err == nil {
					found = true
					break
				}
			}
			if found {
				continue
			}
		}
		resolved := relTo(root, full)
		if strings.HasSuffix(rel, "/") && !strings.HasSuffix(resolved, "/") {
			resolved += "/"
		}
		missing = append(missing, resolved)
		warns = append(warns, fmt.Sprintf("%s expects %s path: %s (not found)", mc.Name, key, resolved))
		if tagged {
			missingArtifacts = append(missingArtifacts, normToken(rel))
		}
	}
	if len(missing) == 0 {
		return nil, "", nil
	}
	sugg := suggestDirs(missing)
	if strict {
		lines := []string{"Path Convention Errors:"}
		for _, w := range warns {
			lines = append(lines, "  - "+w)
		}
		lines = append(lines, "")
		if len(sugg) > 0 {
			lines = append(lines, "Run `accept --lenient` to treat these as warnings instead of blocking errors for this mission run - or, if you want to adopt the convention, see the commands below:",
				"", "Required Actions:")
			for _, s := range sugg {
				lines = append(lines, "  - "+s)
			}
		} else {
			lines = append(lines, "Run `accept --lenient` to treat these as warnings instead of blocking errors for this mission run.")
		}
		dedup := map[string]bool{}
		for _, t := range missingArtifacts {
			if artifactTokens[t] {
				dedup[t] = true
			}
		}
		return []string{strings.Join(lines, "\n")}, "", dedup
	}
	lines := []string{"Path Convention Warnings:"}
	for _, w := range warns {
		lines = append(lines, "  - "+w)
	}
	if len(sugg) > 0 {
		lines = append(lines, "", "Suggestions:")
		for _, s := range sugg {
			lines = append(lines, "  - "+s)
		}
	}
	return nil, strings.Join(lines, "\n"), nil
}

// approvedLaneRoots is _approved_lane_source_roots.
func approvedLaneRoots(root, featureDir string, laneBuckets map[string][]string) []string {
	accepted := map[string]bool{}
	for _, id := range append(append([]string{}, laneBuckets["approved"]...), laneBuckets["done"]...) {
		accepted[id] = true
	}
	if len(accepted) == 0 {
		return nil
	}
	m, err := lanes.Read(featureDir)
	if err != nil || m == nil {
		return nil
	}
	var out []string
	for _, l := range m.Lanes {
		if l.LaneID == lanes.PlanningLaneID || len(l.WPIDs) == 0 {
			continue
		}
		all := true
		for _, id := range l.WPIDs {
			if !accepted[id] {
				all = false
			}
		}
		if !all {
			continue
		}
		p := lanes.WorktreePath(root, m.MissionSlug, l.LaneID)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// CollectOptions configures Collect.
type CollectOptions struct {
	StrictMetadata bool
	MutateMatrix   bool
}

func appendSkippedLaneChecks(s *Summary, reason string, includePresence bool) {
	checks := [][2]string{
		{"acceptance_matrix_presence", "Acceptance matrix presence check"},
		{"acceptance_matrix_evidence", "Acceptance matrix evidence validation"},
		{"negative_invariants", "Negative invariant execution"},
		{"acceptance_matrix_verdict", "Acceptance matrix verdict evaluation"},
	}
	if !includePresence {
		checks = checks[1:]
	}
	for _, c := range checks {
		s.Skipped = append(s.Skipped, Check{c[0], c[1] + " skipped: " + reason})
	}
}

// Collect is collect_feature_summary for mission directory slug.
func Collect(root, slug string, opts CollectOptions) (*Summary, error) {
	sf := coord.Resolve(root, slug)
	featureDir := sf.PrimaryDir
	if st, err := os.Stat(featureDir); err != nil || !st.IsDir() {
		return nil, &Error{"Mission directory not found: " + featureDir}
	}
	s := &Summary{Feature: slug, RepoRoot: root, FeatureDir: featureDir, TasksDir: filepath.Join(featureDir, "tasks"),
		Lanes: map[string][]string{}}
	if b := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD"); b != "" && b != "HEAD" {
		s.Branch = b
	}
	s.WorktreeRoot = root
	if t := gitOut(root, "rev-parse", "--show-toplevel"); t != "" {
		s.WorktreeRoot = realpath(t)
	}
	s.PrimaryRepoRoot = root
	if c := gitOut(root, "rev-parse", "--git-common-dir"); c != "" {
		if !filepath.IsAbs(c) {
			c = filepath.Join(root, c)
		}
		s.PrimaryRepoRoot = filepath.Dir(realpath(c))
	}
	entries, err := StatusEntries(root)
	if err != nil {
		return nil, &Error{"Cannot verify a clean working tree: git status failed: " + err.Error()}
	}
	statusDir := sf.StatusDir()
	s.GitDirty = DirtyGate(entries, slug, sf.Routed())
	for _, l := range LaneOrder {
		s.Lanes[l] = []string{}
	}

	// A non-WP workflow is vouched for by its real completed runtime and
	// declared artifacts, never by fabricated tasks, lanes or WP state: only
	// the WP-specific gathering varies, everything below is shared.
	nonWP, err := collectNonWPFacts(root, featureDir, statusDir)
	if err != nil {
		return nil, err
	}
	allAcceptable := false
	if nonWP == nil {
		if allAcceptable, err = gatherWorkPackages(s, root, slug, featureDir, statusDir, opts); err != nil {
			return nil, err
		}
	} else {
		s.ActivityIssues = append(s.ActivityIssues, nonWP.activity...)
	}

	planning := featureDir
	tasksFile := filepath.Join(planning, "tasks.md")
	var unchecked []string
	var clarificationFiles []string
	if nonWP == nil {
		if err := strictArtifactCheck(tasksFile); err != nil {
			return nil, err
		}
		if text, err := readStrict(tasksFile); err != nil {
			unchecked = []string{"tasks.md missing"}
		} else {
			unchecked = UncheckedSubtaskRows(text)
		}
		clarificationFiles = []string{
			filepath.Join(planning, "spec.md"), filepath.Join(planning, "plan.md"), filepath.Join(planning, "quickstart.md"),
			tasksFile, filepath.Join(planning, "research.md"), filepath.Join(planning, "data-model.md")}
	} else {
		for _, name := range nonWP.required {
			clarificationFiles = append(clarificationFiles, filepath.Join(planning, name))
		}
	}
	for _, f := range clarificationFiles {
		if err := strictArtifactCheck(f); err != nil {
			return nil, err
		}
	}
	s.NeedsClarification = needsClarification(clarificationFiles)

	meta := mission.ReadMetaOrEmpty(featureDir)
	mc := LoadMissionConfig(root, mission.MissionType(meta))
	if nonWP == nil {
		for _, f := range []string{"spec.md", "plan.md", "tasks.md"} {
			if _, err := os.Stat(filepath.Join(planning, f)); err != nil {
				s.MissingArtifacts = append(s.MissingArtifacts, f)
			}
		}
	} else {
		s.MissingArtifacts = nonWP.missing
	}
	optional := []string{"quickstart.md", "data-model.md", "research.md", "contracts"}
	if mc != nil && mc.Optional != nil {
		optional = mc.Optional
	}
	for _, t := range optional {
		if _, err := os.Stat(filepath.Join(planning, t)); err != nil {
			s.OptionalMissing = append(s.OptionalMissing, strings.TrimRight(t, "/"))
		}
	}
	violations, pcWarning, dedup := pathConventions(mc, root, planning, opts.StrictMetadata, approvedLaneRoots(root, featureDir, s.Lanes))
	if len(dedup) > 0 {
		var kept []string
		for _, o := range s.OptionalMissing {
			if !dedup[normToken(o)] {
				kept = append(kept, o)
			}
		}
		s.OptionalMissing = kept
	}
	s.PathViolations = violations
	if len(s.OptionalMissing) > 0 {
		s.Warnings = append(s.Warnings, "Optional artifacts missing: "+strings.Join(s.OptionalMissing, ", "))
	}
	if len(violations) > 0 {
		s.Warnings = append(s.Warnings, "Path conventions not satisfied.")
	} else if pcWarning != "" {
		s.Warnings = append(s.Warnings, pcWarning)
	}

	if nonWP == nil {
		checkLaneGates(s, sf, opts.MutateMatrix)
	} else {
		// No code lanes, so no acceptance matrix is ever produced.
		s.MatrixSkipReason = PlanningArtifactOnlySkip
	}

	if !(len(unchecked) == 1 && unchecked[0] == "tasks.md missing") && !allAcceptable {
		s.UncheckedTasks = unchecked
	}
	s.RecommendedFixOrder = fixOrder(s)
	return s, nil
}

// gatherWorkPackages is _gather_work_packages: the WP-specific state of a
// WP-driven mission (lanes, per-WP state, canceled WPs, readiness); it
// reports whether every tracked WP has an acceptable ending.
func gatherWorkPackages(s *Summary, root, slug, featureDir, statusDir string, opts CollectOptions) (bool, error) {
	missingMsg := fmt.Sprintf("No canonical state found for mission '%s'. Cannot validate acceptance without status.events.jsonl. Run 'spec-kitty agent mission finalize-tasks --mission %s' to bootstrap the event log.", slug, slug)
	var snapWPs *jsonx.Object
	if _, err := os.Stat(status.EventsPath(statusDir)); err != nil {
		s.ActivityIssues = append(s.ActivityIssues, missingMsg)
	} else {
		snap, err := status.ReduceDir(statusDir)
		if err != nil {
			return false, &Error{fmt.Sprintf("Status event log is corrupted for mission '%s': %v", slug, err)}
		}
		snapWPs = snap.WorkPackages
		if snapWPs == nil || snapWPs.Len() == 0 {
			s.ActivityIssues = append(s.ActivityIssues, missingMsg)
		}
	}

	tasksDir := filepath.Join(featureDir, "tasks")
	if st, err := os.Stat(tasksDir); err != nil || !st.IsDir() {
		return false, &Error{fmt.Sprintf("Feature '%s' has no tasks directory at %s.", slug, tasksDir)}
	}
	files, _ := filepath.Glob(filepath.Join(tasksDir, "*.md"))
	sort.Strings(files)
	var expected []string
	for _, f := range files {
		if strings.ToLower(filepath.Base(f)) == "readme.md" {
			continue
		}
		if err := strictArtifactCheck(f); err != nil {
			return false, err
		}
		w, err := wp.Read(f)
		if err != nil {
			return false, &Error{err.Error()}
		}
		id := strings.TrimSpace(w.Meta().GetString("work_package_id"))
		if id == "" {
			id = w.Stem()
		}
		expected = append(expected, id)
		var snap *jsonx.Object
		if snapWPs != nil {
			snap = snapWPs.GetObject(id)
		}
		st, issues := buildWPState(w, id, snap, root, opts.StrictMetadata)
		if _, ok := s.Lanes[st.Lane]; ok {
			s.Lanes[st.Lane] = append(s.Lanes[st.Lane], id)
		} else {
			s.Lanes["planned"] = append(s.Lanes["planned"], id)
		}
		s.MetadataIssues = append(s.MetadataIssues, issues...)
		s.WorkPackages = append(s.WorkPackages, st)
		if st.OperatorProvenance {
			s.CanceledWPs = append(s.CanceledWPs, jsonx.NewObject("wp_id", id, "reason", snap.GetString("cancellation_reason"),
				"actor", snap.GetString("actor"), "at", snap.GetString("last_transition_at")))
		}
	}
	if snapWPs != nil && snapWPs.Len() > 0 {
		for _, id := range expected {
			ws := snapWPs.GetObject(id)
			if ws == nil {
				s.ActivityIssues = append(s.ActivityIssues, id+": no canonical state found in status.events.jsonl")
				continue
			}
			lane := ws.GetString("lane")
			if !acceptableEnding(lane, ws.GetString("reason_source") == "operator") {
				s.ActivityIssues = append(s.ActivityIssues, fmt.Sprintf("%s: canonical lane is '%s', expected 'approved' or 'done'", id, lane))
			}
		}
	}
	allAcceptable := len(s.WorkPackages) > 0
	for _, w := range s.WorkPackages {
		if !acceptableEnding(w.Lane, w.OperatorProvenance) {
			allAcceptable = false
		}
	}

	return allAcceptable, nil
}

func buildWPState(w *wp.WP, id string, snap *jsonx.Object, root string, strict bool) (WPState, []string) {
	title := strings.Trim(w.Title(), `"`)
	field := func(k, fallback string) any {
		if snap == nil {
			return nil
		}
		if v := scalar(snap, k); v != "" {
			return v
		}
		if fallback != "" {
			if v := scalar(snap, fallback); v != "" {
				return v
			}
		}
		return nil
	}
	var canonical any
	bucket := "planned"
	if snap != nil {
		if v, ok := snap.Get("lane"); ok && v != nil {
			canonical = fmt.Sprint(v)
			bucket = fmt.Sprint(v)
		}
	}
	md := jsonx.NewObject("lane", canonical, "agent", field("agent", "implementer_of_record"),
		"assignee", field("assignee", ""), "shell_pid", field("shell_pid", ""))
	var issues []string
	if strict {
		if v, _ := md.Get("agent"); v == nil {
			issues = append(issues, fmt.Sprintf("%s: missing agent in canonical runtime state (never claimed; run `spec-kitty agent tasks move-task %s --to %s --agent <name>` to repair)", id, id, bucket))
		}
		if bucket == "doing" || bucket == "in_progress" || bucket == "for_review" {
			if canonical != nil {
				if v, _ := md.Get("assignee"); v == nil {
					issues = append(issues, id+": missing assignee in canonical runtime state")
				}
				if v, _ := md.Get("shell_pid"); v == nil {
					issues = append(issues, id+": missing shell_pid in canonical runtime state")
				}
			}
		}
	}
	return WPState{ID: id, Lane: bucket, Title: title, Path: relTo(root, w.Path), LatestLane: canonical, Metadata: md,
		OperatorProvenance: snap != nil && snap.GetString("reason_source") == "operator"}, issues
}

// scalar renders a snapshot slot like str(value) for truthy scalars.
func scalar(o *jsonx.Object, k string) string {
	v, ok := o.Get(k)
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if !x {
			return ""
		}
		return "True"
	case float64:
		if x == 0 {
			return ""
		}
		return jsonx.FormatFloat(x)
	case int:
		if x == 0 {
			return ""
		}
		return fmt.Sprint(x)
	}
	return jsonx.Dumps(v, compact)
}

func hasBlocked(s *Summary, check string) bool {
	for _, b := range s.Blocked {
		if b.Check == check {
			return true
		}
	}
	return false
}

func anyContains(xs []string, needle string) bool {
	for _, x := range xs {
		if strings.Contains(x, needle) {
			return true
		}
	}
	return false
}

func fixOrder(s *Summary) []string {
	nonTerminal := false
	for l, ids := range s.Lanes {
		if len(ids) > 0 && !acceptableEnding(l, false) {
			nonTerminal = true
		}
	}
	checks := []struct {
		on  bool
		msg string
	}{
		{len(s.GitDirty) > 0, "Commit, stash, or discard working tree changes before acceptance."},
		{hasBlocked(s, "mission_branch"), "Switch to the mission branch or configured target branch named in the branch failure."},
		{len(s.MissingArtifacts) > 0, "Restore required mission artifacts before acceptance."},
		{len(s.MetadataIssues) > 0, "Fix work-package metadata issues."},
		{nonTerminal, "Move all work packages to approved or done."},
		{len(s.UncheckedTasks) > 0, "Complete unchecked items in tasks.md."},
		{len(s.NeedsClarification) > 0, "Resolve open NEEDS CLARIFICATION markers."},
		{hasBlocked(s, "acceptance_matrix"), "Create or restore kitty-specs/<mission>/acceptance-matrix.json."},
		{hasBlocked(s, "lanes_manifest"), "Restore or regenerate kitty-specs/<mission>/lanes.json."},
		{anyContains(s.ActivityIssues, "Evidence:"), "Fill missing acceptance matrix evidence fields."},
		{anyContains(s.ActivityIssues, "Acceptance matrix verdict is"), "Resolve pending or failing acceptance matrix criteria and negative invariants."},
		{anyContains(s.ActivityIssues, "Workflow run evidence required"), "Add successful GitHub Actions run evidence for workflow changes."},
		{len(s.PathViolations) > 0, "Fix mission path convention violations."},
	}
	out := []string{}
	for _, c := range checks {
		if c.on {
			out = append(out, c.msg)
		}
	}
	return out
}

// missionHasCode is lanes.compute.mission_has_code.
func missionHasCode(m *lanes.Manifest, featureDir string) bool {
	for _, l := range m.Lanes {
		if l.LaneID != lanes.PlanningLaneID {
			return true
		}
	}
	for _, l := range m.Lanes {
		for _, id := range l.WPIDs {
			if p, err := wp.Find(featureDir, id); err == nil {
				if w, err := wp.Read(p); err == nil && w.Meta().GetString("execution_mode") == "code_change" {
					return true
				}
			}
		}
	}
	return false
}

func checkLaneGates(s *Summary, sf *coord.Surface, mutate bool) {
	featureDir := s.FeatureDir
	m, err := lanes.Read(featureDir)
	stop := func(msg, reason string) {
		s.ActivityIssues = append(s.ActivityIssues, msg)
		s.Blocked = append(s.Blocked, Check{"lanes_manifest", msg})
		appendSkippedLaneChecks(s, reason, true)
	}
	if err != nil {
		stop(fmt.Sprintf("Corrupt lanes.json at %s: %v", filepath.Join(featureDir, lanes.File), err), "lanes.json is corrupt or malformed")
		return
	}
	if m == nil {
		stop(fmt.Sprintf("lanes.json is required for %s. Run 'spec-kitty agent mission finalize-tasks' to compute execution lanes, or if the mission has already begun executing, run 'spec-kitty doctor mission-state --fix --mission <slug>' to rebuild lanes.json from the event log.", featureDir), "lanes.json is missing")
		return
	}
	metaTarget := strings.TrimSpace(sf.Meta.GetString("target_branch"))
	if metaTarget != "" && metaTarget != m.TargetBranch {
		msg := fmt.Sprintf("Acceptance target branch mismatch: meta.json targets %s, lanes.json targets %s", metaTarget, m.TargetBranch)
		s.ActivityIssues = append(s.ActivityIssues, msg)
		s.Blocked = append(s.Blocked, Check{"mission_branch", msg})
		appendSkippedLaneChecks(s, "meta.json target_branch does not match lanes.json target_branch", true)
		return
	}
	hasCode := missionHasCode(m, featureDir)
	allowed := map[string]bool{m.TargetBranch: true}
	if hasCode {
		allowed[m.MissionBranch] = true
	}
	if s.Branch == "" || !allowed[s.Branch] {
		var names []string
		for b := range allowed {
			if b != "" {
				names = append(names, b)
			}
		}
		sort.Strings(names)
		cur := s.Branch
		if cur == "" {
			cur = "detached HEAD"
		}
		msg := fmt.Sprintf("Acceptance must run on mission or target branch (%s), not %s", strings.Join(names, ", "), cur)
		s.ActivityIssues = append(s.ActivityIssues, msg)
		s.Blocked = append(s.Blocked, Check{"mission_branch", msg})
		appendSkippedLaneChecks(s, "current branch is neither mission branch nor target branch", true)
		return
	}
	if !hasCode {
		appendSkippedLaneChecks(s, "planning_artifact-only missions do not produce acceptance-matrix.json", true)
		s.MatrixSkipReason = PlanningArtifactOnlySkip
		return
	}
	ref := s.Branch
	if ref == "" {
		ref = metaTarget
	}
	if ref == "" {
		ref = "HEAD"
	}
	matrixDir := featureDir
	kind := "primary"
	if sf.Routed() {
		if st, err := os.Stat(sf.CoordWorktree); err != nil || !st.IsDir() {
			if gitx.BranchExists(sf.Root, sf.CoordBranch) {
				detail := fmt.Sprintf("Acceptance matrix cannot be evaluated (surface_cannot_hold_fact): coordination worktree is not materialized (COORDINATION_WORKTREE_UNMATERIALIZED): coordination branch %s exists but its worktree %s is not materialized [surface=coord ref=%s]", sf.CoordBranch, sf.CoordWorktree, ref)
				s.ActivityIssues = append(s.ActivityIssues, detail)
				s.Blocked = append(s.Blocked, Check{"acceptance_matrix_cannot_evaluate", detail})
				appendSkippedLaneChecks(s, "surface_cannot_hold_fact", false)
				return
			}
		} else if st, err := os.Stat(sf.CoordDir); err == nil && st.IsDir() {
			matrixDir, kind = sf.CoordDir, "coord"
		} else {
			detail := fmt.Sprintf("Acceptance matrix cannot be evaluated (surface_cannot_hold_fact): Kind's declared home is COORD but the resolved surface is stamped PRIMARY (the coordination surface is not materialised yet); a substituted surface cannot hold this fact — refusing rather than judging an empty surface (GEC-5 / C2). [surface=primary ref=%s]", ref)
			s.ActivityIssues = append(s.ActivityIssues, detail)
			s.Blocked = append(s.Blocked, Check{"acceptance_matrix_cannot_evaluate", detail})
			appendSkippedLaneChecks(s, "surface_cannot_hold_fact", false)
			return
		}
	}
	mx, err := ReadMatrix(matrixDir)
	if err != nil {
		s.ActivityIssues = append(s.ActivityIssues, err.Error())
		return
	}
	if mx == nil {
		msg := "Acceptance matrix (acceptance-matrix.json) is required for lane-based features but was not found. This file is normally scaffolded automatically. If it is missing, regenerate it: spec-kitty agent mission finalize-tasks --mission " + filepath.Base(featureDir)
		s.ActivityIssues = append(s.ActivityIssues, msg)
		s.Blocked = append(s.Blocked, Check{"acceptance_matrix", msg})
		appendSkippedLaneChecks(s, "acceptance-matrix.json is missing", false)
		return
	}
	if !mutate {
		if len(mx.NegativeInvariants) > 0 {
			s.Skipped = append(s.Skipped, Check{"negative_invariants", "Negative invariant execution skipped: diagnose mode is read-only"})
		}
		judgeMatrix(s, mx)
		s.MatrixDir = matrixDir
		return
	}
	snapshot := mx.Clone()
	mx.Criteria = PopulateFromReview(matrixDir, mx.Criteria)
	if len(mx.NegativeInvariants) > 0 {
		mx.NegativeInvariants = EnforceNegativeInvariants(s.RepoRoot, mx.NegativeInvariants, &GateContext{SurfaceKind: kind, Ref: ref})
	}
	fresh, err := ReadMatrix(matrixDir)
	if err != nil || fresh == nil {
		fresh = &Matrix{MissionSlug: filepath.Base(featureDir)}
	}
	SpliceOwnedRows(fresh, snapshot, mx)
	if _, err := WriteMatrix(matrixDir, fresh); err != nil {
		s.ActivityIssues = append(s.ActivityIssues, err.Error())
		return
	}
	judgeMatrix(s, fresh)
	s.MatrixDir = matrixDir
}

func judgeMatrix(s *Summary, m *Matrix) {
	for _, e := range ValidateEvidence(m) {
		s.ActivityIssues = append(s.ActivityIssues, "Evidence: "+e)
	}
	switch m.OverallVerdict() {
	case "fail":
		s.ActivityIssues = append(s.ActivityIssues, "Acceptance matrix verdict is 'fail' — negative invariants or criteria not satisfied")
	case "pending":
		s.ActivityIssues = append(s.ActivityIssues, "Acceptance matrix verdict is 'pending' — criteria or invariants have not been verified")
	case VerdictPassPendingConsolidation:
		s.Skipped = append(s.Skipped, Check{"negative_invariants_deferred", "One or more negative invariants are deferred to post-consolidation verification; acceptance is not blocked, but this loop does not verify them — the post-consolidation op (or PR CI) must, before the mission is done."})
	}
}

// PopulateFromReview is populate_criteria_from_review_evidence for
// code_review criteria.
func PopulateFromReview(statusDir string, criteria []*jsonx.Object) []*jsonx.Object {
	targets := map[string]bool{}
	for _, c := range criteria {
		if str(c, "pass_fail") == "pending" && str(c, "proof_type") == "code_review" && !isEmptyScaffold(c) {
			targets[str(c, "criterion_id")] = true
		}
	}
	if len(targets) == 0 {
		return criteria
	}
	snap, err := status.ReduceDir(statusDir)
	if err != nil || snap.WorkPackages == nil || snap.WorkPackages.Len() == 0 {
		return criteria
	}
	ids := snap.WorkPackages.Keys()
	sort.Strings(ids)
	var reviewers, refs []string
	seen := map[string]bool{}
	for _, id := range ids {
		st := snap.WorkPackages.GetObject(id)
		if l := st.GetString("lane"); l != "approved" && l != "done" {
			return criteria
		}
	}
	for _, id := range ids {
		rr := snap.WorkPackages.GetObject(id).GetObject("review_result")
		if rr == nil || rr.GetString("verdict") != "approved" {
			return criteria
		}
		if r := rr.GetString("reviewer"); !seen[r] {
			seen[r] = true
			reviewers = append(reviewers, r)
		}
		refs = append(refs, id+":"+rr.GetString("reference"))
	}
	now := nowISO()
	var out []*jsonx.Object
	for _, c := range criteria {
		if !targets[str(c, "criterion_id")] {
			out = append(out, c)
			continue
		}
		n := c.Clone()
		n.Set("pass_fail", "pass")
		n.Set("verified_by", strings.Join(reviewers, ", "))
		n.Set("verified_at", now)
		n.Set("evidence", strings.Join(refs, "; "))
		if notes := str(c, "notes"); notes != "" {
			n.Set("notes", notes+" "+autoDerivedNote)
		} else {
			n.Set("notes", autoDerivedNote)
		}
		out = append(out, n)
	}
	return out
}

var bom = string([]byte{0xEF, 0xBB, 0xBF})
