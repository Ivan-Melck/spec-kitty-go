// Package dossier ports the mission-dossier parity pieces `spec-kitty
// reconcile` relies on (specify_cli.dossier): the canonical snapshot hash,
// the WP static-projection hash, the present-artifact source projection of
// a feature directory, and the per-artifact reconciler.
package dossier

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
	"gopkg.in/yaml.v3"
)

// Entry is one (relative_path, content_hash) projection entry.
type Entry struct{ Path, Hash string }

// SnapshotHash is compute_dossier_snapshot_hash: sort by (path, hash), join
// "path\thash" lines with "\n", sha256, prefix "sha256:".
func SnapshotHash(entries []Entry) string {
	es := append([]Entry{}, entries...)
	sort.Slice(es, func(i, j int) bool {
		if es[i].Path != es[j].Path {
			return es[i].Path < es[j].Path
		}
		return es[i].Hash < es[j].Hash
	})
	lines := make([]string, len(es))
	for i, e := range es {
		lines[i] = e.Path + "\t" + e.Hash
	}
	s := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(s[:])
}

// staticFields is WP_STATIC_PROJECTION_FIELDS.
var staticFields = []string{"work_package_id", "title", "dependencies", "requirement_refs", "tracker_refs", "priority",
	"execution_mode", "owned_files", "create_intent", "authoritative_surface", "scope", "task_type", "subtasks", "phase"}

type kind int

const (
	kStr kind = iota
	kStrList
	kList
	kAny
	kInt
	kLane
)

// wpFields is the WPMetadata schema (extra="forbid").
var wpFields = map[string]kind{
	"work_package_id": kStr, "title": kStr, "dependencies": kStrList, "base_branch": kStr, "base_commit": kStr,
	"created_at": kStr, "planning_base_branch": kStr, "merge_target_branch": kStr, "branch_strategy": kStr,
	"requirement_refs": kStrList, "tracker_refs": kStrList, "priority": kStr, "execution_mode": kStr,
	"owned_files": kStrList, "create_intent": kStrList, "authoritative_surface": kStr, "scope": kStr, "task_type": kStr,
	"subtasks": kList, "phase": kStr, "phases": kStr, "assignee": kStr, "agent": kAny, "model": kStr,
	"agent_profile": kStr, "role": kStr, "shell_pid": kInt, "shell_pid_created_at": kStr, "history": kList,
	"lane": kLane, "feature_slug": kStr, "activity_log": kStr, "review_status": kStr, "reviewed_by": kStr,
	"approved_by": kStr, "reviewer": kAny, "reviewer_agent": kStr, "reviewer_shell_pid": kStr, "review_feedback": kStr,
	"review_feedback_file": kStr, "subtitle": kStr, "description": kStr, "estimated_duration": kStr, "tags": kStrList,
	"mission_id": kStr, "mission_number": kStr, "mission_slug": kStr, "status": kStr, "wp_code": kStr,
	"work_package_title": kStr,
}

var validLanes = map[string]bool{"planned": true, "claimed": true, "in_progress": true, "for_review": true,
	"in_review": true, "approved": true, "done": true, "blocked": true, "canceled": true}

var (
	reWPID       = regexp.MustCompile(`^WP\d{2,}\n?$`)
	reBaseCommit = regexp.MustCompile(`^[0-9a-f]{7,40}\n?$`)
	reRefSplit   = regexp.MustCompile(`[,\s]+`)
)

// ValidationError is a WPMetadata schema failure (the artifact is then
// non-present: fail closed).
type ValidationError struct{ Field, Msg string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

// timestampKeys reports the top-level keys whose value ruamel loads as a
// date/datetime (an implicit YAML timestamp).
func timestampKeys(src string) map[string]bool {
	out := map[string]bool{}
	var n yaml.Node
	if yaml.Unmarshal([]byte(src), &n) != nil || len(n.Content) == 0 || n.Content[0].Kind != yaml.MappingNode {
		return out
	}
	m := n.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if v := m.Content[i+1]; v.Kind == yaml.ScalarNode && v.Tag == "!!timestamp" {
			out[m.Content[i].Value] = true
		}
	}
	return out
}

func strList(v any, field string) ([]any, error) {
	l, ok := v.([]any)
	if !ok {
		return nil, &ValidationError{field, "Input should be a valid list"}
	}
	for _, x := range l {
		if _, ok := x.(string); !ok {
			return nil, &ValidationError{field, "Input should be a valid string"}
		}
	}
	return l, nil
}

// pyStr is str(v) for the phase coercion.
func pyStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		return pyjson.Repr(t)
	}
	return pyjson.Repr(v)
}

// validateWP is WPMetadata.model_validate on a frontmatter mapping; it
// returns the static projection.
func validateWP(meta *jsonx.Object, timestamps map[string]bool) (*jsonx.Object, error) {
	data := map[string]any{}
	var keys []string
	for _, kv := range meta.Pairs() {
		data[kv.Key] = kv.Value
		keys = append(keys, kv.Key)
	}
	if _, ok := data["title"]; !ok {
		if t, ok := data["work_package_title"]; ok {
			data["title"] = t
		}
	}
	if d, ok := data["dependencies"].(string); ok {
		s := strings.TrimSpace(d)
		l := []any{}
		if s != "[]" {
			for _, p := range strings.Split(s, ",") {
				if p = strings.TrimSpace(p); p != "" {
					l = append(l, p)
				}
			}
		}
		data["dependencies"] = l
	}
	if r, ok := data["requirement_refs"].(string); ok {
		l := []any{}
		for _, tok := range reRefSplit.Split(r, -1) {
			if strings.TrimSpace(tok) != "" {
				l = append(l, tok)
			}
		}
		data["requirement_refs"] = l
	}
	if r, ok := data["tracker_refs"].(string); ok {
		l := []any{}
		for _, p := range strings.Split(r, ",") {
			if p = strings.TrimSpace(p); p != "" {
				l = append(l, p)
			}
		}
		data["tracker_refs"] = l
	}
	for _, k := range keys {
		if _, ok := wpFields[k]; !ok {
			return nil, &ValidationError{k, "Extra inputs are not permitted"}
		}
	}
	if _, ok := data["work_package_id"]; !ok {
		return nil, &ValidationError{"work_package_id", "Field required"}
	}
	out := map[string]any{}
	for field, k := range wpFields {
		v, present := data[field]
		if !present {
			switch k {
			case kStrList, kList:
				out[field] = []any{}
			default:
				out[field] = nil
			}
			continue
		}
		if timestamps[field] && k != kAny && k != kList {
			if field != "phase" {
				return nil, &ValidationError{field, "Input should be a valid string"}
			}
		}
		switch k {
		case kStr:
			if field == "phase" && v != nil {
				v = pyStr(v)
			}
			if v == nil {
				out[field] = nil
				continue
			}
			s, ok := v.(string)
			if !ok {
				return nil, &ValidationError{field, "Input should be a valid string"}
			}
			switch field {
			case "work_package_id":
				if !reWPID.MatchString(s) {
					return nil, &ValidationError{field, "Invalid work_package_id"}
				}
			case "title":
				if strings.TrimSpace(s) == "" {
					return nil, &ValidationError{field, "title must not be empty"}
				}
			case "base_commit":
				if s == "unknown" {
					out[field] = nil
					continue
				}
				if !reBaseCommit.MatchString(s) {
					return nil, &ValidationError{field, "Invalid base_commit"}
				}
			}
			out[field] = s
		case kStrList:
			l, err := strList(v, field)
			if err != nil {
				return nil, err
			}
			out[field] = l
		case kList:
			l, ok := v.([]any)
			if !ok {
				return nil, &ValidationError{field, "Input should be a valid list"}
			}
			out[field] = l
		case kAny:
			out[field] = v
		case kInt:
			switch t := v.(type) {
			case nil:
			case string:
				if t == "" {
					break
				}
				if _, err := strconv.ParseInt(strings.TrimSpace(strings.ReplaceAll(t, "_", "")), 10, 64); err != nil {
					return nil, &ValidationError{field, "invalid literal for int()"}
				}
			case int64, bool:
			case float64:
			default:
				return nil, &ValidationError{field, "Input should be a valid integer"}
			}
		case kLane:
			if v == nil || v == "" {
				continue
			}
			s := pyStr(v)
			if s == "doing" {
				s = "in_progress"
			}
			if !validLanes[s] {
				return nil, &ValidationError{field, "Invalid lane value"}
			}
		}
	}
	proj := jsonx.NewObject()
	for _, f := range staticFields {
		proj.Set(f, out[f])
	}
	return proj, nil
}

// StaticProjectionHash is hash_wp_static_projection over a WP frontmatter
// source: json.dumps(projection, sort_keys=True, separators=(",", ":"),
// ensure_ascii=False) hashed with sha256 (bare hex).
func StaticProjectionHash(yamlSrc string, meta *jsonx.Object) (string, error) {
	proj, err := validateWP(meta, timestampKeys(yamlSrc))
	if err != nil {
		return "", err
	}
	payload := jsonx.Dumps(proj, jsonx.Options{Indent: -1, SortKeys: true, Compact: true})
	s := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(s[:]), nil
}

var reWPFile = regexp.MustCompile(`(?i)^WP\d{2,}`)

func isWPArtifact(p string) bool {
	base := filepath.Base(p)
	ext := filepath.Ext(base)
	return strings.EqualFold(ext, ".md") && reWPFile.MatchString(strings.TrimSuffix(base, ext))
}

// hashFile is hash_file_with_validation ("" when unreadable or not UTF-8).
func hashFile(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil || !utf8.Valid(b) {
		return "", false
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), true
}

// readFrontmatter is FrontmatterManager.read's parse: ok=false means "no
// parseable frontmatter" (raw-byte fallback).
func readFrontmatter(p string) (string, *jsonx.Object, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", nil, false
	}
	text := strings.TrimPrefix(string(b), "\ufeff")
	src, _, ok := fm.Split(text)
	if !ok {
		return "", nil, false
	}
	meta, err := yamlx.ParseObject([]byte(src))
	if err != nil || meta == nil {
		return "", nil, false
	}
	if strings.HasPrefix(filepath.Base(p), "WP") && !meta.Has("dependencies") {
		meta.Set("dependencies", []any{})
	}
	return src, meta, true
}

// hashArtifact is the indexer's per-file content hash ("" = non-present).
func hashArtifact(p string) string {
	if isWPArtifact(p) {
		src, meta, ok := readFrontmatter(p)
		if !ok {
			h, _ := hashFile(p)
			return h
		}
		h, err := StaticProjectionHash(src, meta)
		if err != nil {
			return ""
		}
		return h
	}
	h, _ := hashFile(p)
	return h
}

// SourceProjection is Indexer.index_feature + present_projection: every
// non-hidden file under featureDir with its content hash (present only).
func SourceProjection(featureDir string) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(featureDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == featureDir {
			return nil
		}
		rel, _ := filepath.Rel(featureDir, p)
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			return nil
		}
		if h := hashArtifact(p); h != "" {
			out = append(out, Entry{filepath.ToSlash(rel), h})
		}
		return nil
	})
	return out, err
}

// RecordedProjection is present_projection over a recorded snapshot's
// artifact_summaries.
func RecordedProjection(snapshot *jsonx.Object) []Entry {
	var out []Entry
	v, _ := snapshot.Get("artifact_summaries")
	list, _ := v.([]any)
	for _, x := range list {
		o, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		h, _ := o.Get("content_hash_sha256")
		pres, _ := o.Get("is_present")
		hs := scalar(h)
		if truthy(pres) && hs != "" {
			rp, _ := o.Get("relative_path")
			out = append(out, Entry{scalar(rp), hs})
		}
	}
	return out
}

func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	return pyStr(v)
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int64:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.Object:
		return t.Len() > 0
	}
	return true
}

// Divergence is ArtifactDivergence.
type Divergence struct {
	Path, Kind         string
	SourceHash, Record *string
}

// Result is ReconciliationResult.
type Result struct {
	Status                    string // parity | divergence | error
	RebuiltHash, RecordedHash *string
	Differing                 []Divergence
	Error                     string
}

func ptr(s string) *string { return &s }

func index(entries []Entry) (map[string]string, error) {
	m := map[string]string{}
	for _, e := range entries {
		if _, ok := m[e.Path]; ok {
			return nil, fmt.Errorf("duplicate artifact path in projection: %s", pyjson.ReprString(e.Path))
		}
		m[e.Path] = e.Hash
	}
	return m, nil
}

// Reconcile is DossierReconciler.reconcile.
func Reconcile(source, recorded []Entry) Result {
	rh, ch := SnapshotHash(source), SnapshotHash(recorded)
	if rh == ch {
		return Result{Status: "parity", RebuiltHash: ptr(rh), RecordedHash: ptr(ch)}
	}
	sm, err := index(source)
	if err == nil {
		var rm map[string]string
		rm, err = index(recorded)
		if err == nil {
			paths := map[string]bool{}
			for p := range sm {
				paths[p] = true
			}
			for p := range rm {
				paths[p] = true
			}
			var keys []string
			for p := range paths {
				keys = append(keys, p)
			}
			sort.Strings(keys)
			var divs []Divergence
			for _, p := range keys {
				s, inS := sm[p]
				r, inR := rm[p]
				var d Divergence
				switch {
				case inS && !inR:
					d = Divergence{Path: p, Kind: "missing_in_projection", SourceHash: ptr(s)}
				case inR && !inS:
					d = Divergence{Path: p, Kind: "missing_in_source", Record: ptr(r)}
				case s != r:
					d = Divergence{Path: p, Kind: "content_mismatch", SourceHash: ptr(s), Record: ptr(r)}
				default:
					continue
				}
				divs = append(divs, d)
			}
			if len(divs) == 0 {
				return Result{Status: "error", RebuiltHash: ptr(rh), RecordedHash: ptr(ch),
					Error: "snapshot hashes differ but no differing artifact could be named; the projections are inconsistent"}
			}
			return Result{Status: "divergence", RebuiltHash: ptr(rh), RecordedHash: ptr(ch), Differing: divs}
		}
	}
	return Result{Status: "error", RebuiltHash: ptr(rh), RecordedHash: ptr(ch), Error: err.Error()}
}
