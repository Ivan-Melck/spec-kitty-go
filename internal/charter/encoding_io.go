package charter

import (
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/charterenc"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
)

// Charter reads that upstream routes through the encoding chokepoint
// (charter.activation._io.load_charter_file, ported in internal/charterenc):
// every successful load decodes through the canonical detector and appends
// an encoding-provenance record.

// loadCharterText is load_charter_file(path).text; ok is false when the
// load raised (ambiguous encoding, undecodable bytes after a BOM, read
// error).
func loadCharterText(path string) ([]byte, bool) {
	c, err := charterenc.LoadFile(path, false)
	if err != nil {
		return nil, false
	}
	return []byte(c.Text), true
}

// EnsureBundleFresh is sync.ensure_charter_bundle_fresh's observable effect:
// when charter.md exists and the bundle is stale (no metadata.yaml, or a
// charter_hash that differs, or an unreadable charter), sync() loads
// charter.md through the chokepoint. sync() itself writes nothing and
// swallows load errors.
func EnsureBundleFresh(root string) {
	charterPath := Path(root, CharterMD)
	if !exists(charterPath) {
		return
	}
	stale := true
	metaPath := filepath.Join(filepath.Dir(charterPath), "metadata.yaml")
	if b, err := os.ReadFile(charterPath); err == nil && utf8.Valid(b) && exists(metaPath) {
		stored := ""
		if meta := readYAMLObject(metaPath); meta != nil {
			if v, ok := meta.Get("charter_hash"); ok && v != nil {
				stored = pyStrOf(v)
			}
		}
		stale = hashContent(string(b)) != stored
	}
	if stale {
		loadCharterText(charterPath)
	}
}

// SyncCharterIfPresent is generate._sync_charter_if_present: sync(force=True)
// loads charter.md through the chokepoint when it exists. The returned error
// is SyncResult.error (sync swallows the exception).
func SyncCharterIfPresent(root string) error {
	charterPath := Path(root, CharterMD)
	if !exists(charterPath) {
		return nil
	}
	_, err := charterenc.LoadFile(charterPath, false)
	return err
}

// indexParadigmAssets is compiler._index_yaml_assets over the built-in
// paradigm directory: every *.paradigm.yaml (sorted) is loaded through the
// chokepoint.
func indexParadigmAssets() {
	dir := filepath.Join(dct.BuiltInRoot(), "paradigms")
	matches, _ := filepath.Glob(filepath.Join(dir, "*.paradigm.yaml"))
	sort.Strings(matches)
	for _, m := range matches {
		loadCharterText(m)
	}
}
