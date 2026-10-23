package closeout_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// corruptLog damages the mission's event log (or its status.json) on the
// primary checkout or in the coordination worktree.
func corruptLog(t *testing.T, s *testkit.Sandbox, coordSide bool, kind string) {
	t.Helper()
	ev := filepath.Join(s.Dir, "kitty-specs", slugOf(t, s), "status.events.jsonl")
	if coordSide {
		m, _ := filepath.Glob(filepath.Join(s.Dir, ".worktrees", "*coord*", "kitty-specs", "*", "status.events.jsonl"))
		if len(m) == 0 {
			t.Fatal("no coordination event log")
		}
		ev = m[0]
	}
	b, _ := os.ReadFile(ev)
	switch kind {
	case "badjson":
		os.WriteFile(ev, append(b, []byte("{bad\n")...), 0o644)
	case "nonobj":
		os.WriteFile(ev, append(b, []byte("[1]\n")...), 0o644)
	case "eperm":
		os.WriteFile(ev, b, 0o644)
		os.Chmod(ev, 0)
	case "dir":
		os.Remove(ev)
		os.Mkdir(ev, 0o755)
	case "badutf8":
		os.WriteFile(ev, append(b, []byte("\xff\n")...), 0o644)
	case "drift", "driftdirty":
		st := filepath.Join(filepath.Dir(ev), "status.json")
		sb, _ := os.ReadFile(st)
		os.WriteFile(st, []byte(strings.Replace(string(sb), `"event_count": `, `"event_count": 1`, 1)), 0o644)
		if coordSide && kind == "drift" {
			s.Exec(filepath.Dir(filepath.Dir(filepath.Dir(ev))), "git", "commit", "-q", "-am", "drift")
		}
	}
}

func unlockLogs(s *testkit.Sandbox) {
	filepath.Walk(s.Dir, func(path string, info os.FileInfo, err error) error {
		if info != nil && info.Name() == "status.events.jsonl" {
			os.Chmod(path, 0o644)
		}
		return nil
	})
}

// TestDiffConsolidateUnreadableStatus covers an unreadable or corrupt event
// log: the CPython exception text (StoreError with its chained
// JSONDecodeError, UnicodeDecodeError, IsADirectoryError, PermissionError)
// in the dependency gate (primary log, read through read_wp_frontmatter),
// in `agent tasks status` (status_failed), and as the uncaught traceback of
// the dry-run review-artifact preview and of the real run's
// materialization-drift check (coordination log); plus the drift warning.
func TestDiffConsolidateUnreadableStatus(t *testing.T) {
	for _, sc := range []string{"coord-badjson", "coord-nonobj", "coord-dir", "coord-badutf8", "coord-eperm", "coord-drift", "coord-driftdirty",
		"primary-badjson", "primary-badutf8", "primary-dir", "primary-eperm"} {
		sc := sc
		t.Run(sc, func(t *testing.T) {
			t.Parallel()
			p := acceptedPair(t)
			side, kind, _ := strings.Cut(sc, "-")
			p.Both(func(s *testkit.Sandbox) { corruptLog(t, s, side == "coord", kind) })
			defer p.Both(unlockLogs)
			comparePairTB(t, p, "agent", "tasks", "status", "--mission", "$M", "--json")
			comparePairTB(t, p, "consolidate", "--mission", "$M", "--dry-run")
			comparePairTB(t, p, "consolidate", "--mission", "$M", "--dry-run", "--json")
			comparePairTB(t, p, "consolidate", "--mission", "$M", "--yes")
		})
	}
}

// TestDiffConsolidateReviewArtifactGate covers the review-artifact
// consistency gate: a terminal WP whose event-sourced verdict is
// changes_requested blocks the dry-run forecast (human and JSON), the
// mission review and the real merge; --skip-review-artifact-check records
// committed override evidence and merges.
func TestDiffConsolidateReviewArtifactGate(t *testing.T) {
	t.Parallel()
	p := acceptedPair(t)
	p.Both(func(s *testkit.Sandbox) { forgeRejectedVerdict(t, s, "WP02") })
	comparePairTB(t, p, "consolidate", "--mission", "$M", "--dry-run")
	comparePairTB(t, p, "consolidate", "--mission", "$M", "--dry-run", "--json")
	comparePairTB(t, p, "review", "--mission", "$M")
	report := func(s *testkit.Sandbox) string {
		return s.Normalize(s.ReadFile(filepath.Join("kitty-specs", slugOf(t, s), "mission-review-report.md")))
	}
	if g, o := report(p.Go), report(p.Oracle); g != o {
		t.Errorf("mission-review-report.md differs\n%s", testkit.Diff(o, g))
	}
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "kitty-specs", slugOf(t, s), "mission-review-report.md"))
	})
	comparePairTB(t, p, "consolidate", "--mission", "$M", "--yes")
	compareGit(t, p)
	comparePairTB(t, p, "consolidate", "--mission", "$M", "--yes", "--skip-review-artifact-check", "--note", "accepted risk")
	compareGit(t, p)
}

// forgeRejectedVerdict rewrites the coordination log's approval of wpID to
// carry a changes_requested review_result (a terminal WP with a rejected
// event-sourced verdict) and commits it on the coordination branch.
func forgeRejectedVerdict(t *testing.T, s *testkit.Sandbox, wpID string) {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(s.Dir, ".worktrees", "*coord*", "kitty-specs", "*", "status.events.jsonl"))
	if len(m) == 0 {
		t.Fatal("no coordination event log")
	}
	b, _ := os.ReadFile(m[0])
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if !strings.Contains(l, `"wp_id": "`+wpID+`"`) || !strings.Contains(l, `"to_lane": "approved"`) {
			continue
		}
		k := strings.Index(l, `"review_result": {`)
		if k < 0 {
			t.Fatalf("no review_result in %s", l)
		}
		lines[i] = l[:k] + strings.Replace(l[k:], `"verdict": "approved"`, `"verdict": "changes_requested"`, 1)
		break
	}
	os.WriteFile(m[0], []byte(strings.Join(lines, "\n")), 0o644)
	wt := filepath.Dir(filepath.Dir(filepath.Dir(m[0])))
	if r := s.Exec(wt, "git", "commit", "-q", "-am", "forge verdict"); r.Code != 0 {
		t.Fatalf("commit: %s", r.Stderr)
	}
}

// comparePairTB is comparePair with traceback frames masked on stderr
// (upstream's frames show site-packages paths and source excerpts).
func comparePairTB(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g := p.Go.Run(subst(args, slugOf(t, p.Go))...)
	o := p.Oracle.RunOracle(subst(args, slugOf(t, p.Oracle))...)
	name := strings.Join(args, " ")
	if os.Getenv("KITTY_TEST_TRACE") != "" {
		t.Logf("%s\n== go %d ==\n%s%s== oracle %d ==\n%s%s", name, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
	}
	g.Stdout, o.Stdout = reCommit.ReplaceAllString(g.Stdout, "Commit: <C>"), reCommit.ReplaceAllString(o.Stdout, "Commit: <C>")
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d", name, g.Code, o.Code)
	}
	if gs, ors := digitSHA(p.Go.Normalize(g.Stdout)), digitSHA(p.Oracle.Normalize(o.Stdout)); gs != ors {
		t.Errorf("%s: stdout differs\n%s", name, testkit.Diff(ors, gs))
	}
	gs := testkit.MaskTracebackFrames(digitSHA(p.Go.Normalize(g.Stderr)))
	ors := testkit.MaskTracebackFrames(digitSHA(p.Oracle.Normalize(o.Stderr)))
	if gs != ors {
		t.Errorf("%s: stderr differs\n%s", name, testkit.Diff(ors, gs))
	}
}
