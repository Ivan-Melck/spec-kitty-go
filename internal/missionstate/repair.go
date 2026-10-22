package missionstate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	"github.com/spec-kitty/go-kitty3/internal/healthcheck"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// Options configures RepairRepo.
type Options struct {
	Mission      *string
	ManifestPath string // "" = <AuditRoot>/<run_id>.json
	AllowDirty   bool
	Argv         []string // sys.argv[1:] (scrubbed into command_args)
	CLIVersion   string
	// ScanRoot overrides the kitty-specs/ discovery root (--fixture-dir /
	// --include-fixtures); every directory under it is a Mission.
	ScanRoot string
}

// rawRow is one non-blank status.events.jsonl line.
type rawRow struct {
	line int
	text string
	data *jsonx.Object
}

// splitUniversal splits text like Python's universal-newline line iteration.
func splitUniversal(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// readJSONLRows is _read_jsonl_rows.
func readJSONLRows(path string) ([]rawRow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, err
	}
	var rows []rawRow
	for i, line := range splitUniversal(text) {
		st := strings.TrimSpace(line)
		if st == "" {
			continue
		}
		v, err := pyjson.Loads(st)
		if err != nil {
			return nil, fmt.Errorf("Invalid JSON on line %d: %v", i+1, err)
		}
		o, ok := v.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("Invalid JSONL row on line %d: expected object", i+1)
		}
		rows = append(rows, rawRow{i + 1, st, o})
	}
	return rows, nil
}

// SelectMissionDirs is _select_mission_dirs (default kitty-specs/ scan).
func SelectMissionDirs(root string, handle *string) (dirs, residue []string, err error) {
	return SelectMissionDirsIn(root, "", handle)
}

// SelectMissionDirsIn is _select_mission_dirs(repo_root, scan_root=...): a
// non-empty scanRoot keeps every directory under it.
func SelectMissionDirsIn(root, scanRoot string, handle *string) (dirs, residue []string, err error) {
	specs := absResolved(filepath.Join(root, "kitty-specs"))
	if scanRoot != "" {
		specs = absResolved(scanRoot)
	}
	if _, err := os.Stat(specs); err != nil {
		return nil, nil, nil
	}
	var names []string
	if scanRoot == "" {
		names = migrate.MissionDirs(root)
	} else if entries, err := os.ReadDir(specs); err == nil {
		for _, e := range entries {
			if st, err := os.Stat(filepath.Join(specs, e.Name())); err == nil && st.IsDir() {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
	}
	if handle == nil && scanRoot != "" {
		for _, n := range names {
			dirs = append(dirs, filepath.Join(specs, n))
		}
		return dirs, nil, nil
	}
	if handle == nil {
		tracked := healthcheck.TrackedSpecs(root)
		for _, n := range names {
			d := filepath.Join(specs, n)
			if healthcheck.IsMissionDir(d, n, tracked) {
				dirs = append(dirs, d)
			} else {
				residue = append(residue, d)
			}
		}
		return dirs, residue, nil
	}
	var matches []string
	for _, n := range names {
		if missionHandleMatches(filepath.Join(specs, n), n, *handle) {
			matches = append(matches, filepath.Join(specs, n))
		}
	}
	if len(matches) == 0 {
		return nil, nil, &RepairError{"Mission not found: " + pyjson.ReprString(*handle)}
	}
	if len(matches) > 1 {
		var c []string
		for _, m := range matches {
			c = append(c, filepath.Base(m))
		}
		return nil, nil, &RepairError{fmt.Sprintf("Ambiguous mission handle %s: %s", pyjson.ReprString(*handle), strings.Join(c, ", "))}
	}
	if !fileExists(filepath.Join(matches[0], "spec.md")) && !fileExists(filepath.Join(matches[0], MetaFile)) {
		return nil, nil, &RepairError{fmt.Sprintf("%s holds no Mission artifact (no spec.md or meta.json); refusing to create one.", pyjson.ReprString(filepath.Base(matches[0])))}
	}
	return matches, nil, nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func missionHandleMatches(dir, name, handle string) bool {
	if name == handle {
		return true
	}
	m := missionPrefixRe.FindStringSubmatch(name)
	if m != nil && m[1] == handle {
		return true
	}
	stripped := name
	if m != nil {
		stripped = name[4:]
	}
	if stripped == handle {
		return true
	}
	if id, ok := valueOf(mission.ReadMetaOrEmpty(dir), "mission_id").(string); ok {
		if id == handle {
			return true
		}
		if mid8Re.MatchString(handle) && strings.HasPrefix(id, handle) {
			return true
		}
	}
	return false
}

func valueOf(o *jsonx.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

// computeRunID is _compute_run_id.
func computeRunID(root string, dirs []string) string {
	var buf bytes.Buffer
	buf.WriteString("spec-kitty:mission-state:v1\n")
	for _, d := range dirs {
		for _, name := range []string{MetaFile, EventsFile, StatusFile} {
			p := filepath.Join(d, name)
			buf.WriteString(repoRelpath(root, p))
			buf.WriteByte(0)
			if b, err := os.ReadFile(p); err == nil {
				buf.Write(b)
			} else {
				buf.WriteString("<missing>")
			}
			buf.WriteByte(0)
		}
	}
	return sha256Hex(buf.Bytes())[:16]
}

// RepairRepo is repair_repo: canonicalize historical mission state on disk
// and write the manifest. root must already be the primary checkout.
func RepairRepo(root string, o Options) (*RepairReport, error) {
	root = absResolved(root)
	dirs, residue, err := SelectMissionDirsIn(root, o.ScanRoot, o.Mission)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, &RepairError{"No mission directories found to repair."}
	}
	runID := computeRunID(root, dirs)
	manifestRel := o.ManifestPath
	if manifestRel == "" {
		manifestRel = filepath.Join(AuditRoot, runID+".json")
	}
	manifestAbs := resolveRepoRelative(root, manifestRel)
	var rels []string
	for _, d := range dirs {
		rels = append(rels, repoRelpath(root, d))
	}
	if err := assertGitSafe(root, rels, o.AllowDirty); err != nil {
		return nil, err
	}
	release, err := acquireRepairLock(root)
	if err != nil {
		return nil, err
	}
	defer release()
	generated := []string{runID}
	var results []*MissionResult
	for _, d := range dirs {
		results = append(results, repairMission(root, d, runID, &generated))
	}
	var qroot *string
	for _, r := range results {
		if r.QuarantineWritten {
			q := repoRelpath(root, filepath.Join(root, AuditRoot, "quarantine", runID))
			qroot = &q
			break
		}
	}
	var targets, residueNames []string
	for _, d := range dirs {
		targets = append(targets, filepath.Base(d))
	}
	for _, d := range residue {
		residueNames = append(residueNames, filepath.Base(d))
	}
	gen := map[string]bool{}
	var genIDs []string
	for _, g := range generated {
		if !gen[g] {
			gen[g] = true
			genIDs = append(genIDs, g)
		}
	}
	sort.Strings(genIDs)
	rep := &RepairReport{
		RunID: runID, RepoHead: gitHead(root), TargetMissions: targets,
		ManifestPath: repoRelpath(root, manifestAbs), Missions: results, CLIVersion: o.CLIVersion,
		CommandArgs: ScrubSecretArgs(o.Argv), GeneratedIDs: genIDs, Policy: buildPolicy(policyTracked, policyOptional),
		QuarantineRootPath: qroot, ResidueDirectories: residueNames,
	}
	if err := atomicfile.Write(manifestAbs, []byte(rep.ToJSON())); err != nil {
		return nil, err
	}
	return rep, nil
}

// repairState is _RepairState.
type repairState struct {
	slug              string
	missionID         *string
	fileChanges       []FileChange
	rowChanges        []RowTransformation
	validationErrors  []string
	quarantinedRows   int
	quarantineWritten bool
	metaActions       []string
}

func (s *repairState) recordChange(root, path string, before fingerprint) {
	after := fileFingerprint(path)
	if !before.equal(after) {
		s.fileChanges = append(s.fileChanges, FileChange{Path: repoRelpath(root, path), OldSHA: before.sha, NewSHA: after.sha, OldSize: before.size, NewSize: after.size})
	}
}

func (s *repairState) result(st string) *MissionResult {
	if st == "" {
		st = "unchanged"
		if len(s.fileChanges) > 0 || len(s.rowChanges) > 0 || s.quarantinedRows > 0 {
			st = "updated"
		}
	}
	return &MissionResult{Slug: s.slug, MissionID: s.missionID, Status: st, FileChanges: s.fileChanges,
		RowTransforms: s.rowChanges, QuarantinedRows: s.quarantinedRows, ValidationErrors: s.validationErrors,
		MetaActions: s.metaActions, QuarantineWritten: s.quarantineWritten}
}

// repairMission is _repair_mission.
func repairMission(root, dir, runID string, generated *[]string) (res *MissionResult) {
	st := &repairState{slug: filepath.Base(dir)}
	fail := func(err error) *MissionResult {
		st.validationErrors = append(st.validationErrors, err.Error())
		return st.result("error")
	}
	rows, err := readJSONLRows(filepath.Join(dir, EventsFile))
	if err != nil {
		return fail(err)
	}
	meta, err := repairMetaPhase(root, dir, rows, st, generated)
	if err != nil {
		return fail(err)
	}
	var snap *status.Snapshot
	if fileExists(filepath.Join(dir, EventsFile)) {
		snap, err = repairStatusLogPhases(root, dir, rows, st, runID, generated)
		if err != nil {
			return fail(err)
		}
		if snap == nil {
			return st.result("error")
		}
	}
	if snap != nil {
		if err := repairLanesPhase(root, dir, snap, meta, st); err != nil {
			return fail(err)
		}
	}
	return st.result("")
}

func repairMetaPhase(root, dir string, rows []rawRow, st *repairState, generated *[]string) (*jsonx.Object, error) {
	meta, actions, err := canonicalizeMeta(dir, rows, generated)
	if err != nil {
		return nil, err
	}
	st.metaActions = actions
	if v := valueOf(meta, "mission_slug"); pyjson.Truthy(v) {
		st.slug = pyjson.Str(v)
	}
	id := ""
	if v := valueOf(meta, "mission_id"); pyjson.Truthy(v) {
		id = pyjson.Str(v)
	}
	st.missionID = &id
	metaPath := filepath.Join(dir, MetaFile)
	before := fileFingerprint(metaPath)
	if len(actions) > 0 {
		if errs := validateMeta(meta); len(errs) > 0 {
			return nil, fmt.Errorf("Invalid meta.json for %s: %s", filepath.Base(dir), strings.Join(errs, "; "))
		}
		if err := atomicfile.Write(metaPath, mission.RenderMeta(meta)); err != nil {
			return nil, err
		}
	}
	st.recordChange(root, metaPath, before)
	return meta, nil
}

// validateMeta is mission_metadata.validate_meta.
func validateMeta(meta *jsonx.Object) []string {
	var errs []string
	for _, f := range requiredMetaFields {
		v, ok := meta.Get(f)
		if !ok || !pyjson.Truthy(v) {
			errs = append(errs, "Missing or empty required field: "+f)
		}
	}
	for _, f := range purposeFields {
		if v, ok := meta.Get(f); ok {
			s, isStr := v.(string)
			if !isStr {
				errs = append(errs, fmt.Sprintf("Field %s must be a string when present", f))
			} else if strings.Join(strings.Fields(s), " ") == "" {
				errs = append(errs, fmt.Sprintf("Field %s must not be empty when present", f))
			}
		}
	}
	if v, ok := meta.Get("change_mode"); ok && v != "bulk_edit" {
		errs = append(errs, fmt.Sprintf("Invalid change_mode %s; valid values: ['bulk_edit']", pyjson.Repr(v)))
	}
	return errs
}

func setDefault(o *jsonx.Object, k string, v any) {
	if !o.Has(k) {
		o.Set(k, v)
	}
}

// canonicalizeMeta is _canonicalize_meta.
func canonicalizeMeta(dir string, rows []rawRow, generated *[]string) (*jsonx.Object, []string, error) {
	loaded, err := migrate.LoadMetaFailClosed(dir)
	if err != nil {
		return nil, nil, err
	}
	meta := jsonx.NewObject()
	if loaded != nil {
		meta = loaded.Clone()
	}
	var actions []string
	slug := filepath.Base(dir)
	for _, k := range []string{"mission_slug", "slug", "feature_slug"} {
		if v := valueOf(meta, k); pyjson.Truthy(v) {
			slug = pyjson.Str(v)
			break
		}
	}
	meta.Set("mission_slug", slug)
	setDefault(meta, "slug", slug)
	setDefault(meta, "friendly_name", slug)
	mt := defaultMissionType
	for _, k := range []string{"mission_type", "mission"} {
		if v := valueOf(meta, k); pyjson.Truthy(v) {
			mt = pyjson.Str(v)
			break
		}
	}
	meta.Set("mission_type", mt)
	setDefault(meta, "target_branch", defaultTargetBranch)
	if !pyjson.Truthy(valueOf(meta, "created_at")) {
		ts := firstEventTimestamp(rows)
		if ts == "" {
			ts = epochTimestamp
		}
		meta.Set("created_at", ts)
		actions = append(actions, "created_at_defaulted")
	}
	numberRaw := valueOf(meta, "mission_number")
	if numberRaw == nil || numberRaw == "" {
		numberRaw = valueOf(meta, "feature_number")
	}
	num, err := coerceMissionNumber(numberRaw)
	if err != nil {
		return nil, nil, err
	}
	if num == nil {
		num = missionNumberFromSlug(slug)
	}
	if num == nil {
		meta.Set("mission_number", nil)
	} else {
		meta.Set("mission_number", *num)
	}
	existing, isStr := valueOf(meta, "mission_id").(string)
	if !isStr || !ulidRe.MatchString(existing) {
		minted := DeterministicULID(missionSeed(dir, meta, rows))
		meta.Set("mission_id", minted)
		*generated = append(*generated, minted)
		actions = append(actions, "mission_id_deterministically_backfilled")
	}
	for _, k := range metaLegacyAliases {
		if meta.Has(k) {
			meta.Delete(k)
			actions = append(actions, "removed_meta_key:"+k)
		}
	}
	if v, ok := meta.Get("change_mode"); ok && v != "bulk_edit" {
		meta.Delete("change_mode")
		actions = append(actions, "normalized_change_mode:"+pyjson.Str(v))
	}
	if errs := validateMeta(meta); len(errs) > 0 {
		return nil, nil, fmt.Errorf("Invalid canonical meta.json for %s: %s", filepath.Base(dir), strings.Join(errs, "; "))
	}
	return meta, actions, nil
}

func firstEventTimestamp(rows []rawRow) string {
	var vals []string
	for _, r := range rows {
		if v := valueOf(r.data, "at"); pyjson.Truthy(v) {
			vals = append(vals, pyjson.Str(v))
		}
	}
	if len(vals) == 0 {
		return ""
	}
	sort.Strings(vals)
	return vals[0]
}

// missionSeed is _mission_seed.
func missionSeed(dir string, meta *jsonx.Object, rows []rawRow) string {
	ms := valueOf(meta, "mission_slug")
	if !pyjson.Truthy(ms) {
		ms = filepath.Base(dir)
	}
	stable := jsonx.NewObject("mission_slug", ms, "slug", valueOf(meta, "slug"), "friendly_name", valueOf(meta, "friendly_name"),
		"created_at", valueOf(meta, "created_at"), "target_branch", valueOf(meta, "target_branch"), "mission_type", valueOf(meta, "mission_type"))
	var first *jsonx.Object
	if len(rows) > 0 {
		first = rows[0].data
	}
	return jsonx.Dumps(jsonx.NewObject("meta", stable, "first_event_id", valueOf(first, "event_id"), "first_event_at", valueOf(first, "at")), jsonx.Line)
}

// coerceMissionNumber is mission_state._coerce_mission_number.
func coerceMissionNumber(v any) (*int, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case bool:
		return nil, fmt.Errorf("mission_number must be int or null, got bool %s", pyjson.Repr(t))
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, nil
		}
		if isASCIIDigits(s) {
			n, err := strconv.Atoi(strings.TrimLeft(s, "0") + zeroIfEmpty(strings.TrimLeft(s, "0")))
			if err == nil {
				return &n, nil
			}
		}
	case int:
		return &t, nil
	case int64:
		n := int(t)
		return &n, nil
	}
	return nil, fmt.Errorf("mission_number must be int or numeric string, got %s", pyjson.Repr(v))
}

func zeroIfEmpty(s string) string {
	if s == "" {
		return "0"
	}
	return ""
}

func isASCIIDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// missionNumberFromSlug is mission_metadata.mission_number_from_slug.
func missionNumberFromSlug(slug string) *int {
	m := missionNumberRe.FindStringSubmatch(strings.TrimSpace(slug))
	if m == nil {
		return nil
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	return &n
}

// repairStatusLogPhases is _repair_status_log_phases. A nil snapshot with a
// nil error means row-level errors stopped the repair (reported as error).
func repairStatusLogPhases(root, dir string, rows []rawRow, st *repairState, runID string, generated *[]string) (*status.Snapshot, error) {
	eventsPath := filepath.Join(dir, EventsFile)
	mid := ""
	if st.missionID != nil {
		mid = *st.missionID
	}
	canon, transforms, quarantine, rowErrs, surviving := canonicalizeStatusRows(root, dir, rows, st.slug, mid, generated)
	st.rowChanges = append(st.rowChanges, transforms...)
	combined := append(append([]string{}, rowErrs...), quarantineViolations(quarantine, surviving)...)
	st.validationErrors = append(st.validationErrors, combined...)
	if len(combined) > 0 {
		st.quarantinedRows = len(quarantine)
		return nil, nil
	}
	logChanged, err := repairEventsPhase(root, eventsPath, rows, canon, st)
	if err != nil {
		return nil, err
	}
	if len(quarantine) > 0 {
		if err := repairQuarantinePhase(root, runID, quarantine, st); err != nil {
			return nil, err
		}
	}
	return repairStatusJSONPhase(root, dir, st, logChanged)
}

func repairEventsPhase(root, eventsPath string, rows []rawRow, canon []string, st *repairState) (bool, error) {
	before := fileFingerprint(eventsPath)
	original, err := os.ReadFile(eventsPath)
	if err != nil {
		return false, err
	}
	rendered := renderEventLog(canon, original)
	if len(rows) > 0 && len(canon) == 0 {
		return false, &RepairError{fmt.Sprintf("Refusing to empty %s: all %d row(s) were dropped by repair. This usually indicates a row-classification bug; the original rows are preserved verbatim in the quarantine directory.", repoRelpath(root, eventsPath), len(rows))}
	}
	if !bytes.Equal(original, rendered) {
		if err := atomicfile.Write(eventsPath, rendered); err != nil {
			return false, err
		}
	}
	n := len(st.fileChanges)
	st.recordChange(root, eventsPath, before)
	return len(st.fileChanges) > n, nil
}

// renderEventLog is _render_event_log (keeps CRLF and final-newline state).
func renderEventLog(lines []string, original []byte) []byte {
	eol := "\n"
	if bytes.Contains(original, []byte("\r\n")) {
		eol = "\r\n"
	}
	body := strings.Join(lines, eol)
	if len(lines) > 0 && (bytes.HasSuffix(original, []byte("\n")) || len(original) == 0) {
		body += eol
	}
	return []byte(body)
}

func repairQuarantinePhase(root, runID string, lines []string, st *repairState) error {
	st.quarantinedRows = len(lines)
	if !mission.SafePathSegment(st.slug) {
		return fmt.Errorf("Not a safe path segment: %s", pyjson.ReprString(st.slug))
	}
	path := filepath.Join(root, AuditRoot, "quarantine", runID, st.slug, EventsFile)
	before := fileFingerprint(path)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(strings.TrimRight(l, "\n") + "\n")
	}
	if err := atomicfile.Write(path, []byte(b.String())); err != nil {
		return err
	}
	st.quarantineWritten = true
	st.recordChange(root, path, before)
	return nil
}

func repairStatusJSONPhase(root, dir string, st *repairState, logChanged bool) (*status.Snapshot, error) {
	path := filepath.Join(dir, StatusFile)
	snap, err := status.MaterializeSnapshot(dir)
	if err != nil {
		return nil, err
	}
	if !fileExists(path) {
		return snap, nil
	}
	want := snap.Render()
	if cur, err := os.ReadFile(path); err == nil && string(cur) == want {
		return snap, nil
	}
	if !logChanged || !isTracked(root, repoRelpath(root, path)) {
		st.metaActions = append(st.metaActions, StatusJSONDriftNotWrittenAction)
		return snap, nil
	}
	before := fileFingerprint(path)
	if err := atomicfile.Write(path, []byte(want)); err != nil {
		return nil, err
	}
	st.recordChange(root, path, before)
	return snap, nil
}
