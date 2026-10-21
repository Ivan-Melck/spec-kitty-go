// Package missionstate is the mutating counterpart of the mission-state
// audit: `doctor mission-state --fix` (deterministic repair of historical
// meta.json / status.events.jsonl / status.json shapes plus the dual-key
// review_feedback heal) and `--teamspace-dry-run` (offline TeamSpace
// envelope synthesis and validation). Port of
// specify_cli/migration/mission_state.py and status/dup_key_repair.py.
package missionstate

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Constants shared by the repair and the dry-run.
const (
	MigrationSchemaVersion         = "1.0.0"
	CanonicalEnvelopeSchemaVersion = "3.0.0"
	EventsFile                     = "status.events.jsonl"
	StatusFile                     = "status.json"
	MetaFile                       = "meta.json"
	// AuditRoot is the git-tracked home of the per-run manifest and the
	// verbatim quarantine of removed rows.
	AuditRoot = ".kittify/mission-state-audit"

	LanesRebuiltAction                      = "lanes_rebuilt_from_event_log"
	StatusJSONDriftNotWrittenAction         = "status_json_drift_not_written"
	LanesRebuildSkippedNoWPFilesAction      = "lanes_rebuild_skipped_no_wp_files"
	LanesRebuildSkippedNoOwnedFilesAction   = "lanes_rebuild_skipped_no_owned_files"
	LanesRebuildSkippedTopologyUnmigrated   = "lanes_rebuild_skipped_topology_unmigrated"
	dupKeyManifestPrefix                    = "dup-key-"
	historicalRepairMarker                  = "historical-mission-state-repair"
	teamspaceRemediation                    = "Run `spec-kitty doctor mission-state --audit --fail-on teamspace-blocker`, then `--fix`, then `--teamspace-dry-run` before TeamSpace import/sync."
	repairDirtyMessage                      = "Refusing mission-state repair with dirty relevant paths. Commit/stash them first or pass --allow-dirty."
	epochTimestamp                          = "1970-01-01T00:00:00+00:00"
	defaultMissionType                      = "software-dev"
	defaultTargetBranch                     = "main"
	migrationActor                          = "migration"
	structuredActorLabel                    = "structured-actor"
	quarantinedNonStatusEvent               = "quarantined_non_status_event"
	preservedNonLaneEvent                   = "preserved_non_lane_event"
	duplicateEventIDDropped                 = "duplicate_event_id_dropped"
	teamspaceDryRunBuildID                  = "mission-state-dry-run"
	requiredEventsPackage                   = "5.0.0"
	eventsPackageVersion                    = "5.0.0"
	envelopeEventType                       = "WPStatusChanged"
	envelopeAggregateType                   = "WorkPackage"
	teamspaceProjectContextMissing          = "TEAMSPACE_PROJECT_CONTEXT_MISSING"
	teamspaceTeamContextNotValidated        = "TEAMSPACE_TEAM_CONTEXT_NOT_VALIDATED"
	outOfScopeForLaunchImport               = "out_of_scope_for_launch_import"
	missionStateAuditBlocker                = "MISSION_STATE_AUDIT_BLOCKER"
	statusEventsUnreadable                  = "STATUS_EVENTS_UNREADABLE"
	statusRowNotRepaired                    = "STATUS_ROW_NOT_REPAIRED"
	forbiddenLegacyKey                      = "FORBIDDEN_LEGACY_KEY"
	envelopeInvalid                         = "ENVELOPE_INVALID"
	payloadInvalid                          = "PAYLOAD_INVALID"
	skippedLocalSideLog                     = "skipped_local_side_log"
	skippedRuntimeSideLog                   = "skipped_runtime_side_log"
	typedSideLogMessage                     = "typed side-log row remains in status.events.jsonl; run --fix before dry-run"
	teamspaceTeamContextNotValidatedMessage = "Team/private TeamSpace membership is not checked by offline dry-run."
	teamspaceProjectContextMissingMessage   = "No persisted project.uuid was found; dry-run used a deterministic offline project_uuid for schema validation only."
)

var (
	policyTracked = []string{
		"kitty-specs/*/meta.json",
		"kitty-specs/*/status.events.jsonl",
		"kitty-specs/*/status.json",
		".kittify/mission-state-audit/*.json",
	}
	policyOptional       = []string{"kitty-specs/*/status.json"}
	policyIgnored        = []string{".git/", ".worktrees/", "__pycache__/", "*.pyc"}
	dupKeyPolicyTracked  = []string{"kitty-specs/**/*.md"}
	ulidRe               = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	uuidHyphenRe         = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	uuidBareRe           = regexp.MustCompile(`(?i)^[0-9a-f]{32}$`)
	missionPrefixRe      = regexp.MustCompile(`^(\d{3})-`)
	mid8Re               = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{8}$`)
	missionNumberRe      = regexp.MustCompile(`^(\d+)-`)
	actorSafeRe          = regexp.MustCompile(`[^a-z0-9_.:-]+`)
	forbiddenLegacyKeys  = []string{"feature_number", "feature_slug", "legacy_aggregate_id", "mission_key"}
	metaLegacyAliases    = []string{"feature_number", "feature_slug", "legacy_aggregate_id", "mission", "mission_key"}
	statusRowAliases     = [][2]string{{"feature_slug", "mission_slug"}, {"work_package_id", "wp_id"}}
	strCoercedKeys       = []string{"event_id", "mission_slug", "wp_id", "from_lane", "to_lane", "at", "execution_mode"}
	nullOmittedKeys      = map[string]bool{"reason_source": true, "review_result": true, "mission_id": true}
	validExecutionModes  = map[string]bool{"worktree": true, "direct_repo": true}
	requiredMetaFields   = []string{"created_at", "friendly_name", "mission_slug", "mission_type", "slug", "target_branch"}
	purposeFields        = []string{"purpose_tldr", "purpose_context"}
	legacyTypedLaneField = []string{"wp_id", "from_lane", "to_lane"}
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// DeterministicULID is deterministic_ulid: a 26-char Crockford id from the
// first 16 bytes of sha256(seed).
func DeterministicULID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	v := new(big.Int).SetBytes(sum[:16])
	mask := big.NewInt(31)
	out := make([]byte, 26)
	tmp := new(big.Int)
	for i := 25; i >= 0; i-- {
		out[i] = crockford[tmp.And(v, mask).Int64()]
		v.Rsh(v, 5)
	}
	return string(out)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func sha256Text(s string) string { return sha256Hex([]byte(s)) }

// fingerprint is _file_fingerprint: (sha256, size) or (nil, nil) when absent.
type fingerprint struct {
	sha  *string
	size *int
}

func (f fingerprint) equal(o fingerprint) bool {
	return eqStrPtr(f.sha, o.sha) && eqIntPtr(f.size, o.size)
}

func eqStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func fileFingerprint(path string) fingerprint {
	b, err := os.ReadFile(path)
	if err != nil {
		return fingerprint{}
	}
	s, n := sha256Hex(b), len(b)
	return fingerprint{&s, &n}
}

// FileChange is digest evidence for a file touched by the repair.
type FileChange struct {
	Path             string
	OldSHA, NewSHA   *string
	OldSize, NewSize *int
}

func optS(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func optI(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// ToObject is FileChange.to_dict.
func (c FileChange) ToObject() *jsonx.Object {
	return jsonx.NewObject("path", c.Path, "old_sha256", optS(c.OldSHA), "new_sha256", optS(c.NewSHA),
		"old_size", optI(c.OldSize), "new_size", optI(c.NewSize))
}

// RowTransformation is audit evidence for one rewritten/removed event row.
type RowTransformation struct {
	ArtifactPath string
	LineNumber   int
	EventID      *string
	Actions      []string
	OldSHA       string
	NewSHA       *string
}

// ToObject is RowTransformation.to_dict.
func (r RowTransformation) ToObject() *jsonx.Object {
	return jsonx.NewObject("artifact_path", r.ArtifactPath, "line_number", r.LineNumber, "event_id", optS(r.EventID),
		"actions", strAny(r.Actions), "old_sha256", r.OldSHA, "new_sha256", optS(r.NewSHA))
}

// MissionResult is MissionRepairResult.
type MissionResult struct {
	Slug              string
	MissionID         *string
	Status            string // updated | unchanged | error
	FileChanges       []FileChange
	RowTransforms     []RowTransformation
	QuarantinedRows   int
	ValidationErrors  []string
	MetaActions       []string
	QuarantineWritten bool
}

// ToObject is MissionRepairResult.to_dict.
func (m *MissionResult) ToObject() *jsonx.Object {
	fc := []any{}
	for _, c := range m.FileChanges {
		fc = append(fc, c.ToObject())
	}
	rt := []any{}
	for _, r := range m.RowTransforms {
		rt = append(rt, r.ToObject())
	}
	return jsonx.NewObject("mission_slug", m.Slug, "mission_id", optS(m.MissionID), "status", m.Status,
		"file_changes", fc, "row_transformations", rt, "quarantined_rows", m.QuarantinedRows,
		"validation_errors", strAny(m.ValidationErrors), "meta_actions", strAny(m.MetaActions),
		"quarantine_written", m.QuarantineWritten)
}

// RepairReport is the top-level repair manifest.
type RepairReport struct {
	RunID              string
	RepoHead           *string
	TargetMissions     []string
	ManifestPath       string
	Missions           []*MissionResult
	CLIVersion         string
	CommandArgs        []string
	GeneratedIDs       []string
	Policy             [][2]any // ordered {key, []string}
	QuarantineRootPath *string
	ResidueDirectories []string
}

// Count returns the number of missions with the given status.
func (r *RepairReport) Count(status string) int {
	n := 0
	for _, m := range r.Missions {
		if m.Status == status {
			n++
		}
	}
	return n
}

// QuarantinedRows sums quarantined_rows over all missions.
func (r *RepairReport) QuarantinedRows() int {
	n := 0
	for _, m := range r.Missions {
		n += m.QuarantinedRows
	}
	return n
}

// ToObject is RepairReport.to_dict.
func (r *RepairReport) ToObject() *jsonx.Object {
	pol := jsonx.NewObject()
	for _, kv := range r.Policy {
		pol.Set(kv[0].(string), strAny(kv[1].([]string)))
	}
	ms := []any{}
	for _, m := range r.Missions {
		ms = append(ms, m.ToObject())
	}
	return jsonx.NewObject(
		"schema_version", MigrationSchemaVersion,
		"run_id", r.RunID,
		"repo_head", optS(r.RepoHead),
		"target_missions", strAny(r.TargetMissions),
		"manifest_path", r.ManifestPath,
		"quarantine_root_path", optS(r.QuarantineRootPath),
		"cli_version", r.CLIVersion,
		"command_args", strAny(r.CommandArgs),
		"generated_ids", strAny(r.GeneratedIDs),
		"policy", pol,
		"summary", jsonx.NewObject(
			"missions_total", len(r.Missions),
			"missions_updated", r.Count("updated"),
			"missions_unchanged", r.Count("unchanged"),
			"missions_error", r.Count("error"),
			"quarantined_rows", r.QuarantinedRows(),
		),
		"residue_directories", strAny(r.ResidueDirectories),
		"missions", ms,
	)
}

// ToJSON is RepairReport.to_json: json.dumps(indent=2, sort_keys=True)+"\n".
func (r *RepairReport) ToJSON() string { return jsonx.Dumps(r.ToObject(), jsonx.SortedPretty) + "\n" }

func strAny(xs []string) []any {
	out := make([]any, 0, len(xs))
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}

func sortedCopy(xs []string) []string {
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}

func buildPolicy(tracked []string, optional []string) [][2]any {
	return [][2]any{{"tracked", sortedCopy(tracked)}, {"optional", sortedCopy(optional)}, {"ignored", sortedCopy(policyIgnored)}}
}

// RepairError is MissionStateRepairError.
type RepairError struct{ Msg string }

func (e *RepairError) Error() string { return e.Msg }

// absResolved is Path.resolve() (symlinks followed when the path exists).
func absResolved(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if r, err := filepath.EvalSymlinks(a); err == nil {
		return r
	}
	// resolve the longest existing prefix (Path.resolve(strict=False))
	dir, base := filepath.Split(a)
	dir = filepath.Clean(dir)
	if dir == a {
		return a
	}
	return filepath.Join(absResolved(dir), base)
}

// repoRelpath is _repo_relpath.
func repoRelpath(root, path string) string {
	r, p := absResolved(root), absResolved(path)
	rel, err := filepath.Rel(r, p)
	if err != nil || rel == ".." || len(rel) >= 3 && rel[:3] == "../" {
		return path
	}
	if rel == "." {
		return "."
	}
	return rel
}

// resolveRepoRelative is _resolve_repo_relative.
func resolveRepoRelative(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}
