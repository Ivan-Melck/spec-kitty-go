package setup

import (
	"bufio"
	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/toolsurface"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
	"os"
	"strings"
	"testing"
)

func migrationOraclePython(t *testing.T) string {
	t.Helper()
	bin := testkit.Oracle(t)
	file, err := os.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	line, _ := bufio.NewReader(file).ReadString('\n')
	if !strings.HasPrefix(line, "#!") {
		t.Fatal("oracle has no interpreter shebang")
	}
	return strings.TrimSpace(line[2:])
}

func migrationResultJSON(r migrationResult) string {
	list := func(values []string) []any {
		out := make([]any, len(values))
		for i, v := range values {
			out[i] = v
		}
		return out
	}
	return jsonx.Dumps(jsonx.NewObject("success", r.Success, "changes_made", list(r.Changes), "warnings", list(r.Warnings), "errors", list(r.Errors), "manual_review_required", r.ManualReview, "preserved_paths", list(r.Preserved), "partial_writes", []any{}), jsonx.Options{Indent: 2, SortKeys: true}) + "\n"
}

func TestDeliveryMigrationsMatchOracle(t *testing.T) {
	python := migrationOraclePython(t)
	cases := []struct{ name, id string }{
		{"narrow-owned", "4.0.0rc8_narrow_agent_gitignore"},
		{"narrow-operator", "4.0.0rc8_narrow_agent_gitignore"},
		{"legacy-owned", "4.0.0rc8_command_skill_hyphen_names"},
		{"legacy-edited", "4.0.0rc8_command_skill_hyphen_names"},
		{"legacy-unrecorded", "4.0.0rc8_command_skill_hyphen_names"},
		{"legacy-stalehash", "4.0.0rc8_command_skill_hyphen_names"},
		{"legacy-badmanifest", "4.0.0rc8_command_skill_hyphen_names"},
		{"antigravity-archive", "4.0.0rc8_retire_antigravity_workflows"},
		{"stale-templates", "4.0.0rc8_retire_stale_mission_template_forks"},
		{"intent-complete", "4.0.0rc8_project_intent_directive_rename"},
		{"intent-collision", "4.0.0rc8_project_intent_directive_rename"},
		{"intent-soft-refusal", "4.0.0rc8_project_intent_directive_rename"},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Both(func(s *testkit.Sandbox) {
				config := "agents:\n  available: [codex]\nmission_type_activations: [software-dev]\n"
				if scenario.name == "antigravity-archive" {
					config = "agents:\n  available: []\n"
				}
				s.WriteFile(".kittify/config.yaml", config)
				switch scenario.name {
				case "narrow-owned":
					s.WriteFile(".gitignore", agents.GitignoreMarker+"\n.claude/\n.codex/\n.agents/skills/\n")
					s.WriteFile(".claude/settings.json", "{\"team\":true}\n")
				case "narrow-operator":
					s.WriteFile(".gitignore", "# My own rules\n.claude/\n\n"+agents.GitignoreMarker+"\n.pi/\n")
				case "legacy-owned", "legacy-edited", "legacy-unrecorded", "legacy-stalehash", "legacy-badmanifest":
					canonical, e := agents.RenderCommandSkill(s.Dir, "plan")
					if e != nil {
						t.Fatal(e)
					}
					legacy, e := agents.LegacyCommandSkillRendering(canonical, "plan")
					if e != nil {
						t.Fatal(e)
					}
					content := string(legacy)
					if scenario.name == "legacy-edited" {
						content += "\nMy additions.\n"
					}
					rel := agents.LegacyCommandSkillPath("plan")
					s.WriteFile(rel, content)
					entry := toolsurface.CommandEntry{Path: rel, ContentHash: toolsurface.Digest(legacy), Agents: []string{"codex"}, InstalledAt: "2026-10-23T00:00:00+00:00", SpecKittyVersion: "4.0.0rc8"}
					if scenario.name == "legacy-stalehash" {
						entry.ContentHash = strings.Repeat("0", 64)
					}
					if scenario.name == "legacy-badmanifest" {
						entry.ContentHash = "sha256:previous"
					}
					if scenario.name != "legacy-unrecorded" {
						s.WriteFile(".kittify/command-skills-manifest.json", string(toolsurface.SerializeCommandManifest([]toolsurface.CommandEntry{entry})))
					}
				case "stale-templates":
					for _, item := range [][2]string{
						{".kittify/missions/research/templates/task-prompt-template.md", "specify_cli/missions/research/templates/task-prompt-template.md"},
						{".kittify/overrides/missions/documentation/templates/tasks-template.md", "specify_cli/missions/documentation/templates/tasks-template.md"},
						{".kittify/missions/documentation/templates/spec-template.md", "specify_cli/missions/documentation/templates/spec-template.md"},
					} {
						content, err := assets.ReadFile(item[1])
						if err != nil {
							t.Fatal(err)
						}
						s.WriteFile(item[0], string(content))
					}
					s.WriteFile(".kittify/overrides/missions/research/templates/research-template.md", "My custom template.\n")
				case "intent-complete", "intent-collision", "intent-soft-refusal":
					const old = "007-mission-type-scope-directive.directive.yaml"
					s.WriteFile(".kittify/charter-packs/directive/"+old, "id: PROJECT_007\nbody: Preserve this content.\n")
					s.WriteFile(".kittify/charter/generated/directives/"+old, "id: PROJECT_007\nbody: Generated input.\n")
					s.WriteFile(".kittify/charter/provenance/directive-mission-type-scope-directive.yaml", "artifact_slug: mission-type-scope-directive\nsource_section: mission_type\nsource_input_ids:\n- .kittify/charter/generated/directives/"+old+"\n")
					s.WriteFile(".kittify/charter-packs/graph.yaml", "nodes:\n- urn: directive:PROJECT_007\n  kind: directive\n  label: Mission Type Scope Directive\nedges:\n- source: directive:PROJECT_007\n  target: mission_type:software-dev\n  relation: requires\n")
					s.WriteFile(".kittify/config.yaml", config+"activated_directives: [007-mission-type-scope-directive, 007-project-intent-directive]\n")
					manifest := &charter.SynthManifest{SchemaVersion: "3", CreatedAt: "2026-10-23T00:00:00+00:00", RunID: "rename-test", AdapterID: "generated", AdapterVersion: "4.0.0rc8", SynthesizerVersion: "4.0.0rc8", BuiltInOnly: true, Artifacts: []charter.ManifestEntry{{Kind: "directive", Slug: "mission-type-scope-directive", Path: ".kittify/charter-packs/directive/" + old, ProvenancePath: ".kittify/charter/provenance/directive-mission-type-scope-directive.yaml", ContentHash: strings.Repeat("0", 64)}}}
					s.WriteFile(".kittify/charter/synthesis-manifest.yaml", yamlx.Emit(manifest.Finalize().Object(true)))
					if scenario.name == "intent-collision" {
						s.WriteFile(".kittify/charter-packs/directive/007-project-intent-directive.directive.yaml", "id: PROJECT_007\nbody: Already exists.\n")
					}
					if scenario.name == "intent-soft-refusal" {
						s.WriteFile(".kittify/charter/synthesis-manifest.yaml", "[]\n")
					}
				case "antigravity-archive":
					s.WriteFile(".agent/workflows/spec-kitty.plan.md", "<!-- spec-kitty-command-version: 4.0.0rc7 -->\n\nManaged body with possible edits.\n")
					s.WriteFile(".agent/workflows/spec-kitty.custom.md", "<!-- spec-kitty-command-version: 4.0.0rc7 -->\nPrivate command.\n")
				}
			})
			m, ok := migrationByID(scenario.id)
			if !ok {
				t.Fatal("unknown migration")
			}
			for _, dry := range []bool{true, false} {
				r, err := m.applyErr(p.Go.Dir, dry)
				if err != nil {
					t.Fatal(err)
				}
				script := "import json,dataclasses\nfrom pathlib import Path\nfrom specify_cli.upgrade.migrations import auto_discover_migrations\nfrom specify_cli.upgrade.registry import MigrationRegistry\nauto_discover_migrations()\nm=MigrationRegistry.get_by_id(" + jsonx.Dumps(scenario.id, jsonx.Line) + ")\nprint(json.dumps(dataclasses.asdict(m.apply(Path.cwd(),dry_run=" + map[bool]string{true: "True", false: "False"}[dry] + ")),indent=2,sort_keys=True,ensure_ascii=False))\n"
				o := p.Oracle.Exec("", python, "-c", script)
				if o.Code != 0 {
					t.Fatalf("oracle apply: %s", o.Stderr)
				}
				got, want := p.Go.Normalize(migrationResultJSON(r)), p.Oracle.Normalize(o.Stdout)
				if got != want {
					t.Errorf("dry=%t result differs:\n%s", dry, testkit.Diff(want, got))
				}
				if strings.HasPrefix(scenario.name, "narrow-") {
					if g, o := p.Go.ReadFile(".gitignore"), p.Oracle.ReadFile(".gitignore"); g != o {
						t.Errorf("gitignore differs: %s", testkit.Diff(o, g))
					}
				}
				p.CompareTree(".kittify", true)
				p.CompareTree(".agents", true)
				p.CompareTree(".agent", true)
			}
		})
	}
}

// Preserve user-owned legacy and canonical content while diagnosing the managed
// leftovers that a repair can retire.
func TestCommandDeliveryDiagnosticsMatchOracle(t *testing.T) {
	for _, scenario := range []string{"recorded", "unrecorded", "edited", "folder", "collision", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			p := testkit.NewPair(t)
			p.Compare(false, "init", ".", "--ai", "codex", "--non-interactive")
			p.Both(func(s *testkit.Sandbox) {
				content, err := agents.RenderCommandSkill(s.Dir, "plan")
				if err != nil {
					t.Fatal(err)
				}
				legacy, err := agents.LegacyCommandSkillRendering(content, "plan")
				if err != nil {
					t.Fatal(err)
				}
				rel := agents.LegacyCommandSkillPath("plan")
				switch scenario {
				case "collision":
					s.WriteFile(agents.CommandSkillPath("plan"), "User skill.\n")
					entries, err := toolsurface.LoadCommandManifest(s.Dir)
					if err != nil {
						t.Fatal(err)
					}
					var kept []toolsurface.CommandEntry
					for _, e := range entries {
						if e.Path != agents.CommandSkillPath("plan") {
							kept = append(kept, e)
						}
					}
					s.WriteFile(".kittify/command-skills-manifest.json", string(toolsurface.SerializeCommandManifest(kept)))
				case "malformed":
					s.WriteFile(".kittify/command-skills-manifest.json", "{\"schema_version\":1,\"entries\":[{}]}\n")
				case "folder":
					s.WriteFile(".agents/skills/spec-kitty.plan/notes.txt", "User notes.\n")
				default:
					if scenario == "edited" {
						legacy = append(legacy, []byte("User additions.\n")...)
					}
					s.WriteFile(rel, string(legacy))
					if scenario == "recorded" {
						entries, err := toolsurface.LoadCommandManifest(s.Dir)
						if err != nil {
							t.Fatal(err)
						}
						entries = append(entries, toolsurface.CommandEntry{Path: rel, ContentHash: toolsurface.Digest(legacy), Agents: []string{"codex"}, InstalledAt: "2026-10-23T00:00:00+00:00", SpecKittyVersion: "4.0.0rc8"})
						s.WriteFile(".kittify/command-skills-manifest.json", string(toolsurface.SerializeCommandManifest(entries)))
					}
				}
			})
			p.Compare(false, "doctor", "skills", "--json")
			p.Compare(false, "doctor", "skills")
			if scenario != "malformed" {
				p.Compare(false, "doctor", "tool-surfaces", "--kind", "command-skill", "--json")
				p.Compare(false, "doctor", "skills", "--fix", "--json")
				p.CompareTree(".agents", true)
				p.CompareTree(".kittify/command-skills-manifest.json", true)
			}
		})
	}
}
