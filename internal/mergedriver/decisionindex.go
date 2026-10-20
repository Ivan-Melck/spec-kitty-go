package mergedriver

import (
	"fmt"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Decision statuses (decisions.models.DecisionStatus).
const (
	DecisionOpen     = "open"
	DecisionResolved = "resolved"
	DecisionDeferred = "deferred"
	DecisionCanceled = "canceled"
)

var decisionStatuses = []string{DecisionOpen, DecisionResolved, DecisionDeferred, DecisionCanceled}

func decisionTerminal(s string) bool {
	return s == DecisionResolved || s == DecisionDeferred || s == DecisionCanceled
}

// IsAllowedTerminalReopen is decisions.index_fold.is_allowed_terminal_reopen:
// today only deferred -> resolved.
func IsAllowedTerminalReopen(current, target string) bool {
	return current == DecisionDeferred && target == DecisionResolved
}

func decErr(msg string) error { return newError(KindDecisionIndex, msg) }

// decisionIndexEntries is _decision_index_entries: entries keyed by
// decision_id, in first-seen order.
func decisionIndexEntries(doc *jsonx.Object, side string) ([]string, map[string]*jsonx.Object, error) {
	raw, ok := doc.Get("entries")
	if !ok {
		raw = []any{}
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, nil, decErr(fmt.Sprintf("%s: 'entries' is not a list (%s)", side, pyjson.Repr(raw)))
	}
	var order []string
	by := map[string]*jsonx.Object{}
	for _, e := range list {
		entry, ok := e.(*jsonx.Object)
		if !ok {
			return nil, nil, decErr(fmt.Sprintf("%s: entry is not a JSON object (%s)", side, pyjson.Repr(e)))
		}
		idv, _ := entry.Get("decision_id")
		id, ok := idv.(string)
		if !ok || id == "" {
			return nil, nil, decErr(fmt.Sprintf("%s: entry missing 'decision_id' (%s)", side, pyjson.Repr(entry)))
		}
		if _, seen := by[id]; !seen {
			order = append(order, id)
		}
		by[id] = entry.Clone()
	}
	return order, by, nil
}

func decisionEntryStatus(entry *jsonx.Object, side, id string) (string, error) {
	v, _ := entry.Get("status")
	if s, ok := v.(string); ok {
		for _, st := range decisionStatuses {
			if s == st {
				return s, nil
			}
		}
	}
	return "", decErr(fmt.Sprintf("%s: decision %s has an invalid 'status' (%s)", side, pyjson.ReprString(id), pyjson.Repr(v)))
}

// resolveDecisionEntry is _resolve_decision_entry.
func resolveDecisionEntry(id string, ours, theirs *jsonx.Object) (*jsonx.Object, error) {
	switch {
	case ours == nil:
		return theirs, nil
	case theirs == nil:
		return ours, nil
	case pyjson.Equal(ours, theirs):
		return ours, nil
	}
	os_, err := decisionEntryStatus(ours, "ours", id)
	if err != nil {
		return nil, err
	}
	ts, err := decisionEntryStatus(theirs, "theirs", id)
	if err != nil {
		return nil, err
	}
	ot, tt := decisionTerminal(os_), decisionTerminal(ts)
	switch {
	case ot && !tt:
		return ours, nil
	case tt && !ot:
		return theirs, nil
	case ot && tt && os_ != ts:
		if IsAllowedTerminalReopen(os_, ts) {
			return theirs, nil
		}
		if IsAllowedTerminalReopen(ts, os_) {
			return ours, nil
		}
	}
	return nil, decErr(fmt.Sprintf("decision %s diverged on both sides with no safe precedence "+
		"(ours status=%s, theirs status=%s) "+
		"-- refusing to silently pick a side", pyjson.ReprString(id), pyjson.ReprString(os_), pyjson.ReprString(ts)))
}

// UnionDecisionIndex is union_decision_index: entries unioned by
// decision_id (terminal beats open; deferred->resolved reopen takes the
// target), sorted by (created_at, decision_id); other top-level keys are
// target-authoritative (ours unless None/"").
func UnionDecisionIndex(ours, theirs *jsonx.Object) (*jsonx.Object, error) {
	oOrder, oBy, err := decisionIndexEntries(ours, "ours")
	if err != nil {
		return nil, err
	}
	tOrder, tBy, err := decisionIndexEntries(theirs, "theirs")
	if err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, oOrder...), tOrder...) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	type keyed struct {
		created, id string
		e           *jsonx.Object
	}
	var ks []keyed
	for _, id := range ids {
		e, err := resolveDecisionEntry(id, oBy[id], tBy[id])
		if err != nil {
			return nil, err
		}
		ks = append(ks, keyed{pyjson.Str(getOr(e, "created_at", "")), pyjson.Str(getOr(e, "decision_id", "")), e})
	}
	sort.SliceStable(ks, func(i, j int) bool {
		if ks[i].created != ks[j].created {
			return ks[i].created < ks[j].created
		}
		return ks[i].id < ks[j].id
	})
	entries := make([]any, len(ks))
	for i, k := range ks {
		entries[i] = k.e
	}
	result := &jsonx.Object{}
	var keys []string
	kseen := map[string]bool{"entries": true}
	for _, o := range []*jsonx.Object{ours, theirs} {
		for _, k := range o.Keys() {
			if !kseen[k] {
				kseen[k] = true
				keys = append(keys, k)
			}
		}
	}
	for _, k := range keys {
		v, _ := ours.Get(k)
		if v == nil || v == "" {
			v, _ = theirs.Get(k)
		}
		result.Set(k, v)
	}
	result.Set("entries", entries)
	return result, nil
}

// ReconcileDecisionIndexDocuments unions ours/theirs and round-trips the
// result through the DecisionIndex schema, returning the save_index bytes
// (sort_keys, indent=2, trailing newline).
func ReconcileDecisionIndexDocuments(ours, theirs *jsonx.Object) (string, error) {
	merged, err := UnionDecisionIndex(ours, theirs)
	if err != nil {
		return "", err
	}
	idx, verr := ValidateDecisionIndex(merged)
	if verr != "" {
		return "", newError(KindGeneric, "decisions/index.json: merged document failed schema validation ("+verr+")")
	}
	return jsonx.Dumps(idx, jsonx.SortedPretty) + "\n", nil
}

// RunDecisionIndex is run_decision_index_driver (merge-driver-decision-index).
func RunDecisionIndex(basePath, oursPath, theirsPath string) (Outcome, error) {
	_, ours, theirs, err := resolvePaths(basePath, oursPath, theirsPath)
	if err != nil {
		return Outcome{}, err
	}
	o, err := parseJSONDocument(ours)
	if err != nil {
		return Outcome{}, err
	}
	t, err := parseJSONDocument(theirs)
	if err != nil {
		return Outcome{}, err
	}
	payload, err := ReconcileDecisionIndexDocuments(o, t)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{}, writeText(ours, payload)
}
