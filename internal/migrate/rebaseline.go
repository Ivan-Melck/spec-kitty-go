package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// RebaselineOutcome is dossier.rebaseline.RebaselineOutcome.
type RebaselineOutcome struct {
	SnapshotPath, MissionSlug, OldHash, NewHash string
	Changed                                     bool
	Error                                       *string
}

// RecordedSnapshotFiles is iter_recorded_snapshot_files(root): every
// **/.kittify/dossiers/*/snapshot-latest.json below root, sorted.
func RecordedSnapshotFiles(root string) []string {
	var out []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && info.Name() == "dossiers" && filepath.Base(filepath.Dir(p)) == ".kittify" {
			entries, _ := os.ReadDir(p)
			for _, e := range entries {
				f := filepath.Join(p, e.Name(), "snapshot-latest.json")
				if st, err := os.Stat(f); err == nil && !st.IsDir() {
					out = append(out, f)
				}
			}
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return PyPathLess(out[i], out[j]) })
	return out
}

// RebaselineSnapshots is rebaseline_recorded_snapshots(root, dry_run).
// Canonical ("sha256:") snapshots are left untouched; any other recorded
// hash is replaced by a fresh snapshot of the current source tree.
//
// A recorded snapshot that is valid JSON but not an object crashes upstream
// (AttributeError on data.get); that surfaces as an *UncaughtError.
func RebaselineSnapshots(root string, dryRun bool) ([]RebaselineOutcome, error) {
	var out []RebaselineOutcome
	for _, p := range RecordedSnapshotFiles(root) {
		slug := filepath.Base(filepath.Dir(p))
		b, err := os.ReadFile(p)
		var data *jsonx.Object
		if err == nil {
			var text string
			text, err = pyjson.DecodeUTF8(b)
			if err == nil {
				var v any
				v, err = pyjson.Loads(text)
				if err == nil {
					var isObj bool
					if data, isObj = v.(*jsonx.Object); !isObj {
						return out, &UncaughtError{Type: "AttributeError", Msg: fmt.Sprintf("'%s' object has no attribute 'get'", pyjson.TypeName(v))}
					}
				}
			}
		}
		if err != nil {
			warnf("Cannot read recorded snapshot %s: %v", p, err)
			out = append(out, RebaselineOutcome{SnapshotPath: p, MissionSlug: slug, Error: strPtr("unreadable_snapshot")})
			continue
		}
		old := ""
		if v, ok := data.Get("parity_hash_sha256"); ok {
			old = pyjson.Str(v)
		}
		if v, ok := data.Get("mission_slug"); ok && pyjson.Truthy(v) {
			slug = pyjson.Str(v)
		}
		if strings.HasPrefix(old, "sha256:") {
			out = append(out, RebaselineOutcome{SnapshotPath: p, MissionSlug: slug, OldHash: old, NewHash: old})
			continue
		}
		featureDir := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(p))))
		if !isDirFollow(featureDir) {
			warnf("Source dir missing for recorded snapshot %s", p)
			out = append(out, RebaselineOutcome{SnapshotPath: p, MissionSlug: slug, OldHash: old, NewHash: old, Error: strPtr("source_missing")})
			continue
		}
		snap, newHash, err := reindexSnapshot(featureDir)
		if err != nil {
			warnf("Re-index failed for %s: %s", featureDir, err)
			out = append(out, RebaselineOutcome{SnapshotPath: p, MissionSlug: slug, OldHash: old, NewHash: old, Error: strPtr("reindex_failed: " + err.Error())})
			continue
		}
		if !dryRun {
			os.WriteFile(p, []byte(jsonx.Dumps(snap, jsonx.Pretty)), 0o644)
		}
		out = append(out, RebaselineOutcome{SnapshotPath: p, MissionSlug: slug, OldHash: old, NewHash: newHash, Changed: true})
	}
	return out, nil
}
