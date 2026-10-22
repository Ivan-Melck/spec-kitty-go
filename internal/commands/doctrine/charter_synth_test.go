package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var (
	reRunID     = regexp.MustCompile(`\b[0-9A-Z]{26}\b`)
	reSynthVol  = regexp.MustCompile(`(?m)^(generated_at|produced_at|created_at|synthesis_run_id|run_id|manifest_hash): .*$`)
	reYAMLParse = regexp.MustCompile(`YAML parse error in ([^:]+): [^)]*\)`)
	reBundleSHA = regexp.MustCompile(`sha256:[0-9a-f]{64}`)
	reFixRoot   = regexp.MustCompile(`expected at \S*/tests/charter/fixtures`)
)

const synthGenerated = `id: PROJECT_001
schema_version: "1.0"
title: Mission scope
intent: Deliver predictable changes.
enforcement: required
`

// synthFiles are the agent-authored artifacts the default interview targets.
var synthFiles = map[string]string{
	".kittify/charter/generated/directives/001-mission-type-scope-directive.directive.yaml": synthGenerated,
	".kittify/charter/generated/tactics/review-policy-tactic.tactic.yaml":                   "id: review-policy-tactic\nschema_version: \"1.0\"\nname: Review policy\nsteps:\n  - title: Get one focused reviewer\n",
	".kittify/charter/generated/tactics/testing-philosophy-tactic.tactic.yaml":              "id: testing-philosophy-tactic\nschema_version: \"1.0\"\nname: Testing philosophy\npurpose: Keep tests honest and fast, with a long sentence that goes on and on to exceed the eighty column width limit.\nsteps:\n  - title: Write the test first\n    description: Red, green, refactor.\n",
	".kittify/charter/generated/styleguides/quality-gates-style-guide.styleguide.yaml":      "id: quality-gates-style-guide\nschema_version: \"1.0\"\ntitle: Quality gates\nscope: code\nprinciples:\n  - Only declared gates.\n",
	".kittify/charter/generated/styleguides/testing-style-guide.styleguide.yaml":            "id: testing-style-guide\nschema_version: \"1.0\"\ntitle: Testing style\nscope: testing\nprinciples:\n  - Tests are documentation.\n",
}

// newSynthPair builds two oracle-initialised projects whose interview and
// charter.yaml are produced by the oracle (identical bundle hashes).
func newSynthPair(t *testing.T) *testkit.Pair {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		for _, args := range [][]string{{"charter", "interview", "--defaults", "--json"}, {"charter", "generate", "--json"}} {
			if r := s.Exec("", testkit.Oracle(t), args...); r.Code != 0 {
				t.Fatalf("oracle %v: %s %s", args, r.Stdout, r.Stderr)
			}
		}
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "charter")
	})
	return p
}

func synthMask(s string) string {
	s = reRunID.ReplaceAllString(s, "<RUN>")
	s = reSynthVol.ReplaceAllString(s, "$1: <V>")
	s = reBundleSHA.ReplaceAllString(s, "sha256:<BH>")
	s = reFixRoot.ReplaceAllString(s, "expected at <FIXROOT>/tests/charter/fixtures")
	return reYAMLParse.ReplaceAllString(s, "YAML parse error in $1: <E>)")
}

func compareSynth(t *testing.T, p *testkit.Pair, stderr bool, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%v: exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", args, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	if gs, os_ := synthMask(p.Go.Normalize(g.Stdout)), synthMask(p.Oracle.Normalize(o.Stdout)); gs != os_ {
		t.Errorf("%v: stdout differs\n%s", args, testkit.Diff(os_, gs))
	}
	if stderr {
		if ge, oe := synthMask(p.Go.Normalize(g.Stderr)), synthMask(p.Oracle.Normalize(o.Stderr)); ge != oe {
			t.Errorf("%v: stderr differs\n%s", args, testkit.Diff(oe, ge))
		}
	}
}

func compareSynthFile(t *testing.T, p *testkit.Pair, rel string) {
	t.Helper()
	if p.Go.Exists(rel) != p.Oracle.Exists(rel) {
		t.Errorf("%s: exists go=%v oracle=%v", rel, p.Go.Exists(rel), p.Oracle.Exists(rel))
		return
	}
	if !p.Go.Exists(rel) {
		return
	}
	g, o := synthMask(p.Go.ReadFile(rel)), synthMask(p.Oracle.ReadFile(rel))
	if g != o {
		t.Errorf("%s differs\n%s", rel, testkit.Diff(o, g))
	}
}

var synthOutputs = []string{
	".kittify/charter-packs/graph.yaml",
	".kittify/charter-packs/directive/001-mission-type-scope-directive.directive.yaml",
	".kittify/charter-packs/tactic/review-policy-tactic.tactic.yaml",
	".kittify/charter-packs/tactic/testing-philosophy-tactic.tactic.yaml",
	".kittify/charter-packs/styleguide/quality-gates-style-guide.styleguide.yaml",
	".kittify/charter-packs/styleguide/testing-style-guide.styleguide.yaml",
	".kittify/charter/provenance/directive-mission-type-scope-directive.yaml",
	".kittify/charter/provenance/tactic-review-policy-tactic.yaml",
	".kittify/charter/provenance/styleguide-testing-style-guide.yaml",
	".kittify/charter/synthesis-manifest.yaml",
}

func TestParityCharterSynthesizePipeline(t *testing.T) {
	t.Parallel()
	p := newSynthPair(t)
	// Missing generated artifacts (one written, the rest absent).
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter/generated/directives/001-mission-type-scope-directive.directive.yaml", synthGenerated)
	})
	compareSynth(t, p, false, "charter", "synthesize", "--json")
	compareSynth(t, p, false, "charter", "synthesize")
	compareSynth(t, p, false, "charter", "synthesize", "--adapter", "fixture", "--json", "--skip-code-evidence", "--skip-corpus")
	p.Both(func(s *testkit.Sandbox) {
		for rel, body := range synthFiles {
			s.WriteFile(rel, body)
		}
	})
	compareSynth(t, p, true, "charter", "synthesize", "--dry-run", "--json")
	compareSynth(t, p, true, "charter", "synthesize", "--dry-run")
	compareSynth(t, p, true, "charter", "synthesize", "--json")
	for _, rel := range synthOutputs {
		compareSynthFile(t, p, rel)
	}
	compareSynth(t, p, true, "charter", "synthesize")
	p.Both(func(s *testkit.Sandbox) {
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "syn")
	})
	compareSynth(t, p, true, "charter", "resynthesize", "--list-topics", "--json")
	compareSynth(t, p, true, "charter", "resynthesize", "--list-topics")
	compareSynth(t, p, true, "charter", "resynthesize", "--topic", "tactic:review-policy-tactic", "--json")
	compareSynth(t, p, true, "charter", "resynthesize", "--topic", "testing-philosophy")
	compareSynth(t, p, true, "charter", "resynthesize", "--topic", "directive:DIRECTIVE_001")
	compareSynth(t, p, true, "charter", "resynthesize", "--topic", "nonsense", "--json")
	compareSynth(t, p, true, "charter", "resynthesize")

	// Schema gate, identity mismatch, non-mapping body.
	tac := ".kittify/charter/generated/tactics/review-policy-tactic.tactic.yaml"
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(tac, "id: review-policy-tactic\nschema_version: 1.0\nname: Review policy\nsteps: []\nextra: 1\n")
	})
	compareSynth(t, p, true, "charter", "synthesize", "--json")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(tac, "id: wrong-id\n") })
	compareSynth(t, p, false, "charter", "synthesize")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(tac, "- a\n") })
	compareSynth(t, p, false, "charter", "synthesize", "--json")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(tac, synthFiles[tac]) })

	// Neutrality gate.
	sg := ".kittify/charter/generated/styleguides/testing-style-guide.styleguide.yaml"
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(sg, "id: testing-style-guide\nschema_version: \"1.0\"\ntitle: Testing style\nscope: testing\nprinciples:\n  - Run pytest and junit; pip install deps; python -m pytest\n")
	})
	compareSynth(t, p, false, "charter", "synthesize", "--json")
	compareSynth(t, p, false, "charter", "synthesize", "--dry-run")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(sg, synthFiles[sg])
		s.Exec("", "rm", "-rf", ".kittify/charter/.staging")
	})

	// Retained / orphaned overlay content, prune.
	p.Both(func(s *testkit.Sandbox) {
		g := s.ReadFile(".kittify/charter-packs/graph.yaml")
		g = strings.Replace(g, "edges: []", "- urn: tactic:old-thing\n  kind: tactic\n  label: Old\nedges:\n- source: tactic:old-thing\n  target: directive:PROJECT_001\n  relation: applies", 1)
		s.WriteFile(".kittify/charter-packs/graph.yaml", g)
	})
	compareSynth(t, p, true, "charter", "synthesize", "--dry-run", "--json")
	compareSynth(t, p, true, "charter", "synthesize", "--dry-run")
	compareSynth(t, p, true, "charter", "synthesize", "--json")
	compareSynth(t, p, true, "charter", "synthesize")
	compareSynth(t, p, true, "charter", "synthesize", "--prune", "--json")
	compareSynthFile(t, p, ".kittify/charter-packs/graph.yaml")
	compareSynth(t, p, true, "charter", "synthesize", "--prune")

	// Corrupt overlay.
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/charter-packs/graph.yaml", "schema_version: '2.0'\n") })
	compareSynth(t, p, false, "charter", "synthesize", "--json")
	compareSynth(t, p, true, "charter", "synthesize")
	compareSynth(t, p, true, "charter", "resynthesize", "--topic", "directive:PROJECT_001", "--json")

	// No manifest for resynthesize.
	p.Both(func(s *testkit.Sandbox) {
		s.Git("checkout", "-q", "--", ".")
		s.Exec("", "rm", ".kittify/charter/synthesis-manifest.yaml")
	})
	compareSynth(t, p, true, "charter", "resynthesize", "--topic", "quality_gates", "--json")
	compareSynth(t, p, true, "charter", "resynthesize", "--topic", "quality_gates")
}

func TestParityCharterSynthesizeDirectWrite(t *testing.T) {
	t.Parallel()
	p := newSynthPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter-packs/directive/my-rule.directive.yaml", "id: MY_RULE\nschema_version: \"1.0\"\ntitle: My rule\nintent: Keep it simple.\nenforcement: advisory\n")
		s.WriteFile(".kittify/charter-packs/tactic/my-tactic.tactic.yaml", "id: my-tactic\nschema_version: \"1.0\"\nname: My tactic\nsteps:\n  - title: Do it\n")
		s.WriteFile(".kittify/charter-packs/agent_profiles/helper.agent.yaml", "profile-id: helper\nname: Helper\nroles: [implementer]\npurpose: Help out.\nspecialization:\n  primary-focus: helping\n"+
			"directive-references:\n  - code: MY_RULE\n    name: My rule\n    rationale: because\ntactic-references:\n  - id: my-tactic\n    rationale: useful\n  - id: missing-tactic\n    rationale: nope\n")
	})
	compareSynth(t, p, true, "charter", "synthesize", "--dry-run", "--json")
	compareSynth(t, p, true, "charter", "synthesize", "--dry-run")
	compareSynth(t, p, true, "charter", "synthesize", "--json")
	for _, rel := range []string{".kittify/charter-packs/graph.yaml", ".kittify/charter-packs/PROVENANCE.md", ".kittify/charter/provenance/directive-my-rule.yaml",
		".kittify/charter/provenance/agent_profile-helper.yaml", ".kittify/charter/synthesis-manifest.yaml"} {
		compareSynthFile(t, p, rel)
	}
	compareSynth(t, p, true, "charter", "synthesize")
	compareSynth(t, p, true, "charter", "synthesize", "--json")
	// Registered artifacts are re-emitted alongside generated targets.
	p.Both(func(s *testkit.Sandbox) {
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "reg")
		for rel, body := range synthFiles {
			s.WriteFile(rel, body)
		}
	})
	compareSynth(t, p, true, "charter", "synthesize", "--json")
	compareSynthFile(t, p, ".kittify/charter-packs/graph.yaml")
	compareSynthFile(t, p, ".kittify/charter/synthesis-manifest.yaml")
	compareSynth(t, p, true, "charter", "synthesize")
	compareSynth(t, p, true, "charter", "resynthesize", "--list-topics")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter-packs/directive/bad.directive.yaml", "id: BAD\n")
	})
	compareSynth(t, p, true, "charter", "synthesize", "--json")
}

func TestParityCharterActivateRegistersDirectWrite(t *testing.T) {
	t.Parallel()
	p := newSynthPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter-packs/directive/my-rule.directive.yaml", "id: MY_RULE\nschema_version: \"1.0\"\ntitle: My rule\nintent: Keep it simple.\nenforcement: advisory\n")
		s.WriteFile(".kittify/charter-packs/agent_profiles/helper.agent.yaml", "profile-id: helper\nname: Helper\nroles: [implementer]\npurpose: Help out.\nspecialization:\n  primary-focus: helping\n"+
			"directive-references:\n  - code: MY_RULE\n    name: My rule\n    rationale: because\ntactic-references:\n  - id: missing-tactic\n    rationale: nope\n")
	})
	compareSynth(t, p, true, "charter", "activate", "paradigm", "trunk-based")
	for _, rel := range []string{".kittify/charter-packs/graph.yaml", ".kittify/charter/provenance/directive-my-rule.yaml",
		".kittify/charter/provenance/agent_profile-helper.yaml", ".kittify/charter/synthesis-manifest.yaml"} {
		compareSynthFile(t, p, rel)
	}
	p.Both(func(s *testkit.Sandbox) { s.Exec("", "rm", ".kittify/charter-packs/agent_profiles/helper.agent.yaml") })
	compareSynth(t, p, true, "charter", "activate", "paradigm", "trunk-based")
	compareSynthFile(t, p, ".kittify/charter/synthesis-manifest.yaml")
	compareSynthFile(t, p, ".kittify/charter-packs/graph.yaml")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/charter-packs/directive/bad.directive.yaml", "id: 1\n") })
	compareSynth(t, p, true, "charter", "activate", "paradigm", "trunk-based")
}

// TestCharterSynthesizeGoOnly exercises the pipeline without the oracle.
func TestCharterSynthesizeGoOnly(t *testing.T) {
	s := newProject(t)
	for _, args := range [][]string{{"charter", "interview", "--defaults", "--json"}, {"charter", "generate", "--json"}} {
		if r := s.Run(args...); r.Code != 0 {
			t.Fatalf("%v: %s %s", args, r.Stdout, r.Stderr)
		}
	}
	for rel, body := range synthFiles {
		s.WriteFile(rel, body)
	}
	r := s.Run("charter", "synthesize", "--json", "--skip-code-evidence", "--skip-corpus")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"result": "success"`) || !strings.Contains(r.Stdout, "review-policy-tactic") {
		t.Fatalf("synthesize: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	if !s.Exists(".kittify/charter-packs/graph.yaml") || !strings.Contains(s.ReadFile(".kittify/charter/synthesis-manifest.yaml"), "built_in_only: false") {
		t.Fatalf("outputs missing: %s", s.ReadFile(".kittify/charter/synthesis-manifest.yaml"))
	}
	r = s.Run("charter", "resynthesize", "--topic", "review_policy", "--json", "--skip-code-evidence", "--skip-corpus")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"matched_form": "interview_section"`) {
		t.Fatalf("resynthesize: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("charter", "resynthesize", "--topic", "nope")
	if r.Code != 2 || !strings.Contains(r.Stderr, "Cannot resolve --topic") {
		t.Fatalf("unresolved topic: %d %s %s", r.Code, r.Stdout, r.Stderr)
	}
}
