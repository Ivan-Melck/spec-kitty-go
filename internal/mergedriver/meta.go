package mergedriver

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Field sets mirrored from upstream (acceptance, mission_metadata,
// kernel.vcs_lock, migration.backfill_topology).
var (
	AcceptanceProvenanceFields = []string{"accepted_at", "accepted_by", "accepted_from_commit", "acceptance_mode", "accept_commit", "vcs", "vcs_locked_at"}
	MergeFields                = []string{"merged_at", "merged_by", "merged_into", "merged_strategy", "merged_push", "merged_commit"}
	FlattenCoupledKeys         = []string{"coordination_branch", "topology", "flattened"}
	vcsLockMetaFields          = map[string]bool{"vcs": true, "vcs_locked_at": true}
)

// AcceptanceHistoryField is ACCEPTANCE_HISTORY_FIELD.
const AcceptanceHistoryField = "acceptance_history"

// TargetAuthoritativeMetaFields is _TARGET_AUTHORITATIVE_META_FIELDS.
var TargetAuthoritativeMetaFields = func() []string {
	out := append([]string{}, AcceptanceProvenanceFields...)
	out = append(out, "mission_number", "status", "baseline_merge_commit")
	return append(out, MergeFields...)
}()

var targetAuthoritativeSet = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range TargetAuthoritativeMetaFields {
		m[k] = true
	}
	return m
}()

// coupled key groups, each sorted (tuple(sorted(group))).
var metaCoupledGroups = func() [][]string {
	var acc []string
	for _, k := range AcceptanceProvenanceFields {
		if !vcsLockMetaFields[k] {
			acc = append(acc, k)
		}
	}
	groups := [][]string{append([]string{}, FlattenCoupledKeys...), append([]string{}, MergeFields...), acc}
	for _, g := range groups {
		sort.Strings(g)
	}
	return groups
}()

// IsAssignedMissionNumber: a real positive integer (bool excluded).
func IsAssignedMissionNumber(v any) bool {
	switch t := v.(type) {
	case int:
		return t >= 1
	case int64:
		return t >= 1
	case interface{ String() string }:
		// json.Number big integers
		s := t.String()
		return s != "" && !strings.HasPrefix(s, "-") && strings.Trim(s, "0") != ""
	}
	return false
}

func unionAcceptanceHistory(theirs, ours any) ([]any, error) {
	var combined []any
	seen := map[string]bool{}
	for _, h := range []any{theirs, ours} {
		if !pyjson.Truthy(h) {
			continue
		}
		var items []any
		switch t := h.(type) {
		case []any:
			items = t
		case string:
			for _, r := range t {
				items = append(items, string(r))
			}
		case *jsonx.Object:
			for _, k := range t.Keys() {
				items = append(items, k)
			}
		default:
			return nil, &Crash{Type: "TypeError", Msg: fmt.Sprintf("'%s' object is not iterable", pyjson.TypeName(h))}
		}
		for _, e := range items {
			key := jsonx.Dumps(e, jsonx.Line)
			if seen[key] {
				continue
			}
			seen[key] = true
			combined = append(combined, e)
		}
	}
	type keyed struct {
		at, by string
		v      any
	}
	ks := make([]keyed, len(combined))
	for i, e := range combined {
		o, ok := e.(*jsonx.Object)
		if !ok {
			return nil, &Crash{Type: "AttributeError", Msg: fmt.Sprintf("'%s' object has no attribute 'get'", pyjson.TypeName(e))}
		}
		at, ok := o.Get("accepted_at")
		if !ok {
			at = ""
		}
		by, ok := o.Get("accepted_by")
		if !ok {
			by = ""
		}
		ks[i] = keyed{pyjson.Str(at), pyjson.Str(by), e}
	}
	sort.SliceStable(ks, func(i, j int) bool {
		if ks[i].at != ks[j].at {
			return ks[i].at < ks[j].at
		}
		return ks[i].by < ks[j].by
	})
	out := make([]any, len(ks))
	for i, k := range ks {
		out[i] = k.v
	}
	return out, nil
}

func reconcileMetaTwoWay(ours, theirs *jsonx.Object) (*jsonx.Object, error) {
	result := theirs.Clone()
	for _, key := range TargetAuthoritativeMetaFields {
		v, ok := ours.Get(key)
		if !ok {
			continue
		}
		if key == "mission_number" && !IsAssignedMissionNumber(v) {
			continue
		}
		result.Set(key, v)
	}
	th, _ := theirs.Get(AcceptanceHistoryField)
	oh, _ := ours.Get(AcceptanceHistoryField)
	hist, err := unionAcceptanceHistory(th, oh)
	if err != nil {
		return nil, err
	}
	if len(hist) > 0 {
		result.Set(AcceptanceHistoryField, hist)
	}
	return result, nil
}

type metaValue struct {
	present bool
	v       any
}

func unitValues(o *jsonx.Object, unit []string) []metaValue {
	out := make([]metaValue, len(unit))
	for i, k := range unit {
		v, ok := o.Get(k)
		out[i] = metaValue{ok, v}
	}
	return out
}

func unitEqual(a, b []metaValue) bool {
	for i := range a {
		if a[i].present != b[i].present {
			return false
		}
		if a[i].present && !pyjson.Equal(a[i].v, b[i].v) {
			return false
		}
	}
	return true
}

func metaMergeUnits(keys map[string]bool) [][]string {
	var units [][]string
	grouped := map[string]bool{AcceptanceHistoryField: true}
	for _, g := range metaCoupledGroups {
		touched := false
		for _, k := range g {
			if keys[k] {
				touched = true
			}
		}
		if touched {
			units = append(units, g)
			for _, k := range g {
				grouped[k] = true
			}
		}
	}
	var singles []string
	for k := range keys {
		if !grouped[k] {
			singles = append(singles, k)
		}
	}
	sort.Strings(singles)
	for _, k := range singles {
		units = append(units, []string{k})
	}
	return units
}

// ReconcileMetaPayloads is reconcile_meta_payloads: per-unit three-way
// merge when base is non-empty, else the two-way rule.
func ReconcileMetaPayloads(ours, theirs, base *jsonx.Object) (*jsonx.Object, error) {
	if base == nil || base.Len() == 0 {
		return reconcileMetaTwoWay(ours, theirs)
	}
	keys := map[string]bool{}
	for _, o := range []*jsonx.Object{base, ours, theirs} {
		for _, k := range o.Keys() {
			keys[k] = true
		}
	}
	result := &jsonx.Object{}
	for _, unit := range metaMergeUnits(keys) {
		bv, ov, tv := unitValues(base, unit), unitValues(ours, unit), unitValues(theirs, unit)
		var win []metaValue
		switch {
		case unitEqual(ov, tv) || unitEqual(tv, bv):
			win = ov
		case unitEqual(ov, bv):
			win = tv
		default:
			win = tv
			for _, k := range unit {
				if targetAuthoritativeSet[k] {
					win = ov
					break
				}
			}
		}
		for i, k := range unit {
			if win[i].present {
				result.Set(k, win[i].v)
			}
		}
	}
	// _apply_mission_number_guard
	if mn, _ := result.Get("mission_number"); !IsAssignedMissionNumber(mn) {
		for _, side := range []*jsonx.Object{ours, theirs} {
			if v, _ := side.Get("mission_number"); IsAssignedMissionNumber(v) {
				result.Set("mission_number", v)
				break
			}
		}
	}
	th, _ := theirs.Get(AcceptanceHistoryField)
	oh, _ := ours.Get(AcceptanceHistoryField)
	hist, err := unionAcceptanceHistory(th, oh)
	if err != nil {
		return nil, err
	}
	if len(hist) > 0 {
		result.Set(AcceptanceHistoryField, hist)
	}
	return result, nil
}

// loadMetaObject is _load_json_object.
func loadMetaObject(path, emptySide string) (*jsonx.Object, error) {
	if !exists(path) {
		return &jsonx.Object{}, nil
	}
	text, err := readText(path)
	if err != nil {
		var ue *pyjson.UnicodeError
		if errors.As(err, &ue) {
			return nil, &EventLogMergeError{fmt.Sprintf("%s: malformed meta.json (%s)", path, ue)}
		}
		return nil, err
	}
	if pyStrip(text) == "" {
		if emptySide != "" {
			return nil, &EventLogMergeError{fmt.Sprintf("%s: %s meta.json is empty; refusing a base-aware merge that would treat it as deleting every key", path, emptySide)}
		}
		return &jsonx.Object{}, nil
	}
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil, &EventLogMergeError{fmt.Sprintf("%s: malformed meta.json (Malformed JSON in %s: %s)", path, path, err)}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &EventLogMergeError{fmt.Sprintf("%s: meta.json is not a JSON object", path)}
	}
	return o, nil
}

// MetaJSON renders a meta.json payload like mission_metadata.write_meta
// (indent=2, ensure_ascii=False, sort_keys=True, trailing newline).
func MetaJSON(o *jsonx.Object) string {
	return jsonx.Dumps(o, jsonx.Options{Indent: 2, SortKeys: true}) + "\n"
}

// RunMeta is run_meta_driver (merge-driver-meta). twoWay is the
// MetaTwoWayEnv opt-out.
func RunMeta(basePath, oursPath, theirsPath string, twoWay bool) (Outcome, error) {
	base, ours, theirs, err := resolvePaths(basePath, oursPath, theirsPath)
	if err != nil {
		return Outcome{}, err
	}
	wrap := func(err error) error {
		var me *EventLogMergeError
		if errors.As(err, &me) {
			return newError(KindGeneric, me.Msg)
		}
		return err
	}
	basePayload := &jsonx.Object{}
	if !twoWay {
		if basePayload, err = loadMetaObject(base, ""); err != nil {
			return Outcome{}, wrap(err)
		}
	}
	emptyOurs, emptyTheirs := "", ""
	if basePayload.Len() > 0 {
		emptyOurs, emptyTheirs = "ours", "theirs"
	}
	o, err := loadMetaObject(ours, emptyOurs)
	if err != nil {
		return Outcome{}, wrap(err)
	}
	t, err := loadMetaObject(theirs, emptyTheirs)
	if err != nil {
		return Outcome{}, wrap(err)
	}
	merged, err := ReconcileMetaPayloads(o, t, basePayload)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{}, writeText(ours, MetaJSON(merged))
}

// RunMetaFromEnv runs RunMeta with twoWay read from the environment value
// of MetaTwoWayEnv (as the CLI shell does).
func RunMetaFromEnv(getenv func(string) string) Body {
	return func(b, o, t string) (Outcome, error) { return RunMeta(b, o, t, getenv(MetaTwoWayEnv) == "1") }
}
