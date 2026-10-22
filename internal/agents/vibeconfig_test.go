package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MAINT-15: .vibe/config.toml is read with the TOML parser (pytoml), not a
// regex. Expected values were produced by upstream
// skills.vibe_config.skill_path_configured / prepare_project_skill_path.
func TestVibeSkillPathMatchesUpstream(t *testing.T) {
	for i, c := range []struct {
		exists     bool
		text       string
		configured bool
		desired    string
		err        string
	}{
		{false, "", false, "skill_paths = [\".agents/skills\"]\n", ""},
		{true, "", false, "skill_paths = [\".agents/skills\"]\n", ""},
		{true, "model = \"x\"\n", false, "skill_paths = [\".agents/skills\"]\nmodel = \"x\"\n", ""},
		{true, "# comment\nskill_paths = \".agents/skills\"\n", true, "# comment\nskill_paths = \".agents/skills\"\n", ""},
		{true, "skill_paths = '.agents/skills' # literal\n", true, "skill_paths = '.agents/skills' # literal\n", ""},
		{true, "skill_paths = \"other\"\n", false, "skill_paths = [\"other\", \".agents/skills\"]\n", ""},
		{true, "skill_paths = [\n  \"a\", # first\n  \"b\"\n]\n[tools]\nx = 1\n", false, "skill_paths = [\n  \"a\", # first\n  \"b\"\n, \".agents/skills\"]\n[tools]\nx = 1\n", ""},
		{true, "skill_paths = []\n", false, "skill_paths = [\".agents/skills\"]\n", ""},
		{true, "skill_paths = [\"a\",]\n", false, "skill_paths = [\"a\", \".agents/skills\"]\n", ""},
		{true, "[section]\nskill_paths = [\".agents/skills\"]\n", false, "skill_paths = [\".agents/skills\"]\n[section]\nskill_paths = [\".agents/skills\"]\n", ""},
		{true, "skill_paths = [ \".agents/skills\" ]\n", true, "skill_paths = [ \".agents/skills\" ]\n", ""},
		{true, "skill_paths = 3\n", false, "", "Expected .vibe/config.toml skill_paths to be a string or list"},
		{true, "not toml = = \n", false, "", "Expected '=' after a key in a key/value pair (at line 1, column 5)"},
		{true, "title = \"\"\"multi\nskill_paths = [\".agents/skills\"]\n\"\"\"\n", false, "skill_paths = [\".agents/skills\"]\ntitle = \"\"\"multi\nskill_paths = [\".agents/skills\"]\n\"\"\"\n", ""},
		{true, "a = 1\r\nskill_paths = [\"x\"]\r\n", false, "a = 1\r\nskill_paths = [\"x\", \".agents/skills\"]\r\n", ""},
	} {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, ".vibe"), 0o755)
		if c.exists {
			os.WriteFile(filepath.Join(root, ".vibe", "config.toml"), []byte(c.text), 0o644)
		}
		if got := VibeSkillPathConfigured(root); got != c.configured {
			t.Errorf("case %d %q: configured=%v, want %v", i, c.text, got, c.configured)
		}
		p, err := PrepareVibeSkillPath(root)
		switch {
		case c.err != "":
			if err == nil {
				t.Errorf("case %d %q: want error %q", i, c.text, c.err)
			} else if strings.HasPrefix(c.err, "Expected .vibe") && err.Error() != c.err {
				t.Errorf("case %d: error %q, want %q", i, err, c.err)
			}
		case err != nil:
			t.Errorf("case %d %q: %v", i, c.text, err)
		case string(p.Desired) != c.desired:
			t.Errorf("case %d %q:\n got %q\nwant %q", i, c.text, p.Desired, c.desired)
		}
	}
}
