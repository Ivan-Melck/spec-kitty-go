package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refProject returns an oracle-generated project directory named by
// SPEC_KITTY_REF_PROJECT (a dev aid for byte comparisons), or skips.
func refProject(t *testing.T) string {
	ref := os.Getenv("SPEC_KITTY_REF_PROJECT")
	if ref == "" {
		t.Skip("SPEC_KITTY_REF_PROJECT not set")
	}
	return ref
}

func refDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return "line " + itoa(i+1) + "\n  want: " + x + "\n  got:  " + y
		}
	}
	return "(equal lines)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestCommandSkillsMatchReference(t *testing.T) {
	ref := refProject(t)
	if _, err := os.Stat(filepath.Join(ref, ".agents/skills/spec-kitty.plan")); err != nil {
		t.Skip("reference has no command skills")
	}
	for _, c := range CommandSkillCommands {
		got, err := RenderCommandSkill(ref, c)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := os.ReadFile(filepath.Join(ref, ".agents/skills/spec-kitty."+c+"/SKILL.md"))
		if string(got) != string(want) {
			t.Errorf("%s differs: %s", c, refDiff(string(want), string(got)))
			if d := os.Getenv("SPEC_KITTY_DUMP"); d != "" {
				os.WriteFile(filepath.Join(d, c+".md"), got, 0o644)
			}
		}
	}
}
