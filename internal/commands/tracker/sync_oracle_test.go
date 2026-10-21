package tracker_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// fakeBD is a stand-in for the beads CLI: it logs its argv and serves
// canned JSON from the state directory next to it.
const fakeBD = `#!/bin/sh
D=$(dirname "$0")/state
printf '%s' "bd" >> "$D/log"; for a in "$@"; do printf '|%s' "$a" >> "$D/log"; done; echo >> "$D/log"
[ -f "$D/fail" ] && { cat "$D/fail" >&2; exit 3; }
[ "$1" = "--json" ] || exit 9
case "$2" in
  list) cat "$D/list.json" ;;
  show) cat "$D/show-$3.json" 2>/dev/null || echo '[]' ;;
  create)
    n=$(cat "$D/counter" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$D/counter"
    printf '{"id":"bd-new%s","title":"%s","status":"open","issue_type":"task","priority":2,"updated_at":"2026-02-01T00:00:00Z"}' "$n" "$3" > "$D/show-bd-new$n.json"
    printf '{"id":"bd-new%s"}' "$n" ;;
  update) echo '{}' ;;
  *) exit 7 ;;
esac
`

// fakeFP is a stand-in for the fp CLI.
const fakeFP = `#!/bin/sh
D=$(dirname "$0")/state
printf '%s' "fp" >> "$D/log"; for a in "$@"; do printf '|%s' "$a" >> "$D/log"; done; echo >> "$D/log"
case "$1 $2" in
  "issue list") cat "$D/fp-list.txt" ;;
  "issue show") cat "$D/fp-show-$3.txt" 2>/dev/null ;;
  "issue create") echo "Created FP-9 ok" ;;
  "issue update") echo "updated" ;;
  "context "*) cat "$D/fp-context-$2.txt" 2>/dev/null ;;
  *) exit 7 ;;
esac
`

const bdList1 = `[
 {"id":"bd-1","title":"First issue","description":"Body one","status":"open","issue_type":"feature","priority":1,
  "labels":["a","b"],"assignee":"alice","metadata":{"k":"v"},"external_ref":"http://x/1","parent":"bd-0",
  "dependencies":[{"depends_on_id":"bd-2","dependency_type":"blocks"},{"id":"bd-3","type":"related"}],
  "created_at":"2026-01-01T10:00:00Z","updated_at":"2026-01-02T10:00:00.123456Z"},
 {"id":"bd-2","title":"Second é","status":"in_progress","issue_type":"bug","priority":"7",
  "updated_at":"2026-01-03T10:00:00+02:00"},
 {"id":"bd-2","title":"Second dup older","status":"closed","issue_type":"bug","updated_at":"2026-01-01T00:00:00Z"},
 "junk"
]`

const bdList2 = `{"items":[
 {"id":"bd-1","title":"First issue renamed","description":"Body one","status":"blocked","issue_type":"feature","priority":3,
  "labels":["a"],"updated_at":"2026-01-05T10:00:00Z"}
]}`

func setupFake(t *testing.T, p *testkit.Pair, files map[string]string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) {
		for name, content := range files {
			s.WriteFile(filepath.Join("fake", name), content)
			if name == "bd" || name == "fp" {
				os.Chmod(filepath.Join(s.Dir, "fake", name), 0o755)
			}
		}
		os.MkdirAll(filepath.Join(s.Dir, "fake", "state"), 0o755)
	})
}

func setState(p *testkit.Pair, name, content string) {
	p.Both(func(s *testkit.Sandbox) {
		path := filepath.Join(s.Dir, "fake", "state", name)
		if content == "" {
			os.Remove(path)
			return
		}
		os.WriteFile(path, []byte(content), 0o644)
	})
}

func compareLog(t *testing.T, p *testkit.Pair) {
	t.Helper()
	compareFile(t, p, func(s *testkit.Sandbox) string { return filepath.Join(s.Dir, "fake", "state", "log") })
}

// dumpIssues returns the cached issue rows (identity, system, workspace,
// issue id, payload) via the sqlite3 CLI.
func dumpIssues(t *testing.T, s *testkit.Sandbox) string {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return ""
	}
	dbs, _ := filepath.Glob(filepath.Join(s.Home, ".spec-kitty", "trackers", "*.db"))
	var out strings.Builder
	for _, db := range dbs {
		b, err := exec.Command(bin, db, "PRAGMA integrity_check; SELECT identity, system, workspace, issue_id, payload FROM tracker_issues ORDER BY identity; SELECT checkpoint_key, cursor, updated_since FROM tracker_checkpoints;").CombinedOutput()
		if err != nil {
			t.Fatalf("sqlite3: %v\n%s", err, b)
		}
		out.WriteString(filepath.Base(db) + "\n" + string(b))
	}
	return out.String()
}

func compareDB(t *testing.T, p *testkit.Pair) {
	t.Helper()
	g, o := dumpIssues(t, p.Go), dumpIssues(t, p.Oracle)
	if g != o {
		t.Errorf("tracker db differs:\n%s", testkit.Diff(o, g))
	}
}

func bindFake(t *testing.T, p *testkit.Pair, provider, mode string) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) {
		cmd := filepath.Join(s.Dir, "fake", map[string]string{"beads": "bd", "fp": "fp"}[provider])
		var r testkit.Result
		args := []string{"tracker", "bind", "--provider", provider, "--workspace", "ws", "--ownership-mode", mode, "--credential", "command=" + cmd, "--credential", "actor=tester"}
		if s == p.Go {
			r = s.Run(args...)
		} else {
			r = s.RunOracle(args...)
		}
		if r.Code != 0 {
			t.Fatalf("bind: %d %s %s", r.Code, r.Stdout, r.Stderr)
		}
		cfg := s.ReadFile(".kittify/config.yaml")
		s.WriteFile(".kittify/config.yaml", cfg+"  egress: permitted\n")
	})
}

func TestOracleSyncBeads(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	setupFake(t, p, map[string]string{"bd": fakeBD})
	bindFake(t, p, "beads", "external_authoritative")
	setState(p, "list.json", bdList1)
	setState(p, "show-bd-1.json", `{"issue":{"id":"bd-1","title":"First issue","status":"open"},"comments":[{"text":"hi"}],"dependencies":[]}`)
	g, _ := step(t, p, "tracker", "sync", "pull", "--json")
	mustContain(t, g.Stdout, `"pulled_created": 2`)
	compareDB(t, p)
	step(t, p, "tracker", "sync", "pull")
	step(t, p, "tracker", "status", "--json")
	setState(p, "list.json", bdList2)
	step(t, p, "tracker", "sync", "run", "--json", "--limit", "1")
	step(t, p, "tracker", "sync", "push")
	compareDB(t, p)
	compareLog(t, p)

	// Local-authoritative push reverts remote edits: title/body/labels go
	// through `bd update`, a status change is refused by the gateway.
	p.Both(func(s *testkit.Sandbox) {
		cfg := s.ReadFile(".kittify/config.yaml")
		s.WriteFile(".kittify/config.yaml", strings.Replace(cfg, "mode: external_authoritative", "mode: spec_kitty_authoritative", 1))
	})
	setState(p, "list.json", `[{"id":"bd-1","title":"Remote edit","status":"blocked","issue_type":"feature","priority":3,"labels":["z"],"updated_at":"2026-01-09T10:00:00Z"},
	 {"id":"bd-2","title":"Second é","status":"in_progress","issue_type":"bug","priority":4,"updated_at":"2026-01-03T10:00:00+02:00"}]`)
	g, _ = step(t, p, "tracker", "sync", "push", "--json")
	mustContain(t, g.Stdout, `"pushed_updated": 1`)
	compareLog(t, p)
	setState(p, "list.json", `[{"id":"bd-1","title":"Remote edit","status":"blocked","issue_type":"feature","priority":3,"labels":["z"],"updated_at":"2026-01-09T10:00:00Z"}]`)
	step(t, p, "tracker", "sync", "push", "--json")
	compareLog(t, p)
	compareDB(t, p)
	// A status revert needs `bd update --status`, which the gateway refuses.
	setState(p, "list.json", `[{"id":"bd-1","title":"Remote edit","status":"open","issue_type":"feature","priority":3,"labels":["z"],"updated_at":"2026-01-09T10:00:00Z"}]`)
	g, _ = step(t, p, "tracker", "sync", "push", "--json")
	mustContain(t, g.Stderr, "forbidden lifecycle flag '--status'")

	// Failures: a failing bd list is a recorded page failure on pull and an
	// uncaught error on push; malformed JSON is an uncaught contract error.
	setState(p, "fail", "boom from bd\n")
	step(t, p, "tracker", "sync", "pull", "--json")
	step(t, p, "tracker", "sync", "push", "--json")
	setState(p, "fail", "")
	setState(p, "list.json", "{not json")
	step(t, p, "tracker", "sync", "pull")
	setState(p, "list.json", `[{"id":"bd-9","title":"x","status":"weird"}]`)
	step(t, p, "tracker", "sync", "pull")
	setState(p, "list.json", `[{"id":"bd-9","title":"  "}]`)
	step(t, p, "tracker", "sync", "pull")
	compareLog(t, p)
	compareDB(t, p)
}

func TestOracleSyncBeadsCreateAndMissingCommand(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	setupFake(t, p, map[string]string{"bd": fakeBD})
	bindFake(t, p, "beads", "spec_kitty_authoritative")
	setState(p, "list.json", `[{"id":"bd-5","title":"Will vanish","description":"d","status":"open","issue_type":"chore","priority":0,"labels":["l1","l2"],"parent":"bd-4","updated_at":"2026-01-02T10:00:00Z"},
	 {"id":"bd-6","title":"Blocked one","status":"blocked","issue_type":"epic","metadata":{"feature_slug":"x"},"updated_at":"2026-01-02T10:00:00Z"}]`)
	step(t, p, "tracker", "sync", "pull")
	setState(p, "list.json", "[]")
	g, _ := step(t, p, "tracker", "sync", "push", "--json")
	mustContain(t, g.Stdout, `"pushed_created": 1`)
	compareLog(t, p)
	compareDB(t, p)
	// A missing executable is an uncaught FileNotFoundError.
	p.Both(func(s *testkit.Sandbox) {
		os.Rename(filepath.Join(s.Dir, "fake", "bd"), filepath.Join(s.Dir, "fake", "bd.off"))
	})
	step(t, p, "tracker", "sync", "pull")
}

// TestOracleDBInterop alternates the oracle and the Go binary on ONE
// project/HOME: each must read and update the other's SQLite cache and
// credential file.
func TestOracleDBInterop(t *testing.T) {
	t.Parallel()
	testkit.Oracle(t)
	s := testkit.NewGitSandbox(t)
	missionfx.OracleInit(t, s)
	s.WriteFile("fake/bd", fakeBD)
	os.Chmod(filepath.Join(s.Dir, "fake", "bd"), 0o755)
	s.WriteFile("fake/state/list.json", bdList1)
	same := func(args ...string) {
		t.Helper()
		g, o := s.Run(args...), s.RunOracle(args...)
		if g.Code != o.Code || g.Stdout != o.Stdout {
			t.Errorf("%v: go=%d oracle=%d\n%s\n--- go stderr:\n%s\n--- oracle stderr:\n%s", args, g.Code, o.Code, testkit.Diff(o.Stdout, g.Stdout), g.Stderr, o.Stderr)
		}
	}
	missionfx.RunOracle(t, s, "tracker", "bind", "--provider", "beads", "--workspace", "ws", "--credential", "command="+filepath.Join(s.Dir, "fake", "bd"))
	s.MustRun("tracker", "map", "add", "--wp-id", "WP01", "--external-id", "1")
	missionfx.RunOracle(t, s, "tracker", "map", "add", "--wp-id", "WP02", "--external-id", "2", "--external-key", "K2")
	s.MustRun("tracker", "map", "add", "--wp-id", "WP01", "--external-id", "3", "--external-url", "u")
	same("tracker", "map", "list", "--json")
	same("tracker", "status", "--json")
	s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"  egress: permitted\n")
	missionfx.RunOracle(t, s, "tracker", "sync", "pull")
	same("tracker", "status", "--json")
	s.MustRun("tracker", "sync", "pull")
	same("tracker", "sync", "pull", "--json")
	same("tracker", "map", "list")
	s.MustRun("tracker", "bind", "--provider", "beads", "--workspace", "ws", "--credential", "command="+filepath.Join(s.Dir, "fake", "bd"), "--credential", "actor=x")
	same("tracker", "status")
	if got := dumpIssues(t, s); got != "" && !strings.Contains(got, "\nok\n") {
		t.Errorf("integrity: %s", got)
	}
}

const fpList = `FP-1 [todo] [high] Build the thing
├─ FP-2 [in-progress] Child task
└─ FP-3 [Cancelled] [7] Old work
not an issue line
`

func TestOracleSyncFP(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	setupFake(t, p, map[string]string{"fp": fakeFP})
	bindFake(t, p, "fp", "spec_kitty_authoritative")
	setState(p, "fp-list.txt", fpList)
	step(t, p, "tracker", "sync", "pull", "--json")
	step(t, p, "tracker", "map", "add", "--wp-id", "WP01", "--external-id", "FP-1")
	// Remote drifts: local-authoritative push patches status/priority; a
	// title change is a recorded capability failure; a vanished issue is
	// re-created.
	setState(p, "fp-list.txt", "FP-1 [done] [low] Build the thing renamed\nFP-2 [blocked] Child task\n")
	setState(p, "fp-show-FP-1.txt", "FP-1 [todo] [high] Build the thing\n")
	setState(p, "fp-context-FP-1.txt", "  context body  \n")
	setState(p, "fp-show-FP-9.txt", "Issue FP-9\n")
	step(t, p, "tracker", "sync", "run", "--json")
	compareLog(t, p)
	compareDB(t, p)
	// Same drift with every show/context answer available.
	setState(p, "fp-show-FP-2.txt", "FP-2 [in-progress] Child task\n")
	setState(p, "fp-context-FP-2.txt", "\n")
	setState(p, "fp-context-FP-9.txt", "ctx nine\n")
	setState(p, "fp-list.txt", "FP-1 [done] [low] Build the thing renamed\nFP-2 [blocked] Child task\n")
	g, _ := step(t, p, "tracker", "sync", "push", "--json")
	mustContain(t, g.Stdout, `"pushed_updated": `)
	step(t, p, "tracker", "sync", "pull")
	compareLog(t, p)
	compareDB(t, p)
	step(t, p, "tracker", "status")
	step(t, p, "tracker", "map", "list")
}
