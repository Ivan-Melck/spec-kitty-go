package planning_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/issuematrix"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestConcurrentIssueVerdictsPreserveSiblingRows(t *testing.T) {
	s := missionfx.NewProject(t)
	s.Git("checkout", "-qb", "feat/issues")
	const slug = "issues-01M54Y16"
	dir := filepath.Join("kitty-specs", slug)
	s.WriteFile(filepath.Join(dir, "meta.json"), `{"mission_id":"01M54Y16550EHN17T75926T0NP","mission_slug":"issues-01M54Y16","mid8":"01M54Y16","mission_type":"software-dev","topology":"single_branch","target_branch":"feat/issues","pr_bound":false}`)
	rows := &issuematrix.Rows{}
	for i := 1; i <= 6; i++ {
		rows.Set(fmt.Sprintf("#%d", i), issuematrix.Entry{Verdict: "unknown"})
	}
	s.WriteFile(filepath.Join(dir, issuematrix.JSONFile), string(rows.Render()))
	s.Git("add", "--", dir)
	s.Git("commit", "-qm", "seed issues")
	bin := testkit.Binary(t)
	start := make(chan struct{})
	results := make(chan testkit.Result, 6)
	var wg sync.WaitGroup
	for i := 1; i <= 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results <- s.Exec("", bin, "agent", "issue-verdict", "--mission", slug, "--issue", fmt.Sprint(i), "--verdict", "fixed", "--actor", "tester", "--json")
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	for r := range results {
		if r.Code != 0 {
			t.Errorf("verdict: %d %s %s", r.Code, r.Stdout, r.Stderr)
		}
	}
	for ref, entry := range issuematrix.LoadRows(filepath.Join(s.Dir, dir)).By {
		if entry.Verdict != "fixed" {
			t.Errorf("concurrent update lost for %s: %s", ref, entry.Verdict)
		}
	}
}

func TestDiffIssueVerdictRemoteOnlyRC8(t *testing.T) {
	p := testkit.NewPair(t)
	const slug = "issues-01M54Y16"
	p.Both(func(s *testkit.Sandbox) {
		s.Git("checkout", "-qb", "feat/issues")
		s.WriteFile(".kittify/config.yaml", missionfx.Read(t, "config.yaml"))
		s.WriteFile(".kittify/metadata.yaml", missionfx.Read(t, "metadata.yaml"))
		s.WriteFile(".kittify/migration-ledger.jsonl", "")
		s.WriteFile(filepath.Join("kitty-specs", slug, "meta.json"), `{"mission_id":"01M54Y16550EHN17T75926T0NP","mission_slug":"issues-01M54Y16","mid8":"01M54Y16","mission_type":"software-dev","topology":"coord","coordination_branch":"kitty/mission-issues-01M54Y16","target_branch":"feat/issues","pr_bound":true}`)
		s.Git("add", "--", ".kittify", "kitty-specs")
		s.Git("commit", "-qm", "seed remote-only coordination")
		s.Git("update-ref", "refs/remotes/origin/kitty/mission-issues-01M54Y16", "HEAD")
	})
	g, _ := p.Compare(true, "agent", "issue-verdict", "--mission", slug, "--issue", "1", "--verdict", "fixed", "--actor", "tester", "--json")
	if g.Code != 1 {
		t.Fatalf("remote-only write must refuse: %d %s %s", g.Code, g.Stdout, g.Stderr)
	}
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		if s.Exists(filepath.Join("kitty-specs", slug, issuematrix.JSONFile)) {
			t.Error("refused write created an issue matrix")
		}
	}
}
