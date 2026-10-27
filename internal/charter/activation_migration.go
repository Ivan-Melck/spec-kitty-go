package charter

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

type LedgerMigrationRefusal struct {
	Key, Reason string
	Files       []string
}

func (r *LedgerMigrationRefusal) Error() string {
	what := fmt.Sprintf("%s differs between %s. Make the files agree, or remove the stale copy", r.Key, strings.Join(r.Files, ", "))
	if r.Reason == "legacy_deny_key" {
		what = fmt.Sprintf("%s in %s is a legacy deny-list key; it was never applied and is not moved. Remove it by hand", r.Key, strings.Join(r.Files, ", "))
	}
	return fmt.Sprintf("Refused to move the activation keys to %s: %s, then run `spec-kitty migrate activation-ledger` or `spec-kitty upgrade` again. Nothing was written.", dct.ActivationLedgerRelative, what)
}

type ActivationMove struct{ Key, From, To string }
type activationStrip struct {
	Path     string
	Keys     []string
	Data     *jsonx.Object
	Observed []byte
}
type LedgerMigrationPlan struct {
	Root    string
	Values  *jsonx.Object
	Strips  []activationStrip
	Refusal *LedgerMigrationRefusal
}

func (p *LedgerMigrationPlan) Noop() bool {
	return p.Refusal == nil && len(p.Strips) == 0 && p.Values.Len() == 0
}
func (p *LedgerMigrationPlan) Moves() []ActivationMove {
	out := []ActivationMove{}
	if p.Refusal == nil {
		for _, strip := range p.Strips {
			for _, key := range strip.Keys {
				out = append(out, ActivationMove{key, strip.Path, dct.ActivationLedgerPath(p.Root)})
			}
		}
	}
	return out
}

func activationAllowKeys() []string {
	var out []string
	for _, key := range dct.ActivationLedgerKeys() {
		if !strings.HasPrefix(key, "deactivated_") {
			out = append(out, key)
		}
	}
	return out
}

func migrationIDs(path string, data *jsonx.Object, key string) ([]string, error) {
	v, _ := data.Get(key)
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	out := []string{}
	for _, item := range list {
		s, isString := item.(string)
		if !isString {
			ok = false
			break
		}
		out = append(out, s)
	}
	if !ok {
		return nil, &dct.ConfigError{Body: fmt.Sprintf("Activation key '%s' in %s must be a list of string ids, got %s.\nRemediation: fix .kittify/config.yaml (or the charter.yaml it points to) or run `spec-kitty upgrade` to restore the default active charter shape.", key, path, pyjson.Repr(v))}
	}
	return out, nil
}

var legacyIntentStem = regexp.MustCompile(`^(\d+)-mission-type-scope-directive$`)

func sameMigrationIDs(left, right []string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	l, r := append([]string{}, left...), append([]string{}, right...)
	changed := false
	for _, xs := range [][]string{l, r} {
		for i, id := range xs {
			if m := legacyIntentStem.FindStringSubmatch(id); m != nil {
				xs[i] = m[1] + "-project-intent-directive"
				changed = true
			}
		}
		sort.Strings(xs)
	}
	if changed {
		l = slices.Compact(l)
		r = slices.Compact(r)
	}
	return slices.Equal(l, r)
}

func PlanLedgerMigration(root string) (*LedgerMigrationPlan, error) {
	cfg, err := dct.LoadConfig(root)
	if err != nil {
		return nil, err
	}
	configPath := dct.ConfigPath(root)
	var surfaces []activationStrip
	load := func(path string) (activationStrip, error) {
		b, e := os.ReadFile(path)
		if e != nil {
			return activationStrip{}, e
		}
		o, e := yamlx.ParseObject(b)
		if e != nil {
			return activationStrip{}, e
		}
		if o == nil {
			o = &jsonx.Object{}
		}
		return activationStrip{Path: path, Data: o, Observed: b}, nil
	}
	if pointer := dct.CharterPointer(root, cfg); pointer != "" {
		if _, e := os.Stat(pointer); os.IsNotExist(e) {
			return nil, &dct.ConfigError{Body: fmt.Sprintf(".kittify/config.yaml 'charter:' pointer names %s, which does not exist.\nRemediation: fix the 'charter:' pointer in .kittify/config.yaml, or remove it when the activation keys live in .kittify/config.yaml itself.", pointer)}
		}
		s, e := load(pointer)
		if e != nil {
			return nil, e
		}
		surfaces = append(surfaces, s)
	}
	if _, e := os.Stat(configPath); e == nil {
		s, e := load(configPath)
		if e != nil {
			return nil, e
		}
		surfaces = append(surfaces, s)
	}
	store, err := dct.ResolveActivationStore(root)
	if err != nil {
		return nil, err
	}
	var ledger *jsonx.Object
	if store.Ledger && store.Exists {
		ledger = store.Data
	}
	p := &LedgerMigrationPlan{Root: root, Values: &jsonx.Object{}}
	for _, s := range surfaces {
		for _, key := range dct.ActivationLedgerKeys() {
			if strings.HasPrefix(key, "deactivated_") && s.Data.Has(key) {
				p.Refusal = &LedgerMigrationRefusal{key, "legacy_deny_key", []string{s.Path}}
				return p, nil
			}
		}
	}
	for _, key := range activationAllowKeys() {
		if ledger != nil {
			target, e := migrationIDs(store.Path, ledger, key)
			if e != nil {
				return nil, e
			}
			for _, s := range surfaces {
				if !s.Data.Has(key) {
					continue
				}
				ids, e := migrationIDs(s.Path, s.Data, key)
				if e != nil {
					return nil, e
				}
				if !sameMigrationIDs(ids, target) {
					p.Refusal = &LedgerMigrationRefusal{key, "divergent", []string{s.Path, store.Path}}
					return p, nil
				}
			}
		} else if len(surfaces) > 1 && surfaces[1].Data.Has(key) {
			left, e := migrationIDs(surfaces[0].Path, surfaces[0].Data, key)
			if e != nil {
				return nil, e
			}
			right, e := migrationIDs(surfaces[1].Path, surfaces[1].Data, key)
			if e != nil {
				return nil, e
			}
			if !sameMigrationIDs(left, right) {
				p.Refusal = &LedgerMigrationRefusal{key, "divergent", []string{surfaces[0].Path, surfaces[1].Path}}
				return p, nil
			}
		}
	}
	for _, s := range surfaces {
		for _, key := range activationAllowKeys() {
			if s.Data.Has(key) {
				s.Keys = append(s.Keys, key)
			}
		}
		if len(s.Keys) > 0 {
			p.Strips = append(p.Strips, s)
		}
	}
	if ledger == nil && len(surfaces) > 0 {
		for _, key := range activationAllowKeys() {
			ids, e := migrationIDs(surfaces[0].Path, surfaces[0].Data, key)
			if e != nil {
				return nil, e
			}
			if ids != nil {
				p.Values.Set(key, strsAny(ids))
			}
		}
	}
	if !p.Noop() {
		if _, err := stampableActivationManifest(root); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func stampableActivationManifest(root string) (*SynthManifest, error) {
	path := filepath.Join(root, ManifestPath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	m, err := LoadSynthManifest(path)
	if err != nil {
		return nil, &dct.ConfigError{Body: fmt.Sprintf("The synthesis manifest %s is unreadable, so the activation keys cannot be moved to %s with its bundle_content_hash kept in parity: %v\nRemediation: fix or regenerate %s (`spec-kitty charter synthesize`), then retry. Nothing was written.", ManifestPath, dct.ActivationLedgerRelative, err, ManifestPath)}
	}
	if m.BuiltInOnly || m.BundleContentHash == nil {
		return nil, nil
	}
	return m, nil
}

func RestampBundleHash(root string) error {
	path := filepath.Join(root, ManifestPath)
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		return nil // manifest_path.is_file() is False
	}
	m, err := LoadSynthManifest(path)
	if err != nil {
		return err
	}
	if m.BuiltInOnly || m.BundleContentHash == nil {
		return nil
	}
	hash := BundleContentHash(root)
	if hash == "" || *m.BundleContentHash == hash {
		return nil
	}
	m.BundleContentHash = &hash
	return os.WriteFile(path, []byte(canonicalYAML(m.Finalize().Object(true))), 0o644)
}

func ApplyLedgerMigration(p *LedgerMigrationPlan) ([]string, error) {
	if p.Refusal != nil {
		return nil, p.Refusal
	}
	current, err := PlanLedgerMigration(p.Root)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(current, p) {
		return nil, fmt.Errorf("precondition_changed: %s changed since the move to the activation ledger (activation.yaml) was planned", p.Root)
	}
	written := []string{}
	if p.Values.Len() > 0 {
		path := dct.ActivationLedgerPath(p.Root)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := writeYAMLDocument(path, p.Values, 80); err != nil {
			return nil, err
		}
		written = append(written, path)
	}
	// Prepare all strips before writing the manifest. Its prospective digest
	// includes the new ledger and the charter with the legacy keys removed.
	strips := map[string][]byte{}
	for _, s := range p.Strips {
		doc := s.Data.Clone()
		for _, key := range s.Keys {
			doc.Delete(key)
		}
		out := []byte("{}\n")
		if doc.Len() > 0 {
			out = []byte(RenderYAMLDocument(s.Observed, doc, 80))
		}
		strips[s.Path] = out
	}
	if !p.Noop() {
		m, e := stampableActivationManifest(p.Root)
		if e != nil {
			return written, e
		}
		if m != nil {
			charterPath := Path(p.Root, CharterYAML)
			charterBytes, e := os.ReadFile(charterPath)
			if replacement, ok := strips[charterPath]; ok {
				charterBytes = replacement
				e = nil
			}
			if e == nil {
				parts := []string{hashContent(string(charterBytes))}
				if b, e := os.ReadFile(dct.ActivationLedgerPath(p.Root)); e == nil {
					parts = append(parts, hashContent(string(b)))
				}
				hash := hashContent(strings.Join(parts, "\n"))
				if *m.BundleContentHash != hash {
					m.BundleContentHash = &hash
					path := filepath.Join(p.Root, ManifestPath)
					if e := os.WriteFile(path, []byte(canonicalYAML(m.Finalize().Object(true))), 0o644); e != nil {
						return written, e
					}
					written = append(written, path)
				}
			}
		}
	}
	for _, s := range p.Strips {
		out := strips[s.Path]
		if bytes.Equal(s.Observed, out) {
			continue
		}
		if e := os.WriteFile(s.Path, out, 0o644); e != nil {
			return written, e
		}
		written = append(written, s.Path)
	}
	return written, nil
}
