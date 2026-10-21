package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

// WriteDerivedViews is write_derived_views + generate_progress_json +
// generate_lifecycle_json over featureDir into derived (the writing
// materialize: the mission's status.json is refreshed too). Returns the
// file names written.
func WriteDerivedViews(featureDir, derived string) ([]string, error) {
	snap, err := status.Materialize(featureDir)
	if err != nil {
		return nil, PyStoreError(featureDir, err)
	}
	slug := snap.MissionSlug
	if slug == "" {
		slug = filepath.Base(featureDir)
	}
	outDir := filepath.Join(derived, slug)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	if err := writeDerived(filepath.Join(outDir, status.SnapshotFile), snap.ToObject()); err != nil {
		return nil, err
	}
	if err := writeDerived(filepath.Join(outDir, status.BoardSummaryFile), status.BoardSummary(snap)); err != nil {
		return []string{"status.json"}, err
	}
	if err := writeDerived(filepath.Join(outDir, status.DerivedProgressFile), status.ProgressObject(snap)); err != nil {
		return nil, err
	}
	lc := status.LifecycleObject(featureDir, snap, clock.Now())
	lcDir := outDir
	if s := lc.GetString("mission_slug"); s != "" && s != slug {
		lcDir = filepath.Join(derived, s)
		if err := os.MkdirAll(lcDir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := writeDerived(filepath.Join(lcDir, status.DerivedLifecycle), lc); err != nil {
		return nil, err
	}
	return []string{"status.json", "board-summary.json", "progress.json", "lifecycle.json"}, nil
}

// PyStoreError re-renders an event-log JSON error with CPython's
// JSONDecodeError text ("Invalid JSON on line N: Expecting value: ...").
func PyStoreError(dir string, err error) error {
	var se *status.StoreError
	if !errors.As(err, &se) || !strings.HasPrefix(se.Msg, "Invalid JSON on line ") {
		return err
	}
	b, rerr := os.ReadFile(status.EventsPath(dir))
	if rerr != nil {
		return err
	}
	for i, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		if _, perr := pyjson.Loads(l); perr != nil {
			return fmt.Errorf("Invalid JSON on line %d: %v", i+1, perr)
		}
	}
	return err
}

func writeDerived(path string, o *jsonx.Object) error {
	text := jsonx.Dumps(o, status.SnapshotOptions) + "\n"
	tmp := strings.TrimSuffix(path, ".json") + ".json.tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
