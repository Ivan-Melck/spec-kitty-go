package tracker_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestGoTrackerLifecycle(t *testing.T) {
	t.Parallel()
	s := missionfx.NewProject(t)
	expect := func(code int, wantOut string, args ...string) string {
		t.Helper()
		r := s.Run(args...)
		if r.Code != code || !strings.Contains(r.Stdout+r.Stderr, wantOut) {
			t.Fatalf("%v: exit %d (want %d)\nstdout:\n%s\nstderr:\n%s", args, r.Code, code, r.Stdout, r.Stderr)
		}
		return r.Stdout
	}
	expect(0, `"removed": [`, "tracker", "providers", "--json")
	expect(1, "No tracker bound. Run `spec-kitty tracker bind` first.", "tracker", "status")
	expect(1, `"ok": false`, "tracker", "map", "list", "--json")
	expect(1, "Error: Provider 'linear' is no longer supported.", "tracker", "bind", "--provider", "Linear")
	expect(1, "Unknown provider 'x'. Supported: beads, fp", "tracker", "bind", "--provider", "x")
	expect(2, "1<=x<=10000", "tracker", "sync", "pull", "--limit", "0")
	expect(0, "- credentials_saved: yes", "tracker", "bind", "--provider", "beads", "--workspace", "ws", "--credential", "command=bd")
	if b := s.ReadFile(".kittify/config.yaml"); !strings.Contains(b, "tracker:\n  provider: beads\n") {
		t.Fatalf("config:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Home, ".spec-kitty", "credentials")); string(b) != "[tracker.providers.beads]\ncommand = \"bd\"\n" {
		t.Fatalf("credentials:\n%s", b)
	}
	out := expect(0, `"configured": true`, "tracker", "status", "--json")
	if !strings.Contains(out, "e46273cd70c682bd.db") {
		t.Fatalf("db path: %s", out)
	}
	expect(0, "Mapping saved: WP01 -> 7", "tracker", "map", "add", "--wp-id", "WP01", "--external-id", "7")
	expect(1, "IntegrityError: UNIQUE constraint failed", "tracker", "map", "add", "--wp-id", "WP02", "--external-id", "7")
	expect(0, "- WP01: beads:7", "tracker", "map", "list")
	expect(1, "no tracker.egress key is recorded", "tracker", "sync", "pull")
	expect(1, "Snapshot publish is not supported for local providers", "tracker", "sync", "publish")
	expect(0, "Tracker binding removed", "tracker", "unbind")
	if b := s.ReadFile(".kittify/config.yaml"); strings.Contains(b, "tracker:") {
		t.Fatalf("config still bound:\n%s", b)
	}
}

func TestGoTrackerSyncWithFakeBeads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake")
	}
	t.Parallel()
	s := missionfx.NewProject(t)
	s.WriteFile("fake/bd", fakeBD)
	os.Chmod(filepath.Join(s.Dir, "fake", "bd"), 0o755)
	s.WriteFile("fake/state/list.json", bdList1)
	s.MustRun("tracker", "bind", "--provider", "beads", "--workspace", "ws", "--credential", "command="+filepath.Join(s.Dir, "fake", "bd"))
	s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"  egress: permitted\n")
	r := s.MustRun("tracker", "sync", "pull", "--json")
	if !strings.Contains(r.Stdout, `"pulled_created": 2`) {
		t.Fatalf("pull: %s", r.Stdout)
	}
	r = s.MustRun("tracker", "sync", "run")
	if !strings.Contains(r.Stdout, "Sync run completed (beads)\n- pulled_created: 0\n- pulled_updated: 0\n") {
		t.Fatalf("run: %s", r.Stdout)
	}
	log := s.ReadFile("fake/state/log")
	if !strings.Contains(log, "bd|--json|list|--updated-after|2026-01-03\n") {
		t.Fatalf("log:\n%s", log)
	}
	r = s.MustRun("tracker", "status")
	if !strings.Contains(r.Stdout, "- issue_count: 2") {
		t.Fatalf("status: %s", r.Stdout)
	}
}
