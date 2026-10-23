package charter

import (
	"testing"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestGovernanceIdentityAndDenialsMatchOracle(t *testing.T) {
	python := languageOraclePython(t)
	for _, scenario := range []string{"default", "unknown", "selected", "project-identity", "denied", "explicit-empty", "org-override"} {
		t.Run(scenario, func(t *testing.T) {
			s := testkit.NewSandbox(t)
			s.Git("init", "-q", "-b", "main")
			selection := "selected_paradigms: []\n    selected_directives: []"
			ledger := ""
			switch scenario {
			case "unknown":
				selection = "selected_paradigms: [unknown-paradigm]\n    selected_directives: [missing-directive]"
			case "selected":
				selection = "selected_paradigms: [deep-module-design]\n    selected_directives: [010-documentation]"
			case "project-identity":
				selection = "selected_paradigms: [local-paradigm]\n    selected_directives: [local-rule]"
				s.WriteFile(".kittify/charter-packs/paradigms/local-paradigm.paradigm.yaml", "id: declared-paradigm\nname: Local\nsummary: Fixture\n")
				s.WriteFile(".kittify/charter-packs/directive/local-rule.directive.yaml", "id: declared-rule\ntitle: Local\nintent: Fixture\n")
			case "denied":
				ledger = "deactivated_directives: [010-documentation]\ndeactivated_paradigms: [deep-module-design]\n"
				selection = "selected_paradigms: [deep-module-design]\n    selected_directives: [DIRECTIVE_010]"
			case "explicit-empty":
				ledger = "activated_directives: []\nactivated_paradigms: []\n"
			}
			s.WriteFile(".kittify/config.yaml", "charter: .kittify/charter/charter.yaml\n")
			s.WriteFile(".kittify/charter/charter.yaml", "governance:\n  charter:\n    "+selection+"\n")
			if ledger != "" {
				s.WriteFile(".kittify/charter/activation.yaml", ledger)
			}
			if scenario == "org-override" {
				s.WriteFile(".kittify/config.yaml", "charter: .kittify/charter/charter.yaml\ncharter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: orgpack\n")
				s.WriteFile("orgpack/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: acme\npack_level: org\npack_version: 1.2.3\n")
				s.WriteFile("orgpack/directives/010-x.directive.yaml", "id: DIRECTIVE_010\nschema_version: '1.0'\ntitle: Overridden\nintent: Org override\nenforcement: required\n")
				s.WriteFile("orgpack/org-charter.yaml", "schema_version: '2'\norg_name: Acme\nrequired_tactics: [acme-tactic]\n")
			}
			g, e := ResolveGovernance(s.Dir)
			if e != nil {
				t.Fatal(e)
			}
			got := jsonx.Dumps(jsonx.NewObject("paradigms", strsAny(g.Paradigms), "directives", strsAny(g.Directives), "diagnostics", strsAny(g.Diagnostics), "source", g.DirectivesSource), jsonx.SortedPretty) + "\n"
			script := "import json\nfrom pathlib import Path\nfrom charter.activation.resolver import resolve_project_governance\ng=resolve_project_governance(Path.cwd())\nprint(json.dumps(dict(paradigms=g.paradigms,directives=g.directives,diagnostics=g.diagnostics,source=g.metadata['directives_source']),sort_keys=True,indent=2))"
			oracle := s.Exec("", python, "-c", script)
			if oracle.Code != 0 {
				t.Fatal(oracle.Stderr)
			}
			if got != oracle.Stdout {
				t.Error(testkit.Diff(oracle.Stdout, got))
			}
		})
	}
}

func TestLayeredIdentityCollisionsMatchOracle(t *testing.T) {
	python := languageOraclePython(t)
	s := testkit.NewGitSandbox(t)
	s.WriteFile(".kittify/config.yaml", "charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: orgpack\n")
	s.WriteFile("orgpack/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: acme\npack_level: org\npack_version: 1.2.3\n")
	s.WriteFile("orgpack/directives/shared.directive.yaml", "id: org-rule\n")
	s.WriteFile(".kittify/charter-packs/directive/shared.directive.yaml", "id: project-rule\n")
	s.WriteFile(".kittify/charter-packs/directive/alias.directive.yaml", "id: org-rule\n")
	s.WriteFile(".kittify/charter-packs/directive/DIRECTIVE_010.directive.yaml", "id: shadowing-stem\n")
	ix := dct.NewIdentityIndex(s.Dir)
	var got []any
	tokens := []string{"shared", "project-rule", "org-rule", "alias", "DIRECTIVE_010", "010-documentation", "missing"}
	for _, token := range tokens {
		got = append(got, jsonx.NewObject("declared", ix.DeclaredID(dct.Directive, token), "selection", ix.SelectionID(dct.Directive, token), "stem", ix.Stem(dct.Directive, token), "layer", ix.SourceLayer(dct.Directive, token), "project", ix.DeclaredIDInLayer(dct.Directive, token, dct.LayerProject)))
	}
	script := "import json\nfrom pathlib import Path\nfrom charter.activation.identity_index import IdentityIndex\nfrom charter.offering.artifact_kinds import ArtifactKind\ni=IdentityIndex.build(Path.cwd()); k=ArtifactKind.DIRECTIVE\nprint(json.dumps([dict(declared=i.declared_id(k,t) or '',selection=i.selection_id(k,t) or '',stem=i.stem(k,t) or t,layer=i.source_layer(k,t) or '',project=i.declared_id_in_layer(k,t,'project') or '') for t in " + jsonx.Dumps(strsAny(tokens), jsonx.Line) + "],sort_keys=True,indent=2))"
	oracle := s.Exec("", python, "-c", script)
	if oracle.Code != 0 {
		t.Fatal(oracle.Stderr)
	}
	actual := jsonx.Dumps(got, jsonx.SortedPretty) + "\n"
	if actual != oracle.Stdout {
		t.Error(testkit.Diff(oracle.Stdout, actual))
	}
}
