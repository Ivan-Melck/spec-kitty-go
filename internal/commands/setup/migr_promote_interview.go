package setup

// 4.0.0rc8_promote_interview_selections (#6629): promote interview selections
// into PRESENT activation lists only, through the activation write target
// (the activation ledger once 4.0.0rc8_activation_ledger moved the keys).
// It supersedes unify_charter_activation_promote_answers, which seeded an
// absent activated_<kind> key with the whole in-force set.
//
// Rules: an absent key is never written; a present list gains the interview
// stems it lacks (compared as stems), minus the effective deny; nothing is
// promoted where the superseded promotion already ran; detect is always true
// so one run records success; a list an earlier build seeded is reported,
// never narrowed.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

const (
	promoteInterviewID          = "4.0.0rc8_promote_interview_selections"
	supersededPromotionID       = "unify_charter_activation_promote_answers"
	promoteNothingToPromote     = "No interview selections to promote: every selection is already in force"
	promoteAlreadyPromoted      = "Interview selections were promoted by an earlier upgrade; nothing to promote"
	promoteNoSelections         = "No interview selections to promote"
	promoteNothingPreview       = "dry-run: nothing would be promoted"
	promoteSeededListHintFormat = "%s lists every available %s (%d), as an upgrade before 4.0.0rc8 wrote it; the interview selected %d. " +
		"To keep only those, run `spec-kitty charter interview --narrow-activation` or " +
		"`spec-kitty charter activate %s <id> --only`; `charter synthesize` then asks for companion tactics only for what you keep."
)

func init() {
	migrationImpls[promoteInterviewID] = migrationImpl{
		noWorktrees: true,
		detect:      func(string) bool { return true },
		canApply:    func(string) (bool, string) { return ok() },
		applyE:      applyPromoteInterviewSelections,
	}
}

// promotedKind is one PROMOTED_KINDS entry.
type promotedKind struct {
	token, plural string
	kind          dct.Kind
	resolvable    bool
}

func (k promotedKind) key() string { return "activated_" + k.plural }

func promotedKinds() []promotedKind {
	var out []promotedKind
	for _, pk := range promotableKinds {
		k, err := dct.ParseKind(pk[0])
		out = append(out, promotedKind{token: pk[0], plural: pk[1], kind: k, resolvable: err == nil})
	}
	return out
}

// stemResolver is _StemResolver: built-in declared ids and stems to stems,
// cached per (kind, id); "" means unresolved.
type stemResolver map[string]string

func (r stemResolver) stem(k promotedKind, raw string) string {
	key := k.token + "\x00" + raw
	if s, ok := r[key]; ok {
		return s
	}
	s := ""
	if k.resolvable {
		s = charter.SelectedIDToStem(k.kind, raw, nil, nil)
	}
	r[key] = s
	return s
}

// stems is _StemResolver.stems: an id that resolves to no stem is kept as written.
func (r stemResolver) stems(k promotedKind, raw []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range raw {
		if s := r.stem(k, id); s != "" {
			out[s] = true
		} else {
			out[id] = true
		}
	}
	return out
}

func selectedInterviewIDs(answers *jsonx.Object, k promotedKind) []string {
	raw, _ := answers.Get("selected_" + k.plural)
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, pyStrValue(x))
	}
	return out
}

// shippedPromotionSettled is _shipped_promotion_settled: the ledger records
// that the superseded promotion itself ran (success, or a skipped it recorded
// without a superseded_by note), or a pending ledger adoption will record it.
func shippedPromotionSettled(root string) bool {
	records, err := readMigrationRecords(root, false)
	if err == nil {
		for _, r := range records {
			if r.ID == supersededPromotionID && (r.Result == "success" || r.Result == "skipped" && r.SupersededBy == "") {
				return true
			}
		}
	}
	var adopting []migrationInfo
	for _, m := range migrationCatalog {
		if m.LedgerAdoption && !migrationSettled(root, m) {
			adopting = append(adopting, m)
		}
	}
	if len(adopting) == 0 {
		return false
	}
	if ids, err := pendingAdoptionIDs(root, adopting); err == nil && ids[supersededPromotionID] {
		return true
	}
	if md := kittify.LoadMetadata(root); md != nil {
		for _, r := range md.Applied {
			id := r.ID
			if mapped, ok := kittify.LegacyMigrationID(id); ok {
				id = mapped
			}
			if id == supersededPromotionID && r.Result == "skipped" {
				return true
			}
		}
	}
	return false
}

func applyPromoteInterviewSelections(root string, dryRun bool) (migrationResult, error) {
	answers := loadYAMLMappingSafe(answersPath(root))
	kinds := promotedKinds()
	hasSelections := false
	for _, k := range kinds {
		hasSelections = hasSelections || len(selectedInterviewIDs(answers, k)) > 0
	}
	if !hasSelections {
		return migrationResult{Success: true, Changes: []string{promoteNoSelections}}, nil
	}
	resolver := stemResolver{}
	selected := map[string][]string{}
	var unresolved []string
	for _, k := range kinds {
		var stems []string
		seen := map[string]bool{}
		for _, raw := range selectedInterviewIDs(answers, k) {
			s := resolver.stem(k, raw)
			if s == "" {
				unresolved = append(unresolved, k.token+":"+raw)
			} else if !seen[s] {
				seen[s] = true
				stems = append(stems, s)
			}
		}
		if len(stems) > 0 {
			selected[k.key()] = stems
		}
	}

	target, err := charter.LoadWriteTarget(root)
	if err != nil {
		return migrationResult{}, err
	}
	// _present_lists: only a present list of a selected kind.
	var presentKinds []promotedKind
	present := map[string][]string{}
	for _, k := range kinds {
		raw, ok := target.Data.Get(k.key())
		list, isList := raw.([]any)
		if _, sel := selected[k.key()]; !sel || !ok || !isList {
			continue
		}
		var ids []string
		for _, x := range list {
			ids = append(ids, pyStrValue(x))
		}
		presentKinds = append(presentKinds, k)
		present[k.key()] = ids
	}
	warnings := seededListHints(root, presentKinds, present, selected, resolver)
	if shippedPromotionSettled(root) {
		nothing := promoteAlreadyPromoted
		if dryRun {
			nothing = promoteNothingPreview
		}
		return migrationResult{Success: true, Changes: []string{nothing}, Warnings: warnings}, nil
	}
	// _promotions: each present list's missing interview stems, in answers order.
	type promotion struct {
		k       promotedKind
		missing []string
	}
	var promotions []promotion
	for _, k := range presentKinds {
		listed := resolver.stems(k, present[k.key()])
		var missing []string
		for _, s := range selected[k.key()] {
			if !listed[s] {
				missing = append(missing, s)
			}
		}
		if len(missing) > 0 {
			promotions = append(promotions, promotion{k, missing})
		}
	}
	if len(unresolved) > 0 {
		warnings = append(warnings, "Unresolved interview selections skipped (not promoted): "+strings.Join(unresolved, ", "))
	}
	if len(promotions) == 0 {
		nothing := promoteNothingToPromote
		if dryRun {
			nothing = promoteNothingPreview
		}
		return migrationResult{Success: true, Changes: []string{nothing}, Warnings: warnings}, nil
	}

	// promote_activations over present keys only: append-only, the effective
	// deny (deny less the org required floor, read from the ledger) skipped.
	var notes, promotedKeys []string
	promoted := map[string][]string{}
	ix := dct.NewIdentityIndex(root)
	for _, p := range promotions {
		denied := dct.IDSet{}
		if target.IsLedger && p.k.resolvable && p.k.kind.Activatable() {
			stored, _, err := charter.CurrentActivationList(target.Data, "deactivated_"+p.k.plural)
			if err != nil {
				return migrationResult{}, err
			}
			if len(stored) > 0 {
				floor, err := dct.OrgRequiredTokens(root, p.k.plural, true)
				if err != nil {
					return migrationResult{}, err
				}
				denied = dct.EffectiveDeny(p.k.kind, stored, floor, ix)
			}
		}
		current, _, err := charter.CurrentActivationList(target.Data, p.k.key())
		if err != nil {
			return migrationResult{}, err
		}
		list := append([]any{}, strsToAny(current)...)
		inList := map[string]bool{}
		for _, x := range current {
			inList[x] = true
		}
		var activated, skipped []string
		for _, id := range p.missing {
			if denied[id] {
				skipped = append(skipped, id)
				continue
			}
			if inList[id] {
				notes = append(notes, fmt.Sprintf("%s is already activated for key %s.", pyRepr(id), pyRepr(p.k.key())))
				continue
			}
			inList[id] = true
			list = append(list, id)
			activated = append(activated, id)
		}
		if len(skipped) > 0 {
			sort.Strings(skipped)
			var reprs []string
			for _, id := range skipped {
				reprs = append(reprs, pyRepr(id))
			}
			notes = append(notes, fmt.Sprintf("%s: skipped %s: deactivated; run `spec-kitty charter activate` to reinstate.", p.k.key(), strings.Join(reprs, ", ")))
		}
		target.Data.Set(p.k.key(), list)
		if len(activated) > 0 {
			promotedKeys = append(promotedKeys, p.k.key())
			promoted[p.k.key()] = activated
		}
	}
	warnings = append(notes, warnings...)
	if dryRun {
		change := promoteNothingPreview
		if len(promotedKeys) > 0 {
			var parts []string
			for _, key := range promotedKeys {
				parts = append(parts, pyRepr(key)+": "+pyListRepr(promoted[key]))
			}
			change = "dry-run: would promote {" + strings.Join(parts, ", ") + "}"
		}
		return migrationResult{Success: true, Changes: []string{change}, Warnings: warnings}, nil
	}
	if err := target.Save(); err != nil {
		return migrationResult{}, err
	}
	var changes []string
	if len(promotedKeys) > 0 {
		// The activation ledger is in the bundle hash (FR-018).
		if err := charter.RestampBundleHash(root); err != nil {
			return migrationResult{}, err
		}
		for _, key := range promotedKeys {
			changes = append(changes, fmt.Sprintf("Promoted %s into %s", pyListRepr(promoted[key]), key))
		}
	} else {
		changes = []string{promoteNothingToPromote}
	}
	return migrationResult{Success: true, Changes: changes, Warnings: warnings}, nil
}

// seededListHints is _seeded_list_hints: a warning for each present list that
// holds every available artefact of its kind while the interview selected fewer.
func seededListHints(root string, kinds []promotedKind, present, selected map[string][]string, resolver stemResolver) []string {
	var hints []string
	for _, k := range kinds {
		if len(present[k.key()]) <= len(selected[k.key()]) {
			continue
		}
		ids, reason := charter.EffectiveSet(root, k.token)
		if reason != "" || len(ids) == 0 {
			continue
		}
		listed := resolver.stems(k, present[k.key()])
		inForce := resolver.stems(k, ids)
		chosen := map[string]bool{}
		for _, s := range selected[k.key()] {
			chosen[s] = true
		}
		if !sameSet(listed, inForce) || !properSubset(chosen, listed) {
			continue
		}
		hints = append(hints, fmt.Sprintf(promoteSeededListHintFormat, k.key(), k.token, len(listed), len(chosen), k.token))
	}
	return hints
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for x := range a {
		if !b[x] {
			return false
		}
	}
	return true
}

func properSubset(a, b map[string]bool) bool {
	if len(a) >= len(b) {
		return false
	}
	for x := range a {
		if !b[x] {
			return false
		}
	}
	return true
}

func strsToAny(xs []string) []any {
	out := make([]any, 0, len(xs))
	for _, x := range xs {
		out = append(out, x)
	}
	return out
}
