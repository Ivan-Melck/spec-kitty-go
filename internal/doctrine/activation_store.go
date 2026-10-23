package doctrine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const ActivationLedgerRelative = ".kittify/charter/activation.yaml"

func ActivationLedgerPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(ActivationLedgerRelative))
}

func RefuseUnmigratedDenyWrite(root string) error {
	store, err := ResolveActivationStore(root)
	if err != nil {
		return err
	}
	if len(store.Unmigrated) == 0 {
		return nil
	}
	keys := map[string]bool{}
	var paths []string
	for _, entry := range store.Unmigrated {
		paths = append(paths, entry.Path)
		for _, key := range entry.Keys {
			keys[key] = true
		}
	}
	var listed []string
	for key := range keys {
		listed = append(listed, key)
	}
	sort.Strings(listed)
	return &ConfigError{fmt.Sprintf("Refused: activation keys %s in %s have not been moved to %s, and a deactivation (deactivated_<kind>) is recorded only there. Run `spec-kitty upgrade` (or `spec-kitty migrate activation-ledger`) to move them, then retry. Nothing was written.", strings.Join(listed, ", "), strings.Join(paths, ", "), ActivationLedgerRelative)}
}

// ActivationLedgerKeys follows ArtifactKind order, including deny keys for
// every activatable kind and the two allow-only keys.
func ActivationLedgerKeys() []string {
	keys := []string{"activated_kinds", "mission_type_activations"}
	for _, prefix := range []string{"activated_", "deactivated_"} {
		for _, kind := range AllKinds {
			if kind.Activatable() {
				keys = append(keys, prefix+kind.Plural())
			}
		}
	}
	return keys
}

type ActivationKeysAt struct {
	Path string
	Keys []string
}

// ActivationStore is the one-mode source resolution: an existing ledger
// wins; unmigrated projects and upgrade scopes keep their legacy source;
// a fresh project writes the ledger, even when it does not yet exist.
type ActivationStore struct {
	Path, Pointer     string
	Data              *jsonx.Object
	Ledger, Exists    bool
	PointerExists     bool
	Stray, Unmigrated []ActivationKeysAt
}

var legacyActivationRoots = struct {
	sync.Mutex
	counts map[string]int
}{counts: map[string]int{}}

func canonicalActivationRoot(root string) string {
	p, err := filepath.Abs(root)
	if err != nil {
		return root
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

func LegacyActivationScope(roots []string, fn func() error) error {
	roots = append([]string(nil), roots...)
	legacyActivationRoots.Lock()
	for i, root := range roots {
		roots[i] = canonicalActivationRoot(root)
		legacyActivationRoots.counts[roots[i]]++
	}
	legacyActivationRoots.Unlock()
	defer func() {
		legacyActivationRoots.Lock()
		for _, root := range roots {
			legacyActivationRoots.counts[root]--
		}
		legacyActivationRoots.Unlock()
	}()
	return fn()
}

func inLegacyActivationScope(root string) bool {
	legacyActivationRoots.Lock()
	defer legacyActivationRoots.Unlock()
	return legacyActivationRoots.counts[canonicalActivationRoot(root)] > 0
}

func activationKeysAt(path string, data *jsonx.Object) (out ActivationKeysAt) {
	out.Path = path
	for _, key := range ActivationLedgerKeys() {
		if data.Has(key) {
			out.Keys = append(out.Keys, key)
		}
	}
	sort.Strings(out.Keys)
	return out
}

// ResolveActivationStore reads source metadata without printing diagnostics.
func ResolveActivationStore(root string) (*ActivationStore, error) {
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	pointer := CharterPointer(root, cfg)
	legacy := cfg
	legacyPath := ConfigPath(root)
	var found []ActivationKeysAt
	if keys := activationKeysAt(legacyPath, cfg); len(keys.Keys) > 0 {
		found = append(found, keys)
	}
	pointerExists := false
	if pointer != "" {
		_, err := os.Stat(pointer)
		pointerExists = err == nil
		if pointerExists && isFile(pointer) {
			b, err := os.ReadFile(pointer)
			if err != nil {
				return nil, configError(fmt.Sprintf("Invalid YAML in %s: %v", pointer, err))
			}
			v, err := yamlx.Parse(b)
			if err != nil {
				return nil, configError(fmt.Sprintf("Invalid YAML in %s: %v", pointer, err))
			}
			if v == nil {
				v = &jsonx.Object{}
			}
			var ok bool
			legacy, ok = v.(*jsonx.Object)
			if !ok {
				return nil, configError(pointer + " root must be a mapping.")
			}
			legacyPath = pointer
			if keys := activationKeysAt(pointer, legacy); len(keys.Keys) > 0 {
				found = append(found, keys)
			}
		}
	}
	ledgerPath := ActivationLedgerPath(root)
	ledgerExists := isFile(ledgerPath)
	store := &ActivationStore{Pointer: pointer, PointerExists: pointerExists, Exists: ledgerExists}
	if !ledgerExists && (inLegacyActivationScope(root) || len(found) > 0 || (pointer != "" && !pointerExists)) {
		if pointer != "" && !pointerExists {
			legacyPath, legacy = pointer, &jsonx.Object{}
		}
		store.Path, store.Data, store.Exists = legacyPath, legacy, isFile(legacyPath)
		if !inLegacyActivationScope(root) {
			store.Unmigrated = found
		}
		return store, nil
	}
	store.Path, store.Ledger, store.Stray = ledgerPath, true, found
	store.Data = &jsonx.Object{}
	if ledgerExists {
		b, err := os.ReadFile(ledgerPath)
		if err != nil {
			return nil, err
		}
		v, err := yamlx.Parse(b)
		if err != nil {
			return nil, &ConfigError{fmt.Sprintf("Invalid YAML in %s: %v\nRemediation: fix %s.", ledgerPath, err, ActivationLedgerRelative)}
		}
		if v == nil {
			v = &jsonx.Object{}
		}
		var ok bool
		store.Data, ok = v.(*jsonx.Object)
		if !ok {
			return nil, &ConfigError{fmt.Sprintf("%s root must be a mapping.\nRemediation: fix %s.", ledgerPath, ActivationLedgerRelative)}
		}
	}
	if err := validateActivationLedger(store.Path, store.Data); err != nil {
		return nil, err
	}
	return store, nil
}

func validateActivationLedger(path string, data *jsonx.Object) error {
	keys := ActivationLedgerKeys()
	known := map[string]bool{}
	validate := func(key string, value any) error {
		if value == nil {
			return nil
		}
		list, ok := value.([]any)
		if !ok {
			return &ConfigError{fmt.Sprintf("Activation key '%s' must be a list, got %s. (%s)", key, pyTypeName(value), path)}
		}
		for _, id := range list {
			if _, ok := id.(string); !ok {
				return &ConfigError{fmt.Sprintf("Activation key '%s' in %s must list string ids, got %s.", key, path, pyjson.Repr(list))}
			}
		}
		return nil
	}
	for _, key := range keys {
		known[key] = true
		if value, ok := data.Get(key); ok {
			if err := validate(key, value); err != nil {
				return err
			}
		}
	}
	var unknownDeny []string
	for _, pair := range data.Pairs() {
		if known[pair.Key] {
			continue
		}
		if strings.HasPrefix(pair.Key, "deactivated_") {
			unknownDeny = append(unknownDeny, pair.Key)
			continue
		}
		return &ConfigError{fmt.Sprintf("Unknown activation ledger key %s in %s. Allowed keys: %s (and deactivated_<kind> keys).", pyjson.ReprString(pair.Key), path, strings.Join(keys, ", "))}
	}
	for _, key := range unknownDeny {
		value, _ := data.Get(key)
		if err := validate(key, value); err != nil {
			return err
		}
	}
	return nil
}

// ActivationWarnLog is a plain stderr line, including in --json mode.
// It is deliberately separate from the Python warning/logging bootstrap.
var ActivationWarnLog func(string)
var activationWarnings = struct {
	sync.Mutex
	seen map[string]bool
}{seen: map[string]bool{}}

func activationWarningOnce(key, message string) {
	activationWarnings.Lock()
	if activationWarnings.seen[key] {
		activationWarnings.Unlock()
		return
	}
	activationWarnings.seen[key] = true
	activationWarnings.Unlock()
	if ActivationWarnLog != nil {
		ActivationWarnLog(message)
	}
}

func ReportActivationWarnings(store *ActivationStore) {
	if len(store.Stray) > 0 {
		var listed []string
		for _, entry := range store.Stray {
			listed = append(listed, strings.Join(entry.Keys, ", ")+" in "+entry.Path)
		}
		message := fmt.Sprintf("Legacy activation keys beside %s are ignored; the activation ledger (activation.yaml) wins: %s. Remove them, or run `spec-kitty doctor charter-packs`.", ActivationLedgerRelative, strings.Join(listed, "; "))
		activationWarningOnce(strings.Join(listed, "\x00"), message)
	}
	if !store.Ledger {
		var denied []string
		for _, pair := range store.Data.Pairs() {
			if strings.HasPrefix(pair.Key, "deactivated_") {
				denied = append(denied, pair.Key)
			}
		}
		sort.Strings(denied)
		if len(denied) > 0 {
			message := fmt.Sprintf("%s in %s is not applied: a deactivation is recorded only in %s. Remove the key by hand, then run `spec-kitty upgrade` (or `spec-kitty migrate activation-ledger`).", strings.Join(denied, ", "), store.Path, ActivationLedgerRelative)
			activationWarningOnce(store.Path+"\x00"+strings.Join(denied, "\x00"), message)
		}
	}
}
