package setup_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// Legacy-project corpora: an oracle-initialised project rewound to an older
// recorded version and decorated with the on-disk state of that era, then
// upgraded by both CLIs. The upgrade output (text and JSON), the resulting
// file tree and the auto-commit must match.

type corpus struct {
	name    string
	ai      string
	version string // metadata spec_kitty.version
	noStamp bool   // drop spec_kitty.schema_version (pre-3.0 layout)
	mutate  func(s *testkit.Sandbox)
}

var reDuration = regexp.MustCompile(`"duration_ms": \d+`)

func maskUpgrade(s string) string {
	return reDuration.ReplaceAllString(maskCompat(s), `"duration_ms": <D>`)
}

func setVersion(s *testkit.Sandbox, version string, noStamp bool) {
	p := filepath.Join(s.Dir, ".kittify", "metadata.yaml")
	os.Chmod(p, 0o644)
	md := s.ReadFile(".kittify/metadata.yaml")
	md = regexp.MustCompile(`(?m)^  version: .*$`).ReplaceAllString(md, "  version: "+version)
	if noStamp {
		md = regexp.MustCompile(`(?m)^  schema_version: \d+\n`).ReplaceAllString(md, "")
	}
	s.WriteFile(".kittify/metadata.yaml", md)
}

func runCorpus(t *testing.T, c corpus) {
	p := testkit.NewPair(t)
	ai := c.ai
	if ai == "" {
		ai = "claude,codex"
	}
	p.Both(func(s *testkit.Sandbox) {
		if r := s.RunOracle("init", ".", "--ai", ai, "--non-interactive"); r.Code != 0 {
			t.Fatalf("oracle init: %s%s", r.Stdout, r.Stderr)
		}
		s.Git("add", "-A")
		s.Git("commit", "-qm", "init")
		setVersion(s, c.version, c.noStamp)
		if c.mutate != nil {
			c.mutate(s)
		}
		s.Git("add", "-A")
		s.Git("commit", "-qm", "legacy layout", "--allow-empty")
	})
	cmp := func(args ...string) {
		t.Helper()
		g, o := p.Run(args...)
		if g.Code != o.Code {
			t.Errorf("%s: exit go=%d oracle=%d\n%s\n%s", strings.Join(args, " "), g.Code, o.Code, g.Stderr, o.Stderr)
		}
		gs, os_ := maskUpgrade(p.Go.Normalize(g.Stdout)), maskUpgrade(p.Oracle.Normalize(o.Stdout))
		if gs != os_ {
			t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os_, gs))
		}
	}
	cmp("upgrade", "--dry-run", "--verbose")
	cmp("upgrade", "--dry-run", "--json")
	cmp("upgrade", "--yes", "--json")
	if g, o := lastCommit(p.Go), lastCommit(p.Oracle); g != o {
		t.Errorf("upgrade commit go=%q oracle=%q", g, o)
	}
	compareTrees(t, p)
	// A second run is a no-op on both sides.
	cmp("upgrade", "--yes", "--json")
}

// compareTrees diffs the project trees (modes, file sets, normalized
// contents), masking the duration recorded in metadata notes.
func compareTrees(t *testing.T, p *testkit.Pair) {
	t.Helper()
	ge, oe := treeEntries(p.Go.Dir, []string{".git/"}), treeEntries(p.Oracle.Dir, []string{".git/"})
	if strings.Join(ge, "\n") != strings.Join(oe, "\n") {
		t.Errorf("tree differs (oracle vs go):\n%s", testkit.Diff(strings.Join(oe, "\n"), strings.Join(ge, "\n")))
		return
	}
	for _, e := range ge {
		f := strings.SplitN(e, " ", 3)
		if f[1] != "f" {
			continue
		}
		gb, _ := os.ReadFile(filepath.Join(p.Go.Dir, f[2]))
		ob, _ := os.ReadFile(filepath.Join(p.Oracle.Dir, f[2]))
		gn, on := maskTree(p.Go.Normalize(string(gb))), maskTree(p.Oracle.Normalize(string(ob)))
		if gn != on {
			t.Errorf("file %s differs:\n%s", f[2], testkit.Diff(on, gn))
		}
	}
}

var (
	reNotesDuration = regexp.MustCompile(`"duration_ms": \d+`)
	rePyEnv         = regexp.MustCompile(`(?m)^(  (python_version|platform_version|platform)): .*$`)
	reNodeID        = regexp.MustCompile(`"node_id": "[0-9a-f]{12}"`)
	reBuildID       = regexp.MustCompile(`"build_id": "[^"]*"`)
)

func maskTree(s string) string {
	s = reNotesDuration.ReplaceAllString(s, `"duration_ms": <D>`)
	s = rePyEnv.ReplaceAllString(s, "$1: <ENV>")
	return s
}

const legacyImplementTemplate = `---
description: Implement a work package
---
# Implement

## Work Package Selection

Run ` + "`.kittify/scripts/bash/check-prerequisites.sh --json`" + ` first.
Then move the prompt from tasks/planned/ to tasks/doing/.
Use spec-kitty agent workflow implement WP01
`

const legacyReviewTemplate = `---
description: Review a work package
---
# Review

## Location Pre-flight Check

Conduct the review: read tasks/for_review/ and run scripts/bash/tasks-move-to-lane.sh.
`

func writeLegacy09(s *testkit.Sandbox) {
	for _, n := range []string{"implement", "review", "specify", "plan", "tasks", "charter"} {
		body := "# " + n + "\n\nRun `.kittify/scripts/bash/create-new-feature.sh` from the main repository root.\n"
		switch n {
		case "implement":
			body = legacyImplementTemplate
		case "review":
			body = legacyReviewTemplate
		}
		s.WriteFile(".kittify/missions/software-dev/command-templates/"+n+".md", body)
	}
	s.WriteFile(".kittify/missions/software-dev/mission.yaml", "name: software-dev\n")
	s.WriteFile(".kittify/templates/command-templates/specify.md", "Use scripts/bash/create-new-feature.sh and tasks_cli.py move WP01\n")
	s.WriteFile(".kittify/scripts/bash/create-new-feature.sh", "#!/bin/bash\necho legacy\n")
	s.WriteFile(".kittify/scripts/bash/common.sh", "#!/bin/bash\n")
	s.WriteFile(".kittify/active-mission", "software-dev\n")
	s.WriteFile("memory/constitution.md", "# Project Constitution\n\nThe constitution governs.\n")
	s.WriteFile(".claude/commands/spec-kitty.implement.md", legacyImplementTemplate)
	s.WriteFile(".claude/commands/spec-kitty.review.md", legacyReviewTemplate)
	s.WriteFile(".claude/commands/spec-kitty.clarify.md", "<!-- spec-kitty-command-version: 0.9.0 -->\nclarify\n")
	s.WriteFile("kitty-specs/001-demo-feature/spec.md", "# Demo\n\n**Target Branch**: main\n")
	s.WriteFile("kitty-specs/001-demo-feature/meta.json", "{\n  \"feature_number\": \"001\",\n  \"slug\": \"001-demo-feature\",\n  \"friendly_name\": \"demo feature\",\n  \"mission\": \"software-dev\"\n}\n")
	s.WriteFile("kitty-specs/001-demo-feature/tasks/planned/WP01-setup.md", "---\nwork_package_id: WP01\ntitle: Setup\nlane: planned\nhistory: []\n---\n# WP01\n\nCreate src/app.py\n")
	s.WriteFile("kitty-specs/001-demo-feature/tasks/done/WP02-docs.md", "---\nwork_package_id: WP02\ntitle: Docs\n---\n# WP02\n\nWrite docs/index.md\n")
	s.WriteFile("kitty-specs/001-demo-feature/tasks/doing/.gitkeep", "")
	s.WriteFile("kitty-specs/001-demo-feature/tasks.md", "# Tasks\n\n**Prompt**: `tasks/planned/WP01-setup.md`\n")
	gi := s.ReadFile(".gitignore")
	s.WriteFile(".gitignore", "kitty-specs/\n"+gi)
}

func TestUpgradeLegacyLayoutCorporaMatchOracle(t *testing.T) {
	for _, c := range []corpus{
		{name: "v0.9-claude", ai: "claude", version: "0.9.0", noStamp: true, mutate: writeLegacy09},
		{name: "v0.9-codex", ai: "claude,codex", version: "0.9.0", noStamp: true, mutate: writeLegacy09},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			runCorpus(t, c)
		})
	}
}

func TestUpgradeFreshProjectCorporaMatchOracle(t *testing.T) {
	for _, v := range []string{"0.1.0", "2.0.0", "3.0.0", "3.2.0", "3.2.6", "4.0.0rc1"} {
		v := v
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			runCorpus(t, corpus{name: v, version: v})
		})
	}
}
