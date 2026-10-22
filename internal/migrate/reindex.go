package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/dossier"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Dossier re-index for `migrate rebaseline-dossier-hashes`:
// dossier/indexer.py (Indexer.index_feature), dossier/snapshot.py
// (compute_snapshot), charter/activation/manifest_loader.py (load_manifest
// with the org-pack tier) and the ArtifactRef / ExpectedArtifactManifest
// pydantic models.

// manifestSpec is ExpectedArtifactSpec.
type manifestSpec struct {
	key, class, pattern string
	blocking            bool
}

// artifactManifest is ExpectedArtifactManifest (the parts the indexer reads).
type artifactManifest struct {
	requiredAlways []manifestSpec
	requiredByStep [][]manifestSpec // declaration order
	optionalAlways []manifestSpec
}

// allSpecs is required_always + sum(required_by_step.values()) + optional_always.
func (m *artifactManifest) allSpecs() []manifestSpec {
	out := append([]manifestSpec{}, m.requiredAlways...)
	for _, s := range m.requiredByStep {
		out = append(out, s...)
	}
	return append(out, m.optionalAlways...)
}

func (m *artifactManifest) requiredStatus(key string) string {
	if m == nil {
		return "optional"
	}
	for _, s := range m.requiredAlways {
		if s.key == key {
			return "required"
		}
	}
	for _, l := range m.requiredByStep {
		for _, s := range l {
			if s.key == key {
				return "required"
			}
		}
	}
	return "optional"
}

var artifactClasses = []string{"input", "workflow", "output", "evidence", "policy", "runtime"}

// manifestValidator validates a parsed manifest mapping like
// ExpectedArtifactManifest.model_validate (extra="forbid", lax mode).
type manifestValidator struct{ errs []pydError }

func (v *manifestValidator) add(loc []any, msg, typ string, in any) {
	v.errs = append(v.errs, pydError{loc: loc, msg: msg, typ: typ, input: in})
}

func (v *manifestValidator) str(loc []any, in any, minLen bool) string {
	s, ok := in.(string)
	if !ok {
		v.add(loc, "Input should be a valid string", "string_type", in)
		return ""
	}
	if minLen && utf8.RuneCountInString(s) < 1 {
		v.add(loc, "String should have at least 1 character", "string_too_short", in)
	}
	return s
}

func (v *manifestValidator) boolean(loc []any, in any) bool {
	switch t := in.(type) {
	case bool:
		return t
	case int64:
		if t == 0 || t == 1 {
			return t == 1
		}
		v.add(loc, "Input should be a valid boolean, unable to interpret input", "bool_parsing", in)
	case int:
		if t == 0 || t == 1 {
			return t == 1
		}
		v.add(loc, "Input should be a valid boolean, unable to interpret input", "bool_parsing", in)
	case float64:
		if t == 0 || t == 1 {
			return t == 1
		}
		v.add(loc, "Input should be a valid boolean, unable to interpret input", "bool_parsing", in)
	case string:
		switch strings.ToLower(t) {
		case "0", "off", "f", "false", "n", "no":
			return false
		case "1", "on", "t", "true", "y", "yes":
			return true
		}
		v.add(loc, "Input should be a valid boolean, unable to interpret input", "bool_parsing", in)
	default:
		v.add(loc, "Input should be a valid boolean", "bool_type", in)
	}
	return false
}

func (v *manifestValidator) spec(loc []any, in any) manifestSpec {
	o, ok := in.(*jsonx.Object)
	if !ok {
		v.add(loc, "Input should be a valid dictionary or instance of ExpectedArtifactSpec", "model_type", in)
		return manifestSpec{}
	}
	var s manifestSpec
	if x, ok := o.Get("artifact_key"); ok {
		s.key = v.str(pydLoc(loc, "artifact_key"), x, true)
	} else {
		v.add(pydLoc(loc, "artifact_key"), "Field required", "missing", o)
	}
	if x, ok := o.Get("artifact_class"); ok {
		cs, isStr := x.(string)
		valid := false
		for _, c := range artifactClasses {
			if isStr && cs == c {
				valid = true
			}
		}
		if valid {
			s.class = cs
		} else {
			v.add(pydLoc(loc, "artifact_class"), "Input should be 'input', 'workflow', 'output', 'evidence', 'policy' or 'runtime'", "enum", x)
		}
	} else {
		v.add(pydLoc(loc, "artifact_class"), "Field required", "missing", o)
	}
	if x, ok := o.Get("path_pattern"); ok {
		s.pattern = v.str(pydLoc(loc, "path_pattern"), x, true)
	} else {
		v.add(pydLoc(loc, "path_pattern"), "Field required", "missing", o)
	}
	if x, ok := o.Get("blocking"); ok {
		s.blocking = v.boolean(pydLoc(loc, "blocking"), x)
	}
	v.extras(loc, o, "artifact_key", "artifact_class", "path_pattern", "blocking")
	return s
}

func (v *manifestValidator) specList(loc []any, in any) []manifestSpec {
	l, ok := in.([]any)
	if !ok {
		v.add(loc, "Input should be a valid list", "list_type", in)
		return nil
	}
	out := make([]manifestSpec, 0, len(l))
	for i, x := range l {
		out = append(out, v.spec(pydLoc(loc, i), x))
	}
	return out
}

func (v *manifestValidator) extras(loc []any, o *jsonx.Object, known ...string) {
	for _, kv := range o.Pairs() {
		found := false
		for _, k := range known {
			if kv.Key == k {
				found = true
			}
		}
		if !found {
			v.add(pydLoc(loc, kv.Key), "Extra inputs are not permitted", "extra_forbidden", kv.Value)
		}
	}
}

func validateManifest(o *jsonx.Object) (*artifactManifest, error) {
	v := &manifestValidator{}
	m := &artifactManifest{}
	if x, ok := o.Get("schema_version"); ok {
		v.str([]any{"schema_version"}, x, false)
	}
	if x, ok := o.Get("mission_type"); ok {
		v.str([]any{"mission_type"}, x, false)
	} else {
		v.add([]any{"mission_type"}, "Field required", "missing", o)
	}
	if x, ok := o.Get("manifest_version"); ok {
		v.str([]any{"manifest_version"}, x, false)
	}
	if x, ok := o.Get("required_always"); ok {
		m.requiredAlways = v.specList([]any{"required_always"}, x)
	}
	if x, ok := o.Get("required_by_step"); ok {
		if d, isObj := x.(*jsonx.Object); isObj {
			for _, kv := range d.Pairs() {
				m.requiredByStep = append(m.requiredByStep, v.specList([]any{"required_by_step", kv.Key}, kv.Value))
			}
		} else {
			v.add([]any{"required_by_step"}, "Input should be a valid dictionary", "dict_type", x)
		}
	}
	if x, ok := o.Get("optional_always"); ok {
		m.optionalAlways = v.specList([]any{"optional_always"}, x)
	}
	v.extras(nil, o, "schema_version", "mission_type", "manifest_version", "required_always", "required_by_step", "optional_always")
	if len(v.errs) > 0 {
		return nil, &pydValidationError{title: "ExpectedArtifactManifest", errors: v.errs}
	}
	return m, nil
}

// manifestError is ManifestSchemaError / MalformedManifestError.
type manifestError struct{ msg string }

func (e *manifestError) Error() string { return e.msg }

func schemaError(missionType, origin string, err error) error {
	return &manifestError{fmt.Sprintf("expected-artifacts.yaml schema-invalid for mission type %s (%s): %s", pyjson.ReprString(missionType), origin, err)}
}

func malformedManifest(path string, cause string) error {
	return &manifestError{fmt.Sprintf("Malformed expected-artifacts manifest at %s: %s", path, cause)}
}

// readOrgManifest is org_expected_artifacts._read_yaml_mapping.
func readOrgManifest(path string) (*jsonx.Object, error) {
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, malformedManifest(path, err.Error())
	}
	if _, derr := pyjson.DecodeUTF8(b); derr != nil {
		return nil, malformedManifest(path, derr.Error())
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, malformedManifest(path, err.Error())
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, malformedManifest(path, "expected a YAML mapping, got "+pyjson.TypeName(v))
	}
	return o, nil
}

// loadArtifactManifest is charter.activation.manifest_loader.load_manifest:
// the last org-pack root carrying missions/<type>/expected-artifacts.yaml
// wins (whole file), else the built-in manifest; nil when absent.
func loadArtifactManifest(missionType, repoRoot string) (*artifactManifest, error) {
	var roots []string
	if repoRoot != "" {
		roots = charter.ResolvePackChain(repoRoot)
	}
	var org *jsonx.Object
	for _, r := range roots {
		o, err := readOrgManifest(filepath.Join(r, "missions", missionType, "expected-artifacts.yaml"))
		if err != nil {
			return nil, err
		}
		if o != nil {
			org = o
		}
	}
	if org != nil {
		m, err := validateManifest(org)
		if err != nil {
			origin := fmt.Sprintf("org-tier expected-artifacts.yaml for mission type %s (no single source file path available; checked org roots: %s)",
				pyjson.ReprString(missionType), strings.Join(roots, ", "))
			return nil, schemaError(missionType, origin, err)
		}
		return m, nil
	}
	if missionType == "" || strings.ContainsAny(missionType, "/\\") || missionType == "." || missionType == ".." {
		return nil, nil
	}
	b, err := assets.ReadFile("packs/built-in/missions/" + missionType + "/expected-artifacts.yaml")
	if err != nil {
		return nil, nil
	}
	v, err := yamlx.Parse(b)
	if err != nil || v == nil {
		return nil, nil
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, nil
	}
	m, err := validateManifest(o)
	if err != nil {
		return nil, schemaError(missionType, "built-in/"+missionType+"/expected-artifacts.yaml", err)
	}
	return m, nil
}

// indexedArtifact is ArtifactRef (the fields the snapshot summarises).
type indexedArtifact struct {
	key, class, relPath, hash string
	size                      int64
	required                  string
	present                   bool
	errorReason               *string
	indexedAt                 time.Time
}

var artifactKeyRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validateArtifactKey is ArtifactRef's artifact_key validator.
func validateArtifactKey(key string) error {
	if key == "" {
		return &pydValidationError{title: "ArtifactRef", errors: []pydError{{loc: []any{"artifact_key"}, msg: "String should have at least 1 character", typ: "string_too_short", input: key}}}
	}
	// re.match's "$" also accepts one trailing newline.
	if artifactKeyRe.MatchString(key) || artifactKeyRe.MatchString(strings.TrimSuffix(key, "\n")) && strings.HasSuffix(key, "\n") {
		return nil
	}
	msg := fmt.Sprintf("Value error, artifact_key must contain only alphanumeric characters, dots, underscores, and hyphens; got '%s'", key)
	return &pydValidationError{title: "ArtifactRef", errors: []pydError{{loc: []any{"artifact_key"}, msg: msg, typ: "value_error", input: key}}}
}

// pyStem is PurePath.stem.
func pyStem(name string) string {
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return name
	}
	return name[:i]
}

// deriveArtifactKey is Indexer._derive_artifact_key (manifest patterns are
// matched against the bare file name here, as upstream does).
func deriveArtifactKey(name string, m *artifactManifest) string {
	if m != nil {
		for _, s := range m.allSpecs() {
			if lanes.Fnmatch(name, s.pattern) {
				return s.key
			}
		}
	}
	key := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(pyStem(name)), "_", "-"), " ", "-")
	return "artifact." + key
}

// classifyArtifact is Indexer._classify_artifact ("" = ValueError).
func classifyArtifact(rel string, m *artifactManifest) string {
	if m != nil {
		for _, s := range m.allSpecs() {
			if lanes.Fnmatch(rel, s.pattern) {
				return s.class
			}
		}
	}
	name := strings.ToLower(filepath.Base(rel))
	switch {
	case name == "spec.md" || name == "specification.md":
		return "input"
	case name == "plan.md":
		return "workflow"
	case name == "tasks.md" || (strings.HasPrefix(name, "wp") && strings.HasSuffix(name, ".md")):
		return "workflow"
	case name == "research.md", name == "analysis-report.md":
		return "evidence"
	case strings.Contains(name, "test") || strings.HasPrefix(name, "test_"):
		return "evidence"
	case strings.Contains(name, "gap-analysis"):
		return "evidence"
	case strings.Contains(name, "log") || strings.HasSuffix(name, ".log"):
		return "evidence"
	case strings.Contains(name, "requirements") || strings.Contains(name, "constraints"):
		return "policy"
	case strings.Contains(name, "architecture-decision") || strings.Contains(name, "adr"):
		return "policy"
	case strings.Contains(name, "implementation") || strings.Contains(name, "code"):
		return "output"
	case strings.Contains(name, "findings") || strings.Contains(name, "report"):
		return "output"
	case strings.Contains(name, "generated") || strings.Contains(name, "cache"):
		return "runtime"
	}
	return ""
}

var wpArtifactRe = regexp.MustCompile(`(?i)^WP\d{2,}`)

// hashWithValidation is hash_file_with_validation: (hash, error_reason).
func hashWithValidation(p string) (string, string) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", "unreadable"
	}
	if !utf8.Valid(b) {
		return "", "invalid_utf8"
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]), ""
}

// hashIndexed is the indexer's per-file hash: WP files hash their
// WPMetadata static projection (fail closed on a schema failure, raw bytes
// when there is no parseable frontmatter).
func hashIndexed(p string) (string, string, error) {
	base := filepath.Base(p)
	ext := filepath.Ext(base)
	if strings.EqualFold(ext, ".md") && wpArtifactRe.MatchString(strings.TrimSuffix(base, ext)) {
		h, verr, ok := dossier.WPProjectionHash(p)
		if te, isType := verr.(*dossier.PyTypeError); isType {
			return "", "", te
		}
		if verr != nil {
			warnf("WP %s frontmatter fails schema (%s); marking non-present (fail-closed)", p, verr)
			return "", "wp frontmatter invalid: " + verr.Error(), nil
		}
		if ok {
			return h, "", nil
		}
	}
	h, reason := hashWithValidation(p)
	return h, reason, nil
}

// scanFeature is Indexer._scan_directory: every non-hidden regular file
// below dir (rglob order approximated by a depth-first walk in name order).
func scanFeature(dir string) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// indexFeature is Indexer(ManifestRegistry(), repo_root).index_feature
// followed by compute_snapshot, rendered as the snapshot's model_dump.
func indexFeature(featureDir, missionType, repoRoot string) (*jsonx.Object, string, error) {
	var arts []indexedArtifact
	for _, p := range scanFeature(featureDir) {
		rel, _ := filepath.Rel(featureDir, p)
		m, err := loadArtifactManifest(missionType, repoRoot)
		if err != nil {
			return nil, "", err
		}
		key := deriveArtifactKey(filepath.Base(p), m)
		class := classifyArtifact(rel, m)
		if class == "" {
			class = "output"
		}
		a := indexedArtifact{key: key, class: class, relPath: rel, required: m.requiredStatus(key)}
		h, reason, err := hashIndexed(p)
		if err != nil {
			return nil, "", err
		}
		if reason != "" {
			if st, err := os.Stat(p); err == nil {
				a.size = st.Size()
			}
			warnf("Artifact unreadable %s: %s", rel, reason)
			r := reason
			a.errorReason = &r
		} else {
			a.hash, a.present = h, true
			if st, err := os.Stat(p); err == nil {
				a.size = st.Size()
			}
		}
		if err := validateArtifactKey(key); err != nil {
			return nil, "", err
		}
		a.indexedAt = clock.Now().UTC()
		arts = append(arts, a)
	}
	m, err := loadArtifactManifest(missionType, repoRoot)
	if err != nil {
		return nil, "", err
	}
	if m != nil {
		for _, s := range m.requiredAlways {
			matched := false
			for _, a := range arts {
				if a.key == s.key {
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			req := "optional"
			if s.blocking {
				req = "required"
			}
			if err := validateArtifactKey(s.key); err != nil {
				return nil, "", err
			}
			nf := "not_found"
			arts = append(arts, indexedArtifact{key: s.key, class: s.class, relPath: s.pattern, required: req, errorReason: &nf, indexedAt: clock.Now().UTC()})
		}
	}
	return computeSnapshot(filepath.Base(featureDir), arts, m != nil)
}

// computeSnapshot is snapshot.compute_snapshot(...).model_dump().
func computeSnapshot(slug string, arts []indexedArtifact, hasManifest bool) (*jsonx.Object, string, error) {
	sorted := append([]indexedArtifact{}, arts...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
	reqN, reqPresent, optN, optPresent := 0, 0, 0, 0
	var entries []dossier.Entry
	var components []string
	summaries := []any{}
	for _, a := range sorted {
		switch a.required {
		case "required":
			reqN++
			if a.present {
				reqPresent++
			}
		case "optional":
			optN++
			if a.present {
				optPresent++
			}
		}
		var reason any
		if a.errorReason != nil {
			reason = *a.errorReason
		}
		summaries = append(summaries, jsonx.NewObject("artifact_key", a.key, "artifact_class", a.class, "relative_path", a.relPath,
			"content_hash_sha256", a.hash, "size_bytes", a.size, "wp_id", nil, "step_id", nil, "required_status", a.required,
			"is_present", a.present, "error_reason", reason, "indexed_at", clock.ISO(a.indexedAt), "provenance", nil))
	}
	for _, a := range arts {
		if a.present && a.hash != "" {
			entries = append(entries, dossier.Entry{Path: a.relPath, Hash: a.hash})
			components = append(components, a.hash)
		}
	}
	sort.Strings(components)
	if components == nil {
		components = []string{}
	}
	status := "unknown"
	if hasManifest {
		status = "incomplete"
		if reqN-reqPresent == 0 {
			status = "complete"
		}
	}
	parity := dossier.SnapshotHash(entries)
	comps := make([]any, len(components))
	for i, c := range components {
		comps[i] = c
	}
	snap := jsonx.NewObject("mission_slug", slug, "snapshot_id", kittify.NewUUID4(), "total_artifacts", len(sorted),
		"required_artifacts", reqN, "required_present", reqPresent, "required_missing", reqN-reqPresent,
		"optional_artifacts", optN, "optional_present", optPresent, "completeness_status", status,
		"parity_hash_sha256", parity, "parity_hash_components", comps, "artifact_summaries", summaries,
		"computed_at", strings.Replace(clock.ISO(clock.Now().UTC()), "T", " ", 1))
	return snap, parity, nil
}

// reindexSnapshot is the re-index arm of rebaseline_snapshot_file: the
// fresh snapshot payload and its canonical hash.
func reindexSnapshot(featureDir string) (*jsonx.Object, string, error) {
	missionType := missionart.MissionTypeFor(featureDir)
	repoRoot := ""
	if filepath.Base(filepath.Dir(featureDir)) == "kitty-specs" {
		repoRoot = filepath.Dir(filepath.Dir(featureDir))
	}
	return indexFeature(featureDir, missionType, repoRoot)
}
