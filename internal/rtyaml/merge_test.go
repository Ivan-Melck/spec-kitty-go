package rtyaml

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyyaml"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const mergeSrc = `# Project config
agents:
  available: [claude, codex]   # who
  auto_commit: true
# vcs section
vcs:
  type: "git"   # quoted
spec_kitty:
  schema_version: 2  # old
  schema_capabilities:
    a: true
tracker:
  provider: 'beads'
`

// Expected outputs are ruamel.yaml 0.19 (work-PYF/rtmerge.py): YAML() with
// preserve_quotes, load, the same edit on the CommentedMap, dump.
func TestApplyMatchesRuamelEdits(t *testing.T) {
	cases := []struct {
		name string
		edit func(o *jsonx.Object)
		want string
	}{
		{"replace section", func(o *jsonx.Object) {
			o.Set("agents", jsonx.NewObject("available", []any{"claude"}, "auto_commit", true, "lint_on_edit", false))
		}, "# Project config\nagents:\n  available:\n  - claude\n  auto_commit: true\n  lint_on_edit: false\nvcs:\n  type: \"git\"   # quoted\nspec_kitty:\n  schema_version: 2  # old\n  schema_capabilities:\n    a: true\ntracker:\n  provider: 'beads'\n"},
		{"nested in place", func(o *jsonx.Object) {
			o.GetObject("spec_kitty").Set("schema_version", 3)
		}, "# Project config\nagents:\n  available: [claude, codex]   # who\n  auto_commit: true\n# vcs section\nvcs:\n  type: \"git\"   # quoted\nspec_kitty:\n  schema_version: 3  # old\n  schema_capabilities:\n    a: true\ntracker:\n  provider: 'beads'\n"},
		{"delete", func(o *jsonx.Object) { o.Delete("vcs") },
			"# Project config\nagents:\n  available: [claude, codex]   # who\n  auto_commit: true\n# vcs section\nspec_kitty:\n  schema_version: 2  # old\n  schema_capabilities:\n    a: true\ntracker:\n  provider: 'beads'\n"},
		{"append", func(o *jsonx.Object) { o.Set("mission_type_activations", []any{"software-dev", "research"}) },
			"# Project config\nagents:\n  available: [claude, codex]   # who\n  auto_commit: true\n# vcs section\nvcs:\n  type: \"git\"   # quoted\nspec_kitty:\n  schema_version: 2  # old\n  schema_capabilities:\n    a: true\ntracker:\n  provider: 'beads'\nmission_type_activations:\n- software-dev\n- research\n"},
		{"reassign equal", func(o *jsonx.Object) { o.Set("vcs", jsonx.NewObject("type", "git")) },
			"# Project config\nagents:\n  available: [claude, codex]   # who\n  auto_commit: true\n# vcs section\nvcs:\n  type: git\nspec_kitty:\n  schema_version: 2  # old\n  schema_capabilities:\n    a: true\ntracker:\n  provider: 'beads'\n"},
		{"string keeps quotes", func(o *jsonx.Object) { o.GetObject("tracker").Set("provider", "linear") },
			"# Project config\nagents:\n  available: [claude, codex]   # who\n  auto_commit: true\n# vcs section\nvcs:\n  type: \"git\"   # quoted\nspec_kitty:\n  schema_version: 2  # old\n  schema_capabilities:\n    a: true\ntracker:\n  provider: 'linear'\n"},
		{"delete and re-add", func(o *jsonx.Object) {
			o.Delete("agents")
			o.Set("agents", jsonx.NewObject("available", []any{}))
		}, "# Project config\nvcs:\n  type: \"git\"   # quoted\nspec_kitty:\n  schema_version: 2  # old\n  schema_capabilities:\n    a: true\ntracker:\n  provider: 'beads'\nagents:\n  available: []\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := yamlx.Parse([]byte(mergeSrc))
			if err != nil {
				t.Fatal(err)
			}
			cfg := v.(*jsonx.Object)
			snap := Snap(cfg)
			c.edit(cfg)
			doc, err := Load(pyyaml.StringInput(mergeSrc))
			if err != nil {
				t.Fatal(err)
			}
			doc.Apply(cfg, snap)
			got, err := doc.Dump()
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}
