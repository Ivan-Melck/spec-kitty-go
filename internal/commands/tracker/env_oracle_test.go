package tracker_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// bindNoActor binds beads to the fake bd without an actor credential, so
// the connector falls back to getpass.getuser().
func bindNoActor(t *testing.T, p *testkit.Pair) {
	t.Helper()
	p.Both(func(s *testkit.Sandbox) {
		cmd := filepath.Join(s.Dir, "fake", "bd")
		args := []string{"tracker", "bind", "--provider", "beads", "--workspace", "ws", "--credential", "command=" + cmd}
		var r testkit.Result
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

// TestOracleSyncActorLookup: without an actor credential the bd calls carry
// getpass.getuser(): LOGNAME, then USER, LNAME, USERNAME, then the passwd
// entry.
func TestOracleSyncActorLookup(t *testing.T) {
	t.Parallel()
	p := newTrackerPair(t)
	setupFake(t, p, map[string]string{"bd": fakeBD})
	setState(p, "list.json", bdList1)
	bindNoActor(t, p)
	base := map[*testkit.Sandbox][]string{p.Go: p.Go.Env, p.Oracle: p.Oracle.Env}
	for _, env := range [][]string{
		{"LOGNAME=logname-user", "USER=user-user", "LNAME=lname-user", "USERNAME=username-user"},
		{"USER=user-user", "LNAME=lname-user", "USERNAME=username-user"},
		{"LOGNAME=", "LNAME=lname-user", "USERNAME=username-user"},
		{"USERNAME=username-user"},
		nil,
	} {
		env := env
		p.Both(func(s *testkit.Sandbox) {
			s.Env = append(append([]string(nil), base[s]...), env...)
			os.Remove(filepath.Join(s.Dir, "fake", "state", "log"))
		})
		step(t, p, "tracker", "sync", "run", "--json")
		compareLog(t, p)
	}
	p.Both(func(s *testkit.Sandbox) { s.Env = base[s] })
}

// TestOracleSyncTimeout: a bd call running past the 30 s subprocess
// timeout is a ConnectorRequestError ("Command timed out: ...").
func TestOracleSyncTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the 30 s subprocess timeout")
	}
	t.Parallel()
	p := newTrackerPair(t)
	slow := strings.Replace(fakeBD, "case \"$2\" in", "[ -f \"$D/slow\" ] && sleep 32\ncase \"$2\" in", 1)
	setupFake(t, p, map[string]string{"bd": slow})
	setState(p, "list.json", bdList1)
	bindFake(t, p, "beads", "external_authoritative")
	setState(p, "slow", "1")
	step(t, p, "tracker", "sync", "pull", "--json")
	step(t, p, "tracker", "sync", "push")
	compareLog(t, p)
}
