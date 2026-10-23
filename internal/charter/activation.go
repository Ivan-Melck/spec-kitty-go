package charter

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// WriteTarget is resolve_activation_write_target: the document activation
// keys are read from and written to (config.yaml, or the charter.yaml the
// config `charter:` pointer names).
type WriteTarget struct {
	Path      string
	Data      *jsonx.Object
	IsCharter bool
	IsLedger  bool
	original  *jsonx.Object
	observed  []byte
}

// LoadWriteTarget resolves and loads the activation write target.
func LoadWriteTarget(root string) (*WriteTarget, error) {
	store, err := dct.ResolveActivationStore(root)
	if err != nil {
		return nil, err
	}
	if store.Ledger {
		before, err := os.ReadFile(store.Path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return &WriteTarget{Path: store.Path, Data: store.Data.Clone(), IsLedger: true, original: store.Data.Clone(), observed: before}, nil
	}
	cfgPath := dct.ConfigPath(root)
	cfg := &jsonx.Object{}
	if b, err := os.ReadFile(cfgPath); err == nil {
		v, err := yamlx.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("%v", err)
		}
		if v != nil {
			o, ok := v.(*jsonx.Object)
			if !ok {
				return nil, fmt.Errorf(".kittify/config.yaml root must be a mapping.")
			}
			cfg = o
		}
	}
	p := dct.CharterPointer(root, cfg)
	if p == "" {
		return &WriteTarget{Path: cfgPath, Data: cfg}, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, &dct.ConfigError{Body: fmt.Sprintf(".kittify/config.yaml 'charter:' pointer names %s, which does not exist.\nRemediation: run the charter-bundle migration (`spec-kitty upgrade`) to (re)generate it, or fix the 'charter:' pointer.", p)}
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsonx.Object)
	if v == nil {
		o, ok = &jsonx.Object{}, true
	}
	if !ok {
		return nil, &dct.ConfigError{Body: p + " root must be a mapping."}
	}
	return &WriteTarget{Path: p, Data: o, IsCharter: true}, nil
}

// Save writes the target (config.yaml: ruamel width 80; charter.yaml: the
// activation keys only, width 4096).
func (t *WriteTarget) Save() error {
	return t.SaveWithChoice("")
}

// SaveWithChoice permits known deny changes only for explicit operator choices.
func (t *WriteTarget) SaveWithChoice(choice string) error {
	if t.IsLedger {
		for _, key := range dct.ActivationLedgerKeys() {
			if !strings.HasPrefix(key, "deactivated_") {
				continue
			}
			old, _, err := currentList(t.original, key)
			if err != nil {
				return err
			}
			cur, _, err := currentList(t.Data, key)
			if err != nil {
				return err
			}
			oldSet, newSet := map[string]bool{}, map[string]bool{}
			for _, id := range old {
				oldSet[id] = true
			}
			for _, id := range cur {
				newSet[id] = true
			}
			added, removed := []string{}, []string{}
			for id := range newSet {
				if !oldSet[id] {
					added = append(added, id)
				}
			}
			for id := range oldSet {
				if !newSet[id] {
					removed = append(removed, id)
				}
			}
			sort.Strings(added)
			sort.Strings(removed)
			if (len(added) > 0 || len(removed) > 0) && choice == "" {
				return &dct.ConfigError{Body: fmt.Sprintf("Refused to change %s without an explicit choice (added: %s, removed: %s). Only charter activate/deactivate, --include-deactivated and --preset --force change a deny-list. Nothing was written.", key, pyjson.Repr(strsAny(added)), pyjson.Repr(strsAny(removed)))}
			}
			if len(cur) > 0 {
				var normalized []string
				for id := range newSet {
					normalized = append(normalized, id)
				}
				sort.Strings(normalized)
				t.Data.Set(key, strsAny(normalized))
			} else {
				t.Data.Delete(key)
			}
		}
		before, err := os.ReadFile(t.Path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !bytes.Equal(before, t.observed) {
			return fmt.Errorf("%s changed after it was read; nothing was written.", t.Path)
		}
		if t.Data.Len() == 0 && t.observed == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
			return err
		}
		if t.Data.Len() == 0 {
			return os.WriteFile(t.Path, []byte("{}\n"), 0o644)
		}
		return writeYAMLDocument(t.Path, t.Data, 80)
	}
	if !t.IsCharter {
		os.MkdirAll(filepath.Dir(t.Path), 0o755)
		return writeYAMLDocument(t.Path, t.Data, 80)
	}
	before, err := os.ReadFile(t.Path)
	if err != nil {
		return err
	}
	cur, _ := yamlx.ParseObject(before)
	if cur == nil {
		cur = &jsonx.Object{}
	}
	for _, k := range ActivationYAMLKeys() {
		if v, ok := t.Data.Get(k); ok {
			cur.Set(k, v)
		} else {
			cur.Delete(k)
		}
	}
	return writeYAMLDocument(t.Path, cur, 4096)
}

// ActivationYAMLKeys is ACTIVATION_YAML_KEYS.
func ActivationYAMLKeys() []string {
	out := []string{"activated_kinds"}
	for _, t := range dct.CharterKindTokens {
		out = append(out, dct.YAMLKey(t))
	}
	return append(out, "activated_anti_patterns")
}

// currentList is _current_list / _activation_list_or_error.
func currentList(data *jsonx.Object, key string) ([]string, bool, error) {
	v, ok := data.Get(key)
	if !ok || v == nil {
		return nil, false, nil
	}
	l, ok := v.([]any)
	if !ok {
		return nil, false, fmt.Errorf(".kittify/config.yaml key '%s' must be a list, got %s.", key, pyTypeName(v))
	}
	out := make([]string, 0, len(l))
	for _, x := range l {
		out = append(out, pyStrOf(x))
	}
	return out, true, nil
}

func pyTypeName(v any) string {
	switch v.(type) {
	case string:
		return "str"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case bool:
		return "bool"
	case *jsonx.Object:
		return "CommentedMap"
	}
	return "NoneType"
}

func pyStrOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return jsonx.FormatFloat(t)
	}
	return fmt.Sprint(v)
}

// ListActivated is ActiveCharterManager.list_activated (nil slice = absent).
func ListActivated(root string) (map[string][]string, map[string]bool, error) {
	t, err := LoadWriteTarget(root)
	if err != nil {
		return nil, nil, err
	}
	out := map[string][]string{}
	present := map[string]bool{}
	for _, tok := range dct.CharterKindTokens {
		l, ok, err := currentList(t.Data, dct.YAMLKey(tok))
		if err != nil {
			return nil, nil, err
		}
		if ok {
			seen := map[string]bool{}
			var u []string
			for _, x := range l {
				if !seen[x] {
					seen[x] = true
					u = append(u, x)
				}
			}
			sort.Strings(u)
			if u == nil {
				u = []string{}
			}
			out[tok] = u
			present[tok] = true
		}
	}
	return out, present, nil
}

// UnknownActivationIDError is UnknownActivationIdError.
type UnknownActivationIDError struct{ Kind, ID string }

func (e *UnknownActivationIDError) Error() string {
	return fmt.Sprintf("Unknown %s ID %s. No artifact with that ID is available for kind %s. Run `charter list --show-available` to inspect available IDs, or `doctor charter-packs` to verify the doctrine corpus is intact.",
		e.Kind, dct.PyRepr(e.ID), dct.PyRepr(e.Kind))
}

// NoActivationRestrictionsError is raised when deactivating an absent key.
type NoActivationRestrictionsError struct{ Kind string }

func (e *NoActivationRestrictionsError) Error() string {
	inForce := ""
	if e.Kind == "skill" {
		inForce = " (only org-required entries are in force)"
	}
	return fmt.Sprintf("Kind %s has no explicit activation set%s. Activate one first with `spec-kitty charter activate %s <id>`, or set the list explicitly in `.kittify/config.yaml`, before deactivating individual entries.", dct.PyRepr(e.Kind), inForce, e.Kind)
}

// ActivationResult is the rendered outcome of activate / deactivate.
type ActivationResult struct {
	Activated, Deactivated, Warnings []string
}

// EffectiveIDs is resolve_effective_sets for one kind while its key is
// absent: every config-stem available across built-in, project and each
// existing org root, plus (non-directive kinds) the resolved artifact ids.
// ok=false means unresolved (mission-type ledger, skill required-only).
func EffectiveIDs(root, token string) ([]string, bool) {
	ids, reason := EffectiveSet(root, token)
	return ids, reason == ""
}

// Unresolved effective-set reasons (charter.activation.effective_set).
const (
	effectiveRequiredOnly = "kind is required-only: an absent key puts only org-required entries in force"
	effectiveLedger       = "mission_type_activations is an activation ledger, not a corpus: an absent key puts no mission type in force"
)

// EffectiveSet is resolve_effective_sets for one kind: the effective ids
// while the key is absent, or (reason != "") why the set is unresolved.
// It fails closed like upstream: every declared org pack root must be
// fetched (resolve_pack_chain(strict=True)), and an empty set while
// built-in artifacts of the kind ship is unresolved.
func EffectiveSet(root, token string) ([]string, string) {
	if token == dct.MissionTypeToken {
		return nil, effectiveLedger
	}
	if token == "skill" {
		return nil, effectiveRequiredOnly
	}
	if _, err := RequireDeclaredOrgRoots(root); err != nil {
		return nil, err.Error()
	}
	ids := effectiveScan(root, token)
	if len(ids) == 0 {
		if k, err := dct.ParseKind(token); err == nil && builtInShips(k) {
			return nil, fmt.Sprintf("empty effective set while built-in artifacts of %s ship", token)
		}
	}
	return ids, ""
}

// builtInShips is effective_set._built_in_ships.
func builtInShips(k dct.Kind) bool {
	if !k.HasBuiltInContentDir() {
		return false
	}
	return len(dct.BuiltIn().Walk(k.Plural(), ".yaml", true)) > 0
}

// effectiveScan is the resolved effective set's scan (_scanned_ids plus
// the service keys).
func effectiveScan(root, token string) []string {
	lr := dct.LayerRoots(root)
	base := map[string]string{}
	if p := lr[dct.LayerProject]; p != "" {
		base[dct.LayerProject] = p
	}
	set := dct.AvailableIDs(token, base, nil)
	for _, org := range dct.OrgRoots(root) {
		m := map[string]string{dct.LayerOrg: org}
		for k, v := range base {
			m[k] = v
		}
		for id := range dct.AvailableIDs(token, m, nil) {
			set[id] = true
		}
	}
	if token != "directive" {
		k, err := dct.ParseKind(token)
		if err == nil {
			for _, id := range dct.NewOffering(root, dct.OrgRoots(root)).Repo(k).IDs() {
				set[id] = true
			}
		}
	}
	var out []string
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// fallbackIDs is EffectiveSet.fallback_ids for the tolerant single activation.
func fallbackIDs(root, token string) []string {
	lr := dct.LayerRoots(root)
	base := map[string]string{}
	if p := lr[dct.LayerProject]; p != "" {
		base[dct.LayerProject] = p
	}
	set := dct.AvailableIDs(token, base, nil)
	for _, org := range dct.OrgRoots(root) {
		m := map[string]string{dct.LayerOrg: org}
		for k, v := range base {
			m[k] = v
		}
		for id := range dct.AvailableIDs(token, m, nil) {
			set[id] = true
		}
	}
	var out []string
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ValidKindToken reports whether token is in YAML_KEY_MAP.
func ValidKindToken(token string) bool {
	for _, t := range dct.CharterKindTokens {
		if t == token {
			return true
		}
	}
	return false
}

// Activate is ActiveCharterManager.activate (plan_activation + commit_plan).
func Activate(root, token, id string) (*ActivationResult, error) {
	return ActivateWithOptions(root, token, id, false, "activate")
}

func ActivateWithOptions(root, token, id string, only bool, choice string) (*ActivationResult, error) {
	key := dct.YAMLKey(token)
	t, err := LoadWriteTarget(root)
	if err != nil {
		return nil, err
	}
	avail := dct.AvailableIDs(token, dct.LayerRoots(root), dct.OrgRoots(root))
	ix := dct.NewIdentityIndex(root)
	k, kindErr := dct.ParseKind(token)
	if kindErr == nil {
		id = ix.Stem(k, id)
		if values, present, e := currentList(t.Data, key); e == nil && present {
			for i, value := range values {
				values[i] = ix.Stem(k, value)
			}
			t.Data.Set(key, strsAny(dedupe(values)))
		}
	}
	cur, present, err := currentList(t.Data, key)
	if err != nil {
		return nil, err
	}
	res := &ActivationResult{}
	denyKey := ""
	var stored []string
	denied := dct.IDSet{}
	if kindErr == nil && k.Activatable() {
		denyKey = "deactivated_" + k.Plural()
		if t.IsLedger {
			stored, _, _ = currentList(t.Data, denyKey)
		}
		if len(stored) > 0 {
			floor, e := dct.OrgRequiredTokens(root, k.Plural(), true)
			if e != nil {
				return nil, e
			}
			denied = dct.EffectiveDeny(k, stored, floor, ix)
		}
	}
	clearExclusion := func() {
		if containsString(stored, id) {
			var next []string
			for _, value := range stored {
				if value != id {
					next = append(next, value)
				}
			}
			if len(next) == 0 {
				t.Data.Delete(denyKey)
			} else {
				t.Data.Set(denyKey, strsAny(next))
			}
			res.Warnings = append(res.Warnings, fmt.Sprintf("Cleared the exclusion of %s (%s).", dct.PyRepr(id), denyKey))
		}
	}
	var preserved []string
	inForce := true
	if !present {
		switch {
		case token == "skill":
			// PackContext.activated_skills: an absent key puts the org
			// packs' required_skills in force (built-in defaults: none).
			preserved = OrgRequiredIDs(root, "skills")
			sort.Strings(preserved)
		default:
			if eff, reason := EffectiveSet(root, token); reason == "" {
				preserved = eff
			} else {
				seen := map[string]bool{}
				for _, x := range fallbackIDs(root, token) {
					seen[x] = true
				}
				for x := range avail {
					seen[x] = true
				}
				for x := range seen {
					preserved = append(preserved, x)
				}
				sort.Strings(preserved)
				inForce = false
				if token == dct.MissionTypeToken {
					res.Warnings = append(res.Warnings, fmt.Sprintf("Kind %s had no explicit activation set, so no mission type was in force; initialized from the %d mission types available across all readable layers.", dct.PyRepr(token), len(preserved)))
				} else {
					res.Warnings = append(res.Warnings, fmt.Sprintf("The effective %s set could not be resolved because %s; initialized from the %d ids available across all readable layers.", token, reason, len(preserved)))
				}
			}
		}
	}
	if !avail[id] {
		return nil, &UnknownActivationIDError{token, id}
	}
	if !present && token != "skill" && containsString(stored, id) && !only {
		res.Activated = []string{id}
		res.Warnings = []string{fmt.Sprintf("Kind %s keeps no explicit activation set, so artifacts shipped later stay active.", dct.PyRepr(token))}
		clearExclusion()
		if err := saveActivationTarget(root, t, choice); err != nil {
			return nil, err
		}
		return res, nil
	}
	filtered := preserved[:0]
	for _, value := range preserved {
		if !denied[ix.Stem(k, value)] || value == id {
			filtered = append(filtered, value)
		}
	}
	preserved = filtered
	if only {
		previous := cur
		if !present {
			previous = preserved
			if !inForce {
				previous = nil
				res.Warnings = append(res.Warnings, "What stopped being in force cannot be listed: the effective set could not be resolved.")
			}
		}
		base := []string{}
		if kindErr == nil {
			required, e := dct.OrgRequiredTokens(root, k.Plural(), false)
			if e != nil {
				return nil, e
			}
			for _, value := range required {
				base = append(base, ix.Stem(k, value))
			}
		}
		next := dedupe(append(base, id))
		prev := map[string]bool{}
		for _, value := range previous {
			stem := value
			if kindErr == nil {
				stem = ix.Stem(k, value)
			}
			if prev[stem] {
				continue
			}
			prev[stem] = true
			if !containsString(next, stem) {
				res.Deactivated = append(res.Deactivated, stem)
			}
		}
		if !prev[id] {
			res.Activated = append(res.Activated, id)
		}
		t.Data.Set(key, strsAny(next))
		clearExclusion()
		if e := saveActivationTarget(root, t, choice); e != nil {
			return nil, e
		}
		return res, nil
	}
	var newList []string
	if !present {
		materialized := dedupe(preserved)
		if len(materialized) == 0 && token != "skill" {
			var a []string
			for x := range avail {
				a = append(a, x)
			}
			sort.Strings(a)
			materialized = a
		}
		if inForce {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Kind %s had no explicit activation set. Initialized from the %d artifact(s) already effective, so nothing in force was deactivated.", dct.PyRepr(token), len(materialized))+FrozenListNote)
		}
		newList = materialized
	} else {
		newList = append([]string(nil), cur...)
	}
	contains := false
	for _, x := range newList {
		contains = contains || x == id
	}
	if contains && present {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s is already activated for kind %s.", dct.PyRepr(id), dct.PyRepr(token)))
	} else {
		if !contains {
			newList = append(newList, id)
		}
		res.Activated = append(res.Activated, id)
	}
	t.Data.Set(key, strsAny(newList))
	clearExclusion()
	if err := saveActivationTarget(root, t, choice); err != nil {
		return nil, err
	}
	return res, nil
}

// Deactivate is ActiveCharterManager.deactivate.
func Deactivate(root, token, id string) (*ActivationResult, error) {
	return DeactivateWithOptions(root, token, id, true)
}

func DeactivateWithOptions(root, token, id string, recordExclusion bool) (*ActivationResult, error) {
	key := dct.YAMLKey(token)
	t, err := LoadWriteTarget(root)
	if err != nil {
		return nil, err
	}
	ix := dct.NewIdentityIndex(root)
	k, kindErr := dct.ParseKind(token)
	denyKey := ""
	spellings := map[string]bool{id: true}
	var stored []string
	if kindErr == nil {
		original := id
		id = ix.Stem(k, id)
		spellings[id] = true
		if declared := ix.DeclaredID(k, original); declared != "" {
			spellings[declared] = true
		}
		required, e := dct.OrgRequiredProvenance(root, k, true, ix)
		if e != nil {
			return nil, e
		}
		if requiring := required[id]; len(requiring) > 0 {
			noun := "org pack"
			if len(requiring) > 1 {
				noun = "org packs"
			}
			var names []string
			for _, r := range requiring {
				names = append(names, r.Describe())
			}
			return nil, fmt.Errorf("Cannot deactivate %s %s: required by %s %s (required_%s). Nothing was recorded.", token, dct.PyRepr(id), noun, strings.Join(names, ", "), k.Plural())
		}
		if k.Activatable() {
			denyKey = "deactivated_" + k.Plural()
			if t.IsLedger {
				stored, _, _ = currentList(t.Data, denyKey)
			}
		}
	}
	cur, present, err := currentList(t.Data, key)
	if err != nil {
		return nil, err
	}
	avail := dct.AvailableIDs(token, dct.LayerRoots(root), dct.OrgRoots(root))
	known := avail[id]
	for _, v := range append(append([]string{}, cur...), stored...) {
		known = known || spellings[v]
	}
	if !known {
		return nil, &UnknownActivationIDError{token, id}
	}
	res := &ActivationResult{}
	records := recordExclusion && denyKey != ""
	if records && containsString(stored, id) {
		res.Warnings = []string{fmt.Sprintf("%s is already deactivated for kind %s. Nothing was recorded.", dct.PyRepr(id), dct.PyRepr(token))}
		return res, nil
	}
	var next []string
	removed := false
	for _, v := range cur {
		if spellings[v] {
			removed = true
		} else {
			next = append(next, v)
		}
	}
	if !removed && !records {
		res.Warnings = []string{fmt.Sprintf("%s is not in the activation set for kind %s. Nothing to deactivate.", dct.PyRepr(id), dct.PyRepr(token))}
		return res, nil
	}
	if records {
		if err := dct.RefuseUnmigratedDenyWrite(root); err != nil {
			return nil, err
		}
		t.Data.Set(denyKey, strsAny(dedupe(append(stored, id))))
	}
	if present {
		t.Data.Set(key, strsAny(next))
	}
	res.Deactivated = []string{id}
	if err := saveActivationTarget(root, t, "deactivate"); err != nil {
		return nil, err
	}
	return res, nil
}

const FrozenListNote = " This list is now explicit: artifacts shipped later are not activated automatically; use 'spec-kitty charter activate <kind> <id> --only' to keep activation narrow."

func containsString(values []string, id string) bool {
	for _, v := range values {
		if v == id {
			return true
		}
	}
	return false
}

func strsAny(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// ---- cascade --------------------------------------------------------------

var referenceRelations = map[string]bool{dct.RelRequires: true, dct.RelSuggests: true, dct.RelRefines: true, dct.RelScope: true, dct.RelInstantiates: true}

// CascadeRefs buckets the artifacts referenced (forward closure over the
// reference relations) from sourceURN: activatable kinds and the
// structurally non-activatable ones (template/asset). Kind keys are the
// underscore kind values; ids are DRG bare ids, sorted.
func CascadeRefs(g *dct.Graph, sourceURN string) (map[string][]string, map[string][]string) {
	adj := map[string][]string{}
	for _, e := range g.Edges {
		if referenceRelations[e.Relation] {
			adj[e.Source] = append(adj[e.Source], e.Target)
		}
	}
	visited := map[string]bool{sourceURN: true}
	q := []string{sourceURN}
	for len(q) > 0 {
		c := q[0]
		q = q[1:]
		for _, n := range adj[c] {
			if !visited[n] {
				visited[n] = true
				q = append(q, n)
			}
		}
	}
	delete(visited, sourceURN)
	act, filtered := map[string][]string{}, map[string][]string{}
	for urn := range visited {
		kind, id := dct.SplitURN(urn)
		k, err := dct.ParseKind(kind)
		if err != nil || string(k) != kind {
			continue
		}
		if k == dct.Template || k == dct.Asset {
			filtered[kind] = append(filtered[kind], id)
		} else {
			act[kind] = append(act[kind], id)
		}
	}
	for _, m := range []map[string][]string{act, filtered} {
		for k := range m {
			sort.Strings(m[k])
		}
	}
	return act, filtered
}

// configIDIndex maps (kind, declared id) -> config stem across layers.
type configIDIndex struct {
	root     string
	byKind   map[dct.Kind]map[string]string
	orgChain []string
	lr       map[string]string
}

// NewConfigIDIndex builds a DRG-id -> config-stem resolver for root.
func NewConfigIDIndex(root string) *configIDIndex {
	return &configIDIndex{root: root, byKind: map[dct.Kind]map[string]string{}, orgChain: dct.OrgRoots(root), lr: dct.LayerRoots(root)}
}

// ConfigID is drg_urn_to_config_id: the config stem of a DRG URN, falling
// back to the bare id.
func (ix *configIDIndex) ConfigID(urn string) string {
	kind, id := dct.SplitURN(urn)
	k, err := dct.ParseKind(kind)
	if err != nil {
		return id
	}
	m, ok := ix.byKind[k]
	if !ok {
		m = dct.StemsByDeclaredID(k, ix.lr, ix.orgChain)
		ix.byKind[k] = m
	}
	if s, ok := m[id]; ok {
		return s
	}
	if k == dct.Directive {
		if s, ok := m[dct.NormalizeDirectiveID(id)]; ok {
			return s
		}
	}
	return id
}

// SourceURN resolves the DRG URN of (token, config-stem id) ("" when the
// kind has no DRG node, e.g. mission-type, or no file declares it).
func SourceURN(root, token, stem string) string {
	if token == dct.MissionTypeToken {
		return "mission_type:" + stem
	}
	k, err := dct.ParseKind(token)
	if err != nil {
		return ""
	}
	id := dct.NewIdentityIndex(root).DeclaredID(k, stem)
	if id == "" {
		return ""
	}
	return string(k) + ":" + id
}

func saveActivationTarget(root string, t *WriteTarget, choice string) error {
	if err := t.SaveWithChoice(choice); err != nil {
		return err
	}
	return RestampBundleHash(root)
}

// CurrentActivationList exposes the shared three-state activation list reader.
func CurrentActivationList(data *jsonx.Object, key string) ([]string, bool, error) {
	return currentList(data, key)
}
