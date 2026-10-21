package missionstate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// rowResult is _CanonicalRowResult.
type rowResult struct {
	row      *jsonx.Object // nil = quarantined (when err == "")
	actions  []string
	err      string
	verbatim bool
}

func eventIDOf(o *jsonx.Object) *string {
	v, ok := o.Get("event_id")
	if !ok || v == nil {
		return nil
	}
	s := pyjson.Str(v)
	return &s
}

// canonicalizeStatusRows is _canonicalize_status_rows.
func canonicalizeStatusRows(root, dir string, rows []rawRow, slug, missionID string, generated *[]string) (canon []string, changes []RowTransformation, quarantine []string, errs []string, seen map[string]bool) {
	seen = map[string]bool{}
	rel := repoRelpath(root, filepath.Join(dir, EventsFile))
	for _, r := range rows {
		res := canonicalizeStatusRow(r.data, slug, missionID, r.line, generated)
		oldSHA := sha256Text(r.text)
		if res.err != "" {
			errs = append(errs, fmt.Sprintf("%s:%d: %s", rel, r.line, res.err))
			continue
		}
		if res.row == nil {
			quarantine = append(quarantine, r.text)
			changes = append(changes, RowTransformation{ArtifactPath: rel, LineNumber: r.line, EventID: eventIDOf(r.data), Actions: res.actions, OldSHA: oldSHA})
			continue
		}
		eid := eventIDOf(res.row)
		actions := append([]string{}, res.actions...)
		if eid != nil && seen[*eid] {
			actions = append(actions, duplicateEventIDDropped)
			quarantine = append(quarantine, r.text)
			changes = append(changes, RowTransformation{ArtifactPath: rel, LineNumber: r.line, EventID: eid, Actions: actions, OldSHA: oldSHA})
			continue
		}
		if eid != nil {
			seen[*eid] = true
		} else {
			seen[""] = true
		}
		text := r.text
		if !(res.verbatim || (len(actions) == 0 && semanticallyCanonical(r.data, res.row))) {
			text = status.SerializeLine(res.row)
		}
		canon = append(canon, text)
		if text != r.text || len(actions) > 0 {
			ns := sha256Text(text)
			changes = append(changes, RowTransformation{ArtifactPath: rel, LineNumber: r.line, EventID: eid, Actions: actions, OldSHA: oldSHA, NewSHA: &ns})
		}
	}
	return
}

func semanticallyCanonical(original, canonical *jsonx.Object) bool {
	trimmed := jsonx.NewObject()
	for _, kv := range original.Pairs() {
		if kv.Value == nil && nullOmittedKeys[kv.Key] {
			continue
		}
		trimmed.Set(kv.Key, kv.Value)
	}
	return pyjson.Equal(trimmed, canonical)
}

// isPreservedNonLaneRow is _is_preserved_non_lane_row.
func isPreservedNonLaneRow(o *jsonx.Object) bool {
	if valueOf(o, "kind") == status.AnnotationKind {
		return true
	}
	return status.IsNonLaneRow(o)
}

func isLegacyTypedLaneTransition(o *jsonx.Object) bool {
	if valueOf(o, "event_type") != envelopeEventType {
		return false
	}
	for _, f := range legacyTypedLaneField {
		if !o.Has(f) {
			return false
		}
	}
	return true
}

func isLegacyIdentityRow(o *jsonx.Object) bool {
	for _, k := range forbiddenLegacyKeys {
		if o.Has(k) {
			return true
		}
	}
	for _, a := range statusRowAliases {
		if o.Has(a[0]) {
			return true
		}
	}
	return false
}

func validEventID(v any) bool {
	s, ok := v.(string)
	return ok && (ulidRe.MatchString(s) || uuidHyphenRe.MatchString(s) || uuidBareRe.MatchString(s))
}

// canonicalizeStatusRow is _canonicalize_status_row (the ordered rule
// pipeline; the first error short-circuits).
func canonicalizeStatusRow(data *jsonx.Object, slug, missionID string, line int, generated *[]string) rowResult {
	row := data.Clone()
	var actions []string
	fail := func(e string) rowResult { return rowResult{actions: actions, err: e} }

	// 1. reject / preserve non-lane rows
	if !isLegacyTypedLaneTransition(row) {
		if isPreservedNonLaneRow(row) {
			return rowResult{row: data.Clone(), verbatim: true}
		}
		if row.Has("event_name") || row.Has("event_type") {
			return rowResult{actions: []string{quarantinedNonStatusEvent}}
		}
	}
	// 1b. backfill mission_id on legacy-shaped rows
	if missionID != "" && !pyjson.Truthy(valueOf(row, "mission_id")) && isLegacyIdentityRow(row) {
		row.Set("mission_id", missionID)
		actions = append(actions, "mission_id_backfilled")
	}
	// 2. aliases
	for _, a := range statusRowAliases {
		if v, ok := row.Get(a[0]); ok {
			if nv, has := row.Get(a[1]); !has || !pyjson.Truthy(nv) {
				row.Set(a[1], v)
			}
			row.Delete(a[0])
			actions = append(actions, "renamed_key:"+a[0]+"->"+a[1])
		}
	}
	// 3. strip legacy keys
	for _, k := range forbiddenLegacyKeys {
		if k == "feature_slug" {
			continue
		}
		if row.Has(k) {
			row.Delete(k)
			actions = append(actions, "removed_key:"+k)
		}
	}
	// 4. stamp identity
	if !pyjson.Truthy(valueOf(row, "mission_slug")) {
		row.Set("mission_slug", slug)
		actions = append(actions, "mission_slug_backfilled")
	}
	// 5. mint event id
	if !validEventID(valueOf(row, "event_id")) {
		seed := row.Clone()
		seed.Set("mission_id", missionID)
		minted := DeterministicULID(fmt.Sprintf("%s:line:%d", jsonx.Dumps(seed, jsonx.Line), line))
		row.Set("event_id", minted)
		*generated = append(*generated, minted)
		actions = append(actions, "event_id_deterministically_backfilled")
	}
	// 6. default at
	if !pyjson.Truthy(valueOf(row, "at")) {
		row.Set("at", epochTimestamp)
		actions = append(actions, "at_defaulted")
	}
	// 7. default from_lane
	if valueOf(row, "from_lane") == nil {
		row.Set("from_lane", "planned")
		actions = append(actions, "from_lane_defaulted")
	}
	// 8/9. required fields
	if !pyjson.Truthy(valueOf(row, "to_lane")) {
		return fail("missing required to_lane")
	}
	if !pyjson.Truthy(valueOf(row, "wp_id")) {
		return fail("missing required wp_id")
	}
	// 10. normalize lanes, actor, force, execution_mode; round-trip
	for _, k := range strCoercedKeys {
		if v, ok := row.Get(k); ok && v != nil {
			if _, isStr := v.(string); !isStr {
				row.Set(k, pyjson.Str(v))
				actions = append(actions, "coerced_type:"+k)
			}
		}
	}
	if v, ok := row.Get("force"); ok {
		if _, isBool := v.(bool); !isBool {
			row.Set("force", pyjson.Truthy(v))
			actions = append(actions, "coerced_type:force")
		}
	}
	for _, k := range []string{"from_lane", "to_lane"} {
		lane := pyjson.Str(valueOf(row, k))
		norm := lane
		if a, ok := status.LaneAliases[lane]; ok {
			norm = string(a)
		}
		if !status.IsLane(norm) {
			return fail(fmt.Sprintf("unknown %s %s", k, pyjson.ReprString(lane)))
		}
		if norm != lane {
			actions = append(actions, fmt.Sprintf("lane_alias:%s:%s->%s", k, lane, norm))
			row.Set(k, norm)
		}
	}
	normalizeActorField(row, &actions)
	if !row.Has("force") {
		row.Set("force", false)
		actions = append(actions, "force_defaulted")
	}
	if m, _ := valueOf(row, "execution_mode").(string); !validExecutionModes[m] {
		row.Set("execution_mode", "direct_repo")
		actions = append(actions, "execution_mode_defaulted")
	}
	ev, err := status.EventFromObject(row)
	if err != nil {
		return fail(err.Error())
	}
	canonical := ev.ToObject()
	var dropped []string
	for _, kv := range row.Pairs() {
		if canonical.Has(kv.Key) {
			continue
		}
		if kv.Value == nil && nullOmittedKeys[kv.Key] {
			continue
		}
		dropped = append(dropped, kv.Key)
	}
	sort.Strings(dropped)
	for _, k := range dropped {
		actions = append(actions, "removed_field:"+k)
	}
	return rowResult{row: canonical, actions: actions}
}

func normalizeActorField(row *jsonx.Object, actions *[]string) {
	actor, _ := row.Get("actor")
	if o, ok := actor.(*jsonx.Object); ok {
		if _, err := status.DecodeActor(o); err == nil {
			return
		}
		md := jsonx.NewObject()
		if pm, ok := valueOf(row, "policy_metadata").(*jsonx.Object); ok && pm != nil {
			md = pm.Clone()
		}
		setDefault(md, "migration_original_actor", o)
		row.Set("policy_metadata", md)
		row.Set("actor", actorLabel(o))
		*actions = append(*actions, "actor_dict_labelled")
		return
	}
	if s, ok := actor.(string); !ok || strings.TrimSpace(s) == "" {
		row.Set("actor", migrationActor)
		*actions = append(*actions, "actor_defaulted")
	}
}

func actorLabel(o *jsonx.Object) string {
	for _, k := range []string{"profile", "role", "display_name", "actor_id", "name"} {
		if s, ok := valueOf(o, k).(string); ok && strings.TrimSpace(s) != "" {
			a := actorSafeRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
			a = strings.Trim(a, "-_.:")
			if a == "" {
				return migrationActor
			}
			return a
		}
	}
	return structuredActorLabel
}

// quarantineViolations is _registry_authoritative_quarantine_violations.
func quarantineViolations(lines []string, surviving map[string]bool) []string {
	var out []string
	for _, l := range lines {
		v, err := pyjson.Loads(l)
		if err != nil {
			continue
		}
		o, ok := v.(*jsonx.Object)
		if !ok || !status.IsNonLaneRow(o) {
			continue
		}
		eid := valueOf(o, "event_id")
		if s, ok := eid.(string); ok && surviving[s] {
			continue
		}
		out = append(out, fmt.Sprintf("registry_authoritative_row_quarantined: event_id=%s event_type=%s was dropped as a non-status event despite the durable reader (is_non_lane_event) treating it as non-lane and therefore preserved (#4993 guard)",
			pyjson.Repr(eid), pyjson.Repr(valueOf(o, "event_type"))))
	}
	return out
}
