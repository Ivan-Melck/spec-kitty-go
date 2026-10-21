// Package migrate implements the one-time project-data migrations behind
// `spec-kitty migrate ...` (identity / topology / mission-type backfills,
// lifecycle normalization, runtime-state cutover, ...) and the shared
// fail-closed meta.json reader they use.
package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// MetaReadError is MissionMetaReadError: meta.json exists but is corrupt,
// not an object, or unreadable.
type MetaReadError struct {
	Path  string
	Cause string // the legacy ValueError text ("Malformed JSON in ...")
}

func (e *MetaReadError) Error() string {
	return fmt.Sprintf("Cannot read %s: %s — fail-closed (meta.json exists but is corrupt or unreadable)", e.Path, e.Cause)
}

// LoadMetaFailClosed is core.paths.load_meta_fail_closed: (nil, nil) when
// meta.json is absent, the parsed object, or a *MetaReadError.
func LoadMetaFailClosed(dir string) (*jsonx.Object, error) {
	p := filepath.Join(dir, mission.MetaFile)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &MetaReadError{Path: p, Cause: fmt.Sprintf("Malformed JSON in %s: %v", p, err)}
	}
	text, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return nil, &MetaReadError{Path: p, Cause: fmt.Sprintf("Malformed JSON in %s: %v", p, err)}
	}
	v, err := pyjson.Loads(text)
	if err != nil {
		return nil, &MetaReadError{Path: p, Cause: fmt.Sprintf("Malformed JSON in %s: %v", p, err)}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, &MetaReadError{Path: p, Cause: fmt.Sprintf("Expected JSON object in %s, got %s", p, pyjson.TypeName(v))}
	}
	return o, nil
}

// MissionDirs returns the kitty-specs/ mission directories (sorted names;
// symlinks to directories included), or nil when kitty-specs/ is absent.
func MissionDirs(root string) []string {
	specs := project.Specs(root)
	entries, err := os.ReadDir(specs)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if st, err := os.Stat(filepath.Join(specs, e.Name())); err == nil && st.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// SpecsExist reports whether kitty-specs/ is a directory.
func SpecsExist(root string) bool {
	st, err := os.Stat(project.Specs(root))
	return err == nil && st.IsDir()
}

// scopedMissionDirs is the shared kitty-specs walk: every mission dir, or
// only the one named slug (none when it does not exist).
func scopedMissionDirs(root string, slug *string) []string {
	if !SpecsExist(root) {
		warnf("kitty-specs/ not found at %s", root)
		return nil
	}
	all := MissionDirs(root)
	if slug == nil {
		return all
	}
	for _, n := range all {
		if n == *slug {
			return []string{n}
		}
	}
	warnf("No mission directory found for slug %s", pyjson.ReprString(*slug))
	return nil
}

// UpdateMeta re-reads meta.json fail-closed, applies mutate and writes it
// back (sorted keys) unless mutate reports no change (locked_update_meta).
func UpdateMeta(dir string, mutate func(meta *jsonx.Object) bool) (*jsonx.Object, error) {
	meta, err := LoadMetaFailClosed(dir)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("meta.json not found in %s", dir)
	}
	if mutate(meta) {
		if err := mission.WriteMeta(dir, meta, false); err != nil {
			return nil, err
		}
	}
	return meta, nil
}

func strPtr(s string) *string { return &s }

// Warn receives logger.warning records (the CLI routes them to a
// RichHandler-style stderr line; nil/no-op in --json mode).
var Warn = func(msg string) {}

func warnf(format string, a ...any) { Warn(fmt.Sprintf(format, a...)) }

// OptStr renders an optional string as a JSON value (nil → null).
func OptStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
