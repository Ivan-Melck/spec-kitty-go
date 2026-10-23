package doctrine_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestParityGlossaryResolutionLedgerRC8(t *testing.T) {
	p := newOraclePair(t)
	run := func(s *testkit.Sandbox, binary, input string) testkit.Result {
		cmd := exec.Command(binary, "glossary", "resolve", "11111111-2222-3333-4444-555555555555")
		cmd.Dir = s.Dir
		cmd.Env = s.Environ()
		cmd.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if status, ok := err.(*exec.ExitError); ok {
				code = status.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		return testkit.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
	}
	for _, scenario := range []string{"valid-candidate", "custom", "invalid-candidate", "torn-valid", "torn-invalid"} {
		t.Run(scenario, func(t *testing.T) {
			p.Both(func(s *testkit.Sandbox) {
				os.RemoveAll(filepath.Join(s.Dir, ".kittify/events/glossary"))
				seedGlossary(t, s)
				log := s.ReadFile(".kittify/events/glossary/m1.events.jsonl")
				if strings.Contains(scenario, "invalid") {
					log = strings.Replace(log, `"scope": "team_domain"`, `"scope": "unregistered"`, 1)
				}
				if strings.HasPrefix(scenario, "torn") {
					log = strings.TrimSuffix(log, "\n")
				}
				s.WriteFile(".kittify/events/glossary/m1.events.jsonl", log)
			})
			input := "1\n"
			if scenario == "custom" {
				input = "C\nCustom workspace définition\n"
			}
			g, o := run(p.Go, testkit.Binary(t), input), run(p.Oracle, testkit.Oracle(t), input)
			if g.Code != o.Code {
				t.Errorf("exit go=%d oracle=%d\n%s%s\n%s%s", g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			}
			if a, b := p.Go.Normalize(g.Stdout), p.Oracle.Normalize(o.Stdout); a != b {
				t.Errorf("stdout differs\n%s", testkit.Diff(b, a))
			}
			if a, b := p.Go.Normalize(g.Stderr), p.Oracle.Normalize(o.Stderr); a != b {
				t.Errorf("stderr differs\n%s", testkit.Diff(b, a))
			}
			p.CompareTree(".kittify/events/glossary", true)
		})
	}
}
