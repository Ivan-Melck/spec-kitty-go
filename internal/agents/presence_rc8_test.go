package agents

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestPresencePreservesTrackedSettingsAndRefreshesLegacy(t *testing.T) {
	f, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	python := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	for _, scenario := range []string{"legacy-same-version", "legacy-old-version", "tracked-settings", "edited-orientation"} {
		t.Run(scenario, func(t *testing.T) {
			p := testkit.NewPair(t)
			c := Presence{Version: "4.0.0rc8", ProjectSlug: "fixture", Health: "healthy"}
			p.Both(func(s *testkit.Sandbox) {
				s.Git("init", "-q", "-b", "main")
				s.WriteFile("README", "fixture\n")
				if strings.HasPrefix(scenario, "legacy") || scenario == "edited-orientation" {
					old := c
					if scenario == "legacy-old-version" {
						old.Version = "3.2.6"
					}
					block := strings.Replace(old.Render(), SpecifyInvocation, "`/spec-kitty.specify`", 1)
					if scenario == "edited-orientation" {
						block = strings.Replace(block, "Two usage patterns:", "Operator's usage patterns:", 1)
					}
					s.WriteFile(".claude/CLAUDE.md", "Team rules\n\n"+block)
				}
				s.WriteFile(".claude/settings.json", "{\"teamSetting\": true}\n")
				s.Git("add", ".")
				s.Git("commit", "-qm", "team config")
			})
			if err := WriterFor("claude").WriteOrientation(p.Go.Dir, c); err != nil {
				t.Fatal(err)
			}
			script := "from pathlib import Path\nfrom specify_cli.session_presence.content import SessionPresenceContent\nfrom specify_cli.session_presence.writers.claude_code import ClaudeCodeWriter,committed_settings_notice\nr=Path.cwd()\nc=SessionPresenceContent(version='4.0.0rc8',project_slug='fixture',health='healthy',available_version=None)\nClaudeCodeWriter().write(r,c)\nprint(committed_settings_notice(r) or '')"
			oracle := p.Oracle.Exec("", python, "-c", script)
			if oracle.Code != 0 {
				t.Fatal(oracle.Stderr)
			}
			if notice := CommittedSettingsNotice(p.Go.Dir) + "\n"; notice != oracle.Stdout {
				t.Error(testkit.Diff(oracle.Stdout, notice))
			}
			p.CompareTree(".claude", false)
		})
	}
}
