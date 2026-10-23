package charter

import (
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/charsetnorm"
	"github.com/spec-kitty/go-kitty3/internal/charterenc"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Charter reads that upstream routes through the encoding chokepoint
// (charter.activation._io.load_charter_file, ported in internal/charterenc):
// every successful load decodes through the canonical detector and appends
// an encoding-provenance record.

// LoadPanic carries an exception load_charter_file raises (CharterEncodingError
// for an ambiguous code page, or the UnicodeDecodeError for undecodable bytes
// after a BOM) up to the command boundary, where upstream's handlers (or the
// uncaught-exception path) render it. Recover it with CatchLoad.
type LoadPanic struct{ Err error }

// loadCharterText is load_charter_file(path).text. Encoding failures raise
// (panic with *LoadPanic) like upstream; ok is false only for read errors.
func loadCharterText(path string) ([]byte, bool) {
	c, err := charterenc.LoadFile(path, false)
	if err != nil {
		if _, isPath := err.(*os.PathError); isPath {
			return nil, false
		}
		panic(&LoadPanic{Err: err})
	}
	return []byte(c.Text), true
}

// CatchLoad runs fn and returns the charter-load exception it raised (nil
// when none). Other panics propagate.
func CatchLoad(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if lp, ok := r.(*LoadPanic); ok {
				err = lp.Err
				return
			}
			panic(r)
		}
	}()
	fn()
	return nil
}

// IsCharterEncodingError reports a CharterEncodingError (the code-only
// str(exc) upstream handlers print).
func IsCharterEncodingError(err error) bool {
	_, ok := err.(*charterenc.Error)
	return ok
}

// ExceptionMessage is str(exc) for a charter-load exception: a
// KittyInternalConsistencyError stringifies to its code.
func ExceptionMessage(err error) string {
	if e, ok := err.(*charterenc.Error); ok {
		return e.Code
	}
	if n, ok := err.(*charsetnorm.NotedError); ok {
		return n.Err.Error()
	}
	return err.Error()
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
	if b, err := os.ReadFile(charterPath); err == nil {
		if !utf8.Valid(b) {
			// is_stale -> hash_charter reads charter.md as strict UTF-8.
			if _, derr := pyjson.DecodeUTF8(b); derr != nil {
				warnLog("Failed to evaluate charter bundle freshness: %s", derr.Error())
			}
		} else if exists(metaPath) {
			stored := ""
			if meta := readYAMLObject(metaPath); meta != nil {
				if v, ok := meta.Get("charter_hash"); ok && v != nil {
					stored = pyStrOf(v)
				}
			}
			stale = hashContent(string(b)) != stored
		}
	}
	if stale {
		// sync() swallows the exception into SyncResult.error.
		if _, err := charterenc.LoadFile(charterPath, false); err != nil {
			if _, isPath := err.(*os.PathError); !isPath {
				warnLog("Charter auto-sync failed while refreshing extracted artifacts: %s", ExceptionMessage(err))
			}
		}
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
