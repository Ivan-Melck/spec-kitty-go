package charter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports the in-memory half of the charter synthesizer
// (charter.activation.synthesizer: request, interview_mapping, targets,
// generated/fixture adapters, synthesize_pipeline.run_all).

// ------------------------------------------------------------------ errors

// SynthError is a SynthesisError subclass: Type is the Python class name
// (the error panel title), Msg its str().
type SynthError struct {
	Type, Msg string
	// StagingDir is set for NeutralityGateViolation.
	StagingDir string
	// Raw / Candidates are set for TopicSelectorUnresolvedError.
	Raw        string
	Candidates []string
}

func (e *SynthError) Error() string { return e.Msg }

// DRGLoadError is charter.offering.drg.loader.DRGLoadError raised while
// reconciling against an unparseable on-disk overlay.
type DRGLoadError struct{ Msg string }

func (e *DRGLoadError) Error() string { return e.Msg }

func projectDRGError(errs []string, summary string) *SynthError {
	return &SynthError{Type: "ProjectDRGValidationError", Msg: fmt.Sprintf("Project DRG validation failed (%d error(s)): %s. Graph summary: %s",
		len(errs), strings.Join(errs, "; "), summary)}
}

// ------------------------------------------------------------------ evidence

// SynthEvidence is EvidenceBundle (timestamps dropped: they never reach
// any output).
type SynthEvidence struct {
	Code    *CodeSignals
	URLs    []string
	Corpus  *CorpusSnapshot
	Skipped bool
}

// CorpusSnapshot is evidence.CorpusSnapshot.
type CorpusSnapshot struct {
	SnapshotID, ProfileKey string
	Entries                []CorpusEntry
}

// CorpusEntry is evidence.CorpusEntry.
type CorpusEntry struct {
	Topic, Guidance string
	Tags            []string
}

func (e *SynthEvidence) empty() bool {
	return e == nil || e.Code == nil && len(e.URLs) == 0 && e.Corpus == nil
}

// LoadCorpusSnapshot is CorpusLoader().load(profile_key) with entries.
func LoadCorpusSnapshot(profileKey string) *CorpusSnapshot {
	primary, _, _ := strings.Cut(profileKey, "+")
	for _, c := range []string{primary, "generic"} {
		b, err := assets.ReadFile("charter/activation/corpus/" + c + ".corpus.yaml")
		if err != nil {
			continue
		}
		o, err := yamlx.ParseObject(b)
		if err != nil || o == nil {
			return nil
		}
		s := &CorpusSnapshot{SnapshotID: pyStrOf(mustGetAny(o, "snapshot_id")), ProfileKey: pyStrOf(mustGetAny(o, "profile_key"))}
		for _, x := range o.GetList("entries") {
			eo, ok := x.(*jsonx.Object)
			if !ok {
				continue
			}
			ce := CorpusEntry{Topic: pyStrOf(mustGetAny(eo, "topic")), Guidance: strings.TrimSpace(pyStrOf(mustGetAny(eo, "guidance")))}
			for _, t := range eo.GetList("tags") {
				ce.Tags = append(ce.Tags, pyStrOf(t))
			}
			s.Entries = append(s.Entries, ce)
		}
		return s
	}
	return nil
}

// CollectEvidence is EvidenceOrchestrator.collect (bundle + warnings).
func CollectEvidence(root string, skipCode, skipCorpus bool) (*SynthEvidence, []string, error) {
	urls, err := LoadURLListFromConfig(root)
	if err != nil {
		return nil, nil, err
	}
	ev := &SynthEvidence{URLs: urls}
	var warnings []string
	if !skipCode {
		cs := CollectCodeSignals(root)
		ev.Code = &cs
	}
	if !skipCorpus {
		key := "generic"
		if ev.Code != nil {
			key = ev.Code.StackID
		}
		ev.Corpus = LoadCorpusSnapshot(key)
		if ev.Corpus == nil {
			warnings = append(warnings, "No corpus found for profile '"+key+"' or 'generic'; synthesis proceeds without corpus evidence.")
		}
	}
	return ev, warnings, nil
}

func sortedCopy(l []string) []any {
	c := append([]string{}, l...)
	sort.Strings(c)
	return strs(c)
}

// evidenceJSONable is _evidence_to_jsonable.
func evidenceJSONable(e *SynthEvidence) *jsonx.Object {
	o := &jsonx.Object{}
	if e.Code != nil {
		o.Set("code_signals", jsonx.NewObject("frameworks", sortedCopy(e.Code.Frameworks), "primary_language", e.Code.PrimaryLanguage,
			"representative_files", sortedCopy(e.Code.Representative), "scope_tag", e.Code.PrimaryLanguage, "stack_id", e.Code.StackID,
			"test_frameworks", sortedCopy(e.Code.TestFramework)))
	}
	if len(e.URLs) > 0 {
		o.Set("url_list", sortedCopy(e.URLs))
	}
	if e.Corpus != nil {
		ents := append([]CorpusEntry{}, e.Corpus.Entries...)
		sort.SliceStable(ents, func(i, j int) bool { return ents[i].Topic < ents[j].Topic })
		var l []any
		for _, ce := range ents {
			l = append(l, jsonx.NewObject("guidance", ce.Guidance, "tags", sortedCopy(ce.Tags), "topic", ce.Topic))
		}
		if l == nil {
			l = []any{}
		}
		o.Set("corpus_snapshot", jsonx.NewObject("entries", l, "profile_key", e.Corpus.ProfileKey, "snapshot_id", e.Corpus.SnapshotID))
	}
	return o
}

// evidenceHashes is _compute_evidence_hashes.
func evidenceHashes(e *SynthEvidence) (*string, string) {
	if e.empty() {
		return nil, "(none)"
	}
	sum := sha256.Sum256([]byte(jsonx.Dumps(evidenceJSONable(e), jsonx.Line)))
	h := hex.EncodeToString(sum[:])
	corpus := "(none)"
	if e.Corpus != nil {
		corpus = e.Corpus.SnapshotID
	}
	return &h, corpus
}

// ------------------------------------------------------------------ targets

// SynthTarget is SynthesisTarget.
type SynthTarget struct {
	Kind, Slug, Title, ArtifactID string
	SourceSection                 *string
	SourceURNs                    []string
}

var (
	synSlugRe        = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	synDirectiveIDRe = regexp.MustCompile(`^[A-Z][A-Z0-9_-]*$`)
	synDigitsRe      = regexp.MustCompile(`\d+`)
)

func newSynthTarget(t SynthTarget) (SynthTarget, error) {
	if !synSlugRe.MatchString(t.Slug) {
		return t, fmt.Errorf("Invalid slug '%s': must match ^[a-z][a-z0-9-]*$", t.Slug)
	}
	if t.Kind == "directive" {
		if !synDirectiveIDRe.MatchString(t.ArtifactID) {
			return t, fmt.Errorf("Invalid directive artifact_id '%s': must match ^[A-Z][A-Z0-9_-]*$", t.ArtifactID)
		}
		if strings.HasPrefix(t.ArtifactID, "DIRECTIVE_") {
			return t, fmt.Errorf("Directive artifact_id '%s' must not start with 'DIRECTIVE_' — that namespace is reserved for built-in directives. Use PROJECT_<NNN> or a semantic prefix.", t.ArtifactID)
		}
	}
	if (t.SourceSection == nil || *t.SourceSection == "") && len(t.SourceURNs) == 0 {
		return t, fmt.Errorf("SynthesisTarget(%s:%s): at least one of source_section or source_urns must be non-empty.", t.Kind, t.Slug)
	}
	return t, nil
}

// URN is SynthesisTarget.urn.
func (t SynthTarget) URN() string { return t.Kind + ":" + t.ArtifactID }

// Filename is SynthesisTarget.filename.
func (t SynthTarget) Filename() string {
	switch t.Kind {
	case "directive":
		nnn := synDigitsRe.FindString(t.ArtifactID)
		if nnn == "" {
			nnn = "000"
		}
		return nnn + "-" + t.Slug + ".directive.yaml"
	case "tactic":
		return t.Slug + ".tactic.yaml"
	case "styleguide":
		return t.Slug + ".styleguide.yaml"
	}
	return ""
}

// extractDirectiveNumber is artifact_naming.extract_directive_number.
func extractDirectiveNumber(id string) string {
	n := len(id)
	i := 0
	for i < n {
		start := i
		for i < n && id[i] >= 'A' && id[i] <= 'Z' {
			i++
		}
		if i == start || i >= n || id[i] != '_' {
			i = start + 1
			continue
		}
		ds := i + 1
		de := ds
		for de < n && id[de] >= '0' && id[de] <= '9' {
			de++
		}
		if de > ds {
			d := id[ds:de]
			for len(d) < 3 {
				d = "0" + d
			}
			return d
		}
		i = ds
	}
	return "000"
}

// ArtifactFilename is artifact_naming.artifact_filename.
func ArtifactFilename(kind, slug, artifactID string) string {
	switch kind {
	case "directive":
		return extractDirectiveNumber(artifactID) + "-" + slug + ".directive.yaml"
	case "tactic":
		return slug + ".tactic.yaml"
	case "styleguide":
		return slug + ".styleguide.yaml"
	}
	return ""
}

// ----------------------------------------------------------- interview map

type interviewMapping struct {
	label            string
	kinds            []string
	requiresNonempty bool
}

var interviewMappings = []interviewMapping{
	{"mission_type", []string{"directive"}, false},
	{"testing_philosophy", []string{"tactic", "styleguide"}, true},
	{"neutrality_posture", []string{"directive"}, true},
	{"risk_appetite", []string{"directive"}, true},
	{"quality_gates", []string{"styleguide"}, true},
	{"review_policy", []string{"tactic"}, true},
	{"documentation_policy", []string{"styleguide"}, true},
}

var interviewAliases = []struct {
	section string
	aliases []string
}{
	{"mission_type", []string{"project_intent"}},
	{"testing_philosophy", []string{"testing_requirements"}},
	{"risk_appetite", []string{"risk_boundaries"}},
	{"language_scope", []string{"languages_frameworks"}},
}

func aliasesFor(section string) []string {
	for _, a := range interviewAliases {
		if a.section == section {
			return a.aliases
		}
	}
	return nil
}

func normalizeSectionSelector(label string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(label), "-", "_"), " ", "_")
}

// CanonicalizeInterviewSectionLabel is canonicalize_interview_section_label.
func CanonicalizeInterviewSectionLabel(label string) string {
	n := normalizeSectionSelector(label)
	for _, a := range interviewAliases {
		for _, al := range a.aliases {
			if al == n {
				return a.section
			}
		}
	}
	return n
}

func builtinMissionTypeIDs() []string {
	var ids []string
	for id := range dct.MissionTypeRoster("") {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func missionIdentifierAnswer(normalized string) (string, bool) {
	for _, id := range builtinMissionTypeIDs() {
		if strings.ReplaceAll(id, "-", "_") == normalized {
			return "mission_type:" + id, true
		}
	}
	return "", false
}

func sectionAnswerWithSource(snap *jsonx.Object, label string) (string, string) {
	keys := append([]string{label}, aliasesFor(label)...)
	if label == "mission_type" {
		if c, ok := mustGetAny(snap, label).(string); ok {
			if _, hit := missionIdentifierAnswer(normalizeSectionSelector(c)); hit {
				keys = append(append([]string{}, aliasesFor(label)...), label)
			}
		}
	}
	for _, k := range keys {
		if s, ok := mustGetAny(snap, k).(string); ok {
			if a := strings.TrimSpace(s); a != "" {
				return a, k
			}
		}
	}
	return "", label
}

func iterCleanStrings(raw any) []string {
	switch t := raw.(type) {
	case string:
		if c := strings.TrimSpace(t); c != "" {
			return []string{c}
		}
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	return nil
}

func realLanguages(vals []string) []string {
	var out []string
	for _, v := range vals {
		if !isPlaceholderLanguageAnswer(v) {
			out = append(out, v)
		}
	}
	return out
}

func extractLanguagesFromAlias(raw any) []string {
	if s, ok := raw.(string); ok {
		return ExtractDeclaredLanguages(s)
	}
	return realLanguages(iterCleanStrings(raw))
}

func objDelete(o *jsonx.Object, key string) *jsonx.Object {
	if _, ok := o.Get(key); !ok {
		return o
	}
	n := &jsonx.Object{}
	for _, kv := range o.Pairs() {
		if kv.Key != key {
			n.Set(kv.Key, kv.Value)
		}
	}
	return n
}

// NormalizeInterviewSnapshot is normalize_interview_snapshot.
func NormalizeInterviewSnapshot(snap *jsonx.Object) *jsonx.Object {
	n := snap.Clone()
	for _, a := range interviewAliases {
		if a.section == "language_scope" {
			continue
		}
		answer, src := sectionAnswerWithSource(n, a.section)
		if answer != "" && src != a.section {
			n.Set(a.section, answer)
		}
		if answer != "" {
			for _, al := range a.aliases {
				n = objDelete(n, al)
			}
		}
	}
	langs := realLanguages(iterCleanStrings(mustGetAny(n, "language_scope")))
	if len(langs) == 0 {
		if v, ok := n.Get("languages_frameworks"); ok {
			langs = extractLanguagesFromAlias(v)
			if len(langs) > 0 {
				n.Set("language_scope", strs(langs))
			}
		}
	}
	if len(langs) > 0 {
		n = objDelete(n, "languages_frameworks")
	}
	return n
}

type sectionCtx struct {
	label, answer, sourceSection, answerSource string
	kinds                                      []string
	sourceURNs                                 []string
	directiveID, language                      string
}

// resolveSections is interview_mapping.resolve_sections.
func resolveSections(snap *jsonx.Object) []sectionCtx {
	var out []sectionCtx
	for _, m := range interviewMappings {
		answer, src := sectionAnswerWithSource(snap, m.label)
		if m.requiresNonempty && answer == "" {
			continue
		}
		out = append(out, sectionCtx{label: m.label, answer: answer, kinds: m.kinds, sourceSection: m.label, answerSource: src})
	}
	for _, d := range iterCleanStrings(mustGetAny(snap, "selected_directives")) {
		out = append(out, sectionCtx{label: "selected_directives", answer: d, kinds: []string{"tactic"}, sourceSection: "selected_directives",
			sourceURNs: []string{"directive:" + d}, directiveID: d})
	}
	src := "language_scope"
	langs := realLanguages(iterCleanStrings(mustGetAny(snap, "language_scope")))
	if len(langs) == 0 {
		src = "languages_frameworks"
		v, ok := snap.Get("languages_frameworks")
		if !ok {
			v = ""
		}
		langs = extractLanguagesFromAlias(v)
	}
	for _, l := range langs {
		ll := strings.ToLower(l)
		out = append(out, sectionCtx{label: "language_scope", answer: ll, kinds: []string{"styleguide"}, sourceSection: "language_scope", answerSource: src, language: ll})
	}
	return out
}

var kebabRe = regexp.MustCompile(`[^a-z0-9]+`)
var dashesRe = regexp.MustCompile(`-{2,}`)

func kebab(s string) string {
	k := dashesRe.ReplaceAllString(strings.Trim(kebabRe.ReplaceAllString(strings.ToLower(s), "-"), "-"), "-")
	if k == "" {
		return "unknown"
	}
	return k
}

func slugForSection(c sectionCtx, kind string) string {
	switch c.label {
	case "selected_directives":
		return "how-we-apply-" + kebab(c.directiveID)
	case "language_scope":
		return kebab(c.language) + "-style-guide"
	case "testing_philosophy":
		if kind == "tactic" {
			return "testing-philosophy-tactic"
		}
		if kind == "styleguide" {
			return "testing-style-guide"
		}
	case "mission_type":
		return "mission-type-scope-directive"
	case "neutrality_posture":
		return "neutrality-posture-directive"
	case "risk_appetite":
		return "risk-appetite-directive"
	case "quality_gates":
		return "quality-gates-style-guide"
	case "review_policy":
		return "review-policy-tactic"
	case "documentation_policy":
		return "documentation-policy-style-guide"
	}
	return kebab(c.label) + "-" + kind
}

// pyCapitalize is str.capitalize.
func pyCapitalize(s string) string {
	r := []rune(strings.ToLower(s))
	if len(r) > 0 {
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	}
	return string(r)
}

// pyTitle is str.title.
func pyTitle(s string) string {
	var b strings.Builder
	prevLetter := false
	for _, r := range s {
		isL := strings.ToUpper(string(r)) != strings.ToLower(string(r))
		if isL {
			if prevLetter {
				b.WriteString(strings.ToLower(string(r)))
			} else {
				b.WriteString(strings.ToUpper(string(r)))
			}
		} else {
			b.WriteRune(r)
		}
		prevLetter = isL
	}
	return b.String()
}

func titleForSection(c sectionCtx, kind string) string {
	switch c.label {
	case "selected_directives":
		return "How We Apply " + c.directiveID
	case "language_scope":
		return pyCapitalize(c.language) + " Style Guide"
	}
	return pyTitle(strings.ReplaceAll(c.label, "_", " ")) + " " + pyCapitalize(kind)
}

func drgSnapshotURNs(req *SynthesisRequest) map[string]bool {
	out := map[string]bool{}
	for _, u := range req.DRGNodes {
		out[u] = true
	}
	return out
}

// buildTargets is targets.build_targets + order_targets + detect_duplicates
// (falling back to the placeholder target when the interview yields none).
func buildTargets(req *SynthesisRequest, snap *jsonx.Object) ([]SynthTarget, error) {
	known := drgSnapshotURNs(req)
	var targets []SynthTarget
	dirIdx := 0
	for _, c := range resolveSections(snap) {
		urns := c.sourceURNs
		if len(urns) == 0 && c.label == "mission_type" && c.answer != "" {
			if cand, ok := missionIdentifierAnswer(normalizeSectionSelector(c.answer)); ok && known[cand] {
				urns = []string{cand}
			}
		}
		if len(urns) > 0 {
			var dangling []string
			for _, u := range urns {
				if !known[u] {
					dangling = append(dangling, u)
				}
			}
			if len(dangling) > 0 {
				label := c.label + ":" + pyListRepr(c.kinds)
				var errs []string
				for _, u := range dangling {
					errs = append(errs, fmt.Sprintf("Source URN '%s' referenced by target '%s' does not exist in the built-in DRG snapshot.", u, label))
				}
				return nil, projectDRGError(errs, fmt.Sprintf("%d known URNs; %d dangling reference(s): %s", len(known), len(dangling), strings.Join(dangling, ", ")))
			}
		}
		for _, kind := range c.kinds {
			slug := slugForSection(c, kind)
			id := slug
			if kind == "directive" {
				dirIdx++
				id = fmt.Sprintf("PROJECT_%03d", dirIdx)
			}
			var ss *string
			if c.sourceSection != "" {
				s := c.sourceSection
				ss = &s
			}
			t, err := newSynthTarget(SynthTarget{Kind: kind, Slug: slug, Title: titleForSection(c, kind), ArtifactID: id, SourceSection: ss, SourceURNs: urns})
			if err != nil {
				return nil, err
			}
			targets = append(targets, t)
		}
	}
	order := map[string]int{"directive": 0, "tactic": 1, "styleguide": 2}
	rank := func(k string) int {
		if r, ok := order[k]; ok {
			return r
		}
		return 99
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if rank(targets[i].Kind) != rank(targets[j].Kind) {
			return rank(targets[i].Kind) < rank(targets[j].Kind)
		}
		return targets[i].Slug < targets[j].Slug
	})
	seen := map[string]int{}
	var keys []string
	for _, t := range targets {
		k := t.Kind + ":" + t.Slug
		if seen[k] == 0 {
			keys = append(keys, k)
		}
		seen[k]++
	}
	for _, k := range keys {
		if seen[k] > 1 {
			return nil, &SynthError{Type: "DuplicateTargetError", Msg: fmt.Sprintf("Duplicate synthesis target: %s appears %d time(s) in a single run. Each (kind, slug) pair must be unique within a run.", k, seen[k])}
		}
	}
	if len(targets) == 0 {
		targets = []SynthTarget{req.Placeholder}
	}
	return targets, nil
}

// ------------------------------------------------------------------ hashing

func toJSONable(v any) any {
	switch t := v.(type) {
	case *jsonx.Object:
		keys := make([]string, 0, t.Len())
		for _, kv := range t.Pairs() {
			keys = append(keys, kv.Key)
		}
		sort.Strings(keys)
		o := &jsonx.Object{}
		for _, k := range keys {
			x, _ := t.Get(k)
			o.Set(k, toJSONable(x))
		}
		return o
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = toJSONable(x)
		}
		return l
	case float64:
		return jsonx.FormatFloat(t)
	}
	return v
}

func strPtrAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// computeInputsHash is request.compute_inputs_hash.
func computeInputsHash(req *SynthesisRequest, t SynthTarget, snap *jsonx.Object, adapterID, adapterVersion string) string {
	n := jsonx.NewObject("adapter_id", adapterID, "adapter_version", adapterVersion,
		"target", jsonx.NewObject("artifact_id", t.ArtifactID, "kind", t.Kind, "slug", t.Slug, "source_section", strPtrAny(t.SourceSection),
			"source_urns", sortedCopy(t.SourceURNs), "title", t.Title),
		"interview_snapshot", toJSONable(snap), "doctrine_snapshot", toJSONable(req.DoctrineSnapshot()),
		"drg_snapshot", toJSONable(req.DRGSnapshot()), "adapter_hints", nil)
	if !req.Evidence.empty() {
		n.Set("evidence", evidenceJSONable(req.Evidence))
	}
	sum := sha256.Sum256([]byte(jsonx.Dumps(n, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true, Compact: true})))
	return hex.EncodeToString(sum[:])
}

// ------------------------------------------------------------------ adapters

type adapterOutput struct {
	body        *jsonx.Object
	generatedAt string
	notes       string
}

// SynthAdapter is the adapter seam (generated / fixture).
type SynthAdapter struct {
	ID, Version string
	root        string
}

// NewSynthAdapter returns the adapter for name ("" when unknown).
func NewSynthAdapter(root, name string) *SynthAdapter {
	switch name {
	case "generated":
		return &SynthAdapter{ID: "generated", Version: "1.0.0", root: root}
	case "fixture":
		return &SynthAdapter{ID: "fixture", Version: "1.0.0", root: root}
	}
	return nil
}

// pyISO is datetime.isoformat() of an aware UTC time (microseconds
// omitted when zero).
func pyISO(t time.Time) string {
	t = t.UTC()
	if t.Nanosecond()/1000 == 0 {
		return t.Format("2006-01-02T15:04:05") + "+00:00"
	}
	return t.Format("2006-01-02T15:04:05.000000") + "+00:00"
}

func nowUTCISO() string { return pyISO(time.Now().Round(time.Microsecond)) }

func nowUTCSeconds() string { return time.Now().UTC().Format("2006-01-02T15:04:05") + "+00:00" }

// FixtureRoot is the fixture adapter's root (tests/charter/fixtures/synthesizer
// beside the upstream source tree; overridable for offline tests).
func FixtureRoot() string {
	if v := os.Getenv("SPEC_KITTY_SYNTH_FIXTURE_ROOT"); v != "" {
		return v
	}
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "tests", "charter", "fixtures", "synthesizer")
}

func (a *SynthAdapter) generate(req *SynthesisRequest, t SynthTarget, snap *jsonx.Object) (*adapterOutput, error) {
	if a.ID == "fixture" {
		full := computeInputsHash(req, t, snap, a.ID, a.Version)
		p := filepath.Join(FixtureRoot(), t.Kind, t.Slug, full[:12]+"."+t.Kind+".yaml")
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, &SynthError{Type: "FixtureAdapterMissingError", Msg: fmt.Sprintf("No fixture found for %s:%s (inputs_hash=%s...); expected at %s", t.Kind, t.Slug, full[:12], p)}
		}
		o, err := yamlx.ParseObject(b)
		if err != nil {
			return nil, err
		}
		off, _ := strconv.ParseInt(full[:8], 16, 64)
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(off) * time.Microsecond)
		return &adapterOutput{body: o, generatedAt: pyISO(at), notes: "fixture:" + full[:12]}, nil
	}
	p := filepath.Join(a.root, ".kittify", "charter", "generated", dct.Kind(t.Kind).Plural(), t.Filename())
	st, err := os.Stat(p)
	if err != nil {
		return nil, &SynthError{Type: "GeneratedArtifactMissingError", Msg: fmt.Sprintf("No generated artifact found for %s:%s; expected YAML at %s with id '%s'. Write the agent-authored artifact there, then rerun synthesis.",
			t.Kind, t.Slug, p, t.ArtifactID)}
	}
	loadErr := func(reason string, expected, actual *string) error {
		msg := fmt.Sprintf("Generated artifact at %s could not be loaded: %s", p, reason)
		if expected != nil {
			msg += fmt.Sprintf(". Expected id '%s'", *expected)
			if actual != nil {
				msg += fmt.Sprintf(", got '%s'", *actual)
			}
		}
		return &SynthError{Type: "GeneratedArtifactLoadError", Msg: msg}
	}
	b, _ := os.ReadFile(p)
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, loadErr("invalid YAML: "+err.Error(), nil, nil)
	}
	body, ok := v.(*jsonx.Object)
	if !ok {
		return nil, loadErr("top-level YAML document must be a mapping", nil, nil)
	}
	exp := t.ArtifactID
	raw, ok := mustGetAny(body, "id").(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, loadErr("artifact body must include a non-empty 'id' field", &exp, nil)
	}
	if act := strings.TrimSpace(raw); act != exp {
		return nil, loadErr("artifact id mismatch", &exp, &act)
	}
	note := p
	if rel, err := filepath.Rel(a.root, p); err == nil && !strings.HasPrefix(rel, "..") {
		note = filepath.ToSlash(rel)
	}
	return &adapterOutput{body: body, generatedAt: pyISO(st.ModTime().Round(time.Microsecond)), notes: "generated-artifact:" + note}, nil
}

// ------------------------------------------------------------------ run_all

// ProvEntry is synthesize_pipeline.ProvenanceEntry.
type ProvEntry struct {
	URN, Kind, Slug, ContentHash, InputsHash, AdapterID, AdapterVersion string
	SynthesizerVersion                                                 string
	SourceSection                                                      *string
	SourceURNs, SourceInputIDs                                         []string
	GeneratedAt, ProducedAt, CorpusSnapshotID, RunID                   string
	EvidenceHash, Notes                                                *string
}

// Object is model_dump(mode="python") (field order).
func (p *ProvEntry) Object() *jsonx.Object {
	return jsonx.NewObject("schema_version", "2", "artifact_urn", p.URN, "artifact_kind", p.Kind, "artifact_slug", p.Slug,
		"artifact_content_hash", p.ContentHash, "inputs_hash", p.InputsHash, "adapter_id", p.AdapterID, "adapter_version", p.AdapterVersion,
		"synthesizer_version", p.SynthesizerVersion, "source_section", strPtrAny(p.SourceSection), "source_urns", strs(nz(p.SourceURNs)),
		"source_input_ids", strs(nz(p.SourceInputIDs)), "generated_at", p.GeneratedAt, "produced_at", p.ProducedAt,
		"corpus_snapshot_id", p.CorpusSnapshotID, "synthesis_run_id", p.RunID, "evidence_bundle_hash", strPtrAny(p.EvidenceHash),
		"adapter_notes", strPtrAny(p.Notes))
}

// ArtifactID is write_pipeline._artifact_id_from_provenance ("" for
// non-directives).
func (p *ProvEntry) ArtifactID() (string, error) {
	if p.Kind != "directive" {
		return "", nil
	}
	pre, id, ok := strings.Cut(p.URN, ":")
	if pre != "directive" || !ok || id == "" {
		return "", fmt.Errorf("Directive provenance must carry artifact_urn='directive:<artifact_id>'")
	}
	if id == "PROJECT_000" {
		return "", fmt.Errorf("Directive provenance must not surface PROJECT_000")
	}
	return id, nil
}

// SynthResult is one (body, provenance) pair.
type SynthResult struct {
	Body *jsonx.Object
	Prov *ProvEntry
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pydErrDictRepr renders str(err) for one pydantic ErrorDetails dict.
func pydErrDictRepr(e pydErr) string {
	var loc []string
	if e.loc != "" {
		for _, part := range strings.Split(e.loc, ".") {
			if _, err := strconv.Atoi(part); err == nil {
				loc = append(loc, part)
			} else {
				loc = append(loc, dct.PyRepr(part))
			}
		}
	}
	locRepr := "(" + strings.Join(loc, ", ")
	if len(loc) == 1 {
		locRepr += ","
	}
	locRepr += ")"
	out := "{'type': " + dct.PyRepr(e.typ) + ", 'loc': " + locRepr + ", 'msg': " + dct.PyRepr(e.msg) + ", 'input': " + pyValueRepr(e.input)
	if ctx := pydErrCtx(e); ctx != "" {
		out += ", 'ctx': " + ctx
	}
	return out + ", 'url': 'https://errors.pydantic.dev/2.14/v/" + e.typ + "'}"
}

var (
	reCtxPattern = regexp.MustCompile(`^String should match pattern '(.*)'$`)
	reCtxEnum    = regexp.MustCompile(`^Input should be (.*)$`)
	reCtxMinStr  = regexp.MustCompile(`^String should have at least (\d+) character`)
	reCtxTooShrt = regexp.MustCompile(`^List should have at least (\d+) items? after validation, not (\d+)$`)
	reCtxNum     = regexp.MustCompile(`than(?: or equal to)? (-?\d+)$`)
)

func pydErrCtx(e pydErr) string {
	switch e.typ {
	case "string_pattern_mismatch":
		if m := reCtxPattern.FindStringSubmatch(e.msg); m != nil {
			return "{'pattern': " + dct.PyRepr(m[1]) + "}"
		}
	case "enum":
		if m := reCtxEnum.FindStringSubmatch(e.msg); m != nil {
			return "{'expected': " + dct.PyRepr(m[1]) + "}"
		}
	case "string_too_short":
		if m := reCtxMinStr.FindStringSubmatch(e.msg); m != nil {
			return "{'min_length': " + m[1] + "}"
		}
	case "too_short":
		if m := reCtxTooShrt.FindStringSubmatch(e.msg); m != nil {
			return "{'field_type': 'List', 'min_length': " + m[1] + ", 'actual_length': " + m[2] + "}"
		}
	case "greater_than_equal", "less_than_equal", "greater_than":
		key := map[string]string{"greater_than_equal": "ge", "less_than_equal": "le", "greater_than": "gt"}[e.typ]
		if m := reCtxNum.FindStringSubmatch(e.msg); m != nil {
			return "{'" + key + "': " + m[1] + "}"
		}
	}
	return ""
}

// assertSchema is synthesize_pipeline._assert_schema.
func assertSchema(t SynthTarget, body *jsonx.Object) error {
	m := schemaModels[dct.Kind(t.Kind).Plural()]
	if m == nil {
		return &SynthError{Type: "SynthesisSchemaError", Msg: fmt.Sprintf("Adapter output for %s:%s failed schema validation: Unknown artifact kind: %s", t.Kind, t.Slug, dct.PyRepr(t.Kind))}
	}
	var errs []pydErr
	validateModel(body, m, "", &errs)
	if len(errs) == 0 {
		return nil
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = pydErrDictRepr(e)
	}
	return &SynthError{Type: "SynthesisSchemaError", Msg: fmt.Sprintf("Adapter output for %s:%s failed schema validation: %s", t.Kind, t.Slug, strings.Join(parts, "; "))}
}

// RunAll is synthesize_pipeline.run_all: targets, adapter dispatch,
// schema gate and in-memory provenance.
func RunAll(req *SynthesisRequest, a *SynthAdapter, version string) ([]SynthResult, error) {
	snap := NormalizeInterviewSnapshot(req.InterviewSnapshot)
	targets, err := buildTargets(req, snap)
	if err != nil {
		return nil, err
	}
	outs := make([]*adapterOutput, len(targets))
	for i, t := range targets {
		o, err := a.generate(req, t, snap)
		if err != nil {
			return nil, err
		}
		outs[i] = o
	}
	evHash, corpus := evidenceHashes(req.Evidence)
	var results []SynthResult
	for i, t := range targets {
		if err := assertSchema(t, outs[i].body); err != nil {
			return nil, err
		}
		notes := outs[i].notes
		results = append(results, SynthResult{Body: outs[i].body, Prov: &ProvEntry{
			URN: t.URN(), Kind: t.Kind, Slug: t.Slug, ContentHash: sha256Hex(canonicalYAML(outs[i].body)),
			InputsHash: computeInputsHash(req, t, snap, a.ID, a.Version), AdapterID: a.ID, AdapterVersion: a.Version,
			SynthesizerVersion: version, SourceSection: t.SourceSection, SourceURNs: t.SourceURNs, SourceInputIDs: t.SourceURNs,
			GeneratedAt: outs[i].generatedAt, ProducedAt: nowUTCISO(), CorpusSnapshotID: corpus, RunID: req.RunID,
			EvidenceHash: evHash, Notes: &notes,
		}})
	}
	return results, nil
}
