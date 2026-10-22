package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// reYAMLParseErr masks ruamel vs yaml.v3 parse error texts.
var (
	reYAMLParseErr = regexp.MustCompile(`YAML parse error: [^"\n]*(\\n[^"]*)?`)
	reManifestVol  = regexp.MustCompile(`(?m)^(fetched_at|manifest_hash): .*$`)
)

func comparePackTool(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%v: exit go=%d oracle=%d\n%s\n%s", args, g.Code, o.Code, g.Stdout, o.Stdout)
	}
	mask := func(s string) string { return reYAMLParseErr.ReplaceAllString(s, "YAML parse error: <E>") }
	if gs, os_ := mask(p.Go.Normalize(g.Stdout)), mask(p.Oracle.Normalize(o.Stdout)); gs != os_ {
		t.Errorf("%v: stdout differs\n%s", args, testkit.Diff(os_, gs))
	}
}

func compareFileBoth(t *testing.T, p *testkit.Pair, rel string) {
	t.Helper()
	if p.Go.Exists(rel) != p.Oracle.Exists(rel) {
		t.Errorf("%s: exists go=%v oracle=%v", rel, p.Go.Exists(rel), p.Oracle.Exists(rel))
		return
	}
	if !p.Go.Exists(rel) {
		return
	}
	g := reManifestVol.ReplaceAllString(p.Go.ReadFile(rel), "$1: <V>")
	o := reManifestVol.ReplaceAllString(p.Oracle.ReadFile(rel), "$1: <V>")
	if g != o {
		t.Errorf("%s differs\n%s", rel, testkit.Diff(o, g))
	}
}

func TestParityCharterOrgInitAndValidate(t *testing.T) {
	t.Parallel()
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("tmpl/README.md", "# {{ORG_NAME}} pack at {{LOCAL_PATH}}\n")
		s.WriteFile("tmpl/sub/a.yaml", "x: {{ORG_NAME}}\n")
		s.WriteFile("tmpl/.templateignore", "*.tmp\nignored/\n# comment\n")
		s.WriteFile("tmpl/b.tmp", "junk\n")
		s.WriteFile("tmpl/ignored/z.txt", "z\n")
	})
	for _, args := range [][]string{
		{"charter", "org", "init", "out1"},
		{"charter", "org", "init", "out1"},
		{"charter", "org", "init", "out1", "--force"},
		{"charter", "org", "init", "out2", "--template", "tmpl"},
		{"charter", "org", "init", "out2", "--template", "tmpl", "--org-name", "acme"},
		{"charter", "org", "init", "out3", "--template", "tmpl", "--org-name", "Bad_Name"},
		{"charter", "org", "init", "out3", "--template", "tmpl", "--org-name", "doctrine-org"},
		{"charter", "org", "init", "out3", "--template", "tmpl", "--org-name", "acme", "--local-path", "TODO"},
		{"charter", "org", "init", "out3", "--template", "nope", "--org-name", "acme"},
		{"charter", "org", "init", "out3", "--template", "http://x/y", "--org-name", "acme"},
		{"charter", "org", "init", "out3", "--template", "https://u:p@x/y", "--org-name", "acme"},
		{"charter", "org", "init", "out3", "--template", "tmpl#main", "--org-name", "acme", "--branch", "dev"},
		{"charter", "org", "init", "out2", "--template", "tmpl", "--org-name", "acme"},
		{"charter", "org", "init", "out2", "--template", "tmpl", "--org-name", "acme2", "--force"},
		{"charter", "org", "validate", "out1"},
		{"charter", "org", "validate", "nope"},
		{"charter", "pack", "validate", "out1", "--json"},
		{"charter", "pack", "validate", "nope", "--json"},
	} {
		comparePackTool(t, p, args...)
	}
	for _, rel := range []string{"out1/org-charter.yaml", "out1/drg/fragment.yaml", "out1/presets/starter.yaml", "out1/README.md",
		"out2/README.md", "out2/sub/a.yaml", "out2/b.tmp", "out2/ignored/z.txt", "out2/.templateignore"} {
		compareFileBoth(t, p, rel)
	}
	// A pack exercising every validator leg.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("out1/directives/010-x.directive.yaml", "id: DIRECTIVE_010\nschema_version: \"1.0\"\ntitle: Overridden\nintent: Org override\nenforcement: required\n")
		s.WriteFile("out1/directives/bad.directive.yaml", "id: lower-bad\ntitle: x\n")
		s.WriteFile("out1/directives/dup.directive.yaml", "id: DIRECTIVE_010\nschema_version: \"1.0\"\ntitle: Dup\nintent: dup\nenforcement: advisory\n")
		s.WriteFile("out1/tactics/t.tactic.yaml", "id: acceptance-test-first\nschema_version: \"1.0\"\nname: T\nsteps:\n  - title: s\n")
		s.WriteFile("out1/agent_profiles/p.agent.yaml", "name: Broken\n")
		s.WriteFile("out1/assets/img/logo.asset.yaml", "id: logo\nmime: image/jpeg\npath: logo.png\n")
		s.WriteFile("out1/assets/esc.asset.yaml", "id: esc\nmime: text/plain\npath: ../outside.txt\n")
		s.WriteFile("out1/assets/badmime.asset.yaml", "id: badmime\nmime: plain\npath: x.txt\n")
		s.WriteFile("out1/drg/fragment.yaml", "nodes:\n  - id: acme-rule\n    kind: directives\nedges:\n  - source: acme-rule\n    target: nonexistent-thing\n    relation: requires\n  - source: acme-rule\n    target: tactic:elsewhere\n    relation: suggests\n  - source: acme-rule\n    target: DIRECTIVE_010\n    relation: overrides\n")
		s.WriteFile("out1/drg/extra.graph.yaml", "schema_version: \"1.0\"\ngenerated_at: x\ngenerated_by: y\nnodes:\n  - urn: tactic:acceptance-test-first\n    kind: tactic\n  - urn: directive:DIRECTIVE_001\n    kind: tactic\nedges:\n  - source: tactic:acceptance-test-first\n    target: directive:DIRECTIVE_404\n    relation: requires\n")
		s.WriteFile("out1/replaceable-builtins.yaml", "replaceable_builtins:\n  - urn: directive:DIRECTIVE_010\n  - urn: tactic:not-mine\n")
		s.WriteFile("out1/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: acme\n")
		s.WriteFile("out1/presets/bad.yaml", "name: wrong\ndescription: x\n")
		s.WriteFile("out1/presets/unres.yaml", "name: unres\ndescription: x\nactivated_kinds: [directives]\nactivated_directives: [nope-dir]\nactivated_tactics: [acceptance-test-first]\nmission_type_activations: [nonsense]\n")
		oc := strings.Replace(s.ReadFile("out1/org-charter.yaml"), "required_directives: []", "required_directives: [DIRECTIVE_010, missing-one]", 1)
		oc = strings.Replace(oc, "governance_policies: []", "governance_policies:\n- field: f\n  value: v\n  enforcement: required", 1)
		s.WriteFile("out1/org-charter.yaml", oc)
	})
	comparePackTool(t, p, "charter", "pack", "validate", "out1", "--json")
	comparePackTool(t, p, "charter", "pack", "validate", "out1")
	comparePackTool(t, p, "charter", "org", "validate", "out1")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("out1/org-charter.yaml", s.ReadFile("out1/org-charter.yaml")+"bogus: 1\n") })
	comparePackTool(t, p, "charter", "pack", "validate", "out1", "--json")
}

func writeAssemblePack(s *testkit.Sandbox, name string) {
	s.WriteFile(name+"/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: "+name+"\npack_level: org\n")
}

func TestParityCharterPackAssemble(t *testing.T) {
	t.Parallel()
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		writeAssemblePack(s, "a")
		writeAssemblePack(s, "b")
		s.WriteFile("a/tactics/ta.tactic.yaml", "id: tac-a\nschema_version: \"1.0\"\nname: A\nsteps:\n  - title: s\n")
		s.WriteFile("b/tactics/tb.tactic.yaml", "id: tac-b\nschema_version: \"1.0\"\nname: B\nsteps:\n  - title: s\n")
		s.WriteFile("a/org-charter.yaml", "schema_version: \"2\"\ninterview_defaults:\n  testing: pytest\nrequired_directives: [X1]\ngovernance_policies:\n  - field: f\n    value: v\n")
		s.WriteFile("b/org-charter.yaml", "interview_defaults:\n  testing: unittest\n  style: pep8\nrequired_directives: [X1, X2]\ngovernance_policies:\n  - field: f\n    value: v\n  - field: g\n    value: w\n")
	})
	comparePackTool(t, p, "charter", "pack", "assemble", "out", "a", "b")
	for _, rel := range []string{"out/org-charter.yaml", "out/pack-manifest.yaml", "out/tactics/ta.tactic.yaml", "out/pack.yaml"} {
		compareFileBoth(t, p, rel)
	}
	comparePackTool(t, p, "charter", "pack", "assemble", "out", "a", "b")
	comparePackTool(t, p, "charter", "pack", "assemble", "out", "a", "b", "--force", "--json")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("b/tactics/dup.tactic.yaml", s.ReadFile("a/tactics/ta.tactic.yaml")) })
	comparePackTool(t, p, "charter", "pack", "assemble", "out2", "a", "b")
	comparePackTool(t, p, "charter", "pack", "assemble", "out2", "a", "b", "--json", "--conflicts-out", "c.json")
	compareFileBoth(t, p, "c.json")
	comparePackTool(t, p, "charter", "pack", "assemble", "out2", "a", "b", "--force", "--conflicts-out", "c2.json")
	compareFileBoth(t, p, "c2.json")
	compareFileBoth(t, p, "out2/pack-manifest.yaml")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("notpack/f", "x\n") })
	comparePackTool(t, p, "charter", "pack", "assemble", "notpack", "a", "--force")
	comparePackTool(t, p, "charter", "pack", "assemble", "out3", "nope", "a")
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("a/replaceable-builtins.yaml", "replaceable_builtins: [{urn: directive:DIRECTIVE_001, reason: one}]\n")
		s.WriteFile("b/replaceable-builtins.yaml", "replaceable_builtins: [{urn: directive:DIRECTIVE_001, reason: two}]\n")
	})
	comparePackTool(t, p, "charter", "pack", "assemble", "out4", "a", "b")
	comparePackTool(t, p, "charter", "pack", "assemble", "out4", "a", "b", "--force")
	compareFileBoth(t, p, "out4/replaceable-builtins.yaml")
	p.Both(func(s *testkit.Sandbox) {
		s.Exec("", "rm", "-f", s.Dir+"/b/tactics/dup.tactic.yaml", s.Dir+"/a/replaceable-builtins.yaml", s.Dir+"/b/replaceable-builtins.yaml")
		g := "schema_version: \"1.0\"\ngenerated_at: x\ngenerated_by: y\nnodes: []\nedges:\n  - source: tactic:tac-a\n    target: directive:DIRECTIVE_001\n    relation: requires\n"
		s.WriteFile("a/drg/x.graph.yaml", g)
		s.WriteFile("b/drg/y.graph.yaml", g)
	})
	comparePackTool(t, p, "charter", "pack", "assemble", "out6", "a", "b")
	comparePackTool(t, p, "charter", "pack", "assemble", "out6", "a", "b", "--force")
	for _, rel := range []string{"out6/drg/001-a-x.graph.yaml", "out6/drg/002-b-y.graph.yaml", "out6/pack-manifest.yaml"} {
		compareFileBoth(t, p, rel)
	}
	comparePackTool(t, p, "charter", "pack", "regenerate-graph", "--pack-root", "a")
	comparePackTool(t, p, "charter", "pack", "regenerate-graph", "--pack-root", "nope", "--json")
}

func TestCharterPackToolsGoOnly(t *testing.T) {
	s := testkit.NewSandbox(t)
	if r := s.Run("charter", "org", "init", "pk"); r.Code != 0 {
		t.Fatalf("org init: %d %s", r.Code, r.Stdout)
	}
	r := s.Run("charter", "pack", "validate", "pk", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"ok": true`) {
		t.Fatalf("validate: %d %s", r.Code, r.Stdout)
	}
	s.WriteFile("pk/tactics/x.tactic.yaml", "id: x-tac\nschema_version: \"1.0\"\nname: X\nsteps:\n  - title: s\n")
	r = s.Run("charter", "pack", "assemble", "out", "pk", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"ok": true`) || !s.Exists("out/pack-manifest.yaml") {
		t.Fatalf("assemble: %d %s", r.Code, r.Stdout)
	}
	if r := s.Run("charter", "pack", "regenerate-graph", "--check", "--json"); r.Code != 0 || !strings.Contains(r.Stdout, `"status": "fresh"`) {
		t.Fatalf("regenerate-graph --check: %d %s", r.Code, r.Stdout)
	}
}
