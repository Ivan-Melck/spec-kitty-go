package setup_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// Differential tests for the tool-surface assessment engine
// (internal/toolsurface): doctor tool-surfaces --fix repair blocks,
// upgrade --plan-json effects/dispositions, pack skills, drift policy and
// comment-preserving config.yaml writers.

var (
	reHex64 = regexp.MustCompile(`[0-9a-f]{64}`)
	reHex16 = regexp.MustCompile(`state-v1-[0-9a-f]{16}`)
)

// maskHashes hides content digests that embed run-specific bytes (manifest
// timestamps, project ids) and so differ between any two runs.
func maskHashes(s string) string {
	s = reHex64.ReplaceAllString(s, "<H>")
	return reHex16.ReplaceAllString(s, "state-v1-<H>")
}

func compareHashed(t *testing.T, p *testkit.Pair, args ...string) (testkit.Result, testkit.Result) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%s: exit go=%d oracle=%d\n%s\n%s", strings.Join(args, " "), g.Code, o.Code, g.Stderr, o.Stderr)
	}
	gs := maskHashes(maskCompat(p.Go.Normalize(g.Stdout)))
	os_ := maskHashes(maskCompat(p.Oracle.Normalize(o.Stdout)))
	if gs != os_ {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
	}
	return g, o
}

// drift damages managed surfaces of every owner family.
func drift(s *testkit.Sandbox) {
	rm := func(rel string) { os.RemoveAll(filepath.Join(s.Dir, rel)) }
	edit := func(rel, text string) {
		p := filepath.Join(s.Dir, rel)
		if _, err := os.Stat(p); err != nil {
			return
		}
		os.Chmod(p, 0o644)
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString(text)
		f.Close()
	}
	rm(".agents/skills/spec-kitty.plan/SKILL.md")
	rm(".agents/skills/spk-start-here/SKILL.md")
	rm(".claude/skills/spk-start-here/SKILL.md")
	edit(".agents/skills/spk-run-next/SKILL.md", "local edit\n")
	edit(".claude/agents/analyst-annie.md", "edited\n")
	rm(".claude/agents/architect-alphonso.md")
	if _, err := os.Stat(filepath.Join(s.Dir, ".claude/CLAUDE.md")); err == nil {
		os.WriteFile(filepath.Join(s.Dir, ".claude/CLAUDE.md"), nil, 0o644)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ".vibe/config.toml")); err == nil {
		os.WriteFile(filepath.Join(s.Dir, ".vibe/config.toml"), []byte("# vibe\nmodel = \"x\"\n"), 0o644)
	}
	os.Remove(filepath.Join(s.Home, ".claude/commands/spec-kitty.plan.md"))
}

func commitAll(s *testkit.Sandbox, msg string) {
	s.Git("add", "-A")
	s.Git("commit", "-qm", msg, "--allow-empty")
}

// MAINT-12/MAINT-13: --fix repair blocks (effect ids, plugin-bundle
// staging) across agent mixes and drift states.
func TestToolSurfaceFixMatchesOracle(t *testing.T) {
	for _, sel := range []string{"claude", "claude,codex,vibe", "copilot,gemini,cursor"} {
		t.Run(sel, func(t *testing.T) {
			p := testkit.NewPair(t)
			compareInit(p, "init", ".", "--ai", sel, "--non-interactive")
			compareHashed(t, p, "doctor", "tool-surfaces", "--fix", "--json")
			p.CompareTree("dist", true)
			p.Both(drift)
			compareHashed(t, p, "doctor", "tool-surfaces", "--json")
			compareHashed(t, p, "doctor", "tool-surfaces", "--fix", "--json")
			p.Compare(false, "doctor", "tool-surfaces")
			p.CompareTree(".agents", true)
			p.CompareTree(".claude", true)
			p.CompareTree(".vibe", true)
			p.CompareTree("dist", true)
		})
	}
}

// MAINT-03: upgrade --plan-json carries the owner effects, dispositions and
// diagnostics; the real run applies the prepared batches.
func TestUpgradePlanEffectsMatchOracle(t *testing.T) {
	for _, sel := range []string{"claude,codex", "claude,vibe,copilot"} {
		t.Run(sel, func(t *testing.T) {
			p := testkit.NewPair(t)
			compareInit(p, "init", ".", "--ai", sel, "--non-interactive")
			p.Both(func(s *testkit.Sandbox) { commitAll(s, "init") })
			compareHashed(t, p, "upgrade", "--plan-json")
			p.Both(func(s *testkit.Sandbox) { drift(s); commitAll(s, "drift") })
			compareHashed(t, p, "upgrade", "--plan-json")
			p.Compare(false, "upgrade", "--dry-run")
			compareHashed(t, p, "upgrade", "--dry-run", "--json")
			compareHashed(t, p, "upgrade", "--json")
			if g, o := lastCommit(p.Go), lastCommit(p.Oracle); g != o {
				t.Errorf("upgrade commit go=%q oracle=%q", g, o)
			}
			p.CompareTree(".agents", true)
			p.CompareTree(".claude", true)
			compareHashed(t, p, "upgrade", "--plan-json")
		})
	}
}

// writeOrgPack writes an org charter pack with two skills next to the
// project and points config.yaml at it.
func writeOrgPack(s *testkit.Sandbox) {
	pack := filepath.Join(s.Base, "orgpack")
	os.MkdirAll(filepath.Join(pack, "skills"), 0o755)
	os.WriteFile(filepath.Join(pack, "org-charter.yaml"), []byte("skill_namespace: acme\nrequired_skills:\n  - review-helper\n  - run-status\n"), 0o644)
	os.WriteFile(filepath.Join(pack, "skills", "review-helper.skill.yaml"), []byte(`schema_version: "1.0"
id: review-helper
title: Review Helper
description: Helps   review
  code carefully.
triggers: [review this, check pr]
form: prompt
body_path: review-helper.md
parameters:
  - name: depth
    required: false
    description: How deep
    default: shallow
`), 0o644)
	os.WriteFile(filepath.Join(pack, "skills", "review-helper.md"), []byte("Look at the diff.\r\nThen comment.\n"), 0o644)
	os.WriteFile(filepath.Join(pack, "skills", "run-status.skill.yaml"), []byte(`schema_version: "1.0"
id: run-status
title: Run Status
description: Show status
form: wrapper
expands_to:
  target: "builtin:spec-kitty.status"
  args: "--json"
invocation:
  side_effects: [none]
`), 0o644)
	cfg := filepath.Join(s.Dir, ".kittify", "config.yaml")
	os.Chmod(cfg, 0o644)
	b, _ := os.ReadFile(cfg)
	os.WriteFile(cfg, append(b, []byte("charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: ../orgpack\n")...), 0o644)
}

// MAINT-11: doctor skills reports and projects charter-pack skills.
func TestPackSkillsMatchOracle(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", ".", "--ai", "claude,codex", "--non-interactive")
	p.Both(writeOrgPack)
	compareHashed(t, p, "doctor", "skills", "--json")
	p.Compare(true, "doctor", "skills")
	compareHashed(t, p, "doctor", "skills", "--fix", "--json")
	p.CompareTree(".claude/skills/acme-review-helper", true)
	p.CompareTree(".agents/skills", false)
	compareHashed(t, p, "doctor", "skills", "--json")
	p.Both(func(s *testkit.Sandbox) {
		f := filepath.Join(s.Dir, ".claude/skills/acme-review-helper/SKILL.md")
		os.Chmod(f, 0o644)
		b, _ := os.ReadFile(f)
		os.WriteFile(f, append(b, "local\n"...), 0o644)
		os.WriteFile(filepath.Join(s.Base, "orgpack", "skills", "review-helper.md"), []byte("Look at the diff carefully.\n"), 0o644)
		os.WriteFile(filepath.Join(s.Base, "orgpack", "org-charter.yaml"), []byte("skill_namespace: acme\nrequired_skills:\n  - review-helper\n"), 0o644)
	})
	compareHashed(t, p, "doctor", "skills", "--json")
	p.Compare(true, "doctor", "skills")
	p.Both(func(s *testkit.Sandbox) { commitAll(s, "packs") })
	compareHashed(t, p, "upgrade", "--plan-json")
}

// config.yaml writers keep comments (ruamel round trip): provisioning
// re-seeds mission_type_activations into a commented file.
func TestCommentedConfigMatchesOracle(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", ".", "--ai", "claude", "--non-interactive")
	p.Both(func(s *testkit.Sandbox) {
		cfg := s.ReadFile(".kittify/config.yaml")
		cfg = regexp.MustCompile(`(?s)mission_type_activations:\n(- [^\n]*\n)+`).ReplaceAllString(cfg, "")
		cfg = strings.Replace(cfg, "agents:", "# agents below\nagents:", 1)
		cfg = regexp.MustCompile(`(?m)^vcs:$`).ReplaceAllString(cfg, "vcs:  # vcs settings")
		os.Chmod(filepath.Join(s.Dir, ".kittify/config.yaml"), 0o644)
		s.WriteFile(".kittify/config.yaml", "# Team config — keep this comment\n\n"+cfg)
		commitAll(s, "comments")
	})
	compareHashed(t, p, "upgrade", "--plan-json")
	p.Compare(false, "upgrade")
	compareFiles(t, p, ".kittify/config.yaml")
	if g := p.Go.ReadFile(".kittify/config.yaml"); !strings.Contains(g, "# Team config — keep this comment") || !strings.Contains(g, "# agents below") {
		t.Errorf("comments dropped:\n%s", g)
	}
}

// charter activate/deactivate of a skill re-projects the pack skills in
// force (activate.reproject_pack_skills): newly in-force skills are
// written, no-longer-in-force manifest-owned pack skills retired, locally
// edited ones preserved.
func TestCharterSkillReprojectionMatchesOracle(t *testing.T) {
	p := testkit.NewPair(t)
	compareInit(p, "init", ".", "--ai", "claude,codex", "--non-interactive")
	p.Both(writeOrgPack)
	p.Both(func(s *testkit.Sandbox) {
		os.WriteFile(filepath.Join(s.Base, "orgpack", "org-charter.yaml"), []byte("skill_namespace: acme\nrequired_skills:\n  - review-helper\n"), 0o644)
		commitAll(s, "pack")
	})
	compareHashed(t, p, "doctor", "skills", "--fix", "--json")
	p.Compare(true, "charter", "activate", "skill", "run-status")
	p.CompareTree(".claude/skills", true)
	p.CompareTree(".agents/skills", false)
	compareFiles(t, p, ".kittify/config.yaml")
	p.Both(func(s *testkit.Sandbox) {
		f := filepath.Join(s.Dir, ".claude/skills/acme-run-status/SKILL.md")
		os.Chmod(f, 0o644)
		b, _ := os.ReadFile(f)
		os.WriteFile(f, append(b, "local\n"...), 0o644)
	})
	p.Compare(true, "charter", "deactivate", "skill", "run-status")
	p.Compare(true, "charter", "deactivate", "skill", "review-helper")
	p.CompareTree(".claude/skills", true)
	p.CompareTree(".agents/skills", false)
	compareHashed(t, p, "doctor", "skills", "--json")
}
