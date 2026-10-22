package healthcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledAuditFixtures(t *testing.T) {
	t.Setenv("SPEC_KITTY_HOME", t.TempDir())
	root := BundledAuditFixtures()
	for _, f := range []string{"clean_modern/meta.json", "corrupt_jsonl/status.events.jsonl", "forbidden_event_type/status.events.jsonl", "legacy_feature_slug/meta.json"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	// a materialized tree is reused (fixture edits persist like the wheel's)
	os.WriteFile(filepath.Join(root, "clean_modern", "marker"), []byte("x"), 0o644)
	if again := BundledAuditFixtures(); again != root {
		t.Fatalf("root moved: %s != %s", again, root)
	}
	if _, err := os.Stat(filepath.Join(root, "clean_modern", "marker")); err != nil {
		t.Fatal("tree was re-extracted")
	}
	rep := RunAuditIn(filepath.Dir(root), root, nil)
	if len(rep.Missions) != 4 || stableMissionDir(rep.Missions[0].Dir, rep.Missions[0].Slug) != "clean_modern" {
		t.Fatalf("audit: %+v", rep.Missions)
	}
}
