package status

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func fixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testkit.ModuleRoot(), "internal", "commands", "agentcore", "testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMaterializeFinalizedFixture(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, EventsFile), fixture(t, "calc/finalized/status.events.jsonl"), 0o644)
	snap, err := Materialize(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := string(fixture(t, "calc/finalized/status.json"))
	if got := snap.Render(); got != want {
		t.Fatalf("status.json mismatch:\n%s", testkit.Diff(want, got))
	}
}
