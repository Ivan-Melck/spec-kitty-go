package kittify

import (
	"bytes"
	"os"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// StampSchemaVersion is upgrade.runner.MigrationRunner._stamp_schema_version:
// re-dump metadata.yaml (PyYAML style, header kept) with
// spec_kitty.schema_version set to the current schema and the canonical
// capability map (an existing map is left alone, a legacy list is folded
// in). The file is rewritten only when the rendered bytes differ.
func StampSchemaVersion(root string) (bool, error) {
	path := MetadataPath(root)
	b, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	text := bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	v, err := yamlx.Parse(text)
	if err != nil {
		return false, nil
	}
	doc, ok := v.(*jsonx.Object)
	if !ok {
		return false, nil
	}
	sk, _ := mustGet(doc, "spec_kitty").(*jsonx.Object)
	if sk == nil {
		sk = &jsonx.Object{}
		doc.Set("spec_kitty", sk)
	}
	sk.Set("schema_version", CurrentSchemaVersion)
	caps := capabilitiesObject()
	switch x := mustGet(sk, "schema_capabilities").(type) {
	case *jsonx.Object:
		caps = x
	case []any:
		for _, n := range x {
			if s, ok := n.(string); ok {
				caps.Set(s, true)
			}
		}
	}
	sk.Set("schema_capabilities", caps)
	rendered := MetadataHeader + pyyaml.Dump(doc, pyyaml.Options{})
	if string(text) == rendered {
		return false, nil
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return true, writeAtomic(path, []byte(rendered), mode)
}

// SetMetadataVersion is the no-migrations version bump: when the recorded
// version differs from target, set it and last_upgraded_at (now) and save.
func SetMetadataVersion(root, target, now string) error {
	m := LoadMetadata(root)
	if m == nil || m.Version == target {
		return nil
	}
	m.Version = target
	m.LastUpgradedAt = now
	_, err := m.Save(root)
	return err
}
