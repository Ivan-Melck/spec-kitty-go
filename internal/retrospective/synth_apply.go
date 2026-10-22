package retrospective

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports specify_cli.charter_pack_synthesizer (apply_proposals,
// detect_conflicts, provenance sidecars): applying a legacy-schema
// retrospective record's accepted proposals to project-local glossary, DRG
// overlay and charter-pack state (agent retrospect synthesize --apply).

// ConflictGroup is conflict.ConflictGroup.
type ConflictGroup struct {
	ProposalIDs []string
	Reason      string
}

// Rejected is apply.RejectedProposal.
type Rejected struct{ ProposalID, Reason, Detail string }

// Planned is apply.PlannedApplication.
type Planned struct {
	ProposalID, Kind string
	Targets          []string
	DiffPreview      string
}

// Applied is apply.AppliedChange.
type Applied struct {
	ProposalID, TargetURN, ArtifactPath, ProvenancePath string
	ReApplied                                           bool
}

// SynthResult is apply.SynthesisResult.
type SynthResult struct {
	DryRun        bool
	Planned       []Planned
	Applied       []Applied
	Conflicts     []ConflictGroup
	Rejected      []Rejected
	EventsEmitted []string
}

// Object is SynthesisResult.model_dump().
func (r *SynthResult) Object() *jsonx.Object {
	planned, applied, conflicts, rejected, events := []any{}, []any{}, []any{}, []any{}, []any{}
	for _, p := range r.Planned {
		t := []any{}
		for _, x := range p.Targets {
			t = append(t, x)
		}
		planned = append(planned, jsonx.NewObject("proposal_id", p.ProposalID, "kind", p.Kind, "targets", t, "diff_preview", p.DiffPreview))
	}
	for _, a := range r.Applied {
		applied = append(applied, jsonx.NewObject("proposal_id", a.ProposalID, "target_urn", a.TargetURN, "artifact_path", a.ArtifactPath,
			"provenance_path", a.ProvenancePath, "re_applied", a.ReApplied))
	}
	for _, c := range r.Conflicts {
		ids := []any{}
		for _, x := range c.ProposalIDs {
			ids = append(ids, x)
		}
		conflicts = append(conflicts, jsonx.NewObject("proposal_ids", ids, "reason", c.Reason))
	}
	for _, x := range r.Rejected {
		rejected = append(rejected, jsonx.NewObject("proposal_id", x.ProposalID, "reason", x.Reason, "detail", x.Detail))
	}
	for _, e := range r.EventsEmitted {
		events = append(events, e)
	}
	return jsonx.NewObject("dry_run", r.DryRun, "planned", planned, "applied", applied, "conflicts", conflicts, "rejected", rejected, "events_emitted", events)
}

func pstr(o *jsonx.Object, k string) string { return o.GetString(k) }

func edgeOf(o *jsonx.Object, k string) (from, to, kind string) {
	e := o.GetObject(k)
	if e == nil {
		return "", "", ""
	}
	return e.GetString("from_node"), e.GetString("to_node"), e.GetString("kind")
}

func reprList(xs []string) string { return pyjson.ReprStrings(xs) }

func sortedUnique(xs []string) []string {
	set := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !set[x] {
			set[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// DetectConflicts is conflict.detect_conflicts.
func DetectConflicts(ps []LegacyProposal) []ConflictGroup {
	var groups []ConflictGroup
	type edgeP struct{ id, from, to, kind string }
	var adds, removes []edgeP
	type rew struct {
		id                string
		oFrom, oTo, oKind string
		nFrom, nTo, nKind string
	}
	var rewires []rew
	type hashed struct{ id, hash string }
	addGloss, updGloss := map[string][]hashed{}, map[string][]hashed{}
	var addGlossOrder, updGlossOrder []string
	synth := map[[2]string][]hashed{}
	var synthOrder [][2]string
	for _, p := range ps {
		pl := p.Payload
		switch pl.GetString("kind") {
		case "add_edge":
			f, t, k := edgeOf(pl, "edge")
			adds = append(adds, edgeP{p.ID, f, t, k})
		case "remove_edge":
			f, t, k := edgeOf(pl, "edge")
			removes = append(removes, edgeP{p.ID, f, t, k})
		case "rewire_edge":
			of, ot, ok := edgeOf(pl, "edge_old")
			nf, nt, nk := edgeOf(pl, "edge_new")
			rewires = append(rewires, rew{p.ID, of, ot, ok, nf, nt, nk})
		case "add_glossary_term":
			k := pstr(pl, "term_key")
			if _, seen := addGloss[k]; !seen {
				addGlossOrder = append(addGlossOrder, k)
			}
			addGloss[k] = append(addGloss[k], hashed{p.ID, pstr(pl, "definition_hash")})
		case "update_glossary_term":
			k := pstr(pl, "term_key")
			if _, seen := updGloss[k]; !seen {
				updGlossOrder = append(updGlossOrder, k)
			}
			updGloss[k] = append(updGloss[k], hashed{p.ID, pstr(pl, "definition_hash")})
		case "synthesize_directive", "synthesize_tactic", "synthesize_procedure":
			k := [2]string{pl.GetString("kind"), pstr(pl, "artifact_id")}
			if _, seen := synth[k]; !seen {
				synthOrder = append(synthOrder, k)
			}
			synth[k] = append(synth[k], hashed{p.ID, pstr(pl, "body_hash")})
		}
	}
	key := func(f, t, k string) string { return f + "\x00" + t + "\x00" + k }
	addKeys := map[string]string{}
	for _, a := range adds {
		addKeys[key(a.from, a.to, a.kind)] = a.id
	}
	q := pyjson.ReprString
	for _, r := range removes {
		if id, ok := addKeys[key(r.from, r.to, r.kind)]; ok {
			groups = append(groups, ConflictGroup{[]string{id, r.id}, fmt.Sprintf("add_edge and remove_edge target the same edge (%s → %s [%s])", q(r.from), q(r.to), q(r.kind))})
		}
	}
	for _, r := range rewires {
		if id, ok := addKeys[key(r.nFrom, r.nTo, r.nKind)]; ok {
			groups = append(groups, ConflictGroup{[]string{id, r.id}, fmt.Sprintf("add_edge and rewire_edge both target the same destination edge (%s → %s [%s])", q(r.nFrom), q(r.nTo), q(r.nKind))})
		}
	}
	remKeys := map[string]string{}
	for _, r := range removes {
		remKeys[key(r.from, r.to, r.kind)] = r.id
	}
	for _, r := range rewires {
		if id, ok := remKeys[key(r.oFrom, r.oTo, r.oKind)]; ok {
			groups = append(groups, ConflictGroup{[]string{id, r.id}, fmt.Sprintf("remove_edge and rewire_edge both target the same source edge (%s → %s [%s])", q(r.oFrom), q(r.oTo), q(r.oKind))})
		}
	}
	glossGroups := func(order []string, m map[string][]hashed, label string) {
		for _, k := range order {
			es := m[k]
			if len(es) < 2 {
				continue
			}
			var hs, ids []string
			for _, e := range es {
				hs = append(hs, e.hash)
				ids = append(ids, e.id)
			}
			if u := sortedUnique(hs); len(u) > 1 {
				groups = append(groups, ConflictGroup{ids, fmt.Sprintf("Multiple %s proposals for key %s with diverging definition hashes: %s", label, q(k), reprList(u))})
			}
		}
	}
	glossGroups(addGlossOrder, addGloss, "add_glossary_term")
	glossGroups(updGlossOrder, updGloss, "update_glossary_term")
	for _, k := range synthOrder {
		es := synth[k]
		if len(es) < 2 {
			continue
		}
		var hs, ids []string
		for _, e := range es {
			hs = append(hs, e.hash)
			ids = append(ids, e.id)
		}
		if u := sortedUnique(hs); len(u) > 1 {
			groups = append(groups, ConflictGroup{ids, fmt.Sprintf("Multiple %s proposals for artifact %s with diverging body hashes: %s", q(k[0]), q(k[1]), reprList(u))})
		}
	}
	return groups
}

// missionDirByID is _resolve_mission_by_id: the kitty-specs mission whose
// meta.json carries mission_id.
func missionDirByID(root, missionID string) (string, string) {
	ents, _ := os.ReadDir(filepath.Join(root, "kitty-specs"))
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, "kitty-specs", e.Name())
		b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if err != nil {
			continue
		}
		v, err := pyjson.LoadsBytes(b)
		if err != nil {
			continue
		}
		if o, ok := v.(*jsonx.Object); ok && o.GetString("mission_id") == missionID {
			slug := o.GetString("mission_slug")
			if slug == "" {
				slug = e.Name()
			}
			return dir, slug
		}
	}
	return "", ""
}

func eventIDs(dir string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(dir, "status.events.jsonl"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := pyjson.Loads(line)
		if err != nil {
			continue
		}
		if o, ok := v.(*jsonx.Object); ok {
			if s, ok := mustGet(o, "event_id").(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

func planApplication(p LegacyProposal) Planned {
	pl := p.Payload
	q := pyjson.ReprString
	switch pl.GetString("kind") {
	case "add_glossary_term":
		k := pstr(pl, "term_key")
		return Planned{p.ID, p.Kind, []string{"glossary:term:" + k}, "add glossary term " + q(k)}
	case "update_glossary_term":
		k := pstr(pl, "term_key")
		return Planned{p.ID, p.Kind, []string{"glossary:term:" + k}, "update glossary term " + q(k)}
	case "flag_not_helpful":
		u := pl.GetObject("target").GetString("urn")
		return Planned{p.ID, p.Kind, []string{u}, fmt.Sprintf("flag %s as not_helpful", q(u))}
	case "add_edge":
		f, t, k := edgeOf(pl, "edge")
		return Planned{p.ID, p.Kind, []string{fmt.Sprintf("drg:edge:%s-%s-%s", f, k, t)}, fmt.Sprintf("add DRG edge %s --[%s]--> %s", q(f), k, q(t))}
	case "rewire_edge":
		f, t, k := edgeOf(pl, "edge_new")
		return Planned{p.ID, p.Kind, []string{fmt.Sprintf("drg:edge:%s-%s-%s", f, k, t)}, fmt.Sprintf("rewire DRG edge to %s --[%s]--> %s", q(f), k, q(t))}
	case "synthesize_directive", "synthesize_tactic", "synthesize_procedure":
		ks := strings.TrimPrefix(pl.GetString("kind"), "synthesize_")
		id := pstr(pl, "artifact_id")
		return Planned{p.ID, p.Kind, []string{"charter:" + ks + ":" + id}, "synthesize " + ks + " " + q(id)}
	}
	return Planned{p.ID, p.Kind, []string{"unknown"}, "apply " + p.Kind}
}

// ApplyProposals is apply.apply_proposals.
func ApplyProposals(missionID, root string, proposals []LegacyProposal, approved map[string]bool, actor LegacyActor, dryRun bool) *SynthResult {
	var effective []LegacyProposal
	for _, p := range proposals {
		if approved[p.ID] || p.Kind == "flag_not_helpful" {
			effective = append(effective, p)
		}
	}
	res := &SynthResult{DryRun: dryRun}
	if conflicts := DetectConflicts(effective); len(conflicts) > 0 {
		inGroup := map[string]string{}
		for _, cg := range conflicts {
			for _, id := range cg.ProposalIDs {
				if _, ok := inGroup[id]; !ok {
					inGroup[id] = cg.Reason
				}
			}
		}
		for _, p := range effective {
			if r, ok := inGroup[p.ID]; ok {
				res.Rejected = append(res.Rejected, Rejected{p.ID, "conflict", r})
			}
		}
		if !dryRun {
			res.EventsEmitted = emitConflictRejections(missionID, root, effective, conflicts, actor)
		}
		res.Conflicts = conflicts
		return res
	}
	dir, _ := missionDirByID(root, missionID)
	known := map[string]bool{}
	if dir != "" {
		known = eventIDs(dir)
	}
	var surviving []LegacyProposal
	for _, p := range effective {
		var unreachable []string
		for _, e := range p.EvidenceIDs {
			if !known[e] {
				unreachable = append(unreachable, e)
			}
		}
		if len(unreachable) > 0 {
			res.Rejected = append(res.Rejected, Rejected{p.ID, "stale_evidence", "Evidence event ids not reachable in source mission event log: " + reprList(unreachable)})
			continue
		}
		surviving = append(surviving, p)
	}
	for _, p := range surviving {
		res.Planned = append(res.Planned, planApplication(p))
	}
	if dryRun {
		return res
	}
	sort.SliceStable(surviving, func(i, j int) bool { return surviving[i].ID < surviving[j].ID })
	for _, p := range surviving {
		a, ev, err := applyOne(p, missionID, root, actor)
		if err != nil {
			res.Rejected = append(res.Rejected, Rejected{p.ID, "invalid_payload", err.Error()})
			break
		}
		res.Applied = append(res.Applied, a)
		if ev != "" {
			res.EventsEmitted = append(res.EventsEmitted, ev)
		}
	}
	return res
}

var reUnsafePath = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safePathComponent is provenance._safe_path_component.
func safePathComponent(v string) string {
	c := strings.Trim(reUnsafePath.ReplaceAllString(v, "_"), "._")
	for strings.Contains(c, "..") {
		c = strings.ReplaceAll(c, "..", "__")
	}
	if c == "" {
		c = "artifact"
	}
	if c != v || len([]rune(c)) > 128 {
		sum := sha256.Sum256([]byte(v))
		r := []rune(c)
		if len(r) > 115 {
			r = r[:115]
		}
		c = string(r) + "-" + hex.EncodeToString(sum[:])[:12]
	}
	return c
}

func assertWithin(base, target string) (string, error) {
	rb, err := filepath.EvalSymlinks(base)
	if err != nil {
		rb, _ = filepath.Abs(base)
	}
	rt := filepath.Join(rb, filepath.Base(target))
	if rel, err := filepath.Rel(rb, rt); err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("refusing to write outside artifact base: target=%s base=%s", rt, rb)
	}
	return rt, nil
}

func safeDump(v any) string {
	return pyyaml.Dump(v, pyyaml.Options{SortKeys: true, AllowUnicode: true})
}

func loadEdges(p string) []any {
	b, err := os.ReadFile(p)
	if err != nil {
		return []any{}
	}
	v, err := pyyaml.SafeLoad(string(b))
	if l, ok := v.([]any); err == nil && ok {
		return l
	}
	return []any{}
}

func strsAny(xs []any) []any {
	if xs == nil {
		return []any{}
	}
	return xs
}

func applyOne(p LegacyProposal, missionID, root string, actor LegacyActor) (Applied, string, error) {
	pl := p.Payload
	var targetURN, artifactPath, artifactID string
	var err error
	write := func(path string, content string) error { return os.WriteFile(path, []byte(content), 0o644) }
	switch pl.GetString("kind") {
	case "add_glossary_term", "update_glossary_term":
		key := pstr(pl, "term_key")
		dir := filepath.Join(root, ".kittify", "glossary")
		os.MkdirAll(dir, 0o755)
		if artifactPath, err = assertWithin(dir, filepath.Join(dir, key+".yaml")); err != nil {
			return Applied{}, "", err
		}
		related, _ := mustGet(pl, "related_terms").([]any)
		data := jsonx.NewObject("term_key", key, "definition", pstr(pl, "definition"), "definition_hash", pstr(pl, "definition_hash"), "related_terms", strsAny(related))
		if err := write(artifactPath, safeDump(data)); err != nil {
			return Applied{}, "", err
		}
		targetURN, artifactID = "glossary:term:"+key, key
	case "flag_not_helpful":
		urn := pl.GetObject("target").GetString("urn")
		dir := filepath.Join(root, ".kittify", "charter-packs", ".flags")
		os.MkdirAll(dir, 0o755)
		if artifactPath, err = assertWithin(dir, filepath.Join(dir, safePathComponent(urn)+".yaml")); err != nil {
			return Applied{}, "", err
		}
		if err := write(artifactPath, safeDump(jsonx.NewObject("target_urn", urn, "flag", "not_helpful", "proposal_id", p.ID))); err != nil {
			return Applied{}, "", err
		}
		targetURN, artifactID = urn, urn
	case "add_edge", "rewire_edge":
		dir := filepath.Join(root, ".kittify", "drg")
		os.MkdirAll(dir, 0o755)
		artifactPath = filepath.Join(dir, "edges.yaml")
		edges := loadEdges(artifactPath)
		var f, t, k string
		if pl.GetString("kind") == "rewire_edge" {
			of, ot, ok := edgeOf(pl, "edge_old")
			var kept []any
			for _, e := range edges {
				if eo, isObj := e.(*jsonx.Object); isObj && eo.GetString("from_node") == of && eo.GetString("to_node") == ot && eo.GetString("kind") == ok {
					continue
				}
				kept = append(kept, e)
			}
			edges = kept
			f, t, k = edgeOf(pl, "edge_new")
		} else {
			f, t, k = edgeOf(pl, "edge")
		}
		edges = append(edges, jsonx.NewObject("from_node", f, "to_node", t, "kind", k))
		if err := write(artifactPath, safeDump(strsAny(edges))); err != nil {
			return Applied{}, "", err
		}
		targetURN = fmt.Sprintf("drg:edge:%s-%s-%s", f, k, t)
		artifactID = fmt.Sprintf("%s-%s-%s", f, k, t)
	case "synthesize_directive", "synthesize_tactic", "synthesize_procedure":
		ks := strings.TrimPrefix(pl.GetString("kind"), "synthesize_")
		id := pstr(pl, "artifact_id")
		dir := filepath.Join(root, ".kittify", "charter-packs", ks)
		os.MkdirAll(dir, 0o755)
		if artifactPath, err = assertWithin(dir, filepath.Join(dir, id+".md")); err != nil {
			return Applied{}, "", err
		}
		if err := write(artifactPath, pstr(pl, "body")); err != nil {
			return Applied{}, "", err
		}
		targetURN, artifactID = "charter:"+ks+":"+id, id
	default:
		return Applied{}, "", fmt.Errorf("No apply handler for proposal kind %s", pyjson.ReprString(p.Kind))
	}
	sidecar := filepath.Join(filepath.Dir(artifactPath), ".provenance", safePathComponent(artifactID)+".yaml")
	already := false
	if b, err := os.ReadFile(sidecar); err == nil {
		if o, err := yamlx.ParseObject(b); err == nil && o != nil {
			already = o.GetString("source_mission_id") == p.SourceMissionID && o.GetString("source_proposal_id") == p.ID
		}
	}
	if err := writeProvenance(sidecar, artifactID, p, actor, already); err != nil {
		return Applied{}, "", err
	}
	ap := Applied{p.ID, targetURN, artifactPath, sidecar, already}
	if already {
		return ap, "", nil
	}
	dir, slug := missionDirByID(root, missionID)
	if dir == "" {
		return ap, "", nil
	}
	rel, err := filepath.Rel(root, sidecar)
	if err != nil {
		rel = sidecar
	}
	ev := emitRetroEvent(dir, slug, missionID, actor, "retrospective.proposal.applied",
		jsonx.NewObject("applied_by", sortedActor(actor), "kind", p.Kind, "proposal_id", p.ID, "provenance_ref", filepath.ToSlash(rel), "target_urn", targetURN))
	return ap, ev, nil
}

func sortedActor(a LegacyActor) *jsonx.Object {
	var pid any
	if a.ProfileID != nil {
		pid = *a.ProfileID
	}
	return jsonx.NewObject("id", a.ID, "kind", a.Kind, "profile_id", pid)
}

func writeProvenance(sidecar, artifactID string, p LegacyProposal, actor LegacyActor, reApplied bool) error {
	os.MkdirAll(filepath.Dir(sidecar), 0o755)
	ev := []any{}
	for _, e := range p.EvidenceIDs {
		ev = append(ev, e)
	}
	rec := jsonx.NewObject("artifact_id", artifactID, "source", "retrospective", "source_mission_id", p.SourceMissionID,
		"source_proposal_id", p.ID, "source_evidence_event_ids", ev, "applied_by", actor.Object(), "applied_at", clock.NowUTCISO(),
		"re_applied", reApplied)
	return os.WriteFile(sidecar, []byte(yamlx.Emit(rec)), 0o644)
}

// emitRetroEvent is events.emit_retrospective_event (sorted-key row, then
// status.json materialization).
func emitRetroEvent(dir, slug, missionID string, actor LegacyActor, name string, payload *jsonx.Object) string {
	id := ulid.New()
	mid8 := missionID
	if len(mid8) > 8 {
		mid8 = mid8[:8]
	}
	row := jsonx.NewObject("actor", sortedActor(actor), "at", clock.NowUTCISO(), "event_id", id, "event_name", name, "mid8", mid8,
		"mission_id", missionID, "mission_slug", slug, "payload", payload)
	if err := status.AppendRows(filepath.Join(dir, "status.events.jsonl"), row); err != nil {
		return id
	}
	status.Materialize(dir)
	return id
}

func emitConflictRejections(missionID, root string, effective []LegacyProposal, groups []ConflictGroup, actor LegacyActor) []string {
	dir, slug := missionDirByID(root, missionID)
	if dir == "" {
		return []string{}
	}
	byID := map[string]LegacyProposal{}
	for _, p := range effective {
		byID[p.ID] = p
	}
	// Upstream iterates a set (nondeterministic order); first-group order here.
	var ids []string
	seen := map[string]bool{}
	for _, g := range groups {
		for _, id := range g.ProposalIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	var out []string
	for _, id := range ids {
		p, ok := byID[id]
		if !ok {
			continue
		}
		reason := "conflict"
		for _, g := range groups {
			for _, x := range g.ProposalIDs {
				if x == id {
					reason = g.Reason
					goto found
				}
			}
		}
	found:
		out = append(out, emitRetroEvent(dir, slug, missionID, actor, "retrospective.proposal.rejected",
			jsonx.NewObject("detail", reason, "kind", p.Kind, "proposal_id", id, "reason", "conflict", "rejected_by", sortedActor(actor))))
	}
	return out
}
