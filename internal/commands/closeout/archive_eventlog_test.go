package closeout_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/eventlog"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const (
	terminalSlug = "demo-01KZZZZZ"
	registryRel  = ".kittify/archive/archived-missions.jsonl"
)

// writeTerminalMission adds a committed mission whose only WP is done, so
// `archive create` reaches the registry append.
func writeTerminalMission(s *testkit.Sandbox) {
	d := "kitty-specs/" + terminalSlug + "/"
	s.WriteFile(d+"meta.json", "{\n  \"mission_id\": \"01KZZZZZZZZZZZZZZZZZZZZZZZ\",\n  \"mission_slug\": \""+terminalSlug+"\",\n  \"slug\": \""+terminalSlug+"\"\n}\n")
	s.WriteFile(d+"spec.md", "# Spec\n")
	s.WriteFile(d+"status.events.jsonl", `{"actor": "merge", "at": "2026-08-26T22:59:30.713440+00:00", "event_id": "01M104SKRSBY8M2JQCGZD72W62", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "approved", "mission_id": "01KZZZZZZZZZZZZZZZZZZZZZZZ", "mission_slug": "`+terminalSlug+`", "reason": "Merged", "review_ref": null, "to_lane": "done", "wp_id": "WP01"}`+"\n")
	s.Git("add", "-A")
	s.Git("commit", "-qm", "terminal mission")
}

var reArchivedAt = regexp.MustCompile(`"archived_at": "[^"]*"`)

// TestDiffArchiveRegistryAppend: the archive record is appended through the
// interim event-log lock as json.dumps(sort_keys=True) plus LF, creating
// .kittify/archive/; a torn registry refuses the append (nothing written)
// with the uncaught EventLogTornTailError traceback, exit 1.
func TestDiffArchiveRegistryAppend(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	p.Both(writeTerminalMission)
	create := []string{"archive", "create", terminalSlug, "--by", "me", "--reason", "done ünïcode", "--json"}
	g, o := p.Run(create...)
	if g.Code != 0 || o.Code != 0 {
		t.Fatalf("archive create: go=%d oracle=%d\n%s\n%s", g.Code, o.Code, g.Stderr, o.Stderr)
	}
	if gs, os_ := reArchivedAt.ReplaceAllString(g.Stdout, ""), reArchivedAt.ReplaceAllString(o.Stdout, ""); gs != os_ {
		t.Errorf("archive create stdout differs\n%s", testkit.Diff(os_, gs))
	}
	gr, or := p.Go.ReadFile(registryRel), p.Oracle.ReadFile(registryRel)
	if gm, om := reArchivedAt.ReplaceAllString(gr, ""), reArchivedAt.ReplaceAllString(or, ""); gm != om {
		t.Errorf("registry bytes differ\n oracle: %q\n go:     %q", or, gr)
	}

	p.Both(func(s *testkit.Sandbox) { s.WriteFile(registryRel, s.ReadFile(registryRel)+`{"torn": `) })
	for _, args := range [][]string{
		{"archive", "create", terminalSlug, "--by", "me", "--reason", "again", "--json"},
		{"archive", "create", terminalSlug, "--by", "me", "--reason", "again"},
	} {
		before := map[*testkit.Sandbox]string{p.Go: p.Go.ReadFile(registryRel), p.Oracle: p.Oracle.ReadFile(registryRel)}
		g, o := p.Run(args...)
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d", args, g.Code, o.Code)
		}
		if g.Stdout != o.Stdout {
			t.Errorf("%v: stdout differs\n%s", args, testkit.Diff(o.Stdout, g.Stdout))
		}
		if !testkit.IsRichTraceback(o.Stderr) || !strings.Contains(o.Stderr, "EVENT_LOG_TORN_TAIL") {
			t.Fatalf("%v: the oracle no longer crashes on a torn registry:\n%s", args, o.Stderr)
		}
		// Frames name site-packages files the Go binary cannot have; the
		// wrapped final line depends on the sandbox path length.
		ge := strings.Join(strings.Fields(testkit.MaskTracebackFrames(testkit.StripANSI(p.Go.Normalize(g.Stderr)))), " ")
		oe := strings.Join(strings.Fields(testkit.MaskTracebackFrames(testkit.StripANSI(p.Oracle.Normalize(o.Stderr)))), " ")
		if ge != oe {
			t.Errorf("%v: stderr differs\n oracle: %s\n go:     %s", args, oe, ge)
		}
		for s, b := range before {
			if got := s.ReadFile(registryRel); got != b {
				t.Errorf("%v: a torn registry was appended to: %q", args, got)
			}
		}
	}
}

// TestArchiveCreateTakesEventLogLock: concurrent `archive create` processes
// all land whole records, and a create waits while another writer holds the
// registry's event-log lock.
func TestArchiveCreateTakesEventLogLock(t *testing.T) {
	t.Parallel()
	s := missionfx.NewProject(t)
	writeTerminalMission(s)
	registry := filepath.Join(s.Dir, registryRel)
	if err := os.MkdirAll(filepath.Dir(registry), 0o755); err != nil {
		t.Fatal(err)
	}

	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- eventlog.WithLock(registry, 30*time.Second, func() error { close(held); <-release; return nil })
	}()
	<-held
	c := exec.Command(testkit.Binary(t), "archive", "create", terminalSlug, "--by", "me", "--reason", "waiting")
	c.Dir, c.Env = s.Dir, s.Environ()
	var out strings.Builder
	c.Stdout, c.Stderr = &out, &out
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(registry); err == nil {
		t.Errorf("the registry was written while another writer held its lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(); err != nil {
		t.Fatalf("archive create: %v\n%s", err, out.String())
	}

	const writers = 16
	cmds := make([]*exec.Cmd, writers)
	outs := make([]*strings.Builder, writers)
	for i := range cmds {
		c := exec.Command(testkit.Binary(t), "archive", "create", terminalSlug, "--by", "me", "--reason", fmt.Sprintf("writer-%d", i))
		c.Dir, c.Env = s.Dir, s.Environ()
		outs[i] = &strings.Builder{}
		c.Stdout, c.Stderr = outs[i], outs[i]
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = c
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Errorf("writer %d: %v\n%s", i, err, outs[i].String())
		}
	}
	b, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		v, err := pyjson.Loads(line)
		if err != nil {
			t.Fatalf("torn or interleaved line %q: %v", line, err)
		}
		seen[v.(*jsonx.Object).GetString("reason")]++
	}
	if seen["waiting"] != 1 {
		t.Errorf("the waiting writer's record appears %d times", seen["waiting"])
	}
	for i := 0; i < writers; i++ {
		if n := seen[fmt.Sprintf("writer-%d", i)]; n != 1 {
			t.Errorf("record of writer %d appears %d times", i, n)
		}
	}
}
