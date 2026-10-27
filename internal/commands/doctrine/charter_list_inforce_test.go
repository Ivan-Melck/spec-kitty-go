package doctrine_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// #6417: charter list names the org-required ids an absent allow list
// leaves in force (only ids some layer provides), labels the skill kind's
// absent key as org-required only, and flags an unreadable required floor.
func TestParityCharterListRequiredInForce(t *testing.T) {
	const acme = "charter_packs:\n  org:\n    packs:\n      - name: acme\n        local_path: org-packs/acme\n"
	const team = "charter_packs:\n  org:\n    packs:\n      - name: team-pack\n        local_path: .kittify/team-pack\n"
	const ledger = "mission_type_activations: [software-dev]\n"
	scenarios := map[string]func(s *testkit.Sandbox){
		"provided": func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/config.yaml", acme)
			s.WriteFile("org-packs/acme/org-charter.yaml", "schema_version: 1\nrequired_directives: [DIRECTIVE_001]\n")
			s.WriteFile(".kittify/charter/activation.yaml", ledger)
		},
		"unprovided-id": func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/config.yaml", acme)
			s.WriteFile("org-packs/acme/org-charter.yaml", "schema_version: 1\nrequired_directives: [DIRECTIVE_001, DIRECTIVE_999]\nrequired_tactics: [common-docs-scaffold]\n")
			s.WriteFile(".kittify/charter/activation.yaml", ledger)
		},
		"pack-own-directive": func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/config.yaml", team)
			s.WriteFile(".kittify/team-pack/pack.yaml", "pack_id: 01M4P01D5TWB5FKZGNNQG3900A\nparent_pack: null\nname: team-pack\npack_level: org\nactive_charter: org-charter.yaml\npack_drg: drg/fragment.yaml\n")
			s.WriteFile(".kittify/team-pack/org-charter.yaml", "org_name: team\nskill_namespace: team\nrequired_directives:\n  - team-rule\n")
			s.WriteFile(".kittify/team-pack/drg/fragment.yaml", "pack_name: team-pack\nsource_kind: local_path\nsource_ref: .kittify/team-pack\nlayer_index: 1\nprovenance_marker: org\n"+
				"nodes:\n  - id: team-rule\n    kind: directives\n    title: Team rule\n    body_path: directives/team-rule.directive.yaml\n")
			s.WriteFile(".kittify/team-pack/directives/team-rule.directive.yaml", "schema_version: \"1.0\"\nid: TEAM_RULE\ntitle: Team rule\nscope: docs\nintent: >\n  the team-wide default\nenforcement: required\n")
			s.WriteFile(".kittify/charter/activation.yaml", ledger)
		},
		"unreadable-floor": func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/config.yaml", acme)
			s.WriteFile("org-packs/acme/org-charter.yaml", "required_directives: [unclosed\n")
			s.WriteFile(".kittify/charter/activation.yaml", ledger)
		},
		"explicit-allow-list": func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/config.yaml", acme)
			s.WriteFile("org-packs/acme/org-charter.yaml", "schema_version: 1\nrequired_directives: [DIRECTIVE_001]\n")
			s.WriteFile(".kittify/charter/activation.yaml", ledger+"activated_directives: [003-decision-documentation-requirement]\n")
		},
		"no-org-pack": func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/charter/activation.yaml", ledger)
		},
	}
	for name, setup := range scenarios {
		t.Run(name, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(setup)
			for _, args := range [][]string{{"charter", "list", "--json"}, {"charter", "list"}} {
				g, o := p.Run(args...)
				if g.Code != o.Code {
					t.Errorf("%s: exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
				}
				gs, os := p.Go.Normalize(g.Stdout), p.Oracle.Normalize(o.Stdout)
				if gs != os {
					t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(os, gs))
				}
			}
		})
	}
}

// charter list keeps its --repo-root as given (default ".", never walked
// up): an unknown activation-ledger key names the cwd-relative ledger path,
// and from a subdirectory the project's ledger is not read at all.
func TestParityCharterListUnknownLedgerKey(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter/activation.yaml", "mission_type_activations: [software-dev]\nactivated_directivess: []\n")
		s.WriteFile("sub/keep.txt", "x\n")
	})
	for _, dir := range []string{"", "sub"} {
		for _, args := range [][]string{{"charter", "list", "--json"}, {"charter", "list"}} {
			g, o := p.RunIn(dir, args...)
			if g.Code != o.Code {
				t.Errorf("%s (in %q): exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", strings.Join(args, " "), dir, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			}
			if gs, os := p.Go.Normalize(g.Stdout), p.Oracle.Normalize(o.Stdout); gs != os {
				t.Errorf("%s (in %q): stdout differs\n%s", strings.Join(args, " "), dir, testkit.Diff(os, gs))
			}
		}
	}
}
