package validatetasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// missionDirOf is _mission_dir_of: the parent of the file's "tasks"
// directory, else its own directory.
func missionDirOf(taskFile string) string {
	for d := filepath.Dir(taskFile); d != filepath.Dir(d); d = filepath.Dir(d) {
		if filepath.Base(d) == "tasks" {
			return filepath.Dir(d)
		}
	}
	return filepath.Dir(taskFile)
}

// missionLockKey is mission_lock_key: the coordination directory name
// (<slug>-<mid8>) of a coordination-routed Mission, else the directory name.
func missionLockKey(featureDir string) string {
	name := filepath.Base(featureDir)
	b, err := os.ReadFile(filepath.Join(featureDir, "meta.json"))
	if err != nil {
		return name
	}
	var meta map[string]any
	if json.Unmarshal(b, &meta) != nil {
		return name
	}
	branch, _ := meta["coordination_branch"].(string)
	if strings.TrimSpace(branch) == "" {
		return name
	}
	text := func(k string) string { s, _ := meta[k].(string); return s }
	slug := text("mission_slug")
	if slug == "" {
		slug = name
	}
	mid8 := text("mid8")
	if mid8 == "" {
		mid8 = mission.ResolveMid8(slug, text("mission_id"))
	}
	if mid8 == "" {
		mid8 = mission.Mid8FromSlug(slug)
	}
	return mission.CoordMissionDirName(slug, mid8)
}

// missionWriteLock is mission_write_lock(feature_dir): an exclusive lock on
// <git common dir>/spec-kitty-locks/<key>.status.lock with the holder
// sidecar recorded while held. The returned release truncates nothing and
// removes the sidecar, leaving the (empty) lock file as upstream does.
func missionWriteLock(featureDir string) func() {
	common, err := gitx.CommonDir(featureDir)
	if err != nil {
		return func() {}
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(featureDir, common)
	}
	dir := filepath.Join(common, "spec-kitty-locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
	lockPath := filepath.Join(dir, missionLockKey(featureDir)+".status.lock")
	unlock, err := flockFile(lockPath)
	if err != nil {
		return func() {}
	}
	holder := lockPath + ".holder"
	payload := jsonx.NewObject("acquired_at", clock.NowUTCISO(), "pid", os.Getpid(), "thread", "MainThread")
	os.WriteFile(holder, []byte(jsonx.Dumps(payload, jsonx.Options{Indent: -1, SortKeys: true})), 0o644)
	return func() {
		os.Remove(holder)
		unlock()
	}
}
