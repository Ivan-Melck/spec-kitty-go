package retrospective

import (
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
)

// This file ports the validity half of retrospective.reader.read_record:
// the pydantic RetrospectiveRecord (schema_version "1", the "legacy" nested
// record shape the synthesizer applies proposals from). Only validity and
// the parsed proposals matter to callers: on a validation failure the CLI
// falls back to the generator-record reader and reports its diagnosis.

// LegacyActor is schema.ActorRef.
type LegacyActor struct {
	Kind, ID  string
	ProfileID *string
}

// Object is ActorRef.model_dump().
func (a LegacyActor) Object() *jsonx.Object {
	var pid any
	if a.ProfileID != nil {
		pid = *a.ProfileID
	}
	return jsonx.NewObject("kind", a.Kind, "id", a.ID, "profile_id", pid)
}

// LegacyProposal is schema.Proposal (payload kept raw; validated).
type LegacyProposal struct {
	ID, Kind        string
	Payload         *jsonx.Object
	Status          string
	SourceMissionID string
	EvidenceIDs     []string
}

// LegacyRecord is schema.RetrospectiveRecord (the parts the synthesizer reads).
type LegacyRecord struct {
	Proposals []LegacyProposal
}

var (
	reULID     = regexp.MustCompile(`^[0-9A-HJ-KM-NP-TV-Z]{26}$`)
	reSafeSlug = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
)

type lv struct{ ok bool }

func (v *lv) fail() { v.ok = false }

func obj(x any) (*jsonx.Object, bool) { o, ok := x.(*jsonx.Object); return o, ok }

func (v *lv) keys(o *jsonx.Object, allowed ...string) {
	set := map[string]bool{}
	for _, k := range allowed {
		set[k] = true
	}
	for _, kv := range o.Pairs() {
		if !set[kv.Key] {
			v.fail()
		}
	}
}

// str requires a present str value (required) or allows absence/None
// (optional+nullable).
func (v *lv) str(o *jsonx.Object, k string, required, nullable bool, check func(string) bool) string {
	x, ok := o.Get(k)
	if !ok {
		if required {
			v.fail()
		}
		return ""
	}
	if x == nil && nullable {
		return ""
	}
	s, isStr := x.(string)
	if !isStr {
		v.fail()
		return ""
	}
	if check != nil && !check(s) {
		v.fail()
	}
	return s
}

func oneOf(vals ...string) func(string) bool {
	return func(s string) bool {
		for _, x := range vals {
			if s == x {
				return true
			}
		}
		return false
	}
}

func nonEmpty(s string) bool { return s != "" }
func isULID(s string) bool   { return reULID.MatchString(s) }
func maxLen(n int) func(string) bool {
	return func(s string) bool { return len([]rune(s)) <= n }
}
func safeSlug(s string) bool {
	return reSafeSlug.MatchString(s) && !strings.HasPrefix(s, ".") && !strings.Contains(s, "..")
}

func (v *lv) model(o *jsonx.Object, k string, required, nullable bool, fn func(*jsonx.Object)) {
	x, ok := o.Get(k)
	if !ok {
		if required {
			v.fail()
		}
		return
	}
	if x == nil && nullable {
		return
	}
	m, isObj := obj(x)
	if !isObj {
		v.fail()
		return
	}
	fn(m)
}

func (v *lv) list(o *jsonx.Object, k string, required bool, minN, maxN int, item func(any)) int {
	x, ok := o.Get(k)
	if !ok {
		if required {
			v.fail()
		}
		return 0
	}
	l, isList := x.([]any)
	if !isList {
		v.fail()
		return 0
	}
	if len(l) < minN || (maxN > 0 && len(l) > maxN) {
		v.fail()
	}
	for _, it := range l {
		item(it)
	}
	return len(l)
}

func (v *lv) strItem(check func(string) bool) func(any) {
	return func(x any) {
		s, ok := x.(string)
		if !ok || (check != nil && !check(s)) {
			v.fail()
		}
	}
}

func (v *lv) actor(o *jsonx.Object) LegacyActor {
	v.keys(o, "kind", "id", "profile_id")
	a := LegacyActor{Kind: v.str(o, "kind", true, false, oneOf("human", "agent", "runtime")), ID: v.str(o, "id", true, false, nil)}
	if x, ok := o.Get("profile_id"); ok && x != nil {
		s := v.str(o, "profile_id", false, true, nil)
		a.ProfileID = &s
	}
	return a
}

func (v *lv) target(o *jsonx.Object) {
	v.keys(o, "kind", "urn")
	v.str(o, "kind", true, false, oneOf("doctrine_directive", "doctrine_tactic", "doctrine_procedure", "drg_edge", "drg_node",
		"glossary_term", "prompt_template", "test", "context_artifact"))
	v.str(o, "urn", true, false, nil)
}

func (v *lv) finding(x any) string {
	o, ok := obj(x)
	if !ok {
		v.fail()
		return ""
	}
	v.keys(o, "id", "target", "note", "provenance")
	id := v.str(o, "id", true, false, nil)
	v.model(o, "target", true, false, v.target)
	v.str(o, "note", true, false, maxLen(2000))
	v.model(o, "provenance", true, false, func(p *jsonx.Object) {
		v.keys(p, "source_mission_id", "evidence_event_ids", "actor", "captured_at")
		v.str(p, "source_mission_id", true, false, isULID)
		v.list(p, "evidence_event_ids", true, 1, 0, v.strItem(isULID))
		v.model(p, "actor", true, false, func(a *jsonx.Object) { v.actor(a) })
		v.str(p, "captured_at", true, false, nonEmpty)
	})
	return id
}

func (v *lv) edge(o *jsonx.Object) {
	v.keys(o, "from_node", "to_node", "kind")
	v.str(o, "from_node", true, false, nil)
	v.str(o, "to_node", true, false, nil)
	v.str(o, "kind", true, false, nil)
}

func (v *lv) payload(o *jsonx.Object) {
	kind, _ := o.Get("kind")
	switch kind {
	case "synthesize_directive", "synthesize_tactic", "synthesize_procedure":
		v.keys(o, "kind", "artifact_id", "body", "body_hash", "scope")
		v.str(o, "artifact_id", true, false, safeSlug)
		v.str(o, "body", true, false, nil)
		v.str(o, "body_hash", true, false, nil)
		v.model(o, "scope", true, false, func(s *jsonx.Object) {
			v.keys(s, "actions", "profiles")
			v.list(s, "actions", false, 0, 0, v.strItem(nil))
			v.list(s, "profiles", false, 0, 0, v.strItem(nil))
		})
	case "rewire_edge":
		v.keys(o, "kind", "edge_old", "edge_new")
		v.model(o, "edge_old", true, false, v.edge)
		v.model(o, "edge_new", true, false, v.edge)
	case "add_edge", "remove_edge":
		v.keys(o, "kind", "edge")
		v.model(o, "edge", true, false, v.edge)
	case "add_glossary_term", "update_glossary_term":
		v.keys(o, "kind", "term_key", "definition", "definition_hash", "related_terms")
		v.str(o, "term_key", true, false, safeSlug)
		v.str(o, "definition", true, false, nil)
		v.str(o, "definition_hash", true, false, nil)
		v.list(o, "related_terms", false, 0, 0, v.strItem(nil))
	case "flag_not_helpful":
		v.keys(o, "kind", "target")
		v.model(o, "target", true, false, v.target)
	default:
		v.fail()
	}
}

func (v *lv) proposal(x any) LegacyProposal {
	var p LegacyProposal
	o, ok := obj(x)
	if !ok {
		v.fail()
		return p
	}
	v.keys(o, "id", "kind", "payload", "rationale", "state", "provenance")
	p.ID = v.str(o, "id", true, false, isULID)
	p.Kind = v.str(o, "kind", true, false, oneOf("synthesize_directive", "synthesize_tactic", "synthesize_procedure", "rewire_edge",
		"add_edge", "remove_edge", "add_glossary_term", "update_glossary_term", "flag_not_helpful"))
	v.model(o, "payload", true, false, func(po *jsonx.Object) {
		p.Payload = po
		v.payload(po)
	})
	v.str(o, "rationale", true, false, maxLen(2000))
	v.model(o, "state", true, false, func(s *jsonx.Object) {
		v.keys(s, "status", "decided_at", "decided_by", "apply_attempts")
		p.Status = v.str(s, "status", true, false, oneOf("pending", "accepted", "rejected", "applied", "superseded"))
		v.str(s, "decided_at", false, true, nonEmpty)
		v.model(s, "decided_by", false, true, func(a *jsonx.Object) { v.actor(a) })
		v.list(s, "apply_attempts", false, 0, 0, func(x any) {
			a, ok := obj(x)
			if !ok {
				v.fail()
				return
			}
			v.keys(a, "attempt_id", "at", "outcome", "error")
			v.str(a, "attempt_id", true, false, isULID)
			v.str(a, "at", true, false, nonEmpty)
			v.str(a, "outcome", true, false, oneOf("applied", "rejected_conflict", "rejected_stale", "rejected_invalid"))
			v.str(a, "error", false, true, nil)
		})
	})
	v.model(o, "provenance", true, false, func(pr *jsonx.Object) {
		v.keys(pr, "source_mission_id", "source_evidence_event_ids", "authored_by", "approved_by")
		p.SourceMissionID = v.str(pr, "source_mission_id", true, false, isULID)
		v.list(pr, "source_evidence_event_ids", true, 0, 0, func(x any) {
			s, ok := x.(string)
			if !ok || !isULID(s) {
				v.fail()
				return
			}
			p.EvidenceIDs = append(p.EvidenceIDs, s)
		})
		v.model(pr, "authored_by", true, false, func(a *jsonx.Object) { v.actor(a) })
		v.model(pr, "approved_by", false, true, func(a *jsonx.Object) { v.actor(a) })
	})
	return p
}

// ParseLegacyRecord validates data (a YAML mapping) as RetrospectiveRecord
// (after _coerce_legacy_schema_versions); ok=false when invalid.
func ParseLegacyRecord(data *jsonx.Object) (*LegacyRecord, bool) {
	o := data.Clone()
	if sv, ok := o.Get("schema_version"); ok {
		if n, isInt := pyIntStr(sv); isInt {
			o.Set("schema_version", n)
		}
	}
	if pr, ok := obj(mustGet(o, "provenance")); ok {
		if sv, ok := pr.Get("schema_version"); ok {
			if n, isInt := pyIntStr(sv); isInt {
				c := pr.Clone()
				c.Set("schema_version", n)
				o.Set("provenance", c)
			}
		}
	}
	v := &lv{ok: true}
	rec := &LegacyRecord{}
	v.keys(o, "schema_version", "mission", "mode", "status", "started_at", "completed_at", "actor", "helped", "not_helpful",
		"gaps", "proposals", "provenance", "skip_reason", "failure", "successor_mission_id")
	v.str(o, "schema_version", true, false, oneOf("1"))
	v.model(o, "mission", true, false, func(m *jsonx.Object) {
		v.keys(m, "mission_id", "mid8", "mission_slug", "mission_type", "mission_started_at", "mission_completed_at")
		v.str(m, "mission_id", true, false, isULID)
		v.str(m, "mid8", true, false, func(s string) bool { return len([]rune(s)) == 8 })
		v.str(m, "mission_slug", true, false, nil)
		v.str(m, "mission_type", true, false, nil)
		v.str(m, "mission_started_at", true, false, nonEmpty)
		v.str(m, "mission_completed_at", false, true, nonEmpty)
	})
	v.model(o, "mode", true, false, func(m *jsonx.Object) {
		v.keys(m, "value", "source_signal")
		v.str(m, "value", true, false, oneOf("autonomous", "human_in_command"))
		v.model(m, "source_signal", true, false, func(s *jsonx.Object) {
			v.keys(s, "kind", "evidence")
			v.str(s, "kind", true, false, oneOf("charter_override", "explicit_flag", "environment", "parent_process"))
			v.str(s, "evidence", true, false, nil)
		})
	})
	status := v.str(o, "status", true, false, oneOf("completed", "skipped", "failed", "pending"))
	v.str(o, "started_at", true, false, nonEmpty)
	completed := v.str(o, "completed_at", false, true, nonEmpty)
	v.model(o, "actor", true, false, func(a *jsonx.Object) { v.actor(a) })
	var findingIDs []string
	for _, k := range []string{"helped", "not_helpful", "gaps"} {
		v.list(o, k, false, 0, 0, func(x any) { findingIDs = append(findingIDs, v.finding(x)) })
	}
	v.list(o, "proposals", false, 0, 0, func(x any) { rec.Proposals = append(rec.Proposals, v.proposal(x)) })
	v.model(o, "provenance", true, false, func(p *jsonx.Object) {
		v.keys(p, "authored_by", "runtime_version", "written_at", "schema_version")
		v.model(p, "authored_by", true, false, func(a *jsonx.Object) { v.actor(a) })
		v.str(p, "runtime_version", true, false, nil)
		v.str(p, "written_at", true, false, nonEmpty)
		v.str(p, "schema_version", true, false, oneOf("1"))
	})
	skip := v.str(o, "skip_reason", false, true, nil)
	hasFailure := false
	v.model(o, "failure", false, true, func(f *jsonx.Object) {
		hasFailure = true
		v.keys(f, "code", "message", "error_chain")
		v.str(f, "code", true, false, oneOf("writer_io_error", "schema_invalid", "facilitator_error", "evidence_unreachable", "mode_resolution_error", "internal_error"))
		v.str(f, "message", true, false, nil)
		v.list(f, "error_chain", true, 0, 16, v.strItem(nil))
	})
	v.str(o, "successor_mission_id", false, true, isULID)
	if !v.ok {
		return nil, false
	}
	// Model validators (after): status conditionals, unique ids.
	switch status {
	case "completed":
		if completed == "" {
			return nil, false
		}
	case "skipped":
		if skip == "" {
			return nil, false
		}
	case "failed":
		if !hasFailure {
			return nil, false
		}
	case "pending":
		return nil, false
	}
	seen := map[string]bool{}
	for _, id := range findingIDs {
		if seen[id] {
			return nil, false
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, p := range rec.Proposals {
		if seen[p.ID] {
			return nil, false
		}
		seen[p.ID] = true
	}
	return rec, true
}

// pyIntStr is str(x) for a YAML int (type(x) is int).
func pyIntStr(x any) (string, bool) {
	switch n := x.(type) {
	case int:
		return strconv.Itoa(n), true
	case int64:
		return strconv.FormatInt(n, 10), true
	case *big.Int:
		return n.String(), true
	}
	return "", false
}

func mustGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

// ReadLegacyRecord is read_record(path): (nil, false) when the file does not
// parse as a YAML mapping or fails the RetrospectiveRecord schema.
func ReadLegacyRecord(path string) (*LegacyRecord, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	v, err := pyyaml.SafeLoad(string(b))
	if err != nil {
		return nil, false
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, false
	}
	return ParseLegacyRecord(o)
}
