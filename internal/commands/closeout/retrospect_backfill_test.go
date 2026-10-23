package closeout_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffRetrospectBackfill covers `retrospect backfill` beyond the dry
// run: the registry under .kittify/missions/ (a merged mission whose record
// was removed, a mission with no artifacts, an incomplete one), the
// authored record and its commit, --emit-skipped / --emit-failures, and the
// human panel.
func TestDiffRetrospectBackfill(t *testing.T) {
	p := acceptedPair(t)
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	p.Both(func(s *testkit.Sandbox) {
		slug := slugOf(t, s)
		meta, err := jsonx.DecodeObject([]byte(s.ReadFile(filepath.Join("kitty-specs", slug, "meta.json"))))
		if err != nil {
			t.Fatal(err)
		}
		meta.Set("completed_at", "2026-06-01T12:00:00+00:00")
		s.WriteFile(filepath.Join(".kittify", "missions", "m1", "meta.json"), jsonx.Dumps(meta, jsonx.Pretty)+"\n")
		s.WriteFile(filepath.Join(".kittify", "missions", "m2", "meta.json"),
			`{"mission_id": "01ZZZZZZZZZZZZZZZZZZZZZZZZ", "mission_slug": "ghost-mission", "completed_at": "2026-06-02T00:00:00+00:00"}`+"\n")
		s.WriteFile(filepath.Join(".kittify", "missions", "m3", "meta.json"),
			`{"mission_id": "01YYYYYYYYYYYYYYYYYYYYYYYY", "mission_slug": "open-mission"}`+"\n")
		s.WriteFile(filepath.Join(".kittify", "missions", "m4", "meta.json"),
			`{"mission_id": "01XXXXXXXXXXXXXXXXXXXXXXXX", "mission_slug": "old-mission", "completed_at": "2020-01-01"}`+"\n")
	})
	win := []string{"--since", "2026-01-01", "--until", "2026-12-31"}
	comparePair(t, p, append([]string{"retrospect", "backfill", "--dry-run", "--json"}, win...)...)
	comparePair(t, p, append([]string{"retrospect", "backfill", "--json"}, win...)...)
	p.Both(func(s *testkit.Sandbox) {
		s.Git("rm", "-q", filepath.Join("kitty-specs", slugOf(t, s), "retrospective.yaml"))
		s.Git("commit", "-q", "-m", "drop retrospective")
	})
	comparePair(t, p, append([]string{"retrospect", "backfill", "--dry-run"}, win...)...)
	comparePair(t, p, append([]string{"retrospect", "backfill", "--json", "--emit-skipped", "--emit-failures"}, win...)...)
	compareGit(t, p)
	compareMissionTree(t, p, false)
	p.CompareTree(".kittify/missions", true)
	comparePair(t, p, append([]string{"retrospect", "backfill", "--mission", "nomatch"}, win...)...)
	comparePair(t, p, "retrospect", "backfill", "--since", "not-a-date")
	_ = strings.TrimSpace
}
