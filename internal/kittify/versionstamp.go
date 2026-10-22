package kittify

import (
	"bytes"
	"os"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// legacyMigrationIDs is metadata._LEGACY_MIGRATION_ID_MAP.
var legacyMigrationIDs = map[string]string{
	"0.10.12_constitution_cleanup":          "0.10.12_charter_cleanup",
	"0.13.0_update_constitution_templates":  "0.13.0_update_charter_templates",
	"2.0.0_constitution_directory":          "2.0.0_charter_directory",
	"2.0.2_constitution_context_bootstrap":  "2.0.2_charter_context_bootstrap",
	"2.1.2_fix_constitution_doctrine_skill": "2.1.2_fix_charter_doctrine_skill",
}

// NormalizeAndSaveLegacyIDs is ProjectMetadata.normalize_and_save_legacy_ids.
func (m *Metadata) NormalizeAndSaveLegacyIDs(root string) []string {
	changed := false
	for i, r := range m.Applied {
		if n, ok := legacyMigrationIDs[r.ID]; ok {
			m.Applied[i].ID = n
			changed = true
		}
	}
	if !changed {
		return nil
	}
	m.Save(root)
	return []string{"Normalized legacy constitution-era migration IDs to charter-era IDs"}
}

// VersionStamp is metadata.VersionStamp: the whole metadata.yaml captured
// before a run, restored (version trio only) after a failure.
type VersionStamp struct{ Raw []byte }

// CaptureVersionStamp is VersionStamp.capture.
func CaptureVersionStamp(root string) VersionStamp {
	b, err := os.ReadFile(MetadataPath(root))
	if err != nil {
		return VersionStamp{}
	}
	return VersionStamp{Raw: b}
}

var stampKeys = []string{"version", "last_upgraded_at", "schema_version", "schema_capabilities"}

func parseMappingRaw(b []byte) *jsonx.Object {
	if b == nil {
		return nil
	}
	if _, err := pyjson.DecodeUTF8(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))); err != nil {
		return nil
	}
	v, err := yamlx.Parse(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))
	if err != nil {
		return nil
	}
	o, _ := v.(*jsonx.Object)
	return o
}

func withoutVersionTrio(d *jsonx.Object) *jsonx.Object {
	rest := &jsonx.Object{}
	for _, kv := range d.Pairs() {
		if kv.Key != "spec_kitty" && kv.Key != "environment" {
			rest.Set(kv.Key, kv.Value)
		}
	}
	if mig, ok := rest.Get("migrations"); ok {
		mo, isObj := mig.(*jsonx.Object)
		keep := false
		if isObj {
			if a, ok := mo.Get("applied"); ok && pyjson.Truthy(a) {
				keep = true
			}
		}
		if !keep {
			rest.Delete("migrations")
		}
	}
	if b := d.GetObject("spec_kitty"); b != nil {
		blk := &jsonx.Object{}
		for _, kv := range b.Pairs() {
			skip := false
			for _, k := range stampKeys {
				skip = skip || kv.Key == k
			}
			if !skip {
				blk.Set(kv.Key, kv.Value)
			}
		}
		rest.Set("spec_kitty", blk)
	}
	return rest
}

// Restore is VersionStamp.restore.
func (s VersionStamp) Restore(root string) bool {
	if s.Raw == nil {
		return false
	}
	path := MetadataPath(root)
	cur, err := os.ReadFile(path)
	if err == nil && bytes.Equal(cur, s.Raw) {
		return false
	}
	before := parseMappingRaw(s.Raw)
	var current *jsonx.Object
	if err == nil {
		current = parseMappingRaw(cur)
	}
	if before == nil || current == nil {
		return false
	}
	if pyjson.Equal(withoutVersionTrio(before), withoutVersionTrio(current)) {
		writeAtomic(path, s.Raw, 0o600)
		return true
	}
	old := before.GetObject("spec_kitty")
	if old == nil {
		old = &jsonx.Object{}
	}
	block := current.GetObject("spec_kitty")
	if block == nil {
		block = &jsonx.Object{}
		current.Set("spec_kitty", block)
	}
	changed := false
	for _, k := range stampKeys {
		if v, ok := old.Get(k); ok {
			cv, _ := block.Get(k)
			changed = changed || !pyjson.Equal(cv, v)
			block.Set(k, v)
		} else if v, ok := block.Get(k); ok {
			changed = changed || v != nil
			block.Delete(k)
		}
	}
	if !changed {
		return false
	}
	writeAtomic(path, []byte(MetadataHeader+pyyaml.Dump(current, pyyaml.Options{})), 0o600)
	return true
}
