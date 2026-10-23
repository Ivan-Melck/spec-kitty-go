package charter

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

func activationFinding(code, file, key string, ids []string, message string) *jsonx.Object {
	var rawKey any
	if key != "" {
		rawKey = key
	}
	return jsonx.NewObject("code", code, "file", file, "key", rawKey, "ids", strsAny(ids), "message", message)
}

type RequiredOmission struct {
	Kind  dct.Kind
	ID    string
	Packs []dct.RequiringPack
}

func RequiredNotInForce(root string, strict bool) ([]RequiredOmission, error) {
	store, err := dct.ResolveActivationStore(root)
	if err != nil {
		return nil, err
	}
	ix := dct.NewIdentityIndex(root)
	var out []RequiredOmission
	for _, k := range dct.AllKinds {
		if !k.Activatable() {
			continue
		}
		allow, present, err := currentList(store.Data, "activated_"+k.Plural())
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		floor, err := dct.OrgRequiredProvenance(root, k, strict, ix)
		if err != nil {
			return nil, err
		}
		tokens, err := dct.OrgRequiredTokens(root, k.Plural(), strict)
		if err != nil {
			return nil, err
		}
		allowed, seen := map[string]bool{}, map[string]bool{}
		for _, id := range allow {
			allowed[ix.Stem(k, id)] = true
		}
		for _, token := range tokens {
			id := ix.Stem(k, token)
			if !seen[id] && !allowed[id] {
				seen[id] = true
				out = append(out, RequiredOmission{k, id, floor[id]})
			}
		}
	}
	return out, nil
}

func ActivationLedgerFindings(root string) *jsonx.Object {
	errors, warnings := []any{}, []any{}
	store, err := dct.ResolveActivationStore(root)
	storeError := func(err error) {
		errors = append(errors, activationFinding("ACTIVATION_STORE_ERROR", dct.ActivationLedgerRelative, "", nil, err.Error()))
	}
	finish := func() *jsonx.Object { return jsonx.NewObject("errors", errors, "warnings", warnings) }
	if err != nil {
		storeError(err)
		return finish()
	}
	const ledgerName = "the activation ledger (activation.yaml)"
	const remedy = "`spec-kitty upgrade` or `spec-kitty migrate activation-ledger`"
	for _, entry := range store.Stray {
		for _, key := range entry.Keys {
			message := fmt.Sprintf("%s in %s is ignored: %s exists and %s wins. Remove the key by hand, or run `spec-kitty migrate activation-ledger`, which strips a copy equal to %s.", key, entry.Path, dct.ActivationLedgerRelative, ledgerName, ledgerName)
			errors = append(errors, activationFinding("LEGACY_KEYS_PRESENT", entry.Path, key, nil, message))
		}
	}
	if !store.Ledger {
		for _, entry := range store.Unmigrated {
			b, e := os.ReadFile(entry.Path)
			if e != nil {
				storeError(e)
				return finish()
			}
			mapping, e := yamlx.ParseObject(b)
			if e != nil {
				storeError(e)
				return finish()
			}
			for _, key := range entry.Keys {
				if strings.HasPrefix(key, "deactivated_") {
					ids, _, _ := currentList(mapping, key)
					message := fmt.Sprintf("%s in %s is a legacy deny-list key: it was never applied, and the move to %s refuses it. Remove it by hand, then run %s.", key, entry.Path, ledgerName, remedy)
					errors = append(errors, activationFinding("LEGACY_DENY_KEY", entry.Path, key, ids, message))
				} else {
					message := fmt.Sprintf("%s in %s has not been moved to %s; reads still use the legacy surface. Run %s.", key, entry.Path, dct.ActivationLedgerRelative, remedy)
					warnings = append(warnings, activationFinding("ACTIVATION_UNMIGRATED", entry.Path, key, nil, message))
				}
			}
		}
	}
	ix := dct.NewIdentityIndex(root)
	floors := map[dct.Kind]map[string][]dct.RequiringPack{}
	for _, kind := range dct.AllKinds {
		if !kind.Activatable() || !slices.Contains(OrgRequirableKinds, kind.Plural()) {
			continue
		}
		floor, e := dct.OrgRequiredProvenance(root, kind, true, ix)
		if e != nil {
			storeError(e)
			return finish()
		}
		floors[kind] = floor
	}
	names := func(packs []dct.RequiringPack) string {
		var names []string
		for _, p := range packs {
			names = append(names, p.Describe())
		}
		return strings.Join(names, ", ")
	}
	for _, kind := range dct.AllKinds {
		key := "activated_" + kind.Plural()
		allow, present, e := currentList(store.Data, key)
		if e != nil {
			storeError(e)
			return finish()
		}
		if !present {
			continue
		}
		allowed := map[string]bool{}
		for _, id := range allow {
			allowed[ix.Stem(kind, id)] = true
		}
		// Required ids retain the order in which the organization packs list them.
		tokens, _ := dct.OrgRequiredTokens(root, kind.Plural(), false)
		seen := map[string]bool{}
		for _, token := range tokens {
			id := ix.Stem(kind, token)
			if seen[id] || allowed[id] {
				continue
			}
			seen[id] = true
			message := fmt.Sprintf("%s in %s leaves out %s, required by org pack %s: the explicit allow list keeps it out of force. Run `spec-kitty charter activate %s %s`.", key, store.Path, dct.PyRepr(id), names(floors[kind][id]), kind.OperatorToken(), id)
			warnings = append(warnings, activationFinding("REQUIRED_OMITTED_BY_ALLOW_LIST", store.Path, key, []string{id}, message))
		}
	}
	if !store.Ledger {
		return finish()
	}
	known := map[string]bool{}
	for _, key := range dct.ActivationLedgerKeys() {
		known[key] = true
	}
	var unknown []string
	for _, key := range store.Data.Keys() {
		if !known[key] && strings.HasPrefix(key, "deactivated_") {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		ids, _, _ := currentList(store.Data, key)
		message := fmt.Sprintf("%s in %s names a kind this CLI does not know; it is kept and ignored. Remove the stale entry by hand.", key, store.Path)
		warnings = append(warnings, activationFinding("UNKNOWN_DENY_KEY", store.Path, key, ids, message))
	}
	answersPath := filepath.Join(root, ".kittify/charter/interview/answers.yaml")
	answers := ReadInterview(answersPath)
	for _, kind := range dct.AllKinds {
		if !kind.Activatable() {
			continue
		}
		key := "deactivated_" + kind.Plural()
		denied, _, _ := currentList(store.Data, key)
		if len(denied) == 0 {
			continue
		}
		var missing []string
		for _, token := range denied {
			if ix.DeclaredID(kind, token) == "" {
				missing = append(missing, token)
			}
		}
		if len(missing) > 0 {
			message := fmt.Sprintf("%s in %s lists %s, which no layer provides. Remove the stale entry by hand.", key, store.Path, strings.Join(missing, ", "))
			warnings = append(warnings, activationFinding("DENY_UNKNOWN_ID", store.Path, key, missing, message))
		}
		for _, token := range denied {
			packs := floors[kind][ix.Stem(kind, token)]
			if len(packs) == 0 {
				continue
			}
			message := fmt.Sprintf("%s in %s lists %s, required by org pack %s: the required floor wins, so the entry has no effect. Remove the stale entry by hand.", key, store.Path, dct.PyRepr(token), names(packs))
			warnings = append(warnings, activationFinding("DENY_REQUIRED_ID", store.Path, key, []string{token}, message))
		}
		if answers != nil {
			selected := map[string]bool{}
			for _, id := range strListAny(answers.ToObject().GetList("selected_" + kind.Plural())) {
				selected[ix.Stem(kind, id)] = true
			}
			var hits []string
			for _, token := range denied {
				if selected[ix.Stem(kind, token)] {
					hits = append(hits, token)
				}
			}
			if len(hits) > 0 {
				message := fmt.Sprintf("selected_%s in %s lists %s, excluded by %s in %s: the exclusion wins (answers.yaml is provenance only). Run `spec-kitty charter activate %s <id>` to clear the exclusion, or remove the stale entry by hand.", kind.Plural(), answersPath, strings.Join(hits, ", "), key, store.Path, kind.OperatorToken())
				warnings = append(warnings, activationFinding("DENY_SELECTED_ID", answersPath, key, hits, message))
			}
		}
	}
	return finish()
}

func RunActivationLedgerCheck(rep *CharterPackHealthReport, root string) {
	findings := ActivationLedgerFindings(root)
	rep.OrgDRG.Set("activation_ledger", findings)
	for _, raw := range findings.GetList("errors") {
		AppendOrgError(rep.OrgDRG, raw.(*jsonx.Object).GetString("message"))
	}
}

func strListAny(values []any) []string {
	var out []string
	for _, v := range values {
		out = append(out, pyStrOf(v))
	}
	return out
}
