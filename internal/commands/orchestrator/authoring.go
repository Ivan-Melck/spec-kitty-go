package orchestrator

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/decision"
	"github.com/spec-kitty/go-kitty3/internal/fm"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/lanes"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missionart"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/reqmap"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

var (
	specifyQuestions = [][2]string{
		{"problem_statement", "What problem does this Mission solve?"},
		{"success_criteria", "How will we know this Mission is successful?"},
		{"scope_boundaries", "What is explicitly out of scope for this Mission?"},
	}
	planQuestions = [][2]string{
		{"approach", "What is the high-level implementation approach?"},
		{"risks", "What are the main risks or unknowns?"},
		{"dependencies", "What upstream dependencies does this plan rely on?"},
	}
)

// authoringKinds maps the closed authoring kinds to mission artifact kinds
// (all PRIMARY-partition).
var authoringKinds = map[string]string{
	"specification": mission.KindSpec,
	"plan":          mission.KindPlan,
	"outline":       mission.KindTasksIndex,
	"work_package":  mission.KindWorkPackageTask,
	"research":      mission.KindResearch,
	"data_model":    mission.KindDataModel,
	"quickstart":    mission.KindChecklist,
	"contract":      mission.KindContract,
}

var manifestKeys = map[string]string{
	"specification": "input.spec.main",
	"plan":          "output.plan.main",
	"research":      "evidence.research",
	"data_model":    "evidence.data-model",
	"quickstart":    "evidence.quickstart",
}

var (
	safeNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	wpNNRe     = regexp.MustCompile(`^WP[0-9]{2}$`)
)

// design is the governed-planning authoring service bound to one mission
// (specify_cli.design.*).
type design struct {
	m    *orch.Mission
	root string
	slug string
}

func newDesign(m *orch.Mission) *design { return &design{m: m, root: m.Root, slug: m.Slug} }

func derr(code, msg string, pairs ...any) error { return orch.NewDesignError(code, msg, pairs...) }

func (d *design) missionType() string { return missionart.MissionTypeFor(d.m.PrimaryDir) }

// configuredArtifactName is resolve_configured_artifact_name over the
// mission type's expected-artifacts manifest.
func configuredArtifactName(key, missionType string) (string, bool) {
	if orch.UnsafeSegmentReason(missionType) != "" {
		return "", false
	}
	b, err := assets.ReadFile("packs/built-in/missions/" + missionType + "/expected-artifacts.yaml")
	if err != nil {
		return "", false
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return "", false
	}
	var found string
	visit := func(v any) {
		for _, x := range listOf(v) {
			e := jsonx.ToObject(x)
			if e == nil || found != "" {
				continue
			}
			if e.GetString("artifact_key") == key {
				found = e.GetString("path_pattern")
			}
		}
	}
	visit(mustGet(o, "required_always"))
	if rb := jsonx.ToObject(mustGet(o, "required_by_step")); rb != nil {
		for _, kv := range rb.Pairs() {
			visit(kv.Value)
		}
	}
	visit(mustGet(o, "optional_always"))
	if ob := jsonx.ToObject(mustGet(o, "optional_by_step")); ob != nil {
		for _, kv := range ob.Pairs() {
			visit(kv.Value)
		}
	}
	return found, found != ""
}

func listOf(v any) []any {
	l, _ := v.([]any)
	return l
}

func safeFilename(v string) (string, error) {
	if !safeNameRe.MatchString(v) || strings.Contains(v, "..") {
		return "", derr("DESIGN_PATH_REFUSED", "Artifact identifiers must be a single safe basename")
	}
	return v, nil
}

func manifestPromptName(m *orch.WpsManifest, wpID string) (string, error) {
	var hits []*orch.WPEntry
	for _, e := range m.WorkPackages {
		if e.ID == wpID {
			hits = append(hits, e)
		}
	}
	if len(hits) != 1 {
		return "", derr("DESIGN_PREREQUISITES_FAILED", "The package is not uniquely declared in wps.yaml")
	}
	name := "tasks/" + wpID + ".md"
	if hits[0].PromptFile != nil && *hits[0].PromptFile != "" {
		name = *hits[0].PromptFile
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(name)), "/")
	if len(parts) != 2 || parts[0] != "tasks" || !strings.HasPrefix(parts[1], wpID) || !strings.HasSuffix(parts[1], ".md") {
		return "", derr("DESIGN_PATH_REFUSED", "Manifest prompt_file must name its own tasks/WPnn*.md file")
	}
	if _, err := safeFilename(parts[1]); err != nil {
		return "", err
	}
	return name, nil
}

func (d *design) loadManifest() (*orch.WpsManifest, error) {
	m, err := orch.LoadWpsManifest(d.m.PrimaryDir)
	if err != nil {
		return nil, derr("DESIGN_PREREQUISITES_FAILED", "The existing outline is invalid")
	}
	return m, nil
}

func (d *design) filename(kind string, id *string, manifest *orch.WpsManifest) (string, error) {
	switch kind {
	case "outline":
		if id != nil {
			return "", derr("DESIGN_REQUEST_INVALID", "The outline has no selectable identifier")
		}
		return "wps.yaml", nil
	case "work_package":
		if id == nil || !wpNNRe.MatchString(*id) || manifest == nil {
			return "", derr("DESIGN_PREREQUISITES_FAILED", "WP submission requires a declared WPnn and outline")
		}
		return manifestPromptName(manifest, *id)
	case "contract":
		if id == nil {
			return "", derr("DESIGN_REQUEST_INVALID", "Contracts require an artifact_id basename")
		}
		n, err := safeFilename(*id)
		if err != nil {
			return "", err
		}
		return "contracts/" + n, nil
	}
	if id != nil {
		return "", derr("DESIGN_REQUEST_INVALID", "This artifact kind has no selectable identifier")
	}
	name, ok := configuredArtifactName(manifestKeys[kind], d.missionType())
	if !ok {
		return "", derr("DESIGN_ARTIFACT_UNSUPPORTED", "The mission type does not declare this artifact")
	}
	return name, nil
}

// artifactPath is design.authoring.artifact_path.
func (d *design) artifactPath(kind string, id *string, manifest *orch.WpsManifest) (string, error) {
	if _, ok := authoringKinds[kind]; !ok {
		return "", derr("DESIGN_ARTIFACT_UNSUPPORTED", "The artifact kind is not an authoring capability")
	}
	dir := d.m.PrimaryDir
	if kind == "work_package" && manifest == nil {
		m, err := d.loadManifest()
		if err != nil {
			return "", err
		}
		manifest = m
	}
	rel, err := d.filename(kind, id, manifest)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(rel) || containsDotDot(rel) {
		return "", derr("DESIGN_PATH_REFUSED", "The configured artifact location is unsafe")
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if rp, rd := resolvePath(p), resolvePath(dir); rp != rd && !strings.HasPrefix(rp, rd+string(filepath.Separator)) {
		return "", derr("DESIGN_PATH_REFUSED", "Artifact location escapes the mission")
	}
	for cur := p; cur != dir && strings.HasPrefix(cur, dir); cur = filepath.Dir(cur) {
		if st, err := os.Lstat(cur); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", derr("DESIGN_PATH_REFUSED", "Authoring does not follow artifact symlinks")
		}
	}
	return p, nil
}

func containsDotDot(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// resolvePath is Path.resolve() (non-strict): resolve the longest existing
// prefix through symlinks.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolvePath(parent), filepath.Base(p))
}

// readBounded is design.receipts.read_bounded.
func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, orch.MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > orch.MaxArtifactBytes {
		return nil, derr("DESIGN_BOUNDS_EXCEEDED", "Artifact content exceeds the inline read bound")
	}
	return b, nil
}

// contentDigest is design.receipts.content_digest.
func contentDigest(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return orch.Absent, nil
	}
	b, err := readBounded(path)
	if err != nil {
		return "", err
	}
	return orch.SHA256Hex(b), nil
}

func contentDigestOrAbsent(path string) string {
	d, err := contentDigest(path)
	if err != nil {
		return orch.Absent
	}
	return d
}

// readArtifact is design.authoring.read_artifact.
func (d *design) readArtifact(kind string, id *string) (*jsonx.Object, error) {
	orch.TouchAuthoringLock(d.root, d.slug)
	p, err := d.artifactPath(kind, id, nil)
	if err != nil {
		return nil, err
	}
	var content any
	digest := orch.Absent
	if _, err := os.Stat(p); err == nil {
		b, err := readBounded(p)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(b) {
			return nil, &unicodeErr{}
		}
		content = string(b)
		digest = orch.SHA256Hex(b)
	}
	var idv any
	if id != nil {
		idv = *id
	}
	return jsonx.NewObject("kind", kind, "artifact_id", idv, "sha256", digest, "content", content), nil
}

type unicodeErr struct{}

func (*unicodeErr) Error() string { return "UnicodeDecodeError" }

// ── interview status ──────────────────────────────────────────────────────

func questionsFor(stage string) [][2]string {
	switch stage {
	case "specify":
		return specifyQuestions
	case "plan":
		return planQuestions
	}
	return nil
}

func checkStage(stage string) error {
	if stage != "specify" && stage != "plan" && stage != "tasks" {
		return derr("DESIGN_CONTEXT_FAILED", "Undeclared design stage", "stage", stage)
	}
	return nil
}

type slot struct{ flow, key, question string }

func stageSlots(stage string) []slot {
	flows := []string{stage}
	if stage == "tasks" {
		flows = []string{"specify", "plan"}
	}
	var out []slot
	for _, f := range flows {
		for _, q := range questionsFor(f) {
			out = append(out, slot{f, q[0], q[1]})
		}
	}
	return out
}

func questionObjects(qs []slot, tasks bool) []any {
	out := []any{}
	for _, q := range qs {
		id := q.key
		if tasks {
			id = q.flow + "." + q.key
		}
		out = append(out, jsonx.NewObject("question_id", id, "id", id, "question", q.question))
	}
	return out
}

// interviewStatus is design.context.interview_status.
func (d *design) interviewStatus(stage string) (*jsonx.Object, error) {
	if err := checkStage(stage); err != nil {
		return nil, err
	}
	idx, err := decision.Load(d.m.PrimaryDir)
	if err != nil {
		return nil, derr("DESIGN_INTERVIEW_FAILED", err.Error())
	}
	incomplete := []any{}
	answers := jsonx.NewObject()
	ids := jsonx.NewObject()
	questions := []any{}
	for _, s := range stageSlots(stage) {
		key := s.key
		if stage == "tasks" {
			key = s.flow + "." + s.key
		}
		step := s.flow + "." + s.key
		e := decision.FindByLogicalKey(idx, s.flow, &step, nil, s.key)
		questions = append(questions, jsonx.NewObject("question_id", key, "id", key, "question", s.question))
		if e != nil {
			ids.Set(key, e.DecisionID)
		}
		if e == nil || e.Status != decision.StatusResolved || e.FinalAnswer == nil || strings.TrimSpace(*e.FinalAnswer) == "" {
			incomplete = append(incomplete, key)
		} else {
			answers.Set(key, *e.FinalAnswer)
		}
	}
	return jsonx.NewObject("questions", questions, "complete", len(incomplete) == 0, "incomplete", incomplete,
		"answers", answers, "decision_ids", ids), nil
}

func (d *design) resolvedInterviewAnswers(stage string) error {
	st, err := d.interviewStatus(stage)
	if err != nil {
		return err
	}
	if !st.GetBool("complete") {
		return derr("DESIGN_INTERVIEW_INCOMPLETE", "Required interview answers are not resolved", "incomplete", mustGet(st, "incomplete"), "stage", stage)
	}
	return nil
}

// ── content validation ────────────────────────────────────────────────────

// artifactInput is design.models.ArtifactInput.
type artifactInput struct {
	Kind       string
	ArtifactID *string
	Content    string
	Expected   string
}

func (d *design) validateSource(item *artifactInput) error {
	kind := "plan"
	if item.Kind == "specification" {
		kind = "spec"
	}
	tmp, err := os.MkdirTemp("", "spec-kitty-design-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	p := filepath.Join(tmp, kind+".md")
	if err := os.WriteFile(p, []byte(item.Content), 0o644); err != nil {
		return err
	}
	if !missionart.IsSubstantive(p, kind, d.missionType()) {
		return derr("DESIGN_PREREQUISITES_FAILED", "The authored source is still a placeholder scaffold")
	}
	if kind == "spec" {
		if errs, _ := reqmap.LintSpec(item.Content, d.missionType()); len(errs) > 0 {
			return derr("DESIGN_PREREQUISITES_FAILED", "The specification declares invalid requirement identifiers")
		}
	}
	return nil
}

var forbiddenPromptKeys = []string{"lane", "review_status", "reviewed_by", "review_feedback", "tracker_refs", "shell_pid", "shell_pid_created_at"}

func validatePrompt(item *artifactInput, manifest *orch.WpsManifest) error {
	if manifest == nil {
		return derr("DESIGN_PREREQUISITES_FAILED", "Package prompts require an outline")
	}
	doc, err := fm.Parse(item.Content)
	if err != nil {
		return derr("DESIGN_PREREQUISITES_FAILED", "Package frontmatter is invalid")
	}
	var hits []*orch.WPEntry
	for _, e := range manifest.WorkPackages {
		if item.ArtifactID != nil && e.ID == *item.ArtifactID {
			hits = append(hits, e)
		}
	}
	wpid, _ := doc.Meta.Get("work_package_id")
	if len(hits) != 1 || item.ArtifactID == nil || wpid != *item.ArtifactID || strings.TrimSpace(doc.Body) == "" {
		return derr("DESIGN_PREREQUISITES_FAILED", "Prompt identity/content does not match its declared package")
	}
	e := hits[0]
	for _, f := range []struct {
		name string
		want []string
	}{{"dependencies", e.Dependencies}, {"requirement_refs", e.RequirementRefs}, {"owned_files", e.OwnedFiles}, {"subtasks", e.Subtasks}} {
		got, ok := doc.Meta.Get(f.name)
		if !ok {
			got = []any{}
		}
		if !pyjson.Equal(got, strList(f.want)) {
			return derr("DESIGN_PREREQUISITES_FAILED", "Prompt definition disagrees with its manifest", "field", f.name, "wp_id", e.ID)
		}
	}
	for _, k := range forbiddenPromptKeys {
		if doc.Meta.Has(k) {
			return derr("DESIGN_REQUEST_INVALID", "Authored prompts cannot supply runtime/status authority")
		}
	}
	return nil
}

// validateContent is design.validation.validate_content.
func (d *design) validateContent(item *artifactInput, manifest *orch.WpsManifest) error {
	if strings.TrimSpace(item.Content) == "" {
		return derr("DESIGN_PREREQUISITES_FAILED", "An authored artifact cannot be empty")
	}
	switch item.Kind {
	case "specification", "plan":
		return d.validateSource(item)
	case "work_package":
		return validatePrompt(item, manifest)
	}
	return nil
}

// requireCommittedContent is design.validation.require_committed_content.
func (d *design) requireCommittedContent(kind string) error {
	p, err := d.artifactPath(kind, nil, nil)
	if err != nil {
		return err
	}
	if _, serr := os.Stat(p); serr != nil || !missionart.IsCommitted(p, d.root) {
		return derr("DESIGN_PREREQUISITES_FAILED", "The parent artifact must be committed", "kind", kind)
	}
	cwd, rel, ok := missionart.RepoTreePath(p, d.root)
	if !ok {
		return derr("DESIGN_PREREQUISITES_FAILED", "The committed parent could not be read")
	}
	res, gerr := gitx.Exec(cwd, nil, nil, "show", "HEAD:"+rel)
	if gerr != nil || res.Code != 0 {
		return derr("DESIGN_PREREQUISITES_FAILED", "The committed parent could not be read")
	}
	disk, _ := os.ReadFile(p)
	if !bytes.Equal([]byte(res.Stdout), disk) {
		return derr("DESIGN_PREREQUISITES_FAILED", "The parent has materialized changes that are not committed", "kind", kind)
	}
	if kind == "specification" || kind == "plan" {
		dg, err := contentDigest(p)
		if err != nil {
			return err
		}
		return d.validateContent(&artifactInput{Kind: kind, Content: string(disk), Expected: dg}, nil)
	}
	return nil
}

// validateManifestGraph is design.validation._validate_manifest_graph.
func (d *design) validateManifestGraph(m *orch.WpsManifest) error {
	ids := map[string]bool{}
	for _, e := range m.WorkPackages {
		ids[e.ID] = true
	}
	if len(m.WorkPackages) == 0 || len(m.WorkPackages) > 64 || len(ids) != len(m.WorkPackages) {
		return derr("DESIGN_PREREQUISITES_FAILED", "The outline requires 1–64 unique package keys")
	}
	specName, ok := configuredArtifactName("input.spec.main", d.missionType())
	if !ok {
		return derr("DESIGN_ARTIFACT_UNSUPPORTED", "The mission type does not declare this artifact")
	}
	specText, err := os.ReadFile(filepath.Join(d.m.PrimaryDir, specName))
	if err != nil {
		return err
	}
	declared := map[string]bool{}
	for _, id := range reqmap.ParseSpecIDs(string(specText), d.missionType()).All {
		declared[id] = true
	}
	graph := map[string][]string{}
	for _, e := range m.WorkPackages {
		if _, err := manifestPromptName(m, e.ID); err != nil {
			return err
		}
		for _, dep := range e.Dependencies {
			if !ids[dep] {
				return derr("DESIGN_PREREQUISITES_FAILED", "Package dependencies must identify declared bounded packages")
			}
		}
		if len(e.Dependencies) > 64 {
			return derr("DESIGN_PREREQUISITES_FAILED", "Package dependencies must identify declared bounded packages")
		}
		if strings.TrimSpace(e.Title) == "" || len(e.RequirementRefs) == 0 {
			return derr("DESIGN_PREREQUISITES_FAILED", "Packages require objectives and declared requirement references")
		}
		for _, r := range e.RequirementRefs {
			if !declared[r] {
				return derr("DESIGN_PREREQUISITES_FAILED", "Packages require objectives and declared requirement references")
			}
		}
		graph[e.ID] = e.Dependencies
	}
	if _, err := lanes.TopologicalSort(graph); err != nil {
		return derr("DESIGN_PREREQUISITES_FAILED", "The package graph contains a dependency cycle")
	}
	return nil
}

// validatedManifest is design.validation.validated_manifest.
func (d *design) validatedManifest(content string) (*orch.WpsManifest, error) {
	m, err := orch.ParseWpsManifest([]byte(content))
	if err != nil {
		return nil, derr("DESIGN_PREREQUISITES_FAILED", "The outline does not satisfy the canonical manifest schema")
	}
	if err := d.validateManifestGraph(m); err != nil {
		return nil, err
	}
	return m, nil
}

func artifactKey(kind string, id *string) string {
	if id == nil {
		return kind
	}
	return kind + ":" + *id
}

func artifactAction(kind string) string {
	switch kind {
	case "specification", "research":
		return "specify"
	case "outline", "work_package":
		return "tasks"
	}
	return "plan"
}

func (d *design) requiredReceipts(stage string, receipts *orch.ReceiptSet) error {
	required := []string{"specification"}
	if stage == "plan" || stage == "tasks" {
		required = append(required, "plan")
	}
	if stage == "tasks" {
		required = append(required, "outline")
		m, err := d.loadManifest()
		if err != nil {
			return err
		}
		if m == nil {
			return derr("DESIGN_PREREQUISITES_FAILED", "Task validation requires an outline")
		}
		if err := d.validateManifestGraph(m); err != nil {
			return err
		}
		for _, e := range m.WorkPackages {
			id := e.ID
			required = append(required, artifactKey("work_package", &id))
		}
	}
	var missing []string
	seen := map[string]bool{}
	for _, k := range required {
		if _, ok := receipts.Receipts[k]; !ok && !seen[k] {
			missing = append(missing, k)
			seen[k] = true
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return derr("DESIGN_PREREQUISITES_FAILED", "API-authored stage lacks accepted provenance", "missing", strList(missing))
	}
	return nil
}

func (d *design) checkReceiptLineage(stage string, receipts *orch.ReceiptSet) error {
	for _, k := range receipts.Keys {
		r := receipts.Receipts[k]
		if stage == "specify" && artifactAction(r.Kind) != "specify" {
			continue
		}
		if stage == "plan" && artifactAction(r.Kind) == "tasks" {
			continue
		}
		p, err := d.artifactPath(r.Kind, r.ArtifactID, nil)
		if err != nil {
			return err
		}
		if cd, err := contentDigest(p); err != nil {
			return err
		} else if cd != r.SHA256 {
			return derr("DESIGN_PARENT_STALE", "Accepted content changed outside its authoring receipt", "kind", r.Kind)
		}
		for _, kv := range r.Parents.Pairs() {
			pp, err := d.artifactPath(kv.Key, nil, nil)
			if err != nil {
				return err
			}
			if cd, err := contentDigest(pp); err != nil {
				return err
			} else if cd != pyStr(kv.Value) {
				return derr("DESIGN_PARENT_STALE", "Accepted content has stale parent provenance", "kind", r.Kind, "parent", kv.Key)
			}
		}
		dg, err := d.contextDigest(r.ContextAction)
		if err != nil {
			return err
		}
		if dg != r.ContextSHA256 {
			return derr("DESIGN_CONTEXT_STALE", "Accepted content was authored against old governance/templates")
		}
	}
	return nil
}

// validateStage is design.validation.validate_stage.
func (d *design) validateStage(stage string) (*jsonx.Object, error) {
	if stage != "specify" && stage != "plan" && stage != "tasks" {
		return nil, derr("DESIGN_REQUEST_INVALID", "Unknown design stage")
	}
	if err := d.resolvedInterviewAnswers(stage); err != nil {
		return nil, err
	}
	idx, err := decision.Load(d.m.PrimaryDir)
	if err != nil {
		return nil, err
	}
	for _, e := range idx.Entries {
		if e.Status == decision.StatusOpen {
			return nil, derr("DESIGN_PREREQUISITES_FAILED", "Open design decisions must be resolved before completion")
		}
	}
	if err := d.requireCommittedContent("specification"); err != nil {
		return nil, err
	}
	if stage == "plan" || stage == "tasks" {
		if err := d.requireCommittedContent("plan"); err != nil {
			return nil, err
		}
	}
	receipts, err := orch.ReadReceipts(d.root, d.slug)
	if err != nil {
		return nil, err
	}
	if receipts != nil {
		if err := d.requiredReceipts(stage, receipts); err != nil {
			return nil, err
		}
		if err := d.checkReceiptLineage(stage, receipts); err != nil {
			return nil, err
		}
	}
	return jsonx.NewObject("stage", stage, "prerequisites_satisfied", true, "validation_only", true), nil
}

// ── artifact-read / design-validate / design-status verbs ─────────────────

// invokeDesign is design_authoring._invoke.
func (v *verb) invokeDesign(ctx *cli.Context, op func(d *design) (*jsonx.Object, error)) error {
	root := v.root()
	m, err := v.mission(root, ctx.String("mission"))
	if err != nil {
		return err
	}
	id := orch.Identity(m.StatusDir)
	data, err := op(newDesign(m))
	if err != nil {
		if _, ok := err.(*orch.DesignError); ok {
			return v.designErr(err, id)
		}
		reason := "OSError"
		if _, ok := err.(*unicodeErr); ok {
			reason = "UnicodeDecodeError"
		}
		return v.fail("DESIGN_IO_FAILED", "The host could not read or persist design content", identity(m.StatusDir, "reason", reason))
	}
	for _, kv := range data.Pairs() {
		id.Set(kv.Key, kv.Value)
	}
	return v.ok(id)
}

func runArtifactRead(ctx *cli.Context) error {
	v := newVerb(ctx, "artifact-read")
	return v.invokeDesign(ctx, func(d *design) (*jsonx.Object, error) {
		return d.readArtifact(ctx.String("kind"), optString(ctx, "artifact-id"))
	})
}

func runDesignValidate(ctx *cli.Context) error {
	v := newVerb(ctx, "design-validate")
	return v.invokeDesign(ctx, func(d *design) (*jsonx.Object, error) { return d.validateStage(ctx.String("stage")) })
}

// runDesignStatus is design_status.design_status.
func runDesignStatus(ctx *cli.Context) error {
	v := newVerb(ctx, "design-status")
	root := v.root()
	handle := ctx.String("mission")
	m, err := v.mission(root, handle)
	if err != nil {
		return err
	}
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(m.PrimaryDir, name)); return err == nil }
	fin, ferr := orch.TasksFinalized(m.StatusDir)
	if ferr != nil {
		return v.fail("DESIGN_STATUS_EVENT_LOG_UNREADABLE", fmt.Sprintf("status.events.jsonl could not be read cleanly for mission %s: %s", pyjson.ReprString(handle), ferr.Error()),
			jsonx.NewObject("mission_slug", handle, "error", ferr.Error()))
	}
	var phase string
	var next any
	switch {
	case !exists("spec.md"):
		phase, next = "specify", "specify"
	case !exists("plan.md"):
		phase, next = "specify", "plan"
	case !fin:
		phase, next = "plan", "tasks"
	case !exists("analysis-report.md"):
		phase, next = "tasks", "check-prerequisites"
	default:
		phase, next = "analyze", nil
	}
	idx, err := decision.Load(m.StatusDir)
	if err != nil {
		return v.fail("DESIGN_STATUS_EVENT_LOG_UNREADABLE", fmt.Sprintf("decisions/index.json could not be read cleanly for mission %s: %s", pyjson.ReprString(handle), err.Error()),
			jsonx.NewObject("mission_slug", handle, "error", err.Error()))
	}
	open := []any{}
	for _, e := range idx.Entries {
		if e.Status == decision.StatusOpen {
			open = append(open, jsonx.NewObject("decision_id", e.DecisionID, "origin", e.OriginFlow))
		}
	}
	if len(open) > 0 {
		next = "resolve-decision"
	}
	return v.ok(identity(m.StatusDir, "current_phase", phase, "next_action", next, "open_decisions", open))
}

var _ = safecommit.StatusCommitted
