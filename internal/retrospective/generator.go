// Package retrospective implements the deterministic mission retrospective
// generator (retrospective.yaml), its canonical YAML writer and the
// RetrospectiveCaptured lifecycle event.
package retrospective

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// GeneratorVersion is GENERATOR_VERSION.
const GeneratorVersion = "1.0"

// FileName is the record filename.
const FileName = "retrospective.yaml"

// PolicyKeys is the resolved-policy source map key order.
var PolicyKeys = []string{"enabled", "timing", "failure_policy", "write_record", "generate_proposals", "apply_proposals",
	"permissions.write_record", "permissions.inspect_mission_artifacts", "permissions.propose_glossary_changes",
	"permissions.propose_drg_changes", "permissions.propose_doctrine_changes", "permissions.apply_low_risk_changes",
	"permissions.apply_structural_changes", "precedence", "generator"}

// DefaultPolicySource maps every policy key to "<default>".
func DefaultPolicySource() *jsonx.Object {
	o := &jsonx.Object{}
	for _, k := range PolicyKeys {
		o.Set(k, "<default>")
	}
	return o
}

// Finding is GenFinding.
type Finding struct {
	ID, Category, Summary string
	Details               string // "" = None
	EvidenceRefs          []string
}

type evidence struct {
	ID, Kind, Path, Range string
	hasRange              bool
}

type registry struct {
	n    int
	m    map[string]string
	refs []evidence
}

func (r *registry) addFile(p string) string {
	if id, ok := r.m[p]; ok {
		return id
	}
	r.n++
	id := fmt.Sprintf("e-%03d", r.n)
	r.m[p] = id
	r.refs = append(r.refs, evidence{ID: id, Kind: "file", Path: p})
	return id
}

func (r *registry) addRange(p, rng, key string) string {
	k := p + ":" + key
	if id, ok := r.m[k]; ok {
		return id
	}
	r.n++
	id := fmt.Sprintf("e-%03d", r.n)
	r.m[k] = id
	r.refs = append(r.refs, evidence{ID: id, Kind: "event_range", Path: p, Range: rng, hasRange: true})
	return id
}

type counters map[string]int

func (c counters) next(prefix string) string {
	c[prefix]++
	return fmt.Sprintf("%s-%03d", prefix, c[prefix])
}

type event = *jsonx.Object

func sget(e event, k string) string {
	v, _ := e.Get(k)
	s, _ := v.(string)
	return s
}

var reworkTargets = map[string]bool{"planned": true, "claimed": true, "in_progress": true}

func isBackward(e event) bool {
	from, to := sget(e, "from_lane"), sget(e, "to_lane")
	switch from {
	case "for_review", "in_review", "approved", "done":
		return reworkTargets[to]
	case "in_progress":
		return to == "planned" || to == "claimed"
	}
	return false
}

var reSyntheticRef = regexp.MustCompile(`^review:WP\d+`)

func documentedPointer(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch s {
	case "force-override", "action-review-claim", "workflow-review-claim":
		return false
	}
	return !reSyntheticRef.MatchString(s)
}

func hasFeedback(e event) bool {
	if v, _ := e.Get("review_ref"); documentedPointer(v) {
		return true
	}
	ev, _ := e.Get("evidence")
	switch x := ev.(type) {
	case *jsonx.Object:
		if rv := x.GetObject("review"); rv != nil {
			ref, _ := rv.Get("reference")
			verdict := rv.GetString("verdict")
			return documentedPointer(ref) && (verdict == "changes_requested" || verdict == "rejected")
		}
		return false
	case string:
		return strings.TrimSpace(x) != ""
	}
	return false
}

func isRejection(e event) bool { return isBackward(e) && hasFeedback(e) }

func isClaim(e event) bool {
	s := strings.TrimSpace(sget(e, "review_ref"))
	return s == "action-review-claim" || s == "workflow-review-claim"
}

func isFriction(e event) bool { return isBackward(e) && !isRejection(e) && !isClaim(e) }

var bootstrapActors = map[string]bool{"finalize-tasks": true, "bootstrap": true, "migrate": true}

func actorString(e event) string {
	v, _ := e.Get("actor")
	switch x := v.(type) {
	case string:
		return strings.ToLower(x)
	case nil:
		return ""
	}
	return strings.ToLower(jsonx.Dumps(v, jsonx.Options{Indent: -1}))
}

func isForceOverride(e event) bool {
	if !e.GetBool("force") || bootstrapActors[actorString(e)] {
		return false
	}
	if sget(e, "from_lane") == sget(e, "to_lane") {
		return false
	}
	return !(isRejection(e) || isClaim(e))
}

func countBy(events []event, pred func(event) bool) map[string]int {
	m := map[string]int{}
	for _, e := range events {
		id := sget(e, "wp_id")
		if id != "" && pred(e) {
			m[id]++
		}
	}
	return m
}

func eventWP(e event) string {
	if id := sget(e, "wp_id"); id != "" {
		return id
	}
	if p := e.GetObject("payload"); p != nil {
		return p.GetString("wp_id")
	}
	return ""
}

func rangeFor(events []event, wp string, pred func(event) bool) string {
	var ids []string
	for _, e := range events {
		if eventWP(e) == wp && pred(e) {
			ids = append(ids, sget(e, "event_id"))
		}
	}
	switch {
	case len(ids) > 1:
		return ids[0] + ".." + ids[len(ids)-1]
	case len(ids) == 1:
		return ids[0]
	}
	return ""
}

func sortedKeys(m map[string]int) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func implCycles(events []event) map[string]int {
	byWP := map[string][]event{}
	var order []string
	for _, e := range events {
		id := sget(e, "wp_id")
		if id == "" {
			continue
		}
		if _, ok := byWP[id]; !ok {
			order = append(order, id)
		}
		byWP[id] = append(byWP[id], e)
	}
	out := map[string]int{}
	for _, id := range order {
		entries, undoc, licensed := 0, 0, false
		for _, e := range byWP[id] {
			if bootstrapActors[actorString(e)] {
				continue
			}
			if isRejection(e) {
				licensed = sget(e, "to_lane") != "in_progress"
				continue
			}
			if f := sget(e, "from_lane"); (f == "planned" || f == "claimed") && sget(e, "to_lane") == "in_progress" {
				entries++
				if entries > 1 && !licensed {
					undoc++
				}
				licensed = false
			}
		}
		if undoc > 0 {
			out[id] = undoc
		}
	}
	return out
}

var reKeyEntities = regexp.MustCompile(`(?im)^#{1,6}\s+Key Entities`)
var reEntityBullet = regexp.MustCompile(`(?m)^\s*[-*]\s+\*\*[^*\n\[][^*\n]*\*\*`)
var reAnyHeading = regexp.MustCompile(`(?m)^#{1,6}\s`)

func declaresEntities(spec string) bool {
	loc := reKeyEntities.FindStringIndex(spec)
	if loc == nil {
		return false
	}
	section := spec[loc[1]:]
	if h := reAnyHeading.FindStringIndex(section); h != nil {
		section = section[:h[0]]
	}
	return reEntityBullet.MatchString(section)
}

var reFRDeclared = regexp.MustCompile(`(?m)^\s*(?:\|\s*|#{1,6}\s+|[-*]\s+(?:\*\*)?|\d+\.\s+(?:\*\*)?|\*\*)(FR-\d{3,}[A-Za-z]?)\b`)
var reFRCite = regexp.MustCompile(`\b[Ff][Rr]-(\d{3,})([A-Za-z]?)\b`)

func declaredFRs(spec string) map[string]bool {
	out := map[string]bool{}
	for _, m := range reFRDeclared.FindAllStringSubmatch(spec, -1) {
		out[m[1]] = true
	}
	return out
}

var reNeedsClarification = regexp.MustCompile(`(?i)\[NEEDS CLARIFICATION:`)

func readOptional(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func loadEvents(dir string) []event {
	text, ok := readOptional(filepath.Join(dir, "status.events.jsonl"))
	if !ok {
		return nil
	}
	var out []event
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if o, err := jsonx.DecodeObject([]byte(line)); err == nil {
			out = append(out, o)
		}
	}
	return out
}

func leadingFrontmatter(text string) *jsonx.Object {
	if !strings.HasPrefix(text, "---") {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			v, err := pyyaml.SafeLoad(strings.Join(lines[1:i], "\n"))
			if err != nil {
				return nil
			}
			o, _ := v.(*jsonx.Object)
			return o
		}
	}
	return nil
}

func findingsCount(fm *jsonx.Object) (int, bool) {
	if fm == nil {
		return 0, false
	}
	v, _ := fm.Get("findings")
	switch x := v.(type) {
	case []any:
		return len(x), true
	case int:
		return x, true
	case float64:
		return int(x), true
	}
	return 0, false
}

// Record is a generated retrospective record.
type Record struct {
	Meta                     *jsonx.Object
	Slug, MissionID          string
	MissionNumber            any
	FriendlyName, Type       string
	TargetBranch             string
	CreatedAt                string
	FindingsStatus           string
	Helped, NotHelpful, Gaps []Finding
	Evidence                 []evidence
	PolicySource             *jsonx.Object
	// ProvenanceKind / Command / Human select the provenance block
	// (runtime_post_completion by default).
	ProvenanceKind, Command string
	Human                   bool
}

// Generate is generate_retrospective for mission directory slug.
func Generate(root, slug, invokedAt string) (*Record, error) {
	dir := project.Specs(root, slug)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("Mission %s not found under %s. Check the mission handle (slug, mission_id, or directory name).", mission.PyRepr(slug), project.Specs(root))
	}
	meta := mission.ReadMetaOrEmpty(dir)
	spec, _ := readOptional(filepath.Join(dir, "spec.md"))
	plan, _ := readOptional(filepath.Join(dir, "plan.md"))
	tasks, _ := readOptional(filepath.Join(dir, "tasks.md"))
	_, hasResearch := readOptional(filepath.Join(dir, "research.md"))
	_, hasDataModel := readOptional(filepath.Join(dir, "data-model.md"))
	wfText, hasWF := readOptional(filepath.Join(dir, "workflow-failures-log.md"))
	arText, hasAR := readOptional(filepath.Join(dir, "analysis-report.md"))
	rrText, hasRR := readOptional(filepath.Join(dir, "mission-review-report.md"))
	wpPaths, _ := filepath.Glob(filepath.Join(dir, "tasks", "WP*.md"))
	sort.Strings(wpPaths)
	type wpFile struct{ name, text string }
	var wps []wpFile
	for _, p := range wpPaths {
		if t, ok := readOptional(p); ok {
			wps = append(wps, wpFile{filepath.Base(p), t})
		}
	}
	events := loadEvents(dir)
	reg := &registry{m: map[string]string{}}
	base := "kitty-specs/" + slug + "/"
	specRel, eventsRel := base+"spec.md", base+"status.events.jsonl"
	if meta.Len() > 0 {
		reg.addFile(base + "meta.json")
	}
	if spec != "" {
		reg.addFile(specRel)
	}
	if plan != "" {
		reg.addFile(base + "plan.md")
	}
	if tasks != "" {
		reg.addFile(base + "tasks.md")
	}
	wpEv := map[string]string{}
	for _, w := range wps {
		wpEv[w.name] = reg.addFile(base + "tasks/" + w.name)
	}
	if len(events) > 0 {
		reg.addFile(eventsRel)
	}
	c := counters{}
	var helped, notHelpful, gaps []Finding
	rej := countBy(events, isRejection)
	fric := countBy(events, isFriction)
	cyc := implCycles(events)
	done := map[string]bool{}
	for _, e := range events {
		if id := sget(e, "wp_id"); id != "" && (sget(e, "to_lane") == "done" || sget(e, "to_lane") == "approved") {
			done[id] = true
		}
	}
	var doneIDs []string
	for id := range done {
		doneIDs = append(doneIDs, id)
	}
	sort.Strings(doneIDs)
	ingestor := hasWF || hasAR || hasRR
	if len(rej) > 0 || len(fric) > 0 || ingestor {
		for _, id := range doneIDs {
			if rej[id] != 0 || fric[id] != 0 || cyc[id] != 0 {
				continue
			}
			var refs []string
			if ev, ok := wpEv[id+".md"]; ok {
				refs = []string{ev}
			} else if len(events) > 0 {
				refs = []string{reg.addFile(eventsRel)}
			}
			if len(refs) > 0 {
				helped = append(helped, Finding{ID: c.next("h"), Category: "implementation", Summary: id + " completed without rejection cycles", EvidenceRefs: refs})
			}
		}
	}
	for _, id := range sortedKeys(rej) {
		rng := rangeFor(events, id, isRejection)
		if rng == "" {
			rng = "rejection"
		}
		ev := reg.addRange(eventsRel, rng, "rejection_"+id)
		notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "review_loop",
			Summary: fmt.Sprintf("%s required %d rejection cycle(s) before approval", id, rej[id]),
			Details: fmt.Sprintf("WP %s was sent back to an earlier lane with documented review feedback %d time(s).", id, rej[id]), EvidenceRefs: []string{ev}})
	}
	for _, id := range sortedKeys(fric) {
		rng := rangeFor(events, id, isFriction)
		if rng == "" {
			rng = "lane_friction"
		}
		ev := reg.addRange(eventsRel, rng, "lane_friction_"+id)
		notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "process",
			Summary: fmt.Sprintf("%s had %d lane bounce(s) before approval", id, fric[id]),
			Details: fmt.Sprintf("WP %s moved backward across workflow lanes %d time(s) outside the documented reviewer-feedback flow.", id, fric[id]), EvidenceRefs: []string{ev}})
	}
	force := countBy(events, isForceOverride)
	for _, id := range sortedKeys(force) {
		rng := rangeFor(events, id, isForceOverride)
		if rng == "" {
			rng = "force_override"
		}
		ev := reg.addRange(eventsRel, rng, "force_override_"+id)
		notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "process",
			Summary:      fmt.Sprintf("%s required %d --force override(s) during workflow", id, force[id]),
			Details:      fmt.Sprintf("WP %s required %d operator-driven --force lane transition(s). Force overrides typically indicate the runtime guard failed or the operator routed around it; investigate the underlying cause.", id, force[id]),
			EvidenceRefs: []string{ev}})
	}
	for _, id := range sortedKeys(cyc) {
		ev := reg.addRange(eventsRel, "implementation_cycles", "impl_cycles_"+id)
		notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "implementation",
			Summary:      fmt.Sprintf("%s re-entered in_progress %d time(s) without a documented review rejection", id, cyc[id]),
			Details:      fmt.Sprintf("WP %s went back into implementation %d time(s) with no documented review rejection before it. That rework was not captured by the review loop; the WP scope or contract may need refinement.", id, cyc[id]),
			EvidenceRefs: []string{ev}})
	}
	if spec != "" {
		for _, loc := range reNeedsClarification.FindAllStringIndex(spec, -1) {
			line := strings.Count(spec[:loc[0]], "\n") + 1
			ev := reg.addFile(specRel)
			gaps = append(gaps, Finding{ID: c.next("g"), Category: "spec_quality",
				Summary:      fmt.Sprintf("Unresolved clarification marker in spec.md (line %d)", line),
				Details:      fmt.Sprintf("Found '[NEEDS CLARIFICATION:' marker at line %d of spec.md. This indicates a specification gap that was not resolved before implementation.", line),
				EvidenceRefs: []string{ev}})
		}
	}
	if spec != "" && len(wps) > 0 {
		declared := declaredFRs(spec)
		covered := map[string]bool{}
		for _, w := range wps {
			for _, m := range reFRCite.FindAllStringSubmatch(w.text, -1) {
				covered["FR-"+m[1]+strings.ToUpper(m[2])] = true
			}
		}
		var ids []string
		for fr := range declared {
			if !covered[fr] {
				ids = append(ids, fr)
			}
		}
		sort.Strings(ids)
		for _, fr := range ids {
			reg.addFile(specRel)
			gaps = append(gaps, Finding{ID: c.next("g"), Category: "spec_quality", Summary: fr + " defined in spec.md has no WP coverage",
				Details:      fmt.Sprintf("Requirement %s appears in spec.md but is not referenced by any work package task file. It may be unimplemented.", fr),
				EvidenceRefs: []string{reg.addFile(specRel)}})
		}
	}
	if spec != "" {
		if !hasResearch {
			gaps = append(gaps, Finding{ID: c.next("g"), Category: "doc", Summary: "research.md absent",
				Details:      "spec.md is present but research.md is missing. Research artifacts help future maintainers understand design decisions.",
				EvidenceRefs: []string{reg.addFile(specRel)}})
		}
		if !hasDataModel && declaresEntities(spec) {
			gaps = append(gaps, Finding{ID: c.next("g"), Category: "doc", Summary: "data-model.md absent",
				Details:      "spec.md declares domain entities but data-model.md is missing. A data model document clarifies domain entity relationships.",
				EvidenceRefs: []string{reg.addFile(specRel)}})
		}
	}
	// ingestors
	if hasWF {
		var fails []string
		for _, l := range strings.Split(wfText, "\n") {
			s := strings.TrimSpace(l)
			if strings.HasPrefix(s, "- [ ] FAIL:") || strings.HasPrefix(s, "### FAIL:") {
				fails = append(fails, s)
			}
		}
		rel := base + "workflow-failures-log.md"
		if len(fails) > 0 {
			for _, f := range fails {
				sum := f
				if len([]rune(sum)) > 200 {
					sum = string([]rune(sum)[:200])
				}
				notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "process", Summary: sum,
					Details: "Workflow failure recorded in workflow-failures-log.md. Entry: " + f, EvidenceRefs: []string{reg.addFile(rel)}})
			}
		} else {
			helped = append(helped, Finding{ID: c.next("h"), Category: "process", Summary: "workflow-failures-log.md present with no recorded failures",
				Details:      "A workflow-failures-log.md file is present but contains no structured failure entries (lines starting with '- [ ] FAIL:' or '### FAIL:'). This may indicate all workflows completed cleanly.",
				EvidenceRefs: []string{reg.addFile(rel)}})
		}
	}
	if hasAR {
		rel := base + "analysis-report.md"
		fm := leadingFrontmatter(arText)
		n, ok := findingsCount(fm)
		verdict := ""
		hasVerdict := false
		if fm != nil {
			if v, present := fm.Get("verdict"); present && v != nil {
				verdict, hasVerdict = fmt.Sprint(v), true
			}
		}
		switch {
		case ok && hasVerdict && verdict != "unknown":
			if n > 0 {
				notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "doc", Summary: fmt.Sprintf("analysis-report.md records %d finding(s)", n),
					Details:      fmt.Sprintf("The analysis-report.md artifact for this mission records %d finding(s) in its structured frontmatter. Review them to understand documented issues and decisions.", n),
					EvidenceRefs: []string{reg.addFile(rel)}})
			} else {
				helped = append(helped, Finding{ID: c.next("h"), Category: "doc", Summary: "analysis-report.md present with no recorded findings",
					Details:      "An analysis-report.md artifact is present and its frontmatter records zero findings with a resolved verdict — the analysis raised nothing.",
					EvidenceRefs: []string{reg.addFile(rel)}})
			}
		case ok:
			notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "doc", Summary: "analysis-report.md present; findings not machine-readable",
				Details:      "An analysis-report.md artifact is present for this mission and its frontmatter carries a machine-readable findings count, but no resolved verdict (the verdict key is absent or records the legacy 'unknown' marker), so the count is not asserted; review it manually.",
				EvidenceRefs: []string{reg.addFile(rel)}})
		default:
			notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "doc", Summary: "analysis-report.md present; findings not machine-readable",
				Details:      "An analysis-report.md artifact is present for this mission, but its leading frontmatter does not carry a machine-readable findings count; review it manually.",
				EvidenceRefs: []string{reg.addFile(rel)}})
		}
	}
	if hasRR {
		rel := base + "mission-review-report.md"
		n, ok := findingsCount(leadingFrontmatter(rrText))
		switch {
		case ok && n > 0:
			notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "review_loop", Summary: fmt.Sprintf("mission-review-report.md records %d finding(s)", n),
				Details:      fmt.Sprintf("The mission-review-report.md artifact for this mission records %d finding(s) in its frontmatter. Review them to understand documented review outcomes.", n),
				EvidenceRefs: []string{reg.addFile(rel)}})
		case ok:
			helped = append(helped, Finding{ID: c.next("h"), Category: "review_loop", Summary: "mission-review-report.md present with no findings",
				Details:      "A mission-review-report.md artifact is present and its frontmatter records zero findings — the review pass raised nothing.",
				EvidenceRefs: []string{reg.addFile(rel)}})
		default:
			notHelpful = append(notHelpful, Finding{ID: c.next("n"), Category: "review_loop", Summary: "mission-review-report.md present; findings not machine-readable",
				Details:      "A mission-review-report.md artifact is present for this mission, but its leading frontmatter does not carry a machine-readable findings count; review it manually.",
				EvidenceRefs: []string{reg.addFile(rel)}})
		}
	}
	byCat := func(l []Finding) {
		sort.SliceStable(l, func(i, j int) bool {
			if l[i].Category != l[j].Category {
				return l[i].Category < l[j].Category
			}
			return l[i].Summary < l[j].Summary
		})
	}
	byCat(helped)
	byCat(notHelpful)
	byCat(gaps)
	rec := &Record{Meta: meta, Helped: helped, NotHelpful: notHelpful, Gaps: gaps, Evidence: reg.refs, CreatedAt: invokedAt,
		PolicySource: DefaultPolicySource()}
	rec.FindingsStatus = "ran_no_findings"
	if len(helped)+len(notHelpful)+len(gaps) > 0 {
		rec.FindingsStatus = "has_findings"
	}
	rec.MissionID = meta.GetString("mission_id")
	rec.Slug = meta.GetString("mission_slug")
	if rec.Slug == "" {
		rec.Slug = meta.GetString("slug")
	}
	if rec.Slug == "" {
		rec.Slug = slug
	}
	rec.FriendlyName = meta.GetString("friendly_name")
	if rec.FriendlyName == "" {
		rec.FriendlyName = meta.GetString("name")
	}
	if rec.FriendlyName == "" {
		rec.FriendlyName = rec.Slug
	}
	rec.Type = mission.MissionType(meta)
	rec.TargetBranch = strings.TrimSpace(meta.GetString("target_branch"))
	if n, ok := mission.MissionNumber(meta); ok {
		rec.MissionNumber = n
	}
	return rec, nil
}

func findingsYAML(l []Finding) []any {
	out := []any{}
	for _, f := range l {
		refs := []any{}
		for _, r := range f.EvidenceRefs {
			refs = append(refs, r)
		}
		o := jsonx.NewObject("id", f.ID, "category", f.Category, "summary", f.Summary, "evidence_refs", refs)
		if f.Details != "" {
			o.Set("details", f.Details)
		}
		out = append(out, o)
	}
	return out
}

// ToObject renders the record mapping in schema order.
func (r *Record) ToObject() *jsonx.Object {
	ev := []any{}
	for _, e := range r.Evidence {
		o := jsonx.NewObject("id", e.ID, "kind", e.Kind, "path", e.Path)
		if e.hasRange {
			o.Set("range", e.Range)
		}
		ev = append(ev, o)
	}
	return jsonx.NewObject("schema_version", 1, "mission_id", r.MissionID, "mission_slug", r.Slug, "mission_number", r.MissionNumber,
		"friendly_name", r.FriendlyName, "mission_type", r.Type, "target_branch", r.TargetBranch, "created_at", r.CreatedAt,
		"created_by", r.createdBy(), "provenance", r.provenance(),
		"policy_source", r.PolicySource.Clone(), "findings_status", r.FindingsStatus,
		"helped", findingsYAML(r.Helped), "not_helpful", findingsYAML(r.NotHelpful), "gaps", findingsYAML(r.Gaps),
		"proposals", []any{}, "evidence_refs", ev, "generator_version", GeneratorVersion, "provenance_history", []any{})
}

// Now is the capture timestamp source.
func Now() string { return clock.NowUTCISO() }

func (r *Record) createdBy() *jsonx.Object {
	if r.Human {
		return jsonx.NewObject("kind", "human", "id", "cli", "display", "spec-kitty retrospect")
	}
	return jsonx.NewObject("kind", "runtime", "id", "spec-kitty-generator", "display", "Spec Kitty Generator")
}

func (r *Record) provenance() *jsonx.Object {
	kind := r.ProvenanceKind
	if kind == "" {
		kind = "runtime_post_completion"
	}
	o := jsonx.NewObject("kind", kind, "invoked_at", r.CreatedAt, "policy_resolved_from", r.PolicySource.Clone())
	if r.Command != "" {
		o.Set("command", r.Command)
	}
	return o
}
