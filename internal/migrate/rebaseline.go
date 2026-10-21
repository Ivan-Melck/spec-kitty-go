package migrate

import (
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
//
// Deviation: re-indexing a non-canonical snapshot through the dossier
// indexer is not ported; such snapshots are reported with
// error "reindex_failed: dossier re-index is not supported by this build"
// and left untouched (canonical "sha256:" snapshots are a no-op as upstream).
func RebaselineSnapshots(root string, dryRun bool) []RebaselineOutcome {
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
					data, _ = v.(*jsonx.Object)
				}
			}
		}
		if err != nil || data == nil {
			if err != nil {
				warnf("Cannot read recorded snapshot %s: %v", p, err)
			}
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
		msg := "dossier re-index is not supported by this build"
		warnf("Re-index failed for %s: %s", featureDir, msg)
		out = append(out, RebaselineOutcome{SnapshotPath: p, MissionSlug: slug, OldHash: old, NewHash: old, Error: strPtr("reindex_failed: " + msg)})
	}
	return out
}
